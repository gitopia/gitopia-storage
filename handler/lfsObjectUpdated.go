package handler

import (
	"context"
	"path"
	"strconv"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventLFSObjectUpdatedType = "gitopia.gitopia.storage.EventLFSObjectUpdated"

type LfsObjectUpdatedEvent struct {
	RepositoryId uint64
	Oid          string
	Cid          string
	Deleted      bool
}

// Unmarshal parses LFSObjectUpdated events from an event buffer.
// It can handle multiple events of the same type within a single buffer.
func Unmarshal(eventBuf []byte) ([]LfsObjectUpdatedEvent, error) {
	var events []LfsObjectUpdatedEvent

	repoIDs, err := ExtractStringArray(eventBuf, EventLFSObjectUpdatedType, "repository_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository_id")
	}

	oids, err := ExtractStringArray(eventBuf, EventLFSObjectUpdatedType, "oid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing oid")
	}

	cids, err := ExtractStringArray(eventBuf, EventLFSObjectUpdatedType, "cid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing cid")
	}

	deleteds, err := ExtractStringArray(eventBuf, EventLFSObjectUpdatedType, "deleted")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing deleted")
	}

	// Basic validation
	if len(repoIDs) == 0 {
		return events, nil // No events to process
	}

	if !(len(repoIDs) == len(oids) && len(repoIDs) == len(cids) && len(repoIDs) == len(deleteds)) {
		return nil, errors.New("mismatched attribute array lengths for LFSObjectUpdatedEvent")
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

		events = append(events, LfsObjectUpdatedEvent{
			RepositoryId: repoId,
			Oid:          oids[i],
			Cid:          cids[i],
			Deleted:      deleted,
		})
	}

	return events, nil
}

type LfsObjectUpdatedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewLfsObjectUpdatedEventHandler(g *app.GitopiaProxy, pinataClient *PinataClient) LfsObjectUpdatedEventHandler {
	return LfsObjectUpdatedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
	}
}

func (h *LfsObjectUpdatedEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := Unmarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"oid":           event.Oid,
			}).WithError(err).Error("failed to process LfsObjectUpdatedEvent")
		}
	}

	return nil
}

func (h *LfsObjectUpdatedEventHandler) Process(ctx context.Context, event LfsObjectUpdatedEvent) error {
	if event.Deleted {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"repository_id": event.RepositoryId,
			"oid":           event.Oid,
			"cid":           event.Cid,
		}).Info("processing lfs object deleted event")

		// Unpin from Pinata
		if event.Oid != "" {
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
	} else {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"repository_id": event.RepositoryId,
			"oid":           event.Oid,
			"cid":           event.Cid,
		}).Info("processing lfs object updated event")

		// Pin to Pinata
		if event.Cid != "" {
			cacheDir := viper.GetString("LFS_OBJECTS_DIR")

			// check if lfs object is cached
			cached, err := utils.IsLFSObjectCached(event.Oid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to check if lfs object is cached")
			}
			if !cached {
				err := utils.DownloadLFSObject(event.Cid, event.Oid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).Error("failed to cache lfs object")
				}
			}

			lfsObjectPath := path.Join(cacheDir, event.Oid)
			resp, err := h.pinataClient.PinFile(ctx, lfsObjectPath, event.Oid)
			if err != nil {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"oid":           event.Oid,
					"cid":           event.Cid,
				}).WithError(err).Error("failed to pin file to Pinata")
				// Don't fail the process, just log the error
			} else {
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"oid":           event.Oid,
					"cid":           event.Cid,
					"pinata_id":     resp.Data.ID,
				}).Info("successfully pinned to Pinata")
			}
		}
	}

	return nil
}
