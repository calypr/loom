package postgres

// Explicit-group revision headers and ordered definition rows on
// PostgreSQL 18 (pg-state-explicit-group-definitions-v1).
//
// Explicit groups keep three separate records -- revision header, ordered
// definition rows, and membership rows (internal/explorer/explicit_group_
// repository.go). This file owns the header/writer lifecycle and the
// definition rows; the membership half -- batching/streaming, the
// transactionally verified final digest/count operation, and completion --
// lives in explicit_group_memberships.go (pg-state-explicit-group-members-
// v1). Nothing here can mark a header COMPLETE either: completion happens
// only through the fenced CompleteExplicitGroupRevision CAS, and the
// 000010 migration's guarded lifecycle machine enforces the transition at
// the database -- headers are born STAGING, the only admitted UPDATE
// channel out of STAGING demands the row's writer token and counts equal
// to the stored definition and membership rows, and COMPLETE rows are
// frozen history.
//
// Semantics mirror the Arango adapter (internal/backends/arango/internal/explorer/
// explicit_groups.go) one-for-one where this slice reaches:
//
//   - Begin is the idempotency claim: the revision ID is the deterministic
//     hash of (project, idempotency key), the INSERT is ON CONFLICT DO
//     NOTHING, and a conflicting claim re-reads and compares the canonical
//     intent the same way sameExplicitGroupRevisionIntent does -- same
//     intent + own token renews the writer lease, foreign token on a live
//     staging revision is the typed ErrExplicitGroupRevisionConflict, and
//     an already-COMPLETE header returns without a token check (parity
//     with the Arango begin-after-completion). Expired staging rows are
//     reaped before the claim, so a crashed writer cannot wedge the key.
//   - PutExplicitGroupDefinitions is write-once: the first call stores the
//     canonical (Ordinal ASC, ID ASC) rows -- empty groups included,
//     because a definition row is the group's durable identity and order
//     independent of any membership -- and a later call must present the
//     identical canonical set or it is the typed conflict, exactly like
//     the adapter's stored-vs-canonical DeepEqual.
//   - Abort removes only the owner's STAGING header with its definitions
//     and is idempotent on missing/blank input, like the adapter.
//   - CleanupExplicitGroupStaging reaps only STAGING headers whose lease
//     already expired (or that never carried one), bounded by limit, and
//     never touches active writers. FOR UPDATE SKIP LOCKED additionally
//     keeps a concurrently-working writer's row out of the batch entirely.
//   - Reads are tenant-scoped and fail closed: a missing header is
//     ErrExplicitGroupRevisionNotFound, a STAGING header is
//     ErrExplicitGroupRevisionIncomplete on the public Get/ListDefinitions
//     path, listing shows only COMPLETE headers (staging stays hidden),
//     and any stored row that disagrees with its own mirror columns or
//     with canonical round-trip is ErrCorruptExplicitGroupRevision --
//     never silently repaired.
//
// The source-selection binding is validated against the caller-passed
// SelectionRevision on every Begin (ValidateSourceSelection, including its
// invalid-selection wrap). The Arango adapter's second check against the
// *stored* selection (GetSelection) is not wired here because PostgreSQL
// selection persistence is its own ticket's schema; binding the header to
// its source identity at claim time is what this slice guarantees, and the
// stored-selection revalidation composes with it when that ticket lands.
//
// There is no fallback to any other backend: every failure is returned
// typed for the caller to surface, and no Arango path is wired in here.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const (
	// ExplicitGroupRevisionsTable holds one row per explicit-group revision
	// header, created by the explicit-groups migration inside the store's
	// schema. The writer columns (writer_token, writer_expires_at) are
	// storage-only lifecycle state, never part of the domain struct -- the
	// same split the Arango header document keeps.
	ExplicitGroupRevisionsTable = "explorer_explicit_group_revisions"
	// ExplicitGroupDefinitionsTable holds one row per (revision, declared
	// group): the durable identity and order of every group, including
	// groups with no members.
	ExplicitGroupDefinitionsTable = "explorer_explicit_group_definitions"
)

const (
	// explicitGroupWriterLease mirrors the Arango adapter's lease: every
	// accepted staging write pushes the deadline this far out.
	explicitGroupWriterLease = 10 * time.Minute
	// explicitGroupCleanupLimit bounds one cleanup sweep, like the adapter.
	explicitGroupCleanupLimit = 100
	// explicitGroupListLimit is the default listing page size, like the
	// adapter's ListExplicitGroupRevisions.
	explicitGroupListLimit = 100
)

// GUCs carrying one transaction's delete/renew authorization to the
// migration's DB-enforced guards. The store is the only writer, so these
// are set exclusively through set_config inside the store's own
// transactions; a direct-SQL session that never set them (or set a token
// it does not hold) is refused by the triggers, which is what makes
// "never remove active writers" and the completion gate database facts
// rather than Go-side conventions.
const (
	gucWriterToken = "loom.explorer_explicit_group_writer_token"
	gucAbortToken  = "loom.explorer_explicit_group_abort_token"
	gucCleanupNow  = "loom.explorer_explicit_group_cleanup_now"
)

// Constraint names the store classifies. Matching is always SQLSTATE plus
// the exact constraint name so a violation of a different guard can never
// masquerade as the expected outcome.
const (
	explicitGroupDefinitionPKConstraint   = "explorer_explicit_group_definitions_pkey"
	explicitGroupDefinitionOrdinalCK      = "explorer_explicit_group_definitions_revision_ordinal_key"
	explicitGroupRevisionFKConstraint     = "explorer_explicit_group_definitions_revision_fkey"
	explicitGroupRevisionIdentityCheck    = "explorer_explicit_group_revisions_identity_binding"
	explicitGroupRevisionRequiredFieldsCK = "explorer_explicit_group_revisions_required_fields"
	// The 000009 staging-only CHECKs (state_staging, staging_values) were
	// replaced by the membership migration's single named
	// explorer_explicit_group_revisions_lifecycle_values constraint --
	// classified from the membership slice as
	// explicitGroupRevisionLifecycleValuesCheck.
)

// explicitGroupColumns is the single source of truth for header column
// order: the INSERT column list, the RETURNING/SELECT list, the argument
// order, and the scan order all derive from it, so ordered SQL parameters
// ($1..$N) match the Go argument slice by construction. The writer columns
// are last and never reach the domain struct.
var explicitGroupColumns = []string{
	"id",
	"project",
	"generation",
	"scope_digest",
	"resource_type",
	"source_selection_revision_id",
	"source_membership_digest",
	"definition_digest",
	"membership_digest",
	"group_count",
	"member_count",
	"state",
	"idempotency_key",
	"created_at",
	"completed_at",
	"writer_token",
	"writer_expires_at",
}

// explicitGroupDefinitionColumns is the definition-row column order.
var explicitGroupDefinitionColumns = []string{
	"revision_id",
	"group_id",
	"label",
	"ordinal",
}

// explicitGroupStatements are the backend-tagged statements of the
// explicit-group store, built once per schema.
type explicitGroupStatements struct {
	headerInsert      string // INSERT ... ON CONFLICT (id) DO NOTHING RETURNING <cols>
	headerGet         string // SELECT <cols> WHERE id = $1
	headerGetScoped   string // SELECT <cols> WHERE project = $1 AND id = $2
	headerClaim       string // SELECT <cols> WHERE id = $1 FOR UPDATE
	headerRenew       string // UPDATE writer_expires_at WHERE id/state/token
	headerAbort       string // DELETE WHERE id/state/token (abort GUC set first)
	headerDeleteBatch string // DELETE WHERE id = ANY($1::text[]) (cleanup GUC set first)
	defList           string // SELECT group_id,label,ordinal WHERE revision_id ORDER BY ordinal,group_id
	defDeleteBatch    string // DELETE WHERE revision_id = ANY($1::text[])
	cleanupSelect     string // SELECT id ... FOR UPDATE SKIP LOCKED LIMIT $3
	listComplete      string // SELECT <cols> WHERE project/generation/scope/type AND state='COMPLETE' ORDER BY created_at DESC, id LIMIT $5
	setWriterTokenGUC string // SELECT set_config(gucWriterToken, $1, true)
	setAbortTokenGUC  string // SELECT set_config(gucAbortToken, $1, true)
	setCleanupNowGUC  string // SELECT set_config(gucCleanupNow, $1, true)
	defInsertBase     string // INSERT INTO definitions (cols) VALUES ... (batch suffix per call)
}

func newExplicitGroupStatements(schema string) explicitGroupStatements {
	headerTable := sanitizeTableRef(schema, ExplicitGroupRevisionsTable)
	defTable := sanitizeTableRef(schema, ExplicitGroupDefinitionsTable)
	hcols := strings.Join(explicitGroupColumns, ", ")
	hph := orderedPlaceholders(len(explicitGroupColumns))
	dcols := strings.Join(explicitGroupDefinitionColumns, ", ")
	return explicitGroupStatements{
		headerInsert: "INSERT INTO " + headerTable + " (" + hcols + ") VALUES (" + hph + ") " +
			"ON CONFLICT (id) DO NOTHING RETURNING " + hcols,
		headerGet: "SELECT " + hcols + " FROM " + headerTable + " WHERE id = $1",
		headerGetScoped: "SELECT " + hcols + " FROM " + headerTable +
			" WHERE project = $1 AND id = $2",
		headerClaim: "SELECT " + hcols + " FROM " + headerTable + " WHERE id = $1 FOR UPDATE",
		headerRenew: "UPDATE " + headerTable + " SET writer_expires_at = $2 " +
			"WHERE id = $1 AND state = 'STAGING' AND writer_token = $3",
		headerAbort: "DELETE FROM " + headerTable +
			" WHERE id = $1 AND state = 'STAGING' AND writer_token = $2",
		headerDeleteBatch: "DELETE FROM " + headerTable + " WHERE id = ANY($1::text[])",
		defList: "SELECT group_id, label, ordinal FROM " + defTable +
			" WHERE revision_id = $1 ORDER BY ordinal, group_id",
		defDeleteBatch: "DELETE FROM " + defTable + " WHERE revision_id = ANY($1::text[])",
		cleanupSelect: "SELECT id FROM " + headerTable +
			" WHERE state = 'STAGING' AND created_at < $1" +
			" AND (writer_expires_at IS NULL OR writer_expires_at < $2)" +
			" ORDER BY created_at, id LIMIT $3 FOR UPDATE SKIP LOCKED",
		listComplete: "SELECT " + hcols + " FROM " + headerTable +
			" WHERE project = $1 AND generation = $2 AND scope_digest = $3" +
			" AND resource_type = $4 AND state = 'COMPLETE'" +
			" ORDER BY created_at DESC, id LIMIT $5",
		setWriterTokenGUC: "SELECT set_config('" + gucWriterToken + "', $1, true)",
		setAbortTokenGUC:  "SELECT set_config('" + gucAbortToken + "', $1, true)",
		setCleanupNowGUC:  "SELECT set_config('" + gucCleanupNow + "', $1, true)",
		defInsertBase:     "INSERT INTO " + defTable + " (" + dcols + ") VALUES ",
	}
}

// defInsertStatement renders the multi-row INSERT for n definitions. The
// row order and placeholder numbering are derived from the canonical slice
// itself, so arguments and placeholders cannot drift apart.
func (s explicitGroupStatements) defInsertStatement(n int) string {
	rows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, fmt.Sprintf("($%d, $%d, $%d, $%d)", 4*i+1, 4*i+2, 4*i+3, 4*i+4))
	}
	return s.defInsertBase + strings.Join(rows, ", ")
}

// explicitGroupConn is the statement surface every helper runs on: one
// connection (or the transaction opened on it) acquired by the public
// entry point for the duration of exactly one operation.
type explicitGroupConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var (
	_ explicitGroupConn = (*pgxpool.Conn)(nil)
	_ explicitGroupConn = pgx.Tx(nil)
)

// ExplicitGroupStore persists explicit-group revision headers, ordered
// definition rows, and membership rows in one Explorer control-plane
// schema. With the membership slice's methods (Append, Digest, Complete,
// Visit -- explicit_group_memberships.go) it is the complete
// explorer.ExplicitGroupRepository counterpart. Headers are born STAGING
// and the only path out of STAGING is the guarded STAGING -> COMPLETE
// transition the membership migration's machine enforces at the database:
// a direct-SQL completion is refused unless the session holds the row's
// writer token and the claimed counts equal the stored child rows.
type ExplicitGroupStore struct {
	client *pgstore.Client
	stmts  explicitGroupStatements
	// memberStmts are the membership lifecycle statements
	// (explicit_group_memberships.go), built once for the same schema.
	memberStmts explicitGroupMembershipStatements
	// afterDefinitionsInserted and afterDefinitionsDeleted are the
	// test-only fault/interleaving seams of PutExplicitGroupDefinitions
	// and AbortExplicitGroupRevision (see the methods). Production never
	// assigns them.
	afterDefinitionsInserted func(context.Context, pgx.Tx) error
	afterDefinitionsDeleted  func(context.Context, pgx.Tx) error
	// afterMembershipsInserted is the test-only fault/interleaving seam
	// of AppendExplicitGroupMemberships, beforeCompleteCommit the seam of
	// the fenced CompleteExplicitGroupRevision, and membershipPageStreamed
	// the observer of every fetched membership page (see
	// explicit_group_memberships.go). Production never assigns them.
	afterMembershipsInserted func(context.Context, pgx.Tx) error
	beforeCompleteCommit     func(context.Context, pgx.Tx) error
	membershipPageStreamed   func(rows int)
}

// NewExplicitGroupStore builds the explicit-group store for one Explorer
// control-plane schema behind client. It opens no connection and runs no
// DDL; the schema must already be migrated by this package's history, and
// a missing table is surfaced as SQLSTATE 42P01, never a fallback.
func NewExplicitGroupStore(client *pgstore.Client, schema string) (*ExplicitGroupStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: explicit-group client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &ExplicitGroupStore{
		client:      client,
		stmts:       newExplicitGroupStatements(schema),
		memberStmts: newExplicitGroupMembershipStatements(schema),
	}, nil
}

// storedExplicitGroupHeader is one physically stored header row, writer
// columns included. The scan targets follow explicitGroupColumns order.
type storedExplicitGroupHeader struct {
	ID                        string
	Project                   string
	Generation                string
	ScopeDigest               string
	ResourceType              string
	SourceSelectionRevisionID string
	SourceMembershipDigest    string
	DefinitionDigest          string
	MembershipDigest          string
	GroupCount                int64
	MemberCount               int64
	State                     string
	IdempotencyKey            string
	CreatedAt                 time.Time
	CompletedAt               *time.Time
	WriterToken               *string
	WriterExpiresAt           *time.Time
}

func scanExplicitGroupHeader(row rowScanner, out *storedExplicitGroupHeader) error {
	return row.Scan(
		&out.ID,
		&out.Project,
		&out.Generation,
		&out.ScopeDigest,
		&out.ResourceType,
		&out.SourceSelectionRevisionID,
		&out.SourceMembershipDigest,
		&out.DefinitionDigest,
		&out.MembershipDigest,
		&out.GroupCount,
		&out.MemberCount,
		&out.State,
		&out.IdempotencyKey,
		&out.CreatedAt,
		&out.CompletedAt,
		&out.WriterToken,
		&out.WriterExpiresAt,
	)
}

// headerArgs maps a canonical revision and its store-computed writer
// lifecycle to explicitGroupColumns order. Begin only ever creates STAGING
// headers, so completion columns stay at their STAGING values here; the
// only writer of completion facts is the fenced CAS in
// explicit_group_memberships.go, guarded at the database by 000010.
func headerArgs(revision explorer.ExplicitGroupRevision, writerToken string, writerExpiresAt time.Time) []any {
	return []any{
		string(revision.ID),
		revision.Project,
		revision.Generation,
		revision.ScopeDigest,
		revision.ResourceType,
		revision.SourceSelectionRevisionID,
		revision.SourceMembershipDigest,
		"",       // definition_digest: completion value, stays empty in staging
		"",       // membership_digest: completion value, stays empty in staging
		int64(0), // group_count: completion value
		int64(0), // member_count: completion value
		string(explorer.ExplicitGroupRevisionStaging),
		revision.IdempotencyKey,
		revision.CreatedAt,
		(*time.Time)(nil), // completed_at: STAGING never carries it
		writerToken,
		writerExpiresAt,
	}
}

// toDomain converts a stored header row back to the domain struct,
// re-running the domain invariants on the physically stored values exactly
// as the Arango read path does: an ID that disagrees with the identity it
// was read under, or a row that fails Validate, is corruption -- never a
// silent repair and never served.
func (h *storedExplicitGroupHeader) toDomain(expectedID explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	if expectedID != "" && h.ID != string(expectedID) {
		return nil, fmt.Errorf("%w: header stored id %q is filed under %q",
			explorer.ErrCorruptExplicitGroupRevision, h.ID, string(expectedID))
	}
	revision := explorer.ExplicitGroupRevision{
		ID:                        explorer.ExplicitGroupRevisionID(h.ID),
		Project:                   h.Project,
		Generation:                h.Generation,
		ScopeDigest:               h.ScopeDigest,
		ResourceType:              h.ResourceType,
		SourceSelectionRevisionID: h.SourceSelectionRevisionID,
		SourceMembershipDigest:    h.SourceMembershipDigest,
		DefinitionDigest:          h.DefinitionDigest,
		MembershipDigest:          h.MembershipDigest,
		GroupCount:                h.GroupCount,
		MemberCount:               h.MemberCount,
		State:                     explorer.ExplicitGroupRevisionState(h.State),
		IdempotencyKey:            h.IdempotencyKey,
		CreatedAt:                 h.CreatedAt,
		CompletedAt:               h.CompletedAt,
	}.Canonical()
	if err := revision.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	return &revision, nil
}

// guardExplicitGroupWriter is the exact counterpart of the Arango adapter's
// explicitGroupWriter: a live staging header must exist, carry the STAGING
// state, and carry the caller's writer token, or the mutation is refused
// as the typed conflict without writing.
func guardExplicitGroupWriter(header *storedExplicitGroupHeader, writerToken string) error {
	if header == nil {
		return explorer.ErrExplicitGroupRevisionNotFound
	}
	if header.State != string(explorer.ExplicitGroupRevisionStaging) {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	stored := ""
	if header.WriterToken != nil {
		stored = strings.TrimSpace(*header.WriterToken)
	}
	if stored != writerToken {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

// BeginExplicitGroupRevision claims the header for one staging revision,
// mirroring the Arango Begin: canonicalize, validate identity and the
// source-selection binding (caller-side ValidateSourceSelection on the
// passed selection), reap expired staging rows before the claim so a
// crashed writer cannot wedge the idempotency key, then INSERT ON CONFLICT
// DO NOTHING on the deterministic (project, idempotency-key) ID.
//
// A fresh claim returns what PostgreSQL persisted. A conflicting claim
// re-reads the winner's committed row FOR UPDATE and compares the
// canonical intent (ID, project, generation, scope, resource type, source
// selection, source membership digest, idempotency key) exactly like
// sameExplicitGroupRevisionIntent: different intent is the typed conflict;
// same intent under a foreign token on a live staging header is the typed
// conflict; same intent with the owner's token renews the lease (a CAS on
// id + state + token -- a miss means the row vanished or changed under us
// and is the typed conflict). An already-COMPLETE header is returned
// without a token check, the same begin-after-completion tolerance the
// Arango adapter has. Blank writer token is the typed conflict before any
// database touch; a header whose state is not STAGING is refused as the
// plain "must begin in staging state" error, like the adapter.
func (s *ExplicitGroupStore) BeginExplicitGroupRevision(ctx context.Context, revision explorer.ExplicitGroupRevision, source explorer.SelectionRevision, writerToken string) (*explorer.ExplicitGroupRevision, error) {
	revision = revision.Canonical()
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if err := revision.Validate(); err != nil {
		return nil, err
	}
	if err := revision.ValidateSourceSelection(source); err != nil {
		return nil, err
	}
	if revision.State != explorer.ExplicitGroupRevisionStaging {
		return nil, fmt.Errorf("explicit group revision must begin in staging state")
	}
	// Reap expired staging rows before the claim (Arango parity): a dead
	// writer's expired row must not wedge the idempotency key.
	if err := s.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), explicitGroupCleanupLimit); err != nil {
		return nil, err
	}

	writerExpiresAt := time.Now().UTC().Add(explicitGroupWriterLease)
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group begin connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin explicit-group claim %q: %w", revision.ID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stored storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerInsert,
		headerArgs(revision, writerToken, writerExpiresAt)...), &stored)
	if err == nil {
		domain, err := stored.toDomain(revision.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: decode claimed header %q: %w", revision.ID, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: commit explicit-group claim %q: %w", revision.ID, err)
		}
		return domain, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres explorer store: claim explicit-group header %q: %w", revision.ID, err)
	}

	// The identity already exists (or is being claimed): wait on the row
	// and resolve the conflict exactly as the adapter's post-insert read
	// plus intent/token checks do.
	var winner storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revision.ID)), &winner)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflicting claim aborted between our insert attempt and
		// this wait: the key is free again, and this begin is a lost
		// claim -- typed conflict, retryable by the caller.
		return nil, fmt.Errorf("%w: conflicting explicit-group claim %q vanished before it became visible",
			explorer.ErrExplicitGroupRevisionConflict, revision.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read conflicting explicit-group header %q: %w", revision.ID, err)
	}
	winnerDomain, err := winner.toDomain(revision.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: decode conflicting header %q: %w", revision.ID, err)
	}
	if !sameExplicitGroupRevisionIntent(*winnerDomain, revision) {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	if winnerDomain.State == explorer.ExplicitGroupRevisionStaging {
		if err := guardExplicitGroupWriter(&winner, writerToken); err != nil {
			return nil, err
		}
		renewedAt := time.Now().UTC().Add(explicitGroupWriterLease)
		if _, err := tx.Exec(ctx, s.stmts.setWriterTokenGUC, writerToken); err != nil {
			return nil, fmt.Errorf("postgres explorer store: set writer token guard for begin %q: %w", revision.ID, err)
		}
		tag, err := tx.Exec(ctx, s.stmts.headerRenew, string(revision.ID), renewedAt, writerToken)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: renew explicit-group writer lease %q: %w", revision.ID, err)
		}
		if tag.RowsAffected() != 1 {
			// Renewal CAS miss: the header changed state or lost its
			// writer under us -- the adapter's renew-CAS-miss conflict.
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit explicit-group begin %q: %w", revision.ID, err)
	}
	return winnerDomain, nil
}

// sameExplicitGroupRevisionIntent is the PostgreSQL counterpart of the
// Arango adapter's sameExplicitGroupRevisionIntent: the canonical identity
// and source binding fields, nothing else.
func sameExplicitGroupRevisionIntent(left, right explorer.ExplicitGroupRevision) bool {
	left, right = left.Canonical(), right.Canonical()
	return left.ID == right.ID &&
		left.Project == right.Project &&
		left.Generation == right.Generation &&
		left.ScopeDigest == right.ScopeDigest &&
		left.ResourceType == right.ResourceType &&
		left.SourceSelectionRevisionID == right.SourceSelectionRevisionID &&
		left.SourceMembershipDigest == right.SourceMembershipDigest &&
		left.IdempotencyKey == right.IdempotencyKey
}

// PutExplicitGroupDefinitions stores the canonical ordered definition rows
// for one staging header, exactly once. Empty groups are meaningful: the
// definition row is the group's durable identity and order independent of
// any membership, so a group with no members still round-trips as a real
// group.
//
// Inside ONE transaction on one connection: lock the header FOR UPDATE
// (the serialization point against a concurrent put, abort, or cleanup of
// the same revision), run the writer guard (state + token, like the
// adapter's explicitGroupWriter), compare the stored rows to the new
// canonical set -- an existing set that differs is the typed conflict
// (write-once), an existing identical set is an idempotent lease renewal,
// and an absent set is inserted and re-listed for the post-write
// DeepEqual verification the adapter performs. Every accepted write
// renews the writer lease under the token CAS; a renewal miss is the typed
// conflict. Blank revision ID or writer token is the typed conflict before
// any database touch; canonicalization failures (duplicate ID or ordinal,
// blank ID/label, negative ordinal, empty declaration list) surface the
// domain's plain errors without writing.
func (s *ExplicitGroupStore) PutExplicitGroupDefinitions(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string, groups []explorer.ExplicitGroupDefinition) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	writerToken = strings.TrimSpace(writerToken)
	if revisionID == "" || writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return err
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group put connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group put %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock explicit-group header %q: %w", revisionID, err)
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return err
	}

	stored, err := s.listExplicitGroupDefinitions(ctx, tx, string(revisionID), &header)
	if err != nil {
		return err
	}
	if len(stored) > 0 {
		if !reflect.DeepEqual(stored, canonical) {
			return explorer.ErrExplicitGroupRevisionConflict
		}
		if err := s.renewExplicitGroupWriter(ctx, tx, revisionID, writerToken); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres explorer store: commit explicit-group put %q: %w", revisionID, err)
		}
		return nil
	}

	if len(canonical) > 0 {
		args := make([]any, 0, 4*len(canonical))
		for _, group := range canonical {
			args = append(args, string(revisionID), string(group.ID), group.Label, group.Ordinal)
		}
		if _, err := tx.Exec(ctx, s.stmts.defInsertStatement(len(canonical)), args...); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == duplicateKeySQLState &&
				(pgErr.ConstraintName == explicitGroupDefinitionPKConstraint ||
					pgErr.ConstraintName == explicitGroupDefinitionOrdinalCK) {
				// Another writer committed rows for this revision between
				// our comparison and our insert -- the header lock makes
				// this unreachable today; classified as the conflict with
				// the real PostgreSQL cause retained, never absorbed.
				return errors.Join(explorer.ErrExplicitGroupRevisionConflict, err)
			}
			return fmt.Errorf("postgres explorer store: insert explicit-group definitions for %q: %w", revisionID, err)
		}

		// Test-only seam: exercises faults between the real definition
		// insert and the post-write verification/renewal inside this same
		// live transaction. Always nil in production (unexported, never
		// assigned outside explicit_groups_test.go); an error here aborts
		// the transaction, so the definition rows and the renewal both
		// vanish.
		if s.afterDefinitionsInserted != nil {
			if err := s.afterDefinitionsInserted(ctx, tx); err != nil {
				return fmt.Errorf("postgres explorer store: put explicit-group definitions %q after definition insert: %w",
					revisionID, err)
			}
		}

		verified, err := s.listExplicitGroupDefinitions(ctx, tx, string(revisionID), &header)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(verified, canonical) {
			// The rows PostgreSQL committed are not what we sent: that is
			// the adapter's post-write conflict, never a silent accept.
			return explorer.ErrExplicitGroupRevisionConflict
		}
	}
	if err := s.renewExplicitGroupWriter(ctx, tx, revisionID, writerToken); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group put %q: %w", revisionID, err)
	}
	return nil
}

// renewExplicitGroupWriter pushes the writer deadline out under the
// token-guarded CAS. The migration's UPDATE trigger additionally demands
// the session-local writer-token GUC and refuses any lease shortening, so
// a stale or foreign renew never lands.
func (s *ExplicitGroupStore) renewExplicitGroupWriter(ctx context.Context, tx pgx.Tx, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	if _, err := tx.Exec(ctx, s.stmts.setWriterTokenGUC, writerToken); err != nil {
		return fmt.Errorf("postgres explorer store: set writer token guard for %q: %w", revisionID, err)
	}
	tag, err := tx.Exec(ctx, s.stmts.headerRenew, string(revisionID),
		time.Now().UTC().Add(explicitGroupWriterLease), writerToken)
	if err != nil {
		return fmt.Errorf("postgres explorer store: renew explicit-group writer lease %q: %w", revisionID, err)
	}
	if tag.RowsAffected() != 1 {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	return nil
}

// listExplicitGroupDefinitions reads the stored definition rows in
// canonical order (ordinal ASC, group_id ASC -- ordinal is unique per
// revision, the group_id tiebreak matches the adapter's keyset exactly)
// and re-applies the canonical round-trip on any non-empty result: stored
// rows that no longer canonicalize to themselves are
// ErrCorruptExplicitGroupRevision, mirroring the adapter's read-side
// verification. An empty set is legal only while the header is STAGING --
// exactly the adapter's listExplicitGroupDefinitions contract. For a
// COMPLETE header the adapter's digest/count branch also runs: the stored
// ordered definitions must re-derive the header's frozen definition digest
// and group count, or the revision is corruption -- history that no
// longer agrees with its own frozen facts is never served.
func (s *ExplicitGroupStore) listExplicitGroupDefinitions(ctx context.Context, conn explicitGroupConn, revisionID string, header *storedExplicitGroupHeader) ([]explorer.ExplicitGroupDefinition, error) {
	groups := make([]explorer.ExplicitGroupDefinition, 0)
	rows, err := conn.Query(ctx, s.stmts.defList, revisionID)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group definitions %q: %w", revisionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			groupID string
			label   string
			ordinal int64
		)
		if err := rows.Scan(&groupID, &label, &ordinal); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan explicit-group definition %q: %w", revisionID, err)
		}
		group := explorer.ExplicitGroupDefinition{ID: explorer.ExplicitGroupID(groupID), Label: label, Ordinal: ordinal}
		canonicalGroup := group.Canonical()
		if string(canonicalGroup.ID) != groupID || canonicalGroup.Label != label {
			// The stored row is not in canonical form: the adapter's
			// denormalized-mirror check as a relational read-side guard.
			return nil, fmt.Errorf("%w: definition %q of revision %q is stored non-canonically",
				explorer.ErrCorruptExplicitGroupRevision, groupID, revisionID)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group definitions %q: %w", revisionID, err)
	}
	if len(groups) == 0 && header.State == string(explorer.ExplicitGroupRevisionStaging) {
		return groups, nil
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", explorer.ErrCorruptExplicitGroupRevision, err)
	}
	if !reflect.DeepEqual(canonical, groups) {
		return nil, explorer.ErrCorruptExplicitGroupRevision
	}
	if header.State == string(explorer.ExplicitGroupRevisionComplete) {
		// The adapter's COMPLETE branch: history must agree with its own
		// frozen facts. Definitions that no longer re-derive the header's
		// definition digest and group count are corruption -- the frozen
		// facts only ever come from the fenced completion CAS, so a
		// disagreement means the stored rows were tampered with.
		digest, err := explorer.ExplicitGroupDefinitionDigest(groups)
		if err != nil || int64(len(groups)) != header.GroupCount || digest != header.DefinitionDigest {
			return nil, explorer.ErrCorruptExplicitGroupRevision
		}
	}
	return groups, nil
}

// AbortExplicitGroupRevision removes one staging revision, its membership
// rows, and its definition rows, idempotently and only for the owner:
// blank revision ID is a no-op, blank token is the typed conflict, a
// header that no longer exists is a no-op, and any header the writer guard
// refuses (non-STAGING or foreign token) is the typed conflict without
// removing anything. Memberships go first under the header lock, then the
// definitions, then the header itself under the state+token CAS -- the
// dependency order the two RESTRICT foreign keys demand; the migrations'
// DELETE triggers additionally demand the session-local abort-token GUC
// matching the stored writer token, so an un-authorized direct SQL delete
// -- of any of the three tables, including the active-writer case -- is
// refused by PostgreSQL itself. A header CAS miss
// after the definitions are gone is the typed conflict and the deferred
// rollback restores nothing (the whole operation is one transaction, so
// half-applied aborts are impossible).
func (s *ExplicitGroupStore) AbortExplicitGroupRevision(ctx context.Context, revisionID explorer.ExplicitGroupRevisionID, writerToken string) error {
	revisionID = explorer.ExplicitGroupRevisionID(strings.TrimSpace(string(revisionID)))
	if revisionID == "" {
		return nil
	}
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return explorer.ErrExplicitGroupRevisionConflict
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group abort connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group abort %q: %w", revisionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var header storedExplicitGroupHeader
	err = scanExplicitGroupHeader(tx.QueryRow(ctx, s.stmts.headerClaim, string(revisionID)), &header)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock explicit-group header %q: %w", revisionID, err)
	}
	if err := guardExplicitGroupWriter(&header, writerToken); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, s.stmts.setAbortTokenGUC, writerToken); err != nil {
		return fmt.Errorf("postgres explorer store: set abort guard for %q: %w", revisionID, err)
	}
	// Children leave in dependency order: memberships first (the
	// definitions are their foreign-key parent), then definitions, then
	// the header -- every DELETE rides the same abort-token
	// authorization.
	if _, err := tx.Exec(ctx, s.memberStmts.memberDeleteRevision, string(revisionID)); err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group memberships for abort %q: %w", revisionID, err)
	}
	if _, err := tx.Exec(ctx, s.stmts.defDeleteBatch, []string{string(revisionID)}); err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group definitions for abort %q: %w", revisionID, err)
	}

	// Test-only seam between the definition removal and the header CAS
	// (see afterDefinitionsInserted). An error here aborts the whole
	// transaction: the memberships, definitions, and header all stay.
	if s.afterDefinitionsDeleted != nil {
		if err := s.afterDefinitionsDeleted(ctx, tx); err != nil {
			return fmt.Errorf("postgres explorer store: abort explicit-group revision %q after definition delete: %w",
				revisionID, err)
		}
	}

	tag, err := tx.Exec(ctx, s.stmts.headerAbort, string(revisionID), writerToken)
	if err != nil {
		return fmt.Errorf("postgres explorer store: delete explicit-group header %q: %w", revisionID, err)
	}
	if tag.RowsAffected() != 1 {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group abort %q: %w", revisionID, err)
	}
	return nil
}

// CleanupExplicitGroupStaging reaps expired staging revisions: only rows
// still in STAGING whose created_at precedes before AND whose writer lease
// already expired (or which never carried one -- the adapter tolerates a
// missing writerExpiresAt the same way) are removed, bounded by limit
// (limit <= 0 means the default), oldest first. Active writers are never
// touched: the expiry filter keeps live leases in, and FOR UPDATE SKIP
// LOCKED keeps rows another writer is actively working out of the batch
// entirely. The sweep's clock is stamped into the cleanup GUC so the
// migration's DELETE triggers re-enforce "expired only" at the database on
// every removed row, and a concurrent commit that expired a row after our
// read still satisfies the same-clock check -- deletion converges to
// "expired at the sweep's clock" regardless of interleavings.
func (s *ExplicitGroupStore) CleanupExplicitGroupStaging(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = explicitGroupCleanupLimit
	}
	now := time.Now().UTC()

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire explicit-group cleanup connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin explicit-group cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, s.stmts.setCleanupNowGUC, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("postgres explorer store: set cleanup guard: %w", err)
	}
	rows, err := tx.Query(ctx, s.stmts.cleanupSelect, before, now, limit)
	if err != nil {
		return fmt.Errorf("postgres explorer store: select expired explicit-group headers: %w", err)
	}
	var expired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("postgres explorer store: scan expired explicit-group headers: %w", err)
		}
		expired = append(expired, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres explorer store: select expired explicit-group headers: %w", err)
	}
	if len(expired) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres explorer store: commit empty explicit-group cleanup: %w", err)
		}
		return nil
	}
	// Dependency order: memberships, then definitions, then headers --
	// every DELETE rides the same sweep-clock authorization the triggers
	// re-enforce per row.
	if _, err := tx.Exec(ctx, s.memberStmts.memberDeleteBatch, expired); err != nil {
		return fmt.Errorf("postgres explorer store: delete memberships of expired explicit-group headers: %w", err)
	}
	if _, err := tx.Exec(ctx, s.stmts.defDeleteBatch, expired); err != nil {
		return fmt.Errorf("postgres explorer store: delete definitions of expired explicit-group headers: %w", err)
	}
	tag, err := tx.Exec(ctx, s.stmts.headerDeleteBatch, expired)
	if err != nil {
		return fmt.Errorf("postgres explorer store: delete expired explicit-group headers: %w", err)
	}
	if int(tag.RowsAffected()) != len(expired) {
		return fmt.Errorf("postgres explorer store: cleanup removed %d explicit-group headers, selected %d: %w",
			tag.RowsAffected(), len(expired), explorer.ErrExplicitGroupRevisionConflict)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit explicit-group cleanup: %w", err)
	}
	return nil
}

// GetExplicitGroupRevision returns one COMPLETE header, tenant-scoped: a
// blank revision ID or a header that does not exist (including one that
// exists only under a different project) is ErrExplicitGroupRevisionNotFound
// with no information leak; a header still in STAGING is
// ErrExplicitGroupRevisionIncomplete -- staging headers are invisible on
// the public read path, exactly like the adapter's Get.
func (s *ExplicitGroupStore) GetExplicitGroupRevision(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group get connection: %w", err)
	}
	defer conn.Release()

	header, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return nil, err
	}
	domain, err := header.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	if domain.State != explorer.ExplicitGroupRevisionComplete {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	// Verify-on-read (E2 defense-in-depth): a COMPLETE header is served
	// only if its stored definition children still re-derive the frozen
	// group_count and definition_digest -- the exact refusal machinery
	// ListExplicitGroupDefinitions runs, so one verified path governs
	// every served header and any disagreement is the same typed
	// ErrCorruptExplicitGroupRevision. The children are
	// verified-then-discarded: no child data aliases into the served
	// header. Migration 000011 makes NEW drift unreachable by direct
	// SQL; this refuses any pre-existing or hypothetical corruption
	// instead of serving it.
	if _, err := s.listExplicitGroupDefinitions(ctx, conn, string(revisionID), header); err != nil {
		return nil, err
	}
	return domain, nil
}

// ListExplicitGroupRevisions lists only COMPLETE headers under one exact
// (project, generation, scope digest, resource type) scope, newest first
// (created_at DESC, then ID in exact byte order -- the adapter's
// createdAt DESC, _key ASC), limited to limit (limit <= 0 means the
// default 100). STAGING revisions are hidden from listing: they are not
// choices yet. The result is never nil. Only the fenced
// CompleteExplicitGroupRevision CAS can produce a COMPLETE header, so
// what listing shows is exactly what a verified completion froze --
// and every served row is additionally verified against its stored
// definition children (verify-on-read, whole-page fail-fast, typed
// ErrCorruptExplicitGroupRevision on any disagreement).
func (s *ExplicitGroupStore) ListExplicitGroupRevisions(ctx context.Context, project, generation, scopeDigest, resourceType string, limit int) ([]explorer.ExplicitGroupRevision, error) {
	if limit <= 0 {
		limit = explicitGroupListLimit
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group list connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, s.stmts.listComplete, project, generation, scopeDigest, resourceType, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group revisions %q/%q/%q/%q: %w",
			project, generation, scopeDigest, resourceType, err)
	}
	defer rows.Close()
	// The page is collected first (pgx forbids a second query on the
	// connection while the cursor is live), then EVERY served header is
	// verified against its stored definition children on this same
	// connection before anything is returned -- whole-page fail-fast.
	// There is deliberately no per-row skip: the signature carries no
	// per-row error channel, so skipping would silently re-enact the
	// served-divergence this check exists to refuse. One corrupted
	// header refuses the entire call with the typed
	// ErrCorruptExplicitGroupRevision and zero rows.
	stored := make([]storedExplicitGroupHeader, 0)
	for rows.Next() {
		var header storedExplicitGroupHeader
		if err := scanExplicitGroupHeader(rows, &header); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan explicit-group revision %q: %w", project, err)
		}
		stored = append(stored, header)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explicit-group revisions %q: %w", project, err)
	}
	rows.Close()
	revisions := make([]explorer.ExplicitGroupRevision, 0, len(stored))
	for i := range stored {
		domain, err := stored[i].toDomain("")
		if err != nil {
			return nil, err
		}
		if _, err := s.listExplicitGroupDefinitions(ctx, conn, stored[i].ID, &stored[i]); err != nil {
			return nil, err
		}
		revisions = append(revisions, *domain)
	}
	return revisions, nil
}

// ListExplicitGroupDefinitions returns the canonical ordered definition
// rows of one COMPLETE header. The header read is the public Get path, so
// a STAGING header is ErrExplicitGroupRevisionIncomplete, a missing or
// cross-project header is ErrExplicitGroupRevisionNotFound, and rows that
// fail the canonical round-trip are ErrCorruptExplicitGroupRevision.
// Order is stable across connections and processes because it is stored
// order (ordinal, group_id), not decode order.
func (s *ExplicitGroupStore) ListExplicitGroupDefinitions(ctx context.Context, project string, revisionID explorer.ExplicitGroupRevisionID) ([]explorer.ExplicitGroupDefinition, error) {
	if strings.TrimSpace(string(revisionID)) == "" {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire explicit-group definitions connection: %w", err)
	}
	defer conn.Release()
	stored, err := s.readHeaderUnlocked(ctx, conn, project, revisionID)
	if err != nil {
		return nil, err
	}
	domain, err := stored.toDomain(revisionID)
	if err != nil {
		return nil, err
	}
	if domain.State != explorer.ExplicitGroupRevisionComplete {
		return nil, explorer.ErrExplicitGroupRevisionIncomplete
	}
	return s.listExplicitGroupDefinitions(ctx, conn, string(revisionID), stored)
}

// readHeaderUnlocked resolves one header row for a read that already
// passed the public gate.
func (s *ExplicitGroupStore) readHeaderUnlocked(ctx context.Context, conn explicitGroupConn, project string, revisionID explorer.ExplicitGroupRevisionID) (*storedExplicitGroupHeader, error) {
	var header storedExplicitGroupHeader
	var err error
	if strings.TrimSpace(project) == "" {
		err = scanExplicitGroupHeader(conn.QueryRow(ctx, s.stmts.headerGet, string(revisionID)), &header)
	} else {
		err = scanExplicitGroupHeader(conn.QueryRow(ctx, s.stmts.headerGetScoped, strings.TrimSpace(project), string(revisionID)), &header)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrExplicitGroupRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read explicit-group header %q: %w", revisionID, err)
	}
	return &header, nil
}
