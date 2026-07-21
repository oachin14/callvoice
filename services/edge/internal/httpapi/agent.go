package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/webrtccred"
)

const cookieSession = "cv_session"

type ctxKey int

const userCtxKey ctxKey = 1

// AgentServer serves agent presence and WebRTC credential endpoints.
type AgentServer struct {
	DB     *sql.DB
	Pres   *agent.Presence
	Creds  *webrtccred.Provisioner
	CORS   []string
}

// Mount registers agent routes on mux.
func (s *AgentServer) Mount(mux *http.ServeMux) {
	mux.Handle("POST /agent/session/start", s.withAuth(s.handleStart))
	mux.Handle("POST /agent/session/stop", s.withAuth(s.handleStop))
	mux.Handle("POST /agent/state", s.withAuth(s.handleState))
	mux.Handle("GET /agent/webrtc-config", s.withAuth(s.handleWebRTCConfig))
}

// CORSMiddleware allows credentialed browser calls from configured origins.
func (s *AgentServer) CORSMiddleware(next http.Handler) http.Handler {
	origins := map[string]struct{}{}
	for _, o := range s.CORS {
		origins[o] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := origins[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *AgentServer) withAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieSession)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		user, err := s.lookupUserBySessionToken(r.Context(), c.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, user)
		next(w, r.WithContext(ctx))
	})
}

func userFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(userCtxKey).(*models.User)
	return u
}

func (s *AgentServer) handleStart(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := s.Pres.Start(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	cfg, err := s.Creds.Issue(r.Context(), user.ID)
	if err != nil {
		_ = s.Pres.Stop(r.Context(), user.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"state":  agent.StateAvailable,
		"webrtc": cfg,
	})
}

func (s *AgentServer) handleStop(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = s.Creds.Revoke(r.Context(), user.ID)
	if err := s.Pres.Stop(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *AgentServer) handleState(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	var body struct {
		State agent.State `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if err := s.Pres.SetState(r.Context(), user.ID, body.State); err != nil {
		if errors.Is(err, agent.ErrInvalidState) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_state"})
			return
		}
		if errors.Is(err, agent.ErrNotFound) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session_not_started"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "state": body.State})
}

func (s *AgentServer) handleWebRTCConfig(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	cfg, err := s.Creds.Get(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, webrtccred.ErrNoCreds) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session_not_started"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *AgentServer) lookupUserBySessionToken(ctx context.Context, plain string) (*models.User, error) {
	var u models.User
	err := s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.password_hash, u.role, u.totp_secret_encrypted, u.totp_enabled,
		       u.failed_login_count, u.locked_until, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()
	`, authkit.HashToken(plain)).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.TOTPSecretEncrypted, &u.TOTPEnabled,
		&u.FailedLoginCount, &u.LockedUntil, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// UserIDFromContext is exported for tests/helpers.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	u := userFrom(ctx)
	if u == nil {
		return uuid.Nil
	}
	return u.ID
}
