package proto

import (
	"context"
	"os"
	"strings"
	"time"
)

type BenchmarkOptions struct {
	ConnectionOptions
	Schema           string
	MetaDir          string
	Project          string
	AuthResourcePath string
	Output           string
	BatchSize        int
	ProgressEvery    int
	WriterCount      int
	Truncate         bool
	WriteAPI         string
	QueryFile        string
	CursorBatchSize  int
	DatasetName      string
}

type BenchmarkSummary struct {
	Backend            string             `json:"backend"`
	Transport          string             `json:"transport"`
	DatasetName        string             `json:"dataset_name"`
	Project            string             `json:"project"`
	BatchSize          int                `json:"batch_size"`
	WriterCount        int                `json:"writer_count"`
	BootstrapMode      string             `json:"bootstrap_mode"`
	WriteAPI           string             `json:"write_api,omitempty"`
	Load               LoadSummary        `json:"load"`
	LoadSeconds        float64            `json:"load_seconds"`
	PrepareSeconds     float64            `json:"prepare_seconds,omitempty"`
	DataframeSupported bool               `json:"dataframe_supported"`
	DataframeRows      int                `json:"dataframe_rows"`
	DataframeSeconds   float64            `json:"dataframe_seconds"`
	DataframeError     string             `json:"dataframe_error,omitempty"`
	Output             string             `json:"output,omitempty"`
	QueryFile          string             `json:"query_file,omitempty"`
	Comparable         bool               `json:"comparable"`
	Notes              []string           `json:"notes,omitempty"`
	StageSeconds       map[string]float64 `json:"stage_seconds,omitempty"`
}

func Benchmark(ctx context.Context, opts BenchmarkOptions) (BenchmarkSummary, error) {
	if opts.QueryFile == "" {
		opts.QueryFile = DefaultCaseAssayQueryPathForBackend(opts.Backend)
	}
	if opts.DatasetName == "" {
		opts.DatasetName = opts.MetaDir
	}
	summary := BenchmarkSummary{
		Backend:       backendName(opts.Backend),
		Transport:     benchmarkTransport(opts.URL),
		DatasetName:   opts.DatasetName,
		Project:       opts.Project,
		BatchSize:     opts.BatchSize,
		WriterCount:   opts.WriterCount,
		BootstrapMode: benchmarkBootstrapMode(opts.Truncate),
		WriteAPI:      opts.WriteAPI,
		Output:        opts.Output,
		QueryFile:     opts.QueryFile,
	}

	loadStart := time.Now()
	loadSummary, err := Load(ctx, LoadOptions{
		ConnectionOptions: ConnectionOptions{
			Backend:   opts.Backend,
			URL:       opts.URL,
			Namespace: opts.Namespace,
			Database:  opts.Database,
			Username:  opts.Username,
			Password:  opts.Password,
			AuthToken: opts.AuthToken,
		},
		Schema:           opts.Schema,
		MetaDir:          opts.MetaDir,
		Project:          opts.Project,
		AuthResourcePath: opts.AuthResourcePath,
		BatchSize:        opts.BatchSize,
		ProgressEvery:    opts.ProgressEvery,
		WriterCount:      opts.WriterCount,
		Truncate:         opts.Truncate,
		WriteAPI:         opts.WriteAPI,
	})
	if err != nil {
		return summary, err
	}
	summary.Load = loadSummary
	summary.LoadSeconds = time.Since(loadStart).Seconds()
	summary.StageSeconds = loadSummary.StageSeconds
	if backendName(opts.Backend) == backendSurreal {
		prepareStart := time.Now()
		_, err := PrepareGDCCaseAssayMatrix(ctx, PrepareCaseAssayOptions{
			ConnectionOptions: ConnectionOptions{
				Backend:   opts.Backend,
				URL:       opts.URL,
				Namespace: opts.Namespace,
				Database:  opts.Database,
				Username:  opts.Username,
				Password:  opts.Password,
				AuthToken: opts.AuthToken,
			},
			Project:          opts.Project,
			AuthResourcePath: opts.AuthResourcePath,
			BatchSize:        opts.BatchSize,
			ProgressEvery:    opts.ProgressEvery,
			Truncate:         true,
		})
		if err != nil {
			summary.Notes = append(summary.Notes, "prepare step failed; run is not comparable yet")
			return summary, nil
		}
		summary.PrepareSeconds = time.Since(prepareStart).Seconds()
	}

	if _, err := os.Stat(opts.QueryFile); err != nil {
		summary.Notes = append(summary.Notes, "query file not found for backend; dataframe benchmark skipped")
		return summary, nil
	}

	outputPath := opts.Output
	if outputPath == "" {
		tmpOut, err := os.CreateTemp("", "arango-fhir-proto-benchmark-*.ndjson")
		if err != nil {
			return summary, err
		}
		outputPath = tmpOut.Name()
		tmpOut.Close()
		defer os.Remove(outputPath)
	}

	queryStart := time.Now()
	rows, err := Query(ctx, QueryOptions{
		ConnectionOptions: ConnectionOptions{
			Backend:   opts.Backend,
			URL:       opts.URL,
			Namespace: opts.Namespace,
			Database:  opts.Database,
			Username:  opts.Username,
			Password:  opts.Password,
			AuthToken: opts.AuthToken,
		},
		QueryFile:        opts.QueryFile,
		Output:           outputPath,
		Index:            DefaultBulkIndex(),
		Project:          opts.Project,
		AuthResourcePath: opts.AuthResourcePath,
		BatchSize:        opts.CursorBatchSize,
		ProgressEvery:    opts.ProgressEvery,
		Bulk:             false,
	})
	if err != nil {
		summary.DataframeError = err.Error()
		summary.Notes = append(summary.Notes, "dataframe benchmark failed; run is not comparable yet")
		return summary, nil
	}

	summary.DataframeSupported = true
	summary.DataframeRows = rows
	summary.DataframeSeconds = time.Since(queryStart).Seconds()
	summary.Comparable = true
	return summary, nil
}

func benchmarkTransport(rawURL string) string {
	rawURL = strings.TrimSpace(strings.ToLower(rawURL))
	switch {
	case strings.HasPrefix(rawURL, "ws://"), strings.HasPrefix(rawURL, "wss://"):
		return "websocket"
	case strings.HasPrefix(rawURL, "http://"), strings.HasPrefix(rawURL, "https://"):
		return "http"
	case rawURL == "":
		return "default"
	default:
		return "custom"
	}
}

func benchmarkBootstrapMode(truncate bool) string {
	if truncate {
		return "truncate_records_keep_schema"
	}
	return "reuse_existing_data"
}
