package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *planRenderer) acceptPostWindowTraversal(plan ir.PhysicalPlan, index int) (handled bool, next int, err error) {
	_ = r
	traversal := plan.Operations[index].Traversal
	source, ok := r.rows[traversal.SourceVariable]
	if !ok || source.derivedValue != "" {
		return false, 0, nil
	}
	if shapeErr := validateDeadPostWindowTraversal(plan, *traversal, source); shapeErr != nil {
		return false, 0, nil
	}
	after := index + 1
	if blockErr := matchCanonicalScopeBlock(plan.Operations, &after,
		[]string{traversal.EdgeVariable, traversal.TargetVariable}); blockErr != nil {
		return false, 0, nil
	}
	variables := map[string]bool{traversal.EdgeVariable: true, traversal.TargetVariable: true}
	for scan := after; scan < len(plan.Operations); scan++ {
		if operationReferencesVariables(plan.Operations[scan], variables) {
			return false, 0, fmt.Errorf("%w: operation %d reads %q or %q, so the post-window traversal is not dead; only traversals whose LET has no consumer in the AQL reading are registered after the execution window",
				ErrUnsupportedPostgresPlan, scan, traversal.EdgeVariable, traversal.TargetVariable)
		}
	}
	return true, after, nil
}

func acceptPostWindowSet(plan ir.PhysicalPlan, set ir.PhysicalSet) bool {
	return set.Reduction != nil || sliceConsumesSetVariable(plan, set.Variable) ||
		aggregateConsumesSetVariable(plan, set.Variable)
}

func aggregateConsumesSetVariable(plan ir.PhysicalPlan, setVariable string) bool {
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for index := range operation.Return.Projections {
			expression := operation.Return.Projections[index].Expression
			if expression != nil && aggregateReadsSet(expression.Aggregate, setVariable) {
				return true
			}
		}
	}
	return false
}

func aggregateReadsSet(aggregate *ir.PhysicalAggregate, setVariable string) bool {
	if aggregate == nil || setVariable == "" {
		return false
	}
	switch aggregate.Operation {
	case ir.PhysicalSumAggregate, ir.PhysicalCountAggregate, ir.PhysicalMinAggregate, ir.PhysicalMaxAggregate:
	default:
		return false
	}
	return aggregate.Source.Variable == setVariable && aggregate.Source.BindKey == "" && len(aggregate.Source.Path) == 0
}

func operationReferencesVariables(operation ir.PhysicalOperation, variables map[string]bool) bool {
	switch operation.Kind {
	case ir.PhysicalFilterOp:
		if operation.Filter == nil {
			return true
		}
		if operation.Filter.Expression != nil {
			return predicateReferencesVariables(*operation.Filter.Expression, variables)
		}
		return comparisonReferencesVariables(operation.Filter.Predicate, variables)
	case ir.PhysicalDerivedLetOp:
		if operation.DerivedLet == nil {
			return true
		}
		if variables[operation.DerivedLet.Variable] {
			return true
		}
		for _, input := range operation.DerivedLet.Inputs {
			if valueReferencesVariables(input, variables) {
				return true
			}
		}
		return false
	case ir.PhysicalTraversalOp:
		if operation.Traversal == nil {
			return true
		}
		return variables[operation.Traversal.SourceVariable] ||
			variables[operation.Traversal.EdgeVariable] ||
			variables[operation.Traversal.TargetVariable]
	case ir.PhysicalSetOp:
		if operation.Set == nil {
			return true
		}
		set := operation.Set
		if variables[set.Variable] || variables[set.SourceSetVariable] || variables[set.ItemVariable] {
			return true
		}
		return subplanReferencesVariables(set.Subplan, variables)
	case ir.PhysicalUnnestOp:
		if operation.Unnest == nil {
			return true
		}
		unnest := operation.Unnest
		if variables[unnest.OutputVariable] || variables[unnest.Ordinality] || variables[unnest.HasItemVariable] {
			return true
		}
		if expressionReferencesVariables(unnest.Expression, variables) {
			return true
		}
		for _, ancestor := range unnest.Ancestors {
			if variables[ancestor.Variable] {
				return true
			}
		}
		return false
	case ir.PhysicalSortOp:
		if operation.Sort == nil {
			return true
		}
		for _, key := range operation.Sort.Keys {
			if valueReferencesVariables(key, variables) {
				return true
			}
		}
		return false
	case ir.PhysicalRootScanOp, ir.PhysicalLimitOp:
		return false
	case ir.PhysicalReturnOp:
		if operation.Return == nil {
			return true
		}
		for index := range operation.Return.Projections {
			if projectionReferencesVariables(operation.Return.Projections[index], variables) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func subplanReferencesVariables(subplan ir.PhysicalSubplan, variables map[string]bool) bool {
	for _, capture := range subplan.Captures {
		if variables[capture] {
			return true
		}
	}
	for _, operation := range subplan.Operations {
		if operationReferencesVariables(operation, variables) {
			return true
		}
	}
	if subplan.Sort != nil && valueReferencesVariables(*subplan.Sort, variables) {
		return true
	}
	if subplan.DistinctBy != nil && valueReferencesVariables(*subplan.DistinctBy, variables) {
		return true
	}
	return expressionReferencesVariables(subplan.Return, variables)
}

func projectionReferencesVariables(projection ir.PhysicalProjection, variables map[string]bool) bool {
	if valueReferencesVariables(projection.Value, variables) {
		return true
	}
	if projection.Expression != nil && expressionReferencesVariables(*projection.Expression, variables) {
		return true
	}
	if projection.Presence != nil && valueReferencesVariables(projection.Presence.Source, variables) {
		return true
	}
	return false
}

func expressionReferencesVariables(expression ir.PhysicalExpression, variables map[string]bool) bool {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return expression.Value != nil && valueReferencesVariables(*expression.Value, variables)
	case ir.PhysicalLiteralExpression:
		return false
	case ir.PhysicalExtractExpression:
		return expression.Extract != nil && valueReferencesVariables(expression.Extract.Source, variables)
	case ir.PhysicalAggregateExpression:
		aggregate := expression.Aggregate
		if aggregate == nil {
			return true
		}
		if valueReferencesVariables(aggregate.Source, variables) {
			return true
		}
		if aggregate.Value != nil && expressionReferencesVariables(*aggregate.Value, variables) {
			return true
		}
		if aggregate.Predicate != nil && predicateReferencesVariables(*aggregate.Predicate, variables) {
			return true
		}
		if aggregate.ContributorWindow != nil &&
			(expressionReferencesVariables(aggregate.ContributorWindow.Timestamp, variables) ||
				expressionReferencesVariables(aggregate.ContributorWindow.Anchor, variables)) {
			return true
		}
		if aggregate.Ordering != nil && expressionReferencesVariables(aggregate.Ordering.Timestamp, variables) {
			return true
		}
		return false
	case ir.PhysicalSliceExpression:
		slice := expression.Slice
		if slice == nil {
			return true
		}
		if valueReferencesVariables(slice.Source, variables) {
			return true
		}
		if slice.Predicate != nil && predicateReferencesVariables(*slice.Predicate, variables) {
			return true
		}
		if slice.Sort != nil && expressionReferencesVariables(*slice.Sort, variables) {
			return true
		}
		for index := range slice.Projections {
			if expressionReferencesVariables(slice.Projections[index].Expression, variables) {
				return true
			}
		}
		return false
	case ir.PhysicalObjectExpression:
		object := expression.Object
		if object == nil {
			return true
		}
		for index := range object.Fields {
			if expressionReferencesVariables(object.Fields[index].Expression, variables) {
				return true
			}
		}
		return false
	case ir.PhysicalSubplanExpression:
		if expression.Subplan == nil {
			return true
		}
		return subplanReferencesVariables(*expression.Subplan, variables)
	default:
		return true
	}
}

func predicateReferencesVariables(predicate ir.PhysicalPredicateExpression, variables map[string]bool) bool {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return true
		}
		return comparisonReferencesVariables(*predicate.Comparison, variables)
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate, ir.PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if predicateReferencesVariables(child, variables) {
				return true
			}
		}
		return false
	case ir.PhysicalExistsPredicate:
		if predicate.Exists == nil {
			return true
		}
		return subplanReferencesVariables(*predicate.Exists, variables)
	default:
		return true
	}
}

func comparisonReferencesVariables(predicate ir.PhysicalPredicate, variables map[string]bool) bool {
	if valueReferencesVariables(predicate.Left, variables) {
		return true
	}
	if predicate.Right != nil && valueReferencesVariables(*predicate.Right, variables) {
		return true
	}
	if predicate.LeftExpression != nil && expressionReferencesVariables(*predicate.LeftExpression, variables) {
		return true
	}
	return false
}

func valueReferencesVariables(value ir.PhysicalValue, variables map[string]bool) bool {
	return value.Variable != "" && variables[value.Variable]
}

func (r *planRenderer) renderSetAggregateExpression(plan ir.PhysicalPlan, projection ir.PhysicalProjection) (string, error) {
	aggregate := projection.Expression.Aggregate
	if aggregate == nil {
		return "", fmt.Errorf("%w: projection %q: AGGREGATE expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Expression.Cardinality != ir.PhysicalScalarCardinality {
		return "", fmt.Errorf("%w: projection %q: the registered aggregate surface emits SCALAR-cardinality aggregates, the form the lowering emits for one reduction per row",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: projection %q: the registered aggregate surface emits EMPTY_ON_NULL aggregates; got %q",
			ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.NullBehavior))
	}
	switch aggregate.Operation {
	case ir.PhysicalSumAggregate, ir.PhysicalCountAggregate, ir.PhysicalMinAggregate, ir.PhysicalMaxAggregate:
	default:
		return "", fmt.Errorf("%w: projection %q: aggregate operation %q has no registered PostgreSQL reducer; the correlated-sublink surface covers SUM, COUNT, MIN, MAX",
			ErrUnsupportedPostgresPlan, projection.Name, string(aggregate.Operation))
	}
	if aggregate.ContributorWindow != nil || aggregate.Ordering != nil || aggregate.RequiredValuesBindKey != "" {
		return "", fmt.Errorf("%w: projection %q: contributor windows, temporal ordering, and required-value comparisons have no registered PostgreSQL reducer",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	setVariable := aggregate.Source.Variable
	if setVariable == "" || aggregate.Source.BindKey != "" || len(aggregate.Source.Path) != 0 {
		return "", fmt.Errorf("%w: projection %q: the aggregate source must be a registered set variable",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	corr, ok := r.setBodies[setVariable]
	if !ok {
		return "", fmt.Errorf("%w: projection %q: aggregate source %q is not a registered child set",
			ErrUnsupportedPostgresPlan, projection.Name, setVariable)
	}
	fields := make(map[string]ir.PhysicalSetProjectionField, len(r.setProjections[setVariable]))
	for _, field := range r.setProjections[setVariable] {
		fields[field.Name] = field
	}

	seq := r.nextTraversalSequence()
	itemAlias := "a" + strconv.Itoa(seq)
	elementAlias := "v" + strconv.Itoa(seq)
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()

	whereParts := []string{corr.where}
	if aggregate.Predicate != nil {
		clause, clauseErr := r.aggregatePredicateFragment(plan, setVariable, fields, corr, corr.targetAlias, aggregate.Predicate)
		if clauseErr != nil {
			return "", fmt.Errorf("projection %q: aggregate predicate: %w", projection.Name, clauseErr)
		}
		whereParts = append(whereParts, clause)
	}
	items := "(SELECT DISTINCT " + corr.targetAlias + "." + logicalIDColumn + " AS " + logicalIDColumn +
		", " + corr.targetAlias + "." + resourceTypeColumn + " AS " + resourceTypeColumn +
		", " + corr.targetAlias + "." + payloadColumn + " AS " + payloadColumn +
		" " + corr.from + " WHERE " + strings.Join(whereParts, " AND ") + ")"

	if aggregate.Operation == ir.PhysicalCountAggregate && aggregate.Value == nil {
		return "(SELECT to_jsonb(COUNT(*)) FROM " + items + " AS " + itemAlias + ")", nil
	}

	valueArray, valueErr := r.aggregateSlotValues(setVariable, fields, corr, itemAlias, aggregate.Value, "value")
	if valueErr != nil {
		return "", fmt.Errorf("projection %q: aggregate value: %w", projection.Name, valueErr)
	}
	if valueArray == "" {
		return "", fmt.Errorf("%w: projection %q: this aggregate operation requires a value expression",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	elements := "FROM " + items + " AS " + itemAlias +
		" CROSS JOIN LATERAL jsonb_array_elements(" + valueArray + ") AS " + elementAlias + "(v)" +
		" WHERE " + elementAlias + ".v <> 'null'::jsonb"

	switch aggregate.Operation {
	case ir.PhysicalCountAggregate:
		return "(SELECT to_jsonb(COUNT(*)) " + elements + ")", nil
	case ir.PhysicalSumAggregate:
		messageIndex := r.textParameter("NUMERIC_AGGREGATE_NON_NUMERIC", "")
		raise := "to_jsonb(CAST('ASSERT:' || $" + strconv.Itoa(messageIndex) +
			" || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), '') AS integer))"
		numericSum := groupDoubleBandCast("SUM(" + callBandOperandClamp(
			"(("+elementAlias+".v #>> '{}')::numeric)") + ")")
		return "(SELECT CASE WHEN COUNT(*) = 0 THEN NULL::jsonb" +
			" WHEN bool_and(jsonb_typeof(" + elementAlias + ".v) = 'number')" +
			" THEN to_jsonb(" + numericSum + ") ELSE " + raise + " END " + elements + ")", nil
	case ir.PhysicalMinAggregate, ir.PhysicalMaxAggregate:
		direction := "ASC"
		if aggregate.Operation == ir.PhysicalMaxAggregate {
			direction = "DESC"
		}
		numberKey := "CASE WHEN jsonb_typeof(" + elementAlias + ".v) = 'number' THEN (" + elementAlias + ".v #>> '{}')::numeric END"
		booleanKey := "CASE WHEN jsonb_typeof(" + elementAlias + ".v) = 'boolean' THEN (" + elementAlias + ".v #>> '{}') END COLLATE \"C\""
		stringKey := "CASE WHEN jsonb_typeof(" + elementAlias + ".v) = 'string' THEN (" + elementAlias + ".v #>> '{}') END COLLATE \"C\""
		rank := "CASE jsonb_typeof(" + elementAlias + ".v) WHEN 'boolean' THEN 1 WHEN 'number' THEN 2 WHEN 'string' THEN 3 WHEN 'array' THEN 4 ELSE 5 END"
		orderBy := rank + " " + direction + " NULLS LAST, " +
			numberKey + " " + direction + " NULLS LAST, " +
			booleanKey + " " + direction + " NULLS LAST, " +
			stringKey + " " + direction + " NULLS LAST, " +
			elementAlias + ".v " + direction + " NULLS LAST"
		return "(SELECT " + elementAlias + ".v " + elements + " ORDER BY " + orderBy + " LIMIT 1)", nil
	}
	return "", fmt.Errorf("%w: projection %q: aggregate operation %q is not registered",
		ErrUnsupportedPostgresPlan, projection.Name, string(aggregate.Operation))
}

func (r *planRenderer) aggregatePredicateFragment(
	plan ir.PhysicalPlan,
	setVariable string,
	fields map[string]ir.PhysicalSetProjectionField,
	corr correlatedSetBody,
	baseAlias string,
	predicate *ir.PhysicalPredicateExpression,
) (string, error) {
	if predicate.Kind != ir.PhysicalComparisonPredicate || predicate.Comparison == nil {
		return "", fmt.Errorf("%w: only a comparison predicate is registered on a set aggregate (the AQL reading at render/aql/collections.go renderAggregateItems refuses other forms)",
			ErrUnsupportedPostgresPlan)
	}
	values, err := r.aggregateSlotValues(setVariable, fields, corr, baseAlias, predicate.Comparison.LeftExpression, "predicate")
	if err != nil {
		return "", err
	}
	fragment, err := r.quantifiedMatchFragment(plan, *predicate.Comparison, values)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

func (r *planRenderer) aggregateSlotValues(
	setVariable string,
	fields map[string]ir.PhysicalSetProjectionField,
	corr correlatedSetBody,
	baseAlias string,
	expression *ir.PhysicalExpression,
	role string,
) (string, error) {
	if expression == nil {
		return "", nil
	}
	if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
		return "", fmt.Errorf("%w: aggregate %s must be an EXTRACT selector expression, the form lower/child_sets.go emits",
			ErrUnsupportedPostgresPlan, role)
	}
	extract := expression.Extract
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: aggregate %s must be ARRAY-cardinality; got %q",
			ErrUnsupportedPostgresPlan, role, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: aggregate %s must carry EMPTY_ON_NULL null behavior; got %q",
			ErrUnsupportedPostgresPlan, role, string(expression.NullBehavior))
	}
	if extract.Prepared == nil {
		return "", fmt.Errorf("%w: aggregate %s must carry the prepared projection reference lower/child_sets.go computes",
			ErrUnsupportedPostgresPlan, role)
	}
	if extract.Prepared.SetVariable != setVariable {
		return "", fmt.Errorf("%w: aggregate %s reads a prepared slot of set %q, not the aggregate source set %q",
			ErrUnsupportedPostgresPlan, role, extract.Prepared.SetVariable, setVariable)
	}
	field, ok := fields[extract.Prepared.Field]
	if !ok {
		return "", fmt.Errorf("%w: aggregate %s reads projection slot %q, which set %q does not declare",
			ErrUnsupportedPostgresPlan, role, extract.Prepared.Field, setVariable)
	}
	if extract.Source.Variable != setVariable {
		return "", fmt.Errorf("%w: aggregate %s extracts from variable %q, not the aggregate source set %q",
			ErrUnsupportedPostgresPlan, role, extract.Source.Variable, setVariable)
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar, ir.PhysicalSelectorConditionalArray:
	default:
		return "", fmt.Errorf("%w: aggregate %s selector execution mode %q is unknown",
			ErrUnsupportedPostgresPlan, role, string(extract.ExecutionMode))
	}
	if extract.Distinct {
		return "", fmt.Errorf("%w: aggregate %s DISTINCT changes element multiplicity the quantified reducer does not model (the AQL predicate reading consumes the raw value list)",
			ErrUnsupportedPostgresPlan, role)
	}
	if len(extract.Fallbacks) != 0 {
		return "", fmt.Errorf("%w: aggregate %s selector fallbacks are a separate capability", ErrUnsupportedPostgresPlan, role)
	}
	if extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: aggregate %s unit normalization is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan, role)
	}
	if strings.TrimSpace(field.ResourceType) == "" || field.ResourceType != corr.targetType {
		return "", fmt.Errorf("%w: aggregate %s slot validates against resource type %q, but the set reaches %q",
			ErrUnsupportedPostgresPlan, role, field.ResourceType, corr.targetType)
	}
	if strings.TrimSpace(extract.ResourceType) != field.ResourceType {
		return "", fmt.Errorf("%w: aggregate %s validates its selector against %q, but the slot declares %q",
			ErrUnsupportedPostgresPlan, role, extract.ResourceType, field.ResourceType)
	}
	base := baseAlias + "." + r.storage.Resource.MustColumn("payload").SQLText()
	name := "ags" + strconv.Itoa(r.nextTraversalSequence())
	return r.pivotSelectorArray(base, field.Selector, name)
}

func validateDeadPostWindowTraversal(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal, source rowScope) error {
	switch traversal.Direction {
	case ir.PhysicalOutbound, ir.PhysicalInbound:
	case ir.PhysicalAny:
		return fmt.Errorf("%w: ANY-direction traversal has no proven PostgreSQL contract (render/aql/navigation_layout.go:394-396 refuses it too)", ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown traversal direction %q", ErrUnsupportedPostgresPlan, string(traversal.Direction))
	}
	switch traversal.Strategy {
	case "", ir.PhysicalTraversalNative, ir.PhysicalTraversalEndpointLookup:
	default:
		return fmt.Errorf("%w: unknown traversal strategy %q", ErrUnsupportedPostgresPlan, string(traversal.Strategy))
	}
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup &&
		(strings.TrimSpace(traversal.EndpointField) == "" || strings.TrimSpace(traversal.EndpointJoinField) == "") {
		return fmt.Errorf("%w: ENDPOINT_LOOKUP traversal must name its endpoint fields (the shared lowering contract always does)", ErrUnsupportedPostgresPlan)
	}
	expectedField := "to_type"
	if traversal.Direction == ir.PhysicalInbound {
		expectedField = "from_type"
	}
	if traversal.EdgeTargetTypeField != expectedField {
		return fmt.Errorf("%w: %s traversal must carry EdgeTargetTypeField %q, got %q",
			ErrUnsupportedPostgresPlan, string(traversal.Direction), expectedField, traversal.EdgeTargetTypeField)
	}
	if traversal.SourceVariable == "" || traversal.EdgeVariable == "" || traversal.TargetVariable == "" {
		return fmt.Errorf("%w: traversal must name its source, edge, and target variables (the shared lowering contract always does)", ErrUnsupportedPostgresPlan)
	}
	if traversal.EdgeCollectionBindKey == "" {
		return fmt.Errorf("%w: traversal must name the edge collection bind", ErrUnsupportedPostgresPlan)
	}
	if collection, ok := plan.BindVars[traversal.EdgeCollectionBindKey].(string); !ok || collection != "fhir_edge" {
		return fmt.Errorf("%w: traversal edge collection bind %q must be bound to \"fhir_edge\" (the only registered edge relation)",
			ErrUnsupportedPostgresPlan, traversal.EdgeCollectionBindKey)
	}
	for _, bindKey := range []string{traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
		if value, ok := plan.BindVars[bindKey].(string); !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: traversal bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, bindKey)
		}
	}
	if traversal.SourceTypeBindKey != "" {
		if bound, ok := plan.BindVars[traversal.SourceTypeBindKey].(string); !ok || bound != source.resourceType {
			return fmt.Errorf("%w: traversal source type bind does not match the current row resource type %q",
				ErrUnsupportedPostgresPlan, source.resourceType)
		}
	}
	return nil
}
