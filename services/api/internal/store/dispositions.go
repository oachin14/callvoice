package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/callvoice/callvoice/internal/models"
)

var ErrDuplicateDisposition = errors.New("duplicate disposition code")

type DispositionStore struct {
	DB *sql.DB
}

type CreateDispositionInput struct {
	Code      string
	Label     string
	IsContact bool
	IsSuccess bool
}

func (s *DispositionStore) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]models.Disposition, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, code, label, campaign_id, is_contact, is_success
		FROM dispositions
		WHERE campaign_id = $1
		ORDER BY code ASC
	`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Disposition
	for rows.Next() {
		d, err := scanDisposition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DispositionStore) Create(ctx context.Context, campaignID uuid.UUID, in CreateDispositionInput) (*models.Disposition, error) {
	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO dispositions (code, label, campaign_id, is_contact, is_success)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, code, label, campaign_id, is_contact, is_success
	`, strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Label), campaignID, in.IsContact, in.IsSuccess)

	d, err := scanDisposition(row)
	if err == nil {
		return &d, nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return nil, ErrDuplicateDisposition
	}
	if err != nil {
		return nil, fmt.Errorf("insert disposition: %w", err)
	}
	return &d, nil
}

func scanDisposition(row scannable) (models.Disposition, error) {
	var d models.Disposition
	var campaignID uuid.NullUUID
	err := row.Scan(&d.ID, &d.Code, &d.Label, &campaignID, &d.IsContact, &d.IsSuccess)
	if err != nil {
		return d, err
	}
	if campaignID.Valid {
		d.CampaignID = &campaignID.UUID
	}
	return d, nil
}
