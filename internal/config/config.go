// internal/config/config.go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL           string
	ValkeyURL             string
	InternalServiceSecret string
	SessionSecret         string
	LogLevel              string
	Port                  string
	ServiceName           string
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
