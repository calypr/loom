package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/explorer"

	datasetpostgres "github.com/calypr/loom/internal/backends/postgres/internal/dataset"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataset"
)

const (
	publishTxMaxAttempts  = 4
	publishDeadlockRetry  = 5 * time.Millisecond
	publishDiagnosticsSet = "[]"
)

var ErrInvalidPublicationInput = errors.New("postgres explorer store: invalid publish authoring input")

var ErrPublishContention = errors.New("postgres explorer store: publish authoring lost every deadlock race")

type PublishStore struct {
	client         *pgstore.Client
	explorerSchema string
	datasetSchema  string
	stmts          publishStatements
	crashAt        func(context.Context, pgx.Tx, string) error
}

func NewPublishStore(client *pgstore.Client, explorerSchema, datasetSchema string) (*PublishStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: publish client is required")
	}
	for _, schema := range []string{explorerSchema, datasetSchema} {
		if !schemaNamePattern.MatchString(schema) {
			return nil, fmt.Errorf("postgres explorer store: invalid publish schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
		}
	}
	return &PublishStore{
		client:         client,
		explorerSchema: explorerSchema,
		datasetSchema:  datasetSchema,
		stmts:          newPublishStatements(explorerSchema, datasetSchema),
	}, nil
}

type publishStatements struct {
	receiptInsert        string
	receiptClaim         string
	revisionInsert       string
	revisionClaim        string
	releaseCandidateLock string
	manifestLock         string
	generationPointer    string
	releasePointerLock   string
	releasePointerRead   string
	releasePointerCAS    string
	releasePointerInsert string
	ownerGuard           string
	priorLock            string
	priorSupersede       string
	candidateActive      string
	ownerActivate        string
}

func newPublishStatements(explorerSchema, datasetSchema string) publishStatements {
	ownerTable := sanitizeTableRef(explorerSchema, OwnerTable)
	ownerCols := strings.Join(ownerColumns, ", ")
	manifests := sanitizeTableRef(datasetSchema, "dataset_generation_manifest")
	generationPointer := sanitizeTableRef(datasetSchema, "dataset_active_generation")
	releases := sanitizeTableRef(datasetSchema, datasetpostgres.ProjectReleasesTable)
	pointers := sanitizeTableRef(datasetSchema, datasetpostgres.ActiveProjectReleasesTable)
	return publishStatements{
		receiptInsert:  newReceiptStatements(explorerSchema).insert,
		receiptClaim:   newReceiptStatements(explorerSchema).claim,
		revisionInsert: newRevisionStatements(explorerSchema).insert,
		revisionClaim:  newRevisionStatements(explorerSchema).claim,
		releaseCandidateLock: "SELECT payload::text, (payload - 'createdAt') = ($3::jsonb - 'createdAt') FROM " + releases +
			" WHERE project = $1 AND id = $2 FOR SHARE",
		manifestLock:       "SELECT state FROM " + manifests + " WHERE project = $1 AND generation = $2 FOR SHARE",
		generationPointer:  "SELECT generation FROM " + generationPointer + " WHERE project = $1 FOR SHARE",
		releasePointerLock: "SELECT release_id, revision FROM " + pointers + " WHERE project = $1 FOR UPDATE",
		releasePointerRead: "SELECT release_id, revision FROM " + pointers + " WHERE project = $1",
		releasePointerCAS: "UPDATE " + pointers + " SET release_id = $2, revision = $3, updated_at = now()" +
			" WHERE project = $1 AND revision = $4 RETURNING revision",
		releasePointerInsert: "INSERT INTO " + pointers + " (project, release_id, revision) VALUES ($1, $2, 1) ON CONFLICT DO NOTHING",
		ownerGuard: "SELECT " + ownerCols + " FROM " + ownerTable +
			" WHERE project = $1 AND explorer_id = $2 AND management_mode = $3 FOR UPDATE",
		priorLock:      "SELECT " + strings.Join(revisionColumns, ", ") + " FROM " + sanitizeTableRef(explorerSchema, RevisionsTable) + " WHERE id = $1 FOR UPDATE",
		priorSupersede: "UPDATE " + sanitizeTableRef(explorerSchema, RevisionsTable) + " SET status = 'SUPERSEDED' WHERE id = $1 AND status = 'ACTIVE' RETURNING " + strings.Join(revisionColumns, ", "),
		candidateActive: "UPDATE " + sanitizeTableRef(explorerSchema, RevisionsTable) +
			" SET status = 'ACTIVE', activated_at = $2, failed_at = NULL, diagnostics = $3, publication = $4" +
			" WHERE id = $1 AND status = 'READY' RETURNING " + strings.Join(revisionColumns, ", "),
		ownerActivate: "UPDATE " + ownerTable + " SET active_revision_id = $3, updated_at = $4" +
			" WHERE project = $1 AND explorer_id = $2 RETURNING " + ownerCols,
	}
}

func (p *PublishStore) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if err := validateReceiptForStore(receipt); err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", err)
	}
	if strings.TrimSpace(revision.ID) == "" {
		return nil, fmt.Errorf("%w: publish authoring: revision ID is required", ErrInvalidPublicationInput)
	}
	if revision.Status != explorer.RevisionReady {
		return nil, fmt.Errorf("%w: publish authoring: revision %q carries status %q, only a READY revision may be published",
			ErrInvalidPublicationInput, revision.ID, string(revision.Status))
	}
	if expectedReleaseRevision < 0 {
		return nil, fmt.Errorf("%w: publish authoring: expected release revision %d is negative", ErrInvalidPublicationInput, expectedReleaseRevision)
	}
	contentDigest, err := revisionContentDigest(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: content identity of revision %q: %w", revision.ID, err)
	}
	payloads, err := encodeRevisionPayloads(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode revision %q: %w", revision.ID, err)
	}
	revisionDiag, err := marshalOrNil(revision.Diagnostics)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode revision %q diagnostics: %w", revision.ID, err)
	}
	receiptDoc, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode compilation receipt %q: %w", receipt.ID, err)
	}
	releasePayload, err := marshalPublishReleasePayload(release)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode project release %q: %w", release.ID, err)
	}

	var lastErr error
	for attempt := 1; attempt <= publishTxMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", err)
		}
		out, runErr := p.runPublishAuthoring(ctx, receipt, receiptDoc, revision, payloads, revisionDiag, contentDigest, release, releasePayload, expectedReleaseRevision)
		if runErr == nil {
			return out, nil
		}
		if isTypedPublicationRefusal(runErr) {
			return nil, runErr
		}
		lastErr = runErr
		if !isDeadlockError(runErr) || attempt == publishTxMaxAttempts {
			break
		}
		if waitErr := waitForStoreRetry(ctx, time.Duration(attempt)*publishDeadlockRetry); waitErr != nil {
			return nil, fmt.Errorf("postgres explorer store: publish authoring retry: %w", errors.Join(lastErr, waitErr))
		}
	}
	if lastErr == nil {
		lastErr = ErrPublishContention
	}
	if isDeadlockError(lastErr) {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: %w: %w", ErrPublishContention, lastErr)
	}
	return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", lastErr)
}

func (p *PublishStore) runPublishAuthoring(
	ctx context.Context,
	receipt explorer.CompilationReceipt, receiptDoc []byte,
	revision explorer.Revision, payloads revisionPayloads, revisionDiag []byte, contentDigest string,
	release dataset.ProjectRelease, releasePayload string,
	expectedReleaseRevision int64,
) (out *explorer.Revision, err error) {
	conn, err := p.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire publish connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin publish transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	crash := func(step string) error {
		if p.crashAt != nil {
			if err := p.crashAt(ctx, tx, step); err != nil {
				return fmt.Errorf("injected fault at %s: %w", step, err)
			}
		}
		return nil
	}

	if _, err := p.claimReceiptForPublish(ctx, tx, receipt, receiptDoc); err != nil {
		return nil, err
	}
	if err := crash("receipt-insert"); err != nil {
		return nil, err
	}

	candidateRow, err := p.claimRevisionForPublish(ctx, tx, revision, payloads, revisionDiag, contentDigest)
	if err != nil {
		return nil, err
	}
	storedRevision, err := decodeRevision(*candidateRow)
	if err != nil {
		return nil, err
	}
	if err := crash("revision-insert"); err != nil {
		return nil, err
	}

	if _, err := p.activateReleaseInTx(ctx, tx, release, releasePayload, expectedReleaseRevision); err != nil {
		if isTypedReleaseConflict(err) {
			return nil, err
		}
		return nil, err
	}
	if err := crash("release-pointer"); err != nil {
		return nil, err
	}

	owner, err := p.readPublishOwner(ctx, tx, storedRevision)
	if err != nil {
		return nil, err
	}
	replay := storedRevision.Status == explorer.RevisionActive &&
		owner.ActiveRevisionID == storedRevision.ID
	if replay {
		if err := crash("pre-commit"); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit publish transaction: %w", err)
		}
		committed = true
		return storedRevision, nil
	}

	if storedRevision.Status != explorer.RevisionReady {
		return nil, explorer.ErrDraftConflict
	}

	if owner.ActiveRevisionID != "" && owner.ActiveRevisionID != storedRevision.ID {
		prior, err := p.lockPriorRevision(ctx, tx, owner, storedRevision)
		if err != nil {
			return nil, err
		}
		var superseded storedRevisionRow
		if err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.priorSupersede, prior.ID), &superseded); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: prior revision %q moved before it could be superseded", explorer.ErrDraftConflict, prior.ID)
			}
			return nil, fmt.Errorf("supersede prior revision %q: %w", prior.ID, err)
		}
		if err := crash("prior-supersede"); err != nil {
			return nil, err
		}
	}

	activatedPublication, err := activatedPublicationPayload(candidateRow)
	if err != nil {
		return nil, err
	}
	activatedAt := time.Now().UTC()
	var activated storedRevisionRow
	if err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.candidateActive,
		storedRevision.ID, activatedAt, []byte(publishDiagnosticsSet), []byte(activatedPublication)), &activated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: candidate revision %q moved before it could be activated", explorer.ErrDraftConflict, storedRevision.ID)
		}
		return nil, fmt.Errorf("activate candidate revision %q: %w", storedRevision.ID, err)
	}
	if err := crash("candidate-activate"); err != nil {
		return nil, err
	}

	var activatedOwner explorer.Explorer
	if err := scanOwner(tx.QueryRow(ctx, p.stmts.ownerActivate,
		storedRevision.Project, storedRevision.ExplorerID, storedRevision.ID, time.Now().UTC()), &activatedOwner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: owner vanished before activation", explorer.ErrDraftConflict)
		}
		return nil, fmt.Errorf("activate explorer owner %q/%q: %w", storedRevision.Project, storedRevision.ExplorerID, err)
	}
	if err := crash("owner-activate"); err != nil {
		return nil, err
	}

	out, err = decodeRevision(activated)
	if err != nil {
		return nil, err
	}
	if err := crash("pre-commit"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit publish transaction: %w", err)
	}
	committed = true
	return out, nil
}

func (p *PublishStore) claimReceiptForPublish(ctx context.Context, tx pgx.Tx, receipt explorer.CompilationReceipt, doc []byte) (*explorer.CompilationReceipt, error) {
	wantKey, err := canonicalReceiptKey(receipt)
	if err != nil {
		return nil, fmt.Errorf("content identity of compilation receipt %q: %w", receipt.ID, err)
	}
	for attempt := 1; attempt <= receiptInsertMaxAttempts; attempt++ {
		var row storedReceipt
		err := scanReceipt(tx.QueryRow(ctx, p.stmts.receiptInsert, compilationReceiptArgs(receipt, doc)...), &row)
		if err == nil {
			stored, decodeErr := decodeStoredReceipt(row)
			if decodeErr != nil {
				return nil, decodeErr
			}
			storedKey, keyErr := canonicalReceiptKey(*stored)
			if keyErr != nil || storedKey != wantKey {
				return nil, fmt.Errorf("%w: stored receipt %q content identity disagrees with the claim", explorer.ErrCorruptReceipt, row.ID)
			}
			return stored, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("claim compilation receipt %q: %w", receipt.ID, err)
		}
		existing, err := p.readReceiptRowInTx(ctx, tx, receipt.Project, receipt.ExplorerID, receipt.ID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			stored, decodeErr := decodeStoredReceipt(*existing)
			if decodeErr != nil {
				return nil, decodeErr
			}
			storedKey, keyErr := canonicalReceiptKey(*stored)
			if keyErr != nil || storedKey != wantKey {
				return nil, fmt.Errorf("%w: receipt id %q was reused with different content", explorer.ErrCorruptReceipt, receipt.ID)
			}
			return stored, nil
		}
	}
	return nil, fmt.Errorf("%w: receipt claim %q/%q/%q vanished before it became visible", explorer.ErrNotFound, receipt.Project, receipt.ExplorerID, receipt.ID)
}

func (p *PublishStore) claimRevisionForPublish(ctx context.Context, tx pgx.Tx, revision explorer.Revision, payloads revisionPayloads, revisionDiag []byte, contentDigest string) (*storedRevisionRow, error) {
	args := revisionArgs(revision, payloads, contentDigest, revisionDiag)
	for attempt := 1; attempt <= revisionInsertMaxAttempts; attempt++ {
		var row storedRevisionRow
		err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.revisionInsert, args...), &row)
		if err == nil {
			return &row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("claim revision %q: %w", revision.ID, err)
		}
		var existing storedRevisionRow
		err = scanRevisionRow(tx.QueryRow(ctx, p.stmts.revisionClaim, revision.ID), &existing)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("claim revision %q: %w", revision.ID, err)
		}
		if existing.ContentDigest != contentDigest {
			return nil, fmt.Errorf("%w: revision id %q was reused with different content (stored %s, requested %s)",
				explorer.ErrImmutableRevision, revision.ID, existing.ContentDigest, contentDigest)
		}
		return &existing, nil
	}
	return nil, fmt.Errorf("%w: revision claim %q vanished before it became visible", explorer.ErrNotFound, revision.ID)
}

func (p *PublishStore) activateReleaseInTx(ctx context.Context, tx pgx.Tx, release dataset.ProjectRelease, releasePayload string, expectedRevision int64) (dataset.ActiveRelease, error) {
	var (
		storedPayload string
		matches       bool
	)
	err := tx.QueryRow(ctx, p.stmts.releaseCandidateLock,
		release.Project, release.ID, releasePayload).Scan(&storedPayload, &matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s is not a persisted project release",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("lock release candidate %s/%s: %w", release.Project, release.ID, err)
	}
	if !matches {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s no longer matches the persisted document",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID)
	}
	candidate, err := publishReleaseFromRow(release.Project, release.ID, release.Generation, release.GitCommit, storedPayload)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}

	var state string
	err = tx.QueryRow(ctx, p.stmts.manifestLock, release.Project, release.Generation).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: generation %s/%s has no persisted manifest",
			dataset.ErrReleaseActivationConflict, release.Project, release.Generation)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("lock generation manifest %s/%s: %w", release.Project, release.Generation, err)
	}
	if state != string(dataset.StateStaged) && state != string(dataset.StateReady) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: generation %s/%s manifest is %s, not %s or %s",
			dataset.ErrReleaseActivationConflict, release.Project, release.Generation, state,
			string(dataset.StateStaged), string(dataset.StateReady))
	}

	var activeGeneration string
	err = tx.QueryRow(ctx, p.stmts.generationPointer, release.Project).Scan(&activeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: project %s has no active generation",
			dataset.ErrReleaseActivationConflict, release.Project)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("read active generation for %s: %w", release.Project, err)
	}
	if activeGeneration != release.Generation {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: active generation of %s is %s, not the candidate generation %s",
			dataset.ErrReleaseActivationConflict, release.Project, activeGeneration, release.Generation)
	}

	var (
		pointerReleaseID string
		currentRevision  int64
	)
	lockErr := tx.QueryRow(ctx, p.stmts.releasePointerLock, release.Project).Scan(&pointerReleaseID, &currentRevision)
	if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("lock active release pointer for %s: %w", release.Project, lockErr)
	}
	if lockErr == nil {
		if pointerReleaseID == release.ID {
			return dataset.ActiveRelease{Release: candidate, Revision: currentRevision}, nil
		}
		if currentRevision != expectedRevision {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected active revision %d, found %d",
				dataset.ErrReleaseActivationConflict, release.Project, release.ID, expectedRevision, currentRevision)
		}
		var committedRevision int64
		casErr := tx.QueryRow(ctx, p.stmts.releasePointerCAS,
			release.Project, release.ID, expectedRevision+1, expectedRevision).Scan(&committedRevision)
		if errors.Is(casErr, pgx.ErrNoRows) {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: active release pointer moved during activation", dataset.ErrReleaseActivationConflict)
		}
		if casErr != nil {
			return dataset.ActiveRelease{}, fmt.Errorf("move active release pointer for %s: %w", release.Project, casErr)
		}
		if committedRevision != expectedRevision+1 {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: active release revision committed as %d, want %d",
				datasetpostgres.ErrUnexpectedStoreResult, committedRevision, expectedRevision+1)
		}
		return dataset.ActiveRelease{Release: candidate, Revision: committedRevision}, nil
	}

	if expectedRevision != 0 {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected active revision %d, found no active release",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID, expectedRevision)
	}
	tag, err := tx.Exec(ctx, p.stmts.releasePointerInsert, release.Project, release.ID)
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("create active release pointer for %s: %w", release.Project, err)
	}
	if tag.RowsAffected() == 1 {
		return dataset.ActiveRelease{Release: candidate, Revision: 1}, nil
	}
	var concurrentReleaseID string
	var concurrentRevision int64
	rereadErr := tx.QueryRow(ctx, p.stmts.releasePointerRead, release.Project).Scan(&concurrentReleaseID, &concurrentRevision)
	if errors.Is(rereadErr, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: active release pointer vanished during activation", datasetpostgres.ErrUnexpectedStoreResult)
	}
	if rereadErr != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("reread active release pointer for %s: %w", release.Project, rereadErr)
	}
	if concurrentReleaseID != release.ID {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected no active release, found %s at revision %d (concurrent activation)",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID, concurrentReleaseID, concurrentRevision)
	}
	return dataset.ActiveRelease{Release: candidate, Revision: concurrentRevision}, nil
}

func (p *PublishStore) readPublishOwner(ctx context.Context, tx pgx.Tx, candidate *explorer.Revision) (explorer.Explorer, error) {
	management := explorer.ManagementInteractive
	if candidate.ExplorerID == "default" {
		management = explorer.ManagementRepository
	}
	var owner explorer.Explorer
	err := scanOwner(tx.QueryRow(ctx, p.stmts.ownerGuard,
		candidate.Project, candidate.ExplorerID, string(management)), &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.Explorer{}, explorer.ErrDraftConflict
	}
	if err != nil {
		return explorer.Explorer{}, fmt.Errorf("read owner guard for %q/%q: %w", candidate.Project, candidate.ExplorerID, err)
	}
	if owner.Project != candidate.Project || owner.ExplorerID != candidate.ExplorerID {
		return explorer.Explorer{}, explorer.ErrDraftConflict
	}
	return owner, nil
}

func (p *PublishStore) lockPriorRevision(ctx context.Context, tx pgx.Tx, owner explorer.Explorer, candidate *explorer.Revision) (storedRevisionRow, error) {
	var prior storedRevisionRow
	err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.priorLock, owner.ActiveRevisionID), &prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is not a stored revision",
			explorer.ErrDraftConflict, owner.ActiveRevisionID)
	}
	if err != nil {
		return storedRevisionRow{}, fmt.Errorf("lock prior revision %q: %w", owner.ActiveRevisionID, err)
	}
	if prior.Project != owner.Project || prior.ExplorerID != owner.ExplorerID ||
		prior.Project != candidate.Project || prior.ExplorerID != candidate.ExplorerID {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is foreign to %q/%q",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, owner.Project, owner.ExplorerID)
	}
	if prior.Status != string(explorer.RevisionActive) {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is %s, not ACTIVE",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, prior.Status)
	}
	return prior, nil
}

func activatedPublicationPayload(candidate *storedRevisionRow) (string, error) {
	var stored map[string]json.RawMessage
	if len(candidate.Publication) == 0 {
		return "", fmt.Errorf("%w: candidate revision %q has no stored publication document",
			datasetpostgres.ErrUnexpectedStoreResult, candidate.ID)
	}
	if err := json.Unmarshal(candidate.Publication, &stored); err != nil || stored == nil {
		return "", fmt.Errorf("%w: candidate revision %q publication document is undecodable: %v",
			datasetpostgres.ErrUnexpectedStoreResult, candidate.ID, err)
	}
	updated := make(map[string]json.RawMessage, len(stored)+3)
	for key, value := range stored {
		updated[key] = value
	}
	now := time.Now().UTC()
	stateValue, err := json.Marshal(string(explorer.RevisionActive))
	if err != nil {
		return "", err
	}
	revisionValue, err := json.Marshal(candidate.ID)
	if err != nil {
		return "", err
	}
	updatedValue, err := json.Marshal(now)
	if err != nil {
		return "", err
	}
	updated["state"] = stateValue
	updated["revisionId"] = revisionValue
	updated["updatedAt"] = updatedValue
	if _, present := stored["generation"]; !present && candidate.SourceGeneration != "" {
		generationValue, err := json.Marshal(candidate.SourceGeneration)
		if err != nil {
			return "", err
		}
		updated["generation"] = generationValue
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return "", fmt.Errorf("encode activation publication for revision %q: %w", candidate.ID, err)
	}
	return string(encoded), nil
}

func marshalPublishReleasePayload(release dataset.ProjectRelease) (string, error) {
	normalized := release
	if normalized.Publications == nil {
		normalized.Publications = []dataset.ReleasePublication{}
	}
	if normalized.RequiredVerifications == nil {
		normalized.RequiredVerifications = []dataset.ContractVerification{}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode project release payload: %w", err)
	}
	return string(encoded), nil
}

func publishReleaseFromRow(project, id, generation, gitCommit, storedPayload string) (dataset.ProjectRelease, error) {
	var release dataset.ProjectRelease
	decoder := json.NewDecoder(strings.NewReader(storedPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release payload is malformed: %v", datasetpostgres.ErrUnexpectedStoreResult, err)
	}
	if release.ID == "" {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release candidate id is required", datasetpostgres.ErrUnexpectedStoreResult)
	}
	ref := dataset.Ref{Project: release.Project, Generation: release.Generation}
	if err := ref.Validate(); err != nil {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release is invalid: %v", datasetpostgres.ErrUnexpectedStoreResult, err)
	}
	if release.GitCommit == "" {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release gitCommit is required", datasetpostgres.ErrUnexpectedStoreResult)
	}
	for _, binding := range release.Publications {
		if err := binding.Selector.Validate(); err != nil {
			return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release publication selector %v: %v",
				datasetpostgres.ErrUnexpectedStoreResult, binding.Selector, err)
		}
	}
	for _, verification := range release.RequiredVerifications {
		if err := verification.Selector.Validate(); err != nil {
			return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release verification selector %v: %v",
				datasetpostgres.ErrUnexpectedStoreResult, verification.Selector, err)
		}
	}
	if release.Project != project || release.ID != id || release.Generation != generation || release.GitCommit != gitCommit {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release scan columns disagree with its payload for %s/%s",
			datasetpostgres.ErrUnexpectedStoreResult, project, id)
	}
	return release, nil
}

func (p *PublishStore) readReceiptRowInTx(ctx context.Context, tx pgx.Tx, project, explorerID, id string) (*storedReceipt, error) {
	var row storedReceipt
	err := scanReceipt(tx.QueryRow(ctx, p.stmts.receiptClaim, project, explorerID, id), &row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read compilation receipt %q/%q/%q: %w", project, explorerID, id, err)
	}
	return &row, nil
}

func isTypedReleaseConflict(err error) bool {
	return errors.Is(err, dataset.ErrReleaseActivationConflict) ||
		errors.Is(err, datasetpostgres.ErrUnexpectedStoreResult)
}

func isTypedPublicationRefusal(err error) bool {
	return errors.Is(err, explorer.ErrCorruptReceipt) ||
		errors.Is(err, explorer.ErrNotFound) ||
		errors.Is(err, explorer.ErrImmutableRevision) ||
		errors.Is(err, explorer.ErrDraftConflict) ||
		isTypedReleaseConflict(err)
}

func isDeadlockError(err error) bool {
	return pgstore.IsDeadlockDetected(err)
}
