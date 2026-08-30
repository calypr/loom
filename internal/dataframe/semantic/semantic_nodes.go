package semantic

import (
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// SemanticNode is the canonical backend-independent graph node used by every
// dataframe frontend. Runtime request provenance belongs in ExecutionContext,
// while output-specific shaping belongs in OutputPlan.
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
	Name     string
	FieldRef string
	// Expr and Fallbacks are the checked semantic expressions that produced
	// this field. Selectors are intentionally derived from these expressions
	// at the physical boundary; keeping both representations here allowed the
	// recipe planner and lowerer to silently diverge.
	Expr       SemanticExpression
	Fallbacks  []SemanticExpression
	Projection spec.ProjectionMode
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
	Predicate       *spec.Selector
	PredicateEquals string
	ValueMode       string
}

type SemanticSlice struct {
	Name            string
	Limit           int
	Predicate       *spec.Selector
	PredicateEquals string
	Fields          []SemanticField
}

func validateSemanticSelector(resourceType string, selector spec.Selector) error {
	_, _, err := spec.SelectorCardinality(resourceType, selector)
	return err
}
