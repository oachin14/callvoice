package live_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/services/edge/internal/live"
)

func TestBuildSnapshotFromRedisFixtures(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	agentA := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-111111111111")
	agentB := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-222222222222")
	agentC := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-333333333333")
	campaignID := "cccccccc-dddd-eeee-ffff-000000000001"
	callUUID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	startedAt := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)

	mr.Set("agent:"+agentA.String(), "available")
	mr.Set("agent:"+agentB.String(), `{"state":"paused","campaign_id":"`+campaignID+`"}`)
	mr.Set("agent:"+agentC.String(), "on_call")

	meta, _ := json.Marshal(map[string]string{
		"agent_id":    agentC.String(),
		"to":          "+33123456789",
		"campaign_id": campaignID,
		"started_at":  startedAt,
	})
	mr.Set("call:meta:"+callUUID, string(meta))

	wb, err := live.BuildSnapshot(ctx, rdb)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if wb.Counts.Available != 1 || wb.Counts.Paused != 1 || wb.Counts.OnCall != 1 || wb.Counts.Calls != 1 {
		t.Fatalf("counts = %+v, want available=1 paused=1 on_call=1 calls=1", wb.Counts)
	}
	if len(wb.Agents) != 3 {
		t.Fatalf("len(agents) = %d, want 3", len(wb.Agents))
	}
	if len(wb.Calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(wb.Calls))
	}

	var paused *live.WallboardAgent
	for i := range wb.Agents {
		if wb.Agents[i].UserID == agentB.String() {
			paused = &wb.Agents[i]
			break
		}
	}
	if paused == nil || paused.CampaignID == nil || *paused.CampaignID != campaignID {
		t.Fatalf("paused agent campaign_id = %v, want %s", paused, campaignID)
	}

	call := wb.Calls[0]
	if call.UUID != callUUID || call.AgentID != agentC.String() || call.To != "+33123456789" {
		t.Fatalf("call = %+v", call)
	}
	if call.CampaignID == nil || *call.CampaignID != campaignID {
		t.Fatalf("call campaign_id = %v, want %s", call.CampaignID, campaignID)
	}
	if call.StartedAt != startedAt {
		t.Fatalf("started_at = %q, want %q", call.StartedAt, startedAt)
	}
}
