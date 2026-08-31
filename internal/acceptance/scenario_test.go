package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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
