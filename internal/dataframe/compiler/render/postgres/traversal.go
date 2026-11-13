package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

var traversalResourceColumns = []string{
	"auth_resource_path", "dataset_generation", "logical_id", "payload",
	"project", "recorded_at", "resource_seq", "resource_type",
}

const requiredMatchResultValue = 1

type validatedTraversal struct {
	traversal     ir.PhysicalTraversal
	label         string
	labelIndex    int
	sourceType    string
	targetType    string
	anchoredType  string
	anchoredID    string
	reachedType   string
	reachedID     string
	anchoredIndex int
	reachedIndex  int
}

func (r *planRenderer) validateTraversalContract(
	plan ir.PhysicalPlan,
	traversal ir.PhysicalTraversal,
	source rowScope,
) (validatedTraversal, error) {
	switch traversal.Direction {
	case ir.PhysicalOutbound, ir.PhysicalInbound:
	case ir.PhysicalAny:
		return validatedTraversal{}, fmt.Errorf("%w: ANY-direction traversal has no proven PostgreSQL contract (the AQL generic navigation surface refuses it too, render/aql/navigation_layout.go:394-396)", ErrUnsupportedPostgresPlan)
	default:
		return validatedTraversal{}, fmt.Errorf("%w: unknown traversal direction %q", ErrUnsupportedPostgresPlan, string(traversal.Direction))
	}
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		return validatedTraversal{}, fmt.Errorf("%w: endpoint-lookup strategy has no proven PostgreSQL contract yet; native typed-identity joins are the only registered shape", ErrUnsupportedPostgresPlan)
	}
	if traversal.Strategy != "" && traversal.Strategy != ir.PhysicalTraversalNative {
		return validatedTraversal{}, fmt.Errorf("%w: unknown traversal strategy %q", ErrUnsupportedPostgresPlan, string(traversal.Strategy))
	}
	expectedField := "to_type"
	if traversal.Direction == ir.PhysicalInbound {
		expectedField = "from_type"
	}
	if traversal.EdgeTargetTypeField != expectedField {
		return validatedTraversal{}, fmt.Errorf("%w: %s traversal must carry EdgeTargetTypeField %q, got %q",
			ErrUnsupportedPostgresPlan, string(traversal.Direction), expectedField, traversal.EdgeTargetTypeField)
	}
	if traversal.EdgeVariable == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal must name its edge variable (the shared lowering contract always does; render/aql/navigation_layout.go:404)", ErrUnsupportedPostgresPlan)
	}
	if traversal.EdgeCollectionBindKey == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal must name the edge collection bind", ErrUnsupportedPostgresPlan)
	}
	if collection, ok := plan.BindVars[traversal.EdgeCollectionBindKey].(string); !ok || collection != "fhir_edge" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal edge collection bind %q must be bound to \"fhir_edge\" (the only registered edge relation; render/aql/navigation_layout.go:407-409)",
			ErrUnsupportedPostgresPlan, traversal.EdgeCollectionBindKey)
	}
	label, ok := plan.BindVars[traversal.EdgeLabelBindKey].(string)
	if !ok || strings.TrimSpace(label) == "" {
		return validatedTraversal{}, fmt.Errorf("%w: edge label bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, traversal.EdgeLabelBindKey)
	}
	targetType, ok := plan.BindVars[traversal.TargetTypeBindKey].(string)
	if !ok || strings.TrimSpace(targetType) == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal target type bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, traversal.TargetTypeBindKey)
	}
	if traversal.SourceTypeBindKey != "" {
		if bound, ok := plan.BindVars[traversal.SourceTypeBindKey].(string); !ok || bound != source.resourceType {
			return validatedTraversal{}, fmt.Errorf("%w: traversal source type bind does not match the current row resource type %q",
				ErrUnsupportedPostgresPlan, source.resourceType)
		}
	}

	verified := validatedTraversal{traversal: traversal, label: label, sourceType: source.resourceType, targetType: targetType}
	if traversal.Direction == ir.PhysicalOutbound {
		verified.anchoredType, verified.anchoredID = "source_resource_type", "source_logical_id"
		verified.reachedType, verified.reachedID = "target_resource_type", "target_logical_id"
	} else {
		verified.anchoredType, verified.anchoredID = "target_resource_type", "target_logical_id"
		verified.reachedType, verified.reachedID = "source_resource_type", "source_logical_id"
	}
	verified.labelIndex = r.textParameter(label, "text:"+label)
	verified.anchoredIndex = r.textParameter(source.resourceType, "text:"+source.resourceType)
	verified.reachedIndex = r.textParameter(targetType, "text:"+targetType)
	return verified, nil
}

func (r *planRenderer) edgeOccurrencePredicates(
	verified validatedTraversal,
	edgeAlias, targetAlias, sourceAlias string,
) (edgeWhere string, targetOn string) {
	edgeColumns := r.storage.Edge
	resourceColumns := r.storage.Resource

	edgeWhere = r.scopedEdgePredicate(edgeAlias)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn("label").SQLText() +
		" = $" + strconv.Itoa(verified.labelIndex)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.anchoredType).SQLText() +
		" = $" + strconv.Itoa(verified.anchoredIndex)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.anchoredID).SQLText() +
		" = " + sourceAlias + "." + resourceColumns.MustColumn("logical_id").SQLText()
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.reachedType).SQLText() +
		" = $" + strconv.Itoa(verified.reachedIndex)

	targetOn = r.scopedResourcePredicate(targetAlias, verified.reachedIndex) +
		" AND " + targetAlias + "." + resourceColumns.MustColumn("logical_id").SQLText() +
		" = " + edgeAlias + "." + edgeColumns.MustColumn(verified.reachedID).SQLText()
	return edgeWhere, targetOn
}

func (r *planRenderer) renderTraversal(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal, routeInner bool) error {
	source, ok := r.rows[traversal.SourceVariable]
	if !ok || source.derivedValue != "" {
		return fmt.Errorf("%w: traversal source variable %q does not bind a readable row (edge variables, set variables, and computed unnest values are not readable)",
			ErrUnsupportedPostgresPlan, traversal.SourceVariable)
	}
	verified, err := r.validateTraversalContract(plan, traversal, source)
	if err != nil {
		return err
	}

	seq := r.nextTraversalSequence()
	edgeAlias := "e" + strconv.Itoa(seq)
	innerAlias := "x" + strconv.Itoa(seq)
	lateralAlias := "r" + strconv.Itoa(seq)
	edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, innerAlias, source.alias)

	if routeInner {
		join := "INNER JOIN " + r.storage.Edge.Identifier().SQLText() + " AS " + edgeAlias +
			" ON " + edgeWhere +
			" INNER JOIN " + r.storage.Resource.Identifier().SQLText() + " AS " + innerAlias +
			" ON " + targetOn
		r.joins = append(r.joins, join)
		r.edgeAliases = append(r.edgeAliases, edgeAlias)
		r.rows[traversal.TargetVariable] = rowScope{alias: innerAlias, resourceType: verified.targetType}
		return r.window.rememberEdge(traversal.EdgeVariable, edgeAlias)
	}

	projection := make([]string, 0, len(traversalResourceColumns)+1)
	for _, column := range traversalResourceColumns {
		name := r.storage.Resource.MustColumn(column).SQLText()
		projection = append(projection, innerAlias+"."+name+" AS "+name)
	}
	projection = append(projection, edgeAlias+"."+r.storage.Edge.MustColumn("edge_seq").SQLText()+` AS "edge_seq"`)

	join := "LEFT JOIN LATERAL (SELECT " + strings.Join(projection, ", ") +
		" FROM " + r.storage.Edge.Identifier().SQLText() + " AS " + edgeAlias +
		" INNER JOIN " + r.storage.Resource.Identifier().SQLText() + " AS " + innerAlias +
		" ON " + targetOn +
		" WHERE " + edgeWhere +
		") AS " + lateralAlias + " ON TRUE"
	r.joins = append(r.joins, join)
	r.edgeAliases = append(r.edgeAliases, lateralAlias)
	r.rows[traversal.TargetVariable] = rowScope{alias: lateralAlias, resourceType: verified.targetType, optional: true}
	return r.window.rememberEdge(traversal.EdgeVariable, lateralAlias)
}

func (r *planRenderer) renderRequiredMatchExists(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicateExpression,
) (predicateFragment, error) {
	if predicate.Exists == nil {
		return predicateFragment{}, fmt.Errorf("%w: EXISTS predicate has no subplan payload", ErrUnsupportedPostgresPlan)
	}
	subplan := *predicate.Exists
	if len(subplan.Captures) != 1 {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan must capture exactly one correlation anchor, got %d captures",
			ErrUnsupportedPostgresPlan, len(subplan.Captures))
	}
	anchorVariable := subplan.Captures[0]
	anchor, ok := r.rows[anchorVariable]
	if !ok || anchor.derivedValue != "" || anchor.optional {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan capture %q does not bind a readable row (edge variables, computed unnest values, and OPTIONAL LEFT JOIN LATERAL rows cannot anchor a correlation; lower/required_match.go always captures the root row, and NULL-extended anchor columns would let subplan FILTERs compare absent rows)",
			ErrUnsupportedPostgresPlan, anchorVariable)
	}
	if err := validateRequiredMatchReturn(plan, subplan); err != nil {
		return predicateFragment{}, err
	}

	rows := map[string]rowScope{anchorVariable: anchor}
	fromParts := make([]string, 0, 4)
	whereParts := make([]string, 0, 4)
	sawTraversal := false
	nextSource := rowScope{alias: anchor.alias, resourceType: anchor.resourceType}
	nextSourceVariable := anchorVariable
	for index, operation := range subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalTraversalOp:
			if operation.Traversal == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: TRAVERSAL payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			traversal := *operation.Traversal
			if traversal.SourceVariable != nextSourceVariable {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: traversal must anchor on %q (the capture for the first step, the previous step's reached row thereafter), got source %q",
					ErrUnsupportedPostgresPlan, index, nextSourceVariable, traversal.SourceVariable)
			}
			verified, err := r.validateTraversalContract(plan, traversal, nextSource)
			if err != nil {
				return predicateFragment{}, fmt.Errorf("required-match subplan operation %d: %w", index, err)
			}
			seq := r.nextTraversalSequence()
			edgeAlias := "e" + strconv.Itoa(seq)
			targetAlias := "x" + strconv.Itoa(seq)
			edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, nextSource.alias)
			if !sawTraversal {
				fromParts = append(fromParts, "FROM "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias)
				whereParts = append(whereParts, edgeWhere)
			} else {
				fromParts = append(fromParts,
					"INNER JOIN "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias+" ON "+edgeWhere)
			}
			fromParts = append(fromParts,
				"INNER JOIN "+r.storage.Resource.Identifier().SQLText()+" AS "+targetAlias+" ON "+targetOn)
			rows[traversal.TargetVariable] = rowScope{alias: targetAlias, resourceType: verified.targetType}
			nextSource = rows[traversal.TargetVariable]
			nextSourceVariable = traversal.TargetVariable
			sawTraversal = true
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: FILTER payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			if !sawTraversal {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: a FILTER may only follow a traversal step (the match is over reached child rows, and subplan scope is structural)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.Filter.Expression == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: typed match filters must use the Expression form",
					ErrUnsupportedPostgresPlan, index)
			}
			clause, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, rows, true)
			if err != nil {
				return predicateFragment{}, fmt.Errorf("required-match subplan operation %d: %w", index, err)
			}
			whereParts = append(whereParts, clause.sql)
		default:
			return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d has kind %q; only chained TRAVERSAL steps and typed FILTER operations are registered, and plan-authored scope operations (including the canonical project/generation/auth block) are refused because scope is established structurally from the re-read binds",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
	}
	if !sawTraversal {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan must contain at least one traversal step", ErrUnsupportedPostgresPlan)
	}
	body := "EXISTS (SELECT 1 " + strings.Join(fromParts, " ") + " WHERE " + strings.Join(whereParts, " AND ") + ")"
	return predicateFragment{sql: body}, nil
}

func validateRequiredMatchReturn(plan ir.PhysicalPlan, subplan ir.PhysicalSubplan) error {
	expression := subplan.Return
	if expression.Kind != ir.PhysicalValueExpression || expression.Value == nil ||
		expression.Value.BindKey == "" || expression.Value.Variable != "" || len(expression.Value.Path) != 0 {
		return fmt.Errorf("%w: required-match subplan return must be the canonical scalar VALUE expression over one bind (the lower/required_match.go constant-1 form)",
			ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: required-match subplan return must be SCALAR cardinality with PRESERVE_NULL null behavior (the lower/required_match.go constant-1 form)",
			ErrUnsupportedPostgresPlan)
	}
	if subplan.Sort != nil || subplan.Unique || subplan.DistinctBy != nil {
		return fmt.Errorf("%w: required-match subplan cannot carry projection modifiers", ErrUnsupportedPostgresPlan)
	}
	if value, ok := plan.BindVars[expression.Value.BindKey].(int); !ok || value != requiredMatchResultValue {
		return fmt.Errorf("%w: required-match subplan return bind %q must be bound to the integer constant %d",
			ErrUnsupportedPostgresPlan, expression.Value.BindKey, requiredMatchResultValue)
	}
	return nil
}

func (r *planRenderer) nextTraversalSequence() int {
	r.traversalSeq++
	return r.traversalSeq
}

func (r *planRenderer) resolveValueInRows(rows map[string]rowScope, value ir.PhysicalValue) (string, error) {
	scope, ok := rows[value.Variable]
	if !ok || scope.derivedValue != "" {
		return "", fmt.Errorf("%w: variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 1 {
		if column := canonicalKeyColumn(value.Path[0]); resourceFilterColumns[column] {
			return scope.alias + "." + r.storage.Resource.MustColumn(column).SQLText(), nil
		}
	}
	return r.documentPathExpression(scope.alias, value.Path)
}

func (r *planRenderer) renderPredicateFragmentScoped(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicateExpression,
	rows map[string]rowScope,
	allowSelectorExtract bool,
) (predicateFragment, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return predicateFragment{}, fmt.Errorf("%w: COMPARISON predicate has no payload", ErrUnsupportedPostgresPlan)
		}
		return r.renderComparisonFragmentScoped(plan, *predicate.Comparison, rows, allowSelectorExtract)
	case ir.PhysicalNotPredicate:
		if len(predicate.Children) != 1 {
			return predicateFragment{}, fmt.Errorf("%w: NOT requires exactly one child", ErrUnsupportedPostgresPlan)
		}
		child, err := r.renderPredicateFragmentScoped(plan, predicate.Children[0], rows, allowSelectorExtract)
		if err != nil {
			return predicateFragment{}, err
		}
		if child.mayYieldNull {
			return predicateFragment{sql: "COALESCE(NOT (" + child.sql + "), true)"}, nil
		}
		return predicateFragment{sql: "NOT (" + child.sql + ")"}, nil
	case ir.PhysicalAllPredicate:
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ALL requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragmentScoped(plan, child, rows, allowSelectorExtract)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " AND "), mayYieldNull: mayYieldNull}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: predicate kind %q is not part of the registered traversal predicate surface",
			ErrUnsupportedPostgresPlan, string(predicate.Kind))
	}
}

func (r *planRenderer) renderComparisonFragmentScoped(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicate,
	rows map[string]rowScope,
	allowSelectorExtract bool,
) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.LeftExpression != nil {
		if !allowSelectorExtract {
			return predicateFragment{}, fmt.Errorf("%w: selector-expression and correlated predicates are not yet implemented for PostgreSQL",
				ErrUnsupportedPostgresPlan)
		}
		return r.renderSelectorMatchFragment(plan, predicate, rows)
	}
	if predicate.Quantifier != "" {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	if predicate.Left.BindKey != "" {
		return predicateFragment{}, fmt.Errorf("%w: filter left side must be a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	var left string
	var err error
	switch {
	case predicate.Left.Variable != "":
		left, err = r.resolveValueInRows(rows, predicate.Left)
		if err != nil {
			return predicateFragment{}, err
		}
	default:
		return predicateFragment{}, fmt.Errorf("%w: filter must read a row path or a registered selector expression",
			ErrUnsupportedPostgresPlan)
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	switch operator {
	case "EXISTS", "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: %s must not carry a right value", ErrUnsupportedPostgresPlan, operator)
		}
		if isPayloadExpression(left) {
			presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
			if operator == "EXISTS" {
				return predicateFragment{sql: presence + " <> 'null'"}, nil
			}
			return predicateFragment{sql: presence + " = 'null'"}, nil
		}
		if operator == "EXISTS" {
			return predicateFragment{sql: left + " IS NOT NULL"}, nil
		}
		return predicateFragment{sql: left + " IS NULL"}, nil
	case "EQUALS", "NOT_EQUALS", "GT", "GTE", "LT", "LTE", "IN":
	default:
		return predicateFragment{}, fmt.Errorf("%w: filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	if !isPayloadExpression(left) {
		return r.renderColumnComparison(left, operator, predicate.ValueKind, value)
	}
	return r.renderPayloadComparison(left, operator, predicate.ValueKind, value)
}

func (r *planRenderer) renderSelectorMatchFragment(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicate,
	rows map[string]rowScope,
) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Quantifier != "" && !predicate.Quantifier.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not part of the registered traversal predicate surface",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	quantifier := predicate.Quantifier
	if quantifier == "" {
		quantifier = spec.QuantifierAny
	}
	values, err := r.selectorValuesExpression(*predicate.LeftExpression, rows)
	if err != nil {
		return predicateFragment{}, err
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	length := "jsonb_array_length(" + values + ")"
	switch operator {
	case "EXISTS":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: EXISTS must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "(" + length + " > 0)"}, nil
	case "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: MISSING must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "(" + length + " = 0)"}, nil
	case "EQUALS", "NOT_EQUALS", "IN", "GT", "GTE", "LT", "LTE":
	case "CONTAINS_TEXT":
		return predicateFragment{}, fmt.Errorf("%w: selector CONTAINS_TEXT match needs the AQL TO_STRING/CONTAINS reading (render/aql/predicates.go:243) and is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if !predicate.ValueKind.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: selector filter value kind %q is invalid", ErrUnsupportedPostgresPlan, string(predicate.ValueKind))
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	match, err := r.selectorMatchExpression("__sv.v", operator, predicate.ValueKind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	anyMatch := "EXISTS (SELECT 1 FROM jsonb_array_elements(" + values + ") AS __sv(v) WHERE " + match + ")"
	switch quantifier {
	case spec.QuantifierAny:
		return predicateFragment{sql: "(" + anyMatch + ")"}, nil
	case spec.QuantifierNone:
		return predicateFragment{sql: "(NOT " + anyMatch + ")"}, nil
	default:
		mismatch := "EXISTS (SELECT 1 FROM jsonb_array_elements(" + values + ") AS __sv(v) WHERE NOT (" + match + "))"
		return predicateFragment{sql: "(" + length + " > 0 AND NOT " + mismatch + ")"}, nil
	}
}

func (r *planRenderer) selectorValuesExpression(
	expression ir.PhysicalExpression,
	rows map[string]rowScope,
) (string, error) {
	if expression.Kind != ir.PhysicalExtractExpression {
		return "", fmt.Errorf("%w: required-match filter left side must be an EXTRACT selector expression, got kind %q",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: required-match filter selector is missing its EXTRACT payload", ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: required-match filter selector must be the ARRAY-cardinality form lower/required_match.go emits; got %q",
			ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: required-match filter selector must carry the EMPTY_ON_NULL null behavior lower/required_match.go emits; got %q",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return "", fmt.Errorf("%w: required-match filter selector mode CONDITIONAL_ARRAY requires repeated-step rendering, which is not registered for PostgreSQL", ErrUnsupportedPostgresPlan)
	default:
		return "", fmt.Errorf("%w: required-match filter selector execution mode %q is unknown",
			ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Distinct {
		return "", fmt.Errorf("%w: required-match filter selector DISTINCT changes element multiplicity the quantified match does not model (render/aql/predicates.go reads the raw value list)", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return "", fmt.Errorf("%w: required-match filter selector fallbacks are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.Prepared != nil {
		return "", fmt.Errorf("%w: required-match filter selector prepared-set references are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: required-match filter selector unit normalization is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return "", fmt.Errorf("%w: required-match filter selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Selector.Steps) == 0 {
		return "", fmt.Errorf("%w: required-match filter selector is empty", ErrUnsupportedPostgresPlan)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		switch {
		case step.Field == "":
			return "", fmt.Errorf("%w: required-match filter selector step %d has no property", ErrUnsupportedPostgresPlan, index)
		case step.Iterate:
			return "", fmt.Errorf("%w: required-match filter selector repeated ([]) step %d requires array rendering, which is not registered", ErrUnsupportedPostgresPlan, index)
		case step.Index != nil:
			return "", fmt.Errorf("%w: required-match filter selector indexed step %d requires array rendering, which is not registered", ErrUnsupportedPostgresPlan, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: required-match filter selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
		path = append(path, step.Field)
	}
	if extract.Source.BindKey != "" {
		return "", fmt.Errorf("%w: required-match filter selector source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf(`%w: required-match filter selector source must be the row document at ["payload"], the exact form lower/required_match.go emits; got %v`,
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	scope, ok := rows[extract.Source.Variable]
	if !ok {
		return "", fmt.Errorf("%w: required-match filter selector variable %q does not bind a readable subplan row",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" {
		return "", fmt.Errorf("%w: required-match filter selector must name the schema resource type it was validated against", ErrUnsupportedPostgresPlan)
	}
	if extract.ResourceType != scope.resourceType {
		return "", fmt.Errorf("%w: required-match filter selector validates against resource type %q, but the row is joined as %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	extract_, err := r.documentPathExpression(scope.alias, path)
	if err != nil {
		return "", err
	}
	document := "(" + extract_ + ")"
	return "(CASE WHEN " + document + " IS NULL OR jsonb_typeof(" + document + ") = 'null' THEN '[]'::jsonb" +
		" ELSE jsonb_build_array(" + document + ") END)", nil
}
