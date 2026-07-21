#!/usr/bin/env bash
set -euo pipefail
: "${SEED_ADMIN_PASSWORD:?SEED_ADMIN_PASSWORD is required}"
docker compose run --rm -e SEED_ADMIN_PASSWORD api /seed
