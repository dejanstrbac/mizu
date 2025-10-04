package smtp

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestRateLimiter_Disabled(t *testing.T) {
	rl := NewRateLimiter(false, 10, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Should allow unlimited connections when disabled
	for i := 0; i < 100; i++ {
		if err := rl.CheckRateLimit("192.168.1.1:12345"); err != nil {
			t.Errorf("Rate limiter disabled but blocked connection %d: %v", i, err)
		}
	}
}

func TestRateLimiter_UnlimitedWhenZeroLimit(t *testing.T) {
	rl := NewRateLimiter(true, 0, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Should allow unlimited connections when limit is 0
	for i := 0; i < 100; i++ {
		if err := rl.CheckRateLimit("192.168.1.1:12345"); err != nil {
			t.Errorf("Rate limiter with 0 limit blocked connection %d: %v", i, err)
		}
	}
}

func TestRateLimiter_BasicLimit(t *testing.T) {
	limit := 5
	rl := NewRateLimiter(true, limit, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ip := "192.168.1.1:12345"

	// Should allow exactly 'limit' connections
	for i := 0; i < limit; i++ {
		if err := rl.CheckRateLimit(ip); err != nil {
			t.Errorf("Should allow connection %d/%d but got error: %v", i+1, limit, err)
		}
	}

	// Should block the next connection
	if err := rl.CheckRateLimit(ip); err == nil {
		t.Errorf("Should have blocked connection %d (over limit of %d)", limit+1, limit)
	}

	// Check stats
	current, max := rl.GetStats(ip)
	if current != limit {
		t.Errorf("Expected current=%d, got %d", limit, current)
	}
	if max != limit {
		t.Errorf("Expected max=%d, got %d", limit, max)
	}
}

func TestRateLimiter_SlidingWindow(t *testing.T) {
	limit := 3
	window := 500 * time.Millisecond
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ip := "192.168.1.1:12345"

	// Use all 3 connections
	for i := 0; i < limit; i++ {
		if err := rl.CheckRateLimit(ip); err != nil {
			t.Fatalf("Connection %d should be allowed: %v", i+1, err)
		}
	}

	// 4th connection should be blocked
	if err := rl.CheckRateLimit(ip); err == nil {
		t.Error("4th connection should be blocked")
	}

	// Wait for window to expire
	time.Sleep(window + 100*time.Millisecond)

	// Should be able to connect again
	if err := rl.CheckRateLimit(ip); err != nil {
		t.Errorf("After window expired, connection should be allowed: %v", err)
	}

	t.Logf("✓ Sliding window correctly reset after %v", window)
}

func TestRateLimiter_MultipleIPs(t *testing.T) {
	limit := 3
	rl := NewRateLimiter(true, limit, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Each IP should have independent limit
	ips := []string{"192.168.1.1:1", "192.168.1.2:2", "192.168.1.3:3"}

	for _, ip := range ips {
		for i := 0; i < limit; i++ {
			if err := rl.CheckRateLimit(ip); err != nil {
				t.Errorf("IP %s connection %d should be allowed: %v", ip, i+1, err)
			}
		}

		// Each IP's (limit+1)th connection should be blocked
		if err := rl.CheckRateLimit(ip); err == nil {
			t.Errorf("IP %s connection %d should be blocked", ip, limit+1)
		}
	}

	t.Logf("✓ Multiple IPs have independent rate limits")
}

func TestRateLimiter_IPv6(t *testing.T) {
	limit := 2
	rl := NewRateLimiter(true, limit, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ipv6 := "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:25"

	for i := 0; i < limit; i++ {
		if err := rl.CheckRateLimit(ipv6); err != nil {
			t.Errorf("IPv6 connection %d should be allowed: %v", i+1, err)
		}
	}

	if err := rl.CheckRateLimit(ipv6); err == nil {
		t.Error("IPv6 connection over limit should be blocked")
	}

	t.Logf("✓ IPv6 addresses are rate limited correctly")
}

func TestRateLimiter_Cleanup(t *testing.T) {
	limit := 5
	window := 200 * time.Millisecond
	rl := NewRateLimiter(true, limit, window, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ip := "192.168.1.1:12345"

	// Make some connections
	for i := 0; i < 3; i++ {
		rl.CheckRateLimit(ip)
	}

	// Wait for window to expire + cleanup
	time.Sleep(window * 3)

	// Trigger cleanup manually
	rl.cleanup()

	// After cleanup and window expiry, should have fresh limit
	for i := 0; i < limit; i++ {
		if err := rl.CheckRateLimit(ip); err != nil {
			t.Errorf("After cleanup, connection %d should be allowed: %v", i+1, err)
		}
	}

	t.Logf("✓ Cleanup correctly removes old entries")
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	limit := 100
	rl := NewRateLimiter(true, limit, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	ip := "192.168.1.1:12345"
	concurrency := 200
	done := make(chan bool, concurrency)
	blocked := make(chan bool, concurrency)

	// Attempt many concurrent connections
	for i := 0; i < concurrency; i++ {
		go func() {
			err := rl.CheckRateLimit(ip)
			if err != nil {
				blocked <- true
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < concurrency; i++ {
		<-done
	}

	blockedCount := len(blocked)
	expectedBlocked := concurrency - limit

	// Should have blocked approximately (concurrency - limit) connections
	// Allow small margin due to race conditions
	if blockedCount < expectedBlocked-5 || blockedCount > expectedBlocked+5 {
		t.Errorf("Expected ~%d blocked, got %d", expectedBlocked, blockedCount)
	}

	t.Logf("✓ Concurrent access: %d connections attempted, %d blocked (limit: %d)", concurrency, blockedCount, limit)
}

func TestRateLimiter_ExportData(t *testing.T) {
	limit := 5
	rl := NewRateLimiter(true, limit, 1*time.Minute, false, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Create some connection history
	ips := []string{"192.168.1.1:1", "192.168.1.2:2", "192.168.1.3:3"}
	for _, ip := range ips {
		for i := 0; i < 3; i++ {
			rl.CheckRateLimit(ip)
		}
	}

	// Export data
	data := rl.exportData()

	if len(data) != len(ips) {
		t.Errorf("Expected data for %d IPs, got %d", len(ips), len(data))
	}

	for _, item := range data {
		if item.Connections != 3 {
			t.Errorf("Expected 3 connections for IP %s, got %d", item.IP, item.Connections)
		}
		if item.ReportedAt.IsZero() {
			t.Errorf("ReportedAt should not be zero for IP %s", item.IP)
		}
	}

	t.Logf("✓ Export data correctly captures %d IPs with connection counts", len(data))
}

func TestRateLimiter_MergeGossipData(t *testing.T) {
	limit := 10
	window := 1 * time.Minute
	rl := NewRateLimiter(true, limit, window, true, 5*time.Second, nil, zap.NewNop())
	defer rl.Stop()

	// Simulate receiving gossip data from peer
	gossipData := []RateLimitData{
		{
			IP:          "192.168.1.100",
			Connections: 5,
			WindowStart: time.Now().Add(-30 * time.Second),
			ReportedAt:  time.Now(),
		},
	}

	rl.MergeGossipData(gossipData)

	// After merging, this IP should have 5 connections already counted
	// So it should only allow (limit - 5) more
	allowedCount := 0
	for i := 0; i < limit; i++ {
		if err := rl.CheckRateLimit("192.168.1.100:1234"); err == nil {
			allowedCount++
		}
	}

	// Should allow approximately (limit - 5) connections
	expected := limit - 5
	if allowedCount < expected-2 || allowedCount > expected+2 {
		t.Errorf("After merging 5 connections, expected ~%d more allowed, got %d", expected, allowedCount)
	}

	t.Logf("✓ Merge gossip data: merged 5 connections, allowed %d more (limit: %d)", allowedCount, limit)
}
