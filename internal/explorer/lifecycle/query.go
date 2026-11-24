package lifecycle

import (
	"context"
	"errors"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) List(ctx context.Context, project string) (ListResult, error) {
	project = projectid.Canonical(project)
	values, err := s.store.List(ctx, project)
	if err != nil {
		return ListResult{}, support.PersistenceFailure("list", "Explorer list could not be loaded", err)
	}
	result := ListResult{Project: project, Summaries: make([]viewer.ExplorerSummaryV1, 0, len(values))}
	for _, value := range values {
		result.Summaries = append(result.Summaries, viewer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, ActiveRevisionID: value.ActiveRevisionID, UpdatedAt: value.UpdatedAt})
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, project, id string) (viewer.ExplorerStateV1, error) {
	state, err := s.store.LoadExplorerState(ctx, projectid.Canonical(project), strings.TrimSpace(id))
	if errors.Is(err, explorer.ErrNotFound) {
		return viewer.ExplorerStateV1{}, support.NotFound("get", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if errors.Is(err, explorer.ErrViewerProjectionIntegrity) {
		return viewer.ExplorerStateV1{}, support.Internal("get", "EXPLORER_INTEGRITY_FAILURE", "active Explorer revision contains an invalid immutable projection", err)
	}
	if err != nil {
		return viewer.ExplorerStateV1{}, support.PersistenceFailure("get", "Explorer state could not be loaded", err)
	}
	return state, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (viewer.ExplorerSummaryV1, error) {
	project := projectid.Canonical(request.Project)
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return viewer.ExplorerSummaryV1{}, support.Malformed("create", "name is required", nil)
	}
	id := explorer.StableExplorerID(name)
	if id == "default" || id == DefaultDataframeID {
		return viewer.ExplorerSummaryV1{}, support.Conflict("create", "EXPLORER_EXISTS", "the repository default already exists", nil, nil)
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = name
	}
	var value *explorer.Explorer
	var err error
	if strings.TrimSpace(request.SourceExplorerID) == DefaultDataframeID {
		source, sourceErr := s.core.GetOwner(ctx, project, DefaultDataframeID)
		if sourceErr != nil {
			return viewer.ExplorerSummaryV1{}, sourceErr
		}
		workspace, decodeErr := ws.DecodeWorkspace(source.DraftConfig)
		if decodeErr != nil {
			return viewer.ExplorerSummaryV1{}, support.Conflict("create", "DRAFT_STATE_INVALID", "the source dataframe draft is invalid", nil, decodeErr)
		}
		workspace.Explorer.Title = title
		value, err = s.store.CreateSeededDataframe(ctx, project, id, title, workspace, request.Actor, authoringv2.DraftSourceContext{SnapshotToken: source.DraftSnapshotToken, SourceGeneration: source.DraftSourceGeneration, AuthorizationScopeDigest: source.DraftAuthorizationScopeDigest})
	} else {
		value, err = s.store.CreateInteractiveFrom(ctx, project, id, title, strings.TrimSpace(request.SourceExplorerID), request.Actor)
	}
	if errors.Is(err, explorer.ErrDraftConflict) {
		return viewer.ExplorerSummaryV1{}, support.Conflict("create", "EXPLORER_EXISTS", "an Explorer with this name already exists", nil, err)
	}
	if err != nil {
		return viewer.ExplorerSummaryV1{}, support.PersistenceFailure("create", "the Explorer could not be created", err)
	}
	return viewer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, UpdatedAt: value.UpdatedAt}, nil
}

func (s *Service) Capability(ctx context.Context, project, explorerID, generation string) (capability.Snapshot, ws.CatalogSnapshot, error) {
	if s.config.Capability.Current == nil || s.config.Capability.Catalog == nil {
		return capability.Snapshot{}, ws.CatalogSnapshot{}, support.Unavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability compiler is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, project, explorerID, generation)
	if err != nil || !snapshot.Usable() {
		if err == nil {
			err = capability.ErrSnapshotUnavailable
		}
		return capability.Snapshot{}, ws.CatalogSnapshot{}, support.Unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return snapshot, s.config.Capability.Catalog(snapshot, explorerID), nil
}

func (s *Service) Suggestions(ctx context.Context, request SuggestionsRequest) (SuggestionsResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeID) == "" {
		return SuggestionsResult{}, support.Malformed("suggestions", "snapshotToken and nodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return SuggestionsResult{}, support.Unavailable("suggestions", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return SuggestionsResult{}, support.Conflict("suggestions", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	query := strings.ToLower(strings.TrimSpace(request.Query))
	result := SuggestionsResult{SnapshotToken: request.SnapshotToken, NodeID: request.NodeID, Candidates: make([]ws.CatalogCandidate, 0)}
	for _, candidate := range catalog.Candidates {
		if candidate.NodeID == request.NodeID && (query == "" || strings.Contains(strings.ToLower(candidate.Label), query) || strings.Contains(strings.ToLower(candidate.ID), query)) {
			result.Candidates = append(result.Candidates, candidate)
		}
	}
	return result, nil
}

func (s *Service) Builder(ctx context.Context, request BuilderRequest) (ws.BuilderState, error) {
	snapshot, catalog, err := s.Capability(ctx, request.Project, request.ExplorerID, "")
	if err != nil {
		return ws.BuilderState{}, err
	}
	owner, err := s.core.GetOwnerWithSnapshot(ctx, request.Project, request.ExplorerID, snapshot, catalog)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) {
			return ws.BuilderState{}, support.NotFound("builder", "EXPLORER_NOT_FOUND", "Explorer not found", err)
		}
		return ws.BuilderState{}, err
	}
	state := ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, LifecycleState: ws.LifecycleNew, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, PreviousDraftRevisionID: owner.PreviousDraftRevisionID, Catalog: catalog}
	if request.ExplorerID != DefaultDataframeID && owner.ActiveRevisionID != "" && len(owner.DraftConfig) != 0 {
		active, activeErr := s.store.ActiveRevision(ctx, request.Project, request.ExplorerID)
		if activeErr != nil {
			return ws.BuilderState{}, support.PersistenceFailure("builder", "the active Explorer revision could not be loaded as V2 authoring state", activeErr)
		}
		if _, decodeErr := s.store.RevisionWorkspace(ctx, *active); decodeErr != nil {
			return ws.BuilderState{}, support.Conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", nil, decodeErr)
		}
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := ws.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return ws.BuilderState{}, support.Conflict("builder", "DRAFT_STATE_INVALID", "the saved Explorer draft is not a valid V2 workspace", map[string]any{"draftVersion": owner.DraftVersion}, decodeErr)
		}

		state.DraftVersion = owner.DraftVersion
		state.DraftDigest = owner.DraftDigest
		state.PreviousDraftRevisionID = owner.PreviousDraftRevisionID
		state.Workspace, state.LifecycleState = &workspace, ws.LifecycleReady
	} else if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, request.Project, request.ExplorerID)
		if activeErr != nil {
			return ws.BuilderState{}, support.PersistenceFailure("builder", "the active Explorer revision could not be loaded as V2 authoring state", activeErr)
		}
		workspace, decodeErr := s.store.RevisionWorkspace(ctx, *active)
		if decodeErr != nil {
			return ws.BuilderState{}, support.Conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", map[string]any{"revisionId": active.ID}, decodeErr)
		}
		state.Workspace, state.LifecycleState = &workspace, ws.LifecycleReady
	}
	if err := state.Validate(); err != nil {
		return ws.BuilderState{}, support.Unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return state, nil
}
