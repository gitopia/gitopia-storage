package app

import (
	"context"

	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/pkg/errors"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
	GAS_ADJUSTMENT             = 1.8
	MAX_TRIES                  = 5
	MAX_WAIT_BLOCKS            = 10
)

type GitopiaProxy struct {
	gc gitopia.Client
}

func NewGitopiaProxy(g gitopia.Client) GitopiaProxy {
	return GitopiaProxy{g}
}

func (g GitopiaProxy) ForkRepository(ctx context.Context,
	creator string,
	repositoryId types.RepositoryId,
	forkRepositoryName string,
	forkRepositoryDescription string,
	branch string,
	owner string,
	taskId uint64) error {
	msg := &types.MsgForkRepository{
		Creator:                   creator,
		RepositoryId:              repositoryId,
		ForkRepositoryName:        forkRepositoryName,
		ForkRepositoryDescription: forkRepositoryDescription,
		Branch:                    branch,
		Owner:                     owner,
		TaskId:                    taskId,
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
	resp, err := g.gc.QueryClient().Gitopia.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: id,
	})
	if err != nil {
		return "", errors.WithMessage(err, "query error")
	}

	return resp.Repository.Name, nil
}

func (g GitopiaProxy) CheckGitServerAuthorization(ctx context.Context, userAddress string) (bool, error) {
	resp, err := g.gc.QueryClient().Gitopia.CheckGitServerAuthorization(ctx, &types.QueryCheckGitServerAuthorizationRequest{
		UserAddress:     userAddress,
		ProviderAddress: g.gc.Address().String(),
	})
	if err != nil {
		return false, errors.WithMessage(err, "query error")
	}

	return resp.HaveAuthorization, nil
}

func (g GitopiaProxy) RepositoryId(ctx context.Context, address string, repoName string) (uint64, error) {
	resp, err := g.gc.QueryClient().Gitopia.AnyRepository(ctx, &types.QueryGetAnyRepositoryRequest{
		Id:             address,
		RepositoryName: repoName,
	})
	if err != nil {
		return 0, errors.WithMessage(err, "query error")
	}

	return resp.Repository.Id, nil
}

func (g GitopiaProxy) PullRequest(ctx context.Context, repositoryId uint64, pullRequestIid uint64) (types.PullRequest, error) {
	repoResp, err := g.gc.QueryClient().Gitopia.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: repositoryId,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	prResp, err := g.gc.QueryClient().Gitopia.RepositoryPullRequest(ctx, &types.QueryGetRepositoryPullRequestRequest{
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
	res, err := g.gc.QueryClient().Gitopia.Task(ctx, &types.QueryGetTaskRequest{
		Id: id,
	})
	if err != nil {
		return types.Task{}, errors.Wrap(err, "query error")
	}

	return res.Task, nil
}

func (g GitopiaProxy) Repository(ctx context.Context, repositoryId uint64) (types.Repository, error) {
	resp, err := g.gc.QueryClient().Gitopia.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: repositoryId,
	})
	if err != nil {
		return types.Repository{}, errors.WithMessage(err, "query error")
	}

	return *resp.Repository, nil
}

func (g GitopiaProxy) UpdateRepositoryPackfile(ctx context.Context, repositoryId uint64, name string, cid string, rootHash []byte, size int64) error {
	msg := &storagetypes.MsgUpdateRepositoryPackfile{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Name:         name,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
	}

	err := g.gc.BroadcastTxAndWait(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending tx")
	}

	return nil
}

func (g GitopiaProxy) SubmitChallenge(ctx context.Context, creator string, challengeId uint64, data []byte, proof *storagetypes.Proof) error {
	msg := &storagetypes.MsgSubmitChallengeResponse{
		Creator:     creator,
		ChallengeId: challengeId,
		Data:        data,
		Proof:       proof,
	}

	err := g.gc.BroadcastTxAndWait(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending tx")
	}

	return nil
}

func (g GitopiaProxy) Challenge(ctx context.Context, id uint64) (storagetypes.Challenge, error) {
	resp, err := g.gc.QueryClient().Storage.Challenge(ctx, &storagetypes.QueryChallengeRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.Challenge{}, errors.WithMessage(err, "query error")
	}

	return resp.Challenge, nil
}

func (g GitopiaProxy) Packfile(ctx context.Context, id uint64) (storagetypes.Packfile, error) {
	resp, err := g.gc.QueryClient().Storage.Packfile(ctx, &storagetypes.QueryPackfileRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.Packfile{}, errors.WithMessage(err, "query error")
	}

	return resp.Packfile, nil
}

func (g GitopiaProxy) CheckProvider(provider string) bool {
	return provider == g.gc.Address().String()
}

// get client address
func (g GitopiaProxy) ClientAddress() string {
	return g.gc.Address().String()
}

func (g GitopiaProxy) UserQuota(ctx context.Context, address string) (types.UserQuota, error) {
	resp, err := g.gc.QueryClient().Gitopia.UserQuota(ctx, &types.QueryUserQuotaRequest{
		Address: address,
	})
	if err != nil {
		return types.UserQuota{}, errors.WithMessage(err, "query error")
	}

	return resp.UserQuota, nil
}

func (g GitopiaProxy) ReleaseAsset(ctx context.Context, id uint64) (storagetypes.ReleaseAsset, error) {
	resp, err := g.gc.QueryClient().Storage.ReleaseAsset(ctx, &storagetypes.QueryReleaseAssetRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.ReleaseAsset{}, errors.WithMessage(err, "query error")
	}

	return resp.ReleaseAsset, nil
}

func (g GitopiaProxy) UpdateReleaseAsset(ctx context.Context, repositoryId uint64, tag string, name string, cid string, rootHash []byte, size int64) error {
	msg := &storagetypes.MsgUpdateReleaseAsset{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Tag:          tag,
		Name:         name,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
	}

	err := g.gc.BroadcastTxAndWait(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending tx")
	}

	return nil
}

func (g GitopiaProxy) RepositoryReleaseAsset(ctx context.Context, repositoryId uint64, tag string, name string) (storagetypes.ReleaseAsset, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryReleaseAsset(ctx, &storagetypes.QueryRepositoryReleaseAssetRequest{
		RepositoryId: repositoryId,
		Tag:          tag,
		Name:         name,
	})
	if err != nil {
		return storagetypes.ReleaseAsset{}, errors.WithMessage(err, "query error")
	}

	return resp.ReleaseAsset, nil
}

func (g GitopiaProxy) RepositoryPackfile(ctx context.Context, repositoryId uint64) (storagetypes.Packfile, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryPackfile(ctx, &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: repositoryId,
	})
	if err != nil {
		return storagetypes.Packfile{}, errors.WithMessage(err, "query error")
	}

	return resp.Packfile, nil
}
