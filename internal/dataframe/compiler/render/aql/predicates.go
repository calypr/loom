package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (r *physicalPlanRenderer) renderPredicate(predicate ir.PhysicalPredicate) (string, error) {
	if predicate.Correlation != nil {
		return r.renderCorrelationPredicate(*predicate.Correlation)
	}
	if predicate.LeftExpression != nil {
		return r.renderSelectorPredicate(predicate)
	}
	if predicate.Right == nil {
		return "", fmt.Errorf("physical filter operator %q requires a right value", predicate.Operator)
	}
	left, err := r.renderValue(predicate.Left)
	if err != nil {
		return "", err
	}
	right, err := r.renderValue(*predicate.Right)
	if err != nil {
		return "", err
	}
	switch strings.ToUpper(strings.TrimSpace(predicate.Operator)) {
	case "EQUALS":
		return left + " == " + right, nil
	case "IN":
		return left + " IN " + right, nil
	case "GT":
		return left + " > " + right, nil
	default:
		return "", fmt.Errorf("unsupported direct physical filter operator %q", predicate.Operator)
	}
}

// renderCorrelationPredicate keeps the Coding loop and owner value in one
// lexical scope. A system from Coding[0] can therefore never match a code
// from Coding[1], nor can a value from a different repeated component pass.
func (r *physicalPlanRenderer) renderCorrelationPredicate(correlation ir.PhysicalCorrelation) (string, error) {
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	owner := r.newInternalVariable("correlation_owner")
	coding := r.newInternalVariable("correlation_coding")
	codings, err := r.renderSelectorArrayFromSource(owner, correlation.KeySelector, false, false)
	if err != nil {
		return "", fmt.Errorf("correlation key selector: %w", err)
	}
	system, err := r.renderCorrelationScalar(coding, correlation.SystemSelector)
	if err != nil {
		return "", fmt.Errorf("correlation system selector: %w", err)
	}
	code, err := r.renderCorrelationScalar(coding, correlation.CodeSelector)
	if err != nil {
		return "", fmt.Errorf("correlation code selector: %w", err)
	}
	if correlation.SystemBindKey == "" || correlation.CodeBindKey == "" {
		return "", fmt.Errorf("correlation match binds are required")
	}
	values, err := r.renderCorrelationValues(owner, correlation.ValueSelector, correlation.ValueFallbacks)
	if err != nil {
		return "", err
	}
	unsupportedValues, err := r.renderCorrelationUnsupportedChoiceValues(owner, correlation)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`LENGTH(
  FOR %s IN %s
    FOR %s IN FLATTEN(%s)
      LET __correlation_system = %s
      LET __correlation_code = %s
      FILTER __correlation_system != null AND __correlation_system != ""
      FILTER __correlation_code != null AND __correlation_code != ""
      FILTER __correlation_system == @%s
	      FILTER __correlation_code == @%s
	      LET __correlation_values = %s
	      LET __correlation_unsupported_values = %s
	      FILTER LENGTH(__correlation_unsupported_values) == 0
	      FILTER LENGTH(FLATTEN(__correlation_values)) > 0
	      LIMIT 1
	      RETURN 1
) > 0`, owner, owners, coding, codings, system, code, correlation.SystemBindKey, correlation.CodeBindKey, values, unsupportedValues), nil
}

func (r *physicalPlanRenderer) renderCorrelationOwners(source ir.PhysicalValue, ownerSelector spec.Selector) (string, error) {
	raw, err := r.renderValue(source)
	if err != nil {
		return "", err
	}
	if source.Variable != "" && r.setVariables[source.Variable] != "" {
		if ownerSelector.CanonicalPath() != "" {
			owners, err := r.renderSelectorArrayFromSource(raw, ownerSelector, true, false)
			if err != nil {
				return "", err
			}
			return "FLATTEN(" + owners + ")", nil
		}
		return "(FOR __correlation_entry IN " + raw + " RETURN __correlation_entry.payload)", nil
	}
	if source.Variable != "" && len(source.Path) == 0 {
		raw += ".payload"
	}
	if ownerSelector.CanonicalPath() != "" {
		owners, err := r.renderSelectorArrayFromSource(raw, ownerSelector, false, false)
		if err != nil {
			return "", err
		}
		// A selector whose terminal step iterates an array returns one array
		// per source document. Correlation loops over owner items, so flatten
		// exactly that selector-result layer before pairing Coding/value data.
		return "FLATTEN(" + owners + ")", nil
	}
	return "[" + raw + "]", nil
}

func (r *physicalPlanRenderer) renderCorrelationScalar(source string, selector spec.Selector) (string, error) {
	values, err := r.renderSelectorArrayFromSource(source, selector, false, true)
	if err != nil {
		return "", err
	}
	return "FIRST(" + values + ")", nil
}

func (r *physicalPlanRenderer) renderCorrelationValues(source string, selector spec.Selector, fallbacks []spec.Selector) (string, error) {
	selectors := append([]spec.Selector{selector}, fallbacks...)
	values := make([]string, 0, len(selectors))
	for _, candidate := range selectors {
		value, err := r.renderSelectorArrayFromSource(source, candidate, false, false)
		if err != nil {
			return "", err
		}
		values = append(values, value)
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return "FIRST(FOR __correlation_candidate IN [" + strings.Join(values, ", ") + "] FILTER LENGTH(__correlation_candidate) > 0 RETURN __correlation_candidate)", nil
}

// renderCorrelationUnsupportedChoiceValues returns values observed on choice
// arms that are not part of the checked binding. Keeping this separate from
// the selected value expression lets projections expose an explicit typed
// incompatibility while predicates reject that row instead of treating it as
// ordinary absence.
func (r *physicalPlanRenderer) renderCorrelationUnsupportedChoiceValues(source string, correlation ir.PhysicalCorrelation) (string, error) {
	if len(correlation.ChoiceSelectors) == 0 || len(correlation.ChoiceArms) == 0 {
		return "[]", nil
	}
	parts := make([]string, 0, len(correlation.ChoiceSelectors))
	for _, selector := range correlation.ChoiceSelectors {
		if len(selector.Steps) == 0 || correlationChoiceArmAllowed(selector.Steps[0].Field, correlation.ChoiceArms) {
			continue
		}
		values, err := r.renderSelectorArrayFromSource(source, selector, false, false)
		if err != nil {
			return "", err
		}
		parts = append(parts, values)
	}
	if len(parts) == 0 {
		return "[]", nil
	}
	return "FLATTEN([" + strings.Join(parts, ", ") + "])", nil
}

func correlationChoiceArmAllowed(arm string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == strings.TrimSpace(arm) {
			return true
		}
	}
	return false
}

func (r *physicalPlanRenderer) renderSelectorPredicate(predicate ir.PhysicalPredicate) (string, error) {
	values, err := r.renderExpression(*predicate.LeftExpression)
	if err != nil {
		return "", err
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if operator == "EXISTS" {
		return "LENGTH(" + values + ") > 0", nil
	}
	if operator == "MISSING" {
		return "LENGTH(" + values + ") == 0", nil
	}
	if predicate.Right == nil {
		return "", fmt.Errorf("physical filter operator %q requires a right value", predicate.Operator)
	}
	right, err := r.renderValue(*predicate.Right)
	if err != nil {
		return "", err
	}
	valueVar := r.newInternalVariable("filter_value")
	match := ""
	switch operator {
	case "EQUALS":
		match = valueVar + " == " + right
	case "NOT_EQUALS":
		match = valueVar + " != " + right
	case "IN":
		match = "POSITION(" + right + ", " + valueVar + ")"
	case "CONTAINS_TEXT":
		match = "CONTAINS(TO_STRING(" + valueVar + "), " + right + ")"
	case "GT", "GTE", "LT", "LTE":
		left, comparisonRight := valueVar, right
		if predicate.ValueKind == spec.FilterDate || predicate.ValueKind == spec.FilterDateTime {
			left, comparisonRight = "DATE_TIMESTAMP("+valueVar+")", "DATE_TIMESTAMP("+right+")"
		}
		operatorText := map[string]string{"GT": ">", "GTE": ">=", "LT": "<", "LTE": "<="}[operator]
		match = left + " " + operatorText + " " + comparisonRight
	default:
		return "", fmt.Errorf("unsupported physical selector filter operator %q", predicate.Operator)
	}
	matching := "LENGTH(FOR " + valueVar + " IN " + values + " FILTER " + match + " LIMIT 1 RETURN 1)"
	quantifier := predicate.Quantifier
	if quantifier == "" {
		quantifier = spec.QuantifierAny
	}
	switch quantifier {
	case spec.QuantifierAny:
		return matching + " > 0", nil
	case spec.QuantifierNone:
		return matching + " == 0", nil
	case spec.QuantifierAll:
		return "LENGTH(" + values + ") > 0 AND LENGTH(FOR " + valueVar + " IN " + values + " FILTER NOT (" + match + ") LIMIT 1 RETURN 1) == 0", nil
	default:
		return "", fmt.Errorf("unsupported physical selector filter quantifier %q", quantifier)
	}
}

func (r *physicalPlanRenderer) renderPredicateExpression(predicate ir.PhysicalPredicateExpression, indent string) (string, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		return r.renderPredicate(*predicate.Comparison)
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate:
		parts := make([]string, 0, len(predicate.Children))
		for _, child := range predicate.Children {
			part, err := r.renderPredicateExpression(child, indent)
			if err != nil {
				return "", err
			}
			parts = append(parts, "("+part+")")
		}
		join := " AND "
		if predicate.Kind == ir.PhysicalAnyPredicate {
			join = " OR "
		}
		return strings.Join(parts, join), nil
	case ir.PhysicalNotPredicate:
		child, err := r.renderPredicateExpression(predicate.Children[0], indent)
		if err != nil {
			return "", err
		}
		return "NOT (" + child + ")", nil
	case ir.PhysicalExistsPredicate:
		return r.renderExistsSubplan(*predicate.Exists, indent)
	default:
		return "", fmt.Errorf("unsupported physical predicate kind %q", predicate.Kind)
	}
}

// renderExistsSubplan serializes a validated correlated subplan. EXISTS is
// always bounded: relationship matching is a semi-join, never a row-expanding
// traversal, so the renderer appends LIMIT 1 immediately before RETURN.
func (r *physicalPlanRenderer) renderExistsSubplan(subplan ir.PhysicalSubplan, indent string) (string, error) {
	value, err := r.renderSubplan(subplan, indent, true)
	if err != nil {
		return "", err
	}
	return "LENGTH(" + value + ") > 0", nil
}

func (r *physicalPlanRenderer) renderSubplan(subplan ir.PhysicalSubplan, indent string, bounded bool) (string, error) {
	if bounded && (subplan.Sort != nil || subplan.Unique) {
		return "", fmt.Errorf("EXISTS subplan cannot use projection-only sort or unique modifiers")
	}
	if subplan.Unique && subplan.Sort == nil {
		return "", fmt.Errorf("unique subplan requires a stable sort value")
	}
	lines := make([]string, 0, len(subplan.Operations)*3+2)
	for index, operation := range subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalCollectionScanOp:
			lines = append(lines, fmt.Sprintf("%sFOR %s IN @@%s", indent+"  ", operation.CollectionScan.Variable, operation.CollectionScan.CollectionBindKey))
		case ir.PhysicalTraversalOp:
			lines = append(lines, r.renderTraversalScan(*operation.Traversal, operation.Traversal.SourceVariable, indent+"  ")...)
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			rendered, err := r.renderScopeOperation(operation, indent+"    ")
			if err != nil {
				return "", fmt.Errorf("subplan operation %d (%s): %w", index, operation.Kind, err)
			}
			lines = append(lines, rendered...)
		default:
			return "", fmt.Errorf("subplan operation %d has unsupported render kind %q", index, operation.Kind)
		}
	}
	if subplan.Sort != nil {
		sort, err := r.renderValue(*subplan.Sort)
		if err != nil {
			return "", fmt.Errorf("subplan sort: %w", err)
		}
		lines = append(lines, indent+"    SORT "+sort)
	}
	value, err := r.renderExpression(subplan.Return)
	if err != nil {
		return "", err
	}
	if bounded {
		lines = append(lines, indent+"    LIMIT 1")
	}
	lines = append(lines, indent+"    RETURN "+value)
	result := "(\n" + strings.Join(lines, "\n") + "\n" + indent + "  )"
	if subplan.Unique {
		result = "SORTED_UNIQUE(" + result + ")"
	}
	return result, nil
}
