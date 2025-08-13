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

const EventReleaseAssetsUpdatedType = "gitopia.gitopia.storage.EventReleaseAssetsUpdated"

type ReleaseAssetUpdate struct {
	Name      string
	Cid       string
	OldCid    string
	Sha256    string
	OldSha256 string
	Delete    bool
}

type ReleaseAssetsUpdatedEvent struct {
	RepositoryId uint64
	Tag          string
	Assets       []ReleaseAssetUpdate
	Provider     string
}

func (e *ReleaseAssetsUpdatedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetsUpdatedType+"."+"repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	tag, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetsUpdatedType+"."+"tag", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tag")
	}
	tag = strings.Trim(tag, "\"")

	provider, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetsUpdatedType+"."+"provider", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	} else {
		provider = strings.Trim(provider, "\"")
	}

	// Parse assets array
	assets := make([]ReleaseAssetUpdate, 0)

	// First get the assets value as a string
	assetsStr, err := jsonparser.GetString(eventBuf, "events", EventReleaseAssetsUpdatedType+"."+"assets", "[0]")
	if err != nil {
		return errors.Wrap(err, "error getting assets string")
	}

	// Parse the string as JSON array
	_, err = jsonparser.ArrayEach([]byte(assetsStr), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		if err != nil {
			return
		}

		asset := ReleaseAssetUpdate{}

		name, err := jsonparser.GetString(value, "name")
		if err == nil {
			asset.Name = strings.Trim(name, "\"")
		}

		newCid, err := jsonparser.GetString(value, "cid")
		if err == nil {
			asset.Cid = strings.Trim(newCid, "\"")
		}

		oldCid, err := jsonparser.GetString(value, "old_cid")
		if err == nil {
			asset.OldCid = strings.Trim(oldCid, "\"")
		}

		sha256, err := jsonparser.GetString(value, "sha256")
		if err == nil {
			asset.Sha256 = strings.Trim(sha256, "\"")
		}

		oldSha256, err := jsonparser.GetString(value, "old_sha256")
		if err == nil {
			asset.OldSha256 = strings.Trim(oldSha256, "\"")
		}

		// Parse delete field
		deleteFlag, err := jsonparser.GetBoolean(value, "delete")
		if err == nil {
			asset.Delete = deleteFlag
		} else {
			// delete field might not be present, defaulting to false
			asset.Delete = false
		}

		assets = append(assets, asset)
	})

	if err != nil {
		return errors.Wrap(err, "error parsing assets")
	}

	e.RepositoryId = repoId
	e.Tag = tag
	e.Assets = assets
	e.Provider = provider

	return nil
}

type ReleaseAssetsUpdatedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewReleaseAssetsUpdatedEventHandler(g *app.GitopiaProxy, pinataClient *PinataClient) ReleaseAssetsUpdatedEventHandler {
	return ReleaseAssetsUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *ReleaseAssetsUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &ReleaseAssetsUpdatedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
}

func (h *ReleaseAssetsUpdatedEventHandler) Process(ctx context.Context, event ReleaseAssetsUpdatedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"tag":           event.Tag,
		"asset_count":   len(event.Assets),
	}).Info("processing release assets updated event")

	for _, asset := range event.Assets {
		if asset.Delete {
			// Handle delete operation
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
				"name":          asset.Name,
				"cid":           asset.Cid,
			}).Info("processing release asset delete")

			// Unpin old asset from Pinata if enabled
			refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.Cid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to get attachment reference count")
				continue // Don't fail the entire process if one asset fails
			}
			if refCount == 0 {
				if asset.Cid != "" {
					name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, asset.Name, asset.Sha256)
					err := h.pinataClient.UnpinFile(ctx, name)
					if err != nil {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.Cid,
						}).WithError(err).Error("failed to unpin file from Pinata")
					} else {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.Cid,
						}).Info("successfully unpinned file from Pinata")
					}
				}
			}
		} else {
			// Handle update/creation operation
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
				"name":          asset.Name,
				"new_cid":       asset.Cid,
				"old_cid":       asset.OldCid,
			}).Info("processing release asset update")

			// Pin to Pinata if enabled
			if asset.Cid != "" {
				cacheDir := viper.GetString("ATTACHMENT_DIR")

				// check if release asset is cached
				err := utils.CacheReleaseAsset(event.RepositoryId, event.Tag, asset.Name, cacheDir)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to cache release asset")
				}

				releaseAssetPath := path.Join(cacheDir, asset.Sha256)
				name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, asset.Name, asset.Sha256)
				resp, err := h.pinataClient.PinFile(ctx, releaseAssetPath, name)
				if err != nil {
					logger.FromContext(ctx).WithFields(logrus.Fields{
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
						"name":          asset.Name,
						"cid":           asset.Cid,
					}).WithError(err).Error("failed to pin file to Pinata")
					// Don't fail the process, just log the error
				} else {
					logger.FromContext(ctx).WithFields(logrus.Fields{
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
						"name":          asset.Name,
						"cid":           asset.Cid,
						"pinata_id":     resp.Data.ID,
					}).Info("successfully pinned to Pinata")
				}
			}

			// Unpin old asset from Pinata if enabled and no longer referenced
			if asset.OldCid != "" && asset.OldCid != asset.Cid {
				refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.OldCid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to get attachment reference count")
					continue // Don't fail the entire process if one asset fails
				}
				if refCount == 0 {
					name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, event.Tag, asset.Name, asset.OldSha256)
					err := h.pinataClient.UnpinFile(ctx, name)
					if err != nil {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.OldCid,
						}).WithError(err).Error("failed to unpin file from Pinata")
					}
				}
			}
		}
	}

	return nil
}
