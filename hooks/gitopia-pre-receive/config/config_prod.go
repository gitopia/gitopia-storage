package config

import "os"

func GetGRPCHost() string {
	// Check environment variable first
	if host := os.Getenv("GITOPIA_ADDR"); host != "" {
		return host
	}
	// Default to production endpoint
	return "gitopia-grpc.polkachu.com:11390"
}
