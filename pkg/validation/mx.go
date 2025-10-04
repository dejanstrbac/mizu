package validation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	// MXLookupTimeout is the maximum time to wait for MX record lookup
	MXLookupTimeout = 5 * time.Second
)

// CheckMXRecord verifies that the domain has valid MX records.
// This helps prevent spam from domains without proper mail infrastructure.
// Returns true if the domain has valid MX records, false otherwise.
func CheckMXRecord(ctx context.Context, domain string, resolver *net.Resolver, timeout time.Duration) (bool, error) {
	// Normalize domain: remove any angle brackets and trim whitespace
	domain = strings.Trim(domain, "<>")
	domain = strings.TrimSpace(domain)

	if domain == "" {
		return false, fmt.Errorf("empty domain")
	}

	// Use default resolver if none provided
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	// Use default timeout if not specified
	if timeout == 0 {
		timeout = MXLookupTimeout
	}

	// Create context with timeout
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform MX lookup
	mxRecords, err := resolver.LookupMX(lookupCtx, domain)
	if err != nil {
		// Check if it's a timeout
		if lookupCtx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("MX lookup timeout for domain %s: %w", domain, err)
		}

		// DNS errors mean no MX records found
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, nil // No MX records, but not an error
		}

		return false, fmt.Errorf("MX lookup failed for domain %s: %w", domain, err)
	}

	// Check if we got any MX records
	if len(mxRecords) == 0 {
		return false, nil // No MX records found
	}

	// Domain has valid MX records
	return true, nil
}
