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
	}{
		{"related_id_projection", func(plan *ir.PhysicalPlan) {
			stage := &plan.StageSequence.Stages[0]
			for i := range stage.OutputProjections {
				if stage.OutputProjections[i].Name == "patient_id" {
					stage.OutputProjections[i].Value.Path = []string{"terminal_id"}
				}
			}
		}, true},
		{"root_key_projection", func(plan *ir.PhysicalPlan) {
			returned := plan.Operations[len(plan.Operations)-1].Return
			for i := range returned.Projections {
				if returned.Projections[i].Name == "_key" {
					returned.Projections[i].Value.Path = []string{"payload", "id"}
				}
			}
		}, false},
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
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			bindings := recipe.RuntimeBindings{Project: "owned-project", DatasetGeneration: "owned-generation", IncludeRowIdentity: true}
			compiled := lowerConstructionOutput(t, relatedGroupPreviewOutput(t, bindings), bindings)
			test.change(&compiled.Plan)
			preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
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
	// Canonical forward traversal does not check this redundant source-type field.
	// A reverse optimization must not introduce that extra predicate.
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
	// A candidate prefix containing only unreferenced Patients must fall back;
	// it must never turn a nonempty dataframe into an empty preview.
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
