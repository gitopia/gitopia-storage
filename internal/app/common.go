package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitopia/git-server/utils"
	storagetypes "github.com/gitopia/gitopia/v5/x/storage/types"
)

func (s *Server) CacheRepository(repoId uint64) error {
	// Get cid from the chain
	packfileResp, err := s.QueryService.GitopiaRepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: repoId,
	})
	if err != nil && !strings.Contains(err.Error(), "packfile not found") {
		return fmt.Errorf("failed to get cid from chain: %v", err)
	}

	// Only attempt to fetch and cache packfile if it exists on chain
	if packfileResp != nil && packfileResp.Packfile.Cid != "" {
		// Check if packfile exists in objects/pack directory
		cached := false
		repoPath := filepath.Join(s.Config.Dir, fmt.Sprintf("%d.git", repoId))
		packfilePath := filepath.Join(repoPath, "objects", "pack", packfileResp.Packfile.Name)
		if _, err := os.Stat(packfilePath); err == nil {
			cached = true
		}

		if !cached {
			// Fetch packfile from IPFS and place in objects/pack directory
			err = utils.DownloadRepo(repoId, s.Config.Dir)
			if err != nil {
				return fmt.Errorf("failed to cache repository: %v", err)
			}
		}
	}

	return nil
}
