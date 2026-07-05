ALTER TABLE services
    DROP COLUMN IF EXISTS is_system,
    DROP COLUMN IF EXISTS system_kind;
