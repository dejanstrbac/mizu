package stats

import (
	"math"
	"testing"
	"time"
)

const float64EqualityThreshold = 1e-3

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= float64EqualityThreshold
}

func TestIPEntry_GetReputation_TimeDecay(t *testing.T) {
	tests := []struct {
		name          string
		positive      int64
		negative      int64
		lastSeen      time.Time
		connections   int64
		expectedScore float64
	}{
		{
			name:          "No decay - recent event",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			connections:   MinDataThreshold,
			expectedScore: 0.0, // decayedNegative = 10. (10 - 10) / (10 + 10) = 0
		},
		{
			name:          "Half decay - 12 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now().Add(-12 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.333, // decayedNegative = 10 * 0.5 = 5. (10 - 5) / (10 + 5) = 5 / 15
		},
		{
			name:          "Full decay - 24 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now().Add(-24 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 1.0, // decayedNegative = 0. (10 - 0) / (10 + 0) = 1
		},
		{
			name:          "Full decay - more than 24 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now().Add(-48 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 1.0, // decayedNegative = 0. (10 - 0) / (10 + 0) = 1
		},
		{
			name:          "No positive score, half decay",
			positive:      0,
			negative:      10,
			lastSeen:      time.Now().Add(-12 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: -1.0, // decayedNegative = 5. (0 - 5) / (0 + 5) = -1
		},
		{
			name:          "No positive score, full decay",
			positive:      0,
			negative:      10,
			lastSeen:      time.Now().Add(-24 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.0, // decayedNegative = 0. total = 0.
		},
		{
			name:          "Not enough data",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			connections:   MinDataThreshold - 1,
			expectedScore: 0.0, // Should return neutral score
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &IPEntry{
				Positive:    tt.positive,
				Negative:    tt.negative,
				LastSeen:    tt.lastSeen,
				Connections: tt.connections,
			}

			score := entry.GetReputation()
			if !almostEqual(score, tt.expectedScore) {
				t.Errorf("IPEntry.GetReputation() = %v, want %v", score, tt.expectedScore)
			}
		})
	}
}

func TestDomainEntry_GetReputation_TimeDecay(t *testing.T) {
	tests := []struct {
		name          string
		positive      int64
		negative      int64
		lastSeen      time.Time
		messages      int64
		expectedScore float64
	}{
		{
			name:          "No decay - recent event",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			messages:      MinDataThreshold,
			expectedScore: 0.0, // (10 - 10) / (10 + 10) = 0
		},
		{
			name:          "Half decay - 12 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now().Add(-12 * time.Hour),
			messages:      MinDataThreshold,
			expectedScore: 0.333, // decayedNegative = 5. (10 - 5) / (10 + 5) = 5 / 15
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &DomainEntry{
				Positive: tt.positive,
				Negative: tt.negative,
				LastSeen: tt.lastSeen,
				Messages: tt.messages,
			}

			score := entry.GetReputation()
			if !almostEqual(score, tt.expectedScore) {
				t.Errorf("DomainEntry.GetReputation() = %v, want %v", score, tt.expectedScore)
			}
		})
	}
}
