package handler

import (
	"context"
	"path"
	"strconv"

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
}

func (e *LfsObjectUpdatedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	oid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"oid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing oid")
	}

	cid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectUpdatedType+"."+"cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cid")
	}

	e.RepositoryId = repoId
	e.Oid = oid
	e.Cid = cid

	return nil
}

type LfsObjectUpdatedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewLfsObjectUpdatedEventHandler(g *app.GitopiaProxy) LfsObjectUpdatedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return LfsObjectUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *LfsObjectUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

	event := &LfsObjectUpdatedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *LfsObjectUpdatedEventHandler) Process(ctx context.Context, event LfsObjectUpdatedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"oid":           event.Oid,
		"cid":           event.Cid,
	}).Info("processing lfs object updated event")

	// Pin to Pinata if enabled
	if h.pinataClient != nil && event.Cid != "" {
		cacheDir := viper.GetString("LFS_DIR")

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

	return nil
}
