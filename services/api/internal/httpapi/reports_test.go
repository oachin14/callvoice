package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/services/api/internal/db/migrate"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
	"github.com/callvoice/callvoice/services/api/internal/testutil"
)

func setupReportsServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	conn := testutil.OpenTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, migrate.Up(testutil.DatabaseURL()))
	_, err := conn.Exec(`TRUNCATE audit_logs, sessions, call_logs, leads, lead_lists, dispositions, campaign_agents, campaigns, dids, carriers, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	srv := &httpapi.Server{
		DB:               conn,
		SessionSecret:    []byte("dev-session-secret-change-me-32b!!"),
		CarrierSecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:       24 * time.Hour,
		CookieSecure:     false,
		RequireAdmin2FA:  true,
		Now:              time.Now,
		CarrierPublisher: httpapi.NoopCarrierPublisher{},
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, conn
}

func seedCallLogsFixture(t *testing.T, db *sql.DB) (campaignID, agentID, successDispoID uuid.UUID) {
	t.Helper()

	carrierID := insertCarrier(t, db, fmt.Sprintf("RepCarrier-%s", t.Name()))
	agentEmail := fmt.Sprintf("rep-agent-%s@test.local", t.Name())
	insertUserRole(t, db, agentEmail, "pass", "agent", false, nil)
	require.NoError(t, db.QueryRow(`SELECT id FROM users WHERE email = $1`, strings.ToLower(agentEmail)).Scan(&agentID))

	var campaignUUID uuid.UUID
	require.NoError(t, db.QueryRow(`
		INSERT INTO campaigns (name, carrier_id, status, dial_mode)
		VALUES ($1, $2, 'running', 'manual')
		RETURNING id
	`, "Reports Campaign", carrierID).Scan(&campaignUUID))
	campaignID = campaignUUID

	for _, d := range []struct{ code, label string; contact, success bool }{
		{"NO_ANSWER", "Pas de réponse", false, false},
		{"SUCCESS", "Succès", true, true},
	} {
		_, err := db.Exec(`
			INSERT INTO dispositions (code, label, campaign_id, is_contact, is_success)
			VALUES ($1, $2, $3, $4, $5)
		`, d.code, d.label, campaignID, d.contact, d.success)
		require.NoError(t, err)
	}
	require.NoError(t, db.QueryRow(`SELECT id FROM dispositions WHERE campaign_id = $1 AND code = 'SUCCESS'`, campaignID).Scan(&successDispoID))

	var noAnswerID uuid.UUID
	require.NoError(t, db.QueryRow(`SELECT id FROM dispositions WHERE campaign_id = $1 AND code = 'NO_ANSWER'`, campaignID).Scan(&noAnswerID))

	var listID uuid.UUID
	require.NoError(t, db.QueryRow(`
		INSERT INTO lead_lists (campaign_id, name, row_count) VALUES ($1, 'L', 2) RETURNING id
	`, campaignID).Scan(&listID))

	var lead1, lead2 uuid.UUID
	require.NoError(t, db.QueryRow(`
		INSERT INTO leads (list_id, phone, payload, status) VALUES ($1, '+33611111111', '{}', 'answered') RETURNING id
	`, listID).Scan(&lead1))
	require.NoError(t, db.QueryRow(`
		INSERT INTO leads (list_id, phone, payload, status) VALUES ($1, '+33622222222', '{}', 'no_answer') RETURNING id
	`, listID).Scan(&lead2))

	start1 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	start2 := time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC)
	end1 := start1.Add(2 * time.Minute)
	end2 := start2.Add(1 * time.Minute)
	dur1, dur2 := 120, 60

	_, err := db.Exec(`
		INSERT INTO call_logs (campaign_id, lead_id, agent_id, direction, started_at, ended_at, duration_sec, disposition_id, to_number)
		VALUES
		($1, $2, $3, 'outbound', $4, $5, $6, $7, '+33611111111'),
		($1, $8, $3, 'outbound', $9, $10, $11, $12, '+33622222222')
	`, campaignID, lead1, agentID, start1, end1, dur1, successDispoID, lead2, start2, end2, dur2, noAnswerID)
	require.NoError(t, err)

	return campaignID, agentID, successDispoID
}

func TestReportsSummaryCounts(t *testing.T) {
	ts, db := setupReportsServer(t)
	campaignID, agentID, _ := seedCallLogsFixture(t, db)
	superClient := loginSupervisor(t, ts, db)

	from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	to := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	url := fmt.Sprintf("%s/admin/reports/summary?from=%s&to=%s&campaign_id=%s&agent_id=%s",
		ts.URL, from, to, campaignID, agentID)

	resp, err := superClient.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var summary map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&summary))
	require.Equal(t, float64(2), summary["calls"])
	require.Equal(t, float64(180), summary["total_duration_sec"])
	require.Equal(t, float64(90), summary["avg_duration_sec"])

	byDispo, ok := summary["by_disposition"].([]any)
	require.True(t, ok)
	require.Len(t, byDispo, 2)

	contactRate, ok := summary["contact_rate"].(float64)
	require.True(t, ok)
	require.InDelta(t, 0.5, contactRate, 0.001)

	successRate, ok := summary["success_rate"].(float64)
	require.True(t, ok)
	require.InDelta(t, 0.5, successRate, 0.001)
}

func TestReportsCSVExport(t *testing.T) {
	ts, db := setupReportsServer(t)
	campaignID, agentID, _ := seedCallLogsFixture(t, db)
	superClient := loginSupervisor(t, ts, db)

	from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	to := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	url := fmt.Sprintf("%s/admin/reports/export.csv?from=%s&to=%s&campaign_id=%s&agent_id=%s",
		ts.URL, from, to, campaignID, agentID)

	resp, err := superClient.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/csv", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, []string{
		"started_at", "ended_at", "duration_sec", "campaign_id", "agent_id", "to_number", "disposition_code", "lead_id",
	}, rows[0])
}

func TestReportsExportTooLargeReturns413(t *testing.T) {
	ts, db := setupReportsServer(t)
	campaignID, agentID, successDispoID := seedCallLogsFixture(t, db)

	var listID uuid.UUID
	require.NoError(t, db.QueryRow(`SELECT id FROM lead_lists WHERE campaign_id = $1`, campaignID).Scan(&listID))

	start := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 50001; i++ {
		var leadID uuid.UUID
		phone := fmt.Sprintf("+336%08d", i)
		require.NoError(t, db.QueryRow(`
			INSERT INTO leads (list_id, phone, payload, status) VALUES ($1, $2, '{}', 'disposed') RETURNING id
		`, listID, phone).Scan(&leadID))
		_, err := db.Exec(`
			INSERT INTO call_logs (campaign_id, lead_id, agent_id, direction, started_at, duration_sec, disposition_id, to_number)
			VALUES ($1, $2, $3, 'outbound', $4, 10, $5, $6)
		`, campaignID, leadID, agentID, start.Add(time.Duration(i)*time.Second), successDispoID, phone)
		require.NoError(t, err)
	}

	superClient := loginSupervisor(t, ts, db)
	from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	to := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	url := fmt.Sprintf("%s/admin/reports/export.csv?from=%s&to=%s", ts.URL, from, to)

	resp, err := superClient.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestAgentForbiddenOnReports(t *testing.T) {
	ts, db := setupReportsServer(t)
	_, _, _ = seedCallLogsFixture(t, db)
	agentEmail := fmt.Sprintf("rep-forbidden-%s@test.local", t.Name())
	agentClient := loginAgent(t, ts, db, agentEmail, "pass")

	resp, err := agentClient.Get(ts.URL + "/admin/reports/summary")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
