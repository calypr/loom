package compiler

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestSemanticGroupScalarKeyPreviewFrontierActivatesOnCompiledPlan(t *testing.T) {
	const targetSystem, targetVersion, codingPath = "urn:loom:semantic-group-preview", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupFixtureBinding(t, targetSystem, targetVersion, codingPath)
	bindings := recipe.RuntimeBindings{Project: "semantic-group-preview-unit", DatasetGeneration: "generation-unit"}
	output := semanticGroupArangoOutput(
		t, recipe.ConstructionGroupMissingKeyGroup, bindingID, familyID, ruleVersion,
		targetSystem, targetVersion, codingPath,
	)
	compiled := lowerConstructionOutput(t, output, bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group preview: %v", err)
	}
	if strings.Contains(preview.Query, "group_preview_selected_root_keys") {
		if strings.Count(preview.Query, "root._key") < 2 || !strings.Contains(preview.Query, "group_preview_selected_root_keys") {
			t.Fatalf("frontier did not retain and replay the direct root identity projection:\n%s", preview.Query)
		}
		return
	}
	t.Fatalf("compiled semantic Group plan did not enter its proven frontier:\n%s", preview.Query)
}

func TestSemanticGroupScalarKeyPreviewMatchesFullQueryAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)

	suffix := uuid.NewString()
	project, generation := "loom_semantic_group_preview_"+suffix, "generation_"+suffix
	const targetSystem, targetVersion, codingPath = "urn:loom:semantic-group-preview", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupFixtureBinding(t, targetSystem, targetVersion, codingPath)
	fixtureIDs := []string{
		"null_missing_status", "null_explicit_status", "active_first", "active_second", "active_missing", "active_wrong_version",
		"later_scalar", "z_last_one", "z_last_two", "denied_active", "foreign_project", "foreign_generation", "late_malformed_status",
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR key IN @keys REMOVE key IN Observation",
			map[string]any{"keys": semanticGroupPreviewFixtureKeys(project, fixtureIDs)},
		); err != nil {
			t.Errorf("remove semantic Group preview resources: %v", err)
		}
	})

	allowed := func(id, status string, components ...any) map[string]any {
		return map[string]any{
			"id": id, "status": status, "auth_resource_path": "/allowed", "component": components,
		}
	}
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "null_missing_status", "auth_resource_path": "/allowed"},
		{"id": "null_explicit_status", "status": nil, "auth_resource_path": "/allowed"},
		allowed("active_first", "active",
			semanticGroupComponent(targetSystem, targetVersion, "shared", "Z shared"),
			semanticGroupComponent(targetSystem, targetVersion, "shared", "A shared"),
			semanticGroupComponent(targetSystem, targetVersion, "shared", "A shared"),
			semanticGroupComponent(targetSystem, targetVersion, "other", "A shared")),
		allowed("active_second", "active", semanticGroupComponent(targetSystem, targetVersion, "shared", "Shared")),
		allowed("active_missing", "active"),
		allowed("active_wrong_version", "active", semanticGroupComponent(targetSystem, "v2", "wrong", "Wrong version")),
		allowed("later_scalar", "preliminary", semanticGroupComponent(targetSystem, targetVersion, "later", "Later")),
		allowed("z_last_one", "zz-last-one", semanticGroupComponent(targetSystem, targetVersion, "last-one", "Last one")),
		allowed("z_last_two", "zz-last-two", semanticGroupComponent(targetSystem, targetVersion, "last-two", "Last two")),
		{
			"id": "denied_active", "status": "active", "auth_resource_path": "/blocked",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, "shared", "Denied")},
		},
	})
	insertSemanticGroupPreviewForeignRows(t, ctx, client, project, generation, targetSystem, targetVersion)

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	output := semanticGroupArangoOutput(
		t, recipe.ConstructionGroupMissingKeyGroup, bindingID, familyID, ruleVersion,
		targetSystem, targetVersion, codingPath,
	)
	compiled := lowerConstructionOutput(t, output, bindings)
	const previewLimit = 4
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, previewLimit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group preview: %v", err)
	}
	full, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile full semantic Group query: %v", err)
	}
	if !strings.Contains(preview.Query, "group_preview_selected_root_keys") {
		t.Fatalf("semantic Group preview did not activate the direct-root key frontier:\n%s", preview.Query)
	}
	if strings.Count(preview.Query, "FILTER root.project == @project") != 2 ||
		strings.Count(preview.Query, "FILTER root.dataset_generation == @dataset_generation") != 2 ||
		strings.Count(preview.Query, "root.auth_resource_path IN @auth_resource_paths") != 2 {
		t.Fatalf("frontier or selected-root replay dropped a source/auth predicate:\n%s", preview.Query)
	}
	if preview.PreviewCoveringIndex != nil {
		hinted, _, err := RenderPreviewPhysicalPlanWithRootIndexHint(preview.PhysicalPlan, preview.PreviewCoveringIndex.Name)
		if err != nil {
			t.Fatalf("render semantic Group frontier with the non-forcing preview index hint: %v", err)
		}
		if !strings.Contains(hinted, "OPTIONS { indexHint: \""+preview.PreviewCoveringIndex.Name+"\", forceIndexHint: false }") ||
			!strings.Contains(hinted, "group_preview_selected_root_keys") {
			t.Fatalf("non-forcing preview hint disabled the semantic Group frontier:\n%s", hinted)
		}
	}

	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) <= previewLimit || len(previewRows) != previewLimit || !reflect.DeepEqual(previewRows, fullRows[:previewLimit]) {
		t.Fatalf("semantic Group preview rows = %#v, want first %d exact full-query rows %#v", previewRows, previewLimit, fullRows)
	}
	if previewRows[0]["status"] != nil || previewRows[1]["status"] != "active" ||
		previewRows[2]["status"] != "active" || previewRows[3]["status"] != "active" {
		t.Fatalf("first compound groups must preserve null scalar ordering and the active scalar population: %#v", previewRows)
	}
	if previewRows[0]["category"] != nil || previewRows[1]["category"] != nil ||
		previewRows[2]["category"] != "A shared" || previewRows[3]["category"] != "A shared" ||
		!constructionNumericEqual(previewRows[0]["rows"], 2) || !constructionNumericEqual(previewRows[1]["rows"], 2) ||
		!constructionNumericEqual(previewRows[2]["rows"], 1) || !constructionNumericEqual(previewRows[3]["rows"], 2) {
		t.Fatalf("semantic membership counts lost the per-root dedupe or missing-family sentinel: %#v", previewRows)
	}
	firstCodeID, _ := previewRows[2]["__loom_row_id"].(string)
	secondCodeID, _ := previewRows[3]["__loom_row_id"].(string)
	if firstCodeID == "" || secondCodeID == "" || firstCodeID == secondCodeID {
		t.Fatalf("same-display code tuples do not have distinct identities: %#v", previewRows)
	}
	uniqueScalarKeys := make(map[string]struct{})
	for _, row := range fullRows {
		key, _ := row["status"].(string)
		if row["status"] == nil {
			key = "<null>"
		}
		uniqueScalarKeys[key] = struct{}{}
	}
	if len(uniqueScalarKeys) <= previewLimit {
		t.Fatalf("fixture must have more scalar keys than its preview limit %d: %#v", previewLimit, uniqueScalarKeys)
	}

	// This malformed scalar sorts after the valid string-key page. The frontier
	// must preserve the canonical global type error instead of hiding it beyond
	// the preview limit.
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{{
		"id": "late_malformed_status", "status": map[string]any{"unexpected": "object"},
		"auth_resource_path": "/allowed",
		"component":          []any{semanticGroupComponent(targetSystem, targetVersion, "late", "Late")},
	}})
	for _, limit := range []int{previewLimit, 0} {
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile semantic Group query with malformed scalar key and limit %d: %v", limit, err)
		}
		if err := client.QueryRows(ctx, query.Query, 100, query.BindVars, func(map[string]any) error { return nil }); err == nil {
			t.Fatalf("semantic Group with limit %d hid the malformed key outside its selected page", limit)
		}
	}
}

func semanticGroupPreviewFixtureKeys(project string, ids []string) []string {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, project+"_"+id)
	}
	return keys
}

func insertSemanticGroupPreviewForeignRows(
	t *testing.T,
	ctx context.Context,
	client interface {
		InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
	},
	project, generation, targetSystem, targetVersion string,
) {
	t.Helper()
	row := func(id, resourceProject, resourceGeneration, code string) json.RawMessage {
		t.Helper()
		payload := map[string]any{
			"id": id, "resourceType": "Observation", "status": "active",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, code, "A shared")},
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + id, "id": id, "resourceType": "Observation",
			"project": resourceProject, "project_id": resourceProject,
			"dataset_generation": resourceGeneration, "auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatalf("encode semantic Group preview foreign-scope row: %v", err)
		}
		return document
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{
		row("foreign_project", project+"_foreign", generation, "shared"),
		row("foreign_generation", project, generation+"_old", "shared"),
	}, false, "document"); err != nil {
		t.Fatalf("insert semantic Group preview foreign-scope rows: %v", err)
	}
}
