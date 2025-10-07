# Configuration Reference

Complete reference for all Mizu configuration options.

## Quick Links

- **[SMTP Configuration](smtp.md)** - SMTP server, connections, timeouts
- **[Storage Configuration](storage.md)** - Filesystem vs S3 storage backends
- **[TLS & Certificates](tls.md)** - TLS configuration and Let's Encrypt
- **[Rate Limiting](rate-limiting.md)** - Multi-dimensional rate limiting
- **[DNS & Validation](dns-validation.md)** - DNS, SPF, DKIM, DMARC
- **[Clustering](clustering.md)** - Multi-node cluster configuration
- **[Destination](destination.md)** - Webhook and circuit breaker settings
- **[Health & Metrics](health-metrics.md)** - Health checks and Prometheus metrics

## Configuration File Location

Default locations (in order of precedence):
1. `--config` flag: `mizu-server --config /path/to/config.toml`
2. `./config.toml` (current directory)
3. `/etc/mizu/config.toml`

## Configuration Format

Mizu uses TOML format for configuration:

```toml
# General settings
log_format = "json"
local = false

[smtp]
listen_addr = ":25"
domain = "mail.example.com"

[storage]
backend = "filesystem"
filesystem_path = "/var/lib/mizu/storage"

[destination]
url = "https://your-app.com/email"
api_key = "your-secret-key"
```

## Generate Example Configuration

```bash
mizu-server generate-config > config.toml
```

## Environment Variables

All configuration options can be overridden with environment variables:

Format: `MIZU_<SECTION>_<KEY>`

Examples:
```bash
export MIZU_SMTP_DOMAIN="mail.example.com"
export MIZU_DESTINATION_URL="https://webhook.example.com/email"
export MIZU_DESTINATION_API_KEY="secret-key"
export MIZU_STORAGE_BACKEND="s3"
export MIZU_STORAGE_S3_BUCKET="my-bucket"
```

Nested sections use double underscore:
```bash
export MIZU_DESTINATION_CIRCUIT_BREAKER_ENABLED="true"
export MIZU_SMTP_RATE_LIMIT_ENABLED="true"
```

## Minimal Configuration

### Local Development

```toml
local = true

[smtp]
domain = "localhost"

[storage]
backend = "filesystem"
filesystem_path = "/tmp/mizu-storage"
```

### Production Single Node

```toml
log_format = "json"

[smtp]
listen_addr = ":25"
domain = "mail.example.com"
max_connections = 100

[storage]
backend = "filesystem"
filesystem_path = "/var/lib/mizu/storage"

[destination]
url = "https://your-app.example.com/email"
api_key = "your-secret-api-key"

[health]
enabled = true
listen_addr = ":8080"
username = "admin"
password = "secure-password"

[metrics]
enabled = true
```

### Production Clustered

```toml
log_format = "json"

[smtp]
listen_addr = ":25"
domain = "mail.example.com"
max_connections = 200

[storage]
backend = "s3"
endpoint = "s3.amazonaws.com"
bucket = "mizu-production"
access_key_id = "your-access-key"
secret_access_key = "your-secret-key"
region = "us-east-1"

[destination]
url = "https://your-app.example.com/email"
api_key = "your-secret-api-key"

[cluster]
enabled = true
node_name = "mizu-1"
bind_addr = "0.0.0.0"
bind_port = 7946
peers = ["mizu-2.internal:7946", "mizu-3.internal:7946"]
secret_key = "your-cluster-encryption-key"

[health]
enabled = true
listen_addr = ":8080"

[metrics]
enabled = true
```

## Configuration Validation

Validate configuration without starting server:

```bash
mizu-server --config config.toml --validate
```

## Common Patterns

### Pattern: High Security

```toml
[smtp]
require_sender_mx = true
min_tls_version = "1.3"

[blacklists]
enabled = true
lists = [
  "zen.spamhaus.org",
  "bl.spamcop.net",
  "b.barracudacentral.org"
]

[tls]
enable_autocert = true
use_production = true
```

### Pattern: High Performance

```toml
[smtp]
max_connections = 500
max_connections_per_ip = 50
timeout_seconds = 5

[dns]
cache_ttl_seconds = 600
timeout_seconds = 3

[stats]
retention_seconds = 3600  # 1 hour
max_ip_entries = 50000
```

### Pattern: Strict Rate Limiting

```toml
[smtp.rate_limit]
enabled = true

# Per IP: 30 emails per minute
[[smtp.rate_limit.dimensions]]
name = "per_ip"
keys = ["IP"]
limit = 30
window_seconds = 60

# Per domain: 500 emails per hour
[[smtp.rate_limit.dimensions]]
name = "per_domain"
keys = ["Domain"]
limit = 500
window_seconds = 3600

# Per IP+recipient: 5 emails per 5 minutes
[[smtp.rate_limit.dimensions]]
name = "per_ip_recipient"
keys = ["IP", "Recipient"]
limit = 5
window_seconds = 300
```

## Configuration Sections

### Top-Level Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `log_format` | string | `"text"` | Log format: `"text"` or `"json"` |
| `local` | bool | `false` | Enable local development mode (disables security checks) |

### Section: [smtp]

Core SMTP server configuration. See [SMTP Configuration](smtp.md).

Key options:
- `listen_addr`: Server listen address
- `domain`: Primary mail domain
- `max_message_size`: Maximum message size in bytes
- `max_connections`: Global connection limit

### Section: [storage]

Storage backend configuration. See [Storage Configuration](storage.md).

Key options:
- `backend`: `"filesystem"` or `"s3"`
- `filesystem_path`: Path for filesystem backend
- S3 credentials and bucket configuration

### Section: [destination]

Webhook destination configuration. See [Destination](destination.md).

Key options:
- `url`: Webhook URL
- `api_key`: Authentication key
- `max_retry_attempts`: Retry count
- `circuit_breaker`: Circuit breaker settings

### Section: [tls]

TLS certificate configuration. See [TLS & Certificates](tls.md).

Key options:
- `email`: Let's Encrypt registration email
- `domains`: Domains for certificates
- `enable_autocert`: Enable auto-renewal
- `use_production`: Use LE production vs staging

### Section: [cluster]

Multi-node clustering. See [Clustering](clustering.md).

Key options:
- `enabled`: Enable cluster mode
- `node_name`: Unique node identifier
- `peers`: List of peer nodes
- `secret_key`: Cluster encryption key

### Section: [health]

Health check endpoint. See [Health & Metrics](health-metrics.md).

Key options:
- `enabled`: Enable health endpoint
- `listen_addr`: Listen address
- `username`, `password`: Basic auth credentials

### Section: [metrics]

Prometheus metrics. See [Health & Metrics](health-metrics.md).

Key options:
- `enabled`: Enable metrics endpoint
- `path`: Metrics URL path
- Authentication credentials

## Configuration Best Practices

1. **Use JSON logging in production**: `log_format = "json"`
2. **Enable all security features**: SPF, DKIM, DMARC, blacklists
3. **Set strong API keys**: Use `openssl rand -base64 32`
4. **Configure rate limiting**: Prevent abuse
5. **Enable monitoring**: Health checks + Prometheus metrics
6. **Use TLS 1.2+**: `min_tls_version = "1.2"`
7. **Protect admin endpoints**: Set username/password
8. **Use S3 for clustering**: Required for multi-node
9. **Set appropriate limits**: Based on your capacity
10. **Regular backups**: Backup config and storage

## Configuration Security

### Secrets Management

Never commit secrets to version control:

```toml
# BAD - hardcoded secrets
[destination]
api_key = "abc123secret"

# GOOD - use environment variables
[destination]
api_key = "${MIZU_DESTINATION_API_KEY}"
```

Or use external secrets:
```bash
export MIZU_DESTINATION_API_KEY="$(vault kv get -field=api_key secret/mizu)"
mizu-server --config config.toml
```

### File Permissions

```bash
# Configuration file
sudo chown root:mizu /etc/mizu/config.toml
sudo chmod 640 /etc/mizu/config.toml

# Storage directory
sudo chown mizu:mizu /var/lib/mizu/storage
sudo chmod 750 /var/lib/mizu/storage
```

## Troubleshooting Configuration

### Configuration Validation Failed

```bash
# Check for syntax errors
mizu-server --config config.toml --validate

# Common issues:
# - Missing required fields
# - Invalid TOML syntax
# - Invalid backend value
# - Missing S3 credentials when backend is "s3"
```

### Environment Variable Not Working

```bash
# Verify variable is set
echo $MIZU_SMTP_DOMAIN

# Check variable name format
# Must be: MIZU_<SECTION>_<KEY> in all caps
# Example: MIZU_DESTINATION_API_KEY

# Test with verbose output
MIZU_SMTP_DOMAIN="test.com" mizu-server --config config.toml
```

### Configuration Not Reloading

Mizu requires restart to reload configuration:

```bash
sudo systemctl restart mizu
```

## Next Steps

- Review [SMTP configuration](smtp.md)
- Configure [storage backend](storage.md)
- Set up [TLS certificates](tls.md)
- Configure [rate limiting](rate-limiting.md)
