package smtp

import (
	"context"
	"fmt"
	"log/slog"
	"migadu/mizu/pkg/concurrency"
	"migadu/mizu/pkg/health"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const shardCount = 32

type connectionShard struct {
	mu            sync.RWMutex
	ipConnections map[string]int
	connTimes     map[string][]time.Time
}

// ConnectionTracker tracks active connections globally and per-IP to enforce limits.
// It provides thread-safe operations for tracking and releasing connections.
//
// It uses sharding (32 shards) to minimize lock contention, especially during
// high-concurrency scenarios or when GetStats() is called frequently (e.g. by gossip).
//
// When MaxConnectionDuration is set (> 0), a background reaper goroutine
// periodically scans for connections that have exceeded the maximum duration
// and force-releases them. This prevents connection count leaks.
type ConnectionTracker struct {
	name                  string        // Health checker name (empty = default "connection_tracker")
	maxConnections        int           // Maximum total concurrent connections (0 = unlimited)
	maxConnectionsPerIP   int           // Maximum concurrent connections per IP (0 = unlimited)
	maxConnectionDuration time.Duration // Maximum duration before a connection is force-released (0 = disabled)
	totalConnections      atomic.Int32  // Current total number of connections (atomic for lock-free reads)

	// Sharded storage for connections to reduce lock contention
	shards [shardCount]*connectionShard

	// Reaper stats (how many connections were force-released)
	totalReaped atomic.Uint64

	// Lifecycle
	logger *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewConnectionTracker creates a new connection tracker with the specified limits.
// Set maxConnections to 0 for unlimited total connections.
// Set maxConnectionsPerIP to 0 for unlimited connections per IP.
// Set maxConnectionDuration to 0 to disable the reaper (not recommended in production).
func NewConnectionTracker(maxConnections, maxConnectionsPerIP int, maxConnectionDuration time.Duration, logger *slog.Logger) *ConnectionTracker {
	ctx, cancel := context.WithCancel(context.Background())
	ct := &ConnectionTracker{
		maxConnections:        maxConnections,
		maxConnectionsPerIP:   maxConnectionsPerIP,
		maxConnectionDuration: maxConnectionDuration,
		logger:                logger,
		ctx:                   ctx,
		cancel:                cancel,
	}

	// Initialize shards
	for i := 0; i < shardCount; i++ {
		ct.shards[i] = &connectionShard{
			ipConnections: make(map[string]int),
			connTimes:     make(map[string][]time.Time),
		}
	}

	return ct
}

// getShard returns the shard for a given IP address
func (ct *ConnectionTracker) getShard(ip string) *connectionShard {
	var h uint32 = 5381
	for i := 0; i < len(ip); i++ {
		h = ((h << 5) + h) + uint32(ip[i])
	}
	return ct.shards[h%shardCount]
}

// Start begins the background reaper goroutine (if MaxConnectionDuration > 0).
// Must be called after NewConnectionTracker. Safe to call even if reaping is disabled.
func (ct *ConnectionTracker) Start() {
	if ct.maxConnectionDuration <= 0 {
		return
	}

	// Reap interval: half the max duration, capped at 30s for faster leak detection
	interval := ct.maxConnectionDuration / 2
	if interval < 1*time.Millisecond {
		interval = 1 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}

	ct.wg.Add(1)
	concurrency.SafeGoWithWg(ct.logger, "connection-tracker-reaper", &ct.wg, func() {
		ct.reaperLoop(interval)
	})

	if ct.logger != nil {
		ct.logger.Info("Connection reaper started",
			"max_connection_duration", ct.maxConnectionDuration,
			"reap_interval", interval)
	}
}

// Stop gracefully shuts down the reaper goroutine.
func (ct *ConnectionTracker) Stop() {
	ct.cancel()
	ct.wg.Wait()
}

// reaperLoop periodically checks for and force-releases stale connections.
func (ct *ConnectionTracker) reaperLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ct.ctx.Done():
			return
		case <-ticker.C:
			reaped := ct.reapExpired()
			if reaped > 0 && ct.logger != nil {
				ct.logger.Warn("Reaped stale connections",
					"reaped", reaped,
					"max_duration", ct.maxConnectionDuration,
					"total_connections", ct.getTotalConnections())
			}
		}
	}
}

// reapExpired scans all tracked connections and force-releases any that exceed
// MaxConnectionDuration. Returns the number of connections reaped.
func (ct *ConnectionTracker) reapExpired() int {
	if ct.maxConnectionDuration <= 0 {
		return 0
	}

	now := time.Now()
	cutoff := now.Add(-ct.maxConnectionDuration)
	reaped := 0

	// Iterate over all shards
	for i := 0; i < shardCount; i++ {
		shard := ct.shards[i]
		shard.mu.Lock()

		for ip, times := range shard.connTimes {
			// Times are sorted oldest-first, so count expired from the front
			expired := 0
			for _, t := range times {
				if t.Before(cutoff) {
					expired++
				} else {
					break // All subsequent are newer
				}
			}

			if expired > 0 {
				if ct.logger != nil {
					ct.logger.Warn("Force-releasing stale connections for IP",
						"ip", ip,
						"expired", expired,
						"remaining", len(times)-expired,
						"oldest", times[0].Format(time.RFC3339))
				}

				// Remove expired timestamps
				shard.connTimes[ip] = times[expired:]

				// Decrement counters
				shard.ipConnections[ip] -= expired
				ct.totalConnections.Add(-int32(expired))
				reaped += expired

				// Prevent negative counts (defensive)
				if shard.ipConnections[ip] < 0 {
					shard.ipConnections[ip] = 0
				}
				if ct.totalConnections.Load() < 0 {
					ct.totalConnections.Store(0)
				}

				// Clean up empty entries
				if shard.ipConnections[ip] <= 0 {
					delete(shard.ipConnections, ip)
					delete(shard.connTimes, ip)
				}
			}
		}
		shard.mu.Unlock()
	}

	ct.totalReaped.Add(uint64(reaped))
	return reaped
}

// getTotalConnections returns total connections (for logging, lock-free).
func (ct *ConnectionTracker) getTotalConnections() int {
	return int(ct.totalConnections.Load())
}

// TryAcquire attempts to acquire a connection slot for the given remote address.
// Returns nil on success, or an error if limits are exceeded.
func (ct *ConnectionTracker) TryAcquire(remoteAddr string) error {
	// Extract IP from address (format: "IP:port" or "[IPv6]:port")
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// If no port, assume it's just an IP
		host = remoteAddr
	}

	// Check global connection limit (lock-free check first)
	if ct.maxConnections > 0 && int(ct.totalConnections.Load()) >= ct.maxConnections {
		return fmt.Errorf("maximum total connections reached (%d)", ct.maxConnections)
	}

	shard := ct.getShard(host)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Double-check global limit under lock?
	// No, totalConnections is atomic, but there's a race where we might go slightly over.
	// That's usually acceptable for performance.
	// If strict enforcement is needed, we'd need a global lock or CAS loop, but that defeats sharding.
	// We'll stick to the atomic check above.

	// Check per-IP connection limit
	if ct.maxConnectionsPerIP > 0 {
		currentIPConns := shard.ipConnections[host]
		if currentIPConns >= ct.maxConnectionsPerIP {
			return fmt.Errorf("maximum connections per IP reached (%d)", ct.maxConnectionsPerIP)
		}
	}

	// Acquire connection slot
	ct.totalConnections.Add(1)
	shard.ipConnections[host]++
	shard.connTimes[host] = append(shard.connTimes[host], time.Now())

	return nil
}

// Release releases a connection slot for the given remote address.
// This should be called when a connection is closed, typically in a defer statement.
func (ct *ConnectionTracker) Release(remoteAddr string) {
	// Extract IP from address
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	shard := ct.getShard(host)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Release connection slot
	if ct.totalConnections.Load() > 0 {
		ct.totalConnections.Add(-1)
	}

	if count, exists := shard.ipConnections[host]; exists && count > 0 {
		shard.ipConnections[host]--

		// Remove the oldest timestamp for this IP (FIFO)
		if times := shard.connTimes[host]; len(times) > 0 {
			shard.connTimes[host] = times[1:]
		}

		// Clean up map entries if count reaches zero to prevent memory leak
		if shard.ipConnections[host] == 0 {
			delete(shard.ipConnections, host)
			delete(shard.connTimes, host)
		}
	}
}

// GetStats returns current connection statistics.
// Returns total connections, number of unique IPs, and per-IP breakdown.
// Note: This iterates over all shards, so it's more expensive than GetCountForIP.
func (ct *ConnectionTracker) GetStats() (total int, uniqueIPs int, perIP map[string]int) {
	// Estimate size to avoid reallocations
	estimatedSize := int(ct.totalConnections.Load())
	if estimatedSize < 0 {
		estimatedSize = 0
	}
	perIP = make(map[string]int, estimatedSize)

	// Iterate over all shards
	for i := 0; i < shardCount; i++ {
		shard := ct.shards[i]
		shard.mu.RLock()
		for ip, count := range shard.ipConnections {
			if count > 0 {
				perIP[ip] = count
			}
		}
		shard.mu.RUnlock()
	}

	return int(ct.totalConnections.Load()), len(perIP), perIP
}

// GetTotalCount returns the current total connection count (lock-free).
// This is safe to call from hot paths without causing RWMutex contention.
func (ct *ConnectionTracker) GetTotalCount() int {
	return int(ct.totalConnections.Load())
}

// GetCountForIP returns the current connection count for a specific IP.
// This is more efficient than GetStats() when you only need one IP's count.
func (ct *ConnectionTracker) GetCountForIP(ip string) int {
	shard := ct.getShard(ip)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	return shard.ipConnections[ip]
}

// GetLimits returns the configured connection limits.
func (ct *ConnectionTracker) GetLimits() (maxTotal, maxPerIP int) {
	// These are immutable after creation, so no lock needed
	return ct.maxConnections, ct.maxConnectionsPerIP
}

// SetName sets a custom health checker name (e.g. "connection_tracker:mx-primary").
func (ct *ConnectionTracker) SetName(name string) {
	ct.name = name
}

// Name returns the component name for health checks.
func (ct *ConnectionTracker) Name() string {
	if ct.name != "" {
		return ct.name
	}
	return "connection_tracker"
}

// CheckHealth returns the health status of the connection tracker.
func (ct *ConnectionTracker) CheckHealth() health.ComponentStatus {
	total, uniqueIPs, perIP := ct.GetStats()
	maxTotal, maxPerIP := ct.GetLimits()

	// Calculate utilization percentages
	var totalUtilization float64
	if maxTotal > 0 {
		totalUtilization = float64(total) / float64(maxTotal) * 100
	}

	// Find the IP with the highest connection count
	var maxIPConns int
	var maxIPAddr string
	for ip, count := range perIP {
		if count > maxIPConns {
			maxIPConns = count
			maxIPAddr = ip
		}
	}

	var perIPUtilization float64
	if maxPerIP > 0 && maxIPConns > 0 {
		perIPUtilization = float64(maxIPConns) / float64(maxPerIP) * 100
	}

	status := "healthy"
	if maxTotal > 0 && totalUtilization >= 90 {
		status = "degraded"
	}
	if maxTotal > 0 && totalUtilization >= 100 {
		status = "unhealthy"
	}

	details := map[string]interface{}{
		"total_connections":          total,
		"unique_ips":                 uniqueIPs,
		"max_total_connections":      maxTotal,
		"max_connections_per_ip":     maxPerIP,
		"max_connection_duration":    ct.maxConnectionDuration.String(),
		"total_utilization_pct":      fmt.Sprintf("%.1f", totalUtilization),
		"highest_ip_connections":     maxIPConns,
		"highest_ip_address":         maxIPAddr,
		"highest_ip_utilization_pct": fmt.Sprintf("%.1f", perIPUtilization),
		"total_reaped":               ct.totalReaped.Load(),
	}

	return health.ComponentStatus{
		Status:  status,
		Details: details,
	}
}
