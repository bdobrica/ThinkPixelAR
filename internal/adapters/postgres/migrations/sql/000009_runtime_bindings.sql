CREATE TABLE sandbox_bindings (
    tenant_id uuid NOT NULL,
    sandbox_binding_id uuid NOT NULL,
    session_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    execution_generation bigint NOT NULL CHECK (execution_generation > 0),
    attempt_id uuid NOT NULL,
    attempt_no bigint NOT NULL CHECK (attempt_no > 0),
    provider_kind text NOT NULL CHECK (provider_kind <> '' AND octet_length(provider_kind) <= 128),
    provider_reference text CHECK (provider_reference IS NULL OR (provider_reference <> '' AND octet_length(provider_reference) <= 2048)),
    resolution_digest text NOT NULL CHECK (resolution_digest ~ '^sha256:[0-9a-f]{64}$'),
    acquire_operation_id uuid NOT NULL,
    acquire_request_digest text NOT NULL CHECK (acquire_request_digest ~ '^sha256:[0-9a-f]{64}$'),
    suspend_operation_id uuid,
    suspend_request_digest text,
    resume_operation_id uuid,
    resume_request_digest text,
    release_operation_id uuid,
    release_request_digest text,
    state text NOT NULL DEFAULT 'REQUESTED' CHECK (state IN ('REQUESTED','PROVISIONING','READY','ACTIVE','SUSPENDING','SUSPENDED','RESUMING','RELEASING','RELEASED','FAILED','UNKNOWN')),
    reason text CHECK (reason IS NULL OR (reason <> '' AND octet_length(reason) <= 255)),
    effective_facts_digest text CHECK (effective_facts_digest IS NULL OR effective_facts_digest ~ '^sha256:[0-9a-f]{64}$'),
    observed_at timestamptz,
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, sandbox_binding_id),
    UNIQUE (tenant_id, attempt_id),
    UNIQUE (tenant_id, attempt_id, sandbox_binding_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    FOREIGN KEY (tenant_id, execution_id, execution_generation) REFERENCES executions (tenant_id, execution_id, session_generation),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES attempts (tenant_id, attempt_id),
    CHECK ((suspend_operation_id IS NULL) = (suspend_request_digest IS NULL)),
    CHECK (suspend_request_digest IS NULL OR suspend_request_digest ~ '^sha256:[0-9a-f]{64}$'),
    CHECK ((resume_operation_id IS NULL) = (resume_request_digest IS NULL)),
    CHECK (resume_request_digest IS NULL OR resume_request_digest ~ '^sha256:[0-9a-f]{64}$'),
    CHECK ((release_operation_id IS NULL) = (release_request_digest IS NULL)),
    CHECK (release_request_digest IS NULL OR release_request_digest ~ '^sha256:[0-9a-f]{64}$'),
    CHECK (observed_at IS NULL OR observed_at BETWEEN created_at AND updated_at),
    CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX sandbox_bindings_provider_reference_idx ON sandbox_bindings (tenant_id, provider_kind, provider_reference) WHERE provider_reference IS NOT NULL;
CREATE INDEX sandbox_bindings_reconcile_idx ON sandbox_bindings (tenant_id, state, updated_at, sandbox_binding_id);

CREATE TABLE harness_bindings (
    tenant_id uuid NOT NULL,
    harness_binding_id uuid NOT NULL,
    session_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    execution_generation bigint NOT NULL CHECK (execution_generation > 0),
    attempt_id uuid NOT NULL,
    attempt_no bigint NOT NULL CHECK (attempt_no > 0),
    sandbox_binding_id uuid NOT NULL,
    adapter_kind text NOT NULL CHECK (adapter_kind <> '' AND octet_length(adapter_kind) <= 128),
    adapter_version text NOT NULL CHECK (adapter_version <> '' AND octet_length(adapter_version) <= 128),
    adapter_build_digest text NOT NULL CHECK (adapter_build_digest ~ '^sha256:[0-9a-f]{64}$'),
    negotiation_digest text NOT NULL CHECK (negotiation_digest ~ '^sha256:[0-9a-f]{64}$'),
    protocol_name text NOT NULL CHECK (protocol_name <> '' AND octet_length(protocol_name) <= 128),
    protocol_version text NOT NULL CHECK (protocol_version <> '' AND octet_length(protocol_version) <= 128),
    process_reference text NOT NULL CHECK (process_reference <> '' AND octet_length(process_reference) <= 255),
    vendor_session_reference text NOT NULL CHECK (vendor_session_reference <> '' AND octet_length(vendor_session_reference) <= 2048),
    start_operation_id uuid NOT NULL,
    start_request_digest text NOT NULL CHECK (start_request_digest ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'STARTING' CHECK (state IN ('STARTING','READY','EXECUTING','INTERRUPTING','EXITED','FAILED','UNKNOWN')),
    reason text CHECK (reason IS NULL OR (reason <> '' AND octet_length(reason) <= 255)),
    observed_at timestamptz,
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, harness_binding_id),
    UNIQUE (tenant_id, attempt_id),
    UNIQUE (tenant_id, attempt_id, harness_binding_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    FOREIGN KEY (tenant_id, execution_id, execution_generation) REFERENCES executions (tenant_id, execution_id, session_generation),
    FOREIGN KEY (tenant_id, attempt_id, sandbox_binding_id) REFERENCES sandbox_bindings (tenant_id, attempt_id, sandbox_binding_id),
    CHECK (observed_at IS NULL OR observed_at BETWEEN created_at AND updated_at),
    CHECK (updated_at >= created_at)
);

CREATE INDEX harness_bindings_state_idx ON harness_bindings (tenant_id, state, updated_at, harness_binding_id);

ALTER TABLE attempts ADD CONSTRAINT attempts_sandbox_binding_fk
    FOREIGN KEY (tenant_id, attempt_id, sandbox_binding_reference)
    REFERENCES sandbox_bindings (tenant_id, attempt_id, sandbox_binding_id)
    DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE attempts ADD CONSTRAINT attempts_harness_binding_fk
    FOREIGN KEY (tenant_id, attempt_id, harness_binding_reference)
    REFERENCES harness_bindings (tenant_id, attempt_id, harness_binding_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE sandbox_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY sandbox_bindings_tenant_isolation ON sandbox_bindings
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);
ALTER TABLE harness_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE harness_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY harness_bindings_tenant_isolation ON harness_bindings
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_runtime_binding_identity_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.session_id IS DISTINCT FROM OLD.session_id
       OR NEW.execution_id IS DISTINCT FROM OLD.execution_id OR NEW.execution_generation IS DISTINCT FROM OLD.execution_generation
       OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR NEW.attempt_no IS DISTINCT FROM OLD.attempt_no THEN
        RAISE EXCEPTION 'runtime binding ownership and attempt fence are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'sandbox_bindings' AND (NEW.sandbox_binding_id IS DISTINCT FROM OLD.sandbox_binding_id
       OR NEW.provider_kind IS DISTINCT FROM OLD.provider_kind
       OR (OLD.provider_reference IS NOT NULL AND NEW.provider_reference IS DISTINCT FROM OLD.provider_reference)
       OR NEW.resolution_digest IS DISTINCT FROM OLD.resolution_digest OR NEW.acquire_operation_id IS DISTINCT FROM OLD.acquire_operation_id
       OR NEW.acquire_request_digest IS DISTINCT FROM OLD.acquire_request_digest) THEN
        RAISE EXCEPTION 'sandbox binding identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'sandbox_bindings' AND (
       (OLD.suspend_operation_id IS NOT NULL AND (NEW.suspend_operation_id IS DISTINCT FROM OLD.suspend_operation_id OR NEW.suspend_request_digest IS DISTINCT FROM OLD.suspend_request_digest))
       OR (OLD.resume_operation_id IS NOT NULL AND (NEW.resume_operation_id IS DISTINCT FROM OLD.resume_operation_id OR NEW.resume_request_digest IS DISTINCT FROM OLD.resume_request_digest))
       OR (OLD.release_operation_id IS NOT NULL AND (NEW.release_operation_id IS DISTINCT FROM OLD.release_operation_id OR NEW.release_request_digest IS DISTINCT FROM OLD.release_request_digest))) THEN
        RAISE EXCEPTION 'established sandbox operation identities are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'harness_bindings' AND (NEW.harness_binding_id IS DISTINCT FROM OLD.harness_binding_id
       OR NEW.sandbox_binding_id IS DISTINCT FROM OLD.sandbox_binding_id OR NEW.adapter_kind IS DISTINCT FROM OLD.adapter_kind
       OR NEW.adapter_version IS DISTINCT FROM OLD.adapter_version OR NEW.adapter_build_digest IS DISTINCT FROM OLD.adapter_build_digest
       OR NEW.negotiation_digest IS DISTINCT FROM OLD.negotiation_digest OR NEW.protocol_name IS DISTINCT FROM OLD.protocol_name
       OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version OR NEW.process_reference IS DISTINCT FROM OLD.process_reference
       OR NEW.vendor_session_reference IS DISTINCT FROM OLD.vendor_session_reference OR NEW.start_operation_id IS DISTINCT FROM OLD.start_operation_id
       OR NEW.start_request_digest IS DISTINCT FROM OLD.start_request_digest) THEN
        RAISE EXCEPTION 'harness binding identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER sandbox_bindings_immutable_identity BEFORE UPDATE ON sandbox_bindings FOR EACH ROW EXECUTE FUNCTION reject_runtime_binding_identity_change();
CREATE TRIGGER harness_bindings_immutable_identity BEFORE UPDATE ON harness_bindings FOR EACH ROW EXECUTE FUNCTION reject_runtime_binding_identity_change();
