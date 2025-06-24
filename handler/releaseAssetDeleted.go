package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventReleaseAssetDeletedType = "gitopia.gitopia.storage.EventReleaseAssetDeleted"

type ReleaseAssetDeletedEvent struct {
	RepositoryId uint64
	Tag          string
	Name         string
	Cid          string
	Sha256       string
}

func (e *ReleaseAssetDeletedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetDeletedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	tag, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetDeletedType+"."+"tag", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tag")
	}
	tag = strings.Trim(tag, "\"")

	name, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetDeletedType+"."+"name", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing name")
	}
	name = strings.Trim(name, "\"")

	cid, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetDeletedType+"."+"cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cid")
	}
	cid = strings.Trim(cid, "\"")

	sha256, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetDeletedType+"."+"sha256", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing sha256")
	}
	sha256 = strings.Trim(sha256, "\"")

	e.RepositoryId = repoId
	e.Tag = tag
	e.Name = name
	e.Cid = cid
	e.Sha256 = sha256

	return nil
}

type ReleaseAssetDeletedEventHandler struct {
	gc           app.GitopiaProxy
	pinataClient *PinataClient
}

func NewReleaseAssetDeletedEventHandler(g app.GitopiaProxy) ReleaseAssetDeletedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return ReleaseAssetDeletedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *ReleaseAssetDeletedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

	event := &ReleaseAssetDeletedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *ReleaseAssetDeletedEventHandler) Process(ctx context.Context, event ReleaseAssetDeletedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"tag":           event.Tag,
		"name":          event.Name,
		"cid":           event.Cid,
	}).Info("processing release asset deleted event")

	// Unpin old packfile from Pinata if enabled
	refCount, err := h.gc.StorageCidReferenceCount(ctx, event.Cid)
	if err != nil {
		logger.FromContext(ctx).WithError(err).Error("failed to get attachment reference count")
		return err
	}
	if refCount == 0 {
		if h.pinataClient != nil && event.Cid != "" {
			name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, event.Name, event.Sha256)
			err := h.pinataClient.UnpinFile(ctx, name)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
					"name":          event.Name,
					"cid":           event.Cid,
				}).WithError(err).Error("failed to unpin file from Pinata")
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
					"name":          event.Name,
					"cid":           event.Cid,
				}).Info("successfully unpinned file from Pinata")
			}
		}
	}

	return nil
}
