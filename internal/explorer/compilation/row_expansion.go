package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// expandedItemFieldSuffix returns the selector suffix when a direct field
// belongs to the selected expanded item. The persisted FieldRef remains the
// owner-relative path for provenance; only the executable selector is rebased.
func expandedItemFieldSuffix(expanded *authoringv2.ExpandedRows, occurrenceID, fieldPath string) (string, bool) {
	if expanded == nil || expanded.OccurrenceID != occurrenceID {
		return "", false
	}
	scopePath := strings.TrimPrefix(strings.TrimSpace(expanded.ScopePath), "root.")
	fieldPath = strings.TrimPrefix(strings.TrimSpace(fieldPath), "root.")
	if scopePath == "" || !strings.HasPrefix(fieldPath, scopePath+".") {
		return "", false
	}
	suffix := strings.TrimPrefix(fieldPath, scopePath+".")
	return suffix, suffix != ""
}

func compileExpandedRows(expanded *authoringv2.ExpandedRows, route authoringv2.RouteNode, occurrences map[string]semanticOccurrence, snapshot capability.Snapshot) (recipe.Expansion, error) {
	if expanded == nil {
		return recipe.Expansion{}, fail("intent", "INVALID_ROW_EXPANSION", "$.rows.expanded", "expanded row definition is missing", nil, nil)
	}
	owner, ok := occurrences[expanded.OccurrenceID]
	if !ok {
		return recipe.Expansion{}, fail("intent", "STALE_EXPANSION_OCCURRENCE", "$.rows.expanded.occurrenceId", "expanded row owner occurrence is not present in the resolved route", map[string]any{"occurrenceId": expanded.OccurrenceID}, nil)
	}
	path := strings.TrimPrefix(expanded.ScopePath, "root.")
	if path == "" || path != strings.TrimSpace(path) {
		return recipe.Expansion{}, fail("intent", "INVALID_EXPANSION_SCOPE", "$.rows.expanded.scopePath", "expanded scope path must be a canonical FHIR path", map[string]any{"scopePath": expanded.ScopePath}, nil)
	}

	var candidate capability.Candidate
	matches := 0
	for _, item := range snapshot.Candidates {
		if item.NodeID != owner.graph.ID || item.ResourceType != owner.graph.ResourceType || canonicalSemanticFieldPath(item.FieldPath) != path {
			continue
		}
		candidate = item
		matches++
	}
	if matches == 0 {
		return recipe.Expansion{}, fail("intent", "STALE_EXPANSION_SCOPE", "$.rows.expanded.scopePath", "expanded scope is not present on the selected capability occurrence", map[string]any{"occurrenceId": expanded.OccurrenceID, "resourceType": owner.graph.ResourceType, "scopePath": path}, nil)
	}
	if matches != 1 {
		return recipe.Expansion{}, fail("intent", "AMBIGUOUS_EXPANSION_SCOPE", "$.rows.expanded.scopePath", "expanded scope must resolve to exactly one capability candidate", map[string]any{"occurrenceId": expanded.OccurrenceID, "scopePath": path, "matches": matches}, nil)
	}
	if !capability.IsRepeatedCardinality(candidate.Cardinality) || len(candidate.RepeatedBoundaries) == 0 {
		return recipe.Expansion{}, fail("capability", "UNSUPPORTED_EXPANSION_SCOPE", "$.rows.expanded.scopePath", "expanded scope lacks repeated-value evidence in the current capability snapshot", map[string]any{"candidateId": candidate.ID, "cardinality": candidate.Cardinality}, nil)
	}

	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return recipe.Expansion{}, fail("capability", "FHIR_SCHEMA_UNAVAILABLE", "$.rows.expanded.scopePath", "generated FHIR schema is unavailable", nil, err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(owner.graph.ResourceType), path)
	if err != nil || facts.Shape != fhirschema.RowPathArray || facts.Cardinality != fhirschema.RowCardinalityMany || facts.Reference {
		if err == nil {
			err = fmt.Errorf("scope resolves to shape %s, cardinality %s, reference=%t", facts.Shape, facts.Cardinality, facts.Reference)
		}
		return recipe.Expansion{}, fail("capability", "UNSUPPORTED_EXPANSION_SCOPE", "$.rows.expanded.scopePath", "expanded scope must be a generated-schema repeated non-reference value", map[string]any{"resourceType": owner.graph.ResourceType, "scopePath": path}, err)
	}

	choiceFacts := capability.RowChoiceFacts{
		ResourceType: owner.graph.ResourceType, CanonicalPath: path, FHIRType: string(facts.FHIRType),
		Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray, Reference: facts.Reference,
	}
	if _, err := capability.NewRowChoice(snapshot, semanticRowChoiceOccurrences(route, occurrences), expanded.OccurrenceID, capability.RowChoiceExpandedScope, choiceFacts); err != nil {
		return recipe.Expansion{}, fail("capability", "UNAUTHORIZED_EXPANSION_SCOPE", "$.rows.expanded", "expanded scope is not backed by the exact current capability route", map[string]any{"occurrenceId": expanded.OccurrenceID, "scopePath": path}, err)
	}

	policy := recipe.ExpansionExclude
	switch expanded.EmptyCollectionPolicy {
	case authoringv2.EmptyCollectionError:
		policy = recipe.ExpansionError
	case authoringv2.EmptyCollectionExclude:
		policy = recipe.ExpansionExclude
	case authoringv2.EmptyCollectionPreserveParent:
		policy = recipe.ExpansionPreserveParent
	default:
		return recipe.Expansion{}, fail("intent", "INVALID_ROW_EXPANSION", "$.rows.expanded.emptyCollectionPolicy", "expanded row empty-collection policy is unsupported", map[string]any{"policy": expanded.EmptyCollectionPolicy}, nil)
	}

	return recipe.Expansion{
		OwnerOccurrenceID: expanded.OccurrenceID,
		From:              recipe.Expression{Select: semanticAlias(expanded.OccurrenceID) + "." + path},
		As:                "__loom_expanded_item",
		Ordinality:        "__loom_expanded_ordinal",
		EmptyPolicy:       policy,
	}, nil
}

func semanticRowChoiceOccurrences(route authoringv2.RouteNode, occurrences map[string]semanticOccurrence) []capability.RowChoiceOccurrence {
	result := make([]capability.RowChoiceOccurrence, 0)
	var walk func(authoringv2.RouteNode, []capability.ConstructionRouteStep)
	walk = func(node authoringv2.RouteNode, parentRoute []capability.ConstructionRouteStep) {
		resolved := occurrences[node.OccurrenceID]
		result = append(result, capability.RowChoiceOccurrence{
			OccurrenceID: node.OccurrenceID,
			NodeID:       resolved.graph.ID,
			ResourceType: resolved.graph.ResourceType,
			Route:        append([]capability.ConstructionRouteStep(nil), parentRoute...),
		})
		for _, child := range node.Children {
			childOccurrence := occurrences[child.OccurrenceID]
			edge := childOccurrence.edge
			if edge == nil {
				continue
			}
			matchMode := authoringv2.RouteMatchOptional
			if child.MatchMode.Normalized() == authoringv2.RouteMatchRequired {
				matchMode = authoringv2.RouteMatchRequired
			}
			step := capability.ConstructionRouteStep{
				EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
				FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
				Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)),
				MatchMode: string(matchMode),
			}
			next := append(append([]capability.ConstructionRouteStep(nil), parentRoute...), step)
			walk(child, next)
		}
	}
	walk(route, nil)
	return result
}

func canonicalSemanticFieldPath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "root.")
}
