// Package schema resolves catalog-backed recipe declarations into a
// concrete, typed recipe before semantic compilation. It deliberately knows
// nothing about AQL, Arango collections, or output-specific behavior.
package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func resolveProjectionSets(ctx context.Context, scope Scope, discovery Discovery, resourceType, alias string, sets []recipe.CatalogProjection) ([]recipe.Field, error) {
	fields := make([]recipe.Field, 0)
	seen := map[string]struct{}{}
	for _, set := range sets {
		candidates, err := discovery.Fields(ctx, scope, resourceType)
		if err != nil {
			return nil, err
		}
		selected := filterCandidates(candidates, set)
		// Catalog projections are optional shape discovery. A resource type can
		// be absent from a valid dataset (or have no populated fields matching
		// the requested kind); retain the static recipe fields and resolve this
		// projection set to zero columns rather than rejecting the whole bundle.
		if len(selected) == 0 {
			continue
		}
		if len(selected) > set.MaxColumns {
			return nil, fmt.Errorf("projection set %q matched %d fields, max is %d", set.Name, len(selected), set.MaxColumns)
		}
		sort.SliceStable(selected, func(i, j int) bool { return selected[i].Path < selected[j].Path })
		for _, candidate := range selected {
			name := projectionName(set, candidate.Path)
			if _, exists := seen[name]; exists {
				return nil, fmt.Errorf("projection set %q produced duplicate column %q", set.Name, name)
			}
			seen[name] = struct{}{}
			selectPath := strings.TrimPrefix(candidate.Path, ".")
			if alias != "" && alias != "root" {
				selectPath = alias + "." + selectPath
			} else {
				selectPath = "root." + selectPath
			}
			fields = append(fields, recipe.Field{Name: name, FieldRef: candidate.Path, ValueMode: set.ValueMode, Expr: recipe.Expression{Select: selectPath}})
		}
	}
	return fields, nil
}

func resolvePivots(ctx context.Context, scope Scope, discovery Discovery, resourceType, alias string, pivots []recipe.Pivot) ([]recipe.Pivot, error) {
	resolved := pivots[:0]
	for index := range pivots {
		pivot := &pivots[index]
		if pivot.Discovery == nil {
			resolved = append(resolved, *pivot)
			continue
		}
		candidates, err := discovery.Fields(ctx, scope, resourceType)
		if err != nil {
			return nil, err
		}
		columns := map[string]struct{}{}
		var columnSelect, valueSelect string
		var pivotSpec fhirschema.PivotSpec
		var hasPivotSpec bool
		for _, candidate := range candidates {
			if !candidate.PivotCandidate {
				continue
			}
			if pivot.Discovery.Family != "" && !strings.EqualFold(candidate.PivotFamily, pivot.Discovery.Family) {
				continue
			}
			if pivot.Discovery.Path != "" && candidate.Path != pivot.Discovery.Path {
				continue
			}
			if !hasPivotSpec {
				observedValuePath := ""
				if candidate.PivotFamily == "observation_code_value" {
					observedValuePath = candidate.PivotValueSelect
				}
				if spec, ok := fhirschema.DefaultPivotSpec(resourceType, candidate.Path, observedValuePath); ok {
					pivotSpec, hasPivotSpec = spec, true
					// Persisted catalog metadata is an immutable observation of
					// the loaded shape. Prefer it when present, while retaining the
					// generated schema default for older catalog documents.
					if candidate.PivotColumnSelect != "" {
						pivotSpec.ColumnSelector = fhirschema.FieldSelectorSpecFromPath(candidate.PivotColumnSelect)
					}
					if candidate.PivotValueSelect != "" {
						pivotSpec.ValueSelector = fhirschema.FieldSelectorSpecFromPath(candidate.PivotValueSelect)
					}
					if candidate.PivotItemSource != "" {
						pivotSpec.ItemSourcePath = candidate.PivotItemSource
					}
					if candidate.PivotItemResourceType != "" {
						pivotSpec.ItemResourceType = candidate.PivotItemResourceType
					}
					if len(candidate.PivotValueSelectors) > 0 {
						pivotSpec.ValueSelectors = make([]fhirschema.FieldSelectorSpec, 0, len(candidate.PivotValueSelectors))
						for _, selector := range candidate.PivotValueSelectors {
							pivotSpec.ValueSelectors = append(pivotSpec.ValueSelectors, fhirschema.FieldSelectorSpecFromPath(selector))
						}
						pivotSpec.ValueSelector = pivotSpec.ValueSelectors[0]
					}
					if pivotSpec.ItemSourcePath != "" {
						pivotSpec.ColumnSelector = fhirschema.FieldSelectorSpecFromPath(relativePivotPath(pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(pivotSpec.ColumnSelector)))
						pivotSpec.ValueSelector = fhirschema.FieldSelectorSpecFromPath(relativePivotPath(pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(pivotSpec.ValueSelector)))
						for index, selector := range pivotSpec.ValueSelectors {
							pivotSpec.ValueSelectors[index] = fhirschema.FieldSelectorSpecFromPath(relativePivotPath(pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(selector)))
						}
					}
				}
			}
			if columnSelect == "" {
				columnSelect = candidate.PivotColumnSelect
			}
			if valueSelect == "" {
				valueSelect = candidate.PivotValueSelect
			}
			for _, column := range candidate.PivotColumns {
				columns[column] = struct{}{}
			}
			if len(candidate.PivotColumns) == 0 {
				for _, column := range candidate.DistinctValues {
					columns[column] = struct{}{}
				}
			}
		}
		if len(columns) > pivot.Discovery.MaxColumns {
			return nil, fmt.Errorf("pivot %q discovery found %d columns, exceeding maxColumns %d", pivot.Name, len(columns), pivot.Discovery.MaxColumns)
		}
		pivot.Columns = sortedValues(columns)
		if len(pivot.Columns) == 0 {
			log.Printf("dataframe schema discovery: omit pivot %q for %s: no columns matched", pivot.Name, resourceType)
			continue
		}
		if pivot.ColumnExpr.Select == "" && pivot.ColumnExpr.Call == "" {
			if hasPivotSpec {
				pivot.ColumnExpr = recipe.Expression{Select: qualifyPivotSelector(alias, pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(pivotSpec.ColumnSelector))}
				pivot.ValueExpr = recipe.Expression{Select: qualifyPivotSelector(alias, pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(pivotSpec.ValueSelector))}
				if pivotSpec.ItemSourcePath != "" {
					pivot.ItemResourceType = pivotSpec.ItemResourceType
					pivot.ItemSource = recipe.Expression{Select: qualifyDiscoveredSelector(alias, pivotSpec.ItemSourcePath)}
				}
				for _, fallback := range pivotSpec.ValueSelectors[1:] {
					pivot.ValueFallbacks = append(pivot.ValueFallbacks, recipe.Expression{Select: qualifyPivotSelector(alias, pivotSpec.ItemSourcePath, fhirschema.SelectorExpression(fallback))})
				}
			} else {
				if columnSelect == "" {
					return nil, fmt.Errorf("pivot %q discovery did not provide a column selector", pivot.Name)
				}
				pivot.ColumnExpr = recipe.Expression{Select: qualifyDiscoveredSelector(alias, columnSelect)}
			}
		}
		if pivot.ValueExpr.Select == "" && pivot.ValueExpr.Call == "" {
			if valueSelect == "" {
				return nil, fmt.Errorf("pivot %q discovery did not provide a value selector", pivot.Name)
			}
			pivot.ValueExpr = recipe.Expression{Select: qualifyDiscoveredSelector(alias, valueSelect)}
		}
		pivot.Discovery = nil
		resolved = append(resolved, *pivot)
	}
	return resolved, nil
}

func qualifyPivotSelector(alias, itemSource, selector string) string {
	path := strings.TrimSpace(selector)
	if itemSource != "" {
		path = strings.TrimSuffix(itemSource, ".") + "." + path
	}
	return qualifyDiscoveredSelector(alias, path)
}

func relativePivotPath(itemSource, selector string) string {
	itemSource = fhirschema.CanonicalizePath(itemSource)
	selector = fhirschema.CanonicalizePath(selector)
	prefix := itemSource + "."
	if strings.HasPrefix(selector, prefix) {
		return strings.TrimPrefix(selector, prefix)
	}
	return selector
}

func resolveDynamicColumns(ctx context.Context, scope Scope, discovery Discovery, resourceType, alias string, dynamics []recipe.DynamicColumn) error {
	for index := range dynamics {
		dynamic := &dynamics[index]
		if len(dynamic.Columns) > 0 {
			continue
		}
		if dynamic.Key == nil {
			return fmt.Errorf("dynamic column %q has no static columns or key selector", dynamic.Name)
		}
		keySelect := strings.TrimPrefix(strings.TrimSpace(dynamic.Key.Select), "item.")
		source := strings.TrimPrefix(strings.TrimSpace(dynamic.Source.Select), alias+".")
		keyPath := source + "." + keySelect
		candidates, err := discovery.Fields(ctx, scope, resourceType)
		if err != nil {
			return err
		}
		values := map[string]struct{}{}
		for _, candidate := range candidates {
			if candidate.Path != keyPath {
				continue
			}
			if candidate.DistinctTruncated {
				return fmt.Errorf("dynamic column %q key discovery at %q was truncated", dynamic.Name, keyPath)
			}
			for _, value := range candidate.DistinctValues {
				values[value] = struct{}{}
			}
		}
		if dynamic.MaxColumns <= 0 {
			return fmt.Errorf("dynamic column %q requires maxColumns when discovered", dynamic.Name)
		}
		if len(values) > dynamic.MaxColumns {
			return fmt.Errorf("dynamic column %q discovery found %d columns, exceeding maxColumns %d", dynamic.Name, len(values), dynamic.MaxColumns)
		}
		dynamic.Columns = sortedValues(values)
		// A keyed family is optional: a valid FHIR dataset may have no values
		// for an extension/identifier family at all. Keep the declaration with
		// an empty frozen column set so resolution remains schema-stable and the
		// compiler emits no columns for that family instead of rejecting the
		// entire dataframe.
	}
	return nil
}

func qualifyDiscoveredSelector(alias, selector string) string {
	selector = strings.TrimPrefix(strings.TrimSpace(selector), ".")
	if selector == "" || alias == "" || alias == "root" {
		if alias == "root" && selector != "" {
			return "root." + selector
		}
		return selector
	}
	return alias + "." + selector
}

func filterCandidates(candidates []FieldCandidate, set recipe.CatalogProjection) []FieldCandidate {
	kinds := map[string]struct{}{}
	for _, kind := range set.Kinds {
		kinds[kind] = struct{}{}
	}
	selected := make([]FieldCandidate, 0)
	for _, candidate := range candidates {
		if len(kinds) > 0 {
			if _, ok := kinds[candidate.Kind]; !ok {
				continue
			}
		}
		if !matchesAny(candidate.Path, set.IncludePaths) || matchesAny(candidate.Path, set.ExcludePaths) {
			continue
		}
		selected = append(selected, candidate)
	}
	return selected
}

func matchesAny(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, value); err == nil && ok {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(value, strings.TrimSuffix(pattern, "*")) {
			return true
		}
		if value == pattern {
			return true
		}
	}
	return false
}

func projectionName(set recipe.CatalogProjection, fieldPath string) string {
	name := fieldPath
	if set.Naming == recipe.ColumnNamingPathSuffix {
		name = name[strings.LastIndex(name, ".")+1:]
	}
	name = strings.NewReplacer("[]", "", ".", "_", "-", "_", "/", "_").Replace(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func sortedValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func hasCatalogDeclarations(bundle recipe.Bundle) bool {
	for _, output := range bundle.Outputs {
		if len(output.CatalogProjections) > 0 || hasCatalogPivots(output.Pivots) || hasCatalogDynamic(output.DynamicColumns) {
			return true
		}
		for _, traversal := range output.Traversals {
			if traversalHasCatalog(traversal) {
				return true
			}
		}
	}
	return false
}

func traversalHasCatalog(traversal recipe.Traversal) bool {
	return len(traversal.CatalogProjections) > 0 || hasCatalogPivots(traversal.Pivots) || hasCatalogDynamic(traversal.DynamicColumns)
}

func hasCatalogPivots(pivots []recipe.Pivot) bool {
	for _, pivot := range pivots {
		if pivot.Discovery != nil {
			return true
		}
	}
	return false
}

func hasCatalogDynamic(dynamics []recipe.DynamicColumn) bool {
	for _, dynamic := range dynamics {
		if len(dynamic.Columns) == 0 {
			return true
		}
	}
	return false
}

func cloneBundle(bundle recipe.Bundle) (recipe.Bundle, error) {
	data, err := json.Marshal(bundle)
	if err != nil {
		return recipe.Bundle{}, err
	}
	return recipe.Parse(data)
}
