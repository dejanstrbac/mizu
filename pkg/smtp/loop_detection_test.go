package smtp

import (
	"strings"
	"testing"
)

func TestDetectMailLoop(t *testing.T) {
	tests := []struct {
		name           string
		rawEmail       string
		serverHostname string
		maxHops        int
		expectLoop     bool
		expectHopCount int
		expectHostname string
	}{
		{
			name: "no loop - clean email",
			rawEmail: "From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     false,
			expectHopCount: 0,
		},
		{
			name: "no loop - different servers",
			rawEmail: "Received: from mail1.example.com by mail2.example.com\r\n" +
				"Received: from mail0.example.com by mail1.example.com\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     false,
			expectHopCount: 2,
		},
		{
			name: "loop detected - hostname match",
			rawEmail: "Received: from mail1.example.com by mx.mizu.example\r\n" +
				"Received: from mail0.example.com by mail1.example.com\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     true,
			expectHopCount: 2,
			expectHostname: "mx.mizu.example",
		},
		{
			name: "loop detected - case insensitive hostname",
			rawEmail: "Received: from mail1.example.com by MX.MIZU.EXAMPLE\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     true,
			expectHopCount: 1,
			expectHostname: "mx.mizu.example",
		},
		{
			name: "too many hops",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 35) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     true,
			expectHopCount: 35,
			expectHostname: "", // No hostname match, just too many hops
		},
		{
			name: "exactly at hop limit - no loop (uses > not >=)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 30) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     false,
			expectHopCount: 30,
		},
		{
			name: "one hop over limit - loop detected",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     true,
			expectHopCount: 31,
		},
		{
			name: "hostname in 'from' clause - no loop",
			rawEmail: "Received: from mx.mizu.example (192.168.1.1) by other.example.com\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        30,
			expectLoop:     false, // Should only match in 'by' clause
			expectHopCount: 1,
		},
		{
			name: "zero maxHops uses default (30)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        0, // Should default to 30
			expectLoop:     true,
			expectHopCount: 31,
		},
		{
			name: "negative maxHops uses default (30)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname: "mx.mizu.example",
			maxHops:        -1, // Should default to 30
			expectLoop:     true,
			expectHopCount: 31,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectMailLoop(tt.rawEmail, tt.serverHostname, tt.maxHops)

			if result.IsLoop != tt.expectLoop {
				t.Errorf("IsLoop = %v, want %v", result.IsLoop, tt.expectLoop)
			}

			if result.HopCount != tt.expectHopCount {
				t.Errorf("HopCount = %v, want %v", result.HopCount, tt.expectHopCount)
			}

			if result.LoopHostname != tt.expectHostname {
				t.Errorf("LoopHostname = %q, want %q", result.LoopHostname, tt.expectHostname)
			}
		})
	}
}

func TestDetectMailLoop_ComplexReceivedHeaders(t *testing.T) {
	// Test with more realistic, multi-line Received headers
	rawEmail := "Received: from mail.sender.com (mail.sender.com [192.168.1.10])\r\n" +
		"\tby mx.mizu.example with ESMTPS id abc123\r\n" +
		"\tfor <user@example.com>; Mon, 12 Feb 2026 10:00:00 +0000\r\n" +
		"Received: from gateway.example.com (gateway.example.com [10.0.0.1])\r\n" +
		"\tby mail.sender.com with ESMTP id xyz789;\r\n" +
		"\tMon, 12 Feb 2026 09:59:50 +0000\r\n" +
		"From: sender@sender.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: Test\r\n" +
		"\r\n" +
		"Body"

	result := detectMailLoop(rawEmail, "mx.mizu.example", 30)

	if !result.IsLoop {
		t.Error("Expected loop to be detected with hostname match in multi-line Received header")
	}

	if result.HopCount != 2 {
		t.Errorf("Expected 2 hops, got %d", result.HopCount)
	}

	if result.LoopHostname != "mx.mizu.example" {
		t.Errorf("Expected LoopHostname = 'mx.mizu.example', got %q", result.LoopHostname)
	}
}
