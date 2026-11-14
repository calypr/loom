package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

type viewBuilderHTTPStore struct {
	*testExplorerStore
	explorer.ViewBuilderRepository
	settings explorer.ViewBuilderSettings
	fault    error
	writes   int
}

func (s *viewBuilderHTTPStore) GetViewBuilderSettings(context.Context, string) (explorer.ViewBuilderSettings, error) {
	return s.settings, s.fault
}
func (s *viewBuilderHTTPStore) AdoptViewBuilderCatalogs(context.Context, string) error {
	s.writes++
	return nil
}
func (s *viewBuilderHTTPStore) SaveViewBuilderDefaults(context.Context, string, explorer.ViewBuilderDefaultsRequest) error {
	s.writes++
	return nil
}

type viewBuilderTestAuthorizer struct{ fault error }

func (a viewBuilderTestAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return a.fault
}

func viewBuilderTestApp(t *testing.T, store *viewBuilderHTTPStore, writeFault, readFault error) *fiber.App {
	t.Helper()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handlers := newExplorerHTTPHandlers(viewBuilderTestAuthorizer{writeFault}, func(context.Context, *authscope.Principal, string) error { return readFault }, service)
	app := fiber.New()
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(&HTTPRoutes{explorer: handlers}, nil), loomapi.FiberServerOptions{})
	return app
}

func TestViewBuilderHTTPReadOnlyAndWriteAuthorization(t *testing.T) {
	store := &viewBuilderHTTPStore{testExplorerStore: newTestExplorerStore(), settings: explorer.ViewBuilderSettings{Catalogs: map[string]string{}}}
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
	store := &viewBuilderHTTPStore{testExplorerStore: newTestExplorerStore(), settings: explorer.ViewBuilderSettings{Catalogs: map[string]string{}}}
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
			handlers := &explorerHTTPHandlers{authorizeExecution: func(context.Context, string) error { return test.failure }}
			err := handlers.authorizeViewBuilderExecution(context.Background(), "execution-a")
			if test.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			status, _ := explorerLegacyResponse(err)
			if status != test.status {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}

func TestViewBuilderScopedEditorCapability(t *testing.T) {
	store := &viewBuilderHTTPStore{testExplorerStore: newTestExplorerStore(), settings: explorer.ViewBuilderSettings{Catalogs: map[string]string{}}}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
		return []string{"project-a"}, nil
	}})
	principal := &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"project-a"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	if err := resolver.AuthorizeWrite(ctx, principal, "project-a", "project-a"); err != nil {
		t.Fatalf("editor grant invalid: %v", err)
	}
	handlers := newExplorerHTTPHandlers(authscope.ScopeAuthorizer{Resolver: resolver}, resolver.AuthorizeReadProject, service)
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
