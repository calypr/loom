package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	compilerIR "github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	collections := []string{
		"Patient", "loom_explorer_explicit_group_revisions", "loom_explorer_selections", "loom_explorer_selection_members",
		"loom_explorer_explicit_group_definitions", "loom_explorer_explicit_group_memberships",
	}
	specs := make([]store.CollectionSpec, 0, len(collections))
	for _, name := range collections {
		specs = append(specs, store.CollectionSpec{Name: name})
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: specs}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_cohort_preview_"+uuid.NewString(), "cohort-preview-generation"
	revision, selection := project+"_revision", project+"_selection"
	owned := map[string][]string{}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for collection, keys := range owned {
			if err := client.ExecuteAQL(cleanupCtx, fmt.Sprintf("FOR d IN %s FILTER d._key IN @keys REMOVE d IN %s", collection, collection), map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove owned cohort fixture from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents ...map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			owned[collection] = append(owned[collection], document["_key"].(string))
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatalf("insert cohort fixture into %s: %v", collection, err)
		}
	}
	insert("loom_explorer_explicit_group_revisions", map[string]any{
		"_key": revision, "state": "COMPLETE", "project": project, "generation": generation,
		"resourceType": "Patient", "sourceSelectionRevisionId": selection,
		"scopeDigest": "scope", "sourceMembershipDigest": "membership-digest",
	})
	insert("loom_explorer_selections", map[string]any{
		"_key": selection, "complete": true, "project": project, "generation": generation,
		"resourceType": "Patient", "scopeDigest": "scope", "membershipDigest": "membership-digest",
	})
	for _, id := range []string{"a", "b"} {
		insert("loom_explorer_selection_members", map[string]any{
			"_key": project + "_selection_member_" + id, "selectionId": selection,
			"project": project, "generation": generation, "resourceType": "Patient", "id": id,
		})
	}
	insert("loom_explorer_explicit_group_definitions", map[string]any{
		"_key": project + "_pair", "revisionId": revision, "project": project,
		"groupId": "pair", "label": "Pair", "ordinal": 0,
	})
	insert("loom_explorer_explicit_group_memberships", map[string]any{
		"_key": project + "_membership_a", "revisionId": revision, "project": project,
		"generation": generation, "resourceType": "Patient", "id": "a", "groupId": "pair",
		"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": "a"},
	})
	for _, resource := range []struct{ key, id, gender string }{
		{key: "a_keep", id: "a", gender: "male"}, {key: "a_drop", id: "a", gender: "female"},
		{key: "b_keep", id: "b", gender: "male"}, {key: "outside", id: "x", gender: "male"},
	} {
		insert("Patient", map[string]any{
			"_key": project + "_" + resource.key, "id": resource.id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient", "gender": resource.gender,
			"payload": map[string]any{"id": resource.id, "gender": resource.gender},
		})
	}

	keepGender := "male"
	output := recipe.Output{
		Name: "Cohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{
			{Name: "ids", ColumnID: "ids", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{
				{ID: "ids", Name: "ids", Type: "string"}, {ID: "gender", Name: "gender", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "keep_all_members", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "gender", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &keepGender}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "ids", Name: "ids", Type: "string"}, {ID: "gender", Name: "gender", Type: "string"},
				},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: revision, AfterStepID: "keep_all_members", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED",
			RowValues: []recipe.GroupRowValuePolicy{
				{ColumnID: "ids", Policy: recipe.ConstructionRowValueAll},
				{ColumnID: "gender", Policy: recipe.ConstructionRowValueAll},
			},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort preview", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	rootPageQueries := 0
	candidateScanQuery := ""
	candidateScanBinds := map[string]any{}
	queryErr := error(nil)
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{}, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, RootPageRows: 1,
		QueryRows: func(queryCtx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			if _, paged := bindVars[compiler.RootPageKeysBind]; paged {
				rootPageQueries++
			}
			if strings.Contains(query, "cohort_source_candidate_ids") {
				candidateScanQuery = query
				candidateScanBinds = bindVars
			}
			queryErr = client.QueryRows(queryCtx, query, batchSize, bindVars, visit)
			return queryErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeUnrestricted, IncludeSourceIdentity: true, PreviewLimit: 25,
	}
	resolved, err := engine.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		t.Fatalf("compile cohort preview bundle: %v", err)
	}
	if len(resolved.Compiled.Outputs) != 1 || resolved.Compiled.Outputs[0].Plan.StageSequence == nil {
		t.Fatal("cohort receipt has no typed stage sequence")
	}
	foundCohortStage := false
	for _, stage := range resolved.Compiled.Outputs[0].Plan.StageSequence.Stages {
		foundCohortStage = foundCohortStage || stage.Kind == compilerIR.PhysicalStageCohortGroupOp
	}
	if !foundCohortStage {
		t.Fatal("composition did not insert its typed COHORT_GROUP stage")
	}
	if resolved.Compiled.Outputs[0].Plan.Operations[0].RootScan.CohortSource == nil {
		t.Fatal("safe FILTER→COHORT_GROUP prefix did not install the typed candidate root source")
	}
	assertPreview := func(phase string) {
		t.Helper()
		rootPageQueries, candidateScanQuery = 0, ""
		var rows []map[string]any
		summary, previewErr := engine.PreviewOutput(ctx, resolved, PreviewRequest{Output: "Cohort", Limit: 25, IncludeRowIdentity: true}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if previewErr != nil {
			t.Fatalf("execute %s whole-prefix cohort preview: %v (Arango query error: %v)\nbinds:\n%#v\nquery:\n%s", phase, previewErr, queryErr, candidateScanBinds, candidateScanQuery)
		}
		if rootPageQueries != 0 {
			t.Fatalf("%s cohort contributors were read through %d root-page queries with RootPageRows=1", phase, rootPageQueries)
		}
		if !strings.Contains(candidateScanQuery, "UNION_DISTINCT(") || !strings.Contains(candidateScanQuery, "FOR root IN @@root_collection") {
			t.Fatalf("%s preview query did not use the bounded duplicate-preserving cohort source scan:\n%s", phase, candidateScanQuery)
		}
		if summary.RowCount != 2 || len(rows) != 2 {
			t.Fatalf("%s preview emitted %d cohort rows, want assigned and unassigned rows: %#v", phase, summary.RowCount, rows)
		}
		if got, want := rows[0]["ids"], []any{"a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s assigned member values = %#v, want duplicate-ID filter to retain exactly %#v", phase, got, want)
		}
		if got, want := rows[0]["gender"], []any{"male"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s assigned gender values = %#v, want the filtered duplicate only %#v", phase, got, want)
		}
		if got, want := rows[1]["ids"], []any{"b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s unassigned member values = %#v, want selected unassigned member %#v", phase, got, want)
		}
		if got, want := rows[1]["gender"], []any{"male"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s unassigned gender values = %#v, want %#v", phase, got, want)
		}
	}
	assertPreview("initial")
	if err := client.ExecuteAQL(ctx, `FOR change IN @changes
  LET resource = DOCUMENT(@@patients, change.key)
	  UPDATE resource WITH {gender: change.gender, payload: MERGE(resource.payload, {gender: change.gender})} IN @@patients`, map[string]any{
		"@patients": "Patient",
		"changes": []map[string]any{
			{"key": project + "_a_keep", "gender": "female"},
			{"key": project + "_a_drop", "gender": "male"},
		},
	}); err != nil {
		t.Fatalf("swap duplicate resource genders: %v", err)
	}
	assertPreview("swapped duplicate gender")
}
