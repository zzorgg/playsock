package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"playsock/internal/server"
)

// AppConfig holds runtime configuration for the HTTP server and supporting services.
type AppConfig struct {
	Address         string
	ShutdownTimeout time.Duration
	Server          server.Config
}

// Load reads environment variables and constructs an AppConfig with sane defaults.
func Load() AppConfig {
	cfg := AppConfig{
		Address:         firstNonEmpty(os.Getenv("PLAYSOCK_ADDR"), ":8080"),
		ShutdownTimeout: parseDurationEnv("PLAYSOCK_SHUTDOWN_TIMEOUT", 10*time.Second, true),
		Server: server.Config{
			AllowedOrigins: parseCSV(os.Getenv("PLAYSOCK_ALLOWED_ORIGINS")),
			QueueTimeout:   parseDurationEnv("PLAYSOCK_QUEUE_TIMEOUT", 5*time.Minute, false),
			HandshakeTimeout: parseDurationEnv(
				"PLAYSOCK_HANDSHAKE_TIMEOUT", 5*time.Second, true,
			),
			MaxConnectionsPerIP: parseIntEnv("PLAYSOCK_MAX_CONNECTIONS_PER_IP", 32),
			AuthTokens:          parseCSV(os.Getenv("PLAYSOCK_AUTH_TOKENS")),
		},
	}

	cfg.Server.Valkey = buildValkeyConfig()
	return cfg
}

func parseDurationEnv(key string, fallback time.Duration, allowZero bool) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("invalid %s value %q: %v", key, raw, err)
		return fallback
	}
	if dur <= 0 && !allowZero {
		log.Printf("non-positive %s value %q, using default", key, raw)
		return fallback
	}
	return dur
}

func parseIntEnv(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		log.Printf("invalid %s value %q: %v", key, raw, err)
		return fallback
	}
	return v
}

func parseCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func buildValkeyConfig() *server.ValkeyConfig {
	get := func(primary, legacy string) string {
		if v := os.Getenv(primary); v != "" {
			return v
		}
		return os.Getenv(legacy)
	}

	addr := get("PLAYSOCK_VALKEY_ADDR", "PLAYSOCK_REDIS_ADDR")
	if addr == "" {
		return nil
	}

	cfg := &server.ValkeyConfig{Addr: addr}

	if user := get("PLAYSOCK_VALKEY_USERNAME", "PLAYSOCK_REDIS_USERNAME"); user != "" {
		cfg.Username = user
	}
	if pass := get("PLAYSOCK_VALKEY_PASSWORD", "PLAYSOCK_REDIS_PASSWORD"); pass != "" {
		cfg.Password = pass
	}
	if dbRaw := get("PLAYSOCK_VALKEY_DB", "PLAYSOCK_REDIS_DB"); dbRaw != "" {
		if db, err := strconv.Atoi(dbRaw); err == nil {
			cfg.DB = db
		} else {
			log.Printf("invalid VALKEY/REDIS DB value %q: %v", dbRaw, err)
		}
	}
	if key := get("PLAYSOCK_VALKEY_QUEUE_KEY", "PLAYSOCK_REDIS_QUEUE_KEY"); key != "" {
		cfg.QueueKey = key
	}
	if prefix := get("PLAYSOCK_VALKEY_SESSION_PREFIX", "PLAYSOCK_REDIS_SESSION_PREFIX"); prefix != "" {
		cfg.SessionKeyPrefix = prefix
	}
	if timeoutRaw := get("PLAYSOCK_VALKEY_TIMEOUT", "PLAYSOCK_REDIS_TIMEOUT"); timeoutRaw != "" {
		if dur, err := time.ParseDuration(timeoutRaw); err == nil {
			cfg.OperationTimeout = dur
		} else {
			log.Printf("invalid VALKEY/REDIS TIMEOUT value %q: %v", timeoutRaw, err)
		}
	}

	return cfg
}
