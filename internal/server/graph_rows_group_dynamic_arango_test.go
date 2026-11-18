package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	storearango "github.com/calypr/loom/internal/store/arango"
)

func graphRowsCheckGroupedDynamicInventories(t *testing.T, ctx context.Context, client *storearango.Client) {
	t.Helper()
	for _, project := range []string{"group-known", "group-drift"} {
		var docs []json.RawMessage
		for i, key := range []string{"allowed", "alsoallowed"} {
			if project == "group-drift" && i == 0 {
				key = "unexpected"
			}
			raw, err := json.Marshal(map[string]any{"_key": project + key, "project": project, "project_id": project, "dataset_generation": "fixture-v1", "resourceType": "ResearchSubject", "payload": map[string]any{"id": project + key, "status": "active", "identifier": []any{map[string]any{"value": key}}}})
			if err != nil {
				t.Fatal(err)
			}
			docs = append(docs, raw)
		}
		if err := client.InsertBatchRaw(ctx, "ResearchSubject", docs, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name                          string
		keyed, repeated, empty, drift bool
		wantCount                     int
	}{
		{name: "keyed-known", keyed: true, wantCount: 2},
		{name: "keyless-known", wantCount: 2},
		{name: "repeated-group-known", repeated: true, wantCount: 1},
		{name: "empty-keyless", empty: true, wantCount: 0},
		{name: "keyed-drift", keyed: true, drift: true},
		{name: "keyless-drift", drift: true},
		{name: "repeated-group-drift", repeated: true, drift: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "resource", RootColumnNaming: recipe.RootColumnNamingExact, Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "identifier", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Columns: []string{"allowed", "alsoallowed"}}}}
			if tc.empty {
				id := "no-such-subject"
				source.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &id}}}}
			}
			group := recipe.ConstructionGroup{ConstructionID: "group", Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "count_id"}}}
			outputs := []recipe.StageColumn{{ID: "count_id", Name: "row_count", Type: "integer"}}
			if tc.keyed {
				group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "key_id"}}
				outputs = append(outputs, recipe.StageColumn{ID: "key_id", Name: "status", Type: "string"})
			}
			source.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}, {ID: "identifier_allowed", Name: "identifier_allowed"}, {ID: "identifier_alsoallowed", Name: "identifier_alsoallowed"}}, Steps: []recipe.ConstructionStep{{ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &group}, Outputs: outputs}}}
			if tc.repeated {
				source.Construction.Steps = append(source.Construction.Steps, recipe.ConstructionStep{ID: "group-again", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group-again", Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "again_count"}}}}, Outputs: []recipe.StageColumn{{ID: "again_count", Name: "row_count", Type: "integer"}}})
			}
			engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(ctx context.Context, query string, batchSize int, binds map[string]any, visit func(map[string]any) error) error {
				return client.QueryRows(ctx, query, batchSize, binds, visit)
			}})
			if err != nil {
				t.Fatal(err)
			}
			project := "group-known"
			if tc.drift {
				project = "group-drift"
			}
			resolved, err := engine.CompileResolvedBundle(ctx, recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group-dynamic-checks", TranslationVersion: "test", Outputs: []recipe.Output{source}}, recipe.RuntimeBindings{Project: project, DatasetGeneration: "fixture-v1"})
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			_, err = engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "subjects", Limit: 25}, func(row map[string]any) error {
				if _, exposed := row["__loom_dynamic_runtime_keys"]; exposed {
					t.Fatal("internal inventories leaked into public output")
				}
				rows = append(rows, row)
				return nil
			})
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				t.Logf("validation error %T: %v", cause, cause)
			}

			if tc.drift {
				var drift *dataframeexecution.DynamicDriftError
				if !errors.As(err, &drift) {
					t.Fatalf("expected exact unknown-key validation error, got %v", err)
				}
				if drift.Key != "unexpected" {
					t.Fatalf("wrong drift: %#v", drift)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%#v want one Group", rows)
			}
			graphRowsAssertJSON(t, rows[0]["row_count"], tc.wantCount)
		})
	}
}
