CREATE TABLE workspace_checkpoint_operations (
    tenant_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object' AND octet_length(request::text) <= 8192),
    request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    prior_state text NOT NULL CHECK (prior_state IN ('READY','ATTACHED')),
    state text NOT NULL CHECK (state IN ('PREPARED','COMMITTED','ABORTED')),
    proof bytea CHECK (octet_length(proof) BETWEEN 2 AND 65536),
    proof_digest text CHECK (proof_digest ~ '^sha256:[0-9a-f]{64}$'),
    PRIMARY KEY (tenant_id, operation_id),
    FOREIGN KEY (tenant_id,workspace_id,session_id) REFERENCES workspaces (tenant_id,workspace_id,session_id),
    CHECK ((state = 'COMMITTED') = (proof_digest IS NOT NULL)),
    CHECK ((state = 'COMMITTED') = (proof IS NOT NULL))
);
CREATE UNIQUE INDEX workspace_checkpoint_one_prepared ON workspace_checkpoint_operations(tenant_id,workspace_id) WHERE state='PREPARED';
ALTER TABLE workspace_checkpoint_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_checkpoint_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_checkpoint_tenant_isolation ON workspace_checkpoint_operations
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_workspace_checkpoint_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id OR NEW.session_id IS DISTINCT FROM OLD.session_id
       OR NEW.request IS DISTINCT FROM OLD.request OR NEW.request_digest IS DISTINCT FROM OLD.request_digest
       OR NEW.prior_state IS DISTINCT FROM OLD.prior_state OR OLD.state <> 'PREPARED'
       OR NEW.state NOT IN ('COMMITTED','ABORTED') THEN
        RAISE EXCEPTION 'immutable checkpoint operation or illegal completion' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER workspace_checkpoint_operation_immutable BEFORE UPDATE ON workspace_checkpoint_operations
    FOR EACH ROW EXECUTE FUNCTION enforce_workspace_checkpoint_operation();
