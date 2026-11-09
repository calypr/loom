-- pg-store-03: dataset generation manifests and the active-generation pointer.
--
-- One row per dataset generation (project, generation) holds the lifecycle
-- state and the schema identity document that participates in
-- compare-and-swap equality. The active-generation pointer is one row per
-- project: a missing row means no active generation (there is no placeholder
-- row and no SQL NULL standing in for "absent"). Presence of the pointer row
-- is the compare value for activation CAS; the store never moves it outside
-- its transactional compare-and-swap.
--
-- State values are the public dataset manifest state names, byte-for-byte:
-- LOADING, STAGED, READY (legacy spelling of STAGED), FAILED. schema_identity
-- stores the manifest's schemaIdentity JSON document; SQL NULL is not
-- admitted for any column here -- missing data is a constraint violation,
-- never a silent NULL.
--
-- DDL is retryable (IF NOT EXISTS) per the pg-store-02 runner contract: a
-- crash between a migration's commit and its ledger record must converge on
-- re-application.
--
-- Version-window note: 000002 is reserved for the concurrent pg-store-04
-- ticket, so this history step is 000003. The runner refuses an out-of-order
-- pending migration below the highest recorded version, so any long-lived
-- schema that records version 3 BEFORE 000002 exists can never apply a later
-- added 000002; pg-store-04's 000002 must land on the integration branch
-- before any persistent (non per-test-isolated) schema is migrated with this
-- history, or the coordinator must renumber one of the two steps. Per-test
-- fixtures always start from an empty ledger and are unaffected.
CREATE TABLE IF NOT EXISTS dataset_generation_manifest (
    project         text        NOT NULL,
    generation      text        NOT NULL,
    state           text        NOT NULL,
    schema_identity jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dataset_generation_manifest_pkey
        PRIMARY KEY (project, generation),
    CONSTRAINT dataset_generation_manifest_project_nonempty
        CHECK (project <> ''),
    CONSTRAINT dataset_generation_manifest_generation_nonempty
        CHECK (generation <> ''),
    CONSTRAINT dataset_generation_manifest_state_public_names
        CHECK (state IN ('LOADING', 'STAGED', 'READY', 'FAILED')),
    CONSTRAINT dataset_generation_manifest_identity_is_object
        CHECK (jsonb_typeof(schema_identity) = 'object')
);

CREATE TABLE IF NOT EXISTS dataset_active_generation (
    project    text NOT NULL,
    generation text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dataset_active_generation_pkey
        PRIMARY KEY (project),
    CONSTRAINT dataset_active_generation_project_nonempty
        CHECK (project <> ''),
    CONSTRAINT dataset_active_generation_generation_nonempty
        CHECK (generation <> ''),
    CONSTRAINT dataset_active_generation_manifest_fkey
        FOREIGN KEY (project, generation)
        REFERENCES dataset_generation_manifest (project, generation)
        ON DELETE RESTRICT
);
