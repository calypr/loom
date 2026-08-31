package semantic

// This file is the single semantic boundary for persisted recipes and the
// existing GraphQL dataframe request. It deliberately stops before physical
// lowering: no collection, AQL, SQL, or backend implementation detail belongs
// in these types.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func buildRecipeOutput(output recipe.Output, bindings recipe.RuntimeBindings) (OutputPlan, error) {
	if !fhirschema.HasResource(output.RootResourceType) {
		return OutputPlan{}, fmt.Errorf("root resource type %q is not represented by the active generated FHIR schema", output.RootResourceType)
	}
	grain := spec.RowGrain(output.RowGrain)
	if err := spec.ValidateRootGrain(output.RootResourceType, grain); err != nil {
		// Persisted recipes may introduce a product-specific grain when they
		// also declare the row-shaping operation and an explicit identity. The
		// GraphQL request contract remains strict and continues to use
		// ValidateRootGrain above.
		if output.Expand == nil || output.Identity == nil || !validCustomGrain(string(grain)) {
			return OutputPlan{}, err
		}
	}
	scope := newRootScope(output.RootResourceType)
	if output.Expand != nil {
		// The source is checked in the parent lexical scope first, then its
		// selector path becomes the prefix for the expansion item alias.
		from, err := scope.expression(output.Expand.From, "expand.from")
		if err != nil {
			return OutputPlan{}, err
		}
		if from.Expression.Selector == nil || from.Type.Cardinality != expression.Many {
			return OutputPlan{}, fmt.Errorf("expand.from must be a repeated selector")
		}
		ref := from.Expression.Selector
		binding, err := scopeBindingForSelector(scope, *ref)
		if err != nil {
			return OutputPlan{}, err
		}
		prefix := binding.Prefix
		if prefix != "" {
			prefix += "."
		}
		// The expansion alias denotes one item, not the repeated collection.
		// An explicit index keeps schema cardinality scalar while retaining the
		// canonical array path for generated metadata.
		prefix += strings.TrimSuffix(strings.TrimPrefix(ref.Path, "."), "[]") + "[0]"
		scope, err = scope.child(output.Expand.As, scopeBinding{ResourceType: binding.ResourceType, Prefix: prefix, ExpandedItem: true})
		if err != nil {
			return OutputPlan{}, err
		}
		unnest := &SemanticUnnest{Source: from, As: output.Expand.As, JoinMode: UnnestInner}
		if err := unnest.Validate(); err != nil {
			return OutputPlan{}, fmt.Errorf("expand: %w", err)
		}
		plan := OutputPlan{Name: output.Name, RootResourceType: output.RootResourceType, RowGrain: grain, RootColumnNaming: output.RootColumnNaming.Normalized(), TraversalColumnNaming: output.TraversalColumnNaming.Normalized(), Collision: output.CollisionPolicy, Unnest: unnest}
		return finishRecipeOutput(plan, output, scope)
	}
	plan := OutputPlan{Name: output.Name, RootResourceType: output.RootResourceType, RowGrain: grain, RootColumnNaming: output.RootColumnNaming.Normalized(), TraversalColumnNaming: output.TraversalColumnNaming.Normalized(), Collision: output.CollisionPolicy}
	return finishRecipeOutput(plan, output, scope)
}

func validCustomGrain(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (index > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func finishRecipeOutput(plan OutputPlan, output recipe.Output, scope scopeFrame) (OutputPlan, error) {
	if plan.Collision == "" {
		plan.Collision = "error"
	}
	plan.CatalogProjections = make([]string, 0, len(output.CatalogProjections))
	for _, projection := range output.CatalogProjections {
		plan.CatalogProjections = append(plan.CatalogProjections, projection.Name)
	}
	plan.Root = SemanticNode{Alias: "root", ResourceType: output.RootResourceType, Fields: make([]SemanticField, 0, len(output.Fields))}
	rootFilters, err := LowerRecipeFilters(output.RootResourceType, output.Filters)
	if err != nil {
		return OutputPlan{}, fmt.Errorf("root filters: %w", err)
	}
	plan.Root.Filters = rootFilters
	plan.Root.Pivots, plan.Root.Aggregates, plan.Root.Slices, err = lowerRecipeRichShaping(output.RootResourceType, "root", scope, output.Pivots, output.Aggregates, output.Slices)
	if err != nil {
		return OutputPlan{}, fmt.Errorf("root rich shaping: %w", err)
	}
	for index, field := range output.Fields {
		normalized, err := normalizeRecipeProjection(field, scope, fmt.Sprintf("fields[%d]", index))
		if err != nil {
			return OutputPlan{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		plan.Root.Fields = append(plan.Root.Fields, normalized)
	}
	for index, traversal := range output.Traversals {
		child, err := buildRecipeTraversal(traversal, scope, fmt.Sprintf("traversals[%d]", index))
		if err != nil {
			return OutputPlan{}, err
		}
		plan.Root.Children = append(plan.Root.Children, child)
	}
	if output.Identity != nil {
		x, err := scope.expression(output.Identity.Expr, "identity.expr")
		if err != nil {
			return OutputPlan{}, err
		}
		if x.Type.Cardinality == expression.Many || x.Type.Kind == expression.KindObject || x.Type.Kind == expression.KindNull {
			return OutputPlan{}, fmt.Errorf("identity expression must resolve to one scalar value")
		}
		plan.Identity = &x
	}
	dynamicMaps, err := buildRecipeDynamicMaps(output.DynamicColumns, scope, "dynamicColumns", "", output.RootResourceType)
	if err != nil {
		return OutputPlan{}, err
	}
	plan.DynamicMaps = append(plan.DynamicMaps, dynamicMaps...)
	plan.Root.DynamicMaps = append(plan.Root.DynamicMaps, dynamicMaps...)
	extensionMaps, err := buildRecipeExtensionMaps(output.ExtensionColumns, scope, "extensionColumns", "", output.RootResourceType)
	if err != nil {
		return OutputPlan{}, err
	}
	plan.DynamicMaps = append(plan.DynamicMaps, extensionMaps...)
	plan.Root.DynamicMaps = append(plan.Root.DynamicMaps, extensionMaps...)
	return plan, nil
}

func buildRecipeTraversal(input recipe.Traversal, parent scopeFrame, path string) (SemanticNode, error) {
	if !fhirschema.HasResource(input.ToResourceType) {
		return SemanticNode{}, fmt.Errorf("%s: target resource type %q is not represented by the active generated FHIR schema", path, input.ToResourceType)
	}
	alias := input.Alias
	if strings.TrimSpace(alias) == "" {
		alias = input.Name
	}
	input = qualifyTraversalLocals(input, alias, parent.aliases)
	scope, err := parent.child(alias, scopeBinding{ResourceType: input.ToResourceType})
	if err != nil {
		return SemanticNode{}, fmt.Errorf("%s: %w", path, err)
	}
	matchMode, err := NormalizeRecipeMatchMode(input.MatchMode)
	if err != nil {
		return SemanticNode{}, fmt.Errorf("%s.matchMode: %w", path, err)
	}
	node := SemanticNode{Alias: alias, ResourceType: input.ToResourceType, EdgeLabel: input.Name, MatchMode: matchMode}
	dynamicMaps, err := buildRecipeDynamicMaps(input.DynamicColumns, scope, path+".dynamicColumns", alias, input.ToResourceType)
	if err != nil {
		return SemanticNode{}, err
	}
	node.DynamicMaps = dynamicMaps
	extensionMaps, err := buildRecipeExtensionMaps(input.ExtensionColumns, scope, path+".extensionColumns", alias, input.ToResourceType)
	if err != nil {
		return SemanticNode{}, err
	}
	node.DynamicMaps = append(node.DynamicMaps, extensionMaps...)
	node.Filters, err = LowerRecipeFiltersForAlias(input.ToResourceType, alias, input.Filters)
	if err != nil {
		return SemanticNode{}, fmt.Errorf("%s.filters: %w", path, err)
	}
	node.Pivots, node.Aggregates, node.Slices, err = lowerRecipeRichShaping(input.ToResourceType, alias, scope, input.Pivots, input.Aggregates, input.Slices)
	if err != nil {
		return SemanticNode{}, fmt.Errorf("%s rich shaping: %w", path, err)
	}
	if input.From != nil {
		x, err := parent.expression(*input.From, path+".from")
		if err != nil {
			return SemanticNode{}, err
		}
		node.From = &x
	}
	for index, field := range input.Fields {
		normalized, err := normalizeRecipeProjection(field, scope, fmt.Sprintf("%s.fields[%d]", path, index))
		if err != nil {
			return SemanticNode{}, err
		}
		node.Fields = append(node.Fields, normalized)
	}
	for index, child := range input.Traversals {
		nested, err := buildRecipeTraversal(child, scope, fmt.Sprintf("%s.traversals[%d]", path, index))
		if err != nil {
			return SemanticNode{}, err
		}
		node.Children = append(node.Children, nested)
	}
	return node, nil
}

func buildRecipeExtensionMaps(items []recipe.ExtensionColumn, scope scopeFrame, path, scopeAlias, resourceType string) ([]SemanticDynamicMap, error) {
	result := make([]SemanticDynamicMap, 0)
	for index, item := range items {
		if item.Columns == nil {
			return nil, fmt.Errorf("%s[%d] extension column %q is unresolved; resolve it through schema discovery first", path, index, item.Name)
		}
		if len(item.Columns) == 0 {
			continue
		}
		source, err := scope.expression(item.Source, fmt.Sprintf("%s[%d].source", path, index))
		if err != nil {
			return nil, err
		}
		selector := source.Expression.Selector
		if selector == nil || source.Type.Cardinality != expression.Many {
			return nil, fmt.Errorf("%s[%d].source must be a repeated Extension selector", path, index)
		}
		binding, ok := scope.aliases[selector.Context]
		if selector.Context == "" {
			binding, ok = scope.aliases["root"]
		}
		if !ok {
			return nil, fmt.Errorf("%s[%d].source context %q is not in scope", path, index, selector.Context)
		}
		selectorPath := strings.TrimPrefix(strings.TrimSpace(selector.Path), ".")
		if prefix := strings.TrimSuffix(binding.Prefix, "."); prefix != "" && strings.HasPrefix(selectorPath, prefix+".") {
			selectorPath = strings.TrimPrefix(selectorPath, prefix+".")
		}
		resolved, valid := fhirschema.ResolvePath(binding.ResourceType, selectorPath)
		if !valid || resolved.PropertyRef != "Extension" {
			return nil, fmt.Errorf("%s[%d].source must select a schema-valid repeated Extension", path, index)
		}
		for _, mapping := range item.Columns {
			sourceExpr := item.Source
			if mapping.SourcePath != "" {
				prefix := "root"
				if scopeAlias != "" {
					prefix = scopeAlias
				}
				sourceExpr = recipe.Expression{Select: prefix + "." + strings.TrimPrefix(mapping.SourcePath, ".")}
			}
			value := recipe.Expression{Select: "item." + mapping.ValuePath}
			if mapping.ValuePath == "" {
				value = recipe.Expression{Call: "canonical_json", Args: []recipe.Expression{{Document: &recipe.DocumentRef{Context: "item"}}}}
			}
			key := recipe.Expression{Call: "sanitize_name", Args: []recipe.Expression{{Call: "last_segment", Args: []recipe.Expression{{Select: "item.url"}}}}}
			rawSourceKey := strings.TrimRight(strings.TrimSpace(mapping.URL), "/")
			if split := strings.LastIndexAny(rawSourceKey, "/#"); split >= 0 {
				rawSourceKey = rawSourceKey[split+1:]
			}
			sourceKey := sanitize(rawSourceKey)
			if sourceKey == "" {
				return nil, fmt.Errorf("%s[%d] extension URL %q has no usable column key", path, index, mapping.URL)
			}
			dynamic := recipe.DynamicColumn{
				Name: item.Name + "__" + mapping.Name, ColumnPrefix: item.ColumnPrefix,
				Source: sourceExpr, Key: &key, Value: &value, Columns: []string{mapping.Name}, MaxColumns: item.MaxColumns,
				ColumnTypes: map[string]string{mapping.Name: mapping.ValueType}, ColumnSourceKeys: map[string]string{mapping.Name: sourceKey},
				Discovered: true,
			}
			maps, err := buildRecipeDynamicMaps([]recipe.DynamicColumn{dynamic}, scope, path, scopeAlias, resourceType)
			if err != nil {
				return nil, err
			}
			for index := range maps {
				// Each extension mapping projects one URL from an Extension array.
				// Other URLs in the same array are valid siblings and may have their
				// own independently discovered mappings.
				maps[index].AllowUnknownKeys = true
			}
			result = append(result, maps...)
		}
	}
	return result, nil
}

func buildRecipeDynamicMaps(items []recipe.DynamicColumn, scope scopeFrame, path, scopeAlias, resourceType string) ([]SemanticDynamicMap, error) {
	result := make([]SemanticDynamicMap, 0, len(items))
	for index, dynamic := range items {
		columns := []string(nil)
		if dynamic.Columns != nil {
			// Preserve non-nil empty slices: the resolver uses that distinction
			// to mark an optional family as resolved with zero discovered keys.
			columns = append([]string{}, dynamic.Columns...)
		}
		item := SemanticDynamicMap{Name: dynamic.Name, ScopeAlias: scopeAlias, ResourceType: resourceType, Columns: columns, MaxColumns: dynamic.MaxColumns, ColumnTypes: dynamic.ColumnTypes, ColumnSourceKeys: dynamic.ColumnSourceKeys, AllowUnknownKeys: len(dynamic.ColumnSourceKeys) > 0, Discovered: dynamic.Discovered}
		if dynamic.ColumnPrefix != nil {
			prefix := *dynamic.ColumnPrefix
			item.ColumnPrefix = &prefix
		}
		var err error
		item.Source, err = scope.expression(dynamic.Source, fmt.Sprintf("%s[%d].source", path, index))
		if err != nil {
			return nil, err
		}
		if item.Source.Type.Cardinality != expression.Many {
			return nil, fmt.Errorf("%s[%d].source must be repeated", path, index)
		}
		dynamicScope := scope
		if selector := item.Source.Expression.Selector; selector != nil && strings.Contains(selector.Path, "[]") {
			binding, scopeErr := scopeBindingForSelector(scope, *selector)
			if scopeErr != nil {
				return nil, scopeErr
			}
			selectorPath := strings.TrimPrefix(strings.TrimSpace(selector.Path), ".")
			prefix := strings.TrimPrefix(binding.Prefix+"."+selectorPath, ".")
			dynamicScope, err = scope.child("item", scopeBinding{ResourceType: binding.ResourceType, Prefix: prefix, ExpandedItem: true})
			if err != nil {
				return nil, fmt.Errorf("%s[%d] item scope: %w", path, index, err)
			}
		}
		if dynamic.Key != nil {
			x, err := dynamicScope.expression(*dynamic.Key, fmt.Sprintf("%s[%d].key", path, index))
			if err != nil {
				return nil, err
			}
			if x.Type.Cardinality == expression.Many || (x.Type.Kind != expression.KindString && x.Type.Kind != expression.KindCode) {
				return nil, fmt.Errorf("%s[%d].key must be a scalar string or code", path, index)
			}
			item.Key = &x
		}
		if dynamic.Value != nil {
			x, err := dynamicScope.expression(*dynamic.Value, fmt.Sprintf("%s[%d].value", path, index))
			if err != nil {
				return nil, err
			}
			item.Value = &x
		}
		result = append(result, item)
	}
	return result, nil
}

func scopeBindingForSelector(scope scopeFrame, ref expression.SelectorRef) (scopeBinding, error) {
	alias := ref.Context
	if alias == "" {
		alias = "root"
	}
	binding, ok := scope.aliases[alias]
	if !ok {
		return scopeBinding{}, fmt.Errorf("selector context %q is not in scope", alias)
	}
	return binding, nil
}

func cloneBindings(in recipe.RuntimeBindings) recipe.RuntimeBindings {
	in.AuthResourcePaths = append([]string(nil), in.AuthResourcePaths...)
	in.OutputNames = append([]string(nil), in.OutputNames...)
	return in
}

func keys(values map[string]scopeBinding) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for key := range values {
		result[key] = struct{}{}
	}
	return result
}
