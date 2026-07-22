ALTER TABLE users
  ADD COLUMN display_name TEXT,
  ADD COLUMN disabled_at TIMESTAMPTZ;

CREATE TABLE campaigns (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  carrier_id UUID NOT NULL REFERENCES carriers(id),
  status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft', 'running', 'paused', 'stopped')),
  dial_mode TEXT NOT NULL DEFAULT 'manual'
    CHECK (dial_mode IN ('manual')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE campaign_agents (
  campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (campaign_id, user_id)
);

CREATE TABLE lead_lists (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  row_count INT NOT NULL DEFAULT 0
);

CREATE TABLE dispositions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL,
  label TEXT NOT NULL,
  campaign_id UUID REFERENCES campaigns(id) ON DELETE CASCADE,
  is_contact BOOLEAN NOT NULL DEFAULT FALSE,
  is_success BOOLEAN NOT NULL DEFAULT FALSE,
  UNIQUE (code, campaign_id)
);

CREATE TABLE leads (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  list_id UUID NOT NULL REFERENCES lead_lists(id) ON DELETE CASCADE,
  phone TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'new'
    CHECK (status IN ('new', 'in_progress', 'no_answer', 'busy', 'callback', 'disposed', 'answered')),
  disposition_id UUID REFERENCES dispositions(id) ON DELETE SET NULL,
  assigned_agent_id UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE call_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
  lead_id UUID REFERENCES leads(id) ON DELETE SET NULL,
  agent_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  direction TEXT NOT NULL CHECK (direction IN ('outbound', 'inbound')),
  started_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ,
  duration_sec INT,
  disposition_id UUID REFERENCES dispositions(id) ON DELETE SET NULL,
  to_number TEXT NOT NULL
);
