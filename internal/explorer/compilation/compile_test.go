package compilation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/arangodb/go-driver/v2/utils"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestAcceptanceObservationContributorAQLAndBinds(t *testing.T) {
	fixturePath := testkit.FixturePath(t, "acceptance", "ncpi-tcga-brca", "workspace.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := ws.DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		t.Fatalf("decode locked acceptance workspace: %v", err)
	}
	if len(workspace.Documents) != 1 {
		t.Fatalf("locked fixture has %d documents, want 1", len(workspace.Documents))
	}
	document := workspace.Documents[0]
	wantColumns := map[string]string{
		"age_at_diagnosis_days":   "NCIT_C156418",
		"diagnostic_method":       "NCIT_C177576",
		"days_to_death":           "NCIT_C156419",
		"earliest_collection_day": "81247-9",
		"latest_collection_day":   "81247-9",
	}
	if len(document.Columns) != 17 {
		t.Fatalf("locked fixture has %d columns, want all 17", len(document.Columns))
	}

	snapshot := acceptanceObservationSnapshot()
	catalog := ws.CatalogFromCapability(snapshot, "acceptance")
	workspace.Documents = []ws.Document{document}
	migrated, err := ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatalf("migrate locked fixture contributor predicates: %v", err)
	}
	document = migrated.Documents[0]
	for _, column := range document.Columns {
		predicate := column.Contributor
		wantCode, selected := wantColumns[column.Column]
		if !selected {
			continue
		}
		if predicate == nil || predicate.Operator != ws.ContributorEquals || predicate.Quantifier != ws.ContributorAny || predicate.Value == nil || predicate.Value.Code == nil || predicate.Value.Code.Code != wantCode {
			t.Fatalf("fixture column %q migrated contributor = %#v, want ANY CODE EQUALS %q", column.Column, predicate, wantCode)
		}
	}
	compiled, err := Compile(context.Background(), "acceptance", "acceptance", document, snapshot)
	if err != nil {
		t.Fatalf("compile locked fixture contributor columns: %v", err)
	}
	runtimeBindings := recipe.RuntimeBindings{Project: "acceptance", DatasetGeneration: "tcga-brca-locked"}
	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, runtimeBindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "acceptance-scope", "tcga-brca-locked")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Outputs) != 1 {
		t.Fatalf("physical outputs = %d, want 1", len(physical.Outputs))
	}
	rendered, err := facade.Render(physical.Outputs[0].Plan, facade.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	binds, err := json.Marshal(rendered.BindVars)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("locked acceptance compiled aggregate AQL:\n%s\nBINDVARS: %s", rendered.Query, binds)
	for column, code := range wantColumns {
		if !strings.Contains(string(binds), code) {
			t.Errorf("compiled AQL bind vars omit %s predicate literal for %s: %s", code, column, binds)
		}
	}
	for _, fragment := range []string{"payload.code", "coding", "FILTER", "== @"} {
		if !strings.Contains(rendered.Query, fragment) {
			t.Errorf("compiled aggregate AQL omits expected repeated-code selector fragment %q:\n%s", fragment, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "code.coding[].code") {
		t.Fatal("compiled AQL leaked authoring selector syntax instead of lowering it to payload access")
	}

	arangoURL := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	arangoDatabase := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if arangoURL == "" && arangoDatabase == "" {
		return
	}
	if arangoURL == "" || arangoDatabase == "" {
		t.Fatal("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must be set together")
	}
	database := arangotest.Shared(t)
	executeAcceptanceObservationAQL(t, database.URL, database.Name, physical.Outputs[0], runtimeBindings, rendered.Query, rendered.BindVars)
}

func executeAcceptanceObservationAQL(
	t *testing.T,
	endpoint, database string,
	output facade.CompiledRecipeOutput,
	bindings recipe.RuntimeBindings,
	query string,
	bindVars map[string]any,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(random[:])
	patientCollection := "loom_obsdiag_patient_" + suffix
	conditionCollection := "loom_obsdiag_condition_" + suffix
	specimenCollection := "loom_obsdiag_specimen_" + suffix
	observationCollection := "loom_obsdiag_observation_" + suffix
	edgeCollection := "loom_obsdiag_edge_" + suffix

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	connection := connection.NewHttpConnection(connection.HttpConfiguration{
		Endpoint: connection.NewRoundRobinEndpoints([]string{endpoint}), Transport: transport,
	})
	client := driver.NewClient(connection)
	exists, err := client.DatabaseExists(ctx, database)
	if err != nil {
		t.Fatalf("check configured ArangoDB database: %v", err)
	}
	if !exists {
		t.Fatalf("configured ArangoDB database %q does not exist; refusing to create it", database)
	}
	db, err := client.GetDatabase(ctx, database, nil)
	if err != nil {
		t.Fatalf("open configured ArangoDB database: %v", err)
	}
	collections := make([]driver.Collection, 0, 5)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		for index := len(collections) - 1; index >= 0; index-- {
			if err := collections[index].Remove(cleanupCtx); err != nil {
				t.Errorf("remove private diagnostic collection: %v", err)
			}
		}
	}()
	for _, item := range []struct {
		name string
		edge bool
	}{
		{patientCollection, false}, {conditionCollection, false}, {specimenCollection, false},
		{observationCollection, false}, {edgeCollection, true},
	} {
		properties := &driver.CreateCollectionPropertiesV2{}
		if item.edge {
			properties.Type = utils.NewType(driver.CollectionTypeEdge)
		}
		collection, createErr := db.CreateCollectionV2(ctx, item.name, properties)
		if createErr != nil {
			t.Fatalf("create unique private collection %s: %v", item.name, createErr)
		}
		collections = append(collections, collection)
	}

	store, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open configured ArangoDB through repository query helper: %v", err)
	}
	project, generation := "acceptance", "tcga-brca-locked"
	vertices := map[string][]json.RawMessage{
		patientCollection: {acceptanceRawDocument(t, map[string]any{
			"_key": "p1", "id": "p1", "resourceType": "Patient", "project": project, "project_id": project, "dataset_generation": generation,
			"payload": map[string]any{"resourceType": "Patient", "id": "p1", "identifier": []any{}, "extension": []any{}},
		})},
		conditionCollection: {acceptanceRawDocument(t, map[string]any{
			"_key": "c1", "id": "c1", "resourceType": "Condition", "project": project, "project_id": project, "dataset_generation": generation,
			"payload": map[string]any{"resourceType": "Condition", "id": "c1", "code": map[string]any{"coding": []any{map[string]any{"display": "Histology"}}}, "stage": []any{map[string]any{"summary": map[string]any{"coding": []any{map[string]any{"display": "Stage II"}}}}}},
		})},
		specimenCollection: {
			acceptanceRawDocument(t, map[string]any{
				"_key": "s1", "id": "s1", "resourceType": "Specimen", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Specimen", "id": "s1", "type": map[string]any{"coding": []any{map[string]any{"code": "Tumor"}, map[string]any{"code": "Normal"}}}},
			}),
			acceptanceRawDocument(t, map[string]any{
				"_key": "s_wrong_edge_type", "id": "s_wrong_edge_type", "resourceType": "Specimen", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Specimen", "id": "s_wrong_edge_type", "type": map[string]any{"coding": []any{map[string]any{"code": "Other"}}}},
			}),
		},
		observationCollection: {
			acceptanceRawDocument(t, map[string]any{
				"_key": "o_age", "id": "o_age", "resourceType": "Observation", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Observation", "id": "o_age", "code": map[string]any{"coding": []any{map[string]any{"code": "NCIT_C156418"}}}, "valueQuantity": map[string]any{"value": 31}},
			}),
			acceptanceRawDocument(t, map[string]any{
				"_key": "o_method", "id": "o_method", "resourceType": "Observation", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Observation", "id": "o_method", "code": map[string]any{"coding": []any{map[string]any{"code": "NCIT_C177576"}}}, "valueString": "MRI"},
			}),
			acceptanceRawDocument(t, map[string]any{
				"_key": "o_death", "id": "o_death", "resourceType": "Observation", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Observation", "id": "o_death", "code": map[string]any{"coding": []any{map[string]any{"code": "NCIT_C156419"}}}, "valueQuantity": map[string]any{"value": 987}},
			}),
			acceptanceRawDocument(t, map[string]any{
				"_key": "o_collection", "id": "o_collection", "resourceType": "Observation", "project": project, "project_id": project, "dataset_generation": generation,
				"payload": map[string]any{"resourceType": "Observation", "id": "o_collection", "code": map[string]any{"coding": []any{map[string]any{"code": "81247-9"}}}, "component": []any{map[string]any{"valueInteger": 12}, map[string]any{"valueInteger": 27}}},
			}),
		},
	}
	for collection, docs := range vertices {
		if err := store.InsertBatchRaw(ctx, collection, docs, false, "document"); err != nil {
			t.Fatalf("insert private fixture vertices into %s: %v", collection, err)
		}
	}
	edges := []json.RawMessage{
		acceptanceRawDocument(t, acceptanceEdge("patient_condition", conditionCollection+"/c1", patientCollection+"/p1", "Condition", "Patient", "subject_Patient", project, generation)),
		acceptanceRawDocument(t, acceptanceEdge("patient_specimen", specimenCollection+"/s1", patientCollection+"/p1", "Specimen", "Patient", "subject_Patient", project, generation)),

		acceptanceRawDocument(t, acceptanceEdge("patient_specimen_wrong_edge_type", specimenCollection+"/s_wrong_edge_type", patientCollection+"/p1", "Condition", "Patient", "subject_Patient", project, generation)),
		acceptanceRawDocument(t, acceptanceEdge("patient_age", observationCollection+"/o_age", patientCollection+"/p1", "Observation", "Patient", "subject_Patient", project, generation)),
		acceptanceRawDocument(t, acceptanceEdge("patient_method", observationCollection+"/o_method", patientCollection+"/p1", "Observation", "Patient", "subject_Patient", project, generation)),
		acceptanceRawDocument(t, acceptanceEdge("patient_death", observationCollection+"/o_death", patientCollection+"/p1", "Observation", "Patient", "subject_Patient", project, generation)),
		acceptanceRawDocument(t, acceptanceEdge("specimen_collection", observationCollection+"/o_collection", specimenCollection+"/s1", "Observation", "Specimen", "specimen_Specimen", project, generation)),
	}
	if err := store.InsertBatchRaw(ctx, edgeCollection, edges, false, "document"); err != nil {
		t.Fatalf("insert private fixture edges: %v", err)
	}

	cloneBinds := func(input map[string]any) map[string]any {
		output := make(map[string]any, len(input))
		for key, value := range input {
			output[key] = value
		}
		for key := range output {
			if strings.HasPrefix(key, "@") {
				if key == "@root_collection" {
					output[key] = patientCollection
				} else {
					output[key] = edgeCollection
				}
			}
		}
		return output
	}
	execute := func(label, query string, binds map[string]any) []map[string]any {
		t.Helper()
		if binds["dataset_generation"] != generation {
			t.Fatalf("%s query generation bind = %#v, want %q", label, binds["dataset_generation"], generation)
		}
		rows := make([]map[string]any, 0, 1)
		if err := store.QueryRows(ctx, query, 10, binds, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		}); err != nil {
			t.Fatalf("execute %s query against private fixture collections: %v\n%s", label, err, query)
		}
		if len(rows) != 1 {
			t.Fatalf("%s query returned %d rows, want one private Patient row: %#v", label, len(rows), rows)
		}
		return rows
	}
	expectValues := func(label string, row map[string]any) {
		t.Helper()
		for column, want := range map[string]float64{
			"age_at_diagnosis_days": 31, "days_to_death": 987,
			"earliest_collection_day": 12, "latest_collection_day": 27,
			"specimen_count": 1, "tumor_specimen_count": 1, "normal_specimen_count": 1,
		} {
			assertAcceptanceAQLNumber(t, row[column], want, label+"."+column)
		}
		if got := row["diagnostic_method"]; got != "MRI" {
			t.Errorf("%s.diagnostic_method = %#v, want %q", label, got, "MRI")
		}
		if got := row["has_paired_tumor_normal"]; got != true {
			t.Errorf("%s.has_paired_tumor_normal = %#v, want true", label, got)
		}
	}

	rawRows := execute("direct-render", query, cloneBinds(bindVars))
	expectValues("direct-render", rawRows[0])
	previewBindings := bindings.Clone()
	previewBindings.PreviewLimit = 25
	full, err := facade.CompileRecipeOutputWithPolicy(output, previewBindings, 25, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile production full-preview query: %v", err)
	}
	fullBindLog, err := json.Marshal(full.BindVars)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production full-preview AQL:\n%s\nBINDVARS: %s", full.Query, fullBindLog)
	fullRows := execute("production-full", full.Query, cloneBinds(full.BindVars))
	expectValues("production-full", fullRows[0])
	page, err := facade.CompileRecipeOutputPageWithPolicy(output, previewBindings, 1, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile production selected-root page: %v", err)
	}
	pageRowsBindLog, err := json.Marshal(page.RowsBindVars)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production selected-root page rows AQL:\n%s\nBINDVARS: %s", page.RowsQuery, pageRowsBindLog)
	keyBinds := cloneBinds(page.RootKeysBindVars)
	keyBinds[facade.RootPageAfterKeyBind] = ""
	keyBinds = cloneBinds(keyBinds)
	rootKeys := make([]string, 0, 1)
	if err := store.QueryRows(ctx, page.RootKeysQuery, 10, keyBinds, func(row map[string]any) error {
		key, ok := row["_key"].(string)
		if !ok || key == "" {
			return fmt.Errorf("root key page returned invalid _key %#v", row["_key"])
		}
		rootKeys = append(rootKeys, key)
		return nil
	}); err != nil {
		t.Fatalf("execute production root-key page: %v\n%s", err, page.RootKeysQuery)
	}
	if len(rootKeys) != 1 || rootKeys[0] != "p1" {
		t.Fatalf("production root-key page = %#v, want [p1]", rootKeys)
	}
	pageBinds := cloneBinds(page.RowsBindVars)
	pageBinds[facade.RootPageKeysBind] = rootKeys
	pagedRows := execute("production-paged", page.RowsQuery, pageBinds)
	expectValues("production-paged", pagedRows[0])
	for _, column := range []string{"age_at_diagnosis_days", "diagnostic_method", "days_to_death", "earliest_collection_day", "latest_collection_day"} {
		if rawRows[0][column] != fullRows[0][column] || rawRows[0][column] != pagedRows[0][column] {
			t.Errorf("%s differs across direct/full/paged execution: %#v / %#v / %#v", column, rawRows[0][column], fullRows[0][column], pagedRows[0][column])
		}
	}
}

func acceptanceRawDocument(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func acceptanceEdge(key, from, to, fromType, toType, label, project, generation string) map[string]any {
	return map[string]any{
		"_key": key, "_from": from, "_to": to, "from_type": fromType, "to_type": toType,
		"label": label, "project": project, "dataset_generation": generation,
	}
}

func assertAcceptanceAQLNumber(t *testing.T, value any, want float64, column string) {
	t.Helper()
	var got float64
	switch number := value.(type) {
	case float64:
		got = number
	case int:
		got = float64(number)
	case int64:
		got = float64(number)
	default:
		t.Errorf("%s = %#v (%T), want numeric %v", column, value, value, want)
		return
	}
	if got != want {
		t.Errorf("%s = %v, want %v", column, got, want)
	}
}

func acceptanceObservationSnapshot() capability.Snapshot {
	base := fixtureSnapshotForProject("acceptance")
	nodes := []capability.Node{
		{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", DocumentCount: 100},
		{ID: "n_condition", ResourceType: "Condition", DocumentCount: 136},
		{ID: "n_specimen", ResourceType: "Specimen", DocumentCount: 3151},
		{ID: "n_observation", ResourceType: "Observation", DocumentCount: 3470},
	}
	edges := []capability.Edge{
		{ID: "e_condition", FromNodeID: "n_patient", ToNodeID: "n_condition", Label: "subject_Patient"},
		{ID: "e_specimen", FromNodeID: "n_patient", ToNodeID: "n_specimen", Label: "subject_Patient"},
		{ID: "e_collection_observation", FromNodeID: "n_specimen", ToNodeID: "n_observation", Label: "specimen_Specimen"},
		{ID: "e_observation", FromNodeID: "n_patient", ToNodeID: "n_observation", Label: "subject_Patient"},
	}
	ops := []capability.Operation{capability.OperationSelect, capability.OperationFilter, capability.OperationAggregate}
	candidates := []capability.Candidate{
		{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient.id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops},
		{ID: "c_condition_display", NodeID: "n_condition", ResourceType: "Condition", FieldPath: "code.coding[].display", Label: "Condition.code.coding[].display", LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 4}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_condition_stage_display", NodeID: "n_condition", ResourceType: "Condition", FieldPath: "stage[].summary.coding[].display", Label: "Condition.stage[].summary.coding[].display", LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "stage[]", MaxItems: 4}, {Path: "stage[].summary.coding[]", MaxItems: 4}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_specimen_type_code", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].code", Label: "Specimen.type.coding[].code", LogicalType: "code", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 4}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_observation_code", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "code.coding[].code", Label: "Observation.code.coding[].code", LogicalType: "code", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 4}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_observation_quantity", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "valueQuantity.value", Label: "Observation.valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops},
		{ID: "c_observation_string", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "valueString", Label: "Observation.valueString", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops},
		{ID: "c_observation_component_integer", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "component[].valueInteger", Label: "Observation.component[].valueInteger", LogicalType: "integer", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]", MaxItems: 8}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
	}
	return capability.NewSnapshot(base.Identity, base.Policy, capability.StatusReady, true, false, nodes, edges, candidates, nil)
}

func TestCompileExpandedRowsBindsDeepExactOccurrenceAndIdentity(t *testing.T) {
	snapshot := expandedRowsSnapshot()
	document := expandedRowsDocument("second_patient", "name[]")
	document.Route.Children = []ws.RouteNode{{
		OccurrenceID: "first_condition", ResourceType: "Condition", Relationship: "subject_Patient", CatalogEdgeID: "e_condition",
		Children: []ws.RouteNode{{
			OccurrenceID: "second_patient", ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "e_patient_return",
		}},
	}}

	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 {
		t.Fatalf("compiled outputs=%d, want one", len(compiled.Bundle.Outputs))
	}
	output := compiled.Bundle.Outputs[0]
	if output.RootOccurrenceID != ws.RootOccurrenceID || output.RowGrain != "expanded" {
		t.Fatalf("compiled root identity/grain = %q/%q", output.RootOccurrenceID, output.RowGrain)
	}
	if output.Expand == nil || output.Expand.OwnerOccurrenceID != "second_patient" || output.Expand.From.Select != "second_patient.name[]" || output.Expand.As != "__loom_expanded_item" || output.Expand.Ordinality != "__loom_expanded_ordinal" || output.Expand.EmptyPolicy != recipe.ExpansionPreserveParent {
		t.Fatalf("compiled expansion = %#v", output.Expand)
	}
	if output.Identity == nil || output.Identity.Name != "__loom_row_id" || output.Identity.Expansion == nil || output.Identity.Expr.Select != "" {
		t.Fatalf("compiled identity = %#v", output.Identity)
	}
	if len(output.Traversals) != 1 || output.Traversals[0].OccurrenceID != "first_condition" || len(output.Traversals[0].Traversals) != 1 || output.Traversals[0].Traversals[0].OccurrenceID != "second_patient" {
		t.Fatalf("compiled traversal occurrence IDs = %#v", output.Traversals)
	}
	if compiled.OutputContract.RowGrain != "expanded" || compiled.OutputContract.RowMultiplication != "expand" {
		t.Fatalf("compiled output contract = %#v", compiled.OutputContract)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("expanded output contract does not match compiled evidence: %v", err)
	}

	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a"})
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	physical, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan: %v", err)
	}
	rendered, err := facade.Render(physical.Outputs[0].Plan, facade.RenderOptions{})
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Count(rendered.Query, "FOR __loom_expansion_edge_") != 2 || !strings.Contains(rendered.Query, "__loom_expansion_edge_2._id") || !strings.Contains(rendered.Query, "__loom_has_expanded_item") || !strings.Contains(rendered.Query, "__loom_expanded_ordinal") {
		t.Fatalf("deep occurrence expansion evidence missing from rendered query:\n%s", rendered.Query)
	}
	if rendered.BindVars["expansion_identity_occurrence"] != "second_patient" {
		t.Fatalf("rendered expansion identity occurrence = %#v", rendered.BindVars["expansion_identity_occurrence"])
	}
}

func TestCompileExpandedRowsRebasesDirectFieldsWithinSelectedScope(t *testing.T) {
	document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
	document.Route.Children = []ws.RouteNode{{
		OccurrenceID: "first_condition", ResourceType: "Condition", Relationship: "subject_Patient", CatalogEdgeID: "e_condition",
		Children: []ws.RouteNode{{
			OccurrenceID: "second_patient", ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "e_patient_return",
		}},
	}}
	document.Columns = append(document.Columns,
		ws.Column{Column: "given_first", Label: "Given first", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "FIRST"}}},
		ws.Column{Column: "given_all", Label: "Given all", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "ALL"}}},
		ws.Column{Column: "family_first", Label: "Family", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}},
		ws.Column{Column: "related_given", Label: "Related given", OccurrenceID: "second_patient", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "FIRST"}}},
		ws.Column{Column: "related_given_slots", Label: "Related given slots", OccurrenceID: "second_patient", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}}},
	)
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	fields := compiled.Bundle.Outputs[0].Fields
	got := make(map[string]recipe.Field, len(fields))
	for _, field := range fields {
		got[field.Name] = field
	}
	for name, want := range map[string]struct {
		selectPath string
		fieldRef   string
		valueMode  recipe.ValueMode
	}{
		"patient_id":   {selectPath: "root.id", fieldRef: "id", valueMode: recipe.ValueModeAuto},
		"given_first":  {selectPath: "__loom_expanded_item.given[]", fieldRef: "name[].given[]", valueMode: recipe.ValueModeFirst},
		"given_all":    {selectPath: "__loom_expanded_item.given[]", fieldRef: "name[].given[]", valueMode: recipe.ValueModeAll},
		"family_first": {selectPath: "__loom_expanded_item.family", fieldRef: "name[].family", valueMode: recipe.ValueModeFirst},
	} {
		field, ok := got[name]
		if !ok {
			t.Fatalf("compiled recipe is missing field %q: %#v", name, fields)
		}
		if field.Expr.Select != want.selectPath || field.FieldRef != want.fieldRef || field.ValueMode != want.valueMode {
			t.Errorf("field %q = selector %q, FieldRef %q, value mode %q; want %q, %q, %q", name, field.Expr.Select, field.FieldRef, field.ValueMode, want.selectPath, want.fieldRef, want.valueMode)
		}
	}
	var relatedFields []recipe.Field
	for _, traversal := range compiled.Bundle.Outputs[0].Traversals {
		if traversal.OccurrenceID == "first_condition" && len(traversal.Traversals) > 0 && traversal.Traversals[0].OccurrenceID == "second_patient" {
			relatedFields = traversal.Traversals[0].Fields
		}
	}
	var relatedGiven, relatedIndexed recipe.Field
	for _, field := range relatedFields {
		switch field.Name {
		case "related_given":
			relatedGiven = field
		case "related_given_slots__0__0":
			relatedIndexed = field
		}
	}
	if relatedGiven.Expr.Select != "second_patient.name[].given[]" || relatedGiven.FieldRef != "name[].given[]" || relatedIndexed.Expr.Select != "second_patient.name[0].given[0]" || relatedIndexed.FieldRef != "name[].given[]" {
		t.Fatalf("same field path on a different occurrence was rebound to the expanded owner: %#v", relatedFields)
	}
}

func TestCompileExpandedRowsRejectsIndexedProjectionThatConsumesExpandedBoundary(t *testing.T) {
	document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
	document.Columns = append(document.Columns, ws.Column{
		Column: "given_slots", Label: "Given slots", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}},
	})

	_, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "INDEXED_PROJECTION_OVERLAPS_EXPANDED_SCOPE" {
		t.Fatalf("expanded-row INDEXED compile error = %v, want an explicit unsupported overlap", err)
	}
	if compileErr.Details["scopePath"] != "name[]" || compileErr.Details["fieldPath"] != "name[].given[]" {
		t.Fatalf("overlap error details = %#v", compileErr.Details)
	}
}

func TestCompileExpandedRowsKeepsIndexedProjectionOnUnrelatedBoundary(t *testing.T) {
	snapshot := expandedRowsSnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_telecom_value", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "telecom[].value",
		LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "telecom[]", MaxItems: 2}},
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionIndexed, capability.ProjectionFirst, capability.ProjectionArray},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
	})
	snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
	document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
	document.Columns = append(document.Columns, ws.Column{
		Column: "telecom_value", Label: "Telecom value", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "telecom[].value", ProjectionMode: "INDEXED"}},
	})

	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("nonoverlapping INDEXED field compile error = %v", err)
	}
	for _, field := range compiled.Bundle.Outputs[0].Fields {
		if field.Name == "telecom_value__0" {
			if field.Expr.Select != "root.telecom[0].value" || field.FieldRef != "telecom[].value" {
				t.Fatalf("unrelated indexed field = %#v", field)
			}
			return
		}
	}
	t.Fatalf("compiled fields do not contain unrelated INDEXED column: %#v", compiled.Bundle.Outputs[0].Fields)
}

func TestCompileExpandedRowsBindsRootOccurrenceAndMapsEmptyPolicies(t *testing.T) {
	policies := []struct {
		authored ws.EmptyCollectionPolicy
		recipe   recipe.ExpansionEmptyPolicy
	}{
		{ws.EmptyCollectionError, recipe.ExpansionError},
		{ws.EmptyCollectionExclude, recipe.ExpansionExclude},
		{ws.EmptyCollectionPreserveParent, recipe.ExpansionPreserveParent},
	}
	for _, policy := range policies {
		t.Run(string(policy.authored), func(t *testing.T) {
			document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
			document.Rows.Expanded.EmptyCollectionPolicy = policy.authored
			compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			output := compiled.Bundle.Outputs[0]
			if output.RootOccurrenceID != ws.RootOccurrenceID || output.Expand == nil || output.Expand.OwnerOccurrenceID != ws.RootOccurrenceID || output.Expand.From.Select != "root.name[]" || output.Expand.EmptyPolicy != policy.recipe {
				t.Fatalf("root expansion = %#v", output.Expand)
			}
			if output.Identity == nil || output.Identity.Expansion == nil || compiled.OutputContract.RowMultiplication != "expand" {
				t.Fatalf("expanded identity/contract missing: identity=%#v contract=%#v", output.Identity, compiled.OutputContract)
			}
		})
	}
}

func TestCompileExpandedRowsUsesAuthorizedRepeatedBoundaryWitnesses(t *testing.T) {
	snapshot := expandedRowsSnapshot()
	leafCandidates := snapshot.Candidates[:0]
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != "c_patient_names" {
			leafCandidates = append(leafCandidates, candidate)
		}
	}
	snapshot.Candidates = leafCandidates
	snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
	document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")

	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("compile leaf-only repeated-boundary scope: %v", err)
	}
	if expanded := compiled.Bundle.Outputs[0].Expand; expanded == nil || expanded.OwnerOccurrenceID != ws.RootOccurrenceID || expanded.From.Select != "root.name[]" {
		t.Fatalf("expanded row definition = %#v, want root-owned root.name[]", expanded)
	}

	for _, test := range []struct {
		name   string
		mutate func(*capability.Snapshot)
	}{
		{
			name: "boundary removed",
			mutate: func(snapshot *capability.Snapshot) {
				for index := range snapshot.Candidates {
					if snapshot.Candidates[index].ID == "c_patient_given" || snapshot.Candidates[index].ID == "c_patient_family" {
						snapshot.Candidates[index].RepeatedBoundaries = nil
					}
				}
			},
		},
		{
			name: "boundary belongs to another node",
			mutate: func(snapshot *capability.Snapshot) {
				for index := range snapshot.Candidates {
					if snapshot.Candidates[index].ID == "c_patient_given" || snapshot.Candidates[index].ID == "c_patient_family" {
						snapshot.Candidates[index].NodeID = "n_encounter"
					}
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stale := snapshot.Clone()
			test.mutate(&stale)
			stale = capability.NewSnapshot(stale.Identity, stale.Policy, stale.Status, stale.Complete, stale.Truncated, stale.Nodes, stale.Edges, stale.Candidates, stale.Diagnostics)
			_, err := Compile(context.Background(), "project-a", "explorer-a", document, stale)
			var compileErr *Error
			if !errors.As(err, &compileErr) || compileErr.Code != "STALE_EXPANSION_SCOPE" {
				t.Fatalf("Compile error = %v, want STALE_EXPANSION_SCOPE", err)
			}
		})
	}
}

func TestCompileExplicitGroupRowsPinsRevisionAndPolicy(t *testing.T) {
	document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{Source: ws.GroupSource{
		Kind:     ws.GroupSourceExplicit,
		Explicit: &ws.ExplicitGroupSource{RevisionID: "grouprev_test", UnassignedMemberPolicy: ws.UnassignedMemberGroupAsUnassigned},
	}}}
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Bundle.Outputs[0]
	if output.RowGrain != "groups" || output.GroupRows == nil || output.GroupRows.RevisionID != "grouprev_test" || output.GroupRows.UnassignedMemberPolicy != "GROUP_AS_UNASSIGNED" {
		t.Fatalf("compiled group rows = %#v", output)
	}
	if compiled.OutputContract.RowGrain != "groups" {
		t.Fatalf("group output contract = %#v", compiled.OutputContract)
	}
}

func TestCompileExpandedRowsRejectsStaleAmbiguousAndScalarScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ws.Document, *capability.Snapshot)
		code   string
	}{
		{
			name: "stale occurrence",
			mutate: func(document *ws.Document, _ *capability.Snapshot) {
				document.Rows.Expanded.OccurrenceID = "missing_occurrence"
			},
			code: "STALE_EXPANSION_OCCURRENCE",
		},
		{
			name: "scalar scope",
			mutate: func(document *ws.Document, _ *capability.Snapshot) {
				document.Rows.Expanded.ScopePath = "id"
			},
			code: "UNSUPPORTED_EXPANSION_SCOPE",
		},
		{
			name: "ambiguous capability scope",
			mutate: func(_ *ws.Document, snapshot *capability.Snapshot) {
				snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
					ID: "c_patient_names_duplicate", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]",
					LogicalType: "HumanName", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}},
					ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
				})
				*snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
			},
			code: "AMBIGUOUS_EXPANSION_SCOPE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := expandedRowsSnapshot()
			document := expandedRowsDocument(ws.RootOccurrenceID, "name[]")
			test.mutate(&document, &snapshot)
			_, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
			var compileErr *Error
			if !errors.As(err, &compileErr) || compileErr.Code != test.code {
				t.Fatalf("Compile error = %v, want code %q", err, test.code)
			}
		})
	}
}

func expandedRowsDocument(owner, scopePath string) ws.Document {
	return ws.Document{
		Rows: ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
			OccurrenceID: owner, ScopePath: scopePath, EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent,
		}},
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []ws.Column{{
			Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func expandedRowsSnapshot() capability.Snapshot {
	snapshot := fixtureSnapshot()
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_condition", ResourceType: "Condition", RowRootEligible: true, RowGrain: "condition"})
	for index := range snapshot.Edges {
		switch snapshot.Edges[index].ID {
		case "e_encounter":
			snapshot.Edges[index].SourceResourceType = "Patient"
			snapshot.Edges[index].TargetResourceType = "Encounter"
			snapshot.Edges[index].StorageDirection = "OUTBOUND"
		case "e_self":
			snapshot.Edges[index].SourceResourceType = "Encounter"
			snapshot.Edges[index].TargetResourceType = "Encounter"
			snapshot.Edges[index].StorageDirection = "OUTBOUND"
		}
	}
	snapshot.Edges = append(snapshot.Edges,
		capability.Edge{ID: "e_condition", FromNodeID: "n_patient", ToNodeID: "n_condition", Label: "subject_Patient", SourceResourceType: "Patient", TargetResourceType: "Condition", StorageDirection: "OUTBOUND"},
		capability.Edge{ID: "e_patient_return", FromNodeID: "n_condition", ToNodeID: "n_patient", Label: "subject_Patient", SourceResourceType: "Condition", TargetResourceType: "Patient", StorageDirection: "OUTBOUND"},
	)
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_names", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient names",
		LogicalType: "HumanName", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
	})
	return capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
}

func expandCompileSnapshot(uriLogicalType string) capability.Snapshot {
	base := fixtureSnapshot()
	return capability.NewSnapshot(base.Identity, base.Policy, capability.StatusReady, true, false,
		[]capability.Node{{ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "procedure", Populated: true}},
		nil,
		[]capability.Candidate{
			{ID: "c_procedure_id", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
			{ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "instantiatesUri[]", Label: "Instantiates uri", LogicalType: uriLogicalType, Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}, RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "instantiatesUri[]", MaxItems: 2}}},
			{ID: "c_procedure_use", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "identifier.use", Label: "Identifier use", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		}, nil)
}

func expandCompileDocument(projection ws.ConstructionSourceProjection, payload *ws.ConstructionExpandedFieldSource, steps []ws.ConstructionStep) ws.Document {
	return ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "procedures", Title: "Procedures"}, RootResourceType: "Procedure",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Procedure"},
		Columns: []ws.Column{{
			ColumnID: "procedure-id", Column: "procedure_id", Label: "Procedure ID",
			LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps:             steps,
		},
	}
}

func expandCompileSignedProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "expand_uri", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
}

func expandCompileSignedPayload() *ws.ConstructionExpandedFieldSource {
	return &ws.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "string"}
}

func expandCompileStep(payload *ws.ConstructionExpandedFieldSource) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "expand_uri",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationExpand, Expand: &ws.ConstructionExpand{
			ConstructionID: "expand_uri", InputColumnID: "uri_column", OutputColumnID: "uri_item",
			EmptyPolicy:   ws.ConstructionExpandEmptyExclude,
			ExpandedField: payload,
		}},

		Outputs: []ws.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
		},
	}
}

func expandCompileLower(t *testing.T, document ws.Document, snapshot capability.Snapshot) facade.CompiledRecipe {
	t.Helper()
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("BUG-002/004: compile signed expand state: %v", err)
	}
	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("resolve plan: %v", err)
	}
	lowered, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return lowered
}

func expandCompileError(err error) string {
	var compileErr *Error
	if errors.As(err, &compileErr) {
		return compileErr.Code
	}
	return ""
}

func TestCompileConstructionExpandSignedArrayProjectionCompilesWithManyCardinality(t *testing.T) {
	lowered := expandCompileLower(t,
		expandCompileDocument(expandCompileSignedProjection(), expandCompileSignedPayload(),
			[]ws.ConstructionStep{expandCompileStep(expandCompileSignedPayload())}),
		expandCompileSnapshot("string"))
	if len(lowered.Outputs) != 1 {
		t.Fatalf("lowered outputs = %d, want one", len(lowered.Outputs))
	}
	output := lowered.Outputs[0]
	slotName := ws.ConstructionSourceProjectionName("uri_column")
	var sourceCardinality string
	for _, stage := range output.Stages {
		if stage.ID == recipe.ConstructionSourceProjectionID {
			for _, column := range stage.Columns {
				if column.Name == slotName {
					sourceCardinality = string(column.Cardinality)
				}
			}
		}
	}
	if sourceCardinality != "many" {
		t.Fatalf("compiled root array column %q has cardinality %q, want many for the physical EXPAND gate", slotName, sourceCardinality)
	}
	expandStageSeen := false
	for _, stage := range output.Stages {
		if stage.Operation == string(recipe.ConstructionExpandOp) {
			expandStageSeen = true
		}
	}
	if !expandStageSeen {
		t.Fatalf("compiled pipeline lost the EXPAND stage: %#v", output.Stages)
	}
}

func TestCompileConstructionExpandProjectionWithoutOwnerIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.OwnerStepID = ""
	document := expandCompileDocument(projection, expandCompileSignedPayload(),
		[]ws.ConstructionStep{expandCompileStep(expandCompileSignedPayload())})
	_, err := Compile(context.Background(), "project-a", "explorer-a", document, expandCompileSnapshot("string"))
	if err == nil || expandCompileError(err) != "INVALID_DOCUMENT" {
		t.Fatalf("unowned expand projection error = %v, want INVALID_DOCUMENT from the authoring owner-consumption wall", err)
	}
	if !strings.Contains(err.Error(), "unowned source projections are supported only by a direct-source GROUP step") {
		t.Fatalf("unowned expand projection error = %v, want the explicit unowned-state refusal", err)
	}
}

func TestCompileConstructionExpandPayloadClaimingScalarAsManyIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.FieldPath = "status"
	projection.FHIRType = "string"
	projection.LogicalType = "code"
	payload := &ws.ConstructionExpandedFieldSource{Path: "status", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
	step := expandCompileStep(payload)
	step.Outputs[1].Type = "code"
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		expandCompileSnapshotWithStatus("string"))
	if err == nil || expandCompileError(err) != "STALE_CONSTRUCTION_SOURCE_FIELD" {
		t.Fatalf("scalar-as-many forged payload error = %v, want the signed-array candidate-class refusal (snapshot backing is a scalar VALUE candidate)", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Details["fieldPath"] != "status" {
		t.Fatalf("refusal must name the forged field path: %#v", err)
	}
}

func TestCompileConstructionExpandSnapshotForgedArrayBackingIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.FieldPath = "status"
	projection.FHIRType = "string"
	projection.LogicalType = "code"
	payload := &ws.ConstructionExpandedFieldSource{Path: "status", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
	step := expandCompileStep(payload)
	step.Outputs[1].Type = "code"
	snapshot := expandCompileSnapshotWithStatus("string")
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].FieldPath == "status" {
			snapshot.Candidates[index].Cardinality = "many"
			snapshot.Candidates[index].ProjectionModes = []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}
			snapshot.Candidates[index].RepeatedBoundaries = []capability.RepeatedBoundary{{Path: "status", MaxItems: 2}}
		}
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		snapshot)
	if err == nil || expandCompileError(err) != "STALE_EXPANDED_SOURCE_SCHEMA" {
		t.Fatalf("snapshot-forged array backing error = %v, want STALE_EXPANDED_SOURCE_SCHEMA from the generated-schema verifier", err)
	}
}

func expandCompileSnapshotWithStatus(uriLogicalType string) capability.Snapshot {
	base := expandCompileSnapshot(uriLogicalType)
	for index := range base.Candidates {
		if base.Candidates[index].FieldPath == "identifier.use" {
			base.Candidates[index] = capability.Candidate{ID: "c_procedure_status", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "status", Label: "Status", LogicalType: "code", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
		}
	}
	return base
}

func TestCompileConstructionExpandObjectKindItemsAreRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.LogicalType = "object"
	payload := &ws.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "object"}
	step := expandCompileStep(payload)
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		expandCompileSnapshot("object"))
	if err == nil || expandCompileError(err) != "UNSUPPORTED_EXPANDED_SOURCE_KIND" {
		t.Fatalf("BUG-002: object-kind items error = %v, want UNSUPPORTED_EXPANDED_SOURCE_KIND", err)
	}
}

func TestCompileConstructionExpandStyledWrongOwnerProjectionRefused(t *testing.T) {
	projection := ws.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "derive_uri_copy", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
	derive := ws.ConstructionStep{
		ID:     "derive_uri_copy",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},

		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID: "derive_uri_copy", OutputColumnID: "uri_copy", Operation: ws.ConstructionDerivedAdd,
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "uri_column"},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: expandCompileInt(1)}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_column", Name: ws.ConstructionSourceProjectionName("uri_column"), Label: "Instantiates uri", Type: "string"},
			{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		},
	}
	trailingExpand := expandCompileStep(expandCompileSignedPayload())
	trailingExpand.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_uri_copy"}}
	trailingExpand.Outputs = []ws.StageColumn{
		{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
		{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, expandCompileSignedPayload(),
			[]ws.ConstructionStep{derive, trailingExpand}),
		expandCompileSnapshot("string"))
	if err == nil || expandCompileError(err) != "STALE_CONSTRUCTION_SOURCE_FIELD" {
		t.Fatalf("wrong-owner expand-styled projection error = %v, want the scalar-lane VALUE-backing candidate refusal (proves non-credit)", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Details["fieldPath"] != "instantiatesUri[]" {
		t.Fatalf("refusal should name the forged field path in its details: %#v", err)
	}
	if !strings.Contains(refusal.Path, "sourceProjections[0]") {
		t.Fatalf("refusal should locate the forged projection slot: %q", refusal.Path)
	}
}

func expandCompileInt(value int64) *int64 { return &value }

func TestImportedRecipeCompilationPreservesSourceOutputAndUsesStructuralSchema(t *testing.T) {
	output := recipe.Output{Name: "GroupMember", RootResourceType: "Group", RowGrain: "GROUP_MEMBER", Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.member[]"}, As: "member"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Call: "uuid5", Args: []recipe.Expression{{Literal: json.RawMessage(`"stable namespace"`)}, {Select: "root.id"}, {Select: "member.entity.reference"}}}}, Fields: []recipe.Field{{Name: "member_id", Expr: recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "member.entity.reference"}}}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	document := ws.Document{Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Group members"}, RootResourceType: output.RootResourceType, Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: recipeimport.ImportRows(output), ImportedRecipe: &ref, Columns: []ws.Column{{ColumnID: "member_id_source", Column: "member_id", Label: "Member", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: "member_id"}}, Table: &ws.TablePresentation{Visible: &visible}}}}
	resolvedOutput := output
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Outputs: []recipe.Output{resolvedOutput}}, SourceColumns: []ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "member_id_source", Name: "member_id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "member.entity.reference"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, ws.CatalogSnapshot{}, capability.Snapshot{}, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if got.Name != document.Output.ID || got.Identity == nil || got.Identity.Name != output.Identity.Name || got.Identity.Expr.Call != "uuid5" || len(got.Fields) != 1 || got.Fields[0].Name != output.Fields[0].Name {
		t.Fatalf("imported source operations or identity changed: %#v", got)
	}
	if got.Construction == nil || len(got.Construction.SourceColumns) != 1 || got.Construction.SourceColumns[0].ID != "member_id_source" || got.Construction.SourceColumns[0].Name != "member_id" {
		t.Fatalf("legacy source column did not receive stable construction identity: %#v", got.Construction)
	}
	if got.Fields[0].ColumnID != output.Fields[0].ColumnID || got.Expand.From.Select != output.Expand.From.Select {
		t.Fatalf("source recipe field or expansion changed: %#v", got)
	}
	if compiled.OutputContract.Lossless || compiled.OutputContract.MLReady || len(compiled.OutputContract.LossReasons) == 0 || compiled.OutputContract.Columns[0].LogicalType != "string" || compiled.OutputContract.Columns[0].Filterable || compiled.OutputContract.Columns[0].Chartable {
		t.Fatalf("unsafe imported public assessment: %#v", compiled.OutputContract)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeRejectsStalePinnedSourceAndEditedInputs(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD"}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	workspace := ws.Workspace{Documents: []ws.Document{{ImportedRecipe: &ref, Rows: ws.RecordsRowDefinition()}}, SemanticsVersion: ws.CurrentSemanticsVersion}
	input := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle}
	if err := validateResolvedImportedRecipeCoverage(workspace, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err != nil {
		t.Fatalf("valid pin rejected: %v", err)
	}
	input.Bundle.Outputs[0].RowGrain = "CHANGED"
	if err := validateResolvedImportedRecipeCoverage(workspace, ResolvedInputs{ImportedRecipes: []ResolvedImportedRecipe{input}}); err == nil {
		t.Fatal("stale pinned source accepted")
	}
}

func TestImportedRecipePreservesConstructionOverlay(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD", Fields: []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	column := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	minimum := int64(18)
	document := ws.Document{Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType, Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType}, Rows: ws.RecordsRowDefinition(), ImportedRecipe: &ref, Columns: []ws.Column{{ColumnID: column.ID, Column: column.Name, Label: column.Label, OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: column.Name}}, Table: &ws.TablePresentation{Visible: &visible}}}, Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{ID: "adults", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: column.ID, Operator: ws.ConstructionFilterGreaterEq, Values: []ws.FilterValue{{Kind: ws.ConstructionFilterInteger, Integer: &minimum}}}}, Outputs: []ws.StageColumn{{ID: column.ID, Name: column.Name, Label: column.Label, Type: "integer", Table: &ws.TablePresentation{Visible: &visible}}}}}}}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: column, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, ws.CatalogSnapshot{}, capability.Snapshot{}, resolved)
	if err != nil {
		t.Fatalf("supported construction overlay rejected: %v", err)
	}
	if compiled.Bundle.Outputs[0].Construction == nil {
		t.Fatal("construction overlay was dropped")
	}
	if compiled.Bundle.Outputs[0].Construction.Steps[0].Operation.Kind != recipe.ConstructionFilterOp {
		t.Fatalf("filter overlay operation = %#v", compiled.Bundle.Outputs[0].Construction.Steps[0].Operation)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("translated imported filter bundle is invalid: %v", err)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("filtered imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeCompilesAdditiveRootColumn(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD", Fields: []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType,
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_birth_date", Column: "birthDate", Label: "Birth date", LogicalType: "date", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_given_all", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "ALL"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	catalog := ws.CatalogSnapshot{
		Nodes: []ws.CatalogNode{{ID: "node-patient", ResourceType: "Patient"}},
		Candidates: []ws.CatalogCandidate{
			{ID: "candidate-birth-date", NodeID: "node-patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"},
			{ID: "candidate-given", NodeID: "node-patient", FieldPath: "name[].given[]", Label: "Given", LogicalType: "string", Cardinality: "many", ProjectionModes: []string{"FIRST", "ALL", "DISTINCT"}, DefaultProjectionMode: "ALL"},
		},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes: []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true}},
		Candidates: []capability.Candidate{
			{ID: "candidate-birth-date", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
			{ID: "candidate-given", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "name[].given[]", Label: "Given", LogicalType: "string", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}},
		},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, catalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if len(got.Fields) != 3 || got.Fields[1].Name != "birthDate" || got.Fields[1].Expr.Select != "root.birthDate" || got.Fields[1].ColumnID != "additive_birth_date" {
		t.Fatalf("additive root field was not compiled into the imported output: %#v", got.Fields)
	}
	if got.Fields[2].ValueMode != recipe.ValueModeAll || compiled.EmittedColumns[2].Shape != "array" {
		t.Fatalf("additive repeated projection lost authored ALL intent: field=%#v emitted=%#v", got.Fields[2], compiled.EmittedColumns[2])
	}
	if len(compiled.OutputContract.Columns) != 3 || compiled.OutputContract.Columns[1].Column != "birthDate" || compiled.OutputContract.Columns[2].Shape != "array" {
		t.Fatalf("additive root column missing from output contract: %#v", compiled.OutputContract.Columns)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("additive imported bundle is invalid: %v", err)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("additive imported output contract does not match emitted schema: %v", err)
	}
}

func TestImportedRecipeCompilesAdditiveRelatedColumn(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD",
		Fields:     []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}},
		Traversals: []recipe.Traversal{{Name: "subject", OccurrenceID: "rel_obs", ToResourceType: "Observation", MatchMode: recipe.MatchOptional}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: output.RootResourceType,
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "rel_obs", ResourceType: "Observation", Relationship: "subject", CatalogEdgeID: "e-obs", MatchMode: ws.RouteMatchOptional}}},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_status", Column: "status", Label: "Observation status", LogicalType: "string", OccurrenceID: "rel_obs", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	wireCatalog := ws.CatalogSnapshot{
		Nodes:      []ws.CatalogNode{{ID: "n-patient", ResourceType: "Patient"}, {ID: "n-obs", ResourceType: "Observation"}},
		Edges:      []ws.CatalogEdge{{ID: "e-obs", FromNodeID: "n-patient", ToNodeID: "n-obs", Label: "subject"}},
		Candidates: []ws.CatalogCandidate{{ID: "candidate-status", NodeID: "n-obs", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}}},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes:      []capability.Node{{ID: "n-patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "n-obs", ResourceType: "Observation"}},
		Edges:      []capability.Edge{{ID: "e-obs", FromNodeID: "n-patient", ToNodeID: "n-obs", Label: "subject", SourceResourceType: "Patient", TargetResourceType: "Observation"}},
		Candidates: []capability.Candidate{{ID: "candidate-status", NodeID: "n-obs", ResourceType: "Observation", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	got := compiled.Bundle.Outputs[0]
	if len(got.Traversals) != 1 || len(got.Traversals[0].Fields) != 1 {
		t.Fatalf("related additive field was not merged into the imported traversal: %#v", got.Traversals)
	}
	field := got.Traversals[0].Fields[0]
	if field.Name != "status" || field.Expr.Select != "subject.status" || field.ColumnID != "additive_status" {
		t.Fatalf("related additive field selector/identity = %#v", field)
	}
	if emitted := compiled.EmittedColumns[len(compiled.EmittedColumns)-1]; emitted.OccurrenceID != "rel_obs" || emitted.SourcePath != "status" {
		t.Fatalf("related additive emission = %#v", emitted)
	}
	if err := compiled.Bundle.Validate(); err != nil {
		t.Fatalf("related additive imported bundle is invalid: %v", err)
	}
	again, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("second compileImportedDocument: %v", err)
	}
	if fields := again.Bundle.Outputs[0].Traversals[0].Fields; len(fields) != 1 {
		t.Fatalf("resolved bundle was mutated across compiles: %#v", fields)
	}
}

func TestImportedRecipeAdditiveRelatedColumnToleratesUnobservedOptionalEdge(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "RECORD",
		Fields:     []recipe.Field{{Name: "age", Expr: recipe.Expression{Select: "root.age"}}},
		Traversals: []recipe.Traversal{{Name: "subject", OccurrenceID: "rel_obs", ToResourceType: "Observation", MatchMode: recipe.MatchOptional}},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name}
	visible := true
	importedColumn := recipe.StageColumn{ID: "resolved_age_id", Name: "age", Label: "Age", Type: "integer"}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: output.Name, Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "rel_obs", ResourceType: "Observation", Relationship: "subject", MatchMode: ws.RouteMatchOptional}}},
		Rows:  ws.RecordsRowDefinition(), ImportedRecipe: &ref,
		Columns: []ws.Column{
			{ColumnID: importedColumn.ID, Column: importedColumn.Name, Label: importedColumn.Label, LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: importedColumn.Name}}, Table: &ws.TablePresentation{Visible: &visible}},
			{ColumnID: "additive_status", Column: "status", Label: "Observation status", LogicalType: "string", OccurrenceID: "rel_obs", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
		},
	}
	wireCatalog := ws.CatalogSnapshot{
		Nodes:      []ws.CatalogNode{{ID: "n-patient", ResourceType: "Patient"}, {ID: "n-obs", ResourceType: "Observation"}},
		Candidates: []ws.CatalogCandidate{{ID: "candidate-status", NodeID: "n-obs", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}}},
	}
	capabilitySnapshot := capability.Snapshot{
		Status: capability.StatusReady, Complete: true,
		Nodes:      []capability.Node{{ID: "n-patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "n-obs", ResourceType: "Observation"}},
		Candidates: []capability.Candidate{{ID: "candidate-status", NodeID: "n-obs", ResourceType: "Observation", FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}},
	}
	resolved := ResolvedImportedRecipe{Reference: ref, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []ResolvedImportedSourceColumn{{Column: importedColumn, Cardinality: "required_one", Shape: "scalar", SemanticPath: "age"}}}
	compiled, err := compileImportedDocument(context.Background(), "project", "explorer", document, wireCatalog, capabilitySnapshot, resolved)
	if err != nil {
		t.Fatalf("compileImportedDocument: %v", err)
	}
	if len(compiled.Bundle.Outputs[0].Traversals) != 1 || len(compiled.Bundle.Outputs[0].Traversals[0].Fields) != 1 {
		t.Fatalf("additive related field was not compiled: %#v", compiled.Bundle.Outputs[0].Traversals)
	}
}

func TestCompileMapsStandaloneCombineIntoRecipeWithoutSourceProjection(t *testing.T) {
	document := ws.Document{
		Kind:             ws.Kind,
		Output:           ws.Output{ID: "training", Title: "Training table"},
		RootResourceType: "Patient",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RowDefinition{
			Kind: ws.RowDefinitionRecords, Records: &ws.RecordRows{},
		},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "join_labs",
			Inputs: []ws.ConstructionInputRef{
				{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
				{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
			},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
				Kind: ws.ConstructionCombineKeyJoin,
				Keys: []ws.ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
				Projections: []ws.ConstructionCombineProjection{
					{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
					{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
				},
				JoinType: ws.ConstructionCombineLeftJoin, RightMatchPolicy: ws.ConstructionCombinePreserveAllMatches,
			}},
			Outputs: []ws.StageColumn{
				{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
				{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
			},
		}}},
	}

	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatalf("compile terminal Combine: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Bundle.Outputs))
	}
	output := compiled.Bundle.Outputs[0]
	if len(output.Fields) != 0 {
		t.Fatalf("terminal Combine recipe declared source fields: %#v", output.Fields)
	}
	construction := output.Construction
	if construction == nil || len(construction.SourceColumns) != 0 || len(construction.Steps) != 1 {
		t.Fatalf("compiled construction = %#v, want one step without a source projection", construction)
	}
	if err := construction.Validate(output.Fields); err != nil {
		t.Fatalf("mapped construction failed recipe validation: %v", err)
	}
	step := construction.Steps[0]
	if step.Operation.Combine == nil || step.Operation.Combine.Kind != recipe.ConstructionCombineKeyJoin {
		t.Fatalf("compiled operation = %#v, want key join", step.Operation)
	}
	if len(step.Inputs) != 2 || step.Inputs[0].RevisionID != "patients-r4" || step.Inputs[1].RevisionID != "labs-r9" {
		t.Fatalf("compiled exact inputs = %#v", step.Inputs)
	}
	if !step.Outputs[1].Nullable {
		t.Fatalf("right-side left-join output lost nullability: %#v", step.Outputs[1])
	}
}
