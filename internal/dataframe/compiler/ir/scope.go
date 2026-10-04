package ir

import "fmt"

const (
	physicalScopeProjectBind               = "project"
	physicalScopeAllowedBind               = "scope_allowed"
	physicalScopeAuthPathsBind             = "auth_resource_paths"
	physicalScopeAuthPathsUnrestrictedBind = "auth_resource_paths_unrestricted"
	physicalScopeAuthPathField             = "auth_resource_path"
	physicalScopeProjectField              = "project"
	physicalScopeDatasetGenerationBind     = datasetGenerationBindKey
	physicalScopeDatasetGenerationField    = datasetGenerationField
)

// ValidateGenericPhysicalPlanScope proves the authorization and project-scope
// contract of the navigation-only physical plan built by
// BuildGenericPhysicalPlan. It deliberately validates the physical operation
// graph rather than rendered AQL, so a renderer cannot accidentally hide a
// missing or reordered scope operation.
//
// This is intentionally narrower than PhysicalPlan.Validate: arbitrary
// physical plans may have a different scope strategy, while the generic FHIR
// navigation plan must use the exact project and authorization primitives
// checked here. It also requires an exact dataset-generation predicate for
// each scanned graph document, including the legacy null-generation case.
func ValidateGenericPhysicalPlanScope(plan PhysicalPlan) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate physical plan before verifying generic scope: %w", err)
	}
	walker := physicalScopeWalker{
		activeSubplans:  make(map[*PhysicalSubplan]bool),
		visitedSubplans: make(map[*PhysicalSubplan]bool),
	}
	if err := walker.operations(plan.Operations, "physical plan"); err != nil {
		return err
	}
	if plan.StageSequence != nil {
		for index := range plan.StageSequence.Stages {
			if err := walker.stage(&plan.StageSequence.Stages[index], index); err != nil {
				return err
			}
		}
	}
	return nil
}

type physicalScopeWalker struct {
	activeSubplans  map[*PhysicalSubplan]bool
	visitedSubplans map[*PhysicalSubplan]bool
}

func (walker *physicalScopeWalker) operations(operations []PhysicalOperation, owner string) error {
	for index := range operations {
		operation := &operations[index]
		resource, ok := physicalScopeResourceForOperation(*operation)
		if ok {
			windowEnd := physicalScopeWindowEnd(operations, index+1)
			if err := validatePhysicalScopeWindow(operations, index, windowEnd, resource); err != nil {
				return fmt.Errorf("%s: %w", owner, err)
			}
		}
		if operation.Kind == PhysicalRootScanOp && operation.RootScan != nil && operation.RootScan.Population != nil {
			if err := walker.operations(operation.RootScan.Population.ResourceOperations, owner+" population source"); err != nil {
				return err
			}
		}
		if err := walker.operationChildren(operation, fmt.Sprintf("%s operation %d (%s)", owner, index, operation.Kind)); err != nil {
			return err
		}
	}
	return nil
}

func (walker *physicalScopeWalker) operationChildren(operation *PhysicalOperation, owner string) error {
	if operation == nil {
		return nil
	}
	if operation.Filter != nil {
		if err := walker.predicate(operation.Filter.Expression, owner+" filter"); err != nil {
			return err
		}
		if operation.Filter.Predicate.LeftExpression != nil {
			if err := walker.expression(*operation.Filter.Predicate.LeftExpression, owner+" predicate"); err != nil {
				return err
			}
		}
	}
	if operation.ExpressionLet != nil {
		if err := walker.expression(operation.ExpressionLet.Expression, owner+" expression LET"); err != nil {
			return err
		}
	}
	if operation.Set != nil {
		if err := walker.subplan(&operation.Set.Subplan, owner+" set subplan"); err != nil {
			return err
		}
	}
	if operation.Unnest != nil {
		if err := walker.expression(operation.Unnest.Expression, owner+" unnest"); err != nil {
			return err
		}
		for index := range operation.Unnest.Owner.Route {
			route := operation.Unnest.Owner.Route[index]
			routeOwner := fmt.Sprintf("%s unnest route %d", owner, index)
			if err := validateEmbeddedTraversalScope(route.Traversal, route.Scope); err != nil {
				return fmt.Errorf("%s: %w", routeOwner, err)
			}
			if err := walker.operations(route.Scope, routeOwner); err != nil {
				return err
			}
		}
	}
	if operation.PathExtend != nil {
		if err := validateEmbeddedTraversalScope(operation.PathExtend.Traversal, operation.PathExtend.Scope); err != nil {
			return fmt.Errorf("%s path extension: %w", owner, err)
		}
		if err := walker.operations(operation.PathExtend.Scope, owner+" path extension scope"); err != nil {
			return err
		}
	}
	if operation.Return != nil {
		if err := walker.projections(operation.Return.Projections, owner+" return"); err != nil {
			return err
		}
	}
	if operation.CellTraceReturn != nil {
		if err := walker.expression(operation.CellTraceReturn.Value, owner+" cell trace"); err != nil {
			return err
		}
		for index := range operation.CellTraceReturn.IdentityParts {
			if err := walker.expression(operation.CellTraceReturn.IdentityParts[index].Expression, fmt.Sprintf("%s cell trace identity %d", owner, index)); err != nil {
				return err
			}
		}
		if operation.CellTraceReturn.ExplicitIdentity != nil {
			if err := walker.expression(*operation.CellTraceReturn.ExplicitIdentity, owner+" cell trace explicit identity"); err != nil {
				return err
			}
		}
		if trace := operation.CellTraceReturn.Construction; trace != nil && trace.RelatedSource != nil {
			if err := walker.subplan(&trace.RelatedSource.Subplan, owner+" cell trace related source"); err != nil {
				return err
			}
		}
	}
	if operation.PopulationMappingReturn != nil {
		if err := walker.expression(operation.PopulationMappingReturn.Members, owner+" population mapping members"); err != nil {
			return err
		}
		for index := range operation.PopulationMappingReturn.IdentityParts {
			if err := walker.expression(operation.PopulationMappingReturn.IdentityParts[index].Expression, fmt.Sprintf("%s population mapping identity %d", owner, index)); err != nil {
				return err
			}
		}
		if operation.PopulationMappingReturn.ExplicitIdentity != nil {
			if err := walker.expression(*operation.PopulationMappingReturn.ExplicitIdentity, owner+" population mapping explicit identity"); err != nil {
				return err
			}
		}
	}
	if operation.TableShapeExclusionReturn != nil {
		if err := walker.projections(operation.TableShapeExclusionReturn.Pivot.InputProjections, owner+" table-shape Pivot input projections"); err != nil {
			return err
		}
	}
	return nil
}

func validateEmbeddedTraversalScope(traversal PhysicalTraversal, operations []PhysicalOperation) error {
	operation := PhysicalOperation{Kind: PhysicalTraversalOp, Traversal: &traversal}
	resource, ok := physicalScopeResourceForOperation(operation)
	if !ok {
		return fmt.Errorf("embedded traversal has no resource scope contract")
	}
	return validatePhysicalScopeWindow(operations, -1, len(operations), resource)
}

func (walker *physicalScopeWalker) stage(stage *PhysicalConstructionStage, index int) error {
	if stage == nil {
		return nil
	}
	owner := fmt.Sprintf("construction stage %d %q", index, stage.ID)
	if err := walker.operations(stage.DerivedLets, owner+" derived operations"); err != nil {
		return err
	}
	if stage.Filter != nil {
		if err := walker.predicate(stage.Filter.Expression, owner+" filter"); err != nil {
			return err
		}
		if stage.Filter.Predicate.LeftExpression != nil {
			if err := walker.expression(*stage.Filter.Predicate.LeftExpression, owner+" filter predicate"); err != nil {
				return err
			}
		}
	}
	if err := walker.projections(stage.InputProjections, owner+" input projections"); err != nil {
		return err
	}
	if err := walker.projections(stage.OutputProjections, owner+" output projections"); err != nil {
		return err
	}
	if stage.GroupedPivot != nil {
		if err := walker.projections(stage.GroupedPivot.InputProjections, owner+" Pivot input projections"); err != nil {
			return err
		}
	}
	if stage.Unpivot != nil {
		if err := walker.projections(stage.Unpivot.InputProjections, owner+" Unpivot input projections"); err != nil {
			return err
		}
	}
	if stage.CodedGroup != nil {
		if err := walker.projections(stage.CodedGroup.RowValueProjections, owner+" coded-group input projections"); err != nil {
			return err
		}
	}
	if stage.CohortGroup != nil {
		for valueIndex := range stage.CohortGroup.Rows.MemberValues {
			if err := walker.expression(stage.CohortGroup.Rows.MemberValues[valueIndex].Expression, fmt.Sprintf("%s cohort member value %d", owner, valueIndex)); err != nil {
				return err
			}
		}
	}
	if stage.RelatedExpand != nil {
		if err := walker.subplan(&stage.RelatedExpand.RelatedRecords, owner+" related expansion"); err != nil {
			return err
		}
	}
	return nil
}

func (walker *physicalScopeWalker) projections(projections []PhysicalProjection, owner string) error {
	for index := range projections {
		if projections[index].Expression != nil {
			if err := walker.expression(*projections[index].Expression, fmt.Sprintf("%s projection %d", owner, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (walker *physicalScopeWalker) predicate(predicate *PhysicalPredicateExpression, owner string) error {
	if predicate == nil {
		return nil
	}
	if predicate.Comparison != nil && predicate.Comparison.LeftExpression != nil {
		if err := walker.expression(*predicate.Comparison.LeftExpression, owner+" comparison"); err != nil {
			return err
		}
	}
	for index := range predicate.Children {
		if err := walker.predicate(&predicate.Children[index], fmt.Sprintf("%s child %d", owner, index)); err != nil {
			return err
		}
	}
	if predicate.Exists != nil {
		return walker.subplan(predicate.Exists, owner+" EXISTS subplan")
	}
	return nil
}

func (walker *physicalScopeWalker) expression(expression PhysicalExpression, owner string) error {
	if expression.Aggregate != nil {
		if expression.Aggregate.Value != nil {
			if err := walker.expression(*expression.Aggregate.Value, owner+" aggregate value"); err != nil {
				return err
			}
		}
		if err := walker.predicate(expression.Aggregate.Predicate, owner+" aggregate predicate"); err != nil {
			return err
		}
	}
	if expression.Slice != nil {
		if err := walker.predicate(expression.Slice.Predicate, owner+" slice predicate"); err != nil {
			return err
		}
		if expression.Slice.Sort != nil {
			if err := walker.expression(*expression.Slice.Sort, owner+" slice sort"); err != nil {
				return err
			}
		}
		for index := range expression.Slice.Projections {
			if err := walker.expression(expression.Slice.Projections[index].Expression, fmt.Sprintf("%s slice projection %d", owner, index)); err != nil {
				return err
			}
		}
	}
	if expression.KeyedMap != nil {
		for _, child := range []struct {
			name string
			expr PhysicalExpression
		}{{"source", expression.KeyedMap.Source}, {"item key", expression.KeyedMap.ItemKey}, {"item value", expression.KeyedMap.ItemValue}} {
			if err := walker.expression(child.expr, owner+" keyed map "+child.name); err != nil {
				return err
			}
		}
		for index := range expression.KeyedMap.ValueFallbacks {
			if err := walker.expression(expression.KeyedMap.ValueFallbacks[index], fmt.Sprintf("%s keyed map fallback %d", owner, index)); err != nil {
				return err
			}
		}
	}
	if expression.KeySet != nil {
		if err := walker.expression(expression.KeySet.Source, owner+" key set source"); err != nil {
			return err
		}
		if err := walker.expression(expression.KeySet.ItemKey, owner+" key set item"); err != nil {
			return err
		}
	}
	if expression.Object != nil {
		for index := range expression.Object.Fields {
			if err := walker.expression(expression.Object.Fields[index].Expression, fmt.Sprintf("%s object field %d", owner, index)); err != nil {
				return err
			}
		}
	}
	if expression.Subplan != nil {
		return walker.subplan(expression.Subplan, owner+" expression subplan")
	}
	if expression.Call != nil {
		for index := range expression.Call.Args {
			if err := walker.expression(expression.Call.Args[index], fmt.Sprintf("%s call argument %d", owner, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (walker *physicalScopeWalker) subplan(subplan *PhysicalSubplan, owner string) error {
	if subplan == nil {
		return nil
	}
	if walker.activeSubplans[subplan] {
		return fmt.Errorf("%s contains a recursive physical subplan", owner)
	}
	if walker.visitedSubplans[subplan] {
		return nil
	}
	walker.activeSubplans[subplan] = true
	err := walker.operations(subplan.Operations, owner)
	if err == nil {
		err = walker.expression(subplan.Return, owner+" return")
	}
	delete(walker.activeSubplans, subplan)
	if err != nil {
		return err
	}
	walker.visitedSubplans[subplan] = true
	return nil
}

type physicalScopeResource struct {
	description         string
	projectVariables    []string
	datasetGenVariables []string
	authPaths           []PhysicalValue
}

func physicalScopeResourceForOperation(operation PhysicalOperation) (physicalScopeResource, bool) {
	switch operation.Kind {
	case PhysicalRootScanOp:
		return physicalScopeResource{
			description:         "root scan",
			projectVariables:    []string{operation.RootScan.Variable},
			datasetGenVariables: []string{operation.RootScan.Variable},
			authPaths: []PhysicalValue{{
				Variable: operation.RootScan.Variable,
				Path:     []string{physicalScopeAuthPathField},
			}},
		}, true
	case PhysicalTraversalOp:
		return physicalScopeResource{
			description:         fmt.Sprintf("traversal to %q", operation.Traversal.TargetVariable),
			projectVariables:    []string{operation.Traversal.EdgeVariable, operation.Traversal.TargetVariable},
			datasetGenVariables: []string{operation.Traversal.EdgeVariable, operation.Traversal.TargetVariable},
			authPaths: []PhysicalValue{
				{Variable: operation.Traversal.EdgeVariable, Path: []string{physicalScopeAuthPathField}},
				{Variable: operation.Traversal.TargetVariable, Path: []string{physicalScopeAuthPathField}},
			},
		}, true
	case PhysicalCollectionScanOp:
		if operation.CollectionScan == nil {
			return physicalScopeResource{}, false
		}
		return physicalScopeLookupResource("collection scan", operation.CollectionScan.Variable), true
	case PhysicalKeySetLookupOp:
		if operation.KeySetLookup == nil {
			return physicalScopeResource{}, false
		}
		return physicalScopeLookupResource("key-set lookup", operation.KeySetLookup.Variable), true
	case PhysicalDocumentLookupOp:
		if operation.DocumentLookup == nil {
			return physicalScopeResource{}, false
		}
		return physicalScopeLookupResource("document lookup", operation.DocumentLookup.Variable), true
	default:
		return physicalScopeResource{}, false
	}
}

func physicalScopeLookupResource(description, variable string) physicalScopeResource {
	return physicalScopeResource{
		description:         fmt.Sprintf("%s %q", description, variable),
		projectVariables:    []string{variable},
		datasetGenVariables: []string{variable},
		authPaths:           []PhysicalValue{{Variable: variable, Path: []string{physicalScopeAuthPathField}}},
	}
}

// physicalScopeWindowEnd returns the first subsequent operation that can
// create another resource or terminate the plan. Scope must be established
// before either happens; otherwise a traversal can observe an unscoped row.
func physicalScopeWindowEnd(operations []PhysicalOperation, start int) int {
	for index := start; index < len(operations); index++ {
		switch operations[index].Kind {
		case PhysicalRootScanOp, PhysicalTraversalOp, PhysicalCollectionScanOp, PhysicalKeySetLookupOp, PhysicalDocumentLookupOp,
			PhysicalSetOp, PhysicalUnnestOp, PhysicalReturnOp, PhysicalPopulationMappingReturnOp, PhysicalCellTraceReturnOp, PhysicalTableShapeExclusionReturnOp:
			return index
		}
	}
	return len(operations)
}

func PhysicalScopeWindowEnd(operations []PhysicalOperation, start int) int {
	return physicalScopeWindowEnd(operations, start)
}

func validatePhysicalScopeWindow(operations []PhysicalOperation, resourceIndex, windowEnd int, resource physicalScopeResource) error {
	projectIndex, err := findProjectScopeFilters(operations, resourceIndex+1, windowEnd, resource)
	if err != nil {
		return fmt.Errorf("%s at operation %d: %w", resource.description, resourceIndex, err)
	}

	generationIndex, err := findDatasetGenerationScopeFilters(operations, projectIndex+1, windowEnd, resource)
	if err != nil {
		return err
	}

	authIndex, authVariable, err := findAuthScopeLet(operations, generationIndex+1, windowEnd, resource)
	if err != nil {
		return fmt.Errorf("%s at operation %d: %w", resource.description, resourceIndex, err)
	}

	if err := findAuthScopeEquality(operations, authIndex+1, windowEnd, authVariable); err != nil {
		return fmt.Errorf("%s at operation %d: %w", resource.description, resourceIndex, err)
	}
	return nil
}

func findDatasetGenerationScopeFilters(operations []PhysicalOperation, start, end int, resource physicalScopeResource) (int, error) {
	lastIndex := start - 1
	for _, variable := range resource.datasetGenVariables {
		found := false
		for index := lastIndex + 1; index < end; index++ {
			operation := operations[index]
			if operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
				return 0, fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED LET at operation %d appears before dataset generation scope filter %s.%s == @%s", index, variable, physicalScopeDatasetGenerationField, physicalScopeDatasetGenerationBind)
			}
			if operation.Kind != PhysicalFilterOp {
				continue
			}
			predicate := operation.Filter.Predicate
			if predicate.Left.Variable != variable || !physicalPathEquals(predicate.Left.Path, []string{physicalScopeDatasetGenerationField}) {
				continue
			}
			if isDatasetGenerationScopePredicate(predicate, variable) {
				found = true
				lastIndex = index
				break
			}
			return 0, fmt.Errorf("dataset generation scope filter at operation %d must be %s.%s == @%s", index, variable, physicalScopeDatasetGenerationField, physicalScopeDatasetGenerationBind)
		}
		if !found {
			return 0, fmt.Errorf("missing dataset generation scope filter %s.%s == @%s before the next resource operation", variable, physicalScopeDatasetGenerationField, physicalScopeDatasetGenerationBind)
		}
	}
	return lastIndex, nil
}

func findProjectScopeFilters(operations []PhysicalOperation, start, end int, resource physicalScopeResource) (int, error) {
	lastIndex := start - 1
	for _, variable := range resource.projectVariables {
		found := false
		for index := lastIndex + 1; index < end; index++ {
			operation := operations[index]
			if operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
				return 0, fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED LET at operation %d appears before project scope filter %s.project == @%s", index, variable, physicalScopeProjectBind)
			}
			if operation.Kind != PhysicalFilterOp {
				continue
			}
			predicate := operation.Filter.Predicate
			if predicate.Left.Variable != variable || !physicalPathEquals(predicate.Left.Path, []string{physicalScopeProjectField}) {
				continue
			}
			if isProjectScopePredicate(predicate, variable) {
				found = true
				lastIndex = index
				break
			}
			return 0, fmt.Errorf("project scope filter at operation %d must be %s.project == @%s", index, variable, physicalScopeProjectBind)
		}
		if !found {
			return 0, fmt.Errorf("missing project scope filter %s.project == @%s before the next resource operation", variable, physicalScopeProjectBind)
		}
	}
	return lastIndex, nil
}

func findAuthScopeLet(operations []PhysicalOperation, start, end int, resource physicalScopeResource) (int, string, error) {
	for index := start; index < end; index++ {
		operation := operations[index]
		if operation.Kind == PhysicalFilterOp && isScopeAllowedFilter(operation.Filter.Predicate) {
			return 0, "", fmt.Errorf("auth scope equality at operation %d appears before AUTH_RESOURCE_PATH_ALLOWED LET", index)
		}
		if operation.Kind != PhysicalDerivedLetOp || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
			continue
		}
		if err := validateAuthScopeInputs(operation.DerivedLet.Inputs, resource.authPaths); err != nil {
			return 0, "", fmt.Errorf("AUTH_RESOURCE_PATH_ALLOWED LET at operation %d: %w", index, err)
		}
		return index, operation.DerivedLet.Variable, nil
	}
	return 0, "", fmt.Errorf("missing AUTH_RESOURCE_PATH_ALLOWED LET before the next resource operation")
}

func findAuthScopeEquality(operations []PhysicalOperation, start, end int, authVariable string) error {
	for index := start; index < end; index++ {
		operation := operations[index]
		if operation.Kind != PhysicalFilterOp {
			continue
		}
		predicate := operation.Filter.Predicate
		if predicate.Left.Variable != authVariable || len(predicate.Left.Path) != 0 {
			continue
		}
		if isScopeAllowedPredicate(predicate, authVariable) {
			return nil
		}
		return fmt.Errorf("auth scope equality at operation %d must be %s == @%s", index, authVariable, physicalScopeAllowedBind)
	}
	return fmt.Errorf("missing auth scope equality %s == @%s before the next resource operation", authVariable, physicalScopeAllowedBind)
}

func isProjectScopePredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" &&
		predicate.Left.Variable == variable &&
		physicalPathEquals(predicate.Left.Path, []string{physicalScopeProjectField}) &&
		predicate.Right != nil &&
		predicate.Right.BindKey == physicalScopeProjectBind &&
		predicate.Right.Variable == "" &&
		len(predicate.Right.Path) == 0
}

func isScopeAllowedFilter(predicate PhysicalPredicate) bool {
	return predicate.Operator == "EQUALS" && predicate.Right != nil && predicate.Right.BindKey == physicalScopeAllowedBind && predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}

func isScopeAllowedPredicate(predicate PhysicalPredicate, variable string) bool {
	return isScopeAllowedFilter(predicate) && predicate.Left.Variable == variable && len(predicate.Left.Path) == 0
}

func isDatasetGenerationScopePredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" &&
		predicate.Left.Variable == variable &&
		physicalPathEquals(predicate.Left.Path, []string{physicalScopeDatasetGenerationField}) &&
		predicate.Right != nil &&
		predicate.Right.BindKey == physicalScopeDatasetGenerationBind &&
		predicate.Right.Variable == "" &&
		len(predicate.Right.Path) == 0
}

func validateAuthScopeInputs(inputs, expectedPaths []PhysicalValue) error {
	for _, expected := range expectedPaths {
		if !containsPhysicalValue(inputs, expected) {
			return fmt.Errorf("must include %s", formatPhysicalValue(expected))
		}
	}
	for _, bindKey := range []string{physicalScopeAuthPathsBind, physicalScopeAuthPathsUnrestrictedBind} {
		expected := PhysicalValue{BindKey: bindKey}
		if !containsPhysicalValue(inputs, expected) {
			return fmt.Errorf("must include @%s", bindKey)
		}
	}
	return nil
}

func containsPhysicalValue(values []PhysicalValue, expected PhysicalValue) bool {
	for _, value := range values {
		if value.Variable == expected.Variable && value.BindKey == expected.BindKey && physicalPathEquals(value.Path, expected.Path) {
			return true
		}
	}
	return false
}

func physicalPathEquals(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func formatPhysicalValue(value PhysicalValue) string {
	if value.BindKey != "" {
		return "@" + value.BindKey
	}
	if len(value.Path) == 0 {
		return value.Variable
	}
	return value.Variable + "." + value.Path[0]
}
