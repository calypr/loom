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

	"arangodb-proto/internal/proto"
)

const (
	defaultBackend  = "arango"
	defaultURL      = "http://127.0.0.1:8529"
	defaultNS       = "fhir_proto"
	defaultDatabase = "fhir_proto"
	defaultProject  = "ARANGODB_PROTO"
	defaultSchema   = "/Users/peterkor/Desktop/BMEG/iceberg/schemas/graph/graph-fhir.json"
	defaultMetaDir  = "/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/META"
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
	case "query-gdc-case-assay-matrix":
		err = runQuery(ctx, os.Args[2:], false)
	case "export-gdc-case-assay-matrix":
		err = runQuery(ctx, os.Args[2:], true)
	case "discover-populated-references":
		err = runDiscoverPopulatedReferences(ctx, os.Args[2:])
	case "discover-populated-fields":
		err = runDiscoverPopulatedFields(ctx, os.Args[2:])
	case "prepare-gdc-case-assay-matrix":
		err = runPrepareCaseAssayMatrix(ctx, os.Args[2:])
	case "benchmark":
		err = runBenchmark(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runLoad(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	opts := proto.LoadOptions{}
	cpuProfile := fs.String("cpu-profile", "", "Write CPU profile to file")
	memProfile := fs.String("mem-profile", "", "Write heap profile to file at end of run")
	traceProfile := fs.String("trace-profile", "", "Write runtime trace to file")
	blockProfile := fs.String("block-profile", "", "Write block profile to file at end of run")
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.StringVar(&opts.MetaDir, "meta-dir", defaultMetaDir, "Directory containing META/*.ndjson")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path copied onto vertex data, for example EllrottLab-GDC_Data")
	fs.IntVar(&opts.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&opts.ProgressEvery, "progress-every", 50000, "Emit progress every N input rows")
	fs.IntVar(&opts.WriterCount, "writers", 8, "Concurrent writer goroutines")
	fs.BoolVar(&opts.Truncate, "truncate", true, "Truncate prototype collections before loading")
	fs.BoolVar(&opts.FailFast, "fail-fast", false, "Stop on the first decode, validation, or edge conversion error")
	fs.BoolVar(&opts.UseGeneric, "use-generic", false, "Use the generic jsonschema + jsonschemagraph validator and extractor")
	fs.StringVar(&opts.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	stopProfiles, profileErr := startProfiles(*cpuProfile, *memProfile, *traceProfile, *blockProfile)
	if profileErr != nil {
		return profileErr
	}
	var deferredErr error
	defer func() {
		if stopErr := stopProfiles(); stopErr != nil && deferredErr == nil {
			deferredErr = stopErr
		}
	}()
	summary, err := proto.Load(ctx, opts)
	if err != nil {
		return err
	}
	if deferredErr != nil {
		return deferredErr
	}
	return printJSON(summary)
}

func runQuery(ctx context.Context, args []string, bulk bool) error {
	name := "query-gdc-case-assay-matrix"
	if bulk {
		name = "export-gdc-case-assay-matrix"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	opts := proto.QueryOptions{Bulk: bulk}
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path used to scope dataframe/export queries, for example EllrottLab-GDC_Data")
	fs.StringVar(&opts.PatientKey, "patient-key", "", "Optional patient _key bind var for backend-specific probe queries")
	fs.StringVar(&opts.QueryFile, "query", "", "Backend-specific query file; defaults to the case/assay query for the selected backend")
	fs.StringVar(&opts.Output, "output", "", "Output path; defaults to stdout")
	fs.StringVar(&opts.Index, "index", proto.DefaultBulkIndex(), "Elasticsearch bulk target index")
	fs.IntVar(&opts.BatchSize, "cursor-batch-size", 1000, "Arango cursor batch size")
	fs.IntVar(&opts.ProgressEvery, "progress-every", 50000, "Emit progress every N rows")
	fs.IntVar(&opts.MaxRows, "max-rows", 0, "Stop after N output rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.QueryFile == "" {
		opts.QueryFile = proto.DefaultCaseAssayQueryPathForBackend(opts.Backend)
	}
	if bulk && opts.Output == "" {
		return fmt.Errorf("--output is required for %s", name)
	}
	rows, err := proto.Query(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"step": name, "rows": rows, "output": opts.Output})
}

func runBenchmark(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	opts := proto.BenchmarkOptions{}
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.StringVar(&opts.MetaDir, "meta-dir", defaultMetaDir, "Directory containing META/*.ndjson")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path copied onto loaded records and used in dataframe queries")
	fs.StringVar(&opts.QueryFile, "query", "", "Backend-specific dataframe query file")
	fs.StringVar(&opts.DatasetName, "dataset-name", "", "Optional benchmark dataset label")
	fs.StringVar(&opts.Output, "output", "", "Optional path to keep the benchmark dataframe output; defaults to a temporary file")
	fs.IntVar(&opts.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&opts.CursorBatchSize, "cursor-batch-size", 1000, "Query cursor batch size")
	fs.IntVar(&opts.ProgressEvery, "progress-every", 50000, "Emit progress every N rows")
	fs.IntVar(&opts.WriterCount, "writers", 8, "Concurrent writer goroutines")
	fs.BoolVar(&opts.Truncate, "truncate", true, "Truncate prototype collections before benchmarking")
	fs.StringVar(&opts.WriteAPI, "write-api", "import", "Bulk write API: import or document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.QueryFile == "" {
		opts.QueryFile = proto.DefaultCaseAssayQueryPathForBackend(opts.Backend)
	}
	summary, err := proto.Benchmark(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(summary)
}

func runPrepareCaseAssayMatrix(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prepare-gdc-case-assay-matrix", flag.ExitOnError)
	opts := proto.PrepareCaseAssayOptions{}
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.AuthResourcePath, "auth-resource-path", "", "Optional auth resource path used to scope prepare work")
	fs.IntVar(&opts.BatchSize, "batch-size", 1000, "Bulk insert batch size for helper rows")
	fs.IntVar(&opts.ProgressEvery, "progress-every", 5000, "Emit progress every N prepared rows")
	fs.BoolVar(&opts.Truncate, "truncate", true, "Truncate helper collections before preparing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	summary, err := proto.PrepareGDCCaseAssayMatrix(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(summary)
}

func runDiscoverPopulatedReferences(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("discover-populated-references", flag.ExitOnError)
	opts := proto.PopulatedReferenceOptions{}
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.FromType, "from-type", "", "Optional source collection/resource type filter, for example Patient")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Arango cursor batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	results, err := proto.DiscoverPopulatedReferences(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
}

func runDiscoverPopulatedFields(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("discover-populated-fields", flag.ExitOnError)
	opts := proto.PopulatedFieldOptions{}
	fs.StringVar(&opts.Backend, "backend", defaultBackend, "Backend: arango or surreal")
	fs.StringVar(&opts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&opts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&opts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&opts.Username, "username", "root", "SurrealDB username")
	fs.StringVar(&opts.Password, "password", "root", "SurrealDB password")
	fs.StringVar(&opts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
	fs.StringVar(&opts.Project, "project", defaultProject, "Project label")
	fs.StringVar(&opts.ResourceType, "resource-type", "", "Optional resource type filter, for example Patient")
	fs.BoolVar(&opts.PivotOnly, "pivot-only", false, "Return only pivot-candidate fields")
	fs.IntVar(&opts.CursorBatch, "cursor-batch-size", 1000, "Arango cursor batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	results, err := proto.DiscoverPopulatedFields(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(results)
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
  arango-fhir-proto load [flags]
  arango-fhir-proto query-gdc-case-assay-matrix [flags]
  arango-fhir-proto export-gdc-case-assay-matrix --output FILE [flags]
  arango-fhir-proto discover-populated-references [flags]
  arango-fhir-proto discover-populated-fields [flags]
  arango-fhir-proto prepare-gdc-case-assay-matrix [flags]
  arango-fhir-proto benchmark [flags]
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
