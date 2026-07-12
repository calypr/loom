// Package export serializes flat dataframe rows to portable export formats.
//
// It deliberately owns only row encoding. Query execution, artifact storage,
// delivery transports, and job management stay outside this package so every
// destination can consume the same flat-row contract.
package export

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// RowVisitor receives one flat dataframe row. Returning an error asks the
// producer to stop iteration.
type RowVisitor func(map[string]any) error

// RowStream emits rows without materializing the dataframe in this package.
//
// A stream must respect an error returned by RowVisitor. EncodeCSV calls a
// stream once when CSVOptions.Columns is supplied and twice otherwise: once
// to discover the deterministic column union and once to write records. For
// that mode, callers must provide a replayable stream that returns the same
// logical rows in the same order for each invocation (for example, re-running
// a stable compiled query).
type RowStream func(context.Context, RowVisitor) error

// Result describes encoder progress. On success it describes the complete
// export. If an encoder returns an error, Rows and Bytes describe only the
// progress observed before that error. Columns is always in output order.
type Result struct {
	Rows    int64
	Bytes   int64
	Columns []string
}

// CSVOptions controls CSV schema selection. A non-empty Columns slice is the
// exact header and field order to write. Rows containing a column outside the
// configured schema are rejected rather than silently discarded. With no
// columns, EncodeCSV discovers the union of all row keys and sorts it
// lexicographically.
type CSVOptions struct {
	Columns []string
}

// ValueError reports a value that cannot be represented as a flat dataframe
// scalar. Maps, slices, structs, pointers, and non-finite numbers are
// intentionally rejected instead of being stringified or nested in output.
type ValueError struct {
	Row       int64
	Column    string
	ValueType string
	Reason    string
}

func (e *ValueError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("row %d column %q has unsupported value of type %s: %s", e.Row, e.Column, e.ValueType, e.Reason)
	}
	return fmt.Sprintf("row %d column %q has unsupported nested value of type %s; export rows must contain scalar values", e.Row, e.Column, e.ValueType)
}

// ColumnError reports an invalid CSV schema or a row field that cannot be
// represented by that schema. Row is zero for a configured-header error.
type ColumnError struct {
	Row    int64
	Column string
	Reason string
}

func (e *ColumnError) Error() string {
	if e.Row == 0 {
		return fmt.Sprintf("CSV column %q %s", e.Column, e.Reason)
	}
	return fmt.Sprintf("row %d column %q %s", e.Row, e.Column, e.Reason)
}

// EncodeNDJSON writes one JSON object and exactly one trailing newline for
// every row in stream. It retains at most one encoded row plus the observed
// column set, never the complete dataframe.
func EncodeNDJSON(ctx context.Context, dst io.Writer, stream RowStream) (Result, error) {
	if dst == nil {
		return Result{}, fmt.Errorf("NDJSON destination writer is required")
	}

	writer := &countingWriter{dst: dst}
	columns := make(map[string]struct{})
	var result Result
	err := visitRows(ctx, stream, func(row map[string]any) error {
		rowNumber := result.Rows + 1
		keys, err := validateRow(row, rowNumber)
		if err != nil {
			return err
		}
		for _, key := range keys {
			columns[key] = struct{}{}
		}

		encoded, err := marshalRow(row)
		if err != nil {
			return fmt.Errorf("row %d: encode NDJSON: %w", rowNumber, err)
		}
		encoded = append(encoded, '\n')
		if _, err := writeAll(writer, encoded); err != nil {
			return fmt.Errorf("row %d: write NDJSON: %w", rowNumber, err)
		}
		result.Rows++
		return nil
	})
	result.Bytes = writer.n
	result.Columns = sortedColumns(columns)
	if err != nil {
		return result, err
	}
	return result, nil
}

// EncodeCSV writes a CSV header followed by one record for each row. It uses
// encoding/csv for RFC 4180-compatible escaping. When Columns is omitted the
// source is scanned once to discover a sorted column union, then replayed to
// encode rows; this keeps memory proportional to the number of columns rather
// than the number of rows.
func EncodeCSV(ctx context.Context, dst io.Writer, options CSVOptions, stream RowStream) (Result, error) {
	if dst == nil {
		return Result{}, fmt.Errorf("CSV destination writer is required")
	}

	columns, allowed, err := configuredColumns(options.Columns)
	if err != nil {
		return Result{}, err
	}
	if len(columns) == 0 {
		columns, err = discoverColumns(ctx, stream)
		if err != nil {
			return Result{}, err
		}
		allowed = make(map[string]struct{}, len(columns))
		for _, column := range columns {
			allowed[column] = struct{}{}
		}
	}

	writer := &countingWriter{dst: dst}
	csvWriter := csv.NewWriter(writer)
	if len(columns) > 0 {
		if err := csvWriter.Write(columns); err != nil {
			return Result{Columns: cloneStrings(columns)}, fmt.Errorf("write CSV header: %w", err)
		}
	}

	result := Result{Columns: cloneStrings(columns)}
	streamErr := visitRows(ctx, stream, func(row map[string]any) error {
		rowNumber := result.Rows + 1
		keys, err := validateRow(row, rowNumber)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if _, ok := allowed[key]; !ok {
				return &ColumnError{Row: rowNumber, Column: key, Reason: "is not present in the CSV schema"}
			}
		}

		record := make([]string, len(columns))
		for i, column := range columns {
			record[i] = scalarString(row[column])
		}
		if err := csvWriter.Write(record); err != nil {
			return fmt.Errorf("row %d: write CSV: %w", rowNumber, err)
		}
		result.Rows++
		return nil
	})
	csvWriter.Flush()
	result.Bytes = writer.n
	if streamErr != nil {
		return result, streamErr
	}
	if err := csvWriter.Error(); err != nil {
		return result, fmt.Errorf("write CSV: %w", err)
	}
	return result, nil
}

func visitRows(ctx context.Context, stream RowStream, visit RowVisitor) error {
	if stream == nil {
		return fmt.Errorf("export row stream is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stream(ctx, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return visit(row)
	}); err != nil {
		return fmt.Errorf("stream export rows: %w", err)
	}
	return ctx.Err()
}

func configuredColumns(columns []string) ([]string, map[string]struct{}, error) {
	if len(columns) == 0 {
		return nil, nil, nil
	}
	out := cloneStrings(columns)
	seen := make(map[string]struct{}, len(out))
	for _, column := range out {
		if column == "" {
			return nil, nil, &ColumnError{Column: column, Reason: "must not be empty"}
		}
		if _, ok := seen[column]; ok {
			return nil, nil, &ColumnError{Column: column, Reason: "is duplicated"}
		}
		seen[column] = struct{}{}
	}
	return out, seen, nil
}

func discoverColumns(ctx context.Context, stream RowStream) ([]string, error) {
	columns := make(map[string]struct{})
	var rows int64
	err := visitRows(ctx, stream, func(row map[string]any) error {
		rows++
		keys, err := validateRow(row, rows)
		if err != nil {
			return err
		}
		for _, key := range keys {
			columns[key] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedColumns(columns), nil
}

func validateRow(row map[string]any, rowNumber int64) ([]string, error) {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" {
			return nil, &ColumnError{Row: rowNumber, Column: key, Reason: "must not be empty"}
		}
		if err := validateScalar(row[key], rowNumber, key); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func validateScalar(value any, rowNumber int64, column string) error {
	if value == nil {
		return nil
	}
	valueType := reflect.TypeOf(value).String()
	if _, ok := value.(json.Marshaler); ok {
		return &ValueError{Row: rowNumber, Column: column, ValueType: valueType, Reason: "custom JSON marshalers are not supported for flat export values"}
	}
	valueOf := reflect.ValueOf(value)
	switch valueOf.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if number, ok := value.(json.Number); ok {
			if _, err := json.Marshal(number); err != nil {
				return &ValueError{Row: rowNumber, Column: column, ValueType: valueType, Reason: "invalid JSON number"}
			}
		}
		return nil
	case reflect.Float32, reflect.Float64:
		floatValue := valueOf.Float()
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return &ValueError{Row: rowNumber, Column: column, ValueType: valueType, Reason: "non-finite numbers are not valid JSON"}
		}
		return nil
	default:
		return &ValueError{Row: rowNumber, Column: column, ValueType: valueType}
	}
}

func marshalRow(row map[string]any) ([]byte, error) {
	if row == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(row)
}

func scalarString(value any) string {
	if value == nil {
		return ""
	}
	valueOf := reflect.ValueOf(value)
	switch valueOf.Kind() {
	case reflect.String:
		return valueOf.String()
	case reflect.Bool:
		return strconv.FormatBool(valueOf.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(valueOf.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(valueOf.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(valueOf.Float(), 'g', -1, valueOf.Type().Bits())
	default:
		return ""
	}
}

func sortedColumns(columns map[string]struct{}) []string {
	if len(columns) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(columns))
	for column := range columns {
		out = append(out, column)
	}
	sort.Strings(out)
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

type countingWriter struct {
	dst io.Writer
	n   int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.n += int64(n)
	if err == nil && n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, err
}

func writeAll(dst io.Writer, data []byte) (int64, error) {
	var written int64
	for len(data) > 0 {
		n, err := dst.Write(data)
		written += int64(n)
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}
