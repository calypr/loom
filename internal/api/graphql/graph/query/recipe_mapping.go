package queryapi

// This file is the transport-only GraphQL -> recipe boundary.  It copies the
// already resolved GraphQL selections into the canonical recipe wire types;
// semantic validation and compilation remain in the recipe/semantic packages.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// RecipeBundleFromInput creates the ephemeral one-output recipe used by a
// one-shot GraphQL dataframe request.  Runtime scope (project, generation,
// authorization, and limit) is deliberately not copied into the bundle; the
// caller supplies it through recipe.RuntimeBindings.
//
// Field references must have been resolved by PrepareRunInput first.  Keeping
// that boundary explicit prevents an unresolved catalog name from becoming an
// executable selector or an accidental storage-specific path.
func RecipeBundleFromInput(in model.FhirDataframeInput) (recipe.Bundle, error) {
	if strings.TrimSpace(in.RootResourceType) == "" {
		return recipe.Bundle{}, fmt.Errorf("rootResourceType is required")
	}
	grain, ok := spec.InferRowGrain(in.RootResourceType)
	if in.RowGrain != nil {
		grain = spec.RowGrain(strings.TrimSpace(*in.RowGrain))
		ok = true
	}
	if !ok || strings.TrimSpace(string(grain)) == "" {
		return recipe.Bundle{}, fmt.Errorf("no row grain is available for root resource type %q", in.RootResourceType)
	}
	output, err := recipeOutputFromInput(in.RootResourceType, in.RootFields, in.RootFilters, in.RootPivots, in.RootAggregates, in.RootSlices, in.RootCatalogProjections, in.Traverse)
	if err != nil {
		return recipe.Bundle{}, err
	}
	output.Name = "dataframe"
	output.RowGrain = string(grain)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "graphql_request",
		TranslationVersion:  "graphql-request",
		Outputs:             []recipe.Output{output},
	}
	if err := bundle.Validate(); err != nil {
		return recipe.Bundle{}, err
	}
	return bundle, nil
}

func recipeOutputFromInput(resourceType string, fields []*model.FhirFieldSelectInput, filters []*model.FhirFilterInput, pivots []*model.FhirPivotInput, aggregates []*model.FhirAggregateInput, slices []*model.FhirRepresentativeSliceInput, catalogProjections []*model.FhirCatalogProjectionInput, traversals []*model.FhirTraversalStepInput) (recipe.Output, error) {
	rf, err := recipeFieldsFromInput(fields)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("root fields: %w", err)
	}
	rfilt, err := recipeFiltersFromInput(filters)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("root filters: %w", err)
	}
	rp, err := recipePivotsFromInput(pivots)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("root pivots: %w", err)
	}
	ra, err := recipeAggregatesFromInput(aggregates)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("root aggregates: %w", err)
	}
	rs, err := recipeSlicesFromInput(slices)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("root slices: %w", err)
	}
	rcp, err := recipeCatalogProjectionsFromInput(catalogProjections)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("catalog projections: %w", err)
	}
	rt, err := recipeTraversalsFromInput(traversals)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("traversals: %w", err)
	}
	return recipe.Output{RootResourceType: resourceType, Fields: rf, Filters: rfilt, Pivots: rp, Aggregates: ra, Slices: rs, CatalogProjections: rcp, Traversals: rt}, nil
}

func recipeCatalogProjectionsFromInput(in []*model.FhirCatalogProjectionInput) ([]recipe.CatalogProjection, error) {
	out := make([]recipe.CatalogProjection, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		projection := recipe.CatalogProjection{
			Name: item.Name, IncludePaths: cloneStrings(item.IncludePaths), ExcludePaths: cloneStrings(item.ExcludePaths),
			Kinds: cloneStrings(item.Kinds), Naming: recipe.ColumnNaming(item.Naming.String()), ValueMode: recipe.ValueMode(item.ValueMode.String()), MaxColumns: item.MaxColumns,
		}
		if err := projection.Validate(); err != nil {
			return nil, fmt.Errorf("catalogProjections[%d] %q: %w", index, item.Name, err)
		}
		out = append(out, projection)
	}
	return out, nil
}

func recipeFieldsFromInput(in []*model.FhirFieldSelectInput) ([]recipe.Field, error) {
	out := make([]recipe.Field, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		expr, err := recipeSelectorExpression(item.Selector)
		if err != nil {
			return nil, fmt.Errorf("fields[%d] %q: %w", index, item.Name, err)
		}
		field := recipe.Field{Name: item.Name, FieldRef: strings.TrimSpace(derefString(item.FieldRef)), Expr: expr, ValueMode: recipe.ValueMode(item.ValueMode.String())}
		for fallbackIndex, fallback := range item.FallbackSelectors {
			fallbackExpr, err := recipeSelectorExpression(fallback)
			if err != nil {
				return nil, fmt.Errorf("fields[%d] %q fallback[%d]: %w", index, item.Name, fallbackIndex, err)
			}
			field.Fallbacks = append(field.Fallbacks, fallbackExpr)
		}
		if len(item.FallbackFieldRefs) > 0 && len(item.FallbackFieldRefs) != len(field.Fallbacks) {
			return nil, fmt.Errorf("fields[%d] %q has unresolved fallback field references", index, item.Name)
		}
		out = append(out, field)
	}
	return out, nil
}

func recipeFiltersFromInput(in []*model.FhirFilterInput) ([]recipe.Filter, error) {
	out := make([]recipe.Filter, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		filter := recipe.Filter{
			Select:   strings.TrimSpace(item.Select),
			FieldRef: strings.TrimSpace(derefString(item.FieldRef)),
			Operator: recipe.FilterOperator(item.Operator.String()),
		}
		if item.Quantifier != nil {
			filter.Quantifier = recipe.ArrayQuantifier(item.Quantifier.String())
		}
		for valueIndex, value := range item.Values {
			converted, err := recipeFilterValue(value)
			if err != nil {
				return nil, fmt.Errorf("filters[%d].values[%d]: %w", index, valueIndex, err)
			}
			filter.Values = append(filter.Values, converted)
		}
		out = append(out, filter)
	}
	return out, nil
}

func recipeFilterValue(input *model.FhirFilterValueInput) (recipe.FilterValue, error) {
	if input == nil {
		return recipe.FilterValue{}, fmt.Errorf("value is required")
	}
	value := recipe.FilterValue{Kind: recipe.FilterValueKind(input.Kind.String())}
	switch input.Kind {
	case model.FhirFilterValueKindString:
		value.String = input.String
	case model.FhirFilterValueKindCode:
		if input.Code != nil {
			value.Code = &recipe.CodeValue{System: derefString(input.Code.System), Code: input.Code.Code, Display: derefString(input.Code.Display)}
		}
	case model.FhirFilterValueKindBoolean:
		value.Boolean = input.Boolean
	case model.FhirFilterValueKindInteger:
		if input.Integer != nil {
			converted := int64(*input.Integer)
			value.Integer = &converted
		}
	case model.FhirFilterValueKindDecimal:
		value.Decimal = input.Decimal
	case model.FhirFilterValueKindDate:
		value.Date = input.Date
	case model.FhirFilterValueKindDateTime:
		value.DateTime = input.DateTime
	default:
		return recipe.FilterValue{}, fmt.Errorf("unsupported value kind %q", input.Kind)
	}
	if err := value.Validate(); err != nil {
		return recipe.FilterValue{}, err
	}
	return value, nil
}

func recipePivotsFromInput(in []*model.FhirPivotInput) ([]recipe.Pivot, error) {
	out := make([]recipe.Pivot, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		column, err := recipeSelectorExpression(item.ColumnSelector)
		if err != nil {
			return nil, fmt.Errorf("pivots[%d] %q columnSelector: %w", index, item.Name, err)
		}
		value, err := recipeSelectorExpression(item.ValueSelector)
		if err != nil {
			return nil, fmt.Errorf("pivots[%d] %q valueSelector: %w", index, item.Name, err)
		}
		pivot := recipe.Pivot{Name: item.Name, FieldRef: strings.TrimSpace(derefString(item.FieldRef)), ColumnExpr: column, ValueExpr: value, Columns: cloneStrings(item.Columns)}
		if item.Discovery != nil {
			pivot.Discovery = &recipe.PivotDiscovery{Family: derefString(item.Discovery.Family), Path: derefString(item.Discovery.Path), MaxColumns: item.Discovery.MaxColumns}
		}
		out = append(out, pivot)
	}
	return out, nil
}

func recipeAggregatesFromInput(in []*model.FhirAggregateInput) ([]recipe.Aggregate, error) {
	out := make([]recipe.Aggregate, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		aggregate := recipe.Aggregate{Name: item.Name, Operation: recipe.AggregateOperation(item.Operation.String()), FieldRef: strings.TrimSpace(derefString(item.FieldRef)), ValueMode: recipe.ValueMode(item.ValueMode.String())}
		path := strings.TrimSpace(derefString(item.FhirPath))
		if path != "" {
			expr := recipe.Expression{Select: path}
			aggregate.Expr = &expr
		}
		predicatePath := strings.TrimSpace(derefString(item.PredicatePath))
		predicateValue := derefString(item.PredicateEquals)
		if predicatePath != "" || strings.TrimSpace(predicateValue) != "" {
			if predicatePath == "" || strings.TrimSpace(predicateValue) == "" {
				return nil, fmt.Errorf("aggregates[%d] %q has an incomplete predicate", index, item.Name)
			}
			aggregate.Where = recipeStringEqualityFilter(predicatePath, predicateValue)
		}
		out = append(out, aggregate)
	}
	return out, nil
}

func recipeSlicesFromInput(in []*model.FhirRepresentativeSliceInput) ([]recipe.RepresentativeSlice, error) {
	out := make([]recipe.RepresentativeSlice, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		fields, err := recipeFieldsFromInput(item.Fields)
		if err != nil {
			return nil, fmt.Errorf("slices[%d] %q fields: %w", index, item.Name, err)
		}
		slice := recipe.RepresentativeSlice{Name: item.Name, Limit: item.Limit, Fields: fields}
		wherePath := strings.TrimSpace(derefString(item.WherePath))
		whereValue := derefString(item.WhereEquals)
		if wherePath != "" || strings.TrimSpace(whereValue) != "" {
			if wherePath == "" || strings.TrimSpace(whereValue) == "" {
				return nil, fmt.Errorf("slices[%d] %q has an incomplete predicate", index, item.Name)
			}
			slice.Where = recipeStringEqualityFilter(wherePath, whereValue)
		}
		out = append(out, slice)
	}
	return out, nil
}

func recipeTraversalsFromInput(in []*model.FhirTraversalStepInput) ([]recipe.Traversal, error) {
	out := make([]recipe.Traversal, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		output, err := recipeOutputFromInput(item.ToResourceType, item.Fields, item.Filters, item.Pivots, item.Aggregates, item.Slices, item.CatalogProjections, item.Traverse)
		if err != nil {
			return nil, fmt.Errorf("traversals[%d] %q: %w", index, item.Alias, err)
		}
		matchMode := recipe.MatchOptional
		if item.MatchMode != nil {
			matchMode = recipe.TraversalMatchMode(item.MatchMode.String())
		}
		out = append(out, recipe.Traversal{Name: item.EdgeLabel, ToResourceType: item.ToResourceType, Alias: item.Alias, MatchMode: matchMode, Fields: output.Fields, Filters: output.Filters, Pivots: output.Pivots, Aggregates: output.Aggregates, Slices: output.Slices, CatalogProjections: output.CatalogProjections, Traversals: output.Traversals})
	}
	return out, nil
}

func recipeSelectorExpression(in *model.FhirFieldSelectorInput) (recipe.Expression, error) {
	if in == nil {
		return recipe.Expression{}, fmt.Errorf("selector is required")
	}
	selectText := composeSelector(derefString(in.SourcePath), predicatePathFromInput(in.Where), predicateOpFromInput(in.Where), predicateValueFromInput(in.Where), in.ValuePath)
	if strings.TrimSpace(selectText) == "" {
		return recipe.Expression{}, fmt.Errorf("selector valuePath is required")
	}
	return recipe.Expression{Select: selectText}, nil
}

func recipeStringEqualityFilter(path, value string) *recipe.Filter {
	valueCopy := value
	return &recipe.Filter{Select: path, Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &valueCopy}}}
}
