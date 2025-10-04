# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mizu is a high-performance SMTP relay server that accepts incoming emails on port 25 and synchronously forwards them via HTTP POST to a configured endpoint (e.g., Cloudflare Worker). The server includes comprehensive security features including STARTTLS with Let's Encrypt, distributed reputation tracking, circuit breaker protection, and extensive anti-spam validation.

**Key Architecture Decision**: The relay uses **synchronous delivery** - messages are posted to the HTTP endpoint during the SMTP DATA command, and only returns "250 OK" if delivery succeeds. This ensures we never accept messages we cannot deliver, delegating all business logic (including recipient validation) to the destination endpoint.

## Development Commands

### Building
```bash
go build -o smtp-relay .
```

### Running
```bash
# Production mode
./smtp-relay

# Local development mode (no TLS, dumps to terminal)
./smtp-relay --local

# Generate example config
./smtp-relay generate-config
```

### Configuration
- Primary config: `config.toml` (use `./smtp-relay generate-config` to create example)
- Environment variables for secrets: `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `DESTINATION_API_KEY`
- Command line flags override config file settings

## Architecture

### Core Components

1. **main.go**: Entry point that orchestrates TLS setup, stats tracking, circuit breaker, and SMTP server lifecycle
2. **pkg/smtp/server.go**: SMTP backend implementation with anti-spam validation, session handling, and synchronous message delivery
3. **pkg/config/**: Configuration loading with precedence: CLI flags > config file > env vars > defaults
4. **pkg/stats/**: Distributed reputation tracking system with S3-based sync between multiple servers
5. **pkg/storage/**: S3-compatible certificate storage for certmagic with distributed locking
6. **pkg/validation/**: DMARC, SPF, and DKIM validation using github.com/emersion/go-msgauth
7. **pkg/blacklist/**: DNS blacklist (RBL/DNSBL) checking functionality
8. **pkg/poster/**: HTTP forwarding with circuit breaker, retry logic, and exponential backoff
9. **pkg/health/**: Health check HTTP server for monitoring component status

### Key Design Patterns

- **Synchronous delivery**: SMTP connection blocks until HTTP POST completes - ensures reliable delivery without queuing
- **Circuit breaker**: Protects against cascading failures when destination is down (auto-recovery after timeout)
- **Graceful shutdown**: Uses context cancellation and signal handling throughout
- **Modular validation**: Each validation step (SPF, DMARC, blacklists, reputation) is separate and configurable
- **Distributed coordination**: S3 used for certificate storage and stats synchronization
- **Local vs Production modes**: Complete feature parity with local mode disabling TLS/S3 for development

### SMTP Flow

1. Connection accepted → IP reputation check → RBL/DNSBL validation
2. HELO/EHLO → Hostname validation and resolution checking
3. MAIL FROM → SPF validation and null sender checks
4. RCPT TO → Accepted (recipient validation delegated to HTTP endpoint)
5. DATA → DMARC validation, header validation, duplicate detection
6. **Synchronous delivery** → HTTP POST to destination endpoint with:
   - Circuit breaker check (fails fast if endpoint is down)
   - Retry logic with exponential backoff (up to 3 attempts)
   - Returns 250 OK only if endpoint accepts (200 response)
   - Returns 550 permanent failure if endpoint rejects (4xx response)
   - Returns 451 temporary failure if endpoint unavailable (5xx, timeout, circuit open)

### Stats System

The distributed stats system tracks IP and domain reputation across multiple servers:
- Each server exports stats to S3 as compressed JSON (`stats/{hostname}.json.gz`)
- Servers sync and merge stats from configured peer servers
- Bad actors detected by one server are blocked fleet-wide within sync interval
- Configurable retention periods and sync intervals

### Circuit Breaker

Protects the relay from overwhelming a failing destination endpoint:
- **Closed state**: Normal operation, tracks failures
- **Open state**: After threshold failures (default: 5), blocks requests for timeout period (default: 30s)
- **Half-open state**: After timeout, allows limited test requests to check if service recovered
- Returns SMTP 451 (temporary failure) when circuit is open, causing senders to retry later
- Configurable thresholds, timeouts, and automatic recovery

### Configuration Precedence

1. Command line flags (highest)
2. Configuration file (TOML)
3. Environment variables
4. Default values (lowest)

This allows secure deployment with secrets in environment variables while maintaining readable config files for non-sensitive settings.