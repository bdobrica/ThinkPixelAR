-- Serialize the fence with lifecycle changes, including direct SQL writers.
CREATE OR REPLACE FUNCTION enforce_attempt_mutation_fence() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    fence_is_current boolean;
BEGIN
    -- Acquire parent locks in Session -> Execution order. A plain snapshot read
    -- can accept ACTIVE while a concurrent transaction commits DEGRADED.
    PERFORM 1 FROM sessions s
      WHERE s.tenant_id = NEW.tenant_id AND s.session_id = (
        SELECT e.session_id FROM executions e
        WHERE e.tenant_id = NEW.tenant_id AND e.execution_id = NEW.execution_id)
      FOR UPDATE;
    PERFORM 1 FROM executions e
      WHERE e.tenant_id = NEW.tenant_id AND e.execution_id = NEW.execution_id
      FOR UPDATE;

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
