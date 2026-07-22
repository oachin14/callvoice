package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/cpsgate"
	"github.com/callvoice/callvoice/services/edge/internal/dialer"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
	"github.com/callvoice/callvoice/services/edge/internal/httpapi"
	"github.com/callvoice/callvoice/services/edge/internal/inbound"
	"github.com/callvoice/callvoice/services/edge/internal/live"
	"github.com/callvoice/callvoice/services/edge/internal/webrtccred"
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
	directoryDir := envOr("FREESWITCH_DIRECTORY_DIR", "/etc/freeswitch/directory/default")
	wssURL := envOr("FREESWITCH_WSS_URL", "wss://localhost:7443")
	sipDomain := envOr("FREESWITCH_SIP_DOMAIN", "localhost")

	esl := fs.NewClient(eslAddr, eslPass)
	go esl.RunReconnect(ctx)

	var db *sql.DB
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
		var errDB error
		db, errDB = sql.Open("postgres", dbURL)
		if errDB != nil {
			log.Fatalf("database: %v", errDB)
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
		log.Printf("DATABASE_URL unset; skipping BYOC gateway apply and agent auth")
	}

	var rdb *redis.Client
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("REDIS_URL: %v", err)
		}
		rdb = redis.NewClient(opt)
		if reloader != nil {
			go subscribeCarriersChanged(ctx, rdb, reloader)
		}
	}

	var handler http.Handler = mux
	if db != nil && rdb != nil {
		sessionTTL := 2 * time.Hour
		pres := agent.NewPresence(rdb, sessionTTL)
		requireAdmin2FA := true
		if raw := os.Getenv("REQUIRE_ADMIN_2FA"); raw != "" {
			requireAdmin2FA = raw == "1" || strings.EqualFold(raw, "true")
		}
		globalCPS := 0
		if raw := os.Getenv("GLOBAL_MAX_CPS"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				globalCPS = n
			}
		}
		carrierLoader := &fs.CarrierLoader{DB: db}
		manualDialer := &dialer.Manual{
			ESL:          esl,
			Gate:         cpsgate.New(rdb),
			RDB:          rdb,
			Carriers:     carrierLoader,
			Pres:         pres,
			GlobalMaxCPS: globalCPS,
		}
		hub := live.NewHub()
		agentSrv := &httpapi.AgentServer{
			DB:              db,
			RDB:             rdb,
			Pres:            pres,
			Hub:             hub,
			RequireAdmin2FA: requireAdmin2FA,
			Dialer:          manualDialer,
			Creds: &webrtccred.Provisioner{
				ESL:          esl,
				DirectoryDir: directoryDir,
				WSSURL:       wssURL,
				SIPDomain:    sipDomain,
				ICEServers:   []webrtccred.ICEServer{},
				TTL:          sessionTTL,
				RDB:          rdb,
				Pres:         pres,
			},
			CORS: splitCSV(envOr("CORS_ORIGINS", "http://localhost:3000")),
		}
		agentSrv.Mount(mux)
		handler = agentSrv.CORSMiddleware(mux)

		inboundRouter := &inbound.Router{
			RDB:  rdb,
			DIDs: &inbound.DIDLoader{DB: db},
			ESL:  esl,
			Pres: pres,
		}
		go inbound.RunListener(ctx, eslAddr, eslPass, inboundRouter, nil)
	} else {
		log.Printf("agent routes disabled (need DATABASE_URL + REDIS_URL)")
	}

	srv := &http.Server{Addr: ":8081", Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		esl.Close()
	}()

	log.Printf("edge listening on :8081 (esl=%s gateway_dir=%s directory_dir=%s wss=%s)",
		eslAddr, gatewayDir, directoryDir, wssURL)
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

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
