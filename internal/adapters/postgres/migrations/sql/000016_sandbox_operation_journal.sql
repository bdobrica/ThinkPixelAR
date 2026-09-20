-- Preserve legacy binding columns and immutable first-operation identities.
-- Complete replay inputs and repeated lifecycle operations live in companion rows.
CREATE TABLE sandbox_binding_requests (
    tenant_id uuid NOT NULL,
    sandbox_binding_id uuid NOT NULL,
    acquire_operation_id uuid NOT NULL,
    canonical_request bytea NOT NULL CHECK (octet_length(canonical_request) BETWEEN 2 AND 262144
        AND jsonb_typeof(convert_from(canonical_request,'UTF8')::jsonb) = 'object'),
    operation_revision bigint NOT NULL DEFAULT 0 CHECK (operation_revision >= 0),
    PRIMARY KEY (tenant_id,sandbox_binding_id),
    UNIQUE (tenant_id,acquire_operation_id),
    FOREIGN KEY (tenant_id,sandbox_binding_id) REFERENCES sandbox_bindings (tenant_id,sandbox_binding_id)
);
CREATE TABLE sandbox_operations (
    tenant_id uuid NOT NULL,
    sandbox_binding_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('suspend','resume','release')),
    request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id,operation_id),
    UNIQUE (tenant_id,sandbox_binding_id,revision),
    FOREIGN KEY (tenant_id,sandbox_binding_id) REFERENCES sandbox_binding_requests (tenant_id,sandbox_binding_id)
);
ALTER TABLE sandbox_binding_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_binding_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY sandbox_binding_requests_tenant_isolation ON sandbox_binding_requests
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
ALTER TABLE sandbox_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY sandbox_operations_tenant_isolation ON sandbox_operations
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION enforce_sandbox_request_journal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'sandbox replay records are retained' USING ERRCODE='23514';
    END IF;
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.sandbox_binding_id IS DISTINCT FROM OLD.sandbox_binding_id
        OR NEW.acquire_operation_id IS DISTINCT FROM OLD.acquire_operation_id
        OR NEW.canonical_request IS DISTINCT FROM OLD.canonical_request
        OR NEW.operation_revision <> OLD.operation_revision + 1 THEN
        RAISE EXCEPTION 'sandbox request is immutable and revision advances once' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER sandbox_request_journal BEFORE UPDATE OR DELETE ON sandbox_binding_requests
    FOR EACH ROW EXECUTE FUNCTION enforce_sandbox_request_journal();
CREATE FUNCTION reject_sandbox_operation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'sandbox operations are immutable' USING ERRCODE='23514';
END;
$$;
CREATE TRIGGER sandbox_operations_immutable BEFORE UPDATE OR DELETE ON sandbox_operations
    FOR EACH ROW EXECUTE FUNCTION reject_sandbox_operation_mutation();
