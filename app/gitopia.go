package app

import (
	"context"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
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
	BLOCK_TIME                 = 1600 * time.Millisecond
)

type GitopiaProxy struct {
	gc         gitopia.Client
	batchTxMgr *BatchTxManager
}

func NewGitopiaProxy(g gitopia.Client) *GitopiaProxy {
	// Initialize batch transaction manager with 1.6 second batch interval
	batchTxMgr := NewBatchTxManager(g, BLOCK_TIME)
	batchTxMgr.Start()

	return &GitopiaProxy{
		gc:         g,
		batchTxMgr: batchTxMgr,
	}
}

// Stop gracefully shuts down the GitopiaProxy and its batch transaction manager
func (g *GitopiaProxy) Stop() {
	if g.batchTxMgr != nil {
		g.batchTxMgr.Stop()
	}
}

func (g *GitopiaProxy) UpdateTask(ctx context.Context, id uint64, state types.TaskState, message string) error {
	msg := &types.MsgUpdateTask{
		Creator: g.gc.Address().String(),
		Id:      id,
		State:   state,
		Message: message,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
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

func (g *GitopiaProxy) RepositoryId(ctx context.Context, address string, repoName string) (uint64, error) {
	resp, err := g.gc.QueryClient().Gitopia.AnyRepository(ctx, &types.QueryGetAnyRepositoryRequest{
		Id:             address,
		RepositoryName: repoName,
	})
	if err != nil {
		return 0, errors.WithMessage(err, "query error")
	}

	return resp.Repository.Id, nil
}

func (g *GitopiaProxy) PullRequest(ctx context.Context, repositoryId uint64, pullRequestIid uint64) (types.PullRequest, error) {
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

func (g *GitopiaProxy) Task(ctx context.Context, id uint64) (types.Task, error) {
	res, err := g.gc.QueryClient().Gitopia.Task(ctx, &types.QueryGetTaskRequest{
		Id: id,
	})
	if err != nil {
		return types.Task{}, errors.Wrap(err, "query error")
	}

	return res.Task, nil
}

func (g *GitopiaProxy) Repository(ctx context.Context, repositoryId uint64) (types.Repository, error) {
	resp, err := g.gc.QueryClient().Gitopia.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: repositoryId,
	})
	if err != nil {
		return types.Repository{}, errors.WithMessage(err, "query error")
	}

	return *resp.Repository, nil
}

func (g *GitopiaProxy) UpdateRepositoryPackfile(ctx context.Context, repositoryId uint64, name string, cid string, rootHash []byte, size int64) error {
	msg := &storagetypes.MsgUpdateRepositoryPackfile{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Name:         name,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) DeleteRepositoryPackfile(ctx context.Context, repositoryId uint64, ownerId string) error {
	msg := &storagetypes.MsgDeleteRepositoryPackfile{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		OwnerId:      ownerId,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) SubmitChallenge(ctx context.Context, creator string, challengeId uint64, data []byte, proof *storagetypes.Proof) error {
	msg := &storagetypes.MsgSubmitChallengeResponse{
		Creator:     creator,
		ChallengeId: challengeId,
		Data:        data,
		Proof:       proof,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) Challenge(ctx context.Context, id uint64) (storagetypes.Challenge, error) {
	resp, err := g.gc.QueryClient().Storage.Challenge(ctx, &storagetypes.QueryChallengeRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.Challenge{}, errors.WithMessage(err, "query error")
	}

	return resp.Challenge, nil
}

func (g *GitopiaProxy) Packfile(ctx context.Context, id uint64) (storagetypes.Packfile, error) {
	resp, err := g.gc.QueryClient().Storage.Packfile(ctx, &storagetypes.QueryPackfileRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.Packfile{}, errors.WithMessage(err, "query error")
	}

	return resp.Packfile, nil
}

// CheckProvider checks if the given provider address matches the current node's address
func (g *GitopiaProxy) CheckProvider(providerAddress string) bool {
	return g.gc.Address().String() == providerAddress
}

// ClientAddress returns the client's address as a string
func (g *GitopiaProxy) ClientAddress() string {
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

func (g *GitopiaProxy) UpdateReleaseAsset(ctx context.Context, repositoryId uint64, tag string, name string, cid string, rootHash []byte, size int64, sha256 string) error {
	msg := &storagetypes.MsgUpdateReleaseAsset{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Tag:          tag,
		Name:         name,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
		Sha256:       sha256,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) RepositoryReleaseAsset(ctx context.Context, repositoryId uint64, tag string, name string) (storagetypes.ReleaseAsset, error) {
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

func (g *GitopiaProxy) RepositoryReleaseAssets(ctx context.Context, repositoryId uint64, tag string) ([]storagetypes.ReleaseAsset, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryReleaseAssets(ctx, &storagetypes.QueryRepositoryReleaseAssetsRequest{
		RepositoryId: repositoryId,
		Tag:          tag,
	})
	if err != nil {
		return nil, errors.WithMessage(err, "query error")
	}

	return resp.ReleaseAssets, nil
}

func (g *GitopiaProxy) RepositoryPackfile(ctx context.Context, repositoryId uint64) (storagetypes.Packfile, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryPackfile(ctx, &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: repositoryId,
	})
	if err != nil {
		return storagetypes.Packfile{}, errors.WithMessage(err, "query error")
	}

	return resp.Packfile, nil
}

func (g *GitopiaProxy) MergePullRequest(ctx context.Context, repositoryId uint64, pullRequestIid uint64, mergeCommitSha string, taskId uint64) error {
	msg := &storagetypes.MsgMergePullRequest{
		Creator:        g.gc.Address().String(),
		RepositoryId:   repositoryId,
		PullRequestIid: pullRequestIid,
		MergeCommitSha: mergeCommitSha,
		TaskId:         taskId,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) StorageParams(ctx context.Context) (storagetypes.Params, error) {
	resp, err := g.gc.QueryClient().Storage.Params(ctx, &storagetypes.QueryParamsRequest{})
	if err != nil {
		return storagetypes.Params{}, errors.WithMessage(err, "query error")
	}

	return resp.Params, nil
}

func (g *GitopiaProxy) CosmosBankBalance(ctx context.Context, address, denom string) (sdk.Coin, error) {
	resp, err := g.gc.QueryClient().Bank.Balance(ctx, &banktypes.QueryBalanceRequest{
		Address: address,
		Denom:   denom,
	})
	if err != nil {
		return sdk.Coin{}, err
	}

	return *resp.Balance, nil
}

func (g *GitopiaProxy) StorageCidReferenceCount(ctx context.Context, cid string) (uint64, error) {
	resp, err := g.gc.QueryClient().Storage.CidReferenceCount(ctx, &storagetypes.QueryCidReferenceCountRequest{
		Cid: cid,
	})
	if err != nil {
		return 0, errors.WithMessage(err, "query error")
	}

	return resp.Count, nil
}

func (g *GitopiaProxy) DeleteReleaseAsset(ctx context.Context, repositoryId uint64, tag string, name string, ownerId string) error {
	msg := &storagetypes.MsgDeleteReleaseAsset{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Tag:          tag,
		Name:         name,
		OwnerId:      ownerId,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) ActiveProviders(ctx context.Context) ([]storagetypes.Provider, error) {
	resp, err := g.gc.QueryClient().Storage.ActiveProviders(ctx, &storagetypes.QueryActiveProvidersRequest{})
	if err != nil {
		return nil, errors.WithMessage(err, "query error")
	}

	return resp.Providers, nil
}

// RepositoryReleaseAssetsByRepositoryId returns all release assets for a repository with pagination support
func (g *GitopiaProxy) RepositoryReleaseAssetsByRepositoryId(ctx context.Context, repositoryId uint64, nextKey []byte) ([]storagetypes.ReleaseAsset, []byte, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryReleaseAssetsByRepositoryId(ctx, &storagetypes.QueryRepositoryReleaseAssetsByRepositoryIdRequest{
		RepositoryId: repositoryId,
		Pagination: &query.PageRequest{
			Key: nextKey,
		},
	})
	if err != nil {
		return nil, nil, errors.WithMessage(err, "query error")
	}

	return resp.ReleaseAssets, resp.Pagination.NextKey, nil
}

// RepositoryReleaseAssetsByRepositoryIdAll returns all release assets for a repository (handles pagination internally)
func (g *GitopiaProxy) RepositoryReleaseAssetsByRepositoryIdAll(ctx context.Context, repositoryId uint64) ([]storagetypes.ReleaseAsset, error) {
	var allAssets []storagetypes.ReleaseAsset
	var nextKey []byte

	for {
		assets, next, err := g.RepositoryReleaseAssetsByRepositoryId(ctx, repositoryId, nextKey)
		if err != nil {
			return nil, err
		}

		allAssets = append(allAssets, assets...)

		if next == nil {
			break
		}

		nextKey = next
	}

	return allAssets, nil
}

func (g *GitopiaProxy) UpdateLFSObject(ctx context.Context, repositoryId uint64, oid string, cid string, rootHash []byte, size int64) error {
	msg := &storagetypes.MsgUpdateLFSObject{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Oid:          oid,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) DeleteLFSObject(ctx context.Context, repositoryId uint64, oid string, ownerId string) error {
	msg := &storagetypes.MsgDeleteLFSObject{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Oid:          oid,
		OwnerId:      ownerId,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) LFSObject(ctx context.Context, id uint64) (storagetypes.LFSObject, error) {
	resp, err := g.gc.QueryClient().Storage.LFSObject(ctx, &storagetypes.QueryLFSObjectRequest{
		Id: id,
	})
	if err != nil {
		return storagetypes.LFSObject{}, errors.WithMessage(err, "query error")
	}

	return resp.LfsObject, nil
}

func (g *GitopiaProxy) LFSObjectsByRepositoryId(ctx context.Context, repositoryId uint64) ([]storagetypes.LFSObject, error) {
	resp, err := g.gc.QueryClient().Storage.LFSObjectsByRepositoryId(ctx, &storagetypes.QueryLFSObjectsByRepositoryIdRequest{
		RepositoryId: repositoryId,
	})
	if err != nil {
		return nil, errors.WithMessage(err, "query error")
	}

	return resp.LfsObjects, nil
}

func (g *GitopiaProxy) LFSObjectByRepositoryIdAndOid(ctx context.Context, repositoryId uint64, oid string) (storagetypes.LFSObject, error) {
	resp, err := g.gc.QueryClient().Storage.LFSObjectByRepositoryIdAndOid(ctx, &storagetypes.QueryLFSObjectByRepositoryIdAndOidRequest{
		RepositoryId: repositoryId,
		Oid:          oid,
	})
	if err != nil {
		return storagetypes.LFSObject{}, errors.WithMessage(err, "query error")
	}

	return resp.LfsObject, nil
}

// PollForUpdate polls for an update until the checker function returns true or the context is cancelled
// checkerFn should return (success, error) where success indicates if the update was verified
func (g *GitopiaProxy) PollForUpdate(ctx context.Context, checkerFn func() (bool, error)) error {
	// Create a new context with timeout
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Initial check
	success, err := checkerFn()
	if err != nil {
		return err
	}
	if success {
		return nil
	}

	// Start polling
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			success, err := checkerFn()
			if err != nil {
				return err
			}
			if success {
				return nil
			}
		}
	}
}

// CheckPackfileUpdate verifies if a packfile update was applied
func (g *GitopiaProxy) CheckPackfileUpdate(repositoryId uint64, expectedCid string) (bool, error) {
	packfile, err := g.RepositoryPackfile(context.Background(), repositoryId)
	if err != nil {
		return false, err
	}
	return packfile.Cid == expectedCid, nil
}

// CheckPackfileDelete verifies if a packfile delete was applied
func (g *GitopiaProxy) CheckPackfileDelete(repositoryId uint64) (bool, error) {
	_, err := g.RepositoryPackfile(context.Background(), repositoryId)
	if err != nil && strings.Contains(err.Error(), "packfile not found") {
		return true, nil
	}
	return false, err
}

// CheckLFSObjectUpdate verifies if an LFS object update was applied
func (g *GitopiaProxy) CheckLFSObjectUpdate(repositoryId uint64, oid, expectedCid string) (bool, error) {
	lfsObject, err := g.LFSObjectByRepositoryIdAndOid(context.Background(), repositoryId, oid)
	if err != nil {
		return false, err
	}

	return lfsObject.Cid == expectedCid, nil
}

// CheckLFSObjectDelete verifies if an LFS object delete was applied
func (g *GitopiaProxy) CheckLFSObjectDelete(repositoryId uint64, oid string) (bool, error) {
	_, err := g.LFSObjectByRepositoryIdAndOid(context.Background(), repositoryId, oid)
	if err != nil && strings.Contains(err.Error(), "LFS object not found") {
		return true, nil
	}
	return false, err
}

// CheckReleaseAssetUpdate verifies if a release asset update was applied
func (g *GitopiaProxy) CheckReleaseAssetUpdate(repositoryId uint64, tag string, name string, expectedCid string) (bool, error) {
	releaseAsset, err := g.RepositoryReleaseAsset(context.Background(), repositoryId, tag, name)
	if err != nil {
		return false, err
	}
	return releaseAsset.Cid == expectedCid, nil
}

// CheckReleaseAssetDelete verifies if a release asset delete was applied
func (g *GitopiaProxy) CheckReleaseAssetDelete(repositoryId uint64, tag string, name string) (bool, error) {
	_, err := g.RepositoryReleaseAsset(context.Background(), repositoryId, tag, name)
	if err != nil && strings.Contains(err.Error(), "release asset not found") {
		return true, nil
	}
	return false, err
}

// CheckPullRequestUpdate verifies if a pull request update was applied
func (g *GitopiaProxy) CheckPullRequestUpdate(repositoryId uint64, pullRequestIid uint64, mergeCommitSha string) (bool, error) {
	pullRequest, err := g.PullRequest(context.Background(), repositoryId, pullRequestIid)
	if err != nil {
		return false, err
	}
	return pullRequest.MergeCommitSha == mergeCommitSha, nil
}
