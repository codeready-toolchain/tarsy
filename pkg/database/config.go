package database

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// LoadConfigFromEnv loads database configuration from environment variables
func LoadConfigFromEnv() (Config, error) {
	port, err := strconv.Atoi(getEnvOrDefault("DB_PORT", "5432"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_PORT: %w", err)
	}

	maxOpen, _ := strconv.Atoi(getEnvOrDefault("DB_MAX_OPEN_CONNS", "10"))
	maxIdle, _ := strconv.Atoi(getEnvOrDefault("DB_MAX_IDLE_CONNS", "5"))

	return Config{
		Host:            getEnvOrDefault("DB_HOST", "localhost"),
		Port:            port,
		User:            getEnvOrDefault("DB_USER", "tarsy"),
		Password:        os.Getenv("DB_PASSWORD"),
		Database:        getEnvOrDefault("DB_NAME", "tarsy"),
		SSLMode:         getEnvOrDefault("DB_SSLMODE", "disable"),
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
