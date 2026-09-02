CREATE TABLE executions (
    tenant_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    session_id uuid NOT NULL,
    session_generation bigint NOT NULL CHECK (session_generation > 0),
    authority_mode text NOT NULL CHECK (authority_mode IN ('LOCAL', 'THINKPIXEL_AG')),
    authority_namespace text NOT NULL CHECK (authority_namespace <> '' AND octet_length(authority_namespace) <= 255),
    authority_reference text NOT NULL CHECK (authority_reference <> '' AND octet_length(authority_reference) <= 255),
    external_run_id text CHECK (external_run_id IS NULL OR (external_run_id <> '' AND octet_length(external_run_id) <= 255)),
    grant_digest text NOT NULL CHECK (grant_digest ~ '^sha256:[0-9a-f]{64}$'),
    agent_id text NOT NULL CHECK (agent_id <> '' AND octet_length(agent_id) <= 255),
    agent_version_id text NOT NULL CHECK (agent_version_id <> '' AND octet_length(agent_version_id) <= 255),
    agent_evidence jsonb NOT NULL CHECK (jsonb_typeof(agent_evidence) = 'object' AND octet_length(agent_evidence::text) <= 65536),
    agent_evidence_digest text NOT NULL CHECK (agent_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    deadline timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN (
        'QUEUED', 'MATERIALIZING', 'RUNNING', 'CANCELLING', 'TIMING_OUT',
        'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT'
    )),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    terminal_result_reference text CHECK (
        terminal_result_reference IS NULL OR (terminal_result_reference <> '' AND octet_length(terminal_result_reference) <= 2048)
    ),
    terminal_result_digest text CHECK (terminal_result_digest IS NULL OR terminal_result_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    terminal_at timestamptz,
    PRIMARY KEY (tenant_id, execution_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    UNIQUE (tenant_id, authority_mode, authority_namespace, authority_reference),
    CHECK (deadline > created_at),
    CHECK (updated_at >= created_at),
    CHECK ((state IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT')) = (terminal_at IS NOT NULL)),
    CHECK ((terminal_result_reference IS NULL) = (terminal_result_digest IS NULL)),
    CHECK ((state IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT')) = (terminal_result_reference IS NOT NULL)),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at)
);

CREATE INDEX executions_tenant_session_state_idx
    ON executions (tenant_id, session_id, state, updated_at, execution_id);

ALTER TABLE executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE executions FORCE ROW LEVEL SECURITY;
CREATE POLICY executions_tenant_isolation ON executions
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_execution_binding_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.execution_id IS DISTINCT FROM OLD.execution_id
       OR NEW.session_id IS DISTINCT FROM OLD.session_id
       OR NEW.session_generation IS DISTINCT FROM OLD.session_generation
       OR NEW.authority_mode IS DISTINCT FROM OLD.authority_mode
       OR NEW.authority_namespace IS DISTINCT FROM OLD.authority_namespace
       OR NEW.authority_reference IS DISTINCT FROM OLD.authority_reference
       OR NEW.external_run_id IS DISTINCT FROM OLD.external_run_id
       OR NEW.grant_digest IS DISTINCT FROM OLD.grant_digest
       OR NEW.agent_id IS DISTINCT FROM OLD.agent_id
       OR NEW.agent_version_id IS DISTINCT FROM OLD.agent_version_id
       OR NEW.agent_evidence IS DISTINCT FROM OLD.agent_evidence
       OR NEW.agent_evidence_digest IS DISTINCT FROM OLD.agent_evidence_digest
       OR NEW.deadline IS DISTINCT FROM OLD.deadline THEN
        RAISE EXCEPTION 'execution identity, generation, authority, agent evidence, and deadline are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER executions_immutable_binding
BEFORE UPDATE ON executions
FOR EACH ROW EXECUTE FUNCTION reject_execution_binding_change();
