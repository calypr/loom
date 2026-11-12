package execution

// pg-exec-11 offline capture contract: a PostgreSQL-served output is a
// legitimate private ClickHouse workspace artifact capture source.
//
// The matrix is exhaustive over the three source shapes (AQL-served,
// PostgreSQL-served, prepared ClickHouse combine): a complete PostgreSQL
// capture stream (PostgreSQL tag on the command and the expected backend,
// declared column contract) captures through the shared writer flow; any
// PostgreSQL-tagged stream that is not complete joins neither arm and fails
// before any artifact exists. The capture contract (exact scope evidence,
// scope mode coherence, restricted row-level path, stable row identity,
// schema digest agreement, exact row count) is enforced with zero port
// touches on every refusal path, and refusal messages name PostgreSQL for
// PostgreSQL sources -- never silently impersonating the AQL arm.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
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

// newExec11Capture compiles a two-output bundle through the PostgreSQL
// target and builds the restricted private-capture stream for "patients".
// The row port serves parent-tagged PostgreSQL commands only and records
// every request; Arango and ClickHouse callbacks stay armed as spies.
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

// exec11PortRows returns rows in the exact shape the real PostgreSQL row
// port serves for the compiled capture command: the declared contract
// columns only (_key, value, auth_resource_path). A stable __loom_row_id is
// derived by the engine from the row's _key plus the Loom-owned scope
// bindings; a port that supplied one would be fabricating identity.
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

// exec11IdentityDigest re-derives the engine's scope-bound row identity:
// the SHA-256 (hex) of the JSON array of the identity field values in
// declaration order, here the Loom-owned project binding then the row key.
func exec11IdentityDigest(project, key string) string {
	encoded, err := json.Marshal([]any{project, key})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
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

// L1: the complete PostgreSQL capture stream captures through the PostgreSQL
// arm with the compiler-attested restricted scope; the AQL and ClickHouse
// arms stay dark.
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
	// The identity is bound to the Loom-owned project: the same _key under a
	// different project scope must not reproduce this digest.
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

// L1: the dispatch is exhaustive and honest. A PostgreSQL-tagged command
// that is not a complete capture source joins neither arm and fails before
// any artifact exists.
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

// L2: the restricted capture contract fails closed, naming PostgreSQL.
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
		// The real PostgreSQL row port serves only the declared contract
		// columns; a row that cannot supply its _key has no stable identity
		// even though the scope bindings supply the project component.
		fixture := newExec11Capture(t, []map[string]any{{"value": "Ada", "auth_resource_path": "/programs/a"}})
		exec11Refusal(t, fixture, `stable row identity field "_key" is missing`)
	})
	t.Run("ScopeEvidenceMissing", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		fixture.stream.scopeEvidence = nil
		exec11Refusal(t, fixture, "private PostgreSQL artifact requires opaque evidence for its exact complete source plan")
	})
}

// L3: the artifact scope mode stays coherent with its evidence.
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

// L4: the supplied artifact schema must be exactly the compiled output
// schema; drift refuses before any writer opens.
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
