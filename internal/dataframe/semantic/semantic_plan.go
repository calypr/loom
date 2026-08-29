package semantic

// SemanticPlan is the backend-independent graph consumed by the physical
// compiler. Recipe bundles are the only production input format; the recipe
// builder populates this graph after schema, scope, and expression validation.

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type SemanticPlan struct {
	Version               int
	Project               string
	DatasetGeneration     string
	AuthResourcePaths     []string
	AuthScopeMode         authscope.ReadScopeMode
	RowIdentity           *spec.RowIdentity
	TraversalColumnNaming recipe.TraversalColumnNaming
	Root                  SemanticNode
}

type SemanticNode struct {
	Alias        string
	ResourceType string
	EdgeLabel    string
	MatchMode    spec.TraversalMatchMode
	From         *SemanticExpression
	Fields       []SemanticField
	Filters      []spec.TypedFilter
	Pivots       []SemanticPivot
	Aggregates   []SemanticAggregate
	Slices       []SemanticSlice
	Children     []SemanticNode
	DynamicMaps  []SemanticDynamicMap
}

type SemanticField struct {
	Name       string
	FieldRef   string
	Selector   spec.Selector
	Fallbacks  []spec.Selector
	ValueMode  string
	Expr       *expression.Expression
	ExprType   expression.Type
	SourcePath string
	Discovered bool
}

type SemanticPivot struct {
	Name             string
	FieldRef         string
	ColumnSelector   spec.Selector
	ValueSelector    spec.Selector
	ValueFallbacks   []spec.Selector
	ValueKind        expression.ValueKind
	StringifyValue   bool
	ItemSource       spec.Selector
	ItemResourceType string
	Columns          []string
	Family           string
	Discovered       bool
}

type SemanticAggregate struct {
	Name            string
	Operation       string
	FieldRef        string
	Selector        *spec.Selector
	PredicateField  string
	Predicate       *spec.Selector
	PredicateEquals string
	ValueMode       string
}

type SemanticSlice struct {
	Name            string
	Limit           int
	PredicateField  string
	Predicate       *spec.Selector
	PredicateEquals string
	Fields          []SemanticField
}

func validateSemanticSelector(resourceType string, selector spec.Selector) error {
	_, _, err := spec.SelectorCardinality(resourceType, selector)
	return err
}

// Explain returns a compact logical summary. It intentionally does not expose
// physical AQL decisions.

type SemanticPlanExplanation struct {
	Version           int
	RootResourceType  string
	DatasetGeneration string
	RowIdentity       *spec.RowIdentity
	Nodes             []SemanticNodeExplanation
}

type SemanticNodeExplanation struct {
	Alias          string
	ParentAlias    string
	ResourceType   string
	EdgeLabel      string
	MatchMode      spec.TraversalMatchMode
	FieldCount     int
	PivotCount     int
	AggregateCount int
	SliceCount     int
}
