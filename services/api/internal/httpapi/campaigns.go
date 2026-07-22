package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/csvimport"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

type createCampaignRequest struct {
	Name      string `json:"name"`
	CarrierID string `json:"carrier_id"`
}

type patchCampaignRequest struct {
	Name      *string `json:"name"`
	CarrierID *string `json:"carrier_id"`
	Status    *string `json:"status"`
}

type assignAgentsRequest struct {
	UserIDs []string `json:"user_ids"`
}

type campaignResponse struct {
	ID        uuid.UUID             `json:"id"`
	Name      string                `json:"name"`
	CarrierID uuid.UUID             `json:"carrier_id"`
	Status    models.CampaignStatus `json:"status"`
	DialMode  string                `json:"dial_mode"`
	CreatedAt string                `json:"created_at"`
	UpdatedAt string                `json:"updated_at"`
}

func (s *Server) campaignStore() *store.CampaignStore {
	return &store.CampaignStore{DB: s.DB}
}

func (s *Server) leadStore() *store.LeadStore {
	return &store.LeadStore{DB: s.DB}
}

func (s *Server) dispositionStore() *store.DispositionStore {
	return &store.DispositionStore{DB: s.DB}
}

// RequireSupervisor ensures the authenticated user has role admin or supervisor.
func (s *Server) RequireSupervisor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if user.Role != models.UserRoleAdmin && user.Role != models.UserRoleSupervisor {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleListCampaigns(w http.ResponseWriter, r *http.Request) {
	list, err := s.campaignStore().List(r.Context())
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

func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var req createCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name_required"})
		return
	}

	carrierID, err := uuid.Parse(strings.TrimSpace(req.CarrierID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_carrier_id"})
		return
	}

	if _, err := s.carrierStore().Get(r.Context(), carrierID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "carrier_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	created, err := s.campaignStore().Create(r.Context(), store.CreateCampaignInput{
		Name:      req.Name,
		CarrierID: carrierID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusCreated, toCampaignResponse(created))
}

func (s *Server) handlePatchCampaign(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req patchCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	in := store.UpdateCampaignInput{}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name_required"})
			return
		}
		in.Name = &trimmed
	}
	if req.CarrierID != nil {
		carrierID, err := uuid.Parse(strings.TrimSpace(*req.CarrierID))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_carrier_id"})
			return
		}
		if _, err := s.carrierStore().Get(r.Context(), carrierID); errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "carrier_not_found"})
			return
		} else if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		in.CarrierID = &carrierID
	}
	if req.Status != nil {
		status, errMsg := parseCampaignStatus(*req.Status)
		if errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
			return
		}
		in.Status = &status
	}

	updated, err := s.campaignStore().Update(r.Context(), id, in)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if errors.Is(err, store.ErrInvalidStatusTransition) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_status_transition"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, toCampaignResponse(updated))
}

func (s *Server) handleAssignCampaignAgents(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req assignAgentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	userIDs := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		uid, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_user_id"})
			return
		}
		userIDs = append(userIDs, uid)
	}

	if err := s.campaignStore().SetAgents(r.Context(), id, userIDs); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	} else if errors.Is(err, store.ErrInvalidAgent) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_agent"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parseCampaignStatus(raw string) (models.CampaignStatus, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(models.CampaignStatusDraft):
		return models.CampaignStatusDraft, ""
	case string(models.CampaignStatusRunning):
		return models.CampaignStatusRunning, ""
	case string(models.CampaignStatusPaused):
		return models.CampaignStatusPaused, ""
	case string(models.CampaignStatusStopped):
		return models.CampaignStatusStopped, ""
	default:
		return "", "invalid_status"
	}
}

type createDispositionRequest struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	IsContact bool   `json:"is_contact"`
	IsSuccess bool   `json:"is_success"`
}

type dispositionResponse struct {
	ID         uuid.UUID  `json:"id"`
	Code       string     `json:"code"`
	Label      string     `json:"label"`
	CampaignID *uuid.UUID `json:"campaign_id,omitempty"`
	IsContact  bool       `json:"is_contact"`
	IsSuccess  bool       `json:"is_success"`
}

type importLeadListResponse struct {
	ListID    uuid.UUID           `json:"list_id"`
	Imported  int                 `json:"imported"`
	Rejected  int                 `json:"rejected"`
	Errors    []importRowError    `json:"errors"`
}

type importRowError struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

func (s *Server) handleImportLeadList(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_multipart"})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file_required"})
		return
	}
	defer file.Close()

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Import"
	}

	rows, rowErrs, err := csvimport.Parse(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_csv"})
		return
	}

	importRows := make([]store.ImportLeadRow, 0, len(rows))
	for _, row := range rows {
		importRows = append(importRows, store.ImportLeadRow{
			Phone:   row.Phone,
			Payload: row.Payload,
		})
	}

	result, err := s.leadStore().Import(r.Context(), store.ImportLeadsInput{
		CampaignID: campaignID,
		Name:       name,
		Rows:       importRows,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	errOut := make([]importRowError, 0, len(rowErrs))
	for i, re := range rowErrs {
		if i >= 100 {
			break
		}
		errOut = append(errOut, importRowError{Line: re.Line, Reason: re.Reason})
	}

	writeJSON(w, http.StatusCreated, importLeadListResponse{
		ListID:   result.ListID,
		Imported: result.Imported,
		Rejected: len(rowErrs),
		Errors:   errOut,
	})
}

func (s *Server) handleListDispositions(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	if _, err := s.campaignStore().Get(r.Context(), campaignID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	} else if err != nil {
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

func (s *Server) handleCreateDisposition(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req createDispositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	req.Code = strings.TrimSpace(req.Code)
	req.Label = strings.TrimSpace(req.Label)
	if req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code_required"})
		return
	}
	if req.Label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label_required"})
		return
	}

	if _, err := s.campaignStore().Get(r.Context(), campaignID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	created, err := s.dispositionStore().Create(r.Context(), campaignID, store.CreateDispositionInput{
		Code:      req.Code,
		Label:     req.Label,
		IsContact: req.IsContact,
		IsSuccess: req.IsSuccess,
	})
	if errors.Is(err, store.ErrDuplicateDisposition) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "duplicate_code"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusCreated, toDispositionResponse(created))
}

func toDispositionResponse(d *models.Disposition) dispositionResponse {
	return dispositionResponse{
		ID:         d.ID,
		Code:       d.Code,
		Label:      d.Label,
		CampaignID: d.CampaignID,
		IsContact:  d.IsContact,
		IsSuccess:  d.IsSuccess,
	}
}

func toCampaignResponse(c *models.Campaign) campaignResponse {
	return campaignResponse{
		ID:        c.ID,
		Name:      c.Name,
		CarrierID: c.CarrierID,
		Status:    c.Status,
		DialMode:  c.DialMode,
		CreatedAt: c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}
