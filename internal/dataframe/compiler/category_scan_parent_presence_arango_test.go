package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
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

func TestRootCategoryScanTracksValueQuantityCodeParentPresenceAndOverflowAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)

	project := "loom_quantity_presence_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	overflowProject := project + "_overflow"
	generation := "generation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	const allowedPath = "/category/allowed"
	const deniedPath = "/category/denied"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, cleanupProject := range []string{project, overflowProject} {
			if err := client.ExecuteAQL(cleanupCtx, `
FOR document IN Observation
  FILTER document.project == @project
  FILTER document.dataset_generation == @generation
  REMOVE document IN Observation`, map[string]any{"project": cleanupProject, "generation": generation}); err != nil {
				t.Errorf("remove category parent-presence fixtures for project %s: %v", cleanupProject, err)
			}
		}
	})

	baseFixtures := []struct {
		id             string
		resourcePath   string
		includeParent  bool
		quantityParent any
	}{
		{id: "missing-parent", resourcePath: allowedPath},
		{id: "missing-code", resourcePath: allowedPath, includeParent: true, quantityParent: map[string]any{"value": 5}},
		{id: "explicit-null-code", resourcePath: allowedPath, includeParent: true, quantityParent: map[string]any{"code": nil}},
		{id: "false-code", resourcePath: allowedPath, includeParent: true, quantityParent: map[string]any{"code": false}},
		{id: "zero-code", resourcePath: allowedPath, includeParent: true, quantityParent: map[string]any{"code": 0}},
		{id: "empty-string-code", resourcePath: allowedPath, includeParent: true, quantityParent: map[string]any{"code": ""}},
		{id: "null-parent", resourcePath: allowedPath, includeParent: true, quantityParent: nil},
		{id: "scalar-parent", resourcePath: allowedPath, includeParent: true, quantityParent: "not-an-object"},
		{id: "array-parent", resourcePath: allowedPath, includeParent: true, quantityParent: []any{map[string]any{"code": "array-parent-leak"}}},
		{id: "denied-category", resourcePath: deniedPath, includeParent: true, quantityParent: map[string]any{"code": "denied-only"}},
	}
	insertRootQuantityCategoryFixtureRows(t, ctx, client, project, generation, baseFixtures)

	scan := compileRootValueQuantityCodeCategoryScan(t, project, generation, []string{allowedPath})
	if !scan.Proof.PresenceTracked || scan.OverflowWitness == nil || scan.Proof.OverflowWitnessFingerprint == "" {
		t.Fatalf("root category scan proof = %+v, overflow witness present=%t; want tracked parent presence and a fingerprinted witness", scan.Proof, scan.OverflowWitness != nil)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: scan.Query, BindVars: scan.BindVars})
	got := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		present, ok := row[scan.PresentColumn].(bool)
		if !ok {
			t.Fatalf("category presence = %#v, want bool", row[scan.PresentColumn])
		}
		value, exists := row[scan.ValueColumn]
		if !exists {
			t.Fatalf("category row omitted value column: %#v", row)
		}
		key, err := rootQuantityCategoryKey(present, value)
		if err != nil {
			t.Fatalf("unexpected root category row %#v: %v", row, err)
		}
		if _, duplicate := got[key]; duplicate {
			t.Fatalf("category scan returned duplicate distinct category %q: %#v", key, rows)
		}
		got[key] = struct{}{}
	}
	want := map[string]struct{}{
		"missing":    {},
		"null":       {},
		"bool:false": {},
		"number:0":   {},
		"string:":    {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped valueQuantity.code categories = %v, want exactly %v", got, want)
	}

	overflowFixtures := make([]struct {
		id             string
		resourcePath   string
		includeParent  bool
		quantityParent any
	}, 0, MaxCategoryScanValues+2)
	overflowFixtures = append(overflowFixtures, struct {
		id             string
		resourcePath   string
		includeParent  bool
		quantityParent any
	}{id: "overflow-missing-parent", resourcePath: allowedPath})
	for index := 0; index <= MaxCategoryScanValues; index++ {
		overflowFixtures = append(overflowFixtures, struct {
			id             string
			resourcePath   string
			includeParent  bool
			quantityParent any
		}{
			id: fmt.Sprintf("overflow-%03d", index), resourcePath: allowedPath,
			includeParent: true, quantityParent: map[string]any{"code": fmt.Sprintf("overflow-%03d", index)},
		})
	}
	insertRootQuantityCategoryFixtureRows(t, ctx, client, overflowProject, generation, overflowFixtures)

	overflowScan := compileRootValueQuantityCodeCategoryScan(t, overflowProject, generation, []string{allowedPath})
	if overflowScan.OverflowWitness == nil || !overflowScan.Proof.PresenceTracked {
		t.Fatalf("overflow scan proof = %+v, witness=%+v; want a presence-aware overflow witness", overflowScan.Proof, overflowScan.OverflowWitness)
	}
	witnessRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{
		Query: overflowScan.OverflowWitness.Query, BindVars: overflowScan.OverflowWitness.BindVars,
	})
	if len(witnessRows) != 1 || witnessRows[0][overflowScan.PresentColumn] != false || witnessRows[0][overflowScan.ValueColumn] != nil {
		t.Fatalf("overflow witness rows = %#v, want one MISSING category witness", witnessRows)
	}

	overflowRows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: overflowScan.Query, BindVars: overflowScan.BindVars})
	if len(overflowRows) != MaxCategoryScanValues+1 {
		t.Fatalf("category scan returned %d rows at the overflow boundary, want %d (256 values plus one witness row)", len(overflowRows), MaxCategoryScanValues+1)
	}
	seenOverflow := make(map[string]struct{}, len(overflowRows))
	missingCount := 0
	for _, row := range overflowRows {
		present, ok := row[overflowScan.PresentColumn].(bool)
		if !ok {
			t.Fatalf("overflow category presence = %#v, want bool", row[overflowScan.PresentColumn])
		}
		value, exists := row[overflowScan.ValueColumn]
		if !exists {
			t.Fatalf("overflow category row omitted value column: %#v", row)
		}
		key, err := rootQuantityCategoryKey(present, value)
		if err != nil {
			t.Fatalf("unexpected overflow category row %#v: %v", row, err)
		}
		if _, duplicate := seenOverflow[key]; duplicate {
			t.Fatalf("overflow query returned duplicate category %q", key)
		}
		seenOverflow[key] = struct{}{}
		if key == "missing" {
			missingCount++
		} else if !strings.HasPrefix(key, "string:overflow-") {
			t.Fatalf("overflow scan exposed out-of-scope or malformed-parent category %q", key)
		}
	}
	if missingCount != 1 || len(seenOverflow) != MaxCategoryScanValues+1 {
		t.Fatalf("overflow category set has %d missing and %d distinct values, want one missing plus %d distinct results", missingCount, len(seenOverflow)-missingCount, MaxCategoryScanValues)
	}
}

func insertRootQuantityCategoryFixtureRows(
	t *testing.T,
	ctx context.Context,
	client *store.Client,
	project, generation string,
	fixtures []struct {
		id             string
		resourcePath   string
		includeParent  bool
		quantityParent any
	},
) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		payload := map[string]any{"id": fixture.id, "resourceType": "Observation"}
		if fixture.includeParent {
			payload["valueQuantity"] = fixture.quantityParent
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + fixture.id, "id": fixture.id, "project": project,
			"project_id": project, "dataset_generation": generation, "resourceType": "Observation",
			"auth_resource_path": fixture.resourcePath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert root valueQuantity category fixtures: %v", err)
	}
}

func compileRootValueQuantityCodeCategoryScan(t *testing.T, project, generation string, authPaths []string) CompiledCategoryScanQuery {
	t.Helper()
	columns := []recipe.StageColumn{
		{ID: "observation_id", Name: "observation_id"},
		{ID: "category_id", Name: "category"},
		{ID: "value_id", Name: "value"},
	}
	output := recipe.Output{
		Name: "root_quantity_code_presence", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "observation_id", ColumnID: "observation_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.valueQuantity.code"}},
			{Name: "value", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: append([]string(nil), authPaths...),
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name,
		TranslationVersion: "test", Outputs: []recipe.Output{output},
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
	scan, err := CompileCategoryScanStageWithPolicy(
		compiled.Outputs[0], recipe.ConstructionSourceProjectionID, "category_id", "value_id",
		MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return scan
}

func rootQuantityCategoryKey(present bool, value any) (string, error) {
	if !present {
		if value != nil {
			return "", fmt.Errorf("missing value is %#v, want nil", value)
		}
		return "missing", nil
	}
	switch value := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return "bool:" + strconv.FormatBool(value), nil
	case string:
		return "string:" + value, nil
	case int:
		return "number:" + strconv.Itoa(value), nil
	case int64:
		return "number:" + strconv.FormatInt(value, 10), nil
	case float64:
		return "number:" + strconv.FormatFloat(value, 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported category type %T with value %#v", value, value)
	}
}
