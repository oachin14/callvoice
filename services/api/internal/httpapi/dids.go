package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

var e164DIDRe = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)

type createDIDRequest struct {
	Number      string     `json:"number"`
	Destination string     `json:"destination"`
	CarrierID   *uuid.UUID `json:"carrier_id"`
}

type didResponse struct {
	ID          uuid.UUID  `json:"id"`
	Number      string     `json:"number"`
	CarrierID   *uuid.UUID `json:"carrier_id,omitempty"`
	Destination string     `json:"destination"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toDIDResponse(d *models.DID) didResponse {
	return didResponse{
		ID:          d.ID,
		Number:      d.Number,
		CarrierID:   d.CarrierID,
		Destination: d.Destination,
		CreatedAt:   d.CreatedAt,
	}
}

func (s *Server) didStore() *store.DIDStore {
	return &store.DIDStore{DB: s.DB}
}

func (s *Server) handleCreateDID(w http.ResponseWriter, r *http.Request) {
	var req createDIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	req.Number = strings.TrimSpace(req.Number)
	req.Destination = strings.TrimSpace(req.Destination)
	if req.Destination == "" {
		req.Destination = "agent_pool:default"
	}
	if !e164DIDRe.MatchString(req.Number) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_number"})
		return
	}
	if !strings.HasPrefix(req.Destination, "agent_pool:") && req.Destination != "queue:default" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_destination"})
		return
	}

	created, err := s.didStore().Create(r.Context(), store.CreateDIDInput{
		Number:      req.Number,
		Destination: req.Destination,
		CarrierID:   req.CarrierID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusCreated, toDIDResponse(created))
}
