package utils

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v5/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v5/x/storage/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

func IsRepoCached(id uint64, cacheDir string) (bool, error) {
	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return false, errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := queryClient.Storage.RepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: id,
	})
	if err != nil && !strings.Contains(err.Error(), "packfile not found") {
		return false, errors.Wrap(err, "failed to get cid from chain")
	}

	if res != nil {
		// empty repository
		if res.Packfile.Cid == "" {
			return true, nil
		}

		// Check if packfile exists in objects/pack directory
		repoPath := filepath.Join(cacheDir, fmt.Sprintf("%d.git", id))
		packfilePath := filepath.Join(repoPath, "objects", "pack", res.Packfile.Name)
		if _, err := os.Stat(packfilePath); err == nil {
			return true, nil
		}
	}

	return false, nil
}

func DownloadRepo(id uint64, cacheDir string) error {
	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := queryClient.Gitopia.Repository(context.Background(), &gitopiatypes.QueryGetRepositoryRequest{
		Id: id,
	})
	if err != nil {
		return err
	}

	repoDir := filepath.Join(cacheDir, fmt.Sprintf("%d.git", res.Repository.Id))

	// download parent repos first
	if res.Repository.Parent != 0 {
		err := DownloadRepo(res.Repository.Parent, cacheDir)
		if err != nil {
			return errors.Wrap(err, "error downloading parent repo")
		}

		// Create alternates file to link with parent repo
		alternatesDir := filepath.Join(repoDir, "objects", "info")
		if err := os.MkdirAll(alternatesDir, 0755); err != nil {
			return fmt.Errorf("failed to create alternates directory: %v", err)
		}

		// Write parent repo objects path to alternates file
		alternatesPath := filepath.Join(alternatesDir, "alternates")
		parentObjectsPath := filepath.Join(cacheDir, fmt.Sprintf("%d", res.Repository.Parent), "objects")
		if err := os.WriteFile(alternatesPath, []byte(parentObjectsPath+"\n"), 0644); err != nil {
			return fmt.Errorf("failed to write alternates file: %v", err)
		}
	}

	packfileRes, err := queryClient.Storage.RepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: id,
	})
	if err != nil && !strings.Contains(err.Error(), "packfile not found") {
		return fmt.Errorf("failed to get cid from chain: %v", err)
	}

	if packfileRes != nil {
		if err := downloadPackfile(packfileRes.Packfile.Cid, packfileRes.Packfile.Name, repoDir); err != nil {
			return errors.Wrap(err, "error downloading packfile")
		}
	}

	// create refs on the server
	createBranchesAndTags(queryClient, res.Repository.Owner.Id, res.Repository.Name, repoDir)

	return nil
}

func downloadPackfile(cid string, packfileName string, repoDir string) error {
	ipfsUrl := fmt.Sprintf("http://%s:%s/api/v0/cat?arg=/ipfs/%s&progress=false", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT"), cid)
	resp, err := http.Post(ipfsUrl, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to fetch packfile from IPFS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch packfile from IPFS: %v", resp.Status)
	}

	// Create objects/pack directory if it doesn't exist
	packDir := filepath.Join(repoDir, "objects", "pack")
	if err := os.MkdirAll(packDir, 0755); err != nil {
		return fmt.Errorf("failed to create pack directory: %v", err)
	}

	// Create packfile in objects/pack directory
	packfilePath := filepath.Join(packDir, packfileName)
	packfile, err := os.Create(packfilePath)
	if err != nil {
		return fmt.Errorf("failed to create packfile: %v", err)
	}
	defer packfile.Close()

	// Copy packfile contents
	if _, err := io.Copy(packfile, resp.Body); err != nil {
		return fmt.Errorf("failed to write packfile: %v", err)
	}

	// Build pack index file
	cmd, outPipe := GitCommand("git", "index-pack", packfilePath)
	cmd.Dir = repoDir
	if err := cmd.Start(); err != nil {
		return err
	}
	defer CleanUpProcessGroup(cmd)

	_, err = io.Copy(io.Discard, outPipe)
	if err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func createBranchesAndTags(queryClient gitopia.Query, repoOwner string, repoName string, repoDir string) error {
	branchAllRes, err := queryClient.Gitopia.RepositoryBranchAll(context.Background(), &gitopiatypes.QueryAllRepositoryBranchRequest{
		Id:             repoOwner,
		RepositoryName: repoName,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}
	for _, branch := range branchAllRes.Branch {
		cmd, outPipe := GitCommand("git", "branch", branch.Name, branch.Sha)
		cmd.Dir = repoDir
		if err := cmd.Start(); err != nil {
			return err
		}
		defer CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			return err
		}

		if err := cmd.Wait(); err != nil {
			return err
		}
	}

	tagAllRes, err := queryClient.Gitopia.RepositoryTagAll(context.Background(), &gitopiatypes.QueryAllRepositoryTagRequest{
		Id:             repoOwner,
		RepositoryName: repoName,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}
	for _, tag := range tagAllRes.Tag {
		cmd, outPipe := GitCommand("git", "tag", tag.Name, tag.Sha)
		cmd.Dir = repoDir
		if err := cmd.Start(); err != nil {
			return err
		}
		defer CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			return err
		}

		if err := cmd.Wait(); err != nil {
			return err
		}
	}
	return nil
}
