package poster

import (
	"errors"
	"sync"
	"time"

	"migadu/mizu/pkg/health"

	"go.uber.org/zap"
)

var (
	// ErrCircuitOpen is returned when the circuit breaker is open (too many failures)
	ErrCircuitOpen = errors.New("circuit breaker is open - too many recent failures")
)

// CircuitState represents the state of the circuit breaker
type CircuitState string

const (
	StateClosed   CircuitState = "closed"    // Normal operation, requests allowed
	StateOpen     CircuitState = "open"      // Too many failures, requests blocked
	StateHalfOpen CircuitState = "half_open" // Testing if service recovered
)

// CircuitBreaker implements the circuit breaker pattern to prevent cascading failures.
// It tracks success/failure rates and "opens" to fail fast when the destination is unhealthy.
type CircuitBreaker struct {
	mu sync.RWMutex

	// Configuration
	failureThreshold int           // Number of failures before opening
	successThreshold int           // Number of successes in half-open to close
	timeout          time.Duration // How long to stay open before trying half-open
	halfOpenMaxCalls int           // Max concurrent requests in half-open state
	resetTimeout     time.Duration // How long to wait before resetting counters

	// State
	state            CircuitState
	failureCount     int
	successCount     int
	consecutiveFails int
	lastFailureTime  time.Time
	lastStateChange  time.Time
	halfOpenCalls    int

	// Logging
	logger *zap.Logger
}

// CircuitBreakerConfig holds configuration for the circuit breaker
type CircuitBreakerConfig struct {
	Enabled          bool          `toml:"enabled"`
	FailureThreshold int           `toml:"failure_threshold"`   // failures before opening (default: 5)
	SuccessThreshold int           `toml:"success_threshold"`   // successes in half-open to close (default: 2)
	Timeout          time.Duration `toml:"timeout"`             // time to wait before half-open (default: 30s)
	HalfOpenMaxCalls int           `toml:"half_open_max_calls"` // max concurrent calls in half-open (default: 1)
	ResetTimeout     time.Duration `toml:"reset_timeout"`       // time before resetting counters (default: 60s)
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(config CircuitBreakerConfig, logger *zap.Logger) *CircuitBreaker {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Set defaults if not provided
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold == 0 {
		config.SuccessThreshold = 2
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.HalfOpenMaxCalls == 0 {
		config.HalfOpenMaxCalls = 1
	}
	if config.ResetTimeout == 0 {
		config.ResetTimeout = 60 * time.Second
	}

	return &CircuitBreaker{
		failureThreshold: config.FailureThreshold,
		successThreshold: config.SuccessThreshold,
		timeout:          config.Timeout,
		halfOpenMaxCalls: config.HalfOpenMaxCalls,
		resetTimeout:     config.ResetTimeout,
		state:            StateClosed,
		lastStateChange:  time.Now(),
		logger:           logger,
	}
}

// Call executes the given function through the circuit breaker.
// Returns ErrCircuitOpen if the circuit is open.
func (cb *CircuitBreaker) Call(fn func() error) error {
	// Check if we can proceed with the call
	if !cb.canProceed() {
		return ErrCircuitOpen
	}

	// Execute the function and record the result
	err := fn()
	cb.recordResult(err)

	return err
}

// canProceed determines if a request should be allowed based on circuit state
func (cb *CircuitBreaker) canProceed() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		// Check if we should reset counters (no failures for resetTimeout)
		if cb.failureCount > 0 && now.Sub(cb.lastFailureTime) > cb.resetTimeout {
			cb.failureCount = 0
			cb.consecutiveFails = 0
		}
		return true

	case StateOpen:
		// Check if timeout has elapsed, transition to half-open
		if now.Sub(cb.lastStateChange) >= cb.timeout {
			cb.logger.Info("Circuit breaker transitioning from Open to Half-Open")
			cb.state = StateHalfOpen
			cb.lastStateChange = now
			cb.halfOpenCalls = 0
			cb.successCount = 0
			return true
		}
		return false

	case StateHalfOpen:
		// Limit concurrent requests in half-open state
		if cb.halfOpenCalls < cb.halfOpenMaxCalls {
			cb.halfOpenCalls++
			return true
		}
		return false
	}

	return false
}

// recordResult records the success or failure of a request
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	if err != nil {
		// Failure
		cb.failureCount++
		cb.consecutiveFails++
		cb.lastFailureTime = now

		switch cb.state {
		case StateClosed:
			// Check if we should open the circuit
			if cb.consecutiveFails >= cb.failureThreshold {
				cb.logger.Warn("Circuit breaker transitioning from Closed to Open", zap.Int("failures", cb.consecutiveFails))
				cb.state = StateOpen
				cb.lastStateChange = now
			}

		case StateHalfOpen:
			// Any failure in half-open goes back to open
			cb.logger.Warn("Circuit breaker transitioning from Half-Open to Open due to failure")
			cb.state = StateOpen
			cb.lastStateChange = now
			cb.halfOpenCalls = 0
			cb.successCount = 0
		}
	} else {
		// Success
		cb.successCount++
		cb.consecutiveFails = 0 // Reset consecutive failure counter

		switch cb.state {
		case StateHalfOpen:
			// Decrement half-open call counter (with defensive guard against negative)
			if cb.halfOpenCalls > 0 {
				cb.halfOpenCalls--
			}
			// Check if we have enough successes to close the circuit
			if cb.successCount >= cb.successThreshold {
				cb.logger.Info("Circuit breaker transitioning from Half-Open to Closed", zap.Int("successes", cb.successCount))
				cb.state = StateClosed
				cb.lastStateChange = now
				cb.failureCount = 0
				cb.successCount = 0
			}

		case StateClosed:
			// Reset failure count on success if we've been healthy for a while
			if now.Sub(cb.lastFailureTime) > cb.resetTimeout {
				cb.failureCount = 0
			}
		}
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats returns current statistics about the circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	stats := map[string]interface{}{
		"state":             string(cb.state),
		"failure_count":     cb.failureCount,
		"success_count":     cb.successCount,
		"consecutive_fails": cb.consecutiveFails,
		"failure_threshold": cb.failureThreshold,
		"success_threshold": cb.successThreshold,
	}

	if cb.state == StateOpen {
		timeUntilHalfOpen := cb.timeout - time.Since(cb.lastStateChange)
		if timeUntilHalfOpen > 0 {
			stats["time_until_half_open"] = timeUntilHalfOpen.String()
		} else {
			stats["time_until_half_open"] = "transitioning..."
		}
	}

	return stats
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.consecutiveFails = 0
	cb.halfOpenCalls = 0
	cb.lastStateChange = time.Now()
}

// Name returns the component name for health checks
func (cb *CircuitBreaker) Name() string {
	return "circuit_breaker"
}

// CheckHealth returns the health status of the circuit breaker
// Status is "healthy" when closed, "degraded" when half-open, "unhealthy" when open
func (cb *CircuitBreaker) CheckHealth() health.ComponentStatus {
	stats := cb.GetStats()
	state := cb.GetState()

	var status string
	switch state {
	case StateClosed:
		status = "healthy"
	case StateHalfOpen:
		status = "degraded"
	case StateOpen:
		status = "unhealthy"
	default:
		status = "unknown"
	}

	return health.ComponentStatus{
		Status:  status,
		Details: stats,
	}
}
