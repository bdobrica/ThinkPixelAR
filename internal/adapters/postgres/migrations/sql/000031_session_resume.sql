-- Infrastructure candidates deliberately do not reuse Execution grants/Attempts.
CREATE TABLE session_resume_operations (
 tenant_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 session_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL,
 sandbox_id uuid NOT NULL,
 bootstrap_id uuid NOT NULL,
 attachment_id uuid NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
 intent bytea NOT NULL CHECK (octet_length(intent) BETWEEN 2 AND 524288),
 intent_digest text NOT NULL CHECK (intent_digest ~ '^sha256:[0-9a-f]{64}$'),
 state text NOT NULL CHECK (state IN ('RESUMING','READY','IDLE','DEGRADED')),
 result bytea NOT NULL CHECK (octet_length(result) BETWEEN 2 AND 4096),
 observation bytea CHECK (octet_length(observation) BETWEEN 2 AND 8192),
 cleaned boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,operation_id),
 UNIQUE(tenant_id,sandbox_id), UNIQUE(tenant_id,bootstrap_id), UNIQUE(tenant_id,attachment_id),
 FOREIGN KEY(tenant_id,session_id) REFERENCES sessions(tenant_id,session_id),
 FOREIGN KEY(tenant_id,session_id,checkpoint_id) REFERENCES checkpoints(tenant_id,session_id,checkpoint_id),
 CHECK ((state IN ('READY','IDLE')) = (observation IS NOT NULL)),
 CHECK (NOT cleaned OR state='DEGRADED')
);
CREATE UNIQUE INDEX session_resume_one_candidate ON session_resume_operations(tenant_id,session_id) WHERE state='RESUMING' OR (state='DEGRADED' AND NOT cleaned);
ALTER TABLE session_resume_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_resume_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY session_resume_tenant_isolation ON session_resume_operations
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE FUNCTION enforce_session_resume_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'resume history is immutable' USING ERRCODE='23514'; END IF;
 IF (NEW.tenant_id,NEW.operation_id,NEW.session_id,NEW.checkpoint_id,NEW.sandbox_id,NEW.bootstrap_id,NEW.attachment_id,NEW.request_digest,NEW.intent,NEW.intent_digest,NEW.created_at)
 IS DISTINCT FROM (OLD.tenant_id,OLD.operation_id,OLD.session_id,OLD.checkpoint_id,OLD.sandbox_id,OLD.bootstrap_id,OLD.attachment_id,OLD.request_digest,OLD.intent,OLD.intent_digest,OLD.created_at)
 OR NOT ((OLD.state='RESUMING' AND NEW.state IN ('READY','IDLE','DEGRADED') AND NOT NEW.cleaned)
 OR (OLD.state='DEGRADED' AND NEW.state=OLD.state AND NOT OLD.cleaned AND NEW.cleaned AND NEW.result=OLD.result AND NEW.observation IS NOT DISTINCT FROM OLD.observation)) THEN
 RAISE EXCEPTION 'immutable resume identity or illegal transition' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER session_resume_operation_fence BEFORE UPDATE OR DELETE ON session_resume_operations FOR EACH ROW EXECUTE FUNCTION enforce_session_resume_operation();
