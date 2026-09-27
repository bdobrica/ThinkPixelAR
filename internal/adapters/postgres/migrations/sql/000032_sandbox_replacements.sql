-- One durable replacement decision per lost binding. Provider retries reuse the
-- candidate's immutable acquisition operation; history never authorizes work.
CREATE TABLE sandbox_replacements (
 tenant_id uuid NOT NULL,
 old_sandbox_id uuid NOT NULL,
 new_sandbox_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (tenant_id,old_sandbox_id),
 UNIQUE (tenant_id,new_sandbox_id),
 FOREIGN KEY (tenant_id,old_sandbox_id) REFERENCES sandbox_bindings(tenant_id,sandbox_binding_id),
 FOREIGN KEY (tenant_id,new_sandbox_id) REFERENCES sandbox_bindings(tenant_id,sandbox_binding_id),
 CHECK (old_sandbox_id <> new_sandbox_id)
);
ALTER TABLE sandbox_replacements ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_replacements FORCE ROW LEVEL SECURITY;
CREATE POLICY sandbox_replacements_tenant ON sandbox_replacements
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION retain_sandbox_replacement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'sandbox replacement decisions are immutable' USING ERRCODE='23514';
END;
$$;
CREATE TRIGGER sandbox_replacement_history BEFORE UPDATE OR DELETE ON sandbox_replacements
 FOR EACH ROW EXECUTE FUNCTION retain_sandbox_replacement();
