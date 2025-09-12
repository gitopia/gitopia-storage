package handler

import (
	"context"
	"strconv"
	"sync"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type DeleteRepositoryEvent struct {
	Creator      string
	RepositoryId uint64
	Provider     string
}

func UnmarshalDeleteRepositoryEvent(eventBuf []byte) ([]DeleteRepositoryEvent, error) {
	var events []DeleteRepositoryEvent

	creators, err := ExtractStringArray(eventBuf, "message", "Creator")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing creator")
	}

	repoIDs, err := ExtractStringArray(eventBuf, "message", "RepositoryId")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository id")
	}

	providers, err := ExtractStringArray(eventBuf, "message", "Provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	// Basic validation
	if len(creators) == 0 {
		return events, nil // No events to process
	}

	if !(len(creators) == len(repoIDs) && len(creators) == len(providers)) {
		return nil, errors.New("mismatched attribute array lengths for DeleteRepositoryEvent")
	}

	for i := 0; i < len(creators); i++ {
		repoId, err := strconv.ParseUint(repoIDs[i], 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		events = append(events, DeleteRepositoryEvent{
			Creator:      creators[i],
			RepositoryId: repoId,
			Provider:     providers[i],
		})
	}

	return events, nil
}

type DeleteRepositoryEventHandler struct {
	gc *app.GitopiaProxy
}

func NewDeleteRepositoryEventHandler(g *app.GitopiaProxy) DeleteRepositoryEventHandler {
	return DeleteRepositoryEventHandler{
		gc: g,
	}
}

func (h *DeleteRepositoryEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := UnmarshalDeleteRepositoryEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	// Process events concurrently with a limited number of workers
	// Limit to 5 concurrent delete operations
	maxWorkers := 5
	if len(events) < maxWorkers {
		maxWorkers = len(events)
	}

	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, event := range events {
		wg.Add(1)
		go func(e DeleteRepositoryEvent) {
			defer wg.Done()
			
			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if err := h.Process(ctx, e); err != nil {
				// Log error and continue processing other events
				logger.FromContext(ctx).WithFields(logrus.Fields{
					"repository_id": e.RepositoryId,
					"provider":      e.Provider,
				}).WithError(err).Error("failed to process DeleteRepositoryEvent")
			}
		}(event)
	}

	wg.Wait()
	return nil
}

func (h *DeleteRepositoryEventHandler) Process(ctx context.Context, event DeleteRepositoryEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"provider":      event.Provider,
	}).Info("proposing repository delete")

	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	err := h.gc.ProposeRepositoryDelete(ctx, event.Creator, event.RepositoryId)
	if err != nil {
		return errors.Wrap(err, "failed to propose repository delete")
	}

	err = h.gc.PollForUpdate(ctx, func() (bool, error) {
		return h.gc.CheckProposeRepositoryDelete(event.RepositoryId, event.Creator)
	})
	if err != nil {
		return errors.Wrap(err, "failed to verify repository delete")
	}

	return nil
}
