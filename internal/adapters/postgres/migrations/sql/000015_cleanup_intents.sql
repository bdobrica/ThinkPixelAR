CREATE TABLE cleanup_intents (
    tenant_id uuid NOT NULL,
    cleanup_intent_id uuid NOT NULL,
    owner_type text NOT NULL CHECK (owner_type <> '' AND octet_length(owner_type) <= 64),
    owner_id uuid NOT NULL,
    target_type text NOT NULL CHECK (target_type <> '' AND octet_length(target_type) <= 64),
    provider_kind text NOT NULL CHECK (provider_kind <> '' AND octet_length(provider_kind) <= 128),
    external_reference text NOT NULL CHECK (external_reference <> '' AND octet_length(external_reference) <= 2048),
    cleanup_operation_id uuid NOT NULL,
    request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    ownership_proof_digest text NOT NULL CHECK (ownership_proof_digest ~ '^sha256:[0-9a-f]{64}$'),
    is_orphan boolean NOT NULL,
    state text NOT NULL CHECK (state IN ('PENDING','CONFIRMED','QUARANTINED')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    last_error_code text CHECK (last_error_code IS NULL OR (last_error_code <> '' AND octet_length(last_error_code) <= 128)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    confirmed_at timestamptz,
    quarantined_at timestamptz,
    PRIMARY KEY (tenant_id, cleanup_intent_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    UNIQUE (tenant_id, cleanup_operation_id),
    UNIQUE (tenant_id, provider_kind, target_type, external_reference),
    CHECK (updated_at >= created_at),
    CHECK (
      (state='PENDING' AND confirmed_at IS NULL AND quarantined_at IS NULL) OR
      (state='CONFIRMED' AND attempts > 0 AND confirmed_at=updated_at AND quarantined_at IS NULL AND last_error_code IS NULL) OR
      (state='QUARANTINED' AND attempts > 0 AND quarantined_at=updated_at AND confirmed_at IS NULL AND last_error_code IS NOT NULL)
    )
);

CREATE INDEX cleanup_intents_retry_idx ON cleanup_intents (tenant_id,next_attempt_at,cleanup_intent_id) WHERE state='PENDING';
CREATE INDEX cleanup_intents_owner_idx ON cleanup_intents (tenant_id,owner_type,owner_id,state);
CREATE INDEX cleanup_intents_orphan_idx ON cleanup_intents (tenant_id,provider_kind,target_type) WHERE is_orphan AND state <> 'CONFIRMED';

ALTER TABLE cleanup_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE cleanup_intents FORCE ROW LEVEL SECURITY;
CREATE POLICY cleanup_intents_tenant_isolation ON cleanup_intents
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_cleanup_intent_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.cleanup_intent_id IS DISTINCT FROM OLD.cleanup_intent_id
     OR NEW.owner_type IS DISTINCT FROM OLD.owner_type OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
     OR NEW.target_type IS DISTINCT FROM OLD.target_type OR NEW.provider_kind IS DISTINCT FROM OLD.provider_kind
     OR NEW.external_reference IS DISTINCT FROM OLD.external_reference
     OR NEW.cleanup_operation_id IS DISTINCT FROM OLD.cleanup_operation_id
     OR NEW.request_digest IS DISTINCT FROM OLD.request_digest
     OR NEW.ownership_proof_digest IS DISTINCT FROM OLD.ownership_proof_digest
     OR NEW.is_orphan IS DISTINCT FROM OLD.is_orphan OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'cleanup target and operation identity are immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.state <> 'PENDING' THEN RAISE EXCEPTION 'terminal cleanup tombstone is immutable' USING ERRCODE='23514'; END IF;
  IF NEW.state_version <> OLD.state_version + 1 OR NEW.attempts <> OLD.attempts + 1 THEN
    RAISE EXCEPTION 'cleanup mutation must advance version and attempts exactly once' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER cleanup_intents_fenced_mutation BEFORE UPDATE ON cleanup_intents
FOR EACH ROW EXECUTE FUNCTION enforce_cleanup_intent_mutation();
