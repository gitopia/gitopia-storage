package handler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// ChallengeManager manages multiple WebSocket connections for challenge response redundancy
type ChallengeManager struct {
	connections      []*ChallengeConnection
	challengeHandler *ChallengeEventHandler
	mu               sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	grpcClient       *RedundantGrpcClient
	firstProcessed   bool
	processingMu     sync.Mutex
}

// ChallengeConnection represents a single WebSocket connection with health monitoring
type ChallengeConnection struct {
	client       *gitopia.WSEvents
	rpcEndpoint  string
	grpcEndpoint string
	isHealthy    bool
	lastSeen     time.Time
	failures     int
	mu           sync.RWMutex
}

// NewChallengeManager creates a new challenge manager with redundant connections
func NewChallengeManager(ctx context.Context, challengeHandler *ChallengeEventHandler) (*ChallengeManager, error) {
	childCtx, cancel := context.WithCancel(ctx)

	cm := &ChallengeManager{
		challengeHandler: challengeHandler,
		ctx:              childCtx,
		cancel:           cancel,
	}

	// Parse validator endpoints (paired RPC/gRPC)
	validatorEndpoints := parseValidatorEndpoints()

	// Create gRPC client for storage queries using paired endpoints
	if len(validatorEndpoints) > 0 {
		grpcClient, err := NewRedundantGrpcClient(ctx, validatorEndpoints)
		if err != nil {
			logger.FromContext(ctx).WithError(err).Warn("failed to create redundant gRPC client, continuing without gRPC redundancy")
		} else {
			cm.grpcClient = grpcClient
		}
	}

	maxConnections := viper.GetInt("CHALLENGE_MAX_CONCURRENT_RESPONSES")
	if maxConnections == 0 {
		maxConnections = min(len(validatorEndpoints), 3) // Default to 3 or number of endpoints
	}

	// Create WebSocket connections up to the maximum configured
	for i, ve := range validatorEndpoints {
		if i >= maxConnections {
			break
		}

		conn, err := cm.createConnection(ve.RpcEndpoint, ve.GrpcEndpoint)
		if err != nil {
			logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
				"rpc_endpoint":  ve.RpcEndpoint,
				"grpc_endpoint": ve.GrpcEndpoint,
			}).Warn("failed to create challenge connection, continuing with others")
			continue
		}
		cm.connections = append(cm.connections, conn)
	}

	if len(cm.connections) == 0 {
		return nil, errors.New("failed to create any challenge connections")
	}

	// gRPC client is now managed internally by the challenge manager
	if cm.grpcClient != nil {
		logger.FromContext(ctx).Info("gRPC client configured for enhanced challenge processing")
	}

	logger.FromContext(ctx).WithField("connections", len(cm.connections)).Info("challenge manager initialized with redundant connections")
	return cm, nil
}

// parseValidatorEndpoints parses validator endpoints from configuration
func parseValidatorEndpoints() []ValidatorEndpoint {
	// Try new paired endpoint format first
	validatorEndpoints := viper.Get("VALIDATOR_ENDPOINTS")
	if validatorEndpoints != nil {
		if endpoints, ok := validatorEndpoints.([]interface{}); ok {
			var result []ValidatorEndpoint
			for _, ep := range endpoints {
				if pair, ok := ep.([]interface{}); ok && len(pair) == 2 {
					if rpc, ok := pair[0].(string); ok {
						if grpc, ok := pair[1].(string); ok {
							result = append(result, ValidatorEndpoint{
								RpcEndpoint:  rpc,
								GrpcEndpoint: grpc,
							})
						}
					}
				}
			}
			if len(result) > 0 {
				return result
			}
		}
	}

	rpcEndpoint := viper.GetString("TM_ADDR")
	grpcEndpoint := viper.GetString("GITOPIA_ADDR")
	result := []ValidatorEndpoint{{
		RpcEndpoint:  rpcEndpoint,
		GrpcEndpoint: grpcEndpoint,
	}}

	return result
}

// createConnection creates and configures a single WebSocket connection
func (cm *ChallengeManager) createConnection(rpcEndpoint, grpcEndpoint string) (*ChallengeConnection, error) {
	// Create WebSocket client with the RPC endpoint
	client, err := gitopia.NewWSEventsWithEndpoint(cm.ctx, rpcEndpoint)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create WebSocket client for endpoint %s", rpcEndpoint)
	}

	conn := &ChallengeConnection{
		client:       client,
		rpcEndpoint:  rpcEndpoint,
		grpcEndpoint: grpcEndpoint,
		isHealthy:    true,
		lastSeen:     time.Now(),
		failures:     0,
	}

	// Subscribe to challenge events
	challengeQuery := "tm.event='NewBlock' AND gitopia.gitopia.storage.EventChallengeCreated.challenge_id EXISTS"
	if err := client.SubscribeQueries(cm.ctx, challengeQuery); err != nil {
		client.Close()
		return nil, errors.Wrapf(err, "failed to subscribe to challenges on endpoint %s", rpcEndpoint)
	}

	return conn, nil
}

// Start begins processing challenge events from all connections
func (cm *ChallengeManager) Start() error {
	if len(cm.connections) == 0 {
		return errors.New("no healthy connections available")
	}

	// Start health monitoring
	go cm.healthMonitor()

	// Start event processors for each connection
	for i, conn := range cm.connections {
		go cm.processConnectionEvents(i, conn)
	}

	logger.FromContext(cm.ctx).WithField("active_connections", len(cm.connections)).Info("challenge manager started")
	return nil
}

// processConnectionEvents processes events from a single connection
func (cm *ChallengeManager) processConnectionEvents(connIndex int, conn *ChallengeConnection) {
	logger := logger.FromContext(cm.ctx).WithFields(logrus.Fields{
		"connection_index": connIndex,
		"endpoint":         conn.rpcEndpoint,
	})

	defer func() {
		if r := recover(); r != nil {
			logger.WithField("panic", r).Error("challenge connection processor panicked")
		}
	}()

	for {
		select {
		case <-cm.ctx.Done():
			logger.Info("challenge connection processor stopping")
			return
		default:
			done, errChan := conn.client.ProcessEvents(cm.ctx, func(ctx context.Context, eventBuf []byte) error {
				return cm.handleChallengeEvent(ctx, eventBuf, conn)
			})

			select {
			case err := <-errChan:
				cm.handleConnectionError(conn, err)
				// Try to reconnect
				if cm.reconnectConnection(conn) {
					logger.Info("successfully reconnected challenge connection")
					continue
				}
				logger.Error("failed to reconnect challenge connection, marking as unhealthy")
				return
			case <-done:
				logger.Info("challenge connection completed normally")
				return
			case <-cm.ctx.Done():
				return
			}
		}
	}
}

// handleChallengeEvent processes a challenge event with deduplication and submission checking
func (cm *ChallengeManager) handleChallengeEvent(ctx context.Context, eventBuf []byte, conn *ChallengeConnection) error {
	// Update connection health
	conn.mu.Lock()
	conn.lastSeen = time.Now()
	conn.isHealthy = true
	conn.failures = 0
	conn.mu.Unlock()

	// Extract challenge events from buffer
	events, err := UnmarshalChallengeEvent(eventBuf)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal challenge events")
	}

	for _, event := range events {
		// Check if we should process this challenge (racing logic)
		if !cm.shouldProcessChallenge(fmt.Sprintf("%d", event.ChallengeId), conn.rpcEndpoint) {
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"challenge_id": event.ChallengeId,
				"endpoint":     conn.rpcEndpoint,
			}).Debug("challenge already being processed by another connection")
			continue
		}

		// Create timeout context for challenge processing
		timeout := viper.GetDuration("CHALLENGE_RESPONSE_TIMEOUT")
		if timeout == 0 {
			timeout = 8 * time.Second // Default timeout
		}

		challengeCtx, cancel := context.WithTimeout(ctx, timeout)

		// Process the challenge
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"challenge_id": event.ChallengeId,
			"endpoint":     conn.rpcEndpoint,
			"timeout":      timeout,
		}).Info("processing challenge event")

		// Process individual challenge event
		err = cm.challengeHandler.Process(challengeCtx, event)
		cancel()

		if err != nil {
			logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
				"challenge_id": event.ChallengeId,
				"endpoint":     conn.rpcEndpoint,
			}).Error("challenge processing failed")
			// Continue processing other challenges instead of returning error
			continue
		}

		logger.FromContext(ctx).WithFields(logrus.Fields{
			"challenge_id": event.ChallengeId,
			"endpoint":     conn.rpcEndpoint,
		}).Info("challenge processed successfully")
	}

	return nil
}

// shouldProcessChallenge implements racing logic - only process the first received challenge event
func (cm *ChallengeManager) shouldProcessChallenge(challengeID, endpoint string) bool {
	cm.processingMu.Lock()
	defer cm.processingMu.Unlock()

	// If this is the first challenge, mark it as processed and allow it
	if !cm.firstProcessed {
		cm.firstProcessed = true
		logger.FromContext(cm.ctx).WithFields(logrus.Fields{
			"challenge_id": challengeID,
			"endpoint":     endpoint,
		}).Info("processing first challenge event")
		return true
	}

	// All subsequent challenges are ignored
	logger.FromContext(cm.ctx).WithFields(logrus.Fields{
		"challenge_id": challengeID,
		"endpoint":     endpoint,
	}).Debug("ignoring subsequent challenge event - first already processed")
	return false
}

// handleConnectionError handles connection errors and updates health status
func (cm *ChallengeManager) handleConnectionError(conn *ChallengeConnection, err error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.failures++
	conn.isHealthy = false

	logger.FromContext(cm.ctx).WithError(err).WithFields(logrus.Fields{
		"endpoint": conn.rpcEndpoint,
		"failures": conn.failures,
	}).Warn("challenge connection error")
}

// reconnectConnection attempts to reconnect a failed connection
func (cm *ChallengeManager) reconnectConnection(conn *ChallengeConnection) bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	// Close existing client
	if conn.client != nil {
		conn.client.Close()
	}

	// Create new connection
	newConn, err := cm.createConnection(conn.rpcEndpoint, conn.grpcEndpoint)
	if err != nil {
		logger.FromContext(cm.ctx).WithError(err).WithFields(logrus.Fields{
			"rpc_endpoint":  conn.rpcEndpoint,
			"grpc_endpoint": conn.grpcEndpoint,
		}).Error("failed to reconnect challenge connection")
		return false
	}

	// Update connection
	conn.client = newConn.client
	conn.isHealthy = true
	conn.lastSeen = time.Now()
	conn.failures = 0

	return true
}

// healthMonitor periodically checks connection health
func (cm *ChallengeManager) healthMonitor() {
	interval := viper.GetDuration("CHALLENGE_CONNECTION_HEALTH_CHECK")
	if interval == 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			return
		case <-ticker.C:
			cm.checkConnectionHealth()
		}
	}
}

// checkConnectionHealth checks and reports on connection health
func (cm *ChallengeManager) checkConnectionHealth() {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	healthyCount := 0
	for i, conn := range cm.connections {
		conn.mu.RLock()
		isHealthy := conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute
		conn.mu.RUnlock()

		if isHealthy {
			healthyCount++
		}

		logger.FromContext(cm.ctx).WithFields(logrus.Fields{
			"connection_index": i,
			"endpoint":         conn.rpcEndpoint,
			"healthy":          isHealthy,
			"failures":         conn.failures,
			"last_seen":        conn.lastSeen,
		}).Debug("challenge connection health check")
	}

	logger.FromContext(cm.ctx).WithFields(logrus.Fields{
		"healthy_connections": healthyCount,
		"total_connections":   len(cm.connections),
	}).Info("challenge connection health summary")

	// Alert if too few healthy connections
	if healthyCount == 0 {
		logger.FromContext(cm.ctx).Error("NO HEALTHY CHALLENGE CONNECTIONS - CRITICAL ISSUE")
	} else if healthyCount == 1 {
		logger.FromContext(cm.ctx).Warn("only one healthy challenge connection remaining")
	}
}

// Close closes all connections and stops the manager
func (cm *ChallengeManager) Close() {
	cm.cancel()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, conn := range cm.connections {
		if conn.client != nil {
			conn.client.Close()
		}
	}

	// Close gRPC client if available
	if cm.grpcClient != nil {
		cm.grpcClient.Close()
	}

	logger.FromContext(cm.ctx).Info("challenge manager closed")
}

// GetHealthyConnectionCount returns the number of healthy connections
func (cm *ChallengeManager) GetHealthyConnectionCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	count := 0
	for _, conn := range cm.connections {
		conn.mu.RLock()
		if conn.isHealthy && time.Since(conn.lastSeen) < 2*time.Minute {
			count++
		}
		conn.mu.RUnlock()
	}
	return count
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
