package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/generated/loomapi"
)

// The authoring-v2 preview route answers with loomapi.PreviewResponse
// (internal/server previewResponseFromEncoder,
// openapi_explorer_authoring_direct.go): columns are ContractColumn
// objects whose public name is at .column (v2EmissionColumns,
// api.gen.go), and Sampled is set from
// !summary.Complete || summary.Truncated with no omitempty, so it is
// always on the wire. The paired run at 79e72ed4 (job 132045898495)
// died on "cannot unmarshal object into Go struct field
// PreviewResult.columns of type string" because the client decoded
// columns as []string. An earlier revision of this fix also dropped
// Sampled from the decode on a wrong reading of the encoder alone;
// these tests pin the full chain so a truncated preview can never be
// silently treated as complete: sampled=true -> PreviewResult.Sampled
// -> sample/result Truncated -> GateCompleteness refuses the arm.

func serverPreviewBody(receiptID, outputID string, names []string, rows []map[string]any, sampled bool) loomapi.PreviewResponse {
	cols := make([]loomapi.ContractColumn, 0, len(names))
	for _, n := range names {
		cols = append(cols, loomapi.ContractColumn{Column: n, Label: n, LogicalType: "string"})
	}
	sources := make([]loomapi.PreviewRowSource, 0, len(rows))
	for range rows {
		sources = append(sources, loomapi.PreviewRowSource{Kind: loomapi.PreviewRowSourceKindUNAVAILABLE})
	}
	return loomapi.PreviewResponse{
		ApiVersion:  loomapi.LoomCalyprOrgexplorerAuthoringv2,
		Kind:        loomapi.ExplorerBuilderPreview,
		ReceiptId:   receiptID,
		OutputId:    outputID,
		Columns:     cols,
		Rows:        rows,
		RowSources:  sources,
		RowCount:    len(rows),
		Sampled:     sampled,
		Diagnostics: []loomapi.Diagnostic{},
	}
}

func TestExecuteOutputDecodesRealPreviewWireShape(t *testing.T) {
	rows := []map[string]any{{"patient_id": "p1", "gender": "female"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serverPreviewBody("rcpt-1", "out", []string{"patient_id", "gender"}, rows, false))
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
	if res.Sampled {
		t.Errorf("a complete preview must decode Sampled=false")
	}
}

func TestExecuteOutputSurfacesSampledTrue(t *testing.T) {
	rows := []map[string]any{{"patient_id": "p1", "gender": "female"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serverPreviewBody("rcpt-1", "out", []string{"patient_id", "gender"}, rows, true))
	}))
	defer srv.Close()
	arm := &Arm{Backend: BackendArango, BaseURL: srv.URL, Client: srv.Client()}
	res, err := arm.ExecuteOutput(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-1", "out", 10)
	if err != nil {
		t.Fatalf("preview decode errored: %v", err)
	}
	if !res.Sampled {
		t.Fatal("sampled=true on the wire must survive decode")
	}
}

func TestDecodePreviewAbortsOnNamelessColumn(t *testing.T) {
	resp := serverPreviewBody("rcpt-1", "out", []string{"patient_id", " "}, []map[string]any{{"patient_id": "p1"}}, false)
	value := map[string]any{}
	raw, _ := json.Marshal(resp)
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if _, err := decodePreview(value); err == nil || !strings.Contains(err.Error(), "public name") {
		t.Fatalf("nameless column must abort fail-closed, got: %v", err)
	}
}

// TestSampledTrueChainFailsCompletenessGate pins the full disclosure
// contract: a preview the route flags sampled=true must reach timing
// samples as Truncated (MeasureArms), reach the BackendResult as
// Truncated (backendResultFrom), and make GateCompleteness refuse the
// arm (EvaluateGates) -- so a truncated output can never be timed as
// complete or back a speed claim.
func TestSampledTrueChainFailsCompletenessGate(t *testing.T) {
	rows := []map[string]any{{"patient_id": "p1", "gender": "female"}}
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["w"] = rows
	fp.rows["w"] = rows
	fa.mu.Lock()
	fa.sampled = true // only the arango arm serves an incomplete preview
	fa.mu.Unlock()

	clock := &scriptedClock{base: time.Unix(1700000000, 0)}
	opts := RunOptions{Project: "p", Generation: "g", Explorer: "w", UnrestrictedToken: "Bearer x", PreviewLimit: 100,
		Config: RunConfig{Warmup: 0, Iterations: 2, Concurrency: 1, Rounds: 1}}
	samples, finals, err := MeasureArms(context.Background(), clock, "w", fa.arm(BackendArango), fp.arm(BackendPostgres), opts,
		map[string]string{BackendArango: "r-a", BackendPostgres: "r-p"},
		map[string]string{BackendArango: "w", BackendPostgres: "w"},
		func(ctx context.Context, arm *Arm) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	measured := 0
	for _, s := range samples {
		if s.Warmup {
			continue
		}
		measured++
		switch s.Backend {
		case BackendArango:
			if !s.Truncated {
				t.Fatal("sampled=true preview must surface as sample.Truncated (MeasureArms)")
			}
		case BackendPostgres:
			if s.Truncated {
				t.Fatal("complete arm must not be marked truncated")
			}
		}
	}
	if measured == 0 {
		t.Fatal("no measured samples produced")
	}
	columns := []string{"patient_id", "gender"}
	a, p := backendResultFrom(BackendArango, samples, finals), backendResultFrom(BackendPostgres, samples, finals)
	if !a.Truncated {
		t.Fatal("sampled=true final must reach BackendResult.Truncated (backendResultFrom)")
	}
	if p.Truncated {
		t.Fatal("complete final must not be truncated")
	}
	report := EvaluateGates("w", columns, mustBag(t, columns, rows), a, p, rows, rows, &StorageIDMap{})
	if report.Claimable {
		t.Fatalf("a sampled=true arm must fail GateCompleteness: %+v", report.Gates)
	}
	if !strings.HasPrefix(report.ExclusionReason, "completeness:") {
		t.Fatalf("exclusion must name the completeness gate, got %q", report.ExclusionReason)
	}
}
