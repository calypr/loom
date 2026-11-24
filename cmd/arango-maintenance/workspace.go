package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/projectid"
	arango "github.com/calypr/loom/internal/store/arango"
)

func runWorkspaceMigration(endpoint, database, mode string, authorize bool, migration string, scope ...string) error {
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
	if err := validateLocalTarget(endpoint, database, false, migration == "source-plan"); err != nil {
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
