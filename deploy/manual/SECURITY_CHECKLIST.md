# Checklist sécurité CallVoice (téléphonie)

À cocher avant mise en production d’un environnement dédié.

## FreeSWITCH / VoIP

- [ ] Port **8021 (ESL)** écoute uniquement `127.0.0.1` (jamais `0.0.0.0` sur Internet)
  ```bash
  ss -lntp | grep 8021
  # attendu : 127.0.0.1:8021
  ```
- [ ] Mot de passe ESL **≠ `ClueCon`** (valeur lab par défaut)
  ```bash
  # Générer et poser dans event_socket.conf.xml + FREESWITCH_ESL_PASSWORD
  openssl rand -base64 24
  ```
- [ ] Port **5060/5080** : allowlist IP carriers uniquement (pas d’accès monde)
  ```bash
  sudo ufw status | grep 5060
  nmap -sU -p 5060 --open "$(curl -4 -s ifconfig.me)"   # depuis l’extérieur : filtrés
  ```
- [ ] WSS WebRTC (7443 ou reverse proxy) : TLS valide ; pas d’ESL exposé via le même listener
- [ ] Directory agents / gateways : permissions root:fs, pas world-readable pour secrets

## Réseau / OS

- [ ] `ufw` (ou équivalent) : deny incoming par défaut ; 22/80/443 + SIP allowlist
- [ ] fail2ban actif sur sshd (et éventuellement Caddy/nginx)
  ```bash
  sudo apt install -y fail2ban
  sudo systemctl enable --now fail2ban
  sudo fail2ban-client status sshd
  ```
- [ ] Postgres et Redis bind localhost / réseau Docker privé uniquement
- [ ] Pas de publication Docker `0.0.0.0:8021` / `0.0.0.0:5432` en prod

## Application

- [ ] `REQUIRE_ADMIN_2FA=true` (défaut) — admin sans TOTP bloqué hors enrollment
- [ ] `COOKIE_SECURE=true` derrière HTTPS
- [ ] `SESSION_SECRET` et `CARRIER_SECRET_KEY` aléatoires, distincts par env, hors git
  ```bash
  openssl rand -hex 32   # SESSION_SECRET
  openssl rand -hex 32   # CARRIER_SECRET_KEY (64 hex = 32 bytes AES-256)
  ```
- [ ] `CORS_ORIGINS` limité au(x) front réel(s) — pas `*`
- [ ] Logs audit login / `totp_failed` consultables
- [ ] WebSocket `/ws` : auth cookie session ; pas d’anon

## Vérifs rapides post-déploiement

```bash
curl -sS https://api.example.com/healthz
curl -sS https://edge.example.com/healthz
# ESL depuis l’extérieur doit échouer / timeout :
nc -vz "$(curl -4 -s ifconfig.me)" 8021 || true
SMOKE_SKIP_SIP=1 API_URL=https://api.example.com EDGE_URL=https://edge.example.com \
  ./scripts/smoke_e2e.sh
```

## Rotation

- [ ] Rotation ESL password documentée (FS + edge restart)
- [ ] Rotation `CARRIER_SECRET_KEY` = re-chiffrer secrets carriers (procédure manuelle)
- [ ] Rotation session secret = invalide toutes les sessions (acceptable)
