package ir

import (
	"regexp"

	"github.com/calypr/loom/internal/dataframe/spec"
)

// PhysicalPlan is the renderer-independent AQL operation graph produced after
// semantic planning. Operations are ordered because AQL variables have lexical
// scope: an operation may reference only variables introduced before it.
type PhysicalSet struct {
	Variable string
	Kind     PhysicalSetKind
	Subplan  PhysicalSubplan
	Unique   bool
	// Output describes a compact, identity-safe projection of each set item.
	// A nil output preserves the full stored document for shared traversal or
	// other consumers that have not proved a smaller contract.
	Output *PhysicalSetOutput
	// Projection describes selector values computed in the original child-set
	// subquery. It replaces the payload-bearing second prepared array when all
	// downstream consumers have a projection-safe selector contract.
	Projection *PhysicalSetProjection
	// SourceSetVariable is set for a typed subset over an already materialized
	// shared traversal. Such a set does not begin with TRAVERSAL; ItemVariable
	// is bound by the renderer while iterating SourceSetVariable.
	SourceSetVariable string
	ItemVariable      string
	// SortByKey makes the set's node order part of physical semantics. Optional
	// relationship materialization must not rely on Arango traversal order.
	SortByKey bool
	// Reduction contains set-level reductions for direct child projections. It
	// is intentionally separate from the identity-bearing set because nested
	// traversals still consume every matching child.
	Reduction *PhysicalSetReduction
	Prepared  *PhysicalPreparedSet
}

type PhysicalSetKind string

const (
	PhysicalNodeSetKind     PhysicalSetKind = "NODE_SET"
	PhysicalNodePathSetKind PhysicalSetKind = "NODE_PATH"
)

// PhysicalPathNode is a public path node backed by a stored document. Value is
// typed so path lowering cannot smuggle AQL text into the renderer.
type PhysicalPathNode struct {
	Alias        string
	ResourceType string
	Value        PhysicalValue
}

// PhysicalPathRelationship carries relationship metadata while keeping raw
// edge documents private to the compiler/runtime.
type PhysicalPathRelationship struct {
	Alias            string
	LabelBindKey     string
	FromResourceType string
	ToResourceType   string
}

type PhysicalPathSeed struct {
	Variable   string
	Node       PhysicalPathNode
	RouteOrder int
}

// PhysicalPathExtend appends one depth-one traversal to an existing path
// set. SourcePath identifies the terminal stored document inside each source
// item (empty means the item itself). MatchMode is REQUIRED or OPTIONAL.
type PhysicalPathExtend struct {
	Variable       string
	SourceVariable string
	SourcePath     []string
	Traversal      PhysicalTraversal
	Node           PhysicalPathNode
	Relationship   PhysicalPathRelationship
	MatchMode      string
	RouteOrder     int
	// Scope contains typed edge/target filters and authorization operations
	// evaluated inside the correlated traversal subquery.
	Scope []PhysicalOperation
}

type PhysicalGraphReturn struct {
	PathSets     []string
	LimitBindKey string
}

// PhysicalUnnestJoinMode controls the row-preservation contract for a
// cardinality-changing operation. The renderer chooses the equivalent AQL
// shape; this IR never stores a query fragment.
type PhysicalUnnestJoinMode string

const (
	PhysicalUnnestInner PhysicalUnnestJoinMode = "INNER"
	PhysicalUnnestOuter PhysicalUnnestJoinMode = "OUTER"
)

// PhysicalUnnest introduces OutputVariable for each item produced by the
// array-valued Expression. Ordinality, when present, is a stable zero-based
// item position. InputVariable identifies the parent lexical scope used by
// the source expression and is a cardinality/scope barrier for optimizers.
type PhysicalUnnest struct {
	InputVariable  string
	OutputVariable string
	Ordinality     string
	Expression     PhysicalExpression
	JoinMode       PhysicalUnnestJoinMode
}

// PhysicalSetProjection is a single-materialization selector projection. The
// fields are arrays because selector evaluation preserves repeated FHIR
// values; scalar consumers apply their normal FIRST/FLATTEN semantics when
// reading the projected field.
type PhysicalSetProjection struct {
	Fields []PhysicalSetProjectionField
}

type PhysicalSetProjectionField struct {
	Name          string
	ResourceType  string
	Selector      spec.Selector
	ExecutionMode PhysicalSelectorExecutionMode
	Demand        PhysicalSelectorValueDemand
}

// PhysicalSelectorValueDemand describes how much of a selector's result a
// projected set must retain. The zero value preserves every value so plans
// built without demand analysis remain conservative.
type PhysicalSelectorValueDemand string

const (
	PhysicalSelectorAllValues  PhysicalSelectorValueDemand = ""
	PhysicalSelectorFirstValue PhysicalSelectorValueDemand = "FIRST_ONLY"
)

// PhysicalSetReduction is the typed contract for reducing selector slots
// after a set has been materialized. The renderer owns the AQL spelling; the
// IR only describes which projected slot supplies each result and which
// cardinality-preserving reduction applies.
type PhysicalSetReduction struct {
	Variable          string
	SourceSetVariable string
	Fields            []PhysicalSetReductionField
}

type PhysicalSetReductionField struct {
	Name        string
	SourceField string
	Mode        PhysicalSetReductionMode
}

type PhysicalSetReductionMode string

const (
	PhysicalSetReductionFirst    PhysicalSetReductionMode = "FIRST"
	PhysicalSetReductionAll      PhysicalSetReductionMode = "ALL"
	PhysicalSetReductionDistinct PhysicalSetReductionMode = "DISTINCT"
)

// PhysicalSetOutputField names the only stored properties that may survive a
// compact set projection. The graph identity fields preserve nested traversal
// and duplicate-edge semantics; payload is retained only when a downstream
// selector or rich consumer needs it.
type PhysicalSetOutputField string

const (
	PhysicalSetGraphIDField      PhysicalSetOutputField = "_id"
	PhysicalSetKeyField          PhysicalSetOutputField = "_key"
	PhysicalSetIDField           PhysicalSetOutputField = "id"
	PhysicalSetResourceTypeField PhysicalSetOutputField = "resourceType"
	PhysicalSetPayloadField      PhysicalSetOutputField = "payload"
)

type PhysicalSetOutput struct {
	Fields []PhysicalSetOutputField
}

type PhysicalSubplan struct {
	Captures   []string
	Operations []PhysicalOperation
	Return     PhysicalExpression
}

type PhysicalPredicate struct {
	Operator string
	Left     PhysicalValue
	// LeftExpression lets a comparison consume a typed selector extraction.
	// Exactly one of Left and LeftExpression is present. This keeps user
	// filters out of AQL-shaped strings while the scope predicates can retain
	// their compact value-only form.
	LeftExpression *PhysicalExpression
	Right          *PhysicalValue
	Quantifier     spec.ArrayQuantifier
	ValueKind      spec.FilterValueKind
}

type PhysicalPredicateKind string

const (
	PhysicalComparisonPredicate PhysicalPredicateKind = "COMPARISON"
	PhysicalAllPredicate        PhysicalPredicateKind = "ALL"
	PhysicalAnyPredicate        PhysicalPredicateKind = "ANY"
	PhysicalNotPredicate        PhysicalPredicateKind = "NOT"
	PhysicalExistsPredicate     PhysicalPredicateKind = "EXISTS"
)

// PhysicalPredicateExpression is the typed predicate tree used by rich
// physical operations. Exists contains a bounded correlated subplan; it is
// not a string-shaped LENGTH(FOR ...) compatibility escape hatch.
type PhysicalPredicateExpression struct {
	Kind       PhysicalPredicateKind
	Comparison *PhysicalPredicate
	Children   []PhysicalPredicateExpression
	Exists     *PhysicalSubplan
}

type PhysicalFilter struct {
	// Predicate is retained for the frozen navigation plan. New lowering must
	// use Expression so compound and existence predicates stay typed.
	Predicate  PhysicalPredicate
	Expression *PhysicalPredicateExpression
}

// PhysicalDerivedLet names a derived value. Operator is a compiler-owned
// symbolic operation (for example UNIQUE or LENGTH), never raw AQL.
type PhysicalDerivedLet struct {
	Variable string
	Operator string
	Inputs   []PhysicalValue
}

// PhysicalExpressionLet binds a deterministic expression once in the
// current root-row scope. It is distinct from symbolic derived operators.
type PhysicalExpressionLet struct {
	Variable   string
	Expression PhysicalExpression
}

// PhysicalSort is the deliberately small ordering primitive currently needed
// by generic root-grain previews. Additional sort keys and directions should
// be added only with a corresponding semantic ordering contract.
type PhysicalSort struct {
	Value PhysicalValue
}

// PhysicalLimit references a positive integer bind value. Keeping the value
// in BindVars retains the same parameterized execution boundary as filters.
type PhysicalLimit struct {
	BindKey string
}

type PhysicalProjection struct {
	Name string
	// Hidden projections are returned by the backend for executor-side
	// validation (for example dynamic runtime key metadata) but are omitted
	// from public dataframe columns after post-query materialization.
	Hidden     bool
	Value      PhysicalValue
	Expression *PhysicalExpression
}

type PhysicalReturn struct {
	Projections []PhysicalProjection
}

var (
	physicalVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	physicalBindKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	physicalPathPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate enforces the frozen physical-plan invariants without rendering AQL.
