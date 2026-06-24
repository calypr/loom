package dataframe

type Builder struct {
	Project              string
	AuthResourcePaths    []string
	RootResourceType     string
	PlanHint             *PlanHint
	Fields               []FieldSelect
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
	Traversals           []TraversalStep
	Sets                 []NamedSet
	DerivedFields        []DerivedField
	RepresentativeSlices []RepresentativeSlice
}

type TraversalStep struct {
	Label                string
	ToResourceType       string
	Alias                string
	Fields               []FieldSelect
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
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

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}
