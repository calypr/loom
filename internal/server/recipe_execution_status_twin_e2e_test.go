package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/gofiber/fiber/v3"
)

// This file is the REST twin of the confirmed F-B20 GraphQL probe
// (internal/api/graphql/graph/resolver/recipe_execution_reader_authz_e2e_test.go).
// It is a characterization suite: every assertion records what the shipped HTTP
// handler stack does today, and STEP 0 fails the suite the moment the shipped
// guards in openapi_recipe_execution.go are edited or a real project-membership
// check is added, retiring this artifact as drift rather than passing stale
// findings.
//
// What "real handler stack" owns here. Requests enter through the generated
// Fiber router registration (generated/loomapi/api.gen.go:14599
// GET /api/v1/dataframe/recipe-executions/:id), the generated strict adapter
// (loomapi.NewStrictHandler + RegisterHandlersWithOptions, the same mounting
// done by internal/server/routes.go:38-39), routes.GetRecipeExecution
// (internal/server/openapi_data_routes.go:139) and routes.recipeExecution
// (internal/server/openapi_recipe_execution.go:17). Only the collaborators
// behind HTTPRoutes.releases (publication.BundleCatalog) and HTTPRoutes.scopes
// (*authscope.ScopeResolver, faked only at its declared ScopeResolverConfig
// seams) are substituted, matching internal/server precedent
// (openapi_recipe_execution_test.go, openapi_explorer_write_scope_test.go).
//
// Principal injection mirrors the production request middleware exactly:
// internal/api/http/middleware.go:132 does
// c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal)).
// The authentication step that turns headers into a principal is NOT exercised
// here; the suite injects principals through the real context helper, so it
// proves handler-stack behavior for an injected per-project principal, not
// that any shipped authenticator produces such principals. Deployed posture is
// documented single-operator basic/no-auth (internal/authscope/authentication.go:12-14),
// so nothing here claims multi-tenant exploitability.
const (
	twinForeignProject    = "Q"
	twinForeignGeneration = "gQ"
	twinHomeProject       = "P"
	twinCallerPath        = "p1"
	twinStrangerPath      = "p2"
)

const (
	twinForeignExecID  = "fb20twin-exec-foreign-q"
	twinLocalExecID    = "fb20twin-exec-local-p"
	twinUnknownExecID  = "fb20twin-exec-missing"
	twinRecipeName     = "foreign-recipe-twin"
	twinRecipeDigest   = "sha256-recipe-Q-twin"
	twinSchemaDigest   = "sha256-schema-Q-twin"
	twinOutputName     = "rows"
	twinConcealedError = "recipe execution not found"
)

const twinRowCount = int64(7373737)

const (
	twinHandlerFile = "openapi_recipe_execution.go"
	twinRoutePrefix = "/api/v1/dataframe/recipe-executions/"
)

// twinQuotedFacts are the exact production fragments this adjudication is
// built on. Any disappearance is drift and must fail STEP 0 rather than let a
// stale finding pass: the fetch-then-check ordering, the conditional authz
// guard, and the ProjectId disclosure line.
var twinQuotedFacts = []struct {
	name   string
	source string
	quote  string
	notice string
}{
	{
		name:   "fetch-by-opaque-id-precedes-any-check",
		source: twinHandlerFile,
		quote:  "execution, err := r.releases.GetExecution(ctx, id)",
		notice: "handler fetches the durable execution by opaque id before consulting authorization",
	},
	{
		name:   "authz-only-when-scopes-wired",
		source: twinHandlerFile,
		quote:  "if r.scopes != nil {",
		notice: "the whole authorization block is conditional on a non-nil scope resolver",
	},
	{
		name:   "twin-discloses-project",
		source: twinHandlerFile,
		quote:  "ProjectId:            execution.Project,",
		notice: "handler maps the execution's own project into the response body",
	},
}

// twinExecutionStore is the smallest fake for publication.BundleCatalog, the
// same embedding discipline as the existing recipeExecutionCatalog fake in
// openapi_recipe_execution_test.go. Unknown ids yield the zero execution the
// handler treats as missing (openapi_recipe_execution.go:23).
type twinExecutionStore struct {
	publication.BundleCatalog
	executions map[string]publication.BundleExecution
	ids        []string
}

func (s *twinExecutionStore) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	s.ids = append(s.ids, strings.Clone(id))
	if execution, ok := s.executions[id]; ok {
		return execution, nil
	}
	return publication.BundleExecution{}, nil
}

// twinScopeProbe drives the declared authscope.ScopeResolverConfig seam
// ListExistingAuthResourcePaths so the production resolver can resolve
// restricted scopes without a live Fence or catalog, keyed by the project the
// resolver actually asked about, which also records which project the scope
// was resolved against.
type twinScopeProbe struct {
	existingByProject map[string][]string
	calls             []catalog.AuthResourcePathOptions
}

func (p *twinScopeProbe) listExisting(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	p.calls = append(p.calls, opts)
	return append([]string(nil), p.existingByProject[opts.Project]...), nil
}

func twinForeignBundle() publication.BundleExecution {
	return publication.BundleExecution{
		ID: twinForeignExecID,
		BundleIdentity: publication.BundleIdentity{
			Name:              twinRecipeName,
			Project:           twinForeignProject,
			DatasetGeneration: twinForeignGeneration,
			RecipeDigest:      twinRecipeDigest,
			SchemaDigest:      twinSchemaDigest,
			AuthResourcePaths: []string{twinCallerPath},
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name:     twinOutputName,
			State:    publication.BundleReady,
			RowCount: twinRowCount,
		}},
	}
}

func twinLocalBundle() publication.BundleExecution {
	return publication.BundleExecution{
		ID: twinLocalExecID,
		BundleIdentity: publication.BundleIdentity{
			Name:              "local-recipe",
			Project:           twinHomeProject,
			DatasetGeneration: "gP",
			RecipeDigest:      "sha256-recipe-P",
			SchemaDigest:      "sha256-schema-P",
		},
		State:   publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{Name: "rows", State: publication.BundleReady, RowCount: 11}},
	}
}

// twinHTTPStack mounts the generated router the same way internal/server/
// routes.go:38-39 does, and injects the principal through the same fiber
// seam the production authentication middleware uses
// (internal/api/http/middleware.go:132).
func twinHTTPStack(routes *HTTPRoutes, principal *authscope.Principal) *fiber.App {
	app := fiber.New()
	if principal != nil {
		app.Use(func(c fiber.Ctx) error {
			c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
			return c.Next()
		})
	}
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	return app
}

func twinGetExecution(t *testing.T, app *fiber.App, id string) (int, string, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, twinRoutePrefix+id, nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("GET %s%s: %v", twinRoutePrefix, id, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body for %s: %v", id, err)
	}
	body := string(payload)
	var fields map[string]any
	if body != "" {
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&fields); err != nil {
			t.Fatalf("body for %s is not a JSON object (%s): %v", id, body, err)
		}
	}
	t.Logf("FB20-TWIN HTTP | method=GET path=%s%s status=%d body=%s", twinRoutePrefix, id, response.StatusCode, body)
	return response.StatusCode, body, fields
}

// twinRequireProductionGuards is STEP 0: pin the quoted fragments and the
// negative facts. A fix that adds AuthorizeProject or AuthorizeReadProject to
// the twin retires this finding by failing here with an explicit message.
func twinRequireProductionGuards(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(twinHandlerFile)
	if err != nil {
		t.Fatalf("STEP 0 drift: cannot read %s: %v", twinHandlerFile, err)
	}
	source := string(raw)
	for _, fact := range twinQuotedFacts {
		if !strings.Contains(source, fact.quote) {
			t.Fatalf("STEP 0 drift: %s no longer contains quoted %s\n  file:  %s\n  quote: %s\n  note:  %s",
				fact.name, fact.source, fact.source, fact.quote, fact.notice)
		}
		t.Logf("FB20-TWIN GATE | %s | %s:%d contains %q", fact.name, fact.source, twinLineOf(source, fact.quote), fact.quote)
	}
	for _, forbidden := range []string{"authscope.AuthorizeProject", "AuthorizeReadProject", "slices.Contains"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("STEP 0 drift: %s now contains %q, so F-B20's twin claim (fetch then conditional count-free scope check with no project-membership assertion) no longer holds. Re-adjudicate before trusting any verdict below.", twinHandlerFile, forbidden)
		}
		t.Logf("FB20-TWIN GATE | twin-performs-no-project-membership-check | %s contains no %q", twinHandlerFile, forbidden)
	}
}

func twinLineOf(source, needle string) int {
	index := strings.Index(source, needle)
	if index < 0 {
		return 0
	}
	return strings.Count(source[:index], "\n") + 1
}

func twinAssertDisclosedForeignBody(t *testing.T, fields map[string]any) {
	t.Helper()
	want := map[string]string{
		"id":                   twinForeignExecID,
		"projectId":            twinForeignProject,
		"datasetGeneration":    twinForeignGeneration,
		"recipeDigest":         twinRecipeDigest,
		"schemaDigest":         twinSchemaDigest,
		"resolvedSchemaDigest": twinSchemaDigest,
		"state":                string(publication.BundleReady),
	}
	for key, expected := range want {
		got, ok := fields[key]
		if !ok {
			t.Fatalf("disclosed body is missing field %q (body field set: %s)", key, twinFieldNames(fields))
		}
		text, isText := got.(string)
		if !isText || text != expected {
			t.Fatalf("disclosed field %q = %v, want %q", key, got, expected)
		}
	}
	outputs, ok := fields["outputs"].([]any)
	if !ok || len(outputs) != 1 {
		t.Fatalf("disclosed outputs = %v, want exactly one output", fields["outputs"])
	}
	output, ok := outputs[0].(map[string]any)
	if !ok {
		t.Fatalf("disclosed output[0] is not an object: %v", outputs[0])
	}
	if output["name"] != twinOutputName || output["state"] != string(publication.BundleReady) {
		t.Fatalf("disclosed output[0] name/state = %v/%v, want %s/%s", output["name"], output["state"], twinOutputName, publication.BundleReady)
	}
	count, isNumber := output["rowCount"].(json.Number)
	if !isNumber || count.String() != "7373737" {
		t.Fatalf("disclosed output[0].rowCount = %v, want 7373737", output["rowCount"])
	}
}

func twinFieldNames(fields map[string]any) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return strings.Join(names, ",")
}

func twinAssertMembershipDeniedByTheRealAuthorizer(t *testing.T, principal *authscope.Principal) {
	t.Helper()
	err := authscope.AuthorizeProject(principal, twinForeignProject, false)
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("expected the shipped AuthorizeProject to deny project %q for principal %+v, got %v",
			twinForeignProject, principal, err)
	}
	t.Logf("FB20-TWIN CONTROL | shipped-authorize-project-denies-foreign-project | authscope.AuthorizeProject(principal, %q, false) = %v",
		twinForeignProject, err)
}

func twinVerdict(t *testing.T, id, verdict, observed string) {
	t.Helper()
	t.Logf("FB20-TWIN %s | VERDICT=%s | OBSERVED=%s", id, verdict, observed)
}

func twinForeignStore() *twinExecutionStore {
	return &twinExecutionStore{executions: map[string]publication.BundleExecution{
		twinForeignExecID: twinForeignBundle(),
		twinLocalExecID:   twinLocalBundle(),
	}}
}

// Case T-A: shipped baseline posture on the REST twin. HTTPRoutes.scopes is
// nil (routes.go:38-39 leaves it nil whenever no *authscope.ScopeResolver is
// passed as an optional collaborator) and the request context carries a real
// authscope.ContextWithPrincipal principal whose Projects hold only "P".
func TestFB20RESTTwinNilScopesDisclosesForeignExecutionOverHTTP(t *testing.T) {
	twinRequireProductionGuards(t)
	store := twinForeignStore()
	routes := &HTTPRoutes{releases: store}
	principal := &authscope.Principal{Subject: "operator-" + twinHomeProject, Projects: []string{twinHomeProject}}
	app := twinHTTPStack(routes, principal)

	status, body, fields := twinGetExecution(t, app, twinForeignExecID)
	if status != http.StatusOK {
		twinVerdict(t, "case-T-A", "PREVENTED", "status="+twinItoa(status)+" body="+body)
		t.Fatalf("case T-A expected the shipped nil-scopes path to return the foreign execution over HTTP, got status %d body %s", status, body)
	}
	twinAssertDisclosedForeignBody(t, fields)
	twinAssertMembershipDeniedByTheRealAuthorizer(t, principal)
	disclosedBody := body

	status, body, fields = twinGetExecution(t, app, twinLocalExecID)
	if status != http.StatusOK || fields["projectId"] != twinHomeProject {
		t.Fatalf("control: the legitimate local execution should read normally, got status %d body %s", status, body)
	}
	status, body, _ = twinGetExecution(t, app, twinUnknownExecID)
	if status != http.StatusNotFound || !strings.Contains(body, twinConcealedError) {
		t.Fatalf("control: an unknown id should yield the concealed 404, got status %d body %s", status, body)
	}
	if len(store.ids) != 3 || store.ids[0] != twinForeignExecID || store.ids[1] != twinLocalExecID || store.ids[2] != twinUnknownExecID {
		t.Fatalf("expected the handler to look each execution up by its opaque id, got fetches %v", store.ids)
	}

	twinVerdict(t, "case-T-A", "CONFIRMED (baseline)",
		"GET "+twinRoutePrefix+twinForeignExecID+" over the generated Fiber strict stack with scopes=nil "+
			"(openapi_recipe_execution.go:"+twinItoa(twinLineOf(twinReadHandler(t), "if r.scopes != nil {"))+" guard skipped entirely) returned 200 and disclosed "+
			"foreign project/generation/digests/rowCount for a principal whose Projects=[P] only; "+
			"AuthorizeProject denies project Q, yet the handler never calls it; body="+disclosedBody)
}

// twinReadHandler re-reads the handler file for the guard line number quoted
// in the T-A verdict string, so the log cites source without staling.
func twinReadHandler(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(twinHandlerFile)
	if err != nil {
		t.Fatalf("read %s: %v", twinHandlerFile, err)
	}
	return string(raw)
}

func twinItoa(value int) string {
	return strconv.Itoa(value)
}

// Case T-B: scopes wired, caller restricted (holds auth-path strings), equal
// path counts between caller grants and the foreign execution. Documents the
// actual behavior: acceptance of the same shape as the GraphQL probe's case
// B1, driven only by path-string holdings resolved against the FOREIGN
// project/generation, with no project-membership assertion.
func TestFB20RESTTwinWiredRestrictedScopeAcceptsForeignExecutionOnHeldPathAlone(t *testing.T) {
	twinRequireProductionGuards(t)
	store := twinForeignStore()
	probe := &twinScopeProbe{existingByProject: map[string][]string{twinForeignProject: {twinCallerPath}}}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: probe.listExisting,
	})
	routes := &HTTPRoutes{releases: store, scopes: resolver}
	principal := &authscope.Principal{
		Subject:           "operator-" + twinHomeProject,
		Projects:          []string{twinHomeProject},
		AuthResourcePaths: []string{twinCallerPath},
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	scope, scopeErr := resolver.ResolveReadScopeForGeneration(ctx, principal, twinForeignProject, twinForeignGeneration, []string{twinCallerPath})
	if scopeErr != nil {
		t.Fatalf("precondition: the production resolver should authorize the held path, got %v", scopeErr)
	}
	if scope.Mode != authscope.ReadScopeRestricted {
		t.Fatalf("precondition: expected a restricted scope so the wired guard is exercised, got mode %q", scope.Mode)
	}
	if len(scope.AuthResourcePaths) != len(twinForeignBundle().AuthResourcePaths) {
		t.Fatalf("precondition: counts should be equal (%d vs %d)",
			len(scope.AuthResourcePaths), len(twinForeignBundle().AuthResourcePaths))
	}

	app := twinHTTPStack(routes, principal)
	status, body, fields := twinGetExecution(t, app, twinForeignExecID)
	if status != http.StatusOK {
		twinVerdict(t, "case-T-B", "PREVENTED", "status="+twinItoa(status)+" body="+body)
		t.Fatalf("case T-B expected the wired equal-count restricted path to accept over HTTP, got status %d body %s", status, body)
	}
	twinAssertDisclosedForeignBody(t, fields)
	twinAssertMembershipDeniedByTheRealAuthorizer(t, principal)

	queried := "none"
	for _, call := range probe.calls {
		queried = "project=" + call.Project + " generation=" + call.DatasetGeneration
	}
	twinVerdict(t, "case-T-B", "CONFIRMED",
		"restricted caller holding only auth-path "+twinCallerPath+", Projects=["+twinHomeProject+" only], equal counts "+
			"(execution p1 vs scope p1) was accepted over HTTP by the wired guard "+
			"(openapi_recipe_execution.go:"+twinItoa(twinLineOf(twinReadHandler(t), "if r.scopes != nil {"))+"): "+
			"the resolver was consulted for the FOREIGN "+queried+" and the handler never asserted membership "+
			"(no AuthorizeProject/AuthorizeReadProject in the file); body="+body)
}

// Case T-B2: non-vacuity control for T-B. With scopes wired, a caller whose
// grants lack the foreign execution's path must be refused and concealed. If
// this case accepted, T-B's acceptance would be vacuous harness triviality,
// so the guard's participation is proven here.
func TestFB20RESTTwinWiredScopeRefusesCallerWithoutTheForeignPath(t *testing.T) {
	twinRequireProductionGuards(t)
	store := twinForeignStore()
	probe := &twinScopeProbe{existingByProject: map[string][]string{twinForeignProject: {twinCallerPath}}}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: probe.listExisting,
	})
	routes := &HTTPRoutes{releases: store, scopes: resolver}
	principal := &authscope.Principal{
		Subject:           "operator-" + twinHomeProject,
		Projects:          []string{twinHomeProject},
		AuthResourcePaths: []string{twinStrangerPath},
	}

	app := twinHTTPStack(routes, principal)
	status, body, _ := twinGetExecution(t, app, twinForeignExecID)
	if status == http.StatusOK {
		twinVerdict(t, "case-T-B2", "CONFIRMED", "the wired guard accepted a caller without the foreign path; body="+body)
		t.Fatalf("case T-B2 control expects the wired resolver to refuse a caller lacking %q, but the twin disclosed: %s", twinCallerPath, body)
	}
	if status != http.StatusForbidden {
		twinVerdict(t, "case-T-B2", "INCONCLUSIVE", "status="+twinItoa(status)+" body="+body)
		t.Fatalf("case T-B2 control expected a concealed 403 from the forbidden resolver, got status %d body %s", status, body)
	}
	if !strings.Contains(body, twinConcealedError) || strings.Contains(body, twinForeignGeneration) || strings.Contains(body, twinRecipeDigest) || strings.Contains(body, "7373737") {
		twinVerdict(t, "case-T-B2", "INCONCLUSIVE", "status=403 body="+body)
		t.Fatalf("case T-B2 denial was not the concealed not-found shape: %s", body)
	}
	twinVerdict(t, "case-T-B2", "PREVENTED",
		"with scopes wired, a caller whose grants lack the execution's path gets the concealed "+
			"404-shaped denial and no foreign metadata; this makes case T-B's acceptance non-vacuous: "+
			"the accepted decision came from the held path string alone, not from harness triviality; body="+body)
}
