//go:build prod

package main

import (
	"context"
)

func startPprofServer(ctx context.Context, port int) error {
	// No-op for production builds
	return nil
}

func startMemoryMonitor(ctx context.Context) {
	// No-op for production builds
}
