package lower

import (
	"errors"
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// ErrUnsupportedStorageRoute identifies a FHIR relationship that is known to
// the generated schema but has not been proven safe for Loom's stored
// fhir_edge layout. Callers must not substitute a different AQL direction for
// this error: every physical direction must have a compiler-owned storage
// contract.
var ErrUnsupportedStorageRoute = errors.New("unsupported storage route")

// storageRoute is the compiler-owned bridge from generated FHIR relationship
// metadata to the currently supported stored-edge operation. The generated
// FHIR Direction describes the logical reference, not the Arango direction;
// Direction below is established only by a storage proof in
// resolveStorageRoute.
type storageRoute struct {
	Relationship fhirschema.CompilerTraversal
	Direction    ir.PhysicalTraversalDirection
}

// targetEdgeTypeField returns the fhir_edge type discriminator for the node
// reached by this route. It is deliberately direction-aware: forward FHIR
// references store their target in to_type, while the generated builder route
// reads the source-side type through an INBOUND traversal. The node
// resourceType predicate remains mandatory even when this edge predicate is
// used as an index-selectivity hint.
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

// endpointLookupFields describes the generic fhir_edge compound-index
// contract used by the endpoint strategy. It is derived solely from the
// proven storage direction; callers must still retain native traversal when
// the metadata is incomplete.
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

// resolveStorageRoute accepts generated synthetic reverse routes for which
// the source resource is provably the reference target. The ingester stores
// normal FHIR references as child _from -> parent _to, so a parent -> child
// dataframe route is an INBOUND fhir_edge traversal.
//
// It also accepts generated forward routes when the schema gives an exact
// target hint and an outbound logical reference. EdgeFromGrip (the single
// edge adapter used by ingest) materializes every such reference as source
// _from -> referenced target _to in fhir_edge, so the generated target hint is
// the storage proof; no resource-specific allowlist is needed.
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
	// LookupTraversal is keyed by this exact tuple, but retain the check so a
	// malformed future generated map cannot silently become executable AQL.
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

	// Only a precise, generated <parent-resource>/* target hint proves that
	// this tuple is the synthetic parent -> child route. In particular,
	// Resource/* is deliberately not evidence for a concrete parent type.
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
