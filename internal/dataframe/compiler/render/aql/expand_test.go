package aql_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestTerminalRepeatedPrimitiveSelectorValuesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_repeated_terminal_"+uuid.NewString(), "generation-repeated-terminal"
	key := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		query := "FOR document IN Patient FILTER STARTS_WITH(document._key, @prefix) REMOVE document IN Patient"
		if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"prefix": project + "_"}); err != nil {
			t.Errorf("remove terminal repeated-selector fixtures: %v", err)
		}
	}()

	nullGivenElement := map[string]any{"extension": []any{map[string]any{
		"url": "https://example.org/fhir/StructureDefinition/test", "valueString": "extension-only",
	}}}
	givenNames := []any{
		map[string]any{"given": []any{"alpha", "alpha", nil}, "_given": []any{nil, nil, nullGivenElement}},
		map[string]any{"given": []any{nil}, "_given": []any{nullGivenElement}},
		map[string]any{"given": []any{"beta"}},
	}
	patientDocument := func(id string, payload map[string]any) json.RawMessage {
		t.Helper()
		payload["id"], payload["resourceType"] = id, "Patient"
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient",
			"auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	documents := []json.RawMessage{
		patientDocument("p_values", map[string]any{"name": givenNames}),
		patientDocument("p_null", map[string]any{"name": []any{map[string]any{"given": []any{nil}, "_given": []any{nullGivenElement}}}}),
		patientDocument("p_empty", map[string]any{"name": []any{map[string]any{"given": []any{}}}}),
		patientDocument("p_missing", map[string]any{"name": []any{map[string]any{"family": "OnlyFamily"}}}),
	}
	if err := client.InsertBatchRaw(ctx, "Patient", documents, false, "document"); err != nil {
		t.Fatalf("insert terminal repeated-selector fixtures: %v", err)
	}

	selector, err := spec.ParseSelector("name[].given[]")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []ir.PhysicalSelectorExecutionMode{ir.PhysicalSelectorGeneric, ir.PhysicalSelectorConditionalArray} {
		t.Run(string(mode), func(t *testing.T) {
			plan, err := buildGenericPhysicalPlanWithContext(
				recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient"}},
				recipeplan.ExecutionContext{
					Project: project, DatasetGeneration: generation, AuthResourcePaths: []string{"/allowed"},
					AuthScopeMode: authscope.ReadScopeRestricted,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{
				{Name: "_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
				{Name: "given_values", Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{
						Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						ResourceType: "Patient", Selector: selector, ExecutionMode: mode,
					},
				}},
			}
			rendered, err := aql.Render(plan, aql.Options{})
			if err != nil {
				t.Fatalf("render %s terminal primitive selector: %v", mode, err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: rendered.Query, BindVars: rendered.BindVars})
			if len(rows) != len(documents) {
				t.Fatalf("%s terminal primitive query returned %d rows, want %d:\n%s", mode, len(rows), len(documents), rendered.Query)
			}
			valuesByKey := make(map[string][]string, len(rows))
			for _, row := range rows {
				rowKey, _ := row["_key"].(string)
				rawValues, ok := row["given_values"].([]any)
				if !ok {
					t.Fatalf("%s given_values for %q has type %T: %#v", mode, rowKey, row["given_values"], row)
				}
				values := make([]string, 0, len(rawValues))
				for _, rawValue := range rawValues {
					value, ok := rawValue.(string)
					if !ok {
						t.Fatalf("%s terminal repeated selector retained a null/non-string leaf for %q: %#v", mode, rowKey, rawValues)
					}
					values = append(values, value)
				}
				valuesByKey[rowKey] = values
			}
			assertValues := func(id string, want []string) {
				t.Helper()
				got, ok := valuesByKey[key(id)]
				if !ok || len(got) != len(want) {
					t.Fatalf("%s values for %s = %#v (present=%t), want %#v", mode, id, got, ok, want)
				}
				for index := range want {
					if got[index] != want[index] {
						t.Fatalf("%s values for %s = %#v, want %#v", mode, id, got, want)
					}
				}
			}
			assertValues("p_values", []string{"alpha", "alpha", "beta"})
			assertValues("p_null", nil)
			assertValues("p_empty", nil)
			assertValues("p_missing", nil)
		})
	}
}
