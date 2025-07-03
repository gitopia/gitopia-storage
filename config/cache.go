package config

import (
	"time"

	"github.com/spf13/viper"
)

// CacheConfig holds all cache-related configuration
type CacheConfig struct {
	// Repository cache settings
	RepoMaxAge  time.Duration
	RepoMaxSize int64
	// Asset cache settings
	AssetMaxAge  time.Duration
	AssetMaxSize int64
	// General cache settings
	ClearInterval time.Duration
}

// DefaultCacheConfig returns the default cache configuration
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		// Default to 24 hours for repository cache age
		RepoMaxAge: 24 * time.Hour,
		// Default to 10GB for repository cache size
		RepoMaxSize: 10 * 1024 * 1024 * 1024,
		// Default to 7 days for asset cache age
		AssetMaxAge: 7 * 24 * time.Hour,
		// Default to 5GB for asset cache size
		AssetMaxSize: 5 * 1024 * 1024 * 1024,
		// Default to 1 hour for cache clearing interval
		ClearInterval: time.Hour,
	}
}

// LoadCacheConfig loads cache configuration from viper
func LoadCacheConfig() *CacheConfig {
	config := DefaultCacheConfig()

	// Load repository cache settings
	if viper.IsSet("CACHE_REPO_MAX_AGE") {
		config.RepoMaxAge = viper.GetDuration("CACHE_REPO_MAX_AGE")
	}
	if viper.IsSet("CACHE_REPO_MAX_SIZE") {
		config.RepoMaxSize = viper.GetInt64("CACHE_REPO_MAX_SIZE")
	}

	// Load asset cache settings
	if viper.IsSet("CACHE_ASSET_MAX_AGE") {
		config.AssetMaxAge = viper.GetDuration("CACHE_ASSET_MAX_AGE")
	}
	if viper.IsSet("CACHE_ASSET_MAX_SIZE") {
		config.AssetMaxSize = viper.GetInt64("CACHE_ASSET_MAX_SIZE")
	}

	// Load general cache settings
	if viper.IsSet("CACHE_CLEAR_INTERVAL") {
		config.ClearInterval = viper.GetDuration("CACHE_CLEAR_INTERVAL")
	}

	return config
}
