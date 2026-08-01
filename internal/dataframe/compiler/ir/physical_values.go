package ir

import "github.com/calypr/loom/internal/dataframe/spec"

// PhysicalPlan is the renderer-independent AQL operation graph produced after
// semantic planning. Operations are ordered because AQL variables have lexical
// scope: an operation may reference only variables introduced before it.
type PhysicalValue struct {
	Variable string
	Path     []string
	BindKey  string
}

// PhysicalCardinality and PhysicalNullBehavior are deliberately explicit on
// every rich expression. A renderer must not infer array/scalar/null behavior
// from where an expression appears in an AQL template.
type PhysicalCardinality string

const (
	PhysicalScalarCardinality PhysicalCardinality = "SCALAR"
	PhysicalArrayCardinality  PhysicalCardinality = "ARRAY"
	PhysicalObjectCardinality PhysicalCardinality = "OBJECT"
)

type PhysicalNullBehavior string

const (
	PhysicalPreserveNull PhysicalNullBehavior = "PRESERVE_NULL"
	PhysicalOmitNulls    PhysicalNullBehavior = "OMIT_NULLS"
	PhysicalEmptyOnNull  PhysicalNullBehavior = "EMPTY_ON_NULL"
)

type PhysicalExpressionKind string

const (
	PhysicalValueExpression PhysicalExpressionKind = "VALUE"
	// PhysicalLiteralExpression is a typed bind-backed constant. Literals are
	// kept out of generated AQL so recipe authors cannot inject query source.
	PhysicalLiteralExpression   PhysicalExpressionKind = "LITERAL"
	PhysicalExtractExpression   PhysicalExpressionKind = "EXTRACT"
	PhysicalAggregateExpression PhysicalExpressionKind = "AGGREGATE"
	PhysicalPivotExpression     PhysicalExpressionKind = "PIVOT_MAP"
	PhysicalSliceExpression     PhysicalExpressionKind = "SLICE"
	// PhysicalLookupExpression performs one bounded key lookup over a
	// collection-valued expression. It is backend-neutral and is useful for
	// any frontend that freezes a finite set of key/value columns before
	// execution; it is not a recipe-specific dynamic-map operation.
	PhysicalLookupExpression       PhysicalExpressionKind = "LOOKUP"
	PhysicalObjectLookupExpression PhysicalExpressionKind = "OBJECT_LOOKUP"
	PhysicalKeyedMapExpression     PhysicalExpressionKind = "KEYED_MAP"
	PhysicalObjectKeysExpression   PhysicalExpressionKind = "OBJECT_KEYS"
	// PhysicalKeySetExpression returns the sorted distinct keys observed in a
	// bounded source. It is executor metadata, not a public dataframe column.
	PhysicalKeySetExpression PhysicalExpressionKind = "KEY_SET"
	PhysicalObjectExpression PhysicalExpressionKind = "OBJECT"
	// PhysicalCallExpression represents a recipe-neutral expression function.
	// Name is validated against the compiler-owned operator registry and Args
	// remain typed expressions; neither contains AQL source text.
	PhysicalCallExpression PhysicalExpressionKind = "CALL"
)

// PhysicalSelectorExecutionMode records a schema-proven selector lowering.
// Generic is the safe fallback for predicates, fallbacks, choice paths, and
// unknown cardinality.
type PhysicalSelectorExecutionMode string

const (
	PhysicalSelectorGeneric          PhysicalSelectorExecutionMode = "GENERIC"
	PhysicalSelectorDirectScalar     PhysicalSelectorExecutionMode = "DIRECT_SCALAR"
	PhysicalSelectorConditionalArray PhysicalSelectorExecutionMode = "CONDITIONAL_ARRAY"
)

// PhysicalExpression is a closed, renderer-independent value tree. It carries
// no AQL source text: selector paths have already been parsed and all literals
// are represented by bind values.
type PhysicalExpression struct {
	Kind         PhysicalExpressionKind
	Cardinality  PhysicalCardinality
	NullBehavior PhysicalNullBehavior
	Value        *PhysicalValue
	Literal      *PhysicalLiteral
	Extract      *PhysicalExtract
	Aggregate    *PhysicalAggregate
	Pivot        *PhysicalPivotMap
	Slice        *PhysicalSlice
	Lookup       *PhysicalLookup
	ObjectLookup *PhysicalObjectLookup
	KeyedMap     *PhysicalKeyedMap
	ObjectKeys   *PhysicalObjectKeys
	KeySet       *PhysicalKeySet
	Object       *PhysicalObject
	Call         *PhysicalCall
}

// PhysicalLiteral references a value in the plan bind map. BindKey is
// deliberately required even for primitive constants so all recipe data uses
// the same parameterized execution boundary.
type PhysicalLiteral struct {
	BindKey string
}

// PhysicalCall is a backend-neutral expression operator. TargetKind is used
// only by cast and names a logical scalar type (for example "string" or
// "integer"), never a backend type or query fragment.
type PhysicalCall struct {
	Name       string
	Args       []PhysicalExpression
	TargetKind string
}

// PhysicalExtract obtains one FHIR selector from a variable or prior set
// element. ResourceType keeps schema validation available after semantic
// lowering; fallbacks preserve the existing FIRST_NON_NULL behavior.
type PhysicalExtract struct {
	Source       PhysicalValue
	ResourceType string
	Selector     spec.Selector
	Fallbacks    []spec.Selector
	// Distinct preserves the explicit DISTINCT projection mode after semantic
	// lowering. It is meaningful only for an array-valued expression.
	Distinct      bool
	ExecutionMode PhysicalSelectorExecutionMode
	// Prepared points at a selector value projected by a prepared child set.
	// Source remains the owning set for scope validation and diagnostics.
	Prepared *PhysicalPreparedReference
}

// PhysicalPreparedReference identifies one selector column in a prepared set.
type PhysicalPreparedReference struct {
	SetVariable string
	Field       string
}

// PhysicalPreparedSet describes selector projections shared by rich
// consumers over one materialized relationship set. Preparation stays
// attached to the correlated set so root row grain cannot change.
type PhysicalPreparedSet struct {
	Variable          string
	SourceSetVariable string
	Fields            []PhysicalPreparedField
}

type PhysicalPreparedField struct {
	Name         string
	ResourceType string
	Selector     spec.Selector
}

type PhysicalAggregateOperation string

const (
	PhysicalCountAggregate          PhysicalAggregateOperation = "COUNT"
	PhysicalCountDistinctAggregate  PhysicalAggregateOperation = "COUNT_DISTINCT"
	PhysicalExistsAggregate         PhysicalAggregateOperation = "EXISTS"
	PhysicalDistinctValuesAggregate PhysicalAggregateOperation = "DISTINCT_VALUES"
	PhysicalMinAggregate            PhysicalAggregateOperation = "MIN"
	PhysicalMaxAggregate            PhysicalAggregateOperation = "MAX"
	PhysicalFirstAggregate          PhysicalAggregateOperation = "FIRST"
)

type PhysicalAggregate struct {
	Source    PhysicalValue
	Operation PhysicalAggregateOperation
	Value     *PhysicalExpression
	Predicate *PhysicalPredicateExpression
}

type PhysicalPivotMap struct {
	Source       PhysicalValue
	ResourceType string
	// ItemSource and ItemResourceType describe a repeated backbone item that
	// owns the key/value pair. Keeping this scope in the physical operation is
	// what prevents key and value selectors from being flattened independently
	// (for example Observation.component[].code paired with the same
	// component's value[x]).
	ItemSource          spec.Selector
	ItemResourceType    string
	KeySelector         spec.Selector
	ValueSelector       spec.Selector
	ValueFallbacks      []spec.Selector
	StringifyValue      bool
	ColumnsBindKey      string
	FlattenSingleColumn bool
	PreparedKey         *PhysicalPreparedReference
	PreparedValue       *PhysicalPreparedReference
}

// PhysicalLookup is a typed, bounded lookup over one array-valued source.
// ItemVariable is introduced only while evaluating ItemKey and ItemValue;
// neither expression contains backend query text. MatchBindKey names the
// scalar bind containing the frozen key for this output projection.
//
// Source is intentionally an expression rather than a PhysicalValue because
// dynamic sources commonly select repeated values from a root payload. The
// optimizer can fingerprint identical sources and the renderer can keep the
// lookup shape identical across frontends.
type PhysicalLookup struct {
	Source       PhysicalExpression
	ItemVariable string
	ItemKey      PhysicalExpression
	ItemValue    PhysicalExpression
	MatchBindKey string
}

type PhysicalObjectLookup struct {
	ObjectVariable string
	KeyBindKey     string
}

type PhysicalMapReduction string

const (
	PhysicalMapFirst       PhysicalMapReduction = "FIRST"
	PhysicalMapFirstSorted PhysicalMapReduction = "FIRST_SORTED"
)

type PhysicalKeyedMap struct {
	Source         PhysicalExpression
	ItemVariable   string
	ItemKey        PhysicalExpression
	ItemValue      PhysicalExpression
	ValueFallbacks []PhysicalExpression
	Reduction      PhysicalMapReduction
	FlattenSource  bool
}

type PhysicalObjectKeys struct {
	ObjectVariable string
}

type PhysicalKeySet struct {
	Source       PhysicalExpression
	ItemVariable string
	ItemKey      PhysicalExpression
}

// PhysicalSlice is a bounded, stable, nested projection over a prior set.
// Limit remains a bind variable so callers cannot synthesize query literals.
type PhysicalSlice struct {
	Source       PhysicalValue
	Predicate    *PhysicalPredicateExpression
	Sort         *PhysicalExpression
	LimitBindKey string
	Projections  []PhysicalExpressionProjection
}

type PhysicalExpressionProjection struct {
	Name       string
	Expression PhysicalExpression
}

type PhysicalObject struct {
	Fields []PhysicalExpressionProjection
}

// PhysicalSet is a correlated, array-valued subplan. Captures are declared
// explicitly so validation can prevent accidental references to parent or
// future variables. The set variable becomes visible only after its subplan
// has validated successfully.
