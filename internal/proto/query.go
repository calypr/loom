package proto

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type QueryOptions struct {
	ConnectionOptions
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
	client, err := openBackend(ctx, opts.ConnectionOptions)
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
	Emit("go_query_start", map[string]any{
		"query":              opts.QueryFile,
		"output":             opts.Output,
		"bulk":               opts.Bulk,
		"cursor_batch_size":  opts.BatchSize,
		"auth_resource_path": opts.AuthResourcePath,
	})
	switch backendName(opts.Backend) {
	case backendSurreal:
		bindVars := map[string]interface{}{
			"project":            opts.Project,
			"max_rows":           opts.MaxRows,
			"patient_key":        opts.PatientKey,
			"auth_resource_path": opts.AuthResourcePath,
		}
		err = client.QueryRows(ctx, string(queryBytes), opts.BatchSize, bindVars, func(row map[string]any) error {
			return visitQueryRow(writer, opts, row, &rows, start)
		})
	default:
		bindVars := map[string]interface{}{
			"project": opts.Project,
		}
		if opts.AuthResourcePath != "" {
			bindVars["auth_resource_path"] = opts.AuthResourcePath
		} else {
			bindVars["auth_resource_path"] = nil
		}
		err = client.QueryRows(ctx, string(queryBytes), opts.BatchSize, bindVars, func(row map[string]any) error {
			return visitQueryRow(writer, opts, row, &rows, start)
		})
	}
	if _, ok := err.(stopQuery); ok {
		err = nil
	}
	if err != nil {
		return rows, err
	}
	Emit("go_query_complete", map[string]any{"rows": rows, "seconds": SecondsSince(start), "output": opts.Output})
	return rows, nil
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
		Emit("go_query_progress", map[string]any{"rows": *rows, "seconds": SecondsSince(start)})
	}
	return nil
}

type stopQuery struct{}

func (stopQuery) Error() string { return "stop query" }

func writeJSONLine(writer *bufio.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.WriteByte('\n'); err != nil {
		return err
	}
	return nil
}

func DefaultCaseAssayQueryPath() string {
	return DefaultCaseAssayQueryPathForBackend(backendArango)
}

func DefaultCaseAssayQueryPathForBackend(backend string) string {
	switch backendName(backend) {
	case backendSurreal:
		return "queries_surreal/gdc_case_assay_matrix_surreal_rows.surql"
	default:
		return "queries/gdc_case_assay_matrix_arango_rows.aql"
	}
}

func DefaultBulkIndex() string {
	return fmt.Sprintf("gdc_case_assay_matrix")
}
