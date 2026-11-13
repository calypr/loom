package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	datasetpostgres "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	publicationpostgres "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

const (
	activationTxMaxAttempts  = 4
	activationDeadlockRetry  = 5 * time.Millisecond
	activationPublishedState = string(dataframepublication.BundlePublished)
)

var ErrInvalidActivationInput = errors.New("postgres explorer store: invalid repository generation activation input")

type ActivationStore struct {
	client            *pgstore.Client
	explorerSchema    string
	datasetSchema     string
	publicationSchema string
	stmts             activationStatements
	crashAt           func(context.Context, pgx.Tx, string) error
}

func NewActivationStore(client *pgstore.Client, explorerSchema, datasetSchema, publicationSchema string) (*ActivationStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: activation client is required")
	}
	for _, schema := range []string{explorerSchema, datasetSchema, publicationSchema} {
		if !schemaNamePattern.MatchString(schema) {
			return nil, fmt.Errorf("postgres explorer store: invalid activation schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
		}
	}
	return &ActivationStore{
		client:            client,
		explorerSchema:    explorerSchema,
		datasetSchema:     datasetSchema,
		publicationSchema: publicationSchema,
		stmts:             newActivationStatements(explorerSchema, datasetSchema, publicationSchema),
	}, nil
}

type activationStatements struct {
	publish           publishStatements
	manifestState     string
	generationPointer string
	executionState    string
}

func newActivationStatements(explorerSchema, datasetSchema, publicationSchema string) activationStatements {
	manifests := sanitizeTableRef(datasetSchema, "dataset_generation_manifest")
	generationPointer := sanitizeTableRef(datasetSchema, "dataset_active_generation")
	executions := sanitizeTableRef(publicationSchema, publicationpostgres.BundleExecutionsTable)
	return activationStatements{
		publish:           newPublishStatements(explorerSchema, datasetSchema),
		manifestState:     "SELECT state FROM " + manifests + " WHERE project = $1 AND generation = $2 FOR SHARE",
		generationPointer: "SELECT generation FROM " + generationPointer + " WHERE project = $1 FOR SHARE",
		executionState: "SELECT state FROM " + executions +
			" WHERE id = $1 AND project = $2 AND dataset_generation = $3 FOR SHARE",
	}
}

func (a *ActivationStore) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	if project == "" || generation == "" || revisionID == "" {
		return fmt.Errorf("%w: project, generation, and revision ID are required (project %q, generation %q, revision %q)",
			ErrInvalidActivationInput, project, generation, revisionID)
	}
	var lastErr error
	for attempt := 1; attempt <= activationTxMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("postgres explorer store: activate repository generation: %w", err)
		}
		runErr := a.runActivateRepositoryGeneration(ctx, project, generation, revisionID)
		if runErr == nil {
			return nil
		}
		if errors.Is(runErr, explorer.ErrDraftConflict) {
			return runErr
		}
		lastErr = runErr
		if !isDeadlockError(runErr) || attempt == activationTxMaxAttempts {
			break
		}
		if waitErr := waitForStoreRetry(ctx, time.Duration(attempt)*activationDeadlockRetry); waitErr != nil {
			return fmt.Errorf("postgres explorer store: activate repository generation retry: %w", errors.Join(lastErr, waitErr))
		}
	}
	return fmt.Errorf("postgres explorer store: activate repository generation: %w", lastErr)
}

func (a *ActivationStore) runActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) (err error) {
	conn, err := a.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire activation connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin activation transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	crash := func(step string) error {
		if a.crashAt != nil {
			if err := a.crashAt(ctx, tx, step); err != nil {
				return fmt.Errorf("injected fault at %s: %w", step, err)
			}
		}
		return nil
	}

	candidate, err := a.lockActivationCandidate(ctx, tx, revisionID, project, generation)
	if err != nil {
		return err
	}
	if err := a.verifyActivationManifest(ctx, tx, project, generation); err != nil {
		return err
	}
	if err := a.verifyActivationGenerationPointer(ctx, tx, project, generation); err != nil {
		return err
	}
	if err := a.verifyActivationExecution(ctx, tx, candidate, project, generation); err != nil {
		return err
	}
	owner, err := a.lockActivationOwner(ctx, tx, project)
	if err != nil {
		return err
	}

	if candidate.Status == string(explorer.RevisionActive) && owner.ActiveRevisionID == candidate.ID {
		if err := crash("pre-commit"); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit activation transaction: %w", err)
		}
		committed = true
		return nil
	}
	if candidate.Status != string(explorer.RevisionReady) {
		return fmt.Errorf("%w: candidate revision %q is %s, not activatable", explorer.ErrDraftConflict, candidate.ID, candidate.Status)
	}

	if owner.ActiveRevisionID != "" && owner.ActiveRevisionID != candidate.ID {
		if err := a.supersedePriorActivationRevision(ctx, tx, owner); err != nil {
			return err
		}
		if err := crash("prior-supersede"); err != nil {
			return err
		}
	}

	activatedPublication, err := activatedPublicationPayload(candidate)
	if err != nil {
		return err
	}
	activatedAt := time.Now().UTC()
	var activated storedRevisionRow
	if err := scanRevisionRow(tx.QueryRow(ctx, a.stmts.publish.candidateActive,
		candidate.ID, activatedAt, []byte(publishDiagnosticsSet), []byte(activatedPublication)), &activated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: candidate revision %q moved before it could be activated", explorer.ErrDraftConflict, candidate.ID)
		}
		return fmt.Errorf("activate candidate revision %q: %w", candidate.ID, err)
	}
	if err := crash("candidate-activate"); err != nil {
		return err
	}

	var activatedOwner explorer.Explorer
	if err := scanOwner(tx.QueryRow(ctx, a.stmts.publish.ownerActivate,
		project, "default", candidate.ID, time.Now().UTC()), &activatedOwner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: owner vanished before activation", explorer.ErrDraftConflict)
		}
		return fmt.Errorf("activate explorer owner %q/default: %w", project, err)
	}
	if err := crash("owner-activate"); err != nil {
		return err
	}

	if err := crash("pre-commit"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit activation transaction: %w", err)
	}
	committed = true
	return nil
}

func (a *ActivationStore) lockActivationCandidate(ctx context.Context, tx pgx.Tx, revisionID, project, generation string) (*storedRevisionRow, error) {
	var candidate storedRevisionRow
	if err := scanRevisionRow(tx.QueryRow(ctx, a.stmts.publish.revisionClaim, revisionID), &candidate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: candidate revision %q is not a stored revision", explorer.ErrDraftConflict, revisionID)
		}
		return nil, fmt.Errorf("lock candidate revision %q: %w", revisionID, err)
	}
	if candidate.Project != project || candidate.ExplorerID != "default" || candidate.SourceGeneration != generation {
		return nil, fmt.Errorf("%w: candidate revision %q does not belong to repository explorer %q/default at generation %q",
			explorer.ErrDraftConflict, revisionID, project, generation)
	}
	return &candidate, nil
}

func (a *ActivationStore) verifyActivationManifest(ctx context.Context, tx pgx.Tx, project, generation string) error {
	var state string
	err := tx.QueryRow(ctx, a.stmts.manifestState, project, generation).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: generation %s/%s has no persisted manifest", explorer.ErrDraftConflict, project, generation)
	}
	if err != nil {
		return fmt.Errorf("read activation manifest guard %s/%s: %w", project, generation, err)
	}
	if state != string(dataset.StateStaged) && state != string(dataset.StateReady) {
		return fmt.Errorf("%w: generation %s/%s manifest is %s, not %s or %s",
			explorer.ErrDraftConflict, project, generation, state, string(dataset.StateStaged), string(dataset.StateReady))
	}
	return nil
}

func (a *ActivationStore) verifyActivationGenerationPointer(ctx context.Context, tx pgx.Tx, project, generation string) error {
	var activeGeneration string
	err := tx.QueryRow(ctx, a.stmts.generationPointer, project).Scan(&activeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: project %s has no active generation", explorer.ErrDraftConflict, project)
	}
	if err != nil {
		return fmt.Errorf("read active generation guard for %s: %w", project, err)
	}
	if activeGeneration != generation {
		return fmt.Errorf("%w: active generation of %s is %s, not the activation generation %s",
			explorer.ErrDraftConflict, project, activeGeneration, generation)
	}
	return nil
}

func (a *ActivationStore) verifyActivationExecution(ctx context.Context, tx pgx.Tx, candidate *storedRevisionRow, project, generation string) error {
	materializationID, err := firstActivationMaterializationID(candidate)
	if err != nil {
		return err
	}
	if materializationID == "" {
		return fmt.Errorf("%w: candidate revision %q has no materialized execution", explorer.ErrDraftConflict, candidate.ID)
	}
	var state string
	err = tx.QueryRow(ctx, a.stmts.executionState, materializationID, project, generation).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: materialization %q of candidate revision %q is not a persisted execution for %s/%s",
			explorer.ErrDraftConflict, materializationID, candidate.ID, project, generation)
	}
	if err != nil {
		return fmt.Errorf("read activation execution guard %q: %w", materializationID, err)
	}
	if state != activationPublishedState {
		return fmt.Errorf("%w: execution %q is %s, not %s",
			explorer.ErrDraftConflict, materializationID, state, activationPublishedState)
	}
	return nil
}

func firstActivationMaterializationID(candidate *storedRevisionRow) (string, error) {
	if len(candidate.Materializations) == 0 {
		return "", nil
	}
	var materializations []explorer.Materialization
	if err := json.Unmarshal(candidate.Materializations, &materializations); err != nil {
		return "", fmt.Errorf("%w: stored revision %q materializations are undecodable: %v",
			datasetpostgres.ErrUnexpectedStoreResult, candidate.ID, err)
	}
	if len(materializations) == 0 {
		return "", nil
	}
	return materializations[0].MaterializationID, nil
}

func (a *ActivationStore) lockActivationOwner(ctx context.Context, tx pgx.Tx, project string) (explorer.Explorer, error) {
	var owner explorer.Explorer
	err := scanOwner(tx.QueryRow(ctx, a.stmts.publish.ownerGuard,
		project, "default", string(explorer.ManagementRepository)), &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.Explorer{}, fmt.Errorf("%w: repository explorer %q/default has no activatable owner row",
			explorer.ErrDraftConflict, project)
	}
	if err != nil {
		return explorer.Explorer{}, fmt.Errorf("lock activation owner %q/default: %w", project, err)
	}
	if owner.Project != project || owner.ExplorerID != "default" {
		return explorer.Explorer{}, fmt.Errorf("%w: activation owner row %q/%q is foreign to %q/default",
			explorer.ErrDraftConflict, owner.Project, owner.ExplorerID, project)
	}
	return owner, nil
}

func (a *ActivationStore) supersedePriorActivationRevision(ctx context.Context, tx pgx.Tx, owner explorer.Explorer) error {
	var prior storedRevisionRow
	err := scanRevisionRow(tx.QueryRow(ctx, a.stmts.publish.priorLock, owner.ActiveRevisionID), &prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: owner active revision %q is not a stored revision", explorer.ErrDraftConflict, owner.ActiveRevisionID)
	}
	if err != nil {
		return fmt.Errorf("lock prior revision %q: %w", owner.ActiveRevisionID, err)
	}
	if prior.Project != owner.Project || prior.ExplorerID != owner.ExplorerID {
		return fmt.Errorf("%w: owner active revision %q is foreign to %q/%q",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, owner.Project, owner.ExplorerID)
	}
	if prior.Status != string(explorer.RevisionActive) {
		return fmt.Errorf("%w: owner active revision %q is %s, not ACTIVE",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, prior.Status)
	}
	var superseded storedRevisionRow
	if err := scanRevisionRow(tx.QueryRow(ctx, a.stmts.publish.priorSupersede, prior.ID), &superseded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: prior revision %q moved before it could be superseded", explorer.ErrDraftConflict, prior.ID)
		}
		return fmt.Errorf("supersede prior revision %q: %w", prior.ID, err)
	}
	return nil
}
