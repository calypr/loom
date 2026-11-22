package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	arango "github.com/calypr/loom/internal/store/arango"
)

func runPreviewIndexMigration(endpoint, database, mode string, authorize bool) error {
	if mode != "dry-run" && mode != "apply" {
		return fmt.Errorf("unsupported mode %q", mode)
	}
	if err := validateLocalTarget(endpoint, database, false, false); err != nil {
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
