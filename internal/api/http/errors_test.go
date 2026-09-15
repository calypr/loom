package httpapi

import (
	"errors"
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
	if mapped.Body.Error.Code != "INTERNAL_ERROR" || mapped.Body.Error.Message != "internal server error" {
		t.Fatalf("body = %#v", mapped.Body.Error)
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
