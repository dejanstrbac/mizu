package domains

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	domainRefreshInterval = 1 * time.Minute
	httpTimeout           = 30 * time.Second
)

// Manager handles domain validation and caching
type Manager struct {
	url        string
	apiKey     string
	httpClient *http.Client
	logger     *zap.Logger

	// Domain cache
	domains  map[string]struct{}
	lastETag string
	mu       sync.RWMutex
	ready    bool // Whether domains have been successfully loaded
	stale    bool // Whether the last refresh attempt failed
}

// NewManager creates a new domain manager
func NewManager(domainsURL, apiKey string, logger *zap.Logger) *Manager {
	return &Manager{
		url:    domainsURL,
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
		logger:  logger,
		domains: make(map[string]struct{}),
	}
}

// Start initializes the domain cache and starts the refresh loop
func (m *Manager) Start(ctx context.Context) error {
	// Initial sync (blocking)
	m.logger.Info("Performing initial sync of valid domains...")
	domains, etag, err := m.fetchDomains(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to perform initial sync of valid domains: %w", err)
	}

	m.updateCache(domains, etag)
	m.logger.Info("Initial valid domains cache populated",
		zap.Int("count", len(domains)),
		zap.String("etag", etag))

	// Start background refresh loop
	go m.refreshLoop(ctx)

	return nil
}

// IsValidDomain checks if a domain is in the valid domains list
func (m *Manager) IsValidDomain(domain string) bool {
	// Extract domain from email if needed
	if idx := strings.LastIndex(domain, "@"); idx != -1 {
		domain = domain[idx+1:]
	}

	// Check cache
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.domains[strings.ToLower(domain)]
	return exists
}

// IsReady returns whether the domain list has been successfully loaded
func (m *Manager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// IsStale returns whether the domain list failed to refresh
func (m *Manager) IsStale() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stale
}

// fetchDomains fetches the list of valid domains from the configured URL or file
func (m *Manager) fetchDomains(ctx context.Context, etag string) ([]string, string, error) {
	// Check if it's a local file
	if !strings.HasPrefix(m.url, "http://") && !strings.HasPrefix(m.url, "https://") {
		return m.fetchLocalFile()
	}

	// HTTP/HTTPS fetch
	req, err := http.NewRequestWithContext(ctx, "GET", m.url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	if etag != "" {
		req.Header.Set("If-None-Match", etag) // Use ETag for conditional GET
	}

	// Add API key if provided
	if m.apiKey != "" {
		req.Header.Set("X-API-Key", m.apiKey)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		m.logger.Debug("Valid domains list not modified (304)")
		return nil, etag, nil // No new data, return current ETag
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("domains URL returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var domains []string
	if err := json.NewDecoder(resp.Body).Decode(&domains); err != nil {
		return nil, "", fmt.Errorf("failed to decode domains response: %w", err)
	}

	newETag := resp.Header.Get("ETag")
	return domains, newETag, nil
}

// fetchLocalFile reads domains from a local file
func (m *Manager) fetchLocalFile() ([]string, string, error) {
	file, err := os.Open(m.url)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open local file: %w", err)
	}
	defer file.Close()

	// Get file info for modification time (use as etag)
	info, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("failed to stat file: %w", err)
	}

	// Use modification time as etag
	newETag := info.ModTime().Format(time.RFC3339Nano)

	// Check if file hasn't changed
	if m.lastETag == newETag {
		m.logger.Debug("Local domains file not modified")
		return nil, newETag, nil
	}

	var domains []string
	if err := json.NewDecoder(file).Decode(&domains); err != nil {
		return nil, "", fmt.Errorf("failed to decode local domains file: %w", err)
	}

	m.logger.Info("Loaded domains from local file",
		zap.String("path", m.url),
		zap.Int("count", len(domains)))

	return domains, newETag, nil
}

// updateCache updates the domain cache with new data
func (m *Manager) updateCache(domains []string, etag string) {
	newMap := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		newMap[strings.ToLower(d)] = struct{}{}
	}

	m.mu.Lock()
	m.domains = newMap // Replace the old map
	m.lastETag = etag
	m.ready = true
	m.stale = false // Successfully refreshed, not stale anymore
	m.mu.Unlock()
}

// refreshLoop periodically refreshes the domain cache
func (m *Manager) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(domainRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.logger.Debug("Refreshing valid domains cache...")

			m.mu.RLock()
			currentETag := m.lastETag
			m.mu.RUnlock()

			domains, etag, err := m.fetchDomains(ctx, currentETag)
			if err != nil {
				m.logger.Error("Error refreshing valid domains cache", zap.Error(err))
				// Mark as not ready if we have no domains at all, or stale if we do
				m.mu.Lock()
				if len(m.lastETag) == 0 {
					m.ready = false
				} else {
					m.stale = true // We have domains but failed to refresh
				}
				m.mu.Unlock()
			} else if domains != nil { // Only update if new data was returned (not 304)
				m.updateCache(domains, etag)
				m.logger.Info("Successfully refreshed valid domains cache",
					zap.Int("count", len(domains)),
					zap.String("etag", etag))
			}
		case <-ctx.Done():
			m.logger.Info("Domain refresh loop stopped")
			return
		}
	}
}
