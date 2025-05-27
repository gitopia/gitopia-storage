package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/gitopia-go/logger"
	pinclient "github.com/ipfs/boxo/pinning/remote/client"
	"github.com/ipfs/go-cid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventPackfileUpdatedType = "gitopia.gitopia.storage.EventPackfileUpdated"

type PackfileUpdatedEvent struct {
	RepositoryId uint64
	NewCid       string
	OldCid       string
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

	e.RepositoryId = repoId
	e.NewCid = newCid
	e.OldCid = oldCid

	return nil
}

type PackfileUpdatedEventHandler struct {
	gc                   app.GitopiaProxy
	pinningServiceClient *pinclient.Client
}

func NewPackfileUpdatedEventHandler(g app.GitopiaProxy) PackfileUpdatedEventHandler {
	var pinningClient *pinclient.Client
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinningClient = pinclient.NewClient(viper.GetString("PINNING_SERVICE_API_URL"), viper.GetString("PINNING_SERVICE_API_ACCESS_TOKEN"))
	}
	return PackfileUpdatedEventHandler{
		gc:                   g,
		pinningServiceClient: pinningClient,
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

	// Pin to external service if enabled
	if h.pinningServiceClient != nil && event.NewCid != "" {
		newCid, err := cid.Decode(event.NewCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to decode CID")
			// Don't fail the process, just log the error
		} else {
			_, err = h.pinningServiceClient.Add(ctx, newCid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to pin file to external service")
				// Don't fail the process, just log the error
			} else {
				logger.FromContext(ctx).WithField("cid", event.NewCid).Info("successfully pinned to external service")
			}
		}
	}

	return nil
}
