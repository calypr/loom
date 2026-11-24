package arango_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	model "github.com/calypr/loom/internal/dataframe/compiler/model"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"

	"github.com/calypr/loom/internal/testkit"

	explorerapi "github.com/calypr/loom/internal/api/explorer"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	shared "github.com/arangodb/go-driver/v2/arangodb/shared"
)

func TestConfiguredAQLQueryRowsLogsSafeRequestCorrelatedRecords(t *testing.T) {
	for _, phase := range []string{"query_rows", "preview_query_rows"} {
		t.Run(phase, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			ctx := httpctx.ContextWithRequestID(context.Background(), "request-123")
			query := "FOR d IN patients FILTER d.id == @secret RETURN d"
			bindVars := map[string]any{"secret": "private-bind-value"}
			failure := errors.New("arangodb unavailable: connection refused; query=RETURN @secret auth_token=private-auth-value")
			gotErr := executionarango.ConfiguredAQLQueryRows(logger, phase, explorerapi.PreserveConfiguredQueryError, func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
				return failure
			})(ctx, query, 16, bindVars, func(map[string]any) error { return nil })
			if gotErr != failure {
				t.Fatalf("callback error = %v, want original error", gotErr)
			}

			records := testkit.DiagnosticRecords(t, &output)
			if len(records) != 2 {
				t.Fatalf("record count = %d, want start and completion", len(records))
			}
			start, record := records[0], records[1]
			if start["request_id"] != "request-123" || start["phase"] != phase+"_start" || start["query_hash"] != record["query_hash"] {
				t.Fatalf("start record fields = %#v", start)
			}
			if start["query_bytes"] != float64(len(query)) || start["bind_vars"] != float64(len(bindVars)) || start["cursor_batch_size"] != float64(16) {
				t.Fatalf("start counts = %#v", start)
			}
			if _, found := start["success"]; found {
				t.Fatalf("pending start record has terminal success field: %#v", start)
			}
			if record["request_id"] != "request-123" || record["phase"] != phase || record["success"] != false {
				t.Fatalf("record fields = %#v", record)
			}
			if _, ok := record["duration_ms"].(float64); !ok {
				t.Fatalf("duration_ms = %#v, want number", record["duration_ms"])
			}
			digest := sha256.Sum256([]byte(query))
			if record["query_hash"] != hex.EncodeToString(digest[:]) {
				t.Fatalf("query_hash = %#v, want full SHA-256", record["query_hash"])
			}
			if record["error_type"] != "*errors.errorString" {
				t.Fatalf("error_type = %#v", record["error_type"])
			}
			if record["cause"] != "arangodb unavailable: connection refused; query=[redacted]" {
				t.Fatalf("bounded cause = %#v", record["cause"])
			}
			if len(record["cause"].(string)) > 256 {
				t.Fatalf("AQL cause exceeds %d bytes: %d", 256, len(record["cause"].(string)))
			}
			for _, forbidden := range []string{query, "private-bind-value", "private-auth-value", failure.Error()} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("log contains sensitive input %q: %s", forbidden, output.String())
				}
			}
		})
	}
}

func TestLoggedPreviewIndexPreparationUsesSafeFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	ctx := httpctx.ContextWithRequestID(context.Background(), "request-index")
	failure := fmt.Errorf("prepare index: %w", shared.ArangoError{
		HasError: true, Code: http.StatusConflict, ErrorNum: 12345, ErrorMessage: "AQL: persistent index field is unsupported; access_token=private-auth-value",
	})
	spec := model.PreviewCoveringIndexSpec{Collection: "patients", Name: "idx_patient_category"}
	gotErr := executionarango.LoggedPreviewIndexPreparation(logger, func(context.Context, model.PreviewCoveringIndexSpec) error { return failure })(ctx, spec)
	if gotErr != failure {
		t.Fatalf("preparation error = %v, want original error", gotErr)
	}
	records := testkit.DiagnosticRecords(t, &output)
	if len(records) != 1 || records[0]["request_id"] != "request-index" || records[0]["phase"] != "index_prepare" || records[0]["collection"] != spec.Collection || records[0]["index"] != spec.Name || records[0]["success"] != false {
		t.Fatalf("index log records = %#v", records)
	}
	if records[0]["cause"] != "ArangoDB error number 12345 (HTTP 409): AQL: persistent index field is unsupported; access_token=[redacted]" {
		t.Fatalf("index cause = %#v", records[0]["cause"])
	}
	if len(records[0]["cause"].(string)) > 256 {
		t.Fatalf("index cause exceeds %d bytes: %d", 256, len(records[0]["cause"].(string)))
	}
	if strings.Contains(output.String(), failure.Error()) {
		t.Fatalf("index logs contain raw backend error: %s", output.String())
	}
}
