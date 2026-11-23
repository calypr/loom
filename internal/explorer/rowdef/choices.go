package rowdef

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type RowChoiceKind string

const (
	RowChoiceFieldGroup RowChoiceKind = "FIELD_GROUP"
	RowChoiceExpanded   RowChoiceKind = "EXPANDED"
)

type RowChoiceResolveRequest struct {
	Project      string
	ExplorerID   string
	OutputID     string
	Snapshot     capability.Snapshot
	Route        ws.RouteNode
	RowChoiceID  string
	ExpectedKind RowChoiceKind
}

type ResolvedRowChoice struct {
	Kind         RowChoiceKind
	OccurrenceID string
	FieldPath    string
	ScopePath    string
}

type RowChoiceResolver interface {
	ResolveRowChoiceID(context.Context, RowChoiceResolveRequest) (ResolvedRowChoice, error)
}

type RowChoicePlanner interface {
	ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error)
}

type RowDefinitionChoice struct {
	ChoiceID          string                      `json:"choiceId"`
	OccurrenceID      string                      `json:"occurrenceId,omitempty"`
	FieldPath         string                      `json:"fieldPath"`
	Label             string                      `json:"label"`
	Description       string                      `json:"description"`
	FHIRType          string                      `json:"fhirType"`
	IsIdentifier      bool                        `json:"isIdentifier"`
	IsReference       bool                        `json:"isReference"`
	OccurrenceSummary string                      `json:"occurrenceSummary"`
	RouteSummary      string                      `json:"routeSummary"`
	Kind              RowChoiceKind               `json:"kind"`
	ValueType         string                      `json:"valueType"`
	Policies          []RowDefinitionChoicePolicy `json:"policies"`
}

type RowDefinitionChoicePolicy struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
}

type SchemaRowChoiceResolver struct {
	index *fhirschema.Index
}

func NewSchemaRowChoiceResolver(index *fhirschema.Index) (*SchemaRowChoiceResolver, error) {
	if index == nil {
		return nil, fmt.Errorf("generated schema index is required")
	}
	return &SchemaRowChoiceResolver{index: index}, nil
}

func (r *SchemaRowChoiceResolver) ListRowChoices(ctx context.Context, snapshot capability.Snapshot, document ws.Document) ([]capability.RowChoice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return nil, err
	}
	if document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return nil, fmt.Errorf("saved output route has no exact row root")
	}
	var choices []capability.RowChoice
	seen := make(map[string]struct{})
	var visit func(ws.RouteNode) error
	visit = func(node ws.RouteNode) error {
		occurrence, err := ResolveRowChoiceOccurrence(snapshot, document.RootResourceType, document.Route, node.OccurrenceID, "")
		if err != nil {
			return err
		}
		for _, candidate := range snapshot.Candidates {
			if candidate.NodeID != occurrence.NodeID || candidate.ResourceType != occurrence.ResourceType {
				continue
			}
			paths := []struct {
				path     string
				boundary bool
			}{{path: candidate.FieldPath}}
			for _, boundary := range candidate.RepeatedBoundaries {
				paths = append(paths, struct {
					path     string
					boundary bool
				}{path: boundary.Path, boundary: true})
			}
			for _, candidatePath := range paths {
				facts, err := r.resolveFacts(candidate.ResourceType, candidatePath.path)
				if err != nil {
					continue
				}
				for _, kind := range ApplicableRowChoiceKinds(facts) {
					if candidatePath.boundary && kind != capability.RowChoiceExpandedScope {
						continue
					}
					choice, err := capability.NewRowChoice(snapshot, []capability.RowChoiceOccurrence{occurrence}, occurrence.OccurrenceID, kind, facts)
					if err != nil {
						continue
					}
					if _, ok := seen[choice.ChoiceID]; ok {
						continue
					}
					seen[choice.ChoiceID] = struct{}{}
					choices = append(choices, choice)
				}
			}
		}
		for _, child := range node.Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(document.Route); err != nil {
		return nil, err
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].OccurrenceID != choices[j].OccurrenceID {
			return choices[i].OccurrenceID < choices[j].OccurrenceID
		}
		if choices[i].Kind != choices[j].Kind {
			return choices[i].Kind < choices[j].Kind
		}
		return choices[i].Label < choices[j].Label
	})
	return choices, nil
}

func (r *SchemaRowChoiceResolver) ResolveRowChoiceID(ctx context.Context, request RowChoiceResolveRequest) (ResolvedRowChoice, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedRowChoice{}, err
	}
	identity, err := capability.DecodeRowChoiceID(request.RowChoiceID)
	if err != nil {
		return ResolvedRowChoice{}, err
	}
	expectedKind, err := CapabilityRowChoiceKind(request.ExpectedKind)
	if err != nil || identity.Kind != expectedKind {
		return ResolvedRowChoice{}, fmt.Errorf("row choice kind does not match the requested selection")
	}
	occurrence, err := ResolveRowChoiceOccurrence(request.Snapshot, request.Route.ResourceType, request.Route, identity.Occurrence.OccurrenceID, identity.Occurrence.NodeID)
	if err != nil {
		return ResolvedRowChoice{}, err
	}
	if !SnapshotContainsRowCandidate(request.Snapshot, occurrence, identity.Path, identity.Kind) {
		return ResolvedRowChoice{}, fmt.Errorf("row choice path is no longer a candidate for the saved occurrence")
	}
	choice, err := capability.ResolveRowChoiceID(request.Snapshot, []capability.RowChoiceOccurrence{occurrence}, request.RowChoiceID, r.resolveFacts)
	if err != nil {
		return ResolvedRowChoice{}, err
	}
	resolved := ResolvedRowChoice{Kind: request.ExpectedKind, OccurrenceID: choice.OccurrenceID}
	switch choice.Kind {
	case capability.RowChoiceFieldGroupKey:
		resolved.FieldPath = choice.Path
	case capability.RowChoiceExpandedScope:
		resolved.ScopePath = choice.Path
	default:
		return ResolvedRowChoice{}, fmt.Errorf("row choice kind is unsupported")
	}
	return resolved, nil
}

func (r *SchemaRowChoiceResolver) resolveFacts(resourceType, path string) (capability.RowChoiceFacts, error) {
	facts, err := r.index.ResolveRowPath(fhirschema.DefinitionName(resourceType), path)
	if err != nil {
		return capability.RowChoiceFacts{}, err
	}
	return capability.RowChoiceFacts{
		ResourceType: string(facts.ResourceType), CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: capability.RowChoiceCardinality(facts.Cardinality), Shape: capability.RowChoiceShape(facts.Shape),
		Reference: facts.Reference, Title: facts.Title, Description: facts.Description,
	}, nil
}

func FHIRIdentifierPath(path string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(strings.TrimSpace(path), "root."), ".") {
		segment = strings.TrimSuffix(segment, "[]")
		if strings.EqualFold(segment, "id") {
			return true
		}
	}
	return false
}

func ApplicableRowChoiceKinds(facts capability.RowChoiceFacts) []capability.RowChoiceKind {
	var kinds []capability.RowChoiceKind
	if facts.Shape == capability.RowChoiceScalar && facts.Cardinality == capability.RowChoiceOne && !facts.Reference {
		kinds = append(kinds, capability.RowChoiceFieldGroupKey)
	}
	if facts.Shape == capability.RowChoiceArray && facts.Cardinality == capability.RowChoiceMany && !facts.Reference {
		kinds = append(kinds, capability.RowChoiceExpandedScope)
	}
	return kinds
}

func ResolveRowChoiceOccurrence(snapshot capability.Snapshot, rootResourceType string, authoredRoot ws.RouteNode, occurrenceID, expectedNodeID string) (capability.RowChoiceOccurrence, error) {
	path, found := support.RoutePathToOccurrence(authoredRoot, occurrenceID)
	if !found || len(path) == 0 || path[0].OccurrenceID != ws.RootOccurrenceID || path[0].ResourceType != rootResourceType {
		return capability.RowChoiceOccurrence{}, fmt.Errorf("row choice occurrence is not in the saved route")
	}
	var current capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			if current.ID != "" {
				return capability.RowChoiceOccurrence{}, fmt.Errorf("row choice root is ambiguous")
			}
			current = node
		}
	}
	if current.ID == "" {
		return capability.RowChoiceOccurrence{}, fmt.Errorf("row choice root is unavailable")
	}
	steps := make([]capability.ConstructionRouteStep, 0, len(path)-1)
	for index := 1; index < len(path); index++ {
		parent, child := path[index-1], path[index]
		edge, err := support.ResolveCapabilityRouteEdge(snapshot, current.ID, parent, child)
		if err != nil {
			return capability.RowChoiceOccurrence{}, err
		}
		to, ok := snapshot.Node(edge.ToNodeID)
		if !ok || to.ResourceType != child.ResourceType {
			return capability.RowChoiceOccurrence{}, fmt.Errorf("row choice route target is no longer authorized")
		}
		steps = append(steps, capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: current.ResourceType, ToResourceType: to.ResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(edge.StorageDirection),
			MatchMode: string(child.MatchMode.Normalized()),
		})
		current = to
	}
	if expectedNodeID != "" && current.ID != expectedNodeID {
		return capability.RowChoiceOccurrence{}, fmt.Errorf("row choice node identity changed")
	}
	resolved, err := support.ReauthorizeConstructionRoute(snapshot, rootResourceType, current.ID, steps)
	if err != nil {
		return capability.RowChoiceOccurrence{}, err
	}
	return capability.RowChoiceOccurrence{OccurrenceID: occurrenceID, NodeID: current.ID, ResourceType: current.ResourceType, Route: resolved}, nil
}

func SnapshotContainsRowCandidate(snapshot capability.Snapshot, occurrence capability.RowChoiceOccurrence, path string, kind capability.RowChoiceKind) bool {
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID != occurrence.NodeID || candidate.ResourceType != occurrence.ResourceType {
			continue
		}
		if candidate.FieldPath == path {
			return true
		}
		if kind != capability.RowChoiceExpandedScope {
			continue
		}
		for _, boundary := range candidate.RepeatedBoundaries {
			if boundary.Path == path {
				return true
			}
		}
	}
	return false
}

func CapabilityRowChoiceKind(kind RowChoiceKind) (capability.RowChoiceKind, error) {
	switch kind {
	case RowChoiceFieldGroup:
		return capability.RowChoiceFieldGroupKey, nil
	case RowChoiceExpanded:
		return capability.RowChoiceExpandedScope, nil
	default:
		return "", fmt.Errorf("unsupported row choice kind")
	}
}

func LifecycleRowChoiceKind(kind capability.RowChoiceKind) RowChoiceKind {
	switch kind {
	case capability.RowChoiceFieldGroupKey:
		return RowChoiceFieldGroup
	case capability.RowChoiceExpandedScope:
		return RowChoiceExpanded
	default:
		return ""
	}
}

func RowChoicePolicies(kind capability.RowChoiceKind) []RowDefinitionChoicePolicy {
	switch kind {
	case capability.RowChoiceFieldGroupKey:
		return []RowDefinitionChoicePolicy{{Name: "missingKeyPolicy", Options: []string{
			string(ws.MissingKeyError), string(ws.MissingKeyExclude), string(ws.MissingKeyGroupAsMissing),
		}}}
	case capability.RowChoiceExpandedScope:
		return []RowDefinitionChoicePolicy{{Name: "emptyCollectionPolicy", Options: []string{
			string(ws.EmptyCollectionError), string(ws.EmptyCollectionExclude), string(ws.EmptyCollectionPreserveParent),
		}}}
	default:
		return []RowDefinitionChoicePolicy{}
	}
}

func OccurrenceSummary(choice capability.RowChoice) string {
	if len(choice.Route) == 0 {
		return "Occurrence " + choice.OccurrenceID + " (root)"
	}
	return "Occurrence " + choice.OccurrenceID + " via " + choice.Route[len(choice.Route)-1].Relationship
}

func RouteSummary(choice capability.RowChoice) string {
	if len(choice.Route) == 0 {
		return "Root"
	}
	parts := make([]string, 0, len(choice.Route)+1)
	parts = append(parts, "Root")
	for _, step := range choice.Route {
		parts = append(parts, step.Relationship)
	}
	return strings.Join(parts, " › ")
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

func findSemanticOutput(workspace ws.Workspace, outputID string) *ws.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

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
		if document.Rows.Kind == ws.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == ws.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil {
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

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (ws.Workspace, error) {
	project = projectid.Canonical(project)
	owner, err := s.config.GetOwner(ctx, project, explorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return ws.Workspace{}, support.NotFound("construction-choices", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return ws.Workspace{}, support.PersistenceFailure("construction-choices", "the current Explorer workspace could not be loaded", err)
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := ws.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return ws.Workspace{}, support.Conflict("construction-choices", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.config.Store.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return ws.Workspace{}, support.PersistenceFailure("construction-choices", "the active Explorer workspace could not be loaded", activeErr)
		}
		workspace, decodeErr := s.config.Store.RevisionWorkspace(ctx, *active)
		if decodeErr != nil {
			return ws.Workspace{}, support.Conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	return ws.Workspace{}, support.Conflict("construction-choices", "AUTHORING_STATE_MISSING", "the Explorer has no V2 authoring workspace", nil, nil)
}
