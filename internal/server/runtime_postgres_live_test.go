package server

// Live battery for the full PostgreSQL server runtime
// (pg-runtime-server-composition). Mirrors the first-query-route harness:
// own verify_pgcomp_* census, enumerated mint inventory, TestZZ zero-leftover
// audit (N-5: foreign prefixes are never deleted or attributed), and every
// leg boots the PRODUCTION composition (composePostgreSQLFullRuntime) and
// drives it through the production handler stack. The PostgreSQL legs never
// skip when LOOM_TEST_POSTGRES_URL is set; they fail loud.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

const (
	pgCompDBFmt     = "verify_pgcomp_%d_%s"
	pgCompSchemaFmt = "verify_pgcomp_%d_%s_%s"
	pgCompProject   = "pgcomp-p1"
	pgCompGenA      = "generation-a"
	pgCompAppTag    = "verify-pgcomp-server"
	// firstQueryRouteDeferral is the marker every deliberate refusal of the
	// first-query route carries (postgreSQLFirstQueryRouteWiringError). No
	// response from the FULL runtime may ever carry it: the ladder composed
	// past the route, and a deferral resurfacing means a stub came back.
	firstQueryRouteDeferral = "first query route"
)

// Six-series placement: one minted schema per series inside one minted
// database. The tokens are fixed so the census regex can enumerate them.
var pgCompSeriesTokens = []struct {
	series backendspostgres.Series
	token  string
}{
	{backendspostgres.SeriesSource, "src"},
	{backendspostgres.SeriesExplorerControl, "exp"},
	{backendspostgres.SeriesPublicationControl, "pub"},
	{backendspostgres.SeriesDatasetRelease, "rel"},
	{backendspostgres.SeriesRecipeChains, "rcp"},
	{backendspostgres.SeriesRecipeExecRegistries, "exe"},
}

var (
	pgCompDBPattern     = regexp.MustCompile(`^verify_pgcomp_[0-9]+_[0-9a-f]+$`)
	pgCompSchemaPattern = regexp.MustCompile(`^verify_pgcomp_[0-9]+_[0-9a-f]+_(src|exp|pub|rel|rcp|exe)$`)
	pgCompOwnedMu       sync.Mutex
	pgCompOwned         = map[string]pgCompMint{}
	pgCompStrayMarks    = []string{}
	pgCompBaselineMu    sync.Mutex
	pgCompBaseline      int64
	pgCompBaselineSet   bool
	pgCompBaselineErr   error
)

type pgCompMint struct {
	leg     string
	dropped bool
}

// pgCompMintInventory is the checked-in enumeration of this file's minting
// legs. The TestZZ audit asserts the runtime registry matches it leg-for-leg.
var pgCompMintInventory = map[string]int{
	"TestPostgresRuntimeFullLadderServesProductionReads": 1,
	"TestPostgresRuntimeServerLoadRouteServesEndToEnd":   1,
}

func pgCompTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL full-runtime integration tests")
	}
	return raw
}

func pgCompRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func pgCompRegister(db, leg string) {
	pgCompOwnedMu.Lock()
	defer pgCompOwnedMu.Unlock()
	pgCompOwned[db] = pgCompMint{leg: leg}
}

func pgCompMarkDropped(db string) {
	pgCompOwnedMu.Lock()
	defer pgCompOwnedMu.Unlock()
	rec, ok := pgCompOwned[db]
	if !ok {
		pgCompStrayMarks = append(pgCompStrayMarks, db)
		return
	}
	rec.dropped = true
	pgCompOwned[db] = rec
}

func pgCompCaptureBaseline(t *testing.T, raw string) {
	t.Helper()
	pgCompBaselineMu.Lock()
	defer pgCompBaselineMu.Unlock()
	if pgCompBaselineSet {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		pgCompBaselineErr = fmt.Errorf("connect for baseline: %w", err)
		return
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_pgcomp_[0-9]+_[0-9a-f]+$'").
		Scan(&count); err != nil {
		pgCompBaselineErr = fmt.Errorf("count baseline: %w", err)
		return
	}
	pgCompBaseline = count
	pgCompBaselineSet = true
}

type pgCompFixture struct {
	dsn      string
	schemas  map[backendspostgres.Series]string
	scrub    func(any) string
	secrets  []string
	admin    *pgxpool.Pool
	database string
}

func newPgCompFixture(t *testing.T) *pgCompFixture {
	t.Helper()
	raw := pgCompTestPostgresURL(t)
	pgCompCaptureBaseline(t, raw)
	if pgCompBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", pgCompBaselineErr)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %s", scrub(err))
	}
	t.Cleanup(admin.Close)

	database := fmt.Sprintf(pgCompDBFmt, time.Now().UnixNano(), pgCompRandomHex(t))
	if !pgCompDBPattern.MatchString(database) {
		t.Fatalf("minted database %q violates the owned-name pattern", database)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatalf("create isolated database: %s", scrub(err))
	}
	pgCompRegister(database, t.Name())
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+database+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
			return
		}
		pgCompMarkDropped(database)
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
	}
	u.Path = "/" + database
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated database: %s", scrub(err))
	}
	defer iso.Close()
	schemas := map[backendspostgres.Series]string{}
	for _, placement := range pgCompSeriesTokens {
		schema := fmt.Sprintf(pgCompSchemaFmt, time.Now().UnixNano(), pgCompRandomHex(t), placement.token)
		if !pgCompSchemaPattern.MatchString(schema) {
			t.Fatalf("minted schema %q violates the owned-name pattern", schema)
		}
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatalf("create owned schema for %s: %s", placement.series, scrub(err))
		}
		schemas[placement.series] = schema
	}
	return &pgCompFixture{dsn: dsn, schemas: schemas, scrub: scrub, secrets: secrets, admin: admin, database: database}
}

// migrate bootstraps all six series through the public factory
// (SetupMigrate). The composition itself opens with SetupNone, so migrating
// here is what makes a schema "arranged" the way the factory's law requires.
func (f *pgCompFixture) migrate(t *testing.T) {
	t.Helper()
	runtime, err := backendspostgres.Open(context.Background(), backendspostgres.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas:        backendspostgres.SeriesSchemas(f.schemas),
		Setup:          backendspostgres.SetupMigrate,
	})
	if err != nil {
		t.Fatalf("migrate six-series fixture: %s", f.scrub(err))
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close migrated fixture runtime: %s", f.scrub(err))
	}
}

// catalogRuntime opens the factory for the four catalog evidence writers the
// ingest lane requires, exactly as the loom-data CLI composes them.
func (f *pgCompFixture) catalogRuntime(t *testing.T) *backendspostgres.Runtime {
	t.Helper()
	runtime, err := backendspostgres.Open(context.Background(), backendspostgres.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas:        backendspostgres.SeriesSchemas(f.schemas),
		Setup:          backendspostgres.SetupNone,
	})
	if err != nil {
		t.Fatalf("open catalog fixture runtime: %s", f.scrub(err))
	}
	if runtime.FieldProfiles == nil || runtime.Inventory == nil || runtime.Relationships == nil || runtime.Contributions == nil {
		_ = runtime.Close()
		t.Fatal("fixture runtime assembled without catalog evidence ports")
	}
	return runtime
}

func (f *pgCompFixture) pgOptions(application string) ingest.PGSourceOptions {
	return ingest.PGSourceOptions{
		DSN:             f.dsn,
		Schema:          f.schemas[backendspostgres.SeriesSource],
		ManifestSchema:  f.schemas[backendspostgres.SeriesDatasetRelease],
		ApplicationName: application,
	}
}

// load stages one generation through the production ingest entry point, the
// same shape the loom-data CLI takes.
func (f *pgCompFixture) load(t *testing.T, project, generation string, files map[string][]string) {
	t.Helper()
	dir := t.TempDir()
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write staging fixture: %v", err)
		}
	}
	catalog := f.catalogRuntime(t)
	defer func() { _ = catalog.Close() }()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	opts := ingest.LoadOptions{
		Schema:           pgCompRepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:          dir,
		Project:          project,
		Dataset:          &ref,
		AuthResourcePath: "/fhir",
		StageOnly:        true,
	}
	pg := f.pgOptions(pgCompAppTag)
	pg.Catalog = ingest.GenerationCatalogWriters{
		FieldProfiles: catalog.FieldProfiles,
		Inventory:     catalog.Inventory,
		Relationships: catalog.Relationships,
		Contributions: catalog.Contributions,
	}
	if _, err := ingest.LoadPostgresGeneration(context.Background(), opts, pg); err != nil {
		t.Fatalf("load %s/%s: %s", project, generation, f.scrub(err))
	}
}

func (f *pgCompFixture) activate(t *testing.T, project, generation string) {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	if err := ingest.ActivatePostgresGeneration(context.Background(), ref, f.pgOptions(pgCompAppTag+"-activate")); err != nil {
		t.Fatalf("activate %s/%s: %s", project, generation, f.scrub(err))
	}
}

func pgCompRepoPath(t *testing.T, elems ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file for repository root")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, elems...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test file")
		}
		dir = parent
	}
}

// pgCompBootConfig turns the live fixture into the consumable full-ladder
// configuration. The server.schema stays the repository graph schema (the
// load route feeds it to graph.Load; a schema NAME would be a defect the
// load leg below would catch).
func pgCompBootConfig(t *testing.T, f *pgCompFixture, listen string) Config {
	t.Helper()
	u, err := url.Parse(f.dsn)
	if err != nil {
		t.Fatalf("parse fixture dsn: %v", err)
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split fixture dsn host: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("fixture dsn port %q: %v", portText, err)
	}
	password := ""
	if pw, ok := u.User.Password(); ok {
		password = pw
	}
	sslMode := u.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.Listen = listen
	cfg.Server.AllowUnauthenticated = true
	cfg.Server.Schema = pgCompRepoPath(t, "schemas", "graph-fhir.json")
	cfg.Server.ArtifactDirectory = t.TempDir()
	cfg.Server.PopulationMappingCursorSecret = "pgcomp-cursor-secret-0123456789ab"
	schemas := map[string]string{}
	for _, placement := range pgCompSeriesTokens {
		schemas[string(placement.series)] = f.schemas[placement.series]
	}
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host: host, Port: port, Database: strings.TrimPrefix(u.Path, "/"),
		User: u.User.Username(), Password: password, SSLMode: sslMode,
		MaxOpenConns: 8, MaxIdleConns: 2,
		Schemas: schemas,
	}
	return cfg
}

func pgCompPatientLines(t *testing.T, ids ...string) []string {
	t.Helper()
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		encoded, err := json.Marshal(map[string]any{
			"resourceType": "Patient", "id": id,
			"gender": "female", "birthDate": "1900-01-01",
		})
		if err != nil {
			t.Fatalf("marshal patient fixture: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	return lines
}

func pgCompGraphQL(t *testing.T, app *fiber.App, body string) testHTTPResponse {
	t.Helper()
	return requestJSON(t, app, http.MethodPost, "/graphql/graph", body)
}

// pgCompAssertNoLaneLeak is the shared honesty check for every response this
// battery inspects: no Arango surface, no AQL tell, and no resurfaced
// first-query-route deferral on the full runtime.
func pgCompAssertNoLaneLeak(t *testing.T, f *pgCompFixture, what, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, tell := range []string{"arango", "aql", "for d in ", "@auth_resource_paths", "fhir_field_catalog"} {
		if strings.Contains(lower, tell) {
			t.Fatalf("%s leaks a non-PostgreSQL surface (%q): %s", what, tell, f.scrub(body))
		}
	}
	if strings.Contains(lower, firstQueryRouteDeferral) {
		t.Fatalf("%s resurfaced the first-query-route deferral on the full runtime: %s", what, f.scrub(body))
	}
}

// TestPostgresRuntimeFullLadderServesProductionReads boots the full
// composition against a live six-series fixture (gen-A loaded+active through
// the CLI-parity ingest lane) and proves the ladder serves: the read lane
// answers exact scalars, generation status reads from the manifest store,
// and the capabilities the first-query route deferred no longer carry its
// deferral (they reach the wired engine and answer on their own merits).
func TestPostgresRuntimeFullLadderServesProductionReads(t *testing.T) {
	fixture := newPgCompFixture(t)
	fixture.migrate(t)
	fixture.load(t, pgCompProject, pgCompGenA, map[string][]string{
		"Patient.ndjson": pgCompPatientLines(t, "patient-ada", "patient-alan"),
	})
	fixture.activate(t, pgCompProject, pgCompGenA)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, pgCompBootConfig(t, fixture, "127.0.0.1:0"), logger)
	if err != nil {
		t.Fatalf("compose full runtime: %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close full runtime: %s", fixture.scrub(err))
		}
	}()
	app := server.App()

	t.Run("generation_status_is_a_live_manifest_read", func(t *testing.T) {
		response := requestJSON(t, app, http.MethodGet, "/api/v1/datasets/"+pgCompProject+"/generations/"+pgCompGenA, "")
		pgCompAssertNoLaneLeak(t, fixture, "generation status", response.Body)
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Body, pgCompGenA) {
			t.Fatalf("active-generation status = %d %s, want 200 naming %s", response.StatusCode, fixture.scrub(response.Body), pgCompGenA)
		}
	})

	t.Run("run_fhir_dataframe_serves_exact_scalars", func(t *testing.T) {
		body := `{"query":"mutation { runFhirDataframe(input: {project: \"` + pgCompProject + `\", rootResourceType: \"Patient\", rootFields: [{name: \"gender\", selector: {valuePath: \"gender\"}}, {name: \"birthDate\", selector: {valuePath: \"birthDate\"}}]}) { columns rowCount rows } }"}`
		response := pgCompGraphQL(t, app, body)
		pgCompAssertNoLaneLeak(t, fixture, "runFhirDataframe", response.Body)
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Body, "\"rowCount\":2") || strings.Contains(response.Body, "\"errors\"") {
			t.Fatalf("runFhirDataframe must serve 2 rows from PostgreSQL, got %d %s", response.StatusCode, fixture.scrub(response.Body))
		}
	})

	// The first-query route pinned these as BACKEND_UNAVAILABLE wiring
	// refusals (retryable=false). The full runtime wires the recipe engine
	// and the reference-backed traversal lane stays a typed refusal of the
	// compiler/graph path -- but never the route deferral and never an
	// Arango surface, which pgCompAssertNoLaneLeak enforces on every leg.
	graphqlLegs := []struct{ name, query string }{
		{"recipe_validate", `{"query":"mutation { validateDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + pgCompProject + `\"}}) { name } }"}`},
		{"recipe_explain", `{"query":"query { explainDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + pgCompProject + `\"}}) { name } }"}`},
		{"fhirGraph_traversal", `{"query":"query { fhirGraph(input: {project: \"` + pgCompProject + `\", rootResourceType: \"Patient\"}) { returnedCount } }"}`},
	}
	for _, leg := range graphqlLegs {
		leg := leg
		t.Run(leg.name, func(t *testing.T) {
			response := pgCompGraphQL(t, app, leg.query)
			t.Logf("%s full-runtime answer: status=%d body=%s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			pgCompAssertNoLaneLeak(t, fixture, leg.name, response.Body)
			if response.StatusCode >= 500 && response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("%s returned an untyped server fault %d: %s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			}
		})
	}

	// Live witness for the closer ordering law, gated on an owned
	// ClickHouse (LOOM_CLICKHOUSE_URL; the leg is absent -- not skipped --
	// when the environment does not own one, so the registered skip-set is
	// unchanged). compose has already returned here, so a ClickHouse client
	// deferred closed inside compose would be closed while readiness runs:
	// the Ping inside ClickHouseReadyCheck fails and readiness reports
	// 503/backend_unavailable instead of 200/ready. With the client owned
	// by the returned closer, readiness stays ready for the whole serve.
	if clickhouseURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL")); clickhouseURL != "" {
		t.Run("clickhouse_client_survives_compose_return", func(t *testing.T) {
			chConfig := pgCompBootConfig(t, fixture, "127.0.0.1:0")
			chConfig.Server.ClickHouse = ClickHouseConfig{
				Enabled:  true,
				URL:      clickhouseURL,
				Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"),
				Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
				Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
			}
			chServer, chClose, err := composePostgreSQLFullRuntime(ctx, chConfig, logger)
			if err != nil {
				t.Fatalf("compose ClickHouse-enabled full runtime: %s", fixture.scrub(err))
			}
			defer func() {
				if err := chClose(); err != nil {
					t.Errorf("close ClickHouse-enabled full runtime: %s", fixture.scrub(err))
				}
			}()
			body, status := chServer.Readiness(ctx)
			dataframe, _ := body["dataframe"].(string)
			if status != http.StatusOK || body["status"] != "ready" || dataframe != "ready" {
				t.Fatalf("ClickHouse-enabled readiness after compose returned = %d %#v; want 200 ready/ready (a client closed by compose instead of the closer fails the Ping)", status, body)
			}
		})
	}
}

// TestPostgresRuntimeServerLoadRouteServesEndToEnd is the headline delta of
// the composition over the first-query route: the load route now runs the
// ingest lane's own PostgreSQL entry point through the server (the route
// used to answer with the CLI-deferral 503). The generation loads AND
// activates through the production handler stack, the manifest store reports
// it active, and the read lane serves the row the server itself loaded.
func TestPostgresRuntimeServerLoadRouteServesEndToEnd(t *testing.T) {
	fixture := newPgCompFixture(t)
	fixture.migrate(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, pgCompBootConfig(t, fixture, "127.0.0.1:0"), logger)
	if err != nil {
		t.Fatalf("compose full runtime: %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close full runtime: %s", fixture.scrub(err))
		}
	}()
	app := server.App()

	generation := "generation-server-loaded"
	body, boundary := pgCompMultipart(t,
		map[string]string{"auth_resource_path": "/fhir"},
		map[string]string{
			"Patient.ndjson": "{\"resourceType\":\"Patient\",\"id\":\"patient-server-loaded\",\"gender\":\"male\",\"birthDate\":\"1988-02-02\"}\n",
		})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/"+pgCompProject+"/generations/"+generation, bytes.NewReader(body))
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	response, err := app.Test(request, fiber.TestConfig{Timeout: 60 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	text := string(raw)
	t.Logf("server-load answer: status=%d body=%s", response.StatusCode, fixture.scrub(text))
	pgCompAssertNoLaneLeak(t, fixture, "server-side load", text)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("server-side load must serve through the PostgreSQL ingest lane, got %d %s", response.StatusCode, fixture.scrub(text))
	}

	// Direct witness that the server's load route ran the CLI-parity
	// activation: the project's active POINTER names the server-loaded
	// generation (activation law: pointer moves, manifest row stays STAGED).
	{
		witness := fixture.catalogRuntime(t)
		defer func() { _ = witness.Close() }()
		active, err := witness.Manifests.ResolveActiveManifest(context.Background(), pgCompProject)
		if err != nil {
			t.Fatalf("resolve active pointer after server-side load: %s", fixture.scrub(err))
		}
		if active.Dataset.Generation != generation {
			t.Fatalf("active pointer = %s, want the server-loaded %s", active.Dataset.Generation, generation)
		}
	}

	// The PostgreSQL manifest store keeps the row STAGED and activates by
	// moving the project's active POINTER (ResolveActiveManifest), so the
	// status endpoint honestly reports state=STAGED with reusable=true for a
	// server-loaded generation on both lanes. ACTIVE-ness is proven by the
	// read lane below, which resolves through the active manifest pointer.
	status := requestJSON(t, app, http.MethodGet, "/api/v1/datasets/"+pgCompProject+"/generations/"+generation, "")
	pgCompAssertNoLaneLeak(t, fixture, "loaded-generation status", status.Body)
	if status.StatusCode != http.StatusOK || !strings.Contains(status.Body, generation) ||
		!strings.Contains(status.Body, "\"reusable\":true") {
		t.Fatalf("server-loaded generation must be a persisted reusable manifest, got %d %s", status.StatusCode, fixture.scrub(status.Body))
	}

	query := `{"query":"mutation { runFhirDataframe(input: {project: \"` + pgCompProject + `\", rootResourceType: \"Patient\", rootFields: [{name: \"gender\", selector: {valuePath: \"gender\"}}]}) { rowCount rows } }"}`
	served := pgCompGraphQL(t, app, query)
	pgCompAssertNoLaneLeak(t, fixture, "server-loaded read", served.Body)
	if served.StatusCode != http.StatusOK || !strings.Contains(served.Body, "\"rowCount\":1") ||
		!strings.Contains(served.Body, "patient-server-loaded") {
		t.Fatalf("the read lane must serve what the server loaded, got %d %s", served.StatusCode, fixture.scrub(served.Body))
	}
}

// TestPgCompCensusVocabularyCorrespondence pins offline that every name this
// file can mint matches its own census regex, near-miss shapes never minted
// do not, and the q-route / query-package vocabularies stay disjoint from
// this census (the separation that keeps parallel live batteries race-free).
func TestPgCompCensusVocabularyCorrespondence(t *testing.T) {
	hex := func() string {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatalf("sample suffix: %v", err)
		}
		return fmt.Sprintf("%x", b[:])
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf(pgCompDBFmt, time.Now().UnixNano(), hex())
		if !pgCompDBPattern.MatchString(name) {
			t.Fatalf("mint-shaped name %q does not match the own census regex", name)
		}
		for _, token := range pgCompSeriesTokens {
			schema := fmt.Sprintf(pgCompSchemaFmt, time.Now().UnixNano(), hex(), token.token)
			if !pgCompSchemaPattern.MatchString(schema) {
				t.Fatalf("mint-shaped schema %q does not match the own schema regex", schema)
			}
			if pgCompDBPattern.MatchString(schema) {
				t.Fatalf("schema name %q would double-count as a database in the census", schema)
			}
		}
	}
	positives := []string{"verify_pgcomp_1731234567890_0123456789ab"}
	negatives := []string{
		"verify_pgcomp_",
		"verify_pgcomp_1731234567890_",
		"verify_pgcomp__0123456789ab",
		"verify_pgcomp_1731234567890_0123456789AB",
		"verify_pgcomp_1731234567890_0123456789ab_src",
		"verify_pgcomp_1731234567890_0123456789abzz",
		"verify_qroute_1731234567890_0123456789ab",
		"xverify_pgcomp_1731234567890_0123456789ab",
	}
	for _, name := range positives {
		if !pgCompDBPattern.MatchString(name) {
			t.Errorf("positive %q must match the own census regex", name)
		}
	}
	for _, name := range negatives {
		if pgCompDBPattern.MatchString(name) {
			t.Errorf("never-minted shape %q matched the own census regex", name)
		}
	}
}

// TestZZPgCompZeroLeftoverFixtures is the N-5 audit for this file's census.
func TestZZPgCompZeroLeftoverFixtures(t *testing.T) {
	raw := pgCompTestPostgresURL(t)
	if !pgCompBaselineSet {
		t.Skip("no live fixture was created in this run; nothing to audit")
	}
	if pgCompBaselineErr != nil {
		t.Fatalf("baseline was never captured: %v", pgCompBaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", err)
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_pgcomp_[0-9]+_[0-9a-f]+$'").
		Scan(&count); err != nil {
		t.Fatalf("count leftovers: %s", err)
	}
	pgCompOwnedMu.Lock()
	registered, outstanding, strays := 0, []string{}, append([]string(nil), pgCompStrayMarks...)
	perLeg := map[string]int{}
	unmatched := []string{}
	for db, rec := range pgCompOwned {
		registered++
		perLeg[rec.leg]++
		if !rec.dropped {
			outstanding = append(outstanding, db)
		}
		if !pgCompDBPattern.MatchString(db) {
			unmatched = append(unmatched, db)
		}
	}
	pgCompOwnedMu.Unlock()
	if len(outstanding) > 0 {
		t.Fatalf("owned fixtures still live at audit: %v", outstanding)
	}
	if len(strays) > 0 {
		t.Fatalf("drop marks for names this process never minted: %v", strays)
	}
	if len(unmatched) > 0 {
		t.Fatalf("registered mints outside the census regex: %v", unmatched)
	}
	for leg, want := range pgCompMintInventory {
		if got := perLeg[leg]; got != want {
			t.Errorf("mint inventory: leg %q minted %d databases, inventoried %d", leg, got, want)
		}
		delete(perLeg, leg)
	}
	for leg, got := range perLeg {
		t.Errorf("mint inventory: leg %q minted %d databases but is not inventoried", leg, got)
	}
	if count != pgCompBaseline {
		rows, err := admin.Query(ctx,
			"SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_pgcomp\\_%' ESCAPE '\\' ORDER BY datname")
		names := []string{}
		if err == nil {
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err == nil {
					names = append(names, name)
				}
			}
			rows.Close()
		}
		t.Fatalf("own-prefix census: live=%d baseline=%d registered=%d names=%v", count, pgCompBaseline, registered, names)
	}
}

func pgCompMultipart(t *testing.T, fields, files map[string]string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	for name, fileBody := range files {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatalf("create multipart file: %v", err)
		}
		if _, err := part.Write([]byte(fileBody)); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return buffer.Bytes(), writer.Boundary()
}
