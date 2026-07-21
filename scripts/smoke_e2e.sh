#!/usr/bin/env bash
# Smoke E2E CallVoice — healthz, login+TOTP, carrier, agent session.
# Skip softphone / real SIP when SMOKE_SKIP_SIP=1 (default for CI/lab without headset).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_URL="${API_URL:-http://127.0.0.1:8080}"
EDGE_URL="${EDGE_URL:-http://127.0.0.1:8081}"
ADMIN_EMAIL="${SMOKE_ADMIN_EMAIL:-admin@callvoice.local}"
ADMIN_PASSWORD="${SMOKE_ADMIN_PASSWORD:-${SEED_ADMIN_PASSWORD:-}}"
TOTP_FILE="${SMOKE_TOTP_SECRET_FILE:-$ROOT/.lab/totp_secret}"
SMOKE_SKIP_SIP="${SMOKE_SKIP_SIP:-1}"
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

need curl
need python3

[[ -n "$ADMIN_PASSWORD" ]] || die "set SMOKE_ADMIN_PASSWORD or SEED_ADMIN_PASSWORD"
[[ -f "$TOTP_FILE" ]] || die "TOTP secret file missing: $TOTP_FILE (save seed output there)"
TOTP_SECRET="$(tr -d '[:space:]' <"$TOTP_FILE")"
[[ -n "$TOTP_SECRET" ]] || die "empty TOTP secret in $TOTP_FILE"

log "healthz api"
curl -fsS "$API_URL/healthz" | grep -q '"status":"ok"' || die "api healthz failed"

log "healthz edge"
curl -fsS "$EDGE_URL/healthz" | grep -q '"status":"ok"' || die "edge healthz failed"

log "login"
LOGIN_JSON="$(curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" \
  "$API_URL/auth/login")"
echo "$LOGIN_JSON" | grep -q 'totp_required' || die "expected totp_required, got: $LOGIN_JSON"

CODE="$(totp_code "$TOTP_SECRET")"
log "totp verify"
VERIFY_JSON="$(curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d "{\"code\":\"$CODE\"}" \
  "$API_URL/auth/2fa/verify")"
echo "$VERIFY_JSON" | grep -q '"status":"ok"' || die "totp verify failed: $VERIFY_JSON"

log "create carrier"
CARRIER_JSON="$(curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"smoke-carrier",
    "host":"sip.example.test",
    "port":5060,
    "transport":"udp",
    "username":"smoke",
    "password":"smoke-secret",
    "codecs":["PCMU","PCMA"],
    "caller_ids":["+33100000000"],
    "max_cps":5,
    "max_channels":10,
    "enabled":true,
    "priority":10
  }' \
  "$API_URL/admin/carriers")"
echo "$CARRIER_JSON" | grep -q '"id"' || die "create carrier failed: $CARRIER_JSON"

log "agent session start"
# Edge shares cv_session cookie jar (same-site localhost).
START_JSON="$(curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -X POST \
  -H 'Content-Type: application/json' \
  "$EDGE_URL/agent/session/start")"
echo "$START_JSON" | grep -q '"status":"ok"' || die "session start failed: $START_JSON"

if [[ "$SMOKE_SKIP_SIP" == "1" ]]; then
  log "SMOKE_SKIP_SIP=1 — skip SIP register / outbound media"
else
  die "full SIP path not automated yet; use agent UI or set SMOKE_SKIP_SIP=1"
fi

log "agent session stop"
curl -fsS -c "$COOKIE_JAR" -b "$COOKIE_JAR" \
  -X POST \
  -H 'Content-Type: application/json' \
  "$EDGE_URL/agent/session/stop" >/dev/null

log "smoke OK"
