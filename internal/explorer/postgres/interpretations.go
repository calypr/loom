package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/explorer"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// Interpretation persistence for the PostgreSQL 18 Explorer control plane:
// project-scoped library heads and immutable project revisions with
// transactional digest and parent CAS (the relational counterpart of
// explorer/arango/interpretations.go). The two tables are created by this
// ticket's state-scoped own-dir fixture
// tests/migrations-interpretations/000007_interpretation_library.sql through
// the shared pg-store-02 runner, composed with OwnerMigrations(); this file
// contains no DDL.
//
// The revision rows are the immutable, content-addressed facts: the ID is
// the domain's interpretation_<64 hex> hash over the identity envelope and
// the content digest is sha256 over the canonical content, so the same
// content always re-derives the same row and different immutable content can
// never share one. The library row is the mutable authoring head: exactly
// one CAS statement advances it, guarded on the expected parent, and the
// revision insert plus the head advance commit or roll back as one unit.
// Revisions are never UPDATEd or DELETEd -- the database enforces that with
// a 45501 trigger -- and a library head may only advance to a revision of
// its own library: the schema's composite foreign keys make a head pointing
// at another library's (or a nonexistent) revision a database refusal, not
// an adapter convention.

const (
	// InterpretationLibrariesTable holds one mutable authoring head per
	// (project, library): the head pointer may advance, never anything else.
	InterpretationLibrariesTable = "interpretation_libraries"
	// InterpretationRevisionsTable holds the immutable content-addressed
	// revisions, one row per (project, id).
	InterpretationRevisionsTable = "interpretation_revisions"
)

// Guard and constraint identifiers pinned by the fixture. The revision
// table rejects UPDATE and DELETE outright; the library table admits only
// the head advance (identity columns stable, head changed) and never a
// DELETE. Production references only interpretationRevisionParentFKConstraint
// and interpretationForeignKey below; the guard triggers, their functions,
// and the remaining constraint names are exercised by the suite's
// guard-drop and negative-constraint probes.
const (
	interpretationRevisionImmutableTrigger = "interpretation_revisions_immutable"
	interpretationLibraryHeadGuardTrigger  = "interpretation_libraries_head_guard"
	interpretationRevisionGuardFunction    = "loom_interpretation_revision_immutable"
	interpretationLibraryGuardFunction     = "loom_interpretation_library_guard"

	interpretationRevisionPKConstraint       = "interpretation_revisions_identity_pk"
	interpretationRevisionParentFKConstraint = "interpretation_revisions_parent_same_library_fk"
	interpretationLibraryPKConstraint        = "interpretation_libraries_identity_pk"
	interpretationLibraryHeadFKConstraint    = "interpretation_libraries_head_same_library_fk"

	// interpretationForeignKey is the 23503 class surfaced by the revision
	// insert when its composite parent foreign key fails. The adapter
	// never surfaces a raw duplicate key: ON CONFLICT DO NOTHING plus a
	// classified re-read turns the same-content race into the idempotent
	// path and the different-content race into
	// explorer.ErrImmutableInterpretation, so no unique-violation state is
	// named in production code.
	interpretationForeignKey = "23503"
)

// interpretationRevisionColumns is the single source of truth for the
// revision column order: INSERT, RETURNING, and the scoped reads all derive
// from it, so ordered parameters match the Go argument slices by
// construction. payload is the authoritative canonical revision document;
// the scalar mirrors beside it exist for scope guards, constraints, and
// ordered scans, and decodeInterpretationRevision re-checks every mirror
// against the payload on every read -- a lying mirror never reads back.
var interpretationRevisionColumns = []string{
	"project",
	"id",
	"library_id",
	"parent_revision_id",
	"parent_digest",
	"content_digest",
	"author",
	"explanation",
	"created_at",
	"payload",
}

// interpretationLibraryColumns is the single source of truth for the
// library column order.
var interpretationLibraryColumns = []string{
	"project",
	"id",
	"head_revision_id",
	"head_digest",
	"created_at",
	"updated_at",
}

func interpretationRevisionPlaceholders() string {
	parts := make([]string, len(interpretationRevisionColumns))
	for i := range interpretationRevisionColumns {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

func interpretationLibraryPlaceholders() string {
	parts := make([]string, len(interpretationLibraryColumns))
	for i := range interpretationLibraryColumns {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// interpretationSQL is the ordered, schema-qualified statement set the
// interpretation reads and the create CAS execute. Built once in
// NewInterpretationStore; every statement names only its own two tables.
type interpretationSQL struct {
	// revisionInsert writes one immutable revision row. ON CONFLICT (the
	// (project,id) identity) DO NOTHING makes the same-content race
	// convergent: the loser's insert returns no row and re-reads the
	// winner's row instead of failing.
	revisionInsert string
	// revisionGet resolves one opaque revision id inside exactly one
	// project. A foreign project's id is indistinguishable from a missing
	// id: the same concealed explorer.ErrNotFound.
	revisionGet string
	// parentDigestLookup resolves the parent inside its own project AND
	// library and returns its stored content digest, so the parent-digest
	// CAS check reads the persisted value rather than trusting the caller.
	parentDigestLookup string
	// libraryInsertRoot establishes the head for a brand-new library. If
	// the library already exists the insert matches zero rows: a root
	// create against a library whose head already moved is a parent
	// conflict, never an overwrite.
	libraryInsertRoot string
	// libraryAdvance is the single head CAS: it rewrites only head
	// columns, and only while the row still carries the expected head
	// revision. The identity and created_at columns are outside the SET
	// list; the schema guard refuses any statement that changes them.
	libraryAdvance string
	// libraryList is the project-scoped, byte-order listing (identity
	// columns are COLLATE "C", matching Arango's byte-order listing).
	libraryList string
}

func newInterpretationSQL(schema string) interpretationSQL {
	revTable := pgx.Identifier{schema, InterpretationRevisionsTable}.Sanitize()
	libTable := pgx.Identifier{schema, InterpretationLibrariesTable}.Sanitize()
	revCols := strings.Join(interpretationRevisionColumns, ", ")
	libCols := strings.Join(interpretationLibraryColumns, ", ")
	return interpretationSQL{
		// Bare ON CONFLICT DO NOTHING: the revision identity is guarded by
		// TWO unique indexes -- the (project, id) primary key and the
		// (project, id, library_id) key the same-library parent/head
		// foreign keys target. A targeted conflict clause would arbitrate
		// only the PK and let the same-content race surface a raw 23505 on
		// the second index. Foreign-key violations (23503) are not unique
		// conflicts and still surface as errors.
		revisionInsert: "INSERT INTO " + revTable + " (" + revCols + ") VALUES (" +
			interpretationRevisionPlaceholders() + ") " +
			"ON CONFLICT DO NOTHING RETURNING " + revCols,
		revisionGet: "SELECT " + revCols + " FROM " + revTable +
			" WHERE project = $1 AND id = $2",
		parentDigestLookup: "SELECT content_digest FROM " + revTable +
			" WHERE project = $1 AND id = $2 AND library_id = $3",
		libraryInsertRoot: "INSERT INTO " + libTable + " (" + libCols + ") VALUES (" +
			interpretationLibraryPlaceholders() + ") " +
			"ON CONFLICT DO NOTHING RETURNING " + libCols,
		libraryAdvance: "UPDATE " + libTable + " SET head_revision_id = $4, head_digest = $5, updated_at = $6" +
			" WHERE project = $1 AND id = $2 AND head_revision_id = $3 RETURNING " + libCols,
		libraryList: "SELECT " + libCols + " FROM " + libTable +
			" WHERE project = $1 ORDER BY id",
	}
}

// InterpretationStore is the PostgreSQL 18 counterpart of the Arango
// adapter's interpretation methods: project-scoped library listing,
// exact-ID revision reads, and the create-once revision write whose success
// atomically advances the library head under a parent CAS. It is the
// explorer.InterpretationRepository implementation for this backend; the
// interface is deliberately narrow (no library updates, no revision
// deletion) and the store exposes nothing wider.
type InterpretationStore struct {
	client *pgstore.Client
	sql    interpretationSQL
	// afterRevisionInsert is the test-only fault/interleaving seam of
	// CreateInterpretationRevision: it runs inside the live transaction
	// between the real revision INSERT and the real head CAS. Production
	// never assigns it (unexported, set only by interpretations_test.go);
	// an error aborts the transaction, so the revision row and any head
	// change both vanish together.
	afterRevisionInsert func(context.Context, pgx.Tx) error
}

var _ explorer.InterpretationRepository = (*InterpretationStore)(nil)

// NewInterpretationStore builds the interpretation store for one Explorer
// control-plane schema (the same schema this package's owner history
// migrates; the interpretation tables arrive through the composed fixture
// history). The schema name must be a safe lowercase identifier -- exactly
// the shape the pg-store-02 runner and New accept -- and every statement
// binds the pre-qualified, sanitized table references with ordered SQL
// parameters. It opens no connection: the shared pool stays shared.
func NewInterpretationStore(client *pgstore.Client, schema string) (*InterpretationStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: interpretation client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid interpretation schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &InterpretationStore{client: client, sql: newInterpretationSQL(schema)}, nil
}

// storedInterpretationRevision is one physical revision row: the mirror
// scalars plus the authoritative jsonb payload. The parent pair mirrors are
// nullable: SQL NULL on both sides is the root (no parent), which is also
// what lets the composite parent foreign key skip root rows. The
// interpretation_revisions_parent_pair CHECK keeps the pair NULL-together,
// and decodeInterpretationRevision proves the pair agrees with the payload.
type storedInterpretationRevision struct {
	project        string
	id             string
	libraryID      string
	parentRevision *string
	parentDigest   *string
	contentDigest  string
	author         string
	explanation    string
	createdAt      time.Time
	payload        json.RawMessage
}

// scanInterpretationRevisionRow reads one full revision row.
func scanInterpretationRevisionRow(row rowScanner, s *storedInterpretationRevision) error {
	return row.Scan(&s.project, &s.id, &s.libraryID, &s.parentRevision, &s.parentDigest,
		&s.contentDigest, &s.author, &s.explanation, &s.createdAt, &s.payload)
}

// decodeInterpretationRevision turns a physical row into the domain
// revision and proves the row is self-consistent the way the content
// address demands: the payload must be an object-shaped canonical revision
// whose recomputed content digest and revision ID match what it carries
// (explorer.ErrInterpretationIdentityMismatch via Validate), and every
// mirror column must agree with the payload it mirrors. Any disagreement
// fails closed -- a corrupt or tampered row never reads back as a valid
// revision.
func decodeInterpretationRevision(s *storedInterpretationRevision) (*explorer.InterpretationRevision, error) {
	if len(s.payload) == 0 {
		return nil, fmt.Errorf("%w: interpretation revision %q has an empty payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	var revision explorer.InterpretationRevision
	if err := json.Unmarshal(s.payload, &revision); err != nil {
		return nil, fmt.Errorf("%w: interpretation revision %q payload does not decode: %v", explorer.ErrInterpretationIdentityMismatch, s.id, err)
	}
	if err := revision.Validate(); err != nil {
		return nil, fmt.Errorf("%w: interpretation revision %q failed self-verification: %v", explorer.ErrInterpretationIdentityMismatch, s.id, err)
	}
	if revision.Project != s.project || s.project == "" {
		return nil, fmt.Errorf("%w: interpretation revision %q project mirror disagrees with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	if string(revision.ID) != s.id {
		return nil, fmt.Errorf("%w: interpretation revision %q id mirror disagrees with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	if string(revision.LibraryID) != s.libraryID {
		return nil, fmt.Errorf("%w: interpretation revision %q library mirror disagrees with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	if string(revision.ContentDigest) != s.contentDigest {
		return nil, fmt.Errorf("%w: interpretation revision %q content digest mirror disagrees with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	if revision.Author != s.author || revision.Explanation != s.explanation {
		return nil, fmt.Errorf("%w: interpretation revision %q authorship mirrors disagree with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	payloadParent, payloadDigest := "", ""
	if revision.ParentRevisionID != nil {
		payloadParent = string(*revision.ParentRevisionID)
	}
	if revision.ParentDigest != nil {
		payloadDigest = string(*revision.ParentDigest)
	}
	mirrorParent, mirrorDigest := "", ""
	if s.parentRevision != nil {
		mirrorParent = *s.parentRevision
	}
	if s.parentDigest != nil {
		mirrorDigest = *s.parentDigest
	}
	if payloadParent != mirrorParent || payloadDigest != mirrorDigest {
		return nil, fmt.Errorf("%w: interpretation revision %q parent mirrors disagree with its payload", explorer.ErrInterpretationIdentityMismatch, s.id)
	}
	return &revision, nil
}

// ListInterpretationLibraries returns every library head in one project,
// sorted by library ID in exact byte order (identity columns are COLLATE
// "C"). The result is never nil: a project without libraries lists as an
// empty slice. Each row is validated on read (canonical project, opaque ID
// shapes, head digest shape); a persisted row that no longer satisfies the
// domain contract fails closed rather than being listed.
func (s *InterpretationStore) ListInterpretationLibraries(ctx context.Context, rawProject string) ([]explorer.InterpretationLibrary, error) {
	project, err := explorer.CanonicalInterpretationProject(rawProject)
	if err != nil {
		return nil, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire list-libraries connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, s.sql.libraryList, project)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list interpretation libraries %q: %w", project, err)
	}
	defer rows.Close()

	out := []explorer.InterpretationLibrary{}
	for rows.Next() {
		var row libRow
		if err := scanInterpretationLibraryRow(rows, &row); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan interpretation library %q: %w", project, err)
		}
		library := explorer.InterpretationLibrary{
			ID:             explorer.InterpretationLibraryID(row.id),
			Project:        row.project,
			HeadRevisionID: explorer.InterpretationRevisionID(row.headRevision),
			HeadDigest:     explorer.InterpretationContentDigest(row.headDigest),
		}
		if err := validatePersistedInterpretationLibrary(library, project); err != nil {
			return nil, fmt.Errorf("postgres explorer store: interpretation library %q failed validation: %w", project, err)
		}
		out = append(out, library)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list interpretation libraries %q: %w", project, err)
	}
	return out, nil
}

// validatePersistedInterpretationLibrary is the read-side counterpart of
// the schema's shape constraints, mirroring the Arango adapter's
// validateInterpretationLibrary: canonical project, parseable opaque IDs,
// and a head digest in the sha256 form. A stored row that violates the
// domain contract is a read failure, never a silent listing.
func validatePersistedInterpretationLibrary(value explorer.InterpretationLibrary, project string) error {
	if value.Project != project {
		return fmt.Errorf("persisted interpretation library project %q escaped its scope", value.Project)
	}
	if _, err := explorer.NewInterpretationLibraryID(string(value.ID)); err != nil {
		return err
	}
	if string(value.HeadRevisionID) == "" {
		return fmt.Errorf("interpretation library %q has an empty head", value.ID)
	}
	if _, err := explorer.NewInterpretationRevisionID(string(value.HeadRevisionID)); err != nil {
		return err
	}
	if _, err := explorer.NewInterpretationContentDigest(string(value.HeadDigest)); err != nil {
		return err
	}
	return nil
}

// GetInterpretationRevision resolves one exact immutable revision by
// opaque ID inside exactly one project. Foreign-project IDs, malformed
// IDs, and missing IDs all conceal behind the same typed outcome: missing
// and foreign IDs return explorer.ErrNotFound with no signal whether the
// ID exists elsewhere; a malformed ID is the domain's own typed invalid-ID
// error. Old revisions remain retrievable forever: nothing here reads the
// head, and the table has no delete path.
func (s *InterpretationStore) GetInterpretationRevision(ctx context.Context, rawProject string, id explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	project, err := explorer.CanonicalInterpretationProject(rawProject)
	if err != nil {
		return nil, err
	}
	if _, err := explorer.NewInterpretationRevisionID(string(id)); err != nil {
		return nil, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire get-revision connection: %w", err)
	}
	defer conn.Release()

	var stored storedInterpretationRevision
	err = scanInterpretationRevisionRow(conn.QueryRow(ctx, s.sql.revisionGet, project, string(id)), &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read interpretation revision %q/%q: %w", project, id, err)
	}
	revision, err := decodeInterpretationRevision(&stored)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: %w", err)
	}
	return revision, nil
}

// CreateInterpretationRevision is the create-once CAS of the PostgreSQL
// adapter, the exact counterpart of the Arango adapter's
// CreateInterpretationRevision. The caller-supplied ID and digest are never
// trusted: explorer.PrepareInterpretationRevision canonicalizes the content
// and recomputes both immutable identities first, so the same digest retried
// re-derives the same row and different immutable content can never claim an
// existing identity.
//
// Everything the write does happens inside ONE transaction on one acquired
// connection:
//
//  1. The caller's expected-parent claim must match the content's own
//     parent (a mismatch is explorer.ErrInterpretationParentConflict before
//     anything is touched -- the same pre-flight the Arango adapter runs).
//  2. When the content names a parent, the persisted parent inside its own
//     project AND library is read and its stored content digest must equal
//     the claimed parentDigest. Missing parent, foreign-library parent, or
//     digest drift is explorer.ErrInterpretationParentConflict.
//  3. The revision INSERT runs ON CONFLICT (project, id) DO NOTHING. If no
//     row was inserted, the stored row is re-read and its identity envelope
//     compared with the prepared one: equal means the idempotent path (the
//     same digest retried returns the stored revision unchanged); different
//     means different immutable content was presented for this identity and
//     is refused with explorer.ErrImmutableInterpretation -- never an
//     overwrite.
//  4. The head advances in the same transaction: a root create inserts the
//     library row (an existing library means the root CAS lost --
//     ErrInterpretationParentConflict), and a child create applies the
//     guarded UPDATE ... WHERE head_revision_id = expected. Zero matched
//     rows is explorer.ErrInterpretationParentConflict, and the deferred
//     rollback removes the step-3 revision row: CAS and revision are one
//     atomic unit, never half-applied.
//
// Concurrent child revisions claiming the same expected head serialize on
// the library row: the winner's guarded UPDATE commits, the loser's
// re-evaluated predicate matches zero rows and its own revision row
// disappears with its rollback. There is no retry, no fallback to another
// backend, and no silently absorbed error: every failure other than the
// typed CAS/collision classes is returned wrapped with its statement.
func (s *InterpretationStore) CreateInterpretationRevision(ctx context.Context, input explorer.InterpretationRevision, expectedParent *explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	revision, err := explorer.PrepareInterpretationRevision(input)
	if err != nil {
		return nil, err
	}
	expected := ""
	if expectedParent != nil {
		expected = string(*expectedParent)
	}
	parent := ""
	if revision.ParentRevisionID != nil {
		parent = string(*revision.ParentRevisionID)
	}
	if expected != parent {
		return nil, explorer.ErrInterpretationParentConflict
	}
	preparedEnvelope, err := revision.RevisionIdentityEnvelope()
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: build prepared identity envelope: %w", err)
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire create-revision connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin create interpretation revision %q/%q: %w", revision.Project, revision.LibraryID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. The persisted parent must exist inside its own project AND
	// library and carry the claimed digest: the CAS reads the database's
	// value, not the caller's.
	if parent != "" {
		var persistedDigest string
		err := tx.QueryRow(ctx, s.sql.parentDigestLookup, revision.Project, parent, string(revision.LibraryID)).Scan(&persistedDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, explorer.ErrInterpretationParentConflict
		}
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: read parent interpretation revision %q/%q: %w", revision.Project, parent, err)
		}
		if revision.ParentDigest == nil || string(*revision.ParentDigest) != persistedDigest {
			return nil, explorer.ErrInterpretationParentConflict
		}
	}

	// 2. Insert the immutable revision. Same-content races converge here:
	// the loser's ON CONFLICT DO NOTHING inserts nothing and re-reads the
	// winner's row below.
	payload, err := json.Marshal(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode interpretation revision payload: %w", err)
	}
	createdAt := revision.CreatedAt
	var stored storedInterpretationRevision
	insertErr := scanInterpretationRevisionRow(tx.QueryRow(ctx, s.sql.revisionInsert,
		revision.Project, string(revision.ID), string(revision.LibraryID),
		textParam(parent), digestParam(revision.ParentDigest), string(revision.ContentDigest),
		revision.Author, revision.Explanation, createdAt, json.RawMessage(payload),
	), &stored)
	if errors.Is(insertErr, pgx.ErrNoRows) {
		// The identity already exists -- inserted here or by a competing
		// writer. Classify by the stored row's identity envelope.
		var existing storedInterpretationRevision
		err := scanInterpretationRevisionRow(tx.QueryRow(ctx, s.sql.revisionGet, revision.Project, string(revision.ID)), &existing)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The row vanished between skip and read: it was an
				// uncommitted writer that rolled back. This CAS must not
				// report success over a row that no longer exists.
				return nil, fmt.Errorf("postgres explorer store: interpretation revision %q vanished during create: %w", revision.ID, err)
			}
			return nil, fmt.Errorf("postgres explorer store: re-read interpretation revision %q/%q: %w", revision.Project, revision.ID, err)
		}
		storedRevision, decodeErr := decodeInterpretationRevision(&existing)
		if decodeErr != nil {
			// A stored row for this identity that does not satisfy the
			// content contract is exactly the collision the Arango adapter
			// fails closed on: different immutable content behind one ID.
			return nil, errors.Join(explorer.ErrImmutableInterpretation, decodeErr)
		}
		existingEnvelope, envelopeErr := storedRevision.RevisionIdentityEnvelope()
		if envelopeErr != nil {
			return nil, errors.Join(explorer.ErrImmutableInterpretation, envelopeErr)
		}
		if !sameInterpretationEnvelope(preparedEnvelope, existingEnvelope) {
			return nil, explorer.ErrImmutableInterpretation
		}
		return storedRevision, nil
	}
	if insertErr != nil {
		var pgErr *pgconn.PgError
		if errors.As(insertErr, &pgErr) && pgErr.Code == interpretationForeignKey &&
			pgErr.ConstraintName == interpretationRevisionParentFKConstraint {
			// The parent row vanished after the digest read or pointed at
			// another library: the database refuses the child. The domain
			// conflict retains the real PostgreSQL cause.
			return nil, errors.Join(explorer.ErrInterpretationParentConflict, insertErr)
		}
		return nil, fmt.Errorf("postgres explorer store: insert interpretation revision %q/%q: %w",
			revision.Project, revision.ID, insertErr)
	}

	// Test-only seam: exercises faults and interleavings between the real
	// revision insert and the real head CAS inside this same live
	// transaction. Always nil in production.
	if s.afterRevisionInsert != nil {
		if err := s.afterRevisionInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: create revision %q/%q after insert: %w",
				revision.Project, revision.LibraryID, err)
		}
	}

	// 3. Advance the authoring head in this same transaction.
	var headLib libRow
	if expected == "" {
		err = scanInterpretationLibraryRow(tx.QueryRow(ctx, s.sql.libraryInsertRoot,
			revision.Project, string(revision.LibraryID), string(revision.ID),
			string(revision.ContentDigest), createdAt, createdAt), &headLib)
	} else {
		err = scanInterpretationLibraryRow(tx.QueryRow(ctx, s.sql.libraryAdvance,
			revision.Project, string(revision.LibraryID), expected,
			string(revision.ID), string(revision.ContentDigest), createdAt), &headLib)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// The head moved (or the library existed) after the CAS claim:
		// this writer lost and the deferred rollback removes its revision
		// row -- half-applied state is impossible.
		return nil, explorer.ErrInterpretationParentConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: advance interpretation library head %q/%q: %w",
			revision.Project, revision.LibraryID, err)
	}
	if headLib.headRevision != string(revision.ID) || headLib.headDigest != string(revision.ContentDigest) {
		return nil, fmt.Errorf("postgres explorer store: interpretation library %q/%q head does not bind the accepted revision",
			revision.Project, revision.LibraryID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit create interpretation revision %q/%q: %w",
			revision.Project, revision.ID, err)
	}
	created, err := decodeInterpretationRevision(&stored)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: created %w", err)
	}
	return created, nil
}

// sameInterpretationEnvelope compares two identity envelopes by their
// canonical JSON bytes. Both envelopes come from
// InterpretationRevision.RevisionIdentityEnvelope (a map encoded with
// sorted keys), so byte equality is exact semantic equality.
func sameInterpretationEnvelope(a, b map[string]any) bool {
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ja) == string(jb)
}

// textParam renders an optional mirror column: SQL NULL stands in for the
// empty string, matching the schema's paired-NULL parent contract (the
// composite parent foreign key skips rows whose parent columns are NULL).
func textParam(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// digestParam renders the optional parent-digest mirror.
func digestParam(d *explorer.InterpretationContentDigest) any {
	if d == nil {
		return nil
	}
	return string(*d)
}

// libRow is one physical library row.
type libRow struct {
	project      string
	id           string
	headRevision string
	headDigest   string
	createdAt    time.Time
	updatedAt    time.Time
}

func scanInterpretationLibraryRow(row rowScanner, l *libRow) error {
	return row.Scan(&l.project, &l.id, &l.headRevision, &l.headDigest, &l.createdAt, &l.updatedAt)
}
