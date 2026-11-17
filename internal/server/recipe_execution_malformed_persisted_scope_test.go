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

func TestRESTRecipeExecutionConcealsMalformedPersistedScope(t *testing.T) {
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

	malformedStatus, malformedBody, _ := twinGetExecution(t, app, mscMalformedExecID)
	mscAssertConcealment(t, malformedStatus, malformedBody)

	missingStatus, missingBody, _ := twinGetExecution(t, app, mscMissingID)
	if malformedStatus != missingStatus || malformedBody != missingBody {
		t.Fatalf("malformed-scope execution must be indistinguishable from a missing id; got %d %q vs %d %q",
			malformedStatus, malformedBody, missingStatus, missingBody)
	}

	cleanStatus, cleanBody, cleanFields := twinGetExecution(t, app, mscCleanExecID)
	if cleanStatus != http.StatusOK {
		t.Fatalf("R4 OVER-DENIAL: member granted p1 could not read the clean p1 execution: %d %s", cleanStatus, cleanBody)
	}
	mscAssertAuthorizedBody(t, cleanFields, mscCleanExecID)

	blankStatus, blankBody, _ := twinGetExecution(t, app, mscBlankExecID)
	mscAssertConcealment(t, blankStatus, blankBody)
	paddingStatus, paddingBody, paddingFields := twinGetExecution(t, app, mscPaddingExecID)
	if paddingStatus != http.StatusOK {
		t.Fatalf("OVER-DENIAL: blank padding must remain the legitimate absent policy: %d %s", paddingStatus, paddingBody)
	}
	mscAssertAuthorizedBody(t, paddingFields, mscPaddingExecID)

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

func TestRESTRecipeExecutionMalformedScopePreservesAuthorizationArms(t *testing.T) {
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

	probeAnon := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	anonStatus, anonBody, anonFields := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeAnon, nil), nil), mscCleanExecID)
	if anonStatus != http.StatusOK {
		t.Fatalf("anonymous single-operator posture must keep serving clean executions: %d %s", anonStatus, anonBody)
	}
	mscAssertAuthorizedBody(t, anonFields, mscCleanExecID)

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

	probeFaulty := &mscProbe{pathsByProjectGeneration: mscKnownPaths(), fault: errors.New("arangodb connection reset")}
	faultyStatus, faultyBody, faultyFields := twinGetExecution(t, twinHTTPStack(mscRoutesWith(probeFaulty, nil), mscMemberPrincipal()), mscMalformedExecID)
	if faultyStatus != http.StatusServiceUnavailable {
		t.Fatalf("catalog fault on a malformed-scope execution must stay 503, got %d %s", faultyStatus, faultyBody)
	}
	if nested, ok := faultyFields["error"].(map[string]any); !ok || nested["code"] != "BACKEND_UNAVAILABLE" || nested["retryable"] != true {
		t.Fatalf("catalog fault must keep the retryable BACKEND_UNAVAILABLE envelope, got %s", faultyBody)
	}

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
