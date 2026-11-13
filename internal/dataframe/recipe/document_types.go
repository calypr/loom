package recipe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/dataframe/unit"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const CurrentSchemaVersion = 1

const (
	maxExpressionDepth = 64
	maxExpressionNodes = 4096
	maxLiteralArray    = 256
)

type Bundle struct {
	RecipeSchemaVersion int              `json:"recipeSchemaVersion"`
	Name                string           `json:"name"`
	TranslationVersion  string           `json:"translationVersion"`
	Fragments           *FragmentLibrary `json:"fragments,omitempty"`
	Outputs             []Output         `json:"outputs"`
}

type Output struct {
	Name                  string                  `json:"name"`
	RootResourceType      string                  `json:"rootResourceType"`
	RootOccurrenceID      string                  `json:"rootOccurrenceId,omitempty"`
	RowGrain              string                  `json:"rowGrain"`
	RootColumnNaming      RootColumnNaming        `json:"rootColumnNaming,omitempty"`
	TraversalColumnNaming TraversalColumnNaming   `json:"traversalColumnNaming,omitempty"`
	Fields                []Field                 `json:"fields,omitempty"`
	Filters               []Filter                `json:"filters,omitempty"`
	Pivots                []Pivot                 `json:"pivots,omitempty"`
	OwnerRecords          []OwnerRecordProjection `json:"ownerRecords,omitempty"`
	Aggregates            []Aggregate             `json:"aggregates,omitempty"`
	Slices                []RepresentativeSlice   `json:"slices,omitempty"`
	Traversals            []Traversal             `json:"traversals,omitempty"`
	TableReshape          *TableReshape           `json:"tableReshape,omitempty"`
	Expand                *Expansion              `json:"expand,omitempty"`
	GroupRows             *GroupRows              `json:"groupRows,omitempty"`
	Identity              *Identity               `json:"identity,omitempty"`
	DynamicColumns        []DynamicColumn         `json:"dynamicColumns,omitempty"`
	ExtensionColumns      []ExtensionColumn       `json:"extensionColumns,omitempty"`
	CatalogProjections    []CatalogProjection     `json:"catalogProjections,omitempty"`
	ColumnTransformations []ColumnTransformation  `json:"columnTransformations,omitempty"`
	DerivedColumns        []DerivedColumn         `json:"derivedColumns,omitempty"`
	Construction          *Construction           `json:"construction,omitempty"`
	Population            *PopulationConstraint   `json:"population,omitempty"`
	CollisionPolicy       string                  `json:"collisionPolicy,omitempty"`
}

type ColumnTransformation struct {
	Column         string                              `json:"column"`
	Transformation columntransform.ValueTransformation `json:"transformation"`
}

type GroupRows struct {
	RevisionID             string                `json:"revisionId"`
	UnassignedMemberPolicy string                `json:"unassignedMemberPolicy"`
	AfterStepID            string                `json:"afterStepId,omitempty"`
	RowValues              []GroupRowValuePolicy `json:"rowValues,omitempty"`
}

type GroupRowValuePolicy struct {
	ColumnID string                     `json:"columnId"`
	Policy   ConstructionRowValuePolicy `json:"policy"`
}

type PopulationConstraint struct {
	SelectionRevisionID string                `json:"selectionRevisionId"`
	MembershipDigest    string                `json:"membershipDigest"`
	MemberCount         int64                 `json:"memberCount"`
	ResourceType        string                `json:"resourceType"`
	Route               []PopulationRouteStep `json:"route,omitempty"`
}

type PopulationRouteStep struct {
	ResourceType     string `json:"resourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection,omitempty"`
}

type RootColumnNaming string

const (
	RootColumnNamingPrefixed RootColumnNaming = "PREFIXED"
	RootColumnNamingExact    RootColumnNaming = "EXACT"
)

func (n RootColumnNaming) Valid() bool {
	return n == "" || n == RootColumnNamingPrefixed || n == RootColumnNamingExact
}

func (n RootColumnNaming) Normalized() RootColumnNaming {
	if n == "" {
		return RootColumnNamingPrefixed
	}
	return n
}

type TraversalColumnNaming string

const (
	TraversalColumnNamingPath  TraversalColumnNaming = "PATH"
	TraversalColumnNamingAlias TraversalColumnNaming = "ALIAS"
	TraversalColumnNamingExact TraversalColumnNaming = "EXACT"
)

func (n TraversalColumnNaming) Valid() bool {
	return n == "" || n == TraversalColumnNamingPath || n == TraversalColumnNamingAlias || n == TraversalColumnNamingExact
}

func (n TraversalColumnNaming) Normalized() TraversalColumnNaming {
	if n == "" {
		return TraversalColumnNamingPath
	}
	return n
}

type Field struct {
	Name       string       `json:"name"`
	ColumnID   string       `json:"columnId,omitempty"`
	Label      string       `json:"label,omitempty"`
	FieldRef   string       `json:"fieldRef,omitempty"`
	Expr       Expression   `json:"expr"`
	Fallbacks  []Expression `json:"fallbacks,omitempty"`
	ValueMode  ValueMode    `json:"valueMode,omitempty"`
	Discovered bool         `json:"-"`
}

type ValueMode string

const (
	ValueModeAuto     ValueMode = "AUTO"
	ValueModeFirst    ValueMode = "FIRST"
	ValueModeAll      ValueMode = "ALL"
	ValueModeDistinct ValueMode = "DISTINCT"
)

func (m ValueMode) Valid() bool {
	return m == "" || m == ValueModeAuto || m == ValueModeFirst || m == ValueModeAll || m == ValueModeDistinct
}

func (m ValueMode) Normalized() ValueMode {
	if m == "" {
		return ValueModeAuto
	}
	return m
}

type Filter struct {
	Select     string          `json:"select"`
	FieldRef   string          `json:"fieldRef,omitempty"`
	Operator   FilterOperator  `json:"operator"`
	Quantifier ArrayQuantifier `json:"quantifier,omitempty"`
	Values     []FilterValue   `json:"values,omitempty"`
}

type FilterOperator string

const (
	FilterEquals      FilterOperator = "EQUALS"
	FilterNotEquals   FilterOperator = "NOT_EQUALS"
	FilterIn          FilterOperator = "IN"
	FilterExists      FilterOperator = "EXISTS"
	FilterMissing     FilterOperator = "MISSING"
	FilterContains    FilterOperator = "CONTAINS_TEXT"
	FilterGreaterThan FilterOperator = "GT"
	FilterGreaterEq   FilterOperator = "GTE"
	FilterLessThan    FilterOperator = "LT"
	FilterLessEq      FilterOperator = "LTE"
)

func (op FilterOperator) Valid() bool {
	switch op {
	case FilterEquals, FilterNotEquals, FilterIn, FilterExists, FilterMissing,
		FilterContains, FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
		return true
	default:
		return false
	}
}

type ArrayQuantifier string

const (
	QuantifierAny  ArrayQuantifier = "ANY"
	QuantifierAll  ArrayQuantifier = "ALL"
	QuantifierNone ArrayQuantifier = "NONE"
)

func (q ArrayQuantifier) Valid() bool {
	return q == QuantifierAny || q == QuantifierAll || q == QuantifierNone
}

type FilterValueKind string

const (
	FilterString   FilterValueKind = "STRING"
	FilterCode     FilterValueKind = "CODE"
	FilterBoolean  FilterValueKind = "BOOLEAN"
	FilterInteger  FilterValueKind = "INTEGER"
	FilterDecimal  FilterValueKind = "DECIMAL"
	FilterDate     FilterValueKind = "DATE"
	FilterDateTime FilterValueKind = "DATE_TIME"
)

func (kind FilterValueKind) Valid() bool {
	switch kind {
	case FilterString, FilterCode, FilterBoolean, FilterInteger, FilterDecimal, FilterDate, FilterDateTime:
		return true
	default:
		return false
	}
}

type CodeValue struct {
	System  string `json:"system,omitempty"`
	Code    string `json:"code"`
	Display string `json:"display,omitempty"`
	Version string `json:"version,omitempty"`
}

type FilterValue struct {
	Kind     FilterValueKind `json:"kind"`
	String   *string         `json:"string,omitempty"`
	Code     *CodeValue      `json:"code,omitempty"`
	Boolean  *bool           `json:"boolean,omitempty"`
	Integer  *int64          `json:"integer,omitempty"`
	Decimal  *float64        `json:"decimal,omitempty"`
	Date     *string         `json:"date,omitempty"`
	DateTime *string         `json:"dateTime,omitempty"`
}

type Pivot struct {
	Name                 string                        `json:"name"`
	FieldRef             string                        `json:"fieldRef,omitempty"`
	ColumnExpr           Expression                    `json:"columnExpr"`
	ValueExpr            Expression                    `json:"valueExpr"`
	ValueFallbacks       []Expression                  `json:"valueFallbacks,omitempty"`
	ItemSource           Expression                    `json:"itemSource,omitempty"`
	ItemResourceType     string                        `json:"itemResourceType,omitempty"`
	Columns              []string                      `json:"columns"`
	ColumnAliases        map[string]string             `json:"columnAliases,omitempty"`
	ProjectionMode       string                        `json:"projectionMode,omitempty"`
	Discovery            *PivotDiscovery               `json:"discovery,omitempty"`
	Correlation          *fhirschema.CorrelatedBinding `json:"correlation,omitempty"`
	CorrelationSystem    string                        `json:"correlationSystem,omitempty"`
	CorrelationCode      string                        `json:"correlationCode,omitempty"`
	CorrelationVersion   string                        `json:"correlationVersion,omitempty"`
	ExtensionCorrelation *fhirschema.ExtensionBinding  `json:"extensionCorrelation,omitempty"`
	Discovered           bool                          `json:"-"`
}

type OwnerRecordProjection struct {
	Name     string                       `json:"name"`
	FieldRef string                       `json:"fieldRef,omitempty"`
	Binding  fhirschema.CorrelatedBinding `json:"binding"`
	Key      fhirschema.CorrelatedKey     `json:"key"`
}

const (
	PivotProjectionValue    = "VALUE"
	PivotProjectionFirst    = "FIRST"
	PivotProjectionAll      = "ALL"
	PivotProjectionDistinct = "DISTINCT"
)

func ValidPivotProjectionMode(mode string) bool {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "", PivotProjectionValue, PivotProjectionFirst, PivotProjectionAll, PivotProjectionDistinct:
		return true
	default:
		return false
	}
}

func NormalizedPivotProjectionMode(mode string) string {
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode == "" {
		return PivotProjectionFirst
	}
	return mode
}

func (p Pivot) MarshalJSON() ([]byte, error) {
	type pivotJSON struct {
		Name                 string                        `json:"name"`
		FieldRef             string                        `json:"fieldRef,omitempty"`
		ColumnExpr           *Expression                   `json:"columnExpr,omitempty"`
		ValueExpr            *Expression                   `json:"valueExpr,omitempty"`
		ValueFallbacks       []Expression                  `json:"valueFallbacks,omitempty"`
		ItemSource           *Expression                   `json:"itemSource,omitempty"`
		ItemResourceType     string                        `json:"itemResourceType,omitempty"`
		Columns              []string                      `json:"columns"`
		ColumnAliases        map[string]string             `json:"columnAliases,omitempty"`
		ProjectionMode       string                        `json:"projectionMode,omitempty"`
		Discovery            *PivotDiscovery               `json:"discovery,omitempty"`
		Correlation          *fhirschema.CorrelatedBinding `json:"correlation,omitempty"`
		CorrelationSystem    string                        `json:"correlationSystem,omitempty"`
		CorrelationCode      string                        `json:"correlationCode,omitempty"`
		CorrelationVersion   string                        `json:"correlationVersion,omitempty"`
		ExtensionCorrelation *fhirschema.ExtensionBinding  `json:"extensionCorrelation,omitempty"`
	}
	wire := pivotJSON{Name: p.Name, FieldRef: p.FieldRef, ValueFallbacks: p.ValueFallbacks, ItemResourceType: p.ItemResourceType, Columns: p.Columns, ColumnAliases: p.ColumnAliases, ProjectionMode: p.ProjectionMode, Discovery: p.Discovery, Correlation: p.Correlation, CorrelationSystem: p.CorrelationSystem, CorrelationCode: p.CorrelationCode, CorrelationVersion: p.CorrelationVersion, ExtensionCorrelation: p.ExtensionCorrelation}
	if p.Correlation == nil && p.ExtensionCorrelation == nil && (p.Discovery == nil || !p.ColumnExpr.zero()) {
		value := p.ColumnExpr
		wire.ColumnExpr = &value
	}
	if p.Correlation == nil && p.ExtensionCorrelation == nil && (p.Discovery == nil || !p.ValueExpr.zero()) {
		value := p.ValueExpr
		wire.ValueExpr = &value
	}
	if !p.ItemSource.zero() {
		value := p.ItemSource
		wire.ItemSource = &value
	}
	return json.Marshal(wire)
}

func (e Expression) zero() bool {
	return e.Select == "" && e.Call == "" && e.Literal == nil && e.Document == nil && len(e.Args) == 0
}

type PivotDiscovery struct {
	Family     string `json:"family,omitempty"`
	Path       string `json:"path,omitempty"`
	MaxColumns int    `json:"maxColumns"`
}

type AggregateOperation string

const (
	AggregateCount          AggregateOperation = "COUNT"
	AggregateCountDistinct  AggregateOperation = "COUNT_DISTINCT"
	AggregateExists         AggregateOperation = "EXISTS"
	AggregateDistinctValues AggregateOperation = "DISTINCT_VALUES"
	AggregateMin            AggregateOperation = "MIN"
	AggregateMax            AggregateOperation = "MAX"
	AggregateSum            AggregateOperation = "SUM"
	AggregateMean           AggregateOperation = "MEAN"
	AggregateContainsAll    AggregateOperation = "CONTAINS_ALL"
	AggregateRequireOne     AggregateOperation = "REQUIRE_ONE"
	AggregateCollect        AggregateOperation = "COLLECT"
	AggregateFirstOrdered   AggregateOperation = "FIRST_ORDERED"
)

func (op AggregateOperation) Valid() bool {
	switch op {
	case AggregateCount, AggregateCountDistinct, AggregateExists, AggregateDistinctValues, AggregateMin, AggregateMax, AggregateSum, AggregateMean, AggregateContainsAll, AggregateRequireOne, AggregateCollect, AggregateFirstOrdered:
		return true
	default:
		return false
	}
}

type TemporalDirection string

const (
	TemporalAscending  TemporalDirection = "ASC"
	TemporalDescending TemporalDirection = "DESC"
)

type TemporalPrecision string

const TemporalPrecisionInstant TemporalPrecision = "INSTANT"

type TemporalTiePolicy string

const (
	TemporalTieRequireUnique TemporalTiePolicy = "REQUIRE_UNIQUE"
	TemporalTieResourceKey   TemporalTiePolicy = "RESOURCE_KEY"
)

type ContributorWindow struct {
	Timestamp      Expression        `json:"timestamp"`
	Anchor         Expression        `json:"anchor"`
	LowerOffset    int64             `json:"lowerOffsetSeconds"`
	UpperOffset    int64             `json:"upperOffsetSeconds"`
	LowerInclusive bool              `json:"lowerInclusive"`
	UpperInclusive bool              `json:"upperInclusive"`
	Precision      TemporalPrecision `json:"precision"`
}

type TemporalOrdering struct {
	Timestamp Expression        `json:"timestamp"`
	Direction TemporalDirection `json:"direction"`
	TiePolicy TemporalTiePolicy `json:"tiePolicy"`
}

type Aggregate struct {
	Name              string                   `json:"name"`
	OutputName        string                   `json:"outputName,omitempty"`
	Operation         AggregateOperation       `json:"operation"`
	FieldRef          string                   `json:"fieldRef,omitempty"`
	Expr              *Expression              `json:"expr,omitempty"`
	Where             *Filter                  `json:"where,omitempty"`
	ValueMode         ValueMode                `json:"valueMode,omitempty"`
	RequiredValues    []string                 `json:"requiredValues,omitempty"`
	ContributorWindow *ContributorWindow       `json:"contributorWindow,omitempty"`
	Ordering          *TemporalOrdering        `json:"ordering,omitempty"`
	UnitNormalization *UnitNormalizationPolicy `json:"unitNormalization,omitempty"`
}

type UnitNormalizationPolicy struct {
	SystemPath string                   `json:"systemPath"`
	CodePath   string                   `json:"codePath"`
	Target     unit.UnitIdentity        `json:"target"`
	Rules      []unit.UnitRuleReference `json:"rules"`
}

func (p UnitNormalizationPolicy) Validate() error {
	if !p.Target.Valid() {
		return fmt.Errorf("unit normalization target requires system and code")
	}
	if strings.TrimSpace(p.SystemPath) == "" || strings.TrimSpace(p.CodePath) == "" {
		return fmt.Errorf("unit normalization requires systemPath and codePath")
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("unit normalization requires at least one approved conversion rule")
	}
	seen := make(map[string]struct{}, len(p.Rules))
	for index, rule := range p.Rules {
		if err := unit.ValidateUnitRuleReference(rule); err != nil {
			return fmt.Errorf("rules[%d]: %w", index, err)
		}
		key := strings.TrimSpace(rule.ID) + "\x00" + strings.TrimSpace(rule.Version)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("rules[%d] duplicates source unit identity", index)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type RepresentativeSlice struct {
	Name   string  `json:"name"`
	Where  *Filter `json:"where,omitempty"`
	Limit  int     `json:"limit"`
	Fields []Field `json:"fields"`
}

type TraversalMatchMode string

const (
	MatchOptional TraversalMatchMode = "OPTIONAL"
	MatchRequired TraversalMatchMode = "REQUIRED"
)

func (m TraversalMatchMode) Valid() bool {
	return m == "" || m == MatchOptional || m == MatchRequired || strings.EqualFold(string(m), string(MatchOptional)) || strings.EqualFold(string(m), string(MatchRequired))
}

func (m TraversalMatchMode) Normalized() TraversalMatchMode {
	switch strings.ToUpper(string(m)) {
	case string(MatchRequired):
		return MatchRequired
	default:
		return MatchOptional
	}
}

type Traversal struct {
	Name               string                  `json:"name"`
	OccurrenceID       string                  `json:"occurrenceId,omitempty"`
	ToResourceType     string                  `json:"toResourceType"`
	Alias              string                  `json:"alias,omitempty"`
	From               *Expression             `json:"from,omitempty"`
	MatchMode          TraversalMatchMode      `json:"matchMode,omitempty"`
	Fields             []Field                 `json:"fields,omitempty"`
	Filters            []Filter                `json:"filters,omitempty"`
	Pivots             []Pivot                 `json:"pivots,omitempty"`
	OwnerRecords       []OwnerRecordProjection `json:"ownerRecords,omitempty"`
	Aggregates         []Aggregate             `json:"aggregates,omitempty"`
	Slices             []RepresentativeSlice   `json:"slices,omitempty"`
	Traversals         []Traversal             `json:"traversals,omitempty"`
	DynamicColumns     []DynamicColumn         `json:"dynamicColumns,omitempty"`
	ExtensionColumns   []ExtensionColumn       `json:"extensionColumns,omitempty"`
	CatalogProjections []CatalogProjection     `json:"catalogProjections,omitempty"`
}

type Expansion struct {
	OwnerOccurrenceID string               `json:"ownerOccurrenceId,omitempty"`
	From              Expression           `json:"from"`
	As                string               `json:"as"`
	Ordinality        string               `json:"ordinality,omitempty"`
	EmptyPolicy       ExpansionEmptyPolicy `json:"emptyPolicy,omitempty"`
}

type ExpansionEmptyPolicy string

const (
	ExpansionError          ExpansionEmptyPolicy = "ERROR"
	ExpansionExclude        ExpansionEmptyPolicy = "EXCLUDE"
	ExpansionPreserveParent ExpansionEmptyPolicy = "PRESERVE_PARENT"
)

func (p ExpansionEmptyPolicy) Valid() bool {
	return p == "" || p == ExpansionError || p == ExpansionExclude || p == ExpansionPreserveParent
}

func (p ExpansionEmptyPolicy) Normalized() ExpansionEmptyPolicy {
	if p == "" {
		return ExpansionExclude
	}
	return p
}

type Identity struct {
	Name      string             `json:"name"`
	Expr      Expression         `json:"expr,omitempty"`
	Expansion *ExpansionIdentity `json:"expansion,omitempty"`
}

func (i Identity) MarshalJSON() ([]byte, error) {
	if i.Expansion != nil && !expressionPresent(i.Expr) {
		return json.Marshal(struct {
			Name      string             `json:"name"`
			Expansion *ExpansionIdentity `json:"expansion"`
		}{Name: i.Name, Expansion: i.Expansion})
	}
	type identityWire Identity
	return json.Marshal(identityWire(i))
}

type ExpansionIdentity struct{}

type DynamicColumn struct {
	Name             string            `json:"name"`
	ValueMode        ValueMode         `json:"valueMode,omitempty"`
	ColumnPrefix     *string           `json:"columnPrefix,omitempty"`
	Source           Expression        `json:"source"`
	Key              *Expression       `json:"key,omitempty"`
	Value            *Expression       `json:"value,omitempty"`
	Columns          []string          `json:"columns,omitempty"`
	MaxColumns       int               `json:"maxColumns,omitempty"`
	ColumnTypes      map[string]string `json:"columnTypes,omitempty"`
	ColumnSourceKeys map[string]string `json:"columnSourceKeys,omitempty"`
	Discovered       bool              `json:"-"`
}

type ExtensionColumn struct {
	Name         string                   `json:"name"`
	Source       Expression               `json:"source"`
	ColumnPrefix *string                  `json:"columnPrefix,omitempty"`
	MaxColumns   int                      `json:"maxColumns"`
	Columns      []ExtensionColumnMapping `json:"columns,omitempty"`
	Discovered   bool                     `json:"-"`
}

type ExtensionColumnMapping struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	SourcePath string `json:"sourcePath,omitempty"`
	ValuePath  string `json:"valuePath,omitempty"`
	ValueType  string `json:"valueType"`
}

type CatalogProjection struct {
	Name         string       `json:"name"`
	IncludePaths []string     `json:"includePaths,omitempty"`
	ExcludePaths []string     `json:"excludePaths,omitempty"`
	Kinds        []string     `json:"kinds,omitempty"`
	Naming       ColumnNaming `json:"naming,omitempty"`
	ValueMode    ValueMode    `json:"valueMode,omitempty"`
	MaxColumns   int          `json:"maxColumns"`
}

type ColumnNaming string

const (
	ColumnNamingPath       ColumnNaming = "PATH"
	ColumnNamingPathSuffix ColumnNaming = "PATH_SUFFIX"
)

type RuntimeBindings struct {
	Project                    string
	RecipeDigest               string
	DatasetGeneration          string
	AuthResourcePaths          []string
	AuthScopeMode              authscope.ReadScopeMode
	SelectionProject           string
	SelectionMembersCollection string
	PreviewLimit               int
	IncludeAuthResourcePath    bool
	OutputNames                []string
	IncludeRowIdentity         bool
	IncludeSourceIdentity      bool
}

func (b RuntimeBindings) Clone() RuntimeBindings {
	b.AuthResourcePaths = append([]string(nil), b.AuthResourcePaths...)
	b.OutputNames = append([]string(nil), b.OutputNames...)
	return b
}
