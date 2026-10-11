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

func TestCompileCategoryScanStreamsExactRelatedRouteWithScopeAndStageWitnesses(t *testing.T) {
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
	if strings.Contains(query, "__loom_construction_stage_") || strings.Contains(query, "related_expand_records") || strings.Contains(query, "related_field_document") ||
		strings.Contains(query, "__loom_related_category_candidates") || strings.Contains(query, "reverse_edge_") {
		t.Fatalf("exact related-category scan retained a materialized fallback or terminal candidate pass:\n%s", query)
	}
	for _, marker := range []string{
		"FOR __loom_related_category_stream_root IN @@root_collection",
		"__loom_related_category_stream_edge_1._from == __loom_related_category_stream_root._id",
		"RETURN __loom_related_category_stream_document_1._id",
		"APPEND(__loom_related_category_stream_records_1, LENGTH(__loom_related_category_stream_records_1)==0 ? [null] : [])",
		"COLLECT __loom_related_category_stream_parent_key = __loom_related_category_stream_first_parent",
		"DOCUMENT(__loom_related_category_stream_parent_key)",
		"__loom_related_category_stream_edge_2._to == __loom_related_category_stream_parent._id",
		"APPEND(__loom_related_category_stream_records_2, LENGTH(__loom_related_category_stream_records_2)==0 ? [null] : [])",
		"COLLECT present = __loom_related_category_stream_present, value = __loom_related_category_stream_value",
	} {
		if !strings.Contains(query, marker) {
			t.Errorf("streamed related-category query omitted %q:\n%s", marker, query)
		}
	}
	firstHopScope := strings.Index(query, "__loom_related_category_stream_document_1.auth_resource_path IN @auth_resource_paths")
	parentIdentityCollect := strings.Index(query, "COLLECT __loom_related_category_stream_parent_key = __loom_related_category_stream_first_parent")
	secondHop := strings.Index(query, "FOR __loom_related_category_stream_edge_2 IN @@")
	terminalPresence := strings.Index(query, `HAS(__loom_related_category_stream_document_2.payload.valueQuantity, "code")`)
	secondDocumentScope := strings.Index(query, "__loom_related_category_stream_document_2.auth_resource_path IN @auth_resource_paths")
	if firstHopScope < 0 || parentIdentityCollect <= firstHopScope || secondHop <= parentIdentityCollect || terminalPresence <= secondDocumentScope {
		t.Fatalf("identity dedup moved ahead of first-hop scope or terminal presence ahead of terminal scope: firstScope=%d collect=%d secondHop=%d terminalScope=%d terminalPresence=%d\n%s",
			firstHopScope, parentIdentityCollect, secondHop, secondDocumentScope, terminalPresence, query)
	}
	fallbackScope := strings.Index(query, "__loom_related_category_stream_fallback_document_2.auth_resource_path IN @auth_resource_paths")
	fallbackPresence := strings.Index(query, `HAS(__loom_related_category_stream_fallback_document_2.payload.valueQuantity, "code")`)
	if fallbackScope < 0 || fallbackPresence <= fallbackScope {
		t.Fatalf("cross-collection fallback lost its scope-before-presence check: scope=%d presence=%d\n%s", fallbackScope, fallbackPresence, query)
	}
	for _, guard := range []string{
		"__loom_related_category_stream_root.project == @project",
		"__loom_related_category_stream_root.dataset_generation == @dataset_generation",
		"__loom_related_category_stream_root.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_edge_1.project == @project",
		"__loom_related_category_stream_edge_1.dataset_generation == @dataset_generation",
		"__loom_related_category_stream_edge_1.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_document_1.resourceType == @related_expand_0_hop_1_target_type",
		"__loom_related_category_stream_document_1.project == @project",
		"__loom_related_category_stream_document_1.dataset_generation == @dataset_generation",
		"__loom_related_category_stream_document_1.auth_resource_path IN @auth_resource_paths",
	} {
		position := strings.Index(query, guard)
		if position < 0 || position >= parentIdentityCollect {
			t.Errorf("first-hop scope/type guard %q is missing or occurs after identity COLLECT (position=%d collect=%d):\n%s", guard, position, parentIdentityCollect, query)
		}
	}
	for _, variable := range []string{
		"__loom_related_category_stream_root",
		"__loom_related_category_stream_edge_1",
		"__loom_related_category_stream_document_1",
		"__loom_related_category_stream_parent",
		"__loom_related_category_stream_edge_2",
		"__loom_related_category_stream_document_2",
	} {
		if !strings.Contains(query, variable+".project == @project") ||
			!strings.Contains(query, variable+".dataset_generation == @dataset_generation") ||
			!strings.Contains(query, variable+".auth_resource_path IN @auth_resource_paths") {
			t.Errorf("streamed route component %q is missing project, generation, or auth scope:\n%s", variable, query)
		}
	}
	if scanned.CategoryIndex != nil {
		t.Fatalf("streamed related category query should not synchronously prepare a terminal category index: %+v", scanned.CategoryIndex)
	}
	if !containsCollectionBindValue(scanned.BindVars, "Specimen") || !containsCollectionBindValue(scanned.BindVars, "fhir_edge") {
		t.Fatalf("streamed route is missing its typed root or edge collection bind: %#v", scanned.BindVars)
	}
	if !strings.Contains(query, "FOR __loom_related_category_stream_document_2 IN @@__loom_related_category_target_collection") ||
		scanned.BindVars["@__loom_related_category_target_collection"] != "Observation" {
		t.Fatalf("valueQuantity.code projection is not using the exact typed Observation primary-key collection lookup: bind=%#v\n%s", scanned.BindVars["@__loom_related_category_target_collection"], query)
	}
	if !strings.Contains(query, "IS_SAME_COLLECTION(@related_expand_1_hop_1_target_type, __loom_related_category_stream_edge_2._from)") ||
		!strings.Contains(query, "LET __loom_related_category_stream_fallback_document_2 = DOCUMENT(__loom_related_category_stream_edge_2._from)") {
		t.Fatalf("projected category lookup must retain the collection-identity fallback for a resourceType stored in another collection:\n%s", query)
	}
	for _, routeValue := range []string{"Specimen", "Patient", "Observation", "subject_Patient", "fhir_edge"} {
		if !relatedCategoryHasBindValue(scanned.BindVars, routeValue) {
			t.Errorf("typed related route did not bind exact route value %q: %#v", routeValue, scanned.BindVars)
		}
	}
	assertCategoryScanBindVarsMatchQuery(t, scanned.Query, scanned.BindVars)
	if scanned.OverflowWitness != nil {
		if scanned.Proof.OverflowWitnessFingerprint == "" {
			t.Fatal("overflow witness is not bound into the category proof")
		}
		assertCategoryScanBindVarsMatchQuery(t, scanned.OverflowWitness.Query, scanned.OverflowWitness.BindVars)
	}
	assertCategoryScanLimitFollowsStreamedWitnesses(t, scanned.Query)
	if scanned.BindVars[categoryLimitBind] != MaxCategoryScanValues+1 {
		t.Fatalf("category limit = %#v, want complete-category sentinel %d", scanned.BindVars[categoryLimitBind], MaxCategoryScanValues+1)
	}
	if !scanned.Proof.PresenceTracked || !strings.Contains(query, "stream_present") || !strings.Contains(query, "HAS(") {
		t.Fatalf("exact related-field presence is not bound into the category proof/query: proof=%#v\n%s", scanned.Proof, query)
	}
}

func TestRelatedCategoryStreamingPathRequiresTwoSinglePreservingHops(t *testing.T) {
	stage := func(routeHops int, policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalConstructionStage {
		return ir.PhysicalConstructionStage{RelatedExpand: &ir.PhysicalStageRelatedExpand{
			EmptyPolicy: policy,
			Route:       make([]ir.PhysicalStageRelatedRouteStep, routeHops),
		}}
	}
	preserve := ir.PhysicalUnnestPreserveParent
	valid := relatedCategoryPlan{
		expands:     []ir.PhysicalConstructionStage{stage(1, preserve), stage(1, preserve)},
		hopsByStage: [][]relatedCategoryHop{{{}}, {{}}},
	}
	if !relatedCategoryStreamingPathEligible(valid) {
		t.Fatal("two one-hop PRESERVE_PARENT stages should use the streaming related-category renderer")
	}
	for _, test := range []struct {
		name  string
		shape relatedCategoryPlan
	}{
		{name: "one stage", shape: relatedCategoryPlan{expands: valid.expands[:1], hopsByStage: valid.hopsByStage[:1]}},
		{name: "three stages", shape: relatedCategoryPlan{expands: append(append([]ir.PhysicalConstructionStage(nil), valid.expands...), stage(1, preserve)), hopsByStage: append(append([][]relatedCategoryHop(nil), valid.hopsByStage...), []relatedCategoryHop{{}})}},
		{name: "multi-hop stage", shape: relatedCategoryPlan{expands: []ir.PhysicalConstructionStage{stage(1, preserve), stage(2, preserve)}, hopsByStage: valid.hopsByStage}},
		{name: "non-preserving stage", shape: relatedCategoryPlan{expands: []ir.PhysicalConstructionStage{stage(1, preserve), stage(1, ir.PhysicalUnnestExclude)}, hopsByStage: valid.hopsByStage}},
		{name: "route and traversal mismatch", shape: relatedCategoryPlan{expands: valid.expands, hopsByStage: [][]relatedCategoryHop{{{}}, {{}, {}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if relatedCategoryStreamingPathEligible(test.shape) {
				t.Fatal("unsupported related shape selected the streaming renderer")
			}
		})
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
	if !strings.Contains(deniedScan.Query, "__loom_construction_stage_") || strings.Contains(deniedScan.Query, "__loom_related_category_stream_") {
		t.Fatalf("scope_allowed=false physical plan bypassed its source guards or selected the streamed specialization:\n%s", deniedScan.Query)
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

	// s1 and s3 share one populated patient; s1 also has a sibling patient with
	// no Observation. s2 has no Patient. The category query may deduplicate the
	// shared patient identity, but the ordinary output must preserve each root's
	// full row multiplicity and both empty boundaries.
	s1 := insertResource(project, "Specimen", "s1", map[string]any{"status": "available"})
	insertResource(project, "Specimen", "s2", map[string]any{"status": "available"})
	s3 := insertResource(project, "Specimen", "s3", map[string]any{"status": "available"})
	p1 := insertResource(project, "Patient", "p1", map[string]any{})
	p2 := insertResource(project, "Patient", "p2", map[string]any{})
	insertEdge(project, "s1-p1", "Specimen", s1, "Patient", p1)
	insertEdge(project, "s1-p2", "Specimen", s1, "Patient", p2)
	insertEdge(project, "s3-p1", "Specimen", s3, "Patient", p1)
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
	if len(fullRows) != 10 {
		t.Fatalf("native related rows = %d, want eight linked Observation rows for two roots sharing Patient p1, one empty second-hop row, and one empty first-hop row: %#v", len(fullRows), fullRows)
	}
	var sawSecondStageEmpty, sawFirstStageEmpty bool
	rowsByRoot := map[string]int{}
	for _, row := range fullRows {
		rootID, _ := row["specimen_id"].(string)
		rowsByRoot[rootID]++
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
	if want := map[string]int{"s1": 5, "s2": 1, "s3": 4}; !reflect.DeepEqual(rowsByRoot, want) {
		t.Fatalf("native output multiplicity by Specimen = %v, want %v; category-scan identity dedup must not affect ordinary rows", rowsByRoot, want)
	}
	// The public no-Pivot output materializes optional related fields as nullable
	// values, so its `category` property cannot distinguish a missing source
	// path from explicit NULL. Discover chooser categories from an independent
	// raw traversal that tracks both PRESERVE_PARENT boundaries and exact path
	// presence instead.
	rawCategories := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{
		Query: `FOR root IN Specimen
  FILTER root.project == @project AND root.dataset_generation == @generation
  LET patients = (
    FOR first_edge IN fhir_edge
      FILTER first_edge._from == root._id AND first_edge.label == "subject_Patient" AND first_edge.to_type == "Patient"
        AND first_edge.project == @project AND first_edge.dataset_generation == @generation
      LET patient = DOCUMENT(first_edge._to)
      FILTER patient != null AND patient.resourceType == "Patient" AND patient.project == @project
        AND patient.dataset_generation == @generation
      RETURN patient
  )
  FOR patient_record IN APPEND(patients, LENGTH(patients) == 0 ? [null] : [])
    LET observations = (
      FOR second_edge IN fhir_edge
        FILTER patient_record != null AND second_edge._to == patient_record._id
          AND second_edge.label == "subject_Patient" AND second_edge.from_type == "Observation"
          AND second_edge.project == @project AND second_edge.dataset_generation == @generation
        LET observation = DOCUMENT(second_edge._from)
        FILTER observation != null AND observation.resourceType == "Observation" AND observation.project == @project
          AND observation.dataset_generation == @generation
        RETURN observation
    )
    FOR terminal IN APPEND(observations, LENGTH(observations) == 0 ? [null] : [])
      LET category_present = terminal == null ? true : (
        IS_OBJECT(terminal) AND HAS(terminal, "payload")
        AND IS_OBJECT(terminal.payload) AND HAS(terminal.payload, "valueQuantity")
        AND IS_OBJECT(terminal.payload.valueQuantity) AND HAS(terminal.payload.valueQuantity, "code")
      )
      LET category_value = category_present ? terminal.payload.valueQuantity.code : null
      COLLECT present = category_present, value = category_value
      RETURN { present, value }
`,
		BindVars: map[string]any{"project": project, "generation": generation},
	})
	want := map[string]struct{}{
		relatedCategoryValueKey(false, nil):            {},
		relatedCategoryValueKey(true, nil):             {},
		relatedCategoryValueKey(true, "native-string"): {},
	}
	oracleCategories := map[string]struct{}{}
	for _, row := range rawCategories {
		present, ok := row["present"].(bool)
		if !ok {
			t.Fatalf("independent raw category presence = %#v, want bool", row["present"])
		}
		oracleCategories[relatedCategoryValueKey(present, row["value"])] = struct{}{}
	}
	if !reflect.DeepEqual(oracleCategories, want) {
		t.Fatalf("independent raw category states = %v, want explicit MISSING, NULL, and native string: %v", oracleCategories, want)
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
		t.Fatalf("optimized categories = %v, independent raw categories = %v; query:\n%s", got, want, scanned.Query)
	}
	if _, found := got[relatedCategoryValueKey(true, "orphan-terminal")]; found {
		t.Fatalf("orphan terminal leaked into related categories: %v", got)
	}
	if _, ok := want[relatedCategoryValueKey(true, nil)]; !ok {
		t.Fatalf("independent raw traversal omitted its expected preserved/explicit NULL category: %v", want)
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
		"__loom_related_category_stream_root.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_edge_1.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_document_1.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_edge_2.auth_resource_path IN @auth_resource_paths",
		"__loom_related_category_stream_document_2.auth_resource_path IN @auth_resource_paths",
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
			if strings.Contains(scanned.Query, "__loom_related_category_stream_") {
				t.Fatalf("unsafe shape selected the streamed related-category specialization:\n%s", scanned.Query)
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

func assertCategoryScanLimitFollowsStreamedWitnesses(t *testing.T, query string) {
	t.Helper()
	firstWitness := strings.Index(query, "APPEND(__loom_related_category_stream_records_1,")
	parentIdentity := strings.Index(query, "COLLECT __loom_related_category_stream_parent_key = __loom_related_category_stream_first_parent")
	secondWitness := strings.Index(query, "APPEND(__loom_related_category_stream_records_2,")
	distinct := strings.Index(query, "COLLECT present = __loom_related_category_stream_present, value = __loom_related_category_stream_value")
	limit := strings.LastIndex(query, "LIMIT @"+categoryLimitBind)
	if firstWitness < 0 || parentIdentity <= firstWitness || secondWitness <= parentIdentity || distinct <= secondWitness || limit <= distinct {
		t.Fatalf("parent identity and typed category aggregation must follow the scoped first hop and preserved empty witnesses:\n%s", query)
	}
	if strings.Count(query, "COLLECT ") != 2 {
		t.Fatalf("streamed category query must use one scoped parent identity collect and one final typed distinct aggregation:\n%s", query)
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
