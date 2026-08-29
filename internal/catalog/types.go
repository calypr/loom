package catalog

import (
	"context"
	"sync"
)

const (
	FieldCatalogCollection        = "fhir_field_catalog"
	RelationshipCatalogCollection = "fhir_relationship_catalog"
	fieldKindScalar               = "scalar"
	fieldKindObject               = "object"
	fieldKindArray                = "array"
	fieldKindCodeableConcept      = "codeable_concept"
	fieldKindCoding               = "coding"
	pivotKindCodeableConcept      = "codeable_concept_display_value"
	pivotKindObservation          = "observation_code_value"
)

const (
	TraversalModeStorage = "storage"
	TraversalModeBuilder = "builder"
)

// EvidenceStatus describes the meaning of a capability-evidence read. An
// empty Values slice is only usable when Status is EvidenceEmpty (or
// EvidenceAvailable with Complete true); unavailable, incomplete, and
// truncated reads must never be treated as an empty capability set.
type EvidenceStatus string

const (
	EvidenceAvailable   EvidenceStatus = "available"
	EvidenceEmpty       EvidenceStatus = "empty"
	EvidenceUnavailable EvidenceStatus = "unavailable"
	EvidenceIncomplete  EvidenceStatus = "incomplete"
	EvidenceTruncated   EvidenceStatus = "truncated"
)

// CapabilityEvidenceOptions identifies one immutable, authorization-scoped
// evidence namespace. ResourceTypes is optional and is primarily useful for a
// bounded read or a test; an empty value means every generated concrete FHIR
// resource type.
type CapabilityEvidenceOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	ResourceTypes                 []string
	CursorBatch                   int
}

type ResourceInventoryOptions = CapabilityEvidenceOptions
type RelationshipObservationOptions = CapabilityEvidenceOptions
type FieldEnrichmentOptions = CapabilityEvidenceOptions

// EvidenceDiagnostic records malformed historical evidence without promoting
// it to a capability. Diagnostics are deliberately data-only so callers can
// retain an otherwise usable snapshot and explain excluded records.
type EvidenceDiagnostic struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	ResourceType    string `json:"resource_type,omitempty"`
	StorageFromType string `json:"storage_from_type,omitempty"`
	Label           string `json:"label,omitempty"`
	StorageToType   string `json:"storage_to_type,omitempty"`
	Count           int64  `json:"count,omitempty"`
}

// ResourceInventoryObservation is the only evidence source for capability
// row nodes. It is intentionally independent of field profiling: a resource
// with no populated field may still be a valid, empty row collection.
type ResourceInventoryObservation struct {
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation,omitempty"`
	AuthResourcePath  string `json:"auth_resource_path,omitempty"`
	ResourceType      string `json:"resource_type"`
	DocumentCount     int64  `json:"document_count"`
}

type ResourceInventoryResult struct {
	Values      []ResourceInventoryObservation `json:"values"`
	Available   bool                           `json:"available"`
	Complete    bool                           `json:"complete"`
	Truncated   bool                           `json:"truncated"`
	Status      EvidenceStatus                 `json:"status"`
	Digest      string                         `json:"digest,omitempty"`
	Diagnostics []EvidenceDiagnostic           `json:"diagnostics,omitempty"`
}

// RelationshipObservation retains both projections of one physical edge
// aggregate. StorageFromType/StorageToType are the original _from/_to
// endpoint types (and StorageDirection is always OUTBOUND for fhir_edge).
// FromType/ToType are the Builder-facing inverse where the generated
// traversal registry supports it; BuilderDirection is the physical AQL
// direction needed to follow that Builder edge.
type RelationshipObservation struct {
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation,omitempty"`
	AuthResourcePath  string `json:"auth_resource_path,omitempty"`
	FromType          string `json:"from_type"`
	Label             string `json:"label"`
	ToType            string `json:"to_type"`
	EdgeCount         int64  `json:"edge_count"`
	StorageFromType   string `json:"storage_from_type"`
	StorageToType     string `json:"storage_to_type"`
	StorageDirection  string `json:"storage_direction"`
	BuilderFromType   string `json:"builder_from_type"`
	BuilderToType     string `json:"builder_to_type"`
	BuilderDirection  string `json:"builder_direction"`
}

type RelationshipObservationResult struct {
	Values      []RelationshipObservation `json:"values"`
	Available   bool                      `json:"available"`
	Complete    bool                      `json:"complete"`
	Truncated   bool                      `json:"truncated"`
	Status      EvidenceStatus            `json:"status"`
	Digest      string                    `json:"digest,omitempty"`
	Diagnostics []EvidenceDiagnostic      `json:"diagnostics,omitempty"`
}

// FieldEnrichmentObservation is a capability-facing copy of the bounded
// profiler summary. It is never used to create row nodes. DistinctTruncated
// describes only that field's bounded suggestions; the result-level Truncated
// flag is reserved for an incomplete enrichment artifact and fails closed for
// capability publication.
type FieldEnrichmentObservation struct {
	Project               string                      `json:"project"`
	DatasetGeneration     string                      `json:"dataset_generation,omitempty"`
	AuthResourcePath      string                      `json:"auth_resource_path,omitempty"`
	ResourceType          string                      `json:"resource_type"`
	Path                  string                      `json:"path"`
	Kind                  string                      `json:"kind"`
	DocCount              int64                       `json:"doc_count"`
	SampleCount           int                         `json:"sample_count"`
	DistinctValues        []string                    `json:"distinct_values,omitempty"`
	DistinctTruncated     bool                        `json:"distinct_truncated"`
	ExtensionValues       []ExtensionValueObservation `json:"extension_values,omitempty"`
	PivotCandidate        bool                        `json:"pivot_candidate"`
	PivotKind             string                      `json:"pivot_kind,omitempty"`
	PivotColumns          []string                    `json:"pivot_columns,omitempty"`
	PivotFamily           string                      `json:"pivot_family,omitempty"`
	PivotColumnSelect     string                      `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string                      `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string                      `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string                      `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string                    `json:"pivot_value_selectors,omitempty"`
}

type FieldEnrichmentResult struct {
	Values      []FieldEnrichmentObservation `json:"values"`
	Available   bool                         `json:"available"`
	Complete    bool                         `json:"complete"`
	Truncated   bool                         `json:"truncated"`
	Status      EvidenceStatus               `json:"status"`
	Digest      string                       `json:"digest,omitempty"`
	Diagnostics []EvidenceDiagnostic         `json:"diagnostics,omitempty"`
}

// CapabilityEvidence is the neutral aggregate consumed by a snapshot
// compiler. The three reads remain separately inspectable so optional field
// enrichment cannot make resource inventory or relationship evidence vanish.
type CapabilityEvidence struct {
	ResourceInventory ResourceInventoryResult       `json:"resource_inventory"`
	Relationships     RelationshipObservationResult `json:"relationships"`
	FieldEnrichment   FieldEnrichmentResult         `json:"field_enrichment"`
}

type CapabilityEvidenceReader interface {
	DiscoverResourceInventory(context.Context, ResourceInventoryOptions) (ResourceInventoryResult, error)
	DiscoverRelationshipObservations(context.Context, RelationshipObservationOptions) (RelationshipObservationResult, error)
	DiscoverFieldEnrichment(context.Context, FieldEnrichmentOptions) (FieldEnrichmentResult, error)
}

// Write-side catalog records persisted during load.
type FieldCatalogDocument struct {
	Key     string `json:"_key"`
	Project string `json:"project"`
	// DatasetGeneration identifies the immutable dataset generation that
	// produced this catalog row. An omitted value is intentionally the legacy
	// dataset namespace; readers always bind either this exact value or null so
	// legacy and generation-qualified observations never mix.
	DatasetGeneration     string                      `json:"dataset_generation,omitempty"`
	AuthResourcePath      string                      `json:"auth_resource_path,omitempty"`
	ResourceType          string                      `json:"resource_type"`
	Path                  string                      `json:"path"`
	Kind                  string                      `json:"kind"`
	DocCount              int64                       `json:"doc_count"`
	SampleCount           int                         `json:"sample_count"`
	DistinctValues        []string                    `json:"distinct_values,omitempty"`
	DistinctTruncated     bool                        `json:"distinct_truncated"`
	ExtensionValues       []ExtensionValueObservation `json:"extension_values,omitempty"`
	PivotCandidate        bool                        `json:"pivot_candidate"`
	PivotKind             string                      `json:"pivot_kind,omitempty"`
	PivotColumns          []string                    `json:"pivot_columns,omitempty"`
	PivotFamily           string                      `json:"pivot_family,omitempty"`
	PivotColumnSelect     string                      `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string                      `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string                      `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string                      `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string                    `json:"pivot_value_selectors,omitempty"`
}

// ExtensionValueObservation preserves the correlation between an Extension
// URL and the concrete value[x] member observed on the same object. Keeping
// this pairing in the catalog avoids independently combining unrelated URL
// and value distinct-value sets during recipe schema discovery.
type ExtensionValueObservation struct {
	URL        string `json:"url"`
	SourcePath string `json:"source_path"`
	ValuePath  string `json:"value_path"`
	ValueType  string `json:"value_type"`
	// URLPath contains ancestor Extension URLs for nested extensions. It is
	// empty for top-level extensions and lets recipe discovery avoid leaf-name
	// collisions while retaining deterministic parent__child names.
	URLPath []string `json:"url_path,omitempty"`
}

// Read-side field discovery request and response types.
type PopulatedFieldOptions struct {
	Project string
	// DatasetGeneration is optional. An empty value means the legacy dataset
	// namespace and therefore reads only catalog documents whose
	// dataset_generation is null or absent.
	DatasetGeneration string
	// AuthResourcePathsUnrestricted is the effective AQL bypass mode. A nil
	// value preserves the legacy convention that an empty path list is
	// unrestricted; request services must set it explicitly after resolving
	// authorization so a restricted-empty intersection remains restricted.
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	ResourceType                  string
	PivotOnly                     bool
	CursorBatch                   int
}

type PopulatedField struct {
	Project               string                      `json:"project"`
	DatasetGeneration     string                      `json:"dataset_generation,omitempty"`
	AuthResourcePath      string                      `json:"auth_resource_path,omitempty"`
	ResourceType          string                      `json:"resource_type"`
	Path                  string                      `json:"path"`
	Kind                  string                      `json:"kind"`
	DocCount              int64                       `json:"doc_count"`
	SampleCount           int                         `json:"sample_count"`
	DistinctValues        []string                    `json:"distinct_values,omitempty"`
	DistinctTruncated     bool                        `json:"distinct_truncated"`
	ExtensionValues       []ExtensionValueObservation `json:"extension_values,omitempty"`
	PivotCandidate        bool                        `json:"pivot_candidate"`
	PivotKind             string                      `json:"pivot_kind,omitempty"`
	PivotColumns          []string                    `json:"pivot_columns,omitempty"`
	PivotFamily           string                      `json:"pivot_family,omitempty"`
	PivotColumnSelect     string                      `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string                      `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string                      `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string                      `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string                    `json:"pivot_value_selectors,omitempty"`
}

// Read-side auth path discovery request type.
type AuthResourcePathOptions struct {
	Project           string
	DatasetGeneration string
	CursorBatch       int
}

// Read-side reference discovery request and response types.
type PopulatedReferenceOptions struct {
	Project string
	// DatasetGeneration follows the same legacy-null contract as
	// PopulatedFieldOptions.
	DatasetGeneration string
	// AuthResourcePathsUnrestricted has the same explicit-mode contract as
	// PopulatedFieldOptions.
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	FromType                      string
	NodeType                      string
	Mode                          string
	CursorBatch                   int
}

type PopulatedReference struct {
	DatasetGeneration string `json:"dataset_generation,omitempty"`
	FromType          string `json:"from_type"`
	Label             string `json:"label"`
	ToType            string `json:"to_type"`
	EdgeCount         int64  `json:"edge_count"`
}

// RelationshipCatalogDocument is the ingest-owned edge cardinality row used
// by builder and storage reference discovery. Auth paths remain part of the
// identity so restricted readers can aggregate only authorized edges.
type RelationshipCatalogDocument struct {
	Key               string `json:"_key"`
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation,omitempty"`
	AuthResourcePath  string `json:"auth_resource_path,omitempty"`
	FromType          string `json:"from_type"`
	Label             string `json:"label"`
	ToType            string `json:"to_type"`
	EdgeCount         int64  `json:"edge_count"`
}

type RelationshipKey struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	FromType          string
	Label             string
	ToType            string
}

// Write-side field profiling state.
type Profiler struct {
	project           string
	datasetGeneration string
	authResourcePath  string
	resourceType      string
	limits            ProfileLimits
	shapeCache        *ShapePlanCache
	stats             map[string]*fieldCatalogStats
}

type fieldCatalogStats struct {
	path                  string
	kind                  string
	docCount              int64
	distinctValues        []string
	distinctSet           map[string]struct{}
	distinctTruncated     bool
	pivotCandidate        bool
	pivotKind             string
	pivotColumns          []string
	pivotColumnSet        map[string]struct{}
	pivotFamily           string
	pivotColumnSelect     string
	pivotValueSelect      string
	pivotItemSource       string
	pivotItemResourceType string
	pivotValueSelectors   []string
	extensionValues       []ExtensionValueObservation
	extensionValueSet     map[string]struct{}
}

// Shared write-side shape planning cache.
type ShapePlanCache struct {
	mu       sync.RWMutex
	plans    map[string]*shapePlan
	maxPlans int
}

type shapePlan struct {
	fields []*fieldPlan
}

type fieldPlan struct {
	Path           string
	Kind           string
	Accessor       []pathStep
	PivotCandidate bool
	PivotKind      string
}

type pathStep struct {
	field        string
	iterateArray bool
}
