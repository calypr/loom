package recipe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func predicatePathFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Path
}

func predicateOpFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Op.String()
}

func predicateValueFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Value
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func composeSelector(sourcePath string, wherePath string, whereOp string, whereValue string, valuePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	valuePath = strings.TrimSpace(valuePath)
	if valuePath == "" {
		return ""
	}
	path := valuePath
	if sourcePath != "" {
		path = sourcePath + "." + valuePath
	}
	wherePath = strings.TrimSpace(wherePath)
	whereOp = strings.TrimSpace(strings.ToUpper(whereOp))
	whereValue = strings.TrimSpace(whereValue)
	if wherePath == "" || whereOp == "" || whereValue == "" {
		return path
	}
	if whereOp == "CONTAINS" {
		return fmt.Sprintf(`%s where %s contains %q`, path, wherePath, whereValue)
	}
	return path
}

func (in FhirDataframeInput) Bundle() (Bundle, error) {
	if strings.TrimSpace(in.RootResourceType) == "" {
		return Bundle{}, fmt.Errorf("rootResourceType is required")
	}
	grain, ok := spec.InferRowGrain(in.RootResourceType)
	if in.RowGrain != nil {
		grain = spec.RowGrain(strings.TrimSpace(*in.RowGrain))
		ok = true
	}
	if !ok || strings.TrimSpace(string(grain)) == "" {
		return Bundle{}, fmt.Errorf("no row grain is available for root resource type %q", in.RootResourceType)
	}
	output, err := recipeOutputFromInput(in.RootResourceType, in.RootFields, in.RootFilters, in.RootPivots, in.RootAggregates, in.RootSlices, in.RootCatalogProjections, in.Traverse)
	if err != nil {
		return Bundle{}, err
	}
	output.Name = "dataframe"
	output.RowGrain = string(grain)
	bundle := Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion,
		Name:                "graphql_request",
		TranslationVersion:  "graphql-request",
		Outputs:             []Output{output},
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func recipeOutputFromInput(resourceType string, fields []*FhirFieldSelectInput, filters []*FhirFilterInput, pivots []*FhirPivotInput, aggregates []*FhirAggregateInput, slices []*FhirRepresentativeSliceInput, catalogProjections []*FhirCatalogProjectionInput, traversals []*FhirTraversalStepInput) (Output, error) {
	rf, err := recipeFieldsFromInput(fields)
	if err != nil {
		return Output{}, fmt.Errorf("root fields: %w", err)
	}
	rfilt, err := recipeFiltersFromInput(filters)
	if err != nil {
		return Output{}, fmt.Errorf("root filters: %w", err)
	}
	rp, err := recipePivotsFromInput(pivots)
	if err != nil {
		return Output{}, fmt.Errorf("root pivots: %w", err)
	}
	ra, err := recipeAggregatesFromInput(aggregates)
	if err != nil {
		return Output{}, fmt.Errorf("root aggregates: %w", err)
	}
	rs, err := recipeSlicesFromInput(slices)
	if err != nil {
		return Output{}, fmt.Errorf("root slices: %w", err)
	}
	rcp, err := recipeCatalogProjectionsFromInput(catalogProjections)
	if err != nil {
		return Output{}, fmt.Errorf("catalog projections: %w", err)
	}
	rt, err := recipeTraversalsFromInput(traversals)
	if err != nil {
		return Output{}, fmt.Errorf("traversals: %w", err)
	}
	return Output{RootResourceType: resourceType, Fields: rf, Filters: rfilt, Pivots: rp, Aggregates: ra, Slices: rs, CatalogProjections: rcp, Traversals: rt}, nil
}

func recipeCatalogProjectionsFromInput(in []*FhirCatalogProjectionInput) ([]CatalogProjection, error) {
	out := make([]CatalogProjection, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		projection := CatalogProjection{
			Name: item.Name, IncludePaths: cloneStrings(item.IncludePaths), ExcludePaths: cloneStrings(item.ExcludePaths),
			Kinds: cloneStrings(item.Kinds), Naming: ColumnNaming(item.Naming.String()), ValueMode: ValueMode(item.ValueMode.String()), MaxColumns: item.MaxColumns,
		}
		if err := projection.Validate(); err != nil {
			return nil, fmt.Errorf("catalogProjections[%d] %q: %w", index, item.Name, err)
		}
		out = append(out, projection)
	}
	return out, nil
}

func recipeFieldsFromInput(in []*FhirFieldSelectInput) ([]Field, error) {
	out := make([]Field, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		expr, err := recipeSelectorExpression(item.Selector)
		if err != nil {
			return nil, fmt.Errorf("fields[%d] %q: %w", index, item.Name, err)
		}
		field := Field{Name: item.Name, FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), Expr: expr, ValueMode: ValueMode(item.ValueMode.String())}
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

func recipeFiltersFromInput(in []*FhirFilterInput) ([]Filter, error) {
	out := make([]Filter, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		filter := Filter{
			Select:   strings.TrimSpace(item.Select),
			FieldRef: strings.TrimSpace(optionalString(item.FieldRef)),
			Operator: spec.FilterOperator(item.Operator.String()),
		}
		if item.Quantifier != nil {
			filter.Quantifier = ArrayQuantifier(item.Quantifier.String())
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

func recipeFilterValue(input *FhirFilterValueInput) (spec.FilterValue, error) {
	if input == nil {
		return spec.FilterValue{}, fmt.Errorf("value is required")
	}
	value := spec.FilterValue{Kind: spec.FilterValueKind(input.Kind.String())}
	switch input.Kind {
	case FhirFilterValueKindString:
		value.String = input.String
	case FhirFilterValueKindCode:
		if input.Code != nil {
			value.Code = &CodeValue{System: optionalString(input.Code.System), Code: input.Code.Code, Display: optionalString(input.Code.Display)}
		}
	case FhirFilterValueKindBoolean:
		value.Boolean = input.Boolean
	case FhirFilterValueKindInteger:
		if input.Integer != nil {
			converted := int64(*input.Integer)
			value.Integer = &converted
		}
	case FhirFilterValueKindDecimal:
		value.Decimal = input.Decimal
	case FhirFilterValueKindDate:
		value.Date = input.Date
	case FhirFilterValueKindDateTime:
		value.DateTime = input.DateTime
	default:
		return spec.FilterValue{}, fmt.Errorf("unsupported value kind %q", input.Kind)
	}
	if err := value.Validate(); err != nil {
		return spec.FilterValue{}, err
	}
	return value, nil
}

func recipePivotsFromInput(in []*FhirPivotInput) ([]Pivot, error) {
	out := make([]Pivot, 0, len(in))
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
		pivot := Pivot{Name: item.Name, FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), ColumnExpr: column, ValueExpr: value, Columns: cloneStrings(item.Columns)}
		if item.Discovery != nil {
			pivot.Discovery = &PivotDiscovery{Family: optionalString(item.Discovery.Family), Path: optionalString(item.Discovery.Path), MaxColumns: item.Discovery.MaxColumns}
		}
		out = append(out, pivot)
	}
	return out, nil
}

func recipeAggregatesFromInput(in []*FhirAggregateInput) ([]Aggregate, error) {
	out := make([]Aggregate, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		aggregate := Aggregate{Name: item.Name, Operation: AggregateOperation(item.Operation.String()), FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), ValueMode: ValueMode(item.ValueMode.String())}
		path := strings.TrimSpace(optionalString(item.FhirPath))
		if path != "" {
			expr := Expression{Select: path}
			aggregate.Expr = &expr
		}
		predicatePath := strings.TrimSpace(optionalString(item.PredicatePath))
		predicateValue := optionalString(item.PredicateEquals)
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

func recipeSlicesFromInput(in []*FhirRepresentativeSliceInput) ([]RepresentativeSlice, error) {
	out := make([]RepresentativeSlice, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		fields, err := recipeFieldsFromInput(item.Fields)
		if err != nil {
			return nil, fmt.Errorf("slices[%d] %q fields: %w", index, item.Name, err)
		}
		slice := RepresentativeSlice{Name: item.Name, Limit: item.Limit, Fields: fields}
		wherePath := strings.TrimSpace(optionalString(item.WherePath))
		whereValue := optionalString(item.WhereEquals)
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

func recipeTraversalsFromInput(in []*FhirTraversalStepInput) ([]Traversal, error) {
	out := make([]Traversal, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		output, err := recipeOutputFromInput(item.ToResourceType, item.Fields, item.Filters, item.Pivots, item.Aggregates, item.Slices, item.CatalogProjections, item.Traverse)
		if err != nil {
			return nil, fmt.Errorf("traversals[%d] %q: %w", index, item.Alias, err)
		}
		matchMode := MatchOptional
		if item.MatchMode != nil {
			matchMode = TraversalMatchMode(item.MatchMode.String())
		}
		out = append(out, Traversal{Name: item.EdgeLabel, ToResourceType: item.ToResourceType, Alias: item.Alias, MatchMode: matchMode, Fields: output.Fields, Filters: output.Filters, Pivots: output.Pivots, Aggregates: output.Aggregates, Slices: output.Slices, CatalogProjections: output.CatalogProjections, Traversals: output.Traversals})
	}
	return out, nil
}

func recipeSelectorExpression(in *FhirFieldSelectorInput) (Expression, error) {
	if in == nil {
		return Expression{}, fmt.Errorf("selector is required")
	}
	selectText := composeSelector(optionalString(in.SourcePath), predicatePathFromInput(in.Where), predicateOpFromInput(in.Where), predicateValueFromInput(in.Where), in.ValuePath)
	if strings.TrimSpace(selectText) == "" {
		return Expression{}, fmt.Errorf("selector valuePath is required")
	}
	return Expression{Select: selectText}, nil
}

func recipeStringEqualityFilter(path, value string) *Filter {
	valueCopy := value
	return &Filter{Select: path, Operator: FilterEquals, Values: []spec.FilterValue{{Kind: FilterString, String: &valueCopy}}}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
