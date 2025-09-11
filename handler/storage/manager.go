package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Manager handles multiple storage providers
type Manager struct {
	providers []StorageProvider
	logger    *logrus.Logger
}

// NewManager creates a new storage manager with the given providers
func NewManager(providers []StorageProvider, logger *logrus.Logger) *Manager {
	return &Manager{
		providers: providers,
		logger:    logger,
	}
}

// PinFile uploads a file to all configured storage providers
func (m *Manager) PinFile(ctx context.Context, filePath, name string) error {
	if len(m.providers) == 0 {
		return nil // No providers configured, skip silently
	}

	var errors []string
	successCount := 0

	for _, provider := range m.providers {
		resp, err := provider.PinFile(ctx, filePath, name)
		if err != nil {
			errorMsg := fmt.Sprintf("%s: %v", provider.Name(), err)
			errors = append(errors, errorMsg)
			m.logger.WithError(err).WithField("provider", provider.Name()).Error("failed to pin file")
		} else {
			successCount++
			m.logger.WithFields(logrus.Fields{
				"provider": provider.Name(),
				"file":     name,
				"id":       resp.ID,
				"size":     resp.Size,
			}).Info("successfully pinned file")
		}
	}

	// Log summary
	if successCount > 0 {
		m.logger.WithFields(logrus.Fields{
			"file":         name,
			"success":      successCount,
			"total":        len(m.providers),
			"failed":       len(m.providers) - successCount,
		}).Info("file pinning completed")
	}

	// Return error only if all providers failed
	if successCount == 0 && len(errors) > 0 {
		return fmt.Errorf("all storage providers failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// UnpinFile removes a file from all configured storage providers
func (m *Manager) UnpinFile(ctx context.Context, name string) error {
	if len(m.providers) == 0 {
		return nil // No providers configured, skip silently
	}

	var errors []string
	successCount := 0

	for _, provider := range m.providers {
		err := provider.UnpinFile(ctx, name)
		if err != nil {
			errorMsg := fmt.Sprintf("%s: %v", provider.Name(), err)
			errors = append(errors, errorMsg)
			m.logger.WithError(err).WithField("provider", provider.Name()).Error("failed to unpin file")
		} else {
			successCount++
			m.logger.WithFields(logrus.Fields{
				"provider": provider.Name(),
				"file":     name,
			}).Info("successfully unpinned file")
		}
	}

	// Log summary
	if successCount > 0 {
		m.logger.WithFields(logrus.Fields{
			"file":    name,
			"success": successCount,
			"total":   len(m.providers),
			"failed":  len(m.providers) - successCount,
		}).Info("file unpinning completed")
	}

	// Don't return error for unpinning failures as they're not critical
	return nil
}

// GetProviders returns the list of configured providers
func (m *Manager) GetProviders() []StorageProvider {
	return m.providers
}

// HasProviders returns true if any providers are configured
func (m *Manager) HasProviders() bool {
	return len(m.providers) > 0
}

// Factory creates storage providers based on configuration
type Factory struct{}

// NewFactory creates a new storage provider factory
func NewFactory() *Factory {
	return &Factory{}
}

// CreateProviders creates storage providers based on the configuration
func (f *Factory) CreateProviders(config Config) ([]StorageProvider, error) {
	var providers []StorageProvider

	for _, providerName := range config.EnabledProviders {
		switch strings.ToLower(providerName) {
		case "pinata":
			if config.PinataJWT == "" {
				return nil, errors.New("PINATA_JWT is required when pinata provider is enabled")
			}
			providers = append(providers, NewPinataProvider(config.PinataJWT))

		case "filebase":
			if config.FilebaseAccessKey == "" || config.FilebaseSecretKey == "" || config.FilebaseBucket == "" {
				return nil, errors.New("FILEBASE_ACCESS_KEY, FILEBASE_SECRET_KEY, and FILEBASE_BUCKET are required when filebase provider is enabled")
			}
			
			// Set defaults for optional fields
			region := config.FilebaseRegion
			if region == "" {
				region = "us-east-1"
			}
			
			endpoint := config.FilebaseEndpoint
			if endpoint == "" {
				endpoint = "https://s3.filebase.com"
			}

			provider, err := NewFilebaseProvider(
				config.FilebaseAccessKey,
				config.FilebaseSecretKey,
				config.FilebaseBucket,
				region,
				endpoint,
			)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to create filebase provider")
			}
			providers = append(providers, provider)

		default:
			return nil, errors.Errorf("unknown storage provider: %s", providerName)
		}
	}

	return providers, nil
}
