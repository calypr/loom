package arango

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	SemanticCoverageCollection                  = "fhir_semantic_coverage"
	semanticCoverageDefaultBatchSize            = 500
	semanticCoverageMaximumBatchSize            = 500
	semanticCoverageMaximumPageSize             = 500
	semanticCoverageMaximumRowBytes             = 1 << 20
	semanticCoverageMaximumWriteBatchBytes      = 8 << 20
	semanticCoverageMaximumEnvelopeReadBytes    = 8 << 20
	semanticCoverageMaximumEnvelopeReadRows     = semanticCoverageMaximumEnvelopeReadBytes / semanticCoverageMaximumRowBytes
	semanticCoverageMaximumPartitions           = 10000
	semanticCoverageMaximumResourceTypes        = 1000
	semanticCoverageMaximumSummaries            = 100000
	semanticCoverageMaximumReferencesPerSummary = 1000
)

var (
	ErrInvalidSemanticCoverageRead          = errors.New("invalid semantic coverage read request")
	ErrSemanticCoverageRowTooLarge          = errors.New("semantic coverage row exceeds the storage limit")
	ErrSemanticCoverageSourceSetIncomplete  = errors.New("semantic coverage source set is incomplete")
	ErrSemanticCoverageLookupTooLarge       = errors.New("semantic coverage lookup exceeds its row limit")
	ErrSemanticCoverageMaterializationLimit = errors.New("semantic coverage materialization exceeds its working-set limit")
	ErrSemanticCoverageSourceBatchLimit     = errors.New("semantic coverage source batch exceeds its bounded write limit")
	ErrSemanticCoverageResourceExpectation  = errors.New("semantic coverage resource source-set expectation is invalid or incomplete")
)

type SemanticCoverageBuildState string

const (
	SemanticCoverageBuildBuilding    SemanticCoverageBuildState = "BUILDING"
	SemanticCoverageBuildFailed      SemanticCoverageBuildState = "FAILED"
	SemanticCoverageBuildUnavailable SemanticCoverageBuildState = "UNAVAILABLE"
	SemanticCoverageBuildComplete    SemanticCoverageBuildState = "COMPLETE"
	SemanticCoverageBuildPartial     SemanticCoverageBuildState = "PARTIAL"
	SemanticCoverageBuildMissing     SemanticCoverageBuildState = "MISSING"
)

type SemanticCoverageCandidateStatus string

const (
	SemanticCoverageCandidateGroupable   SemanticCoverageCandidateStatus = "GROUPABLE"
	SemanticCoverageCandidateKeepRaw     SemanticCoverageCandidateStatus = "KEEP_RAW"
	SemanticCoverageCandidateUnavailable SemanticCoverageCandidateStatus = "UNAVAILABLE"
)

type SemanticCoverageCandidatePath struct {
	ResourceType  string
	CanonicalPath string
}

type SemanticCoverageLookupOptions struct {
	Project                       string
	DatasetGeneration             string
	BuildID                       string
	SchemaIdentity                string
	SourceKind                    string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted bool
	Candidates                    []SemanticCoverageCandidatePath
}

type SemanticCoverageCandidateResult struct {
	ResourceType   string
	CanonicalPath  string
	CoverageID     string
	Status         SemanticCoverageCandidateStatus
	ResourceState  SemanticCoverageBuildState
	ResourceReason string
	Reason         catalog.SemanticCoverageReason
	References     []catalog.SemanticCoverageReference
}

type SemanticCoverageLookupResult struct {
	State      SemanticCoverageBuildState
	Reason     string
	Candidates []SemanticCoverageCandidateResult
}

type SemanticCoverageBuildIdentity struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	SchemaIdentity    string
	SourceKind        string
}

type SemanticCoverageResourceExpectation struct {
	ResourceType    string
	SourceExists    bool
	Terminal        bool
	SourceCount     int64
	SourceSetDigest string
	Partitions      []SemanticCoverageSourcePartitionExpectation
}

type SemanticCoverageSourcePartitionExpectation struct {
	AuthResourcePath string
	SourceCount      int64
	SourceSetDigest  string
}

// SemanticCoverageSourceSetAccumulator keeps fixed-size count and digest
// state per authorization partition; it never retains source identifiers.
type SemanticCoverageSourceSetAccumulator struct {
	mu           sync.Mutex
	resourceType string
	sourceExists bool
	sourceCount  int64
	partitions   map[string]*semanticCoverageSourceSetDigest
	failed       error
}

func NewSemanticCoverageSourceSetAccumulator(resourceType string, sourceExists bool) *SemanticCoverageSourceSetAccumulator {
	return &SemanticCoverageSourceSetAccumulator{
		resourceType: resourceType,
		sourceExists: sourceExists,
		partitions:   make(map[string]*semanticCoverageSourceSetDigest),
	}
}

func (a *SemanticCoverageSourceSetAccumulator) AddSource(sourceID, authResourcePath string) error {
	if a == nil {
		return ErrSemanticCoverageResourceExpectation
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed != nil {
		return a.failed
	}
	if strings.TrimSpace(a.resourceType) == "" || strings.TrimSpace(sourceID) == "" || !a.sourceExists {
		a.failed = ErrSemanticCoverageResourceExpectation
		return a.failed
	}
	partition := a.partitions[authResourcePath]
	if partition == nil {
		if len(a.partitions) >= semanticCoverageMaximumPartitions {
			a.failed = fmt.Errorf("%w: authorization partition limit exceeded", ErrSemanticCoverageResourceExpectation)
			return a.failed
		}
		partition = &semanticCoverageSourceSetDigest{}
		a.partitions[authResourcePath] = partition
	}
	if a.sourceCount == int64(^uint64(0)>>1) || partition.count == int64(^uint64(0)>>1) {
		a.failed = fmt.Errorf("%w: source count overflow", ErrSemanticCoverageResourceExpectation)
		return a.failed
	}
	identityDigest := sha256.Sum256([]byte(semanticCoverageDocumentKey("resource-source-identity/v1", a.resourceType, sourceID, authResourcePath)))
	addSemanticCoverageDigest(&partition.sum, identityDigest)
	partition.count++
	a.sourceCount++
	return nil
}

func (a *SemanticCoverageSourceSetAccumulator) Snapshot(terminal bool) (SemanticCoverageResourceExpectation, error) {
	if a == nil {
		return SemanticCoverageResourceExpectation{}, ErrSemanticCoverageResourceExpectation
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed != nil {
		return SemanticCoverageResourceExpectation{}, a.failed
	}
	if strings.TrimSpace(a.resourceType) == "" {
		return SemanticCoverageResourceExpectation{}, ErrSemanticCoverageResourceExpectation
	}
	result := SemanticCoverageResourceExpectation{
		ResourceType: a.resourceType, SourceExists: a.sourceExists, Terminal: terminal,
		SourceCount: a.sourceCount, Partitions: make([]SemanticCoverageSourcePartitionExpectation, 0, len(a.partitions)),
	}
	if !a.sourceExists {
		if a.sourceCount != 0 || len(a.partitions) != 0 {
			return SemanticCoverageResourceExpectation{}, ErrSemanticCoverageResourceExpectation
		}
		return result, nil
	}
	for authPath, digest := range a.partitions {
		result.Partitions = append(result.Partitions, SemanticCoverageSourcePartitionExpectation{
			AuthResourcePath: authPath, SourceCount: digest.count,
			SourceSetDigest: semanticCoveragePartitionDigest(a.resourceType, authPath, digest),
		})
	}
	sort.Slice(result.Partitions, func(i, j int) bool {
		return result.Partitions[i].AuthResourcePath < result.Partitions[j].AuthResourcePath
	})
	result.SourceSetDigest = semanticCoverageResourceDigest(result.ResourceType, result.Partitions)
	return result, nil
}

type semanticCoverageSourceSetDigest struct {
	count int64
	sum   [sha256.Size]byte
}

func addSemanticCoverageDigest(target *[sha256.Size]byte, source [sha256.Size]byte) {
	carry := uint16(0)
	for index := len(target) - 1; index >= 0; index-- {
		total := uint16(target[index]) + uint16(source[index]) + carry
		target[index] = byte(total)
		carry = total >> 8
	}
}

func semanticCoveragePartitionDigest(resourceType, authPath string, digest *semanticCoverageSourceSetDigest) string {
	return semanticCoverageDocumentKey(
		"resource-source-partition/v1", resourceType, authPath,
		fmt.Sprint(digest.count), hex.EncodeToString(digest.sum[:]),
	)
}

func semanticCoverageResourceDigest(resourceType string, partitions []SemanticCoverageSourcePartitionExpectation) string {
	parts := []string{"resource-source-set/v1", resourceType}
	for _, partition := range partitions {
		parts = append(parts, partition.AuthResourcePath, fmt.Sprint(partition.SourceCount), partition.SourceSetDigest)
	}
	return semanticCoverageDocumentKey(parts...)
}

type SemanticCoverageWriteStats struct {
	Rows               int64
	SourceSetsReplaced int64
	// SerializedBytes counts encoded JSON document bytes acknowledged by the
	// writer; it does not estimate Arango storage or index overhead.
	SerializedBytes int64
}

type SemanticCoveragePage struct {
	Rows    []map[string]any
	HasMore bool
}

type semanticCoverageStoredDocument struct {
	Key                          string                             `json:"_key"`
	RecordKind                   string                             `json:"record_kind"`
	BuildState                   SemanticCoverageBuildState         `json:"build_state,omitempty"`
	ResourceState                SemanticCoverageBuildState         `json:"resource_state,omitempty"`
	CoverageStorageFormatVersion int                                `json:"coverage_storage_format_version,omitempty"`
	BuildReason                  string                             `json:"build_reason,omitempty"`
	SourceSetDigest              string                             `json:"source_set_digest,omitempty"`
	SourcePartitionCount         int64                              `json:"source_partition_count,omitempty"`
	MaterializedPartitionCount   int64                              `json:"materialized_partition_count,omitempty"`
	SourceEnumerationComplete    bool                               `json:"source_enumeration_complete,omitempty"`
	CoverageRuleVersion          int                                `json:"coverage_rule_version,omitempty"`
	SummaryKind                  string                             `json:"summary_kind,omitempty"`
	OwnerCanonicalPath           string                             `json:"owner_canonical_path,omitempty"`
	Project                      string                             `json:"project"`
	DatasetGeneration            string                             `json:"dataset_generation"`
	BuildID                      string                             `json:"build_id"`
	SourceKind                   string                             `json:"source_kind"`
	AuthResourcePath             string                             `json:"auth_resource_path"`
	SchemaIdentity               string                             `json:"schema_identity"`
	ResourceType                 string                             `json:"resource_type"`
	ResourceBoundarySignature    string                             `json:"resource_boundary_signature,omitempty"`
	SourceID                     string                             `json:"source_id"`
	CanonicalPath                string                             `json:"canonical_path"`
	ExpectedResourceTypes        []string                           `json:"expected_resource_types,omitempty"`
	Counts                       map[string]int64                   `json:"counts,omitempty"`
	Paths                        []semanticCoverageEnvelopePath     `json:"paths,omitempty"`
	Path                         *semanticCoverageStoredPath        `json:"path,omitempty"`
	Diagnostics                  []semanticCoverageStoredDiagnostic `json:"diagnostics,omitempty"`
	SourceRecordCount            int64                              `json:"source_record_count,omitempty"`
	ExpectedSourceCount          int64                              `json:"expected_source_count,omitempty"`
	MaterializedSourceCount      int64                              `json:"materialized_source_count,omitempty"`
	SummaryCount                 int64                              `json:"summary_count,omitempty"`
	Complete                     bool                               `json:"complete"`
	CountsTruncated              bool                               `json:"counts_truncated,omitempty"`
	FacetsTruncated              bool                               `json:"facets_truncated,omitempty"`
	ReferencesTruncated          bool                               `json:"references_truncated,omitempty"`
	DiagnosticLocatorsTruncated  bool                               `json:"diagnostic_locators_truncated,omitempty"`
	ReconciliationTruncated      bool                               `json:"reconciliation_truncated,omitempty"`
	IncompleteGaps               []string                           `json:"incomplete_gaps,omitempty"`
	Groupable                    bool                               `json:"groupable,omitempty"`
	Status                       SemanticCoverageCandidateStatus    `json:"status,omitempty"`
	Reason                       string                             `json:"reason,omitempty"`
}

type semanticCoverageStoredPath struct {
	ResourceBoundarySignature string                            `json:"resource_boundary_signature,omitempty"`
	Kind                      string                            `json:"kind"`
	Disposition               string                            `json:"disposition"`
	Reason                    string                            `json:"reason,omitempty"`
	OccurrenceCount           int64                             `json:"occurrence_count"`
	OwnerCount                int64                             `json:"owner_count"`
	CodingCount               int64                             `json:"coding_count"`
	PresenceCounts            map[string]int64                  `json:"presence_counts,omitempty"`
	CollectionCounts          map[string]int64                  `json:"collection_counts,omitempty"`
	FacetPresenceCounts       map[string]map[string]int64       `json:"facet_presence_counts,omitempty"`
	Facets                    []semanticCoverageStoredFacet     `json:"facets,omitempty"`
	References                []semanticCoverageStoredReference `json:"references,omitempty"`
	OwnerPathWitnesses        []string                          `json:"owner_path_witnesses,omitempty"`
	WitnessesTruncated        bool                              `json:"witnesses_truncated,omitempty"`
	CountsTruncated           bool                              `json:"counts_truncated,omitempty"`
	FacetsTruncated           bool                              `json:"facets_truncated,omitempty"`
	ReferencesTruncated       bool                              `json:"references_truncated,omitempty"`
}

type semanticCoverageStoredFacet struct {
	Path                      string                            `json:"path"`
	ResourceBoundarySignature string                            `json:"resource_boundary_signature,omitempty"`
	Kind                      string                            `json:"kind"`
	Disposition               string                            `json:"disposition"`
	Reason                    string                            `json:"reason,omitempty"`
	OccurrenceCount           int64                             `json:"occurrence_count"`
	PresenceCounts            map[string]int64                  `json:"presence_counts,omitempty"`
	References                []semanticCoverageStoredReference `json:"references,omitempty"`
	ReferencesTruncated       bool                              `json:"references_truncated,omitempty"`
}

type semanticCoverageStoredReference struct {
	Kind          string                                 `json:"kind"`
	ConceptID     string                                 `json:"concept_id,omitempty"`
	BindingID     string                                 `json:"binding_id,omitempty"`
	Family        *semanticCoverageStoredFamilyReference `json:"family,omitempty"`
	CandidatePath string                                 `json:"candidate_path,omitempty"`
	ResourceType  string                                 `json:"resource_type,omitempty"`
}

type semanticCoverageStoredFamilyReference struct {
	FamilyID     string `json:"family_id"`
	BindingID    string `json:"binding_id"`
	SourceKind   string `json:"source_kind"`
	ResourceType string `json:"resource_type"`
	SourcePath   string `json:"source_path"`
	System       string `json:"system"`
	Version      string `json:"version"`
}

type semanticCoverageStoredDiagnostic struct {
	Code          string `json:"code"`
	CanonicalPath string `json:"canonical_path,omitempty"`
	OwnerPath     string `json:"owner_path,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Explanation   string `json:"explanation,omitempty"`
}

// SemanticCoverageCollectionSpec describes the persistent indexes shared by
// generation ingestion and retained-source backfill.
func SemanticCoverageCollectionSpec() store.CollectionSpec {
	return store.CollectionSpec{
		Name: SemanticCoverageCollection,
		Indexes: [][]string{
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "resource_type", "_key"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "summary_kind", "resource_type", "canonical_path", "resource_boundary_signature", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "resource_type", "auth_resource_path", "_key"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "_key"},
		},
	}
}

// EncodeSemanticCoverageContribution encodes one bounded source envelope. Its
// key is the physical source locator and excludes authorization so replay can
// atomically replace a changed auth path or source shape.
func EncodeSemanticCoverageContribution(buildID string, contribution catalog.SemanticCoverageSourceContribution) ([]json.RawMessage, error) {
	encoded, err := EncodeSemanticCoverageSourceEnvelope(buildID, contribution)
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{encoded}, nil
}

func semanticCoveragePathAggregateKey(path catalog.SemanticCoveragePathAggregate) string {
	return semanticCoverageDocumentKey(path.CanonicalPath, path.ResourceBoundarySignature, string(path.Kind), string(path.Disposition), string(path.Reason))
}

func semanticCoverageStoredReferences(references []catalog.SemanticCoverageReference) []semanticCoverageStoredReference {
	result := make([]semanticCoverageStoredReference, 0, len(references))
	for _, reference := range references {
		stored := semanticCoverageStoredReference{
			Kind: string(reference.Kind), ConceptID: reference.ConceptID, BindingID: reference.BindingID,
			CandidatePath: reference.CandidatePath, ResourceType: reference.ResourceType,
		}
		if reference.Kind == catalog.SemanticCoverageFamilyReference {
			stored.Family = &semanticCoverageStoredFamilyReference{
				FamilyID: reference.Family.FamilyID, BindingID: reference.Family.BindingID,
				SourceKind: reference.Family.SourceKind, ResourceType: reference.Family.ResourceType,
				SourcePath: reference.Family.SourcePath, System: reference.Family.System, Version: reference.Family.Version,
			}
		}
		result = append(result, stored)
	}
	return result
}

func (s *Store) WriteSemanticCoverageContributions(ctx context.Context, buildID string, contributions []catalog.SemanticCoverageSourceContribution, batchSize int) (SemanticCoverageWriteStats, error) {
	if len(contributions) == 0 {
		return SemanticCoverageWriteStats{}, nil
	}
	batchSize = semanticCoverageBatchSize(batchSize)
	stats := SemanticCoverageWriteStats{}
	for start := 0; start < len(contributions); start += batchSize {
		end := min(start+batchSize, len(contributions))
		chunk := contributions[start:end]
		seenSourceIDs := make(map[semanticCoverageLocatorKey]string, len(chunk))
		for index, contribution := range chunk {
			if contribution.Identity.Project != chunk[0].Identity.Project ||
				contribution.Identity.DatasetGeneration != chunk[0].Identity.DatasetGeneration ||
				contribution.Identity.SourceKind != chunk[0].Identity.SourceKind ||
				contribution.Identity.SchemaIdentity != chunk[0].Identity.SchemaIdentity {
				return stats, fmt.Errorf("semantic coverage source %d has a mixed build partition", start+index)
			}
			sourceKey := semanticCoverageLocatorKey{sourceID: contribution.Identity.SourceID, resourceType: contribution.Identity.ResourceType}
			if previousAuthPath, exists := seenSourceIDs[sourceKey]; exists {
				if previousAuthPath != contribution.Identity.AuthResourcePath {
					return stats, fmt.Errorf("semantic coverage source ID %q is ambiguous across authorization paths", contribution.Identity.SourceID)
				}
				return stats, fmt.Errorf("duplicate semantic coverage source ID %q in one write batch", contribution.Identity.SourceID)
			}
			seenSourceIDs[sourceKey] = contribution.Identity.AuthResourcePath
		}
		batch := make([]json.RawMessage, 0, min(batchSize, semanticCoverageMaximumEnvelopeReadRows))
		batchBytes := 0
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			written, err := s.writeSemanticCoverageDocuments(ctx, batch, batchSize)
			stats.Rows += written.Rows
			stats.SourceSetsReplaced += written.Rows
			stats.SerializedBytes += written.SerializedBytes
			batch = batch[:0]
			batchBytes = 0
			return err
		}
		for index, contribution := range chunk {
			encoded, err := EncodeSemanticCoverageContribution(buildID, contribution)
			if err != nil {
				return stats, fmt.Errorf("encode semantic coverage source %d: %w", start+index, err)
			}
			document := encoded[0]
			if len(batch) > 0 && (len(batch) >= batchSize || batchBytes+len(document) > semanticCoverageMaximumWriteBatchBytes) {
				if err := flush(); err != nil {
					return stats, fmt.Errorf("replace semantic coverage source envelopes: %w", err)
				}
			}
			batch = append(batch, document)
			batchBytes += len(document)
			if len(batch) >= batchSize || batchBytes >= semanticCoverageMaximumWriteBatchBytes {
				if err := flush(); err != nil {
					return stats, fmt.Errorf("replace semantic coverage source envelopes: %w", err)
				}
			}
		}
		if err := flush(); err != nil {
			return stats, fmt.Errorf("replace semantic coverage source envelopes: %w", err)
		}
	}
	return stats, nil
}

type semanticCoverageLocatorKey struct {
	sourceID     string
	resourceType string
}

func validateSemanticCoverageContribution(buildID string, contribution catalog.SemanticCoverageSourceContribution) error {
	identity := contribution.Identity
	if strings.TrimSpace(buildID) == "" || contribution.BuildID != buildID ||
		strings.TrimSpace(identity.Project) == "" ||
		strings.TrimSpace(identity.DatasetGeneration) == "" ||
		catalog.NormalizeDatasetGeneration(identity.DatasetGeneration) != identity.DatasetGeneration ||
		strings.TrimSpace(identity.SourceKind) == "" ||
		strings.TrimSpace(identity.SourceID) == "" ||
		strings.TrimSpace(identity.ResourceType) == "" || contribution.SourceRecordCount != 1 ||
		(identity.SchemaIdentity == "" && contribution.Complete) {
		return errors.New("semantic coverage source identity or build is incomplete")
	}
	return nil
}

func validateSemanticCoveragePath(path catalog.SemanticCoveragePathAggregate) error {
	if strings.TrimSpace(path.CanonicalPath) == "" || path.Kind == "" || path.Disposition == "" ||
		path.OccurrenceCount < 0 || path.OwnerCount < 0 || path.CodingCount < 0 {
		return errors.New("semantic coverage path aggregate is incomplete")
	}
	return nil
}

func semanticCoverageGapStrings(gaps []catalog.SemanticCoverageCensusGap) []string {
	if len(gaps) == 0 {
		return nil
	}
	result := make([]string, len(gaps))
	for index, gap := range gaps {
		result[index] = string(gap)
	}
	sort.Strings(result)
	return result
}

func semanticCoverageCountsDocument(counts catalog.SemanticCoverageCounts) map[string]int64 {
	return map[string]int64{
		"codeable_concept_owners":                counts.CodeableConceptOwners,
		"populated_codeable_concept_values":      counts.PopulatedCodeableConceptValues,
		"null_codeable_concept_owners":           counts.NullCodeableConceptOwners,
		"empty_codeable_concept_owners":          counts.EmptyCodeableConceptOwners,
		"coding_occurrences":                     counts.CodingOccurrences,
		"populated_coding_values":                counts.PopulatedCodingValues,
		"null_coding_occurrences":                counts.NullCodingOccurrences,
		"empty_coding_occurrences":               counts.EmptyCodingOccurrences,
		"missing_codeable_concept_facets":        counts.MissingCodeableConceptFacets,
		"null_codeable_concept_facets":           counts.NullCodeableConceptFacets,
		"empty_codeable_concept_facets":          counts.EmptyCodeableConceptFacets,
		"blank_codeable_concept_facets":          counts.BlankCodeableConceptFacets,
		"populated_codeable_concept_facets":      counts.PopulatedCodeableConceptFacets,
		"missing_coding_facets":                  counts.MissingCodingFacets,
		"null_coding_facets":                     counts.NullCodingFacets,
		"empty_coding_facets":                    counts.EmptyCodingFacets,
		"blank_coding_facets":                    counts.BlankCodingFacets,
		"populated_coding_facets":                counts.PopulatedCodingFacets,
		"empty_coding_collections":               counts.EmptyCodingCollections,
		"null_coding_collections":                counts.NullCodingCollections,
		"populated_coding_collections":           counts.PopulatedCodingCollections,
		"empty_codeable_concept_collections":     counts.EmptyCodeableConceptCollections,
		"null_codeable_concept_collections":      counts.NullCodeableConceptCollections,
		"populated_codeable_concept_collections": counts.PopulatedCodeableConceptCollections,
		"identifier_occurrences":                 counts.IdentifierOccurrences,
		"null_identifier_occurrences":            counts.NullIdentifierOccurrences,
		"empty_identifier_occurrences":           counts.EmptyIdentifierOccurrences,
		"extension_value_occurrences":            counts.ExtensionValueOccurrences,
		"null_extension_value_occurrences":       counts.NullExtensionValueOccurrences,
		"empty_extension_value_occurrences":      counts.EmptyExtensionValueOccurrences,
		"primitive_code_occurrences":             counts.PrimitiveCodeOccurrences,
		"null_primitive_code_occurrences":        counts.NullPrimitiveCodeOccurrences,
		"empty_primitive_code_occurrences":       counts.EmptyPrimitiveCodeOccurrences,
		"unknown_structure_occurrences":          counts.UnknownStructureOccurrences,
		"null_unknown_structures":                counts.NullUnknownStructures,
		"empty_unknown_structures":               counts.EmptyUnknownStructures,
	}
}

func semanticCoveragePathDocument(path catalog.SemanticCoveragePathAggregate) *semanticCoverageStoredPath {
	result := &semanticCoverageStoredPath{
		ResourceBoundarySignature: path.ResourceBoundarySignature,
		Kind:                      string(path.Kind),
		Disposition:               string(path.Disposition),
		Reason:                    string(path.Reason),
		OccurrenceCount:           path.OccurrenceCount,
		OwnerCount:                path.OwnerCount,
		CodingCount:               path.CodingCount,
		PresenceCounts:            semanticCoverageStringCounts(path.PresenceCounts),
		CollectionCounts:          semanticCoverageStringCounts(path.CollectionCounts),
		FacetPresenceCounts:       make(map[string]map[string]int64, len(path.FacetPresenceCounts)),
		Facets:                    make([]semanticCoverageStoredFacet, 0, len(path.Facets)),
		References:                make([]semanticCoverageStoredReference, 0, len(path.References)),
		OwnerPathWitnesses:        append([]string(nil), path.OwnerPathWitnesses...),
		WitnessesTruncated:        path.WitnessesTruncated,
		CountsTruncated:           path.CountsTruncated,
		FacetsTruncated:           path.FacetsTruncated,
		ReferencesTruncated:       path.ReferencesTruncated,
	}
	for facetPath, counts := range path.FacetPresenceCounts {
		result.FacetPresenceCounts[facetPath] = semanticCoverageStringCounts(counts)
	}
	for _, facet := range path.Facets {
		facetReferences := append([]catalog.SemanticCoverageReference(nil), facet.References...)
		sort.Slice(facetReferences, func(i, j int) bool {
			return semanticCoverageReferenceKey(facetReferences[i]) < semanticCoverageReferenceKey(facetReferences[j])
		})
		result.Facets = append(result.Facets, semanticCoverageStoredFacet{
			Path: facet.Path, ResourceBoundarySignature: facet.ResourceBoundarySignature, Kind: facet.Kind, Disposition: string(facet.Disposition), Reason: string(facet.Reason), OccurrenceCount: facet.OccurrenceCount,
			PresenceCounts: semanticCoverageStringCounts(facet.PresenceCounts), References: semanticCoverageStoredReferences(facetReferences), ReferencesTruncated: facet.ReferencesTruncated,
		})
	}
	sort.Slice(result.Facets, func(i, j int) bool {
		if result.Facets[i].Path != result.Facets[j].Path {
			return result.Facets[i].Path < result.Facets[j].Path
		}
		if result.Facets[i].ResourceBoundarySignature != result.Facets[j].ResourceBoundarySignature {
			return result.Facets[i].ResourceBoundarySignature < result.Facets[j].ResourceBoundarySignature
		}
		if result.Facets[i].Kind != result.Facets[j].Kind {
			return result.Facets[i].Kind < result.Facets[j].Kind
		}
		if result.Facets[i].Disposition != result.Facets[j].Disposition {
			return result.Facets[i].Disposition < result.Facets[j].Disposition
		}
		return result.Facets[i].Reason < result.Facets[j].Reason
	})
	references := append([]catalog.SemanticCoverageReference(nil), path.References...)
	sort.Slice(references, func(i, j int) bool {
		return semanticCoverageReferenceKey(references[i]) < semanticCoverageReferenceKey(references[j])
	})
	result.References = semanticCoverageStoredReferences(references)
	sort.Slice(result.References, func(i, j int) bool {
		left, right := result.References[i], result.References[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.BindingID != right.BindingID {
			return left.BindingID < right.BindingID
		}
		if left.ConceptID != right.ConceptID {
			return left.ConceptID < right.ConceptID
		}
		if left.Family != nil || right.Family != nil {
			if left.Family == nil {
				return true
			}
			if right.Family == nil {
				return false
			}
			if left.Family.FamilyID != right.Family.FamilyID {
				return left.Family.FamilyID < right.Family.FamilyID
			}
			if left.Family.System != right.Family.System {
				return left.Family.System < right.Family.System
			}
			if left.Family.Version != right.Family.Version {
				return left.Family.Version < right.Family.Version
			}
		}
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		return left.CandidatePath < right.CandidatePath
	})
	sort.Strings(result.OwnerPathWitnesses)
	return result
}

func semanticCoverageStringCounts(counts map[catalog.SemanticCoveragePresence]int64) map[string]int64 {
	if len(counts) == 0 {
		return nil
	}
	result := make(map[string]int64, len(counts))
	for presence, count := range counts {
		result[string(presence)] = count
	}
	return result
}

// writeSemanticCoverageDocuments replaces deterministic rows in bounded
// batches, so replaying a source before its checkpoint advances is idempotent.
func (s *Store) writeSemanticCoverageDocuments(ctx context.Context, documents []json.RawMessage, batchSize int) (SemanticCoverageWriteStats, error) {
	if len(documents) == 0 {
		return SemanticCoverageWriteStats{}, nil
	}
	batchSize = semanticCoverageBatchSize(batchSize)
	for index, document := range documents {
		if err := validateSemanticCoverageDocument(document); err != nil {
			return SemanticCoverageWriteStats{}, fmt.Errorf("validate semantic coverage document %d: %w", index, err)
		}
	}

	stats := SemanticCoverageWriteStats{}
	batch := make([]json.RawMessage, 0, batchSize)
	batchBytes := 0
	flush := func(start int) error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.client.InsertBatchRaw(ctx, SemanticCoverageCollection, batch, true, ""); err != nil {
			return fmt.Errorf("write semantic coverage rows starting at %d: %w", start, err)
		}
		for _, document := range batch {
			stats.SerializedBytes += int64(len(document))
		}
		stats.Rows += int64(len(batch))
		batch = batch[:0]
		batchBytes = 0
		return nil
	}
	for index, document := range documents {
		if len(batch) > 0 && (len(batch) >= batchSize || batchBytes+len(document) > semanticCoverageMaximumWriteBatchBytes) {
			if err := flush(index - len(batch)); err != nil {
				return stats, err
			}
		}
		batch = append(batch, document)
		batchBytes += len(document)
		if len(batch) >= batchSize || batchBytes >= semanticCoverageMaximumWriteBatchBytes {
			if err := flush(index - len(batch) + 1); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(len(documents) - len(batch)); err != nil {
		return stats, err
	}
	return stats, nil
}

func semanticCoverageBatchSize(requested int) int {
	if requested <= 0 {
		return semanticCoverageDefaultBatchSize
	}
	return min(requested, semanticCoverageMaximumBatchSize)
}

// BeginSemanticCoverageBuild publishes BUILDING before any source-set replay
// can invalidate previously completed proof for this exact build.
func (s *Store) BeginSemanticCoverageBuild(ctx context.Context, identity SemanticCoverageBuildIdentity) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildBuilding, "", 0, 0, 0)
}

// BeginSemanticCoverageResource invalidates one resource publication before
// its sources or summaries change. Other resource certificates remain stored,
// but the enclosing global BUILDING marker keeps all lookups fail-closed.
func (s *Store) BeginSemanticCoverageResource(ctx context.Context, identity SemanticCoverageBuildIdentity, resourceType string) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	if strings.TrimSpace(resourceType) == "" {
		return ErrInvalidSemanticCoverageRead
	}
	if err := s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildBuilding, "", SemanticCoverageResourceExpectation{}, 0, 0); err != nil {
		return err
	}
	if err := s.client.ExecuteAQL(ctx, deleteSemanticCoverageResourceSummariesAQL, map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "source_kind": identity.SourceKind,
		"schema_identity": identity.SchemaIdentity, "resource_type": resourceType,
	}); err != nil {
		return fmt.Errorf("clear semantic coverage rows for resource %s: %w", resourceType, err)
	}
	return nil
}

func (s *Store) MarkSemanticCoverageResourceMissing(ctx context.Context, identity SemanticCoverageBuildIdentity, resourceType string) error {
	if err := s.BeginSemanticCoverageResource(ctx, identity, resourceType); err != nil {
		return err
	}
	return s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildMissing, "SOURCE_COLLECTION_MISSING", SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
}

// FailSemanticCoverageBuild keeps execution failures distinct from missing
// source proof so readers can report the real failed state.
func (s *Store) FailSemanticCoverageBuild(ctx context.Context, identity SemanticCoverageBuildIdentity, reason string) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildFailed, boundedSemanticCoverageReason(reason), 0, 0, 0)
}

// CompleteSemanticCoverageBuild is retained for older callers, but source
// counts alone cannot prove the complete resource set. It therefore cannot
// publish a groupable build. New callers must use FinalizeSemanticCoverageBuild.
func (s *Store) CompleteSemanticCoverageBuild(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	expectedSourceCount int64,
	sourceAvailabilityProven bool,
	unavailableReason string,
	_ int,
) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	if expectedSourceCount < 0 {
		return ErrInvalidSemanticCoverageRead
	}
	if identity.SchemaIdentity == "" {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE", expectedSourceCount, 0, 0)
	}
	if !sourceAvailabilityProven {
		if unavailableReason == "" {
			unavailableReason = "SOURCE_AVAILABILITY_UNPROVEN"
		}
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, boundedSemanticCoverageReason(unavailableReason), expectedSourceCount, 0, 0)
	}
	if unavailableReason == "" {
		unavailableReason = "RESOURCE_CERTIFICATES_REQUIRED"
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildPartial, boundedSemanticCoverageReason(unavailableReason), expectedSourceCount, 0, 0)
}

// FinalizeSemanticCoverageBuild publishes the global build marker only after
// every expected resource certificate is present and valid. Resource
// summaries and authorization partitions are written by
// CompleteSemanticCoverageResource before their COMPLETE markers.
func (s *Store) FinalizeSemanticCoverageBuild(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	expectedResourceTypes []string,
) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	resources, err := normalizeSemanticCoverageResourceTypes(expectedResourceTypes)
	if err != nil {
		return ErrSemanticCoverageResourceExpectation
	}
	if identity.SchemaIdentity == "" {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE", 0, 0, 0, resources)
	}
	if len(resources) == 0 {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "EXPECTED_RESOURCE_SET_EMPTY", 0, 0, 0, resources)
	}

	markers := make(map[string]semanticCoverageStoredDocument, len(resources))
	err = s.scanSemanticCoverageDocuments(ctx, identity, "resource", func(marker semanticCoverageStoredDocument) error {
		if marker.RecordKind != "resource" || marker.ResourceType == "" || marker.Key != semanticCoverageResourceKey(identity, marker.ResourceType) ||
			marker.Project != identity.Project || marker.DatasetGeneration != identity.DatasetGeneration || marker.BuildID != identity.BuildID ||
			marker.SourceKind != identity.SourceKind || marker.SchemaIdentity != identity.SchemaIdentity ||
			marker.CoverageRuleVersion != catalog.SemanticCoverageRuleVersion ||
			marker.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion {
			return ErrSemanticCoverageResourceExpectation
		}
		if !containsSortedSemanticCoverageResource(resources, marker.ResourceType) {
			return fmt.Errorf("%w: unexpected resource marker %q", ErrSemanticCoverageResourceExpectation, marker.ResourceType)
		}
		if _, duplicate := markers[marker.ResourceType]; duplicate {
			return fmt.Errorf("%w: duplicate resource marker %q", ErrSemanticCoverageResourceExpectation, marker.ResourceType)
		}
		markers[marker.ResourceType] = marker
		return nil
	})
	if err != nil {
		return fmt.Errorf("read semantic coverage resource certificates: %w", err)
	}
	partitionCounts := make(map[string]int64, len(resources))
	incompletePartitions := make(map[string]string)
	err = s.scanSemanticCoverageDocuments(ctx, identity, "partition", func(partition semanticCoverageStoredDocument) error {
		if partition.RecordKind != "partition" || partition.ResourceType == "" ||
			partition.Key != semanticCoveragePartitionDocumentKey(identity, partition.ResourceType, partition.AuthResourcePath) ||
			partition.Project != identity.Project || partition.DatasetGeneration != identity.DatasetGeneration ||
			partition.BuildID != identity.BuildID || partition.SourceKind != identity.SourceKind || partition.SchemaIdentity != identity.SchemaIdentity ||
			partition.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion {
			return ErrSemanticCoverageResourceExpectation
		}
		if !containsSortedSemanticCoverageResource(resources, partition.ResourceType) {
			return fmt.Errorf("%w: unexpected authorization partition for resource %q", ErrSemanticCoverageResourceExpectation, partition.ResourceType)
		}
		partitionCounts[partition.ResourceType]++
		if !validSemanticCoveragePartition(partition) {
			reason := partition.Reason
			if reason == "" {
				reason = "SOURCE_PARTITION_INCOMPLETE"
			}
			incompletePartitions[partition.ResourceType] = reason
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("read semantic coverage authorization partitions: %w", err)
	}

	state := SemanticCoverageBuildComplete
	reason := ""
	var expectedSources, materializedSources, summaryCount int64
	for _, resourceType := range resources {
		marker, found := markers[resourceType]
		if !found {
			state = SemanticCoverageBuildPartial
			if reason == "" {
				reason = "RESOURCE_CERTIFICATES_INCOMPLETE"
			}
			continue
		}
		if marker.ResourceState != SemanticCoverageBuildComplete || !marker.SourceEnumerationComplete ||
			marker.SourceSetDigest == "" || marker.ExpectedSourceCount != marker.MaterializedSourceCount ||
			marker.SourcePartitionCount != marker.MaterializedPartitionCount {
			state = SemanticCoverageBuildPartial
			if reason == "" {
				reason = marker.BuildReason
				if reason == "" {
					reason = "RESOURCE_CERTIFICATES_INCOMPLETE"
				}
			}
			continue
		}
		expectedSources += marker.ExpectedSourceCount
		materializedSources += marker.MaterializedSourceCount
		summaryCount += marker.SummaryCount
		if partitionCounts[resourceType] != marker.MaterializedPartitionCount || partitionCounts[resourceType] != marker.SourcePartitionCount {
			state = SemanticCoverageBuildPartial
			if reason == "" {
				reason = "RESOURCE_PARTITION_SET_MISMATCH"
			}
		}
		if partitionReason := incompletePartitions[resourceType]; partitionReason != "" {
			state = SemanticCoverageBuildPartial
			if reason == "" {
				reason = partitionReason
			}
		}
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, state, reason, expectedSources, materializedSources, summaryCount, resources)
}

func normalizeSemanticCoverageResourceTypes(resourceTypes []string) ([]string, error) {
	if len(resourceTypes) > semanticCoverageMaximumResourceTypes {
		return nil, ErrSemanticCoverageResourceExpectation
	}
	result := append([]string(nil), resourceTypes...)
	for _, resourceType := range result {
		if strings.TrimSpace(resourceType) == "" {
			return nil, ErrSemanticCoverageResourceExpectation
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, ErrSemanticCoverageResourceExpectation
		}
	}
	return result, nil
}

func containsSemanticCoverageResource(resources []string, candidate string) (int, bool) {
	index := sort.SearchStrings(resources, candidate)
	return index, index < len(resources) && resources[index] == candidate
}

func (s *Store) CompleteSemanticCoverageResource(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	expectation SemanticCoverageResourceExpectation,
	batchSize int,
) (returnErr error) {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	if strings.TrimSpace(expectation.ResourceType) == "" {
		return ErrSemanticCoverageResourceExpectation
	}
	if err := s.BeginSemanticCoverageResource(ctx, identity, expectation.ResourceType); err != nil {
		return err
	}
	failureReason := ""
	defer func() {
		if returnErr == nil {
			return
		}
		reason := failureReason
		if reason == "" {
			reason = returnErr.Error()
		}
		if failErr := s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, expectation.ResourceType, reason); failErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("mark semantic coverage resource failed: %w", failErr))
		}
	}()
	if identity.SchemaIdentity == "" {
		return s.writeSemanticCoverageResourceMarker(ctx, identity, expectation.ResourceType, SemanticCoverageBuildUnavailable, "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE", expectation, 0, 0)
	}
	if !expectation.SourceExists {
		if expectation.SourceCount != 0 || len(expectation.Partitions) != 0 {
			return ErrSemanticCoverageResourceExpectation
		}
		return s.writeSemanticCoverageResourceMarker(ctx, identity, expectation.ResourceType, SemanticCoverageBuildMissing, "SOURCE_COLLECTION_MISSING", expectation, 0, 0)
	}
	if !expectation.Terminal {
		return s.writeSemanticCoverageResourceMarker(ctx, identity, expectation.ResourceType, SemanticCoverageBuildUnavailable, "SOURCE_ENUMERATION_NOT_TERMINAL", expectation, 0, 0)
	}
	if err := validateSemanticCoverageResourceExpectation(expectation); err != nil {
		failureReason = "SOURCE_EXPECTATION_INVALID"
		return err
	}

	materializer := newSemanticCoverageMaterializer(identity)
	actual := NewSemanticCoverageSourceSetAccumulator(expectation.ResourceType, true)
	var actualSourceCount int64
	err := s.scanSemanticCoverageResourceDocuments(ctx, identity, expectation.ResourceType, semanticCoverageEnvelopeRecordKind, func(source semanticCoverageStoredDocument) error {
		if source.RecordKind != semanticCoverageEnvelopeRecordKind ||
			source.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion ||
			source.SourceID == "" || source.SourceRecordCount != 1 ||
			source.Project != identity.Project || source.DatasetGeneration != identity.DatasetGeneration ||
			source.BuildID != identity.BuildID || source.SourceKind != identity.SourceKind ||
			source.SchemaIdentity != identity.SchemaIdentity || source.ResourceType != expectation.ResourceType {
			return ErrSemanticCoverageSourceSetIncomplete
		}
		expectedKey := semanticCoverageSourceEnvelopeKey(catalog.SemanticCoverageSourceIdentity{
			Project: source.Project, DatasetGeneration: source.DatasetGeneration, AuthResourcePath: source.AuthResourcePath,
			SchemaIdentity: source.SchemaIdentity, SourceKind: source.SourceKind, SourceID: source.SourceID, ResourceType: source.ResourceType,
		}, identity.BuildID)
		if source.Key != expectedKey {
			return ErrSemanticCoverageSourceSetIncomplete
		}
		if err := actual.AddSource(source.SourceID, source.AuthResourcePath); err != nil {
			return err
		}
		actualSourceCount++
		return materializer.addEnvelope(source)
	})
	if err != nil {
		if errors.Is(err, ErrSemanticCoverageSourceSetIncomplete) {
			failureReason = "SOURCE_ENVELOPE_IDENTITY_MISMATCH"
			return err
		}
		return fmt.Errorf("enumerate semantic coverage source sentinels for %s: %w", expectation.ResourceType, err)
	}
	actualExpectation, err := actual.Snapshot(true)
	if err != nil {
		return err
	}
	if !sameSemanticCoverageResourceSourceSet(expectation, actualExpectation) {
		failureReason = "SOURCE_ENVELOPE_SET_MISMATCH"
		return ErrSemanticCoverageSourceSetIncomplete
	}
	stats, err := s.writeSemanticCoverageSummaries(ctx, materializer, batchSize)
	if err != nil {
		return fmt.Errorf("write semantic coverage resource summaries for %s: %w", expectation.ResourceType, err)
	}
	partitionStats, err := s.writeSemanticCoveragePartitions(ctx, materializer, batchSize)
	if err != nil {
		return fmt.Errorf("write semantic coverage resource partitions for %s: %w", expectation.ResourceType, err)
	}
	return s.writeSemanticCoverageResourceMarker(
		ctx, identity, expectation.ResourceType, SemanticCoverageBuildComplete, "", expectation,
		actualSourceCount, int64(len(materializer.partitionSources)), stats.Rows+partitionStats.Rows,
	)
}

func (s *Store) FailSemanticCoverageResource(ctx context.Context, identity SemanticCoverageBuildIdentity, resourceType, reason string) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	if strings.TrimSpace(resourceType) == "" {
		return ErrInvalidSemanticCoverageRead
	}
	return s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildFailed, boundedSemanticCoverageReason(reason), SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
}

func validateSemanticCoverageResourceExpectation(expectation SemanticCoverageResourceExpectation) error {
	if strings.TrimSpace(expectation.ResourceType) == "" || !expectation.SourceExists || !expectation.Terminal || expectation.SourceCount < 0 || len(expectation.Partitions) > semanticCoverageMaximumPartitions {
		return ErrSemanticCoverageResourceExpectation
	}
	var sourceCount int64
	seen := make(map[string]struct{}, len(expectation.Partitions))
	for _, partition := range expectation.Partitions {
		if partition.SourceCount <= 0 || strings.TrimSpace(partition.SourceSetDigest) == "" {
			return ErrSemanticCoverageResourceExpectation
		}
		if _, exists := seen[partition.AuthResourcePath]; exists {
			return ErrSemanticCoverageResourceExpectation
		}
		seen[partition.AuthResourcePath] = struct{}{}
		sourceCount += partition.SourceCount
	}
	if sourceCount != expectation.SourceCount || expectation.SourceSetDigest != semanticCoverageResourceDigest(expectation.ResourceType, expectation.Partitions) {
		return ErrSemanticCoverageResourceExpectation
	}
	return nil
}

func sameSemanticCoverageResourceSourceSet(expected, actual SemanticCoverageResourceExpectation) bool {
	if !expected.SourceExists || !expected.Terminal || !actual.SourceExists || !actual.Terminal ||
		expected.ResourceType != actual.ResourceType || expected.SourceCount != actual.SourceCount ||
		expected.SourceSetDigest != actual.SourceSetDigest || len(expected.Partitions) != len(actual.Partitions) {
		return false
	}
	for index := range expected.Partitions {
		if expected.Partitions[index] != actual.Partitions[index] {
			return false
		}
	}
	return true
}

func (s *Store) CompleteRetainedSemanticCoverageResource(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	resourceType string,
	pageSize int,
	batchSize int,
) (int64, error) {
	var certificationSourceRows int64
	if identity.SourceKind != catalog.SemanticInventorySourceRetained {
		return certificationSourceRows, ErrInvalidSemanticCoverageRead
	}
	if pageSize < 1 || pageSize > 1000 {
		return certificationSourceRows, ErrInvalidSemanticInventoryPage
	}
	if err := s.BeginSemanticCoverageResource(ctx, identity, resourceType); err != nil {
		return certificationSourceRows, err
	}
	if identity.SchemaIdentity == "" {
		return certificationSourceRows, s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildUnavailable, "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE", SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
	}
	exists, err := s.client.CollectionExists(ctx, resourceType)
	if err != nil {
		return certificationSourceRows, errors.Join(fmt.Errorf("check retained source collection %s: %w", resourceType, err), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_COLLECTION_READ_FAILED"))
	}
	if !exists {
		return certificationSourceRows, s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildMissing, "SOURCE_COLLECTION_MISSING", SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
	}
	revisionBefore, err := semanticCoverageCollectionRevision(ctx, s.client, resourceType)
	if err != nil {
		return certificationSourceRows, errors.Join(fmt.Errorf("read retained source collection %s revision: %w", resourceType, err), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_COLLECTION_REVISION_FAILED"))
	}
	accumulator := NewSemanticCoverageSourceSetAccumulator(resourceType, true)
	afterKey := ""
	for {
		page, readErr := s.ReadRetainedSemanticInventoryPage(ctx, identity.Project, identity.DatasetGeneration, resourceType, afterKey, pageSize)
		if errors.Is(readErr, ErrRetainedSemanticSourceMissing) {
			return certificationSourceRows, s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildMissing, "SOURCE_COLLECTION_MISSING", SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
		}
		if readErr != nil {
			return certificationSourceRows, errors.Join(fmt.Errorf("enumerate retained source collection %s: %w", resourceType, readErr), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_ENUMERATION_FAILED"))
		}
		if !page.SourceExists {
			return certificationSourceRows, s.writeSemanticCoverageResourceMarker(ctx, identity, resourceType, SemanticCoverageBuildMissing, "SOURCE_COLLECTION_MISSING", SemanticCoverageResourceExpectation{ResourceType: resourceType}, 0, 0)
		}
		for _, source := range page.Rows {
			certificationSourceRows++
			authPath := ""
			if source.AuthResourcePath != nil {
				authPath = *source.AuthResourcePath
			}
			if err := accumulator.AddSource("retained:"+resourceType+"/"+source.Key, authPath); err != nil {
				return certificationSourceRows, errors.Join(err, s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_ENUMERATION_LIMIT"))
			}
			if source.Key <= afterKey {
				return certificationSourceRows, errors.Join(fmt.Errorf("retained %s source enumeration cursor did not advance", resourceType), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_CURSOR_DID_NOT_ADVANCE"))
			}
			afterKey = source.Key
		}
		if !page.HasMore {
			break
		}
		if len(page.Rows) == 0 {
			return certificationSourceRows, errors.Join(fmt.Errorf("retained %s source enumeration reported more rows without advancing", resourceType), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_CURSOR_DID_NOT_ADVANCE"))
		}
	}
	revisionAfter, err := semanticCoverageCollectionRevision(ctx, s.client, resourceType)
	if err != nil {
		return certificationSourceRows, errors.Join(fmt.Errorf("reread retained source collection %s revision: %w", resourceType, err), s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_COLLECTION_REVISION_FAILED"))
	}
	if revisionAfter != revisionBefore {
		return certificationSourceRows, s.CompleteSemanticCoverageResource(ctx, identity, SemanticCoverageResourceExpectation{ResourceType: resourceType, SourceExists: true, Terminal: false}, batchSize)
	}
	expectation, err := accumulator.Snapshot(true)
	if err != nil {
		return certificationSourceRows, errors.Join(err, s.FailSemanticCoverageResource(context.WithoutCancel(ctx), identity, resourceType, "SOURCE_EXPECTATION_INVALID"))
	}
	return certificationSourceRows, s.CompleteSemanticCoverageResource(ctx, identity, expectation, batchSize)
}

func semanticCoverageCollectionRevision(ctx context.Context, client any, collection string) (string, error) {
	reader, ok := client.(interface {
		CollectionRevision(context.Context, string) (string, error)
	})
	if !ok {
		return "", errors.New("Arango client does not expose collection revision reads")
	}
	return reader.CollectionRevision(ctx, collection)
}

func validateSemanticCoverageBuildIdentity(identity SemanticCoverageBuildIdentity) error {
	if strings.TrimSpace(identity.Project) == "" || strings.TrimSpace(identity.DatasetGeneration) == "" ||
		catalog.NormalizeDatasetGeneration(identity.DatasetGeneration) != identity.DatasetGeneration ||
		strings.TrimSpace(identity.BuildID) == "" ||
		strings.TrimSpace(identity.SourceKind) == "" {
		return ErrInvalidSemanticCoverageRead
	}
	return nil
}

func semanticCoverageBuildBindVars(identity SemanticCoverageBuildIdentity) map[string]interface{} {
	return map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "schema_identity": identity.SchemaIdentity,
		"source_kind": identity.SourceKind,
	}
}

func (s *Store) writeSemanticCoverageBuildMarker(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	state SemanticCoverageBuildState,
	reason string,
	expectedSources, materializedSources, summaryCount int64,
	expectedResourceTypes ...[]string,
) error {
	document := semanticCoverageStoredDocument{
		Key: semanticCoverageBuildKey(identity), RecordKind: "build", BuildState: state, BuildReason: reason,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SourceKind: identity.SourceKind, SchemaIdentity: identity.SchemaIdentity, CoverageRuleVersion: catalog.SemanticCoverageRuleVersion,
		CoverageStorageFormatVersion: catalog.SemanticCoverageStorageFormatVersion,
		ExpectedSourceCount:          expectedSources, MaterializedSourceCount: materializedSources, SummaryCount: summaryCount,
	}
	if len(expectedResourceTypes) > 0 {
		document.ExpectedResourceTypes = append([]string(nil), expectedResourceTypes[0]...)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode semantic coverage build marker: %w", err)
	}
	if err := validateSemanticCoverageDocument(encoded); err != nil {
		return fmt.Errorf("validate semantic coverage build marker: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, SemanticCoverageCollection, []json.RawMessage{encoded}, true, ""); err != nil {
		return fmt.Errorf("write semantic coverage %s marker: %w", state, err)
	}
	return nil
}

func (s *Store) writeSemanticCoverageResourceMarker(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	resourceType string,
	state SemanticCoverageBuildState,
	reason string,
	expectation SemanticCoverageResourceExpectation,
	materializedSources int64,
	materializedPartitions int64,
	summaryCounts ...int64,
) error {
	document := semanticCoverageStoredDocument{
		Key: semanticCoverageResourceKey(identity, resourceType), RecordKind: "resource",
		ResourceState: state,
		Project:       identity.Project, DatasetGeneration: identity.DatasetGeneration,
		BuildID: identity.BuildID, SourceKind: identity.SourceKind, SchemaIdentity: identity.SchemaIdentity,
		ResourceType: resourceType, CoverageRuleVersion: catalog.SemanticCoverageRuleVersion,
		CoverageStorageFormatVersion: catalog.SemanticCoverageStorageFormatVersion,
		SourceSetDigest:              expectation.SourceSetDigest, SourceEnumerationComplete: expectation.Terminal,
	}
	if strings.TrimSpace(reason) != "" {
		document.BuildReason = boundedSemanticCoverageReason(reason)
	}
	if expectation.SourceExists {
		document.ExpectedSourceCount = expectation.SourceCount
		document.SourcePartitionCount = int64(len(expectation.Partitions))
	}
	document.MaterializedSourceCount = materializedSources
	document.MaterializedPartitionCount = materializedPartitions
	if len(summaryCounts) > 0 {
		document.SummaryCount = summaryCounts[0]
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode semantic coverage resource marker: %w", err)
	}
	if err := validateSemanticCoverageDocument(encoded); err != nil {
		return fmt.Errorf("validate semantic coverage resource marker: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, SemanticCoverageCollection, []json.RawMessage{encoded}, true, ""); err != nil {
		return fmt.Errorf("write semantic coverage resource %s marker: %w", state, err)
	}
	return nil
}

func semanticCoverageBuildKey(identity SemanticCoverageBuildIdentity) string {
	return semanticCoverageDocumentKey("semantic-coverage/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, "build")
}

func semanticCoverageResourceKey(identity SemanticCoverageBuildIdentity, resourceType string) string {
	return semanticCoverageDocumentKey("semantic-coverage/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, "resource", resourceType)
}

func semanticCoveragePartitionDocumentKey(identity SemanticCoverageBuildIdentity, resourceType, authResourcePath string) string {
	return semanticCoverageDocumentKey("semantic-coverage/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, "partition", authResourcePath, resourceType)
}

func boundedSemanticCoverageReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "COVERAGE_FAILED"
	}
	if len(reason) > 256 {
		return reason[:256]
	}
	return reason
}

func (s *Store) scanSemanticCoverageDocuments(ctx context.Context, identity SemanticCoverageBuildIdentity, recordKind string, visit func(semanticCoverageStoredDocument) error) error {
	afterKey := ""
	for {
		page, err := s.readSemanticCoveragePage(ctx, identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, recordKind, nil, true, afterKey, semanticCoverageMaximumPageSize)
		if err != nil {
			return err
		}
		for _, row := range page.Rows {
			var document semanticCoverageStoredDocument
			if err := decodeSemanticCoverageRow(row, &document); err != nil {
				return fmt.Errorf("decode semantic coverage read row: %w", err)
			}
			if document.Key == "" {
				return errors.New("semantic coverage read row is missing _key")
			}
			if err := visit(document); err != nil {
				return err
			}
			afterKey = document.Key
		}
		if !page.HasMore {
			return nil
		}
	}
}

func (s *Store) scanSemanticCoverageResourceDocuments(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	resourceType string,
	recordKind string,
	visit func(semanticCoverageStoredDocument) error,
) error {
	afterKey := ""
	pageSize := semanticCoverageMaximumPageSize
	if recordKind == semanticCoverageEnvelopeRecordKind {
		pageSize = semanticCoverageMaximumEnvelopeReadRows
	}
	for {
		page := SemanticCoveragePage{Rows: make([]map[string]any, 0, pageSize)}
		err := s.client.QueryRows(ctx, semanticCoverageResourcePageAQL, pageSize, map[string]interface{}{
			"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
			"build_id": identity.BuildID, "source_kind": identity.SourceKind,
			"schema_identity": identity.SchemaIdentity, "resource_type": resourceType,
			"record_kind": recordKind, "after_key": afterKey, "limit": pageSize,
		}, func(row map[string]any) error {
			page.Rows = append(page.Rows, row)
			return nil
		})
		if err != nil {
			return fmt.Errorf("read semantic coverage %s page for %s: %w", recordKind, resourceType, err)
		}
		for _, row := range page.Rows {
			var document semanticCoverageStoredDocument
			if err := decodeSemanticCoverageRow(row, &document); err != nil {
				return fmt.Errorf("decode semantic coverage %s row for %s: %w", recordKind, resourceType, err)
			}
			if document.Key == "" || document.ResourceType != resourceType {
				return fmt.Errorf("semantic coverage %s row for %s has an invalid identity", recordKind, resourceType)
			}
			if err := visit(document); err != nil {
				return err
			}
			if document.Key <= afterKey {
				return fmt.Errorf("semantic coverage %s cursor did not advance for %s", recordKind, resourceType)
			}
			afterKey = document.Key
		}
		if len(page.Rows) < pageSize {
			return nil
		}
	}
}

type semanticCoverageSummaryKey struct {
	authResourcePath  string
	resourceType      string
	canonicalPath     string
	boundarySignature string
	summaryKind       string
}

type semanticCoverageSummaryAggregate struct {
	document       semanticCoverageStoredDocument
	pathSources    int64
	allGroupable   bool
	anyKeepRaw     bool
	referencesFull bool
	reason         catalog.SemanticCoverageReason
	references     map[string]catalog.SemanticCoverageReference
}

type semanticCoverageMaterializer struct {
	identity          SemanticCoverageBuildIdentity
	partitionSources  map[string]int64
	partitionDigests  map[string]*semanticCoverageSourceSetDigest
	partitionIdentity map[string]semanticCoveragePartitionIdentity
	partitionComplete map[string]bool
	partitionReason   map[string]catalog.SemanticCoverageReason
	aggregates        map[semanticCoverageSummaryKey]*semanticCoverageSummaryAggregate
}

type semanticCoveragePartitionIdentity struct {
	authResourcePath string
	resourceType     string
}

func newSemanticCoverageMaterializer(identity SemanticCoverageBuildIdentity) *semanticCoverageMaterializer {
	return &semanticCoverageMaterializer{
		identity: identity, partitionSources: make(map[string]int64), partitionDigests: make(map[string]*semanticCoverageSourceSetDigest), partitionIdentity: make(map[string]semanticCoveragePartitionIdentity), partitionComplete: make(map[string]bool),
		partitionReason: make(map[string]catalog.SemanticCoverageReason), aggregates: make(map[semanticCoverageSummaryKey]*semanticCoverageSummaryAggregate),
	}
}

// addEnvelope folds all rows for a path and all facets for an exact leaf path
// within one source before updating build summaries. This keeps source counts
// from multiplying when one source contributes several occurrence kinds.
func (m *semanticCoverageMaterializer) addEnvelope(source semanticCoverageStoredDocument) error {
	if err := m.addSource(source); err != nil {
		return err
	}
	owners := make(map[semanticCoverageSummaryKey]semanticCoverageStoredDocument)
	ownerGroupable := make(map[semanticCoverageSummaryKey]bool)
	facets := make(map[semanticCoverageSummaryKey]semanticCoverageStoredDocument)
	facetGroupable := make(map[semanticCoverageSummaryKey]bool)

	for _, envelopePath := range source.Paths {
		if envelopePath.Path == nil || strings.TrimSpace(envelopePath.CanonicalPath) == "" {
			return ErrSemanticCoverageSourceSetIncomplete
		}
		path := cloneSemanticCoverageStoredPath(envelopePath.Path)
		path.OwnerPathWitnesses = nil
		ownerKey := semanticCoverageSummaryKey{
			authResourcePath: source.AuthResourcePath, resourceType: source.ResourceType,
			canonicalPath:     envelopePath.CanonicalPath,
			boundarySignature: path.ResourceBoundarySignature,
			summaryKind:       "owner",
		}
		owner, found := owners[ownerKey]
		if !found {
			owner = source
			owner.RecordKind = "path"
			owner.CanonicalPath = envelopePath.CanonicalPath
			owner.ResourceBoundarySignature = path.ResourceBoundarySignature
			owner.Path = path
			owner.Paths = nil
			owner.Groupable = envelopePath.Groupable
			ownerGroupable[ownerKey] = envelopePath.Groupable
		} else {
			mergeSemanticCoverageStoredPath(owner.Path, path)
			mergeSemanticCoverageStoredReferences(&owner.Path.References, path.References, &owner.Path.ReferencesTruncated)
			ownerGroupable[ownerKey] = ownerGroupable[ownerKey] && envelopePath.Groupable
			if path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) {
				owner.Path.Disposition = path.Disposition
				owner.Path.Reason = path.Reason
			}
		}
		owners[ownerKey] = owner

		for _, facet := range path.Facets {
			if strings.TrimSpace(facet.Path) == "" || strings.TrimSpace(facet.Kind) == "" || facet.OccurrenceCount < 0 {
				return ErrSemanticCoverageSourceSetIncomplete
			}
			facetPath := &semanticCoverageStoredPath{
				ResourceBoundarySignature: facet.ResourceBoundarySignature,
				Kind:                      facet.Kind, Disposition: facet.Disposition, Reason: facet.Reason,
				OccurrenceCount:     facet.OccurrenceCount,
				PresenceCounts:      facet.PresenceCounts,
				References:          facet.References,
				ReferencesTruncated: facet.ReferencesTruncated,
			}
			facetKey := semanticCoverageSummaryKey{
				authResourcePath: source.AuthResourcePath, resourceType: source.ResourceType,
				canonicalPath: facet.Path, boundarySignature: facet.ResourceBoundarySignature,
				summaryKind: "facet",
			}
			facetDocument, found := facets[facetKey]
			currentGroupable := envelopePath.Groupable && facet.ResourceBoundarySignature == "" &&
				facet.Disposition == string(catalog.SemanticCoverageSemanticDisposition) &&
				!facet.ReferencesTruncated && len(facet.References) > 0
			if !found {
				facetDocument = source
				facetDocument.RecordKind = "facet"
				facetDocument.CanonicalPath = facet.Path
				facetDocument.ResourceBoundarySignature = facet.ResourceBoundarySignature
				facetDocument.Path = facetPath
				facetDocument.Paths = nil
				facetDocument.Groupable = currentGroupable
				facetGroupable[facetKey] = currentGroupable
			} else {
				mergeSemanticCoverageStoredPath(facetDocument.Path, facetPath)
				mergeSemanticCoverageStoredReferences(&facetDocument.Path.References, facetPath.References, &facetDocument.Path.ReferencesTruncated)
				facetGroupable[facetKey] = facetGroupable[facetKey] && currentGroupable
				if facetPath.Disposition != string(catalog.SemanticCoverageSemanticDisposition) {
					facetDocument.Path.Disposition = facetPath.Disposition
					facetDocument.Path.Reason = facetPath.Reason
				}
			}
			facets[facetKey] = facetDocument
		}
	}

	for _, key := range sortedSemanticCoverageSourceAggregateKeys(owners) {
		document := owners[key]
		document.Groupable = ownerGroupable[key]
		if err := m.addPath(document); err != nil {
			return err
		}
	}
	for _, key := range sortedSemanticCoverageSourceAggregateKeys(facets) {
		document := facets[key]
		document.Groupable = facetGroupable[key]
		if err := m.addFacet(document); err != nil {
			return err
		}
	}
	return nil
}

func sortedSemanticCoverageSourceAggregateKeys(documents map[semanticCoverageSummaryKey]semanticCoverageStoredDocument) []semanticCoverageSummaryKey {
	keys := make([]semanticCoverageSummaryKey, 0, len(documents))
	for key := range documents {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].canonicalPath != keys[j].canonicalPath {
			return keys[i].canonicalPath < keys[j].canonicalPath
		}
		if keys[i].boundarySignature != keys[j].boundarySignature {
			return keys[i].boundarySignature < keys[j].boundarySignature
		}
		return keys[i].summaryKind < keys[j].summaryKind
	})
	return keys
}

func mergeSemanticCoverageStoredReferences(target *[]semanticCoverageStoredReference, source []semanticCoverageStoredReference, truncated *bool) {
	seen := make(map[string]struct{}, len(*target)+len(source))
	for _, reference := range *target {
		converted, ok := semanticCoverageCatalogReference(reference)
		if ok {
			seen[semanticCoverageReferenceKey(converted)] = struct{}{}
		}
	}
	for _, reference := range source {
		converted, ok := semanticCoverageCatalogReference(reference)
		if !ok {
			continue
		}
		key := semanticCoverageReferenceKey(converted)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(*target) >= semanticCoverageMaximumReferencesPerSummary {
			*truncated = true
			continue
		}
		seen[key] = struct{}{}
		*target = append(*target, reference)
	}
}

func (m *semanticCoverageMaterializer) addSource(source semanticCoverageStoredDocument) error {
	partition := semanticCoveragePartitionKey(source.AuthResourcePath, source.ResourceType)
	if _, exists := m.partitionSources[partition]; !exists && len(m.partitionSources) >= semanticCoverageMaximumPartitions {
		return ErrSemanticCoverageMaterializationLimit
	}
	m.partitionSources[partition]++
	m.partitionIdentity[partition] = semanticCoveragePartitionIdentity{authResourcePath: source.AuthResourcePath, resourceType: source.ResourceType}
	digest := m.partitionDigests[partition]
	if digest == nil {
		digest = &semanticCoverageSourceSetDigest{}
		m.partitionDigests[partition] = digest
	}
	identityDigest := sha256.Sum256([]byte(semanticCoverageDocumentKey("resource-source-identity/v1", source.ResourceType, source.SourceID, source.AuthResourcePath)))
	addSemanticCoverageDigest(&digest.sum, identityDigest)
	digest.count++
	if _, exists := m.partitionComplete[partition]; !exists {
		m.partitionComplete[partition] = true
	}
	if !source.Complete {
		m.partitionComplete[partition] = false
		m.partitionReason[partition] = catalog.SemanticCoverageReasonIncompleteSource
	} else if source.CountsTruncated || source.FacetsTruncated || source.ReferencesTruncated || source.ReconciliationTruncated || len(source.IncompleteGaps) != 0 {
		m.partitionComplete[partition] = false
		m.partitionReason[partition] = catalog.SemanticCoverageReasonTruncatedEvidence
	}
	return nil
}

func (m *semanticCoverageMaterializer) addPath(sourcePath semanticCoverageStoredDocument) error {
	if sourcePath.Path == nil {
		return nil
	}
	key := semanticCoverageSummaryKey{authResourcePath: sourcePath.AuthResourcePath, resourceType: sourcePath.ResourceType, canonicalPath: sourcePath.CanonicalPath, boundarySignature: sourcePath.ResourceBoundarySignature, summaryKind: "owner"}
	aggregate := m.aggregates[key]
	if aggregate == nil {
		if len(m.aggregates) >= semanticCoverageMaximumSummaries {
			return ErrSemanticCoverageMaterializationLimit
		}
		aggregate = &semanticCoverageSummaryAggregate{
			document: semanticCoverageStoredDocument{
				RecordKind: "summary", Project: m.identity.Project, DatasetGeneration: m.identity.DatasetGeneration,
				BuildID: m.identity.BuildID, SourceKind: m.identity.SourceKind, SchemaIdentity: m.identity.SchemaIdentity,
				AuthResourcePath: key.authResourcePath, ResourceType: key.resourceType, CanonicalPath: key.canonicalPath,
				ResourceBoundarySignature: key.boundarySignature,
				Complete:                  true, Groupable: true, SummaryKind: key.summaryKind,
			},
			allGroupable: true,
			references:   make(map[string]catalog.SemanticCoverageReference),
		}
		m.aggregates[key] = aggregate
	}
	path := sourcePath.Path
	aggregate.pathSources++
	aggregate.document.CountsTruncated = aggregate.document.CountsTruncated || sourcePath.CountsTruncated || path.CountsTruncated
	aggregate.document.FacetsTruncated = aggregate.document.FacetsTruncated || sourcePath.FacetsTruncated || path.FacetsTruncated
	aggregate.document.ReferencesTruncated = aggregate.document.ReferencesTruncated || sourcePath.ReferencesTruncated || path.ReferencesTruncated
	if aggregate.document.Path == nil {
		aggregate.document.Path = cloneSemanticCoverageStoredPath(path)
		aggregate.document.Path.OwnerPathWitnesses = nil
	} else {
		mergeSemanticCoverageStoredPath(aggregate.document.Path, path)
	}
	if !sourcePath.Groupable || sourcePath.ResourceBoundarySignature != "" || path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) || path.CountsTruncated || path.FacetsTruncated || path.ReferencesTruncated {
		aggregate.allGroupable = false
	}
	if sourcePath.ResourceBoundarySignature != "" {
		aggregate.anyKeepRaw = true
		aggregate.reason = catalog.SemanticCoverageReasonUnsupportedSelector
	}
	if path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) {
		aggregate.anyKeepRaw = true
		if aggregate.reason == "" && path.Reason != "" {
			aggregate.reason = catalog.SemanticCoverageReason(path.Reason)
		}
	}
	for _, reference := range path.References {
		converted, ok := semanticCoverageCatalogReference(reference)
		if !ok {
			continue
		}
		key := semanticCoverageReferenceKey(converted)
		if _, exists := aggregate.references[key]; exists {
			continue
		}
		if len(aggregate.references) >= semanticCoverageMaximumReferencesPerSummary {
			aggregate.referencesFull = true
			aggregate.document.ReferencesTruncated = true
			aggregate.allGroupable = false
			continue
		}
		aggregate.references[key] = converted
	}
	return nil
}

func (m *semanticCoverageMaterializer) addFacet(sourceFacet semanticCoverageStoredDocument) error {
	if sourceFacet.Path == nil {
		return nil
	}
	key := semanticCoverageSummaryKey{authResourcePath: sourceFacet.AuthResourcePath, resourceType: sourceFacet.ResourceType, canonicalPath: sourceFacet.CanonicalPath, boundarySignature: sourceFacet.ResourceBoundarySignature, summaryKind: "facet"}
	aggregate := m.aggregates[key]
	if aggregate == nil {
		if len(m.aggregates) >= semanticCoverageMaximumSummaries {
			return ErrSemanticCoverageMaterializationLimit
		}
		aggregate = &semanticCoverageSummaryAggregate{
			document: semanticCoverageStoredDocument{
				RecordKind: "summary", Project: m.identity.Project, DatasetGeneration: m.identity.DatasetGeneration,
				BuildID: m.identity.BuildID, SourceKind: m.identity.SourceKind, SchemaIdentity: m.identity.SchemaIdentity,
				AuthResourcePath: key.authResourcePath, ResourceType: key.resourceType, CanonicalPath: key.canonicalPath,
				ResourceBoundarySignature: key.boundarySignature,
				Complete:                  true, Groupable: true, SummaryKind: key.summaryKind,
			},
			allGroupable: true,
			references:   make(map[string]catalog.SemanticCoverageReference),
		}
		m.aggregates[key] = aggregate
	}
	path := sourceFacet.Path
	aggregate.pathSources++
	aggregate.document.CountsTruncated = aggregate.document.CountsTruncated || sourceFacet.CountsTruncated || path.CountsTruncated
	aggregate.document.FacetsTruncated = aggregate.document.FacetsTruncated || sourceFacet.FacetsTruncated || path.FacetsTruncated
	aggregate.document.ReferencesTruncated = aggregate.document.ReferencesTruncated || sourceFacet.ReferencesTruncated || path.ReferencesTruncated
	if aggregate.document.Path == nil {
		aggregate.document.Path = cloneSemanticCoverageStoredPath(path)
		aggregate.document.Path.OwnerPathWitnesses = nil
	} else {
		mergeSemanticCoverageStoredPath(aggregate.document.Path, path)
	}
	if !sourceFacet.Groupable || sourceFacet.ResourceBoundarySignature != "" || path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) || sourceFacet.CountsTruncated || sourceFacet.FacetsTruncated || sourceFacet.ReferencesTruncated || path.CountsTruncated || path.FacetsTruncated || path.ReferencesTruncated {
		aggregate.allGroupable = false
	}
	if path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) {
		aggregate.anyKeepRaw = true
		if aggregate.reason == "" && path.Reason != "" {
			aggregate.reason = catalog.SemanticCoverageReason(path.Reason)
		}
	}
	if sourceFacet.ResourceBoundarySignature != "" {
		aggregate.anyKeepRaw = true
		aggregate.reason = catalog.SemanticCoverageReasonUnsupportedSelector
	}
	for _, reference := range path.References {
		converted, ok := semanticCoverageCatalogReference(reference)
		if !ok {
			continue
		}
		referenceKey := semanticCoverageReferenceKey(converted)
		if _, exists := aggregate.references[referenceKey]; exists {
			continue
		}
		if len(aggregate.references) >= semanticCoverageMaximumReferencesPerSummary {
			aggregate.referencesFull = true
			aggregate.document.ReferencesTruncated = true
			aggregate.allGroupable = false
			continue
		}
		aggregate.references[referenceKey] = converted
	}
	return nil
}

func (m *semanticCoverageMaterializer) sortedKeys() []semanticCoverageSummaryKey {
	keys := make([]semanticCoverageSummaryKey, 0, len(m.aggregates))
	for key := range m.aggregates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].authResourcePath != keys[j].authResourcePath {
			return keys[i].authResourcePath < keys[j].authResourcePath
		}
		if keys[i].resourceType != keys[j].resourceType {
			return keys[i].resourceType < keys[j].resourceType
		}
		if keys[i].canonicalPath != keys[j].canonicalPath {
			return keys[i].canonicalPath < keys[j].canonicalPath
		}
		if keys[i].boundarySignature != keys[j].boundarySignature {
			return keys[i].boundarySignature < keys[j].boundarySignature
		}
		return keys[i].summaryKind < keys[j].summaryKind
	})
	return keys
}

func (m *semanticCoverageMaterializer) summaryDocument(key semanticCoverageSummaryKey) semanticCoverageStoredDocument {
	aggregate := m.aggregates[key]
	partition := semanticCoveragePartitionKey(key.authResourcePath, key.resourceType)
	document := aggregate.document
	document.SourceRecordCount = aggregate.pathSources
	document.Groupable = aggregate.allGroupable && m.partitionComplete[partition] && len(aggregate.references) > 0
	switch {
	case document.Groupable:
		document.Status = SemanticCoverageCandidateGroupable
	case !m.partitionComplete[partition]:
		document.Status = SemanticCoverageCandidateUnavailable
		document.Reason = string(m.partitionReason[partition])
		if document.Reason == "" {
			document.Reason = string(catalog.SemanticCoverageReasonIncompleteSource)
		}
	case document.CountsTruncated || document.FacetsTruncated || document.ReferencesTruncated ||
		(document.Path != nil && (document.Path.CountsTruncated || document.Path.FacetsTruncated || document.Path.ReferencesTruncated)):
		document.Status = SemanticCoverageCandidateKeepRaw
		document.Reason = string(catalog.SemanticCoverageReasonTruncatedEvidence)
	case aggregate.anyKeepRaw || !aggregate.allGroupable:
		document.Status = SemanticCoverageCandidateKeepRaw
		document.Reason = string(aggregate.reason)
		if aggregate.referencesFull {
			document.Reason = string(catalog.SemanticCoverageReasonTruncatedEvidence)
		}
		if document.Reason == "" {
			document.Reason = string(catalog.SemanticCoverageReasonUnrepresentedMetadata)
		}
	default:
		document.Status = SemanticCoverageCandidateKeepRaw
		document.Reason = string(catalog.SemanticCoverageReasonUnrepresentedMetadata)
	}
	refs := make([]catalog.SemanticCoverageReference, 0, len(aggregate.references))
	for _, reference := range aggregate.references {
		refs = append(refs, reference)
	}
	sort.Slice(refs, func(i, j int) bool {
		return semanticCoverageReferenceKey(refs[i]) < semanticCoverageReferenceKey(refs[j])
	})
	if document.Path != nil {
		document.Path.References = semanticCoverageStoredReferences(refs)
	}
	document.Key = semanticCoverageDocumentKey("semantic-coverage/v1", m.identity.Project, m.identity.DatasetGeneration, m.identity.BuildID, m.identity.SourceKind, m.identity.SchemaIdentity, "summary", key.summaryKind, key.authResourcePath, key.resourceType, key.canonicalPath, key.boundarySignature)
	if document.Status != SemanticCoverageCandidateGroupable && document.Path != nil {
		document.Path.References = nil
	}
	return document
}

func (s *Store) writeSemanticCoverageSummaries(ctx context.Context, materializer *semanticCoverageMaterializer, batchSize int) (SemanticCoverageWriteStats, error) {
	batchSize = semanticCoverageBatchSize(batchSize)
	keys := materializer.sortedKeys()
	stats := SemanticCoverageWriteStats{}
	batch := make([]json.RawMessage, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		written, err := s.writeSemanticCoverageDocuments(ctx, batch, batchSize)
		stats.Rows += written.Rows
		stats.SerializedBytes += written.SerializedBytes
		batch = batch[:0]
		return err
	}
	for _, key := range keys {
		encoded, err := json.Marshal(materializer.summaryDocument(key))
		if err != nil {
			return stats, fmt.Errorf("encode semantic coverage summary: %w", err)
		}
		if err := validateSemanticCoverageDocument(encoded); err != nil {
			return stats, fmt.Errorf("validate semantic coverage summary: %w", err)
		}
		batch = append(batch, encoded)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *Store) writeSemanticCoveragePartitions(ctx context.Context, materializer *semanticCoverageMaterializer, batchSize int) (SemanticCoverageWriteStats, error) {
	batchSize = semanticCoverageBatchSize(batchSize)
	keys := make([]string, 0, len(materializer.partitionSources))
	for key := range materializer.partitionSources {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := materializer.partitionIdentity[keys[i]], materializer.partitionIdentity[keys[j]]
		if left.authResourcePath != right.authResourcePath {
			return left.authResourcePath < right.authResourcePath
		}
		return left.resourceType < right.resourceType
	})
	stats := SemanticCoverageWriteStats{}
	batch := make([]json.RawMessage, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		written, err := s.writeSemanticCoverageDocuments(ctx, batch, batchSize)
		stats.Rows += written.Rows
		stats.SerializedBytes += written.SerializedBytes
		batch = batch[:0]
		return err
	}
	for _, partitionKey := range keys {
		partition := materializer.partitionIdentity[partitionKey]
		complete := materializer.partitionComplete[partitionKey]
		document := semanticCoverageStoredDocument{
			RecordKind: "partition", Project: materializer.identity.Project,
			DatasetGeneration: materializer.identity.DatasetGeneration, BuildID: materializer.identity.BuildID,
			SourceKind: materializer.identity.SourceKind, SchemaIdentity: materializer.identity.SchemaIdentity,
			CoverageStorageFormatVersion: catalog.SemanticCoverageStorageFormatVersion,
			AuthResourcePath:             partition.authResourcePath, ResourceType: partition.resourceType,
			SourceRecordCount: materializer.partitionSources[partitionKey], SourceSetDigest: semanticCoveragePartitionDigest(partition.resourceType, partition.authResourcePath, materializer.partitionDigests[partitionKey]), Complete: complete, Groupable: complete,
		}
		if complete {
			document.Status = SemanticCoverageCandidateGroupable
		} else {
			document.Status = SemanticCoverageCandidateUnavailable
			document.Reason = string(materializer.partitionReason[partitionKey])
			if document.Reason == "" {
				document.Reason = string(catalog.SemanticCoverageReasonIncompleteSource)
			}
		}
		document.Key = semanticCoveragePartitionDocumentKey(materializer.identity, partition.resourceType, partition.authResourcePath)
		encoded, err := json.Marshal(document)
		if err != nil {
			return stats, fmt.Errorf("encode semantic coverage partition: %w", err)
		}
		if err := validateSemanticCoverageDocument(encoded); err != nil {
			return stats, fmt.Errorf("validate semantic coverage partition: %w", err)
		}
		batch = append(batch, encoded)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func semanticCoveragePartitionKey(authPath, resourceType string) string {
	return semanticCoverageDocumentKey(authPath, resourceType)
}

func cloneSemanticCoverageStoredPath(path *semanticCoverageStoredPath) *semanticCoverageStoredPath {
	data, _ := json.Marshal(path)
	var clone semanticCoverageStoredPath
	_ = json.Unmarshal(data, &clone)
	return &clone
}

func mergeSemanticCoverageStoredPath(target, source *semanticCoverageStoredPath) {
	target.OccurrenceCount += source.OccurrenceCount
	target.OwnerCount += source.OwnerCount
	target.CodingCount += source.CodingCount
	mergeSemanticCoverageStringCounts(&target.PresenceCounts, source.PresenceCounts)
	mergeSemanticCoverageStringCounts(&target.CollectionCounts, source.CollectionCounts)
	for path, counts := range source.FacetPresenceCounts {
		if target.FacetPresenceCounts == nil {
			target.FacetPresenceCounts = make(map[string]map[string]int64)
		}
		if target.FacetPresenceCounts[path] == nil {
			target.FacetPresenceCounts[path] = make(map[string]int64)
		}
		facetCounts := target.FacetPresenceCounts[path]
		mergeSemanticCoverageStringCounts(&facetCounts, counts)
		target.FacetPresenceCounts[path] = facetCounts
	}
	facets := make(map[string]int, len(target.Facets)+len(source.Facets))
	for index, facet := range target.Facets {
		facets[semanticCoverageStoredFacetKey(facet)] = index
	}
	for _, facet := range source.Facets {
		key := semanticCoverageStoredFacetKey(facet)
		if index, exists := facets[key]; exists {
			target.Facets[index].OccurrenceCount += facet.OccurrenceCount
			continue
		}
		if len(target.Facets) >= 5000 {
			target.FacetsTruncated = true
			continue
		}
		facets[key] = len(target.Facets)
		target.Facets = append(target.Facets, facet)
	}
	sort.Slice(target.Facets, func(i, j int) bool {
		if target.Facets[i].Path != target.Facets[j].Path {
			return target.Facets[i].Path < target.Facets[j].Path
		}
		if target.Facets[i].ResourceBoundarySignature != target.Facets[j].ResourceBoundarySignature {
			return target.Facets[i].ResourceBoundarySignature < target.Facets[j].ResourceBoundarySignature
		}
		if target.Facets[i].Kind != target.Facets[j].Kind {
			return target.Facets[i].Kind < target.Facets[j].Kind
		}
		if target.Facets[i].Disposition != target.Facets[j].Disposition {
			return target.Facets[i].Disposition < target.Facets[j].Disposition
		}
		return target.Facets[i].Reason < target.Facets[j].Reason
	})
	target.CountsTruncated = target.CountsTruncated || source.CountsTruncated
	target.FacetsTruncated = target.FacetsTruncated || source.FacetsTruncated
	target.ReferencesTruncated = target.ReferencesTruncated || source.ReferencesTruncated
	target.WitnessesTruncated = target.WitnessesTruncated || source.WitnessesTruncated
}

func semanticCoverageStoredFacetKey(facet semanticCoverageStoredFacet) string {
	return semanticCoverageDocumentKey("facet", facet.Path, facet.ResourceBoundarySignature, facet.Kind, facet.Disposition, facet.Reason)
}

func mergeSemanticCoverageStringCounts(target *map[string]int64, source map[string]int64) {
	if len(source) == 0 {
		return
	}
	if *target == nil {
		*target = make(map[string]int64, len(source))
	}
	for key, count := range source {
		(*target)[key] += count
	}
}

func semanticCoverageCatalogReference(reference semanticCoverageStoredReference) (catalog.SemanticCoverageReference, bool) {
	result := catalog.SemanticCoverageReference{
		Kind: catalog.SemanticCoverageReferenceKind(reference.Kind), ConceptID: reference.ConceptID, BindingID: reference.BindingID,
		CandidatePath: reference.CandidatePath, ResourceType: reference.ResourceType,
	}
	switch result.Kind {
	case catalog.SemanticCoverageExactReference:
		return result, result.ConceptID != "" && result.BindingID != ""
	case catalog.SemanticCoverageFamilyReference:
		if reference.Family == nil {
			return catalog.SemanticCoverageReference{}, false
		}
		result.Family = catalog.SemanticInventoryFamilyReference{FamilyID: reference.Family.FamilyID, BindingID: reference.Family.BindingID, SourceKind: reference.Family.SourceKind, ResourceType: reference.Family.ResourceType, SourcePath: reference.Family.SourcePath, System: reference.Family.System, Version: reference.Family.Version}
		return result, result.Family.FamilyID != "" && result.Family.BindingID != "" && result.Family.System != ""
	default:
		return catalog.SemanticCoverageReference{}, false
	}
}

func semanticCoverageCatalogReferenceFromCatalog(reference catalog.SemanticCoverageReference) (catalog.SemanticCoverageReference, bool) {
	switch reference.Kind {
	case catalog.SemanticCoverageExactReference:
		return reference, reference.ConceptID != "" && reference.BindingID != ""
	case catalog.SemanticCoverageFamilyReference:
		return reference, reference.Family.FamilyID != "" && reference.Family.BindingID != "" && reference.Family.System != ""
	default:
		return catalog.SemanticCoverageReference{}, false
	}
}

func semanticCoverageReferenceKey(reference catalog.SemanticCoverageReference) string {
	if reference.Kind == catalog.SemanticCoverageFamilyReference {
		return strings.Join([]string{string(reference.Kind), reference.Family.FamilyID, reference.Family.BindingID, reference.Family.SourceKind, reference.Family.ResourceType, reference.Family.SourcePath, reference.Family.System, reference.Family.Version}, "\x00")
	}
	return strings.Join([]string{string(reference.Kind), reference.BindingID, reference.ConceptID, reference.ResourceType, reference.CandidatePath}, "\x00")
}

func validateSemanticCoverageDocument(document json.RawMessage) error {
	if !json.Valid(document) {
		return errors.New("document is not valid JSON")
	}
	if len(document) > semanticCoverageMaximumRowBytes {
		return ErrSemanticCoverageRowTooLarge
	}
	var identity struct {
		Key string `json:"_key"`
	}
	if err := json.Unmarshal(document, &identity); err != nil {
		return fmt.Errorf("decode document identity: %w", err)
	}
	if strings.TrimSpace(identity.Key) == "" {
		return errors.New("document is missing deterministic _key")
	}
	return nil
}

// semanticCoverageDocumentKey hashes length-delimited identity parts. This
// keeps source/path keys deterministic without collisions from delimiters in
// FHIR paths or source locators.
func semanticCoverageDocumentKey(parts ...string) string {
	hash := sha256.New()
	var length [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return "scv1_" + hex.EncodeToString(hash.Sum(nil))
}

// readSemanticCoveragePage reads one exact build and authorization scope in
// stable key order. The caller controls whether its authorized path set is
// unrestricted; this method never falls back to another build.
func (s *Store) readSemanticCoveragePage(
	ctx context.Context,
	project string,
	datasetGeneration string,
	buildID string,
	sourceKind string,
	schemaIdentity string,
	recordKind string,
	authResourcePaths []string,
	authResourcePathsUnrestricted bool,
	afterKey string,
	limit int,
) (SemanticCoveragePage, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(datasetGeneration) == "" ||
		strings.TrimSpace(buildID) == "" || strings.TrimSpace(sourceKind) == "" || strings.TrimSpace(schemaIdentity) == "" ||
		(recordKind != semanticCoverageEnvelopeRecordKind && recordKind != "summary" && recordKind != "resource" && recordKind != "partition" && recordKind != "build") ||
		limit < 1 || limit > semanticCoverageMaximumPageSize ||
		(!authResourcePathsUnrestricted && len(authResourcePaths) == 0) {
		return SemanticCoveragePage{}, ErrInvalidSemanticCoverageRead
	}
	paths := append([]string(nil), authResourcePaths...)
	if !authResourcePathsUnrestricted {
		for _, path := range paths {
			if strings.TrimSpace(path) == "" {
				return SemanticCoveragePage{}, ErrInvalidSemanticCoverageRead
			}
		}
	}

	page := SemanticCoveragePage{Rows: make([]map[string]any, 0, limit)}
	count := 0
	err := s.client.QueryRows(ctx, semanticCoveragePageAQL, limit+1, map[string]interface{}{
		"project":                          project,
		"dataset_generation":               datasetGeneration,
		"build_id":                         buildID,
		"source_kind":                      sourceKind,
		"schema_identity":                  schemaIdentity,
		"record_kind":                      recordKind,
		"auth_resource_paths":              paths,
		"auth_resource_paths_unrestricted": authResourcePathsUnrestricted,
		"after_key":                        afterKey,
		"limit":                            limit + 1,
	}, func(row map[string]any) error {
		count++
		if count > limit {
			page.HasMore = true
			return nil
		}
		page.Rows = append(page.Rows, row)
		return nil
	})
	if err != nil {
		return SemanticCoveragePage{}, fmt.Errorf("read semantic coverage page: %w", err)
	}
	return page, nil
}

const semanticCoveragePageAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind == @record_kind
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @after_key == "" OR d._key > @after_key
  SORT d._key
  LIMIT @limit
  RETURN d`

const semanticCoverageResourcePageAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.resource_type == @resource_type
  FILTER d.record_kind == @record_kind
  FILTER @after_key == "" OR d._key > @after_key
  SORT d._key
  LIMIT @limit
  RETURN d`

const deleteSemanticCoverageResourceSummariesAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.resource_type == @resource_type
  FILTER d.record_kind IN ["summary", "partition"]
  REMOVE d IN fhir_semantic_coverage`

const semanticCoverageLookupSnapshotAQL = `
LET build_docs = (
  FOR d IN fhir_semantic_coverage
    FILTER d.project == @project
    FILTER d.dataset_generation == @dataset_generation
    FILTER d.build_id == @build_id
    FILTER d.source_kind == @source_kind
    FILTER d.schema_identity == @schema_identity
    FILTER d.record_kind == "build"
    LIMIT 1
    RETURN d
)
LET resource_docs = (
  FOR resource_type IN @resource_types
    FOR d IN fhir_semantic_coverage
      FILTER d.project == @project
      FILTER d.dataset_generation == @dataset_generation
      FILTER d.build_id == @build_id
      FILTER d.source_kind == @source_kind
      FILTER d.schema_identity == @schema_identity
      FILTER d.record_kind == "resource"
      FILTER d.resource_type == resource_type
  SORT d.resource_type
  LIMIT @resource_limit
  RETURN d
)
LET partition_docs = (
  FOR resource_type IN @resource_types
    FOR d IN fhir_semantic_coverage
      FILTER d.project == @project
      FILTER d.dataset_generation == @dataset_generation
      FILTER d.build_id == @build_id
      FILTER d.source_kind == @source_kind
      FILTER d.schema_identity == @schema_identity
      FILTER d.record_kind == "partition"
      FILTER d.resource_type == resource_type
      FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  SORT d.resource_type, d.auth_resource_path
  LIMIT @partition_limit
  RETURN d
)
LET auth_scope_checks = @auth_resource_paths_unrestricted == true
  ? (
      FOR partition IN partition_docs
        RETURN {resource_type: partition.resource_type, auth_resource_path: partition.auth_resource_path}
    )
  : (
      FOR resource_type IN @resource_types
        FOR auth_resource_path IN @auth_resource_paths
          RETURN {resource_type: resource_type, auth_resource_path: auth_resource_path}
    )
LET source_proof = (
  FOR check IN auth_scope_checks
    LET source_exists = LENGTH(
      FOR source IN fhir_semantic_coverage
        FILTER source.project == @project
        FILTER source.dataset_generation == @dataset_generation
        FILTER source.build_id == @build_id
        FILTER source.source_kind == @source_kind
        FILTER source.schema_identity == @schema_identity
        FILTER source.record_kind == "source_envelope_v1"
        FILTER source.resource_type == check.resource_type
        FILTER source.auth_resource_path == check.auth_resource_path
        LIMIT 1
        RETURN 1
    ) > 0
  SORT check.resource_type, check.auth_resource_path
  LIMIT @source_proof_limit
  RETURN {
    resource_type: check.resource_type,
    auth_resource_path: check.auth_resource_path,
    source_exists: source_exists
  }
)
LET facet_docs = (
  FOR candidate IN @candidates
    FOR d IN fhir_semantic_coverage
      FILTER d.project == @project
      FILTER d.dataset_generation == @dataset_generation
      FILTER d.build_id == @build_id
      FILTER d.source_kind == @source_kind
      FILTER d.schema_identity == @schema_identity
      FILTER d.record_kind == "summary"
      FILTER d.summary_kind == "facet"
      FILTER d.resource_type == candidate.resource_type
      FILTER d.canonical_path == candidate.canonical_path
      FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  SORT d.resource_type, d.canonical_path, d.resource_boundary_signature, d.auth_resource_path
  LIMIT @facet_limit
  RETURN d
)
RETURN {
  build: FIRST(build_docs),
  resources: resource_docs,
  partitions: partition_docs,
  source_proof: source_proof,
  facets: facet_docs
}`

type semanticCoverageLookupSnapshot struct {
	Build       *semanticCoverageStoredDocument  `json:"build"`
	Resources   []semanticCoverageStoredDocument `json:"resources"`
	Partitions  []semanticCoverageStoredDocument `json:"partitions"`
	SourceProof []semanticCoverageSourcePresence `json:"source_proof"`
	Facets      []semanticCoverageStoredDocument `json:"facets"`
}

type semanticCoverageSourcePresence struct {
	ResourceType     string `json:"resource_type"`
	AuthResourcePath string `json:"auth_resource_path"`
	SourceExists     bool   `json:"source_exists"`
}

// LookupSemanticCoverage reads build, resource, partition, source-presence and
// exact facet proof in one bounded AQL snapshot. The caller's auth scope is
// already resolved; no cross-build or cross-scope fallback is performed.
func (s *Store) LookupSemanticCoverage(ctx context.Context, options SemanticCoverageLookupOptions) (SemanticCoverageLookupResult, error) {
	result := SemanticCoverageLookupResult{State: SemanticCoverageBuildUnavailable, Candidates: unavailableSemanticCoverageCandidates(options.Candidates, catalog.SemanticCoverageReasonIncompleteSource)}
	if err := validateSemanticCoverageLookupOptions(options); err != nil {
		return SemanticCoverageLookupResult{}, err
	}
	if options.SchemaIdentity == "" {
		result.Reason = "SOURCE_SCHEMA_IDENTITY_UNAVAILABLE"
		return result, nil
	}
	identity := SemanticCoverageBuildIdentity{
		Project: options.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(options.DatasetGeneration),
		BuildID: options.BuildID, SchemaIdentity: options.SchemaIdentity, SourceKind: options.SourceKind,
	}
	if options.BuildID != catalog.SemanticInventoryBuildID(options.Project, identity.DatasetGeneration) {
		result.Reason = "BUILD_ID_MISMATCH"
		return result, nil
	}

	resourceTypes := make([]string, 0, len(options.Candidates))
	candidateBinds := make([]map[string]string, 0, len(options.Candidates))
	for _, candidate := range options.Candidates {
		resourceTypes = append(resourceTypes, candidate.ResourceType)
		candidateBinds = append(candidateBinds, map[string]string{"resource_type": candidate.ResourceType, "canonical_path": candidate.CanonicalPath})
	}
	sort.Strings(resourceTypes)
	resourceTypes = compactAuthPaths(resourceTypes)
	authPaths := append([]string(nil), options.AuthResourcePaths...)
	sort.Strings(authPaths)
	authPaths = compactAuthPaths(authPaths)
	sourceProofLimit := semanticCoverageMaximumPartitions + 1
	if !options.AuthResourcePathsUnrestricted {
		sourceProofLimit = semanticCoverageLookupMaximumCandidates*semanticCoverageLookupMaximumAuthPaths + 1
	}
	snapshotBinds := map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "source_kind": identity.SourceKind, "schema_identity": identity.SchemaIdentity,
		"resource_types": resourceTypes, "candidates": candidateBinds, "auth_resource_paths": authPaths,
		"auth_resource_paths_unrestricted": options.AuthResourcePathsUnrestricted,
		"resource_limit":                   len(resourceTypes) + 1,
		"partition_limit":                  semanticCoverageLookupMaximumPartitions + 1,
		"facet_limit":                      semanticCoverageLookupMaximumRows + 1,
		"source_proof_limit":               sourceProofLimit,
	}
	var snapshot semanticCoverageLookupSnapshot
	found := false
	rows := 0
	err := s.client.QueryRows(ctx, semanticCoverageLookupSnapshotAQL, 1, snapshotBinds, func(row map[string]any) error {
		rows++
		if rows > 1 {
			return ErrSemanticCoverageLookupTooLarge
		}
		if err := decodeSemanticCoverageRow(row, &snapshot); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return SemanticCoverageLookupResult{}, fmt.Errorf("read semantic coverage lookup snapshot: %w", err)
	}
	if !found || snapshot.Build == nil {
		result.Reason = "COVERAGE_BUILD_MISSING"
		return result, nil
	}
	marker := *snapshot.Build
	if marker.RecordKind != "build" || marker.Key != semanticCoverageBuildKey(identity) || marker.Project != identity.Project ||
		marker.DatasetGeneration != identity.DatasetGeneration || marker.BuildID != identity.BuildID || marker.SourceKind != identity.SourceKind || marker.SchemaIdentity != identity.SchemaIdentity {
		result.Reason = "COVERAGE_BUILD_IDENTITY_MISMATCH"
		return result, nil
	}
	result.State = marker.BuildState
	result.Reason = marker.BuildReason
	if marker.CoverageRuleVersion != catalog.SemanticCoverageRuleVersion || marker.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion {
		result.State = SemanticCoverageBuildUnavailable
		result.Reason = "COVERAGE_FORMAT_VERSION_MISMATCH"
		return result, nil
	}
	if result.State != SemanticCoverageBuildComplete && result.State != SemanticCoverageBuildPartial {
		return result, nil
	}
	if len(snapshot.Resources) > len(resourceTypes) || len(snapshot.Partitions) > semanticCoverageLookupMaximumPartitions || len(snapshot.Facets) > semanticCoverageLookupMaximumRows ||
		len(snapshot.SourceProof) > semanticCoverageMaximumPartitions {
		result.Reason = "LOOKUP_ROW_LIMIT"
		return result, nil
	}

	resourceMarkers := make(map[string]semanticCoverageStoredDocument, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		if resource.RecordKind != "resource" || resource.Key != semanticCoverageResourceKey(identity, resource.ResourceType) ||
			resource.Project != identity.Project || resource.DatasetGeneration != identity.DatasetGeneration || resource.BuildID != identity.BuildID ||
			resource.SourceKind != identity.SourceKind || resource.SchemaIdentity != identity.SchemaIdentity ||
			resource.CoverageRuleVersion != catalog.SemanticCoverageRuleVersion ||
			resource.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion {
			result.Reason = "RESOURCE_CERTIFICATE_IDENTITY_MISMATCH"
			return result, nil
		}
		if _, duplicate := resourceMarkers[resource.ResourceType]; duplicate {
			result.Reason = "RESOURCE_CERTIFICATE_DUPLICATE"
			return result, nil
		}
		resourceMarkers[resource.ResourceType] = resource
	}
	partitionsByResource := make(map[string]map[string]semanticCoverageStoredDocument, len(resourceTypes))
	for _, partition := range snapshot.Partitions {
		if partition.RecordKind != "partition" || partition.Key != semanticCoveragePartitionDocumentKey(identity, partition.ResourceType, partition.AuthResourcePath) ||
			partition.Project != identity.Project || partition.DatasetGeneration != identity.DatasetGeneration || partition.BuildID != identity.BuildID ||
			partition.SourceKind != identity.SourceKind || partition.SchemaIdentity != identity.SchemaIdentity ||
			partition.CoverageStorageFormatVersion != catalog.SemanticCoverageStorageFormatVersion {
			result.Reason = "RESOURCE_PARTITION_IDENTITY_MISMATCH"
			return result, nil
		}
		if partitionsByResource[partition.ResourceType] == nil {
			partitionsByResource[partition.ResourceType] = make(map[string]semanticCoverageStoredDocument)
		}
		if _, duplicate := partitionsByResource[partition.ResourceType][partition.AuthResourcePath]; duplicate {
			result.Reason = "RESOURCE_PARTITION_DUPLICATE"
			return result, nil
		}
		partitionsByResource[partition.ResourceType][partition.AuthResourcePath] = partition
	}
	sourceProofByPartition := make(map[string]bool, len(snapshot.SourceProof))
	for _, proof := range snapshot.SourceProof {
		key := semanticCoveragePartitionKey(proof.AuthResourcePath, proof.ResourceType)
		if _, duplicate := sourceProofByPartition[key]; duplicate {
			result.Reason = "RESOURCE_SOURCE_PROOF_DUPLICATE"
			return result, nil
		}
		sourceProofByPartition[key] = proof.SourceExists
	}
	expectedSourceProofs := len(snapshot.Partitions)
	if !options.AuthResourcePathsUnrestricted {
		expectedSourceProofs = len(resourceTypes) * len(authPaths)
	}
	if len(snapshot.SourceProof) != expectedSourceProofs {
		result.Reason = "RESOURCE_SOURCE_PROOF_INCOMPLETE"
		result.Candidates = unavailableSemanticCoverageCandidates(options.Candidates, catalog.SemanticCoverageReasonIncompleteSource)
		return result, nil
	}
	facetsByCandidate := make(map[SemanticCoverageCandidatePath][]semanticCoverageStoredDocument, len(options.Candidates))
	for _, facet := range snapshot.Facets {
		key := SemanticCoverageCandidatePath{ResourceType: facet.ResourceType, CanonicalPath: facet.CanonicalPath}
		if facet.RecordKind != "summary" || facet.SummaryKind != "facet" ||
			facet.Key != semanticCoverageSummaryDocumentKey(identity, facet) ||
			facet.Project != identity.Project || facet.DatasetGeneration != identity.DatasetGeneration || facet.BuildID != identity.BuildID ||
			facet.SourceKind != identity.SourceKind || facet.SchemaIdentity != identity.SchemaIdentity {
			result.Reason = "FACET_SUMMARY_IDENTITY_MISMATCH"
			return result, nil
		}
		facetsByCandidate[key] = append(facetsByCandidate[key], facet)
	}

	result.Candidates = make([]SemanticCoverageCandidateResult, 0, len(options.Candidates))
	for _, candidate := range options.Candidates {
		candidateResult := SemanticCoverageCandidateResult{ResourceType: candidate.ResourceType, CanonicalPath: candidate.CanonicalPath}
		candidateResult.CoverageID = semanticCoverageDocumentKey("semantic-coverage-absent/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, candidate.ResourceType, candidate.CanonicalPath)
		resource, exists := resourceMarkers[candidate.ResourceType]
		if !containsSortedSemanticCoverageResource(marker.ExpectedResourceTypes, candidate.ResourceType) {
			candidateResult.Status = SemanticCoverageCandidateUnavailable
			candidateResult.ResourceState = SemanticCoverageBuildUnavailable
			candidateResult.ResourceReason = "RESOURCE_NOT_IN_EXPECTED_MANIFEST"
			candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		if !exists {
			candidateResult.Status = SemanticCoverageCandidateUnavailable
			candidateResult.ResourceState = SemanticCoverageBuildUnavailable
			candidateResult.ResourceReason = "RESOURCE_CERTIFICATE_MISSING"
			candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		candidateResult.ResourceState = resource.ResourceState
		candidateResult.ResourceReason = resource.BuildReason
		if resource.ResourceState != SemanticCoverageBuildComplete {
			candidateResult.Status = SemanticCoverageCandidateUnavailable
			if resource.BuildReason == "" {
				candidateResult.ResourceReason = "RESOURCE_CERTIFICATE_INCOMPLETE"
			}
			candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		if !resource.SourceEnumerationComplete || resource.SourceSetDigest == "" ||
			resource.ExpectedSourceCount != resource.MaterializedSourceCount ||
			resource.SourcePartitionCount != resource.MaterializedPartitionCount {
			candidateResult.Status = SemanticCoverageCandidateUnavailable
			candidateResult.ResourceState = SemanticCoverageBuildFailed
			candidateResult.ResourceReason = "RESOURCE_CERTIFICATE_INCOMPLETE"
			candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		if options.AuthResourcePathsUnrestricted {
			partitions := partitionsByResource[candidate.ResourceType]
			if int64(len(partitions)) != resource.SourcePartitionCount {
				candidateResult.Status = SemanticCoverageCandidateUnavailable
				candidateResult.ResourceReason = "RESOURCE_PARTITION_CERTIFICATE_INCOMPLETE"
				candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
				result.Candidates = append(result.Candidates, candidateResult)
				continue
			}
			for authPath, partition := range partitions {
				if !validSemanticCoveragePartition(partition) || !sourceProofByPartition[semanticCoveragePartitionKey(authPath, candidate.ResourceType)] {
					candidateResult.Status = SemanticCoverageCandidateUnavailable
					candidateResult.ResourceReason = "RESOURCE_PARTITION_CERTIFICATE_INCOMPLETE"
					candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
					break
				}
			}
		} else {
			partitions := partitionsByResource[candidate.ResourceType]
			for _, authPath := range authPaths {
				partition, hasPartition := partitions[authPath]
				sourceKey := semanticCoveragePartitionKey(authPath, candidate.ResourceType)
				sourceExists, hasSourceProof := sourceProofByPartition[sourceKey]
				if !hasSourceProof || sourceExists != hasPartition || (hasPartition && !validSemanticCoveragePartition(partition)) {
					candidateResult.Status = SemanticCoverageCandidateUnavailable
					candidateResult.ResourceReason = "RESOURCE_PARTITION_CERTIFICATE_INCOMPLETE"
					candidateResult.Reason = catalog.SemanticCoverageReasonIncompleteSource
					break
				}
			}
		}
		if candidateResult.Status == SemanticCoverageCandidateUnavailable {
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		candidateResult.Status = SemanticCoverageCandidateKeepRaw
		candidateResult.Reason = catalog.SemanticCoverageReasonUnrepresentedMetadata
		rows := facetsByCandidate[candidate]
		if len(rows) == 0 {
			result.Candidates = append(result.Candidates, candidateResult)
			continue
		}
		candidateResult.Status = SemanticCoverageCandidateGroupable
		candidateResult.Reason = ""
		candidateResult.References = []catalog.SemanticCoverageReference{}
		ids := make([]string, 0, len(rows))
		references := make(map[string]catalog.SemanticCoverageReference)
		reasons := make(map[catalog.SemanticCoverageReason]struct{})
		for _, row := range rows {
			ids = append(ids, row.Key)
			switch row.Status {
			case SemanticCoverageCandidateGroupable:
				if row.Path == nil || !row.Groupable || row.CountsTruncated || row.FacetsTruncated || row.ReferencesTruncated || row.Path.CountsTruncated || row.Path.FacetsTruncated || row.Path.ReferencesTruncated {
					candidateResult.Status = SemanticCoverageCandidateUnavailable
					reasons[catalog.SemanticCoverageReasonTruncatedEvidence] = struct{}{}
					continue
				}
				for _, reference := range row.Path.References {
					converted, ok := semanticCoverageCatalogReference(reference)
					if ok {
						converted.CandidatePath = candidate.CanonicalPath
						converted.ResourceType = candidate.ResourceType
						references[semanticCoverageReferenceKey(converted)] = converted
					}
				}
			case SemanticCoverageCandidateKeepRaw:
				if candidateResult.Status != SemanticCoverageCandidateUnavailable {
					candidateResult.Status = SemanticCoverageCandidateKeepRaw
				}
				if row.Reason != "" {
					reasons[catalog.SemanticCoverageReason(row.Reason)] = struct{}{}
				}
			case SemanticCoverageCandidateUnavailable:
				candidateResult.Status = SemanticCoverageCandidateUnavailable
				if row.Reason != "" {
					reasons[catalog.SemanticCoverageReason(row.Reason)] = struct{}{}
				}
			default:
				candidateResult.Status = SemanticCoverageCandidateUnavailable
				reasons[catalog.SemanticCoverageReasonIncompleteSource] = struct{}{}
			}
		}
		sort.Strings(ids)
		candidateResult.CoverageID = semanticCoverageDocumentKey(append([]string{"semantic-coverage-result/v1"}, ids...)...)
		if candidateResult.Status == SemanticCoverageCandidateGroupable && len(references) == 0 {
			candidateResult.Status = SemanticCoverageCandidateKeepRaw
			reasons[catalog.SemanticCoverageReasonUnrepresentedMetadata] = struct{}{}
		}
		if candidateResult.Status == SemanticCoverageCandidateGroupable {
			for _, reference := range references {
				candidateResult.References = append(candidateResult.References, reference)
			}
			sort.Slice(candidateResult.References, func(i, j int) bool {
				return semanticCoverageReferenceKey(candidateResult.References[i]) < semanticCoverageReferenceKey(candidateResult.References[j])
			})
		} else {
			candidateResult.References = nil
		}
		candidateResult.Reason = singleSemanticCoverageReason(reasons)
		result.Candidates = append(result.Candidates, candidateResult)
	}
	return result, nil
}

func validSemanticCoveragePartition(partition semanticCoverageStoredDocument) bool {
	return partition.Complete && partition.Groupable && partition.Status == SemanticCoverageCandidateGroupable &&
		partition.SourceRecordCount > 0 && partition.SourceSetDigest != ""
}

func containsSortedSemanticCoverageResource(resources []string, candidate string) bool {
	index := sort.SearchStrings(resources, candidate)
	return index < len(resources) && resources[index] == candidate
}

func semanticCoverageSummaryDocumentKey(identity SemanticCoverageBuildIdentity, document semanticCoverageStoredDocument) string {
	return semanticCoverageDocumentKey(
		"semantic-coverage/v1", identity.Project, identity.DatasetGeneration, identity.BuildID,
		identity.SourceKind, identity.SchemaIdentity, "summary", document.SummaryKind,
		document.AuthResourcePath, document.ResourceType, document.CanonicalPath, document.ResourceBoundarySignature,
	)
}

const semanticCoverageLookupMaximumCandidates = 50
const semanticCoverageLookupMaximumAuthPaths = 100
const semanticCoverageLookupMaximumRows = semanticCoverageLookupMaximumCandidates * semanticCoverageLookupMaximumAuthPaths
const semanticCoverageLookupMaximumPartitions = semanticCoverageMaximumPartitions

func validateSemanticCoverageLookupOptions(options SemanticCoverageLookupOptions) error {
	if strings.TrimSpace(options.Project) == "" || catalog.NormalizeDatasetGeneration(options.DatasetGeneration) == "" ||
		strings.TrimSpace(options.BuildID) == "" || strings.TrimSpace(options.SourceKind) == "" ||
		len(options.Candidates) == 0 || len(options.Candidates) > semanticCoverageLookupMaximumCandidates ||
		(!options.AuthResourcePathsUnrestricted && (len(options.AuthResourcePaths) == 0 || len(options.AuthResourcePaths) > semanticCoverageLookupMaximumAuthPaths)) {
		return ErrInvalidSemanticCoverageRead
	}
	seen := make(map[SemanticCoverageCandidatePath]struct{}, len(options.Candidates))
	for _, candidate := range options.Candidates {
		if strings.TrimSpace(candidate.ResourceType) == "" || strings.TrimSpace(candidate.CanonicalPath) == "" {
			return ErrInvalidSemanticCoverageRead
		}
		if _, exists := seen[candidate]; exists {
			return ErrInvalidSemanticCoverageRead
		}
		seen[candidate] = struct{}{}
	}
	for _, authPath := range options.AuthResourcePaths {
		if strings.TrimSpace(authPath) == "" {
			return ErrInvalidSemanticCoverageRead
		}
	}
	return nil
}

func unavailableSemanticCoverageCandidates(candidates []SemanticCoverageCandidatePath, reason catalog.SemanticCoverageReason) []SemanticCoverageCandidateResult {
	result := make([]SemanticCoverageCandidateResult, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, unavailableSemanticCoverageCandidate(candidate, reason))
	}
	return result
}

func unavailableSemanticCoverageCandidate(candidate SemanticCoverageCandidatePath, reason catalog.SemanticCoverageReason) SemanticCoverageCandidateResult {
	return SemanticCoverageCandidateResult{ResourceType: candidate.ResourceType, CanonicalPath: candidate.CanonicalPath, Status: SemanticCoverageCandidateUnavailable, Reason: reason}
}

func singleSemanticCoverageReason(reasons map[catalog.SemanticCoverageReason]struct{}) catalog.SemanticCoverageReason {
	if len(reasons) == 0 {
		return ""
	}
	if len(reasons) == 1 {
		for reason := range reasons {
			return reason
		}
	}
	return catalog.SemanticCoverageReasonUnrepresentedMetadata
}

func decodeSemanticCoverageRow(row map[string]any, target any) error {
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode semantic coverage row: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode semantic coverage row: %w", err)
	}
	return nil
}

const semanticCoverageBuildMarkerAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind == "build"
  LIMIT 1
  RETURN d`

const semanticCoverageCandidateLookupAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind == "summary"
  FILTER d.summary_kind == "facet"
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER LENGTH(
    FOR candidate IN @candidates
      FILTER candidate.resource_type == d.resource_type
      FILTER candidate.canonical_path == d.canonical_path
      LIMIT 1
      RETURN 1
  ) > 0
  SORT d.resource_type, d.canonical_path, d.resource_boundary_signature, d.auth_resource_path
  LIMIT @limit
  RETURN d`

const semanticCoveragePartitionLookupAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind == "partition"
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER d.resource_type IN @resource_types
  SORT d.resource_type, d.auth_resource_path
  LIMIT @limit
  RETURN d`
