package database

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// LoadConfigFromEnv loads database configuration from environment variables
// with validation and production-ready defaults
func LoadConfigFromEnv() (Config, error) {
	port, err := strconv.Atoi(getEnvOrDefault("DB_PORT", "5432"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_PORT: %w", err)
	}

	// Production defaults: 25 max open, 10 max idle
	maxOpen, _ := strconv.Atoi(getEnvOrDefault("DB_MAX_OPEN_CONNS", "25"))
	maxIdle, _ := strconv.Atoi(getEnvOrDefault("DB_MAX_IDLE_CONNS", "10"))

	// Parse durations with production defaults
	maxLifetime, err := parseDuration(getEnvOrDefault("DB_CONN_MAX_LIFETIME", "1h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %w", err)
	}

	maxIdleTime, err := parseDuration(getEnvOrDefault("DB_CONN_MAX_IDLE_TIME", "15m"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_CONN_MAX_IDLE_TIME: %w", err)
	}

	cfg := Config{
		Host:            getEnvOrDefault("DB_HOST", "localhost"),
		Port:            port,
		User:            getEnvOrDefault("DB_USER", "tarsy"),
		Password:        os.Getenv("DB_PASSWORD"),
		Database:        getEnvOrDefault("DB_NAME", "tarsy"),
		SSLMode:         getEnvOrDefault("DB_SSLMODE", "disable"),
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: maxLifetime,
		ConnMaxIdleTime: maxIdleTime,
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if c.Password == "" {
		return fmt.Errorf("DB_PASSWORD is required")
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS (%d) cannot exceed DB_MAX_OPEN_CONNS (%d)",
			c.MaxIdleConns, c.MaxOpenConns)
	}
	if c.MaxOpenConns < 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be at least 1")
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("DB_MAX_IDLE_CONNS cannot be negative")
	}
	return nil
}

// parseDuration parses a duration string, supporting common formats
func parseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
