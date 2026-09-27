CREATE TABLE local_authority_grants (
    tenant_id uuid NOT NULL,
    grant_id uuid NOT NULL,
    session_id uuid NOT NULL,
    snapshot bytea NOT NULL CHECK (octet_length(snapshot) BETWEEN 1 AND 65536),
    snapshot_digest text NOT NULL CHECK (snapshot_digest ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'ACTIVE' CHECK (state IN ('ACTIVE','CANCELLED','EXPIRED')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    terminal_at timestamptz,
    PRIMARY KEY (tenant_id,grant_id),
    FOREIGN KEY (tenant_id,session_id) REFERENCES sessions(tenant_id,session_id),
    CHECK ((state='ACTIVE' AND state_version=0 AND terminal_at IS NULL) OR
           (state<>'ACTIVE' AND state_version=1 AND terminal_at IS NOT NULL))
);
ALTER TABLE local_authority_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE local_authority_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY local_authority_grants_tenant_isolation ON local_authority_grants
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_local_grant_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'local grant history is retained' USING ERRCODE='23514'; END IF;
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.grant_id IS DISTINCT FROM OLD.grant_id
     OR NEW.session_id IS DISTINCT FROM OLD.session_id OR NEW.snapshot IS DISTINCT FROM OLD.snapshot
     OR NEW.snapshot_digest IS DISTINCT FROM OLD.snapshot_digest THEN
    RAISE EXCEPTION 'local grant snapshot is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.state <> 'ACTIVE' OR NEW.state NOT IN ('CANCELLED','EXPIRED') OR NEW.state_version <> OLD.state_version+1 THEN
    RAISE EXCEPTION 'local grant status cannot be renewed' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER local_grant_immutable BEFORE UPDATE OR DELETE ON local_authority_grants
FOR EACH ROW EXECUTE FUNCTION enforce_local_grant_mutation();
