package compiler

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestPivotLineagePartitionsForwardRowsAndTraceReconstructsAgainstArango(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := arangotest.Owned(t, "lineageagreement")
	client := database.Client
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_lineage_agreement_" + uuid.NewString()
	const generation = "lineage-agreement-generation"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx, "FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation", map[string]any{"project": project}); err != nil {
			t.Errorf("remove lineage agreement fixture: %v", err)
		}
	})
	insertReshapeAgreementFixture(t, ctx, client, project, generation)

	pivot := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	unlisted := "unlisted"
	pivot.Construction.Steps[0].Operation.Pivot.Categories = append(pivot.Construction.Steps[0].Operation.Pivot.Categories,
		recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &unlisted}, OutputColumnID: "unlisted_value"})
	pivot.Construction.Steps[0].Outputs = append(pivot.Construction.Steps[0].Outputs, recipe.StageColumn{ID: "unlisted_value", Name: "unlisted_value"})
	pivotCompiled, pivotPreview, err := compileCurrentConstructionOracle(pivot, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	forwardRows := executeReshapeOracleQuery(t, ctx, client, pivotPreview)
	if len(forwardRows) != 4 {
		t.Fatalf("forward Pivot rows = %d, want four typed group keys: %#v", len(forwardRows), forwardRows)
	}

	sourceByID := make(map[string]reshapeOracleSourceRow, len(reshapeOracleSourceRows))
	for _, source := range reshapeOracleSourceRows {
		sourceByID[source.SourceID] = source
	}
	seen := make(map[string]string, len(sourceByID))
	targetRow := findAgreementRow(t, forwardRows, "final", 1)
	targetID := targetRow["__loom_row_id"].(string)
	for _, outputRow := range forwardRows {
		rowID := outputRow["__loom_row_id"].(string)
		lineage, err := rowlineage.CompileRowLineageOutput(pivotCompiled, rowID, 0, 50, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile Pivot lineage for %s: %v", rowID, err)
		}
		result := executeRowLineageOracleQuery(t, ctx, client, lineage)
		contributors := lineageContributorRows(t, result)
		if result["found"] != true || len(contributors) == 0 {
			t.Fatalf("forward Pivot row %s has no complete lineage: %#v", rowID, result)
		}
		for _, contributor := range contributors {
			sourceID, ok := contributor["resourceId"].(string)
			if !ok {
				t.Fatalf("lineage contributor has no source ID: %#v", contributor)
			}
			source, ok := sourceByID[sourceID]
			if !ok {
				t.Fatalf("lineage names a source outside the forward fixture: %#v", contributor)
			}
			if source.Group.Text.String == nil || *source.Group.Text.String != outputRow["group_text"] ||
				source.Group.Numeric.Integer == nil || float64(*source.Group.Numeric.Integer) != outputRow["group_number"] {
				t.Fatalf("source %s does not satisfy forward group key for row %#v", sourceID, outputRow)
			}
			if prior, duplicate := seen[sourceID]; duplicate {
				t.Fatalf("source %s belongs to both Pivot rows %s and %s", sourceID, prior, rowID)
			}
			seen[sourceID] = rowID
		}
	}
	if len(seen) != len(sourceByID) {
		t.Fatalf("Pivot lineage partition covered %d of %d forward sources: %#v", len(seen), len(sourceByID), seen)
	}

	alphaTrace := compileAndFindCellTrace(t, ctx, client, pivotCompiled, "alpha", targetID)
	alphaSources := map[string]float64{}
	for _, item := range traceContributions(t, alphaTrace, ir.PhysicalCellTraceContributionsField) {
		id := item["resourceId"].(string)
		alphaSources[id] = item["value"].(float64)
	}
	if len(alphaSources) != 2 || alphaSources["alpha-a"] != 2.5 || alphaSources["alpha-b"] != 4.5 ||
		alphaSources["beta"] != 0 || alphaSources["zero"] != 0 {
		t.Fatalf("Pivot alpha trace contributors = %#v, want alpha-a=2.5 and alpha-b=4.5", alphaSources)
	}
	if total := alphaSources["alpha-a"] + alphaSources["alpha-b"]; total != targetRow["alpha"] {
		t.Fatalf("reconstructed alpha = %v, forward Pivot cell = %#v", total, targetRow["alpha"])
	}

	derivedOutput := currentOracleDerivedPivotOutput(true)
	derivedOutput.Construction.Steps[0].Operation.Pivot.Categories = append(derivedOutput.Construction.Steps[0].Operation.Pivot.Categories,
		recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &unlisted}, OutputColumnID: "unlisted_value"})
	derivedOutput.Construction.Steps[0].Outputs = append(derivedOutput.Construction.Steps[0].Outputs, recipe.StageColumn{ID: "unlisted_value", Name: "unlisted_value"})
	derivedOutput.Construction.Steps[1].Outputs = append(derivedOutput.Construction.Steps[1].Outputs, recipe.StageColumn{ID: "unlisted_value", Name: "unlisted_value"})
	derivedOutput.Construction.Steps[2].Outputs = append(derivedOutput.Construction.Steps[2].Outputs, recipe.StageColumn{ID: "unlisted_value", Name: "unlisted_value"})
	derived, derivedPreview, err := compileCurrentConstructionOracle(derivedOutput, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	derivedRows := executeReshapeOracleQuery(t, ctx, client, derivedPreview)
	derivedRow := findAgreementRow(t, derivedRows, "final", 1)
	if derivedRow["__loom_row_id"] != targetID {
		t.Fatalf("Pivot/Derive identity = %#v, Pivot identity = %q", derivedRow["__loom_row_id"], targetID)
	}
	derivedTrace := compileAndFindCellTrace(t, ctx, client, derived, "alpha_plus_zero", targetID)
	inputs := traceContributions(t, derivedTrace, ir.PhysicalCellTraceContributionsField)
	inputValues := make(map[string]float64, len(inputs))
	for _, input := range inputs {
		column, ok := input["inputColumn"].(string)
		value, valueOK := input["value"].(float64)
		if !ok || !valueOK {
			t.Fatalf("derived trace input has unexpected shape: %#v", input)
		}
		inputValues[column] = value
	}
	if len(inputValues) != 2 || inputValues["alpha"] != targetRow["alpha"] || inputValues["zero"] != targetRow["zero"] {
		t.Fatalf("derived trace inputs = %#v, forward cells alpha=%#v zero=%#v", inputValues, targetRow["alpha"], targetRow["zero"])
	}
	reconstructed := inputValues["alpha"] + inputValues["zero"]
	if math.IsNaN(reconstructed) || reconstructed != derivedRow["alpha_plus_zero"] {
		t.Fatalf("reconstructed alpha_plus_zero = %v, forward derived cell = %#v", reconstructed, derivedRow["alpha_plus_zero"])
	}

	unpivotOutput := currentOracleUnpivotOutput(recipe.UnpivotNullPreserve)
	unpivoted, unpivotPreview, err := compileCurrentConstructionOracle(unpivotOutput, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	unpivotRows := executeReshapeOracleQuery(t, ctx, client, unpivotPreview)
	var unpivotRow map[string]any
	for _, row := range unpivotRows {
		if row["source_id"] == "alpha-a" && row["measure"] == "a-decimal" {
			unpivotRow = row
			break
		}
	}
	if unpivotRow == nil {
		t.Fatalf("forward UNPIVOT has no alpha-a decimal row: %#v", unpivotRows)
	}
	unpivotID := unpivotRow["__loom_row_id"].(string)
	unpivotTrace := compileAndFindCellTrace(t, ctx, client, unpivoted, "amount", unpivotID)
	if unpivotTrace[ir.PhysicalCellTraceValueField] != unpivotRow["amount"] {
		t.Fatalf("UNPIVOT trace value = %#v, forward value = %#v", unpivotTrace[ir.PhysicalCellTraceValueField], unpivotRow["amount"])
	}
	unpivotSources := traceContributions(t, unpivotTrace, ir.PhysicalCellTraceContributionsField)
	if len(unpivotSources) != 1 || unpivotSources[0]["resourceId"] != "alpha-a" || unpivotSources[0]["value"] != unpivotRow["amount"] {
		t.Fatalf("UNPIVOT trace sources = %#v, want alpha-a's forward amount", unpivotSources)
	}
}

func insertReshapeAgreementFixture(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(reshapeOracleSourceRows))
	for _, row := range reshapeOracleSourceRows {
		payload := map[string]any{
			"id": row.SourceID, "resourceType": "Observation", "status": *row.Group.Text.String,
			"valueInteger": *row.NumericCategory.Integer, "valueString": row.Text,
			"code":         map[string]any{"text": *row.StringCategory.String},
			"valueBoolean": row.Flag, "issued": row.Unrelated,
		}
		if row.Number == nil {
			payload["valueQuantity"] = map[string]any{"value": nil}
		} else {
			payload["valueQuantity"] = map[string]any{"value": *row.Number}
		}
		encoded, err := json.Marshal(map[string]any{
			"_key": project + "_" + row.SourceID, "id": row.SourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert lineage agreement fixture: %v", err)
	}
}

func findAgreementRow(t *testing.T, rows []map[string]any, group string, number float64) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["group_text"] == group && row["group_number"] == number {
			return row
		}
	}
	t.Fatalf("forward rows have no group (%s, %v): %#v", group, number, rows)
	return nil
}

func compileAndFindCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column, rowID string) map[string]any {
	t.Helper()
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile trace for %s: %v", column, err)
	}
	var match map[string]any
	var returned []map[string]any
	if err := client.QueryRows(ctx, trace.Query, 100, trace.BindVars, func(row map[string]any) error {
		var identity any
		returned = append(returned, row)
		if trace.ExplicitIdentityColumn != "" {
			identity = row[trace.ExplicitIdentityColumn]
		} else if trace.IdentityPartsColumn != "" {
			identity = row[trace.IdentityPartsColumn]
		}
		if trace.ExplicitIdentityColumn != "" {
			if identity == rowID {
				match = row
			}
			return nil
		}
		encoded, _ := json.Marshal(identity)
		if string(encoded) == rowID {
			match = row
		}
		return nil
	}); err != nil {
		t.Fatalf("execute %s trace query: %v\n%s", column, err, trace.Query)
	}
	if match == nil {
		t.Fatalf("trace for %s did not return forward row identity %s, identity columns=(%q,%q), rows=%#v", column, rowID, trace.ExplicitIdentityColumn, trace.IdentityPartsColumn, returned)
	}
	if match[trace.OmissionColumn] != nil && match[trace.OmissionColumn] != "" {
		t.Fatalf("trace for %s reports omission %v", column, match[trace.OmissionColumn])
	}
	return match
}

func traceContributions(t *testing.T, row map[string]any, column string) []map[string]any {
	t.Helper()
	raw, ok := row[column].([]any)
	if !ok {
		t.Fatalf("trace %s = %#v, want array", column, row[column])
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("trace contributor = %#v, want object", item)
		}
		items = append(items, value)
	}
	return items
}

func TestGroupRowLineagePartitionsForwardRowsAgainstArango(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := arangotest.Owned(t, "grouplineage")
	client := database.Client
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_group_lineage_" + uuid.NewString()
	const generation = "group-lineage-generation"
	insertReshapeAgreementFixture(t, ctx, client, project, generation)

	output := currentOracleSource("group_lineage_agreement")
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group-lineage-agreement",
			Keys: []recipe.ConstructionGroupKey{
				{InputColumnID: "group_text", OutputColumnID: "group_text_id"},
				{InputColumnID: "group_number", OutputColumnID: "group_number_id"},
			},
			Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"}, {ID: "rows_id", Name: "rows"},
		},
	}}
	compiled, preview, err := compileCurrentConstructionOracle(output, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(rows) != 4 {
		t.Fatalf("forward Group rows = %d, want four group keys: %#v", len(rows), rows)
	}
	seen := make(map[string]string, len(reshapeOracleSourceRows))
	for _, row := range rows {
		rowID := row["__loom_row_id"].(string)
		query, err := rowlineage.CompileRowLineageOutput(compiled, rowID, 0, 50, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile Group lineage for %s: %v", rowID, err)
		}
		result := executeRowLineageOracleQuery(t, ctx, client, query)
		contributors := lineageContributorRows(t, result)
		if result["found"] != true || len(contributors) == 0 {
			t.Fatalf("Group row %s has no complete lineage: %#v", rowID, result)
		}
		for _, contributor := range contributors {
			sourceID, ok := contributor["resourceId"].(string)
			if !ok {
				t.Fatalf("Group contributor has no source ID: %#v", contributor)
			}
			source := reshapeSourceByID(t, sourceID)
			if *source.Group.Text.String != row["group_text"] || float64(*source.Group.Numeric.Integer) != row["group_number"] {
				t.Fatalf("source %s violates Group key for row %#v", sourceID, row)
			}
			if prior, duplicate := seen[sourceID]; duplicate {
				t.Fatalf("source %s belongs to both Group rows %s and %s", sourceID, prior, rowID)
			}
			seen[sourceID] = rowID
		}
	}
	if len(seen) != len(reshapeOracleSourceRows) {
		t.Fatalf("Group lineage covered %d of %d forward sources", len(seen), len(reshapeOracleSourceRows))
	}
}

func TestFilteredSourceCellTraceMatchesForwardValueAgainstArango(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := arangotest.Owned(t, "filtertrace")
	client := database.Client
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_filter_trace_" + uuid.NewString()
	const generation = "filter-trace-generation"
	insertReshapeAgreementFixture(t, ctx, client, project, generation)

	output := currentOracleSource("filter_trace_agreement")
	status := "final"
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "group_text", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &status}},
		}},
		Outputs: append([]recipe.StageColumn(nil), output.Construction.SourceColumns...),
	}}
	compiled, preview, err := compileCurrentConstructionOracle(output, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	forwardRows := executeReshapeOracleQuery(t, ctx, client, preview)
	var forward map[string]any
	for _, row := range forwardRows {
		if row["source_id"] == "alpha-a" {
			forward = row
			break
		}
	}
	if forward == nil {
		t.Fatal("forward Filter omitted the matching source row")
	}
	identityParts := make([]any, 0, len(compiled.RowIdentity.Fields))
	for _, field := range compiled.RowIdentity.Fields {
		switch field {
		case "project":
			identityParts = append(identityParts, project)
		case "_key":
			identityParts = append(identityParts, project+"_alpha-a")
		default:
			identityParts = append(identityParts, forward[field])
		}
	}
	encodedIdentity, err := json.Marshal(identityParts)
	if err != nil {
		t.Fatal(err)
	}
	trace := compileAndFindCellTrace(t, ctx, client, compiled, "source_id", string(encodedIdentity))
	if trace[ir.PhysicalCellTraceValueField] != forward["source_id"] {
		t.Fatalf("filtered source trace = %#v, forward value = %#v", trace[ir.PhysicalCellTraceValueField], forward["source_id"])
	}
	contributors := traceContributions(t, trace, ir.PhysicalCellTraceContributionsField)
	if len(contributors) != 1 || contributors[0]["inputColumn"] != "source_id" ||
		contributors[0]["value"] != "alpha-a" || contributors[0]["operation"] != "SOURCE_PROJECTION" {
		t.Fatalf("filtered source trace contributors = %#v, want the source_id projection value", contributors)
	}
}

func reshapeSourceByID(t *testing.T, id string) reshapeOracleSourceRow {
	t.Helper()
	for _, row := range reshapeOracleSourceRows {
		if row.SourceID == id {
			return row
		}
	}
	t.Fatalf("unknown reshape fixture source %q", id)
	return reshapeOracleSourceRow{}
}
