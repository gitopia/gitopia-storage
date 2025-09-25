package handler

import (
	"context"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/gitopia/gitopia-go/logger"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ValidatorEndpoint represents a paired RPC/gRPC endpoint from a validator
type ValidatorEndpoint struct {
	RpcEndpoint  string
	GrpcEndpoint string
}

// GrpcConnection represents a single gRPC connection with health monitoring
type GrpcConnection struct {
	grpcEndpoint string
	rpcEndpoint  string // Associated RPC endpoint for pairing
	conn         *grpc.ClientConn
	queries      GrpcQueryClients
	isHealthy    bool
	lastSeen     time.Time
	failures     int
	mu           sync.RWMutex
}

// GrpcQueryClients holds all the query clients for different modules
type GrpcQueryClients struct {
	Storage storagetypes.QueryClient
}

// RedundantGrpcClient manages multiple gRPC connections for redundancy
type RedundantGrpcClient struct {
	connections       []*GrpcConnection
	mu                sync.RWMutex
	ctx               context.Context
	cancel            context.CancelFunc
	healthCheckTicker *time.Ticker
}

// NewRedundantGrpcClient creates a new redundant gRPC client
func NewRedundantGrpcClient(ctx context.Context, validatorEndpoints []ValidatorEndpoint) (*RedundantGrpcClient, error) {
	childCtx, cancel := context.WithCancel(ctx)

	client := &RedundantGrpcClient{
		ctx:    childCtx,
		cancel: cancel,
	}

	// Create connections to all endpoints
	for _, ve := range validatorEndpoints {
		conn, err := client.createConnection(ve.GrpcEndpoint, ve.RpcEndpoint)
		if err != nil {
			logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
				"grpc_endpoint": ve.GrpcEndpoint,
				"rpc_endpoint":  ve.RpcEndpoint,
			}).Warn("failed to create gRPC connection, continuing with others")
			continue
		}
		client.connections = append(client.connections, conn)
	}

	if len(client.connections) == 0 {
		return nil, errors.New("failed to create any gRPC connections")
	}

	// Start health monitoring
	client.startHealthMonitoring()

	logger.FromContext(ctx).WithField("connections", len(client.connections)).Info("redundant gRPC client initialized")
	return client, nil
}

// createConnection creates and configures a single gRPC connection
func (c *RedundantGrpcClient) createConnection(grpcEndpoint, rpcEndpoint string) (*GrpcConnection, error) {
	grpcConn, err := grpc.Dial(grpcEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial gRPC endpoint %s", grpcEndpoint)
	}

	// Create query clients
	queries := GrpcQueryClients{
		Storage: storagetypes.NewQueryClient(grpcConn),
	}

	conn := &GrpcConnection{
		grpcEndpoint: grpcEndpoint,
		rpcEndpoint:  rpcEndpoint,
		conn:         grpcConn,
		queries:      queries,
		isHealthy:    true,
		lastSeen:     time.Now(),
		failures:     0,
	}

	return conn, nil
}

// GetHealthyConnection returns the first healthy connection, with failover
func (c *RedundantGrpcClient) GetHealthyConnection() (*GrpcConnection, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Try to find a healthy connection
	for _, conn := range c.connections {
		conn.mu.RLock()
		isHealthy := conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute
		conn.mu.RUnlock()

		if isHealthy {
			return conn, nil
		}
	}

	return nil, errors.New("no healthy gRPC connections available")
}

// GetConnectionForRpcEndpoint returns the gRPC connection associated with a specific RPC endpoint
func (c *RedundantGrpcClient) GetConnectionForRpcEndpoint(rpcEndpoint string) (*GrpcConnection, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// First try to find the paired connection for this RPC endpoint
	for _, conn := range c.connections {
		conn.mu.RLock()
		isHealthy := conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute
		isPaired := conn.rpcEndpoint == rpcEndpoint
		conn.mu.RUnlock()

		if isPaired && isHealthy {
			return conn, nil
		}
	}

	// If paired connection is not healthy, fallback to any healthy connection
	return c.GetHealthyConnection()
}

// ProviderLiveness queries provider liveness info with automatic failover
func (c *RedundantGrpcClient) ProviderLiveness(ctx context.Context, providerAddress string) (*storagetypes.ProviderLivenessInfo, error) {
	conn, err := c.GetHealthyConnection()
	if err != nil {
		return nil, err
	}

	req := &storagetypes.QueryProviderLivenessRequest{
		Address: providerAddress,
	}

	// Try the query with timeout
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := conn.queries.Storage.ProviderLiveness(queryCtx, req)
	if err != nil {
		// Mark connection as unhealthy and try another
		c.markConnectionUnhealthy(conn, err)

		// Retry with another connection
		return c.retryProviderLiveness(ctx, providerAddress)
	}

	// Update connection health
	c.markConnectionHealthy(conn)
	return &resp.LivenessInfo, nil
}

// ProviderLivenessForRpcEndpoint queries provider liveness using the gRPC endpoint paired with the specified RPC endpoint
func (c *RedundantGrpcClient) ProviderLivenessForRpcEndpoint(ctx context.Context, providerAddress, rpcEndpoint string) (*storagetypes.ProviderLivenessInfo, error) {
	// If no specific RPC endpoint provided, use any healthy connection
	if rpcEndpoint == "" {
		return c.ProviderLiveness(ctx, providerAddress)
	}

	conn, err := c.GetConnectionForRpcEndpoint(rpcEndpoint)
	if err != nil {
		// Fallback to any healthy connection if paired connection not available
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"rpc_endpoint": rpcEndpoint,
			"error":        err.Error(),
		}).Debug("paired gRPC connection not available, using fallback")
		return c.ProviderLiveness(ctx, providerAddress)
	}

	req := &storagetypes.QueryProviderLivenessRequest{
		Address: providerAddress,
	}

	// Try the query with timeout
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := conn.queries.Storage.ProviderLiveness(queryCtx, req)
	if err != nil {
		// Mark connection as unhealthy and try another
		c.markConnectionUnhealthy(conn, err)

		logger.FromContext(ctx).WithFields(logrus.Fields{
			"rpc_endpoint":  rpcEndpoint,
			"grpc_endpoint": conn.grpcEndpoint,
			"error":         err.Error(),
		}).Debug("paired gRPC query failed, retrying with fallback")

		// Retry with any healthy connection
		return c.ProviderLiveness(ctx, providerAddress)
	}

	// Update connection health
	c.markConnectionHealthy(conn)

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"rpc_endpoint":  rpcEndpoint,
		"grpc_endpoint": conn.grpcEndpoint,
	}).Debug("successfully used paired gRPC endpoint for ProviderLiveness query")

	return &resp.LivenessInfo, nil
}

// retryProviderLiveness retries the query with a different connection
func (c *RedundantGrpcClient) retryProviderLiveness(ctx context.Context, providerAddress string) (*storagetypes.ProviderLivenessInfo, error) {
	conn, err := c.GetHealthyConnection()
	if err != nil {
		return nil, err
	}

	req := &storagetypes.QueryProviderLivenessRequest{
		Address: providerAddress,
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := conn.queries.Storage.ProviderLiveness(queryCtx, req)
	if err != nil {
		c.markConnectionUnhealthy(conn, err)
		return nil, errors.Wrap(err, "failed to query provider liveness after retry")
	}

	c.markConnectionHealthy(conn)
	return &resp.LivenessInfo, nil
}

// Challenge queries challenge details with automatic failover
func (c *RedundantGrpcClient) Challenge(ctx context.Context, challengeId uint64) (*storagetypes.Challenge, error) {
	conn, err := c.GetHealthyConnection()
	if err != nil {
		return nil, err
	}

	req := &storagetypes.QueryChallengeRequest{
		Id: challengeId,
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := conn.queries.Storage.Challenge(queryCtx, req)
	if err != nil {
		c.markConnectionUnhealthy(conn, err)
		return c.retryChallenge(ctx, challengeId)
	}

	c.markConnectionHealthy(conn)
	return &resp.Challenge, nil
}

// retryChallenge retries the challenge query with a different connection
func (c *RedundantGrpcClient) retryChallenge(ctx context.Context, challengeId uint64) (*storagetypes.Challenge, error) {
	conn, err := c.GetHealthyConnection()
	if err != nil {
		return nil, err
	}

	req := &storagetypes.QueryChallengeRequest{
		Id: challengeId,
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := conn.queries.Storage.Challenge(queryCtx, req)
	if err != nil {
		c.markConnectionUnhealthy(conn, err)
		return nil, errors.Wrap(err, "failed to query challenge after retry")
	}

	c.markConnectionHealthy(conn)
	return &resp.Challenge, nil
}

// markConnectionHealthy marks a connection as healthy
func (c *RedundantGrpcClient) markConnectionHealthy(conn *GrpcConnection) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.isHealthy = true
	conn.lastSeen = time.Now()
	conn.failures = 0
}

// markConnectionUnhealthy marks a connection as unhealthy
func (c *RedundantGrpcClient) markConnectionUnhealthy(conn *GrpcConnection, err error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.isHealthy = false
	conn.failures++

	logger.FromContext(c.ctx).WithError(err).WithFields(logrus.Fields{
		"endpoint": conn.grpcEndpoint,
		"failures": conn.failures,
	}).Warn("gRPC connection marked unhealthy")
}

// startHealthMonitoring starts periodic health checks
func (c *RedundantGrpcClient) startHealthMonitoring() {
	c.healthCheckTicker = time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.healthCheckTicker.C:
				c.performHealthCheck()
			}
		}
	}()
}

// performHealthCheck checks the health of all connections
func (c *RedundantGrpcClient) performHealthCheck() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	healthyCount := 0
	for i, conn := range c.connections {
		conn.mu.RLock()
		isHealthy := conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute
		conn.mu.RUnlock()

		if isHealthy {
			healthyCount++
		}

		logger.FromContext(c.ctx).WithFields(logrus.Fields{
			"connection_index": i,
			"endpoint":         conn.grpcEndpoint,
			"healthy":          isHealthy,
			"failures":         conn.failures,
			"last_seen":        conn.lastSeen,
		}).Debug("gRPC connection health check")
	}

	logger.FromContext(c.ctx).WithFields(logrus.Fields{
		"healthy_connections": healthyCount,
		"total_connections":   len(c.connections),
	}).Info("gRPC connection health summary")

	// Alert if too few healthy connections
	if healthyCount == 0 {
		logger.FromContext(c.ctx).Error("NO HEALTHY GRPC CONNECTIONS - CRITICAL ISSUE")
	} else if healthyCount == 1 {
		logger.FromContext(c.ctx).Warn("only one healthy gRPC connection remaining")
	}
}

// GetHealthyConnectionCount returns the number of healthy connections
func (c *RedundantGrpcClient) GetHealthyConnectionCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for _, conn := range c.connections {
		conn.mu.RLock()
		if conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute {
			count++
		}
		conn.mu.RUnlock()
	}
	return count
}

// Close closes all connections and stops health monitoring
func (c *RedundantGrpcClient) Close() error {
	c.cancel()

	if c.healthCheckTicker != nil {
		c.healthCheckTicker.Stop()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, conn := range c.connections {
		if conn.conn != nil {
			conn.conn.Close()
		}
	}

	logger.FromContext(c.ctx).Info("redundant gRPC client closed")
	return nil
}
