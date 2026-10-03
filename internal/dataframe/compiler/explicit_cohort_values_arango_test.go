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

func TestRelatedCountAppendPreservesExplicitCohortMembersAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	collections := []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
		{Name: "loom_explorer_explicit_group_revisions"}, {Name: "loom_explorer_selections"},
		{Name: "loom_explorer_explicit_group_definitions"}, {Name: "loom_explorer_explicit_group_memberships"},
		{Name: "loom_explorer_selection_members"},
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: collections}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_cohort_related_append_"+uuid.NewString(), "cohort-related-generation-"+uuid.NewString()
	revision, selection := project+"_revision", project+"_selection"
	owned := map[string][]string{}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR d IN %s FILTER d._key IN @keys REMOVE d IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove owned related-cohort fixtures from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents ...map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			key, ok := document["_key"].(string)
			if !ok || key == "" {
				t.Fatalf("fixture document in %s has no key: %#v", collection, document)
			}
			owned[collection] = append(owned[collection], key)
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatalf("insert %s related-cohort fixture: %v", collection, err)
		}
	}
	insert("loom_explorer_explicit_group_revisions", map[string]any{
		"_key": revision, "state": "COMPLETE", "project": project, "generation": generation, "resourceType": "Patient",
		"sourceSelectionRevisionId": selection, "scopeDigest": "scope-current", "sourceMembershipDigest": "members-current",
	})
	insert("loom_explorer_selections", map[string]any{
		"_key": selection, "complete": true, "project": project, "generation": generation, "resourceType": "Patient",
		"scopeDigest": "scope-current", "membershipDigest": "members-current",
	})
	for ordinal, group := range []string{"many", "one", "no_matches", "zero"} {
		insert("loom_explorer_explicit_group_definitions", map[string]any{
			"_key": project + "_definition_" + group, "revisionId": revision, "project": project,
			"groupId": group, "label": group, "ordinal": ordinal,
		})
	}
	const missingID, wrongProjectID, wrongGenerationID, filteredID, deniedID = "missing", "wrong_project", "wrong_generation", "filtered", "denied"
	assigned := []string{"a", "b", filteredID, deniedID, missingID, wrongProjectID, wrongGenerationID, "c", "no_observation", "unassigned"}
	selectionMembers := make([]map[string]any, 0, len(assigned))
	for _, id := range assigned {
		selectionMembers = append(selectionMembers, map[string]any{
			"_key": project + "_selected_" + id, "selectionId": selection, "project": project,
			"generation": generation, "resourceType": "Patient", "id": id,
		})
	}
	insert("loom_explorer_selection_members", selectionMembers...)
	for _, id := range []string{"a", "b", filteredID, deniedID, missingID, wrongProjectID, wrongGenerationID} {
		insert("loom_explorer_explicit_group_memberships", map[string]any{
			"_key": project + "_membership_many_" + id, "revisionId": revision, "project": project,
			"generation": generation, "resourceType": "Patient", "id": id, "groupId": "many",
			"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": id},
		})
	}
	insert("loom_explorer_explicit_group_memberships", map[string]any{
		"_key": project + "_membership_one_c", "revisionId": revision, "project": project,
		"generation": generation, "resourceType": "Patient", "id": "c", "groupId": "one",
		"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": "c"},
	}, map[string]any{
		"_key": project + "_membership_no_matches", "revisionId": revision, "project": project,
		"generation": generation, "resourceType": "Patient", "id": "no_observation", "groupId": "no_matches",
		"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": "no_observation"},
	})
	patient := func(id, resourceProject, resourceGeneration, authPath string) map[string]any {
		return map[string]any{
			"_key": project + "_patient_" + id + "_" + resourceProject + "_" + resourceGeneration,
			"id":   id, "project": resourceProject, "project_id": resourceProject, "dataset_generation": resourceGeneration,
			"resourceType": "Patient", "auth_resource_path": authPath, "payload": map[string]any{"id": id, "resourceType": "Patient"},
		}
	}
	for _, id := range []string{"a", "b", filteredID, "c", "no_observation", "unassigned"} {
		insert("Patient", patient(id, project, generation, "/allowed"))
	}
	insert("Patient", patient(deniedID, project, generation, "/denied"),
		patient(wrongProjectID, project+"_foreign", generation, "/allowed"),
		patient(wrongGenerationID, project, generation+"_old", "/allowed"))

	observation := func(id, resourceProject, resourceGeneration, authPath string) map[string]any {
		return map[string]any{
			"_key": project + "_observation_" + id, "id": id, "project": resourceProject, "project_id": resourceProject,
			"dataset_generation": resourceGeneration, "resourceType": "Observation", "auth_resource_path": authPath,
			"payload": map[string]any{"id": id, "resourceType": "Observation"},
		}
	}
	for _, id := range []string{"a1", "a2", "b1", "filtered1", "c1", "unassigned1", "denied_target", "edge_denied"} {
		auth := "/allowed"
		if id == "denied_target" {
			auth = "/denied"
		}
		insert("Observation", observation(id, project, generation, auth))
	}
	insert("Observation", observation("wrong_generation_target", project, generation+"_old", "/allowed"),
		observation("wrong_project_target", project+"_foreign", generation, "/allowed"))
	edge := func(key, observationID, patientID, edgeAuth string) map[string]any {
		return relatedCountEdge(project, generation, key,
			"Observation/"+project+"_observation_"+observationID,
			"Patient/"+project+"_patient_"+patientID+"_"+project+"_"+generation,
			"subject_Patient", "Observation", "Patient", edgeAuth)
	}
	edges := []map[string]any{
		edge("a1", "a1", "a", "/allowed"), edge("a1_duplicate", "a1", "a", "/allowed"),
		edge("a2", "a2", "a", "/allowed"), edge("b1", "b1", "b", "/allowed"),
		edge("filtered", "filtered1", filteredID, "/allowed"), edge("c1", "c1", "c", "/allowed"),
		edge("unassigned", "unassigned1", "unassigned", "/allowed"),
		edge("denied_root", "a1", deniedID, "/allowed"),
		edge("denied_target", "denied_target", "a", "/allowed"),
		edge("denied_edge", "edge_denied", "a", "/denied"),
		edge("wrong_generation", "wrong_generation_target", "a", "/allowed"),
		edge("wrong_project", "wrong_project_target", "a", "/allowed"),
	}
	insert("fhir_edge", edges...)

	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}, IncludeRowIdentity: true,
	}
	groupRows := func(after string) *recipe.GroupRows {
		return &recipe.GroupRows{
			RevisionID: revision, UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: after,
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "patient_id", Policy: recipe.ConstructionRowValueAll}},
		}
	}
	base := recipe.Output{
		Name: "Cohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields:    []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		GroupRows: groupRows(""),
	}
	relatedStep := func(input recipe.ConstructionInputRef, form string) recipe.ConstructionStep {
		outputName := map[string]string{"COUNT": "observation_count", "PRESENCE": "has_observation", "ALL": "observations"}[form]
		return recipe.ConstructionStep{
			ID: "add_observation_count", Inputs: []recipe.ConstructionInputRef{input},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "__loom_row_id", ChoiceID: "patient-observation-choice", SourceOccurrenceID: "observation-node",
				Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-id", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.id", Cardinality: "required_one", LogicalType: "string"},
				Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
				ContributorPolicy: "ALL_MATCHES", Form: form, OutputColumnID: "observation-count",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group_id"}, {ID: "group_label", Name: "group_label"},
				{ID: "group_ordinal", Name: "group_ordinal"}, {ID: "members", Name: "members"},
				{ID: "patient_id", Name: "patient_id"},
				{ID: "observation-count", Name: outputName},
			},
		}
	}
	withRelatedSource := func(output recipe.Output, steps []recipe.ConstructionStep, input recipe.ConstructionInputRef, form string) recipe.Output {
		updated := output
		construction := recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}}}
		construction.Steps = append(append([]recipe.ConstructionStep(nil), steps...), relatedStep(input, form))
		updated.Construction = &construction
		return updated
	}
	execute := func(output recipe.Output) []map[string]any {
		t.Helper()
		compiled := lowerConstructionOutput(t, output, bindings)
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile cohort output %q: %v", output.Name, err)
		}
		return executeReshapeOracleQuery(t, ctx, client, query)
	}
	assertRelatedCounts := func(rows []map[string]any, column string, expected map[string]float64) {
		t.Helper()
		counts := make(map[string]float64, len(rows))
		for _, row := range rows {
			if count, ok := row[column]; ok {
				counts[row["group_id"].(string)] = count.(float64)
			}
		}
		if !reflect.DeepEqual(counts, expected) {
			t.Fatalf("related COUNT values in %q = %#v, want %#v (rows %#v)", column, counts, expected, rows)
		}
	}
	assertRelatedPresence := func(rows []map[string]any, expected map[string]bool) {
		t.Helper()
		values := make(map[string]bool, len(rows))
		for _, row := range rows {
			value, ok := row["has_observation"].(bool)
			if !ok {
				t.Fatalf("related PRESENCE for group %q = %#v, want boolean", row["group_id"], row["has_observation"])
			}
			values[row["group_id"].(string)] = value
		}
		if !reflect.DeepEqual(values, expected) {
			t.Fatalf("related PRESENCE values = %#v, want %#v", values, expected)
		}
	}
	assertEmptyRelatedArrays := func(rows []map[string]any, groups ...string) {
		t.Helper()
		wantEmpty := make(map[string]bool, len(groups))
		for _, group := range groups {
			wantEmpty[group] = true
		}
		for _, row := range rows {
			group := row["group_id"].(string)
			if !wantEmpty[group] {
				continue
			}
			values, ok := row["observations"].([]any)
			if !ok || len(values) != 0 {
				t.Fatalf("related ALL values for zero-match group %q = %#v, want []", group, row["observations"])
			}
			delete(wantEmpty, group)
		}
		if len(wantEmpty) != 0 {
			t.Fatalf("related ALL omitted zero-match groups: %#v", wantEmpty)
		}
	}
	assertSameMembers := func(before, after []map[string]any, name string) {
		t.Helper()
		membersByGroup := func(rows []map[string]any) map[string]any {
			result := make(map[string]any, len(rows))
			for _, row := range rows {
				result[row["group_id"].(string)] = row["members"]
			}
			return result
		}
		if !reflect.DeepEqual(membersByGroup(before), membersByGroup(after)) {
			t.Fatalf("%s changed explicit cohort member rows: before=%#v after=%#v", name, membersByGroup(before), membersByGroup(after))
		}
		valuesByGroup := func(rows []map[string]any) map[string]any {
			result := make(map[string]any, len(rows))
			for _, row := range rows {
				result[row["group_id"].(string)] = row["patient_id"]
			}
			return result
		}
		if !reflect.DeepEqual(valuesByGroup(before), valuesByGroup(after)) {
			t.Fatalf("%s changed authored cohort row values: before=%#v after=%#v", name, valuesByGroup(before), valuesByGroup(after))
		}
	}
	assertFixtureMembers := func(rows []map[string]any, filtered bool) {
		t.Helper()
		ids := make(map[string][]string, len(rows))
		for _, row := range rows {
			members, ok := row["members"].([]any)
			if !ok {
				t.Fatalf("group %q members = %#v", row["group_id"], row["members"])
			}
			groupID := row["group_id"].(string)
			ids[groupID] = []string{}
			for _, raw := range members {
				member := raw.(map[string]any)
				identity := member["source_identity"].(map[string]any)
				id := identity["id"].(string)
				ids[groupID] = append(ids[groupID], id)
				if id == missingID || id == wrongProjectID || id == wrongGenerationID {
					if member["payload"] != nil {
						t.Fatalf("out-of-scope/missing cohort member %q leaked payload %#v", id, member["payload"])
					}
				}
				if id == deniedID {
					t.Fatalf("unauthorized root member appeared in group %q: %#v", row["group_id"], member)
				}
				if filtered && id == filteredID {
					t.Fatalf("pre-cohort filter did not exclude existing member %q", id)
				}
			}
		}
		many := []string{"a", "b", missingID, wrongGenerationID, wrongProjectID}
		if !filtered {
			many = []string{"a", "b", filteredID, missingID, wrongGenerationID, wrongProjectID}
		} else {
			many = []string{"a", "b"}
		}
		want := map[string][]string{"many": many, "one": {"c"}, "no_matches": {"no_observation"}, "zero": {}, "__loom_unassigned__": {"unassigned"}}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("cohort fixture members = %#v, want %#v", ids, want)
		}
	}

	baselineRows := execute(base)
	candidate := withRelatedSource(base, nil, recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}, "COUNT")
	candidateRows := execute(candidate)
	assertSameMembers(baselineRows, candidateRows, "adding related COUNT")
	assertFixtureMembers(candidateRows, false)
	assertRelatedCounts(candidateRows, "observation_count", map[string]float64{"many": 4, "one": 1, "no_matches": 0, "zero": 0, "__loom_unassigned__": 1})
	for _, form := range []string{"PRESENCE", "ALL"} {
		formRows := execute(withRelatedSource(base, nil, recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}, form))
		assertSameMembers(baselineRows, formRows, "adding related "+form)
		assertFixtureMembers(formRows, false)
		if form == "PRESENCE" {
			assertRelatedPresence(formRows, map[string]bool{"many": true, "one": true, "no_matches": false, "zero": false, "__loom_unassigned__": true})
		} else {
			assertEmptyRelatedArrays(formRows, "no_matches", "zero")
		}
	}

	filterValue := filteredID
	filteredSteps := []recipe.ConstructionStep{{
		ID: "exclude_filtered", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "patient_id", Operator: recipe.FilterNotEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
		}},
		Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
	}}
	filteredBase := base
	filteredBase.GroupRows = groupRows("exclude_filtered")
	filteredBase.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}}, Steps: filteredSteps}
	filteredBaselineRows := execute(filteredBase)
	filteredCandidate := withRelatedSource(filteredBase, filteredSteps, recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}, "COUNT")
	filteredRows := execute(filteredCandidate)
	assertSameMembers(filteredBaselineRows, filteredRows, "adding related COUNT after a filtered cohort")
	assertFixtureMembers(filteredRows, true)
	assertRelatedCounts(filteredRows, "observation_count", map[string]float64{"many": 3, "one": 1, "no_matches": 0, "zero": 0, "__loom_unassigned__": 1})
}

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
			!reflect.DeepEqual(got[0]["identifiers"], []any{"shared", "value-0", "value-1"}) || len(got[0]["members"].([]any)) != 3 {
			t.Fatalf("post-cohort FILTER lost the pinned cohort's computed values: %#v", got)
		}
		memberIDs := make([]string, 0, 3)
		missingNullPayload := false
		for _, rawMember := range got[0]["members"].([]any) {
			member := rawMember.(map[string]any)
			identity := member["source_identity"].(map[string]any)
			id := identity["id"].(string)
			memberIDs = append(memberIDs, id)
			if id == "denied" {
				t.Fatalf("post-cohort FILTER retained an unauthorized member: %#v", got[0]["members"])
			}
			if id == "missing" && member["payload"] == nil {
				missingNullPayload = true
			}
		}
		if !reflect.DeepEqual(memberIDs, []string{"a", "b", "missing"}) || !missingNullPayload {
			t.Fatalf("post-cohort FILTER changed exact pinned member identities/null payload: ids=%#v members=%#v", memberIDs, got[0]["members"])
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
