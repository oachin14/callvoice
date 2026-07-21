#!/usr/bin/env bash
set -euo pipefail
SEED_ADMIN_PASSWORD="${SEED_ADMIN_PASSWORD:?}" docker compose run --rm api /seed
