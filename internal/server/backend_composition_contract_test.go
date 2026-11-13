package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	authscope "github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

var errCompositionSpyFault = errors.New("composition spy: open refused")

type compositionLedger struct {
	arango []arangofactory.Config
	pg     []control.Config
	ch     []clickhousestore.Options
}

func installCompositionSpies(t *testing.T, passthrough bool) *compositionLedger {
	t.Helper()
	ledger := &compositionLedger{}
	origArango, origPG, origCH := openArangoBackend, openPostgresBackend, newClickHouseClient
	t.Cleanup(func() {
		openArangoBackend, openPostgresBackend, newClickHouseClient = origArango, origPG, origCH
	})
	openArangoBackend = func(ctx context.Context, cfg arangofactory.Config) (*arangofactory.Runtime, error) {
		ledger.arango = append(ledger.arango, cfg)
		if passthrough {
			return origArango(ctx, cfg)
		}
		return nil, errCompositionSpyFault
	}
	openPostgresBackend = func(ctx context.Context, cfg control.Config) (*backendspostgres.Runtime, error) {
		ledger.pg = append(ledger.pg, cfg)
		if passthrough {
			return origPG(ctx, cfg)
		}
		return nil, errCompositionSpyFault
	}
	newClickHouseClient = func(opts clickhousestore.Options) (*clickhousestore.Client, error) {
		ledger.ch = append(ledger.ch, opts)
		if passthrough {
			return origCH(opts)
		}
		return nil, errCompositionSpyFault
	}
	return ledger
}

func compositionTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

const (
	spyPGPassword = "spy-pg-password-9f3c"
	spyPGUser     = "spy_pg_user"
)

func spyLadderConfig() Config {
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host: "127.0.0.1", Port: 5432, Database: "spydb",
		User: spyPGUser, Password: spyPGPassword, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 1,
		Schemas: map[string]string{
			"source":                 "spy_src",
			"explorer_control":       "spy_exp",
			"publication_control":    "spy_pub",
			"dataset_release":        "spy_rel",
			"recipe_chains":          "spy_rcp",
			"recipe_exec_registries": "spy_exe",
		},
	}
	return cfg
}

func spyFirstQueryConfig() Config {
	cfg := spyLadderConfig()
	cfg.Server.PostgreSQL.Schemas = map[string]string{
		"source":          "spy_src",
		"dataset_release": "spy_rel",
	}
	return cfg
}

func assertLedger(t *testing.T, what string, ledger *compositionLedger, arango, pg, ch int) {
	t.Helper()
	if len(ledger.arango) != arango || len(ledger.pg) != pg || len(ledger.ch) != ch {
		t.Fatalf("%s: open ledger arango=%d postgres=%d clickhouse=%d, want %d/%d/%d",
			what, len(ledger.arango), len(ledger.pg), len(ledger.ch), arango, pg, ch)
	}
}

func TestBackendCompositionContract(t *testing.T) {
	t.Run("arangoDefault_opensOnlyArangoOnce", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		cfg := Config{}
		cfg.Server.URL = "http://127.0.0.1:8529"
		cfg.Server.Database = "spy_arango_db"
		_, _, err := composeArangoRuntime(context.Background(), cfg, compositionTestLogger())
		if !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("compose must stop at the arango open with the spy fault, got %v", err)
		}
		assertLedger(t, "arango default", ledger, 1, 0, 0)
		if ledger.arango[0].URL != cfg.Server.URL || ledger.arango[0].Database != cfg.Server.Database {
			t.Fatalf("arango open carried foreign config: %+v", ledger.arango[0])
		}
	})

	t.Run("postgresLadder_opensOnlyPostgres", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		cfg := spyLadderConfig()
		_, _, err := composePostgreSQLFullRuntime(context.Background(), cfg, compositionTestLogger())
		if !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("compose must stop at the postgres open with the spy fault, got %v", err)
		}
		assertLedger(t, "postgres ladder", ledger, 0, 1, 0)
		opened := ledger.pg[0]
		if len(opened.Schemas) != 6 {
			t.Fatalf("full runtime must open the complete six-series ladder, got %d series", len(opened.Schemas))
		}
		for _, series := range []control.Series{
			control.SeriesSource, control.SeriesExplorerControl,
			control.SeriesPublicationControl, control.SeriesDatasetRelease,
			control.SeriesRecipeChains, control.SeriesRecipeExecRegistries,
		} {
			if opened.Schemas[series] == "" {
				t.Fatalf("series %s unassigned in the ladder open", series)
			}
		}
		if opened.Setup != control.SetupNone {
			t.Fatalf("production compose must never ask the factory to mutate schema state, got Setup=%v", opened.Setup)
		}
		if strings.Contains(err.Error(), spyPGPassword) {
			t.Fatal("open refusal echoed the PostgreSQL password")
		}
	})

	t.Run("twoSeries_firstQueryArm_onlyTwoSeries", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		_, _, err := composePostgreSQLFirstQueryRoute(context.Background(), spyFirstQueryConfig(), compositionTestLogger())
		if !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("first-query compose must stop at the postgres open with the spy fault, got %v", err)
		}
		assertLedger(t, "first-query pair", ledger, 0, 1, 0)
		opened := ledger.pg[0]
		if len(opened.Schemas) != 2 {
			t.Fatalf("first-query route must open exactly two series, got %d: %v", len(opened.Schemas), opened.Schemas)
		}
		if opened.Schemas[control.SeriesSource] != "spy_src" ||
			opened.Schemas[control.SeriesDatasetRelease] != "spy_rel" {
			t.Fatalf("first-query route opened foreign placements: %v", opened.Schemas)
		}
		if opened.Setup != control.SetupNone {
			t.Fatalf("first-query compose must use SetupNone, got %v", opened.Setup)
		}
	})

	t.Run("invalidShape_sentinelPreDial_zeroOpens", func(t *testing.T) {
		shapes := map[string]func(cfg *PostgreSQLConfig){
			"no-schemas": func(pg *PostgreSQLConfig) { pg.Schemas = nil },
			"one-schema": func(pg *PostgreSQLConfig) { pg.Schemas = map[string]string{"source": "spy_src"} },
			"three-schemas": func(pg *PostgreSQLConfig) {
				pg.Schemas = map[string]string{"source": "spy_src", "dataset_release": "spy_rel", "recipe_chains": "spy_rcp"}
			},
			"duplicated-names": func(pg *PostgreSQLConfig) { pg.Schemas["recipe_chains"] = "spy_src" },
			"blank-series":     func(pg *PostgreSQLConfig) { pg.Schemas["recipe_exec_registries"] = "  " },
			"foreign-series":   func(pg *PostgreSQLConfig) { pg.Schemas["made_up_series"] = "spy_extra" },
		}
		for name, mutate := range shapes {
			ledger := installCompositionSpies(t, false)
			cfg := spyLadderConfig()
			mutate(&cfg.Server.PostgreSQL)
			err := RunWithConfig(context.Background(), cfg)
			if !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
				t.Fatalf("%s: want the byte-stable startup refusal through dispatch, got %v", name, err)
			}
			if err.Error() != ErrPostgreSQLRuntimeNotWired.Error() {
				t.Fatalf("%s: refusal text drifted from the pinned sentinel: %q", name, err.Error())
			}
			assertLedger(t, name, ledger, 0, 0, 0)
		}
	})

	t.Run("openFault_neverFallsBackToArango", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		err := RunWithConfig(context.Background(), spyLadderConfig())
		if !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("the postgres open fault must propagate, got %v", err)
		}
		assertLedger(t, "open fault", ledger, 0, 1, 0)
	})

	t.Run("clickHouseDisabled_noClickHouseConstruction", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		cfg := spyLadderConfig()
		cfg.Server.ClickHouse.Enabled = false
		if _, _, err := composePostgreSQLFullRuntime(context.Background(), cfg, compositionTestLogger()); !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("full compose must stop at the postgres open, got %v", err)
		}
		cfg2 := spyFirstQueryConfig()
		cfg2.Server.ClickHouse.Enabled = false
		if _, _, err := composePostgreSQLFirstQueryRoute(context.Background(), cfg2, compositionTestLogger()); !errors.Is(err, errCompositionSpyFault) {
			t.Fatalf("first-query compose must stop at the postgres open, got %v", err)
		}
		assertLedger(t, "clickhouse disabled", ledger, 0, 2, 0)
	})

	t.Run("liveness_doesNoDatabaseWork", func(t *testing.T) {
		ledger := installCompositionSpies(t, false)
		observed := 0
		server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{
			Authorizer:     authscope.AllowAllAuthorizer{},
			CoreReadyCheck: func(context.Context) error { observed++; return nil },
		})
		if err != nil {
			t.Fatalf("build probe server: %v", err)
		}
		body, status := server.Liveness(context.Background())
		if status != http.StatusOK || body["status"] != "live" {
			t.Fatalf("liveness status=%d body=%v", status, body)
		}
		if observed != 0 {
			t.Fatalf("liveness consulted %d dependency checks; must be DB-free", observed)
		}
		assertLedger(t, "liveness", ledger, 0, 0, 0)
	})

	t.Run("liveProductionCompose_ledgerHolds", func(t *testing.T) {
		fixture := newPgCompFixture(t)
		ledger := installCompositionSpies(t, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, pgCompBootConfig(t, fixture, "127.0.0.1:0"), compositionTestLogger())
		if err != nil {
			t.Fatalf("production compose: %s", fixture.scrub(err))
		}
		assertLedger(t, "live full compose", ledger, 0, 1, 0)
		if ledger.pg[0].Setup != control.SetupNone || len(ledger.pg[0].Schemas) != 6 {
			t.Fatalf("live compose opened the wrong shape: Setup=%v series=%d", ledger.pg[0].Setup, len(ledger.pg[0].Schemas))
		}
		response, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/livez", nil))
		if err != nil {
			t.Fatalf("livez request: %s", fixture.scrub(err))
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("livez status=%d", response.StatusCode)
		}
		assertLedger(t, "live liveness", ledger, 0, 1, 0)
		if err := closeRuntime(); err != nil {
			t.Errorf("production closer: %s", fixture.scrub(err))
		}
		assertLedger(t, "live close", ledger, 0, 1, 0)
		if err := closeRuntime(); err != nil {
			t.Errorf("second production closer call: %s", fixture.scrub(err))
		}
		assertLedger(t, "second live close", ledger, 0, 1, 0)
	})
}
