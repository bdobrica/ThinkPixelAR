CREATE UNIQUE INDEX executions_one_mutable_per_session_idx
    ON executions (tenant_id, session_id)
    WHERE state IN ('QUEUED', 'MATERIALIZING', 'RUNNING', 'CANCELLING', 'TIMING_OUT');
