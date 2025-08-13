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

func (e *RepositoryDeletedEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", EventRepositoryDeletedType+".repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoId, err := strconv.ParseUint(strings.Trim(repoIdStr, "\""), 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	e.RepositoryId = repoId

	provider, err := jsonparser.GetString(eventBuf, "events", EventRepositoryDeletedType+".provider", "[0]")
	if err == nil {
		e.Provider = strings.Trim(provider, "\"")
	}

	packfileCid, err := jsonparser.GetString(eventBuf, "events", EventRepositoryDeletedType+".packfile_cid", "[0]")
	if err == nil {
		e.PackfileCid = strings.Trim(packfileCid, "\"")
	}

	packfileName, err := jsonparser.GetString(eventBuf, "events", EventRepositoryDeletedType+".packfile_name", "[0]")
	if err == nil {
		e.PackfileName = strings.Trim(packfileName, "\"")
	}

	_, err = jsonparser.ArrayEach(eventBuf, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		asset := ReleaseAsset{}
		name, _ := jsonparser.GetString(value, "name")
		asset.Name = strings.Trim(name, "\"")
		cid, _ := jsonparser.GetString(value, "cid")
		asset.Cid = strings.Trim(cid, "\"")
		sha, _ := jsonparser.GetString(value, "sha256")
		asset.Sha = strings.Trim(sha, "\"")
		tag, _ := jsonparser.GetString(value, "tag")
		asset.Tag = strings.Trim(tag, "\"")
		e.ReleaseAssets = append(e.ReleaseAssets, asset)
	}, "events", EventRepositoryDeletedType+".release_assets", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing release assets")
	}

	_, err = jsonparser.ArrayEach(eventBuf, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		lfsObject := LFSObject{}
		oid, _ := jsonparser.GetString(value, "oid")
		lfsObject.Oid = strings.Trim(oid, "\"")
		cid, _ := jsonparser.GetString(value, "cid")
		lfsObject.Cid = strings.Trim(cid, "\"")
		e.LfsObjects = append(e.LfsObjects, lfsObject)
	}, "events", EventRepositoryDeletedType+".lfs_objects", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing lfs objects")
	}

	return nil
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
	event := &RepositoryDeletedEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
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
