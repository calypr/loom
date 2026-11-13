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

const (
	twinForeignProject    = "Q"
	twinForeignGeneration = "gQ"
	twinHomeProject       = "P"
	twinCallerPath        = "p1"
	twinStrangerPath      = "p2"
)

const (
	twinForeignExecID        = "fb20twin-exec-foreign-q"
	twinLocalExecID          = "fb20twin-exec-local-p"
	twinUnknownExecID        = "fb20twin-exec-missing"
	twinCoversSupersetExecID = "fb20twin-exec-covers-superset"
	twinCoversExtraPath      = "p-extra"
	twinRecipeName           = "foreign-recipe-twin"
	twinRecipeDigest         = "sha256-recipe-Q-twin"
	twinSchemaDigest         = "sha256-schema-Q-twin"
	twinOutputName           = "rows"
	twinConcealedError       = "recipe execution not found"
)

const twinRowCount = int64(7373737)

const (
	twinHandlerFile = "openapi_recipe_execution.go"
	twinRoutePrefix = "/api/v1/dataframe/recipe-executions/"
)

var twinQuotedFacts = []struct {
	name   string
	source string
	quote  string
	notice string
}{
	{
		name:   "fetch-by-opaque-id-precedes-gates",
		source: twinHandlerFile,
		quote:  "execution, err := r.releases.GetExecution(ctx, id)",
		notice: "the handler fetches the durable execution by opaque id before any gate; denials must conceal",
	},
	{
		name:   "unconditional-project-membership-gate",
		source: twinHandlerFile,
		quote:  "authscope.AuthorizeProject(principal, execution.Project, false)",
		notice: "project membership is asserted for every request, not only when a scope resolver is wired",
	},
	{
		name:   "restricted-mode-uses-the-normalized-path-set-rule",
		source: twinHandlerFile,
		quote:  "scope.CoversExecutionPaths(execution.AuthResourcePaths)",
		notice: "restricted mode compares normalized path sets through the shared authscope rule",
	},
	{
		name:   "resolver-block-remains-conditional-on-purpose",
		source: twinHandlerFile,
		quote:  "if r.scopes != nil {",
		notice: "the resolver consultation itself (and its outage mapping) stays conditional by design",
	},
	{
		name:   "authorized-twin-still-discloses-project",
		source: twinHandlerFile,
		quote:  "ProjectId:            execution.Project,",
		notice: "authorized callers still receive execution metadata (the repair must not over-deny)",
	},
	{
		name:   "concealed-denial-envelope-phrase-pinned",
		source: twinHandlerFile,
		quote:  "\"recipe execution not found\"",
		notice: "denied, concealed responses must carry exactly the missing-execution phrase",
	},
}

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

func twinRequireRepairedGuards(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(twinHandlerFile)
	if err != nil {
		t.Fatalf("STEP 0 drift: cannot read %s: %v", twinHandlerFile, err)
	}
	source := string(raw)
	for _, fact := range twinQuotedFacts {
		quote := fact.quote
		if quote == "twinPhraseAnchor" {
			quote = "\"" + twinConcealedError + "\""
		}
		if !strings.Contains(source, quote) {
			t.Fatalf("STEP 0 drift: %s no longer contains quoted %s\n  file:  %s\n  quote: %s\n  note:  %s",
				fact.name, fact.source, fact.source, quote, fact.notice)
		}
		t.Logf("FB20-TWIN GATE | %s | %s:%d contains %q", fact.name, fact.source, twinLineOf(source, quote), quote)
	}
	archivedMembershipFreeGate := "if r.scopes != nil {\n\t\tprincipal, _ := authscope.PrincipalFromContext(ctx)"
	if strings.Contains(source, archivedMembershipFreeGate) {
		t.Fatalf("FB20-TWIN REGRESSION | %s restored the archived membership-free gate (principal consulted only inside the resolver-conditional block)", twinHandlerFile)
	}
	t.Logf("FB20-TWIN GATE | archived-gate-stays-absent | %s keeps the shipped membership-free gate removed", twinHandlerFile)
}

func twinLineOf(source, needle string) int {
	index := strings.Index(source, needle)
	if index < 0 {
		return 0
	}
	return strings.Count(source[:index], "\n") + 1
}

func twinAssertAuthorizedForeignBody(t *testing.T, fields map[string]any) {
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
			t.Fatalf("authorized body is missing field %q (body field set: %s)", key, twinFieldNames(fields))
		}
		text, isText := got.(string)
		if !isText || text != expected {
			t.Fatalf("authorized field %q = %v, want %q", key, got, expected)
		}
	}
	outputs, ok := fields["outputs"].([]any)
	if !ok || len(outputs) != 1 {
		t.Fatalf("authorized outputs = %v, want exactly one output", fields["outputs"])
	}
	output, ok := outputs[0].(map[string]any)
	if !ok {
		t.Fatalf("authorized output[0] is not an object: %v", outputs[0])
	}
	if output["name"] != twinOutputName || output["state"] != string(publication.BundleReady) {
		t.Fatalf("authorized output[0] name/state = %v/%v, want %s/%s", output["name"], output["state"], twinOutputName, publication.BundleReady)
	}
	count, isNumber := output["rowCount"].(json.Number)
	if !isNumber || count.String() != "7373737" {
		t.Fatalf("authorized output[0].rowCount = %v, want 7373737", output["rowCount"])
	}
}

func twinAssertConcealment(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusNotFound || !strings.Contains(body, twinConcealedError) ||
		strings.Contains(body, twinForeignProject) || strings.Contains(body, twinForeignGeneration) ||
		strings.Contains(body, twinRecipeDigest) || strings.Contains(body, twinSchemaDigest) ||
		strings.Contains(body, "7373737") {
		t.Fatalf("refused read must be indistinguishable from a missing execution (404 + %q + zero foreign metadata), got status %d body %s",
			twinConcealedError, status, body)
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

func twinWiredRoutes() (*HTTPRoutes, *twinExecutionStore, *twinScopeProbe) {
	store := twinForeignStore()
	probe := &twinScopeProbe{existingByProject: map[string][]string{twinForeignProject: {twinCallerPath}}}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: probe.listExisting,
	})
	return &HTTPRoutes{releases: store, scopes: resolver}, store, probe
}

func TestFB20RESTTwinNilScopesRefusesForeignPrincipalOverHTTP(t *testing.T) {
	twinRequireRepairedGuards(t)
	store := twinForeignStore()
	routes := &HTTPRoutes{releases: store}
	principal := &authscope.Principal{Subject: "operator-" + twinHomeProject, Projects: []string{twinHomeProject}}
	app := twinHTTPStack(routes, principal)

	twinAssertMembershipDeniedByTheRealAuthorizer(t, principal)
	status, body, fields := twinGetExecution(t, app, twinForeignExecID)
	if status == http.StatusOK {
		twinAssertAuthorizedForeignBody(t, fields)
		t.Fatalf("FB20-TWIN REGRESSION (archived shipped baseline, case T-A): GET %s%s with scopes=nil returned 200 and "+
			"disclosed foreign project/generation/digests/rowCount for a principal whose Projects=[P] only; AuthorizeProject "+
			"denies project Q, yet the handler never consulted it; body=%s", twinRoutePrefix, twinForeignExecID, body)
	}
	twinAssertConcealment(t, status, body)
	if len(store.ids) != 1 || store.ids[0] != twinForeignExecID {
		t.Fatalf("expected exactly one fetch of the opaque foreign id before the membership gate, got %v", store.ids)
	}
	twinVerdict(t, "case-T-A", "REPAIRED (concealed refusal over HTTP)",
		"nil-scopes foreign read for principal Projects=[P]: status="+twinItoa(status)+" body="+body)

	status, body, fields = twinGetExecution(t, app, twinLocalExecID)
	if status != http.StatusOK || fields["projectId"] != twinHomeProject {
		t.Fatalf("case T-A control: the same-project local execution must keep working over the anonymous+P posture; got %d %s", status, body)
	}
	status, body, _ = twinGetExecution(t, app, twinUnknownExecID)
	twinAssertConcealment(t, status, body)
	foreignStatus, foreignBody, _ := twinGetExecution(t, app, twinForeignExecID)
	if foreignStatus != status || foreignBody != body {
		t.Fatalf("case T-A: denied foreign id must be indistinguishable from missing id; got %d %q vs %d %q",
			foreignStatus, foreignBody, status, body)
	}
}

func TestFB20RESTTwinWiredHeldPathAloneNoLongerAdmitsNonMember(t *testing.T) {
	twinRequireRepairedGuards(t)
	routes, _, probe := twinWiredRoutes()
	principal := &authscope.Principal{
		Subject:           "operator-" + twinHomeProject,
		Projects:          []string{twinHomeProject},
		AuthResourcePaths: []string{twinCallerPath},
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	scope, scopeErr := routes.scopes.ResolveReadScopeForGeneration(ctx, principal, twinForeignProject, twinForeignGeneration, []string{twinCallerPath})
	if scopeErr != nil {
		t.Fatalf("precondition: the production resolver should authorize the held path, got %v", scopeErr)
	}
	if scope.Mode != authscope.ReadScopeRestricted || strings.Join(scope.AuthResourcePaths, ",") != twinCallerPath {
		t.Fatalf("precondition: expected restricted scope [%s], got mode %q paths %v", twinCallerPath, scope.Mode, scope.AuthResourcePaths)
	}
	preProbeCalls := len(probe.calls)

	twinAssertMembershipDeniedByTheRealAuthorizer(t, principal)
	app := twinHTTPStack(routes, principal)
	status, body, fields := twinGetExecution(t, app, twinForeignExecID)
	if status == http.StatusOK {
		twinAssertAuthorizedForeignBody(t, fields)
		t.Fatalf("FB20-TWIN REGRESSION (archived shipped baseline, case T-B): restricted caller holding only auth-path %s "+
			"with Projects=[P only] was accepted over HTTP on the path string alone against foreign project Q; the resolver "+
			"was consulted for the FOREIGN project and the handler never asserted membership; body=%s", twinCallerPath, body)
	}
	twinAssertConcealment(t, status, body)
	if len(probe.calls) != preProbeCalls {
		t.Fatalf("membership refusal must short-circuit before resolver consultation; catalog consulted %d times (baseline %d): %v",
			len(probe.calls), preProbeCalls, probe.calls)
	}
	twinVerdict(t, "case-T-B1", "REPAIRED (concealed refusal, membership precedes resolver)",
		"held-path non-member refused with status="+twinItoa(status)+" and zero new catalog calls beyond the precondition probe")
}

func TestFB20RESTTwinDeniedMemberAndNonMemberAreIndistinguishable(t *testing.T) {
	twinRequireRepairedGuards(t)
	memberPrincipal := &authscope.Principal{
		Subject:           "operator-" + twinForeignProject,
		Projects:          []string{twinForeignProject},
		AuthResourcePaths: []string{twinStrangerPath},
	}
	routesM, _, probeM := twinWiredRoutes()
	appM := twinHTTPStack(routesM, memberPrincipal)
	memberStatus, memberBody, memberFields := twinGetExecution(t, appM, twinForeignExecID)
	if memberStatus == http.StatusOK {
		twinAssertAuthorizedForeignBody(t, memberFields)
		t.Fatalf("FB20-TWIN REGRESSION: member without the execution path read the foreign execution: %s", memberBody)
	}
	if memberStatus != http.StatusNotFound || !strings.Contains(memberBody, twinConcealedError) ||
		strings.Contains(memberBody, twinForeignGeneration) || strings.Contains(memberBody, twinRecipeDigest) ||
		strings.Contains(memberBody, "7373737") {
		t.Fatalf("member/missing-path denial must be the concealed 404 resolver refusal (unified with the GraphQL twin and the REST sibling branch), got %d %s", memberStatus, memberBody)
	}
	if len(probeM.calls) == 0 {
		t.Fatalf("member/missing-path denial should travel the resolver path (catalog consulted), got zero calls")
	}

	nonMemberPrincipal := &authscope.Principal{
		Subject:           "operator-" + twinHomeProject,
		Projects:          []string{twinHomeProject},
		AuthResourcePaths: []string{twinStrangerPath},
	}
	routesN, _, probeN := twinWiredRoutes()
	appN := twinHTTPStack(routesN, nonMemberPrincipal)
	nonMemberStatus, nonMemberBody, _ := twinGetExecution(t, appN, twinForeignExecID)
	twinAssertConcealment(t, nonMemberStatus, nonMemberBody)
	if len(probeN.calls) != 0 {
		t.Fatalf("non-member denial must not consult the catalog; got %d calls: %v", len(probeN.calls), probeN.calls)
	}
	if memberBody != nonMemberBody {
		t.Fatalf("denied member and denied non-member must be indistinguishable: %d %q vs %d %q",
			memberStatus, memberBody, nonMemberStatus, nonMemberBody)
	}
	routesU, _, probeU := twinWiredRoutes()
	appU := twinHTTPStack(routesU, nil)
	anonStatus, anonBody, anonFields := twinGetExecution(t, appU, twinForeignExecID)
	if anonStatus != http.StatusOK {
		t.Fatalf("FB20-TWIN REGRESSION: the anonymous (single-operator) posture must keep working; got %d %s", anonStatus, anonBody)
	}
	twinAssertAuthorizedForeignBody(t, anonFields)
	t.Logf("FB20-TWIN CONTROL | anonymous-unrestricted-shortcut | catalog calls=%d (0 expected for nil principal)", len(probeU.calls))
	twinVerdict(t, "case-T-B2", "REPAIRED (uniform concealment; documented anonymous posture)",
		"member-missing path refused via resolver ("+twinItoa(memberStatus)+"), non-member refused via membership ("+twinItoa(nonMemberStatus)+
			") with byte-identical envelopes; nil-principal disclosure preserved by design; body="+nonMemberBody)
}

func TestFB20RESTTwinCoversSubsetGateIsSoleDisclosureGate(t *testing.T) {
	twinRequireRepairedGuards(t)

	superset := twinForeignBundle()
	superset.ID = twinCoversSupersetExecID
	superset.BundleIdentity.AuthResourcePaths = []string{twinCallerPath, twinCoversExtraPath}
	store := twinForeignStore()
	store.executions[twinCoversSupersetExecID] = superset
	probe := &twinScopeProbe{existingByProject: map[string][]string{twinForeignProject: {twinCallerPath, twinCoversExtraPath}}}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: probe.listExisting})
	routes := &HTTPRoutes{releases: store, scopes: resolver}
	principal := &authscope.Principal{
		Subject:           "operator-" + twinForeignProject,
		Projects:          []string{twinForeignProject},
		AuthResourcePaths: []string{twinCallerPath},
	}

	if err := authscope.AuthorizeProject(principal, twinForeignProject, false); err != nil {
		t.Fatalf("precondition: the shipped authorizer must admit this project member, got %v", err)
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	if _, scopeErr := routes.scopes.ResolveReadScopeForGeneration(ctx, principal, twinForeignProject, twinForeignGeneration, []string{twinCallerPath, twinCoversExtraPath}); !errors.Is(scopeErr, authscope.ErrForbidden) {
		t.Fatalf("precondition: the production resolver must deny the superset read outside caller scope, got %v", scopeErr)
	}
	t.Logf("FB20-TWIN CONTROL | case-T-C-drift-is-not-resolver-reachable | production resolver denies [{%s,%s}] for caller {%s}: the drifted subset in arm (b) models resolver drift, the exact drift the gate documents defense against", twinCallerPath, twinCoversExtraPath, twinCallerPath)

	original := recipeExecutionReadScope
	defer func() { recipeExecutionReadScope = original }()
	app := twinHTTPStack(routes, principal)

	recipeExecutionReadScope = func(context.Context, *authscope.ScopeResolver, *authscope.Principal, string, string, []string) (authscope.ReadScope, error) {
		return authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{twinCallerPath, twinCoversExtraPath}}, nil
	}
	status, body, fields := twinGetExecution(t, app, twinCoversSupersetExecID)
	if status != http.StatusOK {
		t.Fatalf("case T-C(a): a covering restricted scope must serve the execution, got %d %s", status, body)
	}
	if fields["id"] != twinCoversSupersetExecID || fields["projectId"] != twinForeignProject || fields["datasetGeneration"] != twinForeignGeneration {
		t.Fatalf("case T-C(a): disclosed body = %s", body)
	}
	outputs, isList := fields["outputs"].([]any)
	if !isList || len(outputs) != 1 {
		t.Fatalf("case T-C(a): outputs = %v, want exactly one output", fields["outputs"])
	}
	output, isObject := outputs[0].(map[string]any)
	if !isObject {
		t.Fatalf("case T-C(a): output[0] is not an object: %v", outputs[0])
	}
	if count, isNumber := output["rowCount"].(json.Number); !isNumber || count.String() != "7373737" {
		t.Fatalf("case T-C(a): output[0].rowCount = %v, want 7373737", output["rowCount"])
	}

	recipeExecutionReadScope = func(context.Context, *authscope.ScopeResolver, *authscope.Principal, string, string, []string) (authscope.ReadScope, error) {
		return authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{twinCallerPath}}, nil
	}
	subsetStatus, subsetBody, _ := twinGetExecution(t, app, twinCoversSupersetExecID)
	twinAssertConcealment(t, subsetStatus, subsetBody)
	missingStatus, missingBody, _ := twinGetExecution(t, app, twinUnknownExecID)
	if subsetStatus != missingStatus || subsetBody != missingBody {
		t.Fatalf("case T-C(b): the drifted-subset execution must be indistinguishable from a missing id; got %d %q vs %d %q",
			subsetStatus, subsetBody, missingStatus, missingBody)
	}

	twinVerdict(t, "case-T-C", "REPAIRED (Covers subset loop is the sole disclosure gate for a drifted restricted scope)",
		"covering scope served "+twinItoa(status)+", proper-subset scope concealed with the byte-identical missing envelope ("+twinItoa(subsetStatus)+" "+subsetBody+")")
}

func twinItoa(value int) string {
	return strconv.Itoa(value)
}
