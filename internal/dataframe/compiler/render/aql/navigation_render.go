package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderTraversalSet(block physicalNavigationTraversal, rootVariable string, traversalIndex int) ([]string, error) {
	traversal := block.traversal
	setVariable := r.newInternalVariable(fmt.Sprintf("set_%d", traversalIndex))
	parentVariable := traversal.SourceVariable
	traversalIndent := "    "
	lines := []string{fmt.Sprintf("  LET %s = (", setVariable)}
	if traversal.SourceVariable != rootVariable {
		parentSet, ok := r.setVariables[traversal.SourceVariable]
		if !ok {
			return nil, fmt.Errorf("source variable %q has no previously rendered parent set", traversal.SourceVariable)
		}
		parentVariable = r.newInternalVariable(fmt.Sprintf("parent_%d", traversalIndex))
		lines = append(lines, fmt.Sprintf("    FOR %s IN %s", parentVariable, parentSet))
		traversalIndent = "      "
	}
	strategy := traversal.Strategy
	if strategy == "" {
		strategy = ir.PhysicalTraversalNative
	}
	if strategy == ir.PhysicalTraversalEndpointLookup {
		lines = append(lines,
			fmt.Sprintf("%sFOR %s IN @@%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == %s._id", traversalIndent, traversal.EdgeVariable, traversal.EndpointField, parentVariable),
			fmt.Sprintf("%s  FILTER %s.label == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeLabelBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
			fmt.Sprintf("%s  LET %s = DOCUMENT(%s.%s)", traversalIndent, traversal.TargetVariable, traversal.EdgeVariable, traversal.EndpointJoinField),
			fmt.Sprintf("%s  FILTER %s != null", traversalIndent, traversal.TargetVariable),
			fmt.Sprintf("%s  FILTER %s.resourceType == @%s", traversalIndent, traversal.TargetVariable, traversal.TargetTypeBindKey),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", traversalIndent, traversal.TargetVariable, traversal.EdgeVariable, traversal.Direction, parentVariable, traversal.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.label == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeLabelBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
			fmt.Sprintf("%s  FILTER %s.resourceType == @%s", traversalIndent, traversal.TargetVariable, traversal.TargetTypeBindKey),
		)
	}
	for scopeIndex, operation := range block.scope {
		line, err := r.renderScopeOperation(operation, traversalIndent+"  ")
		if err != nil {
			return nil, fmt.Errorf("render traversal scope operation %d: %w", scopeIndex, err)
		}
		lines = append(lines, line...)
	}
	lines = append(lines, traversalIndent+"  RETURN "+traversal.TargetVariable, "  )")
	r.setVariables[traversal.TargetVariable] = setVariable
	return lines, nil
}

// renderUnnest lowers the canonical cardinality-changing operation into
// correlated AQL loops. The physical IR deliberately does not contain AQL;
// this is the sole renderer implementation for both recipe-originated and
// generic plans.
//
// A source is evaluated once per parent row and normalized to [] on null. An
// INNER unnest emits no rows for an empty source. OUTER uses a nullable index
// sentinel so it emits exactly one parent row with a null item. Indexed loops
// are used whenever ordinality or OUTER semantics are requested, preserving
// duplicate values and a stable zero-based position.
func (r *physicalPlanRenderer) renderUnnest(unnest ir.PhysicalUnnest, indent string, ordinal, depth int) ([]string, error) {
	source, err := r.renderExpression(unnest.Expression)
	if err != nil {
		return nil, fmt.Errorf("unnest source: %w", err)
	}
	if unnest.InputVariable == "" || unnest.OutputVariable == "" {
		return nil, fmt.Errorf("unnest requires input and output variables")
	}
	baseIndent := indent
	if depth > 0 {
		baseIndent = strings.Repeat("  ", depth+1)
	}
	sourceVariable := r.newInternalVariable(fmt.Sprintf("unnest_source_%d", ordinal))
	// Selector extraction preserves one array layer per repeated path. UNNEST
	// consumes the resulting collection, so flatten exactly one layer here;
	// otherwise a source such as root.member[] becomes [member[]] and emits one
	// row per parent rather than one row per member.
	lines := []string{fmt.Sprintf("%sLET %s = (%s == null ? [] : FLATTEN(%s))", baseIndent, sourceVariable, source, source)}
	indexed := unnest.JoinMode == ir.PhysicalUnnestOuter || unnest.Ordinality != ""
	if !indexed {
		lines = append(lines, fmt.Sprintf("%sFOR %s IN %s", baseIndent, unnest.OutputVariable, sourceVariable))
		return lines, nil
	}
	indexVariable := r.newInternalVariable(fmt.Sprintf("unnest_index_%d", ordinal))
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? %s : RANGE(0, LENGTH(%s) - 1)", sourceVariable, "[]", sourceVariable)
	if unnest.JoinMode == ir.PhysicalUnnestOuter {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", sourceVariable, sourceVariable)
	}
	lines = append(lines, fmt.Sprintf("%sFOR %s IN (%s)", baseIndent, indexVariable, indices))
	item := fmt.Sprintf("%s == null ? null : %s[%s]", indexVariable, sourceVariable, indexVariable)
	lines = append(lines, fmt.Sprintf("%sLET %s = %s", baseIndent, unnest.OutputVariable, item))
	if unnest.Ordinality != "" {
		lines = append(lines, fmt.Sprintf("%sLET %s = %s", baseIndent, unnest.Ordinality, indexVariable))
	}
	return lines, nil
}

func (r *physicalPlanRenderer) renderSet(set ir.PhysicalSet, index int) ([]string, error) {
	if set.SourceSetVariable != "" {
		return r.renderSharedSubset(set)
	}
	if len(set.Subplan.Operations) == 0 {
		return nil, fmt.Errorf("set %q has no subplan operations", set.Variable)
	}
	first := set.Subplan.Operations[0]
	if first.Kind != ir.PhysicalTraversalOp || first.Traversal == nil {
		return nil, fmt.Errorf("set %q must begin with TRAVERSAL", set.Variable)
	}
	t := first.Traversal
	parentVariable := t.SourceVariable
	indent := "    "
	lines := []string{fmt.Sprintf("  LET %s = (", set.Variable)}
	if parentSet, ok := r.setVariables[t.SourceVariable]; ok {
		parentVariable = r.newInternalVariable(fmt.Sprintf("parent_set_%d", index))
		lines = append(lines, fmt.Sprintf("    FOR %s IN %s", parentVariable, parentSet))
		indent = "      "
	}
	strategy := t.Strategy
	if strategy == "" {
		strategy = ir.PhysicalTraversalNative
	}
	if strategy == ir.PhysicalTraversalEndpointLookup {
		// The endpoint equality is the first edge predicate so Arango can use
		// the route's compound endpoint index. The node is materialized only
		// after edge scope/type predicates have narrowed the candidate set.
		lines = append(lines,
			fmt.Sprintf("%sFOR %s IN @@%s", indent, t.EdgeVariable, t.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == %s._id", indent, t.EdgeVariable, t.EndpointField, parentVariable),
			fmt.Sprintf("%s  FILTER %s.label == @%s", indent, t.EdgeVariable, t.EdgeLabelBindKey),
		)
		if t.TargetTypeBindKey != "" && t.EdgeTargetTypeField != "" {
			if _, ok := r.bindVars[t.TargetTypeBindKey].([]string); ok {
				lines = append(lines, fmt.Sprintf("%s  FILTER POSITION(@%s, %s.%s)", indent, t.TargetTypeBindKey, t.EdgeVariable, t.EdgeTargetTypeField))
			} else {
				lines = append(lines, fmt.Sprintf("%s  FILTER %s.%s == @%s", indent, t.EdgeVariable, t.EdgeTargetTypeField, t.TargetTypeBindKey))
			}
		}
		lines = append(lines,
			fmt.Sprintf("%s  LET %s = DOCUMENT(%s.%s)", indent, t.TargetVariable, t.EdgeVariable, t.EndpointJoinField),
			fmt.Sprintf("%s  FILTER %s != null", indent, t.TargetVariable),
		)
		if t.TargetTypeBindKey != "" {
			if _, ok := r.bindVars[t.TargetTypeBindKey].([]string); ok {
				lines = append(lines, fmt.Sprintf("%s  FILTER POSITION(@%s, %s.resourceType)", indent, t.TargetTypeBindKey, t.TargetVariable))
			} else {
				lines = append(lines, fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent, t.TargetVariable, t.TargetTypeBindKey))
			}
		}
	} else {
		lines = append(lines, fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", indent, t.TargetVariable, t.EdgeVariable, t.Direction, parentVariable, t.EdgeCollectionBindKey), fmt.Sprintf("%s  FILTER %s.label == @%s", indent, t.EdgeVariable, t.EdgeLabelBindKey))
		if t.EdgeTargetTypeField != "" {
			lines = append(lines, r.renderTraversalTypeFilters(t, indent)...)
		}
	}
	for opIndex, operation := range set.Subplan.Operations[1:] {
		if operation.Kind == ir.PhysicalUnnestOp {
			if operation.Unnest == nil {
				return nil, fmt.Errorf("set operation %d: unnest payload is missing", opIndex+1)
			}
			rendered, err := r.renderUnnest(*operation.Unnest, indent+"  ", opIndex+1, 0)
			if err != nil {
				return nil, fmt.Errorf("set operation %d: %w", opIndex+1, err)
			}
			lines = append(lines, rendered...)
			continue
		}
		rendered, err := r.renderScopeOperation(operation, indent+"  ")
		if err != nil {
			return nil, fmt.Errorf("set operation %d: %w", opIndex+1, err)
		}
		lines = append(lines, rendered...)
	}
	value, err := r.renderExpression(set.Subplan.Return)
	if err != nil {
		return nil, err
	}
	if set.Projection != nil {
		value, err = r.renderPhysicalSetProjection(t.TargetVariable, *set.Projection)
	} else {
		value, err = renderPhysicalSetOutput(value, set.Output)
	}
	if err != nil {
		return nil, err
	}
	if set.SortByKey {
		lines = append(lines, indent+"  SORT "+t.TargetVariable+"._key")
	}
	lines = append(lines, indent+"  RETURN "+value, "  )")
	if set.Unique {
		lines[0] = fmt.Sprintf("  LET %s = UNIQUE((", set.Variable)
		lines[len(lines)-1] = "  ))"
	}
	r.setVariables[set.Variable] = set.Variable
	if set.Reduction != nil {
		reduced, err := r.renderPhysicalSetReduction(*set.Reduction)
		if err != nil {
			return nil, err
		}
		lines = append(lines, reduced...)
	}
	if set.Prepared != nil {
		prepared, err := r.renderPreparedSet(*set.Prepared)
		if err != nil {
			return nil, err
		}
		lines = append(lines, prepared...)
		r.setVariables[set.Prepared.Variable] = set.Prepared.Variable
	}
	return lines, nil
}

func renderPhysicalSetOutput(value string, output *ir.PhysicalSetOutput) (string, error) {
	if output == nil {
		return value, nil
	}
	fields := make([]string, 0, len(output.Fields))
	for _, field := range output.Fields {
		name := string(field)
		switch field {
		case ir.PhysicalSetGraphIDField, ir.PhysicalSetKeyField, ir.PhysicalSetIDField, ir.PhysicalSetResourceTypeField, ir.PhysicalSetPayloadField:
			fields = append(fields, fmt.Sprintf("%s: %s.%s", name, value, name))
		default:
			return "", fmt.Errorf("unsupported compact set output field %q", field)
		}
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderPhysicalSetProjection(item string, projection ir.PhysicalSetProjection) (string, error) {
	if len(projection.Fields) == 0 {
		return "", fmt.Errorf("set projection requires at least one field")
	}
	fields := []string{
		"_id: " + item + "._id",
		"_key: " + item + "._key",
		"id: " + item + ".id",
		"resourceType: " + item + ".resourceType",
	}
	for _, field := range projection.Fields {
		values, err := r.renderSelectorByMode(item+".payload", field.Selector, field.ExecutionMode, field.Demand)
		if err != nil {
			return "", fmt.Errorf("projected field %q: %w", field.Name, err)
		}
		fields = append(fields, field.Name+": "+values)
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderPhysicalSetReduction(reduction ir.PhysicalSetReduction) ([]string, error) {
	if r.setVariables[reduction.SourceSetVariable] == "" {
		return nil, fmt.Errorf("reduction source set %q has not been rendered", reduction.SourceSetVariable)
	}
	fields := make([]string, 0, len(reduction.Fields))
	for _, field := range reduction.Fields {
		source := fmt.Sprintf("%s[*].%s", reduction.SourceSetVariable, field.SourceField)
		var value string
		switch field.Mode {
		case ir.PhysicalSetReductionFirst:
			value = "FIRST(FLATTEN(" + source + "))"
		case ir.PhysicalSetReductionAll:
			value = "FLATTEN(" + source + ")"
		case ir.PhysicalSetReductionDistinct:
			value = "SORTED_UNIQUE(FLATTEN(" + source + "))"
		default:
			return nil, fmt.Errorf("unsupported set reduction mode %q", field.Mode)
		}
		fields = append(fields, field.Name+": "+value)
	}
	return []string{fmt.Sprintf("  LET %s = { %s }", reduction.Variable, strings.Join(fields, ", "))}, nil
}

func (r *physicalPlanRenderer) renderPreparedSet(prepared ir.PhysicalPreparedSet) ([]string, error) {
	if r.setVariables[prepared.SourceSetVariable] == "" {
		return nil, fmt.Errorf("prepared source set %q has not been rendered", prepared.SourceSetVariable)
	}
	item := r.newInternalVariable("prepared_item")
	lines := []string{fmt.Sprintf("  LET %s = (", prepared.Variable), fmt.Sprintf("    FOR %s IN %s", item, prepared.SourceSetVariable)}
	// Rich consumers may combine a prepared selector with a direct payload
	// fallback (slice identity, an unprepared pivot value, or a nested object
	// field). Preserve the node-facing fields those consumers already read while
	// adding prepared projections; the optimizer can remove this retention only
	// after a separate compact-set contract proves it safe.
	fields := []string{
		fmt.Sprintf("_key: %s._key", item),
		fmt.Sprintf("id: %s.id", item),
		fmt.Sprintf("resourceType: %s.resourceType", item),
		fmt.Sprintf("payload: %s.payload", item),
		fmt.Sprintf("__loom_prepared_node: %s", item),
	}
	for _, field := range prepared.Fields {
		values, err := r.renderSelectorArrayFromSource(item+".payload", field.Selector, false, false)
		if err != nil {
			return nil, fmt.Errorf("prepared field %q: %w", field.Name, err)
		}
		fields = append(fields, field.Name+": "+values)
	}
	lines = append(lines, "      RETURN { "+strings.Join(fields, ", ")+" }", "    )")
	return lines, nil
}

func (r *physicalPlanRenderer) renderTraversalTypeFilters(t *ir.PhysicalTraversal, indent string) []string {
	if t.TargetTypeBindKey == "" || t.EdgeTargetTypeField == "" {
		return nil
	}
	if _, ok := r.bindVars[t.TargetTypeBindKey].([]string); ok {
		return []string{
			fmt.Sprintf("%s  FILTER POSITION(@%s, %s.%s)", indent, t.TargetTypeBindKey, t.EdgeVariable, t.EdgeTargetTypeField),
			fmt.Sprintf("%s  FILTER POSITION(@%s, %s.resourceType)", indent, t.TargetTypeBindKey, t.TargetVariable),
		}
	}
	return []string{fmt.Sprintf("%s  FILTER %s.%s == @%s", indent, t.EdgeVariable, t.EdgeTargetTypeField, t.TargetTypeBindKey), fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent, t.TargetVariable, t.TargetTypeBindKey)}
}

func (r *physicalPlanRenderer) renderSharedSubset(set ir.PhysicalSet) ([]string, error) {
	if r.setVariables[set.SourceSetVariable] == "" {
		return nil, fmt.Errorf("shared subset source %q has not been rendered", set.SourceSetVariable)
	}
	lines := []string{fmt.Sprintf("  LET %s = (", set.Variable), fmt.Sprintf("    FOR %s IN %s", set.ItemVariable, set.SourceSetVariable)}
	for index, operation := range set.Subplan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if operation.Unnest == nil {
				return nil, fmt.Errorf("shared subset operation %d: unnest payload is missing", index)
			}
			rendered, err := r.renderUnnest(*operation.Unnest, "      ", index, 0)
			if err != nil {
				return nil, fmt.Errorf("shared subset operation %d: %w", index, err)
			}
			lines = append(lines, rendered...)
			continue
		}
		rendered, err := r.renderScopeOperation(operation, "      ")
		if err != nil {
			return nil, fmt.Errorf("shared subset operation %d: %w", index, err)
		}
		lines = append(lines, rendered...)
	}
	value, err := r.renderExpression(set.Subplan.Return)
	if err != nil {
		return nil, err
	}
	if set.Projection != nil {
		value, err = r.renderPhysicalSetProjection(set.ItemVariable, *set.Projection)
	} else {
		value, err = renderPhysicalSetOutput(value, set.Output)
	}
	if err != nil {
		return nil, err
	}
	if set.SortByKey {
		lines = append(lines, "      SORT "+set.ItemVariable+"._key")
	}
	lines = append(lines, "      RETURN "+value, "    )")
	if set.Unique {
		lines[0] = fmt.Sprintf("  LET %s = UNIQUE((", set.Variable)
		lines[len(lines)-1] = "    ))"
	}
	r.setVariables[set.Variable] = set.Variable
	if set.Reduction != nil {
		reduced, err := r.renderPhysicalSetReduction(*set.Reduction)
		if err != nil {
			return nil, err
		}
		lines = append(lines, reduced...)
	}
	if set.Prepared != nil {
		prepared, err := r.renderPreparedSet(*set.Prepared)
		if err != nil {
			return nil, err
		}
		lines = append(lines, prepared...)
		r.setVariables[set.Prepared.Variable] = set.Prepared.Variable
	}
	return lines, nil
}

func physicalPlanVariableNames(plan ir.PhysicalPlan) map[string]struct{} {
	variables := map[string]struct{}{}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			variables[operation.RootScan.Variable] = struct{}{}
		case ir.PhysicalTraversalOp:
			variables[operation.Traversal.SourceVariable] = struct{}{}
			variables[operation.Traversal.TargetVariable] = struct{}{}
			if operation.Traversal.EdgeVariable != "" {
				variables[operation.Traversal.EdgeVariable] = struct{}{}
			}
		case ir.PhysicalDerivedLetOp:
			variables[operation.DerivedLet.Variable] = struct{}{}
		case ir.PhysicalExpressionLetOp:
			variables[operation.ExpressionLet.Variable] = struct{}{}
		case ir.PhysicalSetOp:
			variables[operation.Set.Variable] = struct{}{}
			if operation.Set.Reduction != nil {
				variables[operation.Set.Reduction.Variable] = struct{}{}
			}
			if operation.Set.Prepared != nil {
				variables[operation.Set.Prepared.Variable] = struct{}{}
			}
		case ir.PhysicalUnnestOp:
			variables[operation.Unnest.InputVariable] = struct{}{}
			variables[operation.Unnest.OutputVariable] = struct{}{}
			if operation.Unnest.Ordinality != "" {
				variables[operation.Unnest.Ordinality] = struct{}{}
			}
		case ir.PhysicalPathSeedOp:
			variables[operation.PathSeed.Variable] = struct{}{}
		case ir.PhysicalPathExtendOp:
			variables[operation.PathExtend.Variable] = struct{}{}
			variables[operation.PathExtend.Traversal.TargetVariable] = struct{}{}
			variables[operation.PathExtend.Traversal.EdgeVariable] = struct{}{}
		}
	}
	return variables
}

func (r *physicalPlanRenderer) newInternalVariable(suffix string) string {
	base := "__loom_physical_" + suffix
	variable := base
	for counter := 1; ; counter++ {
		if _, exists := r.reservedVars[variable]; !exists {
			r.reservedVars[variable] = struct{}{}
			return variable
		}
		variable = fmt.Sprintf("%s_%d", base, counter)
	}
}
