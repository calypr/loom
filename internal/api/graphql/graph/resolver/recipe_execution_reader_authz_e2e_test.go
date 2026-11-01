package resolver

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

// This file adjudicates candidate F-B20 against the production GraphQL
// execution reader. It is a characterization suite: every assertion records
// what the shipped reader does today, and STEP 0 fails the suite the moment
// the shipped guards are edited or a real project-membership check is added.
//
// Scope of the probe. NewAuthorizedRecipeExecutionReader returns the closure
// that the GraphQL entry point schema.resolvers.go:211 DataframeRecipeExecution
// calls, and that entry point takes only an opaque id (no project argument), so
// the returned closure is the entire authorization path for this query. The
// suite drives that production closure directly; the only injected collaborator
// is the RecipeExecutionStore interface the reader itself declares at
// recipe_execution_reader.go:18-20.
//
// Why the scope resolver is not a hand-written fake. The constructor takes the
// concrete *authscope.ScopeResolver (recipe_execution_reader.go:25), so no fake
// type can be substituted. Cases B and C therefore build the production
// authscope.NewScopeResolver and control only its two declared seams
// (ScopeResolverConfig.ResourceAccess and
// ScopeResolverConfig.ListExistingAuthResourcePaths), which is what
// internal/authscope/scope_test.go does too. Consequently "scope says X" claims
// below are produced by real resolution code, not by an invented oracle.
//
// REST sibling deliberately out of scope. internal/server/
// openapi_recipe_execution.go:26 has the same "if r.scopes != nil {" posture and
// additionally echoes ProjectId (openapi_recipe_execution.go:64). A live
// behaviour test would need to construct HTTPRoutes with its unexported
// releases/scopes fields, which is only possible from package server, and this
// probe owns exactly this one file. It is covered here by source-level
// assertions in TestFB20PreventionPrecedentsAndRESTTwinStayQuoted plus the
// report note, not by behaviour.
const (
	fb20ForeignProject    = "Q"
	fb20ForeignGeneration = "gQ"
	fb20HomeProject       = "P"
	fb20ExecutionID       = "e1"
	fb20CallerPath        = "p1"
	fb20AlienPath         = "p2"
)

const (
	fb20RecipeName        = "foreign-recipe"
	fb20RecipeDigest      = "sha256-recipe-Q"
	fb20SchemaDigest      = "sha256-schema-Q"
	fb20OutputRowCount    = int64(4242)
	fb20ConcealedNotFound = "RECIPE_EXECUTION_NOT_FOUND"
)

const (
	fb20SourceReader     = "recipe_execution_reader.go"
	fb20SourceService    = "../dataframe/service.go"
	fb20SourceCapability = "../../../../server/explorer_capability.go"
	fb20SourceTwin       = "../../../../server/openapi_recipe_execution.go"
)

// fb20QuotedFacts are the exact source fragments this adjudication is built on.
// Each entry is a promise the candidate relies on: the two fetch-then-check
// guards, the count-only comparison, the reader's own "IDs are not credentials"
// promise, the AuthorizeProject prevention precedents, and the REST sibling
// guard. Any disappearance is drift and must fail the suite rather than let a
// stale finding pass.
var fb20QuotedFacts = []struct {
	name   string
	source string
	quote  string
	notice string
}{
	{
		name:   "fetch-by-opaque-id-precedes-any-check",
		source: fb20SourceReader,
		quote:  "execution, err := store.GetExecution(ctx, id)",
		notice: "reader fetches the durable execution before consulting authorization",
	},
	{
		name:   "authz-only-when-scope-resolver-present",
		source: fb20SourceReader,
		quote:  "if scopeResolver != nil {",
		notice: "the whole authorization block is conditional on a non-nil scope resolver",
	},
	{
		name:   "count-compare-instead-of-membership",
		source: fb20SourceReader,
		quote:  "if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) != len(execution.AuthResourcePaths) {",
		notice: "restricted-mode gate compares path COUNTS, never set membership",
	},
	{
		name:   "reader-promise-ids-are-not-credentials",
		source: fb20SourceReader,
		quote:  "themselves authorization credentials.",
		notice: "doc promise that an opaque execution id must not be treated as authorization",
	},
	{
		name:   "precedent-dataframe-authorize-project",
		source: fb20SourceService,
		quote:  "if err := authscope.AuthorizeProject(principal, project, false); err != nil {",
		notice: "prevention precedent: dataframe service asserts project membership",
	},
	{
		name:   "precedent-explorer-capability-authorize-project",
		source: fb20SourceCapability,
		quote:  "if err := authscope.AuthorizeProject(principal, project, false); err != nil {",
		notice: "prevention precedent: explorer capability asserts project membership",
	},
	{
		name:   "rest-twin-same-conditional-guard",
		source: fb20SourceTwin,
		quote:  "if r.scopes != nil {",
		notice: "REST sibling carries the same conditional authorization block",
	},
	{
		name:   "rest-twin-discloses-project",
		source: fb20SourceTwin,
		quote:  "ProjectId:            execution.Project,",
		notice: "REST sibling maps the execution's project into the response body",
	},
}

// fb20ExecutionStore is the smallest fake for the reader's own RecipeExecutionStore
// interface (recipe_execution_reader.go:18-20). It yields one published,
// ready-output execution that belongs to the foreign project.
type fb20ExecutionStore struct {
	execution materialization.BundleExecution
	err       error
	ids       []string
}

func (s *fb20ExecutionStore) GetExecution(_ context.Context, id string) (materialization.BundleExecution, error) {
	s.ids = append(s.ids, id)
	if s.err != nil {
		return materialization.BundleExecution{}, s.err
	}
	return s.execution, nil
}

// fb20ScopeProbe drives the two documented seams of authscope.ScopeResolverConfig
// so the production resolver can resolve restricted scopes without a live Fence
// or a catalog. Existing paths are keyed by the project the resolver actually
// asked about, which also proves which project the scope was resolved against.
type fb20ScopeProbe struct {
	existingByProject map[string][]string
	calls             []catalog.AuthResourcePathOptions
}

func (p *fb20ScopeProbe) listExisting(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	p.calls = append(p.calls, opts)
	return append([]string(nil), p.existingByProject[opts.Project]...), nil
}

func fb20ForeignBundle(authResourcePaths []string) materialization.BundleExecution {
	return materialization.BundleExecution{
		ID: fb20ExecutionID,
		BundleIdentity: materialization.BundleIdentity{
			Name:              fb20RecipeName,
			Project:           fb20ForeignProject,
			DatasetGeneration: fb20ForeignGeneration,
			RecipeDigest:      fb20RecipeDigest,
			SchemaDigest:      fb20SchemaDigest,
			AuthResourcePaths: authResourcePaths,
		},
		State: materialization.BundlePublished,
		Outputs: []materialization.BundleOutputRecord{{
			Name:     "rows",
			State:    materialization.BundleReady,
			RowCount: fb20OutputRowCount,
		}},
	}
}

func fb20ForeignPrincipal() *authscope.Principal {
	return &authscope.Principal{
		Subject:           "operator-" + fb20HomeProject,
		Projects:          []string{fb20HomeProject},
		AuthResourcePaths: []string{"p-" + fb20HomeProject},
	}
}

func fb20ReadScopeResolver(t *testing.T, probe *fb20ScopeProbe) *authscope.ScopeResolver {
	t.Helper()
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: probe.listExisting,
	})
}

// fb20RequireProductionGuards is STEP 0. It pins every quoted fragment and the
// two negative facts that define the candidate: the reader must NOT contain a
// project-membership call, and its restricted-mode gate must remain a pure
// count comparison. A fix that adds AuthorizeProject retires this finding by
// failing here with an explicit message.
func fb20RequireProductionGuards(t *testing.T) {
	t.Helper()
	contents := make(map[string]string, len(fb20QuotedFacts))
	for _, fact := range fb20QuotedFacts {
		source, ok := contents[fact.source]
		if !ok {
			raw, err := os.ReadFile(fact.source)
			if err != nil {
				t.Fatalf("STEP 0 drift: cannot read %s for %s: %v", fact.source, fact.name, err)
			}
			source = string(raw)
			contents[fact.source] = source
		}
		if !strings.Contains(source, fact.quote) {
			t.Fatalf("STEP 0 drift: %s no longer contains quoted %s\n  file:  %s\n  quote: %s\n  note:  %s",
				fact.name, fact.source, fact.source, fact.quote, fact.notice)
		}
		t.Logf("FB20 GATE | %s | %s:%d contains %q", fact.name, fact.source, fb20LineOf(source, fact.quote), fact.quote)
	}

	reader := contents[fb20SourceReader]
	if strings.Contains(reader, "authscope.AuthorizeProject") {
		t.Fatalf("STEP 0 drift: %s now calls authscope.AuthorizeProject, so F-B20's missing-membership claim no longer holds. Re-adjudicate before trusting any verdict below.", fb20SourceReader)
	}
	t.Logf("FB20 GATE | reader-performs-no-project-membership-check | %s contains no authscope.AuthorizeProject call", fb20SourceReader)
	if strings.Contains(reader, "slices.Contains") || strings.Contains(reader, "containsAuthResourcePath") {
		t.Fatalf("STEP 0 drift: %s now compares auth paths by membership, so F-B20's count-compare claim no longer holds.", fb20SourceReader)
	}
}

func fb20LineOf(source, needle string) int {
	index := strings.Index(source, needle)
	if index < 0 {
		return 0
	}
	return strings.Count(source[:index], "\n") + 1
}

func fb20DisclosedForeignMetadata(t *testing.T, got *RecipeExecution, store *fb20ExecutionStore) string {
	t.Helper()
	if store.execution.ID != fb20ExecutionID || got.ID != fb20ExecutionID {
		t.Fatalf("expected the requested opaque id %q, got reader id %q store id %q", fb20ExecutionID, got.ID, store.execution.ID)
	}
	if got.Name != fb20RecipeName {
		t.Fatalf("disclosed recipe name = %q, want %q", got.Name, fb20RecipeName)
	}
	if got.RecipeDigest != fb20RecipeDigest || got.ResolvedSchemaDigest != fb20SchemaDigest {
		t.Fatalf("disclosed digests = recipe %q schema %q, want %q / %q",
			got.RecipeDigest, got.ResolvedSchemaDigest, fb20RecipeDigest, fb20SchemaDigest)
	}
	if got.SourceGeneration != fb20ForeignGeneration {
		t.Fatalf("disclosed source generation = %q, want the foreign generation %q", got.SourceGeneration, fb20ForeignGeneration)
	}
	if got.State != "PUBLISHED" {
		t.Fatalf("disclosed state = %q, want PUBLISHED", got.State)
	}
	if len(got.Outputs) != 1 {
		t.Fatalf("disclosed outputs = %d, want 1", len(got.Outputs))
	}
	if got.Outputs[0].RowCount == nil || *got.Outputs[0].RowCount != int(fb20OutputRowCount) {
		t.Fatalf("disclosed row count = %v, want %d", got.Outputs[0].RowCount, fb20OutputRowCount)
	}
	if got.Outputs[0].State != "READY" {
		t.Fatalf("disclosed output state = %q, want READY", got.Outputs[0].State)
	}
	return "id=" + got.ID + " name=" + got.Name + " generation=" + got.SourceGeneration +
		" recipeDigest=" + got.RecipeDigest + " schemaDigest=" + got.ResolvedSchemaDigest +
		" state=" + got.State + " output[0].rowCount=" + fb20Itoa(got.Outputs[0].RowCount) +
		" output[0].state=" + got.Outputs[0].State
}

func fb20Itoa(value *int) string {
	if value == nil {
		return "<nil>"
	}
	return strconv.Itoa(*value)
}

func fb20Itoi(value int) string {
	return strconv.Itoa(value)
}

func fb20Verdict(t *testing.T, id, verdict, observed string) {
	t.Helper()
	t.Logf("FB20 %s | VERDICT=%s | OBSERVED=%s", id, verdict, observed)
}

func fb20DeniedCode(t *testing.T, err error) string {
	t.Helper()
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("denial was not a semantic user error: %v", err)
	}
	return userErr.Code()
}

// fb20AssertMembershipDeniedByTheRealAuthorizer proves the caller genuinely
// lacks project membership using the shipped AuthorizeProject that the
// precedents call. Without it, a disclosure result would be vacuous because the
// "foreign" caller might have been authorized after all.
func fb20AssertMembershipDeniedByTheRealAuthorizer(t *testing.T, principal *authscope.Principal) {
	t.Helper()
	err := authscope.AuthorizeProject(principal, fb20ForeignProject, false)
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("expected the shipped AuthorizeProject to deny project %q for principal %+v, got %v",
			fb20ForeignProject, principal, err)
	}
	t.Logf("FB20 CONTROL | shipped-authorize-project-denies-foreign-project | authscope.AuthorizeProject(principal, %q, false) = %v",
		fb20ForeignProject, err)
}

// Case A: shipped baseline posture. scopeResolver == nil (server.go:648 passes
// auth.scopeResolver, which is nil whenever no Fence is configured) and a
// principal whose project allowlist holds only "P".
func TestFB20NilScopeResolverDisclosesForeignExecution(t *testing.T) {
	fb20RequireProductionGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	reader := NewAuthorizedRecipeExecutionReader(store, nil)
	principal := fb20ForeignPrincipal()
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	got, err := reader(ctx, fb20ExecutionID)
	if err != nil {
		fb20Verdict(t, "case-A", "PREVENTED", "reader denied with: "+err.Error())
		t.Fatalf("case A expected the shipped nil-resolver path to return the foreign execution, got error: %v", err)
	}
	disclosed := fb20DisclosedForeignMetadata(t, got, store)
	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	if len(store.ids) != 1 || store.ids[0] != fb20ExecutionID {
		t.Fatalf("expected exactly one fetch of the opaque id, got %v", store.ids)
	}
	fb20Verdict(t, "case-A", "CONFIRMED (baseline)",
		"scopeResolver=nil skips recipe_execution_reader.go:34 entirely: an id-only lookup disclosed cross-project metadata "+
			disclosed+" for a principal whose Projects=[P] only; the reader never asserts membership (no AuthorizeProject) "+
			"and never reads the execution's Project before returning")
}

// Case A2: a scope resolver IS configured but the caller carries neither
// auth_resource_paths nor a bearer token, so the production resolver treats the
// caller as unrestricted. This separates "missing resolver" from "resolver
// present but no fence grants".
func TestFB20ResolverPresentWithoutCallerGrantsStillDiscloses(t *testing.T) {
	fb20RequireProductionGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}}}
	reader := NewAuthorizedRecipeExecutionReader(store, fb20ReadScopeResolver(t, probe))
	principal := &authscope.Principal{Subject: "no-grants", Projects: []string{fb20HomeProject}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	got, err := reader(ctx, fb20ExecutionID)
	if err != nil {
		fb20Verdict(t, "case-A2", "PREVENTED", "reader denied with: "+err.Error())
		t.Fatalf("case A2 expected disclosure for a grant-less caller, got error: %v", err)
	}
	disclosed := fb20DisclosedForeignMetadata(t, got, store)
	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	fb20Verdict(t, "case-A2", "CONFIRMED (baseline)",
		"non-nil resolver, caller with no AuthResourcePaths and no bearer is resolved unrestricted by "+
			"scope_resolver.go resolveCallerPaths, so recipe_execution_reader.go:40 is skipped (mode != restricted) and "+
			disclosed+" was disclosed for project "+fb20ForeignProject)
}

// Case B1: restricted-mode happy path with equal counts. The caller holds the
// very auth-path string the foreign execution carries (a path that is populated
// in the foreign project's generation) while its project allowlist excludes the
// foreign project.
func TestFB20RestrictedModeAcceptsOnPathStringAlone(t *testing.T) {
	fb20RequireProductionGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}}}
	resolver := fb20ReadScopeResolver(t, probe)
	reader := NewAuthorizedRecipeExecutionReader(store, resolver)
	principal := &authscope.Principal{
		Subject:           "operator-" + fb20HomeProject,
		Projects:          []string{fb20HomeProject},
		AuthResourcePaths: []string{fb20CallerPath},
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	scope, scopeErr := resolver.ResolveReadScopeForGeneration(ctx, principal, fb20ForeignProject, fb20ForeignGeneration, []string{fb20CallerPath})
	if scopeErr != nil {
		t.Fatalf("precondition: production resolver should authorize the held path, got %v", scopeErr)
	}
	if len(scope.AuthResourcePaths) != len(store.execution.AuthResourcePaths) {
		t.Fatalf("precondition: counts should be equal (%d vs %d)",
			len(scope.AuthResourcePaths), len(store.execution.AuthResourcePaths))
	}

	got, err := reader(ctx, fb20ExecutionID)
	if err != nil {
		fb20Verdict(t, "case-B1", "PREVENTED", "reader denied with: "+err.Error())
		t.Fatalf("case B1 expected the equal-count restricted path to accept, got error: %v", err)
	}
	disclosed := fb20DisclosedForeignMetadata(t, got, store)
	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	queried := "none"
	for _, call := range probe.calls {
		queried = "project=" + call.Project + " generation=" + call.DatasetGeneration
	}
	fb20Verdict(t, "case-B1", "CONFIRMED",
		"restricted scope with EQUAL counts (execution "+
			strings.Join(store.execution.AuthResourcePaths, ",")+" vs scope "+
			strings.Join(scope.AuthResourcePaths, ",")+") was accepted on the auth-path string alone; "+
			"the reader never called AuthorizeProject for project "+fb20ForeignProject+" (which the shipped "+
			"authorizer denies) and resolved scope for the foreign generation ("+queried+") instead of "+
			"asserting membership; disclosed "+disclosed)
}

// Case B2: the literal "equal count, different paths" request. This probes
// whether recipe_execution_reader.go:40 can be walked past by a scope whose
// single path is semantically different from the execution's.
func TestFB20CountCompareCannotAdmitADifferentSinglePath(t *testing.T) {
	fb20RequireProductionGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20AlienPath}}}
	resolver := fb20ReadScopeResolver(t, probe)
	reader := NewAuthorizedRecipeExecutionReader(store, resolver)
	principal := &authscope.Principal{
		Subject:           "operator-" + fb20HomeProject,
		Projects:          []string{fb20HomeProject},
		AuthResourcePaths: []string{fb20AlienPath},
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	_, scopeErr := resolver.ResolveReadScopeForGeneration(ctx, principal, fb20ForeignProject, fb20ForeignGeneration, store.execution.AuthResourcePaths)
	if !errors.Is(scopeErr, authscope.ErrForbidden) {
		t.Fatalf("precondition: a caller holding only %q must be refused the execution's %q by the production resolver, got %v",
			fb20AlienPath, fb20CallerPath, scopeErr)
	}

	got, err := reader(ctx, fb20ExecutionID)
	if err == nil {
		disclosed := fb20DisclosedForeignMetadata(t, got, store)
		fb20Verdict(t, "case-B2", "CONFIRMED",
			"count-compare admitted a scope whose only path differs from the execution's: "+disclosed)
		t.Fatalf("case B2 expected the equal-count/different-path state to be refused, but the reader disclosed it")
	}
	if got != nil {
		t.Fatalf("case B2 returned a payload alongside an error: %#v", got)
	}
	if code := fb20DeniedCode(t, err); code != fb20ConcealedNotFound {
		t.Fatalf("case B2 denial code = %q, want concealed %q", code, fb20ConcealedNotFound)
	}
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("case B2 denial did not carry the resolver's forbidden condition, so it did not come from the resolver: %v", err)
	}
	fb20Verdict(t, "case-B2", "PREVENTED",
		"no equal-count/different-single-path state is reachable through the production resolver: "+
			"ResolveReadScopeForGeneration echoes the requested list and refuses it when a path is outside the "+
			"caller's grants (scope_resolver.go:176 \"authResourcePath %q is outside caller scope\"), which the reader "+
			"surfaces at recipe_execution_reader.go:38 \"return nil, recipeExecutionLookupError(scopeErr)\" with code "+
			fb20ConcealedNotFound+
			"; the count comparison at :40 never participated in this decision")
}

// Case C: control that makes A/B1 non-vacuous. Both rows prove
// recipe_execution_reader.go:40 does fire, and that it fires on COUNT alone even
// though the caller is authorized on every distinct path.
func TestFB20CountCompareFiresOnCountMismatchAlone(t *testing.T) {
	fb20RequireProductionGuards(t)
	tests := []struct {
		name              string
		executionPaths    []string
		callerPaths       []string
		existingByProject map[string][]string
	}{
		{
			name:              "execution-lists-nothing-caller-holds-one-path",
			executionPaths:    nil,
			callerPaths:       []string{fb20CallerPath},
			existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}},
		},
		{
			name:              "execution-lists-a-duplicate-of-the-same-path",
			executionPaths:    []string{fb20CallerPath, fb20CallerPath},
			callerPaths:       []string{fb20CallerPath},
			existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fb20ExecutionStore{execution: fb20ForeignBundle(test.executionPaths)}
			probe := &fb20ScopeProbe{existingByProject: test.existingByProject}
			resolver := fb20ReadScopeResolver(t, probe)
			reader := NewAuthorizedRecipeExecutionReader(store, resolver)
			principal := &authscope.Principal{
				Subject:           "operator-" + fb20HomeProject,
				Projects:          []string{fb20ForeignProject},
				AuthResourcePaths: test.callerPaths,
			}
			ctx := authscope.ContextWithPrincipal(context.Background(), principal)

			scope, scopeErr := resolver.ResolveReadScopeForGeneration(ctx, principal, fb20ForeignProject, fb20ForeignGeneration, test.executionPaths)
			if scopeErr != nil {
				t.Fatalf("control precondition failed: the production resolver itself should succeed so that any "+
					"denial can be attributed to the reader's count comparison, got %v", scopeErr)
			}
			if scope.Mode != authscope.ReadScopeRestricted {
				t.Fatalf("control precondition failed: expected a restricted scope so recipe_execution_reader.go:40 is eligible, got mode %q", scope.Mode)
			}
			if len(scope.AuthResourcePaths) == len(store.execution.AuthResourcePaths) {
				t.Fatalf("control precondition failed: counts should differ (scope %d vs execution %d)",
					len(scope.AuthResourcePaths), len(store.execution.AuthResourcePaths))
			}

			got, err := reader(ctx, fb20ExecutionID)
			if err == nil {
				t.Fatalf("control failed: recipe_execution_reader.go:40 never fired; reader disclosed %#v", got)
			}
			if got != nil {
				t.Fatalf("reader returned a payload alongside an error: %#v", got)
			}
			if code := fb20DeniedCode(t, err); code != fb20ConcealedNotFound {
				t.Fatalf("denial code = %q, want concealed %q", code, fb20ConcealedNotFound)
			}
			if errors.Is(err, authscope.ErrForbidden) {
				t.Fatalf("denial carried the resolver's forbidden condition, so it came from :38 and the control did not isolate :40: %v", err)
			}
			fb20Verdict(t, "case-C", "CONFIRMED (control: count-compare is live and membership-blind)",
				"resolver returned a valid restricted scope of "+strings.Join(scope.AuthResourcePaths, ",")+
					" for a caller authorized on that path, yet recipe_execution_reader.go:40-"+
					"41 denied with "+fb20ConcealedNotFound+" purely on len(scope.AuthResourcePaths)="+
					fb20Itoi(len(scope.AuthResourcePaths))+" != len(execution.AuthResourcePaths)="+
					fb20Itoi(len(store.execution.AuthResourcePaths))+
					"; the block is reachable, so the acceptance results in cases A/A2/B1 are not vacuous, "+
					"and membership is not what the gate tests")
		})
	}
}

// TestFB20PreventionPrecedentsAndRESTTwinStayQuoted keeps the contrast evidence
// honest and records the REST sibling's shipped posture. The behaviour of
// internal/server/openapi_recipe_execution.go is not exercised here because
// HTTPRoutes.releases and HTTPRoutes.scopes are unexported and this probe owns
// only this file.
func TestFB20PreventionPrecedentsAndRESTTwinStayQuoted(t *testing.T) {
	fb20RequireProductionGuards(t)
	service, err := os.ReadFile(fb20SourceService)
	if err != nil {
		t.Fatalf("read %s: %v", fb20SourceService, err)
	}
	capability, err := os.ReadFile(fb20SourceCapability)
	if err != nil {
		t.Fatalf("read %s: %v", fb20SourceCapability, err)
	}
	twin, err := os.ReadFile(fb20SourceTwin)
	if err != nil {
		t.Fatalf("read %s: %v", fb20SourceTwin, err)
	}
	for _, source := range []struct {
		name string
		text string
	}{
		{fb20SourceService, string(service)},
		{fb20SourceCapability, string(capability)},
	} {
		if count := strings.Count(source.text, "authscope.AuthorizeProject("); count < 2 {
			t.Fatalf("%s should keep at least two authscope.AuthorizeProject call sites as prevention precedent, found %d",
				source.name, count)
		}
		t.Logf("FB20 PRECEDENT | %s asserts project membership via authscope.AuthorizeProject at line %d",
			source.name, fb20LineOf(source.text, "authscope.AuthorizeProject("))
	}
	twinGuard := fb20LineOf(string(twin), "if r.scopes != nil {")
	if twinGuard == 0 {
		t.Fatalf("REST sibling lost its conditional scope guard")
	}
	t.Logf("FB20 SIBLING | %s:%d guards authorization with %q and %d discloses the execution's project; "+
		"live behaviour coverage belongs in package server (unexported HTTPRoutes.releases/scopes) and is not probed here",
		fb20SourceTwin, twinGuard, "if r.scopes != nil {", fb20LineOf(string(twin), "ProjectId:            execution.Project,"))
}
