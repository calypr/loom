package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"reflect"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ProjectReleasesTable       = "project_release"
	ActiveProjectReleasesTable = "active_project_release"
)

var (
	ErrNilReleaseConnSource      = errors.New("dataset postgres: release store connection source is required")
	ErrReleaseContentConflict    = errors.New("project release candidate already exists with different content")
	ErrReleaseActivationConflict = publication.ErrReleaseActivationConflict
)

var _ publication.ReleaseRepository = (*ReleaseStore)(nil)

type ReleaseActivation struct {
	Project  string
	Release  publication.ProjectRelease
	Revision int64
}

type GenerationOwnerAdapter interface {
	EnsureGenerationPrerequisites(ctx context.Context, tx pgx.Tx, project, generation string) error
	OnReleaseActivated(ctx context.Context, tx pgx.Tx, activation ReleaseActivation) error
}

type ReleaseStore struct {
	src         ConnSource
	releases    string
	pointers    string
	adapter     GenerationOwnerAdapter
	commitFault func(step string) error
}

func NewReleaseStore(src ConnSource, schema string) (*ReleaseStore, error) {
	if src == nil {
		return nil, ErrNilReleaseConnSource
	}
	if !storepostgres.ValidSchemaName(schema) {
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
		return s.savedCandidateConflict(ctx, conn, release, payload)
	}
	if scanErr != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", scanErr)
	}

	stored, err := releaseFromRow(release.Project, release.ID, release.Generation, release.GitCommit, storedPayload)
	if err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	var submitted publication.ProjectRelease
	if err := json.NewDecoder(bytes.NewReader([]byte(payload))).Decode(&submitted); err != nil {
		return publication.ProjectRelease{}, fmt.Errorf("save project release: %w", err)
	}
	if !reflect.DeepEqual(stored, submitted) {
		return publication.ProjectRelease{}, fmt.Errorf("%w: stored release does not match the submitted candidate", ErrUnexpectedStoreResult)
	}
	return stored, nil
}

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

	var result *publication.ActiveRelease
	err = s.inTx(ctx, "activate-release", func(tx pgx.Tx) error {
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

		if err := s.adapter.EnsureGenerationPrerequisites(ctx, tx, project, generation); err != nil {
			return err
		}

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
		return publication.ActiveRelease{}, fmt.Errorf("%w: activation produced no verdict", ErrUnexpectedStoreResult)
	}
	return *result, nil
}

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

type datasetGenerationOwner struct {
	manifests string
	pointer   string
}

var _ GenerationOwnerAdapter = (*datasetGenerationOwner)(nil)

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

func (g *datasetGenerationOwner) OnReleaseActivated(context.Context, pgx.Tx, ReleaseActivation) error {
	return nil
}
