package handler

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strconv"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type InvokeForkRepositoryEvent struct {
	Creator             string
	RepoId              uint64
	RepoName            string
	RepoOwnerId         string
	ForkRepoName        string
	ForkRepoDescription string
	ForkRepoBranch      string
	ForkRepoOwnerId     string
	TaskId              uint64
	TxHeight            uint64
	Provider            string
}

// tm event codec
func (e *InvokeForkRepositoryEvent) UnMarshal(eventBuf []byte) error {
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

	repoName, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoNameKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository name")
	}

	repoOwnerId, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoOwnerIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository owner id")
	}

	forkRepoName, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeForkRepoNameKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing fork repository name")
	}

	forkRepoDescription, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeForkRepoDescriptionKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing fork repository description")
	}

	forkRepoBranch, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeForkRepoBranchKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing fork repository branch")
	}

	forkRepoOwnerId, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeForkRepoOwnerIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing fork repository owner id")
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

	e.Creator = creator
	e.RepoId = repoId
	e.RepoName = repoName
	e.RepoOwnerId = repoOwnerId
	e.ForkRepoName = forkRepoName
	e.ForkRepoDescription = forkRepoDescription
	e.ForkRepoBranch = forkRepoBranch
	e.ForkRepoOwnerId = forkRepoOwnerId
	e.TaskId = taskId
	e.TxHeight = height
	e.Provider = provider

	return nil
}

type InvokeForkRepositoryEventHandler struct {
	gc app.GitopiaProxy

	cc consumer.Client
}

func NewInvokeForkRepositoryEventHandler(g app.GitopiaProxy, c consumer.Client) InvokeForkRepositoryEventHandler {
	return InvokeForkRepositoryEventHandler{
		gc: g,
		cc: c,
	}
}

func (h *InvokeForkRepositoryEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &InvokeForkRepositoryEvent{}
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

func (h *InvokeForkRepositoryEventHandler) Process(ctx context.Context, event InvokeForkRepositoryEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"creator":         event.Creator,
		"taskId":          event.TaskId,
		"forkRepoOwner":   event.ForkRepoOwnerId,
		"forkRepoName":    event.ForkRepoName,
		"parentRepoOwner": event.RepoOwnerId,
		"parentRepoName":  event.RepoName,
	}).Info("Processing fork repository event")

	res, err := h.gc.Task(ctx, event.TaskId)
	if err != nil {
		return err
	}
	if res.State != types.StatePending { // Task is already processed
		return nil
	}

	haveAuthorization, err := h.gc.CheckGitServerAuthorization(ctx, event.ForkRepoOwnerId)
	if err != nil {
		return err
	}
	if !haveAuthorization {
		logger.FromContext(ctx).
			WithFields(logrus.Fields{
				"creator":         event.Creator,
				"taskId":          event.TaskId,
				"forkRepoOwner":   event.ForkRepoOwnerId,
				"forkRepoName":    event.ForkRepoName,
				"parentRepoOwner": event.RepoOwnerId,
				"parentRepoName":  event.RepoName,
			}).
			Warn("Skipping fork repository due to lack of authorization")

		return nil
	}

	err = h.gc.ForkRepository(
		ctx,
		event.Creator,
		types.RepositoryId{
			Id:   event.RepoOwnerId,
			Name: event.RepoName,
		},
		event.ForkRepoName,
		event.ForkRepoDescription,
		event.ForkRepoBranch,
		event.ForkRepoOwnerId,
		event.TaskId)
	if err != nil {
		logger := logger.FromContext(ctx).WithFields(logrus.Fields{
			"creator":         event.Creator,
			"taskId":          event.TaskId,
			"forkRepoOwner":   event.ForkRepoOwnerId,
			"forkRepoName":    event.ForkRepoName,
			"parentRepoOwner": event.RepoOwnerId,
			"parentRepoName":  event.RepoName,
		})
		logger.WithError(err).Error("Failed to fork repository")

		err = errors.Wrap(err, "gitopia fork repository error")
		updateErr := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if updateErr != nil {
			logger.WithError(updateErr).Error("Failed to update task status")
			return nil
		}
		return nil
	}

	forkedRepoId, err := h.gc.RepositoryId(ctx, event.ForkRepoOwnerId, event.ForkRepoName)
	if err != nil {
		logger := logger.FromContext(ctx).WithFields(logrus.Fields{
			"creator":       event.Creator,
			"taskId":        event.TaskId,
			"forkRepoOwner": event.ForkRepoOwnerId,
			"forkRepoName":  event.ForkRepoName,
		})
		logger.WithError(err).Error("Failed to get repository ID")

		err = errors.Wrap(err, "failed to get repository ID")
		updateErr := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if updateErr != nil {
			logger.WithError(updateErr).Error("Failed to update task status")
			return nil
		}
		return nil
	}

	cacheDir := viper.GetString("GIT_DIR")
	sourceRepoPath := path.Join(cacheDir, fmt.Sprintf("%v.git", event.RepoId))
	targetRepoPath := path.Join(cacheDir, fmt.Sprintf("%v.git", forkedRepoId))

	// check if source repo is cached
	isCached, err := utils.IsRepoCached(event.RepoId, cacheDir)
	if err != nil {
		return err
	}
	if !isCached {
		err = utils.DownloadRepo(event.RepoId, cacheDir)
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("git", "clone", "--shared", "--bare", sourceRepoPath, targetRepoPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger := logger.FromContext(ctx).WithFields(logrus.Fields{
			"creator":        event.Creator,
			"taskId":         event.TaskId,
			"forkRepoOwner":  event.ForkRepoOwnerId,
			"forkRepoName":   event.ForkRepoName,
			"sourceRepoPath": sourceRepoPath,
			"targetRepoPath": targetRepoPath,
		})
		logger.WithError(err).WithField("output", string(out)).Error("Failed to clone repository")

		wrappedErr := errors.Wrap(err, "fork error")
		updateErr := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, fmt.Sprintf("Error: %v\nOutput: %s", wrappedErr, out))
		if updateErr != nil {
			logger.WithError(updateErr).Error("Failed to update task status")
			return nil
		}
		return nil
	}

	err = h.gc.ForkRepositorySuccess(
		ctx,
		event.Creator,
		types.RepositoryId{
			Id:   event.ForkRepoOwnerId,
			Name: event.RepoName,
		},
		event.TaskId)
	if err != nil {
		logger := logger.FromContext(ctx).WithFields(logrus.Fields{
			"creator":         event.Creator,
			"taskId":          event.TaskId,
			"forkRepoOwner":   event.ForkRepoOwnerId,
			"forkRepoName":    event.ForkRepoName,
			"parentRepoOwner": event.RepoOwnerId,
			"parentRepoName":  event.RepoName,
		})
		logger.WithError(err).Error("Failed to fork repository successfully")

		wrappedErr := errors.WithMessage(err, "fork repository success error")
		updateErr := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, wrappedErr.Error())
		if updateErr != nil {
			logger.WithError(updateErr).Error("Failed to update task status")
			return nil
		}
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"creator":         event.Creator,
		"taskId":          event.TaskId,
		"forkRepoOwner":   event.ForkRepoOwnerId,
		"forkRepoName":    event.ForkRepoName,
		"parentRepoOwner": event.RepoOwnerId,
		"parentRepoName":  event.RepoName,
	}).Info("Repository forked successfully")

	return nil
}
