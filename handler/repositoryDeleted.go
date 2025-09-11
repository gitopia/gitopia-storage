package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/handler/storage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const EventRepositoryDeletedType = "gitopia.gitopia.storage.EventRepositoryDeleted"

type ReleaseAsset struct {
	Name string
	Cid  string
	Sha  string
	Tag  string
}

type LFSObject struct {
	Oid string
	Cid string
}

type RepositoryDeletedEvent struct {
	RepositoryId  uint64
	Provider      string
	PackfileCid   string
	PackfileName  string
	ReleaseAssets []ReleaseAsset
	LfsObjects    []LFSObject
}

func UnmarshalRepositoryDeletedEvent(eventBuf []byte) ([]RepositoryDeletedEvent, error) {
	var events []RepositoryDeletedEvent

	repoIDs, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "repository_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository_id")
	}

	providers, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	packfileCids, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "packfile_cid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing packfile_cid")
	}

	packfileNames, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "packfile_name")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing packfile_name")
	}

	releaseAssetsArray, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "release_assets")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing release_assets")
	}

	lfsObjectsArray, err := ExtractStringArray(eventBuf, EventRepositoryDeletedType, "lfs_objects")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing lfs_objects")
	}

	// Basic validation
	if len(repoIDs) == 0 {
		return events, nil // No events to process
	}

	if !(len(repoIDs) == len(providers) && len(repoIDs) == len(packfileCids) && len(repoIDs) == len(packfileNames) && len(repoIDs) == len(releaseAssetsArray) && len(repoIDs) == len(lfsObjectsArray)) {
		return nil, errors.New("mismatched attribute array lengths for RepositoryDeletedEvent")
	}

	for i := 0; i < len(repoIDs); i++ {
		repoId, err := strconv.ParseUint(repoIDs[i], 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		var releaseAssets []ReleaseAsset
		releaseAssetsStr := releaseAssetsArray[i]
		err = json.Unmarshal([]byte(releaseAssetsStr), &releaseAssets)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing release assets")
		}

		var lfsObjects []LFSObject
		lfsObjectsStr := lfsObjectsArray[i]
		err = json.Unmarshal([]byte(lfsObjectsStr), &lfsObjects)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing lfs objects")
		}

		events = append(events, RepositoryDeletedEvent{
			RepositoryId:  repoId,
			Provider:      providers[i],
			PackfileCid:   packfileCids[i],
			PackfileName:  packfileNames[i],
			ReleaseAssets: releaseAssets,
			LfsObjects:    lfsObjects,
		})
	}

	return events, nil
}

type RepositoryDeletedEventHandler struct {
	gc             *app.GitopiaProxy
	storageManager *storage.Manager
}

func NewRepositoryDeletedEventHandler(g *app.GitopiaProxy, storageManager *storage.Manager) RepositoryDeletedEventHandler {
	return RepositoryDeletedEventHandler{
		gc:             g,
		storageManager: storageManager,
	}
}

func (h *RepositoryDeletedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := UnmarshalRepositoryDeletedEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
			}).WithError(err).Error("failed to process RepositoryDeletedEvent")
		}
	}

	return nil
}

func (h *RepositoryDeletedEventHandler) Process(ctx context.Context, event RepositoryDeletedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
	}).Info("processing repository deleted event")

	// Delete packfile
	if event.PackfileCid != "" && h.storageManager.HasProviders() {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.PackfileCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
			return err
		}

		if refCount == 0 {
			name := fmt.Sprintf("packfiles/%s", event.PackfileName)
			err := h.storageManager.UnpinFile(ctx, name)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to unpin packfile from external storage")
			} else {
				logger.FromContext(ctx).WithField("packfile", event.PackfileName).Info("unpinned packfile from external storage")
			}
		}
	}

	// Delete release assets
	if h.storageManager.HasProviders() {
		for _, asset := range event.ReleaseAssets {
			refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.Cid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to get reference count")
				return err
			}

			if refCount == 0 {
				name := fmt.Sprintf("release-assets/%s", asset.Sha)
				err := h.storageManager.UnpinFile(ctx, name)
				if err != nil {
					logger.FromContext(ctx).WithError(err).WithField("asset", asset.Sha).Error("failed to unpin release asset from external storage")
				} else {
					logger.FromContext(ctx).WithField("asset", asset.Sha).Info("unpinned release asset from external storage")
				}
			}
		}
	}

	// Delete LFS objects
	if h.storageManager.HasProviders() {
		for _, lfsObject := range event.LfsObjects {
			refCount, err := h.gc.StorageCidReferenceCount(ctx, lfsObject.Cid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to get reference count")
				return err
			}

			if refCount == 0 {
				name := fmt.Sprintf("lfs-objects/%s", lfsObject.Oid)
				err := h.storageManager.UnpinFile(ctx, name)
				if err != nil {
					logger.FromContext(ctx).WithError(err).WithField("lfs_object", lfsObject.Oid).Error("failed to unpin LFS object from external storage")
				} else {
					logger.FromContext(ctx).WithField("lfs_object", lfsObject.Oid).Info("unpinned LFS object from external storage")
				}
			}
		}
	}

	return nil
}
