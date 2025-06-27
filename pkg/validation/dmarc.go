package validation

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/mail"
	"strings"

	"github.com/emersion/go-dmarc"
	"github.com/mileusna/spf"
)

// DMARCResult represents the result of DMARC validation
type DMARCResult struct {
	Pass           bool
	Policy         string // none, quarantine, or reject
	SPFAligned     bool
	DKIMAligned    bool
	FailureReasons []string
}

// CheckDMARC performs DMARC validation on an email
func CheckDMARC(ctx context.Context, rawEmail string, remoteIP net.IP, heloHost string) (*DMARCResult, error) {
	// Parse the email message
	msg, err := mail.ReadMessage(strings.NewReader(rawEmail))
	if err != nil {
		return nil, fmt.Errorf("failed to parse email: %w", err)
	}

	// Get the From header
	fromHeader := msg.Header.Get("From")
	if fromHeader == "" {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"missing From header"},
		}, nil
	}

	// Parse From address to get domain
	fromAddrs, err := mail.ParseAddressList(fromHeader)
	if err != nil || len(fromAddrs) == 0 {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"invalid From header"},
		}, nil
	}

	fromAddr := fromAddrs[0].Address
	parts := strings.Split(fromAddr, "@")
	if len(parts) != 2 {
		return &DMARCResult{
			Pass:           false,
			Policy:         "none",
			FailureReasons: []string{"invalid From address format"},
		}, nil
	}
	fromDomain := parts[1]

	// Look up DMARC policy
	record, err := dmarc.Lookup(fromDomain)
	if err != nil {
		log.Printf("DMARC lookup failed for %s: %v", fromDomain, err)
		// No DMARC record means no policy to enforce
		return &DMARCResult{
			Pass:   true,
			Policy: "none",
		}, nil
	}

	result := &DMARCResult{
		Policy:         string(record.Policy),
		FailureReasons: make([]string, 0),
	}

	// Check SPF
	spfResult := spf.CheckHost(remoteIP, fromAddr, heloHost, "")
	spfPass := spfResult == spf.Pass

	// Check SPF alignment
	// For DMARC, SPF must align with the From domain
	returnPath := msg.Header.Get("Return-Path")
	if returnPath == "" {
		// Use envelope from if available
		result.SPFAligned = false
		result.FailureReasons = append(result.FailureReasons, "no Return-Path header for SPF alignment check")
	} else {
		// Extract domain from Return-Path
		returnPath = strings.Trim(returnPath, "<>")
		parts := strings.Split(returnPath, "@")
		if len(parts) == 2 {
			returnDomain := parts[1]
			// Check alignment (relaxed mode by default for SPF)
			// TODO: Check record.SPFAlignment when available in the library
			result.SPFAligned = spfPass && isAligned(fromDomain, returnDomain, false)
		} else {
			result.SPFAligned = false
			result.FailureReasons = append(result.FailureReasons, "invalid Return-Path format")
		}
	}

	// DKIM validation would go here
	// For now, we'll mark DKIM as not aligned since we don't have DKIM validation
	result.DKIMAligned = false
	result.FailureReasons = append(result.FailureReasons, "DKIM validation not implemented")

	// DMARC passes if either SPF or DKIM is aligned
	result.Pass = result.SPFAligned || result.DKIMAligned

	if !result.Pass && result.Policy == "reject" {
		result.FailureReasons = append(result.FailureReasons, "DMARC policy is 'reject' and neither SPF nor DKIM aligned")
	}

	return result, nil
}

// isAligned checks if two domains are aligned according to DMARC rules
func isAligned(fromDomain, authDomain string, strict bool) bool {
	fromDomain = strings.ToLower(strings.TrimSpace(fromDomain))
	authDomain = strings.ToLower(strings.TrimSpace(authDomain))

	if strict {
		// Strict alignment requires exact match
		return fromDomain == authDomain
	}

	// Relaxed alignment allows organizational domain match
	fromOrg := getOrganizationalDomain(fromDomain)
	authOrg := getOrganizationalDomain(authDomain)
	return fromOrg == authOrg
}

// getOrganizationalDomain returns the organizational domain
// This is a simplified version - in production you'd use the Public Suffix List
func getOrganizationalDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		// Return last two parts (e.g., "example.com" from "mail.example.com")
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return domain
}
