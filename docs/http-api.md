# HTTP API Documentation

This document describes the HTTP interfaces that Mizu uses to communicate with your backend services. Mizu makes HTTP requests to your backend for two purposes:

1. **Routing Lookups** - Determine if a recipient should be accepted and where to deliver
2. **Email Delivery** - Deliver the actual email content after acceptance

## Table of Contents

- [Overview](#overview)
- [Routing API (RCPT TO Validation)](#routing-api-rcpt-to-validation)
- [Delivery API (Email Content)](#delivery-api-email-content)
- [Forwarding API (Email Relay)](#forwarding-api-email-relay)
- [Authentication](#authentication)
- [Error Handling](#error-handling)
- [Retry Behavior](#retry-behavior)
- [Security Considerations](#security-considerations)
- [Examples](#examples)

---

## Overview

### Request Flow

```
SMTP Client → Mizu → Routing API (validate recipient)
                  ↓
                  → Delivery API (deliver email content)
                  → Forwarding API (relay to external addresses)
```

### Key Concepts

- **Synchronous Delivery**: Mizu waits for your backend to respond before sending SMTP `250 OK`
- **Zero Message Loss**: No internal queue for accepted emails - delivery happens immediately
- **Separate Endpoints**: Different URLs for routing lookups, local delivery, and forwarding
- **Persistent Queue**: Failed deliveries are queued with automatic retry and DLQ

---

## Routing API (RCPT TO Validation)

### Purpose

Called during the SMTP `RCPT TO` command to determine if Mizu should accept mail for a recipient.

### Configuration

```toml
[routing]
enabled = true
url = "https://your-backend.example.com/routing/resolve"
api_key = "your-routing-api-key"
timeout_seconds = 5
cache_ttl_seconds = 300  # Cache positive results
```

### Request

**Method**: `POST`

**URL**: Configured via `routing.url`

**Headers**:
```http
POST /routing/resolve HTTP/1.1
Host: your-backend.example.com
Content-Type: application/json
X-API-Key: your-routing-api-key
X-Trace-ID: smtp-trace-abc123
```

**Body**:
```json
{
  "recipient": "user@yourdomain.com",
  "sender": "alice@example.com",
  "client_ip": "203.0.113.42",
  "subject": "Important Message"
}
```

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `recipient` | string | Yes | The RCPT TO address being validated |
| `sender` | string | No | The MAIL FROM address (for policy checks) |
| `client_ip` | string | No | Client IP address (for policy checks) |
| `subject` | string | No | Email subject (parsed from DATA, may be empty during RCPT TO) |

### Response

**Success** (200 OK):
```json
{
  "accepted": true,
  "deliver_to": ["local-user@backend.com"],
  "forward_to": ["external@other.com"],
  "delivery_endpoint": "https://backend.example.com/email/deliver",
  "forward_endpoint": "https://backend.example.com/email/forward",
  "priority": 5,
  "is_catchall": false
}
```

**Rejection** (200 OK with `accepted: false`):
```json
{
  "accepted": false,
  "error_code": "recipient_not_found",
  "error_message": "No such user: user@yourdomain.com"
}
```

**Response Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `accepted` | bool | Yes | Whether to accept the email |
| `deliver_to` | []string | No | Recipients for local delivery (internal) |
| `forward_to` | []string | No | Recipients for forwarding (external relay) |
| `delivery_endpoint` | string | No | Custom HTTP endpoint for delivery jobs |
| `forward_endpoint` | string | No | Custom HTTP endpoint for forwarding jobs |
| `priority` | int | No | Queue priority (higher = more important) |
| `is_catchall` | bool | No | Whether this matched a catchall rule |
| `error_code` | string | No | Machine-readable error code (when rejected) |
| `error_message` | string | No | Human-readable error message (when rejected) |

**Standard Error Codes**:
- `domain_not_found` - Domain not configured
- `recipient_not_found` - User doesn't exist
- `recipient_blocked` - Recipient is blocked/disabled
- `policy_rejection` - Rejected by policy (e.g., spam filter)
- `quota_exceeded` - Recipient mailbox full

### Behavior

1. **Cache**: Positive responses (`accepted: true`) are cached for `cache_ttl_seconds`
2. **SMTP Response**:
   - `accepted: true` → `250 OK`
   - `accepted: false` → `550 User not found` (or custom error)
3. **Timeout**: Request must complete within `timeout_seconds`
4. **No Retry**: Routing lookups are not retried (must be fast and reliable)

---

## Delivery API (Email Content)

### Purpose

Called after the SMTP `DATA` command to deliver the actual email content to your backend for local recipients.

### Configuration

```toml
[destination]
url = "https://your-backend.example.com/email/deliver"
api_key = "your-destination-api-key"
timeout_seconds = 30
max_retry_attempts = 5
```

Or via routing response:
```json
{
  "accepted": true,
  "deliver_to": ["user@backend.com"],
  "delivery_endpoint": "https://custom.example.com/webhook"
}
```

### Request

**Method**: `POST`

**URL**:
- Configured via `destination.url` (default)
- Or `delivery_endpoint` from routing response (per-job override)

**Headers**:
```http
POST /email/deliver HTTP/1.1
Host: your-backend.example.com
Content-Type: message/rfc822
X-API-Key: your-destination-api-key
X-Mail-From: alice@example.com
X-Mail-To: user@yourdomain.com, admin@yourdomain.com
X-Trace-ID: smtp-trace-abc123
X-Junk: yes
Content-Length: 1234
```

**Header Fields**:
| Header | Required | Description |
|--------|----------|-------------|
| `Content-Type` | Yes | Always `message/rfc822` (standard email format) |
| `X-API-Key` | Optional | Authentication key (if configured) |
| `X-Mail-From` | Yes | Envelope sender (MAIL FROM address) |
| `X-Mail-To` | Yes | Envelope recipients (comma-separated) |
| `X-Trace-ID` | Yes | Trace ID for log correlation |
| `X-Junk` | Optional | Set to `yes` if email classified as spam |

**Body**:
Raw RFC 822 email message with headers and body:
```
From: alice@example.com
To: user@yourdomain.com
Subject: Test Email
Date: Mon, 15 Jan 2024 12:00:00 +0000
Message-ID: <abc123@example.com>
DKIM-Signature: v=1; a=rsa-sha256; ...

This is the email body content.
```

### Response

**Success**:
```http
HTTP/1.1 200 OK
Content-Type: application/json

{"status": "accepted"}
```

Or:
```http
HTTP/1.1 202 Accepted
Content-Type: application/json

{"status": "queued", "message_id": "msg-xyz789"}
```

**Accepted Status Codes**:
- `200 OK` - Email delivered successfully
- `202 Accepted` - Email queued for processing

**Failure**:
```http
HTTP/1.1 500 Internal Server Error
Content-Type: application/json

{"error": "database connection failed"}
```

### Behavior

1. **Synchronous**: Mizu waits for response before sending SMTP `250 OK`
2. **Retry**: Failed requests are retried with exponential backoff (up to `max_retry_attempts`)
3. **Timeout**: Request must complete within `timeout_seconds`
4. **Circuit Breaker**: Protects your backend from being overwhelmed during failures
5. **DLQ**: After all retries exhausted, job moves to Dead Letter Queue

**Retry Schedule** (default):
- Attempt 1: Immediate
- Attempt 2: 1 second delay
- Attempt 3: 2 second delay
- Attempt 4: 4 second delay
- Attempt 5: 8 second delay

---

## Forwarding API (Email Relay)

### Purpose

Called after the SMTP `DATA` command to relay emails to external addresses (when `forward_to` is specified).

### Configuration

```toml
[forwarding]
url = "https://your-backend.example.com/email/forward"
api_key = "your-forwarding-api-key"
timeout_seconds = 30
```

Or via routing response:
```json
{
  "accepted": true,
  "forward_to": ["external@other.com"],
  "forward_endpoint": "https://relay.example.com/forward"
}
```

### Request

**Identical to Delivery API** with these key differences:

1. **Envelope Sender**: May be SRS-rewritten to prevent SPF failures
   ```
   X-Mail-From: SRS0=hash=ts=example.com=alice@relay.yourdomain.com
   ```

2. **Recipients**: External addresses (not local to your domain)
   ```
   X-Mail-To: bob@external.com, carol@other.net
   ```

3. **Original Sender Tracking**: The job's `OriginalFrom` field preserves the pre-SRS sender

### Example Request

```http
POST /email/forward HTTP/1.1
Host: relay.example.com
Content-Type: message/rfc822
X-API-Key: your-forwarding-api-key
X-Mail-From: SRS0=k7qi=eq=example.com=alice@relay.yourdomain.com
X-Mail-To: bob@external.com
X-Trace-ID: smtp-trace-def456

From: alice@example.com
To: user@yourdomain.com
Subject: Forwarded Email
...
```

### Response

Same as Delivery API (200 OK or 202 Accepted for success).

### Behavior

- **SRS Encoding**: If SRS is enabled, `X-Mail-From` will contain an SRS-rewritten address
- **Retry**: Same retry logic as delivery
- **Separate Queue**: Forwarding jobs are separate from delivery jobs
- **Priority**: Can have different priority from delivery jobs

---

## Authentication

### API Key Authentication

**Header-based** (recommended):
```http
X-API-Key: your-secret-api-key
```

**Configuration**:
```toml
[routing]
api_key = "${ROUTING_API_KEY}"

[destination]
api_key = "${DESTINATION_API_KEY}"

[forwarding]
api_key = "${FORWARDING_API_KEY}"
```

### URL-based Authentication

Some backends use URL query parameters or path-based auth:
```toml
[destination]
url = "https://backend.example.com/webhook?token=abc123"
api_key = ""  # Leave empty if not using X-API-Key header
```

### Mutual TLS (mTLS)

For enhanced security, configure client certificates:
```toml
[destination]
url = "https://backend.example.com/email"
client_cert = "/etc/mizu/certs/client.crt"
client_key = "/etc/mizu/certs/client.key"
```

---

## Error Handling

### HTTP Status Codes

**Success** (no retry):
- `200 OK` - Successfully processed
- `202 Accepted` - Queued for processing

**Client Errors** (no retry):
- `400 Bad Request` - Invalid request format
- `401 Unauthorized` - Invalid API key
- `403 Forbidden` - Access denied
- `404 Not Found` - Endpoint doesn't exist
- `413 Payload Too Large` - Email too large

**Server Errors** (retry):
- `500 Internal Server Error` - Backend failure
- `502 Bad Gateway` - Upstream service unavailable
- `503 Service Unavailable` - Backend overloaded
- `504 Gateway Timeout` - Backend timeout

### Retry Logic

**Retryable Errors**:
- 5xx HTTP status codes
- Network timeouts
- Connection refused
- DNS lookup failures
- Circuit breaker open

**Non-Retryable Errors**:
- 4xx HTTP status codes
- Context cancellation
- Invalid request format

**Exponential Backoff**:
```
Attempt 1: 0 seconds
Attempt 2: 1 second
Attempt 3: 2 seconds
Attempt 4: 4 seconds
Attempt 5: 8 seconds
...
Max delay: 5 minutes (configurable)
```

### Dead Letter Queue (DLQ)

After exhausting all retry attempts, failed jobs are moved to the DLQ:

**View DLQ**:
```bash
./mizu-admin dlq list
```

**Retry DLQ jobs**:
```bash
./mizu-admin dlq retry <job-id>
./mizu-admin dlq retry-all
```

**Purge DLQ**:
```bash
./mizu-admin dlq purge <job-id>
./mizu-admin dlq purge-all
```

---

## Security Considerations

### 1. API Key Security

- **Use strong keys**: Generate with `openssl rand -base64 32`
- **Rotate regularly**: Update keys periodically
- **Use environment variables**: Never commit keys to version control
- **Separate keys**: Different keys for routing, delivery, and forwarding

### 2. HTTPS Only

Always use HTTPS for production:
```toml
[destination]
url = "https://backend.example.com/email"  # ✓ Good
# url = "http://backend.example.com/email"  # ✗ Bad (unencrypted)
```

### 3. IP Allowlisting

Restrict access to your backend endpoints by source IP:
```nginx
# nginx example
location /email {
    allow 203.0.113.0/24;  # Mizu server IPs
    deny all;
    proxy_pass http://backend;
}
```

### 4. Rate Limiting

Protect your backend from abuse:
```nginx
# nginx example
limit_req_zone $binary_remote_addr zone=email:10m rate=100r/s;
location /email {
    limit_req zone=email burst=20;
    proxy_pass http://backend;
}
```

### 5. Validate X-API-Key

Always verify the API key on your backend:
```python
# Python/Flask example
from flask import request, abort

@app.route('/email/deliver', methods=['POST'])
def deliver_email():
    api_key = request.headers.get('X-API-Key')
    if api_key != os.getenv('EXPECTED_API_KEY'):
        abort(401, 'Invalid API key')

    # Process email...
```

### 6. Validate Content-Type

Only accept `message/rfc822` for email delivery:
```python
if request.headers.get('Content-Type') != 'message/rfc822':
    abort(400, 'Invalid content type')
```

---

## Examples

### Example 1: Simple Delivery Webhook (Python/Flask)

```python
from flask import Flask, request
import email
from email import policy

app = Flask(__name__)

@app.route('/email/deliver', methods=['POST'])
def deliver_email():
    # Verify API key
    api_key = request.headers.get('X-API-Key')
    if api_key != os.getenv('MIZU_API_KEY'):
        return {'error': 'unauthorized'}, 401

    # Parse email
    msg = email.message_from_bytes(
        request.data,
        policy=policy.default
    )

    # Extract metadata
    mail_from = request.headers.get('X-Mail-From')
    mail_to = request.headers.get('X-Mail-To', '').split(', ')
    trace_id = request.headers.get('X-Trace-ID')
    is_junk = request.headers.get('X-Junk') == 'yes'

    # Process email
    print(f"[{trace_id}] Delivering email")
    print(f"  From: {mail_from}")
    print(f"  To: {mail_to}")
    print(f"  Subject: {msg['subject']}")
    print(f"  Spam: {is_junk}")

    # Store in database
    store_email(
        sender=mail_from,
        recipients=mail_to,
        subject=msg['subject'],
        body=msg.get_body(preferencelist=('plain',)).get_content(),
        raw=request.data,
        is_spam=is_junk
    )

    return {'status': 'accepted'}, 200
```

### Example 2: Routing Resolver (Python/Flask)

```python
@app.route('/routing/resolve', methods=['POST'])
def resolve_routing():
    # Verify API key
    api_key = request.headers.get('X-API-Key')
    if api_key != os.getenv('MIZU_ROUTING_KEY'):
        return {'error': 'unauthorized'}, 401

    data = request.json
    recipient = data['recipient']
    sender = data.get('sender', '')
    client_ip = data.get('client_ip', '')

    # Check if user exists
    user = lookup_user(recipient)
    if not user:
        return {
            'accepted': False,
            'error_code': 'recipient_not_found',
            'error_message': f'No such user: {recipient}'
        }, 200

    # Check if user is active
    if not user.is_active:
        return {
            'accepted': False,
            'error_code': 'recipient_blocked',
            'error_message': 'Recipient account disabled'
        }, 200

    # Accept with routing
    response = {
        'accepted': True,
        'deliver_to': [user.internal_address],
        'priority': user.priority
    }

    # Forward if user has forwarding enabled
    if user.forward_address:
        response['forward_to'] = [user.forward_address]

    return response, 200
```

### Example 3: Go Webhook Server

```go
package main

import (
    "encoding/json"
    "io"
    "log"
    "net/http"
    "os"
)

type RoutingRequest struct {
    Recipient string `json:"recipient"`
    Sender    string `json:"sender"`
    ClientIP  string `json:"client_ip"`
}

type RoutingResponse struct {
    Accepted     bool     `json:"accepted"`
    DeliverTo    []string `json:"deliver_to,omitempty"`
    ErrorCode    string   `json:"error_code,omitempty"`
    ErrorMessage string   `json:"error_message,omitempty"`
}

func routingHandler(w http.ResponseWriter, r *http.Request) {
    // Verify API key
    apiKey := r.Header.Get("X-API-Key")
    if apiKey != os.Getenv("MIZU_ROUTING_KEY") {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Parse request
    var req RoutingRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }

    // Look up recipient
    exists := checkRecipientExists(req.Recipient)

    var resp RoutingResponse
    if exists {
        resp = RoutingResponse{
            Accepted:  true,
            DeliverTo: []string{req.Recipient},
        }
    } else {
        resp = RoutingResponse{
            Accepted:     false,
            ErrorCode:    "recipient_not_found",
            ErrorMessage: "User not found",
        }
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}

func deliveryHandler(w http.ResponseWriter, r *http.Request) {
    // Verify API key
    apiKey := r.Header.Get("X-API-Key")
    if apiKey != os.Getenv("MIZU_DELIVERY_KEY") {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    // Verify content type
    if r.Header.Get("Content-Type") != "message/rfc822" {
        http.Error(w, "invalid content type", http.StatusBadRequest)
        return
    }

    // Read email
    emailBytes, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "failed to read body", http.StatusBadRequest)
        return
    }

    // Extract metadata
    mailFrom := r.Header.Get("X-Mail-From")
    mailTo := r.Header.Get("X-Mail-To")
    traceID := r.Header.Get("X-Trace-ID")
    isJunk := r.Header.Get("X-Junk") == "yes"

    log.Printf("[%s] Received email from %s to %s (junk=%v)",
        traceID, mailFrom, mailTo, isJunk)

    // Store email
    if err := storeEmail(emailBytes, mailFrom, mailTo); err != nil {
        http.Error(w, "storage failed", http.StatusInternalServerError)
        return
    }

    // Success
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "accepted",
    })
}

func main() {
    http.HandleFunc("/routing/resolve", routingHandler)
    http.HandleFunc("/email/deliver", deliveryHandler)

    log.Println("Starting webhook server on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### Example 4: Node.js/Express Webhook

```javascript
const express = require('express');
const { simpleParser } = require('mailparser');

const app = express();

// Middleware for routing endpoint (JSON)
app.use('/routing', express.json());

// Middleware for delivery endpoint (raw body)
app.use('/email', express.raw({
    type: 'message/rfc822',
    limit: '50mb'
}));

// Routing endpoint
app.post('/routing/resolve', (req, res) => {
    // Verify API key
    const apiKey = req.headers['x-api-key'];
    if (apiKey !== process.env.MIZU_ROUTING_KEY) {
        return res.status(401).json({ error: 'unauthorized' });
    }

    const { recipient, sender, client_ip } = req.body;

    // Check recipient
    const user = lookupUser(recipient);
    if (!user) {
        return res.json({
            accepted: false,
            error_code: 'recipient_not_found',
            error_message: `User not found: ${recipient}`
        });
    }

    // Accept
    res.json({
        accepted: true,
        deliver_to: [user.email],
        priority: user.priority || 0
    });
});

// Delivery endpoint
app.post('/email/deliver', async (req, res) => {
    // Verify API key
    const apiKey = req.headers['x-api-key'];
    if (apiKey !== process.env.MIZU_DELIVERY_KEY) {
        return res.status(401).json({ error: 'unauthorized' });
    }

    // Extract metadata
    const mailFrom = req.headers['x-mail-from'];
    const mailTo = req.headers['x-mail-to'];
    const traceId = req.headers['x-trace-id'];
    const isJunk = req.headers['x-junk'] === 'yes';

    console.log(`[${traceId}] Delivering email from ${mailFrom} to ${mailTo}`);

    // Parse email
    const parsed = await simpleParser(req.body);

    // Store email
    await storeEmail({
        from: mailFrom,
        to: mailTo.split(', '),
        subject: parsed.subject,
        text: parsed.text,
        html: parsed.html,
        raw: req.body,
        spam: isJunk,
        traceId: traceId
    });

    // Success
    res.json({ status: 'accepted' });
});

app.listen(8080, () => {
    console.log('Webhook server listening on port 8080');
});
```

---

## Performance Tips

### 1. Return Quickly

Mizu holds the SMTP connection open while waiting for your response. Aim for:
- **Routing**: < 100ms (fast database lookup)
- **Delivery**: < 1 second (quick database insert)

### 2. Use 202 Accepted for Async Processing

If you need to perform slow operations (virus scanning, content analysis), return `202 Accepted` immediately and process asynchronously:

```python
@app.route('/email/deliver', methods=['POST'])
def deliver_email():
    # Quick validation
    if not validate_api_key(request.headers.get('X-API-Key')):
        return {'error': 'unauthorized'}, 401

    # Queue for async processing
    job_id = queue_email_for_processing(request.data, request.headers)

    # Return immediately
    return {'status': 'queued', 'job_id': job_id}, 202
```

### 3. Cache Routing Results

Use `cache_ttl_seconds` to cache routing lookups:
```toml
[routing]
cache_ttl_seconds = 300  # 5 minutes
```

### 4. Batch Database Operations

If processing high volume, batch database inserts:
```python
email_buffer = []

def deliver_email():
    email_buffer.append(email_data)

    if len(email_buffer) >= 100:
        db.bulk_insert(email_buffer)
        email_buffer.clear()

    return {'status': 'accepted'}, 202
```

---

## Troubleshooting

### Problem: 401 Unauthorized

**Cause**: API key mismatch

**Solution**:
```bash
# Check Mizu config
grep api_key /etc/mizu/config.toml

# Check backend expects same key
echo $MIZU_API_KEY
```

### Problem: Connection Refused

**Cause**: Backend not reachable

**Solution**:
```bash
# Test connectivity from Mizu server
curl -v https://backend.example.com/email/deliver

# Check firewall rules
sudo iptables -L
```

### Problem: 504 Timeout

**Cause**: Backend responding too slowly

**Solution**:
- Optimize backend response time
- Increase timeout: `timeout_seconds = 60`
- Use `202 Accepted` for async processing

### Problem: Circuit Breaker Open

**Cause**: Too many failures triggered circuit breaker

**Solution**:
```bash
# Check circuit breaker metrics
curl http://localhost:8080/metrics | grep circuit_breaker

# Wait for circuit breaker to close (half-open state)
# Or fix backend issues and restart Mizu
```

---

## See Also

- [Configuration Reference](configuration/README.md) - All config options
- [Routing Configuration](configuration/routing.md) - Routing setup
- [Circuit Breaker](configuration/destination.md#circuit-breaker) - Protecting your backend
- [DLQ Management](operations/dlq-management.md) - Dead letter queue operations
- [Monitoring](operations/monitoring.md) - Metrics and logging
