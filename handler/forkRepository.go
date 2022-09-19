package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path"
	"strconv"
	"sync"

	"github.com/buger/jsonparser"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/app/consumer"
	"github.com/gitopia/git-server/app/tm"
	"github.com/gitopia/git-server/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type InvokeForkRepositoryEvent struct {
	Creator      string
	RepositoryId types.RepositoryId
	OwnerId      string
	TaskId       uint64
	TxHeight     uint64
}

// tm event codec
func (e *InvokeForkRepositoryEvent) UnMarshal(eventBuf []byte) error {
	creator, err := jsonparser.GetString(eventBuf, "events", "message.Creator", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing creator")
	}

	baseRepoKeyStr, err := jsonparser.GetString(eventBuf, "events", "message.BaseRepositoryKey", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing RepositoryId")
	}

	var repositoryId types.RepositoryId
	err = json.Unmarshal([]byte(baseRepoKeyStr), &repositoryId)
	if err != nil {
		return errors.Wrap(err, "error decoding RepositoryId")
	}

	ownerId, err := jsonparser.GetString(eventBuf, "events", "message.OwnerId", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing owner id")
	}

	taskIdStr, err := jsonparser.GetString(eventBuf, "events", "message.TaskId", "[0]")
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

	e.Creator = creator
	e.RepositoryId = types.RepositoryId{
		Id:   repositoryId.Id,
		Name: repositoryId.Name,
	}
	e.OwnerId = ownerId
	e.TaskId = taskId
	e.TxHeight = height

	return nil
}

type InvokeForkRepositoryEventHandler struct {
	tmc *tm.Client
	gc  app.GitopiaClient

	cc consumer.Client

	// commit offset only when backfill is complete
	commitOffset bool
	// written by backfill routine
	// read by real time event processor routine
	commitOffsetMu sync.RWMutex
	offsetChan     chan uint64
}

func NewInvokeForkRepositoryEventHandler(
	g app.GitopiaClient,
	t *tm.Client,
	c consumer.Client) InvokeForkRepositoryEventHandler {
	return InvokeForkRepositoryEventHandler{
		tmc:          t,
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
	logger.FromContext(ctx).Info("process fork repository event")

	res, err := h.gc.Task(ctx, event.TaskId)
	if err != nil {
		return err
	}
	if res.State != types.StatePending { // Task is already processed
		return nil
	}

	haveAuthorization, err := h.gc.CheckGitServerAuthorization(ctx, event.Creator)
	if err != nil {
		return err
	}
	if !haveAuthorization {
		logger.FromContext(ctx).
			WithField("creator", event.Creator).
			WithField("parent-repo-id", event.RepositoryId).
			Info("skipping fork repository, not authorized")
		return nil
	}

	err = h.gc.ForkRepository(ctx, event.Creator, event.RepositoryId, event.OwnerId, event.TaskId)
	if err != nil {
		err = errors.WithMessage(err, "gitopia fork repository error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	parentRepoId, err := h.gc.RepositoryId(ctx, event.RepositoryId.Id, event.RepositoryId.Name)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	forkedRepoId, err := h.gc.RepositoryId(ctx, event.OwnerId, event.RepositoryId.Name)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	sourceRepoPath := path.Join(viper.GetString("git_dir"), fmt.Sprintf("%v.git", parentRepoId))
	targetRepoPath := path.Join(viper.GetString("git_dir"), fmt.Sprintf("%v.git", forkedRepoId))
	cmd := exec.Command("git", "clone", "--shared", "--bare", sourceRepoPath, targetRepoPath)
	out, err := cmd.Output()
	if err != nil {
		err = errors.WithMessage(err, "fork error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, string(out))
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	err = h.gc.ForkRepositorySuccess(ctx, event.Creator, types.RepositoryId{
		Id:   event.OwnerId,
		Name: event.RepositoryId.Name,
	}, event.TaskId)
	if err != nil {
		err = errors.WithMessage(err, "fork repository success error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	logger.FromContext(ctx).
		WithField("creator", event.Creator).
		WithField("parent-repo-id", event.RepositoryId).
		WithField("forked-repo-id", types.RepositoryId{
			Id:   event.OwnerId,
			Name: event.RepositoryId.Name,
		}).
		Info("forked repository")
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

		grpcConn, err := grpc.Dial(viper.GetString("gitopia_grpc_url"),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
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

						var repositoryId types.RepositoryId
						err := json.Unmarshal([]byte(attributeMap["RepositoryId"]), &repositoryId)
						if err != nil {
							errChan <- errors.WithMessage(err, "error unmarshalling RepositoryId")
							return
						}

						taskId, err := strconv.ParseUint(attributeMap["TaskId"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing task id")
							return
						}

						event := InvokeForkRepositoryEvent{
							Creator:      attributeMap["Creator"],
							RepositoryId: repositoryId,
							OwnerId:      attributeMap["OwnerId"],
							TaskId:       taskId,
							TxHeight:     uint64(r.Height),
						}

						// backup head commit to IPFS
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
