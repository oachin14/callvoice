# Jalon C — acceptance checklist

Maps jalon C spec (*admin users, manual campaigns + CSV, agent lead/disposition, live wallboard, filtered CSV reports*) to verification status on **2026-07-22**.

**Lab context:** Task 12 run without live Docker stack. API/edge unit tests + web build recorded; browser/WebRTC E2E deferred.

## Spec success criteria

| Item | Status | Evidence |
|------|--------|----------|
| Admin creates agent user | **API + UI verified** | `TestAdminCreatesAgent`, `TestPatchUserDisableAndResetPassword`; `/users` page builds |
| Admin creates campaign, assigns agent, imports CSV | **API + UI verified** | `TestAdminCanManageCampaigns`, `TestAssignCampaignAgents`, `TestImportLeadListCSV`; `/campaigns` pages build |
| Campaign running; agent joins, gets lead, dials, dispositions | **API verified; dial deferred** | `TestAgentListAndJoinCampaign`, `TestTwoAgentsCannotClaimSameLead`, `TestAgentDispositionCreatesCallLog`; agent console UI builds; real WebRTC dial needs FS + carrier |
| Supervisor live wallboard shows agent/call | **Unit verified; WS E2E deferred** | `TestBuildSnapshotFromRedisFixtures`, hub broadcast tests; `/live` page builds; browser WS not exercised this session |
| Reports CSV filtered non-empty | **API verified** | `TestReportsSummaryCounts`, `TestReportsCSVExport`; `/reports` page builds |

## Commands run (Task 12)

```bash
cd services/api && go test ./... -count=1
cd services/edge && go test ./... -count=1
cd apps/web && npm run build
```

## Live lab procedure (when Docker available)

```bash
export SEED_ADMIN_PASSWORD=...
./scripts/seed_jalon_c.sh          # admin + agent@callvoice.local + Demo Campaign + 3 leads
docker compose up -d --build
# Admin: /users /campaigns — verify demo data
# Agent: /agent — join Demo Campaign, next lead, disposition (SMOKE_SKIP_SIP=1 for no media)
# Supervisor: /live wallboard, /reports CSV export with filters
SMOKE_SKIP_SIP=1 ./scripts/smoke_e2e.sh
```

## Demo seed

`SEED_DEMO=1` (via `./scripts/seed_jalon_c.sh`) creates:

- Agent `agent@callvoice.local` (password: `SEED_DEMO_AGENT_PASSWORD` or `SEED_ADMIN_PASSWORD`)
- Running campaign **Demo Campaign** with default dispositions
- 3 lab leads (`+33111111101`–`03`)

Re-runs are idempotent (refreshes passwords, skips lead import if leads exist).

## Deferred / out of scope (jalon C)

- Predictive / preview dialer, barge/whisper, PDF reports (spec §2)
- Full browser E2E: wallboard WS stream, WebRTC outbound with campaign/lead metadata

## Honest E2E gap

**Not verified end-to-end in Task 12:** Docker lab smoke with `./scripts/seed_jalon_c.sh`, supervisor wallboard in browser, agent WebRTC dial tied to campaign/lead, and filtered CSV download in UI. Covered at API/unit level and via `npm run build` for all new routes.
