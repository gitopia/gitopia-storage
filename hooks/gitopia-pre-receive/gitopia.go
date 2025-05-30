package main

import (
	"context"

	"cosmossdk.io/errors"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-storage/hooks/gitopia-pre-receive/config"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
)

func IsForcePushAllowedForBranch(repo uint64, branch string) (bool, error) {
	qc, err := gitopia.GetQueryClient(config.GRPCHost)
	if err != nil {
		return false, errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := qc.Gitopia.Repository(context.Background(), &types.QueryGetRepositoryRequest{
		Id: repo,
	})
	if err != nil {
		return false, errors.Wrap(err, "error querying repo")
	}

	branchRes, err := qc.Gitopia.RepositoryBranch(context.Background(), &types.QueryGetRepositoryBranchRequest{
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		BranchName:     branch,
	})
	if err != nil {
		return false, errors.Wrap(err, "error querying repo branch")
	}
	return branchRes.Branch.AllowForcePush, nil
}
