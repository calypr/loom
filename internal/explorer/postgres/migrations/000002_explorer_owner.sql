-- pg-state-foundation-v1: Explorer owner table (the first concrete control
-- schema under the pg-store-02 migration runner).
--
-- One row is the owner record for exactly one Explorer, identified by the
-- unique (project, explorer_id) pair. Identity columns carry COLLATE "C" so
-- uniqueness and List's ORDER BY explorer_id use exact byte order -- the same
-- deterministic order Arango's byte-order SORT gives -- independent of the
-- database's default collation.
--
-- Column policy:
--   - Queried/authoritative fields are typed columns (text, bigint,
--     timestamptz). The two json.RawMessage payloads are jsonb.
--   - Go strings have no missing/empty distinction, so optional text columns
--     are NOT NULL DEFAULT '' and round-trip the Go zero value exactly.
--   - draft_config and last_authoring_command_results are nullable jsonb and
--     preserve the three Go states of json.RawMessage losslessly: missing
--     (nil) is SQL NULL, JSON-null is the jsonb value 'null' (IS NULL false),
--     and a document is the document. Nothing is coerced across those
--     states; tests assert each with literal rows.
--   - The draft payload is stored lossless: jsonb keeps every member of the
--     canonical ExplorerConfigV2 packet (key order/whitespace are
--     normalized by the jsonb type itself, never a member or value).
--   - explorers_management_mode_known accepts only the ManagementMode domain
--     ('REPOSITORY'/'INTERACTIVE'); explorers_draft_version_non_negative
--     keeps the CAS counter in its meaningful range. Constraint violations
--     surface as PostgreSQL CHECK violations (23514), classified separately
--     from duplicate-owner conflicts.
--   - This migration never drops or truncates: rerunning bootstrap on an
--     existing schema is a no-op that preserves owner state.
CREATE TABLE IF NOT EXISTS explorers (
    project                          text             NOT NULL COLLATE "C",
    explorer_id                      text             NOT NULL COLLATE "C",
    title                            text             NOT NULL,
    management_mode                  text             NOT NULL,
    draft_config                     jsonb,
    draft_version                    bigint           NOT NULL,
    draft_digest                     text             NOT NULL DEFAULT '',
    draft_snapshot_token             text             NOT NULL DEFAULT '',
    draft_source_generation          text             NOT NULL DEFAULT '',
    draft_authorization_scope_digest text             NOT NULL DEFAULT '',
    previous_draft_revision_id       text             NOT NULL DEFAULT '',
    last_authoring_command_id        text             NOT NULL DEFAULT '',
    last_authoring_command_digest    text             NOT NULL DEFAULT '',
    last_authoring_command_results   jsonb,
    active_revision_id               text             NOT NULL DEFAULT '',
    updated_by                       text             NOT NULL DEFAULT '',
    updated_at                       timestamptz      NOT NULL,
    CONSTRAINT explorers_pkey PRIMARY KEY (project, explorer_id),
    CONSTRAINT explorers_management_mode_known
        CHECK (management_mode IN ('REPOSITORY', 'INTERACTIVE')),
    CONSTRAINT explorers_draft_version_non_negative
        CHECK (draft_version >= 0)
);
