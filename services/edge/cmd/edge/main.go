package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	eslAddr := envOr("FREESWITCH_ESL_ADDR", "127.0.0.1:8021")
	eslPass := envOr("FREESWITCH_ESL_PASSWORD", "ClueCon")
	gatewayDir := envOr("FREESWITCH_GATEWAY_DIR", "/etc/freeswitch/gateways")

	esl := fs.NewClient(eslAddr, eslPass)
	go esl.RunReconnect(ctx)

	var reloader *fs.Reloader
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		keyRaw := os.Getenv("CARRIER_SECRET_KEY")
		if keyRaw == "" {
			log.Fatal("CARRIER_SECRET_KEY is required when DATABASE_URL is set")
		}
		key, err := cryptokit.ParseKey(keyRaw)
		if err != nil {
			log.Fatalf("CARRIER_SECRET_KEY: %v", err)
		}
		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		db.SetMaxOpenConns(5)
		db.SetConnMaxLifetime(time.Minute)
		reloader = &fs.Reloader{
			ESL:        esl,
			GatewayDir: gatewayDir,
			Loader:     &fs.CarrierLoader{DB: db},
			SecretKey:  key,
		}
		go func() {
			// Wait briefly for ESL reconnect before first apply.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			if err := reloader.ReloadAll(ctx); err != nil {
				log.Printf("fs gateways: boot reload: %v", err)
			} else {
				log.Printf("fs gateways: boot reload complete")
			}
		}()
	} else {
		log.Printf("DATABASE_URL unset; skipping BYOC gateway apply")
	}

	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" && reloader != nil {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("REDIS_URL: %v", err)
		}
		rdb := redis.NewClient(opt)
		go subscribeCarriersChanged(ctx, rdb, reloader)
	}

	srv := &http.Server{Addr: ":8081", Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		esl.Close()
	}()

	log.Printf("edge listening on :8081 (esl=%s gateway_dir=%s)", eslAddr, gatewayDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func subscribeCarriersChanged(ctx context.Context, rdb *redis.Client, reloader *fs.Reloader) {
	pubsub := rdb.Subscribe(ctx, "carriers.changed")
	defer pubsub.Close()

	ch := pubsub.Channel()
	log.Printf("subscribed to redis channel carriers.changed")
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			log.Printf("carriers.changed: %s — reloading gateways", msg.Payload)
			if err := reloader.ReloadAll(ctx); err != nil {
				log.Printf("fs gateways: reload: %v", err)
			}
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
