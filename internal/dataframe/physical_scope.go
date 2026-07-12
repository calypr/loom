package dataframe

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

	for operationIndex, operation := range plan.Operations {
		resource, ok := physicalScopeResourceForOperation(operation)
		if !ok {
			continue
		}
		windowEnd := physicalScopeWindowEnd(plan.Operations, operationIndex+1)
		if err := validatePhysicalScopeWindow(plan.Operations, operationIndex, windowEnd, resource); err != nil {
			return err
		}
	}
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
	default:
		return physicalScopeResource{}, false
	}
}

// physicalScopeWindowEnd returns the first subsequent operation that can
// create another resource or terminate the plan. Scope must be established
// before either happens; otherwise a traversal can observe an unscoped row.
func physicalScopeWindowEnd(operations []PhysicalOperation, start int) int {
	for index := start; index < len(operations); index++ {
		switch operations[index].Kind {
		case PhysicalRootScanOp, PhysicalTraversalOp, PhysicalSetOp, PhysicalReturnOp:
			return index
		}
	}
	return len(operations)
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
