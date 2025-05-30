package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buger/jsonparser"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/app/consumer"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/ipfs/boxo/files"
	ipfspath "github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type InvokeMergePullRequestEvent struct {
	Creator        string
	RepositoryId   uint64
	PullRequestIid uint64
	TaskId         uint64
	TxHeight       uint64
	Provider       string
}

// tm event codec
func (e *InvokeMergePullRequestEvent) UnMarshal(eventBuf []byte) error {
	creator, err := jsonparser.GetString(eventBuf, "events", "message.Creator", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing creator")
	}

	repositoryIdStr, err := jsonparser.GetString(eventBuf, "events", "message.RepositoryId", "[0]")
	if err != nil {
		errors.Wrap(err, "error parsing repository id")
	}
	repositoryId, err := strconv.ParseUint(repositoryIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	pullRequestIid, err := jsonparser.GetString(eventBuf, "events", "message.PullRequestIid", "[0]")
	if err != nil {
		errors.Wrap(err, "error parsing pull request iid")
	}
	iid, err := strconv.ParseUint(pullRequestIid, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing pull request iid")
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

	provider, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeProviderKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}

	e.Creator = creator
	e.RepositoryId = repositoryId
	e.PullRequestIid = iid
	e.TaskId = taskId
	e.TxHeight = height
	e.Provider = provider

	return nil
}

type InvokeMergePullRequestEventHandler struct {
	gc app.GitopiaProxy

	cc consumer.Client

	ipfsClusterClient ipfsclusterclient.Client

	// commit offset only when backfill is complete
	commitOffset bool
	// written by backfill routine
	// read by real time event processor routine
	commitOffsetMu sync.RWMutex
	offsetChan     chan uint64
}

func NewInvokeMergePullRequestEventHandler(g app.GitopiaProxy, c consumer.Client, ipfsClusterClient ipfsclusterclient.Client) InvokeMergePullRequestEventHandler {
	return InvokeMergePullRequestEventHandler{
		gc:                g,
		cc:                c,
		ipfsClusterClient: ipfsClusterClient,
		offsetChan:        make(chan uint64),
		commitOffset:      false,
	}
}

func (h *InvokeMergePullRequestEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &InvokeMergePullRequestEvent{}
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

func (h *InvokeMergePullRequestEventHandler) Process(ctx context.Context, event InvokeMergePullRequestEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).Info("process merge pull request event")

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
			WithField("repository-id", event.RepositoryId).
			WithField("pull-request-iid", event.PullRequestIid).
			WithField("task-id", event.TaskId).
			WithField("tx-height", event.TxHeight).
			Info("skipping merge pull request, not authorized")
		return nil
	}

	resp, err := h.gc.PullRequest(ctx, event.RepositoryId, event.PullRequestIid)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	// check if head repository is cached
	cacheDir := viper.GetString("GIT_DIR")
	isCached, err := utils.IsRepoCached(resp.Head.RepositoryId, cacheDir)
	if err != nil {
		return err
	}
	if !isCached {
		err = utils.DownloadRepo(resp.Head.RepositoryId, cacheDir)
		if err != nil {
			return err
		}
	}

	if resp.Base.RepositoryId != resp.Head.RepositoryId {
		// check if base repository is cached
		isCached, err := utils.IsRepoCached(resp.Base.RepositoryId, cacheDir)
		if err != nil {
			return err
		}
		if !isCached {
			err = utils.DownloadRepo(resp.Base.RepositoryId, cacheDir)
			if err != nil {
				return err
			}
		}
	}

	headRepositoryName, err := h.gc.RepositoryName(ctx, resp.Head.RepositoryId)
	if err != nil {
		err = errors.WithMessage(err, "query error")
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	message := fmt.Sprintf("Merge pull request #%v from %s/%s", resp.Iid, headRepositoryName, resp.Head.Branch)

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
			// truncate error message to less than 255 characters
			if len(err.Error()) > 255 {
				err = errors.New(err.Error()[:255])
			}
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

	err = h.gc.SetPullRequestState(ctx, event.Creator, event.RepositoryId, event.PullRequestIid, "MERGED", mergeCommitSha, event.TaskId)
	if err != nil {
		err = errors.WithMessage(err, "set pull request state error")
		// truncate error message to less than 255 characters
		if len(err.Error()) > 255 {
			err = errors.New(err.Error()[:255])
		}
		err2 := h.gc.UpdateTask(ctx, event.Creator, event.TaskId, types.StateFailure, err.Error())
		if err2 != nil {
			return errors.WithMessage(err2, "update task error")
		}
		return err
	}

	baseRepoPath := filepath.Join(cacheDir, fmt.Sprintf("%d.git", resp.Base.RepositoryId))

	// git gc
	cmd = exec.Command("git", "gc")
	cmd.Dir = baseRepoPath
	err = cmd.Run()
	if err != nil {
		return errors.WithMessage(err, "git gc error")
	}

	packfileName, err := utils.GetPackfileName(baseRepoPath)
	if err != nil {
		return errors.WithMessage(err, "get packfile name error")
	}

	cid, err := utils.PinFile(h.ipfsClusterClient, packfileName)
	if err != nil {
		return errors.WithMessage(err, "pin packfile error")
	}

	// fetch older packfile details from gitopia
	packfile, err := h.gc.RepositoryPackfile(context.Background(), resp.Base.RepositoryId)
	if err != nil {
		return errors.WithMessage(err, "get packfile details error")
	}

	if packfile.Cid != "" && packfile.Cid != cid {
		err = utils.UnpinFile(h.ipfsClusterClient, packfile.Cid)
		if err != nil {
			return errors.WithMessage(err, "unpin packfile error")
		}
	}

	// Get packfile from IPFS cluster
	ipfsHttpApi, err := rpc.NewURLApiWithClient(fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")), &http.Client{})
	if err != nil {
		return errors.WithMessage(err, "create IPFS API error")
	}

	p, err := ipfspath.NewPath("/ipfs/" + cid)
	if err != nil {
		return errors.WithMessage(err, "create path error")
	}

	f, err := ipfsHttpApi.Unixfs().Get(context.Background(), p)
	if err != nil {
		return errors.WithMessage(err, "get packfile from IPFS error")
	}

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid packfile format")
	}

	rootHash, err := merkleproof.ComputePackfileMerkleRoot(file, 256*1024)
	if err != nil {
		return errors.WithMessage(err, "compute packfile merkle root error")
	}

	// Get packfile size
	packfileInfo, err := os.Stat(packfileName)
	if err != nil {
		return errors.WithMessage(err, "get packfile size error")
	}

	err = h.gc.UpdateRepositoryPackfile(context.Background(), resp.Base.RepositoryId, filepath.Base(packfileName), cid, rootHash, packfileInfo.Size())
	if err != nil {
		return errors.WithMessage(err, "update repository packfile error")
	}

	logger.FromContext(ctx).
		WithField("creator", event.Creator).
		WithField("repository-id", event.RepositoryId).
		WithField("pull-request-id", event.PullRequestIid).
		WithField("task-id", event.TaskId).
		WithField("tx-height", event.TxHeight).
		Info("merged pull request")
	return nil
}

// run asynchronously
// read offset until which tx's are processed
// fetch missed txs
// fetch repository information for missed txs
// process setRepositoryEvent
func (h *InvokeMergePullRequestEventHandler) BackfillMissedEvents(ctx context.Context) (<-chan struct{}, chan error) {
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
				Events: []string{"message.action='InvokeMergePullRequest'",
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
					case "InvokeMergePullRequest":
						attributeMap := make(map[string]string)
						for i := 0; i < len(e.Attributes); i++ {
							attributeMap[string(e.Attributes[i].Key)] = string(e.Attributes[i].Value)
						}

						repoId, err := strconv.ParseUint(attributeMap["RepositoryId"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing repo id")
							return
						}

						prIid, err := strconv.ParseUint(attributeMap["PullRequestIid"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing pull request iid")
							return
						}

						taskId, err := strconv.ParseUint(attributeMap["TaskId"], 10, 64)
						if err != nil {
							errChan <- errors.WithMessage(err, "error parsing task id")
							return
						}

						event := InvokeMergePullRequestEvent{
							Creator:        attributeMap["Creator"],
							RepositoryId:   repoId,
							PullRequestIid: prIid,
							TaskId:         taskId,
							TxHeight:       uint64(r.Height),
						}

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
