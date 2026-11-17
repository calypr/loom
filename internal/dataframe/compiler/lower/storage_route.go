package lower

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var ErrUnsupportedStorageRoute = errors.New("unsupported storage route")

type storageRoute struct {
	Relationship fhirschema.CompilerTraversal
	Direction    ir.PhysicalTraversalDirection
}

func (route storageRoute) targetEdgeTypeField() string {
	switch route.Direction {
	case ir.PhysicalInbound:
		return "from_type"
	case ir.PhysicalOutbound:
		return "to_type"
	default:
		return ""
	}
}

func (route storageRoute) endpointLookupFields() (parentField, joinField string, indexFields []string, ok bool) {
	switch route.Direction {
	case ir.PhysicalInbound:
		return "_to", "_from", []string{"_to", "project", "dataset_generation", "label", "from_type"}, true
	case ir.PhysicalOutbound:
		return "_from", "_to", []string{"_from", "project", "dataset_generation", "label", "to_type"}, true
	default:
		return "", "", nil, false
	}
}

func resolveStorageRoute(fromType, edgeLabel, toType string) (storageRoute, error) {
	if !fhirschema.HasResource(fromType) {
		return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, "source resource type is not represented by the active generated FHIR schema")
	}
	if !fhirschema.HasResource(toType) {
		return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, "target resource type is not represented by the active generated FHIR schema")
	}

	spec, found := fhirschema.LookupTraversal(fromType, edgeLabel, toType)
	if !found {
		return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, "is not represented by the active generated FHIR schema")
	}
	if spec.FromType != fromType || spec.EdgeLabel != edgeLabel || spec.ToType != toType {
		return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, "generated traversal metadata does not match the requested route")
	}
	relationship, found, err := fhirschema.ResolveCompilerTraversal(fromType, edgeLabel, toType)
	if err != nil {
		return storageRoute{}, fmt.Errorf("%w: %s -> %s (%s) has unsafe generated metadata: %v", ErrUnsupportedStorageRoute, fromType, toType, edgeLabel, err)
	}
	if !found {
		return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, "is not represented by the active generated FHIR schema")
	}

	if hasExactStorageParentHint(spec.RegexMatch, fromType) {
		return storageRoute{Relationship: relationship, Direction: ir.PhysicalInbound}, nil
	}

	if isProvenExactOutboundRoute(spec, relationship) {
		return storageRoute{Relationship: relationship, Direction: ir.PhysicalOutbound}, nil
	}

	return storageRoute{}, unsupportedStorageRouteError(fromType, edgeLabel, toType, fmt.Sprintf("is not a proven stored route: reverse routes require RegexMatch %q; outbound references require an exact target hint (got %s)", fromType+"/*", formatRegexMatchHints(spec.RegexMatch)))
}

func isProvenExactOutboundRoute(spec fhirschema.TraversalSpec, relationship fhirschema.CompilerTraversal) bool {
	if relationship.Direction != fhirschema.TraversalOutbound {
		return false
	}
	return hasExactStorageTargetHint(spec.RegexMatch, spec.ToType)
}

func unsupportedStorageRouteError(fromType, edgeLabel, toType, reason string) error {
	return fmt.Errorf("%w: %s -> %s (%s): %s", ErrUnsupportedStorageRoute, fromType, toType, edgeLabel, reason)
}

func hasExactStorageParentHint(hints []string, fromType string) bool {
	return hasExactStorageTargetHint(hints, fromType)
}

func hasExactStorageTargetHint(hints []string, resourceType string) bool {
	want := resourceType + "/*"
	for _, hint := range hints {
		if strings.TrimSpace(hint) == want {
			return true
		}
	}
	return false
}

func formatRegexMatchHints(hints []string) string {
	if len(hints) == 0 {
		return "none"
	}
	quoted := make([]string, 0, len(hints))
	for _, hint := range hints {
		quoted = append(quoted, fmt.Sprintf("%q", strings.TrimSpace(hint)))
	}
	return strings.Join(quoted, ", ")
}
