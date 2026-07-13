package ir

import (
	"fmt"
	"strings"
)

const genericPhysicalExecutionLimitBind = "limit"

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

func matchesPhysicalEquality(predicate PhysicalPredicate, left, right PhysicalValue) bool {
	return strings.EqualFold(strings.TrimSpace(predicate.Operator), "EQUALS") && predicate.Right != nil && sameRenderPhysicalValue(predicate.Left, left) && sameRenderPhysicalValue(*predicate.Right, right)
}

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
		if operation.Kind != PhysicalFilterOp || !matchesPhysicalEquality(operation.Filter.Predicate, PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}}, PhysicalValue{BindKey: "dataset_generation"}) {
			return "", fmt.Errorf("dataset generation scope must be %s.dataset_generation == @dataset_generation", variable)
		}
	}
	authLetIndex := len(expectedProjectVariables) + len(expectedGenerationVariables)
	if operations[authLetIndex].Kind != PhysicalDerivedLetOp || operations[authLetIndex].DerivedLet == nil {
		return "", fmt.Errorf("scope block requires AUTH_RESOURCE_PATH_ALLOWED LET after dataset generation scope")
	}
	derived := operations[authLetIndex].DerivedLet
	if strings.ToUpper(strings.TrimSpace(derived.Operator)) != "AUTH_RESOURCE_PATH_ALLOWED" {
		return "", fmt.Errorf("scope LET must use AUTH_RESOURCE_PATH_ALLOWED")
	}
	expectedInputs := []PhysicalValue{{Variable: resourceVariable, Path: []string{"auth_resource_path"}}}
	if edgeVariable != "" {
		expectedInputs = []PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}
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
	if len(operations) <= authLetIndex+1 || operations[authLetIndex+1].Kind != PhysicalFilterOp || !matchesPhysicalEquality(operations[authLetIndex+1].Filter.Predicate, PhysicalValue{Variable: derived.Variable}, PhysicalValue{BindKey: "scope_allowed"}) {
		return "", fmt.Errorf("auth scope must be %s == @scope_allowed", derived.Variable)
	}
	return derived.Variable, nil
}
