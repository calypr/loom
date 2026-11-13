package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
)

const backfillSignalChildEnv = "LOOM_C01_BACKFILL_SIGNAL_CHILD"
const backfillSignalReadyEnv = "LOOM_C01_BACKFILL_SIGNAL_READY"

func TestBackfillSemanticInventorySignalCancelsProcessContext(t *testing.T) {
	if os.Getenv(backfillSignalChildEnv) == "1" {
		readyPath := os.Getenv(backfillSignalReadyEnv)
		err := withSignalCancellation(context.Background(), func(ctx context.Context) error {
			if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			os.Exit(2)
		}
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackfillSemanticInventorySignalCancelsProcessContext$")
	cmd.Env = append(os.Environ(), backfillSignalChildEnv+"=1", backfillSignalReadyEnv+"="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("child did not enter the backfill signal context")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("send SIGTERM to backfill subprocess: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("backfill subprocess did not cleanly observe SIGTERM: %v", err)
	}
}

func TestParseGenerationLoadWiresImmutableDataset(t *testing.T) {
	config, err := parseLoadCommand([]string{
		"--project", "project-a",
		"--generation", "load:2026-07-11/v1",
		"--meta-dir", "META_SMALL",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseLoadCommand() error = %v", err)
	}
	if config.Options.Truncate {
		t.Fatal("generation load truncate = true, want false")
	}
	if config.Options.Dataset == nil {
		t.Fatal("generation load Dataset = nil, want immutable dataset reference")
	}
	if got, want := config.Options.Dataset.Project, "project-a"; got != want {
		t.Fatalf("generation dataset project = %q, want %q", got, want)
	}
	if got, want := config.Options.Dataset.Generation, "load:2026-07-11/v1"; got != want {
		t.Fatalf("generation dataset generation = %q, want %q", got, want)
	}
	if config.MemoryBytes <= 0 || config.Options.WorkerCount != 2 {
		t.Fatalf("resource defaults = memory %d workers %d, want finite memory and two workers", config.MemoryBytes, config.Options.WorkerCount)
	}
}

func TestParseMemoryBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{input: "4GiB", want: 4 << 30},
		{input: "512Mi", want: 512 << 20},
		{input: "1000", want: 1000},
		{input: "0", want: 0},
	}
	for _, test := range tests {
		got, err := parseMemoryBytes(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parseMemoryBytes(%q) = %d, %v; want %d", test.input, got, err, test.want)
		}
	}
	if _, err := parseMemoryBytes("not-memory"); err == nil {
		t.Fatal("parseMemoryBytes accepted invalid input")
	}
}

func TestParseGenerationLoadRejectsUnsafeOrInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing generation",
			args: []string{"--project", "project-a"},
			want: "--generation is required",
		},
		{
			name: "invalid opaque generation",
			args: []string{"--project", "project-a", "--generation", " generation-a"},
			want: "invalid --generation",
		},
		{
			name: "truncate is not exposed",
			args: []string{"--project", "project-a", "--generation", "generation-a", "--truncate=true"},
			want: "flag provided but not defined",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseLoadCommand(test.args, flag.ContinueOnError)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseLoadCommand() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseRepairCommandStagesDistinctImmutableGeneration(t *testing.T) {
	config, err := parseRepairCommand([]string{
		"--project", "project-a",
		"--source-generation", "generation-old",
		"--generation", "generation-new",
		"--meta-dir", "META_SMALL",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseRepairCommand() error = %v", err)
	}
	if config.Load.Options.Dataset == nil || config.Load.Options.Dataset.Generation != "generation-new" {
		t.Fatalf("repair dataset = %#v, want generation-new", config.Load.Options.Dataset)
	}
	if !config.Load.Options.StageOnly {
		t.Fatal("repair command must stage before optional activation")
	}
	if config.Activate {
		t.Fatal("repair command activated without --activate")
	}
}

func TestParseRepairCommandRejectsSameGeneration(t *testing.T) {
	_, err := parseRepairCommand([]string{
		"--project", "project-a",
		"--source-generation", "generation-a",
		"--generation", "generation-a",
	}, flag.ContinueOnError)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("parseRepairCommand() error = %v, want distinct-generation error", err)
	}
}

func TestParseActivateCommandRequiresValidGeneration(t *testing.T) {
	config, err := parseActivateCommand([]string{
		"--project", "project-a",
		"--generation", "generation-new",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseActivateCommand() error = %v", err)
	}
	if config.Project != "project-a" || config.Generation != "generation-new" {
		t.Fatalf("activation config = %#v", config)
	}
	if _, err := parseActivateCommand([]string{"--project", "project-a"}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "--generation is required") {
		t.Fatalf("missing generation error = %v", err)
	}
}

func TestParseBackfillSemanticInventoryRequiresScopedBoundedRequest(t *testing.T) {
	config, err := parseBackfillSemanticInventoryCommand([]string{
		"--project", "cda-project", "--generation", "cda-v1",
		"--url", "http://arangodb:8529", "--database", "loom-test",
		"--page-size", "700", "--batch-size", "1200",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatal(err)
	}
	if config.Options.Project != "cda-project" || config.Options.DatasetGeneration != "cda-v1" || config.Options.PageSize != 700 || config.Options.BatchSize != 1200 {
		t.Fatalf("parsed backfill request = %+v", config)
	}
	if config.Connection.URL != "http://arangodb:8529" || config.Connection.Database != "loom-test" {
		t.Fatalf("parsed backfill connection = %+v", config.Connection)
	}

	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"--generation", "g"}, want: "--project is required"},
		{args: []string{"--project", "p"}, want: "--generation is required"},
		{args: []string{"--project", "p", "--generation", "g", "--page-size", "1001"}, want: "--page-size"},
		{args: []string{"--project", "p", "--generation", "g", "--batch-size", "5001"}, want: "--batch-size"},
	} {
		_, err := parseBackfillSemanticInventoryCommand(test.args, flag.ContinueOnError)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("parse backfill %v error = %v, want %q", test.args, err, test.want)
		}
	}
}

func TestParseDiscoverPopulatedReferencesRejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"bulder", "storagee", "STORAGE", ""} {
		_, _, err := parseDiscoverPopulatedReferenceOptions([]string{"--mode", mode}, flag.ContinueOnError)
		if mode == catalog.TraversalModeStorage || mode == catalog.TraversalModeBuilder {
			if err != nil {
				t.Fatalf("parseDiscoverPopulatedReferenceOptions(mode=%q) error = %v, want accepted", mode, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("parseDiscoverPopulatedReferenceOptions(mode=%q) accepted invalid traversal mode", mode)
		}
		if !strings.Contains(err.Error(), catalog.TraversalModeStorage) || !strings.Contains(err.Error(), catalog.TraversalModeBuilder) {
			t.Fatalf("parseDiscoverPopulatedReferenceOptions(mode=%q) error = %v, want it to name valid modes %q and %q", mode, err, catalog.TraversalModeStorage, catalog.TraversalModeBuilder)
		}
	}
}

func TestParseDiscoveryCommandsPassExplicitDatasetGeneration(t *testing.T) {
	fields, _, err := parseDiscoverPopulatedFieldOptions([]string{
		"--project", "project-a",
		"--dataset-generation", "generation-a",
		"--resource-type", "Patient",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseDiscoverPopulatedFieldOptions() error = %v", err)
	}
	if got, want := fields.DatasetGeneration, "generation-a"; got != want {
		t.Fatalf("field discovery generation = %q, want %q", got, want)
	}
	if got, want := fields.ResourceType, "Patient"; got != want {
		t.Fatalf("field discovery resource type = %q, want %q", got, want)
	}

	references, _, err := parseDiscoverPopulatedReferenceOptions([]string{
		"--project", "project-a",
		"--dataset-generation", "generation-a",
		"--from-type", "Specimen",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseDiscoverPopulatedReferenceOptions() error = %v", err)
	}
	if got, want := references.DatasetGeneration, "generation-a"; got != want {
		t.Fatalf("reference discovery generation = %q, want %q", got, want)
	}
	if got, want := references.FromType, "Specimen"; got != want {
		t.Fatalf("reference discovery source type = %q, want %q", got, want)
	}
}

func TestCommandRoutesDispatchesMutableLoad(t *testing.T) {
	routes := commandRoutes()
	handler, ok := routes["load"]
	if !ok || handler == nil {
		t.Fatal("`load` command is not dispatched; it must reach a mutable runner")
	}
	if _, ok := routes["load-generation"]; !ok {
		t.Fatal("load-generation route lost while restoring the load dispatch")
	}
	if _, ok := routes["command-that-does-not-exist"]; ok {
		t.Fatal("unknown command unexpectedly routed; unknown commands must reach usage()+exit(2)")
	}
}

func TestParseLoadMutableWiresFlagsWithoutGeneration(t *testing.T) {
	config, err := parseLoadMutableCommand([]string{
		"--url", "http://127.0.0.1:8529",
		"--database", "fhir_proto",
		"--meta-dir", "META",
		"--project", "ARANGODB_PROTO",
		"--auth-resource-path", "EllrottLab-GDC_Data",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseLoadMutableCommand() error = %v", err)
	}
	if config.Options.Dataset != nil {
		t.Fatalf("mutable load set dataset %#v, want nil so Load selects the mutable path", config.Options.Dataset)
	}
	if config.Options.URL != "http://127.0.0.1:8529" || config.Options.Database != "fhir_proto" ||
		config.Options.MetaDir != "META" || config.Options.Project != "ARANGODB_PROTO" ||
		config.Options.AuthResourcePath != "EllrottLab-GDC_Data" {
		t.Fatalf("mutable load parsed options = %+v, want the QUICKSTART flags wired", config.Options)
	}
	if _, err := parseLoadMutableCommand([]string{"--generation", "generation-a"}, flag.ContinueOnError); err == nil ||
		!strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("parseLoadMutableCommand accepted --generation; the mutable load must not expose it")
	}
}

func TestManifestMutatingCommandsUseSignalCancellation(t *testing.T) {
	guarded := signalGuardedCommands()
	routes := commandRoutes()
	for _, command := range []string{"load-generation", "repair-generation", "activate-generation", "backfill-semantic-inventory"} {
		if !guarded[command] {
			t.Fatalf("manifest-mutating command %q must route through signal cancellation", command)
		}
		if _, ok := routes[command]; !ok {
			t.Fatalf("guarded command %q is not a registered route", command)
		}
	}
	for _, command := range []string{"load", "discover-populated-references", "discover-populated-fields", "rebuild-relationship-catalog", "rebuild-resource-inventory", "audit-relationship-edges"} {
		if guarded[command] {
			t.Fatalf("command %q must not be wrapped in signal cancellation", command)
		}
	}
}

func TestRequireDatabaseExistsRefusesMissingDatabase(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/_api/database/current") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":true,"errorNum":1228,"code":404,"errorMessage":"database not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	err := requireDatabaseExists(context.Background(), server.URL, "ghost_db")
	if err == nil {
		t.Fatal("requireDatabaseExists accepted a missing database")
	}
	if !strings.Contains(err.Error(), "refusing to create missing database") || !strings.Contains(err.Error(), "ghost_db") {
		t.Fatalf("requireDatabaseExists error = %v, want a refusal naming the missing database", err)
	}
	if !containsSeen(seen, "/_db/ghost_db/_api/database/current") {
		t.Fatalf("DatabaseExists did not issue the expected current-database request; saw %v", seen)
	}
}

func TestRequireDatabaseExistsAllowsExistingDatabase(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"result":{"name":"fhir_proto","id":"fhir_proto","isSystem":false,"type":11},"allowImplicit":false,"user":"root"}`)
	}))
	defer server.Close()

	if err := requireDatabaseExists(context.Background(), server.URL, "fhir_proto"); err != nil {
		t.Fatalf("requireDatabaseExists(existing) error = %v, want nil", err)
	}
	if !containsSeen(seen, "/_db/fhir_proto/_api/database/current") {
		t.Fatalf("requireDatabaseExists did not probe the current-database endpoint; saw %v", seen)
	}
}

func TestOpenClientRequiringDatabaseRefusesAndNeverAutoCreates(t *testing.T) {
	var createRequests int
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_api/database") {
			createRequests++
		}
		if strings.HasSuffix(r.URL.Path, "/_api/database/current") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":true,"errorNum":1228,"code":404,"errorMessage":"database not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client, err := openClientRequiringDatabase(context.Background(), server.URL, "ghost_db")
	if err == nil || !strings.Contains(err.Error(), "refusing to create missing database") {
		t.Fatalf("openClientRequiringDatabase(missing) error = %v, want refusal", err)
	}
	if client != nil {
		t.Fatal("openClientRequiringDatabase returned a client for a missing database")
	}
	if createRequests != 0 {
		t.Fatalf("missing-database gate must not trigger a create-database request; saw requests %v", seen)
	}
}

func TestOpenClientRequiringDatabaseOpensExistingWithoutAutoCreate(t *testing.T) {
	var createRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_api/database") {
			createRequests++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"result":{"name":"fhir_proto","id":"fhir_proto","isSystem":false,"type":11},"allowImplicit":false,"user":"root"}`)
	}))
	defer server.Close()

	client, err := openClientRequiringDatabase(context.Background(), server.URL, "fhir_proto")
	if err != nil {
		t.Fatalf("openClientRequiringDatabase(existing) error = %v, want nil", err)
	}
	if client == nil {
		t.Fatal("openClientRequiringDatabase(existing) returned a nil client")
	}
	defer func() { _ = client.Close(context.Background()) }()
	if createRequests != 0 {
		t.Fatalf("existing database must not trigger a create-database request")
	}
}

func containsSeen(seen []string, suffix string) bool {
	for _, entry := range seen {
		if strings.HasSuffix(entry, suffix) {
			return true
		}
	}
	return false
}
