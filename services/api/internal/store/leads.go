package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/internal/models"
)

type LeadStore struct {
	DB *sql.DB
}

type ImportLeadRow struct {
	Phone   string
	Payload map[string]string
}

type ImportLeadsInput struct {
	CampaignID uuid.UUID
	Name       string
	Rows       []ImportLeadRow
}

type ImportLeadsResult struct {
	ListID   uuid.UUID
	Imported int
}

func (s *LeadStore) Import(ctx context.Context, in ImportLeadsInput) (*ImportLeadsResult, error) {
	campaignStore := &CampaignStore{DB: s.DB}
	if _, err := campaignStore.Get(ctx, in.CampaignID); err != nil {
		return nil, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var listID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO lead_lists (campaign_id, name, row_count)
		VALUES ($1, $2, 0)
		RETURNING id
	`, in.CampaignID, in.Name).Scan(&listID)
	if err != nil {
		return nil, fmt.Errorf("insert lead_list: %w", err)
	}

	imported := 0
	for _, row := range in.Rows {
		payload := row.Payload
		if payload == nil {
			payload = map[string]string{}
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO leads (list_id, phone, payload, status)
			VALUES ($1, $2, $3, $4)
		`, listID, row.Phone, payloadJSON, models.LeadStatusNew); err != nil {
			return nil, fmt.Errorf("insert lead: %w", err)
		}
		imported++
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE lead_lists SET row_count = $2 WHERE id = $1
	`, listID, imported); err != nil {
		return nil, fmt.Errorf("update row_count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ImportLeadsResult{
		ListID:   listID,
		Imported: imported,
	}, nil
}
