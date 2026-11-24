package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type Service struct {
	owners                ExplorerOwnerStore
	drafts                DraftStore
	revisions             RevisionReader
	revisionWriter        RevisionWriter
	receipts              CompilationReceiptStore
	authoringPublisher    AuthoringPublisher
	repositoryActivator   RepositoryGenerationActivator
	selectionStore        SelectionStore
	viewBuilderRepository ViewBuilderRepository
	draftRevisionReader   DraftRevisionReader
	workspaceProjections  ws.ProjectionReader
	now                   func() time.Time
}

type ServiceDependencies struct {
	Owners               ExplorerOwnerStore
	Drafts               DraftStore
	Revisions            RevisionReader
	RevisionWriter       RevisionWriter
	Receipts             CompilationReceiptStore
	AuthoringPublisher   AuthoringPublisher
	RepositoryActivator  RepositoryGenerationActivator
	Selections           SelectionStore
	ViewBuilder          ViewBuilderRepository
	DraftRevisions       DraftRevisionReader
	WorkspaceProjections ws.ProjectionReader
}

func NewService(dependencies ServiceDependencies) (*Service, error) {
	if dependencies.Owners == nil || dependencies.Drafts == nil {
		return nil, fmt.Errorf("Explorer owner and draft stores are required")
	}
	if dependencies.Revisions == nil || dependencies.Selections == nil {
		return nil, fmt.Errorf("Explorer revision and selection stores are required")
	}
	return &Service{
		owners: dependencies.Owners, drafts: dependencies.Drafts,
		revisions: dependencies.Revisions, revisionWriter: dependencies.RevisionWriter,
		receipts: dependencies.Receipts, authoringPublisher: dependencies.AuthoringPublisher,
		repositoryActivator: dependencies.RepositoryActivator, selectionStore: dependencies.Selections,
		viewBuilderRepository: dependencies.ViewBuilder, draftRevisionReader: dependencies.DraftRevisions,
		workspaceProjections: dependencies.WorkspaceProjections,
		now:                  func() time.Time { return time.Now().UTC() },
	}, nil
}
func (s *Service) ViewBuilderStorage() ViewBuilderRepository {
	if s == nil {
		return nil
	}
	return s.viewBuilderRepository
}

func (s *Service) RevisionReader() RevisionReader         { return s.revisions }
func (s *Service) RevisionWriter() RevisionWriter         { return s.revisionWriter }
func (s *Service) ReceiptStore() CompilationReceiptStore  { return s.receipts }
func (s *Service) AuthoringPublisher() AuthoringPublisher { return s.authoringPublisher }
func (s *Service) RepositoryActivator() RepositoryGenerationActivator {
	return s.repositoryActivator
}
func (s *Service) SelectionStorage() SelectionStore { return s.selectionStore }
func (s *Service) DraftRevisionStorage() DraftRevisionReader {
	return s.draftRevisionReader
}

func (s *Service) List(ctx context.Context, project string) ([]Explorer, error) {
	return s.owners.List(ctx, projectid.Legacy(project))
}
func (s *Service) Get(ctx context.Context, project, id string) (*Explorer, error) {
	return s.owners.Get(ctx, projectid.Legacy(project), id)
}

func (s *Service) LoadExplorerState(ctx context.Context, project, id string) (viewer.ExplorerStateV1, error) {
	project = projectid.Canonical(project)
	id = strings.TrimSpace(id)
	owner, err := s.Get(ctx, project, id)
	if id == "default" && errors.Is(err, ErrNotFound) {
		if repository := s.viewBuilderRepository; repository != nil {
			settings, settingsErr := repository.GetViewBuilderSettings(ctx, projectid.Legacy(project))
			if settingsErr != nil && !errors.Is(settingsErr, ErrNotFound) {
				return viewer.ExplorerStateV1{}, settingsErr
			}
			if settingsErr == nil && settings.DefaultConfigID != "" && settings.DefaultConfigID != id {
				owner, err = s.Get(ctx, project, settings.DefaultConfigID)
			}
		}
	}
	if err != nil {
		return viewer.ExplorerStateV1{}, err
	}
	if owner == nil {
		return viewer.ExplorerStateV1{}, fmt.Errorf("Explorer %s/%s resolved to an empty identity", project, id)
	}

	state := newExplorerStateResponse(owner)
	if owner.ActiveRevisionID == "" {
		markNotPublished(&state)
		return state, nil
	}
	revision, err := s.revisions.GetRevision(ctx, owner.ActiveRevisionID)
	if err != nil {
		return viewer.ExplorerStateV1{}, fmt.Errorf("load active Explorer revision %q: %w", owner.ActiveRevisionID, err)
	}
	state.Active.RevisionID = revision.ID
	state.Active.IntentDigest = revision.IntentDigest
	state.Active.Status = string(revision.Status)
	state.Active.Bundle = append([]byte(nil), revision.AuthoringBundle...)
	state.Generated.RecipeDigest = revision.RecipeDigest
	state.Generated.ResolvedSchemaDigest = revision.ResolvedSchemaDigest
	state.Generated.SourceGeneration = revision.SourceGeneration
	state.Generated.EmittedColumns = append([]viewer.EmittedColumn(nil), revision.EmittedColumns...)
	state.Generated.Materializations = append([]viewer.Materialization(nil), revision.Materializations...)
	state.Generated.Dataset = revision.Dataset
	state.Generated.Dataset.Outputs = append([]viewer.DatasetOutput(nil), revision.Dataset.Outputs...)
	state.Generated.Publication = revisionPublication(revision)
	state.Generated.QualityReports = publication.CloneQualityReports(revision.QualityReports)
	state.Generated.Publication.RevisionID = revision.ID
	state.Generated.Diagnostics = append([]viewer.Diagnostic(nil), revision.Diagnostics...)
	state.Runtime, err = buildViewerProjection(revision, owner.ProjectConfig)
	if err != nil {
		return viewer.ExplorerStateV1{}, fmt.Errorf("active Explorer revision projection is invalid: %w", err)
	}
	if state.Runtime == nil || len(state.Runtime.Outputs) == 0 {
		state.Generated.Publication.State = viewer.ExplorerRuntimeV1NotPublished
		if state.Runtime != nil {
			state.Runtime.Status = viewer.ExplorerRuntimeV1NotPublished
			state.Runtime.Publication.State = viewer.ExplorerRuntimeV1NotPublished
		}
	}
	return state, nil
}

func newExplorerStateResponse(owner *Explorer) viewer.ExplorerStateV1 {
	project := projectid.Canonical(owner.Project)
	state := viewer.ExplorerStateV1{
		APIVersion: viewer.ExplorerStateV1APIVersion,
		Kind:       viewer.ExplorerStateV1Kind,
		Project:    project,
		ExplorerID: owner.ExplorerID,
		Title:      owner.Title,
		Management: owner.ManagementMode,
		Draft: viewer.ExplorerStateV1Draft{
			Bundle:  append([]byte(nil), owner.DraftConfig...),
			Version: owner.DraftVersion,
			Digest:  owner.DraftDigest,
		},
		Generated: viewer.ExplorerStateV1Generated{},
		ActiveURL: explorerURL(project, owner.ExplorerID),
		UpdatedBy: owner.UpdatedBy,
		UpdatedAt: owner.UpdatedAt,
	}
	return state
}

func markNotPublished(state *viewer.ExplorerStateV1) {
	state.Generated.Publication.State = viewer.ExplorerRuntimeV1NotPublished
	state.Runtime = nil
}

func explorerURL(project, id string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(id)
}

func (s *Service) createEmptyInteractive(ctx context.Context, project, id, title, actor string) (*Explorer, error) {
	if id == "" || id == "default" {
		return nil, fmt.Errorf("invalid interactive Explorer identity")
	}
	return s.owners.Create(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: viewer.ManagementInteractive, UpdatedBy: actor, UpdatedAt: s.now()})
}

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
		revision, revisionErr := s.revisions.GetRevision(ctx, source.ActiveRevisionID)
		if revisionErr != nil {
			return nil, revisionErr
		}
		projected, projectionErr := s.RevisionWorkspace(ctx, *revision)
		if projectionErr != nil {
			return nil, projectionErr
		}
		raw, err = projected.CanonicalJSON()
		if err != nil {
			return nil, err
		}
	}
	if len(raw) == 0 {
		return s.createEmptyInteractive(ctx, project, id, title, actor)
	}
	workspace, err := ws.DecodeWorkspace(raw)
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
	return s.owners.Create(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: viewer.ManagementInteractive, DraftConfig: canonical, DraftVersion: 1, DraftDigest: digest, UpdatedBy: actor, UpdatedAt: s.now()})
}

func (s *Service) CreateSeededDataframe(ctx context.Context, project, id, title string, workspace ws.Workspace, actor string, sourceContext authoringv2.DraftSourceContext) (*Explorer, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("seeded dataframe identity and title are required")
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("encode seeded dataframe workspace: %w", err)
	}
	workspace, err = ws.DecodeWorkspace(canonical)
	if err != nil {
		return nil, fmt.Errorf("decode seeded dataframe workspace: %w", err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest seeded dataframe workspace: %w", err)
	}
	return s.owners.Create(ctx, Explorer{
		Project: projectid.Legacy(project), ExplorerID: id, Title: title,
		ManagementMode: viewer.ManagementInteractive, DraftConfig: canonical,
		DraftVersion: 1, DraftDigest: digest, DraftSnapshotToken: sourceContext.SnapshotToken, DraftSourceGeneration: sourceContext.SourceGeneration, DraftAuthorizationScopeDigest: sourceContext.AuthorizationScopeDigest, UpdatedBy: actor, UpdatedAt: s.now(),
	})
}

func (s *Service) ApplyWorkspaceCommands(ctx context.Context, project, id string, catalog ws.CatalogSnapshot, request authoringv2.ApplyCommandsRequest, actor string) (*authoringv2.ApplyCommandsResponse, error) {
	return s.applyWorkspaceCommands(ctx, project, id, catalog, request, actor, nil, nil)
}

func (s *Service) ApplyWorkspaceCommandsChecked(ctx context.Context, project, id string, catalog ws.CatalogSnapshot, request authoringv2.ApplyCommandsRequest, actor string, prepare func(context.Context, ws.Workspace, []authoringv2.Command) ([]authoringv2.Command, error), checker func(ws.Workspace) error) (*authoringv2.ApplyCommandsResponse, error) {
	return s.applyWorkspaceCommands(ctx, project, id, catalog, request, actor, prepare, checker)
}

func (s *Service) applyWorkspaceCommands(ctx context.Context, project, id string, catalog ws.CatalogSnapshot, request authoringv2.ApplyCommandsRequest, actor string, prepare func(context.Context, ws.Workspace, []authoringv2.Command) ([]authoringv2.Command, error), checker func(ws.Workspace) error) (*authoringv2.ApplyCommandsResponse, error) {
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
		workspace, decodeErr := ws.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode replayed authoring workspace: %w", decodeErr)
		}
		results := []authoringv2.CommandResult{}
		if len(owner.LastAuthoringCommandResults) != 0 {
			if decodeErr := json.Unmarshal(owner.LastAuthoringCommandResults, &results); decodeErr != nil {
				return nil, fmt.Errorf("decode replayed authoring command results: %w", decodeErr)
			}
		}
		return &authoringv2.ApplyCommandsResponse{CommandID: request.CommandID, Workspace: workspace, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, PreviousDraftRevisionID: owner.PreviousDraftRevisionID, Results: results, Diagnostics: []any{}}, nil
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || (request.ExpectedDraftDigest != "" && owner.DraftDigest != request.ExpectedDraftDigest) {
		return nil, ErrDraftConflict
	}

	workspace := ws.NewWorkspace(owner.Title)
	if len(owner.DraftConfig) != 0 {
		workspace, err = ws.DecodeWorkspace(owner.DraftConfig)
	} else if owner.ActiveRevisionID != "" {
		var revision *Revision
		revision, err = s.revisions.GetRevision(ctx, owner.ActiveRevisionID)
		if err == nil {
			workspace, err = s.RevisionWorkspace(ctx, *revision)
		}
	}
	if err != nil {
		return nil, err
	}
	commands := cloneAuthoringCommands(request.Commands)
	if prepare != nil {
		commands, err = prepare(ctx, workspace, commands)
		if err != nil {
			return nil, err
		}
		if len(commands) != len(request.Commands) {
			return nil, fmt.Errorf("prepared authoring command count changed")
		}
		for index := range commands {
			if commands[index].Type != request.Commands[index].Type {
				return nil, fmt.Errorf("prepared authoring command type changed at index %d", index)
			}
		}
	}
	workspace, results, err := authoringv2.ApplyCommands(workspace, catalog, request.CommandID, commands)
	if err != nil {
		return nil, err
	}
	if checker != nil {
		if err := checker(workspace); err != nil {
			return nil, err
		}
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
	if sourceContext := request.ResolvedDraftSourceContext(); sourceContext != nil {
		owner.DraftSnapshotToken = sourceContext.SnapshotToken
		owner.DraftSourceGeneration = sourceContext.SourceGeneration
		owner.DraftAuthorizationScopeDigest = sourceContext.AuthorizationScopeDigest
	}
	owner.UpdatedBy = actor
	owner.UpdatedAt = s.now()
	if strings.TrimSpace(workspace.Explorer.Title) != "" {
		owner.Title = workspace.Explorer.Title
	}
	stored, err := s.drafts.SaveDraft(ctx, *owner, request.ExpectedDraftVersion, request.ExpectedDraftDigest)
	if err != nil {
		return nil, err
	}
	return &authoringv2.ApplyCommandsResponse{CommandID: request.CommandID, Workspace: workspace, DraftVersion: stored.DraftVersion, DraftDigest: stored.DraftDigest, PreviousDraftRevisionID: stored.PreviousDraftRevisionID, Results: results, Diagnostics: []any{}}, nil
}

func cloneAuthoringCommands(commands []authoringv2.Command) []authoringv2.Command {
	cloned := make([]authoringv2.Command, len(commands))
	copy(cloned, commands)
	for i := range cloned {
		if payload, ok := cloned[i].Payload.(*authoringv2.SemanticPayload); ok {
			copied := *payload
			copied.SemanticSelections = append([]authoringv2.SemanticSelection(nil), commands[i].SemanticSelections()...)
			cloned[i].Payload = &copied
		}
	}
	return cloned
}

func (s *Service) ActiveRevision(ctx context.Context, project, id string) (*Revision, error) {
	e, err := s.owners.Get(ctx, projectid.Legacy(project), id)
	if err != nil {
		return nil, err
	}
	if e.ActiveRevisionID == "" {
		return nil, ErrNotFound
	}
	return s.revisions.GetRevision(ctx, e.ActiveRevisionID)
}
func (s *Service) FailRevision(ctx context.Context, id string, diagnostics []viewer.Diagnostic) (*Revision, error) {
	return s.revisionWriter.FailRevision(ctx, id, diagnostics)
}

func (s *Service) CompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*receiptmodel.CompilationReceipt, error) {
	return s.receipts.GetCompilationReceiptForExplorer(ctx, projectid.Canonical(project), explorerID, id)
}

func (s *Service) StoreCompilationReceipt(ctx context.Context, receipt receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	return s.receipts.InsertCompilationReceipt(ctx, receipt)
}

func (s *Service) BeginSelection(ctx context.Context, selection explorerselection.SelectionRevision, writerToken string) (*explorerselection.SelectionRevision, error) {
	return s.selectionStore.BeginSelection(ctx, selection, writerToken)
}

func (s *Service) AppendSelectionMembers(ctx context.Context, selectionID, writerToken string, members []explorerselection.SelectionMember) ([]explorerselection.SelectionMember, error) {
	return s.selectionStore.AppendSelectionMembers(ctx, selectionID, writerToken, members)
}

func (s *Service) DigestSelectionMembers(ctx context.Context, project, selectionID string) (string, int64, int64, error) {
	return s.selectionStore.DigestSelectionMembers(ctx, projectid.Canonical(project), selectionID)
}

func (s *Service) CompleteSelection(ctx context.Context, selectionID, writerToken, digest string, count, bytes int64, completedAt time.Time) (*explorerselection.SelectionRevision, error) {
	return s.selectionStore.CompleteSelection(ctx, selectionID, writerToken, digest, count, bytes, completedAt)
}

func (s *Service) AbortSelection(ctx context.Context, selectionID, writerToken string) error {
	return s.selectionStore.AbortSelection(ctx, selectionID, writerToken)
}

func (s *Service) CleanupSelectionStaging(ctx context.Context, before time.Time, limit int) error {
	return s.selectionStore.CleanupSelectionStaging(ctx, before, limit)
}

func (s *Service) GetSelection(ctx context.Context, project, selectionID string) (*explorerselection.SelectionRevision, error) {
	return s.selectionStore.GetSelection(ctx, projectid.Canonical(project), selectionID)
}

func (s *Service) GetRevision(ctx context.Context, revisionID string) (*Revision, error) {
	return s.revisions.GetRevision(ctx, strings.TrimSpace(revisionID))
}

func (s *Service) GetDraftRevision(ctx context.Context, project, explorerID, revisionID string) (*DraftRevision, error) {
	if s.draftRevisionReader == nil {
		return nil, ErrNotFound
	}
	return s.draftRevisionReader.GetDraftRevision(ctx, projectid.Legacy(project), strings.TrimSpace(explorerID), strings.TrimSpace(revisionID))
}

func (s *Service) VisitSelectionMembers(ctx context.Context, project, selectionID, after string, pageSize int, visit func(explorerselection.SelectionMember) error) (string, error) {
	return s.selectionStore.VisitSelectionMembers(ctx, projectid.Canonical(project), selectionID, after, pageSize, visit)
}

func (s *Service) PublishAuthoring(ctx context.Context, receipt receiptmodel.CompilationReceipt, revision Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*Revision, error) {
	if receipt.ID == "" || revision.ID == "" || revision.CompilationReceiptID != receipt.ID {
		return nil, fmt.Errorf("authoring publication identity is incomplete")
	}
	receipt.Project = projectid.Canonical(receipt.Project)
	revision.Project = projectid.Legacy(revision.Project)
	return s.authoringPublisher.PublishAuthoring(ctx, receipt, revision, release, expectedReleaseRevision)
}

func (s *Service) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.repositoryActivator.ActivateRepositoryGeneration(ctx, projectid.Legacy(project), generation, revisionID)
}

func (s *Service) RevisionWorkspace(ctx context.Context, revision Revision) (ws.Workspace, error) {
	workspace, _, err := ws.ResolveStoredArtifact(ctx, s.workspaceProjections, RevisionWorkspaceArtifact(revision), revision.AuthoringWorkspaceBytes())
	return workspace, err
}

func (s *Service) DraftRevisionWorkspace(ctx context.Context, revision DraftRevision) (ws.Workspace, string, error) {
	return ws.ResolveStoredArtifact(ctx, s.workspaceProjections, DraftWorkspaceArtifact(revision), revision.DraftConfig)
}
