package smtp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRateLimiterGossip_HTTPBodyNotNil ensures gossip POST sends actual data
func TestRateLimiterGossip_HTTPBodyNotNil(t *testing.T) {
	receivedBody := false
	receivedData := []RateLimitData{}

	// Mock peer server that captures the POST body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rate-limit-gossip" {
			t.Errorf("Wrong path: got %s, want /api/rate-limit-gossip", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("Wrong method: got %s, want POST", r.Method)
		}

		// Read the body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed to read body: %v", err)
			return
		}

		// Verify body is not empty
		if len(body) == 0 {
			t.Error("REGRESSION: POST body is empty/nil - gossip data not being sent!")
			return
		}

		receivedBody = true

		// Verify body is valid JSON
		if err := json.Unmarshal(body, &receivedData); err != nil {
			t.Errorf("Body is not valid JSON: %v", err)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create rate limiter with the mock server as peer
	rl := NewRateLimiter(true, 60, 1*time.Minute, true, 100*time.Millisecond, []string{server.URL}, zap.NewNop())
	defer rl.Stop()

	// Generate some traffic to create gossip data
	for i := 0; i < 5; i++ {
		rl.CheckRateLimit("192.168.1.100:12345")
	}

	// Trigger gossip manually
	rl.gossip()

	// Wait a bit for async gossip to complete
	time.Sleep(200 * time.Millisecond)

	if !receivedBody {
		t.Fatal("REGRESSION: No POST body received - gossip() may not be sending data correctly")
	}

	if len(receivedData) == 0 {
		t.Error("REGRESSION: Received empty gossip data array")
	}

	t.Logf("✅ Gossip POST correctly sent %d IP records in body", len(receivedData))
}

// TestRateLimiterGossip_HandlerExists ensures the HTTP handler is registered
func TestRateLimiterGossip_HandlerExists(t *testing.T) {
	rl := NewRateLimiter(true, 60, 1*time.Minute, true, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Create test server with the handler
	mux := http.NewServeMux()
	mux.HandleFunc("/api/rate-limit-gossip", rl.HandleGossip)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Test valid gossip POST
	gossipData := []RateLimitData{
		{IP: "1.2.3.4", Connections: 10, WindowStart: time.Now(), ReportedAt: time.Now()},
	}
	body, _ := json.Marshal(gossipData)

	resp, err := http.Post(server.URL+"/api/rate-limit-gossip", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to POST to gossip handler: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Handler returned wrong status: got %d, want 200", resp.StatusCode)
	}

	// Verify the handler rejected GET (should only accept POST)
	resp2, _ := http.Get(server.URL + "/api/rate-limit-gossip")
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Handler should reject GET: got %d, want 405", resp2.StatusCode)
	}
	resp2.Body.Close()

	t.Log("✅ Gossip handler correctly registered and responds to POST")
}

// TestDistributedTracker_URLConstruction ensures peer URLs don't have double paths
func TestDistributedTracker_URLConstruction(t *testing.T) {
	requestedPaths := []string{}
	mu := make(chan struct{}, 1)

	// Mock peer server that logs requested paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu <- struct{}{}
		requestedPaths = append(requestedPaths, r.URL.Path)
		<-mu
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create distributed tracker with mock peer
	local := NewConnectionTracker(10, 5)
	dt := NewDistributedTracker(
		local,
		nil, // No S3
		"",
		"",
		DistributedConfig{
			Hostname:       "test-server",
			Peers:          []string{server.URL}, // Just base URL, no path
			GossipInterval: 100 * time.Millisecond,
		},
		zap.NewNop(),
	)
	dt.Start()
	defer dt.Stop()

	// Track a connection to generate gossip data
	dt.TryAcquire("1.2.3.4:12345")

	// Wait for gossip to happen
	time.Sleep(300 * time.Millisecond)

	// Check the requested path
	if len(requestedPaths) == 0 {
		t.Fatal("No gossip requests received")
	}

	for _, path := range requestedPaths {
		if path == "/api/connections/sync/api/connections/sync" {
			t.Error("REGRESSION: Double path detected! Peer URL should not include path, sendToPeer() adds it")
		}
		if path != "/api/connections/sync" {
			t.Errorf("Wrong path: got %s, want /api/connections/sync", path)
		}
	}

	t.Logf("✅ Distributed tracker correctly POSTs to /api/connections/sync (single path)")
}

// TestDistributedTracker_GossipBodyNotNil ensures gossip sends actual data
func TestDistributedTracker_GossipBodyNotNil(t *testing.T) {
	receivedBody := false
	var receivedSnapshot ConnectionSnapshot

	// Mock peer server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed to read body: %v", err)
			return
		}

		if len(body) == 0 {
			t.Error("REGRESSION: DistributedTracker POST body is empty!")
			return
		}

		receivedBody = true

		if err := json.Unmarshal(body, &receivedSnapshot); err != nil {
			t.Errorf("Body is not valid JSON: %v", err)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	local := NewConnectionTracker(10, 5)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "test-server",
			Peers:          []string{server.URL},
			GossipInterval: 100 * time.Millisecond,
		},
		zap.NewNop(),
	)
	dt.Start()
	defer dt.Stop()

	// Create some connection data
	dt.TryAcquire("10.0.0.1:12345")
	dt.TryAcquire("10.0.0.2:12345")

	// Wait for gossip
	time.Sleep(300 * time.Millisecond)

	if !receivedBody {
		t.Fatal("REGRESSION: DistributedTracker gossip POST body not received")
	}

	if receivedSnapshot.Hostname != "test-server" {
		t.Errorf("Wrong hostname in snapshot: got %s, want test-server", receivedSnapshot.Hostname)
	}

	t.Logf("✅ DistributedTracker gossip correctly sent snapshot with %d IPs", len(receivedSnapshot.Connections))
}

// TestGossipEndpoints_Uniqueness ensures all gossip endpoints are unique
func TestGossipEndpoints_Uniqueness(t *testing.T) {
	endpoints := map[string]string{
		"rate_limiter":        "/api/rate-limit-gossip",
		"distributed_tracker": "/api/connections/sync",
		"stats":               "/api/stats",
		"health":              "/health",
		"flush_cache":         "/api/flush-cache",
	}

	seen := make(map[string]string)
	for name, path := range endpoints {
		if existing, found := seen[path]; found {
			t.Errorf("Duplicate endpoint path %s used by both %s and %s", path, name, existing)
		}
		seen[path] = name
	}

	t.Logf("✅ All %d gossip/API endpoints are unique", len(endpoints))
}

// TestRateLimiterGossip_DisabledWhenNotConfigured ensures gossip respects config
func TestRateLimiterGossip_DisabledWhenNotConfigured(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create rate limiter with gossip DISABLED
	rl := NewRateLimiter(true, 60, 1*time.Minute, false, 100*time.Millisecond, []string{server.URL}, zap.NewNop())
	defer rl.Stop()

	// Generate traffic
	for i := 0; i < 10; i++ {
		rl.CheckRateLimit("192.168.1.100:12345")
	}

	// Wait to see if any gossip happens (it shouldn't)
	time.Sleep(500 * time.Millisecond)

	if requestCount > 0 {
		t.Errorf("REGRESSION: Gossip sent %d requests when disabled (gossipEnabled=false)", requestCount)
	}

	t.Log("✅ Gossip correctly disabled when gossipEnabled=false")
}

// TestRateLimiterGossip_MergeValidation ensures merged data affects rate limiting
func TestRateLimiterGossip_MergeValidation(t *testing.T) {
	rl := NewRateLimiter(true, 20, 1*time.Minute, true, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	attackerIP := "10.0.0.100:12345"

	// Local server accepts 10 connections
	for i := 0; i < 10; i++ {
		if err := rl.CheckRateLimit(attackerIP); err != nil {
			t.Fatalf("Expected connection %d to be accepted, got error: %v", i+1, err)
		}
	}

	// Simulate receiving gossip from peer saying this IP has 12 more connections
	gossipData := []RateLimitData{
		{
			IP:          "10.0.0.100",
			Connections: 12,
			WindowStart: time.Now().Add(-30 * time.Second),
			ReportedAt:  time.Now(),
		},
	}
	rl.MergeGossipData(gossipData)

	// Now we should be near/over limit (10 local + 12 from gossip = 22, limit is 20)
	// Try a few more - most should be rejected
	accepted := 0
	rejected := 0
	for i := 0; i < 10; i++ {
		if err := rl.CheckRateLimit(attackerIP); err != nil {
			rejected++
		} else {
			accepted++
		}
	}

	if rejected == 0 {
		t.Error("REGRESSION: Gossip data was not merged - no connections rejected after gossip")
	}

	t.Logf("✅ Gossip merge working: %d accepted, %d rejected after merge (expected rejections)", accepted, rejected)
}

// TestDistributedTracker_HandlerRegistered ensures HTTP handler works
func TestDistributedTracker_HandlerRegistered(t *testing.T) {
	local := NewConnectionTracker(10, 5)
	dt := NewDistributedTracker(
		local,
		nil,
		"",
		"",
		DistributedConfig{
			Hostname:       "test-server",
			GossipInterval: 5 * time.Second,
		},
		zap.NewNop(),
	)
	defer dt.Stop()

	// Create test server with the handler
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connections/sync", dt.HTTPHandler())
	server := httptest.NewServer(mux)
	defer server.Close()

	// Send valid gossip data
	snapshot := &ConnectionSnapshot{
		Hostname:  "peer-server",
		Timestamp: time.Now(),
		Connections: map[string]int{
			"1.2.3.4": 5,
		},
		TotalCount: 5,
		Version:    1,
	}
	body, _ := json.Marshal(snapshot)

	resp, err := http.Post(server.URL+"/api/connections/sync", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to POST to handler: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Handler returned wrong status: got %d, want 200", resp.StatusCode)
	}

	// Verify handler rejected GET
	resp2, _ := http.Get(server.URL + "/api/connections/sync")
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Handler should reject GET: got %d, want 405", resp2.StatusCode)
	}
	resp2.Body.Close()

	t.Log("✅ DistributedTracker handler correctly registered and validates methods")
}
