package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
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

func TestRootPageRelatedSeedConstructionFilterPlacement(t *testing.T) {
	const project, generation, targetID = "root-page-related-seed-project", "root-page-related-seed-generation", "patient-match"
	bindings := rootPageRelatedSeedBindings(project, generation)
	compiled := compileRootPageRelatedSeedOutput(t, rootPageRelatedSeedOutput(targetID), bindings)
	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related root page: %v", err)
	}

	query := page.RootKeysQuery
	unique := strings.Index(query, "LET child_set_1 = UNIQUE((")
	childSort := strings.Index(query, "SORT child_set_1_node._key")
	first := strings.Index(query, "FIRST(FLATTEN(child_set_1[*].__loom_projection_0))")
	filterBind := strings.LastIndex(query, "@construction_filter_value")
	filter := -1
	if filterBind >= 0 {
		filter = strings.LastIndex(query[:filterBind], "FILTER ")
	}
	rootSort := strings.Index(query, "SORT root._key ASC")
	limit := strings.Index(query, "LIMIT @limit")
	if unique < 0 || childSort < 0 || first < 0 || filter < 0 || filterBind < 0 || rootSort < 0 || limit < 0 ||
		unique > childSort || childSort > first || first > filter || filter > filterBind || filterBind > rootSort || rootSort > limit {
		t.Fatalf("related construction filter must follow UNIQUE, sorted child FIRST, and precede the root-key LIMIT: UNIQUE=%d child-sort=%d FIRST=%d FILTER=%d bind=%d root-sort=%d LIMIT=%d\n%s", unique, childSort, first, filter, filterBind, rootSort, limit, query)
	}
	if got := page.RootKeysBindVars["construction_filter_value"]; got != targetID {
		t.Fatalf("related root-key construction filter bind = %#v, want %q", got, targetID)
	}
	if got := page.RootKeysBindVars["project"]; got != project {
		t.Fatalf("related root-key project bind = %#v, want %q", got, project)
	}
	if got := page.RootKeysBindVars["dataset_generation"]; got != generation {
		t.Fatalf("related root-key generation bind = %#v, want %q", got, generation)
	}
	if got := page.RootKeysBindVars["auth_resource_paths_unrestricted"]; got != false {
		t.Fatalf("related root-key auth bypass bind = %#v, want false", got)
	}
	if paths, ok := page.RootKeysBindVars["auth_resource_paths"].([]string); !ok || !reflect.DeepEqual(paths, []string{"/allowed"}) {
		t.Fatalf("related root-key auth paths = %#v, want [/allowed]", page.RootKeysBindVars["auth_resource_paths"])
	}
}

func TestRootPageRelatedSeedFalsePositiveDoesNotConsumePageAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Observation"}, {Name: "Patient"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatalf("bootstrap related root-page fixture collections: %v", err)
	}

	project := "loom_root_page_related_seed_" + uuid.NewString()
	otherProject := project + "_foreign"
	const generation = "generation_root_page_related_seed"
	const staleGeneration = generation + "_stale"
	const targetID = "patient-match"

	type fixtureDocument struct {
		collection string
		value      map[string]any
	}
	fixtures := make([]fixtureDocument, 0, 48)
	ownedKeys := map[string][]string{"Observation": {}, "Patient": {}, "fhir_edge": {}}
	add := func(collection string, document map[string]any) {
		t.Helper()
		key, ok := document["_key"].(string)
		if !ok || key == "" {
			t.Fatalf("%s fixture is missing _key: %#v", collection, document)
		}
		ownedKeys[collection] = append(ownedKeys[collection], key)
		fixtures = append(fixtures, fixtureDocument{collection: collection, value: document})
	}
	resource := func(collection, key, id, docProject, docGeneration, authPath string, payloadID any, hasPayloadID bool) map[string]any {
		payload := map[string]any{"resourceType": collection}
		if hasPayloadID {
			payload["id"] = payloadID
		}
		document := map[string]any{
			"_key": key, "project": docProject, "project_id": docProject,
			"dataset_generation": docGeneration, "resourceType": collection,
			"auth_resource_path": authPath, "payload": payload,
		}
		if id != "" {
			document["id"] = id
		}
		return document
	}
	addPatient := func(suffix, id, docProject, docGeneration, authPath string, payloadID any, hasPayloadID bool) string {
		key := project + "_patient_" + suffix
		add("Patient", resource("Patient", key, id, docProject, docGeneration, authPath, payloadID, hasPayloadID))
		return key
	}
	addObservation := func(suffix, id, docProject, docGeneration, authPath string) string {
		key := project + "_observation_" + suffix
		add("Observation", resource("Observation", key, id, docProject, docGeneration, authPath, id, true))
		return key
	}
	addEdge := func(suffix, rootKey, patientKey, docProject, docGeneration, authPath string) {
		add("fhir_edge", map[string]any{
			"_key":  project + "_edge_" + suffix,
			"_from": "Observation/" + rootKey, "_to": "Patient/" + patientKey,
			"project": docProject, "project_id": docProject, "dataset_generation": docGeneration,
			"auth_resource_path": authPath, "label": "subject_Patient",
			"from_type": "Observation", "to_type": "Patient",
		})
	}

	nonmatchingFirst := addPatient("a_nonmatch", "patient-other", project, generation, "/allowed", "patient-other", true)
	target := addPatient("m_target", targetID, project, generation, "/allowed", targetID, true)
	nonmatchingLater := addPatient("z_nonmatch", "patient-later", project, generation, "/allowed", "patient-later", true)
	deniedTarget := addPatient("denied_target", targetID, project, generation, "/denied", targetID, true)
	staleTarget := addPatient("stale_target", targetID, project, staleGeneration, "/allowed", targetID, true)
	foreignTarget := addPatient("foreign_target", targetID, otherProject, generation, "/allowed", targetID, true)

	falsePositiveRoot := addObservation("001_seed_false_positive", "seed-false-positive", project, generation, "/allowed")
	firstEligibleRoot := addObservation("010_eligible_first", "eligible-first", project, generation, "/allowed")
	secondEligibleRoot := addObservation("011_eligible_next", "eligible-next", project, generation, "/allowed")
	_ = addObservation("012_no_related", "no-related", project, generation, "/allowed")
	deniedRoot := addObservation("020_denied_root", "denied-root", project, generation, "/denied")
	staleRoot := addObservation("021_stale_root", "stale-root", project, staleGeneration, "/allowed")
	foreignRoot := addObservation("022_foreign_root", "foreign-root", otherProject, generation, "/allowed")
	deniedEdgeRoot := addObservation("023_denied_edge", "denied-edge", project, generation, "/allowed")
	staleEdgeRoot := addObservation("024_stale_edge", "stale-edge", project, generation, "/allowed")
	foreignEdgeRoot := addObservation("025_foreign_edge", "foreign-edge", project, generation, "/allowed")
	deniedTargetRoot := addObservation("026_denied_target", "denied-target", project, generation, "/allowed")
	staleTargetRoot := addObservation("027_stale_target", "stale-target", project, generation, "/allowed")
	foreignTargetRoot := addObservation("028_foreign_target", "foreign-target", project, generation, "/allowed")

	// The first root is a seed-only false positive: its later related Patient
	// matches, but its sorted-first Patient does not.
	addEdge("false_first_nonmatch", falsePositiveRoot, nonmatchingFirst, project, generation, "/allowed")
	addEdge("false_later_match", falsePositiveRoot, target, project, generation, "/allowed")
	addEdge("first_target", firstEligibleRoot, target, project, generation, "/allowed")
	addEdge("first_target_duplicate", firstEligibleRoot, target, project, generation, "/allowed")
	addEdge("first_later_nonmatch", firstEligibleRoot, nonmatchingLater, project, generation, "/allowed")
	addEdge("next_target", secondEligibleRoot, target, project, generation, "/allowed")
	addEdge("denied_root", deniedRoot, target, project, generation, "/allowed")
	addEdge("stale_root", staleRoot, target, project, generation, "/allowed")
	addEdge("foreign_root", foreignRoot, target, project, generation, "/allowed")
	addEdge("denied_edge", deniedEdgeRoot, target, project, generation, "/denied")
	addEdge("stale_edge", staleEdgeRoot, target, project, staleGeneration, "/allowed")
	addEdge("foreign_edge", foreignEdgeRoot, target, otherProject, generation, "/allowed")
	addEdge("denied_target", deniedTargetRoot, deniedTarget, project, generation, "/allowed")
	addEdge("stale_target", staleTargetRoot, staleTarget, project, generation, "/allowed")
	addEdge("foreign_target", foreignTargetRoot, foreignTarget, project, generation, "/allowed")

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Observation", "Patient", "fhir_edge"} {
			keys := ownedKeys[collection]
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR document IN %s FILTER document._key IN @keys REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove related root-page fixtures from %s: %v", collection, err)
			}
		}
	})
	for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
		documents := make([]json.RawMessage, 0)
		for _, fixture := range fixtures {
			if fixture.collection != collection {
				continue
			}
			encoded, err := json.Marshal(fixture.value)
			if err != nil {
				t.Fatalf("marshal %s related root-page fixture: %v", collection, err)
			}
			documents = append(documents, encoded)
		}
		if len(documents) == 0 {
			continue
		}
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert %s related root-page fixtures: %v", collection, err)
		}
	}

	bindings := rootPageRelatedSeedBindings(project, generation)
	compiled := compileRootPageRelatedSeedOutput(t, rootPageRelatedSeedOutput(targetID), bindings)
	full, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile full related construction output: %v", err)
	}
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) != 2 {
		t.Fatalf("unpaged related construction rows = %#v, want exactly the two sorted-first eligible roots", fullRows)
	}
	fullByRoot := rootPageRelatedSeedRowsByRoot(t, fullRows)
	wantObservationIDs := []string{"eligible-first", "eligible-next"}
	if !reflect.DeepEqual(rootPageRelatedSeedSortedKeys(fullByRoot), wantObservationIDs) {
		t.Fatalf("unpaged related construction root IDs = %#v, want %#v", rootPageRelatedSeedSortedKeys(fullByRoot), wantObservationIDs)
	}
	fullSourceIDByRoot := make(map[string]string, len(fullRows))
	for rootID, row := range fullByRoot {
		if row["patient_id"] != targetID {
			t.Errorf("unpaged row for %q selected patient %#v, want sorted-first %q", rootID, row["patient_id"], targetID)
		}
		sourceID, ok := row[ir.PreviewSourceResourceIDColumn].(string)
		if !ok || sourceID != rootID {
			t.Fatalf("unpaged row for %q has source identity %#v, want stored Observation ID %q", rootID, row[ir.PreviewSourceResourceIDColumn], rootID)
		}
		fullSourceIDByRoot[rootID] = sourceID
	}
	if len(fullByRoot) != len(fullRows) {
		t.Fatalf("unpaged related construction has duplicate rows per root: %#v", fullRows)
	}

	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile paged related construction output: %v", err)
	}
	page1 := rootPageRelatedSeedReadPage(t, ctx, client, page, "")
	wantPage1 := []string{project + "_observation_010_eligible_first"}
	if !reflect.DeepEqual(page1.keys, wantPage1) {
		t.Fatalf("first related root-key page = %#v, want eligible root after the earlier seed-only false positive %#v", page1.keys, wantPage1)
	}
	page2 := rootPageRelatedSeedReadPage(t, ctx, client, page, page1.keys[0])
	wantPage2 := []string{project + "_observation_011_eligible_next"}
	if !reflect.DeepEqual(page2.keys, wantPage2) {
		t.Fatalf("next related root-key page = %#v, want next eligible root %#v", page2.keys, wantPage2)
	}
	page3 := rootPageRelatedSeedReadPage(t, ctx, client, page, page2.keys[0])
	if len(page3.keys) != 0 || len(page3.rows) != 0 {
		t.Fatalf("related root-key page after final eligible root = keys %#v rows %#v, want empty", page3.keys, page3.rows)
	}

	pagedRows := append(append([]map[string]any(nil), page1.rows...), page2.rows...)
	if len(page1.rows) != 1 || len(page2.rows) != 1 || len(pagedRows) != len(fullRows) {
		t.Fatalf("paged related construction row multiplicity = page1:%#v page2:%#v full:%#v; want one row per eligible root", page1.rows, page2.rows, fullRows)
	}
	pagedByRoot := rootPageRelatedSeedRowsByRoot(t, pagedRows)
	if len(pagedByRoot) != 2 || len(pagedByRoot) != len(pagedRows) {
		t.Fatalf("paged related construction has duplicate or missing source rows: %#v", pagedRows)
	}
	for _, rootID := range wantObservationIDs {
		row, ok := pagedByRoot[rootID]
		if !ok {
			t.Errorf("paged output omitted eligible source %q: %#v", rootID, pagedRows)
			continue
		}
		if row["patient_id"] != targetID {
			t.Errorf("paged row for %q selected patient %#v, want sorted-first %q", rootID, row["patient_id"], targetID)
		}
		if got := row[ir.PreviewSourceResourceIDColumn]; got != fullSourceIDByRoot[rootID] || got != rootID {
			t.Errorf("paged source identity for %q = %#v, want the same stored Observation ID %#v", rootID, got, fullSourceIDByRoot[rootID])
		}
	}
	if !reflect.DeepEqual(rootPageRelatedSeedRowSignatures(pagedRows), rootPageRelatedSeedRowSignatures(fullRows)) {
		t.Fatalf("paged related construction rows differ from unpaged rows: paged=%#v full=%#v", pagedRows, fullRows)
	}
}

type rootPageRelatedSeedPage struct {
	keys []string
	rows []map[string]any
}

func rootPageRelatedSeedReadPage(t *testing.T, ctx context.Context, client *store.Client, page CompiledOutputPage, after string) rootPageRelatedSeedPage {
	t.Helper()
	keyBinds := rootPageRelatedSeedCloneBinds(page.RootKeysBindVars)
	keyBinds[RootPageAfterKeyBind] = after
	result := rootPageRelatedSeedPage{keys: make([]string, 0, 1)}
	if err := client.QueryRows(ctx, page.RootKeysQuery, 100, keyBinds, func(row map[string]any) error {
		key, ok := row["_key"].(string)
		if !ok || key == "" {
			return fmt.Errorf("root-key query returned invalid _key %#v", row["_key"])
		}
		result.keys = append(result.keys, key)
		return nil
	}); err != nil {
		t.Fatalf("execute related root-key page after %q: %v\n%s", after, err, page.RootKeysQuery)
	}
	if len(result.keys) == 0 {
		return result
	}
	if len(result.keys) != 1 {
		t.Fatalf("related root-key page after %q returned %#v, want at most one key", after, result.keys)
	}
	rowBinds := rootPageRelatedSeedCloneBinds(page.RowsBindVars)
	rowBinds[RootPageKeysBind] = result.keys
	if err := client.QueryRows(ctx, page.RowsQuery, 100, rowBinds, func(row map[string]any) error {
		result.rows = append(result.rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute selected-root rows after %q: %v\n%s", after, err, page.RowsQuery)
	}
	return result
}

func rootPageRelatedSeedBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
		PreviewLimit: 25, IncludeSourceIdentity: true,
	}
}

func rootPageRelatedSeedOutput(patientID string) recipe.Output {
	patientColumn := recipe.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}
	observationColumn := recipe.StageColumn{ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string"}
	return recipe.Output{
		Name: "observations", RootResourceType: "Observation", RootOccurrenceID: "base", RowGrain: "observation",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{
			Name: "observation_id", ColumnID: observationColumn.ID, Label: observationColumn.Label, FieldRef: "id",
			Expr: recipe.Expression{Select: "root.id"},
		}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "patient-occurrence", Alias: "patient-occurrence", ToResourceType: "Patient",
			MatchMode: recipe.MatchOptional,
			Fields: []recipe.Field{{
				Name: "patient_id", ColumnID: patientColumn.ID, Label: patientColumn.Label, FieldRef: "id",
				Expr: recipe.Expression{Select: "patient-occurrence.id"}, ValueMode: recipe.ValueModeAuto,
			}},
		}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{observationColumn, patientColumn},
			Steps: []recipe.ConstructionStep{{
				ID: "keep_matching_patient", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: patientColumn.ID, Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &patientID}},
				}},
				Outputs: []recipe.StageColumn{observationColumn, patientColumn},
			}},
		},
	}
}

func compileRootPageRelatedSeedOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name, TranslationVersion: "root-page-related-seed-test",
		Outputs: []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related root-page recipe plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "root-page-related-seed-test-scope", bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("resolve related root-page recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower related root-page recipe plan: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled related output count = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0]
}

func rootPageRelatedSeedCloneBinds(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func rootPageRelatedSeedRowsByRoot(t *testing.T, rows []map[string]any) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		rootID, ok := row["observation_id"].(string)
		if !ok || rootID == "" {
			t.Fatalf("related root-page row has invalid observation_id %#v: %#v", row["observation_id"], row)
		}
		if _, duplicate := result[rootID]; duplicate {
			t.Fatalf("related root-page output duplicated root %q: %#v", rootID, rows)
		}
		result[rootID] = row
	}
	return result
}

func rootPageRelatedSeedSortedKeys(rows map[string]map[string]any) []string {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func rootPageRelatedSeedRowSignatures(rows []map[string]any) []string {
	signatures := make([]string, 0, len(rows))
	for _, row := range rows {
		signatures = append(signatures, fmt.Sprintf("%v\x00%v\x00%v", row["observation_id"], row["patient_id"], row[ir.PreviewSourceResourceIDColumn]))
	}
	sort.Strings(signatures)
	return signatures
}
