package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"

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

			// Unpin old asset from Pinata
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
				} else {
					logger.FromContext(ctx).WithFields(logrus.Fields{
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
						"name":          asset.Name,
						"cid":           asset.OldCid,
					}).Info("successfully unpinned file from Pinata")
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
