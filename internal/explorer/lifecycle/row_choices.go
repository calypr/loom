package lifecycle

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type RowDefinitionChoicesRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
}

type RowDefinitionChoicesResponse struct {
	SnapshotToken  string                        `json:"snapshotToken"`
	OutputID       string                        `json:"outputId"`
	Choices        []RowDefinitionChoice         `json:"choices"`
	ExplicitGroups []ExplicitGroupRevisionChoice `json:"explicitGroups"`
}

func (s *Service) ListRowDefinitionChoices(ctx context.Context, request RowDefinitionChoicesRequest) (RowDefinitionChoicesResponse, error) {
	result := RowDefinitionChoicesResponse{SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Choices: []RowDefinitionChoice{}, ExplicitGroups: []ExplicitGroupRevisionChoice{}}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" {
		return result, malformed("row-definition-choices", "project, explorerId, snapshotToken, and outputId are required", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.RowChoicePlanner == nil {
		return result, unavailable("row-definition-choices", "ROW_CHOICE_UNAVAILABLE", "authorized row-choice planning is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return result, conflict("row-definition-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before listing row choices", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("row-definition-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil {
		return result, unprocessable("row-definition-choices", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, authorized.Snapshot.Clone(), *document)
	if err != nil {
		return result, conflict("row-definition-choices", "STALE_ROUTE", "the saved route no longer matches the pinned capability snapshot", nil, err)
	}
	result = responseFromRowChoices(request.SnapshotToken, request.OutputID, choices)
	if s.config.ExplicitGroupResolver != nil {
		pinnedRevisionID := ""
		if document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == authoringv2.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil {
			pinnedRevisionID = document.Rows.Groups.Source.Explicit.RevisionID
		}
		result.ExplicitGroups, err = s.config.ExplicitGroupResolver.ListExplicitGroupRevisions(ctx, ExplicitGroupRevisionListRequest{
			Project: request.Project, Snapshot: authorized.Snapshot.Clone(), RootResourceType: document.RootResourceType,
			PinnedRevisionID: pinnedRevisionID,
		})
		if err != nil {
			return RowDefinitionChoicesResponse{}, unavailable("row-definition-choices", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group revisions are unavailable for this table", err)
		}
	}
	return result, nil
}

func responseFromRowChoices(snapshotToken, outputID string, choices []capability.RowChoice) RowDefinitionChoicesResponse {
	response := RowDefinitionChoicesResponse{SnapshotToken: snapshotToken, OutputID: outputID, Choices: make([]RowDefinitionChoice, 0, len(choices)), ExplicitGroups: []ExplicitGroupRevisionChoice{}}
	for _, choice := range choices {
		if choice.Kind != capability.RowChoiceExpandedScope {
			continue
		}
		response.Choices = append(response.Choices, RowDefinitionChoice{
			ChoiceID: choice.ChoiceID, OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path, Label: choice.Label, Description: choice.Description,
			FHIRType: choice.FHIRType, IsIdentifier: fhirIdentifierPath(choice.Path), IsReference: choice.Reference,
			OccurrenceSummary: rowChoiceOccurrenceSummary(choice), RouteSummary: rowChoiceRouteSummary(choice),
			Kind: lifecycleRowChoiceKind(choice.Kind), ValueType: choice.ValueType, Policies: rowChoicePolicies(choice.Kind),
		})
	}
	return response
}
