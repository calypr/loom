package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/projectid"
	arango "github.com/calypr/loom/internal/store/arango"
)

func main() {
	endpoint := flag.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flag.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev, loom_test_*, or fhir_proto for a scoped source-plan migration")
	mode := flag.String("mode", "dry-run", "dry-run or apply (use arango-preview-index-audit for a read-only audit)")
	migration := flag.String("migration", "workspace", "workspace, table-shape, or source-plan; apply workspace before table-shape")
	project := flag.String("project", "", "project for a scoped source-plan migration")
	explorer := flag.String("explorer", "", "Explorer ID for a scoped source-plan migration")
	authorize := flag.Bool("authorize-local-migration", false, "authorize local workspace/digest mutation; required with -mode apply")
	flag.Parse()
	if err := run(*endpoint, *database, *mode, *authorize, *migration, *project, *explorer); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(endpoint, database, mode string, authorize bool, migration string, scope ...string) error {
	if migration != "workspace" && migration != "table-shape" && migration != "source-plan" {
		return fmt.Errorf("unsupported migration %q", migration)
	}
	if mode != "dry-run" && mode != "apply" {
		return fmt.Errorf("unsupported mode %q", mode)
	}
	if migration == "source-plan" {
		if len(scope) != 2 || strings.TrimSpace(scope[0]) == "" || strings.TrimSpace(scope[1]) == "" {
			return errors.New("source-plan migration requires -project and -explorer")
		}
	}
	if err := validateLocalTarget(endpoint, database, migration == "source-plan"); err != nil {
		return err
	}
	if mode == "apply" && !authorize {
		return errors.New("-mode apply requires -authorize-local-migration")
	}
	if mode != "apply" && authorize {
		return errors.New("-authorize-local-migration is only valid with -mode apply")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := arango.OpenExisting(ctx, endpoint, database)
	if err != nil {
		return err
	}
	defer client.Close(context.Background())
	dryRun := mode == "dry-run"
	var report any
	switch migration {
	case "source-plan":
		report, err = explorerarango.MigrateStoredSourcePlan(ctx, client, projectid.Legacy(scope[0]), strings.TrimSpace(scope[1]), dryRun)
	case "table-shape":
		report, err = explorerarango.MigrateStoredTableShapes(ctx, client, dryRun)
	default:
		report, err = explorerarango.MigrateStoredWorkspaces(ctx, client, dryRun)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func validateLocalTarget(endpoint, database string, sourcePlan bool) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint must be a local http URL without credentials, path, query, or fragment")
	}
	host := parsed.Hostname()
	localHost := host == "localhost" || host == "arangodb"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		localHost = true
	}
	if !localHost {
		return fmt.Errorf("refusing non-local Arango endpoint %q", host)
	}
	if database != "loom_dev" && !strings.HasPrefix(database, "loom_test_") && !(sourcePlan && database == "fhir_proto") {
		return fmt.Errorf("refusing database %q; only local Loom development databases are allowed", database)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
