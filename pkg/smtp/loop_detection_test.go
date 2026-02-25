package smtp

import (
	"strings"
	"testing"
)

func TestDetectMailLoop(t *testing.T) {
	tests := []struct {
		name                string
		rawEmail            string
		serverHostname      string
		maxHops             int
		expectLoop          bool
		expectHopCount      int
		expectHostname      string
		expectHostnameCount int
	}{
		{
			name: "no loop - clean email",
			rawEmail: "From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          false,
			expectHopCount:      0,
			expectHostnameCount: 0,
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
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          false,
			expectHopCount:      2,
			expectHostnameCount: 0,
		},
		{
			name: "no loop - single hostname occurrence (mailing list / forwarding)",
			rawEmail: "Received: from mail1.example.com by mx.mizu.example\r\n" +
				"Received: from mail0.example.com by mail1.example.com\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          false,
			expectHopCount:      2,
			expectHostnameCount: 1,
		},
		{
			name: "loop detected - hostname appears twice",
			rawEmail: "Received: from groups.google.com by mx.mizu.example\r\n" +
				"Received: from mx.mizu.example by groups.google.com\r\n" +
				"Received: from sender.example.com by mx.mizu.example\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          true,
			expectHopCount:      3,
			expectHostname:      "mx.mizu.example",
			expectHostnameCount: 2,
		},
		{
			name: "loop detected - case insensitive hostname appears twice",
			rawEmail: "Received: from relay.example.com by MX.MIZU.EXAMPLE\r\n" +
				"Received: from other.example.com by mx.mizu.example\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          true,
			expectHopCount:      2,
			expectHostname:      "mx.mizu.example",
			expectHostnameCount: 2,
		},
		{
			name: "too many hops",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 35) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          true,
			expectHopCount:      35,
			expectHostname:      "", // No hostname match, just too many hops
			expectHostnameCount: 0,
		},
		{
			name: "exactly at hop limit - no loop (uses > not >=)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 30) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          false,
			expectHopCount:      30,
			expectHostnameCount: 0,
		},
		{
			name: "one hop over limit - loop detected",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          true,
			expectHopCount:      31,
			expectHostnameCount: 0,
		},
		{
			name: "hostname in 'from' clause only - no loop",
			rawEmail: "Received: from mx.mizu.example (192.168.1.1) by other.example.com\r\n" +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             30,
			expectLoop:          false, // Should only match in 'by' clause
			expectHopCount:      1,
			expectHostnameCount: 0,
		},
		{
			name: "zero maxHops uses default (30)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             0, // Should default to 30
			expectLoop:          true,
			expectHopCount:      31,
			expectHostnameCount: 0,
		},
		{
			name: "negative maxHops uses default (30)",
			rawEmail: strings.Repeat("Received: from mail.example.com by other.example.com\r\n", 31) +
				"From: sender@example.com\r\n" +
				"To: recipient@example.com\r\n" +
				"Subject: Test\r\n" +
				"\r\n" +
				"Body",
			serverHostname:      "mx.mizu.example",
			maxHops:             -1, // Should default to 30
			expectLoop:          true,
			expectHopCount:      31,
			expectHostnameCount: 0,
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

			if result.HostnameCount != tt.expectHostnameCount {
				t.Errorf("HostnameCount = %v, want %v", result.HostnameCount, tt.expectHostnameCount)
			}
		})
	}
}

func TestDetectMailLoop_ComplexReceivedHeaders(t *testing.T) {
	// Test with realistic multi-line Received headers - single occurrence should NOT trigger loop
	// This simulates a mailing list scenario: message was processed by us once, then came back
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

	if result.IsLoop {
		t.Error("Expected no loop - single hostname occurrence is normal for mailing lists/forwarding")
	}

	if result.HopCount != 2 {
		t.Errorf("Expected 2 hops, got %d", result.HopCount)
	}

	if result.HostnameCount != 1 {
		t.Errorf("Expected HostnameCount = 1, got %d", result.HostnameCount)
	}
}

func TestDetectMailLoop_ComplexReceivedHeaders_RealLoop(t *testing.T) {
	// Test with realistic multi-line Received headers - two occurrences IS a loop
	rawEmail := "Received: from groups.google.com (mail-ed1-x540.google.com [2a00:1450:4864:20::540])\r\n" +
		"\tby mx.mizu.example with ESMTPS id def456\r\n" +
		"\tfor <user@example.com>; Mon, 12 Feb 2026 10:01:00 +0000\r\n" +
		"Received: from mx.mizu.example (mx.mizu.example [203.0.113.1])\r\n" +
		"\tby groups.google.com with ESMTPS id ggg789;\r\n" +
		"\tMon, 12 Feb 2026 10:00:30 +0000\r\n" +
		"Received: from mail.sender.com (mail.sender.com [192.168.1.10])\r\n" +
		"\tby mx.mizu.example with ESMTPS id abc123\r\n" +
		"\tfor <list@googlegroups.com>; Mon, 12 Feb 2026 10:00:00 +0000\r\n" +
		"From: sender@sender.com\r\n" +
		"To: list@googlegroups.com\r\n" +
		"Subject: Test\r\n" +
		"\r\n" +
		"Body"

	result := detectMailLoop(rawEmail, "mx.mizu.example", 30)

	if !result.IsLoop {
		t.Error("Expected loop to be detected with hostname appearing twice in Received headers")
	}

	if result.HopCount != 3 {
		t.Errorf("Expected 3 hops, got %d", result.HopCount)
	}

	if result.HostnameCount != 2 {
		t.Errorf("Expected HostnameCount = 2, got %d", result.HostnameCount)
	}

	if result.LoopHostname != "mx.mizu.example" {
		t.Errorf("Expected LoopHostname = 'mx.mizu.example', got %q", result.LoopHostname)
	}
}

func TestDetectMailLoop_MailingListScenario(t *testing.T) {
	// Simulate the exact scenario from the bug report:
	// A Google Groups mailing list message that was originally sent via our server,
	// forwarded to Google Groups, and is now being delivered back to a recipient on our server.
	// At detection time (before we add our new Received header), the existing headers
	// contain only ONE "by mx.gomailify.com" from the original outbound send.
	// This must NOT be flagged as a loop.
	realisticEmail := "Received: from mail-ed1-x540.google.com (mail-ed1-x540.google.com [2a00:1450:4864:20::540])\r\n" +
		"\tby some-google-relay.google.com with ESMTPS id relay001;\r\n" +
		"\tTue, 25 Feb 2026 12:33:50 +0000\r\n" +
		"Received: from mx.gomailify.com (mx.gomailify.com [203.0.113.1])\r\n" +
		"\tby groups-relay.google.com with ESMTPS id abc123;\r\n" +
		"\tTue, 25 Feb 2026 12:33:45 +0000\r\n" +
		"Received: from client.example.com (client.example.com [192.168.1.10])\r\n" +
		"\tby mx.gomailify.com with ESMTPS id orig001\r\n" +
		"\tfor <sequoiateam@googlegroups.com>; Tue, 25 Feb 2026 12:33:40 +0000\r\n" +
		"From: sequoiateam+bncBCAZVE4FQQMBBMGY7PGAMGQEW7WDT4I@googlegroups.com\r\n" +
		"To: training@sequoiaeyegroup.com\r\n" +
		"Subject: Meeting Update\r\n" +
		"\r\n" +
		"Body"

	realisticResult := detectMailLoop(realisticEmail, "mx.gomailify.com", 30)

	if realisticResult.IsLoop {
		t.Error("Expected no loop for mailing list scenario with single hostname occurrence")
	}

	if realisticResult.HopCount != 3 {
		t.Errorf("Expected 3 hops, got %d", realisticResult.HopCount)
	}

	if realisticResult.HostnameCount != 1 {
		t.Errorf("Expected HostnameCount = 1, got %d", realisticResult.HostnameCount)
	}
}
