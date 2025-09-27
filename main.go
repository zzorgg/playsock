package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
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
		QueueTimeout:   5 * time.Minute,
		Redis:          redisCfg,
	}

	wsServer := server.New(cfg)
	http.HandleFunc("/ws", wsServer.HandleWS)

	log.Printf("playsock websocket server listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
