package catalog

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/catalog/fieldcatalog"

	"github.com/calypr/loom/internal/semantic"
)

type FieldProfileIdentity struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	ResourceType      string
}

func NewFieldProfileIdentity(project, datasetGeneration, authResourcePath, resourceType string) FieldProfileIdentity {
	return FieldProfileIdentity{
		Project:           project,
		DatasetGeneration: strings.TrimSpace(datasetGeneration),
		AuthResourcePath:  authResourcePath,
		ResourceType:      resourceType,
	}
}

const (
	RelationshipCatalogCollection = "fhir_relationship_catalog"
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
	Project               string                                   `json:"project"`
	DatasetGeneration     string                                   `json:"dataset_generation,omitempty"`
	AuthResourcePath      string                                   `json:"auth_resource_path,omitempty"`
	ResourceType          string                                   `json:"resource_type"`
	Path                  string                                   `json:"path"`
	Kind                  string                                   `json:"kind"`
	DocCount              int64                                    `json:"doc_count"`
	MaxItems              int                                      `json:"max_items,omitempty"`
	SampleCount           int                                      `json:"sample_count"`
	DistinctValues        []string                                 `json:"distinct_values,omitempty"`
	DistinctTruncated     bool                                     `json:"distinct_truncated"`
	ExtensionValues       []fieldcatalog.ExtensionValueObservation `json:"extension_values,omitempty"`
	SemanticObservations  []semantic.Observation                   `json:"semantic_observations,omitempty"`
	PivotCandidate        bool                                     `json:"pivot_candidate"`
	PivotKind             string                                   `json:"pivot_kind,omitempty"`
	PivotColumns          []string                                 `json:"pivot_columns,omitempty"`
	PivotFamily           string                                   `json:"pivot_family,omitempty"`
	PivotColumnSelect     string                                   `json:"pivot_column_selector,omitempty"`
	PivotValueSelect      string                                   `json:"pivot_value_selector,omitempty"`
	PivotItemSource       string                                   `json:"pivot_item_source,omitempty"`
	PivotItemResourceType string                                   `json:"pivot_item_resource_type,omitempty"`
	PivotValueSelectors   []string                                 `json:"pivot_value_selectors,omitempty"`
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

type PopulatedReferenceOptions struct {
	Project string

	DatasetGeneration string

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
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation"`
	AuthResourcePath  string `json:"auth_resource_path"`
	FromType          string `json:"from_type"`
	Label             string `json:"label"`
	ToType            string `json:"to_type"`
}
