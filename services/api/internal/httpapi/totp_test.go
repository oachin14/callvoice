package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
)

func TestLoginVerifyTOTPFullFlowAdmin(t *testing.T) {
	ts, db := setupAuthServer(t)
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

	var loginPayload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&loginPayload))
	require.Equal(t, "totp_required", loginPayload["status"])

	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	var hasSession bool
	for _, c := range jar.Cookies(u) {
		if c.Name == httpapi.CookieSession {
			hasSession = true
		}
	}
	require.False(t, hasSession, "must not have full session before TOTP verify")

	code, err := authkit.GenerateTOTPCode(secret)
	require.NoError(t, err)
	verifyBody, _ := json.Marshal(map[string]string{"code": code})
	verifyResp, err := client.Post(ts.URL+"/auth/2fa/verify", "application/json", bytes.NewReader(verifyBody))
	require.NoError(t, err)
	defer verifyResp.Body.Close()
	require.Equal(t, http.StatusOK, verifyResp.StatusCode)

	var verifyPayload map[string]any
	require.NoError(t, json.NewDecoder(verifyResp.Body).Decode(&verifyPayload))
	require.Equal(t, "ok", verifyPayload["status"])
	user, ok := verifyPayload["user"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "admin", user["role"])

	meResp, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode)

	var me map[string]any
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&me))
	require.Equal(t, "admin", me["role"])
	require.Equal(t, true, me["totp_enabled"])

	var loginOK int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE event = 'login_ok'`).Scan(&loginOK))
	require.Equal(t, 1, loginOK)
}

func TestTOTPEnrollEnableThenLogin(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("enroll-%s@test.local", t.Name())
	insertUser(t, db, email, "correct horse", false)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	setupResp, err := client.Post(ts.URL+"/auth/2fa/setup", "application/json", nil)
	require.NoError(t, err)
	defer setupResp.Body.Close()
	require.Equal(t, http.StatusOK, setupResp.StatusCode)

	var setupPayload map[string]string
	require.NoError(t, json.NewDecoder(setupResp.Body).Decode(&setupPayload))
	require.NotEmpty(t, setupPayload["secret"])
	require.Contains(t, setupPayload["otpauth_url"], "issuer=CallVoice")

	code, err := authkit.GenerateTOTPCode(setupPayload["secret"])
	require.NoError(t, err)
	enableBody, _ := json.Marshal(map[string]string{"code": code})
	enableResp, err := client.Post(ts.URL+"/auth/2fa/enable", "application/json", bytes.NewReader(enableBody))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, enableResp.Body)
	_ = enableResp.Body.Close()
	require.Equal(t, http.StatusOK, enableResp.StatusCode)

	logoutResp, err := client.Post(ts.URL+"/auth/logout", "application/json", nil)
	require.NoError(t, err)
	_ = logoutResp.Body.Close()

	loginResp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer loginResp.Body.Close()
	var loginPayload map[string]any
	require.NoError(t, json.NewDecoder(loginResp.Body).Decode(&loginPayload))
	require.Equal(t, "totp_required", loginPayload["status"])

	code2, err := authkit.GenerateTOTPCode(setupPayload["secret"])
	require.NoError(t, err)
	verifyBody, _ := json.Marshal(map[string]string{"code": code2})
	verifyResp, err := client.Post(ts.URL+"/auth/2fa/verify", "application/json", bytes.NewReader(verifyBody))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, verifyResp.Body)
	_ = verifyResp.Body.Close()
	require.Equal(t, http.StatusOK, verifyResp.StatusCode)

	meResp, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusOK, meResp.StatusCode)
}

func TestAdminLoginWithoutTOTPRequiresSetup(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("admin-setup-%s@test.local", t.Name())
	insertUserRole(t, db, email, "correct horse", "admin", false, nil)

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
	require.Equal(t, "totp_setup_required", payload["status"])

	var loginOK int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE event = 'login_ok'`).Scan(&loginOK))
	require.Equal(t, 0, loginOK)

	meResp, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meResp.Body.Close()
	require.Equal(t, http.StatusForbidden, meResp.StatusCode)
	var meErr map[string]string
	require.NoError(t, json.NewDecoder(meResp.Body).Decode(&meErr))
	require.Equal(t, "totp_setup_required", meErr["error"])

	// Session is issued only so they can call setup/enable.
	setupResp, err := client.Post(ts.URL+"/auth/2fa/setup", "application/json", nil)
	require.NoError(t, err)
	defer setupResp.Body.Close()
	require.Equal(t, http.StatusOK, setupResp.StatusCode)

	var setupPayload map[string]string
	require.NoError(t, json.NewDecoder(setupResp.Body).Decode(&setupPayload))
	require.NotEmpty(t, setupPayload["secret"])

	code, err := authkit.GenerateTOTPCode(setupPayload["secret"])
	require.NoError(t, err)
	enableBody, _ := json.Marshal(map[string]string{"code": code})
	enableResp, err := client.Post(ts.URL+"/auth/2fa/enable", "application/json", bytes.NewReader(enableBody))
	require.NoError(t, err)
	defer enableResp.Body.Close()
	require.Equal(t, http.StatusOK, enableResp.StatusCode)

	var enablePayload map[string]any
	require.NoError(t, json.NewDecoder(enableResp.Body).Decode(&enablePayload))
	require.Equal(t, "ok", enablePayload["status"])
	require.Equal(t, true, enablePayload["relogin_required"])

	// Enrollment session cleared — must re-login through TOTP verify.
	meAfter, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meAfter.Body.Close()
	require.Equal(t, http.StatusUnauthorized, meAfter.StatusCode)

	loginResp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer loginResp.Body.Close()
	var loginPayload map[string]any
	require.NoError(t, json.NewDecoder(loginResp.Body).Decode(&loginPayload))
	require.Equal(t, "totp_required", loginPayload["status"])

	code2, err := authkit.GenerateTOTPCode(setupPayload["secret"])
	require.NoError(t, err)
	verifyBody, _ := json.Marshal(map[string]string{"code": code2})
	verifyResp, err := client.Post(ts.URL+"/auth/2fa/verify", "application/json", bytes.NewReader(verifyBody))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, verifyResp.Body)
	_ = verifyResp.Body.Close()
	require.Equal(t, http.StatusOK, verifyResp.StatusCode)

	meOK, err := client.Get(ts.URL + "/auth/me")
	require.NoError(t, err)
	defer meOK.Body.Close()
	require.Equal(t, http.StatusOK, meOK.StatusCode)
}

func TestTOTPVerifyFailsAudits(t *testing.T) {
	ts, db := setupAuthServer(t)
	email := fmt.Sprintf("failtotp-%s@test.local", t.Name())
	secret := "JBSWY3DPEHPK3PXP"
	enc, err := cryptokit.Encrypt([]byte("0123456789abcdef0123456789abcdef"), []byte(secret))
	require.NoError(t, err)
	insertUserRole(t, db, email, "correct horse", "agent", true, enc)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "correct horse"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()

	verifyBody, _ := json.Marshal(map[string]string{"code": "000000"})
	verifyResp, err := client.Post(ts.URL+"/auth/2fa/verify", "application/json", bytes.NewReader(verifyBody))
	require.NoError(t, err)
	defer verifyResp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, verifyResp.StatusCode)

	var failed int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE event = 'totp_failed'`).Scan(&failed))
	require.Equal(t, 1, failed)
}
