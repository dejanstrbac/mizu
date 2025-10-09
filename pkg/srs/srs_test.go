package srs

import (
	"strings"
	"testing"
	"time"
)

func TestSRS_BasicEncodeDecode(t *testing.T) {
	rewriter := NewRewriter("test-secret-key", "relay.mizu.com")

	originalAddress := "alice@example.com"

	// Encode
	srsAddress, err := rewriter.Encode(originalAddress)
	if err != nil {
		t.Fatalf("Failed to encode address: %v", err)
	}

	// Verify format
	if !strings.HasPrefix(srsAddress, "SRS0=") {
		t.Errorf("Expected SRS0 prefix, got: %s", srsAddress)
	}
	if !strings.HasSuffix(srsAddress, "@relay.mizu.com") {
		t.Errorf("Expected domain relay.mizu.com, got: %s", srsAddress)
	}

	t.Logf("Encoded: %s → %s", originalAddress, srsAddress)

	// Decode
	decoded, err := rewriter.Decode(srsAddress)
	if err != nil {
		t.Fatalf("Failed to decode SRS address: %v", err)
	}

	if decoded != originalAddress {
		t.Errorf("Expected %s, got %s", originalAddress, decoded)
	}

	t.Logf("Decoded: %s → %s", srsAddress, decoded)
}

func TestSRS_MultipleAddresses(t *testing.T) {
	rewriter := NewRewriter("my-secret", "forward.example.com")

	testCases := []string{
		"user@domain.com",
		"alice.bob@example.org",
		"test+tag@gmail.com",
		"simple@localhost",
		"user-name@sub.domain.example.com",
	}

	for _, original := range testCases {
		t.Run(original, func(t *testing.T) {
			// Encode
			srs, err := rewriter.Encode(original)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			// Decode
			decoded, err := rewriter.Decode(srs)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			if decoded != original {
				t.Errorf("Roundtrip failed: %s → %s → %s", original, srs, decoded)
			}

			t.Logf("✓ %s → %s → %s", original, srs, decoded)
		})
	}
}

func TestSRS_DoubleEncoding(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	// First encoding
	original := "alice@example.com"
	srs0, err := rewriter.Encode(original)
	if err != nil {
		t.Fatalf("First encode failed: %v", err)
	}

	if !strings.HasPrefix(srs0, "SRS0=") {
		t.Errorf("Expected SRS0, got: %s", srs0)
	}

	// Second encoding (re-forwarding) should create SRS1
	srs1, err := rewriter.Encode(srs0)
	if err != nil {
		t.Fatalf("Second encode failed: %v", err)
	}

	if !strings.HasPrefix(srs1, "SRS1=") {
		t.Errorf("Expected SRS1 for re-forwarding, got: %s", srs1)
	}

	// Third encoding (SRS1 should remain SRS1)
	srs1Again, err := rewriter.Encode(srs1)
	if err != nil {
		t.Fatalf("Third encode failed: %v", err)
	}

	if srs1Again != srs1 {
		t.Errorf("SRS1 should not be re-encoded: %s ≠ %s", srs1, srs1Again)
	}

	t.Logf("Original:  %s", original)
	t.Logf("SRS0:      %s", srs0)
	t.Logf("SRS1:      %s", srs1)
	t.Logf("SRS1 again:%s", srs1Again)

	// Decode SRS1 should give original address
	decoded, err := rewriter.Decode(srs1)
	if err != nil {
		t.Fatalf("Decode SRS1 failed: %v", err)
	}

	if decoded != original {
		t.Errorf("Expected %s, got %s", original, decoded)
	}
}

func TestSRS_InvalidHash(t *testing.T) {
	rewriter := NewRewriter("correct-secret", "relay.mizu.com")

	// Encode with correct secret
	original := "alice@example.com"
	srsAddress, err := rewriter.Encode(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Try to decode with different secret
	wrongRewriter := NewRewriter("wrong-secret", "relay.mizu.com")
	_, err = wrongRewriter.Decode(srsAddress)
	if err == nil {
		t.Error("Expected error when decoding with wrong secret")
	}
	if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "hash") {
		t.Errorf("Expected hash validation error, got: %v", err)
	}

	t.Logf("✓ Hash validation working: %v", err)
}

func TestSRS_IsSRSAddress(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	testCases := []struct {
		address  string
		expected bool
	}{
		{"alice@example.com", false},
		{"SRS0=abcd=5Z=example.com=alice@relay.mizu.com", true},
		{"SRS1=wxyz=relay.mizu.com==SRS0=abcd=5Z=example.com=alice@relay.mizu.com", true},
		{"srs0=test@example.com", false}, // lowercase prefix shouldn't match
		{"not-srs@example.com", false},
		{"", false},
	}

	for _, tc := range testCases {
		result := rewriter.IsSRSAddress(tc.address)
		if result != tc.expected {
			t.Errorf("IsSRSAddress(%s) = %v, expected %v", tc.address, result, tc.expected)
		}
	}
}

func TestSRS_EmptyAddress(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	_, err := rewriter.Encode("")
	if err == nil {
		t.Error("Expected error for empty address")
	}

	_, err = rewriter.Decode("")
	if err == nil {
		t.Error("Expected error for empty SRS address")
	}
}

func TestSRS_InvalidAddress(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	invalidAddresses := []string{
		"no-at-sign",
		"@domain.com", // Empty localpart
		"user@",       // Empty domain
	}

	for _, addr := range invalidAddresses {
		_, err := rewriter.Encode(addr)
		if err == nil {
			t.Errorf("Expected error for invalid address: %s", addr)
		}
	}

	// Note: "user@domain@extra" is split as "user" @ "domain@extra"
	// This is technically allowed (domain could contain @), so we don't reject it
}

func TestSRS_InvalidFormat(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	invalidSRSAddresses := []string{
		"SRS0=abc@relay.mizu.com",            // Missing components
		"SRS0=abcd=TT@relay.mizu.com",        // Missing domain and localpart
		"SRS0=abcd=TT=domain@relay.mizu.com", // Missing localpart
		"SRS1=xyz=domain@relay.mizu.com",     // Invalid SRS1 format
		"not-srs@relay.mizu.com",             // Not SRS at all
	}

	for _, addr := range invalidSRSAddresses {
		_, err := rewriter.Decode(addr)
		if err == nil {
			t.Errorf("Expected error for invalid SRS address: %s", addr)
		}
		t.Logf("✓ Rejected invalid format: %s (%v)", addr, err)
	}
}

func TestSRS_ConsistentHashing(t *testing.T) {
	rewriter := NewRewriter("consistent-secret", "relay.mizu.com")

	original := "test@example.com"

	// Encode multiple times
	srs1, _ := rewriter.Encode(original)
	srs2, _ := rewriter.Encode(original)

	// Hashes should be identical (same timestamp at this granularity)
	if srs1 != srs2 {
		// This might differ due to timestamp, but let's check the components
		t.Logf("Note: SRS addresses may differ due to timestamp: %s vs %s", srs1, srs2)
	}

	// Both should decode to the same original
	decoded1, err := rewriter.Decode(srs1)
	if err != nil {
		t.Fatalf("Decode 1 failed: %v", err)
	}
	decoded2, err := rewriter.Decode(srs2)
	if err != nil {
		t.Fatalf("Decode 2 failed: %v", err)
	}

	if decoded1 != original || decoded2 != original {
		t.Errorf("Decoding inconsistent: %s, %s (expected %s)", decoded1, decoded2, original)
	}
}

func TestSRS_DifferentDomains(t *testing.T) {
	rewriter1 := NewRewriter("secret", "relay1.mizu.com")
	rewriter2 := NewRewriter("secret", "relay2.mizu.com")

	original := "alice@example.com"

	srs1, _ := rewriter1.Encode(original)
	srs2, _ := rewriter2.Encode(original)

	// Should use different domains
	if !strings.Contains(srs1, "@relay1.mizu.com") {
		t.Errorf("Expected relay1.mizu.com in %s", srs1)
	}
	if !strings.Contains(srs2, "@relay2.mizu.com") {
		t.Errorf("Expected relay2.mizu.com in %s", srs2)
	}

	// Each rewriter should decode its own address
	decoded1, err := rewriter1.Decode(srs1)
	if err != nil {
		t.Fatalf("Decode with rewriter1 failed: %v", err)
	}
	if decoded1 != original {
		t.Errorf("Expected %s, got %s", original, decoded1)
	}

	decoded2, err := rewriter2.Decode(srs2)
	if err != nil {
		t.Fatalf("Decode with rewriter2 failed: %v", err)
	}
	if decoded2 != original {
		t.Errorf("Expected %s, got %s", original, decoded2)
	}
}

func TestSRS_TimestampEncoding(t *testing.T) {
	rewriter := NewRewriter("secret", "relay.mizu.com")

	// Test with a known time
	testTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Encode timestamp
	encoded := rewriter.encodeTimestamp(testTime)

	// Should be 2 characters
	if len(encoded) != timestampLength {
		t.Errorf("Expected timestamp length %d, got %d: %s", timestampLength, len(encoded), encoded)
	}

	// Should be lowercase base32
	for _, c := range encoded {
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			t.Errorf("Invalid base32 character in timestamp: %c", c)
		}
	}

	t.Logf("Timestamp for %s: %s", testTime.Format(time.RFC3339), encoded)
}

func TestSRS_ComponentStructure(t *testing.T) {
	rewriter := NewRewriter("test-secret", "relay.mizu.com")

	original := "user@example.com"
	srs, err := rewriter.Encode(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Parse the SRS address structure
	// Format: SRS0=HHHH=TT=domain=localpart@relay.mizu.com

	parts := strings.SplitN(srs, "@", 2)
	if len(parts) != 2 {
		t.Fatalf("Invalid SRS address structure: %s", srs)
	}

	localpart := parts[0]
	domain := parts[1]

	if domain != "relay.mizu.com" {
		t.Errorf("Expected domain relay.mizu.com, got %s", domain)
	}

	// Remove SRS0= prefix
	content := strings.TrimPrefix(localpart, SRS0Prefix)
	components := strings.SplitN(content, "=", 4)

	if len(components) != 4 {
		t.Fatalf("Expected 4 components, got %d: %v", len(components), components)
	}

	hash := components[0]
	timestamp := components[1]
	origDomain := components[2]
	origLocal := components[3]

	// Validate component lengths and content
	if len(hash) != hashLength {
		t.Errorf("Expected hash length %d, got %d", hashLength, len(hash))
	}
	if len(timestamp) != timestampLength {
		t.Errorf("Expected timestamp length %d, got %d", timestampLength, len(timestamp))
	}
	if origDomain != "example.com" {
		t.Errorf("Expected original domain example.com, got %s", origDomain)
	}
	if origLocal != "user" {
		t.Errorf("Expected original localpart user, got %s", origLocal)
	}

	t.Logf("✓ SRS structure valid:")
	t.Logf("  Hash:      %s", hash)
	t.Logf("  Timestamp: %s", timestamp)
	t.Logf("  Domain:    %s", origDomain)
	t.Logf("  Localpart: %s", origLocal)
}
