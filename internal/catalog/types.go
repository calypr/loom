package catalog

import "sync"

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

// Write-side catalog records persisted during load.
type FieldCatalogDocument struct {
	Key     string `json:"_key"`
	Project string `json:"project"`
	// DatasetGeneration identifies the immutable dataset generation that
	// produced this catalog row. An omitted value is intentionally the legacy
	// dataset namespace; readers always bind either this exact value or null so
	// legacy and generation-qualified observations never mix.
	DatasetGeneration     string   `json:"dataset_generation,omitempty"`
	AuthResourcePath      string   `json:"auth_resource_path,omitempty"`
	ResourceType          string   `json:"resource_type"`
	Path                  string   `json:"path"`
	Kind                  string   `json:"kind"`
	DocCount              int64    `json:"doc_count"`
	SampleCount           int      `json:"sample_count"`
	DistinctValues        []string `json:"distinct_values,omitempty"`
	DistinctTruncated     bool     `json:"distinct_truncated"`
	PivotCandidate        bool     `json:"pivot_candidate"`
	PivotKind             string   `json:"pivot_kind,omitempty"`
	PivotColumns          []string `json:"pivot_columns,omitempty"`
	PivotFamily           string   `json:"pivot_family,omitempty"`
	PivotColumnSelect     string   `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string   `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string   `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string   `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string `json:"pivot_value_selectors,omitempty"`
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
	Project               string   `json:"project"`
	DatasetGeneration     string   `json:"dataset_generation,omitempty"`
	AuthResourcePath      string   `json:"auth_resource_path,omitempty"`
	ResourceType          string   `json:"resource_type"`
	Path                  string   `json:"path"`
	Kind                  string   `json:"kind"`
	DocCount              int64    `json:"doc_count"`
	SampleCount           int      `json:"sample_count"`
	DistinctValues        []string `json:"distinct_values,omitempty"`
	DistinctTruncated     bool     `json:"distinct_truncated"`
	PivotCandidate        bool     `json:"pivot_candidate"`
	PivotKind             string   `json:"pivot_kind,omitempty"`
	PivotColumns          []string `json:"pivot_columns,omitempty"`
	PivotFamily           string   `json:"pivot_family,omitempty"`
	PivotColumnSelect     string   `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string   `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string   `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string   `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string `json:"pivot_value_selectors,omitempty"`
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
}

// Shared write-side shape planning cache.
type ShapePlanCache struct {
	mu    sync.RWMutex
	plans map[string]*shapePlan
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
