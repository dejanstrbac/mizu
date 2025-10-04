package smtp

import (
	"fmt"
	"sync"
	"testing"
)

func TestConnectionTracker_GlobalLimit(t *testing.T) {
	tracker := NewConnectionTracker(3, 0) // Max 3 total, unlimited per IP

	// Acquire 3 connections from different IPs
	if err := tracker.TryAcquire("192.168.1.1:1234"); err != nil {
		t.Errorf("Expected to acquire connection 1, got error: %v", err)
	}
	if err := tracker.TryAcquire("192.168.1.2:1234"); err != nil {
		t.Errorf("Expected to acquire connection 2, got error: %v", err)
	}
	if err := tracker.TryAcquire("192.168.1.3:1234"); err != nil {
		t.Errorf("Expected to acquire connection 3, got error: %v", err)
	}

	// 4th connection should fail
	if err := tracker.TryAcquire("192.168.1.4:1234"); err == nil {
		t.Error("Expected error when exceeding global limit, got nil")
	}

	// Release one connection
	tracker.Release("192.168.1.1:1234")

	// Now should be able to acquire again
	if err := tracker.TryAcquire("192.168.1.4:1234"); err != nil {
		t.Errorf("Expected to acquire connection after release, got error: %v", err)
	}

	// Check stats
	total, uniqueIPs, _ := tracker.GetStats()
	if total != 3 {
		t.Errorf("Expected 3 total connections, got %d", total)
	}
	if uniqueIPs != 3 {
		t.Errorf("Expected 3 unique IPs, got %d", uniqueIPs)
	}
}

func TestConnectionTracker_PerIPLimit(t *testing.T) {
	tracker := NewConnectionTracker(0, 2) // Unlimited total, max 2 per IP

	// Acquire 2 connections from same IP
	if err := tracker.TryAcquire("192.168.1.1:1234"); err != nil {
		t.Errorf("Expected to acquire connection 1, got error: %v", err)
	}
	if err := tracker.TryAcquire("192.168.1.1:5678"); err != nil {
		t.Errorf("Expected to acquire connection 2, got error: %v", err)
	}

	// 3rd connection from same IP should fail
	if err := tracker.TryAcquire("192.168.1.1:9999"); err == nil {
		t.Error("Expected error when exceeding per-IP limit, got nil")
	}

	// Different IP should work
	if err := tracker.TryAcquire("192.168.1.2:1234"); err != nil {
		t.Errorf("Expected to acquire connection from different IP, got error: %v", err)
	}

	// Release one connection from first IP
	tracker.Release("192.168.1.1:1234")

	// Now should be able to acquire from that IP again
	if err := tracker.TryAcquire("192.168.1.1:9999"); err != nil {
		t.Errorf("Expected to acquire connection after release, got error: %v", err)
	}

	// Check stats
	total, uniqueIPs, perIP := tracker.GetStats()
	if total != 3 {
		t.Errorf("Expected 3 total connections, got %d", total)
	}
	if uniqueIPs != 2 {
		t.Errorf("Expected 2 unique IPs, got %d", uniqueIPs)
	}
	if perIP["192.168.1.1"] != 2 {
		t.Errorf("Expected 2 connections from 192.168.1.1, got %d", perIP["192.168.1.1"])
	}
}

func TestConnectionTracker_BothLimits(t *testing.T) {
	tracker := NewConnectionTracker(5, 2) // Max 5 total, max 2 per IP

	// Acquire 2 connections from IP1
	tracker.TryAcquire("192.168.1.1:1234")
	tracker.TryAcquire("192.168.1.1:5678")

	// 3rd from IP1 should fail (per-IP limit)
	if err := tracker.TryAcquire("192.168.1.1:9999"); err == nil {
		t.Error("Expected error for per-IP limit, got nil")
	}

	// Acquire 2 from IP2
	tracker.TryAcquire("192.168.1.2:1234")
	tracker.TryAcquire("192.168.1.2:5678")

	// Acquire 1 from IP3 (total = 5)
	tracker.TryAcquire("192.168.1.3:1234")

	// Next connection should fail (global limit)
	if err := tracker.TryAcquire("192.168.1.4:1234"); err == nil {
		t.Error("Expected error for global limit, got nil")
	}
}

func TestConnectionTracker_UnlimitedMode(t *testing.T) {
	tracker := NewConnectionTracker(0, 0) // Unlimited

	// Should accept many connections
	for i := 0; i < 1000; i++ {
		addr := fmt.Sprintf("192.168.1.1:%d", 1000+i)
		if err := tracker.TryAcquire(addr); err != nil {
			t.Errorf("Expected unlimited mode to accept connection, got error: %v", err)
		}
	}

	total, _, _ := tracker.GetStats()
	if total != 1000 {
		t.Errorf("Expected 1000 connections, got %d", total)
	}
}

func TestConnectionTracker_IPv6(t *testing.T) {
	tracker := NewConnectionTracker(0, 2) // Max 2 per IP

	// Test with IPv6 addresses
	if err := tracker.TryAcquire("[2001:db8::1]:1234"); err != nil {
		t.Errorf("Expected to acquire IPv6 connection 1, got error: %v", err)
	}
	if err := tracker.TryAcquire("[2001:db8::1]:5678"); err != nil {
		t.Errorf("Expected to acquire IPv6 connection 2, got error: %v", err)
	}

	// 3rd connection from same IPv6 should fail
	if err := tracker.TryAcquire("[2001:db8::1]:9999"); err == nil {
		t.Error("Expected error when exceeding per-IP limit for IPv6, got nil")
	}
}

func TestConnectionTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewConnectionTracker(100, 10)
	var wg sync.WaitGroup

	// Simulate concurrent connections and releases
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			addr := fmt.Sprintf("192.168.1.%d:1234", id%10) // 10 different IPs
			if err := tracker.TryAcquire(addr); err != nil {
				// Expected if limit reached
				return
			}
			// Simulate some work
			tracker.Release(addr)
		}(i)
	}

	wg.Wait()

	// After all releases, should have 0 connections
	total, uniqueIPs, _ := tracker.GetStats()
	if total != 0 {
		t.Errorf("Expected 0 connections after all releases, got %d", total)
	}
	if uniqueIPs != 0 {
		t.Errorf("Expected 0 unique IPs after cleanup, got %d", uniqueIPs)
	}
}

func TestConnectionTracker_ReleaseNonExistent(t *testing.T) {
	tracker := NewConnectionTracker(10, 5)

	// Release without acquire should not panic or cause issues
	tracker.Release("192.168.1.1:1234")

	total, uniqueIPs, _ := tracker.GetStats()
	if total != 0 {
		t.Errorf("Expected 0 connections, got %d", total)
	}
	if uniqueIPs != 0 {
		t.Errorf("Expected 0 unique IPs, got %d", uniqueIPs)
	}
}

func TestConnectionTracker_GetLimits(t *testing.T) {
	tracker := NewConnectionTracker(50, 5)

	maxTotal, maxPerIP := tracker.GetLimits()
	if maxTotal != 50 {
		t.Errorf("Expected max total 50, got %d", maxTotal)
	}
	if maxPerIP != 5 {
		t.Errorf("Expected max per IP 5, got %d", maxPerIP)
	}
}

func TestConnectionTracker_MemoryLeak(t *testing.T) {
	tracker := NewConnectionTracker(1000, 10)

	// Acquire and release many connections
	for i := 0; i < 100; i++ {
		addr := fmt.Sprintf("192.168.1.%d:1234", i)
		tracker.TryAcquire(addr)
		tracker.Release(addr)
	}

	// Map should be empty after all releases
	_, uniqueIPs, _ := tracker.GetStats()
	if uniqueIPs != 0 {
		t.Errorf("Memory leak detected: expected 0 IPs in map, got %d", uniqueIPs)
	}
}
