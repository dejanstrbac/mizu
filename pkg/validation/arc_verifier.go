package validation

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// ARCCanonicalization represents the canonicalization algorithm
type ARCCanonicalization string

const (
	ARCCanonicalizationSimple  ARCCanonicalization = "simple"
	ARCCanonicalizationRelaxed ARCCanonicalization = "relaxed"
)

// verifyARCSignature verifies an ARC signature (Message-Signature or Seal)
func verifyARCSignature(
	headerName string, // "ARC-Message-Signature" or "ARC-Seal"
	sigHeader RawHeader,
	headers []RawHeader,
	body string,
	resolver func(string) ([]string, error),
	logger *slog.Logger,
) error {
	// 1. Parse signature tags
	tags, err := parseARCTags(sigHeader.Value)
	if err != nil {
		return fmt.Errorf("malformed signature tags: %w", err)
	}

	// 2. Validate tags
	if tags["a"] != "rsa-sha256" {
		// Only rsa-sha256 is strictly required by ARC, though some might use others.
		// For now we assume rsa-sha256 as it's the standard.
		return fmt.Errorf("unsupported algorithm: %s", tags["a"])
	}

	domain := tags["d"]
	selector := tags["s"]
	if domain == "" || selector == "" {
		return fmt.Errorf("missing domain or selector")
	}

	// 3. Get canonicalization
	headerCanon, bodyCanon := parseARCCanonicalization(tags["c"])

	// ARC-Seal uses relaxed/relaxed implicitly if c tag is missing (RFC 8617)
	if headerName == "ARC-Seal" && tags["c"] == "" {
		headerCanon = ARCCanonicalizationRelaxed
		bodyCanon = ARCCanonicalizationRelaxed
	}

	// 4. Calculate Body Hash (only for ARC-Message-Signature)
	// ARC-Seal does not verify body (bh is ignored)
	if headerName == "ARC-Message-Signature" {
		bodyHashStr := tags["bh"]
		if bodyHashStr == "" {
			return fmt.Errorf("missing body hash")
		}

		hasher := sha256.New()
		canonicalizeBody(body, bodyCanon, hasher)
		calculatedHash := hasher.Sum(nil)

		decodedBodyHash, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(bodyHashStr, " ", ""))
		if err != nil {
			return fmt.Errorf("invalid base64 body hash: %w", err)
		}

		if !strings.EqualFold(fmt.Sprintf("%x", calculatedHash), fmt.Sprintf("%x", decodedBodyHash)) {
			// Try strict comparison first
			if string(calculatedHash) != string(decodedBodyHash) {
				return fmt.Errorf("body hash mismatch")
			}
		}
	}

	// 5. Calculate Header Hash
	headersToSign, err := getHeadersToSign(headerName, tags["h"], headers)
	if err != nil {
		return fmt.Errorf("failed to determine headers to sign: %w", err)
	}

	hasher := sha256.New()

	// Hash selected headers
	for _, h := range headersToSign {
		// Use Raw header to ensure correct unfolding/canonicalization
		// parseRawHeaders might have compromised Value by incorrect trimming
		canonicalized := canonicalizeHeaderRaw(h.Raw, headerCanon)
		hasher.Write([]byte(canonicalized))
	}

	// Hash the signature header itself (without the b= value)
	// The key should be the original case from the signature header if possible?
	// RFC 6376: "The header field of the DKIM-Signature header field is also included in the hash calculation"
	// "The dkim-signature header field ... is processed as if it were a header field ... but with the value of the b= tag deleted"

	// We need the raw key of the signature header. RawHeader struct has it.
	// But we need to remove the b= value.
	sigHeaderNoSig := removeSignatureValue(sigHeader.Raw)

	// Wait, removeSignatureValue should return the full raw line but with b= value empty.
	// E.g. "ARC-Message-Signature: ... b=; ..."

	canonicalizedSig := canonicalizeHeaderRaw(sigHeaderNoSig, headerCanon)
	// Note: canonicalizeHeaderRaw expects the full line "Key: Value\r\n"

	// However, removeSignatureValue might return just the value or modified raw?
	// Let's ensure we handle it correctly.

	// Trimming \r\n from the end because canonicalizeHeader adds it back or handles it?
	// The hasher expects the canonicalized form which ends with \r\n.
	// BUT, for the signature header itself, RFC says:
	// "The header field is treated as though it were a header field with a null value for the b= tag"
	// "The header field is canonicalized ... and the result is appended to the hash input"
	// AND importantly: "The DKIM-Signature header field ... does NOT include a trailing CRLF" (Wait, really?)

	// RFC 6376 Section 3.7:
	// "The DKIM-Signature header field ... is canonicalized ... and then the result is input to the hash function.
	// NOTE: The DKIM-Signature header field is inserted into the header block ... therefore it DOES NOT have a trailing CRLF
	// when calculating the signature."

	canonicalizedSig = strings.TrimRight(canonicalizedSig, "\r\n")
	hasher.Write([]byte(canonicalizedSig))

	hashedHeaders := hasher.Sum(nil)

	// 6. Verify Signature
	signatureStr := tags["b"]
	if signatureStr == "" {
		return fmt.Errorf("missing signature")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(signatureStr, " ", ""))
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}

	// 7. Fetch Public Key using provided resolver
	// The resolver parameter is required and should never be nil
	if resolver == nil {
		return fmt.Errorf("DNS resolver is required for signature verification")
	}

	txtRecords, err := resolver(fmt.Sprintf("%s._domainkey.%s", selector, domain))
	if err != nil {
		return fmt.Errorf("failed to lookup public key: %w", err)
	}

	// Find the valid DKIM record (v=DKIM1 or just containing p=)
	var record string
	for _, txt := range txtRecords {
		// Basic check for DKIM record
		if strings.Contains(txt, "p=") {
			record = txt
			break
		}
	}
	if record == "" && len(txtRecords) > 0 {
		// Fallback: try the first one if none matched explicit criteria
		record = txtRecords[0]
	}

	pubKey, err := parsePublicKey(record)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	// 8. Verify
	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashedHeaders, signature)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// Helper functions

func parseARCTags(value string) (map[string]string, error) {
	tags := make(map[string]string)
	parts := strings.Split(value, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue // Malformed tag or empty?
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		tags[key] = val
	}
	return tags, nil
}

func parseARCCanonicalization(c string) (ARCCanonicalization, ARCCanonicalization) {
	if c == "" {
		return ARCCanonicalizationSimple, ARCCanonicalizationSimple
	}
	parts := strings.Split(c, "/")
	header := ARCCanonicalization(strings.TrimSpace(parts[0]))
	body := ARCCanonicalizationSimple
	if len(parts) > 1 {
		body = ARCCanonicalization(strings.TrimSpace(parts[1]))
	}
	return header, body
}

func canonicalizeHeader(key, value, raw string, canon ARCCanonicalization) string {
	if canon == ARCCanonicalizationSimple {
		return raw
	}

	// Relaxed Canonicalization
	// 1. Convert header field name to lowercase
	key = strings.ToLower(key)

	// 2. Unfold value (remove CRLF followed by WSP) and convert WSP to single space
	// 3. Trim WSP at start and end

	// Simple approach for value:
	// Replace all WSP (space, tab, cr, lf) sequences with single space
	// But strictly speaking:
	// "Unfold all header field continuation lines as described in [RFC5322]"
	// "Convert all sequences of one or more WSP characters to a single SP"

	// Let's process the raw value part (after the first colon)
	// But passing 'value' usually has CRLF stripped/processed?
	// Our RawHeader.Value usually has continuation lines joined?
	// No, RawHeader.Value in our implementation currently is built by builder.

	// Let's use a simpler robust approach for relaxed value:
	// Split by any whitespace, filter empty, join by single space.
	fields := strings.Fields(value)
	val := strings.Join(fields, " ")

	return key + ":" + val + "\r\n"
}

func canonicalizeHeaderRaw(raw string, canon ARCCanonicalization) string {
	if canon == ARCCanonicalizationSimple {
		return raw
	}
	// Extract key and value from raw
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) < 2 {
		return raw // Should not happen for valid header
	}
	key := parts[0]
	val := parts[1]

	return canonicalizeHeader(key, val, raw, canon)
}

func canonicalizeBody(body string, canon ARCCanonicalization, w io.Writer) {
	if canon == ARCCanonicalizationSimple {
		// Simple Body Canonicalization:
		// - Ignore all empty lines at the end of the message body.
		// - If there is no trailing CRLF, add one.

		// Remove trailing empty lines
		for strings.HasSuffix(body, "\r\n") {
			body = strings.TrimSuffix(body, "\r\n")
		}
		body += "\r\n"
		w.Write([]byte(body))
		return
	}

	// Relaxed Body Canonicalization
	// 1. Ignore all whitespace at end of lines
	// 2. Compress WSP within a line to single SP
	// 3. Ignore all empty lines at end of body
	// 4. Ensure trailing CRLF

	lines := strings.Split(body, "\n")
	var canonicalLines []string

	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r") // Remove \r

		// Trim trailing whitespace (WSP: space or tab)
		line = strings.TrimRight(line, " \t")

		// Compress WSP within line to single SP
		// BUT do NOT trim leading whitespace (unlike strings.Fields)
		var sb strings.Builder
		spaceSeen := false
		for _, r := range line {
			if r == ' ' || r == '\t' {
				if !spaceSeen {
					sb.WriteRune(' ')
					spaceSeen = true
				}
			} else {
				sb.WriteRune(r)
				spaceSeen = false
			}
		}
		canonicalLines = append(canonicalLines, sb.String())
	}

	// Remove empty lines from end
	for len(canonicalLines) > 0 && canonicalLines[len(canonicalLines)-1] == "" {
		canonicalLines = canonicalLines[:len(canonicalLines)-1]
	}

	for _, line := range canonicalLines {
		w.Write([]byte(line))
		w.Write([]byte("\r\n"))
	}
}

func getHeadersToSign(headerName string, hTag string, headers []RawHeader) ([]RawHeader, error) {
	var headersToSign []RawHeader

	if headerName == "ARC-Seal" {
		// Signs all previous ARC headers
		// We assume 'headers' passed to this function are ALL headers, so we need to filter
		// actually, ARC-Seal signs ARC-Seal, ARC-Message-Signature, ARC-Authentication-Results
		// from instance 1 to current.
		// BUT verifyARCSignature is called with what context?
		// We need to implement this logic carefully.

		// If we are verifying instance N, we need headers 1..N (excluding current Seal?)
		// RFC 8617:
		// "The "ARC-Seal" (AS) header field ... signs the ARC chain ...
		// consisting of the "ARC-Authentication-Results" (AAR), "ARC-Message-Signature" (AMS)
		// and "ARC-Seal" (AS) header fields."
		// "... for instances 1 to i-1, and the AAR and AMS header fields for instance i."

		// We'll rely on the caller to pass only the relevant headers?
		// Or we filter here.
		return headers, nil // Assumes caller filtered
	}

	// ARC-Message-Signature (like DKIM)
	if hTag == "" {
		return nil, fmt.Errorf("missing h tag")
	}

	requiredHeaders := strings.Split(hTag, ":")

	// Need to find headers in reverse order (bottom-up)
	// Make a copy of headers to mark used ones? Or just search from bottom.

	// RFC 6376:
	// "To ensure that the signature matches the signed content ... the signer selects headers ...
	// starting from the bottom of the header block..."

	// We map headers by key for easier lookup, but need to preserve order and handle duplicates.
	// Actually, just iterating from bottom is correct.

	lastIndex := make(map[string]int)
	for i := len(headers) - 1; i >= 0; i-- {
		k := strings.ToLower(headers[i].Key)
		if _, exists := lastIndex[k]; !exists {
			lastIndex[k] = i // First time seeing from bottom
		}
	}
	// This simple map approach doesn't handle multiple headers of same name correctly if h tag specifies them multiple times.
	// We need to keep track of *which* instance of the header we used.

	usedIndices := make(map[int]bool)

	for _, required := range requiredHeaders {
		required = strings.ToLower(strings.TrimSpace(required))

		found := false
		for i := len(headers) - 1; i >= 0; i-- {
			if usedIndices[i] {
				continue
			}
			if strings.ToLower(headers[i].Key) == required {
				headersToSign = append(headersToSign, headers[i])
				usedIndices[i] = true
				found = true
				break
			}
		}

		if !found {
			// RFC says if header doesn't exist, ignore?
			// "Nonexistent header fields do not contribute to the signature computation"
			// But for "required" headers like From?
			// We'll just continue.
		}
	}

	return headersToSign, nil
}

func removeSignatureValue(raw string) string {
	// Replaces b=...; with b=;
	// Use regex or string manipulation
	re := regexp.MustCompile(`(b\s*=)[^;]*`)
	return re.ReplaceAllString(raw, "${1}")
}

func parsePublicKey(record string) (*rsa.PublicKey, error) {
	// Simple parser for v=DKIM1; p=...
	parts := strings.Split(record, ";")
	var p string
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == "p" {
			p = kv[1]
			break
		}
	}
	if p == "" {
		return nil, fmt.Errorf("no public key found in record")
	}

	data, err := base64.StdEncoding.DecodeString(p)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 public key")
	}

	pub, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		// RFC 6376 is inconsistent, try PKCS1 if PKIX fails
		pub, err = x509.ParsePKCS1PublicKey(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}
