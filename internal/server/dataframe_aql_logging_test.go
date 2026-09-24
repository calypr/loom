package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLogDataframeAQLReportsRowsWithoutLoggingValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	row := map[string]any{"value": "private-row-value"}
	var visited map[string]any

	err := logDataframeAQL(
		logger,
		context.Background(),
		"private-query-text",
		32,
		map[string]any{"private-bind-name": "private-bind-value"},
		func(got map[string]any) error {
			visited = got
			return nil
		},
		func(_ context.Context, _ string, _ int, _ map[string]any, visit arangostore.RowVisitor) error {
			return visit(row)
		},
	)
	if err != nil {
		t.Fatalf("logDataframeAQL() error = %v", err)
	}
	if visited == nil || visited["value"] != "private-row-value" {
		t.Fatalf("visitor received %v, want original row", visited)
	}

	logs := output.String()
	for _, want := range []string{"dataframe AQL complete", "row_count=1", "time_to_first_row_seconds=", "status=complete"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not contain %q: %s", want, logs)
		}
	}
	for _, privateValue := range []string{"private-query-text", "private-bind-name", "private-bind-value", "private-row-value"} {
		if strings.Contains(logs, privateValue) {
			t.Errorf("logs contain private value %q: %s", privateValue, logs)
		}
	}
}

func TestLogDataframeAQLDistinguishesEmptyFailureAndCancellation(t *testing.T) {
	failureErr := errors.New("private failure detail")
	tests := []struct {
		name       string
		queryRows  func(context.Context, arangostore.RowVisitor) error
		wantStatus string
		wantEvent  string
		wantErr    error
		wantRows   string
	}{
		{
			name: "empty",
			queryRows: func(context.Context, arangostore.RowVisitor) error {
				return nil
			},
			wantStatus: "status=no_rows",
			wantEvent:  "dataframe AQL no rows",
			wantRows:   "row_count=0",
		},
		{
			name: "failure",
			queryRows: func(_ context.Context, visit arangostore.RowVisitor) error {
				if err := visit(map[string]any{"value": "private-row-value"}); err != nil {
					return err
				}
				return failureErr
			},
			wantStatus: "status=failed",
			wantEvent:  "dataframe AQL failed",
			wantErr:    failureErr,
			wantRows:   "row_count=1",
		},
		{
			name: "cancellation",
			queryRows: func(ctx context.Context, _ arangostore.RowVisitor) error {
				return ctx.Err()
			},
			wantStatus: "status=canceled",
			wantEvent:  "dataframe AQL canceled",
			wantErr:    context.Canceled,
			wantRows:   "row_count=0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.name == "cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))
			err := logDataframeAQL(
				logger,
				ctx,
				"private-query-text",
				16,
				map[string]any{"bind": "private-bind-value"},
				func(map[string]any) error { return nil },
				func(ctx context.Context, _ string, _ int, _ map[string]any, visit arangostore.RowVisitor) error {
					return test.queryRows(ctx, visit)
				},
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("logDataframeAQL() error = %v, want %v", err, test.wantErr)
			}

			logs := output.String()
			for _, want := range []string{test.wantStatus, test.wantEvent, test.wantRows} {
				if !strings.Contains(logs, want) {
					t.Errorf("logs do not contain %q: %s", want, logs)
				}
			}
			if test.name == "failure" && (strings.Contains(logs, "error_num=") || strings.Contains(logs, "http_code=")) {
				t.Errorf("non-Arango failure logs contain Arango error fields: %s", logs)
			}
			if strings.Contains(logs, "time_to_first_row_seconds=") != (test.name == "failure") {
				t.Errorf("time-to-first-row presence does not match delivered rows: %s", logs)
			}
			for _, privateValue := range []string{"private-query-text", "private-bind-value", "private failure detail", "private-row-value"} {
				if strings.Contains(logs, privateValue) {
					t.Errorf("logs contain private value %q: %s", privateValue, logs)
				}
			}
		})
	}
}

func TestLogDataframeAQLPreservesVisitorError(t *testing.T) {
	visitErr := errors.New("visitor stopped")
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	err := logDataframeAQL(
		logger,
		context.Background(),
		"private-query-text",
		16,
		nil,
		func(map[string]any) error { return visitErr },
		func(_ context.Context, _ string, _ int, _ map[string]any, visit arangostore.RowVisitor) error {
			return visit(map[string]any{"value": "private-row-value"})
		},
	)
	if !errors.Is(err, visitErr) {
		t.Fatalf("logDataframeAQL() error = %v, want visitor error %v", err, visitErr)
	}
	logs := output.String()
	for _, want := range []string{"dataframe AQL failed", "status=failed", "row_count=1", "time_to_first_row_seconds="} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not contain %q: %s", want, logs)
		}
	}
}

func TestLogDataframeAQLLogsSafeArangoErrorNumbers(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	queryErr := fmt.Errorf("query wrapper: %w", shared.ArangoError{
		HasError:     true,
		Code:         502,
		ErrorNum:     12345,
		ErrorMessage: "private backend message",
	})

	err := logDataframeAQL(
		logger,
		context.Background(),
		"private-query-text",
		16,
		map[string]any{"bind": "private-bind-value"},
		func(map[string]any) error { return nil },
		func(context.Context, string, int, map[string]any, arangostore.RowVisitor) error {
			return queryErr
		},
	)
	if err != queryErr {
		t.Fatalf("logDataframeAQL() error = %v, want original error %v", err, queryErr)
	}

	logs := output.String()
	for _, want := range []string{"status=failed", "error_num=12345", "http_code=502"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not contain %q: %s", want, logs)
		}
	}
	for _, privateValue := range []string{"private-query-text", "private-bind-value", "private backend message"} {
		if strings.Contains(logs, privateValue) {
			t.Errorf("logs contain private value %q: %s", privateValue, logs)
		}
	}
}
