package execution

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/google/uuid"
)

func TestConstructionDerivedCellTraceReturnsValueAndStageLineageAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: url, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	client := arangoBackend.Client
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, arangofactory.BootstrapSpec{Collections: []arangofactory.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_construction_trace_" + uuid.NewString()
	generation := "generation-construction-trace"
	wants := []struct {
		id     string
		amount int64
		total  float64
	}{{id: "trace-a", amount: 3, total: 5}, {id: "trace-b", amount: 5, total: 7}}
	documents := make([]json.RawMessage, 0, len(wants))
	for _, want := range wants {
		key := project + "_" + want.id
		document, marshalErr := json.Marshal(map[string]any{
			"_key": key, "id": want.id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation",
			"payload": map[string]any{"id": want.id, "resourceType": "Observation", "status": "active", "valueInteger": want.amount},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction trace fixture: %v", err)
	}

	output := executionConstructionTraceOutput()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction-trace", TranslationVersion: "test", Outputs: []recipe.Output{output}}
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
	compiledOutput := compiled.Outputs[0]
	trace, err := compiler.CompileCellTraceOutput(compiler.ArangoTarget(), compiledOutput, "total", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{compileTarget: compiler.ArangoTarget(), batchSize: 100, sourceRows: func(queryCtx context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batchSize := request.BatchSize
		query, bindVars, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return client.QueryRows(queryCtx, query, batchSize, bindVars, arangofactory.RowVisitor(visit))
	}}
	for _, want := range wants {
		// Source identities include both tenant scope and the inserted Arango key.
		// Supplying only the first compiled field loses one of those parts.
		identity := map[string]any{"project": project, "_key": project + "_" + want.id}
		if err := ensureStableRowIdentity(identity, compiledOutput.RowIdentity, nil); err != nil {
			t.Fatalf("build expected row identity for %q: %v", want.id, err)
		}
		rowID, _ := identity["__loom_row_id"].(string)
		result, traceErr := engine.CellTraceCompiled(ctx, trace, CellTraceRequest{
			Output: compiledOutput.Name, RowID: rowID, Column: "total", Limit: 25,
		})
		if traceErr != nil {
			t.Fatalf("trace %q: %v", want.id, traceErr)
		}
		if !result.Complete || result.Status != CellTraceValue || !executionTraceNumberEqual(result.Value, want.total) {
			t.Fatalf("trace %q = %#v, want complete value %v", want.id, result, want.total)
		}
		if len(result.Contributions) != 1 {
			t.Fatalf("trace %q contributors = %#v, want one source-column contribution", want.id, result.Contributions)
		}
		contribution := result.Contributions[0]
		if contribution.InputStageID != "source_projection" || contribution.InputColumnID != "amount_id" || contribution.InputColumn != "amount" || contribution.OutputStageID != "derive_total" || contribution.OutputColumnID != "total_id" || contribution.OutputColumn != "total" || contribution.FinalStageID != "keep_positive" || contribution.ConstructionID != "calc_total" || contribution.Operation != "DERIVE" || !executionTraceNumberEqual(contribution.Value, float64(want.amount)) {
			t.Fatalf("trace %q provenance = %#v", want.id, contribution)
		}
		if contribution.ResourceID != "" || contribution.ResourceType != "" {
			t.Fatalf("trace %q falsely claims resource provenance: %#v", want.id, contribution)
		}
	}
}

func executionConstructionTraceOutput() recipe.Output {
	two := int64(2)
	zero := int64(0)
	return recipe.Output{
		Name: "construction_trace", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "calc_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "keep_positive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive_total"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterThan,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &zero}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
			},
		},
	}
}

func executionTraceNumberEqual(value any, want float64) bool {
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
