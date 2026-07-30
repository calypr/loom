package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (r *physicalPlanRenderer) renderPredicate(predicate ir.PhysicalPredicate) (string, error) {
	if predicate.LeftExpression != nil {
		return r.renderSelectorPredicate(predicate)
	}
	if strings.ToUpper(strings.TrimSpace(predicate.Operator)) != "EQUALS" {
		return "", fmt.Errorf("unsupported physical filter operator %q", predicate.Operator)
	}
	if predicate.Right == nil {
		return "", fmt.Errorf("EQUALS filter requires a right value")
	}
	left, err := r.renderValue(predicate.Left)
	if err != nil {
		return "", err
	}
	right, err := r.renderValue(*predicate.Right)
	if err != nil {
		return "", err
	}
	return left + " == " + right, nil
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
	lines := make([]string, 0, len(subplan.Operations)*3+2)
	for index, operation := range subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalTraversalOp:
			traversal := operation.Traversal
			lines = append(lines,
				fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", indent+"  ", traversal.TargetVariable, traversal.EdgeVariable, traversal.Direction, traversal.SourceVariable, traversal.EdgeCollectionBindKey),
				fmt.Sprintf("%s  FILTER %s.label == @%s", indent+"  ", traversal.EdgeVariable, traversal.EdgeLabelBindKey),
				fmt.Sprintf("%s  FILTER %s.%s == @%s", indent+"  ", traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
				fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent+"  ", traversal.TargetVariable, traversal.TargetTypeBindKey),
			)
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
	value, err := r.renderExpression(subplan.Return)
	if err != nil {
		return "", err
	}
	lines = append(lines, indent+"    LIMIT 1", indent+"    RETURN "+value)
	return "LENGTH((\n" + strings.Join(lines, "\n") + "\n" + indent + "  )) > 0", nil
}
