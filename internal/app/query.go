package app

import (
	"context"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	gc "github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
)

// QueryService defines the interface for querying Gitopia and Cosmos SDK modules.
type QueryService interface {
	GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error)
	GitopiaRepositoryBranches(ctx context.Context, req *gitopiatypes.QueryAllRepositoryBranchRequest) (*gitopiatypes.QueryAllRepositoryBranchResponse, error)
	GitopiaRepositoryTags(ctx context.Context, req *gitopiatypes.QueryAllRepositoryTagRequest) (*gitopiatypes.QueryAllRepositoryTagResponse, error)
	GitopiaRepository(ctx context.Context, req *gitopiatypes.QueryGetRepositoryRequest) (*gitopiatypes.QueryGetRepositoryResponse, error)
	GitopiaUserQuota(ctx context.Context, req *gitopiatypes.QueryUserQuotaRequest) (*gitopiatypes.QueryUserQuotaResponse, error)
	StorageParams(ctx context.Context, req *storagetypes.QueryParamsRequest) (*storagetypes.QueryParamsResponse, error)
	CosmosBankBalance(ctx context.Context, req *banktypes.QueryBalanceRequest) (*banktypes.QueryBalanceResponse, error)
	StorageCidReferenceCount(ctx context.Context, req *storagetypes.QueryCidReferenceCountRequest) (*storagetypes.QueryCidReferenceCountResponse, error)
}

// QueryServiceImpl implements the QueryService interface.
type QueryServiceImpl struct {
	Query *gc.Query
}

func (qs *QueryServiceImpl) GitopiaRepositoryPackfile(ctx context.Context, req *storagetypes.QueryRepositoryPackfileRequest) (*storagetypes.QueryRepositoryPackfileResponse, error) {
	return qs.Query.Storage.RepositoryPackfile(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepositoryBranches(ctx context.Context, req *gitopiatypes.QueryAllRepositoryBranchRequest) (*gitopiatypes.QueryAllRepositoryBranchResponse, error) {
	return qs.Query.Gitopia.RepositoryBranchAll(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepositoryTags(ctx context.Context, req *gitopiatypes.QueryAllRepositoryTagRequest) (*gitopiatypes.QueryAllRepositoryTagResponse, error) {
	return qs.Query.Gitopia.RepositoryTagAll(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaRepository(ctx context.Context, req *gitopiatypes.QueryGetRepositoryRequest) (*gitopiatypes.QueryGetRepositoryResponse, error) {
	return qs.Query.Gitopia.Repository(ctx, req)
}

func (qs *QueryServiceImpl) GitopiaUserQuota(ctx context.Context, req *gitopiatypes.QueryUserQuotaRequest) (*gitopiatypes.QueryUserQuotaResponse, error) {
	return qs.Query.Gitopia.UserQuota(ctx, req)
}

func (qs *QueryServiceImpl) StorageParams(ctx context.Context, req *storagetypes.QueryParamsRequest) (*storagetypes.QueryParamsResponse, error) {
	return qs.Query.Storage.Params(ctx, req)
}

func (qs *QueryServiceImpl) CosmosBankBalance(ctx context.Context, req *banktypes.QueryBalanceRequest) (*banktypes.QueryBalanceResponse, error) {
	return qs.Query.Bank.Balance(ctx, req)
}

func (qs *QueryServiceImpl) StorageCidReferenceCount(ctx context.Context, req *storagetypes.QueryCidReferenceCountRequest) (*storagetypes.QueryCidReferenceCountResponse, error) {
	return qs.Query.Storage.CidReferenceCount(ctx, req)
}
