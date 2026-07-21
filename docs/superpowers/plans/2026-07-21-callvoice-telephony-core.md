# CallVoice Jalon B — Telephony Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a manually deployable dedicated client environment where an admin configures BYOC carriers and an agent logs in with 2FA, places a WebRTC outbound call, and receives a basic inbound call, with CPS caps enforced and FreeSWITCH not publicly exposed.

**Architecture:** Split each client env into `callvoice-app` (Next.js UI + Go API for auth/config) and `callvoice-edge` (Go FreeSWITCH ESL controller: BYOC apply, WebRTC creds, presence, CPS, originate/inbound). PostgreSQL is source of truth; Redis holds live agent/call/CPS state. FreeSWITCH handles SIP/media only behind allowlists.

**Tech Stack:** Next.js 15 (App Router) + TypeScript, Go 1.22+, PostgreSQL 16, Redis 7, FreeSWITCH 1.10+, SIP.js (browser WebRTC over WSS), golang-migrate, chi/v5 router, argon2id, TOTP (`pquerna/otp`), AES-GCM for carrier secrets.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-21-callvoice-telephony-core-design.md`
- One dedicated stack per client (no shared multi-tenant FreeSWITCH)
- BYOC only (client brings 1..n SIP trunks)
- Outbound + inbound from day one; dial modes beyond manual are out of scope for this plan
- WebRTC-only agent phone (no Zoiper/eyeBeam)
- Auth: HTTP-only session cookies + Argon2id + TOTP 2FA (mandatory for Admin)
- FreeSWITCH must not be reachable from the public Internet
- CPS limiter is mandatory before any originate
- Manual deploy docs required; auto-provisioning is out of scope
- YAGNI: no predictive dialer, lists, reinjection, rich reports, or control plane in this plan
- Language: French UI copy for login/agent console; code/comments in English

---

## File structure (lock-in)

```text
callvoice/
  apps/web/                          # Next.js UI (login, admin carriers, agent console)
  services/api/                      # Go HTTP API (auth, users, carriers CRUD)
  services/edge/                     # Go edge (ESL, CPS, WebRTC, calls, WS events)
  internal/                          # shared Go packages used by api + edge
    authkit/                         # password hash, session tokens, TOTP helpers
    cryptokit/                       # AES-GCM envelope for carrier secrets
    cps/                             # sliding-window CPS limiter (Redis)
    models/                          # shared domain types
  deploy/freeswitch/                 # conf snippets + Dockerfile for lab FS
  deploy/manual/                     # MANUAL_DEPLOY.md for a client env
  docker-compose.yml                 # local lab: postgres, redis, freeswitch, api, edge, web
  scripts/smoke_e2e.sh               # happy-path smoke against lab
  docs/superpowers/specs/...         # already exists
```

**Responsibility boundaries**
- `services/api` owns Postgres writes for users/carriers; never opens ESL.
- `services/edge` owns FreeSWITCH and Redis live state; reads carriers from Postgres (or via internal API) to apply gateways.
- `apps/web` talks only to `api` (HTTPS) and `edge` (WSS for softphone signaling/events + SIP.js media to FS WSS).

---

### Task 1: Monorepo lab scaffold (Compose + health)

**Files:**
- Create: `docker-compose.yml`
- Create: `services/api/go.mod`
- Create: `services/api/cmd/api/main.go`
- Create: `services/edge/go.mod`
- Create: `services/edge/cmd/edge/main.go`
- Create: `apps/web/package.json`
- Create: `apps/web/app/page.tsx`
- Create: `deploy/freeswitch/Dockerfile`
- Create: `README.md`

**Interfaces:**
- Consumes: nothing
- Produces: `GET /healthz` on api `:8080` and edge `:8081` returning `{"status":"ok"}`; Compose services `postgres`, `redis`, `freeswitch`, `api`, `edge`, `web`

- [ ] **Step 1: Create Go API health stub**

```go
// services/api/cmd/api/main.go
package main

import (
  "encoding/json"
  "log"
  "net/http"
)

func main() {
  mux := http.NewServeMux()
  mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
  })
  log.Fatal(http.ListenAndServe(":8080", mux))
}
```

```bash
cd services/api && go mod init github.com/callvoice/callvoice/services/api
go build -o /tmp/callvoice-api ./cmd/api
```

- [ ] **Step 2: Create Go edge health stub** (same pattern on `:8081`, module `.../services/edge`)

- [ ] **Step 3: Minimal Next.js app**

```bash
cd apps/web && npx create-next-app@15 . --typescript --app --eslint --no-tailwind --src-dir=false --import-alias "@/*" --use-npm
```

Replace `app/page.tsx` with a single heading `CallVoice` and link text `Health check pending`.

- [ ] **Step 4: FreeSWITCH lab Dockerfile**

```dockerfile
# deploy/freeswitch/Dockerfile
FROM safarov/freeswitch:latest
# Lab image: ESL on 8021, Sofia internal only. Harden in Task 9/14.
EXPOSE 8021 5060/udp 5060/tcp 8081 8082
```

- [ ] **Step 5: docker-compose.yml**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: callvoice
      POSTGRES_PASSWORD: callvoice
      POSTGRES_DB: callvoice
    ports: ["5432:5432"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U callvoice"]
      interval: 5s
      retries: 10
  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]
  freeswitch:
    build: ./deploy/freeswitch
    network_mode: bridge
    # ports intentionally NOT published to host public by default in prod docs;
    # for lab, map ESL only on localhost:
    ports: ["127.0.0.1:8021:8021"]
  api:
    build: ./services/api
    ports: ["8080:8080"]
    environment:
      DATABASE_URL: postgres://callvoice:callvoice@postgres:5432/callvoice?sslmode=disable
      REDIS_URL: redis://redis:6379
      SESSION_SECRET: dev-session-secret-change-me-32b
      CARRIER_SECRET_KEY: 0123456789abcdef0123456789abcdef
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_started }
  edge:
    build: ./services/edge
    ports: ["8081:8081"]
    environment:
      DATABASE_URL: postgres://callvoice:callvoice@postgres:5432/callvoice?sslmode=disable
      REDIS_URL: redis://redis:6379
      FREESWITCH_ESL_ADDR: freeswitch:8021
      FREESWITCH_ESL_PASSWORD: ClueCon
      API_BASE_URL: http://api:8080
    depends_on: [redis, freeswitch, api]
  web:
    build: ./apps/web
    ports: ["3000:3000"]
    environment:
      NEXT_PUBLIC_API_URL: http://localhost:8080
      NEXT_PUBLIC_EDGE_URL: http://localhost:8081
    depends_on: [api, edge]
```

Add minimal `Dockerfile` for api/edge/web that build and run the binaries/apps.

- [ ] **Step 6: Verify health**

```bash
docker compose up -d --build postgres redis api edge
curl -s localhost:8080/healthz && curl -s localhost:8081/healthz
```

Expected: `{"status":"ok"}` twice.

- [ ] **Step 7: Commit**

```bash
git add docker-compose.yml services apps deploy README.md
git commit -m "chore: scaffold CallVoice monorepo lab stack"
```

---

### Task 2: Postgres schema + migrations (users, carriers, DIDs)

**Files:**
- Create: `services/api/migrations/0001_init.up.sql`
- Create: `services/api/migrations/0001_init.down.sql`
- Create: `internal/models/models.go`
- Create: `services/api/internal/db/db.go`
- Test: `services/api/internal/db/db_test.go`

**Interfaces:**
- Consumes: `DATABASE_URL`
- Produces: tables `users`, `carriers`, `dids`, `agent_sessions` (optional stub); migrate on API boot

- [ ] **Step 1: Write migration SQL**

```sql
-- services/api/migrations/0001_init.up.sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE user_role AS ENUM ('admin', 'supervisor', 'agent');

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role user_role NOT NULL,
  totp_secret_encrypted BYTEA,
  totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  failed_login_count INT NOT NULL DEFAULT 0,
  locked_until TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE carriers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  host TEXT NOT NULL,
  port INT NOT NULL DEFAULT 5060,
  transport TEXT NOT NULL DEFAULT 'udp' CHECK (transport IN ('udp','tcp','tls')),
  username TEXT,
  password_encrypted BYTEA,
  realm TEXT,
  codecs TEXT[] NOT NULL DEFAULT ARRAY['PCMU','PCMA'],
  caller_ids TEXT[] NOT NULL DEFAULT '{}',
  max_cps INT NOT NULL DEFAULT 30 CHECK (max_cps > 0),
  max_channels INT NOT NULL DEFAULT 100 CHECK (max_channels > 0),
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  priority INT NOT NULL DEFAULT 100,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE dids (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  number TEXT NOT NULL UNIQUE,
  carrier_id UUID REFERENCES carriers(id) ON DELETE SET NULL,
  destination TEXT NOT NULL DEFAULT 'queue:default',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  event TEXT NOT NULL,
  ip TEXT,
  meta JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Write failing test that migrates and inserts a user row**

```go
func TestMigrateAndInsertUser(t *testing.T) {
  db := testutil.OpenTestDB(t)
  defer db.Close()
  require.NoError(t, migrate.Up(db))
  _, err := db.Exec(`INSERT INTO users (email, password_hash, role) VALUES ($1,$2,'admin')`, "admin@test.local", "x")
  require.NoError(t, err)
}
```

- [ ] **Step 3: Run test — expect FAIL until migrate helper exists**

```bash
cd services/api && go test ./internal/db/... -v
```

- [ ] **Step 4: Implement `migrate.Up` with `golang-migrate` reading `migrations/` and `db.Open`**

- [ ] **Step 5: Re-run tests — PASS**

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(api): add initial postgres schema and migrations"
```

---

### Task 3: Password hashing + session cookies

**Files:**
- Create: `internal/authkit/password.go`
- Create: `internal/authkit/password_test.go`
- Create: `internal/authkit/session.go`
- Create: `internal/authkit/session_test.go`
- Create: `services/api/internal/httpapi/auth.go`
- Modify: `services/api/cmd/api/main.go` (chi router, cookie middleware)

**Interfaces:**
- Consumes: `SESSION_SECRET` (≥32 bytes)
- Produces:
  - `authkit.HashPassword(pw string) (string, error)`
  - `authkit.VerifyPassword(hash, pw string) bool`
  - `authkit.NewSessionToken() (plain string, hash string, error)`
  - `POST /auth/login` (password step) → sets pending cookie OR full session if no 2FA
  - `POST /auth/logout`
  - `GET /auth/me`

- [ ] **Step 1: Failing tests for Argon2id hash/verify and session token hash**

```go
func TestHashAndVerify(t *testing.T) {
  h, err := authkit.HashPassword("correct horse")
  require.NoError(t, err)
  require.True(t, authkit.VerifyPassword(h, "correct horse"))
  require.False(t, authkit.VerifyPassword(h, "wrong"))
}

func TestSessionTokenHashed(t *testing.T) {
  plain, hash, err := authkit.NewSessionToken()
  require.NoError(t, err)
  require.NotEqual(t, plain, hash)
  require.Equal(t, authkit.HashToken(plain), hash)
}
```

- [ ] **Step 2: Run tests — FAIL**

- [ ] **Step 3: Implement Argon2id (params: time=3, memory=64MB, threads=2, keyLen=32) and SHA-256 token hashing**

- [ ] **Step 4: Implement login handler**

Behavior:
- Lookup user by email
- If `locked_until > now` → 423
- Verify password; on fail increment `failed_login_count`, lock 15m after 5 fails, audit `login_failed`
- On success reset counters; if `totp_enabled` set short-lived cookie `cv_pending` (5m) and return `{"status":"totp_required"}`
- Else create `sessions` row, set `cv_session` HttpOnly Secure SameSite=Lax cookie (24h lab / configurable), return `{"status":"ok","user":...}`

- [ ] **Step 5: Integration test login success/fail with httptest + test DB**

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(api): add password auth and session cookies"
```

---

### Task 4: TOTP 2FA (enroll + verify)

**Files:**
- Create: `internal/authkit/totp.go`
- Create: `internal/authkit/totp_test.go`
- Create: `services/api/internal/httpapi/totp.go`
- Modify: `services/api/internal/httpapi/auth.go`

**Interfaces:**
- Produces:
  - `POST /auth/2fa/setup` (auth required) → `{secret, otpauth_url}`
  - `POST /auth/2fa/enable` body `{code}` → enables TOTP
  - `POST /auth/2fa/verify` (pending cookie) body `{code}` → full session
- Admin role: reject full session without TOTP once policy flag `REQUIRE_ADMIN_2FA=true` (default true)

- [ ] **Step 1: Unit tests for generate/validate TOTP with fixed secret**

```go
func TestTOTPValidate(t *testing.T) {
  secret := "JBSWY3DPEHPK3PXP"
  code, err := totp.GenerateCode(secret, time.Now())
  require.NoError(t, err)
  require.True(t, authkit.ValidateTOTP(secret, code))
  require.False(t, authkit.ValidateTOTP(secret, "000000"))
}
```

- [ ] **Step 2: Implement using `github.com/pquerna/otp/totp`; store secret encrypted with `cryptokit` (Task 5 can land encrypt first — if not ready, temporarily store base64 and replace in Task 5)**

- [ ] **Step 3: Wire HTTP handlers + audit `totp_failed` / `login_ok`**

- [ ] **Step 4: Test full flow: login → totp_required → verify → `/auth/me` returns admin**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(api): add TOTP 2FA enroll and verify"
```

---

### Task 5: Carrier secret encryption + carriers CRUD API

**Files:**
- Create: `internal/cryptokit/aesgcm.go`
- Create: `internal/cryptokit/aesgcm_test.go`
- Create: `services/api/internal/httpapi/carriers.go`
- Create: `services/api/internal/store/carriers.go`
- Test: `services/api/internal/httpapi/carriers_test.go`

**Interfaces:**
- Consumes: `CARRIER_SECRET_KEY` (32-byte hex or raw)
- Produces:
  - `cryptokit.Encrypt(key, plaintext []byte) ([]byte, error)`
  - `cryptokit.Decrypt(key, blob []byte) ([]byte, error)`
  - Admin-only REST:
    - `GET /admin/carriers`
    - `POST /admin/carriers`
    - `PATCH /admin/carriers/{id}`
    - `DELETE /admin/carriers/{id}`
  - Response never returns raw SIP password (only `password_set: true|false`)

- [ ] **Step 1: AES-GCM round-trip test**

```go
func TestEncryptDecrypt(t *testing.T) {
  key := bytes.Repeat([]byte{1}, 32)
  ct, err := cryptokit.Encrypt(key, []byte("sip-secret"))
  require.NoError(t, err)
  pt, err := cryptokit.Decrypt(key, ct)
  require.NoError(t, err)
  require.Equal(t, "sip-secret", string(pt))
}
```

- [ ] **Step 2: Implement Encrypt/Decrypt (nonce prepended)**

- [ ] **Step 3: Failing API test: create carrier as admin, list hides password**

- [ ] **Step 4: Implement handlers with validation (`max_cps`, `max_channels`, transport enum)**

- [ ] **Step 5: On create/update, publish Redis channel `carriers.changed` so edge reloads (payload: carrier id or `*`) — edge listener in Task 8**

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(api): carriers CRUD with encrypted SIP secrets"
```

---

### Task 6: Next.js login + admin carriers UI (modern, professional)

**Files:**
- Create: `apps/web/app/login/page.tsx`
- Create: `apps/web/app/login/login.module.css`
- Create: `apps/web/app/(admin)/carriers/page.tsx`
- Create: `apps/web/lib/api.ts`
- Create: `apps/web/middleware.ts` (optional cookie gate)

**Interfaces:**
- Consumes: `NEXT_PUBLIC_API_URL`, cookie session via `credentials: 'include'`
- Produces: working login (password + TOTP step) and carriers admin form

- [ ] **Step 1: `lib/api.ts` fetch helpers with `credentials: 'include'`**

```ts
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${process.env.NEXT_PUBLIC_API_URL}${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}
```

- [ ] **Step 2: Login page — two-step UI (password, then 6-digit TOTP). Distinctive typography (not Inter/Roboto), subtle gradient background, brand name `CallVoice` as hero signal. No purple-default AI look.**

- [ ] **Step 3: Carriers page — table + form fields matching API (host, port, transport, auth, max_cps, max_channels, priority, enabled)**

- [ ] **Step 4: Manual browser check against running API (create admin seed — Task 7)**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(web): login with 2FA and carriers admin UI"
```

---

### Task 7: Seed admin + DID stubs + deploy bootstrap script

**Files:**
- Create: `services/api/cmd/seed/main.go`
- Create: `scripts/seed_admin.sh`
- Modify: `services/api/migrations` if needed for default queue label only (no new tables required)

**Interfaces:**
- Produces: CLI `seed` creating `admin@callvoice.local` with password from env `SEED_ADMIN_PASSWORD`, prints TOTP secret once for lab

- [ ] **Step 1: Implement seed command using authkit + DB**

- [ ] **Step 2: Script wrapper**

```bash
#!/usr/bin/env bash
set -euo pipefail
SEED_ADMIN_PASSWORD="${SEED_ADMIN_PASSWORD:?}" docker compose run --rm api /seed
```

- [ ] **Step 3: Run seed against lab DB; login via UI works**

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(api): admin seed command for lab bootstrap"
```

---

### Task 8: CPS limiter (Redis sliding window)

**Files:**
- Create: `internal/cps/limiter.go`
- Create: `internal/cps/limiter_test.go`
- Create: `services/edge/internal/cpsgate/cpsgate.go`

**Interfaces:**
- Consumes: Redis
- Produces:
  - `cps.Limiter.Allow(ctx, key string, maxCPS int, now time.Time) (bool, error)`
  - keys: `cps:global`, `cps:carrier:{id}`
  - window: 1 second fixed window (document as acceptable for jalon B; sliding optional)

- [ ] **Step 1: Unit test with miniredis**

```go
func TestAllowUnderCap(t *testing.T) {
  r := miniredis.RunT(t)
  lim := cps.New(redis.NewClient(&redis.Options{Addr: r.Addr()}))
  ok, err := lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
  require.NoError(t, err); require.True(t, ok)
  ok, _ = lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
  require.True(t, ok)
  ok, _ = lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
  require.False(t, ok)
}
```

- [ ] **Step 2: Implement INCR + EXPIRE NX on key `key:{unixSec}`**

- [ ] **Step 3: Commit**

```bash
git commit -am "feat(edge): Redis CPS limiter"
```

---

### Task 9: FreeSWITCH ESL client + apply BYOC gateways

**Files:**
- Create: `services/edge/internal/fs/esl.go`
- Create: `services/edge/internal/fs/gateway.go`
- Create: `services/edge/internal/fs/gateway_test.go` (fake ESL)
- Create: `deploy/freeswitch/conf/autoload_configs/event_socket.conf.xml`
- Create: `deploy/freeswitch/conf/sip_profiles/external.xml` (ACL placeholder)
- Modify: `services/edge/cmd/edge/main.go`

**Interfaces:**
- Consumes: `FREESWITCH_ESL_ADDR`, `FREESWITCH_ESL_PASSWORD`, carriers from Postgres
- Produces:
  - `fs.Client.API(cmd string) (string, error)`
  - `fs.ApplyCarrierGateway(c models.Carrier, password string) error` → sofia gateway create/killgw
  - On boot + Redis `carriers.changed`: reload all enabled carriers ordered by `priority ASC`
  - Failover order = priority ascending

- [ ] **Step 1: Lab FS config — ESL listen `0.0.0.0:8021`, password from env; document default `ClueCon` for lab only**

- [ ] **Step 2: Failing test: `ApplyCarrierGateway` sends expected `sofia` API commands to fake ESL**

```go
// Expect commands containing:
// sofia profile external killgw <name>
// sofia profile external gw <name> ... (or xml curl / conf write approach)
```

**Implementation choice (lock):** write gateway XML into a watched dir `deploy/freeswitch/gateways/{id}.xml` and run `sofia profile external rescan`. Password decrypted in edge only in memory.

- [ ] **Step 3: Implement ESL with `github.com/fiorix/go-eventsocket` (or maintained fork); reconnect loop with backoff**

- [ ] **Step 4: Manual lab: create carrier in admin UI → gateway appears in `sofia status gateway`**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(edge): apply BYOC gateways via FreeSWITCH ESL"
```

---

### Task 10: Agent presence + WebRTC ephemeral credentials

**Files:**
- Create: `services/edge/internal/agent/presence.go`
- Create: `services/edge/internal/webrtccred/cred.go`
- Create: `services/edge/internal/httpapi/agent.go`
- Create: `deploy/freeswitch/conf/directory/default/agent.xml.template`
- Modify: `apps/web` agent console stub

**Interfaces:**
- Auth: edge validates session by calling `GET {API_BASE_URL}/auth/me` with forwarded cookie OR shared HMAC internal token — **lock: edge accepts `Authorization: Bearer <session plain token>` duplicated into localStorage is forbidden; instead web opens WSS to edge with cookie on same-site, or edge verifies cookie against Postgres `sessions` table directly**
- **Lock:** edge shares DB read access to `sessions`/`users` (same `DATABASE_URL`) for auth; no token in localStorage.
- Produces:
  - `POST /agent/session/start` → registers agent Redis `agent:{userId}` = `available`, provisions FS directory user `agent-{uuid}` with random password TTL 2h
  - `POST /agent/session/stop`
  - `POST /agent/state` body `{state: available|paused}`
  - `GET /agent/webrtc-config` → `{wssUrl, sipUri, password, iceServers}`

- [ ] **Step 1: Unit test presence state transitions in Redis**

- [ ] **Step 2: Implement directory user provision via ESL `jsrun`/`xml_flush` or mod_xml_curl static file write + `reloadxml`**

- [ ] **Step 3: WebRTC config uses FreeSWITCH WSS URL from env `FREESWITCH_WSS_URL` (lab: `wss://localhost:7443`)**

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(edge): agent presence and ephemeral WebRTC credentials"
```

---

### Task 11: Manual outbound originate + CPS/failover

**Files:**
- Create: `services/edge/internal/dialer/manual.go`
- Create: `services/edge/internal/dialer/manual_test.go`
- Create: `services/edge/internal/httpapi/calls.go`
- Modify: agent console UI dial pad / call button

**Interfaces:**
- Produces: `POST /calls/outbound` body `{to: E164, callerId?: string}`
- Flow:
  1. Load enabled carriers by priority
  2. For each carrier: check channels (Redis count `channels:carrier:{id}`) AND `cps.Allow`
  3. If denied all → `429` with `{"error":"carrier_capacity"}`
  4. ESL originate: bridge agent contact ↔ sofia/gateway/{name}/{to}
  5. Subscribe ESL events; publish Redis `call.events` and track hangup cleanup

- [ ] **Step 1: Unit test failover skips carrier at CPS cap and uses next**

- [ ] **Step 2: Implement originate string builder (escape dialstring; reject non-E.164)**

```go
// Example originate (adjust to FS profile names):
// originate {origination_caller_id_number=...}user/agent-{id} &bridge(sofia/gateway/{gw}/{to})
```

- [ ] **Step 3: Integration lab test with a mock gateway or loopback; assert event `answered`/`hangup` received**

- [ ] **Step 4: UI: number input + Call / Hangup wired to API**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(edge): manual outbound dial with CPS and failover"
```

---

### Task 12: Inbound DID routing (basic)

**Files:**
- Create: `services/edge/internal/inbound/router.go`
- Create: `services/edge/internal/inbound/router_test.go`
- Create: `deploy/freeswitch/scripts/inbound.lua` OR dialplan XML that parks and notifies edge
- Modify: `dids` admin minimal UI or seed-only for jalon B

**Interfaces:**
- Produces: on inbound INVITE to DID, route to first `available` agent (Redis scan); if none, respond `486` / play busy
- DID admin: `POST /admin/dids` (api) minimal — number + destination `agent_pool:default`

- [ ] **Step 1: Unit test: DID → available agent mapping; empty pool → busy**

- [ ] **Step 2: Implement ESL event listener `CHANNEL_CREATE`/`CUSTOM` for inbound; bridge to agent SIP URI**

- [ ] **Step 3: Lab: register DID, simulate inbound (sipp or second FS), agent rings in browser**

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(edge): basic inbound DID to available agent"
```

---

### Task 13: Agent console WebRTC softphone (SIP.js)

**Files:**
- Create: `apps/web/app/agent/page.tsx`
- Create: `apps/web/app/agent/softphone.tsx`
- Create: `apps/web/app/agent/agent.module.css`
- Create: `apps/web/lib/softphone.ts`

**Interfaces:**
- Consumes: `/agent/session/start`, `/agent/webrtc-config`, SIP.js UserAgent
- Produces: Connect / Disconnect / Pause / Available, answer inbound, mute, hold, DTMF, hangup

- [ ] **Step 1: Implement `lib/softphone.ts` wrapping SIP.js with callbacks `onInvite`, `onBye`**

```ts
import { UserAgent, Registerer, SessionState, Invitation } from "sip.js";
// connect(config) -> register
// call(target) optional if originate is server-side (preferred for CPS)
// answer(invitation), hangup(), mute(), hold(), sendDTMF(digit)
```

**Lock:** Outbound is **server-originate** (Task 11) so CPS stays authoritative; browser is media endpoint only. Inbound: browser receives INVITE via SIP.js.

- [ ] **Step 2: Agent page UI states: offline | connecting | available | paused | in_call**

- [ ] **Step 3: Manual E2E in lab with real headset**

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(web): agent WebRTC softphone console"
```

---

### Task 14: Live events WebSocket + hardening + manual deploy doc

**Files:**
- Create: `services/edge/internal/live/hub.go`
- Create: `services/edge/internal/httpapi/ws.go`
- Create: `deploy/manual/MANUAL_DEPLOY.md`
- Create: `deploy/manual/SECURITY_CHECKLIST.md`
- Create: `scripts/smoke_e2e.sh`
- Modify: `apps/web/app/agent/page.tsx` (subscribe statuses)

**Interfaces:**
- Produces: `GET /ws` (WebSocket) events `{type, payload}` for `agent.state`, `call.state`
- Deploy doc: OS deps, install FS, Postgres, Redis, api, edge, web, TLS (Caddy/nginx), SIP allowlist, open ports matrix
- Security checklist: no public 5060/8021; fail2ban; TLS; `REQUIRE_ADMIN_2FA`; rotate `ClueCon`; firewall examples

- [ ] **Step 1: Hub unit test broadcast to two subscribers**

- [ ] **Step 2: WS auth via session cookie; heartbeat 30s**

- [ ] **Step 3: Write MANUAL_DEPLOY.md + SECURITY_CHECKLIST.md (concrete commands, not placeholders)**

- [ ] **Step 4: `scripts/smoke_e2e.sh` — healthz, login+totp (lab secret file), create carrier, agent session start (skip real SIP if `SMOKE_SKIP_SIP=1`)**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat: live WS events, smoke script, manual deploy and security docs"
```

---

### Task 15: Bench script + final acceptance against spec criterion

**Files:**
- Create: `scripts/bench_cps.sh`
- Create: `docs/superpowers/plans/notes/jalon-b-acceptance.md`

**Interfaces:**
- Produces: documented smoke/bench procedure; acceptance checklist mapping spec success criterion

- [ ] **Step 1: Bench script that hammers `POST /calls/outbound` and asserts 429 after `max_cps`**

- [ ] **Step 2: Fill acceptance checklist:**
  - [ ] Admin BYOC configured
  - [ ] Agent 2FA login
  - [ ] WebRTC outbound call
  - [ ] Inbound call received
  - [ ] CPS enforced
  - [ ] FS not publicly exposed (verified with checklist)

- [ ] **Step 3: Run full lab acceptance; record results in notes file**

- [ ] **Step 4: Commit**

```bash
git commit -am "test: CPS bench script and jalon B acceptance notes"
```

---

## Self-review (plan vs spec)

| Spec requirement | Task |
|------------------|------|
| Dedicated env / manual deploy | 1, 14 |
| Next.js + Go + Postgres + Redis + FS | 1 |
| callvoice-app vs callvoice-edge split | 1–14 |
| Login + Argon2id + cookies + 2FA | 3, 4, 6 |
| Roles admin/supervisor/agent | 2 (enum), 3–6 (admin/agent paths); supervisor UI later OK (role exists) |
| BYOC CRUD + encrypted secrets | 5, 6 |
| CPS + max channels + failover | 8, 11 |
| WebRTC agent console, pause/presence | 10, 13 |
| Manual outbound | 11, 13 |
| Basic inbound DID | 12 |
| Live events minimal | 14 |
| VoIP hardening / FS not public | 9, 14 |
| Campaign skeleton (DID destination / queue label) | 2, 12 |
| Out of scope predictive/lists/reports | not planned |

**Ambiguity fixed in plan:** server-side originate for outbound; edge reads sessions from shared DB; gateway apply via XML+rescan; SIP.js (not Verto).

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-21-callvoice-telephony-core.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — execute tasks in this session with checkpoints  

Which approach?
