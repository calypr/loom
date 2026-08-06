package explorerclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type selector struct {
	Recipe, TranslationVersion, Output string
}

type projectStatus struct {
	Project, State, Generation, ExecutionID, ErrorCode string
	Retryable                                          bool
}

type metadata struct {
	ID, Name, Revision, ActiveContractVersion  string
	Selector                                   selector
	Availability                               string
	Completeness                               float64
	IncludedProjectCount, ExpectedProjectCount int
	ProjectStatuses                            []projectStatus
}

type fixture struct {
	Scenario           string
	AuthorizedProjects []string
	ForbiddenProjects  []string
	ExpectedCounts     map[string]int
	Data               struct {
		DataframeRows *struct {
			Materialization metadata
			Columns         []string
			Rows            []map[string]any
			TotalCount      int
		} `json:"dataframeRows"`
		DataframeDataset *metadata `json:"dataframeDataset"`
	} `json:"data"`
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	var value fixture
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return value
}

func fixtureMetadata(t *testing.T, value fixture) (metadata, []map[string]any) {
	t.Helper()
	if value.Data.DataframeRows != nil {
		return value.Data.DataframeRows.Materialization, value.Data.DataframeRows.Rows
	}
	if value.Data.DataframeDataset != nil {
		return *value.Data.DataframeDataset, nil
	}
	t.Fatalf("fixture %q has neither rows nor dataset metadata", value.Scenario)
	return metadata{}, nil
}

func TestExplorerAvailabilityAndStatusFixtures(t *testing.T) {
	files := []string{"available.json", "degraded-building.json", "degraded-stale.json", "degraded-failed.json", "degraded-missing-excluded.json", "unavailable.json"}
	covered := map[string]bool{}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			value := loadFixture(t, file)
			meta, rows := fixtureMetadata(t, value)
			if meta.Selector.Recipe == "" || meta.Selector.TranslationVersion == "" || meta.Selector.Output == "" {
				t.Fatalf("incomplete selector: %#v", meta.Selector)
			}
			counts := map[string]int{"CURRENT": 0, "STALE": 0, "BUILDING": 0, "FAILED": 0, "MISSING": 0, "EXCLUDED": 0}
			authorized := stringSet(value.AuthorizedProjects)
			for _, status := range meta.ProjectStatuses {
				counts[status.State]++
				covered[status.State] = true
				if _, ok := authorized[status.Project]; !ok {
					t.Fatalf("unauthorized project %q in status output", status.Project)
				}
				for _, forbidden := range value.ForbiddenProjects {
					if status.Project == forbidden {
						t.Fatalf("forbidden project %q leaked", forbidden)
					}
				}
			}
			for state, expected := range value.ExpectedCounts {
				if counts[state] != expected {
					t.Fatalf("%s count = %d, want %d", state, counts[state], expected)
				}
			}
			if meta.ExpectedProjectCount != len(meta.ProjectStatuses) {
				t.Fatalf("expectedProjectCount = %d, statuses = %d", meta.ExpectedProjectCount, len(meta.ProjectStatuses))
			}
			switch meta.Availability {
			case "AVAILABLE":
				if meta.Completeness != 1 || len(rows) == 0 {
					t.Fatalf("available fixture is incomplete")
				}
			case "DEGRADED":
				if len(rows) == 0 {
					t.Fatalf("degraded dataset must remain usable")
				}
			case "UNAVAILABLE":
				if meta.IncludedProjectCount != 0 || len(rows) != 0 {
					t.Fatalf("unavailable dataset served rows")
				}
			default:
				t.Fatalf("unknown availability %q", meta.Availability)
			}
		})
	}
	for _, state := range []string{"CURRENT", "STALE", "BUILDING", "FAILED", "MISSING", "EXCLUDED"} {
		if !covered[state] {
			t.Errorf("project state %s has no fixture", state)
		}
	}
}

func TestSparseProjectColumnsAreNormal(t *testing.T) {
	value := loadFixture(t, "available.json")
	_, rows := fixtureMetadata(t, value)
	if _, ok := rows[0]["alpha_only"]; !ok {
		t.Fatal("first project should populate sparse field")
	}
	if _, ok := rows[1]["alpha_only"]; ok {
		t.Fatal("second project should omit sparse field")
	}
}

func TestMultipleRecipeVersionsRemainSelectable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("fixtures", "multiple-versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Data struct {
			DataframeDatasets []metadata `json:"dataframeDatasets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	versions := map[string]bool{}
	for _, dataset := range value.Data.DataframeDatasets {
		versions[dataset.Selector.TranslationVersion] = true
	}
	if !versions["v1"] || !versions["v2"] || len(versions) != 2 {
		t.Fatalf("versions = %#v", versions)
	}
}

func TestExactAndLegacyInputsAreMutuallyExclusive(t *testing.T) {
	for _, test := range []struct{ file, required, forbidden string }{
		{"exact-selector.variables.json", `"selector"`, `"dataType"`},
		{"legacy-data-type.variables.json", `"dataType"`, `"selector"`},
	} {
		data, err := os.ReadFile(test.file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, test.required) || strings.Contains(text, test.forbidden) {
			t.Fatalf("%s violates selector XOR dataType", test.file)
		}
	}
}

func TestGraphQLDocumentsRequestExplorerMetadata(t *testing.T) {
	data, err := os.ReadFile("explorer.graphql")
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, field := range []string{"selector", "translationVersion", "activeContractVersion", "availability", "completeness", "includedProjectCount", "expectedProjectCount", "projectStatuses", "retryable"} {
		if !strings.Contains(document, field) {
			t.Errorf("GraphQL document is missing %s", field)
		}
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
