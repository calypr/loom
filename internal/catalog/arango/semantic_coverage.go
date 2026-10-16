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

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	SemanticCoverageCollection                  = "fhir_semantic_coverage"
	semanticCoverageDefaultBatchSize            = 500
	semanticCoverageMaximumBatchSize            = 500
	semanticCoverageMaximumPageSize             = 500
	semanticCoverageMaximumRowBytes             = 1 << 20
	semanticCoverageMaximumSourceScopesPerBatch = 10000
	semanticCoverageMaximumSourceRowsPerBatch   = 100000
	semanticCoverageMaximumSourceBytesPerBatch  = 64 << 20
	semanticCoverageMaximumPartitions           = 10000
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
)

type SemanticCoverageBuildState string

const (
	SemanticCoverageBuildBuilding    SemanticCoverageBuildState = "BUILDING"
	SemanticCoverageBuildFailed      SemanticCoverageBuildState = "FAILED"
	SemanticCoverageBuildUnavailable SemanticCoverageBuildState = "UNAVAILABLE"
	SemanticCoverageBuildComplete    SemanticCoverageBuildState = "COMPLETE"
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
	ResourceType  string
	CanonicalPath string
	CoverageID    string
	Status        SemanticCoverageCandidateStatus
	Reason        catalog.SemanticCoverageReason
	References    []catalog.SemanticCoverageReference
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
	Key                         string                             `json:"_key"`
	RecordKind                  string                             `json:"record_kind"`
	BuildState                  SemanticCoverageBuildState         `json:"build_state,omitempty"`
	BuildReason                 string                             `json:"build_reason,omitempty"`
	CoverageRuleVersion         int                                `json:"coverage_rule_version,omitempty"`
	SummaryKind                 string                             `json:"summary_kind,omitempty"`
	OwnerCanonicalPath          string                             `json:"owner_canonical_path,omitempty"`
	Project                     string                             `json:"project"`
	DatasetGeneration           string                             `json:"dataset_generation"`
	BuildID                     string                             `json:"build_id"`
	SourceKind                  string                             `json:"source_kind"`
	AuthResourcePath            string                             `json:"auth_resource_path"`
	SchemaIdentity              string                             `json:"schema_identity"`
	ResourceType                string                             `json:"resource_type"`
	ResourceBoundarySignature   string                             `json:"resource_boundary_signature,omitempty"`
	SourceID                    string                             `json:"source_id"`
	CanonicalPath               string                             `json:"canonical_path"`
	Counts                      map[string]int64                   `json:"counts,omitempty"`
	Path                        *semanticCoverageStoredPath        `json:"path,omitempty"`
	Diagnostics                 []semanticCoverageStoredDiagnostic `json:"diagnostics,omitempty"`
	SourceRecordCount           int64                              `json:"source_record_count,omitempty"`
	ExpectedSourceCount         int64                              `json:"expected_source_count,omitempty"`
	MaterializedSourceCount     int64                              `json:"materialized_source_count,omitempty"`
	SummaryCount                int64                              `json:"summary_count,omitempty"`
	Complete                    bool                               `json:"complete"`
	CountsTruncated             bool                               `json:"counts_truncated,omitempty"`
	FacetsTruncated             bool                               `json:"facets_truncated,omitempty"`
	ReferencesTruncated         bool                               `json:"references_truncated,omitempty"`
	DiagnosticLocatorsTruncated bool                               `json:"diagnostic_locators_truncated,omitempty"`
	ReconciliationTruncated     bool                               `json:"reconciliation_truncated,omitempty"`
	IncompleteGaps              []string                           `json:"incomplete_gaps,omitempty"`
	Groupable                   bool                               `json:"groupable,omitempty"`
	Status                      SemanticCoverageCandidateStatus    `json:"status,omitempty"`
	Reason                      string                             `json:"reason,omitempty"`
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
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "auth_resource_path", "resource_type", "canonical_path", "resource_boundary_signature", "source_id", "_key"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "resource_type", "canonical_path", "resource_boundary_signature", "auth_resource_path"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "auth_resource_path", "resource_type"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "resource_type", "source_id", "record_kind"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind", "auth_resource_path", "resource_type", "source_id"},
			{"project", "dataset_generation", "build_id", "source_kind", "schema_identity", "record_kind"},
		},
	}
}

// EncodeSemanticCoverageContribution expands one source contribution into a
// source sentinel and durable path/facet aggregates keyed by canonical path,
// disposition, reason, and resource-boundary signature.
// The exact inventory build is repeated on every row and participates in its
// deterministic key.
func EncodeSemanticCoverageContribution(buildID string, contribution catalog.SemanticCoverageSourceContribution) ([]json.RawMessage, error) {
	if err := validateSemanticCoverageContribution(buildID, contribution); err != nil {
		return nil, err
	}
	identity := contribution.Identity
	base := semanticCoverageStoredDocument{
		Project:                     identity.Project,
		DatasetGeneration:           catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		BuildID:                     buildID,
		SourceKind:                  identity.SourceKind,
		AuthResourcePath:            identity.AuthResourcePath,
		SchemaIdentity:              identity.SchemaIdentity,
		ResourceType:                identity.ResourceType,
		SourceID:                    identity.SourceID,
		Complete:                    contribution.Complete,
		CountsTruncated:             contribution.CountsTruncated,
		FacetsTruncated:             contribution.FacetsTruncated,
		ReferencesTruncated:         contribution.ReferencesTruncated,
		DiagnosticLocatorsTruncated: contribution.DiagnosticLocatorsTruncated,
		ReconciliationTruncated:     contribution.ReconciliationTruncated,
		IncompleteGaps:              semanticCoverageGapStrings(contribution.IncompleteGaps),
	}
	base.RecordKind = "source"
	base.Counts = semanticCoverageCountsDocument(contribution.Counts)
	base.Diagnostics = semanticCoverageDiagnosticsDocument(contribution.Diagnostics)
	base.SourceRecordCount = contribution.SourceRecordCount
	base.Key = semanticCoverageContributionKey(identity, buildID, "source", "", "", "", "", "")
	encodedSource, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("encode semantic coverage source sentinel: %w", err)
	}
	if err := validateSemanticCoverageDocument(encodedSource); err != nil {
		return nil, fmt.Errorf("validate semantic coverage source sentinel: %w", err)
	}
	sourceDocument := json.RawMessage(encodedSource)
	documents := make([]json.RawMessage, 0, len(contribution.Paths)+1)
	seenPaths := make(map[string]struct{}, len(contribution.Paths))
	facetAggregates := make(map[semanticCoverageFacetKey]*semanticCoverageFacetSourceAggregate)
	for _, path := range contribution.Paths {
		if err := validateSemanticCoveragePath(path); err != nil {
			return nil, err
		}
		pathKey := semanticCoverageDocumentKey(path.CanonicalPath, path.ResourceBoundarySignature, string(path.Kind), string(path.Disposition), string(path.Reason))
		if _, exists := seenPaths[pathKey]; exists {
			return nil, fmt.Errorf("duplicate semantic coverage path aggregate %q", path.CanonicalPath)
		}
		seenPaths[pathKey] = struct{}{}
		pathDocument := base
		pathDocument.RecordKind = "path"
		pathDocument.CanonicalPath = path.CanonicalPath
		pathDocument.ResourceBoundarySignature = path.ResourceBoundarySignature
		pathDocument.Path = semanticCoveragePathDocument(path)
		pathDocument.Counts = nil
		pathDocument.Diagnostics = nil
		pathDocument.SourceRecordCount = 0
		pathDocument.Groupable = contribution.Complete && path.ResourceBoundarySignature == "" &&
			!contribution.CountsTruncated && !contribution.FacetsTruncated && !contribution.ReferencesTruncated &&
			!contribution.ReconciliationTruncated && len(contribution.IncompleteGaps) == 0 &&
			!path.CountsTruncated && !path.FacetsTruncated && !path.ReferencesTruncated &&
			path.Disposition == catalog.SemanticCoverageSemanticDisposition
		pathDocument.Key = semanticCoverageContributionKey(
			identity,
			buildID,
			"path",
			path.CanonicalPath,
			string(path.Kind),
			string(path.Disposition),
			string(path.Reason),
			path.ResourceBoundarySignature,
		)
		if err := appendSemanticCoverageDocument(&documents, pathDocument); err != nil {
			return nil, err
		}
		for _, facet := range path.Facets {
			if strings.TrimSpace(facet.Path) == "" || strings.TrimSpace(facet.Kind) == "" || facet.OccurrenceCount < 0 {
				return nil, errors.New("semantic coverage facet aggregate is incomplete")
			}
			key := semanticCoverageFacetKey{path: facet.Path, boundarySignature: facet.ResourceBoundarySignature, kind: facet.Kind, disposition: string(facet.Disposition), reason: string(facet.Reason)}
			aggregate := facetAggregates[key]
			if aggregate == nil {
				aggregate = &semanticCoverageFacetSourceAggregate{references: make(map[string]catalog.SemanticCoverageReference)}
				facetAggregates[key] = aggregate
			}
			aggregate.occurrenceCount += facet.OccurrenceCount
			for presence, count := range facet.PresenceCounts {
				aggregate.presenceCounts = ensureSemanticCoveragePresenceCounts(aggregate.presenceCounts)
				aggregate.presenceCounts[string(presence)] += count
			}
			aggregate.countsTruncated = aggregate.countsTruncated || path.CountsTruncated
			aggregate.facetsTruncated = aggregate.facetsTruncated || path.FacetsTruncated
			aggregate.referencesTruncated = aggregate.referencesTruncated || facet.ReferencesTruncated || path.ReferencesTruncated
			aggregate.groupable = aggregate.groupable || (contribution.Complete && path.ResourceBoundarySignature == "" && facet.ResourceBoundarySignature == "" && !contribution.CountsTruncated && !contribution.FacetsTruncated && !contribution.ReferencesTruncated && !contribution.ReconciliationTruncated && len(contribution.IncompleteGaps) == 0 && !path.CountsTruncated && !path.FacetsTruncated && !path.ReferencesTruncated && !facet.ReferencesTruncated && facet.Disposition == catalog.SemanticCoverageSemanticDisposition)
			for _, reference := range facet.References {
				if reference.Kind == catalog.SemanticCoverageNativeFieldReference {
					continue
				}
				if converted, ok := semanticCoverageCatalogReferenceFromCatalog(reference); ok {
					referenceKey := semanticCoverageReferenceKey(converted)
					if _, exists := aggregate.references[referenceKey]; exists {
						continue
					}
					if len(aggregate.references) >= semanticCoverageMaximumReferencesPerSummary {
						aggregate.referencesTruncated = true
						aggregate.groupable = false
						continue
					}
					aggregate.references[referenceKey] = converted
				}
			}
		}
	}
	facetKeys := make([]semanticCoverageFacetKey, 0, len(facetAggregates))
	for key := range facetAggregates {
		facetKeys = append(facetKeys, key)
	}
	sort.Slice(facetKeys, func(i, j int) bool {
		return semanticCoverageFacetKeyString(facetKeys[i]) < semanticCoverageFacetKeyString(facetKeys[j])
	})
	for _, key := range facetKeys {
		facetDocument := semanticCoverageFacetSourceDocument(identity, buildID, contribution, key, facetAggregates[key])
		if err := appendSemanticCoverageDocument(&documents, facetDocument); err != nil {
			return nil, err
		}
	}
	documents = append(documents, sourceDocument)
	return documents, nil
}

type semanticCoverageFacetKey struct {
	path              string
	boundarySignature string
	kind              string
	disposition       string
	reason            string
}

type semanticCoverageFacetSourceAggregate struct {
	occurrenceCount     int64
	presenceCounts      map[string]int64
	countsTruncated     bool
	facetsTruncated     bool
	referencesTruncated bool
	groupable           bool
	references          map[string]catalog.SemanticCoverageReference
}

func semanticCoverageFacetKeyString(key semanticCoverageFacetKey) string {
	return semanticCoverageDocumentKey("facet", key.path, key.boundarySignature, key.kind, key.disposition, key.reason)
}

func ensureSemanticCoveragePresenceCounts(counts map[string]int64) map[string]int64 {
	if counts == nil {
		return make(map[string]int64)
	}
	return counts
}

func semanticCoverageFacetSourceDocument(identity catalog.SemanticCoverageSourceIdentity, buildID string, contribution catalog.SemanticCoverageSourceContribution, key semanticCoverageFacetKey, aggregate *semanticCoverageFacetSourceAggregate) semanticCoverageStoredDocument {
	references := make([]catalog.SemanticCoverageReference, 0, len(aggregate.references))
	for _, reference := range aggregate.references {
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool {
		return semanticCoverageReferenceKey(references[i]) < semanticCoverageReferenceKey(references[j])
	})
	storedReferences := semanticCoverageStoredReferences(references)
	groupable := aggregate.groupable && len(storedReferences) > 0 && !aggregate.countsTruncated && !aggregate.facetsTruncated && !aggregate.referencesTruncated
	path := &semanticCoverageStoredPath{
		ResourceBoundarySignature: key.boundarySignature, Kind: key.kind, Disposition: key.disposition, Reason: key.reason, OccurrenceCount: aggregate.occurrenceCount,
		PresenceCounts: aggregate.presenceCounts, References: storedReferences,
		CountsTruncated: aggregate.countsTruncated, FacetsTruncated: aggregate.facetsTruncated, ReferencesTruncated: aggregate.referencesTruncated,
		Facets: []semanticCoverageStoredFacet{{Path: key.path, ResourceBoundarySignature: key.boundarySignature, Kind: key.kind, Disposition: key.disposition, Reason: key.reason, OccurrenceCount: aggregate.occurrenceCount, PresenceCounts: aggregate.presenceCounts, References: storedReferences, ReferencesTruncated: aggregate.referencesTruncated}},
	}
	return semanticCoverageStoredDocument{
		Key:        semanticCoverageContributionKey(identity, buildID, "facet", key.path, key.kind, key.disposition, key.reason, key.boundarySignature),
		RecordKind: "facet", Project: identity.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		BuildID: buildID, SourceKind: identity.SourceKind, AuthResourcePath: identity.AuthResourcePath,
		SchemaIdentity: identity.SchemaIdentity, ResourceType: identity.ResourceType,
		ResourceBoundarySignature: key.boundarySignature, SourceID: identity.SourceID,
		CanonicalPath: key.path, Complete: contribution.Complete, CountsTruncated: contribution.CountsTruncated || aggregate.countsTruncated,
		FacetsTruncated: contribution.FacetsTruncated || aggregate.facetsTruncated, ReferencesTruncated: contribution.ReferencesTruncated || aggregate.referencesTruncated,
		ReconciliationTruncated: contribution.ReconciliationTruncated, IncompleteGaps: semanticCoverageGapStrings(contribution.IncompleteGaps),
		Groupable: groupable, Path: path,
	}
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
		encodedSets := make([][]json.RawMessage, 0, len(chunk))
		newScopes := make([]semanticCoverageSourceScope, 0, len(chunk))
		seenSourceIDs := make(map[semanticCoverageSourceScopeKey]string, len(chunk))
		encodedRows := 0
		encodedBytes := int64(0)
		for index, contribution := range chunk {
			sourceKey := semanticCoverageSourceScopeKey{sourceID: contribution.Identity.SourceID, resourceType: contribution.Identity.ResourceType}
			if previousAuthPath, exists := seenSourceIDs[sourceKey]; exists {
				if previousAuthPath != contribution.Identity.AuthResourcePath {
					return stats, fmt.Errorf("semantic coverage source ID %q is ambiguous across authorization paths", contribution.Identity.SourceID)
				}
				return stats, fmt.Errorf("duplicate semantic coverage source ID %q in one write batch", contribution.Identity.SourceID)
			}
			seenSourceIDs[sourceKey] = contribution.Identity.AuthResourcePath
			encoded, err := EncodeSemanticCoverageContribution(buildID, contribution)
			if err != nil {
				return stats, fmt.Errorf("encode semantic coverage source %d: %w", start+index, err)
			}
			for _, document := range encoded {
				encodedRows++
				encodedBytes += int64(len(document))
			}
			if encodedRows > semanticCoverageMaximumSourceRowsPerBatch || encodedBytes > semanticCoverageMaximumSourceBytesPerBatch {
				return stats, ErrSemanticCoverageSourceBatchLimit
			}
			identity := contribution.Identity
			newScopes = append(newScopes, semanticCoverageSourceScope{
				SourceID: identity.SourceID, ResourceType: identity.ResourceType, AuthResourcePath: identity.AuthResourcePath,
			})
			encodedSets = append(encodedSets, encoded)
		}
		for index, contribution := range chunk {
			if contribution.Identity.Project != chunk[0].Identity.Project ||
				contribution.Identity.DatasetGeneration != chunk[0].Identity.DatasetGeneration ||
				contribution.Identity.SourceKind != chunk[0].Identity.SourceKind ||
				contribution.Identity.SchemaIdentity != chunk[0].Identity.SchemaIdentity {
				return stats, fmt.Errorf("semantic coverage source %d has a mixed build partition", start+index)
			}
		}
		scopes, err := s.semanticCoverageSourceScopes(ctx, chunk[0].Identity, buildID, newScopes)
		if err != nil {
			return stats, fmt.Errorf("replace semantic coverage source batch: read existing source scopes: %w", err)
		}
		if err := s.client.ExecuteAQL(ctx, deleteSemanticCoverageSourceSetBatchAQL, map[string]interface{}{
			"project":            chunk[0].Identity.Project,
			"dataset_generation": catalog.NormalizeDatasetGeneration(chunk[0].Identity.DatasetGeneration),
			"build_id":           buildID,
			"source_kind":        chunk[0].Identity.SourceKind,
			"schema_identity":    chunk[0].Identity.SchemaIdentity,
			"source_scopes":      scopes,
		}); err != nil {
			return stats, fmt.Errorf("replace semantic coverage source batch: remove prior source sets: %w", err)
		}
		stats.SourceSetsReplaced += int64(len(chunk))

		pathBatch := make([]json.RawMessage, 0, batchSize)
		for _, encoded := range encodedSets {
			for _, document := range encoded[:len(encoded)-1] {
				pathBatch = append(pathBatch, document)
				if len(pathBatch) == batchSize {
					written, writeErr := s.writeSemanticCoverageDocuments(ctx, pathBatch, batchSize)
					stats.Rows += written.Rows
					stats.SerializedBytes += written.SerializedBytes
					if writeErr != nil {
						return stats, fmt.Errorf("replace semantic coverage source batch: write path rows: %w", writeErr)
					}
					pathBatch = pathBatch[:0]
				}
			}
		}
		if len(pathBatch) > 0 {
			written, writeErr := s.writeSemanticCoverageDocuments(ctx, pathBatch, batchSize)
			stats.Rows += written.Rows
			stats.SerializedBytes += written.SerializedBytes
			if writeErr != nil {
				return stats, fmt.Errorf("replace semantic coverage source batch: write path rows: %w", writeErr)
			}
		}

		sourceDocuments := make([]json.RawMessage, 0, len(encodedSets))
		for _, encoded := range encodedSets {
			sourceDocuments = append(sourceDocuments, encoded[len(encoded)-1])
		}
		written, writeErr := s.writeSemanticCoverageDocuments(ctx, sourceDocuments, batchSize)
		stats.Rows += written.Rows
		stats.SerializedBytes += written.SerializedBytes
		if writeErr != nil {
			return stats, fmt.Errorf("replace semantic coverage source batch: write source sentinels: %w", writeErr)
		}
	}
	return stats, nil
}

type semanticCoverageSourceScope struct {
	SourceID         string `json:"source_id"`
	ResourceType     string `json:"resource_type"`
	AuthResourcePath string `json:"auth_resource_path"`
}

type semanticCoverageSourceScopeKey struct {
	sourceID     string
	resourceType string
}

func (s *Store) semanticCoverageSourceScopes(ctx context.Context, first catalog.SemanticCoverageSourceIdentity, buildID string, current []semanticCoverageSourceScope) ([]semanticCoverageSourceScope, error) {
	currentByID := make(map[semanticCoverageSourceScopeKey]struct{}, len(current))
	resourceTypes := make(map[string]struct{}, len(current))
	for _, scope := range current {
		currentByID[semanticCoverageSourceScopeKey{sourceID: scope.SourceID, resourceType: scope.ResourceType}] = struct{}{}
		resourceTypes[scope.ResourceType] = struct{}{}
	}
	sourceIDSet := make(map[string]struct{}, len(currentByID))
	for scope := range currentByID {
		sourceIDSet[scope.sourceID] = struct{}{}
	}
	sourceIDs := make([]string, 0, len(sourceIDSet))
	for sourceID := range sourceIDSet {
		sourceIDs = append(sourceIDs, sourceID)
	}
	resourceTypeList := make([]string, 0, len(resourceTypes))
	for resourceType := range resourceTypes {
		resourceTypeList = append(resourceTypeList, resourceType)
	}
	sort.Strings(sourceIDs)
	sort.Strings(resourceTypeList)
	rows := make([]semanticCoverageSourceScope, 0, len(current))
	seen := make(map[string]struct{}, len(current))
	oldAuthPaths := make(map[semanticCoverageSourceScopeKey]string, len(current))
	queryErr := s.client.QueryRows(ctx, semanticCoverageSourceScopesAQL, semanticCoverageMaximumSourceScopesPerBatch+1, map[string]interface{}{
		"project": first.Project, "dataset_generation": catalog.NormalizeDatasetGeneration(first.DatasetGeneration),
		"build_id": buildID, "source_kind": first.SourceKind, "schema_identity": first.SchemaIdentity,
		"source_ids": sourceIDs, "resource_types": resourceTypeList, "limit": semanticCoverageMaximumSourceScopesPerBatch + 1,
	}, func(row map[string]any) error {
		var scope semanticCoverageSourceScope
		if err := decodeSemanticCoverageRow(row, &scope); err != nil {
			return err
		}
		identity := semanticCoverageSourceScopeKey{sourceID: scope.SourceID, resourceType: scope.ResourceType}
		if _, relevant := currentByID[identity]; !relevant {
			return nil
		}
		if len(rows) >= semanticCoverageMaximumSourceScopesPerBatch {
			return ErrSemanticCoverageSourceBatchLimit
		}
		if previousPath, exists := oldAuthPaths[identity]; exists && previousPath != scope.AuthResourcePath {
			return fmt.Errorf("stored semantic coverage source %q is ambiguous across authorization paths", scope.SourceID)
		}
		oldAuthPaths[identity] = scope.AuthResourcePath
		key := semanticCoverageDocumentKey(scope.SourceID, scope.ResourceType, scope.AuthResourcePath)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			rows = append(rows, scope)
		}
		return nil
	})
	if queryErr != nil {
		return nil, queryErr
	}
	for _, scope := range current {
		key := semanticCoverageDocumentKey(scope.SourceID, scope.ResourceType, scope.AuthResourcePath)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, scope)
	}
	if len(rows) > semanticCoverageMaximumSourceScopesPerBatch {
		return nil, ErrSemanticCoverageSourceBatchLimit
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SourceID != rows[j].SourceID {
			return rows[i].SourceID < rows[j].SourceID
		}
		if rows[i].ResourceType != rows[j].ResourceType {
			return rows[i].ResourceType < rows[j].ResourceType
		}
		return rows[i].AuthResourcePath < rows[j].AuthResourcePath
	})
	return rows, nil
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

func appendSemanticCoverageDocument(documents *[]json.RawMessage, document semanticCoverageStoredDocument) error {
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode semantic coverage document: %w", err)
	}
	if err := validateSemanticCoverageDocument(encoded); err != nil {
		return fmt.Errorf("validate semantic coverage document: %w", err)
	}
	*documents = append(*documents, encoded)
	return nil
}

func semanticCoverageContributionKey(identity catalog.SemanticCoverageSourceIdentity, buildID, recordKind, canonicalPath, pathKind, disposition, reason, boundarySignature string) string {
	return semanticCoverageDocumentKey(
		"semantic-coverage/v1",
		identity.Project,
		catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		buildID,
		identity.SourceKind,
		identity.AuthResourcePath,
		identity.SchemaIdentity,
		identity.ResourceType,
		identity.SourceID,
		recordKind,
		canonicalPath,
		boundarySignature,
		pathKind,
		disposition,
		reason,
	)
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

func semanticCoverageDiagnosticsDocument(diagnostics []catalog.SemanticCoverageDiagnostic) []semanticCoverageStoredDiagnostic {
	result := make([]semanticCoverageStoredDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, semanticCoverageStoredDiagnostic{
			Code: diagnostic.Code, CanonicalPath: diagnostic.CanonicalPath,
			OwnerPath: diagnostic.OwnerPath, Reason: string(diagnostic.Reason), Explanation: diagnostic.Explanation,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CanonicalPath != result[j].CanonicalPath {
			return result[i].CanonicalPath < result[j].CanonicalPath
		}
		if result[i].OwnerPath != result[j].OwnerPath {
			return result[i].OwnerPath < result[j].OwnerPath
		}
		if result[i].Code != result[j].Code {
			return result[i].Code < result[j].Code
		}
		return result[i].Reason < result[j].Reason
	})
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
	for start := 0; start < len(documents); start += batchSize {
		end := min(start+batchSize, len(documents))
		batch := documents[start:end]
		if err := s.client.InsertBatchRaw(ctx, SemanticCoverageCollection, batch, true, ""); err != nil {
			return stats, fmt.Errorf("write semantic coverage rows %d-%d: %w", start, end-1, err)
		}
		for _, document := range batch {
			stats.SerializedBytes += int64(len(document))
		}
		stats.Rows += int64(len(batch))
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
	if err := s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildBuilding, "", 0, 0, 0); err != nil {
		return err
	}
	if err := s.client.ExecuteAQL(ctx, deleteSemanticCoverageSummariesAQL, semanticCoverageBuildBindVars(identity)); err != nil {
		return fmt.Errorf("clear prior semantic coverage summaries: %w", err)
	}
	return nil
}

// FailSemanticCoverageBuild keeps execution failures distinct from missing
// source proof so readers can report the real failed state.
func (s *Store) FailSemanticCoverageBuild(ctx context.Context, identity SemanticCoverageBuildIdentity, reason string) error {
	if err := validateSemanticCoverageBuildIdentity(identity); err != nil {
		return err
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildFailed, boundedSemanticCoverageReason(reason), 0, 0, 0)
}

// CompleteSemanticCoverageBuild materializes exact-build path summaries only
// after every expected source sentinel exists and carries complete proof. A
// source-availability gap is recorded as UNAVAILABLE and leaves raw choices
// usable; database/query failures remain errors and callers mark FAILED.
func (s *Store) CompleteSemanticCoverageBuild(
	ctx context.Context,
	identity SemanticCoverageBuildIdentity,
	expectedSourceCount int64,
	sourceAvailabilityProven bool,
	unavailableReason string,
	batchSize int,
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

	materializer := newSemanticCoverageMaterializer(identity)
	var sourceCount int64
	err := s.scanSemanticCoverageDocuments(ctx, identity, "source", func(source semanticCoverageStoredDocument) error {
		sourceCount++
		return materializer.addSource(source)
	})
	if errors.Is(err, ErrSemanticCoverageMaterializationLimit) {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "MATERIALIZATION_LIMIT", expectedSourceCount, sourceCount, 0)
	}
	if err != nil {
		return err
	}
	if sourceCount != expectedSourceCount {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "SOURCE_SET_COUNT_MISMATCH", expectedSourceCount, sourceCount, 0)
	}
	if err := s.scanSemanticCoverageDocuments(ctx, identity, "path", func(path semanticCoverageStoredDocument) error {
		return materializer.addPath(path)
	}); errors.Is(err, ErrSemanticCoverageMaterializationLimit) {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "MATERIALIZATION_LIMIT", expectedSourceCount, sourceCount, 0)
	} else if err != nil {
		return err
	}
	if err := s.scanSemanticCoverageDocuments(ctx, identity, "facet", func(facet semanticCoverageStoredDocument) error {
		return materializer.addFacet(facet)
	}); errors.Is(err, ErrSemanticCoverageMaterializationLimit) {
		return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildUnavailable, "MATERIALIZATION_LIMIT", expectedSourceCount, sourceCount, 0)
	} else if err != nil {
		return err
	}
	if err := s.client.ExecuteAQL(ctx, deleteSemanticCoverageSummariesAQL, semanticCoverageBuildBindVars(identity)); err != nil {
		return fmt.Errorf("replace semantic coverage summaries: clear old rows: %w", err)
	}
	stats, err := s.writeSemanticCoverageSummaries(ctx, materializer, batchSize)
	if err != nil {
		return fmt.Errorf("write semantic coverage summaries: %w", err)
	}
	partitionStats, err := s.writeSemanticCoveragePartitions(ctx, materializer, batchSize)
	if err != nil {
		return fmt.Errorf("write semantic coverage partition markers: %w", err)
	}
	return s.writeSemanticCoverageBuildMarker(ctx, identity, SemanticCoverageBuildComplete, "", expectedSourceCount, sourceCount, stats.Rows+partitionStats.Rows)
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
) error {
	document := semanticCoverageStoredDocument{
		Key: semanticCoverageBuildKey(identity), RecordKind: "build", BuildState: state, BuildReason: reason,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, BuildID: identity.BuildID,
		SourceKind: identity.SourceKind, SchemaIdentity: identity.SchemaIdentity, CoverageRuleVersion: catalog.SemanticCoverageRuleVersion,
		ExpectedSourceCount: expectedSources, MaterializedSourceCount: materializedSources, SummaryCount: summaryCount,
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

func semanticCoverageBuildKey(identity SemanticCoverageBuildIdentity) string {
	return semanticCoverageDocumentKey("semantic-coverage/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, "build")
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
		identity: identity, partitionSources: make(map[string]int64), partitionIdentity: make(map[string]semanticCoveragePartitionIdentity), partitionComplete: make(map[string]bool),
		partitionReason: make(map[string]catalog.SemanticCoverageReason), aggregates: make(map[semanticCoverageSummaryKey]*semanticCoverageSummaryAggregate),
	}
}

func (m *semanticCoverageMaterializer) addSource(source semanticCoverageStoredDocument) error {
	partition := semanticCoveragePartitionKey(source.AuthResourcePath, source.ResourceType)
	if _, exists := m.partitionSources[partition]; !exists && len(m.partitionSources) >= semanticCoverageMaximumPartitions {
		return ErrSemanticCoverageMaterializationLimit
	}
	m.partitionSources[partition]++
	m.partitionIdentity[partition] = semanticCoveragePartitionIdentity{authResourcePath: source.AuthResourcePath, resourceType: source.ResourceType}
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
	aggregate.document.CountsTruncated = aggregate.document.CountsTruncated || path.CountsTruncated
	aggregate.document.FacetsTruncated = aggregate.document.FacetsTruncated || path.FacetsTruncated
	aggregate.document.ReferencesTruncated = aggregate.document.ReferencesTruncated || path.ReferencesTruncated
	if aggregate.document.Path == nil {
		aggregate.document.Path = cloneSemanticCoverageStoredPath(path)
		aggregate.document.Path.OwnerPathWitnesses = nil
	} else {
		mergeSemanticCoverageStoredPath(aggregate.document.Path, path)
	}
	if !sourcePath.Groupable || sourcePath.ResourceBoundarySignature != "" || path.Disposition != string(catalog.SemanticCoverageSemanticDisposition) || path.CountsTruncated || path.FacetsTruncated || path.ReferencesTruncated {
		aggregate.allGroupable = false
	}
	for _, facet := range path.Facets {
		if facet.Disposition != string(catalog.SemanticCoverageSemanticDisposition) || facet.ReferencesTruncated || len(facet.References) == 0 {
			aggregate.allGroupable = false
			if facet.Disposition != string(catalog.SemanticCoverageSemanticDisposition) {
				aggregate.anyKeepRaw = true
				if aggregate.reason == "" && facet.Reason != "" {
					aggregate.reason = catalog.SemanticCoverageReason(facet.Reason)
				}
			}
		}
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
			AuthResourcePath: partition.authResourcePath, ResourceType: partition.resourceType,
			SourceRecordCount: materializer.partitionSources[partitionKey], Complete: complete, Groupable: complete,
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
		document.Key = semanticCoverageDocumentKey("semantic-coverage/v1", materializer.identity.Project, materializer.identity.DatasetGeneration, materializer.identity.BuildID, materializer.identity.SourceKind, materializer.identity.SchemaIdentity, "partition", partition.authResourcePath, partition.resourceType)
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
		(recordKind != "source" && recordKind != "path" && recordKind != "facet" && recordKind != "summary") ||
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
  FILTER @record_kind NOT IN ["path", "facet"] OR LENGTH(
    FOR source IN fhir_semantic_coverage
      FILTER source.record_kind == "source"
      FILTER source.project == d.project
      FILTER source.dataset_generation == d.dataset_generation
      FILTER source.build_id == d.build_id
      FILTER source.source_kind == d.source_kind
      FILTER source.schema_identity == d.schema_identity
      FILTER source.auth_resource_path == d.auth_resource_path
      FILTER source.resource_type == d.resource_type
      FILTER source.source_id == d.source_id
      FILTER source.complete == true
      LIMIT 1
      RETURN 1
  ) > 0
  SORT d._key
  LIMIT @limit
  RETURN d`

const deleteSemanticCoverageSourceSetAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.resource_type == @resource_type
  FILTER d.source_id == @source_id
  FILTER d.record_kind IN ["source", "path", "facet"]
  REMOVE d IN fhir_semantic_coverage`

const semanticCoverageSourceScopesAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.resource_type IN @resource_types
  FILTER d.source_id IN @source_ids
  FILTER d.record_kind IN ["source", "path", "facet"]
  COLLECT source_id = d.source_id, resource_type = d.resource_type, auth_resource_path = NOT_NULL(d.auth_resource_path, "")
  SORT source_id, resource_type, auth_resource_path
  LIMIT @limit
  RETURN {source_id: source_id, resource_type: resource_type, auth_resource_path: auth_resource_path}`

const deleteSemanticCoverageSourceSetBatchAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind IN ["source", "path", "facet"]
  FILTER LENGTH(
    FOR source IN @source_scopes
      FILTER source.source_id == d.source_id
      FILTER source.resource_type == d.resource_type
      FILTER source.auth_resource_path == NOT_NULL(d.auth_resource_path, "")
      LIMIT 1
      RETURN 1
  ) > 0
  REMOVE d IN fhir_semantic_coverage`

const deleteSemanticCoverageSummariesAQL = `
FOR d IN fhir_semantic_coverage
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.source_kind == @source_kind
  FILTER d.schema_identity == @schema_identity
  FILTER d.record_kind IN ["summary", "partition"]
  REMOVE d IN fhir_semantic_coverage`

// LookupSemanticCoverage returns exact-build summaries for a bounded set of
// candidate paths under the caller's already-authorized auth path set.
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
	var marker semanticCoverageStoredDocument
	found := false
	err := s.client.QueryRows(ctx, semanticCoverageBuildMarkerAQL, 1, semanticCoverageBuildBindVars(identity), func(row map[string]any) error {
		if err := decodeSemanticCoverageRow(row, &marker); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return SemanticCoverageLookupResult{}, fmt.Errorf("read semantic coverage build marker: %w", err)
	}
	if !found {
		result.Reason = "COVERAGE_BUILD_MISSING"
		return result, nil
	}
	result.State = marker.BuildState
	result.Reason = marker.BuildReason
	if marker.CoverageRuleVersion != catalog.SemanticCoverageRuleVersion {
		result.State = SemanticCoverageBuildUnavailable
		result.Reason = "COVERAGE_RULE_VERSION_MISMATCH"
		return result, nil
	}
	if result.State != SemanticCoverageBuildComplete {
		return result, nil
	}

	paths := append([]string(nil), options.AuthResourcePaths...)
	sort.Strings(paths)
	paths = compactAuthPaths(paths)
	resourceTypes := make([]string, 0, len(options.Candidates))
	for _, candidate := range options.Candidates {
		resourceTypes = append(resourceTypes, candidate.ResourceType)
	}
	sort.Strings(resourceTypes)
	resourceTypes = compactAuthPaths(resourceTypes)
	partitionsByResource := make(map[string][]semanticCoverageStoredDocument, len(resourceTypes))
	partitionRows := 0
	partitionErr := s.client.QueryRows(ctx, semanticCoveragePartitionLookupAQL, semanticCoverageLookupMaximumPartitions+1, map[string]interface{}{
		"project": identity.Project, "dataset_generation": identity.DatasetGeneration,
		"build_id": identity.BuildID, "source_kind": identity.SourceKind,
		"schema_identity": identity.SchemaIdentity, "auth_resource_paths": paths,
		"auth_resource_paths_unrestricted": options.AuthResourcePathsUnrestricted,
		"resource_types":                   resourceTypes, "limit": semanticCoverageLookupMaximumPartitions + 1,
	}, func(row map[string]any) error {
		partitionRows++
		if partitionRows > semanticCoverageLookupMaximumPartitions {
			return nil
		}
		var document semanticCoverageStoredDocument
		if err := decodeSemanticCoverageRow(row, &document); err != nil {
			return err
		}
		partitionsByResource[document.ResourceType] = append(partitionsByResource[document.ResourceType], document)
		return nil
	})
	if partitionErr != nil {
		return SemanticCoverageLookupResult{}, fmt.Errorf("lookup semantic coverage source partitions: %w", partitionErr)
	}
	if partitionRows > semanticCoverageLookupMaximumPartitions {
		result.Reason = "PARTITION_ROW_LIMIT"
		result.Candidates = unavailableSemanticCoverageCandidates(options.Candidates, catalog.SemanticCoverageReasonIncompleteSource)
		return result, nil
	}
	candidateBinds := make([]map[string]string, 0, len(options.Candidates))
	for _, candidate := range options.Candidates {
		candidateBinds = append(candidateBinds, map[string]string{"resource_type": candidate.ResourceType, "canonical_path": candidate.CanonicalPath})
	}
	rowsByCandidate := make(map[SemanticCoverageCandidatePath][]semanticCoverageStoredDocument, len(options.Candidates))
	rowCount := 0
	queryErr := s.client.QueryRows(ctx, semanticCoverageCandidateLookupAQL, semanticCoverageLookupMaximumRows+1, map[string]interface{}{
		"project":                          identity.Project,
		"dataset_generation":               identity.DatasetGeneration,
		"build_id":                         identity.BuildID,
		"source_kind":                      identity.SourceKind,
		"schema_identity":                  identity.SchemaIdentity,
		"auth_resource_paths":              paths,
		"auth_resource_paths_unrestricted": options.AuthResourcePathsUnrestricted,
		"candidates":                       candidateBinds,
		"limit":                            semanticCoverageLookupMaximumRows + 1,
	}, func(row map[string]any) error {
		rowCount++
		if rowCount > semanticCoverageLookupMaximumRows {
			return nil
		}
		var document semanticCoverageStoredDocument
		if err := decodeSemanticCoverageRow(row, &document); err != nil {
			return err
		}
		key := SemanticCoverageCandidatePath{ResourceType: document.ResourceType, CanonicalPath: document.CanonicalPath}
		rowsByCandidate[key] = append(rowsByCandidate[key], document)
		return nil
	})
	if queryErr != nil {
		return SemanticCoverageLookupResult{}, fmt.Errorf("lookup semantic coverage candidates: %w", queryErr)
	}
	if rowCount > semanticCoverageLookupMaximumRows {
		result.Reason = "LOOKUP_ROW_LIMIT"
		return result, nil
	}
	result.Candidates = make([]SemanticCoverageCandidateResult, 0, len(options.Candidates))
	for _, candidate := range options.Candidates {
		partitions := partitionsByResource[candidate.ResourceType]
		if len(partitions) == 0 {
			result.Candidates = append(result.Candidates, unavailableSemanticCoverageCandidate(candidate, catalog.SemanticCoverageReasonIncompleteSource))
			continue
		}
		partitionIncomplete := false
		partitionReason := catalog.SemanticCoverageReasonIncompleteSource
		for _, partition := range partitions {
			if !partition.Complete || partition.Status != SemanticCoverageCandidateGroupable || !partition.Groupable {
				partitionIncomplete = true
				if partition.Reason != "" {
					partitionReason = catalog.SemanticCoverageReason(partition.Reason)
				}
				break
			}
		}
		if partitionIncomplete {
			result.Candidates = append(result.Candidates, unavailableSemanticCoverageCandidate(candidate, partitionReason))
			continue
		}
		rows := rowsByCandidate[candidate]
		if len(rows) == 0 {
			result.Candidates = append(result.Candidates, SemanticCoverageCandidateResult{
				ResourceType: candidate.ResourceType, CanonicalPath: candidate.CanonicalPath,
				CoverageID: semanticCoverageDocumentKey("semantic-coverage-absent/v1", identity.Project, identity.DatasetGeneration, identity.BuildID, identity.SourceKind, identity.SchemaIdentity, candidate.ResourceType, candidate.CanonicalPath),
				Status:     SemanticCoverageCandidateKeepRaw, Reason: catalog.SemanticCoverageReasonUnrepresentedMetadata,
			})
			continue
		}
		candidateResult := SemanticCoverageCandidateResult{
			ResourceType: candidate.ResourceType, CanonicalPath: candidate.CanonicalPath,
			Status: SemanticCoverageCandidateGroupable, References: []catalog.SemanticCoverageReference{},
		}
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
