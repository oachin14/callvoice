package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
)

// DIDStore persists inbound DID rows.
type DIDStore struct {
	DB *sql.DB
}

// CreateDIDInput is the minimal admin create payload.
type CreateDIDInput struct {
	Number      string
	Destination string
	CarrierID   *uuid.UUID
}

// Create upserts a DID by number (unique).
func (s *DIDStore) Create(ctx context.Context, in CreateDIDInput) (*models.DID, error) {
	number := strings.TrimSpace(in.Number)
	dest := strings.TrimSpace(in.Destination)
	if dest == "" {
		dest = "agent_pool:default"
	}
	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO dids (number, destination, carrier_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (number) DO UPDATE
		  SET destination = EXCLUDED.destination,
		      carrier_id = COALESCE(EXCLUDED.carrier_id, dids.carrier_id)
		RETURNING id, number, carrier_id, destination, created_at
	`, number, dest, in.CarrierID)
	return scanDID(row)
}

// GetByNumber looks up a DID by E.164 number.
func (s *DIDStore) GetByNumber(ctx context.Context, number string) (*models.DID, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, number, carrier_id, destination, created_at
		FROM dids
		WHERE number = $1
	`, strings.TrimSpace(number))
	d, err := scanDID(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func scanDID(row interface {
	Scan(dest ...any) error
}) (*models.DID, error) {
	var d models.DID
	var carrierID uuid.NullUUID
	if err := row.Scan(&d.ID, &d.Number, &carrierID, &d.Destination, &d.CreatedAt); err != nil {
		return nil, err
	}
	if carrierID.Valid {
		id := carrierID.UUID
		d.CarrierID = &id
	}
	return &d, nil
}
