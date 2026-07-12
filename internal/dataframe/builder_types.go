package dataframe

import (
	"time"

	"github.com/calypr/loom/internal/authscope"
)

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
	Fields               []FieldSelect
	Filters              []TypedFilter
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
	Traversals           []TraversalStep
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

type RunRequest struct {
	Builder Builder
	Limit   int
}

type Result struct {
	Columns     []string
	Rows        []map[string]any
	RowCount    int
	Diagnostics QueryDiagnostics
}

// QueryDiagnostics separates the cost of turning a dataframe request into
// rows. ArangoQuery is cursor time excluding Loom's per-row processing;
// RowMaterialization is the time spent flattening and delivering rows.
type QueryDiagnostics struct {
	// InputResolution is populated by the GraphQL adapter while resolving field
	// references from the catalog before this service is called.
	InputResolution    time.Duration
	RequestPreparation time.Duration
	Compilation        time.Duration
	ArangoQuery        time.Duration
	RowMaterialization time.Duration
	ResultAssembly     time.Duration
	Total              time.Duration
	Plan               CompilerPlanDiagnostics
}

// StreamResult describes rows delivered to a streaming caller. Columns are
// finalized only after iteration because flattened pivots can add bounded,
// data-dependent output keys. The streaming callback itself receives each
// flattened row as it is read from Arango.
type StreamResult struct {
	Columns     []string
	RowCount    int
	Diagnostics QueryDiagnostics
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
