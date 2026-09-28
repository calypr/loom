package compiler

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionCodedGroupCountsDistinctRootCodingTuplesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_coded_group_"+t.Name(), "generation-coded-group"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{
			"id": "coded-a",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "system-a", "code": "shared", "display": "first"},
				map[string]any{"system": "system-a", "code": "shared", "display": "duplicate"},
				map[string]any{"system": "system-b", "code": "shared"},
				map[string]any{"system": "system-a"},
			}}}},
		},
		{
			"id": "coded-b",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "system-a", "code": "shared"},
			}}}},
		},
		{"id": "coded-missing"},
	})

	output := constructionCodedGroupTestOutput()
	rows := executeConstructionOutput(t, ctx, client, output, project, generation)
	if len(rows) != 3 {
		t.Fatalf("coded grouping rows = %#v, want system-a, system-b, and one missing tuple", rows)
	}
	rowIDs := make(map[string]bool, len(rows))
	for _, row := range rows {
		id, ok := row["__loom_row_id"].(string)
		if !ok || id == "" || rowIDs[id] {
			t.Errorf("coded grouping row ID is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[id] = true
		switch {
		case row["code_system"] == "system-a" && row["code"] == "shared":
			if !constructionNumericEqual(row["source_records"], 2) {
				t.Errorf("system-a/shared source count = %#v, want 2 roots despite duplicate Coding in one root", row["source_records"])
			}
		case row["code_system"] == "system-b" && row["code"] == "shared":
			if !constructionNumericEqual(row["source_records"], 1) {
				t.Errorf("system-b/shared source count = %#v, want 1", row["source_records"])
			}
		case row["code_system"] == nil && row["code"] == nil:
			if row["code_version"] != nil || !constructionNumericEqual(row["source_records"], 2) {
				t.Errorf("missing tuple = %#v, want one count per root with absent or incomplete Coding", row)
			}
		default:
			t.Errorf("unexpected coded grouping row: %#v", row)
		}
	}
	if repeated := executeConstructionOutput(t, ctx, client, output, project, generation); !reflect.DeepEqual(constructionRowIdentities(repeated), constructionRowIdentities(rows)) {
		t.Fatalf("coded grouping row IDs changed between executions: %#v then %#v", constructionRowIdentities(rows), constructionRowIdentities(repeated))
	}
}

func TestConstructionCodedGroupInliningMatchesAuthorizedAndPopulationSourceRowsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	project, generation := "loom_coded_group_inline_"+suffix, "generation-coded-group-inline"
	fixtureCollection := "loom_coded_group_inline_roots_" + suffix
	populationCollection := "loom_coded_group_inline_members_" + suffix
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: fixtureCollection}, {Name: populationCollection},
	}}); err != nil {
		t.Fatal(err)
	}
	insertCodedGroupResources(t, ctx, client, fixtureCollection, project, generation, []map[string]any{
		{"id": "visible-a", "auth_resource_path": "Observation/visible", "component": []any{codedGroupComponent("visible-a")}},
		{"id": "visible-b", "auth_resource_path": "Observation/visible", "component": []any{codedGroupComponent("visible-b")}},
		{"id": "hidden", "auth_resource_path": "Observation/hidden", "component": []any{codedGroupComponent("hidden")}},
	})

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"Observation/visible"},
	}
	allOutput := constructionCodedGroupTestOutput()
	allCompiled := lowerConstructionOutput(t, allOutput, bindings)
	allQuery, err := CompileRecipeOutputWithPolicy(allCompiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	allQuery.BindVars["@root_collection"] = fixtureCollection
	allSourceIDs := codedGroupSourceProjectionIDs(t, ctx, client, allCompiled, fixtureCollection)
	allRows := executeReshapeOracleQuery(t, ctx, client, allQuery)
	assertCodedGroupSourceIDsMatchCodes(t, allRows, allSourceIDs)

	selectionID := "selection-" + t.Name()
	insertCodedGroupPopulationMembers(t, ctx, client, populationCollection, selectionID, project, generation, []string{"visible-a", "hidden"})
	selectedOutput := constructionCodedGroupTestOutput()
	selectedOutput.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: selectionID, MembershipDigest: "sha256:coded-group-inline-members",
		MemberCount: 2, ResourceType: "Observation",
	}
	selectedBindings := bindings
	selectedBindings.SelectionProject = project
	selectedBindings.SelectionMembersCollection = populationCollection
	selectedCompiled := lowerConstructionOutput(t, selectedOutput, selectedBindings)
	selectedQuery, err := CompileRecipeOutputWithPolicy(selectedCompiled, selectedBindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	selectedQuery.BindVars["@root_collection"] = fixtureCollection
	selectedQuery.BindVars["@population_source_collection"] = fixtureCollection
	selectedSourceIDs := codedGroupSourceProjectionIDs(t, ctx, client, selectedCompiled, fixtureCollection)
	selectedRows := executeReshapeOracleQuery(t, ctx, client, selectedQuery)
	assertCodedGroupSourceIDsMatchCodes(t, selectedRows, selectedSourceIDs)
}

func insertCodedGroupResources(t *testing.T, ctx context.Context, client *store.Client, collection, project, generation string, payloads []map[string]any) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(payloads))
	for _, payload := range payloads {
		id := payload["id"].(string)
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
			"auth_resource_path": payload["auth_resource_path"],
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
		t.Fatalf("insert coded-group resource fixture: %v", err)
	}
}

func codedGroupComponent(code string) map[string]any {
	return map[string]any{"code": map[string]any{"coding": []any{
		map[string]any{"system": "urn:loom:inline-test", "code": code},
	}}}
}

func insertCodedGroupPopulationMembers(t *testing.T, ctx context.Context, client *store.Client, collection, selectionID, project, generation string, ids []string) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		document, err := json.Marshal(map[string]any{
			"_key": selectionID + "_" + id, "id": id, "selectionId": selectionID,
			"project": project, "generation": generation, "resourceType": "Observation",
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
		t.Fatalf("insert coded-group population members: %v", err)
	}
}

func codedGroupSourceProjectionIDs(t *testing.T, ctx context.Context, client *store.Client, compiled lower.CompiledRecipeOutput, collection string) []string {
	t.Helper()
	plan := ir.ClonePhysicalPlan(compiled.Plan)
	plan.StageSequence = nil
	rootVariable := plan.Operations[0].RootScan.Variable
	terminal := &plan.Operations[len(plan.Operations)-1]
	if terminal.Kind != ir.PhysicalReturnOp || terminal.Return == nil {
		t.Fatalf("source plan has no terminal projection: %#v", terminal)
	}
	terminal.Return.Projections = []ir.PhysicalProjection{{
		Name: "resource_id", Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"id"}},
	}}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("render authorized source projection: %v", err)
	}
	rendered.BindVars["@root_collection"] = collection
	if _, populationSource := rendered.BindVars["@population_source_collection"]; populationSource {
		rendered.BindVars["@population_source_collection"] = collection
	}
	ids := make([]string, 0, 4)
	if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
		id, _ := row["resource_id"].(string)
		ids = append(ids, id)
		return nil
	}); err != nil {
		t.Fatalf("execute authorized source projection: %v\n%s", err, rendered.Query)
	}
	sort.Strings(ids)
	return ids
}

func assertCodedGroupSourceIDsMatchCodes(t *testing.T, rows []map[string]any, sourceIDs []string) {
	t.Helper()
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		code, _ := row["code"].(string)
		if row["code_system"] != "urn:loom:inline-test" || !constructionNumericEqual(row["source_records"], 1) {
			t.Errorf("coded-group row = %#v, want one authorized source per code", row)
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	if !reflect.DeepEqual(codes, sourceIDs) {
		t.Fatalf("inlined CODED_GROUP codes = %#v, authorized source projection IDs = %#v", codes, sourceIDs)
	}
}

func constructionCodedGroupTestOutput() recipe.Output {
	const idColumn = "resource_id"
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	return recipe.Output{
		Name: "coded_group_rows", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: idColumn, ColumnID: idColumn, Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: idColumn, Name: idColumn}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID: "group_codes",
					Source: recipe.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: "Observation", CodingPath: "component[].code.coding[]",
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
					},
					MissingKeyPolicy:     recipe.ConstructionGroupMissingKeyGroup,
					SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
					CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}},
		},
	}
}
