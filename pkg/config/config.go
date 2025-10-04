package config

import (
	"errors"
	"fmt"
	"time"
)

// Validate checks the configuration for required fields and placeholder values.
func (c *Config) Validate() error {
	// Validate retry attempts (prevent infinite loops and excessive delays)
	if c.Destination.MaxRetryAttempts > 5 {
		return fmt.Errorf("destination.max_retry_attempts must be <= 5 (got %d) to prevent excessive delays", c.Destination.MaxRetryAttempts)
	}
	if c.Destination.MaxRetryAttempts < 1 {
		return fmt.Errorf("destination.max_retry_attempts must be >= 1 (got %d)", c.Destination.MaxRetryAttempts)
	}

	// Validate HTTP timeout
	if c.Destination.HTTPTimeout < 1*time.Second {
		return fmt.Errorf("destination.http_timeout must be >= 1s (got %v)", c.Destination.HTTPTimeout)
	}
	if c.Destination.HTTPTimeout > 5*time.Minute {
		return fmt.Errorf("destination.http_timeout must be <= 5m (got %v) to prevent blocking SMTP sessions", c.Destination.HTTPTimeout)
	}

	// Validate distributed tracking settings
	if c.SMTP.Distributed.Enabled {
		if !c.Cluster.Enabled {
			return errors.New("smtp.distributed.enabled requires cluster.enabled=true")
		}
		if c.SMTP.Distributed.RecipientCacheTTL < 1*time.Minute {
			return fmt.Errorf("smtp.distributed.recipient_cache_ttl must be >= 1m (got %v)", c.SMTP.Distributed.RecipientCacheTTL)
		}
	}

	// Production mode validations
	if c.Local {
		return nil // In local mode, remaining checks are skipped.
	}

	if c.SMTP.Domain == "" || c.SMTP.Domain == "mail.example.com" {
		return errors.New("smtp.domain must be set")
	}
	if c.Destination.URL == "" {
		return errors.New("destination.url must be set")
	}
	if c.Destination.APIKey == "" || c.Destination.APIKey == "your-api-key-here" {
		return errors.New("destination.api_key must be set")
	}
	if c.S3.AccessKeyID == "" || c.S3.AccessKeyID == "your-s3-access-key-id" {
		return errors.New("s3.access_key_id must be set")
	}
	if c.S3.SecretAccessKey == "" || c.S3.SecretAccessKey == "your-s3-secret-access-key" {
		return errors.New("s3.secret_access_key must be set")
	}
	if c.TLS.Email == "" || c.TLS.Email == "admin@example.com" {
		return errors.New("tls.email must be set for Let's Encrypt certificate management")
	}

	return nil
}
