package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	postgres "github.com/calypr/loom/internal/backends/postgres"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHTTPCodePolicyCoversEveryPublicError(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	centralPath := filepath.Join(filepath.Dir(filename), "../../dataframe/errors/errors.go")
	file, err := parser.ParseFile(token.NewFileSet(), centralPath, nil, 0)
	if err != nil {
		t.Fatalf("parse central error registry: %v", err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.GenDecl)
		if !ok || declaration.Tok != token.CONST {
			return true
		}
		for _, spec := range declaration.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if !strings.HasPrefix(name.Name, "Code") || index >= len(values.Values) {
					continue
				}
				literal, ok := values.Values[index].(*ast.BasicLit)
				if !ok {
					continue
				}
				code := strings.Trim(literal.Value, "\"")
				policy, exists := httpCodePolicies[code]
				if !exists || policy.Status < 400 {
					t.Errorf("public error code %s has no valid HTTP policy", code)
				}
				mapped := MapDataframeError(dataframeerrors.NewError(dataframeerrors.ErrorCode(code), ""), "policy-test")
				if mapped.Body.Error.Code != code || mapped.Body.Error.Message == "" || mapped.Status != policy.Status {
					t.Errorf("public error code %s mapped = %#v, policy = %#v", code, mapped, policy)
				}
			}
		}
		return false
	})
}

func TestMapDataframeErrorRedactsUnknownCause(t *testing.T) {
	mapped := MapDataframeError(errors.New("arango password=secret collection=private"), "req-1")
	if mapped.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", mapped.Status)
	}
	if mapped.Body.Error.Code != "INTERNAL_ERROR" || mapped.Body.Error.Message != "an unexpected internal error occurred while processing the data request" {
		t.Fatalf("body = %#v", mapped.Body.Error)
	}
	if mapped.Body.Error.Details["causeType"] != "*errors.errorString" {
		t.Fatalf("details = %#v, want causeType", mapped.Body.Error.Details)
	}
	if mapped.Body.Error.RequestID != "req-1" {
		t.Fatalf("request id = %q", mapped.Body.Error.RequestID)
	}
}

func TestMapDataframeErrorBackendIsRetryable(t *testing.T) {
	err := dataframeerrors.Wrap(errors.New("clickhouse tcp://secret"), dataframeerrors.CodeBackendUnavailable, "")
	mapped := MapDataframeError(err, "req-2")
	if mapped.Status != http.StatusServiceUnavailable || mapped.Body.Error.Code != "BACKEND_UNAVAILABLE" || !mapped.Body.Error.Retryable {
		t.Fatalf("mapped = %#v", mapped)
	}
	if mapped.Body.Error.Message == "clickhouse tcp://secret" {
		t.Fatal("backend cause leaked into response")
	}
}

func TestMapDataframeErrorUnlistedPivotCategoryIsActionable(t *testing.T) {
	code := dataframeerrors.ErrorCode("TABLE_PIVOT_UNLISTED_CATEGORY")
	mapped := MapDataframeError(dataframeerrors.NewError(code, "private"), "req-pivot")
	wantMessage := "Pivot found an unlisted category; select all discovered categories or filter rows before Pivot."
	if mapped.Status != http.StatusUnprocessableEntity || mapped.Body.Error.Code != string(code) || mapped.Body.Error.Message != wantMessage || mapped.Body.Error.Retryable {
		t.Fatalf("mapped = %#v, want non-retryable 422 %s with actionable message", mapped, code)
	}
}

func TestMapDataframeErrorPreservesOrderedFieldPath(t *testing.T) {
	err := dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "", dataframeerrors.WithFieldPath("input", "outputs", "0", "columns", "2"))
	mapped := MapDataframeError(err, "req-path")
	want := []string{"input", "outputs", "0", "columns", "2"}
	if mapped.Status != http.StatusBadRequest || mapped.Body.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("mapped = %#v, want INVALID_REQUEST/400", mapped)
	}
	if !reflect.DeepEqual(mapped.Body.Error.FieldPath, want) {
		t.Fatalf("field path = %#v, want %#v", mapped.Body.Error.FieldPath, want)
	}
}

func TestMapDataframeErrorPreviewClassifications(t *testing.T) {
	for _, test := range []struct {
		code      dataframeerrors.ErrorCode
		status    int
		retryable bool
	}{
		{dataframeerrors.CodePlanTooExpensive, http.StatusTooManyRequests, false},
		{dataframeerrors.CodeTablePivotCellCardinality, http.StatusUnprocessableEntity, false},
		{dataframeerrors.CodeReceiptStoreUnavailable, http.StatusServiceUnavailable, true},
		{dataframeerrors.CodePreviewTimeout, http.StatusGatewayTimeout, true},
		{dataframeerrors.CodePreviewResponseTooLarge, http.StatusRequestEntityTooLarge, false},
		{dataframeerrors.CodeQueryMemoryLimitExceeded, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeQueryResourceLimitExceeded, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeQueryBackendOutOfMemory, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeDynamicSchemaDrift, http.StatusConflict, false},
		{dataframeerrors.CodeRecipeContractViolation, http.StatusConflict, false},
	} {
		mapped := MapDataframeError(dataframeerrors.NewError(test.code, "private", dataframeerrors.WithRetryable(test.retryable)), "req-preview")
		if mapped.Status != test.status || mapped.Body.Error.Code != string(test.code) || mapped.Body.Error.Retryable != test.retryable {
			t.Errorf("%s mapped = %#v", test.code, mapped)
		}
		if mapped.Body.Error.Message == "private" {
			t.Errorf("%s leaked private message", test.code)
		}
	}
}

func TestMapDataframeErrorFiberStatus(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
		want int
	}{
		{fiber.ErrNotFound, "NOT_FOUND", http.StatusNotFound},
		{fiber.ErrMethodNotAllowed, "METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed},
		{fiber.ErrRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", http.StatusRequestEntityTooLarge},
	} {
		mapped := MapDataframeError(test.err, "")
		if mapped.Status != test.want || mapped.Body.Error.Code != test.code {
			t.Errorf("%v => %#v, want %s/%d", test.err, mapped, test.code, test.want)
		}
	}
}

func TestGenerationFileErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{dataframeerrors.NewError(dataframeerrors.CodeInvalidGenerationFile, ""), "INVALID_GENERATION_FILE"},
		{dataframeerrors.NewError(dataframeerrors.CodeDuplicateGenerationFile, ""), "DUPLICATE_GENERATION_FILE"},
	} {
		mapped := MapDataframeError(test.err, "req-file")
		if mapped.Status != http.StatusBadRequest || mapped.Body.Error.Code != test.code || mapped.Body.Error.Retryable {
			t.Errorf("mapped = %#v, want %s/400", mapped, test.code)
		}
	}
}

func TestMapDataframeErrorPreservesLimitAndPublicationCodes(t *testing.T) {
	for _, test := range []struct {
		code      dataframeerrors.ErrorCode
		status    int
		retryable bool
	}{
		{dataframeerrors.CodeInvalidLimit, http.StatusBadRequest, false},
		{dataframeerrors.CodePublicationFailed, http.StatusServiceUnavailable, true},
	} {
		mapped := MapDataframeError(dataframeerrors.NewError(test.code, ""), "req-publication")
		if mapped.Status != test.status || mapped.Body.Error.Code != string(test.code) || mapped.Body.Error.Retryable != test.retryable {
			t.Errorf("%s mapped = %#v, want %s/%d retryable=%t", test.code, mapped, test.code, test.status, test.retryable)
		}
	}
}

type adapterUserError struct {
	code string
}

func (e adapterUserError) Error() string {
	return "private adapter detail"
}

func (e adapterUserError) Code() string {
	return e.code
}

func (e adapterUserError) FieldPath() []string {
	return nil
}

func (e adapterUserError) Details() map[string]any {
	return nil
}

func (e adapterUserError) Retryable() bool {
	return false
}

func TestMapDataframeErrorDescribesUnknownAdapterUserCode(t *testing.T) {
	for _, test := range []struct {
		code    string
		message string
		status  int
	}{
		{"ADAPTER_PRIVATE_CODE", "an unexpected internal error occurred while processing the data request", http.StatusInternalServerError},
		{"INVALID_REQUEST", "the request is invalid", http.StatusBadRequest},
	} {
		mapped := MapDataframeError(adapterUserError{code: test.code}, "req-adapter")
		if mapped.Status != test.status {
			t.Fatalf("%s status = %d, want %d", test.code, mapped.Status, test.status)
		}
		if mapped.Body.Error.Message != test.message {
			t.Fatalf("%s message = %q, want %q", test.code, mapped.Body.Error.Message, test.message)
		}
		if strings.Contains(mapped.Body.Error.Message, "private adapter detail") {
			t.Fatalf("%s leaked adapter cause", test.code)
		}
	}
}

// TestMapDataframeErrorRedactsPostgresDriverCause proves the HTTP payload
// for PostgreSQL failures — translated or not — never carries SQL text,
// bound argument values, DSN material, or schema/constraint names, and that
// retryability follows the boundary contract (connectivity yes;
// serialization/deadlock/unique/aborted/ambiguous-commit never blanket).
func TestMapDataframeErrorRedactsPostgresDriverCause(t *testing.T) {
	canaries := []string{
		"reb_http_canary_select", "reb_http_schema", "reb_http_table",
		"reb_http_ux", "reb_http_bind_value", "reb_http_user", "reb_http_pass", "reb_http_host",
	}
	secretPgErr := &pgconn.PgError{
		Severity: "ERROR", Code: "23505",
		Message:        `duplicate key value violates unique constraint "reb_http_ux" with reb_http_canary_select`,
		Detail:         `Key (col)=(reb_http_bind_value) already exists on reb_http_table.`,
		Hint:           "check reb_http_schema",
		SchemaName:     "reb_http_schema",
		TableName:      "reb_http_table",
		ConstraintName: "reb_http_ux",
	}

	assertJSONClean := func(what string, mapped MappedError) string {
		t.Helper()
		body, err := json.Marshal(mapped.Body)
		if err != nil {
			t.Fatalf("marshal %s: %v", what, err)
		}
		for _, forbidden := range canaries {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s payload leaks %q: %s", what, forbidden, body)
			}
		}
		return string(body)
	}

	t.Run("untranslated driver error stays redacted", func(t *testing.T) {
		mapped := MapDataframeError(fmt.Errorf("seam: %w", secretPgErr), "req-pg-raw")
		if mapped.Status != http.StatusInternalServerError || mapped.Body.Error.Code != "INTERNAL_ERROR" {
			t.Fatalf("mapped = %#v, want redacted 500 INTERNAL_ERROR", mapped)
		}
		assertJSONClean("raw 23505", mapped)
	})

	t.Run("translated connectivity failure is retryable 503", func(t *testing.T) {
		failure := &postgres.ConnectError{Op: "open", Err: fmt.Errorf(
			"failed to connect to `user=reb_http_user password=reb_http_pass host=reb_http_host`: refused")}
		translated, ok := postgres.TranslateError(failure)
		if !ok {
			t.Fatal("connect failure not translated")
		}
		mapped := MapDataframeError(translated, "req-pg-connect")
		body := assertJSONClean("connect", mapped)
		if mapped.Status != http.StatusServiceUnavailable || mapped.Body.Error.Code != "BACKEND_UNAVAILABLE" || !mapped.Body.Error.Retryable {
			t.Fatalf("mapped = %s, want retryable 503 BACKEND_UNAVAILABLE", body)
		}
		if mapped.Body.Error.Details["backend"] != "postgres" {
			t.Fatalf("details lack postgres tag: %#v", mapped.Body.Error.Details)
		}
	})

	t.Run("translated serialization failure is never blanket retryable", func(t *testing.T) {
		translated, ok := postgres.TranslateError(fmt.Errorf("seam: %w",
			&pgconn.PgError{Severity: "ERROR", Code: "40001", Message: "reb_http_canary_select serialize conflict on reb_http_table"}))
		if !ok {
			t.Fatal("40001 not translated")
		}
		mapped := MapDataframeError(translated, "req-pg-ssi")
		body := assertJSONClean("40001", mapped)
		if mapped.Body.Error.Retryable || mapped.Status != http.StatusInternalServerError {
			t.Fatalf("mapped = %s, want non-retryable 500 (retry is an operation-policy decision)", body)
		}
	})

	t.Run("translated ambiguous commit is never retryable", func(t *testing.T) {
		translated, ok := postgres.TranslateCommitError(fmt.Errorf("commit: %w",
			&pgconn.PgError{Severity: "FATAL", Code: "57P01", Message: "reb_http_canary_select terminating reb_http_table"}))
		if !ok {
			t.Fatal("ambiguous commit not translated")
		}
		mapped := MapDataframeError(translated, "req-pg-commit")
		body := assertJSONClean("commit", mapped)
		if mapped.Body.Error.Retryable {
			t.Fatalf("mapped = %s, ambiguous commit must never advertise retry", body)
		}
	})

	t.Run("translated query cancel is non-retryable limit", func(t *testing.T) {
		translated, ok := postgres.TranslateError(fmt.Errorf("seam: %w",
			&pgconn.PgError{Severity: "ERROR", Code: "57014", Message: "reb_http_canary_select canceling statement"}))
		if !ok {
			t.Fatal("57014 not translated")
		}
		mapped := MapDataframeError(translated, "req-pg-cancel")
		body := assertJSONClean("57014", mapped)
		if mapped.Body.Error.Code != "QUERY_RESOURCE_LIMIT_EXCEEDED" || mapped.Body.Error.Retryable {
			t.Fatalf("mapped = %s, want non-retryable QUERY_RESOURCE_LIMIT_EXCEEDED", body)
		}
	})
}
