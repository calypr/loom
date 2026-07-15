package semantic

// SemanticPlan is the backend-independent graph consumed by the physical
// compiler. Recipe bundles are the only production input format; the recipe
// builder populates this graph after schema, scope, and expression validation.

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type SemanticPlan struct {
	Version           int
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
	RowIdentity       *RowIdentity
	Root              SemanticNode
}

type SemanticNode struct {
	Alias        string
	ResourceType string
	EdgeLabel    string
	MatchMode    TraversalMatchMode
	From         *SemanticExpression
	Fields       []SemanticField
	Filters      []TypedFilter
	Pivots       []SemanticPivot
	Aggregates   []SemanticAggregate
	Slices       []SemanticSlice
	Children     []SemanticNode
	DynamicMaps  []SemanticDynamicMap
}

type SemanticField struct {
	Name       string
	FieldRef   string
	Selector   Selector
	Fallbacks  []Selector
	ValueMode  string
	Expr       *expression.Expression
	ExprType   expression.Type
	SourcePath string
}

type SemanticPivot struct {
	Name             string
	FieldRef         string
	ColumnSelector   Selector
	ValueSelector    Selector
	ValueFallbacks   []Selector
	ItemSource       Selector
	ItemResourceType string
	Columns          []string
	Family           string
}

type SemanticAggregate struct {
	Name            string
	Operation       string
	FieldRef        string
	Selector        *Selector
	PredicateField  string
	Predicate       *Selector
	PredicateEquals string
	ValueMode       string
}

type SemanticSlice struct {
	Name            string
	Limit           int
	PredicateField  string
	Predicate       *Selector
	PredicateEquals string
	Fields          []SemanticField
}

func validateSemanticSelector(resourceType string, selector Selector) error {
	_, _, err := spec.SelectorCardinality(resourceType, selector)
	return err
}

// Explain returns a compact logical summary. It intentionally does not expose
// physical AQL decisions.

type SemanticPlanExplanation struct {
	Version           int
	RootResourceType  string
	DatasetGeneration string
	RowIdentity       *RowIdentity
	Nodes             []SemanticNodeExplanation
}

type SemanticNodeExplanation struct {
	Alias          string
	ParentAlias    string
	ResourceType   string
	EdgeLabel      string
	MatchMode      TraversalMatchMode
	FieldCount     int
	PivotCount     int
	AggregateCount int
	SliceCount     int
}
