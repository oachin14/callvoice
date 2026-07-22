# Task 12 Report

## Deliverables

- **`SEED_DEMO=1`** in `services/api/cmd/seed/main.go` — demo agent, running campaign, 3 leads (idempotent)
- **`scripts/seed_jalon_c.sh`** — wrapper for docker compose seed with demo flag
- **`docs/superpowers/plans/notes/jalon-c-acceptance.md`** — checklist with honest E2E gaps
- **`README.md`** — web routes + demo seed one-liner

## Verification

- `go test ./...` in `services/api` — pass
- `npm run build` in `apps/web` — pass
- Live Docker smoke — **deferred** (documented in acceptance notes)

## Commit

`test: jalon C acceptance notes and demo seed`
