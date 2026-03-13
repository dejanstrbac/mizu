package stats

import (
	"sync"
	"time"
)

// Event weight constants
const (
	// Positive events
	WeightHamDelivery = 1

	// Negative events
	WeightJunkMessage      = 1
	WeightInvalidRecipient = 2
	WeightSPFFailure       = 3
	WeightDMARCFailure     = 3
	WeightDNSBLHit         = 5
	WeightSpoofingAttempt  = 10

	// Minimum data threshold — Laplace smoothing constant that prevents
	// wild reputation swings when only a few events have been observed.
	// Higher values make the system more conservative (slower to condemn
	// or trust a new IP).
	MinDataThreshold = 20

	// Reputation threshold for denial
	ReputationDenyThreshold = -0.2
)

// IPEntry tracks reputation for an IP address
type IPEntry struct {
	FirstSeen      time.Time
	LastSeen       time.Time
	LastNegativeAt time.Time           // Time of last negative event (for decay calculation)
	Connections    int64               // Total connections from this IP
	Positive       int64               // Ham messages delivered
	Negative       int64               // Junk + failed recipients + spoofing + DMARC failures
	IsDenied       bool                // Set true if no rDNS
	Servers        map[string]struct{} // Config server names that saw this IP
	mu             sync.RWMutex
}

// AddPositive increments the positive (ham) counter.
// The reputation score is computed from the ratio of Positive to Negative
// using Laplace smoothing, so there is no need for cross-counter adjustment.
func (e *IPEntry) AddPositive(weight int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.Positive += weight
	e.LastSeen = time.Now()
}

// AddNegative increments the negative (junk/failure) counter.
// The reputation score is computed from the ratio of Positive to Negative
// using Laplace smoothing, so there is no need for cross-counter adjustment.
func (e *IPEntry) AddNegative(weight int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	e.Negative += weight
	e.LastNegativeAt = now
	e.LastSeen = now
}

// IncrementConnections increments the connection count
func (e *IPEntry) IncrementConnections() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Connections++
	e.LastSeen = time.Now()
}

// GetReputation returns the reputation score from -1 (worst) to +1 (best)
func (e *IPEntry) GetReputation() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.getReputationLocked()
}

// getReputationLocked computes the reputation score without acquiring locks.
// Caller must hold e.mu.RLock() or e.mu.Lock().
func (e *IPEntry) getReputationLocked() float64 {
	// Apply time decay to the negative score based on when the last negative
	// event occurred (not LastSeen). This ensures that an actively-sending IP
	// whose negative events are old sees its negative score decay, even if new
	// positive events (connections, ham deliveries) keep refreshing LastSeen.
	// The decay is linear, reaching zero after 24 hours since the last negative event.
	decayFactor := 1.0
	if !e.LastNegativeAt.IsZero() {
		hoursSinceLastNegative := time.Since(e.LastNegativeAt).Hours()
		decayFactor = 1.0 - (hoursSinceLastNegative / 24.0)
		if decayFactor < 0 {
			decayFactor = 0
		}
	} else {
		// No negative events recorded — decay everything
		decayFactor = 0
	}
	decayedNegative := float64(e.Negative) * decayFactor

	// Laplace smoothing with MinDataThreshold to prevent wild swings on small data
	smoothing := float64(MinDataThreshold)
	total := float64(e.Positive) + decayedNegative + smoothing

	// Return reputation score: -1 (worst) to +1 (best)
	return (float64(e.Positive) - decayedNegative) / total
}

// ShouldDeny returns true if the IP should be denied based on reputation
func (e *IPEntry) ShouldDeny() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.IsDenied { // No rDNS
		return true
	}

	// Deny if reputation < -0.2
	// Use lock-free internal method to avoid recursive RLock deadlock
	return e.getReputationLocked() < ReputationDenyThreshold
}

// IsExpired checks if the entry is older than the retention duration
func (e *IPEntry) IsExpired(retention time.Duration) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return time.Since(e.LastSeen) > retention
}

// GetConnections returns the connection count (thread-safe)
func (e *IPEntry) GetConnections() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Connections
}

// GetPositive returns the positive score (thread-safe)
func (e *IPEntry) GetPositive() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Positive
}

// GetNegative returns the negative score (thread-safe)
func (e *IPEntry) GetNegative() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Negative
}

// GetIsDenied returns whether the IP is denied (thread-safe)
func (e *IPEntry) GetIsDenied() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.IsDenied
}

// Export types for JSON serialization

// IPExport is the JSON-serializable version of IPEntry
type IPExport struct {
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	LastNegativeAt time.Time `json:"last_negative_at,omitempty"`
	Connections    int64     `json:"connections"`
	Positive       int64     `json:"positive"`
	Negative       int64     `json:"negative"`
	IsDenied       bool      `json:"is_denied"`
	Servers        []string  `json:"servers,omitempty"`
}

// ExportSummary provides per-server message counts in exports
type ExportSummary struct {
	TotalMessages    int64 `json:"total_messages"`
	AcceptedMessages int64 `json:"accepted_messages"`
	RejectedMessages int64 `json:"rejected_messages"`
	JunkMessages     int64 `json:"junk_messages"`
}

// StatsExport is the complete stats export structure
type StatsExport struct {
	Version   string               `json:"version"`
	Hostname  string               `json:"hostname"`
	Timestamp time.Time            `json:"timestamp"`
	IPs       map[string]*IPExport `json:"ips"`
	Summary   *ExportSummary       `json:"summary,omitempty"` // Per-server message counts
}

// ToExport converts IPEntry to IPExport
func (e *IPEntry) ToExport() *IPExport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var servers []string
	for s := range e.Servers {
		servers = append(servers, s)
	}

	return &IPExport{
		FirstSeen:      e.FirstSeen,
		LastSeen:       e.LastSeen,
		LastNegativeAt: e.LastNegativeAt,
		Connections:    e.Connections,
		Positive:       e.Positive,
		Negative:       e.Negative,
		IsDenied:       e.IsDenied,
		Servers:        servers,
	}
}

// FromExport updates IPEntry from IPExport (used in merging)
func (e *IPEntry) FromExport(export *IPExport) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.FirstSeen = export.FirstSeen
	e.LastSeen = export.LastSeen
	e.LastNegativeAt = export.LastNegativeAt
	e.Connections = export.Connections
	e.Positive = export.Positive
	e.Negative = export.Negative
	e.IsDenied = export.IsDenied
	if len(export.Servers) > 0 {
		e.Servers = make(map[string]struct{}, len(export.Servers))
		for _, s := range export.Servers {
			e.Servers[s] = struct{}{}
		}
	}
}

// ServerDomainStats tracks per-domain message counts within a single server
type ServerDomainStats struct {
	Messages int64 `json:"messages"`
	Accepted int64 `json:"accepted"`
	Rejected int64 `json:"rejected"`
	Junk     int64 `json:"junk"`
}

// ServerSummary provides per-server message statistics
type ServerSummary struct {
	Hostname          string                        `json:"hostname"`
	TotalMessages     int64                         `json:"total_messages"`
	AcceptedMessages  int64                         `json:"accepted_messages"`
	RejectedMessages  int64                         `json:"rejected_messages"`
	JunkMessages      int64                         `json:"junk_messages"`
	ActiveConnections int64                         `json:"active_connections"` // Active SMTP connections for this server
	LastUpdated       time.Time                     `json:"last_updated"`
	SenderDomains     map[string]*ServerDomainStats `json:"sender_domains,omitempty"`    // Sender (FROM) domains
	RecipientDomains  map[string]*ServerDomainStats `json:"recipient_domains,omitempty"` // Recipient (TO) domains
}

// StatsSnapshot is a complete snapshot of stats for API responses
type StatsSnapshot struct {
	IPs     map[string]*IPExport      `json:"ips"`
	Summary StatsSummary              `json:"summary"`
	Servers map[string]*ServerSummary `json:"servers,omitempty"` // Per-server breakdown
}

// StatsSummary provides aggregated statistics
type StatsSummary struct {
	TotalIPs          int   `json:"total_ips"`
	BlockedIPs        int   `json:"blocked_ips"`
	ActiveConnections int64 `json:"active_connections"` // Current active SMTP connections across all servers
	TotalMessages     int64 `json:"total_messages"`
	AcceptedMessages  int64 `json:"accepted_messages"`
	RejectedMessages  int64 `json:"rejected_messages"`
	JunkMessages      int64 `json:"junk_messages"`
	EventsProcessed   int64 `json:"events_processed"`
	EventsDropped     int64 `json:"events_dropped"`
}
