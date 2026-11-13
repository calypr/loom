-- pg-catalog-04: semantic-inventory build lifecycle, lease, and typed
-- checkpoint state in the PostgreSQL source store.
--
-- This mirrors the Arango fhir_semantic_inventory_builds collection
-- (internal/catalog/semantic_inventory.go, internal/catalog/arango/
-- semantic_inventory.go) as one typed row per build identity, owned by
-- internal/catalog/postgres/semantic_build.go:
--
--   - One row per (project, dataset_generation, build_id): the content-
--     addressed build identity pins schema/rule/emitter versions, so a
--     version change is a different row, never an in-place edit.
--   - state carries the closed SemanticInventoryState set; the repository
--     only drives not_started/unknown -> running -> complete|failed, and a
--     completed row is never rewritten by a claim (file and retained-vertex
--     contribution sets are never mixed).
--   - lease_token/lease_expires_at (epoch milliseconds, matching
--     catalog.SemanticInventoryBuild.LeaseExpiresAt exactly) implement a
--     fenced lease: both-empty-or-both-set, so a released lease cannot be
--     half-cleared, and every guard is one statement, so exactly one
--     claimant can hold a live lease.
--   - checkpoint_collection/checkpoint_key is the typed keyset cursor that
--     advances only under compare-and-swap on the exact expected pair; the
--     legacy string checkpoint is preserved for round-trip parity with the
--     shared contract.
--   - diagnostics is an ordered text array (a failed build carries its one
--     failure diagnostic; a complete build carries none).
--
-- Every column is NOT NULL: like migration 000005, a missing domain value
-- is the typed empty string or zero, never SQL NULL, so SQL NULL carries no
-- meaning here and nothing collapses "missing" into "null". The DDL is
-- idempotent (IF NOT EXISTS) because the runner records the ledger row
-- after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS fhir_semantic_inventory_build (
    build_key             text        NOT NULL,
    project               text        NOT NULL,
    dataset_generation    text        NOT NULL,
    build_id              text        NOT NULL,
    state                 text        NOT NULL,
    observation_schema    integer     NOT NULL,
    inventory_schema      integer     NOT NULL,
    entry_index_version   integer     NOT NULL DEFAULT 0,
    rule_version          integer     NOT NULL,
    source_kind           text        NOT NULL,
    source_availability   text        NOT NULL,
    auth_resource_path    text        NOT NULL DEFAULT '',
    scanned_resources     bigint      NOT NULL DEFAULT 0,
    checkpoint            text        NOT NULL DEFAULT '',
    checkpoint_collection text        NOT NULL DEFAULT '',
    checkpoint_key        text        NOT NULL DEFAULT '',
    lease_token           text        NOT NULL DEFAULT '',
    lease_expires_at      bigint      NOT NULL DEFAULT 0,
    diagnostics           text[]      NOT NULL DEFAULT '{}',
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_semantic_inventory_build_pkey
        PRIMARY KEY (project, dataset_generation, build_id),
    CONSTRAINT fhir_semantic_inventory_build_key_ux
        UNIQUE (build_key),
    CONSTRAINT fhir_semantic_inventory_build_key_shape
        CHECK (build_key ~ '^sib_[0-9a-f]{64}$'),
    CONSTRAINT fhir_semantic_inventory_build_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_semantic_inventory_build_build_present
        CHECK (build_id <> ''),
    CONSTRAINT fhir_semantic_inventory_build_build_shape
        CHECK (build_id ~ '^[0-9a-f]{64}$'),
    CONSTRAINT fhir_semantic_inventory_build_state_known
        CHECK (state IN ('unknown', 'not_started', 'running', 'complete', 'failed', 'invalidated')),
    CONSTRAINT fhir_semantic_inventory_build_versions_sane
        CHECK (observation_schema >= 1 AND inventory_schema >= 1 AND rule_version >= 1 AND entry_index_version >= 0),
    CONSTRAINT fhir_semantic_inventory_build_source_kind_known
        CHECK (source_kind IN ('file', 'retained_vertex')),
    CONSTRAINT fhir_semantic_inventory_build_availability_known
        CHECK (source_availability IN ('unknown', 'verified', 'unproven')),
    CONSTRAINT fhir_semantic_inventory_build_scanned_nonnegative
        CHECK (scanned_resources >= 0),
    CONSTRAINT fhir_semantic_inventory_build_checkpoint_paired
        CHECK ((checkpoint_collection = '') = (checkpoint_key = '')),
    CONSTRAINT fhir_semantic_inventory_build_lease_paired
        CHECK ((lease_token = '') = (lease_expires_at = 0)),
    CONSTRAINT fhir_semantic_inventory_build_lease_expiry_nonnegative
        CHECK (lease_expires_at >= 0)
);
