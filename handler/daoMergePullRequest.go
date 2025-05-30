package handler

import (
	"context"
	"strconv"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type InvokeDaoMergePullRequestEvent struct {
	Admin        string
	RepositoryId uint64
	PullIid      uint64
	TaskId       uint64
	TxHeight     uint64
	Provider     string
}

// tm event codec
func (e *InvokeDaoMergePullRequestEvent) UnMarshal(eventBuf []byte) error {
	admin, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeCreatorKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing admin")
	}

	repositoryIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repositoryId, err := strconv.ParseUint(repositoryIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	pullRequestIid, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributePullRequestIidKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing pull request iid")
	}
	iid, err := strconv.ParseUint(pullRequestIid, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing pull request iid")
	}

	taskIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeTaskIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing task id")
	}
	taskId, err := strconv.ParseUint(taskIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing task id")
	}

	h, err := jsonparser.GetString(eventBuf, "events", "tx.height", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tx height")
	}
	height, err := strconv.ParseUint(h, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing height")
	}

	provider, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeProviderKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}

	e.Admin = admin
	e.RepositoryId = repositoryId
	e.PullIid = iid
	e.TaskId = taskId
	e.TxHeight = height
	e.Provider = provider

	return nil
}

type InvokeDaoMergePullRequestEventHandler struct {
	gc app.GitopiaProxy

	cc consumer.Client

	ipfsClusterClient ipfsclusterclient.Client
}

func NewInvokeDaoMergePullRequestEventHandler(g app.GitopiaProxy, c consumer.Client, ipfsClusterClient ipfsclusterclient.Client) InvokeDaoMergePullRequestEventHandler {
	return InvokeDaoMergePullRequestEventHandler{
		gc:                g,
		cc:                c,
		ipfsClusterClient: ipfsClusterClient,
	}
}

func (h *InvokeDaoMergePullRequestEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &InvokeDaoMergePullRequestEvent{}
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

func (h *InvokeDaoMergePullRequestEventHandler) Process(ctx context.Context, event InvokeDaoMergePullRequestEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"admin":         event.Admin,
		"repository-id": event.RepositoryId,
		"pull-iid":      event.PullIid,
		"task-id":       event.TaskId,
		"tx-height":     event.TxHeight,
	}).Info("Processing dao merge pull request event")

	res, err := h.gc.Task(ctx, event.TaskId)
	if err != nil {
		return err
	}
	if res.State != types.StatePending { // Task is already processed
		return nil
	}

	// Get dao address from repository
	repository, err := h.gc.Repository(ctx, event.RepositoryId)
	if err != nil {
		return err
	}

	haveAuthorization, err := h.gc.CheckGitServerAuthorization(ctx, repository.Owner.Id)
	if err != nil {
		return err
	}
	if !haveAuthorization {
		logger.FromContext(ctx).
			WithField("creator", repository.Owner.Id).
			WithField("repository-id", event.RepositoryId).
			WithField("pull-iid", event.PullIid).
			WithField("task-id", event.TaskId).
			WithField("tx-height", event.TxHeight).
			Info("skipping dao merge pull request, not authorized")
		return nil
	}

	mergePullEvent := InvokeMergePullRequestEvent{
		Creator:        repository.Owner.Id,
		RepositoryId:   event.RepositoryId,
		PullRequestIid: event.PullIid,
		TaskId:         event.TaskId,
		TxHeight:       event.TxHeight,
	}

	// Create a new InvokeMergePullRequestEventHandler to reuse its Process method
	mergePullHandler := NewInvokeMergePullRequestEventHandler(h.gc, h.cc, h.ipfsClusterClient)

	return mergePullHandler.Process(ctx, mergePullEvent)
}
