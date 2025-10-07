// Package validation provides email authentication and validation functionality.
//
// This package implements industry-standard email authentication protocols:
//   - SPF (Sender Policy Framework) - RFC 7208
//   - DKIM (DomainKeys Identified Mail) - RFC 6376
//   - DMARC (Domain-based Message Authentication, Reporting & Conformance) - RFC 7489
//   - ARC (Authenticated Received Chain) - RFC 8617
//
// # SPF Validation
//
// SPF validation checks whether the sending IP is authorized to send email
// for the envelope sender's domain:
//
//	result := validation.ConvertSPFResult(spfResult)
//	if result.Value == authres.ResultPass {
//	    // IP is authorized
//	}
//
// # DKIM Validation
//
// DKIM validation verifies cryptographic signatures in email headers to ensure
// message integrity and authenticate the signing domain. This is performed as
// part of DMARC validation.
//
// # DMARC Validation
//
// DMARC combines SPF and DKIM validation with alignment checking to provide
// comprehensive sender authentication:
//
//	dmarcResult, err := validation.CheckDMARC(
//	    ctx,
//	    rawEmail,
//	    spfResult,
//	    quarantineAsJunk,
//	    logger,
//	)
//	if dmarcResult.Pass {
//	    // Message passed DMARC validation
//	}
//
// DMARC checks both SPF and DKIM, then verifies that at least one of them
// "aligns" with the domain in the From header. Alignment can be:
//   - Relaxed: organizational domain matches (mail.example.com aligns with example.com)
//   - Strict: exact domain match required
//
// # ARC Validation
//
// ARC (Authenticated Received Chain) preserves email authentication results
// when messages pass through intermediaries like mailing lists or forwarders.
// This prevents authentication from breaking during legitimate forwarding:
//
//	arcResult, err := validation.CheckARC(ctx, rawEmail, logger)
//	if arcResult.Pass && arcResult.Instance > 0 {
//	    // Message has valid ARC chain from previous hops
//	}
//
// ARC validation verifies:
//   - ARC-Seal headers (sign the entire chain)
//   - ARC-Message-Signature headers (sign message content)
//   - ARC-Authentication-Results headers (record auth results)
//   - Chain integrity (instance numbers 1 → N)
//
// # ARC Signing
//
// When acting as a mail forwarder, Mizu can add ARC headers to preserve
// authentication results for downstream servers:
//
//	signer, err := validation.NewARCSigner(
//	    "mail.example.com",
//	    "arc",
//	    "/path/to/private-key.pem",
//	    logger,
//	)
//
//	signedEmail, err := signer.SignEmail(
//	    rawEmail,
//	    spfResult,
//	    dmarcResult,
//	    arcResult,
//	)
//
// ARC signing adds three header types:
//   - ARC-Authentication-Results: Records SPF, DKIM, DMARC results at this hop
//   - ARC-Message-Signature: DKIM-style signature of message headers
//   - ARC-Seal: Signs the entire ARC chain with validation status (cv=none/pass/fail)
//
// # MX Record Validation
//
// MX validation ensures that sender domains have valid mail exchanger records:
//
//	hasValidMX, err := validation.CheckMXRecord(ctx, domain, resolver)
//	if !hasValidMX {
//	    // Domain cannot receive email
//	}
//
// # Authentication Results
//
// All validation results follow the Authentication-Results header format (RFC 8601)
// and can be used to construct standardized authentication result headers for
// downstream processing.
//
// # DNS Timeouts
//
// All DNS lookups (DKIM, DMARC, MX) use a configurable timeout (default 5s)
// to prevent validation from blocking indefinitely on slow DNS servers.
//
// # Thread Safety
//
// All validation functions are thread-safe and can be called concurrently
// from multiple goroutines.
package validation
