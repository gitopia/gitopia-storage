package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var NonFFErr = errors.New("non fast-forward pushes are not allowed")

func isForcePushAllowedForBranch(repo uint64, branch string) (bool, error) {
	qc, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return false, errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := qc.Repository(context.Background(), &types.QueryGetRepositoryRequest{
		Id: repo,
	})
	if err != nil {
		return false, errors.Wrap(err, "error querying repo")
	}

	branchRes, err := qc.RepositoryBranch(context.Background(), &types.QueryGetRepositoryBranchRequest{
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		BranchName:     branch,
	})
	if err != nil {
		return false, errors.Wrap(err, "error querying repo branch")
	}
	return branchRes.Branch.AllowForcePush, nil
}

func receive(reader io.Reader) error {
	input, err := Parse(reader)
	if err != nil {
		return errors.Wrap(err, "error parsing input")
	}
	// Check if push is non fast-forward (force)
	force, err := input.IsForcePush()
	if err != nil {
		return errors.Wrap(err, "error checking force push")
	}

	AllowForcePush, err := isForcePushAllowedForBranch(input.RepoId, input.RefName)
	if err != nil {
		return errors.Wrap(err, "error fetching force push config")
	}

	// Reject force push
	if !AllowForcePush && force {
		return NonFFErr
	}

	return nil
}

func main() {
	viper.AutomaticEnv()
	// Git hook data is provided via STDIN
	if err := receive(os.Stdin); err != nil {
		log.Println(err)
		os.Exit(1) // terminating with non-zero status will cancel push
	}
}
