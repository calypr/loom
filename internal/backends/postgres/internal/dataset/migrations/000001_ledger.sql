-- pg-store-02: create the migration ledger.
--
-- The ledger is itself a versioned migration so its creation is audited by
-- the same runner that reads it. It stores version, name, and checksum only
-- (plus when it was applied) -- never the migration SQL content. The runner
-- inserts each row only after the migration transaction commits, so a
-- recorded migration always applied successfully.
CREATE TABLE schema_migrations (
    version    bigint      NOT NULL,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schema_migrations_pkey PRIMARY KEY (version),
    CONSTRAINT schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT schema_migrations_name_shape CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT schema_migrations_checksum_hex CHECK (checksum ~ '^[0-9a-f]{64}$')
);
