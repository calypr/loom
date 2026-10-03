package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestExplicitCohortMemberValuesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	collections := []string{"Patient", "loom_explorer_explicit_group_revisions", "loom_explorer_selections", "loom_explorer_explicit_group_definitions", "loom_explorer_explicit_group_memberships", "loom_explorer_selection_members"}
	specs := make([]store.CollectionSpec, 0, len(collections))
	for _, name := range collections {
		specs = append(specs, store.CollectionSpec{Name: name})
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: specs}); err != nil {
		t.Fatal(err)
	}
	project := "loom_cohort_values_" + uuid.NewString()
	generation := "cohort-generation"
	revision, selection := project+"_revision", project+"_selection"
	owned := map[string][]string{}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if err := client.ExecuteAQL(cleanupCtx, fmt.Sprintf("FOR d IN %s FILTER d._key IN @keys REMOVE d IN %s", collection, collection), map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove owned cohort fixture: %v", err)
			}
		}
	})
	insert := func(collection string, documents ...map[string]any) {
		t.Helper()
		var raw []json.RawMessage
		for _, document := range documents {
			owned[collection] = append(owned[collection], document["_key"].(string))
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	insert(collections[1], map[string]any{"_key": revision, "state": "COMPLETE", "project": project, "generation": generation, "resourceType": "Patient", "sourceSelectionRevisionId": selection, "scopeDigest": "scope", "sourceMembershipDigest": "members"})
	insert(collections[2], map[string]any{"_key": selection, "complete": true, "project": project, "generation": generation, "resourceType": "Patient", "scopeDigest": "scope", "membershipDigest": "members"})
	for index, group := range []string{"pair", "empty"} {
		insert(collections[3], map[string]any{"_key": project + "_" + group, "revisionId": revision, "project": project, "groupId": group, "label": group, "ordinal": index})
	}
	for index, id := range []string{"a", "b", "missing", "denied", "unassigned"} {
		member := map[string]any{"_key": project + "_selected_" + id, "selectionId": selection, "project": project, "generation": generation, "resourceType": "Patient", "id": id}
		insert(collections[5], member)
		if id != "unassigned" {
			insert(collections[4], map[string]any{"_key": project + "_membership_" + id, "revisionId": revision, "project": project, "generation": generation, "resourceType": "Patient", "id": id, "groupId": "pair", "ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": id}})
		}
		auth := "/allowed"
		if id == "denied" {
			auth = "/denied"
		}
		if id != "missing" {
			insert("Patient", map[string]any{"_key": project + "_hashed_" + id, "id": id, "project": project, "dataset_generation": generation, "resourceType": "Patient", "auth_resource_path": auth,
				"payload": map[string]any{"id": id, "gender": "female", "identifier": []any{map[string]any{"value": "shared"}, map[string]any{"value": fmt.Sprintf("value-%d", index)}}}})
		}
	}
	// Same FHIR ID outside either dataset boundary must never supply member values.
	for index, scope := range []map[string]any{{"project": "another-project", "dataset_generation": generation}, {"project": project, "dataset_generation": "another-generation"}} {
		scope["_key"], scope["id"], scope["resourceType"], scope["auth_resource_path"] = project+fmt.Sprintf("_outside_%d", index), "a", "Patient", "/allowed"
		scope["payload"] = map[string]any{"id": "leaked", "gender": "male"}
		insert("Patient", scope)
	}
	output := recipe.Output{Name: "cohorts", RootResourceType: "Patient", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: revision, UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED",
		RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "ids", Policy: recipe.ConstructionRowValueAll}, {ColumnID: "gender", Policy: recipe.ConstructionRowValueOne}, {ColumnID: "identifiers", Policy: recipe.ConstructionRowValueAll}}},
		Fields: []recipe.Field{{Name: "ids", ColumnID: "ids", Expr: recipe.Expression{Select: "root.id"}}, {Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}, {Name: "identifiers", ColumnID: "identifiers", Expr: recipe.Expression{Select: "root.identifier[].value"}}},
	}
	bindings := recipe.RuntimeBindings{Project: project, SelectionProject: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}, IncludeRowIdentity: true}
	compiled := lowerConstructionOutput(t, output, bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 3 {
		t.Fatalf("cohort rows: %#v", rows)
	}
	for _, row := range rows {
		var ids, identifiers []any
		var gender any
		switch row["group_id"] {
		case "pair":
			ids, identifiers, gender = []any{"a", "b"}, []any{"shared", "value-0", "value-1"}, "female"
			if len(row["members"].([]any)) != 3 {
				t.Fatalf("pinned missing-resource member identity was not retained: %#v", row)
			}
			missingPayload := false
			for _, rawMember := range row["members"].([]any) {
				member := rawMember.(map[string]any)
				identity := member["source_identity"].(map[string]any)
				if identity["id"] == "missing" && member["payload"] == nil {
					missingPayload = true
				}
			}
			if !missingPayload {
				t.Fatalf("standalone cohort did not preserve its null-payload pinned member: %#v", row["members"])
			}
		case "empty":
			ids, identifiers = []any{}, []any{}
		case "__loom_unassigned__":
			ids, identifiers, gender = []any{"unassigned"}, []any{"shared", "value-4"}, "female"
		default:
			t.Fatalf("unexpected cohort: %#v", row)
		}
		if !reflect.DeepEqual(row["ids"], ids) || !reflect.DeepEqual(row["identifiers"], identifiers) || row["gender"] != gender {
			t.Fatalf("cohort %s lost exact member values: got %#v; want ids=%#v identifiers=%#v gender=%#v", row["group_id"], row, ids, identifiers, gender)
		}
	}
	t.Run("retained_source_filter", func(t *testing.T) {
		filtered := output
		groups := *output.GroupRows
		groups.AfterStepID = "keep_a"
		filtered.GroupRows = &groups
		selectedID := "a"
		filtered.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{
			{ID: "ids", Name: "ids", Type: "string"},
			{ID: "gender", Name: "gender", Type: "string"},
			{ID: "identifiers", Name: "identifiers", Type: "array"},
		}, Steps: []recipe.ConstructionStep{{
			ID: "keep_a", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "ids", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedID}},
			}},
			Outputs: []recipe.StageColumn{{ID: "ids", Name: "ids", Type: "string"}, {ID: "gender", Name: "gender", Type: "string"}, {ID: "identifiers", Name: "identifiers", Type: "string"}},
		}}}
		compiled := lowerConstructionOutput(t, filtered, bindings)
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		got := executeReshapeOracleQuery(t, ctx, client, query)
		if len(got) != 3 {
			t.Fatalf("filter must preserve pinned cohort definitions and unassigned identity: %#v", got)
		}
		for _, row := range got {
			want := []any{}
			if row["group_id"] == "pair" {
				want = []any{"a"}
			}
			if !reflect.DeepEqual(row["ids"], want) || len(row["members"].([]any)) != len(want) {
				t.Fatalf("source filter was lost or regrouped excluded members: %#v; want IDs %#v", row, want)
			}
			if row["group_id"] == "pair" && len(row["members"].([]any)) != 1 {
				t.Fatalf("composed source filter retained a missing or excluded member that never reached its input stage: %#v", row)
			}
		}
	})
	t.Run("post_cohort_filter", func(t *testing.T) {
		filtered := output
		selectedGroup := "pair"
		filtered.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{
			{ID: "ids", Name: "ids", Type: "string"},
			{ID: "gender", Name: "gender", Type: "string"},
			{ID: "identifiers", Name: "identifiers", Type: "array"},
		}, Steps: []recipe.ConstructionStep{{
			ID: "keep_pair", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "group_id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedGroup}},
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group_id", Type: "string"},
				{ID: "group_label", Name: "group_label", Type: "string"},
				{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
				{ID: "members", Name: "members", Type: "array"},
				{ID: "ids", Name: "ids", Type: "array"},
				{ID: "gender", Name: "gender", Type: "string"},
				{ID: "identifiers", Name: "identifiers", Type: "array"},
			},
		}}}
		compiled := lowerConstructionOutput(t, filtered, bindings)
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		got := executeReshapeOracleQuery(t, ctx, client, query)
		if len(got) != 1 || got[0]["group_id"] != "pair" ||
			!reflect.DeepEqual(got[0]["ids"], []any{"a", "b"}) ||
			!reflect.DeepEqual(got[0]["identifiers"], []any{"shared", "value-0", "value-1"}) || len(got[0]["members"].([]any)) != 2 {
			t.Fatalf("post-cohort FILTER lost the pinned cohort's computed values: %#v", got)
		}
	})
	t.Run("one_disagreement", func(t *testing.T) {
		one := output
		groups := *output.GroupRows
		groups.RowValues = []recipe.GroupRowValuePolicy{{ColumnID: "ids", Policy: recipe.ConstructionRowValueOne}}
		one.GroupRows = &groups
		compiled := lowerConstructionOutput(t, one, bindings)
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		err = client.ExecuteAQL(ctx, query.Query, query.BindVars)
		if err == nil || !strings.Contains(err.Error(), "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") {
			t.Fatalf("ONE disagreement must preserve its typed repair error: %v", err)
		}
	})
	t.Run("restricted_empty_scope", func(t *testing.T) {
		denyAll := bindings
		denyAll.AuthResourcePaths = nil
		compiled := lowerConstructionOutput(t, output, denyAll)
		query, err := CompileRecipeOutputWithPolicy(compiled, denyAll, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range executeReshapeOracleQuery(t, ctx, client, query) {
			if !reflect.DeepEqual(row["ids"], []any{}) || row["gender"] != nil || !reflect.DeepEqual(row["identifiers"], []any{}) {
				t.Fatalf("restricted-empty authorization leaked cohort member data: %#v", row)
			}
			// A verified pinned selection may retain the old null-payload identity
			// for a resource that no longer exists; it cannot carry field values.
			for _, rawMember := range row["members"].([]any) {
				member := rawMember.(map[string]any)
				identity := member["source_identity"].(map[string]any)
				if identity["id"] != "missing" || member["payload"] != nil {
					t.Fatalf("restricted-empty authorization leaked a resolvable source member: %#v", member)
				}
			}
		}
	})
}
