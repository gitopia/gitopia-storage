//go:build prod

package main

import (
	"context"
	"time"
)

func startPprofServer(ctx context.Context, port int) error {
	// No-op for production builds
	return nil
}

func startMemoryMonitor(ctx context.Context) {
	// No-op for production builds
}

func startMemoryMonitorWithInterval(ctx context.Context, interval time.Duration) {
	// No-op for production builds
}
