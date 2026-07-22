# CallVoice Jalon C — Campaigns Ops & Supervision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship admin users, manual campaigns with CSV leads and dispositions, a live wallboard, and filtered CSV reports on top of jalon B telephony.

**Architecture:** Extend `callvoice-api` (Postgres CRUD, imports, reports, agent lead/disposition) and `callvoice-edge` (Redis-backed wallboard WS). Next.js gains sidebar nav + pages for Users, Campaigns, Live, Reports, and agent campaign/qualification UI. Reuse existing auth cookies, carriers, and `POST /calls/outbound`.

**Tech Stack:** Same as jalon B — Go API/edge, PostgreSQL 16, Redis 7, Next.js 15, encoding/csv, existing WS hub patterns.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-22-callvoice-campaigns-ops-design.md`
- Prerequisite: jalon B on `main` (auth 2FA, BYOC, edge WebRTC, CPS, call events)
- Dial mode for C: **manual only** (`dial_mode = manual`)
- Reports: summary + CSV export with `from`/`to`/`campaign_id`/`agent_id` filters — **no PDF**
- Live: counters + agent list + active calls — **no barge/whisper**
- French UI copy; distinctive typography (existing CallVoice look); no purple-default AI theme
- YAGNI: no predictive, reinjection, AMD, scripts, multi-env control plane
- TDD for API/store logic; UI verified with build + smoke where possible
- Follow existing patterns in `services/api/internal/httpapi` (`RequireSession`, `RequireAdmin`, chi routes)

---

## File structure (lock-in)

```text
services/api/migrations/0002_campaigns_ops.up.sql
services/api/migrations/0002_campaigns_ops.down.sql
internal/models/          # extend Campaign, Lead, Disposition, CallLog, User fields
services/api/internal/store/{users,campaigns,leads,dispositions,calllogs,reports}.go
services/api/internal/csvimport/parse.go
services/api/internal/httpapi/{users,campaigns,agent_ops,reports}.go
services/edge/internal/live/wallboard.go   # snapshot from Redis
services/edge/internal/httpapi/ws_live.go  # /ws/live
apps/web/app/(admin)/layout.tsx            # sidebar shell
apps/web/app/(admin)/users/page.tsx
apps/web/app/(admin)/campaigns/...
apps/web/app/(admin)/live/page.tsx
apps/web/app/(admin)/reports/page.tsx
apps/web/app/agent/page.tsx                # campaign join + lead + disposition
docs/superpowers/plans/notes/jalon-c-acceptance.md
```

---

### Task 1: Migration 0002 + models

**Files:**
- Create: `services/api/migrations/0002_campaigns_ops.up.sql`
- Create: `services/api/migrations/0002_campaigns_ops.down.sql`
- Modify: `internal/models/models.go`
- Test: `services/api/internal/db/db_test.go` (extend migrate+smoke insert)

**Interfaces:**
- Produces: tables `campaigns`, `campaign_agents`, `lead_lists`, `leads`, `dispositions`, `call_logs`; user columns `display_name TEXT`, `disabled_at TIMESTAMPTZ`
- Campaign status enum/check: `draft|running|paused|stopped`
- Lead status check: `new|in_progress|no_answer|busy|callback|disposed|answered`

- [ ] **Step 1: Write failing test** that migrates Up through 0002 and inserts one campaign row

- [ ] **Step 2: Run test — expect FAIL** until migration exists

```bash
cd /Users/provectio/Documents/PersoDev/callvoice && go test -p 1 ./services/api/internal/db/... -count=1 -v
```

- [ ] **Step 3: Write SQL + models** matching spec §4 exactly (FK to `carriers`, `users`)

- [ ] **Step 4: Run tests — PASS**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(api): add campaigns ops schema migration 0002"
```

---

### Task 2: Users admin API

**Files:**
- Create: `services/api/internal/store/users.go`
- Create: `services/api/internal/httpapi/users.go`
- Test: `services/api/internal/httpapi/users_test.go`
- Modify: `services/api/internal/httpapi/auth.go` (register routes; add `RequireAdminOrSupervisor` later in Task 3)

**Interfaces:**
- `GET /admin/users` → `[]UserResponse` (no password hash)
- `POST /admin/users` body `{email,password,role,display_name?}` → 201
- `PATCH /admin/users/{id}` `{display_name?,role?,disabled?}`
- `POST /admin/users/{id}/reset-password` `{password}`
- Reject creating second owner freely; roles ∈ `admin|supervisor|agent`
- Disabled users cannot login (hook in `handleLogin`)

- [ ] **Step 1: Failing tests** — admin creates agent; agent cannot `POST /admin/users`; disabled user login 403

- [ ] **Step 2: Implement store + handlers + login disabled check**

- [ ] **Step 3: Tests PASS**

- [ ] **Step 4: Commit** `feat(api): admin users CRUD and disable login`

---

### Task 3: Campaigns CRUD + agent assignment + role middleware

**Files:**
- Create: `services/api/internal/store/campaigns.go`
- Create: `services/api/internal/httpapi/campaigns.go`
- Test: `services/api/internal/httpapi/campaigns_test.go`
- Modify: routes registration

**Interfaces:**
- `RequireSupervisor` = role ∈ `admin|supervisor`
- `GET/POST /admin/campaigns`
- `PATCH /admin/campaigns/{id}` (name, carrier_id, status transitions)
- `PUT /admin/campaigns/{id}/agents` body `{user_ids:[]uuid}` — only role=agent
- Status: allow `draft→running`, `running↔paused`, `*→stopped`, reject invalid jumps with 400
- On create: `dial_mode=manual`, seed default dispositions for campaign (Task 4 can own seed helper)

- [ ] **Step 1: Failing tests** CRUD + assign agents + supervisor access + agent 403

- [ ] **Step 2: Implement**

- [ ] **Step 3: PASS + commit** `feat(api): campaigns CRUD and agent assignment`

---

### Task 4: Dispositions + CSV import

**Files:**
- Create: `services/api/internal/csvimport/parse.go`
- Create: `services/api/internal/csvimport/parse_test.go`
- Create: `services/api/internal/store/leads.go`
- Create: `services/api/internal/store/dispositions.go`
- Modify: `services/api/internal/httpapi/campaigns.go` (import + dispositions routes)

**Interfaces:**
- `csvimport.Parse(reader) (rows []LeadRow, errs []RowError, err error)`
  - Required header `phone` (or `téléphone` / `mobile` aliases)
  - Normalize to E.164 (`+` + digits); invalid → RowError, continue
  - Extra columns → `map[string]string` payload
- `POST /admin/campaigns/{id}/lists/import` multipart `file` + optional `name`
  - Creates `lead_lists` + bulk insert `leads` status=`new`
  - Response `{list_id, imported, rejected, errors:[{line,reason}] }` (cap errors at 100)
- `GET/POST /admin/campaigns/{id}/dispositions`
- Default dispositions on campaign create: `NO_ANSWER`, `BUSY`, `CALLBACK`, `SUCCESS`, `DNC` (labels FR)

- [ ] **Step 1: Unit tests** Parse valid/invalid phones + extras

- [ ] **Step 2: Implement parse**

- [ ] **Step 3: HTTP import test** with CSV bytes

- [ ] **Step 4: Commit** `feat(api): CSV lead import and dispositions`

---

### Task 5: Agent campaign join, next lead, disposition + call_logs

**Files:**
- Create: `services/api/internal/store/calllogs.go`
- Create: `services/api/internal/httpapi/agent_ops.go`
- Test: `services/api/internal/httpapi/agent_ops_test.go`
- Modify: edge outbound path optionally accepts `campaign_id`/`lead_id` headers or JSON — **prefer API records call_log on disposition**; for live duration, edge already has call meta — document bridge:
  - Agent UI calls edge `POST /calls/outbound` with `{to, callerId?, campaign_id?, lead_id?}`
  - Modify edge `calls.go` to store optional campaign/lead on Redis call meta (small change in this task or Task 7)

**Interfaces:**
- `GET /agent/campaigns` → running campaigns where user assigned
- `POST /agent/campaigns/{id}/join` → 204 (verify assigned + running); set Redis via edge later OR API sets nothing and agent UI calls edge `/agent/state` with campaign — **lock:** join stores `campaign_id` in Redis presence through new edge endpoint `POST /agent/campaign` body `{campaign_id}` (add in Task 7); for Task 5 API-only join records session in Postgres optional table `agent_campaign_sessions` **OR** simply validate and return OK and let UI pass campaign_id on subsequent calls.
  - **Lock for implementer:** API `join` validates only; UI keeps `campaignId` in React state; `next`/`disposition` require that campaign_id query/body.
- `GET /agent/leads/next?campaign_id=` → claim one `new` lead (`UPDATE … WHERE status='new' FOR UPDATE SKIP LOCKED` → `in_progress`, `assigned_agent_id`)
- `POST /agent/dispositions` body `{campaign_id, lead_id, disposition_id, call_uuid?, to_number, started_at?, ended_at?, duration_sec?}` → update lead status from disposition flags + insert `call_logs`

- [ ] **Step 1: Test** two agents cannot claim same lead (SKIP LOCKED)

- [ ] **Step 2: Implement claim + disposition**

- [ ] **Step 3: PASS + commit** `feat(api): agent lead claim and disposition call logs`

---

### Task 6: Reports summary + CSV export

**Files:**
- Create: `services/api/internal/store/reports.go`
- Create: `services/api/internal/httpapi/reports.go`
- Test: `services/api/internal/httpapi/reports_test.go`

**Interfaces:**
- `GET /admin/reports/summary?from=RFC3339&to=&campaign_id=&agent_id=`
  - JSON `{calls, total_duration_sec, avg_duration_sec, by_disposition:[{code,label,count}], contact_rate?, success_rate?}`
- `GET /admin/reports/export.csv` same filters
  - Columns: started_at, ended_at, duration_sec, campaign_id, agent_id, to_number, disposition_code, lead_id
  - Max 50_000 rows → 413 if exceeded
- Supervisor + admin only

- [ ] **Step 1: Seed call_logs in test + assert summary counts**

- [ ] **Step 2: Implement**

- [ ] **Step 3: Commit** `feat(api): reports summary and CSV export`

---

### Task 7: Edge wallboard live WS

**Files:**
- Create: `services/edge/internal/live/wallboard.go`
- Create: `services/edge/internal/live/wallboard_test.go`
- Modify: `services/edge/internal/httpapi/ws.go` or create `ws_live.go`
- Modify: `services/edge/internal/agent/presence.go` — store optional `campaign_id` JSON in Redis value
- Modify: `services/edge/internal/dialer/manual.go` / `calls.go` — include `campaign_id`, `lead_id`, `to` in call meta Redis
- Modify: `services/edge/cmd/edge/main.go` mount `GET /ws/live`

**Interfaces:**
- `GET /ws/live` — RequireSession + role admin|supervisor (edge reads user from DB like agent auth)
- On connect: send `{type:"live.snapshot", payload: Wallboard}`
- Periodic refresh 2s OR push on Redis keyspace/presence updates (prefer 2s ticker for jalon C YAGNI)
- `Wallboard`: `{counts:{available,paused,on_call,calls}, agents:[{user_id,state,campaign_id}], calls:[{uuid,agent_id,to,campaign_id,started_at}]}`

- [ ] **Step 1: Unit test** BuildSnapshot from fake Redis fixtures

- [ ] **Step 2: Implement WS**

- [ ] **Step 3: Commit** `feat(edge): live wallboard websocket for supervisors`

---

### Task 8: Web admin shell + Users UI

**Files:**
- Create: `apps/web/app/(admin)/layout.tsx` + `admin.module.css` (sidebar)
- Create: `apps/web/app/(admin)/users/page.tsx`
- Modify: `apps/web/app/(admin)/carriers/page.tsx` — use shared layout (move under same group if not already)
- Modify: `apps/web/lib/api.ts` — user helpers

**Interfaces:**
- Sidebar links FR: Carriers, Utilisateurs, Campagnes, Live, Rapports, Console agent
- Hide Utilisateurs/Carriers for supervisor; hide all admin for agent (redirect `/agent`)

- [ ] **Step 1: Layout + users page CRUD forms**

- [ ] **Step 2: `npm run build` PASS**

- [ ] **Step 3: Commit** `feat(web): admin shell and users management UI`

---

### Task 9: Web Campaigns UI (CRUD, agents, CSV, dispositions)

**Files:**
- Create: `apps/web/app/(admin)/campaigns/page.tsx`
- Create: `apps/web/app/(admin)/campaigns/[id]/page.tsx`
- Create: CSS module

- [ ] **Step 1: List + create campaign**

- [ ] **Step 2: Detail — status buttons, agent multi-select, file upload CSV, dispositions table**

- [ ] **Step 3: Build PASS + commit** `feat(web): campaigns admin UI with CSV import`

---

### Task 10: Web Live + Reports pages

**Files:**
- Create: `apps/web/app/(admin)/live/page.tsx`
- Create: `apps/web/app/(admin)/reports/page.tsx`
- Create: `apps/web/lib/live.ts` (WS client to `NEXT_PUBLIC_EDGE_URL/ws/live`)

- [ ] **Step 1: Live wallboard** bind snapshot + updates

- [ ] **Step 2: Reports** filters + table + download CSV (`credentials:include`)

- [ ] **Step 3: Build PASS + commit** `feat(web): live wallboard and reports UI`

---

### Task 11: Agent console — campaign, lead, disposition

**Files:**
- Modify: `apps/web/app/agent/page.tsx`
- Modify: `apps/web/lib/edge.ts` / softphone outbound payload to pass `campaign_id`/`lead_id` if edge supports

- [ ] **Step 1: Campaign selector** from `GET /agent/campaigns` + join

- [ ] **Step 2: Next lead panel** + Call button uses lead.phone

- [ ] **Step 3: Disposition buttons** after hangup / during wrap-up → `POST /agent/dispositions`

- [ ] **Step 4: Build PASS + commit** `feat(web): agent campaign lead and qualification`

---

### Task 12: Seed helpers + acceptance notes

**Files:**
- Modify: `services/api/cmd/seed/main.go` optional flags OR create `scripts/seed_jalon_c.sh`
- Create: `docs/superpowers/plans/notes/jalon-c-acceptance.md`
- Modify: `README.md` — document new routes briefly

**Acceptance checklist:**
- [ ] Admin creates agent user
- [ ] Admin creates campaign, assigns agent, imports CSV
- [ ] Campaign running; agent joins, gets lead, dials, dispositions
- [ ] Supervisor live wallboard shows agent/call
- [ ] Reports CSV filtered non-empty

- [ ] **Step 1: Script** creates demo agent + campaign + 3 leads when `SEED_DEMO=1`

- [ ] **Step 2: Run lab smoke** (or document deferred if no Docker)

- [ ] **Step 3: Commit** `test: jalon C acceptance notes and demo seed`

---

## Self-review (plan vs spec)

| Spec item | Task |
|-----------|------|
| Users CRUD + disable | 2, 8 |
| Campaigns + agents + CSV + dispositions | 3, 4, 9 |
| Agent join / next / disposition / call_logs | 5, 11 |
| Live wallboard WS | 7, 10 |
| Reports + CSV filters | 6, 10 |
| Nav / roles | 3, 8 |
| No predictive/PDF/barge | respected |

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-22-callvoice-campaigns-ops.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task  
2. **Inline Execution** — this session with checkpoints  

Which approach?
