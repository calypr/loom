// Package recipe defines the persistence-neutral recipe document used by the
// dataframe compiler. A recipe describes semantic row shaping only; it never
// carries database collection, table, AQL, or SQL details.
package recipe

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/internal/authscope"
)

// CurrentSchemaVersion is the first stable recipe document schema.
const CurrentSchemaVersion = 1

const (
	maxExpressionDepth = 64
	maxExpressionNodes = 4096
	maxLiteralArray    = 256
)

// Bundle is an immutable, versioned collection of dataframe outputs.
type Bundle struct {
	RecipeSchemaVersion int              `json:"recipeSchemaVersion"`
	Name                string           `json:"name"`
	TranslationVersion  string           `json:"translationVersion"`
	Fragments           *FragmentLibrary `json:"fragments,omitempty"`
	Outputs             []Output         `json:"outputs"`
}

// Output describes one row-shaped result. Names are semantic names, not
// storage identifiers.
type Output struct {
	Name               string                `json:"name"`
	RootResourceType   string                `json:"rootResourceType"`
	RowGrain           string                `json:"rowGrain"`
	Fields             []Field               `json:"fields,omitempty"`
	Filters            []Filter              `json:"filters,omitempty"`
	Pivots             []Pivot               `json:"pivots,omitempty"`
	Aggregates         []Aggregate           `json:"aggregates,omitempty"`
	Slices             []RepresentativeSlice `json:"slices,omitempty"`
	Traversals         []Traversal           `json:"traversals,omitempty"`
	Expand             *Expansion            `json:"expand,omitempty"`
	Identity           *Identity             `json:"identity,omitempty"`
	DynamicColumns     []DynamicColumn       `json:"dynamicColumns,omitempty"`
	ExtensionColumns   []ExtensionColumn     `json:"extensionColumns,omitempty"`
	CatalogProjections []CatalogProjection   `json:"catalogProjections,omitempty"`
	CollisionPolicy    string                `json:"collisionPolicy,omitempty"`
}

// Field projects one named semantic value into an output row.
type Field struct {
	Name       string       `json:"name"`
	FieldRef   string       `json:"fieldRef,omitempty"`
	Expr       Expression   `json:"expr"`
	Fallbacks  []Expression `json:"fallbacks,omitempty"`
	ValueMode  ValueMode    `json:"valueMode,omitempty"`
	Discovered bool         `json:"-"`
}

// ValueMode controls how a checked selector contributes values to one row.
// The empty value is the backwards-compatible AUTO default and is omitted from
// persisted JSON so existing recipe bytes and digests remain stable.
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

// Filter is a typed, storage-independent predicate. Select is the executable
// selector; FieldRef is optional frontend/catalog provenance.
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
}

// FilterValue is a strict tagged union. Pointer scalar members preserve false,
// zero, and empty string values during JSON decoding.
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

// Pivot describes a bounded, schema-validated column/value mapping.
type Pivot struct {
	Name             string          `json:"name"`
	FieldRef         string          `json:"fieldRef,omitempty"`
	ColumnExpr       Expression      `json:"columnExpr"`
	ValueExpr        Expression      `json:"valueExpr"`
	ValueFallbacks   []Expression    `json:"valueFallbacks,omitempty"`
	ItemSource       Expression      `json:"itemSource,omitempty"`
	ItemResourceType string          `json:"itemResourceType,omitempty"`
	Columns          []string        `json:"columns"`
	Discovery        *PivotDiscovery `json:"discovery,omitempty"`
	Discovered       bool            `json:"-"`
}

// MarshalJSON permits catalog-backed pivots to omit selectors in their stored
// declaration. The schema resolver fills those selectors from the scoped
// catalog metadata before semantic compilation; ordinary static pivots still
// serialize both required expressions and therefore retain the strict AST
// contract.
func (p Pivot) MarshalJSON() ([]byte, error) {
	type pivotJSON struct {
		Name             string          `json:"name"`
		FieldRef         string          `json:"fieldRef,omitempty"`
		ColumnExpr       *Expression     `json:"columnExpr,omitempty"`
		ValueExpr        *Expression     `json:"valueExpr,omitempty"`
		ValueFallbacks   []Expression    `json:"valueFallbacks,omitempty"`
		ItemSource       *Expression     `json:"itemSource,omitempty"`
		ItemResourceType string          `json:"itemResourceType,omitempty"`
		Columns          []string        `json:"columns"`
		Discovery        *PivotDiscovery `json:"discovery,omitempty"`
	}
	wire := pivotJSON{Name: p.Name, FieldRef: p.FieldRef, ValueFallbacks: p.ValueFallbacks, ItemResourceType: p.ItemResourceType, Columns: p.Columns, Discovery: p.Discovery}
	if p.Discovery == nil || !p.ColumnExpr.zero() {
		value := p.ColumnExpr
		wire.ColumnExpr = &value
	}
	if p.Discovery == nil || !p.ValueExpr.zero() {
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

// PivotDiscovery requests a bounded column set from the scoped field catalog.
// The schema resolver replaces this declaration with concrete Columns before
// semantic typing and physical lowering.
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
)

func (op AggregateOperation) Valid() bool {
	switch op {
	case AggregateCount, AggregateCountDistinct, AggregateExists, AggregateDistinctValues, AggregateMin, AggregateMax:
		return true
	default:
		return false
	}
}

type Aggregate struct {
	Name      string             `json:"name"`
	Operation AggregateOperation `json:"operation"`
	FieldRef  string             `json:"fieldRef,omitempty"`
	Expr      *Expression        `json:"expr,omitempty"`
	Where     *Filter            `json:"where,omitempty"`
	ValueMode ValueMode          `json:"valueMode,omitempty"`
}

type RepresentativeSlice struct {
	Name   string  `json:"name"`
	Where  *Filter `json:"where,omitempty"`
	Limit  int     `json:"limit"`
	Fields []Field `json:"fields"`
}

// TraversalMatchMode is deliberately closed. Empty remains the legacy
// optional default and is normalized only at semantic lowering time.
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

// Traversal describes a relationship traversal without naming a physical
// graph collection or edge table.
type Traversal struct {
	Name               string                `json:"name"`
	ToResourceType     string                `json:"toResourceType"`
	Alias              string                `json:"alias,omitempty"`
	From               *Expression           `json:"from,omitempty"`
	MatchMode          TraversalMatchMode    `json:"matchMode,omitempty"`
	Fields             []Field               `json:"fields,omitempty"`
	Filters            []Filter              `json:"filters,omitempty"`
	Pivots             []Pivot               `json:"pivots,omitempty"`
	Aggregates         []Aggregate           `json:"aggregates,omitempty"`
	Slices             []RepresentativeSlice `json:"slices,omitempty"`
	Traversals         []Traversal           `json:"traversals,omitempty"`
	DynamicColumns     []DynamicColumn       `json:"dynamicColumns,omitempty"`
	ExtensionColumns   []ExtensionColumn     `json:"extensionColumns,omitempty"`
	CatalogProjections []CatalogProjection   `json:"catalogProjections,omitempty"`
}

// Expansion turns a repeated expression into one row per element.
type Expansion struct {
	From Expression `json:"from"`
	As   string     `json:"as"`
}

// Identity derives a deterministic row identity.
type Identity struct {
	Name string     `json:"name"`
	Expr Expression `json:"expr"`
}

// DynamicColumn discovers a bounded set of key/value columns. The compiler
// freezes discovered keys before materialization.
type DynamicColumn struct {
	Name string `json:"name"`
	// ColumnPrefix controls the public prefix of frozen dynamic columns. When
	// omitted, Name remains the prefix for backwards compatibility. An explicit
	// empty string permits a dynamic family such as URL-keyed extensions to
	// publish its normalized keys directly (for example source_path).
	ColumnPrefix *string     `json:"columnPrefix,omitempty"`
	Source       Expression  `json:"source"`
	Key          *Expression `json:"key,omitempty"`
	Value        *Expression `json:"value,omitempty"`
	Columns      []string    `json:"columns,omitempty"`
	MaxColumns   int         `json:"maxColumns,omitempty"`
	// ColumnTypes carries resolver-observed logical types for frozen keys. It
	// is optional for legacy dynamicColumns and is populated for typed
	// extensionColumns.
	ColumnTypes map[string]string `json:"columnTypes,omitempty"`
	// ColumnSourceKeys decouples a public frozen column name from the runtime
	// keyed-map lookup key. Extension columns use this for parent__child names
	// while matching the leaf Extension.url segment inside its scoped source.
	ColumnSourceKeys map[string]string `json:"columnSourceKeys,omitempty"`
	Discovered       bool              `json:"-"`
}

// ExtensionColumn is a bounded, URL-keyed projection of FHIR Extension
// values. Unlike dynamicColumns, its resolver understands nested Extension
// arrays and freezes both the URL mapping and the value representation.
type ExtensionColumn struct {
	Name         string                   `json:"name"`
	Source       Expression               `json:"source"`
	ColumnPrefix *string                  `json:"columnPrefix,omitempty"`
	MaxColumns   int                      `json:"maxColumns"`
	Columns      []ExtensionColumnMapping `json:"columns,omitempty"`
	Discovered   bool                     `json:"-"`
}

// ExtensionColumnMapping is immutable schema-discovery output. Name is the
// normalized public key, URL is the raw Extension.url, and ValuePath is the
// relative item selector for a single primitive value[x]. Empty ValuePath
// means the extension object is represented by canonical JSON.
type ExtensionColumnMapping struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	SourcePath string `json:"sourcePath,omitempty"`
	ValuePath  string `json:"valuePath,omitempty"`
	ValueType  string `json:"valueType"`
}

// CatalogProjection describes a bounded family of populated FHIR paths. It
// is storage-neutral; the scoped schema resolver turns it into concrete typed
// recipe fields before semantic compilation.
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

// RuntimeBindings are request-scoped and deliberately not part of a stored
// recipe digest.
type RuntimeBindings struct {
	Project string
	// RecipeDigest selects an immutable project-scoped revision when supplied.
	// Empty preserves the server-owned name lookup used by legacy callers.
	RecipeDigest      string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
	PreviewLimit      int
	// IncludeAuthResourcePath is set only for ClickHouse publication streams.
	// It keeps the reserved row-level authorization field out of ordinary
	// dataframe previews while ensuring published rows carry their source path.
	IncludeAuthResourcePath bool
	// OutputNames limits execution to named outputs for preview requests. An
	// empty list preserves the bundle's all-output behavior and is not part of
	// recipe or schema identity.
	OutputNames []string
}

// Clone returns request-scoped bindings with independent authorization paths.
// Runtime bindings are never serialized into a recipe or included in its
// digest.
func (b RuntimeBindings) Clone() RuntimeBindings {
	b.AuthResourcePaths = append([]string(nil), b.AuthResourcePaths...)
	b.OutputNames = append([]string(nil), b.OutputNames...)
	return b
}

// ExpandFragments resolves the optional declarative fragment library and
// returns a standalone bundle suitable for semantic compilation. The stored
// library is omitted from the returned document because the expanded recipe
// is the immutable compiler input and its digest is what runtime plans carry.
