package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/callvoice/callvoice/services/api/internal/db"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := db.OpenAndMigrate(ctx)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer conn.Close()

	if os.Getenv("SESSION_SECRET") == "" {
		_ = os.Setenv("SESSION_SECRET", "dev-session-secret-change-me-32b!!")
	}
	if os.Getenv("COOKIE_SECURE") == "" {
		_ = os.Setenv("COOKIE_SECURE", "false")
	}

	srv, err := httpapi.NewServer(conn)
	if err != nil {
		log.Fatalf("auth server: %v", err)
	}

	addr := ":8080"
	if v := os.Getenv("API_ADDR"); v != "" {
		addr = v
	}
	log.Printf("api listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.Routes()))
}
