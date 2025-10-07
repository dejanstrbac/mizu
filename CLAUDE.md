# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mizu is a high-performance, distributed SMTP relay server written in Go that accepts emails via SMTP and synchronously forwards them to a configured HTTP backend. It's designed for production use with comprehensive security, anti-spam features, and distributed coordination.

**Core Principle**: Zero message loss - SMTP `250 OK` is sent ONLY after receiving HTTP `200`/`202` from the backend. No internal message queue; delivery is synchronous.

## Build & Run Commands

```bash
# Build both binaries
make build                    # Builds mizu-server and mizu-admin
make mizu-server             # Build only the server
make mizu-admin              # Build only the admin CLI

# Run tests
make test                    # Run all tests
go test -race ./...          # Run with race detector
go test ./pkg/smtp -run E2E -v  # Run SMTP integration tests

# Generate documentation
make docs                    # Generate package documentation in docs/generated/
make godoc                   # Start godoc server at http://localhost:6060

# Generate example config
./mizu-server generate-config > config.toml.example

# Run server
./mizu-server --config config.toml     # Production mode
./mizu-server --local                  # Local dev mode (no TLS, dumps to terminal)

# Admin CLI operations
./mizu-admin health                    # Check server health
./mizu-admin stats                     # View statistics
./mizu-admin blocked-ips               # List blocked IPs
./mizu-admin flush-cache               # Flush caches
```

## API Documentation

The codebase is fully documented using Go documentation (godoc). View the complete API documentation:

```bash
# Generate documentation files
make docs

# Or start an interactive documentation server
make godoc
# Then visit: http://localhost:6060/pkg/migadu/mizu/
```

Key packages:
- **[pkg/validation](pkg/validation/)**: Email authentication (SPF, DKIM, DMARC, ARC)
- **[pkg/smtp](pkg/smtp/)**: SMTP protocol implementation
- **[pkg/config](pkg/config/)**: Configuration management
- **[pkg/poster](pkg/poster/)**: HTTP delivery and circuit breaker
- **[pkg/cluster](pkg/cluster/)**: Distributed coordination
- **[pkg/stats](pkg/stats/)**: Reputation tracking

## Architecture & Key Concepts

### Multi-Binary Structure

- **`cmd/mizu-server`**: Main SMTP relay server
- **`cmd/mizu-admin`**: CLI tool for operational tasks (health checks, stats viewing)

### Core Components

1. **SMTP Server** ([pkg/smtp/](pkg/smtp/))
   - `Backend`: Main server implementation, creates sessions
   - `Session`: Per-connection handler with complete email validation pipeline
   - Entry point: `Backend.NewSession()` → creates `Session` for each connection
   - Message flow: Connection → rDNS → DNSBL → SPF/DKIM/DMARC/ARC → Header validation → HTTP POST to backend

2. **Distributed Coordination** ([pkg/cluster/](pkg/cluster/))
   - Uses **hashicorp/memberlist** for P2P gossip protocol
   - Supports leader election for TLS certificate management
   - Shares connection state and rate limits across cluster nodes
   - Message types: `MessageTypeConnectionState`, `MessageTypeRateLimit`

3. **Connection Tracking & DoS Protection** ([pkg/smtp/](pkg/smtp/))
   - `ConnectionTracker`: Local per-IP and global connection limits
   - `DistributedTracker`: Cluster-wide connection tracking via gossip + S3 sync
   - `RateLimiter`: Multi-dimensional rate limiting (IP, FROM, FROM_DOMAIN, TO, TO_DOMAIN) with optional gossip

4. **Reputation & Stats** ([pkg/stats/](pkg/stats/))
   - `Manager`: Tracks IP and domain reputation scores
   - Event-driven architecture with async processing (ring buffer, worker goroutines)
   - Syncs reputation data across cluster via S3
   - LRU-based eviction for memory efficiency (configurable max entries)

5. **Circuit Breaker** ([pkg/poster/](pkg/poster/))
   - Protects backend from being overwhelmed during failures
   - States: Closed → Open → HalfOpen
   - Configurable failure threshold, timeout, and success threshold

6. **TLS Certificate Management** ([pkg/tls/](pkg/tls/))
   - `Manager`: Handles autocert with Let's Encrypt (TLS-ALPN-01 and HTTP-01 challenges)
   - Distributed mode: Only cluster leader obtains certificates, stores in S3
   - Uses S3 for certificate storage across instances
   - Alternative: certmagic library for on-demand certificates

7. **Email Validation** ([pkg/validation/](pkg/validation/))
   - SPF validation (checks sender IP authorization)
   - DKIM validation (verifies email signature)
   - DMARC validation (checks alignment + policy enforcement)
   - ARC validation and signing (Authenticated Received Chain - preserves authentication through forwarding)
   - MX record validation for sender domains

### Configuration System

- TOML-based configuration ([pkg/config/](pkg/config/))
- `Config` struct in [pkg/config/types.go](pkg/config/types.go) defines all settings
- Environment variables supported for secrets: `DESTINATION_API_KEY`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `HEALTH_PASSWORD`, `CLUSTER_SECRET_KEY`
- Default values defined in `DefaultConfig()`

### Storage Backend Configuration

Mizu supports two storage backends for TLS certificates and stats synchronization:

1. **S3 (default)** - For production clusters
   ```toml
   [storage]
   backend = "s3"
   endpoint = "s3.amazonaws.com"
   bucket = "mizu-storage"
   prefix = "certs/"
   access_key_id = "..." # Or via S3_ACCESS_KEY_ID env var
   secret_access_key = "..." # Or via S3_SECRET_ACCESS_KEY env var
   region = "us-east-1"
   ```

2. **Filesystem** - For single-node deployments
   ```toml
   [storage]
   backend = "filesystem"
   filesystem_path = "/var/lib/mizu/storage"
   ```

**When to use filesystem backend:**
- Single-node deployments without clustering
- Development/testing environments
- Scenarios where S3 is not available or desired
- Lower operational complexity

**Implementation:** [pkg/storage/](pkg/storage/) provides a `Backend` interface with both `S3Backend` and `FilesystemBackend` implementations

### Distributed Features Require Cluster Mode

Several features require `cluster.enabled=true`:
- Distributed connection tracking (`smtp.distributed.enabled`)
- Rate limit gossip (`smtp.rate_limit.gossip_enabled`)
- TLS autocert with leader election
- Reputation stats sync (uses S3 + memberlist)

## Testing Patterns

- **Unit tests**: Standard Go tests in `*_test.go` files
- **Integration tests**: E2E tests in `pkg/smtp/*_e2e_test.go` (use `-run E2E` to run)
- **Benchmarks**: DNS and rate limiter benchmarks exist
- **Mock testing**: Uses interfaces for testability (e.g., `poster.HTTPClient`)

### Manual SMTP Testing

```bash
# Start server in local mode
./mizu-server --local &

# Test with telnet
telnet localhost 25
> EHLO test.local
> MAIL FROM:<sender@example.com>
> RCPT TO:<recipient@example.com>
> DATA
> Subject: Test
>
> This is a test.
> .
> QUIT
```

## Important Implementation Details

### Panic Recovery & Graceful Shutdown

- **ALL goroutines MUST use `logging.SafeGo()`** ([pkg/logging/recovery.go](pkg/logging/recovery.go))
  - Prevents WaitGroup leaks on panics
  - Logs stack traces
  - Example: `logging.SafeGo(logger, "goroutine-name", func() { ... })`

- **Graceful shutdown** ([cmd/mizu-server/main.go](cmd/mizu-server/main.go:655-698)):
  1. Stop accepting new connections (close `ShutdownChan`, close listener)
  2. Wait for active sessions with timeout (`ActiveSessionsWg`)
  3. Stop stats manager
  4. Stop health server

### DNS Resolution

- Custom DNS resolvers supported via `dns.resolvers` config
- Round-robin + failover + caching implemented in [pkg/dns/resolver.go](pkg/dns/resolver.go)
- Default uses OS resolver
- Caching wrapper: [pkg/dns/caching_wrapper.go](pkg/dns/caching_wrapper.go)

### Stats System Architecture

- **Event-driven**: Components emit events → ring buffer → async workers → stats manager
- **Vector clocks** ([pkg/cluster/vectorclock.go](pkg/cluster/vectorclock.go)) for distributed state merging
- **S3 export/import** for cross-cluster synchronization
- **LRU eviction** when limits exceeded

### Rate Limiting

- Multi-dimensional: Can combine keys (IP, FROM, FROM_DOMAIN, TO, TO_DOMAIN)
- Sliding window algorithm
- Gossip-based cluster-wide enforcement (optional)
- Configured via `smtp.rate_limit.dimensions` array

## Development Workflow

1. **Making changes to core SMTP logic**: Edit [pkg/smtp/server.go](pkg/smtp/server.go), run E2E tests
2. **Adding new configuration options**:
   - Add field to `Config` struct in [pkg/config/types.go](pkg/config/types.go)
   - Update `DefaultConfig()` with sensible default
   - Update validation in `config.Validate()` if needed
3. **Modifying validation logic**: Edit files in [pkg/validation/](pkg/validation/)
4. **Cluster/gossip changes**: Work in [pkg/cluster/](pkg/cluster/)
5. **Adding metrics**: Use prometheus client from [pkg/metrics/](pkg/metrics/)

## Common Gotchas

1. **Distributed features require cluster mode**: Always check `cluster.enabled` before enabling distributed features
2. **Graceful shutdown timeout**: Default 60s, configurable via `smtp.shutdown_timeout_seconds`
3. **S3 is required for production**: Used for certs AND stats sync (if enabled)
4. **TLS minimum version**: Only TLS 1.2 and 1.3 supported (1.0/1.1 deprecated)
5. **Autocert leader election**: Only works with cluster mode enabled
6. **Rate limit dimensions**: Must specify at least one dimension if rate limiting enabled

## Module Information

- Module path: `migadu/mizu`
- Go version: 1.24.1
- Key dependencies:
  - `emersion/go-smtp` - SMTP protocol implementation
  - `emersion/go-msgauth` - SPF/DKIM/DMARC validation
  - `hashicorp/memberlist` - Distributed cluster coordination
  - `caddyserver/certmagic` - TLS certificate automation
  - `minio/minio-go` - S3 client
  - `uber-go/zap` - Structured logging
  - `prometheus/client_golang` - Metrics

## Version Information

Build-time version injection via linker flags in Makefile:
- `VERSION`, `COMMIT`, `DATE` variables set in both `cmd/mizu-server/main.go` and `cmd/mizu-admin/main.go`
- Access via: `./mizu-server --version` or `./mizu-admin --version`
