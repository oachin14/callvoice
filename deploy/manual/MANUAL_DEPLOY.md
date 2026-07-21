# Déploiement manuel CallVoice (Jalon B)

Guide ops pour un environnement client dédié (Ubuntu 24.04 LTS). Commandes concrètes — adapter les noms d’hôte / secrets.

## Matrice des ports

| Port | Proto | Service | Exposition publique |
|------|-------|---------|---------------------|
| 443 | TCP | Caddy/nginx → web + API + edge (WSS `/ws`) | Oui |
| 80 | TCP | Redirect HTTP→HTTPS | Oui (redirect) |
| 3000 | TCP | Next.js (`web`) | Non (via reverse proxy) |
| 8080 | TCP | `callvoice-api` | Non (via reverse proxy) |
| 8081 | TCP | `callvoice-edge` (+ `GET /ws`) | Non (via reverse proxy) |
| 5432 | TCP | PostgreSQL | Non (localhost / Docker network) |
| 6379 | TCP | Redis | Non |
| 5060 | UDP/TCP | FreeSWITCH SIP (carriers) | **Allowlist SIP only** |
| 5080 | UDP/TCP | FreeSWITCH external (si utilisé) | Allowlist |
| 8021 | TCP | FreeSWITCH ESL | **Jamais public** (127.0.0.1) |
| 7443 | TCP | FreeSWITCH WSS (WebRTC) | Via reverse proxy ou allowlist agents |
| 16384–32768 | UDP | RTP | Vers carriers / agents selon topo |

## 1. Dépendances OS

```bash
sudo apt update
sudo apt install -y git curl ca-certificates gnupg ufw fail2ban \
  postgresql-client redis-tools oathtool
# Docker (lab / compose) :
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
```

## 2. Lab rapide (Docker Compose)

Depuis la racine du repo :

```bash
export SEED_ADMIN_PASSWORD='change-me-strong'
docker compose up -d --build
./scripts/seed_admin.sh   # imprime le secret TOTP une fois — le sauver dans .lab/totp_secret

curl -sS http://127.0.0.1:8080/healthz   # api
curl -sS http://127.0.0.1:8081/healthz   # edge
SMOKE_SKIP_SIP=1 ./scripts/smoke_e2e.sh
```

## 3. PostgreSQL

```bash
sudo apt install -y postgresql postgresql-contrib
sudo -u postgres createuser -P callvoice          # mot de passe fort
sudo -u postgres createdb -O callvoice callvoice
# pg_hba : connexions locales / Docker network only
```

`DATABASE_URL` exemple :

```text
postgres://callvoice:SECRET@127.0.0.1:5432/callvoice?sslmode=disable
```

Les migrations partent au boot de l’API (`OpenAndMigrate`).

## 4. Redis

```bash
sudo apt install -y redis-server
sudo sed -i 's/^bind .*/bind 127.0.0.1/' /etc/redis/redis.conf
sudo systemctl enable --now redis-server
redis-cli ping   # PONG
```

`REDIS_URL=redis://127.0.0.1:6379`

## 5. FreeSWITCH

Image lab : `deploy/freeswitch/` (compose service `freeswitch`).

Prod (schéma) :

```bash
# ESL uniquement en loopback (déjà dans conf lab)
# Vérifier :
ss -lntp | grep 8021
# doit montrer 127.0.0.1:8021 — PAS 0.0.0.0 en prod internet

# Gateways BYOC écrits par edge dans FREESWITCH_GATEWAY_DIR
# Directory agents WebRTC dans FREESWITCH_DIRECTORY_DIR
# WSS : voir deploy/freeswitch/NOTES-WSS.md (profil :7443)
```

Variables edge typiques :

```bash
FREESWITCH_ESL_ADDR=127.0.0.1:8021
FREESWITCH_ESL_PASSWORD='<rotated-not-ClueCon>'
FREESWITCH_GATEWAY_DIR=/etc/freeswitch/gateways
FREESWITCH_DIRECTORY_DIR=/etc/freeswitch/directory/default
FREESWITCH_WSS_URL=wss://voice.example.com/wss
FREESWITCH_SIP_DOMAIN=voice.example.com
```

## 6. API (`callvoice-app` Go)

```bash
cd /opt/callvoice
# build
docker build -f services/api/Dockerfile -t callvoice-api:latest .
# ou : go build -o /usr/local/bin/callvoice-api ./services/api/cmd/api

export DATABASE_URL=postgres://callvoice:SECRET@127.0.0.1:5432/callvoice?sslmode=disable
export REDIS_URL=redis://127.0.0.1:6379
export SESSION_SECRET="$(openssl rand -hex 32)"
export CARRIER_SECRET_KEY="$(openssl rand -hex 16)"   # 32 hex chars = 16 bytes
export COOKIE_SECURE=true
export CORS_ORIGINS=https://app.example.com
export REQUIRE_ADMIN_2FA=true

# systemd ExecStart=/usr/local/bin/callvoice-api  (listen :8080)
```

Seed admin :

```bash
SEED_ADMIN_PASSWORD='...' CARRIER_SECRET_KEY='...' DATABASE_URL='...' \
  go run ./services/api/cmd/seed
# Sauver le TOTP secret affiché → /etc/callvoice/lab_totp_secret (mode 600)
```

## 7. Edge (`callvoice-edge`)

```bash
docker build -f services/edge/Dockerfile -t callvoice-edge:latest .
# listen :8081 — healthz + /agent/* + /calls/* + GET /ws

export DATABASE_URL=...
export REDIS_URL=...
export CARRIER_SECRET_KEY=...   # même clé que l’API
export CORS_ORIGINS=https://app.example.com
export REQUIRE_ADMIN_2FA=true
export GLOBAL_MAX_CPS=30
# + vars FreeSWITCH ci-dessus
```

## 8. Web (Next.js)

```bash
cd apps/web
npm ci
NEXT_PUBLIC_API_URL=https://api.example.com \
NEXT_PUBLIC_EDGE_URL=https://edge.example.com \
  npm run build
# servir `.next` via `next start -p 3000` derrière Caddy
```

## 9. TLS (Caddy exemple)

`/etc/caddy/Caddyfile` :

```caddy
app.example.com {
  reverse_proxy 127.0.0.1:3000
}

api.example.com {
  reverse_proxy 127.0.0.1:8080
}

edge.example.com {
  reverse_proxy 127.0.0.1:8081
}
```

WebSocket live : `wss://edge.example.com/ws` (même cookie `cv_session` que l’API si domaine parent partagé, ou proxy unifié).

Pour unifier cookies (même site) :

```caddy
app.example.com {
  handle /api/* {
    uri strip_prefix /api
    reverse_proxy 127.0.0.1:8080
  }
  handle /edge/* {
    uri strip_prefix /edge
    reverse_proxy 127.0.0.1:8081
  }
  handle {
    reverse_proxy 127.0.0.1:3000
  }
}
```

## 10. Allowlist SIP / firewall

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
# SIP carriers (exemples — IPs réelles du BYOC) :
sudo ufw allow from 203.0.113.10 to any port 5060 proto udp
sudo ufw allow from 203.0.113.10 to any port 5060 proto tcp
# RTP (ajuster plage FS) :
sudo ufw allow from 203.0.113.10 to any port 16384:32768 proto udp
# ESL / Redis / Postgres : PAS d’ouverture publique
sudo ufw enable
sudo ufw status verbose
```

Voir aussi `deploy/manual/SECURITY_CHECKLIST.md`.
