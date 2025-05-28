package handler

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia-go/logger"
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

	e.RepositoryId = repoId
	e.NewCid = newCid
	e.OldCid = oldCid
	e.NewName = newName
	e.OldName = oldName

	return nil
}

type PackfileUpdatedEventHandler struct {
	gc           app.GitopiaProxy
	pinataClient *PinataClient
}

func NewPackfileUpdatedEventHandler(g app.GitopiaProxy) PackfileUpdatedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return PackfileUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *PackfileUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

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
	}).Info("processing packfile updated event")

	// Pin to Pinata if enabled
	if h.pinataClient != nil && event.NewCid != "" {
		cacheDir := viper.GetString("GIT_DIR")

		// check if repo is cached
		isCached, err := utils.IsRepoCached(event.RepositoryId, cacheDir)
		if err != nil {
			return err
		}
		if !isCached {
			err = utils.DownloadRepo(event.RepositoryId, cacheDir)
			if err != nil {
				return err
			}
		}

		packfilePath := path.Join(cacheDir, fmt.Sprintf("%v.git/objects/pack/", event.RepositoryId), event.NewName)
		resp, err := h.pinataClient.PinFile(ctx, packfilePath, filepath.Base(packfilePath))
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to pin file to Pinata")
			// Don't fail the process, just log the error
		} else {
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"cid":       event.NewCid,
				"pinata_id": resp.Data.ID,
			}).Info("successfully pinned to Pinata")
		}
	}

	// Unpin old packfile from Pinata if enabled
	if h.pinataClient != nil && event.OldCid != "" && event.OldCid != event.NewCid {
		err := h.pinataClient.UnpinFile(ctx, event.OldName)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to unpin file from Pinata")
		}
	}
	return nil
}
