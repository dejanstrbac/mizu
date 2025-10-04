package smtp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestDistributedTracker_LocalAndGlobalLimits tests that both local and global limits are enforced
func TestDistributedTracker_LocalAndGlobalLimits(t *testing.T) {
	logger := zap.NewNop()

	// Local tracker: max 5 per IP, max 10 total
	local := NewConnectionTracker(10, 5)

	// Distributed tracker: global max 8 per IP across cluster
	dt := NewDistributedTracker(
		local,
		nil, // No S3
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour, // Don't gossip during test
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 8,
		},
		logger,
	)

	// Acquire 5 connections locally (local limit)
	for i := 0; i < 5; i++ {
		if err := dt.TryAcquire("192.168.1.100:5000"); err != nil {
			t.Fatalf("Expected to acquire connection %d, got error: %v", i+1, err)
		}
	}

	// 6th connection should fail (local limit reached)
	if err := dt.TryAcquire("192.168.1.100:5000"); err == nil {
		t.Fatal("Expected error when exceeding local per-IP limit, got nil")
	}

	// Simulate peer having 4 connections from same IP
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"192.168.1.100": 4,
		},
		TotalCount: 4,
		UpdatedAt:  time.Now(),
	}
	dt.peerMu.Unlock()

	// Now we have 5 local + 4 peer = 9 total for this IP
	// Global limit is 8, so new connection should fail even though local has room
	if err := dt.TryAcquire("192.168.1.100:5001"); err == nil {
		t.Fatal("Expected error when exceeding global per-IP limit, got nil")
	}

	// Different IP should still work
	if err := dt.TryAcquire("192.168.1.200:5000"); err != nil {
		t.Fatalf("Expected to acquire connection from different IP, got error: %v", err)
	}
}

// TestDistributedTracker_StalePerrsIgnored tests that stale peer data is ignored
func TestDistributedTracker_StalePeersIgnored(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 10)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 15,
		},
		logger,
	)

	// Acquire 5 connections locally
	for i := 0; i < 5; i++ {
		if err := dt.TryAcquire("192.168.1.100:5000"); err != nil {
			t.Fatalf("Failed to acquire connection %d: %v", i+1, err)
		}
	}

	// Add stale peer data (31 seconds old)
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now().Add(-31 * time.Second),
		Connections: map[string]int{
			"192.168.1.100": 100, // This should be ignored
		},
		TotalCount: 100,
		UpdatedAt:  time.Now().Add(-31 * time.Second),
	}
	dt.peerMu.Unlock()

	// Should succeed because stale peer data is ignored
	// Local: 5, Stale peer: 100 (ignored), Global limit: 15
	if err := dt.TryAcquire("192.168.1.100:5001"); err != nil {
		t.Fatalf("Expected to acquire connection (stale peer should be ignored), got error: %v", err)
	}
}

// TestDistributedTracker_HTTPHandler tests the gossip HTTP endpoint
func TestDistributedTracker_HTTPHandler(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 10)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 20,
		},
		logger,
	)

	// Acquire some connections
	dt.TryAcquire("192.168.1.100:5000")
	dt.TryAcquire("192.168.1.100:5001")
	dt.TryAcquire("192.168.1.101:5000")

	// Create a peer snapshot to send
	peerSnapshot := PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"192.168.1.200": 5,
		},
		TotalCount: 5,
	}

	// Create test request with JSON body
	w := httptest.NewRecorder()
	handler := dt.HTTPHandler()

	peerJSON, _ := json.Marshal(peerSnapshot)
	req := httptest.NewRequest("POST", "/api/connections/sync", bytes.NewReader(peerJSON))
	req.Header.Set("Content-Type", "application/json")

	handler(w, req)

	// Should return 200
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Response should be our snapshot
	var response PeerConnectionState
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Hostname != "server1" {
		t.Errorf("Expected hostname 'server1', got '%s'", response.Hostname)
	}

	if response.TotalCount != 3 {
		t.Errorf("Expected total_count 3, got %d", response.TotalCount)
	}

	// Verify peer data was stored
	dt.peerMu.RLock()
	stored, ok := dt.peerConnections["server2"]
	dt.peerMu.RUnlock()

	if !ok {
		t.Fatal("Expected peer 'server2' to be stored")
	}

	if stored.Connections["192.168.1.200"] != 5 {
		t.Errorf("Expected peer to have 5 connections from 192.168.1.200, got %d",
			stored.Connections["192.168.1.200"])
	}
}

// TestDistributedTracker_GetGlobalStats tests global statistics aggregation
func TestDistributedTracker_GetGlobalStats(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 10)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 20,
		},
		logger,
	)

	// Acquire 5 connections from 2 IPs locally
	for i := 0; i < 3; i++ {
		dt.TryAcquire("192.168.1.100:5000")
	}
	for i := 0; i < 2; i++ {
		dt.TryAcquire("192.168.1.101:5000")
	}

	// Add peer data
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"192.168.1.100": 2, // Same IP as local
			"192.168.1.102": 4, // Different IP
		},
		TotalCount: 6,
		UpdatedAt:  time.Now(),
	}
	dt.peerMu.Unlock()

	localTotal, globalTotal, uniqueIPs, topIPs := dt.GetGlobalStats()

	// Local should have 5 connections
	if localTotal != 5 {
		t.Errorf("Expected local total 5, got %d", localTotal)
	}

	// Global should be 5 (local) + 6 (peer) = 11
	if globalTotal != 11 {
		t.Errorf("Expected global total 11, got %d", globalTotal)
	}

	// Unique IPs: 192.168.1.100, 192.168.1.101, 192.168.1.102 = 3
	if uniqueIPs != 3 {
		t.Errorf("Expected 3 unique IPs, got %d", uniqueIPs)
	}

	// Check aggregated counts per IP
	if topIPs["192.168.1.100"] != 5 { // 3 local + 2 peer
		t.Errorf("Expected 192.168.1.100 to have 5 connections, got %d", topIPs["192.168.1.100"])
	}
	if topIPs["192.168.1.101"] != 2 {
		t.Errorf("Expected 192.168.1.101 to have 2 connections, got %d", topIPs["192.168.1.101"])
	}
	if topIPs["192.168.1.102"] != 4 {
		t.Errorf("Expected 192.168.1.102 to have 4 connections, got %d", topIPs["192.168.1.102"])
	}
}

// TestDistributedTracker_ConcurrentAccess tests thread safety
func TestDistributedTracker_ConcurrentAccess(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(1000, 50)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 100,
		},
		logger,
	)

	var wg sync.WaitGroup
	concurrency := 50
	operationsPerGoroutine := 100

	// Concurrent acquire/release
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			addr := "192.168.1.100:5000"
			for j := 0; j < operationsPerGoroutine; j++ {
				if err := dt.TryAcquire(addr); err == nil {
					dt.Release(addr)
				}
			}
		}(i)
	}

	// Concurrent peer updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				dt.peerMu.Lock()
				dt.peerConnections["peer"+string(rune(id))] = &PeerConnectionState{
					Hostname:    "peer" + string(rune(id)),
					Timestamp:   time.Now(),
					Connections: map[string]int{"192.168.1.200": j},
					TotalCount:  j,
					UpdatedAt:   time.Now(),
				}
				dt.peerMu.Unlock()
			}
		}(i)
	}

	// Concurrent stats reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				dt.GetGlobalStats()
			}
		}()
	}

	wg.Wait()

	// Should have no connections left (all released)
	localTotal, _, _, _ := dt.GetGlobalStats()
	if localTotal != 0 {
		t.Errorf("Expected 0 local connections after cleanup, got %d", localTotal)
	}
}

// TestDistributedTracker_ReleaseRollback tests rollback on global limit failure
func TestDistributedTracker_ReleaseRollback(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 10)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 8,
		},
		logger,
	)

	// Acquire 5 locally
	for i := 0; i < 5; i++ {
		if err := dt.TryAcquire("192.168.1.100:5000"); err != nil {
			t.Fatalf("Failed to acquire connection %d: %v", i+1, err)
		}
	}

	// Simulate peer with 5 connections (total = 10, exceeds global limit of 8)
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"192.168.1.100": 5,
		},
		TotalCount: 5,
		UpdatedAt:  time.Now(),
	}
	dt.peerMu.Unlock()

	// Try to acquire 6th connection - should fail global limit
	if err := dt.TryAcquire("192.168.1.100:5001"); err == nil {
		t.Fatal("Expected error due to global limit, got nil")
	}

	// Verify local count is still 5 (rollback worked)
	localTotal, _, _, topIPs := dt.GetGlobalStats()
	if localTotal != 5 {
		t.Errorf("Expected local total to remain 5 after rollback, got %d", localTotal)
	}

	if topIPs["192.168.1.100"] != 10 { // 5 local + 5 peer
		t.Errorf("Expected 192.168.1.100 to have 10 total connections, got %d", topIPs["192.168.1.100"])
	}
}

// TestDistributedTracker_HealthCheck tests the health checker interface
func TestDistributedTracker_HealthCheck(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 10)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{"https://server2:8080", "https://server3:8080"},
			GossipInterval: 5 * time.Second,
			S3SyncInterval: 30 * time.Second,
			GlobalMaxPerIP: 20,
		},
		logger,
	)

	// Initially no peers active - should be degraded
	status := dt.CheckHealth()
	if status.Status != "degraded" {
		t.Errorf("Expected status 'degraded' with no active peers, got '%s'", status.Status)
	}

	// Add active peer
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:   "server2",
		Timestamp:  time.Now(),
		TotalCount: 10,
		UpdatedAt:  time.Now(),
	}
	dt.peerMu.Unlock()

	status = dt.CheckHealth()
	if status.Status != "healthy" {
		t.Errorf("Expected status 'healthy' with active peer, got '%s'", status.Status)
	}

	details, ok := status.Details.(map[string]any)
	if !ok {
		t.Fatal("Expected details to be map[string]any")
	}
	if details["configured_peers"] != 2 {
		t.Errorf("Expected 2 configured peers, got %v", details["configured_peers"])
	}
	if details["active_peers"] != 1 {
		t.Errorf("Expected 1 active peer, got %v", details["active_peers"])
	}
}

// TestDistributedTracker_NoGlobalLimit tests behavior when global limit is 0 (disabled)
func TestDistributedTracker_NoGlobalLimit(t *testing.T) {
	logger := zap.NewNop()

	local := NewConnectionTracker(100, 5)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "server1",
			Peers:          []string{},
			GossipInterval: 1 * time.Hour,
			S3SyncInterval: 1 * time.Hour,
			GlobalMaxPerIP: 0, // Disabled
		},
		logger,
	)

	// Acquire 5 connections (local limit)
	for i := 0; i < 5; i++ {
		if err := dt.TryAcquire("192.168.1.100:5000"); err != nil {
			t.Fatalf("Failed to acquire connection %d: %v", i+1, err)
		}
	}

	// Add peer with huge number of connections
	dt.peerMu.Lock()
	dt.peerConnections["server2"] = &PeerConnectionState{
		Hostname:  "server2",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"192.168.1.100": 1000,
		},
		TotalCount: 1000,
		UpdatedAt:  time.Now(),
	}
	dt.peerMu.Unlock()

	// 6th connection should fail due to LOCAL limit only (global is disabled)
	if err := dt.TryAcquire("192.168.1.100:5001"); err == nil {
		t.Fatal("Expected error due to local limit, got nil")
	} else if !strings.Contains(err.Error(), "maximum connections per IP") {
		t.Errorf("Expected local limit error, got: %v", err)
	}
}
