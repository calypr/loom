package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func runPrepareAvailableColumns(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prepare-available-columns", flag.ContinueOnError)
	connection := arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}
	var project, generation string
	fs.StringVar(&connection.URL, "url", defaultURL, "ArangoDB base URL")
	fs.StringVar(&connection.Database, "database", defaultDatabase, "ArangoDB database")
	fs.StringVar(&project, "project", "", "Exact project name")
	fs.StringVar(&generation, "generation", "", "Exact immutable dataset generation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(project) == "" || strings.TrimSpace(generation) == "" || len(fs.Args()) != 0 {
		return fmt.Errorf("prepare-available-columns requires --project and --generation and no positional arguments")
	}
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close(ctx) }()
	store, err := catalogarango.New(client)
	if err != nil {
		return err
	}
	if err := ingest.PrepareGenerationAvailability(ctx, store, project, generation, nil); err != nil {
		return err
	}
	return printJSON(map[string]string{"project": project, "generation": generation, "state": "COMPLETE"})
}
