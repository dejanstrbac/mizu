# Deployment Guide

This guide covers deploying Mizu SMTP relay in various configurations.

## Deployment Options

Mizu supports multiple deployment scenarios:

1. **[Single Node](single-node.md)** - Simple deployment for low to medium traffic
2. **[Clustered](clustered.md)** - High-availability multi-node deployment
3. **[Docker](docker.md)** - Container-based deployment
4. **[Kubernetes](kubernetes.md)** - Cloud-native deployment

## Prerequisites

### System Requirements

**Minimum (Single Node)**
- CPU: 2 cores
- RAM: 2 GB
- Disk: 20 GB
- OS: Linux (Ubuntu 22.04+, Debian 11+, RHEL 8+)

**Recommended (Production)**
- CPU: 4+ cores
- RAM: 8+ GB
- Disk: 50+ GB SSD
- OS: Linux with kernel 5.x+

**Clustered (Per Node)**
- CPU: 4+ cores
- RAM: 8+ GB
- Disk: 50+ GB SSD
- Network: Low-latency connection between nodes (<10ms)

### Network Requirements

- **Inbound TCP/25**: SMTP (required)
- **Inbound TCP/587**: SMTP submission (optional)
- **Inbound TCP/8080**: Health checks & admin API
- **Inbound TCP/7946**: Cluster gossip (clustered mode only)
- **Outbound TCP/443**: For destination webhook, Let's Encrypt, S3

### DNS Requirements

For production deployment:
1. **MX Record**: Points to your Mizu server(s)
   ```
   example.com.  MX  10  mail.example.com.
   ```

2. **A/AAAA Record**: For mail server hostname
   ```
   mail.example.com.  A  203.0.113.10
   ```

3. **PTR Record**: Reverse DNS (strongly recommended)
   ```
   10.113.0.203.in-addr.arpa.  PTR  mail.example.com.
   ```

4. **SPF Record**: Allow your server to send email
   ```
   example.com.  TXT  "v=spf1 mx ~all"
   ```

### Storage Requirements

Mizu supports two storage backends:

**Filesystem Backend** (Single Node)
- Suitable for: Single-node deployments
- Requirements: Local disk with 20+ GB free space
- Pros: Simple, no external dependencies
- Cons: Not suitable for clustering

**S3 Backend** (Clustered)
- Suitable for: Production clustered deployments
- Requirements: S3-compatible storage (AWS S3, MinIO, etc.)
- Pros: Shared storage, supports clustering, durable
- Cons: Requires external service

## Quick Start

### 1. Install Binary

```bash
# Download latest release
wget https://github.com/migadu/mizu/releases/latest/download/mizu-server
wget https://github.com/migadu/mizu/releases/latest/download/mizu-admin
chmod +x mizu-server mizu-admin
sudo mv mizu-server mizu-admin /usr/local/bin/
```

### 2. Generate Configuration

```bash
mizu-server generate-config > config.toml
```

### 3. Edit Configuration

Minimum required changes:
```toml
[smtp]
domain = "mail.example.com"  # Your mail server hostname

[destination]
url = "https://your-app.example.com/email"  # Your webhook
api_key = "your-secret-api-key"  # Your API key

[storage]
backend = "filesystem"  # Use "filesystem" for single node
filesystem_path = "/var/lib/mizu/storage"
```

### 4. Create Storage Directory

```bash
sudo mkdir -p /var/lib/mizu/storage
sudo chown mizu:mizu /var/lib/mizu/storage
```

### 5. Start Server

```bash
mizu-server --config config.toml
```

## Next Steps

- **Single Node**: Follow [Single Node Deployment](single-node.md) for detailed setup
- **Clustered**: Follow [Clustered Deployment](clustered.md) for HA setup
- **TLS**: Configure [TLS certificates](../configuration/tls.md)
- **Monitoring**: Set up [monitoring and metrics](../operations/monitoring.md)

## Security Checklist

Before going to production:

- [ ] TLS certificates configured and tested
- [ ] Strong API keys generated for destination webhook
- [ ] Rate limiting configured appropriately
- [ ] Health check endpoint protected with authentication
- [ ] Metrics endpoint protected (if exposed publicly)
- [ ] Firewall rules configured (limit SMTP to trusted sources if possible)
- [ ] Reverse DNS (PTR) configured
- [ ] SPF/DKIM/DMARC records published
- [ ] System user created (don't run as root)
- [ ] Log aggregation configured
- [ ] Backup strategy implemented

## Choosing a Deployment Type

| Factor | Single Node | Clustered |
|--------|-------------|-----------|
| **Traffic Volume** | <10K emails/day | >10K emails/day |
| **Availability** | 99% (downtime for updates) | 99.9%+ (rolling updates) |
| **Complexity** | Low | Medium-High |
| **Cost** | Low | Medium-High |
| **Storage** | Filesystem | S3/MinIO required |
| **Setup Time** | 30 minutes | 2-4 hours |

## Common Deployment Patterns

### Pattern 1: Simple Single Node
- 1 Mizu server
- Filesystem storage
- Manual TLS certificate management
- Best for: Small businesses, testing, <5K emails/day

### Pattern 2: HA Clustered
- 3+ Mizu servers behind load balancer
- S3 storage (AWS S3 or MinIO)
- Automatic TLS with Let's Encrypt
- Best for: Production, >10K emails/day, high availability required

### Pattern 3: Kubernetes
- Kubernetes deployment with HPA
- S3 storage
- Ingress for TLS termination
- Best for: Cloud-native, auto-scaling, multi-region

## Troubleshooting Deployment

### Cannot Bind to Port 25

**Issue**: Permission denied when binding to port 25

**Solution**:
```bash
# Option 1: Use setcap (recommended)
sudo setcap 'cap_net_bind_service=+ep' /usr/local/bin/mizu-server

# Option 2: Run as root (not recommended)
sudo mizu-server --config config.toml

# Option 3: Use port forwarding
sudo iptables -t nat -A PREROUTING -p tcp --dport 25 -j REDIRECT --to-port 2525
```

### TLS Certificate Errors

See [TLS Configuration Guide](../configuration/tls.md)

### Connection Timeouts

Check firewall rules:
```bash
# Allow SMTP
sudo ufw allow 25/tcp

# Allow health checks
sudo ufw allow 8080/tcp
```

### Storage Permission Errors

```bash
# Fix filesystem storage permissions
sudo chown -R mizu:mizu /var/lib/mizu/storage
sudo chmod 750 /var/lib/mizu/storage
```
