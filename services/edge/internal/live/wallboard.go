package live

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	TypeLiveSnapshot = "live.snapshot"

	agentKeyPattern   = "agent:*"
	callMetaKeyPrefix = "call:meta:"
)

// WallboardCounts aggregates live agent and call counters.
type WallboardCounts struct {
	Available int `json:"available"`
	Paused    int `json:"paused"`
	OnCall    int `json:"on_call"`
	Calls     int `json:"calls"`
}

// WallboardAgent is one connected agent on the wallboard.
type WallboardAgent struct {
	UserID     string  `json:"user_id"`
	State      string  `json:"state"`
	CampaignID *string `json:"campaign_id,omitempty"`
}

// WallboardCall is one active tracked outbound call.
type WallboardCall struct {
	UUID       string  `json:"uuid"`
	AgentID    string  `json:"agent_id"`
	To         string  `json:"to"`
	CampaignID *string `json:"campaign_id,omitempty"`
	StartedAt  string  `json:"started_at"`
}

// Wallboard is the supervisor live dashboard snapshot.
type Wallboard struct {
	Counts WallboardCounts  `json:"counts"`
	Agents []WallboardAgent `json:"agents"`
	Calls  []WallboardCall  `json:"calls"`
}

type agentPresence struct {
	State      string `json:"state"`
	CampaignID string `json:"campaign_id,omitempty"`
}

type callMeta struct {
	AgentID    string `json:"agent_id"`
	To         string `json:"to"`
	CampaignID string `json:"campaign_id,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
}

// BuildSnapshot scans Redis agent presence and call meta keys.
func BuildSnapshot(ctx context.Context, rdb *redis.Client) (*Wallboard, error) {
	if rdb == nil {
		return &Wallboard{Agents: []WallboardAgent{}, Calls: []WallboardCall{}}, nil
	}

	wb := &Wallboard{
		Agents: []WallboardAgent{},
		Calls:  []WallboardCall{},
	}

	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, agentKeyPattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			userID := strings.TrimPrefix(key, "agent:")
			if userID == key || userID == "" {
				continue
			}
			raw, err := rdb.Get(ctx, key).Result()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return nil, err
			}
			state, campaignID := parseAgentPresence(raw)
			if state == "" {
				continue
			}
			agent := WallboardAgent{UserID: userID, State: state}
			if campaignID != "" {
				agent.CampaignID = &campaignID
			}
			wb.Agents = append(wb.Agents, agent)
			switch state {
			case "available":
				wb.Counts.Available++
			case "paused":
				wb.Counts.Paused++
			case "on_call":
				wb.Counts.OnCall++
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	cursor = 0
	for {
		keys, next, err := rdb.Scan(ctx, cursor, callMetaKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			uuid := strings.TrimPrefix(key, callMetaKeyPrefix)
			if uuid == key || uuid == "" {
				continue
			}
			raw, err := rdb.Get(ctx, key).Bytes()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return nil, err
			}
			var meta callMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				continue
			}
			call := WallboardCall{
				UUID:      uuid,
				AgentID:   meta.AgentID,
				To:        meta.To,
				StartedAt: meta.StartedAt,
			}
			if meta.CampaignID != "" {
				call.CampaignID = &meta.CampaignID
			}
			wb.Calls = append(wb.Calls, call)
			wb.Counts.Calls++
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	return wb, nil
}

func parseAgentPresence(raw string) (state, campaignID string) {
	var p agentPresence
	if err := json.Unmarshal([]byte(raw), &p); err == nil && p.State != "" {
		return p.State, p.CampaignID
	}
	switch raw {
	case "available", "paused", "on_call":
		return raw, ""
	default:
		return "", ""
	}
}
