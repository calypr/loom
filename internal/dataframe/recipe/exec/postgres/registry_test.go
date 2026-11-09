package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
// Every acceptance assertion below runs against a real server: each test
// creates one uniquely owned pgrecip01v1_it_* database and one owned schema
// inside it, applies this package's embedded migrations through the real
// pg-store-02 runner, and asserts literal rows. Concurrency is exercised by
// real concurrent transactions -- goroutines on the pool and independent
// pgx sessions that hold locks across commit points -- never by mocks.
// The fence proofs watch pg_stat_activity to establish that a racing writer
// genuinely waits on the recipe-row lock instead of merely finishing fast.
// ---------------------------------------------------------------------------

var pgrecip01v1NameRe = regexp.MustCompile(`^pgrecip01v1_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 recipe exec-registry integration tests")
	}
	return raw
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// createdDatabases records every isolated database name this test binary
// ever created. The zero-leftover audits count exactly these names: the
// pgrecip01v1_it_* namespace is shared with the sibling recipe/postgres
// test binary, and pg_database is cluster-wide, so counting the whole
// prefix would race live fixtures of the other binary whenever go test
// runs packages in parallel (the default). Auditing the names this binary
// owns is precise and parallel-safe; the service-database schema check
// stays namespace-wide because neither binary ever creates a schema in the
// service database, so it cannot see the other binary's isolated-database
// schemas.
var createdDatabases sync.Map // database name -> struct{}{}

func snapshotCreatedDatabases() []string {
	var names []string
	createdDatabases.Range(func(k, _ any) bool {
		names = append(names, k.(string))
		return true
	})
	sort.Strings(names)
	return names
}

type registryFixture struct {
	ctx      context.Context
	admin    *pgxpool.Pool // shared service database
	iso      *pgxpool.Pool // isolated database
	dbName   string
	schema   string
	dsn      string
	registry *Registry
	scrub    func(any) string
	fail     func(step string, err error)
}

// newRegistryFixture creates one isolated database (pgrecip01v1_it_*) plus
// one owned schema (pgrecip01v1_it_*) inside it, applies the exec-registry
// history through the real runner, and registers LIFO cleanups: drop schema,
// close iso pool, drop database, then the zero-leftover audit scoped to this
// namespace, then close the admin pool, then cancel the context. Only
// resources this fixture created are ever dropped.
func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	// Scrub every credential-bearing string that could appear in a failure
	// message: the raw service URL, the userinfo, the bare password, and the
	// rewritten isolated-database DSN appended below.
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
	fail := func(step string, err error) {
		if err == nil {
			return
		}
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgrecip01v1_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgrecip01v1NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	createdDatabases.Store(dbName, struct{}{})

	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		names := snapshotCreatedDatabases()
		var mineLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = ANY($1)`, names).Scan(&mineLeftovers); err != nil {
			t.Errorf("leftover audit: query owned database set: %s", scrub(err))
		}
		if mineLeftovers != 0 {
			t.Errorf("leftover audit: %d databases created by this binary remain: %v", mineLeftovers, names)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgrecip01v1_it_%'`).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d pgrecip01v1_it_* schemas remain in the service database", schemaLeftovers)
		}
	})

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	// pgconn lets a dbname query parameter outrank the URL path; drop any
	// dbname the service URL carries so isolation is provable.
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgrecip01v1_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgrecip01v1NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	f := &registryFixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
	if _, err := MigrateSchema(ctx, iso, schema); err != nil {
		f.fail("migrate exec-registry history", err)
	}
	registry, err := New(iso, schema)
	if err != nil {
		f.fail("build exec registry", err)
	}
	f.registry = registry
	return f
}

func (f *registryFixture) recipesTable() string {
	return pgx.Identifier{f.schema, RecipesTable}.Sanitize()
}

func (f *registryFixture) occurrencesTable() string {
	return pgx.Identifier{f.schema, OccurrencesTable}.Sanitize()
}

func (f *registryFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.fail("count", fmt.Errorf("%s: %w", query, err))
	}
	return n
}

func (f *registryFixture) mustText(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&s); err != nil {
		f.fail("witness text", fmt.Errorf("%s: %w", query, err))
	}
	return s
}

func (f *registryFixture) storedDigest(t *testing.T, name, version string) string {
	t.Helper()
	return f.mustText(t, "SELECT digest FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", name, version)
}

func (f *registryFixture) mustPgErrorCode(t *testing.T, step, query string, args ...any) string {
	t.Helper()
	_, err := f.iso.Exec(f.ctx, query, args...)
	if err == nil {
		t.Fatalf("%s: statement unexpectedly succeeded", step)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		f.fail(step, err)
	}
	return pgErr.Code
}

// waitLockedFor blocks until a backend is actively waiting on a Lock-class
// event whose query matches one of the given LIKE patterns, failing the test
// if the raced operation completes first or the deadline passes.
func (f *registryFixture) waitLockedFor(t *testing.T, done <-chan struct{}, patterns ...string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	ors := make([]string, 0, len(patterns))
	for range patterns {
		ors = append(ors, "query LIKE $"+fmt.Sprint(len(ors)+1))
	}
	query := "SELECT count(*) FROM pg_stat_activity" +
		" WHERE state = 'active' AND wait_event_type = 'Lock' AND (" + strings.Join(ors, " OR ") + ")"
	args := make([]any, 0, len(patterns))
	for _, p := range patterns {
		args = append(args, p)
	}
	for {
		var waiting int
		if err := f.admin.QueryRow(f.ctx, query, args...).Scan(&waiting); err != nil {
			f.fail("witness lock wait", err)
		}
		if waiting > 0 {
			return
		}
		select {
		case <-done:
			t.Fatal("the fenced operation completed before the competing writer committed -- the fence did not serialize")
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the fenced operation never blocked on the recipe row lock")
		}
	}
}

// ---------------------------------------------------------------------------
// Domain fixtures.
// ---------------------------------------------------------------------------

func canonicalEntry(t *testing.T, name, translationVersion, rowGrain string) exec.Entry {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  translationVersion,
		Outputs: []recipe.Output{{
			Name:             "DocumentReference",
			RootResourceType: "DocumentReference",
			RowGrain:         rowGrain,
		}},
	}
	canonical, err := bundle.CanonicalBundle()
	if err != nil {
		t.Fatalf("canonicalize bundle: %v", err)
	}
	digest, err := canonical.Digest()
	if err != nil {
		t.Fatalf("canonical digest: %v", err)
	}
	return exec.Entry{Bundle: canonical, Digest: digest}
}

// ---------------------------------------------------------------------------
// Registration round trip and literal rows.
// ---------------------------------------------------------------------------

func TestExecSaveLoadRoundTripAndLiteralRows(t *testing.T) {
	f := newRegistryFixture(t)
	first := canonicalEntry(t, "files", "1", "document_reference")
	second := canonicalEntry(t, "files", "2", "document_reference_v2")
	f.fail("save first", f.registry.SaveRecipe(f.ctx, first))
	f.fail("save second", f.registry.SaveRecipe(f.ctx, second))

	for _, entry := range []exec.Entry{first, second} {
		loaded, err := f.registry.LoadRecipeVersion(f.ctx, "files", entry.Bundle.TranslationVersion)
		f.fail("load version", err)
		if !reflect.DeepEqual(loaded, entry) {
			t.Fatalf("loaded entry = %+v, want %+v", loaded, entry)
		}
	}

	// Literal rows: identity columns, digest, and payload equality against
	// the exact bytes the adapter marshals (jsonb equality normalizes only
	// formatting; members and values must all match).
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 2 {
		t.Fatalf("registration rows = %d, want 2", n)
	}
	for _, entry := range []exec.Entry{first, second} {
		payload, err := json.Marshal(entry)
		f.fail("marshal expected payload", err)
		if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()+
			" WHERE recipe_name = $1 AND translation_version = $2 AND digest = $3 AND payload = $4::jsonb",
			entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest, string(payload)); n != 1 {
			t.Fatalf("stored row for %s is not the literal registration", entry.Bundle.TranslationVersion)
		}
	}

	// LoadRecipe returns the current registration: highest version in exact
	// byte order.
	loaded, err := f.registry.LoadRecipe(f.ctx, "files")
	f.fail("load current", err)
	if loaded.Digest != second.Digest {
		t.Fatalf("current digest = %s, want %s", loaded.Digest, second.Digest)
	}
}

func TestExecLoadRecipeCurrentUsesExactByteOrder(t *testing.T) {
	f := newRegistryFixture(t)
	// Versions 1, 2, and 10: byte order ranks "2" > "10" > "1", which is
	// exactly Arango's byte-order SORT and provably not the numeric- or
	// locale-aware order a default-collation index would give.
	digests := map[string]string{}
	for _, version := range []string{"1", "2", "10"} {
		entry := canonicalEntry(t, "files", version, "document_reference")
		f.fail("save "+version, f.registry.SaveRecipe(f.ctx, entry))
		digests[version] = entry.Digest
	}
	loaded, err := f.registry.LoadRecipe(f.ctx, "files")
	f.fail("load current", err)
	if loaded.Digest != digests["2"] {
		t.Fatalf("current digest = %s, want version 2's %s (COLLATE \"C\" byte order)", loaded.Digest, digests["2"])
	}
	witness := f.mustText(t, "SELECT translation_version FROM "+f.recipesTable()+
		" WHERE recipe_name = 'files' ORDER BY translation_version DESC LIMIT 1")
	if witness != "2" {
		t.Fatalf("byte-order witness = %q, want 2", witness)
	}
}

func TestExecLoadMissingReportsRecipeNotFound(t *testing.T) {
	f := newRegistryFixture(t)
	if _, err := f.registry.LoadRecipe(f.ctx, "never"); !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("LoadRecipe err = %v, want ErrRecipeNotFound", err)
	}
	f.fail("seed", f.registry.SaveRecipe(f.ctx, canonicalEntry(t, "files", "1", "document_reference")))
	if _, err := f.registry.LoadRecipeVersion(f.ctx, "files", "missing"); !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("LoadRecipeVersion err = %v, want ErrRecipeNotFound", err)
	}
	if _, err := f.registry.LoadRecipeVersion(f.ctx, "other", "1"); !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("cross-name LoadRecipeVersion err = %v, want ErrRecipeNotFound", err)
	}
}

func TestExecSaveSameDigestIsIdempotent(t *testing.T) {
	f := newRegistryFixture(t)
	entry := canonicalEntry(t, "files", "1", "document_reference")
	f.fail("first save", f.registry.SaveRecipe(f.ctx, entry))
	created := f.mustText(t, "SELECT created_at::text FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1")
	f.fail("retry save", f.registry.SaveRecipe(f.ctx, entry))
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 1 {
		t.Fatalf("rows after idempotent retry = %d, want 1", n)
	}
	after := f.mustText(t, "SELECT created_at::text FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1")
	if after != created {
		t.Fatalf("idempotent retry rewrote created_at: %s -> %s", created, after)
	}
}

func TestExecConcurrentDivergentFirstSaveHasOneWinner(t *testing.T) {
	f := newRegistryFixture(t)
	entryA := canonicalEntry(t, "files", "1", "grain-a")
	entryB := canonicalEntry(t, "files", "1", "grain-b")

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	for i, entry := range []exec.Entry{entryA, entryB} {
		go func(i int, entry exec.Entry) {
			defer wg.Done()
			<-start
			err := f.registry.SaveRecipe(f.ctx, entry)
			if i == 0 {
				errA = err
			} else {
				errB = err
			}
		}(i, entry)
	}
	close(start)
	wg.Wait()

	outcomes := []error{errA, errB}
	wins, conflicts := 0, 0
	for _, err := range outcomes {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrRecipeVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected save outcome: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("divergent first-save race: wins=%d conflicts=%d (errA=%v errB=%v), want 1/1",
			wins, conflicts, errA, errB)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 1 {
		t.Fatalf("rows after race = %d, want 1", n)
	}
	want := entryA.Digest
	if errA != nil {
		want = entryB.Digest
	}
	if got := f.storedDigest(t, "files", "1"); got != want {
		t.Fatalf("stored digest = %s, want the winner's %s", got, want)
	}
}

func TestExecConcurrentIdenticalFirstSaveIsIdempotent(t *testing.T) {
	f := newRegistryFixture(t)
	entry := canonicalEntry(t, "files", "1", "document_reference")
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = f.registry.SaveRecipe(f.ctx, entry)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent save %d failed: %v", i, err)
		}
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// Replacement: allowed before use, immutable after, typed when absent.
// ---------------------------------------------------------------------------

func TestExecReplaceBeforeUseSwapsContentAndKeepsCreatedAt(t *testing.T) {
	f := newRegistryFixture(t)
	original := canonicalEntry(t, "files", "1", "document_reference")
	replacement := canonicalEntry(t, "files", "1", "document_reference_changed")
	f.fail("save", f.registry.SaveRecipe(f.ctx, original))
	beforeCreated := f.mustText(t, "SELECT created_at::text FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1")

	// Leave the microsecond clock behind so the stamp move is provable.
	time.Sleep(2 * time.Millisecond)
	f.fail("replace", f.registry.ReplaceRecipeVersion(f.ctx, replacement))

	loaded, err := f.registry.LoadRecipeVersion(f.ctx, "files", "1")
	f.fail("load after replace", err)
	if !reflect.DeepEqual(loaded, replacement) {
		t.Fatalf("loaded = %+v, want replaced %+v", loaded, replacement)
	}
	created := f.mustText(t, "SELECT created_at::text FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1")
	if created != beforeCreated {
		t.Fatalf("replace moved created_at: %s -> %s", beforeCreated, created)
	}
	updated := f.mustText(t, "SELECT updated_at::text FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1")
	if updated == created {
		t.Fatal("replace did not advance updated_at")
	}
}

func TestExecReplaceMissingVersionReportsNotFound(t *testing.T) {
	f := newRegistryFixture(t)
	// Documented sharpening of the Arango adapter: absent is not used.
	err := f.registry.ReplaceRecipeVersion(f.ctx, canonicalEntry(t, "files", "1", "document_reference"))
	if !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("replace on absent version err = %v, want ErrRecipeNotFound", err)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 0 {
		t.Fatalf("replace manufactured a row: %d", n)
	}
}

func TestExecOccurrenceMakesVersionImmutable(t *testing.T) {
	f := newRegistryFixture(t)
	original := canonicalEntry(t, "files", "1", "document_reference")
	replacement := canonicalEntry(t, "files", "1", "changed")
	f.fail("save", f.registry.SaveRecipe(f.ctx, original))

	used, err := f.registry.RecipeVersionUsed(f.ctx, "files", "1")
	f.fail("probe unused", err)
	if used {
		t.Fatal("RecipeVersionUsed = true before an occurrence exists")
	}
	f.fail("record first use", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "exec-1", "worker-a"))

	used, err = f.registry.RecipeVersionUsed(f.ctx, "files", "1")
	f.fail("probe used", err)
	if !used {
		t.Fatal("RecipeVersionUsed = false after an occurrence exists")
	}
	err = f.registry.ReplaceRecipeVersion(f.ctx, replacement)
	if !errors.Is(err, exec.ErrRecipeVersionImmutable) {
		t.Fatalf("replace after use err = %v, want ErrRecipeVersionImmutable", err)
	}
	if got := f.storedDigest(t, "files", "1"); got != original.Digest {
		t.Fatalf("immutable version changed: %s != %s", got, original.Digest)
	}
}

func TestExecOccurrenceIdentityIsIdempotentAndOwnersCorrelate(t *testing.T) {
	f := newRegistryFixture(t)
	f.fail("save", f.registry.SaveRecipe(f.ctx, canonicalEntry(t, "files", "1", "document_reference")))
	f.fail("save other version", f.registry.SaveRecipe(f.ctx, canonicalEntry(t, "files", "2", "document_reference")))
	f.fail("save other recipe", f.registry.SaveRecipe(f.ctx, canonicalEntry(t, "other", "1", "document_reference")))

	f.fail("record o1", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "o1", "worker-a"))
	// Re-recording the same occurrence identity converges on the same row.
	f.fail("re-record o1", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "o1", "worker-a"))
	f.fail("record o2", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "o2", "worker-a"))
	f.fail("record o3", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "o3", "worker-b"))

	if n := f.count(t, "SELECT count(*) FROM "+f.occurrencesTable()); n != 3 {
		t.Fatalf("occurrence rows = %d, want 3 (o1 recorded once)", n)
	}
	// Repeated-owner correlation: owner_sequence is the per-(version, owner)
	// ordinal, strictly increasing in recording order.
	type seq struct {
		id  string
		seq int64
	}
	for _, want := range []seq{{"o1", 1}, {"o2", 2}, {"o3", 1}} {
		got := f.mustText(t, "SELECT owner_sequence::text FROM "+f.occurrencesTable()+
			" WHERE recipe_name = $1 AND translation_version = $2 AND occurrence_id = $3",
			"files", "1", want.id)
		if got != fmt.Sprint(want.seq) {
			t.Fatalf("occurrence %s owner_sequence = %s, want %d", want.id, got, want.seq)
		}
	}

	// Usage is exact scope equality, never a name-only or version-only guess.
	for _, probe := range []struct {
		name, version string
		want          bool
	}{
		{"files", "1", true},
		{"files", "2", false},
		{"other", "1", false},
	} {
		used, err := f.registry.RecipeVersionUsed(f.ctx, probe.name, probe.version)
		f.fail("probe", err)
		if used != probe.want {
			t.Fatalf("RecipeVersionUsed(%s,%s) = %v, want %v", probe.name, probe.version, used, probe.want)
		}
	}

	// Occurrences for a never-registered version are a wiring fault.
	if err := f.registry.RecordRecipeExecutionOccurrence(f.ctx, "ghost", "1", "o1", "worker-a"); !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("orphan occurrence err = %v, want ErrRecipeNotFound", err)
	}
	if err := f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", " ", "worker-a"); err == nil {
		t.Fatal("blank occurrence id accepted")
	}
}

// ---------------------------------------------------------------------------
// Domain-level acceptance through exec.PersistentRegistry.
// ---------------------------------------------------------------------------

func TestExecPersistentRegistryIdempotentRetryAndImmutableAfterUse(t *testing.T) {
	f := newRegistryFixture(t)
	registry := &exec.PersistentRegistry{Store: f.registry}

	bundleWithGrain := func(grain string) recipe.Bundle {
		return recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "files",
			TranslationVersion:  "1",
			Outputs: []recipe.Output{{
				Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: grain,
			}},
		}
	}
	bundle := bundleWithGrain("document_reference")
	first, err := registry.RegisterVersion(f.ctx, bundle)
	f.fail("register first", err)
	retry, err := registry.RegisterVersion(f.ctx, bundleWithGrain("document_reference"))
	f.fail("exact retry", err)
	if retry.Digest != first.Digest || !reflect.DeepEqual(retry.Bundle, first.Bundle) {
		t.Fatalf("exact retry = %+v, want %+v", retry, first)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.recipesTable()); n != 1 {
		t.Fatalf("rows after exact retry = %d, want 1", n)
	}

	// Same name+version, changed content, before any durable execution:
	// replaceable, exactly the Arango contract.
	changed := bundleWithGrain("changed")
	replaced, err := registry.RegisterVersion(f.ctx, changed)
	f.fail("register changed", err)
	if replaced.Digest == first.Digest {
		t.Fatal("pre-use replacement did not change the digest")
	}

	// A first durable execution makes the version immutable.
	f.fail("use it", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "exec-9", "worker-x"))
	third := bundleWithGrain("after-use")
	if _, err := registry.RegisterVersion(f.ctx, third); !errors.Is(err, exec.ErrRecipeVersionImmutable) {
		t.Fatalf("post-use RegisterVersion err = %v, want ErrRecipeVersionImmutable", err)
	}
	if got := f.storedDigest(t, "files", "1"); got != replaced.Digest {
		t.Fatalf("post-use attempt mutated the version: %s != %s", got, replaced.Digest)
	}
	// The exact-content retry still succeeds after use: immutability pins
	// the stored content, it does not punish re-registration of that content.
	again, err := registry.RegisterVersion(f.ctx, bundleWithGrain("changed"))
	f.fail("exact retry after use", err)
	if again.Digest != replaced.Digest {
		t.Fatalf("exact retry after use = %s, want %s", again.Digest, replaced.Digest)
	}
}

// ---------------------------------------------------------------------------
// Fences: first use versus replacement, proved with committing lock-holders.
// ---------------------------------------------------------------------------

// TestExecReplaceFencedAgainstCommittingFirstUse is the direct fence proof:
// an independent session begins a first-use recording (row lock + occurrence
// INSERT, uncommitted), and while that transaction is in flight
// ReplaceRecipeVersion blocks on the row lock. When the use commits, the
// replace must observe it and fail immutable -- it can never check-then-act
// its way past a use that committed mid-flight.
func TestExecReplaceFencedAgainstCommittingFirstUse(t *testing.T) {
	f := newRegistryFixture(t)
	original := canonicalEntry(t, "files", "1", "document_reference")
	replacement := canonicalEntry(t, "files", "1", "fenced-out")
	f.fail("save", f.registry.SaveRecipe(f.ctx, original))

	connA, err := pgx.Connect(f.ctx, f.dsn)
	f.fail("connect first-use session", err)
	defer connA.Close(context.Background())
	txConnA, err := connA.Begin(f.ctx)
	f.fail("begin first-use transaction", err)
	defer func() { _ = txConnA.Rollback(f.ctx) }()

	// The registry's own locking discipline, run in a real competing
	// session: lock the recipe row, then insert the occurrence, uncommitted.
	if _, err := txConnA.Exec(f.ctx, "SELECT true FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2 FOR UPDATE", "files", "1"); err != nil {
		f.fail("first-use row lock", err)
	}
	if _, err := txConnA.Exec(f.ctx, "INSERT INTO "+f.occurrencesTable()+
		" (recipe_name, translation_version, occurrence_id, owner_id, owner_sequence, recorded_at)"+
		" VALUES ($1, $2, $3, $4, 1, $5)",
		"files", "1", "race-use", "worker-race", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		f.fail("first-use insert", err)
	}

	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- f.registry.ReplaceRecipeVersion(f.ctx, replacement)
	}()

	f.waitLockedFor(t, done,
		"SELECT true FROM "+f.recipesTable()+"%FOR UPDATE",
		"UPDATE "+f.recipesTable()+"%")

	f.fail("commit racing first use", txConnA.Commit(f.ctx))
	err = <-result
	if !errors.Is(err, exec.ErrRecipeVersionImmutable) {
		t.Fatalf("replace racing a committing first use = %v, want ErrRecipeVersionImmutable", err)
	}
	if got := f.storedDigest(t, "files", "1"); got != original.Digest {
		t.Fatalf("fence leaked: digest moved to %s while a use committed underneath", got)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.occurrencesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2", "files", "1"); n != 1 {
		t.Fatalf("occurrence rows = %d, want 1", n)
	}
}

// TestExecRecordFencedBehindCommittingReplace is the symmetric proof: the
// occurrence recorder takes the same row lock, so it blocks behind an
// in-flight replacement and lands after it. The resulting order -- replace
// committed, then first use -- is the allowed one, and both writes survive.
func TestExecRecordFencedBehindCommittingReplace(t *testing.T) {
	f := newRegistryFixture(t)
	original := canonicalEntry(t, "files", "1", "document_reference")
	replacement := canonicalEntry(t, "files", "1", "replaced-first")
	f.fail("save", f.registry.SaveRecipe(f.ctx, original))

	connA, err := pgx.Connect(f.ctx, f.dsn)
	f.fail("connect replacing session", err)
	defer connA.Close(context.Background())
	txConnA, err := connA.Begin(f.ctx)
	f.fail("begin replacing transaction", err)
	defer func() { _ = txConnA.Rollback(f.ctx) }()

	if _, err := txConnA.Exec(f.ctx, "SELECT true FROM "+f.recipesTable()+
		" WHERE recipe_name = $1 AND translation_version = $2 FOR UPDATE", "files", "1"); err != nil {
		f.fail("replace row lock", err)
	}
	if _, err := txConnA.Exec(f.ctx, "UPDATE "+f.recipesTable()+
		" SET digest = $3, updated_at = $4 WHERE recipe_name = $1 AND translation_version = $2",
		"files", "1", replacement.Digest, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		f.fail("replace update", err)
	}

	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "late-use", "worker-late")
	}()

	f.waitLockedFor(t, done,
		"SELECT true FROM "+f.recipesTable()+"%FOR UPDATE")

	f.fail("commit racing replace", txConnA.Commit(f.ctx))
	f.fail("occurrence behind replace", <-result)
	if got := f.storedDigest(t, "files", "1"); got != replacement.Digest {
		t.Fatalf("digest = %s, want the committed replacement %s", got, replacement.Digest)
	}
	used, err := f.registry.RecipeVersionUsed(f.ctx, "files", "1")
	f.fail("probe after ordered use", err)
	if !used {
		t.Fatal("occurrence committed after the replace must still count as use")
	}
}

func TestExecConcurrentReplaceVersusOccurrenceNeverBothWinWrong(t *testing.T) {
	f := newRegistryFixture(t)
	for iteration := 0; iteration < 8; iteration++ {
		name := fmt.Sprintf("race-%d", iteration)
		original := canonicalEntry(t, name, "1", "document_reference")
		replacement := canonicalEntry(t, name, "1", "swapped")
		f.fail("save race entry", f.registry.SaveRecipe(f.ctx, original))

		start := make(chan struct{})
		var wg sync.WaitGroup
		var replaceErr, recordErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			replaceErr = f.registry.ReplaceRecipeVersion(f.ctx, replacement)
		}()
		go func() {
			defer wg.Done()
			<-start
			recordErr = f.registry.RecordRecipeExecutionOccurrence(f.ctx, name, "1", "race-occ", "worker-r")
		}()
		close(start)
		wg.Wait()

		f.fail("occurrence always records", recordErr)
		if n := f.count(t, "SELECT count(*) FROM "+f.occurrencesTable()+
			" WHERE recipe_name = $1 AND translation_version = $2", name, "1"); n != 1 {
			t.Fatalf("iteration %d: occurrence rows = %d, want 1", iteration, n)
		}
		switch {
		case replaceErr == nil:
			// The replace committed first; the use then landed on the
			// replaced version -- the allowed ordering.
			if got := f.storedDigest(t, name, "1"); got != replacement.Digest {
				t.Fatalf("iteration %d: successful replace stored %s, want %s", iteration, got, replacement.Digest)
			}
		case errors.Is(replaceErr, exec.ErrRecipeVersionImmutable):
			// The use committed first and the replace correctly refused.
			if got := f.storedDigest(t, name, "1"); got != original.Digest {
				t.Fatalf("iteration %d: immutable replace still moved the digest", iteration)
			}
		default:
			t.Fatalf("iteration %d: unexpected replace error %v", iteration, replaceErr)
		}
	}
}

// ---------------------------------------------------------------------------
// Guards, migrations, constraints, persistence.
// ---------------------------------------------------------------------------

func TestExecNewGuards(t *testing.T) {
	f := newRegistryFixture(t)
	if _, err := New(nil, f.schema); err == nil {
		t.Fatal("nil pool accepted")
	}
	for _, bad := range []string{"", "Bad-Schema", "1nvalid", `"; DROP`, strings.Repeat("a", 64)} {
		if _, err := New(f.iso, bad); err == nil {
			t.Fatalf("unsafe schema %q accepted", bad)
		}
	}
}

func TestExecStoreFaultsStayUntyped(t *testing.T) {
	f := newRegistryFixture(t)
	bareSchema := f.schema + "_bare"
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+bareSchema+`"`); err != nil {
		f.fail("create bare schema", err)
	}
	registry, err := New(f.iso, bareSchema)
	f.fail("build bare registry", err)
	if err := registry.SaveRecipe(f.ctx, canonicalEntry(t, "files", "1", "document_reference")); err == nil {
		t.Fatal("save against unmigrated schema succeeded")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
			t.Fatalf("expected undefined-table store fault, got %v", err)
		}
	}
}

func TestExecMigrateSchemaHistoryLedgerAndRerun(t *testing.T) {
	f := newRegistryFixture(t)

	history, err := RegistryMigrations()
	f.fail("load registry migrations", err)
	if len(history) != 2 || history[0].Version != 1 || history[0].Name != "ledger" ||
		history[1].Version != 2 || history[1].Name != "recipe_exec_registries" {
		t.Fatalf("embedded history = %+v, want {1 ledger, 2 recipe_exec_registries}", history)
	}

	rows, err := f.iso.Query(f.ctx,
		"SELECT version, name, checksum FROM "+
			pgx.Identifier{f.schema, pgstore.LedgerTable}.Sanitize()+" ORDER BY version")
	f.fail("read ledger", err)
	defer rows.Close()
	type rec struct {
		version  int64
		name     string
		checksum string
	}
	var got []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.version, &r.name, &r.checksum); err != nil {
			f.fail("scan ledger", err)
		}
		got = append(got, r)
	}
	f.fail("ledger rows", rows.Err())
	if len(got) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(got))
	}
	for i, want := range history {
		if got[i].version != want.Version || got[i].name != want.Name || got[i].checksum != want.Checksum {
			t.Fatalf("ledger[%d] = %+v, want {%d %s %s}", i, got[i], want.Version, want.Name, want.Checksum)
		}
	}

	entry := canonicalEntry(t, "files", "1", "document_reference")
	f.fail("save before rerun", f.registry.SaveRecipe(f.ctx, entry))

	// The production entry point on the readiness-checked pg-store-01 pool
	// re-applies the applied history as a provable no-op; state survives.
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	f.fail("open pg-store pool", err)
	defer client.Close()
	result, err := MigrateSchema(f.ctx, client, f.schema)
	f.fail("rerun migrate via production pool", err)
	if result.LatestVersion != 2 || len(result.Applied) != 2 {
		t.Fatalf("rerun result = %+v, want latest 2 with two applied rows", result)
	}
	loaded, err := f.registry.LoadRecipeVersion(f.ctx, "files", "1")
	f.fail("load after rerun", err)
	if loaded.Digest != entry.Digest {
		t.Fatalf("state did not survive rerun: %s != %s", loaded.Digest, entry.Digest)
	}
}

func TestExecSchemaConstraintsAndNULRejection(t *testing.T) {
	f := newRegistryFixture(t)
	valid := canonicalEntry(t, "files", "1", "document_reference")

	// Garbage digests and empty scopes die at the database boundary.
	if code := f.mustPgErrorCode(t, "garbage digest",
		"INSERT INTO "+f.recipesTable()+
			" (recipe_name, translation_version, digest, payload, created_at, updated_at)"+
			" VALUES ('p', '1', 'nope', '{}'::jsonb, now(), now())"); code != "23514" {
		t.Fatalf("garbage digest sqlstate = %s, want 23514", code)
	}
	if code := f.mustPgErrorCode(t, "empty version",
		"INSERT INTO "+f.recipesTable()+
			" (recipe_name, translation_version, digest, payload, created_at, updated_at)"+
			" VALUES ('p', '', $1, '{}'::jsonb, now(), now())", valid.Digest); code != "23514" {
		t.Fatalf("empty version sqlstate = %s, want 23514", code)
	}
	if code := f.mustPgErrorCode(t, "scalar payload",
		"INSERT INTO "+f.recipesTable()+
			" (recipe_name, translation_version, digest, payload, created_at, updated_at)"+
			" VALUES ('p', '1', $1, '5'::jsonb, now(), now())", valid.Digest); code != "23514" {
		t.Fatalf("scalar payload sqlstate = %s, want 23514", code)
	}
	// NUL bytes cannot masquerade as text scope values.
	_, err := f.iso.Exec(f.ctx, "INSERT INTO "+f.recipesTable()+
		" (recipe_name, translation_version, digest, payload, created_at, updated_at)"+
		" VALUES ($1, '1', $2, '{}'::jsonb, now(), now())", "bad\x00name", valid.Digest)
	if err == nil {
		t.Fatal("NUL-bearing recipe name accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "22") {
		t.Fatalf("NUL-bearing name err class = %v, want 22xxx", err)
	}
	if code := f.mustPgErrorCode(t, "owner sequence zero",
		"INSERT INTO "+f.occurrencesTable()+
			" (recipe_name, translation_version, occurrence_id, owner_id, owner_sequence, recorded_at)"+
			" VALUES ('p', '1', 'o1', 'w', 0, now())"); code != "23514" {
		t.Fatalf("owner_sequence 0 sqlstate = %s, want 23514", code)
	}
}

func TestExecStatePersistsBeyondOneConnection(t *testing.T) {
	f := newRegistryFixture(t)
	entry := canonicalEntry(t, "files", "1", "document_reference")
	f.fail("save", f.registry.SaveRecipe(f.ctx, entry))
	f.fail("record", f.registry.RecordRecipeExecutionOccurrence(f.ctx, "files", "1", "o1", "worker-a"))

	fresh, err := pgxpool.New(f.ctx, f.dsn)
	f.fail("open fresh pool", err)
	defer fresh.Close()
	second, err := New(fresh, f.schema)
	f.fail("build registry on fresh pool", err)
	loaded, err := second.LoadRecipeVersion(f.ctx, "files", "1")
	f.fail("load on fresh pool", err)
	if loaded.Digest != entry.Digest {
		t.Fatalf("fresh pool digest = %s, want %s", loaded.Digest, entry.Digest)
	}
	used, err := second.RecipeVersionUsed(f.ctx, "files", "1")
	f.fail("probe on fresh pool", err)
	if !used {
		t.Fatal("occurrence did not survive to the fresh pool")
	}
}

// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	code := m.Run()
	if err := auditZeroLeftovers(); err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit failed: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func auditZeroLeftovers() error {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return nil
	}
	// Scrub credential-bearing fragments before any audit error reaches
	// stderr; the fixtures scrub the same strings on failure paths.
	auditScrub := func(format string, args ...any) string {
		message := fmt.Sprintf(format, args...)
		secrets := []string{raw}
		if u, err := url.Parse(raw); err == nil && u.User != nil {
			secrets = append(secrets, u.User.String())
			if pw, ok := u.User.Password(); ok && pw != "" {
				secrets = append(secrets, pw)
			}
		}
		for _, secret := range secrets {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "<redacted>")
			}
		}
		return message
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		return errors.New(auditScrub("connect: %v", err))
	}
	defer admin.Close()
	// Audit the exact databases this binary created; the shared
	// pgrecip01v1_it_* prefix belongs to two binaries under parallel
	// go test, so a cluster-wide prefix count would race live sibling
	// fixtures. See createdDatabases.
	names := snapshotCreatedDatabases()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname = ANY($1)", names).Scan(&databases); err != nil {
		return errors.New(auditScrub("count databases: %v", err))
	}
	if databases != 0 {
		return errors.New(auditScrub("%d databases created by this binary still exist: %v", databases, names))
	}
	// Neither binary creates schemas in the service database (owned schemas
	// live inside each isolated database and die with it), so a service-db
	// namespace scan is race-free and catches accidental service-db writes.
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgrecip01v1_it_%'").Scan(&schemas); err != nil {
		return errors.New(auditScrub("count schemas: %v", err))
	}
	if schemas != 0 {
		return errors.New(auditScrub("%d pgrecip01v1_it_* schemas still exist in the service database", schemas))
	}
	return nil
}
