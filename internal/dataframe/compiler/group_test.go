package compiler

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"

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

	output := compilerfixtures.ObservationCodedGroupOutput()
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
	allOutput := compilerfixtures.ObservationCodedGroupOutput()
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
	selectedOutput := compilerfixtures.ObservationCodedGroupOutput()
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
	rendered, err := aql.Render(plan, aql.Options{})
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

func TestSemanticCategoryGroupPreservesExactMembershipAndRowMultiplicityAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)

	suffix := uuid.NewString()
	project, generation := "loom_semantic_group_"+suffix, "generation_"+suffix
	const targetSystem, targetVersion, codingPath = "urn:loom:semantic-group", "v1", "component[].code.coding[]"
	bindingID, familyID, ruleVersion := semanticGroupFixtureBinding(t, targetSystem, targetVersion, codingPath)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove semantic Group resources: %v", err)
		}
	})

	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{
			"id": "same-source-row", "status": "active",
			"component": []any{
				semanticGroupComponent(targetSystem, targetVersion, "shared-code", "Z Shared"),

				semanticGroupComponent(targetSystem, targetVersion, "shared-code", "A Shared"),
				semanticGroupComponent(targetSystem, targetVersion, "shared-code", "A Shared"),

				semanticGroupComponent(targetSystem, targetVersion, "different-code", "A Shared"),

				semanticGroupComponent(targetSystem, targetVersion, "fallback-code", "  "),
			},
		},
		{
			"id": "same-code-other-status", "status": "preliminary",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, "shared-code", "Z Shared")},
		},
		{
			"id": "same-code-active-second-row", "status": "active",
			"component": []any{semanticGroupComponent(targetSystem, targetVersion, "shared-code", "C Shared")},
		},
		{
			"id": "same-code-other-system", "status": "active",
			"component": []any{semanticGroupComponent("urn:loom:other-system", targetVersion, "shared-code", "A Shared")},
		},
		{
			"id": "same-code-other-version", "status": "active",
			"component": []any{semanticGroupComponent(targetSystem, "v2", "shared-code", "A Shared")},
		},
		{"id": "missing-category", "status": "active"},
	})
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	groupedOutput := semanticGroupArangoOutput(
		t, recipe.ConstructionGroupMissingKeyGroup, bindingID, familyID, ruleVersion,
		targetSystem, targetVersion, codingPath,
	)
	groupedCompiled := lowerConstructionOutput(t, groupedOutput, bindings)
	compile := func(policy recipe.ConstructionGroupMissingKeyPolicy) model.CompiledQuery {
		t.Helper()
		if policy == recipe.ConstructionGroupMissingKeyGroup {
			query, err := CompileRecipeOutputWithPolicy(groupedCompiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile semantic Group with %s missing-key policy: %v", policy, err)
			}
			return query
		}
		return compileSemanticCategoryGroupQuery(t, bindings, semanticGroupArangoOutput(
			t, policy, bindingID, familyID, ruleVersion, targetSystem, targetVersion, codingPath,
		))
	}

	groupedQuery := compile(recipe.ConstructionGroupMissingKeyGroup)
	grouped := executeReshapeOracleQuery(t, ctx, client, groupedQuery)
	if len(grouped) != 5 {
		t.Fatalf("semantic Group rows = %#v, want four keyed groups plus one missing-key group", grouped)
	}
	unlimitedQuery, err := CompileRecipeOutputWithPolicy(groupedCompiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile unlimited semantic Group query: %v", err)
	}
	unlimitedRows := executeReshapeOracleQuery(t, ctx, client, unlimitedQuery)
	if len(unlimitedRows) != 5 {
		t.Fatalf("unlimited semantic Group rows = %#v, want the same five exact groups with compiler-returned bind vars unchanged", unlimitedRows)
	}
	var sharedDisplayIDs []string
	got := make(map[string][]float64, len(grouped))
	for _, row := range grouped {
		status, _ := row["status"].(string)
		label, _ := row["category"].(string)
		labelKey := "<missing>"
		if row["category"] != nil {
			labelKey = label
		}
		key := status + "\x00" + labelKey
		got[key] = append(got[key], numericValue(row["rows"]))
		if status == "active" && label == "A Shared" {
			rowID, _ := row["__loom_row_id"].(string)
			sharedDisplayIDs = append(sharedDisplayIDs, rowID)
		}
	}

	if len(sharedDisplayIDs) != 2 || sharedDisplayIDs[0] == "" || sharedDisplayIDs[1] == "" || sharedDisplayIDs[0] == sharedDisplayIDs[1] {
		t.Fatalf("same-display semantic groups have row IDs %#v, want two distinct tuple identities", sharedDisplayIDs)
	}
	sharedDisplayCounts := got["active\x00A Shared"]
	validSharedCounts := len(sharedDisplayCounts) == 2 &&
		((constructionNumericEqual(sharedDisplayCounts[0], 2) && constructionNumericEqual(sharedDisplayCounts[1], 1)) ||
			(constructionNumericEqual(sharedDisplayCounts[0], 1) && constructionNumericEqual(sharedDisplayCounts[1], 2)))
	if len(got) != 4 || !validSharedCounts ||
		len(got["active\x00fallback-code"]) != 1 || !constructionNumericEqual(got["active\x00fallback-code"][0], 1) ||
		len(got["active\x00<missing>"]) != 1 || !constructionNumericEqual(got["active\x00<missing>"][0], 3) ||
		len(got["preliminary\x00Z Shared"]) != 1 || !constructionNumericEqual(got["preliminary\x00Z Shared"][0], 1) {
		t.Fatalf("semantic Group counts = %#v, want row-local dedupe, exact family scope, and scalar-key separation", got)
	}
	var sharedCodeRow map[string]any
	for _, row := range grouped {
		if row["status"] == "active" && row["category"] == "A Shared" && constructionNumericEqual(row["rows"], 2) {
			sharedCodeRow = row
		}
	}
	if sharedCodeRow == nil {
		t.Fatalf("active shared-code Group row with two contributors is missing: %#v", grouped)
	}
	sharedCodeRowID, _ := sharedCodeRow["__loom_row_id"].(string)
	lineageQuery, err := rowlineage.CompileRowLineageOutput(groupedCompiled, sharedCodeRowID, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group row-lineage page: %v", err)
	}
	identityProbe := lineageQuery
	identityProbe.BindVars = make(map[string]any, len(lineageQuery.BindVars))
	for key, value := range lineageQuery.BindVars {
		identityProbe.BindVars[key] = value
	}
	identityProbe.Query = strings.Replace(identityProbe.Query,
		"FILTER __loom_physical_construction_row_lineage_semantic_identity == @row_lineage_row_id",
		"FILTER __loom_physical_construction_row_lineage_semantic_collected_key_0 == @diagnostic_scalar_key AND __loom_physical_construction_row_lineage_semantic_tuple == @diagnostic_semantic_tuple", 1)
	identityProbe.Query = strings.Replace(identityProbe.Query,
		"RETURN {key0: __loom_physical_construction_row_lineage_semantic_collected_key_0, semanticTuple: __loom_physical_construction_row_lineage_semantic_tuple}",
		"RETURN {identity: __loom_physical_construction_row_lineage_semantic_identity, key0: __loom_physical_construction_row_lineage_semantic_collected_key_0, semanticTuple: __loom_physical_construction_row_lineage_semantic_tuple}", 1)
	identityProbe.Query = strings.Replace(identityProbe.Query,
		"RETURN {found: __loom_physical_construction_row_lineage_semantic_selected_group != null, contributors:",
		"RETURN {selectedGroup: __loom_physical_construction_row_lineage_semantic_selected_group, found: __loom_physical_construction_row_lineage_semantic_selected_group != null, contributors:", 1)
	identityProbe.BindVars["diagnostic_scalar_key"] = "active"
	identityProbe.BindVars["diagnostic_semantic_tuple"] = []any{targetSystem, targetVersion, "shared-code"}
	delete(identityProbe.BindVars, rowlineage.RowLineageRowIDBind)
	identityResult := recipetest.ExecuteRowLineageOracleQuery(t, ctx, client, identityProbe)
	selectedGroup, _ := identityResult["selectedGroup"].(map[string]any)
	lineageIdentity, _ := selectedGroup["identity"].(string)
	if lineageIdentity != sharedCodeRowID {
		t.Fatalf("executed semantic Group identity = %q, want actual grouped row ID %q; probe=%#v", lineageIdentity, sharedCodeRowID, identityResult)
	}
	lineage := recipetest.ExecuteRowLineageOracleQuery(t, ctx, client, lineageQuery)
	contributors, _ := lineage["contributors"].([]any)
	if lineage["found"] != true || lineage["hasMore"] != false || len(contributors) != 2 {
		t.Fatalf("semantic Group row lineage = %#v, want the two exact matching source rows; grouped row=%#v executed identity=%#v; AQL:\n%s",
			lineage, sharedCodeRow, identityResult, lineageQuery.Query)
	}
	wantContributorIDs := map[string]bool{"same-source-row": false, "same-code-active-second-row": false}
	for _, value := range contributors {
		contributor, _ := value.(map[string]any)
		id, _ := contributor["resourceId"].(string)
		if contributor["resourceType"] != "Observation" || contributor["occurrenceKey"] != project+"_"+id {
			t.Errorf("semantic Group contributor = %#v, want exact Observation row identity", contributor)
		}
		if _, ok := wantContributorIDs[id]; !ok {
			t.Errorf("unexpected semantic Group contributor ID %q", id)
		}
		wantContributorIDs[id] = true
	}
	for id, found := range wantContributorIDs {
		if !found {
			t.Errorf("semantic Group lineage omitted source ID %q", id)
		}
	}

	bagQuery := compile(recipe.ConstructionGroupMissingKeyGroup)
	const rootScan = "FOR root IN @@root_collection"
	if strings.Count(bagQuery.Query, rootScan) != 1 {
		t.Fatalf("semantic Group query has %d replaceable direct-root scans, want one:\n%s", strings.Count(bagQuery.Query, rootScan), bagQuery.Query)
	}
	bagQuery.Query = strings.Replace(bagQuery.Query, rootScan,
		"FOR root IN [DOCUMENT(@@root_collection, @duplicate_root_key), DOCUMENT(@@root_collection, @duplicate_root_key)]", 1)
	bagQuery.BindVars["duplicate_root_key"] = project + "_same-source-row"
	bagRows := executeReshapeOracleQuery(t, ctx, client, bagQuery)
	bagCounts := make(map[string][]float64, len(bagRows))
	for _, row := range bagRows {
		if row["status"] != "active" {
			t.Fatalf("test-only duplicate root relation included a different scalar group: %#v", row)
		}
		label := "<missing>"
		if row["category"] != nil {
			label, _ = row["category"].(string)
		}
		key := "active\x00" + label
		bagCounts[key] = append(bagCounts[key], numericValue(row["rows"]))
	}
	if len(bagRows) != 3 || len(bagCounts["active\x00A Shared"]) != 2 ||
		!constructionNumericEqual(bagCounts["active\x00A Shared"][0], 2) || !constructionNumericEqual(bagCounts["active\x00A Shared"][1], 2) ||
		len(bagCounts["active\x00fallback-code"]) != 1 || !constructionNumericEqual(bagCounts["active\x00fallback-code"][0], 2) {
		t.Fatalf("test-only duplicate physical row counts = %#v, want each exact Coding tuple once per repeated `_key` row", bagCounts)
	}

	for _, scope := range []struct {
		system, version string
	}{
		{system: targetSystem, version: "v2"},
		{system: "urn:loom:other-system", version: targetVersion},
	} {
		otherBinding, otherFamily, otherRule := semanticGroupFixtureBinding(t, scope.system, scope.version, codingPath)
		query := compileSemanticCategoryGroupQuery(t, bindings, semanticGroupArangoOutput(
			t, recipe.ConstructionGroupMissingKeyGroup, otherBinding, otherFamily,
			otherRule, scope.system, scope.version, codingPath,
		))
		rows := executeReshapeOracleQuery(t, ctx, client, query)
		var sameCodeRow map[string]any
		for _, row := range rows {
			if row["status"] == "active" && row["category"] == "A Shared" && constructionNumericEqual(row["rows"], 1) {
				sameCodeRow = row
			}
		}
		if sameCodeRow == nil {
			t.Fatalf("same code under system/version %s|%s did not form its own active group: %#v", scope.system, scope.version, rows)
		}
		rowID, _ := sameCodeRow["__loom_row_id"].(string)
		if rowID == "" || rowID == sharedDisplayIDs[0] || rowID == sharedDisplayIDs[1] {
			t.Fatalf("same code under system/version %s|%s reused another tuple row identity %q", scope.system, scope.version, rowID)
		}
	}

	excluded := executeReshapeOracleQuery(t, ctx, client, compile(recipe.ConstructionGroupMissingKeyExclude))
	if len(excluded) != 4 {
		t.Fatalf("EXCLUDE semantic Group rows = %#v, want only the four matching family memberships", excluded)
	}
	for _, row := range excluded {
		if row["category"] == nil {
			t.Fatalf("EXCLUDE retained a missing or out-of-family category: %#v", row)
		}
	}

	errorQuery := compile(recipe.ConstructionGroupMissingKeyError)
	if err := client.QueryRows(ctx, errorQuery.Query, 100, errorQuery.BindVars, func(map[string]any) error { return nil }); err == nil {
		t.Fatal("ERROR semantic Group accepted missing and out-of-family category rows")
	}
}

func semanticGroupFixtureBinding(t *testing.T, system, version, codingPath string) (bindingID, familyID, ruleVersion string) {
	t.Helper()
	const sourceID = "retained:Observation/semantic-group-binding-fixture"
	payload := map[string]any{
		"resourceType": "Observation",
		"component": []any{map[string]any{"code": map[string]any{"coding": []any{
			map[string]any{"system": system, "version": version, "code": "binding-witness", "display": "Binding witness"},
		}}}},
	}
	var contribution semantic.Contribution
	if err := semantic.NewEmitter("semantic-group-fixture", "generation").ObservePayload(
		payload, "Observation", "", sourceID,
		func(value semantic.Contribution) {
			if value.Observation.Source.Path == codingPath && value.Observation.Key.System == system && value.Observation.Key.Version == version {
				contribution = value
			}
		},
	); err != nil {
		t.Fatalf("derive semantic Coding binding for fixture: %v", err)
	}
	if contribution.BindingID == "" || contribution.Observation.Source.Path != codingPath {
		t.Fatalf("semantic Coding binding witness = %#v, want generated path %q", contribution, codingPath)
	}
	family := semantic.FamilyReference{
		BindingID: contribution.BindingID, SourceKind: semantic.SourceRetained,
		ResourceType: "Observation", SourcePath: codingPath, System: system, Version: version,
	}
	familyID = semantic.FamilyID(family)
	return contribution.BindingID, familyID, strconv.Itoa(semantic.SemanticObservationRuleVersion)
}

func semanticGroupComponent(system, version, code, display string) map[string]any {
	return map[string]any{"code": map[string]any{"coding": []any{
		map[string]any{"system": system, "version": version, "code": code, "display": display},
	}}}
}

func semanticGroupArangoOutput(
	t *testing.T,
	policy recipe.ConstructionGroupMissingKeyPolicy,
	bindingID, familyID, ruleVersion, system, version, codingPath string,
) recipe.Output {
	t.Helper()
	key := func(id string) map[string]any {
		return map[string]any{"inputColumnId": id, "outputColumnId": "group_" + id}
	}
	semanticKey := map[string]any{
		"outputColumnId": "category_id",
		"semanticCategory": map[string]any{
			"kind": "BINDING", "bindingId": bindingID, "ruleVersion": ruleVersion,
			"familyId": familyID, "system": system, "version": version,
			"sourceKind":   semantic.SourceRetained,
			"resourceType": "Observation", "codingPath": codingPath,
		},
	}
	wire := map[string]any{
		"name": "semantic_group_arango", "rootResourceType": "Observation", "rowGrain": "resource",
		"fields": []any{map[string]any{"name": "status", "columnId": "status_id", "expr": map[string]any{"select": "root.status"}}},
		"construction": map[string]any{
			"version":       1,
			"sourceColumns": []any{map[string]any{"id": "status_id", "name": "status", "type": "string"}},
			"steps": []any{map[string]any{
				"id": "group_semantic_category", "inputs": []any{map[string]any{"kind": "SOURCE_PROJECTION"}},
				"operation": map[string]any{"kind": "GROUP", "group": map[string]any{
					"constructionId": "group_semantic_category", "missingKeyPolicy": policy,
					"keys":       []any{key("status_id"), semanticKey},
					"aggregates": []any{map[string]any{"operation": "COUNT_ROWS", "outputColumnId": "rows_id"}},
				}},
				"outputs": []any{
					map[string]any{"id": "group_status_id", "name": "status", "type": "string"},
					map[string]any{"id": "category_id", "name": "category", "type": "string", "nullable": true},
					map[string]any{"id": "rows_id", "name": "rows", "type": "integer"},
				},
			}},
		},
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("encode raw semantic Group construction fixture: %v", err)
	}
	var output recipe.Output
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode raw semantic Group construction fixture: %v", err)
	}
	return output
}

func compileSemanticCategoryGroupQuery(t *testing.T, bindings recipe.RuntimeBindings, output recipe.Output) model.CompiledQuery {
	t.Helper()
	compiled := lowerConstructionOutput(t, output, bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile semantic Group query: %v", err)
	}
	return query
}
