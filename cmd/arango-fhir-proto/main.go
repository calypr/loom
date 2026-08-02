package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/ingest"
	publication "github.com/calypr/loom/internal/publication"
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
	case "load":
		err = runLoad(ctx, os.Args[2:])
	case "load-generation":
		err = runLoadGeneration(ctx, os.Args[2:])
	case "discover-populated-references":
		err = runDiscoverPopulatedReferences(ctx, os.Args[2:])
	case "discover-populated-fields":
		err = runDiscoverPopulatedFields(ctx, os.Args[2:])
	case "rebuild-relationship-catalog":
		err = runRebuildRelationshipCatalog(ctx, os.Args[2:])
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
	Backend string

	CPUProfile   string
	MemProfile   string
	TraceProfile string
	BlockProfile string

	Generation string
}

func runLoad(ctx context.Context, args []string) error {
	return runLoadCommand(ctx, args, false)
}

func runLoadGeneration(ctx context.Context, args []string) error {
	return runLoadCommand(ctx, args, true)
}

func runLoadCommand(ctx context.Context, args []string, generationMode bool) error {
	config, err := parseLoadCommand(args, generationMode, flag.ExitOnError)
	if err != nil {
		return err
	}
	return runConfiguredLoad(ctx, config)
}

func parseLoadCommand(args []string, generationMode bool, errorHandling flag.ErrorHandling) (loadCommandConfig, error) {
	name := "load"
	if generationMode {
		name = "load-generation"
	}
	fs := flag.NewFlagSet(name, errorHandling)
	config := loadCommandConfig{}
	configureLoadFlags(fs, &config, generationMode)
	if err := fs.Parse(args); err != nil {
		return loadCommandConfig{}, err
	}
	if config.Backend != "arango" {
		return loadCommandConfig{}, fmt.Errorf("unsupported backend %q: only arango is supported", config.Backend)
	}
	if !generationMode {
		return config, nil
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

func configureLoadFlags(fs *flag.FlagSet, config *loadCommandConfig, generationMode bool) {
	fs.StringVar(&config.Backend, "backend", "arango", "Storage backend; only arango is supported")
	fs.StringVar(&config.CPUProfile, "cpu-profile", "", "Write CPU profile to file")
	fs.StringVar(&config.MemProfile, "mem-profile", "", "Write heap profile to file at end of run")
	fs.StringVar(&config.TraceProfile, "trace-profile", "", "Write runtime trace to file")
	fs.StringVar(&config.BlockProfile, "block-profile", "", "Write block profile to file at end of run")
	fs.StringVar(&config.Options.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&config.Options.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&config.Options.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.StringVar(&config.Options.MetaDir, "meta-dir", defaultMetaDir, "Directory containing META/*.ndjson")
	fs.StringVar(&config.Options.Project, "project", defaultProject, "Project label")
	fs.StringVar(&config.Options.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path copied onto vertex data, for example EllrottLab-GDC_Data")
	fs.IntVar(&config.Options.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&config.Options.ProgressEvery, "progress-every", 50000, "Emit progress every N input rows")
	fs.IntVar(&config.Options.WriterCount, "writers", 8, "Concurrent writer goroutines")
	if !generationMode {
		fs.BoolVar(&config.Options.Truncate, "truncate", true, "Truncate prototype collections before loading")
	}
	fs.BoolVar(&config.Options.FailFast, "fail-fast", false, "Stop on the first decode, validation, or edge conversion error")
	fs.BoolVar(&config.Options.UseGeneric, "use-generic", false, "Use the generic jsonschema + jsonschemagraph validator and extractor")
	fs.StringVar(&config.Options.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	if generationMode {
		fs.StringVar(&config.Generation, "generation", "", "Required opaque immutable dataset generation identifier")
	}
}

func runConfiguredLoad(ctx context.Context, config loadCommandConfig) error {
	stopProfiles, profileErr := startProfiles(config.CPUProfile, config.MemProfile, config.TraceProfile, config.BlockProfile)
	if profileErr != nil {
		return profileErr
	}
	var deferredErr error
	defer func() {
		if stopErr := stopProfiles(); stopErr != nil && deferredErr == nil {
			deferredErr = stopErr
		}
	}()
	summary, err := ingest.Load(ctx, config.Options)
	if err != nil {
		return err
	}
	if deferredErr != nil {
		return deferredErr
	}
	return printJSON(summary)
}

func runDiscoverPopulatedReferences(ctx context.Context, args []string) error {
	opts, err := parseDiscoverPopulatedReferenceOptions(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	results, err := catalog.DiscoverPopulatedReferences(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
}

func runDiscoverPopulatedFields(ctx context.Context, args []string) error {
	opts, err := parseDiscoverPopulatedFieldOptions(args, flag.ExitOnError)
	if err != nil {
		return err
	}
	results, err := catalog.DiscoverPopulatedFields(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
}

func runRebuildRelationshipCatalog(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rebuild-relationship-catalog", flag.ExitOnError)
	opts := catalog.RelationshipRebuildOptions{}
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Arango database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation; empty selects the legacy namespace")
	fs.StringVar(&opts.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	fs.IntVar(&opts.BatchSize, "batch-size", 1000, "Catalog write batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	summary, err := catalog.RebuildRelationshipCatalog(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(summary)
}

func parseDiscoverPopulatedReferenceOptions(args []string, errorHandling flag.ErrorHandling) (catalog.PopulatedReferenceOptions, error) {
	fs := flag.NewFlagSet("discover-populated-references", errorHandling)
	opts := catalog.PopulatedReferenceOptions{}
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation to inspect; empty selects the legacy namespace and never resolves an active generation")
	fs.StringVar(&opts.FromType, "from-type", "", "Optional source collection/resource type filter, for example Patient")
	fs.StringVar(&opts.NodeType, "node-type", "", "Optional builder node/resource type filter, for example Patient")
	fs.StringVar(&opts.Mode, "mode", catalog.TraversalModeStorage, "Traversal discovery mode: storage or builder")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	if err := fs.Parse(args); err != nil {
		return catalog.PopulatedReferenceOptions{}, err
	}
	return opts, nil
}

func parseDiscoverPopulatedFieldOptions(args []string, errorHandling flag.ErrorHandling) (catalog.PopulatedFieldOptions, error) {
	fs := flag.NewFlagSet("discover-populated-fields", errorHandling)
	opts := catalog.PopulatedFieldOptions{}
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.DatasetGeneration, "dataset-generation", "", "Optional generation to inspect; empty selects the legacy namespace and never resolves an active generation")
	fs.StringVar(&opts.ResourceType, "resource-type", "", "Optional resource type filter, for example Patient")
	fs.BoolVar(&opts.PivotOnly, "pivot-only", false, "Return only pivot-candidate fields")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Query cursor batch size")
	if err := fs.Parse(args); err != nil {
		return catalog.PopulatedFieldOptions{}, err
	}
	return opts, nil
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
  arango-fhir-proto load [flags]  # legacy mutable import; default --truncate=true
  arango-fhir-proto load-generation --generation OPAQUE_ID [flags]  # immutable complete META directory; no --truncate flag
  arango-fhir-proto discover-populated-references [flags]
  arango-fhir-proto discover-populated-fields [flags]
  arango-fhir-proto rebuild-relationship-catalog [flags]  # explicit fhir_edge repair/backfill
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
