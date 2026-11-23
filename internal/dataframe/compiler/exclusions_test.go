package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestS04CurrentConstructionFixtureCompilation(t *testing.T) {
	output := reshapeOracleOutput("sum", &oracleReshape{Kind: oracleReshapePivot, GroupedPivot: &oraclePivot{ConstructionID: "s04-pivot-sum", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value", Categories: []oraclePivotCategory{{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}, {Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"}}, DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence}})
	output.DerivedColumns = []recipe.DerivedColumn{{ConstructionID: "s04-derived-after-pivot", Name: "alpha_plus_zero", Operation: recipe.DerivedAdd, Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"}, Right: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStagePivotOp || compiled.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageDeriveOp {
		t.Fatal("S04 fixture bypassed current Pivot/Derive stages")
	}
	for _, stage := range compiled.Plan.StageSequence.Stages {
		found := false
		for _, column := range stage.OutputColumns {
			if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
				found = true
			}
		}
		if !found {
			t.Fatalf("stage %s dropped compiler-owned Pivot exclusion count", stage.ID)
		}
	}
	foundCount := false
	for _, column := range compiled.Plan.StageSequence.FinalColumns {
		if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
			foundCount = true
		}
	}
	if !foundCount {
		t.Fatal("final output dropped Pivot exclusion count")
	}
	if _, err := celltrace.CompileCellTraceOutputWithPolicy(compiled, "alpha_plus_zero", 0, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatal(err)
	}
	assertReshapeOracleUnpivotRejectsIncompatibleInputs(t, "project", "generation")
	for _, policy := range []recipe.UnpivotNullRowPolicy{recipe.UnpivotNullPreserve, recipe.UnpivotNullDrop} {
		compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(policy, "s04-unpivot"), "project", "generation", 100)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Plan.StageSequence == nil || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageUnpivotOp {
			t.Fatal("S04 fixture bypassed current Unpivot stage")
		}
		if outputColumn(compiled.OutputSchema, "amount") != "decimal" {
			t.Fatal("S04 integer/decimal unpivot widening changed")
		}
	}
}

func currentNullMissingPivotOutput() recipe.Output {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)
	output.Name = "current_null_missing_exclusions"
	output.Construction.Steps[0].Operation.Pivot.Categories = []recipe.ConstructionPivotCategory{
		{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_value"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_value"},
	}
	output.Construction.Steps[0].Outputs = []recipe.StageColumn{
		{ID: "group_text", Name: "group_text"},
		{ID: "group_number", Name: "group_number"},
		{ID: "null_value", Name: "null_value"},
		{ID: "missing_value", Name: "missing_value"},
	}
	return output
}

func TestCurrentPivotPreservesSourceCategoryPresence(t *testing.T) {
	for _, test := range []struct {
		name      string
		output    recipe.Output
		fromInput bool
	}{
		{name: "literal categories", output: currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)},
		{name: "NULL and MISSING categories", output: currentNullMissingPivotOutput(), fromInput: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, _, err := compileCurrentConstructionOracle(test.output, "private-project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatal(err)
			}
			original := compiled.Plan.StageSequence.Stages[0].GroupedPivot
			if original.CategoryPresenceFromInput != test.fromInput {
				t.Fatalf("category-presence path = %t, want %t", original.CategoryPresenceFromInput, test.fromInput)
			}
			source := compiled.Plan.Operations[len(compiled.Plan.Operations)-1].Return.Projections
			var originalCategory *ir.PhysicalProjection
			for i := range source {
				if source[i].Name == original.CategoryColumn {
					originalCategory = &source[i]
				}
			}
			if originalCategory == nil {
				t.Fatal("source category projection is missing")
			}
		})
	}
}

func TestCurrentPivotNullMissingPartitionAgainstArango(t *testing.T) {
	endpoint, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if endpoint == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must name the supplied local Arango fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.OpenExisting(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_null_missing_exclusion_" + uuid.NewString()
	const generation = "generation-current-null-missing-exclusion"

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.QueryRows(cleanupCtx, `FOR d IN Observation FILTER d.project == @project AND d.dataset_generation == @generation REMOVE d IN Observation RETURN {_key: OLD._key}`, 10, map[string]any{"project": project, "generation": generation}, func(map[string]any) error { return nil }); err != nil {
			t.Errorf("remove owned exclusion fixture: %v", err)
		}
	}()
	documents := make([]json.RawMessage, 0, 3)
	for _, row := range []struct {
		id    string
		code  map[string]any
		value int
	}{
		{id: "null", code: map[string]any{"text": nil}, value: 10},
		{id: "missing", code: map[string]any{}, value: 20},
		{id: "other", code: map[string]any{"text": "other"}, value: 30},
	} {
		payload := map[string]any{"resourceType": "Observation", "id": row.id, "status": "final", "valueInteger": 1, "code": row.code, "valueQuantity": map[string]any{"value": row.value}}
		raw, err := json.Marshal(map[string]any{"_key": project + "_" + row.id, "id": row.id, "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, raw)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatal(err)
	}
	compiled, preview, err := compileCurrentConstructionOracle(currentNullMissingPivotOutput(), project, generation, 10)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(previewRows) != 1 {
		t.Fatalf("preview rows = %#v, want one final group", previewRows)
	}
	row := previewRows[0]
	for name, expected := range map[string]any{"group_text": "final", "group_number": float64(1), "null_value": float64(10), "missing_value": float64(20), "__loom_reshape_unlisted_count": float64(1)} {
		if row[name] != expected {
			t.Fatalf("preview %s = %#v, want %#v", name, row[name], expected)
		}
	}
	const rowID = `["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",1]]`
	if row["__loom_row_id"] != rowID {
		t.Fatalf("preview identity = %#v, want %s", row["__loom_row_id"], rowID)
	}
	after, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preview execution mutated the source plan")
	}
}

func TestCurrentPivotWithUnlistedCountCompiles(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Plan.StageSequence == nil || compiled.Plan.StageSequence.Stages[0].GroupedPivot == nil {
		t.Fatal("missing typed Pivot stage")
	}
}

func TestCurrentPivotWithPostPivotFilterCompiles(t *testing.T) {
	output := currentOracleDerivedPivotOutput(true)
	output.Construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy = recipe.PivotUnlistedCategoryExcludeWithEvidence
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	stages := compiled.Plan.StageSequence.Stages
	if len(stages) != 3 || stages[2].Filter == nil {
		t.Fatal("post-Pivot filter was dropped")
	}
}

func TestCurrentPivotWithPostPivotDeriveCompiles(t *testing.T) {
	output := reshapeOracleOutput("Observations", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "exclusion-test", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []oraclePivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "derived-after-pivot", Name: "after", Label: "After", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	compiledOutput, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	stages := compiledOutput.Plan.StageSequence.Stages
	if len(stages) != 2 || stages[0].GroupedPivot == nil || stages[1].Kind != ir.PhysicalStageDeriveOp {
		t.Fatal("Pivot and Derive stages were not retained")
	}
}

func TestCurrentUnpivotAndErrorPolicyPivotCompile(t *testing.T) {
	unsupported := compileForwardReshapeFixture(t, bindOracleCurrentConstruction(recipe.Output{
		Name: "Unpivot", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	}, &oracleReshape{Kind: oracleReshapeUnpivot, Unpivot: &oracleUnpivot{
		ConstructionID: "u", Inputs: []oracleUnpivotInput{{Column: "id", Key: reshapeOracleString("id")}},
		KeyOutput: "key", KeyLabel: "Key", ValueOutput: "value", ValueLabel: "Value", NullRowPolicy: recipe.UnpivotNullPreserve,
	}}))
	if unsupported.Plan.StageSequence.Stages[0].Unpivot == nil {
		t.Fatal("missing typed Unpivot")
	}
	noPolicy := compileForwardReshapeFixture(t, bindOracleCurrentConstruction(recipe.Output{
		Name: "NoPolicy", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "category", Expr: recipe.Expression{Select: "root.status"}}, {Name: "value", Expr: recipe.Expression{Select: "root.valueInteger"}}},
	}, &oracleReshape{Kind: oracleReshapePivot, GroupedPivot: &oraclePivot{
		ConstructionID: "p", GroupKeys: []string{"id"}, CategoryColumn: "category", ValueColumn: "value",
		Categories:      []oraclePivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
		DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
		UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
	}}))
	if noPolicy.Plan.StageSequence.Stages[0].GroupedPivot == nil {
		t.Fatal("missing typed error-policy Pivot")
	}
}

func compileForwardReshapeFixture(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestCurrentPivotCompilesWithAndWithoutLiteralCategories(t *testing.T) {
	for _, test := range []struct {
		name        string
		withLiteral bool
	}{
		{name: "NULL and MISSING only"},
		{name: "NULL MISSING and literal", withLiteral: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := currentNullMissingPivotOutput()
			if test.withLiteral {
				known := "known"
				pivot := output.Construction.Steps[0].Operation.Pivot
				pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{
					Key:            recipe.TableScalar{Kind: recipe.TableScalarString, String: &known},
					OutputColumnID: "literal_value",
				})
				output.Construction.Steps[0].Outputs = append(output.Construction.Steps[0].Outputs,
					recipe.StageColumn{ID: "literal_value", Name: "literal_value"})
			}
			compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 10)
			if err != nil {
				t.Fatal(err)
			}
			pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
			wantCategories := 2
			if test.withLiteral {
				wantCategories++
			}
			if got := len(pivot.Categories); got != wantCategories {
				t.Fatalf("typed Pivot categories = %d, want %d", got, wantCategories)
			}
		})
	}
}
