CREATE TABLE runtime_event_streams (
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, session_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id)
);

CREATE TABLE runtime_events (
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    session_id uuid NOT NULL,
    execution_id uuid,
    attempt_id uuid,
    sequence bigint NOT NULL CHECK (sequence > 0),
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    schema_version text NOT NULL CHECK (schema_version = 'thinkpixel.runtime-event/v1'),
    event_type text NOT NULL CHECK (event_type IN (
        'session.created', 'session.state_changed', 'session.degraded', 'session.closed',
        'execution.accepted', 'execution.started', 'execution.completed', 'execution.failed', 'execution.cancelled', 'execution.timed_out',
        'attempt.started', 'attempt.replaced', 'attempt.terminal', 'sandbox.state_changed', 'sandbox.health_changed',
        'workspace.generation_committed', 'checkpoint.committed', 'checkpoint.deleted',
        'assistant.message.delta', 'assistant.message.completed', 'tool.requested', 'tool.status_changed', 'artifact.published',
        'signal.accepted', 'permission.requested', 'permission.resolved', 'stream.gap'
    )),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source text NOT NULL CHECK (source IN ('agent-runtime', 'agentd', 'harness-adapter', 'sandbox-provider', 'workspace-provider', 'run-authority', 'gateway')),
    classification text NOT NULL CHECK (classification IN ('Public', 'Internal', 'Confidential')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) BETWEEN 2 AND 65536),
    payload_reference text CHECK (payload_reference IS NULL OR (payload_reference <> '' AND octet_length(payload_reference) <= 2048)),
    request_id uuid,
    trace_id text CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$'),
    span_id text CHECK (span_id IS NULL OR span_id ~ '^[0-9a-f]{16}$'),
    retention_policy text NOT NULL CHECK (retention_policy <> '' AND octet_length(retention_policy) <= 128),
    retain_until timestamptz,
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    FOREIGN KEY (tenant_id, execution_id) REFERENCES executions (tenant_id, execution_id),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, attempt_id),
    UNIQUE (tenant_id, session_id, sequence),
    CHECK (attempt_id IS NULL OR execution_id IS NOT NULL),
    CHECK (span_id IS NULL OR trace_id IS NOT NULL),
    CHECK (retain_until IS NULL OR retain_until > recorded_at)
);

CREATE INDEX runtime_events_tenant_session_sequence_idx ON runtime_events (tenant_id, session_id, sequence);
CREATE INDEX runtime_events_tenant_retention_idx ON runtime_events (tenant_id, retain_until, session_id, sequence) WHERE retain_until IS NOT NULL;

ALTER TABLE runtime_event_streams ENABLE ROW LEVEL SECURITY;
ALTER TABLE runtime_event_streams FORCE ROW LEVEL SECURITY;
CREATE POLICY runtime_event_streams_tenant_isolation ON runtime_event_streams
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);
ALTER TABLE runtime_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE runtime_events FORCE ROW LEVEL SECURITY;
CREATE POLICY runtime_events_tenant_isolation ON runtime_events
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION allocate_runtime_event_sequence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    previous_sequence bigint;
BEGIN
    INSERT INTO runtime_event_streams (tenant_id, session_id) VALUES (NEW.tenant_id, NEW.session_id)
        ON CONFLICT (tenant_id, session_id) DO NOTHING;
    SELECT last_sequence INTO previous_sequence FROM runtime_event_streams
        WHERE tenant_id = NEW.tenant_id AND session_id = NEW.session_id FOR UPDATE;
    IF NEW.sequence <> previous_sequence + 1 THEN
        RAISE EXCEPTION 'runtime event sequence must advance by exactly one' USING ERRCODE = '23514';
    END IF;
    UPDATE runtime_event_streams SET last_sequence = NEW.sequence, updated_at = NEW.recorded_at
        WHERE tenant_id = NEW.tenant_id AND session_id = NEW.session_id;
    RETURN NEW;
END;
$$;

CREATE FUNCTION validate_runtime_event_lineage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.execution_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM executions WHERE tenant_id = NEW.tenant_id AND execution_id = NEW.execution_id AND session_id = NEW.session_id
    ) THEN
        RAISE EXCEPTION 'runtime event execution does not belong to session' USING ERRCODE = '23514';
    END IF;
    IF NEW.attempt_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM attempts WHERE tenant_id = NEW.tenant_id AND attempt_id = NEW.attempt_id AND execution_id = NEW.execution_id
    ) THEN
        RAISE EXCEPTION 'runtime event attempt does not belong to execution' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION reject_runtime_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'runtime events are append-only' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER runtime_events_validate_lineage BEFORE INSERT ON runtime_events FOR EACH ROW EXECUTE FUNCTION validate_runtime_event_lineage();
CREATE TRIGGER runtime_events_allocate_sequence BEFORE INSERT ON runtime_events FOR EACH ROW EXECUTE FUNCTION allocate_runtime_event_sequence();
CREATE TRIGGER runtime_events_append_only BEFORE UPDATE OR DELETE ON runtime_events FOR EACH ROW EXECUTE FUNCTION reject_runtime_event_mutation();
