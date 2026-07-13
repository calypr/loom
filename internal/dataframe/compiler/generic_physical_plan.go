package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// BuildGenericPhysicalPlan lowers generic navigation plus root and optional
// child selections into the typed physical IR.
func BuildGenericPhysicalPlan(semantic SemanticPlan) (PhysicalPlan, error) {
	return BuildGenericPhysicalPlanWithPolicy(semantic, DefaultPhysicalOptimizationPolicy())
}

// BuildGenericPhysicalPlanWithPolicy threads an explicit optimizer policy
// through physical construction so prepared-selector ablations happen before
// references to prepared variables are attached to projections.
func BuildGenericPhysicalPlanWithPolicy(semantic SemanticPlan, policy PhysicalOptimizationPolicy) (PhysicalPlan, error) {
	if strings.TrimSpace(semantic.Project) == "" {
		return PhysicalPlan{}, fmt.Errorf("semantic plan project is required")
	}
	if err := ValidateSemanticGraph(semantic); err != nil {
		return PhysicalPlan{}, err
	}
	if !fhirschema.ResourceExists(semantic.Root.ResourceType) {
		return PhysicalPlan{}, fmt.Errorf("root resource type %q is not represented by the generated FHIR schema", semantic.Root.ResourceType)
	}
	if err := validateGenericPhysicalNode(semantic.Root, true); err != nil {
		return PhysicalPlan{}, err
	}

	physical := PhysicalPlan{
		Version: 1,
		Source: PhysicalSource{
			SemanticNode: semantic.Root.Alias,
			ResourceType: semantic.Root.ResourceType,
		},
		BindVars: map[string]any{
			"root_collection":                  semantic.Root.ResourceType,
			"project":                          semantic.Project,
			datasetGenerationBindKey:           datasetGenerationBindValue(semantic.DatasetGeneration),
			"auth_resource_paths":              append([]string(nil), semantic.AuthResourcePaths...),
			"auth_resource_paths_unrestricted": semanticAuthScopeUnrestricted(semantic),
			"scope_allowed":                    true,
		},
		Operations: []PhysicalOperation{
			{
				Kind:     PhysicalRootScanOp,
				Source:   PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType},
				RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
		},
	}
	physical.Operations = appendProjectScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{"root"}, "", semantic.Root)
	physical.Operations = appendAuthScope(physical.Operations, []PhysicalValue{{Variable: "root", Path: []string{"auth_resource_path"}}}, "root_scope_allowed", semantic.Root)
	if err := appendRootPhysicalFilters(&physical, semantic.Root); err != nil {
		return PhysicalPlan{}, err
	}
	if err := appendRequiredTraversalMatchFilters(&physical, semantic.Root); err != nil {
		return PhysicalPlan{}, err
	}

	childSetIndex := 0
	returnProjections := []PhysicalProjection{}
	var walk func(parent SemanticNode, parentVariable, projectionPrefix string) error
	walk = func(parent SemanticNode, parentVariable, projectionPrefix string) error {
		for _, child := range parent.Children {
			if child.MatchMode.required() {
				// Required routes are represented by the root semi-join emitted
				// above for membership, but they may still need a materialized
				// child set for selected fields or nested shaping. The second
				// physical set is post-window output work; it cannot change root
				// membership because the semi-join remains before SORT/LIMIT.
				if physicalNodeNeedsMaterializedSet(child) {
					childSetIndex++
					childProjectionPrefix := child.Alias
					if projectionPrefix != "" {
						childProjectionPrefix = projectionPrefix + "__" + child.Alias
					}
					set, projections, err := buildOptionalChildPhysicalSet(&physical, childSetIndex, parent, parentVariable, child, childProjectionPrefix, policy)
					if err != nil {
						return err
					}
					physical.Operations = append(physical.Operations, PhysicalOperation{Kind: PhysicalSetOp, Source: PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}, Set: &set})
					returnProjections = append(returnProjections, projections...)
					if err := walk(child, set.Variable, childProjectionPrefix); err != nil {
						return err
					}
				}
				continue
			}
			if !physicalNodeNeedsMaterializedSet(child) {
				route, err := resolveStorageRoute(parent.ResourceType, child.EdgeLabel, child.ResourceType)
				if err != nil {
					return err
				}
				traversalIndex := 1
				for _, operation := range physical.Operations {
					if operation.Kind == PhysicalTraversalOp {
						traversalIndex++
					}
				}
				nodeVariable := fmt.Sprintf("node_%d", traversalIndex)
				edgeVariable := fmt.Sprintf("edge_%d", traversalIndex)
				labelBind := fmt.Sprintf("traversal_%d_label", traversalIndex)
				typeBind := fmt.Sprintf("traversal_%d_target_type", traversalIndex)
				edgeCollectionBind := fmt.Sprintf("traversal_%d_edge_collection", traversalIndex)
				physical.BindVars[labelBind] = child.EdgeLabel
				physical.BindVars[typeBind] = child.ResourceType
				physical.BindVars[edgeCollectionBind] = "fhir_edge"
				strategy, endpointField, endpointJoinField, endpointIndexFields := physicalTraversalStrategyForRoute(policy, parentVariable, route)
				physical.Operations = append(physical.Operations, PhysicalOperation{Kind: PhysicalTraversalOp,
					Source: PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel},
					Traversal: &PhysicalTraversal{SourceVariable: parentVariable, TargetVariable: nodeVariable, EdgeVariable: edgeVariable, Direction: route.Direction,
						EdgeCollectionBindKey: edgeCollectionBind, EdgeLabelBindKey: labelBind, TargetTypeBindKey: typeBind, EdgeTargetTypeField: route.targetEdgeTypeField(), Strategy: strategy, EndpointField: endpointField, EndpointJoinField: endpointJoinField, EndpointIndexFields: endpointIndexFields}})
				physical.Operations = appendProjectScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
				physical.Operations = appendDatasetGenerationScope(physical.Operations, []string{edgeVariable, nodeVariable}, child.EdgeLabel, child)
				physical.Operations = appendAuthScope(physical.Operations, []PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: nodeVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("traversal_%d_scope_allowed", traversalIndex), child)
				if err := walk(child, nodeVariable, projectionPrefix); err != nil {
					return err
				}
				continue
			}
			// Optional children are correlated sets. Keeping them in a LET
			// subquery preserves the parent row grain while allowing typed child
			// filters and projections to be applied before materialization.
			childSetIndex++
			childProjectionPrefix := child.Alias
			if projectionPrefix != "" {
				childProjectionPrefix = projectionPrefix + "__" + child.Alias
			}
			set, projections, err := buildOptionalChildPhysicalSet(&physical, childSetIndex, parent, parentVariable, child, childProjectionPrefix, policy)
			if err != nil {
				return err
			}
			physical.Operations = append(physical.Operations, PhysicalOperation{Kind: PhysicalSetOp, Source: PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}, Set: &set})
			returnProjections = append(returnProjections, projections...)
			if err := walk(child, set.Variable, childProjectionPrefix); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(semantic.Root, "root", ""); err != nil {
		return PhysicalPlan{}, err
	}
	projections, err := rootPhysicalProjections(&physical, semantic.Root)
	if err != nil {
		return PhysicalPlan{}, err
	}
	projections = append(projections, returnProjections...)
	physical.Operations = append(physical.Operations, PhysicalOperation{
		Kind:   PhysicalReturnOp,
		Source: PhysicalSource{SemanticNode: semantic.Root.Alias, ResourceType: semantic.Root.ResourceType, SemanticField: "_key"},
		Return: &PhysicalReturn{Projections: projections},
	})
	if err := physical.Validate(); err != nil {
		return PhysicalPlan{}, fmt.Errorf("validate generic physical plan: %w", err)
	}
	if err := ValidateGenericPhysicalPlanScope(physical); err != nil {
		return PhysicalPlan{}, fmt.Errorf("verify generic physical plan scope: %w", err)
	}
	return physical, nil
}

// physicalNodeNeedsMaterializedSet reports whether a node or any optional
// descendant has shaped output. Materializing an otherwise unselected parent
// is necessary to give nested sets a stable correlated source variable.
func physicalNodeNeedsMaterializedSet(node SemanticNode) bool {
	if len(node.Fields) != 0 || len(node.Filters) != 0 || len(node.Pivots) != 0 || len(node.Aggregates) != 0 || len(node.Slices) != 0 {
		return true
	}
	for _, child := range node.Children {
		if child.MatchMode.required() || physicalNodeNeedsMaterializedSet(child) {
			return true
		}
	}
	return false
}

func appendProjectScope(operations []PhysicalOperation, variables []string, relationship string, node SemanticNode) []PhysicalOperation {
	right := PhysicalValue{BindKey: "project"}
	for _, variable := range variables {
		operations = append(operations, PhysicalOperation{
			Kind:   PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: "project"},
			Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
				Operator: "EQUALS",
				Left:     PhysicalValue{Variable: variable, Path: []string{"project"}},
				Right:    &right,
			}},
		})
	}
	return operations
}

// appendDatasetGenerationScope applies the same exact generation bind to
// every physical document participating in a scan/traversal. With a nil bind
// value this renders `dataset_generation == null`, deliberately isolating
// legacy documents from later generation-qualified loads.
func appendDatasetGenerationScope(operations []PhysicalOperation, variables []string, relationship string, node SemanticNode) []PhysicalOperation {
	right := PhysicalValue{BindKey: datasetGenerationBindKey}
	for _, variable := range variables {
		operations = append(operations, PhysicalOperation{
			Kind:   PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: relationship, SemanticField: datasetGenerationField},
			Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
				Operator: "EQUALS",
				Left:     PhysicalValue{Variable: variable, Path: []string{datasetGenerationField}},
				Right:    &right,
			}},
		})
	}
	return operations
}

func appendAuthScope(operations []PhysicalOperation, scopedValues []PhysicalValue, resultVariable string, node SemanticNode) []PhysicalOperation {
	inputs := append([]PhysicalValue(nil), scopedValues...)
	inputs = append(inputs, PhysicalValue{BindKey: "auth_resource_paths"}, PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	operations = append(operations, PhysicalOperation{
		Kind:       PhysicalDerivedLetOp,
		Source:     PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		DerivedLet: &PhysicalDerivedLet{Variable: resultVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: inputs},
	})
	right := PhysicalValue{BindKey: "scope_allowed"}
	return append(operations, PhysicalOperation{
		Kind:   PhysicalFilterOp,
		Source: PhysicalSource{SemanticNode: node.Alias, ResourceType: node.ResourceType, Relationship: node.EdgeLabel, SemanticField: "auth_resource_path"},
		Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: resultVariable}, Right: &right}},
	})
}

func validateGenericPhysicalNode(node SemanticNode, root bool) error {
	for _, child := range node.Children {
		if err := validateGenericPhysicalNode(child, false); err != nil {
			return err
		}
	}
	return nil
}

func rootPhysicalProjections(physical *PhysicalPlan, root SemanticNode) ([]PhysicalProjection, error) {
	projections := []PhysicalProjection{{Name: "_key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}
	for index, field := range root.Fields {
		selection, err := ResolveSemanticField(root.ResourceType, root.Alias, index, field)
		if err != nil {
			return nil, err
		}
		cardinality, distinct := PhysicalScalarCardinality, false
		switch selection.Projection {
		case ProjectionArray:
			cardinality = PhysicalArrayCardinality
		case ProjectionDistinctArray:
			cardinality, distinct = PhysicalArrayCardinality, true
		case ProjectionScalar, ProjectionFirst:
		default:
			return nil, fmt.Errorf("root field %q has unsupported projection %q", field.Name, selection.Projection)
		}
		expression := PhysicalExpression{
			Kind: PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: PhysicalPreserveNull,
			Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: field.Selector, Fallbacks: append([]Selector(nil), field.Fallbacks...), Distinct: distinct, ExecutionMode: selectorExecutionMode(root.ResourceType, field.Selector, field.Fallbacks...)},
		}
		if cardinality == PhysicalArrayCardinality {
			expression.NullBehavior = PhysicalEmptyOnNull
		}
		projections = append(projections, PhysicalProjection{Name: field.Name, Expression: &expression})
	}
	for _, aggregate := range root.Aggregates {
		expression, err := physicalAggregateExpression(physical, root.ResourceType, PhysicalValue{Variable: "root"}, aggregate, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, PhysicalProjection{Name: aggregate.Name, Expression: &expression})
	}
	for _, pivot := range root.Pivots {
		expression, err := physicalPivotExpression(physical, root.ResourceType, PhysicalValue{Variable: "root"}, pivot, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, PhysicalProjection{Name: pivot.Name, Expression: &expression})
	}
	for _, slice := range root.Slices {
		expression, err := physicalSliceExpression(physical, root.ResourceType, PhysicalValue{Variable: "root"}, slice, false)
		if err != nil {
			return nil, err
		}
		projections = append(projections, PhysicalProjection{Name: slice.Name, Expression: &expression})
	}
	return projections, nil
}

func physicalPivotExpression(physical *PhysicalPlan, resourceType string, source PhysicalValue, pivot SemanticPivot, sourceIsSet bool) (PhysicalExpression, error) {
	if len(pivot.Columns) == 0 {
		return PhysicalExpression{}, fmt.Errorf("pivot %q requires bounded columns", pivot.Name)
	}
	// Pivot sources are resource documents (root or correlated child set), not
	// payload values. The renderer applies selectors against each item's
	// payload, preserving the same semantics for singleton and set sources.
	key := "pivot_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(pivot.Name) + "_columns"
	physical.BindVars[key] = append([]string(nil), pivot.Columns...)
	return PhysicalExpression{
		Kind: PhysicalPivotExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull,
		Pivot: &PhysicalPivotMap{Source: source, ResourceType: resourceType, KeySelector: pivot.ColumnSelector, ValueSelector: pivot.ValueSelector, ColumnsBindKey: key},
	}, nil
}

// physicalAggregateExpression lowers one semantic aggregate into the typed
// aggregate IR. A root source is a singleton document; child sources are
// correlated PhysicalSet variables. Selector extraction remains typed so the
// renderer can choose the correct payload iteration without embedding user
// paths in AQL.
func physicalAggregateExpression(physical *PhysicalPlan, resourceType string, source PhysicalValue, aggregate SemanticAggregate, sourceIsSet bool) (PhysicalExpression, error) {
	op := PhysicalAggregateOperation(strings.ToUpper(strings.TrimSpace(aggregate.Operation)))
	switch op {
	case PhysicalCountAggregate, PhysicalCountDistinctAggregate, PhysicalExistsAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
	default:
		return PhysicalExpression{}, fmt.Errorf("aggregate %q uses unsupported operation %q", aggregate.Name, aggregate.Operation)
	}
	aggregatePhysical := PhysicalAggregate{Source: source, Operation: op}
	if aggregate.Selector != nil {
		valueSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			valueSource = PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		value := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: valueSource, ResourceType: resourceType, Selector: *aggregate.Selector, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Selector)}}
		aggregatePhysical.Value = &value
	}
	if aggregate.Predicate != nil {
		leftSource := source
		if !sourceIsSet && source.Variable != "" && len(source.Path) == 0 {
			leftSource = PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
		}
		left := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *aggregate.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *aggregate.Predicate)}}
		comparison := &PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if aggregate.PredicateEquals != "" {
			comparison.Operator = "EQUALS"
			comparison.ValueKind = FilterString
			key := "aggregate_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(aggregate.Name) + "_predicate_equals"
			physical.BindVars[key] = aggregate.PredicateEquals
			comparison.Right = &PhysicalValue{BindKey: key}
		}
		aggregatePhysical.Predicate = &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: comparison}
	}
	cardinality := PhysicalScalarCardinality
	nullBehavior := PhysicalEmptyOnNull
	if op == PhysicalDistinctValuesAggregate {
		cardinality = PhysicalArrayCardinality
	}
	return PhysicalExpression{Kind: PhysicalAggregateExpression, Cardinality: cardinality, NullBehavior: nullBehavior, Aggregate: &aggregatePhysical}, nil
}

// physicalSliceExpression lowers a representative slice into a typed,
// bounded projection over either the singleton root document or a correlated
// child set. The source set is already materialized in stable _key order;
// the explicit sort expression below makes that ordering part of the slice
// contract and gives the renderer a deterministic tie-break.
func physicalSliceExpression(physical *PhysicalPlan, resourceType string, source PhysicalValue, slice SemanticSlice, sourceIsSet bool) (PhysicalExpression, error) {
	_ = sourceIsSet
	if slice.Limit <= 0 {
		return PhysicalExpression{}, fmt.Errorf("slice %q requires positive limit", slice.Name)
	}
	limitKey := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_limit"
	physical.BindVars[limitKey] = slice.Limit
	// Expressions are anchored to the declared source variable for validation;
	// the renderer rebinds them to its per-item loop variable.
	leftSource := source
	physicalSlice := PhysicalSlice{
		Source:       source,
		LimitBindKey: limitKey,
		Sort:         &PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: source.Variable, Path: []string{"_key"}}},
		Projections:  make([]PhysicalExpressionProjection, 0, len(slice.Fields)),
	}
	if slice.Predicate != nil {
		left := PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: *slice.Predicate, ExecutionMode: selectorExecutionMode(resourceType, *slice.Predicate)}}
		comparison := &PhysicalPredicate{Operator: "EXISTS", LeftExpression: &left}
		if slice.PredicateEquals != "" {
			key := "slice_" + sanitizeColumnName(source.Variable) + "_" + sanitizeColumnName(slice.Name) + "_predicate_equals"
			physical.BindVars[key] = slice.PredicateEquals
			comparison.Operator = "EQUALS"
			comparison.ValueKind = FilterString
			comparison.Right = &PhysicalValue{BindKey: key}
		}
		physicalSlice.Predicate = &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: comparison}
	}
	for index, field := range slice.Fields {
		selection := field
		if selection.Name == "" {
			return PhysicalExpression{}, fmt.Errorf("slice %q field %d requires name", slice.Name, index)
		}
		physicalSlice.Projections = append(physicalSlice.Projections, PhysicalExpressionProjection{
			Name: selection.Name,
			Expression: PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
				Extract: &PhysicalExtract{Source: leftSource, ResourceType: resourceType, Selector: selection.Selector, Fallbacks: append([]Selector(nil), selection.Fallbacks...), ExecutionMode: selectorExecutionMode(resourceType, selection.Selector, selection.Fallbacks...)}},
		})
	}
	return PhysicalExpression{Kind: PhysicalSliceExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Slice: &physicalSlice}, nil
}

func appendRootPhysicalFilters(physical *PhysicalPlan, root SemanticNode) error {
	for index, filter := range root.Filters {
		if err := ValidateTypedFilterForResource(root.ResourceType, filter); err != nil {
			return fmt.Errorf("root filter %q: %w", filter.FieldRef, err)
		}
		selector, err := ParseSelector(filter.Selector)
		if err != nil {
			return fmt.Errorf("root filter %q selector: %w", filter.FieldRef, err)
		}
		predicate := PhysicalPredicate{
			Operator: string(filter.Operator), Quantifier: filter.Quantifier, ValueKind: filter.FieldKind,
			LeftExpression: &PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
				Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: root.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(root.ResourceType, selector)}},
		}
		if filter.Operator != FilterExists && filter.Operator != FilterMissing {
			key := fmt.Sprintf("root_filter_%d_value", index+1)
			if filter.Operator == FilterIn {
				values := make([]any, 0, len(filter.Values))
				for _, value := range filter.Values {
					literal, err := filterLiteral(value)
					if err != nil {
						return err
					}
					values = append(values, literal)
				}
				physical.BindVars[key] = values
			} else {
				literal, err := filterLiteral(filter.Values[0])
				if err != nil {
					return err
				}
				physical.BindVars[key] = literal
			}
			predicate.Right = &PhysicalValue{BindKey: key}
		}
		physical.Operations = append(physical.Operations, PhysicalOperation{Kind: PhysicalFilterOp,
			Source: PhysicalSource{SemanticNode: root.Alias, ResourceType: root.ResourceType, SemanticField: filter.FieldRef},
			Filter: &PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: &predicate}}})
	}
	return nil
}

// buildOptionalChildPhysicalSet lowers one root-relative optional traversal.
// The set retains resource documents (rather than pre-shaped maps), allowing
// the renderer to apply each child selector against the document payload at
// the outer return while preserving root row grain.
func buildOptionalChildPhysicalSet(physical *PhysicalPlan, setIndex int, parent SemanticNode, parentVariable string, child SemanticNode, projectionPrefix string, policy PhysicalOptimizationPolicy) (PhysicalSet, []PhysicalProjection, error) {
	route, err := resolveStorageRoute(parent.ResourceType, child.EdgeLabel, child.ResourceType)
	if err != nil {
		return PhysicalSet{}, nil, err
	}
	prefix := fmt.Sprintf("child_set_%d", setIndex)
	labelBind := prefix + "_label"
	typeBind := prefix + "_target_type"
	edgeCollectionBind := prefix + "_edge_collection"
	physical.BindVars[labelBind] = child.EdgeLabel
	physical.BindVars[typeBind] = child.ResourceType
	physical.BindVars[edgeCollectionBind] = "fhir_edge"
	targetVariable := fmt.Sprintf("%s_node", prefix)
	edgeVariable := fmt.Sprintf("%s_edge", prefix)
	strategy, endpointField, endpointJoinField, endpointIndexFields := physicalTraversalStrategyForRoute(policy, parentVariable, route)
	source := PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: child.EdgeLabel}
	subplan := PhysicalSubplan{Captures: []string{parentVariable}, Operations: []PhysicalOperation{{
		Kind: PhysicalTraversalOp, Source: source,
		Traversal: &PhysicalTraversal{SourceVariable: parentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable, Direction: route.Direction, EdgeCollectionBindKey: edgeCollectionBind, EdgeLabelBindKey: labelBind, TargetTypeBindKey: typeBind, EdgeTargetTypeField: route.targetEdgeTypeField(), Strategy: strategy, EndpointField: endpointField, EndpointJoinField: endpointJoinField, EndpointIndexFields: endpointIndexFields},
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
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + field.Name, Expression: &PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Extract: &PhysicalExtract{Source: PhysicalValue{Variable: set.Variable}, ResourceType: child.ResourceType, Selector: selection.Selector, Fallbacks: append([]Selector(nil), selection.Fallbacks...), Distinct: selection.Projection == ProjectionDistinctArray, ExecutionMode: selectorExecutionMode(child.ResourceType, selection.Selector, selection.Fallbacks...)}}})
	}
	for _, aggregate := range child.Aggregates {
		expression, err := physicalAggregateExpression(physical, child.ResourceType, PhysicalValue{Variable: set.Variable}, aggregate, true)
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + aggregate.Name, Expression: &expression})
	}
	for _, pivot := range child.Pivots {
		expression, err := physicalPivotExpression(physical, child.ResourceType, PhysicalValue{Variable: set.Variable}, pivot, true)
		if err != nil {
			return PhysicalSet{}, nil, err
		}
		projections = append(projections, PhysicalProjection{Name: projectionPrefix + "__" + pivot.Name, Expression: &expression})
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
		projectPhysicalChildSet(&set, child.ResourceType, projections)
	}
	if set.Projection == nil {
		prepareRichChildSet(&set, child.ResourceType, projections, policy)
	}
	return set, projections, nil
}

func physicalTraversalStrategyForRoute(policy PhysicalOptimizationPolicy, parentVariable string, route storageRoute) (PhysicalTraversalStrategy, string, string, []string) {
	// Endpoint equality is eligible for every validated depth-one route,
	// including a root sibling group. When sibling sets are shared, the
	// optimizer preserves this typed endpoint contract on the broad set and
	// replaces the target-type predicate with a bind-backed list. If route
	// metadata cannot provide the complete endpoint/index contract, native
	// traversal remains the safe fallback.
	if !policy.RuleEnabled(PhysicalOptimizationRuleEndpointTraversal) {
		return PhysicalTraversalNative, "", "", nil
	}
	if parentField, joinField, fields, ok := route.endpointLookupFields(); ok && len(fields) > 0 {
		return PhysicalTraversalEndpointLookup, parentField, joinField, append([]string(nil), fields...)
	}
	return PhysicalTraversalNative, "", "", nil
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
	needsPayload := len(node.Fields) > 0 || len(node.Pivots) > 0 || len(node.Slices) > 0
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
