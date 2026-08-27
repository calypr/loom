package explorer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

// Service is the persistence-neutral lifecycle coordinator. Compiler and
// publication adapters are intentionally injected by server wiring.
type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer store is required")
	}
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}, nil
}
func (s *Service) List(ctx context.Context, project string) ([]Explorer, error) {
	return s.store.List(ctx, projectid.Legacy(project))
}
func (s *Service) RepositoryConfig(ctx context.Context, project string) (*RepositoryConfig, error) {
	return s.store.GetRepositoryConfig(ctx, projectid.Legacy(project))
}
func (s *Service) SaveRepositoryConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	value.Project = projectid.Legacy(value.Project)
	return s.store.SaveRepositoryConfig(ctx, value)
}
func (s *Service) Get(ctx context.Context, project, id string) (*Explorer, error) {
	return s.store.Get(ctx, projectid.Legacy(project), id)
}

// LoadExplorerState returns the one canonical response for the public
// Explorer GET route. It loads the identity and immutable active revision
// together, then derives the renderer-facing projection from that revision.
// The HTTP layer should not reconstruct this response from individual legacy
// adapters.
func (s *Service) LoadExplorerState(ctx context.Context, project, id string) (ExplorerStateV1, error) {
	project = projectid.Canonical(project)
	id = strings.TrimSpace(id)
	owner, err := s.Get(ctx, project, id)
	if errors.Is(err, ErrNotFound) && id == "default" {
		// Repository bootstrap can briefly expose its repository record before
		// the Explorer identity is created. Preserve the read contract for that
		// window without manufacturing editable authoring state.
		repository, repositoryErr := s.RepositoryConfig(ctx, project)
		if repositoryErr != nil {
			return ExplorerStateV1{}, err
		}
		owner = &Explorer{
			Project:          projectid.Legacy(project),
			ExplorerID:       "default",
			Title:            configV2Title(repository.Config),
			ManagementMode:   ManagementRepository,
			ActiveRevisionID: repository.ActiveRevisionID,
			Publication:      repository.Publication,
			UpdatedAt:        repository.UpdatedAt,
		}
		err = nil
	}
	if err != nil {
		return ExplorerStateV1{}, err
	}
	if owner == nil {
		return ExplorerStateV1{}, fmt.Errorf("Explorer %s/%s resolved to an empty identity", project, id)
	}

	state := newExplorerStateResponse(owner)
	if owner.ActiveRevisionID == "" {
		markNotPublished(&state)
		return state, nil
	}
	revision, err := s.Revision(ctx, owner.ActiveRevisionID)
	if err != nil {
		return ExplorerStateV1{}, fmt.Errorf("load active Explorer revision %q: %w", owner.ActiveRevisionID, err)
	}
	state.Active.RevisionID = revision.ID
	state.Active.IntentDigest = revision.IntentDigest
	state.Active.Status = string(revision.Status)
	state.Active.Bundle = append([]byte(nil), revision.AuthoringBundle...)
	state.Generated.RecipeDigest = revision.RecipeDigest
	state.Generated.ResolvedSchemaDigest = revision.ResolvedSchemaDigest
	state.Generated.SourceGeneration = revision.SourceGeneration
	state.Generated.EmittedColumns = append([]EmittedColumn(nil), revision.EmittedColumns...)
	state.Generated.Materializations, state.Generated.Dataset = WithDataframeSelectors(revision.Recipe, revision.Materializations, revision.Dataset)
	state.Generated.Publication = revision.Publication
	state.Generated.Publication.State = firstNonEmptyString(revision.Publication.State, string(revision.Status), ExplorerRuntimeV1NotPublished)
	state.Generated.Publication.RevisionID = revision.ID
	state.Generated.Diagnostics = append([]Diagnostic(nil), revision.Diagnostics...)
	state.Runtime = s.BuildViewerProjection(revision)
	if state.Runtime == nil || len(state.Runtime.Outputs) == 0 {
		state.Generated.Publication.State = ExplorerRuntimeV1NotPublished
		if state.Runtime != nil {
			state.Runtime.Status = ExplorerRuntimeV1NotPublished
			state.Runtime.Publication.State = ExplorerRuntimeV1NotPublished
		}
	}
	return state, nil
}

// BuildViewerProjection derives the renderer-facing state from one immutable
// revision. It is deliberately tolerant of revisions that contain an
// executable recipe but no authored presentation: the default table is
// generated from the query and publication metadata.
func (s *Service) BuildViewerProjection(revision *Revision) *ExplorerRuntimeV1 {
	return buildViewerProjection(revision)
}

func newExplorerStateResponse(owner *Explorer) ExplorerStateV1 {
	project := projectid.Canonical(owner.Project)
	state := ExplorerStateV1{
		APIVersion: ExplorerStateV1APIVersion,
		Kind:       ExplorerStateV1Kind,
		Project:    project,
		ExplorerID: owner.ExplorerID,
		Title:      owner.Title,
		Management: owner.ManagementMode,
		Draft: ExplorerStateV1Draft{
			Bundle:  append([]byte(nil), owner.DraftConfig...),
			Version: owner.DraftVersion,
			Digest:  owner.DraftDigest,
		},
		Generated: ExplorerStateV1Generated{
			RecipeDigest:         owner.RecipeDigest,
			ResolvedSchemaDigest: owner.ResolvedSchemaDigest,
			SourceGeneration:     owner.SourceGeneration,
			EmittedColumns:       append([]EmittedColumn(nil), owner.EmittedColumns...),
			Materializations:     append([]Materialization(nil), owner.Materializations...),
			Dataset:              owner.Dataset,
			Publication:          owner.Publication,
			Diagnostics:          append([]Diagnostic(nil), owner.Diagnostics...),
		},
		ActiveURL: explorerURL(project, owner.ExplorerID),
		UpdatedBy: owner.UpdatedBy,
		UpdatedAt: owner.UpdatedAt,
	}
	return state
}

func markNotPublished(state *ExplorerStateV1) {
	state.Generated.Publication.State = ExplorerRuntimeV1NotPublished
	state.Runtime = nil
}

func configV2Title(raw json.RawMessage) string {
	var config ConfigV2
	if json.Unmarshal(raw, &config) != nil || strings.TrimSpace(config.Explorer.Title) == "" {
		return "Default"
	}
	return config.Explorer.Title
}

func explorerURL(project, id string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(id)
}

// CreateEmptyInteractive creates only the Explorer identity. Unpublished
// Explorers have no persisted authoring content and hydrate as an empty valid
// Builder model.
func (s *Service) CreateEmptyInteractive(ctx context.Context, project, id, title, actor string) (*Explorer, error) {
	if id == "" || id == "default" {
		return nil, fmt.Errorf("invalid interactive Explorer identity")
	}
	return s.store.CreateInteractive(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: ManagementInteractive, UpdatedBy: actor, UpdatedAt: s.now()})
}

// SaveWorkspaceDraft persists canonical V2 authoring intent with optimistic
// concurrency. It never compiles, materializes, or changes the active pointer.
func (s *Service) SaveWorkspaceDraft(ctx context.Context, project, id string, workspace authoringv2.Workspace, expectedVersion int64, expectedDigest, actor string) (*Explorer, error) {
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	digest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	owner, err := s.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	owner.DraftConfig = canonical
	owner.DraftDigest = digest
	owner.UpdatedBy = actor
	owner.UpdatedAt = s.now()
	if len(workspace.Tabs) > 0 && strings.TrimSpace(workspace.Tabs[0].Title) != "" {
		owner.Title = workspace.Tabs[0].Title
	}
	return s.store.SaveDraft(ctx, *owner, expectedVersion, expectedDigest)
}
func (s *Service) Revision(ctx context.Context, id string) (*Revision, error) {
	return s.store.GetRevision(ctx, id)
}
func (s *Service) ActiveRevision(ctx context.Context, project, id string) (*Revision, error) {
	e, err := s.store.Get(ctx, projectid.Legacy(project), id)
	if err != nil {
		return nil, err
	}
	if e.ActiveRevisionID == "" {
		return nil, ErrNotFound
	}
	return s.store.GetRevision(ctx, e.ActiveRevisionID)
}
func (s *Service) FailRevision(ctx context.Context, id string, diagnostics []Diagnostic) (*Revision, error) {
	return s.store.TransitionRevision(ctx, id, RevisionFailed, diagnostics)
}

// CompilationReceiptForExplorer performs the tenant-scoped lookup used by
// execution and publication. The ID-only method above remains for legacy
// callers during the receipt migration.
func (s *Service) CompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceiptForExplorer(ctx, projectid.Canonical(project), explorerID, id)
}

func (s *Service) CompilationReceiptByCompilationKey(ctx context.Context, project, explorerID, compilationKey string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceiptByCompilationKey(ctx, projectid.Canonical(project), explorerID, compilationKey)
}

func (s *Service) StoreCompilationReceipt(ctx context.Context, receipt CompilationReceipt) (*CompilationReceipt, error) {
	return s.store.InsertCompilationReceipt(ctx, receipt)
}

// PublishAuthoring atomically stores the server receipt and immutable revision
// and switches the Explorer's active pointer.
func (s *Service) PublishAuthoring(ctx context.Context, receipt CompilationReceipt, revision Revision) (*Revision, error) {
	if receipt.ID == "" || revision.ID == "" || revision.CompilationReceiptID != receipt.ID {
		return nil, fmt.Errorf("authoring publication identity is incomplete")
	}
	receipt.Project = projectid.Canonical(receipt.Project)
	revision.Project = projectid.Legacy(revision.Project)
	return s.store.PublishAuthoring(ctx, receipt, revision)
}

// UpsertRepositoryV2 records the repository-owned default and its immutable
// receipt-backed ready revision after dataframe publication has succeeded.
// Activation is intentionally a separate call so callers can compose it with
// the dataset release switch where the durable adapter supports that atomic
// transaction.
func (s *Service) UpsertRepositoryV2(ctx context.Context, receipt CompilationReceipt, sourceCommit, actor string, materializations []Materialization, dataset DatasetMetadata, publication PublicationMetadata) (*Explorer, *Revision, error) {
	if err := receipt.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid repository compilation receipt: %w", err)
	}
	if strings.TrimSpace(receipt.ExplorerID) != "default" {
		return nil, nil, fmt.Errorf("repository compilation receipt must target default")
	}
	if len(receipt.NormalizedBundle) == 0 || len(receipt.CompiledConfig) == 0 || len(receipt.PublicOutputContract) == 0 {
		return nil, nil, fmt.Errorf("repository compilation receipt is missing durable V2 artifacts")
	}
	project := projectid.Canonical(receipt.Project)
	storageProject := projectid.Legacy(project)
	workspace := append(json.RawMessage(nil), receipt.NormalizedBundle...)
	compiledConfig := append(json.RawMessage(nil), receipt.CompiledConfig...)
	title := "Default"
	if decoded, err := authoringv2.DecodeWorkspace(workspace); err == nil && len(decoded.Tabs) > 0 {
		title = decoded.Tabs[0].Title
	}
	if _, err := s.StoreCompilationReceipt(ctx, receipt); err != nil {
		return nil, nil, err
	}
	owner, err := s.store.Get(ctx, storageProject, "default")
	if errors.Is(err, ErrNotFound) {
		owner, err = s.store.CreateRepository(ctx, Explorer{Project: storageProject, ExplorerID: "default", Title: title, ManagementMode: ManagementRepository, DraftConfig: workspace, DraftVersion: 1, DraftDigest: receipt.IntentDigest, SourceGeneration: receipt.SourceGeneration, Dataset: dataset, Publication: publication, UpdatedBy: actor, UpdatedAt: s.now()})
	} else if err == nil {
		owner.Title, owner.DraftConfig, owner.DraftDigest, owner.SourceGeneration, owner.Dataset, owner.Publication, owner.UpdatedBy = title, workspace, receipt.IntentDigest, receipt.SourceGeneration, dataset, publication, actor
		owner, err = s.store.SaveDraft(ctx, *owner, owner.DraftVersion)
	}
	if err != nil {
		return nil, nil, err
	}
	revisionID := RepositoryRevisionID(project, sourceCommit, receipt.IntentDigest, receipt.SourceGeneration, receipt.ID)
	now := s.now()
	revision, err := s.store.InsertRevision(ctx, Revision{ID: revisionID, Project: storageProject, ExplorerID: "default", Config: compiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: workspace, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: append(json.RawMessage(nil), receipt.PublicOutputContract...), Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, SourceCommit: sourceCommit, Dataset: dataset, Publication: publication, Materializations: append([]Materialization(nil), materializations...), EmittedColumns: append([]EmittedColumn(nil), receipt.EmittedColumns...), Status: RevisionReady, CreatedBy: actor, CreatedAt: now, ReadyAt: &now})
	if err != nil {
		return nil, nil, err
	}
	return owner, revision, nil
}
func (s *Service) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.store.ActivateRepositoryGeneration(ctx, projectid.Legacy(project), generation, revisionID)
}

// RepositoryRevisionID is stable for retrying the same checked-out repository
// content and immutable compilation receipt against the same Loom generation.
// Including the receipt identity prevents a compiler-contract upgrade from
// colliding with a revision produced under older lowering semantics.
func RepositoryRevisionID(project, sourceCommit, definitionDigest, sourceGeneration string, receiptIdentity ...string) string {
	artifact := ""
	if len(receiptIdentity) > 0 {
		artifact = receiptIdentity[0]
	}
	sum := sha256.Sum256([]byte(project + "\x00default\x00" + sourceCommit + "\x00" + definitionDigest + "\x00" + sourceGeneration + "\x00" + artifact))
	return "repository_" + hex.EncodeToString(sum[:])
}
