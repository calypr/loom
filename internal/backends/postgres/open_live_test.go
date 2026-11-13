package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	rows "github.com/calypr/loom/internal/backends/postgres/internal/rows"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/execution"
)

const ab06Namespace = "verify_ab06_"

func ab06LiveURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 live Open assembly half; the runner lane makes a skip a failure, never acceptance")
	}
	if u := raw; !strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://") {
		t.Fatalf("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL, got scheme-prefixed %q", u[:min(len(u), 16)])
	}
	return raw
}

func ab06NewNonce(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate fixture namespace nonce: %v", err)
	}
	return hex.EncodeToString(buf)
}

func ab06RawPool(t *testing.T, ctx context.Context, dsn string) *control.Client {
	t.Helper()
	pool, err := control.OpenPool(ctx, control.ClientConfig{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open control pool for fixture administration: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func ab06SeriesSchema(nonce string, series control.Series) string {
	return ab06Namespace + nonce + "_" + string(series)
}

func ab06AllSeriesSchemas(nonce string) control.SeriesSchemas {
	schemas := make(control.SeriesSchemas, len(control.AllSeries()))
	for _, series := range control.AllSeries() {
		schemas[series] = ab06SeriesSchema(nonce, series)
	}
	return schemas
}

func ab06Exec(t *testing.T, pool *control.Client, sql string) {
	t.Helper()
	rs, err := pool.Query(context.Background(), sql)
	if err != nil {
		t.Fatalf("fixture exec %q: %v", sql, err)
	}
	for rs.Next() {
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		t.Fatalf("fixture exec %q: %v", sql, err)
	}
}

func factoryOpenLiveAssembly(t *testing.T) {
	t.Run("live_open_assembly", func(t *testing.T) {
		dsn := ab06LiveURL(t)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		admin := ab06RawPool(t, ctx, dsn)

		nonce := ab06NewNonce(t)
		schemas := ab06AllSeriesSchemas(nonce)
		for _, series := range control.AllSeries() {
			ab06Exec(t, admin, "CREATE SCHEMA "+ab06SeriesSchema(nonce, series))
		}
		t.Cleanup(func() {
			for _, series := range control.AllSeries() {
				ab06Exec(t, admin, "DROP SCHEMA IF EXISTS "+ab06SeriesSchema(nonce, series)+" CASCADE")
			}
		})

		appName := "loom-pg-ab06live" + nonce
		runtime, err := postgres.Open(ctx, control.Config{
			DSN:             dsn,
			ApplicationName: appName,
			Schemas:         schemas,
			Setup:           control.SetupMigrate,
			MaxConns:        4,
			MinConns:        1,
			AcquireTimeout:  10 * time.Second,
		})
		if err != nil {
			t.Fatalf("postgres.Open over the real server: %v", err)
		}
		defer runtime.Close()

		if runtime.SourceRows == nil {
			t.Fatal("Open returned a Runtime with a nil SourceRows port")
		}
		if runtime.PlanJSON == nil {
			t.Fatal("Open returned a Runtime with a nil PlanJSON port")
		}

		if err := runtime.Health(ctx); err != nil {
			t.Fatalf("Health on the live runtime: %v", err)
		}

		var serverVersion string
		if err := runtime.QueryRow(ctx, "SHOW server_version").Scan(&serverVersion); err != nil {
			t.Fatalf("read server_version through the public query surface: %v", err)
		}
		major := serverVersion
		if i := strings.IndexAny(major, " \t"); i >= 0 {
			major = major[:i]
		}
		if i := strings.IndexByte(major, '.'); i >= 0 {
			major = major[:i]
		}
		if major != "18" {
			t.Fatalf("readiness gate admitted server major %q, want 18 (server_version %q)", major, serverVersion)
		}

		migrateResult, err := runtime.MigrateSchema(ctx)
		if err != nil {
			t.Fatalf("public MigrateSchema witness after Open(SetupMigrate): %v", err)
		}
		if migrateResult.LatestVersion <= 0 || len(migrateResult.Applied) == 0 {
			t.Fatalf("source-series bootstrap not witnessed: %+v", migrateResult)
		}
		for _, series := range control.AllSeries() {
			var ledgerRows int64
			if err := runtime.QueryRow(ctx, fmt.Sprintf(
				"SELECT count(*) FROM %s.schema_migrations", ab06SeriesSchema(nonce, series),
			)).Scan(&ledgerRows); err != nil {
				t.Fatalf("read ledger from series %q schema %s: %v", series, ab06SeriesSchema(nonce, series), err)
			}
			if ledgerRows == 0 {
				t.Fatalf("series %q bootstrap not witnessed: empty ledger in %s", series, ab06SeriesSchema(nonce, series))
			}
		}

		var tagged int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE application_name = $1", appName,
		).Scan(&tagged); err != nil {
			t.Fatalf("query pg_stat_activity for the own-prefix tag: %v", err)
		}
		if tagged < 1 {
			t.Fatalf("no live backend carried the own-prefix application_name %q (pool self-tagging regressed)", appName)
		}

		streamNonce := ab06NewNonce(t)
		streamSchemas := ab06AllSeriesSchemas(streamNonce)
		for _, series := range control.AllSeries() {
			ab06Exec(t, admin, "CREATE SCHEMA "+ab06SeriesSchema(streamNonce, series))
		}
		t.Cleanup(func() {
			for _, series := range control.AllSeries() {
				ab06Exec(t, admin, "DROP SCHEMA IF EXISTS "+ab06SeriesSchema(streamNonce, series)+" CASCADE")
			}
		})
		streamRuntime, err := postgres.Open(ctx, control.Config{
			DSN:             dsn,
			ApplicationName: "loom-pg-ab06stream" + streamNonce,
			Schemas:         streamSchemas,
			Setup:           control.SetupNone,
			MaxConns:        2,
			MinConns:        1,
			AcquireTimeout:  250 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("open streaming runtime (AcquireTimeout 250ms): %v", err)
		}
		defer streamRuntime.Close()

		const streamRows = 30
		streamCommand, err := command.NewPostgreSQLCommand(
			fmt.Sprintf("SELECT generate_series(1, %d)::bigint AS n, pg_sleep(0.02) AS drip", streamRows), nil)
		if err != nil {
			t.Fatalf("build server-paced stream command: %v", err)
		}
		seen := 0
		if err := streamRuntime.SourceRows(ctx, execution.RowsRequest{
			Command:         streamCommand,
			DeclaredColumns: []string{"n", "drip"},
		}, func(row map[string]any) error {
			seen++
			return nil
		}); err != nil {
			t.Fatalf("server-paced stream ran longer than AcquireTimeout and failed (context bound to acquisition, not to the caller): %v", err)
		}
		if seen != streamRows {
			t.Fatalf("streamed %d rows, want %d", seen, streamRows)
		}
		slowRow := streamRuntime.QueryRow(ctx, "SELECT pg_sleep(0.35), 7::bigint")
		var dripped string
		var seven int64
		if err := slowRow.Scan(&dripped, &seven); err != nil {
			t.Fatalf("QueryRow Scan after a server-side delay past the acquire timeout failed: %v", err)
		}
		if seven != 7 {
			t.Fatalf("slow QueryRow value = %d, want 7", seven)
		}

		literalCommand, err := command.NewPostgreSQLCommand(
			"SELECT $1::bigint AS n, 'loom'::text AS tag",
			[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterBigint, Value: int64(42)}},
		)
		if err != nil {
			t.Fatalf("build PG-tagged literal command: %v", err)
		}
		var streamed []map[string]any
		if err := runtime.SourceRows(ctx, execution.RowsRequest{
			Command:         literalCommand,
			DeclaredColumns: []string{"n", "tag"},
		}, func(row map[string]any) error {
			streamed = append(streamed, row)
			return nil
		}); err != nil {
			t.Fatalf("SourceRows over the live server: %v", err)
		}
		if len(streamed) != 1 {
			t.Fatalf("SourceRows streamed %d rows, want exactly 1", len(streamed))
		}
		if n, ok := streamed[0]["n"].(int64); !ok || n != 42 {
			t.Fatalf("typed literal round trip: row[n] = %#v, want int64(42)", streamed[0]["n"])
		}
		if tag, ok := streamed[0]["tag"].(string); !ok || tag != "loom" {
			t.Fatalf("typed literal round trip: row[tag] = %#v, want \"loom\"", streamed[0]["tag"])
		}

		if err := runtime.SourceRows(ctx, execution.RowsRequest{
			Command:         literalCommand,
			DeclaredColumns: []string{"n"},
		}, func(map[string]any) error { return nil }); !errors.Is(err, rows.ErrColumnContractMismatch) {
			t.Fatalf("declared-column undercount: err = %v, want %v", err, rows.ErrColumnContractMismatch)
		}

		aqlCommand, err := command.NewAQLCommand("FOR d IN c RETURN d", nil)
		if err != nil {
			t.Fatalf("build AQL-tagged command: %v", err)
		}
		if err := runtime.SourceRows(ctx, execution.RowsRequest{
			Command:         aqlCommand,
			DeclaredColumns: []string{"d"},
		}, func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("AQL-tagged command into the PostgreSQL port: err = %v, want %v", err, command.ErrCompiledCommandBackendMismatch)
		}

		explainCommand, err := command.NewPostgreSQLCommand(
			"EXPLAIN (FORMAT JSON) SELECT $1::bigint AS n",
			[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterBigint, Value: int64(1)}},
		)
		if err != nil {
			t.Fatalf("build EXPLAIN-retagged command: %v", err)
		}
		plan, err := runtime.PlanJSON(ctx, explainCommand)
		if err != nil {
			t.Fatalf("PlanJSON over the live server: %v", err)
		}
		var planDoc []map[string]any
		if err := json.Unmarshal(plan, &planDoc); err != nil {
			t.Fatalf("PlanJSON did not decode as a JSON array of plan objects: %v (raw %s)", err, plan)
		}
		if len(planDoc) == 0 {
			t.Fatal("PlanJSON returned an empty plan array")
		}

		absentNonce := ab06NewNonce(t)
		missingSchema := ab06SeriesSchema(absentNonce, control.AllSeries()[0])
		failedTag := "loom-pg-ab06fail" + absentNonce
		if _, err := postgres.Open(ctx, control.Config{
			DSN:             dsn,
			ApplicationName: failedTag,
			Schemas:         ab06AllSeriesSchemas(absentNonce),
			Setup:           control.SetupMigrate,
			MaxConns:        2,
			MinConns:        1,
			AcquireTimeout:  10 * time.Second,
		}); !errors.Is(err, control.ErrBootstrap) {
			t.Fatalf("Open over an absent caller-owned schema: err = %v, want an %v-wrapped refusal", err, control.ErrBootstrap)
		}
		var created bool
		if err := admin.QueryRow(ctx, "SELECT to_regnamespace($1) IS NOT NULL", missingSchema).Scan(&created); err != nil {
			t.Fatalf("check absent schema stayed absent: %v", err)
		}
		if created {
			t.Fatalf("setup policy created the unassigned schema %q; the migrate runner must refuse and leave schema lifecycle to the caller", missingSchema)
		}
		ab06WaitBackendsZero(t, admin, failedTag, "failed Open")

		if err := runtime.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatalf("second Close must stay safe, got: %v", err)
		}
		if err := runtime.Health(ctx); !errors.Is(err, control.ErrRuntimeClosed) {
			t.Fatalf("Health after Close: err = %v, want %v", err, control.ErrRuntimeClosed)
		}
		if _, err := runtime.Query(ctx, "SELECT 1"); !errors.Is(err, control.ErrRuntimeClosed) {
			t.Fatalf("Query after Close: err = %v, want %v", err, control.ErrRuntimeClosed)
		}
		if err := runtime.SourceRows(ctx, execution.RowsRequest{
			Command:         literalCommand,
			DeclaredColumns: []string{"n", "tag"},
		}, func(map[string]any) error { return nil }); !errors.Is(err, rows.ErrQuery) {
			t.Fatalf("SourceRows after Close: err = %v, want an %v-classified refusal", err, rows.ErrQuery)
		}

		ab06WaitBackendsZero(t, admin, appName, "Close of the main runtime")

		var remaining int
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE $1", ab06Namespace+"%",
		).Scan(&remaining); err != nil {
			t.Fatalf("count owned-namespace schemas: %v", err)
		}
		if want := 2 * len(control.AllSeries()); remaining != want {
			t.Fatalf("owned namespace holds %d schemas during the run, want exactly %d (this subtest's two fixtures)", remaining, want)
		}
	})
}

func TestZZZeroLeftoverAB06LiveOpenFixtures(t *testing.T) {
	dsn := ab06LiveURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := ab06RawPool(t, ctx, dsn)
	var leftovers []string
	rs, err := pool.Query(ctx,
		"SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE $1 ORDER BY schema_name",
		ab06Namespace+"%")
	if err != nil {
		t.Fatalf("list owned-namespace schemas: %v", err)
	}
	defer rs.Close()
	for rs.Next() {
		var name string
		if err := rs.Scan(&name); err != nil {
			t.Fatalf("scan leftover schema name: %v", err)
		}
		leftovers = append(leftovers, name)
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("read leftover schema rows: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("verify_ab06_* schemas survived the suite: %v", leftovers)
	}
}

func ab06WaitBackendsZero(t *testing.T, admin *control.Client, tag, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var leaked int64
	for {
		if err := admin.QueryRow(context.Background(),
			"SELECT count(*) FROM pg_stat_activity WHERE application_name = $1", tag,
		).Scan(&leaked); err != nil {
			t.Fatalf("query pg_stat_activity for tag %q: %v", tag, err)
		}
		if leaked == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s leaked %d live backends under tag %q after 10s", what, leaked, tag)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
