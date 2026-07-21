# Jalon B — acceptance checklist

Maps spec success criterion (*admin BYOC, agent 2FA, WebRTC outbound/inbound, CPS enforced, FS not public*) to verification status on **2026-07-21**.

**Lab context:** Task 15 run on dev machine without Docker stack / `.lab/totp_secret`. Live E2E deferred; unit + build verification recorded below.

## Spec success criterion

> Un admin configure un carrier BYOC ; un agent s'authentifie en 2FA, passe un appel WebRTC sortant et reçoit un appel entrant ; les plafonds CPS sont respectés ; FreeSWITCH n'est pas exposé sur Internet.

## Checklist

| Item | Status | Evidence |
|------|--------|----------|
| Admin BYOC configured | **Code + API verified** | CRUD handlers + admin carriers UI (Tasks 5–6); `scripts/smoke_e2e.sh` creates carrier when stack up |
| Agent 2FA login | **Code + API verified** | Argon2id + TOTP flow (Tasks 3–4); smoke script covers login→verify |
| WebRTC outbound call | **Deferred (Docker FS WSS lab)** | Server-side originate + SIP.js agent console implemented (Tasks 10–11, 13); needs headset + WSS + FS for real media |
| Inbound call received | **Deferred (Docker FS WSS lab)** | DID router + inbound ESL listener (Task 12); not E2E tested this session |
| CPS enforced | **Unit + script verified** | `go test ./internal/cps/...`, `TestOriginateAllDeniedReturnsCarrierCapacity`; `scripts/bench_cps.sh` (live or `BENCH_SKIP_LIVE=1`) |
| FS not publicly exposed | **Design verified** | `docker-compose.yml` ESL `127.0.0.1:8021`; `deploy/manual/SECURITY_CHECKLIST.md`; manual port/firewall checks **deferred on real host** |

## Commands run (Task 15)

```bash
# Go (repo root + services)
go test ./...
cd services/api && go test ./...
cd services/edge && go test ./...

# CPS bench — unit fallback (no live stack)
BENCH_SKIP_LIVE=1 ./scripts/bench_cps.sh

# Frontend
cd apps/web && npm run build
```

## Live lab procedure (when Docker / dedicated host available)

```bash
export SEED_ADMIN_PASSWORD=...
./scripts/seed_admin.sh          # save TOTP to .lab/totp_secret
docker compose up -d --build
SMOKE_SKIP_SIP=1 ./scripts/smoke_e2e.sh
./scripts/bench_cps.sh           # asserts 429 carrier_capacity after max_cps
# Full media: SMOKE_SKIP_SIP=0 + agent UI + carrier SIP + inbound DID test
# Security: deploy/manual/SECURITY_CHECKLIST.md on the host
```

## Deferred / out of scope (jalon B)

- Predictive / preview / progressive dialer, lists, reporting (spec §2)
- Supervisor UI (role exists; screens later)
- Load test at 50 agents / 1000 concurrent / 30 CPS (post-jalon)

## Honest E2E gap

**Not verified end-to-end in Task 15:** WebRTC audio path (outbound + inbound), FreeSWITCH WSS registration, real carrier SIP, and production firewall nmap checks. CPS **429 after cap** is proven at dialer/unit level and via `bench_cps.sh` when api+edge+redis+postgres are running (ESL optional for 429; required for 200 originate).
