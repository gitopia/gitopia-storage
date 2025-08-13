package handler

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventPackfileUpdatedType = "gitopia.gitopia.storage.EventPackfileUpdated"

type PackfileUpdatedEvent struct {
	RepositoryId uint64
	NewCid       string
	OldCid       string
	NewName      string
	OldName      string
	Deleted      bool
}

func (e *PackfileUpdatedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventPackfileUpdatedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	newCid, err := jsonparser.GetString(eventBuf, "events", EventPackfileUpdatedType+"."+"new_cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing new cid")
	}
	newCid = strings.Trim(newCid, "\"")

	oldCid, err := jsonparser.GetString(eventBuf, "events", EventPackfileUpdatedType+"."+"old_cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing old cid")
	}
	oldCid = strings.Trim(oldCid, "\"")

	newName, err := jsonparser.GetString(eventBuf, "events", EventPackfileUpdatedType+"."+"new_name", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing new name")
	}
	newName = strings.Trim(newName, "\"")

	oldName, err := jsonparser.GetString(eventBuf, "events", EventPackfileUpdatedType+"."+"old_name", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing old name")
	}
	oldName = strings.Trim(oldName, "\"")

	deleted, err := jsonparser.GetBoolean(eventBuf, "events", EventPackfileUpdatedType+"."+"deleted", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing deleted field")
	}

	e.RepositoryId = repoId
	e.NewCid = newCid
	e.OldCid = oldCid
	e.NewName = newName
	e.OldName = oldName
	e.Deleted = deleted

	return nil
}

type PackfileUpdatedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewPackfileUpdatedEventHandler(g *app.GitopiaProxy, pinataClient *PinataClient) PackfileUpdatedEventHandler {
	return PackfileUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *PackfileUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &PackfileUpdatedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *PackfileUpdatedEventHandler) Process(ctx context.Context, event PackfileUpdatedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"new_cid":       event.NewCid,
		"old_cid":       event.OldCid,
		"deleted":       event.Deleted,
	}).Info("processing packfile updated event")

	if event.NewCid != "" && !event.Deleted {
		cacheDir := viper.GetString("GIT_REPOS_DIR")

		// cache repo
		utils.LockRepository(event.RepositoryId)
		defer utils.UnlockRepository(event.RepositoryId)

		if err := utils.CacheRepository(event.RepositoryId, cacheDir); err != nil {
			return err
		}

		packfilePath := path.Join(cacheDir, fmt.Sprintf("%v.git/objects/pack/", event.RepositoryId), event.NewName)
		resp, err := h.pinataClient.PinFile(ctx, packfilePath, filepath.Base(packfilePath))
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to pin file to Pinata")
			// Don't fail the process, just log the error
		} else {
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"packfile_name": event.NewName,
				"cid":           event.NewCid,
				"pinata_id":     resp.Data.ID,
			}).Info("successfully pinned to Pinata")
		}
	}

	// Unpin old packfile from Pinata
	if event.OldCid != "" && event.OldCid != event.NewCid {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.OldCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
			return err
		}

		if refCount == 0 {
			err := h.pinataClient.UnpinFile(ctx, event.OldName)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to unpin file from Pinata")
			}
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"old_name":      event.OldName,
				"old_cid":       event.OldCid,
			}).Info("unpinned file from Pinata")
		}
	}

	// Handle deletion case - unpin the packfile from Pinata if deleted and no references exist
	if event.Deleted {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.NewCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
			return err
		}

		if refCount == 0 {
			err := h.pinataClient.UnpinFile(ctx, event.NewName)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.NewName,
					"cid":           event.NewCid,
				}).WithError(err).Error("failed to unpin packfile from Pinata")
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.NewName,
					"cid":           event.NewCid,
				}).Info("successfully unpinned packfile from Pinata")
			}
		}
	}

	return nil
}
