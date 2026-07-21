package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/cryptokit"
	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/api/internal/store"
)

const carriersChangedChannel = "carriers.changed"

// CarrierChangePublisher notifies edge when carrier config changes.
type CarrierChangePublisher interface {
	PublishCarriersChanged(ctx context.Context, payload string) error
}

// NoopCarrierPublisher discards change notifications (tests / Redis-less labs).
type NoopCarrierPublisher struct{}

func (NoopCarrierPublisher) PublishCarriersChanged(context.Context, string) error { return nil }

// RedisCarrierPublisher publishes to Redis Pub/Sub.
type RedisCarrierPublisher struct {
	Client *redis.Client
}

func (p *RedisCarrierPublisher) PublishCarriersChanged(ctx context.Context, payload string) error {
	if p == nil || p.Client == nil {
		return nil
	}
	return p.Client.Publish(ctx, carriersChangedChannel, payload).Err()
}

// NewRedisCarrierPublisherFromURL connects when REDIS_URL is set; otherwise returns noop.
func NewRedisCarrierPublisherFromURL(redisURL string) (CarrierChangePublisher, error) {
	if strings.TrimSpace(redisURL) == "" {
		return NoopCarrierPublisher{}, nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisCarrierPublisher{Client: client}, nil
}

type carrierResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Transport   string    `json:"transport"`
	Username    *string   `json:"username,omitempty"`
	PasswordSet bool      `json:"password_set"`
	Realm       *string   `json:"realm,omitempty"`
	Codecs      []string  `json:"codecs"`
	CallerIDs   []string  `json:"caller_ids"`
	MaxCPS      int       `json:"max_cps"`
	MaxChannels int       `json:"max_channels"`
	Enabled     bool      `json:"enabled"`
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
}

type createCarrierRequest struct {
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Port        *int     `json:"port"`
	Transport   string   `json:"transport"`
	Username    *string  `json:"username"`
	Password    *string  `json:"password"`
	Realm       *string  `json:"realm"`
	Codecs      []string `json:"codecs"`
	CallerIDs   []string `json:"caller_ids"`
	MaxCPS      *int     `json:"max_cps"`
	MaxChannels *int     `json:"max_channels"`
	Enabled     *bool    `json:"enabled"`
	Priority    *int     `json:"priority"`
}

type patchCarrierRequest struct {
	Name        *string  `json:"name"`
	Host        *string  `json:"host"`
	Port        *int     `json:"port"`
	Transport   *string  `json:"transport"`
	Username    *string  `json:"username"`
	Password    *string  `json:"password"`
	Realm       *string  `json:"realm"`
	Codecs      []string `json:"codecs"`
	CallerIDs   []string `json:"caller_ids"`
	MaxCPS      *int     `json:"max_cps"`
	MaxChannels *int     `json:"max_channels"`
	Enabled     *bool    `json:"enabled"`
	Priority    *int     `json:"priority"`
}

func toCarrierResponse(c *models.Carrier) carrierResponse {
	codecs := c.Codecs
	if codecs == nil {
		codecs = []string{}
	}
	callerIDs := c.CallerIDs
	if callerIDs == nil {
		callerIDs = []string{}
	}
	return carrierResponse{
		ID:          c.ID,
		Name:        c.Name,
		Host:        c.Host,
		Port:        c.Port,
		Transport:   c.Transport,
		Username:    c.Username,
		PasswordSet: len(c.PasswordEncrypted) > 0,
		Realm:       c.Realm,
		Codecs:      codecs,
		CallerIDs:   callerIDs,
		MaxCPS:      c.MaxCPS,
		MaxChannels: c.MaxChannels,
		Enabled:     c.Enabled,
		Priority:    c.Priority,
		CreatedAt:   c.CreatedAt,
	}
}

func (s *Server) carrierStore() *store.CarrierStore {
	return &store.CarrierStore{DB: s.DB}
}

func (s *Server) publishCarriersChanged(ctx context.Context, payload string) {
	if s.CarrierPublisher == nil {
		return
	}
	_ = s.CarrierPublisher.PublishCarriersChanged(ctx, payload)
}

// RequireAdmin ensures the authenticated user has role admin.
func (s *Server) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if user.Role != models.UserRoleAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleListCarriers(w http.ResponseWriter, r *http.Request) {
	list, err := s.carrierStore().List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	out := make([]carrierResponse, 0, len(list))
	for i := range list {
		out = append(out, toCarrierResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateCarrier(w http.ResponseWriter, r *http.Request) {
	var req createCarrierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	if req.Transport == "" {
		req.Transport = "udp"
	}

	maxCPS := 30
	if req.MaxCPS != nil {
		maxCPS = *req.MaxCPS
	}
	maxChannels := 100
	if req.MaxChannels != nil {
		maxChannels = *req.MaxChannels
	}
	if errMsg := validateCarrierFields(req.Name, req.Host, req.Transport, maxCPS, maxChannels, req.Port); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	port := 5060
	if req.Port != nil {
		port = *req.Port
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}

	var enc []byte
	if req.Password != nil && *req.Password != "" {
		blob, err := cryptokit.Encrypt(s.CarrierSecretKey, []byte(*req.Password))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		enc = blob
	}

	created, err := s.carrierStore().Create(r.Context(), store.CreateCarrierInput{
		Name:              req.Name,
		Host:              req.Host,
		Port:              port,
		Transport:         req.Transport,
		Username:          trimPtr(req.Username),
		PasswordEncrypted: enc,
		Realm:             trimPtr(req.Realm),
		Codecs:            req.Codecs,
		CallerIDs:         req.CallerIDs,
		MaxCPS:            maxCPS,
		MaxChannels:       maxChannels,
		Enabled:           enabled,
		Priority:          priority,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	s.publishCarriersChanged(r.Context(), created.ID.String())
	writeJSON(w, http.StatusCreated, toCarrierResponse(created))
}

func (s *Server) handlePatchCarrier(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}

	var req patchCarrierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}

	cur, err := s.carrierStore().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	name := cur.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	host := cur.Host
	if req.Host != nil {
		host = strings.TrimSpace(*req.Host)
	}
	transport := cur.Transport
	if req.Transport != nil {
		transport = strings.ToLower(strings.TrimSpace(*req.Transport))
	}
	maxCPS := cur.MaxCPS
	if req.MaxCPS != nil {
		maxCPS = *req.MaxCPS
	}
	maxChannels := cur.MaxChannels
	if req.MaxChannels != nil {
		maxChannels = *req.MaxChannels
	}
	port := &cur.Port
	if req.Port != nil {
		port = req.Port
	}
	if errMsg := validateCarrierFields(name, host, transport, maxCPS, maxChannels, port); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	in := store.UpdateCarrierInput{
		Name:        &name,
		Host:        &host,
		Transport:   &transport,
		MaxCPS:      &maxCPS,
		MaxChannels: &maxChannels,
		Codecs:      req.Codecs,
		CallerIDs:   req.CallerIDs,
		Enabled:     req.Enabled,
		Priority:    req.Priority,
		Port:        req.Port,
	}
	if req.Username != nil {
		trimmed := strings.TrimSpace(*req.Username)
		if trimmed == "" {
			in.ClearUsername = true
		} else {
			in.Username = &trimmed
		}
	}
	if req.Realm != nil {
		trimmed := strings.TrimSpace(*req.Realm)
		if trimmed == "" {
			in.ClearRealm = true
		} else {
			in.Realm = &trimmed
		}
	}
	if req.Password != nil {
		if *req.Password == "" {
			in.ClearPassword = true
		} else {
			blob, err := cryptokit.Encrypt(s.CarrierSecretKey, []byte(*req.Password))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
				return
			}
			in.PasswordEncrypted = blob
		}
	}

	updated, err := s.carrierStore().Update(r.Context(), id, in)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	s.publishCarriersChanged(r.Context(), updated.ID.String())
	writeJSON(w, http.StatusOK, toCarrierResponse(updated))
}

func (s *Server) handleDeleteCarrier(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_id"})
		return
	}
	if err := s.carrierStore().Delete(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	s.publishCarriersChanged(r.Context(), id.String())
	w.WriteHeader(http.StatusNoContent)
}

func validateCarrierFields(name, host, transport string, maxCPS, maxChannels int, port *int) string {
	if name == "" {
		return "name_required"
	}
	if host == "" {
		return "host_required"
	}
	switch transport {
	case "udp", "tcp", "tls":
	default:
		return "invalid_transport"
	}
	if maxCPS <= 0 {
		return "invalid_max_cps"
	}
	if maxChannels <= 0 {
		return "invalid_max_channels"
	}
	if port != nil && (*port <= 0 || *port > 65535) {
		return "invalid_port"
	}
	return ""
}

func trimPtr(v *string) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	return &s
}
