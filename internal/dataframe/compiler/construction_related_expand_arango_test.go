package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRelatedSourceProjectionLivenessForKeylessCount(t *testing.T) {
	for _, test := range []struct {
		name        string
		groupByFlag bool
		wantMarker  bool
	}{
		{name: "unused before related expansion", wantMarker: false},
		{name: "retained when grouped", groupByFlag: true, wantMarker: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := compileRelatedExpandCountQuery(t, relatedExpandCountOutput(test.groupByFlag))
			for _, marker := range []string{"unused_count_column_marker", "unused_list_column_marker", "unused_presence_column_marker"} {
				gotMarker := strings.Contains(query.Query, marker)
				if gotMarker != test.wantMarker {
					t.Errorf("related source projection %q included = %t, want %t:\n%s", marker, gotMarker, test.wantMarker, query.Query)
				}
			}
			if !strings.Contains(query.Query, "related_expand_records") || !strings.Contains(query.Query, "WITH COUNT INTO") {
				t.Fatalf("query must retain related expansion and row counting:\n%s", query.Query)
			}
		})
	}
}

func TestRelatedExpandDistinctTerminalRowsAndEmptyPoliciesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_related_expand_"+uuid.NewString(), "generation-related-expand"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-expansion fixtures from %s: %v", collection, err)
			}
		}
	}()

	document := func(key, resourceType, docGeneration string, payload map[string]any) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key, "id": payload["id"], "project": project, "project_id": project,
			"dataset_generation": docGeneration, "resourceType": resourceType, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	patients := []json.RawMessage{
		document(project+"_p1", "Patient", generation, map[string]any{"id": "p1", "resourceType": "Patient", "active": true}),
		document(project+"_p2", "Patient", generation, map[string]any{"id": "p2", "resourceType": "Patient", "active": true}),
		document(project+"_p3", "Patient", generation, map[string]any{"id": "p3", "resourceType": "Patient", "active": false}),
		document(project+"_p4", "Patient", generation, map[string]any{"id": "p4", "resourceType": "Patient", "active": false}),
	}
	observations := []json.RawMessage{
		document(project+"_o1", "Observation", generation, map[string]any{"id": "o1", "resourceType": "Observation", "status": "registered"}),
		document(project+"_o2", "Observation", generation, map[string]any{"id": "o2", "resourceType": "Observation", "status": "final"}),
	}
	for collection, documents := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert related-expansion fixtures into %s: %v", collection, err)
		}
	}
	edges := make([]json.RawMessage, 0, 3)
	for index, observationID := range []string{"o1", "o1", "o2"} {
		encoded, err := json.Marshal(map[string]any{
			"_key":    fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from":   "Observation/" + project + "_" + observationID,
			"_to":     "Patient/" + project + "_p1",
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	encoded, err := json.Marshal(map[string]any{
		"_key": project + "_edge_p4", "_from": "Observation/" + project + "_o2", "_to": "Patient/" + project + "_p4",
		"project": project, "project_id": project, "dataset_generation": generation,
		"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	edges = append(edges, encoded)
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert related-expansion edges: %v", err)
	}

	t.Run("row lineage source contributors", func(t *testing.T) {
		bindings := recipe.RuntimeBindings{
			Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted,
		}
		directOutput := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionPreserveParent), bindings)
		previewQuery, err := CompileRecipeOutputWithPolicy(directOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		rows := executeReshapeOracleQuery(t, ctx, client, previewQuery)
		byParent := make(map[string][]map[string]any)
		for _, row := range rows {
			parent, _ := row["patient_id"].(string)
			byParent[parent] = append(byParent[parent], row)
		}
		if len(byParent["p1"]) != 2 || len(byParent["p2"]) != 1 || len(byParent["p3"]) != 1 || len(byParent["p4"]) != 1 {
			t.Fatalf("direct related rows by parent = %#v, want many/zero/zero/one matches", byParent)
		}
		for parent, parentRows := range byParent {
			for _, row := range parentRows {
				rowID, _ := row["__loom_row_id"].(string)
				compiled, err := CompileRowLineageOutput(directOutput, rowID, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Fatalf("compile row lineage for %s: %v", parent, err)
				}
				result := executeRowLineageOracleQuery(t, ctx, client, compiled)
				contributors := result["contributors"].([]any)
				want := 2
				if parent == "p2" || parent == "p3" {
					want = 1
				}
				if result["found"] != true || len(contributors) != want {
					t.Fatalf("%s row lineage = %#v, want %d source contributors", parent, result, want)
				}
				root := contributors[0].(map[string]any)
				if root["resourceType"] != "Patient" || root["resourceId"] != parent || root["occurrenceKey"] != project+"_"+parent {
					t.Errorf("%s root contributor = %#v", parent, root)
				}
				if want == 2 {
					target := contributors[1].(map[string]any)
					if target["resourceType"] != "Observation" || target["resourceId"] != row["observation_id"] {
						t.Errorf("%s terminal contributor = %#v, row target %#v", parent, target, row["observation_id"])
					}
				}
			}
		}

		stage := directOutput.Plan.StageSequence.Stages[0]
		constructionID := directOutput.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
		forgedRowID := relatedExpandRowID(t, project+"_p1", constructionID, "Observation/"+project+"_forged")
		forged, err := CompileRowLineageOutput(directOutput, forgedRowID, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		forgedResult := executeRowLineageOracleQuery(t, ctx, client, forged)
		if forgedResult["found"] != false || len(forgedResult["contributors"].([]any)) != 0 {
			t.Fatalf("forged related row identity disclosed contributors: %#v", forgedResult)
		}
	})

	for _, policy := range []recipe.ExpansionEmptyPolicy{recipe.ExpansionExclude, recipe.ExpansionPreserveParent} {
		t.Run(string(policy), func(t *testing.T) {
			compiled, query := compileRelatedExpandOracleQuery(t, relatedExpandOracleOutput(policy), project, generation)
			started := time.Now()
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			t.Logf("RELATED_EXPAND %s query returned %d rows in %s", policy, len(rows), time.Since(started))
			ids := make(map[string]bool, len(rows))
			byParent := make(map[string][]string)
			for _, row := range rows {
				parent, _ := row["patient_id"].(string)
				rowID, _ := row["__loom_row_id"].(string)
				if rowID == "" || ids[rowID] {
					t.Errorf("related row identity is missing or duplicated: %#v", row["__loom_row_id"])
				}
				ids[rowID] = true
				if row["observation_id"] == nil {
					byParent[parent] = append(byParent[parent], "<empty>")
				} else {
					byParent[parent] = append(byParent[parent], fmt.Sprint(row["observation_id"]))
				}
			}
			if policy == recipe.ExpansionExclude {
				if len(rows) != 2 || len(byParent["p1"]) != 2 || len(byParent["p2"]) != 0 || len(byParent["p3"]) != 0 {
					t.Fatalf("EXCLUDE rows = %#v, want two unique p1 terminals after the active filter", rows)
				}
				if byParent["p1"][0] == byParent["p1"][1] || !(byParent["p1"][0] == "o1" || byParent["p1"][1] == "o1") || !(byParent["p1"][0] == "o2" || byParent["p1"][1] == "o2") {
					t.Fatalf("EXCLUDE related IDs = %#v, want distinct FHIR IDs o1 and o2 despite duplicate paths", byParent["p1"])
				}
			} else if len(rows) != 3 || len(byParent["p1"]) != 2 || len(byParent["p2"]) != 1 || byParent["p2"][0] != "<empty>" || len(byParent["p3"]) != 0 {
				t.Fatalf("PRESERVE_PARENT rows = %#v, want two p1 terminals and a distinct null p2 row after filtering", rows)
			}
			if len(compiled.Stages) < 3 || compiled.Stages[1].ID != "filter_active" || compiled.Stages[2].RelatedExpand == nil {
				t.Fatalf("related expansion was not compiled from the selected filtered stage: %#v", compiled.Stages)
			}
			repeated := executeReshapeOracleQuery(t, ctx, client, query)
			if !sameConstructionRowIdentities(rows, repeated) {
				t.Fatalf("repeated related-expansion row identities = %#v, want %#v", constructionRowIdentities(repeated), constructionRowIdentities(rows))
			}
		})
	}

	t.Run(string(recipe.ExpansionError), func(t *testing.T) {
		_, query := compileRelatedExpandOracleQuery(t, relatedExpandOracleOutput(recipe.ExpansionError), project, generation)
		started := time.Now()
		err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
		t.Logf("RELATED_EXPAND %s query rejected an empty route in %s", recipe.ExpansionError, time.Since(started))
		if err == nil {
			t.Fatalf("ERROR policy returned rows despite active p2 having no related resource")
		}
		t.Logf("ERROR policy result: %v", err)
	})
}

func TestGroupedRelatedExpandUsesDistinctContributorUnionAndOnwardIdentityAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "Specimen"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_group_expand_"+uuid.NewString(), "generation-group-expand"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "Specimen", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER STARTS_WITH(document._key, @prefix) REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"prefix": project + "_"}); err != nil {
				t.Errorf("remove grouped related-expansion fixtures from %s: %v", collection, err)
			}
		}
	}()

	key := func(id string) string { return project + "_" + id }
	insertResources := func(collection string, docs ...map[string]any) {
		t.Helper()
		encoded := make([]json.RawMessage, 0, len(docs))
		for _, doc := range docs {
			resourceType := doc["resourceType"].(string)
			payload := doc["payload"].(map[string]any)
			documentGeneration, _ := doc["dataset_generation"].(string)
			encodedDoc, err := json.Marshal(map[string]any{
				"_key": doc["_key"], "id": payload["id"], "project": doc["project"], "project_id": doc["project"],
				"dataset_generation": documentGeneration, "resourceType": resourceType,
				"auth_resource_path": doc["auth_resource_path"], "payload": payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, encodedDoc)
		}
		if err := client.InsertBatchRaw(ctx, collection, encoded, false, "document"); err != nil {
			t.Fatalf("insert grouped related-expansion fixtures into %s: %v", collection, err)
		}
	}
	resource := func(id, resourceType string, payload map[string]any) map[string]any {
		payload["id"], payload["resourceType"] = id, resourceType
		return map[string]any{"_key": key(id), "project": project, "dataset_generation": generation,
			"auth_resource_path": "/allowed", "resourceType": resourceType, "payload": payload}
	}
	wrongProjectObservation := resource("wrong-project-observation", "Observation", map[string]any{"status": "final"})
	wrongProjectObservation["project"] = project + "_other"
	wrongGenerationObservation := resource("wrong-generation-observation", "Observation", map[string]any{"status": "final"})
	wrongGenerationObservation["dataset_generation"] = "old-generation"
	deniedRoot := resource("p_denied", "Patient", map[string]any{"gender": "male"})
	deniedRoot["auth_resource_path"] = "/forbidden"
	deniedObservation := resource("o_denied", "Observation", map[string]any{"status": "denied-target"})
	deniedObservation["auth_resource_path"] = "/forbidden"
	rootOnlyObservation := resource("o_root_only", "Observation", map[string]any{"status": "root-denied"})
	edgeOnlyObservation := resource("o_edge_only", "Observation", map[string]any{"status": "edge-denied"})
	deniedSpecimen := resource("s_denied", "Specimen", map[string]any{"status": "denied-target"})
	deniedSpecimen["auth_resource_path"] = "/forbidden"
	wrongGenerationPatient := resource("p_old", "Patient", map[string]any{"gender": "male"})
	wrongGenerationPatient["dataset_generation"] = "old-generation"
	insertResources("Patient",
		resource("p1", "Patient", map[string]any{"gender": "male"}),
		resource("p2", "Patient", map[string]any{"gender": "male"}),
		resource("p3", "Patient", map[string]any{"gender": "female"}),
		deniedRoot,
		wrongGenerationPatient,
	)
	insertResources("Observation",
		resource("o1", "Observation", map[string]any{"status": "registered"}),
		resource("o2", "Observation", map[string]any{"status": "final"}),
		wrongProjectObservation, wrongGenerationObservation,
		deniedObservation, rootOnlyObservation, edgeOnlyObservation,
	)
	insertResources("Specimen", resource("s1", "Specimen", map[string]any{"status": "available"}), deniedSpecimen)
	edges := make([]json.RawMessage, 0, 8)
	addEdge := func(index int, fromType, fromID, toType, toID, relationship, authPath string) {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, index),
			"_from": fromType + "/" + key(fromID), "_to": toType + "/" + key(toID),
			"project": project, "project_id": project, "dataset_generation": generation, "auth_resource_path": authPath,
			"label":     relationship,
			"from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	addEdge(1, "Observation", "o1", "Patient", "p1", "subject_Patient", "/allowed")
	addEdge(2, "Observation", "o1", "Patient", "p2", "subject_Patient", "/allowed")
	addEdge(3, "Observation", "o2", "Patient", "p1", "subject_Patient", "/allowed")
	addEdge(4, "Observation", "o2", "Patient", "p2", "subject_Patient", "/allowed")
	addEdge(5, "Observation", "wrong-project-observation", "Patient", "p1", "subject_Patient", "/allowed")
	addEdge(6, "Observation", "wrong-generation-observation", "Patient", "p2", "subject_Patient", "/allowed")
	addEdge(7, "Observation", "o1", "Specimen", "s1", "specimen_Specimen", "/allowed")
	addEdge(8, "Observation", "o1", "Specimen", "s_denied", "specimen_Specimen", "/allowed")
	addEdge(9, "Observation", "o_denied", "Patient", "p1", "subject_Patient", "/allowed")
	addEdge(10, "Observation", "o_root_only", "Patient", "p_denied", "subject_Patient", "/allowed")
	addEdge(11, "Observation", "o_edge_only", "Patient", "p2", "subject_Patient", "/forbidden")
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert grouped related-expansion edges: %v", err)
	}

	for _, policy := range []recipe.ExpansionEmptyPolicy{recipe.ExpansionPreserveParent, recipe.ExpansionExclude} {
		t.Run(string(policy), func(t *testing.T) {
			output := groupedRelatedExpansionOutput(policy)
			bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
			compiled := lowerConstructionOutput(t, output, bindings)
			if len(compiled.Stages) != 4 || len(compiled.Plan.StageSequence.Stages) != 3 || compiled.Plan.StageSequence.Stages[0].Group == nil ||
				compiled.Plan.StageSequence.Stages[1].RelatedExpand == nil || compiled.Plan.StageSequence.Stages[1].RelatedExpand.AnchorKind != "rootContributors" ||
				compiled.Plan.StageSequence.Stages[2].RelatedExpand == nil || compiled.Plan.StageSequence.Stages[2].RelatedExpand.AnchorKind != "activeRelatedRecord" {
				t.Fatalf("expected Group → contributor-set expansion → exact onward identity stages: %#v", compiled.Stages)
			}
			query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			rowIDs := map[string]bool{}
			maleByObservation := map[string]any{}
			femaleRows := 0
			for _, row := range rows {
				rowID, _ := row["__loom_row_id"].(string)
				if rowID == "" || rowIDs[rowID] {
					t.Errorf("missing or duplicate onward row identity: %#v", row)
				}
				rowIDs[rowID] = true
				if row["gender"] == "female" {
					femaleRows++
					if row["observation_id"] != nil || row["specimen_id"] != nil {
						t.Errorf("empty female contributor group acquired a related record: %#v", row)
					}
					continue
				}
				if !constructionNumericEqual(row["patient_count"], 2) {
					t.Errorf("male group lost a root contributor: %#v", row)
				}
				observationID, _ := row["observation_id"].(string)
				if observationID == "" || observationID == "wrong-project-observation" || observationID == "wrong-generation-observation" {
					t.Errorf("wrong-scope observation appeared in contributor union: %#v", row)
				}
				if _, duplicate := maleByObservation[observationID]; duplicate {
					t.Errorf("shared observation %q was emitted more than once for the grouped roots", observationID)
				}
				maleByObservation[observationID] = row["specimen_id"]
			}
			if policy == recipe.ExpansionPreserveParent && femaleRows != 1 {
				t.Errorf("PRESERVE_PARENT emitted %d empty-group rows, want one; rows=%#v", femaleRows, rows)
			}
			if policy == recipe.ExpansionExclude && femaleRows != 0 {
				t.Errorf("EXCLUDE retained empty-group rows: %#v", rows)
			}
			if len(maleByObservation) != 2 || maleByObservation["o1"] != "s1" || maleByObservation["o2"] != nil {
				t.Errorf("onward expansion did not follow each distinct observation identity: %#v; rows=%#v", maleByObservation, rows)
			}
		})
	}
	t.Run("ERROR", func(t *testing.T) {
		output := groupedRelatedExpansionOutput(recipe.ExpansionError)
		query := compileConstructionOutputQueryWithBindings(t, output, project, generation,
			recipe.RuntimeBindings{AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}})
		err := client.QueryRows(ctx, query.Query, 100, query.BindVars, func(map[string]any) error { return nil })
		if err == nil {
			t.Fatal("ERROR policy returned rows for the empty female contributor group")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "related expansion") || !strings.Contains(strings.ToLower(err.Error()), "no related records") {
			t.Fatalf("ERROR policy failed for an unrelated reason: %v", err)
		}
	})
	thresholdTwo, thresholdThree := 2, 3
	for _, match := range []struct {
		name      string
		kind      string
		threshold *int
		wantRows  int
		wantGroup string
	}{
		{name: "EXISTS", kind: recipe.RelatedEligibilityExists, wantRows: 1, wantGroup: "male"},
		{name: "ABSENT", kind: recipe.RelatedEligibilityAbsent, wantRows: 1, wantGroup: "female"},
		{name: "COUNT_AT_LEAST two distinct scoped targets", kind: recipe.RelatedEligibilityCountAtLeast, threshold: &thresholdTwo, wantRows: 1, wantGroup: "male"},
		{name: "COUNT_AT_LEAST excludes duplicate and unauthorized targets", kind: recipe.RelatedEligibilityCountAtLeast, threshold: &thresholdThree},
	} {
		t.Run("RELATED_ELIGIBILITY after Group/"+match.name, func(t *testing.T) {
			output := groupedRelatedEligibilityOutput(match.kind, match.threshold)
			bindings := recipe.RuntimeBindings{
				Project: project, DatasetGeneration: generation,
				AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
			}
			compiled := lowerConstructionOutput(t, output, bindings)
			if len(compiled.Plan.StageSequence.Stages) != 2 || compiled.Plan.StageSequence.Stages[0].Group == nil ||
				compiled.Plan.StageSequence.Stages[0].Group.RootContributorOutputColumn != "__loom_root_contributor_keys" ||
				compiled.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageRelatedEligibilityOp {
				t.Fatalf("expected direct Group -> related eligibility with materialized root contributors: %#v", compiled.Plan.StageSequence.Stages)
			}
			query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			if len(rows) != match.wantRows {
				t.Fatalf("%s returned %d grouped rows, want %d: %#v", match.name, len(rows), match.wantRows, rows)
			}
			if match.wantRows == 1 {
				wantCount := float64(2)
				if match.wantGroup == "female" {
					wantCount = 1
				}
				if rows[0]["gender"] != match.wantGroup || !constructionNumericEqual(rows[0]["patient_count"], wantCount) {
					t.Fatalf("%s returned wrong group or authorized root count: %#v", match.name, rows[0])
				}
			}
		})
	}
}

func groupedRelatedEligibilityOutput(matchKind string, threshold *int) recipe.Output {
	groupColumns := []recipe.StageColumn{
		{ID: "gender_id", Name: "gender", Type: "string"},
		{ID: "patient_count_id", Name: "patient_count", Type: "integer"},
	}
	return recipe.Output{
		Name: "grouped_related_eligibility", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_patients",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "gender_id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient_count_id"}},
					}}, Outputs: groupColumns},
				{ID: "filter_related", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_patients"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
						AnchorColumnID: "__loom_root_contributor_keys", ChoiceID: "patient-observation",
						TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}}, ContributorPolicy: "ALL_MATCHES", MatchKind: matchKind, Threshold: threshold,
					}}, Outputs: groupColumns},
			},
		},
	}
}

func groupedRelatedExpansionOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	observationIdentity := fmt.Sprintf("__loom_related_terminal_id_%x", sha256.Sum256([]byte("expand_observations")))
	groupColumns := []recipe.StageColumn{
		{ID: "gender_id", Name: "gender", Type: "string"},
		{ID: "patient_count_id", Name: "patient_count", Type: "integer"},
	}
	observationColumns := append(append([]recipe.StageColumn(nil), groupColumns...), recipe.StageColumn{
		ID: "observation_id", Name: "observation_id", Type: "string", Nullable: emptyPolicy == recipe.ExpansionPreserveParent,
	})
	finalColumns := append(append([]recipe.StageColumn(nil), observationColumns...), recipe.StageColumn{
		ID: "specimen_id", Name: "specimen_id", Type: "string", Nullable: true,
	})
	return recipe.Output{
		Name: "grouped_related_expansion", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_patients",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "gender_id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient_count_id"}},
					}}, Outputs: groupColumns},
				{ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_patients"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "__loom_root_contributor_keys", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: emptyPolicy, RelatedRecordColumnID: "observation_id",
					}}, Outputs: observationColumns},
				{ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: observationIdentity, ChoiceID: "observation-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node",
							FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
							StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
						}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "specimen_id",
					}}, Outputs: finalColumns},
			},
		},
	}
}

func TestRelatedFieldReadsExactExpandedRecordAfterFilterAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_related_field_"+uuid.NewString(), "generation-related-field"
	key := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-field fixtures from %s: %v", collection, err)
			}
		}
	}()
	document := func(id, resourceType, docGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": docGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	patients := []json.RawMessage{
		document("p1", "Patient", generation, "/allowed", map[string]any{"id": "p1", "resourceType": "Patient", "active": true}),
		document("p2", "Patient", generation, "/allowed", map[string]any{"id": "p2", "resourceType": "Patient", "active": true}),
		document("p3", "Patient", generation, "/allowed", map[string]any{"id": "p3", "resourceType": "Patient", "active": false}),
	}
	observations := []json.RawMessage{
		document("o1", "Observation", generation, "/allowed", map[string]any{"id": "o1", "resourceType": "Observation", "status": "registered"}),
		document("o2", "Observation", generation, "/forbidden", map[string]any{"id": "o2", "resourceType": "Observation", "status": "denied-scope"}),
		document("o3", "Observation", "generation-older", "/allowed", map[string]any{"id": "o3", "resourceType": "Observation", "status": "stale-generation"}),
	}
	for collection, rows := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations} {
		if err := client.InsertBatchRaw(ctx, collection, rows, false, "document"); err != nil {
			t.Fatalf("insert related-field fixtures into %s: %v", collection, err)
		}
	}
	edges := make([]json.RawMessage, 0, 3)
	for index, observationID := range []string{"o1", "o2", "o3"} {
		encoded, err := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from": "Observation/" + key(observationID), "_to": "Patient/" + key("p1"),
			"project": project, "project_id": project, "dataset_generation": generation,
			"auth_resource_path": "/allowed", "label": "subject_Patient",
			"from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert related-field edges: %v", err)
	}

	active := true
	sourceColumns := []recipe.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "patient-active", Name: "patient_active", Label: "Active"},
	}
	expandedColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true,
	})
	fieldColumns := append(append([]recipe.StageColumn(nil), expandedColumns...), recipe.StageColumn{
		ID: "observation-status", Name: "observation_status", Label: "Observation status", Type: "string", Nullable: true,
	})
	output := recipe.Output{
		Name: "related_field_oracle", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation-id",
				}}, Outputs: expandedColumns,
			},
			{
				ID: "keep_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-active", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &active}},
				}}, Outputs: expandedColumns,
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
					ChoiceID: "exact-observation-status-choice", OutputColumnID: "observation-status",
					Source: recipe.ConstructionRelatedFieldSource{
						CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
						Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
					},
				}}, Outputs: fieldColumns,
			},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/allowed"},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"related_field_document.project == @project",
		"related_field_document.dataset_generation == @dataset_generation",
		"related_field_document.auth_resource_path IN @auth_resource_paths",
	} {
		if !strings.Contains(query.Query, required) {
			t.Fatalf("exact field lookup omits scope guard %q: %s", required, query.Query)
		}
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	byPatient := make(map[string]map[string]any, len(rows))
	rowIDs := make(map[string]bool, len(rows))
	for _, row := range rows {
		patientID, _ := row["patient_id"].(string)
		byPatient[patientID] = row
		rowID, _ := row["__loom_row_id"].(string)
		if rowID == "" || rowIDs[rowID] {
			t.Errorf("related-field row identity is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[rowID] = true
	}
	if len(byPatient) != 2 || len(rowIDs) != 2 {
		t.Fatalf("related-field rows = %#v, want active p1 and preserved empty p2 only", rows)
	}
	if byPatient["p1"]["observation_id"] != "o1" || byPatient["p1"]["observation_status"] != "registered" {
		t.Fatalf("p1 exact related field = %#v, want authorized current-generation Observation o1", byPatient["p1"])
	}
	if byPatient["p2"]["observation_id"] != nil || byPatient["p2"]["observation_status"] != nil {
		t.Fatalf("PRESERVE_PARENT empty related field = %#v, want null observation ID and status", byPatient["p2"])
	}
	for _, forbidden := range []string{"denied-scope", "stale-generation"} {
		for _, row := range rows {
			if row["observation_status"] == forbidden {
				t.Fatalf("related field returned %q from a mismatched target document: %#v", forbidden, row)
			}
		}
	}
}

func relatedExpandOracleOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	trueValue := true
	columns := []recipe.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "patient-active", Name: "patient_active", Label: "Active"},
	}
	filteredColumns := append([]recipe.StageColumn(nil), columns...)
	finalColumns := append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: emptyPolicy == recipe.ExpansionPreserveParent,
	})
	return recipe.Output{
		Name: "related_expand_oracle", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "filter_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-active", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &trueValue}},
				}}, Outputs: filteredColumns,
			},
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: emptyPolicy, RelatedRecordColumnID: "observation-id",
				}}, Outputs: finalColumns,
			},
		}},
	}
}

func compileRelatedExpandOracleQuery(t *testing.T, output recipe.Output, project, generation string) (lower.CompiledRecipeOutput, CompiledQuery) {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related-expansion recipe plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve related-expansion recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related-expansion recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related-expansion query: %v", err)
	}
	return compiled.Outputs[0], query
}

func executeRowLineageOracleQuery(t *testing.T, ctx context.Context, client *store.Client, query CompiledRowLineageQuery) map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0, 1)
	if err := client.QueryRows(ctx, query.Query, 32, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute compiler-generated row lineage query: %v\n%s", err, query.Query)
	}
	if len(rows) != 1 {
		t.Fatalf("row lineage returned %d rows, want one result: %#v", len(rows), rows)
	}
	return rows[0]
}

func relatedExpandCountOutput(groupByPresence bool) recipe.Output {
	sourceField := recipe.Field{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}}
	sourceColumn := recipe.StageColumn{ID: sourceField.ColumnID, Name: sourceField.Name}
	countColumn := recipe.StageColumn{ID: "count-id", Name: "unused_count_column_marker", Type: "integer"}
	listColumn := recipe.StageColumn{ID: "list-id", Name: "unused_list_column_marker", Type: "string"}
	presenceColumn := recipe.StageColumn{ID: "presence-id", Name: "unused_presence_column_marker", Type: "boolean"}
	countColumns := []recipe.StageColumn{sourceColumn, countColumn}
	listColumns := append(append([]recipe.StageColumn(nil), countColumns...), listColumn)
	relatedSourceColumns := append(append([]recipe.StageColumn(nil), listColumns...), presenceColumn)
	expandedColumns := append(append([]recipe.StageColumn(nil), relatedSourceColumns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Type: "string",
	})
	group := &recipe.ConstructionGroup{
		ConstructionID: "count_expanded_rows",
		Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row-count-id"}},
	}
	groupOutputs := []recipe.StageColumn{{ID: "row-count-id", Name: "row_count", Type: "integer"}}
	if groupByPresence {
		group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: presenceColumn.ID, OutputColumnID: "presence-group-id"}}
		groupOutputs = append([]recipe.StageColumn{{ID: "presence-group-id", Name: "presence_group", Type: "boolean"}}, groupOutputs...)
	}
	statusSource := recipe.ConstructionRelatedFieldSource{
		CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
		Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
	}
	observationRoute := []recipe.ConstructionRelatedRouteStep{{
		EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	relatedSourceStep := func(id, inputStep, form string, output recipe.StageColumn, outputs []recipe.StageColumn) recipe.ConstructionStep {
		input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
		if inputStep != "" {
			input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: inputStep}
		}
		return recipe.ConstructionStep{
			ID: id, Inputs: []recipe.ConstructionInputRef{input},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: id + "-route", SourceOccurrenceID: "observation-node",
				Source: statusSource, Route: observationRoute, ContributorPolicy: "ALL_MATCHES",
				Form: form, OutputColumnID: output.ID,
			}},
			Outputs: outputs,
		}
	}
	return recipe.Output{
		Name: "related_expand_count", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{sourceField},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{sourceColumn},
			Steps: []recipe.ConstructionStep{
				relatedSourceStep("count", "", "COUNT", countColumn, countColumns),
				relatedSourceStep("list", "count", "ALL", listColumn, listColumns),
				relatedSourceStep("presence", "list", "PRESENCE", presenceColumn, relatedSourceColumns),
				{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "presence"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "_key", ChoiceID: "observation-route", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "observation-id",
					}},
					Outputs: expandedColumns,
				},
				{
					ID: "count_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: group}, Outputs: groupOutputs,
				},
			},
		},
	}
}

func compileRelatedExpandCountQuery(t *testing.T, output recipe.Output) CompiledQuery {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: "related-expand-count-project", DatasetGeneration: "related-expand-count-generation",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related expansion count recipe: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("resolve related expansion count recipe: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related expansion count recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("render related expansion count recipe: %v", err)
	}
	return query
}

func TestRelatedExpandRepeatedScalarContributorMatchesWithinTerminalRecordAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_related_repeated_"+uuid.NewString(), "generation-related-repeated"
	key := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER STARTS_WITH(document._key, @prefix) REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"prefix": project + "_"}); err != nil {
				t.Errorf("remove repeated-contributor fixtures from %s: %v", collection, err)
			}
		}
	}()

	document := func(id, resourceType, documentProject, documentGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		payload["id"], payload["resourceType"] = id, resourceType
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": documentProject, "project_id": documentProject,
			"dataset_generation": documentGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	targetCategory := []any{
		map[string]any{"coding": []any{
			map[string]any{"system": "urn:system:one", "code": "target-code"},
			map[string]any{"system": "urn:system:two", "code": "target-code"},
		}},
		map[string]any{"coding": []any{map[string]any{"system": "urn:system:three", "code": "target-code"}}},
	}
	nonmatchingCategory := []any{map[string]any{"coding": []any{map[string]any{"system": "urn:system:one", "code": "other-code"}}}}
	nullOnlyCategory := []any{map[string]any{"coding": []any{map[string]any{"system": "urn:system:null", "code": nil}}}}
	emptyOnlyCategory := []any{map[string]any{"coding": []any{}}}
	missingCodeCategory := []any{map[string]any{"coding": []any{map[string]any{"system": "urn:system:missing"}}}}
	patients := []json.RawMessage{
		document("p1", "Patient", project, generation, "/allowed", map[string]any{"active": true}),
		document("p2", "Patient", project, generation, "/allowed", map[string]any{"active": true}),
	}
	observations := []json.RawMessage{
		document("o_match", "Observation", project, generation, "/allowed", map[string]any{"status": "final", "category": targetCategory}),
		document("o_no_match", "Observation", project, generation, "/allowed", map[string]any{"status": "final", "category": nonmatchingCategory}),
		document("o_null_only", "Observation", project, generation, "/allowed", map[string]any{"status": "final", "category": nullOnlyCategory}),
		document("o_empty_only", "Observation", project, generation, "/allowed", map[string]any{"status": "final", "category": emptyOnlyCategory}),
		document("o_missing_code", "Observation", project, generation, "/allowed", map[string]any{"status": "final", "category": missingCodeCategory}),
		document("o_denied", "Observation", project, generation, "/denied", map[string]any{"status": "final", "category": targetCategory}),
		document("o_stale", "Observation", project, "stale-generation", "/allowed", map[string]any{"status": "final", "category": targetCategory}),
		document("o_foreign", "Observation", project+"_foreign", generation, "/allowed", map[string]any{"status": "final", "category": targetCategory}),
	}
	for collection, documents := range map[string][]json.RawMessage{"Patient": patients, "Observation": observations} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert repeated-contributor fixtures into %s: %v", collection, err)
		}
	}
	edges := make([]json.RawMessage, 0, len(observations))
	for index, observationID := range []string{"o_match", "o_no_match", "o_null_only", "o_empty_only", "o_missing_code", "o_denied", "o_stale", "o_foreign"} {
		encoded, err := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, index+1),
			"_from": "Observation/" + key(observationID), "_to": "Patient/" + key("p1"),
			"project": project, "project_id": project, "dataset_generation": generation, "auth_resource_path": "/allowed",
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatalf("insert repeated-contributor edges: %v", err)
	}

	output := relatedExpandOracleOutput(recipe.ExpansionExclude)
	related := output.Construction.Steps[1].Operation.RelatedExpand
	code := "target-code"
	related.ContributorPolicy = "ALL_MATCHES"
	related.ContributorPredicate = &recipe.ConstructionRelatedPredicate{
		CandidateID: "observation-code", Quantifier: recipe.QuantifierAny, Operator: recipe.FilterEquals,
		Value: &recipe.FilterValue{Kind: recipe.FilterCode, Code: &recipe.CodeValue{Code: code}},
	}
	related.ContributorSource = &recipe.ConstructionRelatedFieldSource{
		CandidateID: "observation-code", NodeID: "observation-node", ResourceType: "Observation",
		Path: "Observation.category[].coding[].code", Cardinality: "many", LogicalType: "code",
		RepeatedBoundaries: []recipe.ConstructionRelatedRepeatedBoundary{
			{Path: "category[]", MaxItems: 8},
			{Path: "category[].coding[]", MaxItems: 8},
		},
	}
	related.ContributorChoiceID = "observation-code-choice"
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	query := compileConstructionOutputQueryWithBindings(t, output, project, generation, bindings)
	for _, required := range []string{".project == @project", ".dataset_generation == @dataset_generation", "auth_resource_path IN @auth_resource_paths"} {
		if !strings.Contains(query.Query, required) {
			t.Fatalf("repeated related predicate query omitted scope guard %q: %s", required, query.Query)
		}
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 1 || rows[0]["patient_id"] != "p1" || rows[0]["observation_id"] != "o_match" {
		t.Fatalf("repeated code-only predicate returned %#v, want one p1/o_match terminal despite duplicate values", rows)
	}

	related.ContributorPredicate = &recipe.ConstructionRelatedPredicate{
		CandidateID: "observation-code", Quantifier: recipe.QuantifierAny, Operator: recipe.FilterExists,
	}
	existsQuery := compileConstructionOutputQueryWithBindings(t, output, project, generation, bindings)
	existsRows := executeReshapeOracleQuery(t, ctx, client, existsQuery)
	if len(existsRows) != 2 {
		t.Fatalf("repeated ANY EXISTS returned %#v, want only the two terminals with non-null code values", existsRows)
	}
	existing := map[string]bool{}
	for _, row := range existsRows {
		if row["patient_id"] != "p1" {
			t.Fatalf("repeated ANY EXISTS escaped its root scope: %#v", row)
		}
		if id, ok := row["observation_id"].(string); !ok || existing[id] {
			t.Fatalf("repeated ANY EXISTS returned a duplicate or invalid terminal: %#v", row)
		} else {
			existing[id] = true
		}
	}
	if !existing["o_match"] || !existing["o_no_match"] || existing["o_null_only"] || existing["o_empty_only"] || existing["o_missing_code"] {
		t.Fatalf("repeated ANY EXISTS included null/empty/missing values or omitted a populated terminal: %#v", existing)
	}
}

func TestTerminalRepeatedPrimitiveSelectorValuesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_repeated_terminal_"+uuid.NewString(), "generation-repeated-terminal"
	key := func(id string) string { return project + "_" + id }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		query := "FOR document IN Patient FILTER STARTS_WITH(document._key, @prefix) REMOVE document IN Patient"
		if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"prefix": project + "_"}); err != nil {
			t.Errorf("remove terminal repeated-selector fixtures: %v", err)
		}
	}()

	nullGivenElement := map[string]any{"extension": []any{map[string]any{
		"url": "https://example.org/fhir/StructureDefinition/test", "valueString": "extension-only",
	}}}
	givenNames := []any{
		map[string]any{"given": []any{"alpha", "alpha", nil}, "_given": []any{nil, nil, nullGivenElement}},
		map[string]any{"given": []any{nil}, "_given": []any{nullGivenElement}},
		map[string]any{"given": []any{"beta"}},
	}
	patientDocument := func(id string, payload map[string]any) json.RawMessage {
		t.Helper()
		payload["id"], payload["resourceType"] = id, "Patient"
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Patient",
			"auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	documents := []json.RawMessage{
		patientDocument("p_values", map[string]any{"name": givenNames}),
		patientDocument("p_null", map[string]any{"name": []any{map[string]any{"given": []any{nil}, "_given": []any{nullGivenElement}}}}),
		patientDocument("p_empty", map[string]any{"name": []any{map[string]any{"given": []any{}}}}),
		patientDocument("p_missing", map[string]any{"name": []any{map[string]any{"family": "OnlyFamily"}}}),
	}
	if err := client.InsertBatchRaw(ctx, "Patient", documents, false, "document"); err != nil {
		t.Fatalf("insert terminal repeated-selector fixtures: %v", err)
	}

	selector, err := spec.ParseSelector("name[].given[]")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []ir.PhysicalSelectorExecutionMode{ir.PhysicalSelectorGeneric, ir.PhysicalSelectorConditionalArray} {
		t.Run(string(mode), func(t *testing.T) {
			plan, err := buildGenericPhysicalPlanWithContext(
				semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}},
				semantic.ExecutionContext{
					Project: project, DatasetGeneration: generation, AuthResourcePaths: []string{"/allowed"},
					AuthScopeMode: authscope.ReadScopeRestricted,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{
				{Name: "_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
				{Name: "given_values", Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{
						Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						ResourceType: "Patient", Selector: selector, ExecutionMode: mode,
					},
				}},
			}
			rendered, err := aql.RenderPhysicalPlan(plan)
			if err != nil {
				t.Fatalf("render %s terminal primitive selector: %v", mode, err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: rendered.Query, BindVars: rendered.BindVars})
			if len(rows) != len(documents) {
				t.Fatalf("%s terminal primitive query returned %d rows, want %d:\n%s", mode, len(rows), len(documents), rendered.Query)
			}
			valuesByKey := make(map[string][]string, len(rows))
			for _, row := range rows {
				rowKey, _ := row["_key"].(string)
				rawValues, ok := row["given_values"].([]any)
				if !ok {
					t.Fatalf("%s given_values for %q has type %T: %#v", mode, rowKey, row["given_values"], row)
				}
				values := make([]string, 0, len(rawValues))
				for _, rawValue := range rawValues {
					value, ok := rawValue.(string)
					if !ok {
						t.Fatalf("%s terminal repeated selector retained a null/non-string leaf for %q: %#v", mode, rowKey, rawValues)
					}
					values = append(values, value)
				}
				valuesByKey[rowKey] = values
			}
			assertValues := func(id string, want []string) {
				t.Helper()
				got, ok := valuesByKey[key(id)]
				if !ok || len(got) != len(want) {
					t.Fatalf("%s values for %s = %#v (present=%t), want %#v", mode, id, got, ok, want)
				}
				for index := range want {
					if got[index] != want[index] {
						t.Fatalf("%s values for %s = %#v, want %#v", mode, id, got, want)
					}
				}
			}
			assertValues("p_values", []string{"alpha", "alpha", "beta"})
			assertValues("p_null", nil)
			assertValues("p_empty", nil)
			assertValues("p_missing", nil)
		})
	}
}
