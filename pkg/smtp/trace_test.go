package smtp

import (
	"regexp"
	"testing"
)

func TestGenerateTraceID(t *testing.T) {
	// Test basic generation
	traceID := generateTraceID()

	// Should be 16 hex characters
	matched, err := regexp.MatchString("^[0-9a-f]{16}$", traceID)
	if err != nil {
		t.Fatalf("Regex error: %v", err)
	}
	if !matched {
		t.Errorf("Invalid trace ID format: %s (expected 16 hex chars)", traceID)
	}

	t.Logf("Generated trace ID: %s", traceID)
}

func TestGenerateTraceIDUniqueness(t *testing.T) {
	// Generate 1000 trace IDs and ensure they're all unique
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := generateTraceID()
		if ids[id] {
			t.Errorf("Duplicate trace ID generated: %s", id)
		}
		ids[id] = true
	}
	t.Logf("Generated 1000 unique trace IDs")
}
