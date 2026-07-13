package aql

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RenderedPhysicalPlan is an executable AQL representation of a validated
// PhysicalPlan. BindVars is independent of the input plan and uses Arango's
// required "@name" key form for collection bind variables referenced as
// "@@name" in Query.
//
// This renderer covers generic physical navigation and rich expression
// operators emitted by BuildGenericPhysicalPlan. Projection names, including
// nested object field names, remain bind-backed and never become AQL source.
type RenderedPhysicalPlan struct {
	Query    string
	BindVars map[string]any
}

// RenderPhysicalPlan renders a validated physical plan to deterministic AQL.
// It keeps data and metadata values out of the generated AQL source.
func RenderPhysicalPlan(plan PhysicalPlan) (RenderedPhysicalPlan, error) {
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}

	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := validateRenderablePhysicalPlan(plan, collectionKeys); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	// This renderer deliberately supports only BuildGenericPhysicalPlan's
	// navigation contract. Validate its required project/auth windows again at
	// the executable boundary so a manually assembled plan cannot render an
	// unscoped resource scan.
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("verify renderable generic physical plan scope: %w", err)
	}
	layout, err := buildNavigationRenderLayout(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}

	renderer := physicalPlanRenderer{
		bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   physicalPlanVariableNames(plan),
	}
	lines := make([]string, 0, len(plan.Operations)+1)
	lines = append(lines, fmt.Sprintf("FOR %s IN @@%s", layout.root.Variable, layout.root.CollectionBindKey))
	for index, operation := range layout.rootScope {
		line, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root scope operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, operation := range layout.rootPredicates {
		line, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root predicate %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, operation := range layout.rootWindow {
		line, err := renderer.renderRootWindowOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root execution window operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, traversal := range layout.traversals {
		line, err := renderer.renderTraversalSet(traversal, layout.root.Variable, index+1)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render traversal %d: %w", index+1, err)
		}
		lines = append(lines, line...)
	}
	for index, set := range layout.sets {
		line, err := renderer.renderSet(set, index+1)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render child set %d: %w", index+1, err)
		}
		lines = append(lines, line...)
	}
	returnExpression, err := renderer.renderReturn(layout.returnOp)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render RETURN: %w", err)
	}
	lines = append(lines, "RETURN "+returnExpression)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{
		Query:    query,
		BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query),
	}, nil
}

// pruneUnusedRuntimeBindVars is required after physical rewrites. Traversal
// sharing can remove a typed edge predicate while retaining its original
// logical bind in the cloned plan. Arango rejects undeclared bind variables,
// so only values referenced by the final rendered AQL may cross the execution
// boundary.
func pruneUnusedRuntimeBindVars(bindVars map[string]any, query string) map[string]any {
	pruned := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if strings.Contains(query, "@"+key) {
			pruned[key] = value
		}
	}
	return pruned
}

func PruneUnusedRuntimeBindVars(bindVars map[string]any, query string) map[string]any {
	return pruneUnusedRuntimeBindVars(bindVars, query)
}

func (r *physicalPlanRenderer) renderRootWindowOperation(operation PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case PhysicalSortOp:
		value, err := r.renderValue(operation.Sort.Value)
		if err != nil {
			return nil, err
		}
		return []string{indent + "SORT " + value}, nil
	case PhysicalLimitOp:
		if _, collectionBinding := r.collectionKeys[operation.Limit.BindKey]; collectionBinding {
			return nil, fmt.Errorf("limit bind key %q cannot be a collection bind", operation.Limit.BindKey)
		}
		return []string{indent + "LIMIT @" + operation.Limit.BindKey}, nil
	default:
		return nil, fmt.Errorf("root execution window cannot contain physical operation %q", operation.Kind)
	}
}

type physicalPlanRenderer struct {
	bindVars       map[string]any
	collectionKeys map[string]struct{}
	setVariables   map[string]string
	reservedVars   map[string]struct{}
	preparedItem   string
}

func (r *physicalPlanRenderer) renderScopeOperation(operation PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case PhysicalFilterOp:
		var expression string
		var err error
		if operation.Filter.Expression != nil {
			expression, err = r.renderPredicateExpression(*operation.Filter.Expression, indent)
		} else {
			expression, err = r.renderPredicate(operation.Filter.Predicate)
		}
		if err != nil {
			return nil, err
		}
		return []string{indent + "FILTER " + expression}, nil
	case PhysicalDerivedLetOp:
		expression, err := r.renderDerivedLet(*operation.DerivedLet)
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("%sLET %s = %s", indent, operation.DerivedLet.Variable, expression)}, nil
	default:
		return nil, fmt.Errorf("navigation scope cannot contain physical operation %q", operation.Kind)
	}
}

// physicalNavigationRenderLayout is the intentionally narrow executable shape
// produced by BuildGenericPhysicalPlan. Traversals are retained as sets rather
// than emitted as top-level loops so the root scan remains the row grain.
type physicalNavigationRenderLayout struct {
	root           PhysicalRootScan
	rootScope      []PhysicalOperation
	rootPredicates []PhysicalOperation
	rootWindow     []PhysicalOperation
	traversals     []physicalNavigationTraversal
	sets           []PhysicalSet
	returnOp       PhysicalReturn
}

type physicalNavigationTraversal struct {
	traversal PhysicalTraversal
	scope     []PhysicalOperation
}

func buildNavigationRenderLayout(plan PhysicalPlan) (physicalNavigationRenderLayout, error) {
	if len(plan.Operations) < 6 {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires ROOT_SCAN, scope operations, and RETURN")
	}
	if plan.Operations[0].Kind != PhysicalRootScanOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires ROOT_SCAN as the first operation")
	}
	last := len(plan.Operations) - 1
	if plan.Operations[last].Kind != PhysicalReturnOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("generic navigation renderer requires RETURN as the final operation")
	}

	layout := physicalNavigationRenderLayout{
		root:      *plan.Operations[0].RootScan,
		rootScope: append([]PhysicalOperation(nil), plan.Operations[1:5]...),
		returnOp:  *plan.Operations[last].Return,
	}
	rootScopeVariable, err := validateGenericNavigationScopeBlock(layout.rootScope, layout.root.Variable, "", layout.root.Variable)
	if err != nil {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root navigation scope: %w", err)
	}

	index := 5
	for index < last && plan.Operations[index].Kind == PhysicalFilterOp && plan.Operations[index].Filter.Expression != nil {
		layout.rootPredicates = append(layout.rootPredicates, plan.Operations[index])
		index++
	}
	if index < last && plan.Operations[index].Kind == PhysicalSortOp {
		if err := validateGenericNavigationRootSort(plan.Operations[index], layout.root.Variable); err != nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: %w", index, err)
		}
		layout.rootWindow = append(layout.rootWindow, plan.Operations[index])
		index++
		if index < last && plan.Operations[index].Kind == PhysicalLimitOp {
			if err := validateGenericNavigationRootLimit(plan.Operations[index]); err != nil {
				return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: %w", index, err)
			}
			layout.rootWindow = append(layout.rootWindow, plan.Operations[index])
			index++
		}
	} else if index < last && plan.Operations[index].Kind == PhysicalLimitOp {
		return physicalNavigationRenderLayout{}, fmt.Errorf("root execution window at operation %d: LIMIT requires deterministic root SORT", index)
	}
	for index < last {
		operation := plan.Operations[index]
		if operation.Kind == PhysicalSetOp {
			layout.sets = append(layout.sets, *operation.Set)
			index++
			continue
		}
		if operation.Kind != PhysicalTraversalOp {
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
		scope := append([]PhysicalOperation(nil), plan.Operations[index+1:index+1+traversalScopeLength]...)
		if _, err := validateGenericNavigationScopeBlock(scope, traversal.TargetVariable, traversal.EdgeVariable, traversal.TargetVariable); err != nil {
			return physicalNavigationRenderLayout{}, fmt.Errorf("traversal at operation %d scope: %w", index, err)
		}
		layout.traversals = append(layout.traversals, physicalNavigationTraversal{traversal: traversal, scope: scope})
		index += 1 + traversalScopeLength
	}
	if err := validateNavigationReturnScope(layout.returnOp, layout.root.Variable, rootScopeVariable); err != nil {
		return physicalNavigationRenderLayout{}, err
	}
	return layout, nil
}

func validateGenericNavigationTraversal(plan PhysicalPlan, traversal PhysicalTraversal) error {
	if traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalOutbound {
		return fmt.Errorf("generic navigation traversal direction must be INBOUND or OUTBOUND, got %q", traversal.Direction)
	}
	wantEdgeTypeField := "from_type"
	if traversal.Direction == PhysicalOutbound {
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
		strategy = PhysicalTraversalNative
	}
	if strategy != PhysicalTraversalNative && strategy != PhysicalTraversalEndpointLookup {
		return fmt.Errorf("unsupported generic navigation traversal strategy %q", strategy)
	}
	if strategy == PhysicalTraversalEndpointLookup {
		wantEndpoint, wantJoin := "_to", "_from"
		wantIndexType := "from_type"
		if traversal.Direction == PhysicalOutbound {
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

func validateGenericNavigationRootSort(operation PhysicalOperation, rootVariable string) error {
	if operation.Sort == nil || !sameRenderPhysicalValue(operation.Sort.Value, PhysicalValue{Variable: rootVariable, Path: []string{"_key"}}) {
		return fmt.Errorf("SORT must order the root variable %s._key", rootVariable)
	}
	return nil
}

func validateGenericNavigationRootLimit(operation PhysicalOperation) error {
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
func validateGenericNavigationScopeBlock(operations []PhysicalOperation, resourceVariable, edgeVariable, targetVariable string) (string, error) {
	expectedProjectVariables := []string{resourceVariable}
	expectedGenerationVariables := []string{resourceVariable}
	if edgeVariable != "" {
		expectedProjectVariables = []string{edgeVariable, targetVariable}
		expectedGenerationVariables = []string{edgeVariable, targetVariable}
	}
	expectedLength := len(expectedProjectVariables) + len(expectedGenerationVariables) + 2
	if len(operations) != expectedLength || operations[0].Kind != PhysicalFilterOp {
		return "", fmt.Errorf("requires project filters for every graph document, dataset_generation filters, LET AUTH_RESOURCE_PATH_ALLOWED, FILTER scope_allowed in order")
	}
	for index, variable := range expectedProjectVariables {
		operation := operations[index]
		if operation.Kind != PhysicalFilterOp || !matchesPhysicalEquality(operation.Filter.Predicate, PhysicalValue{Variable: variable, Path: []string{"project"}}, PhysicalValue{BindKey: "project"}) {
			return "", fmt.Errorf("project scope must be %s.project == @project", variable)
		}
	}
	for index, variable := range expectedGenerationVariables {
		operation := operations[len(expectedProjectVariables)+index]
		if operation.Kind != PhysicalFilterOp || !matchesPhysicalEquality(operation.Filter.Predicate, PhysicalValue{Variable: variable, Path: []string{datasetGenerationField}}, PhysicalValue{BindKey: datasetGenerationBindKey}) {
			return "", fmt.Errorf("dataset generation scope must be %s.%s == @%s", variable, datasetGenerationField, datasetGenerationBindKey)
		}
	}
	authLetIndex := len(expectedProjectVariables) + len(expectedGenerationVariables)
	authFilterIndex := authLetIndex + 1

	if operations[authLetIndex].Kind != PhysicalDerivedLetOp || operations[authLetIndex].DerivedLet == nil {
		return "", fmt.Errorf("scope block requires AUTH_RESOURCE_PATH_ALLOWED LET after dataset generation scope")
	}
	derived := operations[authLetIndex].DerivedLet
	if strings.ToUpper(strings.TrimSpace(derived.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
		return "", fmt.Errorf("scope LET must use AUTH_RESOURCE_PATH_ALLOWED")
	}
	expectedInputs := []PhysicalValue{{Variable: resourceVariable, Path: []string{"auth_resource_path"}}}
	if edgeVariable != "" {
		expectedInputs = []PhysicalValue{
			{Variable: edgeVariable, Path: []string{"auth_resource_path"}},
			{Variable: targetVariable, Path: []string{"auth_resource_path"}},
		}
	}
	expectedInputs = append(expectedInputs, PhysicalValue{BindKey: "auth_resource_paths"}, PhysicalValue{BindKey: "auth_resource_paths_unrestricted"})
	if len(derived.Inputs) != len(expectedInputs) {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires the exact generic auth scope inputs")
	}
	for index := range expectedInputs {
		if !sameRenderPhysicalValue(derived.Inputs[index], expectedInputs[index]) {
			return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED input %d is not the required generic scope value", index)
		}
	}
	if operations[authFilterIndex].Kind != PhysicalFilterOp || !matchesPhysicalEquality(operations[authFilterIndex].Filter.Predicate, PhysicalValue{Variable: derived.Variable}, PhysicalValue{BindKey: "scope_allowed"}) {
		return "", fmt.Errorf("auth scope must be %s == @scope_allowed", derived.Variable)
	}
	return derived.Variable, nil
}

func matchesPhysicalEquality(predicate PhysicalPredicate, left, right PhysicalValue) bool {
	return strings.ToUpper(strings.TrimSpace(predicate.Operator)) == "EQUALS" &&
		predicate.Right != nil &&
		sameRenderPhysicalValue(predicate.Left, left) &&
		sameRenderPhysicalValue(*predicate.Right, right)
}

func sameRenderPhysicalValue(left, right PhysicalValue) bool {
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

func validateNavigationReturnScope(returnOp PhysicalReturn, rootVariable, rootScopeVariable string) error {
	for _, projection := range returnOp.Projections {
		if projection.Expression != nil {
			continue
		}
		if projection.Value.BindKey != "" {
			continue
		}
		if projection.Value.Variable != rootVariable && projection.Value.Variable != rootScopeVariable {
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
	strategy := traversal.Strategy
	if strategy == "" {
		strategy = PhysicalTraversalNative
	}
	if strategy == PhysicalTraversalEndpointLookup {
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

func (r *physicalPlanRenderer) renderSet(set PhysicalSet, index int) ([]string, error) {
	if set.SourceSetVariable != "" {
		return r.renderSharedSubset(set)
	}
	if len(set.Subplan.Operations) == 0 {
		return nil, fmt.Errorf("set %q has no subplan operations", set.Variable)
	}
	first := set.Subplan.Operations[0]
	if first.Kind != PhysicalTraversalOp || first.Traversal == nil {
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
		strategy = PhysicalTraversalNative
	}
	if strategy == PhysicalTraversalEndpointLookup {
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
	if set.Prepared != nil {
		prepared, err := r.renderPreparedSet(*set.Prepared)
		if err != nil {
			return nil, err
		}
		lines = append(lines, prepared...)
		r.setVariables[set.Prepared.Variable] = set.Prepared.Variable
	}
	_ = index
	return lines, nil
}

func renderPhysicalSetOutput(value string, output *PhysicalSetOutput) (string, error) {
	if output == nil {
		return value, nil
	}
	fields := make([]string, 0, len(output.Fields))
	for _, field := range output.Fields {
		name := string(field)
		switch field {
		case PhysicalSetGraphIDField, PhysicalSetKeyField, PhysicalSetIDField, PhysicalSetResourceTypeField, PhysicalSetPayloadField:
			fields = append(fields, fmt.Sprintf("%s: %s.%s", name, value, name))
		default:
			return "", fmt.Errorf("unsupported compact set output field %q", field)
		}
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderPhysicalSetProjection(item string, projection PhysicalSetProjection) (string, error) {
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
		values, err := r.renderSelectorByMode(item+".payload", field.Selector, field.ExecutionMode)
		if err != nil {
			return "", fmt.Errorf("projected field %q: %w", field.Name, err)
		}
		fields = append(fields, field.Name+": "+values)
	}
	return "{ " + strings.Join(fields, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderPreparedSet(prepared PhysicalPreparedSet) ([]string, error) {
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
		values, err := r.renderSelectorArrayFromSource(item+".payload", field.Selector, false)
		if err != nil {
			return nil, fmt.Errorf("prepared field %q: %w", field.Name, err)
		}
		fields = append(fields, field.Name+": "+values)
	}
	lines = append(lines, "      RETURN { "+strings.Join(fields, ", ")+" }", "    )")
	return lines, nil
}

func (r *physicalPlanRenderer) renderTraversalTypeFilters(t *PhysicalTraversal, indent string) []string {
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

func (r *physicalPlanRenderer) renderSharedSubset(set PhysicalSet) ([]string, error) {
	if r.setVariables[set.SourceSetVariable] == "" {
		return nil, fmt.Errorf("shared subset source %q has not been rendered", set.SourceSetVariable)
	}
	lines := []string{fmt.Sprintf("  LET %s = (", set.Variable), fmt.Sprintf("    FOR %s IN %s", set.ItemVariable, set.SourceSetVariable)}
	for index, operation := range set.Subplan.Operations {
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

func physicalPlanVariableNames(plan PhysicalPlan) map[string]struct{} {
	variables := map[string]struct{}{}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case PhysicalRootScanOp:
			variables[operation.RootScan.Variable] = struct{}{}
		case PhysicalTraversalOp:
			variables[operation.Traversal.SourceVariable] = struct{}{}
			variables[operation.Traversal.TargetVariable] = struct{}{}
			if operation.Traversal.EdgeVariable != "" {
				variables[operation.Traversal.EdgeVariable] = struct{}{}
			}
		case PhysicalDerivedLetOp:
			variables[operation.DerivedLet.Variable] = struct{}{}
		case PhysicalSetOp:
			variables[operation.Set.Variable] = struct{}{}
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

func (r *physicalPlanRenderer) renderPredicate(predicate PhysicalPredicate) (string, error) {
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

func (r *physicalPlanRenderer) renderSelectorPredicate(predicate PhysicalPredicate) (string, error) {
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
		if predicate.ValueKind == FilterDate || predicate.ValueKind == FilterDateTime {
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
		quantifier = QuantifierAny
	}
	switch quantifier {
	case QuantifierAny:
		return matching + " > 0", nil
	case QuantifierNone:
		return matching + " == 0", nil
	case QuantifierAll:
		return "LENGTH(" + values + ") > 0 AND LENGTH(FOR " + valueVar + " IN " + values + " FILTER NOT (" + match + ") LIMIT 1 RETURN 1) == 0", nil
	default:
		return "", fmt.Errorf("unsupported physical selector filter quantifier %q", quantifier)
	}
}

func (r *physicalPlanRenderer) renderPredicateExpression(predicate PhysicalPredicateExpression, indent string) (string, error) {
	switch predicate.Kind {
	case PhysicalComparisonPredicate:
		return r.renderPredicate(*predicate.Comparison)
	case PhysicalAllPredicate, PhysicalAnyPredicate:
		parts := make([]string, 0, len(predicate.Children))
		for _, child := range predicate.Children {
			part, err := r.renderPredicateExpression(child, indent)
			if err != nil {
				return "", err
			}
			parts = append(parts, "("+part+")")
		}
		join := " AND "
		if predicate.Kind == PhysicalAnyPredicate {
			join = " OR "
		}
		return strings.Join(parts, join), nil
	case PhysicalNotPredicate:
		child, err := r.renderPredicateExpression(predicate.Children[0], indent)
		if err != nil {
			return "", err
		}
		return "NOT (" + child + ")", nil
	case PhysicalExistsPredicate:
		return r.renderExistsSubplan(*predicate.Exists, indent)
	default:
		return "", fmt.Errorf("unsupported physical predicate kind %q", predicate.Kind)
	}
}

// renderExistsSubplan serializes a validated correlated subplan. EXISTS is
// always bounded: relationship matching is a semi-join, never a row-expanding
// traversal, so the renderer appends LIMIT 1 immediately before RETURN.
func (r *physicalPlanRenderer) renderExistsSubplan(subplan PhysicalSubplan, indent string) (string, error) {
	lines := make([]string, 0, len(subplan.Operations)*3+2)
	for index, operation := range subplan.Operations {
		switch operation.Kind {
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			lines = append(lines,
				fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", indent+"  ", traversal.TargetVariable, traversal.EdgeVariable, traversal.Direction, traversal.SourceVariable, traversal.EdgeCollectionBindKey),
				fmt.Sprintf("%s  FILTER %s.label == @%s", indent+"  ", traversal.EdgeVariable, traversal.EdgeLabelBindKey),
				fmt.Sprintf("%s  FILTER %s.%s == @%s", indent+"  ", traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
				fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent+"  ", traversal.TargetVariable, traversal.TargetTypeBindKey),
			)
		case PhysicalFilterOp, PhysicalDerivedLetOp:
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

func (r *physicalPlanRenderer) renderExpression(expression PhysicalExpression) (string, error) {
	switch expression.Kind {
	case PhysicalValueExpression:
		return r.renderValue(*expression.Value)
	case PhysicalExtractExpression:
		return r.renderExtract(expression)
	case PhysicalAggregateExpression:
		return r.renderAggregate(expression)
	case PhysicalPivotExpression:
		return r.renderPivot(expression)
	case PhysicalSliceExpression:
		return r.renderSlice(expression)
	case PhysicalObjectExpression:
		return r.renderObject(expression)
	default:
		return "", fmt.Errorf("physical renderer does not yet support expression kind %q", expression.Kind)
	}
}

// renderObject renders a recursively typed object expression. Sorting a copy
// gives equivalent physical plans stable AQL and bind-key allocation without
// changing the semantic field order held by the plan.
//
// The compact dynamic-key literal is used when every field preserves nulls.
// If any field requests OMIT_NULLS, fields are represented as a temporary
// stream and merged so null-valued fields can be removed without evaluating
// their expression twice.
func (r *physicalPlanRenderer) renderObject(expression PhysicalExpression) (string, error) {
	object := expression.Object
	if object == nil {
		return "", fmt.Errorf("OBJECT expression is missing payload")
	}
	fields := append([]PhysicalExpressionProjection(nil), object.Fields...)
	sort.SliceStable(fields, func(left, right int) bool {
		return fields[left].Name < fields[right].Name
	})

	type renderedField struct {
		nameKey string
		value   string
		omit    bool
	}
	rendered := make([]renderedField, 0, len(fields))
	for index, field := range fields {
		value, err := r.renderExpression(field.Expression)
		if err != nil {
			return "", fmt.Errorf("object field %q: %w", field.Name, err)
		}
		nameKey := r.newInternalBindKey("object_field_" + strconv.Itoa(index) + "_name")
		r.bindVars[nameKey] = field.Name
		rendered = append(rendered, renderedField{
			nameKey: nameKey,
			value:   value,
			omit:    field.Expression.NullBehavior == PhysicalOmitNulls,
		})
	}

	hasOmittedField := false
	for _, field := range rendered {
		if field.omit {
			hasOmittedField = true
			break
		}
	}
	if !hasOmittedField {
		parts := make([]string, 0, len(rendered))
		for _, field := range rendered {
			parts = append(parts, fmt.Sprintf("[@%s]: %s", field.nameKey, field.value))
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}

	items := make([]string, 0, len(rendered))
	for _, field := range rendered {
		items = append(items, fmt.Sprintf("{ __loom_object_name: @%s, __loom_object_value: %s, __loom_object_omit: %t }", field.nameKey, field.value, field.omit))
	}
	return fmt.Sprintf(`MERGE(
  FOR __loom_object_field IN [%s]
    FILTER __loom_object_field.__loom_object_omit == false OR __loom_object_field.__loom_object_value != null
    RETURN { [__loom_object_field.__loom_object_name]: __loom_object_field.__loom_object_value }
)`, strings.Join(items, ", ")), nil
}

// renderSlice emits a correlated, bounded array projection. Sort and the
// _key tie-break are rendered inside the subquery so representative values
// are deterministic even when traversal order changes.
func (r *physicalPlanRenderer) renderSlice(expression PhysicalExpression) (string, error) {
	slice := expression.Slice
	if slice == nil {
		return "", fmt.Errorf("SLICE expression is missing payload")
	}
	source, err := r.renderValue(slice.Source)
	if err != nil {
		return "", err
	}
	items := source
	preparedVariable := slicePreparedVariable(slice)
	if preparedVariable != "" {
		items = preparedVariable
	}
	setSource := slice.Source.Variable != "" && r.setVariables[slice.Source.Variable] != ""
	if !setSource {
		items = "[" + source + "]"
	}
	item := r.newInternalVariable("slice_item")
	lines := []string{"(FOR " + item + " IN " + items}
	if slice.Predicate != nil {
		if slice.Predicate.Kind != PhysicalComparisonPredicate || slice.Predicate.Comparison == nil {
			return "", fmt.Errorf("slice predicate must be a comparison")
		}
		comparison := *slice.Predicate.Comparison
		if comparison.LeftExpression != nil && comparison.LeftExpression.Extract != nil {
			left := *comparison.LeftExpression
			extract := *left.Extract
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
			left.Extract = &extract
			comparison.LeftExpression = &left
		} else {
			comparison.Left = PhysicalValue{Variable: item}
		}
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		predicate, err := r.renderPredicate(comparison)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", err
		}
		lines = append(lines, "  FILTER "+predicate)
	}
	if slice.Sort == nil {
		return "", fmt.Errorf("slice requires sort expression")
	}
	sortExpression := *slice.Sort
	if sortExpression.Kind == PhysicalValueExpression && sortExpression.Value != nil {
		value := *sortExpression.Value
		value.Variable = item
		value.BindKey = ""
		sortExpression.Value = &value
	}
	sortValue, err := r.renderExpression(sortExpression)
	if err != nil {
		return "", err
	}
	lines = append(lines, "  SORT "+sortValue+" ASC, "+item+"._key ASC")
	lines = append(lines, "  LIMIT @"+slice.LimitBindKey)
	fields := make([]string, 0, len(slice.Projections))
	for index, projection := range slice.Projections {
		projectionExpression := projection.Expression
		if projectionExpression.Kind == PhysicalExtractExpression && projectionExpression.Extract != nil {
			extract := *projectionExpression.Extract
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
			projectionExpression.Extract = &extract
		}
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		value, err := r.renderExpression(projectionExpression)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", fmt.Errorf("slice projection %d (%s): %w", index, projection.Name, err)
		}
		nameKey := r.newInternalBindKey("slice_projection_" + strconv.Itoa(index) + "_name")
		r.bindVars[nameKey] = projection.Name
		fields = append(fields, "["+"@"+nameKey+"]: "+value)
	}
	lines = append(lines, "  RETURN { "+strings.Join(fields, ", ")+" }")
	return strings.Join(lines, "\n") + "\n)", nil
}

func slicePreparedVariable(slice *PhysicalSlice) string {
	if slice == nil {
		return ""
	}
	if slice.Predicate != nil && slice.Predicate.Comparison != nil && slice.Predicate.Comparison.LeftExpression != nil && slice.Predicate.Comparison.LeftExpression.Extract != nil && slice.Predicate.Comparison.LeftExpression.Extract.Prepared != nil {
		return slice.Predicate.Comparison.LeftExpression.Extract.Prepared.SetVariable
	}
	for _, projection := range slice.Projections {
		if projection.Expression.Extract != nil && projection.Expression.Extract.Prepared != nil {
			return projection.Expression.Extract.Prepared.SetVariable
		}
	}
	return ""
}

// renderPivot emits a bounded sparse object keyed by the requested catalog
// columns. Values from all matching resources are combined per key and reduced
// deterministically to the first sorted value while keeping selectors and
// column values typed.
func (r *physicalPlanRenderer) renderPivot(expression PhysicalExpression) (string, error) {
	pivot := expression.Pivot
	if pivot == nil {
		return "", fmt.Errorf("PIVOT expression is missing payload")
	}
	if _, collection := r.collectionKeys[pivot.ColumnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", pivot.ColumnsBindKey)
	}
	columns, ok := r.bindVars[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", pivot.ColumnsBindKey)
	}
	items, err := r.renderValue(pivot.Source)
	if err != nil {
		return "", err
	}
	if pivot.Source.Variable == "" || r.setVariables[pivot.Source.Variable] == "" {
		items = "[" + items + "]"
	}
	if pivot.PreparedKey != nil {
		items = pivot.PreparedKey.SetVariable
	}
	item := r.newInternalVariable("pivot_item")
	previousPreparedItem := r.preparedItem
	r.preparedItem = item
	keyExpr, err := r.renderPreparedOrSelector(item, pivot.PreparedKey, pivot.KeySelector)
	if err != nil {
		r.preparedItem = previousPreparedItem
		return "", err
	}
	valueExpr, err := r.renderPreparedOrSelector(item, pivot.PreparedValue, pivot.ValueSelector)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`MERGE(
  FOR __pair IN (
    FOR %s IN %s
      LET __pivot_keys = UNIQUE(%s)
      LET __pivot_values = %s
      FILTER LENGTH(__pivot_values) > 0
      FOR __pivot_key IN __pivot_keys
        FILTER POSITION(@%s, __pivot_key)
        RETURN { key: __pivot_key, values: __pivot_values }
  )
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN { [__pivot_key]: FIRST(__pivot_flat_values) }
)`, item, items, keyExpr, valueExpr, pivot.ColumnsBindKey), nil
}

// renderAggregate emits reductions over either a correlated PhysicalSet or a
// singleton root document. The source is kept typed in the IR; this method is
// the only place that decides the AQL collection expression (`set` versus
// `[root]`).
func (r *physicalPlanRenderer) renderAggregate(expression PhysicalExpression) (string, error) {
	aggregate := expression.Aggregate
	if aggregate == nil {
		return "", fmt.Errorf("AGGREGATE expression is missing payload")
	}
	source, err := r.renderValue(aggregate.Source)
	if err != nil {
		return "", err
	}
	items := source
	if preparedVariable := aggregatePreparedVariable(aggregate); preparedVariable != "" {
		items = preparedVariable
	}
	if aggregate.Source.Variable == "" || r.setVariables[aggregate.Source.Variable] == "" {
		items = "[" + source + "]"
	}
	perItem := aggregate.Predicate != nil
	if perItem {
		if aggregate.Predicate.Kind != PhysicalComparisonPredicate || aggregate.Predicate.Comparison == nil {
			return "", fmt.Errorf("aggregate predicate must be a comparison")
		}
		item := r.newInternalVariable("aggregate_item")
		comparison := *aggregate.Predicate.Comparison
		if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
			return "", fmt.Errorf("aggregate predicate must extract a selector")
		}
		left := *comparison.LeftExpression
		extract := *left.Extract
		if extract.Prepared == nil {
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
		}
		left.Extract = &extract
		comparison.LeftExpression = &left
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		predicate, err := r.renderPredicate(comparison)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", err
		}
		items = "(FOR " + item + " IN " + items + " FILTER " + predicate + " RETURN " + item + ")"
	}
	switch aggregate.Operation {
	case PhysicalCountAggregate:
		return "LENGTH(" + items + ")", nil
	case PhysicalExistsAggregate:
		if aggregate.Value == nil {
			return "LENGTH(" + items + ") > 0", nil
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		return "LENGTH(FOR __value IN FLATTEN(" + values + ") FILTER __value != null LIMIT 1 RETURN 1) > 0", nil
	case PhysicalCountDistinctAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
		if aggregate.Value == nil {
			return "", fmt.Errorf("aggregate operation %q requires a value expression", aggregate.Operation)
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		flattened := "FLATTEN(" + values + ")"
		switch aggregate.Operation {
		case PhysicalCountDistinctAggregate:
			return "LENGTH(SORTED_UNIQUE(" + flattened + "))", nil
		case PhysicalDistinctValuesAggregate:
			return "SORTED_UNIQUE(" + flattened + ")", nil
		case PhysicalMinAggregate:
			return "MIN(" + flattened + ")", nil
		case PhysicalMaxAggregate:
			return "MAX(" + flattened + ")", nil
		case PhysicalFirstAggregate:
			return "FIRST(" + flattened + ")", nil
		}
	}
	return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
}

func aggregatePreparedVariable(aggregate *PhysicalAggregate) string {
	if aggregate == nil {
		return ""
	}
	if aggregate.Value != nil && aggregate.Value.Extract != nil && aggregate.Value.Extract.Prepared != nil {
		return aggregate.Value.Extract.Prepared.SetVariable
	}
	if aggregate.Predicate != nil && aggregate.Predicate.Comparison != nil && aggregate.Predicate.Comparison.LeftExpression != nil && aggregate.Predicate.Comparison.LeftExpression.Extract != nil && aggregate.Predicate.Comparison.LeftExpression.Extract.Prepared != nil {
		return aggregate.Predicate.Comparison.LeftExpression.Extract.Prepared.SetVariable
	}
	return ""
}

func (r *physicalPlanRenderer) renderAggregateValue(expression PhysicalExpression, items string, perItem bool) (string, error) {
	if !perItem {
		if expression.Extract != nil && expression.Extract.Prepared != nil {
			return "(FOR __loom_prepared_value IN " + expression.Extract.Prepared.SetVariable + " RETURN __loom_prepared_value." + expression.Extract.Prepared.Field + ")", nil
		}
		return r.renderExpression(expression)
	}
	if expression.Kind != PhysicalExtractExpression || expression.Extract == nil {
		return "", fmt.Errorf("aggregate predicates require an extract value expression")
	}
	item := r.newInternalVariable("aggregate_value_item")
	clone := expression
	extract := *expression.Extract
	if extract.Prepared == nil {
		extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
	}
	clone.Extract = &extract
	previousPreparedItem := r.preparedItem
	r.preparedItem = item
	value, err := r.renderExtract(clone)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", err
	}
	return "(FOR " + item + " IN " + items + " RETURN " + value + ")", nil
}

func (r *physicalPlanRenderer) renderPreparedOrSelector(item string, reference *PhysicalPreparedReference, selector Selector) (string, error) {
	if reference != nil {
		return item + "." + reference.Field, nil
	}
	return r.renderSelectorArrayFromSource(item+".payload", selector, false)
}

func (r *physicalPlanRenderer) renderExtract(expression PhysicalExpression) (string, error) {
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("EXTRACT expression is missing payload")
	}
	if extract.Prepared != nil {
		value := ""
		if r.preparedItem != "" {
			value = r.preparedItem + "." + extract.Prepared.Field
		} else {
			value = "(FOR __loom_prepared_value IN " + extract.Prepared.SetVariable + " RETURN __loom_prepared_value." + extract.Prepared.Field + ")"
		}
		if expression.Cardinality == PhysicalArrayCardinality {
			if extract.Distinct {
				return "SORTED_UNIQUE(FLATTEN(" + value + "))", nil
			}
			return value, nil
		}
		return "FIRST(FLATTEN(" + value + "))", nil
	}
	source, err := r.renderValue(extract.Source)
	if err != nil {
		return "", err
	}
	if len(extract.Fallbacks) == 0 && extract.Selector.Filter == nil {
		switch extract.ExecutionMode {
		case PhysicalSelectorDirectScalar:
			if expression.Cardinality != PhysicalArrayCardinality {
				return compileDirectExpr(source, extract.Selector.Steps), nil
			}
		case PhysicalSelectorConditionalArray:
			values, err := r.renderConditionalSelectorArray(source, extract.Selector)
			if err != nil {
				return "", err
			}
			if expression.Cardinality == PhysicalArrayCardinality {
				if extract.Distinct {
					return "SORTED_UNIQUE(" + values + ")", nil
				}
				return values, nil
			}
			return "FIRST(" + values + ")", nil
		}
	}
	arrays := make([]string, 0, 1+len(extract.Fallbacks))
	setSource := extract.Source.Variable != "" && r.setVariables[extract.Source.Variable] != ""
	for _, selector := range append([]Selector{extract.Selector}, extract.Fallbacks...) {
		array, err := r.renderSelectorArrayFromSource(source, selector, setSource)
		if err != nil {
			return "", err
		}
		arrays = append(arrays, array)
	}
	values := arrays[0]
	if len(arrays) > 1 {
		values = "FLATTEN([" + strings.Join(arrays, ", ") + "])"
	}
	if expression.Cardinality == PhysicalArrayCardinality {
		if extract.Distinct {
			return "SORTED_UNIQUE(" + values + ")", nil
		}
		return values, nil
	}
	if !setSource && len(arrays) == 1 && extract.Selector.Filter == nil && selectorHasNoArrays(extract.Selector) {
		return compileDirectExpr(source, extract.Selector.Steps), nil
	}
	return "FIRST(" + values + ")", nil
}

func (r *physicalPlanRenderer) renderSelectorByMode(source string, selector Selector, mode PhysicalSelectorExecutionMode) (string, error) {
	if mode == PhysicalSelectorDirectScalar && selectorHasNoArrays(selector) && selector.Filter == nil {
		return "(FOR __loom_value IN [" + compileDirectExpr(source, selector.Steps) + "] FILTER __loom_value != null RETURN __loom_value)", nil
	}
	if mode == PhysicalSelectorConditionalArray && selectorHasIteratedArray(selector) && selector.Filter == nil {
		return r.renderConditionalSelectorArray(source, selector)
	}
	return r.renderSelectorArrayFromSource(source, selector, false)
}

func (r *physicalPlanRenderer) renderConditionalSelectorArray(source string, selector Selector) (string, error) {
	if len(selector.Steps) == 0 {
		return "", fmt.Errorf("selector is required")
	}
	prefix, last := selector.Steps[:len(selector.Steps)-1], selector.Steps[len(selector.Steps)-1]
	lines := make([]string, 0, len(prefix)+3)
	current := source
	for index, step := range prefix {
		next := fmt.Sprintf("__loom_selector_%d", index)
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index), "FILTER "+next+" != null")
		default:
			lines = append(lines, fmt.Sprintf("LET %s = %s.%s", next, current, step.Field), "FILTER "+next+" != null")
		}
		current = next
	}
	lines = append(lines, "LET __value = "+extractFinalExpr(current, last), "FILTER __value != null", "RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )", nil
}

func (r *physicalPlanRenderer) renderSelectorArrayFromSource(source string, selector Selector, setSource bool) (string, error) {
	if len(selector.Steps) == 0 {
		return "", fmt.Errorf("selector is required")
	}
	prefix, last := selector.Steps[:len(selector.Steps)-1], selector.Steps[len(selector.Steps)-1]
	lines, current := []string{"FOR __root IN [" + source + "]"}, "__root"
	if setSource {
		lines, current = []string{"FOR __item IN " + source, "  FOR __root IN [__item.payload]"}, "__root"
	}
	for index, step := range prefix {
		next := fmt.Sprintf("__s%d", index)
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index), "  FILTER "+next+" != null")
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, current, step.Field), "  FILTER "+next+" != null")
		}
		current = next
	}
	if selector.Filter != nil {
		key := r.newInternalBindKey("selector_contains")
		r.bindVars[key] = selector.Filter.Needle
		lines = append(lines, fmt.Sprintf("  FILTER CONTAINS(%s.%s ? %s.%s : \"\", @%s)", current, selector.Filter.Field, current, selector.Filter.Field, key))
	}
	lines = append(lines, "  LET __value = "+extractFinalExpr(current, last), "  FILTER __value != null", "  RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )", nil
}

func (r *physicalPlanRenderer) renderDerivedLet(derived PhysicalDerivedLet) (string, error) {
	if strings.ToUpper(strings.TrimSpace(derived.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
		return "", fmt.Errorf("unsupported physical derived LET operator %q", derived.Operator)
	}
	if len(derived.Inputs) < 3 {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires one or more scope values plus paths and unrestricted inputs")
	}

	paths := derived.Inputs[len(derived.Inputs)-2]
	unrestricted := derived.Inputs[len(derived.Inputs)-1]
	if paths.BindKey == "" || paths.Variable != "" || unrestricted.BindKey == "" || unrestricted.Variable != "" {
		return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED requires paths and unrestricted bind inputs")
	}
	pathsExpression, err := r.renderValue(paths)
	if err != nil {
		return "", err
	}
	unrestrictedExpression, err := r.renderValue(unrestricted)
	if err != nil {
		return "", err
	}

	scopeChecks := make([]string, 0, len(derived.Inputs)-2)
	for _, input := range derived.Inputs[:len(derived.Inputs)-2] {
		if input.Variable == "" || input.BindKey != "" {
			return "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED scope inputs must be variable paths")
		}
		scopeValue, err := r.renderValue(input)
		if err != nil {
			return "", err
		}
		scopeChecks = append(scopeChecks, scopeValue+" IN "+pathsExpression)
	}

	scopeExpression := strings.Join(scopeChecks, " AND ")
	if len(scopeChecks) > 1 {
		scopeExpression = "(" + scopeExpression + ")"
	}
	return unrestrictedExpression + " == true OR " + scopeExpression, nil
}

func (r *physicalPlanRenderer) renderReturn(returnOp PhysicalReturn) (string, error) {
	if len(returnOp.Projections) == 0 {
		return "{}", nil
	}
	projections := make([]string, 0, len(returnOp.Projections))
	for index, projection := range returnOp.Projections {
		nameBindKey := r.newInternalBindKey(fmt.Sprintf("projection_%d_name", index))
		r.bindVars[nameBindKey] = projection.Name
		var value string
		var err error
		if projection.Expression != nil {
			value, err = r.renderExpression(*projection.Expression)
		} else {
			value, err = r.renderValue(projection.Value)
		}
		if err != nil {
			return "", err
		}
		projections = append(projections, fmt.Sprintf("[@%s]: %s", nameBindKey, value))
	}
	return "{ " + strings.Join(projections, ", ") + " }", nil
}

func (r *physicalPlanRenderer) renderValue(value PhysicalValue) (string, error) {
	if value.BindKey != "" {
		if _, collectionBinding := r.collectionKeys[value.BindKey]; collectionBinding {
			return "", fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", value.BindKey)
		}
		return "@" + value.BindKey, nil
	}
	if value.Variable == "" {
		return "", fmt.Errorf("physical value has no variable or bind key")
	}
	if len(value.Path) == 0 {
		return value.Variable, nil
	}
	return value.Variable + "." + strings.Join(value.Path, "."), nil
}

func (r *physicalPlanRenderer) newInternalBindKey(suffix string) string {
	base := "__loom_physical_" + suffix
	key := base
	for counter := 1; ; counter++ {
		if _, exists := r.bindVars[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, counter)
	}
}

func collectionBindKeys(plan PhysicalPlan) (map[string]struct{}, error) {
	keys := map[string]struct{}{}
	var collectOperations func([]PhysicalOperation, string) error
	collectOperations = func(operations []PhysicalOperation, owner string) error {
		for index, operation := range operations {
			switch operation.Kind {
			case PhysicalRootScanOp:
				keys[operation.RootScan.CollectionBindKey] = struct{}{}
			case PhysicalTraversalOp:
				if operation.Traversal.EdgeCollectionBindKey == "" {
					return fmt.Errorf("%s operation %d (TRAVERSAL): edge collection bind key is required", owner, index)
				}
				keys[operation.Traversal.EdgeCollectionBindKey] = struct{}{}
			case PhysicalFilterOp:
				if operation.Filter.Expression != nil {
					if err := collectPredicateCollections(*operation.Filter.Expression, collectOperations, owner); err != nil {
						return err
					}
				}
			case PhysicalSetOp:
				if err := collectOperations(operation.Set.Subplan.Operations, owner+" SET"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collectOperations(plan.Operations, "render"); err != nil {
		return nil, err
	}
	for key := range keys {
		value, ok := plan.BindVars[key]
		if !ok {
			return nil, fmt.Errorf("collection bind key %q is not defined", key)
		}
		collection, ok := value.(string)
		if !ok || strings.TrimSpace(collection) == "" {
			return nil, fmt.Errorf("collection bind key %q must have a non-empty string value", key)
		}
	}
	return keys, nil
}

func collectPredicateCollections(predicate PhysicalPredicateExpression, collectOperations func([]PhysicalOperation, string) error, owner string) error {
	if predicate.Exists != nil {
		return collectOperations(predicate.Exists.Operations, owner+" EXISTS")
	}
	for _, child := range predicate.Children {
		if err := collectPredicateCollections(child, collectOperations, owner); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderablePhysicalPlan(plan PhysicalPlan, collectionKeys map[string]struct{}) error {
	for index, operation := range plan.Operations {
		if err := validateRenderableOperation(operation, collectionKeys); err != nil {
			return fmt.Errorf("render operation %d (%s): %w", index, operation.Kind, err)
		}
	}
	return nil
}

func validateRenderableOperation(operation PhysicalOperation, collectionKeys map[string]struct{}) error {
	valueIsCollection := func(value PhysicalValue) error {
		if _, isCollection := collectionKeys[value.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", value.BindKey)
		}
		return nil
	}
	checkValue := func(value PhysicalValue) error {
		if err := valueIsCollection(value); err != nil {
			return err
		}
		return nil
	}

	switch operation.Kind {
	case PhysicalRootScanOp:
		return nil
	case PhysicalTraversalOp:
		traversal := operation.Traversal
		if traversal.EdgeVariable == "" {
			return fmt.Errorf("TRAVERSAL requires an edge variable for edge-label and project scope checks")
		}
		if traversal.EdgeLabelBindKey == "" || traversal.TargetTypeBindKey == "" {
			return fmt.Errorf("TRAVERSAL requires edge label and target resource type bind keys")
		}
		return nil
	case PhysicalSetOp:
		for index, suboperation := range operation.Set.Subplan.Operations {
			if err := validateRenderableOperation(suboperation, collectionKeys); err != nil {
				return fmt.Errorf("SET subplan operation %d: %w", index, err)
			}
		}
		return nil
	case PhysicalFilterOp:
		if operation.Filter.Expression != nil {
			return validateRenderablePredicateExpression(*operation.Filter.Expression, collectionKeys)
		}
		if strings.ToUpper(strings.TrimSpace(operation.Filter.Predicate.Operator)) != "EQUALS" {
			return fmt.Errorf("unsupported physical filter operator %q", operation.Filter.Predicate.Operator)
		}
		if operation.Filter.Predicate.Right == nil {
			return fmt.Errorf("EQUALS filter requires a right value")
		}
		if err := checkValue(operation.Filter.Predicate.Left); err != nil {
			return err
		}
		return checkValue(*operation.Filter.Predicate.Right)
	case PhysicalDerivedLetOp:
		if strings.ToUpper(strings.TrimSpace(operation.DerivedLet.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
			return fmt.Errorf("unsupported physical derived LET operator %q", operation.DerivedLet.Operator)
		}
		for _, input := range operation.DerivedLet.Inputs {
			if err := checkValue(input); err != nil {
				return err
			}
		}
		return nil
	case PhysicalSortOp:
		return checkValue(operation.Sort.Value)
	case PhysicalLimitOp:
		if _, isCollection := collectionKeys[operation.Limit.BindKey]; isCollection {
			return fmt.Errorf("bind key %q cannot be used as both a collection and scalar bind", operation.Limit.BindKey)
		}
		return nil
	case PhysicalReturnOp:
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				if projection.Expression.Kind != PhysicalValueExpression && projection.Expression.Kind != PhysicalExtractExpression && projection.Expression.Kind != PhysicalAggregateExpression && projection.Expression.Kind != PhysicalPivotExpression && projection.Expression.Kind != PhysicalSliceExpression && projection.Expression.Kind != PhysicalObjectExpression {
					return fmt.Errorf("unsupported physical return expression kind %q", projection.Expression.Kind)
				}
				continue
			}
			if err := checkValue(projection.Value); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported physical operation %q", operation.Kind)
	}
}

func validateRenderablePredicateExpression(predicate PhysicalPredicateExpression, collectionKeys map[string]struct{}) error {
	switch predicate.Kind {
	case PhysicalExistsPredicate:
		if predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires a subplan")
		}
		for index, operation := range predicate.Exists.Operations {
			if err := validateRenderableOperation(operation, collectionKeys); err != nil {
				return fmt.Errorf("EXISTS subplan operation %d (%s): %w", index, operation.Kind, err)
			}
		}
		if predicate.Exists.Return.Kind != PhysicalValueExpression || predicate.Exists.Return.Value == nil {
			return fmt.Errorf("EXISTS subplan return must be a physical value expression")
		}
		return nil
	case PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return fmt.Errorf("comparison predicate requires a comparison")
		}
		return nil
	case PhysicalAllPredicate, PhysicalAnyPredicate, PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if err := validateRenderablePredicateExpression(child, collectionKeys); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported physical predicate kind %q", predicate.Kind)
	}
}

func runtimePhysicalBindVars(bindVars map[string]any, collectionKeys map[string]struct{}) map[string]any {
	out := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if _, collectionBinding := collectionKeys[key]; collectionBinding {
			out["@"+key] = clonePhysicalBindValue(value)
			continue
		}
		out[key] = clonePhysicalBindValue(value)
	}
	return out
}

func clonePhysicalBindValue(value any) any {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = clonePhysicalBindValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = clonePhysicalBindValue(item)
		}
		return out
	default:
		return value
	}
}
