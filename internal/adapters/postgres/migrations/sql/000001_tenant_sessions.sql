CREATE TABLE tenants (
    tenant_id uuid PRIMARY KEY,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
    security_epoch bigint NOT NULL DEFAULT 0 CHECK (security_epoch >= 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (updated_at >= created_at)
);

CREATE TABLE sessions (
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN (
        'PROVISIONING', 'READY', 'ACTIVE', 'IDLE', 'SUSPENDED', 'DEGRADED', 'CLOSING', 'CLOSED'
    )),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    execution_generation bigint NOT NULL DEFAULT 0 CHECK (execution_generation >= 0),
    authority_mode text NOT NULL CHECK (authority_mode IN ('LOCAL', 'THINKPIXEL_AG')),
    authority_namespace text NOT NULL CHECK (authority_namespace <> ''),
    agent_id text NOT NULL CHECK (agent_id <> '' AND octet_length(agent_id) <= 255),
    agent_version_id text NOT NULL CHECK (agent_version_id <> '' AND octet_length(agent_version_id) <= 255),
    runtime_spec_schema_version text NOT NULL CHECK (runtime_spec_schema_version <> ''),
    runtime_spec jsonb NOT NULL CHECK (jsonb_typeof(runtime_spec) = 'object' AND octet_length(runtime_spec::text) <= 65536),
    runtime_spec_digest text NOT NULL CHECK (runtime_spec_digest ~ '^sha256:[0-9a-f]{64}$'),
    runtime_profile_schema_version text NOT NULL CHECK (runtime_profile_schema_version <> ''),
    runtime_profile_snapshot jsonb NOT NULL CHECK (jsonb_typeof(runtime_profile_snapshot) = 'object' AND octet_length(runtime_profile_snapshot::text) <= 65536),
    runtime_profile_digest text NOT NULL CHECK (runtime_profile_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at timestamptz,
    PRIMARY KEY (tenant_id, session_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    CHECK (updated_at >= created_at),
    CHECK ((state = 'CLOSED') = (closed_at IS NOT NULL)),
    CHECK (closed_at IS NULL OR closed_at >= created_at)
);

CREATE INDEX sessions_tenant_state_updated_idx ON sessions (tenant_id, state, updated_at, session_id);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_tenant_isolation ON tenants
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY sessions_tenant_isolation ON sessions
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION reject_session_binding_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.session_id IS DISTINCT FROM OLD.session_id
       OR NEW.authority_mode IS DISTINCT FROM OLD.authority_mode
       OR NEW.authority_namespace IS DISTINCT FROM OLD.authority_namespace
       OR NEW.agent_id IS DISTINCT FROM OLD.agent_id
       OR NEW.agent_version_id IS DISTINCT FROM OLD.agent_version_id
       OR NEW.runtime_spec_schema_version IS DISTINCT FROM OLD.runtime_spec_schema_version
       OR NEW.runtime_spec IS DISTINCT FROM OLD.runtime_spec
       OR NEW.runtime_spec_digest IS DISTINCT FROM OLD.runtime_spec_digest
       OR NEW.runtime_profile_schema_version IS DISTINCT FROM OLD.runtime_profile_schema_version
       OR NEW.runtime_profile_snapshot IS DISTINCT FROM OLD.runtime_profile_snapshot
       OR NEW.runtime_profile_digest IS DISTINCT FROM OLD.runtime_profile_digest THEN
        RAISE EXCEPTION 'session identity and runtime binding are immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sessions_immutable_binding
BEFORE UPDATE ON sessions
FOR EACH ROW EXECUTE FUNCTION reject_session_binding_change();
