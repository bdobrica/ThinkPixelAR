-- Immutable historical result; same operation never suspends a later incarnation.
CREATE TABLE session_suspend_operations (
 tenant_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 session_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
 prior_state text NOT NULL CHECK (prior_state IN ('READY','IDLE')),
 state_version bigint NOT NULL CHECK (state_version > 0),
 execution_generation bigint NOT NULL CHECK (execution_generation >= 0),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id,operation_id),
 FOREIGN KEY (tenant_id,session_id) REFERENCES sessions(tenant_id,session_id),
 FOREIGN KEY (tenant_id,session_id,checkpoint_id) REFERENCES checkpoints(tenant_id,session_id,checkpoint_id)
);
ALTER TABLE session_suspend_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_suspend_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY session_suspend_operations_tenant_isolation ON session_suspend_operations
 USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION reject_session_suspend_operation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Session suspend operations are immutable' USING ERRCODE='23514';
END;
$$;
CREATE TRIGGER session_suspend_operations_immutable BEFORE UPDATE OR DELETE ON session_suspend_operations
 FOR EACH ROW EXECUTE FUNCTION reject_session_suspend_operation_mutation();
