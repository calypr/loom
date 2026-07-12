// Package discovery defines the internal, catalog-backed vocabulary used to
// build a guided dataframe experience. It deliberately exposes opaque column
// and relationship identifiers instead of FHIR selectors or graph labels.
//
// The package has no transport or database dependency. A caller first obtains
// scoped facts from internal/catalog, then passes those facts to BuildSnapshot.
// A later command handler must resolve every opaque identifier against a fresh,
// authorized snapshot before it asks the dataframe compiler to lower a query.
package discovery

import "github.com/calypr/loom/internal/catalog"

const maxSuggestedValues = 50

// CatalogFacts is a server-internal adapter input. Fields and Relationships
// must already have been read for one authorized project/scope using the
// existing catalog readers. It is intentionally not a transport request: it
// carries catalog facts, including implementation-level field paths, only long
// enough for BuildSnapshot to turn them into opaque user-facing identifiers.
// This package deliberately does not create a dataset, generation, or scope
// identity model; an owning identity layer can correlate this scoped snapshot
// later without changing the discovery vocabulary.
type CatalogFacts struct {
	Project       string
	Fields        []catalog.PopulatedField
	Relationships []catalog.PopulatedReference
}

// Snapshot is the bounded discovery response that a guided frontend may use.
// It contains no FHIR field paths, selectors, AQL fragments, collection names,
// authorization paths, or graph edge labels.
type Snapshot struct {
	Dataset       DatasetSummary           `json:"dataset"`
	Relationships RelationshipInventory    `json:"relationships"`
	Columns       []CandidateColumn        `json:"columns"`
	Filters       []GuidedFilterSuggestion `json:"filters"`
}

// DatasetSummary describes the generated-schema root resources and their
// availability in the supplied catalog facts. Roots remain present even when
// unavailable so a caller can distinguish "not loaded in this scope" from
// "not represented by this Loom build". A user-facing chooser should normally
// offer only roots whose Available flag is true.
type DatasetSummary struct {
	Project string                `json:"project"`
	Roots   []RootResourceSummary `json:"roots"`
}

// RootResourceSummary is derived from the active generated FHIR schema and
// then annotated from catalog facts. The counts describe discoverable compiler
// inputs, not a total number of FHIR documents.
type RootResourceSummary struct {
	ResourceType         string            `json:"resourceType"`
	Supported            bool              `json:"supported"`
	Available            bool              `json:"available"`
	SupportReason        RootSupportReason `json:"supportReason"`
	CandidateColumnCount int               `json:"candidateColumnCount"`
	RelationshipCount    int               `json:"relationshipCount"`
}

// RootSupportReason is intentionally small and stable. It describes only the
// generated-schema/catalog evidence available to this package; it is not a
// dataset generation, load job, or persistence state.
type RootSupportReason string

const (
	RootSupportObservedInCatalog    RootSupportReason = "OBSERVED_IN_CATALOG"
	RootSupportNotObservedInCatalog RootSupportReason = "NOT_OBSERVED_IN_CATALOG"
)

// RelationshipID is an opaque, deterministic identifier. It is stable for a
// generated-schema route while the active graph schema remains the same.
type RelationshipID string

// RelationshipInventory contains only populated routes that are represented
// by the active generated FHIR graph schema. Its entries are suitable for a
// guided "include related …" chooser, but do not imply a physical AQL
// direction.
type RelationshipInventory struct {
	Entries []Relationship `json:"entries"`
}

// RelationshipMultiplicity is the schema-declared target cardinality. It is a
// semantic fact, not an estimate of rows or an AQL traversal direction.
type RelationshipMultiplicity string

const (
	RelationshipOne  RelationshipMultiplicity = "ONE"
	RelationshipMany RelationshipMultiplicity = "MANY"
)

// Relationship is a populated, compiler-safe route between concrete generated
// FHIR resource roots. Label is presentation text; the underlying graph label
// remains internal and is recoverable only by resolving ID against facts.
type Relationship struct {
	ID                RelationshipID           `json:"id"`
	FromResourceType  string                   `json:"fromResourceType"`
	ToResourceType    string                   `json:"toResourceType"`
	Label             string                   `json:"label"`
	Multiplicity      RelationshipMultiplicity `json:"multiplicity"`
	ObservedEdgeCount int64                    `json:"observedEdgeCount"`
}

// ColumnID is an opaque, deterministic identifier for a catalog field. It is
// deliberately not a field path or a dataframe FieldRef.
type ColumnID string

// ValueKind is the safe scalar shape known from generated FHIR metadata. A
// COMPOSITE candidate represents a catalog pivot root and is not directly
// filterable or selectable until a later compiler adapter resolves it.
type ValueKind string

const (
	ValueKindString    ValueKind = "STRING"
	ValueKindBoolean   ValueKind = "BOOLEAN"
	ValueKindInteger   ValueKind = "INTEGER"
	ValueKindDecimal   ValueKind = "DECIMAL"
	ValueKindDate      ValueKind = "DATE"
	ValueKindDateTime  ValueKind = "DATE_TIME"
	ValueKindComposite ValueKind = "COMPOSITE"
)

// CandidateColumn is a safe frontend-facing field choice. Capability flags
// make composite pivot roots explicit instead of pretending every observed
// catalog object can be lowered as a scalar dataframe column.
type CandidateColumn struct {
	ID                     ColumnID  `json:"id"`
	ResourceType           string    `json:"resourceType"`
	Label                  string    `json:"label"`
	ValueKind              ValueKind `json:"valueKind"`
	Repeated               bool      `json:"repeated"`
	CanSelect              bool      `json:"canSelect"`
	CanFilter              bool      `json:"canFilter"`
	CanPivot               bool      `json:"canPivot"`
	PopulatedDocumentCount int64     `json:"populatedDocumentCount"`
	SuggestedValues        []string  `json:"suggestedValues"`
	ValuesTruncated        bool      `json:"valuesTruncated"`
}

// FilterSuggestionID is an opaque, deterministic identifier for the guided
// filter affordance associated with a scalar CandidateColumn.
type FilterSuggestionID string

// FilterOperator is intentionally a closed product vocabulary. A later
// compiler adapter maps these tokens to dataframe.TypedFilter only after it
// resolves ColumnID against the current authorized catalog facts.
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

// FilterQuantifier controls how a repeated scalar field is evaluated. Scalar
// candidates have no quantifier options.
type FilterQuantifier string

const (
	FilterAny  FilterQuantifier = "ANY"
	FilterAll  FilterQuantifier = "ALL"
	FilterNone FilterQuantifier = "NONE"
)

// GuidedFilterSuggestion is the complete field/operator/value vocabulary for
// a guided filter sentence such as "Gender is female." Values are examples
// observed by the catalog, not an exhaustive terminology list.
type GuidedFilterSuggestion struct {
	ID              FilterSuggestionID `json:"id"`
	ColumnID        ColumnID           `json:"columnId"`
	ResourceType    string             `json:"resourceType"`
	Label           string             `json:"label"`
	ValueKind       ValueKind          `json:"valueKind"`
	Repeated        bool               `json:"repeated"`
	Operators       []FilterOperator   `json:"operators"`
	Quantifiers     []FilterQuantifier `json:"quantifiers"`
	SuggestedValues []string           `json:"suggestedValues"`
	ValuesTruncated bool               `json:"valuesTruncated"`
}
