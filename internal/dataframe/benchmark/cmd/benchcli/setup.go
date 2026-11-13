package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	backendspostgres "github.com/calypr/loom/internal/backends/postgres/control"
)

// setup-pg / drop-pg: namespace-guarded lifecycle for the benchmark
// database. The admin DSN is read from LOOM_BENCH_ADMIN_DSN (never argv,
// never echoed): passwords must not reach process listings or logs.
//
// setup follows the canonical arrangement the server's full runtime
// requires: the caller CREATES the six assigned schemas, then the public
// factory bootstraps them with SetupMigrate; the server later opens the
// same placement with SetupNone (the factory's arrangement law).
//
// DDL runs on the factory's public ConnSource port: the PostgreSQL driver
// stays confined to internal/backends (ownership contract), and every
// identifier interpolated into DDL first passes a closed regex gate, then
// carries SQL-standard double-quote escaping.

var benchDatabaseRe = regexp.MustCompile(`^` + benchmarkNamespacePrefix + `[a-z0-9_]{4,48}$`)

var benchSchemaIdentRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// quoteBenchIdent renders name as a quoted SQL identifier. It refuses any
// name outside the closed benchmark shapes: no unvalidated text ever
// reaches DDL.
func quoteBenchIdent(name string) (string, error) {
	if !benchSchemaIdentRe.MatchString(name) {
		return "", fmt.Errorf("identifier %q is outside the benchmark shape", name)
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

func adminDSNFromEnv() (string, error) {
	dsn := strings.TrimSpace(os.Getenv("LOOM_BENCH_ADMIN_DSN"))
	if dsn == "" {
		return "", fmt.Errorf("LOOM_BENCH_ADMIN_DSN is not set")
	}
	return dsn, nil
}

func runSetupPG() {
	fs := flag.NewFlagSet("setup-pg", flag.ExitOnError)
	database := fs.String("database", "", "benchmark database name (must be under "+benchmarkNamespacePrefix+")")
	schemaPrefix := fs.String("schema-prefix", "loom_bench", "schema name prefix for the six-series ladder")
	_ = fs.Parse(os.Args[2:])
	dsn, err := adminDSNFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchcli setup-pg: BLOCKED —", err)
		os.Exit(3)
	}
	if !benchDatabaseRe.MatchString(*database) {
		fmt.Fprintf(os.Stderr, "benchcli setup-pg: BLOCKED — database %q is outside the benchmark namespace\n", *database)
		os.Exit(3)
	}
	ctx := context.Background()
	if err := setupPG(ctx, dsn, *database, *schemaPrefix); err != nil {
		fmt.Fprintln(os.Stderr, "benchcli setup-pg: FAILED —", redactSecretText(err.Error(), dsn))
		os.Exit(1)
	}
	fmt.Printf("benchcli setup-pg: OK database=%s schemas=%d\n", *database, len(backendspostgres.AllSeries()))
}

func runDropPG() {
	fs := flag.NewFlagSet("drop-pg", flag.ExitOnError)
	database := fs.String("database", "", "benchmark database name (must be under "+benchmarkNamespacePrefix+")")
	_ = fs.Parse(os.Args[2:])
	dsn, err := adminDSNFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchcli drop-pg: BLOCKED —", err)
		os.Exit(3)
	}
	if !benchDatabaseRe.MatchString(*database) {
		fmt.Fprintf(os.Stderr, "benchcli drop-pg: BLOCKED — database %q is outside the benchmark namespace\n", *database)
		os.Exit(3)
	}
	ctx := context.Background()
	if err := dropPG(ctx, dsn, *database); err != nil {
		fmt.Fprintln(os.Stderr, "benchcli drop-pg: FAILED —", redactSecretText(err.Error(), dsn))
		os.Exit(1)
	}
	fmt.Printf("benchcli drop-pg: OK database=%s\n", *database)
}

func setupPG(ctx context.Context, adminDSN, database, schemaPrefix string) error {
	// Schema names are <prefix>_<series>; every full name must carry the
	// benchmark namespace prefix.
	if schemaPrefix != strings.TrimSuffix(benchmarkNamespacePrefix, "_") &&
		!strings.HasPrefix(schemaPrefix, benchmarkNamespacePrefix) {
		return fmt.Errorf("schema prefix %q produces schemas outside the benchmark namespace", schemaPrefix)
	}
	quotedDatabase, err := quoteBenchIdent(database)
	if err != nil {
		return err
	}
	admin, err := openProbeRuntime(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("admin connect (credentials redacted): %w", err)
	}
	defer func() { _ = admin.Close() }()
	adminConn, err := admin.ConnSource().Acquire(ctx)
	if err != nil {
		return fmt.Errorf("admin acquire (credentials redacted): %w", err)
	}
	releaseAdmin := true
	defer func() {
		if releaseAdmin {
			adminConn.Release()
		}
	}()
	var exists bool
	if err := adminConn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", database).Scan(&exists); err != nil {
		return err
	}
	if exists {
		// Fresh database per run keeps digests and timings attributable.
		return fmt.Errorf("database %s already exists; drop-pg first (fresh per run keeps evidence attributable)", database)
	}
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+quotedDatabase); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	adminConn.Release()
	releaseAdmin = false // explicit release; the guard defers the rest

	targetDSN, err := withDatabase(adminDSN, database)
	if err != nil {
		return err
	}
	target, err := openProbeRuntime(ctx, targetDSN)
	if err != nil {
		return fmt.Errorf("target connect (credentials redacted): %w", err)
	}
	defer func() { _ = target.Close() }()
	targetConn, err := target.ConnSource().Acquire(ctx)
	if err != nil {
		return fmt.Errorf("target acquire (credentials redacted): %w", err)
	}
	defer targetConn.Release()
	schemas := map[backendspostgres.Series]string{}
	for _, series := range backendspostgres.AllSeries() {
		schema := schemaPrefix + "_" + string(series)
		quotedSchema, err := quoteBenchIdent(schema)
		if err != nil {
			return err
		}
		if _, err := targetConn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
			return fmt.Errorf("create schema %s: %w", schema, err)
		}
		schemas[series] = schema
	}

	// Canonical bootstrap: the public factory with SetupMigrate, exactly as
	// the server's live composition fixtures arrange it.
	runtime, err := backendspostgres.Open(ctx, backendspostgres.Config{
		DSN:            targetDSN,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
		Schemas:        backendspostgres.SeriesSchemas(schemas),
		Setup:          backendspostgres.SetupMigrate,
	})
	if err != nil {
		return fmt.Errorf("migrate six-series ladder: %w", err)
	}
	return runtime.Close()
}

func dropPG(ctx context.Context, adminDSN, database string) error {
	quotedDatabase, err := quoteBenchIdent(database)
	if err != nil {
		return err
	}
	admin, err := openProbeRuntime(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("admin connect (credentials redacted): %w", err)
	}
	defer func() { _ = admin.Close() }()
	adminConn, err := admin.ConnSource().Acquire(ctx)
	if err != nil {
		return fmt.Errorf("admin acquire (credentials redacted): %w", err)
	}
	defer adminConn.Release()
	_, err = adminConn.Exec(ctx, "DROP DATABASE IF EXISTS "+quotedDatabase+" WITH (FORCE)")
	return err
}

// withDatabase swaps the DSN path to the target database, keeping the
// admin's server/user/password selection. The admin DSN must be URL form
// (the accepted shape everywhere in this lane).
func withDatabase(adminDSN, database string) (string, error) {
	parsed, err := url.Parse(adminDSN)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", fmt.Errorf("admin dsn must be URL form (credentials redacted)")
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}
