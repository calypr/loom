package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
)

var _ publication.ActiveResolver = (*Store)(nil)

// CreateManifest persists a new generation manifest in LOADING state. The
// insert is the compare-and-swap: a second create for the same
// (project, generation) returns ErrManifestAlreadyExists regardless of
// content, so a generation identity is never re-pointed at different bytes.
func (s *Store) CreateManifest(ctx context.Context, manifest publication.Manifest) (publication.Manifest, error) {
	if err := manifest.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	if manifest.State != publication.StateLoading {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w: state must be %s, got %s", publication.ErrInvalidTransition, publication.StateLoading, manifest.State)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	identity, err := marshalSchemaIdentity(manifest.SchemaIdentity)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	command, err := NewPostgreSQLCommand(
		"INSERT INTO "+s.manifestFQN+` (
    project, generation, state, schema_identity
)
VALUES ($1, $2, $3, $4::jsonb)
RETURNING project, generation, state, schema_identity::text`,
		[]Parameter{
			{Type: ParameterText, Value: manifest.Dataset.Project},
			{Type: ParameterText, Value: manifest.Dataset.Generation},
			{Type: ParameterText, Value: string(manifest.State)},
			{Type: ParameterJSONB, Value: identity},
		})
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}

	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: acquire connection: %w", err)
	}
	defer conn.Release()

	var (
		project    string
		generation string
		state      string
		identityDB string
	)
	err = queryRowConn(ctx, conn, command).Scan(&project, &generation, &state, &identityDB)
	if err != nil {
		if isUniqueViolation(err) {
			return publication.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestAlreadyExists, manifest.Dataset.Project, manifest.Dataset.Generation)
		}
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	created, err := manifestFromRow(project, generation, state, identityDB)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	if !reflect.DeepEqual(created, manifest) {
		return publication.Manifest{}, fmt.Errorf("%w: created manifest does not match request", ErrUnexpectedStoreResult)
	}
	return created, nil
}

// ReadManifest returns the persisted manifest for one generation.
func (s *Store) ReadManifest(ctx context.Context, ref publication.Ref) (publication.Manifest, error) {
	if err := ref.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	command, err := NewPostgreSQLCommand(
		"SELECT project, generation, state, schema_identity::text FROM "+s.manifestFQN+`
WHERE project = $1 AND generation = $2`,
		[]Parameter{
			{Type: ParameterText, Value: ref.Project},
			{Type: ParameterText, Value: ref.Generation},
		})
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}

	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: acquire connection: %w", err)
	}
	defer conn.Release()

	var (
		project    string
		generation string
		state      string
		identityDB string
	)
	err = queryRowConn(ctx, conn, command).Scan(&project, &generation, &state, &identityDB)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestNotFound, ref.Project, ref.Generation)
		}
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}
	manifest, err := manifestFromRow(project, generation, state, identityDB)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}
	if manifest.Dataset != ref {
		return publication.Manifest{}, fmt.Errorf("%w: read manifest reference does not match request", ErrUnexpectedStoreResult)
	}
	return manifest, nil
}

// TransitionManifest moves a manifest from its current state to next. The
// compare-and-swap is the UPDATE predicate: the row must still carry the
// expected state and the immutable schema identity bytes, so a concurrent
// transition or an edited identity yields ErrManifestTransitionConflict
// instead of a blind overwrite.
func (s *Store) TransitionManifest(ctx context.Context, manifest publication.Manifest, next publication.State) (publication.Manifest, error) {
	nextManifest, err := manifest.Transition(next)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	identity, err := marshalSchemaIdentity(manifest.SchemaIdentity)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}
	command, err := NewPostgreSQLCommand(
		"UPDATE "+s.manifestFQN+`
SET state = $3, updated_at = now()
WHERE project = $1 AND generation = $2 AND state = $4 AND schema_identity = $5::jsonb
RETURNING project, generation, state, schema_identity::text`,
		[]Parameter{
			{Type: ParameterText, Value: manifest.Dataset.Project},
			{Type: ParameterText, Value: manifest.Dataset.Generation},
			{Type: ParameterText, Value: string(next)},
			{Type: ParameterText, Value: string(manifest.State)},
			{Type: ParameterJSONB, Value: identity},
		})
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}

	var result *publication.Manifest
	err = s.inTx(ctx, "transition", func(tx pgx.Tx) error {
		var (
			project    string
			generation string
			state      string
			identityDB string
		)
		scanErr := queryRowCommand(ctx, tx, command).Scan(&project, &generation, &state, &identityDB)
		if scanErr != nil {
			if errors.Is(scanErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s/%s was not %s with the expected immutable metadata",
					ErrManifestTransitionConflict, manifest.Dataset.Project, manifest.Dataset.Generation, manifest.State)
			}
			return scanErr
		}
		decoded, decodeErr := manifestFromRow(project, generation, state, identityDB)
		if decodeErr != nil {
			return decodeErr
		}
		if !reflect.DeepEqual(decoded, nextManifest) {
			return fmt.Errorf("%w: transition result does not match requested state", ErrUnexpectedStoreResult)
		}
		result = &decoded
		return nil
	})
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}
	return *result, nil
}

// ResolveActiveManifest returns the manifest the project's active-generation
// pointer names. A missing pointer row means no active generation; a pointer
// naming a manifest that is not staged/ready is an inconsistency, never a
// silent fallback to another generation or backend.
func (s *Store) ResolveActiveManifest(ctx context.Context, project string) (publication.Manifest, error) {
	if err := validateProject(project); err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	command, err := NewPostgreSQLCommand(
		"SELECT m.project, m.generation, m.state, m.schema_identity::text FROM "+s.pointerFQN+` p
JOIN `+s.manifestFQN+` m
  ON m.project = p.project AND m.generation = p.generation
WHERE p.project = $1`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}

	conn, err := s.src.Acquire(ctx)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: acquire connection: %w", err)
	}
	defer conn.Release()

	var (
		generatedProject string
		generation       string
		state            string
		identityDB       string
	)
	err = queryRowConn(ctx, conn, command).Scan(&generatedProject, &generation, &state, &identityDB)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.Manifest{}, fmt.Errorf("%w: %s", publication.ErrNoActiveGeneration, project)
		}
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}
	manifest, err := manifestFromRow(generatedProject, generation, state, identityDB)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}
	if manifest.Dataset.Project != project || !manifest.IsStaged() {
		return publication.Manifest{}, fmt.Errorf("%w: active pointer and staged manifest are inconsistent", ErrUnexpectedStoreResult)
	}
	return manifest, nil
}

// ActivateGeneration atomically compares-and-swaps the project's
// active-generation pointer onto candidate.
//
// expected is the pointer value the caller believes is current: nil means
// "there is no active generation yet"; a non-nil Ref names the generation
// the caller resolved as active. The entire operation runs in one
// transaction that locks the pointer row, so two concurrent activations
// presenting the same expected value produce exactly one winner and a typed
// ErrActivationConflict for the loser -- there is no read-then-act window
// between consulting the pointer and swapping it.
//
// Rules:
//   - A candidate that is not staged (in-memory validation) is refused with
//     publication.ErrGenerationNotReady.
//   - A candidate that is not a persisted manifest, or whose persisted state
//     or schema identity bytes no longer match the candidate (a stale read),
//     is refused with ErrActivationConflict. A FAILED or LOADING manifest
//     can never become active, even if the caller hands over a staged
//     manifest value read before the row moved.
//   - Re-activating the exact manifest the pointer already names is
//     idempotent: err=nil, zero writes.
//   - A pointer whose current value differs from expected yields
//     ErrActivationConflict and leaves the pointer untouched.
//
// The pointer's previous value is compared, never ignored: this is the CAS
// shape the Arango generation-activation path (F-B17) lacks.
func (s *Store) ActivateGeneration(ctx context.Context, candidate publication.Manifest, expected *publication.Ref) error {
	if !candidate.IsStaged() {
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("activate generation: %w", err)
		}
		return fmt.Errorf("activate generation: %w: %s is %s", publication.ErrGenerationNotReady, candidate.Dataset.Generation, candidate.State)
	}
	if expected != nil {
		if err := expected.Validate(); err != nil {
			return fmt.Errorf("activate generation: %w: expected pointer: %w", publication.ErrInvalidActiveGeneration, err)
		}
		if expected.Project != candidate.Dataset.Project {
			return fmt.Errorf("%w: expected pointer project %q does not match candidate project %q",
				publication.ErrInvalidActiveGeneration, expected.Project, candidate.Dataset.Project)
		}
	}
	if err := s.validate(); err != nil {
		return err
	}
	project := candidate.Dataset.Project
	generation := candidate.Dataset.Generation

	lockCandidate, err := NewPostgreSQLCommand(
		"SELECT state, schema_identity::text FROM "+s.manifestFQN+`
WHERE project = $1 AND generation = $2
FOR SHARE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: generation},
		})
	if err != nil {
		return fmt.Errorf("activate generation: %w", err)
	}
	lockPointer, err := NewPostgreSQLCommand(
		"SELECT generation FROM "+s.pointerFQN+`
WHERE project = $1
FOR UPDATE`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return fmt.Errorf("activate generation: %w", err)
	}
	readPointer, err := NewPostgreSQLCommand(
		"SELECT generation FROM "+s.pointerFQN+`
WHERE project = $1`,
		[]Parameter{
			{Type: ParameterText, Value: project},
		})
	if err != nil {
		return fmt.Errorf("activate generation: %w", err)
	}
	// The compare-and-swap UPDATE is built only once the pointer's actual
	// value has been compared against a non-nil expected Ref, so a swap can
	// never be dispatched with a placeholder expected value.
	updatePointerSQL := "UPDATE " + s.pointerFQN + `
SET generation = $2, updated_at = now()
WHERE project = $1 AND generation = $3`
	insertPointer, err := NewPostgreSQLCommand(
		"INSERT INTO "+s.pointerFQN+` (project, generation)
VALUES ($1, $2)
ON CONFLICT DO NOTHING`,
		[]Parameter{
			{Type: ParameterText, Value: project},
			{Type: ParameterText, Value: generation},
		})
	if err != nil {
		return fmt.Errorf("activate generation: %w", err)
	}

	return s.inTx(ctx, "activate", func(tx pgx.Tx) error {
		// 1. The candidate must be a persisted staged manifest whose stored
		//    schema identity bytes still equal the caller's candidate.
		var (
			storedState    string
			storedIdentity string
		)
		scanErr := queryRowCommand(ctx, tx, lockCandidate).Scan(&storedState, &storedIdentity)
		if scanErr != nil {
			if errors.Is(scanErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: candidate %s/%s is not a persisted manifest", ErrActivationConflict, project, generation)
			}
			return scanErr
		}
		stored, err := manifestFromRow(project, generation, storedState, storedIdentity)
		if err != nil {
			return err
		}
		if !stored.IsStaged() || !reflect.DeepEqual(stored.SchemaIdentity, candidate.SchemaIdentity) {
			return fmt.Errorf("%w: candidate %s/%s was not a persisted staged manifest with the expected immutable metadata",
				ErrActivationConflict, project, generation)
		}

		// 2. Lock the pointer row and compare its current value.
		var pointerGeneration string
		pointerErr := queryRowCommand(ctx, tx, lockPointer).Scan(&pointerGeneration)
		if pointerErr != nil && !errors.Is(pointerErr, pgx.ErrNoRows) {
			return pointerErr
		}
		hasPointer := pointerErr == nil

		if hasPointer {
			if pointerGeneration == generation {
				// Pointer already names the exact validated candidate:
				// idempotent success with zero writes.
				return nil
			}
			if expected == nil {
				return fmt.Errorf("%w: candidate %s/%s expected no active generation, found %s",
					ErrActivationConflict, project, generation, pointerGeneration)
			}
			if expected.Generation != pointerGeneration {
				return fmt.Errorf("%w: candidate %s/%s expected active generation %s, found %s",
					ErrActivationConflict, project, generation, expected.Generation, pointerGeneration)
			}
			updatePointer, err := NewPostgreSQLCommand(
				updatePointerSQL,
				[]Parameter{
					{Type: ParameterText, Value: project},
					{Type: ParameterText, Value: generation},
					{Type: ParameterText, Value: expected.Generation},
				})
			if err != nil {
				return err
			}
			tag, err := execCommand(ctx, tx, updatePointer)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("%w: active generation pointer moved during activation", ErrActivationConflict)
			}
			return nil
		}

		// No pointer row: only an expected-nil caller may create one.
		if expected != nil {
			return fmt.Errorf("%w: candidate %s/%s expected active generation %s, found no active generation",
				ErrActivationConflict, project, generation, expected.Generation)
		}
		tag, err := execCommand(ctx, tx, insertPointer)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		// A concurrent activation created the pointer first (read committed
		// sees it after ON CONFLICT waits out the winner). If it names this
		// very candidate the requested end state already holds; otherwise
		// the CAS is lost.
		var concurrent string
		rereadErr := queryRowCommand(ctx, tx, readPointer).Scan(&concurrent)
		if rereadErr != nil {
			if errors.Is(rereadErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: active generation pointer vanished during activation", ErrUnexpectedStoreResult)
			}
			return rereadErr
		}
		if concurrent == generation {
			return nil
		}
		return fmt.Errorf("%w: candidate %s/%s expected no active generation, found %s (concurrent activation)",
			ErrActivationConflict, project, generation, concurrent)
	})
}

// inTx runs body inside one transaction on a pooled connection and commits
// only if body returns nil. The commitFault test seam runs immediately
// before COMMIT so a fault leaves every write of the transaction invisible:
// neither manifest state nor the active pointer moves.
func (s *Store) inTx(ctx context.Context, step string, body func(pgx.Tx) error) (err error) {
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

// marshalSchemaIdentity encodes the schema identity document for storage.
// Encoding is the plain JSON of the domain struct, so the persisted bytes
// carry the public schema identity fields unchanged; a JSON null inside the
// document would stay a JSON null and is never promoted to SQL NULL (the
// column is NOT NULL).
func marshalSchemaIdentity(schema publication.SchemaSnapshot) (string, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("encode schema identity: %w", err)
	}
	return string(encoded), nil
}

// manifestFromRow rebuilds a manifest from one stored row. Unknown states or
// malformed identity documents are store inconsistencies, never coerced.
func manifestFromRow(project, generation, state, identityDB string) (publication.Manifest, error) {
	var schema publication.SchemaSnapshot
	decoder := json.NewDecoder(bytes.NewReader([]byte(identityDB)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return publication.Manifest{}, fmt.Errorf("%w: stored schema identity is malformed: %v", ErrUnexpectedStoreResult, err)
	}
	manifest := publication.Manifest{
		Dataset:        publication.Ref{Project: project, Generation: generation},
		State:          publication.State(state),
		SchemaIdentity: schema,
	}
	if err := manifest.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("%w: stored manifest is invalid: %v", ErrUnexpectedStoreResult, err)
	}
	return manifest, nil
}
