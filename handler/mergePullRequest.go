package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
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
)

const (
	maxErrorLength  = 255
	defaultTimeout  = 5 * time.Minute
	merkleChunkSize = 256 * 1024
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
}

func NewInvokeMergePullRequestEventHandler(g app.GitopiaProxy, c consumer.Client, ipfsClusterClient ipfsclusterclient.Client) InvokeMergePullRequestEventHandler {
	return InvokeMergePullRequestEventHandler{
		gc:                g,
		cc:                c,
		ipfsClusterClient: ipfsClusterClient,
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

	return nil
}

func (h *InvokeMergePullRequestEventHandler) Process(ctx context.Context, event InvokeMergePullRequestEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	h.logOperation(ctx, "process_merge_pull_request", map[string]interface{}{
		"creator":          event.Creator,
		"repository_id":    event.RepositoryId,
		"pull_request_iid": event.PullRequestIid,
		"task_id":          event.TaskId,
		"tx_height":        event.TxHeight,
	})

	// Check task state
	res, err := h.gc.Task(ctx, event.TaskId)
	if err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "task query error")
	}
	if res.State != types.StatePending {
		return nil
	}

	// Get pull request details
	resp, err := h.gc.PullRequest(ctx, event.RepositoryId, event.PullRequestIid)
	if err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "pull request query error")
	}

	// Validate repository state
	if err := h.validateRepositoryState(ctx, resp); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "repository validation error")
	}

	// Prepare repositories
	cacheDir := viper.GetString("GIT_DIR")
	if err := h.prepareRepositories(ctx, resp, cacheDir); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "repository preparation error")
	}

	// Get repository name
	headRepositoryName, err := h.gc.RepositoryName(ctx, resp.Head.RepositoryId)
	if err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "repository name query error")
	}

	message := fmt.Sprintf("Merge pull request #%v from %s/%s", resp.Iid, headRepositoryName, resp.Head.Branch)

	// Create quarantine repository
	quarantineRepoPath, err := utils.CreateQuarantineRepo(resp.Base.RepositoryId, resp.Head.RepositoryId, resp.Base.Branch, resp.Head.Branch)
	if err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "create quarantine repo error")
	}
	defer os.RemoveAll(quarantineRepoPath)

	// Prepare environment variables
	env := h.prepareGitEnv(event.Creator)

	// Perform merge
	mergeStyle := utils.MergeStyleMerge
	if err := h.performMerge(ctx, mergeStyle, resp, quarantineRepoPath, env, message); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "merge operation error")
	}

	// Get merge commit SHA
	mergeCommitSha, err := utils.GetFullCommitSha(quarantineRepoPath, "base")
	if err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "merge commit sha error")
	}

	// Push changes
	if err := h.pushChanges(ctx, resp, quarantineRepoPath, env, mergeStyle); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "push changes error")
	}

	// Handle IPFS operations
	if err := h.handlePostMergeOperations(ctx, resp, cacheDir); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "post-merge operations error")
	}

	// Update pull request state and repository branch reference
	if err := h.gc.MergePullRequest(ctx, event.RepositoryId, event.PullRequestIid, mergeCommitSha, event.TaskId); err != nil {
		return h.handleError(ctx, err, event.Creator, event.TaskId, "set pull request state error")
	}

	h.logOperation(ctx, "merge_completed", map[string]interface{}{
		"creator":          event.Creator,
		"repository_id":    event.RepositoryId,
		"pull_request_iid": event.PullRequestIid,
		"task_id":          event.TaskId,
		"tx_height":        event.TxHeight,
	})
	return nil
}

// prepareRepositories ensures repositories are cached and available
func (h *InvokeMergePullRequestEventHandler) prepareRepositories(ctx context.Context, resp types.PullRequest, cacheDir string) error {
	// Check and cache head repository
	isCached, err := utils.IsRepoCached(resp.Head.RepositoryId, cacheDir)
	if err != nil {
		return err
	}
	if !isCached {
		if err := utils.DownloadRepo(resp.Head.RepositoryId, cacheDir); err != nil {
			return err
		}
	}

	// Check and cache base repository if different
	if resp.Base.RepositoryId != resp.Head.RepositoryId {
		isCached, err := utils.IsRepoCached(resp.Base.RepositoryId, cacheDir)
		if err != nil {
			return err
		}
		if !isCached {
			if err := utils.DownloadRepo(resp.Base.RepositoryId, cacheDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// prepareGitEnv prepares environment variables for git operations
func (h *InvokeMergePullRequestEventHandler) prepareGitEnv(creator string) []string {
	commitTimeStr := time.Now().Format(time.RFC3339)
	return append(os.Environ(),
		"GIT_AUTHOR_NAME="+creator,
		"GIT_AUTHOR_EMAIL=<>",
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+creator,
		"GIT_COMMITTER_EMAIL=<>",
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)
}

// performMerge executes the merge operation based on the merge style
func (h *InvokeMergePullRequestEventHandler) performMerge(ctx context.Context, mergeStyle utils.MergeStyle, resp types.PullRequest, quarantineRepoPath string, env []string, message string) error {
	switch mergeStyle {
	case utils.MergeStyleMerge:
		return h.performMergeStyle(ctx, resp, quarantineRepoPath, env, message)
	case utils.MergeStyleRebase:
		return h.performRebaseStyle(ctx, resp, quarantineRepoPath, env, message)
	case utils.MergeStyleSquash:
		return h.performSquashStyle(ctx, resp, quarantineRepoPath, env, message)
	default:
		return fmt.Errorf("invalid merge style: %v", mergeStyle)
	}
}

// pushChanges pushes the merged changes to the repository
func (h *InvokeMergePullRequestEventHandler) pushChanges(ctx context.Context, resp types.PullRequest, quarantineRepoPath string, env []string, mergeStyle utils.MergeStyle) error {
	var pushCmd *exec.Cmd
	if mergeStyle == utils.MergeStyleRebaseUpdate {
		pushCmd = exec.Command("git", "push", "-f", "head_repo", "staging:refs/heads/"+resp.Head.Branch)
	} else {
		pushCmd = exec.Command("git", "push", "origin", "base:refs/heads/"+resp.Base.Branch)
	}

	pushCmd.Env = env
	pushCmd.Dir = quarantineRepoPath
	return h.runGitCommandWithTimeout(ctx, pushCmd, defaultTimeout)
}

// handlePostMergeOperations handles operations after successful merge
func (h *InvokeMergePullRequestEventHandler) handlePostMergeOperations(ctx context.Context, resp types.PullRequest, cacheDir string) error {
	baseRepoPath := filepath.Join(cacheDir, fmt.Sprintf("%d.git", resp.Base.RepositoryId))

	// Run git gc
	cmd := exec.Command("git", "gc")
	cmd.Dir = baseRepoPath
	if err := h.runGitCommandWithTimeout(ctx, cmd, defaultTimeout); err != nil {
		return errors.WithMessage(err, "git gc error")
	}

	// Get packfile name
	packfileName, err := utils.GetPackfileName(baseRepoPath)
	if err != nil {
		return errors.WithMessage(err, "get packfile name error")
	}

	// Handle IPFS operations
	cid, err := h.handleIPFSOperations(ctx, packfileName, resp.Base.RepositoryId)
	if err != nil {
		return errors.WithMessage(err, "IPFS operations error")
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

	f, err := ipfsHttpApi.Unixfs().Get(ctx, p)
	if err != nil {
		return errors.WithMessage(err, "get packfile from IPFS error")
	}

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid packfile format")
	}

	rootHash, err := merkleproof.ComputePackfileMerkleRoot(file, merkleChunkSize)
	if err != nil {
		return errors.WithMessage(err, "compute packfile merkle root error")
	}

	// Get packfile size
	packfileInfo, err := os.Stat(packfileName)
	if err != nil {
		return errors.WithMessage(err, "get packfile size error")
	}

	return h.gc.UpdateRepositoryPackfile(ctx, resp.Base.RepositoryId, filepath.Base(packfileName), cid, rootHash, packfileInfo.Size())
}

// handleError is a helper function for common error handling pattern
func (h *InvokeMergePullRequestEventHandler) handleError(ctx context.Context, err error, creator string, taskId uint64, message string) error {
	if err == nil {
		return nil
	}
	// Truncate error message if needed
	errMsg := err.Error()
	if len(errMsg) > maxErrorLength {
		errMsg = errMsg[:maxErrorLength]
	}
	updateErr := h.gc.UpdateTask(ctx, creator, taskId, types.StateFailure, errMsg)
	if updateErr != nil {
		return errors.WithMessage(updateErr, "update task error")
	}
	return errors.WithMessage(err, message)
}

// runGitCommandWithTimeout executes a git command with a timeout
func (h *InvokeMergePullRequestEventHandler) runGitCommandWithTimeout(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Dir = cmd.Dir
	cmd.Env = cmd.Env
	return cmd.Run()
}

// validateRepositoryState checks if the repository is in a valid state for merging
func (h *InvokeMergePullRequestEventHandler) validateRepositoryState(ctx context.Context, resp types.PullRequest) error {
	// Check if branches exist
	if err := h.validateBranches(ctx, resp); err != nil {
		return err
	}

	// Check if repository is still accessible
	if err := h.validateRepositoryAccess(ctx, resp); err != nil {
		return err
	}

	return nil
}

// validateBranches checks if the required branches exist
func (h *InvokeMergePullRequestEventHandler) validateBranches(ctx context.Context, resp types.PullRequest) error {
	// TODO: Implement branch validation
	return nil
}

// validateRepositoryAccess checks if the repository is still accessible
func (h *InvokeMergePullRequestEventHandler) validateRepositoryAccess(ctx context.Context, resp types.PullRequest) error {
	// TODO: Implement repository access validation
	return nil
}

// handleIPFSOperations handles IPFS-related operations concurrently
func (h *InvokeMergePullRequestEventHandler) handleIPFSOperations(ctx context.Context, packfileName string, repositoryId uint64) (string, error) {
	errChan := make(chan error, 2)
	cidChan := make(chan string, 1)

	go func() {
		cid, err := utils.PinFile(h.ipfsClusterClient, packfileName)
		if err != nil {
			errChan <- err
			return
		}
		cidChan <- cid
		errChan <- nil
	}()

	go func() {
		_, err := h.gc.RepositoryPackfile(ctx, repositoryId)
		errChan <- err
	}()

	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			return "", err
		}
	}

	return <-cidChan, nil
}

// logOperation adds structured logging with context
func (h *InvokeMergePullRequestEventHandler) logOperation(ctx context.Context, operation string, fields map[string]interface{}) {
	logger.FromContext(ctx).
		WithFields(fields).
		WithField("operation", operation).
		Info("processing operation")
}

// performMergeStyle handles the standard merge style
func (h *InvokeMergePullRequestEventHandler) performMergeStyle(ctx context.Context, resp types.PullRequest, quarantineRepoPath string, env []string, message string) error {
	cmd := exec.Command("git", "merge", "--no-ff", "--no-commit", "tracking")
	cmd.Env = env
	if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
		return errors.WithMessage(err, "merge error")
	}

	if err := utils.CommitAndSignNoAuthor(resp, message, "", quarantineRepoPath, env); err != nil {
		return errors.WithMessage(err, "merge commit error")
	}
	return nil
}

// performRebaseStyle handles the rebase merge style
func (h *InvokeMergePullRequestEventHandler) performRebaseStyle(ctx context.Context, resp types.PullRequest, quarantineRepoPath string, env []string, message string) error {
	// Checkout head branch
	cmd := exec.Command("git", "checkout", "-b", "staging", "tracking")
	cmd.Dir = quarantineRepoPath
	if err := h.runGitCommandWithTimeout(ctx, cmd, defaultTimeout); err != nil {
		return errors.WithMessage(err, "git checkout error")
	}

	// Rebase before merging
	cmd = exec.Command("git", "rebase", "base")
	cmd.Dir = quarantineRepoPath
	if err := h.runGitCommandWithTimeout(ctx, cmd, defaultTimeout); err != nil {
		// Check for rebase conflicts
		if _, statErr := os.Stat(filepath.Join(quarantineRepoPath, ".git", "REBASE_HEAD")); statErr == nil {
			return errors.WithMessage(err, "rebase conflict error")
		}
		return errors.WithMessage(err, "rebase error")
	}

	// Checkout base branch again
	cmd = exec.Command("git", "checkout", "base")
	cmd.Dir = quarantineRepoPath
	if err := h.runGitCommandWithTimeout(ctx, cmd, defaultTimeout); err != nil {
		return errors.WithMessage(err, "git checkout error")
	}

	// Merge with fast-forward
	cmd = exec.Command("git", "merge", "--ff-only", "staging")
	if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
		return errors.WithMessage(err, "git merge error")
	}

	return nil
}

// performSquashStyle handles the squash merge style
func (h *InvokeMergePullRequestEventHandler) performSquashStyle(ctx context.Context, resp types.PullRequest, quarantineRepoPath string, env []string, message string) error {
	// Merge with squash
	cmd := exec.Command("git", "merge", "--squash", "tracking")
	if err := utils.RunMergeCommand(*resp.Head, *resp.Base, cmd, quarantineRepoPath); err != nil {
		return errors.WithMessage(err, "git merge --squash error")
	}

	cmd = exec.Command("git", "commit", fmt.Sprintf("--author='%s <%s>'", resp.Creator, "<>"), "-m", message)
	cmd.Env = env
	cmd.Dir = quarantineRepoPath
	return h.runGitCommandWithTimeout(ctx, cmd, defaultTimeout)
}
