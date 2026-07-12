package discovery

import (
	"errors"
	"sort"

	"github.com/calypr/loom/internal/fhirschema"
)

// ErrColumnUnavailable is returned for both unknown and stale column IDs. The
// error intentionally does not disclose whether an identifier was once valid
// in another project, authorization scope, or catalog state.
var ErrColumnUnavailable = errors.New("discovery column capability is unavailable in current catalog facts")

// ErrRelationshipUnavailable is returned for both unknown and stale route
// IDs, without disclosing graph labels outside the current scoped facts.
var ErrRelationshipUnavailable = errors.New("discovery relationship capability is unavailable in current catalog facts")

// CapabilityResolver resolves opaque Snapshot identifiers against one fresh,
// already-authorized CatalogFacts value. It is intentionally in-memory only:
// the caller owns catalog reads, authorization, and any future dataset identity
// integration.
type CapabilityResolver struct {
	evidence discoveryEvidence
}

// NewCapabilityResolver builds a resolver from one current project/scope's
// catalog facts. It applies exactly the same generated-schema and catalog
// normalization as BuildSnapshot.
func NewCapabilityResolver(facts CatalogFacts) (*CapabilityResolver, error) {
	evidence, err := collectEvidence(facts)
	if err != nil {
		return nil, err
	}
	return &CapabilityResolver{evidence: evidence}, nil
}

// ResolvedColumn contains compiler-adjacent facts that must never be used as
// transport data. All fields are excluded from JSON to prevent canonical FHIR
// paths or selectors from escaping the opaque Snapshot boundary.
type ResolvedColumn struct {
	ID                     ColumnID                      `json:"-"`
	ResourceType           string                        `json:"-"`
	CanonicalPath          string                        `json:"-"`
	Selector               *fhirschema.FieldSelectorSpec `json:"-"`
	ValueKind              ValueKind                     `json:"-"`
	Repeated               bool                          `json:"-"`
	CanSelect              bool                          `json:"-"`
	CanFilter              bool                          `json:"-"`
	CanPivot               bool                          `json:"-"`
	FilterOperators        []FilterOperator              `json:"-"`
	FilterQuantifiers      []FilterQuantifier            `json:"-"`
	PopulatedDocumentCount int64                         `json:"-"`
	SuggestedValues        []string                      `json:"-"`
	ValuesTruncated        bool                          `json:"-"`
	Pivot                  *ResolvedPivot                `json:"-"`
}

// ResolvedPivot is a schema-validated, catalog-observed pivot family. Its
// selector fields remain internal and are omitted from JSON even when a caller
// accidentally marshals a resolved capability.
type ResolvedPivot struct {
	Family           string                       `json:"-"`
	ColumnSelector   fhirschema.FieldSelectorSpec `json:"-"`
	ValueSelector    fhirschema.FieldSelectorSpec `json:"-"`
	Columns          []string                     `json:"-"`
	ColumnsTruncated bool                         `json:"-"`
}

// ResolvedRelationship contains the raw generated graph route required by a
// later mapper. Snapshot intentionally exposes only an opaque ID and a human
// label, while this record is kept entirely out of JSON.
type ResolvedRelationship struct {
	ID                RelationshipID               `json:"-"`
	FromResourceType  string                       `json:"-"`
	EdgeLabel         string                       `json:"-"`
	ToResourceType    string                       `json:"-"`
	Multiplicity      RelationshipMultiplicity     `json:"-"`
	ObservedEdgeCount int64                        `json:"-"`
	Traversal         fhirschema.CompilerTraversal `json:"-"`
}

// ResolveColumn resolves a ColumnID only when it is still present in the fresh
// schema-supported catalog evidence used to construct this resolver.
func (resolver *CapabilityResolver) ResolveColumn(id ColumnID) (ResolvedColumn, error) {
	if resolver == nil {
		return ResolvedColumn{}, ErrColumnUnavailable
	}
	aggregate, ok := resolver.evidence.columns[id]
	if !ok {
		return ResolvedColumn{}, ErrColumnUnavailable
	}
	values, truncated := normalizedValues(aggregate.values, aggregate.valuesTruncated)
	resolved := ResolvedColumn{
		ID:                     id,
		ResourceType:           aggregate.resourceType,
		CanonicalPath:          aggregate.canonical,
		ValueKind:              aggregate.valueKind,
		Repeated:               aggregate.repeated,
		CanSelect:              aggregate.canSelect,
		CanFilter:              aggregate.canFilter,
		CanPivot:               aggregate.canPivot,
		PopulatedDocumentCount: aggregate.populatedDocumentCount,
		SuggestedValues:        values,
		ValuesTruncated:        truncated,
	}
	if aggregate.hasSelector {
		selector := cloneSelectorSpec(aggregate.selector)
		resolved.Selector = &selector
	}
	if aggregate.canFilter {
		resolved.FilterOperators = append([]FilterOperator(nil), filterOperators(aggregate.valueKind)...)
		resolved.FilterQuantifiers = append([]FilterQuantifier(nil), filterQuantifiers(aggregate.repeated)...)
	}
	if pivot := resolvedPivot(aggregate); pivot != nil {
		resolved.Pivot = pivot
	}
	return resolved, nil
}

// ResolveRelationship resolves a RelationshipID only when its route remains
// populated and represented by the active generated FHIR graph schema.
func (resolver *CapabilityResolver) ResolveRelationship(id RelationshipID) (ResolvedRelationship, error) {
	if resolver == nil {
		return ResolvedRelationship{}, ErrRelationshipUnavailable
	}
	aggregate, ok := resolver.evidence.relationships[id]
	if !ok {
		return ResolvedRelationship{}, ErrRelationshipUnavailable
	}
	return ResolvedRelationship{
		ID:                id,
		FromResourceType:  aggregate.fromType,
		EdgeLabel:         aggregate.label,
		ToResourceType:    aggregate.toType,
		Multiplicity:      aggregate.multiple,
		ObservedEdgeCount: aggregate.edgeCount,
		Traversal:         aggregate.traversal,
	}, nil
}

func resolvedPivot(aggregate *columnAggregate) *ResolvedPivot {
	if len(aggregate.pivots) == 0 {
		return nil
	}
	keys := make([]string, 0, len(aggregate.pivots))
	for key := range aggregate.pivots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pivot := aggregate.pivots[keys[0]]
	columns, truncated := normalizedValues(pivot.columns, pivot.truncated)
	return &ResolvedPivot{
		Family:           pivot.spec.family,
		ColumnSelector:   cloneSelectorSpec(pivot.spec.columnSelector),
		ValueSelector:    cloneSelectorSpec(pivot.spec.valueSelector),
		Columns:          columns,
		ColumnsTruncated: truncated,
	}
}

func cloneSelectorSpec(spec fhirschema.FieldSelectorSpec) fhirschema.FieldSelectorSpec {
	cloned := spec
	if spec.Where != nil {
		where := *spec.Where
		cloned.Where = &where
	}
	return cloned
}
