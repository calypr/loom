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

// This exercises standalone explicit-cohort row lineage against the same
// pinned membership documents used by GROUP_ROWS rendering. It intentionally
// uses a restricted scope so a revoked member cannot be confused with a
// missing source resource.
func TestStandaloneExplicitGroupRowLineagePagesAgainstArango(t *testing.T) {
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
		membershipsCollection, selectionMembersCollection,
	}
	specs := make([]store.CollectionSpec, 0, len(collections))
	for _, collection := range collections {
		specs = append(specs, store.CollectionSpec{Name: collection})
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: specs}); err != nil {
		t.Fatal(err)
	}

	project := "loom_cohort_lineage_" + uuid.NewString()
	generation := "cohort-lineage-generation"
	revisionID, selectionID := project+"_revision", project+"_selection"
	staleRevisionID, staleSelectionID := project+"_stale_revision", project+"_stale_selection"
	owned := make(map[string][]string, len(collections)+1)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR document IN %s FILTER document._key IN @keys REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove owned standalone cohort row-lineage fixtures from %s: %v", collection, err)
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
			t.Fatalf("insert %s standalone cohort row-lineage fixture: %v", collection, err)
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
		map[string]any{"_key": project + "_definition_assigned", "revisionId": revisionID, "project": project, "groupId": "assigned", "label": "Assigned", "ordinal": 0},
		map[string]any{"_key": project + "_definition_empty", "revisionId": revisionID, "project": project, "groupId": "empty", "label": "Empty", "ordinal": 1},
	)

	const allowedID, revokedID, missingID, unassignedID = "allowed-a", "revoked-b", "missing-c", "unassigned-z"
	selectionMembers := []map[string]any{}
	for _, id := range []string{allowedID, revokedID, missingID, unassignedID} {
		selectionMembers = append(selectionMembers, map[string]any{
			"_key": project + "_selected_" + id, "selectionId": selectionID, "project": project,
			"generation": generation, "resourceType": "Observation", "id": id,
		})
	}
	insert(selectionMembersCollection, selectionMembers...)
	insert(membershipsCollection,
		groupRowsMembership(revisionID, project, generation, "assigned", allowedID),
		groupRowsMembership(revisionID, project, generation, "assigned", revokedID),
		groupRowsMembership(revisionID, project, generation, "assigned", missingID),
	)
	insert("Observation",
		map[string]any{"_key": project + "_resource_" + allowedID, "id": allowedID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": map[string]any{"id": allowedID}},
		map[string]any{"_key": project + "_resource_" + revokedID, "id": revokedID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/revoked", "payload": map[string]any{"id": revokedID}},
		map[string]any{"_key": project + "_resource_" + unassignedID, "id": unassignedID, "project": project, "dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": map[string]any{"id": unassignedID}},
	)
	// missing-c has a verified selection and assigned membership but no current
	// resource document. GROUP_ROWS preserves that identity with a null payload.

	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
		IncludeRowIdentity: true,
	}
	output := recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: revisionID, UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
	}
	compiled := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiled); !capability.Available {
		t.Fatalf("standalone explicit GROUP_ROWS lineage capability = %#v", capability)
	}
	compilePage := func(groupID string, offset, limit int) CompiledRowLineageQuery {
		t.Helper()
		rowID, err := json.Marshal(map[string]string{"group_revision_id": revisionID, "group_id": groupID})
		if err != nil {
			t.Fatal(err)
		}
		query, err := CompileRowLineageOutput(compiled, string(rowID), offset, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile standalone cohort lineage page for %s at %d: %v", groupID, offset, err)
		}
		return query
	}
	assertPage := func(got map[string]any, wantIDs, wantOccurrenceKeys []string, wantFound, wantMore bool) {
		t.Helper()
		if got["found"] != wantFound || got["hasMore"] != wantMore {
			t.Fatalf("cohort lineage page found/hasMore = %#v/%#v, want %t/%t: %#v", got["found"], got["hasMore"], wantFound, wantMore, got)
		}
		contributors, ok := got["contributors"].([]any)
		if !ok || len(contributors) != len(wantIDs) {
			t.Fatalf("cohort lineage contributors = %#v, want IDs %#v", got["contributors"], wantIDs)
		}
		ids := make([]string, 0, len(contributors))
		for index, raw := range contributors {
			contributor, ok := raw.(map[string]any)
			if !ok || contributor["resourceType"] != "Observation" {
				t.Fatalf("cohort lineage contributor shape = %#v", raw)
			}
			id, _ := contributor["resourceId"].(string)
			if id == "" || index >= len(wantOccurrenceKeys) || contributor["occurrenceKey"] != wantOccurrenceKeys[index] {
				t.Fatalf("cohort lineage occurrence identity = %#v", contributor)
			}
			ids = append(ids, id)
		}
		if !reflect.DeepEqual(ids, append([]string{}, wantIDs...)) {
			t.Fatalf("cohort lineage page IDs = %#v, want canonical scoped members %#v", ids, wantIDs)
		}
	}

	first := executeRowLineageOracleQuery(t, ctx, client, compilePage("assigned", 0, 1))
	assertPage(first, []string{allowedID}, []string{"membership-" + revisionID + "-assigned-" + allowedID}, true, true)
	second := executeRowLineageOracleQuery(t, ctx, client, compilePage("assigned", 1, 1))
	assertPage(second, []string{missingID}, []string{"membership-" + revisionID + "-assigned-" + missingID}, true, false)
	pastEnd := executeRowLineageOracleQuery(t, ctx, client, compilePage("assigned", 2, 1))
	assertPage(pastEnd, nil, nil, true, false)

	empty := executeRowLineageOracleQuery(t, ctx, client, compilePage("empty", 0, 2))
	assertPage(empty, nil, nil, true, false)
	unassigned := executeRowLineageOracleQuery(t, ctx, client, compilePage("__loom_unassigned__", 0, 2))
	assertPage(unassigned, []string{unassignedID}, []string{project + "_selected_" + unassignedID}, true, false)

	// Compare these identities with canonical GROUP_ROWS materialization so
	// revoked members are filtered and a missing current resource remains an
	// identity-only contributor in both renderers.
	groupRowsQuery, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile canonical standalone GROUP_ROWS fixture: %v", err)
	}
	groupRows := executeReshapeOracleQuery(t, ctx, client, groupRowsQuery)
	groupMembers := make(map[string][]string, len(groupRows))
	for _, row := range groupRows {
		members, ok := row["members"].([]any)
		if !ok {
			t.Fatalf("canonical GROUP_ROWS members = %#v", row["members"])
		}
		ids := make([]string, 0, len(members))
		for _, raw := range members {
			member, _ := raw.(map[string]any)
			identity, _ := member["source_identity"].(map[string]any)
			id, _ := identity["id"].(string)
			if id == "" {
				t.Fatalf("canonical GROUP_ROWS member identity = %#v", raw)
			}
			if id == missingID && member["payload"] != nil {
				t.Fatalf("missing source resource payload = %#v, want null", member["payload"])
			}
			ids = append(ids, id)
		}
		groupMembers[row["group_id"].(string)] = ids
	}
	if !reflect.DeepEqual(groupMembers["assigned"], []string{allowedID, missingID}) ||
		!reflect.DeepEqual(groupMembers["empty"], []string{}) ||
		!reflect.DeepEqual(groupMembers["__loom_unassigned__"], []string{unassignedID}) {
		t.Fatalf("canonical GROUP_ROWS scoped members = %#v", groupMembers)
	}

	forgedID, err := json.Marshal(map[string]string{"group_revision_id": revisionID + "_other", "group_id": "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	forged, err := CompileRowLineageOutput(compiled, string(forgedID), 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile forged cross-revision row identity: %v", err)
	}
	assertPage(executeRowLineageOracleQuery(t, ctx, client, forged), nil, nil, false, false)

	// A stale source membership digest must fail the same guard used by the
	// canonical GROUP_ROWS renderer, before any contributor page is returned.
	insert(revisionsCollection, map[string]any{
		"_key": staleRevisionID, "state": "COMPLETE", "project": project, "generation": generation,
		"resourceType": "Observation", "sourceSelectionRevisionId": staleSelectionID,
		"scopeDigest": "scope-stale", "sourceMembershipDigest": "members-stale",
	})
	insert(selectionsCollection, map[string]any{
		"_key": staleSelectionID, "complete": true, "project": project, "generation": generation,
		"resourceType": "Observation", "scopeDigest": "scope-stale", "membershipDigest": "members-current",
	})
	staleOutput := output
	staleGroups := *output.GroupRows
	staleGroups.RevisionID = staleRevisionID
	staleOutput.GroupRows = &staleGroups
	staleCompiled := lowerConstructionOutput(t, staleOutput, bindings)
	staleRowID, err := json.Marshal(map[string]string{"group_revision_id": staleRevisionID, "group_id": "assigned"})
	if err != nil {
		t.Fatal(err)
	}
	staleQuery, err := CompileRowLineageOutput(staleCompiled, string(staleRowID), 0, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile stale standalone cohort row lineage: %v", err)
	}
	queryErr := client.QueryRows(ctx, staleQuery.Query, 32, staleQuery.BindVars, func(map[string]any) error { return nil })
	if queryErr == nil || !strings.Contains(queryErr.Error(), "EXPLICIT_GROUP_SOURCE_STALE") {
		t.Fatalf("source membership digest mismatch error = %v, want EXPLICIT_GROUP_SOURCE_STALE", queryErr)
	}
}
