package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/generated/loomapi"
)

// The authoring-v2 preview route answers ExplorerBuilderPreview with
// columns as loomapi.ContractColumn objects (internal/server
// explorer_preview_response.go: v2EmissionColumns, api.gen.go:7137)
// whose public name is at .column. The client decoded columns as
// []string, so the paired run at 79e72ed4 (job 132040946330) aborted:
// "json: cannot unmarshal object into Go struct field
// PreviewResult.columns of type string". The mock served the same
// string array the client expected. This test marshals the real wire
// types and fails on the pre-fix client.

func serverPreviewBody(receiptID, outputID string, names []string, rows []map[string]any) map[string]any {
	cols := make([]loomapi.ContractColumn, 0, len(names))
	for _, n := range names {
		cols = append(cols, loomapi.ContractColumn{Column: n, Label: n, LogicalType: "string"})
	}
	return map[string]any{
		"apiVersion":  "loom.calypr.org/explorer-authoring/v2",
		"kind":        "ExplorerBuilderPreview",
		"receiptId":   receiptID,
		"outputId":    outputID,
		"columns":     cols,
		"rows":        rows,
		"rowCount":    len(rows),
		"diagnostics": []any{},
	}
}

func TestExecuteOutputDecodesRealPreviewWireShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serverPreviewBody("rcpt-1", "out",
			[]string{"patient_id", "gender"},
			[]map[string]any{{"patient_id": "p1", "gender": "female"}}))
	}))
	defer srv.Close()
	arm := &Arm{Backend: BackendArango, BaseURL: srv.URL, Client: srv.Client()}
	res, err := arm.ExecuteOutput(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-1", "out", 10)
	if err != nil {
		t.Fatalf("preview decode errored: %v", err)
	}
	if strings.Join(res.Columns, ",") != "patient_id,gender" {
		t.Errorf("columns lost: got %v", res.Columns)
	}
	if res.RowCount != 1 || len(res.Rows) != 1 || res.Digest == "" {
		t.Errorf("rows/rowCount/digest lost: %+v", res)
	}
}

func TestDecodePreviewAbortsOnNamelessColumn(t *testing.T) {
	value := serverPreviewBody("rcpt-1", "out", []string{"patient_id", " "}, []map[string]any{{"patient_id": "p1"}})
	if _, err := decodePreview(value); err == nil || !strings.Contains(err.Error(), "public name") {
		t.Fatalf("nameless column must abort fail-closed, got: %v", err)
	}
}
