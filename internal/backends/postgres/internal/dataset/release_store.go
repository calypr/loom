package postgres

// Project release persistence over PostgreSQL 18 (pg-publication-project-
// release-cas-v1): immutable ProjectRelease candidates and the active
// project release compare-and-swap pointer.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication row store. This file adds no data-plane rows and never falls
// back to another backend: every failure is returned as a named error for
// the caller to surface, and no method consults Arango or ClickHouse.
//
// Ownership follows the architecture contract. ReleaseStore owns only
// project_release (the immutable candidate archive) and
// active_project_release (the active-visibility pointer). The dataset
// generation manifest and the active-generation pointer stay owned by the
// generation lane (Store in this package, migration 000003): the generation
// owner participates in release activation through GenerationOwnerAdapter
// inside the activation's own transaction, and neither side duplicates the
// other's schema.
//
// Guarantees:
//   - Immutability: candidates are content-addressed documents keyed on
//     (project, id). Re-saving identical content (createdAt excluded, as in
//     the Arango baseline) is accepted and returns the stored document
//     untouched; changed content under the same id is refused with
//     ErrReleaseContentConflict. No statement ever updates candidate rows,
//     so a lost activation race keeps the candidate retained for audit.
//   - Atomic compare-and-swap: the candidate check, the generation-owner
//     participation, the pointer comparison, and the pointer write all run
//     in one transaction. Two activations presenting the same expected
//     revision produce exactly one winner; the loser gets the typed
//     publication.ErrReleaseActivationConflict and no pointer moves. An
//     already-active candidate short-circuits to a zero-write,
//     revision-stable success (the F-B15 rule), and expected revision is
//     never compared against a pointer that already names the candidate.
//   - Missing versus JSON-null versus SQL-NULL: the payload column stores
//     the caller's JSON document unchanged (authoritative on read), every
//     column is NOT NULL, and the command layer binds no SQL NULL, so the
//     three states stay distinct. Absent visibility is a missing pointer
//     row, never a SQL NULL.
//   - Backend-tagged commands with ordered positional SQL parameters; an
//     untagged or foreign command fails closed before execution.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// ProjectReleasesTable stores immutable ProjectRelease candidates.
	ProjectReleasesTable = "project_release"
	// ActiveProjectReleasesTable stores the one-row-per-project active
	// release pointer with its CAS revision.
	ActiveProjectReleasesTable = "active_project_release"
)

// Sentinel errors for the release store, classified without string matching.
var (
	// ErrNilReleaseConnSource reports construction without a connection
	// source.
	ErrNilReleaseConnSource = errors.New("dataset postgres: release store connection source is required")
	// ErrReleaseContentConflict reports a SaveRelease whose (project, id)
	// already stores a candidate with different content (createdAt
	// excluded). The stored document is left untouched.
	ErrReleaseContentConflict = errors.New("project release candidate already exists with different content")
	// ErrReleaseActivationConflict aliases the public domain sentinel so
	// callers classify activation compare-and-swap losses and refused
	// candidates without importing this package's vocabulary. It covers an
	// unpersisted or stale candidate, a refused generation-owner guard, and
	// a lost revision race -- each wrapped with the distinguishing detail.
	// Backend faults are deliberately NOT classified as conflicts.
	ErrReleaseActivationConflict = publication.ErrReleaseActivationConflict
)

var _ publication.ReleaseRepository = (*ReleaseStore)(nil)

// ReleaseActivation describes the activation decision handed to the
// generation owner inside the activation transaction, after the active
// release pointer moved onto this candidate and before COMMIT.
type ReleaseActivation struct {
	// Project is the project whose pointer moved.
	Project string
	// Release is the persisted immutable candidate that became active.
	Release publication.ProjectRelease
	// Revision is the pointer revision the activation committed.
	Revision int64
}

// GenerationOwnerAdapter is the seam through which the dataset generation
// owner -- the lane that owns dataset_generation_manifest and
// dataset_active_generation (migration 000003) -- participates in release
// activation without duplicated schema. Both methods run on the very
// pgx.Tx that compares and swaps the active release pointer: the adapter
// never opens its own transaction, and it reads or writes only its own
// tables. Neither side mirrors the other's columns.
//
// The default implementation is the datasetGenerationOwner bound to the
// same schema as the store, enforcing the same activation filters the
// Arango baseline applies inside activateReleaseAQL (persisted manifest in
// STAGED/READY state; existing active-generation pointer). Tests inject
// adapters to prove same-transaction participation and rollback.
type GenerationOwnerAdapter interface {
	// EnsureGenerationPrerequisites runs inside the activation transaction
	// before the active release pointer is compared. Returning an error
	// (typically wrapping publication.ErrReleaseActivationConflict) fails
	// the activation; nothing has been written yet at this point.
	EnsureGenerationPrerequisites(ctx context.Context, tx pgx.Tx, project, generation string) error
	// OnReleaseActivated runs inside the same transaction immediately after
	// the active release pointer moved onto the candidate, before COMMIT.
	// Returning an error aborts the whole activation: the pointer write
	// never commits. It is never called on the zero-write already-active
	// path, matching the Arango baseline's zero-write F-B15 rule.
	OnReleaseActivated(ctx context.Context, tx pgx.Tx, activation ReleaseActivation) error
}

// ReleaseStore is one schema-bound view of the project release tables.
// Create one with NewReleaseStore; it is safe for concurrent use.
type ReleaseStore struct {
	src      ConnSource
	releases string
	pointers string
	adapter  GenerationOwnerAdapter
	// commitFault, when non-nil, is invoked immediately before COMMIT in
	// every transactional write path; returning an error aborts the
	// transaction. It is a test seam mirroring Store.commitFault;
	// production construction always leaves it nil.
	commitFault func(step string) error
}

// NewReleaseStore validates the connection source and target schema and
// binds the store to <schema>.project_release and
// <schema>.active_project_release. The caller owns the schema lifecycle;
// this store requires the schema and its migrations to exist and never
// creates, drops, or migrates anything. The generation owner participates
// through the default same-schema GenerationOwnerAdapter.
func NewReleaseStore(src ConnSource, schema string) (*ReleaseStore, error) {
	if src == nil {
		return nil, ErrNilReleaseConnSource
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	s := &ReleaseStore{
		src:      src,
		releases: pgx.Identifier{schema, ProjectReleasesTable}.Sanitize(),
		pointers: pgx.Identifier{schema, ActiveProjectReleasesTable}.Sanitize(),
	}
	s.adapter = &datasetGenerationOwner{
		manifests: pgx.Identifier{schema, "dataset_generation_manifest"}.Sanitize(),
		pointer:   pgx.Identifier{schema, "dataset_active_generation"}.Sanitize(),
	}
	return s, nil
}

func (s *ReleaseStore) validate() error {
	if s == nil || s.src == nil {
		return ErrNilReleaseConnSource
	}
	return nil
}

// ---------------------------------------------------------------------------
// payload encoding and validation
// ---------------------------------------------------------------------------

// validateRelease enforces the store's intake contract. The release id is an
// opaque immutable content label (internal/dataset/release.go): this store
// never re-derives it, it only requires it to be present and consistent
// with the persisted document.
func validateRelease(release publication.ProjectRelease) error {
	if release.ID == "" {
		return fmt.Errorf("project release: candidate id is required")
	}
	ref := publication.Ref{Project: release.Project, Generation: release.Generation}
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("project release: %w", err)
	}
	if release.GitCommit == "" {
		return fmt.Errorf("project release: gitCommit is required")
	}
	for _, binding := range release.Publications {
		if err := binding.Selector.Validate(); err != nil {
			return fmt.Errorf("project release: publication selector %v: %w", binding.Selector, err)
		}
	}
	for _, verification := range release.RequiredVerifications {
		if err := verification.Selector.Validate(); err != nil {
			return fmt.Errorf("project release: verification selector %v: %w", verification.Selector, err)
		}
	}
	return nil
}

// marshalReleasePayload canonicalizes the nil-versus-empty slice form (the
// content digest already treats them as one service form) and encodes the
// document for storage. Encoding is the plain JSON of the domain struct, so
// the persisted bytes carry the public release fields unchanged; a JSON
// null inside a field stays a JSON null and is never promoted to SQL NULL.
func marshalReleasePayload(release publication.ProjectRelease) (string, error) {
	normalized := release
	if normalized.Publications == nil {
		normalized.Publications = []publication.ReleasePublication{}
	}
	if normalized.RequiredVerifications == nil {
		normalized.RequiredVerifications = []publication.ContractVerification{}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode project release payload: %w", err)
	}
	return string(encoded), nil
}

// releaseFromRow rebuilds a candidate from one stored row, verifying that
// the scan columns agree with the authoritative payload. Disagreement,
// unknown shapes, or malformed documents are store inconsistencies, never
// coerced.
func releaseFromRow(project, id, generation, gitCommit, payloadDB string) (publication.ProjectRelease, error) {
	var release publication.ProjectRelease
	decoder := json.NewDecoder(bytes.NewReader([]byte(payloadDB)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("%w: stored release payload is malformed: %v", ErrUnexpectedStoreResult, err)
	}
	if err := validateRelease(release); err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("%w: stored release is invalid: %v", ErrUnexpectedStoreResult, err)
	}
	if release.Project != project || release.ID != id || release.Generation != generation || release.GitCommit != gitCommit {
		return publication.ProjectRelease{}, fmt.Errorf("%w: stored release scan columns disagree with its payload for %s/%s",
			ErrUnexpectedStoreResult, project, id)
	}
	return release, nil
}

// ---------------------------------------------------------------------------
// SaveRelease -- immutable candidate persistence
// ---------------------------------------------------------------------------

// SaveRelease retains the immutable candidate without moving active
// visibility. The INSERT ... ON CONFLICT DO NOTHING is the whole write: the
// store never UPDATEs a candidate row, so the first stored document (and
// its createdAt) is the durable record. Re-saving content-identical bytes
// (createdAt excluded from the comparison, mirroring the Arango baseline's
// compatible guard) is accepted and returns the stored document; the same
// id with changed content is refused with the typed ErrReleaseContentConflict
// and the stored row stays byte-identical. Inactive candidates stay
// retained for audit; this store never deletes release history.
func (s *ReleaseStore) SaveRelease(ctx context.Context, release publication.ProjectRelease) (publication.ProjectRelease, error) {
	if err := s.validate(); err != nil {
		return publication.ProjectRelease{}, err
	}
	if err := validateRelease(release); err != nil {
		return publication.ProjectRelease{}, err
	}
	payload, err := marshalReleasePayload(release)
	if err != nil {
		return publication.ProjectRelease{}, err
	}

	insert, err := NewPostgreSQLCommand(
		"INSERT INTO "+s.releases+` (project, id, generation, git_commit, payload)
VALUES ($1, $2, $3, $4, $5::jsonb)
ON CONFLICT DO NOTHING
RETURNING payload::text`,
		[]Parameter{
			{Type: ParameterText, Value: release.Project},
			{Type: ParameterText, Value: release.ID},
			{Type: ParameterText, Value: release.Generation},
			{Type: ParameterText, Value: release.GitCommit},
			{Type: ParameterJSONB, Value: payload},
		})
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}

	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: acquire connection: %w", err)
	}
	defer conn.Release()

	var storedPayload string
	scanErr := queryRowConn(ctx, conn, insert).Scan(&storedPayload)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		// The (project, id) row already exists. Compare content the way
		// the baseline does: everything except the first-creation
		// timestamp, with jsonb equality evaluated by the server.
		return s.savedCandidateConflict(ctx, conn, release, payload)
	}
	if scanErr != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", scanErr)
	}

	stored, err := releaseFromRow(release.Project, release.ID, release.Generation, release.GitCommit, storedPayload)
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	// The row the server stored must decode back to exactly the document
	// the caller submitted; any server-side or disk-level drift fails
	// closed instead of echoing the caller's struct.
	var submitted publication.ProjectRelease
	if err := json.NewDecoder(bytes.NewReader([]byte(payload))).Decode(&submitted); err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	if !reflect.DeepEqual(stored, submitted) {
		return publication.ProjectRelease{}, fmt.Errorf("%w: stored release does not match the submitted candidate", ErrUnexpectedStoreResult)
	}
	return stored, nil
}

// savedCandidateConflict resolves an ON CONFLICT miss: identical content is
// an accepted retry returning the untouched stored document; anything else
// is a typed content conflict.
func (s *ReleaseStore) savedCandidateConflict(ctx context.Context, conn *pgxpool.Conn, release publication.ProjectRelease, payload string) (publication.ProjectRelease, error) {
	lookup, err := NewPostgreSQLCommand(
		"SELECT (payload - 'createdAt') = ($2::jsonb - 'createdAt'), payload::text FROM "+s.releases+`
WHERE project = $1 AND id = $3`,
		[]Parameter{
			{Type: ParameterText, Value: release.Project},
			{Type: ParameterJSONB, Value: payload},
			{Type: ParameterText, Value: release.ID},
		})
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	var (
		compatible    bool
		storedPayload string
	)
	scanErr := queryRowConn(ctx, conn, lookup).Scan(&compatible, &storedPayload)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return publication.ProjectRelease{}, fmt.Errorf("%w: release row vanished during the save retry race", ErrUnexpectedStoreResult)
	}
	if scanErr != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", scanErr)
	}
	if !compatible {
		return publication.ProjectRelease{}, fmt.Errorf("%w: %s/%s already stores a different document",
			ErrReleaseContentConflict, release.Project, release.ID)
	}
	stored, err := releaseFromRow(release.Project, release.ID, release.Generation, release.GitCommit, storedPayload)
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	return stored, nil
}

// ---------------------------------------------------------------------------
// ReadActiveRelease
// ---------------------------------------------------------------------------

// ReadActiveRelease returns the candidate the project's active release
// pointer names, with the pointer revision. A missing pointer row means no
// active release (ErrNoActiveRelease); there is no placeholder row, no
// SQL NULL stand-in, and no fallback to another candidate or backend.
func (s *ReleaseStore) ReadActiveRelease(ctx context.Context, project string) (publication.ActiveRelease, error) {
	if err := validateProject(project); err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("read active release: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.ActiveRelease{}, err
	}
	command, err := NewPostgreSQLCommand(
		"SELECT r.project, r.id, r.generation, r.git_commit, r.payload::text, p.revision FROM "+s.pointers+` p
JOIN `+s.releases+` r
  ON r.project = p.project AND r.id = p.release_id
WHERE p.project = $1`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("read active release: %w", err)
	}

	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("read active release: acquire connection: %w", err)
	}
	defer conn.Release()

	var (
		rowProject, rowID, rowGeneration, rowGitCommit string
		storedPayload                                  string
		revision                                       int64
	)
	scanErr := queryRowConn(ctx, conn, command).Scan(&rowProject, &rowID, &rowGeneration, &rowGitCommit, &storedPayload, &revision)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return publication.ActiveRelease{}, fmt.Errorf("%w: %s", publication.ErrNoActiveRelease, project)
	}
	if scanErr != nil {
		return publication.ActiveRelease{}, fmt.Errorf("read active release: %w", scanErr)
	}
	release, err := releaseFromRow(rowProject, rowID, rowGeneration, rowGitCommit, storedPayload)
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("read active release: %w", err)
	}
	if revision < 1 {
		return publication.ActiveRelease{}, fmt.Errorf("%w: active release revision %d is below the stored floor", ErrUnexpectedStoreResult, revision)
	}
	return publication.ActiveRelease{Release: release, Revision: revision}, nil
}

// ---------------------------------------------------------------------------
// CompareAndSwapActivateRelease
// ---------------------------------------------------------------------------

// CompareAndSwapActivateRelease atomically moves the project's active
// release pointer onto a persisted candidate. The entire operation --
// candidate verification, generation-owner participation, pointer
// comparison, pointer write, and the activation hook -- runs in one
// PostgreSQL transaction. There is no check-then-act window: two
// activations presenting the same expected revision produce exactly one
// winner, and the loser receives the typed publication.ErrReleaseActivationConflict
// with the pointer unchanged.
//
// Rules (mirroring the Arango baseline's activateReleaseAQL):
//   - The candidate must already be persisted under (project, id) and its
//     stored content must still match the caller's candidate (createdAt
//     excluded), or the activation is refused as a conflict. A candidate
//     that was never saved can never become active.
//   - The generation owner's adapter verifies the generation prerequisites
//     (persisted manifest in STAGED/READY, existing active-generation
//     pointer) inside this same transaction; refusals are typed conflicts
//     and stop the activation before any write.
//   - If the pointer already names this exact candidate the activation is
//     the F-B15 zero-write no-op: current revision returned, no UPDATE,
//     no INSERT, not even updated_at churn. This takes precedence over the
//     expected revision, exactly as the baseline's "currentRevision ==
//     @expected_revision OR alreadyActive" predicate does.
//   - Otherwise the pointer's current revision is compared: a mismatch is
//     a typed conflict that changes no pointer, and a match moves the
//     pointer to the candidate at expectedRevision+1 through an UPDATE
//     whose WHERE clause re-compares the revision (the CAS). With no
//     pointer row only expectedRevision 0 may create one, via INSERT ON
//     CONFLICT with a bounded re-read so a concurrent winner either names
//     this exact candidate (accepted, zero write) or is reported as the
//     lost race.
//   - Backend faults are never classified as conflicts; they surface as
//     plain errors so callers keep them retryable/unavailable.
func (s *ReleaseStore) CompareAndSwapActivateRelease(ctx context.Context, release publication.ProjectRelease, expectedRevision int64) (publication.ActiveRelease, error) {
	if err := s.validate(); err != nil {
		return publication.ActiveRelease{}, err
	}
	if err := validateRelease(release); err != nil {
		return publication.ActiveRelease{}, err
	}
	if expectedRevision < 0 {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: expectedRevision must not be negative, got %d", expectedRevision)
	}
	payload, err := marshalReleasePayload(release)
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	project := release.Project
	generation := release.Generation

	// 1. The candidate must be a persisted, still-matching immutable
	//    document. FOR SHARE holds the row against concurrent modification
	//    for the transaction's duration (this store itself never updates
	//    candidates).
	lockCandidate, err := NewPostgreSQLCommand(
		"SELECT payload::text, (payload - 'createdAt') = ($3::jsonb - 'createdAt') FROM "+s.releases+`
WHERE project = $1 AND id = $2
FOR SHARE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: release.ID},
			{Type: ParameterJSONB, Value: payload},
		})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	// 2. The generation owner's rows are locked and verified inside this
	//    same transaction by the GenerationOwnerAdapter (step 2 of the
	//    transaction body below), which holds its own statements.
	// 3. The active release pointer: locked, compared, and moved inside
	//    this transaction only.
	lockPointer, err := NewPostgreSQLCommand(
		"SELECT release_id, revision FROM "+s.pointers+`
WHERE project = $1
FOR UPDATE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	readPointer, err := NewPostgreSQLCommand(
		"SELECT release_id, revision FROM "+s.pointers+`
WHERE project = $1`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	insertPointer, err := NewPostgreSQLCommand(
		"INSERT INTO "+s.pointers+` (project, release_id, revision)
VALUES ($1, $2, 1)
ON CONFLICT DO NOTHING`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: release.ID},
		})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	// The compare-and-swap UPDATE (newCASPointer, built with the concrete
	// revision values at the call site) compares the revision again in its
	// WHERE clause: the pointer can never move without holding the exact
	// value the transaction observed.

	var result *publication.ActiveRelease
	err = s.inTx(ctx, "activate-release", func(tx pgx.Tx) error {
		// 1. Candidate verification.
		var (
			storedPayload string
			matches       bool
		)
		scanErr := queryRowCommand(ctx, tx, lockCandidate).Scan(&storedPayload, &matches)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return fmt.Errorf("%w: candidate %s/%s is not a persisted project release",
				ErrReleaseActivationConflict, project, release.ID)
		}
		if scanErr != nil {
			return scanErr
		}
		if !matches {
			return fmt.Errorf("%w: candidate %s/%s no longer matches the persisted document",
				ErrReleaseActivationConflict, project, release.ID)
		}
		candidate, err := releaseFromRow(project, release.ID, generation, release.GitCommit, storedPayload)
		if err != nil {
			return err
		}

		// 2. Generation-owner participation, same transaction, before any
		//    write. The default adapter runs the lockManifest and
		//    lockGenerationPointer checks; a refusal aborts here with no
		//    pointer write dispatched.
		if err := s.adapter.EnsureGenerationPrerequisites(ctx, tx, project, generation); err != nil {
			return err
		}

		// 3. Pointer compare-and-swap.
		var (
			pointerReleaseID string
			currentRevision  int64
		)
		pointerErr := queryRowCommand(ctx, tx, lockPointer).Scan(&pointerReleaseID, &currentRevision)
		if pointerErr != nil && !errors.Is(pointerErr, pgx.ErrNoRows) {
			return pointerErr
		}
		hasPointer := pointerErr == nil

		if hasPointer && pointerReleaseID == release.ID {
			// F-B15 zero-write no-op: already active, revision stable,
			// expected revision not compared.
			result = &publication.ActiveRelease{Release: candidate, Revision: currentRevision}
			return nil
		}

		if hasPointer {
			if currentRevision != expectedRevision {
				return fmt.Errorf("%w: candidate %s/%s expected active revision %d, found %d",
					ErrReleaseActivationConflict, project, release.ID, expectedRevision, currentRevision)
			}
			updatePointer, err := newCASPointer(s.pointers, project, release.ID, expectedRevision)
			if err != nil {
				return err
			}
			var committedRevision int64
			casErr := queryRowCommand(ctx, tx, updatePointer).Scan(&committedRevision)
			if errors.Is(casErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: active release pointer moved during activation", ErrReleaseActivationConflict)
			}
			if casErr != nil {
				return casErr
			}
			if committedRevision != expectedRevision+1 {
				return fmt.Errorf("%w: active release revision committed as %d, want %d",
					ErrUnexpectedStoreResult, committedRevision, expectedRevision+1)
			}
			result = &publication.ActiveRelease{Release: candidate, Revision: committedRevision}
			return s.adapter.OnReleaseActivated(ctx, tx, ReleaseActivation{Project: project, Release: candidate, Revision: committedRevision})
		}

		// No pointer row: only an expected-revision-0 caller may create one.
		if expectedRevision != 0 {
			return fmt.Errorf("%w: candidate %s/%s expected active revision %d, found no active release",
				ErrReleaseActivationConflict, project, release.ID, expectedRevision)
		}
		tag, err := execCommand(ctx, tx, insertPointer)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			result = &publication.ActiveRelease{Release: candidate, Revision: 1}
			return s.adapter.OnReleaseActivated(ctx, tx, ReleaseActivation{Project: project, Release: candidate, Revision: 1})
		}
		// A concurrent activation created the pointer first (read committed
		// sees it after ON CONFLICT waits out the winner). If it names this
		// very candidate the requested end state already holds -- accepted
		// with zero further writes; otherwise the CAS is lost.
		var concurrentReleaseID string
		var concurrentRevision int64
		rereadErr := queryRowCommand(ctx, tx, readPointer).Scan(&concurrentReleaseID, &concurrentRevision)
		if errors.Is(rereadErr, pgx.ErrNoRows) {
			return fmt.Errorf("%w: active release pointer vanished during activation", ErrUnexpectedStoreResult)
		}
		if rereadErr != nil {
			return rereadErr
		}
		if concurrentReleaseID != release.ID {
			return fmt.Errorf("%w: candidate %s/%s expected no active release, found %s at revision %d (concurrent activation)",
				ErrReleaseActivationConflict, project, release.ID, concurrentReleaseID, concurrentRevision)
		}
		result = &publication.ActiveRelease{Release: candidate, Revision: concurrentRevision}
		return nil
	})
	if err != nil {
		return publication.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	if result == nil {
		// Unreachable while body sets result on every nil-return path; a
		// missing verdict is a store fault, never a silent conflict.
		return publication.ActiveRelease{}, fmt.Errorf("%w: activation produced no verdict", ErrUnexpectedStoreResult)
	}
	return *result, nil
}

// newCASPointer builds the revision-conditional pointer UPDATE. The concrete
// expected and next revisions are bound as ordered parameters so no
// execution can run with a placeholder compare value.
func newCASPointer(pointersFQN, project, releaseID string, expectedRevision int64) (Command, error) {
	return NewPostgreSQLCommand(
		"UPDATE "+pointersFQN+`
SET release_id = $2, revision = $3, updated_at = now()
WHERE project = $1 AND revision = $4
RETURNING revision`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: releaseID},
			{Type: ParameterText, Value: fmt.Sprintf("%d", expectedRevision+1)},
			{Type: ParameterText, Value: fmt.Sprintf("%d", expectedRevision)},
		})
}

// inTx runs body inside one transaction on a pooled connection and commits
// only if body returns nil. The commitFault test seam runs immediately
// before COMMIT so a fault leaves every write of the transaction invisible.
func (s *ReleaseStore) inTx(ctx context.Context, step string, body func(pgx.Tx) error) (err error) {
	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := body(tx); err != nil {
		return err
	}
	if s.commitFault != nil {
		if err := s.commitFault(step); err != nil {
			return fmt.Errorf("injected fault before commit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// ---------------------------------------------------------------------------
// default GenerationOwnerAdapter
// ---------------------------------------------------------------------------

// datasetGenerationOwner is the release store's default generation-owner
// participant. It is bound to the same schema and reads only the
// generation lane's own tables (dataset_generation_manifest and
// dataset_active_generation, migration 000003) inside the caller's
// transaction -- the same activation filters the Arango baseline applies
// inside activateReleaseAQL, with no duplicated schema on either side.
type datasetGenerationOwner struct {
	manifests string
	pointer   string
}

var _ GenerationOwnerAdapter = (*datasetGenerationOwner)(nil)

// EnsureGenerationPrerequisites verifies, inside the activation
// transaction, that the release's generation is a persisted manifest in a
// publicly staged state (STAGED or the retained legacy READY spelling) and
// that the project has an active-generation pointer. Rows are locked FOR
// SHARE so a concurrent generation transition cannot slip between the
// check and the commit.
func (g *datasetGenerationOwner) EnsureGenerationPrerequisites(ctx context.Context, tx pgx.Tx, project, generation string) error {
	lockManifest, err := NewPostgreSQLCommand(
		"SELECT state FROM "+g.manifests+`
WHERE project = $1 AND generation = $2
FOR SHARE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: generation},
		})
	if err != nil {
		return fmt.Errorf("activate release: %w", err)
	}
	var state string
	scanErr := queryRowCommand(ctx, tx, lockManifest).Scan(&state)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return fmt.Errorf("%w: generation %s/%s has no persisted manifest",
			ErrReleaseActivationConflict, project, generation)
	}
	if scanErr != nil {
		return fmt.Errorf("activate release: %w", scanErr)
	}
	if state != string(publication.StateStaged) && state != string(publication.StateReady) {
		return fmt.Errorf("%w: generation %s/%s manifest is %s, not %s or %s",
			ErrReleaseActivationConflict, project, generation, state,
			string(publication.StateStaged), string(publication.StateReady))
	}

	lockPointer, err := NewPostgreSQLCommand(
		"SELECT generation FROM "+g.pointer+`
WHERE project = $1
FOR SHARE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return fmt.Errorf("activate release: %w", err)
	}
	var activeGeneration string
	scanErr = queryRowCommand(ctx, tx, lockPointer).Scan(&activeGeneration)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return fmt.Errorf("%w: project %s has no active generation",
			ErrReleaseActivationConflict, project)
	}
	if scanErr != nil {
		return fmt.Errorf("activate release: %w", scanErr)
	}
	return nil
}

// OnReleaseActivated is the generation owner's post-swap hook. The PG
// active-generation table (migration 000003) carries no release columns,
// and this store refuses to add columns to another lane's schema -- so the
// default hook is a deliberate zero-write, unlike the Arango baseline which
// patches release facts onto its combined lifecycle pointer. Owners that
// need release facts record them through their own migrations and inject a
// custom adapter.
func (g *datasetGenerationOwner) OnReleaseActivated(context.Context, pgx.Tx, ReleaseActivation) error {
	return nil
}
