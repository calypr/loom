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

// TestPivotPresenceArangoAudit executes the presence matrix against real AQL and
// reports which offered columns would reach MISSING_UNSUPPORTED when the source
// contains absent or explicit-null values. It is diagnostic and env-gated.
func TestPivotPresenceArangoAudit(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point at a disposable Arango database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}, {Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_presence_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	const generation = "presence-audit-generation"
	const allowedPath = "/presence-audit/allowed"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, collection := range []string{"Patient", "Observation"} {
			if err := client.ExecuteAQL(cleanupCtx, fmt.Sprintf(`
FOR document IN %s
  FILTER document.project == @project
  REMOVE document IN %s`, collection, collection), map[string]any{"project": project}); err != nil {
				t.Errorf("remove %s presence audit fixtures: %v", collection, err)
			}
		}
	})
	seedPresenceAuditDocuments(t, ctx, client, project, generation, allowedPath)

	fixtures := []struct {
		name   string
		output recipe.Output
	}{
		{name: "source-derive-filter", output: categoryScanConstructionRecipeOutput()},
		{name: "expand", output: constructionExpandOutput()},
		{name: "group", output: constructionGroupRowValuesOutput()},
		{name: "group-pivot", output: constructionCountRowsGroupPivotLineageOutput()},
		{name: "pivot", output: constructionPreviewMultiKeyPivotOutput()},
		{name: "unpivot", output: constructionOracleOutput()},
		{name: "simple-unpivot", output: presenceAuditUnpivotOutput()},
	}
	for _, fixture := range fixtures {
		base := compilePresenceAuditOutput(t, fixture.output, project, generation, []string{allowedPath})
		for _, stage := range base.Stages {
			eligible := eligibleCategoryColumns(stage.Columns)
			for _, category := range eligible {
				var value *lower.CompiledOutputColumn
				for index := range eligible {
					if eligible[index].ID != category.ID {
						value = &eligible[index]
						break
					}
				}
				if value == nil {
					continue
				}
				audited := appendPresenceAuditPivot(fixture.output, stage, category, *value)
				compiled := compilePresenceAuditOutput(t, audited, project, generation, []string{allowedPath})
				scanned, err := CompileCategoryScanStageWithPolicy(compiled, stage.ID, category.ID, value.ID, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Logf("%s\t%s\t%s\tCOMPILE_REFUSED\t%v", fixture.name, stage.ID, category.Name, err)
					continue
				}
				presentFalse, missing, conclusive, rows, err := executePresenceAuditScan(ctx, client, scanned)
				if err != nil {
					t.Logf("%s\t%s\t%s\tEXECUTION_ERROR\t%v", fixture.name, stage.ID, category.Name, err)
					continue
				}
				outcome := "COMPLETE"
				if conclusive {
					outcome = "MISSING_UNSUPPORTED"
				} else if missing {
					outcome = "MISSING_TRACKED"
				}
				t.Logf("%s\t%s\t%s\t%s\ttracked=%t\tpresentFalse=%d\trows=%d",
					fixture.name, stage.ID, category.Name, outcome, scanned.Proof.PresenceTracked, presentFalse, rows)
			}
		}
	}
}

func presenceAuditUnpivotOutput() recipe.Output {
	return recipe.Output{
		Name: "presence_audit_unpivot", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "subject_ref", ColumnID: "subject_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "subject_id", Name: "subject_ref"}, {ID: "status_id", Name: "status"}, {ID: "category_id", Name: "category"},
			},
			Steps: []recipe.ConstructionStep{{
				ID:     "unpivot",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
					ConstructionID: "presence_audit_unpivot",
					Inputs: []recipe.ConstructionUnpivotInput{
						{ColumnID: "status_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: presenceAuditString("status")}},
						{ColumnID: "category_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: presenceAuditString("category")}},
					},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id",
					NullRowPolicy: recipe.UnpivotNullPreserve,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "subject_id", Name: "subject_ref"}, {ID: "measure_id", Name: "measure"}, {ID: "value_id", Name: "value"},
				},
			}},
		},
	}
}

func presenceAuditString(value string) *string { return &value }

func compilePresenceAuditOutput(t *testing.T, output recipe.Output, project, generation string, authPaths []string) lower.CompiledRecipeOutput {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: append([]string(nil), authPaths...),
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name,
		TranslationVersion: "presence-audit", Outputs: []recipe.Output{output},
	}
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
	return compiled.Outputs[0]
}

func executePresenceAuditScan(ctx context.Context, client *store.Client, scanned CompiledCategoryScanQuery) (presentFalse int, missing, conclusive bool, rows int, err error) {
	if witness := scanned.OverflowWitness; witness != nil {
		witnessRows := 0
		witnessMissing := false
		if err := client.QueryRows(ctx, witness.Query, 100, witness.BindVars, func(row map[string]any) error {
			present, ok := row[scanned.PresentColumn].(bool)
			if !ok {
				return fmt.Errorf("overflow witness has invalid presence marker")
			}
			witnessMissing = witnessMissing || !present
			witnessRows++
			return nil
		}); err != nil {
			return 0, false, false, 0, fmt.Errorf("execute overflow witness: %w", err)
		}
		if witnessRows > scanned.Proof.MaxValues {
			return 0, false, false, witnessRows, nil
		}
		if witnessMissing && !scanned.Proof.PresenceTracked {
			return 0, true, true, witnessRows, nil
		}
	}
	err = client.QueryRows(ctx, scanned.Query, 100, scanned.BindVars, func(row map[string]any) error {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			return fmt.Errorf("category scan row has invalid presence marker")
		}
		if !present {
			presentFalse++
		}
		rows++
		return nil
	})
	if err != nil {
		return 0, false, false, 0, fmt.Errorf("execute category scan: %w", err)
	}
	return presentFalse, presentFalse > 0, false, rows, nil
}

func seedPresenceAuditDocuments(t *testing.T, ctx context.Context, client *store.Client, project, generation, authPath string) {
	t.Helper()
	base := func(key, resourceType string) map[string]any {
		return map[string]any{
			"_key": project + "_" + key, "id": key, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": resourceType, "auth_resource_path": authPath,
		}
	}
	observations := []map[string]any{
		base("obs-all", "Observation"),
		base("obs-missing-status", "Observation"),
		base("obs-null-status", "Observation"),
		base("obs-active-status", "Observation"),
		base("obs-missing-integer", "Observation"),
		base("obs-null-integer", "Observation"),
		base("obs-missing-code", "Observation"),
		base("obs-null-code", "Observation"),
		base("obs-missing-quantity", "Observation"),
		base("obs-null-quantity", "Observation"),
		base("obs-missing-note", "Observation"),
		base("obs-null-note", "Observation"),
		base("obs-empty-note", "Observation"),
		base("obs-null-note-text", "Observation"),
	}
	for index := range observations {
		payload := map[string]any{"id": observations[index]["id"], "resourceType": "Observation", "subject": map[string]any{"reference": "Patient/subject"}}
		switch {
		case index == 0:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = map[string]any{"value": 3, "code": "d"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 1:
			payload["valueInteger"] = int64(5)
		case index == 2:
			payload["status"] = nil
			payload["valueInteger"] = int64(5)
		case index == 3:
			payload["status"], payload["valueInteger"] = "active", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["code"] = "final", map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": nil}, map[string]any{}}
		}
		observations[index]["payload"] = payload
	}
	patients := []map[string]any{
		base("pat-all", "Patient"),
		base("pat-missing-birth", "Patient"),
		base("pat-null-birth", "Patient"),
		base("pat-missing-active", "Patient"),
		base("pat-null-active", "Patient"),
		base("pat-missing-gender", "Patient"),
		base("pat-null-gender", "Patient"),
		base("pat-missing-multiple", "Patient"),
		base("pat-null-multiple", "Patient"),
	}
	for index := range patients {
		payload := map[string]any{"id": patients[index]["id"], "resourceType": "Patient"}
		if index != 1 && index != 3 && index != 5 && index != 7 {
			payload["birthDate"] = "2000-01-01"
			payload["active"] = true
			payload["gender"] = "female"
			payload["multipleBirthInteger"] = int64(2)
		}
		switch index {
		case 2:
			payload["birthDate"] = nil
		case 4:
			payload["active"] = nil
		case 6:
			payload["gender"] = nil
		case 8:
			payload["multipleBirthInteger"] = nil
		}
		patients[index]["payload"] = payload
	}
	insertPresenceAuditBatch(t, ctx, client, "Observation", observations)
	insertPresenceAuditBatch(t, ctx, client, "Patient", patients)
}

func insertPresenceAuditBatch(t *testing.T, ctx context.Context, client *store.Client, collection string, rows []map[string]any) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		document, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
		t.Fatalf("insert %s presence audit fixtures: %v", collection, err)
	}
}
