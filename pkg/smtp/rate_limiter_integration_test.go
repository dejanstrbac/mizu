package smtp

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRateLimitE2E_RapidConnections tests that rate limiting blocks rapid connection attempts
func TestRateLimitE2E_RapidConnections(t *testing.T) {
	limit := 10
	window := 1 * time.Minute
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ip := "10.0.0.100:5000"
	accepted := 0
	rejected := 0

	t.Logf("=== Simulating rapid connection attempts ===")
	t.Logf("Rate limit: %d connections per %v", limit, window)

	// Attempt many rapid connections
	for i := 1; i <= 50; i++ {
		err := rl.CheckRateLimit(ip)
		if err == nil {
			accepted++
			t.Logf("✓ Connection %2d: ACCEPTED", i)
		} else {
			rejected++
			if rejected == 1 {
				// Log first rejection
				t.Logf("✗ Connection %2d: REJECTED (%v)", i, err)
			}
		}

		// Simulate rapid attempts (no delay)
	}

	t.Logf("\n=== Results ===")
	t.Logf("Attempted: 50")
	t.Logf("Accepted:  %d", accepted)
	t.Logf("Rejected:  %d", rejected)

	if accepted != limit {
		t.Errorf("Expected exactly %d accepted, got %d", limit, accepted)
	}

	if rejected != 40 {
		t.Errorf("Expected 40 rejected, got %d", rejected)
	}

	t.Logf("✅ Rate limiter correctly blocked rapid connection attempts")
}

// TestRateLimitE2E_LegitimateTraffic tests that normal traffic is not blocked
func TestRateLimitE2E_LegitimateTraffic(t *testing.T) {
	limit := 60
	window := 1 * time.Minute
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	t.Logf("=== Simulating legitimate email traffic ===")
	t.Logf("Rate limit: %d connections per %v", limit, window)

	// Simulate 5 different email clients connecting at reasonable rate
	clients := []string{
		"203.0.113.10:1234",
		"203.0.113.11:1234",
		"203.0.113.12:1234",
		"203.0.113.13:1234",
		"203.0.113.14:1234",
	}

	accepted := 0
	rejected := 0

	// Each client connects 10 times with small delay (normal behavior)
	for round := 1; round <= 10; round++ {
		for _, client := range clients {
			err := rl.CheckRateLimit(client)
			if err == nil {
				accepted++
			} else {
				rejected++
				t.Logf("✗ Client %s connection %d: REJECTED (unexpected)", client, round)
			}
		}
		time.Sleep(10 * time.Millisecond) // Small delay between rounds
	}

	t.Logf("\n=== Results ===")
	t.Logf("Total connections: %d", accepted+rejected)
	t.Logf("Accepted: %d", accepted)
	t.Logf("Rejected: %d", rejected)

	if rejected > 0 {
		t.Errorf("Legitimate traffic should not be rejected, but got %d rejections", rejected)
	}

	t.Logf("✅ Legitimate traffic from multiple clients was not rate limited")
}

// TestRateLimitE2E_AttackAndRecovery tests attack detection and recovery
func TestRateLimitE2E_AttackAndRecovery(t *testing.T) {
	limit := 5
	window := 500 * time.Millisecond
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	attacker := "1.2.3.4:666"

	t.Logf("=== Phase 1: Attack ===")
	t.Logf("Attacker attempts %d rapid connections (limit: %d)", 20, limit)

	accepted := 0
	rejected := 0

	for i := 1; i <= 20; i++ {
		err := rl.CheckRateLimit(attacker)
		if err == nil {
			accepted++
		} else {
			rejected++
		}
	}

	t.Logf("Attack results: %d accepted, %d rejected", accepted, rejected)

	if accepted != limit {
		t.Errorf("Should accept exactly %d, got %d", limit, accepted)
	}

	t.Logf("=== Phase 2: Recovery ===")
	t.Logf("Waiting %v for rate limit window to expire...", window)
	time.Sleep(window + 100*time.Millisecond)

	// After window expires, attacker should be able to connect again (up to limit)
	recovered := 0
	for i := 1; i <= limit; i++ {
		err := rl.CheckRateLimit(attacker)
		if err == nil {
			recovered++
		}
	}

	t.Logf("After recovery: %d connections allowed", recovered)

	if recovered != limit {
		t.Errorf("After window expired, should allow %d connections, got %d", limit, recovered)
	}

	t.Logf("✅ Attack was blocked and system recovered after window expiration")
}

// TestRateLimitE2E_DistributedScenario simulates multiple servers with gossip
func TestRateLimitE2E_DistributedScenario(t *testing.T) {
	// Create two HTTP servers to simulate distributed peers
	logger := zap.NewNop()

	// Server 1: Will receive connections and gossip to Server 2
	rl1 := NewRateLimiter(true, 60, 1*time.Minute, true, 100*time.Millisecond, nil, logger)
	defer rl1.Stop()

	// Start HTTP server for server1
	mux1 := http.NewServeMux()
	mux1.HandleFunc("/api/rate-limit-gossip", rl1.HandleGossip)
	server1 := &http.Server{Addr: "127.0.0.1:18081", Handler: mux1}
	go server1.ListenAndServe()
	defer server1.Close()

	// Server 2: Will receive gossip from Server 1
	rl2 := NewRateLimiter(true, 60, 1*time.Minute, true, 100*time.Millisecond, []string{"http://127.0.0.1:18081"}, logger)
	defer rl2.Stop()

	// Start HTTP server for server2
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/api/rate-limit-gossip", rl2.HandleGossip)
	server2 := &http.Server{Addr: "127.0.0.1:18082", Handler: mux2}
	go server2.ListenAndServe()
	defer server2.Close()

	// Update server1 to gossip to server2
	rl1.peerURLs = []string{"http://127.0.0.1:18082"}

	time.Sleep(100 * time.Millisecond) // Let servers start

	t.Log("=== Distributed Gossip Test ===")
	t.Log("Server1 limit: 60/min, Server2 limit: 60/min")
	t.Log("Attacker connects to Server1, gossip should propagate to Server2")

	attackerIP := "192.168.1.100:12345"

	// Phase 1: Attacker makes 40 connections to Server1
	accepted1 := 0
	for i := 0; i < 40; i++ {
		if err := rl1.CheckRateLimit(attackerIP); err == nil {
			accepted1++
		}
	}
	t.Logf("Server1 accepted %d/40 connections from attacker", accepted1)

	// Trigger gossip manually and wait for propagation
	rl1.gossip()
	time.Sleep(200 * time.Millisecond)

	// Phase 2: Attacker tries 30 more connections to Server2
	// Should only allow 20 because Server2 knows about 40 from gossip
	accepted2 := 0
	rejected2 := 0
	for i := 0; i < 30; i++ {
		if err := rl2.CheckRateLimit(attackerIP); err == nil {
			accepted2++
		} else {
			rejected2++
		}
	}

	t.Logf("\nServer2 results after gossip:")
	t.Logf("  Accepted: %d/30", accepted2)
	t.Logf("  Rejected: %d/30", rejected2)
	t.Logf("  Total across cluster: %d/60 (limit)", accepted1+accepted2)

	// Verify: Total accepted across both servers should be ~60
	totalAccepted := accepted1 + accepted2
	if totalAccepted > 65 {
		t.Errorf("Gossip failed: total accepted (%d) exceeds limit (60) by too much", totalAccepted)
	}

	// Server2 should have rejected some connections
	if rejected2 == 0 {
		t.Errorf("Server2 did not reject any connections - gossip may not be working")
	}

	t.Logf("\n✅ Distributed gossip synchronized rate limits across cluster")
}

// TestRateLimitE2E_MixedAttackAndLegitimate tests that attacks don't affect other IPs
func TestRateLimitE2E_MixedAttackAndLegitimate(t *testing.T) {
	limit := 10
	window := 1 * time.Minute
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	attacker := "1.2.3.4:666"
	legitimate := []string{
		"203.0.113.10:1234",
		"203.0.113.11:1234",
		"203.0.113.12:1234",
	}

	t.Logf("=== Simulating attack + legitimate traffic ===")

	// Attacker floods the server
	t.Logf("Attacker flooding from %s", attacker)
	attackBlocked := 0
	for i := 0; i < 100; i++ {
		if err := rl.CheckRateLimit(attacker); err != nil {
			attackBlocked++
		}
	}
	t.Logf("Attacker: 100 attempts, %d blocked", attackBlocked)

	// Meanwhile, legitimate users should still be able to connect
	t.Logf("\nLegitimate users connecting...")
	legitRejected := 0
	for _, client := range legitimate {
		for i := 0; i < 5; i++ {
			if err := rl.CheckRateLimit(client); err != nil {
				legitRejected++
				t.Errorf("✗ Legitimate client %s was blocked: %v", client, err)
			}
		}
	}

	if legitRejected > 0 {
		t.Errorf("Legitimate clients should not be affected by attack, but %d were rejected", legitRejected)
	}

	t.Logf("✅ Legitimate traffic unaffected by attack on different IP")
}

// TestRateLimitE2E_RealisticEmailServer simulates a realistic email server scenario
func TestRateLimitE2E_RealisticEmailServer(t *testing.T) {
	// Realistic settings: 60 connections per minute per IP
	limit := 60
	window := 1 * time.Minute
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	t.Logf("=== Realistic Email Server Scenario ===")
	t.Logf("Rate limit: %d connections/IP/%v", limit, window)

	scenarios := []struct {
		name        string
		ip          string
		connections int
		delay       time.Duration
		expectBlock bool
	}{
		{
			name:        "Normal mail server (1 email every 2 seconds)",
			ip:          "203.0.113.10:25",
			connections: 30,
			delay:       2 * time.Second / 30, // Spread 30 over simulated time
			expectBlock: false,
		},
		{
			name:        "Bulk sender (1 email per second)",
			ip:          "203.0.113.20:25",
			connections: 60,
			delay:       1 * time.Second / 60,
			expectBlock: false, // Just at limit
		},
		{
			name:        "Spam bot (rapid fire)",
			ip:          "1.2.3.4:12345",
			connections: 100,
			delay:       0,
			expectBlock: true,
		},
	}

	for _, scenario := range scenarios {
		t.Logf("\n--- %s ---", scenario.name)

		accepted := 0
		rejected := 0

		for i := 0; i < scenario.connections; i++ {
			err := rl.CheckRateLimit(scenario.ip)
			if err == nil {
				accepted++
			} else {
				rejected++
			}

			if scenario.delay > 0 {
				time.Sleep(scenario.delay)
			}
		}

		blocked := rejected > 0

		t.Logf("Attempts: %d, Accepted: %d, Rejected: %d", scenario.connections, accepted, rejected)

		if scenario.expectBlock && !blocked {
			t.Errorf("Expected %s to be rate limited, but wasn't", scenario.name)
		}
		if !scenario.expectBlock && blocked {
			t.Errorf("Did not expect %s to be rate limited, but was (%d rejected)", scenario.name, rejected)
		}
	}

	t.Logf("\n✅ Realistic scenarios handled correctly")
}

// Helper function to extract IP from address (copied from connection_tracker.go)
func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// BenchmarkRateLimiter_CheckRateLimit benchmarks the rate limit check performance
func BenchmarkRateLimiter_CheckRateLimit(b *testing.B) {
	rl := NewRateLimiter(true, 1000, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ip := fmt.Sprintf("192.168.1.%d:12345", i%255+1)
		rl.CheckRateLimit(ip)
	}
}

// BenchmarkRateLimiter_Concurrent benchmarks concurrent rate limit checks
func BenchmarkRateLimiter_Concurrent(b *testing.B) {
	rl := NewRateLimiter(true, 10000, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("192.168.1.%d:12345", i%255+1)
			rl.CheckRateLimit(ip)
			i++
		}
	})
}
