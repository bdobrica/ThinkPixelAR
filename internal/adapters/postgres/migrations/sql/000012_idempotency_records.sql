CREATE TABLE idempotency_records (
    tenant_id uuid NOT NULL,
    idempotency_record_id uuid NOT NULL,
    principal_digest text NOT NULL CHECK (principal_digest ~ '^sha256:[0-9a-f]{64}$'),
    action text NOT NULL CHECK (action <> '' AND octet_length(action) <= 255),
    key_digest text NOT NULL CHECK (key_digest ~ '^sha256:[0-9a-f]{64}$'),
    normalization_version text NOT NULL CHECK (normalization_version <> '' AND octet_length(normalization_version) <= 64),
    request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    operation_id uuid NOT NULL,
    resource_id uuid,
    state text NOT NULL CHECK (state IN ('IN_PROGRESS', 'SUCCEEDED', 'FAILED')),
    owner_id uuid NOT NULL,
    owner_fence bigint NOT NULL CHECK (owner_fence > 0),
    lease_expires_at timestamptz,
    http_status integer,
    response_payload bytea CHECK (response_payload IS NULL OR octet_length(response_payload) <= 65536),
    response_reference text CHECK (response_reference IS NULL OR (response_reference <> '' AND octet_length(response_reference) <= 2048)),
    problem_type text CHECK (problem_type IS NULL OR (problem_type <> '' AND octet_length(problem_type) <= 255)),
    problem_code text CHECK (problem_code IS NULL OR (problem_code <> '' AND octet_length(problem_code) <= 128)),
    audit_correlation_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_record_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    UNIQUE (tenant_id, principal_digest, action, key_digest),
    CHECK (updated_at >= created_at),
    CHECK (expires_at > created_at),
    CHECK (
        (state = 'IN_PROGRESS' AND lease_expires_at IS NOT NULL AND completed_at IS NULL AND http_status IS NULL AND response_payload IS NULL AND response_reference IS NULL AND problem_type IS NULL AND problem_code IS NULL AND expires_at > lease_expires_at)
        OR
        (state = 'SUCCEEDED' AND lease_expires_at IS NULL AND completed_at IS NOT NULL AND http_status BETWEEN 200 AND 399 AND (response_payload IS NOT NULL OR response_reference IS NOT NULL) AND problem_type IS NULL AND problem_code IS NULL)
        OR
        (state = 'FAILED' AND lease_expires_at IS NULL AND completed_at IS NOT NULL AND http_status BETWEEN 400 AND 499 AND response_payload IS NULL AND response_reference IS NULL AND problem_type IS NOT NULL AND problem_code IS NOT NULL)
    )
);

CREATE INDEX idempotency_records_expiry_idx ON idempotency_records (tenant_id, expires_at)
    WHERE state <> 'IN_PROGRESS';
CREATE INDEX idempotency_records_recovery_idx ON idempotency_records (tenant_id, lease_expires_at)
    WHERE state = 'IN_PROGRESS';

ALTER TABLE idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_records FORCE ROW LEVEL SECURITY;
CREATE POLICY idempotency_records_tenant_isolation ON idempotency_records
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_idempotency_record_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.idempotency_record_id IS DISTINCT FROM OLD.idempotency_record_id
       OR NEW.principal_digest IS DISTINCT FROM OLD.principal_digest
       OR NEW.action IS DISTINCT FROM OLD.action
       OR NEW.key_digest IS DISTINCT FROM OLD.key_digest
       OR NEW.normalization_version IS DISTINCT FROM OLD.normalization_version
       OR NEW.request_digest IS DISTINCT FROM OLD.request_digest
       OR NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.resource_id IS DISTINCT FROM OLD.resource_id
       OR NEW.audit_correlation_id IS DISTINCT FROM OLD.audit_correlation_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'idempotency scope and logical operation are immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.state <> 'IN_PROGRESS' THEN
        RAISE EXCEPTION 'terminal idempotency record is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.owner_fence < OLD.owner_fence OR NEW.owner_fence > OLD.owner_fence + 1 THEN
        RAISE EXCEPTION 'idempotency owner fence must be monotonic' USING ERRCODE = '23514';
    END IF;
    IF NEW.owner_fence = OLD.owner_fence AND NEW.owner_id IS DISTINCT FROM OLD.owner_id THEN
        RAISE EXCEPTION 'idempotency owner change requires a new fence' USING ERRCODE = '23514';
    END IF;
    IF NEW.owner_fence = OLD.owner_fence + 1 AND (OLD.lease_expires_at IS NULL OR CURRENT_TIMESTAMP < OLD.lease_expires_at) THEN
        RAISE EXCEPTION 'active idempotency lease cannot be taken over' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER idempotency_records_fenced_mutation BEFORE UPDATE ON idempotency_records
FOR EACH ROW EXECUTE FUNCTION enforce_idempotency_record_mutation();
