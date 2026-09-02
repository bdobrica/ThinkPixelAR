CREATE TABLE workspaces (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('PROVISIONING', 'READY', 'ATTACHED', 'SNAPSHOTTING', 'DEGRADED', 'DELETING', 'DELETED')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    provider_kind text NOT NULL CHECK (provider_kind <> '' AND octet_length(provider_kind) <= 255),
    provider_reference text NOT NULL CHECK (provider_reference <> '' AND octet_length(provider_reference) <= 2048),
    mount_path text NOT NULL DEFAULT '/workspace' CHECK (mount_path = '/workspace'),
    capacity_bytes bigint NOT NULL CHECK (capacity_bytes > 0),
    access_mode text NOT NULL CHECK (access_mode <> '' AND octet_length(access_mode) <= 255),
    volume_mode text NOT NULL CHECK (volume_mode <> '' AND octet_length(volume_mode) <= 255),
    encryption_class text NOT NULL CHECK (encryption_class <> '' AND octet_length(encryption_class) <= 255),
    storage_profile text NOT NULL CHECK (storage_profile <> '' AND octet_length(storage_profile) <= 255),
    config_digest text NOT NULL CHECK (config_digest ~ '^sha256:[0-9a-f]{64}$'),
    source_type text NOT NULL CHECK (source_type <> '' AND octet_length(source_type) <= 255),
    source_reference text NOT NULL CHECK (source_reference <> '' AND octet_length(source_reference) <= 2048),
    provenance jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'object' AND octet_length(provenance::text) <= 65536),
    provenance_digest text NOT NULL CHECK (provenance_digest ~ '^sha256:[0-9a-f]{64}$'),
    create_operation_id uuid NOT NULL,
    delete_operation_id uuid,
    cleanup_state text,
    retention_disposition text NOT NULL CHECK (retention_disposition <> '' AND octet_length(retention_disposition) <= 255),
    current_generation bigint CHECK (current_generation >= 0),
    current_workspace_generation_id uuid,
    current_attachment_id uuid,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamptz,
    PRIMARY KEY (tenant_id, workspace_id),
    UNIQUE (tenant_id, session_id),
    UNIQUE (tenant_id, workspace_id, session_id),
    UNIQUE (tenant_id, provider_kind, provider_reference),
    UNIQUE (tenant_id, create_operation_id),
    FOREIGN KEY (tenant_id, session_id) REFERENCES sessions (tenant_id, session_id),
    CHECK ((current_generation IS NULL) = (current_workspace_generation_id IS NULL)),
    CHECK (state NOT IN ('READY', 'ATTACHED', 'SNAPSHOTTING') OR current_generation IS NOT NULL),
    CHECK ((state = 'DELETED') = (deleted_at IS NOT NULL)),
    CHECK (updated_at >= created_at),
    CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

ALTER TABLE attempts ADD UNIQUE (tenant_id, attempt_id, execution_id, execution_generation);

CREATE TABLE workspace_generations (
    tenant_id uuid NOT NULL,
    workspace_generation_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 0),
    parent_workspace_generation_id uuid,
    parent_generation bigint,
    operation_id uuid NOT NULL,
    provider_snapshot_reference text CHECK (provider_snapshot_reference IS NULL OR (provider_snapshot_reference <> '' AND octet_length(provider_snapshot_reference) <= 2048)),
    integrity_algorithm text NOT NULL CHECK (integrity_algorithm <> '' AND octet_length(integrity_algorithm) <= 255),
    integrity_root text NOT NULL CHECK (integrity_root ~ '^sha256:[0-9a-f]{64}$'),
    manifest_digest text NOT NULL CHECK (manifest_digest ~ '^sha256:[0-9a-f]{64}$'),
    logical_bytes bigint NOT NULL CHECK (logical_bytes >= 0),
    logical_files bigint NOT NULL CHECK (logical_files >= 0),
    creator_execution_id uuid,
    creator_attempt_id uuid,
    creator_execution_generation bigint CHECK (creator_execution_generation > 0),
    storage_evidence jsonb NOT NULL CHECK (jsonb_typeof(storage_evidence) = 'object' AND octet_length(storage_evidence::text) <= 65536),
    storage_evidence_digest text NOT NULL CHECK (storage_evidence_digest ~ '^sha256:[0-9a-f]{64}$'),
    classification text NOT NULL CHECK (classification = 'CONFIDENTIAL'),
    retention_disposition text NOT NULL CHECK (retention_disposition <> '' AND octet_length(retention_disposition) <= 255),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, workspace_generation_id),
    UNIQUE (tenant_id, workspace_id, generation),
    UNIQUE (tenant_id, workspace_id, workspace_generation_id, generation),
    UNIQUE (tenant_id, workspace_id, operation_id),
    FOREIGN KEY (tenant_id, workspace_id, session_id) REFERENCES workspaces (tenant_id, workspace_id, session_id),
    FOREIGN KEY (tenant_id, workspace_id, parent_workspace_generation_id, parent_generation)
        REFERENCES workspace_generations (tenant_id, workspace_id, workspace_generation_id, generation)
        DEFERRABLE INITIALLY DEFERRED,
    CHECK ((generation = 0) = (parent_workspace_generation_id IS NULL)),
    CHECK ((parent_workspace_generation_id IS NULL) = (parent_generation IS NULL)),
    CHECK (parent_generation IS NULL OR parent_generation = generation - 1),
    CHECK ((creator_execution_id IS NULL) = (creator_attempt_id IS NULL)),
    CHECK ((creator_execution_id IS NULL) = (creator_execution_generation IS NULL)),
    FOREIGN KEY (tenant_id, creator_execution_id, session_id, creator_execution_generation)
        REFERENCES executions (tenant_id, execution_id, session_id, session_generation),
    FOREIGN KEY (tenant_id, creator_attempt_id, creator_execution_id, creator_execution_generation)
        REFERENCES attempts (tenant_id, attempt_id, execution_id, execution_generation)
);

ALTER TABLE workspaces ADD CONSTRAINT workspaces_current_generation_fk
    FOREIGN KEY (tenant_id, workspace_id, current_workspace_generation_id, current_generation)
    REFERENCES workspace_generations (tenant_id, workspace_id, workspace_generation_id, generation)
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX workspaces_tenant_state_updated_idx ON workspaces (tenant_id, state, updated_at, workspace_id);
CREATE INDEX workspace_generations_tenant_session_created_idx ON workspace_generations (tenant_id, session_id, created_at, workspace_generation_id);

ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;
CREATE POLICY workspaces_tenant_isolation ON workspaces
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);
ALTER TABLE workspace_generations ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_generations FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_generations_tenant_isolation ON workspace_generations
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_workspace_identity_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.session_id IS DISTINCT FROM OLD.session_id OR NEW.provider_kind IS DISTINCT FROM OLD.provider_kind
       OR NEW.provider_reference IS DISTINCT FROM OLD.provider_reference OR NEW.mount_path IS DISTINCT FROM OLD.mount_path
       OR NEW.capacity_bytes IS DISTINCT FROM OLD.capacity_bytes OR NEW.access_mode IS DISTINCT FROM OLD.access_mode
       OR NEW.volume_mode IS DISTINCT FROM OLD.volume_mode OR NEW.encryption_class IS DISTINCT FROM OLD.encryption_class
       OR NEW.storage_profile IS DISTINCT FROM OLD.storage_profile OR NEW.config_digest IS DISTINCT FROM OLD.config_digest
       OR NEW.source_type IS DISTINCT FROM OLD.source_type OR NEW.source_reference IS DISTINCT FROM OLD.source_reference
       OR NEW.provenance IS DISTINCT FROM OLD.provenance OR NEW.provenance_digest IS DISTINCT FROM OLD.provenance_digest
       OR NEW.create_operation_id IS DISTINCT FROM OLD.create_operation_id THEN
        RAISE EXCEPTION 'workspace identity, provider, storage, and source binding are immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.current_generation IS DISTINCT FROM OLD.current_generation THEN
        IF OLD.current_generation IS NOT NULL
           AND (NEW.current_generation IS NULL OR NEW.current_generation - OLD.current_generation <> 1
                OR OLD.state <> 'SNAPSHOTTING' OR NEW.state NOT IN ('READY', 'ATTACHED')) THEN
            RAISE EXCEPTION 'workspace generation must advance by exactly one from snapshotting' USING ERRCODE = '23514';
        END IF;
        IF OLD.current_generation IS NULL
           AND (NEW.current_generation <> 0 OR OLD.state <> 'PROVISIONING' OR NEW.state <> 'READY') THEN
            RAISE EXCEPTION 'workspace initial generation must be zero at readiness' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER workspaces_immutable_binding_and_generation
BEFORE UPDATE ON workspaces FOR EACH ROW EXECUTE FUNCTION reject_workspace_identity_change();

CREATE FUNCTION reject_workspace_generation_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'workspace generations are immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER workspace_generations_immutable
BEFORE UPDATE ON workspace_generations FOR EACH ROW EXECUTE FUNCTION reject_workspace_generation_update();
