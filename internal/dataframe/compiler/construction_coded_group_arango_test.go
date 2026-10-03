package compiler

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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

func TestConstructionCodedGroupRowLineagePagesScopedContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, foreignProject, generation := "loom_coded_lineage_"+uuid.NewString(), "loom_coded_lineage_foreign_"+uuid.NewString(), "generation-coded-lineage"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project IN @projects REMOVE document IN Observation",
			map[string]any{"projects": []string{project, foreignProject}},
		); err != nil {
			t.Errorf("remove CODED_GROUP row-lineage fixtures: %v", err)
		}
	})

	type fixture struct {
		project, generation, id, authPath, coding string
	}
	fixtures := []fixture{
		{project, generation, "coded-a", "Observation/visible", "duplicate"},
		{project, generation, "coded-b", "Observation/visible", "shared"},
		{project, generation, "coded-c", "Observation/visible", "shared"},
		{project, generation, "coded-denied", "Observation/hidden", "shared"},
		{project, "old-generation", "coded-old", "Observation/visible", "shared"},
		{foreignProject, generation, "coded-foreign", "Observation/visible", "shared"},
		{project, generation, "missing-a", "Observation/visible", "incomplete"},
		{project, generation, "missing-b", "Observation/visible", "absent"},
		{project, generation, "missing-denied", "Observation/hidden", "absent"},
		{project, "old-generation", "missing-old", "Observation/visible", "incomplete"},
		{foreignProject, generation, "missing-foreign", "Observation/visible", "absent"},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, item := range fixtures {
		payload := map[string]any{"id": item.id, "resourceType": "Observation"}
		switch item.coding {
		case "duplicate":
			payload["component"] = []any{codedGroupComponent("shared"), codedGroupComponent("shared")}
		case "shared":
			payload["component"] = []any{codedGroupComponent("shared")}
		case "incomplete":
			payload["component"] = []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "urn:loom:inline-test"},
			}}}}
		case "absent":
		default:
			t.Fatalf("unknown test coding kind %q", item.coding)
		}
		document, err := json.Marshal(map[string]any{
			"_key": item.project + "_" + item.id, "id": item.id,
			"project": item.project, "project_id": item.project, "dataset_generation": item.generation,
			"resourceType": "Observation", "auth_resource_path": item.authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert CODED_GROUP row-lineage fixtures: %v", err)
	}

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/visible"},
	}
	output := constructionCodedGroupTestOutput()
	compiled := lowerConstructionOutput(t, output, bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	var codedRow, missingRow map[string]any
	for _, row := range rows {
		switch row["code"] {
		case "shared":
			codedRow = row
		case nil:
			missingRow = row
		}
	}
	if len(rows) != 2 || codedRow == nil || missingRow == nil || !constructionNumericEqual(codedRow["source_records"], 3) ||
		!constructionNumericEqual(missingRow["source_records"], 2) {
		t.Fatalf("scoped coded rows = %#v, want three shared-code roots and two missing-code roots", rows)
	}
	getRowID := func(row map[string]any) string {
		t.Helper()
		rowID, ok := row["__loom_row_id"].(string)
		if !ok || rowID == "" {
			t.Fatalf("CODED_GROUP row identity = %#v", row["__loom_row_id"])
		}
		return rowID
	}
	compilePage := func(rowID string, offset int) CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := CompileRowLineageOutput(compiled, rowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile CODED_GROUP row-lineage page at %d: %v", offset, compileErr)
		}
		for _, want := range []string{
			"root.project == @project", "root.dataset_generation == @dataset_generation",
			"root.auth_resource_path IN @auth_resource_paths",
		} {
			if !strings.Contains(query.Query, want) {
				t.Errorf("CODED_GROUP row-lineage query lost source scope %q:\n%s", want, query.Query)
			}
		}
		return query
	}
	assertContributor := func(got any, id string) {
		t.Helper()
		contributor, _ := got.(map[string]any)
		if contributor["resourceType"] != "Observation" || contributor["resourceId"] != id || contributor["occurrenceKey"] != project+"_"+id {
			t.Errorf("CODED_GROUP contributor = %#v, want Observation/%s", contributor, id)
		}
	}
	for offset, id := range []string{"coded-a", "coded-b", "coded-c"} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(getRowID(codedRow), offset))
		contributors, _ := result["contributors"].([]any)
		wantMore := offset < 2
		if result["found"] != true || result["hasMore"] != wantMore || len(contributors) != 1 {
			t.Fatalf("CODED_GROUP page at %d = %#v, want one exact contributor and hasMore=%t", offset, result, wantMore)
		}
		assertContributor(contributors[0], id)
	}
	missingRowID := getRowID(missingRow)
	missing := executeRowLineageOracleQuery(t, ctx, client, compilePage(missingRowID, 0))
	missingContributors, _ := missing["contributors"].([]any)
	if missing["found"] != true || missing["hasMore"] != true || len(missingContributors) != 1 {
		t.Fatalf("valid missing-code row lineage = %#v, want its first contributor and another page", missing)
	}
	assertContributor(missingContributors[0], "missing-a")
	missingSecond := executeRowLineageOracleQuery(t, ctx, client, compilePage(missingRowID, 1))
	missingSecondContributors, _ := missingSecond["contributors"].([]any)
	if missingSecond["found"] != true || missingSecond["hasMore"] != false || len(missingSecondContributors) != 1 {
		t.Fatalf("second missing-code row lineage page = %#v, want final contributor", missingSecond)
	}
	assertContributor(missingSecondContributors[0], "missing-b")

	var identity []any
	if err := json.Unmarshal([]byte(missingRowID), &identity); err != nil || len(identity) < 2 {
		t.Fatalf("decode CODED_GROUP row identity %q: %v", missingRowID, err)
	}
	identity[1] = "different_group"
	wrongStageBytes, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, forgedID := range []string{"forged-coded-row-id", string(wrongStageBytes)} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(forgedID, 0))
		if result["found"] != false || result["hasMore"] != false || len(result["contributors"].([]any)) != 0 {
			t.Errorf("forged/wrong-stage CODED_GROUP identity disclosed contributors: %#v", result)
		}
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
