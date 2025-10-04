package blacklist

import (
	"fmt"
	"net"
)

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
