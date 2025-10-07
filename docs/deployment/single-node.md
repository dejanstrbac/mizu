# Single Node Deployment

This guide covers deploying Mizu on a single server for low to medium traffic volumes.

## Overview

Single node deployment is suitable for:
- Email volumes up to 10,000 messages/day
- Development and testing environments
- Small to medium businesses
- Scenarios where 99% uptime is acceptable

## Architecture

```
┌──────────────────────────────────────┐
│         Single Mizu Node             │
│                                      │
│  ┌────────────────────────────┐     │
│  │  mizu-server               │     │
│  │  - SMTP :25                │     │
│  │  - Health :8080            │     │
│  └────────────────────────────┘     │
│                                      │
│  ┌────────────────────────────┐     │
│  │  Filesystem Storage        │     │
│  │  /var/lib/mizu/storage     │     │
│  └────────────────────────────┘     │
└──────────────────────────────────────┘
```

## Prerequisites

- Linux server (Ubuntu 22.04+ recommended)
- Root or sudo access
- Public IP address
- Domain name with DNS access

## Step 1: System Preparation

### Create System User

```bash
sudo useradd -r -s /bin/false -d /var/lib/mizu mizu
```

### Install Dependencies

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install -y wget curl

# RHEL/CentOS
sudo yum install -y wget curl
```

## Step 2: Install Mizu

### Download Binaries

```bash
cd /tmp
wget https://github.com/migadu/mizu/releases/latest/download/mizu-server
wget https://github.com/migadu/mizu/releases/latest/download/mizu-admin
chmod +x mizu-server mizu-admin
sudo mv mizu-server mizu-admin /usr/local/bin/
```

### Verify Installation

```bash
mizu-server -version
mizu-admin -version
```

## Step 3: Configure Storage

### Create Storage Directory

```bash
sudo mkdir -p /var/lib/mizu/storage
sudo mkdir -p /etc/mizu
sudo mkdir -p /var/log/mizu
sudo chown -R mizu:mizu /var/lib/mizu /var/log/mizu
```

## Step 4: Generate Configuration

```bash
mizu-server generate-config | sudo tee /etc/mizu/config.toml
```

## Step 5: Edit Configuration

Edit `/etc/mizu/config.toml`:

```toml
# Log format (use "json" in production)
log_format = "json"

[smtp]
listen_addr = ":25"
domain = "mail.example.com"  # CHANGE THIS
max_message_size = 10485760  # 10 MB
timeout_seconds = 10
max_connections = 100
max_connections_per_ip = 10

[smtp.rate_limit]
enabled = true

[[smtp.rate_limit.dimensions]]
name = "per_ip"
keys = ["IP"]
limit = 60
window_seconds = 60

[[smtp.rate_limit.dimensions]]
name = "per_domain"
keys = ["Domain"]
limit = 1000
window_seconds = 3600

[dns]
timeout_seconds = 5
cache_ttl_seconds = 300

[storage]
backend = "filesystem"
filesystem_path = "/var/lib/mizu/storage"

[destination]
url = "https://your-app.example.com/email"  # CHANGE THIS
api_key = "your-secret-api-key"  # CHANGE THIS
max_retry_attempts = 3
http_timeout_seconds = 30

[destination.circuit_breaker]
enabled = true
failure_threshold = 5
success_threshold = 2
timeout_seconds = 30

[health]
enabled = true
listen_addr = ":8080"
username = "admin"  # CHANGE THIS
password = "secure-password"  # CHANGE THIS

[metrics]
enabled = true
path = "/metrics"

[stats]
enabled = true
retention_seconds = 86400  # 24 hours

[blacklists]
enabled = true
lists = ["zen.spamhaus.org"]
timeout_seconds = 3
```

### Generate Strong Passwords

```bash
# Generate API key for destination
openssl rand -base64 32

# Generate admin password
openssl rand -base64 24
```

## Step 6: Configure Systemd Service

Create `/etc/systemd/system/mizu.service`:

```ini
[Unit]
Description=Mizu SMTP Relay
After=network.target
Documentation=https://github.com/migadu/mizu

[Service]
Type=simple
User=mizu
Group=mizu
ExecStart=/usr/local/bin/mizu-server --config /etc/mizu/config.toml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=mizu

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/mizu /var/log/mizu

# Allow binding to port 25
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

### Enable and Start Service

```bash
sudo systemctl daemon-reload
sudo systemctl enable mizu
sudo systemctl start mizu
```

### Check Status

```bash
sudo systemctl status mizu
sudo journalctl -u mizu -f
```

## Step 7: Configure Firewall

```bash
# UFW (Ubuntu/Debian)
sudo ufw allow 25/tcp comment 'SMTP'
sudo ufw allow 8080/tcp comment 'Mizu Health'

# firewalld (RHEL/CentOS)
sudo firewall-cmd --permanent --add-port=25/tcp
sudo firewall-cmd --permanent --add-port=8080/tcp
sudo firewall-cmd --reload
```

## Step 8: Verify Deployment

### Check Health Endpoint

```bash
curl -u admin:secure-password http://localhost:8080/health
```

Expected output:
```json
{
  "status": "healthy",
  "components": {
    "smtp": {"status": "healthy"},
    "storage": {"status": "healthy"}
  }
}
```

### Check Metrics

```bash
curl http://localhost:8080/metrics | grep mizu_smtp
```

### Test SMTP Connection

```bash
telnet localhost 25
# Should see: 220 mail.example.com ESMTP
QUIT
```

## Step 9: DNS Configuration

### Configure MX Record

```
example.com.  IN  MX  10  mail.example.com.
```

### Configure A Record

```
mail.example.com.  IN  A  203.0.113.10
```

### Configure PTR Record

Contact your hosting provider to set up reverse DNS:
```
10.113.0.203.in-addr.arpa.  PTR  mail.example.com.
```

### Verify DNS

```bash
dig MX example.com
dig A mail.example.com
dig -x 203.0.113.10
```

## Step 10: Send Test Email

```bash
# Install swaks (SMTP test tool)
sudo apt install swaks  # Ubuntu/Debian
sudo yum install swaks  # RHEL/CentOS

# Send test email
swaks --to test@yourdomain.com \
      --from sender@example.com \
      --server localhost \
      --port 25 \
      --body "Test email from Mizu"
```

## Monitoring

### View Logs

```bash
# Real-time logs
sudo journalctl -u mizu -f

# Last 100 lines
sudo journalctl -u mizu -n 100

# Filter by time
sudo journalctl -u mizu --since "1 hour ago"
```

### Check Statistics

```bash
mizu-admin -server http://localhost:8080 \
           -config /etc/mizu/config.toml \
           stats
```

### Monitor Metrics

Set up Prometheus scraping:
```yaml
scrape_configs:
  - job_name: 'mizu'
    static_configs:
      - targets: ['mail.example.com:8080']
    metrics_path: '/metrics'
```

## Backup and Recovery

### Backup Configuration

```bash
sudo cp /etc/mizu/config.toml /etc/mizu/config.toml.backup
```

### Backup Storage

```bash
sudo tar -czf /tmp/mizu-storage-backup.tar.gz /var/lib/mizu/storage
```

### Restore

```bash
sudo tar -xzf /tmp/mizu-storage-backup.tar.gz -C /
sudo chown -R mizu:mizu /var/lib/mizu/storage
sudo systemctl restart mizu
```

## Upgrading

```bash
# Stop service
sudo systemctl stop mizu

# Backup current binary
sudo cp /usr/local/bin/mizu-server /usr/local/bin/mizu-server.backup

# Download new version
wget https://github.com/migadu/mizu/releases/latest/download/mizu-server
chmod +x mizu-server
sudo mv mizu-server /usr/local/bin/

# Start service
sudo systemctl start mizu

# Verify
mizu-server -version
sudo systemctl status mizu
```

## Troubleshooting

### Service Won't Start

```bash
# Check logs
sudo journalctl -u mizu -n 50

# Check configuration
mizu-server --config /etc/mizu/config.toml --validate

# Check permissions
ls -la /var/lib/mizu/storage
```

### Port 25 Permission Denied

```bash
# Check capabilities
getcap /usr/local/bin/mizu-server

# Add capability
sudo setcap 'cap_net_bind_service=+ep' /usr/local/bin/mizu-server
```

### High Memory Usage

```bash
# Check stats retention
grep retention_seconds /etc/mizu/config.toml

# Reduce retention
sudo sed -i 's/retention_seconds = 86400/retention_seconds = 3600/' /etc/mizu/config.toml
sudo systemctl restart mizu
```

## Next Steps

- Configure [TLS certificates](../configuration/tls.md)
- Set up [monitoring dashboards](../operations/monitoring.md)
- Review [rate limiting](../configuration/rate-limiting.md)
- Implement [backup automation](../operations/backup-recovery.md)
