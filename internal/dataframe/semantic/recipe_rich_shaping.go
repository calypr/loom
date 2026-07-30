package semantic

// This file owns the recipe rich-shaping boundary. Pivots, aggregates, and
// representative slices are converted into canonical semantic operations. Nothing here chooses a traversal strategy or emits
// a backend expression; those decisions remain in the common compiler.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

var recipeRichNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// lowerRecipePivots converts bounded recipe pivots into canonical semantic
// pivots.  Both expressions must be selectors owned by the containing node;
// schema metadata, rather than a client-supplied family, proves that the
// selector pair is supported.
func lowerRecipePivots(resourceType, alias string, scope scopeFrame, pivots []recipe.Pivot) ([]SemanticPivot, error) {
	if !fhirschema.HasResource(resourceType) {
		return nil, fmt.Errorf("pivot resource type %q is not represented by the active generated FHIR schema", resourceType)
	}
	out := make([]SemanticPivot, 0, len(pivots))
	seen := make(map[string]struct{}, len(pivots))
	for index, input := range pivots {
		path := fmt.Sprintf("pivots[%d]", index)
		if err := validateRecipeRichName(input.Name, path+".name"); err != nil {
			return nil, err
		}
		if _, exists := seen[input.Name]; exists {
			return nil, fmt.Errorf("%s.name %q is duplicated", path, input.Name)
		}
		seen[input.Name] = struct{}{}
		if len(input.Columns) == 0 {
			return nil, fmt.Errorf("%s.columns must contain at least one bounded column", path)
		}
		columns := make([]string, len(input.Columns))
		columnNames := make(map[string]struct{}, len(input.Columns))
		for columnIndex, column := range input.Columns {
			columnPath := fmt.Sprintf("%s.columns[%d]", path, columnIndex)
			if strings.TrimSpace(column) == "" {
				return nil, fmt.Errorf("%s must not be empty", columnPath)
			}
			if _, exists := columnNames[column]; exists {
				return nil, fmt.Errorf("%s is duplicated", columnPath)
			}
			columnNames[column] = struct{}{}
			columns[columnIndex] = column
		}

		column, err := recipeNodeSelector(resourceType, alias, scope, input.ColumnExpr, path+".columnExpr")
		if err != nil {
			return nil, err
		}
		value, err := recipeNodeSelector(resourceType, alias, scope, input.ValueExpr, path+".valueExpr")
		if err != nil {
			return nil, err
		}
		validationResourceType := resourceType
		validationColumn := column
		validationValue := value
		if input.ItemResourceType != "" {
			validationResourceType = input.ItemResourceType
			validationColumn, err = relativePivotSelector(column, input.ItemSource.Select)
			if err != nil {
				return nil, fmt.Errorf("%s column selector: %w", path, err)
			}
			validationValue, err = relativePivotSelector(value, input.ItemSource.Select)
			if err != nil {
				return nil, fmt.Errorf("%s value selector: %w", path, err)
			}
		}
		pivotSpec, err := fhirschema.ValidatePivotSelectors(validationResourceType, selectorSpecFromSelector(validationColumn), selectorSpecFromSelector(validationValue))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if input.ItemResourceType != "" {
			pivotSpec.ItemResourceType = input.ItemResourceType
			pivotSpec.ItemSourcePath = selectorPath(input.ItemSource.Select)
		}
		itemResourceType := pivotSpec.ItemResourceType
		if pivotSpec.ItemSourcePath == "" {
			// A resource-level pivot has no nested item scope; leave the item
			// type empty so the physical renderer uses its singleton-document
			// lowering rather than requiring an item-source selector.
			itemResourceType = ""
		} else if itemResourceType == "" {
			itemResourceType = resourceType
		}
		itemSource := spec.Selector{}
		if pivotSpec.ItemSourcePath != "" {
			itemSource, err = spec.ParseSelector(pivotSpec.ItemSourcePath)
			if err != nil {
				return nil, fmt.Errorf("%s item source: %w", path, err)
			}
			if err := validateSemanticSelector(resourceType, itemSource); err != nil {
				return nil, fmt.Errorf("%s item source: %w", path, err)
			}
		}
		if pivotSpec.ItemSourcePath != "" {
			// The schema owns the relative selector conversion. Reparse the
			// normalized selectors so component pivots are evaluated against one
			// generated backbone item rather than independently against the
			// containing Observation.
			column, err = spec.ParseSelector(fhirschema.SelectorExpression(pivotSpec.ColumnSelector))
			if err != nil {
				return nil, fmt.Errorf("%s column selector: %w", path, err)
			}
			value, err = spec.ParseSelector(fhirschema.SelectorExpression(pivotSpec.ValueSelector))
			if err != nil {
				return nil, fmt.Errorf("%s value selector: %w", path, err)
			}
		}
		fallbacks := make([]spec.Selector, 0, len(pivotSpec.ValueSelectors))
		if len(input.ValueFallbacks) > 0 {
			for index, fallbackInput := range input.ValueFallbacks {
				fallback, fallbackErr := recipeNodeSelector(resourceType, alias, scope, fallbackInput, fmt.Sprintf("%s.valueFallbacks[%d]", path, index))
				if fallbackErr != nil {
					return nil, fallbackErr
				}
				if input.ItemResourceType != "" {
					fallback, fallbackErr = relativePivotSelector(fallback, input.ItemSource.Select)
					if fallbackErr != nil {
						return nil, fmt.Errorf("%s value selector fallback %d: %w", path, index, fallbackErr)
					}
				}
				fallbacks = append(fallbacks, fallback)
			}
		} else {
			for index, alternative := range pivotSpec.ValueSelectors {
				if index == 0 {
					continue
				}
				fallback, parseErr := spec.ParseSelector(fhirschema.SelectorExpression(alternative))
				if parseErr != nil {
					return nil, fmt.Errorf("%s value selector fallback %d: %w", path, index, parseErr)
				}
				fallbacks = append(fallbacks, fallback)
			}
		}
		valueResourceType := resourceType
		if itemResourceType != "" {
			valueResourceType = itemResourceType
		}
		valueKind, stringifyValue, err := pivotValueKind(valueResourceType, value, fallbacks)
		if err != nil {
			return nil, fmt.Errorf("%s value selectors: %w", path, err)
		}
		out = append(out, SemanticPivot{
			Name:             input.Name,
			FieldRef:         input.FieldRef,
			ColumnSelector:   column,
			ValueSelector:    value,
			ValueFallbacks:   fallbacks,
			ValueKind:        valueKind,
			StringifyValue:   stringifyValue,
			ItemSource:       itemSource,
			ItemResourceType: itemResourceType,
			Columns:          columns,
			Family:           pivotSpec.Family,
		})
	}
	return out, nil
}

// pivotValueKind derives the flat output type from the value selectors rather
// than from the pivot map that temporarily carries them. FHIR choice values
// can mix primitive kinds (for example Observation.valueQuantity.value and
// Observation.valueString); those values share one discovered column family,
// so normalize that heterogeneous family to strings at the physical boundary.
func pivotValueKind(resourceType string, primary spec.Selector, fallbacks []spec.Selector) (expression.ValueKind, bool, error) {
	selectors := append([]spec.Selector{primary}, fallbacks...)
	kinds := make([]expression.ValueKind, 0, len(selectors))
	for _, selector := range selectors {
		metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
		if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
			return "", false, fmt.Errorf("selector %q does not resolve to a primitive value", selector.CanonicalPath())
		}
		kind := expression.KindString
		switch metadata.Primitive {
		case fhirschema.PrimitiveBoolean:
			kind = expression.KindBoolean
		case fhirschema.PrimitiveInteger:
			kind = expression.KindInteger
		case fhirschema.PrimitiveDecimal:
			kind = expression.KindDecimal
		case fhirschema.PrimitiveDate:
			kind = expression.KindDate
		case fhirschema.PrimitiveDateTime:
			kind = expression.KindDateTime
		}
		kinds = append(kinds, kind)
	}
	kind := kinds[0]
	for _, candidate := range kinds[1:] {
		if candidate == kind {
			continue
		}
		if (kind == expression.KindInteger && candidate == expression.KindDecimal) ||
			(kind == expression.KindDecimal && candidate == expression.KindInteger) {
			kind = expression.KindDecimal
			continue
		}
		return expression.KindString, true, nil
	}
	return kind, false, nil
}

func relativePivotSelector(selector spec.Selector, source string) (spec.Selector, error) {
	if source == "" {
		return selector, nil
	}
	selectorPath := selector.CanonicalPath()
	prefix := selectorPathFromExpression(source)
	if prefix == "" {
		return selector, nil
	}
	if selectorPath == prefix {
		return spec.Selector{}, fmt.Errorf("pivot selector cannot be the item source itself")
	}
	if !strings.HasPrefix(selectorPath, prefix+".") {
		return spec.Selector{}, fmt.Errorf("selector %q is outside item source %q", selectorPath, prefix)
	}
	return spec.ParseSelector(strings.TrimPrefix(selectorPath, prefix+"."))
}

func selectorPathFromExpression(input string) string {
	input = strings.TrimSpace(input)
	if index := strings.Index(input, "."); index >= 0 {
		return strings.TrimPrefix(input[index+1:], ".")
	}
	return ""
}

func selectorPath(input string) string {
	return selectorPathFromExpression(input)
}

// lowerRecipeAggregates converts recipe aggregate declarations into the
// canonical aggregate representation.  Version-one aggregate expressions are
// selector-only because SemanticAggregate intentionally stores selector
// metadata and the existing physical aggregate lowerer consumes that shape.
func lowerRecipeAggregates(resourceType, alias string, scope scopeFrame, aggregates []recipe.Aggregate) ([]SemanticAggregate, error) {
	if !fhirschema.HasResource(resourceType) {
		return nil, fmt.Errorf("aggregate resource type %q is not represented by the active generated FHIR schema", resourceType)
	}
	out := make([]SemanticAggregate, 0, len(aggregates))
	seen := make(map[string]struct{}, len(aggregates))
	for index, input := range aggregates {
		path := fmt.Sprintf("aggregates[%d]", index)
		if err := validateRecipeRichName(input.Name, path+".name"); err != nil {
			return nil, err
		}
		if _, exists := seen[input.Name]; exists {
			return nil, fmt.Errorf("%s.name %q is duplicated", path, input.Name)
		}
		seen[input.Name] = struct{}{}
		operation := strings.ToUpper(strings.TrimSpace(string(input.Operation)))
		if !recipe.AggregateOperation(operation).Valid() {
			return nil, fmt.Errorf("%s.operation %q is unsupported", path, input.Operation)
		}
		if !input.ValueMode.Valid() {
			return nil, fmt.Errorf("%s.valueMode %q is unsupported", path, input.ValueMode)
		}
		semanticAggregate := SemanticAggregate{
			Name:      input.Name,
			Operation: operation,
			FieldRef:  input.FieldRef,
			ValueMode: string(input.ValueMode),
		}
		requiresSelector := operation == string(recipe.AggregateCountDistinct) ||
			operation == string(recipe.AggregateDistinctValues) ||
			operation == string(recipe.AggregateMin) ||
			operation == string(recipe.AggregateMax)
		if input.Expr != nil {
			if !requiresSelector {
				return nil, fmt.Errorf("%s.expr is not accepted for operation %s", path, operation)
			}
			selector, err := recipeNodeSelector(resourceType, alias, scope, *input.Expr, path+".expr")
			if err != nil {
				return nil, err
			}
			semanticAggregate.Selector = &selector
		} else if requiresSelector {
			return nil, fmt.Errorf("%s.expr is required for operation %s", path, operation)
		}
		if input.Where != nil {
			predicate, equals, err := lowerRecipePredicate(resourceType, alias, scope, input.Where, path+".where")
			if err != nil {
				return nil, err
			}
			semanticAggregate.Predicate = predicate
			semanticAggregate.PredicateEquals = equals
		}
		out = append(out, semanticAggregate)
	}
	return out, nil
}

// lowerRecipeSlices converts representative slices into canonical bounded
// slices.  Slice fields use the ordinary recipe projection checker, but the
// canonical SemanticSlice representation intentionally remains selector-based
// so the existing physical slice lowerer can retain typed extraction,
// fallback, and value-mode behavior.
func lowerRecipeSlices(resourceType, alias string, scope scopeFrame, slices []recipe.RepresentativeSlice) ([]SemanticSlice, error) {
	if !fhirschema.HasResource(resourceType) {
		return nil, fmt.Errorf("slice resource type %q is not represented by the active generated FHIR schema", resourceType)
	}
	out := make([]SemanticSlice, 0, len(slices))
	seen := make(map[string]struct{}, len(slices))
	for index, input := range slices {
		path := fmt.Sprintf("slices[%d]", index)
		if err := validateRecipeRichName(input.Name, path+".name"); err != nil {
			return nil, err
		}
		if _, exists := seen[input.Name]; exists {
			return nil, fmt.Errorf("%s.name %q is duplicated", path, input.Name)
		}
		seen[input.Name] = struct{}{}
		if input.Limit <= 0 {
			return nil, fmt.Errorf("%s.limit must be positive", path)
		}
		if len(input.Fields) == 0 {
			return nil, fmt.Errorf("%s.fields must contain at least one field", path)
		}
		semanticSlice := SemanticSlice{Name: input.Name, Limit: input.Limit, Fields: make([]SemanticField, 0, len(input.Fields))}
		seenFields := make(map[string]struct{}, len(input.Fields))
		for fieldIndex, field := range input.Fields {
			fieldPath := fmt.Sprintf("%s.fields[%d]", path, fieldIndex)
			semanticField, err := recipeProjectionField(field, scope, fieldPath)
			if err != nil {
				return nil, err
			}
			if _, exists := seenFields[field.Name]; exists {
				return nil, fmt.Errorf("%s.name %q is duplicated", fieldPath, field.Name)
			}
			seenFields[field.Name] = struct{}{}
			if semanticField.Expr == nil || semanticField.Expr.Selector == nil {
				return nil, fmt.Errorf("%s.expr must be a selector for canonical slice lowering", fieldPath)
			}
			if err := ensureSelectorOwnedByNode(semanticField.Expr.Selector.Context, alias, fieldPath+".expr"); err != nil {
				return nil, err
			}
			semanticSlice.Fields = append(semanticSlice.Fields, semanticField)
		}
		if input.Where != nil {
			predicate, equals, err := lowerRecipePredicate(resourceType, alias, scope, input.Where, path+".where")
			if err != nil {
				return nil, err
			}
			semanticSlice.Predicate = predicate
			semanticSlice.PredicateEquals = equals
		}
		out = append(out, semanticSlice)
	}
	return out, nil
}

// lowerRecipeRichShaping is the coordinated entrypoint used by the recipe
// node builder. Keeping the three conversions together makes it easy for the
// caller to attach them to exactly one semantic node while preserving the
// independent validation and tests for each operation family.
func lowerRecipeRichShaping(resourceType, alias string, scope scopeFrame, pivots []recipe.Pivot, aggregates []recipe.Aggregate, slices []recipe.RepresentativeSlice) ([]SemanticPivot, []SemanticAggregate, []SemanticSlice, error) {
	pivotPlan, err := lowerRecipePivots(resourceType, alias, scope, pivots)
	if err != nil {
		return nil, nil, nil, err
	}
	aggregatePlan, err := lowerRecipeAggregates(resourceType, alias, scope, aggregates)
	if err != nil {
		return nil, nil, nil, err
	}
	slicePlan, err := lowerRecipeSlices(resourceType, alias, scope, slices)
	if err != nil {
		return nil, nil, nil, err
	}
	return pivotPlan, aggregatePlan, slicePlan, nil
}

func recipeNodeSelector(resourceType, alias string, scope scopeFrame, input recipe.Expression, path string) (spec.Selector, error) {
	checked, err := scope.expression(input, path)
	if err != nil {
		return spec.Selector{}, err
	}
	if checked.Expression.Selector == nil {
		return spec.Selector{}, fmt.Errorf("%s must be a selector expression", path)
	}
	if err := ensureSelectorOwnedByNode(checked.Expression.Selector.Context, alias, path); err != nil {
		return spec.Selector{}, err
	}
	selector, err := spec.ParseSelector(checked.Expression.Selector.Path)
	if err != nil {
		return spec.Selector{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateSemanticSelector(resourceType, selector); err != nil {
		return spec.Selector{}, fmt.Errorf("%s: %w", path, err)
	}
	return selector, nil
}

// lowerRecipePredicate maps the only predicate shape currently representable
// by SemanticAggregate and SemanticSlice: selector existence or a string
// equality. Richer boolean predicates remain ordinary node filters until the
// canonical physical predicate IR grows an equivalent typed representation.
func lowerRecipePredicate(resourceType, alias string, scope scopeFrame, input *recipe.Filter, path string) (*spec.Selector, string, error) {
	if input == nil {
		return nil, "", nil
	}
	filters, err := LowerRecipeFiltersForAlias(resourceType, alias, []recipe.Filter{*input})
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	if len(filters) != 1 {
		return nil, "", fmt.Errorf("%s must contain exactly one predicate", path)
	}
	filter := filters[0]
	selector, err := spec.ParseSelector(filter.Selector)
	if err != nil {
		return nil, "", fmt.Errorf("%s selector: %w", path, err)
	}
	if err := validateSemanticSelector(resourceType, selector); err != nil {
		return nil, "", fmt.Errorf("%s selector: %w", path, err)
	}
	switch filter.Operator {
	case spec.FilterExists:
		return &selector, "", nil
	case spec.FilterEquals:
		if len(filter.Values) != 1 || filter.Values[0].String == nil || filter.Values[0].Kind != spec.FilterString {
			return nil, "", fmt.Errorf("%s equality predicate must use one STRING value", path)
		}
		return &selector, *filter.Values[0].String, nil
	default:
		return nil, "", fmt.Errorf("%s operator %s is not representable by canonical aggregate/slice predicates", path, filter.Operator)
	}
}

func ensureSelectorOwnedByNode(context, alias, path string) error {
	context = strings.TrimSpace(context)
	alias = strings.TrimSpace(alias)
	if context != "" && context != alias {
		return fmt.Errorf("%s selector context %q must match containing node %q", path, context, alias)
	}
	return nil
}

func validateRecipeRichName(value, path string) error {
	if !recipeRichNamePattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("%s %q is not a safe semantic name", path, value)
	}
	return nil
}
