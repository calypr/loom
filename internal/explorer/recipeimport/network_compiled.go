package recipeimport

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// BindCompiledNetwork binds imported output IDs to compiler-proven origins and
// the original source descriptors. Current settings are required because
// disabled projection families compact compiler arrays but never original IDs.
// Ambiguous legacy discovery provenance is diagnosed rather than guessed.
func BindCompiledNetwork(network authoringv2.AuthoringNetwork, columns []lower.CompiledOutputColumn, plans ...*authoringv2.ImportedSourcePlan) (authoringv2.AuthoringNetwork, error) {
	if len(plans) != 1 || plans[0] == nil {
		return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_UNAVAILABLE: exact source descriptors are required")
	}
	plan := plans[0]
	if err := plan.Validate(); err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	occurrences := map[string]authoringv2.NetworkOccurrence{}
	scopes := map[string]authoringv2.NetworkOccurrence{}
	for _, o := range network.Occurrences {
		occurrences[o.OccurrenceID] = o
		scopes[o.SourceStepID] = o
	}
	additions := map[string]authoringv2.ImportedRelationship{}
	if plan.Relationships != nil {
		for _, a := range plan.Relationships.Additions {
			additions[a.OccurrenceID] = a
		}
	}
	bound := map[string]authoringv2.NetworkColumnBinding{}
	for _, column := range columns {
		if column.Internal || column.Identity || column.SourceOrigin == nil {
			continue
		}
		origin := column.SourceOrigin
		occurrence, ok := occurrences[origin.OccurrenceID]
		scope, relative := sourceOriginScope(origin.SourcePath)
		if !ok && origin.OccurrenceID == "" {
			occurrence, ok = scopes[scope]
		}
		if !ok {
			return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_UNAVAILABLE: compiled source column %q refers to unavailable occurrence %q", column.ID, origin.OccurrenceID)
		}
		sourceStep := ""
		if addition, exists := additions[occurrence.OccurrenceID]; exists {
			for _, field := range addition.Fields {
				if field.ColumnID == column.ID {
					sourceStep = "relationships." + addition.OccurrenceID
				}
			}
			if sourceStep == "" {
				return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_UNAVAILABLE: addition %q does not own column %q", addition.OccurrenceID, column.ID)
			}
		} else {
			kind := origin.SourceKind
			index := 0
			if kind == "" {
				switch {
				case strings.HasPrefix(relative, "fields"):
					kind = SourcePlanFieldGroup
				case strings.HasPrefix(relative, "catalogProjections["):
					kind = SourcePlanCatalogProjection
				case strings.HasPrefix(relative, "dynamicColumns["):
					kind = SourcePlanDynamicColumn
				case strings.HasPrefix(relative, "extensionColumns["):
					kind = SourcePlanExtensionColumn
				case strings.HasPrefix(relative, "pivots["):
					kind = SourcePlanPivot
				}
			}
			if start := strings.Index(relative, "["); start >= 0 {
				if end := strings.Index(relative[start:], "]"); end >= 0 {
					value, err := strconv.Atoi(relative[start+1 : start+end])
					if err != nil {
						return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_UNAVAILABLE: malformed compiler source path")
					}
					index = value
				}
			}
			var eligible []authoringv2.ImportedSourceStep
			for _, step := range plan.Steps {
				if step.ParentID == occurrence.SourceStepID && step.Kind == kind && step.Enabled {
					eligible = append(eligible, step)
				}
			}
			// Catalog resolution predates per-field projection provenance. A unique
			// original projection is an explicit adapter; several are unsupported until
			// the resolver retains their separate identities through discovery.
			if origin.SourceKey != "" {
				matching := eligible[:0]
				for _, step := range eligible {
					if step.SourceKey == origin.SourceKey {
						matching = append(matching, step)
					}
				}
				eligible = matching
				index = 0
				if len(eligible) != 1 {
					return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_AMBIGUOUS: column %q has %d source owners matching native family %q", column.ID, len(eligible), origin.SourceKey)
				}
			}
			if kind == SourcePlanCatalogProjection && len(eligible) != 1 {
				return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_AMBIGUOUS: column %q has %d eligible catalog projection owners in occurrence %q", column.ID, len(eligible), occurrence.OccurrenceID)
			}
			if index >= len(eligible) || kind == "" {
				return authoringv2.AuthoringNetwork{}, fmt.Errorf("SOURCE_PROVENANCE_UNAVAILABLE: column %q has no original %s source step in occurrence %q", column.ID, kind, occurrence.OccurrenceID)
			}
			sourceStep = eligible[index].ID
		}
		bound[column.ID] = authoringv2.NetworkColumnBinding{ColumnID: column.ID, OccurrenceID: occurrence.OccurrenceID, SourceStepID: sourceStep, ValueMode: authoringValueMode(origin.ValueMode), Status: "EXACT"}
	}
	for i, b := range network.Bindings {
		if exact, ok := bound[b.ColumnID]; ok {
			network.Bindings[i] = exact
		}
	}
	return network, nil
}

// sourceOriginScope splits a compiler structural path at its last traversal
// occurrence, retaining a relative projection-family path. It never parses
// display text or a FHIR field path.
func sourceOriginScope(path string) (string, string) {
	scope := ""
	start := 0
	for {
		at := strings.Index(path[start:], "traversals[")
		if at < 0 {
			break
		}
		at += start
		if at > 0 && path[at-1] != '.' {
			break
		}
		end := strings.Index(path[at:], "]")
		if end < 0 {
			break
		}
		end += at + 1
		scope = path[:end]
		start = end
	}
	if scope == "" {
		return "", path
	}
	return scope, strings.TrimPrefix(path[len(scope):], ".")
}

// Compiler projection shapes use a different vocabulary from authored value
// policies. Preserve the first/all/distinct distinction at the read boundary.
func authoringValueMode(mode string) string {
	switch mode {
	case "scalar":
		return "VALUE"
	case "first":
		return "FIRST"
	case "array":
		return "ALL"
	case "distinct_array":
		return "DISTINCT"
	default:
		return strings.ToUpper(mode)
	}
}
