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
	connections    []*ChallengeConnection
	challengeHandler *ChallengeEventHandler
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	responseTracker map[string]*ChallengeResponse
	trackerMu      sync.RWMutex
}

// ChallengeConnection represents a single WebSocket connection with health monitoring
type ChallengeConnection struct {
	client     *gitopia.WSEvents
	endpoint   string
	isHealthy  bool
	lastSeen   time.Time
	failures   int
	mu         sync.RWMutex
}

// ChallengeResponse tracks responses to prevent duplicate submissions
type ChallengeResponse struct {
	challengeID string
	submitted   bool
	submittedAt time.Time
	endpoint    string
	mu          sync.Mutex
}

// NewChallengeManager creates a new challenge manager with redundant connections
func NewChallengeManager(ctx context.Context, challengeHandler *ChallengeEventHandler) (*ChallengeManager, error) {
	childCtx, cancel := context.WithCancel(ctx)
	
	cm := &ChallengeManager{
		challengeHandler: challengeHandler,
		ctx:             childCtx,
		cancel:          cancel,
		responseTracker: make(map[string]*ChallengeResponse),
	}

	// Get RPC endpoints from configuration
	endpoints := viper.GetStringSlice("TM_RPC_ENDPOINTS")
	if len(endpoints) == 0 {
		// Fallback to single endpoint if array not configured
		endpoints = []string{viper.GetString("TM_ADDR")}
	}

	maxConnections := viper.GetInt("CHALLENGE_MAX_CONCURRENT_RESPONSES")
	if maxConnections == 0 {
		maxConnections = min(len(endpoints), 3) // Default to 3 or number of endpoints
	}

	// Create connections up to the maximum configured
	for i, endpoint := range endpoints {
		if i >= maxConnections {
			break
		}

		conn, err := cm.createConnection(endpoint)
		if err != nil {
			logger.FromContext(ctx).WithError(err).WithField("endpoint", endpoint).Warn("failed to create challenge connection, continuing with others")
			continue
		}
		cm.connections = append(cm.connections, conn)
	}

	if len(cm.connections) == 0 {
		return nil, errors.New("failed to create any challenge connections")
	}

	logger.FromContext(ctx).WithField("connections", len(cm.connections)).Info("challenge manager initialized with redundant connections")
	return cm, nil
}

// createConnection creates and configures a single WebSocket connection
func (cm *ChallengeManager) createConnection(endpoint string) (*ChallengeConnection, error) {
	// Temporarily set the TM_ADDR to the desired endpoint
	originalAddr := viper.GetString("TM_ADDR")
	viper.Set("TM_ADDR", endpoint)
	defer viper.Set("TM_ADDR", originalAddr) // Restore original

	// Create WebSocket client with the temporarily set endpoint
	client, err := gitopia.NewWSEvents(cm.ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create WebSocket client for endpoint %s", endpoint)
	}

	conn := &ChallengeConnection{
		client:    client,
		endpoint:  endpoint,
		isHealthy: true,
		lastSeen:  time.Now(),
		failures:  0,
	}

	// Subscribe to challenge events
	challengeQuery := "tm.event='NewBlock' AND gitopia.gitopia.storage.EventChallengeCreated.challenge_id EXISTS"
	if err := client.SubscribeQueries(cm.ctx, challengeQuery); err != nil {
		client.Close()
		return nil, errors.Wrapf(err, "failed to subscribe to challenges on endpoint %s", endpoint)
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
		"endpoint":        conn.endpoint,
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

// handleChallengeEvent processes a challenge event with deduplication
func (cm *ChallengeManager) handleChallengeEvent(ctx context.Context, eventBuf []byte, conn *ChallengeConnection) error {
	// Update connection health
	conn.mu.Lock()
	conn.lastSeen = time.Now()
	conn.isHealthy = true
	conn.failures = 0
	conn.mu.Unlock()

	// Extract challenge ID for deduplication
	challengeID, err := cm.extractChallengeID(eventBuf)
	if err != nil {
		return errors.Wrap(err, "failed to extract challenge ID")
	}

	// Check if we should process this challenge (racing logic)
	if !cm.shouldProcessChallenge(challengeID, conn.endpoint) {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"challenge_id": challengeID,
			"endpoint":     conn.endpoint,
		}).Debug("challenge already being processed by another connection")
		return nil
	}

	// Create timeout context for challenge processing
	timeout := viper.GetDuration("CHALLENGE_RESPONSE_TIMEOUT")
	if timeout == 0 {
		timeout = 8 * time.Second // Default timeout
	}

	challengeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Process the challenge
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challenge_id": challengeID,
		"endpoint":     conn.endpoint,
		"timeout":      timeout,
	}).Info("processing challenge event")

	err = cm.challengeHandler.Handle(challengeCtx, eventBuf)
	
	// Mark as submitted regardless of success/failure to prevent retries
	cm.markChallengeSubmitted(challengeID, conn.endpoint)

	if err != nil {
		logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
			"challenge_id": challengeID,
			"endpoint":     conn.endpoint,
		}).Error("challenge processing failed")
		return err
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challenge_id": challengeID,
		"endpoint":     conn.endpoint,
	}).Info("challenge processed successfully")

	return nil
}

// shouldProcessChallenge implements racing logic - first connection wins
func (cm *ChallengeManager) shouldProcessChallenge(challengeID, endpoint string) bool {
	cm.trackerMu.Lock()
	defer cm.trackerMu.Unlock()

	response, exists := cm.responseTracker[challengeID]
	if !exists {
		// First time seeing this challenge, create tracker and allow processing
		cm.responseTracker[challengeID] = &ChallengeResponse{
			challengeID: challengeID,
			submitted:   false,
			endpoint:    endpoint,
		}
		return true
	}

	response.mu.Lock()
	defer response.mu.Unlock()

	// If already submitted, don't process again
	if response.submitted {
		return false
	}

	// If not submitted yet, this connection wins the race
	return true
}

// markChallengeSubmitted marks a challenge as submitted
func (cm *ChallengeManager) markChallengeSubmitted(challengeID, endpoint string) {
	cm.trackerMu.Lock()
	defer cm.trackerMu.Unlock()

	if response, exists := cm.responseTracker[challengeID]; exists {
		response.mu.Lock()
		response.submitted = true
		response.submittedAt = time.Now()
		response.endpoint = endpoint
		response.mu.Unlock()
	}
}

// extractChallengeID extracts challenge ID from event buffer
func (cm *ChallengeManager) extractChallengeID(eventBuf []byte) (string, error) {
	// This would need to be implemented based on the actual event structure
	// For now, using a placeholder implementation
	// In practice, you'd parse the JSON to extract the challenge ID
	return fmt.Sprintf("challenge_%d", time.Now().UnixNano()), nil
}

// handleConnectionError handles connection errors and updates health status
func (cm *ChallengeManager) handleConnectionError(conn *ChallengeConnection, err error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.failures++
	conn.isHealthy = false

	logger.FromContext(cm.ctx).WithError(err).WithFields(logrus.Fields{
		"endpoint": conn.endpoint,
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
	newConn, err := cm.createConnection(conn.endpoint)
	if err != nil {
		logger.FromContext(cm.ctx).WithError(err).WithField("endpoint", conn.endpoint).Error("failed to reconnect challenge connection")
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
			"endpoint":        conn.endpoint,
			"healthy":         isHealthy,
			"failures":        conn.failures,
			"last_seen":       conn.lastSeen,
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
