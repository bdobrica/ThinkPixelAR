-- Session creation has state_version zero. Only its first creation event may
-- carry zero; all subsequent events retain the positive-version invariant.
ALTER TABLE runtime_events DROP CONSTRAINT runtime_events_aggregate_version_check;
ALTER TABLE runtime_events ADD CONSTRAINT runtime_events_aggregate_version_check
CHECK (aggregate_version > 0 OR (aggregate_version = 0 AND event_type = 'session.created'
    AND sequence = 1 AND execution_id IS NULL AND attempt_id IS NULL));
