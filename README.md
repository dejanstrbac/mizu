# Mizu - High-Performance SMTP Relay Server

**Mizu** (水 - Japanese for "water") is a production-ready SMTP relay server that accepts incoming emails and synchronously forwards them via HTTP POST to your backend endpoint. Designed for reliability and security, Mizu ensures **zero message loss** by only accepting messages after successful delivery confirmation.

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## 🎯 Core Design Principles

1. **Zero Message Loss**: SMTP 250 OK is ONLY sent after HTTP 200/202 from destination
2. **Synchronous Delivery**: No queues - messages delivered during SMTP session
3. **Production Ready**: Comprehensive error handling, panic recovery, graceful shutdown
4. **Distributed Architecture**: Built for multi-instance deployments from the ground up
5. **Security First**: Mandatory TLS, comprehensive anti-spam validation, reputation tracking

## ✨ Key Features

### 🔒 Security & Anti-Spam

- **Mandatory STARTTLS** with automatic Let's Encrypt certificates
- **SPF & DMARC Validation** with alignment checking
- **DKIM Validation** using emersion/go-msgauth
- **DNS Blacklists** (RBL/DNSBL) support (Spamhaus, etc.)
- **Reverse DNS Validation** with configurable timeout
- **Sender MX Validation** - reject domains without MX records
- **Header Validation** - From, Date, Message-ID required
- **Null Sender Rejection** - blocks empty senders `<>`
- **Custom DNS Resolvers** - use Cloudflare/Google DNS globally

### 🛡️ DoS Protection & Rate Limiting

- **Connection Limits** - Global and per-IP concurrent connection limits
- **Rate Limiting** - Sliding window algorithm (connections/minute per IP)
- **Distributed Rate Limiting** - Gossip protocol shares state across cluster
- **Distributed Connection Tracking** - P2P gossip + S3 sync for cluster-wide limits
- **Circuit Breaker Pattern** - Protects against destination failures with auto-recovery

### 📊 Reputation & Intelligence

- **Real-time Reputation Tracking** - IP and domain scoring
- **Distributed Stats Sync** - Share reputation data across cluster via S3
- **Automatic Blocking** - Bad actors blocked based on reputation
- **Recipient Caching** - Cache 404/403 responses cluster-wide (15min TTL)
- **LRU Eviction** - Memory-safe with configurable max entries
- **DMARC Failure Tracking** - Identify spoofing attempts

### 🔄 High Availability

- **Distributed Deployment** - Multi-instance with peer-to-peer coordination
- **Graceful Shutdown** - Waits for active sessions (configurable timeout)
- **Health Monitoring** - HTTP health checks with component status
- **S3 Certificate Storage** - Distributed cert management with locking
- **Circuit Breaker** - Fail fast when destination is down (auto-recovery)
- **Panic Recovery** - WaitGroup leak prevention with stack traces

### 🔧 Operational Excellence

- **Structured Logging** - JSON or text format with trace IDs
- **Session Monitoring** - Real-time active session count tracking
- **Admin CLI** - `mizu-admin` for health, stats, blocked IPs, cache flush
- **HTTP Basic Auth** - Protect health/API endpoints
- **Comprehensive Testing** - 100+ tests including integration & E2E

## 🏗️ Architecture

```
┌─────────────┐
│   Internet  │
└──────┬──────┘
       │ SMTP (port 25)
       │ STARTTLS
       ▼
┌─────────────────────────────────────────┐
│         Mizu SMTP Relay Cluster         │
│                                         │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐ │
│  │ Mizu #1 │  │ Mizu #2 │  │ Mizu #3 │ │
│  └─────────┘  └─────────┘  └─────────┘ │
│       │            │            │       │
│       └────────────┼────────────┘       │
│              P2P Gossip                 │
│         (connections, rate limits)      │
└─────────────────┬───────────────────────┘
                  │
        ┌─────────┴─────────┐
        │                   │
        ▼                   ▼
   ┌─────────┐         ┌─────────┐
   │   S3    │         │  HTTP   │
   │  Certs  │         │ Backend │
   │  Stats  │         │Endpoint │
   └─────────┘         └─────────┘
```

### Message Flow

1. **SMTP Reception** → Accept connection, check rate limits
2. **Security Validation** → rDNS, blacklists, SPF, DMARC
3. **Content Validation** → Headers, size, duplicate detection
4. **Synchronous Delivery** → HTTP POST to destination (with retries)
5. **SMTP Response** → 250 OK **only if** HTTP 200/202
6. **Stats Recording** → Update reputation, sync to cluster

## 📦 Quick Start

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/mizu.git
cd mizu

# Build
go build -o mizu ./cmd/mizu

# Generate example config
./mizu generate-config > config.toml

# Edit config.toml with your settings
nano config.toml
```

### Minimal Configuration

```toml
[smtp]
domain = "mail.example.com"

[destination]
url = "https://your-backend.example.com/email"
api_key = "your-api-key"

[tls]
email = "admin@example.com"

[s3]
bucket = "your-s3-bucket"
access_key_id = "YOUR_KEY"      # Or use env: S3_ACCESS_KEY_ID
secret_access_key = "YOUR_SECRET"  # Or use env: S3_SECRET_ACCESS_KEY
```

### Run

```bash
# Production mode
./mizu

# Local development mode (no TLS, dumps to terminal)
./mizu --local

# With custom config
./mizu --config /path/to/config.toml
```

## 📋 Configuration Reference

### Complete Example

See [config.toml.example](config.toml.example) for a fully documented configuration file.

### Key Sections

#### DNS Configuration (Global)
```toml
[dns]
servers = ["1.1.1.1:53", "1.0.0.1:53"]  # Cloudflare DNS
timeout = "5s"
```

#### Cluster Configuration
```toml
[cluster]
enabled = true
hostname = "smtp-1.example.com"
peer_port = ":8080"
peers = ["smtp-2.example.com", "smtp-3.example.com"]
```

#### Rate Limiting
```toml
[smtp.rate_limit]
enabled = true
connections_per_minute = 60
window_size = "1m"
gossip_enabled = true  # Share state with cluster
gossip_interval = "5s"
```

#### Distributed Connection Tracking
```toml
[smtp.distributed]
enabled = true
global_max_per_ip = 30  # Cluster-wide limit
gossip_interval = "5s"
recipient_cache_ttl = "15m"
```

#### Circuit Breaker
```toml
[destination.circuit_breaker]
enabled = true
failure_threshold = 5
timeout = "30s"
```

#### Health Check API
```toml
[health]
enabled = true
listen_addr = ":8080"
username = "admin"
password = "changeme"
```

### Environment Variables

For security, use environment variables for secrets:

```bash
export S3_ACCESS_KEY_ID="your-key"
export S3_SECRET_ACCESS_KEY="your-secret"
export DESTINATION_API_KEY="your-api-key"
```

## 🔧 Admin CLI

Mizu includes a command-line tool for operational tasks:

```bash
# Build admin CLI
go build -o mizu-admin ./cmd/mizu-admin

# Check health
./mizu-admin health

# View stats
./mizu-admin stats

# List blocked IPs
./mizu-admin blocked-ips

# Flush recipient cache (distributed mode)
./mizu-admin flush-cache

# With authentication
./mizu-admin health -username admin -password changeme
```

## 🧪 Testing

```bash
# Run all tests
make test

# Run with race detection
go test -race ./...

# Run specific package tests
go test ./pkg/smtp -v

# Run integration tests
go test ./pkg/smtp -run E2E -v

# Test with telnet (local mode)
./mizu --local &
telnet localhost 25
```

Example SMTP session:
```
EHLO test.local
MAIL FROM:<sender@example.com>
RCPT TO:<recipient@example.com>
DATA
Subject: Test Email

This is a test.
.
QUIT
```

## 📊 Monitoring & Observability

### Health Check Endpoint

```bash
# Basic health check
curl http://localhost:8080/health

# With authentication
curl -u admin:changeme http://localhost:8080/health

# View stats
curl -u admin:changeme http://localhost:8080/api/stats

# Flush cache
curl -X POST -u admin:changeme http://localhost:8080/api/flush-cache
```

### Logging

```toml
log_format = "json"  # or "text"
```

JSON logs include:
- `trace_id` - Unique per session for correlation
- `remote_addr` - Client IP:port
- `from` / `to` - Envelope addresses
- Structured fields for all events

### Metrics to Monitor

- Active session count (logged every 30s if > 0)
- Circuit breaker state transitions
- Rate limit exceeded events
- DMARC/SPF failures
- Reputation scores
- Connection limit utilization

## 🚀 Production Deployment

### Single Instance

```bash
# systemd service
sudo systemctl start mizu
sudo systemctl enable mizu
```

### Multi-Instance Cluster (Recommended)

```bash
# Deploy 3+ instances with:
# - Shared S3 bucket for certs & stats
# - P2P gossip for real-time coordination
# - DNS round-robin for load distribution

# Instance 1
./mizu --config config-server1.toml

# Instance 2
./mizu --config config-server2.toml

# Instance 3
./mizu --config config-server3.toml
```

### Production Checklist

- **Pre-deployment**: Review all config sections, set environment variables for secrets
- **Security hardening**: Enable all validation, configure blacklists, set connection limits
- **Performance tuning**: Adjust rate limits, circuit breaker thresholds based on load
- **Monitoring setup**: Enable health endpoints with authentication, review logs regularly
- **Emergency procedures**: Have rollback plan, monitor circuit breaker state
- **Scaling**: Deploy 3+ instances with cluster mode, use DNS round-robin

## 🏛️ Architecture Details

### Package Structure

```
.
├── cmd/
│   ├── mizu/              # Main SMTP server
│   └── mizu-admin/        # Admin CLI tool
├── pkg/
│   ├── blacklist/         # DNS blacklist checking
│   ├── config/            # Configuration loading
│   ├── health/            # Health check HTTP server
│   ├── logging/           # Structured logging
│   ├── poster/            # HTTP delivery + circuit breaker
│   ├── smtp/              # SMTP server implementation
│   │   ├── server.go      # Core SMTP logic
│   │   ├── connection_tracker.go
│   │   ├── distributed_tracker.go
│   │   ├── rate_limiter.go
│   │   └── *_test.go      # Comprehensive tests
│   ├── stats/             # Reputation tracking
│   ├── storage/           # S3 certificate storage
│   └── validation/        # SPF, DMARC, DKIM validation
└── config.toml.example    # Example configuration
```

### Key Design Decisions

1. **Synchronous Delivery** - No queue needed, SMTP session blocks until HTTP delivery
2. **No Message Loss** - SMTP 250 OK only after HTTP 200/202 (verified in audit)
3. **Distributed by Default** - P2P gossip + S3 sync for cluster coordination
4. **Circuit Breaker** - Protects both server and destination from cascading failures
5. **Panic Recovery** - Defense-in-depth prevents WaitGroup deadlocks

## 🐛 Known Limitations

- **No Message Queue** - Synchronous delivery blocks SMTP session (by design)
- **Single Destination** - One HTTP endpoint per instance (use multiple instances for multiple backends)
- **No Prometheus Metrics** - Only JSON API available (planned enhancement)
- **Per-Instance Connection Limits** - Not cluster-wide unless distributed tracking enabled

## 🤝 Contributing

Contributions welcome! Please:

1. Review architecture details in this README
2. Run tests: `make test` and `go test -race ./...`
3. Follow existing code patterns
4. Add tests for new features
5. Update documentation

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

## 🙏 Credits

Built with:
- [certmagic](https://github.com/caddyserver/certmagic) - Automatic HTTPS
- [go-smtp](https://github.com/emersion/go-smtp) - SMTP server
- [go-msgauth](https://github.com/emersion/go-msgauth) - DMARC/DKIM/SPF
- [minio-go](https://github.com/minio/minio-go) - S3 client
- [zap](https://github.com/uber-go/zap) - Structured logging

## 🔗 Related Projects

- [mizu-worker](https://github.com/yourusername/mizu-worker) - Example Cloudflare Worker backend
- [mizu-helm](https://github.com/yourusername/mizu-helm) - Kubernetes Helm charts

---

**Mizu** - Like water, emails flow smoothly and reliably 💧
