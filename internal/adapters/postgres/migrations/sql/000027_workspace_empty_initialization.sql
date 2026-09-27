ALTER TABLE workspace_storage_operations
 ADD COLUMN initializer_reference text CHECK (initializer_reference IS NULL OR (octet_length(initializer_reference) BETWEEN 1 AND 2048)),
 ADD COLUMN initializer_spec text CHECK (initializer_spec ~ '^sha256:[0-9a-f]{64}$'),
 ADD COLUMN empty_proof jsonb CHECK (jsonb_typeof(empty_proof)='object' AND octet_length(empty_proof::text)<=16384);
CREATE FUNCTION protect_workspace_initialization() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.initializer_reference IS NOT NULL AND NEW.initializer_reference IS DISTINCT FROM OLD.initializer_reference)
 OR (OLD.initializer_spec IS NOT NULL AND NEW.initializer_spec IS DISTINCT FROM OLD.initializer_spec)
 OR (OLD.empty_proof IS NOT NULL AND NEW.empty_proof IS DISTINCT FROM OLD.empty_proof)
 THEN RAISE EXCEPTION 'immutable workspace initialization' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER workspace_initialization_immutable BEFORE UPDATE ON workspace_storage_operations
 FOR EACH ROW EXECUTE FUNCTION protect_workspace_initialization();
