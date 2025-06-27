package config

import "time"

// Config holds all configuration for the SMTP relay server
type Config struct {
	SMTP        SMTPConfig        `toml:"smtp"`
	S3          S3Config          `toml:"s3"`
	Destination DestinationConfig `toml:"destination"`
	TLS         TLSConfig         `toml:"tls"`
	Domains     DomainsConfig     `toml:"domains"`
	Blacklists  BlacklistsConfig  `toml:"blacklists"`
	LogFormat   string            `toml:"log_format"` // "json" or "text"
	Local       bool              `toml:"local"`      // Local development mode
}

// SMTPConfig holds SMTP server configuration
type SMTPConfig struct {
	ListenAddr      string        `toml:"listen_addr"`
	Domain          string        `toml:"domain"`
	MaxMessageSize  int           `toml:"max_message_size"`
	TimeoutDuration time.Duration `toml:"timeout_duration"`
	MinTLSVersion   string        `toml:"min_tls_version"` // Minimum TLS version: "1.2" or "1.3" (TLS 1.0/1.1 not supported)
}

// S3Config holds S3 configuration for certificate storage
type S3Config struct {
	Endpoint        string `toml:"endpoint"`
	Bucket          string `toml:"bucket"`
	Prefix          string `toml:"prefix"`
	AccessKeyID     string `toml:"access_key_id"`
	SecretAccessKey string `toml:"secret_access_key"`
	Region          string `toml:"region"`
}

// DestinationConfig holds configuration for the HTTP destination endpoint
type DestinationConfig struct {
	URL              string `toml:"url"`
	APIKey           string `toml:"api_key"`
	MaxRetryAttempts int    `toml:"max_retry_attempts"`
}

// DomainsConfig holds configuration for domains validation
type DomainsConfig struct {
	URL    string `toml:"url"`     // URL or file path to fetch valid domains list (JSON array)
	APIKey string `toml:"api_key"` // API key for authentication (optional)
}

// BlacklistsConfig holds configuration for DNS blacklists
type BlacklistsConfig struct {
	Enabled           bool          `toml:"enabled"`             // Whether to enable blacklist checking
	Lists             []string      `toml:"lists"`               // DNS blacklist servers to check
	Timeout           time.Duration `toml:"timeout"`             // Timeout for blacklist queries
	CheckHELOResolves bool          `toml:"check_helo_resolves"` // Whether to check if HELO hostname resolves
}

// TLSConfig holds TLS/certificate configuration
type TLSConfig struct {
	Email            string `toml:"email"`             // Email for Let's Encrypt
	UseProduction    bool   `toml:"use_production"`    // Use Let's Encrypt production (vs staging)
	UseLocalCA       bool   `toml:"use_local_ca"`      // Use local CA for testing
	CertMagicVerbose bool   `toml:"certmagic_verbose"` // Enable verbose certmagic logging
}

// DefaultConfig returns a Config with default values
func DefaultConfig() *Config {
	return &Config{
		SMTP: SMTPConfig{
			ListenAddr:      ":25",
			Domain:          "mail.yourdomain.com",
			MaxMessageSize:  10 << 20, // 10 MB
			TimeoutDuration: 10 * time.Second,
			MinTLSVersion:   "1.2", // Default to TLS 1.2
		},
		S3: S3Config{
			Endpoint: "s3.amazonaws.com",
			Bucket:   "email-mx-certs",
			Prefix:   "certs/",
			Region:   "us-east-1",
		},
		Destination: DestinationConfig{
			MaxRetryAttempts: 3, // Default to 3 retry attempts
		},
		TLS: TLSConfig{
			UseProduction:    true,
			CertMagicVerbose: false,
		},
		Blacklists: BlacklistsConfig{
			Enabled:           true,
			Lists:             []string{"zen.spamhaus.org"},
			Timeout:           3 * time.Second,
			CheckHELOResolves: false,
		},
		LogFormat: "text",
		Local:     false,
	}
}
