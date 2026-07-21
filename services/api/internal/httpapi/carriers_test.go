package httpapi_test

import (
	"bytes"
	"context"
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

type recordingPublisher struct {
	payloads []string
}

func (p *recordingPublisher) PublishCarriersChanged(_ context.Context, payload string) error {
	p.payloads = append(p.payloads, payload)
	return nil
}

func setupCarriersServer(t *testing.T) (*httptest.Server, *sql.DB, *recordingPublisher) {
	t.Helper()

	conn := testutil.OpenTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })

	// Truncate instead of migrate.Down to avoid racing parallel packages on a shared lab DB.
	require.NoError(t, migrate.Up(testutil.DatabaseURL()))
	_, err := conn.Exec(`TRUNCATE audit_logs, sessions, dids, carriers, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	pub := &recordingPublisher{}
	srv := &httpapi.Server{
		DB:               conn,
		SessionSecret:    []byte("dev-session-secret-change-me-32b!!"),
		CarrierSecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:       24 * time.Hour,
		CookieSecure:     false,
		RequireAdmin2FA:  true,
		Now:              time.Now,
		CarrierPublisher: pub,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, conn, pub
}

func loginAdmin(t *testing.T, ts *httptest.Server, db *sql.DB) *http.Client {
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

func TestCreateCarrierAsAdminListHidesPassword(t *testing.T) {
	ts, db, pub := setupCarriersServer(t)
	client := loginAdmin(t, ts, db)

	createBody, _ := json.Marshal(map[string]any{
		"name":         "Primary SIP",
		"host":         "sip.example.com",
		"port":         5061,
		"transport":    "tls",
		"username":     "trunkuser",
		"password":     "sip-secret-plaintext",
		"realm":        "example.com",
		"codecs":       []string{"PCMU", "PCMA"},
		"caller_ids":   []string{"+33123456789"},
		"max_cps":      10,
		"max_channels": 50,
		"enabled":      true,
		"priority":     10,
	})
	createResp, err := client.Post(ts.URL+"/admin/carriers", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)

	var created map[string]any
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	require.Equal(t, "Primary SIP", created["name"])
	require.Equal(t, true, created["password_set"])
	_, hasPassword := created["password"]
	require.False(t, hasPassword, "must never return raw password field")
	_, hasEnc := created["password_encrypted"]
	require.False(t, hasEnc, "must never return encrypted blob")

	id, ok := created["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, id)

	var encBlob []byte
	require.NoError(t, db.QueryRow(`SELECT password_encrypted FROM carriers WHERE id = $1`, id).Scan(&encBlob))
	require.NotEmpty(t, encBlob)
	pt, err := cryptokit.Decrypt([]byte("0123456789abcdef0123456789abcdef"), encBlob)
	require.NoError(t, err)
	require.Equal(t, "sip-secret-plaintext", string(pt))

	listResp, err := client.Get(ts.URL + "/admin/carriers")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Equal(t, true, list[0]["password_set"])
	_, hasPassword = list[0]["password"]
	require.False(t, hasPassword)
	raw, _ := json.Marshal(list[0])
	require.NotContains(t, string(raw), "sip-secret-plaintext")

	require.NotEmpty(t, pub.payloads)
	require.Equal(t, id, pub.payloads[0])
}

func TestCarriersForbiddenForAgent(t *testing.T) {
	ts, db, _ := setupCarriersServer(t)
	email := fmt.Sprintf("agent-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", false)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	listResp, err := client.Get(ts.URL + "/admin/carriers")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusForbidden, listResp.StatusCode)
}

func TestCreateCarrierValidation(t *testing.T) {
	ts, db, _ := setupCarriersServer(t)
	client := loginAdmin(t, ts, db)

	cases := []map[string]any{
		{"name": "x", "host": "h", "transport": "udp", "max_cps": 0, "max_channels": 1},
		{"name": "x", "host": "h", "transport": "udp", "max_cps": 1, "max_channels": 0},
		{"name": "x", "host": "h", "transport": "sctp", "max_cps": 1, "max_channels": 1},
		{"host": "h", "transport": "udp", "max_cps": 1, "max_channels": 1},
	}
	for _, bodyMap := range cases {
		body, _ := json.Marshal(bodyMap)
		resp, err := client.Post(ts.URL+"/admin/carriers", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%v", bodyMap)
	}
}

func TestPatchAndDeleteCarrier(t *testing.T) {
	ts, db, pub := setupCarriersServer(t)
	client := loginAdmin(t, ts, db)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Trunk", "host": "sip.example.com", "transport": "udp",
		"password": "old-secret", "max_cps": 5, "max_channels": 10,
	})
	createResp, err := client.Post(ts.URL+"/admin/carriers", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created map[string]any
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	id := created["id"].(string)

	patchBody, _ := json.Marshal(map[string]any{
		"name":     "Trunk Updated",
		"password": "new-secret",
		"max_cps":  15,
	})
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/carriers/"+id, bytes.NewReader(patchBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	patchResp, err := client.Do(req)
	require.NoError(t, err)
	defer patchResp.Body.Close()
	require.Equal(t, http.StatusOK, patchResp.StatusCode)

	var patched map[string]any
	require.NoError(t, json.NewDecoder(patchResp.Body).Decode(&patched))
	require.Equal(t, "Trunk Updated", patched["name"])
	require.Equal(t, true, patched["password_set"])
	require.Equal(t, float64(15), patched["max_cps"])

	var encBlob []byte
	require.NoError(t, db.QueryRow(`SELECT password_encrypted FROM carriers WHERE id = $1`, id).Scan(&encBlob))
	pt, err := cryptokit.Decrypt([]byte("0123456789abcdef0123456789abcdef"), encBlob)
	require.NoError(t, err)
	require.Equal(t, "new-secret", string(pt))

	delReq, err := http.NewRequest(http.MethodDelete, ts.URL+"/admin/carriers/"+id, nil)
	require.NoError(t, err)
	delResp, err := client.Do(delReq)
	require.NoError(t, err)
	defer delResp.Body.Close()
	require.Equal(t, http.StatusNoContent, delResp.StatusCode)

	listResp, err := client.Get(ts.URL + "/admin/carriers")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var list []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 0)

	require.GreaterOrEqual(t, len(pub.payloads), 3)
	require.Equal(t, id, pub.payloads[0])
	require.Equal(t, id, pub.payloads[1])
	require.Equal(t, id, pub.payloads[2])
}
