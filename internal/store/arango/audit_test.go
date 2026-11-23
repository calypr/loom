package arango

import (
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

func TestPreviewSupersedesAuditClassifiesOnlyExactCompilerTargets(t *testing.T) {
	legacyFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	legacy := previewIndexResponse(previewCoveringIndexName("Observation", legacyFields), "Observation/old", legacyFields)
	quantityFields := append([]string(nil), previewQuantityCategoryLegacyFields...)
	quantity := previewIndexResponse(
		previewCoveringIndexNameWithStoredValues("Observation", quantityFields, previewQuantityCategoryStoredValues),
		"Observation/quantity-old", quantityFields)
	quantity.RegularIndex.StoredValues = append([]string(nil), previewQuantityCategoryStoredValues...)
	unknown := previewIndexResponse("loom_pivot_preview_bad_hash", "Observation/unclassified", legacyFields)
	wrongType := previewIndexResponse(legacy.Name, "Observation/hash", legacyFields)
	wrongType.Type = driver.IndexType("hash")
	sparse := previewIndexResponse("loom_pivot_preview_sparse", "Observation/sparse", legacyFields)
	sparse.Sparse = boolPointer(true)

	other, candidates, count := auditPreviewIndexCollection("Observation", []driver.IndexResponse{legacy, quantity, unknown, wrongType, sparse})
	if count != 5 || len(candidates) != 2 || len(other) != 3 {
		t.Fatalf("audit count=%d candidates=%+v other=%+v", count, candidates, other)
	}
	if candidates[0].Kind != "category-field-only" && candidates[1].Kind != "category-field-only" {
		t.Fatalf("category replacement not classified: %+v", candidates)
	}
	if candidates[0].Kind != "observation-quantity-stored-values" && candidates[1].Kind != "observation-quantity-stored-values" {
		t.Fatalf("stored-value replacement not classified: %+v", candidates)
	}
}

func TestPreviewSupersedesAuditDerivesKnownIndexNames(t *testing.T) {
	legacy := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	candidate, ok := previewSupersededCandidate("Observation", previewIndexResponse(
		"loom_pivot_preview_e358392c38a61a57", "Observation/e358", legacy))
	if !ok || candidate.NewName != "loom_pivot_preview_601548896a95cbf8" {
		t.Fatalf("known compiler replacement = %+v, ok=%v", candidate, ok)
	}
}

func TestPreviewSupersedesAuditReportsExactReplacementPresent(t *testing.T) {
	oldFields := []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}
	oldName := previewCoveringIndexName("Patient", oldFields)
	newFields := []string{"project", "dataset_generation", "resourceType", "payload.status", "auth_resource_path"}
	newName := previewCoveringIndexName("Patient", newFields)
	old := previewIndexResponse(oldName, "Patient/old", oldFields)
	current := previewIndexResponse(newName, "Patient/current", newFields)
	other, candidates, count := auditPreviewIndexCollection("Patient", []driver.IndexResponse{old, current})
	if count != 2 || len(candidates) != 1 || candidates[0].ReplacementID != current.ID || !candidates[0].ReplacementExists {
		t.Fatalf("replacement audit: count=%d candidates=%+v", count, candidates)
	}
	if len(other) != 0 {
		t.Fatalf("recognized definitions reported as unclassified: %+v", other)
	}
}

func TestPreviewIndexInventoryComparesFlagValues(t *testing.T) {
	fields := []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}
	name := previewCoveringIndexName("Observation", fields)
	before := previewIndexResponse(name, "Observation/same", fields)
	after := previewIndexResponse(name, "Observation/same", append([]string(nil), fields...))
	if !samePreviewIndexesExcept([]driver.IndexResponse{before}, []driver.IndexResponse{after}) {
		t.Fatal("equal flags decoded into distinct pointers changed the inventory")
	}
	after.Unique = boolPointer(true)
	if samePreviewIndexesExcept([]driver.IndexResponse{before}, []driver.IndexResponse{after}) {
		t.Fatal("changed unique flag was ignored")
	}
	after.Unique = boolPointer(false)
	after.Sparse = nil
	if samePreviewIndexesExcept([]driver.IndexResponse{before}, []driver.IndexResponse{after}) {
		t.Fatal("missing sparse flag was treated as an explicit false flag")
	}
}
