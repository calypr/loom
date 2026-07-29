package lower

import (
	"fmt"
	"sort"
	"strings"
)

func buildOptionalChildPhysicalSet(physical *PhysicalPlan, setIndex int, parent SemanticNode, parentVariable string, child SemanticNode, projectionPrefix string, policy PhysicalOptimizationPolicy) (PhysicalSet, []PhysicalProjection, error) {
	prefix := fmt.Sprintf("child_set_%d", setIndex)
	targetVariable := fmt.Sprintf("%s_node", prefix)
	edgeVariable := fmt.Sprintf("%s_edge", prefix)
	traversal, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType: parent.ResourceType, EdgeLabel: child.EdgeLabel, ToType: child.ResourceType,
		SourceVariable: parentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
		BindPrefix: prefix, Policy: policy,
	})
	if err != nil {
		return PhysicalSet{}, nil, err
	}
	for key, value := range traversal.BindVars {
		physical.BindVars[key] = value
	}
	source := PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}
	subplan := PhysicalSubplan{Captures: []string{parentVariable}, Operations: []PhysicalOperation{{
		Kind: PhysicalTraversalOp, Source: source,
		Traversal: &traversal.Traversal,
	}}}
	subplan.Operations = appendProjectScope(subplan.Operations, []string{edgeVariable, targetVariable}, child.EdgeLabel, child)
	subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{edgeVariable, targetVariable}, child.EdgeLabel, child)
	subplan.Operations = appendAuthScope(subplan.Operations, []PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}, prefix+"_scope_allowed", child)
	for index, filter := range child.Filters {
		if err := ValidateTypedFilterForResource(child.ResourceType, filter); err != nil {
			return PhysicalSet{}, nil, fmt.Errorf("child filter %q: %w", filter.FieldRef, err)
		}
		selector, err := ParseSelector(filter.Selector)
		if err != nil {
			return PhysicalSet{}, nil, fmt.Errorf("child filter %q selector: %w", filter.FieldRef, err)
		}
		predicate := PhysicalPredicate{Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind, LeftExpression: &PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Extract: &PhysicalExtract{Source: PhysicalValue{Variable: targetVariable, Path: []string{"payload"}}, ResourceType: child.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(child.ResourceType, selector)}}}
		if filter.Operator != FilterExists && filter.Operator != FilterMissing {
			key := fmt.Sprintf("%s_filter_%d_value", prefix, index+1)
			if filter.Operator == FilterIn {
				values := make([]any, 0, len(filter.Values))
				for _, value := range filter.Values {
					literal, err := filterLiteral(value)
					if err != nil {
						return PhysicalSet{}, nil, err
					}
					values = append(values, literal)
				}
				physical.BindVars[key] = values
			} else {
				if len(filter.Values) == 0 {
					return PhysicalSet{}, nil, fmt.Errorf("child filter %q has no value", filter.FieldRef)
				}
				literal, err := filterLiteral(filter.Values[0])
				if err != nil {
					return PhysicalSet{}, nil, err
				}
				physical.BindVars[key] = literal
			}
			predicate.Right = &PhysicalValue{BindKey: key}
		}
		subplan.Operations = append(subplan.Operations, PhysicalOperation{Kind: PhysicalFilterOp, Source: PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, SemanticField: filter.FieldRef}, Filter: &PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	subplan.Return = PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: targetVariable}}
	var output *PhysicalSetOutput
	if policy.RuleEnabled(PhysicalOptimizationRuleCompactProjection) {
		output = compactPhysicalSetOutput(child)
	}
	set := PhysicalSet{Variable: prefix, Subplan: subplan, Unique: true, SortByKey: true, Output: output}
	projections := make([]PhysicalProjection, 0, len(child.Fields))
	for index, field := range child.Fields {
		selection, err := ResolveSemanticField(child.ResourceType, child.Alias, index, field)
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		cardinality, distinct := PhysicalScalarCardinality, false
		nullBehavior := PhysicalPreserveNull
		switch selection.Projection {
		case ProjectionArray:
			cardinality, nullBehavior = PhysicalArrayCardinality, PhysicalEmptyOnNull
		case ProjectionDistinctArray:
			cardinality, distinct, nullBehavior = PhysicalArrayCardinality, true, PhysicalEmptyOnNull
		case ProjectionScalar, ProjectionFirst:
		default:
			return PhysicalSet{}, nil, fmt.Errorf("child field %q has unsupported projection %q", field.Name, selection.Projection)
		}
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + field.Name, Expression: &PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: nullBehavior, Extract: &PhysicalExtract{Source: PhysicalValue{Variable: set.Variable}, ResourceType: child.ResourceType, Selector: selection.Selector, Fallbacks: append([]Selector(nil), selection.Fallbacks...), Distinct: distinct, ExecutionMode: selectorExecutionMode(child.ResourceType, selection.Selector, selection.Fallbacks...)}}})
	}
	for _, aggregate := range child.Aggregates {
		expression, err := physicalAggregateExpression(physical, child.ResourceType, PhysicalValue{Variable: set.Variable}, aggregate, true)
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + aggregate.Name, Expression: &expression})
	}
	for _, pivot := range child.Pivots {
		pivotProjections, err := physicalPivotProjections(physical, child.ResourceType, PhysicalValue{Variable: set.Variable}, pivot, true, projectionPrefix+"__")
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		projections = append(projections, pivotProjections...)
	}
	for _, slice := range child.Slices {
		expression, err := physicalSliceExpression(physical, child.ResourceType, PhysicalValue{Variable: set.Variable}, slice, true)
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + slice.Name, Expression: &expression})
	}
	// A traversal-time projection is the production form of selector reuse. It
	// computes selector arrays in the original child subquery and removes the
	// payload before materialization. The older prepared-set pass is retained
	// only for explicit experiments and is never layered on top of this shape.
	if policy.RuleEnabled(PhysicalOptimizationRuleCompactProjection) {
		// Dynamic maps and deferred pivot families both read the original
		// resource payload at the outer return boundary. A selector-only
		// projection would discard that payload and leave their lookup/pivot
		// evaluation iterating over `child_set_N.payload` (null). Keep the
		// compact identity/payload contract for those sets; ordinary selector
		// consumers still use the cheaper single-materialization projection.
		//
		// A pivot's public columns are OBJECT_LOOKUP projections over its
		// deferred map, so projectPhysicalChildSet cannot discover its key/value
		// selectors from the return projection list. Do not drop payload until a
		// correlated pivot-specific projection contract exists.
		if len(child.DynamicMaps) == 0 && len(child.Pivots) == 0 {
			projectPhysicalChildSet(&set, child.ResourceType, projections)
		} else {
			set.Output = compactPhysicalSetOutput(child)
		}
	}
	if set.Projection == nil {
		prepareRichChildSet(&set, child.ResourceType, projections, policy)
	}
	return set, projections, nil
}

// compactPhysicalSetOutput retains graph identity and only the payload needed
// by downstream selectors/rich consumers. Scope predicates run before this
// projection, so project, generation, and authorization metadata do not need
// to remain in the post-window set. Nested traversal still receives _id, and
// UNIQUE/SORT retain both identity keys to preserve duplicate and ordering
// semantics.
func compactPhysicalSetOutput(node SemanticNode) *PhysicalSetOutput {
	fields := []PhysicalSetOutputField{
		PhysicalSetGraphIDField,
		PhysicalSetKeyField,
		PhysicalSetIDField,
		PhysicalSetResourceTypeField,
	}
	needsPayload := len(node.Fields) > 0 || len(node.Pivots) > 0 || len(node.Slices) > 0 || len(node.DynamicMaps) > 0
	if !needsPayload {
		for _, aggregate := range node.Aggregates {
			if aggregate.Selector != nil || aggregate.Predicate != nil {
				needsPayload = true
				break
			}
		}
	}
	if needsPayload {
		fields = append(fields, PhysicalSetPayloadField)
	}
	return &PhysicalSetOutput{Fields: fields}
}

// prepareRichChildSet adds a generic selector cache when at least two rich
// consumers read the same child relationship set. The cache is deliberately
// selector-based (not FHIR-resource-specific): any generated resource type
// with repeated aggregate, pivot, or slice selectors can use it.
func prepareRichChildSet(set *PhysicalSet, resourceType string, projections []PhysicalProjection, policy PhysicalOptimizationPolicy) {
	if !policy.RuleEnabled(PhysicalOptimizationRulePreparedSelectors) {
		return
	}
	counts := map[string]int{}
	selectors := map[string]Selector{}
	add := func(selector Selector) {
		key := physicalSelectorIdentity(selector)
		counts[key]++
		selectors[key] = selector
	}
	var collect func(*PhysicalExpression)
	collect = func(expression *PhysicalExpression) {
		if expression == nil {
			return
		}
		switch expression.Kind {
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil {
				if expression.Aggregate.Value != nil {
					collect(expression.Aggregate.Value)
				}
				if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
					collect(expression.Aggregate.Predicate.Comparison.LeftExpression)
				}
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil {
				add(expression.Pivot.KeySelector)
				add(expression.Pivot.ValueSelector)
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
					collect(expression.Slice.Predicate.Comparison.LeftExpression)
				}
				for index := range expression.Slice.Projections {
					collect(&expression.Slice.Projections[index].Expression)
				}
			}
		case PhysicalExtractExpression:
			if expression.Extract != nil {
				add(expression.Extract.Selector)
			}
		case PhysicalObjectExpression:
			if expression.Object != nil {
				for index := range expression.Object.Fields {
					collect(&expression.Object.Fields[index].Expression)
				}
			}
		}
	}
	for index := range projections {
		collect(projections[index].Expression)
	}
	eligible := map[string]string{}
	fields := make([]PhysicalPreparedField, 0)
	// Map iteration order must not influence the physical plan. Stable field
	// allocation keeps generated AQL and bind-variable names deterministic,
	// which is important for explain fixtures and cache keys.
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		count := counts[key]
		_, _, savings := estimatePreparedSelectorWork(count)
		if savings <= 0 {
			continue
		}
		field := fmt.Sprintf("__loom_prepared_%d", len(fields))
		eligible[key] = field
		fields = append(fields, PhysicalPreparedField{Name: field, ResourceType: resourceType, Selector: selectors[key]})
	}
	if len(fields) == 0 {
		return
	}
	set.Prepared = &PhysicalPreparedSet{Variable: set.Variable + "_prepared", SourceSetVariable: set.Variable, Fields: fields}
	var annotate func(*PhysicalExpression)
	annotate = func(expression *PhysicalExpression) {
		if expression == nil {
			return
		}
		annotateExtract := func(extract *PhysicalExtract) {
			if extract == nil || extract.Source.Variable != set.Variable {
				return
			}
			// Fallback selectors are an ordered fallback chain. A prepared
			// primary value alone cannot preserve that contract, so leave these
			// extracts on the original renderer path until the prepared schema
			// can represent fallback alternatives explicitly.
			if len(extract.Fallbacks) == 0 {
				if field, ok := eligible[physicalSelectorIdentity(extract.Selector)]; ok {
					extract.Prepared = &PhysicalPreparedReference{SetVariable: set.Prepared.Variable, Field: field}
				}
			}
		}
		switch expression.Kind {
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil {
				if expression.Aggregate.Value != nil {
					annotate(expression.Aggregate.Value)
				}
				if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
					annotate(expression.Aggregate.Predicate.Comparison.LeftExpression)
				}
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil {
				if field, ok := eligible[physicalSelectorIdentity(expression.Pivot.KeySelector)]; ok {
					expression.Pivot.PreparedKey = &PhysicalPreparedReference{SetVariable: set.Prepared.Variable, Field: field}
				}
				if field, ok := eligible[physicalSelectorIdentity(expression.Pivot.ValueSelector)]; ok {
					expression.Pivot.PreparedValue = &PhysicalPreparedReference{SetVariable: set.Prepared.Variable, Field: field}
				}
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
					annotate(expression.Slice.Predicate.Comparison.LeftExpression)
				}
				for index := range expression.Slice.Projections {
					annotate(&expression.Slice.Projections[index].Expression)
				}
			}
		case PhysicalExtractExpression:
			annotateExtract(expression.Extract)
		case PhysicalObjectExpression:
			if expression.Object != nil {
				for index := range expression.Object.Fields {
					annotate(&expression.Object.Fields[index].Expression)
				}
			}
		}
	}
	for index := range projections {
		annotate(projections[index].Expression)
	}
}

// projectPhysicalChildSet attaches selector values to the original set item
// rather than creating a second prepared array. A fallback selector is an
// ordered multi-source contract that cannot be represented by one projected
// field, so the entire set conservatively retains its existing payload output.
func projectPhysicalChildSet(set *PhysicalSet, resourceType string, projections []PhysicalProjection) {
	if set == nil || len(projections) == 0 {
		return
	}
	selectors := map[string]Selector{}
	fallback := false
	var collect func(*PhysicalExpression)
	collect = func(expression *PhysicalExpression) {
		if expression == nil {
			return
		}
		switch expression.Kind {
		case PhysicalExtractExpression:
			if expression.Extract == nil || expression.Extract.Source.Variable != set.Variable {
				return
			}
			if len(expression.Extract.Fallbacks) != 0 {
				fallback = true
				return
			}
			selectors[physicalSelectorIdentity(expression.Extract.Selector)] = expression.Extract.Selector
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil {
				collect(expression.Aggregate.Value)
				if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
					collect(expression.Aggregate.Predicate.Comparison.LeftExpression)
				}
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.Source.Variable == set.Variable {
				selectors[physicalSelectorIdentity(expression.Pivot.KeySelector)] = expression.Pivot.KeySelector
				selectors[physicalSelectorIdentity(expression.Pivot.ValueSelector)] = expression.Pivot.ValueSelector
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
					collect(expression.Slice.Predicate.Comparison.LeftExpression)
				}
				for index := range expression.Slice.Projections {
					collect(&expression.Slice.Projections[index].Expression)
				}
			}
		case PhysicalObjectExpression:
			if expression.Object != nil {
				for index := range expression.Object.Fields {
					collect(&expression.Object.Fields[index].Expression)
				}
			}
		}
	}
	for index := range projections {
		collect(projections[index].Expression)
	}
	if fallback || len(selectors) == 0 {
		return
	}
	keys := make([]string, 0, len(selectors))
	for key := range selectors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]PhysicalSetProjectionField, 0, len(keys))
	fieldBySelector := make(map[string]string, len(keys))
	for index, key := range keys {
		name := fmt.Sprintf("__loom_projection_%d", index)
		fields = append(fields, PhysicalSetProjectionField{Name: name, ResourceType: resourceType, Selector: selectors[key], ExecutionMode: selectorExecutionMode(resourceType, selectors[key])})
		fieldBySelector[key] = name
	}
	var rewrite func(*PhysicalExpression)
	rewrite = func(expression *PhysicalExpression) {
		if expression == nil {
			return
		}
		switch expression.Kind {
		case PhysicalExtractExpression:
			if expression.Extract != nil && expression.Extract.Source.Variable == set.Variable && len(expression.Extract.Fallbacks) == 0 {
				if field := fieldBySelector[physicalSelectorIdentity(expression.Extract.Selector)]; field != "" {
					expression.Extract.Prepared = &PhysicalPreparedReference{SetVariable: set.Variable, Field: field}
				}
			}
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil {
				rewrite(expression.Aggregate.Value)
				if expression.Aggregate.Predicate != nil && expression.Aggregate.Predicate.Comparison != nil {
					rewrite(expression.Aggregate.Predicate.Comparison.LeftExpression)
				}
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.Source.Variable == set.Variable {
				expression.Pivot.PreparedKey = &PhysicalPreparedReference{SetVariable: set.Variable, Field: fieldBySelector[physicalSelectorIdentity(expression.Pivot.KeySelector)]}
				expression.Pivot.PreparedValue = &PhysicalPreparedReference{SetVariable: set.Variable, Field: fieldBySelector[physicalSelectorIdentity(expression.Pivot.ValueSelector)]}
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				if expression.Slice.Predicate != nil && expression.Slice.Predicate.Comparison != nil {
					rewrite(expression.Slice.Predicate.Comparison.LeftExpression)
				}
				for index := range expression.Slice.Projections {
					rewrite(&expression.Slice.Projections[index].Expression)
				}
			}
		case PhysicalObjectExpression:
			if expression.Object != nil {
				for index := range expression.Object.Fields {
					rewrite(&expression.Object.Fields[index].Expression)
				}
			}
		}
	}
	for index := range projections {
		rewrite(projections[index].Expression)
	}
	set.Projection = &PhysicalSetProjection{Fields: fields}
	set.Output = nil
}

func physicalSelectorIdentity(selector Selector) string {
	var b strings.Builder
	for _, step := range selector.Steps {
		b.WriteString(step.Field)
		if step.Iterate {
			b.WriteString("[]")
		}
		if step.Index != nil {
			fmt.Fprintf(&b, "[%d]", *step.Index)
		}
		b.WriteByte('.')
	}
	if selector.Filter != nil {
		b.WriteString("?" + selector.Filter.Field + "=" + selector.Filter.Needle)
	}
	return b.String()
}
