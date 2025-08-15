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

const EventProposalTimeoutType = "gitopia.gitopia.storage.EventProposalTimeout"

type ProposalTimeoutEvent struct {
	Provider string
	Cids     []string
}

func UnmarshalProposalTimeoutEvent(eventBuf []byte) ([]ProposalTimeoutEvent, error) {
	var events []ProposalTimeoutEvent

	// Helper to extract string arrays from json
	extractStringArray := func(key string) ([]string, error) {
		var result []string
		value, _, _, err := jsonparser.Get(eventBuf, "events", EventProposalTimeoutType+"."+key)
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

	providers, err := extractStringArray("provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	cidsArray, err := extractStringArray("cids")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing cids")
	}

	// Basic validation
	if len(providers) == 0 {
		return events, nil // No events to process
	}

	if len(providers) != len(cidsArray) {
		return nil, errors.New("mismatched attribute array lengths for ProposalTimeoutEvent")
	}

	for i := 0; i < len(providers); i++ {
		var cids []string
		cidsValue := strings.Trim(cidsArray[i], `"`)
		_, err := jsonparser.ArrayEach([]byte(cidsValue), func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			cids = append(cids, strings.Trim(string(value), `"`))
		})
		if err != nil {
			return nil, errors.Wrap(err, "error parsing cids array")
		}

		events = append(events, ProposalTimeoutEvent{
			Provider: strings.Trim(providers[i], `"`),
			Cids:     cids,
		})
	}

	return events, nil
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
	events, err := UnmarshalProposalTimeoutEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"provider": event.Provider,
			}).WithError(err).Error("failed to process ProposalTimeoutEvent")
		}
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
