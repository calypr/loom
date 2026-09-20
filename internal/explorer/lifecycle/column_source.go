package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type ColumnSourceRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
	Column        string
}

type ColumnSourceRouteStep struct {
	OccurrenceID     string `json:"occurrenceId"`
	ResourceType     string `json:"resourceType"`
	CatalogEdgeID    string `json:"catalogEdgeId,omitempty"`
	Relationship     string `json:"relationship,omitempty"`
	StorageDirection string `json:"storageDirection,omitempty"`
	MatchMode        string `json:"matchMode,omitempty"`
}

type ColumnSourceResponse struct {
	SnapshotToken string                              `json:"snapshotToken"`
	OutputID      string                              `json:"outputId"`
	Column        string                              `json:"column"`
	Route         []ColumnSourceRouteStep             `json:"route"`
	Summary       string                              `json:"summary"`
	Facts         []capability.ConstructionChoiceFact `json:"facts"`
}

func (s *Service) ColumnSource(ctx context.Context, request ColumnSourceRequest) (ColumnSourceResponse, error) {
	result := ColumnSourceResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Column: request.Column,
		Route: []ColumnSourceRouteStep{}, Facts: []capability.ConstructionChoiceFact{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.Column) == "" {
		return result, malformed("column-source", "project, explorerId, snapshotToken, outputId, and column are required", nil)
	}
	if s.config.Capability.ForCompilation == nil {
		return result, unavailable("column-source", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, conflict("column-source", "STALE_CATALOG_SNAPSHOT", "reload the Builder before inspecting this column", nil, err)
	}
	snapshot := authorized.Snapshot
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("column-source", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil {
		return result, notFound("column-source", "COLUMN_NOT_FOUND", "the requested table does not exist", nil)
	}
	var column *authoringv2.Column
	for index := range document.Columns {
		if document.Columns[index].Column == request.Column {
			column = &document.Columns[index]
			break
		}
	}
	if column == nil {
		return result, notFound("column-source", "COLUMN_NOT_FOUND", "the requested column does not exist in this table", nil)
	}
	route, compilerRoute, resourceType, err := columnRoute(document, column.OccurrenceID, snapshot)
	if err != nil {
		return result, conflict("column-source", "STALE_COLUMN_ROUTE", "the column's saved route no longer resolves in this authorized snapshot", nil, err)
	}
	if !provePopulationRoute(ctx, authorized, document.RootResourceType, resourceType, compilerRoute) {
		return result, conflict("column-source", "STALE_COLUMN_ROUTE", "the column's saved route no longer compiles in this authorized snapshot", nil, nil)
	}
	result.Route = route
	result.Summary, result.Facts = columnSourcePresentation(*column, resourceType)
	return result, nil
}

func columnRoute(document *authoringv2.Document, occurrenceID string, snapshot capability.Snapshot) ([]ColumnSourceRouteStep, []capability.ConstructionRouteStep, string, error) {
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return nil, nil, "", fmt.Errorf("table row-root route is invalid")
	}
	root, found := capabilityRootNode(snapshot, document.RootResourceType)
	if !found {
		return nil, nil, "", fmt.Errorf("table row root is missing or ambiguous")
	}
	var path []authoringv2.RouteNode
	if !findOccurrencePath(document.Route, occurrenceID, nil, &path) {
		return nil, nil, "", fmt.Errorf("column occurrence %q is not in its table route", occurrenceID)
	}
	if len(path) == 0 || path[0].OccurrenceID != authoringv2.RootOccurrenceID {
		return nil, nil, "", fmt.Errorf("column occurrence route does not begin at the row root")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(path)-1 > snapshot.Policy.Route.MaxHops {
		return nil, nil, "", fmt.Errorf("column route exceeds the current route policy")
	}
	responseRoute := make([]ColumnSourceRouteStep, 0, len(path))
	compilerRoute := make([]capability.ConstructionRouteStep, 0, len(path)-1)
	responseRoute = append(responseRoute, ColumnSourceRouteStep{OccurrenceID: path[0].OccurrenceID, ResourceType: path[0].ResourceType})
	currentNodeID := root.ID
	usedEdges := make(map[string]bool, len(path)-1)
	for index := 1; index < len(path); index++ {
		parent, child := path[index-1], path[index]
		edge, err := resolveCapabilityRouteEdge(snapshot, currentNodeID, parent, child)
		if err != nil {
			return nil, nil, "", fmt.Errorf("relationship %q from %s to %s does not resolve: %w", child.Relationship, parent.ResourceType, child.ResourceType, err)
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && usedEdges[edge.ID] {
			return nil, nil, "", fmt.Errorf("column route repeats edge %q but repeated edges are not allowed", edge.ID)
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return nil, nil, "", fmt.Errorf("column route uses self-loop edge %q but self-loops are not allowed", edge.ID)
		}
		target, ok := snapshot.Node(edge.ToNodeID)
		if !ok || target.ResourceType != child.ResourceType {
			return nil, nil, "", fmt.Errorf("relationship %q target changed", child.Relationship)
		}
		matchMode := string(child.MatchMode.Normalized())
		step := capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: parent.ResourceType, ToResourceType: child.ResourceType,
			Relationship: child.Relationship, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: matchMode,
		}
		compilerRoute = append(compilerRoute, step)
		responseRoute = append(responseRoute, ColumnSourceRouteStep{
			OccurrenceID: child.OccurrenceID, ResourceType: child.ResourceType,
			CatalogEdgeID: edge.ID, Relationship: child.Relationship, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: matchMode,
		})
		usedEdges[edge.ID] = true
		currentNodeID = target.ID
	}
	return responseRoute, compilerRoute, path[len(path)-1].ResourceType, nil
}

func findOccurrencePath(node authoringv2.RouteNode, occurrenceID string, prefix []authoringv2.RouteNode, result *[]authoringv2.RouteNode) bool {
	current := append(append([]authoringv2.RouteNode(nil), prefix...), node)
	if node.OccurrenceID == occurrenceID {
		*result = current
		return true
	}
	for _, child := range node.Children {
		if findOccurrencePath(child, occurrenceID, current, result) {
			return true
		}
	}
	return false
}

func capabilityRootNode(snapshot capability.Snapshot, resourceType string) (capability.Node, bool) {
	var result capability.Node
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != resourceType {
			continue
		}
		if result.ID != "" {
			return capability.Node{}, false
		}
		result = node
	}
	return result, result.ID != ""
}

func columnSourcePresentation(column authoringv2.Column, resourceType string) (string, []capability.ConstructionChoiceFact) {
	facts := []capability.ConstructionChoiceFact{{Label: "Value type", Value: firstColumnFact(column.LogicalType, "Unknown")}, {Label: "FHIR resource", Value: resourceType}, {Label: "Source kind", Value: column.Source.Kind}}
	add := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			facts = append(facts, capability.ConstructionChoiceFact{Label: label, Value: value})
		}
	}
	source := column.Source
	add("FHIR path", source.FieldPath())
	add("Projection", source.ProjectionMode())
	if source.Field != nil && source.Field.RelatedSelection != nil {
		add("Related records", source.Field.RelatedSelection.Kind)
	}
	if source.Lookup != nil {
		lookup := source.Lookup
		if lookup.Identifier != nil {
			add("Identifier system", lookup.Identifier.SystemURI)
			add("Identifier owner", lookup.Identifier.OwnerPath)
			add("Identifier value", lookup.Identifier.ValuePath)
		}
		if lookup.Binding != nil {
			if lookup.Key != nil {
				add("Code system", lookup.Key.System)
				add("Code", lookup.Key.Code)
			}
			add("Value selector", lookup.Binding.ValuePath)
			if len(lookup.Binding.ChoiceArms) > 0 {
				add("FHIR choice arm", strings.Join(lookup.Binding.ChoiceArms, ", "))
			}
		}
		if lookup.Extension != nil {
			add("Extension URL path", strings.Join(lookup.Extension.URLPath, " → "))
			add("Extension owner", lookup.Extension.OwnerPath)
			add("Extension value", lookup.Extension.ValuePath)
			if len(lookup.Extension.ChoiceArms) > 0 {
				add("FHIR choice arm", strings.Join(lookup.Extension.ChoiceArms, ", "))
			}
		}
	}
	if source.Aggregate != nil {
		add("Operation", source.Aggregate.Operation)
		add("Input path", source.Aggregate.Path)
		if source.Aggregate.Temporal != nil {
			add("Time direction", source.Aggregate.Temporal.Direction)
		}
		if source.Aggregate.UnitNormalization != nil {
			add("Unit policy", source.Aggregate.UnitNormalization.PolicyID+"@"+source.Aggregate.UnitNormalization.Version)
		}
	}
	if source.OwnerRecords != nil {
		add("Owner", source.OwnerRecords.Binding.OwnerPath)
		add("Owner code", source.OwnerRecords.Key.Code)
		add("Owner value", source.OwnerRecords.Binding.ValuePath)
	}
	if column.Source.Kind == authoringv2.SourceProjectID {
		add("Value", "Project identifier")
	}
	label := strings.TrimSpace(column.Label)
	if label == "" {
		label = column.Column
	}
	summary := "" + label + " from " + resourceType
	return summary, facts
}

func firstColumnFact(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
