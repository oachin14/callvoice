# CallVoice

Monorepo lab stack for CallVoice telephony core development.

## Services

| Service    | Port | Description                          |
|------------|------|--------------------------------------|
| `api`      | 8080 | REST API (`GET /healthz`)            |
| `edge`     | 8081 | Telephony edge (`GET /healthz`)      |
| `web`      | 3000 | Next.js web app                      |
| `postgres` | 5432 | PostgreSQL 16                        |
| `redis`    | 6379 | Redis 7                              |
| `freeswitch` | 8021 (localhost) | FreeSWITCH ESL lab image |

## Quick start

```bash
docker compose up -d --build
curl -s localhost:8080/healthz   # {"status":"ok"}
curl -s localhost:8081/healthz   # {"status":"ok"}
```

## Layout

```
services/api/       Go API service
services/edge/      Go edge service
apps/web/           Next.js 15 web app
deploy/freeswitch/  FreeSWITCH lab Dockerfile
```
