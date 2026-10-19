package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestNestedRepeatedSourceValuesThroughAuthoredConstructionExpandAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project := "loom_nested_construction_expand_" + uuid.NewString()
	generation := "nested-expansion-" + uuid.NewString()
	t.Logf("fixture scope project=%q generation=%q", project, generation)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		query := "FOR document IN Observation FILTER document.project == @project AND document.dataset_generation == @generation REMOVE document IN Observation"
		if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project, "generation": generation}); err != nil {
			t.Errorf("remove nested construction expansion fixture: %v", err)
			return
		}
		remaining := 0
		query = "FOR document IN Observation FILTER document.project == @project AND document.dataset_generation == @generation RETURN document._key"
		if err := client.QueryRows(cleanupCtx, query, 100, map[string]any{"project": project, "generation": generation}, func(map[string]any) error {
			remaining++
			return nil
		}); err != nil {
			t.Errorf("verify nested construction expansion cleanup: %v", err)
		} else if remaining != 0 {
			t.Errorf("nested construction expansion cleanup left %d fixture documents", remaining)
		}
	})

	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{
			"id": "mixed",
			"component": []any{
				map[string]any{"valueString": "alpha", "code": map[string]any{"coding": []any{map[string]any{"code": "red"}, map[string]any{"code": "blue"}}}},
				map[string]any{"valueString": "empty-inner", "code": map[string]any{"coding": []any{}}},
				map[string]any{"valueString": "missing-inner", "code": map[string]any{}},
				map[string]any{"valueString": "beta", "code": map[string]any{"coding": []any{map[string]any{"code": "red"}}}},
			},
		},
		{"id": "missing-component"},
		{"id": "empty-component", "component": []any{}},
		{"id": "missing-code", "component": []any{map[string]any{"valueString": "no-code"}}},
		{"id": "empty-coding", "component": []any{map[string]any{"valueString": "no-codings", "code": map[string]any{"coding": []any{}}}}},
	})

	want := []nestedConstructionExpandExpected{
		{observationID: "mixed", componentLabels: []any{"alpha", "empty-inner", "missing-inner", "beta"}, code: "red", itemPresent: true, ordinal: 0},
		{observationID: "mixed", componentLabels: []any{"alpha", "empty-inner", "missing-inner", "beta"}, code: "blue", itemPresent: true, ordinal: 1},
		{observationID: "mixed", componentLabels: []any{"alpha", "empty-inner", "missing-inner", "beta"}, code: "red", itemPresent: true, ordinal: 2},
		{observationID: "missing-component", componentLabels: []any{}},
		{observationID: "empty-component", componentLabels: []any{}},
		{observationID: "missing-code", componentLabels: []any{"no-code"}},
		{observationID: "empty-coding", componentLabels: []any{"no-codings"}},
	}
	preservedQuery := compileConstructionOutputQuery(t, nestedConstructionExpandOutput(recipe.ExpansionPreserveParent), project, generation)
	assertNestedConstructionExpandRows(t, executeReshapeOracleQuery(t, ctx, client, preservedQuery), project, want)

	var wantNonEmpty []nestedConstructionExpandExpected
	for _, row := range want {
		if row.itemPresent {
			wantNonEmpty = append(wantNonEmpty, row)
		}
	}
	excludedQuery := compileConstructionOutputQuery(t, nestedConstructionExpandOutput(recipe.ExpansionExclude), project, generation)
	assertNestedConstructionExpandRows(t, executeReshapeOracleQuery(t, ctx, client, excludedQuery), project, wantNonEmpty)

	errorQuery := compileConstructionOutputQuery(t, nestedConstructionExpandOutput(recipe.ExpansionError), project, generation)
	if err := client.QueryRows(ctx, errorQuery.Query, 500, errorQuery.BindVars, func(map[string]any) error { return nil }); err == nil || !strings.Contains(err.Error(), "CONSTRUCTION_EXPANSION_EMPTY") {
		t.Fatalf("ERROR policy over missing/empty nested source lists = %v, want CONSTRUCTION_EXPANSION_EMPTY", err)
	}
}

type nestedConstructionExpandExpected struct {
	observationID   string
	componentLabels []any
	code            string
	itemPresent     bool
	ordinal         int
}

func nestedConstructionExpandOutput(policy recipe.ExpansionEmptyPolicy) recipe.Output {
	return recipe.Output{
		Name: "NestedComponentCodings", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "observation_id", ColumnID: "observation_id_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "component_labels", ColumnID: "component_labels_id", Expr: recipe.Expression{Select: "root.component[].valueString"}, ValueMode: recipe.ValueModeAll},
			{Name: "coding_codes", ColumnID: "coding_codes_id", Expr: recipe.Expression{Select: "root.component[].code.coding[].code"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "observation_id_id", Name: "observation_id"},
				{ID: "component_labels_id", Name: "component_labels"},
				{ID: "coding_codes_id", Name: "coding_codes"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_nested_codings", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "nested_coding_expand", InputColumnID: "coding_codes_id", OutputColumnID: "coding_code_id",
					OrdinalColumnID: "ordinal_id", EmptyPolicy: policy,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "observation_id_id", Name: "observation_id"},
					{ID: "component_labels_id", Name: "component_labels"},
					{ID: "coding_code_id", Name: "coding_code"},
					{ID: "ordinal_id", Name: "ordinal"},
				},
			}},
		},
	}
}

func assertNestedConstructionExpandRows(t *testing.T, rows []map[string]any, project string, want []nestedConstructionExpandExpected) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("authored nested EXPAND emitted %d rows, want %d: %#v", len(rows), len(want), rows)
	}
	wantByKey := make(map[string]nestedConstructionExpandExpected, len(want))
	for _, expected := range want {
		wantByKey[nestedConstructionExpandRowKey(expected.observationID, expected.itemPresent, expected.ordinal)] = expected
	}
	seen := make(map[string]bool, len(rows))
	seenIdentity := make(map[string]bool, len(rows))
	for _, row := range rows {
		observationID, ok := row["observation_id"].(string)
		if !ok {
			t.Fatalf("expanded observation ID = %#v, want string", row["observation_id"])
		}
		itemPresent := row["coding_code"] != nil
		code := ""
		if itemPresent {
			var codeOK bool
			code, codeOK = row["coding_code"].(string)
			if !codeOK {
				t.Fatalf("expanded coding code = %#v, want string or null", row["coding_code"])
			}
		}
		ordinal := -1
		if row["ordinal"] != nil {
			ordinal = int(numericValue(row["ordinal"]))
		}
		key := nestedConstructionExpandRowKey(observationID, itemPresent, ordinal)
		expected, ok := wantByKey[key]
		if !ok || seen[key] {
			t.Fatalf("unexpected or duplicate authored nested EXPAND row %q: %#v", key, row)
		}
		seen[key] = true
		if expected.itemPresent {
			if code != expected.code || ordinal != expected.ordinal {
				t.Fatalf("expanded row %q = code %q ordinal %d, want code %q ordinal %d", key, code, ordinal, expected.code, expected.ordinal)
			}
		} else if row["coding_code"] != nil || row["ordinal"] != nil {
			t.Fatalf("preserved source row %q = code %#v ordinal %#v, want null/null", key, row["coding_code"], row["ordinal"])
		}

		labels, ok := row["component_labels"].([]any)
		if !ok {
			t.Fatalf("expanded row %q component labels = %#v, want array", key, row["component_labels"])
		}
		if !reflect.DeepEqual(labels, expected.componentLabels) {
			t.Fatalf("expanded row %q component labels = %#v, want %#v", key, labels, expected.componentLabels)
		}

		identity, ok := row["__loom_row_id"].(string)
		if !ok || identity == "" || seenIdentity[identity] {
			t.Fatalf("expanded row %q identity = %#v, want unique string", key, row["__loom_row_id"])
		}
		seenIdentity[identity] = true
		var parts [][]any
		if err := json.Unmarshal([]byte(identity), &parts); err != nil {
			t.Fatalf("expanded row %q identity %q is not encoded identity JSON: %v", key, identity, err)
		}
		wantOrdinal := any(nil)
		if expected.itemPresent {
			wantOrdinal = float64(expected.ordinal)
		}
		wantParts := [][]any{
			{"input", project + "_" + expected.observationID},
			{"construction", "nested_coding_expand"},
			{"ordinal", wantOrdinal},
		}
		if !reflect.DeepEqual(parts, wantParts) {
			t.Fatalf("expanded row %q identity parts = %#v, want source/construction/ordinal %#v", key, parts, wantParts)
		}
	}
	if len(seen) != len(wantByKey) {
		t.Fatalf("authored nested EXPAND rows = %#v, want keys %#v", seen, wantByKey)
	}
}

func nestedConstructionExpandRowKey(observationID string, itemPresent bool, ordinal int) string {
	if !itemPresent {
		return fmt.Sprintf("%s\x00<null>", observationID)
	}
	return fmt.Sprintf("%s\x00%d", observationID, ordinal)
}
