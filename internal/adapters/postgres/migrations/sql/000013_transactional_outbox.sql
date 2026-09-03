CREATE TABLE outbox_messages (
    tenant_id uuid NOT NULL,
    message_id uuid NOT NULL,
    topic text NOT NULL CHECK (topic <> '' AND octet_length(topic) <= 255),
    schema_version text NOT NULL CHECK (schema_version <> '' AND octet_length(schema_version) <= 128),
    event_id uuid NOT NULL,
    aggregate_type text NOT NULL CHECK (aggregate_type <> '' AND octet_length(aggregate_type) <= 64),
    aggregate_id uuid NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    payload jsonb,
    payload_reference text CHECK (payload_reference IS NULL OR (payload_reference <> '' AND octet_length(payload_reference) <= 2048)),
    payload_digest text NOT NULL CHECK (payload_digest ~ '^sha256:[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN ('PENDING', 'CLAIMED', 'DELIVERED', 'DEAD_LETTERED')),
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claim_owner_id uuid,
    claim_fence bigint NOT NULL DEFAULT 0 CHECK (claim_fence >= 0),
    claim_expires_at timestamptz,
    available_at timestamptz NOT NULL,
    last_error_code text CHECK (last_error_code IS NULL OR (last_error_code <> '' AND octet_length(last_error_code) <= 128)),
    dead_letter_reason_code text CHECK (dead_letter_reason_code IS NULL OR (dead_letter_reason_code <> '' AND octet_length(dead_letter_reason_code) <= 128)),
    dead_letter_detail text CHECK (dead_letter_detail IS NULL OR octet_length(dead_letter_detail) <= 1024),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    delivered_at timestamptz,
    PRIMARY KEY (tenant_id, message_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    UNIQUE (tenant_id, topic, event_id),
    CHECK ((payload IS NOT NULL) <> (payload_reference IS NOT NULL)),
    CHECK (payload IS NULL OR (jsonb_typeof(payload) IS NOT NULL AND octet_length(payload::text) BETWEEN 2 AND 65536)),
    CHECK (updated_at >= created_at),
    CHECK (
        (state = 'PENDING' AND claim_owner_id IS NULL AND claim_expires_at IS NULL AND delivered_at IS NULL AND dead_letter_reason_code IS NULL AND dead_letter_detail IS NULL)
        OR
        (state = 'CLAIMED' AND attempts > 0 AND claim_fence > 0 AND claim_owner_id IS NOT NULL AND claim_expires_at > updated_at AND delivered_at IS NULL AND dead_letter_reason_code IS NULL AND dead_letter_detail IS NULL)
        OR
        (state = 'DELIVERED' AND claim_owner_id IS NULL AND claim_expires_at IS NULL AND delivered_at IS NOT NULL AND dead_letter_reason_code IS NULL AND dead_letter_detail IS NULL)
        OR
        (state = 'DEAD_LETTERED' AND claim_owner_id IS NULL AND claim_expires_at IS NULL AND delivered_at IS NULL AND dead_letter_reason_code IS NOT NULL)
    )
);

CREATE INDEX outbox_messages_available_idx ON outbox_messages (tenant_id, available_at, message_id)
    WHERE state = 'PENDING';
CREATE INDEX outbox_messages_expired_claim_idx ON outbox_messages (tenant_id, claim_expires_at, message_id)
    WHERE state = 'CLAIMED';
CREATE INDEX outbox_messages_dead_letter_idx ON outbox_messages (tenant_id, updated_at, message_id)
    WHERE state = 'DEAD_LETTERED';

ALTER TABLE outbox_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY outbox_messages_tenant_isolation ON outbox_messages
    USING (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('thinkpixelar.tenant_id', true), '')::uuid);

CREATE FUNCTION enforce_outbox_message_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.message_id IS DISTINCT FROM OLD.message_id
       OR NEW.topic IS DISTINCT FROM OLD.topic
       OR NEW.schema_version IS DISTINCT FROM OLD.schema_version
       OR NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.aggregate_type IS DISTINCT FROM OLD.aggregate_type
       OR NEW.aggregate_id IS DISTINCT FROM OLD.aggregate_id
       OR NEW.aggregate_version IS DISTINCT FROM OLD.aggregate_version
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.payload_reference IS DISTINCT FROM OLD.payload_reference
       OR NEW.payload_digest IS DISTINCT FROM OLD.payload_digest
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'outbox semantic identity and envelope are immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.state IN ('DELIVERED', 'DEAD_LETTERED') THEN
        RAISE EXCEPTION 'terminal outbox message is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.claim_fence < OLD.claim_fence OR NEW.claim_fence > OLD.claim_fence + 1 THEN
        RAISE EXCEPTION 'outbox claim fence must advance by at most one' USING ERRCODE = '23514';
    END IF;
    IF NEW.state = 'CLAIMED' AND NEW.claim_fence <> OLD.claim_fence + 1 THEN
        RAISE EXCEPTION 'outbox claim requires a new fence' USING ERRCODE = '23514';
    END IF;
    IF OLD.state = 'CLAIMED' AND NEW.state = 'CLAIMED' AND CURRENT_TIMESTAMP < OLD.claim_expires_at THEN
        RAISE EXCEPTION 'active outbox claim cannot be taken over' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbox_messages_fenced_mutation BEFORE UPDATE ON outbox_messages
FOR EACH ROW EXECUTE FUNCTION enforce_outbox_message_mutation();
