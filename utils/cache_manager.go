package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// CacheManager handles cache clearing operations for repositories and release assets
type CacheManager struct {
	repoCacheDir  string
	assetCacheDir string
	repoMaxAge    time.Duration
	assetMaxAge   time.Duration
	repoMaxSize   int64
	assetMaxSize  int64
	clearInterval time.Duration
	stopChan      chan struct{}
	mu            sync.Mutex
	isRunning     bool
}

// NewCacheManager creates a new cache manager with the specified configuration
func NewCacheManager() *CacheManager {
	return &CacheManager{
		repoCacheDir:  viper.GetString("GIT_REPOS_DIR"),
		assetCacheDir: viper.GetString("ATTACHMENT_DIR"),
		repoMaxAge:    viper.GetDuration("CACHE_REPO_MAX_AGE"),
		assetMaxAge:   viper.GetDuration("CACHE_ASSET_MAX_AGE"),
		repoMaxSize:   viper.GetInt64("CACHE_REPO_MAX_SIZE"),
		assetMaxSize:  viper.GetInt64("CACHE_ASSET_MAX_SIZE"),
		clearInterval: viper.GetDuration("CACHE_CLEAR_INTERVAL"),
		stopChan:      make(chan struct{}),
	}
}

// Start begins the cache clearing routine
func (cm *CacheManager) Start() error {
	cm.mu.Lock()
	if cm.isRunning {
		cm.mu.Unlock()
		return errors.New("cache manager is already running")
	}
	cm.isRunning = true
	cm.mu.Unlock()

	go cm.clearCacheRoutine()
	return nil
}

// Stop halts the cache clearing routine
func (cm *CacheManager) Stop() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if !cm.isRunning {
		return
	}

	close(cm.stopChan)
	cm.isRunning = false
}

// clearCacheRoutine periodically clears the cache based on configured rules
func (cm *CacheManager) clearCacheRoutine() {
	ticker := time.NewTicker(cm.clearInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := cm.clearCache(); err != nil {
				LogError("cache-clear", fmt.Errorf("failed to clear cache: %v", err))
			}
		case <-cm.stopChan:
			return
		}
	}
}

// clearCache performs the actual cache clearing operation
func (cm *CacheManager) clearCache() error {
	// Clear repository cache
	if err := cm.clearRepositoryCache(); err != nil {
		return errors.Wrap(err, "failed to clear repository cache")
	}

	// Clear asset cache
	if err := cm.clearAssetCache(); err != nil {
		return errors.Wrap(err, "failed to clear asset cache")
	}

	return nil
}

// clearRepositoryCache clears old repository caches based on age and size
func (cm *CacheManager) clearRepositoryCache() error {
	entries, err := os.ReadDir(cm.repoCacheDir)
	if err != nil {
		return errors.Wrap(err, "failed to read repository cache directory")
	}

	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		repoPath := filepath.Join(cm.repoCacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			LogError("cache-clear", fmt.Errorf("failed to get info for %s: %v", repoPath, err))
			continue
		}

		// Check age
		if time.Since(info.ModTime()) > cm.repoMaxAge {
			if err := os.RemoveAll(repoPath); err != nil {
				LogError("cache-clear", fmt.Errorf("failed to remove old repository %s: %v", repoPath, err))
			} else {
				logrus.WithFields(logrus.Fields{
					"path": repoPath,
					"age":  time.Since(info.ModTime()),
				}).Info("cleared old repository cache")
			}
			continue
		}

		// Calculate size
		size, err := cm.calculateDirSize(repoPath)
		if err != nil {
			LogError("cache-clear", fmt.Errorf("failed to calculate size for %s: %v", repoPath, err))
			continue
		}
		totalSize += size
	}

	// If total size exceeds max size, remove oldest entries
	if totalSize > cm.repoMaxSize {
		entries, err := os.ReadDir(cm.repoCacheDir)
		if err != nil {
			return errors.Wrap(err, "failed to read repository cache directory")
		}

		// Sort entries by modification time
		type entryInfo struct {
			path    string
			modTime time.Time
			size    int64
		}
		var sortedEntries []entryInfo

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			repoPath := filepath.Join(cm.repoCacheDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}

			size, err := cm.calculateDirSize(repoPath)
			if err != nil {
				continue
			}

			sortedEntries = append(sortedEntries, entryInfo{
				path:    repoPath,
				modTime: info.ModTime(),
				size:    size,
			})
		}

		// Remove oldest entries until we're under the size limit
		for _, entry := range sortedEntries {
			if totalSize <= cm.repoMaxSize {
				break
			}

			if err := os.RemoveAll(entry.path); err != nil {
				LogError("cache-clear", fmt.Errorf("failed to remove repository %s: %v", entry.path, err))
				continue
			}

			totalSize -= entry.size
			logrus.WithFields(logrus.Fields{
				"path": entry.path,
				"size": entry.size,
			}).Info("cleared repository cache due to size limit")
		}
	}

	return nil
}

// clearAssetCache clears old asset caches based on age and size
func (cm *CacheManager) clearAssetCache() error {
	entries, err := os.ReadDir(cm.assetCacheDir)
	if err != nil {
		return errors.Wrap(err, "failed to read asset cache directory")
	}

	var totalSize int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		assetPath := filepath.Join(cm.assetCacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			LogError("cache-clear", fmt.Errorf("failed to get info for %s: %v", assetPath, err))
			continue
		}

		// Check age
		if time.Since(info.ModTime()) > cm.assetMaxAge {
			if err := os.Remove(assetPath); err != nil {
				LogError("cache-clear", fmt.Errorf("failed to remove old asset %s: %v", assetPath, err))
			} else {
				logrus.WithFields(logrus.Fields{
					"path": assetPath,
					"age":  time.Since(info.ModTime()),
				}).Info("cleared old asset cache")
			}
			continue
		}

		totalSize += info.Size()
	}

	// If total size exceeds max size, remove oldest entries
	if totalSize > cm.assetMaxSize {
		entries, err := os.ReadDir(cm.assetCacheDir)
		if err != nil {
			return errors.Wrap(err, "failed to read asset cache directory")
		}

		// Sort entries by modification time
		type entryInfo struct {
			path    string
			modTime time.Time
			size    int64
		}
		var sortedEntries []entryInfo

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			assetPath := filepath.Join(cm.assetCacheDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}

			sortedEntries = append(sortedEntries, entryInfo{
				path:    assetPath,
				modTime: info.ModTime(),
				size:    info.Size(),
			})
		}

		// Remove oldest entries until we're under the size limit
		for _, entry := range sortedEntries {
			if totalSize <= cm.assetMaxSize {
				break
			}

			if err := os.Remove(entry.path); err != nil {
				LogError("cache-clear", fmt.Errorf("failed to remove asset %s: %v", entry.path, err))
				continue
			}

			totalSize -= entry.size
			logrus.WithFields(logrus.Fields{
				"path": entry.path,
				"size": entry.size,
			}).Info("cleared asset cache due to size limit")
		}
	}

	return nil
}

// calculateDirSize calculates the total size of a directory
func (cm *CacheManager) calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
