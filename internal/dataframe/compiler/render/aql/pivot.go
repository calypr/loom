package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
)

func (r *physicalPlanRenderer) renderPivot(expression ir.PhysicalExpression) (string, error) {
	pivot := expression.Pivot
	if pivot == nil {
		return "", fmt.Errorf("PIVOT expression is missing payload")
	}
	if pivot.Correlation != nil {
		return r.renderCorrelatedPivot(*pivot.Correlation, pivot.ColumnsBindKey, pivot.StringifyValue, pivot.FlattenSingleColumn, pivot.ProjectionMode)
	}
	if _, collection := r.collectionKeys[pivot.ColumnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", pivot.ColumnsBindKey)
	}
	columns, ok := r.bindVars[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", pivot.ColumnsBindKey)
	}
	items, err := r.renderValue(pivot.Source)
	if err != nil {
		return "", err
	}
	if pivot.Source.Variable == "" || r.setVariables[pivot.Source.Variable] == "" {
		items = "[" + items + "]"
	}
	if pivot.PreparedKey != nil {
		items = pivot.PreparedKey.SetVariable
	}
	item := r.navigation().newInternalVariable("pivot_item")
	itemExpression := item
	itemLoop := fmt.Sprintf("FOR %s IN %s", item, items)
	if pivot.ItemResourceType != "" {
		if pivot.ItemSource.CanonicalPath() == "" {
			return "", fmt.Errorf("pivot item source is required when item resource type is set")
		}
		itemValues, sourceErr := r.renderSelectorArrayFromSource(item+".payload", pivot.ItemSource, false, false)
		if sourceErr != nil {
			return "", fmt.Errorf("pivot item source: %w", sourceErr)
		}
		itemExpression = r.navigation().newInternalVariable("pivot_item_value")

		itemLoop += fmt.Sprintf("\n      FOR %s IN FLATTEN(%s)", itemExpression, itemValues)
	}
	previousPreparedItem := r.preparedItem
	r.preparedItem = itemExpression
	keyExpr, err := r.renderPivotSelector(item, itemExpression, pivot.PreparedKey, pivot.KeySelector, pivot.ItemResourceType != "")
	if err != nil {
		r.preparedItem = previousPreparedItem
		return "", err
	}
	valueSelectors := append([]spec.Selector{pivot.ValueSelector}, pivot.ValueFallbacks...)
	valueExpressions := make([]string, 0, len(valueSelectors))
	for _, selector := range valueSelectors {
		value, valueErr := r.renderPivotSelector(item, itemExpression, nil, selector, pivot.ItemResourceType != "")
		if valueErr != nil {
			r.preparedItem = previousPreparedItem
			return "", valueErr
		}
		valueExpressions = append(valueExpressions, value)
	}
	if pivot.PreparedValue != nil {
		valueExpressions = []string{itemExpression + "." + pivot.PreparedValue.Field}
	}
	r.preparedItem = previousPreparedItem
	if len(valueExpressions) == 0 {
		return "", fmt.Errorf("pivot value selector is required")
	}
	valueExpr := valueExpressions[0]
	if len(valueExpressions) > 1 {
		valueExpr = "FIRST(FOR __pivot_candidate IN [" + strings.Join(valueExpressions, ", ") + "] FILTER LENGTH(__pivot_candidate) > 0 RETURN __pivot_candidate)"
	}
	pairs := fmt.Sprintf(`FOR __pair IN (
    %s
      LET __pivot_keys = UNIQUE(%s)
      LET __pivot_values = %s
      FILTER LENGTH(__pivot_values) > 0
      FOR __pivot_key IN __pivot_keys
        FILTER POSITION(@%s, __pivot_key)
        RETURN { key: __pivot_key, values: __pivot_values }
  )`, itemLoop, keyExpr, valueExpr, pivot.ColumnsBindKey)
	if pivot.FlattenSingleColumn {
		value := "FIRST(__pivot_flat_values)"
		if pivot.StringifyValue {
			value = "TO_STRING(" + value + ")"
		}
		return fmt.Sprintf(`FIRST(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN %s
)`, pairs, value), nil
	}
	value := "FIRST(__pivot_flat_values)"
	if pivot.StringifyValue {
		value = "TO_STRING(" + value + ")"
	}
	return fmt.Sprintf(`MERGE(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN { [__pivot_key]: %s }
)`, pairs, value), nil
}

func (r *physicalPlanRenderer) renderCorrelatedPivot(correlation ir.PhysicalCorrelation, columnsBindKey string, stringify, flattenSingle bool, projectionMode string) (string, error) {
	if len(correlation.ExtensionURLSelectors) > 0 {
		return r.renderExtensionCorrelatedPivot(correlation, columnsBindKey, stringify, flattenSingle, projectionMode)
	}
	if columnsBindKey == "" {
		return "", fmt.Errorf("correlated pivot columns bind is required")
	}
	if _, collection := r.collectionKeys[columnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", columnsBindKey)
	}
	columns, ok := r.bindVars[columnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", columnsBindKey)
	}
	if correlation.SelectionScope == fhirbinding.CorrelatedSelectionScopeSystemVersion && len(columns) != 1 {
		return "", fmt.Errorf("SYSTEM_VERSION pivot requires exactly one output column")
	}
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	owner := r.navigation().newInternalVariable("correlation_pivot_owner")
	coding := r.navigation().newInternalVariable("correlation_pivot_coding")
	codings, err := r.renderCorrelationCodingItems(owner, correlation)
	if err != nil {
		return "", err
	}
	system, err := r.renderCorrelationScalar(coding, correlation.SystemSelector)
	if err != nil {
		return "", err
	}
	code, err := r.renderCorrelationScalar(coding, correlation.CodeSelector)
	if err != nil {
		return "", err
	}
	versionClause, err := r.renderCorrelationVersionClause(correlation, coding, "      ")
	if err != nil {
		return "", err
	}
	valueSource := correlationValueSource(correlation, owner, coding)
	values, err := r.renderCorrelationValues(valueSource, correlation.ValueSelector, correlation.ValueFallbacks, correlation.FallbackOnBlankString)
	if err != nil {
		return "", err
	}
	unsupportedValues, err := r.renderCorrelationUnsupportedChoiceValues(valueSource, correlation)
	if err != nil {
		return "", err
	}
	value, err := renderCorrelatedReduction(projectionMode, stringify, correlation.ValuePrimitive)
	if err != nil {
		return "", err
	}
	codeClause := fmt.Sprintf("FILTER POSITION(@%s, __correlation_code)", columnsBindKey)
	pairKey := "__correlation_code"
	if correlation.SelectionScope == fhirbinding.CorrelatedSelectionScopeSystemVersion {
		codeClause = ""
		pairKey = "FIRST(@" + columnsBindKey + ")"
	}

	pairs := fmt.Sprintf(`FOR %s IN %s
  LET __correlation_owner_pairs = (
    FOR %s IN %s
      LET __correlation_system = %s
      LET __correlation_code = %s
      FILTER __correlation_system != null AND __correlation_system != ""
      FILTER __correlation_code != null AND __correlation_code != ""
      FILTER __correlation_system == @%s
      %s%s
      LET __correlation_values = %s
      LET __correlation_flat_values = FLATTEN(__correlation_values)
      LET __correlation_unsupported_values = %s
      FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
      RETURN { key: %s, values: __correlation_values, unsupported: __correlation_unsupported_values }
  )
  FOR __correlation_owner_pair IN (
    FOR __correlation_candidate IN __correlation_owner_pairs
      COLLECT __correlation_owner_key = __correlation_candidate.key INTO __correlation_owner_group
        LET __correlation_owner_values = UNIQUE(FLATTEN(__correlation_owner_group[*].__correlation_candidate.values))
        LET __correlation_owner_unsupported = UNIQUE(FLATTEN(__correlation_owner_group[*].__correlation_candidate.unsupported))
        RETURN { key: __correlation_owner_key, values: __correlation_owner_values, unsupported: __correlation_owner_unsupported }
  )
    RETURN __correlation_owner_pair`, owner, owners, coding, codings, system, code, correlation.SystemBindKey, codeClause, versionClause, values, unsupportedValues, pairKey)
	if flattenSingle {
		return fmt.Sprintf(`FIRST(
	  FOR __correlation_pair IN (
	    %s
	  )
	  COLLECT __correlation_key = __correlation_pair.key INTO __correlation_group
	    LET __correlation_flat_values = FLATTEN(__correlation_group[*].__correlation_pair.values)
	    LET __correlation_unsupported_values = FLATTEN(__correlation_group[*].__correlation_pair.unsupported)
	    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
	    RETURN %s
)`, pairs, value), nil
	}
	return fmt.Sprintf(`MERGE(
  FOR __correlation_pair IN (
    %s
	  )
	  COLLECT __correlation_key = __correlation_pair.key INTO __correlation_group
	    LET __correlation_flat_values = FLATTEN(__correlation_group[*].__correlation_pair.values)
	    LET __correlation_unsupported_values = FLATTEN(__correlation_group[*].__correlation_pair.unsupported)
	    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
	    RETURN { [__correlation_key]: %s }
)`, pairs, value), nil
}

func (r *physicalPlanRenderer) renderPivotSelector(resourceItem, item string, prepared *ir.PhysicalPreparedReference, selector spec.Selector, itemScoped bool) (string, error) {
	if prepared != nil {
		return item + "." + prepared.Field, nil
	}
	source := resourceItem + ".payload"
	if itemScoped {
		source = item
	}
	return r.renderSelectorArrayFromSource(source, selector, false, false)
}

func (r *physicalPlanRenderer) renderExtensionCorrelatedPivot(correlation ir.PhysicalCorrelation, columnsBindKey string, stringify, flattenSingle bool, projectionMode string) (string, error) {
	if columnsBindKey == "" {
		return "", fmt.Errorf("extension pivot columns bind is required")
	}
	if _, collection := r.collectionKeys[columnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", columnsBindKey)
	}
	columns, ok := r.bindVars[columnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", columnsBindKey)
	}
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	if len(correlation.ExtensionURLSelectors) != len(correlation.ExtensionURLBindKeys) {
		return "", fmt.Errorf("extension URL selectors and bind keys must have equal length")
	}
	extensionSelector, err := spec.ParseSelector("extension[]")
	if err != nil {
		return "", err
	}
	current := "__extension_base"
	lines := []string{"FOR " + current + " IN " + owners}
	for index, urlSelector := range correlation.ExtensionURLSelectors {
		extension := fmt.Sprintf("__correlation_extension_%d", index)
		extensions, selectorErr := r.renderSelectorArrayFromSource(current, extensionSelector, false, false)
		if selectorErr != nil {
			return "", fmt.Errorf("extension owner selector %d: %w", index, selectorErr)
		}
		lines = append(lines, "  FOR "+extension+" IN FLATTEN("+extensions+")")
		url, scalarErr := r.renderCorrelationScalar(extension, urlSelector)
		if scalarErr != nil {
			return "", fmt.Errorf("extension URL selector %d: %w", index, scalarErr)
		}
		lines = append(lines, fmt.Sprintf("    FILTER %s != null AND %s == @%s", url, url, correlation.ExtensionURLBindKeys[index]))
		current = extension
	}
	var values, unsupportedValues string
	if correlation.CanonicalJSONValue {
		values, unsupportedValues = "["+current+"]", "[]"
	} else {
		values, err = r.renderCorrelationValues(current, correlation.ValueSelector, correlation.ValueFallbacks, correlation.FallbackOnBlankString)
		if err != nil {
			return "", err
		}
		unsupportedValues, err = r.renderCorrelationUnsupportedChoiceValues(current, correlation)
		if err != nil {
			return "", err
		}
	}
	lines = append(lines,
		"    LET __correlation_values = "+values,
		"    LET __correlation_flat_values = FLATTEN(__correlation_values)",
		"    LET __correlation_unsupported_values = "+unsupportedValues,
		"    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0",
		"    RETURN { values: __correlation_values, unsupported: __correlation_unsupported_values }")
	pairs := strings.Join(lines, "\n")
	value, err := renderCorrelatedReduction(projectionMode, stringify, correlation.ValuePrimitive)
	if err != nil {
		return "", err
	}
	if correlation.CanonicalJSONValue {
		value = `{"__loom_postquery_call":"canonical_json","__loom_postquery_args":[FIRST(SORTED(__correlation_flat_values))]}`
	}

	aggregation := fmt.Sprintf("FOR __extension_result IN [1]\n  LET __extension_pairs = (\n    %s\n  )\n  LET __correlation_flat_values = FLATTEN(__extension_pairs[*].values)\n  LET __correlation_unsupported_values = FLATTEN(__extension_pairs[*].unsupported)\n  FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0\n  RETURN ", pairs)
	if flattenSingle {
		return "FIRST(\n  " + aggregation + value + "\n)", nil
	}
	return "MERGE(\n  " + aggregation + "{ [FIRST(@" + columnsBindKey + ")]: " + value + " }\n)", nil
}

func renderCorrelatedReduction(projectionMode string, stringify bool, primitive string) (string, error) {
	mode := strings.ToUpper(strings.TrimSpace(projectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	valuePrimitive := strings.ToLower(strings.TrimSpace(primitive))
	stringConversion := stringify && valuePrimitive != "" && valuePrimitive != "string"
	value := "__correlation_flat_values"
	switch mode {
	case "ALL":
		if stringConversion {
			value = "(FOR __correlation_value IN __correlation_flat_values RETURN TO_STRING(__correlation_value))"
		}
	case "DISTINCT":
		if stringConversion {
			value = "(FOR __correlation_value IN __correlation_flat_values RETURN TO_STRING(__correlation_value))"
		}
		value = "SORTED_UNIQUE(" + value + ")"
	case "VALUE":
		first := "FIRST(__correlation_flat_values)"
		if stringConversion {
			first = "TO_STRING(" + first + ")"
		}
		value = `LENGTH(__correlation_flat_values) == 1 ? ` + first + ` : { status: "INVALID_MULTIPLE_VALUES", raw: __correlation_flat_values }`
	case "FIRST":
		value = "FIRST(SORTED(__correlation_flat_values))"
		if stringConversion {
			value = "TO_STRING(" + value + ")"
		}
	default:
		return "", fmt.Errorf("correlated pivot projection mode %q is unsupported", projectionMode)
	}
	return `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : ` + value, nil
}
