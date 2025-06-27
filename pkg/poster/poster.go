package poster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// HTTPClient for posting to a destination
var HTTPClient = &http.Client{
	Timeout: 30 * time.Second, // Allow more time for response
}

// PostEmailToDestination sends the raw email content to the destination with retry logic
func PostEmailToDestination(rawEmail string, destinationURL, apiKey string, maxRetryAttempts int) error {
	return PostEmailToDestinationWithContext(context.Background(), rawEmail, destinationURL, apiKey, maxRetryAttempts, false)
}

// PostEmailToDestinationWithContext sends the raw email content to the destination with retry logic and context support
func PostEmailToDestinationWithContext(ctx context.Context, rawEmail string, destinationURL, apiKey string, maxRetryAttempts int, isJunk bool) error {
	var lastErr error

	// Ensure at least one attempt
	if maxRetryAttempts < 1 {
		maxRetryAttempts = 1
	}

	for attempt := 0; attempt < maxRetryAttempts; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		// Calculate backoff delay (exponential: 0s, 1s, 2s, 4s, 8s...)
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			log.Printf("Retrying HTTP post to URL (attempt %d/%d) after %v delay", attempt+1, maxRetryAttempts, backoff)

			// Sleep with context
			select {
			case <-time.After(backoff):
				// Continue after backoff
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
			}
		}

		err := postEmailAttemptWithContext(ctx, rawEmail, destinationURL, apiKey, isJunk)
		if err == nil {
			// Success
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			log.Printf("Non-retryable error posting to URL: %v", err)
			return err
		}

		if attempt < maxRetryAttempts-1 {
			log.Printf("Retryable error posting to URL (attempt %d/%d): %v", attempt+1, maxRetryAttempts, err)
		}
	}

	// All retries exhausted
	log.Printf("All retry attempts exhausted (%d/%d) posting to URL: %v", maxRetryAttempts, maxRetryAttempts, lastErr)
	return fmt.Errorf("failed after %d attempts: %w", maxRetryAttempts, lastErr)
}

// postEmailAttemptWithContext performs a single attempt to post the email with context support
func postEmailAttemptWithContext(ctx context.Context, rawEmail string, destinationURL, apiKey string, isJunk bool) error {
	req, err := http.NewRequestWithContext(ctx, "POST", destinationURL, strings.NewReader(rawEmail))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "message/rfc822") // Standard MIME type for raw email
	req.Header.Set("X-API-Key", apiKey)              // Custom header for API key authentication

	// Add X-Junk header if message is marked as junk
	if isJunk {
		req.Header.Set("X-Junk", "yes")
	}

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request to URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return NewHTTPStatusError(resp.StatusCode, string(bodyBytes))
	}

	log.Printf("Successfully sent email to destination URL, status: %d", resp.StatusCode)
	return nil
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's an HTTP status error
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.IsRetryable()
	}

	// Check if it's a context error (non-retryable)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are generally retryable
	// Check for common network error patterns in the error chain
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "dial tcp") {
		return true
	}

	// Default to retryable for unknown errors
	return true
}
