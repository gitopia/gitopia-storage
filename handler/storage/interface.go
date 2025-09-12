package storage

import (
	"context"
)

// StorageProvider defines the interface for external storage providers
type StorageProvider interface {
	// PinFile uploads a file to the storage provider
	PinFile(ctx context.Context, filePath, name string) (*PinResponse, error)
	
	// UnpinFile removes a file from the storage provider
	UnpinFile(ctx context.Context, name string) error
	
	// Name returns the name of the storage provider
	Name() string
}

// PinResponse represents a successful pin operation response
type PinResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CID  string `json:"cid,omitempty"`
	Size int    `json:"size,omitempty"`
}

// Config holds configuration for storage providers
type Config struct {
	// Pinata configuration
	PinataJWT string `mapstructure:"PINATA_JWT"`
	
	// Filebase configuration
	FilebaseAccessKey string `mapstructure:"FILEBASE_ACCESS_KEY"`
	FilebaseSecretKey string `mapstructure:"FILEBASE_SECRET_KEY"`
	FilebaseBucket    string `mapstructure:"FILEBASE_BUCKET"`
	FilebaseRegion    string `mapstructure:"FILEBASE_REGION"`
	FilebaseEndpoint  string `mapstructure:"FILEBASE_ENDPOINT"`
	
	// Provider selection
	EnabledProviders []string `mapstructure:"STORAGE_PROVIDERS"`
}
