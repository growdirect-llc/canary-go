// internal/config/config.go
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL           string
	ValkeyURL             string
	InternalServiceSecret string
	SessionSecret         string
	LogLevel              string
	Port                  string
	ServiceName           string
	PublicURL             string // base URL for discovery doc (e.g. https://demo.growdirect.io)

	// Reference-tier cache (T3A.1 / GRO-894). Default off — enable per
	// service via TIER_REFERENCE_CACHE=1. TTL via
	// TIER_REFERENCE_CACHE_TTL=Ns (default 60s, matches spec
	// §"Per-tier infrastructure" reference row "Long TTL (~60s hot)").
	TierReferenceCache    bool
	TierReferenceCacheTTL time.Duration
}

// Load reads required environment variables and panics on missing ones.
// Call at service startup before any other initialization.
func Load(serviceName string) *Config {
	cfg := &Config{
		DatabaseURL:           require("DATABASE_URL"),
		ValkeyURL:             require("VALKEY_URL"),
		InternalServiceSecret: require("INTERNAL_SERVICE_SECRET"),
		SessionSecret:         require("SESSION_SECRET"),
		LogLevel:              getOr("LOG_LEVEL", "info"),
		Port:                  getOr("PORT", "8080"),
		ServiceName:           serviceName,
		PublicURL:             getOr("PUBLIC_URL", ""),
		TierReferenceCache:    getBool("TIER_REFERENCE_CACHE", false),
		TierReferenceCacheTTL: getDuration("TIER_REFERENCE_CACHE_TTL", 60*time.Second),
	}
	return cfg
}

func require(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return v
}

func getOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getBool reads an env var as a bool. Anything matching "1", "true", "TRUE",
// or "yes" is true; otherwise the default applies. Used for feature flags.
func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	case "0", "false", "FALSE", "no", "NO":
		return false
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return def
}

// getDuration parses a duration env var (e.g. "60s", "5m"). Default applies
// when unset or unparseable.
func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}
