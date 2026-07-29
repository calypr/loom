package semantic

import "github.com/calypr/loom/internal/dataframe/spec"

type (
	RowGrain           = spec.RowGrain
	ProjectionMode     = spec.ProjectionMode
	Cardinality        = spec.Cardinality
	RowIdentity        = spec.RowIdentity
	TraversalMatchMode = spec.TraversalMatchMode
	TypedFilter        = spec.TypedFilter
	Selector           = spec.Selector
	SelectorStep       = spec.SelectorStep
)

const (
	ProjectionScalar        = spec.ProjectionScalar
	ProjectionFirst         = spec.ProjectionFirst
	ProjectionArray         = spec.ProjectionArray
	ProjectionDistinctArray = spec.ProjectionDistinctArray
	CardinalityOptionalOne  = spec.CardinalityOptionalOne
	CardinalityMany         = spec.CardinalityMany
	TraversalMatchOptional  = spec.TraversalMatchOptional
	TraversalMatchRequired  = spec.TraversalMatchRequired
)

var (
	ParseSelector                  = spec.ParseSelector
	ValidateTypedFilterForResource = spec.ValidateTypedFilterForResource
	InferRowGrain                  = spec.InferRowGrain
	RootResourceForGrain           = spec.RootResourceForGrain
	ValidateRootGrain              = spec.ValidateRootGrain
	DefaultRowIdentity             = spec.DefaultRowIdentity
	ValidateProjection             = spec.ValidateProjection
)
