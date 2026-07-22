package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"encoding/json"

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

func (s *LeadStore) ClaimNext(ctx context.Context, campaignID, agentID uuid.UUID) (*models.Lead, error) {
	campaignStore := &CampaignStore{DB: s.DB}
	campaign, err := campaignStore.Get(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if campaign.Status != models.CampaignStatusRunning {
		return nil, ErrInvalidCampaignState
	}

	assigned, err := campaignStore.IsAgentAssigned(ctx, campaignID, agentID)
	if err != nil {
		return nil, err
	}
	if !assigned {
		return nil, ErrForbidden
	}

	row := s.DB.QueryRowContext(ctx, `
		WITH picked AS (
			SELECT l.id
			FROM leads l
			JOIN lead_lists ll ON ll.id = l.list_id
			WHERE ll.campaign_id = $1 AND l.status = 'new'
			ORDER BY l.id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE leads l
		SET status = 'in_progress', assigned_agent_id = $2
		FROM picked
		WHERE l.id = picked.id
		RETURNING l.id, l.list_id, l.phone, l.payload, l.status, l.disposition_id, l.assigned_agent_id
	`, campaignID, agentID)

	lead, err := scanLead(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoLeadsAvailable
	}
	if err != nil {
		return nil, fmt.Errorf("claim lead: %w", err)
	}
	return &lead, nil
}

func scanLead(row scannable) (models.Lead, error) {
	var l models.Lead
	var dispositionID uuid.NullUUID
	var assignedAgentID uuid.NullUUID
	err := row.Scan(&l.ID, &l.ListID, &l.Phone, &l.Payload, &l.Status, &dispositionID, &assignedAgentID)
	if err != nil {
		return l, err
	}
	if dispositionID.Valid {
		l.DispositionID = &dispositionID.UUID
	}
	if assignedAgentID.Valid {
		l.AssignedAgentID = &assignedAgentID.UUID
	}
	return l, nil
}
