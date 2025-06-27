package blacklist

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Checker handles DNS blacklist lookups
type Checker struct {
	lists   []string
	timeout time.Duration
	logger  *zap.Logger
}

// NewChecker creates a new DNS blacklist checker
func NewChecker(lists []string, timeout time.Duration, logger *zap.Logger) *Checker {
	return &Checker{
		lists:   lists,
		timeout: timeout,
		logger:  logger,
	}
}

// CheckIP checks if an IP address is listed in any of the configured blacklists
func (c *Checker) CheckIP(ip net.IP) (bool, string, error) {
	// Convert IP to reverse format for DNS lookup
	reverseIP := reverseIPAddress(ip)
	if reverseIP == "" {
		return false, "", fmt.Errorf("invalid IP address")
	}

	// Check each blacklist
	for _, list := range c.lists {
		query := fmt.Sprintf("%s.%s", reverseIP, list)

		// Create context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()

		// Perform DNS lookup
		r := &net.Resolver{}
		addrs, err := r.LookupIPAddr(ctx, query)

		if err != nil {
			// No listing found (NXDOMAIN) - this is normal and expected
			if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
				continue
			}
			// Log actual DNS errors but don't fail the check
			c.logger.Debug("DNS lookup error",
				zap.String("query", query),
				zap.Error(err))
			continue
		}

		// If we got a response, the IP is blacklisted
		if len(addrs) > 0 {
			// Get the response code if it's in 127.0.0.x format
			reason := "listed"
			if len(addrs) > 0 && addrs[0].IP.To4() != nil {
				ip4 := addrs[0].IP.To4()
				if ip4[0] == 127 && ip4[1] == 0 && ip4[2] == 0 {
					reason = getSpamhausReason(ip4[3])
				}
			}

			c.logger.Info("IP found in blacklist",
				zap.String("ip", ip.String()),
				zap.String("list", list),
				zap.String("reason", reason))

			return true, fmt.Sprintf("%s (%s)", list, reason), nil
		}
	}

	return false, "", nil
}

// reverseIPAddress converts an IP address to reverse format for DNS lookups
func reverseIPAddress(ip net.IP) string {
	// Only handle IPv4 for now
	ip4 := ip.To4()
	if ip4 == nil {
		return ""
	}

	// Reverse the octets
	return fmt.Sprintf("%d.%d.%d.%d", ip4[3], ip4[2], ip4[1], ip4[0])
}

// getSpamhausReason interprets Spamhaus return codes
func getSpamhausReason(code byte) string {
	switch code {
	case 2:
		return "SBL - Spam source"
	case 3:
		return "CSS - Snowshoe spam"
	case 4, 5, 6, 7:
		return "XBL - Exploited/compromised"
	case 9:
		return "DROP - Hijacked netblocks"
	case 10, 11:
		return "PBL - Policy block list"
	default:
		return fmt.Sprintf("Code %d", code)
	}
}

// CheckHELOResolves checks if a HELO hostname resolves to any IP
func CheckHELOResolves(hostname string, timeout time.Duration) (bool, error) {
	// Skip IP addresses in brackets
	if strings.HasPrefix(hostname, "[") && strings.HasSuffix(hostname, "]") {
		return true, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r := &net.Resolver{}
	addrs, err := r.LookupHost(ctx, hostname)

	if err != nil {
		return false, err
	}

	return len(addrs) > 0, nil
}
