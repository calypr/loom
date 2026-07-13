package compiler

// The compiler facade keeps the historical compiler symbols available while
// request contracts live in the independent spec package. These aliases are
// intentionally mechanical: the compiler does not own request semantics.
import "github.com/calypr/loom/internal/dataframe/spec"

type (
	Builder             = spec.Builder
	TraversalStep       = spec.TraversalStep
	RepresentativeSlice = spec.RepresentativeSlice
	FieldSelect         = spec.FieldSelect
	PivotSelect         = spec.PivotSelect
	AggregateSelect     = spec.AggregateSelect
	RowGrain            = spec.RowGrain
	ProjectionMode      = spec.ProjectionMode
	Cardinality         = spec.Cardinality
	RowIdentity         = spec.RowIdentity
	TraversalMatchMode  = spec.TraversalMatchMode
	FilterOperator      = spec.FilterOperator
	FilterValueKind     = spec.FilterValueKind
	ArrayQuantifier     = spec.ArrayQuantifier
	CodeValue           = spec.CodeValue
	FilterValue         = spec.FilterValue
	TypedFilter         = spec.TypedFilter
	Selector            = spec.Selector
	SelectorStep        = spec.SelectorStep
	ContainsFilter      = spec.ContainsFilter
)

const (
	RowGrainResource               = spec.RowGrainResource
	RowGrainPatient                = spec.RowGrainPatient
	RowGrainSpecimen               = spec.RowGrainSpecimen
	RowGrainFile                   = spec.RowGrainFile
	RowGrainDiagnosis              = spec.RowGrainDiagnosis
	RowGrainObservation            = spec.RowGrainObservation
	RowGrainStudyEnrollment        = spec.RowGrainStudyEnrollment
	ProjectionScalar               = spec.ProjectionScalar
	ProjectionFirst                = spec.ProjectionFirst
	ProjectionArray                = spec.ProjectionArray
	ProjectionDistinctArray        = spec.ProjectionDistinctArray
	ProjectionAggregate            = spec.ProjectionAggregate
	ProjectionPivot                = spec.ProjectionPivot
	ProjectionExplode              = spec.ProjectionExplode
	CardinalityRequiredOne         = spec.CardinalityRequiredOne
	CardinalityOptionalOne         = spec.CardinalityOptionalOne
	CardinalityMany                = spec.CardinalityMany
	CardinalityUnknownObservedMany = spec.CardinalityUnknownObservedMany
	TraversalMatchOptional         = spec.TraversalMatchOptional
	TraversalMatchRequired         = spec.TraversalMatchRequired
	FilterEquals                   = spec.FilterEquals
	FilterNotEquals                = spec.FilterNotEquals
	FilterIn                       = spec.FilterIn
	FilterExists                   = spec.FilterExists
	FilterMissing                  = spec.FilterMissing
	FilterContains                 = spec.FilterContains
	FilterGreaterThan              = spec.FilterGreaterThan
	FilterGreaterEq                = spec.FilterGreaterEq
	FilterLessThan                 = spec.FilterLessThan
	FilterLessEq                   = spec.FilterLessEq
	FilterString                   = spec.FilterString
	FilterCode                     = spec.FilterCode
	FilterBoolean                  = spec.FilterBoolean
	FilterInteger                  = spec.FilterInteger
	FilterDecimal                  = spec.FilterDecimal
	FilterDate                     = spec.FilterDate
	FilterDateTime                 = spec.FilterDateTime
	QuantifierAny                  = spec.QuantifierAny
	QuantifierAll                  = spec.QuantifierAll
	QuantifierNone                 = spec.QuantifierNone
)

var (
	ParseSelector                  = spec.ParseSelector
	ValidateTypedFilterForResource = spec.ValidateTypedFilterForResource
	OperatorSupportsKind           = spec.OperatorSupportsKind
	InferRowGrain                  = spec.InferRowGrain
	RootResourceForGrain           = spec.RootResourceForGrain
	ValidateRootGrain              = spec.ValidateRootGrain
	DefaultRowIdentity             = spec.DefaultRowIdentity
	ValidateProjection             = spec.ValidateProjection
)
