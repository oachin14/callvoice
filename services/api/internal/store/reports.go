package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxReportExportRows = 50_000

var ErrExportTooLarge = errors.New("export too large")

type ReportStore struct {
	DB *sql.DB
}

type ReportFilters struct {
	From       *time.Time
	To         *time.Time
	CampaignID *uuid.UUID
	AgentID    *uuid.UUID
}

type DispositionCount struct {
	Code  string
	Label string
	Count int
}

type ReportSummary struct {
	Calls             int
	TotalDurationSec  int64
	AvgDurationSec    float64
	ByDisposition     []DispositionCount
	ContactRate       *float64
	SuccessRate       *float64
}

type ReportExportRow struct {
	StartedAt        time.Time
	EndedAt          *time.Time
	DurationSec      *int
	CampaignID       uuid.UUID
	AgentID          uuid.UUID
	ToNumber         string
	DispositionCode  *string
	LeadID           *uuid.UUID
}

func (s *ReportStore) Summary(ctx context.Context, f ReportFilters) (*ReportSummary, error) {
	where, args := reportWhereClause(f)

	var calls int
	var totalDuration sql.NullInt64
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*), COALESCE(SUM(cl.duration_sec), 0)
		FROM call_logs cl
		WHERE %s
	`, where), args...).Scan(&calls, &totalDuration)
	if err != nil {
		return nil, fmt.Errorf("summary counts: %w", err)
	}

	summary := &ReportSummary{Calls: calls}
	if totalDuration.Valid {
		summary.TotalDurationSec = totalDuration.Int64
	}
	if calls > 0 {
		summary.AvgDurationSec = float64(summary.TotalDurationSec) / float64(calls)
	}

	dispoRows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(d.code, ''), COALESCE(d.label, ''), COUNT(*)
		FROM call_logs cl
		LEFT JOIN dispositions d ON d.id = cl.disposition_id
		WHERE %s
		GROUP BY d.code, d.label
		ORDER BY COUNT(*) DESC, d.code ASC
	`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("summary dispositions: %w", err)
	}
	defer dispoRows.Close()

	for dispoRows.Next() {
		var dc DispositionCount
		if err := dispoRows.Scan(&dc.Code, &dc.Label, &dc.Count); err != nil {
			return nil, err
		}
		summary.ByDisposition = append(summary.ByDisposition, dc)
	}
	if err := dispoRows.Err(); err != nil {
		return nil, err
	}

	if calls > 0 {
		var contactCount, successCount int
		err = s.DB.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT
				COUNT(*) FILTER (WHERE d.is_contact = TRUE),
				COUNT(*) FILTER (WHERE d.is_success = TRUE)
			FROM call_logs cl
			LEFT JOIN dispositions d ON d.id = cl.disposition_id
			WHERE %s
		`, where), args...).Scan(&contactCount, &successCount)
		if err != nil {
			return nil, fmt.Errorf("summary rates: %w", err)
		}
		contactRate := float64(contactCount) / float64(calls)
		successRate := float64(successCount) / float64(calls)
		summary.ContactRate = &contactRate
		summary.SuccessRate = &successRate
	}

	return summary, nil
}

func (s *ReportStore) ExportCount(ctx context.Context, f ReportFilters) (int, error) {
	where, args := reportWhereClause(f)
	var count int
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*) FROM call_logs cl WHERE %s
	`, where), args...).Scan(&count)
	return count, err
}

func (s *ReportStore) ExportRows(ctx context.Context, f ReportFilters) ([]ReportExportRow, error) {
	count, err := s.ExportCount(ctx, f)
	if err != nil {
		return nil, err
	}
	if count > MaxReportExportRows {
		return nil, ErrExportTooLarge
	}

	where, args := reportWhereClause(f)
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT cl.started_at, cl.ended_at, cl.duration_sec, cl.campaign_id, cl.agent_id,
		       cl.to_number, d.code, cl.lead_id
		FROM call_logs cl
		LEFT JOIN dispositions d ON d.id = cl.disposition_id
		WHERE %s
		ORDER BY cl.started_at ASC
	`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReportExportRow
	for rows.Next() {
		var row ReportExportRow
		var endedAt sql.NullTime
		var durationSec sql.NullInt64
		var dispositionCode sql.NullString
		var leadID uuid.NullUUID
		if err := rows.Scan(
			&row.StartedAt, &endedAt, &durationSec, &row.CampaignID, &row.AgentID,
			&row.ToNumber, &dispositionCode, &leadID,
		); err != nil {
			return nil, err
		}
		if endedAt.Valid {
			t := endedAt.Time
			row.EndedAt = &t
		}
		if durationSec.Valid {
			d := int(durationSec.Int64)
			row.DurationSec = &d
		}
		if dispositionCode.Valid {
			code := dispositionCode.String
			row.DispositionCode = &code
		}
		if leadID.Valid {
			row.LeadID = &leadID.UUID
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func reportWhereClause(f ReportFilters) (string, []any) {
	parts := []string{"1=1"}
	args := make([]any, 0, 4)
	argN := 1

	if f.From != nil {
		parts = append(parts, fmt.Sprintf("cl.started_at >= $%d", argN))
		args = append(args, *f.From)
		argN++
	}
	if f.To != nil {
		parts = append(parts, fmt.Sprintf("cl.started_at < $%d", argN))
		args = append(args, *f.To)
		argN++
	}
	if f.CampaignID != nil {
		parts = append(parts, fmt.Sprintf("cl.campaign_id = $%d", argN))
		args = append(args, *f.CampaignID)
		argN++
	}
	if f.AgentID != nil {
		parts = append(parts, fmt.Sprintf("cl.agent_id = $%d", argN))
		args = append(args, *f.AgentID)
	}

	return strings.Join(parts, " AND "), args
}
