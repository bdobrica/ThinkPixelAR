-- Only credential digests and finite validity; never private keys or raw proofs.
CREATE TABLE agentd_credential_state (
    tenant_id uuid NOT NULL,
    sandbox_binding_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    latest_digest text CHECK (latest_digest ~ '^sha256:[0-9a-f]{64}$'),
    issued_at timestamptz,
    connection_id uuid,
    connection_epoch bigint NOT NULL DEFAULT 0 CHECK (connection_epoch >= 0),
    connection_digest text CHECK (connection_digest ~ '^sha256:[0-9a-f]{64}$'),
    connection_deadline timestamptz,
    PRIMARY KEY (tenant_id,sandbox_binding_id),
    FOREIGN KEY (tenant_id,attempt_id,sandbox_binding_id) REFERENCES sandbox_bindings (tenant_id,attempt_id,sandbox_binding_id),
    CHECK ((latest_digest IS NULL) = (issued_at IS NULL)),
    CHECK ((connection_id IS NULL) = (connection_digest IS NULL)),
    CHECK ((connection_id IS NULL) = (connection_deadline IS NULL)),
    CHECK (connection_id IS NULL OR connection_epoch > 0)
);
CREATE TABLE agentd_credentials (
    tenant_id uuid NOT NULL,
    sandbox_binding_id uuid NOT NULL,
    credential_id uuid NOT NULL,
    certificate_digest text NOT NULL CHECK (certificate_digest ~ '^sha256:[0-9a-f]{64}$'),
    issuer_digest text NOT NULL CHECK (issuer_digest ~ '^sha256:[0-9a-f]{64}$'),
    proof_digest text CHECK (proof_digest ~ '^sha256:[0-9a-f]{64}$'),
    bootstrap boolean NOT NULL,
    not_before timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    PRIMARY KEY (tenant_id,credential_id),
    UNIQUE (tenant_id,certificate_digest),
    FOREIGN KEY (tenant_id,sandbox_binding_id) REFERENCES agentd_credential_state (tenant_id,sandbox_binding_id),
    CHECK (bootstrap = (proof_digest IS NOT NULL)),
    CHECK (expires_at > not_before),
    CHECK (expires_at-not_before <= CASE WHEN bootstrap THEN interval '10 minutes' ELSE interval '15 minutes' END),
    CHECK (consumed_at IS NULL OR (bootstrap AND consumed_at >= not_before AND consumed_at < expires_at))
);
ALTER TABLE agentd_credential_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_credential_state FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_credential_state_tenant_isolation ON agentd_credential_state
 USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
ALTER TABLE agentd_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_credentials_tenant_isolation ON agentd_credentials
 USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION enforce_agentd_credential_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  RAISE EXCEPTION 'agentd credential history is retained' USING ERRCODE='23514';
 END IF;
 IF (to_jsonb(NEW)-'consumed_at') IS DISTINCT FROM (to_jsonb(OLD)-'consumed_at')
    OR OLD.consumed_at IS NOT NULL OR NEW.consumed_at IS NULL THEN
  RAISE EXCEPTION 'agentd credential identity is immutable and bootstrap consumption is once' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER agentd_credentials_immutable BEFORE UPDATE OR DELETE ON agentd_credentials
 FOR EACH ROW EXECUTE FUNCTION enforce_agentd_credential_history();
