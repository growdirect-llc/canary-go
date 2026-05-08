// internal/config/config.go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	// IdentityDatabaseURL points at canary_identity_gcp. Optional in
	// the shared config (most services don't connect to it); the
	// identity binary asserts non-empty at startup. See
	// Brain/wiki/cards/platform-identity-database-boundary.md.
	IdentityDatabaseURL   string
	ValkeyURL             string
	InternalServiceSecret string
	SessionSecret         string
	LogLevel              string
	Port                  string
	ServiceName           string
	PublicURL             string // base URL for discovery doc (e.g. https://demo.growdirect.io)
}

// Load reads required environment variables and panics on missing ones.
// Call at service startup before any other initialization.
func Load(serviceName string) *Config {
	cfg := &Config{
		DatabaseURL:           require("DATABASE_URL"),
		IdentityDatabaseURL:   getOr("IDENTITY_DATABASE_URL", ""),
		ValkeyURL:             require("VALKEY_URL"),
		InternalServiceSecret: require("INTERNAL_SERVICE_SECRET"),
		SessionSecret:         require("SESSION_SECRET"),
		LogLevel:              getOr("LOG_LEVEL", "info"),
		Port:                  getOr("PORT", "8080"),
		ServiceName:           serviceName,
		PublicURL:             getOr("PUBLIC_URL", ""),
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
