package ir

import (
	"regexp"

	"github.com/calypr/loom/internal/dataframe/spec"
)

const PhysicalCodedPivotSourcePayloadColumn = "__loom_coded_pivot_source_payload"

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

// PhysicalPopulationMappingReturn is an internal final-row witness terminal.
// Members must be the matched selected-member array for the current root row.
// IdentityParts preserve the ordered default identity inputs; ExplicitIdentity
// carries a recipe-defined __loom_row_id expression when present. The
// renderer emits one witness row per (member, identity) pair without changing
// the ordinary return schema.
type PhysicalPopulationMappingReturn struct {
	Members          PhysicalExpression
	IdentityParts    []PhysicalPopulationMappingIdentityPart
	ExplicitIdentity *PhysicalExpression
}

type PhysicalPopulationMappingIdentityPart struct {
	Name       string
	Expression PhysicalExpression
}

// PhysicalCellTraceReturn is an internal evidence terminal. Value is the
// exact final projection expression. The renderer derives bounded source
// contributors only from this closed expression tree; it never evaluates the
// FHIR document independently of the compiled plan.
type PhysicalCellTraceReturn struct {
	Value             PhysicalExpression
	Contribution      *PhysicalCellTraceContribution
	Reshape           *PhysicalCellTraceReshape
	Construction      *PhysicalCellTraceConstruction
	IdentityParts     []PhysicalPopulationMappingIdentityPart
	ExplicitIdentity  *PhysicalExpression
	OffsetBindKey     string
	LimitBindKey      string
	FetchLimitBindKey string
	OmissionCode      string
}

// PhysicalCellTraceConstruction describes value lineage through an ordered
// construction sequence. The renderer evaluates InputColumns on the selected
// final row and attaches the stable stage and column identities to each item.
type PhysicalCellTraceConstruction struct {
	FinalStageID      string
	RowIdentityColumn string
	// RowIdentityFields preserve the ordered public identity contract used by
	// Preview. Construction stages can keep a narrower internal row identity
	// column (for example _key) while the public identity also includes the
	// project bind.
	RowIdentityFields []string
	OutputColumnID    string
	OutputColumn      string
	ProducerStageID   string
	ConstructionID    string
	Operation         string
	Inputs            []PhysicalCellTraceConstructionInput
	RelatedSource     *PhysicalCellTraceRelatedSource
	OmissionCode      string
}

// PhysicalCellTraceConstructionInput identifies a column read by the
// construction step that produced the requested cell. FinalValueColumn is the
// surviving name for that stable column ID in the final stage schema.
type PhysicalCellTraceConstructionInput struct {
	StageID          string
	ColumnID         string
	Column           string
	FinalValueColumn string
}

// PhysicalCellTraceRelatedSource replays the compiler-owned route subplan for
// the exact final row. Each traversed path remains one contribution occurrence.
type PhysicalCellTraceRelatedSource struct {
	InputRowVariable string
	AnchorColumn     string
	ResourceType     string
	Subplan          PhysicalSubplan
}

// PhysicalTableShapeExclusionReturn emits source-level exclusions from the
// input rows of one compiler-validated grouped pivot.
type PhysicalTableShapeExclusionReturn struct {
	Pivot             PhysicalGroupedPivot
	OffsetBindKey     string
	LimitBindKey      string
	FetchLimitBindKey string
}

// PhysicalCellTraceReshape carries the exact source-cell lineage needed to
// explain a table-shape output. It exists only on the diagnostic terminal;
// ordinary dataframe rows and schemas remain unchanged.
type PhysicalCellTraceReshape struct {
	OutputVariable string
	Sources        []PhysicalCellTraceReshapeSource
	OmissionCode   string
}

type PhysicalCellTraceReshapeSourceKind string

const (
	PhysicalCellTracePivotGroupKey PhysicalCellTraceReshapeSourceKind = "PIVOT_GROUP_KEY"
	PhysicalCellTracePivotCell     PhysicalCellTraceReshapeSourceKind = "PIVOT_CELL"
	PhysicalCellTraceUnpivotValue  PhysicalCellTraceReshapeSourceKind = "UNPIVOT_VALUE"
	PhysicalCellTraceUnpivotField  PhysicalCellTraceReshapeSourceKind = "UNPIVOT_PASSTHROUGH"
)

// PhysicalCellTraceReshapeSource identifies one source expression that
// contributes to the requested reshaped cell. Pivot sources use the grouped
// rows unless lowering proves one input row per group; unpivot sources are
// carried on the emitted row.
type PhysicalCellTraceReshapeSource struct {
	Kind                  PhysicalCellTraceReshapeSourceKind
	GroupRowsVariable     string
	InputRowVariable      string
	OneInputRowPerGroup   bool
	SourceColumn          string
	SourcePresenceField   string
	CategoryColumn        string
	CategoryPresenceField string
	CategoryType          string
	Category              *PhysicalGroupedPivotCategory
	ValueColumn           string
	ValueColumnType       string
	DuplicatePolicy       string
	OmissionCode          string
}

const (
	PhysicalCellTraceSourceDocumentField      = "__loom_trace_source_document"
	PhysicalCellTraceSourcePresencePrefix     = "__loom_trace_source_presence_"
	PhysicalCellTraceSourcePresenceField      = "__loom_trace_source_presence"
	PhysicalCellTraceSourceSupportedField     = "__loom_trace_source_supported"
	PhysicalCellTracePassSourceDocumentField  = "__loom_trace_pass_source_document"
	PhysicalCellTracePassSourcePresenceField  = "__loom_trace_pass_source_presence"
	PhysicalCellTracePassSourceSupportedField = "__loom_trace_pass_source_supported"
	PhysicalCellTraceGeneratedKeyOmission     = "TABLE_SHAPE_GENERATED_KEY_HAS_NO_SOURCE_CELL"
	PhysicalCellTraceSourceOmission           = "TABLE_SHAPE_SOURCE_LINEAGE_UNAVAILABLE"
	PhysicalCellTraceDerivedOmission          = "TABLE_SHAPE_DERIVED_LINEAGE_UNAVAILABLE"
	PhysicalCellTraceDerivedLiteralOnly       = "TABLE_SHAPE_DERIVED_HAS_NO_SOURCE_CELL"
)

// PhysicalCellTraceContribution points at the pre-reduction projected set
// already produced by canonical lowering. ValueField is compiler-generated,
// never supplied by a request.
type PhysicalCellTraceContribution struct {
	SetVariable string
	ValueField  string
	Lossy       bool
}

const (
	PhysicalCellTraceValueField            = "__loom_trace_value"
	PhysicalCellTraceContributionsField    = "__loom_trace_contributions"
	PhysicalCellTraceIdentityPartsField    = "__loom_trace_identity_parts"
	PhysicalCellTraceExplicitIdentityField = "__loom_trace_explicit_identity"
	PhysicalCellTraceStatusField           = "__loom_trace_status"
	PhysicalCellTraceHasMoreField          = "__loom_trace_has_more"
	PhysicalCellTraceOmissionField         = "__loom_trace_omission"
)

const (
	PhysicalTableShapeExclusionResourceTypeField         = "__loom_table_shape_exclusion_resource_type"
	PhysicalTableShapeExclusionResourceIDField           = "__loom_table_shape_exclusion_resource_id"
	PhysicalTableShapeExclusionIdentityStatusField       = "__loom_table_shape_exclusion_identity_status"
	PhysicalTableShapeExclusionCategoryValueField        = "__loom_table_shape_exclusion_category_value"
	PhysicalTableShapeExclusionCategoryPresentField      = "__loom_table_shape_exclusion_category_present"
	PhysicalTableShapeExclusionCategoryTypeField         = "__loom_table_shape_exclusion_category_type"
	PhysicalTableShapeExclusionOutputRowIDField          = "__loom_table_shape_exclusion_output_row_id"
	PhysicalTableShapeExclusionReasonField               = "__loom_table_shape_exclusion_reason"
	PhysicalTableShapeExclusionOmissionField             = "__loom_table_shape_exclusion_omission"
	PhysicalTableShapeExclusionReasonUnlistedCategory    = "UNLISTED_CATEGORY"
	PhysicalTableShapeExclusionIdentityExact             = "EXACT"
	PhysicalTableShapeExclusionIdentityUnavailable       = "UNAVAILABLE"
	PhysicalTableShapeExclusionSourceIdentityUnavailable = "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"
)

const (
	PhysicalPopulationMappingMemberField           = "__loom_population_member"
	PhysicalPopulationMappingIdentityPartsField    = "__loom_population_identity_parts"
	PhysicalPopulationMappingExplicitIdentityField = "__loom_population_explicit_identity"
)

// PhysicalUnnestEmptyPolicy controls null/empty source behavior. The renderer
// owns the AQL shape; this IR never stores a query fragment.
type PhysicalUnnestEmptyPolicy string

const (
	PhysicalUnnestError          PhysicalUnnestEmptyPolicy = "ERROR"
	PhysicalUnnestExclude        PhysicalUnnestEmptyPolicy = "EXCLUDE"
	PhysicalUnnestPreserveParent PhysicalUnnestEmptyPolicy = "PRESERVE_PARENT"
)

// PhysicalUnnestOwner describes the exact root-to-owner route evaluated before
// the selected repeated value is expanded. An empty Route means the owner is
// the root document.
type PhysicalUnnestOwner struct {
	OccurrenceID  string
	ResourceType  string
	RootVariable  string
	OwnerVariable string
	Route         []PhysicalUnnestRouteStep
}

type PhysicalUnnestRouteStep struct {
	OccurrenceID string
	Traversal    PhysicalTraversal
	Scope        []PhysicalOperation
}

// PhysicalUnnestAncestor retains one repeated item traversed on the way to
// the selected item. It lets correlated projections read sibling fields from
// the exact enclosing item that produced each expanded row.
type PhysicalUnnestAncestor struct {
	StepIndex int
	Variable  string
}

// PhysicalUnnest is the single enriched cardinality boundary. It carries the
// typed owner route, source, empty policy, compiler-owned item evidence, and
// any repeated ancestors needed to keep nested projections correlated.
type PhysicalUnnest struct {
	Owner           PhysicalUnnestOwner
	OutputVariable  string
	Ordinality      string
	HasItemVariable string
	Expression      PhysicalExpression
	Ancestors       []PhysicalUnnestAncestor
	EmptyPolicy     PhysicalUnnestEmptyPolicy
}

// PhysicalUnnestSortKeys is the compiler-owned stable ordering tuple for an
// expanded row window. Route edges are preserved in route order so two graph
// paths to the same owner remain distinct ordered witnesses.
func PhysicalUnnestSortKeys(unnest PhysicalUnnest) []PhysicalValue {
	keys := []PhysicalValue{{Variable: unnest.Owner.RootVariable, Path: []string{"_key"}}}
	for _, step := range unnest.Owner.Route {
		if step.Traversal.EdgeVariable != "" {
			keys = append(keys, PhysicalValue{Variable: step.Traversal.EdgeVariable, Path: []string{"_key"}})
		}
	}
	if unnest.Owner.OwnerVariable != unnest.Owner.RootVariable {
		keys = append(keys, PhysicalValue{Variable: unnest.Owner.OwnerVariable, Path: []string{"_key"}})
	}
	keys = append(keys, PhysicalValue{Variable: unnest.HasItemVariable})
	if unnest.Ordinality != "" {
		keys = append(keys, PhysicalValue{Variable: unnest.Ordinality})
	}
	return keys
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
	// Sort makes an array-valued subplan projection deterministic. It is
	// validated in the subplan's local scope and is absent from EXISTS plans.
	Sort   *PhysicalValue
	Unique bool
	// DistinctBy groups projected values by one exact identity while preserving
	// equal values from different identities. It is used for related ALL forms.
	DistinctBy *PhysicalValue
}

// PhysicalCollectionScan reads a compiler-provided collection inside a
// correlated predicate subplan. It is used for indexed immutable membership
// joins and is not legal as a top-level root scan.
type PhysicalCollectionScan struct {
	Variable          string
	CollectionBindKey string
}

// PhysicalDocumentLookup resolves an exact hidden _id in one compiler-chosen
// resource collection. The renderer verifies the resolved _id still equals
// the captured value before later scope filters or traversals can use it.
type PhysicalDocumentLookup struct {
	Variable          string
	CollectionBindKey string
	ExactID           PhysicalValue
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
	Correlation    *PhysicalCorrelation
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

// PhysicalSort carries one or more typed keys for a deterministic execution
// window. Key order is semantic: expanded rows include their occurrence and
// owner-relative item witnesses after the root identity.
type PhysicalSort struct {
	Keys []PhysicalValue
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
	// Presence preserves whether a source property existed before projection.
	// It is metadata for row operations such as grouped pivot and is not itself
	// emitted in the ordinary public projection.
	Presence *PhysicalProjectionPresence
}

// PhysicalProjectionPresence is a closed property-presence proof for one or
// more concrete selector paths rooted at the same physical value. Any path
// being present makes the projected value present (for example, a selector
// fallback that supplies the final value).
type PhysicalProjectionPresence struct {
	Source PhysicalValue
	Paths  [][]string
}

type PhysicalReturn struct {
	Projections []PhysicalProjection
}

// PhysicalGroupedPivot is a typed row operation over one materialized base
// row projection. COLLECT forms groups; categories remain frozen values and
// never become query fragments.
type PhysicalGroupedPivot struct {
	ConstructionID    string
	InputRowVariable  string
	GroupRowsVariable string
	OutputRowVariable string
	// OneInputRowPerGroup is set only when lowering proves that a direct root
	// resource ID is among the group keys and the source plan cannot multiply
	// root rows. Renderers can then emit one pivot row per input without COLLECT.
	OneInputRowPerGroup         bool
	InputProjections            []PhysicalProjection
	GroupKeys                   []PhysicalGroupedPivotKey
	CategoryColumn              string
	CategoryPresenceColumn      string
	CategoryPresence            *PhysicalProjectionPresence
	CategoryType                string
	ValueColumn                 string
	ValueType                   string
	Categories                  []PhysicalGroupedPivotCategory
	RowValues                   []PhysicalStageRowValue
	RootContributorInputColumn  string
	RootContributorInputMany    bool
	RootContributorOutputColumn string
	RootContributorVariable     string
	// CodedCorrelation is present for construction CODED_PIVOT. The renderer
	// substitutes each coded category's bound system/code pair into this one
	// checked owner/key/value binding, keeping category identity and value in
	// the same owner scope.
	CodedCorrelation *PhysicalCorrelation
	CodedCategories  []PhysicalGroupedCodedPivotCategory
	// CodedSourceVariable is the selected direct root scan variable. The first
	// construction stage inlines that scan so it never materializes full root
	// payloads into an intermediate array.
	CodedSourceVariable    string
	ConstructionIDBindKey  string
	UnlistedEvidenceColumn string
	DuplicatePolicy        string
	MissingCellPolicy      string
	UnlistedCategoryPolicy string
}

type PhysicalGroupedPivotKey struct {
	Column string
	// Output permits a construction stage to rename a pass-through group key
	// while still resolving the key against the preceding stage's schema.
	Output   string
	Variable string
	Kind     string
	Hidden   bool
}

type PhysicalGroupedPivotCategory struct {
	Output       string
	MatchKind    PhysicalPivotCategoryMatchKind
	ValueBindKey string
	ValueKind    string
}

type PhysicalGroupedCodedPivotCategory struct {
	Output        string
	SystemBindKey string
	CodeBindKey   string
}

type PhysicalPivotCategoryMatchKind string

const (
	PhysicalPivotCategoryValueMatch   PhysicalPivotCategoryMatchKind = "VALUE"
	PhysicalPivotCategoryNullMatch    PhysicalPivotCategoryMatchKind = "NULL"
	PhysicalPivotCategoryMissingMatch PhysicalPivotCategoryMatchKind = "MISSING"
)

// PhysicalUnpivot is a terminal cardinality-changing row operation. Input
// order defines emitted key/value rows; identity columns are copied from the
// input row before the selected value key is appended.
type PhysicalUnpivot struct {
	ConstructionID        string
	InputRowVariable      string
	SlotVariable          string
	OutputRowVariable     string
	InputProjections      []PhysicalProjection
	Inputs                []PhysicalUnpivotInput
	PreservedOutputs      []PhysicalUnpivotOutput
	KeyOutput             string
	KeyType               string
	ValueOutput           string
	ValueType             string
	IdentityParts         []PhysicalUnpivotIdentityPart
	ConstructionIDBindKey string
	NullRowPolicy         string
}

// PhysicalUnpivotOutput preserves one input value under its stage output
// name. Empty PreservedOutputs retains the input name for legacy reshapes.
type PhysicalUnpivotOutput struct {
	InputColumn  string
	OutputColumn string
}

type PhysicalUnpivotInput struct {
	Column     string
	KeyBindKey string
	KeyKind    string
	ValueKind  string
}

type PhysicalUnpivotIdentityPart struct {
	Name  string
	Value PhysicalValue
}

var (
	physicalVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	physicalBindKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	physicalPathPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate enforces the frozen physical-plan invariants without rendering AQL.
