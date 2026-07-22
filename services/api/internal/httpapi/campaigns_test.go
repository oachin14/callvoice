package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
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

func setupCampaignsServer(t *testing.T) (*httptest.Server, *sql.DB) {
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

func insertCarrier(t *testing.T, db *sql.DB, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.QueryRow(`
		INSERT INTO carriers (name, host, port, transport, codecs, caller_ids, max_cps, max_channels, enabled, priority)
		VALUES ($1, 'sip.test.local', 5060, 'udp', '{PCMU}', '{}', 10, 50, true, 100)
		RETURNING id
	`, name).Scan(&id)
	require.NoError(t, err)
	return id
}

func loginSupervisor(t *testing.T, ts *httptest.Server, db *sql.DB) *http.Client {
	t.Helper()
	email := fmt.Sprintf("supervisor-%s@test.local", t.Name())
	insertUserRole(t, db, email, "super pass", "supervisor", false, nil)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": email, "password": "super pass"})
	resp, err := client.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return client
}

func createCampaign(t *testing.T, client *http.Client, ts *httptest.Server, carrierID uuid.UUID, name string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "carrier_id": carrierID.String()})
	resp, err := client.Post(ts.URL+"/admin/campaigns", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	return created
}

func TestSupervisorCanCreateAndListCampaigns(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier A")
	client := loginSupervisor(t, ts, db)

	created := createCampaign(t, client, ts, carrierID, "Outbound Q1")
	require.Equal(t, "Outbound Q1", created["name"])
	require.Equal(t, carrierID.String(), created["carrier_id"])
	require.Equal(t, "draft", created["status"])
	require.Equal(t, "manual", created["dial_mode"])

	listResp, err := client.Get(ts.URL + "/admin/campaigns")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 1)
}

func TestAdminCanManageCampaigns(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier B")
	client := loginAdmin(t, ts, db)

	created := createCampaign(t, client, ts, carrierID, "Admin Campaign")
	id := created["id"].(string)

	patchBody, _ := json.Marshal(map[string]any{"status": "running"})
	patchReq, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/campaigns/"+id, bytes.NewReader(patchBody))
	require.NoError(t, err)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := client.Do(patchReq)
	require.NoError(t, err)
	defer patchResp.Body.Close()
	require.Equal(t, http.StatusOK, patchResp.StatusCode)

	var patched map[string]any
	require.NoError(t, json.NewDecoder(patchResp.Body).Decode(&patched))
	require.Equal(t, "running", patched["status"])
}

func TestAgentForbiddenOnCampaigns(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	_ = insertCarrier(t, db, "Carrier C")
	agentEmail := fmt.Sprintf("agent-camp-%s@test.local", t.Name())
	client := loginAgent(t, ts, db, agentEmail, "agent-pass")

	listResp, err := client.Get(ts.URL + "/admin/campaigns")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusForbidden, listResp.StatusCode)

	createBody, _ := json.Marshal(map[string]string{"name": "Nope", "carrier_id": uuid.New().String()})
	createResp, err := client.Post(ts.URL+"/admin/campaigns", "application/json", bytes.NewReader(createBody))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusForbidden, createResp.StatusCode)
}

func TestCampaignStatusTransitions(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier D")
	client := loginSupervisor(t, ts, db)
	created := createCampaign(t, client, ts, carrierID, "Status Test")
	id := created["id"].(string)

	assertPatchStatus := func(status string, wantCode int) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"status": status})
		req, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/campaigns/"+id, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, wantCode, resp.StatusCode, "status=%s", status)
	}

	assertPatchStatus("running", http.StatusOK)
	assertPatchStatus("paused", http.StatusOK)
	assertPatchStatus("running", http.StatusOK)
	assertPatchStatus("stopped", http.StatusOK)
	assertPatchStatus("running", http.StatusBadRequest)
	assertPatchStatus("draft", http.StatusBadRequest)
}

func TestInvalidStatusTransitionFromDraft(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier E")
	client := loginSupervisor(t, ts, db)
	created := createCampaign(t, client, ts, carrierID, "Draft Only")
	id := created["id"].(string)

	body, _ := json.Marshal(map[string]string{"status": "paused"})
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/admin/campaigns/"+id, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAssignCampaignAgents(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier F")
	client := loginSupervisor(t, ts, db)

	agent1 := fmt.Sprintf("agent1-%s@test.local", t.Name())
	agent2 := fmt.Sprintf("agent2-%s@test.local", t.Name())
	superEmail := fmt.Sprintf("super2-%s@test.local", t.Name())
	insertUserRole(t, db, agent1, "pass", "agent", false, nil)
	insertUserRole(t, db, agent2, "pass", "agent", false, nil)
	insertUserRole(t, db, superEmail, "pass", "supervisor", false, nil)

	var agent1ID, agent2ID, superID uuid.UUID
	require.NoError(t, db.QueryRow(`SELECT id FROM users WHERE email = $1`, strings.ToLower(agent1)).Scan(&agent1ID))
	require.NoError(t, db.QueryRow(`SELECT id FROM users WHERE email = $1`, strings.ToLower(agent2)).Scan(&agent2ID))
	require.NoError(t, db.QueryRow(`SELECT id FROM users WHERE email = $1`, strings.ToLower(superEmail)).Scan(&superID))

	created := createCampaign(t, client, ts, carrierID, "Agents Test")
	campaignID := created["id"].(string)

	assignBody, _ := json.Marshal(map[string]any{"user_ids": []string{agent1ID.String(), agent2ID.String()}})
	assignReq, err := http.NewRequest(http.MethodPut, ts.URL+"/admin/campaigns/"+campaignID+"/agents", bytes.NewReader(assignBody))
	require.NoError(t, err)
	assignReq.Header.Set("Content-Type", "application/json")
	assignResp, err := client.Do(assignReq)
	require.NoError(t, err)
	defer assignResp.Body.Close()
	require.Equal(t, http.StatusOK, assignResp.StatusCode)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM campaign_agents WHERE campaign_id = $1`, campaignID).Scan(&count))
	require.Equal(t, 2, count)

	badBody, _ := json.Marshal(map[string]any{"user_ids": []string{agent1ID.String(), superID.String()}})
	badReq, err := http.NewRequest(http.MethodPut, ts.URL+"/admin/campaigns/"+campaignID+"/agents", bytes.NewReader(badBody))
	require.NoError(t, err)
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := client.Do(badReq)
	require.NoError(t, err)
	defer badResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, badResp.StatusCode)
}

func TestImportLeadListCSV(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier H")
	client := loginSupervisor(t, ts, db)
	created := createCampaign(t, client, ts, carrierID, "Import Test")
	campaignID := created["id"].(string)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "leads.csv")
	require.NoError(t, err)
	_, err = io.WriteString(part, "phone,name\n+33612345678,Alice\nbad,\n0611223344,Bob\n")
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("name", "Q1 List"))
	require.NoError(t, writer.Close())

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/admin/campaigns/"+campaignID+"/lists/import", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, float64(2), result["imported"])
	require.Equal(t, float64(1), result["rejected"])
	require.NotEmpty(t, result["list_id"])

	var leadCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM leads WHERE list_id = $1`, result["list_id"]).Scan(&leadCount))
	require.Equal(t, 2, leadCount)
}

func TestListAndCreateDispositions(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier I")
	client := loginSupervisor(t, ts, db)
	created := createCampaign(t, client, ts, carrierID, "Dispo API")
	campaignID := created["id"].(string)

	listResp, err := client.Get(ts.URL + "/admin/campaigns/" + campaignID + "/dispositions")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 5)

	createBody, _ := json.Marshal(map[string]any{
		"code":       "CUSTOM",
		"label":      "Personnalisé",
		"is_contact": false,
		"is_success": false,
	})
	createResp, err := client.Post(
		ts.URL+"/admin/campaigns/"+campaignID+"/dispositions",
		"application/json",
		bytes.NewReader(createBody),
	)
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM dispositions WHERE campaign_id = $1`, campaignID).Scan(&count))
	require.Equal(t, 6, count)
}

func TestCreateCampaignSeedsDefaultDispositions(t *testing.T) {
	ts, db := setupCampaignsServer(t)
	carrierID := insertCarrier(t, db, "Carrier G")
	client := loginSupervisor(t, ts, db)
	created := createCampaign(t, client, ts, carrierID, "Dispo Seed")
	campaignID := created["id"].(string)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM dispositions WHERE campaign_id = $1`, campaignID).Scan(&count))
	require.Equal(t, 5, count)

	var code string
	require.NoError(t, db.QueryRow(`SELECT code FROM dispositions WHERE campaign_id = $1 AND code = 'SUCCESS'`, campaignID).Scan(&code))
	require.Equal(t, "SUCCESS", code)
}
