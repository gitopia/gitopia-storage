//go:build !prod

package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func startPprofServer(ctx context.Context, port int) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	logrus.Infof("starting pprof server on http://%s/debug/pprof/", addr)

	server := &http.Server{Addr: addr, Handler: nil} // Use default mux

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return errors.Wrap(err, "pprof server error")
	}
	logrus.Info("pprof server stopped")
	return nil
}

func startMemoryMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			logrus.Infof("Memory Usage: Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v",
				m.Alloc/1024/1024, m.TotalAlloc/1024/1024, m.Sys/1024/1024, m.NumGC)
		case <-ctx.Done():
			logrus.Info("memory monitor stopped")
			return
		}
	}
}
