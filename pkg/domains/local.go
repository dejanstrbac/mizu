package domains

import (
	"strings"
)

// LocalManager is a simple domain manager for local development
// that only accepts emails for a single configured domain
type LocalManager struct {
	domain string
}

// NewLocalManager creates a domain manager for local mode
func NewLocalManager(domain string) *LocalManager {
	return &LocalManager{
		domain: strings.ToLower(domain),
	}
}

// IsValidDomain checks if the email/domain matches the configured local domain
func (m *LocalManager) IsValidDomain(emailOrDomain string) bool {
	// Extract domain from email if needed
	domain := emailOrDomain
	if idx := strings.LastIndex(emailOrDomain, "@"); idx != -1 {
		domain = emailOrDomain[idx+1:]
	}

	// Case-insensitive comparison
	return strings.ToLower(domain) == m.domain
}

// IsReady always returns true for local manager since it doesn't fetch external data
func (m *LocalManager) IsReady() bool {
	return true
}

// IsStale always returns false for local manager since it doesn't fetch external data
func (m *LocalManager) IsStale() bool {
	return false
}
