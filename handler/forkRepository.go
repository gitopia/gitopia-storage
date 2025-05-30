package handler

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"path"
	"strconv"
	"sync"

	"github.com/buger/jsonparser"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	// commit offset only when backfill is complete
	commitOffset bool
	// written by backfill routine
	// read by real time event processor routine
	commitOffsetMu sync.RWMutex
	offsetChan     chan uint64
}

func NewInvokeForkRepositoryEventHandler(g app.GitopiaProxy, c consumer.Client) InvokeForkRepositoryEventHandler {
	return InvokeForkRepositoryEventHandler{
		gc:           g,
		cc:           c,
		offsetChan:   make(chan uint64),
		commitOffset: false,
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

	commitOffset := func() bool {
		h.commitOffsetMu.RLock()
		defer h.commitOffsetMu.RUnlock()
		return h.commitOffset
	}()
	if commitOffset {
		err = h.cc.Commit(event.TxHeight)
		if err != nil {
			return errors.WithMessage(err, "error commiting tx height")
		}
	} else {
		// send if there are any receivers listening
		select {
		case h.offsetChan <- event.TxHeight:
		default:
			// ignore if there are no receivers
		}
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

// run asynchronously
// read offset until which tx's are processed
// fetch missed txs
// fetch repository information for missed txs
// process setRepositoryEvent
func (h *InvokeForkRepositoryEventHandler) BackfillMissedEvents(ctx context.Context) (<-chan struct{}, chan error) {
	errChan := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer func() {
			// notify backfill is done
			func() {
				h.commitOffsetMu.Lock()
				defer h.commitOffsetMu.Unlock()
				h.commitOffset = true
			}()
			cancel()
		}()

		startHeight, err := h.cc.Offset()
		if err != nil {
			errChan <- errors.WithMessage(err, "error fetching processed tx height")
			return
		}

		// no known offset to backfill
		if startHeight == 0 {
			logger.FromContext(ctx).Info("no known offset to backfill. terminating")
			return
		}

		// fetch upper limit for range query
		// in order to avoid scanning whole kv store
		// !!WAIT!! until first real time event is received. practically, real time events should flow as soon as bridge starts
		// backfill till current offset
		endHeight := <-h.offsetChan

		logger.FromContext(ctx).WithField("start", startHeight).WithField("end", endHeight).Info("backfill in progress")
		defer func() {
			logger.FromContext(ctx).Info("backfill done")
		}()

		grpcConn, err := grpc.Dial(viper.GetString("GITOPIA_ADDR"),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
		)
		if err != nil {
			errChan <- errors.Wrap(err, "dial err")
			return
		}
		serviceClient := tx.NewServiceClient(grpcConn)

		// process events in batches
		for offset, remainingPages := uint64(0), uint64(0); offset == 0 || remainingPages > 0; offset,
			remainingPages = offset+query.DefaultLimit, remainingPages-1 {
			res, err := serviceClient.GetTxsEvent(ctx, &tx.GetTxsEventRequest{
				Events: []string{"message.action='InvokeForkRepository'",
					// NOTE: > and < operators are not supported
					fmt.Sprintf("tx.height>=%d", startHeight+1),
					fmt.Sprintf("tx.height<=%d", endHeight-1),
				},
				Pagination: &query.PageRequest{
					Offset: offset,
				},
			})
			if err != nil {
				errChan <- errors.Wrap(err, "error fetching events")
				return
			}
			if remainingPages == 0 {
				remainingPages = uint64(math.Ceil(float64(res.GetPagination().GetTotal()) / query.DefaultLimit))
			}

			for _, r := range res.GetTxResponses() {
				for _, e := range r.Events {
					switch e.GetType() {
					case "InvokeForkRepository":
						attributeMap := make(map[string]string)
						for i := 0; i < len(e.Attributes); i++ {
							attributeMap[string(e.Attributes[i].Key)] = string(e.Attributes[i].Value)
						}

						repoId, err := strconv.ParseUint(attributeMap[types.EventAttributeRepoIdKey], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing repo id")
							return
						}

						taskId, err := strconv.ParseUint(attributeMap[types.EventAttributeTaskIdKey], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing task id")
							return
						}

						event := InvokeForkRepositoryEvent{
							Creator:             attributeMap[types.EventAttributeCreatorKey],
							RepoId:              repoId,
							RepoName:            attributeMap[types.EventAttributeRepoNameKey],
							RepoOwnerId:         attributeMap[types.EventAttributeRepoOwnerIdKey],
							ForkRepoName:        attributeMap[types.EventAttributeForkRepoNameKey],
							ForkRepoDescription: attributeMap[types.EventAttributeForkRepoDescriptionKey],
							ForkRepoBranch:      attributeMap[types.EventAttributeForkRepoBranchKey],
							ForkRepoOwnerId:     attributeMap[types.EventAttributeForkRepoOwnerIdKey],
							TaskId:              taskId,
							TxHeight:            uint64(r.Height),
						}

						// The missed events will be processed for the current/latest state of the repository, and not the state of the repository when the event was triggered
						err = h.Process(ctx, event)
						if err != nil {
							errChan <- errors.WithMessage(err, "error processing event")
							return
						}
						err = h.cc.Commit(event.TxHeight)
						if err != nil {
							errChan <- errors.WithMessage(err, "error commiting tx height")
							return
						}
					}
				}
			}
		}
		// processed all missed txs
		// handoff height tracking to real time event processor
		err = h.cc.Commit(endHeight)
		if err != nil {
			errChan <- errors.WithMessage(err, "error commiting tx height")
			return
		}
	}()

	return ctx.Done(), errChan
}
