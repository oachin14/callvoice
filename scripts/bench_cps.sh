#!/usr/bin/env bash
# CPS bench — hammer POST /calls/outbound until carrier CPS cap returns 429.
#
# Requires a running lab stack (docker compose or manual deploy):
#   API_URL, EDGE_URL, SMOKE_ADMIN_PASSWORD, TOTP secret file.
#
# CPS is enforced before ESL originate; 429 is returned once the per-carrier
# window is exhausted even when FreeSWITCH/SIP is unavailable (SMOKE_SKIP_SIP=1).
#
# Usage:
#   export SMOKE_ADMIN_PASSWORD=...
#   echo "$TOTP_SECRET" > .lab/totp_secret
#   docker compose up -d
#   ./scripts/bench_cps.sh
#
# Env:
#   BENCH_MAX_CPS      per-carrier cap for test carrier (default 3)
#   BENCH_BURST        outbound attempts (default max_cps + 5)
#   BENCH_TARGET       E.164 destination (default +33123456789)
#   BENCH_SKIP_LIVE=1  skip HTTP bench; run go test dialer CPS instead
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
EDGE_URL="${EDGE_URL:-http://127.0.0.1:8081}"
ADMIN_EMAIL="${SMOKE_ADMIN_EMAIL:-admin@callvoice.local}"
ADMIN_PASSWORD="${SMOKE_ADMIN_PASSWORD:-${SEED_ADMIN_PASSWORD:-}}"
TOTP_FILE="${SMOKE_TOTP_SECRET_FILE:-$ROOT/.lab/totp_secret}"
BENCH_MAX_CPS="${BENCH_MAX_CPS:-3}"
BENCH_BURST="${BENCH_BURST:-$((BENCH_MAX_CPS + 5))}"
BENCH_TARGET="${BENCH_TARGET:-+33123456789}"
BENCH_SKIP_LIVE="${BENCH_SKIP_LIVE:-0}"
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"' EXIT

log() { printf '==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }

totp_code() {
  local secret="$1"
  if command -v oathtool >/dev/null 2>&1; then
    oathtool --totp -b "$secret"
    return
  fi
  python3 - "$secret" <<'PY'
import base64, hmac, hashlib, struct, sys, time
secret = sys.argv[1].strip().replace(" ", "").upper()
pad = "=" * ((8 - len(secret) % 8) % 8)
key = base64.b32decode(secret + pad)
counter = int(time.time()) // 30
msg = struct.pack(">Q", counter)
digest = hmac.new(key, msg, hashlib.sha1).digest()
offset = digest[19] & 0x0F
code = (struct.unpack(">I", digest[offset:offset+4])[0] & 0x7FFFFFFF) % 1_000_000
print(f"{code:06d}")
PY
}

run_unit_bench() {
  log "BENCH_SKIP_LIVE=1 — unit proof via go test (dialer CPS / 429 path)"
  (cd "$ROOT/services/edge" && go test ./internal/dialer/ -run 'CPS|Capacity' -count=1 -v)
}

if [[ "$BENCH_SKIP_LIVE" == "1" ]]; then
  run_unit_bench
  log "unit CPS bench OK"
  exit 0
fi

need curl
need python3

[[ -n "$ADMIN_PASSWORD" ]] || die "set SMOKE_ADMIN_PASSWORD or SEED_ADMIN_PASSWORD"
[[ -f "$TOTP_FILE" ]] || die "TOTP secret file missing: $TOTP_FILE"
TOTP_SECRET="$(tr -d '[:space:]' <"$TOTP_FILE")"
[[ -n "$TOTP_SECRET" ]] || die "empty TOTP secret in $TOTP_FILE"

log "healthz api + edge"
curl -fsS "$API_URL/healthz" | grep -q '"status":"ok"' || die "api healthz failed"
curl -fsS "$EDGE_URL/healthz" | grep -q '"status":"ok"' || die "edge healthz failed"

log "login + 2FA"
curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" \
  "$API_URL/auth/login" >/dev/null
CODE="$(totp_code "$TOTP_SECRET")"
curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d "{\"code\":\"$CODE\"}" \
  "$API_URL/auth/2fa/verify" | grep -q '"status":"ok"' || die "totp verify failed"

SUFFIX="$(date +%s)"
log "create bench carrier max_cps=$BENCH_MAX_CPS"
CARRIER_JSON="$(curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\":\"bench-cps-$SUFFIX\",
    \"host\":\"sip.bench.test\",
    \"port\":5060,
    \"transport\":\"udp\",
    \"username\":\"bench\",
    \"password\":\"bench-secret\",
    \"codecs\":[\"PCMU\"],
    \"caller_ids\":[\"+33100000001\"],
    \"max_cps\":$BENCH_MAX_CPS,
    \"max_channels\":100,
    \"enabled\":true,
    \"priority\":1
  }" \
  "$API_URL/admin/carriers")"
echo "$CARRIER_JSON" | grep -q '"id"' || die "create carrier failed: $CARRIER_JSON"

sleep 1

log "agent session start (edge auth for /calls/outbound)"
curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -X POST -H 'Content-Type: application/json' \
  "$EDGE_URL/agent/session/start" >/dev/null || true

log "hammer POST /calls/outbound burst=$BENCH_BURST target=$BENCH_TARGET"
OK=0
CAP=0
OTHER=0
for i in $(seq 1 "$BENCH_BURST"); do
  RESP_FILE="$(mktemp)"
  HTTP_CODE="$(curl -sS -o "$RESP_FILE" -w '%{http_code}' \
    -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
    -X POST -H 'Content-Type: application/json' \
    -d "{\"to\":\"$BENCH_TARGET\"}" \
    "$EDGE_URL/calls/outbound" || echo "000")"
  BODY="$(cat "$RESP_FILE")"
  rm -f "$RESP_FILE"
  case "$HTTP_CODE" in
    200) OK=$((OK + 1)) ;;
    429)
      if echo "$BODY" | grep -q 'carrier_capacity'; then
        CAP=$((CAP + 1))
      else
        OTHER=$((OTHER + 1))
        printf '  attempt %s: 429 without carrier_capacity: %s\n' "$i" "$BODY" >&2
      fi
      ;;
    *)
      OTHER=$((OTHER + 1))
      printf '  attempt %s: HTTP %s %s\n' "$i" "$HTTP_CODE" "$BODY" >&2
      ;;
  esac
done

log "results: ok=$OK capacity_429=$CAP other=$OTHER (max_cps=$BENCH_MAX_CPS burst=$BENCH_BURST)"

if [[ "$CAP" -lt 1 ]]; then
  log "no 429 carrier_capacity — falling back to unit proof"
  run_unit_bench
  die "live bench did not observe 429 (stack may be down or GLOBAL_MAX_CPS misconfigured); unit tests passed"
fi

if [[ "$CAP" -lt $((BENCH_BURST - BENCH_MAX_CPS)) ]]; then
  log "WARN: expected more 429 responses after CPS exhaustion (got $CAP)"
fi

log "CPS bench OK (saw $CAP x 429 carrier_capacity)"
