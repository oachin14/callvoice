package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/httpapi"
	"github.com/callvoice/callvoice/services/edge/internal/webrtccred"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://callvoice:callvoice@localhost:5432/callvoice?sslmode=disable"
	}
	conn, err := sql.Open("postgres", url)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		t.Skipf("postgres unavailable: %v", err)
	}
	var hasUsers bool
	if err := conn.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'users'
		)`).Scan(&hasUsers); err != nil || !hasUsers {
		_ = conn.Close()
		t.Skip("postgres schema not migrated (users missing)")
	}
	return conn
}

func setupAgentServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	conn := openTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	pres := agent.NewPresence(rdb, agent.DefaultTTL)
	mux := http.NewServeMux()
	srv := &httpapi.AgentServer{
		DB:              conn,
		Pres:            pres,
		RequireAdmin2FA: true,
		Creds: &webrtccred.Provisioner{
			DirectoryDir: t.TempDir(),
			WSSURL:       "wss://localhost:7443",
			SIPDomain:    "localhost",
			TTL:          agent.DefaultTTL,
			RDB:          rdb,
			Pres:         pres,
		},
	}
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, conn
}

func insertAdminSession(t *testing.T, db *sql.DB, totpEnabled bool) (email, session string) {
	t.Helper()
	email = fmt.Sprintf("admin-%s@test.local", uuid.NewString())
	hash, err := authkit.HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	var userID uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO users (email, password_hash, role, totp_enabled)
		 VALUES (lower($1), $2, 'admin', $3) RETURNING id`,
		email, hash, totpEnabled,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	plain, tokenHash, err := authkit.NewSessionToken()
	if err != nil {
		t.Fatalf("session token: %v", err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	if _, err := db.Exec(
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expires,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return email, plain
}

func TestAgentUnauthorizedWithoutCookie(t *testing.T) {
	mux := http.NewServeMux()
	(&httpapi.AgentServer{RequireAdmin2FA: true}).Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/agent/session/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Fatalf("error = %q, want unauthorized", body["error"])
	}
}

func TestAgentAdminWithoutTOTPForbidden(t *testing.T) {
	ts, db := setupAgentServer(t)
	_, session := insertAdminSession(t, db, false)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/agent/session/start", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "cv_session", Value: session})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if body["error"] != "totp_setup_required" {
		t.Fatalf("error = %q, want totp_setup_required", body["error"])
	}
}
