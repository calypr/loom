package dataframe

import (
	"fmt"
	"strings"
)

// RenderedPhysicalPlan is an executable AQL representation of a validated
// PhysicalPlan. BindVars is independent of the input plan and uses Arango's
// required "@name" key form for collection bind variables referenced as
// "@@name" in Query.
//
// This renderer covers the frozen generic navigation subset emitted by
// BuildGenericPhysicalPlan. CompileRequest and the dataframe service use it
// for that subset; richer semantic requests retain the compatibility lowered
// renderer until their typed physical operators are frozen.
type RenderedPhysicalPlan struct {
	Query    string
	BindVars map[string]any
}

// RenderPhysicalPlan renders the frozen navigation-only physical-plan subset
// to deterministic AQL. It validates the full plan before rendering and keeps
// data and metadata values out of the generated AQL source.
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
	returnExpression, err := renderer.renderReturn(layout.returnOp)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render RETURN: %w", err)
	}
	lines = append(lines, "RETURN "+returnExpression)
	return RenderedPhysicalPlan{
		Query:    strings.Join(lines, "\n") + "\n",
		BindVars: renderer.bindVars,
	}, nil
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
}

func (r *physicalPlanRenderer) renderScopeOperation(operation PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case PhysicalFilterOp:
		expression, err := r.renderPredicate(operation.Filter.Predicate)
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
	root       PhysicalRootScan
	rootScope  []PhysicalOperation
	rootWindow []PhysicalOperation
	traversals []physicalNavigationTraversal
	returnOp   PhysicalReturn
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
	lines = append(lines,
		fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", traversalIndent, traversal.TargetVariable, traversal.EdgeVariable, traversal.Direction, parentVariable, traversal.EdgeCollectionBindKey),
		fmt.Sprintf("%s  FILTER %s.label == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeLabelBindKey),
		fmt.Sprintf("%s  FILTER %s.%s == @%s", traversalIndent, traversal.EdgeVariable, traversal.EdgeTargetTypeField, traversal.TargetTypeBindKey),
		fmt.Sprintf("%s  FILTER %s.resourceType == @%s", traversalIndent, traversal.TargetVariable, traversal.TargetTypeBindKey),
	)
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
		value, err := r.renderValue(projection.Value)
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
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case PhysicalRootScanOp:
			keys[operation.RootScan.CollectionBindKey] = struct{}{}
		case PhysicalTraversalOp:
			if operation.Traversal.EdgeCollectionBindKey == "" {
				return nil, fmt.Errorf("render operation %d (TRAVERSAL): edge collection bind key is required", index)
			}
			keys[operation.Traversal.EdgeCollectionBindKey] = struct{}{}
		}
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
	case PhysicalFilterOp:
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
			if err := checkValue(projection.Value); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported physical operation %q", operation.Kind)
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
