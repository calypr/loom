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

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

var pgrecip01v1NameRe = regexp.MustCompile(`^pgrecip01v1_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 recipe revision-chain integration tests")
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

var createdDatabases sync.Map

func snapshotCreatedDatabases() []string {
	var names []string
	createdDatabases.Range(func(k, _ any) bool {
		names = append(names, k.(string))
		return true
	})
	sort.Strings(names)
	return names
}

type chainFixture struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	dbName   string
	schema   string
	dsn      string
	registry *RevisionRegistry
	scrub    func(any) string
	fail     func(step string, err error)
}

func newChainFixture(t *testing.T) *chainFixture {
	t.Helper()
	raw := testPostgresURL(t)

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

	f := &chainFixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
	if _, err := MigrateSchema(ctx, iso, schema); err != nil {
		f.fail("migrate revision-chain history", err)
	}
	registry, err := New(iso, schema)
	if err != nil {
		f.fail("build revision registry", err)
	}
	f.registry = registry
	return f
}

func (f *chainFixture) revisionsTable() string {
	return pgx.Identifier{f.schema, RevisionsTable}.Sanitize()
}

func (f *chainFixture) headsTable() string {
	return pgx.Identifier{f.schema, HeadsTable}.Sanitize()
}

func (f *chainFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.fail("count", fmt.Errorf("%s: %w", query, err))
	}
	return n
}

func (f *chainFixture) mustText(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&s); err != nil {
		f.fail("witness text", fmt.Errorf("%s: %w", query, err))
	}
	return s
}

func (f *chainFixture) mustInt64(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.fail("witness int", fmt.Errorf("%s: %w", query, err))
	}
	return n
}

func (f *chainFixture) mustPgErrorCode(t *testing.T, step, query string, args ...any) string {
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

func bundleFor(name, translationVersion string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  translationVersion,
		Outputs: []recipe.Output{{
			Name:             "DocumentReference",
			RootResourceType: "DocumentReference",
			RowGrain:         "document_reference",
		}},
	}
}

func requireRevisionEqual(t *testing.T, want, got recipe.RecipeRevision, where string) {
	t.Helper()
	if got.Project != want.Project || got.Name != want.Name || got.Digest != want.Digest || got.Parent != want.Parent {
		t.Fatalf("%s: identity changed: got %+v want %+v", where, got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("%s: createdAt instant changed: got %s want %s", where, got.CreatedAt, want.CreatedAt)
	}
	if !reflect.DeepEqual(got.Bundle, want.Bundle) {
		t.Fatalf("%s: bundle changed: got %+v want %+v", where, got.Bundle, want.Bundle)
	}
}

func userFault(t *testing.T, err error, want dataframeerrors.ErrorCode) dataframeerrors.UserError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected typed %s, got nil", want)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("expected typed %s, got untyped %v", want, err)
	}
	if userErr.Code() != string(want) {
		t.Fatalf("code=%s want %s (err=%v)", userErr.Code(), want, err)
	}
	return userErr
}

func TestRevisionRegistryRoundTripAndLiteralRows(t *testing.T) {
	f := newChainFixture(t)
	bundle := bundleFor("files", "1")
	first, err := f.registry.Register(f.ctx, "project-a", bundle, "")
	f.fail("register first", err)

	wantDigest := mustCanonicalDigest(t, bundle)
	if first.Digest != wantDigest || first.Project != "project-a" || first.Name != "files" || first.Parent != "" {
		t.Fatalf("first revision = %+v, want project-a/files digest %s parent empty", first, wantDigest)
	}

	loaded, err := f.registry.Get(f.ctx, "project-a", "files", wantDigest)
	f.fail("get first", err)
	requireRevisionEqual(t, first, loaded, "round trip")

	list, err := f.registry.List(f.ctx, "project-a", "files")
	f.fail("list first", err)
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1", len(list))
	}
	requireRevisionEqual(t, first, list[0], "list")

	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()); n != 1 {
		t.Fatalf("revision rows = %d, want 1", n)
	}
	if got := f.mustText(t, "SELECT digest FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); got != wantDigest {
		t.Fatalf("stored digest = %q, want %q", got, wantDigest)
	}
	if got := f.mustText(t, "SELECT parent_digest FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); got != "" {
		t.Fatalf("stored parent_digest = %q, want the empty string (Go's missing parent)", got)
	}
	if present := f.mustText(t, "SELECT (payload ? 'parentDigest')::text FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); present != "false" {
		t.Fatalf("payload has parentDigest key = %s, want absent (missing parent)", present)
	}
	payload, err := json.Marshal(recipe.RecipeRevision{
		Project: "project-a", Name: "files", Digest: wantDigest, Parent: "",
		Bundle: mustCanonical(t, bundle), CreatedAt: first.CreatedAt,
	})
	f.fail("marshal expected payload", err)
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()+
		" WHERE project = $1 AND name = $2 AND digest = $3 AND payload = $4::jsonb",
		"project-a", "files", wantDigest, string(payload)); n != 1 {
		t.Fatal("stored payload is not exactly the marshaled revision")
	}
	if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); epoch != 1 {
		t.Fatalf("head epoch after first register = %d, want 1", epoch)
	}
	if got := f.mustText(t, "SELECT head_digest FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); got != wantDigest {
		t.Fatalf("head digest = %q, want %q", got, wantDigest)
	}
}

func TestRevisionRegistryChainsChildrenAndBumpsEpoch(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register first", err)
	second, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "2"), first.Digest)
	f.fail("register child", err)
	if second.Parent != first.Digest || second.Digest == first.Digest {
		t.Fatalf("child = %+v, want parent %s and a distinct digest", second, first.Digest)
	}
	if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); epoch != 2 {
		t.Fatalf("head epoch = %d, want 2 (one committed extension after the first)", epoch)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); n != 2 {
		t.Fatalf("revision rows = %d, want 2", n)
	}
	list, err := f.registry.List(f.ctx, "project-a", "files")
	f.fail("list chain", err)
	if len(list) != 2 || list[0].Digest != second.Digest || list[1].Digest != first.Digest {
		t.Fatalf("list order wrong: %+v", list)
	}
}

func mustCanonical(t *testing.T, bundle recipe.Bundle) recipe.Bundle {
	t.Helper()
	canonical, err := bundle.CanonicalBundle()
	if err != nil {
		t.Fatalf("canonicalize bundle: %v", err)
	}
	return canonical
}

func mustCanonicalDigest(t *testing.T, bundle recipe.Bundle) string {
	t.Helper()
	digest, err := mustCanonical(t, bundle).Digest()
	if err != nil {
		t.Fatalf("canonical digest: %v", err)
	}
	return digest
}

func TestRevisionRegistryRejectsParentForFirstRevision(t *testing.T) {
	f := newChainFixture(t)
	_, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), strings.Repeat("a", 64))
	userFault(t, err, dataframeerrors.CodeInvalidRequest)

	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()); n != 0 {
		t.Fatalf("revision rows = %d, want 0 (no dangling first revision)", n)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.headsTable()); n != 0 {
		t.Fatalf("head rows = %d, want 0 (placeholder rolled back)", n)
	}
}

func TestRevisionRegistryStaleAndEmptyParentAreTypedStaleCursor(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register first", err)

	_, err = f.registry.Register(f.ctx, "project-a", bundleFor("files", "2"), "")
	userErr := userFault(t, err, dataframeerrors.CodeStaleCursor)
	if userErr.Retryable() {
		t.Fatal("stale parent error must not be retryable")
	}
	if _, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "3"), strings.Repeat("f", 64)); userFault(t, err, dataframeerrors.CodeStaleCursor) == nil {
		t.Fatal("wrong parent not typed")
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); n != 1 {
		t.Fatalf("revision rows after rejections = %d, want 1", n)
	}
	epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files")
	if epoch != 1 {
		t.Fatalf("head epoch after rejections = %d, want 1", epoch)
	}
	if got := f.mustText(t, "SELECT head_digest FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); got != first.Digest {
		t.Fatalf("head digest after rejections = %q, want the untouched %q", got, first.Digest)
	}
}

func TestRevisionRegistryValidatesIdentityBeforeStore(t *testing.T) {
	f := newChainFixture(t)
	if _, err := f.registry.Register(f.ctx, "project-a", bundleFor("", "1"), ""); userFault(t, err, dataframeerrors.CodeInvalidRequest) == nil {
		t.Fatal("missing recipe name not typed")
	}
	if _, err := f.registry.Register(f.ctx, "  ", bundleFor("files", "1"), ""); userFault(t, err, dataframeerrors.CodeInvalidRequest) == nil {
		t.Fatal("missing project not typed")
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()); n != 0 {
		t.Fatalf("rows after invalid requests = %d, want 0", n)
	}
}

func TestRevisionRegistryStoreFaultsStayUntyped(t *testing.T) {
	f := newChainFixture(t)
	bareSchema := f.schema + "_bare"
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+bareSchema+`"`); err != nil {
		f.fail("create bare schema", err)
	}
	registry, err := New(f.iso, bareSchema)
	f.fail("build bare registry", err)
	_, err = registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	if err == nil {
		t.Fatal("register against unmigrated schema succeeded")
	}
	if _, typed := dataframeerrors.AsUserError(err); typed {
		t.Fatalf("store fault must stay untyped, got %#v", err)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("expected undefined-table store fault, got %v", err)
	}
}

func TestRevisionRegistryHeadRetryIsIdempotent(t *testing.T) {
	f := newChainFixture(t)
	bundle := bundleFor("files", "1")
	first, err := f.registry.Register(f.ctx, "project-a", bundle, "")
	f.fail("register first", err)

	retry, err := f.registry.Register(f.ctx, "project-a", bundle, first.Digest)
	f.fail("retry head content", err)
	requireRevisionEqual(t, first, retry, "head retry")

	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()); n != 1 {
		t.Fatalf("revision rows after head retry = %d, want 1", n)
	}
	if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); epoch != 1 {
		t.Fatalf("head epoch after idempotent retry = %d, want 1 (pure retries do not bump)", epoch)
	}
}

func TestRevisionRegistryDeepDuplicateKeepsOriginalRow(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register first", err)
	second, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "2"), first.Digest)
	f.fail("register second", err)

	time.Sleep(2 * time.Millisecond)
	relinked, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), second.Digest)
	f.fail("relink first content", err)
	if relinked.Digest != first.Digest || relinked.Parent != second.Digest {
		t.Fatalf("relinked revision = %+v, want digest %s parent %s", relinked, first.Digest, second.Digest)
	}
	if !relinked.CreatedAt.After(first.CreatedAt) {
		t.Fatalf("relink did not restamp the row: %s vs original %s", relinked.CreatedAt, first.CreatedAt)
	}
	if !reflect.DeepEqual(relinked.Bundle, first.Bundle) {
		t.Fatal("relink changed the digest-pinned bundle content")
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()); n != 2 {
		t.Fatalf("revision rows = %d, want 2 (relink reuses the stored row)", n)
	}
	if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()); epoch != 3 {
		t.Fatalf("head epoch = %d, want 3", epoch)
	}
	if got := f.mustText(t, "SELECT head_digest FROM "+f.headsTable()); got != first.Digest {
		t.Fatalf("head digest = %q, want the relinked %q", got, first.Digest)
	}
}

func runTwoRegisters(t *testing.T, f *chainFixture, a, b func() (recipe.RecipeRevision, error)) (recipe.RecipeRevision, error, recipe.RecipeRevision, error) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var ra, rb recipe.RecipeRevision
	var ea, eb error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		ra, ea = a()
	}()
	go func() {
		defer wg.Done()
		<-start
		rb, eb = b()
	}()
	close(start)
	wg.Wait()
	return ra, ea, rb, eb
}

func TestRevisionConcurrentChildrenOfOneParentHaveOneWinner(t *testing.T) {
	f := newChainFixture(t)
	for iteration := 0; iteration < 6; iteration++ {
		name := fmt.Sprintf("files-race-%d", iteration)
		head, err := f.registry.Register(f.ctx, "project-a", bundleFor(name, "1"), "")
		f.fail("register race head", err)

		childA := bundleFor(name, "a")
		childB := bundleFor(name, "b")
		revA, errA, revB, errB := runTwoRegisters(t, f,
			func() (recipe.RecipeRevision, error) {
				return f.registry.Register(f.ctx, "project-a", childA, head.Digest)
			},
			func() (recipe.RecipeRevision, error) {
				return f.registry.Register(f.ctx, "project-a", childB, head.Digest)
			})

		winners := 0
		losers := 0
		for _, pair := range []struct {
			rev recipe.RecipeRevision
			err error
		}{{revA, errA}, {revB, errB}} {
			switch {
			case pair.err == nil:
				winners++
			default:
				losers++
				userErr := userFault(t, pair.err, dataframeerrors.CodeStaleCursor)
				if userErr.Retryable() {
					t.Fatal("concurrent loser must receive a non-retryable typed stale-parent conflict")
				}
			}
		}
		if winners != 1 || losers != 1 {
			t.Fatalf("iteration %d: winners=%d losers=%d (errA=%v errB=%v), want exactly one winner",
				iteration, winners, losers, errA, errB)
		}

		if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", name); n != 2 {
			t.Fatalf("iteration %d: revision rows = %d, want 2 (loser left no orphan)", iteration, n)
		}
		if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", name); epoch != 2 {
			t.Fatalf("iteration %d: head epoch = %d, want 2", iteration, epoch)
		}
		var winner recipe.RecipeRevision
		if errA == nil {
			winner = revA
		} else {
			winner = revB
		}
		if got := f.mustText(t, "SELECT head_digest FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", name); got != winner.Digest {
			t.Fatalf("iteration %d: head digest = %q, want winner %q", iteration, got, winner.Digest)
		}
	}
}

func TestRevisionConcurrentFirstRegistrationsHaveOneWinner(t *testing.T) {
	f := newChainFixture(t)
	name := "files-first-race"
	_, errA, _, errB := runTwoRegisters(t, f,
		func() (recipe.RecipeRevision, error) {
			return f.registry.Register(f.ctx, "project-a", bundleFor(name, "a"), "")
		},
		func() (recipe.RecipeRevision, error) {
			return f.registry.Register(f.ctx, "project-a", bundleFor(name, "b"), "")
		})

	winners, losers := 0, 0
	for _, err := range []error{errA, errB} {
		if err == nil {
			winners++
			continue
		}
		losers++
		userFault(t, err, dataframeerrors.CodeStaleCursor)
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("first-registration race: winners=%d losers=%d, want 1/1", winners, losers)
	}
	if n := f.count(t, "SELECT count(*) FROM "+f.revisionsTable()+" WHERE project = $1 AND name = $2", "project-a", name); n != 1 {
		t.Fatalf("revision rows = %d, want 1", n)
	}
	if epoch := f.mustInt64(t, "SELECT epoch FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", name); epoch != 1 {
		t.Fatalf("head epoch = %d, want 1", epoch)
	}
}

func TestRevisionGetAndListAreProjectAndNameScoped(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register", err)

	if _, err := f.registry.Get(f.ctx, "project-b", "files", first.Digest); !errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		t.Fatalf("cross-project Get err = %v, want ErrRecipeRevisionNotFound", err)
	}
	if _, err := f.registry.Get(f.ctx, "project-a", "other", first.Digest); !errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		t.Fatalf("cross-name Get err = %v, want ErrRecipeRevisionNotFound", err)
	}
	if _, err := f.registry.Get(f.ctx, "project-a", "files", strings.Repeat("0", 64)); !errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		t.Fatalf("missing digest err = %v, want ErrRecipeRevisionNotFound", err)
	}
	empty, err := f.registry.List(f.ctx, "project-z", "nothing")
	f.fail("list unknown", err)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unknown-chain list = %#v, want non-nil empty slice", empty)
	}

	other, err := f.registry.Register(f.ctx, "project-b", bundleFor("files", "1"), "")
	f.fail("register in project-b", err)
	if other.Digest != first.Digest {
		t.Fatal("identical bundle digests disagree across projects")
	}
	if _, err := f.registry.Get(f.ctx, "project-b", "files", other.Digest); err != nil {
		f.fail("get cross-project twin", err)
	}
}

func TestRevisionListIsDeterministicWithLiteralTies(t *testing.T) {
	f := newChainFixture(t)
	stamp := func(offset time.Duration) time.Time {
		return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC).Add(offset)
	}
	type lit struct {
		digest  string
		created time.Time
	}
	lits := []lit{
		{strings.Repeat("a", 64), stamp(0)},
		{strings.Repeat("b", 64), stamp(0)},
		{strings.Repeat("c", 64), stamp(0)},
		{strings.Repeat("d", 64), stamp(time.Hour)},
	}
	for _, l := range lits {
		payload := fmt.Sprintf(
			`{"project":"literal-p","name":"literal-r","digest":%q,"bundle":{"recipeSchemaVersion":1,"name":"literal-r","translationVersion":"1","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient"}]},"createdAt":%q}`,
			l.digest, l.created.Format(time.RFC3339Nano))
		if _, err := f.iso.Exec(f.ctx,
			"INSERT INTO "+f.revisionsTable()+
				" (project, name, digest, parent_digest, payload, created_at)"+
				" VALUES ($1, $2, $3, $4, $5, $6)",
			"literal-p", "literal-r", l.digest, "", payload, l.created); err != nil {
			f.fail("insert literal revision", err)
		}
	}
	list, err := f.registry.List(f.ctx, "literal-p", "literal-r")
	f.fail("list literal", err)
	want := []string{
		strings.Repeat("d", 64),
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
	}
	if len(list) != len(want) {
		t.Fatalf("list length = %d, want %d", len(list), len(want))
	}
	for i, digest := range want {
		if list[i].Digest != digest {
			t.Fatalf("list[%d] = %s, want %s (created_at DESC, digest tie-break)", i, list[i].Digest, digest)
		}
	}
}

func TestRevisionThreeStatesAndHeadPlaceholderInvariant(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register", err)

	nullDigest := strings.Repeat("9", 64)
	payload := fmt.Sprintf(
		`{"project":"project-a","name":"files","digest":%q,"parentDigest":null,"bundle":{"recipeSchemaVersion":1,"name":"files","translationVersion":"1","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"document_reference"}]},"createdAt":"2026-02-03T04:05:06Z"}`,
		nullDigest)
	if _, err := f.iso.Exec(f.ctx,
		"INSERT INTO "+f.revisionsTable()+
			" (project, name, digest, parent_digest, payload, created_at)"+
			" VALUES ($1, $2, $3, $4, $5, $6)",
		"project-a", "files", nullDigest, "", payload, time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)); err != nil {
		f.fail("insert json-null parent row", err)
	}
	if has := f.mustText(t, "SELECT (payload ? 'parentDigest')::text FROM "+f.revisionsTable()+" WHERE digest = $1", nullDigest); has != "true" {
		t.Fatalf("json-null parentDigest key presence = %s, want true (null is not absent)", has)
	}
	if isNull := f.mustText(t, "SELECT COALESCE(jsonb_typeof(payload -> 'parentDigest'), '<absent>') FROM "+
		f.revisionsTable()+" WHERE digest = $1", nullDigest); isNull != "null" {
		t.Fatalf("json-null parentDigest typeof = %s, want null (JSON null preserved, distinct from absent)", isNull)
	}
	if absent := f.mustText(t, "SELECT COALESCE(jsonb_typeof(payload -> 'parentDigest'), '<absent>') FROM "+
		f.revisionsTable()+" WHERE digest = $1", first.Digest); absent != "<absent>" {
		t.Fatalf("adapter-written row parentDigest typeof = %s, want absent (missing, not null)", absent)
	}

	if _, err := f.iso.Exec(f.ctx,
		"INSERT INTO "+f.headsTable()+" (project, name, head_digest, epoch, updated_at)"+
			" VALUES ($1, $2, NULL, 0, $3)", "placeholder-p", "placeholder-r", time.Now().UTC()); err != nil {
		f.fail("insert placeholder head", err)
	}
	list, err := f.registry.List(f.ctx, "placeholder-p", "placeholder-r")
	f.fail("list placeholder chain", err)
	if len(list) != 0 {
		t.Fatalf("placeholder head leaked %d revisions", len(list))
	}
	if code := f.mustPgErrorCode(t, "digest set at epoch 0",
		"UPDATE "+f.headsTable()+" SET head_digest = $1 WHERE project = $2 AND name = $3",
		strings.Repeat("e", 64), "placeholder-p", "placeholder-r"); code != "23514" {
		t.Fatalf("digest-set-at-epoch-0 sqlstate = %s, want 23514", code)
	}
	if got := f.mustText(t, "SELECT head_digest FROM "+f.headsTable()+" WHERE project = $1 AND name = $2", "project-a", "files"); got != first.Digest {
		t.Fatalf("head digest = %q, want the registered %q before the clear attempt", got, first.Digest)
	}
	if code := f.mustPgErrorCode(t, "digest cleared at epoch 1",
		"UPDATE "+f.headsTable()+" SET head_digest = NULL WHERE project = $1 AND name = $2",
		"project-a", "files"); code != "23514" {
		t.Fatalf("digest-cleared-at-epoch-1 sqlstate = %s, want 23514", code)
	}

	if code := f.mustPgErrorCode(t, "garbage digest",
		"INSERT INTO "+f.revisionsTable()+
			" (project, name, digest, parent_digest, payload, created_at)"+
			" VALUES ('p', 'n', 'nope', '', '{}'::jsonb, now())"); code != "23514" {
		t.Fatalf("garbage digest sqlstate = %s, want 23514", code)
	}
	if code := f.mustPgErrorCode(t, "garbage parent",
		"INSERT INTO "+f.revisionsTable()+
			" (project, name, digest, parent_digest, payload, created_at)"+
			" VALUES ('p', 'n', $1, 'garbage', '{}'::jsonb, now())", strings.Repeat("0", 64)); code != "23514" {
		t.Fatalf("garbage parent sqlstate = %s, want 23514", code)
	}
	if code := f.mustPgErrorCode(t, "empty project",
		"INSERT INTO "+f.revisionsTable()+
			" (project, name, digest, parent_digest, payload, created_at)"+
			" VALUES ('', 'n', $1, '', '{}'::jsonb, now())", strings.Repeat("0", 64)); code != "23514" {
		t.Fatalf("empty project sqlstate = %s, want 23514", code)
	}
}

func TestRevisionNewGuards(t *testing.T) {
	f := newChainFixture(t)
	if _, err := New(nil, f.schema); err == nil {
		t.Fatal("nil pool accepted")
	}
	for _, bad := range []string{"", "Bad-Schema", "1nvalid", `"; DROP`, strings.Repeat("a", 64)} {
		if _, err := New(f.iso, bad); err == nil {
			t.Fatalf("unsafe schema %q accepted", bad)
		}
	}
}

func TestRevisionMigrateSchemaHistoryLedgerAndRerun(t *testing.T) {
	f := newChainFixture(t)

	history, err := ChainMigrations()
	f.fail("load chain migrations", err)
	if len(history) != 2 || history[0].Version != 1 || history[0].Name != "ledger" ||
		history[1].Version != 3 || history[1].Name != "recipe_revision_chains" {
		t.Fatalf("embedded history = %+v, want {1 ledger, 3 recipe_revision_chains}", history)
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

	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register before rerun", err)
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
	if result.LatestVersion != 3 || len(result.Applied) != 2 {
		t.Fatalf("rerun result = %+v, want latest 3 with two applied rows", result)
	}
	for i, want := range history {
		if result.Applied[i].Version != want.Version || result.Applied[i].Checksum != want.Checksum {
			t.Fatalf("rerun applied[%d] = %+v, want %d/%s", i, result.Applied[i], want.Version, want.Checksum)
		}
	}
	loaded, err := f.registry.Get(f.ctx, "project-a", "files", first.Digest)
	f.fail("get after rerun", err)
	requireRevisionEqual(t, first, loaded, "after rerun")
}

func TestRevisionSchemaMixedHistoryFailsClosed(t *testing.T) {
	f := newChainFixture(t)

	edited := pgstore.Migration{Version: 1, Name: "ledger", SQL: "CREATE TABLE schema_migrations (version bigint);"}
	runner, err := pgstore.NewMigrationRunner(f.iso, f.schema, []pgstore.Migration{edited})
	f.fail("build edited-ledger runner", err)
	if _, err := runner.Migrate(f.ctx); !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
		t.Fatalf("edited ledger history err = %v, want ErrMigrationChecksumMismatch", err)
	}

	outOfOrder := pgstore.Migration{Version: 2, Name: "foreign_table", SQL: "CREATE TABLE IF NOT EXISTS foreign_table (id text);"}
	history, err := ChainMigrations()
	f.fail("load chain migrations", err)
	runner, err = pgstore.NewMigrationRunner(f.iso, f.schema, []pgstore.Migration{
		history[0], outOfOrder, history[1],
	})
	f.fail("build out-of-order runner", err)
	if _, err := runner.Migrate(f.ctx); !errors.Is(err, pgstore.ErrMigrationOutOfOrder) {
		t.Fatalf("pending version below the recorded maximum err = %v, want ErrMigrationOutOfOrder", err)
	}
	if exists := f.mustText(t, "SELECT (to_regclass($1) IS NOT NULL)::text",
		pgx.Identifier{f.schema, "foreign_table"}.Sanitize()); exists != "false" {
		t.Fatalf("refused history created its table: %s", exists)
	}
}

func TestRevisionStatePersistsBeyondOneConnection(t *testing.T) {
	f := newChainFixture(t)
	first, err := f.registry.Register(f.ctx, "project-a", bundleFor("files", "1"), "")
	f.fail("register", err)

	fresh, err := pgxpool.New(f.ctx, f.dsn)
	f.fail("open fresh pool", err)
	defer fresh.Close()
	second, err := New(fresh, f.schema)
	f.fail("build registry on fresh pool", err)
	loaded, err := second.Get(f.ctx, "project-a", "files", first.Digest)
	f.fail("get on fresh pool", err)
	requireRevisionEqual(t, first, loaded, "fresh pool")
}

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
		return nil
	}
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
	names := snapshotCreatedDatabases()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname = ANY($1)", names).Scan(&databases); err != nil {
		return errors.New(auditScrub("count databases: %v", err))
	}
	if databases != 0 {
		return errors.New(auditScrub("%d databases created by this binary still exist: %v", databases, names))
	}
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
