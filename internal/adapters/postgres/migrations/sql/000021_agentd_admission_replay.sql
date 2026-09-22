-- Trusted, non-secret materialization metadata and dispatch bookkeeping only.
CREATE TABLE agentd_admission (
 tenant_id uuid NOT NULL,
 sandbox_binding_id uuid NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=98304),
 revoked boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,sandbox_binding_id),
 FOREIGN KEY(tenant_id,sandbox_binding_id) REFERENCES sandbox_bindings(tenant_id,sandbox_binding_id)
);
CREATE TABLE agentd_commands (
 tenant_id uuid NOT NULL,
 sandbox_binding_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 request_digest text NOT NULL CHECK(request_digest ~ '^sha256:[0-9a-f]{64}$'),
 connection_id uuid NOT NULL,
 connection_epoch bigint NOT NULL CHECK(connection_epoch>0),
 message_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 outcome text NOT NULL DEFAULT 'PENDING' CHECK(outcome IN ('PENDING','ACKNOWLEDGED','UNKNOWN')),
 PRIMARY KEY(tenant_id,operation_id),
 FOREIGN KEY(tenant_id,sandbox_binding_id) REFERENCES agentd_admission(tenant_id,sandbox_binding_id)
);
CREATE TABLE agentd_frame_sequences (
 tenant_id uuid NOT NULL,
 sandbox_binding_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 direction text NOT NULL CHECK(direction IN ('AR','AGENTD')),
 sequence bigint NOT NULL CHECK(sequence>0),
 frame_digest text NOT NULL CHECK(frame_digest ~ '^sha256:[0-9a-f]{64}$'),
 PRIMARY KEY(tenant_id,sandbox_binding_id,connection_id,direction),
 FOREIGN KEY(tenant_id,sandbox_binding_id) REFERENCES agentd_admission(tenant_id,sandbox_binding_id)
);
ALTER TABLE agentd_admission ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_admission FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_admission_tenant ON agentd_admission USING(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
ALTER TABLE agentd_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_commands FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_commands_tenant ON agentd_commands USING(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
ALTER TABLE agentd_frame_sequences ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_frame_sequences FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_frame_sequences_tenant ON agentd_frame_sequences USING(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION enforce_agentd_policy_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'agentd policy history is retained' USING ERRCODE='23514'; END IF;
 IF TG_TABLE_NAME='agentd_admission' THEN
  IF (to_jsonb(NEW)-'revoked') IS DISTINCT FROM (to_jsonb(OLD)-'revoked') OR (OLD.revoked AND NOT NEW.revoked) THEN
   RAISE EXCEPTION 'agentd admission is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  IF (to_jsonb(NEW)-'outcome') IS DISTINCT FROM (to_jsonb(OLD)-'outcome') OR (OLD.outcome<>'PENDING' AND NEW.outcome<>OLD.outcome) THEN
   RAISE EXCEPTION 'agentd command outcome is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER agentd_admission_history BEFORE UPDATE OR DELETE ON agentd_admission FOR EACH ROW EXECUTE FUNCTION enforce_agentd_policy_history();
CREATE TRIGGER agentd_command_history BEFORE UPDATE OR DELETE ON agentd_commands FOR EACH ROW EXECUTE FUNCTION enforce_agentd_policy_history();
