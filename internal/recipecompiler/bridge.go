// Package recipecompiler turns the product-level recipe contract into the
// compiler's internal Builder only after resolving its opaque capabilities
// against fresh, authorized discovery facts.
//
// This first bridge is intentionally root-only. A recipe column or filter can
// refer only to the resource represented by its named row grain; relationship
// choices, repeated-value quantifiers, pivots, aggregates, and row expansion
// need explicit product contracts before they can be lowered safely.
package recipecompiler

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/discovery"
	"github.com/calypr/loom/internal/fhirschema"
	"github.com/calypr/loom/internal/recipe"
)

// Stable errors returned by Build. Callers should use errors.Is rather than
// parsing error text; the bridge deliberately does not disclose raw catalog
// selectors or unrecognized recipe identifiers in those errors.
var (
	ErrCatalogProjectMismatch      = errors.New("recipe project does not match catalog facts")
	ErrColumnCapabilityUnavailable = errors.New("recipe column capability is unavailable")
	ErrRelatedResource             = errors.New("recipe capability belongs to a related resource")
	ErrPivotChoiceUnsupported      = errors.New("recipe pivot-only capability is unsupported")
	ErrRepeatedColumn              = errors.New("recipe repeated column requires an explicit cardinality decision")
	ErrRepeatedFilter              = errors.New("recipe repeated filter requires an explicit quantifier decision")
	ErrUnsupportedValueConversion  = errors.New("recipe filter value cannot be converted to the resolved schema kind")
	ErrUnsupportedFilter           = errors.New("recipe filter is unsupported for the resolved schema capability")
	ErrOutputNameCollision         = errors.New("recipe output names collide after compiler normalization")
	ErrGenerationBindingRequired   = errors.New("pinned recipe generation requires a dataset generation binding")
)

// Plan is the compiler-ready result of resolving a Recipe against one fresh
// authorized catalog snapshot. Recipe is normalized and Builder contains only
// selectors recovered from discovery capability resolution, never selectors
// supplied by the recipe input.
type Plan struct {
	Recipe  recipe.Recipe
	Builder dataframe.Builder
}

// Build normalizes input, resolves every opaque column/filter capability from
// the supplied fresh CatalogFacts, and produces a root-only dataframe Builder.
//
// A recipe BETWEEN filter is intentionally lowered as two inclusive filters:
// GTE for the first value and LTE for the second. NOT_IN is lowered as one
// scalar NOT_EQUALS filter per value. Repeated values are rejected in V1 rather
// than assuming ANY, ALL, or a projection/cardinality policy on the user's
// behalf.
//
// CatalogFacts must have been collected for the caller's current authorized
// project/scope. The returned Builder intentionally leaves AuthResourcePaths
// unset: caller authorization remains the responsibility of dataframe.Service
// or the owning request layer, which has the principal and scope identity.
// Pinned recipes return ErrGenerationBindingRequired until an owning dataset
// layer supplies facts bound to the requested generation.
func Build(input recipe.Recipe, facts discovery.CatalogFacts) (Plan, error) {
	normalized, err := input.Normalize()
	if err != nil {
		return Plan{}, err
	}
	if normalized.GenerationPolicy == recipe.GenerationPinned {
		// CatalogFacts deliberately has no dataset-generation identity. Accepting
		// a pinned recipe here would silently compile it against whichever facts
		// happen to be current, violating the recipe's reproducibility contract.
		return Plan{}, ErrGenerationBindingRequired
	}
	if strings.TrimSpace(facts.Project) != normalized.Project {
		return Plan{}, ErrCatalogProjectMismatch
	}

	resolver, err := discovery.NewCapabilityResolver(facts)
	if err != nil {
		return Plan{}, err
	}
	rowGrain, rootResourceType, err := rootForRecipeGrain(normalized.Grain)
	if err != nil {
		return Plan{}, err
	}

	resolvedColumns := make(map[string]discovery.ResolvedColumn, len(normalized.Columns))
	for _, selection := range normalized.Columns {
		resolved, err := resolveRootColumn(resolver, selection.ID, rootResourceType)
		if err != nil {
			return Plan{}, err
		}
		resolvedColumns[selection.ID] = resolved
	}

	builder := dataframe.Builder{
		Project:          normalized.Project,
		RootResourceType: rootResourceType,
		RowGrain:         rowGrain,
		Fields:           make([]dataframe.FieldSelect, 0, len(normalized.Columns)),
		Filters:          make([]dataframe.TypedFilter, 0, len(normalized.Filters)),
	}

	// Resolve filters before materializing selections so a recipe which uses a
	// repeated field as a filter receives the precise missing-quantifier error;
	// a repeated selected field without a filter receives ErrRepeatedColumn.
	for _, filter := range normalized.Filters {
		resolved, ok := resolvedColumns[filter.ColumnID]
		if !ok {
			// Normalize currently proves this cannot happen, but do not turn a
			// future recipe-contract regression into a selector fallback.
			return Plan{}, ErrColumnCapabilityUnavailable
		}
		lowered, err := lowerRootFilter(rootResourceType, resolved, filter)
		if err != nil {
			return Plan{}, err
		}
		builder.Filters = append(builder.Filters, lowered...)
	}

	outputNames, err := outputNames(normalized.Columns)
	if err != nil {
		return Plan{}, err
	}
	for index, selection := range normalized.Columns {
		field, err := lowerRootSelection(resolvedColumns[selection.ID], outputNames[index])
		if err != nil {
			return Plan{}, err
		}
		builder.Fields = append(builder.Fields, field)
	}

	// This verifies the internal builder has a valid generated-schema semantic
	// shape before it reaches later service/catalog validation or lowering.
	if _, err := dataframe.BuildSemanticPlan(builder); err != nil {
		return Plan{}, fmt.Errorf("recipe compiler generated invalid dataframe builder: %w", err)
	}

	return Plan{Recipe: normalized, Builder: builder}, nil
}

func rootForRecipeGrain(grain recipe.Grain) (dataframe.RowGrain, string, error) {
	var rowGrain dataframe.RowGrain
	switch grain {
	case recipe.GrainPatient:
		rowGrain = dataframe.RowGrainPatient
	case recipe.GrainSpecimen:
		rowGrain = dataframe.RowGrainSpecimen
	case recipe.GrainFile:
		rowGrain = dataframe.RowGrainFile
	case recipe.GrainDiagnosis:
		rowGrain = dataframe.RowGrainDiagnosis
	case recipe.GrainObservation:
		rowGrain = dataframe.RowGrainObservation
	case recipe.GrainStudyEnrollment:
		rowGrain = dataframe.RowGrainStudyEnrollment
	default:
		return "", "", fmt.Errorf("recipe grain %q has no named dataframe row grain", grain)
	}
	rootResourceType, ok := dataframe.RootResourceForGrain(rowGrain)
	if !ok || rootResourceType == "" {
		return "", "", fmt.Errorf("dataframe row grain %q has no named root resource", rowGrain)
	}
	return rowGrain, rootResourceType, nil
}

func resolveRootColumn(resolver *discovery.CapabilityResolver, id string, rootResourceType string) (discovery.ResolvedColumn, error) {
	resolved, err := resolver.ResolveColumn(discovery.ColumnID(id))
	if err != nil {
		if errors.Is(err, discovery.ErrColumnUnavailable) {
			return discovery.ResolvedColumn{}, fmt.Errorf("%w: %w", ErrColumnCapabilityUnavailable, discovery.ErrColumnUnavailable)
		}
		return discovery.ResolvedColumn{}, err
	}
	if resolved.ResourceType != rootResourceType {
		return discovery.ResolvedColumn{}, ErrRelatedResource
	}
	return resolved, nil
}

func lowerRootSelection(resolved discovery.ResolvedColumn, outputName string) (dataframe.FieldSelect, error) {
	if pivotOnly(resolved) {
		return dataframe.FieldSelect{}, ErrPivotChoiceUnsupported
	}
	if resolved.Repeated {
		return dataframe.FieldSelect{}, ErrRepeatedColumn
	}
	if !resolved.CanSelect || resolved.Selector == nil {
		return dataframe.FieldSelect{}, ErrColumnCapabilityUnavailable
	}
	return dataframe.FieldSelect{
		Name:      outputName,
		FieldRef:  string(resolved.ID),
		Select:    fhirschema.SelectorExpression(*resolved.Selector),
		ValueMode: "AUTO",
	}, nil
}

func lowerRootFilter(rootResourceType string, resolved discovery.ResolvedColumn, input recipe.Filter) ([]dataframe.TypedFilter, error) {
	if pivotOnly(resolved) {
		return nil, ErrPivotChoiceUnsupported
	}
	if resolved.Repeated {
		return nil, ErrRepeatedFilter
	}
	if !resolved.CanFilter || resolved.Selector == nil {
		return nil, ErrUnsupportedFilter
	}

	kind, ok := dataframeFilterKind(resolved.ValueKind)
	if !ok {
		return nil, ErrUnsupportedValueConversion
	}
	values, err := convertFilterValues(kind, input.Values)
	if err != nil {
		return nil, err
	}
	newFilter := func(operator dataframe.FilterOperator, filterValues []dataframe.FilterValue) (dataframe.TypedFilter, error) {
		filter := dataframe.TypedFilter{
			FieldRef:  string(resolved.ID),
			Selector:  fhirschema.SelectorExpression(*resolved.Selector),
			FieldKind: kind,
			Operator:  operator,
			Values:    append([]dataframe.FilterValue(nil), filterValues...),
		}
		if err := dataframe.ValidateTypedFilterForResource(rootResourceType, filter); err != nil {
			return dataframe.TypedFilter{}, ErrUnsupportedFilter
		}
		return filter, nil
	}

	switch input.Operator {
	case recipe.FilterEquals:
		filter, err := newFilter(dataframe.FilterEquals, values)
		return oneFilter(filter, err)
	case recipe.FilterNotEquals:
		filter, err := newFilter(dataframe.FilterNotEquals, values)
		return oneFilter(filter, err)
	case recipe.FilterIn:
		filter, err := newFilter(dataframe.FilterIn, values)
		return oneFilter(filter, err)
	case recipe.FilterNotIn:
		filters := make([]dataframe.TypedFilter, 0, len(values))
		for _, value := range values {
			filter, err := newFilter(dataframe.FilterNotEquals, []dataframe.FilterValue{value})
			if err != nil {
				return nil, err
			}
			filters = append(filters, filter)
		}
		return filters, nil
	case recipe.FilterExists:
		filter, err := newFilter(dataframe.FilterExists, nil)
		return oneFilter(filter, err)
	case recipe.FilterMissing:
		filter, err := newFilter(dataframe.FilterMissing, nil)
		return oneFilter(filter, err)
	case recipe.FilterContains:
		filter, err := newFilter(dataframe.FilterContains, values)
		return oneFilter(filter, err)
	case recipe.FilterGreaterThan:
		filter, err := newFilter(dataframe.FilterGreaterThan, values)
		return oneFilter(filter, err)
	case recipe.FilterLessThan:
		filter, err := newFilter(dataframe.FilterLessThan, values)
		return oneFilter(filter, err)
	case recipe.FilterBetween:
		lower, err := newFilter(dataframe.FilterGreaterEq, values[:1])
		if err != nil {
			return nil, err
		}
		upper, err := newFilter(dataframe.FilterLessEq, values[1:])
		if err != nil {
			return nil, err
		}
		return []dataframe.TypedFilter{lower, upper}, nil
	default:
		// Recipe.Normalize currently keeps this closed, but never fall through
		// to a textual operator or a future unsupported enum value.
		return nil, ErrUnsupportedFilter
	}
}

func oneFilter(filter dataframe.TypedFilter, err error) ([]dataframe.TypedFilter, error) {
	if err != nil {
		return nil, err
	}
	return []dataframe.TypedFilter{filter}, nil
}

func pivotOnly(resolved discovery.ResolvedColumn) bool {
	return resolved.ValueKind == discovery.ValueKindComposite || (!resolved.CanSelect && resolved.CanPivot)
}

func dataframeFilterKind(kind discovery.ValueKind) (dataframe.FilterValueKind, bool) {
	switch kind {
	case discovery.ValueKindString:
		return dataframe.FilterString, true
	case discovery.ValueKindBoolean:
		return dataframe.FilterBoolean, true
	case discovery.ValueKindInteger:
		return dataframe.FilterInteger, true
	case discovery.ValueKindDecimal:
		return dataframe.FilterDecimal, true
	case discovery.ValueKindDate:
		return dataframe.FilterDate, true
	case discovery.ValueKindDateTime:
		return dataframe.FilterDateTime, true
	default:
		return "", false
	}
}

func convertFilterValues(kind dataframe.FilterValueKind, values []string) ([]dataframe.FilterValue, error) {
	converted := make([]dataframe.FilterValue, 0, len(values))
	for _, value := range values {
		convertedValue, err := convertFilterValue(kind, value)
		if err != nil {
			return nil, err
		}
		converted = append(converted, convertedValue)
	}
	return converted, nil
}

func convertFilterValue(kind dataframe.FilterValueKind, raw string) (dataframe.FilterValue, error) {
	var value dataframe.FilterValue
	switch kind {
	case dataframe.FilterString:
		value = dataframe.FilterValue{Kind: kind, String: &raw}
	case dataframe.FilterBoolean:
		var parsed bool
		switch raw {
		case "true":
			parsed = true
		case "false":
			parsed = false
		default:
			return dataframe.FilterValue{}, ErrUnsupportedValueConversion
		}
		value = dataframe.FilterValue{Kind: kind, Boolean: &parsed}
	case dataframe.FilterInteger:
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return dataframe.FilterValue{}, ErrUnsupportedValueConversion
		}
		value = dataframe.FilterValue{Kind: kind, Integer: &parsed}
	case dataframe.FilterDecimal:
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return dataframe.FilterValue{}, ErrUnsupportedValueConversion
		}
		value = dataframe.FilterValue{Kind: kind, Decimal: &parsed}
	case dataframe.FilterDate:
		value = dataframe.FilterValue{Kind: kind, Date: &raw}
	case dataframe.FilterDateTime:
		value = dataframe.FilterValue{Kind: kind, DateTime: &raw}
	default:
		return dataframe.FilterValue{}, ErrUnsupportedValueConversion
	}
	if err := value.Validate(); err != nil {
		return dataframe.FilterValue{}, ErrUnsupportedValueConversion
	}
	return value, nil
}

func outputNames(columns []recipe.ColumnSelection) ([]string, error) {
	// Check all explicit names first, so defaults can avoid a user-provided
	// column_1 instead of creating a lower-level compiler collision.
	usedCompilerNames := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if column.OutputName == "" {
			continue
		}
		name := compilerOutputName(column.OutputName)
		if _, exists := usedCompilerNames[name]; exists {
			return nil, ErrOutputNameCollision
		}
		usedCompilerNames[name] = struct{}{}
	}

	names := make([]string, len(columns))
	for index, column := range columns {
		if column.OutputName != "" {
			names[index] = column.OutputName
			continue
		}
		for suffix := index + 1; ; suffix++ {
			candidate := fmt.Sprintf("column_%d", suffix)
			compilerName := compilerOutputName(candidate)
			if _, exists := usedCompilerNames[compilerName]; exists {
				continue
			}
			usedCompilerNames[compilerName] = struct{}{}
			names[index] = candidate
			break
		}
	}
	return names, nil
}

// compilerOutputName intentionally mirrors dataframe's output-key
// normalization so recipe input cannot create duplicate compiled columns by
// using different punctuation in otherwise identical display names.
func compilerOutputName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	return builder.String()
}
