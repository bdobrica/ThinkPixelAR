ALTER TABLE sessions
    ADD COLUMN recovery_state text
        CHECK (recovery_state IN ('PROVISIONING', 'READY', 'ACTIVE', 'IDLE', 'SUSPENDED', 'CLOSING'));

ALTER TABLE sessions
    ADD CONSTRAINT sessions_recovery_state_consistent CHECK (
        (state = 'DEGRADED') = (recovery_state IS NOT NULL)
    );
