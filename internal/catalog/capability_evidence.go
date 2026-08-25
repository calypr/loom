package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// newEvidenceStatus centralizes the empty/partial distinction. A successful
// query with no rows is valid empty evidence; a failed query is unavailable,
// and a bounded profiler result is incomplete/truncated.
func newEvidenceStatus(available, complete, truncated bool, count int) EvidenceStatus {
	if !available {
		return EvidenceUnavailable
	}
	if truncated {
		return EvidenceTruncated
	}
	if !complete {
		return EvidenceIncomplete
	}
	if count == 0 {
		return EvidenceEmpty
	}
	return EvidenceAvailable
}

func unavailableInventory(diagnostics []EvidenceDiagnostic) ResourceInventoryResult {
	return ResourceInventoryResult{Values: []ResourceInventoryObservation{}, Status: EvidenceUnavailable, Diagnostics: append([]EvidenceDiagnostic(nil), diagnostics...)}
}

func unavailableRelationships(diagnostics []EvidenceDiagnostic) RelationshipObservationResult {
	return RelationshipObservationResult{Values: []RelationshipObservation{}, Status: EvidenceUnavailable, Diagnostics: append([]EvidenceDiagnostic(nil), diagnostics...)}
}

func unavailableEnrichment(diagnostics []EvidenceDiagnostic) FieldEnrichmentResult {
	return FieldEnrichmentResult{Values: []FieldEnrichmentObservation{}, Status: EvidenceUnavailable, Diagnostics: append([]EvidenceDiagnostic(nil), diagnostics...)}
}

func canonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ResourceInventoryDigest(records []ResourceInventoryObservation) (string, error) {
	canonical := append([]ResourceInventoryObservation(nil), records...)
	sort.SliceStable(canonical, func(i, j int) bool {
		return inventoryLess(canonical[i], canonical[j])
	})
	return canonicalDigest(canonical)
}

func inventoryLess(a, b ResourceInventoryObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocumentCount < b.DocumentCount
}

func RelationshipObservationDigest(records []RelationshipObservation) (string, error) {
	canonical := append([]RelationshipObservation(nil), records...)
	sort.SliceStable(canonical, func(i, j int) bool {
		return relationshipObservationLess(canonical[i], canonical[j])
	})
	return canonicalDigest(canonical)
}

func relationshipObservationLess(a, b RelationshipObservation) bool {
	left := [12]string{a.Project, a.DatasetGeneration, a.AuthResourcePath, a.StorageFromType, a.Label, a.StorageToType, a.StorageDirection, a.BuilderFromType, a.BuilderToType, a.BuilderDirection, a.FromType, a.ToType}
	right := [12]string{b.Project, b.DatasetGeneration, b.AuthResourcePath, b.StorageFromType, b.Label, b.StorageToType, b.StorageDirection, b.BuilderFromType, b.BuilderToType, b.BuilderDirection, b.FromType, b.ToType}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return a.EdgeCount < b.EdgeCount
}

func FieldEnrichmentDigest(records []FieldEnrichmentObservation) (string, error) {
	canonical := cloneFieldEnrichment(records)
	sort.SliceStable(canonical, func(i, j int) bool {
		a, b := canonical[i], canonical[j]
		for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}, {a.Path, b.Path}, {a.Kind, b.Kind}} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return a.DocCount < b.DocCount
	})
	return canonicalDigest(canonical)
}

func cloneFieldEnrichment(in []FieldEnrichmentObservation) []FieldEnrichmentObservation {
	if len(in) == 0 {
		return []FieldEnrichmentObservation{}
	}
	out := make([]FieldEnrichmentObservation, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].DistinctValues = append([]string(nil), in[i].DistinctValues...)
		out[i].PivotColumns = append([]string(nil), in[i].PivotColumns...)
		out[i].PivotValueSelectors = append([]string(nil), in[i].PivotValueSelectors...)
		out[i].ExtensionValues = append([]ExtensionValueObservation(nil), in[i].ExtensionValues...)
		for j := range out[i].ExtensionValues {
			out[i].ExtensionValues[j].URLPath = append([]string(nil), in[i].ExtensionValues[j].URLPath...)
		}
	}
	return out
}

// ReadCapabilityEvidence keeps the three evidence sources independent while
// providing a convenient aggregate for snapshot compilers. It returns the
// first dependency error but still returns all successfully read evidence.
func ReadCapabilityEvidence(ctx context.Context, reader CapabilityEvidenceReader, opts CapabilityEvidenceOptions) (CapabilityEvidence, error) {
	var result CapabilityEvidence
	var firstErr error
	inventory, err := reader.DiscoverResourceInventory(ctx, opts)
	result.ResourceInventory = inventory
	if err != nil {
		firstErr = err
	}
	relationships, err := reader.DiscoverRelationshipObservations(ctx, opts)
	result.Relationships = relationships
	if err != nil {
		if firstErr == nil {
			firstErr = err
		}
	}
	enrichment, err := reader.DiscoverFieldEnrichment(ctx, opts)
	result.FieldEnrichment = enrichment
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return result, firstErr
}
