package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type TraversalLoweringRequest struct {
	FromType          string
	EdgeLabel         string
	ToType            string
	DirectionOverride ir.PhysicalTraversalDirection
	SourceVariable    string
	TargetVariable    string
	EdgeVariable      string
	BindPrefix        string
	Policy            ir.PhysicalOptimizationPolicy
}

type TraversalLoweringResult struct {
	Traversal ir.PhysicalTraversal
	BindVars  map[string]any
}

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
	if request.DirectionOverride != "" {
		if request.DirectionOverride != ir.PhysicalInbound && request.DirectionOverride != ir.PhysicalOutbound {
			return TraversalLoweringResult{}, fmt.Errorf("traversal direction override %q is unsupported", request.DirectionOverride)
		}
		if request.FromType != request.ToType && route.Direction != request.DirectionOverride {
			return TraversalLoweringResult{}, fmt.Errorf("traversal direction override %q conflicts with schema-derived route %q", request.DirectionOverride, route.Direction)
		}
		route.Direction = request.DirectionOverride
	}
	strategy, endpointField, endpointJoinField, endpointIndexFields := physicalTraversalStrategyForRoute(request.Policy, route)
	labelBind := prefix + "_label"
	sourceTypeBind := prefix + "_source_type"
	typeBind := prefix + "_target_type"
	edgeCollectionBind := prefix + "_edge_collection"
	return TraversalLoweringResult{
		Traversal: ir.PhysicalTraversal{
			SourceVariable:        request.SourceVariable,
			TargetVariable:        request.TargetVariable,
			EdgeVariable:          request.EdgeVariable,
			Direction:             route.Direction,
			EdgeCollectionBindKey: edgeCollectionBind,
			EdgeLabelBindKey:      labelBind,
			SourceTypeBindKey:     sourceTypeBind,
			TargetTypeBindKey:     typeBind,
			EdgeTargetTypeField:   route.targetEdgeTypeField(),
			Strategy:              strategy,
			EndpointField:         endpointField,
			EndpointJoinField:     endpointJoinField,
			EndpointIndexFields:   endpointIndexFields,
		},
		BindVars: map[string]any{
			labelBind:          request.EdgeLabel,
			sourceTypeBind:     request.FromType,
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

func physicalTraversalStrategyForRoute(policy ir.PhysicalOptimizationPolicy, route storageRoute) (ir.PhysicalTraversalStrategy, string, string, []string) {
	if !policy.RuleEnabled(ir.PhysicalOptimizationRuleEndpointTraversal) {
		return ir.PhysicalTraversalNative, "", "", nil
	}
	if parentField, joinField, fields, ok := route.endpointLookupFields(); ok && len(fields) > 0 {
		return ir.PhysicalTraversalEndpointLookup, parentField, joinField, append([]string(nil), fields...)
	}
	return ir.PhysicalTraversalNative, "", "", nil
}
