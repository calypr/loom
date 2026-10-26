package compiler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestConstructionCountRowsGroupPivotRowLineageMatchesScopedSourcePreimageAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project := "loom_group_pivot_lineage_" + uuid.NewString()
	foreignProject := "loom_group_pivot_lineage_foreign_" + uuid.NewString()
	generation := "generation-group-pivot-lineage"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project IN @projects REMOVE document IN Observation",
			map[string]any{"projects": []string{project, foreignProject}},
		); err != nil {
			t.Errorf("remove Group/Pivot row-lineage fixtures: %v", err)
		}
	})

	type fixture struct {
		id, patient, project, generation, authPath string
		status                                     *string
	}
	active, inactive, unlisted := "active", "inactive", "unlisted"
	fixtures := []fixture{
		{id: "a_active_one", patient: "Patient/target", project: project, generation: generation, authPath: "/allowed", status: &active},
		{id: "b_active_two", patient: "Patient/target", project: project, generation: generation, authPath: "/allowed", status: &active},
		{id: "c_inactive", patient: "Patient/target", project: project, generation: generation, authPath: "/allowed", status: &inactive},
		{id: "d_explicit_null", patient: "Patient/target", project: project, generation: generation, authPath: "/allowed", status: nil},
		{id: "e_missing_status", patient: "Patient/target", project: project, generation: generation, authPath: "/allowed"},
		{id: "other_active", patient: "Patient/other", project: project, generation: generation, authPath: "/allowed", status: &active},
		// These match the requested Pivot row keys and category, but each violates
		// one source boundary. They must affect neither preview counts nor lineage.
		{id: "z_denied", patient: "Patient/target", project: project, generation: generation, authPath: "/denied", status: &active},
		{id: "z_old_generation", patient: "Patient/target", project: project, generation: "old-generation", authPath: "/allowed", status: &active},
		{id: "z_foreign_project", patient: "Patient/target", project: foreignProject, generation: generation, authPath: "/allowed", status: &active},
	}
	insert := func(items []fixture) {
		t.Helper()
		documents := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			payload := map[string]any{
				"id": item.id, "resourceType": "Observation",
				"subject": map[string]any{"reference": item.patient},
			}
			if item.status != nil {
				payload["status"] = *item.status
			} else if item.id == "d_explicit_null" {
				payload["status"] = nil
			}
			document, err := json.Marshal(map[string]any{
				"_key": item.project + "_" + item.id, "id": item.id, "project": item.project,
				"project_id": item.project, "dataset_generation": item.generation,
				"resourceType": "Observation", "auth_resource_path": item.authPath,
				"payload": payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			documents = append(documents, document)
		}
		if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
			t.Fatalf("insert Group/Pivot row-lineage fixtures: %v", err)
		}
	}
	insert(fixtures)

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
		IncludeRowIdentity: true,
	}
	output := constructionCountRowsGroupPivotLineageArangoOutput()
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("COUNT_ROWS Group/Pivot row-lineage capability = %#v", capability)
	}
	preview, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(previewRows) != 2 {
		t.Fatalf("scoped Group/Pivot preview rows = %#v, want target and other patient only", previewRows)
	}
	var targetRow, otherRow map[string]any
	for _, row := range previewRows {
		switch row["patient_ref"] {
		case "Patient/target":
			targetRow = row
		case "Patient/other":
			otherRow = row
		}
	}
	if targetRow == nil || !constructionNumericEqual(targetRow["active_rows"], 2) ||
		!constructionNumericEqual(targetRow["inactive_rows"], 1) || !constructionNumericEqual(targetRow["null_status_rows"], 2) {
		t.Fatalf("target Group/Pivot preview = %#v, want active=2, inactive=1, NULL=2", targetRow)
	}
	if otherRow == nil || !constructionNumericEqual(otherRow["active_rows"], 1) ||
		otherRow["inactive_rows"] != nil || otherRow["null_status_rows"] != nil {
		t.Fatalf("other Group/Pivot preview = %#v, want active=1 and missing cells NULL", otherRow)
	}
	rowID, ok := targetRow["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("target Group/Pivot row identity = %#v", targetRow["__loom_row_id"])
	}
	wantIdentity, err := json.Marshal([]any{"GROUPED_PIVOT", "group_pivot_lineage", []any{"STRING", "Patient/target"}})
	if err != nil {
		t.Fatal(err)
	}
	if rowID != string(wantIdentity) {
		t.Fatalf("target Group/Pivot row identity = %s, want exact typed identity %s", rowID, wantIdentity)
	}

	compilePage := func(requestedID string, offset int) CompiledRowLineageQuery {
		t.Helper()
		compiled, compileErr := CompileRowLineageOutput(compiledOutput, requestedID, offset, 2, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile Group/Pivot lineage page at offset %d: %v", offset, compileErr)
		}
		for _, want := range []string{
			"root.project == @project", "root.dataset_generation == @dataset_generation",
			"root.auth_resource_path IN @auth_resource_paths", "LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		} {
			if !strings.Contains(compiled.Query, want) {
				t.Errorf("Group/Pivot row-lineage query lost source scope or bounded page clause %q:\n%s", want, compiled.Query)
			}
		}
		if strings.Contains(compiled.Query, "COLLECT INTO") || strings.Contains(compiled.Query, "SORTED_UNIQUE") ||
			strings.Contains(compiled.Query, "__loom_root_contributor_keys") {
			t.Errorf("Group/Pivot row-lineage query relied on materialized contributor arrays or sidecars:\n%s", compiled.Query)
		}
		return compiled
	}
	wantIDs := []string{"a_active_one", "b_active_two", "c_inactive", "d_explicit_null", "e_missing_status"}
	seenIDs, seenKeys := map[string]bool{}, map[string]bool{}
	for pageIndex, offset := range []int{0, 2, 4} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, offset))
		contributors, ok := result["contributors"].([]any)
		wantPageIDs := wantIDs[offset:min(offset+2, len(wantIDs))]
		wantMore := offset+len(wantPageIDs) < len(wantIDs)
		if !ok || result["found"] != true || result["hasMore"] != wantMore || len(contributors) != len(wantPageIDs) {
			t.Fatalf("Group/Pivot lineage page %d = %#v, want IDs %v and hasMore=%t", pageIndex, result, wantPageIDs, wantMore)
		}
		for itemIndex, raw := range contributors {
			contributor, _ := raw.(map[string]any)
			id := wantPageIDs[itemIndex]
			key := project + "_" + id
			if contributor["resourceType"] != "Observation" || contributor["resourceId"] != id || contributor["occurrenceKey"] != key {
				t.Fatalf("Group/Pivot contributor = %#v, want exact Observation/%s at %s", contributor, id, key)
			}
			if seenIDs[id] || seenKeys[key] {
				t.Fatalf("Group/Pivot lineage duplicated source identity across pages: %s / %s", id, key)
			}
			seenIDs[id], seenKeys[key] = true, true
		}
	}
	if len(seenIDs) != len(wantIDs) || len(seenKeys) != len(wantIDs) {
		t.Fatalf("Group/Pivot source preimage IDs/keys = %#v / %#v, want exact five roots", seenIDs, seenKeys)
	}
	pastEnd := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, len(wantIDs)))
	if pastEnd["found"] != true || pastEnd["hasMore"] != false || len(pastEnd["contributors"].([]any)) != 0 {
		t.Fatalf("past-end Group/Pivot lineage page = %#v, want found row and empty page", pastEnd)
	}
	forgedID, err := json.Marshal([]any{"GROUPED_PIVOT", "wrong_construction", []any{"STRING", "Patient/target"}})
	if err != nil {
		t.Fatal(err)
	}
	forged := executeRowLineageOracleQuery(t, ctx, client, compilePage(string(forgedID), 0))
	if forged["found"] != false || len(forged["contributors"].([]any)) != 0 {
		t.Fatalf("wrong-construction Group/Pivot identity disclosed source rows: %#v", forged)
	}

	// The unlisted category belongs to another Pivot row and sorts after the
	// requested contributor page. Full-scope policy validation must still fail.
	insert([]fixture{{id: "zz_unlisted_other", patient: "Patient/unrelated", project: project, generation: generation, authPath: "/allowed", status: &unlisted}})
	lineage := compilePage(rowID, 0)
	err = client.QueryRows(ctx, lineage.Query, 32, lineage.BindVars, func(map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "TABLE_PIVOT_UNLISTED_CATEGORY") {
		t.Fatalf("unlisted category outside the requested row/page produced error %v, want TABLE_PIVOT_UNLISTED_CATEGORY", err)
	}
}

func constructionCountRowsGroupPivotLineageArangoOutput() recipe.Output {
	active, inactive := "active", "inactive"
	return recipe.Output{
		Name: "construction_group_pivot_lineage_arango", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "patient_ref", ColumnID: "patient_ref_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "patient_ref_id", Name: "patient_ref", Type: "string"},
				{ID: "status_id", Name: "status", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys: []recipe.ConstructionGroupKey{
							{InputColumnID: "patient_ref_id", OutputColumnID: "patient_ref_group_id"},
							{InputColumnID: "status_id", OutputColumnID: "status_group_id"},
						},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "status_group_id", Name: "status", Type: "string"},
						{ID: "rows_id", Name: "rows", Type: "integer"},
					},
				},
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "group_pivot_lineage", GroupKeyIDs: []string{"patient_ref_group_id"},
						CategoryColumnID: "status_group_id", ValueColumnID: "rows_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &active}, OutputColumnID: "active_rows_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &inactive}, OutputColumnID: "inactive_rows_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_status_rows_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "active_rows_id", Name: "active_rows", Type: "integer"},
						{ID: "inactive_rows_id", Name: "inactive_rows", Type: "integer"},
						{ID: "null_status_rows_id", Name: "null_status_rows", Type: "integer"},
					},
				},
			},
		},
	}
}
