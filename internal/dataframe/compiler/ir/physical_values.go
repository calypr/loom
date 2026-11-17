package ir

import (
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type PhysicalValue struct {
	Variable string
	Path     []string
	BindKey  string
}

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
	PhysicalValueExpression        PhysicalExpressionKind = "VALUE"
	PhysicalLiteralExpression      PhysicalExpressionKind = "LITERAL"
	PhysicalExtractExpression      PhysicalExpressionKind = "EXTRACT"
	PhysicalAggregateExpression    PhysicalExpressionKind = "AGGREGATE"
	PhysicalPivotExpression        PhysicalExpressionKind = "PIVOT_MAP"
	PhysicalOwnerRecordsExpression PhysicalExpressionKind = "OWNER_RECORDS"
	PhysicalSliceExpression        PhysicalExpressionKind = "SLICE"
	PhysicalObjectLookupExpression PhysicalExpressionKind = "OBJECT_LOOKUP"
	PhysicalKeyedMapExpression     PhysicalExpressionKind = "KEYED_MAP"
	PhysicalObjectKeysExpression   PhysicalExpressionKind = "OBJECT_KEYS"
	PhysicalKeySetExpression       PhysicalExpressionKind = "KEY_SET"
	PhysicalObjectExpression       PhysicalExpressionKind = "OBJECT"
	PhysicalSubplanExpression      PhysicalExpressionKind = "SUBPLAN"
	PhysicalCallExpression         PhysicalExpressionKind = "CALL"
	PhysicalRelatedFieldExpression PhysicalExpressionKind = "RELATED_FIELD"
)

type PhysicalSelectorExecutionMode string

const (
	PhysicalSelectorGeneric          PhysicalSelectorExecutionMode = "GENERIC"
	PhysicalSelectorDirectScalar     PhysicalSelectorExecutionMode = "DIRECT_SCALAR"
	PhysicalSelectorConditionalArray PhysicalSelectorExecutionMode = "CONDITIONAL_ARRAY"
)

type PhysicalExpression struct {
	Kind         PhysicalExpressionKind
	Cardinality  PhysicalCardinality
	NullBehavior PhysicalNullBehavior
	Value        *PhysicalValue
	Literal      *PhysicalLiteral
	Extract      *PhysicalExtract
	Aggregate    *PhysicalAggregate
	Pivot        *PhysicalPivotMap
	OwnerRecords *PhysicalOwnerRecords
	Slice        *PhysicalSlice
	ObjectLookup *PhysicalObjectLookup
	KeyedMap     *PhysicalKeyedMap
	ObjectKeys   *PhysicalObjectKeys
	KeySet       *PhysicalKeySet
	Object       *PhysicalObject
	Subplan      *PhysicalSubplan
	Call         *PhysicalCall
	RelatedField *PhysicalRelatedField
}

type PhysicalLiteral struct {
	BindKey string
}

type PhysicalCall struct {
	Name       string
	Args       []PhysicalExpression
	TargetKind string
}

type PhysicalRelatedField struct {
	DocumentID   PhysicalValue
	ResourceType string
	Path         []string
	PresenceOnly bool
}

type PhysicalExtract struct {
	Source            PhysicalValue
	ResourceType      string
	Selector          spec.Selector
	Fallbacks         []PhysicalSelectorFallback
	Distinct          bool
	ExecutionMode     PhysicalSelectorExecutionMode
	Prepared          *PhysicalPreparedReference
	UnitNormalization *PhysicalUnitNormalization
}

type PhysicalSelectorFallback struct {
	Source       PhysicalValue
	ResourceType string
	Selector     spec.Selector
}

type PhysicalUnitNormalization struct {
	OriginalValue spec.Selector
	SourceSystem  spec.Selector
	SourceCode    spec.Selector
	Target        unit.UnitIdentity
	Dimension     unit.UnitDimension
	Rules         []unit.UnitConversionRule
}

type PhysicalPreparedReference struct {
	SetVariable string
	Field       string
}

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
	PhysicalSumAggregate            PhysicalAggregateOperation = "SUM"
	PhysicalMeanAggregate           PhysicalAggregateOperation = "MEAN"
	PhysicalFirstAggregate          PhysicalAggregateOperation = "FIRST"
	PhysicalContainsAllAggregate    PhysicalAggregateOperation = "CONTAINS_ALL"
	PhysicalRequireOneAggregate     PhysicalAggregateOperation = "REQUIRE_ONE"
	PhysicalCollectAggregate        PhysicalAggregateOperation = "COLLECT"
	PhysicalFirstOrderedAggregate   PhysicalAggregateOperation = "FIRST_ORDERED"
)

type PhysicalContributorWindow struct {
	Timestamp      PhysicalExpression
	Anchor         PhysicalExpression
	LowerOffset    int64
	UpperOffset    int64
	LowerInclusive bool
	UpperInclusive bool
	Precision      string
}

type PhysicalTemporalOrdering struct {
	Timestamp PhysicalExpression
	Direction string
	TiePolicy string
}

type PhysicalAggregate struct {
	Source                PhysicalValue
	Operation             PhysicalAggregateOperation
	Value                 *PhysicalExpression
	Predicate             *PhysicalPredicateExpression
	RequiredValuesBindKey string
	ContributorWindow     *PhysicalContributorWindow
	Ordering              *PhysicalTemporalOrdering
}

type PhysicalPivotMap struct {
	Source              PhysicalValue
	ResourceType        string
	ItemSource          spec.Selector
	ItemResourceType    string
	KeySelector         spec.Selector
	ValueSelector       spec.Selector
	ValueFallbacks      []spec.Selector
	StringifyValue      bool
	ColumnsBindKey      string
	FlattenSingleColumn bool
	ColumnAliases       map[string]string
	ProjectionMode      string
	PreparedKey         *PhysicalPreparedReference
	PreparedValue       *PhysicalPreparedReference
	Correlation         *PhysicalCorrelation
}

type PhysicalOwnerRecords struct {
	Correlation        PhysicalCorrelation
	OwnerPathBindKey   string
	ChoiceArmBindKey   string
	LogicalTypeBindKey string
}

type PhysicalCorrelation struct {
	Source                PhysicalValue
	ResourceType          string
	OwnerResource         string
	OwnerSelector         spec.Selector
	ValueScope            fhirschema.CorrelatedValueScope
	SelectionScope        fhirschema.CorrelatedSelectionScope
	KeyResource           string
	KeySelector           spec.Selector
	KeyIsArray            bool
	VersionSelector       *spec.Selector
	SystemSelector        spec.Selector
	CodeSelector          spec.Selector
	ValueSelector         spec.Selector
	ValueFallbacks        []spec.Selector
	FallbackOnBlankString bool
	ChoiceArms            []string
	ChoiceSelectors       []spec.Selector
	LogicalType           string
	ValuePrimitive        string
	UnitSelector          *spec.Selector
	SystemBindKey         string
	CodeBindKey           string
	VersionBindKey        string
	ExtensionURLSelectors []spec.Selector
	ExtensionURLBindKeys  []string
}

type PhysicalObjectLookup struct {
	ObjectVariable string
	KeyBindKey     string
}

type PhysicalMapReduction string

const (
	PhysicalMapFirst       PhysicalMapReduction = "FIRST"
	PhysicalMapFirstSorted PhysicalMapReduction = "FIRST_SORTED"
	PhysicalMapAll         PhysicalMapReduction = "ALL"
	PhysicalMapDistinct    PhysicalMapReduction = "DISTINCT"
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
