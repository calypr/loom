package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestPopulationMappingRoutedCohortPreservesSelectedMembersAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	project, generation := "loom_population_routed_"+suffix, "generation_"+suffix
	observations := "loom_population_routed_observations_" + suffix
	patients := "loom_population_routed_patients_" + suffix
	edges := "loom_population_routed_edges_" + suffix
	collections := []store.CollectionSpec{
		{Name: observations}, {Name: patients}, {Name: edges, Edge: true},
		{Name: "loom_explorer_explicit_group_revisions"}, {Name: "loom_explorer_selections"},
		{Name: "loom_explorer_explicit_group_definitions"}, {Name: "loom_explorer_explicit_group_memberships"},
		{Name: "loom_explorer_selection_members"},
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: collections}); err != nil {
		t.Fatal(err)
	}
	owned := make(map[string][]string)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR document IN %s FILTER document._key IN @keys REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove routed population mapping fixtures from %s: %v", collection, err)
			}
		}
		if err := dropPopulationFrontierCollections(cleanupCtx, os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE"), edges, patients, observations); err != nil {
			t.Errorf("drop routed population mapping collections: %v", err)
		}
	})
	insert := func(collection string, docs ...map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(docs))
		for _, doc := range docs {
			key, ok := doc["_key"].(string)
			if !ok || key == "" {
				t.Fatalf("fixture in %s has no _key: %#v", collection, doc)
			}
			owned[collection] = append(owned[collection], key)
			encoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatalf("insert routed population mapping fixtures into %s: %v", collection, err)
		}
	}
	key := func(id string) string { return suffix + "_" + id }

	type patientSeed struct {
		id, gender, project, generation, authPath string
	}
	patientSeeds := []patientSeed{
		{id: "shared", gender: "male", project: project, generation: generation, authPath: "/allowed"},
		{id: "filtered", gender: "female", project: project, generation: generation, authPath: "/allowed"},
		{id: "wrong_project", gender: "female", project: "other-" + project, generation: generation, authPath: "/allowed"},
		{id: "wrong_generation", gender: "male", project: project, generation: "old-" + generation, authPath: "/allowed"},
		{id: "denied", gender: "female", project: project, generation: generation, authPath: "/denied"},
	}
	patientsByID := make(map[string]patientSeed, len(patientSeeds))
	patientDocs := make([]map[string]any, 0, len(patientSeeds))
	for _, patient := range patientSeeds {
		patientsByID[patient.id] = patient
		patientDocs = append(patientDocs, map[string]any{
			"_key": key("patient_" + patient.id), "id": patient.id,
			"project": patient.project, "project_id": patient.project, "dataset_generation": patient.generation,
			"resourceType": "Patient", "auth_resource_path": patient.authPath,
			"gender":  patient.gender,
			"payload": map[string]any{"id": patient.id, "resourceType": "Patient", "gender": patient.gender},
		})
	}
	insert(patients, patientDocs...)

	// Each selected Observation is routed to a Patient. The fixture table is
	// the source oracle: only the first two rows satisfy every scope check and
	// survive the authored patient_id filter; both route to the same Patient.
	type memberSeed struct {
		id, patientID                         string
		sourceProject, sourceGeneration, auth string
		edgeProject, edgeGeneration, edgeAuth string
	}
	members := []memberSeed{
		{id: "obs_shared_a", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_shared_b", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_filtered", patientID: "filtered", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_source_project", patientID: "shared", sourceProject: "other-" + project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_source_generation", patientID: "shared", sourceProject: project, sourceGeneration: "old-" + generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_denied_source", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/denied", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_target_project", patientID: "wrong_project", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_target_generation", patientID: "wrong_generation", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_denied_target", patientID: "denied", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_edge_project", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: "other-" + project, edgeGeneration: generation, edgeAuth: "/allowed"},
		{id: "obs_wrong_edge_generation", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: "old-" + generation, edgeAuth: "/allowed"},
		{id: "obs_denied_edge", patientID: "shared", sourceProject: project, sourceGeneration: generation, auth: "/allowed", edgeProject: project, edgeGeneration: generation, edgeAuth: "/denied"},
	}
	observationDocs := make([]map[string]any, 0, len(members))
	edgeDocs := make([]map[string]any, 0, len(members))
	populationMemberDocs := make([]map[string]any, 0, len(members))
	for _, member := range members {
		observationDocs = append(observationDocs, map[string]any{
			"_key": key("observation_" + member.id), "id": member.id,
			"project": member.sourceProject, "project_id": member.sourceProject,
			"dataset_generation": member.sourceGeneration, "resourceType": "Observation",
			"auth_resource_path": member.auth,
			"payload":            map[string]any{"id": member.id, "resourceType": "Observation"},
		})
		edgeDocs = append(edgeDocs, map[string]any{
			"_key":    key("edge_" + member.id),
			"_from":   observations + "/" + key("observation_"+member.id),
			"_to":     patients + "/" + key("patient_"+member.patientID),
			"project": member.edgeProject, "project_id": member.edgeProject,
			"dataset_generation": member.edgeGeneration, "auth_resource_path": member.edgeAuth,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		populationMemberDocs = append(populationMemberDocs, map[string]any{
			"_key": key("population_member_" + member.id), "selectionId": key("population_selection"),
			"project": project, "generation": generation, "resourceType": "Observation", "id": member.id,
		})
	}
	insert(observations, observationDocs...)
	insert(edges, edgeDocs...)
	insert("loom_explorer_selection_members", populationMemberDocs...)

	populationSelectionID := key("population_selection")
	cohortSelectionID, cohortRevisionID := key("cohort_selection"), key("cohort_revision")
	scopeDigest, cohortMembershipDigest := key("scope_digest"), key("cohort_membership_digest")
	insert("loom_explorer_selections", map[string]any{
		"_key": populationSelectionID, "complete": true, "project": project, "generation": generation,
		"resourceType": "Observation", "scopeDigest": scopeDigest, "membershipDigest": key("population_membership_digest"),
	}, map[string]any{
		"_key": cohortSelectionID, "complete": true, "project": project, "generation": generation,
		"resourceType": "Patient", "scopeDigest": scopeDigest, "membershipDigest": cohortMembershipDigest,
	})
	insert("loom_explorer_explicit_group_revisions", map[string]any{
		"_key": cohortRevisionID, "state": "COMPLETE", "project": project, "generation": generation,
		"resourceType": "Patient", "sourceSelectionRevisionId": cohortSelectionID,
		"scopeDigest": scopeDigest, "sourceMembershipDigest": cohortMembershipDigest,
	})
	insert("loom_explorer_explicit_group_definitions",
		map[string]any{"_key": key("definition_routed"), "revisionId": cohortRevisionID, "project": project, "groupId": "routed", "label": "Routed", "ordinal": 0},
		map[string]any{"_key": key("definition_empty"), "revisionId": cohortRevisionID, "project": project, "groupId": "empty", "label": "Empty", "ordinal": 1},
	)
	cohortMembers := make([]map[string]any, 0, len(patientSeeds))
	groupMemberships := make([]map[string]any, 0, len(patientSeeds))
	for _, patient := range patientSeeds {
		cohortMembers = append(cohortMembers, map[string]any{
			"_key": key("cohort_member_" + patient.id), "selectionId": cohortSelectionID,
			"project": project, "generation": generation, "resourceType": "Patient", "id": patient.id,
		})
		groupMemberships = append(groupMemberships, map[string]any{
			"_key": key("group_membership_" + patient.id), "revisionId": cohortRevisionID,
			"project": project, "generation": generation, "resourceType": "Patient",
			"id": patient.id, "groupId": "routed",
			"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": patient.id},
		})
	}
	insert("loom_explorer_selection_members", cohortMembers...)
	insert("loom_explorer_explicit_group_memberships", groupMemberships...)

	excludedPatientID := "filtered"
	output := recipe.Output{
		Name: "RoutedCohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: populationSelectionID, MembershipDigest: key("population_membership_digest"),
			MemberCount: int64(len(members)), ResourceType: "Observation",
			Route: []recipe.PopulationRouteStep{{ResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}},
		},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "exclude_upstream_patient", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient_id", Operator: recipe.FilterNotEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &excludedPatientID}},
				}},
				Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: cohortRevisionID, UnassignedMemberPolicy: "EXCLUDE", AfterStepID: "exclude_upstream_patient",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "patient_id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		SelectionMembersCollection: "loom_explorer_selection_members",
		AuthScopeMode:              authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
		IncludeRowIdentity: true,
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-cohort-mapping",
		TranslationVersion: "population-routed-cohort-mapping", Outputs: []recipe.Output{output},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, key("scope"), generation)
	if err != nil {
		t.Fatal(err)
	}
	compiledRecipe, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	bindCollections := func(bindVars map[string]any) {
		bindVars["@root_collection"] = patients
		if _, exists := bindVars["@cohort_group_rows_resource_collection"]; exists {
			bindVars["@cohort_group_rows_resource_collection"] = patients
		}
		bindVars["@population_source_collection"] = observations
		bindVars["@population_members_collection"] = "loom_explorer_selection_members"
		bindVars["@population_root_route_0_edge_collection"] = edges
	}
	preview, err := CompileRecipeOutputWithPolicy(compiledRecipe.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed cohort preview: %v", err)
	}
	bindCollections(preview.BindVars)
	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	previewGroups := make(map[string]map[string]any, len(previewRows))
	for _, row := range previewRows {
		groupID, ok := row["group_id"].(string)
		if !ok {
			t.Fatalf("preview row has no group identity: %#v", row)
		}
		previewGroups[groupID] = row
	}
	emptyPreview, ok := previewGroups["empty"]
	if !ok || len(previewGroups) != 2 {
		t.Fatalf("routed cohort preview groups = %#v, want populated and declared empty groups", previewGroups)
	}
	routedPreview, ok := previewGroups["routed"]
	if !ok {
		t.Fatalf("routed cohort preview omitted the populated group: %#v", previewGroups)
	}
	routedMembers, ok := routedPreview["members"].([]any)
	if !ok || len(routedMembers) != 1 {
		t.Fatalf("routed cohort preview members = %#v, want its one shared Patient member", routedPreview["members"])
	}
	routedMember, ok := routedMembers[0].(map[string]any)
	if !ok {
		t.Fatalf("routed cohort preview member = %#v", routedMembers[0])
	}
	routedIdentity, ok := routedMember["source_identity"].(map[string]any)
	if !ok || routedIdentity["id"] != "shared" {
		t.Fatalf("routed cohort preview source identity = %#v, want Patient/shared", routedMember["source_identity"])
	}
	patientValues, ok := routedPreview["patient_id"].([]any)
	if !ok || !reflect.DeepEqual(patientValues, []any{"shared"}) {
		t.Fatalf("routed cohort preview patient_id values = %#v, want [shared]", routedPreview["patient_id"])
	}
	rootKeys, ok := routedPreview["__loom_root_contributor_keys"].([]any)
	if !ok || !reflect.DeepEqual(rootKeys, []any{key("patient_shared")}) {
		t.Fatalf("routed cohort preview contributor keys = %#v, want the seeded shared Patient storage key", routedPreview["__loom_root_contributor_keys"])
	}
	emptyMembers, ok := emptyPreview["members"].([]any)
	if !ok || len(emptyMembers) != 0 {
		t.Fatalf("empty cohort preview members = %#v, want empty list", emptyPreview["members"])
	}
	mapping, err := CompilePopulationMappingOutputWithPolicy(compiledRecipe.Outputs[0], bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	bindCollections(mapping.BindVars)
	rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: mapping.Query, BindVars: mapping.BindVars})

	// Derive the witness oracle from the fixture rows used above, without using
	// the compiler preview or mapping result to decide which members should map.
	wantMembers := make([]string, 0, len(members))
	for _, member := range members {
		patient := patientsByID[member.patientID]
		if member.sourceProject == project && member.sourceGeneration == generation && member.auth == "/allowed" &&
			member.edgeProject == project && member.edgeGeneration == generation && member.edgeAuth == "/allowed" &&
			patient.project == project && patient.generation == generation && patient.authPath == "/allowed" &&
			patient.id != excludedPatientID {
			wantMembers = append(wantMembers, member.id)
		}
	}
	sort.Strings(wantMembers)
	wantIdentity := map[string]any{"group_revision_id": cohortRevisionID, "group_id": "routed"}
	if len(rows) != len(wantMembers) {
		t.Logf("ordinary preview rows: %#v", previewRows)
		t.Logf("compiled population mapping query:\n%s", mapping.Query)
		t.Logf("compiled population mapping bind vars: %#v", mapping.BindVars)
		const scopedSourceJoin = `
FOR member IN @@members
  FILTER member.selectionId == @selection_id
    AND member.project == @project
    AND member.generation == @generation
    AND member.resourceType == @source_type
FOR source IN @@sources
  FILTER source.id == member.id
    AND source.project == @project
    AND source.project_id == @project
    AND source.dataset_generation == @generation
    AND source.resourceType == @source_type
    AND source.auth_resource_path IN @auth_paths
FOR edge IN @@edges
  FILTER edge._from == CONCAT(@source_collection, "/", source._key)
    AND edge.label == @relationship
    AND edge.from_type == @source_type
    AND edge.to_type == @root_type
    AND edge.project == @project
    AND edge.project_id == @project
    AND edge.dataset_generation == @generation
    AND edge.auth_resource_path IN @auth_paths
FOR target IN @@roots
  FILTER edge._to == CONCAT(@root_collection, "/", target._key)
    AND target.project == @project
    AND target.project_id == @project
    AND target.dataset_generation == @generation
    AND target.resourceType == @root_type
    AND target.auth_resource_path IN @auth_paths
  SORT member.id
  RETURN {
    member_id: member.id, source_key: source._key, source_id: source.id,
    edge_from: edge._from, edge_to: edge._to, target_key: target._key, target_id: target.id
  }`
		scopedRows := make([]map[string]any, 0)
		scopedBinds := map[string]any{
			"@members": "loom_explorer_selection_members", "@sources": observations,
			"@edges": edges, "@roots": patients,
			"selection_id": populationSelectionID, "project": project, "generation": generation,
			"source_type": "Observation", "root_type": "Patient", "relationship": "subject_Patient",
			"source_collection": observations, "root_collection": patients, "auth_paths": []string{"/allowed"},
		}
		if queryErr := client.QueryRows(ctx, scopedSourceJoin, 100, scopedBinds, func(row map[string]any) error {
			scopedRows = append(scopedRows, row)
			return nil
		}); queryErr != nil {
			t.Logf("independent scoped original source/root join query failed: %v\n%s\nbinds=%#v", queryErr, scopedSourceJoin, scopedBinds)
		} else {
			t.Logf("independent scoped original source/root join rows: %#v", scopedRows)
		}
		t.Fatalf("population mapping produced %d witnesses, want exactly two routed members; rows=%#v", len(rows), rows)
	}
	gotMembers := make([]string, 0, len(rows))
	for _, row := range rows {
		memberID, ok := row[mapping.MemberColumn].(string)
		if !ok {
			t.Fatalf("population mapping member has type %T: %#v", row[mapping.MemberColumn], row)
		}
		parts, ok := row[mapping.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("final identity parts = %#v, want one explicit cohort identity object", row[mapping.IdentityPartsColumn])
		}
		identity, ok := parts[0].(map[string]any)
		if !ok || !reflect.DeepEqual(identity, wantIdentity) {
			t.Fatalf("final identity object = %#v, want %#v", parts[0], wantIdentity)
		}
		gotMembers = append(gotMembers, memberID)
	}
	sort.Strings(gotMembers)
	if !reflect.DeepEqual(gotMembers, wantMembers) {
		t.Fatalf("mapped selection member IDs = %#v, want %#v", gotMembers, wantMembers)
	}
	if !reflect.DeepEqual(wantMembers, []string{"obs_shared_a", "obs_shared_b"}) {
		t.Fatalf("seed-derived witness oracle changed unexpectedly: %#v", wantMembers)
	}

	// Exercise original selected-member witnesses across GROUP→PIVOT→cohort.
	// The same fixture oracle must still map both selected Observations to the
	// exact final cohort identity after two contributor-set reductions.
	pivotOutput := routedPopulationGroupPivotCohortOutput(output.Population, cohortRevisionID, excludedPatientID)
	pivotPlan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-group-pivot-cohort",
		TranslationVersion: "population-routed-group-pivot-cohort", Outputs: []recipe.Output{pivotOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build routed GROUP→PIVOT cohort recipe: %v", err)
	}
	pivotResolved, err := semantic.ResolveRecipePlan(pivotPlan, key("scope"), generation)
	if err != nil {
		t.Fatalf("resolve routed GROUP→PIVOT cohort recipe: %v", err)
	}
	pivotCompiled, err := lower.CompileResolvedRecipePlan(pivotResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed GROUP→PIVOT cohort recipe: %v", err)
	}
	preCohortOutput := pivotOutput
	preCohortOutput.Name = "RoutedGroupPivotPreCohort"
	preCohortOutput.RowGrain = "patient"
	preCohortOutput.GroupRows = nil
	preCohortPlan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-group-pivot-pre-cohort",
		TranslationVersion: "population-routed-group-pivot-pre-cohort", Outputs: []recipe.Output{preCohortOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build pre-cohort GROUP→PIVOT recipe: %v", err)
	}
	preCohortResolved, err := semantic.ResolveRecipePlan(preCohortPlan, key("scope"), generation)
	if err != nil {
		t.Fatalf("resolve pre-cohort GROUP→PIVOT recipe: %v", err)
	}
	preCohortCompiled, err := lower.CompileResolvedRecipePlan(preCohortResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile pre-cohort GROUP→PIVOT recipe: %v", err)
	}
	preCohortPreview, err := CompileRecipeOutputWithPolicy(preCohortCompiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile pre-cohort GROUP→PIVOT preview: %v", err)
	}
	bindCollections(preCohortPreview.BindVars)
	preCohortRows := executeReshapeOracleQuery(t, ctx, client, preCohortPreview)
	if len(preCohortRows) != 1 || preCohortRows[0]["patient_id"] != "shared" {
		t.Fatalf("pre-cohort GROUP→PIVOT rows = %#v, want exactly Patient/shared; query:\n%s", preCohortRows, preCohortPreview.Query)
	}
	preCohortRow := preCohortRows[0]
	if got, ok := preCohortRow["male_count"].(float64); !ok || got != 1 {
		t.Fatalf("pre-cohort GROUP→PIVOT male count = %#v, want 1 distinct root Patient row; row=%#v query:\n%s", preCohortRow["male_count"], preCohortRow, preCohortPreview.Query)
	}
	if got := preCohortRow["female_count"]; got != nil {
		t.Fatalf("pre-cohort GROUP→PIVOT female count = %#v, want a null missing cell; query:\n%s", got, preCohortPreview.Query)
	}
	if got, ok := preCohortRow["__loom_root_contributor_keys"].([]any); !ok || !reflect.DeepEqual(got, []any{key("patient_shared")}) {
		t.Fatalf("pre-cohort GROUP→PIVOT root contributors = %#v, want the exact shared Patient storage key; query:\n%s", preCohortRow["__loom_root_contributor_keys"], preCohortPreview.Query)
	}

	pivotPreview, err := CompileRecipeOutputWithPolicy(pivotCompiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed GROUP→PIVOT cohort preview: %v", err)
	}
	bindCollections(pivotPreview.BindVars)
	pivotRows := executeReshapeOracleQuery(t, ctx, client, pivotPreview)
	pivotGroups := make(map[string]map[string]any, len(pivotRows))
	for _, row := range pivotRows {
		groupID, ok := row["group_id"].(string)
		if !ok {
			t.Fatalf("GROUP→PIVOT cohort preview row has no group identity: %#v", row)
		}
		pivotGroups[groupID] = row
	}
	routedPivotPreview := pivotGroups["routed"]
	if routedPivotPreview == nil || len(pivotGroups) != 2 {
		t.Fatalf("GROUP→PIVOT cohort preview groups = %#v, want routed and declared empty groups", pivotGroups)
	}
	pivotCohortMembers, ok := routedPivotPreview["members"].([]any)
	if !ok || len(pivotCohortMembers) != 1 {
		t.Fatalf("GROUP→PIVOT cohort members = %#v, want exactly Patient/shared", routedPivotPreview["members"])
	}
	pivotCohortMember, ok := pivotCohortMembers[0].(map[string]any)
	if !ok {
		t.Fatalf("GROUP→PIVOT cohort member = %#v, want a typed Patient member object", pivotCohortMembers[0])
	}
	wantCohortMemberIdentity := map[string]any{"project": project, "generation": generation, "resource_type": "Patient", "id": "shared"}
	if got := pivotCohortMember["source_identity"]; !reflect.DeepEqual(got, wantCohortMemberIdentity) {
		t.Fatalf("GROUP→PIVOT cohort member identity = %#v, want %#v", got, wantCohortMemberIdentity)
	}
	if got, ok := routedPivotPreview["patient_id"].([]any); !ok || !reflect.DeepEqual(got, []any{"shared"}) {
		t.Fatalf("GROUP→PIVOT cohort patient IDs = %#v, want [shared]", routedPivotPreview["patient_id"])
	}
	wantFinalIdentity := map[string]any{"group_revision_id": cohortRevisionID, "group_id": "routed"}
	if got := routedPivotPreview["__loom_row_id"]; !reflect.DeepEqual(got, wantFinalIdentity) {
		t.Fatalf("GROUP→PIVOT cohort row identity = %#v, want %#v", got, wantFinalIdentity)
	}
	if got, ok := routedPivotPreview["__loom_root_contributor_keys"].([]any); !ok || !reflect.DeepEqual(got, []any{key("patient_shared")}) {
		t.Fatalf("GROUP→PIVOT cohort root contributors = %#v, want the exact shared Patient storage key", routedPivotPreview["__loom_root_contributor_keys"])
	}

	pivotMapping, err := CompilePopulationMappingOutputWithPolicy(pivotCompiled.Outputs[0], bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed GROUP→PIVOT population mapping: %v", err)
	}
	bindCollections(pivotMapping.BindVars)
	pivotMappingRows := make([]map[string]any, 0, len(wantMembers))
	if err := client.QueryRows(ctx, pivotMapping.Query, 100, pivotMapping.BindVars, func(row map[string]any) error {
		pivotMappingRows = append(pivotMappingRows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute routed GROUP→PIVOT population mapping query:\n%s\n%v", pivotMapping.Query, err)
	}
	pivotMappedMembers := make([]string, 0, len(pivotMappingRows))
	for _, row := range pivotMappingRows {
		memberID, ok := row[pivotMapping.MemberColumn].(string)
		if !ok {
			t.Fatalf("GROUP→PIVOT mapped member has type %T: %#v", row[pivotMapping.MemberColumn], row)
		}
		parts, ok := row[pivotMapping.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("GROUP→PIVOT final identity parts = %#v, want one exact final cohort identity", row[pivotMapping.IdentityPartsColumn])
		}
		identity, ok := parts[0].(map[string]any)
		if !ok || !reflect.DeepEqual(identity, wantFinalIdentity) {
			t.Fatalf("GROUP→PIVOT final identity = %#v, want %#v", parts[0], wantFinalIdentity)
		}
		pivotMappedMembers = append(pivotMappedMembers, memberID)
	}
	sort.Strings(pivotMappedMembers)
	if !reflect.DeepEqual(pivotMappedMembers, wantMembers) {
		t.Fatalf("GROUP→PIVOT mapped original selection IDs = %#v, want raw-seed oracle %#v", pivotMappedMembers, wantMembers)
	}
}

func routedPopulationGroupPivotCohortOutput(population *recipe.PopulationConstraint, cohortRevisionID, excludedPatientID string) recipe.Output {
	male, female := "male", "female"
	return recipe.Output{
		Name: "RoutedGroupPivotCohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
		Population: population,
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}, {ID: "gender", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "patient_gender_group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "patient_gender_group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys: []recipe.ConstructionGroupKey{
							{InputColumnID: "patient_id", OutputColumnID: "patient_id"},
							{InputColumnID: "gender", OutputColumnID: "gender"},
						},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_id", Name: "patient_id", Type: "string"},
						{ID: "gender", Name: "gender", Type: "string"},
						{ID: "records", Name: "records", Type: "integer"},
					},
				},
				{
					ID: "patient_gender_pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "patient_gender_group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "patient_gender_pivot", GroupKeyIDs: []string{"patient_id"},
						CategoryColumnID: "gender", ValueColumnID: "records",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &male}, OutputColumnID: "male_count"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &female}, OutputColumnID: "female_count"},
						},
						DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_id", Name: "patient_id", Type: "string"},
						{ID: "male_count", Name: "male_count", Type: "integer"},
						{ID: "female_count", Name: "female_count", Type: "integer"},
					},
				},
				{
					ID: "exclude_filtered_patient", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "patient_gender_pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "patient_id", Operator: recipe.FilterNotEquals,
						Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &excludedPatientID}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_id", Name: "patient_id", Type: "string"},
						{ID: "male_count", Name: "male_count", Type: "integer"},
						{ID: "female_count", Name: "female_count", Type: "integer"},
					},
				},
			},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: cohortRevisionID, UnassignedMemberPolicy: "EXCLUDE", AfterStepID: "exclude_filtered_patient",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "patient_id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
}

func TestPopulationMappingRoutedGroupPivotCohortCompilesFinalIdentity(t *testing.T) {
	output := routedPopulationGroupPivotCohortOutput(&recipe.PopulationConstraint{
		SelectionRevisionID: "population_selection", MembershipDigest: "sha256:population_membership", MemberCount: 2,
		ResourceType: "Observation",
		Route:        []recipe.PopulationRouteStep{{ResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}},
	}, "cohort_revision", "filtered")
	bindings := recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation",
		SelectionMembersCollection: "loom_explorer_selection_members", AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-group-pivot-cohort",
		TranslationVersion: "population-group-pivot-cohort", Outputs: []recipe.Output{output},
	}, bindings)
	if err != nil {
		t.Fatalf("build routed GROUP→PIVOT cohort: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatalf("resolve routed GROUP→PIVOT cohort: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed GROUP→PIVOT cohort: %v", err)
	}
	outputPlan := compiled.Outputs[0]
	sequence := outputPlan.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 4 {
		t.Fatalf("routed GROUP→PIVOT cohort stages = %#v, want GROUP, PIVOT, FILTER, COHORT_GROUP", sequence)
	}
	group, pivot, cohort := sequence.Stages[0], sequence.Stages[1], sequence.Stages[3]
	if group.Group == nil || group.Group.RootContributorInputColumn != "_key" || group.Group.RootContributorOutputColumn != "__loom_root_contributor_keys" {
		t.Fatalf("GROUP did not retain root storage keys: %#v", group.Group)
	}
	if pivot.GroupedPivot == nil || pivot.GroupedPivot.RootContributorInputColumn != "__loom_root_contributor_keys" || pivot.GroupedPivot.RootContributorOutputColumn != "__loom_root_contributor_keys" {
		t.Fatalf("PIVOT did not retain grouped root storage keys: %#v", pivot.GroupedPivot)
	}
	if cohort.CohortGroup == nil || cohort.CohortGroup.ContributorInputColumn != "__loom_root_contributor_keys" {
		t.Fatalf("cohort did not consume final root storage keys: %#v", cohort.CohortGroup)
	}
	if outputPlan.RowIdentity == nil || outputPlan.RowIdentity.Grain != "groups" ||
		!reflect.DeepEqual(outputPlan.RowIdentity.Fields, []string{sequence.FinalRowIdentity}) {
		t.Fatalf("cohort semantic identity = %#v, want the final typed group identity object", outputPlan.RowIdentity)
	}
	identityColumnFound, groupRevisionFound, groupIDFound := false, false, false
	for _, column := range sequence.FinalColumns {
		switch column.Name {
		case sequence.FinalRowIdentity:
			identityColumnFound = column.Internal && column.Identity && column.Kind == "object" && column.Cardinality == "required_one"
		case "group_revision_id":
			groupRevisionFound = column.Identity && column.Kind == "string" && column.Cardinality == "required_one"
		case "group_id":
			groupIDFound = column.Identity && column.Kind == "string" && column.Cardinality == "required_one"
		}
	}
	if !identityColumnFound || !groupRevisionFound || !groupIDFound {
		t.Fatalf("cohort final identity is not a required object over required string group identity components: %#v", sequence.FinalColumns)
	}
	mapping, err := CompilePopulationMappingOutputWithPolicy(outputPlan, bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed GROUP→PIVOT population mapping: %v", err)
	}
	if mapping.IdentityPartsColumn == "" || mapping.RowIdentity == nil || len(mapping.RowIdentity.Fields) != 1 || mapping.RowIdentity.Fields[0] != sequence.FinalRowIdentity {
		t.Fatalf("population mapping identity = %#v, parts column %q; want exact final typed row identity %q", mapping.RowIdentity, mapping.IdentityPartsColumn, sequence.FinalRowIdentity)
	}
	invalidIdentityShapes := []struct {
		name   string
		mutate func(*ir.PhysicalStageSequence)
	}{
		{
			name: "cohort row identity must stay an object",
			mutate: func(sequence *ir.PhysicalStageSequence) {
				mutate := func(columns []ir.PhysicalStageColumn) {
					for index := range columns {
						if columns[index].Name == sequence.FinalRowIdentity {
							columns[index].Kind = "string"
						}
					}
				}
				mutate(sequence.FinalColumns)
				mutate(sequence.Stages[len(sequence.Stages)-1].OutputColumns)
			},
		},
		{
			name: "cohort tuple components must stay required strings",
			mutate: func(sequence *ir.PhysicalStageSequence) {
				for index := range sequence.Stages[len(sequence.Stages)-1].OutputColumns {
					if sequence.Stages[len(sequence.Stages)-1].OutputColumns[index].Name == "group_id" {
						sequence.Stages[len(sequence.Stages)-1].OutputColumns[index].Kind = "object"
					}
				}
				for index := range sequence.FinalColumns {
					if sequence.FinalColumns[index].Name == "group_id" {
						sequence.FinalColumns[index].Kind = "object"
					}
				}
			},
		},
		{
			name: "object identity needs cohort lineage",
			mutate: func(sequence *ir.PhysicalStageSequence) {
				last := &sequence.Stages[len(sequence.Stages)-1]
				last.Kind = ir.PhysicalStageFilterOp
				last.CohortGroup = nil
			},
		},
	}
	for _, test := range invalidIdentityShapes {
		t.Run(test.name, func(t *testing.T) {
			candidate := outputPlan
			candidate.Plan = clonePhysicalPlan(outputPlan.Plan)
			candidate.OptimizedPlan = nil
			test.mutate(candidate.Plan.StageSequence)
			if _, _, err := finalConstructionPopulationMappingIdentity(candidate.Plan, candidate); err == nil {
				t.Fatalf("final construction identity accepted forged cohort shape: %#v", candidate.Plan.StageSequence)
			}
			if _, err := CompilePopulationMappingOutputWithPolicy(candidate, bindings, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
				t.Fatalf("population mapping accepted forged cohort identity shape: %#v", candidate.Plan.StageSequence)
			}
		})
	}

	preCohortOutput := output
	preCohortOutput.Name = "RoutedGroupPivotPreCohort"
	preCohortOutput.RowGrain = "patient"
	preCohortOutput.GroupRows = nil
	preCohortPlan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-group-pivot-pre-cohort",
		TranslationVersion: "population-group-pivot-pre-cohort", Outputs: []recipe.Output{preCohortOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build pre-cohort GROUP→PIVOT: %v", err)
	}
	preCohortResolved, err := semantic.ResolveRecipePlan(preCohortPlan, "scope", "generation")
	if err != nil {
		t.Fatalf("resolve pre-cohort GROUP→PIVOT: %v", err)
	}
	preCohortCompiled, err := lower.CompileResolvedRecipePlan(preCohortResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile pre-cohort GROUP→PIVOT: %v", err)
	}
	preCohortPreview, err := CompileRecipeOutputWithPolicy(preCohortCompiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("render pre-cohort GROUP→PIVOT preview: %v", err)
	}
	if strings.TrimSpace(preCohortPreview.Query) == "" {
		t.Fatal("pre-cohort GROUP→PIVOT preview query is empty")
	}
}
