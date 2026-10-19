package compiler

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRootPageRoutedExistsKeepsEligibleRootsAcrossCursorAgainstArango(t *testing.T) {
	project := "loom_root_page_routed_exists_" + uuid.NewString()
	const generation = "generation_root_page_routed_exists"
	output := rootAndRelatedPageFilterOutput(false)
	output.Construction.Steps = []recipe.ConstructionStep{
		pageFilterStep("require_patient", "", "patient-id", recipe.FilterExists, "", output.Construction.SourceColumns),
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "routed-root-exists-page", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build routed root-page recipe: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve routed root-page recipe: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower routed root-page recipe: %v", err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed EXISTS root page: %v", err)
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Observation"}, {Name: "Patient"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatalf("bootstrap routed root-page fixture collections: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, query := range map[string]string{
			"Observation": "FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			"Patient":     "FOR document IN Patient FILTER document.project == @project REMOVE document IN Patient",
			"fhir_edge":   "FOR document IN fhir_edge FILTER document.project == @project REMOVE document IN fhir_edge",
		} {
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove routed root-page fixture documents from %s: %v", collection, err)
			}
		}
	})

	observations := make([]json.RawMessage, 0, 4)
	patients := make([]json.RawMessage, 0, 2)
	edges := make([]json.RawMessage, 0, 2)
	addObservation := func(suffix, id string) string {
		t.Helper()
		key := project + "_observation_" + suffix
		document, err := json.Marshal(map[string]any{
			"_key": key, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation",
			"payload": map[string]any{"id": id, "resourceType": "Observation"},
		})
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, document)
		return key
	}
	addPatientEdge := func(suffix, observationKey, patientID string) {
		t.Helper()
		patientKey := project + "_patient_" + suffix
		patient, err := json.Marshal(map[string]any{
			"_key": patientKey, "id": patientID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient",
			"payload": map[string]any{"id": patientID, "resourceType": "Patient"},
		})
		if err != nil {
			t.Fatal(err)
		}
		patients = append(patients, patient)
		edge, err := json.Marshal(map[string]any{
			"_key":  project + "_edge_" + suffix,
			"_from": "Observation/" + observationKey, "_to": "Patient/" + patientKey,
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, edge)
	}

	addObservation("001_without_patient", "obs-without-patient")
	firstMatchKey := addObservation("002_first_match", "obs-first-match")
	addPatientEdge("first", firstMatchKey, "patient-first")
	addObservation("003_without_patient", "obs-without-patient-between")
	secondMatchKey := addObservation("004_second_match", "obs-second-match")
	addPatientEdge("second", secondMatchKey, "patient-second")

	for collection, documents := range map[string][]json.RawMessage{
		"Observation": observations, "Patient": patients, "fhir_edge": edges,
	} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert routed root-page fixtures into %s: %v", collection, err)
		}
	}

	type readResult struct {
		keys []string
		rows []map[string]any
	}
	readPage := func(after string) readResult {
		t.Helper()
		keyBinds := make(map[string]any, len(page.RootKeysBindVars))
		for key, value := range page.RootKeysBindVars {
			keyBinds[key] = value
		}
		keyBinds[RootPageAfterKeyBind] = after
		result := readResult{keys: make([]string, 0, 1)}
		if err := client.QueryRows(ctx, page.RootKeysQuery, 10, keyBinds, func(row map[string]any) error {
			key, ok := row["_key"].(string)
			if !ok {
				t.Fatalf("routed EXISTS root-key query returned invalid _key %#v", row["_key"])
			}
			result.keys = append(result.keys, key)
			return nil
		}); err != nil {
			t.Fatalf("execute routed EXISTS root-key query after %q:\n%s\n%v", after, page.RootKeysQuery, err)
		}
		if len(result.keys) > 1 {
			t.Fatalf("routed EXISTS root-key page after %q returned %#v, want at most one key", after, result.keys)
		}
		if len(result.keys) == 0 {
			return result
		}

		rowBinds := make(map[string]any, len(page.RowsBindVars)+1)
		for key, value := range page.RowsBindVars {
			rowBinds[key] = value
		}
		rowBinds[RootPageKeysBind] = result.keys
		result.rows = make([]map[string]any, 0, 1)
		if err := client.QueryRows(ctx, page.RowsQuery, 10, rowBinds, func(row map[string]any) error {
			result.rows = append(result.rows, row)
			return nil
		}); err != nil {
			t.Fatalf("execute routed EXISTS selected-root query after %q:\n%s\n%v", after, page.RowsQuery, err)
		}
		return result
	}

	firstPage := readPage("")
	if !reflect.DeepEqual(firstPage.keys, []string{firstMatchKey}) {
		t.Fatalf("first routed EXISTS root-key page = %#v, want only %q\n%s", firstPage.keys, firstMatchKey, page.RootKeysQuery)
	}
	secondPage := readPage(firstPage.keys[0])
	if !reflect.DeepEqual(secondPage.keys, []string{secondMatchKey}) {
		t.Fatalf("next routed EXISTS root-key page = %#v, want only %q\n%s", secondPage.keys, secondMatchKey, page.RootKeysQuery)
	}
	thirdPage := readPage(secondPage.keys[0])
	if len(thirdPage.keys) != 0 || len(thirdPage.rows) != 0 {
		t.Fatalf("routed EXISTS root-key page after final match = keys %#v rows %#v, want empty", thirdPage.keys, thirdPage.rows)
	}

	pagedRows := append(append([]map[string]any(nil), firstPage.rows...), secondPage.rows...)
	gotIDs := make([]string, 0, len(pagedRows))
	for _, row := range pagedRows {
		id, ok := row["root_id"].(string)
		if !ok {
			t.Fatalf("routed EXISTS selected row has invalid root_id %#v: %#v", row["root_id"], row)
		}
		gotIDs = append(gotIDs, id)
	}
	wantIDs := []string{"obs-first-match", "obs-second-match"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("paged routed EXISTS output IDs = %#v, want exact ID multiset %#v", gotIDs, wantIDs)
	}
}
