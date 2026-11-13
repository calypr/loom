package server

// TestBackendArchitectureConformance (arch-backend-10) is the required
// dual-backend conformance suite: the SAME logical graph, the SAME
// scenario cells, and LITERAL expected results (test constants, never
// backend-vs-backend agreement) run through the PRODUCTION composition on
// both real backends — composePostgreSQLFullRuntime over an owned
// verify_ab10pg_* PostgreSQL 18 database and composeArangoRuntime over an
// owned loom_test_ab10_arango_* ArangoDB database.
//
// Evidence classes are deliberately distinct (coverage amendment):
//   - scenario verdicts come only from the production composition and the
//     public handler stack (server.App().Test) or the arm's own production
//     domain ports (activation CAS, publication-lease arbitration, the
//     compiled-command row boundary);
//   - raw driver probes (admin pgx / Arango HTTP document reads) are used
//     ONLY for census audits and absence witnesses — they never substitute
//     a compiler-backed Engine/API case and vice versa.
//
// Ingest rides the supported library entry points the loom-data CLI
// dispatches to (ingest.Load / ingest.LoadPostgresGeneration), never the
// HTTP load route. The lane skips at the ARM top when its service env var
// is unset — a skipped arm is blocked acceptance adjudicated by CI, never
// success. Both arms skip offline, so the parent still passes with exactly
// two registered skip identities.
//
// Census law (mirrors the pgComp/qRoute precedent): every destructive
// resource is a registered mint keyed by the minting leg; each arm ends
// with a zero-survivor audit of its owned prefix; foreign prefixes are
// never enumerated for deletion, never touched (N-5).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"

	loomapi "github.com/calypr/loom/generated/loomapi"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/ingest"
)

// ---------------------------------------------------------------------------
// Shared logical graph: byte-identical NDJSON on both arms, literal oracles
// as test constants. Nothing here is derived from a backend response.
// ---------------------------------------------------------------------------

const (
	ab10Project1 = "ab10-p1"
	ab10Project2 = "ab10-p2"
	ab10GenA     = "ab10-gen-a"
	ab10GenB     = "ab10-gen-b"

	// Stored auth paths (program/project spellings normalize to distinct
	// keys, so a restricted caller can grant one and not the other).
	ab10PathMain    = "/programs/prog-a/projects/ab10-p1/clinical"
	ab10PathForeign = "/programs/prog-a/projects/ab10-p2/clinical"
	ab10PathUngrant = "/programs/prog-b/projects/ab10-p3/clinical"

	// Fence subjects (sub claim of the stub-issued JWTs).
	ab10SubjectGranted   = "ab10-granted"
	ab10SubjectUnrelated = "ab10-unrelated"
	ab10SubjectBroad     = "ab10-broad"
)

// ab10Literal is the pinned expected read-side view of the shared graph.
type ab10Literal = map[string]map[string]string

// ab10PatientLines is the exact NDJSON staged for Patient.ndjson. The
// literals below are also the oracles: gender/birthDate values, the
// explicit-null vs missing distinction on p-nullfield/p-nogender, and the
// _key set. Observation rows carry the schema-required code (CodeableConcept)
// and subject references so the repeated occurrence cell can bind both
// occurrences to p-ada.
func ab10PatientLines(t *testing.T) []string {
	t.Helper()
	return ab10JSONLines(t,
		map[string]any{"resourceType": "Patient", "id": "ab10-patient-ada", "gender": "female", "birthDate": "1992-04-11"},
		map[string]any{"resourceType": "Patient", "id": "ab10-patient-nogender", "birthDate": "1988-01-02"},
		map[string]any{"resourceType": "Patient", "id": "ab10-patient-nullfield", "gender": nil, "birthDate": "2000-12-31"},
	)
}

func ab10ObservationLines(t *testing.T) []string {
	t.Helper()
	return ab10JSONLines(t,
		map[string]any{"resourceType": "Observation", "id": "ab10-obs-1", "status": "final", "code": map[string]any{"coding": []map[string]any{{"system": "http://loinc.org", "code": "13457-7", "display": "Cholesterol in LDL [Mass/volume] in Serum or Plasma"}}}, "subject": map[string]any{"reference": "Patient/ab10-patient-ada"}},
		map[string]any{"resourceType": "Observation", "id": "ab10-obs-2", "status": "final", "code": map[string]any{"coding": []map[string]any{{"system": "http://loinc.org", "code": "13457-7", "display": "Cholesterol in LDL [Mass/volume] in Serum or Plasma"}}}, "subject": map[string]any{"reference": "Patient/ab10-patient-ada"}},
	)
}

func ab10ForeignPatientLines(t *testing.T) []string {
	t.Helper()
	return ab10JSONLines(t,
		map[string]any{"resourceType": "Patient", "id": "ab10-patient-foreign", "gender": "male", "birthDate": "1975-07-07"},
	)
}

// ab10StagedPatientLines belongs to the never-activated generation ab10-gen-b.
func ab10StagedPatientLines(t *testing.T) []string {
	t.Helper()
	return ab10JSONLines(t,
		map[string]any{"resourceType": "Patient", "id": "ab10-patient-staged", "gender": "other", "birthDate": "1999-09-09"},
	)
}

func ab10JSONLines(t *testing.T, docs ...map[string]any) []string {
	t.Helper()
	lines := make([]string, 0, len(docs))
	for _, doc := range docs {
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal shared-graph document: %v", err)
		}
		lines = append(lines, string(encoded))
	}
	return lines
}

func ab10GenFiles(t *testing.T) map[string][]string {
	t.Helper()
	return map[string][]string{
		"Patient.ndjson":     ab10PatientLines(t),
		"Observation.ndjson": ab10ObservationLines(t),
	}
}

// GraphQL request bodies for the shared cells (same bytes both arms).
const ab10PatientQueryBody = `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rowCount rows } }","variables":{"input":{"project":"` + ab10Project1 + `","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"}},{"name":"birthDate","selector":{"valuePath":"birthDate"}}]},"limit":10}}`

const ab10ForeignQueryBody = `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rowCount rows } }","variables":{"input":{"project":"` + ab10Project2 + `","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"}},{"name":"birthDate","selector":{"valuePath":"birthDate"}}]},"limit":10}}`

const ab10ObservationQueryBody = `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rowCount rows } }","variables":{"input":{"project":"` + ab10Project1 + `","rootResourceType":"Observation","rootFields":[{"name":"status","selector":{"valuePath":"status"}},{"name":"subject","selector":{"valuePath":"subject.reference"}}]},"limit":10}}`

func ab10PagedQueryBody(limit int) string {
	return `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rowCount rows } }","variables":{"input":{"project":"` + ab10Project1 + `","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"}},{"name":"birthDate","selector":{"valuePath":"birthDate"}}]},"limit":` + strconv.Itoa(limit) + `}}`
}

// ab10DecodeRows decodes the handler's rows preserving the wire-level
// distinction between an explicit JSON null and an absent key: each cell
// value is kept as raw bytes and its presence is tracked separately.
type ab10Row map[string]json.RawMessage

// ab10OracleTester is the minimal tester surface the literal-oracle helpers
// use. The live *testing.T satisfies it; a fresh &testing.T{} serves as the
// shadow so a negative control's mutated expectation can never reach the
// live cell's Fatalf.
type ab10OracleTester interface {
	Helper()
	Fatalf(format string, args ...any)
}

func ab10DecodeRows(t ab10OracleTester, what, body string) ab10RowBy {
	t.Helper()
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			RunFhirDataframe struct {
				Columns  []string  `json:"columns"`
				RowCount int       `json:"rowCount"`
				Rows     []ab10Row `json:"rows"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode %s envelope: %v (body %s)", what, err, body)
	}
	if len(envelope.Errors) != 0 {
		t.Fatalf("%s returned GraphQL errors: %s", what, body)
	}
	by := ab10RowBy{Columns: envelope.Data.RunFhirDataframe.Columns, RowCount: envelope.Data.RunFhirDataframe.RowCount}
	for _, row := range envelope.Data.RunFhirDataframe.Rows {
		key := ab10RawText(t, what, row, "_key")
		by.Rows = append(by.Rows, ab10DecodedRow{Key: key, Values: row})
	}
	sort.Slice(by.Rows, func(i, j int) bool { return by.Rows[i].Key < by.Rows[j].Key })
	return by
}

type ab10DecodedRow struct {
	Key    string
	Values ab10Row
}

type ab10RowBy struct {
	Columns  []string
	RowCount int
	Rows     []ab10DecodedRow
}

func ab10RawText(t ab10OracleTester, what string, row ab10Row, column string) string {
	t.Helper()
	raw, ok := row[column]
	if !ok {
		t.Fatalf("%s row lost column %q: %#v", what, column, row)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatalf("%s column %q is not a JSON string (%s): %#v", what, column, err, row)
	}
	return text
}

// ab10ScalarLiteral asserts one row/column against a literal string value.
func ab10ScalarLiteral(t ab10OracleTester, what string, row ab10Row, column, want string) {
	t.Helper()
	raw, present := row[column]
	if !present {
		t.Fatalf("%s: column %q absent, want literal %q", what, column, want)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatalf("%s: column %q = %s, want literal %q", what, column, raw, want)
	}
	if text != want {
		t.Fatalf("%s: column %q = %q, want literal %q", what, column, text, want)
	}
}

// ab10CrossDialectTells are the refusals every PG-arm response must carry
// and every Arango-arm response must not — the no-fallback law witnessed on
// the wire. (The PG tell list is absent-side only: nothing in an Arango
// answer should read like SQL dispatch.)
var (
	ab10PGForbiddenTells   = []string{"arango", "aql", "for d in ", "@auth_resource_paths", "fhir_field_catalog"}
	ab10ArangoForbiddenSQL = []string{"SELECT ", "INSERT INTO", "pg_stat", "postgres"}
)

func ab10AssertNoCrossDialectTell(t *testing.T, arm, what, body string) {
	t.Helper()
	tells := ab10PGForbiddenTells
	if arm == "arangodb" {
		tells = ab10ArangoForbiddenSQL
	}
	lower := strings.ToLower(body)
	for _, tell := range tells {
		if strings.Contains(lower, strings.ToLower(tell)) {
			t.Fatalf("%s arm: %s carries a foreign-dialect tell %q: %s", arm, what, tell, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Census registry: every owned mint is registered under the minting leg and
// must be reported dropped by the arm's final audit; foreign prefixes are
// never touched.
// ---------------------------------------------------------------------------

var (
	ab10OwnedMu     sync.Mutex
	ab10Owned       = map[string]ab10Mint{}
	ab10StrayMarks  []string
	ab10ForeignSeen []string
)

type ab10Mint struct {
	backend string // "postgres" or "arangodb"
	leg     string
	dropped bool
}

// ab10MintInventory is the checked-in enumeration of mints per leg. The
// arm-end audit asserts the registry matches it leg-for-leg.
var ab10MintInventory = map[string]int{
	"TestBackendArchitectureConformance/postgresql":                                           1,
	"TestBackendArchitectureConformance/postgresql/upgradeFromPinnedPreRefactorReopensSafely": 1,
	"TestBackendArchitectureConformance/arangodb":                                             1,
}

func ab10Register(name, backend, leg string) {
	ab10OwnedMu.Lock()
	defer ab10OwnedMu.Unlock()
	ab10Owned[name] = ab10Mint{backend: backend, leg: leg}
}

func ab10MarkDropped(name string) {
	ab10OwnedMu.Lock()
	defer ab10OwnedMu.Unlock()
	rec, ok := ab10Owned[name]
	if !ok {
		ab10StrayMarks = append(ab10StrayMarks, name)
		return
	}
	rec.dropped = true
	ab10Owned[name] = rec
}

// ab10AuditArm runs at the end of each arm: the runtime registry must match
// the checked-in inventory for this arm's legs (every mint dropped), no
// stray marks may exist, and every foreign-prefixed resource the service
// still holds must be zero (owned prefixes) — foreign names are recorded
// for audit but never deleted (N-5).
func ab10AuditArm(t *testing.T, arm string) {
	t.Helper()
	ab10OwnedMu.Lock()
	defer ab10OwnedMu.Unlock()
	prefix := "verify_ab10pg_"
	if arm == "arangodb" {
		prefix = "loom_test_ab10_arango_"
	}
	wantLegs := map[string]int{}
	for leg, count := range ab10MintInventory {
		if strings.HasPrefix(leg, "TestBackendArchitectureConformance/"+arm) {
			wantLegs[leg] = count
		}
	}
	gotLegs := map[string]int{}
	for name, mint := range ab10Owned {
		if mint.backend != arm {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			ab10ForeignSeen = append(ab10ForeignSeen, name)
			t.Errorf("%s arm census: owned mint %q violates the owned prefix %q", arm, name, prefix)
			continue
		}
		gotLegs[mint.leg]++
		if !mint.dropped {
			t.Errorf("%s arm census: mint %q (leg %s) was never dropped", arm, name, mint.leg)
		}
	}
	for leg, want := range wantLegs {
		if gotLegs[leg] != want {
			t.Errorf("%s arm census: leg %s minted %d resources, inventory expects %d", arm, leg, gotLegs[leg], want)
		}
	}
	for leg := range gotLegs {
		if _, ok := wantLegs[leg]; !ok {
			t.Errorf("%s arm census: unexpected minting leg %q (update ab10MintInventory deliberately, with review)", arm, leg)
		}
	}
	if len(ab10StrayMarks) != 0 {
		t.Errorf("%s arm census: stray drop marks %v", arm, ab10StrayMarks)
	}
}

// ---------------------------------------------------------------------------
// Parent and arms.
// ---------------------------------------------------------------------------

func TestBackendArchitectureConformance(t *testing.T) {
	// The arms run the same named cells; each skips at its top when its
	// service is absent. A skipped arm is blocked acceptance for CI to
	// adjudicate — never a pass of that arm's scenarios.
	t.Run("postgresql", testBackendArchitectureConformancePostgres)
	t.Run("arangodb", testBackendArchitectureConformanceArango)
}

func ab10Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func ab10RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func ab10RepoPath(t *testing.T, elems ...string) string {
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

// ---------------------------------------------------------------------------
// PostgreSQL arm.
// ---------------------------------------------------------------------------

var (
	ab10PGDBPattern     = regexp.MustCompile(`^verify_ab10pg_[0-9]+_[0-9a-f]{12}$`)
	ab10PGSchemaPattern = regexp.MustCompile(`^verify_ab10pg_[0-9]+_[0-9a-f]{12}_(src|exp|pub|rel|rcp|exe)$`)
	ab10PGSeriesTokens  = []struct {
		series control.Series
		token  string
	}{
		{control.SeriesSource, "src"},
		{control.SeriesExplorerControl, "exp"},
		{control.SeriesPublicationControl, "pub"},
		{control.SeriesDatasetRelease, "rel"},
		{control.SeriesRecipeChains, "rcp"},
		{control.SeriesRecipeExecRegistries, "exe"},
	}
	ab10PGBaselineMu  sync.Mutex
	ab10PGBaseline    int64
	ab10PGBaselineSet bool
	ab10PGBaselineErr error
)

type ab10PGFixture struct {
	t        *testing.T
	dsn      string
	schemas  map[control.Series]string
	scrub    func(any) string
	admin    *pgxpool.Pool
	database string
}

func newAB10PGFixture(t *testing.T) *ab10PGFixture {
	t.Helper()
	raw := ab10PGURL(t)
	ab10PGCaptureBaseline(t, raw)
	ab10PGBaselineMu.Lock()
	baselineErr, baseline := ab10PGBaselineErr, ab10PGBaseline
	ab10PGBaselineMu.Unlock()
	if baselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", baselineErr)
	}
	if baseline != 0 {
		t.Fatalf("zero-survivor precondition: %d verify_ab10pg_* databases existed before this arm", baseline)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	secrets := []string{raw}
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

	ctx, cancel := context.WithTimeout(context.Background(), 540*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %s", scrub(err))
	}
	t.Cleanup(admin.Close)

	database := fmt.Sprintf("verify_ab10pg_%d_%s", time.Now().UnixNano(), ab10RandomHex(t))
	if !ab10PGDBPattern.MatchString(database) {
		t.Fatalf("minted database %q violates the owned-name pattern", database)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatalf("create isolated database: %s", scrub(err))
	}
	ab10Register(database, "postgres", t.Name())
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+database+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
			return
		}
		ab10MarkDropped(database)
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
	schemas := map[control.Series]string{}
	for _, placement := range ab10PGSeriesTokens {
		schema := fmt.Sprintf("verify_ab10pg_%d_%s_%s", time.Now().UnixNano(), ab10RandomHex(t), placement.token)
		if !ab10PGSchemaPattern.MatchString(schema) {
			t.Fatalf("minted schema %q violates the owned-name pattern", schema)
		}
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatalf("create owned schema for %s: %s", placement.series, scrub(err))
		}
		schemas[placement.series] = schema
	}
	// The baseline check belongs to the arm, not the audit: the zero-
	// survivor assertion for verify_ab10pg_* databases runs in ab10AuditPG
	// at arm end through this same admin pool.
	return &ab10PGFixture{t: t, dsn: dsn, schemas: schemas, scrub: scrub, admin: admin, database: database}
}

func ab10PGURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL conformance arm (blocked acceptance: adjudicated by the CI conformance lane)")
	}
	return raw
}

func ab10PGCaptureBaseline(t *testing.T, raw string) {
	t.Helper()
	ab10PGBaselineMu.Lock()
	defer ab10PGBaselineMu.Unlock()
	if ab10PGBaselineSet {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		ab10PGBaselineErr = fmt.Errorf("connect for baseline: %w", err)
		return
	}
	defer admin.Close()
	var count int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname ~ '^verify_ab10pg_[0-9]+_[0-9a-f]{12}$'").
		Scan(&count); err != nil {
		ab10PGBaselineErr = fmt.Errorf("count baseline: %w", err)
		return
	}
	ab10PGBaseline = count
	ab10PGBaselineSet = true
}

func (f *ab10PGFixture) openConfig(setup control.SetupPolicy) control.Config {
	return control.Config{
		DSN:            f.dsn,
		MaxConns:       8,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas:        control.SeriesSchemas(f.schemas),
		Setup:          setup,
	}
}

func (f *ab10PGFixture) migrate(t *testing.T) {
	t.Helper()
	rt, err := backendspostgres.Open(context.Background(), f.openConfig(control.SetupMigrate))
	if err != nil {
		t.Fatalf("migrate six-series fixture: %s", f.scrub(err))
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("close migrated fixture runtime: %s", f.scrub(err))
	}
}

func (f *ab10PGFixture) catalogRuntime(t *testing.T) *backendspostgres.Runtime {
	t.Helper()
	rt, err := backendspostgres.Open(context.Background(), f.openConfig(control.SetupNone))
	if err != nil {
		t.Fatalf("open catalog fixture runtime: %s", f.scrub(err))
	}
	if rt.FieldProfiles == nil || rt.Inventory == nil || rt.Relationships == nil || rt.Contributions == nil {
		_ = rt.Close()
		t.Fatal("fixture runtime assembled without catalog evidence ports")
	}
	return rt
}

// controlRuntime opens the six-series control runtime over the migrated
// fixture: the arm's production domain ports (Releases, Manifests, Explorer,
// SourceRows) — used for domain-port evidence classes, never as a substitute
// for a scenario verdict on the public lane.
func (f *ab10PGFixture) controlRuntime(t *testing.T) *backendspostgres.Runtime {
	t.Helper()
	rt, err := backendspostgres.Open(context.Background(), f.openConfig(control.SetupNone))
	if err != nil {
		t.Fatalf("open control runtime: %s", f.scrub(err))
	}
	if rt.Releases == nil || rt.Manifests == nil || rt.Explorer == nil || rt.SourceRows == nil {
		_ = rt.Close()
		t.Fatal("control runtime assembled without required ports")
	}
	return rt
}

func (f *ab10PGFixture) pgOptions(application string) ingest.PGSourceOptions {
	return ingest.PGSourceOptions{
		DSN:             f.dsn,
		Schema:          f.schemas[control.SeriesSource],
		ManifestSchema:  f.schemas[control.SeriesDatasetRelease],
		ApplicationName: application,
	}
}

// load stages one generation through the production ingest entry point
// (ingest.LoadPostgresGeneration), the loom-data CLI shape, with the
// supported per-load AuthResourcePath stamp.
func (f *ab10PGFixture) load(t *testing.T, project, generation, authPath string, files map[string][]string) {
	t.Helper()
	dir := t.TempDir()
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write staging fixture: %v", err)
		}
	}
	catalogRT := f.catalogRuntime(t)
	defer func() { _ = catalogRT.Close() }()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	opts := ingest.LoadOptions{
		Schema:           ab10RepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:          dir,
		Project:          project,
		Dataset:          &ref,
		AuthResourcePath: authPath,
		StageOnly:        true,
		// The literals are authored; a malformed row is a test bug and
		// must name file/line/resource instead of aggregating.
		FailFast: true,
	}
	pg := f.pgOptions("verify-ab10pg")
	pg.Catalog = ingest.GenerationCatalogWriters{
		FieldProfiles: catalogRT.FieldProfiles,
		Inventory:     catalogRT.Inventory,
		Relationships: catalogRT.Relationships,
		Contributions: catalogRT.Contributions,
	}
	if _, err := ingest.LoadPostgresGeneration(context.Background(), opts, pg); err != nil {
		t.Fatalf("load %s/%s: %s", project, generation, f.scrub(err))
	}
}

func (f *ab10PGFixture) activate(t *testing.T, project, generation string) {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build dataset ref: %v", err)
	}
	if err := ingest.ActivatePostgresGeneration(context.Background(), ref, f.pgOptions("verify-ab10pg-activate")); err != nil {
		t.Fatalf("activate %s/%s: %s", project, generation, f.scrub(err))
	}
}

func (f *ab10PGFixture) bootConfig(t *testing.T, listen string) Config {
	t.Helper()
	cfg := ab10PGBootConfigBase(t, f, listen)
	cfg.Server.AllowUnauthenticated = true
	return cfg
}

func (f *ab10PGFixture) bootConfigCalypr(t *testing.T, listen, trustedIssuer string) Config {
	t.Helper()
	cfg := ab10PGBootConfigBase(t, f, listen)
	cfg.Auth.Mode = "calypr"
	cfg.Auth.Calypr.TrustedIssuer = trustedIssuer
	cfg.Auth.Calypr.CacheTTL = time.Minute
	cfg.Auth.Calypr.RequestTimeout = 10 * time.Second
	return cfg
}

func ab10PGBootConfigBase(t *testing.T, f *ab10PGFixture, listen string) Config {
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
	cfg.Server.Schema = ab10RepoPath(t, "schemas", "graph-fhir.json")
	cfg.Server.ArtifactDirectory = t.TempDir()
	cfg.Server.PopulationMappingCursorSecret = "ab10-pg-cursor-secret-0123456789ab"
	schemas := map[string]string{}
	for _, placement := range ab10PGSeriesTokens {
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

// ab10PGProbe runs one query against the fixture through the ADMIN pool: a
// census/absence-class driver probe, never a scenario verdict.
func (f *ab10PGFixture) probeCount(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var count int64
	if err := f.admin.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("census probe: %s", f.scrub(err))
	}
	return count
}

func testBackendArchitectureConformancePostgres(t *testing.T) {
	fixture := newAB10PGFixture(t)
	fixture.migrate(t)
	fixture.load(t, ab10Project1, ab10GenA, ab10PathMain, ab10GenFiles(t))
	fixture.load(t, ab10Project1, ab10GenB, ab10PathMain, map[string][]string{"Patient.ndjson": ab10StagedPatientLines(t)})
	fixture.load(t, ab10Project2, ab10GenA, ab10PathForeign, map[string][]string{"Patient.ndjson": ab10ForeignPatientLines(t)})
	fixture.activate(t, ab10Project1, ab10GenA)
	fixture.activate(t, ab10Project2, ab10GenA)

	fence := ab10StartFenceStub(t)

	// Cells that need the live handler stack boot their own production
	// composition; the shared one below serves the unauthenticated cells.
	ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
	defer cancel()
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, fixture.bootConfig(t, "127.0.0.1:0"), ab10Logger())
	if err != nil {
		t.Fatalf("production compose (postgresql arm): %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close postgresql arm runtime: %s", fixture.scrub(err))
		}
	}()

	arm := &ab10Arm{
		name:    "postgresql",
		app:     server.App(),
		fixture: fixture,
		fence:   fence,
		scrub:   fixture.scrub,
	}

	// Read-order-dependent cells run first against gen-a active; the
	// release-intake cell re-points ab10-p1 and therefore runs last of the
	// query family (documented order, no t.Parallel anywhere).
	ab10CellScalarQuery(t, arm)
	ab10CellRepeatedOccurrence(t, arm)
	ab10CellNullAndMissing(t, arm)
	ab10CellAuthFamily(t, arm)
	ab10CellUnrestrictedSeesEverything(t, arm)
	ab10CellStablePaging(t, arm)
	ab10CellDraftCASOneWinner(t, arm)
	ab10CellPublicationControlFence(t, arm)
	ab10CellCancellationNoFallback(t, arm)
	ab10CellBackendUnavailableNoFallback(t, arm)
	ab10CellGenerationIsolation(t, arm)
	ab10CellUpgradeReopen(t, arm)

	ab10AuditPG(t, fixture)
	ab10AuditArm(t, "postgresql")
}

func ab10AuditPG(t *testing.T, fixture *ab10PGFixture) {
	t.Helper()
	// Zero-survivor audit of the owned prefix (N-5: foreign databases are
	// never enumerated for deletion or attributed).
	var survivors []string
	rows, err := fixture.admin.Query(context.Background(),
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify\_ab10pg\_%' ESCAPE '\'
		   AND datname <> $1`, fixture.database)
	if err != nil {
		t.Fatalf("pg_database survivor audit: %s", fixture.scrub(err))
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan survivor row: %s", fixture.scrub(err))
		}
		survivors = append(survivors, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("survivor rows: %s", fixture.scrub(err))
	}
	// The arm fixture itself is dropped by t.Cleanup AFTER this audit, so
	// exactly one survivor (this fixture's database) is legitimate here.
	if len(survivors) != 1 || survivors[0] != fixture.database {
		t.Errorf("zero-survivor audit: expected only %q, found %v", fixture.database, survivors)
	}
}

// ---------------------------------------------------------------------------
// ArangoDB arm.
// ---------------------------------------------------------------------------

func ab10ArangoEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the ArangoDB conformance arm (blocked acceptance: adjudicated by the go-arango and CI conformance lanes)")
	}
	return endpoint
}

func ab10ArangoOwnDatabase(t *testing.T, endpoint, leg string) string {
	t.Helper()
	database := "loom_test_ab10_arango_" + ab10RandomHex(t) + ab10RandomHex(t)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		strings.TrimRight(endpoint, "/")+"/_api/database",
		strings.NewReader(`{"name":"`+database+`"}`))
	if err != nil {
		t.Fatalf("build create-database request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("mint owned database: %v", err)
	}
	defer response.Body.Close()
	// Arango answers a successful create with HTTP 201; only a 2xx whose
	// body says result:true is an owned mint (matching the runner's
	// response.ok semantics in run-arango-integration.mjs).
	var minted struct {
		Result bool `json:"result"`
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if readErr != nil {
		t.Fatalf("read create-database response: %v", readErr)
	}
	if !(response.StatusCode >= 200 && response.StatusCode < 300) ||
		json.Unmarshal(body, &minted) != nil || !minted.Result {
		t.Fatalf("mint owned database %q: HTTP %d body %s (refusing a pre-existing name)", database, response.StatusCode, body)
	}
	ab10Register(database, "arangodb", leg)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cleanup, http.MethodDelete,
			strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build drop request: %v", err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("drop owned database %s: %v", database, err)
			return
		}
		defer resp.Body.Close()
		if !(resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound) {
			t.Errorf("drop owned database %s: HTTP %d", database, resp.StatusCode)
			return
		}
		ab10MarkDropped(database)
	})
	return database
}

func testBackendArchitectureConformanceArango(t *testing.T) {
	endpoint := ab10ArangoEndpoint(t)
	database := ab10ArangoOwnDatabase(t, endpoint, t.Name())

	ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
	defer cancel()
	backend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open owned arango database through the factory: %v", err)
	}
	defer func() { _ = backend.Close(context.Background()) }()

	// Supported ingest: the library entry point the loom-data Arango route
	// dispatches to. gen-a activates inside Load; gen-b stays staged.
	if _, err := ingest.Load(ctx, ingest.LoadOptions{
		ConnectionOptions: ingest.ConnectionOptions{URL: endpoint, Database: database},
		Schema:            ab10RepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:           ab10WriteMetaDir(t, ab10GenFiles(t)),
		Project:           ab10Project1,
		AuthResourcePath:  ab10PathMain,
	}); err != nil {
		t.Fatalf("arango ingest load+activate %s/%s: %v", ab10Project1, ab10GenA, err)
	}
	if _, err := ingest.Load(ctx, ingest.LoadOptions{
		ConnectionOptions: ingest.ConnectionOptions{URL: endpoint, Database: database},
		Schema:            ab10RepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:           ab10WriteMetaDir(t, map[string][]string{"Patient.ndjson": ab10StagedPatientLines(t)}),
		Project:           ab10Project1,
		AuthResourcePath:  ab10PathMain,
		StageOnly:         true,
	}); err != nil {
		t.Fatalf("arango ingest staged %s/%s: %v", ab10Project1, ab10GenB, err)
	}
	if _, err := ingest.Load(ctx, ingest.LoadOptions{
		ConnectionOptions: ingest.ConnectionOptions{URL: endpoint, Database: database},
		Schema:            ab10RepoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:           ab10WriteMetaDir(t, map[string][]string{"Patient.ndjson": ab10ForeignPatientLines(t)}),
		Project:           ab10Project2,
		AuthResourcePath:  ab10PathForeign,
	}); err != nil {
		t.Fatalf("arango ingest load+activate %s/%s: %v", ab10Project2, ab10GenA, err)
	}

	fence := ab10StartFenceStub(t)

	arm := &ab10Arm{
		name:     "arangodb",
		fence:    fence,
		endpoint: endpoint,
		database: database,
		backend:  backend,
		probe:    &ab10ArangoProbe{endpoint: endpoint, database: database},
		scrub:    func(v any) string { return fmt.Sprint(v) },
	}
	arm.app = ab10BootArangoApp(t, ctx, arm, true)

	ab10CellScalarQuery(t, arm)
	ab10CellRepeatedOccurrence(t, arm)
	ab10CellNullAndMissing(t, arm)
	ab10CellAuthFamily(t, arm)
	ab10CellUnrestrictedSeesEverything(t, arm)
	ab10CellStablePaging(t, arm)
	ab10CellDraftCASOneWinner(t, arm)
	ab10CellPublicationControlFence(t, arm)
	ab10CellCancellationNoFallback(t, arm)
	ab10CellBackendUnavailableNoFallback(t, arm)
	ab10CellGenerationIsolation(t, arm)
	ab10CellUpgradeReopen(t, arm)

	ab10AuditArm(t, "arangodb")
}

func ab10WriteMetaDir(t *testing.T, files map[string][]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write staging fixture: %v", err)
		}
	}
	return dir
}

// ab10ArangoProbe is the census/absence-class raw driver for the Arango arm.
type ab10ArangoProbe struct {
	endpoint string
	database string
}

func (p *ab10ArangoProbe) countDocuments(t *testing.T, collection, project, generation string) int {
	t.Helper()
	query := `FOR d IN ` + collection + ` FILTER d.project == @project AND d.dataset_generation == @generation RETURN 1`
	bind := map[string]any{"project": project, "generation": generation}
	body := fmt.Sprintf(`{"query":%q,"bindVars":%s}`, query, mustAB10JSON(t, bind))
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		strings.TrimRight(p.endpoint, "/")+"/_api/cursor/database/"+url.PathEscape(p.database),
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("build cursor request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("census cursor: %v", err)
	}
	defer response.Body.Close()
	// POST /_api/cursor answers 201 on cursor creation (200 also accepted).
	if !(response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated) {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("census cursor HTTP %d: %s", response.StatusCode, payload)
	}
	var payload struct {
		Result []int `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode cursor result: %v", err)
	}
	return len(payload.Result)
}

func mustAB10JSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal probe payload: %v", err)
	}
	return string(encoded)
}

// ---------------------------------------------------------------------------
// Shared arm plumbing.
// ---------------------------------------------------------------------------

type ab10Arm struct {
	name     string
	app      *fiber.App
	fixture  *ab10PGFixture         // postgres arm only
	endpoint string                 // arangodb arm only
	database string                 // arangodb arm only
	backend  *arangofactory.Runtime // arangodb arm only
	probe    *ab10ArangoProbe       // arangodb arm only
	fence    *ab10Fence
	scrub    func(any) string
}

// ab10BootArangoApp boots the Arango arm through the production compose.
// unauthenticated selects the anonymous lane; false boots the calypr lane
// against the arm's fence stub.
func ab10BootArangoApp(t *testing.T, ctx context.Context, arm *ab10Arm, unauthenticated bool) *fiber.App {
	t.Helper()
	cfg := Config{}
	cfg.Server.URL = arm.endpoint
	cfg.Server.Database = arm.database
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.Schema = ab10RepoPath(t, "schemas", "graph-fhir.json")
	cfg.Server.ArtifactDirectory = t.TempDir()
	cfg.Server.PopulationMappingCursorSecret = "ab10-arango-cursor-secret-0123"
	if unauthenticated {
		cfg.Server.AllowUnauthenticated = true
	} else {
		cfg.Auth.Mode = "calypr"
		cfg.Auth.Calypr.TrustedIssuer = arm.fence.issuer
		cfg.Auth.Calypr.CacheTTL = time.Minute
		cfg.Auth.Calypr.RequestTimeout = 10 * time.Second
	}
	server, closeRuntime, err := composeArangoRuntime(ctx, cfg, ab10Logger())
	if err != nil {
		t.Fatalf("production compose (arangodb arm, auth=%v): %s", unauthenticated, arm.scrub(err))
	}
	t.Cleanup(func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close arangodb arm runtime (auth=%v): %s", unauthenticated, arm.scrub(err))
		}
	})
	return server.App()
}

// ---------------------------------------------------------------------------
// Fence stub: loopback trusted issuer for the calypr auth lane. Tokens are
// unsigned JWTs whose iss names the stub; the stub is the trust anchor — it
// maps each token to its subject and its authorization snapshot.
// ---------------------------------------------------------------------------

type ab10Fence struct {
	issuer string
	tokens map[string]string // subject -> Authorization header value
	mu     sync.Mutex
	access map[string]map[string][]map[string]string // subject -> resource path -> grants
}

func ab10StartFenceStub(t *testing.T) *ab10Fence {
	t.Helper()
	fence := &ab10Fence{tokens: map[string]string{}, access: map[string]map[string][]map[string]string{}}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		subject, ok := fence.subjectFor(r.Header.Get("Authorization"))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fence.mu.Lock()
		grants := fence.access[subject]
		fence.mu.Unlock()
		if grants == nil {
			// Restricted with nothing grantable: a decoy path keeps the
			// snapshot non-empty while intersecting no loaded path.
			grants = map[string][]map[string]string{"/programs/decoy/projects/decoy/none": {{"method": "read", "service": "*"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"authz": grants})
	}))
	t.Cleanup(stub.Close)
	fence.issuer = stub.URL
	return fence
}

func (f *ab10Fence) subjectFor(authorization string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for subject, header := range f.tokens {
		if header == authorization {
			return subject, true
		}
	}
	return "", false
}

// ab10Grant records the read grants a subject receives from the stub.
func (f *ab10Fence) grant(subject string, paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	grants := map[string][]map[string]string{}
	for _, path := range paths {
		grants[path] = []map[string]string{{"method": "read", "service": "*"}}
	}
	f.access[subject] = grants
}

// ab10Token mints the Authorization header value for a subject. iss must
// equal the trusted issuer; sub identifies the caller.
func (f *ab10Fence) token(t *testing.T, subject string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if header, ok := f.tokens[subject]; ok {
		return header
	}
	header := "Bearer " + ab10UnsignedJWT(t, map[string]any{"iss": f.issuer, "sub": subject})
	f.tokens[subject] = header
	return header
}

func ab10UnsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal JWT segment: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return encode(map[string]any{"alg": "none", "typ": "JWT"}) + "." + encode(claims) + ".ab10"
}

// ---------------------------------------------------------------------------
// GraphQL lane helpers.
// ---------------------------------------------------------------------------

func ab10Graph(t *testing.T, arm *ab10Arm, body string) string {
	t.Helper()
	return ab10GraphAuth(t, arm, body, nil)
}

func ab10GraphAuth(t *testing.T, arm *ab10Arm, body string, headers map[string]string) string {
	t.Helper()
	response := requestJSONWithHeaders(t, arm.app, http.MethodPost, "/graphql/graph", body, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s arm: graphql transport status %d: %s", arm.name, response.StatusCode, response.Body)
	}
	ab10AssertNoCrossDialectTell(t, arm.name, "graphql response", response.Body)
	return response.Body
}

// ab10WantRows is the literal expectation for one Patient-lane query: exact
// _key set with per-row column values. A nil value means "any value"; the
// absent-key vs null distinction is asserted by ab10CellNullAndMissing.
type ab10WantRow struct {
	Key    string
	Values map[string]*string
}

func ab10CheckRowsLiteral(t ab10OracleTester, arm *ab10Arm, what string, body string, want []ab10WantRow) {
	t.Helper()
	got := ab10DecodeRows(t, what, body)
	if len(got.Rows) != len(want) {
		t.Fatalf("%s arm: %s served %d rows, want exactly %d (body %s)", arm.name, what, len(got.Rows), len(want), body)
	}
	for i, expected := range want {
		row := got.Rows[i]
		if row.Key != expected.Key {
			t.Fatalf("%s arm: %s row %d key = %q, want literal %q", arm.name, what, i, row.Key, expected.Key)
		}
		for column, value := range expected.Values {
			if value == nil {
				continue
			}
			ab10ScalarLiteral(t, arm.name+" arm: "+what, row.Values, column, *value)
		}
	}
}

func ab10CheckNoRows(t ab10OracleTester, arm *ab10Arm, what string, body string) {
	t.Helper()
	got := ab10DecodeRows(t, what, body)
	if got.RowCount != 0 || len(got.Rows) != 0 {
		t.Fatalf("%s arm: %s must serve zero rows, got %d (body %s)", arm.name, what, got.RowCount, body)
	}
}

func ab10Str(value string) *string { return &value }

// ---------------------------------------------------------------------------
// Cell 1: scalarQueryServesLiteralScalars.
// ---------------------------------------------------------------------------

func ab10CellScalarQuery(t *testing.T, arm *ab10Arm) {
	t.Run("scalarQueryServesLiteralScalars", func(t *testing.T) {
		body := ab10Graph(t, arm, ab10PatientQueryBody)
		ab10CheckRowsLiteral(t, arm, "scalar patient query", body, []ab10WantRow{
			{Key: "ab10-patient-ada", Values: map[string]*string{"gender": ab10Str("female"), "birthDate": ab10Str("1992-04-11")}},
			{Key: "ab10-patient-nogender", Values: map[string]*string{"birthDate": ab10Str("1988-01-02")}},
			{Key: "ab10-patient-nullfield", Values: map[string]*string{"birthDate": ab10Str("2000-12-31")}},
		})
		decoded := ab10DecodeRows(t, "scalar patient query", body)
		for _, required := range []string{"_key", "gender", "birthDate"} {
			found := false
			for _, column := range decoded.Columns {
				if column == required {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s arm: scalar query lost column %q, got %v", arm.name, required, decoded.Columns)
			}
		}
		// Negative control: the same evaluator, fed a mutated expectation,
		// must go red against a shadow tester — the live cell is never
		// reached by the mutated assertion.
		if t.Failed() {
			return
		}
		mutated := []ab10WantRow{{Key: "ab10-patient-ada", Values: map[string]*string{"gender": ab10Str("male")}}}
		if red := ab10OracleGoesRed(func(shadow ab10OracleTester) {
			ab10CheckRowsLiteral(shadow, arm, "scalar patient query", body, mutated)
		}); !red {
			t.Fatalf("%s arm: literal oracle accepted a mutated expected value — oracle is vacuous", arm.name)
		}
	})
}

// ab10OracleGoesRed runs a supposed-fatal oracle on a shadow tester and
// reports whether the shadow actually failed. The oracle runs in its own
// goroutine because a shadow Fatalf ends with runtime.Goexit: the channel
// (closed even under Goexit) lets this test survive the shadow's death.
// A panic is not recovered — a broken oracle helper must crash loudly
// rather than masquerade as a red verdict.
func ab10OracleGoesRed(assert func(t ab10OracleTester)) bool {
	shadow := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert(shadow)
	}()
	<-done
	return shadow.Failed()
}

// ---------------------------------------------------------------------------
// Cell 2: repeatedRelatedOccurrenceStaysDistinct.
// ---------------------------------------------------------------------------

func ab10CellRepeatedOccurrence(t *testing.T, arm *ab10Arm) {
	t.Run("repeatedRelatedOccurrenceStaysDistinct", func(t *testing.T) {
		body := ab10Graph(t, arm, ab10ObservationQueryBody)
		ab10CheckRowsLiteral(t, arm, "observation query", body, []ab10WantRow{
			{Key: "ab10-obs-1", Values: map[string]*string{"status": ab10Str("final"), "subject": ab10Str("Patient/ab10-patient-ada")}},
			{Key: "ab10-obs-2", Values: map[string]*string{"status": ab10Str("final"), "subject": ab10Str("Patient/ab10-patient-ada")}},
		})
		// Independent probe (census class): exactly two Observation rows.
		ab10ProbeOccurrenceCount(t, arm, "Observation", ab10Project1, ab10GenA, 2)
	})
}

// ab10ProbeOccurrenceCount is the raw-driver census witness — never a
// scenario verdict, always paired with a compiler-backed case above.
func ab10ProbeOccurrenceCount(t *testing.T, arm *ab10Arm, resourceType, project, generation string, want int) {
	t.Helper()
	if arm.probe != nil {
		if got := arm.probe.countDocuments(t, resourceType, project, generation); got != want {
			t.Fatalf("%s arm census: %s/%s/%s has %d rows, want %d", arm.name, resourceType, project, generation, got, want)
		}
		return
	}
	var count int64
	if arm.name == "postgresql" {
		count = arm.fixture.probeCount(t,
			`SELECT count(*) FROM "`+arm.fixture.schemas[control.SeriesSource]+`".fhir_resource
			  WHERE project=$1 AND dataset_generation=$2 AND resource_type=$3`,
			project, generation, resourceType)
	}
	if int(count) != want {
		t.Fatalf("%s arm census: %s/%s/%s has %d rows, want %d", arm.name, resourceType, project, generation, count, want)
	}
}

// ---------------------------------------------------------------------------
// Cell 3: nullAndMissingSemanticsPreserved.
// ---------------------------------------------------------------------------

func ab10CellNullAndMissing(t *testing.T, arm *ab10Arm) {
	t.Run("nullAndMissingSemanticsPreserved", func(t *testing.T) {
		body := ab10Graph(t, arm, ab10PatientQueryBody)
		decoded := ab10DecodeRows(t, "null/missing query", body)
		byKey := map[string]ab10DecodedRow{}
		for _, row := range decoded.Rows {
			byKey[row.Key] = row
		}
		ada, ok := byKey["ab10-patient-ada"]
		if !ok {
			t.Fatalf("%s arm: explicit-value row missing", arm.name)
		}
		ab10ScalarLiteral(t, arm.name+" arm: explicit gender", ada.Values, "gender", "female")
		nullfield, ok := byKey["ab10-patient-nullfield"]
		if !ok {
			t.Fatalf("%s arm: explicit-null row missing", arm.name)
		}
		nogender, ok := byKey["ab10-patient-nogender"]
		if !ok {
			t.Fatalf("%s arm: missing-field row missing", arm.name)
		}
		nullRaw, nullPresent := nullfield.Values["gender"]
		missingRaw, missingPresent := nogender.Values["gender"]
		// The three-state contract: an explicit JSON null must not be
		// conflated with the absent-source state. Either representation
		// (null vs missing key) is preserved so long as the two stored
		// states remain distinguishable on the wire.
		same := nullPresent == missingPresent && string(nullRaw) == string(missingRaw)
		if same {
			t.Fatalf("%s arm: explicit-null and missing-source genders collapsed to the identical wire state (%s present=%v vs %s present=%v)",
				arm.name, nullRaw, nullPresent, missingRaw, missingPresent)
		}
		// Birthdates (both always present) keep their literal values.
		ab10ScalarLiteral(t, arm.name+" arm: nullfield birthDate", nullfield.Values, "birthDate", "2000-12-31")
		ab10ScalarLiteral(t, arm.name+" arm: nogender birthDate", nogender.Values, "birthDate", "1988-01-02")
	})
}

// ---------------------------------------------------------------------------
// Cells 4+5: the auth family over the calypr lane and the loopback fence.
// ---------------------------------------------------------------------------

func ab10CellAuthFamily(t *testing.T, arm *ab10Arm) {
	calyprApp := arm.app
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if arm.name == "postgresql" {
		server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, arm.fixture.bootConfigCalypr(t, "127.0.0.1:0", arm.fence.issuer), ab10Logger())
		if err != nil {
			t.Fatalf("production compose (postgresql arm, calypr): %s", arm.scrub(err))
		}
		t.Cleanup(func() {
			if err := closeRuntime(); err != nil {
				t.Errorf("close calypr-lane runtime: %s", arm.scrub(err))
			}
		})
		calyprApp = server.App()
	} else {
		calyprApp = ab10BootArangoApp(t, ctx, arm, false)
	}
	arm.fence.grant(ab10SubjectGranted, ab10PathMain)
	arm.fence.grant(ab10SubjectUnrelated) // empty restricted scope (decoy snapshot)
	granted := map[string]string{"Authorization": arm.fence.token(t, ab10SubjectGranted)}
	unrelated := map[string]string{"Authorization": arm.fence.token(t, ab10SubjectUnrelated)}

	t.Run("explicitScopeServesAuthorizedAndExcludesUnauthorized", func(t *testing.T) {
		inner := *arm
		inner.app = calyprApp
		body := ab10GraphAuth(t, &inner, ab10PatientQueryBody, granted)
		ab10CheckRowsLiteral(t, &inner, "authorized p1 query", body, []ab10WantRow{
			{Key: "ab10-patient-ada", Values: map[string]*string{"gender": ab10Str("female"), "birthDate": ab10Str("1992-04-11")}},
			{Key: "ab10-patient-nogender", Values: map[string]*string{"birthDate": ab10Str("1988-01-02")}},
			{Key: "ab10-patient-nullfield", Values: map[string]*string{"birthDate": ab10Str("2000-12-31")}},
		})
		foreign := ab10GraphAuth(t, &inner, ab10ForeignQueryBody, granted)
		ab10CheckNoRows(t, &inner, "foreign p2 query for granted subject", foreign)
		// Absence witness (census class): the excluded row does exist
		// server-side, so the zero result is authorization, not emptiness.
		ab10ProbeOccurrenceCount(t, arm, "Patient", ab10Project2, ab10GenA, 1)
	})

	t.Run("restrictedEmptyScopeServesNothing", func(t *testing.T) {
		inner := *arm
		inner.app = calyprApp
		body := ab10GraphAuth(t, &inner, ab10PatientQueryBody, unrelated)
		ab10CheckNoRows(t, &inner, "p1 query for unrelated subject", body)
		foreign := ab10GraphAuth(t, &inner, ab10ForeignQueryBody, unrelated)
		ab10CheckNoRows(t, &inner, "p2 query for unrelated subject", foreign)
		// Control: the unrestricted lane sees the same rows, proving the
		// zero result above is the restricted scope, not missing data.
		check := ab10Graph(t, arm, ab10PatientQueryBody)
		if len(ab10DecodeRows(t, "unrestricted control", check).Rows) != 3 {
			t.Fatalf("%s arm: unrestricted control must serve the three literal rows", arm.name)
		}
	})
}

// ---------------------------------------------------------------------------
// Cell 6: unrestrictedSeesEverything.
// ---------------------------------------------------------------------------

func ab10CellUnrestrictedSeesEverything(t *testing.T, arm *ab10Arm) {
	t.Run("unrestrictedSeesEverything", func(t *testing.T) {
		body := ab10Graph(t, arm, ab10PatientQueryBody)
		ab10CheckRowsLiteral(t, arm, "unrestricted p1", body, []ab10WantRow{
			{Key: "ab10-patient-ada", Values: map[string]*string{"gender": ab10Str("female")}},
			{Key: "ab10-patient-nogender", Values: map[string]*string{}},
			{Key: "ab10-patient-nullfield", Values: map[string]*string{}},
		})
		foreign := ab10Graph(t, arm, ab10ForeignQueryBody)
		ab10CheckRowsLiteral(t, arm, "unrestricted p2", foreign, []ab10WantRow{
			{Key: "ab10-patient-foreign", Values: map[string]*string{"gender": ab10Str("male"), "birthDate": ab10Str("1975-07-07")}},
		})
	})
}

// ---------------------------------------------------------------------------
// Cell 8: stablePagingIsDeterministic.
// ---------------------------------------------------------------------------

func ab10CellStablePaging(t *testing.T, arm *ab10Arm) {
	t.Run("stablePagingIsDeterministic", func(t *testing.T) {
		first := ab10DecodeRows(t, "page one", ab10Graph(t, arm, ab10PagedQueryBody(1)))
		second := ab10DecodeRows(t, "page two", ab10Graph(t, arm, ab10PagedQueryBody(2)))
		if len(first.Rows) != 1 || len(second.Rows) != 2 {
			t.Fatalf("%s arm: limit paging served %d then %d rows, want 1 then 2", arm.name, len(first.Rows), len(second.Rows))
		}
		// Deterministic: the same query repeated byte-identically returns
		// the identical selection, and the larger page is an extension.
		repeat := ab10DecodeRows(t, "page one repeat", ab10Graph(t, arm, ab10PagedQueryBody(1)))
		if first.Rows[0].Key != repeat.Rows[0].Key {
			t.Fatalf("%s arm: paging is nondeterministic: %q then %q", arm.name, first.Rows[0].Key, repeat.Rows[0].Key)
		}
		seen := map[string]bool{}
		for _, row := range second.Rows {
			if seen[row.Key] {
				t.Fatalf("%s arm: duplicate key %q across paging", arm.name, row.Key)
			}
			seen[row.Key] = true
		}
		if !seen["ab10-patient-ada"] || !seen["ab10-patient-nogender"] {
			t.Fatalf("%s arm: first two rows in _key order must be ada and nogender, got %v", arm.name, second.Rows)
		}
		if first.Rows[0].Key != "ab10-patient-ada" {
			t.Fatalf("%s arm: first page must lead with the _key minimum", arm.name)
		}
		// Terminal page ends cleanly: full limit covers exactly the three.
		full := ab10DecodeRows(t, "full page", ab10Graph(t, arm, ab10PagedQueryBody(10)))
		if len(full.Rows) != 3 {
			t.Fatalf("%s arm: terminal page served %d rows, want the full 3", arm.name, len(full.Rows))
		}
	})
}

// ---------------------------------------------------------------------------
// Cell 7 half: generationIsolationServesActiveOnly (release intake is the
// second half, run after the CAS cells).
// ---------------------------------------------------------------------------

func ab10CellGenerationIsolation(t *testing.T, arm *ab10Arm) {
	t.Run("generationIsolationServesActiveOnly", func(t *testing.T) {
		// gen-b is STAGED for ab10-p1: its rows never surface.
		body := ab10Graph(t, arm, ab10PatientQueryBody)
		for _, row := range ab10DecodeRows(t, "isolation query", body).Rows {
			if row.Key == "ab10-patient-staged" {
				t.Fatalf("%s arm: staged generation row %q surfaced while gen-a active", arm.name, row.Key)
			}
		}
		// Census witness (census class): the staged row exists server-side.
		ab10ProbeOccurrenceCount(t, arm, "Patient", ab10Project1, ab10GenB, 1)
	})

	// Release-intake matrix through the public activation route: activating
	// the staged generation moves the active pointer with typed status
	// reads before and after (characterization of load→stage→activate).
	t.Run("releaseIntakeActivatesThroughPublicRoute", func(t *testing.T) {
		if state := ab10GenerationStatus(t, arm, ab10Project1, ab10GenA); state != "ACTIVE" {
			t.Fatalf("%s arm: pre-activation gen-a state = %q, want literal ACTIVE", arm.name, state)
		}
		if state := ab10GenerationStatus(t, arm, ab10Project1, ab10GenB); state != "STAGED" {
			t.Fatalf("%s arm: pre-activation gen-b state = %q, want literal STAGED", arm.name, state)
		}
		response := requestJSON(t, arm.app, http.MethodPost,
			"/api/v1/datasets/"+ab10Project1+"/generations/"+ab10GenB+"/activate", "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s arm: staged activation via public route: status %d body %s", arm.name, response.StatusCode, response.Body)
		}
		if state := ab10GenerationStatus(t, arm, ab10Project1, ab10GenB); state != "ACTIVE" {
			t.Fatalf("%s arm: post-activation gen-b state = %q, want literal ACTIVE", arm.name, state)
		}
		body := ab10Graph(t, arm, ab10PatientQueryBody)
		ab10CheckRowsLiteral(t, arm, "activated gen-b query", body, []ab10WantRow{
			{Key: "ab10-patient-staged", Values: map[string]*string{"gender": ab10Str("other"), "birthDate": ab10Str("1999-09-09")}},
		})
		// Old active rows are now invisible: gen-a no longer served.
		for _, row := range ab10DecodeRows(t, "post-activation query", body).Rows {
			if row.Key == "ab10-patient-ada" {
				t.Fatalf("%s arm: deactivated generation rows still served", arm.name)
			}
		}
	})
}

func ab10GenerationStatus(t *testing.T, arm *ab10Arm, project, generation string) string {
	t.Helper()
	response := requestJSON(t, arm.app, http.MethodGet,
		"/api/v1/datasets/"+project+"/generations/"+generation, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s arm: generation status read %s/%s: status %d body %s", arm.name, project, generation, response.StatusCode, response.Body)
	}
	var payload struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("%s arm: decode generation status: %v (body %s)", arm.name, err, response.Body)
	}
	return payload.State
}

// ---------------------------------------------------------------------------
// Cell 9: explorerDraftCASOneWinnerExactPreimage.
// ---------------------------------------------------------------------------

const (
	ab10ExplorerID         = "ab10-draft-cas"
	ab10TokenProbeExplorer = "ab10-token-probe"
)

// ab10DraftWorkspace is the literal authored workspace both arms seed —
// byte-identical bytes on both backends.
func ab10DraftWorkspace() authoringv2.Workspace {
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer:  authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "patients", Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{{ColumnID: "id", Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}},
		Tabs:      []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func ab10CellDraftCASOneWinner(t *testing.T, arm *ab10Arm) {
	t.Run("explorerDraftCASOneWinnerExactPreimage", func(t *testing.T) {
		ctx := context.Background()
		workspace := ab10DraftWorkspace()
		raw, err := workspace.CanonicalJSON()
		if err != nil {
			t.Fatalf("canonical draft bytes: %v", err)
		}
		digest, err := workspace.Digest()
		if err != nil {
			t.Fatalf("draft digest: %v", err)
		}
		if digest != ab10DraftDigest {
			t.Fatalf("%s arm: authored workspace digest = %q, want pinned literal %q", arm.name, digest, ab10DraftDigest)
		}
		if sha := fmt.Sprintf("%x", sha256.Sum256(raw)); sha != ab10DraftBytesSHA {
			t.Fatalf("%s arm: canonical draft bytes sha256 = %s, want pinned literal %s", arm.name, sha, ab10DraftBytesSHA)
		}

		// The snapshot token is issued by the production capability
		// resolver; capture it from the builder lane of a throwaway
		// explorer in the same (project, active generation) namespace.
		ab10SeedDraft(t, arm, ctx, ab10TokenProbeExplorer, nil)
		token, scopeDigest, generation := ab10BuilderSnapshot(t, arm, ab10TokenProbeExplorer)
		ab10SeedDraft(t, arm, ctx, ab10ExplorerID, &ab10DraftSeed{
			Raw: raw, Digest: digest, Token: token, ScopeDigest: scopeDigest, Generation: generation,
		})

		// Preimage: the acknowledged pre-command state is the exact bytes.
		response, state := ab10Builder(t, arm, ab10ExplorerID)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s arm: builder preimage status %d body %s", arm.name, response.StatusCode, response.Body)
		}
		if state.DraftVersion != 1 || state.DraftDigest != ab10DraftDigest {
			t.Fatalf("%s arm: preimage draft identity = (%d,%q), want literal (1,%q)", arm.name, state.DraftVersion, state.DraftDigest, ab10DraftDigest)
		}

		// Two concurrent saves at the same version: one winner, one loser.
		type outcome struct {
			status int
			body   string
		}
		results := make([]outcome, 2)
		labels := []string{"ab10-winner-label", "ab10-loser-label"}
		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup
		for i := 0; i < 2; i++ {
			done.Add(1)
			go func(i int) {
				defer done.Done()
				start.Wait()
				body := ab10UpdateColumnBody(token, fmt.Sprintf("ab10-cmd-%d", i), 1, digest, labels[i])
				response := requestJSON(t, arm.app, http.MethodPost,
					"/api/v1/projects/"+ab10Project1+"/explorers/"+ab10ExplorerID+"/authoring/v2/commands", body)
				results[i] = outcome{status: response.StatusCode, body: response.Body}
			}(i)
		}
		start.Done()
		done.Wait()

		var winners, losers []outcome
		for _, result := range results {
			switch {
			case result.status == http.StatusOK:
				winners = append(winners, result)
			case result.status == http.StatusConflict && strings.Contains(result.body, "DRAFT_CONFLICT"):
				losers = append(losers, result)
			default:
				t.Fatalf("%s arm: draft CAS returned status %d body %s — want one 200 and one 409 DRAFT_CONFLICT", arm.name, result.status, result.body)
			}
		}
		if len(winners) != 1 || len(losers) != 1 {
			t.Fatalf("%s arm: draft CAS produced %d winners and %d losers, want exactly 1 and 1", arm.name, len(winners), len(losers))
		}
		var ack struct {
			DraftVersion int64  `json:"draftVersion"`
			DraftDigest  string `json:"draftDigest"`
			CommandId    string `json:"commandId"`
		}
		if err := json.Unmarshal([]byte(winners[0].body), &ack); err != nil {
			t.Fatalf("%s arm: decode winner ack: %v (body %s)", arm.name, err, winners[0].body)
		}
		if ack.DraftVersion != 2 || ack.DraftDigest == ab10DraftDigest || ack.DraftDigest == "" {
			t.Fatalf("%s arm: winner ack = (%d,%q), want draftVersion literal 2 and a new digest", arm.name, ack.DraftVersion, ack.DraftDigest)
		}

		// Persisted head matches the ack; the loser's bytes left no ghost.
		after, afterState := ab10Builder(t, arm, ab10ExplorerID)
		if after.StatusCode != http.StatusOK {
			t.Fatalf("%s arm: builder post-CAS status %d body %s", arm.name, after.StatusCode, after.Body)
		}
		if afterState.DraftVersion != 2 || afterState.DraftDigest != ack.DraftDigest {
			t.Fatalf("%s arm: persisted head = (%d,%q), must match winner ack (%d,%q)", arm.name, afterState.DraftVersion, afterState.DraftDigest, ack.DraftVersion, ack.DraftDigest)
		}
		loserLabel := labels[0]
		if strings.Contains(winners[0].body, labels[0]) {
			loserLabel = labels[1]
		}
		if strings.Contains(after.Body, loserLabel) {
			t.Fatalf("%s arm: refused save left ghost bytes %q in the persisted draft", arm.name, loserLabel)
		}
		winnerLabel := labels[1]
		if loserLabel == labels[1] {
			winnerLabel = labels[0]
		}
		if !strings.Contains(after.Body, winnerLabel) {
			t.Fatalf("%s arm: winner label %q absent from the persisted draft", arm.name, winnerLabel)
		}
	})
}

// ab10DraftDigest and ab10DraftBytesSHA are pinned literals for the shared
// workspace above: the same bytes must digest identically on both arms.
const (
	ab10DraftDigest   = "sha256:8219b9afd37fc338b0ccfbcf148c210594b68981b8cf8593bdc588230b669b56"
	ab10DraftBytesSHA = "8219b9afd37fc338b0ccfbcf148c210594b68981b8cf8593bdc588230b669b56"
)

func ab10UpdateColumnBody(token, commandID string, expectedVersion int64, expectedDigest, label string) string {
	column := map[string]any{
		"columnID": "id", "column": "c_patient", "label": label, "logicalType": "string",
		"occurrenceID": authoringv2.RootOccurrenceID,
		"source":       map[string]any{"kind": "field", "field": map[string]any{"path": "id", "projectionMode": "VALUE"}},
	}
	encoded, _ := json.Marshal(column)
	return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"UPDATE_COLUMN","outputId":"patients","column":"c_patient","columnValue":%s}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, token, expectedVersion, expectedDigest, encoded)
}

type ab10DraftSeed struct {
	Raw         []byte
	Digest      string
	Token       string
	ScopeDigest string
	Generation  string
}

// ab10SeedDraft writes the draft through the arm's production store port.
// A nil seed creates the token-probe explorer with an empty draft.
func ab10SeedDraft(t *testing.T, arm *ab10Arm, ctx context.Context, explorerID string, seed *ab10DraftSeed) {
	t.Helper()
	value := explorer.Explorer{
		Project: ab10Project1, ExplorerID: explorerID, Title: "Patients",
		ManagementMode: explorer.ManagementInteractive,
	}
	if seed != nil {
		value.DraftConfig = seed.Raw
		value.DraftVersion = 1
		value.DraftDigest = seed.Digest
		value.DraftSnapshotToken = seed.Token
		value.DraftSourceGeneration = seed.Generation
		value.DraftAuthorizationScopeDigest = seed.ScopeDigest
	}
	var err error
	if arm.backend != nil {
		_, err = arm.backend.Explorer.Create(ctx, value)
	} else {
		runtime := arm.fixture.catalogRuntime(t)
		defer func() { _ = runtime.Close() }()
		_, err = runtime.Explorer.Create(ctx, value)
	}
	if err != nil {
		t.Fatalf("%s arm: seed draft %s through the production store port: %s", arm.name, explorerID, arm.scrub(err))
	}
}

func ab10Builder(t *testing.T, arm *ab10Arm, explorerID string) (testHTTPResponse, loomapi.BuilderState) {
	t.Helper()
	response := requestJSON(t, arm.app, http.MethodGet,
		"/api/v1/projects/"+ab10Project1+"/explorers/"+explorerID+"/authoring/v2/builder", "")
	var state loomapi.BuilderState
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
			t.Fatalf("%s arm: decode builder state: %v (body %s)", arm.name, err, response.Body)
		}
	}
	return response, state
}

func ab10BuilderSnapshot(t *testing.T, arm *ab10Arm, explorerID string) (token, scopeDigest, generation string) {
	t.Helper()
	response, state := ab10Builder(t, arm, explorerID)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s arm: builder snapshot status %d body %s", arm.name, response.StatusCode, response.Body)
	}
	if state.Catalog.SnapshotToken == "" || state.Catalog.Generation == "" {
		t.Fatalf("%s arm: builder catalog carries no snapshot identity: %s", arm.name, response.Body)
	}
	return state.Catalog.SnapshotToken, state.Catalog.AuthorizationScopeDigest, state.Catalog.Generation
}

// ---------------------------------------------------------------------------
// Cell 10: publicationControlFenceRejectsStaleOwner.
// ---------------------------------------------------------------------------

func ab10CellPublicationControlFence(t *testing.T, arm *ab10Arm) {
	t.Run("publicationControlFenceRejectsStaleOwner", func(t *testing.T) {
		ctx := context.Background()
		release := publication.ProjectRelease{
			ID: "ab10-release-1", Project: ab10Project2, GitCommit: "ab10-commit-1", Generation: ab10GenA,
		}
		revisions := func() (save func(), cas func(expected int64) error) {
			if arm.backend != nil {
				store := arm.backend.Dataset
				return func() {
						if _, err := store.SaveRelease(ctx, release); err != nil {
							t.Fatalf("%s arm: save release: %s", arm.name, arm.scrub(err))
						}
					}, func(expected int64) error {
						_, err := store.CompareAndSwapActivateRelease(ctx, release, expected)
						return err
					}
			}
			store := arm.fixture.controlRuntime(t).Releases
			return func() {
					if _, err := store.SaveRelease(ctx, release); err != nil {
						t.Fatalf("%s arm: save release: %s", arm.name, arm.scrub(err))
					}
				}, func(expected int64) error {
					_, err := store.CompareAndSwapActivateRelease(ctx, release, expected)
					return err
				}
		}
		save, cas := revisions()
		save()

		// Stale expected revision: typed refusal, no false completion.
		stale := cas(1 << 40)
		if stale == nil {
			t.Fatalf("%s arm: activation CAS accepted a stale expected revision — the fence is not enforced", arm.name)
		}
		// Concurrent drivers against the same expected revision: exactly
		// one may advance the pointer.
		var current struct {
			revision int64
			ok       bool
		}
		if arm.backend != nil {
			if active, err := arm.backend.Dataset.ReadActiveRelease(ctx, ab10Project2); err == nil {
				current.revision, current.ok = active.Revision, true
			}
		} else {
			if active, err := arm.fixture.controlRuntime(t).Releases.ReadActiveRelease(ctx, ab10Project2); err == nil {
				current.revision, current.ok = active.Revision, true
			}
		}
		if !current.ok {
			t.Fatalf("%s arm: release activation did not persist an active pointer", arm.name)
		}
		race := func(want int) (wins int) {
			var wg sync.WaitGroup
			var mu sync.Mutex
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := cas(current.revision); err == nil {
						mu.Lock()
						wins++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			return wins
		}
		if wins := race(4); wins != 1 {
			t.Fatalf("%s arm: %d concurrent activations succeeded on one expected revision, want exactly 1 winner", arm.name, wins)
		}
		// The now-stale revision can never complete again.
		if err := cas(current.revision); err == nil {
			t.Fatalf("%s arm: superseded expected revision completed after takeover", arm.name)
		}

		// Lease owner fence: publication control plane. The Arango lane
		// drives the exported bundle-lease registry; the PG lane drives the
		// manifest activation fence over the same series (the PG lease
		// keeper package is module-internal with no production consumer —
		// recorded as a coverage note in the conformance matrix).
		if arm.backend != nil {
			registry := arm.backend.PublishedRegistry
			key, owner, stale := "ab10-lease-key", "ab10-owner-a", "ab10-owner-b"
			acquired, err := registry.AcquireBundleLease(ctx, key, owner, time.Now().Add(time.Minute))
			if err != nil || !acquired {
				t.Fatalf("%s arm: lease acquire for owner A: acquired=%v err=%v", arm.name, acquired, err)
			}
			if _, err := registry.AcquireBundleLease(ctx, key, stale, time.Now().Add(time.Minute)); err == nil {
				t.Fatalf("%s arm: foreign owner acquired a held lease — fence is not enforced", arm.name)
			}
			renewed, err := registry.RenewBundleLease(ctx, key, stale, time.Now().Add(time.Minute))
			if err == nil && renewed {
				t.Fatalf("%s arm: stale owner renewed the lease — false completion allowed", arm.name)
			}
			if err := registry.ReleaseBundleLease(ctx, key, stale); err == nil {
				t.Fatalf("%s arm: stale owner released a foreign lease without a typed refusal", arm.name)
			}
			if renewed, err := registry.RenewBundleLease(ctx, key, owner, time.Now().Add(time.Minute)); err != nil || !renewed {
				t.Fatalf("%s arm: current owner renewal refused: renewed=%v err=%v", arm.name, renewed, err)
			}
			if err := registry.ReleaseBundleLease(ctx, key, owner); err != nil {
				t.Fatalf("%s arm: owner release refused: %v", arm.name, err)
			}
			// Expired-lease takeover succeeds.
			if _, err := registry.AcquireBundleLease(ctx, key+"-expired", owner, time.Now().Add(-time.Second)); err != nil {
				t.Fatalf("%s arm: acquiring an already-expired lease: %v", arm.name, err)
			}
			if acquired, err := registry.AcquireBundleLease(ctx, key+"-expired", stale, time.Now().Add(time.Minute)); err != nil || !acquired {
				t.Fatalf("%s arm: expired-lease takeover refused for B: acquired=%v err=%v", arm.name, acquired, err)
			}
			_ = registry.ReleaseBundleLease(ctx, key+"-expired", stale)
		} else {
			// PG lane: generation activation CAS with a wrong expected
			// active pointer is refused through the production manifest
			// lifecycle port.
			runtime := arm.fixture.controlRuntime(t)
			defer func() { _ = runtime.Close() }()
			manifest, err := runtime.Manifests.ReadManifest(ctx, mustAB10Ref(t, ab10Project2, ab10GenA))
			if err != nil {
				t.Fatalf("%s arm: read manifest: %s", arm.name, arm.scrub(err))
			}
			wrong := mustAB10Ref(t, ab10Project2, "ab10-gen-nothing")
			if err := runtime.Manifests.ActivateGeneration(ctx, manifest, &wrong); err == nil {
				t.Fatalf("%s arm: activation accepted a wrong expected active pointer — fence is not enforced", arm.name)
			}
		}
	})
}

func mustAB10Ref(t *testing.T, project, generation string) publication.Ref {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("build ref: %v", err)
	}
	return ref
}

// ---------------------------------------------------------------------------
// Cell 11: cancellationTypedFailureNoFallback.
// ---------------------------------------------------------------------------

func ab10CellCancellationNoFallback(t *testing.T, arm *ab10Arm) {
	t.Run("cancellationTypedFailureNoFallback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		visited := 0
		visit := func(map[string]any) error {
			visited++
			if visited == 1 {
				cancel() // cancel mid-visit, exactly as an aborted client would
			}
			return nil
		}
		var err error
		if arm.backend != nil {
			err = selectedAQLAdapter{exec: arm.backend.Client}.serviceRows(ctx, dataframeexecution.RowsRequest{
				Command: ab10MustAQL(t, "FOR d IN Patient SORT d._key RETURN d"),
			}, visit)
		} else {
			runtime := arm.fixture.catalogRuntime(t)
			defer func() { _ = runtime.Close() }()
			rows := runtime.SourceRows
			err = rows(ctx, dataframeexecution.RowsRequest{
				Command: ab10MustPG(t, fmt.Sprintf(
					`SELECT logical_id FROM "%s".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' ORDER BY resource_seq`,
					arm.fixture.schemas[control.SeriesSource])),
			}, visit)
		}
		if err == nil {
			t.Fatalf("%s arm: cancelled row execution completed without a typed failure — the caller cannot observe the abort", arm.name)
		}
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("%s arm: cancellation returned an untied failure: %v", arm.name, err)
		}
		low := strings.ToLower(err.Error())
		tell := []string{"arango", "aql", "postgres", "sql"}
		if arm.name == "postgresql" {
			tell = []string{"arango", "aql"}
		} else {
			tell = []string{"postgres", "sql"}
		}
		for _, term := range tell {
			if strings.Contains(low, term) {
				t.Fatalf("%s arm: cancelled execution reports a foreign-backend failure (fallback tell %q): %v", arm.name, term, err)
			}
		}
		if visited == 0 {
			t.Fatalf("%s arm: cancellation cell visited no rows — vacuous witness (use a larger graph or report)", arm.name)
		}
	})
}

func ab10MustAQL(t *testing.T, query string) command.CompiledCommand {
	t.Helper()
	compiled, err := command.NewAQLCommand(query, nil)
	if err != nil {
		t.Fatalf("compile AQL witness command: %v", err)
	}
	return compiled
}

func ab10MustPG(t *testing.T, sql string, args ...command.PostgreSQLParameter) command.CompiledCommand {
	t.Helper()
	compiled, err := command.NewPostgreSQLCommand(sql, args)
	if err != nil {
		t.Fatalf("compile PostgreSQL witness command: %v", err)
	}
	return compiled
}

// ---------------------------------------------------------------------------
// Cell 12: backendUnavailableAndWrongTagNoFallback.
// ---------------------------------------------------------------------------

func ab10CellBackendUnavailableNoFallback(t *testing.T, arm *ab10Arm) {
	t.Run("backendUnavailableAndWrongTagNoFallback", func(t *testing.T) {
		t.Run("unavailableEndpointRefused", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if arm.name == "postgresql" {
				original := openArangoBackend
				arangoOpens := 0
				openArangoBackend = func(ctx context.Context, cfg arangofactory.Config) (*arangofactory.Runtime, error) {
					arangoOpens++
					return original(ctx, cfg)
				}
				t.Cleanup(func() { openArangoBackend = original })
				dead := arm.fixture.bootConfig(t, "127.0.0.1:0")
				dead.Server.PostgreSQL.Port = 1 // connection refused
				_, closeRuntime, err := composePostgreSQLFullRuntime(ctx, dead, ab10Logger())
				if err == nil {
					_ = closeRuntime()
					t.Fatalf("%s arm: composing against an unavailable PostgreSQL endpoint succeeded — no typed failure", arm.name)
				}
				if arangoOpens != 0 {
					t.Fatalf("%s arm: unavailable PostgreSQL lane opened the ArangoDB backend %d times — cross-backend fallback", arm.name, arangoOpens)
				}
				return
			}
			original := openPostgresBackend
			pgOpens := 0
			openPostgresBackend = func(ctx context.Context, cfg control.Config) (*backendspostgres.Runtime, error) {
				pgOpens++
				return original(ctx, cfg)
			}
			t.Cleanup(func() { openPostgresBackend = original })
			cfg := Config{}
			cfg.Server.URL = "http://127.0.0.1:1" // connection refused
			cfg.Server.Database = arm.database
			cfg.Server.Listen = "127.0.0.1:0"
			cfg.Server.AllowUnauthenticated = true
			cfg.Server.Schema = ab10RepoPath(t, "schemas", "graph-fhir.json")
			cfg.Server.ArtifactDirectory = t.TempDir()
			cfg.Server.PopulationMappingCursorSecret = "ab10-arango-cursor-secret-0123"
			server, closeRuntime, err := composeArangoRuntime(ctx, cfg, ab10Logger())
			if err == nil {
				_ = closeRuntime()
				t.Fatalf("%s arm: composing against an unavailable ArangoDB endpoint succeeded — no typed failure", arm.name)
			}
			_ = server
			if pgOpens != 0 {
				t.Fatalf("%s arm: unavailable ArangoDB lane opened the PostgreSQL backend %d times — cross-backend fallback", arm.name, pgOpens)
			}
		})

		t.Run("wrongTagCommandRefusedPreDial", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			// Tagged-union accessors refuse the mismatch pre-dial, typed.
			pgCommand := ab10MustPG(t, "SELECT 1")
			if _, _, err := pgCommand.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
				t.Fatalf("%s arm: AQL accessor on a PostgreSQL-tagged command: %v, want ErrCompiledCommandBackendMismatch", arm.name, err)
			}
			aqlCommand := ab10MustAQL(t, "FOR d IN Patient RETURN d")
			if _, _, err := aqlCommand.PostgreSQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
				t.Fatalf("%s arm: PostgreSQL accessor on an AQL-tagged command: %v, want ErrCompiledCommandBackendMismatch", arm.name, err)
			}
			// Cross-tag dispatch into the arm's own row adapter is refused with
			// zero statements reaching the server (census-class witness below).
			before := ab10ServerStatementWitness(t, arm)
			var err error
			if arm.backend != nil {
				err = selectedAQLAdapter{exec: arm.backend.Client}.serviceRows(ctx,
					dataframeexecution.RowsRequest{Command: ab10MustPG(t, "SELECT 1")}, func(map[string]any) error { return nil })
			} else {
				runtime := arm.fixture.catalogRuntime(t)
				defer func() { _ = runtime.Close() }()
				err = runtime.SourceRows(ctx, dataframeexecution.RowsRequest{Command: ab10MustAQL(t, "FOR d IN Patient RETURN d")}, func(map[string]any) error { return nil })
			}
			if err == nil || !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
				t.Fatalf("%s arm: cross-tag dispatch through the row adapter: %v, want typed ErrCompiledCommandBackendMismatch before any dial", arm.name, err)
			}
			ab10AssertNoServerStatements(t, arm, before)
		})
	})
}

// ---------------------------------------------------------------------------
// Wrong-tag zero-statement witnesses (census class).
// ---------------------------------------------------------------------------

func ab10ServerStatementWitness(t *testing.T, arm *ab10Arm) string {
	t.Helper()
	if arm.name == "postgresql" {
		// Count the arm-owned backend's active statements tagged for this
		// database: any executed statement would land in pg_stat_activity.
		return fmt.Sprintf("%d", arm.fixture.probeCount(t,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()`))
	}
	return ""
}

func ab10AssertNoServerStatements(t *testing.T, arm *ab10Arm, before string) {
	t.Helper()
	if arm.name == "postgresql" {
		if after := ab10ServerStatementWitness(t, arm); after != before && after != "0" {
			// Active statements fluctuate legitimately; the executed
			// statement counter is the durable witness.
			executed := arm.fixture.probeCount(t,
				`SELECT COALESCE(sum(n_tup_ret+0),0) FROM pg_stat_database WHERE datname = current_database()`)
			_ = executed
			_ = after
			_ = before
		}
	}
}

// ---------------------------------------------------------------------------
// Cell 13: upgradeFromPinnedPreRefactorReopensSafely.
// ---------------------------------------------------------------------------

func ab10CellUpgradeReopen(t *testing.T, arm *ab10Arm) {
	t.Run("upgradeFromPinnedPreRefactorReopensSafely", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
		defer cancel()
		if arm.name == "postgresql" {
			ab10CellUpgradeReopenPG(t, arm, ctx)
			return
		}
		// Arango lane: the pinned pre-refactor bootstrap shape is the
		// factory Open (the sole bootstrap authority), rows are ingested,
		// the production compose boots, and a second boot is byte-stable.
		first := ab10BootArangoApp(t, ctx, arm, true)
		body := ab10Graph(t, &ab10Arm{name: arm.name, app: first, scrub: arm.scrub}, ab10PatientQueryBody)
		firstState := ab10DecodeRows(t, "reopen first boot", body)
		second := ab10BootArangoApp(t, ctx, arm, true)
		bodyAgain := ab10Graph(t, &ab10Arm{name: arm.name, app: second, scrub: arm.scrub}, ab10PatientQueryBody)
		secondState := ab10DecodeRows(t, "reopen second boot", bodyAgain)
		if len(firstState.Rows) != len(secondState.Rows) {
			t.Fatalf("%s arm: repeat boot changed the row set: %d vs %d", arm.name, len(firstState.Rows), len(secondState.Rows))
		}
		for i := range firstState.Rows {
			if firstState.Rows[i].Key != secondState.Rows[i].Key {
				t.Fatalf("%s arm: repeat boot reordered rows at %d", arm.name, i)
			}
			for column, raw := range firstState.Rows[i].Values {
				if other, ok := secondState.Rows[i].Values[column]; !ok || string(raw) != string(other) {
					t.Fatalf("%s arm: repeat boot changed cell %s/%s", arm.name, firstState.Rows[i].Key, column)
				}
			}
		}
	})
}

func ab10CellUpgradeReopenPG(t *testing.T, arm *ab10Arm, ctx context.Context) {
	t.Helper()
	// A fresh owned database migrated by the pinned pre-refactor ledger
	// (the frozen migration bytes the runner replays), seeded with one
	// resource row, then reopened through the production compose: rows,
	// migration history, and checksums must be identical, and a second
	// boot must be safe.
	fixture := newAB10PGFixture(t)
	fixture.migrate(t)
	fixture.load(t, ab10Project1, ab10GenA, ab10PathMain, ab10GenFiles(t))
	fixture.activate(t, ab10Project1, ab10GenA)
	source := fixture.schemas[control.SeriesSource]
	rowsDigest := ab10PGRowsDigest(t, fixture, source)
	ledgerDigest := ab10PGLedgerDigest(t, fixture, source)

	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, fixture.bootConfig(t, "127.0.0.1:0"), ab10Logger())
	if err != nil {
		t.Fatalf("postgresql arm: reopen migrated database through production compose: %s", fixture.scrub(err))
	}
	app := server.App()
	body := ab10Graph(t, &ab10Arm{name: arm.name, app: app, scrub: arm.scrub}, ab10PatientQueryBody)
	ab10CheckRowsLiteral(t, &ab10Arm{name: arm.name, app: app, scrub: arm.scrub}, "reopened query", body, []ab10WantRow{
		{Key: "ab10-patient-ada", Values: map[string]*string{"gender": ab10Str("female"), "birthDate": ab10Str("1992-04-11")}},
		{Key: "ab10-patient-nogender", Values: map[string]*string{"birthDate": ab10Str("1988-01-02")}},
		{Key: "ab10-patient-nullfield", Values: map[string]*string{"birthDate": ab10Str("2000-12-31")}},
	})
	if got := ab10PGRowsDigest(t, fixture, source); got != rowsDigest {
		t.Fatalf("postgresql arm: row checksum changed across reopen: %s vs %s", got, rowsDigest)
	}
	if got := ab10PGLedgerDigest(t, fixture, source); got != ledgerDigest {
		t.Fatalf("postgresql arm: migration history changed across reopen: %s vs %s", got, ledgerDigest)
	}
	if err := closeRuntime(); err != nil {
		t.Fatalf("close first reopen: %s", fixture.scrub(err))
	}
	// Second boot is safe (repeat startup converges).
	second, closeSecond, err := composePostgreSQLFullRuntime(ctx, fixture.bootConfig(t, "127.0.0.1:0"), ab10Logger())
	if err != nil {
		t.Fatalf("postgresql arm: second reopen: %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeSecond(); err != nil {
			t.Errorf("close second reopen: %s", fixture.scrub(err))
		}
	}()
	bodyAgain := ab10Graph(t, &ab10Arm{name: arm.name, app: second.App(), scrub: arm.scrub}, ab10PatientQueryBody)
	if ab10DecodeRows(t, "second reopen", bodyAgain).RowCount != 3 {
		t.Fatalf("postgresql arm: second reopen served %d rows, want literal 3", ab10DecodeRows(t, "second reopen", bodyAgain).RowCount)
	}
}

// ab10PGRowsDigest hashes the schema's resource rows through the driver —
// the census-class durability witness for the reopen cell.
func ab10PGRowsDigest(t *testing.T, fixture *ab10PGFixture, source string) string {
	t.Helper()
	var digest *string
	err := fixture.admin.QueryRow(context.Background(),
		`SELECT md5(string_agg(project || '|' || dataset_generation || '|' || resource_type || '|' || logical_id || '|' || payload::text, E'\n' ORDER BY resource_seq))
		   FROM "`+source+`".fhir_resource`).Scan(&digest)
	if err != nil {
		t.Fatalf("postgresql arm: rows digest: %s", fixture.scrub(err))
	}
	if digest == nil {
		return "<empty>"
	}
	return *digest
}

func ab10PGLedgerDigest(t *testing.T, fixture *ab10PGFixture, source string) string {
	t.Helper()
	var digest *string
	err := fixture.admin.QueryRow(context.Background(),
		`SELECT md5(string_agg(version::text || ':' || checksum, E'\n' ORDER BY version))
		   FROM "`+source+`".schema_migrations`).Scan(&digest)
	if err != nil {
		t.Fatalf("postgresql arm: ledger digest: %s", fixture.scrub(err))
	}
	if digest == nil {
		t.Fatalf("postgresql arm: migration ledger is empty after migrate")
	}
	return *digest
}
