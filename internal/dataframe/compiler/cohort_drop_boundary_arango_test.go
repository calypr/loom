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

// This fixture proves that a pinned member missing from the current resource
// collection cannot pass a row-dropping construction stage before COHORT_GROUP.
func TestPreCohortDropStagesExcludeMissingPinnedMembersAgainstArango(t *testing.T) {
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

	project, generation := "loom_cohort_drop_"+uuid.NewString(), "cohort-drop-generation-"+uuid.NewString()
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
				t.Errorf("remove owned cohort-drop fixtures from %s: %v", collection, err)
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
			t.Fatalf("insert %s cohort-drop fixtures: %v", collection, err)
		}
	}

	createPinnedGroup := func(caseName string, ids []string) string {
		t.Helper()
		revision, selection := project+"_"+caseName+"_revision", project+"_"+caseName+"_selection"
		digest := "membership-" + caseName
		insert("loom_explorer_explicit_group_revisions", map[string]any{
			"_key": revision, "state": "COMPLETE", "project": project, "generation": generation,
			"resourceType": "Patient", "sourceSelectionRevisionId": selection,
			"scopeDigest": "scope-" + caseName, "sourceMembershipDigest": digest,
		})
		insert("loom_explorer_selections", map[string]any{
			"_key": selection, "complete": true, "project": project, "generation": generation,
			"resourceType": "Patient", "scopeDigest": "scope-" + caseName, "membershipDigest": digest,
		})
		insert("loom_explorer_explicit_group_definitions", map[string]any{
			"_key": project + "_" + caseName + "_definition", "revisionId": revision,
			"project": project, "groupId": "test", "label": "Test", "ordinal": 0,
		})
		selectionMembers := make([]map[string]any, 0, len(ids))
		memberships := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			selectionMembers = append(selectionMembers, map[string]any{
				"_key": project + "_" + caseName + "_selection_" + id, "selectionId": selection,
				"project": project, "generation": generation, "resourceType": "Patient", "id": id,
			})
			memberships = append(memberships, map[string]any{
				"_key": project + "_" + caseName + "_membership_" + id, "revisionId": revision,
				"project": project, "generation": generation, "resourceType": "Patient", "id": id, "groupId": "test",
				"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Patient", "id": id},
			})
		}
		insert("loom_explorer_selection_members", selectionMembers...)
		insert("loom_explorer_explicit_group_memberships", memberships...)
		return revision
	}

	patient := func(id string, active any) map[string]any {
		return map[string]any{
			"_key": project + "_patient_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient", "auth_resource_path": "/allowed",
			"payload": map[string]any{"id": id, "resourceType": "Patient", "active": active},
		}
	}
	insert("Patient",
		patient("eligibility_pass", true), patient("eligibility_fail", true),
		patient("expand_hit", true), patient("expand_empty", true),
		patient("unpivot_value", true), patient("unpivot_null", nil),
	)
	insert("Observation",
		map[string]any{"_key": project + "_observation_allowed", "id": "allowed", "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": map[string]any{"id": "allowed", "resourceType": "Observation"}},
		map[string]any{"_key": project + "_observation_denied", "id": "denied", "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/denied", "payload": map[string]any{"id": "denied", "resourceType": "Observation"}},
	)
	insert("fhir_edge",
		relatedCountEdge(project, generation, "eligibility_allowed", "Observation/"+project+"_observation_allowed", "Patient/"+project+"_patient_eligibility_pass", "subject_Patient", "Observation", "Patient", "/allowed"),
		relatedCountEdge(project, generation, "eligibility_denied", "Observation/"+project+"_observation_denied", "Patient/"+project+"_patient_eligibility_fail", "subject_Patient", "Observation", "Patient", "/allowed"),
		relatedCountEdge(project, generation, "expand_allowed", "Observation/"+project+"_observation_allowed", "Patient/"+project+"_patient_expand_hit", "subject_Patient", "Observation", "Patient", "/allowed"),
	)

	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}, IncludeRowIdentity: true,
	}
	rootFields := []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}
	rootColumns := []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}
	route := []recipe.ConstructionRelatedRouteStep{{
		EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}

	eligibilityRevision := createPinnedGroup("eligibility", []string{"eligibility_pass", "eligibility_fail", "eligibility_missing"})
	expandRevision := createPinnedGroup("expand", []string{"expand_hit", "expand_empty", "expand_missing"})
	unpivotRevision := createPinnedGroup("unpivot", []string{"unpivot_value", "unpivot_null", "unpivot_missing"})

	eligibility := recipe.ConstructionStep{
		ID: "eligible", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
			AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: route, ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
		}}, Outputs: rootColumns,
	}
	expand := recipe.ConstructionStep{
		ID: "expanded", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: route, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "observation_id",
		}},
		Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}, {ID: "observation_id", Name: "observation_id", Type: "string"}},
	}
	unpivotFields := append(append([]recipe.Field(nil), rootFields...), recipe.Field{Name: "active", ColumnID: "active", Expr: recipe.Expression{Select: "root.active"}})
	unpivotColumns := append(append([]recipe.StageColumn(nil), rootColumns...), recipe.StageColumn{ID: "active", Name: "active", Type: "boolean", Nullable: true})
	activeKey := "active"
	unpivot := recipe.ConstructionStep{
		ID: "unpivoted", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
			ConstructionID: "unpivot_boundary", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "active", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &activeKey}}},
			KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id", NullRowPolicy: recipe.UnpivotNullDrop,
		}},
		Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}, {ID: "measure_id", Name: "measure", Type: "string"}, {ID: "value_id", Name: "value", Type: "boolean"}},
	}

	assertRows := func(name, revision string, fields []recipe.Field, columns []recipe.StageColumn, step recipe.ConstructionStep, want []string) {
		t.Helper()
		output := recipe.Output{
			Name: name, RootResourceType: "Patient", RowGrain: "groups", Fields: fields,
			GroupRows:    &recipe.GroupRows{RevisionID: revision, UnassignedMemberPolicy: "EXCLUDE", AfterStepID: step.ID},
			Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{step}},
		}
		compiled := lowerConstructionOutput(t, output, bindings)
		var cohort *ir.PhysicalStageCohortGroup
		for _, stage := range compiled.Plan.StageSequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				cohort = stage.CohortGroup
				break
			}
		}
		if cohort == nil || cohort.PreserveMissingMembers {
			t.Fatalf("%s PreserveMissingMembers = %#v, want false", name, cohort)
		}
		query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile %s cohort query: %v", name, err)
		}
		rows := executeReshapeOracleQuery(t, ctx, client, query)
		if len(rows) != 1 || rows[0]["group_id"] != "test" {
			t.Fatalf("%s cohort rows = %#v, want one test group", name, rows)
		}
		members, ok := rows[0]["members"].([]any)
		if !ok {
			t.Fatalf("%s members have type %T: %#v", name, rows[0]["members"], rows[0]["members"])
		}
		got := make([]string, 0, len(members))
		for _, raw := range members {
			member, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s member has type %T: %#v", name, raw, raw)
			}
			identity, ok := member["source_identity"].(map[string]any)
			if !ok {
				t.Fatalf("%s member source identity has type %T: %#v", name, member["source_identity"], member)
			}
			got = append(got, identity["id"].(string))
			if member["payload"] == nil {
				t.Fatalf("%s unexpectedly retained a missing member: %#v", name, member)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s member IDs = %#v, want %#v", name, got, want)
		}
	}

	assertRows("pre_eligibility", eligibilityRevision, rootFields, rootColumns, eligibility, []string{"eligibility_pass"})
	assertRows("pre_related_expand_exclude", expandRevision, rootFields, rootColumns, expand, []string{"expand_hit"})
	assertRows("pre_unpivot_drop", unpivotRevision, unpivotFields, unpivotColumns, unpivot, []string{"unpivot_value"})
}
