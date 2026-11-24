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
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRelatedGroupPreviewReportsValidationWindow(t *testing.T) {
	for _, withSummary := range []bool{true, false} {
		t.Run(fmt.Sprintf("summary_%t", withSummary), func(t *testing.T) {
			bindings := recipe.RuntimeBindings{Project: "owned-project", DatasetGeneration: "owned-generation", IncludeRowIdentity: true}
			output := relatedGroupPreviewOutput(t, bindings)
			if !withSummary {
				output.Construction.Steps = output.Construction.Steps[:3]
			}
			compiled := lowerConstructionOutput(t, output, bindings)
			preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if !preview.PartialValidation {
				t.Fatal("a related Group preview must report that validation is bounded to the selected complete groups")
			}
		})
	}
}

func TestRelatedGroupPreviewPreservesProjectionAndScopeContracts(t *testing.T) {
	for _, test := range []struct {
		name              string
		change            func(*ir.PhysicalPlan)
		invalidProjection bool
		wantError         string
	}{
		{"related_id_projection", func(plan *ir.PhysicalPlan) {
			stage := &plan.StageSequence.Stages[0]
			for i := range stage.OutputProjections {
				if stage.OutputProjections[i].Name == "patient_id" {
					stage.OutputProjections[i].Value.Path = []string{"terminal_id"}
				}
			}
		}, true, ""},
		{"root_key_projection", func(plan *ir.PhysicalPlan) {
			returned := plan.Operations[len(plan.Operations)-1].Return
			for i := range returned.Projections {
				if returned.Projections[i].Name == "_key" {
					returned.Projections[i].Value.Path = []string{"payload", "id"}
				}
			}
		}, false, ""},
		{"edge_project_binding", func(plan *ir.PhysicalPlan) {
			operations := plan.StageSequence.Stages[0].RelatedExpand.RelatedRecords.Operations
			var edgeVariable string
			for _, operation := range operations {
				if operation.Traversal != nil {
					edgeVariable = operation.Traversal.EdgeVariable
				}
				if operation.Filter != nil && operation.Filter.Predicate.Left.Variable == edgeVariable && reflect.DeepEqual(operation.Filter.Predicate.Left.Path, []string{"project"}) {
					operation.Filter.Predicate.Right = &ir.PhysicalValue{BindKey: "other_edge_project"}
				}
			}
			plan.BindVars["other_edge_project"] = plan.BindVars["project"]
		}, false, "apply canonical recipe execution window: validate construction physical execution scope"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bindings := recipe.RuntimeBindings{Project: "owned-project", DatasetGeneration: "owned-generation", IncludeRowIdentity: true}
			compiled := lowerConstructionOutput(t, relatedGroupPreviewOutput(t, bindings), bindings)
			test.change(&compiled.Plan)
			preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) ||
					!strings.Contains(err.Error(), "project scope filter at operation") || !strings.Contains(err.Error(), ".project == @project") {
					t.Fatalf("noncanonical related project binding must be rejected without weakening scope isolation: %v", err)
				}
				return
			}
			if test.invalidProjection {
				if err == nil || !strings.Contains(err.Error(), "public related-record ID must project the terminal FHIR id") {
					t.Fatalf("invalid related-ID projection must be rejected: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if preview.PartialValidation {
				t.Fatal("a changed source, related-ID projection, or route scope must retain canonical execution")
			}
		})
	}
}

func TestRelatedGroupPreviewKeepsCompleteContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_related_group_preview_" + uuid.NewString()
	generation := "owned-preview-generation"
	owned := map[string][]string{}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			query := fmt.Sprintf("FOR d IN %s FILTER d._key IN @keys REMOVE d IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove owned %s fixture: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents []map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			owned[collection] = append(owned[collection], document["_key"].(string))
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	resource := func(kind, key string, id any) map[string]any {
		return map[string]any{"_key": project + "_" + key, "id": id, "resourceType": kind,
			"project": project, "dataset_generation": generation, "auth_resource_path": "/allowed",
			"payload": map[string]any{"id": "payload-" + key, "resourceType": kind}}
	}
	specimens := []map[string]any{}
	for _, key := range []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"} {
		specimens = append(specimens, resource("Specimen", key, key))
	}
	blockedRoot := resource("Specimen", "blocked_root", "blocked_root")
	blockedRoot["auth_resource_path"] = "/blocked"
	wrongGenerationRoot := resource("Specimen", "wrong_generation_root", "wrong_generation_root")
	wrongGenerationRoot["dataset_generation"] = "other-generation"
	wrongProjectRoot := resource("Specimen", "wrong_project_root", "wrong_project_root")
	wrongProjectRoot["project"] = project + "_other"
	insert("Specimen", append(specimens, blockedRoot, wrongGenerationRoot, wrongProjectRoot))
	patients := []map[string]any{
		resource("Patient", "pa", "a"), resource("Patient", "pa_duplicate", "a"),
		resource("Patient", "pb", "b"), resource("Patient", "pz", "z"),
		resource("Patient", "pquote", "\"quoted"), resource("Patient", "pnull", nil),
	}
	blockedPatient := resource("Patient", "blocked_patient", "a")
	blockedPatient["auth_resource_path"] = "/blocked"
	wrongGenerationPatient := resource("Patient", "wrong_generation_patient", "a")
	wrongGenerationPatient["dataset_generation"] = "other-generation"
	wrongProjectPatient := resource("Patient", "wrong_project_patient", "a")
	wrongProjectPatient["project"] = project + "_other"
	insert("Patient", append(patients, blockedPatient, wrongGenerationPatient, wrongProjectPatient))
	observations := []map[string]any{}
	for _, key := range []string{"o1", "o2", "o3", "o4", "o5", "rogue_root"} {
		observations = append(observations, resource("Observation", key, key))
	}
	blockedObservation := resource("Observation", "blocked_observation", "blocked_observation")
	blockedObservation["auth_resource_path"] = "/blocked"
	wrongGenerationObservation := resource("Observation", "wrong_generation_observation", "wrong_generation_observation")
	wrongGenerationObservation["dataset_generation"] = "other-generation"
	wrongProjectObservation := resource("Observation", "wrong_project_observation", "wrong_project_observation")
	wrongProjectObservation["project"] = project + "_other"
	insert("Observation", append(observations, blockedObservation, wrongGenerationObservation, wrongProjectObservation))
	edges := []map[string]any{}
	addEdge := func(kind, from, to string) map[string]any {
		edge := map[string]any{"_key": fmt.Sprintf("%s_edge_%d", project, len(edges)),
			"_from": kind + "/" + project + "_" + from, "_to": "Patient/" + project + "_" + to,
			"project": project, "dataset_generation": generation, "auth_resource_path": "/allowed",
			"label": "subject_Patient", "from_type": kind, "to_type": "Patient"}
		edges = append(edges, edge)
		return edge
	}
	for _, pair := range [][2]string{{"s1", "pa"}, {"s1", "pa"}, {"s1", "pa_duplicate"}, {"s1", "pb"},
		{"s2", "pa_duplicate"}, {"s3", "pb"}, {"s4", "pz"}, {"s5", "pquote"}, {"s6", "pnull"},
		{"s8", "blocked_patient"}, {"s8", "wrong_generation_patient"}, {"s8", "wrong_project_patient"},
		{"blocked_root", "pa"}, {"wrong_generation_root", "pa"}, {"wrong_project_root", "pa"}} {
		addEdge("Specimen", pair[0], pair[1])
	}

	addEdge("Specimen", "s2", "pa_duplicate")["from_type"] = "unexpected-source-type"
	addEdge("Observation", "rogue_root", "pa")["from_type"] = "Specimen"
	for _, pair := range [][2]string{{"o1", "pa"}, {"o1", "pa"}, {"o2", "pa"}, {"o3", "pa_duplicate"},
		{"o4", "pb"}, {"o5", "pb"}, {"blocked_observation", "pa"}, {"wrong_generation_observation", "pa"}, {"wrong_project_observation", "pa"}} {
		addEdge("Observation", pair[0], pair[1])
	}
	addEdge("Observation", "o5", "pa")["auth_resource_path"] = "/blocked"
	addEdge("Observation", "o5", "pa")["dataset_generation"] = "other-generation"
	addEdge("Observation", "o5", "pa")["project"] = project + "_other"
	insert("fhir_edge", edges)

	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}, IncludeRowIdentity: true}
	output := relatedGroupPreviewOutput(t, bindings)
	compiled := lowerConstructionOutput(t, output, bindings)
	full, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if full.PartialValidation {
		t.Fatal("full execution must validate the complete source")
	}
	want := executeReshapeOracleQuery(t, ctx, client, full)
	sort.Slice(want, func(i, j int) bool { return want[i]["__loom_row_id"].(string) < want[j]["__loom_row_id"].(string) })
	wantCounts := map[any][2]int{nil: {3, 0}, "\"quoted": {1, 0}, "a": {4, 5}, "b": {4, 5}, "z": {1, 0}}
	if len(want) != len(wantCounts) {
		t.Fatalf("canonical rows = %#v", want)
	}
	for _, row := range want {
		counts, ok := wantCounts[row["patient_group"]]
		if !ok || !constructionNumericEqual(row["rows"], float64(counts[0])) || !constructionNumericEqual(row["summary"], float64(counts[1])) {
			t.Fatalf("independent contributor/count oracle differs: %#v", row)
		}
	}
	comparePreview := func(t *testing.T, limit int) {
		t.Helper()
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		rows := executeReshapeOracleQuery(t, ctx, client, query)
		end := min(limit, len(want))
		if !reflect.DeepEqual(rows, want[:end]) {
			t.Fatalf("limit %d changed complete groups, counts, identity, or contributors:\ngot %#v\nwant %#v", limit, rows, want[:end])
		}
	}
	for _, limit := range []int{1, 2, 3, 4, 100} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) { comparePreview(t, limit) })
	}
	t.Run("terminal_group", func(t *testing.T) {
		terminalOutput := relatedGroupPreviewOutput(t, bindings)
		terminalOutput.Construction.Steps = terminalOutput.Construction.Steps[:3]
		terminal := lowerConstructionOutput(t, terminalOutput, bindings)
		fullQuery, err := CompileRecipeOutputWithPolicy(terminal, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		fullRows := executeReshapeOracleQuery(t, ctx, client, fullQuery)
		sort.Slice(fullRows, func(i, j int) bool {
			return fullRows[i]["__loom_row_id"].(string) < fullRows[j]["__loom_row_id"].(string)
		})
		previewQuery, err := CompileRecipeOutputWithPolicy(terminal, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		previewRows := executeReshapeOracleQuery(t, ctx, client, previewQuery)
		if !reflect.DeepEqual(previewRows, fullRows[:2]) {
			t.Fatalf("terminal Group changed complete groups: got %#v, want %#v", previewRows, fullRows[:2])
		}
	})

	unreferenced := make([]map[string]any, 0, 20)
	for i := range 20 {
		unreferenced = append(unreferenced, resource("Patient", fmt.Sprintf("unused_%d", i), fmt.Sprintf("!unused_%02d", i)))
	}
	insert("Patient", unreferenced)
	t.Run("sparse_candidate_prefix", func(t *testing.T) { comparePreview(t, 2) })
}

func relatedGroupPreviewOutput(t *testing.T, bindings recipe.RuntimeBindings) recipe.Output {
	t.Helper()
	sourceColumns := []recipe.StageColumn{{ID: "specimen", Name: "specimen_id", Type: "string"}}
	patientColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{ID: "patient", Name: "patient_id", Type: "string", Nullable: true})
	patientRoute := recipe.ConstructionRelatedRouteStep{EdgeID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node",
		FromResourceType: "Specimen", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"}
	observationRoute := recipe.ConstructionRelatedRouteStep{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}
	output := recipe.Output{Name: "related_group_preview", RootResourceType: "Specimen", RowGrain: "specimen", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{{
			ID: "expand_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "patient-route", TargetNodeID: "patient-node", TargetResourceType: "Patient",
				Route: []recipe.ConstructionRelatedRouteStep{patientRoute}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "patient"}},
			Outputs: patientColumns,
		}}}}
	prefix := lowerConstructionOutput(t, output, bindings)
	patientAnchor := ""
	for _, column := range prefix.OutputSchema {
		if column.RelatedRecordAnchor != nil && column.RelatedRecordAnchor.TargetResourceType == "Patient" {
			patientAnchor = column.ID
			break
		}
	}
	if patientAnchor == "" {
		t.Fatal("Patient expansion must retain an exact document anchor")
	}
	observationColumns := append(append([]recipe.StageColumn(nil), patientColumns...), recipe.StageColumn{ID: "observation", Name: "observation_id", Type: "string", Nullable: true})
	groupColumns := []recipe.StageColumn{{ID: "patient-group", Name: "patient_group", Type: "string", Nullable: true}, {ID: "rows", Name: "rows", Type: "integer"}}
	output.Construction.Steps = append(output.Construction.Steps,
		recipe.ConstructionStep{ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_patients"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: patientAnchor, ChoiceID: "observation-route", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{observationRoute}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation"}}, Outputs: observationColumns},
		recipe.ConstructionStep{ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group",
				Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "patient", OutputColumnID: "patient-group"}},
				Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}}}}, Outputs: groupColumns},
		recipe.ConstructionStep{ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "__loom_row_id", ChoiceID: "summary-route", SourceOccurrenceID: "observation-node", Form: "COUNT", OutputColumnID: "summary", ContributorPolicy: "ALL_MATCHES",
				Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-id", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.id", LogicalType: "string", Cardinality: "required_one"},
				Route:  []recipe.ConstructionRelatedRouteStep{patientRoute, observationRoute}}},
			Outputs: append(append([]recipe.StageColumn(nil), groupColumns...), recipe.StageColumn{ID: "summary", Name: "summary", Type: "integer"})},
	)
	return output
}

func TestSemanticGroupScalarKeyPreviewFrontierActivatesOnCompiledPlan(t *testing.T) {
	const targetSystem, targetVersion, codingPath = "urn:loom:semantic-group-preview", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupFixtureBinding(t, targetSystem, targetVersion, codingPath)
	bindings := recipe.RuntimeBindings{Project: "semantic-group-preview-unit", DatasetGeneration: "generation-unit"}
	output := semanticGroupArangoOutput(
		t, recipe.ConstructionGroupMissingKeyGroup, bindingID, familyID, ruleVersion,
		targetSystem, targetVersion, codingPath,
	)
	compiled := lowerConstructionOutput(t, output, bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group preview: %v", err)
	}
	if strings.Contains(preview.Query, "group_preview_selected_root_keys") {
		if strings.Count(preview.Query, "root._key") < 2 || !strings.Contains(preview.Query, "group_preview_selected_root_keys") {
			t.Fatalf("frontier did not retain and replay the direct root identity projection:\n%s", preview.Query)
		}
		return
	}
	t.Fatalf("compiled semantic Group plan did not enter its proven frontier:\n%s", preview.Query)
}

func TestSemanticGroupScalarKeyPreviewMatchesFullQueryAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)

	suffix := uuid.NewString()
	project, generation := "loom_semantic_group_preview_"+suffix, "generation_"+suffix
	const targetSystem, targetVersion, codingPath = "urn:loom:semantic-group-preview", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupFixtureBinding(t, targetSystem, targetVersion, codingPath)
	fixtureIDs := []string{
		"null_missing_status", "null_explicit_status", "active_first", "active_second", "active_missing", "active_wrong_version",
		"later_scalar", "z_last_one", "z_last_two", "denied_active", "foreign_project", "foreign_generation", "late_malformed_status",
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR key IN @keys REMOVE key IN Observation",
			map[string]any{"keys": semanticGroupPreviewFixtureKeys(project, fixtureIDs)},
		); err != nil {
			t.Errorf("remove semantic Group preview resources: %v", err)
		}
	})

	allowed := func(id, status string, components ...any) map[string]any {
		return map[string]any{
			"id": id, "status": status, "auth_resource_path": "/allowed", "component": components,
		}
	}
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "null_missing_status", "auth_resource_path": "/allowed"},
		{"id": "null_explicit_status", "status": nil, "auth_resource_path": "/allowed"},
		allowed("active_first", "active",
			semanticGroupComponent(targetSystem, targetVersion, "shared", "Z shared"),
			semanticGroupComponent(targetSystem, targetVersion, "shared", "A shared"),
			semanticGroupComponent(targetSystem, targetVersion, "shared", "A shared"),
			semanticGroupComponent(targetSystem, targetVersion, "other", "A shared")),
		allowed("active_second", "active", semanticGroupComponent(targetSystem, targetVersion, "shared", "Shared")),
		allowed("active_missing", "active"),
		allowed("active_wrong_version", "active", semanticGroupComponent(targetSystem, "v2", "wrong", "Wrong version")),
		allowed("later_scalar", "preliminary", semanticGroupComponent(targetSystem, targetVersion, "later", "Later")),
		allowed("z_last_one", "zz-last-one", semanticGroupComponent(targetSystem, targetVersion, "last-one", "Last one")),
		allowed("z_last_two", "zz-last-two", semanticGroupComponent(targetSystem, targetVersion, "last-two", "Last two")),
		{
			"id": "denied_active", "status": "active", "auth_resource_path": "/blocked",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, "shared", "Denied")},
		},
	})
	insertSemanticGroupPreviewForeignRows(t, ctx, client, project, generation, targetSystem, targetVersion)

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	output := semanticGroupArangoOutput(
		t, recipe.ConstructionGroupMissingKeyGroup, bindingID, familyID, ruleVersion,
		targetSystem, targetVersion, codingPath,
	)
	compiled := lowerConstructionOutput(t, output, bindings)
	const previewLimit = 4
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, previewLimit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group preview: %v", err)
	}
	full, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile full semantic Group query: %v", err)
	}
	if !strings.Contains(preview.Query, "group_preview_selected_root_keys") {
		t.Fatalf("semantic Group preview did not activate the direct-root key frontier:\n%s", preview.Query)
	}
	if strings.Count(preview.Query, "FILTER root.project == @project") != 2 ||
		strings.Count(preview.Query, "FILTER root.dataset_generation == @dataset_generation") != 2 ||
		strings.Count(preview.Query, "root.auth_resource_path IN @auth_resource_paths") != 2 {
		t.Fatalf("frontier or selected-root replay dropped a source/auth predicate:\n%s", preview.Query)
	}
	if preview.PreviewCoveringIndex != nil {
		hinted, _, err := RenderPreviewPhysicalPlanWithRootIndexHint(preview.PhysicalPlan, preview.PreviewCoveringIndex.Name)
		if err != nil {
			t.Fatalf("render semantic Group frontier with the non-forcing preview index hint: %v", err)
		}
		if !strings.Contains(hinted, "OPTIONS { indexHint: \""+preview.PreviewCoveringIndex.Name+"\", forceIndexHint: false }") ||
			!strings.Contains(hinted, "group_preview_selected_root_keys") {
			t.Fatalf("non-forcing preview hint disabled the semantic Group frontier:\n%s", hinted)
		}
	}

	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) <= previewLimit || len(previewRows) != previewLimit || !reflect.DeepEqual(previewRows, fullRows[:previewLimit]) {
		t.Fatalf("semantic Group preview rows = %#v, want first %d exact full-query rows %#v", previewRows, previewLimit, fullRows)
	}
	if previewRows[0]["status"] != nil || previewRows[1]["status"] != "active" ||
		previewRows[2]["status"] != "active" || previewRows[3]["status"] != "active" {
		t.Fatalf("first compound groups must preserve null scalar ordering and the active scalar population: %#v", previewRows)
	}
	if previewRows[0]["category"] != nil || previewRows[1]["category"] != nil ||
		previewRows[2]["category"] != "A shared" || previewRows[3]["category"] != "A shared" ||
		!constructionNumericEqual(previewRows[0]["rows"], 2) || !constructionNumericEqual(previewRows[1]["rows"], 2) ||
		!constructionNumericEqual(previewRows[2]["rows"], 1) || !constructionNumericEqual(previewRows[3]["rows"], 2) {
		t.Fatalf("semantic membership counts lost the per-root dedupe or missing-family sentinel: %#v", previewRows)
	}
	firstCodeID, _ := previewRows[2]["__loom_row_id"].(string)
	secondCodeID, _ := previewRows[3]["__loom_row_id"].(string)
	if firstCodeID == "" || secondCodeID == "" || firstCodeID == secondCodeID {
		t.Fatalf("same-display code tuples do not have distinct identities: %#v", previewRows)
	}
	uniqueScalarKeys := make(map[string]struct{})
	for _, row := range fullRows {
		key, _ := row["status"].(string)
		if row["status"] == nil {
			key = "<null>"
		}
		uniqueScalarKeys[key] = struct{}{}
	}
	if len(uniqueScalarKeys) <= previewLimit {
		t.Fatalf("fixture must have more scalar keys than its preview limit %d: %#v", previewLimit, uniqueScalarKeys)
	}

	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{{
		"id": "late_malformed_status", "status": map[string]any{"unexpected": "object"},
		"auth_resource_path": "/allowed",
		"component":          []any{semanticGroupComponent(targetSystem, targetVersion, "late", "Late")},
	}})
	for _, limit := range []int{previewLimit, 0} {
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile semantic Group query with malformed scalar key and limit %d: %v", limit, err)
		}
		if err := client.QueryRows(ctx, query.Query, 100, query.BindVars, func(map[string]any) error { return nil }); err == nil {
			t.Fatalf("semantic Group with limit %d hid the malformed key outside its selected page", limit)
		}
	}
}

func semanticGroupPreviewFixtureKeys(project string, ids []string) []string {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, project+"_"+id)
	}
	return keys
}

func insertSemanticGroupPreviewForeignRows(
	t *testing.T,
	ctx context.Context,
	client interface {
		InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
	},
	project, generation, targetSystem, targetVersion string,
) {
	t.Helper()
	row := func(id, resourceProject, resourceGeneration, code string) json.RawMessage {
		t.Helper()
		payload := map[string]any{
			"id": id, "resourceType": "Observation", "status": "active",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, code, "A shared")},
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + id, "id": id, "resourceType": "Observation",
			"project": resourceProject, "project_id": resourceProject,
			"dataset_generation": resourceGeneration, "auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatalf("encode semantic Group preview foreign-scope row: %v", err)
		}
		return document
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{
		row("foreign_project", project+"_foreign", generation, "shared"),
		row("foreign_generation", project, generation+"_old", "shared"),
	}, false, "document"); err != nil {
		t.Fatalf("insert semantic Group preview foreign-scope rows: %v", err)
	}
}
