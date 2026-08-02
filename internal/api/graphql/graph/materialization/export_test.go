package materializationapi

import (
	"bytes"
	"strings"
	"testing"

	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

func TestExportWriterFormats(t *testing.T) {
	for _, test := range []struct {
		name   string
		format dfmaterialization.ExportFormat
		want   string
	}{
		{name: "csv", format: dfmaterialization.ExportCSV, want: "id,label\n1,hello\n2,\"a,b\"\n"},
		{name: "tsv", format: dfmaterialization.ExportTSV, want: "id\tlabel\n1\thello\n2\ta,b\n"},
		{name: "jsonl", format: dfmaterialization.ExportJSONL, want: "{\"id\":\"1\",\"label\":\"hello\"}\n{\"id\":\"2\",\"label\":\"a,b\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writer := &exportWriter{format: test.format, columns: []string{"id", "label"}, out: &exportCountingWriter{out: &output, maxRows: 10, maxBytes: 1000}}
			if err := writer.begin(); err != nil {
				t.Fatal(err)
			}
			if err := writer.visit(map[string]any{"id": "1", "label": "hello"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.visit(map[string]any{"id": "2", "label": "a,b"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.end(); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("output = %q, want %q", output.String(), test.want)
			}
		})
	}

	var jsonOutput bytes.Buffer
	jsonWriter := &exportWriter{format: dfmaterialization.ExportJSON, columns: []string{"id"}, out: &exportCountingWriter{out: &jsonOutput, maxRows: 10, maxBytes: 1000}}
	if err := jsonWriter.begin(); err != nil {
		t.Fatal(err)
	}
	if err := jsonWriter.visit(map[string]any{"id": "1", "__loom_row_id": "internal"}); err != nil {
		t.Fatal(err)
	}
	if err := jsonWriter.end(); err != nil {
		t.Fatal(err)
	}
	if got := jsonOutput.String(); !strings.Contains(got, `[{"id":"1"}`) || strings.Contains(got, "__loom_row_id") {
		t.Fatalf("json output = %q", got)
	}
}

func TestExportCountingWriterLimits(t *testing.T) {
	var output bytes.Buffer
	writer := &exportCountingWriter{out: &output, maxRows: 1, maxBytes: 2}
	if _, err := writer.Write([]byte("123")); err == nil {
		t.Fatal("byte limit was not enforced")
	}
	rowWriter := &exportWriter{format: dfmaterialization.ExportJSONL, columns: []string{"id"}, out: &exportCountingWriter{out: &bytes.Buffer{}, maxRows: 1, maxBytes: 1000}}
	if err := rowWriter.begin(); err != nil {
		t.Fatal(err)
	}
	if err := rowWriter.visit(map[string]any{"id": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := rowWriter.visit(map[string]any{"id": "2"}); err == nil {
		t.Fatal("row limit was not enforced")
	}
}
