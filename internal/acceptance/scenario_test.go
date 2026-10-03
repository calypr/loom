package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/generated/loomapi"
	dfpublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestAcceptanceWorkspaceDefinesPatientCohortContract(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/acceptance/ncpi-tcga-brca/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.ValidateForPublication(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"patient_id", "submitter_id", "birth_sex", "race", "ethnicity",
		"primary_histology", "pathological_stage", "age_at_diagnosis_days", "diagnostic_method", "days_to_death",
		"condition_count", "specimen_count", "tumor_specimen_count", "normal_specimen_count", "has_paired_tumor_normal",
		"earliest_collection_day", "latest_collection_day",
	}
	if len(workspace.Documents) != 1 || len(workspace.Documents[0].Columns) != len(want) {
		t.Fatalf("workspace documents/columns = %d/%d", len(workspace.Documents), len(workspace.Documents[0].Columns))
	}
	for index, column := range workspace.Documents[0].Columns {
		if column.Column != want[index] {
			t.Fatalf("column[%d] = %q, want %q", index, column.Column, want[index])
		}
	}
	route := workspace.Documents[0].Route
	if len(route.Children) != 3 || route.Children[1].OccurrenceID != "specimen" || len(route.Children[1].Children) != 1 || route.Children[1].Children[0].Relationship != "specimen_Specimen" {
		t.Fatalf("workspace lost patient-to-specimen-to-observation traversal: %#v", route)
	}
}

func TestAcceptanceWorkspaceMigratesLegacyContributorPredicatesAgainstPinnedCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/acceptance/ncpi-tcga-brca/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SemanticsVersion != 3 || !hasLegacyAggregateWhere(legacy) {
		t.Fatalf("fixture legacy contract = semanticsVersion %d, nested where=%t", legacy.SemanticsVersion, hasLegacyAggregateWhere(legacy))
	}

	catalog := acceptanceMigrationTestCatalog(t)
	migrated, err := migrateLegacyAcceptanceWorkspace(legacy, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
		t.Fatalf("migrated semanticsVersion = %d, want %d", migrated.SemanticsVersion, authoringv2.CurrentSemanticsVersion)
	}
	if got := countWorkspaceColumns(migrated); got != 17 {
		t.Fatalf("migrated column count = %d, want 17", got)
	}
	if got := countContributorPredicates(migrated); got != 7 {
		t.Fatalf("migrated contributor predicate count = %d, want 7", got)
	}
	for index, before := range legacy.Documents[0].Columns {
		if after := migrated.Documents[0].Columns[index]; after.Column != before.Column {
			t.Fatalf("column[%d] = %q after migration, want %q", index, after.Column, before.Column)
		}
	}

	want := map[string]struct {
		candidateID  string
		resourceType string
		path         string
		value        string
		operation    string
	}{
		"age_at_diagnosis_days":   {"observation-code", "Observation", "code.coding[].code", "NCIT_C156418", "MIN"},
		"diagnostic_method":       {"observation-code", "Observation", "code.coding[].code", "NCIT_C177576", "MIN"},
		"days_to_death":           {"observation-code", "Observation", "code.coding[].code", "NCIT_C156419", "MIN"},
		"tumor_specimen_count":    {"specimen-code", "Specimen", "type.coding[].code", "Tumor", "COUNT"},
		"normal_specimen_count":   {"specimen-code", "Specimen", "type.coding[].code", "Normal", "COUNT"},
		"earliest_collection_day": {"observation-code", "Observation", "code.coding[].code", "81247-9", "MIN"},
		"latest_collection_day":   {"observation-code", "Observation", "code.coding[].code", "81247-9", "MAX"},
	}
	for _, column := range migrated.Documents[0].Columns {
		expected, hasPredicate := want[column.Column]
		if !hasPredicate {
			if column.Contributor != nil {
				t.Errorf("non-predicate column %q gained a Contributor", column.Column)
			}
			continue
		}
		if column.Source.Aggregate == nil || column.Source.Aggregate.Where != nil || column.Source.Aggregate.Operation != expected.operation {
			t.Errorf("column %q aggregate after migration = %#v", column.Column, column.Source.Aggregate)
			continue
		}
		predicate := column.Contributor
		if predicate == nil || predicate.CandidateID != expected.candidateID || predicate.Operator != authoringv2.ContributorEquals || predicate.Quantifier != authoringv2.ContributorAny || predicate.Value == nil || predicate.Value.Kind != authoringv2.ContributorValueCode || predicate.Value.Code == nil || predicate.Value.Code.Code != expected.value {
			t.Errorf("column %q Contributor = %#v, want candidate %q, ANY code equality %q", column.Column, predicate, expected.candidateID, expected.value)
			continue
		}
		candidate := catalogCandidateByID(t, catalog, predicate.CandidateID)
		if candidate.FieldPath != expected.path {
			t.Errorf("column %q candidate path = %q, want %q", column.Column, candidate.FieldPath, expected.path)
		}
		if resourceType := catalogNodeByID(t, catalog, candidate.NodeID).ResourceType; resourceType != expected.resourceType {
			t.Errorf("column %q candidate resource type = %q, want %q", column.Column, resourceType, expected.resourceType)
		}
		delete(want, column.Column)
	}
	if len(want) != 0 {
		t.Fatalf("legacy predicate columns were not migrated: %v", want)
	}

	canonical, err := migrated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var publishRequest loomapi.PublishRepositoryExplorerConfigJSONRequestBody
	if err := json.Unmarshal(canonical, &publishRequest); err != nil {
		t.Fatalf("strict repository publish request decoder rejected migrated workspace: %v", err)
	}
	if countContributorPredicates(publishRequest) != 7 {
		t.Fatalf("strict publish request Contributor predicate count = %d, want 7", countContributorPredicates(publishRequest))
	}
	reloaded, err := authoringv2.DecodeWorkspace(canonical)
	if err != nil {
		t.Fatalf("current request workspace did not decode: %v", err)
	}
	if err := reloaded.ValidateForPublication(); err != nil {
		t.Fatalf("current request workspace is not publishable: %v", err)
	}
	if got := countContributorPredicates(reloaded); got != 7 {
		t.Fatalf("reloaded Contributor predicate count = %d, want 7", got)
	}
}

func TestDecodeAcceptanceCatalogBindsGenerationAndRequestScope(t *testing.T) {
	catalog := acceptanceMigrationTestCatalog(t)
	rawCatalog, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var publicCatalog map[string]any
	if err := json.Unmarshal(rawCatalog, &publicCatalog); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"apiVersion": authoringv2.APIVersion,
		"kind":       authoringv2.StateKind,
		"catalog":    publicCatalog,
	}

	decoded, err := decodeAcceptanceCatalog(state, "NCPI_ACCEPTANCE", "acceptance-catalog-test", "tcga-brca-locked")
	if err != nil {
		t.Fatalf("decode current pinned catalog: %v", err)
	}
	if decoded.Project != "NCPI_ACCEPTANCE" || decoded.ExplorerID != "acceptance-catalog-test" || decoded.SourceGeneration != "tcga-brca-locked" || decoded.SnapshotToken != catalog.SnapshotToken {
		t.Fatalf("decoded catalog scope = project %q explorer %q generation %q token %q", decoded.Project, decoded.ExplorerID, decoded.SourceGeneration, decoded.SnapshotToken)
	}

	if _, err := decodeAcceptanceCatalog(state, "NCPI_ACCEPTANCE", "acceptance-catalog-test", "other-generation"); err == nil || !strings.Contains(err.Error(), "does not match acceptance generation") {
		t.Fatalf("wrong-generation catalog error = %v", err)
	}
}

func TestMigrateLegacyWorkspaceForOldAPIPreservesExactBytes(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/acceptance/ncpi-tcga-brca/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	capabilityRequests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capabilityRequests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects/acceptance-project/explorers/default/authoring/v2/capability" {
			t.Errorf("old API probe = %s %s, want scoped capability GET", r.Method, r.URL.Path)
		}
		http.Error(w, "authoring v2 unavailable", http.StatusNotFound)
	})

	cfg := ScenarioConfig{
		Connections: Connections{LoomURL: "http://acceptance.test"},
		Namespace:   Namespace{Run: "old-api-run", Project: "acceptance-project", Generation: "fixture-generation"},
	}
	client := acceptanceTestHTTPClient(handler)
	body, migration, err := migrateLegacyWorkspaceForCurrentAPI(context.Background(), cfg, client, raw)
	if err != nil {
		t.Fatal(err)
	}
	if migration != nil {
		t.Fatalf("legacy API unexpectedly reported migration: %#v", migration)
	}
	if !bytes.Equal(body, raw) {
		t.Fatal("old API path changed the workspace bytes")
	}
	if capabilityRequests != 1 {
		t.Fatalf("capability requests = %d, want 1", capabilityRequests)
	}
}

func TestMigrateLegacyWorkspaceForCurrentAPIUsesScopedCatalogAndReusesPublishBody(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/acceptance/ncpi-tcga-brca/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	const project = "acceptance-project"
	const explorerID = "verifier-owned-explorer"
	const generation = "fixture-generation"
	var paths []string
	var publishBodies [][]byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/"+project+"/explorers/default/authoring/v2/capability":
			writeAcceptanceTestJSON(w, map[string]any{
				"apiVersion": authoringv2.APIVersion,
				"kind":       "ExplorerAuthoringCapabilities",
				"operations": []string{"builder", "publish", "commands"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/"+project+"/explorers":
			var request struct {
				Name  string `json:"name"`
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode verifier Explorer creation: %v", err)
			}
			if !strings.HasPrefix(request.Name, "Acceptance catalog migration ") || request.Title != request.Name {
				t.Errorf("verifier Explorer request = %#v", request)
			}
			writeAcceptanceTestJSON(w, map[string]string{"explorerId": explorerID})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/"+project+"/explorers/"+explorerID+"/authoring/v2/builder":
			writeAcceptanceTestJSON(w, acceptanceMigrationPublicBuilderState(generation, true, false, false, false))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/"+project+"/generations/"+generation+"/explorer-config":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read publish request: %v", err)
			}
			publishBodies = append(publishBodies, body)
			writeAcceptanceTestJSON(w, map[string]string{
				"project": project, "generation": generation, "executionId": "execution-1",
				"recipe": "cohort", "translationVersion": "test", "output": "cohort",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cfg := ScenarioConfig{
		Connections:  Connections{LoomURL: "http://acceptance.test"},
		Namespace:    Namespace{Run: "current-api-run", Project: project, Generation: generation},
		SourceCommit: "test-commit",
	}
	client := acceptanceTestHTTPClient(handler)
	migratedBody, details, err := migrateLegacyWorkspaceForCurrentAPI(context.Background(), cfg, client, raw)
	if err != nil {
		t.Fatal(err)
	}
	if details["column_count"] != 17 || details["predicate_count"] != 7 || details["source_generation"] != generation || details["catalog_complete"] != true {
		t.Fatalf("migration report = %#v", details)
	}
	decoded, err := authoringv2.DecodeWorkspace(migratedBody)
	if err != nil {
		t.Fatalf("decode migrated body: %v", err)
	}
	if got := countContributorPredicates(decoded); got != 7 {
		t.Fatalf("migrated body predicates = %d, want 7", got)
	}

	// Match RunScenario's idempotency path: publish the one selected body twice.
	for range 2 {
		if _, err := publishWorkspace(context.Background(), cfg, client, migratedBody); err != nil {
			t.Fatal(err)
		}
	}
	wantPaths := []string{
		"GET /api/v1/projects/" + project + "/explorers/default/authoring/v2/capability",
		"POST /api/v1/projects/" + project + "/explorers",
		"GET /api/v1/projects/" + project + "/explorers/" + explorerID + "/authoring/v2/builder",
		"POST /api/v1/projects/" + project + "/generations/" + generation + "/explorer-config",
		"POST /api/v1/projects/" + project + "/generations/" + generation + "/explorer-config",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("request sequence = %#v, want %#v", paths, wantPaths)
	}
	if len(publishBodies) != 2 || !bytes.Equal(publishBodies[0], migratedBody) || !bytes.Equal(publishBodies[1], migratedBody) {
		t.Fatalf("published request bodies do not both equal the migrated body: count=%d", len(publishBodies))
	}
}

func TestMigrateLegacyWorkspaceRejectsTruncatedOrMalformedBuilderCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/acceptance/ncpi-tcga-brca/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name             string
		complete         bool
		truncated        bool
		missingNode      bool
		negativeBoundary bool
		wantError        string
	}{
		{name: "incomplete", complete: false, wantError: "incomplete or truncated"},
		{name: "truncated", complete: true, truncated: true, wantError: "incomplete or truncated"},
		{name: "candidate references missing node", complete: true, missingNode: true, wantError: "references a missing node"},
		{name: "negative repeated maxItems", complete: true, negativeBoundary: true, wantError: "invalid repeated boundary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var paths []string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/authoring/v2/capability"):
					writeAcceptanceTestJSON(w, map[string]any{
						"apiVersion": authoringv2.APIVersion,
						"kind":       "ExplorerAuthoringCapabilities",
						"operations": []string{"builder"},
					})
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/explorers"):
					writeAcceptanceTestJSON(w, map[string]string{"explorerId": "catalog-explorer"})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/authoring/v2/builder"):
					writeAcceptanceTestJSON(w, acceptanceMigrationPublicBuilderState("fixture-generation", test.complete, test.truncated, test.missingNode, test.negativeBoundary))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})

			cfg := ScenarioConfig{
				Connections: Connections{LoomURL: "http://acceptance.test"},
				Namespace:   Namespace{Run: "malformed-catalog-run", Project: "acceptance-project", Generation: "fixture-generation"},
			}
			client := acceptanceTestHTTPClient(handler)
			if _, _, err := migrateLegacyWorkspaceForCurrentAPI(context.Background(), cfg, client, raw); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("catalog migration error = %v, want substring %q", err, test.wantError)
			}
			if len(paths) != 3 || !strings.HasSuffix(paths[2], "/authoring/v2/builder") {
				t.Fatalf("catalog rejection request sequence = %#v", paths)
			}
		})
	}
}

func acceptanceMigrationPublicBuilderState(generation string, complete, truncated, missingNode, negativeBoundary bool) map[string]any {
	observationNodeID := "observation-node"
	specimenNodeID := "specimen-node"
	observationMaxItems := 0
	if negativeBoundary {
		observationMaxItems = -1
	}
	if missingNode {
		observationNodeID = "missing-observation-node"
	}
	return map[string]any{
		"apiVersion": authoringv2.APIVersion,
		"kind":       authoringv2.StateKind,
		"catalog": map[string]any{
			"authorizationScopeDigest": "scope-from-builder",
			"snapshotToken":            "snapshot-from-builder",
			"generation":               generation,
			"complete":                 complete,
			"truncated":                truncated,
			"nodes": []map[string]any{
				{"nodeId": "observation-node", "resourceType": "Observation"},
				{"nodeId": specimenNodeID, "resourceType": "Specimen"},
			},
			"candidates": []map[string]any{
				{
					"candidateId": "observation-code", "nodeId": observationNodeID,
					"fieldPath": "code.coding[].code", "logicalType": "code", "repeated": true,
					"repeatedBoundaries": []map[string]any{{"path": "code.coding[]", "maxItems": observationMaxItems}},
				},
				{
					"candidateId": "specimen-code", "nodeId": specimenNodeID,
					"fieldPath": "type.coding[].code", "logicalType": "code", "repeated": true,
					"repeatedBoundaries": []map[string]any{{"path": "type.coding[]", "maxItems": 4}},
				},
			},
		},
	}
}

func writeAcceptanceTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type acceptanceTestRoundTripper struct {
	handler http.Handler
}

func (transport acceptanceTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func acceptanceTestHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: acceptanceTestRoundTripper{handler: handler}}
}

func acceptanceMigrationTestCatalog(t *testing.T) authoringv2.CatalogSnapshot {
	t.Helper()
	return authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: "NCPI_ACCEPTANCE", ExplorerID: "acceptance-catalog-test",
		SourceGeneration: "tcga-brca-locked", AuthorizationScopeDigest: "acceptance-test-scope",
		SnapshotToken: "acceptance-test-snapshot", Complete: true,
		Nodes: []authoringv2.CatalogNode{
			{ID: "observation-node", ResourceType: "Observation"},
			{ID: "specimen-node", ResourceType: "Specimen"},
		},
		Candidates: []authoringv2.CatalogCandidate{
			{
				ID: "observation-code", NodeID: "observation-node", FieldPath: "code.coding[].code",
				LogicalType: "code", Repeated: true,
				RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 4}},
			},
			{
				ID: "specimen-code", NodeID: "specimen-node", FieldPath: "type.coding[].code",
				LogicalType: "code", Repeated: true,
				RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 4}},
			},
		},
	}
}

func catalogCandidateByID(t *testing.T, catalog authoringv2.CatalogSnapshot, id string) authoringv2.CatalogCandidate {
	t.Helper()
	for _, candidate := range catalog.Candidates {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("catalog candidate %q not found", id)
	return authoringv2.CatalogCandidate{}
}

func catalogNodeByID(t *testing.T, catalog authoringv2.CatalogSnapshot, id string) authoringv2.CatalogNode {
	t.Helper()
	for _, node := range catalog.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("catalog node %q not found", id)
	return authoringv2.CatalogNode{}
}

func TestPublicationOutputRequiresNamedQueryableOutput(t *testing.T) {
	verified := time.Now().UTC()
	execution := dfpublication.BundleExecution{ID: "execution-a", Outputs: []dfpublication.BundleOutputRecord{{
		Name: "cohort", PhysicalTable: "loom_bundle_a_cohort", State: dfpublication.BundlePublished, VerifiedAt: &verified,
	}}}
	output, err := publicationOutput(execution, "cohort")
	if err != nil || output.PhysicalTable != "loom_bundle_a_cohort" {
		t.Fatalf("publication output = %#v, %v", output, err)
	}
	if _, err := publicationOutput(execution, "missing"); err == nil {
		t.Fatal("missing publication output was accepted")
	}
}

func TestPatientColumnPrefersStableIdentity(t *testing.T) {
	materialization := map[string]any{"columns": []any{
		map[string]any{"name": "project_id"},
		map[string]any{"name": "patient_id"},
	}}
	if got := patientColumn(materialization); got != "patient_id" {
		t.Fatalf("patient sort column = %q", got)
	}
}

func TestCompareColumnsRejectsMissingOracleColumn(t *testing.T) {
	columns := []dfpublication.PhysicalColumn{{Name: "patient_id"}}
	if err := compareColumns(columns, []string{"patient_id"}); err != nil {
		t.Fatal(err)
	}
	if err := compareColumns(columns, []string{"patient_id", "diagnosis"}); err == nil {
		t.Fatal("missing oracle column was accepted")
	}
}

func TestCompareGenerationSummaryAcceptsReusedReadyGeneration(t *testing.T) {
	counts := map[string]int{"Patient": 100}
	if err := compareSummary(map[string]any{"reused": true}, counts); err != nil {
		t.Fatalf("reused generation = %v", err)
	}
	if err := compareSummary(map[string]any{}, counts); err == nil {
		t.Fatal("fresh generation without a load summary was accepted")
	}
}

func TestVerifyRowProfilesExplainsCohortDrift(t *testing.T) {
	rows := []any{
		map[string]any{"patient_id": "p1", "stage": "I", "paired": true},
		map[string]any{"patient_id": "p2", "stage": nil, "paired": false},
	}
	oracle := Oracle{NonNullCounts: map[string]int{"patient_id": 2, "stage": 1}, TrueCounts: map[string]int{"paired": 1}}
	if err := verifyRowProfiles(rows, oracle); err != nil {
		t.Fatal(err)
	}
	oracle.NonNullCounts["stage"] = 2
	if err := verifyRowProfiles(rows, oracle); err == nil || !strings.Contains(err.Error(), "stage non-null rows=1 want 2") {
		t.Fatalf("profile mismatch = %v", err)
	}
}

func TestVerifyGraphQLRejectsMalformedRequiredFields(t *testing.T) {
	cases := []struct {
		name          string
		mutateDataset func(map[string]any)
		mutateRows    func(map[string]any)
	}{
		{name: "dataset rowCount missing", mutateDataset: func(dataset map[string]any) { delete(dataset, "rowCount") }},
		{name: "dataset rowCount wrong shape", mutateDataset: func(dataset map[string]any) { dataset["rowCount"] = map[string]any{} }},
		{name: "dataframeRows missing", mutateRows: func(rows map[string]any) { delete(rows, "dataframeRows") }},
		{name: "dataframeRows wrong shape", mutateRows: func(rows map[string]any) { rows["dataframeRows"] = "wrong" }},
		{name: "totalCount missing", mutateRows: func(rows map[string]any) { delete(rows["dataframeRows"].(map[string]any), "totalCount") }},
		{name: "totalCount wrong shape", mutateRows: func(rows map[string]any) { rows["dataframeRows"].(map[string]any)["totalCount"] = map[string]any{} }},
		{name: "rows missing", mutateRows: func(rows map[string]any) { delete(rows["dataframeRows"].(map[string]any), "rows") }},
		{name: "rows wrong shape", mutateRows: func(rows map[string]any) { rows["dataframeRows"].(map[string]any)["rows"] = map[string]any{} }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				response := map[string]any{"data": map[string]any{}}
				switch {
				case strings.Contains(request.Query, "dataframeDataset"):
					response["data"].(map[string]any)["dataframeDataset"] = map[string]any{
						"state": "READY", "rowCount": 1, "columns": []any{},
					}
					if test.mutateDataset != nil {
						test.mutateDataset(response["data"].(map[string]any)["dataframeDataset"].(map[string]any))
					}
				case strings.Contains(request.Query, "dataframeRows"):
					response["data"].(map[string]any)["dataframeRows"] = map[string]any{
						"columns": []any{}, "rows": []any{}, "totalCount": 1,
					}
					if test.mutateRows != nil {
						test.mutateRows(response["data"].(map[string]any))
					}
				default:
					response["data"].(map[string]any)["ok"] = true
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			cfg := ScenarioConfig{Connections: Connections{LoomURL: server.URL}, Namespace: Namespace{Project: "project"}, HTTPClient: &http.Client{}}
			_, err := verifyGraphQL(context.Background(), cfg, publication{Recipe: "recipe", TranslationVersion: "v1", Output: "output"}, Oracle{RowCount: 1, UniquePatientCount: 1})
			if err == nil {
				t.Fatal("malformed GraphQL response was accepted")
			}
		})
	}
}
