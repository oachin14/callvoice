package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/services/api/internal/db/migrate"
	"github.com/callvoice/callvoice/services/api/internal/httpapi"
	"github.com/callvoice/callvoice/services/api/internal/testutil"
)

func setupAgentOpsServer(t *testing.T) (*httptest.Server, *sql.DB) {
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

func setupRunningCampaignWithAgents(t *testing.T, ts *httptest.Server, db *sql.DB, agentEmails ...string) (campaignID string, superClient *http.Client, agentClients []*http.Client) {
	t.Helper()

	carrierID := insertCarrier(t, db, fmt.Sprintf("Carrier-%s", t.Name()))
	superClient = loginSupervisor(t, ts, db)
	created := createCampaign(t, superClient, ts, carrierID, "Agent Ops Campaign")
	campaignID = created["id"].(string)

	agentIDs := make([]string, 0, len(agentEmails))
	for _, email := range agentEmails {
		client := loginAgent(t, ts, db, email, "agent-pass")
		var agentID uuid.UUID
		require.NoError(t, db.QueryRow(`SELECT id FROM users WHERE email = $1`, strings.ToLower(email)).Scan(&agentID))
		agentIDs = append(agentIDs, agentID.String())
		agentClients = append(agentClients, client)
	}

	assignBody, _ := json.Marshal(map[string]any{"user_ids": agentIDs})
	assignReq, err := http.NewRequest(http.MethodPut, ts.URL+"/admin/campaigns/"+campaignID+"/agents", bytes.NewReader(assignBody))
	require.NoError(t, err)
	assignReq.Header.Set("Content-Type", "application/json")
	assignResp, err := superClient.Do(assignReq)
	require.NoError(t, err)
	defer assignResp.Body.Close()
	require.Equal(t, http.StatusOK, assignResp.StatusCode)

	patchBody, _ := json.Marshal(map[string]string{"status": "running"})
	patchReq, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/campaigns/"+campaignID, bytes.NewReader(patchBody))
	require.NoError(t, err)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := superClient.Do(patchReq)
	require.NoError(t, err)
	defer patchResp.Body.Close()
	require.Equal(t, http.StatusOK, patchResp.StatusCode)

	return campaignID, superClient, agentClients
}

func importOneLead(t *testing.T, ts *httptest.Server, superClient *http.Client, campaignID string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "leads.csv")
	require.NoError(t, err)
	_, err = io.WriteString(part, "phone,name\n+33612345678,Alice\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/admin/campaigns/"+campaignID+"/lists/import", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := superClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestTwoAgentsCannotClaimSameLead(t *testing.T) {
	ts, db := setupAgentOpsServer(t)
	agent1Email := fmt.Sprintf("agent1-%s@test.local", t.Name())
	agent2Email := fmt.Sprintf("agent2-%s@test.local", t.Name())
	campaignID, superClient, clients := setupRunningCampaignWithAgents(t, ts, db, agent1Email, agent2Email)
	importOneLead(t, ts, superClient, campaignID)

	nextURL := ts.URL + "/agent/leads/next?campaign_id=" + campaignID
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, client := range clients {
		wg.Add(1)
		go func(c *http.Client) {
			defer wg.Done()
			resp, err := c.Get(nextURL)
			if err != nil {
				results <- 0
				return
			}
			defer resp.Body.Close()
			results <- resp.StatusCode
		}(client)
	}
	wg.Wait()
	close(results)

	okCount := 0
	noContentCount := 0
	for code := range results {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusNoContent:
			noContentCount++
		}
	}
	require.Equal(t, 1, okCount, "exactly one agent should claim the lead")
	require.Equal(t, 1, noContentCount, "second agent should get no lead")

	var leadCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM leads WHERE status = 'in_progress'`).Scan(&leadCount))
	require.Equal(t, 1, leadCount)
}

func TestAgentListAndJoinCampaign(t *testing.T) {
	ts, db := setupAgentOpsServer(t)
	agentEmail := fmt.Sprintf("agent-join-%s@test.local", t.Name())
	campaignID, _, clients := setupRunningCampaignWithAgents(t, ts, db, agentEmail)
	agentClient := clients[0]

	listResp, err := agentClient.Get(ts.URL + "/agent/campaigns")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var campaigns []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&campaigns))
	require.Len(t, campaigns, 1)
	require.Equal(t, campaignID, campaigns[0]["id"])
	require.Equal(t, "running", campaigns[0]["status"])

	joinReq, err := http.NewRequest(http.MethodPost, ts.URL+"/agent/campaigns/"+campaignID+"/join", nil)
	require.NoError(t, err)
	joinResp, err := agentClient.Do(joinReq)
	require.NoError(t, err)
	defer joinResp.Body.Close()
	require.Equal(t, http.StatusNoContent, joinResp.StatusCode)
}

func TestAgentJoinForbiddenWhenNotAssigned(t *testing.T) {
	ts, db := setupAgentOpsServer(t)
	campaignID, _, _ := setupRunningCampaignWithAgents(t, ts, db, fmt.Sprintf("assigned-%s@test.local", t.Name()))
	otherEmail := fmt.Sprintf("other-%s@test.local", t.Name())
	otherClient := loginAgent(t, ts, db, otherEmail, "agent-pass")

	joinReq, err := http.NewRequest(http.MethodPost, ts.URL+"/agent/campaigns/"+campaignID+"/join", nil)
	require.NoError(t, err)
	joinResp, err := otherClient.Do(joinReq)
	require.NoError(t, err)
	defer joinResp.Body.Close()
	require.Equal(t, http.StatusForbidden, joinResp.StatusCode)
}

func TestAgentDispositionCreatesCallLog(t *testing.T) {
	ts, db := setupAgentOpsServer(t)
	agentEmail := fmt.Sprintf("agent-dispo-%s@test.local", t.Name())
	campaignID, superClient, clients := setupRunningCampaignWithAgents(t, ts, db, agentEmail)
	importOneLead(t, ts, superClient, campaignID)
	agentClient := clients[0]

	nextResp, err := agentClient.Get(ts.URL + "/agent/leads/next?campaign_id=" + campaignID)
	require.NoError(t, err)
	defer nextResp.Body.Close()
	require.Equal(t, http.StatusOK, nextResp.StatusCode)

	var lead map[string]any
	require.NoError(t, json.NewDecoder(nextResp.Body).Decode(&lead))
	leadID := lead["id"].(string)

	var dispositionID string
	require.NoError(t, db.QueryRow(`
		SELECT id FROM dispositions WHERE campaign_id = $1 AND code = 'SUCCESS'
	`, campaignID).Scan(&dispositionID))

	startedAt := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	endedAt := time.Now().UTC().Format(time.RFC3339)
	dispoBody, _ := json.Marshal(map[string]any{
		"campaign_id":    campaignID,
		"lead_id":        leadID,
		"disposition_id": dispositionID,
		"to_number":      "+33612345678",
		"started_at":     startedAt,
		"ended_at":       endedAt,
		"duration_sec":   120,
	})
	dispoResp, err := agentClient.Post(ts.URL+"/agent/dispositions", "application/json", bytes.NewReader(dispoBody))
	require.NoError(t, err)
	defer dispoResp.Body.Close()
	require.Equal(t, http.StatusCreated, dispoResp.StatusCode)

	var callLogCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM call_logs WHERE lead_id = $1`, leadID).Scan(&callLogCount))
	require.Equal(t, 1, callLogCount)

	var leadStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM leads WHERE id = $1`, leadID).Scan(&leadStatus))
	require.Equal(t, "answered", leadStatus)
}

func TestSupervisorForbiddenOnAgentRoutes(t *testing.T) {
	ts, db := setupAgentOpsServer(t)
	superClient := loginSupervisor(t, ts, db)

	resp, err := superClient.Get(ts.URL + "/agent/campaigns")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
