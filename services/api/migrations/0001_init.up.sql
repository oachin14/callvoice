CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE user_role AS ENUM ('admin', 'supervisor', 'agent');

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role user_role NOT NULL,
  totp_secret_encrypted BYTEA,
  totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  failed_login_count INT NOT NULL DEFAULT 0,
  locked_until TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE carriers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  host TEXT NOT NULL,
  port INT NOT NULL DEFAULT 5060,
  transport TEXT NOT NULL DEFAULT 'udp' CHECK (transport IN ('udp','tcp','tls')),
  username TEXT,
  password_encrypted BYTEA,
  realm TEXT,
  codecs TEXT[] NOT NULL DEFAULT ARRAY['PCMU','PCMA'],
  caller_ids TEXT[] NOT NULL DEFAULT '{}',
  max_cps INT NOT NULL DEFAULT 30 CHECK (max_cps > 0),
  max_channels INT NOT NULL DEFAULT 100 CHECK (max_channels > 0),
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  priority INT NOT NULL DEFAULT 100,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE dids (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  number TEXT NOT NULL UNIQUE,
  carrier_id UUID REFERENCES carriers(id) ON DELETE SET NULL,
  destination TEXT NOT NULL DEFAULT 'queue:default',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  event TEXT NOT NULL,
  ip TEXT,
  meta JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
