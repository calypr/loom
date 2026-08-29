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

	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
			name := uniqueProjectionName(set, candidate.Path, seen)
			seen[name] = struct{}{}
			selectPath := strings.TrimPrefix(candidate.Path, ".")
			if alias != "" && alias != "root" {
				selectPath = alias + "." + selectPath
			} else {
				selectPath = "root." + selectPath
			}
			fields = append(fields, recipe.Field{Name: name, FieldRef: candidate.Path, ValueMode: set.ValueMode, Expr: recipe.Expression{Select: selectPath}, Discovered: true})
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
		pivot.Discovered = true
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
		wasUnresolved := len(dynamic.Columns) == 0
		if len(dynamic.Columns) > 0 {
			continue
		}
		if dynamic.Key == nil {
			return fmt.Errorf("dynamic column %q has no static columns or key selector", dynamic.Name)
		}
		source, keySelect, transforms, err := dynamicDiscoveryKey(*dynamic, alias)
		if err != nil {
			return fmt.Errorf("dynamic column %q key discovery: %w", dynamic.Name, err)
		}
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
				transformed, err := applyDynamicKeyTransforms(value, transforms)
				if err != nil {
					return fmt.Errorf("dynamic column %q key %q: %w", dynamic.Name, value, err)
				}
				if transformed != "" {
					values[transformed] = struct{}{}
				}
			}
		}
		if dynamic.MaxColumns <= 0 {
			return fmt.Errorf("dynamic column %q requires maxColumns when discovered", dynamic.Name)
		}
		if len(values) > dynamic.MaxColumns {
			return fmt.Errorf("dynamic column %q discovery found %d columns, exceeding maxColumns %d", dynamic.Name, len(values), dynamic.MaxColumns)
		}
		dynamic.Columns = sortedValues(values)
		if wasUnresolved {
			dynamic.Discovered = true
		}
		// A keyed family is optional: a valid FHIR dataset may have no values
		// for an extension/identifier family at all. Keep the declaration with
		// an empty frozen column set so resolution remains schema-stable and the
		// compiler emits no columns for that family instead of rejecting the
		// entire dataframe.
	}
	return nil
}

// dynamicDiscoveryKey derives the catalog path that supplies raw dynamic keys
// and the deterministic transforms the physical key expression applies. The
// catalog stores only observed scalar paths, so a transformed key (for
// example last_segment(item.url)) must be frozen from the transformed catalog
// values; freezing the original URL would make physical map lookups miss.
func dynamicDiscoveryKey(dynamic recipe.DynamicColumn, alias string) (string, string, []string, error) {
	source := strings.TrimPrefix(strings.TrimSpace(dynamic.Source.Select), alias+".")
	if source == "" {
		return "", "", nil, fmt.Errorf("source must be a selector")
	}
	if dynamic.Key == nil {
		return "", "", nil, fmt.Errorf("key selector is required")
	}
	key, transforms, err := dynamicKeySelector(*dynamic.Key)
	if err != nil {
		return "", "", nil, err
	}
	return source, key, transforms, nil
}

func dynamicKeySelector(expr recipe.Expression) (string, []string, error) {
	if expr.Select != "" {
		key := strings.TrimPrefix(strings.TrimSpace(expr.Select), "item.")
		if key == "" || key == strings.TrimSpace(expr.Select) {
			return "", nil, fmt.Errorf("key must be rooted at item")
		}
		return key, nil, nil
	}
	name := strings.ToLower(strings.TrimSpace(expr.Call))
	switch name {
	case "last_segment", "basename", "path_segment", "sanitize_name", "sanitize_graphql_name":
		if len(expr.Args) != 1 {
			return "", nil, fmt.Errorf("call %q requires one argument", expr.Call)
		}
		key, transforms, err := dynamicKeySelector(expr.Args[0])
		if err != nil {
			return "", nil, err
		}
		return key, append(transforms, name), nil
	default:
		return "", nil, fmt.Errorf("key must be an item selector or a supported key-normalization call")
	}
}

func applyDynamicKeyTransforms(value string, transforms []string) (string, error) {
	for _, transform := range transforms {
		switch transform {
		case "last_segment", "basename", "path_segment":
			value = strings.TrimRight(value, "/")
			parts := strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '#' })
			if len(parts) == 0 {
				return "", nil
			}
			value = parts[len(parts)-1]
		case "sanitize_name", "sanitize_graphql_name":
			var name strings.Builder
			for _, r := range value {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
					name.WriteRune(r)
				} else {
					name.WriteByte('_')
				}
			}
			value = name.String()
			if value == "" {
				value = "_"
			} else if value[0] >= '0' && value[0] <= '9' {
				value = "_" + value
			}
			if strings.HasPrefix(value, "__") {
				value = "_" + strings.TrimPrefix(value, "__")
			}
		default:
			return "", fmt.Errorf("unsupported key transform %q", transform)
		}
	}
	return value, nil
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

// uniqueProjectionName preserves the established PATH column spelling when it
// is unambiguous. FHIR catalogs may contain both scalar and repeated variants
// of a path (for example author.reference and author[].reference), which
// normalize to the same SQL-safe name. Keep both catalog-backed fields rather
// than rejecting an otherwise valid, dataset-independent recipe.
func uniqueProjectionName(set recipe.CatalogProjection, fieldPath string, used map[string]struct{}) string {
	base := projectionName(set, fieldPath)
	if _, exists := used[base]; !exists {
		return base
	}
	suffix := "__alternate"
	if strings.Contains(fieldPath, "[]") {
		suffix = "__repeated"
	}
	name := base + suffix
	for index := 2; ; index++ {
		if _, exists := used[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s%s_%d", base, suffix, index)
	}
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
		if len(output.CatalogProjections) > 0 || hasCatalogPivots(output.Pivots) || hasCatalogDynamic(output.DynamicColumns) || len(output.ExtensionColumns) > 0 {
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
	return len(traversal.CatalogProjections) > 0 || hasCatalogPivots(traversal.Pivots) || hasCatalogDynamic(traversal.DynamicColumns) || len(traversal.ExtensionColumns) > 0
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
