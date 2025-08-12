package handler

import (
	"context"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type DeleteStorageObjectEventHandler struct {
	gc                *app.GitopiaProxy
	ipfsClusterClient ipfsclusterclient.Client
}

func NewDeleteStorageObjectEventHandler(g *app.GitopiaProxy, ipfsClusterClient ipfsclusterclient.Client) DeleteStorageObjectEventHandler {
	return DeleteStorageObjectEventHandler{
		gc:                g,
		ipfsClusterClient: ipfsClusterClient,
	}
}

func (h *DeleteStorageObjectEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	provider, err := jsonparser.GetString(eventBuf, "events", "gitopia.gitopia.storage.EventDeleteStorageObject.provider", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}
	provider = strings.Trim(provider, "\"")

	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(provider) {
		return nil
	}

	var cids []string
	cidsValue, err := jsonparser.GetString(eventBuf, "events", "gitopia.gitopia.storage.EventDeleteStorageObject.cids", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cids value")
	}

	// Trim the quotes from the value string
	cidsValue = strings.Trim(cidsValue, "\"")

	// Parse the JSON array string
	_, err = jsonparser.ArrayEach([]byte(cidsValue), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		cids = append(cids, strings.Trim(string(value), "\""))
	})
	if err != nil {
		return errors.Wrap(err, "error parsing cids array")
	}

	for _, cid := range cids {
		if cid != "" {
			// Get packfile reference count
			refCount, err := h.gc.StorageCidReferenceCount(ctx, cid)
			if err != nil {
				return errors.WithMessage(err, "failed to get packfile reference count")
			}

			if refCount == 0 {
				err = utils.UnpinFile(h.ipfsClusterClient, cid)
				if err != nil {
					return errors.WithMessage(err, "failed to unpin packfile from IPFS cluster")
				}

				logger.FromContext(ctx).WithFields(logrus.Fields{
					"cid": cid,
				}).Info("unpinned")
			}
		}
	}

	return nil
}
