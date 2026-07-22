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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/services/api/internal/db/migrate"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
	"github.com/callvoice/callvoice/services/api/internal/testutil"
)

func setupUsersServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	conn := testutil.OpenTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })

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
		CarrierPublisher: httpapi.NoopCarrierPublisher{},
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, conn
}

func loginAdminUsers(t *testing.T, ts *httptest.Server, db *sql.DB) *http.Client {
	t.Helper()
	email := fmt.Sprintf("admin-%s@test.local", t.Name())
	secret := "JBSWY3DPEHPK3PXP"
	enc, err := cryptokit.Encrypt([]byte("0123456789abcdef0123456789abcdef"), []byte(secret))
	require.NoError(t, err)
	insertUserRole(t, db, email, "correct horse", "admin", true, enc)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	code, err := authkit.GenerateTOTPCode(secret)
	require.NoError(t, err)
	verifyBody, _ := json.Marshal(map[string]string{"code": code})
	verifyResp, err := client.Post(ts.URL+"/auth/2fa/verify", "application/json", bytes.NewReader(verifyBody))
	require.NoError(t, err)
	defer verifyResp.Body.Close()
	require.Equal(t, http.StatusOK, verifyResp.StatusCode)

	return client
}

func loginAgent(t *testing.T, ts *httptest.Server, db *sql.DB, email, password string) *http.Client {
	t.Helper()
	insertUser(t, db, email, password, false)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return client
}

func TestAdminCreatesAgent(t *testing.T) {
	ts, db := setupUsersServer(t)
	client := loginAdminUsers(t, ts, db)

	createBody, _ := json.Marshal(map[string]any{
		"email":        "agent.new@test.local",
		"password":     "temp-pass-123",
		"role":         "agent",
		"display_name": "New Agent",
	})
	createResp, err := client.Post(ts.URL+"/admin/users", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)

	var created map[string]any
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	require.Equal(t, "agent.new@test.local", created["email"])
	require.Equal(t, "agent", created["role"])
	require.Equal(t, "New Agent", created["display_name"])
	require.Equal(t, false, created["disabled"])
	_, hasHash := created["password_hash"]
	require.False(t, hasHash)
	_, hasPassword := created["password"]
	require.False(t, hasPassword)

	listResp, err := client.Get(ts.URL + "/admin/users")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 2) // admin + new agent
}

func TestAgentCannotCreateUser(t *testing.T) {
	ts, db := setupUsersServer(t)
	_ = loginAdminUsers(t, ts, db) // seed admin exists

	agentEmail := fmt.Sprintf("agent-%s@test.local", t.Name())
	client := loginAgent(t, ts, db, agentEmail, "agent-pass")

	createBody, _ := json.Marshal(map[string]any{
		"email":    "other@test.local",
		"password": "temp-pass",
		"role":     "agent",
	})
	createResp, err := client.Post(ts.URL+"/admin/users", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusForbidden, createResp.StatusCode)
}

func TestDisabledUserLoginForbidden(t *testing.T) {
	ts, db := setupUsersServer(t)
	email := fmt.Sprintf("disabled-%s@test.local", t.Name())
	hash, err := authkit.HashPassword("correct horse")
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO users (email, password_hash, role, disabled_at) VALUES (lower($1), $2, 'agent', now())`,
		email, hash,
	)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := http.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, "account_disabled", payload["error"])
}

func TestRejectSecondAdmin(t *testing.T) {
	ts, db := setupUsersServer(t)
	client := loginAdminUsers(t, ts, db)

	createBody, _ := json.Marshal(map[string]any{
		"email":    "admin2@test.local",
		"password": "temp-pass",
		"role":     "admin",
	})
	createResp, err := client.Post(ts.URL+"/admin/users", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, createResp.StatusCode)
}

func TestPatchUserDisableAndResetPassword(t *testing.T) {
	ts, db := setupUsersServer(t)
	client := loginAdminUsers(t, ts, db)

	createBody, _ := json.Marshal(map[string]any{
		"email":    "patch.me@test.local",
		"password": "old-pass",
		"role":     "agent",
	})
	createResp, err := client.Post(ts.URL+"/admin/users", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created map[string]any
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	id := created["id"].(string)

	patchBody, _ := json.Marshal(map[string]any{
		"display_name": "Renamed",
		"disabled":     true,
	})
	patchReq, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/users/"+id, bytes.NewReader(patchBody))
	require.NoError(t, err)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := client.Do(patchReq)
	require.NoError(t, err)
	defer patchResp.Body.Close()
	require.Equal(t, http.StatusOK, patchResp.StatusCode)

	var patched map[string]any
	require.NoError(t, json.NewDecoder(patchResp.Body).Decode(&patched))
	require.Equal(t, "Renamed", patched["display_name"])
	require.Equal(t, true, patched["disabled"])

	resetBody, _ := json.Marshal(map[string]string{"password": "new-pass-xyz"})
	resetResp, err := client.Post(ts.URL+"/admin/users/"+id+"/reset-password", "application/json", bytes.NewReader(resetBody))
	require.NoError(t, err)
	defer resetResp.Body.Close()
	require.Equal(t, http.StatusOK, resetResp.StatusCode)

	var storedHash string
	require.NoError(t, db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, id).Scan(&storedHash))
	require.True(t, authkit.VerifyPassword(storedHash, "new-pass-xyz"))
}

func TestCreateUserValidation(t *testing.T) {
	ts, db := setupUsersServer(t)
	client := loginAdminUsers(t, ts, db)

	cases := []map[string]any{
		{"email": "", "password": "x", "role": "agent"},
		{"email": "bad", "password": "x", "role": "agent"},
		{"email": "ok@test.local", "password": "", "role": "agent"},
		{"email": "ok@test.local", "password": "x", "role": "owner"},
	}
	for _, bodyMap := range cases {
		body, _ := json.Marshal(bodyMap)
		resp, err := client.Post(ts.URL+"/admin/users", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%v", bodyMap)
	}
}
