package authoringv2

import (
	"github.com/calypr/loom/internal/explorer/capability"
	"strings"
)

func CatalogFromCapability(snapshot capability.Snapshot, explorerID string) CatalogSnapshot {
	result := CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind,
		Project: snapshot.Identity.Project, ExplorerID: explorerID,
		SourceGeneration:         snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		ResolvedSchemaDigest:     snapshot.Identity.SchemaDigest,
		SnapshotToken:            snapshot.Token, Complete: snapshot.Complete, Truncated: snapshot.Truncated,
		Availability: catalogAvailability(snapshot.Availability),
		Nodes:        []CatalogNode{}, Edges: []CatalogEdge{}, Candidates: []CatalogCandidate{}, Diagnostics: []CatalogDiagnostic{},
		RoutePolicy: RoutePolicy{Unbounded: snapshot.Policy.Route.MaxHops == 0, AllowRepeatedEdges: snapshot.Policy.Route.AllowsRepeatedEdges, AllowSelfLoops: snapshot.Policy.Route.AllowsSelfLoops},
	}
	if snapshot.Policy.Route.MaxHops > 0 {
		maxHops := snapshot.Policy.Route.MaxHops
		result.RoutePolicy.MaxHops = &maxHops
	}
	for _, node := range snapshot.Nodes {
		count := node.DocumentCount
		result.Nodes = append(result.Nodes, CatalogNode{
			ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible,
			RowGrain: node.RowGrain, Populated: node.Populated, DocumentCount: &count,
		})
	}
	for _, edge := range snapshot.Edges {
		result.Edges = append(result.Edges, CatalogEdge{
			ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label,
			StorageDirection: edge.StorageDirection, Populated: edge.ObservedEdgeCount > 0,
		})
	}
	for _, candidate := range snapshot.Candidates {
		projectionModes := catalogProjectionModes(candidate.ProjectionModes)
		count := candidate.ObservedDocumentCount
		wire := CatalogCandidate{
			ID: candidate.ID, NodeID: candidate.NodeID, Label: candidate.Label,
			FieldPath:          candidate.FieldPath,
			RepeatedBoundaries: catalogRepeatedBoundaries(candidate.RepeatedBoundaries),
			LogicalType:        candidate.LogicalType, Cardinality: candidate.Cardinality, Repeated: capability.IsRepeatedCardinality(candidate.Cardinality),
			Filterable: len(candidate.FilterOperators) > 0, Chartable: len(candidate.ChartAggregations) > 0,
			ProjectionModes: projectionModes, DefaultProjectionMode: catalogDefaultProjectionMode(projectionModes),
			FilterOperators: catalogFilterOperators(candidate.FilterOperators), ChartOperations: catalogChartOperations(candidate.ChartAggregations),
			AggregateOperations: append([]capability.AggregateOperationCapability{}, candidate.AggregateOperations...),
			Populated:           candidate.Populated, Count: &count,
			SuggestionsAvailable: len(candidate.SuggestedValues) > 0,
			SuggestionsComplete:  candidate.SuggestionsComplete,
			SuggestionsTruncated: candidate.SuggestionsTruncated,
			SuggestionCount:      len(candidate.SuggestedValues),
			ConceptCandidates:    catalogConceptCandidates(candidate.ConceptCandidates),
		}
		choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
		if err != nil {
			code := "CONSTRUCTION_CHOICE_UNAVAILABLE"
			message := "A catalog candidate was omitted because no compiler-proved construction output is available."
			if !capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
				code = "UNSUPPORTED_CONSTRUCTION_SOURCE_TYPE"
				if typeErr := capability.ValidateConstructionSourceType(candidate, snapshot.Candidates); typeErr != nil {
					message = typeErr.Error()
				}
			}
			result.Diagnostics = append(result.Diagnostics, CatalogDiagnostic{
				Severity: "WARNING", Code: code, Message: message,
			})
			continue
		}
		wire.ConstructionChoice = &choice
		result.Candidates = append(result.Candidates, wire)
	}
	transformations := AggregateTransformationCapabilitiesForCatalogCandidates(result)
	for index := range result.Candidates {
		result.Candidates[index].Transformations = transformations[index]
		result.Candidates[index].ValueTransformations = ColumnValueTransformationCapabilitiesForCatalog(result, result.Candidates[index].ID)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		severity := diagnostic.Severity
		if snapshot.Usable() && strings.EqualFold(severity, "ERROR") {
			severity = "WARNING"
		}
		result.Diagnostics = append(result.Diagnostics, CatalogDiagnostic{Severity: severity, Code: diagnostic.Code, Message: diagnostic.Message})
	}
	return result
}

func catalogConceptCandidates(values []capability.ConceptCandidate) []ConceptCandidate {
	return capability.CloneConceptCandidates(values)
}

func catalogAvailability(value *capability.Availability) *CatalogAvailability {
	if value == nil {
		return nil
	}
	result := &CatalogAvailability{
		Readiness: CatalogAvailabilityReadiness{
			Status: value.Readiness.Status, Reason: value.Readiness.Reason,
			ResourceInventory: CatalogAvailabilitySourceReadiness{Status: value.Readiness.ResourceInventory.Status, Reason: value.Readiness.ResourceInventory.Reason},
			Relationships:     CatalogAvailabilitySourceReadiness{Status: value.Readiness.Relationships.Status, Reason: value.Readiness.Relationships.Reason},
			FieldProfiles:     CatalogAvailabilitySourceReadiness{Status: value.Readiness.FieldProfiles.Status, Reason: value.Readiness.FieldProfiles.Reason},
		},
		Resources:     make([]CatalogObservedResource, 0, len(value.Resources)),
		Fields:        make([]CatalogObservedField, 0, len(value.Fields)),
		Relationships: make([]CatalogObservedRelationship, 0, len(value.Relationships)),
	}
	for _, resource := range value.Resources {
		result.Resources = append(result.Resources, CatalogObservedResource{ResourceType: resource.ResourceType, DocumentCount: resource.DocumentCount, Populated: resource.Populated})
	}
	for _, field := range value.Fields {
		semanticObservations := catalogConceptCandidates(field.SemanticObservations)
		if semanticObservations == nil {
			semanticObservations = []ConceptCandidate{}
		}
		result.Fields = append(result.Fields, CatalogObservedField{
			ResourceType: field.ResourceType, Path: field.Path, ObservedShapeKind: field.ShapeKind,
			PathObservedDocumentCount: field.PathObservedCount, Examples: append([]string{}, field.Examples...),
			ExamplesTruncated: field.ExamplesTruncated, SemanticObservations: semanticObservations,
		})
	}
	for _, relationship := range value.Relationships {
		result.Relationships = append(result.Relationships, CatalogObservedRelationship{
			SourceResourceType: relationship.SourceResourceType, TargetResourceType: relationship.TargetResourceType,
			Label: relationship.Label, StorageDirection: relationship.StorageDirection, BuilderDirection: relationship.BuilderDirection,
			ObservedEdgeCount: relationship.ObservedEdgeCount,
		})
	}
	return result
}

func catalogProjectionModes(values []capability.ProjectionMode) []string {

	out := make([]string, len(values))
	for i, value := range values {
		switch value {
		case capability.ProjectionScalar:
			out[i] = "VALUE"
		case capability.ProjectionArray:
			out[i] = "ALL"
		case capability.ProjectionDistinctArray:
			out[i] = "DISTINCT"
		default:
			out[i] = string(value)
		}
	}
	return out
}

func catalogFilterOperators(values []capability.FilterOperator) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func catalogChartOperations(values []capability.ChartAggregation) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func catalogDefaultProjectionMode(values []string) string {
	for _, preferred := range []string{"VALUE", "INDEXED", "ALL", "FIRST", "COUNT"} {
		for _, value := range values {
			if value == preferred {
				return value
			}
		}
	}
	return ""
}

func catalogRepeatedBoundaries(values []capability.RepeatedBoundary) []RepeatedBoundary {
	out := make([]RepeatedBoundary, len(values))
	for index, value := range values {
		out[index] = RepeatedBoundary{Path: value.Path, MaxItems: value.MaxItems}
	}
	return out
}
