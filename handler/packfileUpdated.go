package handler

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/handler/storage"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventPackfileUpdatedType = "gitopia.gitopia.storage.EventPackfileUpdated"

type PackfileUpdatedEvent struct {
	RepositoryId uint64
	NewCid       string
	OldCid       string
	NewName      string
	OldName      string
	Deleted      bool
}

func UnmarshalPackfileUpdatedEvent(eventBuf []byte) ([]PackfileUpdatedEvent, error) {
	var events []PackfileUpdatedEvent

	repoIDs, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "repository_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository_id")
	}

	newCids, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "new_cid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing new_cid")
	}

	oldCids, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "old_cid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing old_cid")
	}

	newNames, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "new_name")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing new_name")
	}

	oldNames, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "old_name")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing old_name")
	}

	deleteds, err := ExtractStringArray(eventBuf, EventPackfileUpdatedType, "deleted")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing deleted")
	}

	// Basic validation
	if len(repoIDs) == 0 {
		return events, nil // No events to process
	}

	if !(len(repoIDs) == len(newCids) && len(repoIDs) == len(oldCids) && len(repoIDs) == len(newNames) && len(repoIDs) == len(oldNames) && len(repoIDs) == len(deleteds)) {
		return nil, errors.New("mismatched attribute array lengths for PackfileUpdatedEvent")
	}

	for i := 0; i < len(repoIDs); i++ {
		repoId, err := strconv.ParseUint(repoIDs[i], 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		deleted, err := strconv.ParseBool(deleteds[i])
		if err != nil {
			return nil, errors.Wrap(err, "error parsing deleted flag")
		}

		events = append(events, PackfileUpdatedEvent{
			RepositoryId: repoId,
			NewCid:       newCids[i],
			OldCid:       oldCids[i],
			NewName:      newNames[i],
			OldName:      oldNames[i],
			Deleted:      deleted,
		})
	}

	return events, nil
}

type PackfileUpdatedEventHandler struct {
	gc             *app.GitopiaProxy
	storageManager *storage.Manager
}

func NewPackfileUpdatedEventHandler(g *app.GitopiaProxy, storageManager *storage.Manager) PackfileUpdatedEventHandler {
	return PackfileUpdatedEventHandler{
		gc:             g,
		storageManager: storageManager,
	}
}

func (h *PackfileUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := UnmarshalPackfileUpdatedEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"new_cid":       event.NewCid,
				"old_cid":       event.OldCid,
			}).WithError(err).Error("failed to process PackfileUpdatedEvent")
		}
	}

	return nil
}

func (h *PackfileUpdatedEventHandler) Process(ctx context.Context, event PackfileUpdatedEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"new_cid":       event.NewCid,
		"old_cid":       event.OldCid,
		"deleted":       event.Deleted,
	}).Info("processing packfile updated event")

	if event.NewCid != "" && !event.Deleted && h.storageManager.HasProviders() {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.NewCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get reference count")
			return err
		}

		if refCount == 1 {
			cacheDir := viper.GetString("GIT_REPOS_DIR")

			// cache repo
			utils.RLockRepository(event.RepositoryId)
			defer utils.RUnlockRepository(event.RepositoryId)

			if err := utils.CacheRepository(event.RepositoryId, cacheDir); err != nil {
				return err
			}

			packfilePath := path.Join(cacheDir, fmt.Sprintf("%v.git/objects/pack/", event.RepositoryId), event.NewName)
			name := fmt.Sprintf("packfiles/%s", filepath.Base(packfilePath))
			err := h.storageManager.PinFile(ctx, packfilePath, name)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to pin file to external storage")
				// Don't fail the process, just log the error
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"packfile_name": event.NewName,
					"cid":           event.NewCid,
				}).Info("successfully pinned to external storage")
			}
		}
	}

	// Unpin old packfile from Pinata
	if event.OldCid != "" && event.OldCid != event.NewCid {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.OldCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
			return err
		}

		if refCount == 0 && h.storageManager.HasProviders() {
			name := fmt.Sprintf("packfiles/%s", event.OldName)
			err := h.storageManager.UnpinFile(ctx, name)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to unpin file from external storage")
			}
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"old_name":      event.OldName,
				"old_cid":       event.OldCid,
			}).Info("unpinned file from external storage")
		}
	}

	// Handle deletion case - unpin the packfile from Pinata if deleted and no references exist
	if event.Deleted {
		refCount, err := h.gc.StorageCidReferenceCount(ctx, event.NewCid)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Error("failed to get packfile reference count")
			return err
		}

		if refCount == 0 && h.storageManager.HasProviders() {
			name := fmt.Sprintf("packfiles/%s", event.NewName)
			err := h.storageManager.UnpinFile(ctx, name)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.NewName,
					"cid":           event.NewCid,
				}).WithError(err).Error("failed to unpin packfile from external storage")
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"name":          event.NewName,
					"cid":           event.NewCid,
				}).Info("successfully unpinned packfile from external storage")
			}
		}
	}

	return nil
}
