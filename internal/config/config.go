package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds everything the app needs, sourced from the .env file.
type Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	CFAPIToken      string // optional: enables public-link detection
	Port            string
}

// Load reads configuration from the process environment. The Makefile / wrapper
// is responsible for exporting the .env file before the binary starts.
func Load() (*Config, error) {
	c := &Config{
		AccountID:       strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID")),
		AccessKeyID:     strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY")),
		CFAPIToken:      strings.TrimSpace(os.Getenv("CF_API_TOKEN")),
		Port:            strings.TrimSpace(os.Getenv("PORT")),
	}
	if c.Port == "" {
		c.Port = "8787"
	}

	var missing []string
	if c.AccountID == "" {
		missing = append(missing, "R2_ACCOUNT_ID")
	}
	if c.AccessKeyID == "" {
		missing = append(missing, "R2_ACCESS_KEY_ID")
	}
	if c.SecretAccessKey == "" {
		missing = append(missing, "R2_SECRET_ACCESS_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s (did you copy .env.example to .env and fill it in?)", strings.Join(missing, ", "))
	}

	return c, nil
}

// S3Endpoint is the R2 S3-compatible API endpoint for this account.
func (c *Config) S3Endpoint() string {
	return fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.AccountID)
}

// HasCFToken reports whether public-link detection is available.
func (c *Config) HasCFToken() bool {
	return c.CFAPIToken != ""
}
