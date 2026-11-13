package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/google/uuid"
)

type arch12UnusedRegistry struct{}

func (arch12UnusedRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, fmt.Errorf("live diagnostics witness compiles bundles directly; the registry is never addressed")
}

func (arch12UnusedRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, fmt.Errorf("live diagnostics witness compiles bundles directly; the registry is never addressed")
}

func TestBackendNeutralDiagnosticsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: url, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	if err := client.Bootstrap(ctx, arangofactory.BootstrapSpec{Collections: []arangofactory.CollectionSpec{{Name: "Patient"}}}); err != nil {
		t.Fatal(err)
	}
	project, generation := "loom_arch12_diag_"+uuid.NewString(), "arch12-generation"
	owned := []string{}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, key := range owned {
			if err := client.ExecuteAQL(cleanupCtx, "REMOVE @key IN Patient", map[string]any{"key": key}); err != nil {
				t.Errorf("remove owned arch12 fixture %s: %v", key, err)
			}
		}
	})
	for _, fixture := range []struct{ key, id, gender string }{
		{key: "ada", id: "patient-ada", gender: "female"},
		{key: "alan", id: "patient-alan", gender: "male"},
	} {
		key := project + "_" + fixture.key
		owned = append(owned, key)
		if err := client.ExecuteAQL(ctx, "INSERT {\"_key\": @key, id: @id, project: @project, project_id: @project, dataset_generation: @generation, resourceType: \"Patient\", gender: @gender, payload: {id: @id, gender: @gender}} IN Patient", map[string]any{
			"key": key, "id": fixture.id, "project": project, "generation": generation, "gender": fixture.gender,
		}); err != nil {
			t.Fatalf("insert arch12 patient fixture: %v", err)
		}
	}
	rowsPort := func(queryCtx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		query, bindVars, dispatchErr := request.Command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		return client.QueryRows(queryCtx, query, request.BatchSize, bindVars, arangofactory.RowVisitor(visit))
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      arch12UnusedRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "arch12-scope" },
		SourceRows:    rowsPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "arch12 diagnostics", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "Rows", RootResourceType: "Patient", RowGrain: "resource",
		Fields: []recipe.Field{
			{Name: "ids", ColumnID: "ids", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
	}}}
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeUnrestricted, PreviewLimit: 25, OutputNames: []string{"Rows"},
	}
	resolved, err := engine.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		t.Fatalf("compile arch12 bundle: %v", err)
	}
	compiled, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile arch12 command: %v", err)
	}
	if compiled.Command.Backend() != command.CompiledBackendAQL {
		t.Fatalf("fixture command tag = %q, want aql", compiled.Command.Backend())
	}
	service := dataframeexecution.NewService(dataframeexecution.ServiceConfig{SourceRows: rowsPort})
	result, err := service.RunCompiled(ctx, compiled)
	if err != nil {
		t.Fatalf("RunCompiled against Arango: %v", err)
	}
	if result.RowCount != 2 {
		t.Fatalf("live row count = %d, want 2", result.RowCount)
	}
	if result.Diagnostics.SourceQuery <= 0 {
		t.Fatalf("real Arango cursor must produce a positive source interval, got %v", result.Diagnostics.SourceQuery)
	}
	if result.Diagnostics.SourceBackend != command.CompiledBackendAQL {
		t.Fatalf("source backend tag = %q, want aql", result.Diagnostics.SourceBackend)
	}
	if result.Diagnostics.RowMaterialization < 0 {
		t.Fatalf("row materialization must be non-negative, got %v", result.Diagnostics.RowMaterialization)
	}
	summary, err := engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "Rows", Limit: 25}, func(map[string]any) error { return nil })
	if err != nil {
		t.Fatalf("PreviewOutput against Arango: %v", err)
	}
	if summary.SourceQueryDuration <= 0 {
		t.Fatalf("real Arango preview cursor must produce a positive source interval, got %v", summary.SourceQueryDuration)
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !hex64.MatchString(summary.PlanFingerprint) {
		t.Fatalf("preview plan fingerprint is not a 64-hex shape digest: %q", summary.PlanFingerprint)
	}
	explanation, err := (dataframeexecution.Control{Engine: engine}).ExplainResolved(ctx, resolved, false)
	if err != nil {
		t.Fatalf("ExplainResolved against Arango: %v", err)
	}
	if len(explanation.Outputs) != 1 || explanation.Outputs[0].PlanFingerprint != summary.PlanFingerprint {
		t.Fatalf("Explain and preview diverged against the live service: explain=%+v preview=%q", explanation.Outputs, summary.PlanFingerprint)
	}
	encoded, err := json.Marshal(struct {
		Summary     dataframeexecution.PreviewSummary   `json:"summary"`
		Diagnostics dataframeexecution.QueryDiagnostics `json:"diagnostics"`
	}{Summary: summary, Diagnostics: result.Diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	for _, tell := range []string{"FOR d IN ", "LIMIT @limit", "@auth_resource_paths", "patient-ada", project} {
		if strings.Contains(string(encoded), tell) {
			t.Fatalf("public diagnostics carry the tell %q: %s", tell, encoded)
		}
	}
}

func TestBackendNeutralDiagnosticsLivePostgreSQL(t *testing.T) {
	fixture := newQRouteFixture(t)
	fixture.load(t, qRouteProject, qRouteGenA, map[string][]string{
		"Patient.ndjson": qRoutePatientLines(t, "patient-ada", "patient-alan"),
	})
	fixture.activate(t, qRouteProject, qRouteGenA)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, closeRuntime, err := composePostgreSQLFirstQueryRoute(ctx, qRouteBootConfig(t, fixture, "127.0.0.1:0"), logger)
	if err != nil {
		t.Fatalf("compose production route: %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close production route runtime: %s", fixture.scrub(err))
		}
	}()
	body := `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { rowCount diagnostics { sourceQueryMs arangoQueryMs compilationMs totalMs } } }","variables":{"input":{"project":"` + qRouteProject + `","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"}}]},"limit":10}}`
	response := qRouteGraphQL(t, server.App(), body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runFhirDataframe HTTP %d: %s", response.StatusCode, fixture.scrub(response.Body))
	}
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			RunFhirDataframe struct {
				RowCount    int `json:"rowCount"`
				Diagnostics *struct {
					SourceQueryMs float64 `json:"sourceQueryMs"`
					ArangoQueryMs float64 `json:"arangoQueryMs"`
					CompilationMs float64 `json:"compilationMs"`
					TotalMs       float64 `json:"totalMs"`
				} `json:"diagnostics"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("decode handler envelope: %v (body %s)", err, fixture.scrub(response.Body))
	}
	if len(envelope.Errors) != 0 {
		t.Fatalf("handler returned GraphQL errors: %s", fixture.scrub(response.Body))
	}
	run := envelope.Data.RunFhirDataframe
	if run.RowCount != 2 {
		t.Fatalf("rowCount = %d, want 2: %s", run.RowCount, fixture.scrub(response.Body))
	}
	if run.Diagnostics == nil {
		t.Fatal("diagnostics must be projected on the PostgreSQL lane")
	}
	if run.Diagnostics.SourceQueryMs <= 0 {
		t.Fatalf("real PostgreSQL cursor must produce a positive sourceQueryMs, got %v", run.Diagnostics.SourceQueryMs)
	}
	if run.Diagnostics.ArangoQueryMs != 0 {
		t.Fatalf("arangoQueryMs must be exactly zero on the PostgreSQL lane, got %v", run.Diagnostics.ArangoQueryMs)
	}
	for _, tell := range []string{"SELECT ", "FROM loom.", "$1", "$2", "patient-ada", qRouteProject} {
		if strings.Contains(response.Body, tell) {
			t.Fatalf("PostgreSQL diagnostics response carries the SQL/value tell %q: %s", tell, fixture.scrub(response.Body))
		}
	}
}
