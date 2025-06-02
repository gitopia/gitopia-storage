package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitopia/gitopia-storage/utils"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
)

func (s *Server) CacheRepository(repoId uint64) error {
	// Get cid from the chain
	packfileResp, err := s.QueryService.GitopiaRepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: repoId,
	})
	if err != nil && !strings.Contains(err.Error(), "packfile not found") {
		return fmt.Errorf("failed to get cid from chain: %v", err)
	}

	// Get repo from chain
	repoResp, err := s.QueryService.GitopiaRepository(context.Background(), &gitopiatypes.QueryGetRepositoryRequest{
		Id: repoId,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo from chain: %v", err)
	}

	// Attempt to fetch and cache packfile if it exists on chain or if it's a forked repo
	if (packfileResp != nil && packfileResp.Packfile.Cid != "") || repoResp.Repository.Fork == true {
		// Check if packfile exists in objects/pack directory
		cached := false
		repoPath := filepath.Join(s.Config.Dir, fmt.Sprintf("%d.git", repoId))
		if packfileResp != nil && packfileResp.Packfile.Cid != "" {
			packfilePath := filepath.Join(repoPath, "objects", "pack", packfileResp.Packfile.Name)
			if _, err := os.Stat(packfilePath); err == nil {
				cached = true
			}
		}

		if !cached {
			if packfileResp != nil && packfileResp.Packfile.Cid != "" {
				utils.LogInfo("info", fmt.Sprintf("Packfile with cid %s not found for repo %d, downloading from IPFS cluster", packfileResp.Packfile.Cid, repoId))
			} else {
				utils.LogInfo("info", fmt.Sprintf("Forked repo %d, downloading from IPFS cluster", repoId))
			}

			// Fetch packfile from IPFS and place in objects/pack directory
			err = utils.DownloadRepo(repoId, s.Config.Dir)
			if err != nil {
				return fmt.Errorf("failed to cache repository: %v", err)
			}
		}
	}

	return nil
}
