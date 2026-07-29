package compilerfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/store/arango"
)

// TestGDCOptimizationPolicyAblationAgainstArango is opt-in because it reads
// the local development database and profiles four complete GDC executions.
// It proves that independent policy switches preserve the same dataframe
// result before a later work package uses the measurements to enable a new
// rewrite.
func TestGDCOptimizationPolicyAblationAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run policy ablation against Arango")
	}
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture Fixture
	for _, candidate := range fixtures {
		if candidate.ID == "gdc-case-matrix" {
			fixture = candidate
			break
		}
	}
	if fixture.ID == "" {
		t.Fatal("gdc-case-matrix fixture is missing")
	}

	defaultPolicy := dataframe.DefaultPhysicalOptimizationPolicy()
	policies := []struct {
		name   string
		policy dataframe.PhysicalOptimizationPolicy
	}{
		{name: "none", policy: dataframe.PhysicalOptimizationPolicy{Enabled: false, MinimumSavings: 1}},
		{name: "sharing-only", policy: defaultPolicy.WithRule(dataframe.PhysicalOptimizationRulePreparedSelectors, false)},
		{name: "prepared-only", policy: defaultPolicy.WithRule(dataframe.PhysicalOptimizationRuleTraversalSharing, false).WithRule(dataframe.PhysicalOptimizationRulePreparedSelectors, true)},
		{name: "defaults", policy: defaultPolicy},
	}

	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	opts := arango.ConnectionOptions{URL: url, Database: database}
	var expectedHash string
	var expectedRows []map[string]any
	for _, candidate := range policies {
		candidate := candidate
		t.Run(candidate.name, func(t *testing.T) {
			compiled, err := compileRecipe(fixture.Recipe, project, 1000, candidate.policy)
			if err != nil {
				t.Fatal(err)
			}
			rows := make([]map[string]any, 0, 1000)
			err = dataframe.ExecuteQueryRows(ctx, dataframe.ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 1000}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(payload)
			resultHash := hex.EncodeToString(hash[:])
			if expectedHash == "" {
				expectedHash = resultHash
				expectedRows = rows
			} else if resultHash != expectedHash {
				for index := range rows {
					if index >= len(expectedRows) {
						break
					}
					// JSON object encoding sorts map keys, making this a stable
					// row-level diagnostic even though rows are decoded into maps.
					left, _ := json.Marshal(rows[index])
					right, _ := json.Marshal(expectedRows[index])
					if string(left) != string(right) {
						t.Logf("first differing row=%d candidate=%s baseline=%s", index, left, right)
						break
					}
				}
				t.Fatalf("result hash = %s, want %s", resultHash, expectedHash)
			}
			profile, err := dataframe.ProfileCompiledQuery(ctx, opts, compiled, 2)
			if err != nil {
				t.Fatal(err)
			}
			summary := arango.SummarizeProfile(profile)
			topNodes := summary.Nodes
			if len(topNodes) > 5 {
				topNodes = topNodes[:5]
			}
			t.Logf("policy=%s rows=%d hash=%s aql_sha256=%x profile_runtime=%0.6fs scanned_full=%d scanned_index=%d lookups=%d peak_memory=%d top_nodes=%#v rule_states=%#v", candidate.name, len(rows), resultHash, sha256.Sum256([]byte(compiled.Query)), summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profile.Extra.Stats.DocumentLookups, summary.PeakMemory, topNodes, compiled.PlanDiagnostics.OptimizationPolicy.RuleStates)
		})
	}
}

// TestTraversalSharingAblationAgainstArango profiles focused sibling and
// deep-traversal fixtures with sharing isolated from prepared selectors. The
// fixtures intentionally use an oracle project; this test rewrites only the
// project bind to the loaded local dataset and leaves route semantics intact.
func TestTraversalSharingAblationAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run traversal sharing profile")
	}
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	opts := arango.ConnectionOptions{URL: url, Database: database}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, fixtureID := range []string{"patient-sibling-targets", "patient-deep-filter"} {
		fixture, ok := byID[fixtureID]
		if !ok {
			t.Fatalf("fixture %q is missing", fixtureID)
		}
		t.Run(fixtureID, func(t *testing.T) {
			policies := []struct {
				name   string
				policy dataframe.PhysicalOptimizationPolicy
			}{
				{name: "unshared", policy: dataframe.PhysicalOptimizationPolicy{Enabled: false, MinimumSavings: 1}},
				{name: "sharing", policy: dataframe.DefaultPhysicalOptimizationPolicy().WithRule(dataframe.PhysicalOptimizationRulePreparedSelectors, false)},
			}
			var expectedHash string
			for _, candidate := range policies {
				compiled, err := compileRecipe(fixture.Recipe, project, fixture.Limit, candidate.policy)
				if err != nil {
					t.Fatal(err)
				}
				var rows []map[string]any
				executeSeconds := make([]float64, 0, 5)
				var executionHash string
				for run := 0; run < 5; run++ {
					candidateRows := make([]map[string]any, 0, fixture.Limit)
					executeStart := time.Now()
					err = dataframe.ExecuteQueryRows(ctx, dataframe.ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 1000}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
						candidateRows = append(candidateRows, row)
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					executeSeconds = append(executeSeconds, time.Since(executeStart).Seconds())
					if run == 0 {
						rows = candidateRows
					} else if len(candidateRows) != len(rows) {
						t.Fatalf("%s run %d rows = %d, want %d", candidate.name, run+1, len(candidateRows), len(rows))
					}
					runPayload, err := json.Marshal(candidateRows)
					if err != nil {
						t.Fatal(err)
					}
					runHash := sha256.Sum256(runPayload)
					if run == 0 {
						executionHash = hex.EncodeToString(runHash[:])
					} else if got := hex.EncodeToString(runHash[:]); got != executionHash {
						t.Fatalf("%s run %d result hash = %s, want %s", candidate.name, run+1, got, executionHash)
					}
				}
				warm := append([]float64(nil), executeSeconds[1:]...)
				sort.Float64s(warm)
				warmMedian := warm[len(warm)/2]
				payload, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(payload)
				resultHash := hex.EncodeToString(hash[:])
				if expectedHash == "" {
					expectedHash = resultHash
				} else if resultHash != expectedHash {
					t.Fatalf("%s result hash = %s, want %s", candidate.name, resultHash, expectedHash)
				}
				profile, err := dataframe.ProfileCompiledQuery(ctx, opts, compiled, 2)
				if err != nil {
					t.Fatal(err)
				}
				summary := arango.SummarizeProfile(profile)
				topNodes := summary.Nodes
				if len(topNodes) > 10 {
					topNodes = topNodes[:10]
				}
				t.Logf("policy=%s rows=%d hash=%s aql_sha256=%x warm_median=%0.6fs warm_min=%0.6fs runs=%#v profile_runtime=%0.6fs scanned_full=%d scanned_index=%d lookups=%d peak_memory=%d top_nodes=%#v shared=%d", candidate.name, len(rows), resultHash, sha256.Sum256([]byte(compiled.Query)), warmMedian, warm[0], executeSeconds, summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profile.Extra.Stats.DocumentLookups, summary.PeakMemory, topNodes, compiled.PlanDiagnostics.SharedTraversalCount)
			}
		})
	}
}

// TestPreparedSelectorAblationAgainstArango profiles the prepared selector
// payload independently from traversal sharing. It intentionally covers a
// child aggregate+slice, a child pivot, a nested child, and the full GDC
// dataframe so WP3 can make a generic payload decision instead of relying on
// one resource-specific query.
func TestPreparedSelectorAblationAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run prepared-selector profile")
	}
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	opts := arango.ConnectionOptions{URL: url, Database: database}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, fixtureID := range []string{"specimen-aggregate-slice", "patient-observation-pivot", "patient-deep-filter", "gdc-case-matrix"} {
		fixture, ok := byID[fixtureID]
		if !ok {
			t.Fatalf("fixture %q is missing", fixtureID)
		}
		t.Run(fixtureID, func(t *testing.T) {
			policies := []struct {
				name   string
				policy dataframe.PhysicalOptimizationPolicy
			}{
				{name: "direct", policy: dataframe.PhysicalOptimizationPolicy{Enabled: false, MinimumSavings: 1}},
				{name: "prepared", policy: dataframe.DefaultPhysicalOptimizationPolicy().WithRule(dataframe.PhysicalOptimizationRuleTraversalSharing, false)},
			}
			var expectedHash string
			for _, candidate := range policies {
				compiled, err := compileRecipe(fixture.Recipe, project, fixture.Limit, candidate.policy)
				if err != nil {
					t.Fatal(err)
				}
				var rows []map[string]any
				executeSeconds := make([]float64, 0, 5)
				var executionHash string
				var responseBytes int
				for run := 0; run < 5; run++ {
					candidateRows := make([]map[string]any, 0, fixture.Limit)
					executeStart := time.Now()
					err = dataframe.ExecuteQueryRows(ctx, dataframe.ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 1000}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
						candidateRows = append(candidateRows, row)
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					executeSeconds = append(executeSeconds, time.Since(executeStart).Seconds())
					payload, err := json.Marshal(candidateRows)
					if err != nil {
						t.Fatal(err)
					}
					result := sha256.Sum256(payload)
					runHash := hex.EncodeToString(result[:])
					if run == 0 {
						rows = candidateRows
						responseBytes = len(payload)
						executionHash = runHash
					} else if runHash != executionHash {
						t.Fatalf("%s run %d result hash = %s, want %s", candidate.name, run+1, runHash, executionHash)
					}
				}
				if expectedHash == "" {
					expectedHash = executionHash
				} else if executionHash != expectedHash {
					t.Fatalf("%s result hash = %s, want %s", candidate.name, executionHash, expectedHash)
				}
				warm := append([]float64(nil), executeSeconds[1:]...)
				sort.Float64s(warm)
				profile, err := dataframe.ProfileCompiledQuery(ctx, opts, compiled, 2)
				if err != nil {
					t.Fatal(err)
				}
				summary := arango.SummarizeProfile(profile)
				topNodes := summary.Nodes
				if len(topNodes) > 10 {
					topNodes = topNodes[:10]
				}
				explain, err := dataframe.ExplainCompiledQuery(ctx, opts, compiled)
				if err != nil {
					t.Fatal(err)
				}
				assessment := arango.AssessExplainResult(explain)
				preparedFields := strings.Count(compiled.Query, "__loom_prepared_")
				t.Logf("policy=%s rows=%d response_bytes=%d hash=%s aql_sha256=%x prepared_tokens=%d warm_median=%0.6fs warm_min=%0.6fs runs=%#v profile_runtime=%0.6fs scanned_full=%d scanned_index=%d lookups=%d peak_memory=%d indexes=%#v top_nodes=%#v", candidate.name, len(rows), responseBytes, executionHash, sha256.Sum256([]byte(compiled.Query)), preparedFields, warm[len(warm)/2], warm[0], executeSeconds, summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profile.Extra.Stats.DocumentLookups, summary.PeakMemory, assessment.Indexes, topNodes)
			}
		})
	}
}

// TestRichConsumerReuseProfileAgainstArango records the baseline work for
// WP4's proposed rich-consumer fusion. It deliberately uses the production
// default (prepared selectors disabled after WP3) and reports the physical
// reuse groups plus the rendered loop count. There is no fused candidate in
// this test: fusion must be added only after a compatible group and a profile
// benefit are demonstrated.
func TestRichConsumerReuseProfileAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run rich-consumer profile")
	}
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	opts := arango.ConnectionOptions{URL: url, Database: database}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, fixtureID := range []string{"specimen-aggregate-slice", "gdc-case-matrix"} {
		fixture, ok := byID[fixtureID]
		if !ok {
			t.Fatalf("fixture %q is missing", fixtureID)
		}
		t.Run(fixtureID, func(t *testing.T) {
			policy := dataframe.DefaultPhysicalOptimizationPolicy().WithRule(dataframe.PhysicalOptimizationRulePreparedSelectors, false)
			compiled, err := compileRecipe(fixture.Recipe, project, fixture.Limit, policy)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			executeSeconds := make([]float64, 0, 5)
			var resultHash string
			var responseBytes int
			for run := 0; run < 5; run++ {
				candidateRows := make([]map[string]any, 0, fixture.Limit)
				start := time.Now()
				err = dataframe.ExecuteQueryRows(ctx, dataframe.ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 1000}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
					candidateRows = append(candidateRows, row)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				executeSeconds = append(executeSeconds, time.Since(start).Seconds())
				payload, err := json.Marshal(candidateRows)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(payload)
				gotHash := hex.EncodeToString(hash[:])
				if run == 0 {
					rows = candidateRows
					responseBytes = len(payload)
					resultHash = gotHash
				} else if gotHash != resultHash {
					t.Fatalf("run %d result hash = %s, want %s", run+1, gotHash, resultHash)
				}
			}
			warm := append([]float64(nil), executeSeconds[1:]...)
			sort.Float64s(warm)
			profile, err := dataframe.ProfileCompiledQuery(ctx, opts, compiled, 2)
			if err != nil {
				t.Fatal(err)
			}
			summary := arango.SummarizeProfile(profile)
			topNodes := summary.Nodes
			if len(topNodes) > 10 {
				topNodes = topNodes[:10]
			}
			explain, err := dataframe.ExplainCompiledQuery(ctx, opts, compiled)
			if err != nil {
				t.Fatal(err)
			}
			assessment := arango.AssessExplainResult(explain)
			t.Logf("rows=%d response_bytes=%d hash=%s aql_sha256=%x warm_median=%0.6fs warm_min=%0.6fs runs=%#v rich_source_reuse=%#v rich_consumer_groups=%#v aql_for_loops=%d profile_runtime=%0.6fs scanned_full=%d scanned_index=%d lookups=%d peak_memory=%d indexes=%#v top_nodes=%#v", len(rows), responseBytes, resultHash, sha256.Sum256([]byte(compiled.Query)), warm[len(warm)/2], warm[0], executeSeconds, compiled.PlanDiagnostics.RichSourceReuse, compiled.PlanDiagnostics.RichConsumerGroups, strings.Count(compiled.Query, "FOR "), summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profile.Extra.Stats.DocumentLookups, summary.PeakMemory, assessment.Indexes, topNodes)
		})
	}
}

// TestCompactSetProjectionAblationAgainstArango compares full stored nodes
// with the typed identity-safe set projection while keeping traversal sharing
// and prepared selectors fixed. It is the WP5 evidence gate.
func TestCompactSetProjectionAblationAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compact-set profile")
	}
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	opts := arango.ConnectionOptions{URL: url, Database: database}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, fixtureID := range []string{"specimen-aggregate-slice", "patient-specimen-file", "patient-deep-filter", "gdc-case-matrix"} {
		fixture, ok := byID[fixtureID]
		if !ok {
			t.Fatalf("fixture %q is missing", fixtureID)
		}
		t.Run(fixtureID, func(t *testing.T) {
			limit := fixture.Limit
			if fixtureID == "gdc-case-matrix" {
				limit = 1000
			}
			basePolicy := dataframe.DefaultPhysicalOptimizationPolicy().WithRule(dataframe.PhysicalOptimizationRulePreparedSelectors, false)
			policies := []struct {
				name   string
				policy dataframe.PhysicalOptimizationPolicy
			}{
				{name: "full", policy: basePolicy.WithRule(dataframe.PhysicalOptimizationRuleCompactProjection, false)},
				{name: "compact", policy: basePolicy.WithRule(dataframe.PhysicalOptimizationRuleCompactProjection, true)},
			}
			var expectedHash string
			for _, candidate := range policies {
				compiled, err := compileRecipe(fixture.Recipe, project, limit, candidate.policy)
				if err != nil {
					t.Fatal(err)
				}
				var rows []map[string]any
				executeSeconds := make([]float64, 0, 5)
				var resultHash string
				var responseBytes int
				for run := 0; run < 5; run++ {
					candidateRows := make([]map[string]any, 0, limit)
					start := time.Now()
					err = dataframe.ExecuteQueryRows(ctx, dataframe.ExecuteQueryOptions{ConnectionOptions: opts, BatchSize: 1000}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
						candidateRows = append(candidateRows, row)
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					executeSeconds = append(executeSeconds, time.Since(start).Seconds())
					payload, err := json.Marshal(candidateRows)
					if err != nil {
						t.Fatal(err)
					}
					hash := sha256.Sum256(payload)
					gotHash := hex.EncodeToString(hash[:])
					if run == 0 {
						rows = candidateRows
						responseBytes = len(payload)
						resultHash = gotHash
					} else if gotHash != resultHash {
						t.Fatalf("%s run %d result hash = %s, want %s", candidate.name, run+1, gotHash, resultHash)
					}
				}
				if expectedHash == "" {
					expectedHash = resultHash
				} else if resultHash != expectedHash {
					t.Fatalf("%s result hash = %s, want %s", candidate.name, resultHash, expectedHash)
				}
				warm := append([]float64(nil), executeSeconds[1:]...)
				sort.Float64s(warm)
				profile, err := dataframe.ProfileCompiledQuery(ctx, opts, compiled, 2)
				if err != nil {
					t.Fatal(err)
				}
				summary := arango.SummarizeProfile(profile)
				explain, err := dataframe.ExplainCompiledQuery(ctx, opts, compiled)
				if err != nil {
					t.Fatal(err)
				}
				assessment := arango.AssessExplainResult(explain)
				t.Logf("policy=%s rows=%d response_bytes=%d hash=%s aql_sha256=%x warm_median=%0.6fs warm_min=%0.6fs runs=%#v profile_runtime=%0.6fs scanned_full=%d scanned_index=%d lookups=%d peak_memory=%d indexes=%#v compact_rule=%t", candidate.name, len(rows), responseBytes, resultHash, sha256.Sum256([]byte(compiled.Query)), warm[len(warm)/2], warm[0], executeSeconds, summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profile.Extra.Stats.DocumentLookups, summary.PeakMemory, assessment.Indexes, candidate.policy.RuleEnabled(dataframe.PhysicalOptimizationRuleCompactProjection))
			}
		})
	}
}
