# Task 2 Report

## P2: Single-admin guard TOCTOU (fixed)

**Problem:** `CountAdmins` ran outside a transaction before insert/update, so concurrent admin creates could both pass the check.

**Fix:** `Create` and admin-promotion paths in `Update` now run inside a DB transaction. `pg_advisory_xact_lock` serializes the admin slot check; `SELECT … FOR UPDATE` locks the target row on update. `RequireSession` now rejects users with `disabled_at` set (403 `account_disabled`).

**Files:** `services/api/internal/store/users.go`, `services/api/internal/httpapi/auth.go`

**Tests:** `go test ./...` in `services/api` — pass.
