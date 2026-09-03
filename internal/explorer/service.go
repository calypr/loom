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

	"github.com/calypr/loom/internal/dataset"
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
	revision, err := s.store.GetRevision(ctx, owner.ActiveRevisionID)
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
	state.Generated.Materializations = append([]Materialization(nil), revision.Materializations...)
	state.Generated.Dataset = revision.Dataset
	state.Generated.Dataset.Outputs = append([]DatasetOutput(nil), revision.Dataset.Outputs...)
	state.Generated.Publication = revision.Publication
	state.Generated.Publication.State = firstNonEmptyString(revision.Publication.State, string(revision.Status), ExplorerRuntimeV1NotPublished)
	state.Generated.Publication.RevisionID = revision.ID
	state.Generated.Diagnostics = append([]Diagnostic(nil), revision.Diagnostics...)
	state.Runtime = BuildViewerProjection(revision)
	if state.Runtime == nil || len(state.Runtime.Outputs) == 0 {
		state.Generated.Publication.State = ExplorerRuntimeV1NotPublished
		if state.Runtime != nil {
			state.Runtime.Status = ExplorerRuntimeV1NotPublished
			state.Runtime.Publication.State = ExplorerRuntimeV1NotPublished
		}
	}
	return state, nil
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
		Generated: ExplorerStateV1Generated{},
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

func explorerURL(project, id string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(id)
}

// createEmptyInteractive creates only the Explorer identity. Unpublished
// Explorers have no persisted authoring content and hydrate as an empty valid
// Builder model.
func (s *Service) createEmptyInteractive(ctx context.Context, project, id, title, actor string) (*Explorer, error) {
	if id == "" || id == "default" {
		return nil, fmt.Errorf("invalid interactive Explorer identity")
	}
	return s.store.Create(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: ManagementInteractive, UpdatedBy: actor, UpdatedAt: s.now()})
}

// CreateInteractiveFrom creates a new Explorer identity and, when requested,
// seeds it from a server-owned source workspace without asking the browser to
// reconstruct or re-identify that workspace.
func (s *Service) CreateInteractiveFrom(ctx context.Context, project, id, title, sourceExplorerID, actor string) (*Explorer, error) {
	if strings.TrimSpace(sourceExplorerID) == "" {
		return s.createEmptyInteractive(ctx, project, id, title, actor)
	}
	if id == "" || id == "default" {
		return nil, fmt.Errorf("invalid interactive Explorer identity")
	}
	source, err := s.Get(ctx, project, sourceExplorerID)
	if err != nil {
		return nil, err
	}
	raw := source.DraftConfig
	if len(raw) == 0 && source.ActiveRevisionID != "" {
		revision, revisionErr := s.store.GetRevision(ctx, source.ActiveRevisionID)
		if revisionErr != nil {
			return nil, revisionErr
		}
		raw = revision.AuthoringBundle
	}
	if len(raw) == 0 {
		return s.createEmptyInteractive(ctx, project, id, title, actor)
	}
	workspace, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		return nil, fmt.Errorf("source Explorer has no cloneable workspace: %w", err)
	}
	workspace.Explorer.Title = title
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	digest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	return s.store.Create(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: ManagementInteractive, DraftConfig: canonical, DraftVersion: 1, DraftDigest: digest, UpdatedBy: actor, UpdatedAt: s.now()})
}

// ApplyWorkspaceCommands is the authoritative Builder mutation boundary. It
// resolves backend-owned identities, applies a command batch atomically, and
// persists both the new draft and the replay record in one compare-and-swap.
func (s *Service) ApplyWorkspaceCommands(ctx context.Context, project, id string, catalog authoringv2.CatalogSnapshot, request authoringv2.ApplyCommandsRequest, actor string) (*authoringv2.ApplyCommandsResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	commandDigest, err := request.Digest()
	if err != nil {
		return nil, err
	}
	owner, err := s.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if owner.LastAuthoringCommandID == request.CommandID {
		if owner.LastAuthoringCommandDigest != commandDigest {
			return nil, ErrAuthoringCommandConflict
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode replayed authoring workspace: %w", decodeErr)
		}
		results := []authoringv2.CommandResult{}
		if len(owner.LastAuthoringCommandResults) != 0 {
			if decodeErr := json.Unmarshal(owner.LastAuthoringCommandResults, &results); decodeErr != nil {
				return nil, fmt.Errorf("decode replayed authoring command results: %w", decodeErr)
			}
		}
		return &authoringv2.ApplyCommandsResponse{CommandID: request.CommandID, Workspace: workspace, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Results: results, Diagnostics: []any{}}, nil
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || (request.ExpectedDraftDigest != "" && owner.DraftDigest != request.ExpectedDraftDigest) {
		return nil, ErrDraftConflict
	}

	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: owner.Title}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}
	if len(owner.DraftConfig) != 0 {
		workspace, err = authoringv2.DecodeWorkspace(owner.DraftConfig)
	} else if owner.ActiveRevisionID != "" {
		var revision *Revision
		revision, err = s.store.GetRevision(ctx, owner.ActiveRevisionID)
		if err == nil {
			workspace, err = authoringv2.DecodeWorkspace(revision.AuthoringBundle)
		}
	}
	if err != nil {
		return nil, err
	}
	workspace, results, err := authoringv2.ApplyCommands(workspace, catalog, request.CommandID, request.Commands)
	if err != nil {
		return nil, err
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	draftDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	resultJSON, err := json.Marshal(results)
	if err != nil {
		return nil, err
	}
	owner.DraftConfig = canonical
	owner.DraftDigest = draftDigest
	owner.LastAuthoringCommandID = request.CommandID
	owner.LastAuthoringCommandDigest = commandDigest
	owner.LastAuthoringCommandResults = resultJSON
	owner.UpdatedBy = actor
	owner.UpdatedAt = s.now()
	if strings.TrimSpace(workspace.Explorer.Title) != "" {
		owner.Title = workspace.Explorer.Title
	}
	stored, err := s.store.SaveDraft(ctx, *owner, request.ExpectedDraftVersion, request.ExpectedDraftDigest)
	if err != nil {
		return nil, err
	}
	return &authoringv2.ApplyCommandsResponse{CommandID: request.CommandID, Workspace: workspace, DraftVersion: stored.DraftVersion, DraftDigest: stored.DraftDigest, Results: results, Diagnostics: []any{}}, nil
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
	return s.store.FailRevision(ctx, id, diagnostics)
}

// CompilationReceiptForExplorer performs the tenant-scoped lookup used by
// execution and publication. The ID-only method above remains for legacy
// callers during the receipt migration.
func (s *Service) CompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceiptForExplorer(ctx, projectid.Canonical(project), explorerID, id)
}

func (s *Service) StoreCompilationReceipt(ctx context.Context, receipt CompilationReceipt) (*CompilationReceipt, error) {
	return s.store.InsertCompilationReceipt(ctx, receipt)
}

// PublishAuthoring atomically stores the receipt and immutable revision, then
// switches both the dataset release and Explorer active pointers.
func (s *Service) PublishAuthoring(ctx context.Context, receipt CompilationReceipt, revision Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*Revision, error) {
	if receipt.ID == "" || revision.ID == "" || revision.CompilationReceiptID != receipt.ID {
		return nil, fmt.Errorf("authoring publication identity is incomplete")
	}
	receipt.Project = projectid.Canonical(receipt.Project)
	revision.Project = projectid.Legacy(revision.Project)
	return s.store.PublishAuthoring(ctx, receipt, revision, release, expectedReleaseRevision)
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
		owner, err = s.store.Create(ctx, Explorer{Project: storageProject, ExplorerID: "default", Title: title, ManagementMode: ManagementRepository, DraftConfig: workspace, DraftVersion: 1, DraftDigest: receipt.IntentDigest, UpdatedBy: actor, UpdatedAt: s.now()})
	} else if err == nil {
		owner.Title, owner.DraftConfig, owner.DraftDigest, owner.UpdatedBy = title, workspace, receipt.IntentDigest, actor
		owner, err = s.store.SaveDraft(ctx, *owner, owner.DraftVersion)
	}
	if err != nil {
		return nil, nil, err
	}
	revisionID := RepositoryRevisionID(project, sourceCommit, receipt.IntentDigest, receipt.SourceGeneration, receipt.ID)
	now := s.now()
	revision, err := s.store.InsertRevision(ctx, Revision{ID: revisionID, Project: storageProject, ExplorerID: "default", Config: compiledConfig, AuthoringBundle: workspace, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: append(json.RawMessage(nil), receipt.PublicOutputContract...), Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Dataset: dataset, Publication: publication, Materializations: append([]Materialization(nil), materializations...), EmittedColumns: append([]EmittedColumn(nil), receipt.EmittedColumns...), Status: RevisionReady, CreatedBy: actor, CreatedAt: now, ReadyAt: &now})
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
