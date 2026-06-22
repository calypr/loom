package querysvc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"arangodb-proto/internal/dbio"
)

type QueryOptions struct {
	dbio.ConnectionOptions
	QueryFile        string
	Output           string
	Index            string
	Project          string
	AuthResourcePath string
	PatientKey       string
	BatchSize        int
	ProgressEvery    int
	MaxRows          int
	Bulk             bool
}

type ExecuteQueryOptions struct {
	dbio.ConnectionOptions
	BatchSize int
}

func Query(ctx context.Context, opts QueryOptions) (int, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 50000
	}
	if opts.QueryFile == "" {
		opts.QueryFile = DefaultCaseAssayQueryPathForBackend(opts.Backend)
	}
	queryBytes, err := os.ReadFile(opts.QueryFile)
	if err != nil {
		return 0, err
	}
	var out *os.File
	if opts.Output != "" {
		out, err = os.Create(opts.Output)
		if err != nil {
			return 0, err
		}
		defer out.Close()
	} else {
		out = os.Stdout
	}
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	start := time.Now()
	rows := 0
	emit("go_query_start", map[string]any{
		"query":              opts.QueryFile,
		"output":             opts.Output,
		"bulk":               opts.Bulk,
		"cursor_batch_size":  opts.BatchSize,
		"auth_resource_path": opts.AuthResourcePath,
	})
	bindVars := queryBindVars(opts)
	err = ExecuteQueryRows(ctx, ExecuteQueryOptions{
		ConnectionOptions: opts.ConnectionOptions,
		BatchSize:         opts.BatchSize,
	}, string(queryBytes), bindVars, func(row map[string]any) error {
		return visitQueryRow(writer, opts, row, &rows, start)
	})
	if _, ok := err.(stopQuery); ok {
		err = nil
	}
	if err != nil {
		return rows, err
	}
	emit("go_query_complete", map[string]any{"rows": rows, "seconds": secondsSince(start), "output": opts.Output})
	return rows, nil
}

func ExecuteQueryRows(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	client, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	return client.QueryRows(ctx, query, opts.BatchSize, bindVars, func(row map[string]any) error {
		return visit(row)
	})
}

func queryBindVars(opts QueryOptions) map[string]any {
	switch dbio.BackendName(opts.Backend) {
	case dbio.BackendSurreal, dbio.BackendPostgres:
		return map[string]any{
			"project":            opts.Project,
			"max_rows":           opts.MaxRows,
			"patient_key":        opts.PatientKey,
			"auth_resource_path": opts.AuthResourcePath,
		}
	default:
		authPaths := []string(nil)
		if opts.AuthResourcePath != "" {
			authPaths = []string{opts.AuthResourcePath}
		}
		bindVars := map[string]any{
			"project":                          opts.Project,
			"auth_resource_paths":              authPaths,
			"auth_resource_paths_unrestricted": authPaths == nil,
		}
		if opts.AuthResourcePath != "" {
			bindVars["auth_resource_path"] = opts.AuthResourcePath
		} else {
			bindVars["auth_resource_path"] = nil
		}
		return bindVars
	}
}

func visitQueryRow(writer *bufio.Writer, opts QueryOptions, row map[string]any, rows *int, start time.Time) error {
	if opts.MaxRows > 0 && *rows >= opts.MaxRows {
		return stopQuery{}
	}
	*rows++
	if opts.Bulk {
		rowID := row["_key"]
		if rowID == nil {
			rowID = row["case_fhir_id"]
		}
		delete(row, "_key")
		delete(row, "_id")
		delete(row, "_rev")
		meta := map[string]any{"index": map[string]any{"_index": opts.Index, "_id": rowID}}
		if err := writeJSONLine(writer, meta); err != nil {
			return err
		}
	}
	if err := writeJSONLine(writer, row); err != nil {
		return err
	}
	if *rows%opts.ProgressEvery == 0 {
		emit("go_query_progress", map[string]any{"rows": *rows, "seconds": secondsSince(start)})
	}
	return nil
}

type stopQuery struct{}

func (stopQuery) Error() string { return "stop query" }

func DefaultCaseAssayQueryPath() string {
	return DefaultCaseAssayQueryPathForBackend(dbio.BackendArango)
}

func DefaultCaseAssayQueryPathForBackend(backend string) string {
	switch dbio.BackendName(backend) {
	case dbio.BackendPostgres:
		return "experimental/queries/postgres/gdc_case_assay_matrix_postgres_rows.sql"
	case dbio.BackendSurreal:
		return "experimental/queries/surreal/gdc_case_assay_matrix_surreal_rows.surql"
	default:
		return "queries/gdc_case_assay_matrix_arango_rows.aql"
	}
}

func DefaultBulkIndex() string {
	return fmt.Sprintf("gdc_case_assay_matrix")
}
