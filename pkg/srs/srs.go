// Package srs implements Sender Rewriting Scheme (SRS) for email forwarding.
//
// SRS solves the problem of SPF validation failures when forwarding emails.
// When an email is forwarded, the original sender's domain won't authorize the
// forwarding server's IP address, causing SPF checks to fail. SRS rewrites the
// envelope sender to use the forwarding domain, preserving the ability to
// receive bounces while maintaining SPF compliance.
//
// # SRS Address Format
//
// SRS uses a specific format for rewritten addresses:
//
//	SRS0=HHH=TT=domain=localpart@forwarder.com
//
// Where:
//   - HHH: Hash of the address components (4 chars base32)
//   - TT: Timestamp in base32 (2 chars, days since epoch)
//   - domain: Original sender's domain
//   - localpart: Original sender's local part
//
// # Example
//
//	Original:  alice@example.com
//	Rewritten: SRS0=abcd=5Z=example.com=alice@relay.mizu.com
//
// # Bounce Handling
//
// When a bounce occurs, the receiving server sends it to the SRS address.
// The forwarder decodes the SRS address to extract the original sender and
// forwards the bounce appropriately.
//
// # Security
//
// SRS uses HMAC-SHA1 with a secret key to prevent address forgery.
// The hash ensures that only the server with the secret can generate valid
// SRS addresses.
//
// # Standards
//
// This implementation follows the SRS specification:
// https://en.wikipedia.org/wiki/Sender_Rewriting_Scheme
package srs

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

const (
	// SRS0Prefix is the prefix for SRS-rewritten addresses
	SRS0Prefix = "SRS0="

	// SRS1Prefix is the prefix for re-forwarded SRS addresses
	SRS1Prefix = "SRS1="

	// hashLength is the number of base32 characters for the hash
	hashLength = 4

	// timestampLength is the number of base32 characters for the timestamp
	timestampLength = 2

	// base32Encoding uses lowercase without padding for compact addresses
	base32Alphabet = "abcdefghijklmnopqrstuvwxyz234567"
)

var (
	// base32Encoding for compact SRS addresses (lowercase, no padding)
	encoding = base32.NewEncoding(base32Alphabet).WithPadding(base32.NoPadding)
)

// Rewriter handles SRS address encoding and decoding
type Rewriter struct {
	secret []byte // Secret key for HMAC
	domain string // Domain to use for SRS addresses (e.g., "relay.mizu.com")
}

// NewRewriter creates a new SRS rewriter with the given secret and domain
func NewRewriter(secret, domain string) *Rewriter {
	return &Rewriter{
		secret: []byte(secret),
		domain: domain,
	}
}

// Encode rewrites an email address using SRS
//
// Example:
//
//	original: alice@example.com
//	returns:  SRS0=abcd=5Z=example.com=alice@relay.mizu.com
func (r *Rewriter) Encode(originalAddress string) (string, error) {
	if originalAddress == "" {
		return "", fmt.Errorf("original address cannot be empty")
	}

	// Parse the original address
	parts := strings.SplitN(originalAddress, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid email address: %s", originalAddress)
	}
	localpart := parts[0]
	domain := parts[1]

	// Check if already SRS-encoded (don't double-encode)
	if strings.HasPrefix(localpart, "SRS0=") {
		// Already SRS0, convert to SRS1 for re-forwarding
		return r.encodeSRS1(originalAddress)
	}
	if strings.HasPrefix(localpart, "SRS1=") {
		// Already SRS1, keep as-is (don't re-encode)
		return originalAddress, nil
	}

	// Generate timestamp (days since epoch, base32-encoded)
	timestamp := r.encodeTimestamp(time.Now())

	// Create the SRS local part components
	// Format: SRS0=HHH=TT=domain=localpart
	srsComponents := fmt.Sprintf("%s=%s=%s", timestamp, domain, localpart)

	// Generate hash
	hash := r.generateHash(timestamp, domain, localpart)

	// Build the SRS address
	srsLocalpart := fmt.Sprintf("%s%s=%s", SRS0Prefix, hash, srsComponents)
	srsAddress := fmt.Sprintf("%s@%s", srsLocalpart, r.domain)

	return srsAddress, nil
}

// encodeSRS1 converts an SRS0 address to SRS1 for re-forwarding
func (r *Rewriter) encodeSRS1(srs0Address string) (string, error) {
	// SRS1 format: SRS1=HHH=domain==localpart@forwarder.com
	// This preserves the original SRS0 address when re-forwarding

	parts := strings.SplitN(srs0Address, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid SRS0 address: %s", srs0Address)
	}

	srs0Local := parts[0]
	srs0Domain := parts[1]

	// Generate hash for SRS1
	hash := r.generateHashSRS1(srs0Domain, srs0Local)

	// SRS1 format: SRS1=HHH=domain==localpart
	srs1Localpart := fmt.Sprintf("%s%s=%s==%s", SRS1Prefix, hash, srs0Domain, srs0Local)
	srs1Address := fmt.Sprintf("%s@%s", srs1Localpart, r.domain)

	return srs1Address, nil
}

// Decode extracts the original email address from an SRS-encoded address
//
// Example:
//
//	srsAddress: SRS0=abcd=5Z=example.com=alice@relay.mizu.com
//	returns:    alice@example.com
func (r *Rewriter) Decode(srsAddress string) (string, error) {
	if srsAddress == "" {
		return "", fmt.Errorf("SRS address cannot be empty")
	}

	// Parse the address
	parts := strings.SplitN(srsAddress, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid SRS address: %s", srsAddress)
	}

	localpart := parts[0]
	domain := parts[1]

	// Check SRS0 format
	if strings.HasPrefix(localpart, SRS0Prefix) {
		return r.decodeSRS0(localpart, domain)
	}

	// Check SRS1 format
	if strings.HasPrefix(localpart, SRS1Prefix) {
		return r.decodeSRS1(localpart, domain)
	}

	return "", fmt.Errorf("not an SRS address: %s", srsAddress)
}

// decodeSRS0 decodes an SRS0 address
func (r *Rewriter) decodeSRS0(localpart, domain string) (string, error) {
	// Remove SRS0= prefix
	content := strings.TrimPrefix(localpart, SRS0Prefix)

	// Split into components: HHH=TT=domain=localpart
	components := strings.SplitN(content, "=", 4)
	if len(components) != 4 {
		return "", fmt.Errorf("invalid SRS0 format: %s", localpart)
	}

	hash := components[0]
	timestamp := components[1]
	originalDomain := components[2]
	originalLocalpart := components[3]

	// Verify hash
	expectedHash := r.generateHash(timestamp, originalDomain, originalLocalpart)
	if hash != expectedHash {
		return "", fmt.Errorf("invalid SRS0 hash: address may be forged")
	}

	// Reconstruct original address
	originalAddress := fmt.Sprintf("%s@%s", originalLocalpart, originalDomain)
	return originalAddress, nil
}

// decodeSRS1 decodes an SRS1 address
func (r *Rewriter) decodeSRS1(localpart, domain string) (string, error) {
	// Remove SRS1= prefix
	content := strings.TrimPrefix(localpart, SRS1Prefix)

	// Split into components: HHH=domain==localpart
	components := strings.SplitN(content, "=", 3)
	if len(components) != 3 {
		return "", fmt.Errorf("invalid SRS1 format: %s", localpart)
	}

	hash := components[0]
	srs0Domain := components[1]
	// components[2] should be empty due to ==, followed by original localpart
	remaining := components[2]

	// The remaining part should start with "=" (from ==)
	if !strings.HasPrefix(remaining, "=") {
		return "", fmt.Errorf("invalid SRS1 format: missing separator")
	}
	srs0Local := strings.TrimPrefix(remaining, "=")

	// Verify hash
	expectedHash := r.generateHashSRS1(srs0Domain, srs0Local)
	if hash != expectedHash {
		return "", fmt.Errorf("invalid SRS1 hash: address may be forged")
	}

	// Decode the SRS0 components directly
	return r.decodeSRS0(srs0Local, srs0Domain)
}

// IsSRSAddress checks if an address is SRS-encoded
func (r *Rewriter) IsSRSAddress(address string) bool {
	parts := strings.SplitN(address, "@", 2)
	if len(parts) != 2 {
		return false
	}
	localpart := parts[0]
	return strings.HasPrefix(localpart, SRS0Prefix) || strings.HasPrefix(localpart, SRS1Prefix)
}

// generateHash creates a hash for SRS0 addresses
func (r *Rewriter) generateHash(timestamp, domain, localpart string) string {
	// Create HMAC-SHA1 hash
	h := hmac.New(sha1.New, r.secret)
	h.Write([]byte(timestamp))
	h.Write([]byte(domain))
	h.Write([]byte(localpart))
	hash := h.Sum(nil)

	// Encode to base32 and take first 4 characters
	encoded := strings.ToLower(encoding.EncodeToString(hash))
	if len(encoded) < hashLength {
		return encoded
	}
	return encoded[:hashLength]
}

// generateHashSRS1 creates a hash for SRS1 addresses
func (r *Rewriter) generateHashSRS1(srs0Domain, srs0Local string) string {
	h := hmac.New(sha1.New, r.secret)
	h.Write([]byte(srs0Domain))
	h.Write([]byte(srs0Local))
	hash := h.Sum(nil)

	encoded := strings.ToLower(encoding.EncodeToString(hash))
	if len(encoded) < hashLength {
		return encoded
	}
	return encoded[:hashLength]
}

// encodeTimestamp encodes a timestamp as base32 (days since epoch)
func (r *Rewriter) encodeTimestamp(t time.Time) string {
	// Use days since Unix epoch for compact representation
	days := int(t.Unix() / 86400)

	// Encode as base32
	buf := make([]byte, 4)
	buf[0] = byte(days >> 24)
	buf[1] = byte(days >> 16)
	buf[2] = byte(days >> 8)
	buf[3] = byte(days)

	encoded := strings.ToLower(encoding.EncodeToString(buf))
	// Take last 2 characters for compact representation
	if len(encoded) < timestampLength {
		return encoded
	}
	return encoded[len(encoded)-timestampLength:]
}

// decodeTimestamp decodes a base32 timestamp to a time.Time
func (r *Rewriter) decodeTimestamp(encoded string) (time.Time, error) {
	// This is for validation/expiration checking if needed
	// For now, we don't strictly validate timestamps
	decoded, err := encoding.DecodeString(strings.ToUpper(encoded))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp: %w", err)
	}

	if len(decoded) == 0 {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}

	// Reconstruct days
	days := int64(decoded[len(decoded)-1])
	if len(decoded) > 1 {
		days |= int64(decoded[len(decoded)-2]) << 8
	}

	// Convert to Unix timestamp
	unixTime := days * 86400
	return time.Unix(unixTime, 0), nil
}
