package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const (
	InterpretationLibrariesTable = "interpretation_libraries"
	InterpretationRevisionsTable = "interpretation_revisions"
)

const (
	interpretationRevisionImmutableTrigger = "interpretation_revisions_immutable"
	interpretationLibraryHeadGuardTrigger  = "interpretation_libraries_head_guard"
	interpretationRevisionGuardFunction    = "loom_interpretation_revision_immutable"
	interpretationLibraryGuardFunction     = "loom_interpretation_library_guard"

	interpretationRevisionPKConstraint       = "interpretation_revisions_identity_pk"
	interpretationRevisionParentFKConstraint = "interpretation_revisions_parent_same_library_fk"
	interpretationLibraryPKConstraint        = "interpretation_libraries_identity_pk"
	interpretationLibraryHeadFKConstraint    = "interpretation_libraries_head_same_library_fk"
)

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

type interpretationSQL struct {
	revisionInsert     string
	revisionGet        string
	parentDigestLookup string
	libraryInsertRoot  string
	libraryAdvance     string
	libraryList        string
}

func newInterpretationSQL(schema string) interpretationSQL {
	revTable := pgx.Identifier{schema, InterpretationRevisionsTable}.Sanitize()
	libTable := pgx.Identifier{schema, InterpretationLibrariesTable}.Sanitize()
	revCols := strings.Join(interpretationRevisionColumns, ", ")
	libCols := strings.Join(interpretationLibraryColumns, ", ")
	return interpretationSQL{
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

type InterpretationStore struct {
	client              *pgstore.Client
	sql                 interpretationSQL
	afterRevisionInsert func(context.Context, pgx.Tx) error
}

var _ explorer.InterpretationRepository = (*InterpretationStore)(nil)

func NewInterpretationStore(client *pgstore.Client, schema string) (*InterpretationStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: interpretation client is required")
	}
	if !pgstore.ValidSchemaName(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid interpretation schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &InterpretationStore{client: client, sql: newInterpretationSQL(schema)}, nil
}

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

func scanInterpretationRevisionRow(row rowScanner, s *storedInterpretationRevision) error {
	return row.Scan(&s.project, &s.id, &s.libraryID, &s.parentRevision, &s.parentDigest,
		&s.contentDigest, &s.author, &s.explanation, &s.createdAt, &s.payload)
}

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
		var existing storedInterpretationRevision
		err := scanInterpretationRevisionRow(tx.QueryRow(ctx, s.sql.revisionGet, revision.Project, string(revision.ID)), &existing)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("postgres explorer store: interpretation revision %q vanished during create: %w", revision.ID, err)
			}
			return nil, fmt.Errorf("postgres explorer store: re-read interpretation revision %q/%q: %w", revision.Project, revision.ID, err)
		}
		storedRevision, decodeErr := decodeInterpretationRevision(&existing)
		if decodeErr != nil {
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
		if pgstore.IsForeignKeyConstraintViolation(insertErr, interpretationRevisionParentFKConstraint) {
			return nil, errors.Join(explorer.ErrInterpretationParentConflict, insertErr)
		}
		return nil, fmt.Errorf("postgres explorer store: insert interpretation revision %q/%q: %w",
			revision.Project, revision.ID, insertErr)
	}

	if s.afterRevisionInsert != nil {
		if err := s.afterRevisionInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: create revision %q/%q after insert: %w",
				revision.Project, revision.LibraryID, err)
		}
	}

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

func textParam(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func digestParam(d *explorer.InterpretationContentDigest) any {
	if d == nil {
		return nil
	}
	return string(*d)
}

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
