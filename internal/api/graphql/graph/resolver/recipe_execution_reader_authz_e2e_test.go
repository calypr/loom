package resolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

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

var fb20QuotedFacts = []struct {
	name   string
	source string
	quote  string
	notice string
}{
	{
		name:   "fetch-by-opaque-id-precedes-gates",
		source: fb20SourceReader,
		quote:  "execution, err := store.GetExecution(ctx, id)",
		notice: "reader fetches the durable execution before consulting authorization; denials must conceal",
	},
	{
		name:   "reader-asserts-project-membership-unconditionally",
		source: fb20SourceReader,
		quote:  "if err := authscope.AuthorizeProject(principal, execution.Project, false); err != nil {",
		notice: "project membership is asserted for every request, not only when a scope resolver is wired",
	},
	{
		name:   "restricted-gate-is-the-shared-path-set-rule",
		source: fb20SourceReader,
		quote:  "if scope.Mode == authscope.ReadScopeRestricted && !scope.CoversExecutionPaths(execution.AuthResourcePaths) {",
		notice: "restricted-mode gate compares normalized path SETS through the shared rule, never counts",
	},
	{
		name:   "rest-twin-asserts-project-membership-unconditionally",
		source: fb20SourceTwin,
		quote:  "if err := authscope.AuthorizeProject(principal, execution.Project, false); err != nil {",
		notice: "REST sibling carries the same unconditional project-membership assertion",
	},
	{
		name:   "rest-twin-restricted-gate-is-the-shared-path-set-rule",
		source: fb20SourceTwin,
		quote:  "if scope.Mode == authscope.ReadScopeRestricted && !scope.CoversExecutionPaths(execution.AuthResourcePaths) {",
		notice: "REST sibling carries the same restricted-mode set rule",
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
		name:   "rest-twin-discloses-project-to-authorized-callers",
		source: fb20SourceTwin,
		quote:  "ProjectId:            execution.Project,",
		notice: "REST sibling maps the execution's project into the response body for authorized callers",
	},
}

var fb20ArchivedPreFixFacts = []struct {
	name   string
	source string
	quote  string
	notice string
}{
	{
		name:   "archived-nil-resolver-zero-authorization-shape",
		source: fb20SourceReader,
		quote:  "if scopeResolver != nil {\n\t\t\tprincipal, _ := authscope.PrincipalFromContext(ctx)",
		notice: "the archived shape consulted authorization only inside the resolver-conditional block",
	},
	{
		name:   "archived-count-compare-shape",
		source: fb20SourceReader,
		quote:  "len(scope.AuthResourcePaths) != len(execution.AuthResourcePaths)",
		notice: "the archived restricted gate compared path counts, never set membership",
	},
}

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

func fb20RequireRepairedGuards(t *testing.T) {
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
		t.Logf("FB20 GATE | %s | %s contains %q at line %d", fact.name, fact.source, fact.quote, fb20LineOf(source, fact.quote))
	}
	for _, fact := range fb20ArchivedPreFixFacts {
		source, ok := contents[fact.source]
		if !ok {
			raw, err := os.ReadFile(fact.source)
			if err != nil {
				t.Fatalf("STEP 0 drift: cannot read %s for %s: %v", fact.source, fact.name, err)
			}
			source = string(raw)
			contents[fact.source] = source
		}
		if strings.Contains(source, fact.quote) {
			t.Fatalf("FB20 REGRESSION | %s restored the archived pre-fix shape\n  file:  %s\n  quote: %s\n  note:  %s",
				fact.name, fact.source, fact.quote, fact.notice)
		}
		t.Logf("FB20 GATE | %s | %s keeps archived shape absent", fact.name, fact.source)
	}
}

func fb20LineOf(source, needle string) int {
	index := strings.Index(source, needle)
	if index < 0 {
		return 0
	}
	return strings.Count(source[:index], "\n") + 1
}

func fb20AssertConcealedDenial(t *testing.T, got *RecipeExecution, err error) string {
	t.Helper()
	if got != nil {
		t.Fatalf("denial returned a payload alongside an error: %#v", got)
	}
	if err == nil {
		t.Fatal("expected a concealed denial, got success")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("denial was not a semantic user error: %v", err)
	}
	if userErr.Code() != fb20ConcealedNotFound {
		t.Fatalf("denial code = %q, want the concealed code %q (denied reads must not be distinguishable from missing ids)",
			userErr.Code(), fb20ConcealedNotFound)
	}
	return "code=" + userErr.Code()
}

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

func fb20Verdict(t *testing.T, id, verdict, observed string) {
	t.Helper()
	t.Logf("FB20 %s | VERDICT=%s | OBSERVED=%s", id, verdict, observed)
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

func TestFB20NilScopeResolverRefusesForeignPrincipal(t *testing.T) {
	fb20RequireRepairedGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	reader := NewAuthorizedRecipeExecutionReader(store, nil)
	principal := fb20ForeignPrincipal()
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	got, err := reader(ctx, fb20ExecutionID)
	if err == nil {
		disclosed := fb20DisclosedForeignMetadata(t, got, store)
		t.Fatalf("FB20 REGRESSION: scopeResolver=nil + a principal whose Projects=[P] only disclosed cross-project metadata %s — "+
			"the archived F-B20 defect state (the reader skipped authorization entirely without a resolver). "+
			"Archived baseline for reference: F-B20 CONFIRMED disclosed for a non-member exactly like this.", disclosed)
	}
	denied := fb20AssertConcealedDenial(t, got, err)
	if len(store.ids) != 1 || store.ids[0] != fb20ExecutionID {
		t.Fatalf("expected exactly one fetch of the opaque id before the membership gate, got %v", store.ids)
	}
	fb20Verdict(t, "case-A", "REPAIRED (concealed refusal)",
		"nil scopeResolver no longer skips authorization: the unconditional authscope.AuthorizeProject in "+
			fb20SourceReader+" refused the foreign principal with "+denied+
			" and errors.Is(err, authscope.ErrForbidden); the anonymous (principal-less) posture stays allowed by design")
}

func TestFB20ResolverPresentWithoutCallerGrantsIsRefused(t *testing.T) {
	fb20RequireRepairedGuards(t)
	store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}}}
	reader := NewAuthorizedRecipeExecutionReader(store, fb20ReadScopeResolver(t, probe))
	principal := &authscope.Principal{Subject: "no-grants", Projects: []string{fb20HomeProject}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)

	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	got, err := reader(ctx, fb20ExecutionID)
	if err == nil {
		disclosed := fb20DisclosedForeignMetadata(t, got, store)
		t.Fatalf("FB20 REGRESSION: grant-less caller with a configured resolver disclosed %s — "+
			"unrestricted resolution must not bypass the membership gate", disclosed)
	}
	denied := fb20AssertConcealedDenial(t, got, err)
	if len(probe.calls) != 0 {
		t.Fatalf("membership denial must short-circuit before consulting the scope resolver, yet the catalog was queried %d times: %v",
			len(probe.calls), probe.calls)
	}
	fb20Verdict(t, "case-A2", "REPAIRED (concealed refusal before resolver)",
		"the membership gate fires before any catalog consultation: "+denied+
			"; zero listExisting calls prove the ordering (archived shape required unrestricted mode to fall through)")
}

func TestFB20RestrictedPathStringAloneNoLongerAdmits(t *testing.T) {
	fb20RequireRepairedGuards(t)
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
	afterPreconditionCalls := len(probe.calls)
	if scope.Mode != authscope.ReadScopeRestricted || strings.Join(scope.AuthResourcePaths, ",") != fb20CallerPath {
		t.Fatalf("precondition: expected restricted scope of [%s], got mode %q paths %v", fb20CallerPath, scope.Mode, scope.AuthResourcePaths)
	}

	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principal)
	got, err := reader(ctx, fb20ExecutionID)
	if err == nil {
		disclosed := fb20DisclosedForeignMetadata(t, got, store)
		t.Fatalf("FB20 REGRESSION: restricted caller holding path string %q disclosed foreign project %q metadata "+
			disclosed+" without project membership", fb20CallerPath, fb20ForeignProject)
	}
	denied := fb20AssertConcealedDenial(t, got, err)
	if len(probe.calls) != afterPreconditionCalls {
		t.Fatalf("membership denial must not consult the resolver beyond the precondition probe; "+
			"catalog went from %d to %d calls: %v", afterPreconditionCalls, len(probe.calls), probe.calls)
	}
	fb20Verdict(t, "case-B1", "REPAIRED (membership precedes path acceptance)",
		"equal counts and the exact held path string no longer accept a non-member: the resolver alone would have "+
			"authorized, yet the reader refused with "+denied+" before any catalog call")
}

func TestFB20PathMismatchStaysConcealedForMemberAndNonMember(t *testing.T) {
	fb20RequireRepairedGuards(t)
	alienPrincipal := &authscope.Principal{
		Subject:           "alien-" + fb20ForeignProject,
		Projects:          []string{fb20ForeignProject},
		AuthResourcePaths: []string{fb20AlienPath},
	}
	storeM := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probeM := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20AlienPath}}}
	readerM := NewAuthorizedRecipeExecutionReader(storeM, fb20ReadScopeResolver(t, probeM))
	ctxM := authscope.ContextWithPrincipal(context.Background(), alienPrincipal)
	gotM, errM := readerM(ctxM, fb20ExecutionID)
	deniedM := fb20AssertConcealedDenial(t, gotM, errM)
	if len(probeM.calls) == 0 {
		t.Fatalf("member/missing-path denial should come from the resolver path (which consults the catalog), got zero calls")
	}

	storeN := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath})}
	probeN := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20AlienPath}}}
	readerN := NewAuthorizedRecipeExecutionReader(storeN, fb20ReadScopeResolver(t, probeN))
	principalN := &authscope.Principal{
		Subject:           "operator-" + fb20HomeProject,
		Projects:          []string{fb20HomeProject},
		AuthResourcePaths: []string{fb20AlienPath},
	}
	ctxN := authscope.ContextWithPrincipal(context.Background(), principalN)
	fb20AssertMembershipDeniedByTheRealAuthorizer(t, principalN)
	gotN, errN := readerN(ctxN, fb20ExecutionID)
	deniedN := fb20AssertConcealedDenial(t, gotN, errN)
	if len(probeN.calls) != 0 {
		t.Fatalf("non-member denial must short-circuit before the resolver; catalog queried %d times: %v", len(probeN.calls), probeN.calls)
	}
	fb20Verdict(t, "case-B2", "REPAIRED (uniform concealment, ordered gates)",
		"member missing the execution path is refused via the resolver ("+deniedM+"), the non-member is refused by "+
			"membership with the identical concealed code ("+deniedN+") without touching the catalog; denied and missing "+
			"remain indistinguishable to callers")
}

func TestFB20SetRuleReplacesCountCompare(t *testing.T) {
	fb20RequireRepairedGuards(t)
	t.Run("authorized-caller-duplicate-declaration-is-served", func(t *testing.T) {
		store := &fb20ExecutionStore{execution: fb20ForeignBundle([]string{fb20CallerPath, fb20CallerPath})}
		probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}}}
		reader := NewAuthorizedRecipeExecutionReader(store, fb20ReadScopeResolver(t, probe))
		principal := &authscope.Principal{
			Subject:           "operator-" + fb20ForeignProject,
			Projects:          []string{fb20ForeignProject},
			AuthResourcePaths: []string{fb20CallerPath},
		}
		ctx := authscope.ContextWithPrincipal(context.Background(), principal)
		got, err := reader(ctx, fb20ExecutionID)
		if err != nil {
			t.Fatalf("FB20 REGRESSION (over-rejection restored): authorized member with duplicate path declarations "+
				"%v was denied: %v — raw-vs-normalized count differences must not change the decision", []string{fb20CallerPath, fb20CallerPath}, err)
		}
		disclosed := fb20DisclosedForeignMetadata(t, got, store)
		if len(probe.calls) == 0 {
			t.Fatal("authorized restricted read should still consult the production resolver")
		}
		fb20Verdict(t, "case-C1", "REPAIRED (set rule, no count over-rejection)",
			"execution declaring "+fb20CallerPath+" twice served to the caller authorized on that distinct path: "+disclosed)
	})
	t.Run("restricted-execution-without-declared-paths-is-refused", func(t *testing.T) {
		for _, declared := range [][]string{nil, {""}, {"  "}} {
			store := &fb20ExecutionStore{execution: fb20ForeignBundle(declared)}
			probe := &fb20ScopeProbe{existingByProject: map[string][]string{fb20ForeignProject: {fb20CallerPath}}}
			reader := NewAuthorizedRecipeExecutionReader(store, fb20ReadScopeResolver(t, probe))
			principal := &authscope.Principal{
				Subject:           "operator-" + fb20ForeignProject,
				Projects:          []string{fb20ForeignProject},
				AuthResourcePaths: []string{fb20CallerPath},
			}
			ctx := authscope.ContextWithPrincipal(context.Background(), principal)
			got, err := reader(ctx, fb20ExecutionID)
			if err == nil {
				t.Fatalf("FB20 REGRESSION: restricted execution declaring %v was served to a restricted caller; "+
					"an execution with no non-blank auth path has no resolvable authorization and must be refused", declared)
			}
			fb20AssertConcealedDenial(t, got, err)
		}
		fb20Verdict(t, "case-C2", "REPAIRED (fail-closed blank declarations)",
			"restricted executions declaring no non-blank auth path are refused for restricted callers (nil, empty and whitespace "+
				"declarations all concealed); unrestricted callers keep the shipped no-path-filtering behavior")
	})
}

func TestFB20PreventionPrecedentsAndRESTTwinStayQuoted(t *testing.T) {
	fb20RequireRepairedGuards(t)
	service, err := os.ReadFile(fb20SourceService)
	if err != nil {
		t.Fatalf("read %s: %v", fb20SourceService, err)
	}
	capability, err := os.ReadFile(fb20SourceCapability)
	if err != nil {
		t.Fatalf("read %s: %v", fb20SourceCapability, err)
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
	if fmt.Sprint(len(fb20QuotedFacts)) == "" {
		t.Fatal("unreachable string safety")
	}
}
