-- Only non-secret bootstrap identity is durable. No key, proof or bundle bytes.
CREATE TABLE agentd_bootstrap_delivery (
 tenant_id uuid NOT NULL,
 credential_id uuid NOT NULL,
 reference jsonb NOT NULL CHECK (jsonb_typeof(reference)='object' AND octet_length(reference::text)<=16384),
 provider_uid text NOT NULL DEFAULT '' CHECK (length(provider_uid)<=256),
 expires_at timestamptz NOT NULL,
 retry_after timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 cleanup_requested boolean NOT NULL DEFAULT false,
 cleaned boolean NOT NULL DEFAULT false,
 PRIMARY KEY (tenant_id,credential_id),
 FOREIGN KEY (tenant_id,credential_id) REFERENCES agentd_credentials(tenant_id,credential_id),
 CHECK (NOT cleaned OR (cleanup_requested AND provider_uid<>''))
);
ALTER TABLE agentd_bootstrap_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE agentd_bootstrap_delivery FORCE ROW LEVEL SECURITY;
CREATE POLICY agentd_bootstrap_delivery_tenant_isolation ON agentd_bootstrap_delivery
 USING (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('thinkpixelar.tenant_id',true),'')::uuid);
CREATE INDEX agentd_bootstrap_delivery_pending ON agentd_bootstrap_delivery(tenant_id,expires_at,credential_id) WHERE NOT cleaned;
CREATE FUNCTION enforce_agentd_delivery_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  RAISE EXCEPTION 'bootstrap delivery history is retained' USING ERRCODE='23514';
 END IF;
 IF (NEW.tenant_id,NEW.credential_id,NEW.reference,NEW.expires_at) IS DISTINCT FROM
    (OLD.tenant_id,OLD.credential_id,OLD.reference,OLD.expires_at)
    OR (OLD.provider_uid<>'' AND NEW.provider_uid<>OLD.provider_uid)
    OR (OLD.cleanup_requested AND NOT NEW.cleanup_requested)
    OR (OLD.cleaned AND NOT NEW.cleaned) THEN
  RAISE EXCEPTION 'bootstrap delivery identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER agentd_bootstrap_delivery_immutable BEFORE UPDATE OR DELETE ON agentd_bootstrap_delivery
 FOR EACH ROW EXECUTE FUNCTION enforce_agentd_delivery_history();
