package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeNDJSONWritesOneDeterministicObjectPerLine(t *testing.T) {
	rows := staticRows(
		map[string]any{"zeta": 2, "alpha": "first"},
		map[string]any{"alpha": "second", "nullable": nil},
	)

	var output bytes.Buffer
	result, err := EncodeNDJSON(context.Background(), &output, rows)
	if err != nil {
		t.Fatalf("EncodeNDJSON() error = %v", err)
	}
	const want = "{\"alpha\":\"first\",\"zeta\":2}\n{\"alpha\":\"second\",\"nullable\":null}\n"
	if got := output.String(); got != want {
		t.Fatalf("NDJSON = %q, want %q", got, want)
	}
	if result.Rows != 2 {
		t.Fatalf("Rows = %d, want 2", result.Rows)
	}
	if result.Bytes != int64(len(want)) {
		t.Fatalf("Bytes = %d, want %d", result.Bytes, len(want))
	}
	if wantColumns := []string{"alpha", "nullable", "zeta"}; !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}

	for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("NDJSON line %q is not an object: %v", line, err)
		}
	}
}

func TestEncodeCSVDiscoversSortedColumnUnionWithoutMaterializingRows(t *testing.T) {
	var passes int
	rows := func(ctx context.Context, visit RowVisitor) error {
		passes++
		for _, row := range []map[string]any{
			{"zeta": "z", "alpha": "a"},
			{"middle": 2, "alpha": "again"},
		} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}

	var output bytes.Buffer
	result, err := EncodeCSV(context.Background(), &output, CSVOptions{}, rows)
	if err != nil {
		t.Fatalf("EncodeCSV() error = %v", err)
	}
	const want = "alpha,middle,zeta\na,,z\nagain,2,\n"
	if got := output.String(); got != want {
		t.Fatalf("CSV = %q, want %q", got, want)
	}
	if passes != 2 {
		t.Fatalf("stream passes = %d, want 2 for inferred columns", passes)
	}
	if result.Rows != 2 {
		t.Fatalf("Rows = %d, want 2", result.Rows)
	}
	if wantColumns := []string{"alpha", "middle", "zeta"}; !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}
}

func TestEncodeCSVUsesConfiguredOrderAndStandardEscaping(t *testing.T) {
	rows := staticRows(map[string]any{
		"plain":   "value",
		"quoted":  "say \"hello\"",
		"comma":   "left,right",
		"newline": "first\nsecond",
	})

	var output bytes.Buffer
	result, err := EncodeCSV(context.Background(), &output, CSVOptions{Columns: []string{"newline", "comma", "quoted", "plain"}}, rows)
	if err != nil {
		t.Fatalf("EncodeCSV() error = %v", err)
	}
	if wantColumns := []string{"newline", "comma", "quoted", "plain"}; !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}

	parsed, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	want := [][]string{
		{"newline", "comma", "quoted", "plain"},
		{"first\nsecond", "left,right", "say \"hello\"", "value"},
	}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("parsed CSV = %#v, want %#v; raw=%q", parsed, want, output.String())
	}
}

func TestEncodeEmptyInput(t *testing.T) {
	empty := staticRows()

	var ndjson bytes.Buffer
	ndjsonResult, err := EncodeNDJSON(context.Background(), &ndjson, empty)
	if err != nil {
		t.Fatalf("EncodeNDJSON() error = %v", err)
	}
	if ndjson.String() != "" || ndjsonResult.Rows != 0 || len(ndjsonResult.Columns) != 0 {
		t.Fatalf("empty NDJSON = %q, result=%#v", ndjson.String(), ndjsonResult)
	}

	var inferredCSV bytes.Buffer
	inferredResult, err := EncodeCSV(context.Background(), &inferredCSV, CSVOptions{}, empty)
	if err != nil {
		t.Fatalf("EncodeCSV inferred() error = %v", err)
	}
	if inferredCSV.String() != "" || inferredResult.Rows != 0 || len(inferredResult.Columns) != 0 {
		t.Fatalf("empty inferred CSV = %q, result=%#v", inferredCSV.String(), inferredResult)
	}

	var configuredCSV bytes.Buffer
	configuredResult, err := EncodeCSV(context.Background(), &configuredCSV, CSVOptions{Columns: []string{"patient_id", "status"}}, empty)
	if err != nil {
		t.Fatalf("EncodeCSV configured() error = %v", err)
	}
	if got, want := configuredCSV.String(), "patient_id,status\n"; got != want {
		t.Fatalf("configured empty CSV = %q, want %q", got, want)
	}
	if configuredResult.Rows != 0 {
		t.Fatalf("configured empty Rows = %d, want 0", configuredResult.Rows)
	}
}

func TestEncodeRejectsNestedValuesWithRowAndColumn(t *testing.T) {
	tests := []struct {
		name string
		run  func(*bytes.Buffer) error
	}{
		{
			name: "ndjson",
			run: func(output *bytes.Buffer) error {
				_, err := EncodeNDJSON(context.Background(), output, staticRows(map[string]any{"nested": map[string]any{"no": "thanks"}}))
				return err
			},
		},
		{
			name: "csv",
			run: func(output *bytes.Buffer) error {
				_, err := EncodeCSV(context.Background(), output, CSVOptions{}, staticRows(map[string]any{"nested": []string{"no", "thanks"}}))
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := test.run(&output)
			if err == nil {
				t.Fatal("expected an error")
			}
			var valueErr *ValueError
			if !errors.As(err, &valueErr) {
				t.Fatalf("error = %T %v, want ValueError", err, err)
			}
			if valueErr.Row != 1 || valueErr.Column != "nested" {
				t.Fatalf("ValueError = %#v, want row 1/nested", valueErr)
			}
			if !strings.Contains(err.Error(), "unsupported nested value") {
				t.Fatalf("error = %q, want clear nested-value explanation", err)
			}
		})
	}
}

func TestEncodeCSVRejectsColumnsThatWouldBeDropped(t *testing.T) {
	var output bytes.Buffer
	_, err := EncodeCSV(context.Background(), &output, CSVOptions{Columns: []string{"included"}}, staticRows(map[string]any{"included": "yes", "extra": "no"}))
	if err == nil {
		t.Fatal("expected an error")
	}
	var columnErr *ColumnError
	if !errors.As(err, &columnErr) {
		t.Fatalf("error = %T %v, want ColumnError", err, err)
	}
	if columnErr.Row != 1 || columnErr.Column != "extra" {
		t.Fatalf("ColumnError = %#v, want row 1 extra", columnErr)
	}
}

func staticRows(rows ...map[string]any) RowStream {
	return func(ctx context.Context, visit RowVisitor) error {
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}
