-- Preserve the current Execution during recovery; Attempt mutation still requires ACTIVE.
CREATE OR REPLACE FUNCTION enforce_session_execution_epoch() RETURNS trigger
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
       AND ((NEW.state <> 'ACTIVE' AND NOT (NEW.state = 'DEGRADED'
                 AND NEW.recovery_state = 'ACTIVE'
                 AND NEW.current_execution_id IS NOT DISTINCT FROM OLD.current_execution_id
                 AND NEW.execution_generation = OLD.execution_generation))
            OR NEW.execution_generation = 0) THEN
        RAISE EXCEPTION 'current execution requires an active session generation'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
