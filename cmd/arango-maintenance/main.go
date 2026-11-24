package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "preview-index-audit":
		err = previewIndexAuditCommand(os.Args[2:])
	case "preview-index-migration":
		err = previewIndexMigrationCommand(os.Args[2:])
	case "workspace-migration":
		err = workspaceMigrationCommand(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: arango-maintenance <preview-index-audit|preview-index-migration|workspace-migration> [flags]")
}

func previewIndexAuditCommand(args []string) error {
	flags := flag.NewFlagSet("preview-index-audit", flag.ExitOnError)
	endpoint := flags.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flags.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev or loom_test_* database")
	timeout := flags.Duration("timeout", 10*time.Minute, "complete read-only audit deadline")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return runPreviewIndexAudit(*endpoint, *database, *timeout)
}

func previewIndexMigrationCommand(args []string) error {
	flags := flag.NewFlagSet("preview-index-migration", flag.ExitOnError)
	endpoint := flags.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flags.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev or loom_test_* database")
	mode := flags.String("mode", "dry-run", "dry-run or apply (use preview-index-audit for a read-only audit)")
	authorize := flags.Bool("authorize-local-migration", false, "authorize local index mutation; required with -mode apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return runPreviewIndexMigration(*endpoint, *database, *mode, *authorize)
}

func workspaceMigrationCommand(args []string) error {
	flags := flag.NewFlagSet("workspace-migration", flag.ExitOnError)
	endpoint := flags.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flags.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev, loom_test_*, or fhir_proto for a scoped source-plan migration")
	mode := flags.String("mode", "dry-run", "dry-run or apply (use preview-index-audit for a read-only audit)")
	migration := flags.String("migration", "workspace", "workspace, table-shape, or source-plan; apply workspace before table-shape")
	project := flags.String("project", "", "project for a scoped source-plan migration")
	explorer := flags.String("explorer", "", "Explorer ID for a scoped source-plan migration")
	authorize := flags.Bool("authorize-local-migration", false, "authorize local workspace/digest mutation; required with -mode apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return runWorkspaceMigration(*endpoint, *database, *mode, *authorize, *migration, *project, *explorer)
}

func validateLocalTarget(endpoint, database string, allowCleanup, allowSourcePlan bool) error {
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
	allowed := database == "loom_dev" || strings.HasPrefix(database, "loom_test_") || (allowCleanup && strings.HasPrefix(database, "loom_cleanup_")) || (allowSourcePlan && database == "fhir_proto")
	if !allowed {
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
