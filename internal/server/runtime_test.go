package server

// Offline contract cells for the full PostgreSQL server runtime
// (pg-runtime-server-composition). These run without any database: they pin
// the consumability ladder, the dispatch order, the typed explicit-unavailable
// postures, the Arango-collection honesty of the PostgreSQL authorizer, the
// neutrality of the receipt-materializer seam, and the no-Arango-import law
// of the composition file itself.

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/authscope"
	backendsarango "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/ingest"
)

// The receipt-materializer seam must be satisfied by BOTH registries purely
// structurally: this compile-time pair is the no-backend-decision proof.
var (
	_ receiptExecutionFinder = (*backendsarango.PublishedRegistry)(nil)
	_ receiptExecutionFinder = (*backendspostgres.BundleCatalog)(nil)
)

func fullLadderSchemas() map[string]string {
	return map[string]string{
		"source":                 "loom_source",
		"explorer_control":       "loom_explorer",
		"publication_control":    "loom_publication",
		"dataset_release":        "loom_release",
		"recipe_chains":          "loom_recipe",
		"recipe_exec_registries": "loom_exec",
	}
}

func TestPostgresRuntimeAssignmentAcceptsFullLadder(t *testing.T) {
	assignment, names, err := postgresRuntimeAssignment(PostgreSQLConfig{Schemas: fullLadderSchemas()})
	if err != nil {
		t.Fatalf("the complete six-series ladder must be consumable: %v", err)
	}
	if len(assignment) != 6 || len(names) != 6 {
		t.Fatalf("assignment must carry all six series, got %v", assignment)
	}
	for series, schema := range fullLadderSchemas() {
		if string(assignment[backendspostgres.Series(series)]) != schema {
			t.Fatalf("series %s must bind schema %s, got %v", series, schema, assignment)
		}
		if names[backendspostgres.Series(series)] != schema {
			t.Fatalf("resolved names must mirror the assignment for %s", series)
		}
	}
}

func TestPostgresRuntimeAssignmentKeepsTheSentinelForEveryOtherShape(t *testing.T) {
	cases := []struct {
		name    string
		schemas map[string]string
	}{
		{"absent", nil},
		{"empty", map[string]string{}},
		{"first-query pair is the route's own, not the ladder", map[string]string{"source": "s", "dataset_release": "r"}},
		{"ladder minus one", func() map[string]string {
			s := fullLadderSchemas()
			delete(s, "recipe_exec_registries")
			return s
		}()},
		{"ladder plus foreign", func() map[string]string {
			s := fullLadderSchemas()
			s["warehouse"] = "w"
			return s
		}()},
		{"duplicate schema across two series", func() map[string]string {
			s := fullLadderSchemas()
			s["recipe_chains"] = s["recipe_exec_registries"]
			return s
		}()},
		{"whitespace-colliding pair", func() map[string]string {
			s := fullLadderSchemas()
			s["recipe_chains"] = " loom_exec "
			return s
		}()},
		{"blank member", func() map[string]string {
			s := fullLadderSchemas()
			s["publication_control"] = "  "
			return s
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := postgresRuntimeAssignment(PostgreSQLConfig{Schemas: tc.schemas}); !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
				t.Fatalf("schemas %v must keep the byte-stable not-wired sentinel, got %v", tc.schemas, err)
			}
		})
	}
}

// TestPostgresRuntimeDispatchReachesThePostgreSQLPathPreDial proves the
// three-way dispatch selects the full runtime for a ladder config WITHOUT
// dialing: an unsafe schema identifier must surface the factory's own pre-dial
// identifier law -- never the consumability sentinel (the shape IS
// consumable) and never an ArangoDB path. 192.0.2.1 is TEST-NET-1: the pass
// condition is the named refusal arriving long before the deadline.
func TestPostgresRuntimeDispatchReachesThePostgreSQLPathPreDial(t *testing.T) {
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host: "192.0.2.1", Port: 60543, Database: "loom", User: "loom",
		Password: "pgcomp-Canary-Pa55w0rd", SSLMode: "require",
		MaxOpenConns: 2, MaxIdleConns: 1,
		Schemas: map[string]string{
			"source":                 "Loom Source", // unsafe: identifier law
			"explorer_control":       "loom_explorer",
			"publication_control":    "loom_publication",
			"dataset_release":        "loom_release",
			"recipe_chains":          "loom_recipe",
			"recipe_exec_registries": "loom_exec",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*1024*1024*1024)
	defer cancel()
	err := RunWithConfig(ctx, cfg)
	if err == nil {
		t.Fatal("ladder config unexpectedly started the server")
	}
	if errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
		t.Fatalf("a consumable ladder was refused by the not-wired sentinel: %v", err)
	}
	if strings.Contains(err.Error(), cfg.Server.PostgreSQL.Password) {
		t.Fatalf("refusal leaked the connection password: %v", err)
	}
	if !strings.Contains(err.Error(), "Loom Source") && !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error %q must name the unsafe schema identifier or the schema law", err)
	}
}

func TestPostgresRuntimeExplorerStoreTypedRefusals(t *testing.T) {
	store := pgExplorerStore{ExplorerStore: &backendspostgres.ExplorerStore{}}
	if err := store.ActivateRepositoryGeneration(context.Background(), "p", "g", "r"); err == nil {
		t.Fatal("repository-generation activation must refuse without an Arango fall-through")
	} else if !isTypedExplicitUnavailable(err, "Repository-generation activation") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
	if err := store.ValidateSelectionReferences(context.Background(), "p", "g", authscope.ReadScope{}, nil); err == nil {
		t.Fatal("selection-reference validation must refuse without an Arango fall-through")
	} else if !isTypedExplicitUnavailable(err, "Explicit-selection reference validation") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
	if _, err := store.PublishAuthoring(context.Background(), explorer.CompilationReceipt{}, explorer.Revision{}, publicationcontract.ProjectRelease{}, 0); err == nil {
		t.Fatal("publication without the dataset_release assignment must refuse")
	} else if !isTypedExplicitUnavailable(err, "Explorer publication") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
}

// isTypedExplicitUnavailable asserts the named typed posture produced by
// postgreSQLRuntimeUnavailable: BACKEND_UNAVAILABLE, retryable=false,
// postgresql-tagged details.
func isTypedExplicitUnavailable(err error, operationSubstring string) bool {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return false
	}
	if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		return false
	}
	if userErr.Retryable() {
		return false
	}
	if !strings.Contains(userErr.Error(), operationSubstring) {
		return false
	}
	backend, _ := userErr.Details()["backend"].(string)
	return backend == "postgresql"
}

// TestPostgresRuntimeAuthorizerNeverStampsTheArangoCollection is the
// wired-not-vacuous contrast: the Arango lane's authorizer stamps the Arango
// selection-members collection onto every binding; the PostgreSQL authorizer
// must leave the field empty on both read and write.
func TestPostgresRuntimeAuthorizerNeverStampsTheArangoCollection(t *testing.T) {
	pgRead, err := pgRecipeAuthorization{}.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("unrestricted read authorization: %v", err)
	}
	if pgRead.SelectionMembersCollection != "" {
		t.Fatalf("the PostgreSQL authorizer must leave SelectionMembersCollection empty, got %q", pgRead.SelectionMembersCollection)
	}
	if pgRead.SelectionProject != "p1" {
		t.Fatalf("SelectionProject must be the canonical project, got %q", pgRead.SelectionProject)
	}
	pgWrite, err := pgRecipeAuthorization{}.AuthorizeWrite(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("unrestricted write authorization: %v", err)
	}
	if pgWrite.SelectionMembersCollection != "" {
		t.Fatalf("the PostgreSQL write authorizer must leave SelectionMembersCollection empty, got %q", pgWrite.SelectionMembersCollection)
	}
	// The contrast half: same package, same input, Arango lane stamps the
	// collection. Without this half the assertion above could pass on a
	// broken authorizer that simply drops every binding field.
	arangoRead, err := recipeAuthorization{}.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("arango-lane read authorization: %v", err)
	}
	if arangoRead.SelectionMembersCollection == "" {
		t.Fatal("the Arango lane must keep stamping its selection-members collection; the contrast fixture went vacuous")
	}
}

// TestPostgresRuntimeCompositionHasNoArangoImport is the source-level proof
// of the no-fall-through law for the composition file: internal/server/
// runtime.go may not import, mention, or construct any ArangoDB adapter or
// AQL surface.
func TestPostgresRuntimeCompositionHasNoArangoImport(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatalf("read the composition source: %v", err)
	}
	text := string(source)
	importBlock := text
	if start := strings.Index(text, "import ("); start >= 0 {
		if end := strings.Index(text[start:], "\n)"); end >= 0 {
			importBlock = text[start : start+end]
		}
	}
	// One owned factory package holds every ArangoDB adapter tree
	// (arch-backend-07); the PostgreSQL composition must import none of it.
	// The AQL renderer stayed in the compiler tree and is forbidden too.
	forbidden := []string{
		"backends/arango",
		"compiler/render/aql",
	}
	for _, path := range forbidden {
		if strings.Contains(importBlock, path) {
			t.Fatalf("the PostgreSQL composition must not import %s", path)
		}
	}
	for _, token := range []string{"arangostore.", "AQLParameters"} {
		if strings.Contains(text, token) {
			t.Fatalf("the PostgreSQL composition must not mention %s (no AQL/Arango surface on the PostgreSQL lane)", token)
		}
	}
}

// ---- closer ordering law (compose must not own deferred cleanup) ----

// TestPostgresRuntimeComposeOwnsNoDeferredCleanup is the witness for the
// close-ordering law: composePostgreSQLFullRuntime returns while the server
// is still about to Listen, so nothing that must outlive compose may be
// deferred in its body. The pre-fix shape deferred clickhouse.Close() and
// both reconciler cancel-and-wait closures inside compose, closing the
// client and cancelling the reconcilers before a single request was served.
// This cell goes red if any defer reappears directly in the compose body:
// exactly one defer is legal -- the zero-argument error-path sweep that
// drains the cleanups stack, as the first statement.
func TestPostgresRuntimeComposeOwnsNoDeferredCleanup(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatalf("read the composition source: %v", err)
	}
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "runtime.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse the composition source: %v", err)
	}
	var compose *ast.FuncDecl
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "composePostgreSQLFullRuntime" {
			compose = fn
		}
	}
	if compose == nil {
		t.Fatal("composePostgreSQLFullRuntime is missing from the composition source")
	}
	var bodyDefers []*ast.DeferStmt
	var insideFuncLit []bool
	ast.Inspect(compose.Body, func(n ast.Node) bool {
		if n == nil {
			insideFuncLit = insideFuncLit[:len(insideFuncLit)-1]
			return false
		}
		nested := len(insideFuncLit) > 0 && insideFuncLit[len(insideFuncLit)-1]
		_, isLiteral := n.(*ast.FuncLit)
		insideFuncLit = append(insideFuncLit, nested || isLiteral)
		if stmt, ok := n.(*ast.DeferStmt); ok && !nested {
			bodyDefers = append(bodyDefers, stmt)
		}
		return true
	})
	if len(bodyDefers) != 1 {
		positions := make([]string, 0, len(bodyDefers))
		for _, d := range bodyDefers {
			positions = append(positions, fileSet.Position(d.Pos()).String())
		}
		t.Fatalf("compose must own exactly one deferred cleanup sweep (resources that outlive compose belong to the returned closer); found %d at %v", len(bodyDefers), positions)
	}
	sweep := bodyDefers[0]
	for _, before := range compose.Body.List {
		if before == sweep {
			break
		}
		decl, isDecl := before.(*ast.DeclStmt)
		if !isDecl {
			t.Fatalf("nothing may run before the deferred cleanup sweep (it must precede every acquisition), found %T at %s", before, fileSet.Position(before.Pos()).String())
		}
		gen, ok := decl.Decl.(*ast.GenDecl)
		if !ok {
			t.Fatalf("statements before the sweep must be plain declarations, found %T at %s", decl.Decl, fileSet.Position(before.Pos()).String())
		}
		for _, spec := range gen.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok && len(value.Values) != 0 {
				t.Fatalf("statements before the sweep must be bare declarations (an initialized var can acquire); found initializer at %s", fileSet.Position(spec.Pos()).String())
			}
		}
	}
	literal, ok := sweep.Call.Fun.(*ast.FuncLit)
	if !ok || len(literal.Type.Params.List) != 0 || literal.Type.Results != nil {
		t.Fatalf("the deferred sweep must be a zero-argument closure, got %s", fileSet.Position(sweep.Pos()).String())
	}
	if !expressionReferences(literal.Body, "cleanups") {
		t.Fatal("the deferred sweep must drain the cleanups stack")
	}
	if !expressionReferences(literal.Body, "runRuntimeCloseSteps") {
		t.Fatal("the deferred sweep must tear down through runRuntimeCloseSteps (one ordering implementation for both paths)")
	}
	// The full composition must hand its closer the four acquired resources:
	// pool, ClickHouse client, private-artifact reconciler, artifact-fs
	// reconciler. A dropped push (e.g. the ClickHouse close deleted instead
	// of moved) fails here as well.
	composeText := string(source)[fileSet.Position(compose.Pos()).Offset:fileSet.Position(compose.End()).Offset]
	if got := strings.Count(composeText, "cleanups = append("); got != 4 {
		t.Fatalf("the full composition must push exactly four closer steps (pool, ClickHouse client, private-artifact reconciler, artifact-fs reconciler), found %d", got)
	}
	if !strings.Contains(composeText, "closeRuntime = func() error { return runRuntimeCloseSteps(cleanups) }") {
		t.Fatal("the returned closer must be the reverse-order cleanups drain")
	}
}

func expressionReferences(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

// TestPostgresRuntimeCloseStepsTearDownInReverseAndKeepFirstError pins the
// shared teardown semantics: reverse acquisition order, every step runs
// even after a failure, and the first failure encountered (the
// most-recently-acquired failing resource) is the reported one.
func TestPostgresRuntimeCloseStepsTearDownInReverseAndKeepFirstError(t *testing.T) {
	var order []int
	errAcquiredThird := errors.New("third-acquired step failed")
	errAcquiredSecond := errors.New("second-acquired step failed")
	steps := []func() error{
		func() error { order = append(order, 0); return nil },
		func() error { order = append(order, 1); return errAcquiredSecond },
		func() error { order = append(order, 2); return nil },
		func() error { order = append(order, 3); return errAcquiredThird },
		func() error { order = append(order, 4); return nil },
	}
	if err := runRuntimeCloseSteps(steps); !errors.Is(err, errAcquiredThird) {
		t.Fatalf("teardown runs newest-first, so the most recently acquired failure (step 3) must be reported, got %v", err)
	}
	if want := []int{4, 3, 2, 1, 0}; len(order) != len(want) {
		t.Fatalf("every step must run even after a failure: wanted %v, got %v", want, order)
	} else {
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("teardown must run in reverse acquisition order: wanted %v, got %v", want, order)
			}
		}
	}
	if err := runRuntimeCloseSteps(nil); err != nil {
		t.Fatalf("an empty teardown must report no failure, got %v", err)
	}
}

// ---- activation outcome classification (CLI-parity law) ----

// activationMatrixStore is a ManifestLifecycle double that only answers the
// two calls the activation path makes: the active-pointer read and the CAS
// itself. Anything else means the activator grew an uncontracted store call
// and the cell crashes on it.
type activationMatrixStore struct {
	activateErr error
}

func (s activationMatrixStore) CreateManifest(context.Context, publicationcontract.Manifest) (publicationcontract.Manifest, error) {
	panic("CreateManifest is outside the activation path")
}

func (s activationMatrixStore) ReadManifest(context.Context, publicationcontract.Ref) (publicationcontract.Manifest, error) {
	panic("ReadManifest is outside the activation path")
}

func (s activationMatrixStore) TransitionManifest(context.Context, publicationcontract.Manifest, publicationcontract.State) (publicationcontract.Manifest, error) {
	panic("TransitionManifest is outside the activation path")
}

func (s activationMatrixStore) ResolveActiveManifest(context.Context, string) (publicationcontract.Manifest, error) {
	return publicationcontract.Manifest{}, publicationcontract.ErrNoActiveGeneration
}

func (s activationMatrixStore) ActivateGeneration(_ context.Context, manifest publicationcontract.Manifest, expected *publicationcontract.Ref) error {
	if !manifest.IsStaged() {
		return errors.New("matrix fixture: manifest must reach the CAS staged")
	}
	if expected != nil {
		return errors.New("matrix fixture: no active generation means a nil expected pointer")
	}
	return s.activateErr
}

func stagedActivationManifest(t *testing.T) publicationcontract.Manifest {
	t.Helper()
	ref, err := publicationcontract.NewRef("acme", "gen-1")
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	snapshot, err := publicationcontract.NewSchemaSnapshot("", "", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("build schema snapshot: %v", err)
	}
	manifest, err := publicationcontract.NewManifest(ref, snapshot)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	staged, err := manifest.Transition(publicationcontract.StateStaged)
	if err != nil {
		t.Fatalf("stage manifest: %v", err)
	}
	return staged
}

type activationTimeoutError struct{}

func (activationTimeoutError) Error() string   { return "activation network timeout" }
func (activationTimeoutError) Timeout() bool   { return true }
func (activationTimeoutError) Temporary() bool { return false }

var _ net.Error = activationTimeoutError{}

// TestPostgresRuntimeActivationOutcomeClassification is the CLI-parity
// cell for pgGenerationActivator.Activate: every error class that the CLI
// lane's pgCommitOutcomeUnknown treats as commit-outcome-unknown must
// surface wrapped as *ingest.ActivationOutcomeError, and every known typed
// refusal must surface unchanged. Narrowing the mapping back to the single
// ErrCommitOutcomeUnknown sentinel -- the pre-fix shape -- fails every
// connectivity-class row; broadening it to wrap-everything fails the
// known-refusal rows.
func TestPostgresRuntimeActivationOutcomeClassification(t *testing.T) {
	manifest := stagedActivationManifest(t)
	outcomeUnknownCases := []struct {
		name string
		err  error
	}{
		{"store commit-outcome-unknown sentinel", backendspostgres.ErrCommitOutcomeUnknown},
		{"connect sentinel", fmt.Errorf("dial postgres: %w", backendspostgres.ErrConnect)},
		{"batch-write sentinel", fmt.Errorf("copy rows: %w", backendspostgres.ErrBatchWrite)},
		{"sqlstate connection exception", &pgconn.PgError{Code: "08006", Message: "connection failure"}},
		{"sqlstate admin shutdown", &pgconn.PgError{Code: "57P01", Message: "admin shutdown"}},
		{"sqlstate crash shutdown", &pgconn.PgError{Code: "57P02", Message: "crash shutdown"}},
		{"context cancellation", fmt.Errorf("activate cas: %w", context.Canceled)},
		{"context deadline exceeded", fmt.Errorf("activate cas: %w", context.DeadlineExceeded)},
		{"network timeout", activationTimeoutError{}},
		{"eof mid-commit", io.EOF},
		{"unexpected eof mid-commit", io.ErrUnexpectedEOF},
	}
	for _, tc := range outcomeUnknownCases {
		err := pgGenerationActivator{manifests: activationMatrixStore{activateErr: tc.err}}.Activate(context.Background(), manifest)
		var outcome *ingest.ActivationOutcomeError
		if !errors.As(err, &outcome) {
			t.Fatalf("%s: connectivity-class activation failure must surface as *ingest.ActivationOutcomeError (CLI parity), got %v", tc.name, err)
		}
		if !errors.Is(outcome.Err, tc.err) {
			t.Fatalf("%s: the underlying failure must stay wrapped for the audit, got %v", tc.name, outcome.Err)
		}
		if outcome.Dataset != manifest.Dataset {
			t.Fatalf("%s: the outcome error must name the dataset generation, got %+v", tc.name, outcome.Dataset)
		}
	}
	knownRefusalCases := []struct {
		name string
		err  error
	}{
		{"activation conflict", backendspostgres.ErrActivationConflict},
		{"generation not ready", publicationcontract.ErrGenerationNotReady},
		{"invalid active generation", publicationcontract.ErrInvalidActiveGeneration},
		{"conflict outranks connectivity", fmt.Errorf("%w: %w", backendspostgres.ErrActivationConflict, backendspostgres.ErrConnect)},
		{"plain store refusal", errors.New("no such staged manifest")},
	}
	for _, tc := range knownRefusalCases {
		err := pgGenerationActivator{manifests: activationMatrixStore{activateErr: tc.err}}.Activate(context.Background(), manifest)
		if !errors.Is(err, tc.err) {
			t.Fatalf("%s: known typed refusal must surface unchanged, got %v", tc.name, err)
		}
		var outcome *ingest.ActivationOutcomeError
		if errors.As(err, &outcome) {
			t.Fatalf("%s: known typed refusal must not be laundered into an outcome-unknown audit", tc.name)
		}
	}
}
