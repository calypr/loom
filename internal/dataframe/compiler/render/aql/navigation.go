package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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
	if plan.Operations[last].Kind != ir.PhysicalReturnOp && plan.Operations[last].Kind != ir.PhysicalPopulationMappingReturnOp && plan.Operations[last].Kind != ir.PhysicalCellTraceReturnOp {
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

			layout.rootPredicates = append(layout.rootPredicates, operation)
			index++
		case ir.PhysicalSortOp:
			break rootPredicates
		case ir.PhysicalSetOp:
			if !keysetSeen {

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
		const traversalScopeLength = 6
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
	lines = append(lines, r.renderTraversalScan(traversal, parentVariable, traversalIndent)...)
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

func (r *physicalPlanRenderer) renderTraversalScan(traversal ir.PhysicalTraversal, sourceVariable, indent string) []string {
	strategy := traversal.Strategy
	if strategy == "" {
		strategy = ir.PhysicalTraversalNative
	}
	if strategy == ir.PhysicalTraversalEndpointLookup {
		return []string{
			fmt.Sprintf("%sFOR %s IN @@%s", indent, traversal.EdgeVariable, traversal.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == %s._id", indent, traversal.EdgeVariable, traversal.EndpointField, sourceVariable),
			fmt.Sprintf("%s  FILTER %s.label == @%s", indent, traversal.EdgeVariable, traversal.EdgeLabelBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == @%s", indent, traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
			fmt.Sprintf("%s  LET %s = DOCUMENT(%s.%s)", indent, traversal.TargetVariable, traversal.EdgeVariable, traversal.EndpointJoinField),
			fmt.Sprintf("%s  FILTER %s != null", indent, traversal.TargetVariable),
			fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent, traversal.TargetVariable, traversal.TargetTypeBindKey),
		}
	}
	return []string{
		fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", indent, traversal.TargetVariable, traversal.EdgeVariable, traversal.Direction, sourceVariable, traversal.EdgeCollectionBindKey),
		fmt.Sprintf("%s  FILTER %s.label == @%s", indent, traversal.EdgeVariable, traversal.EdgeLabelBindKey),
		fmt.Sprintf("%s  FILTER %s.%s == @%s", indent, traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
		fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent, traversal.TargetVariable, traversal.TargetTypeBindKey),
	}
}

func (r *physicalPlanRenderer) renderUnnestSource(unnest ir.PhysicalUnnest) (string, error) {
	if len(unnest.Ancestors) == 0 {
		return r.renderExpression(unnest.Expression)
	}
	extract := unnest.Expression.Extract
	if extract == nil || extract.Prepared != nil || len(extract.Selector.Steps) == 0 {
		return "", fmt.Errorf("unnest ancestry requires an unprepared selector source")
	}
	selector := extract.Selector
	if !selector.Steps[len(selector.Steps)-1].Iterate {
		return "", fmt.Errorf("unnest ancestry requires a repeated terminal selector step")
	}
	source, err := r.renderValue(extract.Source)
	if err != nil {
		return "", err
	}
	ancestorsByStep := make(map[int]ir.PhysicalUnnestAncestor, len(unnest.Ancestors))
	for index, ancestor := range unnest.Ancestors {
		if ancestor.StepIndex < 0 || ancestor.StepIndex >= len(selector.Steps)-1 || !selector.Steps[ancestor.StepIndex].Iterate {
			return "", fmt.Errorf("unnest ancestor %d does not refer to a repeated prefix step", index)
		}
		if _, duplicate := ancestorsByStep[ancestor.StepIndex]; duplicate {
			return "", fmt.Errorf("unnest repeats ancestry step %d", ancestor.StepIndex)
		}
		ancestorsByStep[ancestor.StepIndex] = ancestor
	}
	root := r.newInternalVariable("unnest_lineage_root")
	current := root
	capturedAncestors := make(map[int]string, len(unnest.Ancestors))
	lines := []string{"FOR " + root + " IN [" + source + "]"}
	for index, step := range selector.Steps {
		if index == len(selector.Steps)-1 && selector.Filter != nil {
			key := r.newInternalBindKey("unnest_lineage_contains")
			r.bindVars[key] = selector.Filter.Needle
			lines = append(lines, fmt.Sprintf("  FILTER CONTAINS(%s.%s ? %s.%s : \"\", @%s)", current, selector.Filter.Field, current, selector.Filter.Field, key))
		}
		next := r.newInternalVariable(fmt.Sprintf("unnest_lineage_step_%d", index))
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines,
				fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index),
				"  FILTER "+next+" != null",
			)
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, current, step.Field))
			if index < len(selector.Steps)-1 {
				lines = append(lines, "  FILTER "+next+" != null")
			}
		}
		current = next
		if _, capture := ancestorsByStep[index]; capture {
			capturedAncestors[index] = current
		}
		if index == len(selector.Steps)-1 {
			lines = append(lines, "  FILTER "+current+" != null")
		}
	}
	fields := []string{"item: " + current}
	for index, ancestor := range unnest.Ancestors {
		variable, ok := capturedAncestors[ancestor.StepIndex]
		if !ok {
			return "", fmt.Errorf("unnest ancestor %d was not captured", index)
		}
		fields = append(fields, fmt.Sprintf("ancestor_%d: %s", index, variable))
	}
	lines = append(lines, "  RETURN {"+strings.Join(fields, ", ")+"}")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )", nil
}

func (r *physicalPlanRenderer) renderUnnest(unnest ir.PhysicalUnnest, indent string, ordinal int) ([]string, error) {
	source, err := r.renderUnnestSource(unnest)
	if err != nil {
		return nil, fmt.Errorf("unnest source: %w", err)
	}
	if unnest.Owner.RootVariable == "" || unnest.Owner.OwnerVariable == "" || unnest.OutputVariable == "" || unnest.HasItemVariable == "" {
		return nil, fmt.Errorf("unnest requires root, owner, output, and item-discriminator variables")
	}
	lines := make([]string, 0, len(unnest.Owner.Route)*8+6)
	parentVariable := unnest.Owner.RootVariable
	for routeIndex, step := range unnest.Owner.Route {
		lines = append(lines, r.renderTraversalScan(step.Traversal, parentVariable, indent)...)
		for scopeIndex, operation := range step.Scope {
			rendered, err := r.renderScopeOperation(operation, indent+"  ")
			if err != nil {
				return nil, fmt.Errorf("owner route step %d scope operation %d: %w", routeIndex, scopeIndex, err)
			}
			lines = append(lines, rendered...)
		}
		parentVariable = step.Traversal.TargetVariable
	}
	if parentVariable != unnest.Owner.OwnerVariable {
		return nil, fmt.Errorf("unnest route terminates at %q, not owner %q", parentVariable, unnest.Owner.OwnerVariable)
	}
	rawSourceVariable := r.newInternalVariable(fmt.Sprintf("unnest_raw_source_%d", ordinal))
	sourceVariable := r.newInternalVariable(fmt.Sprintf("unnest_source_%d", ordinal))

	lines = append(lines,
		fmt.Sprintf("%sLET %s = %s", indent, rawSourceVariable, source),
		fmt.Sprintf("%sLET %s = (%s == null ? [] : FLATTEN(%s))", indent, sourceVariable, rawSourceVariable, rawSourceVariable),
	)
	if unnest.EmptyPolicy == ir.PhysicalUnnestError {
		errorBindKey := fmt.Sprintf("unnest_error_occurrence_%d", ordinal)
		occurrenceID := unnest.Owner.OccurrenceID
		if occurrenceID == "" {
			occurrenceID = "root"
		}
		r.bindVars[errorBindKey] = occurrenceID
		message := fmt.Sprintf("CONCAT(\"%s: row expansion occurrence \", @%s, \" has no items for owner \", %s._key)", dataframeerrors.CodeConstructionExpansionEmpty, errorBindKey, unnest.Owner.OwnerVariable)
		lines = append(lines, fmt.Sprintf("%sFILTER ASSERT(LENGTH(%s) > 0, %s)", indent, sourceVariable, message))
	}
	indexVariable := r.newInternalVariable(fmt.Sprintf("unnest_index_%d", ordinal))
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? [] : RANGE(0, LENGTH(%s) - 1)", sourceVariable, sourceVariable)
	if unnest.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", sourceVariable, sourceVariable)
	}
	lines = append(lines, fmt.Sprintf("%sFOR %s IN (%s)", indent, indexVariable, indices))
	itemSource := fmt.Sprintf("%s[%s]", sourceVariable, indexVariable)
	carrierVariable := ""
	if len(unnest.Ancestors) > 0 {
		carrierVariable = r.newInternalVariable(fmt.Sprintf("unnest_lineage_item_%d", ordinal))
		lines = append(lines, fmt.Sprintf("%sLET %s = %s == null ? null : %s", indent, carrierVariable, indexVariable, itemSource))
		itemSource = carrierVariable + ".item"
	}
	item := fmt.Sprintf("%s == null ? null : %s", indexVariable, itemSource)
	lines = append(lines, fmt.Sprintf("%sLET %s = %s", indent, unnest.OutputVariable, item))
	lines = append(lines, fmt.Sprintf("%sLET %s = %s != null", indent, unnest.HasItemVariable, indexVariable))
	for index, ancestor := range unnest.Ancestors {
		lines = append(lines, fmt.Sprintf("%sLET %s = %s == null ? null : %s.ancestor_%d", indent, ancestor.Variable, carrierVariable, carrierVariable, index))
	}
	if unnest.Ordinality != "" {
		lines = append(lines, fmt.Sprintf("%sLET %s = %s", indent, unnest.Ordinality, indexVariable))
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
				lines = append(lines,
					fmt.Sprintf("%s  FILTER POSITION(@%s, %s.resourceType)", indent, t.TargetTypeBindKey, t.TargetVariable),
					fmt.Sprintf("%s  FILTER %s.%s == %s.resourceType", indent, t.EdgeVariable, t.EdgeTargetTypeField, t.TargetVariable),
				)
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
			rendered, err := r.renderUnnest(*operation.Unnest, indent+"  ", opIndex+1)
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
		lines = append(lines, indent+"  SORT NOT_NULL("+t.TargetVariable+".logical_key, "+t.TargetVariable+"._key), "+t.TargetVariable+"._key")
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

func (r *physicalPlanRenderer) renderPhysicalSetProjection(item string, projection ir.PhysicalSetProjection) (string, error) {
	if len(projection.Fields) == 0 {
		return "", fmt.Errorf("set projection requires at least one field")
	}
	fields := []string{
		"_id: " + item + "._id",
		"_key: " + item + "._key",
		"logical_key: " + item + ".logical_key",
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

	fields := []string{
		fmt.Sprintf("_key: %s._key", item),
		fmt.Sprintf("logical_key: %s.logical_key", item),
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
			fmt.Sprintf("%s  FILTER %s.%s == %s.resourceType", indent, t.EdgeVariable, t.EdgeTargetTypeField, t.TargetVariable),
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
			rendered, err := r.renderUnnest(*operation.Unnest, "      ", index)
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
		lines = append(lines, "      SORT NOT_NULL("+set.ItemVariable+".logical_key, "+set.ItemVariable+"._key), "+set.ItemVariable+"._key")
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

func (r *physicalPlanRenderer) newInternalVariable(suffix string) string {
	base := "__loom_physical_" + r.internalPrefix + suffix
	variable := base
	for counter := 1; ; counter++ {
		if _, exists := r.reservedVars[variable]; !exists {
			r.reservedVars[variable] = struct{}{}
			return variable
		}
		variable = fmt.Sprintf("%s_%d", base, counter)
	}
}

func renderPhysicalSetOutput(value string, output *ir.PhysicalSetOutput) (string, error) {
	if output == nil {
		return value, nil
	}
	fields := make([]string, 0, len(output.Fields))
	for _, field := range output.Fields {
		name := string(field)
		switch field {
		case ir.PhysicalSetGraphIDField, ir.PhysicalSetKeyField, ir.PhysicalSetLogicalKeyField, ir.PhysicalSetIDField, ir.PhysicalSetResourceTypeField, ir.PhysicalSetPayloadField:
			fields = append(fields, fmt.Sprintf("%s: %s.%s", name, value, name))
		default:
			return "", fmt.Errorf("unsupported compact set output field %q", field)
		}
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func physicalPlanVariableNames(plan ir.PhysicalPlan) map[string]struct{} {
	variables := map[string]struct{}{}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			variables[operation.RootScan.Variable] = struct{}{}
			if seed := operation.RootScan.PageCandidateSeed; seed != nil {
				for _, seedOperation := range seed.Subplan.Operations {
					switch seedOperation.Kind {
					case ir.PhysicalCollectionScanOp:
						variables[seedOperation.CollectionScan.Variable] = struct{}{}
					case ir.PhysicalTraversalOp:
						variables[seedOperation.Traversal.SourceVariable] = struct{}{}
						variables[seedOperation.Traversal.TargetVariable] = struct{}{}
						if seedOperation.Traversal.EdgeVariable != "" {
							variables[seedOperation.Traversal.EdgeVariable] = struct{}{}
						}
					case ir.PhysicalDerivedLetOp:
						variables[seedOperation.DerivedLet.Variable] = struct{}{}
					case ir.PhysicalExpressionLetOp:
						variables[seedOperation.ExpressionLet.Variable] = struct{}{}
					}
				}
			}
			if population := operation.RootScan.Population; population != nil {
				variables[population.MemberScan.Variable] = struct{}{}
				if population.CollectMembersVariable != "" {
					variables[population.CollectMembersVariable] = struct{}{}
				}
				for _, populationOperation := range population.ResourceOperations {
					switch populationOperation.Kind {
					case ir.PhysicalCollectionScanOp:
						variables[populationOperation.CollectionScan.Variable] = struct{}{}
					case ir.PhysicalTraversalOp:
						variables[populationOperation.Traversal.TargetVariable] = struct{}{}
						variables[populationOperation.Traversal.EdgeVariable] = struct{}{}
					case ir.PhysicalDerivedLetOp:
						variables[populationOperation.DerivedLet.Variable] = struct{}{}
					}
				}
			}
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
			variables[operation.Unnest.Owner.RootVariable] = struct{}{}
			variables[operation.Unnest.Owner.OwnerVariable] = struct{}{}
			for _, step := range operation.Unnest.Owner.Route {
				variables[step.Traversal.SourceVariable] = struct{}{}
				variables[step.Traversal.TargetVariable] = struct{}{}
				if step.Traversal.EdgeVariable != "" {
					variables[step.Traversal.EdgeVariable] = struct{}{}
				}
				for _, scoped := range step.Scope {
					if scoped.Kind == ir.PhysicalDerivedLetOp {
						variables[scoped.DerivedLet.Variable] = struct{}{}
					}
					if scoped.Kind == ir.PhysicalExpressionLetOp {
						variables[scoped.ExpressionLet.Variable] = struct{}{}
					}
				}
			}
			variables[operation.Unnest.OutputVariable] = struct{}{}
			variables[operation.Unnest.HasItemVariable] = struct{}{}
			if operation.Unnest.Ordinality != "" {
				variables[operation.Unnest.Ordinality] = struct{}{}
			}
		case ir.PhysicalGroupedPivotOp:
			pivot := operation.GroupedPivot
			variables[pivot.InputRowVariable] = struct{}{}
			variables[pivot.GroupRowsVariable] = struct{}{}
			variables[pivot.OutputRowVariable] = struct{}{}
			for _, key := range pivot.GroupKeys {
				variables[key.Variable] = struct{}{}
			}
		case ir.PhysicalUnpivotOp:
			unpivot := operation.Unpivot
			variables[unpivot.InputRowVariable] = struct{}{}
			variables[unpivot.SlotVariable] = struct{}{}
			variables[unpivot.OutputRowVariable] = struct{}{}
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
