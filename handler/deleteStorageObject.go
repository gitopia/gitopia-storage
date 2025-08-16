package handler

import (
	"context"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const EventDeleteStorageObjectType = "gitopia.gitopia.storage.EventDeleteStorageObject"

type DeleteStorageObjectEvent struct {
	Provider string
	Cids     []string
}

func UnmarshalDeleteStorageObjectEvent(eventBuf []byte) ([]DeleteStorageObjectEvent, error) {
	var events []DeleteStorageObjectEvent

	providers, err := ExtractStringArray(eventBuf, EventDeleteStorageObjectType, "provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	cidsArray, err := ExtractStringArray(eventBuf, EventDeleteStorageObjectType, "cids")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing cids")
	}

	// Basic validation
	if len(providers) == 0 {
		return events, nil // No events to process
	}

	if len(providers) != len(cidsArray) {
		return nil, errors.New("mismatched attribute array lengths for DeleteStorageObjectEvent")
	}

	for i := 0; i < len(providers); i++ {
		var cids []string
		cidsValue := cidsArray[i]
		_, err := jsonparser.ArrayEach([]byte(cidsValue), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			cids = append(cids, string(value))
		})
		if err != nil {
			return nil, errors.Wrap(err, "error parsing cids array")
		}

		events = append(events, DeleteStorageObjectEvent{
			Provider: providers[i],
			Cids:     cids,
		})
	}

	return events, nil
}

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
	events, err := UnmarshalDeleteStorageObjectEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"provider": event.Provider,
			}).WithError(err).Error("failed to process DeleteStorageObjectEvent")
		}
	}

	return nil
}

func (h *DeleteStorageObjectEventHandler) Process(ctx context.Context, event DeleteStorageObjectEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	for _, cid := range event.Cids {
		if cid != "" {
			// Get packfile reference count
			refCount, err := h.gc.StorageCidReferenceCount(ctx, cid)
			if err != nil {
				return errors.WithMessage(err, "failed to get cid reference count")
			}

			if refCount == 0 {
				err = utils.UnpinFile(h.ipfsClusterClient, cid)
				if err != nil {
					return errors.WithMessage(err, "failed to unpin cid from IPFS cluster")
				}

				logger.FromContext(ctx).WithFields(logrus.Fields{
					"cid": cid,
				}).Info("unpinned")
			}
		}
	}

	return nil
}
