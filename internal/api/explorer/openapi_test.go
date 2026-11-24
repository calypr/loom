package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/viewer"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type faultInjectionAuthPathStore struct {
	calls int
	paths []string
	fault error
}

func (s *faultInjectionAuthPathStore) List(_ context.Context, _ authscope.AuthResourcePathOptions) ([]string, error) {
	s.calls++
	if s.fault != nil {
		return nil, s.fault
	}
	return append([]string(nil), s.paths...), nil
}

const (
	faultInjectionCallerPath         = "programs/f403-masking/allowed"
	faultInjectionRecordedPath       = "programs/f403-masking/recorded"
	faultInjectionForeignPath        = "programs/f403-masking/foreign"
	faultInjectionProject            = "f403-masking-project"
	faultInjectionAuthoringFaultCode = "BACKEND_UNAVAILABLE"
)

var faultInjectionForbiddenDiagnostic = explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}

var faultInjectionBackendDiagnostic = explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: faultInjectionAuthoringFaultCode, Message: "authorization backend unavailable"}

func faultInjectionHandlers(store *faultInjectionAuthPathStore) *Handlers {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: store.List})
	return &Handlers{
		authorizer:    authscope.ScopeAuthorizer{Resolver: resolver},
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return nil },
		application:   &lifecycle.Service{},
	}
}

func faultInjectionPrincipal() *authscope.Principal {
	return &authscope.Principal{Subject: "restricted-writer", AuthResourcePaths: []string{faultInjectionCallerPath}}
}

func faultInjectionContext() context.Context {
	return authscope.ContextWithPrincipal(context.Background(), faultInjectionPrincipal())
}

func faultInjectionRouteStack(handlers *Handlers) *fiber.App {
	app := fiber.New()
	principal := faultInjectionPrincipal()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
		return c.Next()
	})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})
	return app
}

func arangoQueryFault() error {
	return fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: http.StatusBadRequest, ErrorNum: 1561, ErrorMessage: "bind parameter 'dataset_generation' was not declared in the query"})
}

func authoringWriteStorageFaultCases() []struct {
	name  string
	fault error
} {
	return []struct {
		name  string
		fault error
	}{
		{name: "arangodb-client-error", fault: arangoQueryFault()},
		{name: "context-deadline", fault: fmt.Errorf("list existing auth resource paths: %w", context.DeadlineExceeded)},
		{name: "authorization-backend-sentinel", fault: fmt.Errorf("discover existing auth resource paths: %w", authscope.ErrAuthorizationBackendUnavailable)},
	}
}

func TestAuthoringWriteDirectStorageFaultNotMaskedAsForbidden(t *testing.T) {
	for _, test := range authoringWriteStorageFaultCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
			if err == nil {
				t.Fatal("storage fault at the store boundary did not fail the write authorization")
			}
			if store.calls == 0 {
				t.Fatal("the F-B27 project-binding branch never consulted the catalog store; fault did not traverse the intended boundary")
			}
			if errors.Is(err, authscope.ErrForbidden) {
				t.Fatalf("storage fault %v was classified as a permission denial: %v", test.fault, err)
			}
			var authoringErr *explorer.AuthoringError
			if !errors.As(err, &authoringErr) {
				t.Fatalf("err=%v (%T); want *explorer.AuthoringError fault envelope", err, err)
			}
			if authoringErr.Status == http.StatusForbidden || authoringErr.Status < http.StatusInternalServerError {
				t.Fatalf("storage fault masked as status %d (diagnostic %+v); want named 5xx-class fault, cause=%v", authoringErr.Status, authoringErr.Diagnostic, test.fault)
			}
			if authoringErr.Status != http.StatusServiceUnavailable {
				t.Fatalf("storage fault status=%d; want %d", authoringErr.Status, http.StatusServiceUnavailable)
			}
			if !reflect.DeepEqual(authoringErr.Diagnostic, faultInjectionBackendDiagnostic) {
				t.Fatalf("storage fault diagnostic=%+v; want exact %+v", authoringErr.Diagnostic, faultInjectionBackendDiagnostic)
			}
			if !errors.Is(err, test.fault) {
				t.Fatalf("cause not preserved through errors.Is: chain=%v, want %v", err, test.fault)
			}
		})
	}
}

func TestAuthoringWriteDeniedReferencesKeepForbiddenEnvelope(t *testing.T) {
	t.Run("outside-caller-scope", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{paths: []string{faultInjectionCallerPath}}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionForeignPath, nil)
		if !errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("write outside caller scope err=%v; want authscope.ErrForbidden denial", err)
		}
		if store.calls != 0 {
			t.Fatalf("out-of-scope denial consulted the catalog store %d times; must be a permission decision independent of storage", store.calls)
		}
		assertForbiddenAuthoringEnvelope(t, err)
	})
	t.Run("not-a-recorded-project-path", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{paths: []string{faultInjectionRecordedPath}}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
		if !errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("write into project without recorded path err=%v; want F-B27 authscope.ErrForbidden denial", err)
		}
		if store.calls != 1 {
			t.Fatalf("recorded-path denial consulted the healthy catalog store %d times; want 1", store.calls)
		}
		assertForbiddenAuthoringEnvelope(t, err)
	})
}

func assertForbiddenAuthoringEnvelope(t *testing.T, err error) {
	t.Helper()
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) {
		t.Fatalf("err=%v (%T); want *explorer.AuthoringError", err, err)
	}
	if authoringErr.Status != http.StatusForbidden {
		t.Fatalf("real denial status=%d; want %d (denials must never become 5xx)", authoringErr.Status, http.StatusForbidden)
	}
	if !reflect.DeepEqual(authoringErr.Diagnostic, faultInjectionForbiddenDiagnostic) {
		t.Fatalf("real denial diagnostic=%+v; want byte-identical F-B27 envelope %+v", authoringErr.Diagnostic, faultInjectionForbiddenDiagnostic)
	}
	if authoringErr.Cause == nil {
		t.Fatal("real denial lost the underlying cause")
	}
}

func decodeAuthoringFaultCode(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode authoring fault response: %v", err)
	}
	return body.Error.Code
}

func TestPublishExplorerRouteStorageFaultRendersNamed503(t *testing.T) {
	store := &faultInjectionAuthPathStore{fault: arangoQueryFault()}
	app := faultInjectionRouteStack(faultInjectionHandlers(store))
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/projects/f403-masking-project/explorers/explorer-1/authoring/v2/publish?auth_resource_path=programs%2Ff403-masking%2Fallowed",
		strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		code := decodeAuthoringFaultCode(t, response)
		t.Fatalf("storage fault route status=%d code=%q; want %d %q", response.StatusCode, code, http.StatusServiceUnavailable, faultInjectionAuthoringFaultCode)
	}
	if code := decodeAuthoringFaultCode(t, response); code != faultInjectionAuthoringFaultCode {
		t.Fatalf("storage fault route code=%q; want %q", code, faultInjectionAuthoringFaultCode)
	}
}

func TestPublishExplorerRouteRealDenialStillRenders403(t *testing.T) {
	store := &faultInjectionAuthPathStore{paths: []string{faultInjectionRecordedPath}}
	app := faultInjectionRouteStack(faultInjectionHandlers(store))
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/projects/f403-masking-project/explorers/explorer-1/authoring/v2/publish?auth_resource_path=programs%2Ff403-masking%2Fallowed",
		strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("real denial route status=%d; want %d", response.StatusCode, http.StatusForbidden)
	}
}

func arangoDriverFault(code, errorNum int, message string) error {
	return fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: code, ErrorNum: errorNum, ErrorMessage: message})
}

type authoringWriteArangoResponseCase struct {
	name        string
	fault       error
	wantStorage bool
}

func authoringWriteArangoResponseCases() []authoringWriteArangoResponseCase {
	return []authoringWriteArangoResponseCase{
		{name: "http-401-response", fault: arangoDriverFault(shared.ErrHttpUnauthorized, 0, "not authorized to execute this operation"), wantStorage: false},
		{name: "http-403-response", fault: arangoDriverFault(shared.ErrHttpForbidden, shared.ErrForbidden, "not authorized to execute this operation"), wantStorage: false},
		{name: "errornum-11-forbidden", fault: arangoDriverFault(0, shared.ErrForbidden, "forbidden"), wantStorage: false},
		{name: "query-400-response", fault: arangoDriverFault(http.StatusBadRequest, shared.ErrQueryInvalidArithmeticValue, "query execution failed"), wantStorage: true},
		{name: "bind-parameter-undeclared-1552", fault: arangoDriverFault(http.StatusBadRequest, shared.ErrQueryBindParameterUndeclared, "bind parameter 'dataset_generation' was not declared in the query"), wantStorage: true},
		{name: "query-bind-1561", fault: arangoDriverFault(http.StatusBadRequest, 1561, "invalid query bind"), wantStorage: true},
		{name: "joined-denial-and-outage-denial-wins", fault: errors.Join(authscope.ErrForbidden, authscope.ErrAuthorizationBackendUnavailable), wantStorage: false},
		{name: "context-canceled-remains-denial", fault: context.Canceled, wantStorage: false},
		{name: "deadline-is-retryable-outage", fault: context.DeadlineExceeded, wantStorage: true},
		{name: "pretyped-backend-code-without-cause-remains-denial", fault: dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "pretyped"), wantStorage: false},
		{name: "pretyped-error-with-backend-sentinel-is-outage", fault: dataframeerrors.Wrap(authscope.ErrAuthorizationBackendUnavailable, dataframeerrors.CodeForbidden, "pretyped"), wantStorage: true},
		{name: "pointer-query-fault", fault: fmt.Errorf("arango query: %w", error(&shared.ArangoError{HasError: true, Code: http.StatusBadRequest, ErrorNum: 1552, ErrorMessage: "bind fault"})), wantStorage: true},
		{name: "pointer-http-permission", fault: fmt.Errorf("arango: %w", error(&shared.ArangoError{Code: http.StatusForbidden, ErrorNum: shared.ErrForbidden})), wantStorage: false},
	}
}

func TestAuthoringWriteDirectArangoResponseClassification(t *testing.T) {
	for _, test := range authoringWriteArangoResponseCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
			if err == nil {
				t.Fatal("injected driver response did not fail write authorization")
			}
			var authoringErr *explorer.AuthoringError
			if !errors.As(err, &authoringErr) {
				t.Fatalf("err=%v (%T); want *explorer.AuthoringError", err, err)
			}
			if !errors.Is(err, test.fault) {
				t.Fatalf("cause not preserved: chain=%v, want %v", err, test.fault)
			}
			if test.wantStorage {
				if authoringErr.Status != http.StatusServiceUnavailable || !reflect.DeepEqual(authoringErr.Diagnostic, faultInjectionBackendDiagnostic) {
					t.Fatalf("query fault status=%d diagnostic=%+v; want 503 %+v", authoringErr.Status, authoringErr.Diagnostic, faultInjectionBackendDiagnostic)
				}
				return
			}
			if authoringErr.Diagnostic.Code == faultInjectionAuthoringFaultCode {
				t.Fatalf("store-level permission response %v relabeled as %s: %+v", test.fault, faultInjectionAuthoringFaultCode, authoringErr.Diagnostic)
			}
			if authoringErr.Status != http.StatusForbidden || !reflect.DeepEqual(authoringErr.Diagnostic, faultInjectionForbiddenDiagnostic) {
				t.Fatalf("permission-class response status=%d diagnostic=%+v; want byte-identical 403 %+v", authoringErr.Status, authoringErr.Diagnostic, faultInjectionForbiddenDiagnostic)
			}
		})
	}
}

func TestPublishExplorerRouteArangoResponseClassification(t *testing.T) {
	for _, test := range authoringWriteArangoResponseCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			app := faultInjectionRouteStack(faultInjectionHandlers(store))
			request := httptest.NewRequest(http.MethodPost,
				"/api/v1/projects/f403-masking-project/explorers/explorer-1/authoring/v2/publish?auth_resource_path=programs%2Ff403-masking%2Fallowed",
				strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			code := decodeAuthoringFaultCode(t, response)
			if test.wantStorage {
				if response.StatusCode != http.StatusServiceUnavailable || code != faultInjectionAuthoringFaultCode {
					t.Fatalf("query fault route status=%d code=%q; want 503 %q", response.StatusCode, code, faultInjectionAuthoringFaultCode)
				}
				return
			}
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("permission-class response route status=%d code=%q; want 403 with no %s", response.StatusCode, code, faultInjectionAuthoringFaultCode)
			}
			if code == faultInjectionAuthoringFaultCode {
				t.Fatalf("permission-class response rendered %s at the route", code)
			}
		})
	}
}

func TestCreateExplorerLegacyArangoResponseClassification(t *testing.T) {
	for _, test := range authoringWriteArangoResponseCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			rawBody := loomapi.RawJSON{"name": "f403"}
			body := loomapi.CreateExplorerJSONRequestBody(rawBody)
			_, err := handlers.createExplorer(faultInjectionContext(), faultInjectionProject, faultInjectionCallerPath, &body)
			if err == nil {
				t.Fatal("injected driver response did not fail write authorization")
			}
			var legacy *explorerLegacyOperationError
			if !errors.As(err, &legacy) {
				t.Fatalf("err=%v (%T); want *explorerLegacyOperationError", err, err)
			}
			if !errors.Is(err, test.fault) {
				t.Fatalf("cause chain %v does not preserve the injected driver fault", err)
			}
			if test.wantStorage {
				if legacy.status != http.StatusServiceUnavailable || legacy.code != faultInjectionAuthoringFaultCode {
					t.Fatalf("query fault legacy status=%d code=%q; want 503 %q", legacy.status, legacy.code, faultInjectionAuthoringFaultCode)
				}
				return
			}
			if legacy.code == faultInjectionAuthoringFaultCode {
				t.Fatalf("store-level permission response relabeled %s at legacy createExplorer", legacy.code)
			}
			if legacy.status != http.StatusForbidden || legacy.code != "FORBIDDEN" || legacy.message != "forbidden" {
				t.Fatalf("permission-class response legacy status=%d code=%q message=%q; want byte-identical 403 FORBIDDEN forbidden", legacy.status, legacy.code, legacy.message)
			}
		})
	}
}

func TestPublishRepositoryLegacyArangoResponseClassification(t *testing.T) {
	authPath := faultInjectionCallerPath
	request := loomapi.PublishRepositoryExplorerConfigRequestObject{
		Project:    loomapi.Project(faultInjectionProject),
		Generation: loomapi.Generation("gen-1"),
		Params:     loomapi.PublishRepositoryExplorerConfigParams{AuthResourcePath: &authPath},
	}
	for _, test := range authoringWriteArangoResponseCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			_, err := handlers.publishRepositoryExplorerConfig(faultInjectionContext(), request)
			if err == nil {
				t.Fatal("injected driver response did not fail publisher authorization")
			}
			var legacy *explorerLegacyOperationError
			if !errors.As(err, &legacy) {
				t.Fatalf("err=%v (%T); want *explorerLegacyOperationError", err, err)
			}
			if !errors.Is(err, test.fault) {
				t.Fatalf("cause chain %v does not preserve the injected driver fault", err)
			}
			if test.wantStorage {
				if legacy.status != http.StatusServiceUnavailable || legacy.code != faultInjectionAuthoringFaultCode {
					t.Fatalf("query fault legacy status=%d code=%q; want 503 %q", legacy.status, legacy.code, faultInjectionAuthoringFaultCode)
				}
				return
			}
			if legacy.code == faultInjectionAuthoringFaultCode || legacy.status != http.StatusForbidden {
				t.Fatalf("permission-class response legacy status=%d code=%q; want 403 with no %s", legacy.status, legacy.code, faultInjectionAuthoringFaultCode)
			}
		})
	}
}

func TestAuthoringWriteDirectArangoResponseCompositePrecedence(t *testing.T) {
	t.Run("joined-denial-beats-arango-403", func(t *testing.T) {
		composite := errors.Join(
			fmt.Errorf("authorize write: %w", authscope.ErrForbidden),
			arangoDriverFault(shared.ErrHttpForbidden, shared.ErrForbidden, "not authorized to execute this operation"),
		)
		store := &faultInjectionAuthPathStore{fault: composite}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
		if !errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("composite denial lost its sentinel: %v", err)
		}
		assertForbiddenAuthoringEnvelope(t, err)
		if !errors.Is(err, composite) {
			t.Fatalf("composite cause not preserved: %v", err)
		}
	})
	t.Run("joined-backend-sentinel-stays-named-5xx", func(t *testing.T) {
		composite := errors.Join(
			fmt.Errorf("discover existing auth resource paths: %w", authscope.ErrAuthorizationBackendUnavailable),
			arangoDriverFault(shared.ErrHttpForbidden, shared.ErrForbidden, "not authorized to execute this operation"),
		)
		store := &faultInjectionAuthPathStore{fault: composite}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishReceipt(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
		var authoringErr *explorer.AuthoringError
		if !errors.As(err, &authoringErr) {
			t.Fatalf("err=%v (%T); want *explorer.AuthoringError", err, err)
		}
		if authoringErr.Status != http.StatusServiceUnavailable || !reflect.DeepEqual(authoringErr.Diagnostic, faultInjectionBackendDiagnostic) {
			t.Fatalf("explicit backend sentinel status=%d diagnostic=%+v; want 503 %+v", authoringErr.Status, authoringErr.Diagnostic, faultInjectionBackendDiagnostic)
		}
		if !errors.Is(err, composite) {
			t.Fatalf("composite cause not preserved: %v", err)
		}
	})
}

func TestPublishRepositoryLegacyAuthorizerFaultNotMaskedAsForbidden(t *testing.T) {
	authPath := faultInjectionCallerPath
	request := loomapi.PublishRepositoryExplorerConfigRequestObject{
		Project:    loomapi.Project(faultInjectionProject),
		Generation: loomapi.Generation("gen-1"),
		Params:     loomapi.PublishRepositoryExplorerConfigParams{AuthResourcePath: &authPath},
	}
	t.Run("storage-fault", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{fault: arangoQueryFault()}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishRepositoryExplorerConfig(faultInjectionContext(), request)
		if err == nil {
			t.Fatal("storage fault at the store boundary did not fail publisher authorization")
		}
		if errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("storage fault classified as permission denial: %v", err)
		}
		status, body := explorerErrorResponse(err)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("storage fault legacy status=%d; want %d (named 503-class, route has a 503 arm)", status, http.StatusServiceUnavailable)
		}
		_ = body
		if ok, _ := shared.IsArangoError(err); !ok {
			t.Fatalf("cause chain %v does not preserve the injected storage fault", err)
		}
	})
	t.Run("real-denial", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{paths: []string{faultInjectionRecordedPath}}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishRepositoryExplorerConfig(faultInjectionContext(), request)
		if !errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("legacy denial err=%v; want authscope.ErrForbidden", err)
		}
		status, _ := explorerErrorResponse(err)
		if status != http.StatusForbidden {
			t.Fatalf("legacy denial status=%d; want %d (denials must never become 5xx)", status, http.StatusForbidden)
		}
	})
}

func TestCreateExplorerLegacyAuthorizerFaultNotMaskedAsForbidden(t *testing.T) {
	t.Run("storage-fault", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{fault: arangoQueryFault()}
		handlers := faultInjectionHandlers(store)
		rawBody := loomapi.RawJSON{"name": "f403"}
		body := loomapi.CreateExplorerJSONRequestBody(rawBody)
		_, err := handlers.createExplorer(faultInjectionContext(), faultInjectionProject, faultInjectionCallerPath, &body)
		if err == nil {
			t.Fatal("storage fault at the store boundary did not fail the write authorization")
		}
		if errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("storage fault classified as permission denial: %v", err)
		}
		var legacy *explorerLegacyOperationError
		if !errors.As(err, &legacy) {
			t.Fatalf("err=%v (%T); want *explorerLegacyOperationError", err, err)
		}
		if legacy.status != http.StatusServiceUnavailable {
			t.Fatalf("storage fault legacy status=%d; want %d", legacy.status, http.StatusServiceUnavailable)
		}
		if legacy.code != faultInjectionAuthoringFaultCode || legacy.message != "authorization backend unavailable" {
			t.Fatalf("storage fault legacy code=%q message=%q; want named %q fault", legacy.code, legacy.message, faultInjectionAuthoringFaultCode)
		}
		if ok, _ := shared.IsArangoError(err); !ok {
			t.Fatalf("cause chain %v does not preserve the injected storage fault", err)
		}
	})
	t.Run("real-denial", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{paths: []string{faultInjectionRecordedPath}}
		handlers := faultInjectionHandlers(store)
		rawBody := loomapi.RawJSON{"name": "f403"}
		body := loomapi.CreateExplorerJSONRequestBody(rawBody)
		_, err := handlers.createExplorer(faultInjectionContext(), faultInjectionProject, faultInjectionCallerPath, &body)
		if !errors.Is(err, authscope.ErrForbidden) {
			t.Fatalf("legacy denial err=%v; want authscope.ErrForbidden", err)
		}
		var legacy *explorerLegacyOperationError
		if !errors.As(err, &legacy) {
			t.Fatalf("err=%v (%T); want *explorerLegacyOperationError", err, err)
		}
		if legacy.status != http.StatusForbidden || legacy.code != "FORBIDDEN" || legacy.message != "forbidden" {
			t.Fatalf("legacy denial status=%d code=%q message=%q; want byte-identical 403 FORBIDDEN forbidden", legacy.status, legacy.code, legacy.message)
		}
	})
}

const restrictedStartingCollectionTestDatabasePrefix = "loom_test_restricted_starting_collection_"

func TestRestrictedStartingCollectionRejectsDeniedReferences(t *testing.T) {
	arangoURL := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	database := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if arangoURL == "" && database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and a randomized LOOM_TEST_ARANGO_DATABASE")
	}
	if arangoURL == "" || database == "" {
		t.Fatal("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must be set together")
	}
	suffix := strings.TrimPrefix(database, restrictedStartingCollectionTestDatabasePrefix)
	if suffix == database {
		t.Fatalf("refusing non-disposable Arango database %q; require %s<uuid>", database, restrictedStartingCollectionTestDatabasePrefix)
	}
	if _, err := uuid.Parse(suffix); err != nil {
		t.Fatalf("Arango database %q does not have the required randomized UUID suffix: %v", database, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := storearango.Open(ctx, arangoURL, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{
		{Name: "Patient"},
		{Name: "fhir_field_catalog"},
	}}); err != nil {
		t.Fatal(err)
	}

	const (
		project      = "project-a"
		resourceType = "Patient"
		allowedPath  = "programs/collection-repair/allowed"
		deniedPath   = "programs/collection-repair/denied"
	)
	generation := "restricted-starting-collection-" + suffix
	fixtureID := strings.ReplaceAll(uuid.NewString(), "-", "")
	allowedOne := "starting-allowed-one-" + fixtureID
	allowedTwo := "starting-allowed-two-" + fixtureID
	allowedThree := "starting-allowed-three-" + fixtureID
	deniedWitness := "starting-denied-witness-" + fixtureID
	resourceKeys := []string{"starting-" + fixtureID + "-1", "starting-" + fixtureID + "-2", "starting-" + fixtureID + "-3", "starting-" + fixtureID + "-4"}
	catalogKeys := []string{"starting-catalog-" + fixtureID + "-1", "starting-catalog-" + fixtureID + "-2"}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, item := range []struct {
			collection string
			keys       []string
		}{{"Patient", resourceKeys}, {"fhir_field_catalog", catalogKeys}} {
			if err := client.ExecuteAQL(cleanupCtx, `
FOR d IN @@collection
  FILTER d._key IN @keys
  REMOVE d IN @@collection`, map[string]any{"@collection": item.collection, "keys": item.keys}); err != nil {
				t.Errorf("remove owned %s fixture rows: %v", item.collection, err)
			}
		}
	})

	resourceIDs := []string{allowedOne, allowedTwo, allowedThree, deniedWitness}
	resourcePaths := []string{allowedPath, allowedPath, allowedPath, deniedPath}
	resourceDocs := make([]json.RawMessage, 0, len(resourceIDs))
	for index, id := range resourceIDs {
		doc, err := json.Marshal(map[string]any{
			"_key": resourceKeys[index], "id": id, "project": project,
			"dataset_generation": generation, "auth_resource_path": resourcePaths[index],
		})
		if err != nil {
			t.Fatal(err)
		}
		resourceDocs = append(resourceDocs, doc)
	}
	catalogDocs := make([]json.RawMessage, 0, 2)
	for index, path := range []string{allowedPath, deniedPath} {
		doc, err := json.Marshal(map[string]any{
			"_key": catalogKeys[index], "project": project,
			"dataset_generation": generation, "auth_resource_path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		catalogDocs = append(catalogDocs, doc)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", resourceDocs, false, "import"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_field_catalog", catalogDocs, false, "import"); err != nil {
		t.Fatal(err)
	}

	readIDs := func(path string) []string {
		t.Helper()
		ids := make([]string, 0, 4)
		err := client.QueryRows(ctx, `
FOR d IN Patient
  FILTER d.project == @project
  FILTER d.dataset_generation == @generation
  FILTER d.auth_resource_path == @path
  SORT d.id
  RETURN {id: d.id}`, 100, map[string]any{"project": project, "generation": generation, "path": path}, func(row map[string]any) error {
			id, ok := row["id"].(string)
			if !ok {
				return fmt.Errorf("source oracle returned non-string id %T", row["id"])
			}
			ids = append(ids, id)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	wantAllowedIDs := sortedIDs([]string{allowedOne, allowedTwo, allowedThree})
	if got, want := readIDs(allowedPath), wantAllowedIDs; !reflect.DeepEqual(got, want) {
		t.Fatalf("independent allowed-path fixture=%v; want %v", got, want)
	}
	if got := readIDs(deniedPath); !reflect.DeepEqual(got, []string{deniedWitness}) {
		t.Fatalf("independent denied-path fixture=%v; want [%s]", got, deniedWitness)
	}

	catalogStore, err := catalogarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	explorerStore, err := newTestExplorerArangoStore(client)
	if err != nil {
		t.Fatal(err)
	}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: catalogStore.DiscoverExistingAuthResourcePaths,
	})
	principal, err := (authscope.StaticAuthenticator{Principal: authscope.Principal{
		Subject: "restricted-starting-collection-test",

		AuthResourcePaths: []string{project, allowedPath},
	}}).Authenticate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx := authscope.ContextWithPrincipal(ctx, principal)
	scope, err := resolver.ResolveReadScopeForGeneration(requestCtx, principal, project, generation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{allowedPath}) {
		t.Fatalf("actual ScopeResolver returned %#v; want restricted scope [%s]", scope, allowedPath)
	}

	baseSnapshot := testAuthoringV2CapabilitySnapshot()
	identity := baseSnapshot.Identity
	identity.Project = project
	identity.Generation = generation
	identity.AuthorizationScopeDigest = capabilityresolver.ScopeDigest(scope)
	snapshot := capability.NewSnapshot(identity, baseSnapshot.Policy, baseSnapshot.Status, baseSnapshot.Complete, baseSnapshot.Truncated,
		baseSnapshot.Nodes, baseSnapshot.Edges, baseSnapshot.Candidates, baseSnapshot.Diagnostics)
	resolveCapability := func(ctx context.Context, requestedProject, token string) (ports.AuthorizedCapability, error) {
		currentPrincipal, _ := authscope.PrincipalFromContext(ctx)
		currentScope, err := resolver.ResolveReadScopeForGeneration(ctx, currentPrincipal, requestedProject, generation, nil)
		if err != nil {
			return ports.AuthorizedCapability{}, err
		}
		if token != snapshot.Token || capabilityresolver.ScopeDigest(currentScope) != snapshot.Identity.AuthorizationScopeDigest {
			return ports.AuthorizedCapability{}, capability.ErrStaleSnapshot
		}
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: currentScope}, nil
	}

	proposalStore := &explorerfixtures.SelectionStore{
		Store:      newTestExplorerStore(),
		Selections: map[string]explorerselection.SelectionRevision{},
		Members:    map[string][]explorerselection.SelectionMember{},
	}
	domain, err := explorerfixtures.NewService(proposalStore)
	if err != nil {
		t.Fatal(err)
	}
	dependencies := explorerfixtures.Dependencies(proposalStore)
	config := lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
		Capability: ports.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) {
				return snapshot, nil
			},
			ForCompilation: resolveCapability,
			ForExecution:   resolveCapability,
		},
		SelectionReferenceValidator: explorerStore.ValidateSelectionReferences,
	}
	config = lifecycleConfigForTest(config, dependencies)
	handlers := NewHandlers(Config{
		Authorizer: authscope.ScopeAuthorizer{Resolver: resolver}, AuthorizeRead: resolver.AuthorizeReadProject,
		Explorers: domain, Revisions: dependencies.Revisions, ViewBuilder: dependencies.ViewBuilder, Lifecycle: config,
	})

	toWireRef := func(id string) loomapi.SelectionResourceRef {
		return loomapi.SelectionResourceRef{Project: project, Generation: generation, ResourceType: resourceType, Id: id}
	}
	selectionRequest := func(key string, ids ...string) loomapi.CreateExplorerSelectionRequestObject {
		refs := make([]loomapi.SelectionResourceRef, 0, len(ids))
		for _, id := range ids {
			refs = append(refs, toWireRef(id))
		}
		path := loomapi.AuthResourcePath(allowedPath)
		resourceTypeValue := resourceType
		return loomapi.CreateExplorerSelectionRequestObject{
			Project: loomapi.Project(project), ExplorerId: loomapi.ExplorerId("restricted-starting-collection"),
			Params: loomapi.CreateExplorerSelectionParams{AuthResourcePath: &path},
			Body: &loomapi.SelectionCreateRequest{
				IdempotencyKey: key, SnapshotToken: snapshot.Token,
				Source: loomapi.SelectionRequestSource{
					Kind:      loomapi.SelectionRequestSourceKindResources,
					Resources: &loomapi.SelectionExplicitResources{ResourceType: &resourceTypeValue, Refs: refs},
				},
			},
		}
	}

	deniedWrite := selectionRequest("denied-write-path", allowedOne)
	deniedPathParam := loomapi.AuthResourcePath(deniedPath)
	deniedWrite.Params.AuthResourcePath = &deniedPathParam
	response, err := handlers.CreateExplorerSelection(requestCtx, deniedWrite)
	if _, ok := response.(loomapi.CreateExplorerSelection403JSONResponse); !ok || err != nil {
		t.Fatalf("write under denied auth_resource_path err=%v; want actual ScopeAuthorizer denial", err)
	}

	deniedRefRequest := selectionRequest("denied-reference-witness", allowedOne, deniedWitness)
	response, err = handlers.CreateExplorerSelection(requestCtx, deniedRefRequest)
	conflict, ok := response.(loomapi.CreateExplorerSelection409JSONResponse)
	if !ok || err != nil || conflict.Error.Code != "SELECTION_STALE_SCOPE" ||
		conflict.Error.Message != "selection references are outside the authorized snapshot" {
		t.Fatalf("allowed-path create containing denied witness response=%#v err=%v; want real Arango scope-reference rejection", response, err)
	}
	proposalStore.SelectionMu.Lock()
	partialSelectionCount := len(proposalStore.Selections)
	proposalStore.SelectionMu.Unlock()
	if partialSelectionCount != 0 {
		t.Fatalf("denied create persisted %d selection headers before rejection", partialSelectionCount)
	}

	createdResponse, err := handlers.CreateExplorerSelection(requestCtx, selectionRequest("allowed-starting-collection", allowedOne, allowedTwo, allowedThree))
	created, ok := createdResponse.(loomapi.CreateExplorerSelection201JSONResponse)
	if !ok {
		t.Fatalf("create selection response = %T, want 201", createdResponse)
	}
	if err != nil {
		t.Fatalf("create allowed-only starting collection: %v", err)
	}
	if created.MemberCount != int64(len(wantAllowedIDs)) || created.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		t.Fatalf("allowed selection header lost exact count/scope: %#v", created)
	}
	selectionPageResponse, err := handlers.GetExplorerSelection(requestCtx, loomapi.GetExplorerSelectionRequestObject{
		Project: loomapi.Project(project), ExplorerId: loomapi.ExplorerId("restricted-starting-collection"), SelectionRevision: created.Id,
	})
	if err != nil {
		t.Fatal(err)
	}
	selectionPage, ok := selectionPageResponse.(loomapi.GetExplorerSelection200JSONResponse)
	if !ok {
		t.Fatalf("get selection response = %T, want 200", selectionPageResponse)
	}
	visibleIDs := sortedIDs(selectionMemberIDs(selectionPage.Members))
	if !reflect.DeepEqual(visibleIDs, wantAllowedIDs) {
		t.Fatalf("visible starting collection=%v; want exact allowed rows %v", visibleIDs, wantAllowedIDs)
	}
	assertNoDeniedID(t, deniedWitness, visibleIDs)
}

func sortedIDs(ids []string) []string {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	return result
}

func selectionMemberIDs(members []loomapi.SelectionMember) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.Ref.Id)
	}
	return ids
}

func assertNoDeniedID(t *testing.T, denied string, values []string) {
	t.Helper()
	for _, value := range values {
		if value == denied {
			t.Fatalf("denied witness %s leaked into %v", denied, values)
		}
	}
}

type viewBuilderHTTPStore struct {
	*testExplorerStore
	explorer.ViewBuilderRepository
	settings viewer.ViewBuilderSettings
	fault    error
	writes   int
}

func (s *viewBuilderHTTPStore) GetViewBuilderSettings(context.Context, string) (viewer.ViewBuilderSettings, error) {
	return s.settings, s.fault
}

func (s *viewBuilderHTTPStore) AdoptViewBuilderCatalogs(context.Context, string) error {
	s.writes++
	return nil
}

func (s *viewBuilderHTTPStore) SaveViewBuilderDefaults(context.Context, string, viewer.ViewBuilderDefaultsRequest) error {
	s.writes++
	return nil
}

type viewBuilderTestAuthorizer struct{ fault error }

func (a viewBuilderTestAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return a.fault
}

func viewBuilderTestApp(t *testing.T, store *viewBuilderHTTPStore, writeFault, readFault error) *fiber.App {
	t.Helper()
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handlers := newExplorerHTTPHandlers(viewBuilderTestAuthorizer{writeFault}, func(context.Context, *authscope.Principal, string) error { return readFault }, service, store)
	app := fiber.New()
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})
	return app
}

func TestViewBuilderHTTPReadOnlyAndWriteAuthorization(t *testing.T) {
	store := &viewBuilderHTTPStore{testExplorerStore: explorerfixtures.NewStore(), settings: viewer.ViewBuilderSettings{Catalogs: map[string]string{}}}
	app := viewBuilderTestApp(t, store, authscope.ErrForbidden, nil)
	result := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/view-builder", "")
	if result.StatusCode != http.StatusOK || !viewBuilderCanEditFalse(t, result.Body) {
		t.Fatalf("read=%d %s", result.StatusCode, result.Body)
	}
	for _, path := range []string{"/view-builder/adopt", "/view-builder/defaults", "/view-builder/configs/files"} {
		method := http.MethodPut
		if path == "/view-builder/adopt" {
			method = http.MethodPost
		}
		result = requestJSON(t, app, method, "/api/v1/projects/project-a"+path, `{}`)
		if result.StatusCode != http.StatusForbidden {
			t.Fatalf("%s=%d %s", path, result.StatusCode, result.Body)
		}
	}
	if store.writes != 0 {
		t.Fatalf("denied writes reached store: %d", store.writes)
	}
}

func TestViewBuilderHTTPStorageFailuresAndMalformedRequests(t *testing.T) {
	store := &viewBuilderHTTPStore{testExplorerStore: explorerfixtures.NewStore(), settings: viewer.ViewBuilderSettings{Catalogs: map[string]string{}}}
	app := viewBuilderTestApp(t, store, nil, nil)
	store.fault = errors.New("private database failure with credentials")
	result := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/view-builder", "")
	if result.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("storagefault=%d %s", result.StatusCode, result.Body)
	}
	if strings.Contains(result.Body, "credentials") {
		t.Fatalf("storage failure leaked: %s", result.Body)
	}
	store.fault = nil
	result = requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/view-builder/defaults", `{"expectedVersion":0,"defaultConfigId":"files","defaultFrameId":"recipe/Files","unknown":true}`)
	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknownfield=%d %s", result.StatusCode, result.Body)
	}
	if store.writes != 0 {
		t.Fatalf("malformed request reached store: %d", store.writes)
	}
}

func viewBuilderCanEditFalse(t *testing.T, body string) bool {
	t.Helper()
	var value struct {
		CanEdit *bool `json:"canEdit"`
	}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	return value.CanEdit != nil && !*value.CanEdit
}

func TestViewBuilderExecutionAuthorizationKeepsBackendFailuresDistinct(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
		status  int
	}{
		{"allowed", nil, 0},
		{"concealed", dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, ""), http.StatusNotFound},
		{"backend", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, ""), http.StatusServiceUnavailable},
		{"unexpected", errors.New("private database detail"), http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handlers := &Handlers{authorizeExecution: func(context.Context, string) error { return test.failure }}
			err := handlers.authorizeViewBuilderExecution(context.Background(), "execution-a")
			if test.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			status, _ := explorerErrorResponse(err)
			if status != test.status {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}

func TestViewBuilderScopedEditorCapability(t *testing.T) {
	store := &viewBuilderHTTPStore{testExplorerStore: explorerfixtures.NewStore(), settings: viewer.ViewBuilderSettings{Catalogs: map[string]string{}}}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
		return []string{"project-a"}, nil
	}})
	principal := &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"project-a"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	if err := resolver.AuthorizeWrite(ctx, principal, "project-a", "project-a"); err != nil {
		t.Fatalf("editor grant invalid: %v", err)
	}
	handlers := newExplorerHTTPHandlers(authscope.ScopeAuthorizer{Resolver: resolver}, resolver.AuthorizeReadProject, service, store)
	state, err := handlers.authorizedViewBuilderState(ctx, "project-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if !state.CanEdit {
		t.Fatal("scoped editor with valid project write grant receives canEdit=false on GET")
	}
	if err := handlers.authorizeViewBuilder(ctx, "project-a", "", true); err != nil {
		t.Fatalf("scoped mutation without override: %v", err)
	}
	if err := handlers.authorizeViewBuilder(ctx, "project-a", "foreign-project", true); err == nil {
		t.Fatal("foreign scope override was accepted")
	}
}

func TestViewBuilderHTTPGetUsesAuthResourcePathForCanEdit(t *testing.T) {
	const project = "HTAN_INT/BForePC"
	const authResourcePath = "/programs/HTAN_INT/projects/BForePC"

	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
			return []string{authResourcePath}, nil
		},
	})
	principal := &authscope.Principal{
		Subject:           "editor",
		Projects:          []string{project},
		AuthResourcePaths: []string{authResourcePath},
	}
	store := &viewBuilderHTTPStore{
		testExplorerStore: explorerfixtures.NewStore(),
		settings:          viewer.ViewBuilderSettings{Catalogs: map[string]string{}},
	}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handlers := newExplorerHTTPHandlers(
		authscope.ScopeAuthorizer{Resolver: resolver},
		func(context.Context, *authscope.Principal, string) error { return nil },
		service, store,
	)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
		return c.Next()
	})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})

	get := func(query string) (int, bool) {
		t.Helper()
		path := "/api/v1/projects/HTAN_INT%252FBForePC/view-builder"
		if query != "" {
			path += "?" + query
		}
		result := requestJSON(t, app, http.MethodGet, path, "")
		var body struct {
			CanEdit bool `json:"canEdit"`
		}
		if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
			t.Fatalf("decode GET response %q: %v", result.Body, err)
		}
		return result.StatusCode, body.CanEdit
	}

	status, canEdit := get(url.Values{"auth_resource_path": {authResourcePath}}.Encode())
	if status != http.StatusOK || !canEdit {
		t.Fatalf("scoped editor GET status=%d canEdit=%v; want HTTP 200 and canEdit=true", status, canEdit)
	}
	status, canEdit = get("")
	if status != http.StatusOK || !canEdit {
		t.Fatalf("scoped GET without auth_resource_path status=%d canEdit=%v; want legacy project fallback to remain editable", status, canEdit)
	}
	status, canEdit = get(url.Values{"auth_resource_path": {"/programs/OTHER/projects/foreign"}}.Encode())
	if status != http.StatusOK || canEdit {
		t.Fatalf("GET with foreign auth_resource_path status=%d canEdit=%v; want HTTP 200 and canEdit=false", status, canEdit)
	}

	unscopedStore := &viewBuilderHTTPStore{
		testExplorerStore: explorerfixtures.NewStore(),
		settings:          viewer.ViewBuilderSettings{Catalogs: map[string]string{}},
	}
	unscopedApp := viewBuilderTestApp(t, unscopedStore, nil, nil)
	status, canEdit = func() (int, bool) {
		result := requestJSON(t, unscopedApp, http.MethodGet, "/api/v1/projects/project-a/view-builder", "")
		var body struct {
			CanEdit bool `json:"canEdit"`
		}
		if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
			t.Fatalf("decode unscoped GET response %q: %v", result.Body, err)
		}
		return result.StatusCode, body.CanEdit
	}()
	if status != http.StatusOK || !canEdit {
		t.Fatalf("unscoped GET status=%d canEdit=%v; want HTTP 200 and canEdit=true", status, canEdit)
	}
}

func TestExplorerOperationKeepsUnavailableStatusWithoutApplication(t *testing.T) {
	handlers := &Handlers{}
	response, err := handlers.CreateExplorerSelection(context.Background(), loomapi.CreateExplorerSelectionRequestObject{})
	if err != nil {
		t.Fatalf("CreateExplorerSelection() error = %v", err)
	}
	if _, ok := response.(loomapi.CreateExplorerSelection503JSONResponse); !ok {
		t.Fatalf("CreateExplorerSelection() response = %T, want 503", response)
	}
}
