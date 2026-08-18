package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	QueryRows(context.Context, string, int, map[string]any, store.RowVisitor) error
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}
type Store struct{ client client }

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	return &Store{client: client}, nil
}
func key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func explorerKey(project, id string) string { return "explorer_" + key(project, id) }
func repositoryConfigKey(project string) string {
	return "repository_config_" + key(project, "default")
}
func configKey(project, id string) string { return "explorer_config_" + key(project, id) }
func (s *Store) ListConfigs(ctx context.Context, project string) ([]explorer.RepositoryConfig, error) {
	out := []explorer.RepositoryConfig{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, 1000, map[string]any{"@c": RepositoryConfigsCollection, "project": project}, func(row map[string]any) error {
		v, err := decode[explorer.RepositoryConfig](row)
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	for i := range out {
		if out[i].ExplorerID == "" {
			out[i].ExplorerID, out[i].Management = "default", explorer.ManagementRepository
		}
	}
	return out, err
}
func (s *Store) GetConfig(ctx context.Context, project, id string) (*explorer.RepositoryConfig, error) {
	var out *explorer.RepositoryConfig
	keys := []string{configKey(project, id)}
	if id == "default" {
		keys = append(keys, repositoryConfigKey(project))
	}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key IN @keys SORT d.updatedAt DESC RETURN d`, 1, map[string]any{"@c": RepositoryConfigsCollection, "keys": keys}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	if out.ExplorerID == "" {
		out.ExplorerID, out.Management = "default", explorer.ManagementRepository
	}
	return out, nil
}
func (s *Store) SaveConfig(ctx context.Context, value explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	value.UpdatedAt = time.Now().UTC()
	doc, err := document(value, configKey(value.Project, value.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.RepositoryConfig
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE @doc IN @@c RETURN NEW`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": configKey(value.Project, value.ExplorerID), "doc": doc}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) GetRepositoryConfig(ctx context.Context, project string) (*explorer.RepositoryConfig, error) {
	return s.GetConfig(ctx, project, "default")
}
func (s *Store) SaveRepositoryConfig(ctx context.Context, value explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	value.ExplorerID, value.Management = "default", explorer.ManagementRepository
	return s.SaveConfig(ctx, value)
}
func (s *Store) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	out := []explorer.Explorer{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, 1000, map[string]any{"@c": ExplorersCollection, "project": project}, func(row map[string]any) error {
		v, err := decode[explorer.Explorer](row)
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	return out, err
}
func (s *Store) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	var out *explorer.Explorer
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(project, id), "project": project}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
func (s *Store) CreateInteractive(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}
func (s *Store) CreateRepository(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	return s.CreateInteractive(ctx, e)
}
func (s *Store) SaveDraft(ctx context.Context, e explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	e.DraftVersion = expected + 1
	e.UpdatedAt = time.Now().UTC()
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	digest := ""
	if len(expectedDigest) > 0 {
		digest = expectedDigest[0]
	}
	err = s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.draftVersion == @expected AND (@expectedDigest == "" OR d.draftDigest == @expectedDigest) UPDATE d WITH @doc IN @@c RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(e.Project, e.ExplorerID), "expected": expected, "expectedDigest": digest, "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}

func (s *Store) InsertRevision(ctx context.Context, revision explorer.Revision) (*explorer.Revision, error) {
	if revision.ID == "" {
		return nil, fmt.Errorf("revision ID is required")
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	var out *explorer.Revision
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID, "doc": doc}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func (s *Store) GetRevision(ctx context.Context, id string) (*explorer.Revision, error) {
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RevisionsCollection, "key": id}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

// TransitionRevision updates lifecycle fields only. The AQL never accepts a
// replacement definition/recipe document, preserving revision immutability.
func (s *Store) TransitionRevision(ctx context.Context, id string, status explorer.RevisionStatus, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	now := time.Now().UTC()
	patch := map[string]any{"status": status, "diagnostics": diagnostics}
	if status == explorer.RevisionReady {
		patch["readyAt"] = now
	}
	if status == explorer.RevisionFailed {
		patch["failedAt"] = now
	}
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH @patch IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": id, "patch": patch}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

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
FILTER candidate.status == "READY" OR (candidate.status == "ACTIVE" AND owner.activeRevisionId == @revisionKey)
FILTER execution != null AND execution.project == @project AND execution.datasetGeneration == @generation AND execution.state == "PUBLISHED"
LET prior = owner.activeRevisionId == null ? null : DOCUMENT(@@revisions, owner.activeRevisionId)
RETURN {manifest: manifest, active: active, owner: owner, candidate: candidate, prior: prior}`

const activationUpdateAQL = `FOR d IN @@c
FILTER d._key == @key
UPDATE d WITH @patch IN @@c
RETURN NEW`

type activationState struct {
	owner     map[string]any
	candidate map[string]any
	prior     map[string]any
}

func (s *Store) ActivateInteractive(ctx context.Context, project, explorerID, revisionID string) error {
	return s.activateOwnerRevision(ctx, project, explorerID, revisionID, explorer.ManagementInteractive, []string{ExplorersCollection, RevisionsCollection})
}

func (s *Store) ActivateRepository(ctx context.Context, project, revisionID string) error {
	return s.activateOwnerRevision(ctx, project, "default", revisionID, explorer.ManagementRepository, []string{ExplorersCollection, RevisionsCollection})
}

func (s *Store) activateOwnerRevision(ctx context.Context, project, explorerID, revisionID string, management explorer.ManagementMode, writeCollections []string) error {
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: writeCollections}, func(txCtx context.Context, tx store.Transaction) error {
		row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{
			"@explorers": ExplorersCollection, "@revisions": RevisionsCollection,
			"explorerKey": explorerKey(project, explorerID), "revisionKey": revisionID,
			"project": project, "explorerId": explorerID, "management": management,
		})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		return activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC())
	})
}

// ActivateRepositoryGeneration is the repository-only composite visibility
// switch. It validates all pointers first, then performs each single-collection
// modification inside one Arango transaction so no partial activation is
// visible if any update fails.
func (s *Store) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{publicationarango.BundleExecutionsCollection},
		Write: []string{datasetarango.LifecycleCollection, ExplorersCollection, RevisionsCollection},
	}, func(txCtx context.Context, tx store.Transaction) error {
		row, found, err := readActivationRow(txCtx, tx, compositeActivationReadAQL, map[string]any{
			"@lifecycle": datasetarango.LifecycleCollection, "@explorers": ExplorersCollection,
			"@revisions": RevisionsCollection, "@executions": publicationarango.BundleExecutionsCollection,
			"manifestKey": datasetarango.ManifestDocumentKey(dataset.Ref{Project: project, Generation: generation}),
			"activeKey":   datasetarango.ActiveDocumentKey(project), "explorerKey": explorerKey(project, "default"),
			"revisionKey": revisionID, "project": project, "generation": generation,
		})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		manifest, err := requiredActivationDocument(row, "manifest")
		if err != nil {
			return err
		}
		active, err := requiredActivationDocument(row, "active")
		if err != nil {
			return err
		}
		activeKey, err := activationDocumentKey(active)
		if err != nil {
			return err
		}
		manifestKey, err := activationDocumentKey(manifest)
		if err != nil {
			return err
		}
		dataset, ok := manifest["dataset"]
		if !ok {
			return fmt.Errorf("activation manifest has no dataset")
		}
		if err := updateActivationDocument(txCtx, tx, datasetarango.LifecycleCollection, activeKey, map[string]any{"dataset": dataset, "manifestKey": manifestKey}); err != nil {
			return err
		}
		return activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC())
	})
}

func readActivationRow(ctx context.Context, tx store.Transaction, query string, binds map[string]any) (map[string]any, bool, error) {
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

func activateRevisionAndOwner(ctx context.Context, tx store.Transaction, state activationState, now time.Time) error {
	candidateKey, err := activationDocumentKey(state.candidate)
	if err != nil {
		return err
	}
	candidate, err := decode[explorer.Revision](state.candidate)
	if err != nil {
		return fmt.Errorf("decode activation candidate: %w", err)
	}
	if state.prior != nil {
		priorKey, err := activationDocumentKey(state.prior)
		if err != nil {
			return err
		}
		if priorKey != candidateKey {
			if err := updateActivationDocument(ctx, tx, RevisionsCollection, priorKey, map[string]any{"status": explorer.RevisionSuperseded}); err != nil {
				return err
			}
		}
	}
	if err := updateActivationDocument(ctx, tx, RevisionsCollection, candidateKey, map[string]any{"status": explorer.RevisionActive, "activatedAt": now}); err != nil {
		return err
	}
	ownerKey, err := activationDocumentKey(state.owner)
	if err != nil {
		return err
	}
	return updateActivationDocument(ctx, tx, ExplorersCollection, ownerKey, activeExplorerPatch(candidate, candidateKey, now))
}

func activeExplorerPatch(candidate explorer.Revision, revisionKey string, now time.Time) map[string]any {
	publication := candidate.Publication
	materializations, dataset := explorer.WithDataframeSelectors(candidate.Recipe, candidate.Materializations, candidate.Dataset)
	publication.State = string(explorer.RevisionActive)
	publication.RevisionID = revisionKey
	publication.UpdatedAt = now
	return map[string]any{
		"activeRevisionId": revisionKey, "activeConfig": candidate.Config,
		"recipeDigest": candidate.RecipeDigest, "resolvedSchemaDigest": candidate.ResolvedSchemaDigest,
		"sourceGeneration": candidate.SourceGeneration, "dataset": dataset,
		"publication": publication, "emittedColumns": candidate.EmittedColumns,
		"materializations": materializations, "diagnostics": candidate.Diagnostics,
	}
}

func updateActivationDocument(ctx context.Context, tx store.Transaction, collection, key string, patch map[string]any) error {
	var updated bool
	err := tx.QueryRows(ctx, activationUpdateAQL, 1, map[string]any{"@c": collection, "key": key, "patch": patch}, func(map[string]any) error {
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

var _ explorer.Store = (*Store)(nil)

func decode[T any](value any) (T, error) {
	var out T
	raw, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
func document(value any, k string) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	doc["_key"] = k
	return doc, nil
}
