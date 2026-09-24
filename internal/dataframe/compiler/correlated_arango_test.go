package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestNumericAggregateLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	selector, err := spec.ParseSelector("component[].valueInteger")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		values      []any
		want        *float64
		wantTypeErr bool
	}{
		{name: "non-null contributors ignore null and retain zero", values: []any{map[string]any{"valueInteger": 2}, map[string]any{"valueInteger": nil}, map[string]any{"valueInteger": 0}, map[string]any{"valueInteger": 4}}, want: float64Ptr(6)},
		{name: "no numeric contributors is null", values: []any{map[string]any{"valueInteger": nil}}, want: nil},
		{name: "malformed nonnumeric stored value fails closed", values: []any{map[string]any{"valueInteger": "not-numeric"}}, wantTypeErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "loom_numeric_aggregate_" + uuid.NewString()
			payload := map[string]any{"id": project, "resourceType": "Observation", "component": tc.values}
			raw, marshalErr := json.Marshal(map[string]any{
				"_key": project, "id": project, "project": project, "project_id": project,
				"resourceType": "Observation", "payload": payload,
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Aggregates: []semantic.SemanticAggregate{
				{Name: "sum_value", OutputName: "sum_value", Operation: "SUM", Selector: &selector},
				{Name: "mean_value", OutputName: "mean_value", Operation: "MEAN", Selector: &selector},
			}}
			physical, buildErr := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			rendered, renderErr := aql.RenderPhysicalPlan(physical)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			rows := []map[string]any{}
			queryErr := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if tc.wantTypeErr {
				if queryErr == nil || !strings.Contains(queryErr.Error(), "NUMERIC_AGGREGATE_NON_NUMERIC") {
					t.Fatalf("query error=%v, want numeric type failure\n%s", queryErr, rendered.Query)
				}
				return
			}
			if queryErr != nil {
				t.Fatalf("execute literal aggregate query: %v\n%s", queryErr, rendered.Query)
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%#v, want one root record", rows)
			}
			for _, name := range []string{"sum_value", "mean_value"} {
				got, exists := rows[0][name]
				if !exists {
					t.Fatalf("result lacks %q: %#v", name, rows[0])
				}
				if tc.want == nil {
					if got != nil {
						t.Errorf("%s=%#v, want null", name, got)
					}
					continue
				}
				want := *tc.want
				if name == "mean_value" {
					want /= 3 // 2, 0, and 4 contribute; the null is ignored.
				}
				value, ok := got.(float64)
				if !ok || math.Abs(value-want) > 1e-9 {
					t.Errorf("%s=%#v, want %v", name, got, want)
				}
			}
		})
	}
}

func TestExplicitGroupRowsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	collections := []store.CollectionSpec{
		{Name: "Observation"},
		{Name: "loom_explorer_explicit_group_revisions"},
		{Name: "loom_explorer_explicit_group_definitions"},
		{Name: "loom_explorer_explicit_group_memberships"},
		{Name: "loom_explorer_selections"},
		{Name: "loom_explorer_selection_members"},
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: collections}); err != nil {
		t.Fatal(err)
	}
	project, generation := "group_rows_"+uuid.NewString(), "generation_"+uuid.NewString()
	revisionID, selectionID := "grouprev_"+uuid.NewString(), "selection_"+uuid.NewString()
	member1ID, member2ID := project+"-member-1", project+"-member-2"
	missingMemberID, unrelatedID := project+"-member-missing", project+"-unrelated"
	resourceDocs := []map[string]any{
		{"_key": member1ID, "id": member1ID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/all", "payload": map[string]any{"resourceType": "Observation", "id": member1ID, "component": []any{map[string]any{"valueInteger": 1}, map[string]any{"valueInteger": 2}}}},
		{"_key": member2ID, "id": member2ID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/all", "payload": map[string]any{"resourceType": "Observation", "id": member2ID, "component": []any{map[string]any{"valueInteger": 3}}}},
		{"_key": unrelatedID, "id": unrelatedID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/all", "payload": map[string]any{"resourceType": "Observation", "id": unrelatedID, "component": []any{map[string]any{"valueInteger": 4}, map[string]any{"valueInteger": 5}, map[string]any{"valueInteger": 6}}}},
	}
	insertGroupRowsFixtureDocs(ctx, t, client, "Observation", resourceDocs)
	scopeDigest, sourceMembershipDigest := "group-row-scope", "group-row-source-membership"
	insertGroupRowsFixtureDocs(ctx, t, client, "loom_explorer_explicit_group_revisions", []map[string]any{{
		"_key": revisionID, "id": revisionID, "project": project, "generation": generation, "resourceType": "Observation",
		"scopeDigest": scopeDigest, "sourceSelectionRevisionId": selectionID, "sourceMembershipDigest": sourceMembershipDigest, "definitionDigest": "definition-digest",
		"membershipDigest": "membership-digest", "state": "COMPLETE",
	}})
	insertGroupRowsFixtureDocs(ctx, t, client, "loom_explorer_selections", []map[string]any{{
		"_key": selectionID, "id": selectionID, "project": project, "generation": generation, "resourceType": "Observation",
		"scopeDigest": scopeDigest, "membershipDigest": sourceMembershipDigest, "complete": true,
	}})
	insertGroupRowsFixtureDocs(ctx, t, client, "loom_explorer_explicit_group_definitions", []map[string]any{
		{"_key": "def-a-" + revisionID, "revisionId": revisionID, "project": project, "groupId": "group-a", "label": "Alpha", "ordinal": 0},
		{"_key": "def-b-" + revisionID, "revisionId": revisionID, "project": project, "groupId": "group-b", "label": "Beta", "ordinal": 1},
		{"_key": "def-empty-" + revisionID, "revisionId": revisionID, "project": project, "groupId": "group-empty", "label": "Empty", "ordinal": 2},
	})
	selectionIDs := []string{member1ID, member2ID, missingMemberID, project + "-member-unassigned"}
	selectionMembers := make([]map[string]any, 0, len(selectionIDs))
	for _, id := range selectionIDs {
		selectionMembers = append(selectionMembers, map[string]any{"_key": "sel-" + revisionID + "-" + id, "selectionId": selectionID, "project": project, "generation": generation, "resourceType": "Observation", "id": id})
	}
	insertGroupRowsFixtureDocs(ctx, t, client, "loom_explorer_selection_members", selectionMembers)
	groupMemberships := []map[string]any{
		groupRowsMembership(revisionID, project, generation, "group-a", member1ID),
		groupRowsMembership(revisionID, project, generation, "group-a", member2ID),
		groupRowsMembership(revisionID, project, generation, "group-a", missingMemberID),
		groupRowsMembership(revisionID, project, generation, "group-b", member1ID),
	}
	insertGroupRowsFixtureDocs(ctx, t, client, "loom_explorer_explicit_group_memberships", groupMemberships)

	execute := func(policy string) []map[string]any {
		t.Helper()
		queries, compileErr := compileGroupRowsQuery(project, generation, revisionID, policy)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		rows := make([]map[string]any, 0)
		if queryErr := client.QueryRows(ctx, queries[0].Query, 100, queries[0].BindVars, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		}); queryErr != nil {
			t.Fatalf("execute explicit group rows: %v\n%s", queryErr, queries[0].Query)
		}
		return rows
	}
	rows := execute("EXCLUDE")
	if len(rows) != 3 {
		t.Fatalf("group rows=%#v, want two populated groups and one declared empty group", rows)
	}
	wantGroups := []string{"group-a", "group-b", "group-empty"}
	wantMembers := [][]string{{member1ID, member2ID, missingMemberID}, {member1ID}, {}}
	identities := make([]any, len(rows))
	for index, row := range rows {
		if row["group_id"] != wantGroups[index] || row["group_revision_id"] != revisionID {
			t.Fatalf("group row %d identity/order = %#v", index, row)
		}
		identities[index] = row["__loom_row_id"]
		members, ok := row["members"].([]any)
		if !ok || len(members) != len(wantMembers[index]) {
			t.Fatalf("group %q members=%#v, want %d exact memberships", wantGroups[index], row["members"], len(wantMembers[index]))
		}
		for memberIndex, member := range members {
			value := member.(map[string]any)
			identity := value["source_identity"].(map[string]any)
			if identity["id"] != wantMembers[index][memberIndex] {
				t.Fatalf("group %q member order/identity = %#v, want %q", wantGroups[index], identity, wantMembers[index][memberIndex])
			}
			if identity["project"] != project || identity["generation"] != generation || identity["resource_type"] != "Observation" {
				t.Fatalf("member exact source identity = %#v", identity)
			}
			if identity["id"] == missingMemberID && value["payload"] != nil {
				t.Fatalf("missing resource payload = %#v, want null with membership retained", value["payload"])
			}
			if identity["id"] == member1ID {
				payload := value["payload"].(map[string]any)
				components := payload["component"].([]any)
				if len(components) != 2 {
					t.Fatalf("unreduced repeated member field = %#v, want both values", payload["component"])
				}
			}
		}
	}
	secondRun := execute("EXCLUDE")
	for index := range identities {
		if !reflect.DeepEqual(identities[index], secondRun[index]["__loom_row_id"]) {
			t.Fatalf("group identity changed across runs: %#v != %#v", identities[index], secondRun[index]["__loom_row_id"])
		}
	}

	unassignedQueries, err := compileGroupRowsQuery(project, generation, revisionID, "GROUP_AS_UNASSIGNED")
	if err != nil {
		t.Fatal(err)
	}
	unassignedRows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, unassignedQueries[0].Query, 100, unassignedQueries[0].BindVars, func(row map[string]any) error {
		unassignedRows = append(unassignedRows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute GROUP_AS_UNASSIGNED query: %v\n%s", err, unassignedQueries[0].Query)
	}
	if len(unassignedRows) != 4 || unassignedRows[3]["group_id"] != "__loom_unassigned__" {
		t.Fatalf("unassigned group rows = %#v", unassignedRows)
	}
	errorQuery := mustCompileGroupRowsQuery(t, project, generation, revisionID, "ERROR")
	if err := client.QueryRows(ctx, errorQuery.Query, 100, errorQuery.BindVars, func(map[string]any) error { return nil }); err == nil || !strings.Contains(err.Error(), "EXPLICIT_GROUP_UNASSIGNED_MEMBER") {
		t.Fatalf("ERROR policy query error = %v, want unassigned member assertion", err)
	}
}

func compileGroupRowsQuery(project, generation, revisionID, policy string) ([]CompiledQuery, error) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explicit groups", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "Grouped", RootResourceType: "Observation", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: revisionID, UnassignedMemberPolicy: policy}}},
	}, recipe.RuntimeBindings{Project: project, SelectionProject: project, DatasetGeneration: generation})
	if err != nil {
		return nil, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		return nil, err
	}
	return CompileResolvedRecipePlanWithPolicy(resolved, 100, ir.DefaultPhysicalOptimizationPolicy())
}

func mustCompileGroupRowsQuery(t *testing.T, project, generation, revisionID, policy string) CompiledQuery {
	t.Helper()
	queries, err := compileGroupRowsQuery(project, generation, revisionID, policy)
	if err != nil {
		t.Fatal(err)
	}
	return queries[0]
}

func groupRowsMembership(revisionID, project, generation, groupID, memberID string) map[string]any {
	return map[string]any{
		"_key":       "membership-" + revisionID + "-" + groupID + "-" + memberID,
		"revisionId": revisionID, "groupId": groupID, "project": project, "generation": generation,
		"resourceType": "Observation", "id": memberID,
		"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Observation", "id": memberID},
	}
}

func insertGroupRowsFixtureDocs(ctx context.Context, t *testing.T, client *store.Client, collection string, docs []map[string]any) {
	t.Helper()
	raw := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, encoded)
	}
	if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
		t.Fatalf("insert %s group fixture: %v", collection, err)
	}
}

func float64Ptr(value float64) *float64 { return &value }

func TestUnitNormalizationLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	valueSelector, err := spec.ParseSelector("valueQuantity.value")
	if err != nil {
		t.Fatal(err)
	}
	systemSelector, err := spec.ParseSelector("valueQuantity.system")
	if err != nil {
		t.Fatal(err)
	}
	codeSelector, err := spec.ParseSelector("valueQuantity.code")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, policy, system, code string
		value, want                float64
		wantUnknown                bool
	}{
		{name: "identity centimeters", policy: "to-centimeters", system: "http://unitsofmeasure.org", code: "cm", value: 180, want: 180},
		{name: "linear meters to centimeters", policy: "to-centimeters", system: "http://unitsofmeasure.org", code: "m", value: 1.8, want: 180},
		{name: "affine Celsius to Fahrenheit", policy: "to-fahrenheit", system: "http://unitsofmeasure.org", code: "Cel", value: 0, want: 32},
		{name: "affine Fahrenheit to Celsius", policy: "to-celsius", system: "http://unitsofmeasure.org", code: "[degF]", value: 32, want: 0},
		{name: "display label is not an identity", policy: "to-centimeters", code: "cm", value: 180, wantUnknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "loom_unit_" + uuid.NewString()
			payload := map[string]any{
				"id": project, "resourceType": "Observation",
				"valueQuantity": map[string]any{"value": tc.value, "system": tc.system, "code": tc.code, "unit": tc.code},
			}
			raw, marshalErr := json.Marshal(map[string]any{
				"_key": project, "id": project, "project": project, "project_id": project,
				"resourceType": "Observation", "payload": payload,
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}

			policy, policyErr := unit.ResolveApprovedUnitPolicy(tc.policy, "1")
			if policyErr != nil {
				t.Fatal(policyErr)
			}
			dimension, rules, resolveErr := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Aggregates: []semantic.SemanticAggregate{{
				Name: "normalized", OutputName: "normalized", Operation: "REQUIRE_ONE", Selector: &valueSelector,
				UnitSystemSelector: &systemSelector, UnitCodeSelector: &codeSelector,
				UnitNormalization: &unit.UnitNormalization{Target: policy.Target, Dimension: dimension, Rules: rules},
			}}}
			physical, buildErr := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			rendered, renderErr := aql.RenderPhysicalPlan(physical)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			rows := []map[string]any{}
			queryErr := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if tc.wantUnknown {
				if queryErr == nil || !strings.Contains(queryErr.Error(), "UNIT_IDENTITY_UNKNOWN") {
					t.Fatalf("query error = %v, want UNIT_IDENTITY_UNKNOWN\n%s", queryErr, rendered.Query)
				}
				return
			}
			if queryErr != nil {
				t.Fatalf("execute unit normalization query: %v\n%s", queryErr, rendered.Query)
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%#v, want one normalized row", rows)
			}
			got, ok := rows[0]["normalized"].(float64)
			if !ok || math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("normalized=%#v, want %v", rows[0]["normalized"], tc.want)
			}
		})
	}
}

func TestExtensionCompilerLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_extension_" + uuid.NewString()
	leaf := func(value string) map[string]any { return map[string]any{"url": "urn:leaf", "valueString": value} }
	payload := map[string]any{"id": project, "resourceType": "Observation", "extension": []any{
		map[string]any{"url": "urn:left", "extension": []any{leaf("left-one"), leaf("left-two")}},
		map[string]any{"url": "urn:right", "extension": []any{leaf("right-only")}},
	}}
	raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, parent, mode, path, kind string
		want                           any
	}{
		{"all retains both owners", "urn:left", "ALL", "valueString", "string", []any{"left-one", "left-two"}},
		{"value rejects both owners", "urn:left", "VALUE", "valueString", "string", map[string]any{"status": "INVALID_MULTIPLE_VALUES", "raw": []any{"left-one", "left-two"}}},
		{"right parent stays separate", "urn:right", "ALL", "valueString", "string", []any{"right-only"}},
		{"missing parent stays null", "urn:absent", "ALL", "valueString", "string", nil},
		{"omitted choice arms expose mismatch", "urn:right", "ALL", "valueQuantity.value", "decimal", map[string]any{"status": "INVALID_CHOICE_ARM", "raw": []any{"right-only"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := &fhirschema.ExtensionBinding{OwnerPath: "extension[].extension[]", URLPath: []string{tc.parent, "urn:leaf"}, ValuePath: tc.path, LogicalType: tc.kind}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "extension", Columns: []string{"feature"}, ColumnAliases: map[string]string{"feature": "feature"}, ProjectionMode: tc.mode, ExtensionCorrelation: binding}}}
			physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := aql.RenderPhysicalPlan(physical)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
				t.Fatalf("execute: %v\n%s", err, rendered.Query)
			}
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["feature"], tc.want) {
				t.Fatalf("rows=%#v; want feature=%#v\n%s", rows, tc.want, rendered.Query)
			}
		})
	}
}

func TestCorrelatedCompilerLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	coding := func(system, code string) map[string]any { return map[string]any{"system": system, "code": code} }
	component := func(codes []any, field string, value any) map[string]any {
		return map[string]any{"code": map[string]any{"coding": codes}, field: value}
	}
	quantity := func(value int) map[string]any { return map[string]any{"value": value, "unit": "cm"} }
	paired := []any{
		component([]any{coding("A", "shared"), coding("B", "decoy"), coding("A", "shared")}, "valueQuantity", quantity(111)),
		component([]any{coding("B", "shared"), coding("A", "other")}, "valueQuantity", quantity(222)),
		component([]any{coding("A", "shared")}, "valueQuantity", quantity(111)),
	}
	for _, tc := range []struct {
		name, system, code, mode, path, kind string
		components                           []any
		want                                 any
	}{
		{name: "all retains owners not duplicate coding", system: "A", code: "shared", mode: "ALL", components: paired, want: []any{float64(111), float64(111)}},
		{name: "distinct is explicit", system: "A", code: "shared", mode: "DISTINCT", components: paired, want: []any{float64(111)}},
		{name: "first is explicit", system: "A", code: "shared", mode: "FIRST", components: paired, want: float64(111)},
		{name: "value refuses multiplicity", system: "A", code: "shared", mode: "VALUE", components: paired, want: map[string]any{"status": "INVALID_MULTIPLE_VALUES", "raw": []any{float64(111), float64(111)}}},
		{name: "same coding and owning component", system: "B", code: "shared", mode: "VALUE", components: paired, want: float64(222)},
		{name: "cross element code cannot match", system: "B", code: "other", mode: "VALUE", components: paired, want: nil},
		{name: "missing system cannot match", system: "A", code: "shared", mode: "VALUE", components: []any{component([]any{map[string]any{"code": "shared"}}, "valueQuantity", quantity(333))}, want: nil},
		{name: "wrong choice arm remains visible", system: "A", code: "shared", mode: "VALUE", components: []any{component([]any{coding("A", "shared")}, "valueString", "not-numeric")}, want: map[string]any{"status": "INVALID_CHOICE_ARM", "raw": []any{"not-numeric"}}},
		{name: "string all remains array", system: "A", code: "shared", mode: "ALL", path: "valueString", kind: "string", components: []any{
			component([]any{coding("A", "shared"), coding("A", "shared")}, "valueString", "left"),
			component([]any{coding("A", "shared")}, "valueString", "right"),
		}, want: []any{"left", "right"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "loom_correlated_" + uuid.NewString()
			path, kind := tc.path, tc.kind
			if path == "" {
				path, kind = "valueQuantity.value", "decimal"
			}
			binding := &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: path, LogicalType: kind}
			payload := map[string]any{"id": project, "resourceType": "Observation", "component": tc.components}
			raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "feature", Columns: []string{tc.code}, ColumnAliases: map[string]string{tc.code: "height_cm"}, ProjectionMode: tc.mode, Correlation: binding, CorrelationSystem: tc.system, CorrelationCode: tc.code, StringifyValue: kind == "string"}}}
			var lastQuery string
			var lastBinds map[string]any
			query := func(node semantic.SemanticNode) []map[string]any {
				t.Helper()
				physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: node}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Fatal(err)
				}
				rendered, err := aql.RenderPhysicalPlan(physical)
				if err != nil {
					t.Fatal(err)
				}
				lastQuery, lastBinds = rendered.Query, rendered.BindVars
				rows := []map[string]any{}
				if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
					t.Fatalf("execute rendered query: %v\n%s", err, rendered.Query)
				}
				return rows
			}
			rows := query(root)
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["height_cm"], tc.want) {
				t.Fatalf("rows=%#v; height_cm must equal %#v\nbinds=%#v\n%s", rows, tc.want, lastBinds, lastQuery)
			}
			if tc.name == "same coding and owning component" {
				filter := func(system, code string) spec.TypedFilter {
					return spec.TypedFilter{FieldRef: "component", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: system, Code: code}}}, Correlation: binding}
				}
				root.Filters = []spec.TypedFilter{filter("A", "shared"), filter("B", "shared")}
				if got := query(root); len(got) != 1 {
					t.Fatalf("both present pairs must match: %#v", got)
				}
				root.Filters = []spec.TypedFilter{filter("B", "other"), filter("B", "shared")}
				if got := query(root); len(got) != 0 {
					t.Fatalf("independent absent pair must reject row: %#v", got)
				}
			}
		})
	}
}

func TestOwnerRecordsPreserveRepeatedFHIRValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_owner_records_" + uuid.NewString()
	coding := func(code string) map[string]any { return map[string]any{"system": "urn:test", "code": code} }
	component := func(code string, values map[string]any) map[string]any {
		owner := map[string]any{"code": map[string]any{"coding": []any{coding(code)}}}
		for key, value := range values {
			owner[key] = value
		}
		return owner
	}
	heightWithAliases := component("height", map[string]any{"valueQuantity": map[string]any{"value": 0, "unit": "cm"}, "sourceNote": "preserve me"})
	heightWithAliases["code"] = map[string]any{"coding": []any{coding("height"), coding("height")}}
	payload := map[string]any{"id": project, "resourceType": "Observation", "component": []any{
		heightWithAliases,
		component("height", map[string]any{"valueQuantity": map[string]any{"unit": "cm"}}),
		component("height", map[string]any{"valueString": "wrong arm"}),
		component("flag", map[string]any{"valueBoolean": false}),
		component("text", map[string]any{"valueString": ""}),
		component("multi", map[string]any{"valueCodeableConcept": map[string]any{"coding": []any{coding("a"), coding("b")}}}),
	}}
	raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
		t.Fatal(err)
	}

	binding := func(valuePath, logicalType string, choiceArms []string) fhirschema.CorrelatedBinding {
		return fhirschema.CorrelatedBinding{
			OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code",
			ValuePath: valuePath, LogicalType: logicalType, ChoiceArms: choiceArms,
		}
	}
	heightBinding := binding("valueQuantity.value", "decimal", []string{"valueQuantity"})
	heightBinding.UnitPath = "valueQuantity.unit"
	root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", OwnerRecords: []semantic.SemanticOwnerRecords{
		{Name: "height_records", FieldRef: "component", Binding: heightBinding, Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "height"}},
		{Name: "flag_records", FieldRef: "component", Binding: binding("valueBoolean", "boolean", []string{"valueBoolean"}), Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "flag"}},
		{Name: "text_records", FieldRef: "component", Binding: binding("valueString", "string", []string{"valueString"}), Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "text"}},
		{Name: "multi_records", FieldRef: "component", Binding: binding("valueCodeableConcept.coding[].code", "string", []string{"valueCodeableConcept"}), Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "multi"}},
	}}
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute owner-record query: %v\n%s", err, rendered.Query)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%#v, want one Observation row", rows)
	}
	records := func(column string, want int) []any {
		t.Helper()
		got, ok := rows[0][column].([]any)
		if !ok || len(got) != want {
			t.Fatalf("%s=%#v, want %d records", column, rows[0][column], want)
		}
		return got
	}
	record := func(value any) map[string]any {
		t.Helper()
		got, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("record=%#v, want object", value)
		}
		return got
	}

	heights := records("height_records", 3)
	firstHeight := record(heights[0])
	if firstHeight["status"] != "VALUE" || firstHeight["value"] != float64(0) || firstHeight["unit"] != "cm" {
		t.Fatalf("zero-valued height record=%#v", firstHeight)
	}
	if codings, ok := firstHeight["codings"].([]any); !ok || len(codings) != 2 {
		t.Fatalf("matching coding aliases=%#v, want both aliases in one owner record", firstHeight["codings"])
	}
	if source := record(firstHeight["source"]); source["ownerOrdinal"] != float64(0) || source["resourceId"] != project || source["ownerPath"] != "component[]" {
		t.Fatalf("source evidence=%#v", source)
	}
	if owner := record(firstHeight["owner"]); owner["sourceNote"] != "preserve me" {
		t.Fatalf("raw owner=%#v, want unknown fields preserved", owner)
	}
	if record(heights[1])["status"] != "ABSENT" || record(heights[2])["status"] != "INVALID_CHOICE_ARM" {
		t.Fatalf("height statuses=%#v", heights)
	}

	flag := record(records("flag_records", 1)[0])
	if flag["status"] != "VALUE" || flag["value"] != false {
		t.Fatalf("false-valued record=%#v", flag)
	}
	text := record(records("text_records", 1)[0])
	if text["status"] != "VALUE" || text["value"] != "" {
		t.Fatalf("empty-string record=%#v", text)
	}
	multi := record(records("multi_records", 1)[0])
	if multi["status"] != "VALUE" || !reflect.DeepEqual(multi["value"], []any{"a", "b"}) || !reflect.DeepEqual(multi["values"], []any{"a", "b"}) {
		t.Fatalf("multiple-valued record=%#v", multi)
	}
}

func TestContributorPredicatesRemainFeatureLocalAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_contributor_" + uuid.NewString()
	patientKey := func(id string) string { return project + "_" + id }
	document := func(key, resourceType string, payload map[string]any) json.RawMessage {
		t.Helper()
		raw, marshalErr := json.Marshal(map[string]any{
			"_key": key, "id": payload["id"], "project": project, "project_id": project,
			"resourceType": resourceType, "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return raw
	}
	patients := []json.RawMessage{
		document(patientKey("p1"), "Patient", map[string]any{"id": "p1", "resourceType": "Patient"}),
		document(patientKey("p2"), "Patient", map[string]any{"id": "p2", "resourceType": "Patient"}),
	}
	observations := []json.RawMessage{}
	edges := []json.RawMessage{}
	for index, status := range []string{"registered", "cancelled", "cancelled"} {
		observationKey := fmt.Sprintf("%s_o%d", project, index+1)
		observations = append(observations, document(observationKey, "Observation", map[string]any{
			"id": fmt.Sprintf("o%d", index+1), "resourceType": "Observation", "status": status,
			"subject": map[string]any{"reference": "Patient/p1"},
		}))
		edge, marshalErr := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_e%d", project, index+1),
			"_from": "Observation/" + observationKey, "_to": "Patient/" + patientKey("p1"),
			"project": project, "project_id": project,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		edges = append(edges, edge)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", patients, false, "document"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", observations, false, "document"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatal(err)
	}

	predicate := func(value string) *spec.TypedFilter {
		return &spec.TypedFilter{FieldRef: "observation-status", Selector: "status", FieldKind: spec.FilterString,
			Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &value}}}
	}
	root := semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{testSemanticField("patient_id", spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}, spec.ProjectionFirst)},
		Children: []semantic.SemanticNode{
			{Alias: "registered_observations", ResourceType: "Observation", EdgeLabel: "subject_Patient", Aggregates: []semantic.SemanticAggregate{{Name: "registered_count", OutputName: "registered_count", Operation: "COUNT", Predicate: predicate("registered")}}},
			{Alias: "cancelled_observations", ResourceType: "Observation", EdgeLabel: "subject_Patient", Aggregates: []semantic.SemanticAggregate{{Name: "cancelled_count", OutputName: "cancelled_count", Operation: "COUNT", Predicate: predicate("cancelled")}}},
		},
	}
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.BindVars["aggregate_child_set_1_registered_count_predicate_value"] != "registered" || rendered.BindVars["aggregate_child_set_2_cancelled_count_predicate_value"] != "cancelled" {
		t.Fatalf("sibling contributor binds collided or disappeared: %#v", rendered.BindVars)
	}
	rows := []map[string]any{}
	if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute contributor query: %v\n%s", err, rendered.Query)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%#v, want both Patient roots", rows)
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[fmt.Sprint(row["patient_id"])] = row
	}
	for id, want := range map[string][2]float64{"p1": {1, 2}, "p2": {0, 0}} {
		row := byID[id]
		if row == nil || row["registered_count"] != want[0] || row["cancelled_count"] != want[1] {
			t.Fatalf("patient %s row=%#v, want registered=%v cancelled=%v; all rows=%#v", id, row, want[0], want[1], rows)
		}
	}

	root.Children[0].MatchMode = spec.TraversalMatchRequired
	requiredPhysical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	requiredRendered, err := aql.RenderPhysicalPlan(requiredPhysical)
	if err != nil {
		t.Fatal(err)
	}
	requiredRows := []map[string]any{}
	if err := client.QueryRows(ctx, requiredRendered.Query, 100, requiredRendered.BindVars, func(row map[string]any) error {
		requiredRows = append(requiredRows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute required-match query: %v\n%s", err, requiredRendered.Query)
	}
	if len(requiredRows) != 1 || requiredRows[0]["patient_id"] != "p1" || requiredRows[0]["registered_count"] != float64(1) || requiredRows[0]["cancelled_count"] != float64(2) {
		t.Fatalf("required-match rows=%#v, want only p1 with independent contributor counts 1 and 2", requiredRows)
	}
}

func TestRecipeOccurrenceExpansionLiteralRowsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "Specimen"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	key := func(prefix string) string { return prefix + strings.ReplaceAll(uuid.NewString(), "-", "") }
	project, generation := key("loom_expansion_project_"), key("generation_")
	patientKey, emptyPatientKey := key("patient_"), key("patient_empty_")
	sharedSpecimenKey, emptySpecimenKey := key("specimen_shared_"), key("specimen_empty_")
	observationKeys := []string{key("observation_"), key("observation_"), key("observation_"), key("observation_")}
	documents := map[string][]json.RawMessage{"Patient": {}, "Observation": {}, "Specimen": {}}
	edges := []json.RawMessage{}
	appendJSON := func(collection string, value map[string]any) {
		t.Helper()
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents[collection] = append(documents[collection], raw)
	}
	appendResource := func(collection, documentKey, resourceType string, payload map[string]any) {
		payload["id"], payload["resourceType"] = documentKey, resourceType
		appendJSON(collection, map[string]any{
			"_key": documentKey, "id": documentKey, "project": project, "project_id": project,
			"resourceType": resourceType, "dataset_generation": generation, "payload": payload,
		})
	}
	appendEdge := func(edgeKey, from, to, label, fromType, toType string) {
		raw, marshalErr := json.Marshal(map[string]any{
			"_key": edgeKey, "_from": from, "_to": to, "project": project, "project_id": project,
			"dataset_generation": generation, "label": label, "from_type": fromType, "to_type": toType,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		edges = append(edges, raw)
	}
	appendResource("Patient", patientKey, "Patient", map[string]any{
		"name":       []any{map[string]any{"text": "duplicate"}, map[string]any{"text": "duplicate"}},
		"identifier": []any{map[string]any{"value": "id-one"}, map[string]any{"value": "id-two"}},
	})
	appendResource("Patient", emptyPatientKey, "Patient", map[string]any{"name": []any{}})
	for _, observationKey := range observationKeys {
		appendResource("Observation", observationKey, "Observation", map[string]any{})
	}
	appendResource("Specimen", sharedSpecimenKey, "Specimen", map[string]any{
		"container": []any{map[string]any{"description": "same-container"}, map[string]any{"description": "same-container"}},
	})
	appendResource("Specimen", emptySpecimenKey, "Specimen", map[string]any{"container": []any{}})
	for index, observationKey := range observationKeys {
		appendEdge(key("patient_observation_edge_"), "Observation/"+observationKey, "Patient/"+patientKey, "subject_Patient", "Observation", "Patient")
		if index < 2 {
			appendEdge(key("observation_shared_specimen_edge_"), "Observation/"+observationKey, "Specimen/"+sharedSpecimenKey, "specimen_Specimen", "Observation", "Specimen")
		} else if index == 2 {
			appendEdge(key("observation_empty_specimen_edge_"), "Observation/"+observationKey, "Specimen/"+emptySpecimenKey, "specimen_Specimen", "Observation", "Specimen")
		}
	}
	for _, collection := range []string{"Patient", "Observation", "Specimen"} {
		if err := client.InsertBatchRaw(ctx, collection, documents[collection], false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatal(err)
	}

	rootOutput := recipe.Output{
		Name: "ExpandedPatientNames", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		Fields: []recipe.Field{
			{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "identifier_values", Expr: recipe.Expression{Select: "root.identifier[].value"}, ValueMode: recipe.ValueModeAll},
		},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "patient-root", From: recipe.Expression{Select: "root.name[]"}, As: "expanded_name", Ordinality: "position", EmptyPolicy: recipe.ExpansionExclude},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	rootRows := runRecipeExpansionQuery(t, ctx, client, project, generation, rootOutput)
	if len(rootRows) != 2 {
		t.Fatalf("EXCLUDE root rows = %#v, want two repeated values and no row for the empty root", rootRows)
	}
	rootOrdinals := map[float64]bool{}
	for _, row := range rootRows {
		if row["patient_id"] != patientKey {
			t.Fatalf("expanded root row = %#v, want only the populated root", row)
		}
		if got := row["identifier_values"]; !reflect.DeepEqual(got, []any{"id-one", "id-two"}) {
			t.Fatalf("unrelated repeated identifier collection = %#v, want both values without multiplying rows", got)
		}
		identity := expansionIdentityFromRow(t, row)
		if identity["root_id"] != "Patient/"+patientKey || identity["occurrence_id"] != "patient-root" || identity["owner_id"] != "Patient/"+patientKey || identity["item_present"] != true {
			t.Fatalf("root expansion identity = %#v", identity)
		}
		ordinal, ok := identity["ordinal"].(float64)
		if !ok || ordinal > 1 || rootOrdinals[ordinal] {
			t.Fatalf("root ordinal = %#v, seen=%#v", identity["ordinal"], rootOrdinals)
		}
		rootOrdinals[ordinal] = true
	}
	if !rootOrdinals[0] || !rootOrdinals[1] {
		t.Fatalf("duplicate equal root items did not retain distinct ordinals: %#v", rootOrdinals)
	}
	t.Logf("root expansion rows: %#v", rootRows)

	rootOutput.Expand.EmptyPolicy = recipe.ExpansionPreserveParent
	preservedRootRows := runRecipeExpansionQuery(t, ctx, client, project, generation, rootOutput)
	if len(preservedRootRows) != 3 {
		t.Fatalf("PRESERVE_PARENT root rows = %#v, want two items plus one empty-owner row", preservedRootRows)
	}
	var preservedEmptyRoot bool
	for _, row := range preservedRootRows {
		identity := expansionIdentityFromRow(t, row)
		if identity["owner_id"] == "Patient/"+emptyPatientKey {
			preservedEmptyRoot = true
			if identity["item_present"] != false || identity["ordinal"] != nil {
				t.Fatalf("empty root identity = %#v, want no item and null ordinal", identity)
			}
		}
	}
	if !preservedEmptyRoot {
		t.Fatalf("PRESERVE_PARENT omitted the existing empty root: %#v", preservedRootRows)
	}

	deepOutput := recipe.Output{
		Name: "ExpandedSpecimenContainers", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingAlias,
		Fields:                []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Expand:                &recipe.Expansion{OwnerOccurrenceID: "specimen-owner", From: recipe.Expression{Select: "specimen.container[]"}, As: "container_item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent},
		Identity:              &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation",
			Traversals: []recipe.Traversal{{Name: "specimen_Specimen", OccurrenceID: "specimen-owner", Alias: "specimen", ToResourceType: "Specimen"}},
		}},
	}
	deepRows := runRecipeExpansionQuery(t, ctx, client, project, generation, deepOutput)
	secondDeepRows := runRecipeExpansionQuery(t, ctx, client, project, generation, deepOutput)
	if len(deepRows) != 5 || len(secondDeepRows) != 5 {
		t.Fatalf("deep preserve rows = %d / %d, want four path-item rows and one existing-empty-owner row", len(deepRows), len(secondDeepRows))
	}
	firstIdentities := make([]map[string]any, 0, len(deepRows))
	secondIdentities := make([]map[string]any, 0, len(secondDeepRows))
	for _, row := range deepRows {
		firstIdentities = append(firstIdentities, expansionIdentityFromRow(t, row))
	}
	for _, row := range secondDeepRows {
		secondIdentities = append(secondIdentities, expansionIdentityFromRow(t, row))
	}
	if !reflect.DeepEqual(firstIdentities, secondIdentities) {
		t.Fatalf("hidden expansion identities changed across identical executions: %#v != %#v", firstIdentities, secondIdentities)
	}
	t.Logf("related expansion identities: %#v", firstIdentities)
	pathWitnesses := map[float64]map[string]bool{0: {}, 1: {}}
	itemCounts := map[float64]int{}
	var emptyOwnerRows int
	for index, row := range deepRows {
		identity := firstIdentities[index]
		if identity["root_id"] != "Patient/"+patientKey || identity["occurrence_id"] != "specimen-owner" {
			t.Fatalf("deep expansion identity = %#v", identity)
		}
		if identity["owner_id"] == "Specimen/"+emptySpecimenKey {
			emptyOwnerRows++
			if identity["item_present"] != false || identity["ordinal"] != nil {
				t.Fatalf("empty related owner row=%#v identity=%#v", row, identity)
			}
			continue
		}
		if identity["owner_id"] != "Specimen/"+sharedSpecimenKey || identity["item_present"] != true {
			t.Fatalf("expanded related row=%#v identity=%#v", row, identity)
		}
		ordinal, ok := identity["ordinal"].(float64)
		if !ok || (ordinal != 0 && ordinal != 1) {
			t.Fatalf("deep ordinal = %#v", identity["ordinal"])
		}
		path := fmt.Sprint(identity["edge_0_id"], "|", identity["edge_1_id"])
		pathWitnesses[ordinal][path] = true
		itemCounts[ordinal]++
	}
	if emptyOwnerRows != 1 || itemCounts[0] != 2 || itemCounts[1] != 2 || len(pathWitnesses[0]) != 2 || len(pathWitnesses[1]) != 2 {
		t.Fatalf("deep owner/item/path witnesses: empty=%d counts=%#v paths=%#v", emptyOwnerRows, itemCounts, pathWitnesses)
	}
	for ordinal, paths := range pathWitnesses {
		if len(paths) != 2 {
			t.Fatalf("duplicate graph paths to one owner collapsed for ordinal %v: %#v", ordinal, paths)
		}
	}

	deepOutput.Expand.EmptyPolicy = recipe.ExpansionError
	queries, err := compileRecipeExpansionQuery(t, project, generation, deepOutput)
	if err != nil {
		t.Fatal(err)
	}
	queryErr := client.QueryRows(ctx, queries[0].Query, 100, queries[0].BindVars, func(map[string]any) error { return nil })
	if queryErr == nil || !strings.Contains(queryErr.Error(), "specimen-owner") || !strings.Contains(queryErr.Error(), emptySpecimenKey) {
		t.Fatalf("ERROR policy error = %v, want owner-boundary assertion for occurrence specimen-owner and empty owner %s\n%s", queryErr, emptySpecimenKey, queries[0].Query)
	}
}

func runRecipeExpansionQuery(t *testing.T, ctx context.Context, client *store.Client, project, generation string, output recipe.Output) []map[string]any {
	t.Helper()
	queries, err := compileRecipeExpansionQuery(t, project, generation, output)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, queries[0].Query, 100, queries[0].BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute occurrence expansion query: %v\n%s", err, queries[0].Query)
	}
	return rows
}

func compileRecipeExpansionQuery(t *testing.T, project, generation string, output recipe.Output) ([]CompiledQuery, error) {
	t.Helper()
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "literal-occurrence-expansion", TranslationVersion: "test", Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation})
	if err != nil {
		return nil, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		return nil, err
	}
	return CompileResolvedRecipePlanWithPolicy(resolved, 100, ir.DefaultPhysicalOptimizationPolicy())
}

func expansionIdentityFromRow(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	identity, ok := row["__loom_expansion_identity"].(map[string]any)
	if !ok {
		t.Fatalf("hidden expansion identity = %#v, want object", row["__loom_expansion_identity"])
	}
	return identity
}
