-- Historical metadata may lack a publication request; new publisher rows never do.
ALTER TABLE checkpoints ADD COLUMN publication_request_digest text
    CHECK (publication_request_digest ~ '^sha256:[0-9a-f]{64}$');
CREATE FUNCTION preserve_checkpoint_publication_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.publication_request_digest IS DISTINCT FROM OLD.publication_request_digest THEN
        RAISE EXCEPTION 'checkpoint publication request is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER checkpoints_publication_request_immutable BEFORE UPDATE ON checkpoints
    FOR EACH ROW EXECUTE FUNCTION preserve_checkpoint_publication_request();
ALTER TABLE sessions ADD COLUMN current_checkpoint_id uuid,
    ADD CONSTRAINT sessions_current_checkpoint_fk FOREIGN KEY (tenant_id,session_id,current_checkpoint_id)
    REFERENCES checkpoints(tenant_id,session_id,checkpoint_id);
