CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('active', 'revoked')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE agent_devices (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    public_key TEXT NOT NULL,
    capabilities TEXT[] NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'revoked')),
    registered_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (agent_id, id)
);

CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    scopes TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    FOREIGN KEY (agent_id, device_id) REFERENCES agent_devices(agent_id, id) ON DELETE CASCADE
);

CREATE TABLE transfers (
    id TEXT PRIMARY KEY,
    transport TEXT NOT NULL,
    target TEXT NOT NULL CHECK (target IN ('agent', 'browser_link')),
    status TEXT NOT NULL CHECK (status IN ('created', 'offered', 'accepted', 'connecting', 'transferring', 'interrupted', 'retryable', 'completed', 'failed', 'cancelled', 'expired')),
    from_agent_id TEXT NOT NULL REFERENCES agents(id),
    to_agent_id TEXT REFERENCES agents(id),
    public_token_hash TEXT,
    agent_ticket_hash TEXT NOT NULL UNIQUE,
    receiver_ticket_hash TEXT,
    receiver_ticket_expires_at TIMESTAMPTZ,
    file_name TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL CHECK (file_size_bytes >= 0),
    file_sha256 TEXT,
    mime_type TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    max_downloads INTEGER NOT NULL CHECK (max_downloads > 0),
    download_count INTEGER NOT NULL DEFAULT 0 CHECK (download_count >= 0),
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    interrupted_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    failure_reason TEXT,
    CHECK ((target = 'agent' AND to_agent_id IS NOT NULL) OR (target = 'browser_link' AND to_agent_id IS NULL))
);

CREATE INDEX idx_transfers_status_created_at ON transfers(status, created_at);
CREATE INDEX idx_transfers_from_agent_created_at ON transfers(from_agent_id, created_at);
CREATE INDEX idx_transfers_to_agent_created_at ON transfers(to_agent_id, created_at);
CREATE UNIQUE INDEX idx_transfers_public_token_hash_unique ON transfers(public_token_hash) WHERE public_token_hash IS NOT NULL;

CREATE TABLE transfer_events (
    id BIGSERIAL PRIMARY KEY,
    transfer_id TEXT NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('transfer.created', 'transfer.offered', 'transfer.accepted', 'transfer.started', 'transfer.completed', 'transfer.failed', 'transfer.cancelled', 'transfer.expired')),
    actor_agent_id TEXT REFERENCES agents(id),
    role TEXT NOT NULL CHECK (role IN ('sender_agent', 'receiving_agent', 'browser_recipient')),
    redacted_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_transfer_events_transfer_id_created_at ON transfer_events(transfer_id, created_at);
CREATE INDEX idx_transfer_events_type_created_at ON transfer_events(event_type, created_at);
