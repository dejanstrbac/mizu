package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RateLimiter implements a sliding window rate limiter with optional distributed gossip sync.
// It tracks connection attempts per IP address over a configurable time window.
type RateLimiter struct {
	mu                   sync.RWMutex
	enabled              bool
	connectionsPerMinute int
	windowSize           time.Duration
	ipWindows            map[string]*connectionWindow // IP -> sliding window of connection timestamps
	gossipEnabled        bool
	gossipInterval       time.Duration
	logger               *zap.Logger
	peerURLs             []string // Cluster peer URLs for gossip
	ctx                  context.Context
	cancel               context.CancelFunc
}

// connectionWindow tracks connection attempts for a single IP using a sliding window
type connectionWindow struct {
	timestamps  []time.Time
	lastCleanup time.Time
}

// RateLimitData represents rate limit data that can be gossiped across the cluster
type RateLimitData struct {
	IP          string    `json:"ip"`
	Connections int       `json:"connections"`
	WindowStart time.Time `json:"window_start"`
	ReportedAt  time.Time `json:"reported_at"`
}

// NewRateLimiter creates a new rate limiter with the specified configuration
func NewRateLimiter(enabled bool, connectionsPerMinute int, windowSize time.Duration, gossipEnabled bool, gossipInterval time.Duration, peerURLs []string, logger *zap.Logger) *RateLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}

	ctx, cancel := context.WithCancel(context.Background())

	rl := &RateLimiter{
		enabled:              enabled,
		connectionsPerMinute: connectionsPerMinute,
		windowSize:           windowSize,
		ipWindows:            make(map[string]*connectionWindow),
		gossipEnabled:        gossipEnabled,
		gossipInterval:       gossipInterval,
		logger:               logger,
		peerURLs:             peerURLs,
		ctx:                  ctx,
		cancel:               cancel,
	}

	// Start gossip loop if enabled
	if gossipEnabled && len(peerURLs) > 0 {
		go rl.gossipLoop()
	}

	// Start cleanup loop
	go rl.cleanupLoop()

	return rl
}

// CheckRateLimit checks if an IP has exceeded the rate limit
// Returns nil if allowed, error if rate limit exceeded
func (rl *RateLimiter) CheckRateLimit(remoteAddr string) error {
	if !rl.enabled || rl.connectionsPerMinute == 0 {
		return nil // Rate limiting disabled
	}

	// Extract IP from address
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Get or create window for this IP
	window, exists := rl.ipWindows[host]
	if !exists {
		window = &connectionWindow{
			timestamps:  make([]time.Time, 0),
			lastCleanup: now,
		}
		rl.ipWindows[host] = window
	}

	// Remove timestamps outside the sliding window
	cutoff := now.Add(-rl.windowSize)
	validTimestamps := make([]time.Time, 0, len(window.timestamps))
	for _, ts := range window.timestamps {
		if ts.After(cutoff) {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	window.timestamps = validTimestamps
	window.lastCleanup = now

	// Check if adding this connection would exceed the limit
	if len(window.timestamps) >= rl.connectionsPerMinute {
		rl.logger.Warn("Rate limit exceeded",
			zap.String("ip", host),
			zap.Int("connections_in_window", len(window.timestamps)),
			zap.Int("limit", rl.connectionsPerMinute),
			zap.Duration("window", rl.windowSize))
		return fmt.Errorf("rate limit exceeded: %d connections in %s (limit: %d)", len(window.timestamps), rl.windowSize, rl.connectionsPerMinute)
	}

	// Record this connection attempt
	window.timestamps = append(window.timestamps, now)

	return nil
}

// GetStats returns current rate limit statistics for an IP
func (rl *RateLimiter) GetStats(remoteAddr string) (int, int) {
	if !rl.enabled {
		return 0, 0
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	rl.mu.RLock()
	defer rl.mu.RUnlock()

	window, exists := rl.ipWindows[host]
	if !exists {
		return 0, rl.connectionsPerMinute
	}

	// Count valid connections in current window
	now := time.Now()
	cutoff := now.Add(-rl.windowSize)
	count := 0
	for _, ts := range window.timestamps {
		if ts.After(cutoff) {
			count++
		}
	}

	return count, rl.connectionsPerMinute
}

// cleanupLoop periodically removes old entries to prevent memory leaks
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.ctx.Done():
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes IPs that haven't had connections in 2x the window size
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cleanupThreshold := now.Add(-rl.windowSize * 2)

	for ip, window := range rl.ipWindows {
		if window.lastCleanup.Before(cleanupThreshold) && len(window.timestamps) == 0 {
			delete(rl.ipWindows, ip)
		}
	}
}

// gossipLoop periodically shares rate limit data with peer servers
func (rl *RateLimiter) gossipLoop() {
	ticker := time.NewTicker(rl.gossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.ctx.Done():
			return
		case <-ticker.C:
			rl.gossip()
		}
	}
}

// gossip shares current rate limit data with all peers
func (rl *RateLimiter) gossip() {
	// Get current data
	data := rl.exportData()
	if len(data) == 0 {
		return
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		rl.logger.Error("Failed to marshal rate limit gossip data", zap.Error(err))
		return
	}

	// Send to all peers
	client := &http.Client{Timeout: 5 * time.Second}
	for _, peerURL := range rl.peerURLs {
		go func(url string) {
			resp, err := client.Post(url+"/api/rate-limit-gossip", "application/json", bytes.NewReader(jsonData))
			if err != nil {
				rl.logger.Debug("Failed to gossip rate limit data to peer", zap.String("peer", url), zap.Error(err))
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				rl.logger.Debug("Peer rejected rate limit gossip", zap.String("peer", url), zap.Int("status", resp.StatusCode))
			}
		}(peerURL)
	}

	rl.logger.Debug("Gossiped rate limit data to peers", zap.Int("ips", len(data)), zap.Int("peers", len(rl.peerURLs)))
}

// exportData exports current rate limit state for gossip
func (rl *RateLimiter) exportData() []RateLimitData {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-rl.windowSize)
	data := make([]RateLimitData, 0)

	for ip, window := range rl.ipWindows {
		// Count valid connections
		count := 0
		var oldestTimestamp time.Time
		for _, ts := range window.timestamps {
			if ts.After(cutoff) {
				count++
				if oldestTimestamp.IsZero() || ts.Before(oldestTimestamp) {
					oldestTimestamp = ts
				}
			}
		}

		if count > 0 {
			data = append(data, RateLimitData{
				IP:          ip,
				Connections: count,
				WindowStart: oldestTimestamp,
				ReportedAt:  now,
			})
		}
	}

	return data
}

// MergeGossipData merges rate limit data received from peers
func (rl *RateLimiter) MergeGossipData(data []RateLimitData) {
	if !rl.gossipEnabled {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.windowSize)

	for _, item := range data {
		// Ignore stale data (older than window)
		if item.ReportedAt.Before(cutoff) {
			continue
		}

		// Get or create window
		window, exists := rl.ipWindows[item.IP]
		if !exists {
			window = &connectionWindow{
				timestamps:  make([]time.Time, 0),
				lastCleanup: now,
			}
			rl.ipWindows[item.IP] = window
		}

		// Merge peer data by adding synthetic timestamps
		// This is a simplified approach - in production you might want more sophisticated merging
		for i := 0; i < item.Connections; i++ {
			// Spread connections evenly across the window
			ts := item.WindowStart.Add(time.Duration(i) * (rl.windowSize / time.Duration(item.Connections)))
			if ts.After(cutoff) {
				window.timestamps = append(window.timestamps, ts)
			}
		}
	}

	rl.logger.Debug("Merged gossip data from peers", zap.Int("items", len(data)))
}

// HandleGossip is an HTTP handler that receives and merges rate limit gossip data from peers
func (rl *RateLimiter) HandleGossip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !rl.gossipEnabled {
		http.Error(w, "Gossip not enabled", http.StatusNotImplemented)
		return
	}

	var data []RateLimitData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		rl.logger.Warn("Failed to decode gossip data", zap.Error(err))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	rl.MergeGossipData(data)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "success",
		"merged":  len(data),
		"message": "Gossip data merged successfully",
	})
}

// Stop gracefully shuts down the rate limiter
func (rl *RateLimiter) Stop() {
	rl.cancel()
}
