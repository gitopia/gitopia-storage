package app

import (
	"context"

	"github.com/gitopia/git-server/internal/db"
	"github.com/gitopia/git-server/utils"
	gitopiatypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
)

func (s *Server) CacheRepository(repoId uint64) error {
	res, err := s.QueryService.GitopiaRepositoryStorage(context.Background(), &gitopiatypes.QueryGetRepositoryStorageRequest{
		RepositoryId: repoId,
	})
	if err != nil {
		return err
	}

	if !utils.IsCached(db.CacheDb, repoId, res.Storage.Latest.Id, res.Storage.Latest.Name) {
		CacheMutex.Lock()
		defer CacheMutex.Unlock()
		if err := utils.DownloadRepo(db.CacheDb, repoId, r.RepoPath, &s.Config); err != nil {
			return err
		}
	} else { // Repository is already available in file system cache
		CacheMutex.Lock()
		// Increase cache expiry time for this repository
		if err := utils.UpdateCacheEntry(db.CacheDb, repoId, res.Storage.Latest.Id, res.Storage.Latest.Name); err != nil {
			return err
		}
		CacheMutex.Unlock()

		// Acquire mutex for reading
		// This is to make sure that no cache updates happen
		CacheMutex.RLock()
		defer CacheMutex.RUnlock()
	}
	return nil
}
