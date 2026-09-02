CREATE TABLE runtime_profile_resolution_snapshots (
    tenant_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    schema_version bigint NOT NULL CHECK (schema_version = 1),
    profile_name text NOT NULL CHECK (profile_name ~ '^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$' AND octet_length(profile_name) <= 128),
    canonical_resolution bytea NOT NULL CHECK (
        octet_length(canonical_resolution) BETWEEN 2 AND 65536
        AND jsonb_typeof(convert_from(canonical_resolution, 'UTF8')::jsonb) = 'object'
    ),
    resolution_digest text NOT NULL CHECK (resolution_digest ~ '^sha256:[0-9a-f]{64}$'),
    canonicalization text NOT NULL DEFAULT 'RFC8785-JCS' CHECK (canonicalization = 'RFC8785-JCS'),
    implementation_reference text NOT NULL CHECK (implementation_reference <> '' AND octet_length(implementation_reference) <= 255),
    implementation_version text NOT NULL CHECK (implementation_version <> '' AND octet_length(implementation_version) <= 128),
    implementation_digest text NOT NULL CHECK (implementation_digest ~ '^sha256:[0-9a-f]{64}$'),
    canonical_supported_versions bytea NOT NULL CHECK (
        octet_length(canonical_supported_versions) BETWEEN 2 AND 32768
        AND jsonb_typeof(convert_from(canonical_supported_versions, 'UTF8')::jsonb) = 'object'
    ),
    supported_versions_digest text NOT NULL CHECK (supported_versions_digest ~ '^sha256:[0-9a-f]{64}$'),
    decision_reason text NOT NULL CHECK (decision_reason <> '' AND octet_length(decision_reason) <= 255),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, execution_id),
    FOREIGN KEY (tenant_id, execution_id) REFERENCES executions (tenant_id, execution_id)
);

ALTER TABLE runtime_profile_resolution_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE runtime_profile_resolution_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY runtime_profile_resolution_snapshots_tenant_isolation ON runtime_profile_resolution_snapshots
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_runtime_profile_resolution_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'runtime profile resolution snapshots are immutable' USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER runtime_profile_resolution_snapshots_immutable
BEFORE UPDATE OR DELETE ON runtime_profile_resolution_snapshots
FOR EACH ROW EXECUTE FUNCTION reject_runtime_profile_resolution_snapshot_mutation();
