package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const EventDeleteRepositoryType = "gitopia.gitopia.gitopia.MsgDeleteRepository"

type DeleteRepositoryEvent struct {
	Creator      string
	RepositoryId uint64
	Provider     string
}

func (e *DeleteRepositoryEvent) UnMarshal(eventBuf []byte) error {
	creator, err := jsonparser.GetString(eventBuf, "events", "message.creator", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing creator")
	}
	e.Creator = strings.Trim(creator, "\"")

	repoIdStr, err := jsonparser.GetString(eventBuf, "events", "message.repository_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoId, err := strconv.ParseUint(strings.Trim(repoIdStr, "\""), 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	e.RepositoryId = repoId

	provider, err := jsonparser.GetString(eventBuf, "events", "message.provider", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}
	e.Provider = strings.Trim(provider, "\"")

	return nil
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
	event := &DeleteRepositoryEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	return h.Process(ctx, *event)
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

	err := h.gc.ProposeRepositoryDelete(ctx, h.gc.ClientAddress(), event.RepositoryId, true)
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
