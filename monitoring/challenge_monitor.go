package monitoring

import (
	"context"
	"sync"
	"time"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	challengeResponsesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: viper.GetString("APP_NAME"),
		Name:      "challenge_responses_total",
		Help:      "Total number of challenge responses processed",
	}, []string{"status"}) // status: success, failed, timeout

	challengeResponseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: viper.GetString("APP_NAME"),
		Name:      "challenge_response_duration_seconds",
		Help:      "Time taken to process challenge responses",
		Buckets:   prometheus.DefBuckets,
	}, []string{"challenge_type"})

	lastChallengeTime = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: viper.GetString("APP_NAME"),
		Name:      "last_challenge_timestamp",
		Help:      "Timestamp of the last processed challenge",
	})

	consecutiveFailures = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: viper.GetString("APP_NAME"),
		Name:      "consecutive_challenge_failures",
		Help:      "Number of consecutive challenge failures",
	})
)

type ChallengeMonitor struct {
	mu                    sync.RWMutex
	lastChallengeReceived time.Time
	consecutiveFailCount  int
	alertThreshold        int
	alertCallback         func(string)
}

func NewChallengeMonitor(alertThreshold int, alertCallback func(string)) *ChallengeMonitor {
	return &ChallengeMonitor{
		alertThreshold: alertThreshold,
		alertCallback:  alertCallback,
	}
}

// RecordChallengeReceived records when a challenge is received
func (cm *ChallengeMonitor) RecordChallengeReceived() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.lastChallengeReceived = time.Now()
	lastChallengeTime.SetToCurrentTime()
}

// RecordChallengeSuccess records a successful challenge response
func (cm *ChallengeMonitor) RecordChallengeSuccess(challengeType string, duration time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	challengeResponsesTotal.WithLabelValues("success").Inc()
	challengeResponseDuration.WithLabelValues(challengeType).Observe(duration.Seconds())
	
	// Reset consecutive failure count on success
	cm.consecutiveFailCount = 0
	consecutiveFailures.Set(0)
}

// RecordChallengeFailure records a failed challenge response
func (cm *ChallengeMonitor) RecordChallengeFailure(challengeType string, reason string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	status := "failed"
	if reason == "timeout" {
		status = "timeout"
	}
	
	challengeResponsesTotal.WithLabelValues(status).Inc()
	cm.consecutiveFailCount++
	consecutiveFailures.Set(float64(cm.consecutiveFailCount))
	
	// Trigger alert if threshold exceeded
	if cm.consecutiveFailCount >= cm.alertThreshold && cm.alertCallback != nil {
		cm.alertCallback(reason)
	}
}

// StartHealthCheck starts a background health check routine
func (cm *ChallengeMonitor) StartHealthCheck(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			cm.checkHealth(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (cm *ChallengeMonitor) checkHealth(ctx context.Context) {
	cm.mu.RLock()
	lastReceived := cm.lastChallengeReceived
	failCount := cm.consecutiveFailCount
	cm.mu.RUnlock()
	
	// Check if we haven't received challenges in a while
	// Challenges should come every ~30 minutes according to docs
	if !lastReceived.IsZero() && time.Since(lastReceived) > 45*time.Minute {
		logger.FromContext(ctx).WithFields(logrus.Fields{
			"last_challenge": lastReceived,
			"time_since":     time.Since(lastReceived),
		}).Warn("No challenges received recently - possible connection issue")
		
		if cm.alertCallback != nil {
			cm.alertCallback("no_challenges_received")
		}
	}
	
	// Log current health status
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"last_challenge_time":   lastReceived,
		"consecutive_failures":  failCount,
		"time_since_challenge": time.Since(lastReceived),
	}).Debug("Challenge monitor health check")
}

// GetStats returns current monitoring statistics
func (cm *ChallengeMonitor) GetStats() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	return map[string]interface{}{
		"last_challenge_received": cm.lastChallengeReceived,
		"consecutive_failures":    cm.consecutiveFailCount,
		"time_since_last":        time.Since(cm.lastChallengeReceived),
	}
}
