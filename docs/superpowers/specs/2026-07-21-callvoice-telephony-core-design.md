# CallVoice — Design: cœur téléphonie (Jalon B)

**Date:** 2026-07-21  
**Statut:** Draft pour revue  
**Périmètre:** Premier sous-projet — fondations téléphonie d’un environnement client dédié

## 1. Contexte et objectifs

CallVoice est une plateforme AtoZ moderne pour centres d’appels (alternative à Vicidial) : campagnes, listes, réinjection, dialer manuel/prédictif, rapports, enregistrements, live stats, qualification, carriers, routage, agent WebRTC.

**Modèle commercial :** SaaS **mono-client** — chaque client dispose d’un **environnement dédié** (isolation perf et sécurité), pas de multi-tenant partagé sur un même FreeSWITCH.

**Cibles par environnement :** 50 agents, 1000 appels simultanés, 30 CPS (CPS = ressource rare côté carrier → optimisation critique).

**Inspiration :** Vicidial (fonctionnel), sans reprendre UI legacy, Asterisk, ni la complexité paramétrique.

**Moteur téléphonie :** FreeSWITCH (stabilité / capacité) plutôt qu’Asterisk.

### Décisions verrouillées

| Décision | Choix |
|----------|--------|
| Hébergement | SaaS, 1 stack dédié par client |
| Carriers | BYOC — le client ajoute 1..n trunks SIP |
| Flux | Sortant + entrant dès le départ (usage initial surtout sortant) |
| Modes dialer (vision) | Manuel + preview + progressive + prédictif (CPS/vitesse) |
| Provisioning env | Manuel au début ; automatisation plus tard |
| Stack | Next.js + Go (`callvoice-edge` / app) + PostgreSQL + Redis + FreeSWITCH |
| Premier jalon | Téléphonie solide (FS, BYOC, WebRTC) avant features campagnes avancées |

## 2. Décomposition produit

Trop large pour un seul cycle. Sous-projets :

1. **Fondations téléphonie (ce spec)** — auth 2FA, BYOC, FreeSWITCH, WebRTC agent, CPS limiter, live events minimaux
2. **Ops campagnes** — campagnes, listes, réinjection, qualification
3. **Dialer avancé** — preview, progressive, prédictif + tuning CPS/vitesse
4. **Observabilité** — dashboards temps réel riches, rapports, UI enregistrements
5. **Control plane** — provisioning auto des environnements (plus tard)

## 3. Architecture (Approche 2 — cœur découplé)

Chaque environnement client (déploiement manuel) :

```text
Internet
   │
   ├─ HTTPS ──► Next.js (UI agent + admin) ──► API app (Go)
   │                                              │
   │                                              ▼
   │                                         PostgreSQL
   │                                              │
   └─ WSS/WebRTC ──► callvoice-edge (Go) ◄── Redis (sessions, events, CPS)
                           │
                           ▼
                     FreeSWITCH
                           │
                     Carriers SIP (BYOC)
```

### Composants

| Composant | Responsabilité |
|-----------|----------------|
| **callvoice-app** (Next.js + API Go) | Login, 2FA, users/rôles, config métier. Pas de média. |
| **callvoice-edge** (Go) | ESL FreeSWITCH, BYOC, WebRTC credentials, sessions agent, CPS limiter, events temps réel (WebSocket). |
| **FreeSWITCH** | SIP, media, bridge, enregistrements locaux. |
| **PostgreSQL** | Source de vérité (users, carriers, squelette campagnes/DID). |
| **Redis** | État live (agents, appels), tokens CPS, pub/sub. |

**Règle d’isolation :** aucun client navigateur ne parle directement à FreeSWITCH. Tout contrôle passe par `callvoice-edge`.

## 4. Auth, rôles et login

- Page login moderne (Next.js) : identifiant + mot de passe + **2FA TOTP**.
- Sessions : cookies HTTP-only sécurisés (SameSite, Secure), côté serveur ; pas de JWT longue durée dans le navigateur.
- Hash mots de passe : Argon2id.
- 2FA **obligatoire** pour Admin ; fortement recommandé (configurable) pour Supervisor/Agent.
- Rate-limit + lockout progressif ; audit des connexions / échecs 2FA.
- Reset password : email si SMTP configuré ; sinon reset par admin en phase early.

### Rôles (par environnement)

| Rôle | Accès |
|------|--------|
| Owner / Admin | Users, carriers, sécurité, config globale |
| Supervisor | Campagnes, listes, live, rapports (jalons suivants) |
| Agent | Console : présence, pauses, WebRTC, appels, qualification (plus tard) |

Rôle ops plateforme CallVoice (multi-env) : hors jalon B.

### Flux session agent

1. Login web + 2FA  
2. Console agent  
3. `callvoice-edge` émet credentials WebRTC **éphémères** (TTL court, liés à la session)  
4. Enregistrement FreeSWITCH via WSS  
5. Déconnexion / timeout → cleanup FS + Redis  

## 5. FreeSWITCH, BYOC, WebRTC

### BYOC

- Admin CRUD trunk : host, port, transport (UDP/TCP/TLS), auth (user/pass ou IP ACL), codecs, caller IDs, **CPS max**, **canaux max**.
- Secrets carriers **chiffrés au repos** (Postgres).
- Edge applique la config FreeSWITCH (gateways) et expose health trunk.
- Jalon B : carrier par défaut + **failover simple** (carrier suivant si échec).
- Entrant : mapping DID → file/campagne (modèle prêt dès B).

### WebRTC agent

- Navigateur ↔ FreeSWITCH en WebRTC over WSS (choix exact Verto vs SIP.js/SIP-WSS figé à l’implémentation ; critère : stabilité + simplicité ops).
- Softphone intégré : answer, hangup, mute, hold, DTMF.
- Pas de softphone externe (Zoiper, eyeBeam) dans le produit cible.
- Enregistrements : FreeSWITCH local ; rétention/UI complètes reportées.

### CPS et capacité

- Compteurs Redis : CPS global + par carrier (fenêtre glissante).
- `originate` refusé si plafond atteint → backpressure explicite (socle du futur prédictif).
- Design dimensionné pour 50 agents / 1000 concurrent / 30 CPS ; validation charge hors CI au début.

### Sécurité VoIP

- FreeSWITCH non exposé publiquement ; SIP allowlist IPs carriers.
- Rate-limit REGISTER/OPTIONS ; fail2ban ou équivalent.
- TLS pour UI/API ; SIP TLS vers carriers si supporté.
- Surface d’attaque minimale : seuls HTTPS (app) et WSS (edge/WebRTC) + SIP depuis carriers.

## 6. Périmètre jalon B

### Inclus

- Doc déploiement manuel d’un env dédié
- Login + users/rôles + 2FA
- BYOC 1..n + secrets chiffrés + CPS/canaux + failover simple
- Console agent WebRTC : connect/disconnect, dispo/pause, sortant manuel, entrant basique
- Events live minimaux (statut agent, appel en cours)
- Hardening VoIP de base
- Squelette données campagnes / files / DID

### Exclus (jalons suivants)

- Preview / progressive / prédictif + tuning vitesse avancé
- Listes + réinjection par statut
- Qualification riche, scripts
- Rapports, UI enregistrements complète, CRM clients
- Dashboards temps réel riches
- Provisioning automatique des env
- Softphones SIP externes

### Critère de succès jalon B

Un admin configure un carrier BYOC ; un agent s’authentifie en 2FA, passe un appel WebRTC sortant et reçoit un appel entrant ; les plafonds CPS sont respectés ; FreeSWITCH n’est pas exposé sur Internet.

## 7. Flux critiques

### Agent en ligne

Login → session → edge enregistre agent (`available` dans Redis) → endpoint WebRTC → UI “connecté”. Cleanup à la déconnexion.

### Sortant manuel

UI → droits → edge vérifie CPS/canaux → originate FS → ring → bridge agent ↔ callee → events → hangup/cleanup.

### Entrant

INVITE (ACL OK) → FS → edge route DID → agent/file → ring WebRTC → bridge. Aucun agent : busy ou stub configurable.

## 8. Erreurs et résilience

| Situation | Comportement |
|-----------|----------------|
| CPS atteint | Refus originate + message UI clair |
| Carrier timeout / 5xx | Failover si configuré ; sinon échec explicite |
| Restart edge | Reconnexion agents ; reconcile état Redis ↔ FS |
| FS down | Healthcheck rouge ; appels bloqués proprement |
| Échec ICE WebRTC | Retry court + message réseau/firewall |
| Brute-force auth | Lockout + audit |

## 9. Tests

- **Unitaires :** CPS limiter, routing DID, chiffrement secrets
- **Intégration :** edge ↔ FS (ESL) en lab ; originate + inbound trunk test
- **E2E :** login 2FA → appel manuel WebRTC → hangup
- **Charge :** script de bench documenté (montée progressive) ; pas d’exigence 1000/30 en CI au jalon B
- **Sécu :** audit ports exposés ; refus SIP hors allowlist

## 10. Hors scope de ce document

Design détaillé du dialer prédictif, réinjection de listes, control plane multi-env, et UI marketing complète — specs dédiées après livraison du jalon B.

## 11. Prochaine étape

Après approbation de ce spec → plan d’implémentation (`writing-plans`) pour le jalon B uniquement.
