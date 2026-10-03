package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestComposedCohortFilterLineageAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	const (
		revisionsCollection        = "loom_explorer_explicit_group_revisions"
		selectionsCollection       = "loom_explorer_selections"
		definitionsCollection      = "loom_explorer_explicit_group_definitions"
		membershipsCollection      = "loom_explorer_explicit_group_memberships"
		selectionMembersCollection = "loom_explorer_selection_members"
	)
	collections := []string{
		revisionsCollection, selectionsCollection, definitionsCollection,
		membershipsCollection, selectionMembersCollection, "Observation",
	}
	specs := make([]store.CollectionSpec, 0, len(collections))
	for _, collection := range collections {
		specs = append(specs, store.CollectionSpec{Name: collection})
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: specs}); err != nil {
		t.Fatal(err)
	}

	project := "loom_cohort_filter_lineage_" + uuid.NewString()
	generation := "cohort-filter-lineage-generation"
	revisionID, selectionID := project+"_revision", project+"_selection"
	owned := make(map[string][]string, len(collections))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR document IN %s FILTER document._key IN @keys REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove composed cohort filter-lineage fixtures from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents ...map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			key, ok := document["_key"].(string)
			if !ok || key == "" {
				t.Fatalf("fixture document in %s has no _key: %#v", collection, document)
			}
			owned[collection] = append(owned[collection], key)
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatalf("insert %s composed cohort filter-lineage fixture: %v", collection, err)
		}
	}

	insert(revisionsCollection, map[string]any{
		"_key": revisionID, "state": "COMPLETE", "project": project, "generation": generation,
		"resourceType": "Observation", "sourceSelectionRevisionId": selectionID,
		"scopeDigest": "scope-current", "sourceMembershipDigest": "members-current",
	})
	insert(selectionsCollection, map[string]any{
		"_key": selectionID, "complete": true, "project": project, "generation": generation,
		"resourceType": "Observation", "scopeDigest": "scope-current", "membershipDigest": "members-current",
	})
	insert(definitionsCollection,
		map[string]any{"_key": project + "_definition_cohort", "revisionId": revisionID, "project": project, "groupId": "cohort", "label": "Cohort", "ordinal": 0},
		map[string]any{"_key": project + "_definition_filtered", "revisionId": revisionID, "project": project, "groupId": "filtered", "label": "Other", "ordinal": 1},
	)
	insert(selectionMembersCollection,
		map[string]any{"_key": project + "_selected_allowed", "selectionId": selectionID, "project": project, "generation": generation, "resourceType": "Observation", "id": "allowed-a"},
		map[string]any{"_key": project + "_selected_revoked", "selectionId": selectionID, "project": project, "generation": generation, "resourceType": "Observation", "id": "revoked-b"},
		map[string]any{"_key": project + "_selected_other", "selectionId": selectionID, "project": project, "generation": generation, "resourceType": "Observation", "id": "other-c"},
	)
	insert(membershipsCollection,
		groupRowsMembership(revisionID, project, generation, "cohort", "allowed-a"),
		groupRowsMembership(revisionID, project, generation, "cohort", "revoked-b"),
		groupRowsMembership(revisionID, project, generation, "filtered", "other-c"),
	)
	insert("Observation",
		map[string]any{"_key": project + "_allowed", "id": "allowed-a", "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": map[string]any{"id": "allowed-a"}},
		map[string]any{"_key": project + "_revoked", "id": "revoked-b", "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/revoked", "payload": map[string]any{"id": "revoked-b"}},
		map[string]any{"_key": project + "_other", "id": "other-c", "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": map[string]any{"id": "other-c"}},
	)

	filterValue := "Cohort"
	output := recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "filter_group_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "group_label", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group_id", Type: "string"},
					{ID: "group_label", Name: "group_label", Type: "string"},
					{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
					{ID: "members", Name: "members", Type: "array"},
					{ID: "specimen_id", Name: "specimen_id", Type: "array"},
				},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: revisionID, UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}, IncludeRowIdentity: true,
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("composed cohort FILTER lineage capability = %#v", capability)
	}
	previewQuery, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile filtered cohort preview: %v", err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, previewQuery)
	if len(previewRows) != 1 || previewRows[0]["group_id"] != "cohort" || previewRows[0]["group_label"] != "Cohort" {
		t.Fatalf("filtered cohort preview rows = %#v", previewRows)
	}
	rowID, err := json.Marshal(previewRows[0]["__loom_row_id"])
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := CompileRowLineageOutput(compiledOutput, string(rowID), 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile final filtered cohort row lineage: %v", err)
	}
	assertLineage := func(query CompiledRowLineageQuery, wantFound bool, wantIDs, wantKeys []string) {
		t.Helper()
		got := executeRowLineageOracleQuery(t, ctx, client, query)
		if got["found"] != wantFound || got["hasMore"] != false {
			t.Fatalf("lineage found/hasMore = %#v/%#v, want %t/false", got["found"], got["hasMore"], wantFound)
		}
		contributors, ok := got["contributors"].([]any)
		if !ok || len(contributors) != len(wantIDs) {
			t.Fatalf("lineage contributors = %#v, want %#v", got["contributors"], wantIDs)
		}
		ids, keys := make([]string, 0, len(contributors)), make([]string, 0, len(contributors))
		for _, raw := range contributors {
			contributor, ok := raw.(map[string]any)
			if !ok || contributor["resourceType"] != "Observation" {
				t.Fatalf("lineage contributor shape = %#v", raw)
			}
			ids = append(ids, contributor["resourceId"].(string))
			keys = append(keys, contributor["occurrenceKey"].(string))
		}
		if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(keys, wantKeys) {
			t.Fatalf("lineage ids/occurrence keys = %#v/%#v, want %#v/%#v", ids, keys, wantIDs, wantKeys)
		}
	}
	assertLineage(lineage, true, []string{"allowed-a"}, []string{"membership-" + revisionID + "-cohort-allowed-a"})

	filteredOutID, err := json.Marshal(map[string]string{"group_revision_id": revisionID, "group_id": "filtered"})
	if err != nil {
		t.Fatal(err)
	}
	filteredOut, err := CompileRowLineageOutput(compiledOutput, string(filteredOutID), 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile filtered-out cohort row lineage: %v", err)
	}
	assertLineage(filteredOut, false, []string{}, []string{})

	forgedID, err := json.Marshal(map[string]string{"group_revision_id": revisionID, "group_id": "forged"})
	if err != nil {
		t.Fatal(err)
	}
	forged, err := CompileRowLineageOutput(compiledOutput, string(forgedID), 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile forged cohort row lineage: %v", err)
	}
	assertLineage(forged, false, []string{}, []string{})
}
