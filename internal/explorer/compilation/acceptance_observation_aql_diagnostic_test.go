package compilation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/arangodb/go-driver/v2/utils"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestAcceptanceObservationContributorAQLAndBinds(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "acceptance", "ncpi-tcga-brca", "workspace.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(raw)
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
	catalog := catalogFromCapability(snapshot, "acceptance")
	workspace.Documents = []authoringv2.Document{document}
	migrated, err := authoringv2.MigrateLegacyContributors(workspace, catalog)
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
		if predicate == nil || predicate.Operator != authoringv2.ContributorEquals || predicate.Quantifier != authoringv2.ContributorAny || predicate.Value == nil || predicate.Value.Code == nil || predicate.Value.Code.Code != wantCode {
			t.Fatalf("fixture column %q migrated contributor = %#v, want ANY CODE EQUALS %q", column.Column, predicate, wantCode)
		}
	}
	compiled, err := Compile(context.Background(), "acceptance", "acceptance", document, snapshot)
	if err != nil {
		t.Fatalf("compile locked fixture contributor columns: %v", err)
	}
	runtimeBindings := recipe.RuntimeBindings{Project: "acceptance", DatasetGeneration: "tcga-brca-locked"}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, runtimeBindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "acceptance-scope", "tcga-brca-locked")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Outputs) != 1 {
		t.Fatalf("physical outputs = %d, want 1", len(physical.Outputs))
	}
	rendered, err := aql.RenderPhysicalPlan(physical.Outputs[0].Plan)
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
	arangoDatabase := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DB"))
	if arangoURL == "" && arangoDatabase == "" {
		return
	}
	if arangoURL == "" || arangoDatabase == "" {
		t.Fatal("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DB must be set together")
	}
	executeAcceptanceObservationAQL(t, arangoURL, arangoDatabase, physical.Outputs[0], runtimeBindings, rendered.Query, rendered.BindVars)
}

func executeAcceptanceObservationAQL(
	t *testing.T,
	endpoint, database string,
	output lower.CompiledRecipeOutput,
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
		// This deliberately inconsistent stored edge models the case scalar traversal rejects:
		// its endpoint is a Specimen, but its discriminator claims Condition.
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

	// Compare the direct renderer output with the canonical execution compiler
	// and the selected-root page template used by production preview streams.
	rawRows := execute("direct-render", query, cloneBinds(bindVars))
	expectValues("direct-render", rawRows[0])
	previewBindings := bindings.Clone()
	previewBindings.PreviewLimit = 25
	full, err := compiler.CompileRecipeOutputWithPolicy(output, previewBindings, 25, ir.DefaultPhysicalOptimizationPolicy())
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
	page, err := compiler.CompileRecipeOutputPageWithPolicy(output, previewBindings, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile production selected-root page: %v", err)
	}
	pageRowsBindLog, err := json.Marshal(page.RowsBindVars)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production selected-root page rows AQL:\n%s\nBINDVARS: %s", page.RowsQuery, pageRowsBindLog)
	keyBinds := cloneBinds(page.RootKeysBindVars)
	keyBinds[compiler.RootPageAfterKeyBind] = ""
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
	pageBinds[compiler.RootPageKeysBind] = rootKeys
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
