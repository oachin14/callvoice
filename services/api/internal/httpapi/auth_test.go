package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/services/api/internal/db/migrate"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
	"github.com/callvoice/callvoice/services/api/internal/testutil"
)

func setupAuthServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	conn := testutil.OpenTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })

	// Truncate instead of migrate.Down to avoid racing parallel packages on a shared lab DB.
	require.NoError(t, migrate.Up(testutil.DatabaseURL()))
	_, err := conn.Exec(`TRUNCATE audit_logs, sessions, dids, carriers, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	srv := &httpapi.Server{
		DB:               conn,
		SessionSecret:    []byte("dev-session-secret-change-me-32b!!"),
		CarrierSecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:       24 * time.Hour,
		CookieSecure:     false,
		RequireAdmin2FA:  true,
		Now:              time.Now,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, conn
}

func insertUser(t *testing.T, db *sql.DB, email, password string, totpEnabled bool) {
	t.Helper()
	insertUserRole(t, db, email, password, "agent", totpEnabled, nil)
}

func insertUserRole(t *testing.T, db *sql.DB, email, password, role string, totpEnabled bool, totpSecretEnc []byte) {
	t.Helper()
	hash, err := authkit.HashPassword(password)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO users (email, password_hash, role, totp_enabled, totp_secret_encrypted)
		 VALUES (lower($1), $2, $3, $4, $5)`,
		email, hash, role, totpEnabled, totpSecretEnc,
	)
	require.NoError(t, err)
}

func TestLoginSuccessSetsSessionCookie(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("ok-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", false)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "ok", payload["status"])

	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	cookies := jar.Cookies(u)
	var found bool
	for _, c := range cookies {
		if c.Name == httpapi.CookieSession && c.Value != "" {
			found = true
		}
	}
	require.True(t, found, "expected cv_session cookie")

	meResp, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode)

	var me map[string]any
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&me))
	require.Equal(t, strings.ToLower(email), me["email"])
}

func TestLoginFailIncrementsAndLocks(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("fail-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", false)

	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "wrong"})
		resp, err := http.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := http.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusLocked, resp.StatusCode)

	var count int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE event = 'login_failed'`,
	).Scan(&count))
	require.GreaterOrEqual(t, count, 5)
}

func TestLoginTOTPRequiredSetsPendingCookie(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("totp-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", true)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "totp_required", payload["status"])

	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	var pending string
	for _, c := range jar.Cookies(u) {
		if c.Name == httpapi.CookiePending {
			pending = c.Value
		}
		require.NotEqual(t, httpapi.CookieSession, c.Name)
	}
	require.NotEmpty(t, pending)
	_, err = authkit.ParsePendingToken(pending, []byte("dev-session-secret-change-me-32b!!"))
	require.NoError(t, err)
}

func TestLogoutClearsSession(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("logout-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", false)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	logoutResp, err := client.Post(ts.URL+"/auth/logout", "application/json", nil)
	require.NoError(t, err)
	_ = logoutResp.Body.Close()
	require.Equal(t, http.StatusOK, logoutResp.StatusCode)

	meResp, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, meResp.StatusCode)
}
