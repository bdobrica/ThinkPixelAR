CREATE TABLE reconciliation_work (
    tenant_id uuid NOT NULL,
    work_id uuid NOT NULL,
    work_kind text NOT NULL CHECK (work_kind <> '' AND octet_length(work_kind) <= 128),
    target_type text NOT NULL CHECK (target_type <> '' AND octet_length(target_type) <= 64),
    target_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('PENDING', 'CLAIMED', 'COMPLETED')),
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claim_owner_id uuid,
    claim_fence bigint NOT NULL DEFAULT 0 CHECK (claim_fence >= 0),
    claim_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL,
    last_error_code text CHECK (last_error_code IS NULL OR (last_error_code <> '' AND octet_length(last_error_code) <= 128)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, work_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    UNIQUE (tenant_id, work_kind, target_type, target_id),
    CHECK (updated_at >= created_at),
    CHECK (
      (state='PENDING' AND claim_owner_id IS NULL AND claim_expires_at IS NULL AND completed_at IS NULL) OR
      (state='CLAIMED' AND attempts > 0 AND claim_fence > 0 AND claim_owner_id IS NOT NULL AND claim_expires_at > updated_at AND completed_at IS NULL) OR
      (state='COMPLETED' AND attempts > 0 AND claim_fence > 0 AND claim_owner_id IS NULL AND claim_expires_at IS NULL AND completed_at = updated_at)
    )
);

CREATE INDEX reconciliation_work_available_idx ON reconciliation_work (tenant_id, next_attempt_at, work_id) WHERE state='PENDING';
CREATE INDEX reconciliation_work_expired_claim_idx ON reconciliation_work (tenant_id, claim_expires_at, work_id) WHERE state='CLAIMED';

ALTER TABLE reconciliation_work ENABLE ROW LEVEL SECURITY;
ALTER TABLE reconciliation_work FORCE ROW LEVEL SECURITY;
CREATE POLICY reconciliation_work_tenant_isolation ON reconciliation_work
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_reconciliation_work_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.work_id IS DISTINCT FROM OLD.work_id
     OR NEW.work_kind IS DISTINCT FROM OLD.work_kind OR NEW.target_type IS DISTINCT FROM OLD.target_type
     OR NEW.target_id IS DISTINCT FROM OLD.target_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'reconciliation work identity is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.state='COMPLETED' THEN RAISE EXCEPTION 'completed reconciliation work is immutable' USING ERRCODE='23514'; END IF;
  IF NEW.claim_fence < OLD.claim_fence OR NEW.claim_fence > OLD.claim_fence + 1 THEN
    RAISE EXCEPTION 'reconciliation claim fence must advance by at most one' USING ERRCODE='23514';
  END IF;
  IF NEW.state='CLAIMED' AND OLD.state <> 'CLAIMED' AND NEW.claim_fence <> OLD.claim_fence + 1 THEN
    RAISE EXCEPTION 'reconciliation claim requires a new fence' USING ERRCODE='23514';
  END IF;
  IF OLD.state='CLAIMED' AND NEW.state='CLAIMED' AND NEW.claim_fence = OLD.claim_fence
     AND NEW.claim_owner_id IS DISTINCT FROM OLD.claim_owner_id THEN
    RAISE EXCEPTION 'reconciliation claim owner requires a new fence' USING ERRCODE='23514';
  END IF;
  IF NEW.state <> 'CLAIMED' AND NEW.claim_fence <> OLD.claim_fence THEN
    RAISE EXCEPTION 'reconciliation completion or reschedule preserves claim fence' USING ERRCODE='23514';
  END IF;
  IF OLD.state='CLAIMED' AND NEW.state='CLAIMED' AND NEW.claim_fence = OLD.claim_fence + 1
     AND NEW.updated_at < OLD.claim_expires_at THEN
    RAISE EXCEPTION 'active reconciliation claim cannot be taken over' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER reconciliation_work_fenced_mutation BEFORE UPDATE ON reconciliation_work
FOR EACH ROW EXECUTE FUNCTION enforce_reconciliation_work_mutation();
