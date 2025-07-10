package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type DeleteRepositoryEvent struct {
	Creator           string
	RepositoryId      uint64
	RepositoryOwnerId string
	RepositoryName    string
	Provider          string
}

// UnMarshal parses the event data into the DeleteRepositoryEvent struct
func (e *DeleteRepositoryEvent) UnMarshal(eventBuf []byte) error {
	creator, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeCreatorKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing creator")
	}

	repoIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	repoOwnerId, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoOwnerIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository owner id")
	}

	repoName, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoNameKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository name")
	}

	provider, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeProviderKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}

	e.Creator = creator
	e.RepositoryId = repoId
	e.RepositoryOwnerId = repoOwnerId
	e.RepositoryName = repoName
	e.Provider = provider

	return nil
}

type DeleteRepositoryEventHandler struct {
	gc                app.GitopiaProxy
	cc                consumer.Client
	ipfsClusterClient ipfsclusterclient.Client
}

// NewDeleteRepositoryEventHandler creates a new DeleteRepositoryEventHandler
func NewDeleteRepositoryEventHandler(g app.GitopiaProxy, c consumer.Client, ipfsClusterClient ipfsclusterclient.Client) *DeleteRepositoryEventHandler {
	return &DeleteRepositoryEventHandler{
		gc:                g,
		cc:                c,
		ipfsClusterClient: ipfsClusterClient,
	}
}

// Handle processes the DeleteRepository event
func (h *DeleteRepositoryEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	var event DeleteRepositoryEvent
	if err := event.UnMarshal(eventBuf); err != nil {
		return errors.Wrap(err, "failed to unmarshal DeleteRepository event")
	}

	return h.Process(ctx, event)
}

// Process handles the repository deletion process
func (h *DeleteRepositoryEventHandler) Process(ctx context.Context, event DeleteRepositoryEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	// Log the repository deletion event
	logger := logrus.WithFields(logrus.Fields{
		"repository_id":   event.RepositoryId,
		"repository_name": event.RepositoryName,
		"creator":         event.Creator,
	})
	logger.Info("processing repository deletion")

	packfile, err := h.gc.RepositoryPackfile(ctx, event.RepositoryId)
	if err != nil {
		// Empty repository
		if strings.Contains(err.Error(), "packfile not found") {
			return nil
		}
		return errors.Wrap(err, "failed to get repository packfile")
	}

	if err := h.gc.DeleteRepositoryPackfile(ctx, event.RepositoryId, event.RepositoryOwnerId); err != nil {
		return errors.Wrap(err, "failed to delete repository packfile")
	}

	// Unpin old packfile from IPFS cluster
	refCount, err := h.gc.StorageCidReferenceCount(ctx, packfile.Cid)
	if err != nil {
		return errors.Wrap(err, "failed to get reference count")
	}
	if refCount == 0 {
		err := utils.UnpinFile(h.ipfsClusterClient, packfile.Cid)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"repository_id":   event.RepositoryId,
				"repository_name": event.RepositoryName,
				"cid":             packfile.Cid,
			}).WithError(err).Error("failed to unpin file from IPFS Cluster")
		}
	}

	assets, err := h.gc.RepositoryReleaseAssetsByRepositoryIdAll(ctx, event.RepositoryId)
	if err != nil {
		return errors.Wrap(err, "failed to get repository release assets")
	}

	for _, asset := range assets {
		if err := h.gc.DeleteReleaseAsset(ctx, asset.RepositoryId, asset.Tag, asset.Name, event.RepositoryOwnerId); err != nil {
			return errors.Wrap(err, "failed to delete release asset")
		}

		// check reference count
		refCount, err := h.gc.StorageCidReferenceCount(ctx, asset.Cid)
		if err != nil {
			return errors.Wrap(err, "failed to get reference count")
		}
		if refCount == 0 {
			err := utils.UnpinFile(h.ipfsClusterClient, asset.Cid)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"repository_id":   event.RepositoryId,
					"repository_name": event.RepositoryName,
					"name":            asset.Name,
					"cid":             asset.Cid,
				}).WithError(err).Error("failed to unpin file from IPFS Cluster")
			}

		}
	}

	logger.WithFields(logrus.Fields{
		"repository_id":   event.RepositoryId,
		"repository_name": event.RepositoryName,
	}).Info("successfully processed repository deletion")

	return nil
}
