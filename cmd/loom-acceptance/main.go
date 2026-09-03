package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/calypr/loom/internal/acceptance"
	"github.com/calypr/loom/internal/explorer"
)

type smokeExpectation struct {
	Management  string
	Generation  string
	OutputID    string
	OutputTitle string
	Columns     []string
}

type smokeOracle struct {
	Columns []string `json:"columns"`
}

func main() {
	var (
		loomURL                  = flag.String("loom-url", "http://127.0.0.1:8080", "Loom HTTP URL")
		arangoURL                = flag.String("arango-url", "http://127.0.0.1:8529", "ArangoDB HTTP URL")
		clickhouseURL            = flag.String("clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL")
		clickhouseUser           = flag.String("clickhouse-username", "default", "ClickHouse username")
		clickhousePassword       = flag.String("clickhouse-password", "", "ClickHouse password (prefer LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD)")
		lockPath                 = flag.String("fixture-lock", "testdata/acceptance/ncpi-tcga-brca/fixture.lock.json", "metadata-only fixture lock")
		cacheDir                 = flag.String("fixture-cache", ".cache/acceptance/fixture", "content-addressed fixture cache")
		workspacePath            = flag.String("workspace", "testdata/acceptance/ncpi-tcga-brca/workspace.json", "Explorer workspace")
		oraclePath               = flag.String("oracle", "testdata/acceptance/ncpi-tcga-brca/oracle.json", "expected result oracle")
		artifactDir              = flag.String("artifacts", ".artifacts/acceptance/local", "evidence directory")
		project                  = flag.String("project", "NCPI_ACCEPTANCE", "run-specific project")
		generation               = flag.String("generation", "tcga-brca-locked", "run-specific immutable generation")
		runID                    = flag.String("run-id", "", "run identifier; defaults to random 16-byte hex")
		refresh                  = flag.Bool("refresh-fixture", false, "discover and write a metadata-only fixture lock, then exit")
		fixtureOnly              = flag.Bool("fixture-only", false, "validate/acquire the locked fixture and print counts, then exit")
		fhirEndpoint             = flag.String("fhir-endpoint", acceptance.DefaultFHIRBase, "public FHIR Aggregator base URL")
		studyID                  = flag.String("study-id", acceptance.DefaultStudyID, "FHIR Aggregator ResearchStudy ID")
		performanceBase          = flag.String("performance-base-report", "", "base acceptance report to compare")
		performanceCurrent       = flag.String("performance-current-report", "", "current acceptance report to compare")
		performanceRepeatBase    = flag.String("performance-repeat-base-report", "", "repeat base acceptance report")
		performanceRepeatCurrent = flag.String("performance-repeat-current-report", "", "repeat current acceptance report")
		performanceOutput        = flag.String("performance-output", "", "performance comparison JSON output")
		smokeOnly                = flag.Bool("smoke-only", false, "verify the running demo Explorer contract, then exit")
		smokeManagement          = flag.String("smoke-management", "REPOSITORY", "expected Explorer management mode")
		smokeOutputID            = flag.String("smoke-output-id", "tcga_brca_cohort", "expected Explorer output ID")
		smokeOutputTitle         = flag.String("smoke-output-title", "TCGA-BRCA patient cohort", "expected Explorer output title")
	)
	flag.Parse()
	if *smokeOnly {
		want, err := loadSmokeExpectation(*smokeManagement, *generation, *smokeOutputID, *smokeOutputTitle, *oraclePath)
		if err != nil {
			fatalf("load smoke expectation: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		state, err := fetchExplorer(ctx, *loomURL, *project)
		if err != nil {
			fatalf("fetch Explorer: %v", err)
		}
		if err := verifyExplorerContract(state, want); err != nil {
			fatalf("demo contract mismatch: %v", err)
		}
		fmt.Printf("demo Explorer contract verified: management=%s generation=%s output=%q columns=%d\n", want.Management, want.Generation, want.OutputTitle, len(want.Columns))
		return
	}
	if *performanceBase != "" || *performanceCurrent != "" {
		if *performanceBase == "" || *performanceCurrent == "" || *performanceOutput == "" {
			fatalf("performance comparison requires base, current, and output paths")
		}
		report, err := acceptance.ComparePerformanceReportFiles(*performanceBase, *performanceCurrent, *performanceRepeatBase, *performanceRepeatCurrent)
		if err != nil {
			fatal(err)
		}
		if err := acceptance.WritePerformanceComparison(*performanceOutput, report); err != nil {
			fatal(err)
		}
		fmt.Printf("acceptance performance %s observations=%d\n", report.Status, len(report.Observations))
		switch report.Status {
		case "SUSPECTED":
			os.Exit(3)
		case "FAILED":
			os.Exit(1)
		}
		return
	}
	if *clickhousePassword == "" {
		*clickhousePassword = os.Getenv("LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if *refresh {
		lock, err := acceptance.RefreshLock(ctx, *fhirEndpoint, *studyID, *lockPath, &http.Client{Timeout: 45 * time.Second}, acceptance.DefaultLimits())
		if err != nil {
			fatal(err)
		}
		fmt.Printf("refreshed fixture lock %s: %d resources\n", lock.Digest, sumCounts(lock.Counts))
		return
	}
	lock, err := acceptance.LoadLock(*lockPath)
	if err != nil {
		fatal(err)
	}
	if *fixtureOnly {
		fixture, err := (&acceptance.Fetcher{CacheDir: *cacheDir, Lock: lock, Limits: acceptance.DefaultLimits(), HTTPClient: &http.Client{Timeout: 45 * time.Second}}).Acquire(ctx)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("fixture %s meta=%s resources=%d counts=%v\n", fixture.Digest, fixture.MetaDir, sumCounts(fixture.Counts), fixture.Counts)
		return
	}
	runner, err := acceptance.New(acceptance.Config{
		Target:  acceptance.StaticTarget{Conn: acceptance.Connections{LoomURL: *loomURL, ArangoURL: *arangoURL, ClickHouseURL: *clickhouseURL, ClickHouseUsername: *clickhouseUser, ClickHousePassword: *clickhousePassword}},
		Fixture: acceptance.Fetcher{CacheDir: *cacheDir, Lock: lock, Limits: acceptance.DefaultLimits(), HTTPClient: &http.Client{Timeout: 45 * time.Second}},
		Run:     acceptance.RunSpec{RunID: acceptance.RunID(*runID), Project: *project, Generation: *generation}, ArtifactDir: *artifactDir, WorkspacePath: *workspacePath, OraclePath: *oraclePath,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute}, SourceCommit: sourceCommit(),
	})
	if err != nil {
		fatal(err)
	}
	report, err := runner.Run(ctx)
	if err != nil {
		fatalf("acceptance failed (report=%s): %v", filepath.Join(*artifactDir, "report.json"), err)
	}
	fmt.Printf("acceptance %s run=%s fixture=%s\n", report.Status, report.Run.Run, report.Fixture.Digest)
}

func sourceCommit() string {
	if value := os.Getenv("GITHUB_SHA"); value != "" {
		return value
	}
	return "local"
}
func sumCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func loadSmokeExpectation(management, generation, outputID, outputTitle, oraclePath string) (smokeExpectation, error) {
	data, err := os.ReadFile(oraclePath)
	if err != nil {
		return smokeExpectation{}, err
	}
	var value smokeOracle
	if err := json.Unmarshal(data, &value); err != nil {
		return smokeExpectation{}, err
	}
	want := smokeExpectation{Management: management, Generation: generation, OutputID: outputID, OutputTitle: outputTitle, Columns: value.Columns}
	if strings.TrimSpace(want.Management) == "" || strings.TrimSpace(want.Generation) == "" || strings.TrimSpace(want.OutputID) == "" || strings.TrimSpace(want.OutputTitle) == "" || len(want.Columns) == 0 {
		return smokeExpectation{}, errors.New("management, generation, output ID, output title, and oracle columns are required")
	}
	return want, nil
}

func fetchExplorer(ctx context.Context, base, project string) (explorer.ExplorerStateV1, error) {
	path := strings.TrimRight(base, "/") + "/api/v1/projects/" + url.PathEscape(project) + "/explorers/default"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return explorer.ExplorerStateV1{}, fmt.Errorf("%s returned %s", request.URL.Path, response.Status)
	}
	var state explorer.ExplorerStateV1
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	return state, nil
}

func verifyExplorerContract(state explorer.ExplorerStateV1, want smokeExpectation) error {
	if string(state.Management) != want.Management {
		return fmt.Errorf("management=%q want %q", state.Management, want.Management)
	}
	if state.Runtime == nil {
		return errors.New("runtime is null")
	}
	if state.Runtime.Generation != want.Generation {
		return fmt.Errorf("generation=%q want %q", state.Runtime.Generation, want.Generation)
	}
	for _, output := range state.Runtime.Outputs {
		if output.OutputID != want.OutputID {
			continue
		}
		if output.Title != want.OutputTitle {
			return fmt.Errorf("output %q title=%q want %q", want.OutputID, output.Title, want.OutputTitle)
		}
		columns := make([]string, len(output.Columns))
		for index, column := range output.Columns {
			columns[index] = column.Column
		}
		if !slices.Equal(columns, want.Columns) {
			return fmt.Errorf("output %q columns=%q want %q", want.OutputID, columns, want.Columns)
		}
		return nil
	}
	return fmt.Errorf("output %q is missing", want.OutputID)
}

func fatal(err error) { fatalf("%v", err) }
func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
