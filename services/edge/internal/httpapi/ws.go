package httpapi

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/callvoice/callvoice/services/edge/internal/live"
)

const wsHeartbeatInterval = 30 * time.Second

// MountWS registers GET /ws (cookie session required).
func (s *AgentServer) MountWS(mux *http.ServeMux) {
	mux.Handle("GET /ws", s.withAuth(s.handleWS))
}

func (s *AgentServer) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live_unavailable"})
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: s.checkWSOrigin,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	user := userFrom(r.Context())
	userID := user.ID.String()
	ch := s.Hub.Subscribe(userID)
	defer s.Hub.Unsubscribe(userID, ch)

	// Initial hello so clients know the socket is authenticated.
	_ = writeWSEvent(conn, live.Event{
		Type: live.TypeAgentState,
		Payload: map[string]any{
			"user_id":  userID,
			"agent_id": userID,
			"state":    "subscribed",
			"email":    user.Email,
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(wsHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeWSEvent(conn, ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeWSEvent(conn, live.Event{
				Type: live.TypeHeartbeat,
				Payload: map[string]any{
					"ts": time.Now().UTC().Format(time.RFC3339),
				},
			}); err != nil {
				return
			}
		}
	}
}

func writeWSEvent(conn *websocket.Conn, ev live.Event) error {
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(ev)
}

func (s *AgentServer) checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, o := range s.CORS {
		if o == origin || o == "*" {
			return true
		}
	}
	return false
}

func (s *AgentServer) publishAgentState(userID, state string, extra map[string]any) {
	if s.Hub == nil {
		return
	}
	payload := map[string]any{
		"user_id":  userID,
		"agent_id": userID,
		"state":    state,
	}
	for k, v := range extra {
		payload[k] = v
	}
	s.Hub.Broadcast(userID, live.Event{Type: live.TypeAgentState, Payload: payload})
}

func (s *AgentServer) publishCallState(userID string, payload map[string]any) {
	if s.Hub == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["user_id"] = userID
	if _, ok := payload["agent_id"]; !ok {
		payload["agent_id"] = userID
	}
	s.Hub.Broadcast(userID, live.Event{Type: live.TypeCallState, Payload: payload})
}
