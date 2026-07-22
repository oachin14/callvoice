package models

import (
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	UserRoleAdmin      UserRole = "admin"
	UserRoleSupervisor UserRole = "supervisor"
	UserRoleAgent      UserRole = "agent"
)

type User struct {
	ID                   uuid.UUID  `json:"id"`
	Email                string     `json:"email"`
	PasswordHash         string     `json:"-"`
	Role                 UserRole   `json:"role"`
	DisplayName          *string    `json:"display_name,omitempty"`
	TOTPSecretEncrypted  []byte     `json:"-"`
	TOTPEnabled          bool       `json:"totp_enabled"`
	FailedLoginCount     int        `json:"failed_login_count"`
	LockedUntil          *time.Time `json:"locked_until,omitempty"`
	DisabledAt           *time.Time `json:"disabled_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
}

type Carrier struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	Transport         string    `json:"transport"`
	Username          *string   `json:"username,omitempty"`
	PasswordEncrypted []byte    `json:"-"`
	Realm             *string   `json:"realm,omitempty"`
	Codecs            []string  `json:"codecs"`
	CallerIDs         []string  `json:"caller_ids"`
	MaxCPS            int       `json:"max_cps"`
	MaxChannels       int       `json:"max_channels"`
	Enabled           bool      `json:"enabled"`
	Priority          int       `json:"priority"`
	CreatedAt         time.Time `json:"created_at"`
}

type DID struct {
	ID          uuid.UUID  `json:"id"`
	Number      string     `json:"number"`
	CarrierID   *uuid.UUID `json:"carrier_id,omitempty"`
	Destination string     `json:"destination"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Session struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        int64      `json:"id"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	Event     string     `json:"event"`
	IP        *string    `json:"ip,omitempty"`
	Meta      []byte     `json:"meta"`
	CreatedAt time.Time  `json:"created_at"`
}

type CampaignStatus string

const (
	CampaignStatusDraft   CampaignStatus = "draft"
	CampaignStatusRunning CampaignStatus = "running"
	CampaignStatusPaused  CampaignStatus = "paused"
	CampaignStatusStopped CampaignStatus = "stopped"
)

const DialModeManual = "manual"

type Campaign struct {
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	CarrierID uuid.UUID      `json:"carrier_id"`
	Status    CampaignStatus `json:"status"`
	DialMode  string         `json:"dial_mode"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type CampaignAgent struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	UserID     uuid.UUID `json:"user_id"`
}

type LeadList struct {
	ID         uuid.UUID `json:"id"`
	CampaignID uuid.UUID `json:"campaign_id"`
	Name       string    `json:"name"`
	ImportedAt time.Time `json:"imported_at"`
	RowCount   int       `json:"row_count"`
}

type LeadStatus string

const (
	LeadStatusNew        LeadStatus = "new"
	LeadStatusInProgress LeadStatus = "in_progress"
	LeadStatusNoAnswer   LeadStatus = "no_answer"
	LeadStatusBusy       LeadStatus = "busy"
	LeadStatusCallback   LeadStatus = "callback"
	LeadStatusDisposed   LeadStatus = "disposed"
	LeadStatusAnswered   LeadStatus = "answered"
)

type Lead struct {
	ID              uuid.UUID  `json:"id"`
	ListID          uuid.UUID  `json:"list_id"`
	Phone           string     `json:"phone"`
	Payload         []byte     `json:"payload"`
	Status          LeadStatus `json:"status"`
	DispositionID   *uuid.UUID `json:"disposition_id,omitempty"`
	AssignedAgentID *uuid.UUID `json:"assigned_agent_id,omitempty"`
}

type Disposition struct {
	ID         uuid.UUID  `json:"id"`
	Code       string     `json:"code"`
	Label      string     `json:"label"`
	CampaignID *uuid.UUID `json:"campaign_id,omitempty"`
	IsContact  bool       `json:"is_contact"`
	IsSuccess  bool       `json:"is_success"`
}

type CallLog struct {
	ID            uuid.UUID  `json:"id"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	LeadID        *uuid.UUID `json:"lead_id,omitempty"`
	AgentID       uuid.UUID  `json:"agent_id"`
	Direction     string     `json:"direction"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	DurationSec   *int       `json:"duration_sec,omitempty"`
	DispositionID *uuid.UUID `json:"disposition_id,omitempty"`
	ToNumber      string     `json:"to_number"`
}
