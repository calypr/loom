package spec

import "github.com/calypr/loom/internal/authscope"

type Builder struct {
	Project string
	// DatasetGeneration is optional. An empty value deliberately targets only
	// the legacy null generation namespace; it never means every generation.
	DatasetGeneration string
	AuthResourcePaths []string
	// AuthScopeMode is set by a request-level authorization resolver. It is
	// required to distinguish a restricted empty path set from an unrestricted
	// one. The empty value preserves the legacy direct-Builder convention that
	// no paths means unrestricted.
	AuthScopeMode    authscope.ReadScopeMode
	RootResourceType string
	RowGrain         RowGrain
	Fields           []FieldSelect
	Filters          []TypedFilter
	Pivots           []PivotSelect
	Aggregates       []AggregateSelect
	Slices           []RepresentativeSlice
	Traversals       []TraversalStep
}

type TraversalStep struct {
	Label          string
	ToResourceType string
	Alias          string
	Fields         []FieldSelect
	Filters        []TypedFilter
	Pivots         []PivotSelect
	Aggregates     []AggregateSelect
	Slices         []RepresentativeSlice
	// MatchMode defaults to OPTIONAL. REQUIRED retains only root rows with a
	// matching relationship route; it is lowered as a semi-join rather than a
	// post-projection filter.
	MatchMode  TraversalMatchMode
	Traversals []TraversalStep
}

// RepresentativeSlice is a bounded child projection requested by the
// semantic dataframe request. It is consumed directly by the physical plan;
// it is not a lowered named-set artifact.
type RepresentativeSlice struct {
	Name              string
	SourceSet         string
	Predicate         string
	PredicateFieldRef string
	PredicatePath     string
	PredicateEquals   string
	Limit             int
	Fields            []FieldSelect
}

type FieldSelect struct {
	Name              string
	FieldRef          string
	Select            string
	FallbackFieldRefs []string
	FallbackSelects   []string
	ValueMode         string
}

type PivotSelect struct {
	Name         string
	FieldRef     string
	ColumnSelect string
	ValueSelect  string
	Columns      []string
	PivotFamily  string
}

type AggregateSelect struct {
	Name              string
	Operation         string
	FieldRef          string
	Select            string
	PredicateFieldRef string
	PredicatePath     string
	PredicateEquals   string
	ValueMode         string
}
