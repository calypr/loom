package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRelatedCategoryReverseLookupPreservesCrossCollectionResourceTypeMatchAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_related_category_collection_identity_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation-related-category-collection-identity"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Specimen", "Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-category collection-identity fixture from %s: %v", collection, err)
			}
		}
	}()

	key := func(id string) string { return strings.ReplaceAll(project, "-", "") + "_" + id }
	insertDocument := func(collection, resourceType, id string, category any) string {
		t.Helper()
		payload := map[string]any{"id": id, "resourceType": resourceType}
		if category != nil {
			payload["valueQuantity"] = map[string]any{"code": category}
		}
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "auth_resource_path": "/all",
			"resourceType": resourceType, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, collection, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert %s fixture %s claiming resourceType %s: %v", collection, id, resourceType, err)
		}
		return key(id)
	}
	insertEdge := func(id, fromCollection, fromKey, toCollection, toKey, fromType, toType string) {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key("edge_" + id), "_from": fromCollection + "/" + fromKey,
			"_to": toCollection + "/" + toKey, "project": project, "project_id": project,
			"dataset_generation": generation, "auth_resource_path": "/all",
			"label": "subject_Patient", "from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related-category edge %s: %v", id, err)
		}
	}

	canonicalRoot := insertDocument("Specimen", "Specimen", "root_canonical", nil)
	canonicalPatient := insertDocument("Patient", "Patient", "patient_canonical", nil)
	canonicalObservation := insertDocument("Observation", "Observation", "observation_canonical", "canonical-category")
	insertEdge("canonical_root_patient", "Specimen", canonicalRoot, "Patient", canonicalPatient, "Specimen", "Patient")
	insertEdge("canonical_observation_patient", "Observation", canonicalObservation, "Patient", canonicalPatient, "Observation", "Patient")

	// Keep a scoped cross-collection endpoint claiming Patient in the fixture:
	// current lookup semantics accept it because its declared type and scope
	// match the route, regardless of its actual collection.
	foreignRoot := insertDocument("Specimen", "Specimen", "root_foreign_collection", nil)
	foreignPatient := insertDocument("Observation", "Patient", "patient_stored_as_observation", nil)
	foreignObservation := insertDocument("Observation", "Observation", "observation_foreign_collection", "cross-collection-category")
	insertEdge("foreign_root_patient", "Specimen", foreignRoot, "Observation", foreignPatient, "Specimen", "Patient")
	insertEdge("foreign_observation_patient", "Observation", foreignObservation, "Observation", foreignPatient, "Observation", "Patient")

	missingTargetObservation := insertDocument("Observation", "Observation", "observation_missing_target", "missing-target-category")
	insertEdge("missing_target", "Observation", missingTargetObservation, "Patient", key("patient_missing"), "Observation", "Patient")

	_, _, scanned := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), project, generation, MaxCategoryScanValues)
	rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: scanned.Query, BindVars: scanned.BindVars})
	got := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			t.Fatalf("category presence = %#v, want bool", row[scanned.PresentColumn])
		}
		got[relatedCategoryValueKey(present, row[scanned.ValueColumn])] = struct{}{}
	}
	want := map[string]struct{}{
		relatedCategoryValueKey(true, "canonical-category"):        {},
		relatedCategoryValueKey(true, "cross-collection-category"): {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("related categories = %v, want canonical plus the scoped cross-collection Patient claim and no missing-target category %v; query:\n%s", got, want, scanned.Query)
	}
}
