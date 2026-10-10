package compiler

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestCompileCategoryScanReversesExactRelatedRouteWithScopeAndStageWitnesses(t *testing.T) {
	output := compilePopulationMappingOutput(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}))
	scanned, err := CompileCategoryScanStageWithPolicy(output, "add_amount", "category_id", "amount_id", MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	query := scanned.Query
	wantRoute := []struct{ from, to, direction string }{
		{from: "Specimen", to: "Patient", direction: "OUTBOUND"},
		{from: "Patient", to: "Observation", direction: "INBOUND"},
	}
	var gotRoute []struct{ from, to, direction string }
	for _, stage := range output.Plan.StageSequence.Stages {
		if stage.RelatedExpand == nil {
			continue
		}
		for _, hop := range stage.RelatedExpand.Route {
			gotRoute = append(gotRoute, struct{ from, to, direction string }{hop.FromResourceType, hop.ToResourceType, hop.StorageDirection})
		}
	}
	if !reflect.DeepEqual(gotRoute, wantRoute) {
		t.Fatalf("typed category route = %#v, want %#v", gotRoute, wantRoute)
	}
	if strings.Contains(query, "__loom_construction_stage_") || strings.Contains(query, "related_expand_records") || strings.Contains(query, "related_field_document") {
		t.Fatalf("exact related-category scan fell back to forward construction materialization:\n%s", query)
	}
	for _, required := range []string{
		"._from ==", "._to ==", "project == @project",
		"dataset_generation == @dataset_generation", "auth_resource_path", "auth_resource_paths",
	} {
		if !strings.Contains(query, required) {
			t.Errorf("reverse route is missing %q:\n%s", required, query)
		}
	}
	for _, marker := range []string{"__loom_related_category_candidates", "__loom_related_category_empty_stage_1", "__loom_related_category_empty_stage_2"} {
		if !strings.Contains(query, marker) {
			t.Errorf("related-category query is missing stage or candidate marker %q:\n%s", marker, query)
		}
	}
	if scanned.CategoryIndex == nil || scanned.CategoryIndex.Collection != "Observation" || !reflect.DeepEqual(scanned.CategoryIndex.Fields, []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}) {
		t.Fatalf("related category index = %+v, want the five-field Observation code index", scanned.CategoryIndex)
	}
	legacyFields := []string{"project", "dataset_generation", "payload.valueQuantity.code", "auth_resource_path"}
	if scanned.CategoryIndex.Supersedes == nil || scanned.CategoryIndex.Supersedes.Name != previewCoveringIndexName("Observation", legacyFields) || !reflect.DeepEqual(scanned.CategoryIndex.Supersedes.Fields, legacyFields) {
		t.Fatalf("related category index supersession = %+v, want only its deterministic four-field legacy index", scanned.CategoryIndex.Supersedes)
	}
	if !containsCollectionBindValue(scanned.BindVars, "Observation") {
		t.Fatalf("reverse scan is not rooted at terminal Observation candidates: %#v", scanned.BindVars)
	}
	for _, routeValue := range []string{"Specimen", "Patient", "Observation", "subject_Patient", "fhir_edge"} {
		if !relatedCategoryHasBindValue(scanned.BindVars, routeValue) {
			t.Errorf("typed reverse route did not bind exact route value %q: %#v", routeValue, scanned.BindVars)
		}
	}
	assertCategoryScanBindVarsMatchQuery(t, scanned.Query, scanned.BindVars)
	if scanned.OverflowWitness != nil {
		if scanned.Proof.OverflowWitnessFingerprint == "" {
			t.Fatal("overflow witness is not bound into the category proof")
		}
		assertCategoryScanBindVarsMatchQuery(t, scanned.OverflowWitness.Query, scanned.OverflowWitness.BindVars)
	}
	assertCategoryScanLimitFollowsStageWitnesses(t, scanned.Query)
	if scanned.BindVars[categoryLimitBind] != MaxCategoryScanValues+1 {
		t.Fatalf("category limit = %#v, want complete-category sentinel %d", scanned.BindVars[categoryLimitBind], MaxCategoryScanValues+1)
	}
	if !scanned.Proof.PresenceTracked || !strings.Contains(query, "candidate_present") || !strings.Contains(query, "present: category.present") {
		t.Fatalf("exact related-field presence is not bound into the category proof/query: proof=%#v\n%s", scanned.Proof, query)
	}
}

func TestRelatedCategoryScanRetainsLegacyGenerationRejectionAndFalseScopeFallback(t *testing.T) {
	legacy := compilePopulationMappingOutput(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}))
	legacy.OptimizedPlan = nil
	legacy.Plan = ir.ClonePhysicalPlan(legacy.Plan)
	legacy.Plan.BindVars["dataset_generation"] = nil
	if _, err := CompileCategoryScanStageWithPolicy(legacy, "add_amount", "category_id", "amount_id", MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("nil dataset generation bypassed the existing typed related-field source validation")
	}

	denied := compilePopulationMappingOutput(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}))
	denied.OptimizedPlan = nil
	denied.Plan = ir.ClonePhysicalPlan(denied.Plan)
	denied.Plan.BindVars["scope_allowed"] = false
	deniedScan, err := CompileCategoryScanStageWithPolicy(denied, "add_amount", "category_id", "amount_id", MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deniedScan.Query, "__loom_construction_stage_") || strings.Contains(deniedScan.Query, "__loom_related_category_candidates") {
		t.Fatalf("scope_allowed=false physical plan bypassed its source guards:\n%s", deniedScan.Query)
	}
}

func TestRelatedCategoryScanMatchesPreservedRowsAndCapsAfterEmptyWitnessAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_related_category_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation-related-category"
	witnessProject := project + "_witness_second"
	firstStageProject := project + "_witness_first"
	capProject := project + "_cap"
	defer func() {
		for _, ownedProject := range []string{project, witnessProject, firstStageProject, capProject} {
			for _, collection := range []string{"Specimen", "Patient", "Observation", "fhir_edge"} {
				query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
				if err := client.ExecuteAQL(ctx, query, map[string]any{"project": ownedProject}); err != nil {
					t.Errorf("remove related-category fixture from %s: %v", collection, err)
				}
			}
		}
	}()

	insertResource := func(targetProject, resourceType, id string, payload map[string]any) string {
		t.Helper()
		key := strings.ReplaceAll(targetProject, "-", "") + "_" + id
		payload["id"], payload["resourceType"] = id, resourceType
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "id": id, "project": targetProject, "project_id": targetProject,
			"dataset_generation": generation, "resourceType": resourceType, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, resourceType, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related-category %s %s: %v", resourceType, id, err)
		}
		return key
	}
	insertEdge := func(targetProject, id, fromType, fromKey, toType, toKey string) {
		t.Helper()
		key := strings.ReplaceAll(targetProject, "-", "") + "_edge_" + id
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "_from": fromType + "/" + fromKey, "_to": toType + "/" + toKey,
			"project": targetProject, "project_id": targetProject, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related-category edge %s: %v", id, err)
		}
	}

	// s1 has one populated patient and a sibling patient with no Observation;
	// s2 has no Patient. Both empty boundaries must contribute the native NULL.
	s1 := insertResource(project, "Specimen", "s1", map[string]any{"status": "available"})
	insertResource(project, "Specimen", "s2", map[string]any{"status": "available"})
	p1 := insertResource(project, "Patient", "p1", map[string]any{})
	p2 := insertResource(project, "Patient", "p2", map[string]any{})
	insertEdge(project, "s1-p1", "Specimen", s1, "Patient", p1)
	insertEdge(project, "s1-p2", "Specimen", s1, "Patient", p2)
	for id, quantity := range map[string]any{
		"o-code":    map[string]any{"code": "native-string"},
		"o-null":    map[string]any{"code": nil},
		"o-missing": map[string]any{"value": 4.0},
	} {
		o := insertResource(project, "Observation", id, map[string]any{"valueQuantity": quantity})
		insertEdge(project, id+"-patient", "Observation", o, "Patient", p1)
	}
	duplicate := insertResource(project, "Observation", "o-duplicate", map[string]any{"valueQuantity": map[string]any{"code": "native-string"}})
	insertEdge(project, "o-duplicate-patient", "Observation", duplicate, "Patient", p1)
	insertResource(project, "Observation", "o-orphan", map[string]any{"valueQuantity": map[string]any{"code": "orphan-terminal"}})

	_, full, scanned := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), project, generation, MaxCategoryScanValues)
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) != 6 {
		t.Fatalf("native related rows = %d, want four linked Observation rows plus two per-boundary preserved NULL rows: %#v", len(fullRows), fullRows)
	}
	var sawSecondStageEmpty, sawFirstStageEmpty bool
	for _, row := range fullRows {
		if row["specimen_id"] == "s1" && row["patient_record"] == "p2" && row["observation_record"] == nil {
			sawSecondStageEmpty = true
		}
		if row["specimen_id"] == "s2" && row["patient_record"] == nil && row["observation_record"] == nil {
			sawFirstStageEmpty = true
		}
	}
	if !sawSecondStageEmpty || !sawFirstStageEmpty {
		t.Fatalf("native rows omitted an empty expansion boundary: second=%t first=%t rows=%#v", sawSecondStageEmpty, sawFirstStageEmpty, fullRows)
	}
	want := map[string]struct{}{}
	for _, row := range fullRows {
		value, present := row["category"]
		want[relatedCategoryValueKey(present, value)] = struct{}{}
	}
	actualRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: scanned.Query, BindVars: scanned.BindVars})
	got := map[string]struct{}{}
	for _, row := range actualRows {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			t.Fatalf("category presence marker = %#v, want bool", row[scanned.PresentColumn])
		}
		got[relatedCategoryValueKey(present, row[scanned.ValueColumn])] = struct{}{}
	}
	if len(actualRows) != len(got) {
		t.Fatalf("optimized category scan returned duplicate category rows: rows=%#v distinct=%v", actualRows, got)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("optimized categories = %v, native distinct categories = %v; query:\n%s", got, want, scanned.Query)
	}
	if _, found := got[relatedCategoryValueKey(true, "orphan-terminal")]; found {
		t.Fatalf("orphan terminal leaked into related categories: %v", got)
	}
	if _, ok := want[relatedCategoryValueKey(true, nil)]; !ok {
		t.Fatalf("native recipe did not expose its expected preserved/explicit NULL category: %v", want)
	}

	// This isolated project has only the category "d" on terminal records,
	// so NULL can only come from the empty second-stage expansion.
	witnessRoot := insertResource(witnessProject, "Specimen", "root", map[string]any{"status": "available"})
	witnessFull := insertResource(witnessProject, "Patient", "full", map[string]any{})
	witnessEmpty := insertResource(witnessProject, "Patient", "empty", map[string]any{})
	insertEdge(witnessProject, "root-full", "Specimen", witnessRoot, "Patient", witnessFull)
	insertEdge(witnessProject, "root-empty", "Specimen", witnessRoot, "Patient", witnessEmpty)
	witnessObservation := insertResource(witnessProject, "Observation", "only-code", map[string]any{"valueQuantity": map[string]any{"code": "d"}})
	insertEdge(witnessProject, "only-code-patient", "Observation", witnessObservation, "Patient", witnessFull)
	_, _, witnessScan := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), witnessProject, generation, MaxCategoryScanValues)
	witnessRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: witnessScan.Query, BindVars: witnessScan.BindVars})
	witnessCategories := map[string]struct{}{}
	for _, row := range witnessRows {
		witnessCategories[relatedCategoryValueKey(row[witnessScan.PresentColumn].(bool), row[witnessScan.ValueColumn])] = struct{}{}
	}
	if len(witnessCategories) != 2 {
		t.Fatalf("stage witnesses returned %d categories, want string d and the preserved NULL: %v", len(witnessCategories), witnessCategories)
	}
	if _, ok := witnessCategories[relatedCategoryValueKey(true, nil)]; !ok {
		t.Fatalf("second-stage PRESERVE_PARENT NULL was lost: %v", witnessCategories)
	}

	// This project has one complete non-NULL chain and one root with no Patient,
	// isolating the first PRESERVE_PARENT boundary.
	firstFullRoot := insertResource(firstStageProject, "Specimen", "full-root", map[string]any{"status": "available"})
	insertResource(firstStageProject, "Specimen", "empty-root", map[string]any{"status": "available"})
	firstPatient := insertResource(firstStageProject, "Patient", "only-patient", map[string]any{})
	insertEdge(firstStageProject, "full-root-patient", "Specimen", firstFullRoot, "Patient", firstPatient)
	firstObservation := insertResource(firstStageProject, "Observation", "only-code", map[string]any{"valueQuantity": map[string]any{"code": "e"}})
	insertEdge(firstStageProject, "only-code-patient", "Observation", firstObservation, "Patient", firstPatient)
	_, _, firstStageScan := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), firstStageProject, generation, MaxCategoryScanValues)
	firstStageRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: firstStageScan.Query, BindVars: firstStageScan.BindVars})
	firstStageCategories := map[string]struct{}{}
	for _, row := range firstStageRows {
		firstStageCategories[relatedCategoryValueKey(row[firstStageScan.PresentColumn].(bool), row[firstStageScan.ValueColumn])] = struct{}{}
	}
	if len(firstStageCategories) != 2 {
		t.Fatalf("first-stage witness returned %d categories, want string e and the preserved NULL: %v", len(firstStageCategories), firstStageCategories)
	}
	if _, ok := firstStageCategories[relatedCategoryValueKey(true, nil)]; !ok {
		t.Fatalf("first-stage PRESERVE_PARENT NULL was lost: %v", firstStageCategories)
	}

	// Exactly 256 distinct terminal strings plus one empty PRESERVE_PARENT
	// category must return 257 rows so the scan can report overflow accurately.
	capSpecimen := insertResource(capProject, "Specimen", "root", map[string]any{"status": "available"})
	capFull := insertResource(capProject, "Patient", "full", map[string]any{})
	capEmpty := insertResource(capProject, "Patient", "empty", map[string]any{})
	insertEdge(capProject, "root-full", "Specimen", capSpecimen, "Patient", capFull)
	insertEdge(capProject, "root-empty", "Specimen", capSpecimen, "Patient", capEmpty)
	for index := 0; index < MaxCategoryScanValues; index++ {
		id := fmt.Sprintf("cap-%03d", index)
		observation := insertResource(capProject, "Observation", id, map[string]any{"valueQuantity": map[string]any{"code": id}})
		insertEdge(capProject, id+"-patient", "Observation", observation, "Patient", capFull)
	}
	_, _, capScan := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), capProject, generation, MaxCategoryScanValues)
	capRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: capScan.Query, BindVars: capScan.BindVars})
	if len(capRows) != MaxCategoryScanValues+1 {
		t.Fatalf("category scan returned %d rows for 256 string categories plus a preserved empty category; want sentinel %d", len(capRows), MaxCategoryScanValues+1)
	}
}

func TestRelatedCategoryScanMatchesRestrictedRootEdgeAndTargetScopeAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_related_category_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation-related-category-auth"
	defer func() {
		for _, collection := range []string{"Specimen", "Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(ctx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove restricted related-category fixture from %s: %v", collection, err)
			}
		}
	}()

	insertResource := func(resourceType, id, authPath string, payload map[string]any) string {
		t.Helper()
		key := strings.ReplaceAll(project, "-", "") + "_" + id
		if payload == nil {
			payload = map[string]any{}
		}
		payload["id"], payload["resourceType"] = id, resourceType
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": resourceType,
			"auth_resource_path": authPath,
			"payload":            payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, resourceType, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert scoped related-category %s %s: %v", resourceType, id, err)
		}
		return key
	}
	insertEdge := func(id, fromType, fromKey, toType, toKey, authPath string) {
		t.Helper()
		key := strings.ReplaceAll(project, "-", "") + "_edge_" + id
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "_from": fromType + "/" + fromKey, "_to": toType + "/" + toKey,
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": fromType, "to_type": toType,
			"auth_resource_path": authPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert scoped related-category edge %s: %v", id, err)
		}
	}
	const allowed, blocked = "/allowed", "/blocked"
	allowedSpecimen := insertResource("Specimen", "allowed-root", allowed, nil)
	blockedSpecimen := insertResource("Specimen", "blocked-root", blocked, nil)
	allowedPatient := insertResource("Patient", "allowed-patient", allowed, nil)
	blockedPatient := insertResource("Patient", "blocked-patient", blocked, nil)
	firstEdgeBlockedPatient := insertResource("Patient", "first-edge-patient", allowed, nil)
	rootOnlyPatient := insertResource("Patient", "root-only-patient", allowed, nil)
	insertEdge("allowed-root-patient", "Specimen", allowedSpecimen, "Patient", allowedPatient, allowed)
	insertEdge("first-edge-blocked", "Specimen", allowedSpecimen, "Patient", firstEdgeBlockedPatient, blocked)
	insertEdge("patient-target-blocked", "Specimen", allowedSpecimen, "Patient", blockedPatient, allowed)
	insertEdge("blocked-root-patient", "Specimen", blockedSpecimen, "Patient", rootOnlyPatient, allowed)

	addObservation := func(id, patientKey, code, edgePath, targetPath string) {
		t.Helper()
		observation := insertResource("Observation", id, targetPath, map[string]any{"valueQuantity": map[string]any{"code": code}})
		insertEdge(id+"-patient", "Observation", observation, "Patient", patientKey, edgePath)
	}
	addObservation("included", allowedPatient, "included", allowed, allowed)
	addObservation("first-edge-denied", firstEdgeBlockedPatient, "first-edge-denied", allowed, allowed)
	addObservation("patient-denied", blockedPatient, "patient-denied", allowed, allowed)
	addObservation("root-denied", rootOnlyPatient, "root-denied", allowed, allowed)
	addObservation("second-edge-denied", allowedPatient, "second-edge-denied", blocked, allowed)
	addObservation("terminal-denied", allowedPatient, "terminal-denied", allowed, blocked)
	insertResource("Observation", "orphan", allowed, map[string]any{"valueQuantity": map[string]any{"code": "orphan"}})
	// The orphan is intentionally not connected to any Patient by subject_Patient.

	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{allowed},
	}
	_, full, scanned := compileRelatedCategoryRuntimeScansWithBindings(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), bindings, MaxCategoryScanValues)
	for _, path := range []string{
		"terminal.auth_resource_path IN @auth_resource_paths",
		"reverse_edge_0.auth_resource_path IN @auth_resource_paths",
		"reverse_edge_1.auth_resource_path IN @auth_resource_paths",
		"reverse_document_0.auth_resource_path IN @auth_resource_paths",
		"reverse_document_1.auth_resource_path IN @auth_resource_paths",
	} {
		if !strings.Contains(scanned.Query, path) {
			t.Errorf("related-category query omitted scoped route component %q:\n%s", path, scanned.Query)
		}
	}
	want := map[string]struct{}{}
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	for _, row := range fullRows {
		value, present := row["category"]
		want[relatedCategoryValueKey(present, value)] = struct{}{}
	}
	actualRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: scanned.Query, BindVars: scanned.BindVars})
	got := map[string]struct{}{}
	for _, row := range actualRows {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			t.Fatalf("restricted category presence = %#v, want bool", row[scanned.PresentColumn])
		}
		got[relatedCategoryValueKey(present, row[scanned.ValueColumn])] = struct{}{}
	}
	if len(actualRows) != len(got) {
		t.Fatalf("restricted optimized scan returned duplicate category rows: rows=%#v distinct=%v", actualRows, got)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restricted optimized categories = %v, native categories = %v", got, want)
	}
	for _, denied := range []string{"first-edge-denied", "patient-denied", "root-denied", "second-edge-denied", "terminal-denied", "orphan"} {
		if _, found := got[relatedCategoryValueKey(true, denied)]; found {
			t.Errorf("restricted category scan exposed %q", denied)
		}
	}
	if _, found := got[relatedCategoryValueKey(true, "included")]; !found {
		t.Fatalf("restricted category scan omitted the fully scoped route: %v", got)
	}
}

func compileRelatedCategoryRuntimeScans(t *testing.T, definition recipe.Output, project, generation string, maxValues int) (lower.CompiledRecipeOutput, CompiledQuery, CompiledCategoryScanQuery) {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	return compileRelatedCategoryRuntimeScansWithBindings(t, definition, bindings, maxValues)
}

func compileRelatedCategoryRuntimeScansWithBindings(t *testing.T, definition recipe.Output, bindings recipe.RuntimeBindings, maxValues int) (lower.CompiledRecipeOutput, CompiledQuery, CompiledCategoryScanQuery) {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: definition.Name, TranslationVersion: "related-category-test", Outputs: []recipe.Output{definition}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	full, err := CompileRecipeOutputWithPolicy(output, bindings, 1024, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := CompileCategoryScanStageWithPolicy(output, "add_amount", "category_id", "amount_id", maxValues, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return output, full, scanned
}

func relatedCategoryValueKey(present bool, value any) string {
	encoded, _ := json.Marshal(struct {
		Present bool `json:"present"`
		Value   any  `json:"value"`
	}{Present: present, Value: value})
	return string(encoded)
}

func TestCompileRelatedCategoryScanFallsBackForUnsafeShapes(t *testing.T) {
	contributorValue := "final"
	tests := []struct {
		name    string
		options relatedCategoryScanOptions
	}{
		{name: "population", options: relatedCategoryScanOptions{population: true}},
		{name: "root filter", options: relatedCategoryScanOptions{rootFilter: true}},
		{name: "contributor predicate", options: relatedCategoryScanOptions{contributorPredicate: &contributorValue}},
		{name: "unsupported stage", options: relatedCategoryScanOptions{unsupportedStage: true}},
		{name: "category and value use different active anchors", options: relatedCategoryScanOptions{valueFromPatient: true}},
		{name: "one expansion excludes empty parents", options: relatedCategoryScanOptions{secondEmptyPolicy: recipe.ExpansionExclude}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, relatedCategoryScanOutput(test.options))
			scanned, err := CompileCategoryScanStageWithPolicy(output, test.options.TerminalIDForTest(), "category_id", "amount_id", MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(scanned.Query, "__loom_construction_stage_") || !strings.Contains(scanned.Query, "related_expand_records") {
				t.Fatalf("unsafe shape bypassed the exact construction prefix:\n%s", scanned.Query)
			}
			if strings.Contains(scanned.Query, "__loom_related_category_candidates") {
				t.Fatalf("unsafe shape selected the related-category fast path:\n%s", scanned.Query)
			}
		})
	}
}

// These tests keep invalid scan requests on the typed refusal path rather than
// allowing the related-route specialization to guess at a schema.
func TestCompileRelatedCategoryScanRejectsMismatchedOrInactiveColumnIDs(t *testing.T) {
	output := compilePopulationMappingOutput(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}))
	for _, test := range []struct {
		stage, category, value string
	}{
		{stage: "stale_stage", category: "category_id", value: "amount_id"},
		{stage: "add_amount", category: "stale_category", value: "amount_id"},
		{stage: "add_amount", category: "category_id", value: "stale_value"},
	} {
		_, err := CompileCategoryScanStageWithPolicy(output, test.stage, test.category, test.value, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
		var refusal *CategoryScanRefusal
		if !errors.As(err, &refusal) {
			t.Errorf("scan (%q, %q, %q) error = %v, want typed refusal", test.stage, test.category, test.value, err)
		}
	}
}

func assertCategoryScanLimitFollowsStageWitnesses(t *testing.T, query string) {
	t.Helper()
	union := strings.LastIndex(query, "UNION_DISTINCT(")
	limit := strings.LastIndex(query, "LIMIT ")
	if union < 0 || limit <= union {
		t.Fatalf("complete category cap must follow final distinct across all witness branches:\n%s", query)
	}
	for _, marker := range []string{"__loom_related_category_empty_stage_1", "__loom_related_category_empty_stage_2"} {
		let := strings.Index(query, "LET "+marker+" =")
		if let < 0 || let >= union || !strings.Contains(query[union:limit], marker) {
			t.Fatalf("category cap does not include %s in the final distinct:\n%s", marker, query)
		}
	}
}

func relatedCategoryHasBindValue(bindVars map[string]any, want string) bool {
	for _, value := range bindVars {
		if value == want {
			return true
		}
	}
	return false
}

func containsCollectionBindValue(bindVars map[string]any, want string) bool {
	for key, value := range bindVars {
		if strings.HasPrefix(key, "@") && value == want {
			return true
		}
	}
	return false
}

type relatedCategoryScanOptions struct {
	population           bool
	rootFilter           bool
	contributorPredicate *string
	unsupportedStage     bool
	valueFromPatient     bool
	secondEmptyPolicy    recipe.ExpansionEmptyPolicy
}

// TerminalIDForTest returns the final stage ID for the matching recipe shape.
// It lives on the options type so negative cases scan the same stage they build.
func (options relatedCategoryScanOptions) TerminalIDForTest() string {
	if options.valueFromPatient {
		return "add_category"
	}
	if options.unsupportedStage {
		return "unsupported_filter"
	}
	return "add_amount"
}

func relatedCategoryScanOutput(options relatedCategoryScanOptions) recipe.Output {
	secondPolicy := options.secondEmptyPolicy
	if secondPolicy == "" {
		secondPolicy = recipe.ExpansionPreserveParent
	}
	sourceColumns := []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id"}, {ID: "status", Name: "status"}}
	patientColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{
		ID: "patient_record", Name: "patient_record", Type: "string", Nullable: true,
	})
	steps := []recipe.ConstructionStep{{
		ID: "expand_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "specimen-patient", TargetNodeID: "patient-node", TargetResourceType: "Patient",
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node",
				FromResourceType: "Specimen", ToResourceType: "Patient", Relationship: "subject_Patient",
				StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
			}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "patient_record",
		}}, Outputs: patientColumns,
	}}
	if options.valueFromPatient {
		patientColumns = append(patientColumns, recipe.StageColumn{ID: "amount_id", Name: "amount", Type: "integer", Nullable: true})
		steps = append(steps, relatedFieldStep("add_patient_amount", "expand_patients", "amount_id", patientColumns,
			recipe.ConstructionRelatedFieldSource{CandidateID: "patient-amount", NodeID: "patient-node", ResourceType: "Patient", Path: "Patient.multipleBirthInteger", Cardinality: "optional_one", LogicalType: "integer"}))
	}
	observationColumns := append(append([]recipe.StageColumn(nil), patientColumns...), recipe.StageColumn{
		ID: "observation_record", Name: "observation_record", Type: "string", Nullable: secondPolicy == recipe.ExpansionPreserveParent,
	})
	expandObservation := &recipe.ConstructionRelatedExpand{
		AnchorColumnID: relatedTerminalIdentityColumn("expand_patients"), ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
		Route: []recipe.ConstructionRelatedRouteStep{{
			EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
			StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: secondPolicy, RelatedRecordColumnID: "observation_record",
	}
	if options.contributorPredicate != nil {
		expandObservation.ContributorSource = &recipe.ConstructionRelatedFieldSource{
			CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
			Path: "Observation.status", Cardinality: "optional_one", LogicalType: "code",
		}
		expandObservation.ContributorChoiceID = "observation-status-choice"
		expandObservation.ContributorPredicate = &recipe.ConstructionRelatedPredicate{
			CandidateID: "observation-status", Operator: recipe.FilterEquals,
			Value: &recipe.FilterValue{Kind: recipe.FilterCode, Code: &recipe.CodeValue{Code: *options.contributorPredicate}},
		}
	}
	previous := "expand_patients"
	if options.valueFromPatient {
		previous = "add_patient_amount"
	}
	steps = append(steps, recipe.ConstructionStep{
		ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: previous}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: expandObservation}, Outputs: observationColumns,
	})
	categoryColumns := append(append([]recipe.StageColumn(nil), observationColumns...), recipe.StageColumn{
		ID: "category_id", Name: "category", Type: "string", Nullable: true,
	})
	steps = append(steps, relatedFieldStep("add_category", "expand_observations", "category_id", categoryColumns,
		recipe.ConstructionRelatedFieldSource{CandidateID: "observation-code", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.valueQuantity.code", Cardinality: "optional_one", LogicalType: "string"}))
	terminalID := "add_category"
	if !options.valueFromPatient {
		amountColumns := append(append([]recipe.StageColumn(nil), categoryColumns...), recipe.StageColumn{
			ID: "amount_id", Name: "amount", Type: "decimal", Nullable: true,
		})
		steps = append(steps, relatedFieldStep("add_amount", "add_category", "amount_id", amountColumns,
			recipe.ConstructionRelatedFieldSource{CandidateID: "observation-amount", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.valueQuantity.value", Cardinality: "optional_one", LogicalType: "decimal"}))
		terminalID = "add_amount"
	}
	if options.unsupportedStage {
		lastColumns := steps[len(steps)-1].Outputs
		steps = append(steps, recipe.ConstructionStep{
			ID: "unsupported_filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: terminalID}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "status", Operator: recipe.FilterExists}},
			Outputs:   lastColumns,
		})
		terminalID = "unsupported_filter"
	}
	output := recipe.Output{
		Name: "specimen_related_categories", RootResourceType: "Specimen", RowGrain: "specimen", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "status", ColumnID: "status", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: steps},
	}
	if options.rootFilter {
		output.Filters = []recipe.Filter{{Select: "root.status", Operator: recipe.FilterExists}}
	}
	if options.population {
		output.Population = &recipe.PopulationConstraint{
			SelectionRevisionID: "revision-1", MembershipDigest: "sha256:test", MemberCount: 1, ResourceType: "Specimen",
		}
	}
	return output
}

func relatedFieldStep(id, inputID, outputID string, columns []recipe.StageColumn, source recipe.ConstructionRelatedFieldSource) recipe.ConstructionStep {
	return recipe.ConstructionStep{
		ID: id, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: inputID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
			ChoiceID: id + "-choice", Source: source, OutputColumnID: outputID,
		}}, Outputs: columns,
	}
}

func relatedTerminalIdentityColumn(stageID string) string {
	digest := sha256.Sum256([]byte(stageID))
	return fmt.Sprintf("__loom_related_terminal_id_%x", digest)
}
