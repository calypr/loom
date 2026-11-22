package fieldcatalog

import "github.com/calypr/loom/internal/semantic"

const (
	FieldCatalogCollection       = "fhir_field_catalog"
	FieldProfileStatusRecordType = "profile_status"
	fieldKindScalar              = "scalar"
	fieldKindObject              = "object"
	fieldKindArray               = "array"
	fieldKindCodeableConcept     = "codeable_concept"
	fieldKindCoding              = "coding"
	pivotKindCodeableConcept     = "codeable_concept_display_value"
	pivotKindObservation         = "observation_code_value"
)

type FieldCatalogDocument struct {
	Key        string `json:"_key"`
	RecordType string `json:"record_type,omitempty"`
	Project    string `json:"project"`

	DatasetGeneration   string                      `json:"dataset_generation,omitempty"`
	AuthResourcePath    string                      `json:"auth_resource_path,omitempty"`
	ResourceType        string                      `json:"resource_type"`
	Path                string                      `json:"path"`
	Kind                string                      `json:"kind"`
	DocCount            int64                       `json:"doc_count"`
	MaxItems            int                         `json:"max_items,omitempty"`
	SampleCount         int                         `json:"sample_count"`
	DistinctValues      []string                    `json:"distinct_values,omitempty"`
	DistinctTruncated   bool                        `json:"distinct_truncated"`
	EnrichmentComplete  *bool                       `json:"enrichment_complete,omitempty"`
	EnrichmentTruncated bool                        `json:"enrichment_truncated"`
	ExtensionValues     []ExtensionValueObservation `json:"extension_values,omitempty"`

	SemanticObservations  []semantic.Observation `json:"semantic_observations,omitempty"`
	PivotCandidate        bool                   `json:"pivot_candidate"`
	PivotKind             string                 `json:"pivot_kind,omitempty"`
	PivotColumns          []string               `json:"pivot_columns,omitempty"`
	PivotFamily           string                 `json:"pivot_family,omitempty"`
	PivotColumnSelect     string                 `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string                 `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string                 `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string                 `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string               `json:"pivot_value_selectors,omitempty"`
}

type ExtensionValueObservation struct {
	URL        string `json:"url"`
	SourcePath string `json:"source_path"`
	ValuePath  string `json:"value_path"`
	ValueType  string `json:"value_type"`

	URLPath []string `json:"url_path,omitempty"`
}

type PopulatedFieldOptions struct {
	Project string

	DatasetGeneration string

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
	MaxItems              int                         `json:"max_items,omitempty"`
	SampleCount           int                         `json:"sample_count"`
	DistinctValues        []string                    `json:"distinct_values,omitempty"`
	DistinctTruncated     bool                        `json:"distinct_truncated"`
	ExtensionValues       []ExtensionValueObservation `json:"extension_values,omitempty"`
	SemanticObservations  []semantic.Observation      `json:"semantic_observations,omitempty"`
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
