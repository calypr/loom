package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestAuthoringErrorForOpenAPIMapsConstructionCategoryScanFailures(t *testing.T) {
	privateCause := errors.New("private query bind value")
	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantCode     string
		wantMsg      string
		wantLog      bool
		wantLogCause string
	}{
		{
			name: "compiler refusal",
			err: &lifecycle.Error{
				Class: lifecycle.ClassUnprocessable, Stage: "construction-category-discovery",
				Code: string(compiler.CategoryScanStageUnknown), Message: "complete compiler-owned pivot categories are unavailable for this stage and pair",
				Cause: privateCause,
			},
			wantStatus: 422, wantCode: string(compiler.CategoryScanStageUnknown),
			wantMsg: "complete compiler-owned pivot categories are unavailable for this stage and pair",
		},
		{
			name: "deadline",
			err: &lifecycle.Error{
				Class: lifecycle.ClassUnavailable, Stage: "construction-category-discovery",
				Code: "CATEGORY_SCAN_TIMEOUT", Message: "finding all category values exceeded the preview time limit; filter the source rows and try again",
				Cause: fmt.Errorf("execution deadline: %w", context.DeadlineExceeded),
			},
			wantStatus: 503, wantCode: "CATEGORY_SCAN_TIMEOUT",
			wantMsg: "finding all category values exceeded the preview time limit; filter the source rows and try again",
			wantLog: true, wantLogCause: context.DeadlineExceeded.Error(),
		},
		{
			name: "internal execution failure",
			err: &lifecycle.Error{
				Class: lifecycle.ClassInternal, Stage: "construction-category-discovery",
				Code: "INTERNAL_ERROR", Message: "category discovery failed unexpectedly",
				Cause: fmt.Errorf("execute category query with private bind value: %w", privateCause),
			},
			wantStatus: 500, wantCode: "INTERNAL_ERROR",
			wantMsg: "category discovery failed unexpectedly",
			wantLog: true, wantLogCause: "private bind value",
		},
	}

	const requestID = "request-category-discovery-1"
	logs := &openAPIErrorLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := httpapi.ContextWithRequestID(context.Background(), requestID)
			status, response := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", test.err)
			if status != test.wantStatus {
				t.Fatalf("authoringErrorForOpenAPI status = %d, want %d", status, test.wantStatus)
			}
			if response.Error.Code != test.wantCode || response.Error.Message != test.wantMsg {
				t.Fatalf("authoringErrorForOpenAPI body = %#v, want code %q and safe message %q", response.Error, test.wantCode, test.wantMsg)
			}
			if response.Error.RequestId == nil || *response.Error.RequestId != requestID {
				t.Fatalf("response requestId = %v, want %q", response.Error.RequestId, requestID)
			}
			wire, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), privateCause.Error()) || strings.Contains(string(wire), "private bind value") {
				t.Fatalf("public error response leaked cause: %s", wire)
			}
			if test.wantLog && !logs.hasRequestID(requestID) {
				t.Fatalf("captured authoring error log does not contain request_id %q", requestID)
			}
			if test.wantLogCause != "" && !logs.hasCause(test.wantLogCause) {
				t.Fatalf("captured authoring error log does not retain cause %q", test.wantLogCause)
			}
		})
	}
}

type openAPIErrorLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *openAPIErrorLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *openAPIErrorLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *openAPIErrorLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *openAPIErrorLogCapture) WithGroup(string) slog.Handler      { return h }

func (h *openAPIErrorLogCapture) hasRequestID(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "request_id" && attr.Value.String() == want {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func (h *openAPIErrorLogCapture) hasCause(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "cause" && strings.Contains(fmt.Sprint(attr.Value.Any()), want) {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}
