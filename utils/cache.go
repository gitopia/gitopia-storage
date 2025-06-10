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

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var (
	repoMutexes  sync.Map // map[uint64]*sync.Mutex
	assetMutexes sync.Map // map[string]*sync.Mutex
)

// getRepoMutex returns a mutex for the given repository ID
func getRepoMutex(repoID uint64) *sync.Mutex {
	mutex, _ := repoMutexes.LoadOrStore(repoID, &sync.Mutex{})
	return mutex.(*sync.Mutex)
}

// getAssetMutex returns a mutex for the given asset SHA
func getAssetMutex(sha string) *sync.Mutex {
	mutex, _ := assetMutexes.LoadOrStore(sha, &sync.Mutex{})
	return mutex.(*sync.Mutex)
}

// LockAsset acquires the asset-specific lock
func LockAsset(sha string) {
	getAssetMutex(sha).Lock()
}

// UnlockAsset releases the asset-specific lock
func UnlockAsset(sha string) {
	getAssetMutex(sha).Unlock()
}

// LockRepository acquires the repository-specific lock
func LockRepository(repoID uint64) {
	getRepoMutex(repoID).Lock()
}

// UnlockRepository releases the repository-specific lock
func UnlockRepository(repoID uint64) {
	getRepoMutex(repoID).Unlock()
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
	LockRepository(id)
	defer UnlockRepository(id)

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

	branchAllRes, err := queryClient.Gitopia.RepositoryBranchAll(context.Background(), &gitopiatypes.QueryAllRepositoryBranchRequest{
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}

	repoDir := filepath.Join(cacheDir, fmt.Sprintf("%d.git", id))
	for _, branch := range branchAllRes.Branch {
		cmd, outPipe := GitCommand("git", "branch", "-f", branch.Name, branch.Sha)
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
		Id:             res.Repository.Owner.Id,
		RepositoryName: res.Repository.Name,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}
	for _, tag := range tagAllRes.Tag {
		cmd, outPipe := GitCommand("git", "tag", "-f", tag.Name, tag.Sha)
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
		return errors.Wrap(err, "error getting release asset")
	}

	LockAsset(res.ReleaseAsset.Sha256)
	defer UnlockAsset(res.ReleaseAsset.Sha256)

	isCached, err := IsReleaseAssetCached(res.ReleaseAsset.Sha256, cacheDir)
	if err != nil {
		return errors.Wrap(err, "error checking if release asset is cached")
	}
	if !isCached {
		err = DownloadReleaseAsset(res.ReleaseAsset.Cid, res.ReleaseAsset.Sha256, cacheDir)
		if err != nil {
			return errors.Wrap(err, "error downloading release asset")
		}
	}
	return nil
}
