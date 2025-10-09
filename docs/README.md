# Mizu Documentation

Welcome to the Mizu SMTP relay documentation. This guide will help you deploy, configure, and operate Mizu in production environments.

## Quick Start

- **[Deployment Guide](deployment/README.md)** - Deploy Mizu for single-node or clustered setups
- **[Configuration Reference](configuration/README.md)** - Complete configuration options
- **[HTTP API Documentation](http-api.md)** - Webhook integration guide
- **[Operations Guide](operations/README.md)** - Day-to-day operations and maintenance

## Documentation Structure

### Integration
- [HTTP API Documentation](http-api.md) - Webhook endpoints for routing and delivery

### Configuration
- [Configuration Overview](configuration/README.md)
- [SMTP Configuration](configuration/smtp.md)
- [Storage Configuration](configuration/storage.md)
- [TLS & Certificates](configuration/tls.md)
- [Rate Limiting](configuration/rate-limiting.md)
- [DNS & Validation](configuration/dns-validation.md)
- [SRS (Sender Rewriting Scheme)](configuration/srs.md)
- [Clustering](configuration/clustering.md)

### Deployment
- [Single Node Deployment](deployment/single-node.md)
- [Clustered Deployment](deployment/clustered.md)
- [Docker Deployment](deployment/docker.md)
- [Kubernetes Deployment](deployment/kubernetes.md)

### Operations
- [Monitoring & Metrics](operations/monitoring.md)
- [Health Checks](operations/health-checks.md)
- [Troubleshooting](operations/troubleshooting.md)
- [Backup & Recovery](operations/backup-recovery.md)
- [Upgrading](operations/upgrading.md)

## Common Tasks

### Initial Setup
1. Follow the [Single Node Deployment](deployment/single-node.md) guide
2. Configure [SMTP settings](configuration/smtp.md)
3. Set up [TLS certificates](configuration/tls.md)
4. Configure [monitoring](operations/monitoring.md)

### Production Checklist
- ✅ TLS certificates configured and auto-renewal enabled
- ✅ Rate limiting configured
- ✅ DNS validation enabled (SPF, DKIM, DMARC)
- ✅ Health checks configured
- ✅ Prometheus metrics enabled
- ✅ Backup strategy implemented
- ✅ Monitoring and alerting configured

## Getting Help

- **Issues**: [GitHub Issues](https://github.com/migadu/mizu/issues)
- **Configuration Examples**: See `config.toml.example` in the repository root
- **Admin Tool**: Use `mizu-admin` for operational tasks

## Architecture Overview

Mizu is a production-ready SMTP relay that:
- Accepts SMTP connections on port 25/587
- Validates emails (SPF, DKIM, DMARC)
- Applies rate limiting and reputation tracking
- Forwards validated emails to a webhook endpoint
- Supports clustering for high availability
- Provides Prometheus metrics and health checks

```
┌─────────────┐
│   Internet  │
└──────┬──────┘
       │ SMTP (25/587)
┌──────▼──────────────────────────────┐
│         Mizu SMTP Relay             │
│  ┌────────────────────────────┐     │
│  │  SPF/DKIM/DMARC Validation │     │
│  └────────────────────────────┘     │
│  ┌────────────────────────────┐     │
│  │  Rate Limiting & Reputation│     │
│  └────────────────────────────┘     │
│  ┌────────────────────────────┐     │
│  │  Circuit Breaker           │     │
│  └────────────────────────────┘     │
└──────┬──────────────────────────────┘
       │ HTTPS
┌──────▼──────────┐
│  Your Webhook   │
│  (Email Handler)│
└─────────────────┘
```
