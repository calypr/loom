package queryapi

import (
	"fmt"
	"strings"

	httpapi "github.com/calypr/loom/internal/api/http"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func RecipeBundleFromInput(in dataframemodel.FhirDataframeInput) (recipe.Bundle, error) {
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

func recipeOutputFromInput(resourceType string, fields []*dataframemodel.FhirFieldSelectInput, filters []*dataframemodel.FhirFilterInput, pivots []*dataframemodel.FhirPivotInput, aggregates []*dataframemodel.FhirAggregateInput, slices []*dataframemodel.FhirRepresentativeSliceInput, catalogProjections []*dataframemodel.FhirCatalogProjectionInput, traversals []*dataframemodel.FhirTraversalStepInput) (recipe.Output, error) {
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

func recipeCatalogProjectionsFromInput(in []*dataframemodel.FhirCatalogProjectionInput) ([]recipe.CatalogProjection, error) {
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

func recipeFieldsFromInput(in []*dataframemodel.FhirFieldSelectInput) ([]recipe.Field, error) {
	out := make([]recipe.Field, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		expr, err := recipeSelectorExpression(item.Selector)
		if err != nil {
			return nil, fmt.Errorf("fields[%d] %q: %w", index, item.Name, err)
		}
		field := recipe.Field{Name: item.Name, FieldRef: strings.TrimSpace(httpapi.OptionalString(item.FieldRef)), Expr: expr, ValueMode: recipe.ValueMode(item.ValueMode.String())}
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

func recipeFiltersFromInput(in []*dataframemodel.FhirFilterInput) ([]recipe.Filter, error) {
	out := make([]recipe.Filter, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		filter := recipe.Filter{
			Select:   strings.TrimSpace(item.Select),
			FieldRef: strings.TrimSpace(httpapi.OptionalString(item.FieldRef)),
			Operator: spec.FilterOperator(item.Operator.String()),
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

func recipeFilterValue(input *dataframemodel.FhirFilterValueInput) (spec.FilterValue, error) {
	if input == nil {
		return spec.FilterValue{}, fmt.Errorf("value is required")
	}
	value := spec.FilterValue{Kind: spec.FilterValueKind(input.Kind.String())}
	switch input.Kind {
	case dataframemodel.FhirFilterValueKindString:
		value.String = input.String
	case dataframemodel.FhirFilterValueKindCode:
		if input.Code != nil {
			value.Code = &recipe.CodeValue{System: httpapi.OptionalString(input.Code.System), Code: input.Code.Code, Display: httpapi.OptionalString(input.Code.Display)}
		}
	case dataframemodel.FhirFilterValueKindBoolean:
		value.Boolean = input.Boolean
	case dataframemodel.FhirFilterValueKindInteger:
		if input.Integer != nil {
			converted := int64(*input.Integer)
			value.Integer = &converted
		}
	case dataframemodel.FhirFilterValueKindDecimal:
		value.Decimal = input.Decimal
	case dataframemodel.FhirFilterValueKindDate:
		value.Date = input.Date
	case dataframemodel.FhirFilterValueKindDateTime:
		value.DateTime = input.DateTime
	default:
		return spec.FilterValue{}, fmt.Errorf("unsupported value kind %q", input.Kind)
	}
	if err := value.Validate(); err != nil {
		return spec.FilterValue{}, err
	}
	return value, nil
}

func recipePivotsFromInput(in []*dataframemodel.FhirPivotInput) ([]recipe.Pivot, error) {
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
		pivot := recipe.Pivot{Name: item.Name, FieldRef: strings.TrimSpace(httpapi.OptionalString(item.FieldRef)), ColumnExpr: column, ValueExpr: value, Columns: cloneStrings(item.Columns)}
		if item.Discovery != nil {
			pivot.Discovery = &recipe.PivotDiscovery{Family: httpapi.OptionalString(item.Discovery.Family), Path: httpapi.OptionalString(item.Discovery.Path), MaxColumns: item.Discovery.MaxColumns}
		}
		out = append(out, pivot)
	}
	return out, nil
}

func recipeAggregatesFromInput(in []*dataframemodel.FhirAggregateInput) ([]recipe.Aggregate, error) {
	out := make([]recipe.Aggregate, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		aggregate := recipe.Aggregate{Name: item.Name, Operation: recipe.AggregateOperation(item.Operation.String()), FieldRef: strings.TrimSpace(httpapi.OptionalString(item.FieldRef)), ValueMode: recipe.ValueMode(item.ValueMode.String())}
		path := strings.TrimSpace(httpapi.OptionalString(item.FhirPath))
		if path != "" {
			expr := recipe.Expression{Select: path}
			aggregate.Expr = &expr
		}
		predicatePath := strings.TrimSpace(httpapi.OptionalString(item.PredicatePath))
		predicateValue := httpapi.OptionalString(item.PredicateEquals)
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

func recipeSlicesFromInput(in []*dataframemodel.FhirRepresentativeSliceInput) ([]recipe.RepresentativeSlice, error) {
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
		wherePath := strings.TrimSpace(httpapi.OptionalString(item.WherePath))
		whereValue := httpapi.OptionalString(item.WhereEquals)
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

func recipeTraversalsFromInput(in []*dataframemodel.FhirTraversalStepInput) ([]recipe.Traversal, error) {
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

func recipeSelectorExpression(in *dataframemodel.FhirFieldSelectorInput) (recipe.Expression, error) {
	if in == nil {
		return recipe.Expression{}, fmt.Errorf("selector is required")
	}
	selectText := composeSelector(httpapi.OptionalString(in.SourcePath), predicatePathFromInput(in.Where), predicateOpFromInput(in.Where), predicateValueFromInput(in.Where), in.ValuePath)
	if strings.TrimSpace(selectText) == "" {
		return recipe.Expression{}, fmt.Errorf("selector valuePath is required")
	}
	return recipe.Expression{Select: selectText}, nil
}

func recipeStringEqualityFilter(path, value string) *recipe.Filter {
	valueCopy := value
	return &recipe.Filter{Select: path, Operator: recipe.FilterEquals, Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &valueCopy}}}
}
