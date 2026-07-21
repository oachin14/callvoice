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
	TOTPSecretEncrypted  []byte     `json:"-"`
	TOTPEnabled          bool       `json:"totp_enabled"`
	FailedLoginCount     int        `json:"failed_login_count"`
	LockedUntil          *time.Time `json:"locked_until,omitempty"`
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
