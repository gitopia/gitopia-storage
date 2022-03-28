package handler

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buger/jsonparser"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia-ipfs-bridge/app/consumer"
	"github.com/gitopia/gitopia-ipfs-bridge/app/tm"
	"github.com/gitopia/gitopia-ipfs-bridge/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

type InvokeSetPullRequestStateEvent struct {
	Creator       string
	PullRequestId uint64
	TaskId        uint64
	TxHeight      uint64
}

// tm event codec
func (e *InvokeSetPullRequestStateEvent) UnMarshal(eventBuf []byte) error {
	creator, err := jsonparser.GetString(eventBuf, "events", "message.Creator", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing creator")
	}

	pullRequestId, err := jsonparser.GetString(eventBuf, "events", "message.PullRequestId", "[0]")
	if err != nil {
		errors.Wrap(err, "error parsing pull request Id")
	}
	id, err := strconv.ParseUint(pullRequestId, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing pull request id")
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
	e.PullRequestId = id
	e.TaskId = taskId
	e.TxHeight = height

	return nil
}

type InvokeSetPullRequestStateEventHandler struct {
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

func NewInvokeSetPullRequestStateEventHandler(
	g app.GitopiaClient,
	t *tm.Client,
	c consumer.Client) InvokeSetPullRequestStateEventHandler {
	return InvokeSetPullRequestStateEventHandler{
		tmc:          t,
		gc:           g,
		cc:           c,
		offsetChan:   make(chan uint64),
		commitOffset: false,
	}
}

func (h *InvokeSetPullRequestStateEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &InvokeSetPullRequestStateEvent{}
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

func (h *InvokeSetPullRequestStateEventHandler) Process(ctx context.Context, event InvokeSetPullRequestStateEvent) error {
	logger.FromContext(ctx).Info("process event")

	haveAuthorization, err := h.gc.CheckGitServerAuthorization(ctx, event.Creator)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}
	if !haveAuthorization {
		err = errors.WithMessage(err, "authorization error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	resp, err := h.gc.PullRequest(ctx, event.PullRequestId)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	message := fmt.Sprintf("Merge pull request #%v from %s/%s", resp.Iid, event.Creator, resp.Head.Branch)

	quarantineRepoPath, err := utils.CreateQuarantineRepo(resp.Base.RepositoryId, resp.Head.RepositoryId, resp.Base.Branch, resp.Head.Branch)
	if err != nil {
		err = errors.WithMessage(err, "create quarantine repo error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}
	defer os.RemoveAll(quarantineRepoPath)

	baseBranch := "base"
	trackingBranch := "tracking"
	stagingBranch := "staging"

	// Read base branch index
	cmd := exec.Command("git", "read-tree", "HEAD")
	cmd.Dir = quarantineRepoPath
	out, err := cmd.Output()
	if err != nil {
		err = errors.WithMessage(err, "read base branch error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	commitTimeStr := time.Now().Format(time.RFC3339)

	// Because this may call hooks we should pass in the environment
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+event.Creator,
		"GIT_AUTHOR_EMAIL=<>",
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+event.Creator,
		"GIT_COMMITTER_EMAIL=<>",
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)

	// Currently only merge style merge is enabled
	mergeStyle := utils.MergeStyleMerge

	// Merge commits.
	switch mergeStyle {
	case utils.MergeStyleMerge:
		cmd := exec.Command("git", "merge", "--no-ff", "--no-commit", trackingBranch)
		cmd.Env = env
		if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
			err = errors.WithMessage(err, "merge error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

		if err := utils.CommitAndSignNoAuthor(resp, message, "", quarantineRepoPath, env); err != nil {
			err = errors.WithMessage(err, "merge commit error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}
	case utils.MergeStyleRebase:
		fallthrough
	case utils.MergeStyleRebaseUpdate:
		fallthrough
	case utils.MergeStyleRebaseMerge:
		// Checkout head branch
		cmd = exec.Command("git", "checkout", "-b", stagingBranch, trackingBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = errors.WithMessage(err, "git checkout error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

		// Rebase before merging
		cmd = exec.Command("git", "rebase", baseBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			// Rebase will leave a REBASE_HEAD file in .git if there is a conflict
			if _, statErr := os.Stat(filepath.Join(quarantineRepoPath, ".git", "REBASE_HEAD")); statErr == nil {
				ok := false
				failingCommitPaths := []string{
					filepath.Join(quarantineRepoPath, ".git", "rebase-apply", "original-commit"), // Git < 2.26
					filepath.Join(quarantineRepoPath, ".git", "rebase-merge", "stopped-sha"),     // Git >= 2.26
				}
				for _, failingCommitPath := range failingCommitPaths {
					if _, statErr := os.Stat(filepath.Join(failingCommitPath)); statErr == nil {
						_, readErr := os.ReadFile(filepath.Join(failingCommitPath))
						if readErr != nil {
							// Abandon this attempt to handle the error
						}
						ok = true
						break
					}
				}
				if !ok {
					err = errors.WithMessage(err, "git rebase error")
					err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
					if err2 != nil {
						return errors.WithMessage(err2, "update task error")
					}
					return err
				}
				err = errors.WithMessage(err, "rebase conflict error")
				err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
				if err2 != nil {
					return errors.WithMessage(err2, "update task error")
				}
				return err
			}
			err = errors.WithMessage(err, "rebase error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

		// not need merge, just update by rebase. so skip
		if mergeStyle == utils.MergeStyleRebaseUpdate {
			break
		}

		// Checkout base branch again
		cmd = exec.Command("git", "checkout", baseBranch)
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = errors.WithMessage(err, "git checkout error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

		cmd = exec.Command("git", "merge")
		if mergeStyle == utils.MergeStyleRebase {
			cmd.Args = append(cmd.Args, "--ff-only")
		} else {
			cmd.Args = append(cmd.Args, "--no-ff", "--no-commit")
		}
		cmd.Args = append(cmd.Args, stagingBranch)

		// Prepare merge with commit
		if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
			err = errors.WithMessage(err, "git merge error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}
		if mergeStyle == utils.MergeStyleRebaseMerge {
			if err := utils.CommitAndSignNoAuthor(resp, message, "", quarantineRepoPath, env); err != nil {
				err = errors.WithMessage(err, "merge commit error")
				err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
				if err2 != nil {
					return errors.WithMessage(err2, "update task error")
				}
				return err
			}
		}
	case utils.MergeStyleSquash:
		// Merge with squash
		cmd := exec.Command("git", "merge", "--squash", trackingBranch)
		if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
			err = errors.WithMessage(err, "git merge --squash error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

		cmd = exec.Command("git", "commit", fmt.Sprintf("--author='%s <%s>'", event.Creator, "<>"), "-m", message)
		cmd.Env = env
		cmd.Dir = quarantineRepoPath
		out, err = cmd.Output()
		if err != nil {
			err = errors.WithMessage(err, "git commit error")
			err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
			if err2 != nil {
				return errors.WithMessage(err2, "update task error")
			}
			return err
		}

	default:
		err = fmt.Errorf("Invalid merge style: %v", mergeStyle)
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	mergeCommitSha, err := utils.GetFullCommitSha(quarantineRepoPath, baseBranch)
	if err != nil {
		err = errors.WithMessage(err, "merge commit sha error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+event.Creator,
		"GIT_AUTHOR_EMAIL=<>",
		"GIT_COMMITTER_NAME="+event.Creator,
		"GIT_COMMITTER_EMAIL=<>")

	var pushCmd *exec.Cmd
	if mergeStyle == utils.MergeStyleRebaseUpdate {
		// force push the rebase result to head brach
		pushCmd = exec.Command("git", "push", "-f", "head_repo", stagingBranch+":refs/heads/"+resp.Head.Branch)
	} else {
		pushCmd = exec.Command("git", "push", "origin", baseBranch+":refs/heads/"+resp.Base.Branch)
	}

	// Push back to upstream.
	pushCmd.Env = env
	pushCmd.Dir = quarantineRepoPath
	out, err = pushCmd.Output()
	if err != nil {
		if strings.Contains(string(out), "non-fast-forward") {

		} else if strings.Contains(string(out), "! [remote rejected]") {

		}
		err = errors.WithMessage(err, "git push error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	err = h.gc.SetPullRequestState(ctx, event.Creator, event.PullRequestId, "MERGED", mergeCommitSha)
	if err != nil {
		err = errors.WithMessage(err, "set pull request state error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	logger.FromContext(ctx).
		WithField("pull-request-id", event.PullRequestId).
		Info("merged pull request")
	return nil
}

// run asynchronously
// read offset until which tx's are processed
// fetch missed txs
// fetch repository information for missed txs
// process setRepositoryEvent
func (h *InvokeSetPullRequestStateEventHandler) BackfillMissedEvents(ctx context.Context) (<-chan struct{}, chan error) {
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
			grpc.WithInsecure(),
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
				Events: []string{"message.action='InvokeSetPullRequestState'",
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
					case "InvokeSetPullRequestState":
						attributeMap := make(map[string]string)
						for i := 0; i < len(e.Attributes); i++ {
							attributeMap[string(e.Attributes[i].Key)] = string(e.Attributes[i].Value)
						}

						prId, err := strconv.ParseUint(attributeMap["PullRequestId"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing repo id")
							return
						}

						taskId, err := strconv.ParseUint(attributeMap["TaskId"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing task id")
							return
						}

						event := InvokeSetPullRequestStateEvent{
							Creator:       attributeMap["Creator"],
							PullRequestId: prId,
							TaskId:        taskId,
							TxHeight:      uint64(r.Height),
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
