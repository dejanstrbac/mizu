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
		lastNegAt     time.Time
		connections   int64
		expectedScore float64
	}{
		{
			name:          "No decay - recent event",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now(),
			connections:   MinDataThreshold,
			expectedScore: 0.0, // (10 - 10) / (10 + 10 + 20) = 0/40 = 0
		},
		{
			name:          "Half decay - 12 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now().Add(-12 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.143, // decayed=5. (10 - 5) / (10 + 5 + 20) = 5/35 ≈ 0.143
		},
		{
			name:          "Full decay - 24 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now().Add(-24 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.333, // decayed=0. (10 - 0) / (10 + 0 + 20) = 10/30 ≈ 0.333
		},
		{
			name:          "Full decay - more than 24 hours ago",
			positive:      10,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now().Add(-48 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.333, // decayed=0. (10 - 0) / (10 + 0 + 20) = 10/30 ≈ 0.333
		},
		{
			name:          "No positive score, half decay",
			positive:      0,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now().Add(-12 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: -0.2, // decayed=5. (0 - 5) / (0 + 5 + 20) = -5/25 = -0.2
		},
		{
			name:          "No positive score, full decay",
			positive:      0,
			negative:      10,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now().Add(-24 * time.Hour),
			connections:   MinDataThreshold,
			expectedScore: 0.0, // decayed=0. (0 - 0) / (0 + 0 + 20) = 0
		},
		{
			name:          "Not enough data - smoothed",
			positive:      0,
			negative:      2,
			lastSeen:      time.Now(),
			lastNegAt:     time.Now(),
			connections:   1,
			expectedScore: -0.0909, // (0 - 2) / (0 + 2 + 20) = -2/22 ≈ -0.0909
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &IPEntry{
				Positive:       tt.positive,
				Negative:       tt.negative,
				LastSeen:       tt.lastSeen,
				LastNegativeAt: tt.lastNegAt,
				Connections:    tt.connections,
			}

			score := entry.GetReputation()
			if !almostEqual(score, tt.expectedScore) {
				t.Errorf("IPEntry.GetReputation() = %v, want %v", score, tt.expectedScore)
			}
		})
	}
}

// TestIPEntry_DecayUsesLastNegativeAt verifies that the decay is based on
// LastNegativeAt (when the last negative event occurred), NOT LastSeen
// (when the IP was last active). This is critical: an IP that keeps connecting
// and delivering ham should see its old negative events decay, even though
// LastSeen stays recent.
func TestIPEntry_DecayUsesLastNegativeAt(t *testing.T) {
	// Scenario: Amazon SES sent 2 junk messages 6 hours ago, then kept
	// sending good emails. LastSeen is recent, but LastNegativeAt is 6h old.
	entry := &IPEntry{
		Positive:       10,
		Negative:       6,
		LastSeen:       time.Now(),                     // just connected
		LastNegativeAt: time.Now().Add(-6 * time.Hour), // last junk was 6h ago
		Connections:    MinDataThreshold,
	}

	// Decay factor for 6h: 1 - (6/24) = 0.75
	// Decayed negative: 6 * 0.75 = 4.5
	// Score: (10 - 4.5) / (10 + 4.5 + 20) = 5.5/34.5 ≈ 0.159
	rep := entry.GetReputation()
	if rep <= 0 {
		t.Errorf("Reputation = %f; should be positive (negative events are 6h old)", rep)
	}
	if !almostEqual(rep, 0.159) {
		t.Errorf("Reputation = %f; want ~0.159", rep)
	}

	// Compare: if decay wrongly used LastSeen (which is now), decay=1.0,
	// score would be (10-6)/(10+6+20) = 4/36 = 0.111 — still positive but lower.
	// The difference is more dramatic with larger negative values.

	// Now test with LastSeen old but LastNegativeAt recent (rare but possible
	// via cluster merge). Decay should NOT apply since negative events are fresh.
	entry2 := &IPEntry{
		Positive:       10,
		Negative:       6,
		LastSeen:       time.Now().Add(-12 * time.Hour), // hasn't been seen in 12h
		LastNegativeAt: time.Now(),                      // but negative event is fresh
		Connections:    MinDataThreshold,
	}
	rep2 := entry2.GetReputation()
	// No decay: (10-6)/(10+6+20) = 4/36 ≈ 0.111
	if !almostEqual(rep2, 0.111) {
		t.Errorf("Reputation = %f; want ~0.111 (no decay, fresh negative)", rep2)
	}
}

// TestIPEntry_DecayWithNoNegativeEvents verifies that if an IP has never
// had negative events (LastNegativeAt is zero), all accumulated negative
// score (e.g. from cluster merge) decays to zero.
func TestIPEntry_DecayWithNoNegativeEvents(t *testing.T) {
	entry := &IPEntry{
		Positive:    10,
		Negative:    5, // e.g. from cluster merge with no LastNegativeAt
		LastSeen:    time.Now(),
		Connections: MinDataThreshold,
		// LastNegativeAt is zero value — no negative events recorded locally
	}

	// With LastNegativeAt zero, decayFactor=0, so decayedNegative=0
	// Score: (10-0)/(10+0+20) = 10/30 ≈ 0.333
	rep := entry.GetReputation()
	if !almostEqual(rep, 0.333) {
		t.Errorf("Reputation = %f; want ~0.333 (zero-time negative fully decayed)", rep)
	}
}

// TestIPEntry_AmazonSESExactScenario reproduces the exact log sequence from
// the bug report: 2 junk emails 20 minutes apart, then connection 20 minutes later.
func TestIPEntry_AmazonSESExactScenario(t *testing.T) {
	now := time.Now()

	entry := &IPEntry{
		Connections: 3,   // 3 connections total
		LastSeen:    now, // latest connection just now
	}

	// 14:28 — first junk email delivered
	// 14:48 — second junk email delivered (LastNegativeAt)
	// 15:08 — new connection (LastSeen = now)
	entry.Negative = 2 * WeightJunkMessage            // 2 junk messages
	entry.LastNegativeAt = now.Add(-20 * time.Minute) // 20 min ago

	// Decay factor: 1 - (0.333h / 24h) = 0.986
	// Decayed negative: 2 * 0.986 = 1.972
	// Score: (0 - 1.972) / (0 + 1.972 + 20) = -1.972/21.972 ≈ -0.0898
	rep := entry.GetReputation()
	t.Logf("Amazon SES 20min scenario: reputation=%.4f (threshold=%.1f)", rep, ReputationDenyThreshold)

	if rep < ReputationDenyThreshold {
		t.Errorf("Reputation = %f; should NOT be below deny threshold %f", rep, ReputationDenyThreshold)
	}
	if entry.ShouldDeny() {
		t.Error("Amazon SES IP should NOT be denied after 2 junk emails 20min ago")
	}

	// Even after 1 hour, it should still not be denied
	entry.LastNegativeAt = now.Add(-1 * time.Hour)
	rep = entry.GetReputation()
	t.Logf("Amazon SES 1h scenario: reputation=%.4f", rep)
	if entry.ShouldDeny() {
		t.Error("Amazon SES IP should NOT be denied after 2 junk emails 1h ago")
	}

	// After 12 hours, negative should be half-decayed
	entry.LastNegativeAt = now.Add(-12 * time.Hour)
	rep = entry.GetReputation()
	t.Logf("Amazon SES 12h scenario: reputation=%.4f (negative half-decayed)", rep)
	// Decayed negative = 2 * 0.5 = 1, score = (0-1)/(0+1+20) = -1/21 ≈ -0.048
	if !almostEqual(rep, -0.048) {
		t.Errorf("Reputation = %f; want ~-0.048", rep)
	}

	// After 24 hours, negative fully decayed
	entry.LastNegativeAt = now.Add(-24 * time.Hour)
	rep = entry.GetReputation()
	t.Logf("Amazon SES 24h scenario: reputation=%.4f (negative fully decayed)", rep)
	if rep != 0 {
		t.Errorf("Reputation = %f; want 0 (fully decayed)", rep)
	}
}

// =============================================================================
// Per-Recipient Reputation Scoring Tests
// =============================================================================

// TestIPEntry_SingleRecipientHamDelivery verifies that a single-recipient
// delivery awards exactly WeightHamDelivery positive score.
func TestIPEntry_SingleRecipientHamDelivery(t *testing.T) {
	entry := &IPEntry{LastSeen: time.Now()}
	entry.AddPositive(WeightHamDelivery * 1) // 1 recipient

	if entry.Positive != WeightHamDelivery {
		t.Errorf("Positive = %d; want %d", entry.Positive, WeightHamDelivery)
	}
}

// TestIPEntry_MultiRecipientHamDelivery verifies that a multi-recipient
// delivery awards weight proportional to recipient count.
func TestIPEntry_MultiRecipientHamDelivery(t *testing.T) {
	entry := &IPEntry{LastSeen: time.Now()}
	recipientCount := 100
	entry.AddPositive(WeightHamDelivery * int64(recipientCount))

	expected := WeightHamDelivery * int64(recipientCount) // 100
	if entry.Positive != expected {
		t.Errorf("Positive = %d; want %d", entry.Positive, expected)
	}
}

// TestIPEntry_MailingListScenario tests the exact bug scenario:
// Google Groups sends to 100 recipients, 1 is invalid, 99 delivered.
//
// Without cross-penalty, counters accumulate independently:
// Positive=99, Negative=2 (WeightInvalidRecipient)
// Reputation = (99 - 2) / (99 + 2 + 20) = 97/121 ≈ 0.802
func TestIPEntry_MailingListScenario(t *testing.T) {
	entry := &IPEntry{
		Connections: MinDataThreshold, // Enough data for reputation calculation
		LastSeen:    time.Now(),
	}

	// 1 invalid recipient during RCPT TO phase
	entry.AddNegative(WeightInvalidRecipient) // -2

	if entry.Negative != WeightInvalidRecipient {
		t.Errorf("After invalid recipient: Negative = %d; want %d", entry.Negative, WeightInvalidRecipient)
	}

	// 99 successful deliveries
	entry.AddPositive(WeightHamDelivery * 99) // +99

	if entry.Positive != 99 {
		t.Errorf("After delivery: Positive = %d; want 99", entry.Positive)
	}

	// No cross-penalty: Negative stays at 2
	if entry.Negative != WeightInvalidRecipient {
		t.Errorf("After delivery: Negative = %d; want %d (no cross-penalty)", entry.Negative, WeightInvalidRecipient)
	}

	// Reputation should be strongly positive
	// (99 - 2) / (99 + 2 + 20) = 97/121 ≈ 0.802
	rep := entry.GetReputation()
	if rep <= 0 {
		t.Errorf("Reputation = %f; should be positive for legitimate mailing list", rep)
	}
	if !almostEqual(rep, 0.802) {
		t.Errorf("Reputation = %f; want ~0.802 (97 / 121)", rep)
	}

	// Should NOT be denied
	if entry.ShouldDeny() {
		t.Error("Legitimate mailing list should not be denied")
	}
}

// TestIPEntry_BulkSpammerStillDenied verifies that a bulk spammer sending
// to many invalid recipients still gets negative reputation.
func TestIPEntry_BulkSpammerStillDenied(t *testing.T) {
	entry := &IPEntry{
		Connections: MinDataThreshold,
		LastSeen:    time.Now(),
	}

	// Spammer: 50 invalid recipients
	for i := 0; i < 50; i++ {
		entry.AddNegative(WeightInvalidRecipient) // -2 each = -100 total
	}

	// Only 5 successful deliveries
	entry.AddPositive(WeightHamDelivery * 5) // +5

	// Negative = 100, Positive = 5
	// Reputation = (5 - 100) / (5 + 100 + 20) = -95/125 = -0.76
	rep := entry.GetReputation()
	if rep >= 0 {
		t.Errorf("Spammer reputation = %f; should be negative", rep)
	}

	// Should be denied
	if !entry.ShouldDeny() {
		t.Error("Bulk spammer should be denied")
	}

	t.Logf("Bulk spammer: positive=%d, negative=%d, reputation=%f ✓",
		entry.Positive, entry.Negative, rep)
}

// TestIPEntry_IndependentCounters tests that AddPositive and AddNegative
// do NOT cross-modify the other counter. This prevents wild reputation
// swings from a single event erasing accumulated history.
func TestIPEntry_IndependentCounters(t *testing.T) {
	tests := []struct {
		name             string
		negativeFirst    int64 // applied first via AddNegative
		positiveWeight   int64 // applied second via AddPositive
		expectedPositive int64
		expectedNegative int64
	}{
		{
			name:             "Small negative, large positive",
			negativeFirst:    2,  // 1 invalid recipient
			positiveWeight:   99, // 99 successful deliveries
			expectedPositive: 99,
			expectedNegative: 2, // unchanged
		},
		{
			name:             "Equal negative and positive",
			negativeFirst:    10,
			positiveWeight:   10,
			expectedPositive: 10,
			expectedNegative: 10, // unchanged
		},
		{
			name:             "Large negative, small positive",
			negativeFirst:    100,
			positiveWeight:   5,
			expectedPositive: 5,
			expectedNegative: 100, // unchanged
		},
		{
			name:             "Zero positive after negative",
			negativeFirst:    10,
			positiveWeight:   0,
			expectedPositive: 0,
			expectedNegative: 10,
		},
		{
			name:             "No negative, large positive",
			negativeFirst:    0,
			positiveWeight:   100,
			expectedPositive: 100,
			expectedNegative: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &IPEntry{LastSeen: time.Now()}

			if tt.negativeFirst > 0 {
				entry.AddNegative(tt.negativeFirst)
			}
			if tt.positiveWeight > 0 {
				entry.AddPositive(tt.positiveWeight)
			}

			if entry.Positive != tt.expectedPositive {
				t.Errorf("Positive = %d; want %d", entry.Positive, tt.expectedPositive)
			}
			if entry.Negative != tt.expectedNegative {
				t.Errorf("Negative = %d; want %d", entry.Negative, tt.expectedNegative)
			}
		})
	}
}

// TestIPEntry_NegativeAfterPositive tests that AddNegative does NOT
// reduce the Positive counter.
func TestIPEntry_NegativeAfterPositive(t *testing.T) {
	tests := []struct {
		name             string
		positiveFirst    int64 // applied first via AddPositive
		negativeWeight   int64 // applied second via AddNegative
		expectedPositive int64
		expectedNegative int64
	}{
		{
			name:             "100 deliveries then 1 invalid",
			positiveFirst:    100,
			negativeWeight:   WeightInvalidRecipient, // 2
			expectedPositive: 100,                    // unchanged
			expectedNegative: 2,
		},
		{
			name:             "100 deliveries then spoofing attempt",
			positiveFirst:    100,
			negativeWeight:   WeightSpoofingAttempt, // 10
			expectedPositive: 100,                   // unchanged
			expectedNegative: 10,
		},
		{
			name:             "5 deliveries then DMARC failure",
			positiveFirst:    5,
			negativeWeight:   WeightDMARCFailure, // 3
			expectedPositive: 5,                  // unchanged
			expectedNegative: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &IPEntry{LastSeen: time.Now()}

			entry.AddPositive(tt.positiveFirst)
			entry.AddNegative(tt.negativeWeight)

			if entry.Positive != tt.expectedPositive {
				t.Errorf("Positive = %d; want %d", entry.Positive, tt.expectedPositive)
			}
			if entry.Negative != tt.expectedNegative {
				t.Errorf("Negative = %d; want %d", entry.Negative, tt.expectedNegative)
			}
		})
	}
}

// TestIPEntry_ReputationScoreWithMultiRecipient tests the computed reputation
// score for various mailing list scenarios.
func TestIPEntry_ReputationScoreWithMultiRecipient(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(e *IPEntry)
		wantDeny    bool
		wantRepSign int // -1 negative, 0 neutral, +1 positive
	}{
		{
			name: "Perfect mailing list: 100 recipients, 0 failures",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				e.AddPositive(100) // 100 successful recipients
			},
			wantDeny:    false,
			wantRepSign: +1,
		},
		{
			name: "Good mailing list: 100 recipients, 1 invalid",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				e.AddNegative(WeightInvalidRecipient) // -2 for 1 invalid
				e.AddPositive(99)                     // 99 successful
			},
			wantDeny:    false,
			wantRepSign: +1,
		},
		{
			name: "Good mailing list: 100 recipients, 5 invalid",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				for i := 0; i < 5; i++ {
					e.AddNegative(WeightInvalidRecipient) // 5 × -2 = -10
				}
				e.AddPositive(95) // 95 successful
			},
			wantDeny:    false,
			wantRepSign: +1,
		},
		{
			name: "Bad sender: 10 recipients, 8 invalid",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				for i := 0; i < 8; i++ {
					e.AddNegative(WeightInvalidRecipient) // 8 × -2 = -16
				}
				e.AddPositive(2) // only 2 successful
				// Reputation = (2-16)/(2+16+20) = -14/38 = -0.368 → denied
			},
			wantDeny:    true,
			wantRepSign: -1,
		},
		{
			name: "Spammer: 50 recipients, all junk",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				for i := 0; i < 50; i++ {
					e.AddNegative(WeightJunkMessage) // 50 × -1 = -50
				}
				// Reputation = (0-50)/(0+50+20) = -50/70 = -0.714 → denied
			},
			wantDeny:    true,
			wantRepSign: -1,
		},
		{
			name: "Mixed: 200 good deliveries, 10 junk, 5 invalid",
			setup: func(e *IPEntry) {
				e.Connections = MinDataThreshold
				e.AddPositive(200) // 200 good recipients
				for i := 0; i < 10; i++ {
					e.AddNegative(WeightJunkMessage) // 10 junk
				}
				for i := 0; i < 5; i++ {
					e.AddNegative(WeightInvalidRecipient) // 5 invalid
				}
				// Positive=200, Negative=10+10=20
				// Reputation = (200-20)/(200+20+20) = 180/240 = 0.75
			},
			wantDeny:    false,
			wantRepSign: +1,
		},
		{
			name: "Small amount of negative data: should be negative but not denied",
			setup: func(e *IPEntry) {
				e.AddNegative(1) // Just 1 junk message
				// Reputation = (0-1)/(0+1+20) = -1/21 ≈ -0.048 → NOT denied
			},
			wantDeny:    false,
			wantRepSign: -1,
		},
		{
			name: "Amazon SES scenario: 3 junk emails should NOT be denied",
			setup: func(e *IPEntry) {
				// Simulate 3 emails marked as junk due to missing DMARC
				for i := 0; i < 3; i++ {
					e.AddNegative(WeightJunkMessage) // 3 × -1 = -3
				}
				// Reputation = (0-3)/(0+3+20) = -3/23 ≈ -0.130 → NOT denied
			},
			wantDeny:    false,
			wantRepSign: -1,
		},
		{
			name: "Amazon SES scenario: 3 junk + 2 ham should NOT be denied",
			setup: func(e *IPEntry) {
				e.AddNegative(WeightJunkMessage) // junk
				e.AddPositive(WeightHamDelivery) // ham
				e.AddNegative(WeightJunkMessage) // junk
				e.AddPositive(WeightHamDelivery) // ham
				e.AddNegative(WeightJunkMessage) // junk
				// Positive=2, Negative=3
				// Reputation = (2-3)/(2+3+20) = -1/25 = -0.04 → NOT denied
			},
			wantDeny:    false,
			wantRepSign: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &IPEntry{LastSeen: time.Now()}
			tt.setup(entry)

			rep := entry.GetReputation()
			deny := entry.ShouldDeny()

			if deny != tt.wantDeny {
				t.Errorf("ShouldDeny() = %v; want %v (reputation=%f, positive=%d, negative=%d)",
					deny, tt.wantDeny, rep, entry.Positive, entry.Negative)
			}

			switch tt.wantRepSign {
			case +1:
				if rep <= 0 {
					t.Errorf("Reputation = %f; want positive", rep)
				}
			case -1:
				if rep >= 0 {
					t.Errorf("Reputation = %f; want negative", rep)
				}
			case 0:
				if rep != 0 {
					t.Errorf("Reputation = %f; want 0 (neutral)", rep)
				}
			}

			t.Logf("positive=%d, negative=%d, reputation=%.3f, deny=%v",
				entry.Positive, entry.Negative, rep, deny)
		})
	}
}

// TestIPEntry_MultipleTransactions simulates multiple SMTP transactions from
// the same IP, each with different recipient counts.
func TestIPEntry_MultipleTransactions(t *testing.T) {
	entry := &IPEntry{
		Connections: MinDataThreshold,
		LastSeen:    time.Now(),
	}

	// Transaction 1: Newsletter to 50 recipients, all valid
	entry.AddPositive(WeightHamDelivery * 50) // +50

	// Transaction 2: Newsletter to 30 recipients, 2 invalid
	entry.AddNegative(WeightInvalidRecipient) // -2
	entry.AddNegative(WeightInvalidRecipient) // -2
	entry.AddPositive(WeightHamDelivery * 28) // +28

	// Transaction 3: Single email
	entry.AddPositive(WeightHamDelivery * 1) // +1

	// Total: Positive = 50 + 28 + 1 = 79, Negative = 2 + 2 = 4
	// Reputation = (79 - 4) / (79 + 4 + 20) = 75/103 ≈ 0.728
	rep := entry.GetReputation()
	if rep <= 0 {
		t.Errorf("Reputation after multiple transactions = %f; should be positive", rep)
	}
	if entry.ShouldDeny() {
		t.Error("IP with mostly good transactions should not be denied")
	}

	if entry.Positive != 79 {
		t.Errorf("Positive = %d; want 79", entry.Positive)
	}
	if entry.Negative != 4 {
		t.Errorf("Negative = %d; want 4", entry.Negative)
	}

	t.Logf("Multiple transactions: positive=%d, negative=%d, reputation=%.3f",
		entry.Positive, entry.Negative, rep)
}

// TestIPEntry_WeightDMARCFailure verifies DMARC failure weight is lower than spoofing.
func TestIPEntry_WeightDMARCFailure(t *testing.T) {
	if WeightDMARCFailure >= WeightSpoofingAttempt {
		t.Errorf("WeightDMARCFailure (%d) should be less than WeightSpoofingAttempt (%d)",
			WeightDMARCFailure, WeightSpoofingAttempt)
	}
	if WeightDMARCFailure != 3 {
		t.Errorf("WeightDMARCFailure = %d; want 3", WeightDMARCFailure)
	}
}

// TestIPEntry_MinDataThresholdSmoothing verifies that the smoothing constant
// prevents small amounts of negative data from immediately denying an IP.
func TestIPEntry_MinDataThresholdSmoothing(t *testing.T) {
	// With MinDataThreshold=20, even 5 junk messages shouldn't deny
	entry := &IPEntry{LastSeen: time.Now()}
	for i := 0; i < 5; i++ {
		entry.AddNegative(WeightJunkMessage) // 5 × 1 = 5
	}

	// Reputation = (0 - 5) / (0 + 5 + 20) = -5/25 = -0.2
	// This is exactly at the threshold boundary, so should NOT be denied
	// (the check is strictly less than -0.2)
	rep := entry.GetReputation()
	t.Logf("5 junk messages: reputation=%.4f (threshold=%.1f)", rep, ReputationDenyThreshold)

	// With smoothing=20, need more negative weight to cross -0.2
	// N/(N+20) > 0.2 → N > 0.2*(N+20) → 0.8N > 4 → N > 5
	// So 5 is exactly at boundary, 6+ should deny
	if entry.ShouldDeny() {
		t.Error("5 junk messages should NOT deny with smoothing=20 (score is at boundary)")
	}

	// 6 junk messages should tip over
	entry.AddNegative(WeightJunkMessage)
	rep = entry.GetReputation()
	// (0-6)/(0+6+20) = -6/26 ≈ -0.231 → denied
	if !entry.ShouldDeny() {
		t.Errorf("6 junk messages should deny: reputation=%.4f", rep)
	}
}
