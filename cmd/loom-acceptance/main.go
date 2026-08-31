package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/calypr/loom/internal/acceptance"
)

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
	)
	flag.Parse()
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
func fatal(err error) { fatalf("%v", err) }
func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
