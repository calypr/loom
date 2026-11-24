package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	recipeexec "github.com/calypr/loom/internal/api/dataframe/recipeexec"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/viewer"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
)

type recipeExecutionCatalogFixture struct {
	execution publication.BundleExecution
}

func (c recipeExecutionCatalogFixture) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return c.execution, nil
}

func TestPersistedExplorerRequiresExecutionVisibility(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "files", "Files", "", "test"); err != nil {
		t.Fatal(err)
	}
	revision := explorer.Revision{ID: "revision-a", Project: "project-a", ExplorerID: "files", Status: explorer.RevisionActive,
		Recipe:      recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "metadata", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "files", RootResourceType: "DocumentReference", RowGrain: "document"}}},
		Publication: viewer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "files")] = owner
	store.Mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"visible", nil, 0},
		{"concealed", dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, ""), 404},
		{"backend failure", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, ""), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handlers.authorizeExecution = func(_ context.Context, id string) error {
				calls++
				if id != "execution-a" {
					t.Fatalf("execution %q", id)
				}
				return tc.failure
			}
			_, err := handlers.getExplorer(context.Background(), "project-a", "files")
			if calls != 1 {
				t.Fatalf("execution authorizer calls=%d err=%v", calls, err)
			}
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *explorerLegacyOperationError
			if !errors.As(err, &failure) || failure.status != tc.status {
				t.Fatalf("failure=%v expected status %d", err, tc.status)
			}
		})
	}
}

func TestMissingProjectConfigIsNotCreatedOnRead(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	_, err = handlers.getExplorer(context.Background(), "project-a", "default")
	var failure *explorerLegacyOperationError
	if !errors.As(err, &failure) || failure.status != 404 {
		t.Fatalf("missing config response=%v", err)
	}
	store.Mu.Lock()
	defer store.Mu.Unlock()
	if len(store.Explorers) != 0 || len(store.Revisions) != 0 {
		t.Fatal("reading a missing config wrote state")
	}
}

func TestPersistedExplorerConcealsPartialExecutionScope(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "files", "Files", "", "test"); err != nil {
		t.Fatal(err)
	}
	revision := explorer.Revision{ID: "revision-a", Project: "project-a", ExplorerID: "files", Status: explorer.RevisionActive,
		Recipe:      recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "metadata", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "files", RootResourceType: "DocumentReference", RowGrain: "document"}}},
		Publication: viewer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "files")] = owner
	store.Mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
		return []string{"allowed", "denied"}, nil
	}})
	execution := publication.BundleExecution{ID: "execution-a", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "g", AuthResourcePaths: []string{"allowed", "denied"}}, State: publication.BundlePublished}
	for _, tc := range []struct {
		name      string
		principal *authscope.Principal
		resolver  *authscope.ScopeResolver
		status    int
	}{
		{name: "no-auth", status: 0},
		{name: "partial", principal: &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"allowed"}}, resolver: scopes, status: 404},
		{name: "nonmember", principal: &authscope.Principal{Projects: []string{"other"}}, status: 404},
		{name: "full", principal: &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"allowed", "denied"}}, resolver: scopes, status: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := recipeexec.NewRecipeExecutionAuthorizer(recipeExecutionCatalogFixture{execution: execution}, tc.resolver)
			handlers.authorizeExecution = reader
			ctx := authscope.ContextWithPrincipal(context.Background(), tc.principal)
			_, err := handlers.getExplorer(ctx, "project-a", "files")
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *explorerLegacyOperationError
			if !errors.As(err, &failure) || failure.status != tc.status {
				t.Fatalf("err=%v failure=%#v expected=%d", err, failure, tc.status)
			}
		})
	}
}

func TestExplorerErrorResponsePreservesLifecycleDetails(t *testing.T) {
	status, response := explorerErrorResponse(legacyOperationFailureDetails(http.StatusConflict, "COMPILATION_FAILED", "catalog field is unavailable", map[string]any{"retryable": false}, nil))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if response.Error.Code != "COMPILATION_FAILED" || response.Error.Message != "catalog field is unavailable" {
		t.Fatalf("error = %#v", response.Error)
	}
	if response.Error.Details == nil || (*response.Error.Details)["retryable"] != false {
		t.Fatalf("details = %#v, want explicit retryable=false", response.Error.Details)
	}
}

func TestExplorerLegacyResponseUnknownErrorIsDescriptive(t *testing.T) {
	private := errors.New("private socket detail")
	status, response := explorerErrorResponse(private)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q, want INTERNAL_ERROR", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, "unexpected internal error") || !strings.Contains(response.Error.Message, "request ID") {
		t.Fatalf("message = %q, want descriptive internal-error guidance", response.Error.Message)
	}
	if strings.Contains(response.Error.Message, private.Error()) {
		t.Fatalf("response leaked private cause: %q", response.Error.Message)
	}
}

func TestExplorerLegacyResponseFillsEmptyInternalMessage(t *testing.T) {
	status, response := explorerErrorResponse(legacyOperationFailure(http.StatusInternalServerError, "", "", nil))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" || !strings.Contains(response.Error.Message, "unexpected internal error") {
		t.Fatalf("body = %#v, want descriptive INTERNAL_ERROR", response.Error)
	}
}

func TestAuthoringErrorMapsPublicationInProgressToConflict(t *testing.T) {
	status, response := authoringErrorForOpenAPI(context.Background(), "publish", &ports.Error{
		Class:   ports.ClassConflict,
		Stage:   "materialize",
		Code:    "PUBLICATION_IN_PROGRESS",
		Message: "Explorer publication is already in progress; retry after it completes",
		Details: map[string]any{"executionId": "execution-a", "retryable": true},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if response.Error.Code != "PUBLICATION_IN_PROGRESS" || response.Error.Diagnostic == nil {
		t.Fatalf("error response = %#v", response)
	}
	if response.Error.Details == nil {
		t.Fatal("error omitted publication details")
	}
	if got := (*response.Error.Details)["executionId"]; got != "execution-a" {
		t.Fatalf("executionId = %v, want execution-a", got)
	}
	if got := (*response.Error.Diagnostic.Details)["retryable"]; got != true {
		t.Fatalf("retryable = %v, want true", got)
	}
}

func marshalExplorerErrorBody(t *testing.T, err error) (map[string]any, int) {
	t.Helper()
	status, response := explorerErrorResponse(err)

	raw, merr := json.Marshal(response)
	if merr != nil {
		t.Fatalf("marshal Explorer envelope: %v", merr)
	}
	var envelope map[string]any
	if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
		t.Fatalf("unmarshal Explorer envelope: %v", uerr)
	}
	body, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("Explorer error envelope is not a structured object (client cannot read explicit retryability): %s", raw)
	}
	return body, status
}

func TestLegacyFailureCarriesExplicitRetryabilityForActivationMiss(t *testing.T) {

	miss := &ports.Error{Class: ports.ClassConflict, Stage: "repository_publish", Code: "RELEASE_ACTIVATION_FAILED", Message: "activate published ExplorerConfigV2: requirements unmet", Details: map[string]any{"retryable": false}, Cause: errors.New("release requirements unmet")}
	body, status := marshalExplorerErrorBody(t, lifecycleLegacyFailure(miss))
	if status != http.StatusConflict {
		t.Fatalf("activation miss status = %d, want 409", status)
	}
	if body["code"] != "RELEASE_ACTIVATION_FAILED" {
		t.Fatalf("Explorer body lost the lifecycle code: %v", body)
	}
	if retryBlock, ok := body["details"].(map[string]any); !ok || retryBlock["retryable"] != false {
		t.Fatalf("F-B28 RED: explicit retryable=false from the lifecycle error details never reached the client body: %v", body)
	}
}

func TestLegacyFailureKeepsUnrelatedErrorsUnwidened(t *testing.T) {

	body, status := marshalExplorerErrorBody(t, lifecycleLegacyFailure(errors.New("store exploded")))
	if status != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d, want 500", status)
	}
	if body["code"] != "INTERNAL_ERROR" {
		t.Fatalf("plain error code = %v, want INTERNAL_ERROR", body["code"])
	}
	if _, present := body["retryable"]; present {
		t.Fatalf("encoder invented retryability for an error that never declared one: %v", body)
	}
	if _, present := body["details"]; present {
		t.Fatalf("encoder invented a details block for an error that never declared one: %v", body)
	}
	var typed loomapi.LegacyErrorBody
	if err := json.Unmarshal([]byte(`{"code":"X","retryable":true}`), &typed); err != nil || typed.Code != "X" || typed.AdditionalProperties["retryable"] != true {
		t.Fatalf("generated AdditionalProperties contract drifted: %#v err=%v", typed, err)
	}
}
