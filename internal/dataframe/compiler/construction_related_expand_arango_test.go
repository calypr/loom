package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

func TestRelatedSourceProjectionLivenessForKeylessCount(t *testing.T) {
	for _, test := range []struct {
		name        string
		groupByFlag bool
		wantMarker  bool
	}{
		{name: "unused before related expansion", wantMarker: false},
		{name: "retained when grouped", groupByFlag: true, wantMarker: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := compileRelatedExpandCountQuery(t, relatedExpandCountOutput(test.groupByFlag))
			for _, marker := range []string{"unused_count_column_marker", "unused_list_column_marker", "unused_presence_column_marker"} {
				gotMarker := strings.Contains(query.Query, marker)
				if gotMarker != test.wantMarker {
					t.Errorf("related source projection %q included = %t, want %t:\n%s", marker, gotMarker, test.wantMarker, query.Query)
				}
			}
			if !strings.Contains(query.Query, "related_expand_records") || !strings.Contains(query.Query, "WITH COUNT INTO") {
				t.Fatalf("query must retain related expansion and row counting:\n%s", query.Query)
			}
		})
	}
}

func TestRelatedExpandDistinctTerminalRowsAndEmptyPoliciesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_related_expand_"+uuid.NewString(), "generation-related-expand"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-expansion fixtures from %s: %v", collection, err)
			}
		}
	}()

	document := func(key, resourceType, docGeneration string, payload map[string]any) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "id": payload["id"], "project": project, "project_id": project,
			"dataset_generation": docGeneration, "resourceType": resourceType, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	patients := []json.RawMessage{
		document(project+"_p1", "Patient", generation, map[string]any{"id": "p1", "resourceType": "Patient", "active": true}),
		document(project+"_p2", "Patient", generation, map[string]any{"id": "p2", "resourceType": "Patient", "active": true}),
		document(project+"_p3", "Patient", generation, map[string]any{"id": "p3", "resourceType": "Patient", "active": false}),
	}
	observations := []json.RawMessage{
		document(project+"_o1", "Observation", generation, map[string]any{"id": "o1", "resourceType": "Observation", "status": "registered"}),
		document(project+"_o2", "Observation", generation, map[string]any{"id": "o2", "resourceType": "Observation", "status": "final"}),
	}
	for collection, documents := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert related-expansion fixtures into %s: %v", collection, err)
		}
	}
	edges := make([]json.RawMessage, 0, 3)
	for index, observationID := range []string{"o1", "o1", "o2"} {
		encoded, err := json.Marshal(map[string]any{
			"_key":    fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from":   "Observation/" + project + "_" + observationID,
			"_to":     "Patient/" + project + "_p1",
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert related-expansion edges: %v", err)
	}

	for _, policy := range []recipe.ExpansionEmptyPolicy{recipe.ExpansionExclude, recipe.ExpansionPreserveParent} {
		t.Run(string(policy), func(t *testing.T) {
			compiled, query := compileRelatedExpandOracleQuery(t, relatedExpandOracleOutput(policy), project, generation)
			started := time.Now()
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			t.Logf("RELATED_EXPAND %s query returned %d rows in %s", policy, len(rows), time.Since(started))
			ids := make(map[string]bool, len(rows))
			byParent := make(map[string][]string)
			for _, row := range rows {
				parent, _ := row["patient_id"].(string)
				rowID, _ := row["__loom_row_id"].(string)
				if rowID == "" || ids[rowID] {
					t.Errorf("related row identity is missing or duplicated: %#v", row["__loom_row_id"])
				}
				ids[rowID] = true
				if row["observation_id"] == nil {
					byParent[parent] = append(byParent[parent], "<empty>")
				} else {
					byParent[parent] = append(byParent[parent], fmt.Sprint(row["observation_id"]))
				}
			}
			if policy == recipe.ExpansionExclude {
				if len(rows) != 2 || len(byParent["p1"]) != 2 || len(byParent["p2"]) != 0 || len(byParent["p3"]) != 0 {
					t.Fatalf("EXCLUDE rows = %#v, want two unique p1 terminals after the active filter", rows)
				}
				if byParent["p1"][0] == byParent["p1"][1] || !(byParent["p1"][0] == "o1" || byParent["p1"][1] == "o1") || !(byParent["p1"][0] == "o2" || byParent["p1"][1] == "o2") {
					t.Fatalf("EXCLUDE related IDs = %#v, want distinct FHIR IDs o1 and o2 despite duplicate paths", byParent["p1"])
				}
			} else if len(rows) != 3 || len(byParent["p1"]) != 2 || len(byParent["p2"]) != 1 || byParent["p2"][0] != "<empty>" || len(byParent["p3"]) != 0 {
				t.Fatalf("PRESERVE_PARENT rows = %#v, want two p1 terminals and a distinct null p2 row after filtering", rows)
			}
			if len(compiled.Stages) < 3 || compiled.Stages[1].ID != "filter_active" || compiled.Stages[2].RelatedExpand == nil {
				t.Fatalf("related expansion was not compiled from the selected filtered stage: %#v", compiled.Stages)
			}
			repeated := executeReshapeOracleQuery(t, ctx, client, query)
			if !sameConstructionRowIdentities(rows, repeated) {
				t.Fatalf("repeated related-expansion row identities = %#v, want %#v", constructionRowIdentities(repeated), constructionRowIdentities(rows))
			}
		})
	}

	t.Run(string(recipe.ExpansionError), func(t *testing.T) {
		_, query := compileRelatedExpandOracleQuery(t, relatedExpandOracleOutput(recipe.ExpansionError), project, generation)
		started := time.Now()
		err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
		t.Logf("RELATED_EXPAND %s query rejected an empty route in %s", recipe.ExpansionError, time.Since(started))
		if err == nil {
			t.Fatalf("ERROR policy returned rows despite active p2 having no related resource")
		}
		t.Logf("ERROR policy result: %v", err)
	})
}

func TestRelatedFieldReadsExactExpandedRecordAfterFilterAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_related_field_"+uuid.NewString(), "generation-related-field"
	key := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-field fixtures from %s: %v", collection, err)
			}
		}
	}()
	document := func(id, resourceType, docGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": docGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	patients := []json.RawMessage{
		document("p1", "Patient", generation, "/allowed", map[string]any{"id": "p1", "resourceType": "Patient", "active": true}),
		document("p2", "Patient", generation, "/allowed", map[string]any{"id": "p2", "resourceType": "Patient", "active": true}),
		document("p3", "Patient", generation, "/allowed", map[string]any{"id": "p3", "resourceType": "Patient", "active": false}),
	}
	observations := []json.RawMessage{
		document("o1", "Observation", generation, "/allowed", map[string]any{"id": "o1", "resourceType": "Observation", "status": "registered"}),
		document("o2", "Observation", generation, "/forbidden", map[string]any{"id": "o2", "resourceType": "Observation", "status": "denied-scope"}),
		document("o3", "Observation", "generation-older", "/allowed", map[string]any{"id": "o3", "resourceType": "Observation", "status": "stale-generation"}),
	}
	for collection, rows := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations} {
		if err := client.InsertBatchRaw(ctx, collection, rows, false, "document"); err != nil {
			t.Fatalf("insert related-field fixtures into %s: %v", collection, err)
		}
	}
	edges := make([]json.RawMessage, 0, 3)
	for index, observationID := range []string{"o1", "o2", "o3"} {
		encoded, err := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from": "Observation/" + key(observationID), "_to": "Patient/" + key("p1"),
			"project": project, "project_id": project, "dataset_generation": generation,
			"auth_resource_path": "/allowed", "label": "subject_Patient",
			"from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert related-field edges: %v", err)
	}

	active := true
	sourceColumns := []recipe.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "patient-active", Name: "patient_active", Label: "Active"},
	}
	expandedColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true,
	})
	fieldColumns := append(append([]recipe.StageColumn(nil), expandedColumns...), recipe.StageColumn{
		ID: "observation-status", Name: "observation_status", Label: "Observation status", Type: "string", Nullable: true,
	})
	output := recipe.Output{
		Name: "related_field_oracle", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation-id",
				}}, Outputs: expandedColumns,
			},
			{
				ID: "keep_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-active", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &active}},
				}}, Outputs: expandedColumns,
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
					ChoiceID: "exact-observation-status-choice", OutputColumnID: "observation-status",
					Source: recipe.ConstructionRelatedFieldSource{
						CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
						Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
					},
				}}, Outputs: fieldColumns,
			},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/allowed"},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"related_field_document.project == @project",
		"related_field_document.dataset_generation == @dataset_generation",
		"related_field_document.auth_resource_path IN @auth_resource_paths",
	} {
		if !strings.Contains(query.Query, required) {
			t.Fatalf("exact field lookup omits scope guard %q: %s", required, query.Query)
		}
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	byPatient := make(map[string]map[string]any, len(rows))
	rowIDs := make(map[string]bool, len(rows))
	for _, row := range rows {
		patientID, _ := row["patient_id"].(string)
		byPatient[patientID] = row
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || rowIDs[rowID] {
			t.Errorf("related-field row identity is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[rowID] = true
	}
	if len(byPatient) != 2 || len(rowIDs) != 2 {
		t.Fatalf("related-field rows = %#v, want active p1 and preserved empty p2 only", rows)
	}
	if byPatient["p1"]["observation_id"] != "o1" || byPatient["p1"]["observation_status"] != "registered" {
		t.Fatalf("p1 exact related field = %#v, want authorized current-generation Observation o1", byPatient["p1"])
	}
	if byPatient["p2"]["observation_id"] != nil || byPatient["p2"]["observation_status"] != nil {
		t.Fatalf("PRESERVE_PARENT empty related field = %#v, want null observation ID and status", byPatient["p2"])
	}
	for _, forbidden := range []string{"denied-scope", "stale-generation"} {
		for _, row := range rows {
			if row["observation_status"] == forbidden {
				t.Fatalf("related field returned %q from a mismatched target document: %#v", forbidden, row)
			}
		}
	}
}

func relatedExpandOracleOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	trueValue := true
	columns := []recipe.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "patient-active", Name: "patient_active", Label: "Active"},
	}
	filteredColumns := append([]recipe.StageColumn(nil), columns...)
	finalColumns := append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: emptyPolicy == recipe.ExpansionPreserveParent,
	})
	return recipe.Output{
		Name: "related_expand_oracle", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "filter_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-active", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &trueValue}},
				}}, Outputs: filteredColumns,
			},
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: emptyPolicy, RelatedRecordColumnID: "observation-id",
				}}, Outputs: finalColumns,
			},
		}},
	}
}

func compileRelatedExpandOracleQuery(t *testing.T, output recipe.Output, project, generation string) (lower.CompiledRecipeOutput, CompiledQuery) {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related-expansion recipe plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve related-expansion recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related-expansion recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related-expansion query: %v", err)
	}
	return compiled.Outputs[0], query
}

func relatedExpandCountOutput(groupByPresence bool) recipe.Output {
	sourceField := recipe.Field{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}}
	sourceColumn := recipe.StageColumn{ID: sourceField.ColumnID, Name: sourceField.Name}
	countColumn := recipe.StageColumn{ID: "count-id", Name: "unused_count_column_marker", Type: "integer"}
	listColumn := recipe.StageColumn{ID: "list-id", Name: "unused_list_column_marker", Type: "string"}
	presenceColumn := recipe.StageColumn{ID: "presence-id", Name: "unused_presence_column_marker", Type: "boolean"}
	countColumns := []recipe.StageColumn{sourceColumn, countColumn}
	listColumns := append(append([]recipe.StageColumn(nil), countColumns...), listColumn)
	relatedSourceColumns := append(append([]recipe.StageColumn(nil), listColumns...), presenceColumn)
	expandedColumns := append(append([]recipe.StageColumn(nil), relatedSourceColumns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Type: "string",
	})
	group := &recipe.ConstructionGroup{
		ConstructionID: "count_expanded_rows",
		Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row-count-id"}},
	}
	groupOutputs := []recipe.StageColumn{{ID: "row-count-id", Name: "row_count", Type: "integer"}}
	if groupByPresence {
		group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: presenceColumn.ID, OutputColumnID: "presence-group-id"}}
		groupOutputs = append([]recipe.StageColumn{{ID: "presence-group-id", Name: "presence_group", Type: "boolean"}}, groupOutputs...)
	}
	statusSource := recipe.ConstructionRelatedFieldSource{
		CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
		Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
	}
	observationRoute := []recipe.ConstructionRelatedRouteStep{{
		EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	relatedSourceStep := func(id, inputStep, form string, output recipe.StageColumn, outputs []recipe.StageColumn) recipe.ConstructionStep {
		input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
		if inputStep != "" {
			input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: inputStep}
		}
		return recipe.ConstructionStep{
			ID: id, Inputs: []recipe.ConstructionInputRef{input},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: id + "-route", SourceOccurrenceID: "observation-node",
				Source: statusSource, Route: observationRoute, ContributorPolicy: "ALL_MATCHES",
				Form: form, OutputColumnID: output.ID,
			}},
			Outputs: outputs,
		}
	}
	return recipe.Output{
		Name: "related_expand_count", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{sourceField},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{sourceColumn},
			Steps: []recipe.ConstructionStep{
				relatedSourceStep("count", "", "COUNT", countColumn, countColumns),
				relatedSourceStep("list", "count", "ALL", listColumn, listColumns),
				relatedSourceStep("presence", "list", "PRESENCE", presenceColumn, relatedSourceColumns),
				{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "presence"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "_key", ChoiceID: "observation-route", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "observation-id",
					}},
					Outputs: expandedColumns,
				},
				{
					ID: "count_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: group}, Outputs: groupOutputs,
				},
			},
		},
	}
}

func compileRelatedExpandCountQuery(t *testing.T, output recipe.Output) CompiledQuery {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: "related-expand-count-project", DatasetGeneration: "related-expand-count-generation",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related expansion count recipe: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("resolve related expansion count recipe: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related expansion count recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("render related expansion count recipe: %v", err)
	}
	return query
}
