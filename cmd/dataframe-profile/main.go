// dataframe-profile compiles one checked-in dataframe fixture and profiles
// the exact rendered AQL directly against ArangoDB. It is intentionally a
// diagnostic command: normal GraphQL execution never enables PROFILE.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	compilerfixture "github.com/calypr/loom/conformance/compiler"
	"github.com/calypr/loom/generated/graphqlapi/model"
	queryapi "github.com/calypr/loom/internal/graphqlapi/query"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type profileReport struct {
	ArtifactVersion        string                     `json:"artifact_version"`
	Status                 string                     `json:"status"`
	Fixture                string                     `json:"fixture"`
	AQLSHA256              string                     `json:"aql_sha256"`
	ResultSHA256           string                     `json:"result_sha256,omitempty"`
	BindVars               map[string]any             `json:"bind_vars"`
	Rows                   int                        `json:"rows"`
	Explain                explainReport              `json:"explain"`
	Profile                profileReportDetails       `json:"profile"`
	PlanDiagnostics        ir.CompilerPlanDiagnostics `json:"plan_diagnostics"`
	AQLPath                string                     `json:"aql_path"`
	ProfilePath            string                     `json:"profile_path,omitempty"`
	ProfileWallSeconds     float64                    `json:"profile_wall_seconds"`
	ProfileColdWallSeconds float64                    `json:"profile_cold_wall_seconds"`
	Expected               expectedReport             `json:"expected,omitempty"`
}

type expectedReport struct {
	Path         string   `json:"path,omitempty"`
	Rows         int      `json:"rows,omitempty"`
	Columns      []string `json:"columns,omitempty"`
	RowsMatch    bool     `json:"rows_match"`
	ColumnsMatch bool     `json:"columns_match"`
	Compared     bool     `json:"compared"`
}

type explainReport struct {
	Plans               []arangostore.ExplainPlanEstimate   `json:"plans"`
	FullCollectionScans []arangostore.ExplainCollectionScan `json:"full_collection_scans"`
	Indexes             []arangostore.ExplainIndexSummary   `json:"indexes"`
	OptimizerRules      []string                            `json:"optimizer_rules"`
	Warnings            []arangostore.ExplainWarning        `json:"warnings"`
}

type profileReportDetails struct {
	RuntimeSeconds     float64                          `json:"runtime_seconds_sum_of_nodes"`
	ScannedFull        int                              `json:"scanned_full"`
	ScannedIndex       int                              `json:"scanned_index"`
	PeakMemory         uint64                           `json:"peak_memory_bytes"`
	Phases             arangostore.ProfilePhases        `json:"phases"`
	ByType             []arangostore.ProfileNodeGroup   `json:"by_node_type"`
	TraversalNodes     []arangostore.ProfileNodeSummary `json:"traversal_nodes"`
	EnumerateListNodes []arangostore.ProfileNodeSummary `json:"enumerate_list_nodes"`
	TopNodes           []arangostore.ProfileNodeSummary `json:"top_nodes"`
}

func main() {
	var (
		fixtureDir  = flag.String("fixtures", "conformance/compiler/fixtures", "compiler fixture directory")
		fixtureID   = flag.String("fixture", "gdc-case-matrix", "fixture ID to compile")
		variables   = flag.String("variables", "", "GraphQL variables JSON; when set, compile its input instead of the fixture builder")
		limit       = flag.Int("limit", 1000, "root row limit")
		url         = flag.String("url", "http://127.0.0.1:8529", "ArangoDB URL")
		database    = flag.String("database", "fhir_proto", "ArangoDB database")
		aqlPath     = flag.String("aql-out", "", "write exact rendered AQL to this path (default: <fixture>-<hash>.aql)")
		reportPath  = flag.String("report-out", "", "write JSON profile report to this path")
		profile     = flag.Int("profile", 2, "Arango profile level")
		batchSize   = flag.Int("batch-size", 10000, "Arango profile cursor batch size")
		expectedCSV = flag.String("expected-csv", "", "optional legacy CSV used for row/column parity")
	)
	flag.Parse()
	if *limit <= 0 {
		fatalf("limit must be positive")
	}
	if *profile < 1 || *profile > 2 {
		fatalf("profile must be 1 or 2")
	}

	var (
		fixture  compilerfixture.Fixture
		bundle   recipe.Bundle
		bindings recipe.RuntimeBindings
		label    = *fixtureID
	)
	if *variables != "" {
		data, err := os.ReadFile(*variables)
		if err != nil {
			fatalf("read variables %q: %v", *variables, err)
		}
		var payload struct {
			Input model.FhirDataframeInput `json:"input"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			fatalf("decode GraphQL variables %q: %v", *variables, err)
		}
		if payload.Input.Project == "" || payload.Input.RootResourceType == "" {
			fatalf("GraphQL variables %q do not contain a complete input", *variables)
		}
		bundle, err = queryapi.RecipeBundleFromInput(payload.Input)
		if err != nil {
			fatalf("convert GraphQL variables to recipe: %v", err)
		}
		bindings = recipe.RuntimeBindings{Project: payload.Input.Project, AuthResourcePaths: payload.Input.AuthResourcePaths}
		label = filepath.Base(*variables)
	} else {
		fixtures, err := compilerfixture.LoadDir(*fixtureDir)
		if err != nil {
			fatalf("load fixtures: %v", err)
		}
		for _, candidate := range fixtures {
			if candidate.ID == *fixtureID {
				fixture = candidate
				break
			}
		}
		if fixture.ID == "" {
			fatalf("fixture %q not found in %s", *fixtureID, *fixtureDir)
		}
		bundle = fixture.Recipe
		bindings = recipe.RuntimeBindings{Project: fixture.Project}
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		fatalf("build recipe plan %q: %v", label, err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "profile", bindings.DatasetGeneration)
	if err != nil {
		fatalf("resolve recipe plan %q: %v", label, err)
	}
	compiledQueries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, *limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		fatalf("compile GDC request %q: %v", label, err)
	}
	if len(compiledQueries) != 1 {
		fatalf("compile GDC request %q produced %d outputs; profiling requires exactly one", label, len(compiledQueries))
	}
	compiled := compiledQueries[0]
	hash := sha256.Sum256([]byte(compiled.Query))
	aqlHash := hex.EncodeToString(hash[:])
	if *aqlPath == "" {
		*aqlPath = filepath.Join("docs", "benchmarks", label+"-"+aqlHash[:16]+".aql")
	}
	if err := os.WriteFile(*aqlPath, []byte(compiled.Query+"\n"), 0o644); err != nil {
		fatalf("write AQL %q: %v", *aqlPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client, err := arangostore.Open(ctx, *url, *database)
	if err != nil {
		fatalf("open Arango: %v", err)
	}
	defer client.Close(ctx)
	explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: compiled.Query, BindVars: compiled.BindVars})
	if err != nil {
		fatalf("EXPLAIN: %v", err)
	}
	started := time.Now()
	profileRequest := arangostore.ProfileRequest{
		Query:     compiled.Query,
		BindVars:  compiled.BindVars,
		BatchSize: *batchSize,
		Count:     true,
		Options:   arangostore.ProfileOptions{Profile: *profile},
	}
	_, err = client.Profile(ctx, profileRequest)
	if err != nil {
		fatalf("PROFILE: %v", err)
	}
	coldSeconds := time.Since(started).Seconds()
	warmStarted := time.Now()
	profileResult, err := client.Profile(ctx, profileRequest)
	if err != nil {
		fatalf("warm PROFILE: %v", err)
	}
	warmSeconds := time.Since(warmStarted).Seconds()

	assessment := arangostore.AssessExplainResult(explain)
	summary := arangostore.SummarizeProfile(profileResult)
	topNodes := append([]arangostore.ProfileNodeSummary(nil), summary.Nodes...)
	if len(topNodes) > 20 {
		topNodes = topNodes[:20]
	}
	traversalNodes := filterProfileNodes(summary.Nodes, "TraversalNode")
	enumerateListNodes := filterProfileNodes(summary.Nodes, "EnumerateListNode")
	report := profileReport{
		ArtifactVersion: "loom-default-dataframer-parity/v1",
		Status:          "live-arango",
		Fixture:         label,
		AQLSHA256:       aqlHash,
		ResultSHA256:    canonicalResultHash(profileResult.Result),
		BindVars:        compiled.BindVars,
		Rows:            profileResult.Count,
		Explain: explainReport{
			Plans:               assessment.Plans,
			FullCollectionScans: assessment.FullCollectionScans,
			Indexes:             assessment.Indexes,
			OptimizerRules:      assessment.AppliedOptimizerRules,
			Warnings:            assessment.Warnings,
		},
		Profile: profileReportDetails{
			RuntimeSeconds:     summary.RuntimeSeconds,
			ScannedFull:        summary.ScannedFull,
			ScannedIndex:       summary.ScannedIndex,
			PeakMemory:         summary.PeakMemory,
			Phases:             profileResult.Extra.Profile,
			ByType:             summary.ByType,
			TraversalNodes:     traversalNodes,
			EnumerateListNodes: enumerateListNodes,
			TopNodes:           topNodes,
		},
		PlanDiagnostics:        compiled.PlanDiagnostics,
		AQLPath:                *aqlPath,
		ProfileWallSeconds:     warmSeconds,
		ProfileColdWallSeconds: coldSeconds,
	}
	report.Expected = compareExpectedCSV(*expectedCSV, compiled.Columns, profileResult.Count)
	if report.Expected.Compared && (!report.Expected.RowsMatch || !report.Expected.ColumnsMatch) {
		report.Status = "live-arango-parity-mismatch"
	}
	if report.Expected.Compared && (!report.Expected.RowsMatch || !report.Expected.ColumnsMatch) {
		report.Status = "live-arango-parity-mismatch"
	}
	if *reportPath != "" {
		report.ProfilePath = *reportPath
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
			fatalf("write report %q: %v", *reportPath, err)
		}
	}
	fmt.Println(string(encoded))
}

func filterProfileNodes(nodes []arangostore.ProfileNodeSummary, typ string) []arangostore.ProfileNodeSummary {
	filtered := make([]arangostore.ProfileNodeSummary, 0)
	for _, node := range nodes {
		if node.Type == typ {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func canonicalResultHash(rows []json.RawMessage) string {
	hash := sha256.New()
	for _, raw := range rows {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			continue
		}
		_, _ = hash.Write(canonical)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func compareExpectedCSV(path string, compiledColumns []string, rows int) expectedReport {
	result := expectedReport{Path: path}
	if strings.TrimSpace(path) == "" {
		return result
	}
	result.Compared = true
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil || len(records) == 0 {
		return result
	}
	result.Rows = len(records) - 1
	result.Columns = records[0]
	result.RowsMatch = result.Rows == rows
	result.ColumnsMatch = sameStrings(result.Columns, compiledColumns)
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dataframe-profile: "+format+"\n", args...)
	os.Exit(1)
}
