// Package config handles loading and validating the aggregator configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Polling PollingConfig `yaml:"polling"`
	Feeds   []FeedConfig  `yaml:"feeds"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Addr              string      `yaml:"addr"`
	Title             string      `yaml:"title"`
	Auth              *AuthConfig `yaml:"auth,omitempty"`
	DefaultMaxEntries int         `yaml:"default_max_entries"` // default entries per page (0 = unlimited)
}

// AuthConfig holds Basic Auth credentials.
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PollingConfig controls feed polling.
type PollingConfig struct {
	Interval string `yaml:"interval"`
}

// ParsedInterval returns the polling interval as a time.Duration.
func (p PollingConfig) ParsedInterval() (time.Duration, error) {
	if p.Interval == "" {
		return 6 * time.Hour, nil
	}
	d, err := time.ParseDuration(p.Interval)
	if err != nil {
		return 0, fmt.Errorf("config: invalid polling interval %q: %w", p.Interval, err)
	}
	return d, nil
}

// FeedConfig describes a single upstream OPDS feed.
type FeedConfig struct {
	Name        string      `yaml:"name"`
	URL         string      `yaml:"url"`
	Auth        *AuthConfig `yaml:"auth,omitempty"`
	PollDepth   int         `yaml:"poll_depth"`
	MaxEntries  int         `yaml:"max_entries"`  // max entries per page (0 = use server default)
	MaxPaginate int         `yaml:"max_paginate"` // max upstream pages to follow (0 = all)
}

// Slug returns a URL-safe identifier for the feed.
func (f FeedConfig) Slug() string {
	slug := make([]byte, 0, len(f.Name))
	for _, c := range []byte(f.Name) {
		switch {
		case c >= 'a' && c <= 'z':
			slug = append(slug, c)
		case c >= 'A' && c <= 'Z':
			slug = append(slug, c+32) // lowercase
		case c >= '0' && c <= '9':
			slug = append(slug, c)
		case c == ' ' || c == '-' || c == '_':
			if len(slug) == 0 || slug[len(slug)-1] != '-' {
				slug = append(slug, '-')
			}
		}
	}
	// trim trailing dash
	if len(slug) > 0 && slug[len(slug)-1] == '-' {
		slug = slug[:len(slug)-1]
	}
	return string(slug)
}

// Load reads config from the given YAML file path, applies environment
// variable overrides, then applies defaults and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.ApplyEnv()
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadFromEnv builds a Config entirely from environment variables,
// applies defaults, and validates. Used when no YAML file is available.
func LoadFromEnv() (*Config, error) {
	var cfg Config
	cfg.ApplyEnv()
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ApplyEnv overlays environment variables onto the config.
// For scalar fields, non-empty env vars override existing values.
// For feeds, if any OPDS_FEED_* vars are set they replace all YAML-defined feeds.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("OPDS_SERVER_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv("OPDS_SERVER_TITLE"); v != "" {
		c.Server.Title = v
	}
	if v := os.Getenv("OPDS_SERVER_DEFAULT_MAX_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.DefaultMaxEntries = n
		}
	}
	if v := os.Getenv("OPDS_POLLING_INTERVAL"); v != "" {
		c.Polling.Interval = v
	}

	// Server auth from env.
	authUser := os.Getenv("OPDS_AUTH_USERNAME")
	authPass := os.Getenv("OPDS_AUTH_PASSWORD")
	if authUser != "" || authPass != "" {
		c.Server.Auth = &AuthConfig{Username: authUser, Password: authPass}
	}

	// Feeds from env (indexed, replaces YAML feeds if any are defined).
	if feeds := feedsFromEnv(); len(feeds) > 0 {
		c.Feeds = feeds
	}
}

// feedsFromEnv scans OPDS_FEED_0_NAME, OPDS_FEED_1_NAME, ... and builds
// FeedConfig entries. Stops at the first index where _NAME is not set.
func feedsFromEnv() []FeedConfig {
	var feeds []FeedConfig
	for i := 0; ; i++ {
		prefix := fmt.Sprintf("OPDS_FEED_%d_", i)
		name := os.Getenv(prefix + "NAME")
		if name == "" {
			break
		}
		fc := FeedConfig{
			Name: name,
			URL:  os.Getenv(prefix + "URL"),
		}
		if v := os.Getenv(prefix + "POLL_DEPTH"); v != "" {
			if depth, err := strconv.Atoi(v); err == nil {
				fc.PollDepth = depth
			}
		}
		if v := os.Getenv(prefix + "MAX_ENTRIES"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				fc.MaxEntries = n
			}
		}
		if v := os.Getenv(prefix + "MAX_PAGINATE"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				fc.MaxPaginate = n
			}
		}
		feedUser := os.Getenv(prefix + "AUTH_USERNAME")
		feedPass := os.Getenv(prefix + "AUTH_PASSWORD")
		if feedUser != "" || feedPass != "" {
			fc.Auth = &AuthConfig{Username: feedUser, Password: feedPass}
		}
		feeds = append(feeds, fc)
	}
	return feeds
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.Title == "" {
		c.Server.Title = "OPDS Aggregator"
	}
	if c.Polling.Interval == "" {
		c.Polling.Interval = "6h"
	}
}

func (c *Config) validate() error {
	if len(c.Feeds) == 0 {
		return fmt.Errorf("config: at least one feed must be configured")
	}
	slugs := make(map[string]bool)
	for i, f := range c.Feeds {
		if f.Name == "" {
			return fmt.Errorf("config: feed[%d]: name is required", i)
		}
		if f.URL == "" {
			return fmt.Errorf("config: feed[%d] (%s): url is required", i, f.Name)
		}
		slug := f.Slug()
		if slugs[slug] {
			return fmt.Errorf("config: feed[%d] (%s): duplicate slug %q", i, f.Name, slug)
		}
		slugs[slug] = true
	}
	return nil
}

// DefaultConfigPaths returns the list of paths to check for configuration,
// in order of priority.
func DefaultConfigPaths() []string {
	paths := []string{"config.yaml", "config.yml"}

	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".config", "opds-aggregator", "config.yaml"),
			filepath.Join(home, ".config", "opds-aggregator", "config.yml"),
		)
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths,
			filepath.Join(xdg, "opds-aggregator", "config.yaml"),
			filepath.Join(xdg, "opds-aggregator", "config.yml"),
		)
	}

	return paths
}

// FindConfig returns the first existing config file from the default paths,
// or an empty string if none found.
func FindConfig() string {
	for _, p := range DefaultConfigPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
