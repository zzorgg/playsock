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

	var redisCfg *server.RedisConfig
	if addr := os.Getenv("PLAYSOCK_REDIS_ADDR"); addr != "" {
		rc := &server.RedisConfig{Addr: addr}
		if user := os.Getenv("PLAYSOCK_REDIS_USERNAME"); user != "" {
			rc.Username = user
		}
		if pass := os.Getenv("PLAYSOCK_REDIS_PASSWORD"); pass != "" {
			rc.Password = pass
		}
		if dbRaw := os.Getenv("PLAYSOCK_REDIS_DB"); dbRaw != "" {
			if db, err := strconv.Atoi(dbRaw); err == nil {
				rc.DB = db
			} else {
				log.Printf("invalid PLAYSOCK_REDIS_DB value %q: %v", dbRaw, err)
			}
		}
		if key := os.Getenv("PLAYSOCK_REDIS_QUEUE_KEY"); key != "" {
			rc.QueueKey = key
		}
		if prefix := os.Getenv("PLAYSOCK_REDIS_SESSION_PREFIX"); prefix != "" {
			rc.SessionKeyPrefix = prefix
		}
		if timeoutRaw := os.Getenv("PLAYSOCK_REDIS_TIMEOUT"); timeoutRaw != "" {
			if dur, err := time.ParseDuration(timeoutRaw); err == nil {
				rc.OperationTimeout = dur
			} else {
				log.Printf("invalid PLAYSOCK_REDIS_TIMEOUT value %q: %v", timeoutRaw, err)
			}
		}
		redisCfg = rc
	}

	cfg := server.Config{
		AllowedOrigins: origins,
		QueueTimeout:   queueTimeout,
		Redis:          redisCfg,
	}

	wsServer := server.New(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.HandleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

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
