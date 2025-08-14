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

func NewGitopiaProxy(g gitopia.Client, batchTxMgr *BatchTxManager) *GitopiaProxy {
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

func (g *GitopiaProxy) UpdateRepositoryPackfile(ctx context.Context, repositoryId uint64, name string, cid string, rootHash []byte, size int64, oldCid string) error {
	msg := &storagetypes.MsgUpdateRepositoryPackfile{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Name:         name,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
		OldCid:       oldCid,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) ProposePackfileUpdate(ctx context.Context, user string, repositoryId uint64, name string, cid string, rootHash []byte, size int64, oldCid string, mergeCommitSha string, delete bool) error {
	msg := &storagetypes.MsgProposeRepositoryPackfileUpdate{
		Creator:        g.gc.Address().String(),
		User:           user,
		RepositoryId:   repositoryId,
		Name:           name,
		Cid:            cid,
		RootHash:       rootHash,
		Size_:          uint64(size),
		OldCid:         oldCid,
		MergeCommitSha: mergeCommitSha,
		Delete:         delete,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) ProposeRepositoryDelete(ctx context.Context, user string, repositoryId uint64) error {
	msg := &storagetypes.MsgProposeRepositoryDelete{
		Creator:      g.gc.Address().String(),
		User:         user,
		RepositoryId: repositoryId,
	}

	// Use batch transaction manager
	return g.batchTxMgr.AddToBatch(ctx, msg)
}

func (g *GitopiaProxy) SubmitChallenge(ctx context.Context, challengeId uint64, data []byte, proof *storagetypes.Proof) error {
	msg := &storagetypes.MsgSubmitChallengeResponse{
		Creator:     g.gc.Address().String(),
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

func (g *GitopiaProxy) UpdateReleaseAssets(ctx context.Context, repositoryId uint64, tag string, assets []*storagetypes.ReleaseAssetUpdate) error {
	msg := &storagetypes.MsgUpdateReleaseAssets{
		Creator:      g.gc.Address().String(),
		RepositoryId: repositoryId,
		Tag:          tag,
		Assets:       assets,
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

func (g *GitopiaProxy) StorageParams(ctx context.Context) (storagetypes.Params, error) {
	resp, err := g.gc.QueryClient().Storage.Params(ctx, &storagetypes.QueryParamsRequest{})
	if err != nil {
		return storagetypes.Params{}, errors.WithMessage(err, "query error")
	}

	return resp.Params, nil
}

// ProposeReleaseAssetsUpdate submits a proposal to update multiple release assets for a given tag
func (g *GitopiaProxy) ProposeReleaseAssetsUpdate(ctx context.Context, user string, repositoryId uint64, tag string, assets []*storagetypes.ReleaseAssetUpdate) error {
	msg := &storagetypes.MsgProposeReleaseAssetsUpdate{
		Creator:      g.gc.Address().String(),
		User:         user,
		RepositoryId: repositoryId,
		Tag:          tag,
		Assets:       assets,
	}

	return g.batchTxMgr.AddToBatch(ctx, msg)
}

// ReleaseAssetsUpdateProposal fetches a release assets update proposal for a repo, tag and user
func (g *GitopiaProxy) ReleaseAssetsUpdateProposal(ctx context.Context, repositoryId uint64, tag string, user string) (storagetypes.ProposedReleaseAssetsUpdate, error) {
	resp, err := g.gc.QueryClient().Storage.ReleaseAssetsUpdateProposal(ctx, &storagetypes.QueryReleaseAssetsUpdateProposalRequest{
		RepositoryId: repositoryId,
		Tag:          tag,
		User:         user,
	})
	if err != nil {
		return storagetypes.ProposedReleaseAssetsUpdate{}, errors.WithMessage(err, "query error")
	}

	return resp.ReleaseAssetsProposal, nil
}

// CheckProposeReleaseAssetsUpdate verifies if a release assets update proposal exists
func (g *GitopiaProxy) CheckProposeReleaseAssetsUpdate(repositoryId uint64, tag string, user string) (bool, error) {
	proposal, err := g.ReleaseAssetsUpdateProposal(context.Background(), repositoryId, tag, user)
	if err != nil {
		if strings.Contains(err.Error(), "release assets update proposal not found") {
			return false, nil
		}
		return false, err
	}

	return len(proposal.Assets) > 0, nil
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

func (g *GitopiaProxy) ProposeLFSObjectUpdate(ctx context.Context, user string, repositoryId uint64, oid string, cid string, rootHash []byte, size int64, delete bool) error {
	msg := &storagetypes.MsgProposeLFSObjectUpdate{
		Creator:      g.gc.Address().String(),
		User:         user,
		RepositoryId: repositoryId,
		Oid:          oid,
		Cid:          cid,
		RootHash:     rootHash,
		Size_:        uint64(size),
		Delete:       delete,
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

// CheckProposePackfileUpdate verifies if a packfile update was proposed
func (g *GitopiaProxy) CheckProposePackfileUpdate(repositoryId uint64, user string) (bool, error) {
	packfileUpdateProposal, err := g.PackfileUpdateProposal(context.Background(), repositoryId, user)
	if err != nil {
		if strings.Contains(err.Error(), "packfile update proposal not found") {
			return false, nil // Return false but no error to continue polling
		}
		return false, err
	}

	return packfileUpdateProposal.Cid != "", nil
}

// CheckProposeRepositoryDelete verifies if a repository delete was proposed
func (g *GitopiaProxy) CheckProposeRepositoryDelete(repositoryId uint64, user string) (bool, error) {
	_, err := g.RepositoryDeleteProposal(context.Background(), repositoryId, user)
	if err != nil {
		if strings.Contains(err.Error(), "repository delete proposal not found") {
			return false, nil // Return false but no error to continue polling
		}
		return false, err
	}

	return true, nil
}

// CheckProposeLFSObjectUpdate verifies if an LFS object update was proposed
func (g *GitopiaProxy) CheckProposeLFSObjectUpdate(repositoryId uint64, oid string, user string) (bool, error) {
	lfsObjectUpdateProposal, err := g.LFSObjectUpdateProposal(context.Background(), repositoryId, oid, user)
	if err != nil {
		if strings.Contains(err.Error(), "lfs object update proposal not found") {
			return false, nil // Return false but no error to continue polling
		}
		return false, err
	}

	return lfsObjectUpdateProposal.Cid != "", nil
}

// CheckPullRequestUpdate verifies if a pull request update was applied
func (g *GitopiaProxy) CheckPullRequestUpdate(repositoryId uint64, pullRequestIid uint64, mergeCommitSha string) (bool, error) {
	pullRequest, err := g.PullRequest(context.Background(), repositoryId, pullRequestIid)
	if err != nil {
		return false, err
	}
	return pullRequest.MergeCommitSha == mergeCommitSha, nil
}

// PackfileUpdateProposal returns the packfile update proposal for a repository and user
func (g *GitopiaProxy) PackfileUpdateProposal(ctx context.Context, repositoryId uint64, user string) (storagetypes.ProposedPackfileUpdate, error) {
	resp, err := g.gc.QueryClient().Storage.PackfileUpdateProposal(ctx, &storagetypes.QueryPackfileUpdateProposalRequest{
		RepositoryId: repositoryId,
		User:         user,
	})
	if err != nil {
		return storagetypes.ProposedPackfileUpdate{}, errors.WithMessage(err, "query error")
	}

	return resp.PackfileUpdateProposal, nil
}

// RepositoryDeleteProposal returns the repository delete proposal for a repository and user
func (g *GitopiaProxy) RepositoryDeleteProposal(ctx context.Context, repositoryId uint64, user string) (storagetypes.ProposedRepositoryDelete, error) {
	resp, err := g.gc.QueryClient().Storage.RepositoryDeleteProposal(ctx, &storagetypes.QueryRepositoryDeleteProposalRequest{
		RepositoryId: repositoryId,
		User:         user,
	})
	if err != nil {
		return storagetypes.ProposedRepositoryDelete{}, errors.WithMessage(err, "query error")
	}

	return resp.RepositoryDeleteProposal, nil
}

// LFSObjectUpdateProposal returns the LFS object update proposal for a repository and user
func (g *GitopiaProxy) LFSObjectUpdateProposal(ctx context.Context, repositoryId uint64, oid string, user string) (storagetypes.ProposedLFSObjectUpdate, error) {
	resp, err := g.gc.QueryClient().Storage.LFSObjectUpdateProposal(ctx, &storagetypes.QueryLFSObjectUpdateProposalRequest{
		RepositoryId: repositoryId,
		Oid:          oid,
		User:         user,
	})
	if err != nil {
		return storagetypes.ProposedLFSObjectUpdate{}, errors.WithMessage(err, "query error")
	}

	return resp.LfsObjectProposal, nil
}

// RepositoryReleaseAll return all releases for a repository
func (g *GitopiaProxy) RepositoryReleaseAll(ctx context.Context, ownerId string, repositoryName string) ([]*types.Release, error) {
	resp, err := g.gc.QueryClient().Gitopia.RepositoryReleaseAll(ctx, &types.QueryAllRepositoryReleaseRequest{
		Id:             ownerId,
		RepositoryName: repositoryName,
	})
	if err != nil {
		return nil, errors.WithMessage(err, "query error")
	}

	return resp.Release, nil
}
