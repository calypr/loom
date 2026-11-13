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
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
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

	// Percent-encoded echo legs (review e22c4825 optional 2): a driver
	// echoing the credential parsed from the connection URL renders the
	// ESCAPED form, which raw-string scrubbing alone misses. The fixture
	// password is escape-invariant and cannot exercise this, so pin a
	// variant whose password requires escaping in both encodings.
	esc := qRoutePostgreSQLConfig()
	esc.Password = "qroute p@ss:w0rd/s?x=&%"
	escDSN := postgresFirstQueryRouteDSN(esc)
	queryEscaped, pathEscaped := url.QueryEscape(esc.Password), url.PathEscape(esc.Password)
	if queryEscaped == esc.Password || pathEscaped == esc.Password {
		t.Fatalf("variant password must require escaping in both encodings: query=%q path=%q", queryEscaped, pathEscaped)
	}
	// The DSN's own userinfo follows the URL package's user-password
	// encoding (a third shape); the scrubber must not depend on which
	// encoding an echo used, so the legs below drive the escaped forms
	// directly alongside the full DSN.
	if !strings.Contains(escDSN, esc.User) {
		t.Fatalf("variant DSN lost the user: %q", escDSN)
	}
	for _, raw := range []string{
		"auth failed for user " + esc.User + ":" + queryEscaped + " (sqlstate 28P01)",
		"server rejected password " + pathEscaped,
		"driver echoed parsed credential " + esc.User + ":" + pathEscaped + " while dialing " + escDSN,
	} {
		got := postgresFirstQueryRouteError(fmt.Errorf("%s: %w", raw, cause), esc, escDSN)
		if got == nil {
			t.Fatalf("scrubber dropped a non-nil error %q", raw)
		}
		for _, secret := range []string{escDSN, esc.Password, queryEscaped, pathEscaped} {
			if secret != "" && strings.Contains(got.Error(), secret) {
				t.Fatalf("scrubbed error %q still contains credential %q", got.Error(), secret)
			}
		}
		if !strings.Contains(got.Error(), "<redacted>") || !strings.Contains(got.Error(), "dial failed") {
			t.Fatalf("scrubbed error %q lost the marker or the cause text", got.Error())
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

// Shape-scoping of the census is the registered adjudication of an
// OBSERVED parallel-package race: `go test ./internal/api/graphql/graph/
// query ./internal/server` runs the two packages concurrently, and the
// query package's six live legs hold `verify_qroute_q<unixnano>_<hex>`
// databases in flight while this file's census counted `datname LIKE
// 'verify\_qroute\_%'`. Those in-flight sub-namespace mints matched the
// prefix and pushed this census above baseline -- a foreign-straggler false
// positive this package must never delete or attribute (N-5). Each package
// now counts only its own mint shape, and
// TestQRouteCensusVocabularyCorrespondence below pins the separation and
// the mint/regex correspondence with assertions, not log claims.
var (
	qRouteDBPattern  = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+$`)
	qRouteSrcPattern = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+_src$`)
	qRouteRelPattern = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+_rel$`)
	// qRouteCensusSibling mirrors internal/api/graphql/graph/query/
	// postgres_query_test.go's qrDBPattern literal; both files keep the
	// literals byte-identical and assert neither vocabulary matches the
	// other's census.
	qRouteCensusSibling = regexp.MustCompile(`^verify_qroute_q[0-9]+_[0-9a-f]+$`)
	qRouteOwnedMu       sync.Mutex
	qRouteOwned         = map[string]qRouteMint{} // database -> minting leg + dropped
	qRouteStrayMarks    = []string{}              // drop marks for unregistered names
	qRouteBaselineMu    sync.Mutex
	qRouteBaseline      int64
	qRouteBaselineSet   bool
	qRouteBaselineErr   error
)

type qRouteMint struct {
	leg     string
	dropped bool
}

// qRouteMintInventory is the checked-in enumeration of the minting legs
// that share this harness: exactly these tests mint, each exactly this many
// databases. The TestZZ audit asserts the runtime registry matches it
// leg-for-leg.
var qRouteMintInventory = map[string]int{
	"TestPostgreSQLQueryRouteExplorerAndRowOpsExplicitlyUnavailable":     1,
	"TestPostgreSQLQueryRouteServesExactScalarsThroughProductionHandler": 1,
	// pg-runtime-health-readiness leg (mints one database).
	"TestPostgreSQLFirstQueryRouteLivenessAndReadiness": 1,
	// arch-backend-12 backend-neutral diagnostics leg (mints one database;
	// implementation lives in backend_neutral_diagnostics_live_test.go).
	"TestBackendNeutralDiagnosticsLivePostgreSQL": 1,
}

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

func qRouteRegister(db, leg string) {
	qRouteOwnedMu.Lock()
	defer qRouteOwnedMu.Unlock()
	qRouteOwned[db] = qRouteMint{leg: leg}
}

func qRouteMarkDropped(db string) {
	qRouteOwnedMu.Lock()
	defer qRouteOwnedMu.Unlock()
	rec, ok := qRouteOwned[db]
	if !ok {
		// A drop witnessed for a name this process never minted: recorded,
		// never folded into the registry, and the audit fails on it.
		qRouteStrayMarks = append(qRouteStrayMarks, db)
		return
	}
	rec.dropped = true
	qRouteOwned[db] = rec
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
	// Static literal; the regex is a constant, not built input. The count
	// covers THIS file's mint shape only: internal/api/graphql/graph/query
	// lives in the verify_qroute_q sub-namespace with its own registry and
	// audit. That separation is the adjudication of the observed
	// parallel-run race named on the pattern block above, and the
	// correspondence test pins it with assertions. Foreign names are still
	// enumerated and classified on mismatch (N-5).
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_qroute_[0-9]+_[0-9a-f]+$'").
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
	qRouteRegister(database, t.Name())
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
	runtime, err := backendspostgres.Open(context.Background(), control.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas: control.SeriesSchemas{
			control.SeriesSource:         f.srcSchema,
			control.SeriesDatasetRelease: f.relSchema,
		},
		Setup: control.SetupMigrate,
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
	// wantCode is the named typed refusal each leg must carry.
	// fhirGraph_traversal's code is the Ask-C supersession of the
	// registered QR-D5 row: with field discovery wired, traversal now
	// reaches compilation and the PostgreSQL renderer refuses the graph
	// physical plan; the shared graph path flattens that refusal to
	// INVALID_REQUEST (retryable=false). Honest, typed, Arango-free --
	// the flattening classification itself is banked as a cross-lane
	// finding for the compiler/renderer lane, not silently re-pinned to
	// a stale expectation.
	graphqlLegs := []struct {
		name          string
		query         string
		wantCode      string
		wantRetryable bool
	}{
		{"fhirGraph_traversal", `{"query":"query { fhirGraph(input: {project: \"` + qRouteProject + `\", rootResourceType: \"Patient\"}) { returnedCount } }"}`, "INVALID_REQUEST", false},
		{"recipe_validate", `{"query":"mutation { validateDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, "BACKEND_UNAVAILABLE", false},
		{"recipe_run", `{"query":"mutation { runDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, "BACKEND_UNAVAILABLE", false},
		{"recipe_preview", `{"query":"mutation { previewDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, "BACKEND_UNAVAILABLE", false},
		{"recipe_explain", `{"query":"query { explainDataframeRecipe(input: {name: \"r\", bindings: {project: \"` + qRouteProject + `\"}}) { name } }"}`, "BACKEND_UNAVAILABLE", false},
	}
	for _, leg := range graphqlLegs {
		leg := leg
		t.Run(leg.name, func(t *testing.T) {
			response := qRouteGraphQL(t, app, leg.query)
			t.Logf("%s verbatim refusal: status=%d body=%s", leg.name, response.StatusCode, fixture.scrub(response.Body))
			if !strings.Contains(response.Body, leg.wantCode) {
				t.Fatalf("%s refusal %q must carry %s", leg.name, fixture.scrub(response.Body), leg.wantCode)
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

	// The census inventory is complete only when it also records what the
	// route DOES serve: fhirDataframe answers from the read lane. (The
	// dedicated exact-literals witness is
	// TestPostgreSQLQueryRouteServesExactScalarsThroughProductionHandler.)
	t.Run("fhirDataframe_serves_via_read_lane", func(t *testing.T) {
		response := qRouteGraphQL(t, app, `{"query":"mutation { runFhirDataframe(input: {project: \"`+qRouteProject+`\", rootResourceType: \"Patient\", rootFields: [{name: \"gender\", selector: {valuePath: \"gender\"}}]}) { rows rowCount } }"}`)
		t.Logf("fhirDataframe serving witness: status=%d body=%s", response.StatusCode, fixture.scrub(response.Body))
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Body, "\"rowCount\":2") ||
			strings.Contains(response.Body, "\"errors\"") {
			t.Fatalf("fhirDataframe must serve through the read lane, got %d %s", response.StatusCode, fixture.scrub(response.Body))
		}
		if lower := strings.ToLower(response.Body); strings.Contains(lower, "arango") || strings.Contains(lower, "aql") {
			t.Fatalf("served answer leaks an Arango surface: %s", fixture.scrub(response.Body))
		}
	})
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

// TestPostgreSQLQueryRouteServesExactScalarsThroughProductionHandler is
// the ticket's headline witness: the first scoped PostgreSQL scalar read
// through the public GraphQL route. It boots the production composition
// (composePostgreSQLFirstQueryRoute -- the same function the server
// serves), drives /graphql/graph's runFhirDataframe through the
// production handler stack against a live PostgreSQL generation, and
// requires literal rows. No Arango surface exists on this branch (the
// composition never constructs one), so any answer here is PostgreSQL's.
func TestPostgreSQLQueryRouteServesExactScalarsThroughProductionHandler(t *testing.T) {
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
	app := server.App()

	body := `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rowCount rows } }","variables":{"input":{"project":"` + qRouteProject + `","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"}},{"name":"birthDate","selector":{"valuePath":"birthDate"}}]},"limit":10}}`
	response := qRouteGraphQL(t, app, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runFhirDataframe HTTP %d: %s", response.StatusCode, fixture.scrub(response.Body))
	}
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			RunFhirDataframe struct {
				Columns  []string         `json:"columns"`
				RowCount int              `json:"rowCount"`
				Rows     []map[string]any `json:"rows"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("decode handler envelope: %v (body %s)", err, fixture.scrub(response.Body))
	}
	if len(envelope.Errors) != 0 {
		t.Fatalf("handler returned GraphQL errors: %s", fixture.scrub(response.Body))
	}
	result := envelope.Data.RunFhirDataframe
	if result.RowCount != 2 || len(result.Rows) != 2 {
		t.Fatalf("handler rows = %d/%d, want 2: %s", result.RowCount, len(result.Rows), fixture.scrub(response.Body))
	}
	gotColumns := append([]string(nil), result.Columns...)
	sort.Strings(gotColumns)
	if want := []string{"_key", "birthDate", "gender"}; !reflect.DeepEqual(gotColumns, want) {
		t.Fatalf("handler columns = %v, want %v", result.Columns, want)
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		return fmt.Sprint(result.Rows[i]["_key"]) < fmt.Sprint(result.Rows[j]["_key"])
	})
	for i, want := range []map[string]string{
		{"_key": "patient-ada", "birthDate": "1900-01-01", "gender": "female"},
		{"_key": "patient-alan", "birthDate": "1900-01-01", "gender": "female"},
	} {
		for column, wantValue := range want {
			got, present := result.Rows[i][column]
			if !present {
				t.Fatalf("row %d lost column %q: %#v", i, column, result.Rows[i])
			}
			if gotText, ok := got.(string); !ok || gotText != wantValue {
				t.Fatalf("row %d column %q = %#v, want %q", i, column, got, wantValue)
			}
		}
	}
	// No Arango dialect may leak into the response: this branch
	// constructs no Arango surface at all, and the answer must not read
	// like an AQL echo either.
	for _, aqlTell := range []string{"FOR d IN ", "@auth_resource_paths", "fhir_field_catalog"} {
		if strings.Contains(response.Body, aqlTell) {
			t.Fatalf("response body carries AQL tell %q: %s", aqlTell, fixture.scrub(response.Body))
		}
	}
}

// TestPostgreSQLFirstQueryRouteLivenessAndReadiness is the
// pg-runtime-health-readiness leg for the first-query route: on a migrated
// placement /livez answers the published DB-free body and /readyz reports
// ready with dataframe=disabled (the route refuses ClickHouse by contract);
// after the closer tears the pool down, /livez STILL answers 200 through the
// production handler stack while /readyz refuses with core=not_ready and
// the safe core_state=runtime_closed token.
func TestPostgreSQLFirstQueryRouteLivenessAndReadiness(t *testing.T) {
	markReadinessLeg(t, "TestPostgreSQLFirstQueryRouteLivenessAndReadiness")
	fixture := newQRouteFixture(t)
	catalog := fixture.migrate(t)
	_ = catalog.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, closeRuntime, err := composePostgreSQLFirstQueryRoute(ctx, qRouteBootConfig(t, fixture, "127.0.0.1:0"), logger)
	if err != nil {
		t.Fatalf("compose production route: %s", fixture.scrub(err))
	}
	var closeOnce sync.Once
	closeNow := func() {
		closeOnce.Do(func() {
			if err := closeRuntime(); err != nil {
				t.Errorf("close production route runtime: %s", fixture.scrub(err))
			}
		})
	}
	defer closeNow()
	app := server.App()

	live := requestJSON(t, app, http.MethodGet, "/livez", "")
	if live.StatusCode != http.StatusOK || live.Body != `{"status":"live"}` {
		t.Fatalf("livez on the first-query route = %d %s; want 200 {\"status\":\"live\"}", live.StatusCode, fixture.scrub(live.Body))
	}
	response := requestJSON(t, app, http.MethodGet, "/readyz", "")
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(response.Body, `"status":"ready"`) ||
		!strings.Contains(response.Body, `"core":"ready"`) ||
		!strings.Contains(response.Body, `"dataframe":"disabled"`) {
		t.Fatalf("migrated first-query-route readiness = %d %s; want 200 ready/ready/disabled",
			response.StatusCode, fixture.scrub(response.Body))
	}
	if strings.Contains(strings.ToLower(response.Body), "password") || strings.Contains(response.Body, fixture.dsn) {
		t.Fatalf("readiness body leaked connection material: %s", fixture.scrub(response.Body))
	}

	closeNow()
	liveAfter := requestJSON(t, app, http.MethodGet, "/livez", "")
	if liveAfter.StatusCode != http.StatusOK || liveAfter.Body != `{"status":"live"}` {
		t.Fatalf("livez with a closed pool = %d %s; liveness must be DB-free", liveAfter.StatusCode, fixture.scrub(liveAfter.Body))
	}
	refused := requestJSON(t, app, http.MethodGet, "/readyz", "")
	if refused.StatusCode != http.StatusServiceUnavailable ||
		!strings.Contains(refused.Body, `"core":"not_ready"`) ||
		!strings.Contains(refused.Body, `"core_state":"runtime_closed"`) {
		t.Fatalf("readyz with a closed pool = %d %s; want 503 core=not_ready core_state=runtime_closed",
			refused.StatusCode, fixture.scrub(refused.Body))
	}
}

// TestQRouteCensusVocabularyCorrespondence pins, offline and with no
// database contact, the two vocabularies this batch minted: this file's
// bare verify_qroute_<unixnano>_<hex> census shape and the
// verify_qroute_q<unixnano>_<hex> sub-namespace owned by
// internal/api/graphql/graph/query/postgres_query_test.go. Conditions it
// makes test-enforced (coordinator ruling on the census narrowing):
//
//   - every name shape this process mints matches its own census regex, so
//     a leak can never go uncensused;
//   - near-miss shapes never minted -- including the fuzz names the
//     verifier was told to try (verify_qroute_q_zz, verify_qroute_1_x) --
//     do NOT match, so the census cannot silently claim foreign churn;
//   - neither vocabulary matches the other package's census, which is the
//     exact separation that makes the parallel `go test` of this package
//     and internal/api/graphql/graph/query race-free (see the registry
//     comment above the pattern block).
func TestQRouteCensusVocabularyCorrespondence(t *testing.T) {
	qrHex := func() string {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatalf("sample suffix: %v", err)
		}
		return fmt.Sprintf("%x", b[:])
	}
	// Positive: 200 samples through the exact mint format.
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf(qRouteDBFmt, time.Now().UnixNano(), qrHex())
		if !qRouteDBPattern.MatchString(name) {
			t.Fatalf("mint-shaped name %q does not match the own census regex", name)
		}
		if qRouteCensusSibling.MatchString(name) {
			t.Fatalf("own-vocabulary name %q matched the query package's census regex: parallel census race is back", name)
		}
	}
	ownPositives := []string{"verify_qroute_1731234567890_0123456789ab"}
	// Negative: shapes this file never mints. The first two are the
	// verifier's named fuzz examples; the sub-namespace shape is the query
	// package's whole vocabulary, and _src/_rel suffixes are this file's
	// own schema shapes, which must never double-count as databases.
	ownNegatives := []string{
		"verify_qroute_q_zz",
		"verify_qroute_1_x",
		"verify_qroute_q1731234567890_0123456789ab", // the query package's mint shape
		"verify_qroute_",
		"verify_qroute_1731234567890_",
		"verify_qroute__0123456789ab",
		"verify_qroute_1731234567890_0123456789AB", // uppercase hex is never minted
		"verify_qroute_1731234567890_0123456789ab_src",
		"verify_qroute_1731234567890_0123456789ab_rel",
		"verify_qroute_0x1f_0123456789ab",
		"xverify_qroute_1731234567890_0123456789ab",
		"verify_qroute_1731234567890_0123456789abx",
	}
	for _, name := range ownPositives {
		if !qRouteDBPattern.MatchString(name) {
			t.Errorf("positive %q must match the own census regex", name)
		}
		if qRouteCensusSibling.MatchString(name) {
			t.Errorf("positive %q must not match the sibling census regex", name)
		}
	}
	for _, name := range ownNegatives {
		if qRouteDBPattern.MatchString(name) {
			t.Errorf("never-minted shape %q matched the own census regex: the census can now hide foreign churn under this file's name", name)
		}
	}
	// Sibling direction: the query package's sub-namespace shape is
	// census-invisible here (mirror assertions live in that file).
	for i := 0; i < 200; i++ {
		sibling := fmt.Sprintf("verify_qroute_q%d_%s", time.Now().UnixNano(), qrHex())
		if qRouteDBPattern.MatchString(sibling) {
			t.Fatalf("sibling-vocabulary name %q matched this file's census regex", sibling)
		}
	}
}

// TestZZQRouteZeroLeftoverFixtures is the N-5 audit: it compares the
// pg_database count for THIS process's own mint shape against the baseline
// captured at first use and requires every registered mint to be witnessed
// dropped. Foreign prefixes are printed and classified only -- never
// deleted, never attributed to this pull request. The audit additionally
// asserts (not logs) drop-list == minted-list, mint-name/regex
// correspondence, and per-leg equality against qRouteMintInventory.
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
	// Same shape-scoped count as the baseline: this file's mints only; the
	// query package's verify_qroute_q mints are audited by its own N-5 leg.
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_qroute_[0-9]+_[0-9a-f]+$'").
		Scan(&count); err != nil {
		t.Fatalf("count leftovers: %s", err)
	}
	qRouteOwnedMu.Lock()
	registered, outstanding, strays := 0, []string{}, append([]string(nil), qRouteStrayMarks...)
	perLeg := map[string]int{}
	unmatched := []string{}
	for db, rec := range qRouteOwned {
		registered++
		perLeg[rec.leg]++
		if !rec.dropped {
			outstanding = append(outstanding, db)
		}
		if !qRouteDBPattern.MatchString(db) {
			unmatched = append(unmatched, db)
		}
	}
	qRouteOwnedMu.Unlock()
	if len(outstanding) > 0 {
		t.Fatalf("owned fixtures still live at audit: %v", outstanding)
	}
	// Drop-list == minted-list, asserted (not logged): every registered
	// mint was witnessed dropped above, and a drop mark can only land on a
	// registered name -- anything else went to qRouteStrayMarks:
	if len(strays) > 0 {
		t.Fatalf("drop marks for names this process never minted: %v", strays)
	}
	// Every minted name matches the census regex, so no leak of this
	// process can hide outside the census that watches for leaks.
	if len(unmatched) > 0 {
		t.Fatalf("registered mints outside the census regex: %v", unmatched)
	}
	// Checked-in per-leg enumeration == runtime registry, both directions:
	// every inventoried leg minted its declared count, and no leg outside
	// the inventory minted anything. (This is the full-package census run;
	// a -run subset that skips inventoried legs fails here on purpose so
	// the subset is never mistaken for the census.)
	for leg, want := range qRouteMintInventory {
		if got := perLeg[leg]; got != want {
			t.Errorf("mint inventory: leg %q minted %d databases, inventoried %d", leg, got, want)
		}
		delete(perLeg, leg)
	}
	for leg, got := range perLeg {
		t.Errorf("mint inventory: leg %q minted %d databases but is not inventoried", leg, got)
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

// TestPostgreSQLFirstQueryRouteDeadEndpointFailsBounded pins today's truth
// for the registered factory-layer risk (plan 5c): with the consumable pair
// against a dead endpoint, the startup path eventually surfaces a typed,
// credential-free failure -- no hang-forever, no panic. This is a BOUND
// assertion, not a speed assertion: the measured behavior (run receipt in
// the plan) is ~60s to first failure regardless of the caller ctx -- the
// factory's eager MinConns dial plus its AcquireTimeout policy own that
// latency, not the server ctx -- so the generous bound below must never be
// tightened into a performance promise. It is live-gated on purpose: the
// route battery itself stays on reachable mints.
func TestPostgreSQLFirstQueryRouteDeadEndpointFailsBounded(t *testing.T) {
	qRouteTestPostgresURL(t) // live-gate; the witness dials no reachable host
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.PostgreSQL = qRoutePostgreSQLConfig()
	cfg.Server.PostgreSQL.Schemas = map[string]string{
		"source": "verify_qroute_dead_src", "dataset_release": "verify_qroute_dead_rel",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	err := runPostgreSQLFirstQueryRoute(ctx, cfg, logger)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("dead-endpoint composition unexpectedly started the server")
	}
	if errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
		t.Fatalf("dead endpoint was refused by the consumability sentinel instead of the factory's dial law: %v", err)
	}
	if elapsed > 5*time.Minute {
		t.Fatalf("dead-endpoint failure took %v: the route must converge on a typed failure, not hang-forever", elapsed)
	}
	pg := cfg.Server.PostgreSQL
	for _, secret := range []string{pg.Password, pg.User + ":" + pg.Password, pg.Host} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("dead-endpoint failure leaked credential material %q: %v", secret, err)
		}
	}
	// Cause chain stays intact through the scrubber (no half-typed error).
	var wrapped *redactedRouteError
	if errors.As(err, &wrapped) && wrapped.Unwrap() == nil {
		t.Fatalf("scrubbed failure lost its cause: %v", err)
	}
	t.Logf("dead-endpoint bound witness: elapsed=%v errKind=%T", elapsed, err)
}
