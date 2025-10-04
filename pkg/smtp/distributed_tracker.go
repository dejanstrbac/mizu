package smtp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"migadu/mizu/pkg/health"
	"net"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"
)

// DistributedTracker wraps ConnectionTracker with P2P gossip and S3 sync capabilities
type DistributedTracker struct {
	local    *ConnectionTracker // Local connection tracking (fast path)
	hostname string             // This server's hostname
	logger   *zap.Logger

	// P2P gossip configuration
	peers           []string      // Peer HTTP endpoints (e.g., "https://smtp2:8080")
	gossipInterval  time.Duration // How often to broadcast (default: 5s)
	peerConnections map[string]*PeerConnectionState
	peerMu          sync.RWMutex

	// S3 sync configuration
	s3Client       *minio.Client
	s3Bucket       string
	s3Prefix       string
	s3SyncInterval time.Duration // How often to sync with S3 (default: 30s)

	// Global limits (enforced across cluster)
	globalMaxPerIP int

	// Recipient cache (distributed via gossip)
	recipientNotFound map[string]time.Time // email -> expiry (404 responses)
	recipientBlocked  map[string]time.Time // email -> expiry (403 responses)
	recipientMu       sync.RWMutex
	recipientCacheTTL time.Duration // How long to cache recipient results

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// PeerConnectionState holds connection counts from a peer server
type PeerConnectionState struct {
	Hostname    string         `json:"hostname"`
	Timestamp   time.Time      `json:"timestamp"`
	Connections map[string]int `json:"connections"` // IP -> count
	TotalCount  int            `json:"total_count"`
	UpdatedAt   time.Time      `json:"-"` // When we received this update
}

// ConnectionSnapshot represents the state to sync
type ConnectionSnapshot struct {
	Hostname       string                  `json:"hostname"`
	Timestamp      time.Time               `json:"timestamp"`
	Connections    map[string]int          `json:"connections"`
	TotalCount     int                     `json:"total_count"`
	Version        int                     `json:"version"` // For conflict resolution
	RecipientCache *RecipientCacheSnapshot `json:"recipient_cache,omitempty"`
}

// RecipientCacheSnapshot holds cached recipient validation results
type RecipientCacheSnapshot struct {
	NotFound map[string]time.Time `json:"not_found,omitempty"` // email -> expiry (404 responses)
	Blocked  map[string]time.Time `json:"blocked,omitempty"`   // email -> expiry (403 responses)
}

// DistributedConfig holds configuration for distributed tracking
type DistributedConfig struct {
	Hostname          string
	Peers             []string      // Peer endpoints
	GossipInterval    time.Duration // P2P broadcast interval
	S3SyncInterval    time.Duration // S3 sync interval
	GlobalMaxPerIP    int           // Global per-IP limit across cluster
	RecipientCacheTTL time.Duration // How long to cache recipient validation results
}

// NewDistributedTracker creates a new distributed connection tracker
func NewDistributedTracker(
	local *ConnectionTracker,
	s3Client *minio.Client,
	s3Bucket, s3Prefix string,
	config DistributedConfig,
	logger *zap.Logger,
) *DistributedTracker {
	ctx, cancel := context.WithCancel(context.Background())

	return &DistributedTracker{
		local:             local,
		hostname:          config.Hostname,
		peers:             config.Peers,
		gossipInterval:    config.GossipInterval,
		s3Client:          s3Client,
		s3Bucket:          s3Bucket,
		s3Prefix:          s3Prefix,
		s3SyncInterval:    config.S3SyncInterval,
		globalMaxPerIP:    config.GlobalMaxPerIP,
		recipientNotFound: make(map[string]time.Time),
		recipientBlocked:  make(map[string]time.Time),
		recipientCacheTTL: config.RecipientCacheTTL,
		peerConnections:   make(map[string]*PeerConnectionState),
		logger:            logger,
		ctx:               ctx,
		cancel:            cancel,
	}
}

// Start begins the gossip and sync loops
func (dt *DistributedTracker) Start() {
	dt.logger.Info("Starting distributed connection tracker",
		zap.String("hostname", dt.hostname),
		zap.Int("peers", len(dt.peers)),
		zap.Duration("gossip_interval", dt.gossipInterval),
		zap.Duration("s3_sync_interval", dt.s3SyncInterval),
		zap.Int("global_max_per_ip", dt.globalMaxPerIP),
		zap.Duration("recipient_cache_ttl", dt.recipientCacheTTL))

	// Start P2P gossip loop
	dt.wg.Add(1)
	go dt.gossipLoop()

	// Start S3 sync loop (if configured)
	if dt.s3Client != nil && dt.s3Bucket != "" {
		dt.wg.Add(1)
		go dt.s3SyncLoop()
	}

	// Start recipient cache cleanup loop
	dt.wg.Add(1)
	go dt.recipientCacheCleanupLoop()

	// Start stale peer cleanup loop
	dt.wg.Add(1)
	go dt.stalePeerCleanupLoop()
}

// recipientCacheCleanupLoop periodically removes expired entries from recipient cache
func (dt *DistributedTracker) recipientCacheCleanupLoop() {
	defer dt.wg.Done()

	ticker := time.NewTicker(1 * time.Minute) // Cleanup every minute
	defer ticker.Stop()

	for {
		select {
		case <-dt.ctx.Done():
			return
		case <-ticker.C:
			dt.cleanupExpiredRecipients()
		}
	}
}

// cleanupExpiredRecipients removes expired entries from recipient cache
func (dt *DistributedTracker) cleanupExpiredRecipients() {
	dt.recipientMu.Lock()
	defer dt.recipientMu.Unlock()

	now := time.Now()
	removed := 0

	for email, expiry := range dt.recipientNotFound {
		if now.After(expiry) {
			delete(dt.recipientNotFound, email)
			removed++
		}
	}

	for email, expiry := range dt.recipientBlocked {
		if now.After(expiry) {
			delete(dt.recipientBlocked, email)
			removed++
		}
	}

	if removed > 0 {
		dt.logger.Debug("Cleaned up expired recipient cache entries",
			zap.Int("removed", removed),
			zap.Int("not_found_remaining", len(dt.recipientNotFound)),
			zap.Int("blocked_remaining", len(dt.recipientBlocked)))
	}
}

// stalePeerCleanupLoop periodically removes stale peer connections
func (dt *DistributedTracker) stalePeerCleanupLoop() {
	defer dt.wg.Done()

	ticker := time.NewTicker(5 * time.Minute) // Cleanup every 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-dt.ctx.Done():
			return
		case <-ticker.C:
			dt.cleanupStalePeers()
		}
	}
}

// cleanupStalePeers removes peer connections that haven't been updated in 5 minutes
func (dt *DistributedTracker) cleanupStalePeers() {
	dt.peerMu.Lock()
	defer dt.peerMu.Unlock()

	now := time.Now()
	removed := 0
	staleThreshold := 5 * time.Minute

	for hostname, peer := range dt.peerConnections {
		if now.Sub(peer.UpdatedAt) > staleThreshold {
			delete(dt.peerConnections, hostname)
			removed++
		}
	}

	if removed > 0 {
		dt.logger.Debug("Cleaned up stale peer connections",
			zap.Int("removed", removed),
			zap.Int("remaining_peers", len(dt.peerConnections)))
	}
}

// CacheRecipientNotFound adds a recipient to the "not found" cache (404 responses)
func (dt *DistributedTracker) CacheRecipientNotFound(email string) {
	dt.recipientMu.Lock()
	defer dt.recipientMu.Unlock()

	expiry := time.Now().Add(dt.recipientCacheTTL)
	dt.recipientNotFound[email] = expiry

	dt.logger.Debug("Cached recipient as not found",
		zap.String("email", email),
		zap.Time("expiry", expiry))
}

// CacheRecipientBlocked adds a recipient to the "blocked" cache (403 responses)
func (dt *DistributedTracker) CacheRecipientBlocked(email string) {
	dt.recipientMu.Lock()
	defer dt.recipientMu.Unlock()

	expiry := time.Now().Add(dt.recipientCacheTTL)
	dt.recipientBlocked[email] = expiry

	dt.logger.Debug("Cached recipient as blocked",
		zap.String("email", email),
		zap.Time("expiry", expiry))
}

// IsRecipientCached checks if a recipient is in the cache and returns the status
// Returns (found, isBlocked, reason)
func (dt *DistributedTracker) IsRecipientCached(email string) (bool, bool, string) {
	dt.recipientMu.RLock()
	defer dt.recipientMu.RUnlock()

	now := time.Now()

	// Check if recipient is in "not found" cache
	if expiry, exists := dt.recipientNotFound[email]; exists {
		if now.Before(expiry) {
			return true, false, "recipient not found (cached)"
		}
	}

	// Check if recipient is in "blocked" cache
	if expiry, exists := dt.recipientBlocked[email]; exists {
		if now.Before(expiry) {
			return true, true, "recipient blocked by destination (cached)"
		}
	}

	return false, false, ""
}

// Stop gracefully shuts down the distributed tracker
func (dt *DistributedTracker) Stop() {
	dt.logger.Info("Stopping distributed connection tracker")
	dt.cancel()
	dt.wg.Wait()
}

// TryAcquire attempts to acquire a connection slot with cluster-wide limit checking
func (dt *DistributedTracker) TryAcquire(remoteAddr string) error {
	// Step 1: Check local limit (fast path)
	if err := dt.local.TryAcquire(remoteAddr); err != nil {
		return err
	}

	// Step 2: Check estimated global limit (if configured)
	if dt.globalMaxPerIP > 0 {
		globalCount := dt.estimateGlobalCount(remoteAddr)
		if globalCount > dt.globalMaxPerIP {
			// Rollback local acquisition
			dt.local.Release(remoteAddr)
			return fmt.Errorf("estimated global connections per IP limit reached (%d)", dt.globalMaxPerIP)
		}
	}

	return nil
}

// Release releases a connection slot
func (dt *DistributedTracker) Release(remoteAddr string) {
	dt.local.Release(remoteAddr)
}

// estimateGlobalCount calculates the estimated global connection count for an IP
func (dt *DistributedTracker) estimateGlobalCount(remoteAddr string) int {
	host, _, _ := parseAddr(remoteAddr)

	// Get local count
	_, _, perIP := dt.local.GetStats()
	localCount := perIP[host]

	// Add peer counts
	dt.peerMu.RLock()
	defer dt.peerMu.RUnlock()

	peerTotal := 0
	for _, peerState := range dt.peerConnections {
		// Ignore stale peer data (older than 30 seconds)
		if time.Since(peerState.UpdatedAt) > 30*time.Second {
			continue
		}
		if peerState.Connections != nil {
			peerTotal += peerState.Connections[host]
		}
	}

	return localCount + peerTotal
}

// gossipLoop periodically broadcasts connection state to peers
func (dt *DistributedTracker) gossipLoop() {
	defer dt.wg.Done()

	ticker := time.NewTicker(dt.gossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dt.ctx.Done():
			return
		case <-ticker.C:
			dt.broadcastToPeers()
		}
	}
}

// broadcastToPeers sends current connection state to all peers
func (dt *DistributedTracker) broadcastToPeers() {
	snapshot := dt.createSnapshot()

	for _, peer := range dt.peers {
		go func(peerURL string) {
			if err := dt.sendToPeer(peerURL, snapshot); err != nil {
				dt.logger.Debug("Failed to send gossip to peer",
					zap.String("peer", peerURL),
					zap.Error(err))
			}
		}(peer)
	}
}

// createSnapshot creates a snapshot of current connection state
func (dt *DistributedTracker) createSnapshot() *ConnectionSnapshot {
	total, _, perIP := dt.local.GetStats()

	// Copy recipient cache
	dt.recipientMu.RLock()
	notFoundCopy := make(map[string]time.Time, len(dt.recipientNotFound))
	for k, v := range dt.recipientNotFound {
		notFoundCopy[k] = v
	}
	blockedCopy := make(map[string]time.Time, len(dt.recipientBlocked))
	for k, v := range dt.recipientBlocked {
		blockedCopy[k] = v
	}
	dt.recipientMu.RUnlock()

	var recipientCache *RecipientCacheSnapshot
	if len(notFoundCopy) > 0 || len(blockedCopy) > 0 {
		recipientCache = &RecipientCacheSnapshot{
			NotFound: notFoundCopy,
			Blocked:  blockedCopy,
		}
	}

	return &ConnectionSnapshot{
		Hostname:       dt.hostname,
		Timestamp:      time.Now(),
		Connections:    perIP,
		TotalCount:     total,
		Version:        1, // Simple versioning
		RecipientCache: recipientCache,
	}
}

// sendToPeer sends a snapshot to a peer server
func (dt *DistributedTracker) sendToPeer(peerURL string, snapshot *ConnectionSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	endpoint := peerURL + "/api/connections/sync"
	client := &http.Client{
		Timeout: 5 * time.Second, // Prevent gossip from hanging
	}
	req, err := http.NewRequestWithContext(dt.ctx, "POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request for peer: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post to peer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned non-OK status: %d", resp.StatusCode)
	}

	return nil
}

// HandlePeerUpdate processes a connection snapshot from a peer
func (dt *DistributedTracker) HandlePeerUpdate(snapshot *ConnectionSnapshot) {
	dt.peerMu.Lock()
	// Convert to PeerConnectionState
	peerState := &PeerConnectionState{
		Hostname:    snapshot.Hostname,
		Timestamp:   snapshot.Timestamp,
		Connections: snapshot.Connections,
		TotalCount:  snapshot.TotalCount,
		UpdatedAt:   time.Now(),
	}

	// Update peer state
	dt.peerConnections[snapshot.Hostname] = peerState
	dt.peerMu.Unlock()

	// Merge recipient cache from peer
	if snapshot.RecipientCache != nil {
		dt.mergeRecipientCache(snapshot.RecipientCache)
	}

	dt.logger.Debug("Received peer update",
		zap.String("peer", snapshot.Hostname),
		zap.Int("total_connections", snapshot.TotalCount),
		zap.Int("unique_ips", len(snapshot.Connections)))
}

// mergeRecipientCache merges recipient cache from a peer snapshot
func (dt *DistributedTracker) mergeRecipientCache(peerCache *RecipientCacheSnapshot) {
	dt.recipientMu.Lock()
	defer dt.recipientMu.Unlock()

	now := time.Now()
	merged := 0

	// Merge "not found" entries
	for email, expiry := range peerCache.NotFound {
		// Only merge if not expired and (not exists locally OR peer's expiry is later)
		if now.Before(expiry) {
			if localExpiry, exists := dt.recipientNotFound[email]; !exists || expiry.After(localExpiry) {
				dt.recipientNotFound[email] = expiry
				merged++
			}
		}
	}

	// Merge "blocked" entries
	for email, expiry := range peerCache.Blocked {
		// Only merge if not expired and (not exists locally OR peer's expiry is later)
		if now.Before(expiry) {
			if localExpiry, exists := dt.recipientBlocked[email]; !exists || expiry.After(localExpiry) {
				dt.recipientBlocked[email] = expiry
				merged++
			}
		}
	}

	if merged > 0 {
		dt.logger.Debug("Merged recipient cache from peer",
			zap.Int("entries_merged", merged),
			zap.Int("total_not_found", len(dt.recipientNotFound)),
			zap.Int("total_blocked", len(dt.recipientBlocked)))
	}
}

// s3SyncLoop periodically syncs connection state to/from S3
func (dt *DistributedTracker) s3SyncLoop() {
	defer dt.wg.Done()

	ticker := time.NewTicker(dt.s3SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dt.ctx.Done():
			return
		case <-ticker.C:
			dt.syncWithS3()
		}
	}
}

// syncWithS3 exports local state and imports peer states from S3
func (dt *DistributedTracker) syncWithS3() {
	// Check if S3 client is available
	if dt.s3Client == nil {
		dt.logger.Debug("S3 client not initialized, skipping S3 sync")
		return
	}

	// Export our state
	if err := dt.exportToS3(); err != nil {
		dt.logger.Error("Failed to export to S3", zap.Error(err))
	}

	// Import peer states
	if err := dt.importFromS3(); err != nil {
		dt.logger.Error("Failed to import from S3", zap.Error(err))
	}
}

// exportToS3 exports current connection state to S3
func (dt *DistributedTracker) exportToS3() error {
	if dt.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	snapshot := dt.createSnapshot()

	// Marshal to JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	// Compress
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	if _, err := gzWriter.Write(data); err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip: %w", err)
	}

	// Upload to S3
	objectName := path.Join(dt.s3Prefix, "connections", fmt.Sprintf("%s.json.gz", dt.hostname))
	_, err = dt.s3Client.PutObject(
		context.Background(),
		dt.s3Bucket,
		objectName,
		&buf,
		int64(buf.Len()),
		minio.PutObjectOptions{
			ContentType: "application/gzip",
		},
	)
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	dt.logger.Debug("Exported connection state to S3",
		zap.String("object", objectName),
		zap.Int("size", buf.Len()))

	return nil
}

// importFromS3 imports peer connection states from S3
func (dt *DistributedTracker) importFromS3() error {
	if dt.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	prefix := path.Join(dt.s3Prefix, "connections") + "/"

	// List all connection files
	objectCh := dt.s3Client.ListObjects(
		context.Background(),
		dt.s3Bucket,
		minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: false,
		},
	)

	for object := range objectCh {
		if object.Err != nil {
			dt.logger.Error("Error listing S3 objects", zap.Error(object.Err))
			continue
		}

		// Skip our own file
		expectedName := fmt.Sprintf("%s.json.gz", dt.hostname)
		if path.Base(object.Key) == expectedName {
			continue
		}

		// Download and process peer state
		if err := dt.downloadPeerState(object.Key); err != nil {
			dt.logger.Debug("Failed to download peer state",
				zap.String("key", object.Key),
				zap.Error(err))
		}
	}

	return nil
}

// downloadPeerState downloads and processes a peer's connection state from S3
func (dt *DistributedTracker) downloadPeerState(objectKey string) error {
	if dt.s3Client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	obj, err := dt.s3Client.GetObject(
		context.Background(),
		dt.s3Bucket,
		objectKey,
		minio.GetObjectOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// Decompress
	gzReader, err := gzip.NewReader(obj)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Read and unmarshal
	data, err := io.ReadAll(gzReader)
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}

	var snapshot ConnectionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}

	// Process the update (same as P2P gossip)
	dt.HandlePeerUpdate(&snapshot)

	return nil
}

// GetGlobalStats returns connection statistics across the cluster
func (dt *DistributedTracker) GetGlobalStats() (localTotal, estimatedGlobalTotal, uniqueIPs int, topIPs map[string]int) {
	localTotal, _, localPerIP := dt.local.GetStats()

	dt.peerMu.RLock()
	defer dt.peerMu.RUnlock()

	// Aggregate all IPs across cluster
	globalPerIP := make(map[string]int)
	for ip, count := range localPerIP {
		globalPerIP[ip] = count
	}

	peerTotal := 0
	for _, peerState := range dt.peerConnections {
		// Skip stale peers
		if time.Since(peerState.UpdatedAt) > 30*time.Second {
			continue
		}

		peerTotal += peerState.TotalCount
		for ip, count := range peerState.Connections {
			globalPerIP[ip] += count
		}
	}

	return localTotal, localTotal + peerTotal, len(globalPerIP), globalPerIP
}

// HTTPHandler returns an HTTP handler for receiving peer gossip updates
func (dt *DistributedTracker) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var snapshot ConnectionSnapshot
		if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
			dt.logger.Error("Failed to decode peer snapshot", zap.Error(err))
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Process the update
		dt.HandlePeerUpdate(&snapshot)

		// Respond with our current state
		mySnapshot := dt.createSnapshot()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mySnapshot)
	}
}

// parseAddr extracts IP from "IP:port" format
func parseAddr(addr string) (string, string, error) {
	// Try to parse as IP:port
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// If no port, assume it's just an IP
		return addr, "", nil
	}
	return host, port, nil
}

// Name returns the name of this health checker
func (dt *DistributedTracker) Name() string {
	return "distributed_connections"
}

// CheckHealth reports the health status of the distributed connection tracker
func (dt *DistributedTracker) CheckHealth() health.ComponentStatus {
	localTotal, globalTotal, uniqueIPs, _ := dt.GetGlobalStats()

	// Count active peers
	dt.peerMu.RLock()
	activePeers := 0
	stalePeers := 0
	for _, peerState := range dt.peerConnections {
		if time.Since(peerState.UpdatedAt) > 30*time.Second {
			stalePeers++
		} else {
			activePeers++
		}
	}
	dt.peerMu.RUnlock()

	// Determine status
	status := "healthy"
	if len(dt.peers) > 0 && activePeers == 0 {
		// We have peers configured but none are responding
		status = "degraded"
	}

	return health.ComponentStatus{
		Status: status,
		Details: map[string]any{
			"local_connections":  localTotal,
			"global_connections": globalTotal,
			"unique_ips":         uniqueIPs,
			"configured_peers":   len(dt.peers),
			"active_peers":       activePeers,
			"stale_peers":        stalePeers,
			"global_max_per_ip":  dt.globalMaxPerIP,
		},
	}
}

// FlushCache clears the recipient cache and returns the number of flushed entries.
// This method implements the health.CacheFlusher interface.
func (dt *DistributedTracker) FlushCache() map[string]int {
	dt.recipientMu.Lock()
	defer dt.recipientMu.Unlock()

	flushedCounts := make(map[string]int)

	flushedCounts["recipient_not_found"] = len(dt.recipientNotFound)
	flushedCounts["recipient_blocked"] = len(dt.recipientBlocked)

	// Re-initialize the maps to clear them
	dt.recipientNotFound = make(map[string]time.Time)
	dt.recipientBlocked = make(map[string]time.Time)

	dt.logger.Info("Recipient cache flushed via API",
		zap.Int("not_found_flushed", flushedCounts["recipient_not_found"]),
		zap.Int("blocked_flushed", flushedCounts["recipient_blocked"]))

	return flushedCounts
}
