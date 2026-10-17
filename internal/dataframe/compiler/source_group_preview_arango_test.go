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

func TestSourceGroupPreviewMatchesGenericScopedExpandedGroupAgainstArango(t *testing.T) {
	project, generation := "loom_source_group_preview_"+uuid.NewString(), "generation_"+uuid.NewString()
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	compiledOutput := lowerConstructionOutput(t, sourceGroupPreviewArangoOutput(), bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile key-paged source Group preview: %v", err)
	}
	full, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile generic full-source Group: %v", err)
	}
	selectedKeys := strings.Index(preview.Query, "source_group_preview_selected_root_keys")
	canonicalGroup := strings.Index(preview.Query, "LET __loom_construction_stage_1 = (")
	hasFocusRelationship := false
	for _, value := range preview.BindVars {
		if relationship, ok := value.(string); ok && relationship == "focus_Specimen" {
			hasFocusRelationship = true
			break
		}
	}
	if selectedKeys < 0 || canonicalGroup < selectedKeys || !hasFocusRelationship ||
		!strings.Contains(preview.Query, "WITH COUNT INTO") || !strings.Contains(preview.Query, "__loom_physical_unnest_index_0 IN") {
		t.Fatalf("preview did not key-page before the unchanged routed COUNT_ROWS Group:\n%s", preview.Query)
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	key := func(id string) string { return project + "_" + id }
	owned := map[string][]string{"Specimen": {}, "Observation": {}, "fhir_edge": {}}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR key IN @keys REMOVE key IN %s", collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove source Group preview fixtures from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents []json.RawMessage) {
		t.Helper()
		if len(documents) == 0 {
			return
		}
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert source Group preview fixtures into %s: %v", collection, err)
		}
	}
	resource := func(id, resourceType, docProject, docGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		collection := resourceType
		owned[collection] = append(owned[collection], key(id))
		payload["id"], payload["resourceType"] = id, resourceType
		document, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": docProject, "project_id": docProject,
			"dataset_generation": docGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	edge := func(id, rootID, observationID, docProject, docGeneration, authPath string) json.RawMessage {
		t.Helper()
		owned["fhir_edge"] = append(owned["fhir_edge"], key(id))
		document, err := json.Marshal(map[string]any{
			"_key": key(id), "_from": "Observation/" + key(observationID), "_to": "Specimen/" + key(rootID),
			"project": docProject, "project_id": docProject, "dataset_generation": docGeneration,
			"auth_resource_path": authPath, "label": "focus_Specimen",
			"from_type": "Observation", "to_type": "Specimen",
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	rootPayload := func(reference any, includeReference bool) map[string]any {
		subject := map[string]any{}
		if includeReference {
			subject["reference"] = reference
		}
		return map[string]any{"subject": subject}
	}
	coding := func(codes ...string) []any {
		items := make([]any, 0, len(codes))
		for _, code := range codes {
			items = append(items, map[string]any{"system": "urn:test", "code": code})
		}
		return items
	}
	observationPayload := func(codes ...string) map[string]any {
		return map[string]any{"valueCodeableConcept": map[string]any{"coding": coding(codes...)}}
	}
	roots := make([]json.RawMessage, 0, 45)
	observations := make([]json.RawMessage, 0, 45)
	edges := make([]json.RawMessage, 0, 48)
	addValid := func(rootID, observationID string, reference any, includeReference bool, codes ...string) {
		t.Helper()
		roots = append(roots, resource(rootID, "Specimen", project, generation, "/allowed", rootPayload(reference, includeReference)))
		observations = append(observations, resource(observationID, "Observation", project, generation, "/allowed", observationPayload(codes...)))
		edges = append(edges, edge("edge_"+rootID, rootID, observationID, project, generation, "/allowed"))
	}
	for index := 0; index < 27; index++ {
		rootID, observationID := fmt.Sprintf("group_%02d", index), fmt.Sprintf("observation_%02d", index)
		if index == 0 {
			addValid(rootID, observationID, rootID, true, "duplicate", "duplicate")
			edges = append(edges, edge("edge_group_00_duplicate", rootID, observationID, project, generation, "/allowed"))
			continue
		}
		addValid(rootID, observationID, rootID, true, "one")
	}
	addValid("missing_key", "observation_missing_key", nil, false, "one")
	addValid("null_key", "observation_null_key", nil, true, "one", "two")
	// This malformed key has no eligible relationship. It must not trigger the
	// STRING-key assertion in either the preview frontier or the generic Group.
	roots = append(roots, resource("malformed_ineligible", "Specimen", project, generation, "/allowed",
		rootPayload(map[string]any{"unexpected": "object"}, true)))
	observations = append(observations, resource("observation_malformed_ineligible", "Observation", project, generation, "/allowed", observationPayload()))
	roots = append(roots, resource("malformed_eligible", "Specimen", project, generation, "/allowed",
		rootPayload(map[string]any{"unexpected": "eligible-object"}, true)))
	// A key without any route is excluded from both the scoped source rows and
	// the preview frontier.
	roots = append(roots, resource("no_eligible_route", "Specimen", project, generation, "/allowed", rootPayload("zz-no-route", true)))

	addInvalidRoute := func(rootID, observationID, relation string, edgeProject, edgeGeneration, edgeAuth, targetProject, targetGeneration, targetAuth string) {
		t.Helper()
		roots = append(roots, resource(rootID, "Specimen", project, generation, "/allowed", rootPayload("zz-"+rootID, true)))
		observations = append(observations, resource(observationID, "Observation", targetProject, targetGeneration, targetAuth, observationPayload("one")))
		edges = append(edges, edge(relation, rootID, observationID, edgeProject, edgeGeneration, edgeAuth))
	}
	addInvalidRoute("edge_auth_denied", "target_for_edge_auth_denied", "edge_auth_denied", project, generation, "/blocked", project, generation, "/allowed")
	addInvalidRoute("edge_foreign_project", "target_for_edge_foreign_project", "edge_foreign_project", project+"_foreign", generation, "/allowed", project, generation, "/allowed")
	addInvalidRoute("edge_foreign_generation", "target_for_edge_foreign_generation", "edge_foreign_generation", project, generation+"_old", "/allowed", project, generation, "/allowed")
	addInvalidRoute("target_auth_denied", "target_auth_denied", "target_auth_denied", project, generation, "/allowed", project, generation, "/blocked")
	addInvalidRoute("target_foreign_project", "target_foreign_project", "target_foreign_project", project, generation, "/allowed", project+"_foreign", generation, "/allowed")
	addInvalidRoute("target_foreign_generation", "target_foreign_generation", "target_foreign_generation", project, generation, "/allowed", project, generation+"_old", "/allowed")
	insert("Specimen", roots)
	insert("Observation", observations)
	insert("fhir_edge", edges)

	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) != 28 || len(previewRows) != 25 || !reflect.DeepEqual(previewRows, fullRows[:25]) {
		t.Fatalf("key-paged first 25 source Groups differ from generic full-source order:\npreview %#v\nfull %#v", previewRows, fullRows)
	}
	if previewRows[0]["subject_reference"] != nil || previewRows[1]["subject_reference"] != "group_00" || previewRows[24]["subject_reference"] != "group_23" {
		t.Fatalf("first-page key ordering = %#v, want null followed by group_00 through group_23", previewRows)
	}
	if !constructionNumericEqual(previewRows[0]["rows"], 3) || !constructionNumericEqual(previewRows[1]["rows"], 4) {
		t.Fatalf("null/missing and duplicate-edge/coding multiplicities = %#v / %#v, want 3 / 4", previewRows[0], previewRows[1])
	}
	for _, row := range fullRows {
		if row["subject_reference"] == "zz-no-route" || row["subject_reference"] == "zz-edge_auth_denied" ||
			row["subject_reference"] == "zz-edge_foreign_project" || row["subject_reference"] == "zz-edge_foreign_generation" ||
			row["subject_reference"] == "zz-target_auth_denied" || row["subject_reference"] == "zz-target_foreign_project" ||
			row["subject_reference"] == "zz-target_foreign_generation" {
			t.Errorf("ineligible scoped route entered canonical Group output: %#v", row)
		}
	}
	if !constructionNumericEqual(fullRows[len(fullRows)-1]["rows"], 1) || fullRows[len(fullRows)-1]["subject_reference"] != "group_26" {
		t.Fatalf("last generic Group = %#v, want group_26 after the 25-row preview boundary", fullRows[len(fullRows)-1])
	}

	// Confirm from Arango's own mixed-type sort that an object key lies beyond
	// this 25-group page before making it relationship-eligible below.
	var orderedTypes []string
	orderQuery := `FOR root IN Specimen
FILTER root.project == @project AND root.dataset_generation == @dataset_generation
  AND root.auth_resource_path IN @auth_resource_paths
COLLECT group_key = root.payload.subject.reference
SORT group_key ASC
RETURN {kind: TYPENAME(group_key)}`
	if err := client.QueryRows(ctx, orderQuery, 100, map[string]any{
		"project": project, "dataset_generation": generation, "auth_resource_paths": []string{"/allowed"},
	}, func(row map[string]any) error {
		orderedTypes = append(orderedTypes, fmt.Sprint(row["kind"]))
		return nil
	}); err != nil {
		t.Fatalf("verify malformed-key order beyond preview page: %v", err)
	}
	objectPosition := -1
	for index, kind := range orderedTypes {
		if kind == "object" {
			objectPosition = index
			break
		}
	}
	if objectPosition < 25 {
		t.Fatalf("malformed object-key group position = %d in Arango sort order (%v), want beyond preview limit 25", objectPosition, orderedTypes)
	}

	eligibleTarget := resource("observation_malformed_eligible", "Observation", project, generation, "/allowed", observationPayload())
	insert("Observation", []json.RawMessage{eligibleTarget})
	eligibleEdge := edge("edge_malformed_eligible", "malformed_eligible", "observation_malformed_eligible", project, generation, "/allowed")
	insert("fhir_edge", []json.RawMessage{eligibleEdge})
	previewErr := executeSourceGroupPreviewError(ctx, client, preview)
	if previewErr == nil || !strings.Contains(previewErr.Error(), "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH") {
		t.Fatalf("eligible malformed STRING key preview error = %v, want CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH despite being beyond the first 25 groups", previewErr)
	}
	fullErr := executeSourceGroupPreviewError(ctx, client, full)
	if fullErr == nil || !strings.Contains(fullErr.Error(), "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH") {
		t.Fatalf("generic Group error for eligible malformed key = %v, want CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH", fullErr)
	}
}

func sourceGroupPreviewArangoOutput() recipe.Output {
	return recipe.Output{
		Name: "source_group_preview_arango", RootResourceType: "Specimen", RootOccurrenceID: "specimen-root", RowGrain: "expanded",
		Fields: []recipe.Field{{Name: "subject_reference", ColumnID: "subject-reference", Expr: recipe.Expression{Select: "root.subject.reference"}}},
		Traversals: []recipe.Traversal{{
			Name: "focus_Specimen", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation",
		}},
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner",
			From:              recipe.Expression{Select: "observation.valueCodeableConcept.coding[]"},
			As:                "coding", EmptyPolicy: recipe.ExpansionPreserveParent,
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "subject-reference", Name: "subject_reference", Type: "string", Nullable: true}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_subject_reference", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_subject_reference", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "subject-reference", OutputColumnID: "subject-reference-group"}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "subject-reference-group", Name: "subject_reference", Type: "string", Nullable: true},
					{ID: "rows", Name: "rows", Type: "integer"},
				},
			}},
		},
	}
}

func executeSourceGroupPreviewError(ctx context.Context, client *store.Client, query CompiledQuery) error {
	return client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
}
