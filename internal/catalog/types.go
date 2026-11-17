package catalog

import (
	"context"
	"sync"
)

const (
	FieldCatalogCollection        = "fhir_field_catalog"
	RelationshipCatalogCollection = "fhir_relationship_catalog"
	FieldProfileStatusRecordType  = "profile_status"
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

type EvidenceStatus string

const (
	EvidenceAvailable   EvidenceStatus = "available"
	EvidenceEmpty       EvidenceStatus = "empty"
	EvidenceUnavailable EvidenceStatus = "unavailable"
	EvidenceIncomplete  EvidenceStatus = "incomplete"
	EvidenceTruncated   EvidenceStatus = "truncated"
)

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

type EvidenceDiagnostic struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	ResourceType    string `json:"resource_type,omitempty"`
	StorageFromType string `json:"storage_from_type,omitempty"`
	Label           string `json:"label,omitempty"`
	StorageToType   string `json:"storage_to_type,omitempty"`
	Count           int64  `json:"count,omitempty"`
}

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

const ResourceInventoryCollection = "fhir_resource_inventory"

type ResourceInventoryCount struct {
	Key               string `json:"_key"`
	RecordType        string `json:"record_type"`
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation"`
	AuthResourcePath  string `json:"auth_resource_path"`
	ResourceType      string `json:"resource_type"`
	BuildID           string `json:"build_id"`
	DocumentCount     int64  `json:"document_count"`
}

type ResourceInventoryBuild struct {
	Key               string   `json:"_key"`
	RecordType        string   `json:"record_type"`
	Project           string   `json:"project"`
	DatasetGeneration string   `json:"dataset_generation"`
	BuildID           string   `json:"build_id"`
	CoverageDigest    string   `json:"coverage_digest"`
	ResourceTypes     []string `json:"resource_types"`
	ResourceTypeCount int      `json:"resource_type_count"`
	CountRowCount     int      `json:"count_row_count"`
	Complete          bool     `json:"complete"`
}

type ResourceInventoryRebuildOptions struct {
	Project           string
	DatasetGeneration string
	ResourceTypes     []string
	CursorBatch       int
	BatchSize         int
	WriteAPI          string
}

type ResourceInventoryRebuildSummary struct {
	BuildID           string `json:"build_id"`
	CoverageDigest    string `json:"coverage_digest"`
	ResourceTypeCount int    `json:"resource_type_count"`
	CountRowCount     int    `json:"count_row_count"`
	DocumentCount     int64  `json:"document_count"`
}

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

type FieldEnrichmentObservation struct {
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
	SemanticObservations  []SemanticObservation       `json:"semantic_observations,omitempty"`
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
	Values             []FieldEnrichmentObservation `json:"values"`
	Available          bool                         `json:"available"`
	Complete           bool                         `json:"complete"`
	Truncated          bool                         `json:"truncated"`
	FieldCoverageKnown bool                         `json:"field_coverage_known"`
	Status             EvidenceStatus               `json:"status"`
	Digest             string                       `json:"digest,omitempty"`
	Diagnostics        []EvidenceDiagnostic         `json:"diagnostics,omitempty"`
}

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

type FieldCatalogDocument struct {
	Key                   string                      `json:"_key"`
	RecordType            string                      `json:"record_type,omitempty"`
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
	EnrichmentComplete    *bool                       `json:"enrichment_complete,omitempty"`
	EnrichmentTruncated   bool                        `json:"enrichment_truncated"`
	ExtensionValues       []ExtensionValueObservation `json:"extension_values,omitempty"`
	SemanticObservations  []SemanticObservation       `json:"semantic_observations,omitempty"`
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

type SemanticObservationCompleteness string

const (
	SemanticRuleHintCodedConceptV1  = "CODED_CONCEPT_V1"
	SemanticRuleHintCodedCategoryV1 = "CODED_CATEGORY_V1"
)

const (
	SemanticComplete   SemanticObservationCompleteness = "complete"
	SemanticPartial    SemanticObservationCompleteness = "partial"
	SemanticIncomplete SemanticObservationCompleteness = "incomplete"
)

type SemanticObservation struct {
	SchemaVersion          int                             `json:"schema_version"`
	Source                 SemanticObservationSource       `json:"source"`
	Key                    SemanticObservationKey          `json:"key"`
	Value                  SemanticObservationValue        `json:"value"`
	OwningScope            string                          `json:"owning_scope,omitempty"`
	ExtensionURLPath       []string                        `json:"extension_url_path,omitempty"`
	ChoiceArm              string                          `json:"choice_arm,omitempty"`
	LogicalType            string                          `json:"logical_type,omitempty"`
	ObservedUnits          []string                        `json:"observed_units,omitempty"`
	ObservedUnitsTruncated bool                            `json:"observed_units_truncated,omitempty"`
	Completeness           SemanticObservationCompleteness `json:"completeness"`
	Status                 string                          `json:"status,omitempty"`
	Population             int64                           `json:"population"`
	Examples               []string                        `json:"examples,omitempty"`
	ExamplesTruncated      bool                            `json:"examples_truncated"`
	RuleHint               string                          `json:"rule_hint,omitempty"`
	RuleVersion            string                          `json:"rule_version,omitempty"`
}

type ConceptCandidate = SemanticObservation

type SemanticObservationSource struct {
	Canonical string `json:"canonical,omitempty"`
	Type      string `json:"type,omitempty"`
	Profile   string `json:"profile,omitempty"`
	Path      string `json:"path,omitempty"`
}

type SemanticObservationKey struct {
	Selector string `json:"selector,omitempty"`
	System   string `json:"system,omitempty"`
	Version  string `json:"version,omitempty"`
	Code     string `json:"code,omitempty"`
	Display  string `json:"display,omitempty"`
}

type SemanticObservationValue struct {
	Selector              string   `json:"selector,omitempty"`
	Type                  string   `json:"type,omitempty"`
	FallbackSelectors     []string `json:"fallback_selectors,omitempty"`
	FallbackOnBlankString bool     `json:"fallback_on_blank_string,omitempty"`
}

type ExtensionValueObservation struct {
	URL        string   `json:"url"`
	SourcePath string   `json:"source_path"`
	ValuePath  string   `json:"value_path"`
	ValueType  string   `json:"value_type"`
	URLPath    []string `json:"url_path,omitempty"`
}

type PopulatedFieldOptions struct {
	Project                       string
	DatasetGeneration             string
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
	SemanticObservations  []SemanticObservation       `json:"semantic_observations,omitempty"`
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

type AuthResourcePathOptions struct {
	Project           string
	DatasetGeneration string
	CursorBatch       int
	AllGenerations    bool
}

type PopulatedReferenceOptions struct {
	Project                       string
	DatasetGeneration             string
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

type Profiler struct {
	project                  string
	datasetGeneration        string
	semanticInventoryBuildID string
	authResourcePath         string
	resourceType             string
	semanticSourceKind       string
	semanticOnly             bool
	limits                   ProfileLimits
	shapeCache               *ShapePlanCache
	stats                    map[string]*fieldCatalogStats
	retainedBytes            int
	truncated                bool
	enrichmentTruncated      bool
	budget                   *retentionBudget
}

type fieldCatalogStats struct {
	path                  string
	kind                  string
	docCount              int64
	maxItems              int
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
	pivotColumnArms       map[string]struct{}
	pivotValueArms        map[string]struct{}
	pivotSpecApplied      bool
	extensionValues       []ExtensionValueObservation
	extensionValueSet     map[string]struct{}
	semanticObservations  map[string]*semanticObservationStats
}

type semanticObservationStats struct {
	observation SemanticObservation
	exampleSet  map[string]struct{}
	unitSet     map[string]struct{}
}

type ShapePlanCache struct {
	mu            sync.RWMutex
	plans         map[string]*shapePlan
	maxPlans      int
	maxBytes      int
	retainedBytes int
	truncated     bool
	budget        *retentionBudget
}

type retentionBudget struct {
	mu   sync.Mutex
	max  int
	used int
}

func newRetentionBudget(max int) *retentionBudget {
	return &retentionBudget{max: max}
}

func (b *retentionBudget) reserve(weight int) bool {
	if weight <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max > 0 && b.used > b.max-weight {
		return false
	}
	b.used += weight
	return true
}

func (b *retentionBudget) release(weight int) {
	if weight <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= weight
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
}

func (b *retentionBudget) usage() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (c *ShapePlanCache) retentionBudget(max int) *retentionBudget {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budget == nil {
		c.budget = newRetentionBudget(max)
	} else if c.budget.max <= 0 && max > 0 {
		c.budget.max = max
	}
	return c.budget
}

func (c *ShapePlanCache) isTruncated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.truncated
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
