package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
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

func TestConstructionPivotDeriveFilterUnpivotRowsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	var sourceRows []reshapeOracleSourceRow
	for _, row := range reshapeOracleSourceRows {
		switch row.SourceID {
		case "alpha-a", "alpha-b", "beta", "final-two":
			sourceRows = append(sourceRows, row)
		}
	}
	project := "loom_construction_chain_" + uuid.NewString()
	generation := "generation-construction-chain"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove construction fixtures: %v", err)
		}
	}()
	documents := make([]json.RawMessage, 0, len(sourceRows))
	for _, row := range sourceRows {
		payload := map[string]any{
			"id": row.SourceID, "resourceType": "Observation", "status": *row.Group.Text.String,
			"valueInteger": *row.NumericCategory.Integer, "valueString": row.Text,
			"code":         map[string]any{"text": *row.StringCategory.String},
			"valueBoolean": row.Flag, "issued": row.Unrelated,
		}
		if row.Number != nil {
			payload["valueQuantity"] = map[string]any{"value": *row.Number}
		} else {
			payload["valueQuantity"] = map[string]any{"value": nil}
		}
		document, marshalErr := json.Marshal(map[string]any{
			"_key": project + "_" + row.SourceID, "id": row.SourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction fixture: %v", err)
	}

	output := constructionOracleOutput()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
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
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 2 {
		t.Fatalf("construction chain rows = %#v, want alpha and beta rows from the one qualifying intermediate group", rows)
	}
	wantAmounts := map[string]float64{"alpha": 7, "beta": 3.5}
	rowIDs := map[string]bool{}
	for _, row := range rows {
		measure, ok := row["measure"].(string)
		if !ok {
			t.Fatalf("unpivot key = %#v, want string", row["measure"])
		}
		wantAmount, ok := wantAmounts[measure]
		if !ok || !constructionNumericEqual(row["amount"], wantAmount) {
			t.Errorf("unpivot amount for %q = %#v, want %v", measure, row["amount"], wantAmount)
		}
		if row["group_text"] != "final" || !constructionNumericEqual(row["group_number"], 1) || !constructionNumericEqual(row["total"], 10.5) {
			t.Errorf("intermediate group/derive values = %#v, want final / 1 / 10.5", row)
		}
		id, ok := row["__loom_row_id"].(string)
		if !ok || id == "" || rowIDs[id] {
			t.Errorf("unpivot row identity is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[id] = true
	}
	if len(rowIDs) != 2 {
		t.Fatalf("unpivot identities = %#v, want two distinct rows", rowIDs)
	}
}

func TestRelatedSourceAllMatchesAtSelectedStageAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_related_source_" + uuid.NewString()
	generation := "generation-related-source"
	patientKey := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-source fixtures from %s: %v", collection, err)
			}
		}
	}()
	document := func(key, resourceType, docGeneration string, payload map[string]any) json.RawMessage {
		t.Helper()
		encoded, marshalErr := json.Marshal(map[string]any{
			"_key": key, "id": payload["id"], "project": project, "project_id": project,
			"dataset_generation": docGeneration, "resourceType": resourceType, "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return encoded
	}
	patients := []json.RawMessage{
		document(patientKey("p1"), "Patient", generation, map[string]any{
			"id": "p1", "resourceType": "Patient", "active": true, "gender": "female", "birthDate": "1980-01-02",
			"deceasedBoolean": false, "multipleBirthBoolean": false,
			"name": []any{map[string]any{"family": "Patient One"}}, "telecom": []any{map[string]any{"value": "one@example.test"}},
		}),
		document(patientKey("p2"), "Patient", generation, map[string]any{
			"id": "p2", "resourceType": "Patient", "active": false, "gender": "male", "birthDate": "1985-03-04",
			"deceasedBoolean": false, "multipleBirthBoolean": false,
			"name": []any{map[string]any{"family": "Patient Two"}}, "telecom": []any{map[string]any{"value": "two@example.test"}},
		}),
	}
	observations := []json.RawMessage{
		document(patientKey("o1"), "Observation", generation, map[string]any{"id": "o1", "resourceType": "Observation", "status": "registered"}),
		document(patientKey("o2"), "Observation", generation, map[string]any{"id": "o2", "resourceType": "Observation", "status": "cancelled"}),
		document(patientKey("o3"), "Observation", "generation-older", map[string]any{"id": "o3", "resourceType": "Observation", "status": "stale-generation"}),
		document(patientKey("o4"), "Observation", generation, map[string]any{"id": "o4", "resourceType": "Observation"}),
	}
	edges := make([]json.RawMessage, 0, len(observations))
	for index, observationID := range []string{"o1", "o2", "o3", "o4"} {
		encoded, marshalErr := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from": "Observation/" + patientKey(observationID), "_to": "Patient/" + patientKey("p1"),
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		edges = append(edges, encoded)
	}
	for collection, rows := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations, "fhir_edge": edges} {
		if err := client.InsertBatchRaw(ctx, collection, rows, false, "document"); err != nil {
			t.Fatalf("insert related-source fixtures into %s: %v", collection, err)
		}
	}

	sourceFields := []recipe.Field{
		{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		{Name: "patient_gender", ColumnID: "patient-gender", Label: "Gender", Expr: recipe.Expression{Select: "root.gender"}},
		{Name: "patient_birth_date", ColumnID: "patient-birth-date", Label: "Birth date", Expr: recipe.Expression{Select: "root.birthDate"}},
		{Name: "patient_deceased", ColumnID: "patient-deceased", Label: "Deceased", Expr: recipe.Expression{Select: "root.deceasedBoolean"}},
		{Name: "patient_multiple_birth", ColumnID: "patient-multiple-birth", Label: "Multiple birth", Expr: recipe.Expression{Select: "root.multipleBirthBoolean"}},
		{Name: "patient_family", ColumnID: "patient-family", Label: "Family name", Expr: recipe.Expression{Select: "root.name[].family"}, ValueMode: recipe.ValueModeFirst},
		{Name: "patient_telecom", ColumnID: "patient-telecom", Label: "Telecom", Expr: recipe.Expression{Select: "root.telecom[].value"}, ValueMode: recipe.ValueModeFirst},
	}
	sourceColumns := make([]recipe.StageColumn, 0, len(sourceFields))
	for _, field := range sourceFields {
		sourceColumns = append(sourceColumns, recipe.StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
	}
	relatedColumns := append([]recipe.StageColumn(nil), sourceColumns...)
	relatedColumns = append(relatedColumns, recipe.StageColumn{ID: "observation-status", Name: "observation_status", Label: "Observation statuses"})
	output := recipe.Output{
		Name: "related_source_oracle", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           sourceFields,
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{
			{
				ID: "keep_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "patient-id", Operator: recipe.FilterExists}},
				Outputs:   sourceColumns,
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_patients"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "route-choice-test", SourceOccurrenceID: "observation-node",
					Source: recipe.ConstructionRelatedFieldSource{
						CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
						Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
					},
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorPolicy: "ALL_MATCHES",
					Predicate: &recipe.ConstructionRelatedPredicate{
						CandidateID: "observation-status", Operator: recipe.FilterEquals,
						Value: &recipe.FilterValue{Kind: recipe.FilterString, String: stringPtr("registered")},
					},
					Form: "ALL", OutputColumnID: "observation-status",
				}},
				Outputs: relatedColumns,
			},
		}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "related-source-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted}
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
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	byPatient := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byPatient[fmt.Sprint(row["patient_id"])] = row
	}
	if len(byPatient) != 2 {
		t.Fatalf("related-source rows = %#v, want both Patient roots", rows)
	}
	gotStatuses, ok := byPatient["p1"]["observation_status"].([]any)
	if !ok || len(gotStatuses) != 1 {
		t.Fatalf("p1 observation statuses = %#v, want only the matching related row", byPatient["p1"]["observation_status"])
	}
	statusSet := map[string]int{}
	for _, status := range gotStatuses {
		statusSet[fmt.Sprint(status)]++
	}
	if statusSet["registered"] != 1 || statusSet["cancelled"] != 0 || statusSet["stale-generation"] != 0 {
		t.Fatalf("p1 statuses = %#v, want only registered", gotStatuses)
	}
	gotSparse, ok := byPatient["p2"]["observation_status"].([]any)
	if !ok || len(gotSparse) != 0 {
		t.Fatalf("sparse p2 observation statuses = %#v, want empty list", byPatient["p2"]["observation_status"])
	}
	trace, err := CompileCellTraceOutputWithPolicy(compiled.Outputs[0], "observation_status", 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	traceRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: trace.Query, BindVars: trace.BindVars})
	traces := make(map[string]map[string]any, len(traceRows))
	for _, row := range traceRows {
		parts, ok := row[trace.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("related-source trace identity = %#v, want one retained root key", row[trace.IdentityPartsColumn])
		}
		traces[fmt.Sprint(parts[0])] = row
	}
	for patient, expected := range map[string]struct {
		status string
		count  int
	}{patientKey("p1"): {"VALUE", 1}, patientKey("p2"): {"NO_MATCH", 0}} {
		row := traces[patient]
		if row == nil || row[trace.StatusColumn] != expected.status || row[trace.OmissionColumn] != "" {
			t.Fatalf("%s related-source trace = %#v, want status %q with no omission", patient, row, expected.status)
		}
		contributors, ok := row[trace.ContributionsColumn].([]any)
		if !ok || len(contributors) != expected.count {
			t.Fatalf("%s related-source contributors = %#v, want %d", patient, row[trace.ContributionsColumn], expected.count)
		}
		if patient == patientKey("p1") {
			values := map[string]int{}
			for _, raw := range contributors {
				contribution, ok := raw.(map[string]any)
				if !ok || contribution["resourceType"] != "Observation" || contribution["value"] != "registered" {
					t.Fatalf("p1 related-source contributor = %#v, want Observation", raw)
				}
				values[fmt.Sprint(contribution["resourceId"])]++
			}
			if values["o1"] != 1 || len(values) != 1 {
				t.Fatalf("p1 related-source trace values = %#v, want only the matching source record", values)
			}
		}
	}

	pageSize := 1
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, pageSize, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile paged related-source output: %v", err)
	}
	cloneBinds := func(input map[string]any) map[string]any {
		output := make(map[string]any, len(input))
		for key, value := range input {
			output[key] = value
		}
		return output
	}
	pagedRows := make([]map[string]any, 0, len(patients))
	afterKey := ""
	for pageIndex := 0; pageIndex <= len(patients); pageIndex++ {
		keyBinds := cloneBinds(page.RootKeysBindVars)
		keyBinds[RootPageAfterKeyBind] = afterKey
		rootKeys := make([]string, 0, pageSize)
		if err := client.QueryRows(ctx, page.RootKeysQuery, 100, keyBinds, func(row map[string]any) error {
			key, ok := row["_key"].(string)
			if !ok || key == "" {
				return fmt.Errorf("root-key page returned invalid _key %v", row["_key"])
			}
			rootKeys = append(rootKeys, key)
			return nil
		}); err != nil {
			t.Fatalf("execute related-source root-key page %d: %v\n%s", pageIndex, err, page.RootKeysQuery)
		}
		if len(rootKeys) == 0 {
			break
		}
		if len(rootKeys) > pageSize {
			t.Fatalf("root-key page returned %d keys, want at most %d", len(rootKeys), pageSize)
		}
		rowBinds := cloneBinds(page.RowsBindVars)
		rowBinds[RootPageKeysBind] = rootKeys
		if err := client.QueryRows(ctx, page.RowsQuery, 100, rowBinds, func(row map[string]any) error {
			pagedRows = append(pagedRows, row)
			return nil
		}); err != nil {
			t.Fatalf("execute related-source selected-root page %d: %v\n%s", pageIndex, err, page.RowsQuery)
		}
		afterKey = rootKeys[len(rootKeys)-1]
	}
	pagedByPatient := make(map[string]map[string]any, len(pagedRows))
	for _, row := range pagedRows {
		pagedByPatient[fmt.Sprint(row["patient_id"])] = row
	}
	if len(pagedByPatient) != 2 {
		t.Fatalf("paged related-source rows = %#v, want both Patient roots", pagedRows)
	}
	pagedStatuses, ok := pagedByPatient["p1"]["observation_status"].([]any)
	if !ok || len(pagedStatuses) != 1 {
		t.Fatalf("paged p1 statuses = %#v, want only the matching related row", pagedByPatient["p1"]["observation_status"])
	}
	pagedStatusSet := map[string]int{}
	for _, status := range pagedStatuses {
		pagedStatusSet[fmt.Sprint(status)]++
	}
	if pagedStatusSet["registered"] != 1 || pagedStatusSet["cancelled"] != 0 || pagedStatusSet["stale-generation"] != 0 {
		t.Fatalf("paged p1 statuses = %#v, want only registered", pagedStatuses)
	}
	pagedSparse, ok := pagedByPatient["p2"]["observation_status"].([]any)
	if !ok || len(pagedSparse) != 0 {
		t.Fatalf("paged sparse p2 statuses = %#v, want empty list", pagedByPatient["p2"]["observation_status"])
	}

	duplicateEdge, err := json.Marshal(map[string]any{
		"_key": project + "_duplicate_edge", "_from": "Observation/" + patientKey("o1"), "_to": "Patient/" + patientKey("p1"),
		"project": project, "project_id": project, "dataset_generation": generation,
		"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{duplicateEdge}, false, "document"); err != nil {
		t.Fatal(err)
	}
	for _, form := range []struct {
		name    string
		column  string
		present any
		absent  any
		kind    string
	}{
		{name: "COUNT", column: "observation_count", present: float64(1), absent: float64(0), kind: "integer"},
		{name: "PRESENCE", column: "has_observation", present: true, absent: false, kind: "boolean"},
	} {
		t.Run(form.name, func(t *testing.T) {
			output.Name = "related_source_" + form.name
			step := &output.Construction.Steps[1]
			step.Operation.RelatedSource.Form = form.name
			step.Outputs[len(step.Outputs)-1].Name = form.column
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
			columns := compiled.Outputs[0].OutputSchema
			found := false
			for _, column := range columns {
				if column.Name == form.column {
					found = column.Kind == form.kind && column.Cardinality == "required_one" && !column.Nullable
				}
			}
			if !found {
				t.Fatalf("%s output schema = %#v", form.name, columns)
			}
			query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			values := map[string]any{}
			for _, row := range rows {
				values[fmt.Sprint(row["patient_id"])] = row[form.column]
			}
			if values["p1"] != form.present || values["p2"] != form.absent {
				t.Fatalf("%s values = %#v, want p1=%v p2=%v", form.name, values, form.present, form.absent)
			}
			trace, err := CompileCellTraceOutputWithPolicy(compiled.Outputs[0], form.column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			traceRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: trace.Query, BindVars: trace.BindVars})
			for _, row := range traceRows {
				parts, ok := row[trace.IdentityPartsColumn].([]any)
				if !ok || len(parts) != 1 || fmt.Sprint(parts[0]) != patientKey("p1") {
					continue
				}
				contributors, ok := row[trace.ContributionsColumn].([]any)
				if !ok || len(contributors) != 1 || row[trace.OmissionColumn] != "" {
					t.Fatalf("%s p1 trace = %#v, want only the matching source record", form.name, row)
				}
				return
			}
			t.Fatalf("%s trace omitted p1", form.name)
		})
	}

	related := output.Construction.Steps[1].Operation.RelatedSource
	related.Form = "ALL"
	related.Predicate.Operator = recipe.FilterExists
	related.Predicate.Value = nil
	output.Name = "related_source_exists"
	output.Construction.Steps[1].Outputs[len(output.Construction.Steps[1].Outputs)-1].Name = "observation_status"
	existsBundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	existsPlan, err := semantic.BuildRecipePlan(existsBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	existsResolved, err := semantic.ResolveRecipePlan(existsPlan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	existsCompiled, err := lower.CompileResolvedRecipePlan(existsResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	existsQuery, err := CompileRecipeOutputWithPolicy(existsCompiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	existsRows := executeReshapeOracleQuery(t, ctx, client, existsQuery)
	existsByPatient := make(map[string]map[string]any, len(existsRows))
	for _, row := range existsRows {
		existsByPatient[fmt.Sprint(row["patient_id"])] = row
	}
	existsStatuses, ok := existsByPatient["p1"]["observation_status"].([]any)
	if !ok || len(existsStatuses) != 3 {
		t.Fatalf("EXISTS p1 statuses = %#v, want both edges to o1 and one o2 status", existsByPatient["p1"]["observation_status"])
	}
	existsStatusSet := map[string]int{}
	for _, status := range existsStatuses {
		existsStatusSet[fmt.Sprint(status)]++
	}
	if existsStatusSet["registered"] != 2 || existsStatusSet["cancelled"] != 1 || existsStatusSet["stale-generation"] != 0 {
		t.Fatalf("EXISTS p1 statuses = %#v, want only populated current-generation sources", existsStatuses)
	}
	existsSparse, ok := existsByPatient["p2"]["observation_status"].([]any)
	if !ok || len(existsSparse) != 0 {
		t.Fatalf("EXISTS sparse p2 statuses = %#v, want empty list", existsByPatient["p2"]["observation_status"])
	}
	existsTrace, err := CompileCellTraceOutputWithPolicy(existsCompiled.Outputs[0], "observation_status", 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	existsTraceRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: existsTrace.Query, BindVars: existsTrace.BindVars})
	for _, row := range existsTraceRows {
		parts, ok := row[existsTrace.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 1 || fmt.Sprint(parts[0]) != patientKey("p1") {
			continue
		}
		contributors, ok := row[existsTrace.ContributionsColumn].([]any)
		if !ok || len(contributors) != 3 || row[existsTrace.OmissionColumn] != "" {
			t.Fatalf("EXISTS p1 trace = %#v, want the same three eligible route occurrences as its output", row)
		}
		return
	}
	t.Fatal("EXISTS cell trace omitted p1")
}

func constructionOracleOutput() recipe.Output {
	stringAlpha, stringBeta := "alpha", "beta"
	minimumTotal := 10.0
	return recipe.Output{
		Name: "construction_oracle", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "group_text", ColumnID: "group_text_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "group_number", ColumnID: "group_number_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
				{ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "oracle_pivot", GroupKeyIDs: []string{"group_text_id", "group_number_id"},
						CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: reshapeOracleString(stringAlpha), OutputColumnID: "alpha_id"},
							{Key: reshapeOracleString(stringBeta), OutputColumnID: "beta_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"},
					},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "oracle_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "beta_id"},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &minimumTotal}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "oracle_unpivot", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "alpha_id", Key: reshapeOracleString(stringAlpha)},
							{ColumnID: "beta_id", Key: reshapeOracleString(stringBeta)},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_output_id", Name: "amount"},
					},
				},
			},
		},
	}
}

func constructionNumericEqual(value any, want float64) bool {
	switch got := value.(type) {
	case float64:
		return math.Abs(got-want) < 1e-9
	case int64:
		return float64(got) == want
	case int:
		return float64(got) == want
	default:
		return false
	}
}
