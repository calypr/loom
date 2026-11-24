package publicationdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/releasecommit"
	store "github.com/calypr/loom/internal/store/arango"
)

const activationReadAQL = `LET owner = DOCUMENT(@@explorers, @explorerKey)
LET candidate = DOCUMENT(@@revisions, @revisionKey)
FILTER owner != null AND candidate != null AND owner.project == @project AND owner.explorerId == @explorerId AND owner.managementMode == @management AND candidate.project == @project AND candidate.explorerId == @explorerId
FILTER candidate.status == "READY" OR (candidate.status == "ACTIVE" AND owner.activeRevisionId == @revisionKey)
LET prior = owner.activeRevisionId == null ? null : DOCUMENT(@@revisions, owner.activeRevisionId)
RETURN {owner: owner, candidate: candidate, prior: prior}`

const compositeActivationReadAQL = `LET manifest = DOCUMENT(@@lifecycle, @manifestKey)
LET active = DOCUMENT(@@lifecycle, @activeKey)
LET owner = DOCUMENT(@@explorers, @explorerKey)
LET candidate = DOCUMENT(@@revisions, @revisionKey)
LET execution = candidate == null OR LENGTH(candidate.materializations) == 0 ? null : DOCUMENT(@@executions, candidate.materializations[0].materializationId)
FILTER manifest != null AND manifest.recordType == "manifest" AND manifest.dataset.project == @project AND manifest.dataset.generation == @generation AND manifest.state IN ["STAGED", "READY"]
FILTER active != null AND active.recordType == "active_generation" AND active.project == @project
FILTER owner != null AND owner.project == @project AND owner.explorerId == "default" AND owner.managementMode == "REPOSITORY"
FILTER candidate != null AND candidate.project == @project AND candidate.explorerId == "default" AND candidate.sourceGeneration == @generation
FILTER candidate.status IN ["READY", "FAILED"] OR (candidate.status == "ACTIVE" AND owner.activeRevisionId == @revisionKey)
FILTER execution != null AND execution.project == @project AND execution.datasetGeneration == @generation AND execution.state == "PUBLISHED"
LET prior = owner.activeRevisionId == null ? null : DOCUMENT(@@revisions, owner.activeRevisionId)
RETURN {manifest: manifest, active: active, owner: owner, candidate: candidate, prior: prior}`

const activationUpdateAQL = `FOR d IN @@c
FILTER d._key == @key
UPDATE d WITH @patch IN @@c
RETURN NEW`

const ownerActivationUpdateAQL = `FOR d IN @@c
FILTER d._key == @key
REPLACE d WITH MERGE(UNSET(d, "activeConfig", "recipeDigest", "resolvedSchemaDigest", "sourceGeneration", "dataset", "publication", "emittedColumns", "materializations", "diagnostics"), {activeRevisionId: @revisionId}) IN @@c
RETURN NEW`

type activationState struct {
	owner     map[string]any
	candidate map[string]any
	prior     map[string]any
}

func (s *PublicationStore) ActivateRepositoryGeneration(ctx context.Context, release dataset.ProjectRelease, expectedRevision int64, revisionID string) error {
	project, generation := release.Project, release.Generation
	applied := false
	_, err := releasecommit.GoLive(ctx, s.client, releasecommit.Request{
		Release: release, ExpectedRevision: expectedRevision,
		Collections: store.TransactionCollections{
			Read:  []string{publicationarango.BundleExecutionsCollection},
			Write: []string{db.ExplorersCollection, db.RevisionsCollection},
		},
		Prepare: func(txCtx context.Context, tx store.RowQueryer) (releasecommit.Participant, error) {
			state, err := readRepositoryActivation(txCtx, tx, project, generation, revisionID)
			if err != nil {
				return nil, &explorer.RepositoryGenerationActivationError{Cause: err}
			}
			return func(txCtx context.Context, tx store.RowQueryer, _ dataset.ActiveRelease) error {
				applied = true
				if err := activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC()); err != nil {
					return &explorer.RepositoryGenerationActivationError{Cause: err}
				}
				return nil
			}, nil
		},
	})
	if err != nil && applied {
		var generationErr *explorer.RepositoryGenerationActivationError
		if !errors.As(err, &generationErr) {
			return &explorer.RepositoryGenerationActivationError{Cause: err}
		}
	}
	return err
}

func readRepositoryActivation(ctx context.Context, tx store.RowQueryer, project, generation, revisionID string) (activationState, error) {
	row, found, err := readActivationRow(ctx, tx, compositeActivationReadAQL, map[string]any{
		"@lifecycle": datasetarango.LifecycleCollection, "@explorers": db.ExplorersCollection,
		"@revisions": db.RevisionsCollection, "@executions": publicationarango.BundleExecutionsCollection,
		"manifestKey": datasetarango.ManifestDocumentKey(dataset.Ref{Project: project, Generation: generation}),
		"activeKey":   datasetarango.ActiveDocumentKey(project), "explorerKey": explorerDocumentKey(project, "default"),
		"revisionKey": revisionID, "project": project, "generation": generation,
	})
	if err != nil {
		return activationState{}, err
	}
	if !found {
		return activationState{}, explorer.ErrDraftConflict
	}
	state, err := activationStateFromRow(row)
	if err != nil {
		return activationState{}, err
	}
	for _, name := range []string{"manifest", "active"} {
		document, err := requiredActivationDocument(row, name)
		if err != nil {
			return activationState{}, err
		}
		if _, err := activationDocumentKey(document); err != nil {
			return activationState{}, err
		}
		if name == "manifest" {
			if _, ok := document["dataset"]; !ok {
				return activationState{}, fmt.Errorf("activation manifest has no dataset")
			}
		}
	}
	return state, nil
}

func readActivationRow(ctx context.Context, tx store.RowQueryer, query string, binds map[string]any) (map[string]any, bool, error) {
	var row map[string]any
	err := tx.QueryRows(ctx, query, 1, binds, func(value map[string]any) error {
		if row != nil {
			return fmt.Errorf("activation guard returned more than one row")
		}
		row = value
		return nil
	})
	return row, row != nil, err
}

func activationStateFromRow(row map[string]any) (activationState, error) {
	owner, err := requiredActivationDocument(row, "owner")
	if err != nil {
		return activationState{}, err
	}
	candidate, err := requiredActivationDocument(row, "candidate")
	if err != nil {
		return activationState{}, err
	}
	prior, err := optionalActivationDocument(row, "prior")
	if err != nil {
		return activationState{}, err
	}
	return activationState{owner: owner, candidate: candidate, prior: prior}, nil
}

func requiredActivationDocument(row map[string]any, field string) (map[string]any, error) {
	document, err := optionalActivationDocument(row, field)
	if err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("activation guard returned no %s document", field)
	}
	return document, nil
}

func optionalActivationDocument(row map[string]any, field string) (map[string]any, error) {
	value := row[field]
	if value == nil {
		return nil, nil
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("activation guard returned invalid %s document", field)
	}
	return document, nil
}

func activationDocumentKey(document map[string]any) (string, error) {
	key, ok := document["_key"].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("activation document has no _key")
	}
	return key, nil
}

func activateRevisionAndOwner(ctx context.Context, tx store.RowQueryer, state activationState, now time.Time) error {
	candidateKey, err := activationDocumentKey(state.candidate)
	if err != nil {
		return err
	}
	if state.prior != nil {
		priorKey, err := activationDocumentKey(state.prior)
		if err != nil {
			return err
		}
		if priorKey != candidateKey {
			if err := updateRevisionActivation(ctx, tx, priorKey, map[string]any{"status": explorer.RevisionSuperseded}); err != nil {
				return err
			}
		}
	}
	publication := activatedPublication(state.candidate, candidateKey, now)
	if err := updateRevisionActivation(ctx, tx, candidateKey, map[string]any{
		"status":      explorer.RevisionActive,
		"activatedAt": now,
		"failedAt":    nil,
		"diagnostics": []any{},
		"publication": publication,
	}); err != nil {
		return err
	}
	ownerKey, err := activationDocumentKey(state.owner)
	if err != nil {
		return err
	}
	return updateActiveExplorerDocument(ctx, tx, ownerKey, candidateKey)
}

func activatedPublication(candidate map[string]any, revisionID string, now time.Time) map[string]any {
	publication := map[string]any{}
	if value, ok := candidate["publication"].(map[string]any); ok {
		for key, item := range value {
			publication[key] = item
		}
	}
	publication["state"] = string(explorer.RevisionActive)
	publication["revisionId"] = revisionID
	publication["updatedAt"] = now
	if _, ok := publication["generation"]; !ok {
		if generation, ok := candidate["sourceGeneration"].(string); ok && generation != "" {
			publication["generation"] = generation
		}
	}
	return publication
}

func updateActiveExplorerDocument(ctx context.Context, tx store.RowQueryer, ownerKey, revisionID string) error {
	var updated bool
	err := tx.QueryRows(ctx, ownerActivationUpdateAQL, 1, map[string]any{"@c": db.ExplorersCollection, "key": ownerKey, "revisionId": revisionID}, func(map[string]any) error {
		if updated {
			return fmt.Errorf("activation owner update returned more than one document")
		}
		updated = true
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}

func updateRevisionActivation(ctx context.Context, tx store.RowQueryer, key string, patch map[string]any) error {
	var updated bool
	err := tx.QueryRows(ctx, activationUpdateAQL, 1, map[string]any{"@c": db.RevisionsCollection, "key": key, "patch": patch}, func(map[string]any) error {
		if updated {
			return fmt.Errorf("activation update returned more than one document")
		}
		updated = true
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}
