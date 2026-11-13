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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	datasetpostgres "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

var pubauthNameRe = regexp.MustCompile(`^verify_pubauth_[0-9]+_[0-9a-f]+(?:_[a-z]+)?$`)

var (
	pubauthBaselineOnce    sync.Once
	pubauthDBBaseline      atomic.Int64
	pubauthLive            atomic.Int64
	pubauthSchemaBaseline  atomic.Int64
	pubauthBaselineFailure atomic.Value
)

func pubauthCaptureBaseline(admin *pgxpool.Pool) {
	pubauthBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var mine, schemas int64
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_pubauth\_%' ESCAPE '\'`).Scan(&mine); err != nil {
			pubauthBaselineFailure.Store(fmt.Errorf("baseline: query owned database namespace: %w", err))
			return
		}
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_pubauth\_%' ESCAPE '\'`).Scan(&schemas); err != nil {
			pubauthBaselineFailure.Store(fmt.Errorf("baseline: query owned schema namespace: %w", err))
			return
		}
		pubauthDBBaseline.Store(mine)
		pubauthSchemaBaseline.Store(schemas)
	})
	if stored := pubauthBaselineFailure.Load(); stored != nil {
		panic(stored.(error))
	}
}

func pubauthTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 publish-authoring integration tests")
	}
	return raw
}

func pubauthRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type pubAuthFixture struct {
	ctx         context.Context
	admin       *pgxpool.Pool
	iso         *pgxpool.Pool
	dbName      string
	ctlSchema   string
	lanesSchema string
	dsn         string
	scrub       func(any) string
	fail        func(step string, err error)
}

func newPGPubAuthFixture(t *testing.T) *pubAuthFixture {
	t.Helper()
	raw := pubauthTestPostgresURL(t)

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
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	pubauthCaptureBaseline(admin)
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("verify_pubauth_%d_%s", time.Now().UnixNano(), pubauthRandomHex(t))
	if !pubauthNameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceDatabases int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_pubauth\_%' ESCAPE '\'`).Scan(&namespaceDatabases); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if want := pubauthDBBaseline.Load() + pubauthLive.Load(); int64(namespaceDatabases) != want {
			t.Errorf("leftover audit: %d verify_pubauth_* databases remain, baseline+live is %d",
				namespaceDatabases, want)
		}
		var namespaceSchemas int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_pubauth\_%' ESCAPE '\'`).Scan(&namespaceSchemas); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if int64(namespaceSchemas) != pubauthSchemaBaseline.Load() {
			t.Errorf("leftover audit: %d verify_pubauth_* schemas remain in the service database, baseline is %d",
				namespaceSchemas, pubauthSchemaBaseline.Load())
		}
	})

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
		pubauthLive.Add(-1)
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pubauthLive.Add(1)
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

	base := fmt.Sprintf("verify_pubauth_%d_%s", time.Now().UnixNano(), pubauthRandomHex(t))
	ctlSchema := base + "_ctl"
	lanesSchema := base + "_lanes"
	for _, schema := range []string{ctlSchema, lanesSchema} {
		if !pubauthNameRe.MatchString(schema) {
			t.Fatalf("owned schema name %q does not match the required pattern", schema)
		}
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			fail("create owned schema "+schema, err)
		}
	}
	t.Cleanup(func() {
		for _, schema := range []string{lanesSchema, ctlSchema} {
			if _, err := iso.Exec(context.WithoutCancel(ctx),
				`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
				t.Errorf("drop owned schema %s: %s", schema, scrub(err))
			}
		}
	})

	return &pubAuthFixture{
		ctx: ctx, admin: admin, iso: iso, dbName: dbName,
		ctlSchema: ctlSchema, lanesSchema: lanesSchema,
		dsn: dsn, scrub: scrub, fail: fail,
	}
}

type pubauthMigrationPin struct {
	version  int64
	name     string
	checksum string
}

var (
	pubauthControlPins = []pubauthMigrationPin{
		{4, "capability_artifacts", "bec3e49f897b28ef71d7d69a5f7c71bb11d13b7c631ee26724de36a89d639222"},
		{7, "interpretation_library", "5e308b21a3f567b3066c46458606dd361a9c90646db54ee71618902a544a9ba2"},
		{8, "selection_revisions", "562a61ef33786cf674f1c87552e2dc8e70691b34116b7f415d5f81960ed8d65d"},
	}
	pubauthLanePins = []pubauthMigrationPin{
		{1, "ledger", "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
		{3, "dataset_generation_manifest", "06c614680a9c1cd4ba2ef0ac0471053bcb3d8e471c36d3852e70682887a72f5d"},
		{8, "project_release", "1d581ec39731958dd0f79cdab18e92861a380007d3e85d6de4940800378480b1"},
	}
)

func pubauthVerifyPins(t *testing.T, label string, history []pgstore.Migration, pins []pubauthMigrationPin) {
	t.Helper()
	byVersion := make(map[int64]pgstore.Migration, len(history))
	for _, m := range history {
		byVersion[m.Version] = m
	}
	for _, pin := range pins {
		m, ok := byVersion[pin.version]
		if !ok {
			t.Fatalf("%s owner history lacks version %d (%s): the owning ticket's migration vanished", label, pin.version, pin.name)
		}
		if m.Name != pin.name || m.Checksum != pin.checksum {
			t.Fatalf("%s owner history version %d = {Name:%q Checksum:%q}, want the byte-frozen merge pin {Name:%q Checksum:%q}: the owning ticket's migration drifted",
				label, pin.version, m.Name, m.Checksum, pin.name, pin.checksum)
		}
	}
}

func (f *pubAuthFixture) migrate(t *testing.T) {
	t.Helper()
	ctlHistory, err := OwnerMigrations()
	if err != nil {
		f.fail("explorer owner migrations", err)
	}
	pubauthVerifyPins(t, "explorer control", ctlHistory, pubauthControlPins)
	lanesHistory, err := datasetpostgres.OwnerMigrations()
	if err != nil {
		f.fail("dataset owner migrations", err)
	}
	pubauthVerifyPins(t, "dataset lane", lanesHistory, pubauthLanePins)

	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open migration pool", err)
	}
	defer client.Close()

	ctl := f.migrateHistory(t, client, f.ctlSchema, ctlHistory, 11, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	lanes := f.migrateHistory(t, client, f.lanesSchema, lanesHistory, 8, []int64{1, 3, 8})
	if ctl.LatestVersion != 11 || lanes.LatestVersion != 8 {
		t.Fatalf("ledger tops = %d/%d, want 11/8", ctl.LatestVersion, lanes.LatestVersion)
	}
}

func (f *pubAuthFixture) migrateHistory(t *testing.T, client *pgstore.Client, schema string,
	history []pgstore.Migration, wantTop int64, wantVersions []int64,
) pgstore.MigrateResult {
	t.Helper()
	runner, err := pgstore.NewMigrationRunner(client, schema, history)
	if err != nil {
		f.fail("new migration runner for "+schema, err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate schema "+schema, err)
	}
	again, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("re-migrate schema "+schema, err)
	}
	if len(result.Applied) != len(wantVersions) || len(again.Applied) != len(wantVersions) {
		t.Fatalf("schema %s applied %d/%d migrations, want %d", schema, len(result.Applied), len(again.Applied), len(wantVersions))
	}
	if result.LatestVersion != wantTop || again.LatestVersion != wantTop {
		t.Fatalf("schema %s latest version = %d/%d, want %d", schema, result.LatestVersion, again.LatestVersion, wantTop)
	}
	for i, want := range wantVersions {
		if result.Applied[i].Version != want || again.Applied[i].Version != want {
			t.Fatalf("schema %s ledger position %d = %d/%d, want %d",
				schema, i, result.Applied[i].Version, again.Applied[i].Version, want)
		}
	}
	return result
}

func (f *pubAuthFixture) rerunMigration(t *testing.T, version int64) {
	t.Helper()
	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("load migrations for rerun", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			conn, err := f.iso.Acquire(f.ctx)
			if err != nil {
				f.fail("acquire connection for migration rerun", err)
			}
			defer conn.Release()
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				f.fail("begin migration rerun transaction", err)
			}
			if _, err := tx.Exec(f.ctx,
				"SET LOCAL search_path TO "+pgx.Identifier{f.ctlSchema}.Sanitize()); err != nil {
				tx.Rollback(f.ctx)
				f.fail("set search_path for migration rerun", err)
			}
			if _, err := tx.Exec(f.ctx, m.SQL); err != nil {
				tx.Rollback(f.ctx)
				f.fail(fmt.Sprintf("rerun migration %d/%s", m.Version, m.Name), err)
			}
			if err := tx.Commit(f.ctx); err != nil {
				f.fail("commit migration rerun", err)
			}
			return
		}
	}
	t.Fatalf("control-plane migration version %d is absent from the embedded history", version)
}

func (f *pubAuthFixture) newStore(t *testing.T) *Store {
	t.Helper()
	client := f.newPool(t, 4, 0)
	store, err := New(client, f.ctlSchema)
	if err != nil {
		f.fail("build Explorer control-plane store", err)
	}
	return store
}

func (f *pubAuthFixture) newDedicatedStore(t *testing.T) (*Store, int32) {
	t.Helper()
	client := f.newPool(t, 1, 1)
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire dedicated control-plane connection", err)
	}
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated control-plane backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated control-plane pool reported backend pid 0")
	}
	store, err := New(client, f.ctlSchema)
	if err != nil {
		f.fail("build dedicated control-plane store", err)
	}
	return store, pid
}

func (f *pubAuthFixture) newPublishStore(t *testing.T) *PublishStore {
	t.Helper()
	client := f.newPool(t, 4, 0)
	ps, err := NewPublishStore(client, f.ctlSchema, f.lanesSchema)
	if err != nil {
		f.fail("build publish store", err)
	}
	return ps
}

func (f *pubAuthFixture) newDedicatedPublishStore(t *testing.T) (*PublishStore, int32) {
	t.Helper()
	client := f.newPool(t, 1, 1)
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire dedicated publish connection", err)
	}
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated publish backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated publish pool reported backend pid 0")
	}
	ps, err := NewPublishStore(client, f.ctlSchema, f.lanesSchema)
	if err != nil {
		f.fail("build dedicated publish store", err)
	}
	return ps, pid
}

func (f *pubAuthFixture) newDedicatedPool(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(f.dsn)
	if err != nil {
		f.fail("parse dedicated raw pool config", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, poolConfig)
	if err != nil {
		f.fail("open dedicated raw pool", err)
	}
	t.Cleanup(pool.Close)
	var pid int32
	if err := pool.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated raw backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated raw pool reported backend pid 0")
	}
	return pool, pid
}

func (f *pubAuthFixture) newPool(t *testing.T, maxConns, minConns int32) *pgstore.Client {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN: f.dsn, MaxConns: maxConns, MinConns: minConns, AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open pg-store pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (f *pubAuthFixture) newGenerationStore(t *testing.T) *datasetpostgres.Store {
	t.Helper()
	client := f.newPool(t, 2, 0)
	gen, err := datasetpostgres.New(client, f.lanesSchema)
	if err != nil {
		f.fail("build generation store", err)
	}
	return gen
}

func (f *pubAuthFixture) newReleaseStore(t *testing.T) *datasetpostgres.ReleaseStore {
	t.Helper()
	client := f.newPool(t, 2, 0)
	rel, err := datasetpostgres.NewReleaseStore(client, f.lanesSchema)
	if err != nil {
		f.fail("build release store", err)
	}
	return rel
}

func requireDistinctBackendsPubAuth(t *testing.T, pidA, pidB int32, labelA, labelB string) {
	t.Helper()
	if pidA == pidB {
		t.Fatalf("%s and %s share backend pid %d, want two real PG connections", labelA, labelB, pidA)
	}
}

func (f *pubAuthFixture) ctlTable(table string) string {
	return sanitizeTableRef(f.ctlSchema, table)
}

func (f *pubAuthFixture) lanesTable(table string) string {
	return sanitizeTableRef(f.lanesSchema, table)
}

func (f *pubAuthFixture) snapshot(t *testing.T, table string, mask ...string) string {
	t.Helper()
	return f.snapshotWhere(t, table, "", nil, mask...)
}

func (f *pubAuthFixture) snapshotWhere(t *testing.T, table, where string, args []any, mask ...string) string {
	t.Helper()
	expr := "to_jsonb(t)"
	for _, column := range mask {
		expr += fmt.Sprintf(" - '%s'", column)
	}
	query := "SELECT COALESCE(string_agg(row_text, E'\\x1e' ORDER BY row_text), '<none>') FROM (SELECT (" +
		expr + ")::text AS row_text FROM " + table + " t"
	if where != "" {
		query += " WHERE " + where
	}
	query += ") s"
	var out string
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&out); err != nil {
		f.fail("snapshot table "+table, err)
	}
	return out
}

func (f *pubAuthFixture) countRows(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	query := "SELECT count(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	var count int
	if err := f.iso.QueryRow(f.ctx, query, args...).Scan(&count); err != nil {
		f.fail("count rows in "+table, err)
	}
	return count
}

func (f *pubAuthFixture) execRaw(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.iso.Exec(f.ctx, sql, args...); err != nil {
		f.fail("direct exec: "+sql, err)
	}
}

func (f *pubAuthFixture) locksHeldBy(t *testing.T, pool *pgxpool.Pool, pid int32) map[string]bool {
	t.Helper()
	rows, err := pool.Query(f.ctx, `
SELECT c.relname
FROM pg_locks l
JOIN pg_class c ON c.oid = l.relation
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE l.pid = $1 AND l.relation IS NOT NULL AND n.nspname = ANY($2::text[])`,
		pid, []string{f.ctlSchema, f.lanesSchema})
	if err != nil {
		f.fail("read pg_locks for pid "+fmt.Sprint(pid), err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			f.fail("scan pg_locks row", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		f.fail("read pg_locks rows", err)
	}
	return out
}

func requirePgErrPubAuth(t *testing.T, err error, sqlstate, step string) *pgconn.PgError {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
	return pgErr
}

var (
	pubauthCreatedAt  = time.Date(2026, 4, 5, 6, 7, 8, 987654000, time.UTC)
	pubauthVerifiedAt = time.Date(2026, 4, 5, 6, 7, 9, 123456000, time.UTC)
)

const pubauthGeneration = "gen_pubauth_01"

const pubauthSnapshotSHA256 = "2be6ca3efe274cf87ad45c17a516bc91f154e64de9e557516b973f0d22314341"

func pubauthOwner(project, explorerID string, mode explorer.ManagementMode) explorer.Explorer {
	return explorer.Explorer{
		Project:        project,
		ExplorerID:     explorerID,
		Title:          "pubauth " + explorerID,
		ManagementMode: mode,
		DraftVersion:   1,
		DraftDigest:    "sha256:draft-" + explorerID,
		UpdatedBy:      "author@calypr",
		UpdatedAt:      pubauthCreatedAt,
	}
}

const (
	cfgPubauthAlpha    = `{"title": "PubAuth", "filters": [{"column": "day", "op": "eq", "value": "2026-01-01"}], "limit": 25}`
	cfgPubauthBeta     = `{"a": 1, "m": "keep", "z": [true, false, null]}`
	cfgPubauthContract = `{"outputs":[{"outputId":"output","columns":[],"lossless":true,"mlReady":true}]}`
)

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func newPubauthBundle(name string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "pubauth",
		Outputs: []recipe.Output{{
			Name: "output", RootResourceType: "Patient", RowGrain: "resource",
		}},
	}
}

func pubauthReceipt(t *testing.T, project, explorerID, tag string) explorer.CompilationReceipt {
	t.Helper()
	bundle := newPubauthBundle("pubauth-" + tag)
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:     explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion:  explorer.CurrentCompilerContractVersion,
		Project:                  project,
		ExplorerID:               explorerID,
		IntentDigest:             "sha256:intent-" + tag,
		SnapshotToken:            "snapshot-pubauth-" + tag,
		AuthorizationScopeDigest: "scope-pubauth",
		CapabilitySchemaDigest:   "schema-pubauth",
		ShapeDigest:              "shape-pubauth",
		SourceGeneration:         pubauthGeneration,
		RecipeDigest:             "sha256:recipe-" + tag,
		ResolvedSchemaDigest:     "sha256:resolved-" + tag,
		NormalizedBundle:         jsonRaw(cfgPubauthAlpha),
		Bundle:                   bundle,
		PublicOutputContract:     jsonRaw(`{"outputs":[{"outputId":"output","columns":[],"lossless":true,"mlReady":true}]}`),
		OutputColumnProvenance:   map[string]map[string]string{"output": {"__loom_row_id": "EXPLICIT"}},
		RequestID:                "req_" + tag,
		CreatedAt:                pubauthCreatedAt,
	}
	var err error
	if receipt.ResolvedRecipeDigest, err = bundle.Digest(); err != nil {
		t.Fatalf("bundle digest: %v", err)
	}
	if receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(receipt.PublicOutputContract); err != nil {
		t.Fatalf("output contract digest: %v", err)
	}
	if receipt.CompilationKey, err = explorer.CompilationKey(receipt); err != nil {
		t.Fatalf("compilation key: %v", err)
	}
	if receipt.ID, err = explorer.ReceiptID(receipt); err != nil {
		t.Fatalf("receipt id: %v", err)
	}
	return receipt
}

func pubauthManualReceipt(t *testing.T, project, explorerID, tag, id string) explorer.CompilationReceipt {
	t.Helper()
	receipt := pubauthReceipt(t, project, explorerID, tag)
	receipt.ReceiptFormatVersion = 0
	receipt.CompilerContractVersion = ""
	receipt.CompilationKey = ""
	receipt.ID = id
	return receipt
}

func pubauthRevision(id, project, explorerID, receiptID, tag string) explorer.Revision {
	return explorer.Revision{
		ID:                   id,
		Project:              project,
		ExplorerID:           explorerID,
		Config:               jsonRaw(cfgPubauthAlpha),
		AuthoringBundle:      jsonRaw(cfgPubauthBeta),
		IntentDigest:         "sha256:pubauth-intent-" + tag,
		CompilationReceiptID: receiptID,
		PublicOutputContract: jsonRaw(cfgPubauthContract),
		Recipe:               newPubauthBundle("pubauth-" + tag),
		RecipeDigest:         "sha256:pubauth-recipe-" + tag,
		ResolvedSchemaDigest: "sha256:pubauth-schema-" + tag,
		SourceGeneration:     pubauthGeneration,
		Materializations: []explorer.Materialization{{
			OutputID: "output", Output: "output", MaterializationID: "mat_pubauth_1",
			Fingerprint: "fp-pubauth-1",
		}},
		EmittedColumns: []explorer.EmittedColumn{{
			EmissionID: "emit_1", OutputID: "output", PublicColumn: "day",
			LogicalType: "date", Shape: "scalar", Filterable: true,
		}},
		Dataset: explorer.DatasetMetadata{
			Generation:   pubauthGeneration,
			SchemaDigest: "sha256:pubauth-dataset",
			Outputs:      []explorer.DatasetOutput{{Name: "output", State: "QUERYABLE", Queryable: true}},
		},
		Publication: explorer.PublicationMetadata{
			State:       string(explorer.RevisionReady),
			Generation:  pubauthGeneration,
			ExecutionID: "exec_pubauth_" + tag,
			UpdatedAt:   pubauthCreatedAt,
		},
		Status:    explorer.RevisionReady,
		CreatedBy: "author@calypr",
		CreatedAt: pubauthCreatedAt,
		ReadyAt:   timePtr(pubauthCreatedAt),
	}
}

func pubauthRelease(project, generation, id string) dataset.ProjectRelease {
	return dataset.ProjectRelease{
		ID:         id,
		Project:    project,
		GitCommit:  "commit-" + id,
		Generation: generation,
		Publications: []dataset.ReleasePublication{{
			Selector:    dataset.DataframeSelector{Recipe: "recipe-pubauth", TranslationVersion: "v1", Output: "output"},
			ExecutionID: "exec_pubauth_1", Generation: generation, Required: true, VerifiedAt: pubauthVerifiedAt,
		}},
		RequiredVerifications: []dataset.ContractVerification{{
			Selector:    dataset.DataframeSelector{Recipe: "recipe-pubauth", TranslationVersion: "v1", Output: "output"},
			ExecutionID: "exec_pubauth_1", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: pubauthVerifiedAt,
		}},
		CreatedAt: pubauthCreatedAt,
	}
}

func (f *pubAuthFixture) seedPublishedWorld(t *testing.T, store *Store, project, explorerID, tag string,
) (explorer.CompilationReceipt, explorer.Revision, dataset.ProjectRelease) {
	t.Helper()
	if _, err := store.Create(f.ctx, pubauthOwner(project, explorerID, explorer.ManagementInteractive)); err != nil {
		f.fail("create owner "+project+"/"+explorerID, err)
	}
	stageAndActivate(t, f, f.newGenerationStore(t), project, pubauthGeneration, nil)
	release := pubauthRelease(project, pubauthGeneration, "rel_"+project+"_"+tag)
	rel := f.newReleaseStore(t)
	if _, err := rel.SaveRelease(f.ctx, release); err != nil {
		f.fail("save release candidate "+release.ID, err)
	}
	receipt := pubauthReceipt(t, project, explorerID, tag)
	revision := pubauthRevision("rev_"+project+"_"+tag, project, explorerID, receipt.ID, tag)
	return receipt, revision, release
}

func (f *pubAuthFixture) publishOK(t *testing.T, ps *PublishStore,
	receipt explorer.CompilationReceipt, revision explorer.Revision,
	release dataset.ProjectRelease, expected int64,
) *explorer.Revision {
	t.Helper()
	out, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, expected)
	if err != nil {
		f.fail("PublishAuthoring "+revision.ID, err)
	}
	return out
}

func timePtr(v time.Time) *time.Time { return &v }

func installCrashSeam(ps *PublishStore, gate func(step string, tx pgx.Tx) error) *[]string {
	steps := &[]string{}
	ps.crashAt = func(ctx context.Context, tx pgx.Tx, step string) error {
		*steps = append(*steps, step)
		if gate != nil {
			return gate(step, tx)
		}
		return nil
	}
	return steps
}
