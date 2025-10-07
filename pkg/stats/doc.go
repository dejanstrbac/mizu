// Package stats provides real-time reputation tracking for IPs and domains.
//
// # Overview
//
// The stats package tracks email sending behavior to identify good and bad actors:
//
//	Success Rate: Percentage of successful deliveries
//	Failure Rate: Percentage of failed deliveries
//	DMARC Failures: Count of DMARC validation failures
//	Reputation Score: Calculated from success/failure ratio
//
// This information is used for:
//
//	Rate limiting: Bad actors get lower limits
//	Blocking: IPs/domains with very poor reputation are blocked
//	Monitoring: Track sending patterns and anomalies
//
// # Architecture
//
// The stats manager uses an event-driven architecture:
//
//  1. SMTP sessions emit events (success, failure, DMARC fail, etc.)
//  2. Events go into a lock-free ring buffer
//  3. Worker goroutines process events asynchronously
//  4. Stats are updated in memory (LRU cache)
//  5. Periodically synced to S3 for cluster-wide sharing
//
// This design provides:
//
//	High throughput: No locks in hot path
//	Low latency: SMTP sessions don't block on stats updates
//	Scalability: Multiple workers process events concurrently
//
// # Event Types
//
// The package defines several event types:
//
//	EventEmailSuccess: Email delivered successfully
//	EventEmailFailure: Email delivery failed
//	EventDMARCFailure: Email failed DMARC validation
//	EventConnectionRefused: Connection rejected
//	EventRateLimitExceeded: Rate limit hit
//
// Events contain:
//
//	IP address
//	Domain
//	Timestamp
//	Event-specific data
//
// # Reputation Scoring
//
// Reputation scores are calculated using a weighted average:
//
//	Score = (successes - failures) / (successes + failures)
//	Range: -1.0 (all failures) to +1.0 (all successes)
//
// The calculation uses time-weighted decay so recent behavior
// matters more than old behavior.
//
// # LRU Eviction
//
// To prevent unbounded memory growth, the manager uses LRU eviction:
//
//	Max IP entries: 100,000 (configurable)
//	Max domain entries: 50,000 (configurable)
//
// Least recently used entries are evicted when limits are exceeded.
// Before eviction, entries are written to S3 so data isn't lost.
//
// # Distributed Sync
//
// When cluster mode is enabled, stats are synced across nodes:
//
//  1. Each node maintains local stats in memory
//  2. Periodically (default: 60s), nodes export stats to S3
//  3. Other nodes import stats from S3 and merge with local data
//  4. Vector clocks ensure correct merging of concurrent updates
//
// This provides eventual consistency across the cluster without
// requiring real-time synchronization.
//
// # Configuration
//
//	[stats]
//	enabled = true
//	retention_seconds = 86400  # 24 hours
//	sync_enabled = true  # Requires cluster mode
//	sync_interval_seconds = 60
//	max_ip_entries = 100000
//	max_domain_entries = 50000
//
// # Metrics
//
// Stats metrics are exposed via Prometheus:
//
//	mizu_stats_ip_entries_total: Current number of IP entries
//	mizu_stats_domain_entries_total: Current number of domain entries
//	mizu_stats_events_processed_total: Total events processed
//	mizu_stats_events_dropped_total: Events dropped due to full buffer
//
// # Example Usage
//
//	// Create stats manager
//	mgr := stats.New(
//	    cfg.Stats.MaxIPEntries,
//	    cfg.Stats.MaxDomainEntries,
//	    cfg.Stats.RetentionSeconds,
//	    storageBackend,
//	    logger,
//	)
//
//	// Start manager
//	mgr.Start()
//	defer mgr.Stop()
//
//	// Record event
//	mgr.RecordEvent(stats.Event{
//	    Type:      stats.EventEmailSuccess,
//	    IP:        "192.0.2.1",
//	    Domain:    "example.com",
//	    Timestamp: time.Now(),
//	})
//
//	// Get reputation
//	ipStats := mgr.GetIPStats("192.0.2.1")
//	if ipStats.ReputationScore < -0.5 {
//	    // Block bad actor
//	}
//
// # Thread Safety
//
// The Manager type is fully thread-safe and can be used concurrently
// from multiple goroutines. Internal synchronization uses lock-free
// data structures where possible.
//
// # Performance
//
// Typical performance on modern hardware:
//
//	Event throughput: 100,000+ events/sec
//	Memory per entry: ~200 bytes
//	Lookup latency: <1 microsecond
//	S3 export time: 1-5 seconds for 100k entries
package stats
