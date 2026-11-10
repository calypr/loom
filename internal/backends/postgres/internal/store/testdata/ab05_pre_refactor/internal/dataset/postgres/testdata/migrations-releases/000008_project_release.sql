-- pg-publication-project-release-cas-v1: immutable ProjectRelease candidates
-- and the active-project-release compare-and-swap pointer.
--
-- Ownership: this lane owns exactly these two tables. The dataset generation
-- manifest and the active-generation pointer belong to pg-store-03
-- (000003_dataset_generation_manifest.sql) and are NOT duplicated here; the
-- generation owner participates in release activation through the
-- ReleaseStore's GenerationOwnerAdapter inside the same PostgreSQL
-- transaction, reading its own rows -- no mirrored manifest or generation
-- columns are added to either side. ClickHouse remains the publication row
-- store: no dataframe rows move here, and Arango/AQL stay supported.
--
-- Version reservation: 000006 and 000007 are coordinator-reserved shared
-- numbers (tests/migrations-leases holds 000006). This fixture is
-- package-local (loaded only by this package's tests through the pg-store-02
-- runner alongside the embedded ledger bootstrap and migration 3) and takes
-- 000008 so it composes without colliding with the shared reservations.
-- Promotion to a shared migrations directory requires coordinator approval.
--
-- project_release is the immutable candidate archive:
--   * One row per (project, content-addressed release id). The id is an
--     opaque immutable content label minted by internal/dataset
--     (releaseID, "release_" + SHA-256 hex); this schema deliberately adds
--     no id-format CHECK so pre-widening digests and any future framing
--     change stay readable, matching the reader-side treatment in
--     internal/dataset/release.go.
--   * payload stores the complete ProjectRelease JSON document exactly as
--     saved. It is authoritative on read, so nanosecond timestamps and the
--     JSON null-versus-absent shape survive a save/read round trip
--     unchanged; every column is NOT NULL and no statement binds SQL NULL,
--     so missing, JSON-null, and SQL-NULL stay distinct.
--   * generation and git_commit are scan columns derived from the same
--     struct fields the payload carries; the store verifies column/payload
--     agreement on every read and refuses disagreement as a store
--     inconsistency instead of coercing it.
--   * Rows are never updated or deleted: inactive candidates stay retained
--     for audit, so a lost activation race never destroys the candidate.
--
-- active_project_release is the active-visibility pointer:
--   * One row per project. A missing row means no active release -- there is
--     no placeholder row and no SQL NULL standing in for "absent", matching
--     the pg-store-03 pointer convention.
--   * revision is the CAS counter compared inside the activation
--     transaction: it starts at 1 (revision >= 1; callers express "no
--     active release yet" as expectedRevision 0, exactly as the Arango
--     baseline's releasePointer == null ? 0 rule does) and only ever moves
--     through the store's compare-and-swap. The foreign key makes "the
--     active pointer names a persisted candidate" a schema invariant,
--     mirroring how 000003 makes the active-generation manifest a schema
--     invariant.
--
-- DDL is retryable (IF NOT EXISTS) per the pg-store-02 runner contract: a
-- crash between a migration's commit and its ledger record must converge on
-- re-application.
CREATE TABLE IF NOT EXISTS project_release (
    project    text        NOT NULL,
    id         text        NOT NULL,
    generation text        NOT NULL,
    git_commit text        NOT NULL,
    payload    jsonb       NOT NULL,
    CONSTRAINT project_release_pkey
        PRIMARY KEY (project, id),
    CONSTRAINT project_release_project_nonempty
        CHECK (project <> ''),
    CONSTRAINT project_release_id_nonempty
        CHECK (id <> ''),
    CONSTRAINT project_release_generation_nonempty
        CHECK (generation <> ''),
    CONSTRAINT project_release_git_commit_nonempty
        CHECK (git_commit <> ''),
    CONSTRAINT project_release_payload_is_object
        CHECK (jsonb_typeof(payload) = 'object')
);

-- Candidate lookup by audit scope: which candidates were ever retained for a
-- project+generation, newest identity first by id.
CREATE INDEX IF NOT EXISTS project_release_project_generation
    ON project_release (project, generation);

CREATE TABLE IF NOT EXISTS active_project_release (
    project    text        NOT NULL,
    release_id text        NOT NULL,
    revision   bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT active_project_release_pkey
        PRIMARY KEY (project),
    CONSTRAINT active_project_release_project_nonempty
        CHECK (project <> ''),
    CONSTRAINT active_project_release_release_id_nonempty
        CHECK (release_id <> ''),
    CONSTRAINT active_project_release_revision_positive
        CHECK (revision >= 1),
    CONSTRAINT active_project_release_release_fkey
        FOREIGN KEY (project, release_id)
        REFERENCES project_release (project, id)
);
