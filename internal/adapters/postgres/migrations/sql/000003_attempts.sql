ALTER TABLE executions
    ADD UNIQUE (tenant_id, execution_id, session_generation);

CREATE TABLE attempts (
    tenant_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    execution_generation bigint NOT NULL CHECK (execution_generation > 0),
    attempt_no bigint NOT NULL CHECK (attempt_no > 0),
    is_current boolean NOT NULL DEFAULT true,
    state text NOT NULL CHECK (state IN (
        'PENDING', 'ACQUIRING', 'STARTING', 'RUNNING', 'INTERRUPTING',
        'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'REPLACED'
    )),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    sandbox_binding_reference uuid,
    harness_binding_reference uuid,
    sandbox_heartbeat_at timestamptz,
    harness_heartbeat_at timestamptz,
    terminal_result_reference text CHECK (
        terminal_result_reference IS NULL OR (terminal_result_reference <> '' AND octet_length(terminal_result_reference) <= 2048)
    ),
    terminal_result_digest text CHECK (terminal_result_digest IS NULL OR terminal_result_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    terminal_at timestamptz,
    PRIMARY KEY (tenant_id, attempt_id),
    FOREIGN KEY (tenant_id, execution_id, execution_generation)
        REFERENCES executions (tenant_id, execution_id, session_generation),
    UNIQUE (tenant_id, execution_id, attempt_no),
    CHECK (updated_at >= created_at),
    CHECK (sandbox_heartbeat_at IS NULL OR sandbox_heartbeat_at BETWEEN created_at AND updated_at),
    CHECK (harness_heartbeat_at IS NULL OR harness_heartbeat_at BETWEEN created_at AND updated_at),
    CHECK ((state IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'REPLACED')) = (terminal_at IS NOT NULL)),
    CHECK ((terminal_result_reference IS NULL) = (terminal_result_digest IS NULL)),
    CHECK ((state IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'REPLACED')) = (terminal_result_reference IS NOT NULL)),
    CHECK (terminal_at IS NULL OR terminal_at >= created_at),
    CHECK (NOT is_current OR state NOT IN ('SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'REPLACED'))
);

CREATE UNIQUE INDEX attempts_one_current_idx
    ON attempts (tenant_id, execution_id) WHERE is_current;
CREATE INDEX attempts_tenant_execution_state_idx
    ON attempts (tenant_id, execution_id, state, updated_at, attempt_id);

ALTER TABLE attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY attempts_tenant_isolation ON attempts
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_attempt_identity_or_binding_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
       OR NEW.execution_id IS DISTINCT FROM OLD.execution_id
       OR NEW.execution_generation IS DISTINCT FROM OLD.execution_generation
       OR NEW.attempt_no IS DISTINCT FROM OLD.attempt_no
       OR (OLD.sandbox_binding_reference IS NOT NULL AND NEW.sandbox_binding_reference IS DISTINCT FROM OLD.sandbox_binding_reference)
       OR (OLD.harness_binding_reference IS NOT NULL AND NEW.harness_binding_reference IS DISTINCT FROM OLD.harness_binding_reference) THEN
        RAISE EXCEPTION 'attempt identity, generation, ordinal, and established binding references are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attempts_immutable_identity_and_bindings
BEFORE UPDATE ON attempts
FOR EACH ROW EXECUTE FUNCTION reject_attempt_identity_or_binding_change();
