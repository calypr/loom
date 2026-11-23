package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
)

func assertReshapeOracleUnpivot(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot")
	output.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterIn, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a"), reshapeOracleStringFilter("empty-and-null")}}}
	compiled, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile preserving unpivot: %v", err)
	}
	if got := outputColumn(compiled.OutputSchema, "amount"); got != "decimal" {
		t.Fatalf("unpivot amount schema kind = %v, want decimal after INTEGER+DECIMAL widening", got)
	}
	if got := outputColumn(compiled.OutputSchema, "measure"); got != "string" {
		t.Fatalf("unpivot key schema kind = %v, want string", got)
	}
	if !reshapeOracleColumnNullable(compiled.OutputSchema, "amount") {
		t.Fatal("PRESERVE amount schema is not nullable")
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	t.Logf("unpivot PRESERVE identities: %q", reshapeOracleIdentities(rows))
	wantPreserve := []reshapeOracleUnpivotWant{
		{SourceID: "alpha-a", GroupText: "final", StringCategory: "alpha", NumericCategory: 1, Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a", Key: "a-decimal", Value: 2.5, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "a-decimal")},
		{SourceID: "alpha-a", GroupText: "final", StringCategory: "alpha", NumericCategory: 1, Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a", Key: "z-integer", Value: 1.0, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "z-integer")},
		{SourceID: "empty-and-null", GroupText: "preliminary", StringCategory: "", NumericCategory: 2, Flag: false, Text: "", Unrelated: "keep-empty-and-null", Key: "a-decimal", Value: nil, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "a-decimal")},
		{SourceID: "empty-and-null", GroupText: "preliminary", StringCategory: "", NumericCategory: 2, Flag: false, Text: "", Unrelated: "keep-empty-and-null", Key: "z-integer", Value: 2.0, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "z-integer")},
	}
	assertReshapeOracleUnpivotRows(t, rows, wantPreserve)
	assertReshapeOracleUnpivotCellTrace(t, ctx, client, compiled, project)
	_, limitedQuery, err := compileReshapeOracle(output, project, generation, 3)
	if err != nil {
		t.Fatalf("compile limited unpivot: %v", err)
	}
	limitedRows := executeReshapeOracleQuery(t, ctx, client, limitedQuery)
	assertReshapeOracleUnpivotRows(t, limitedRows, wantPreserve[:3])

	dropOutput := reshapeOracleUnpivotOutput(recipe.UnpivotNullDrop, "s04-unpivot-drop")
	dropOutput.Filters = append([]recipe.Filter(nil), output.Filters...)
	dropCompiled, dropQuery, err := compileReshapeOracle(dropOutput, project, generation, 100)
	if err != nil {
		t.Fatalf("compile dropping unpivot: %v", err)
	}
	if got := outputColumn(dropCompiled.OutputSchema, "amount"); got != "decimal" || reshapeOracleColumnNullable(dropCompiled.OutputSchema, "amount") {
		t.Fatalf("DROP amount schema = %v nullable=%t, want decimal non-null", got, reshapeOracleColumnNullable(dropCompiled.OutputSchema, "amount"))
	}
	dropRows := executeReshapeOracleQuery(t, ctx, client, dropQuery)
	t.Logf("unpivot DROP identities: %q", reshapeOracleIdentities(dropRows))
	wantDrop := []reshapeOracleUnpivotWant{wantPreserve[0], wantPreserve[1], wantPreserve[3]}
	for index := range wantDrop {
		wantDrop[index].RowID = reshapeOracleUnpivotID(project, "s04-unpivot-drop", wantDrop[index].SourceID, wantDrop[index].Key)
	}
	assertReshapeOracleUnpivotRows(t, dropRows, wantDrop)
	droppedNullIdentity := currentReshapeOracleUnpivotIdentity(t, reshapeOracleUnpivotID(project, "s04-unpivot-drop", "empty-and-null", "a-decimal"))
	for _, identity := range reshapeOracleIdentities(dropRows) {
		if identity == droppedNullIdentity {
			t.Fatalf("DROP output unexpectedly retained null-row identity %q", droppedNullIdentity)
		}
	}

	repeated := executeReshapeOracleQuery(t, ctx, client, query)
	if got, want := reshapeOracleIdentities(repeated), reshapeOracleIdentities(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated unpivot identity order = %#v, want %#v", got, want)
	}

	ordered := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot-order")
	ordered.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a")}}}
	compiledOrder, orderedQuery, err := compileReshapeOracle(ordered, project, generation, 100)
	if err != nil {
		t.Fatalf("compile ordered unpivot: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiledOrder.Plan)
	if err != nil {
		t.Fatalf("render unwindowed compiler plan: %v", err)
	}
	rawRows, err := queryReshapeOracle(ctx, client, model.CompiledQuery{Query: rendered.Query, BindVars: rendered.BindVars})
	if err != nil {
		t.Fatalf("execute unwindowed compiler plan: %v\n%s", err, rendered.Query)
	}
	assertCurrentUnpivotAuthoredSlots(t, compiledOrder, rendered.Query, rendered.BindVars)
	if len(rawRows) != 2 || rawRows[0]["measure"] != "a-decimal" || rawRows[1]["measure"] != "z-integer" {
		t.Fatalf("current raw Unpivot canonical keys = %#v, want [a-decimal, z-integer]", rawRows)
	}
	windowRows := executeReshapeOracleQuery(t, ctx, client, orderedQuery)
	if len(windowRows) != 2 || windowRows[0]["measure"] != "a-decimal" || windowRows[1]["measure"] != "z-integer" {
		t.Fatalf("stable-sorted unpivot keys = %#v, want [a-decimal, z-integer]", windowRows)
	}
	for _, row := range windowRows {
		if got, ok := row["__loom_row_id"].(string); !ok || !strings.Contains(got, fmt.Sprintf(`["STRING","%v"]`, row["measure"])) {
			t.Errorf("unpivot identity %v does not include its selected input key %v", row["__loom_row_id"], row["measure"])
		}
	}
}

func assertReshapeOracleUnpivotCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, project string) {
	t.Helper()
	_, amount := executeReshapeOracleTrace(t, ctx, client, output, "amount")
	alphaID := currentReshapeOracleUnpivotIdentity(t, reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "a-decimal"))
	alphaTrace, exists := amount[alphaID]
	if !exists {
		t.Fatalf("Unpivot trace lacks current row identity %q; actual traces: %#v", alphaID, amount)
	}
	assertReshapeTraceResult(t, alphaTrace, float64(2.5), "VALUE", reshapeOracleContributors("alpha-a", float64(2.5)), "")
	nullID := currentReshapeOracleUnpivotIdentity(t, reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "a-decimal"))
	nullTrace, exists := amount[nullID]
	if !exists {
		t.Fatalf("Unpivot null trace lacks current row identity %q; actual traces: %#v", nullID, amount)
	}
	assertReshapeTraceResult(t, nullTrace, nil, "RECORDED_NULL", reshapeOracleContributors("empty-and-null", nil), "")
	trace, measure := executeReshapeOracleTrace(t, ctx, client, output, "measure")
	keyCell := measure[alphaID]
	if keyCell.value != "a-decimal" || keyCell.status != "NO_MATCH" || keyCell.omission != ir.PhysicalCellTraceGeneratedKeyOmission || len(keyCell.contributors) != 0 {
		t.Fatalf("unpivot generated-key evidence = %#v, want explicit no-source omission", keyCell)
	}
	if trace.ExplicitIdentityColumn == "" {
		t.Fatal("unpivot trace does not preserve stable row identity")
	}
}

func reshapeOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy, constructionID string) recipe.Output {
	return reshapeOracleOutput("unpivot", &oracleReshape{
		Kind: oracleReshapeUnpivot,
		Unpivot: &oracleUnpivot{
			ConstructionID: constructionID,
			Inputs: []oracleUnpivotInput{
				{Column: "group_number", Key: reshapeOracleString("z-integer")},
				{Column: "numeric_value", Key: reshapeOracleString("a-decimal")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: policy,
		},
	})
}

func reshapeOracleUnpivotID(project, constructionID, sourceID, key string) string {
	return fmt.Sprintf(`[["project","%s"],["_key","%s_%s"],["%s","TABLE_UNPIVOT"],["STRING","%s"]]`, project, project, sourceID, constructionID, key)
}

func assertReshapeOracleUnpivotRows(t *testing.T, rows []map[string]any, wants []reshapeOracleUnpivotWant) {
	t.Helper()
	if len(rows) != len(wants) {
		t.Fatalf("unpivot rows = %#v, want %d typed rows", rows, len(wants))
	}
	for index, want := range wants {
		row := rows[index]
		var legacyParts [][]string
		if err := json.Unmarshal([]byte(want.RowID), &legacyParts); err != nil {
			t.Fatal(err)
		}
		actualID, ok := row["__loom_row_id"].(string)
		if !ok {
			t.Fatalf("Unpivot raw current identity is not a string: %#v", row["__loom_row_id"])
		}
		if legacy := legacyReshapeOracleUnpivotIdentity(t, legacyParts[0][1], actualID); legacy != want.RowID {
			t.Errorf("current Unpivot identity does not round-trip to unchanged legacy oracle: %s != %s", legacy, want.RowID)
		}
		for column, expected := range map[string]any{
			"source_id": want.SourceID, "group_text": want.GroupText,
			"string_category": want.StringCategory, "numeric_category": float64(want.NumericCategory), "flag_value": want.Flag,
			"text_value": want.Text, "unrelated": want.Unrelated, "measure": want.Key, "amount": want.Value, "__loom_row_id": currentReshapeOracleUnpivotIdentity(t, want.RowID),
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("unpivot row %d.%s = %#v, want literal %#v", index, column, got, expected)
			}
		}
		if _, ok := row["amount"].(string); ok {
			t.Errorf("unpivot numeric value %q was coerced to string: %#v", want.Key, row["amount"])
		}
	}
}

func assertReshapeOracleUnpivotRejectsIncompatibleInputs(t *testing.T, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("incompatible_unpivot", &oracleReshape{
		Kind: oracleReshapeUnpivot,
		Unpivot: &oracleUnpivot{
			ConstructionID: "s04-unpivot-incompatible",
			Inputs: []oracleUnpivotInput{
				{Column: "numeric_value", Key: reshapeOracleString("number")},
				{Column: "text_value", Key: reshapeOracleString("text")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	})
	if _, _, err := compileReshapeOracle(output, project, generation, 100); err == nil || !strings.Contains(err.Error(), "incompatible with previous inputs") {
		t.Fatalf("incompatible unpivot compile error = %v, want incompatible input types", err)
	}
}

func currentReshapeOracleUnpivotIdentity(t *testing.T, legacy string) string {
	t.Helper()
	var parts [][]string
	if err := json.Unmarshal([]byte(legacy), &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 4 || len(parts[0]) != 2 || parts[0][0] != "project" || len(parts[1]) != 2 || parts[1][0] != "_key" || !strings.HasPrefix(parts[1][1], parts[0][1]+"_") || len(parts[2]) != 2 || parts[2][1] != "TABLE_UNPIVOT" || len(parts[3]) != 2 || parts[3][0] != "STRING" {
		t.Fatalf("invalid legacy Unpivot identity witness: %s", legacy)
	}
	current, err := json.Marshal(parts[1:])
	if err != nil {
		t.Fatal(err)
	}
	return string(current)
}

func legacyReshapeOracleUnpivotIdentity(t *testing.T, project, current string) string {
	t.Helper()
	var parts [][]string
	if err := json.Unmarshal([]byte(current), &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || len(parts[0]) != 2 || parts[0][0] != "_key" || !strings.HasPrefix(parts[0][1], project+"_") || len(parts[1]) != 2 || parts[1][1] != "TABLE_UNPIVOT" || len(parts[2]) != 2 || parts[2][0] != "STRING" {
		t.Fatalf("invalid current Unpivot identity witness: %s", current)
	}
	legacy, err := json.Marshal(append([][]string{{"project", project}}, parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(legacy)
}

func TestCurrentUnpivotOracleIdentityMapping(t *testing.T) {
	const old = `[["project","p"],["_key","p_source"],["shape","TABLE_UNPIVOT"],["STRING","amount"]]`
	const want = `[["_key","p_source"],["shape","TABLE_UNPIVOT"],["STRING","amount"]]`
	if got := legacyReshapeOracleUnpivotIdentity(t, "p", want); got != old {
		t.Fatalf("legacy identity = %s, want %s", got, old)
	}
	if got := currentReshapeOracleUnpivotIdentity(t, old); got != want {
		t.Fatalf("current identity = %s, want %s", got, want)
	}
}

func assertCurrentUnpivotAuthoredSlots(t *testing.T, output lower.CompiledRecipeOutput, query string, bindings map[string]any) {
	t.Helper()
	if output.Plan.StageSequence == nil || len(output.Plan.StageSequence.Stages) != 1 || output.Plan.StageSequence.Stages[0].Unpivot == nil {
		t.Fatal("missing current Unpivot stage")
	}
	u := output.Plan.StageSequence.Stages[0].Unpivot
	if len(u.Inputs) != 2 || u.Inputs[0].Column != "group_number" || u.Inputs[1].Column != "numeric_value" || bindings[u.Inputs[0].KeyBindKey] != "z-integer" || bindings[u.Inputs[1].KeyBindKey] != "a-decimal" || u.Inputs[0].KeyKind != "STRING" || u.Inputs[1].KeyKind != "STRING" {
		t.Fatalf("Unpivot authored slot order changed: %#v, bindings=%#v", u.Inputs, bindings)
	}
	first := strings.Index(query, "{key: @"+u.Inputs[0].KeyBindKey+",")
	second := strings.Index(query, "{key: @"+u.Inputs[1].KeyBindKey+",")
	if first < 0 || second <= first {
		t.Fatalf("AQL slot enumeration lost authored [z-integer, a-decimal] order: %s", query)
	}
	if !strings.Contains(query, "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("current Unpivot lost canonical identity ordering: %s", query)
	}
}

func TestCurrentUnpivotPreservesAuthoredSlotsAndCanonicalRendererOrdering(t *testing.T) {
	compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot-order"), "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentUnpivotAuthoredSlots(t, compiled, rendered.Query, rendered.BindVars)
}
