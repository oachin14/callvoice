package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/internal/models"
)

const (
	CookieSession = "cv_session"
	CookiePending = "cv_pending"

	defaultSessionTTL = 24 * time.Hour
	pendingTTL        = 5 * time.Minute
	maxFailedLogins   = 5
	lockDuration      = 15 * time.Minute
)

type contextKey string

const userContextKey contextKey = "user"

// Server hosts auth HTTP handlers.
type Server struct {
	DB               *sql.DB
	SessionSecret    []byte
	CarrierSecretKey []byte
	SessionTTL       time.Duration
	CookieSecure     bool
	RequireAdmin2FA  bool
	Now              func() time.Time
	CarrierPublisher CarrierChangePublisher
}

// NewServer builds an API server from environment defaults.
func NewServer(db *sql.DB) (*Server, error) {
	secret := os.Getenv("SESSION_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("SESSION_SECRET must be at least 32 bytes")
	}

	carrierKeyRaw := os.Getenv("CARRIER_SECRET_KEY")
	if carrierKeyRaw == "" {
		return nil, errors.New("CARRIER_SECRET_KEY is required (32-byte raw or 64 hex chars; also encrypts TOTP secrets)")
	}
	carrierKey, err := cryptokit.ParseKey(carrierKeyRaw)
	if err != nil {
		return nil, errors.New("CARRIER_SECRET_KEY must be 32 raw bytes or 64 hex characters")
	}

	ttl := defaultSessionTTL
	if raw := os.Getenv("SESSION_TTL_HOURS"); raw != "" {
		hours, err := strconv.Atoi(raw)
		if err != nil || hours <= 0 {
			return nil, errors.New("SESSION_TTL_HOURS must be a positive integer")
		}
		ttl = time.Duration(hours) * time.Hour
	}

	secure := true
	if raw := os.Getenv("COOKIE_SECURE"); raw != "" {
		secure = raw == "1" || strings.EqualFold(raw, "true")
	}

	requireAdmin2FA := true
	if raw := os.Getenv("REQUIRE_ADMIN_2FA"); raw != "" {
		requireAdmin2FA = raw == "1" || strings.EqualFold(raw, "true")
	}

	publisher, err := NewRedisCarrierPublisherFromURL(os.Getenv("REDIS_URL"))
	if err != nil {
		// Redis optional at boot: fall back to noop so API still serves config CRUD.
		publisher = NoopCarrierPublisher{}
	}

	return &Server{
		DB:               db,
		SessionSecret:    []byte(secret),
		CarrierSecretKey: carrierKey,
		SessionTTL:       ttl,
		CookieSecure:     secure,
		RequireAdmin2FA:  requireAdmin2FA,
		Now:              time.Now,
		CarrierPublisher: publisher,
	}, nil
}

// Routes registers API routes on a chi router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsAllowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
		r.Post("/2fa/verify", s.handleTOTPVerify)
		r.With(s.RequireSession).Get("/me", s.handleMe)
		r.With(s.RequireSession).Post("/2fa/setup", s.handleTOTPSetup)
		r.With(s.RequireSession).Post("/2fa/enable", s.handleTOTPEnable)
	})

	r.Route("/admin", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(s.RequireSession, s.RequireAdmin)
			r.Get("/carriers", s.handleListCarriers)
			r.Post("/carriers", s.handleCreateCarrier)
			r.Patch("/carriers/{id}", s.handlePatchCarrier)
			r.Delete("/carriers/{id}", s.handleDeleteCarrier)
			r.Post("/dids", s.handleCreateDID)
			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Patch("/users/{id}", s.handlePatchUser)
			r.Post("/users/{id}/reset-password", s.handleResetUserPassword)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.RequireSession, s.RequireSupervisor)
			r.Get("/campaigns", s.handleListCampaigns)
			r.Post("/campaigns", s.handleCreateCampaign)
			r.Patch("/campaigns/{id}", s.handlePatchCampaign)
			r.Put("/campaigns/{id}/agents", s.handleAssignCampaignAgents)
			r.Post("/campaigns/{id}/lists/import", s.handleImportLeadList)
			r.Get("/campaigns/{id}/dispositions", s.handleListDispositions)
			r.Post("/campaigns/{id}/dispositions", s.handleCreateDisposition)
			r.Get("/reports/summary", s.handleReportsSummary)
			r.Get("/reports/export.csv", s.handleReportsExportCSV)
		})
	})

	r.Route("/agent", func(r chi.Router) {
		r.Use(s.RequireSession, s.RequireAgent)
		r.Get("/campaigns", s.handleAgentListCampaigns)
		r.Post("/campaigns/{id}/join", s.handleAgentJoinCampaign)
		r.Get("/leads/next", s.handleAgentNextLead)
		r.Post("/dispositions", s.handleAgentDisposition)
	})

	return r
}

func corsAllowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ORIGINS"))
	if raw == "" {
		return []string{"http://localhost:3000"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"http://localhost:3000"}
	}
	return out
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID          uuid.UUID       `json:"id"`
	Email       string          `json:"email"`
	Role        models.UserRole `json:"role"`
	DisplayName *string         `json:"display_name,omitempty"`
	TOTPEnabled bool            `json:"totp_enabled"`
	Disabled    bool            `json:"disabled"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email_and_password_required"})
		return
	}

	user, err := s.lookupUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	now := s.Now().UTC()
	if user.DisabledAt != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account_disabled"})
		return
	}

	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		writeJSON(w, http.StatusLocked, map[string]string{"error": "account_locked"})
		return
	}

	if !authkit.VerifyPassword(user.PasswordHash, req.Password) {
		_ = s.recordFailedLogin(r.Context(), user, clientIP(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}

	if err := s.resetFailedLogin(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	if user.TOTPEnabled {
		token, err := authkit.NewPendingToken(user.ID, s.SessionSecret, pendingTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		http.SetCookie(w, s.cookie(CookiePending, token, pendingTTL))
		clearCookie(w, CookieSession, s.CookieSecure)
		writeJSON(w, http.StatusOK, map[string]string{"status": "totp_required"})
		return
	}

	// Admins must enroll TOTP before a full session when policy is on.
	if s.RequireAdmin2FA && user.Role == models.UserRoleAdmin {
		plain, hash, err := authkit.NewSessionToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		expiresAt := now.Add(s.SessionTTL)
		if err := s.insertSession(r.Context(), user.ID, hash, expiresAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		http.SetCookie(w, s.cookie(CookieSession, plain, s.SessionTTL))
		clearCookie(w, CookiePending, s.CookieSecure)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "totp_setup_required",
			"user":   toUserResponse(user),
		})
		return
	}

	plain, hash, err := authkit.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	expiresAt := now.Add(s.SessionTTL)
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

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieSession); err == nil && c.Value != "" {
		_, _ = s.DB.ExecContext(r.Context(),
			`DELETE FROM sessions WHERE token_hash = $1`,
			authkit.HashToken(c.Value),
		)
	}
	clearCookie(w, CookieSession, s.CookieSecure)
	clearCookie(w, CookiePending, s.CookieSecure)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// RequireSession loads the authenticated user from cv_session into context.
// When REQUIRE_ADMIN_2FA is on, admins without TOTP may only hit enrollment allowlist routes.
func (s *Server) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieSession)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		user, err := s.lookupUserBySessionToken(r.Context(), c.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		if user.DisabledAt != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "account_disabled"})
			return
		}

		if s.adminTOTPSetupRequired(user) && !admin2FAEnrollmentAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "totp_setup_required"})
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) adminTOTPSetupRequired(user *models.User) bool {
	return s.RequireAdmin2FA && user.Role == models.UserRoleAdmin && !user.TOTPEnabled
}

// admin2FAEnrollmentAllowed is the only authenticated surface for admins mid-enrollment.
func admin2FAEnrollmentAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/auth/2fa/setup", "/auth/2fa/enable", "/auth/logout":
		return true
	default:
		return false
	}
}

// UserFromContext returns the authenticated user, if any.
func UserFromContext(ctx context.Context) *models.User {
	user, _ := ctx.Value(userContextKey).(*models.User)
	return user
}

func (s *Server) lookupUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	var displayName sql.NullString
	var lockedUntil, disabledAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, email, password_hash, role, display_name, totp_secret_encrypted, totp_enabled,
		       failed_login_count, locked_until, disabled_at, created_at
		FROM users WHERE lower(email) = $1
	`, email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &displayName, &u.TOTPSecretEncrypted, &u.TOTPEnabled,
		&u.FailedLoginCount, &lockedUntil, &disabledAt, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if displayName.Valid {
		u.DisplayName = &displayName.String
	}
	if lockedUntil.Valid {
		u.LockedUntil = &lockedUntil.Time
	}
	if disabledAt.Valid {
		u.DisabledAt = &disabledAt.Time
	}
	return &u, nil
}

func (s *Server) lookupUserBySessionToken(ctx context.Context, plain string) (*models.User, error) {
	var u models.User
	var displayName sql.NullString
	var lockedUntil, disabledAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.password_hash, u.role, u.display_name, u.totp_secret_encrypted, u.totp_enabled,
		       u.failed_login_count, u.locked_until, u.disabled_at, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()
	`, authkit.HashToken(plain)).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &displayName, &u.TOTPSecretEncrypted, &u.TOTPEnabled,
		&u.FailedLoginCount, &lockedUntil, &disabledAt, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if displayName.Valid {
		u.DisplayName = &displayName.String
	}
	if lockedUntil.Valid {
		u.LockedUntil = &lockedUntil.Time
	}
	if disabledAt.Valid {
		u.DisabledAt = &disabledAt.Time
	}
	return &u, nil
}

func (s *Server) insertSession(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt)
	return err
}

func (s *Server) recordFailedLogin(ctx context.Context, user *models.User, ip string) error {
	failed := user.FailedLoginCount + 1
	var lockedUntil *time.Time
	if failed >= maxFailedLogins {
		t := s.Now().UTC().Add(lockDuration)
		lockedUntil = &t
		failed = 0
	}

	_, err := s.DB.ExecContext(ctx, `
		UPDATE users SET failed_login_count = $2, locked_until = $3 WHERE id = $1
	`, user.ID, failed, lockedUntil)
	if err != nil {
		return err
	}

	meta, _ := json.Marshal(map[string]any{"email": user.Email})
	return s.insertAudit(ctx, &user.ID, "login_failed", ip, meta)
}

func (s *Server) resetFailedLogin(ctx context.Context, userID uuid.UUID) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE users SET failed_login_count = 0, locked_until = NULL WHERE id = $1
	`, userID)
	return err
}

func (s *Server) insertAudit(ctx context.Context, userID *uuid.UUID, event, ip string, meta []byte) error {
	if meta == nil {
		meta = []byte("{}")
	}
	var ipPtr *string
	if ip != "" {
		ipPtr = &ip
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO audit_logs (user_id, event, ip, meta) VALUES ($1, $2, $3, $4::jsonb)
	`, userID, event, ipPtr, string(meta))
	return err
}

func (s *Server) cookie(name, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Expires:  s.Now().UTC().Add(ttl),
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func toUserResponse(u *models.User) userResponse {
	return userResponse{
		ID:          u.ID,
		Email:       u.Email,
		Role:        u.Role,
		DisplayName: u.DisplayName,
		TOTPEnabled: u.TOTPEnabled,
		Disabled:    u.DisabledAt != nil,
		CreatedAt:   u.CreatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
