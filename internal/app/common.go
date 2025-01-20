package app

import (
	// "context"

	"github.com/gitopia/git-server/internal/db"
	"github.com/gitopia/git-server/utils"
	// gitopiatypes "github.com/gitopia/gitopia/v5/x/gitopia/types"
)

func (s *Server) CacheRepository(repoId uint64) error {
	// TODO: get latest packfile info from the chain
	name := "pack-xyz.pack"
	cid := "QmPK1xyz"

	if !utils.IsCached(db.CacheDb, repoId, cid, name) {
		CacheMutex.Lock()
		defer CacheMutex.Unlock()
		if err := utils.DownloadRepo(db.CacheDb, repoId, "repo-path", &s.Config); err != nil {
			return err
		}
	} else { // Repository is already available in file system cache
		CacheMutex.Lock()
		// Increase cache expiry time for this repository
		if err := utils.UpdateCacheEntry(db.CacheDb, repoId, cid, name); err != nil {
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
