package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/callvoice/callvoice/services/edge/internal/dialer"
)

// MountCalls registers outbound call routes (requires Dialer).
func (s *AgentServer) MountCalls(mux *http.ServeMux) {
	mux.Handle("POST /calls/outbound", s.withAuth(s.handleOutbound))
	mux.Handle("POST /calls/hangup", s.withAuth(s.handleHangup))
}

func (s *AgentServer) handleOutbound(w http.ResponseWriter, r *http.Request) {
	if s.Dialer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dialer_unavailable"})
		return
	}
	user := userFrom(r.Context())
	var body struct {
		To       string `json:"to"`
		CallerID string `json:"callerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	res, err := s.Dialer.Originate(r.Context(), dialer.OutboundRequest{
		AgentID:  user.ID,
		To:       body.To,
		CallerID: body.CallerID,
	})
	if err != nil {
		writeOutboundError(w, err)
		return
	}
	s.publishCallState(map[string]any{
		"call_uuid":  res.CallUUID,
		"agent_id":   user.ID.String(),
		"carrier_id": res.CarrierID.String(),
		"to":         res.To,
		"state":      "active",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"call_uuid":  res.CallUUID,
		"carrier_id": res.CarrierID,
		"to":         res.To,
	})
}

func (s *AgentServer) handleHangup(w http.ResponseWriter, r *http.Request) {
	if s.Dialer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dialer_unavailable"})
		return
	}
	user := userFrom(r.Context())
	var body struct {
		UUID string `json:"uuid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.Dialer.Hangup(r.Context(), user.ID, body.UUID); err != nil {
		if errors.Is(err, dialer.ErrNoActiveCall) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no_active_call"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	s.publishCallState(map[string]any{
		"call_uuid": body.UUID,
		"agent_id":  user.ID.String(),
		"state":     "ended",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeOutboundError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dialer.ErrInvalidE164):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_e164"})
	case errors.Is(err, dialer.ErrCarrierCapacity):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "carrier_capacity"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
	}
}
