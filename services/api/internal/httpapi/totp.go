package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/internal/models"
)

type totpCodeRequest struct {
	Code string `json:"code"`
}

// handleTOTPSetup generates a new TOTP secret, stores it encrypted (disabled), and returns
// the plaintext secret plus otpauth URL for authenticator enrollment.
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if user.TOTPEnabled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "totp_already_enabled"})
		return
	}

	secret, err := authkit.GenerateTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	encrypted, err := cryptokit.Encrypt(s.CarrierSecretKey, []byte(secret))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	_, err = s.DB.ExecContext(r.Context(), `
		UPDATE users SET totp_secret_encrypted = $2, totp_enabled = FALSE WHERE id = $1
	`, user.ID, encrypted)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_url": authkit.OTPAuthURL(secret, user.Email),
	})
}

// handleTOTPEnable verifies a code against the stored secret and enables TOTP.
func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if user.TOTPEnabled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "totp_already_enabled"})
		return
	}

	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code_required"})
		return
	}

	// Reload user so we see the secret written by /auth/2fa/setup.
	fresh, err := s.lookupUserByID(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	secret, err := s.decryptTOTPSecret(fresh)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "totp_not_setup"})
		return
	}

	if !authkit.ValidateTOTP(secret, req.Code) {
		_ = s.insertAudit(r.Context(), &user.ID, "totp_failed", clientIP(r), nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_totp"})
		return
	}

	_, err = s.DB.ExecContext(r.Context(), `
		UPDATE users SET totp_enabled = TRUE WHERE id = $1
	`, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTOTPVerify completes login using cv_pending + TOTP code, issuing a full session.
func (s *Server) handleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookiePending)
	if err != nil || c.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pending_required"})
		return
	}

	userID, err := authkit.ParsePendingToken(c.Value, s.SessionSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "pending_invalid"})
		return
	}

	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code_required"})
		return
	}

	user, err := s.lookupUserByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	if !user.TOTPEnabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "totp_not_enabled"})
		return
	}

	secret, err := s.decryptTOTPSecret(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	if !authkit.ValidateTOTP(secret, req.Code) {
		_ = s.insertAudit(r.Context(), &user.ID, "totp_failed", clientIP(r), nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_totp"})
		return
	}

	plain, hash, err := authkit.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	expiresAt := s.Now().UTC().Add(s.SessionTTL)
	if err := s.insertSession(r.Context(), user.ID, hash, expiresAt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	_ = s.insertAudit(r.Context(), &user.ID, "login_ok", clientIP(r), nil)

	http.SetCookie(w, s.cookie(CookieSession, plain, s.SessionTTL))
	clearCookie(w, CookiePending, s.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"user":   toUserResponse(user),
	})
}

func (s *Server) decryptTOTPSecret(user *models.User) (string, error) {
	if len(user.TOTPSecretEncrypted) == 0 {
		return "", errors.New("totp secret missing")
	}
	pt, err := cryptokit.Decrypt(s.CarrierSecretKey, user.TOTPSecretEncrypted)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (s *Server) lookupUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, email, password_hash, role, totp_secret_encrypted, totp_enabled,
		       failed_login_count, locked_until, created_at
		FROM users WHERE id = $1
	`, id).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.TOTPSecretEncrypted, &u.TOTPEnabled,
		&u.FailedLoginCount, &u.LockedUntil, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
