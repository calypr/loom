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

	arango "github.com/calypr/loom/internal/store/arango"
)

func main() {
	endpoint := flag.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flag.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev or loom_test_* database")
	mode := flag.String("mode", "dry-run", "dry-run or apply (use arango-preview-index-audit for a read-only audit)")
	authorize := flag.Bool("authorize-local-migration", false, "authorize local index mutation; required with -mode apply")
	flag.Parse()
	if err := run(*endpoint, *database, *mode, *authorize); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(endpoint, database, mode string, authorize bool) error {
	if mode != "dry-run" && mode != "apply" {
		return fmt.Errorf("unsupported mode %q", mode)
	}
	if err := validateLocalTarget(endpoint, database); err != nil {
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
	report, err := client.PreviewIndexes().MigratePreviewSupersededIndexes(ctx, dryRun)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func validateLocalTarget(endpoint, database string) error {
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
	if database != "loom_dev" && !strings.HasPrefix(database, "loom_test_") {
		return fmt.Errorf("refusing database %q; only loom_dev and loom_test_* are allowed", database)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
