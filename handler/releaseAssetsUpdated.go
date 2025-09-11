package handler

import (
	"context"
	"encoding/json"
	"path"
	"strconv"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/handler/storage"
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

func UnmarshalReleaseAssetsUpdatedEvent(eventBuf []byte) ([]ReleaseAssetsUpdatedEvent, error) {
	var events []ReleaseAssetsUpdatedEvent

	repoIDs, err := ExtractStringArray(eventBuf, EventReleaseAssetsUpdatedType, "repository_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository_id")
	}

	tags, err := ExtractStringArray(eventBuf, EventReleaseAssetsUpdatedType, "tag")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing tag")
	}

	providers, err := ExtractStringArray(eventBuf, EventReleaseAssetsUpdatedType, "provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	assetsArray, err := ExtractStringArray(eventBuf, EventReleaseAssetsUpdatedType, "assets")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing assets")
	}

	// Basic validation
	if len(repoIDs) == 0 {
		return events, nil // No events to process
	}

	if !(len(repoIDs) == len(tags) && len(repoIDs) == len(providers) && len(repoIDs) == len(assetsArray)) {
		return nil, errors.New("mismatched attribute array lengths for ReleaseAssetsUpdatedEvent")
	}

	for i := 0; i < len(repoIDs); i++ {
		repoId, err := strconv.ParseUint(repoIDs[i], 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		var assets []ReleaseAssetUpdate
		assetsStr := assetsArray[i]
		err = json.Unmarshal([]byte(assetsStr), &assets)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing assets")
		}

		events = append(events, ReleaseAssetsUpdatedEvent{
			RepositoryId: repoId,
			Tag:          tags[i],
			Assets:       assets,
			Provider:     providers[i],
		})
	}

	return events, nil
}

type ReleaseAssetsUpdatedEventHandler struct {
	gc             *app.GitopiaProxy
	storageManager *storage.Manager
}

func NewReleaseAssetsUpdatedEventHandler(g *app.GitopiaProxy, storageManager *storage.Manager) ReleaseAssetsUpdatedEventHandler {
	return ReleaseAssetsUpdatedEventHandler{
		gc:             g,
		storageManager: storageManager,
	}
}

func (h *ReleaseAssetsUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := UnmarshalReleaseAssetsUpdatedEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).WithError(err).Error("failed to process ReleaseAssetsUpdatedEvent")
		}
	}

	return nil
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

			// Unpin old asset from external storage
			refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.OldCid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to get attachment reference count")
				continue // Don't fail the entire process if one asset fails
			}
			if refCount == 0 && h.storageManager.HasProviders() {
				err := h.storageManager.UnpinFile(ctx, asset.OldSha256)
				if err != nil {
					logger.FromContext(ctx).WithFields(logrus.Fields{
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
						"name":          asset.Name,
						"cid":           asset.OldCid,
					}).WithError(err).Error("failed to unpin file from external storage")
				} else {
					logger.FromContext(ctx).WithFields(logrus.Fields{
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
						"name":          asset.Name,
						"cid":           asset.OldCid,
					}).Info("successfully unpinned file from external storage")
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

			// Pin to external storage if enabled
			if asset.Cid != "" && h.storageManager.HasProviders() {
				refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.Cid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to get reference count")
					return err
				}

				if refCount == 1 {
					cacheDir := viper.GetString("ATTACHMENT_DIR")

					// check if release asset is cached
					err := utils.CacheReleaseAsset(event.RepositoryId, event.Tag, asset.Name, cacheDir)
					if err != nil {
						logger.FromContext(ctx).WithError(err).Error("failed to cache release asset")
					}

					releaseAssetPath := path.Join(cacheDir, asset.Sha256)
					err = h.storageManager.PinFile(ctx, releaseAssetPath, asset.Sha256)
					if err != nil {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.Cid,
						}).WithError(err).Error("failed to pin file to external storage")
						// Don't fail the process, just log the error
					} else {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.Cid,
						}).Info("successfully pinned to external storage")
					}
				}
			}

			// Unpin old asset from external storage if enabled and no longer referenced
			if asset.OldCid != "" && asset.OldCid != asset.Cid && h.storageManager.HasProviders() {
				refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.OldCid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to get attachment reference count")
					continue // Don't fail the entire process if one asset fails
				}
				if refCount == 0 {
					err := h.storageManager.UnpinFile(ctx, asset.OldSha256)
					if err != nil {
						logger.FromContext(ctx).WithFields(logrus.Fields{
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
							"name":          asset.Name,
							"cid":           asset.OldCid,
						}).WithError(err).Error("failed to unpin file from external storage")
					}
				}
			}
		}
	}

	return nil
}
