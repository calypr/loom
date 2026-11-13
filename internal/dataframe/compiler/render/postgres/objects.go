package postgres

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const maxCompositeDepth = 4

func sliceConsumesSetVariable(plan ir.PhysicalPlan, setVariable string) bool {
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for index := range operation.Return.Projections {
			if operation.Return.Projections[index].Expression != nil &&
				expressionConsumesSetVariable(*operation.Return.Projections[index].Expression, setVariable) {
				return true
			}
		}
	}
	return false
}

func expressionConsumesSetVariable(expression ir.PhysicalExpression, setVariable string) bool {
	if slice := expression.Slice; slice != nil {
		if slice.Source.Variable == setVariable && slice.Source.BindKey == "" {
			return true
		}
		for index := range slice.Projections {
			if expressionConsumesSetVariable(slice.Projections[index].Expression, setVariable) {
				return true
			}
		}
	}
	if object := expression.Object; object != nil {
		for index := range object.Fields {
			if expressionConsumesSetVariable(object.Fields[index].Expression, setVariable) {
				return true
			}
		}
	}
	return false
}

const sliceItemVariable = "__loom_slice_item"

func rebindSliceOperand(expression ir.PhysicalExpression, sourceVariable string) ir.PhysicalExpression {
	rewritten := expression
	if extract := rewritten.Extract; extract != nil && extract.Source.Variable == sourceVariable {
		extractCopy := *extract
		extractCopy.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: extractCopy.Source.Path}
		rewritten.Extract = &extractCopy
	}
	if value := rewritten.Value; value != nil && value.Variable == sourceVariable {
		valueCopy := *value
		valueCopy.Variable = sliceItemVariable
		rewritten.Value = &valueCopy
	}
	if object := rewritten.Object; object != nil {
		fields := make([]ir.PhysicalExpressionProjection, len(object.Fields))
		for index, field := range object.Fields {
			fields[index] = field
			fields[index].Expression = rebindSliceOperand(field.Expression, sourceVariable)
		}
		objectCopy := *object
		objectCopy.Fields = fields
		rewritten.Object = &objectCopy
	}
	return rewritten
}

func (r *planRenderer) resolveSlicePreparedExtract(setVariable string, original, rewritten ir.PhysicalExpression, context string) error {
	extract := rewritten.Extract
	if extract == nil {
		return nil
	}
	if extract.Prepared != nil {
		reference := *extract.Prepared
		originalSourceVariable := ""
		if original.Extract != nil {
			originalSourceVariable = original.Extract.Source.Variable
		}
		verbatim := fmt.Errorf("%w: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
		if setVariable == "" || reference.SetVariable != setVariable || originalSourceVariable != setVariable {
			return verbatim
		}
		var slot *ir.PhysicalSetProjectionField
		for index := range r.setProjections[setVariable] {
			if r.setProjections[setVariable][index].Name == reference.Field {
				slot = &r.setProjections[setVariable][index]
				break
			}
		}
		if slot == nil {
			return verbatim
		}
		switch slot.Demand {
		case ir.PhysicalSelectorAllValues, ir.PhysicalSelectorFirstValue:
		default:
			return fmt.Errorf("%w: slice %s: prepared slot %q carries unsupported value demand %q",
				ErrUnsupportedPostgresPlan, context, reference.Field, string(slot.Demand))
		}
		switch slot.ExecutionMode {
		case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
		case ir.PhysicalSelectorConditionalArray:
			return fmt.Errorf("%w: slice %s: prepared slot %q is CONDITIONAL_ARRAY, whose repeated-step rendering is not registered for PostgreSQL",
				ErrUnsupportedPostgresPlan, context, reference.Field)
		default:
			return fmt.Errorf("%w: slice %s: prepared slot %q has unknown selector execution mode %q",
				ErrUnsupportedPostgresPlan, context, reference.Field, string(slot.ExecutionMode))
		}
		if slot.ResourceType != extract.ResourceType {
			return fmt.Errorf("%w: slice %s: prepared slot %q projects resource type %q but the operand validates against %q",
				ErrUnsupportedPostgresPlan, context, reference.Field, slot.ResourceType, extract.ResourceType)
		}
		if !reflect.DeepEqual(extract.Selector, slot.Selector) {
			return fmt.Errorf("%w: slice %s: prepared slot %q names a different selector than the operand carries; the annotation must re-read the registered slot exactly",
				ErrUnsupportedPostgresPlan, context, reference.Field)
		}
		if slot.Selector.Filter != nil || len(slot.Selector.Steps) == 0 {
			return fmt.Errorf("%w: slice %s: prepared slot %q is not a direct property path; prepared resolution is registered for direct-path slots only",
				ErrUnsupportedPostgresPlan, context, reference.Field)
		}
		for index, step := range slot.Selector.Steps {
			if step.Field == "" || step.Iterate || step.Index != nil {
				return fmt.Errorf("%w: slice %s: prepared slot %q step %d is not a direct property step; prepared resolution is registered for direct-path slots only",
					ErrUnsupportedPostgresPlan, context, reference.Field, index)
			}
		}
		extract.Prepared = nil
	}
	return nil
}

func (r *planRenderer) objectFieldJSON(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return r.compositeValueJSON(rows, expression)
	case ir.PhysicalExtractExpression:
		return r.compositeExtractJSON(rows, expression)
	case ir.PhysicalObjectExpression:
		return r.renderObjectValue(plan, rows, expression, depth+1)
	case ir.PhysicalSliceExpression:
		return r.renderSliceValue(plan, rows, expression, depth+1)
	default:
		return "", fmt.Errorf("%w: composite operand kind %q has no registered PostgreSQL rendering (literal, aggregate, pivot, owner-records, object-lookup, keyed-map, object-keys, key-set, subplan, call, and related-field forms are separate capabilities)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

func (r *planRenderer) compositeValueJSON(rows map[string]rowScope, expression ir.PhysicalExpression) (string, error) {
	value := expression.Value
	if value == nil {
		return "", fmt.Errorf("%w: composite VALUE operand has no value payload", ErrUnsupportedPostgresPlan)
	}
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: composite VALUE operand %v must read a row value, not a bind value", ErrUnsupportedPostgresPlan, value.Path)
	}
	scope, ok := rows[value.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: composite VALUE operand reads variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 0 {
		return "", fmt.Errorf("%w: composite VALUE operand must name a document path or a typed column; the whole-document reference is spelled [\"payload\"] (lower/recipe_expression.go:128-146)",
			ErrUnsupportedPostgresPlan)
	}
	column := func(name string) string {
		return "to_jsonb(" + scope.alias + "." + r.storage.Resource.MustColumn(name).SQLText() + ")"
	}
	if len(value.Path) == 1 {
		segment := value.Path[0]
		switch {
		case segment == "payload":
			if expression.Cardinality != ir.PhysicalObjectCardinality {
				return "", fmt.Errorf(`%w: composite VALUE operand ["payload"] must carry OBJECT cardinality (lower/recipe_expression.go:133-137)`, ErrUnsupportedPostgresPlan)
			}
			return r.documentColumn(scope.alias), nil
		case segment == "resourceType":
			return column("resource_type"), nil
		case segment == "_key" || segment == "id":
			return column(canonicalKeyColumn(segment)), nil
		case resourceFilterColumns[segment]:
			return column(segment), nil
		}
	}
	return r.documentPathExpression(scope.alias, value.Path)
}

func (r *planRenderer) compositeExtractJSON(rows map[string]rowScope, expression ir.PhysicalExpression) (string, error) {
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand has no payload", ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality && expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: composite EXTRACT operand is registered for SCALAR and ARRAY cardinality only", ErrUnsupportedPostgresPlan)
	}
	switch expression.NullBehavior {
	case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
	default:
		return "", fmt.Errorf("%w: composite EXTRACT operand: null behavior %q has no registered PostgreSQL extraction form",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return "", fmt.Errorf("%w: composite EXTRACT operand: CONDITIONAL_ARRAY selector mode requires repeated-step rendering, which is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return "", fmt.Errorf("%w: composite EXTRACT operand: unknown selector execution mode %q", ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Prepared != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if extract.Distinct {
		return "", fmt.Errorf("%w: composite EXTRACT operand: DISTINCT projection is meaningful only for array-valued selectors registered with their own reduction",
			ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return "", fmt.Errorf("%w: composite EXTRACT operand: ordered fallback selectors are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: unit-normalized extraction is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Selector.Steps) == 0 {
		return "", fmt.Errorf("%w: composite EXTRACT operand: the selector is empty", ErrUnsupportedPostgresPlan)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		switch {
		case step.Field == "":
			return "", fmt.Errorf("%w: composite EXTRACT operand: selector step %d has no property", ErrUnsupportedPostgresPlan, index)
		case step.Iterate:
			return "", fmt.Errorf("%w: composite EXTRACT operand: repeated ([]) selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, index)
		case step.Index != nil:
			return "", fmt.Errorf("%w: composite EXTRACT operand: indexed selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: composite EXTRACT operand: selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
		path = append(path, step.Field)
	}
	if extract.Source.BindKey != "" {
		return "", fmt.Errorf("%w: composite EXTRACT operand: the selector source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 0 && (len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload") {
		return "", fmt.Errorf(`%w: composite EXTRACT operand: the selector source must be the row document, spelled ["payload"] or as the bare row (empty path, the slice lowerer's leftSource at lower/root_projection.go:402); got %v`,
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	scope, ok := rows[extract.Source.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: composite EXTRACT operand reads variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
		return "", fmt.Errorf("%w: composite EXTRACT operand validates selector against resource type %q, but the row is scanned as %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	return r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
}

func (r *planRenderer) renderObjectValue(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	object := expression.Object
	if object == nil {
		return "", fmt.Errorf("%w: OBJECT expression is missing payload", ErrUnsupportedPostgresPlan)
	}
	if r.membership != nil {
		return "", fmt.Errorf("%w: OBJECT projection is registered on the plain spine only; membership-root routes keep the population.go contract and refuse composite projections", ErrUnsupportedPostgresPlan)
	}
	if depth > maxCompositeDepth {
		return "", fmt.Errorf("%w: OBJECT nesting exceeds the registered maximum depth %d (defensive closed-surface bound; production lowering derives at most depth 1 — SLICE projections are EXTRACT-only and the document envelope's fields are VALUE-only — and AQL's renderExpression recursion is unbounded, cited per ruling)",
			ErrUnsupportedPostgresPlan, maxCompositeDepth)
	}
	switch expression.Cardinality {
	case ir.PhysicalObjectCardinality:
	default:
		return "", fmt.Errorf("%w: OBJECT expression must carry OBJECT cardinality, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	switch expression.NullBehavior {
	case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
	default:
		return "", fmt.Errorf("%w: OBJECT expression null behavior %q is not a registered form", ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	if len(object.Fields) == 0 {
		return "", fmt.Errorf("%w: OBJECT expression requires at least one field (ir/physical_validation_shapes.go:39-54)", ErrUnsupportedPostgresPlan)
	}
	seen := make(map[string]bool, len(object.Fields))
	parts := make([]string, 0, len(object.Fields))
	anyOmit := false
	for index, field := range object.Fields {
		if strings.TrimSpace(field.Name) == "" || seen[field.Name] {
			return "", fmt.Errorf("%w: OBJECT field %d name %q is empty or duplicated (ir/physical_validation_shapes.go:45-47)",
				ErrUnsupportedPostgresPlan, index, field.Name)
		}
		seen[field.Name] = true
		value, err := r.objectFieldJSON(plan, rows, field.Expression, depth)
		if err != nil {
			return "", fmt.Errorf("object field %q: %w", field.Name, err)
		}
		nameIndex := r.textParameter(field.Name, "text:"+field.Name)
		pair := "jsonb_build_object($" + strconv.Itoa(nameIndex) + "::text, " + value + ")"
		if field.Expression.NullBehavior == ir.PhysicalOmitNulls {
			anyOmit = true
			pair = "CASE WHEN (" + value + ") IS NULL OR (" + value + ") = 'null'::jsonb THEN '{}'::jsonb ELSE " + pair + " END"
		}
		parts = append(parts, pair)
	}
	if !anyOmit {
		return strings.Join(parts, " || "), nil
	}
	return "(" + strings.Join(parts, " || ") + ")", nil
}

func (r *planRenderer) renderSliceValue(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	slice := expression.Slice
	if slice == nil {
		return "", fmt.Errorf("%w: SLICE expression is missing payload", ErrUnsupportedPostgresPlan)
	}
	if r.membership != nil {
		return "", fmt.Errorf("%w: SLICE projection is registered on the plain spine only; membership-root routes keep the population.go contract and refuse composite projections", ErrUnsupportedPostgresPlan)
	}
	if depth > maxCompositeDepth {
		return "", fmt.Errorf("%w: SLICE nesting exceeds the registered maximum depth %d (defensive closed-surface bound; production SLICE projections are EXTRACT-only, lower/root_projection.go:459-463, and AQL recursion is unbounded, cited per ruling)",
			ErrUnsupportedPostgresPlan, maxCompositeDepth)
	}
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: SLICE expression must carry ARRAY cardinality, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: SLICE expression null behavior %q is not the registered EMPTY_ON_NULL array reading (lower/root_projection.go:465)",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	if len(slice.Projections) == 0 {
		return "", fmt.Errorf("%w: SLICE requires at least one projection (ir/physical_validation_shapes.go:39-54)", ErrUnsupportedPostgresPlan)
	}

	source := slice.Source
	if source.BindKey != "" {
		return "", fmt.Errorf("%w: SLICE source must be a row variable, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(source.Path) != 0 {
		return "", fmt.Errorf("%w: SLICE source must be the row itself, the exact form lower/root_projection.go:402-408 passes (no document path); got %v",
			ErrUnsupportedPostgresPlan, source.Path)
	}
	if body, ok := r.setBodies[source.Variable]; ok {
		return r.renderSliceOverChildSet(plan, slice, body, source.Variable, depth)
	}
	scope, ok := rows[source.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: SLICE source variable %q does not bind a readable stored row", ErrUnsupportedPostgresPlan, source.Variable)
	}
	if source.Variable != r.rootVariable {
		return "", fmt.Errorf("%w: SLICE source variable %q is not a registered item source: the registered sources are the ROOT_SCAN row (singleton) and a child set whose reduction contract was validated by sets.go; traversal targets, derived values, prepared variables, and melt outputs have no registered slice contract",
			ErrUnsupportedPostgresPlan, source.Variable)
	}
	return r.assembleSliceSublink(plan, slice, map[string]rowScope{sliceItemVariable: scope}, scope, "", "", depth)
}

func (r *planRenderer) renderSliceOverChildSet(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, body correlatedSetBody, setVariable string, depth int) (string, error) {
	r.sliceSeq++
	itemAlias := "slx" + strconv.Itoa(r.sliceSeq)
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()
	items := "(SELECT DISTINCT " + body.targetAlias + "." + logicalIDColumn + " AS " + logicalIDColumn +
		", " + body.targetAlias + "." + resourceTypeColumn + " AS " + resourceTypeColumn +
		", " + body.targetAlias + "." + payloadColumn + " AS " + payloadColumn +
		" " + body.from + " WHERE " + body.where + ")"
	itemScope := rowScope{alias: itemAlias, resourceType: body.targetType}
	return r.assembleSliceSublink(plan, slice,
		map[string]rowScope{sliceItemVariable: itemScope}, itemScope,
		" FROM "+items+" AS "+itemAlias, setVariable, depth)
}

func (r *planRenderer) assembleSliceSublink(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope, itemScope rowScope, fromClause string, setVariable string, depth int) (string, error) {
	itemObject, err := r.sliceItemObject(plan, slice, itemRows, setVariable, depth)
	if err != nil {
		return "", err
	}
	sortExpr, err := r.renderSliceSort(slice, itemRows, itemScope, setVariable)
	if err != nil {
		return "", err
	}
	innerSelect := "SELECT " + itemObject + " AS \"item\", " +
		sortExpr + " AS \"item_sort\", " +
		itemScope.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText() + " AS \"item_key\"" + fromClause
	if slice.Predicate != nil {
		predicate, err := r.renderSlicePredicate(plan, slice, itemRows, setVariable)
		if err != nil {
			return "", err
		}
		innerSelect += " WHERE " + predicate
	}
	limitValue, ok := plan.BindVars[slice.LimitBindKey].(int)
	if !ok || limitValue <= 0 {
		return "", fmt.Errorf("%w: slice limit bind %q must be a positive int (ir/physical_validation_shapes.go:28-31)",
			ErrUnsupportedPostgresPlan, slice.LimitBindKey)
	}
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limitValue)}, "")
	innerSelect += " ORDER BY " + sortExpr + " ASC NULLS FIRST, item_key ASC LIMIT $" + strconv.Itoa(limitIndex)

	r.sliceSeq++
	sliceAlias := "sl" + strconv.Itoa(r.sliceSeq)
	return "(SELECT COALESCE(jsonb_agg(" + sliceAlias + ".\"item\" ORDER BY " + sliceAlias + ".\"item_sort\" ASC NULLS FIRST, " +
		sliceAlias + ".\"item_key\"), '[]'::jsonb) FROM (" + innerSelect + ") " + sliceAlias + ")", nil
}

func (r *planRenderer) sliceItemObject(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope, setVariable string, depth int) (string, error) {
	seen := make(map[string]bool, len(slice.Projections))
	args := make([]string, 0, 2*len(slice.Projections))
	for index, projection := range slice.Projections {
		if strings.TrimSpace(projection.Name) == "" || seen[projection.Name] {
			return "", fmt.Errorf("%w: slice projection %d name %q is empty or duplicated (ir/physical_validation_shapes.go:45-47)",
				ErrUnsupportedPostgresPlan, index, projection.Name)
		}
		seen[projection.Name] = true
		operand := rebindSliceOperand(projection.Expression, slice.Source.Variable)
		if err := r.resolveSlicePreparedExtract(setVariable, projection.Expression, operand,
			"projection "+strconv.Itoa(index)+" ("+projection.Name+")"); err != nil {
			return "", fmt.Errorf("slice projection %d (%s): %w", index, projection.Name, err)
		}
		value, err := r.objectFieldJSON(plan, itemRows, operand, depth)
		if err != nil {
			return "", fmt.Errorf("slice projection %d (%s): %w", index, projection.Name, err)
		}
		nameIndex := r.textParameter(projection.Name, "text:"+projection.Name)
		args = append(args, "$"+strconv.Itoa(nameIndex)+"::text", value)
	}
	return "jsonb_build_object(" + strings.Join(args, ", ") + ")", nil
}

func (r *planRenderer) renderSlicePredicate(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope, setVariable string) (string, error) {
	predicate := *slice.Predicate
	if predicate.Kind != ir.PhysicalComparisonPredicate || predicate.Comparison == nil {
		return "", fmt.Errorf("%w: slice predicate must be a comparison (the AQL contract, render/aql/collections.go:93-95)", ErrUnsupportedPostgresPlan)
	}
	comparison := *predicate.Comparison
	if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
		return "", fmt.Errorf("%w: slice predicate must carry the selector-extraction left side both slice producers emit (lower/typed_predicate.go:30-44, lower/root_projection.go:417-430); a direct column comparison is not a slice lowering output",
			ErrUnsupportedPostgresPlan)
	}
	if comparison.Left.Variable != "" || comparison.Left.BindKey != "" {
		return "", fmt.Errorf("%w: slice predicate must not also carry a direct left value", ErrUnsupportedPostgresPlan)
	}
	left := *comparison.LeftExpression
	extract := *left.Extract
	if extract.Source.Variable != slice.Source.Variable {
		return "", fmt.Errorf("%w: slice predicate extraction must read the slice source row (rebind target), got variable %q",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	extract.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: []string{"payload"}}
	left.Extract = &extract
	if err := r.resolveSlicePreparedExtract(setVariable, *comparison.LeftExpression, left, "predicate"); err != nil {
		return "", fmt.Errorf("slice predicate: %w", err)
	}
	comparison.LeftExpression = &left
	fragment, err := r.renderPredicateFragmentScoped(plan, ir.PhysicalPredicateExpression{
		Kind:       ir.PhysicalComparisonPredicate,
		Comparison: &comparison,
	}, itemRows, true)
	if err != nil {
		return "", fmt.Errorf("slice predicate: %w", err)
	}
	return fragment.sql, nil
}

func (r *planRenderer) renderSliceSort(slice *ir.PhysicalSlice, itemRows map[string]rowScope, scope rowScope, setVariable string) (string, error) {
	if slice.Sort == nil {
		return "", fmt.Errorf("%w: slice requires sort expression (ir/physical_validation_shapes.go:19-24)", ErrUnsupportedPostgresPlan)
	}
	sortExpression := *slice.Sort
	if sortExpression.Kind == ir.PhysicalValueExpression && sortExpression.Value != nil {
		value := *sortExpression.Value
		if value.Variable != slice.Source.Variable || value.BindKey != "" || len(value.Path) != 1 ||
			(value.Path[0] != "_key" && value.Path[0] != "id") {
			return "", fmt.Errorf("%w: slice VALUE sort must be the lowering identity key over the slice source variable (lower/root_projection.go:406: VALUE %v); got variable %q path %v",
				ErrUnsupportedPostgresPlan, []string{"_key"}, value.Variable, value.Path)
		}
		return scope.alias + "." + r.storage.Resource.MustColumn(canonicalKeyColumn(value.Path[0])).SQLText(), nil
	}
	if sortExpression.Kind == ir.PhysicalExtractExpression {
		if sortExpression.Extract == nil {
			return "", fmt.Errorf("%w: slice EXTRACT sort has no extraction payload", ErrUnsupportedPostgresPlan)
		}
		if sortExpression.Cardinality != ir.PhysicalScalarCardinality {
			return "", fmt.Errorf("%w: slice EXTRACT sort must be scalar-cardinality; array sort values are a separate capability", ErrUnsupportedPostgresPlan)
		}
		extract := *sortExpression.Extract
		if extract.Source.Variable != slice.Source.Variable {
			return "", fmt.Errorf("%w: slice sort extraction must read the slice source row, got variable %q",
				ErrUnsupportedPostgresPlan, extract.Source.Variable)
		}
		extract.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: []string{"payload"}}
		sortCopy := sortExpression
		sortCopy.Extract = &extract
		if err := r.resolveSlicePreparedExtract(setVariable, sortExpression, sortCopy, "sort"); err != nil {
			return "", fmt.Errorf("slice sort: %w", err)
		}
		return r.compositeExtractJSON(itemRows, sortCopy)
	}
	return "", fmt.Errorf("%w: slice sort kind %q is outside the registered forms (VALUE identity key or scalar EXTRACT)",
		ErrUnsupportedPostgresPlan, string(sortExpression.Kind))
}
