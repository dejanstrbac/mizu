# SRS (Sender Rewriting Scheme)

## Overview

SRS (Sender Rewriting Scheme) is a mechanism that rewrites the envelope sender address when forwarding emails to prevent SPF (Sender Policy Framework) validation failures. When Mizu forwards an email, SRS ensures that bounces are properly routed back through the relay while maintaining the original sender information.

## The Problem SRS Solves

When forwarding emails, a common problem occurs with SPF validation:

1. Alice sends email from `alice@example.com` to `user@yourdomain.com`
2. Your Mizu server receives it and forwards to `bob@destination.com`
3. `destination.com` performs SPF check on sender `alice@example.com`
4. SPF check **fails** because your Mizu server is not authorized to send for `example.com`
5. Email is rejected or marked as spam

**SRS Solution**: Rewrite the envelope sender to an address on your relay domain:
- Original: `MAIL FROM:<alice@example.com>`
- Rewritten: `MAIL FROM:<SRS0=hash=ts=example.com=alice@relay.mizu.com>`

Now SPF checks pass because the sender domain matches your relay server. If the email bounces, the bounce is sent to the SRS address at your relay, which can decode it back to the original sender.

## How SRS Works in Mizu

### Outbound (Forwarding)

When Mizu forwards an email, it encodes the original sender into an SRS address:

```
Original sender: alice@example.com
Forwarding to:   bob@destination.com

Envelope rewritten to:
MAIL FROM:<SRS0=k7qi=eq=example.com=alice@relay.mizu.com>
```

The SRS address contains:
- **SRS0**: Format version
- **k7qi**: HMAC-SHA1 hash (prevents forgery)
- **eq**: Timestamp (base32-encoded days since epoch)
- **example.com**: Original domain
- **alice**: Original local part
- **@relay.mizu.com**: Your relay domain

### Inbound (Bounce Handling)

When a bounce arrives at an SRS address, Mizu decodes it back to the original sender before delivering to your backend:

```
Bounce arrives to: SRS0=k7qi=eq=example.com=alice@relay.mizu.com
Decoded to:        alice@example.com
Backend receives:  Original sender address
```

### Re-Forwarding (SRS1)

If an SRS-encoded address is forwarded again, SRS1 format is used to prevent infinite growth:

```
First forward:  alice@example.com
                → SRS0=k7qi=eq=example.com=alice@relay1.com

Second forward: SRS0=k7qi=eq=example.com=alice@relay1.com
                → SRS1=sg5e=relay1.com==SRS0=k7qi=eq=example.com=alice@relay1.com@relay2.com
```

SRS1 addresses can still be decoded back to the original sender.

## Configuration

### Basic Configuration

```toml
[srs]
enabled = true
secret = "your-secret-key-change-this"
domain = "relay.yourdomain.com"
```

### Configuration Options

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `enabled` | No | `false` | Enable SRS rewriting |
| `secret` | Yes* | - | HMAC secret for hash generation (keep secret!) |
| `domain` | Yes* | - | Domain for SRS addresses (must be your relay domain) |

*Required when `enabled = true`

### Important Notes

1. **Secret Key Security**: The `secret` is used to generate HMAC hashes. Keep it secret and consistent across all cluster nodes. If changed, existing SRS addresses become invalid.

2. **Domain Choice**: Use a domain you control where your Mizu server can receive mail. This is typically the same as your `smtp.hostname`.

3. **Environment Variables**: For production, use environment variable:
   ```bash
   export SRS_SECRET="your-secret-key"
   ```
   Then reference in config:
   ```toml
   [srs]
   secret = "${SRS_SECRET}"
   ```

## When SRS is Applied

### Applied (Forwarding)

SRS encoding is **only applied to forwarding jobs**:

```toml
# Backend returns routing decision
{
  "accepted": true,
  "forward_to": ["bob@destination.com"]  # ← SRS applied here
}
```

The envelope sender is rewritten to an SRS address before forwarding.

### Not Applied (Local Delivery)

SRS is **not applied to local delivery**:

```toml
# Backend returns routing decision
{
  "accepted": true,
  "deliver_to": ["local@backend.com"]  # ← No SRS, original sender preserved
}
```

Local deliveries use the original sender address.

### Mixed Scenarios

When both delivery and forwarding occur:

```toml
{
  "accepted": true,
  "deliver_to": ["local@backend.com"],      # ← No SRS
  "forward_to": ["external@destination.com"] # ← SRS applied
}
```

- Delivery job: Original sender
- Forwarding job: SRS-encoded sender

## Security Considerations

### Hash Validation

Every SRS address contains an HMAC-SHA1 hash that prevents address forgery:

```
SRS0=k7qi=eq=example.com=alice@relay.mizu.com
     ^^^^
     Hash prevents forgery
```

If someone tries to craft a fake SRS address, the hash validation will fail and Mizu will reject it with:
```
550 invalid SRS address
```

### Timestamp Limitations

The timestamp in SRS addresses (e.g., `eq` = days since epoch) allows for potential expiration policies in future versions. Currently, no expiration is enforced, but the timestamp is preserved for forward compatibility.

### Secret Rotation

If you need to rotate the SRS secret:

1. **Add new secret** to config
2. **Deploy to all cluster nodes** simultaneously
3. **Wait for old SRS addresses to age out** (typically days to weeks)
4. Old SRS addresses will fail validation after rotation

**Important**: Secret rotation will invalidate all existing SRS addresses. Plan accordingly.

## Monitoring and Debugging

### Metrics

Mizu exposes SRS-related metrics:

```
# SRS encoding operations
mizu_srs_encode_total{result="success"}
mizu_srs_encode_total{result="error"}

# SRS decoding operations
mizu_srs_decode_total{result="success"}
mizu_srs_decode_total{result="error"}
mizu_srs_decode_total{result="invalid_hash"}
```

### Logs

SRS operations are logged at appropriate levels:

```json
{
  "level": "debug",
  "msg": "Applied SRS rewriting for forwarding",
  "original_from": "alice@example.com",
  "srs_from": "SRS0=k7qi=eq=example.com=alice@relay.mizu.com"
}
```

```json
{
  "level": "debug",
  "msg": "Decoded SRS address in RCPT TO",
  "srs_address": "SRS0=k7qi=eq=example.com=alice@relay.mizu.com",
  "original_address": "alice@example.com"
}
```

### Testing SRS

Manual testing with swaks:

```bash
# Test forwarding (outbound SRS)
swaks --to user@yourdomain.com \
      --from alice@example.com \
      --server relay.yourdomain.com \
      --port 25

# Check logs for SRS encoding
tail -f /var/log/mizu.log | grep SRS

# Test bounce handling (inbound SRS)
swaks --to SRS0=k7qi=eq=example.com=alice@relay.yourdomain.com \
      --from mailer-daemon@destination.com \
      --server relay.yourdomain.com \
      --port 25
```

## Troubleshooting

### SRS Addresses Not Being Created

**Symptom**: Forwarded emails use original sender instead of SRS address.

**Causes**:
1. SRS not enabled in config
2. `srsRewriter` is nil (check initialization)
3. Not a forwarding job (SRS only applies to forwarding)

**Solution**:
```toml
[srs]
enabled = true
secret = "your-secret"
domain = "relay.yourdomain.com"
```

### Invalid SRS Address Errors

**Symptom**: `550 invalid SRS address` when receiving mail.

**Causes**:
1. Hash validation failed (wrong secret or forged address)
2. Malformed SRS address format
3. Secret was rotated and address is now invalid

**Solution**:
- Verify SRS secret matches across all nodes
- Check address format: `SRS0=HASH=TS=domain=localpart@relay.domain`
- Review logs for specific validation failure

### SRS Address Decoding Failures

**Symptom**: Bounce emails not decoded correctly.

**Causes**:
1. Invalid base32 encoding in address components
2. Missing or corrupted address parts
3. Domain mismatch (SRS address not for this relay)

**Solution**:
- Check SRS address format
- Verify domain in SRS address matches your `srs.domain`
- Enable debug logging: `log_level = "debug"`

## Performance Considerations

### CPU Usage

SRS operations are lightweight:
- **Encoding**: HMAC-SHA1 hash + string formatting (~10μs)
- **Decoding**: Hash validation + string parsing (~15μs)

Overhead is negligible compared to SMTP protocol overhead.

### Memory Usage

- No persistent storage required
- Stateless design (all info in address)
- No caching needed

### Cluster Coordination

SRS is completely stateless and requires no cluster coordination:
- Each node can encode/decode independently
- Only requirement: Same secret across all nodes
- No gossip or S3 sync needed for SRS

## Best Practices

1. **Use Strong Secrets**: Generate cryptographically random secrets:
   ```bash
   openssl rand -base64 32
   ```

2. **Keep Secrets Consistent**: All cluster nodes must use the same SRS secret.

3. **Use Descriptive Domain**: Choose an SRS domain that clearly identifies your relay:
   ```toml
   domain = "relay.yourdomain.com"  # Good
   domain = "yourdomain.com"        # Confusing
   ```

4. **Monitor Metrics**: Track SRS success/failure rates to detect issues.

5. **Test Before Production**: Test forwarding and bounce handling in staging.

6. **Document Your Setup**: Record your SRS domain and secret storage location.

## Integration with Other Features

### SPF Validation

SRS works alongside SPF validation:
- **Inbound**: Validate SPF for incoming mail
- **Outbound**: Use SRS to ensure forwarded mail passes SPF at destination

### DKIM Signing

If Mizu signs emails with DKIM:
1. Original message signature (if any) is preserved
2. Mizu adds its own DKIM signature
3. SRS rewrites envelope sender (not visible in DKIM)

This allows both SPF (via SRS) and DKIM to pass at the destination.

### ARC (Authenticated Received Chain)

ARC preserves authentication results through forwarding:
1. Mizu validates SPF/DKIM/DMARC on incoming mail
2. Adds ARC headers to preserve validation results
3. Applies SRS for outbound SPF validation
4. Result: Destination sees both original authentication (via ARC) and new authentication (via SRS/SPF)

### Routing Integration

SRS respects routing decisions from your backend:

```json
{
  "accepted": true,
  "deliver_to": ["local@backend.com"],      // No SRS
  "forward_to": ["external@other.com"],     // SRS applied
  "reject": false
}
```

Your backend doesn't need to know about SRS—Mizu handles it automatically.

## Implementation Details

For developers working on Mizu:

### Package Structure

- **[pkg/srs/srs.go](../../pkg/srs/srs.go)**: Core SRS library (encode/decode)
- **[pkg/smtp/server.go](../../pkg/smtp/server.go)**: Integration points
  - `Session.Rcpt()`: Inbound SRS decoding (line ~1163)
  - `Session.createDeliveryJobs()`: Outbound SRS encoding (line ~1272)

### Key Functions

```go
// Encode original address to SRS format
func (r *Rewriter) Encode(original string) (string, error)

// Decode SRS address back to original
func (r *Rewriter) Decode(srsAddress string) (string, error)

// Check if address is in SRS format
func IsSRSAddress(address string) bool
```

### Testing

- **Unit tests**: [pkg/srs/srs_test.go](../../pkg/srs/srs_test.go) (13 tests)
- **Inbound integration**: [pkg/smtp/srs_integration_test.go](../../pkg/smtp/srs_integration_test.go) (5 tests)
- **Outbound integration**: [pkg/smtp/srs_outbound_test.go](../../pkg/smtp/srs_outbound_test.go) (5 tests)

Run all SRS tests:
```bash
go test -v -run TestSRS ./pkg/smtp ./pkg/srs
```

## References

- **RFC**: No official RFC for SRS (community standard)
- **Specification**: http://www.libsrs2.org/srs/srs.pdf
- **Background**: https://en.wikipedia.org/wiki/Sender_Rewriting_Scheme

## See Also

- [Email Validation](../operations/email-validation.md) - SPF/DKIM/DMARC
- [Routing](routing.md) - Backend routing decisions
- [Configuration Reference](README.md) - All config options
