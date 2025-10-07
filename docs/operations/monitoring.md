# Monitoring & Metrics

Comprehensive guide to monitoring Mizu SMTP relay in production.

## Overview

Mizu provides multiple monitoring interfaces:
- **Prometheus Metrics**: Time-series metrics for detailed monitoring
- **Health Checks**: Simple HTTP endpoint for uptime monitoring
- **Admin API**: Real-time statistics via mizu-admin tool
- **Structured Logs**: JSON logs for log aggregation

## Prometheus Metrics

### Enabling Metrics

Configure in `config.toml`:

```toml
[metrics]
enabled = true
path = "/metrics"
username = "prometheus"  # Optional
password = "secret"      # Optional
```

### Available Metrics

#### SMTP Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_smtp_connections_total` | Counter | Total SMTP connections received |
| `mizu_smtp_connections_active` | Gauge | Currently active SMTP connections |
| `mizu_smtp_messages_received` | Counter | Total messages received |
| `mizu_smtp_messages_rejected` | Counter (with labels) | Messages rejected by reason |
| `mizu_smtp_message_size_bytes` | Histogram | Size distribution of messages |
| `mizu_smtp_session_duration_seconds` | Histogram | SMTP session duration |

#### Validation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_smtp_spf_checks` | Counter (with labels) | SPF validation results (pass/fail/none) |
| `mizu_smtp_dkim_checks` | Counter (with labels) | DKIM validation results |
| `mizu_smtp_dmarc_checks` | Counter (with labels) | DMARC validation results |
| `mizu_smtp_mx_validation` | Counter (with labels) | MX record validation results |

#### Circuit Breaker Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_circuit_breaker_state` | Gauge | Current state (0=closed, 1=open, 2=half-open) |
| `mizu_circuit_breaker_failures` | Counter | Circuit breaker failures |
| `mizu_circuit_breaker_successes` | Counter | Circuit breaker successes |
| `mizu_circuit_breaker_rejects` | Counter | Requests rejected by open circuit |

#### Rate Limiting Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_rate_limit_checks` | Counter | Total rate limit checks |
| `mizu_rate_limit_exceeded` | Counter (with labels) | Rate limits exceeded by dimension |
| `mizu_rate_limit_allowed` | Counter | Requests allowed through rate limiter |

#### Destination Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mizu_destination_requests_total` | Counter | Total webhook requests |
| `mizu_destination_errors_total` | Counter (with labels) | Webhook errors by type |
| `mizu_destination_duration_seconds` | Histogram | Webhook request duration |
| `mizu_destination_retries_total` | Counter | Total retry attempts |

### Prometheus Configuration

Add to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'mizu'
    scrape_interval: 15s
    static_configs:
      - targets: ['mail.example.com:8080']
    metrics_path: '/metrics'
    basic_auth:
      username: 'prometheus'
      password: 'secret'
```

For clustered deployments:

```yaml
scrape_configs:
  - job_name: 'mizu-cluster'
    scrape_interval: 15s
    static_configs:
      - targets:
        - 'mizu-1.example.com:8080'
        - 'mizu-2.example.com:8080'
        - 'mizu-3.example.com:8080'
    metrics_path: '/metrics'
```

## Health Checks

### Health Endpoint

```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "status": "healthy",
  "components": {
    "smtp": {
      "status": "healthy",
      "message": "SMTP server running",
      "connections": 5
    },
    "storage": {
      "status": "healthy",
      "message": "Storage backend operational"
    },
    "circuit_breaker": {
      "status": "closed",
      "failures": 0,
      "successes": 1523
    }
  },
  "uptime_seconds": 86400,
  "version": "1.0.0"
}
```

### Health Check Configuration

```toml
[health]
enabled = true
listen_addr = ":8080"
username = "monitor"
password = "secure-pass"
```

### Integration with Load Balancers

#### HAProxy

```haproxy
backend mizu_smtp
    option httpchk GET /health
    http-check expect status 200
    server mizu1 10.0.1.10:8080 check
    server mizu2 10.0.1.11:8080 check
```

#### Nginx

```nginx
upstream mizu_backend {
    server 10.0.1.10:8080;
    server 10.0.1.11:8080;
}

server {
    location /health {
        proxy_pass http://mizu_backend/health;
        proxy_http_version 1.1;
    }
}
```

#### Kubernetes

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 30

readinessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

## Admin Tool (mizu-admin)

### Installation

```bash
# Admin tool is included in release
sudo mv mizu-admin /usr/local/bin/
```

### Usage

```bash
# View health status
mizu-admin -server http://localhost:8080 \
           -config config.toml \
           health

# View blocked IPs
mizu-admin -server http://localhost:8080 \
           -config config.toml \
           blocked-ips

# View statistics
mizu-admin -server http://localhost:8080 \
           -config config.toml \
           stats

# View TLS certificates
mizu-admin -server http://localhost:8080 \
           -config config.toml \
           certs

# Flush caches
mizu-admin -server http://localhost:8080 \
           -config config.toml \
           flush-cache
```

### Example Output

```bash
$ mizu-admin stats

Message Statistics:
  Accepted: 15,234
  Rejected: 892
  Junk:     234

Top Sender Domains:
  1. example.com      (5,234 messages)
  2. test.org         (3,421 messages)
  3. company.net      (2,109 messages)

Top Blocked IPs:
  1. 192.0.2.100     (reputation: -0.85)
  2. 198.51.100.50   (reputation: -0.72)
```

## Grafana Dashboards

### Sample Dashboard JSON

Create a dashboard in Grafana with these panels:

#### Panel 1: Message Rate

```promql
# Messages per second
rate(mizu_smtp_messages_received[5m])
```

#### Panel 2: Connection Count

```promql
# Active connections
mizu_smtp_connections_active
```

#### Panel 3: Rejection Rate

```promql
# Rejections by reason
sum(rate(mizu_smtp_messages_rejected[5m])) by (reason)
```

#### Panel 4: Circuit Breaker State

```promql
# Circuit breaker state
mizu_circuit_breaker_state
```

#### Panel 5: Webhook Latency

```promql
# 95th percentile webhook latency
histogram_quantile(0.95, rate(mizu_destination_duration_seconds_bucket[5m]))
```

#### Panel 6: Validation Success Rate

```promql
# SPF pass rate
sum(rate(mizu_smtp_spf_checks{result="pass"}[5m])) /
sum(rate(mizu_smtp_spf_checks[5m])) * 100
```

### Import Dashboard

Dashboard ID: [Coming soon]

Or manually import from `docs/monitoring/grafana-dashboard.json`

## Alerting Rules

### Prometheus Alert Rules

Create `mizu-alerts.yml`:

```yaml
groups:
  - name: mizu_alerts
    interval: 30s
    rules:
      # High rejection rate
      - alert: HighRejectionRate
        expr: |
          rate(mizu_smtp_messages_rejected[5m]) > 10
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High email rejection rate"
          description: "Mizu is rejecting {{ $value }} messages/sec"

      # Circuit breaker open
      - alert: CircuitBreakerOpen
        expr: |
          mizu_circuit_breaker_state{state="open"} == 1
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "Circuit breaker is open"
          description: "Destination webhook is unreachable"

      # High webhook latency
      - alert: HighWebhookLatency
        expr: |
          histogram_quantile(0.95,
            rate(mizu_destination_duration_seconds_bucket[5m])
          ) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High webhook latency"
          description: "95th percentile latency is {{ $value }}s"

      # Service down
      - alert: MizuDown
        expr: up{job="mizu"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Mizu service is down"
          description: "Mizu has been down for more than 1 minute"

      # High connection count
      - alert: HighConnectionCount
        expr: mizu_smtp_connections_active > 80
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High active connection count"
          description: "{{ $value }} active connections"

      # Rate limit frequently exceeded
      - alert: RateLimitExceeded
        expr: |
          rate(mizu_rate_limit_exceeded[5m]) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Rate limits frequently exceeded"
          description: "{{ $value }} rate limit violations/sec"
```

## Log Aggregation

### JSON Logging

Enable in config:

```toml
log_format = "json"
```

### Example Log Entry

```json
{
  "level": "info",
  "ts": 1704067200,
  "caller": "smtp/server.go:123",
  "msg": "Message accepted",
  "trace_id": "abc123...",
  "remote_addr": "203.0.113.10:54321",
  "from": "sender@example.com",
  "to": "recipient@yourdomain.com",
  "size": 15234,
  "spf": "pass",
  "dmarc": "pass"
}
```

### Elasticsearch/Filebeat

`filebeat.yml`:

```yaml
filebeat.inputs:
  - type: journald
    id: mizu-logs
    include_matches:
      - "systemd.unit=mizu.service"

processors:
  - decode_json_fields:
      fields: ["message"]
      target: ""
      overwrite_keys: true

output.elasticsearch:
  hosts: ["localhost:9200"]
  index: "mizu-logs-%{+yyyy.MM.dd}"
```

### Loki/Promtail

`promtail-config.yml`:

```yaml
clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: mizu
    journal:
      matches: _SYSTEMD_UNIT=mizu.service
    pipeline_stages:
      - json:
          expressions:
            level: level
            msg: msg
            trace_id: trace_id
      - labels:
          level:
          trace_id:
```

## Performance Monitoring

### Key Metrics to Monitor

1. **Throughput**: Messages per second
2. **Latency**: P50, P95, P99 webhook response times
3. **Error Rate**: Rejections and webhook failures
4. **Resource Usage**: CPU, memory, connections
5. **Validation**: SPF/DKIM/DMARC pass rates

### Capacity Planning

Monitor these metrics for capacity planning:

```promql
# Messages per hour
sum(increase(mizu_smtp_messages_received[1h]))

# Average message size
avg(mizu_smtp_message_size_bytes)

# Peak connection count
max_over_time(mizu_smtp_connections_active[1h])
```

## Troubleshooting Metrics

### Debug High Rejection Rate

```bash
# Check rejection reasons
curl http://localhost:8080/metrics | grep mizu_smtp_messages_rejected

# Check blocked IPs
mizu-admin -server http://localhost:8080 blocked-ips
```

### Debug Webhook Issues

```bash
# Check circuit breaker state
curl http://localhost:8080/metrics | grep circuit_breaker_state

# Check webhook errors
curl http://localhost:8080/metrics | grep destination_errors
```

## Next Steps

- Set up [alerting](../operations/troubleshooting.md)
- Review [backup strategy](../operations/backup-recovery.md)
- Configure [log retention](../configuration/smtp.md)
