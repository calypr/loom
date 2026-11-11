package server

// pg-runtime-first-query-route: offline witnesses for the PostgreSQL first
// query route composition. The live scalar-read tests ride the backend
// catalog read lane (the DiscoverFields/DiscoverReferences ports); until
// that lane lands, this file pins what the composition owes on its own:
// (1) every configuration the route cannot consume keeps the byte-stable
// ErrPostgreSQLRuntimeNotWired refusal, dial-free, credential-free;
// (2) the route's own error scrubber removes every credential-bearing form
// of the connection configuration from any error it returns;
// (3) every deliberately-unsupported route posture is a named, typed,
// retryable-false, backend-tagged BACKEND_UNAVAILABLE refusal -- the
// explicit-unavailable law, never a silent zero value and never an
// ArangoDB fall-through.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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

	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qRoutePostgreSQLConfig is a fully valid PostgreSQL connection shape on the
// non-routable TEST-NET-1 address: any composition leg that regressed into
// dialing before refusing would hang until the deadline instead of returning
// the named refusal, so the elapsed-time bound is a real dial-free witness.
func qRoutePostgreSQLConfig() PostgreSQLConfig {
	pg := validPostgreSQLConfig()
	pg.Host = "192.0.2.1"
	pg.Port = 60543
	pg.Database = "verify_qroute_predial_witness"
	pg.User = "verify_qroute_predial_user"
	pg.Password = "qroute-Pg-Pa55w0rd-canary"
	return pg
}

// TestPostgreSQLFirstRoutePreDialRefusals mirrors the pinned startup-refusal
// invariant (postgres_refusal_contract_test.go) across the NEW gate region:
// the shapes the consumability gate cannot consume. The sentinel must arrive
// byte-stably (the pinned contract text, unchanged), before any credential
// validation and before any dial, and must echo no connection material.
func TestPostgreSQLFirstRoutePreDialRefusals(t *testing.T) {
	cases := []struct {
		name    string
		schemas map[string]string
	}{
		{
			name:    "absent schemas keep the arch-backend-06 refusal",
			schemas: nil,
		},
		{
			name:    "empty schemas map keeps the arch-backend-06 refusal",
			schemas: map[string]string{},
		},
		{
			name:    "source alone is not a consumable assignment",
			schemas: map[string]string{"source": "verify_qroute_source"},
		},
		{
			name:    "dataset_release alone is not a consumable assignment",
			schemas: map[string]string{"dataset_release": "verify_qroute_release"},
		},
		{
			name: "an unconsumed series beside the pair stays refused",
			schemas: map[string]string{
				"source": "verify_qroute_source", "dataset_release": "verify_qroute_release",
				"explorer_control": "verify_qroute_explorer",
			},
		},
		{
			name: "blank source is refused at the gate, not dialed with",
			schemas: map[string]string{
				"source": "  ", "dataset_release": "verify_qroute_release",
			},
		},
		{
			name: "the two consumable series sharing one schema are refused",
			schemas: map[string]string{
				"source": "verify_qroute_shared", "dataset_release": "verify_qroute_shared",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			cfg.Server.Backend = sourceBackendPostgres
			cfg.Server.PostgreSQL = qRoutePostgreSQLConfig()
			cfg.Server.PostgreSQL.Schemas = tc.schemas

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			err := RunWithConfig(ctx, cfg)
			elapsed := time.Since(started)

			if !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
				t.Fatalf("RunWithConfig(postgres, schemas=%v) err = %v, want errors.Is(err, ErrPostgreSQLRuntimeNotWired)", tc.schemas, err)
			}
			if got := err.Error(); got != postgreSQLRefusalMessage {
				t.Fatalf("refusal message drifted: got %q, want byte-stable %q", got, postgreSQLRefusalMessage)
			}
			if elapsed > 1500*time.Millisecond {
				t.Fatalf("refusal took %v: the consumability gate must refuse before any connection attempt", elapsed)
			}
			pg := cfg.Server.PostgreSQL
			for _, secret := range []string{pg.Host, pg.User, pg.Password, pg.Database} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("refusal error leaked connection material %q: %v", secret, err)
				}
			}
		})
	}
}

// TestPostgreSQLFirstQueryRouteScrubberPinsCredentials is the route file's
// own Exec-04-style scrubber pin: every credential-bearing form the
// composition can put into an error -- the full DSN, the bare password, and
// the user:password pair -- must be replaced, the cause must stay reachable
// through Unwrap, and a secret-free error must pass through untouched
// (identity, so wrapping is never fabricated).
func TestPostgreSQLFirstQueryRouteScrubberPinsCredentials(t *testing.T) {
	pg := qRoutePostgreSQLConfig()
	dsn := postgresFirstQueryRouteDSN(pg)
	if !strings.Contains(dsn, pg.Password) {
		t.Fatalf("fixture DSN must actually carry the password for the scrub to be exercised: %q", dsn)
	}
	cause := errors.New("dial failed")
	for _, raw := range []string{
		"open PostgreSQL source runtime: " + dsn,
		"auth failed for user " + pg.User + ":" + pg.Password + " (sqlstate 28P01)",
		"server rejected password " + pg.Password,
	} {
		got := postgresFirstQueryRouteError(fmt.Errorf("%s: %w", raw, cause), pg, dsn)
		if got == nil {
			t.Fatalf("scrubber dropped a non-nil error %q", raw)
		}
		for _, secret := range []string{dsn, pg.Password, pg.User + ":" + pg.Password} {
			if strings.Contains(got.Error(), secret) {
				t.Fatalf("scrubbed error %q still contains credential %q", got.Error(), secret)
			}
		}
		if !strings.Contains(got.Error(), "<redacted>") {
			t.Fatalf("scrubbed error %q lost the redaction marker", got.Error())
		}
		if !strings.Contains(got.Error(), "dial failed") {
			t.Fatalf("scrubbed error %q lost the non-secret cause text", got.Error())
		}
		var wrapped *redactedRouteError
		if !errors.As(got, &wrapped) || wrapped.Unwrap() == nil {
			t.Fatalf("scrubbed error %v must expose the original cause through Unwrap", got)
		}
		if !errors.Is(got, cause) {
			t.Fatalf("scrubbed error %v lost the original cause identity", got)
		}
	}

	// Secret-free errors pass through by identity: no fabricated wrapper.
	plain := errors.New("schema loom_source does not exist")
	if got := postgresFirstQueryRouteError(plain, pg, dsn); got != plain {
		t.Fatalf("secret-free error was rewritten: %v", got)
	}
	if postgresFirstQueryRouteError(nil, pg, dsn) != nil {
		t.Fatal("scrubber must map nil to nil")
	}
}

// TestPostgreSQLFirstQueryRouteWiringErrorShape pins the explicit-unavailable
// law for the route's deliberate refusals: named typed BACKEND_UNAVAILABLE,
// retryable false, backend-tagged, naming the operation and the enabling
// ticket -- never a nil/zero value and never an ArangoDB fall-through
// message.
func TestPostgreSQLFirstQueryRouteWiringErrorShape(t *testing.T) {
	err := postgreSQLFirstQueryRouteWiringError("Server-side generation activation", "pg-runtime-server-composition")
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("refusal %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("refusal code = %q, want %q", typed.Code(), dataframeerrors.CodeBackendUnavailable)
	}
	if typed.Retryable() {
		t.Fatal("a wiring refusal must not advertise retry: the route will not serve it on retry either")
	}
	details := typed.Details()
	if details["backend"] != "postgresql" || details["stage"] != "wiring" {
		t.Fatalf("refusal details %v must tag backend=postgresql stage=wiring", details)
	}
	if details["enablingTicket"] != "pg-runtime-server-composition" {
		t.Fatalf("refusal details %v must name the enabling ticket", details)
	}
	if !strings.Contains(typed.Error(), "Server-side generation activation") ||
		!strings.Contains(typed.Error(), "pg-runtime-server-composition") {
		t.Fatalf("refusal text %q must name both the operation and the enabling ticket", typed.Error())
	}
}

// ---------------------------------------------------------------------------
// Live leg: fixture harness (own verify_qroute_* prefix, N-5 enumerated
// cleanup) + the route census through the PRODUCTION handler stack.
// ---------------------------------------------------------------------------

const (
	qRouteDBFmt   = "verify_qroute_%d_%s"
	qRouteSrcFmt  = "verify_qroute_%d_%s_src"
	qRouteRelFmt  = "verify_qroute_%d_%s_rel"
	qRouteProject = "qroute-p1"
	qRouteForeign = "qroute-p2"
	qRouteGenA    = "generation-a"
	qRouteGenB    = "generation-b"
	qRouteAppTag  = "verify-qroute-server"
)

var (
	qRouteDBPattern   = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+$`)
	qRouteSrcPattern  = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+_src$`)
	qRouteRelPattern  = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+_rel$`)
	qRouteOwnedMu     sync.Mutex
	qRouteOwned       = map[string]bool{} // database -> dropped
	qRouteBaselineMu  sync.Mutex
	qRouteBaseline    int64
	qRouteBaselineSet bool
	qRouteBaselineErr error
)

func qRouteTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL first query route integration tests")
	}
	return raw
}

func qRouteRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func qRouteRegister(db string) {
	qRouteOwnedMu.Lock()
	defer qRouteOwnedMu.Unlock()
	qRouteOwned[db] = false
}

func qRouteMarkDropped(db string) {
	qRouteOwnedMu.Lock()
	defer qRouteOwnedMu.Unlock()
	qRouteOwned[db] = true
}

// qRouteCaptureBaseline counts own-prefix databases once per process. The
// zero-leftover audit compares against this baseline and audits ONLY the
// verify_qroute_ prefix (N-5: foreign churn is never deleted or attributed).
func qRouteCaptureBaseline(t *testing.T, raw string) {
	t.Helper()
	qRouteBaselineMu.Lock()
	defer qRouteBaselineMu.Unlock()
	if qRouteBaselineSet {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		qRouteBaselineErr = fmt.Errorf("connect for baseline: %w", err)
		return
	}
	defer admin.Close()
	var count int64
	// Static literal; the LIKE pattern is a constant, not built input.
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_qroute\\_%' ESCAPE '\\'").
		Scan(&count); err != nil {
		qRouteBaselineErr = fmt.Errorf("count baseline: %w", err)
		return
	}
	qRouteBaseline = count
	qRouteBaselineSet = true
}

// qRouteFixture owns one isolated database with one schema per consumed
// series. Everything destructive names an enumerated mint from this
// process's registry; no pattern-wide drop exists.
type qRouteFixture struct {
	dsn       string
	srcSchema string
	relSchema string
	scrub     func(any) string
	secrets   []string
	admin     *pgxpool.Pool
	database  string
}

func newQRouteFixture(t *testing.T) *qRouteFixture {
	t.Helper()
	raw := qRouteTestPostgresURL(t)
	qRouteCaptureBaseline(t, raw)
	if qRouteBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", qRouteBaselineErr)
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

	database := fmt.Sprintf(qRouteDBFmt, time.Now().UnixNano(), qRouteRandomHex(t))
	if !qRouteDBPattern.MatchString(database) {
		t.Fatalf("minted database %q violates the owned-name pattern", database)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatalf("create isolated database: %s", scrub(err))
	}
	qRouteRegister(database)
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+database+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
			return
		}
		qRouteMarkDropped(database)
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
	srcSchema := fmt.Sprintf(qRouteSrcFmt, time.Now().UnixNano(), qRouteRandomHex(t))
	relSchema := fmt.Sprintf(qRouteRelFmt, time.Now().UnixNano(), qRouteRandomHex(t))
	if !qRouteSrcPattern.MatchString(srcSchema) || !qRouteRelPattern.MatchString(relSchema) {
		t.Fatalf("minted schemas %q/%q violate the owned-name patterns", srcSchema, relSchema)
	}
	for _, schema := range []string{srcSchema, relSchema} {
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatalf("create owned schema: %s", scrub(err))
		}
	}
	return &qRouteFixture{dsn: dsn, srcSchema: srcSchema, relSchema: relSchema, scrub: scrub, secrets: secrets, admin: admin, database: database}
}

// migrate bootstraps both consumed series through the public factory, then
// hands back the runtime the catalog evidence writers ride (the exact
// cmd/loom-data openCatalogWriters shape: one factory Open, four ports).
func (f *qRouteFixture) migrate(t *testing.T) *backendspostgres.Runtime {
	t.Helper()
	runtime, err := backendspostgres.Open(context.Background(), backendspostgres.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas: backendspostgres.SeriesSchemas{
			backendspostgres.SeriesSource:         f.srcSchema,
			backendspostgres.SeriesDatasetRelease: f.relSchema,
		},
		Setup: backendspostgres.SetupMigrate,
	})
	if err != nil {
		t.Fatalf("migrate fixture series: %s", f.scrub(err))
	}
	if runtime.FieldProfiles == nil || runtime.Inventory == nil || runtime.Relationships == nil || runtime.Contributions == nil {
		_ = runtime.Close()
		t.Fatal("fixture runtime assembled without catalog evidence ports")
	}
	return runtime
}

func (f *qRouteFixture) pgOptions(application string) ingest.PGSourceOptions {
	return ingest.PGSourceOptions{
		DSN:             f.dsn,
		Schema:          f.srcSchema,
		ManifestSchema:  f.relSchema,
		ApplicationName: application,
	}
}

// load stages one generation through the production ingest entry
// (ingest.LoadPostgresGeneration) with the four catalog writers wired,
// exactly as the loom-data CLI composes it. The catalog runtime is closed
// after the load completes.
func (f *qRouteFixture) load(t *testing.T, project, generation string, files map[string][]string) {
	t.Helper()
	dir := t.TempDir()
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write staging fixture: %v", err)
		}
	}
	catalog := f.migrate(t)
	defer func() { _ = catalog.Close() }()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	opts := ingest.LoadOptions{
		Schema:           qRouteRepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:          dir,
		Project:          project,
		Dataset:          &ref,
		AuthResourcePath: "/fhir",
		StageOnly:        true,
	}
	pg := f.pgOptions(qRouteAppTag)
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

func (f *qRouteFixture) activate(t *testing.T, project, generation string) {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	if err := ingest.ActivatePostgresGeneration(context.Background(), ref, f.pgOptions(qRouteAppTag+"-activate")); err != nil {
		t.Fatalf("activate %s/%s: %s", project, generation, f.scrub(err))
	}
}

// qRouteRepoPath resolves a repository-relative path (mirrors the ingest
// lane's repoPath convention for the graph schema).
func qRouteRepoPath(t *testing.T, elems ...string) string {
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

// qRouteBootConfig turns a live URL into the consumable first-query-route
// configuration; the pair assignment is what unlocks composition past the
// sentinel. Unauthenticated mode keeps the census focused on route posture.
func qRouteBootConfig(t *testing.T, f *qRouteFixture, listen string) Config {
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
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host: host, Port: port, Database: strings.TrimPrefix(u.Path, "/"),
		User: u.User.Username(), Password: password, SSLMode: sslMode,
		MaxOpenConns: 4, MaxIdleConns: 1,
		Schemas: map[string]string{"source": f.srcSchema, "dataset_release": f.relSchema},
	}
	return cfg
}

func qRouteGraphQL(t *testing.T, app *fiber.App, body string) testHTTPResponse {
	t.Helper()
	return requestJSON(t, app, http.MethodPost, "/graphql/graph", body)
}

func qRoutePatientLines(t *testing.T, ids ...string) []string {
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

// TestPostgreSQLQueryRouteExplorerAndRowOpsExplicitlyUnavailable boots the
// production composition against a live fixture (gen-A loaded+active, gen-B
// staged) and walks the deliberately-unsupported inventory through the
// production handler stack: every leg must answer with its named typed
// refusal -- no 2xx data, no 5xx panic, and no Arango/AQL surface anywhere
// in any response body.
func TestPostgreSQLQueryRouteExplorerAndRowOpsExplicitlyUnavailable(t *testing.T) {
	fixture := newQRouteFixture(t)
	fixture.load(t, qRouteProject, qRouteGenA, map[string][]string{
		"Patient.ndjson":     qRoutePatientLines(t, "patient-ada", "patient-alan"),
		"Observation.ndjson": {`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"http://loinc.org","code":"8867-4","display":"Heart rate"}]},"subject":{"reference":"Patient/patient-ada"}}`},
	})
	fixture.load(t, qRouteProject, qRouteGenB, map[string][]string{"Patient.ndjson": qRoutePatientLines(t, "patient-zoe")})
	fixture.load(t, qRouteForeign, qRouteGenA, map[string][]string{"Patient.ndjson": qRoutePatientLines(t, "patient-foreign")})
	fixture.activate(t, qRouteProject, qRouteGenA)
	fixture.activate(t, qRouteForeign, qRouteGenA)

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
	app := server.App()

	const base = "/api/v1"

	t.Run("generation_status_is_a_live_postgres_read", func(t *testing.T) {
		response := requestJSON(t, app, http.MethodGet, base+"/datasets/"+qRouteProject+"/generations/"+qRouteGenA, "")
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Body, qRouteGenA) {
			t.Fatalf("active-generation status = %d %s, want 200 naming %s", response.StatusCode, response.Body, qRouteGenA)
		}
	})

	t.Run("load_route_refuses_with_the_cli_deferral", func(t *testing.T) {
		// A generation the manifest store has never seen: a known-STAGED
		// generation short-circuits to the service's replay answer before
		// the loader is consulted, and that answer would hide the refusal.
		body, boundary := qRouteMultipart(t, map[string]string{"Patient.ndjson": "{\"resourceType\":\"Patient\",\"id\":\"route-should-never-load\"}\n"})
		request := httptest.NewRequest(http.MethodPost, base+"/datasets/"+qRouteProject+"/generations/generation-route-attempt", bytes.NewReader(body))
		request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		response, err := app.Test(request, fiber.TestConfig{Timeout: 15 * time.Second, FailOnTimeout: true})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		text := string(raw)
		t.Logf("QR-D2 verbatim load-route refusal: status=%d body=%s", response.StatusCode, fixture.scrub(text))
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("load-route status = %d body=%s, want 503 typed refusal", response.StatusCode, fixture.scrub(text))
		}
		for _, want := range []string{"BACKEND_UNAVAILABLE", "loom-data CLI", "pg-runtime-server-composition"} {
			if !strings.Contains(text, want) {
				t.Fatalf("load-route refusal %q must contain %q", fixture.scrub(text), want)
			}
		}
	})

	t.Run("activation_route_refuses", func(t *testing.T) {
		response := requestJSON(t, app, http.MethodPost,
			base+"/datasets/"+qRouteProject+"/generations/"+qRouteGenB+"/activate?dataframe_execution_id=exec-census-1", `{}`)
		t.Logf("activation refusal: status=%d body=%s", response.StatusCode, fixture.scrub(response.Body))
		if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, "BACKEND_UNAVAILABLE") ||
			!strings.Contains(response.Body, "pg-runtime-server-composition") {
			t.Fatalf("activation refusal = %d %s, want 503 typed refusal naming the enabling ticket", response.StatusCode, fixture.scrub(response.Body))
		}
	})

	authoringRefusals := []struct {
		name, method, path, body string
		wantCode                 string
	}{
		{"explorer_authoring_suggestions", http.MethodPost, base + "/projects/" + qRouteProject + "/explorers/ex-1/authoring/v2/suggestions", `{"query":"patient"}`, "AUTHORING_UNAVAILABLE"},
		{"explorer_preview", http.MethodPost, base + "/projects/" + qRouteProject + "/explorers/ex-1/authoring/v2/preview", `{}`, "AUTHORING_UNAVAILABLE"},
		{"explorer_cell_trace", http.MethodPost, base + "/projects/" + qRouteProject + "/explorers/ex-1/authoring/v2/cell-trace", `{}`, "TRACE_UNAVAILABLE"},
		{"explorer_row_lineage", http.MethodPost, base + "/projects/" + qRouteProject + "/explorers/ex-1/authoring/v2/row-lineage", `{}`, "ROW_LINEAGE_UNAVAILABLE"},
	}
	for _, leg := range authoringRefusals {
		leg := leg
		t.Run(leg.name, func(t *testing.T) {
			response := requestJSON(t, app, leg.method, leg.path, leg.body)
			t.Logf("%s verbatim refusal: status=%d body=%s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				t.Fatalf("%s unexpectedly served data: %d %s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			}
			// 503 is a legitimate transport for a named typed refusal; any
			// other 5xx (or a 500 that lost the code) is a fault, not a
			// posture.
			if response.StatusCode >= 500 && response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("%s returned server fault %d: %s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			}
			if !strings.Contains(response.Body, leg.wantCode) {
				t.Fatalf("%s refusal %q must carry %q", leg.name, fixture.scrub(response.Body), leg.wantCode)
			}
			if response.StatusCode == http.StatusServiceUnavailable && strings.Contains(response.Body, "INTERNAL_ERROR") {
				t.Fatalf("%s 503 lost its typed code: %s", leg.name, fixture.scrub(response.Body))
			}
			if lower := strings.ToLower(response.Body); strings.Contains(lower, "arango") || strings.Contains(lower, "aql") {
				t.Fatalf("%s refusal leaks an Arango surface: %s", leg.name, fixture.scrub(response.Body))
			}
		})
	}

	// wantRetryable is load-bearing, not decoration: the nil-discovery
	// refusals are the service's retryable catalogUnavailable, while the
	// recipe refusals are this composition's wiring errors, which must
	// never advertise retry (retrying cannot make an unwired route serve).
	graphqlLegs := []struct {
		name          string
		query         string
		wantRetryable bool
	}{
		{"fhirDataframe_nil_discovery", `{"query":"mutation { runFhirDataframe(input: {project: \"` + qRouteProject + `\", rootResourceType: \"Patient\", rootFields: [{name: \"gender\", selector: {valuePath: \"gender\"}}]}) { rows rowCount } }"}`, true},
		{"fhirGraph_traversal", `{"query":"query { fhirGraph(input: {project: \"` + qRouteProject + `\", rootResourceType: \"Patient\"}) { returnedCount } }"}`, true},
		{"recipe_validate", `{"query":"mutation { validateDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, false},
		{"recipe_run", `{"query":"mutation { runDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, false},
		{"recipe_preview", `{"query":"mutation { previewDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, false},
		{"recipe_explain", `{"query":"query { explainDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, false},
	}
	for _, leg := range graphqlLegs {
		leg := leg
		t.Run(leg.name, func(t *testing.T) {
			response := qRouteGraphQL(t, app, leg.query)
			t.Logf("%s verbatim refusal: status=%d body=%s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			if !strings.Contains(response.Body, "BACKEND_UNAVAILABLE") {
				t.Fatalf("%s refusal %q must carry BACKEND_UNAVAILABLE", leg.name, fixture.scrub(response.Body))
			}
			wantRetryable := `"retryable":true`
			if !leg.wantRetryable {
				wantRetryable = `"retryable":false`
			}
			if !strings.Contains(response.Body, wantRetryable) {
				t.Fatalf("%s refusal %q must classify retryable=%v", leg.name, fixture.scrub(response.Body), leg.wantRetryable)
			}
			if lower := strings.ToLower(response.Body); strings.Contains(lower, "arango") || strings.Contains(lower, "aql") {
				t.Fatalf("%s refusal leaks an Arango surface: %s", leg.name, fixture.scrub(response.Body))
			}
		})
	}
}

func qRouteMultipart(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, body := range files {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatalf("create multipart file: %v", err)
		}
		if _, err := part.Write([]byte(body)); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return buffer.Bytes(), writer.Boundary()
}

// TestZZQRouteZeroLeftoverFixtures is the N-5 audit: it compares the
// pg_database count for THIS process's own verify_qroute_ prefix against the
// baseline captured at first use and requires every registered mint to be
// witnessed dropped. Foreign prefixes are printed and classified only --
// never deleted, never attributed to this pull request.
func TestZZQRouteZeroLeftoverFixtures(t *testing.T) {
	raw := qRouteTestPostgresURL(t)
	if !qRouteBaselineSet {
		t.Skip("no live fixture was created in this run; nothing to audit")
	}
	if qRouteBaselineErr != nil {
		t.Fatalf("baseline was never captured: %v", qRouteBaselineErr)
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_qroute\\_%' ESCAPE '\\'").
		Scan(&count); err != nil {
		t.Fatalf("count leftovers: %s", err)
	}
	qRouteOwnedMu.Lock()
	registered, outstanding := 0, []string{}
	for db, dropped := range qRouteOwned {
		registered++
		if !dropped {
			outstanding = append(outstanding, db)
		}
	}
	qRouteOwnedMu.Unlock()
	if len(outstanding) > 0 {
		t.Fatalf("owned fixtures still live at audit: %v", outstanding)
	}
	if count != qRouteBaseline {
		rows, err := admin.Query(ctx,
			"SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_qroute\\_%' ESCAPE '\\' ORDER BY datname")
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
		t.Fatalf("own-prefix census: live=%d baseline=%d registered=%d names=%v", count, qRouteBaseline, registered, names)
	}
}
