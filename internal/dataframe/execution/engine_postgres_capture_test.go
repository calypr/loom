package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type exec11CaptureFixture struct {
	engine   *Engine
	spies    *dispatchSpies
	catalog  *testArtifactCatalog
	ch       *testArtifactClickHouse
	manager  *chartifact.Manager
	resolved Resolved
	stream   OutputStream
	columns  []chartifact.Column
	declared []string
}

func exec11RestrictedBindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/programs/a"},
	}
}

func newExec11Capture(t *testing.T, rows []map[string]any) exec11CaptureFixture {
	t.Helper()
	makeOutput := func(name string) recipe.Output {
		return recipe.Output{
			Name: name, RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "value", ColumnID: name + "-value", Expr: recipe.Expression{Select: "root.id"}}},
		}
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "pg-capture-fixture", TranslationVersion: "test",
		Outputs: []recipe.Output{makeOutput("patients"), makeOutput("observations")},
	}
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	config.SourceRows = exec02pgPort(spies, &spies.pgFull, rows)
	config.PreviewSourceRows = exec02pgPort(spies, &spies.pgPreview, rows)
	engine := config.newEngineForTest(t)
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, exec11RestrictedBindings())
	if err != nil {
		t.Fatalf("compile capture bundle: %v", err)
	}
	var output = resolved.Compiled.Outputs[0]
	if output.Name != "patients" {
		t.Fatalf("fixture output ordering drifted: %q", output.Name)
	}
	stream, query, err := engine.buildCompiledOutputStream(context.Background(), resolved, output, 0, map[string]bool{}, true)
	if err != nil {
		t.Fatalf("build PostgreSQL capture stream: %v", err)
	}
	if stream.command.Backend() != command.CompiledBackendPostgreSQL || stream.expectedBackend != command.CompiledBackendPostgreSQL {
		t.Fatalf("capture stream is not fully PostgreSQL-tagged: command=%q expected=%q", stream.command.Backend(), stream.expectedBackend)
	}
	columns, err := chartifact.ColumnsFromCompiledOutput(stream.outputSchema)
	if err != nil {
		t.Fatalf("artifact columns: %v", err)
	}
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatalf("chartifact.New: %v", err)
	}
	return exec11CaptureFixture{
		engine: engine, spies: spies, catalog: catalog, ch: ch, manager: manager,
		resolved: resolved, stream: stream, columns: columns, declared: append([]string(nil), query.Columns...),
	}
}

func exec11PortRows(values ...string) []map[string]any {
	rows := make([]map[string]any, 0, len(values))
	for index, value := range values {
		rows = append(rows, map[string]any{
			"_key":               []string{"generation-2/exec11-row-one", "generation-2/exec11-row-two"}[index],
			"value":              value,
			"auth_resource_path": "/programs/a",
		})
	}
	return rows
}

func exec11IdentityDigest(project, key string) string {
	encoded, err := json.Marshal([]any{project, key})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func TestPostgreSQLPrivateCaptureCombinePortsPredicate(t *testing.T) {
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	build := func(withManager, withQueryRows, withResolve, withPins bool) *Engine {
		config := Config{
			CompileTarget: exec02PostgreSQLTarget(t),
			Registry:      invalidRecipeRegistry{},
			SourceRows:    func(context.Context, RowsRequest, RowVisitor) error { return nil },
		}
		if withQueryRows {
			config.ClickHouseQueryRows = func(context.Context, string, []string, func(map[string]any) error, ...any) error { return nil }
		}
		if withResolve {
			config.ResolveClickHouseInputs = func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
				return nil, nil
			}
		}
		if withPins {
			config.WithExecutionReadPins = func(_ context.Context, _ []string, visit func(context.Context) error) error {
				return visit(context.Background())
			}
		}
		if withManager {
			config.PrivateClickHouseArtifacts = manager
		}
		engine, err := New(config)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return engine
	}
	if !build(true, true, true, true).CombinePortsConfigured() {
		t.Fatal("fully wired combine engine must report configured ports")
	}
	for _, missing := range []struct {
		name string
		skip int
	}{
		{"manager", 0}, {"query-rows", 1}, {"resolve", 2}, {"pins", 3},
	} {
		with := []bool{true, true, true, true}
		with[missing.skip] = false
		if build(with[0], with[1], with[2], with[3]).CombinePortsConfigured() {
			t.Fatalf("engine missing the %s port must report unconfigured", missing.name)
		}
	}
}

func exec11Refusal(t *testing.T, fixture exec11CaptureFixture, wantPhrase string) {
	t.Helper()
	_, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-1", fixture.stream.stageID, fixture.columns)
	if err == nil || !strings.Contains(err.Error(), wantPhrase) {
		t.Fatalf("refusal drifted: got %v, want %q", err, wantPhrase)
	}
	if len(fixture.catalog.items) != 0 {
		t.Fatalf("refused capture created %d artifacts", len(fixture.catalog.items))
	}
}

func TestPostgreSQLPrivateCaptureServesRestrictedRows(t *testing.T) {
	fixture := newExec11Capture(t, exec11PortRows("Ada", "Grace"))
	if fixture.stream.artifactScopeMode != chartifact.ScopeModeRows || fixture.stream.scopeEvidence == nil || fixture.stream.scopeEvidenceIdentity == nil {
		t.Fatalf("capture stream lacks compiler scope: mode=%q evidence=%v identity=%v",
			fixture.stream.artifactScopeMode, fixture.stream.scopeEvidence != nil, fixture.stream.scopeEvidenceIdentity != nil)
	}
	artifact, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-1", fixture.stream.stageID, fixture.columns)
	if err != nil {
		t.Fatalf("materialize PostgreSQL capture: %v", err)
	}
	manifest := artifact.Manifest()
	if manifest.RowCount != 2 || manifest.Identity.ExecutionID != "exec11-1" || manifest.Identity.OutputID != "patients" ||
		manifest.Identity.AuthScopeMode != authscope.ReadScopeRestricted ||
		len(manifest.Identity.AuthResourcePaths) != 1 || manifest.Identity.AuthResourcePaths[0] != "/programs/a" ||
		manifest.Identity.ScopeMode != chartifact.ScopeModeRows ||
		manifest.Identity.RecipeDigest != fixture.resolved.StoredRecipeDigest {
		t.Fatalf("PostgreSQL capture lost its exact restricted identity: %#v", manifest)
	}
	if manifest.Identity.PlanDigest == "" {
		t.Fatalf("PostgreSQL capture artifact lacks its plan fingerprint")
	}
	written := fixture.ch.rows[manifest.PhysicalTable]
	if len(written) != 2 {
		t.Fatalf("artifact rows = %d, want 2", len(written))
	}
	for index, row := range written {
		identity, _ := row["__loom_row_id"].(string)
		if len(identity) != 64 {
			t.Fatalf("PostgreSQL capture row %d lacks a derived 64-hex stable identity: %v", index, row)
		}
		if row["value"] != exec11PortRows("Ada", "Grace")[index]["value"] ||
			row["auth_resource_path"] != "/programs/a" || row["project_id"] != "project-a" {
			t.Fatalf("PostgreSQL capture row %d drifted or lost its authorization path: %v", index, row)
		}
	}
	if written[0]["__loom_row_id"] == written[1]["__loom_row_id"] {
		t.Fatalf("distinct _key rows collapsed to one stable identity")
	}
	if digest := exec11IdentityDigest("project-a", "generation-2/exec11-row-one"); written[0]["__loom_row_id"] != digest {
		t.Fatalf("stable identity is not the scope-bound digest of the row key")
	}
	if fixture.spies.clickHouseQueries != 0 || len(fixture.spies.aqlQueries) != 0 || len(fixture.spies.aqlPreviewQueries) != 0 || len(fixture.spies.controlSnapshot()) != 0 {
		t.Fatalf("PostgreSQL capture leaked into AQL/ClickHouse callbacks: clickhouse=%d aql=%v preview=%v controls=%v",
			fixture.spies.clickHouseQueries, fixture.spies.aqlQueries, fixture.spies.aqlPreviewQueries, fixture.spies.controlSnapshot())
	}
	if len(fixture.spies.pgFull) != 1 {
		t.Fatalf("capture port calls = %d, want 1", len(fixture.spies.pgFull))
	}
	if strings.Join(fixture.spies.pgFull[0].Columns, ",") != strings.Join(fixture.declared, ",") {
		t.Fatalf("capture port received declared columns %v, want the compiled contract %v", fixture.spies.pgFull[0].Columns, fixture.declared)
	}
	if err := artifact.Release(context.Background()); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func exec11IdentityDigestFields(values ...any) string {
	encoded, err := json.Marshal(append([]any(nil), values...))
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func TestPostgreSQLPrivateCaptureRowIdentityMatchesCommandBinds(t *testing.T) {
	t.Run("UntrimmedRawBindingCannotEnterTheDigest", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.RowIdentity = &spec.RowIdentity{Fields: []string{"project", "dataset_generation", "_key"}}
		fixture.stream.bindings.Project = "  project-a  "
		fixture.stream.bindings.DatasetGeneration = "  generation-2  "
		sqlText, params, err := fixture.stream.command.PostgreSQLParameters()
		if err != nil {
			t.Fatal(err)
		}
		if len(params) < 2 {
			t.Fatalf("command carries fewer than two scope parameters: %s", sqlText)
		}
		boundProject, okProject := params[0].Value.(string)
		boundGeneration, okGeneration := params[1].Value.(string)
		if !okProject || !okGeneration || boundProject != "project-a" || boundGeneration != "generation-2" {
			t.Fatalf("command scope arguments drifted from the trimmed binds: %#v/%#v in %s",
				params[0].Value, params[1].Value, sqlText)
		}
		want := exec11IdentityDigestFields(boundProject, boundGeneration, "generation-2/exec11-row-one")
		var got string
		if _, err := fixture.stream.Stream(context.Background(), func(row map[string]any) error {
			got, _ = row["__loom_row_id"].(string)
			return nil
		}); err != nil {
			t.Fatalf("stream: %v", err)
		}
		if got != want {
			t.Fatalf("row identity did not derive from the values the command bound: got %s, want command-bound %s", got, want)
		}
	})
	t.Run("MissingScopeBindFailsClosedLikeTheAQLArm", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.RowIdentity = &spec.RowIdentity{Fields: []string{"project", "dataset_generation", "_key"}}
		fixture.stream.physicalPlan.BindVars["dataset_generation"] = 42
		if _, err := fixture.stream.Stream(context.Background(), func(map[string]any) error { return nil }); err == nil ||
			!strings.Contains(err.Error(), `stable row identity field "dataset_generation" is missing`) {
			t.Fatalf("untyped scope bind error = %v, want typed missing-field refusal", err)
		}
	})
	t.Run("BlankGenerationCannotBuildACaptureCommand", func(t *testing.T) {
		bindings := exec11RestrictedBindings()
		bindings.DatasetGeneration = "   "
		makeOutput := func(name string) recipe.Output {
			return recipe.Output{
				Name: name, RootResourceType: "Patient", RowGrain: "patient",
				Fields: []recipe.Field{{Name: "value", ColumnID: name + "-value", Expr: recipe.Expression{Select: "root.id"}}},
			}
		}
		bundle := recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "pg-capture-blank", TranslationVersion: "test",
			Outputs: []recipe.Output{makeOutput("patients"), makeOutput("observations")},
		}
		spies := &dispatchSpies{}
		config := spies.arangoEngine()
		config.CompileTarget = exec02PostgreSQLTarget(t)
		config.SourceRows = exec02pgPort(spies, &spies.pgFull, exec11PortRows("Ada"))
		engine := config.newEngineForTest(t)
		resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, bindings)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if _, _, err := engine.buildCompiledOutputStream(context.Background(), resolved, resolved.Compiled.Outputs[0], 0, map[string]bool{}, true); err == nil ||
			!strings.Contains(err.Error(), `"dataset_generation" bind must be a string`) {
			t.Fatalf("blank-generation capture build error = %v, want the renderer's typed scope refusal", err)
		}
	})
}

func TestPostgreSQLPrivateCaptureDispatchRefusesIncompleteSourceShapes(t *testing.T) {
	t.Run("ForeignExpectedBackend", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.expectedBackend = command.CompiledBackendAQL
		exec11Refusal(t, fixture, "private artifact requires a compiler-resolved streamable AQL, PostgreSQL, or ClickHouse output")
	})
	t.Run("MissingDeclaredContract", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.commandColumns = nil
		exec11Refusal(t, fixture, "private artifact requires a compiler-resolved streamable AQL, PostgreSQL, or ClickHouse output")
	})
	t.Run("BoundedPreview", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.queryLimit = 10
		exec11Refusal(t, fixture, "cannot be built from a bounded preview")
	})
}

func TestPostgreSQLPrivateCaptureRestrictedContractFailClosed(t *testing.T) {
	t.Run("RestrictedWithoutRowPathProjection", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.bindings.IncludeAuthResourcePath = false
		exec11Refusal(t, fixture, "restricted PostgreSQL artifact requires a row-level authorization path projection")
	})
	t.Run("MissingStableRowIdentity", func(t *testing.T) {
		fixture := newExec11Capture(t, []map[string]any{{"value": "Ada", "auth_resource_path": "/programs/a"}})
		fixture.stream.RowIdentity = nil
		_, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-1", fixture.stream.stageID, fixture.columns)
		if err == nil || !strings.Contains(err.Error(), "compiled output is missing a stable row identity") ||
			!strings.Contains(err.Error(), "stream PostgreSQL prefix into private ClickHouse artifact") {
			t.Fatalf("missing row identity must fail through the PostgreSQL-labelled stream error: %v", err)
		}
		if len(fixture.catalog.items) != 0 {
			t.Fatalf("refused capture created %d artifacts", len(fixture.catalog.items))
		}
	})
	t.Run("MissingRowKeyColumn", func(t *testing.T) {
		fixture := newExec11Capture(t, []map[string]any{{"value": "Ada", "auth_resource_path": "/programs/a"}})
		exec11Refusal(t, fixture, `stable row identity field "_key" is missing`)
	})
	t.Run("ScopeEvidenceMissing", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.scopeEvidence = nil
		exec11Refusal(t, fixture, "private PostgreSQL artifact requires opaque evidence for its exact complete source plan")
	})
}

func TestPostgreSQLPrivateCaptureScopeModeCoherence(t *testing.T) {
	t.Run("WholeScopeWithoutCompilerDigest", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.artifactScopeMode = chartifact.ScopeModeWhole
		fixture.stream.artifactScopeEvidence = ""
		exec11Refusal(t, fixture, "whole-scope private artifact requires compiler-issued scope evidence")
	})
	t.Run("RowScopeCarryingWholeDigest", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.artifactScopeEvidence = "forged-digest"
		exec11Refusal(t, fixture, "row-scoped private artifact cannot carry whole-scope evidence")
	})
}

func TestPostgreSQLPrivateCaptureSchemaIdentity(t *testing.T) {
	t.Run("SchemaDigestDrift", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		drifted := append([]chartifact.Column(nil), fixture.columns...)
		for index := range drifted {
			if drifted[index].Name == "value" {
				drifted[index].LogicalType = "integer"
				drifted[index].ClickHouseType = ""
			}
		}
		_, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-1", fixture.stream.stageID, drifted)
		if err == nil || !strings.Contains(err.Error(), "does not match the compiled output schema") {
			t.Fatalf("schema drift must refuse: %v", err)
		}
		if len(fixture.catalog.items) != 0 {
			t.Fatalf("refused capture created %d artifacts", len(fixture.catalog.items))
		}
	})
	t.Run("StreamColumnMissingFromSchema", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.Columns = nil
		_, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-1", fixture.stream.stageID, fixture.columns)
		if err == nil || !strings.Contains(err.Error(), "do not match the private artifact schema") {
			t.Fatalf("stream/schema divergence must refuse: %v", err)
		}
		if len(fixture.catalog.items) != 0 {
			t.Fatalf("refused capture created %d artifacts", len(fixture.catalog.items))
		}
	})
}
