package lifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) List(ctx context.Context, project string) (ListResult, error) {
	project = projectid.Canonical(project)
	values, err := s.store.List(ctx, project)
	if err != nil {
		return ListResult{}, internal("list", "EXPLORER_READ_FAILED", err.Error(), err)
	}
	result := ListResult{Project: project, Summaries: make([]explorer.ExplorerSummaryV1, 0, len(values))}
	for _, value := range values {
		result.Summaries = append(result.Summaries, explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, ActiveRevisionID: value.ActiveRevisionID, UpdatedAt: value.UpdatedAt})
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, project, id string) (explorer.ExplorerStateV1, error) {
	state, err := s.store.LoadExplorerState(ctx, projectid.Canonical(project), strings.TrimSpace(id))
	if errors.Is(err, explorer.ErrNotFound) {
		return explorer.ExplorerStateV1{}, notFound("get", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return explorer.ExplorerStateV1{}, internal("get", "EXPLORER_READ_FAILED", err.Error(), err)
	}
	return state, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (explorer.ExplorerSummaryV1, error) {
	project := projectid.Canonical(request.Project)
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return explorer.ExplorerSummaryV1{}, malformed("create", "name is required", nil)
	}
	id := explorer.StableExplorerID(name)
	if id == "default" {
		return explorer.ExplorerSummaryV1{}, conflict("create", "EXPLORER_EXISTS", "the repository default already exists", nil, nil)
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = name
	}
	value, err := s.store.CreateInteractiveFrom(ctx, project, id, title, strings.TrimSpace(request.SourceExplorerID), request.Actor)
	if errors.Is(err, explorer.ErrDraftConflict) {
		return explorer.ExplorerSummaryV1{}, conflict("create", "EXPLORER_EXISTS", "an Explorer with this name already exists", nil, err)
	}
	if err != nil {
		return explorer.ExplorerSummaryV1{}, unprocessable("create", "INVALID_EXPLORER", err.Error(), err)
	}
	return explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, UpdatedAt: value.UpdatedAt}, nil
}

func (s *Service) Capability(ctx context.Context, project, explorerID, generation string) (capability.Snapshot, authoringv2.CatalogSnapshot, error) {
	if s.config.Capability.Current == nil || s.config.Capability.Catalog == nil {
		return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability compiler is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, project, explorerID, generation)
	if err != nil || !snapshot.Usable() {
		if err == nil {
			err = capability.ErrSnapshotUnavailable
		}
		return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return snapshot, s.config.Capability.Catalog(snapshot, explorerID), nil
}

func (s *Service) Suggestions(ctx context.Context, request SuggestionsRequest) (SuggestionsResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeID) == "" {
		return SuggestionsResult{}, malformed("suggestions", "snapshotToken and nodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return SuggestionsResult{}, unavailable("suggestions", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return SuggestionsResult{}, conflict("suggestions", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	query := strings.ToLower(strings.TrimSpace(request.Query))
	result := SuggestionsResult{SnapshotToken: request.SnapshotToken, NodeID: request.NodeID, Candidates: make([]authoringv2.CatalogCandidate, 0)}
	for _, candidate := range catalog.Candidates {
		if candidate.NodeID == request.NodeID && (query == "" || strings.Contains(strings.ToLower(candidate.Label), query) || strings.Contains(strings.ToLower(candidate.ID), query)) {
			result.Candidates = append(result.Candidates, candidate)
		}
	}
	return result, nil
}

func (s *Service) Builder(ctx context.Context, request BuilderRequest) (authoringv2.BuilderState, error) {
	_, catalog, err := s.Capability(ctx, request.Project, request.ExplorerID, "")
	if err != nil {
		return authoringv2.BuilderState{}, err
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) {
			return authoringv2.BuilderState{}, notFound("builder", "EXPLORER_NOT_FOUND", "Explorer not found", err)
		}
		return authoringv2.BuilderState{}, err
	}
	state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, LifecycleState: authoringv2.LifecycleNew, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Catalog: catalog}
	var activeWorkspace *authoringv2.Workspace
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, request.Project, request.ExplorerID)
		if activeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision cannot be loaded as V2 authoring state", map[string]any{"revisionId": owner.ActiveRevisionID}, activeErr)
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
		if decodeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", map[string]any{"revisionId": active.ID}, decodeErr)
		}
		activeWorkspace = &workspace
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "DRAFT_STATE_INVALID", "the saved Explorer draft is not a valid V2 workspace", map[string]any{"draftVersion": owner.DraftVersion}, decodeErr)
		}
		state.Workspace, state.LifecycleState = &workspace, authoringv2.LifecycleReady
	} else if activeWorkspace != nil {
		state.Workspace, state.LifecycleState = activeWorkspace, authoringv2.LifecycleReady
	}
	if err := state.Validate(); err != nil {
		return authoringv2.BuilderState{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return state, nil
}
