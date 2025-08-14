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

type ProposalTimeoutEvent struct {
	Provider string
	Cids     []string
}

func (e *ProposalTimeoutEvent) UnMarshal(eventBuf []byte) error {
	provider, err := jsonparser.GetString(eventBuf, "events", "gitopia.gitopia.storage.EventProposalTimeout.provider", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}
	e.Provider = strings.Trim(provider, "\"")

	cidsValue, err := jsonparser.GetString(eventBuf, "events", "gitopia.gitopia.storage.EventDeleteStorageObject.cids", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing cids value")
	}

	// Trim the quotes from the value string
	cidsValue = strings.Trim(cidsValue, "\"")

	// Parse the JSON array string
	_, err = jsonparser.ArrayEach([]byte(cidsValue), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		e.Cids = append(e.Cids, strings.Trim(string(value), "\""))
	})
	if err != nil {
		return errors.Wrap(err, "error parsing cids array")
	}

	return nil
}

type ProposalTimeoutHandler struct {
	gc                *app.GitopiaProxy
	ipfsClusterClient ipfsclusterclient.Client
}

func NewProposalTimeoutHandler(g *app.GitopiaProxy, ipfsClusterClient ipfsclusterclient.Client) ProposalTimeoutHandler {
	return ProposalTimeoutHandler{
		gc:                g,
		ipfsClusterClient: ipfsClusterClient,
	}
}

func (h *ProposalTimeoutHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &ProposalTimeoutEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	err = h.Process(ctx, *event)
	if err != nil {
		return errors.WithMessage(err, "error processing event")
	}

	return nil
}

func (h *ProposalTimeoutHandler) Process(ctx context.Context, event ProposalTimeoutEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"provider": event.Provider,
	}).Info("processing proposal timeout event")

	for _, cid := range event.Cids {
		// Get packfile reference count
		refCount, err := h.gc.StorageCidReferenceCount(ctx, cid)
		if err != nil {
			return errors.WithMessage(err, "failed to get cid reference count")
		}

		if refCount == 0 {
			err := utils.UnpinFile(h.ipfsClusterClient, cid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithField("cid", cid).Error("failed to unpin cid")
				continue
			}
			logger.FromContext(ctx).WithField("cid", cid).Info("unpinned cid")
		}
	}

	return nil
}
