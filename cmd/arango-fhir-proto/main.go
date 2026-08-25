package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	defaultURL      = "http://127.0.0.1:8529"
	defaultDatabase = "fhir_proto"
	defaultProject  = "ARANGODB_PROTO"
	defaultSchema   = "schemas/graph-fhir.json"
	defaultMetaDir  = "META"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "load-generation":
		err = runLoadGeneration(ctx, os.Args[2:])
	case "discover-populated-references":
		err = runDiscoverPopulatedReferences(ctx, os.Args[2:])
	case "discover-populated-fields":
		err = runDiscoverPopulatedFields(ctx, os.Args[2:])
	case "rebuild-relationship-catalog":
		err = runRebuildRelationshipCatalog(ctx, os.Args[2:])
	case "audit-relationship-edges":
		err = runAuditRelationshipEdges(ctx, os.Args[2:])
	case "repair-generation":
		err = runRepairGeneration(ctx, os.Args[2:])
	case "activate-generation":
		err = runActivateGeneration(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type loadCommandConfig struct {
	Options ingest.LoadOptions

	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string
	MemoryLimit  string
	MemoryBytes  int64

	Generation string
}

func runLoadGeneration(ctx context.Context, args []string) error {
	config, err := parseLoadCommand(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	return runConfiguredLoad(ctx, config)
}

func parseLoadCommand(args []string, errorHandling flag.ErrorHandling) (loadCommandConfig, error) {
	fs := flag.NewFlagSet("load-generation", errorHandling)
	config := loadCommandConfig{}
	configureLoadFlags(fs, &config)
	if err := fs.Parse(args); err != nil {
		return loadCommandConfig{}, err
	}
	if err := config.parseMemoryLimit(); err != nil {
		return loadCommandConfig{}, err
	}
	if config.Generation == "" {
		return loadCommandConfig{}, fmt.Errorf("--generation is required for load-generation")
	}
	if config.Options.Truncate {
		return loadCommandConfig{}, fmt.Errorf("--truncate=true is not permitted for load-generation")
	}
	ref, err := publication.NewRef(config.Options.Project, config.Generation)
	if err != nil {
		return loadCommandConfig{}, fmt.Errorf("invalid --generation for load-generation: %w", err)
	}
	config.Options.Dataset = &ref
	return config, nil
}

func configureLoadFlags(fs *flag.FlagSet, config *loadCommandConfig) {
	fs.StringVar(&config.CPUProfile, "cpu-profile", "", "Write CPU profile to file")
	fs.StringVar(&config.MemProfile, "mem-profile", "", "Write heap profile to file at end of run")
	fs.StringVar(&config.TraceProfile, "trace-profile", "", "Write runtime trace to file")
	fs.StringVar(&config.BlockProfile, "block-profile", "", "Write block profile to file at end of run")
	fs.StringVar(&config.MemoryLimit, "memory-limit", "4GiB", "Go memory limit for this load, for example 4GiB; 0 disables the soft limit")
	fs.StringVar(&config.Options.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&config.Options.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&config.Options.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.StringVar(&config.Options.MetaDir, "meta-dir", defaultMetaDir, "Directory containing META/*.ndjson")
	fs.StringVar(&config.Options.Project, "project", defaultProject, "Project label")
	fs.StringVar(&config.Options.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path copied onto vertex data, for example EllrottLab-GDC_Data")
	fs.IntVar(&config.Options.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&config.Options.ProgressEvery, "progress-every", 50000, "Emit progress every N input rows")
	fs.IntVar(&config.Options.WriterCount, "writers", 2, "Concurrent writer goroutines")
	fs.IntVar(&config.Options.WorkerCount, "workers", 2, "Concurrent parse/profile workers")
	fs.IntVar(&config.Options.LineQueueSize, "line-queue-size", 1024, "Buffered input lines")
	fs.IntVar(&config.Options.WriteQueueSize, "write-queue-size", 8, "Buffered write batches")
	profileLimits := catalog.DefaultProfileLimits()
	fs.IntVar(&config.Options.CatalogLimits.MaxFields, "max-catalog-fields", profileLimits.MaxFields, "Maximum retained catalog fields per resource")
	fs.IntVar(&config.Options.CatalogLimits.MaxDistinctValuesPerField, "max-distinct-values", profileLimits.MaxDistinctValuesPerField, "Maximum retained distinct values per field")
	fs.IntVar(&config.Options.CatalogLimits.MaxDistinctValueBytes, "max-distinct-value-bytes", profileLimits.MaxDistinctValueBytes, "Maximum retained bytes per catalog value")
	fs.IntVar(&config.Options.CatalogLimits.MaxPivotColumnsPerField, "max-pivot-columns", profileLimits.MaxPivotColumnsPerField, "Maximum retained pivot columns per field")
	fs.IntVar(&config.Options.CatalogLimits.MaxExtensionValuesPerField, "max-extension-values", profileLimits.MaxExtensionValuesPerField, "Maximum retained extension observations per field")
	fs.IntVar(&config.Options.CatalogLimits.MaxShapePlans, "max-shape-plans", profileLimits.MaxShapePlans, "Maximum cached payload shape plans")
	fs.BoolVar(&config.Options.FailFast, "fail-fast", false, "Stop on the first decode, validation, or edge conversion error")
	fs.BoolVar(&config.Options.UseGeneric, "use-generic", false, "Use the generic jsonschema + jsonschemagraph validator and extractor")
	fs.StringVar(&config.Options.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	fs.StringVar(&config.Generation, "generation", "", "Required opaque immutable dataset generation identifier")
}

func (config *loadCommandConfig) parseMemoryLimit() error {
	bytes, err := parseMemoryBytes(config.MemoryLimit)
	if err != nil {
		return fmt.Errorf("invalid --memory-limit: %w", err)
	}
	config.MemoryBytes = bytes
	return nil
}

func runConfiguredLoad(ctx context.Context, config loadCommandConfig) error {
	summary, err := runLoadWithProfiles(ctx, config)
	if err != nil {
		return err
	}
	return printJSON(summary)
}

func runLoadWithProfiles(ctx context.Context, config loadCommandConfig) (summary ingest.LoadSummary, err error) {
	previousMemoryLimit := int64(-1)
	if config.MemoryBytes > 0 {
		previousMemoryLimit = debug.SetMemoryLimit(config.MemoryBytes)
	}
	if previousMemoryLimit >= 0 {
		defer debug.SetMemoryLimit(previousMemoryLimit)
	}
	stopProfiles, profileErr := startProfiles(config.CPUProfile, config.MemProfile, config.TraceProfile, config.BlockProfile)
	if profileErr != nil {
		return ingest.LoadSummary{}, profileErr
	}
	defer func() {
		if stopErr := stopProfiles(); stopErr != nil && err == nil {
			err = stopErr
		}
	}()
	return ingest.Load(ctx, config.Options)
}

func parseMemoryBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	lower := strings.ToLower(raw)
	units := []struct {
		suffix string
		factor float64
	}{
		{suffix: "gib", factor: 1 << 30},
		{suffix: "gb", factor: 1e9},
		{suffix: "gi", factor: 1 << 30},
		{suffix: "g", factor: 1e9},
		{suffix: "mib", factor: 1 << 20},
		{suffix: "mb", factor: 1e6},
		{suffix: "mi", factor: 1 << 20},
		{suffix: "m", factor: 1e6},
		{suffix: "kib", factor: 1 << 10},
		{suffix: "kb", factor: 1e3},
		{suffix: "ki", factor: 1 << 10},
		{suffix: "k", factor: 1e3},
		{suffix: "b", factor: 1},
	}
	number := lower
	factor := float64(1)
	for _, unit := range units {
		if strings.HasSuffix(lower, unit.suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(lower, unit.suffix))
			factor = unit.factor
			break
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value <= 0 || value*factor > math.MaxInt64 {
		return 0, fmt.Errorf("expected a positive byte value such as 4GiB")
	}
	return int64(value * factor), nil
}

func runDiscoverPopulatedReferences(ctx context.Context, args []string) error {
	opts, connection, err := parseDiscoverPopulatedReferenceOptions(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	adapter, err := catalogarango.New(client)
	if err != nil {
		return err
	}
	results, err := adapter.DiscoverReferences(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
}

func runDiscoverPopulatedFields(ctx context.Context, args []string) error {
	opts, connection, err := parseDiscoverPopulatedFieldOptions(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	adapter, err := catalogarango.New(client)
	if err != nil {
		return err
	}
	results, err := adapter.DiscoverFields(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
}

func runRebuildRelationshipCatalog(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rebuild-relationship-catalog", flag.ExitOnError)
	opts := catalog.RelationshipRebuildOptions{}
	connection := arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}
	fs.StringVar(&connection.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&connection.Database, "database", defaultDatabase, "Arango database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation; empty selects the legacy namespace")
	fs.StringVar(&opts.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	fs.IntVar(&opts.BatchSize, "batch-size", 1000, "Catalog write batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	adapter, err := catalogarango.New(client)
	if err != nil {
		return err
	}
	summary, err := adapter.RebuildRelationshipCatalog(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(summary)
}

func runAuditRelationshipEdges(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("audit-relationship-edges", flag.ExitOnError)
	opts := catalog.RelationshipAuditOptions{}
	connection := arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}
	fs.StringVar(&connection.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&connection.Database, "database", defaultDatabase, "Arango database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation; empty selects the legacy namespace")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := auditRelationshipEdges(ctx, connection, opts)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func auditRelationshipEdges(ctx context.Context, connection arangostore.ConnectionOptions, opts catalog.RelationshipAuditOptions) (catalog.RelationshipAuditSummary, error) {
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return catalog.RelationshipAuditSummary{}, err
	}
	defer client.Close(ctx)
	adapter, err := catalogarango.New(client)
	if err != nil {
		return catalog.RelationshipAuditSummary{}, err
	}
	return adapter.AuditRelationshipEdges(ctx, opts)
}

type repairCommandConfig struct {
	Load             loadCommandConfig
	SourceGeneration string
	Activate         bool
}

type repairGenerationReport struct {
	Project          string                           `json:"project"`
	SourceGeneration string                           `json:"source_generation"`
	TargetGeneration string                           `json:"target_generation"`
	SourceAudit      catalog.RelationshipAuditSummary `json:"source_audit"`
	Load             ingest.LoadSummary               `json:"load"`
	TargetAudit      catalog.RelationshipAuditSummary `json:"target_audit"`
	Activated        bool                             `json:"activated"`
}

type activateCommandConfig struct {
	Connection arangostore.ConnectionOptions
	Project    string
	Generation string
}

type activateGenerationReport struct {
	Project     string                           `json:"project"`
	Generation  string                           `json:"generation"`
	TargetAudit catalog.RelationshipAuditSummary `json:"target_audit"`
	Activated   bool                             `json:"activated"`
}

func parseRepairCommand(args []string, errorHandling flag.ErrorHandling) (repairCommandConfig, error) {
	fs := flag.NewFlagSet("repair-generation", errorHandling)
	config := repairCommandConfig{}
	configureLoadFlags(fs, &config.Load)
	fs.StringVar(&config.SourceGeneration, "source-generation", "", "Existing generation to audit and replace")
	fs.BoolVar(&config.Activate, "activate", false, "Activate the repaired generation after validation")
	if err := fs.Parse(args); err != nil {
		return repairCommandConfig{}, err
	}
	if err := config.Load.parseMemoryLimit(); err != nil {
		return repairCommandConfig{}, err
	}
	if config.SourceGeneration == "" {
		return repairCommandConfig{}, fmt.Errorf("--source-generation is required for repair-generation")
	}
	if config.Load.Generation == "" {
		return repairCommandConfig{}, fmt.Errorf("--generation is required for repair-generation")
	}
	if config.SourceGeneration == config.Load.Generation {
		return repairCommandConfig{}, fmt.Errorf("--generation must differ from --source-generation")
	}
	if _, err := publication.NewRef(config.Load.Options.Project, config.SourceGeneration); err != nil {
		return repairCommandConfig{}, fmt.Errorf("invalid --source-generation: %w", err)
	}
	ref, err := publication.NewRef(config.Load.Options.Project, config.Load.Generation)
	if err != nil {
		return repairCommandConfig{}, fmt.Errorf("invalid --generation for repair-generation: %w", err)
	}
	config.Load.Options.Dataset = &ref
	config.Load.Options.StageOnly = true
	return config, nil
}

func runRepairGeneration(ctx context.Context, args []string) error {
	config, err := parseRepairCommand(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	connection := arangostore.ConnectionOptions{URL: config.Load.Options.URL, Database: config.Load.Options.Database}
	sourceAudit, err := auditRelationshipEdges(ctx, connection, catalog.RelationshipAuditOptions{Project: config.Load.Options.Project, DatasetGeneration: config.SourceGeneration})
	if err != nil {
		return fmt.Errorf("audit source generation: %w", err)
	}
	loadSummary, err := runLoadWithProfiles(ctx, config.Load)
	if err != nil {
		return err
	}
	targetAudit, err := auditRelationshipEdges(ctx, connection, catalog.RelationshipAuditOptions{Project: config.Load.Options.Project, DatasetGeneration: config.Load.Generation})
	if err != nil {
		return fmt.Errorf("audit repaired generation: %w", err)
	}
	if targetAudit.InvalidEdgeCount != 0 {
		return fmt.Errorf("repaired generation %s still contains %d invalid edge(s); it remains staged", config.Load.Generation, targetAudit.InvalidEdgeCount)
	}
	activated := false
	if config.Activate {
		if err := activateStagedGeneration(ctx, connection, *config.Load.Options.Dataset); err != nil {
			return fmt.Errorf("activate repaired generation: %w", err)
		}
		activated = true
	}
	return printJSON(repairGenerationReport{Project: config.Load.Options.Project, SourceGeneration: config.SourceGeneration, TargetGeneration: config.Load.Generation, SourceAudit: sourceAudit, Load: loadSummary, TargetAudit: targetAudit, Activated: activated})
}

func parseActivateCommand(args []string, errorHandling flag.ErrorHandling) (activateCommandConfig, error) {
	fs := flag.NewFlagSet("activate-generation", errorHandling)
	config := activateCommandConfig{Connection: arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}}
	fs.StringVar(&config.Connection.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&config.Connection.Database, "database", defaultDatabase, "Arango database")
	fs.StringVar(&config.Project, "project", defaultProject, "Project label")
	fs.StringVar(&config.Generation, "generation", "", "Staged immutable generation to activate")
	if err := fs.Parse(args); err != nil {
		return activateCommandConfig{}, err
	}
	if config.Generation == "" {
		return activateCommandConfig{}, fmt.Errorf("--generation is required for activate-generation")
	}
	if _, err := publication.NewRef(config.Project, config.Generation); err != nil {
		return activateCommandConfig{}, fmt.Errorf("invalid --generation for activate-generation: %w", err)
	}
	return config, nil
}

func runActivateGeneration(ctx context.Context, args []string) error {
	config, err := parseActivateCommand(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	audit, err := auditRelationshipEdges(ctx, config.Connection, catalog.RelationshipAuditOptions{Project: config.Project, DatasetGeneration: config.Generation})
	if err != nil {
		return fmt.Errorf("audit generation before activation: %w", err)
	}
	if audit.InvalidEdgeCount != 0 {
		return fmt.Errorf("generation %s contains %d invalid edge(s); activation refused", config.Generation, audit.InvalidEdgeCount)
	}
	ref, _ := publication.NewRef(config.Project, config.Generation)
	if err := activateStagedGeneration(ctx, config.Connection, ref); err != nil {
		return err
	}
	return printJSON(activateGenerationReport{Project: config.Project, Generation: config.Generation, TargetAudit: audit, Activated: true})
}

func activateStagedGeneration(ctx context.Context, connection arangostore.ConnectionOptions, ref publication.Ref) error {
	client, err := arangostore.Open(ctx, connection.URL, connection.Database)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	lifecycle, err := publicationarango.New(client)
	if err != nil {
		return err
	}
	manifest, err := lifecycle.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	return lifecycle.Activate(ctx, manifest)
}

func parseDiscoverPopulatedReferenceOptions(args []string, errorHandling flag.ErrorHandling) (catalog.PopulatedReferenceOptions, arangostore.ConnectionOptions, error) {
	fs := flag.NewFlagSet("discover-populated-references", errorHandling)
	opts := catalog.PopulatedReferenceOptions{}
	connection := arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}
	fs.StringVar(&connection.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&connection.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation to inspect; empty selects the legacy namespace and never resolves an active generation")
	fs.StringVar(&opts.FromType, "from-type", "", "Optional source collection/resource type filter, for example Patient")
	fs.StringVar(&opts.NodeType, "node-type", "", "Optional builder node/resource type filter, for example Patient")
	fs.StringVar(&opts.Mode, "mode", catalog.TraversalModeStorage, "Traversal discovery mode: storage or builder")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	if err := fs.Parse(args); err != nil {
		return catalog.PopulatedReferenceOptions{}, arangostore.ConnectionOptions{}, err
	}
	return opts, connection, nil
}

func parseDiscoverPopulatedFieldOptions(args []string, errorHandling flag.ErrorHandling) (catalog.PopulatedFieldOptions, arangostore.ConnectionOptions, error) {
	fs := flag.NewFlagSet("discover-populated-fields", errorHandling)
	opts := catalog.PopulatedFieldOptions{}
	connection := arangostore.ConnectionOptions{URL: defaultURL, Database: defaultDatabase}
	fs.StringVar(&connection.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&connection.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation to inspect; empty selects the legacy namespace and never resolves an active generation")
	fs.StringVar(&opts.ResourceType, "resource-type", "", "Optional resource type filter, for example Patient")
	fs.BoolVar(&opts.PivotOnly, "pivot-only", false, "Return only pivot-candidate fields")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	if err := fs.Parse(args); err != nil {
		return catalog.PopulatedFieldOptions{}, arangostore.ConnectionOptions{}, err
	}
	return opts, connection, nil
}

func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  arango-fhir-proto load-generation --generation OPAQUE_ID [flags]  # immutable complete META directory
  arango-fhir-proto discover-populated-references [flags]
  arango-fhir-proto discover-populated-fields [flags]
  arango-fhir-proto audit-relationship-edges [flags]  # read-only invalid endpoint audit
  arango-fhir-proto rebuild-relationship-catalog [flags]  # explicit fhir_edge repair/backfill
  arango-fhir-proto repair-generation [flags]  # stage a corrected immutable generation; --activate is explicit
  arango-fhir-proto activate-generation [flags]  # validate and activate a staged immutable generation
`)
}

func startProfiles(cpuPath, memPath, tracePath, blockPath string) (func() error, error) {
	type closer func() error
	var closers []closer

	if blockPath != "" {
		runtime.SetBlockProfileRate(1)
		closers = append(closers, func() error {
			defer runtime.SetBlockProfileRate(0)
			f, err := os.Create(blockPath)
			if err != nil {
				return err
			}
			defer f.Close()
			return pprof.Lookup("block").WriteTo(f, 0)
		})
	}

	if tracePath != "" {
		f, err := os.Create(tracePath)
		if err != nil {
			return nil, err
		}
		if err := trace.Start(f); err != nil {
			f.Close()
			return nil, err
		}
		closers = append(closers, func() error {
			trace.Stop()
			return f.Close()
		})
	}

	if cpuPath != "" {
		f, err := os.Create(cpuPath)
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return nil, err
		}
		closers = append(closers, func() error {
			pprof.StopCPUProfile()
			return f.Close()
		})
	}

	if memPath != "" {
		closers = append(closers, func() error {
			runtime.GC()
			f, err := os.Create(memPath)
			if err != nil {
				return err
			}
			defer f.Close()
			return pprof.WriteHeapProfile(f)
		})
	}

	return func() error {
		var firstErr error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}, nil
}
