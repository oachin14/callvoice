package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/authkit"
	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

type createUserRequest struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Role        string  `json:"role"`
	DisplayName *string `json:"display_name"`
}

type patchUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
	Disabled    *bool   `json:"disabled"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (s *Server) userStore() *store.UserStore {
	return &store.UserStore{DB: s.DB}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.userStore().List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	out := make([]userResponse, 0, len(list))
	for i := range list {
		out = append(out, toUserResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_email"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password_required"})
		return
	}

	role, errMsg := parseUserRole(req.Role)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	hash, err := authkit.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	var displayName *string
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed != "" {
			displayName = &trimmed
		}
	}

	created, err := s.userStore().Create(r.Context(), store.CreateUserInput{
		Email:        req.Email,
		PasswordHash: hash,
		Role:         role,
		DisplayName:  displayName,
	})
	if errors.Is(err, store.ErrAdminExists) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "admin_exists"})
		return
	}
	if errors.Is(err, store.ErrDuplicateEmail) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email_taken"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(created))
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req patchUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	in := store.UpdateUserInput{
		DisplayName: req.DisplayName,
		Disabled:    req.Disabled,
	}
	if req.Disabled != nil && *req.Disabled {
		now := s.Now().UTC()
		in.DisabledAt = &now
	}
	if req.Role != nil {
		role, errMsg := parseUserRole(*req.Role)
		if errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
			return
		}
		in.Role = &role
	}

	updated, err := s.userStore().Update(r.Context(), id, in)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if errors.Is(err, store.ErrAdminExists) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "admin_exists"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(updated))
}

func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password_required"})
		return
	}

	hash, err := authkit.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	if err := s.userStore().ResetPassword(r.Context(), id, hash); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parseUserRole(raw string) (models.UserRole, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(models.UserRoleAdmin):
		return models.UserRoleAdmin, ""
	case string(models.UserRoleSupervisor):
		return models.UserRoleSupervisor, ""
	case string(models.UserRoleAgent):
		return models.UserRoleAgent, ""
	default:
		return "", "invalid_role"
	}
}
