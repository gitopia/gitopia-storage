package utils

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// RefCountedRWMutex is a RWMutex wrapper that tracks both lock state and reference count
// Supports concurrent read operations while maintaining exclusive write access
type RefCountedRWMutex struct {
	mu         sync.RWMutex
	readCount  int32  // Number of active read locks
	writeCount int32  // Number of active write locks (0 or 1)
	lastUsed   time.Time
	cleanupCh  chan struct{}
}

var (
	repoMutexes  sync.Map // map[uint64]*RefCountedRWMutex
	assetMutexes sync.Map // map[string]*RefCountedRWMutex
	lfsMutexes   sync.Map // map[string]*RefCountedRWMutex
)

// getRepoMutex returns a reference-counted RWMutex for the given repository ID
func getRepoMutex(repoID uint64) *RefCountedRWMutex {
	mutex, _ := repoMutexes.LoadOrStore(repoID, &RefCountedRWMutex{
		cleanupCh: make(chan struct{}),
	})
	return mutex.(*RefCountedRWMutex)
}

// getAssetMutex returns a reference-counted RWMutex for the given asset SHA
func getAssetMutex(sha string) *RefCountedRWMutex {
	mutex, _ := assetMutexes.LoadOrStore(sha, &RefCountedRWMutex{
		cleanupCh: make(chan struct{}),
	})
	return mutex.(*RefCountedRWMutex)
}

// getLFSObjectMutex returns a reference-counted RWMutex for the given lfs object OID
func getLFSObjectMutex(oid string) *RefCountedRWMutex {
	mutex, _ := lfsMutexes.LoadOrStore(oid, &RefCountedRWMutex{
		cleanupCh: make(chan struct{}),
	})
	return mutex.(*RefCountedRWMutex)
}

// LockAsset acquires the asset-specific write lock and increments reference count
func LockAsset(sha string) {
	mutex := getAssetMutex(sha)
	atomic.AddInt32(&mutex.writeCount, 1)
	mutex.mu.Lock()
	mutex.lastUsed = time.Now()
}

// UnlockAsset releases the asset-specific write lock and decrements reference count
func UnlockAsset(sha string) {
	mutex := getAssetMutex(sha)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.writeCount) <= 0 {
		return
	}
	mutex.mu.Unlock()
	if atomic.AddInt32(&mutex.writeCount, -1) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				assetMutexes.Delete(sha)
			}
		}()
	}
}

// RLockAsset acquires the asset-specific read lock and increments read reference count
// Multiple read locks can be held concurrently, but read locks block write locks
func RLockAsset(sha string) {
	mutex := getAssetMutex(sha)
	atomic.AddInt32(&mutex.readCount, 1)
	mutex.mu.RLock()
	mutex.lastUsed = time.Now()
}

// RUnlockAsset releases the asset-specific read lock and decrements read reference count
func RUnlockAsset(sha string) {
	mutex := getAssetMutex(sha)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.readCount) <= 0 {
		return
	}
	mutex.mu.RUnlock()
	if atomic.AddInt32(&mutex.readCount, -1) == 0 && atomic.LoadInt32(&mutex.writeCount) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				assetMutexes.Delete(sha)
			}
		}()
	}
}

// LockLFSObject acquires the lfs object-specific write lock and increments reference count
func LockLFSObject(oid string) {
	mutex := getLFSObjectMutex(oid)
	atomic.AddInt32(&mutex.writeCount, 1)
	mutex.mu.Lock()
	mutex.lastUsed = time.Now()
}

// UnlockLFSObject releases the lfs object-specific write lock and decrements reference count
func UnlockLFSObject(oid string) {
	mutex := getLFSObjectMutex(oid)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.writeCount) <= 0 {
		return
	}
	mutex.mu.Unlock()
	if atomic.AddInt32(&mutex.writeCount, -1) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				lfsMutexes.Delete(oid)
			}
		}()
	}
}

// RLockLFSObject acquires the lfs object-specific read lock and increments read reference count
// Multiple read locks can be held concurrently, but read locks block write locks
func RLockLFSObject(oid string) {
	mutex := getLFSObjectMutex(oid)
	atomic.AddInt32(&mutex.readCount, 1)
	mutex.mu.RLock()
	mutex.lastUsed = time.Now()
}

// RUnlockLFSObject releases the lfs object-specific read lock and decrements read reference count
func RUnlockLFSObject(oid string) {
	mutex := getLFSObjectMutex(oid)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.readCount) <= 0 {
		return
	}
	mutex.mu.RUnlock()
	if atomic.AddInt32(&mutex.readCount, -1) == 0 && atomic.LoadInt32(&mutex.writeCount) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				lfsMutexes.Delete(oid)
			}
		}()
	}
}

// LockRepository acquires the repository-specific write lock and increments reference count
func LockRepository(repoID uint64) {
	mutex := getRepoMutex(repoID)
	atomic.AddInt32(&mutex.writeCount, 1)
	mutex.mu.Lock()
	mutex.lastUsed = time.Now()
}

// UnlockRepository releases the repository-specific write lock and decrements reference count
func UnlockRepository(repoID uint64) {
	mutex := getRepoMutex(repoID)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.writeCount) <= 0 {
		return
	}
	mutex.mu.Unlock()
	if atomic.AddInt32(&mutex.writeCount, -1) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				repoMutexes.Delete(repoID)
			}
		}()
	}
}

// RLockRepository acquires the repository-specific read lock and increments read reference count
// Multiple read locks can be held concurrently, but read locks block write locks
func RLockRepository(repoID uint64) {
	mutex := getRepoMutex(repoID)
	atomic.AddInt32(&mutex.readCount, 1)
	mutex.mu.RLock()
	mutex.lastUsed = time.Now()
}

// RUnlockRepository releases the repository-specific read lock and decrements read reference count
func RUnlockRepository(repoID uint64) {
	mutex := getRepoMutex(repoID)
	// Check reference count before attempting to unlock to prevent double unlock panic
	if atomic.LoadInt32(&mutex.readCount) <= 0 {
		return
	}
	mutex.mu.RUnlock()
	if atomic.AddInt32(&mutex.readCount, -1) == 0 && atomic.LoadInt32(&mutex.writeCount) == 0 {
		// If no more references, schedule cleanup
		go func() {
			select {
			case <-mutex.cleanupCh:
				// Wait for cleanup signal
			case <-time.After(5 * time.Minute):
				// If no activity for 5 minutes, remove from map
				repoMutexes.Delete(repoID)
			}
		}()
	}
}

// IsRepositoryInUse checks if a repository is currently locked/in use
func IsRepositoryInUse(repoID uint64) bool {
	if mutex, exists := repoMutexes.Load(repoID); exists {
		rm := mutex.(*RefCountedRWMutex)
		return atomic.LoadInt32(&rm.readCount) > 0 || atomic.LoadInt32(&rm.writeCount) > 0
	}
	return false
}

// IsAssetInUse checks if an asset is currently locked/in use
func IsAssetInUse(sha string) bool {
	if mutex, exists := assetMutexes.Load(sha); exists {
		rm := mutex.(*RefCountedRWMutex)
		return atomic.LoadInt32(&rm.readCount) > 0 || atomic.LoadInt32(&rm.writeCount) > 0
	}
	return false
}

// IsLFSObjectInUse checks if an lfs object is currently locked/in use
func IsLFSObjectInUse(oid string) bool {
	if mutex, exists := lfsMutexes.Load(oid); exists {
		rm := mutex.(*RefCountedRWMutex)
		return atomic.LoadInt32(&rm.readCount) > 0 || atomic.LoadInt32(&rm.writeCount) > 0
	}
	return false
}

func IsRepositoryPackfileCached(id uint64, cacheDir string) (bool, error) {
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

// CacheRepository caches a repository by downloading its packfile and syncing its refs
func CacheRepository(id uint64, cacheDir string) error {
	isRepoCached, err := IsRepositoryPackfileCached(id, cacheDir)
	if err != nil {
		return errors.Wrap(err, "error checking if repo is cached")
	}

	if !isRepoCached {
		if err := DownloadRepositoryPackfile(id, cacheDir); err != nil {
			return errors.Wrap(err, "error downloading repository packfile")
		}
	}

	if err := SyncRepositoryRefs(id, cacheDir); err != nil {
		return errors.Wrap(err, "error syncing repository refs")
	}

	return nil
}

func DownloadRepositoryPackfile(id uint64, cacheDir string) error {
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

	// Initialize repository if it doesn't exist
	if _, err := os.Stat(filepath.Join(repoDir, "objects")); os.IsNotExist(err) {
		cmd := exec.Command("git", "init", "--bare", repoDir)
		if err := cmd.Run(); err != nil {
			return errors.Wrap(err, "failed to initialize repository")
		}
	}

	// download parent repos first
	if res.Repository.Fork {
		// Check if parent repo is cached
		isParentCached, err := IsRepositoryPackfileCached(res.Repository.Parent, cacheDir)
		if err != nil {
			return errors.Wrap(err, "error checking if parent repo is cached")
		}

		if !isParentCached {
			err := DownloadRepositoryPackfile(res.Repository.Parent, cacheDir)
			if err != nil {
				return errors.Wrap(err, "error downloading parent repo")
			}
		}

		// Check link to parent repo in alternates file
		alternatesPath := filepath.Join(repoDir, "objects", "info", "alternates")
		if _, err := os.Stat(alternatesPath); os.IsNotExist(err) {
			// Create alternates file to link with parent repo
			parentObjectsPath := filepath.Join(cacheDir, fmt.Sprintf("%d.git", res.Repository.Parent), "objects")
			if err := os.WriteFile(alternatesPath, []byte(parentObjectsPath+"\n"), 0644); err != nil {
				return fmt.Errorf("failed to write alternates file: %v", err)
			}
		}

	}

	packfileRes, err := queryClient.Storage.RepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{
		RepositoryId: id,
	})
	if err != nil && !strings.Contains(err.Error(), "packfile not found") {
		return fmt.Errorf("failed to get cid from chain: %v", err)
	}

	if packfileRes != nil {
		LogInfo("info", fmt.Sprintf("Downloading packfile with cid %s for repo %d", packfileRes.Packfile.Cid, id))

		if err := downloadPackfile(packfileRes.Packfile.Cid, packfileRes.Packfile.Name, repoDir); err != nil {
			return errors.Wrap(err, "error downloading packfile")
		}
	}

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
	cmd, outPipe, err := GitCommand("git", "index-pack", packfilePath)
	if err != nil {
		return err
	}
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

func SyncRepositoryRefs(id uint64, cacheDir string) error {
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

	repoDir := filepath.Join(cacheDir, fmt.Sprintf("%d.git", id))
	var failedRefs []string

	// Fetch branches and tags concurrently
	type refData struct {
		branches []gitopiatypes.Branch
		tags     []gitopiatypes.Tag
		err      error
	}

	refChan := make(chan refData, 1)
	go func() {
		var data refData
		
		// Fetch branches
		branchAllRes, err := queryClient.Gitopia.RepositoryBranchAll(context.Background(), &gitopiatypes.QueryAllRepositoryBranchRequest{
			Id:             res.Repository.Owner.Id,
			RepositoryName: res.Repository.Name,
			Pagination: &query.PageRequest{
				Limit: math.MaxUint64,
			},
		})
		if err != nil {
			data.err = err
			refChan <- data
			return
		}
		data.branches = branchAllRes.Branch

		// Fetch tags
		tagAllRes, err := queryClient.Gitopia.RepositoryTagAll(context.Background(), &gitopiatypes.QueryAllRepositoryTagRequest{
			Id:             res.Repository.Owner.Id,
			RepositoryName: res.Repository.Name,
			Pagination: &query.PageRequest{
				Limit: math.MaxUint64,
			},
		})
		if err != nil {
			data.err = err
			refChan <- data
			return
		}
		data.tags = tagAllRes.Tag
		
		refChan <- data
	}()

	// Wait for ref data
	data := <-refChan
	if data.err != nil {
		return data.err
	}

	// Use git update-ref for efficient batch updates
	if len(data.branches) > 0 || len(data.tags) > 0 {
		failedRefs = append(failedRefs, syncRefsWithUpdateRef(repoDir, data.branches, data.tags, id)...)
	}

	// Log summary of failed refs but don't fail the entire operation
	if len(failedRefs) > 0 {
		LogError("warning", fmt.Errorf("Repository %d loaded successfully but %d refs failed to sync: %v", id, len(failedRefs), failedRefs))
	}

	return nil
}

// syncRefsWithUpdateRef uses git update-ref for efficient batch ref updates
func syncRefsWithUpdateRef(repoDir string, branches []gitopiatypes.Branch, tags []gitopiatypes.Tag, repoId uint64) []string {
	var failedRefs []string

	// Create update-ref commands for all refs
	var updateCommands []string
	
	// Add branch updates
	for _, branch := range branches {
		updateCommands = append(updateCommands, fmt.Sprintf("update refs/heads/%s %s", branch.Name, branch.Sha))
	}
	
	// Add tag updates  
	for _, tag := range tags {
		updateCommands = append(updateCommands, fmt.Sprintf("update refs/tags/%s %s", tag.Name, tag.Sha))
	}

	if len(updateCommands) == 0 {
		return failedRefs
	}

	// Use git update-ref --stdin for atomic batch updates
	cmd, outPipe, err := GitCommand("git", "update-ref", "--stdin")
	if err != nil {
		LogError("error", fmt.Errorf("Failed to create git update-ref command for repo %d: %v", repoId, err))
		return syncRefsIndividually(repoDir, branches, tags, repoId)
	}
	cmd.Dir = repoDir
	
	stdin, err := cmd.StdinPipe()
	if err != nil {
		LogError("error", fmt.Errorf("Failed to create stdin pipe for repo %d: %v", repoId, err))
		// Fallback to individual updates
		return syncRefsIndividually(repoDir, branches, tags, repoId)
	}

	if err := cmd.Start(); err != nil {
		LogError("error", fmt.Errorf("Failed to start git update-ref for repo %d: %v", repoId, err))
		stdin.Close()
		// Fallback to individual updates
		return syncRefsIndividually(repoDir, branches, tags, repoId)
	}

	// Write all update commands
	go func() {
		defer stdin.Close()
		for _, updateCmd := range updateCommands {
			if _, err := fmt.Fprintln(stdin, updateCmd); err != nil {
				LogError("error", fmt.Errorf("Failed to write update command for repo %d: %v", repoId, err))
				return
			}
		}
	}()

	// Read output
	_, err = io.Copy(io.Discard, outPipe)
	if err != nil {
		LogError("error", fmt.Errorf("Failed to read git update-ref output for repo %d: %v", repoId, err))
		CleanUpProcessGroup(cmd)
		// Fallback to individual updates
		return syncRefsIndividually(repoDir, branches, tags, repoId)
	}

	if err := cmd.Wait(); err != nil {
		LogError("error", fmt.Errorf("Batch ref update failed for repo %d: %v", repoId, err))
		// Fallback to individual updates to identify specific failures
		return syncRefsIndividually(repoDir, branches, tags, repoId)
	}

	CleanUpProcessGroup(cmd)
	return failedRefs
}

// syncRefsIndividually falls back to individual ref updates when batch fails
func syncRefsIndividually(repoDir string, branches []gitopiatypes.Branch, tags []gitopiatypes.Tag, repoId uint64) []string {
	var failedRefs []string

	// Update branches individually
	for _, branch := range branches {
		cmd, outPipe, err := GitCommand("git", "update-ref", fmt.Sprintf("refs/heads/%s", branch.Name), branch.Sha)
		if err != nil {
			LogError("error", fmt.Errorf("Failed to create git update-ref command for repo %d, branch %s: %v", repoId, branch.Name, err))
			failedRefs = append(failedRefs, fmt.Sprintf("branch:%s", branch.Name))
			continue
		}
		cmd.Dir = repoDir
		
		if err := cmd.Start(); err != nil {
			LogError("error", fmt.Errorf("Failed to start git update-ref for repo %d, branch %s: %v", repoId, branch.Name, err))
			failedRefs = append(failedRefs, fmt.Sprintf("branch:%s", branch.Name))
			continue
		}

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			LogError("error", fmt.Errorf("Failed to read git update-ref output for repo %d, branch %s: %v", repoId, branch.Name, err))
			CleanUpProcessGroup(cmd)
			failedRefs = append(failedRefs, fmt.Sprintf("branch:%s", branch.Name))
			continue
		}

		if err := cmd.Wait(); err != nil {
			LogError("error", fmt.Errorf("Failed to update branch %s (SHA: %s) for repo %d: %v", branch.Name, branch.Sha, repoId, err))
			failedRefs = append(failedRefs, fmt.Sprintf("branch:%s", branch.Name))
			continue
		}
		CleanUpProcessGroup(cmd)
	}

	// Update tags individually
	for _, tag := range tags {
		cmd, outPipe, err := GitCommand("git", "update-ref", fmt.Sprintf("refs/tags/%s", tag.Name), tag.Sha)
		if err != nil {
			LogError("error", fmt.Errorf("Failed to create git update-ref command for repo %d, tag %s: %v", repoId, tag.Name, err))
			failedRefs = append(failedRefs, fmt.Sprintf("tag:%s", tag.Name))
			continue
		}
		cmd.Dir = repoDir
		
		if err := cmd.Start(); err != nil {
			LogError("error", fmt.Errorf("Failed to start git update-ref for repo %d, tag %s: %v", repoId, tag.Name, err))
			failedRefs = append(failedRefs, fmt.Sprintf("tag:%s", tag.Name))
			continue
		}

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			LogError("error", fmt.Errorf("Failed to read git update-ref output for repo %d, tag %s: %v", repoId, tag.Name, err))
			CleanUpProcessGroup(cmd)
			failedRefs = append(failedRefs, fmt.Sprintf("tag:%s", tag.Name))
			continue
		}

		if err := cmd.Wait(); err != nil {
			LogError("error", fmt.Errorf("Failed to update tag %s (SHA: %s) for repo %d: %v", tag.Name, tag.Sha, repoId, err))
			failedRefs = append(failedRefs, fmt.Sprintf("tag:%s", tag.Name))
			continue
		}
		CleanUpProcessGroup(cmd)
	}

	return failedRefs
}

func IsReleaseAssetCached(sha256, cacheDir string) (bool, error) {
	attachmentDir := viper.GetString("ATTACHMENT_DIR")
	filePath := fmt.Sprintf("%s/%s", attachmentDir, sha256)

	if _, err := os.Stat(filePath); err == nil {
		return true, nil
	}
	return false, nil
}

func DownloadReleaseAsset(cid, sha256, cacheDir string) error {
	ipfsUrl := fmt.Sprintf("http://%s:%s/api/v0/cat?arg=/ipfs/%s&progress=false", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT"), cid)
	resp, err := http.Post(ipfsUrl, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to fetch release asset from IPFS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch release asset from IPFS: %v", resp.Status)
	}

	attachmentDir := viper.GetString("ATTACHMENT_DIR")
	filePath := fmt.Sprintf("%s/%s", attachmentDir, sha256)
	attachmentFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create attachment file: %v", err)
	}
	defer attachmentFile.Close()

	if _, err := io.Copy(attachmentFile, resp.Body); err != nil {
		return fmt.Errorf("failed to write attachment file: %v", err)
	}

	return nil
}

func CacheReleaseAsset(repositoryId uint64, tag, name string, cacheDir string) error {
	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := queryClient.Storage.RepositoryReleaseAsset(context.Background(), &storagetypes.QueryRepositoryReleaseAssetRequest{
		RepositoryId: repositoryId,
		Tag:          tag,
		Name:         name,
	})
	if err != nil {
		return errors.Wrap(err, "failed to get release asset from chain")
	}

	isAssetCached, err := IsReleaseAssetCached(res.ReleaseAsset.Sha256, cacheDir)
	if err != nil {
		return errors.Wrap(err, "error checking if asset is cached")
	}

	if !isAssetCached {
		if err := DownloadReleaseAsset(res.ReleaseAsset.Cid, res.ReleaseAsset.Sha256, cacheDir); err != nil {
			return errors.Wrap(err, "error downloading release asset")
		}
	}

	return nil
}

func IsLFSObjectCached(oid string) (bool, error) {
	RLockLFSObject(oid)
	defer RUnlockLFSObject(oid)

	lfsDir := viper.GetString("LFS_OBJECTS_DIR")
	filePath := filepath.Join(lfsDir, oid)

	if _, err := os.Stat(filePath); err == nil {
		return true, nil
	}
	return false, nil
}

func DownloadLFSObject(cid, oid string) error {
	LockLFSObject(oid)
	defer UnlockLFSObject(oid)

	// Check if object already exists after acquiring lock to prevent duplicate downloads
	lfsDir := viper.GetString("LFS_OBJECTS_DIR")
	filePath := filepath.Join(lfsDir, oid)
	if _, err := os.Stat(filePath); err == nil {
		return nil // Object already exists
	}

	ipfsUrl := fmt.Sprintf("http://%s:%s/api/v0/cat?arg=/ipfs/%s&progress=false", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT"), cid)
	resp, err := http.Post(ipfsUrl, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to fetch lfs object from IPFS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch lfs object from IPFS: %v", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create lfs object directory: %v", err)
	}

	lfsFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create lfs object file: %v", err)
	}
	defer lfsFile.Close()

	if _, err := io.Copy(lfsFile, resp.Body); err != nil {
		return fmt.Errorf("failed to write lfs object file: %v", err)
	}

	return nil
}

func CacheLFSObjects(repositoryId uint64) error {
	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := queryClient.Storage.LFSObjectsByRepositoryId(context.Background(), &storagetypes.QueryLFSObjectsByRepositoryIdRequest{
		RepositoryId: repositoryId,
	})
	if err != nil {
		return errors.Wrap(err, "failed to get lfs objects from chain")
	}

	for _, lfsObject := range res.LfsObjects {
		isLFSObjectCached, err := IsLFSObjectCached(lfsObject.Oid)
		if err != nil {
			return errors.Wrap(err, "error checking if lfs object is cached")
		}

		if !isLFSObjectCached {
			if err := DownloadLFSObject(lfsObject.Cid, lfsObject.Oid); err != nil {
				return errors.Wrap(err, "error downloading lfs object")
			}
		}
	}

	return nil
}
