package smtp

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestReaper_ExpiredConnectionsAreRemoved(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create tracker with very short max duration (100ms) and fast reaper (50ms)
	tracker := NewConnectionTracker(100, 10, 100*time.Millisecond, logger)
	tracker.Start()
	defer tracker.Stop()

	// Add connections
	if err := tracker.TryAcquire("192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.TryAcquire("192.168.1.2"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.TryAcquire("192.168.1.2"); err != nil {
		t.Fatal(err)
	}

	total, _, perIP := tracker.GetStats()
	if total != 3 {
		t.Fatalf("expected 3 active connections, got %d", total)
	}
	if perIP["192.168.1.2"] != 2 {
		t.Fatalf("expected 2 connections for 192.168.1.2, got %d", perIP["192.168.1.2"])
	}

	// Wait for reaper to clean them up (connections expire after 100ms, reaper runs every maxDuration/2 = 50ms)
	time.Sleep(300 * time.Millisecond)

	total, _, perIP = tracker.GetStats()
	if total != 0 {
		t.Errorf("expected 0 active connections after reaper, got %d", total)
	}
	if perIP["192.168.1.1"] != 0 {
		t.Errorf("expected 0 connections for 192.168.1.1, got %d", perIP["192.168.1.1"])
	}
	if perIP["192.168.1.2"] != 0 {
		t.Errorf("expected 0 connections for 192.168.1.2, got %d", perIP["192.168.1.2"])
	}
}

func TestReaper_NormalReleaseStillWorks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Long max duration so reaper won't trigger during this test
	tracker := NewConnectionTracker(100, 10, 1*time.Hour, logger)
	tracker.Start()
	defer tracker.Stop()

	tracker.TryAcquire("10.0.0.1")
	tracker.TryAcquire("10.0.0.1")

	total, _, _ := tracker.GetStats()
	if total != 2 {
		t.Fatalf("expected 2, got %d", total)
	}

	tracker.Release("10.0.0.1")
	total, _, _ = tracker.GetStats()
	if total != 1 {
		t.Fatalf("expected 1, got %d", total)
	}

	tracker.Release("10.0.0.1")
	total, _, _ = tracker.GetStats()
	if total != 0 {
		t.Fatalf("expected 0, got %d", total)
	}
}

func TestReaper_DisabledWhenZeroDuration(t *testing.T) {
	// maxDuration=0 means no reaper
	tracker := NewConnectionTracker(100, 10, 0, nil)

	tracker.TryAcquire("1.2.3.4")
	time.Sleep(50 * time.Millisecond)

	total, _, _ := tracker.GetStats()
	if total != 1 {
		t.Errorf("expected 1, got %d", total)
	}
}

func TestReaper_StopIsSafe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	tracker := NewConnectionTracker(100, 10, 100*time.Millisecond, logger)
	tracker.Start()

	// Stop should be safe to call
	tracker.Stop()

	// Double-stop should be safe
	tracker.Stop()
}

func TestReaper_MixedExpiredAndFresh(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tracker := NewConnectionTracker(100, 10, 200*time.Millisecond, logger)
	tracker.Start()
	defer tracker.Stop()

	// Add old connections
	tracker.TryAcquire("192.168.1.1")
	tracker.TryAcquire("192.168.1.1")

	// Wait for them to expire
	time.Sleep(250 * time.Millisecond)

	// Add fresh connection
	tracker.TryAcquire("192.168.1.2")

	// Give reaper time to run
	time.Sleep(200 * time.Millisecond)

	// Old connections should be reaped
	_, _, perIP := tracker.GetStats()
	if perIP["192.168.1.1"] != 0 {
		t.Errorf("expected 0 for expired IP, got %d", perIP["192.168.1.1"])
	}

	// Fresh connection might or might not be expired by now depending on timing.
	// The key assertion is that old ones are gone.
	total, _, _ := tracker.GetStats()
	if total > 1 {
		t.Errorf("expected at most 1 active connection, got %d", total)
	}
}
