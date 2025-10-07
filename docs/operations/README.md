# Operations Guide

Day-to-day operations, maintenance, and troubleshooting for Mizu SMTP relay.

## Quick Links

- **[Monitoring & Metrics](monitoring.md)** - Prometheus metrics, health checks, dashboards
- **[Health Checks](health-checks.md)** - Health endpoint integration and monitoring
- **[Troubleshooting](troubleshooting.md)** - Common issues and solutions
- **[Backup & Recovery](backup-recovery.md)** - Backup strategies and disaster recovery
- **[Upgrading](upgrading.md)** - Safe upgrade procedures

## Daily Operations

### Check System Health

```bash
# Quick health check
mizu-admin -server http://localhost:8080 \
           -config /etc/mizu/config.toml \
           health

# Check metrics
curl http://localhost:8080/metrics | grep mizu_smtp

# View recent logs
sudo journalctl -u mizu -n 100
```

### Monitor Message Flow

```bash
# View statistics
mizu-admin stats

# Watch real-time logs
sudo journalctl -u mizu -f
```

### Check Resource Usage

```bash
# CPU and memory
ps aux | grep mizu-server

# Disk usage
df -h /var/lib/mizu

# Connection count
ss -tln | grep :25 | wc -l
```

## Common Tasks

### Restart Service

```bash
sudo systemctl restart mizu
sudo systemctl status mizu
```

### Reload Configuration

Mizu requires a restart to reload configuration:

```bash
# Edit config
sudo nano /etc/mizu/config.toml

# Restart
sudo systemctl restart mizu
```

### Flush Caches

```bash
# Flush recipient and IP caches
mizu-admin -server http://localhost:8080 \
           -config /etc/mizu/config.toml \
           flush-cache
```

### View Blocked IPs

```bash
mizu-admin -server http://localhost:8080 \
           -config /etc/mizu/config.toml \
           blocked-ips
```

### Force Certificate Renewal

```bash
mizu-admin -server http://localhost:8080 \
           -config /etc/mizu/config.toml \
           renew-cert --domain mail.example.com
```

## Maintenance Windows

### Planned Downtime

For single-node deployments:

1. **Notify users** of maintenance window
2. **Update DNS TTL** to 60 seconds (1 hour before)
3. **Take backup** of configuration and storage
4. **Perform maintenance** (upgrade, config changes)
5. **Verify health** after restart
6. **Restore DNS TTL** to normal (300-3600 seconds)

For clustered deployments:

1. **No downtime needed** - use rolling updates
2. **Remove node from load balancer**
3. **Update node**
4. **Verify health**
5. **Return to load balancer**
6. **Repeat for each node**

### Rolling Updates (Clustered)

```bash
# For each node:
# 1. Remove from load balancer
# 2. Update
ssh mizu-1 "sudo systemctl stop mizu"
ssh mizu-1 "sudo cp /usr/local/bin/mizu-server /usr/local/bin/mizu-server.backup"
scp mizu-server mizu-1:/tmp/
ssh mizu-1 "sudo mv /tmp/mizu-server /usr/local/bin/ && sudo systemctl start mizu"
# 3. Verify health
ssh mizu-1 "curl http://localhost:8080/health"
# 4. Add back to load balancer
# 5. Wait 2 minutes, then proceed to next node
```

## Monitoring Checklist

Daily:
- [ ] Check health endpoint
- [ ] Review error logs
- [ ] Check rejection rate
- [ ] Verify webhook connectivity

Weekly:
- [ ] Review blocked IPs
- [ ] Check disk usage
- [ ] Review rate limit violations
- [ ] Verify certificate expiry dates
- [ ] Review Prometheus alerts

Monthly:
- [ ] Test backup restore procedure
- [ ] Review and rotate logs
- [ ] Capacity planning review
- [ ] Security updates
- [ ] DNS record verification

## Alert Response

### Critical Alerts

#### Service Down
1. Check systemd status: `sudo systemctl status mizu`
2. Review logs: `sudo journalctl -u mizu -n 50`
3. Attempt restart: `sudo systemctl restart mizu`
4. If fails, check configuration: `mizu-server --validate`
5. If still failing, restore from backup

#### Circuit Breaker Open
1. Check destination health: `curl -I https://your-webhook.com/email`
2. Review webhook errors in logs
3. Check circuit breaker metrics
4. Verify API key is correct
5. Contact webhook service provider if needed

#### Disk Full
1. Check disk usage: `df -h`
2. Clean old logs: `sudo journalctl --vacuum-time=7d`
3. Check storage usage: `du -sh /var/lib/mizu/storage/*`
4. Increase disk or reduce retention

### Warning Alerts

#### High Rejection Rate
1. Check rejection reasons: `mizu-admin stats`
2. Review blocked IPs: `mizu-admin blocked-ips`
3. Check if legitimate senders are blocked
4. Adjust rate limits if needed
5. Review SPF/DKIM/DMARC validation

#### High Latency
1. Check webhook performance
2. Review circuit breaker stats
3. Check network connectivity
4. Monitor destination service health
5. Consider adding more nodes if sustained

## Performance Tuning

### Optimize for High Volume

```toml
[smtp]
max_connections = 200
max_connections_per_ip = 20

[smtp.rate_limit]
# Increase limits for legitimate traffic
[[smtp.rate_limit.dimensions]]
name = "per_ip"
keys = ["IP"]
limit = 120
window_seconds = 60
```

### Reduce Memory Usage

```toml
[stats]
retention_seconds = 3600  # Reduce from 24h to 1h
max_ip_entries = 50000    # Reduce from 100000
max_domain_entries = 25000 # Reduce from 50000
```

### Improve Validation Speed

```toml
[dns]
cache_ttl_seconds = 600  # Increase cache TTL
timeout_seconds = 3      # Reduce timeout
```

## Security Operations

### Rotate API Keys

1. Generate new API key: `openssl rand -base64 32`
2. Update destination service with new key
3. Update Mizu config with new key
4. Restart Mizu: `sudo systemctl restart mizu`
5. Verify connectivity
6. Revoke old key from destination service

### Update TLS Certificates

Auto-renewal is handled by CertMagic. Manual renewal:

```bash
mizu-admin renew-cert --domain mail.example.com
```

### Review Access Logs

```bash
# View all SMTP connections
sudo journalctl -u mizu | grep "SMTP connection"

# View rejected messages
sudo journalctl -u mizu | grep "rejected"

# View blocked IPs
sudo journalctl -u mizu | grep "blocked"
```

## Disaster Recovery

See detailed guide: [Backup & Recovery](backup-recovery.md)

Quick recovery steps:
1. Install Mizu on new server
2. Restore configuration from backup
3. Restore storage from backup
4. Update DNS to point to new server
5. Start service and verify health

## Capacity Planning

### Metrics to Monitor

- Messages per hour: `rate(mizu_smtp_messages_received[1h])`
- Peak connections: `max_over_time(mizu_smtp_connections_active[1h])`
- Average message size: `avg(mizu_smtp_message_size_bytes)`
- CPU usage: `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
- Memory usage: `node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes * 100`

### When to Scale

**Add more nodes when:**
- CPU usage consistently >70%
- Active connections consistently >80% of max
- Message queue backs up
- Webhook latency increases
- You need higher availability

**Upgrade node resources when:**
- Memory usage >80%
- Disk I/O wait time high
- Single node sufficient for availability

## Runbooks

### Runbook: Service Won't Start

```bash
# 1. Check logs
sudo journalctl -u mizu -n 50

# 2. Validate config
mizu-server --config /etc/mizu/config.toml --validate

# 3. Check port availability
sudo ss -tln | grep :25

# 4. Check permissions
ls -la /var/lib/mizu/storage
sudo -u mizu touch /var/lib/mizu/storage/test && rm /var/lib/mizu/storage/test

# 5. Check capabilities
getcap /usr/local/bin/mizu-server

# 6. Try manual start
sudo -u mizu /usr/local/bin/mizu-server --config /etc/mizu/config.toml
```

### Runbook: High Memory Usage

```bash
# 1. Check current usage
ps aux | grep mizu-server

# 2. Check stats retention
grep retention_seconds /etc/mizu/config.toml

# 3. Check active connections
mizu-admin health

# 4. Reduce retention temporarily
sudo sed -i 's/retention_seconds = 86400/retention_seconds = 3600/' /etc/mizu/config.toml
sudo systemctl restart mizu

# 5. Monitor
watch -n 5 'ps aux | grep mizu-server'
```

### Runbook: Webhook Failures

```bash
# 1. Check circuit breaker state
curl http://localhost:8080/metrics | grep circuit_breaker_state

# 2. Test webhook manually
curl -X POST https://your-webhook.com/email \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'

# 3. Check recent webhook errors
sudo journalctl -u mizu | grep "webhook" | tail -20

# 4. Review configuration
grep -A 10 "\[destination\]" /etc/mizu/config.toml

# 5. Temporarily increase timeout
sudo sed -i 's/http_timeout_seconds = 30/http_timeout_seconds = 60/' /etc/mizu/config.toml
sudo systemctl restart mizu
```

## Next Steps

- Set up [monitoring dashboards](monitoring.md)
- Implement [backup automation](backup-recovery.md)
- Review [troubleshooting guide](troubleshooting.md)
- Plan [upgrade schedule](upgrading.md)
