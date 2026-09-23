package eval

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const maxEvaluationItems = 100_000

type pivotExpression struct {
	pivot       ir.PhysicalPivotMap
	correlation ir.PhysicalCorrelation
	columns     []string
	stringify   bool
	flatten     bool
	mode        string
}

func compileCorrelatedPivot(pivot ir.PhysicalPivotMap, binds map[string]any) (expression, error) {
	correlation := *pivot.Correlation
	if !reflect.DeepEqual(pivot.Source, correlation.Source) {
		return nil, unsupported("direct pivot source differs from correlated source")
	}
	if len(pivot.KeySelector.Steps) != 0 || len(pivot.ValueSelector.Steps) != 0 || len(pivot.ValueFallbacks) != 0 || pivot.PreparedKey != nil || pivot.PreparedValue != nil {
		return nil, unsupported("correlated pivot also carries legacy selectors or prepared references")
	}
	if pivot.ResourceType != correlation.ResourceType {
		return nil, unsupported("pivot and correlation resource types differ")
	}
	columns, ok := binds[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return nil, unsupported("pivot columns bind %q must be a non-empty []string", pivot.ColumnsBindKey)
	}
	mode := strings.ToUpper(strings.TrimSpace(pivot.ProjectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	if mode != "FIRST" && mode != "ALL" && mode != "VALUE" && mode != "DISTINCT" {
		return nil, unsupported("pivot reduction %q is unsupported", mode)
	}
	if mode == "DISTINCT" {
		return nil, unsupported("DISTINCT pivot reduction requires AQL SORTED_UNIQUE ordering")
	}
	if mode == "FIRST" {
		primitive := strings.ToLower(strings.TrimSpace(correlation.ValuePrimitive))
		if primitive == "" {
			primitive = strings.ToLower(strings.TrimSpace(correlation.LogicalType))
		}
		switch primitive {
		case "string", "integer", "decimal", "date", "datetime":
		default:
			return nil, unsupported("FIRST pivot primitive %q requires AQL sort ordering", primitive)
		}
	}
	if pivot.StringifyValue && correlation.ValuePrimitive != "" && correlation.ValuePrimitive != "string" {
		return nil, unsupported("pivot unexpectedly stringifies %q values", correlation.ValuePrimitive)
	}
	if err := validateSelectorSubset(correlation.OwnerSelector); len(correlation.OwnerSelector.Steps) > 0 && err != nil {
		return nil, err
	}
	if len(correlation.ExtensionURLSelectors) > 0 {
		if len(correlation.ExtensionURLSelectors) != len(correlation.ExtensionURLBindKeys) || len(columns) != 1 || pivot.FlattenSingleColumn {
			return nil, unsupported("extension pivot shape is outside the captured subset")
		}
		for index, selector := range correlation.ExtensionURLSelectors {
			if err := validateSelectorSubset(selector); err != nil {
				return nil, err
			}
			if _, ok := binds[correlation.ExtensionURLBindKeys[index]]; !ok {
				return nil, unsupported("extension URL bind %q is missing", correlation.ExtensionURLBindKeys[index])
			}
		}
	} else {
		for _, selector := range []spec.Selector{correlation.KeySelector, correlation.SystemSelector} {
			if err := validateSelectorSubset(selector); err != nil {
				return nil, err
			}
		}
		if !correlation.NamespaceOnly {
			if err := validateSelectorSubset(correlation.CodeSelector); err != nil {
				return nil, err
			}
			if correlation.ValueScope != "" && correlation.ValueScope != ir.PhysicalCorrelationValueOwner && correlation.ValueScope != ir.PhysicalCorrelationValueKeyItem {
				return nil, unsupported("correlation value scope %q is unsupported", correlation.ValueScope)
			}
		} else if pivot.FlattenSingleColumn {
			return nil, unsupported("single-column namespace pivot is outside the captured subset")
		}
	}
	if correlation.ValuePresentation != "" && correlation.ValuePresentation != "DISPLAY_OR_CODE" {
		return nil, unsupported("value presentation %q is unsupported", correlation.ValuePresentation)
	}
	if correlation.ValuePresentation == "DISPLAY_OR_CODE" {
		if len(correlation.ValueSelector.Steps) < 2 && !correlation.NamespaceOnly {
			return nil, unsupported("DISPLAY_OR_CODE requires a coding owner and terminal selector")
		}
	} else if err := validateSelectorSubset(correlation.ValueSelector); err != nil {
		return nil, err
	}
	for _, selector := range correlation.ValueFallbacks {
		if err := validateSelectorSubset(selector); err != nil {
			return nil, err
		}
	}
	for _, selector := range correlation.ChoiceSelectors {
		if err := validateSelectorSubset(selector); err != nil {
			return nil, err
		}
	}
	if correlation.NamespaceOnly && correlation.SystemBindKey == "" {
		return nil, unsupported("namespace pivot requires a system bind")
	}
	if !correlation.NamespaceOnly && len(correlation.ExtensionURLSelectors) == 0 && correlation.SystemBindKey == "" {
		return nil, unsupported("correlated pivot requires a system bind")
	}
	for _, key := range append([]string{correlation.SystemBindKey}, correlation.ExtensionURLBindKeys...) {
		if key != "" {
			if _, ok := binds[key]; !ok {
				return nil, unsupported("correlation bind %q is missing", key)
			}
		}
	}
	return pivotExpression{pivot: pivot, correlation: correlation, columns: append([]string(nil), columns...), stringify: pivot.StringifyValue, flatten: pivot.FlattenSingleColumn, mode: mode}, nil
}

func (value pivotExpression) references(names map[string]struct{}) {
	if value.correlation.Source.Variable != "" {
		names[value.correlation.Source.Variable] = struct{}{}
	}
}

func (value pivotExpression) evaluate(scope *evaluationScope) (any, error) {
	owners, err := correlationOwners(value.correlation, scope)
	if err != nil {
		return nil, err
	}
	if len(owners) > maxEvaluationItems {
		return nil, fmt.Errorf("pivot owner count exceeds in-memory limit %d", maxEvaluationItems)
	}
	result := make(map[string]any)
	switch {
	case len(value.correlation.ExtensionURLSelectors) > 0:
		values := make([]any, 0)
		unsupportedValues := make([]any, 0)
		for _, owner := range owners {
			current := []any{owner}
			for index, selector := range value.correlation.ExtensionURLSelectors {
				expected := scope.bindVars[value.correlation.ExtensionURLBindKeys[index]]
				next := make([]any, 0)
				for _, extensionBase := range current {
					extensions, selectErr := selectRows(extensionBase, spec.Selector{Steps: []spec.SelectorStep{{Field: "extension", Iterate: true}}})
					if selectErr != nil {
						return nil, selectErr
					}
					for _, extension := range flattenOne(extensions) {
						url, selectErr := firstSelected(extension, selector)
						if selectErr != nil {
							return nil, selectErr
						}
						if !isMissing(url) && url != nil && scalarEqual(url, expected) {
							next = append(next, extension)
						}
					}
				}
				current = next
				if len(current) > maxEvaluationItems {
					return nil, fmt.Errorf("extension pivot exceeds in-memory item limit %d", maxEvaluationItems)
				}
			}
			for _, terminal := range current {
				selected, selectErr := correlatedValues(terminal, value.correlation)
				if selectErr != nil {
					return nil, selectErr
				}
				values = append(values, selected...)
				invalid, selectErr := unsupportedChoices(terminal, value.correlation)
				if selectErr != nil {
					return nil, selectErr
				}
				unsupportedValues = append(unsupportedValues, invalid...)
			}
		}
		if len(values) > 0 || len(unsupportedValues) > 0 {
			result[value.columns[0]], err = reducePivot(value.mode, value.stringify, value.correlation.ValuePrimitive, values, unsupportedValues)
		}
	case value.correlation.NamespaceOnly:
		pairs := make(map[string][]any)
		expectedSystem := scope.bindVars[value.correlation.SystemBindKey]
		for _, owner := range owners {
			codings, selectErr := selectRows(owner, value.correlation.KeySelector)
			if selectErr != nil {
				return nil, selectErr
			}
			for _, coding := range flattenOne(codings) {
				system, selectErr := firstSelected(coding, value.correlation.SystemSelector)
				if selectErr != nil {
					return nil, selectErr
				}
				systemText, isString := system.(string)
				if !isString || systemText == "" || !scalarEqual(system, expectedSystem) || !containsColumn(value.columns, systemText) {
					continue
				}
				var selected any
				if value.correlation.ValuePresentation == "DISPLAY_OR_CODE" {
					displaySelector := spec.Selector{Steps: []spec.SelectorStep{{Field: "display"}}}
					if len(value.correlation.ValueFallbacks) > 0 {
						displaySelector = value.correlation.ValueFallbacks[0]
					}
					display, selectErr := firstSelected(coding, displaySelector)
					if selectErr != nil {
						return nil, selectErr
					}
					code, selectErr := firstSelected(coding, value.correlation.ValueSelector)
					if selectErr != nil {
						return nil, selectErr
					}
					if text, ok := display.(string); ok && strings.TrimSpace(text) != "" {
						selected = display
					} else {
						selected = code
					}
				} else {
					candidates, selectErr := correlatedValues(coding, value.correlation)
					if selectErr != nil {
						return nil, selectErr
					}
					selected = firstOrMissing(candidates)
				}
				if !isMissing(selected) && selected != nil {
					pairs[systemText] = append(pairs[systemText], selected)
				}
			}
		}
		for _, column := range value.columns {
			values := pairs[column]
			if len(values) == 0 {
				continue
			}
			result[column], err = reducePivot(value.mode, value.stringify, value.correlation.ValuePrimitive, values, nil)
			if err != nil {
				break
			}
		}
	default:
		valuesByCode := make(map[string][]any)
		unsupportedByCode := make(map[string][]any)
		for _, owner := range owners {
			perOwnerValues := make(map[string][]any)
			perOwnerUnsupported := make(map[string][]any)
			codings, selectErr := selectRows(owner, value.correlation.KeySelector)
			if selectErr != nil {
				return nil, selectErr
			}
			for _, coding := range flattenOne(codings) {
				system, selectErr := firstSelected(coding, value.correlation.SystemSelector)
				if selectErr != nil {
					return nil, selectErr
				}
				code, selectErr := firstSelected(coding, value.correlation.CodeSelector)
				if selectErr != nil {
					return nil, selectErr
				}
				systemText, okSystem := system.(string)
				codeText, okCode := code.(string)
				if !okSystem || systemText == "" || !scalarEqual(system, scope.bindVars[value.correlation.SystemBindKey]) || !okCode || !containsColumn(value.columns, codeText) {
					continue
				}
				source := owner
				if value.correlation.ValueScope == ir.PhysicalCorrelationValueKeyItem {
					source = coding
				}
				selected, selectErr := correlatedValues(source, value.correlation)
				if selectErr != nil {
					return nil, selectErr
				}
				invalid, selectErr := unsupportedChoices(source, value.correlation)
				if selectErr != nil {
					return nil, selectErr
				}
				perOwnerValues[codeText] = append(perOwnerValues[codeText], flattenOne(selected)...)
				perOwnerUnsupported[codeText] = append(perOwnerUnsupported[codeText], invalid...)
			}
			for code, items := range perOwnerValues {
				unique, uniqueErr := uniqueValues(items)
				if uniqueErr != nil {
					return nil, uniqueErr
				}
				valuesByCode[code] = append(valuesByCode[code], unique...)
			}
			for code, items := range perOwnerUnsupported {
				unique, uniqueErr := uniqueValues(items)
				if uniqueErr != nil {
					return nil, uniqueErr
				}
				unsupportedByCode[code] = append(unsupportedByCode[code], unique...)
			}
		}
		for _, column := range value.columns {
			values, invalid := valuesByCode[column], unsupportedByCode[column]
			if len(values) == 0 && len(invalid) == 0 {
				continue
			}
			result[column], err = reducePivot(value.mode, value.stringify, value.correlation.ValuePrimitive, values, invalid)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func correlationOwners(correlation ir.PhysicalCorrelation, scope *evaluationScope) ([]any, error) {
	if _, isSet := scope.sets[correlation.Source.Variable]; isSet {
		rows, ok := scope.variables[correlation.Source.Variable].([]any)
		if !ok {
			return nil, fmt.Errorf("source set %q must be an array", correlation.Source.Variable)
		}
		owners := make([]any, 0, len(rows))
		for index, item := range rows {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("source set %q row %d must be an object", correlation.Source.Variable, index)
			}
			payload, ok := row["payload"]
			if !ok {
				return nil, fmt.Errorf("source set %q row %d is missing payload", correlation.Source.Variable, index)
			}
			if len(correlation.OwnerSelector.Steps) == 0 {
				owners = append(owners, payload)
				continue
			}
			selected, err := selectRows(payload, correlation.OwnerSelector)
			if err != nil {
				return nil, err
			}
			owners = append(owners, flattenOne(selected)...)
		}
		return owners, nil
	}
	source, err := resolveValue(correlation.Source, scope)
	if err != nil {
		return nil, err
	}
	if correlation.Source.Variable == scope.rootVariable && len(correlation.Source.Path) == 0 {
		source = lookupField(source, "payload")
	}
	if isMissing(source) || source == nil {
		return nil, nil
	}
	if len(correlation.OwnerSelector.Steps) == 0 {
		return []any{source}, nil
	}
	selected, err := selectRows(source, correlation.OwnerSelector)
	if err != nil {
		return nil, err
	}
	return flattenOne(selected), nil
}

func correlatedValues(source any, correlation ir.PhysicalCorrelation) ([]any, error) {
	if correlation.ValuePresentation == "DISPLAY_OR_CODE" {
		steps := correlation.ValueSelector.Steps
		if len(steps) < 2 {
			return nil, unsupported("DISPLAY_OR_CODE selector has no coding owner")
		}
		codings, err := selectRows(source, spec.Selector{Steps: append([]spec.SelectorStep(nil), steps[:len(steps)-1]...)})
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(codings))
		for _, coding := range flattenOne(codings) {
			display := lookupField(coding, "display")
			if text, ok := display.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, display)
				continue
			}
			code := lookupField(coding, "code")
			if !isMissing(code) && code != nil {
				values = append(values, code)
			}
		}
		return values, nil
	}
	if correlation.ValuePresentation != "" {
		return nil, unsupported("value presentation %q is unsupported", correlation.ValuePresentation)
	}
	selectors := make([]spec.Selector, 0, len(correlation.ValueFallbacks)+1)
	selectors = append(selectors, correlation.ValueSelector)
	selectors = append(selectors, correlation.ValueFallbacks...)
	for _, selector := range selectors {
		values, err := selectRows(source, selector)
		if err != nil {
			return nil, err
		}
		if len(values) > 0 {
			return values, nil
		}
	}
	return []any{}, nil
}

func unsupportedChoices(source any, correlation ir.PhysicalCorrelation) ([]any, error) {
	allowed := make(map[string]struct{}, len(correlation.ChoiceArms))
	for _, arm := range correlation.ChoiceArms {
		allowed[arm] = struct{}{}
	}
	result := make([]any, 0)
	for _, selector := range correlation.ChoiceSelectors {
		if len(selector.Steps) == 0 {
			return nil, unsupported("choice selector has no steps")
		}
		if _, ok := allowed[selector.Steps[0].Field]; ok {
			continue
		}
		values, err := selectRows(source, selector)
		if err != nil {
			return nil, err
		}
		result = append(result, flattenOne(values)...)
	}
	return result, nil
}

func firstSelected(source any, selector spec.Selector) (any, error) {
	values, err := selectRows(source, selector)
	if err != nil {
		return missingValue, err
	}
	return firstOrMissing(values), nil
}

func firstOrMissing(values []any) any {
	if len(values) == 0 {
		return missingValue
	}
	return values[0]
}

func reducePivot(mode string, stringify bool, primitive string, values, invalid []any) (any, error) {
	if len(invalid) > 0 {
		return map[string]any{"status": "INVALID_CHOICE_ARM", "raw": invalid}, nil
	}
	if stringify && primitive != "" && primitive != "string" {
		return nil, unsupported("pivot unexpectedly stringifies %q values", primitive)
	}
	switch mode {
	case "ALL":
		return values, nil
	case "VALUE":
		if len(values) == 1 {
			return values[0], nil
		}
		return map[string]any{"status": "INVALID_MULTIPLE_VALUES", "raw": values}, nil
	case "DISTINCT":
		return nil, unsupported("DISTINCT pivot reduction requires AQL SORTED_UNIQUE ordering")
	case "FIRST":
		if len(values) == 0 {
			return nil, nil
		}
		ordered := append([]any(nil), values...)
		stringMode := false
		numberMode := false
		for _, item := range ordered {
			if !isAQLScalar(item) {
				return nil, fmt.Errorf("FIRST pivot reduction has non-scalar value %T", item)
			}
			if _, ok := item.(string); ok {
				stringMode = true
			} else {
				numberMode = true
			}
		}
		if stringMode && numberMode {
			return nil, fmt.Errorf("FIRST pivot reduction has mixed string and numeric values")
		}
		sortAQLValues(ordered)
		return ordered[0], nil
	default:
		return nil, unsupported("pivot reduction %q is unsupported", mode)
	}
}

func sortAQLValues(values []any) {
	// Callers validate one scalar domain first, so AQL-like ordering is
	// transitive and can use the standard O(n log n) sorter.
	sort.Slice(values, func(left, right int) bool { return aqlLess(values[left], values[right]) })
}

func aqlLess(left, right any) bool {
	leftText, leftIsText := left.(string)
	rightText, rightIsText := right.(string)
	if leftIsText && rightIsText {
		return leftText < rightText
	}
	leftNumber, leftIsNumber := numericValue(left)
	rightNumber, rightIsNumber := numericValue(right)
	if !leftIsNumber || !rightIsNumber {
		return false
	}
	return leftNumber < rightNumber
}

func isAQLScalar(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	_, ok := numericValue(value)
	return ok
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, !math.IsNaN(number)
	default:
		return 0, false
	}
}

func uniqueValues(values []any) ([]any, error) {
	result := make([]any, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key, ok := distinctKey(value)
		if !ok {
			return nil, fmt.Errorf("DISTINCT pivot value is not a scalar: %T", value)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func distinctKey(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "null", true
	case string:
		return "s:" + typed, true
	case bool:
		if typed {
			return "b:true", true
		}
		return "b:false", true
	default:
		number, ok := numericValue(value)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("n:%g", number), true
	}
}

func scalarEqual(left, right any) bool {
	if leftText, ok := left.(string); ok {
		rightText, rightOK := right.(string)
		return rightOK && leftText == rightText
	}
	leftNumber, leftOK := numericValue(left)
	rightNumber, rightOK := numericValue(right)
	return leftOK && rightOK && leftNumber == rightNumber
}

func containsColumn(columns []string, value string) bool {
	for _, column := range columns {
		if column == value {
			return true
		}
	}
	return false
}
