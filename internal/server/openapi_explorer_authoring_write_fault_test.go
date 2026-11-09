package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

// faultInjectionAuthPathStore is the overlay stub at the catalog-store
// boundary the ScopeResolver consults for the F-B27 project binding
// (ScopeResolverConfig.ListExistingAuthResourcePaths — the production seam
// wired to catalogStore.DiscoverExistingAuthResourcePaths, which is exactly
// where the dc52ed0a dataset_generation bind fault originated).
type faultInjectionAuthPathStore struct {
	calls int
	paths []string
	fault error
}

func (s *faultInjectionAuthPathStore) List(_ context.Context, _ catalog.AuthResourcePathOptions) ([]string, error) {
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

func faultInjectionHandlers(store *faultInjectionAuthPathStore) *explorerHTTPHandlers {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: store.List})
	return &explorerHTTPHandlers{
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

func faultInjectionRouteStack(handlers *explorerHTTPHandlers) *fiber.App {
	app := fiber.New()
	routes := &HTTPRoutes{explorer: handlers}
	principal := faultInjectionPrincipal()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
		return c.Next()
	})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	return app
}

// arangoQueryFault mirrors the exact production chain of the masked bind
// fault: store/arango queryRows wraps the Arango client error with
// "arango query: %w" (client.go:345) and the catalog store propagates it raw.
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

// TestAuthoringWriteDirectStorageFaultNotMaskedAsForbidden is the durable for
// the filed masking class: a storage-class fault injected at the catalog-store
// boundary reaches authoringWriteDirect through the real ScopeResolver F-B27
// project-binding branch (scope_resolver.go:242 region) and must not be
// classified as a permission denial. The named fault is 503-class and the
// injected cause stays visible through errors.Is. Pre-fix (HEAD d3a1d12a)
// every case returned the 403 FORBIDDEN envelope.
func TestAuthoringWriteDirectStorageFaultNotMaskedAsForbidden(t *testing.T) {
	for _, test := range authoringWriteStorageFaultCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
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

// TestAuthoringWriteWriteDeniedReferencesKeepForbiddenEnvelope proves the
// precision half of the repair from both directions: genuine denials from the
// real ScopeResolver (outside caller scope, and the F-B27 recorded-path
// branch with a healthy store) keep the byte-identical 403 FORBIDDEN
// envelope and are never reinterpreted as 5xx. These assertions hold before
// and after the classification change.
func TestAuthoringWriteDeniedReferencesKeepForbiddenEnvelope(t *testing.T) {
	t.Run("outside-caller-scope", func(t *testing.T) {
		store := &faultInjectionAuthPathStore{paths: []string{faultInjectionCallerPath}}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionForeignPath, nil)
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
		_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
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

// TestPublishExplorerRouteStorageFaultRendersNamed503 proves the class fix at
// the HTTP boundary for the generated authoring mutation routes: the store
// fault renders the named 5xx envelope instead of 403, while a real denial
// renders the unchanged 403.
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

// arangoDriverFault builds a store-seam fault that carries the go-driver
// response fields (HTTP Code + Arango ErrorNum) the way store/arango
// queryRows propagates them ("arango query: %w" at client.go:345). The
// ErrorMessage is wire content only; the classifier must decide on the
// numeric fields, never on it.
func arangoDriverFault(code, errorNum int, message string) error {
	return fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: code, ErrorNum: errorNum, ErrorMessage: message})
}

type authoringWriteArangoResponseCase struct {
	name        string
	fault       error
	wantStorage bool
}

// authoringWriteArangoResponseCases is the C2 matrix: auth-class driver
// responses (HTTP 401/403, ErrorNum 11 ErrForbidden) must classify as a
// permission decision (403 arm), while query-fault-class responses (Code 400
// family, bind-parameter/undeclared error numbers) stay storage faults → 5xx.
// The bind-1552 case mirrors the dc52ed0a production fault verbatim.
func authoringWriteArangoResponseCases() []authoringWriteArangoResponseCase {
	return []authoringWriteArangoResponseCase{
		{name: "http-401-response", fault: arangoDriverFault(shared.ErrHttpUnauthorized, 0, "not authorized to execute this operation"), wantStorage: false},
		{name: "http-403-response", fault: arangoDriverFault(shared.ErrHttpForbidden, shared.ErrForbidden, "not authorized to execute this operation"), wantStorage: false},
		{name: "errornum-11-forbidden", fault: arangoDriverFault(0, shared.ErrForbidden, "forbidden"), wantStorage: false},
		{name: "query-400-response", fault: arangoDriverFault(http.StatusBadRequest, shared.ErrQueryInvalidArithmeticValue, "query execution failed"), wantStorage: true},
		{name: "bind-parameter-undeclared-1552", fault: arangoDriverFault(http.StatusBadRequest, shared.ErrQueryBindParameterUndeclared, "bind parameter 'dataset_generation' was not declared in the query"), wantStorage: true},
		{name: "query-bind-1561", fault: arangoDriverFault(http.StatusBadRequest, 1561, "invalid query bind"), wantStorage: true},
	}
}

// TestAuthoringWriteDirectArangoResponseClassification pins the C2 repair at
// the filed choke point: store-native permission responses stay in the
// byte-identical 403 FORBIDDEN arm (never BACKEND_UNAVAILABLE), query faults
// stay named 5xx, and the cause chain survives in both directions. Pre-fix
// (blanket errors.As arm, no response-field guard) the three auth-class
// cases returned 503 BACKEND_UNAVAILABLE.
func TestAuthoringWriteDirectArangoResponseClassification(t *testing.T) {
	for _, test := range authoringWriteArangoResponseCases() {
		t.Run(test.name, func(t *testing.T) {
			store := &faultInjectionAuthPathStore{fault: test.fault}
			handlers := faultInjectionHandlers(store)
			_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
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

// TestPublishExplorerRouteArangoResponseClassification pins the same matrix
// through the generated authoring route (HTTP render + response body code).
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

// TestCreateExplorerLegacyArangoResponseClassification pins the matrix at the
// legacy createExplorer AuthorizeWrite site (500-class arm).
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
			if ok, _ := shared.IsArangoError(err); !ok {
				t.Fatalf("cause chain %v does not preserve the injected driver fault", err)
			}
			if test.wantStorage {
				if legacy.status != http.StatusInternalServerError || legacy.code != faultInjectionAuthoringFaultCode {
					t.Fatalf("query fault legacy status=%d code=%q; want 500 %q", legacy.status, legacy.code, faultInjectionAuthoringFaultCode)
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

// TestPublishRepositoryLegacyArangoResponseClassification pins the matrix at
// the legacy publishRepositoryExplorerConfig AuthorizeWrite site (503-class
// arm for genuine storage faults; permission responses stay 403).
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
			if ok, _ := shared.IsArangoError(err); !ok {
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

// TestAuthoringWriteDirectArangoResponseCompositePrecedence pins the C1
// ordering after the response-field guard: a composite carrying the
// authscope denial sentinel keeps the 403 arm even when joined with an
// Arango 403 response, while a composite explicitly claiming the backend
// sentinel stays the named 5xx availability fault. Both keep their causes.
func TestAuthoringWriteDirectArangoResponseCompositePrecedence(t *testing.T) {
	t.Run("joined-denial-beats-arango-403", func(t *testing.T) {
		composite := errors.Join(
			fmt.Errorf("authorize write: %w", authscope.ErrForbidden),
			arangoDriverFault(shared.ErrHttpForbidden, shared.ErrForbidden, "not authorized to execute this operation"),
		)
		store := &faultInjectionAuthPathStore{fault: composite}
		handlers := faultInjectionHandlers(store)
		_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
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
		_, err := handlers.publishAuthoringDirect(faultInjectionContext(), faultInjectionProject, "explorer-1", faultInjectionCallerPath, nil)
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

// third AuthorizeWrite masking site (legacy publishRepositoryExplorerConfig).
// Its OpenAPI route supports a 503 arm, so a storage fault renders the named
// 503-class legacy envelope; a real denial keeps the byte-identical
// 403 forbidden string form.
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
		status, body := explorerLegacyResponse(err)
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
		status, _ := explorerLegacyResponse(err)
		if status != http.StatusForbidden {
			t.Fatalf("legacy denial status=%d; want %d (denials must never become 5xx)", status, http.StatusForbidden)
		}
	})
}

// legacy twin of the same mask on POST /explorers (openapi_explorer_direct.go
// authorizer call, whose generated route has no 503 arm, so the named fault
// is the 500-class envelope with the cause preserved). Real denials keep the
// byte-identical legacy FORBIDDEN.
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
		if legacy.status != http.StatusInternalServerError {
			t.Fatalf("storage fault legacy status=%d; want %d (named 500-class; the legacy route has no 503 arm)", legacy.status, http.StatusInternalServerError)
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
