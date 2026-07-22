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

var ErrNoLeadsAvailable = errors.New("no leads available")

type CallLogStore struct {
	DB *sql.DB
}

type RecordDispositionInput struct {
	CampaignID    uuid.UUID
	LeadID        uuid.UUID
	AgentID       uuid.UUID
	DispositionID uuid.UUID
	ToNumber      string
	StartedAt     time.Time
	EndedAt       *time.Time
	DurationSec   *int
}

func (s *CallLogStore) RecordDisposition(ctx context.Context, in RecordDispositionInput) (*models.CallLog, error) {
	dispositionStore := &DispositionStore{DB: s.DB}
	disp, err := dispositionStore.Get(ctx, in.DispositionID)
	if err != nil {
		return nil, err
	}
	if disp.CampaignID == nil || *disp.CampaignID != in.CampaignID {
		return nil, ErrNotFound
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var assignedAgentID uuid.NullUUID
	var currentStatus models.LeadStatus
	err = tx.QueryRowContext(ctx, `
		SELECT l.status, l.assigned_agent_id
		FROM leads l
		JOIN lead_lists ll ON ll.id = l.list_id
		WHERE l.id = $1 AND ll.campaign_id = $2
	`, in.LeadID, in.CampaignID).Scan(&currentStatus, &assignedAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup lead: %w", err)
	}
	if !assignedAgentID.Valid || assignedAgentID.UUID != in.AgentID {
		return nil, ErrNotFound
	}

	newStatus := leadStatusFromDisposition(*disp)
	_, err = tx.ExecContext(ctx, `
		UPDATE leads
		SET status = $2, disposition_id = $3
		WHERE id = $1
	`, in.LeadID, newStatus, in.DispositionID)
	if err != nil {
		return nil, fmt.Errorf("update lead: %w", err)
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO call_logs (
			campaign_id, lead_id, agent_id, direction,
			started_at, ended_at, duration_sec, disposition_id, to_number
		) VALUES ($1, $2, $3, 'outbound', $4, $5, $6, $7, $8)
		RETURNING id, campaign_id, lead_id, agent_id, direction, started_at, ended_at, duration_sec, disposition_id, to_number
	`, in.CampaignID, in.LeadID, in.AgentID, in.StartedAt, in.EndedAt, in.DurationSec, in.DispositionID, in.ToNumber)

	cl, err := scanCallLog(row)
	if err != nil {
		return nil, fmt.Errorf("insert call_log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &cl, nil
}

func leadStatusFromDisposition(d models.Disposition) models.LeadStatus {
	switch d.Code {
	case "NO_ANSWER":
		return models.LeadStatusNoAnswer
	case "BUSY":
		return models.LeadStatusBusy
	case "CALLBACK":
		return models.LeadStatusCallback
	case "SUCCESS":
		return models.LeadStatusAnswered
	case "DNC":
		return models.LeadStatusDisposed
	default:
		if d.IsSuccess {
			return models.LeadStatusAnswered
		}
		return models.LeadStatusDisposed
	}
}

func scanCallLog(row scannable) (models.CallLog, error) {
	var cl models.CallLog
	var leadID uuid.NullUUID
	var dispositionID uuid.NullUUID
	var endedAt sql.NullTime
	var durationSec sql.NullInt64
	err := row.Scan(
		&cl.ID, &cl.CampaignID, &leadID, &cl.AgentID, &cl.Direction,
		&cl.StartedAt, &endedAt, &durationSec, &dispositionID, &cl.ToNumber,
	)
	if err != nil {
		return cl, err
	}
	if leadID.Valid {
		cl.LeadID = &leadID.UUID
	}
	if dispositionID.Valid {
		cl.DispositionID = &dispositionID.UUID
	}
	if endedAt.Valid {
		cl.EndedAt = &endedAt.Time
	}
	if durationSec.Valid {
		d := int(durationSec.Int64)
		cl.DurationSec = &d
	}
	return cl, nil
}
