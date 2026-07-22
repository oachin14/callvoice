package httpapi

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/live"
)

const liveRefreshInterval = 2 * time.Second

// MountLiveWS registers GET /ws/live (admin|supervisor session required).
func (s *AgentServer) MountLiveWS(mux *http.ServeMux) {
	mux.Handle("GET /ws/live", s.withSupervisorAuth(s.handleLiveWS))
}

func (s *AgentServer) withSupervisorAuth(next http.HandlerFunc) http.Handler {
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
		if user.Role != models.UserRoleAdmin && user.Role != models.UserRoleSupervisor {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if s.adminTOTPSetupRequired(user) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "totp_setup_required"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, user)))
	})
}

func (s *AgentServer) handleLiveWS(w http.ResponseWriter, r *http.Request) {
	if s.RDB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live_unavailable"})
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: s.checkWSOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws/live upgrade: %v", err)
		return
	}
	defer conn.Close()

	sendSnapshot := func() error {
		wb, err := live.BuildSnapshot(r.Context(), s.RDB)
		if err != nil {
			return err
		}
		return writeWSEvent(conn, live.Event{Type: live.TypeLiveSnapshot, Payload: wb})
	}

	if err := sendSnapshot(); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(liveRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := sendSnapshot(); err != nil {
				return
			}
		}
	}
}
