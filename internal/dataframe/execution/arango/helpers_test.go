package arango_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/recipetest"
)

func graphRowsCheckDefaultRelatedInventory(t *testing.T, ctx context.Context, client *storearango.Client, generation, mode string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/graph_rows_default_related_field.json")
	if err != nil {
		t.Fatal(err)
	}
	var output recipe.Output
	if err = json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(mode, "type") {
		output.DynamicColumns[0].ColumnTypes = map[string]string{"urn:graph-rows": "string"}
	}
	if strings.HasPrefix(mode, "related-source") {
		step := graphRowsFixtureSummaries().Construction.Steps[0]
		step.Outputs = append(append([]recipe.StageColumn{}, output.Construction.SourceColumns...), step.Outputs[len(step.Outputs)-1])
		output.Construction.Steps = []recipe.ConstructionStep{step}
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
		return client.QueryRows(ctx, query, batch, binds, visit)
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(ctx, recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "default-related-inventory", TranslationVersion: "test", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: "graph-fixture", DatasetGeneration: generation})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	_, err = engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: output.Name, Limit: 100}, func(row map[string]any) error {
		if _, leak := row["__loom_dynamic_runtime_keys"]; leak {
			t.Fatal("private dynamic inventory exposed publicly")
		}
		rows = append(rows, row)
		return nil
	})
	causes := ""
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		t.Logf("preview cause %T: %v", cause, cause)
		causes += cause.Error() + "\n"
	}
	if strings.HasSuffix(mode, "drift") {
		if user, ok := dataframeerrors.AsUserError(err); !ok || user.Code() != string(dataframeerrors.CodeDynamicSchemaDrift) {
			t.Fatalf("unexpected dynamic key must remain rejected as schema drift: %v", err)
		}
		return
	}
	if strings.HasSuffix(mode, "type") {
		if err == nil || !strings.Contains(causes, "incompatible value type") {
			t.Fatalf("wrong dynamic type must remain rejected: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(mode, "related-source") {
		if len(rows) != 4 {
			t.Fatalf("related summary changed source rows: %#v", rows)
		}
		expected := map[string]int{"rs-zero": 0, "rs-one": 1, "rs-many": 2, "rs-shared": 2}
		for _, row := range rows {
			id := row["id"].(string)
			want, ok := expected[id]
			if !ok {
				t.Fatalf("unexpected/duplicate root %s", id)
			}
			delete(expected, id)
			graphRowsAssertJSON(t, row["observation_count"], want)
		}
		if len(expected) != 0 {
			t.Fatalf("missing roots %#v", expected)
		}
		return
	}

	baseline := output
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep{}, construction.Steps[:1]...)
	baseline.Construction = &construction
	baseResolved, baseErr := engine.CompileResolvedBundle(ctx, recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "default-expansion-before-field", TranslationVersion: "test", Outputs: []recipe.Output{baseline}}, recipe.RuntimeBindings{Project: "graph-fixture", DatasetGeneration: generation})
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	prior := map[string]map[string]any{}
	pairKey := func(row map[string]any) string {
		key := row["id"].(string) + "/<nil>"
		if id := row["expanded_observation_id"]; id != nil {
			key = row["id"].(string) + "/" + id.(string)
		}
		return key
	}
	_, baseErr = engine.PreviewOutput(ctx, baseResolved, dataframeexecution.PreviewRequest{Output: output.Name, Limit: 100}, func(row map[string]any) error { prior[pairKey(row)] = row; return nil })
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	for _, row := range rows {
		before, ok := prior[pairKey(row)]
		if !ok {
			t.Fatalf("missing prior row for %s", pairKey(row))
		}
		retained := map[string]any{}
		for name, value := range row {
			if name != "per_match_observation_value" {
				retained[name] = value
			}
		}
		graphRowsAssertJSON(t, retained, before)
	}
	if len(rows) != 6 {
		t.Fatalf("full default expansion/scalar rows=%d want6", len(rows))
	}
	expected := map[string]struct {
		value any
		list  []int
	}{"rs-zero/<nil>": {nil, []int{}}, "rs-one/obs-one": {7, []int{7}}, "rs-many/obs-many-A": {11, []int{11, 13}}, "rs-many/obs-many-B": {13, []int{11, 13}}, "rs-shared/obs-many-A": {11, []int{11, 13}}, "rs-shared/obs-many-B": {13, []int{11, 13}}}
	for _, row := range rows {
		key := row["id"].(string) + "/<nil>"
		if id := row["expanded_observation_id"]; id != nil {
			key = row["id"].(string) + "/" + id.(string)
		}
		want, ok := expected[key]
		if !ok {
			t.Fatalf("unexpected/duplicate pair %s", key)
		}
		delete(expected, key)
		graphRowsAssertJSON(t, row["per_match_observation_value"], want.value)
		graphRowsAssertJSON(t, row["patient__observation__valueInteger"], want.list)
	}
	if len(expected) != 0 {
		t.Fatalf("missing pairs: %#v", expected)
	}
}

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
			engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, QueryRows: func(ctx context.Context, query string, batchSize int, binds map[string]any, visit func(map[string]any) error) error {
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
