package rowdef

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) ListRowDefinitionChoices(ctx context.Context, request RowDefinitionChoicesRequest) (RowDefinitionChoicesResponse, error) {
	result := RowDefinitionChoicesResponse{SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Choices: []RowDefinitionChoice{}, ExplicitGroups: []explicitgroup.ExplicitGroupRevisionChoice{}}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" {
		return result, support.Malformed("row-definition-choices", "project, explorerId, snapshotToken, and outputId are required", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.RowChoicePlanner == nil {
		return result, support.Unavailable("row-definition-choices", "ROW_CHOICE_UNAVAILABLE", "authorized row-choice planning is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return result, support.Conflict("row-definition-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before listing row choices", nil, err)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, support.Conflict("row-definition-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil {
		return result, support.Unprocessable("row-definition-choices", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, authorized.Snapshot.Clone(), *document)
	if err != nil {
		return result, support.Conflict("row-definition-choices", "STALE_ROUTE", "the saved route no longer matches the pinned capability snapshot", nil, err)
	}
	result = responseFromRowChoices(request.SnapshotToken, request.OutputID, choices)
	if s.config.ExplicitGroupResolver != nil {
		pinnedRevisionID := ""
		if document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == authoringv2.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil {
			pinnedRevisionID = document.Rows.Groups.Source.Explicit.RevisionID
		}
		result.ExplicitGroups, err = s.config.ExplicitGroupResolver.ListExplicitGroupRevisions(ctx, explicitgroup.ExplicitGroupRevisionListRequest{
			Project: request.Project, Snapshot: authorized.Snapshot.Clone(), RootResourceType: document.RootResourceType,
			PinnedRevisionID: pinnedRevisionID,
		})
		if err != nil {
			return RowDefinitionChoicesResponse{}, support.Unavailable("row-definition-choices", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group revisions are unavailable for this table", err)
		}
	}
	return result, nil
}

func responseFromRowChoices(snapshotToken, outputID string, choices []capability.RowChoice) RowDefinitionChoicesResponse {
	response := RowDefinitionChoicesResponse{SnapshotToken: snapshotToken, OutputID: outputID, Choices: make([]RowDefinitionChoice, 0, len(choices)), ExplicitGroups: []explicitgroup.ExplicitGroupRevisionChoice{}}
	for _, choice := range choices {
		if choice.Kind != capability.RowChoiceExpandedScope {
			continue
		}
		response.Choices = append(response.Choices, RowDefinitionChoice{
			ChoiceID: choice.ChoiceID, OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path, Label: choice.Label, Description: choice.Description,
			FHIRType: choice.FHIRType, IsIdentifier: FHIRIdentifierPath(choice.Path), IsReference: choice.Reference,
			OccurrenceSummary: OccurrenceSummary(choice), RouteSummary: RouteSummary(choice),
			Kind: LifecycleRowChoiceKind(choice.Kind), ValueType: choice.ValueType, Policies: RowChoicePolicies(choice.Kind),
		})
	}
	return response
}

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	project = projectid.Canonical(project)
	owner, err := s.config.GetOwner(ctx, project, explorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return authoringv2.Workspace{}, support.NotFound("construction-choices", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return authoringv2.Workspace{}, support.PersistenceFailure("construction-choices", "the current Explorer workspace could not be loaded", err)
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return authoringv2.Workspace{}, support.Conflict("construction-choices", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.config.Store.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return authoringv2.Workspace{}, support.PersistenceFailure("construction-choices", "the active Explorer workspace could not be loaded", activeErr)
		}
		workspace, decodeErr := s.config.Store.RevisionWorkspace(ctx, *active)
		if decodeErr != nil {
			return authoringv2.Workspace{}, support.Conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	return authoringv2.Workspace{}, support.Conflict("construction-choices", "AUTHORING_STATE_MISSING", "the Explorer has no V2 authoring workspace", nil, nil)
}

func findSemanticOutput(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}
