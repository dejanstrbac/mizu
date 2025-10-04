package stats

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"
)

// SyncFromS3 downloads and merges stats from other servers
func (m *Manager) SyncFromS3(ctx context.Context, s3Client *minio.Client, bucket, prefix string) error {
	if !m.enabled || !m.syncEnabled || len(m.syncServers) == 0 {
		return nil
	}

	var totalMerged int
	for _, serverHostname := range m.syncServers {
		merged, err := m.syncFromServer(ctx, s3Client, bucket, prefix, serverHostname)
		if err != nil {
			m.logger.Error("Failed to sync from server",
				zap.String("server", serverHostname),
				zap.Error(err))
			// Continue with other servers even if one fails
			continue
		}
		totalMerged += merged
	}

	if totalMerged > 0 {
		m.logger.Debug("Completed stats sync",
			zap.Int("servers", len(m.syncServers)),
			zap.Int("total_entries_merged", totalMerged))
	}

	return nil
}

// syncFromServer syncs stats from a single server
func (m *Manager) syncFromServer(ctx context.Context, s3Client *minio.Client, bucket, prefix, serverHostname string) (int, error) {
	objectName := path.Join(prefix, "stats", fmt.Sprintf("%s.json.gz", serverHostname))

	// Check if we've already synced this version
	m.lastSyncMu.RLock()
	lastSync, exists := m.lastSync[serverHostname]
	lastAttempt, attemptExists := m.lastSyncAttempt[serverHostname]
	m.lastSyncMu.RUnlock()

	// Mark that we are attempting to sync now
	m.lastSyncMu.Lock()
	m.lastSyncAttempt[serverHostname] = time.Now()
	m.lastSyncMu.Unlock()

	// Check last modified time
	objInfo, err := s3Client.StatObject(ctx, bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		// Check if the error is because the object doesn't exist
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			// If we have synced before and it's been a while, the peer might be gone.
			if attemptExists && time.Since(lastAttempt) > stalePeerTimeout {
				m.logger.Warn("Peer stats file not found for stale peer, skipping for now", zap.String("peer", serverHostname), zap.String("object", objectName))
				return 0, nil // Not an error, just a stale peer
			}
		}
		return 0, fmt.Errorf("failed to stat object: %w", err)
	}

	if exists && !objInfo.LastModified.After(lastSync) {
		// No changes since last sync
		return 0, nil
	}

	// Download the stats file
	obj, err := s3Client.GetObject(ctx, bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// Read and decompress
	gzReader, err := gzip.NewReader(obj)
	if err != nil {
		return 0, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gzReader); err != nil {
		return 0, fmt.Errorf("failed to decompress: %w", err)
	}

	// Parse JSON
	var remoteStats StatsExport
	if err := json.Unmarshal(buf.Bytes(), &remoteStats); err != nil {
		return 0, fmt.Errorf("failed to unmarshal stats: %w", err)
	}

	// Merge the stats
	merged := m.mergeStats(&remoteStats)

	// Update last sync time
	m.lastSyncMu.Lock()
	m.lastSync[serverHostname] = objInfo.LastModified
	m.lastSyncMu.Unlock()

	m.logger.Debug("Synced stats from server",
		zap.String("server", serverHostname),
		zap.Time("last_modified", objInfo.LastModified),
		zap.Int("ips", len(remoteStats.IPs)),
		zap.Int("domains", len(remoteStats.Domains)),
		zap.Int("merged", merged))

	return merged, nil
}

// StartSyncLoop starts the periodic sync from other servers
func (m *Manager) StartSyncLoop(ctx context.Context, s3Client *minio.Client, bucket, prefix string, interval time.Duration) {
	if !m.enabled || !m.syncEnabled || len(m.syncServers) == 0 {
		m.logger.Info("Stats sync disabled or no servers to sync from")
		return
	}

	m.logger.Info("Starting stats sync loop",
		zap.Duration("interval", interval),
		zap.Int("servers", len(m.syncServers)))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Sync immediately on start
	if err := m.SyncFromS3(ctx, s3Client, bucket, prefix); err != nil {
		m.logger.Error("Failed to sync stats", zap.Error(err))
	}

	for {
		select {
		case <-ticker.C:
			if err := m.SyncFromS3(ctx, s3Client, bucket, prefix); err != nil {
				m.logger.Error("Failed to sync stats", zap.Error(err))
				// Continue running even if sync fails
			}
		case <-ctx.Done():
			m.logger.Info("Stats sync loop stopped")
			return
		}
	}
}
