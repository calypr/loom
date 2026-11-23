package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"github.com/calypr/loom/internal/dataframe/compiler/population"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/semantic"
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
	plan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-cohort-mapping",
		TranslationVersion: "population-routed-cohort-mapping", Outputs: []recipe.Output{output},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, key("scope"), generation)
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
	mapping, err := population.CompilePopulationMappingOutputWithPolicy(compiledRecipe.Outputs[0], bindings, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	bindCollections(mapping.BindVars)
	rows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: mapping.Query, BindVars: mapping.BindVars})

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

	pivotOutput := routedPopulationGroupPivotCohortOutput(output.Population, cohortRevisionID, excludedPatientID)
	pivotPlan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-group-pivot-cohort",
		TranslationVersion: "population-routed-group-pivot-cohort", Outputs: []recipe.Output{pivotOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build routed GROUP→PIVOT cohort recipe: %v", err)
	}
	pivotResolved, err := recipeplan.ResolveRecipePlan(pivotPlan, key("scope"), generation)
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
	preCohortPlan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-routed-group-pivot-pre-cohort",
		TranslationVersion: "population-routed-group-pivot-pre-cohort", Outputs: []recipe.Output{preCohortOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build pre-cohort GROUP→PIVOT recipe: %v", err)
	}
	preCohortResolved, err := recipeplan.ResolveRecipePlan(preCohortPlan, key("scope"), generation)
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

	pivotMapping, err := population.CompilePopulationMappingOutputWithPolicy(pivotCompiled.Outputs[0], bindings, ir.DefaultPhysicalOptimizationPolicy())
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
	plan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-group-pivot-cohort",
		TranslationVersion: "population-group-pivot-cohort", Outputs: []recipe.Output{output},
	}, bindings)
	if err != nil {
		t.Fatalf("build routed GROUP→PIVOT cohort: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope", "generation")
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
	mapping, err := population.CompilePopulationMappingOutputWithPolicy(outputPlan, bindings, ir.DefaultPhysicalOptimizationPolicy())
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
			candidate.Plan = ir.ClonePhysicalPlan(outputPlan.Plan)
			candidate.OptimizedPlan = nil
			test.mutate(candidate.Plan.StageSequence)
			if _, _, err := population.FinalConstructionPopulationMappingIdentity(candidate.Plan, candidate); err == nil {
				t.Fatalf("final construction identity accepted forged cohort shape: %#v", candidate.Plan.StageSequence)
			}
			if _, err := population.CompilePopulationMappingOutputWithPolicy(candidate, bindings, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
				t.Fatalf("population mapping accepted forged cohort identity shape: %#v", candidate.Plan.StageSequence)
			}
		})
	}

	preCohortOutput := output
	preCohortOutput.Name = "RoutedGroupPivotPreCohort"
	preCohortOutput.RowGrain = "patient"
	preCohortOutput.GroupRows = nil
	preCohortPlan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population-group-pivot-pre-cohort",
		TranslationVersion: "population-group-pivot-pre-cohort", Outputs: []recipe.Output{preCohortOutput},
	}, bindings)
	if err != nil {
		t.Fatalf("build pre-cohort GROUP→PIVOT: %v", err)
	}
	preCohortResolved, err := recipeplan.ResolveRecipePlan(preCohortPlan, "scope", "generation")
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
	compilePage := func(rowID string, offset int) rowlineage.CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := rowlineage.CompileRowLineageOutput(compiled, rowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
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
	identityResult := executeRowLineageOracleQuery(t, ctx, client, identityProbe)
	selectedGroup, _ := identityResult["selectedGroup"].(map[string]any)
	lineageIdentity, _ := selectedGroup["identity"].(string)
	if lineageIdentity != sharedCodeRowID {
		t.Fatalf("executed semantic Group identity = %q, want actual grouped row ID %q; probe=%#v", lineageIdentity, sharedCodeRowID, identityResult)
	}
	lineage := executeRowLineageOracleQuery(t, ctx, client, lineageQuery)
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
