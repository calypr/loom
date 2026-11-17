-- pg-state-immutable-capability-receipts-v1: immutable capability snapshots
-- and table-shape capability receipts for the Explorer control plane.
--
-- Both tables are fully bound, create-once artifacts. The jsonb payload is
-- authoritative for reads (nanosecond-free canonical snapshot documents and
-- receipt objects with their missing-versus-JSON-null member shapes
-- round-trip untouched); the scan columns mirror the payload for retrieval
-- scopes, constraints, and ordered scans only. Every statement in the
-- adapter writes both from the same validated value, and the adapter
-- re-checks payload-versus-mirror agreement on every read. No row is ever
-- UPDATEd or DELETEd: content identity (token / content digest) is the key,
-- so a changed payload is a new artifact or a typed collision, never a
-- mutation. The version continues the Explorer control-plane fixture history
-- after the 000003 draft-revision reservation (pg-state-explorer-draft-cas)
-- and the DDL is idempotent because the pg-store-02 runner may retry a
-- migration that committed before its ledger row was written.
--
-- Capability snapshot identity:
--   * token is the content address ("sha256:" + 64 lowercase hex of the
--     canonical snapshot payload). Put is insert-only, so concurrent
--     duplicate puts converge on the existing row; a stored payload that no
--     longer hashes to its token is a typed collision, never a rewrite.
--   * The identity columns mirror SnapshotIdentity exactly (empty string
--     stands in for the omitted cacheVersion/shapeDigest, matching the
--     Arango adapter's NOT_NULL(identity.cacheVersion, "") filter). The
--     status CHECK closes the domain and the READY guard keeps the SQL
--     mirror consistent with validateSnapshot's READY completeness rule;
--     the payload remains the authority that the adapter re-validates.
--
-- Table-shape receipt identity:
--   * id is the content-derived opaque artifact ID with a kind-bound prefix
--     (tsc_ catalog, tsd_ category scan, tsr_ resolution), so the CHECK
--     below makes a kind/prefix mismatch impossible at the database, not
--     just in the adapter. content_digest is the "sha256:" digest over the
--     receipt's canonical identity bytes.
--   * Every binding column is NOT NULL and non-empty and draft_version is
--     at least 1, mirroring Binding.Validate; retrieval is scoped by the
--     full binding, so an opaque ID is never trusted without its current
--     project/Explorer/output/snapshot/source binding. parent_catalog_id is
--     empty exactly for CATALOG rows.
--
-- Immutability is enforced here, not only in Go: AFTER the tables, a
-- shared guard function plus BEFORE UPDATE OR DELETE triggers refuse any
-- mutation of an artifact row with SQLSTATE 45501 before it can run. The
-- suite proves the guards are not vacuous by dropping each trigger,
-- repeating the violating statement (which must then succeed), and
-- restoring the trigger.

CREATE TABLE IF NOT EXISTS capability_snapshots (
    token text PRIMARY KEY
        CONSTRAINT capability_snapshots_token_shape CHECK (token ~ '^sha256:[0-9a-f]{64}$'),
    project text NOT NULL DEFAULT '',
    generation text NOT NULL DEFAULT '',
    authorization_scope_digest text NOT NULL DEFAULT '',
    cache_version text NOT NULL DEFAULT '',
    schema_digest text NOT NULL DEFAULT '',
    resource_inventory_digest text NOT NULL DEFAULT '',
    relationship_digest text NOT NULL DEFAULT '',
    field_digest text NOT NULL DEFAULT '',
    shape_digest text NOT NULL DEFAULT '',
    protocol_version text NOT NULL DEFAULT '',
    compiler_version text NOT NULL DEFAULT '',
    traversal_policy_version text NOT NULL DEFAULT '',
    projection_policy_version text NOT NULL DEFAULT '',
    status text NOT NULL
        CONSTRAINT capability_snapshots_status_known CHECK (status IN ('BUILDING', 'READY', 'FAILED')),
    complete boolean NOT NULL,
    truncated boolean NOT NULL,
    payload jsonb NOT NULL
        CONSTRAINT capability_snapshots_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT capability_snapshots_ready_is_usable
        CHECK (status <> 'READY' OR (complete AND NOT truncated))
);

-- Retrieval mirrors the Arango collection indexes: identity lookup and the
-- status-bounded current-identity lookup.
CREATE INDEX IF NOT EXISTS capability_snapshots_identity_ix
    ON capability_snapshots (project, generation, authorization_scope_digest, protocol_version, compiler_version);
CREATE INDEX IF NOT EXISTS capability_snapshots_status_ix
    ON capability_snapshots (project, generation, authorization_scope_digest, status);

CREATE TABLE IF NOT EXISTS table_shape_capabilities (
    id text PRIMARY KEY,
    kind text NOT NULL
        CONSTRAINT table_shape_capabilities_kind_known CHECK (kind IN ('CATALOG', 'CATEGORY_SCAN', 'RESOLUTION')),
    parent_catalog_id text NOT NULL DEFAULT '',
    project text NOT NULL,
    explorer_id text NOT NULL,
    output_id text NOT NULL,
    snapshot_token text NOT NULL,
    authorization_scope text NOT NULL,
    source_generation text NOT NULL,
    draft_version bigint NOT NULL,
    draft_digest text NOT NULL,
    base_document_digest text NOT NULL,
    base_compilation_receipt_id text NOT NULL,
    output_fingerprint text NOT NULL,
    compiler_schema_digest text NOT NULL,
    content_digest text NOT NULL
        CONSTRAINT table_shape_capabilities_digest_shape CHECK (content_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at text NOT NULL DEFAULT '',
    payload jsonb NOT NULL
        CONSTRAINT table_shape_capabilities_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT table_shape_capabilities_id_kind_prefix CHECK (
        (kind = 'CATALOG' AND id ~ '^tsc_[0-9a-f]{32}$')
        OR (kind = 'CATEGORY_SCAN' AND id ~ '^tsd_[0-9a-f]{32}$')
        OR (kind = 'RESOLUTION' AND id ~ '^tsr_[0-9a-f]{32}$')
    ),
    CONSTRAINT table_shape_capabilities_parent_bound CHECK (
        (kind = 'CATALOG' AND parent_catalog_id = '')
        OR (kind <> 'CATALOG' AND parent_catalog_id <> '')
    ),
    CONSTRAINT table_shape_capabilities_binding_complete CHECK (
        project <> '' AND explorer_id <> '' AND output_id <> ''
        AND snapshot_token <> '' AND authorization_scope <> '' AND source_generation <> ''
        AND draft_version >= 1
        AND draft_digest <> '' AND base_document_digest <> ''
        AND base_compilation_receipt_id <> '' AND output_fingerprint <> ''
        AND compiler_schema_digest <> ''
    )
);

-- Retrieval mirrors the Arango collection indexes: the fully bound opaque-ID
-- lookup and the parent-scoped kind scan.
CREATE INDEX IF NOT EXISTS table_shape_capabilities_binding_ix
    ON table_shape_capabilities (project, explorer_id, output_id, snapshot_token, kind, id);
CREATE INDEX IF NOT EXISTS table_shape_capabilities_parent_ix
    ON table_shape_capabilities (project, explorer_id, output_id, parent_catalog_id, kind);

-- The immutability guard: artifact rows are create-once. Any UPDATE or
-- DELETE -- from any session, including one holding the table owner's
-- rights -- aborts with SQLSTATE 45501 before touching a page. The
-- adapter never issues UPDATE/DELETE against these tables; the trigger is
-- what makes that a database-enforced property rather than a convention.
CREATE OR REPLACE FUNCTION loom_capability_artifact_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'loom: capability artifact rows are immutable (create-once)'
        USING ERRCODE = '45501';
END;
$$;

DROP TRIGGER IF EXISTS capability_snapshots_immutable ON capability_snapshots;
CREATE TRIGGER capability_snapshots_immutable
    BEFORE UPDATE OR DELETE ON capability_snapshots
    FOR EACH ROW EXECUTE FUNCTION loom_capability_artifact_immutable();

DROP TRIGGER IF EXISTS table_shape_capabilities_immutable ON table_shape_capabilities;
CREATE TRIGGER table_shape_capabilities_immutable
    BEFORE UPDATE OR DELETE ON table_shape_capabilities
    FOR EACH ROW EXECUTE FUNCTION loom_capability_artifact_immutable();
