# CallVoice — Design: Ops campagnes & supervision (Jalon C)

**Date:** 2026-07-22  
**Statut:** Draft pour revue  
**Périmètre:** Deuxième sous-projet — utilisateurs/agents, campagnes manuelles, listes CSV, dispositions, live wallboard, rapports CSV  
**Prérequis:** Jalon B (téléphonie core) livré — auth 2FA, BYOC, edge FreeSWITCH, WebRTC agent, CPS, call events

## 1. Contexte et objectifs

Le jalon B a livré le socle téléphonie. L’UI admin ne propose aujourd’hui que les carriers BYOC ; il manque la création d’agents, les campagnes, la qualification, le live et les rapports.

**Jalon C** ajoute la couche “ops centre d’appels” **sans** dialer prédictif ni réinjection avancée.

### Décisions verrouillées

| Décision | Choix |
|----------|--------|
| Priorité livrable | Users + campagnes + live + rapports dans le même jalon |
| Campagnes | CRUD + carrier + on/off + agents + CSV + appels manuels + dispositions |
| Rapports | Stats + filtres date/campagne/agent + export CSV (pas de PDF) |
| Live | Wallboard : compteurs + liste agents + liste appels (pas de barge/écoute) |
| Architecture | Approche 2 — API métier (Postgres) + edge live (Redis/WS) + Next.js |

## 2. Périmètre

### Inclus

- CRUD **utilisateurs** (admin / supervisor / agent) : créer, désactiver, reset MDP, statut 2FA
- Navigation admin/supervisor selon rôle (sidebar)
- **Campagnes** sortantes mode `manual` : CRUD, carrier, status (draft/running/paused/stopped), affectation agents
- **Listes** : import CSV → leads E.164
- **Dispositions** simples (globales + par campagne)
- Console agent : rejoindre une campagne running, recevoir une fiche, appeler (edge existant), qualifier
- **Live wallboard** (`/live`) via WebSocket edge
- **Rapports** (`/reports`) agrégés depuis `call_logs` + export CSV

### Exclus

- Preview / progressive / prédictif / tuning CPS campagne
- Réinjection par statut, AMD, scripts longs
- Export PDF, barge / whisper / listen
- Control plane multi-env, softphones SIP externes

### Critère de succès

Un admin crée un agent et une campagne, importe un CSV, démarre la campagne ; un agent se connecte, appelle une fiche et pose une disposition ; un supervisor voit le wallboard live et exporte un CSV filtré.

## 3. Architecture

Réutilise le split jalon B :

```text
Admin/Supervisor UI (Next.js)
        │ HTTPS
        ▼
   callvoice-api (Go) ──► PostgreSQL (users, campaigns, leads, call_logs, dispositions)
        │
Agent UI ── WSS ──► callvoice-edge (Go) ──► Redis (agent/call live state)
                           │
                           ▼
                      FreeSWITCH (inchangé)
```

- **API** : source de vérité métier, rapports, imports
- **Edge** : états live déjà présents → hub WS wallboard (événements `live.snapshot` / `live.agents` / `live.calls`)
- **Web** : pages Users, Campagnes, Live, Rapports + enrichissement `/agent`

## 4. Modèle de données (migration `0002`)

| Table | Contenu principal |
|-------|-------------------|
| `users` | Existante ; ajouter `display_name`, `disabled_at` si absents |
| `campaigns` | `name`, `carrier_id`, `status`, `dial_mode` (`manual`), timestamps |
| `campaign_agents` | `(campaign_id, user_id)` unique |
| `lead_lists` | `campaign_id`, `name`, `imported_at`, `row_count` |
| `leads` | `list_id`, `phone` E.164, `payload` JSONB, `status`, `disposition_id`, `assigned_agent_id` nullable |
| `dispositions` | `code`, `label`, `campaign_id` nullable, `is_contact`, `is_success` |
| `call_logs` | `campaign_id`, `lead_id`, `agent_id`, `direction`, `started_at`, `ended_at`, `duration_sec`, `disposition_id`, `to_number` |

**Lead status (jalon C)** : `new`, `in_progress`, `no_answer`, `busy`, `callback`, `disposed` (et éventuellement `answered` avant disposition).

**Redis (edge)** : `agent:{userId}` enrichi (`campaign_id`, `state`) ; métadonnées d’appel actif (`to`, `campaign_id`, `agent_id`, `started_at`) pour le wallboard.

## 5. Flows

### Users

1. Admin crée user (email, rôle, MDP temporaire, display_name)
2. Agent/supervisor se connecte (2FA selon politique existante)
3. Admin peut désactiver / reset MDP

### Campagne + liste

1. Créer campagne (carrier, dispositions seed)
2. Affecter agents
3. Import CSV (colonne téléphone obligatoire ; colonnes extras → `payload`)
4. Status → `running`
5. Agent sur `/agent` sélectionne la campagne, état `available`
6. API délivre prochain lead `new` (ou lead assigné) → agent appelle via `POST /calls/outbound` existant
7. Disposition → update lead + insert `call_logs`

### Live

- Client supervisor/admin ouvre `/live`, WS edge authentifié (cookie session)
- Edge pousse snapshot puis diffs (compteurs + rows agents + rows appels)
- Pas d’action barge

### Rapports

- `GET /admin/reports/summary?from=&to=&campaign_id=&agent_id=`
- Métriques : nb appels, durée totale/moyenne, ventilation dispositions, taux contact/success si flags dispos
- `GET /admin/reports/export.csv` mêmes filtres

## 6. API (surfaces principales)

**Users (admin)**  
`GET/POST /admin/users`, `PATCH /admin/users/{id}`, `POST /admin/users/{id}/reset-password`

**Campagnes (admin + supervisor lecture/écriture ops)**  
`GET/POST /admin/campaigns`, `PATCH /admin/campaigns/{id}`,  
`PUT /admin/campaigns/{id}/agents`,  
`POST /admin/campaigns/{id}/lists/import`,  
`GET/POST /admin/campaigns/{id}/dispositions`

**Agent**  
`GET /agent/campaigns` (running + assigned),  
`POST /agent/campaigns/{id}/join`,  
`GET /agent/leads/next`,  
`POST /agent/calls/{id}/disposition`

**Live**  
`GET` WS edge `/ws/live` (admin/supervisor) — événements wallboard

**Rapports**  
`GET /admin/reports/summary`, `GET /admin/reports/export.csv`

Auth : cookies existants ; middleware rôle (admin / supervisor / agent).

## 7. UI

- Sidebar : Accueil, Carriers (admin), Utilisateurs (admin), Campagnes, Live, Rapports, Console agent
- Cohérence visuelle avec login/carriers existants (FR, typo distinctive, pas de thème purple-default)
- `/agent` : sélecteur campagne, fiche lead, dispositions, softphone inchangé

## 8. Erreurs & règles

| Cas | Comportement |
|-----|----------------|
| Import CSV téléphone invalide | Ligne rejetée + compteur erreurs ; lignes valides importées |
| Campagne non `running` | Agent ne peut pas joindre / tirer de lead |
| Agent non affecté | 403 sur join |
| Disposition inconnue | 400 |
| Export trop large | Limite configurable (ex. 50k lignes) + message |

## 9. Tests

- Unitaires : parsing CSV E.164, agrégats rapports, ACL rôles
- Intégration API : CRUD campagne, import, next lead, disposition → call_log
- Live : hub wallboard filtre non-agents ; snapshot cohérent
- E2E lab : seed agent + campagne + 1 lead → disposition → CSV non vide

## 10. Hors scope / suite

Jalon D+ : prédictif, réinjection, scripts, PDF, barge, ACD entrant riche.

## 11. Prochaine étape

Après approbation de ce spec → plan d’implémentation (`writing-plans`) jalon C uniquement.
