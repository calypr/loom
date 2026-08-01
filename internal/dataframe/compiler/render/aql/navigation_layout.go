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
	if plan.Operations[last].Kind != ir.PhysicalReturnOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires RETURN as the final operation")
	}

	layout := physicalNavigationRenderLayout{
		root:      *plan.Operations[0].RootScan,
		rootScope: append([]ir.PhysicalOperation(nil), plan.Operations[1:5]...),
		returnOp:  *plan.Operations[last].Return,
	}
	rootScopeVariable, err := validateGenericNavigationScopeBlock(layout.rootScope, layout.root.Variable, "", layout.root.Variable)
	if err != nil {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root navigation scope: %w", err)
	}

	index := 5
	for index < last && plan.Operations[index].Kind == ir.PhysicalFilterOp && plan.Operations[index].Filter.Expression != nil {
		layout.rootPredicates = append(layout.rootPredicates, plan.Operations[index])
		index++
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
		if err := validateGenericNavigationRootSort(plan.Operations[index], layout.root.Variable); err != nil {
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
	for index < last {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalExpressionLetOp {
			layout.expressionLets = append(layout.expressionLets, operation)
			index++
			continue
		}
		if operation.Kind == ir.PhysicalSetOp {
			layout.sets = append(layout.sets, *operation.Set)
			index++
			continue
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
		layout.traversals = append(layout.traversals, physicalNavigationTraversal{traversal: traversal, scope: scope})
		index += 1 + traversalScopeLength
	}
	unnestVariables := map[string]struct{}{}
	for _, unnest := range layout.unnests {
		unnestVariables[unnest.OutputVariable] = struct{}{}
		if unnest.Ordinality != "" {
			unnestVariables[unnest.Ordinality] = struct{}{}
		}
	}
	if err := validateNavigationReturnScope(layout.returnOp, layout.root.Variable, rootScopeVariable, unnestVariables); err != nil {
		return physicalNavigationRenderLayout{}, err
	}
	return layout, nil
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

func validateGenericNavigationRootSort(operation ir.PhysicalOperation, rootVariable string) error {
	if operation.Sort == nil || !sameRenderPhysicalValue(operation.Sort.Value, ir.PhysicalValue{Variable: rootVariable, Path: []string{"_key"}}) {
		return fmt.Errorf("SORT must order the root variable %s._key", rootVariable)
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

func validateNavigationReturnScope(returnOp ir.PhysicalReturn, rootVariable, rootScopeVariable string, unnestVariables map[string]struct{}) error {
	for _, projection := range returnOp.Projections {
		if projection.Expression != nil {
			continue
		}
		if projection.Value.BindKey != "" {
			continue
		}
		if projection.Value.Variable != rootVariable && projection.Value.Variable != rootScopeVariable {
			if _, ok := unnestVariables[projection.Value.Variable]; ok {
				continue
			}
			return fmt.Errorf("RETURN projection %q references %q, but traversal variables are local to LET subqueries", projection.Name, projection.Value.Variable)
		}
	}
	return nil
}
