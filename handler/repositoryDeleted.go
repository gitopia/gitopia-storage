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

	// Helper to extract string arrays from json
	extractStringArray := func(key string) ([]string, error) {
		var result []string
		value, _, _, err := jsonparser.Get(eventBuf, "events", EventRepositoryDeletedType+"."+key)
		if err != nil {
			if err == jsonparser.KeyPathNotFoundError {
				return result, nil // Not found is not an error here
			}
			return nil, err
		}
		jsonparser.ArrayEach(value, func(v []byte, dt jsonparser.ValueType, offset int, err error) {
			result = append(result, string(v))
		})
		return result, nil
	}

	repoIDs, err := extractStringArray("repository_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository_id")
	}

	providers, err := extractStringArray("provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	packfileCids, err := extractStringArray("packfile_cid")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing packfile_cid")
	}

	packfileNames, err := extractStringArray("packfile_name")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing packfile_name")
	}

	releaseAssetsArray, err := extractStringArray("release_assets")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing release_assets")
	}

	lfsObjectsArray, err := extractStringArray("lfs_objects")
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
		repoId, err := strconv.ParseUint(strings.Trim(repoIDs[i], `"`), 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		var releaseAssets []ReleaseAsset
		releaseAssetsStr := strings.Trim(releaseAssetsArray[i], `"`)
		_, err = jsonparser.ArrayEach([]byte(releaseAssetsStr), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			asset := ReleaseAsset{}
			name, _ := jsonparser.GetString(value, "name")
			asset.Name = name
			cid, _ := jsonparser.GetString(value, "cid")
			asset.Cid = cid
			sha, _ := jsonparser.GetString(value, "sha256")
			asset.Sha = sha
			tag, _ := jsonparser.GetString(value, "tag")
			asset.Tag = tag
			releaseAssets = append(releaseAssets, asset)
		})
		if err != nil {
			return nil, errors.Wrap(err, "error parsing release assets")
		}

		var lfsObjects []LFSObject
		lfsObjectsStr := strings.Trim(lfsObjectsArray[i], `"`)
		_, err = jsonparser.ArrayEach([]byte(lfsObjectsStr), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			lfsObject := LFSObject{}
			oid, _ := jsonparser.GetString(value, "oid")
			lfsObject.Oid = oid
			cid, _ := jsonparser.GetString(value, "cid")
			lfsObject.Cid = cid
			lfsObjects = append(lfsObjects, lfsObject)
		})
		if err != nil {
			return nil, errors.Wrap(err, "error parsing lfs objects")
		}

		events = append(events, RepositoryDeletedEvent{
			RepositoryId:  repoId,
			Provider:      strings.Trim(providers[i], `"`),
			PackfileCid:   strings.Trim(packfileCids[i], `"`),
			PackfileName:  strings.Trim(packfileNames[i], `"`),
			ReleaseAssets: releaseAssets,
			LfsObjects:    lfsObjects,
		})
	}

	return events, nil
}

type RepositoryDeletedEventHandler struct {
	gc           *app.GitopiaProxy
	pinataClient *PinataClient
}

func NewRepositoryDeletedEventHandler(g *app.GitopiaProxy, pinataClient *PinataClient) RepositoryDeletedEventHandler {
	return RepositoryDeletedEventHandler{
		gc:           g,
		pinataClient: pinataClient,
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
	if event.PackfileCid != "" {
		if h.pinataClient != nil {
			err := h.pinataClient.UnpinFile(ctx, event.PackfileName)
			if err != nil {
				logger.FromContext(ctx).WithError(err).Error("failed to unpin packfile from Pinata")
			} else {
				logger.FromContext(ctx).WithField("packfile", event.PackfileName).Info("unpinned packfile from Pinata")
			}
		}
	}

	// Delete release assets
	for _, asset := range event.ReleaseAssets {
		if h.pinataClient != nil {
			name := fmt.Sprintf("release-%d-%s-%s-%s", event.RepositoryId, asset.Tag, asset.Name, asset.Sha)
			err := h.pinataClient.UnpinFile(ctx, name)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithField("asset", name).Error("failed to unpin release asset from Pinata")
			} else {
				logger.FromContext(ctx).WithField("asset", name).Info("unpinned release asset from Pinata")
			}
		}
	}

	// Delete LFS objects
	for _, lfsObject := range event.LfsObjects {
		if h.pinataClient != nil {
			err := h.pinataClient.UnpinFile(ctx, lfsObject.Oid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithField("lfs_object", lfsObject.Oid).Error("failed to unpin LFS object from Pinata")
			} else {
				logger.FromContext(ctx).WithField("lfs_object", lfsObject.Oid).Info("unpinned LFS object from Pinata")
			}
		}
	}

	return nil
}
