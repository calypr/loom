package lower

import (
	"fmt"
	"strings"
)

// TraversalLoweringRequest is the backend-neutral input to the shared
// schema-derived traversal constructor. The caller supplies semantic resource
// identities and compiler-owned variable names; route direction, edge type
// discriminator, and endpoint fields are resolved from fhirschema through
// resolveStorageRoute.
//
// BindPrefix is part of the physical naming contract. It must be unique in
// the containing plan and stable for a given semantic traversal. It is used
// only to derive bind keys; it is never rendered as AQL source.
type TraversalLoweringRequest struct {
	FromType       string
	EdgeLabel      string
	ToType         string
	SourceVariable string
	TargetVariable string
	EdgeVariable   string
	BindPrefix     string
	Policy         PhysicalOptimizationPolicy
}

// TraversalLoweringResult contains the canonical physical traversal and the
// bind values required by its renderer. The route is retained as provenance
// for callers that need to inspect the schema-derived contract; callers must
// not mutate it or substitute a direction after construction.
type TraversalLoweringResult struct {
	Route     StorageRoute
	Traversal PhysicalTraversal
	BindVars  map[string]any
}

// BuildPhysicalTraversal resolves one FHIR relationship and constructs the
// canonical physical traversal used by every compiler frontend. It is the
// only lower-layer entry point that should assemble traversal direction,
// target discriminator, endpoint fields, and traversal bind keys.
func BuildPhysicalTraversal(request TraversalLoweringRequest) (TraversalLoweringResult, error) {
	if strings.TrimSpace(request.FromType) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal source resource type is required")
	}
	if strings.TrimSpace(request.EdgeLabel) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal edge label is required")
	}
	if strings.TrimSpace(request.ToType) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal target resource type is required")
	}
	if strings.TrimSpace(request.SourceVariable) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal source variable is required")
	}
	if strings.TrimSpace(request.TargetVariable) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal target variable is required")
	}
	if strings.TrimSpace(request.EdgeVariable) == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal edge variable is required")
	}
	prefix := strings.TrimSpace(request.BindPrefix)
	if prefix == "" {
		return TraversalLoweringResult{}, fmt.Errorf("traversal bind prefix is required")
	}
	identifiers := []struct {
		name  string
		value string
	}{
		{name: "source variable", value: request.SourceVariable},
		{name: "target variable", value: request.TargetVariable},
		{name: "edge variable", value: request.EdgeVariable},
		{name: "bind prefix", value: prefix},
	}
	for _, identifier := range identifiers {
		if !isCompilerIdentifier(identifier.value) {
			return TraversalLoweringResult{}, fmt.Errorf("traversal %s %q is not a safe compiler identifier", identifier.name, identifier.value)
		}
	}

	route, err := resolveStorageRoute(request.FromType, request.EdgeLabel, request.ToType)
	if err != nil {
		return TraversalLoweringResult{}, err
	}
	strategy, endpointField, endpointJoinField, endpointIndexFields := physicalTraversalStrategyForRoute(request.Policy, route)
	labelBind := prefix + "_label"
	typeBind := prefix + "_target_type"
	edgeCollectionBind := prefix + "_edge_collection"
	return TraversalLoweringResult{
		Route: route,
		Traversal: PhysicalTraversal{
			SourceVariable:        request.SourceVariable,
			TargetVariable:        request.TargetVariable,
			EdgeVariable:          request.EdgeVariable,
			Direction:             route.Direction,
			EdgeCollectionBindKey: edgeCollectionBind,
			EdgeLabelBindKey:      labelBind,
			TargetTypeBindKey:     typeBind,
			EdgeTargetTypeField:   route.targetEdgeTypeField(),
			Strategy:              strategy,
			EndpointField:         endpointField,
			EndpointJoinField:     endpointJoinField,
			EndpointIndexFields:   endpointIndexFields,
		},
		BindVars: map[string]any{
			labelBind:          request.EdgeLabel,
			typeBind:           request.ToType,
			edgeCollectionBind: "fhir_edge",
		},
	}, nil
}

func isCompilerIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

// physicalTraversalStrategyForRoute selects an execution strategy only after
// route metadata has been validated. Endpoint lookup is a typed physical
// choice; it does not permit callers to provide endpoint fields or AQL.
func physicalTraversalStrategyForRoute(policy PhysicalOptimizationPolicy, route storageRoute) (PhysicalTraversalStrategy, string, string, []string) {
	if !policy.RuleEnabled(PhysicalOptimizationRuleEndpointTraversal) {
		return PhysicalTraversalNative, "", "", nil
	}
	if parentField, joinField, fields, ok := route.endpointLookupFields(); ok && len(fields) > 0 {
		return PhysicalTraversalEndpointLookup, parentField, joinField, append([]string(nil), fields...)
	}
	return PhysicalTraversalNative, "", "", nil
}
