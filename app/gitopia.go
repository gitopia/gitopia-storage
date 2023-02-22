package app

import (
	"context"

	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
	GAS_ADJUSTMENT             = 1.5
	MAX_TRIES                  = 5
	MAX_WAIT_BLOCKS            = 10
)

type GitopiaProxy struct {
	gc gitopia.Client
}

func NewGitopiaProxy(g gitopia.Client) GitopiaProxy {
	return GitopiaProxy{g}
}

func (g GitopiaProxy) ForkRepository(ctx context.Context, creator string, repositoryId types.RepositoryId, owner string, taskId uint64) error {
	msg := &types.MsgForkRepository{
		Creator:      creator,
		RepositoryId: repositoryId,
		Owner:        owner,
		TaskId:       taskId,
	}

	err := g.gc.AuthorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
	}

	return nil
}

func (g GitopiaProxy) ForkRepositorySuccess(ctx context.Context, creator string, repositoryId types.RepositoryId, taskId uint64) error {
	msg := &types.MsgForkRepositorySuccess{
		Creator:      creator,
		RepositoryId: repositoryId,
		TaskId:       taskId,
	}

	err := g.gc.AuthorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
	}

	return nil
}

func (g GitopiaProxy) UpdateTask(ctx context.Context, creator string, id uint64, state types.TaskState, message string) error {
	msg := &types.MsgUpdateTask{
		Creator: creator,
		Id:      id,
		State:   state,
		Message: message,
	}

	err := g.gc.AuthorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
	}

	return nil
}

func (g GitopiaProxy) SetPullRequestState(ctx context.Context, creator string, repositoryId, iid uint64, state string, mergeCommitSha string, taskId uint64) error {
	msg := &types.MsgSetPullRequestState{
		Creator:        creator,
		RepositoryId:   repositoryId,
		Iid:            iid,
		State:          state,
		MergeCommitSha: mergeCommitSha,
		TaskId:         taskId,
	}

	err := g.gc.AuthorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
	}

	return nil
}

func (g GitopiaProxy) RepositoryName(ctx context.Context, id uint64) (string, error) {
	resp, err := g.gc.QueryClient().Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: id,
	})
	if err != nil {
		return "", errors.WithMessage(err, "query error")
	}

	return resp.Repository.Name, nil
}

func (g GitopiaProxy) CheckGitServerAuthorization(ctx context.Context, userAddress string) (bool, error) {
	resp, err := g.gc.QueryClient().CheckGitServerAuthorization(ctx, &types.QueryCheckGitServerAuthorizationRequest{
		UserAddress:     userAddress,
		ProviderAddress: g.gc.Address().String(),
	})
	if err != nil {
		return false, errors.WithMessage(err, "query error")
	}

	return resp.HaveAuthorization, nil
}

func (g GitopiaProxy) RepositoryId(ctx context.Context, address string, repoName string) (uint64, error) {
	resp, err := g.gc.QueryClient().AnyRepository(ctx, &types.QueryGetAnyRepositoryRequest{
		Id:             address,
		RepositoryName: repoName,
	})
	if err != nil {
		return 0, errors.WithMessage(err, "query error")
	}

	return resp.Repository.Id, nil
}

func (g GitopiaProxy) PullRequest(ctx context.Context, repositoryId uint64, pullRequestIid uint64) (types.PullRequest, error) {
	repoResp, err := g.gc.QueryClient().Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: repositoryId,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	prResp, err := g.gc.QueryClient().RepositoryPullRequest(ctx, &types.QueryGetRepositoryPullRequestRequest{
		Id:             repoResp.Repository.Owner.Id,
		RepositoryName: repoResp.Repository.Name,
		PullIid:        pullRequestIid,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	return *prResp.PullRequest, nil
}

func (g GitopiaProxy) Task(ctx context.Context, id uint64) (types.Task, error) {
	res, err := g.gc.QueryClient().Task(ctx, &types.QueryGetTaskRequest{
		Id: id,
	})
	if err != nil {
		return types.Task{}, errors.Wrap(err, "query error")
	}

	return res.Task, nil
}
