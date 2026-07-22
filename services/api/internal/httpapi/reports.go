package httpapi

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/callvoice/callvoice/services/api/internal/store"
)

const maxReportExportRows = 50_000

type dispositionCountResponse struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type reportSummaryResponse struct {
	Calls            int                        `json:"calls"`
	TotalDurationSec int64                      `json:"total_duration_sec"`
	AvgDurationSec   float64                    `json:"avg_duration_sec"`
	ByDisposition    []dispositionCountResponse `json:"by_disposition"`
	ContactRate      *float64                   `json:"contact_rate,omitempty"`
	SuccessRate      *float64                   `json:"success_rate,omitempty"`
}

func (s *Server) reportStore() *store.ReportStore {
	return &store.ReportStore{DB: s.DB}
}

func (s *Server) handleReportsSummary(w http.ResponseWriter, r *http.Request) {
	filters, errMsg := parseReportFilters(r)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	summary, err := s.reportStore().Summary(r.Context(), filters)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	out := reportSummaryResponse{
		Calls:            summary.Calls,
		TotalDurationSec: summary.TotalDurationSec,
		AvgDurationSec:   summary.AvgDurationSec,
		ByDisposition:    make([]dispositionCountResponse, 0, len(summary.ByDisposition)),
		ContactRate:      summary.ContactRate,
		SuccessRate:      summary.SuccessRate,
	}
	for _, d := range summary.ByDisposition {
		out.ByDisposition = append(out.ByDisposition, dispositionCountResponse{
			Code:  d.Code,
			Label: d.Label,
			Count: d.Count,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleReportsExportCSV(w http.ResponseWriter, r *http.Request) {
	filters, errMsg := parseReportFilters(r)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	count, err := s.reportStore().ExportCount(r.Context(), filters)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	if count > maxReportExportRows {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "export_too_large"})
		return
	}

	rows, err := s.reportStore().ExportRows(r.Context(), filters)
	if errors.Is(err, store.ErrExportTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "export_too_large"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"started_at", "ended_at", "duration_sec", "campaign_id", "agent_id", "to_number", "disposition_code", "lead_id",
	})
	for _, row := range rows {
		record := []string{
			row.StartedAt.UTC().Format(time.RFC3339),
			"",
			"",
			row.CampaignID.String(),
			row.AgentID.String(),
			row.ToNumber,
			"",
			"",
		}
		if row.EndedAt != nil {
			record[1] = row.EndedAt.UTC().Format(time.RFC3339)
		}
		if row.DurationSec != nil {
			record[2] = strconv.Itoa(*row.DurationSec)
		}
		if row.DispositionCode != nil {
			record[6] = *row.DispositionCode
		}
		if row.LeadID != nil {
			record[7] = row.LeadID.String()
		}
		_ = writer.Write(record)
	}
	writer.Flush()
}

func parseReportFilters(r *http.Request) (store.ReportFilters, string) {
	var f store.ReportFilters

	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return f, "invalid_from"
		}
		utc := t.UTC()
		f.From = &utc
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return f, "invalid_to"
		}
		utc := t.UTC()
		f.To = &utc
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("campaign_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return f, "invalid_campaign_id"
		}
		f.CampaignID = &id
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("agent_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return f, "invalid_agent_id"
		}
		f.AgentID = &id
	}
	return f, ""
}
