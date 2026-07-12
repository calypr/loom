package dataframe

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
	AuthScopeMode        authscope.ReadScopeMode
	RootResourceType     string
	RowGrain             RowGrain
	PlanHint             *PlanHint
	Fields               []FieldSelect
	Filters              []TypedFilter
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
	Traversals           []TraversalStep
	Sets                 []NamedSet
	DerivedFields        []DerivedField
	RepresentativeSlices []RepresentativeSlice
	// RequiredTraversalMatches is populated only by compiler lowering from
	// TraversalStep.MatchMode. It keeps root-scoped relationship predicates
	// separate from materialized traversal sets, so Compile can apply them
	// before root SORT/LIMIT.
	RequiredTraversalMatches []RequiredTraversalMatch
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
	MatchMode            TraversalMatchMode
	Traversals           []TraversalStep
	Sets                 []NamedSet
	DerivedFields        []DerivedField
	RepresentativeSlices []RepresentativeSlice
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

type RunRequest struct {
	Builder Builder
	Limit   int
}

type Result struct {
	Columns  []string
	Rows     []map[string]any
	RowCount int
}

// StreamResult describes rows delivered to a streaming caller. Columns are
// finalized only after iteration because flattened pivots can add bounded,
// data-dependent output keys. The streaming callback itself receives each
// flattened row as it is read from Arango.
type StreamResult struct {
	Columns  []string
	RowCount int
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}
