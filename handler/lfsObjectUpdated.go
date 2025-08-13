package handler

import (
	"context"
	"path"
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

const EventLFSObjectUpdatedType = "gitopia.gitopia.storage.EventLFSObjectUpdated"

type LfsObjectUpdatedEvent struct {
	RepositoryId uint64
	Oid          string
	Cid          string
	Deleted      bool
}

func (e *LfsObjectUpdatedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	oid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"oid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing oid")
	}
	oid = strings.Trim(oid, "\"")

	cid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cid")
	}
	cid = strings.Trim(cid, "\"")

	deleted, err := jsonparser.GetBoolean(eventBuf, "events", EventLFSObjectUpdatedType+"."+"deleted", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing deleted")
	}

	e.RepositoryId = repoId
	e.Oid = oid
	e.Cid = cid
	e.Deleted = deleted

	return nil
}

type LfsObjectUpdatedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewLfsObjectUpdatedEventHandler(g *app.GitopiaProxy, pinataClient *PinataClient) LfsObjectUpdatedEventHandler {
	return LfsObjectUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *LfsObjectUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &LfsObjectUpdatedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *LfsObjectUpdatedEventHandler) Process(ctx context.Context, event LfsObjectUpdatedEvent) error {
	if event.Deleted {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"repository_id": event.RepositoryId,
			"oid":           event.Oid,
			"cid":           event.Cid,
		}).Info("processing lfs object deleted event")

		// Unpin from Pinata
		if event.Oid != "" {
			err := h.pinataClient.UnpinFile(ctx, event.Oid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to unpin file from Pinata")
				// Don't fail the process, just log the error
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"oid":           event.Oid,
					"cid":           event.Cid,
				}).Info("successfully unpinned from Pinata")
			}
		}
	} else {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"repository_id": event.RepositoryId,
			"oid":           event.Oid,
			"cid":           event.Cid,
		}).Info("processing lfs object updated event")

		// Pin to Pinata
		if event.Cid != "" {
			cacheDir := viper.GetString("LFS_OBJECTS_DIR")

			// check if lfs object is cached
			cached, err := utils.IsLFSObjectCached(event.Oid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to check if lfs object is cached")
			}
			if !cached {
				err := utils.DownloadLFSObject(event.Cid, event.Oid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to cache lfs object")
				}
			}

			lfsObjectPath := path.Join(cacheDir, event.Oid)
			resp, err := h.pinataClient.PinFile(ctx, lfsObjectPath, event.Oid)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"oid":           event.Oid,
					"cid":           event.Cid,
				}).WithError(err).Error("failed to pin file to Pinata")
				// Don't fail the process, just log the error
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"oid":           event.Oid,
					"cid":           event.Cid,
					"pinata_id":     resp.Data.ID,
				}).Info("successfully pinned to Pinata")
			}
		}
	}

	return nil
}
