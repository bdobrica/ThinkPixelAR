-- Confidential input is tenant-scoped and never published in queue/event metadata.
CREATE TABLE execution_inputs (
 tenant_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 input bytea NOT NULL CHECK (octet_length(input) BETWEEN 1 AND 262144),
 input_digest text NOT NULL CHECK (input_digest ~ '^sha256:[0-9a-f]{64}$'),
 PRIMARY KEY (tenant_id,execution_id),
 FOREIGN KEY (tenant_id,execution_id) REFERENCES executions(tenant_id,execution_id)
);
ALTER TABLE execution_inputs ENABLE ROW LEVEL SECURITY;
ALTER TABLE execution_inputs FORCE ROW LEVEL SECURITY;
CREATE POLICY execution_inputs_tenant_isolation ON execution_inputs
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE TRIGGER execution_inputs_immutable BEFORE UPDATE ON execution_inputs
 FOR EACH ROW EXECUTE FUNCTION reject_runtime_event_mutation();

ALTER TABLE runtime_events DROP CONSTRAINT runtime_events_aggregate_version_check;
ALTER TABLE runtime_events ADD CONSTRAINT runtime_events_aggregate_version_check CHECK (
 aggregate_version > 0 OR (aggregate_version=0 AND attempt_id IS NULL AND (
 (event_type='session.created' AND sequence=1 AND execution_id IS NULL) OR
 (event_type='execution.accepted' AND execution_id IS NOT NULL))));
