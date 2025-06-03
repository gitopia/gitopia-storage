package handler

import (
	"context"
	"fmt"
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

const EventReleaseAssetUpdatedType = "gitopia.gitopia.storage.EventReleaseAssetUpdated"

type ReleaseAssetUpdatedEvent struct {
	RepositoryId uint64
	Tag          string
	Name         string
	NewCid       string
	OldCid       string
	NewSha256    string
	OldSha256    string
}

func (e *ReleaseAssetUpdatedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	tag, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"tag", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tag")
	}
	tag = strings.Trim(tag, "\"")

	name, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"name", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing name")
	}
	name = strings.Trim(name, "\"")

	newCid, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"new_cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing new cid")
	}
	newCid = strings.Trim(newCid, "\"")

	oldCid, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"old_cid", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing old cid")
	}
	oldCid = strings.Trim(oldCid, "\"")

	newSha256, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"new_sha256", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing new name")
	}
	newSha256 = strings.Trim(newSha256, "\"")

	oldSha256, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetUpdatedType+"."+"old_sha256", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing old name")
	}
	oldSha256 = strings.Trim(oldSha256, "\"")

	e.RepositoryId = repoId
	e.Tag = tag
	e.Name = name
	e.NewCid = newCid
	e.OldCid = oldCid
	e.NewSha256 = newSha256
	e.OldSha256 = oldSha256

	return nil
}

type ReleaseAssetUpdatedEventHandler struct {
	gc           app.GitopiaProxy
	pinataClient *PinataClient
}

func NewReleaseAssetUpdatedEventHandler(g app.GitopiaProxy) ReleaseAssetUpdatedEventHandler {
	var pinataClient *PinataClient
	if viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		pinataClient = NewPinataClient(viper.GetString("PINATA_JWT"))
	}
	return ReleaseAssetUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *ReleaseAssetUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	// Skip processing if external pinning is not enabled
	if !viper.GetBool("ENABLE_EXTERNAL_PINNING") {
		return nil
	}

	event := &ReleaseAssetUpdatedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *ReleaseAssetUpdatedEventHandler) Process(ctx context.Context, event ReleaseAssetUpdatedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"tag":           event.Tag,
		"name":          event.Name,
		"new_cid":       event.NewCid,
		"old_cid":       event.OldCid,
	}).Info("processing release asset updated event")

	// Pin to Pinata if enabled
	if h.pinataClient != nil && event.NewCid != "" {
		cacheDir := viper.GetString("ATTACHMENT_DIR")

		// check if release asset is cached
		isCached, err := utils.IsReleaseAssetCached(event.NewSha256, cacheDir)
		if err != nil {
			return err
		}
		if !isCached {
			err = utils.DownloadReleaseAsset(event.NewCid, event.NewSha256, cacheDir)
			if err != nil {
				return err
			}
		}

		releaseAssetPath := path.Join(cacheDir, event.NewSha256)
		name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, event.Name, event.NewSha256)
		resp, err := h.pinataClient.PinFile(ctx, releaseAssetPath, name)
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
		name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, event.Name, event.OldSha256)
		err := h.pinataClient.UnpinFile(ctx, name)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to unpin file from Pinata")
		}
	}

	return nil
}
