package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventPackfileDeletedType = "gitopia.gitopia.storage.EventPackfileDeleted"

type PackfileDeletedEvent struct {
	RepositoryId uint64
	Name         string
	Cid          string
}

func (e *PackfileDeletedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventPackfileDeletedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	name, err := jsonparser.GetString(eventBuf, "events", EventPackfileDeletedType+"."+"name", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing name")
	}
	name = strings.Trim(name, "\"")

	cid, err := jsonparser.GetString(eventBuf, "events", EventPackfileDeletedType+"."+"cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cid")
	}
	cid = strings.Trim(cid, "\"")

	e.RepositoryId = repoId
	e.Name = name
	e.Cid = cid

	return nil
}

type PackfileDeletedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewPackfileDeletedEventHandler(g *app.GitopiaProxy) PackfileDeletedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return PackfileDeletedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *PackfileDeletedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

	event := &PackfileDeletedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *PackfileDeletedEventHandler) Process(ctx context.Context, event PackfileDeletedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"name":          event.Name,
		"cid":           event.Cid,
	}).Info("processing packfile deleted event")

	// Unpin the packfile from Pinata if enabled
	refCount, err := h.gc.StorageCidReferenceCount(ctx, event.Cid)
	if err != nil {
		logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
		return err
	}

	if refCount == 0 {
		if h.pinataClient != nil && event.Cid != "" {
			err := h.pinataClient.UnpinFile(ctx, event.Name)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.Name,
					"cid":           event.Cid,
				}).WithError(err).Error("failed to unpin packfile from Pinata")
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.Name,
					"cid":           event.Cid,
				}).Info("successfully unpinned packfile from Pinata")
			}
		}
	}

	return nil
}
