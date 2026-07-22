package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
)

var ErrInvalidStatusTransition = errors.New("invalid status transition")
var ErrInvalidAgent = errors.New("invalid agent")

type CampaignStore struct {
	DB *sql.DB
}

type CreateCampaignInput struct {
	Name      string
	CarrierID uuid.UUID
}

type UpdateCampaignInput struct {
	Name      *string
	CarrierID *uuid.UUID
	Status    *models.CampaignStatus
}

var defaultDispositions = []struct {
	code      string
	label     string
	isContact bool
	isSuccess bool
}{
	{"NO_ANSWER", "Pas de réponse", false, false},
	{"BUSY", "Occupé", false, false},
	{"CALLBACK", "Rappel", false, false},
	{"SUCCESS", "Succès", true, true},
	{"DNC", "Ne pas appeler", false, false},
}

func ValidStatusTransition(from, to models.CampaignStatus) bool {
	if from == to {
		return true
	}
	if to == models.CampaignStatusStopped {
		return true
	}
	switch from {
	case models.CampaignStatusDraft:
		return to == models.CampaignStatusRunning
	case models.CampaignStatusRunning:
		return to == models.CampaignStatusPaused
	case models.CampaignStatusPaused:
		return to == models.CampaignStatusRunning
	default:
		return false
	}
}

func (s *CampaignStore) List(ctx context.Context) ([]models.Campaign, error) {
	rows, err := s.DB.QueryContext(ctx, campaignSelectSQL+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *CampaignStore) Get(ctx context.Context, id uuid.UUID) (*models.Campaign, error) {
	row := s.DB.QueryRowContext(ctx, campaignSelectSQL+` WHERE id = $1`, id)
	c, err := scanCampaign(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *CampaignStore) Create(ctx context.Context, in CreateCampaignInput) (*models.Campaign, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO campaigns (name, carrier_id, status, dial_mode)
		VALUES ($1, $2, 'draft', 'manual')
		RETURNING `+campaignReturningCols,
		in.Name, in.CarrierID,
	)
	c, err := scanCampaign(row)
	if err != nil {
		return nil, fmt.Errorf("insert campaign: %w", err)
	}

	for _, d := range defaultDispositions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dispositions (code, label, campaign_id, is_contact, is_success)
			VALUES ($1, $2, $3, $4, $5)
		`, d.code, d.label, c.ID, d.isContact, d.isSuccess); err != nil {
			return nil, fmt.Errorf("seed disposition %s: %w", d.code, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *CampaignStore) Update(ctx context.Context, id uuid.UUID, in UpdateCampaignInput) (*models.Campaign, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	name := cur.Name
	if in.Name != nil {
		name = *in.Name
	}
	carrierID := cur.CarrierID
	if in.CarrierID != nil {
		carrierID = *in.CarrierID
	}
	status := cur.Status
	if in.Status != nil {
		if !ValidStatusTransition(cur.Status, *in.Status) {
			return nil, ErrInvalidStatusTransition
		}
		status = *in.Status
	}

	row := s.DB.QueryRowContext(ctx, `
		UPDATE campaigns SET name = $2, carrier_id = $3, status = $4, updated_at = now()
		WHERE id = $1
		RETURNING `+campaignReturningCols,
		id, name, carrierID, status,
	)
	c, err := scanCampaign(row)
	if err != nil {
		return nil, fmt.Errorf("update campaign: %w", err)
	}
	return &c, nil
}

func (s *CampaignStore) SetAgents(ctx context.Context, campaignID uuid.UUID, userIDs []uuid.UUID) error {
	if _, err := s.Get(ctx, campaignID); err != nil {
		return err
	}

	for _, uid := range userIDs {
		var role models.UserRole
		err := s.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, uid).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidAgent
		}
		if err != nil {
			return err
		}
		if role != models.UserRoleAgent {
			return ErrInvalidAgent
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM campaign_agents WHERE campaign_id = $1`, campaignID); err != nil {
		return err
	}
	for _, uid := range userIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO campaign_agents (campaign_id, user_id) VALUES ($1, $2)
		`, campaignID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const campaignSelectSQL = `
	SELECT id, name, carrier_id, status, dial_mode, created_at, updated_at
	FROM campaigns`

const campaignReturningCols = `
	id, name, carrier_id, status, dial_mode, created_at, updated_at`

func scanCampaign(row scannable) (models.Campaign, error) {
	var c models.Campaign
	var createdAt, updatedAt time.Time
	err := row.Scan(&c.ID, &c.Name, &c.CarrierID, &c.Status, &c.DialMode, &createdAt, &updatedAt)
	if err != nil {
		return c, err
	}
	c.CreatedAt = createdAt
	c.UpdatedAt = updatedAt
	return c, nil
}
