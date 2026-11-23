package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
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

	traceRootKey := func(row map[string]any, identityColumn string) string {
		t.Helper()
		parts, ok := row[identityColumn].([]any)
		if !ok || len(parts) != 2 || parts[0] != project {
			t.Fatalf("related-source trace identity = %#v, want [project, retained root key]", row[identityColumn])
		}
		key, ok := parts[1].(string)
		if !ok || !strings.HasPrefix(key, project+"_") {
			t.Fatalf("related-source trace root key = %#v, want project-scoped storage key", parts[1])
		}
		return key
	}
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
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
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(compiled.Outputs[0], "observation_status", 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	traceRows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: trace.Query, BindVars: trace.BindVars})
	traces := make(map[string]map[string]any, len(traceRows))
	for _, row := range traceRows {
		traces[traceRootKey(row, trace.IdentityPartsColumn)] = row
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
			plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
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
			trace, err := celltrace.CompileCellTraceOutputWithPolicy(compiled.Outputs[0], form.column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			traceRows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: trace.Query, BindVars: trace.BindVars})
			for _, row := range traceRows {
				if traceRootKey(row, trace.IdentityPartsColumn) != patientKey("p1") {
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
	existsPlan, err := recipeplan.BuildRecipePlan(existsBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	existsResolved, err := recipeplan.ResolveRecipePlan(existsPlan, project, generation)
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

	if !ok || len(existsStatuses) != 2 {
		t.Fatalf("EXISTS p1 statuses = %#v, want one status per distinct eligible source record", existsByPatient["p1"]["observation_status"])
	}
	existsStatusSet := map[string]int{}
	for _, status := range existsStatuses {
		existsStatusSet[fmt.Sprint(status)]++
	}
	if existsStatusSet["registered"] != 1 || existsStatusSet["cancelled"] != 1 || existsStatusSet["stale-generation"] != 0 {
		t.Fatalf("EXISTS p1 statuses = %#v, want only populated current-generation sources", existsStatuses)
	}
	existsSparse, ok := existsByPatient["p2"]["observation_status"].([]any)
	if !ok || len(existsSparse) != 0 {
		t.Fatalf("EXISTS sparse p2 statuses = %#v, want empty list", existsByPatient["p2"]["observation_status"])
	}
	existsTrace, err := celltrace.CompileCellTraceOutputWithPolicy(existsCompiled.Outputs[0], "observation_status", 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	existsTraceRows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: existsTrace.Query, BindVars: existsTrace.BindVars})
	for _, row := range existsTraceRows {
		if traceRootKey(row, existsTrace.IdentityPartsColumn) != patientKey("p1") {
			continue
		}
		contributors, ok := row[existsTrace.ContributionsColumn].([]any)
		if !ok || len(contributors) != 2 || row[existsTrace.OmissionColumn] != "" {
			t.Fatalf("EXISTS p1 trace = %#v, want the same two distinct eligible source records as its output", row)
		}
		return
	}
	t.Fatal("EXISTS cell trace omitted p1")
}

func TestPagedConstructionEqualsFilterReturnsMatchingSourceRootFromArango(t *testing.T) {
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
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Specimen"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_construction_equals_page_" + uuid.NewString()
	generation := "generation-construction-equals-page"
	targetID := "b7cad184-db67-5542-a975-10fffa3e89e7"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Specimen FILTER document.project == @project REMOVE document IN Specimen",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove construction EQUALS page fixtures: %v", err)
		}
	}()

	documents := make([]json.RawMessage, 0, 2)
	for index, id := range []string{targetID, "unmatched-specimen"} {
		payload := map[string]any{"id": id, "resourceType": "Specimen"}
		encoded, marshalErr := json.Marshal(map[string]any{
			"_key": fmt.Sprintf("%s_specimen_%d", project, index+1), "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Specimen", "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents = append(documents, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "Specimen", documents, false, "document"); err != nil {
		t.Fatalf("insert construction EQUALS page fixtures: %v", err)
	}

	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-filter-root-page",
		TranslationVersion:  "construction-filter-root-page",
		Outputs:             []recipe.Output{constructionEqualsPageOutput(targetID)},
	}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rootKeys := make([]string, 0, 1)
	if err := client.QueryRows(ctx, page.RootKeysQuery, 100, page.RootKeysBindVars, func(row map[string]any) error {
		key, ok := row["_key"].(string)
		if !ok {
			return fmt.Errorf("root-key query returned invalid _key %#v", row["_key"])
		}
		rootKeys = append(rootKeys, key)
		return nil
	}); err != nil {
		t.Fatalf("execute construction EQUALS root-key query:\n%s\n%v", page.RootKeysQuery, err)
	}
	wantKey := fmt.Sprintf("%s_specimen_1", project)
	if len(rootKeys) != 1 || rootKeys[0] != wantKey {
		t.Fatalf("construction EQUALS root keys = %#v, want only %q\n%s", rootKeys, wantKey, page.RootKeysQuery)
	}

	rowBindVars := make(map[string]any, len(page.RowsBindVars)+1)
	for key, value := range page.RowsBindVars {
		rowBindVars[key] = value
	}
	rowBindVars[RootPageKeysBind] = rootKeys
	rows := make([]map[string]any, 0, 1)
	if err := client.QueryRows(ctx, page.RowsQuery, 100, rowBindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute selected construction rows query:\n%s\n%v", page.RowsQuery, err)
	}
	if len(rows) != 1 || rows[0]["specimen_id"] != targetID {
		t.Fatalf("construction EQUALS rows = %#v, want one row with Specimen ID %q", rows, targetID)
	}
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

func TestCompileRecipeOutputPagePartitionsWideSourceProjection(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []recipeplan.SemanticField{{
			Name: "id", FieldRef: "Patient.id",
			Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	terminalIndex := len(plan.Operations) - 1
	terminal := plan.Operations[terminalIndex].Return
	if terminal == nil {
		t.Fatal("generic source plan has no terminal return")
	}
	hasKey := false
	for _, projection := range terminal.Projections {
		hasKey = hasKey || projection.Name == "_key"
	}
	if !hasKey {
		root := plan.Operations[0].RootScan.Variable
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{
			Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}},
		})
	}
	root := plan.Operations[0].RootScan.Variable
	for index := 0; index < 160; index++ {
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{
			Name:  fmt.Sprintf("wide_%03d", index),
			Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", fmt.Sprintf("wide_%03d", index)}},
		})
	}
	fullProjectionCount := len(terminal.Projections)
	page, err := CompileRecipeOutputPageWithPolicy(lower.CompiledRecipeOutput{
		Name: "patients", RootResourceType: "Patient", Plan: plan,
	}, recipe.RuntimeBindings{Project: "p", PreviewLimit: 25}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.RowsPartitions) < 2 {
		t.Fatalf("partition count = %d, want multiple partitions", len(page.RowsPartitions))
	}
	if len(plan.Operations[terminalIndex].Return.Projections) != fullProjectionCount {
		t.Fatal("partition compilation mutated the caller's physical plan")
	}
	for index, partition := range page.RowsPartitions {
		if !strings.Contains(partition.RowsQuery, "SORTED_UNIQUE(@"+RootPageKeysBind+")") {
			t.Errorf("partition %d lost selected root-key window", index)
		}
		hasIdentity := false
		for _, name := range partition.ProjectionNames {
			hasIdentity = hasIdentity || name == "_key"
		}
		if !hasIdentity {
			t.Errorf("partition %d does not return root identity: %#v", index, partition.ProjectionNames)
		}
	}
	if !strings.Contains(page.RowsQuery, "wide_159") {
		t.Fatal("full fallback query lost its complete projection")
	}
}

func TestPreviewProjectionPartitionsRejectCardinalityChangingPlans(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
	}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Operations = append(plan.Operations[:len(plan.Operations)-1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalUnnestOp}}, plan.Operations[len(plan.Operations)-1:]...)...)
	if previewProjectionPartitionEligible(plan) {
		t.Fatal("cardinality-changing source plan was eligible for root-key partitions")
	}
}

func TestCompileRecipeOutputPageSelectsRootsBeforeExpansion(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []recipeplan.SemanticField{{
			Name: "id", FieldRef: "Patient.id",
			Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := spec.ParseSelector("extension[].url")
	if err != nil {
		t.Fatal(err)
	}
	unnest := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
		Owner:          ir.PhysicalUnnestOwner{ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
		OutputVariable: "item", HasItemVariable: "has_item", EmptyPolicy: ir.PhysicalUnnestExclude,
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: selector, ExecutionMode: ir.PhysicalSelectorConditionalArray},
		},
	}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[6:], plan.Operations[5:])
	plan.Operations[5] = unnest
	page, err := CompileRecipeOutputPageWithPolicy(lower.CompiledRecipeOutput{
		Name: "patients", RootResourceType: "Patient", Plan: plan,
	}, recipe.RuntimeBindings{Project: "p"}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := page.RootKeysBindVars[RootPageSizeBind]; got != 25 {
		t.Fatalf("root page size bind = %#v, want 25", got)
	}
	if strings.Contains(page.RootKeysQuery, "unnest") || strings.Contains(page.RootKeysQuery, "extension") {
		t.Fatalf("root-key discovery crossed the expansion boundary:\n%s", page.RootKeysQuery)
	}
	keyWindow := strings.Index(page.RowsQuery, "IN SORTED_UNIQUE(@"+RootPageKeysBind+")")
	rootLookup := strings.Index(page.RowsQuery, "LET root = DOCUMENT(@@root_collection,")
	nullGuard := strings.Index(page.RowsQuery, "FILTER root != null")
	unnestIndex := strings.Index(page.RowsQuery, "LET item =")
	if keyWindow < 0 || rootLookup < keyWindow || nullGuard < rootLookup || unnestIndex < nullGuard {
		t.Fatalf("sorted page-key lookup and missing-root guard must run before UNNEST:\n%s", page.RowsQuery)
	}
	if strings.Contains(page.RowsQuery, "root._key IN @"+RootPageKeysBind) {
		t.Fatalf("selected-root rows still scan the root collection and filter its keys:\n%s", page.RowsQuery)
	}
	if !strings.Contains(page.RootKeysQuery, "root._key > @"+RootPageAfterKeyBind) {
		t.Fatalf("root-key discovery is not a keyset query:\n%s", page.RootKeysQuery)
	}
}

func TestCompileRecipeOutputPageKeepsRelatedStageOutOfRootKeyDiscovery(t *testing.T) {
	sourceFields := []recipe.Field{
		{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "patient_active", ColumnID: "patient-active", Expr: recipe.Expression{Select: "root.active"}},
		{Name: "patient_gender", ColumnID: "patient-gender", Expr: recipe.Expression{Select: "root.gender"}},
		{Name: "patient_birth_date", ColumnID: "patient-birth-date", Expr: recipe.Expression{Select: "root.birthDate"}},
		{Name: "patient_deceased", ColumnID: "patient-deceased", Expr: recipe.Expression{Select: "root.deceasedBoolean"}},
		{Name: "patient_multiple_birth", ColumnID: "patient-multiple-birth", Expr: recipe.Expression{Select: "root.multipleBirthBoolean"}},
		{Name: "patient_family", ColumnID: "patient-family", Expr: recipe.Expression{Select: "root.name[].family"}, ValueMode: recipe.ValueModeFirst},
		{Name: "patient_telecom", ColumnID: "patient-telecom", Expr: recipe.Expression{Select: "root.telecom[].value"}, ValueMode: recipe.ValueModeFirst},
	}
	sourceColumns := make([]recipe.StageColumn, 0, len(sourceFields))
	for _, field := range sourceFields {
		sourceColumns = append(sourceColumns, recipe.StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
	}
	outputColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{ID: "observation-status", Name: "observation_status", Label: "Observation statuses"})
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: sourceFields,
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: sourceColumns,
			Steps: []recipe.ConstructionStep{{
				ID: "related_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "choice-observation-status", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation-status",
				}},
				Outputs: outputColumns,
			}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "related-source-paging", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile paged related-source output: %v", err)
	}
	if strings.Contains(page.RootKeysQuery, "observation_status") || strings.Contains(page.RootKeysQuery, "@@related_") {
		t.Fatalf("root-key discovery evaluated the related-source stage:\n%s", page.RootKeysQuery)
	}
	if !strings.Contains(page.RootKeysQuery, "root._key > @"+RootPageAfterKeyBind) || page.RootKeysBindVars[RootPageSizeBind] != 2 {
		t.Fatalf("root-key discovery lost its keyset window: query=%s binds=%#v", page.RootKeysQuery, page.RootKeysBindVars)
	}
	if !strings.Contains(page.RowsQuery, "IN SORTED_UNIQUE(@"+RootPageKeysBind+")") ||
		!strings.Contains(page.RowsQuery, "LET root = DOCUMENT(@@root_collection,") ||
		!strings.Contains(page.RowsQuery, "observation_status") || !strings.Contains(page.RowsQuery, "@@related_0_hop_1_edge_collection") {
		t.Fatalf("selected-root page did not retain the related-source stage:\n%s", page.RowsQuery)
	}
}

func TestCompileRecipeOutputPageKeepsAggregationOnCollectionFilterPath(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled := lowerConstructionOutput(t, constructionOracleOutput(), bindings)
	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile aggregated output page: %v", err)
	}
	if strings.Contains(page.RowsQuery, "IN SORTED_UNIQUE(@"+RootPageKeysBind+")") ||
		!strings.Contains(page.RowsQuery, "root._key IN @"+RootPageKeysBind) {
		t.Fatalf("aggregated Pivot/Unpivot output must keep the existing root collection filter path:\n%s", page.RowsQuery)
	}
}

func TestRelatedExpandPreviewPageCapsOnlyTerminalIdentityPartition(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "related-expand-preview", DatasetGeneration: "generation-a"}
	output := directRelatedExpandOutput(recipe.ExpansionExclude)
	compiled := lowerConstructionOutput(t, output, bindings)

	bindings.PreviewLimit = 3
	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile terminal related-expansion preview page: %v", err)
	}
	previewLimitBind := ""
	for key, value := range page.RowsBindVars {
		if strings.HasPrefix(key, "__loom_physical_construction_related_expand_preview_limit") && value == 3 {
			previewLimitBind = key
		}
	}
	if previewLimitBind == "" {
		t.Fatalf("selected-root page has no per-parent preview limit bind: %#v", page.RowsBindVars)
	}
	if !strings.Contains(page.RowsQuery, "SORT TO_STRING([[\"input\",") || !strings.Contains(page.RowsQuery, "[\"related\",") ||
		!strings.Contains(page.RowsQuery, "LIMIT @"+previewLimitBind) {
		t.Fatalf("terminal related candidates were not capped by the exact final row identity:\n%s", page.RowsQuery)
	}
	if strings.Contains(page.RowsQuery, "LIMIT @limit") {
		t.Fatalf("selected-root rows query gained a global page limit:\n%s", page.RowsQuery)
	}
	if !strings.Contains(page.RowsQuery, "SORTED_UNIQUE(") || !strings.Contains(page.RowsQuery, "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("related deduplication or final identity ordering was removed:\n%s", page.RowsQuery)
	}

	full, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile full related-expansion output: %v", err)
	}
	if strings.Contains(full.Query, "related_expand_preview_candidate") || strings.Contains(full.Query, "LIMIT @limit") {
		t.Fatalf("preview cap leaked into full publication execution:\n%s", full.Query)
	}

	filteredOutput := directRelatedExpandOutput(recipe.ExpansionExclude)
	expand := filteredOutput.Construction.Steps[0]
	filteredOutput.Construction.Steps = append(filteredOutput.Construction.Steps, recipe.ConstructionStep{
		ID: "filter_after_related_expand", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: expand.ID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "observation-id", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: stringPointer("observation-1")}},
		}},
		Outputs: append([]recipe.StageColumn(nil), expand.Outputs...),
	})
	filtered := lowerConstructionOutput(t, filteredOutput, recipe.RuntimeBindings{Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration})
	filteredPage, err := CompileRecipeOutputPageWithPolicy(filtered, bindings, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile nonterminal related-expansion page: %v", err)
	}
	if strings.Contains(filteredPage.RowsQuery, "related_expand_preview_limit") || strings.Contains(filteredPage.RowsQuery, "related_expand_preview_candidate") {
		t.Fatalf("nonterminal related expansion was capped before its downstream filter:\n%s", filteredPage.RowsQuery)
	}
}

func stringPointer(value string) *string { return &value }

func TestCompileRecipeOutputPageRetainsSinglePopulationComputation(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-page-test",
		TranslationVersion:  "population-page-test",
		Outputs: []recipe.Output{{
			Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
				ResourceType: "Specimen",
			},
		}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{"root keys": page.RootKeysQuery, "rows": page.RowsQuery} {
		if got := strings.Count(query, "FOR population_member IN @@population_members_collection"); got != 1 {
			t.Fatalf("%s population member scan count = %d, want 1:\n%s", name, got, query)
		}
		if !strings.Contains(query, "FOR population_source IN @@population_source_collection") || !strings.Contains(query, "COLLECT __loom_physical_population_root_key = population_source._key") {
			t.Fatalf("%s query lost the membership-driven population root source:\n%s", name, query)
		}
		if strings.Contains(query, "__loom_population_members_value") || strings.Contains(query, "__loom_population_members") {
			t.Fatalf("%s ordinary page query materialized population provenance:\n%s", name, query)
		}
	}
	filterIndex := strings.Index(page.RowsQuery, "root._key IN @"+RootPageKeysBind)
	populationIndex := strings.Index(page.RowsQuery, "FOR population_member IN @@population_members_collection")
	if populationIndex < 0 || filterIndex < 0 || populationIndex > filterIndex {
		t.Fatalf("population eligibility was not evaluated before selected-root paging:\n%s", page.RowsQuery)
	}
}

func TestCompileRecipeOutputPageFiltersConstructionRowsBeforeRootWindow(t *testing.T) {
	id := "b7cad184-db67-5542-a975-10fffa3e89e7"
	output := constructionEqualsPageOutput(id)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-filter-root-page",
		TranslationVersion:  "construction-filter-root-page",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	letSuffix := " = root.payload.id"
	letValue := strings.Index(page.RootKeysQuery, letSuffix)
	if letValue < 0 {
		t.Fatalf("root-key query lost the source projection for the filtered ID:\n%s", page.RootKeysQuery)
	}
	letStart := strings.LastIndex(page.RootKeysQuery[:letValue], "\n") + 1
	letVariable := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(page.RootKeysQuery[letStart:letValue]), "LET "))
	filterText := "FILTER " + letVariable + " == @construction_filter_value"
	filter := strings.Index(page.RootKeysQuery, filterText)
	window := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
	limit := strings.Index(page.RootKeysQuery, "LIMIT @limit")
	if letVariable == "" || filter < 0 || window < 0 || limit < 0 || letValue > filter || filter > window || window > limit {
		t.Fatalf("construction EQUALS filter must compare the source ID value and run before the keyset page window: let=%q filter=%d sort=%d limit=%d\n%s", letVariable, filter, window, limit, page.RootKeysQuery)
	}
	for name, bindVars := range map[string]map[string]any{"root keys": page.RootKeysBindVars, "selected rows": page.RowsBindVars} {
		if got := bindVars["construction_filter_value"]; got != id {
			t.Fatalf("%s construction filter bind = %#v, want %q", name, got, id)
		}
	}
	if !strings.Contains(page.RowsQuery, "@construction_filter_value") {
		t.Fatalf("selected-root execution lost the construction filter:\n%s", page.RowsQuery)
	}
}

func TestCompileRecipeOutputPageFiltersRelatedConstructionRowsBeforeRootWindow(t *testing.T) {
	patientID := "patient-that-matches-the-filter"
	output := relatedTraversalIDFilterPageOutput(patientID)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "related-construction-filter-root-page",
		TranslationVersion:  "related-construction-filter-root-page",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	window := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
	limit := strings.Index(page.RootKeysQuery, "LIMIT @limit")
	filter := strings.Index(page.RootKeysQuery, "@construction_filter_value")
	if filter < 0 || window < 0 || limit < 0 || filter > window || window > limit {
		t.Fatalf("source-projection Patient.id construction filter must run before the root-key window: filter=%d sort=%d limit=%d\n%s", filter, window, limit, page.RootKeysQuery)
	}
	if got := page.RootKeysBindVars["construction_filter_value"]; got != patientID {
		t.Fatalf("root-key related construction filter bind = %#v, want %q", got, patientID)
	}
}

func relatedTraversalIDFilterPageOutput(patientID string) recipe.Output {
	patientColumn := recipe.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}
	return recipe.Output{
		Name: "observations", RootResourceType: "Observation", RootOccurrenceID: "base", RowGrain: "observation",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "patient-occurrence", Alias: "patient-occurrence", ToResourceType: "Patient",
			MatchMode: recipe.MatchOptional,
			Fields: []recipe.Field{{
				Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", FieldRef: "id",
				Expr: recipe.Expression{Select: "patient-occurrence.id"}, ValueMode: recipe.ValueModeAuto,
			}},
		}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{patientColumn},
			Steps: []recipe.ConstructionStep{{
				ID: "keep_matching_patient", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &patientID}},
				}},
				Outputs: []recipe.StageColumn{patientColumn},
			}},
		},
	}
}

func TestCompileRecipeOutputPageFiltersMissingConstructionRowsBeforeRootWindow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		operator   recipe.FilterOperator
		comparison string
	}{
		{name: "MISSING", operator: recipe.FilterMissing, comparison: "== null"},
		{name: "EXISTS", operator: recipe.FilterExists, comparison: "!= null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := constructionEqualsPageOutput("")
			output.Construction.Steps[0].Operation.Filter.Operator = tc.operator
			output.Construction.Steps[0].Operation.Filter.Values = nil
			bundle := recipe.Bundle{
				RecipeSchemaVersion: recipe.CurrentSchemaVersion,
				Name:                "construction-filter-root-page-" + tc.name,
				TranslationVersion:  "construction-filter-root-page-" + tc.name,
				Outputs:             []recipe.Output{output},
			}
			bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
			semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rootFilter := "FILTER __loom_construction_input_1 " + tc.comparison
			filter := strings.Index(page.RootKeysQuery, rootFilter)
			window := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
			limit := strings.Index(page.RootKeysQuery, "LIMIT @limit")
			if filter < 0 || window < 0 || limit < 0 || filter > window || window > limit {
				t.Fatalf("construction %s filter must run before the root-key page window: filter=%d sort=%d limit=%d\n%s", tc.name, filter, window, limit, page.RootKeysQuery)
			}
			if !strings.Contains(page.RowsQuery, "FILTER __loom_construction_input_1.specimen_id "+tc.comparison) {
				t.Fatalf("selected-root execution lost the construction %s filter:\n%s", tc.name, page.RowsQuery)
			}
		})
	}
}

func TestCompileRecipeOutputPageDirectRootExistsOrdersPredicateBeforeLimit(t *testing.T) {
	output := constructionEqualsPageOutput("")
	output.Name = "observations"
	output.RootResourceType = "Observation"
	output.RowGrain = "observation"
	output.Construction.Steps[0].Operation.Filter.Operator = recipe.FilterExists
	output.Construction.Steps[0].Operation.Filter.Values = nil
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-exists-root-page-structure",
		TranslationVersion:  "construction-exists-root-page-structure",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
	semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	pageSize := 2
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, pageSize, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	cursorFilter := strings.Index(page.RootKeysQuery, "FILTER root._key > @"+RootPageAfterKeyBind)
	projectedID := strings.Index(page.RootKeysQuery, " = root.payload.id")
	existsFilter := strings.Index(page.RootKeysQuery, "FILTER __loom_construction_input_1 != null")
	sort := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
	limit := strings.Index(page.RootKeysQuery, "LIMIT @"+RootPageSizeBind)
	if cursorFilter < 0 || projectedID <= cursorFilter || existsFilter <= projectedID || sort <= existsFilter || limit <= sort {
		t.Fatalf("compiled direct-root EXISTS query must place its predicate after the cursor and before root-key sorting/limiting: cursor=%d projection=%d exists=%d sort=%d limit=%d\n%s", cursorFilter, projectedID, existsFilter, sort, limit, page.RootKeysQuery)
	}
	if got := page.RootKeysBindVars[RootPageSizeBind]; got != pageSize {
		t.Fatalf("compiled root page size = %#v, want %d", got, pageSize)
	}
}

func TestCompileRecipeOutputPageRendersEveryValueFilterBeforeRootWindow(t *testing.T) {
	stringValue := func(value string) recipe.FilterValue {
		return recipe.FilterValue{Kind: recipe.FilterString, String: &value}
	}
	decimalValue := func(value float64) recipe.FilterValue {
		return recipe.FilterValue{Kind: recipe.FilterDecimal, Decimal: &value}
	}
	tests := []struct {
		name       string
		operator   recipe.FilterOperator
		values     []recipe.FilterValue
		columnType string
		columnName string
		selectExpr string
		comparison string
		wantBind   any
	}{
		{name: "NOT_EQUALS", operator: recipe.FilterNotEquals, values: []recipe.FilterValue{stringValue("excluded")}, columnType: "string", columnName: "specimen_id", selectExpr: "root.id", comparison: "!= @construction_filter_value", wantBind: "excluded"},
		{name: "IN", operator: recipe.FilterIn, values: []recipe.FilterValue{stringValue("a"), stringValue("b")}, columnType: "string", columnName: "specimen_id", selectExpr: "root.id", comparison: "IN @construction_filter_value", wantBind: []any{"a", "b"}},
		{name: "CONTAINS_TEXT", operator: recipe.FilterContains, values: []recipe.FilterValue{stringValue("sample")}, columnType: "string", columnName: "specimen_id", selectExpr: "root.id", comparison: "CONTAINS(TO_STRING(__loom_construction_input_1), @construction_filter_value)", wantBind: "sample"},
		{name: "GT", operator: recipe.FilterGreaterThan, values: []recipe.FilterValue{decimalValue(18.5)}, columnType: "decimal", columnName: "quantity_value", selectExpr: "root.valueQuantity.value", comparison: "> @construction_filter_value", wantBind: 18.5},
		{name: "GTE", operator: recipe.FilterGreaterEq, values: []recipe.FilterValue{decimalValue(18.5)}, columnType: "decimal", columnName: "quantity_value", selectExpr: "root.valueQuantity.value", comparison: ">= @construction_filter_value", wantBind: 18.5},
		{name: "LT", operator: recipe.FilterLessThan, values: []recipe.FilterValue{decimalValue(18.5)}, columnType: "decimal", columnName: "quantity_value", selectExpr: "root.valueQuantity.value", comparison: "< @construction_filter_value", wantBind: 18.5},
		{name: "LTE", operator: recipe.FilterLessEq, values: []recipe.FilterValue{decimalValue(18.5)}, columnType: "decimal", columnName: "quantity_value", selectExpr: "root.valueQuantity.value", comparison: "<= @construction_filter_value", wantBind: 18.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := constructionEqualsPageOutput("")
			if tc.columnType == "decimal" {
				output.RootResourceType = "Observation"
				output.RowGrain = "observation"
			}
			columns := []recipe.StageColumn{{ID: "specimen-id", Name: tc.columnName, Label: tc.columnName, Type: tc.columnType}}
			output.Fields[0].Name = tc.columnName
			output.Fields[0].Expr.Select = tc.selectExpr
			output.Construction.SourceColumns = columns
			output.Construction.Steps[0].Outputs = columns
			predicateFilter := output.Construction.Steps[0].Operation.Filter
			predicateFilter.Operator = tc.operator
			predicateFilter.Values = tc.values

			bundle := recipe.Bundle{
				RecipeSchemaVersion: recipe.CurrentSchemaVersion,
				Name:                "construction-filter-root-page-" + tc.name,
				TranslationVersion:  "construction-filter-root-page-" + tc.name,
				Outputs:             []recipe.Output{output},
			}
			bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a"}
			semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, "scope-a", bindings.DatasetGeneration)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}

			filterIndex := strings.Index(page.RootKeysQuery, "FILTER __loom_construction_input_1 "+tc.comparison)
			if tc.operator == recipe.FilterContains {
				filterIndex = strings.Index(page.RootKeysQuery, "FILTER "+tc.comparison)
			}
			window := strings.Index(page.RootKeysQuery, "SORT root._key ASC")
			limit := strings.Index(page.RootKeysQuery, "LIMIT @limit")
			if filterIndex < 0 || window < 0 || limit < 0 || filterIndex > window || window > limit {
				t.Fatalf("construction %s filter must execute before the root-key window: filter=%d sort=%d limit=%d\n%s", tc.name, filterIndex, window, limit, page.RootKeysQuery)
			}
			if !strings.Contains(page.RowsQuery, "@construction_filter_value") {
				t.Fatalf("selected-root execution lost the construction %s predicate:\n%s", tc.name, page.RowsQuery)
			}
			for label, bindVars := range map[string]map[string]any{"root keys": page.RootKeysBindVars, "selected rows": page.RowsBindVars} {
				if got := bindVars["construction_filter_value"]; !reflect.DeepEqual(got, tc.wantBind) {
					t.Fatalf("%s %s bind = %#v, want %#v", label, tc.name, got, tc.wantBind)
				}
			}
		})
	}
}

func constructionEqualsPageOutput(id string) recipe.Output {
	columns := []recipe.StageColumn{{ID: "specimen-id", Name: "specimen_id", Label: "Specimen ID"}}
	return recipe.Output{
		Name: "specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen-id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: columns,
			Steps: []recipe.ConstructionStep{{
				ID: "keep_matching_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "specimen-id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &id}},
				}},
				Outputs: columns,
			}},
		},
	}
}

func TestWorkspaceOutputExecutionRemainsExplicitlyUnsupportedUntilCapture(t *testing.T) {
	output := lower.CompiledRecipeOutput{
		Name: "combined",
		Plan: ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &ir.PhysicalClickHouseCombine{
			Kind:   ir.PhysicalCombineAppend,
			Inputs: []ir.PhysicalCombineInputRef{{WorkspaceOutputID: "base"}, {TableID: "table", RevisionID: "r1", OutputID: "table"}},
		}},
		WorkspaceOutputSources: []lower.WorkspaceOutputSource{{InputIndex: 0, OutputID: "base", Schema: []lower.CompiledOutputColumn{{ID: "value", Kind: "string"}}}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a"}
	if _, err := CompileRecipeOutputWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("query compile error = %v", err)
	}
	if _, err := CompileRecipeOutputPageWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("page compile error = %v", err)
	}
}

func TestCompiledQueryRetainsFinalizedPlanThatRendersExecutedQuery(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "physical-plan-project", DatasetGeneration: "physical-plan-generation"}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "retained_physical_plan",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
		}},
	}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlanWithPolicy: %v", err)
	}
	if len(queries) != 1 || queries[0].PhysicalPlan.Engine != ir.PhysicalEngineAQL {
		t.Fatalf("compiled query did not retain one finalized AQL plan: %#v", queries)
	}
	rendered, err := aql.Render(queries[0].PhysicalPlan, aql.Options{})
	if err != nil {
		t.Fatalf("render retained plan: %v", err)
	}
	if rendered.Query != queries[0].Query || !reflect.DeepEqual(rendered.BindVars, queries[0].BindVars) {
		t.Fatalf("retained plan differs from executed query/binds: queryMatch=%t bindMatch=%t", rendered.Query == queries[0].Query, reflect.DeepEqual(rendered.BindVars, queries[0].BindVars))
	}
	if got := queries[0].BindVars["limit"]; got != 7 {
		t.Fatalf("retained execution plan lost its final preview window, limit bind=%#v", got)
	}
}
