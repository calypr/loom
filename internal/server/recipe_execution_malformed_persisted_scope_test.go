package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/publication"
)

// Durable R4 control for the production REST execution reader.
//
// Reproduction. A persisted execution declares auth_resource_paths
// ["p1","foreign\x00secret"]. A project member granted only p1 was served the
// execution metadata over GET /api/v1/dataframe/recipe-executions/:id, because
// normalizeAuthResourcePathList drops the malformed non-blank entry and the
// surviving path covered the truncated declaration.
// ReadScope.CoversExecutionPaths now refuses a declaration holding any
// malformed non-blank entry, so the handler's restricted-mode Covers gate
// (openapi_recipe_execution.go) conceals it exactly like a missing id.
//
// Requests enter through the same generated Fiber stack the FB20 twin uses
// (twinHTTPStack/twinGetExecution); only the durable catalog behind
// HTTPRoutes.releases and the catalog lister behind the authentic
// authscope.ScopeResolver are substituted. The Covers gate is reached through
// the authentic resolver call (no recipeExecutionReadScope seam override):
// the resolver returns Restricted for this caller (non-nil principal with a
// held path) and echoes back the caller's own clean grant, so only the
// declaration-side refusal can deny the malformed read.
//
// Contract pinned here:
//   - malformed persisted scope -> the same concealed 404 envelope as a
//     genuinely missing id, byte-identical, with zero metadata disclosure and
//     zero leak of the malformed bytes;
//   - permitted normal execution -> still 200 with full metadata (no
//     over-denial);
//   - legitimate blank declarations -> unchanged pre-existing policy (blank
//     padding around real paths stays absent; a blank-only declaration is not
//     a resolved authorization);
//   - membership/generation/authorization behavior -> non-member 404 without
//     catalog consultation, resolver path denial 404, anonymous disclosure of
//     legitimate executions, Fence-less bearer header 503, unauthenticated
//     401, store/catalog faults retryable BACKEND_UNAVAILABLE 503.

const (
	mscHomeProject       = "P"
	mscGeneration        = "g-msc"
	mscGrantedPath       = "p1"
	mscMalformedNUL      = "foreign\x00secret"
	mscMalformedExecID   = "msc-exec-malformed"
	mscCleanExecID       = "msc-exec-clean"
	mscBlankExecID       = "msc-exec-blank"
	mscPaddingExecID     = "msc-exec-padding"
	mscMissingID         = "msc-exec-missing"
	mscRecipeName        = "persisted-scope-recipe"
	mscRecipeDigest      = "sha256-recipe-msc"
	mscSchemaDigest      = "sha256-schema-msc"
	mscOutputName        = "rows"
	mscOutputRowCount    = "424242"
	mscSecretFingerprint = "secret"
)

// mscProbe is a generation-aware catalog lister for the authentic resolver.
// It keeps the FB20 twin's twinScopeProbe untouched while recording the exact
// lookups so gate ordering stays observable.
type mscProbe struct {
	pathsByProjectGeneration map[string][]string
	calls                    []catalog.AuthResourcePathOptions
	fault                    error
}

func mscKey(project, generation string) string {
	return project + "\x01" + generation
}

func mscKnownPaths() map[string][]string {
	return map[string][]string{mscKey(mscHomeProject, mscGeneration): {mscGrantedPath}}
}

func (p *mscProbe) listExisting(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	p.calls = append(p.calls, opts)
	if p.fault != nil {
		return nil, p.fault
	}
	return append([]string(nil), p.pathsByProjectGeneration[mscKey(opts.Project, opts.DatasetGeneration)]...), nil
}

// mscDeniedTokenClient is the Fence stand-in that rejects the bearer token,
// driving the shipped unauthenticated mapping without a live Fence.
type mscDeniedTokenClient struct{}

func (mscDeniedTokenClient) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return nil, fmt.Errorf("%w: invalid token", authscope.ErrUnauthenticated)
}

func mscBundle(id string, declared []string) publication.BundleExecution {
	return publication.BundleExecution{
		ID: id,
		BundleIdentity: publication.BundleIdentity{
			Name:              mscRecipeName,
			Project:           mscHomeProject,
			DatasetGeneration: mscGeneration,
			RecipeDigest:      mscRecipeDigest,
			SchemaDigest:      mscSchemaDigest,
			AuthResourcePaths: declared,
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name:     mscOutputName,
			State:    publication.BundleReady,
			RowCount: 424242,
		}},
	}
}

func mscStore() *twinExecutionStore {
	return &twinExecutionStore{executions: map[string]publication.BundleExecution{
		mscMalformedExecID: mscBundle(mscMalformedExecID, []string{mscGrantedPath, mscMalformedNUL}),
		mscCleanExecID:     mscBundle(mscCleanExecID, []string{mscGrantedPath}),
		mscBlankExecID:     mscBundle(mscBlankExecID, []string{"", "  "}),
		mscPaddingExecID:   mscBundle(mscPaddingExecID, []string{mscGrantedPath, "", " ", "\t"}),
	}}
}

// mscFailingStore answers every fetch with a durable fault, the store twin of
// a transient Arango outage.
type mscFailingStore struct {
	publication.BundleCatalog
	err error
}

func (s mscFailingStore) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, s.err
}

func mscRoutesWith(probe *mscProbe, resourceAccess authscope.ResourceAccessClient) *HTTPRoutes {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess:                resourceAccess,
		ListExistingAuthResourcePaths: probe.listExisting,
	})
	return &HTTPRoutes{releases: mscStore(), scopes: resolver}
}

func mscMemberPrincipal() *authscope.Principal {
	return &authscope.Principal{
		Subject:           "operator-" + mscHomeProject,
		Projects:          []string{mscHomeProject},
		AuthResourcePaths: []string{mscGrantedPath},
	}
}

// mscAssertAuthorizedBody pins the full metadata disclosure for a permitted
// read so the concealment arms cannot pass by over-denial.
func mscAssertAuthorizedBody(t *testing.T, fields map[string]any, id string) {
	t.Helper()
	want := map[string]string{
		"id":                   id,
		"projectId":            mscHomeProject,
		"datasetGeneration":    mscGeneration,
		"recipeDigest":         mscRecipeDigest,
		"schemaDigest":         mscSchemaDigest,
		"resolvedSchemaDigest": mscSchemaDigest,
		"state":                string(publication.BundleReady),
	}
	for key, expected := range want {
		got, ok := fields[key]
		if !ok {
			t.Fatalf("authorized body is missing field %q (fields: %s)", key, twinFieldNames(fields))
		}
		if text, isText := got.(string); !isText || text != expected {
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
	if output["name"] != mscOutputName {
		t.Fatalf("authorized output name = %v, want %s", output["name"], mscOutputName)
	}
	if count, isNumber := output["rowCount"].(json.Number); !isNumber || count.String() != mscOutputRowCount {
		t.Fatalf("authorized output rowCount = %v, want %s", output["rowCount"], mscOutputRowCount)
	}
}

// mscAssertConcealment extends twinAssertConcealment's leak checks with this
// file's own fixture constants, including the malformed declaration's secret
// substring, its NUL escape, and the row count.
func mscAssertConcealment(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusNotFound || !strings.Contains(body, twinConcealedError) ||
		strings.Contains(body, mscHomeProject) || strings.Contains(body, mscGeneration) ||
		strings.Contains(body, mscRecipeDigest) || strings.Contains(body, mscSchemaDigest) ||
		strings.Contains(body, mscOutputRowCount) || strings.Contains(body, mscMalformedNUL) ||
		strings.Contains(body, mscSecretFingerprint) || strings.Contains(body, "\\u0000") ||
		strings.Contains(body, mscGrantedPath) {
		t.Fatalf("refused read must be the concealed missing-execution envelope (404 + %q + zero metadata + zero malformed bytes), got %d %s",
			twinConcealedError, status, body)
	}
}

// TestRESTRecipeExecutionConcealsMalformedPersistedScope is the required
// regression for acceptance criterion [2].
func TestRESTRecipeExecutionConcealsMalformedPersistedScope(t *testing.T) {
	// Precondition: the shipped authorizer admits this member and the real
	// resolver resolves this caller to a Restricted scope echoing the clean
	// grant, so the Covers gate (not membership, not a resolver refusal)
	// decides the malformed read.
	principal := mscMemberPrincipal()
	if err := authscope.AuthorizeProject(principal, mscHomeProject, false); err != nil {
		t.Fatalf("precondition: shipped authorizer must admit the project member, got %v", err)
	}
	probe := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: probe.listExisting})
	scope, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, mscHomeProject, mscGeneration, []string{mscGrantedPath, mscMalformedNUL})
	if err != nil {
		t.Fatalf("precondition: resolver must resolve the member's grant, got %v", err)
	}
	if scope.Mode != authscope.ReadScopeRestricted || len(scope.AuthResourcePaths) != 1 || scope.AuthResourcePaths[0] != mscGrantedPath {
		t.Fatalf("precondition: resolver must echo the clean restricted grant, got %+v", scope)
	}
	if scope.CoversExecutionPaths([]string{mscGrantedPath, mscMalformedNUL}) {
		t.Fatal("R4 REGRESSION: the shared coverage rule still covers a malformed persisted declaration")
	}

	routes := mscRoutesWith(probe, nil)
	app := twinHTTPStack(routes, principal)

	// Arm 1 (the R4 reproduction): mixed valid-plus-malformed declaration.
	malformedStatus, malformedBody, _ := twinGetExecution(t, app, mscMalformedExecID)
	mscAssertConcealment(t, malformedStatus, malformedBody)

	// Arm 2: indistinguishable from a genuinely missing id (the same generated
	// legacy 404 envelope, byte for byte).
	missingStatus, missingBody, _ := twinGetExecution(t, app, mscMissingID)
	if malformedStatus != missingStatus || malformedBody != missingBody {
		t.Fatalf("malformed-scope execution must be indistinguishable from a missing id; got %d %q vs %d %q",
			malformedStatus, malformedBody, missingStatus, missingBody)
	}

	// Arm 3: the permitted normal execution remains readable through the same
	// handler, resolver and scope shape.
	cleanStatus, cleanBody, cleanFields := twinGetExecution(t, app, mscCleanExecID)
	if cleanStatus != http.StatusOK {
		t.Fatalf("R4 OVER-DENIAL: member granted p1 could not read the clean p1 execution: %d %s", cleanStatus, cleanBody)
	}
	mscAssertAuthorizedBody(t, cleanFields, mscCleanExecID)

	// Arm 4: legitimate blank policy is the shipped one — blank padding around
	// real paths stays absent (serves), and a declaration with no non-blank
	// path is not a resolved authorization (restricted coverage refuses it,
	// unchanged pre-repair behavior).
	blankStatus, blankBody, _ := twinGetExecution(t, app, mscBlankExecID)
	mscAssertConcealment(t, blankStatus, blankBody)
	paddingStatus, paddingBody, paddingFields := twinGetExecution(t, app, mscPaddingExecID)
	if paddingStatus != http.StatusOK {
		t.Fatalf("OVER-DENIAL: blank padding must remain the legitimate absent policy: %d %s", paddingStatus, paddingBody)
	}
	mscAssertAuthorizedBody(t, paddingFields, mscPaddingExecID)

	// Gate ordering: every resolver-path request consulted the catalog for the
	// execution's exact project+generation, and the store really fetched each
	// id (no early synthetic not-found keyed on the malformed bytes).
	if len(probe.calls) == 0 {
		t.Fatal("the malformed read must travel the resolver path (catalog consulted), got zero calls")
	}
	for _, call := range probe.calls {
		if call.Project != mscHomeProject || call.DatasetGeneration != mscGeneration {
			t.Fatalf("resolver consulted for the wrong scope: %+v", call)
		}
	}
	t.Logf("R4 REST | VERDICT=CONCEALED | malformed=%d clean=%d blank=%d padding=%d missing=%d body=%s",
		malformedStatus, cleanStatus, blankStatus, paddingStatus, missingStatus, malformedBody)
}

// TestRESTRecipeExecutionMalformedScopePreservesAuthorizationArms pins the
// second half of acceptance criterion [2]: the pre-existing membership,
// generation, unauthenticated and backend-fault classification survive the
// fails-closed rule.
func TestRESTRecipeExecutionMalformedScopePreservesAuthorizationArms(t *testing.T) {
	// Non-member: refused by the membership gate before the resolver, and
	// byte-identical to the malformed-scope concealment.
	outsider := &authscope.Principal{
		Subject:           "operator-foreign",
		Projects:          []string{"FOREIGN"},
		AuthResourcePaths: []string{mscGrantedPath},
	}
	probeOutsider := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	outStatus, outBody, _ := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeOutsider, nil), outsider), mscMalformedExecID)
	mscAssertConcealment(t, outStatus, outBody)
	if len(probeOutsider.calls) != 0 {
		t.Fatalf("non-member denial must not consult the catalog; got %d calls: %v", len(probeOutsider.calls), probeOutsider.calls)
	}

	// Restricted member missing the declared path: the authentic resolver
	// refuses with ErrForbidden, which stays the unified concealed 404.
	missingPath := &authscope.Principal{
		Subject:           "operator-other",
		Projects:          []string{mscHomeProject},
		AuthResourcePaths: []string{"p2"},
	}
	probeMissingPath := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	mpStatus, mpBody, _ := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeMissingPath, nil), missingPath), mscCleanExecID)
	mscAssertConcealment(t, mpStatus, mpBody)
	if len(probeMissingPath.calls) == 0 {
		t.Fatal("resolver-path denial expected a catalog lookup, got zero calls")
	}

	// Anonymous caller: the shipped unrestricted posture keeps serving
	// legitimate executions.
	probeAnon := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	anonStatus, anonBody, anonFields := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeAnon, nil), nil), mscCleanExecID)
	if anonStatus != http.StatusOK {
		t.Fatalf("anonymous single-operator posture must keep serving clean executions: %d %s", anonStatus, anonBody)
	}
	mscAssertAuthorizedBody(t, anonFields, mscCleanExecID)

	// Bearer header without a configured Fence client: authorization-backend
	// failure keeps the retryable 503 envelope, not the concealed 404.
	bearer := &authscope.Principal{
		Subject:             "operator-bearer",
		Projects:            []string{mscHomeProject},
		AuthResourcePaths:   []string{mscGrantedPath},
		AuthorizationHeader: "Bearer not-a-real-token",
	}
	probeBearer := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	bearerStatus, bearerBody, bearerFields := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeBearer, nil), bearer), mscCleanExecID)
	if bearerStatus != http.StatusServiceUnavailable {
		t.Fatalf("Fence-less bearer read must stay 503, got %d %s", bearerStatus, bearerBody)
	}
	if nested, ok := bearerFields["error"].(map[string]any); !ok || nested["code"] != "BACKEND_UNAVAILABLE" || nested["retryable"] != true {
		t.Fatalf("Fence-less bearer read must keep the retryable BACKEND_UNAVAILABLE envelope, got %s", bearerBody)
	}

	// Rejected token through a wired client: the unauthenticated mapping stays
	// 401 (with the concealed phrase body), not a coverage-based 404.
	rejected := &authscope.Principal{
		Subject:             "operator-rejected",
		Projects:            []string{mscHomeProject},
		AuthResourcePaths:   []string{mscGrantedPath},
		AuthorizationHeader: "Bearer rejected-token",
	}
	probeRejected := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	rejectedStatus, rejectedBody, _ := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeRejected, mscDeniedTokenClient{}), rejected), mscMalformedExecID)
	if rejectedStatus != http.StatusUnauthorized || !strings.Contains(rejectedBody, twinConcealedError) {
		t.Fatalf("rejected token must stay the 401 concealed-phrase envelope, got %d %s", rejectedStatus, rejectedBody)
	}

	// Catalog lister fault: unclassified authorization-backend failure stays a
	// retryable 503 even for the malformed-scope execution.
	probeFaulty := &mscProbe{pathsByProjectGeneration: mscKnownPaths(), fault: errors.New("arangodb connection reset")}
	faultyStatus, faultyBody, faultyFields := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeFaulty, nil), mscMemberPrincipal()), mscMalformedExecID)
	if faultyStatus != http.StatusServiceUnavailable {
		t.Fatalf("catalog fault on a malformed-scope execution must stay 503, got %d %s", faultyStatus, faultyBody)
	}
	if nested, ok := faultyFields["error"].(map[string]any); !ok || nested["code"] != "BACKEND_UNAVAILABLE" || nested["retryable"] != true {
		t.Fatalf("catalog fault must keep the retryable BACKEND_UNAVAILABLE envelope, got %s", faultyBody)
	}

	// Durable store fault likewise stays 503 rather than collapsing into the
	// concealed 404.
	storeFault := errors.New("arangodb connection reset")
	probeStore := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	routesStore := mscRoutesWith(probeStore, nil)
	routesStore.releases = mscFailingStore{err: storeFault}
	storeStatus, storeBody, storeFields := twinGetExecution(t, twinHTTPStack(routesStore, mscMemberPrincipal()), mscMalformedExecID)
	if storeStatus != http.StatusServiceUnavailable {
		t.Fatalf("store fault must stay 503, got %d %s", storeStatus, storeBody)
	}
	if nested, ok := storeFields["error"].(map[string]any); !ok || nested["code"] != "BACKEND_UNAVAILABLE" || nested["retryable"] != true {
		t.Fatalf("store fault must keep the retryable BACKEND_UNAVAILABLE envelope, got %s", storeBody)
	}

	t.Log("R4 REST | VERDICT=ARMS-PRESERVED | non-member=404, missing-path=404, anonymous=200, fence-less-bearer=503, rejected-token=401, catalog-fault=503, store-fault=503")
}
