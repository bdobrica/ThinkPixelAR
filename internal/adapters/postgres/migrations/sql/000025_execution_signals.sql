-- Confidential bodies are separate from replay responses, events and outbox.
CREATE TABLE execution_signals (
 tenant_id uuid NOT NULL,
 signal_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 payload bytea NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 65536),
 payload_digest text NOT NULL CHECK (payload_digest ~ '^sha256:[0-9a-f]{64}$'),
 PRIMARY KEY (tenant_id,signal_id),
 FOREIGN KEY (tenant_id,execution_id) REFERENCES executions(tenant_id,execution_id)
);
ALTER TABLE execution_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE execution_signals FORCE ROW LEVEL SECURITY;
CREATE POLICY execution_signals_tenant_isolation ON execution_signals
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE TRIGGER execution_signals_immutable BEFORE UPDATE ON execution_signals
 FOR EACH ROW EXECUTE FUNCTION reject_runtime_event_mutation();
