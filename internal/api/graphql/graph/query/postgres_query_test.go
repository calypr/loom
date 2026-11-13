package queryapi

// pg-runtime-first-query-route: the PostgreSQL first scoped scalar read
// through the public fhirDataframe route. The offline witnesses prove the
// wiring posture: with discovery ports unwired the service fails closed as
// the named typed BACKEND_UNAVAILABLE before any row-port contact -- the
// route is driven by its ports, and an unwired port can never leak into a
// zero-value "success" or an ArangoDB fall-through. The live legs run the
// real service over the real factory row port and the backend catalog
// read lane's DiscoverFields behind the same discovery cache the server
// composition uses; auth and generation filtering ride the real
// compiled-SQL binds. Counters observe port contact without
// substitution.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qRoutePostgreSQLCompileTarget builds the same closed PostgreSQL compile
// target the server composition selects once from the source schema
// placement, so this file exercises the route's real target, not the Arango
// default the rest of this package's fixtures compile against.
func qRoutePostgreSQLCompileTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("verify_qroute_source")
	if err != nil {
		t.Fatalf("bind PostgreSQL storage placement: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("select PostgreSQL compile target: %v", err)
	}
	return target
}

// TestFhirDataframeNilDiscoveryRefusesTyped pins the composition-side claim
// on the query service: with DiscoverFields/DiscoverReferences unwired (the
// posture runPostgreSQLFirstQueryRoute uses until the backend catalog read
// lane lands), every discovery-backed query fails closed as the named typed
// BACKEND_UNAVAILABLE -- and the row port is never contacted, so a refusal
// can never be "answered" with rows from any other backend.
func TestFhirDataframeNilDiscoveryRefusesTyped(t *testing.T) {
	rowPortCalls := 0
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
			rowPortCalls++
			return nil
		},
	})
	service := NewService(Config{
		CompileTarget:      qRoutePostgreSQLCompileTarget(t),
		Dataframes:         dataframes,
		DiscoverFields:     nil,
		DiscoverReferences: nil,
	})

	result, err := service.Run(context.Background(), queryapiGenderInput(), nil)
	if err == nil {
		t.Fatalf("Run with unwired discovery ports returned %+v; it must fail closed, not degrade", result)
	}
	if rowPortCalls != 0 {
		t.Fatalf("unwired discovery still contacted the row port %d times: the refusal must precede any backend read", rowPortCalls)
	}
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("discovery refusal %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("discovery refusal code = %q, want %q", typed.Code(), dataframeerrors.CodeBackendUnavailable)
	}
	if result != nil {
		t.Fatalf("typed refusal must not carry a partial result: %+v", result)
	}
}

// ---------------------------------------------------------------------------
// Pre-staged live legs (reachable mints in this file's verify_qroute_q
// sub-namespace, registered and dropped by enumerated name only; see the
// N-5 audit at the foot of this file). Every port is the production one:
// the backend read lane supplies field discovery, auth and generation
// filtering ride the compiled-SQL binds (render/postgres/scope.go
// deny-by-default mapping), so the paired controls below are load-bearing
// witnesses, not fixture luck.
// ---------------------------------------------------------------------------

const (
	qrLiveProject = "qroute-p1"
	qrLiveGenA    = "generation-a"
	qrLiveGenB    = "generation-b"
	qrLoadPath    = "/fhir"
)

type qrLiveFixture struct {
	dsn       string
	srcSchema string
	relSchema string
	database  string
	scrub     func(any) string
	bootstrap *backendspostgres.Runtime
}

// N-5 ownership for this file's mints: the verify_qroute_q sub-namespace
// (the bare verify_qroute_ mint shape belongs to internal/server's census
// harness, whose shape-scoped audit never counts these names). Every mint
// is registered before any use, dropped by enumerated name only, and the
// TestZZ audit below requires baseline parity plus all-registered-dropped.
//
// The shape-scoping is not decoration -- it is the registered adjudication
// of an OBSERVED parallel-package race: `go test ./internal/api/graphql/
// graph/query ./internal/server` runs the two packages concurrently, and
// this file's live legs hold `verify_qroute_q<unixnano>_<hex>` databases in
// flight while internal/server's census test counted `datname LIKE
// 'verify\_qroute\_%'`. Those in-flight sub-namespace mints matched the
// server prefix and pushed its census above baseline -- a foreign-straggler
// false positive that package must never delete or attribute (N-5). Each
// package now counts only its own mint shape, and
// TestQRouteLiveCensusVocabularyCorrespondence below pins the separation
// (and every mint-vs-regex correspondence) with assertions, not log claims.
var (
	qrDBPattern = regexp.MustCompile(`^verify_qroute_q[0-9]+_[0-9a-f]+$`)
	// qrCensusSibling mirrors internal/server/postgres_query_test.go's
	// qRouteDBPattern literal; the two files' tables keep the literals
	// byte-identical and assert neither vocabulary matches the other's
	// census, which is exactly what keeps the parallel run race-free.
	qrCensusSibling = regexp.MustCompile(`^verify_qroute_[0-9]+_[0-9a-f]+$`)
	qrOwnedMu       sync.Mutex
	qrOwned         = map[string]qrMint{} // database -> minting leg + dropped
	qrStrayMarks    = []string{}          // drop marks for unregistered names
	qrBaselineMu    sync.Mutex
	qrBaseline      int64
	qrBaselineSet   bool
	qrBaselineErr   error
)

type qrMint struct {
	leg     string
	dropped bool
}

// qrMintInventory is the checked-in enumeration of this file's minting
// legs: exactly these tests mint, each exactly this many databases. The
// TestZZ audit asserts the runtime registry matches it leg-for-leg, so a
// new minting leg without an inventory row fails the census instead of
// leaking quietly.
var qrMintInventory = map[string]int{
	"TestFhirDataframePostgreSQLLiveExactScalarRows":                 1,
	"TestFhirDataframePostgreSQLLiveRestrictedPrincipalSeesNoRows":   1,
	"TestFhirDataframePostgreSQLLiveActiveGenerationExcludesStaged":  1,
	"TestFhirDataframePostgreSQLLiveBackendOutageTyped":              1,
	"TestFhirDataframePostgreSQLLivePreCancellationTyped":            1,
	"TestFhirDataframePostgreSQLLiveGarbagePrincipalPathsFailClosed": 1,
}

func qrRegister(db, leg string) {
	qrOwnedMu.Lock()
	defer qrOwnedMu.Unlock()
	qrOwned[db] = qrMint{leg: leg}
}

func qrMarkDropped(db string) {
	qrOwnedMu.Lock()
	defer qrOwnedMu.Unlock()
	rec, ok := qrOwned[db]
	if !ok {
		// A drop witnessed for a name this process never minted: recorded,
		// never folded into the registry, and the audit fails on it.
		qrStrayMarks = append(qrStrayMarks, db)
		return
	}
	rec.dropped = true
	qrOwned[db] = rec
}

func qrCaptureBaseline(t *testing.T, raw string) {
	t.Helper()
	qrBaselineMu.Lock()
	defer qrBaselineMu.Unlock()
	if qrBaselineSet {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		qrBaselineErr = fmt.Errorf("connect for baseline: %w", err)
		return
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_qroute_q[0-9]+_[0-9a-f]+$'").
		Scan(&count); err != nil {
		qrBaselineErr = fmt.Errorf("baseline count: %s", scrubQRStatic(err, raw))
		return
	}
	qrBaseline, qrBaselineSet = count, true
}

// scrubQRStatic redacts credentials that could appear in connection errors
// without depending on a per-fixture scrubber (baseline runs pre-fixture).
func scrubQRStatic(err error, raw string) string {
	s := err.Error()
	if u, perr := url.Parse(raw); perr == nil {
		secrets := []string{raw}
		if u.User != nil {
			secrets = append(secrets, u.User.String())
			if pw, ok := u.User.Password(); ok && pw != "" {
				secrets = append(secrets, pw)
			}
		}
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
	}
	return s
}

func qrTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL first query route live legs")
	}
	return raw
}

func qrRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

// newQRLiveFixture mints one isolated database plus the two consumed-series
// schemas and bootstraps both series through the public factory.
func newQRLiveFixture(t *testing.T) *qrLiveFixture {
	t.Helper()
	raw := qrTestPostgresURL(t)
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

	ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %s", scrub(err))
	}
	t.Cleanup(admin.Close)

	database := fmt.Sprintf("verify_qroute_q%d_%s", time.Now().UnixNano(), qrRandomHex(t))
	if !qrDBPattern.MatchString(database) {
		t.Fatalf("mint name %q violates the owned verify_qroute_q pattern", database)
	}
	qrRegister(database, t.Name())
	qrCaptureBaseline(t, raw)
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		qrMarkDropped(database) // never existed; the audit must not double-count
		t.Fatalf("create isolated database: %s", scrub(err))
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+database+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
			return
		}
		qrMarkDropped(database)
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
	}
	u.Path = "/" + database
	dsn := u.String()
	secrets = append(secrets, dsn)

	f := &qrLiveFixture{dsn: dsn, database: database, scrub: scrub,
		srcSchema: fmt.Sprintf("verify_qroute_q%d_%s_src", time.Now().UnixNano(), qrRandomHex(t)),
		relSchema: fmt.Sprintf("verify_qroute_q%d_%s_rel", time.Now().UnixNano(), qrRandomHex(t))}

	// The factory owns table setup, not schema lifecycle: create both
	// series schemas first, as cmd/loom-data and the ingest fixtures do.
	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated database: %s", scrub(err))
	}
	for _, schema := range []string{f.srcSchema, f.relSchema} {
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			iso.Close()
			t.Fatalf("create series schema: %s", scrub(err))
		}
	}
	iso.Close()

	bootstrap, err := backendspostgres.Open(ctx, control.Config{
		DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 30 * time.Second,
		Schemas: control.SeriesSchemas{
			control.SeriesSource:         f.srcSchema,
			control.SeriesDatasetRelease: f.relSchema,
		},
		Setup: control.SetupMigrate,
	})
	if err != nil {
		t.Fatalf("bootstrap fixture series: %s", scrub(err))
	}
	if bootstrap.FieldProfiles == nil || bootstrap.Inventory == nil || bootstrap.Relationships == nil || bootstrap.Contributions == nil {
		_ = bootstrap.Close()
		t.Fatal("fixture runtime assembled without catalog evidence ports")
	}
	t.Cleanup(func() { _ = bootstrap.Close() })
	f.bootstrap = bootstrap
	return f
}

// load stages one generation through the production ingest entry with the
// four catalog writers, exactly as cmd/loom-data composes them.
func (f *qrLiveFixture) load(t *testing.T, project, generation string, files map[string][]string) {
	t.Helper()
	dir := t.TempDir()
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write staging fixture: %v", err)
		}
	}
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	opts := ingest.LoadOptions{
		Schema:           qrRepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:          dir,
		Project:          project,
		Dataset:          &ref,
		AuthResourcePath: qrLoadPath,
		StageOnly:        true,
	}
	pg := ingest.PGSourceOptions{
		DSN: f.dsn, Schema: f.srcSchema, ManifestSchema: f.relSchema,
		ApplicationName: "verify-qroute-live",
		Catalog: ingest.GenerationCatalogWriters{
			FieldProfiles: f.bootstrap.FieldProfiles,
			Inventory:     f.bootstrap.Inventory,
			Relationships: f.bootstrap.Relationships,
			Contributions: f.bootstrap.Contributions,
		},
	}
	if _, err := ingest.LoadPostgresGeneration(context.Background(), opts, pg); err != nil {
		t.Fatalf("load %s/%s: %s", project, generation, f.scrub(err))
	}
}

func (f *qrLiveFixture) activate(t *testing.T, project, generation string) {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	pg := ingest.PGSourceOptions{
		DSN: f.dsn, Schema: f.srcSchema, ManifestSchema: f.relSchema,
		ApplicationName: "verify-qroute-live-activate",
	}
	if err := ingest.ActivatePostgresGeneration(context.Background(), ref, pg); err != nil {
		t.Fatalf("activate %s/%s: %s", project, generation, f.scrub(err))
	}
}

// serviceRuntime opens the SetupNone runtime the service row port rides.
func (f *qrLiveFixture) serviceRuntime(t *testing.T) *backendspostgres.Runtime {
	t.Helper()
	rt, err := backendspostgres.Open(context.Background(), control.Config{
		DSN: f.dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 30 * time.Second,
		Schemas: control.SeriesSchemas{
			control.SeriesSource:         f.srcSchema,
			control.SeriesDatasetRelease: f.relSchema,
		},
		Setup: control.SetupNone,
	})
	if err != nil {
		t.Fatalf("open service runtime: %s", f.scrub(err))
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func qrRepoPath(t *testing.T, elems ...string) string {
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

func qrPatientLine(t *testing.T, id, gender, birthDate string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"resourceType": "Patient", "id": id, "gender": gender, "birthDate": birthDate,
	})
	if err != nil {
		t.Fatalf("marshal patient fixture: %v", err)
	}
	return string(encoded)
}

func qrPatientInput(paths []string) *model.FhirDataframeInput {
	input := &model.FhirDataframeInput{
		Project:           qrLiveProject,
		RootResourceType:  "Patient",
		AuthResourcePaths: paths,
	}
	for _, name := range []string{"birthDate", "gender"} {
		input.RootFields = append(input.RootFields, &model.FhirFieldSelectInput{
			Name: name, Selector: &model.FhirFieldSelectorInput{ValuePath: name},
		})
	}
	return input
}

// qrDispatchGuardedRows mirrors the server composition's dispatch guard:
// only PostgreSQL-tagged commands reach the factory executor, and a
// non-PostgreSQL command is refused before any driver call. The stub exists
// so this file fails loudly rather than silently executing an AQL command
// through the PostgreSQL port.
func qrDispatchGuardedRows(execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		return execute(ctx, request, visit)
	}
}

// qrCounters observe port contact without changing port behavior.
type qrCounters struct {
	rowCalls      atomic.Int64
	discoverCalls atomic.Int64
}

// qrService wires the query service exactly as the server composition
// does: the backend read lane's DiscoverFields behind the same
// catalog.NewCache the Arango lane uses, the factory row executor behind
// the dispatch guard, and the manifest port as ActiveManifestResolver.
// The counters delegate: they observe, never substitute.
func qrService(t *testing.T, f *qrLiveFixture, rt *backendspostgres.Runtime, counters *qrCounters) *Service {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(f.srcSchema)
	if err != nil {
		t.Fatalf("bind PostgreSQL storage placement: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("select PostgreSQL compile target: %v", err)
	}
	countedRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		counters.rowCalls.Add(1)
		return qrDispatchGuardedRows(rt.SourceRows)(ctx, request, visit)
	}
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: countedRows,
	})
	cachedDiscovery := catalog.NewCache().DiscoverFields(rt.DiscoverFields)
	countedDiscovery := func(ctx context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		counters.discoverCalls.Add(1)
		return cachedDiscovery(ctx, options)
	}
	return NewService(Config{
		CompileTarget:          target,
		Dataframes:             dataframes,
		ScopeResolver:          nil,
		ActiveManifestResolver: rt.Manifests,
		DiscoverFields:         countedDiscovery,
	})
}

func TestFhirDataframePostgreSQLLiveExactScalarRows(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
		qrPatientLine(t, "patient-alan", "male", "1912-06-23"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	service := qrService(t, f, f.serviceRuntime(t), &counters)

	result, err := service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err != nil {
		t.Fatalf("live scoped read failed: %s", f.scrub(err))
	}
	if counters.discoverCalls.Load() == 0 {
		t.Fatal("discovery-backed query never consulted the backend read lane")
	}
	if counters.rowCalls.Load() == 0 {
		t.Fatal("exact-rows leg never contacted the row port")
	}
	if result == nil || result.RowCount != 2 || len(result.Rows) != 2 {
		t.Fatalf("result = %+v, want exactly 2 rows", result)
	}
	gotColumns := append([]string(nil), result.Columns...)
	sort.Strings(gotColumns)
	if want := []string{"_key", "birthDate", "gender"}; !reflect.DeepEqual(gotColumns, want) {
		t.Fatalf("columns = %v, want %v", result.Columns, want)
	}
	wantRows := []map[string]string{
		{"_key": "patient-ada", "birthDate": "1815-12-10", "gender": "female"},
		{"_key": "patient-alan", "birthDate": "1912-06-23", "gender": "male"},
	}
	var gotRows []map[string]string
	for _, row := range result.Rows {
		decoded := map[string]string{}
		for name, value := range row {
			var scalar string
			switch v := value.(type) {
			case json.RawMessage:
				if err := json.Unmarshal(v, &scalar); err != nil {
					t.Fatalf("column %q is not a JSON string (%s): %v", name, v, err)
				}
			case string:
				scalar = v
			default:
				t.Fatalf("column %q came back as %T, want raw JSON or string from the row port", name, value)
			}
			decoded[name] = scalar
		}
		gotRows = append(gotRows, decoded)
	}
	sort.Slice(gotRows, func(i, j int) bool { return gotRows[i]["_key"] < gotRows[j]["_key"] })
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("rows = %#v, want the exact loaded scalars %#v", gotRows, wantRows)
	}
	if result.Diagnostics.Total <= 0 {
		t.Fatalf("diagnostics missing: %+v", result.Diagnostics)
	}
}

func TestFhirDataframePostgreSQLLiveRestrictedPrincipalSeesNoRows(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
		qrPatientLine(t, "patient-alan", "male", "1912-06-23"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	service := qrService(t, f, f.serviceRuntime(t), &counters)

	// Alive control first: the identical unrestricted query returns both.
	control, err := service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err != nil || control == nil || control.RowCount != 2 {
		t.Fatalf("unrestricted control = %+v err=%v, want 2 rows (fixture is not vacuous)", control, f.scrub(err))
	}

	// A principal scoped away from the loaded path must read zero rows
	// through the deny-by-default SQL binds -- success envelope, no error.
	restrictedCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject: "verify_qroute_nurse", AuthResourcePaths: []string{"/fhir-nurse"},
	})
	result, err := service.Run(restrictedCtx, *qrPatientInput([]string{"/fhir-nurse"}), nil)
	if err != nil {
		t.Fatalf("restricted read failed: %s", f.scrub(err))
	}
	if result == nil || result.RowCount != 0 || len(result.Rows) != 0 {
		t.Fatalf("restricted read = %+v, want 0 rows with a success envelope", result)
	}
}

func TestFhirDataframePostgreSQLLiveActiveGenerationExcludesStaged(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
	}})
	f.load(t, qrLiveProject, qrLiveGenB, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-zoe", "female", "1955-03-04"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	service := qrService(t, f, f.serviceRuntime(t), &counters)

	result, err := service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err != nil {
		t.Fatalf("active-generation read failed: %s", f.scrub(err))
	}
	if result == nil || result.RowCount != 1 {
		t.Fatalf("active-generation read = %+v, want exactly the 1 gen-A row", result)
	}
	for _, row := range result.Rows {
		if strings.Contains(fmt.Sprintf("%v", row), "1955-03-04") {
			t.Fatalf("staged gen-B leaked into the active-generation read: %#v", row)
		}
	}
}

func TestFhirDataframePostgreSQLLiveBackendOutageTyped(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	rt := f.serviceRuntime(t)
	service := qrService(t, f, rt, &counters)

	// Alive control: rows first, then the outage.
	control, err := service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err != nil || control == nil || control.RowCount != 1 {
		t.Fatalf("alive control = %+v err=%v, want 1 row before the outage", control, f.scrub(err))
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("close pool for outage: %s", f.scrub(err))
	}
	_, err = service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err == nil {
		t.Fatal("read after pool close unexpectedly succeeded")
	}
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("outage error %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("outage code = %q, want %q", typed.Code(), dataframeerrors.CodeBackendUnavailable)
	}
	if !typed.Retryable() {
		t.Fatal("a closed pool must stay retry-classified (a fresh pool serves the next attempt)")
	}
}

func TestFhirDataframePostgreSQLLivePreCancellationTyped(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	service := qrService(t, f, f.serviceRuntime(t), &counters)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Run(ctx, *qrPatientInput(nil), nil)
	if err == nil {
		t.Fatal("pre-cancelled read unexpectedly succeeded")
	}
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("cancellation error %v is not a typed dataframe error", err)
	}
	t.Logf("cancellation classification witness: code=%q retryable=%v", typed.Code(), typed.Retryable())
}

// TestQRouteLiveCensusVocabularyCorrespondence pins, offline and with no
// database contact, the two vocabularies this batch minted: this file's
// verify_qroute_q<unixnano>_<hex> sub-namespace and internal/server's bare
// verify_qroute_<unixnano>_<hex> census shape. Conditions it makes
// test-enforced (coordinator ruling on the census narrowing):
//
//   - every name shape this process mints matches its own census regex, so
//     a leak can never go uncensused;
//   - near-miss shapes never minted -- including the fuzz names the
//     verifier was told to try (verify_qroute_q_zz, verify_qroute_1_x) --
//     do NOT match, so the census cannot silently claim foreign churn;
//   - neither vocabulary matches the other package's census, which is the
//     exact separation that makes the parallel `go test` of this package
//     and internal/server race-free (see the registry comment above).
func TestQRouteLiveCensusVocabularyCorrespondence(t *testing.T) {
	qrHex := func() string {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatalf("sample suffix: %v", err)
		}
		return fmt.Sprintf("%x", b[:])
	}
	// Positive: 200 samples through the exact mint format.
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("verify_qroute_q%d_%s", time.Now().UnixNano(), qrHex())
		if !qrDBPattern.MatchString(name) {
			t.Fatalf("mint-shaped name %q does not match the own census regex", name)
		}
		if qrCensusSibling.MatchString(name) {
			t.Fatalf("own-vocabulary name %q matched internal/server's census regex: parallel census race is back", name)
		}
	}
	ownPositives := []string{"verify_qroute_q1731234567890_0123456789ab"}
	// Negative: shapes this file never mints. The first two are the
	// verifier's named fuzz examples. The bare server shape and the _src /
	// _rel suffix shapes are the neighbouring vocabularies.
	ownNegatives := []string{
		"verify_qroute_q_zz",
		"verify_qroute_1_x",
		"verify_qroute_zz",
		"verify_qroute_1731234567890_0123456789ab", // internal/server's mint shape
		"verify_qroute_qq1731234567890_0123456789ab",
		"verify_qroute_q1731234567890_0123456789AB", // uppercase hex is never minted
		"verify_qroute_q_0123456789ab",
		"verify_qroute_q1731234567890_",
		"verify_qroute_q1731234567890_0123456789ab_src",
		"verify_qroute_q1731234567890_z123456789ab",
		"xverify_qroute_q1731234567890_0123456789ab",
	}
	for _, name := range ownPositives {
		if !qrDBPattern.MatchString(name) {
			t.Errorf("positive %q must match the own census regex", name)
		}
		if qrCensusSibling.MatchString(name) {
			t.Errorf("positive %q must not match the sibling census regex", name)
		}
	}
	for _, name := range ownNegatives {
		if qrDBPattern.MatchString(name) {
			t.Errorf("never-minted shape %q matched the own census regex: the census can now hide foreign churn under this file's name", name)
		}
	}
	// Sibling direction: the server's mint shape is census-invisible here,
	// and this file's shape is census-invisible there (mirror assertions
	// live in internal/server/postgres_query_test.go).
	for i := 0; i < 200; i++ {
		sibling := fmt.Sprintf("verify_qroute_%d_%s", time.Now().UnixNano(), qrHex())
		if qrDBPattern.MatchString(sibling) {
			t.Fatalf("sibling-vocabulary name %q matched this file's census regex", sibling)
		}
	}
}

func TestFhirDataframePostgreSQLLiveGarbagePrincipalPathsFailClosed(t *testing.T) {
	f := newQRLiveFixture(t)
	f.load(t, qrLiveProject, qrLiveGenA, map[string][]string{"Patient.ndjson": {
		qrPatientLine(t, "patient-ada", "female", "1815-12-10"),
	}})
	f.activate(t, qrLiveProject, qrLiveGenA)
	var counters qrCounters
	service := qrService(t, f, f.serviceRuntime(t), &counters)

	// Alive control first so the refusal is proven load-bearing.
	control, err := service.Run(context.Background(), *qrPatientInput(nil), nil)
	if err != nil || control == nil || control.RowCount != 1 {
		t.Fatalf("alive control = %+v err=%v, want 1 row before the refusal", control, f.scrub(err))
	}
	counters.rowCalls.Store(0)
	counters.discoverCalls.Store(0)

	// A principal whose only paths normalize to nothing (control bytes)
	// is refused as typed FORBIDDEN at input resolution -- before field
	// discovery and before the row port see anything (F-B22 law).
	garbageCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject: "verify_qroute_garbage", AuthResourcePaths: []string{"\x00\x01\x02"},
	})
	_, err = service.Run(garbageCtx, *qrPatientInput(nil), nil)
	if err == nil {
		t.Fatal("garbage principal unexpectedly served rows")
	}
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("garbage-principal refusal %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeForbidden) {
		t.Fatalf("garbage-principal code = %q, want %q", typed.Code(), dataframeerrors.CodeForbidden)
	}
	if counters.rowCalls.Load() != 0 || counters.discoverCalls.Load() != 0 {
		t.Fatalf("garbage-principal refusal reached the backend: rows=%d discovery=%d, want 0/0",
			counters.rowCalls.Load(), counters.discoverCalls.Load())
	}
}

// TestZZQRouteLiveZeroLeftoverFixtures is this file's N-5 audit: every
// verify_qroute_q mint this process registered must be witnessed dropped,
// and the sub-namespace count must return to the first-use baseline. The
// mismatch enumeration lists own sub-namespace names only; nothing outside
// it is ever deleted or attributed (N-5). The audit additionally asserts
// (not logs) drop-list == minted-list, mint-name/regex correspondence, and
// per-leg equality against qrMintInventory.
//
// Go runs tests in source order, so this function is deliberately the last
// function in the file: every minting leg above it (including
// TestFhirDataframePostgreSQLLiveGarbagePrincipalPathsFailClosed) has
// registered and dropped its database before the census compares. Any new
// minting leg must be added ABOVE this audit, never below it.
func TestZZQRouteLiveZeroLeftoverFixtures(t *testing.T) {
	raw := qrTestPostgresURL(t)
	if !qrBaselineSet {
		t.Skip("no live fixture was created in this run; nothing to audit")
	}
	if qrBaselineErr != nil {
		t.Fatalf("baseline was never captured: %v", qrBaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrubQRStatic(err, raw))
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_qroute_q[0-9]+_[0-9a-f]+$'").
		Scan(&count); err != nil {
		t.Fatalf("count leftovers: %s", scrubQRStatic(err, raw))
	}
	qrOwnedMu.Lock()
	registered, outstanding, strays := 0, []string{}, append([]string(nil), qrStrayMarks...)
	perLeg := map[string]int{}
	unmatched := []string{}
	for db, rec := range qrOwned {
		registered++
		perLeg[rec.leg]++
		if !rec.dropped {
			outstanding = append(outstanding, db)
		}
		if !qrDBPattern.MatchString(db) {
			unmatched = append(unmatched, db)
		}
	}
	qrOwnedMu.Unlock()
	if len(outstanding) > 0 {
		t.Fatalf("owned fixtures still live at audit: %v", outstanding)
	}
	// Drop-list == minted-list, asserted (not logged): every registered
	// mint was witnessed dropped above, and a drop mark can only land on a
	// registered name -- anything else went to qrStrayMarks:
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
	for leg, want := range qrMintInventory {
		if got := perLeg[leg]; got != want {
			t.Errorf("mint inventory: leg %q minted %d databases, inventoried %d", leg, got, want)
		}
		delete(perLeg, leg)
	}
	for leg, got := range perLeg {
		t.Errorf("mint inventory: leg %q minted %d databases but is not inventoried", leg, got)
	}
	if count != qrBaseline {
		rows, err := admin.Query(ctx,
			"SELECT datname FROM pg_database WHERE datname ~ '^verify_qroute_q[0-9]+_[0-9a-f]+$' ORDER BY datname")
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
		t.Fatalf("own sub-namespace census: live=%d baseline=%d registered=%d names=%v",
			count, qrBaseline, registered, names)
	}
}
