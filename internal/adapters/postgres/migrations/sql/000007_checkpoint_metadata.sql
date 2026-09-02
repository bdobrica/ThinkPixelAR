CREATE TABLE checkpoints (
    tenant_id uuid NOT NULL,
    checkpoint_id uuid NOT NULL,
    session_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    workspace_generation_id uuid NOT NULL,
    workspace_generation bigint NOT NULL CHECK (workspace_generation >= 0),
    operation_id uuid NOT NULL,
    parent_checkpoint_id uuid,
    lineage_purpose text NOT NULL CHECK (lineage_purpose IN ('checkpoint', 'suspend', 'fork', 'migration')),
    state text NOT NULL CHECK (state IN ('CREATING', 'COMMITTED', 'DELETING', 'DELETED')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    manifest_schema_version text NOT NULL DEFAULT 'thinkpixel.checkpoint/v1' CHECK (manifest_schema_version = 'thinkpixel.checkpoint/v1'),
    runtime_spec_id text NOT NULL CHECK (runtime_spec_id <> '' AND octet_length(runtime_spec_id) <= 128),
    runtime_spec_digest text NOT NULL CHECK (runtime_spec_digest ~ '^[A-Za-z0-9_-]+$' AND octet_length(runtime_spec_digest) BETWEEN 32 AND 256),
    adapter_kind text NOT NULL CHECK (adapter_kind <> '' AND octet_length(adapter_kind) <= 128),
    adapter_version text NOT NULL CHECK (adapter_version <> '' AND octet_length(adapter_version) <= 128),
    adapter_build_digest text NOT NULL CHECK (adapter_build_digest ~ '^[A-Za-z0-9_-]+$' AND octet_length(adapter_build_digest) BETWEEN 32 AND 256),
    protocol_name text NOT NULL CHECK (protocol_name <> '' AND octet_length(protocol_name) <= 128),
    protocol_version text NOT NULL CHECK (protocol_version <> '' AND octet_length(protocol_version) <= 128),
    state_format_name text NOT NULL CHECK (state_format_name <> '' AND octet_length(state_format_name) <= 128),
    state_format_version text NOT NULL CHECK (state_format_version <> '' AND octet_length(state_format_version) <= 128),
    runtime_profile_digest text NOT NULL CHECK (runtime_profile_digest ~ '^[A-Za-z0-9_-]+$' AND octet_length(runtime_profile_digest) BETWEEN 32 AND 256),
    canonical_manifest bytea,
    vendor_state jsonb,
    exclusions text[],
    canonicalization text,
    digest_algorithm text,
    payload_digest text,
    composite_root text,
    signature_algorithm text,
    signer text,
    key_id text,
    signature text,
    signed_at timestamptz,
    retention_disposition text NOT NULL CHECK (retention_disposition <> '' AND octet_length(retention_disposition) <= 255),
    delete_operation_id uuid,
    cleanup_state text,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    committed_at timestamptz,
    deleted_at timestamptz,
    PRIMARY KEY (tenant_id, checkpoint_id),
    UNIQUE (tenant_id, session_id, checkpoint_id),
    UNIQUE (tenant_id, operation_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    FOREIGN KEY (tenant_id, workspace_id, session_id) REFERENCES workspaces (tenant_id, workspace_id, session_id),
    FOREIGN KEY (tenant_id, workspace_id, workspace_generation_id, workspace_generation)
        REFERENCES workspace_generations (tenant_id, workspace_id, workspace_generation_id, generation),
    FOREIGN KEY (tenant_id, parent_checkpoint_id)
        REFERENCES checkpoints (tenant_id, checkpoint_id) DEFERRABLE INITIALLY DEFERRED,
    CHECK (parent_checkpoint_id IS NULL OR parent_checkpoint_id <> checkpoint_id),
    CHECK (octet_length(canonical_manifest) <= 262144),
    CHECK (vendor_state IS NULL OR (jsonb_typeof(vendor_state) = 'array' AND jsonb_array_length(vendor_state) <= 64 AND octet_length(vendor_state::text) <= 262144)),
    CHECK (exclusions IS NULL OR exclusions @> ARRAY['execution_credentials','bootstrap_credentials','provider_credentials','gateway_tokens','scm_and_tool_credentials','signing_private_keys','sandbox_process_authority']::text[]),
    CHECK (exclusions IS NULL OR cardinality(exclusions) = 7),
    CHECK (canonicalization IS NULL OR canonicalization = 'RFC8785-JCS'),
    CHECK (digest_algorithm IS NULL OR digest_algorithm IN ('sha-256', 'sha-512')),
    CHECK (payload_digest IS NULL OR (payload_digest ~ '^[A-Za-z0-9_-]+$' AND octet_length(payload_digest) BETWEEN 32 AND 256)),
    CHECK (composite_root IS NULL OR (composite_root ~ '^[A-Za-z0-9_-]+$' AND octet_length(composite_root) BETWEEN 32 AND 256)),
    CHECK (signature_algorithm IS NULL OR (signature_algorithm <> '' AND octet_length(signature_algorithm) <= 64)),
    CHECK (signer IS NULL OR (signer <> '' AND octet_length(signer) <= 128)),
    CHECK (key_id IS NULL OR (key_id <> '' AND octet_length(key_id) <= 128)),
    CHECK (signature IS NULL OR (signature ~ '^[A-Za-z0-9_-]+$' AND octet_length(signature) BETWEEN 32 AND 4096)),
    CHECK (state <> 'COMMITTED' OR committed_at IS NOT NULL),
    CHECK (state <> 'CREATING' OR committed_at IS NULL),
    CHECK ((state = 'DELETED') = (deleted_at IS NOT NULL)),
    CHECK ((canonical_manifest IS NULL) = (committed_at IS NULL)),
    CHECK ((canonical_manifest IS NULL) = (vendor_state IS NULL)
       AND (canonical_manifest IS NULL) = (exclusions IS NULL)
       AND (canonical_manifest IS NULL) = (canonicalization IS NULL)
       AND (canonical_manifest IS NULL) = (digest_algorithm IS NULL)
       AND (canonical_manifest IS NULL) = (payload_digest IS NULL)
       AND (canonical_manifest IS NULL) = (composite_root IS NULL)
       AND (canonical_manifest IS NULL) = (signature_algorithm IS NULL)
       AND (canonical_manifest IS NULL) = (signer IS NULL)
       AND (canonical_manifest IS NULL) = (key_id IS NULL)
       AND (canonical_manifest IS NULL) = (signature IS NULL)
       AND (canonical_manifest IS NULL) = (signed_at IS NULL)),
    CHECK (updated_at >= created_at),
    CHECK (committed_at IS NULL OR (committed_at >= created_at AND signed_at <= committed_at)),
    CHECK (deleted_at IS NULL OR deleted_at >= COALESCE(committed_at, created_at))
);

CREATE INDEX checkpoints_tenant_session_committed_idx ON checkpoints (tenant_id, session_id, committed_at DESC, checkpoint_id);

ALTER TABLE checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY checkpoints_tenant_isolation ON checkpoints
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_checkpoint_immutability() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.checkpoint_id IS DISTINCT FROM OLD.checkpoint_id
       OR NEW.session_id IS DISTINCT FROM OLD.session_id OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.workspace_generation_id IS DISTINCT FROM OLD.workspace_generation_id OR NEW.workspace_generation IS DISTINCT FROM OLD.workspace_generation
       OR NEW.operation_id IS DISTINCT FROM OLD.operation_id OR NEW.parent_checkpoint_id IS DISTINCT FROM OLD.parent_checkpoint_id
       OR NEW.lineage_purpose IS DISTINCT FROM OLD.lineage_purpose OR NEW.manifest_schema_version IS DISTINCT FROM OLD.manifest_schema_version
       OR NEW.runtime_spec_id IS DISTINCT FROM OLD.runtime_spec_id OR NEW.runtime_spec_digest IS DISTINCT FROM OLD.runtime_spec_digest
       OR NEW.adapter_kind IS DISTINCT FROM OLD.adapter_kind OR NEW.adapter_version IS DISTINCT FROM OLD.adapter_version
       OR NEW.adapter_build_digest IS DISTINCT FROM OLD.adapter_build_digest OR NEW.protocol_name IS DISTINCT FROM OLD.protocol_name
       OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version OR NEW.state_format_name IS DISTINCT FROM OLD.state_format_name
       OR NEW.state_format_version IS DISTINCT FROM OLD.state_format_version OR NEW.runtime_profile_digest IS DISTINCT FROM OLD.runtime_profile_digest
       OR NEW.retention_disposition IS DISTINCT FROM OLD.retention_disposition THEN
        RAISE EXCEPTION 'checkpoint identity, generation, lineage, and compatibility binding are immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.state <> 'CREATING' AND (NEW.canonical_manifest IS DISTINCT FROM OLD.canonical_manifest
       OR NEW.vendor_state IS DISTINCT FROM OLD.vendor_state OR NEW.exclusions IS DISTINCT FROM OLD.exclusions
       OR NEW.canonicalization IS DISTINCT FROM OLD.canonicalization OR NEW.digest_algorithm IS DISTINCT FROM OLD.digest_algorithm
       OR NEW.payload_digest IS DISTINCT FROM OLD.payload_digest OR NEW.composite_root IS DISTINCT FROM OLD.composite_root
       OR NEW.signature_algorithm IS DISTINCT FROM OLD.signature_algorithm OR NEW.signer IS DISTINCT FROM OLD.signer
       OR NEW.key_id IS DISTINCT FROM OLD.key_id OR NEW.signature IS DISTINCT FROM OLD.signature OR NEW.signed_at IS DISTINCT FROM OLD.signed_at
       OR NEW.committed_at IS DISTINCT FROM OLD.committed_at) THEN
        RAISE EXCEPTION 'committed checkpoint integrity metadata is immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT ((OLD.state = 'CREATING' AND NEW.state IN ('COMMITTED', 'DELETING'))
       OR (OLD.state = 'COMMITTED' AND NEW.state = 'DELETING')
       OR (OLD.state = 'DELETING' AND NEW.state IN ('DELETING', 'DELETED'))
       OR (OLD.state = 'DELETED' AND NEW.state = 'DELETED')) THEN
        RAISE EXCEPTION 'illegal checkpoint state transition' USING ERRCODE = '23514';
    END IF;
    IF NEW.state_version <> OLD.state_version + 1 THEN
        RAISE EXCEPTION 'checkpoint state version must advance by exactly one' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER checkpoints_immutable_integrity
BEFORE UPDATE ON checkpoints FOR EACH ROW EXECUTE FUNCTION enforce_checkpoint_immutability();
