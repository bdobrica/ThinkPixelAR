-- Provider observations do not publish Workspace generations or grant writers.
CREATE TABLE workspace_storage_operations (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 create_operation_id uuid NOT NULL,
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object' AND octet_length(request::text)<=16384),
 workspace_reference text,
 state_reference text,
 delete_operation_id uuid,
 delete_digest text CHECK (delete_digest ~ '^sha256:[0-9a-f]{64}$'),
 PRIMARY KEY (tenant_id,workspace_id),
 UNIQUE (tenant_id,create_operation_id),
 UNIQUE (tenant_id,delete_operation_id),
 FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,workspace_id),
 CHECK ((delete_operation_id IS NULL)=(delete_digest IS NULL)),
 CHECK (workspace_reference IS NULL OR (workspace_reference<>'' AND octet_length(workspace_reference)<=2048)),
 CHECK (state_reference IS NULL OR (state_reference<>'' AND octet_length(state_reference)<=2048))
);
ALTER TABLE workspace_storage_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_storage_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_storage_operations_tenant ON workspace_storage_operations
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION protect_workspace_storage_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
 OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
 OR NEW.create_operation_id IS DISTINCT FROM OLD.create_operation_id
 OR NEW.request IS DISTINCT FROM OLD.request
 OR (OLD.workspace_reference IS NOT NULL AND NEW.workspace_reference IS DISTINCT FROM OLD.workspace_reference)
 OR (OLD.state_reference IS NOT NULL AND NEW.state_reference IS DISTINCT FROM OLD.state_reference)
 OR (OLD.delete_operation_id IS NOT NULL AND (NEW.delete_operation_id IS DISTINCT FROM OLD.delete_operation_id OR NEW.delete_digest IS DISTINCT FROM OLD.delete_digest))
 THEN RAISE EXCEPTION 'immutable workspace storage operation' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER workspace_storage_operation_immutable BEFORE UPDATE ON workspace_storage_operations
 FOR EACH ROW EXECUTE FUNCTION protect_workspace_storage_operation();
