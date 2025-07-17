package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventLFSObjectDeletedType = "gitopia.gitopia.storage.EventLFSObjectDeleted"

type LfsObjectDeletedEvent struct {
	RepositoryId uint64
	Oid          string
	Cid          string
}

func (e *LfsObjectDeletedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectDeletedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	oid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectDeletedType+"."+"oid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing oid")
	}
	oid = strings.Trim(oid, "\"")

	cid, err := jsonparser.GetString(eventBuf, "events", EventLFSObjectDeletedType+"."+"cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cid")
	}
	cid = strings.Trim(cid, "\"")

	e.RepositoryId = repoId
	e.Oid = oid
	e.Cid = cid

	return nil
}

type LfsObjectDeletedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewLfsObjectDeletedEventHandler(g *app.GitopiaProxy) LfsObjectDeletedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return LfsObjectDeletedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *LfsObjectDeletedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

	event := &LfsObjectDeletedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *LfsObjectDeletedEventHandler) Process(ctx context.Context, event LfsObjectDeletedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"oid":           event.Oid,
		"cid":           event.Cid,
	}).Info("processing lfs object deleted event")

	// Unpin from Pinata if enabled
	if h.pinataClient != nil && event.Oid != "" {
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

	return nil
}
