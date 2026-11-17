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
	storearango "github.com/calypr/loom/internal/store/arango"
)

func main() {
	endpoint := flag.String("endpoint", envOr("LOOM_ARANGO_URL", "http://127.0.0.1:8529"), "local Arango endpoint")
	database := flag.String("database", envOr("LOOM_ARANGO_DATABASE", "loom_dev"), "existing local loom_dev or loom_test_* database")
	timeout := flag.Duration("timeout", 10*time.Minute, "complete read-only audit deadline")
	flag.Parse()
	if err := run(*endpoint, *database, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(endpoint, database string, timeout time.Duration) error {
	if err := validateLocalTarget(endpoint, database); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := storearango.OpenExisting(ctx, endpoint, database)
	if err != nil {
		return err
	}
	defer client.Close(context.Background())
	collections, err := client.CollectionNamesReadOnly(ctx)
	if err != nil {
		return err
	}
	workspaceReport := explorerarango.WorkspaceAuditReport{
		Collections: []string{}, Entries: []explorerarango.WorkspaceAuditEntry{},
		ByClass: map[string]int{}, DigestRelations: map[string]int{}, ByFeature: map[string]int{},
	}
	for _, collection := range collections {
		workspaceReport.Collections = append(workspaceReport.Collections, collection)
		if err := client.VisitCollectionDocumentsRaw(ctx, collection, func(raw json.RawMessage) error {
			return workspaceReport.ObserveWorkspaceAuditDocument(collection, raw)
		}); err != nil {
			return fmt.Errorf("workspace audit incomplete at collection %s: %w", collection, err)
		}
	}
	previewIndexReport, err := client.AuditPreviewSupersededIndexes(ctx)
	if err != nil {
		return fmt.Errorf("preview-index audit incomplete: %w", err)
	}
	output := struct {
		Database  string                              `json:"database"`
		Complete  bool                                `json:"complete"`
		Workspace explorerarango.WorkspaceAuditReport `json:"workspace"`
		storearango.PreviewSupersedesAudit
	}{Database: database, Complete: true, Workspace: workspaceReport, PreviewSupersedesAudit: previewIndexReport}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
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
	if database != "loom_dev" && !strings.HasPrefix(database, "loom_test_") && !strings.HasPrefix(database, "loom_cleanup_") {
		return fmt.Errorf("refusing database %q; only loom_dev, loom_test_*, and loom_cleanup_* are allowed", database)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
