-- SQL expressions can resolve record fields before Boolean short-circuiting.
-- Branch by table before preparing expressions for that table-specific record.
CREATE OR REPLACE FUNCTION reject_runtime_binding_identity_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.session_id IS DISTINCT FROM OLD.session_id
       OR NEW.execution_id IS DISTINCT FROM OLD.execution_id OR NEW.execution_generation IS DISTINCT FROM OLD.execution_generation
       OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR NEW.attempt_no IS DISTINCT FROM OLD.attempt_no THEN
        RAISE EXCEPTION 'runtime binding ownership and attempt fence are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'sandbox_bindings' THEN
        IF (NEW.sandbox_binding_id IS DISTINCT FROM OLD.sandbox_binding_id
       OR NEW.provider_kind IS DISTINCT FROM OLD.provider_kind
       OR (OLD.provider_reference IS NOT NULL AND NEW.provider_reference IS DISTINCT FROM OLD.provider_reference)
       OR NEW.resolution_digest IS DISTINCT FROM OLD.resolution_digest OR NEW.acquire_operation_id IS DISTINCT FROM OLD.acquire_operation_id
       OR NEW.acquire_request_digest IS DISTINCT FROM OLD.acquire_request_digest) THEN
        RAISE EXCEPTION 'sandbox binding identity is immutable' USING ERRCODE = '23514';
        END IF;
    END IF;
    IF TG_TABLE_NAME = 'sandbox_bindings' THEN
        IF (
       (OLD.suspend_operation_id IS NOT NULL AND (NEW.suspend_operation_id IS DISTINCT FROM OLD.suspend_operation_id OR NEW.suspend_request_digest IS DISTINCT FROM OLD.suspend_request_digest))
       OR (OLD.resume_operation_id IS NOT NULL AND (NEW.resume_operation_id IS DISTINCT FROM OLD.resume_operation_id OR NEW.resume_request_digest IS DISTINCT FROM OLD.resume_request_digest))
       OR (OLD.release_operation_id IS NOT NULL AND (NEW.release_operation_id IS DISTINCT FROM OLD.release_operation_id OR NEW.release_request_digest IS DISTINCT FROM OLD.release_request_digest))) THEN
        RAISE EXCEPTION 'established sandbox operation identities are immutable' USING ERRCODE = '23514';
        END IF;
    END IF;
    IF TG_TABLE_NAME = 'harness_bindings' THEN
        IF (NEW.harness_binding_id IS DISTINCT FROM OLD.harness_binding_id
       OR NEW.sandbox_binding_id IS DISTINCT FROM OLD.sandbox_binding_id OR NEW.adapter_kind IS DISTINCT FROM OLD.adapter_kind
       OR NEW.adapter_version IS DISTINCT FROM OLD.adapter_version OR NEW.adapter_build_digest IS DISTINCT FROM OLD.adapter_build_digest
       OR NEW.negotiation_digest IS DISTINCT FROM OLD.negotiation_digest OR NEW.protocol_name IS DISTINCT FROM OLD.protocol_name
       OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version OR NEW.process_reference IS DISTINCT FROM OLD.process_reference
       OR NEW.vendor_session_reference IS DISTINCT FROM OLD.vendor_session_reference OR NEW.start_operation_id IS DISTINCT FROM OLD.start_operation_id
       OR NEW.start_request_digest IS DISTINCT FROM OLD.start_request_digest) THEN
        RAISE EXCEPTION 'harness binding identity is immutable' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
