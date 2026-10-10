package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	genericPhysicalExecutionLimitBind = "limit"
	datasetGenerationBindKey          = "dataset_generation"
	datasetGenerationField            = "dataset_generation"
)

func buildNavigationRenderLayout(plan ir.PhysicalPlan) (physicalNavigationRenderLayout, error) {
	if len(plan.Operations) < 6 {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires ROOT_SCAN, scope operations, and RETURN")
	}
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires ROOT_SCAN as the first operation")
	}
	last := len(plan.Operations) - 1
	if plan.Operations[last].Kind != ir.PhysicalReturnOp && plan.Operations[last].Kind != ir.PhysicalPopulationMappingReturnOp && plan.Operations[last].Kind != ir.PhysicalCellTraceReturnOp && plan.Operations[last].Kind != ir.PhysicalTableShapeExclusionReturnOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires a supported terminal operation")
	}

	layout := physicalNavigationRenderLayout{
		root:      *plan.Operations[0].RootScan,
		rootScope: append([]ir.PhysicalOperation(nil), plan.Operations[1:5]...),
	}
	if plan.Operations[last].Kind == ir.PhysicalReturnOp {
		returnOp := *plan.Operations[last].Return
		layout.returnOp = &returnOp
	} else if plan.Operations[last].Kind == ir.PhysicalPopulationMappingReturnOp {
		mappingReturn := *plan.Operations[last].PopulationMappingReturn
		layout.mappingReturn = &mappingReturn
	} else if plan.Operations[last].Kind == ir.PhysicalCellTraceReturnOp {
		traceReturn := *plan.Operations[last].CellTraceReturn
		layout.traceReturn = &traceReturn
	} else {
		exclusionReturn := *plan.Operations[last].TableShapeExclusionReturn
		layout.exclusionReturn = &exclusionReturn
	}
	rootScopeVariable, err := validateGenericNavigationScopeBlock(layout.rootScope, layout.root.Variable, "", layout.root.Variable)
	if err != nil {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root navigation scope: %w", err)
	}

	index := 5
	keysetSeen := false
	pageSets := make(map[string]*ir.PhysicalSet)
	usedPageSets := make(map[string]bool)
	pageExpressionLets := make([]ir.PhysicalOperation, 0)
rootPredicates:
	for index < last {
		operation := plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if rootPageKeysetFilter(operation, layout.root.Variable) {
				if keysetSeen {
					return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window predicates contain more than one root-page keyset filter")
				}
				keysetSeen = true
			} else if keysetSeen {
				if operation.Filter == nil || operation.Filter.Expression != nil || operation.Filter.Predicate.LeftExpression != nil {
					return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window page filter must use a projected scalar value")
				}
				for reductionVariable, set := range pageSets {
					if operation.Filter.Predicate.Left.Variable == set.Variable {
						return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window filter must use a SET reduction field, not the set array")
					}
					if !rootPageFilterUsesReduction(operation, set) {
						continue
					}
					if !rootPageFilterMatchesReduction(operation, set) {
						return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window filter does not match SET reduction %q", reductionVariable)
					}
					usedPageSets[reductionVariable] = true
				}
				if hasUnusedRootPageSet(pageSets, usedPageSets) {
					return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window SET must be followed by a filter over its projected reduction field")
				}
			}
			layout.rootPredicates = append(layout.rootPredicates, operation)
			index++
		case ir.PhysicalExpressionLetOp:
			if hasUnusedRootPageSet(pageSets, usedPageSets) {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window SET must be filtered before an expression LET")
			}
			if keysetSeen {
				pageExpressionLets = append(pageExpressionLets, operation)
			}
			// Compiler-owned expression LETs may establish a typed value for a
			// root predicate. Keep them in the pre-window block so the value is
			// computed once per root and before SORT/LIMIT.
			layout.rootPredicates = append(layout.rootPredicates, operation)
			index++
		case ir.PhysicalSortOp:
			break rootPredicates
		case ir.PhysicalSetOp:
			if !keysetSeen {
				// Preserve the ordinary post-window set path for plans without a
				// root-page cursor predicate.
				break rootPredicates
			}
			if hasUnusedRootPageSet(pageSets, usedPageSets) {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window SET must be filtered before another producer")
			}
			if err := validateRootPageRelatedSet(operation, layout.root.Variable); err != nil {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window SET at operation %d: %w", index, err)
			}
			reductionVariable := operation.Set.Reduction.Variable
			if _, duplicate := pageSets[reductionVariable]; duplicate {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window SET reduction %q is duplicated", reductionVariable)
			}
			pageSets[reductionVariable] = operation.Set
			layout.rootPredicates = append(layout.rootPredicates, operation)
			index++
		default:
			if len(pageSets) != 0 {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window related SET segment cannot contain %s", operation.Kind)
			}
			break rootPredicates
		}
	}
	if len(pageSets) != 0 {
		if !keysetSeen || hasUnusedRootPageSet(pageSets, usedPageSets) || index >= last || plan.Operations[index].Kind != ir.PhysicalSortOp {
			return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window related SET segment must be consumed before the root sort")
		}
		for _, operation := range pageExpressionLets {
			if !rootPageExpressionLetRootSafe(operation, layout.root.Variable) {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window expression LET must depend only on the root")
			}
		}
	}
	// UNNEST is a cardinality boundary. It is kept after root predicates and
	// before the execution window so a row-grain-aware compiler can put a
	// database-side LIMIT after expansion. This remains a canonical physical
	// operation; the slice only captures the validated renderer layout.
	for index < last && plan.Operations[index].Kind == ir.PhysicalUnnestOp {
		if plan.Operations[index].Unnest == nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("unnest at operation %d is missing payload", index)
		}
		layout.unnests = append(layout.unnests, *plan.Operations[index].Unnest)
		index++
	}
	if index < last && plan.Operations[index].Kind == ir.PhysicalSortOp {
		if err := validateGenericNavigationRootSort(plan.Operations[index], layout.root.Variable, layout.unnests, plan.PreviewSourceWindowByRootID); err != nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: %w", index, err)
		}
		layout.rootWindow = append(layout.rootWindow, plan.Operations[index])
		index++
		if index < last && plan.Operations[index].Kind == ir.PhysicalLimitOp {
			if err := validateGenericNavigationRootLimit(plan.Operations[index]); err != nil {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: %w", index, err)
			}
			layout.rootWindow = append(layout.rootWindow, plan.Operations[index])
			index++
		}
	} else if index < last && plan.Operations[index].Kind == ir.PhysicalLimitOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: LIMIT requires deterministic root SORT", index)
	}
	if len(pageSets) != 0 && (len(layout.rootWindow) != 2 || layout.rootWindow[1].Limit == nil || layout.rootWindow[1].Limit.BindKey != genericPhysicalExecutionLimitBind) {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root pre-window related SET segment requires the bounded root page window")
	}
	for index < last {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalExpressionLetOp {
			layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: operation})
			index++
			continue
		}
		if operation.Kind == ir.PhysicalSetOp {
			layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: operation})
			index++
			continue
		}
		if operation.Kind == ir.PhysicalGroupedPivotOp || operation.Kind == ir.PhysicalUnpivotOp {
			layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: operation})
			index++
			continue
		}
		if operation.Kind == ir.PhysicalSortOp {
			outputVariable, ok := reshapedOutputVariable(layout.postWindow)
			if !ok || !validateReshapedRowSort(operation, outputVariable) {
				return physicalNavigationRenderLayout{}, fmt.Errorf("execution window at operation %d must sort a reshaped row by its stable row identity", index)
			}
			layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: operation})
			index++
			if index < last && plan.Operations[index].Kind == ir.PhysicalLimitOp {
				if err := validateGenericNavigationRootLimit(plan.Operations[index]); err != nil {
					return physicalNavigationRenderLayout{}, fmt.Errorf("execution window at operation %d: %w", index, err)
				}
				layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: plan.Operations[index]})
				index++
			}
			continue
		}
		if operation.Kind == ir.PhysicalLimitOp {
			return physicalNavigationRenderLayout{}, fmt.Errorf("execution window at operation %d: LIMIT requires deterministic reshaped row SORT", index)
		}
		if operation.Kind == ir.PhysicalUnnestOp {
			return physicalNavigationRenderLayout{}, fmt.Errorf("unnest at operation %d must appear before the root execution window and traversal/set operations", index)
		}
		if operation.Kind != ir.PhysicalTraversalOp {
			return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer expected TRAVERSAL at operation %d, got %s", index, operation.Kind)
		}
		const traversalScopeLength = 6 // edge + target project/generation, then auth LET/filter
		if index+traversalScopeLength >= last {
			return physicalNavigationRenderLayout{}, fmt.Errorf("traversal at operation %d is missing its project/auth scope block", index)
		}
		traversal := *operation.Traversal
		if err := validateGenericNavigationTraversal(plan, traversal); err != nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("traversal at operation %d: %w", index, err)
		}
		scope := append([]ir.PhysicalOperation(nil), plan.Operations[index+1:index+1+traversalScopeLength]...)
		if _, err := validateGenericNavigationScopeBlock(scope, traversal.TargetVariable, traversal.EdgeVariable, traversal.TargetVariable); err != nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("traversal at operation %d scope: %w", index, err)
		}
		layout.postWindow = append(layout.postWindow, physicalNavigationRenderItem{operation: operation, traversalScope: scope})
		index += 1 + traversalScopeLength
	}
	unnestVariables := map[string]struct{}{}
	for _, unnest := range layout.unnests {
		unnestVariables[unnest.OutputVariable] = struct{}{}
		unnestVariables[unnest.HasItemVariable] = struct{}{}
		if unnest.Ordinality != "" {
			unnestVariables[unnest.Ordinality] = struct{}{}
		}
		for _, step := range unnest.Owner.Route {
			unnestVariables[step.Traversal.TargetVariable] = struct{}{}
			if step.Traversal.EdgeVariable != "" {
				unnestVariables[step.Traversal.EdgeVariable] = struct{}{}
			}
		}
	}
	reductionVariables := map[string]struct{}{}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil && operation.RootScan.Population != nil &&
			operation.RootScan.Population.CollectMembersVariable != "" {
			reductionVariables[operation.RootScan.Population.CollectMembersVariable] = struct{}{}
		}
		if operation.Set != nil && operation.Set.Reduction != nil {
			reductionVariables[operation.Set.Reduction.Variable] = struct{}{}
		}
	}
	if layout.returnOp != nil {
		for _, operation := range plan.Operations {
			if operation.Kind == ir.PhysicalGroupedPivotOp && operation.GroupedPivot != nil {
				reductionVariables[operation.GroupedPivot.OutputRowVariable] = struct{}{}
			}
			if operation.Kind == ir.PhysicalUnpivotOp && operation.Unpivot != nil {
				reductionVariables[operation.Unpivot.OutputRowVariable] = struct{}{}
			}
		}
	}
	if layout.returnOp != nil {
		if err := validateNavigationReturnScope(*layout.returnOp, layout.root.Variable, rootScopeVariable, unnestVariables, reductionVariables); err != nil {
			return physicalNavigationRenderLayout{}, err
		}
	}
	return layout, nil
}

func validateRootPageRelatedSet(operation ir.PhysicalOperation, rootVariable string) error {
	if operation.Set == nil {
		return fmt.Errorf("missing payload")
	}
	set := operation.Set
	if !set.Unique || !set.SortByKey || set.SourceSetVariable != "" || set.Reduction == nil ||
		set.Reduction.SourceSetVariable != set.Variable || len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != rootVariable || len(set.Subplan.Operations) == 0 {
		return fmt.Errorf("must be a sorted, unique direct root-correlated reduction")
	}
	traversalCount := 0
	for _, operation := range set.Subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalTraversalOp:
			traversalCount++
			if operation.Traversal == nil || operation.Traversal.SourceVariable != rootVariable {
				return fmt.Errorf("must use one traversal directly from the root")
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
			// These retain the child's typed project, generation, authorization,
			// and authored filter scopes inside the correlated set.
		default:
			return fmt.Errorf("contains unsupported subplan operation %s", operation.Kind)
		}
	}
	first := set.Subplan.Operations[0]
	if traversalCount != 1 || first.Kind != ir.PhysicalTraversalOp || first.Traversal == nil || first.Traversal.SourceVariable != rootVariable {
		return fmt.Errorf("must begin with one traversal directly from the root")
	}
	return nil
}

func rootPageFilterUsesReduction(operation ir.PhysicalOperation, set *ir.PhysicalSet) bool {
	return operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Expression == nil &&
		operation.Filter.Predicate.LeftExpression == nil && set != nil && set.Reduction != nil &&
		operation.Filter.Predicate.Left.Variable == set.Reduction.Variable
}

func rootPageFilterMatchesReduction(operation ir.PhysicalOperation, set *ir.PhysicalSet) bool {
	if !rootPageFilterUsesReduction(operation, set) {
		return false
	}
	left := operation.Filter.Predicate.Left
	if left.BindKey != "" || len(left.Path) != 1 {
		return false
	}
	matchedField := false
	for _, field := range set.Reduction.Fields {
		if field.Name != left.Path[0] {
			continue
		}
		if matchedField || field.Mode != ir.PhysicalSetReductionFirst {
			return false
		}
		matchedField = true
	}
	return matchedField
}

func rootPageExpressionLetRootSafe(operation ir.PhysicalOperation, rootVariable string) bool {
	if operation.Kind != ir.PhysicalExpressionLetOp || operation.ExpressionLet == nil {
		return false
	}
	expression := operation.ExpressionLet.Expression
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return expression.Value != nil && expression.Value.Variable == rootVariable && expression.Value.BindKey == ""
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil || expression.Extract.Prepared != nil ||
			expression.Extract.Source.Variable != rootVariable || expression.Extract.Source.BindKey != "" {
			return false
		}
		for _, fallback := range expression.Extract.Fallbacks {
			if fallback.Source.Variable != rootVariable || fallback.Source.BindKey != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func hasUnusedRootPageSet(sets map[string]*ir.PhysicalSet, used map[string]bool) bool {
	for variable := range sets {
		if !used[variable] {
			return true
		}
	}
	return false
}

func rootPageKeysetFilter(operation ir.PhysicalOperation, rootVariable string) bool {
	if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil || operation.Filter.Expression != nil {
		return false
	}
	predicate := operation.Filter.Predicate
	return predicate.Operator == "GT" && predicate.LeftExpression == nil && predicate.Left.Variable == rootVariable &&
		predicate.Left.BindKey == "" && len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == "_key" &&
		predicate.Right != nil && predicate.Right.BindKey == "loom_root_page_after_key" &&
		predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}

func reshapedOutputVariable(items []physicalNavigationRenderItem) (string, bool) {
	for index := len(items) - 1; index >= 0; index-- {
		operation := items[index].operation
		switch operation.Kind {
		case ir.PhysicalGroupedPivotOp:
			if operation.GroupedPivot != nil {
				return operation.GroupedPivot.OutputRowVariable, true
			}
		case ir.PhysicalUnpivotOp:
			if operation.Unpivot != nil {
				return operation.Unpivot.OutputRowVariable, true
			}
		}
	}
	return "", false
}

func validateReshapedRowSort(operation ir.PhysicalOperation, outputVariable string) bool {
	if operation.Sort == nil || len(operation.Sort.Keys) != 1 {
		return false
	}
	key := operation.Sort.Keys[0]
	return key.Variable == outputVariable && key.BindKey == "" && len(key.Path) == 1 && key.Path[0] == "__loom_row_id"
}

func validateGenericNavigationTraversal(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal) error {
	if traversal.Direction != ir.PhysicalInbound && traversal.Direction != ir.PhysicalOutbound {
		return fmt.Errorf("generic navigation traversal direction must be INBOUND or OUTBOUND, got %q", traversal.Direction)
	}
	wantEdgeTypeField := "from_type"
	if traversal.Direction == ir.PhysicalOutbound {
		wantEdgeTypeField = "to_type"
	}
	if traversal.EdgeTargetTypeField != wantEdgeTypeField {
		return fmt.Errorf("generic navigation traversal %s must constrain edge.%s, got %q", traversal.Direction, wantEdgeTypeField, traversal.EdgeTargetTypeField)
	}
	if traversal.EdgeVariable == "" || traversal.EdgeLabelBindKey == "" || traversal.TargetTypeBindKey == "" {
		return fmt.Errorf("generic navigation traversal requires edge variable, edge label bind, and target type bind")
	}
	collection, ok := plan.BindVars[traversal.EdgeCollectionBindKey].(string)
	if !ok || collection != "fhir_edge" {
		return fmt.Errorf("generic navigation traversal must use fhir_edge through its collection bind")
	}
	strategy := traversal.Strategy
	if strategy == "" {
		strategy = ir.PhysicalTraversalNative
	}
	if strategy != ir.PhysicalTraversalNative && strategy != ir.PhysicalTraversalEndpointLookup {
		return fmt.Errorf("unsupported generic navigation traversal strategy %q", strategy)
	}
	if strategy == ir.PhysicalTraversalEndpointLookup {
		wantEndpoint, wantJoin := "_to", "_from"
		wantIndexType := "from_type"
		if traversal.Direction == ir.PhysicalOutbound {
			wantEndpoint, wantJoin, wantIndexType = "_from", "_to", "to_type"
		}
		if traversal.EndpointField != wantEndpoint || traversal.EndpointJoinField != wantJoin {
			return fmt.Errorf("endpoint lookup %s requires %s -> %s, got %s -> %s", traversal.Direction, wantEndpoint, wantJoin, traversal.EndpointField, traversal.EndpointJoinField)
		}
		wantIndex := []string{wantEndpoint, "project", "dataset_generation", "label", wantIndexType}
		if len(traversal.EndpointIndexFields) != len(wantIndex) {
			return fmt.Errorf("endpoint lookup requires compound index fields %#v", wantIndex)
		}
		for index := range wantIndex {
			if traversal.EndpointIndexFields[index] != wantIndex[index] {
				return fmt.Errorf("endpoint lookup index field %d = %q, want %q", index, traversal.EndpointIndexFields[index], wantIndex[index])
			}
		}
	}
	return nil
}

func validateGenericNavigationRootSort(operation ir.PhysicalOperation, rootVariable string, unnests []ir.PhysicalUnnest, previewSourceWindowByRootID bool) error {
	if operation.Sort == nil {
		return fmt.Errorf("SORT requires typed keys")
	}
	if previewSourceWindowByRootID {
		want := []ir.PhysicalValue{{Variable: rootVariable, Path: []string{"id"}}}
		if len(unnests) != 0 || len(operation.Sort.Keys) != 1 || !sameRenderPhysicalValue(operation.Sort.Keys[0], want[0]) {
			return fmt.Errorf("preview source SORT must use the proven root resource id")
		}
		return nil
	}
	want := []ir.PhysicalValue{{Variable: rootVariable, Path: []string{"_key"}}}
	if len(unnests) > 1 {
		return fmt.Errorf("generic navigation supports one row expansion")
	}
	if len(unnests) == 1 {
		want = ir.PhysicalUnnestSortKeys(unnests[0])
	}
	if len(operation.Sort.Keys) != len(want) {
		return fmt.Errorf("SORT keys do not match the stable row identity tuple")
	}
	for index := range want {
		if !sameRenderPhysicalValue(operation.Sort.Keys[index], want[index]) {
			return fmt.Errorf("SORT key %d does not match the stable row identity tuple", index)
		}
	}
	return nil
}

func validateGenericNavigationRootLimit(operation ir.PhysicalOperation) error {
	if operation.Limit == nil || operation.Limit.BindKey != genericPhysicalExecutionLimitBind {
		return fmt.Errorf("LIMIT must use @%s", genericPhysicalExecutionLimitBind)
	}
	return nil
}

// validateGenericNavigationScopeBlock accepts the exact generation-safe scope
// operations emitted by appendProjectScope, appendDatasetGenerationScope, and
// appendAuthScope. The standalone
// scope verifier is intentionally more flexible; rendering is stricter so it
// can relocate the whole block safely into a LET subquery.
func validateGenericNavigationScopeBlock(operations []ir.PhysicalOperation, resourceVariable, edgeVariable, targetVariable string) (string, error) {
	expectedProjectVariables := []string{resourceVariable}
	expectedGenerationVariables := []string{resourceVariable}
	if edgeVariable != "" {
		expectedProjectVariables = []string{edgeVariable, targetVariable}
		expectedGenerationVariables = []string{edgeVariable, targetVariable}
	}
	expectedLength := len(expectedProjectVariables) + len(expectedGenerationVariables) + 2
	if len(operations) != expectedLength || operations[0].Kind != ir.PhysicalFilterOp {
		return "", fmt.Errorf("requires project filters for every graph document, dataset_generation filters, LET AUTH_RESOURCE_PATH_ALLOWED, FILTER scope_allowed in order")
	}
	for index, variable := range expectedProjectVariables {
		operation := operations[index]
		if operation.Kind != ir.PhysicalFilterOp || !matchesPhysicalEquality(operation.Filter.Predicate, ir.PhysicalValue{Variable: variable, Path: []string{"project"}}, ir.PhysicalValue{BindKey: "project"}) {
			return "", fmt.Errorf("project scope must be %s.project == @project", variable)
		}
	}
	for index, variable := range expectedGenerationVariables {
		operation := operations[len(expectedProjectVariables)+index]
		if operation.Kind != ir.PhysicalFilterOp || !matchesPhysicalEquality(operation.Filter.Predicate, ir.PhysicalValue{Variable: variable, Path: []string{datasetGenerationField}}, ir.PhysicalValue{BindKey: datasetGenerationBindKey}) {
			return "", fmt.Errorf("dataset generation scope must be %s.%s == @%s", variable, datasetGenerationField, datasetGenerationBindKey)
		}
	}
	authLetIndex := len(expectedProjectVariables) + len(expectedGenerationVariables)
	authFilterIndex := authLetIndex + 1

	if operations[authLetIndex].Kind != ir.PhysicalDerivedLetOp || operations[authLetIndex].DerivedLet == nil {
		return "", fmt.Errorf("scope block requires AUTH_RESOURCE_PATH_ALLOWED LET after dataset generation scope")
	}
	derived := operations[authLetIndex].DerivedLet
	if strings.ToUpper(strings.TrimSpace(derived.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
		return "", fmt.Errorf("scope LET must use AUTH_RESOURCE_PATH_ALLOWED")
	}
	expectedInputs := []ir.PhysicalValue{{Variable: resourceVariable, Path: []string{"auth_resource_path"}}}
	if edgeVariable != "" {
		expectedInputs = []ir.PhysicalValue{
			{Variable: edgeVariable, Path: []string{"auth_resource_path"}},
			{Variable: targetVariable, Path: []string{"auth_resource_path"}},
		}
	}
	expectedInputs = append(expectedInputs, ir.PhysicalValue{BindKey: "auth_resource_paths"}, ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	if len(derived.Inputs) != len(expectedInputs) {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires the exact generic auth scope inputs")
	}
	for index := range expectedInputs {
		if !sameRenderPhysicalValue(derived.Inputs[index], expectedInputs[index]) {
			return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED input %d is not the required generic scope value", index)
		}
	}
	if operations[authFilterIndex].Kind != ir.PhysicalFilterOp || !matchesPhysicalEquality(operations[authFilterIndex].Filter.Predicate, ir.PhysicalValue{Variable: derived.Variable}, ir.PhysicalValue{BindKey: "scope_allowed"}) {
		return "", fmt.Errorf("auth scope must be %s == @scope_allowed", derived.Variable)
	}
	return derived.Variable, nil
}

func matchesPhysicalEquality(predicate ir.PhysicalPredicate, left, right ir.PhysicalValue) bool {
	return strings.ToUpper(strings.TrimSpace(predicate.Operator)) == "EQUALS" &&
		predicate.Right != nil &&
		sameRenderPhysicalValue(predicate.Left, left) &&
		sameRenderPhysicalValue(*predicate.Right, right)
}

func sameRenderPhysicalValue(left, right ir.PhysicalValue) bool {
	if left.Variable != right.Variable || left.BindKey != right.BindKey || len(left.Path) != len(right.Path) {
		return false
	}
	for index := range left.Path {
		if left.Path[index] != right.Path[index] {
			return false
		}
	}
	return true
}

func validateNavigationReturnScope(returnOp ir.PhysicalReturn, rootVariable, rootScopeVariable string, unnestVariables, reductionVariables map[string]struct{}) error {
	for _, projection := range returnOp.Projections {
		if projection.Expression != nil || projection.PresenceOutput {
			continue
		}
		if projection.Value.BindKey != "" {
			continue
		}
		if projection.Value.Variable != rootVariable && projection.Value.Variable != rootScopeVariable {
			if _, ok := unnestVariables[projection.Value.Variable]; ok {
				continue
			}
			if _, ok := reductionVariables[projection.Value.Variable]; ok {
				continue
			}
			return fmt.Errorf("RETURN projection %q references %q, but traversal variables are local to LET subqueries", projection.Name, projection.Value.Variable)
		}
	}
	return nil
}
