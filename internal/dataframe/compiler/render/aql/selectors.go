package aql

import (
	"fmt"
	"strings"
)

func (r *physicalPlanRenderer) renderExtract(expression PhysicalExpression) (string, error) {
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("EXTRACT expression is missing payload")
	}
	if extract.Prepared != nil {
		value := ""
		if r.preparedItem != "" {
			value = r.preparedItem + "." + extract.Prepared.Field
		} else {
			value = "(FOR __loom_prepared_value IN " + extract.Prepared.SetVariable + " RETURN __loom_prepared_value." + extract.Prepared.Field + ")"
		}
		if expression.Cardinality == PhysicalArrayCardinality {
			if extract.Distinct {
				return "SORTED_UNIQUE(FLATTEN(" + value + "))", nil
			}
			return value, nil
		}
		return "FIRST(FLATTEN(" + value + "))", nil
	}
	source, err := r.renderValue(extract.Source)
	if err != nil {
		return "", err
	}
	setSource := extract.Source.Variable != "" && r.setVariables[extract.Source.Variable] != ""
	// A set-valued source is a collection of resource documents.  Its
	// PhysicalValue may carry the document payload path from semantic lowering,
	// but that path cannot be applied to the collection itself (`set.payload`
	// is null in AQL).  The selector renderer already projects each set item
	// through `item.payload`, so pass the collection variable as the loop source.
	if setSource {
		source = extract.Source.Variable
	}
	if len(extract.Fallbacks) == 0 && extract.Selector.Filter == nil {
		switch extract.ExecutionMode {
		case PhysicalSelectorDirectScalar:
			if !setSource && expression.Cardinality != PhysicalArrayCardinality {
				return compileDirectExpr(source, extract.Selector.Steps), nil
			}
		case PhysicalSelectorConditionalArray:
			if setSource {
				break
			}
			values, err := r.renderConditionalSelectorArray(source, extract.Selector)
			if err != nil {
				return "", err
			}
			if expression.Cardinality == PhysicalArrayCardinality {
				if extract.Distinct {
					return "SORTED_UNIQUE(" + values + ")", nil
				}
				return values, nil
			}
			return "FIRST(" + values + ")", nil
		}
	}
	arrays := make([]string, 0, 1+len(extract.Fallbacks))
	for _, selector := range append([]Selector{extract.Selector}, extract.Fallbacks...) {
		array, err := r.renderSelectorArrayFromSource(source, selector, setSource)
		if err != nil {
			return "", err
		}
		arrays = append(arrays, array)
	}
	values := arrays[0]
	if len(arrays) > 1 {
		values = "FLATTEN([" + strings.Join(arrays, ", ") + "])"
	}
	if expression.Cardinality == PhysicalArrayCardinality {
		if extract.Distinct {
			return "SORTED_UNIQUE(" + values + ")", nil
		}
		return values, nil
	}
	if !setSource && len(arrays) == 1 && extract.Selector.Filter == nil && selectorHasNoArrays(extract.Selector) {
		return compileDirectExpr(source, extract.Selector.Steps), nil
	}
	return "FIRST(" + values + ")", nil
}

func (r *physicalPlanRenderer) renderSelectorByMode(source string, selector Selector, mode PhysicalSelectorExecutionMode) (string, error) {
	if mode == PhysicalSelectorDirectScalar && selectorHasNoArrays(selector) && selector.Filter == nil {
		return "(FOR __loom_value IN [" + compileDirectExpr(source, selector.Steps) + "] FILTER __loom_value != null RETURN __loom_value)", nil
	}
	if mode == PhysicalSelectorConditionalArray && selectorHasIteratedArray(selector) && selector.Filter == nil {
		return r.renderConditionalSelectorArray(source, selector)
	}
	return r.renderSelectorArrayFromSource(source, selector, false)
}

func (r *physicalPlanRenderer) renderConditionalSelectorArray(source string, selector Selector) (string, error) {
	if len(selector.Steps) == 0 {
		return "", fmt.Errorf("selector is required")
	}
	prefix, last := selector.Steps[:len(selector.Steps)-1], selector.Steps[len(selector.Steps)-1]
	lines := make([]string, 0, len(prefix)+3)
	current := source
	for index, step := range prefix {
		next := fmt.Sprintf("__loom_selector_%d", index)
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index), "FILTER "+next+" != null")
		default:
			lines = append(lines, fmt.Sprintf("LET %s = %s.%s", next, current, step.Field), "FILTER "+next+" != null")
		}
		current = next
	}
	lines = append(lines, "LET __value = "+extractFinalExpr(current, last), "FILTER __value != null", "RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )", nil
}

func (r *physicalPlanRenderer) renderSelectorArrayFromSource(source string, selector Selector, setSource bool) (string, error) {
	if len(selector.Steps) == 0 {
		return "", fmt.Errorf("selector is required")
	}
	prefix, last := selector.Steps[:len(selector.Steps)-1], selector.Steps[len(selector.Steps)-1]
	lines, current := []string{"FOR __root IN [" + source + "]"}, "__root"
	if setSource {
		lines, current = []string{"FOR __item IN " + source, "  FOR __root IN [__item.payload]"}, "__root"
	}
	for index, step := range prefix {
		next := fmt.Sprintf("__s%d", index)
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index), "  FILTER "+next+" != null")
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, current, step.Field), "  FILTER "+next+" != null")
		}
		current = next
	}
	if selector.Filter != nil {
		key := r.newInternalBindKey("selector_contains")
		r.bindVars[key] = selector.Filter.Needle
		lines = append(lines, fmt.Sprintf("  FILTER CONTAINS(%s.%s ? %s.%s : \"\", @%s)", current, selector.Filter.Field, current, selector.Filter.Field, key))
	}
	lines = append(lines, "  LET __value = "+extractFinalExpr(current, last), "  FILTER __value != null", "  RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )", nil
}

func (r *physicalPlanRenderer) renderDerivedLet(derived PhysicalDerivedLet) (string, error) {
	if strings.ToUpper(strings.TrimSpace(derived.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
		return "", fmt.Errorf("unsupported physical derived LET operator %q", derived.Operator)
	}
	if len(derived.Inputs) < 3 {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires one or more scope values plus paths and unrestricted inputs")
	}

	paths := derived.Inputs[len(derived.Inputs)-2]
	unrestricted := derived.Inputs[len(derived.Inputs)-1]
	if paths.BindKey == "" || paths.Variable != "" || unrestricted.BindKey == "" || unrestricted.Variable != "" {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires paths and unrestricted bind inputs")
	}
	pathsExpression, err := r.renderValue(paths)
	if err != nil {
		return "", err
	}
	unrestrictedExpression, err := r.renderValue(unrestricted)
	if err != nil {
		return "", err
	}

	scopeChecks := make([]string, 0, len(derived.Inputs)-2)
	for _, input := range derived.Inputs[:len(derived.Inputs)-2] {
		if input.Variable == "" || input.BindKey != "" {
			return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED scope inputs must be variable paths")
		}
		scopeValue, err := r.renderValue(input)
		if err != nil {
			return "", err
		}
		scopeChecks = append(scopeChecks, scopeValue+" IN "+pathsExpression)
	}

	scopeExpression := strings.Join(scopeChecks, " AND ")
	if len(scopeChecks) > 1 {
		scopeExpression = "(" + scopeExpression + ")"
	}
	return unrestrictedExpression + " == true OR " + scopeExpression, nil
}

func (r *physicalPlanRenderer) renderReturn(returnOp PhysicalReturn) (string, error) {
	if len(returnOp.Projections) == 0 {
		return "{}", nil
	}
	projections := make([]string, 0, len(returnOp.Projections))
	for index, projection := range returnOp.Projections {
		nameBindKey := r.newInternalBindKey(fmt.Sprintf("projection_%d_name", index))
		r.bindVars[nameBindKey] = projection.Name
		var value string
		var err error
		if projection.Expression != nil {
			value, err = r.renderExpression(*projection.Expression)
		} else {
			value, err = r.renderValue(projection.Value)
		}
		if err != nil {
			return "", err
		}
		projections = append(projections, fmt.Sprintf("[@%s]: %s", nameBindKey, value))
	}
	return "{ " + strings.Join(projections, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderValue(value PhysicalValue) (string, error) {
	if value.BindKey != "" {
		if _, collectionBinding := r.collectionKeys[value.BindKey]; collectionBinding {
			return "", fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", value.BindKey)
		}
		return "@" + value.BindKey, nil
	}
	if value.Variable == "" {
		return "", fmt.Errorf("physical value has no variable or bind key")
	}
	if len(value.Path) == 0 {
		return value.Variable, nil
	}
	return value.Variable + "." + strings.Join(value.Path, "."), nil
}
