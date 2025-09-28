package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"playsock/internal/server"
)

func main() {
	addr := os.Getenv("PLAYSOCK_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	originsEnv := os.Getenv("PLAYSOCK_ALLOWED_ORIGINS")
	var origins []string
	if originsEnv != "" {
		for _, origin := range strings.Split(originsEnv, ",") {
			trimmed := strings.TrimSpace(origin)
			if trimmed != "" {
				origins = append(origins, trimmed)
			}
		}
	}

	queueTimeout := 5 * time.Minute
	if timeoutRaw := os.Getenv("PLAYSOCK_QUEUE_TIMEOUT"); timeoutRaw != "" {
		if dur, err := time.ParseDuration(timeoutRaw); err == nil && dur > 0 {
			queueTimeout = dur
		} else {
			log.Printf("invalid PLAYSOCK_QUEUE_TIMEOUT value %q: %v", timeoutRaw, err)
		}
	}

	// Valkey configuration (new) with backward compatibility for legacy PLAYSOCK_REDIS_* vars.
	// New preferred variables: PLAYSOCK_VALKEY_*
	// Fallback to PLAYSOCK_REDIS_* if VALKEY versions are absent.
	get := func(primary, legacy string) string {
		if v := os.Getenv(primary); v != "" {
			return v
		}
		return os.Getenv(legacy)
	}

	var valkeyCfg *server.ValkeyConfig
	if addr := get("PLAYSOCK_VALKEY_ADDR", "PLAYSOCK_REDIS_ADDR"); addr != "" {
		vc := &server.ValkeyConfig{Addr: addr}
		if user := get("PLAYSOCK_VALKEY_USERNAME", "PLAYSOCK_REDIS_USERNAME"); user != "" {
			vc.Username = user
		}
		if pass := get("PLAYSOCK_VALKEY_PASSWORD", "PLAYSOCK_REDIS_PASSWORD"); pass != "" {
			vc.Password = pass
		}
		if dbRaw := get("PLAYSOCK_VALKEY_DB", "PLAYSOCK_REDIS_DB"); dbRaw != "" {
			if db, err := strconv.Atoi(dbRaw); err == nil {
				vc.DB = db
			} else {
				log.Printf("invalid VALKEY/REDIS DB value %q: %v", dbRaw, err)
			}
		}
		if key := get("PLAYSOCK_VALKEY_QUEUE_KEY", "PLAYSOCK_REDIS_QUEUE_KEY"); key != "" {
			vc.QueueKey = key
		}
		if prefix := get("PLAYSOCK_VALKEY_SESSION_PREFIX", "PLAYSOCK_REDIS_SESSION_PREFIX"); prefix != "" {
			vc.SessionKeyPrefix = prefix
		}
		if timeoutRaw := get("PLAYSOCK_VALKEY_TIMEOUT", "PLAYSOCK_REDIS_TIMEOUT"); timeoutRaw != "" {
			if dur, err := time.ParseDuration(timeoutRaw); err == nil {
				vc.OperationTimeout = dur
			} else {
				log.Printf("invalid VALKEY/REDIS TIMEOUT value %q: %v", timeoutRaw, err)
			}
		}
		valkeyCfg = vc
	}

	cfg := server.Config{
		AllowedOrigins: origins,
		QueueTimeout:   queueTimeout,
		Valkey:         valkeyCfg,
		Redis:          nil, // deprecated field intentionally left nil
	}

	wsServer := server.New(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.HandleWS)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTimeout := 10 * time.Second
	if timeoutRaw := os.Getenv("PLAYSOCK_SHUTDOWN_TIMEOUT"); timeoutRaw != "" {
		if dur, err := time.ParseDuration(timeoutRaw); err == nil && dur > 0 {
			shutdownTimeout = dur
		} else {
			log.Printf("invalid PLAYSOCK_SHUTDOWN_TIMEOUT value %q: %v", timeoutRaw, err)
		}
	}

	go func() {
		<-ctx.Done()
		stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}()

	log.Printf("playsock websocket server listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}

	log.Printf("server shut down cleanly")
}
