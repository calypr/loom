package arango

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/catalog"
)

const (
	SemanticCoverageEnvelopeFormatVersion = 1
	semanticCoverageEnvelopeRecordKind    = "source_envelope_v1"
)

// semanticCoverageSourceEnvelope is the sole persisted source-level coverage
// document. The key names the physical source locator; auth scope is mutable
// source data so replay can replace it atomically.
type semanticCoverageSourceEnvelope struct {
	Key                          string                               `json:"_key"`
	RecordKind                   string                               `json:"record_kind"`
	CoverageStorageFormatVersion int                                  `json:"coverage_storage_format_version"`
	Project                      string                               `json:"project"`
	DatasetGeneration            string                               `json:"dataset_generation"`
	BuildID                      string                               `json:"build_id"`
	SchemaIdentity               string                               `json:"schema_identity"`
	SourceKind                   string                               `json:"source_kind"`
	ResourceType                 string                               `json:"resource_type"`
	SourceID                     string                               `json:"source_id"`
	AuthResourcePath             string                               `json:"auth_resource_path"`
	SourceRecordCount            int64                                `json:"source_record_count"`
	Counts                       map[string]int64                     `json:"counts,omitempty"`
	Paths                        []semanticCoverageEnvelopePath       `json:"paths,omitempty"`
	Diagnostics                  []semanticCoverageEnvelopeDiagnostic `json:"diagnostics,omitempty"`
	IncompleteGaps               []string                             `json:"incomplete_gaps,omitempty"`
	Complete                     bool                                 `json:"complete"`
	CountsTruncated              bool                                 `json:"counts_truncated,omitempty"`
	FacetsTruncated              bool                                 `json:"facets_truncated,omitempty"`
	ReferencesTruncated          bool                                 `json:"references_truncated,omitempty"`
	ReconciliationTruncated      bool                                 `json:"reconciliation_truncated,omitempty"`
	DiagnosticLocatorsTruncated  bool                                 `json:"diagnostic_locators_truncated,omitempty"`
}

type semanticCoverageEnvelopePath struct {
	CanonicalPath string                      `json:"canonical_path"`
	Groupable     bool                        `json:"groupable"`
	Path          *semanticCoverageStoredPath `json:"path"`
}

// Explanations are intentionally omitted from persisted diagnostics. They are
// presentation text and must not become a channel for source values.
type semanticCoverageEnvelopeDiagnostic struct {
	Code          string `json:"code"`
	CanonicalPath string `json:"canonical_path,omitempty"`
	OwnerPath     string `json:"owner_path,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// EncodeSemanticCoverageSourceEnvelope returns one bounded JSON document for
// a single source contribution. It persists only aggregated metadata, paths,
// facets, references, and diagnostics; ephemeral emission evidence and source
// values are not part of the DTO.
func EncodeSemanticCoverageSourceEnvelope(buildID string, contribution catalog.SemanticCoverageSourceContribution) (json.RawMessage, error) {
	if err := validateSemanticCoverageContribution(buildID, contribution); err != nil {
		return nil, err
	}
	identity := contribution.Identity
	document := semanticCoverageSourceEnvelope{
		Key:                          semanticCoverageSourceEnvelopeKey(identity, buildID),
		RecordKind:                   semanticCoverageEnvelopeRecordKind,
		CoverageStorageFormatVersion: SemanticCoverageEnvelopeFormatVersion,
		Project:                      identity.Project,
		DatasetGeneration:            catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		BuildID:                      buildID,
		SchemaIdentity:               identity.SchemaIdentity,
		SourceKind:                   identity.SourceKind,
		ResourceType:                 identity.ResourceType,
		SourceID:                     identity.SourceID,
		AuthResourcePath:             identity.AuthResourcePath,
		SourceRecordCount:            contribution.SourceRecordCount,
		Counts:                       semanticCoverageCountsDocument(contribution.Counts),
		Paths:                        make([]semanticCoverageEnvelopePath, 0, len(contribution.Paths)),
		IncompleteGaps:               semanticCoverageGapStrings(contribution.IncompleteGaps),
		Complete:                     contribution.Complete,
		CountsTruncated:              contribution.CountsTruncated,
		FacetsTruncated:              contribution.FacetsTruncated,
		ReferencesTruncated:          contribution.ReferencesTruncated,
		ReconciliationTruncated:      contribution.ReconciliationTruncated,
		DiagnosticLocatorsTruncated:  contribution.DiagnosticLocatorsTruncated,
	}

	paths := append([]catalog.SemanticCoveragePathAggregate(nil), contribution.Paths...)
	sort.Slice(paths, func(i, j int) bool {
		return semanticCoverageEnvelopeAggregateKey(paths[i]) < semanticCoverageEnvelopeAggregateKey(paths[j])
	})
	seenPaths := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if err := validateSemanticCoveragePath(path); err != nil {
			return nil, err
		}
		pathKey := semanticCoverageEnvelopeAggregateKey(path)
		if _, exists := seenPaths[pathKey]; exists {
			return nil, fmt.Errorf("duplicate semantic coverage path aggregate %q", path.CanonicalPath)
		}
		seenPaths[pathKey] = struct{}{}
		storedPath := semanticCoveragePathDocument(path)
		storedPath.OwnerPathWitnesses = nil
		storedPath.WitnessesTruncated = false
		document.Paths = append(document.Paths, semanticCoverageEnvelopePath{
			CanonicalPath: path.CanonicalPath,
			Groupable:     coverageEnvelopePathGroupable(contribution, path),
			Path:          storedPath,
		})
	}

	for _, diagnostic := range contribution.Diagnostics {
		document.Diagnostics = append(document.Diagnostics, semanticCoverageEnvelopeDiagnostic{
			Code:          diagnostic.Code,
			CanonicalPath: diagnostic.CanonicalPath,
			OwnerPath:     diagnostic.OwnerPath,
			Reason:        string(diagnostic.Reason),
		})
	}
	sort.Slice(document.Diagnostics, func(i, j int) bool {
		left, right := document.Diagnostics[i], document.Diagnostics[j]
		if left.CanonicalPath != right.CanonicalPath {
			return left.CanonicalPath < right.CanonicalPath
		}
		if left.OwnerPath != right.OwnerPath {
			return left.OwnerPath < right.OwnerPath
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Reason < right.Reason
	})

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode semantic coverage source envelope: %w", err)
	}
	if err := validateSemanticCoverageDocument(encoded); err != nil {
		return nil, fmt.Errorf("validate semantic coverage source envelope: %w", err)
	}
	return encoded, nil
}

func semanticCoverageEnvelopeAggregateKey(path catalog.SemanticCoveragePathAggregate) string {
	return semanticCoverageDocumentKey(path.CanonicalPath, path.ResourceBoundarySignature, string(path.Kind), string(path.Disposition), string(path.Reason))
}

func semanticCoverageSourceEnvelopeKey(identity catalog.SemanticCoverageSourceIdentity, buildID string) string {
	return semanticCoverageDocumentKey(
		"semantic-coverage-source-envelope/v1",
		identity.Project,
		catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		buildID,
		identity.SchemaIdentity,
		identity.SourceKind,
		identity.ResourceType,
		identity.SourceID,
	)
}

func coverageEnvelopePathGroupable(contribution catalog.SemanticCoverageSourceContribution, path catalog.SemanticCoveragePathAggregate) bool {
	return contribution.Complete && contribution.Identity.SchemaIdentity != "" &&
		!contribution.CountsTruncated && !contribution.FacetsTruncated && !contribution.ReferencesTruncated &&
		!contribution.ReconciliationTruncated && len(contribution.IncompleteGaps) == 0 &&
		path.ResourceBoundarySignature == "" && !path.CountsTruncated && !path.FacetsTruncated &&
		!path.ReferencesTruncated && path.Disposition == catalog.SemanticCoverageSemanticDisposition
}
