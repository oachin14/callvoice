package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

type leadResponse struct {
	ID              uuid.UUID         `json:"id"`
	ListID          uuid.UUID         `json:"list_id"`
	Phone           string            `json:"phone"`
	Payload         map[string]string `json:"payload"`
	Status          models.LeadStatus `json:"status"`
	DispositionID   *uuid.UUID        `json:"disposition_id,omitempty"`
	AssignedAgentID *uuid.UUID        `json:"assigned_agent_id,omitempty"`
}

type dispositionRequest struct {
	CampaignID    string  `json:"campaign_id"`
	LeadID        string  `json:"lead_id"`
	DispositionID string  `json:"disposition_id"`
	CallUUID      *string `json:"call_uuid,omitempty"`
	ToNumber      string  `json:"to_number"`
	StartedAt     *string `json:"started_at,omitempty"`
	EndedAt       *string `json:"ended_at,omitempty"`
	DurationSec   *int    `json:"duration_sec,omitempty"`
}

type callLogResponse struct {
	ID            uuid.UUID  `json:"id"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	LeadID        *uuid.UUID `json:"lead_id,omitempty"`
	AgentID       uuid.UUID  `json:"agent_id"`
	Direction     string     `json:"direction"`
	StartedAt     string     `json:"started_at"`
	EndedAt       *string    `json:"ended_at,omitempty"`
	DurationSec   *int       `json:"duration_sec,omitempty"`
	DispositionID *uuid.UUID `json:"disposition_id,omitempty"`
	ToNumber      string     `json:"to_number"`
}

func (s *Server) callLogStore() *store.CallLogStore {
	return &store.CallLogStore{DB: s.DB}
}

// RequireAgent ensures the authenticated user has role agent.
func (s *Server) RequireAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if user.Role != models.UserRoleAgent {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAgentListCampaigns(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	list, err := s.campaignStore().ListRunningForAgent(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	out := make([]campaignResponse, 0, len(list))
	for i := range list {
		out = append(out, toCampaignResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAgentJoinCampaign(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	err = s.campaignStore().ValidateAgentJoin(r.Context(), campaignID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if errors.Is(err, store.ErrInvalidCampaignState) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "campaign_not_running"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentListDispositions(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	if err := s.campaignStore().ValidateAgentJoin(r.Context(), campaignID, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if errors.Is(err, store.ErrInvalidCampaignState) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "campaign_not_running"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	list, err := s.dispositionStore().ListByCampaign(r.Context(), campaignID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	out := make([]dispositionResponse, 0, len(list))
	for i := range list {
		out = append(out, toDispositionResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAgentNextLead(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	rawCampaignID := strings.TrimSpace(r.URL.Query().Get("campaign_id"))
	if rawCampaignID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "campaign_id_required"})
		return
	}
	campaignID, err := uuid.Parse(rawCampaignID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_campaign_id"})
		return
	}

	lead, err := s.leadStore().ClaimNext(r.Context(), campaignID, user.ID)
	if errors.Is(err, store.ErrNoLeadsAvailable) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if errors.Is(err, store.ErrInvalidCampaignState) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "campaign_not_running"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, toLeadResponse(lead))
}

func (s *Server) handleAgentDisposition(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var req dispositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	campaignID, err := uuid.Parse(strings.TrimSpace(req.CampaignID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_campaign_id"})
		return
	}
	leadID, err := uuid.Parse(strings.TrimSpace(req.LeadID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_lead_id"})
		return
	}
	dispositionID, err := uuid.Parse(strings.TrimSpace(req.DispositionID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_disposition_id"})
		return
	}

	req.ToNumber = strings.TrimSpace(req.ToNumber)
	if req.ToNumber == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to_number_required"})
		return
	}

	startedAt := s.Now().UTC()
	if req.StartedAt != nil && strings.TrimSpace(*req.StartedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.StartedAt))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_started_at"})
			return
		}
		startedAt = parsed.UTC()
	}

	var endedAt *time.Time
	if req.EndedAt != nil && strings.TrimSpace(*req.EndedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.EndedAt))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_ended_at"})
			return
		}
		t := parsed.UTC()
		endedAt = &t
	}

	if err := s.campaignStore().ValidateAgentJoin(r.Context(), campaignID, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if errors.Is(err, store.ErrInvalidCampaignState) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "campaign_not_running"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	cl, err := s.callLogStore().RecordDisposition(r.Context(), store.RecordDispositionInput{
		CampaignID:    campaignID,
		LeadID:        leadID,
		AgentID:       user.ID,
		DispositionID: dispositionID,
		ToNumber:      req.ToNumber,
		StartedAt:     startedAt,
		EndedAt:       endedAt,
		DurationSec:   req.DurationSec,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusCreated, toCallLogResponse(cl))
}

func toLeadResponse(l *models.Lead) leadResponse {
	payload := map[string]string{}
	if len(l.Payload) > 0 {
		_ = json.Unmarshal(l.Payload, &payload)
	}
	return leadResponse{
		ID:              l.ID,
		ListID:          l.ListID,
		Phone:           l.Phone,
		Payload:         payload,
		Status:          l.Status,
		DispositionID:   l.DispositionID,
		AssignedAgentID: l.AssignedAgentID,
	}
}

func toCallLogResponse(cl *models.CallLog) callLogResponse {
	out := callLogResponse{
		ID:            cl.ID,
		CampaignID:    cl.CampaignID,
		LeadID:        cl.LeadID,
		AgentID:       cl.AgentID,
		Direction:     cl.Direction,
		StartedAt:     cl.StartedAt.UTC().Format(time.RFC3339),
		DurationSec:   cl.DurationSec,
		DispositionID: cl.DispositionID,
		ToNumber:      cl.ToNumber,
	}
	if cl.EndedAt != nil {
		s := cl.EndedAt.UTC().Format(time.RFC3339)
		out.EndedAt = &s
	}
	return out
}
