-- +goose Up

-- 同版本更新在一次调用内提交；无法快速处理时不写数据，由应用回退。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tripfolio_private.patch_item(
    p_account_id uuid,
    p_operation_id uuid,
    p_operation_type text,
    p_request_hash bytea,
    p_entity_type text,
    p_trip_id uuid,
    p_entity_id uuid,
    p_base_version bigint,
    p_patch jsonb,
    p_fields text[]
) RETURNS jsonb
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
SET search_path = pg_catalog, public
SET timezone = 'UTC'
AS $$
DECLARE
    v_last_seq bigint;
    v_next_seq bigint;
    v_account_status text;
    v_request_hash bytea;
    v_result jsonb;
    v_replayed boolean;
    v_has_resource boolean;
    v_snapshot jsonb;
    v_primary jsonb;
    v_version bigint;
    v_now timestamptz;
    v_packing public.packing_items%ROWTYPE;
    v_todo public.todo_items%ROWTYPE;
BEGIN
    IF NOT (
        (p_entity_type = 'packing_item' AND p_operation_type = 'packing.update')
        OR (p_entity_type = 'todo' AND p_operation_type = 'todo.update')
    ) OR p_entity_type IS NULL OR p_operation_type IS NULL THEN
        RETURN NULL;
    END IF;

    -- 锁后单独读取收据，使等待锁的请求能看到刚提交的操作。
    SELECT s.last_seq, a.status
      INTO v_last_seq, v_account_status
      FROM public.account_sync_state s
      JOIN public.accounts a ON a.id = s.account_id
     WHERE s.account_id = p_account_id
       FOR UPDATE OF s;
    IF NOT FOUND THEN
        RETURN jsonb_build_object('error_code', 'SESSION_EXPIRED');
    END IF;
    IF v_account_status <> 'active' THEN
        RETURN jsonb_build_object('error_code', 'ACCOUNT_DELETING');
    END IF;

    v_now := clock_timestamp();
    SELECT r.request_hash, r.result
      INTO v_request_hash, v_result
      FROM public.mutation_receipts r
     WHERE r.account_id = p_account_id AND r.operation_id = p_operation_id;
    v_replayed := FOUND;
    IF v_replayed AND v_request_hash IS DISTINCT FROM p_request_hash THEN
        RETURN jsonb_build_object('error_code', 'IDEMPOTENCY_CONFLICT');
    END IF;

    IF p_entity_type = 'packing_item' THEN
        IF v_replayed THEN
            SELECT p.* INTO v_packing
              FROM public.packing_items p
             WHERE p.account_id = p_account_id AND p.trip_id = p_trip_id AND p.id = p_entity_id;
            v_has_resource := FOUND;
        ELSE
            UPDATE public.packing_items p
               SET name = coalesce(p_patch->>'name', p.name),
                   category = coalesce(p_patch->>'category', p.category),
                   quantity = coalesce((p_patch->>'quantity')::integer, p.quantity),
                   notes = coalesce(p_patch->>'notes', p.notes),
                   status = coalesce(p_patch->>'status', p.status),
                   version = p.version + 1,
                   updated_at = v_now
             WHERE p.account_id = p_account_id AND p.trip_id = p_trip_id AND p.id = p_entity_id
               AND p.version = p_base_version AND p.deleted_at IS NULL
               AND EXISTS (
                   SELECT 1 FROM public.trips t
                    WHERE t.account_id = p_account_id AND t.id = p_trip_id AND t.deleted_at IS NULL
               )
               AND (
                   (coalesce(p_patch->>'name', p.name) = p.name
                    AND coalesce(p_patch->>'category', p.category) = p.category)
                   OR NOT EXISTS (
                       SELECT 1 FROM public.packing_items other
                        WHERE other.account_id = p_account_id AND other.trip_id = p_trip_id
                          AND other.deleted_at IS NULL AND other.id <> p_entity_id
                          AND other.category = coalesce(p_patch->>'category', p.category)
                          AND lower(btrim(other.name)) = lower(btrim(coalesce(p_patch->>'name', p.name)))
                   )
               )
             RETURNING p.* INTO v_packing;
            IF NOT FOUND THEN
                RETURN NULL;
            END IF;
            v_has_resource := true;
        END IF;
        IF v_has_resource THEN
            v_version := v_packing.version;
            v_snapshot := jsonb_build_object(
                'id', v_packing.id, 'trip_id', v_packing.trip_id,
                'name', v_packing.name, 'category', v_packing.category,
                'sort_order', v_packing.sort_order, 'quantity', v_packing.quantity, 'notes', v_packing.notes, 'status', v_packing.status,
                'version', v_packing.version::text,
                'created_at', v_packing.created_at, 'updated_at', v_packing.updated_at,
                'deleted_at', v_packing.deleted_at
            );
        END IF;
    ELSE
        IF v_replayed THEN
            SELECT t.* INTO v_todo
              FROM public.todo_items t
             WHERE t.account_id = p_account_id AND t.trip_id = p_trip_id AND t.id = p_entity_id;
            v_has_resource := FOUND;
        ELSE
            UPDATE public.todo_items t
               SET title = coalesce(p_patch->>'title', t.title),
                   due_on = CASE WHEN coalesce((p_patch->>'due_on_set')::boolean, false) THEN
                       CASE WHEN p_patch->>'due_on' LIKE '0000-%'
                           THEN to_date('0001-' || substr(p_patch->>'due_on', 6) || ' BC', 'YYYY-MM-DD BC')
                           ELSE (p_patch->>'due_on')::date END
                       ELSE t.due_on END,
                   notes = coalesce(p_patch->>'notes', t.notes),
                   completed_at = CASE
                       WHEN p_patch->>'completed' IS NULL THEN t.completed_at
                       WHEN (p_patch->>'completed')::boolean THEN coalesce(t.completed_at, v_now)
                       ELSE NULL
                   END,
                   version = t.version + 1,
                   updated_at = v_now
             WHERE t.account_id = p_account_id AND t.trip_id = p_trip_id AND t.id = p_entity_id
               AND t.version = p_base_version AND t.deleted_at IS NULL
               AND EXISTS (
                   SELECT 1 FROM public.trips trip
                    WHERE trip.account_id = p_account_id AND trip.id = p_trip_id AND trip.deleted_at IS NULL
               )
             RETURNING t.* INTO v_todo;
            IF NOT FOUND THEN
                RETURN NULL;
            END IF;
            v_has_resource := true;
        END IF;
        IF v_has_resource THEN
            v_version := v_todo.version;
            v_snapshot := jsonb_build_object(
                'id', v_todo.id, 'trip_id', v_todo.trip_id,
                'title', v_todo.title, 'due_on', CASE WHEN extract(year FROM v_todo.due_on) = -1
                    THEN '0000-' || to_char(v_todo.due_on, 'MM-DD')
                    ELSE to_char(v_todo.due_on, 'YYYY-MM-DD') END,
                'sort_order', v_todo.sort_order, 'notes', v_todo.notes, 'completed', v_todo.completed_at IS NOT NULL,
                'completed_at', v_todo.completed_at, 'version', v_todo.version::text,
                'created_at', v_todo.created_at, 'updated_at', v_todo.updated_at,
                'deleted_at', v_todo.deleted_at
            );
        END IF;
    END IF;

    IF v_replayed THEN
        RETURN v_result || jsonb_build_object('replayed', true, 'data', v_snapshot);
    END IF;

    v_primary := jsonb_build_object('type', p_entity_type, 'id', p_entity_id, 'version', v_version::text);
    v_result := jsonb_build_object(
        'operation_id', p_operation_id, 'primary', v_primary, 'affected', jsonb_build_array(v_primary),
        'commit_cursor', NULL, 'warnings', jsonb_build_array(), 'replayed', false, 'data', NULL
    );
    v_next_seq := v_last_seq + 1;
    INSERT INTO public.sync_changes (
        account_id, seq, batch_id, batch_end_seq, entity_type, entity_id, trip_id,
        entity_version, change_kind, schema_version, snapshot, changed_fields, requires_snapshot, created_at
    ) VALUES (
        p_account_id, v_next_seq, p_operation_id, v_next_seq, p_entity_type, p_entity_id, p_trip_id,
        v_version, 'upsert', 1, v_snapshot, p_fields, false, v_now
    );
    UPDATE public.account_sync_state
       SET last_seq = v_next_seq, updated_at = v_now
     WHERE account_id = p_account_id;
    INSERT INTO public.mutation_receipts (account_id, operation_id, operation_type, request_hash, result, created_at)
    VALUES (p_account_id, p_operation_id, p_operation_type, p_request_hash, v_result, v_now);

    RETURN v_result || jsonb_build_object('data', v_snapshot);
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION tripfolio_private.patch_item(uuid, uuid, text, bytea, text, uuid, uuid, bigint, jsonb, text[]) FROM PUBLIC;

-- +goose Down

-- 同版本更新在一次调用内提交；无法快速处理时不写数据，由应用回退。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tripfolio_private.patch_item(
    p_account_id uuid,
    p_operation_id uuid,
    p_operation_type text,
    p_request_hash bytea,
    p_entity_type text,
    p_trip_id uuid,
    p_entity_id uuid,
    p_base_version bigint,
    p_patch jsonb,
    p_fields text[]
) RETURNS jsonb
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
SET search_path = pg_catalog, public
SET timezone = 'UTC'
AS $$
DECLARE
    v_last_seq bigint;
    v_next_seq bigint;
    v_account_status text;
    v_request_hash bytea;
    v_result jsonb;
    v_replayed boolean;
    v_has_resource boolean;
    v_snapshot jsonb;
    v_primary jsonb;
    v_version bigint;
    v_now timestamptz;
    v_packing public.packing_items%ROWTYPE;
    v_todo public.todo_items%ROWTYPE;
BEGIN
    IF NOT (
        (p_entity_type = 'packing_item' AND p_operation_type = 'packing.update')
        OR (p_entity_type = 'todo' AND p_operation_type = 'todo.update')
    ) OR p_entity_type IS NULL OR p_operation_type IS NULL THEN
        RETURN NULL;
    END IF;

    -- 锁后单独读取收据，使等待锁的请求能看到刚提交的操作。
    SELECT s.last_seq, a.status
      INTO v_last_seq, v_account_status
      FROM public.account_sync_state s
      JOIN public.accounts a ON a.id = s.account_id
     WHERE s.account_id = p_account_id
       FOR UPDATE OF s;
    IF NOT FOUND THEN
        RETURN jsonb_build_object('error_code', 'SESSION_EXPIRED');
    END IF;
    IF v_account_status <> 'active' THEN
        RETURN jsonb_build_object('error_code', 'ACCOUNT_DELETING');
    END IF;

    v_now := clock_timestamp();
    SELECT r.request_hash, r.result
      INTO v_request_hash, v_result
      FROM public.mutation_receipts r
     WHERE r.account_id = p_account_id AND r.operation_id = p_operation_id;
    v_replayed := FOUND;
    IF v_replayed AND v_request_hash IS DISTINCT FROM p_request_hash THEN
        RETURN jsonb_build_object('error_code', 'IDEMPOTENCY_CONFLICT');
    END IF;

    IF p_entity_type = 'packing_item' THEN
        IF v_replayed THEN
            SELECT p.* INTO v_packing
              FROM public.packing_items p
             WHERE p.account_id = p_account_id AND p.trip_id = p_trip_id AND p.id = p_entity_id;
            v_has_resource := FOUND;
        ELSE
            UPDATE public.packing_items p
               SET name = coalesce(p_patch->>'name', p.name),
                   category = coalesce(p_patch->>'category', p.category),
                   quantity = coalesce((p_patch->>'quantity')::integer, p.quantity),
                   notes = coalesce(p_patch->>'notes', p.notes),
                   status = coalesce(p_patch->>'status', p.status),
                   version = p.version + 1,
                   updated_at = v_now
             WHERE p.account_id = p_account_id AND p.trip_id = p_trip_id AND p.id = p_entity_id
               AND p.version = p_base_version AND p.deleted_at IS NULL
               AND EXISTS (
                   SELECT 1 FROM public.trips t
                    WHERE t.account_id = p_account_id AND t.id = p_trip_id AND t.deleted_at IS NULL
               )
               AND (
                   (coalesce(p_patch->>'name', p.name) = p.name
                    AND coalesce(p_patch->>'category', p.category) = p.category)
                   OR NOT EXISTS (
                       SELECT 1 FROM public.packing_items other
                        WHERE other.account_id = p_account_id AND other.trip_id = p_trip_id
                          AND other.deleted_at IS NULL AND other.id <> p_entity_id
                          AND other.category = coalesce(p_patch->>'category', p.category)
                          AND lower(btrim(other.name)) = lower(btrim(coalesce(p_patch->>'name', p.name)))
                   )
               )
             RETURNING p.* INTO v_packing;
            IF NOT FOUND THEN
                RETURN NULL;
            END IF;
            v_has_resource := true;
        END IF;
        IF v_has_resource THEN
            v_version := v_packing.version;
            v_snapshot := jsonb_build_object(
                'id', v_packing.id, 'trip_id', v_packing.trip_id,
                'name', v_packing.name, 'category', v_packing.category,
                'quantity', v_packing.quantity, 'notes', v_packing.notes, 'status', v_packing.status,
                'version', v_packing.version::text,
                'created_at', v_packing.created_at, 'updated_at', v_packing.updated_at,
                'deleted_at', v_packing.deleted_at
            );
        END IF;
    ELSE
        IF v_replayed THEN
            SELECT t.* INTO v_todo
              FROM public.todo_items t
             WHERE t.account_id = p_account_id AND t.trip_id = p_trip_id AND t.id = p_entity_id;
            v_has_resource := FOUND;
        ELSE
            UPDATE public.todo_items t
               SET title = coalesce(p_patch->>'title', t.title),
                   due_on = CASE WHEN coalesce((p_patch->>'due_on_set')::boolean, false) THEN
                       CASE WHEN p_patch->>'due_on' LIKE '0000-%'
                           THEN to_date('0001-' || substr(p_patch->>'due_on', 6) || ' BC', 'YYYY-MM-DD BC')
                           ELSE (p_patch->>'due_on')::date END
                       ELSE t.due_on END,
                   notes = coalesce(p_patch->>'notes', t.notes),
                   completed_at = CASE
                       WHEN p_patch->>'completed' IS NULL THEN t.completed_at
                       WHEN (p_patch->>'completed')::boolean THEN coalesce(t.completed_at, v_now)
                       ELSE NULL
                   END,
                   version = t.version + 1,
                   updated_at = v_now
             WHERE t.account_id = p_account_id AND t.trip_id = p_trip_id AND t.id = p_entity_id
               AND t.version = p_base_version AND t.deleted_at IS NULL
               AND EXISTS (
                   SELECT 1 FROM public.trips trip
                    WHERE trip.account_id = p_account_id AND trip.id = p_trip_id AND trip.deleted_at IS NULL
               )
             RETURNING t.* INTO v_todo;
            IF NOT FOUND THEN
                RETURN NULL;
            END IF;
            v_has_resource := true;
        END IF;
        IF v_has_resource THEN
            v_version := v_todo.version;
            v_snapshot := jsonb_build_object(
                'id', v_todo.id, 'trip_id', v_todo.trip_id,
                'title', v_todo.title, 'due_on', CASE WHEN extract(year FROM v_todo.due_on) = -1
                    THEN '0000-' || to_char(v_todo.due_on, 'MM-DD')
                    ELSE to_char(v_todo.due_on, 'YYYY-MM-DD') END,
                'notes', v_todo.notes, 'completed', v_todo.completed_at IS NOT NULL,
                'completed_at', v_todo.completed_at, 'version', v_todo.version::text,
                'created_at', v_todo.created_at, 'updated_at', v_todo.updated_at,
                'deleted_at', v_todo.deleted_at
            );
        END IF;
    END IF;

    IF v_replayed THEN
        RETURN v_result || jsonb_build_object('replayed', true, 'data', v_snapshot);
    END IF;

    v_primary := jsonb_build_object('type', p_entity_type, 'id', p_entity_id, 'version', v_version::text);
    v_result := jsonb_build_object(
        'operation_id', p_operation_id, 'primary', v_primary, 'affected', jsonb_build_array(v_primary),
        'commit_cursor', NULL, 'warnings', jsonb_build_array(), 'replayed', false, 'data', NULL
    );
    v_next_seq := v_last_seq + 1;
    INSERT INTO public.sync_changes (
        account_id, seq, batch_id, batch_end_seq, entity_type, entity_id, trip_id,
        entity_version, change_kind, schema_version, snapshot, changed_fields, requires_snapshot, created_at
    ) VALUES (
        p_account_id, v_next_seq, p_operation_id, v_next_seq, p_entity_type, p_entity_id, p_trip_id,
        v_version, 'upsert', 1, v_snapshot, p_fields, false, v_now
    );
    UPDATE public.account_sync_state
       SET last_seq = v_next_seq, updated_at = v_now
     WHERE account_id = p_account_id;
    INSERT INTO public.mutation_receipts (account_id, operation_id, operation_type, request_hash, result, created_at)
    VALUES (p_account_id, p_operation_id, p_operation_type, p_request_hash, v_result, v_now);

    RETURN v_result || jsonb_build_object('data', v_snapshot);
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION tripfolio_private.patch_item(uuid, uuid, text, bytea, text, uuid, uuid, bigint, jsonb, text[]) FROM PUBLIC;
