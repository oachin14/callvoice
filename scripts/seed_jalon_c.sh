#!/usr/bin/env bash
# Seed admin + jalon C demo data (agent, running campaign, 3 leads).
set -euo pipefail
: "${SEED_ADMIN_PASSWORD:?SEED_ADMIN_PASSWORD is required}"
docker compose run --rm \
  -e SEED_ADMIN_PASSWORD \
  -e SEED_DEMO=1 \
  -e SEED_DEMO_AGENT_PASSWORD="${SEED_DEMO_AGENT_PASSWORD:-}" \
  api /seed
