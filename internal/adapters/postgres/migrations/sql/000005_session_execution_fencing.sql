ALTER TABLE executions
    ADD UNIQUE (tenant_id, execution_id, session_id, session_generation),
    ADD UNIQUE (tenant_id, session_id, session_generation);

ALTER TABLE sessions
    ADD COLUMN current_execution_id uuid,
    ADD CONSTRAINT sessions_current_execution_fk
        FOREIGN KEY (tenant_id, current_execution_id, session_id, execution_generation)
        REFERENCES executions (tenant_id, execution_id, session_id, session_generation)
        DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION enforce_session_execution_epoch() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.execution_generation < OLD.execution_generation
       OR (NEW.execution_generation > OLD.execution_generation
           AND NEW.execution_generation - OLD.execution_generation <> 1) THEN
        RAISE EXCEPTION 'session execution generation must be monotonic and advance by one'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.execution_generation > OLD.execution_generation
       AND (OLD.state NOT IN ('READY', 'IDLE')
            OR NEW.state <> 'ACTIVE'
            OR NEW.current_execution_id IS NULL) THEN
        RAISE EXCEPTION 'session execution generation advances only on admission'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_execution_id IS NOT NULL
       AND (NEW.state <> 'ACTIVE'
            OR NEW.execution_generation = 0) THEN
        RAISE EXCEPTION 'current execution requires an active session generation'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sessions_execution_epoch
BEFORE UPDATE ON sessions
FOR EACH ROW EXECUTE FUNCTION enforce_session_execution_epoch();

CREATE FUNCTION enforce_attempt_mutation_fence() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    fence_is_current boolean;
BEGIN
    SELECT s.state = 'ACTIVE'
           AND s.current_execution_id = e.execution_id
           AND s.execution_generation = e.session_generation
           AND e.session_generation = NEW.execution_generation
           AND e.state IN ('MATERIALIZING', 'RUNNING', 'CANCELLING', 'TIMING_OUT')
           AND ((TG_OP = 'INSERT' AND NEW.is_current)
                OR (TG_OP = 'UPDATE' AND OLD.is_current))
      INTO fence_is_current
      FROM executions e
      JOIN sessions s
        ON s.tenant_id = e.tenant_id
       AND s.session_id = e.session_id
     WHERE e.tenant_id = NEW.tenant_id
       AND e.execution_id = NEW.execution_id;

    IF fence_is_current IS DISTINCT FROM true THEN
        RAISE EXCEPTION 'stale attempt mutation fence'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attempts_current_mutation_fence
BEFORE INSERT OR UPDATE ON attempts
FOR EACH ROW EXECUTE FUNCTION enforce_attempt_mutation_fence();
