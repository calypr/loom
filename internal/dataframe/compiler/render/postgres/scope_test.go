package postgres

import (
	"context"
	"encoding/json"
	"errors"
	control "github.com/calypr/loom/internal/backends/postgres/control"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pgsq02ScopePlan(project, generation string, paths []string, unrestricted bool) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":     "Patient",
			projectBindKey:        project,
			datasetGenerationBind: generation,
			authPathsBindKey:      paths,
			authUnrestrictedBind:  unrestricted,
			scopeAllowedBind:      true,
		},
	}
}

func pgsq02ScopeRenderer(t *testing.T, plan ir.PhysicalPlan) *planRenderer {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("pgsq02_it_unit")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	r := &planRenderer{storage: binding, parameters: map[string]int{}, rows: map[string]rowScope{}}
	if err := r.loadScope(plan); err != nil {
		t.Fatalf("loadScope: %v", err)
	}
	return r
}

func TestScopeResourcePredicateGoldenRestricted(t *testing.T) {
	r := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "gen-1",
		[]string{"FHIR/Patient", "FHIR/Specimen"}, false))
	typeIndex := r.textParameter("Patient", "text:Patient")

	const want = `r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4`
	if got := r.scopedResourcePredicate("r0", typeIndex); got != want {
		t.Fatalf("restricted resource predicate =\n%s\nwant\n%s", got, want)
	}
	if got := r.scopedResourcePredicate("r0", 0); got != strings.TrimSuffix(want, ` AND r0."resource_type" = $4`) {
		t.Fatalf("type-less resource predicate =\n%s", got)
	}
	wantArgs := []struct {
		Type  string
		Value any
	}{
		{"text", "pgsq02-proj-a"},
		{"text", "gen-1"},
		{"jsonb", json.RawMessage(`["FHIR/Patient","FHIR/Specimen"]`)},
		{"text", "Patient"},
	}
	if len(r.args) != len(wantArgs) {
		t.Fatalf("args = %d, want %d", len(r.args), len(wantArgs))
	}
	for index, wantArg := range wantArgs {
		if string(r.args[index].Type) != wantArg.Type || !reflect.DeepEqual(r.args[index].Value, wantArg.Value) {
			t.Fatalf("arg %d = %#v (%s), want %#v (%s)", index, r.args[index].Value, r.args[index].Type, wantArg.Value, wantArg.Type)
		}
	}
}

func TestScopeResourcePredicateGoldenUnrestricted(t *testing.T) {
	r := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "", nil, true))
	typeIndex := r.textParameter("Patient", "text:Patient")

	const want = `r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."resource_type" = $3`
	got := r.scopedResourcePredicate("r0", typeIndex)
	if got != want {
		t.Fatalf("unrestricted resource predicate =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "auth_resource_path") {
		t.Fatalf("explicit unrestricted mode must add no auth predicate: %s", got)
	}
	if len(r.args) != 3 {
		t.Fatalf("unrestricted scope must bind exactly project, generation, type; args = %#v", r.args)
	}
	for _, arg := range r.args {
		if string(arg.Type) == "jsonb" {
			t.Fatalf("unrestricted scope bound an auth parameter anyway: %#v", r.args)
		}
	}
}

func TestScopeDenyAllKeepsAuthClauseWithEmptyJSONArray(t *testing.T) {
	for name, paths := range map[string][]string{"empty": {}, "nil": nil} {
		r := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "gen-1", paths, false))
		const want = `e1."project" = $1 AND e1."dataset_generation" = $2` +
			` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))`
		if got := r.scopedEdgePredicate("e1"); got != want {
			t.Fatalf("%s deny-all edge predicate =\n%s\nwant\n%s", name, got, want)
		}
		if len(r.args) != 3 {
			t.Fatalf("%s deny-all args = %#v, want project, generation, empty array", name, r.args)
		}
		auth := r.args[2]
		if string(auth.Type) != "jsonb" {
			t.Fatalf("%s deny-all auth type = %s, want jsonb", name, auth.Type)
		}
		if raw, ok := auth.Value.(json.RawMessage); !ok || string(raw) != "[]" {
			t.Fatalf("%s deny-all auth value = %#v, want JSON empty array", name, auth.Value)
		}
	}
}

func TestScopeEdgePredicateGoldenRestrictedAndUnrestricted(t *testing.T) {
	restricted := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient"}, false))
	const wantRestricted = `e1."project" = $1` +
		` AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))`
	if got := restricted.scopedEdgePredicate("e1"); got != wantRestricted {
		t.Fatalf("restricted edge predicate =\n%s\nwant\n%s", got, wantRestricted)
	}
	for _, leaked := range []string{"label", "source_logical_id", "target_logical_id", "resource_type"} {
		if strings.Contains(restricted.scopedEdgePredicate("e1"), leaked) {
			t.Fatalf("canonical edge predicate must not carry traversal payload %q: %s", leaked, wantRestricted)
		}
	}
	unrestricted := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "", nil, true))
	const wantUnrestricted = `e1."project" = $1 AND e1."dataset_generation" = $2`
	if got := unrestricted.scopedEdgePredicate("e1"); got != wantUnrestricted {
		t.Fatalf("unrestricted edge predicate =\n%s\nwant\n%s", got, wantUnrestricted)
	}
}

func pgsq02DeniedPlan(plan ir.PhysicalPlan) ir.PhysicalPlan {
	plan.BindVars[scopeAllowedBind] = false
	return plan
}

func TestScopeAllowedFalseMapsToLiteralZeroRowPredicate(t *testing.T) {
	restricted := pgsq02ScopeRenderer(t, pgsq02DeniedPlan(
		pgsq02ScopePlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient", "FHIR/Specimen"}, false)))
	typeIndex := restricted.textParameter("Patient", "text:Patient")
	const wantDeniedResource = `r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4 AND FALSE`
	if got := restricted.scopedResourcePredicate("r0", typeIndex); got != wantDeniedResource {
		t.Fatalf("denied restricted resource predicate =\n%s\nwant\n%s", got, wantDeniedResource)
	}
	const wantDeniedEdge = `e1."project" = $1` +
		` AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND FALSE`
	if got := restricted.scopedEdgePredicate("e1"); got != wantDeniedEdge {
		t.Fatalf("denied restricted edge predicate =\n%s\nwant\n%s", got, wantDeniedEdge)
	}
	if len(restricted.args) != 4 {
		t.Fatalf("denied scope must bind exactly the allowed scope's parameters; args = %#v", restricted.args)
	}
	for _, arg := range restricted.args {
		if string(arg.Type) == "boolean" {
			t.Fatalf("the deny became a parameter instead of a predicate shape: %#v", restricted.args)
		}
	}
	if raw, ok := restricted.args[2].Value.(json.RawMessage); !ok || string(raw) != `["FHIR/Patient","FHIR/Specimen"]` {
		t.Fatalf("denied auth array = %#v, want the exact authorized set kept", restricted.args[2])
	}

	free := pgsq02ScopeRenderer(t, pgsq02DeniedPlan(pgsq02ScopePlan("pgsq02-proj-a", "", nil, true)))
	freeType := free.textParameter("Patient", "text:Patient")
	const wantDeniedFree = `r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."resource_type" = $3 AND FALSE`
	if got := free.scopedResourcePredicate("r0", freeType); got != wantDeniedFree {
		t.Fatalf("denied unrestricted resource predicate =\n%s\nwant\n%s", got, wantDeniedFree)
	}
	if strings.Contains(free.scopedEdgePredicate("e1"), "auth_resource_path") {
		t.Fatalf("denied unrestricted mode must stay auth-predicate-free: %s", free.scopedEdgePredicate("e1"))
	}

	denyAll := pgsq02ScopeRenderer(t, pgsq02DeniedPlan(pgsq02ScopePlan("pgsq02-proj-a", "gen-1", nil, false)))
	if got := denyAll.scopedEdgePredicate("e1"); got != wantDeniedEdge {
		t.Fatalf("denied deny-all edge predicate =\n%s\nwant\n%s", got, wantDeniedEdge)
	}
	if raw, ok := denyAll.args[2].Value.(json.RawMessage); !ok || string(raw) != "[]" {
		t.Fatalf("denied deny-all auth value = %#v, want JSON empty array kept", denyAll.args[2])
	}

	allowed := pgsq02ScopeRenderer(t, pgsq02ScopePlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient"}, false))
	if strings.Contains(allowed.scopedResourcePredicate("r0", typeIndex), "FALSE") ||
		strings.Contains(allowed.scopedEdgePredicate("e1"), "FALSE") {
		t.Fatalf("allowed scope must not emit a deny token")
	}
}

func TestScopeAllowedFalseIsWiredIntoEveryRowSource(t *testing.T) {
	plan := pgsq02TraversalPlanForScope("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient", "FHIR/Specimen"}, false,
		"Patient", "has-specimen", "Specimen")
	allowed, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("allowed traversal render: %v", err)
	}
	if strings.Contains(allowed.SQL, "FALSE") {
		t.Fatalf("allowed statement carries a deny token: %s", allowed.SQL)
	}
	denied, err := RenderPhysicalPlan(pgsq02DeniedPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("denied traversal render: %v", err)
	}
	if !reflect.DeepEqual(denied.Args, allowed.Args) {
		t.Fatalf("denied args drifted from the allowed render: %#v vs %#v", denied.Args, allowed.Args)
	}
	for _, wantFragment := range []string{
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
			` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND r0."resource_type" = $4 AND FALSE`,
		` WHERE e1."project" = $1 AND e1."dataset_generation" = $2` +
			` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND FALSE AND e1."label" = $5`,
		` ON x1."project" = $1 AND x1."dataset_generation" = $2` +
			` AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND x1."resource_type" = $6 AND FALSE AND x1."logical_id" = e1."target_logical_id"`,
	} {
		if !strings.Contains(denied.SQL, wantFragment) {
			t.Fatalf("denied statement missing %q:\n%s", wantFragment, denied.SQL)
		}
	}
	if count := strings.Count(denied.SQL, " AND FALSE"); count != 3 {
		t.Fatalf("deny conjunct count = %d, want 3 (root, edge, target):\n%s", count, denied.SQL)
	}

	root := pgsq02RootPlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient"}, false, "Patient", "logical_id")
	deniedRoot, err := RenderRootScan(pgsq02DeniedPlan(root), mustBinding(t))
	if err != nil {
		t.Fatalf("denied root scan render: %v", err)
	}
	if !strings.Contains(deniedRoot.SQL, ` WHERE r0."project" = $1 AND r0."dataset_generation" = $2`+
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))`+
		` AND r0."resource_type" = $4 AND FALSE`) {
		t.Fatalf("denied root-scan statement missing its deny conjunct:\n%s", deniedRoot.SQL)
	}
}

func TestScopeLoadRefusesEveryDefectiveBindBeforeBindingAnything(t *testing.T) {
	for _, probe := range []struct {
		desc   string
		mutate func(map[string]any)
	}{
		{"project missing", func(b map[string]any) { delete(b, projectBindKey) }},
		{"project empty", func(b map[string]any) { b[projectBindKey] = "" }},
		{"project blank", func(b map[string]any) { b[projectBindKey] = "   " }},
		{"project not a string", func(b map[string]any) { b[projectBindKey] = 42 }},
		{"generation missing", func(b map[string]any) { delete(b, datasetGenerationBind) }},
		{"generation not a string", func(b map[string]any) { b[datasetGenerationBind] = 7 }},
		{"auth mode missing (no implicit scope)", func(b map[string]any) { delete(b, authUnrestrictedBind) }},
		{"auth mode not a bool", func(b map[string]any) { b[authUnrestrictedBind] = "true" }},
		{"auth paths missing", func(b map[string]any) { delete(b, authPathsBindKey) }},
		{"auth paths not []string", func(b map[string]any) { b[authPathsBindKey] = []any{"FHIR/Patient"} }},
		{"unrestricted carries paths", func(b map[string]any) {
			b[authUnrestrictedBind] = true
			b[authPathsBindKey] = []string{"FHIR/Patient"}
		}},
		{"scope_allowed missing (fifth canonical bind, no implicit allow)", func(b map[string]any) { delete(b, scopeAllowedBind) }},
		{"scope_allowed not a bool", func(b map[string]any) { b[scopeAllowedBind] = "true" }},
	} {
		plan := pgsq02ScopePlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient"}, false)
		probe.mutate(plan.BindVars)
		binding, err := ir.NewPostgresStorageBinding("pgsq02_it_unit")
		if err != nil {
			t.Fatalf("binding: %v", err)
		}
		r := &planRenderer{storage: binding, parameters: map[string]int{}, rows: map[string]rowScope{}}
		err = r.loadScope(plan)
		if !errors.Is(err, ErrInvalidPostgresPlanScope) {
			t.Fatalf("%s: error = %v, want ErrInvalidPostgresPlanScope", probe.desc, err)
		}
		if len(r.args) != 0 {
			t.Fatalf("%s: refused scope still bound %d parameter(s): %#v", probe.desc, len(r.args), r.args)
		}
		if r.scope != (planScope{}) {
			t.Fatalf("%s: refused scope populated %+v", probe.desc, r.scope)
		}
	}
}

func TestScopeRendererEntryRefusesDefectiveScopeWithNoSQL(t *testing.T) {
	plan := pgsq02ScopePlan("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient"}, false)
	delete(plan.BindVars, authUnrestrictedBind)
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	if rendered, err := RenderRootScan(plan, mustBinding(t)); !errors.Is(err, ErrInvalidPostgresPlanScope) {
		t.Fatalf("RenderRootScan defective scope: err = %v, want ErrInvalidPostgresPlanScope", err)
	} else if rendered.SQL != "" || len(rendered.Args) != 0 {
		t.Fatalf("refused plan produced SQL/args: %q %#v", rendered.SQL, rendered.Args)
	}
	if rendered, err := RenderPhysicalPlan(plan, mustBinding(t)); !errors.Is(err, ErrInvalidPostgresPlanScope) {
		t.Fatalf("RenderPhysicalPlan defective scope: err = %v, want ErrInvalidPostgresPlanScope", err)
	} else if rendered.SQL != "" || len(rendered.Args) != 0 {
		t.Fatalf("refused plan produced SQL/args: %q %#v", rendered.SQL, rendered.Args)
	}
}

func TestScopeValuesNeverBecomeSQLTextAndRenderingIsDeterministic(t *testing.T) {
	hostileProject := `pg'; DROP TABLE fhir_resource; -- "$1`
	hostileGeneration := "gen-\"2\"\\$2"
	hostilePaths := []string{`FHIR/*' OR 1=1 --`, `FHIR/"injected"; $3`}

	plan := pgsq02TraversalPlanForScope(hostileProject, hostileGeneration, hostilePaths, false,
		"Patient", "has-specimen", "Specimen")
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	for _, leaked := range append([]string{hostileProject, hostileGeneration}, hostilePaths...) {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("scope value %q leaked into SQL text: %s", leaked, rendered.SQL)
		}
	}
	if len(rendered.Args) < 3 {
		t.Fatalf("scope arguments missing: %#v", rendered.Args)
	}
	if got := rendered.Args[0]; string(got.Type) != "text" || got.Value != hostileProject {
		t.Fatalf("project argument = %#v", got)
	}
	if got := rendered.Args[1]; string(got.Type) != "text" || got.Value != hostileGeneration {
		t.Fatalf("generation argument = %#v", got)
	}
	if got := rendered.Args[2]; string(got.Type) != "jsonb" ||
		string(got.Value.(json.RawMessage)) != `["FHIR/*' OR 1=1 --","FHIR/\"injected\"; $3"]` {
		t.Fatalf("auth argument = %#v", got)
	}

	second, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("second render drifted: %v", err)
	}
}

func TestScopeFragmentsAreWiredIntoAssembledStatements(t *testing.T) {
	plan := pgsq02TraversalPlanForScope("pgsq02-proj-a", "gen-1", []string{"FHIR/Patient", "FHIR/Specimen"}, false,
		"Patient", "has-specimen", "Specimen")
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	for _, wantFragment := range []string{
		` WHERE e1."project" = $1 AND e1."dataset_generation" = $2` +
			` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND e1."label" = $5`,
		` ON x1."project" = $1 AND x1."dataset_generation" = $2` +
			` AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND x1."resource_type" = $6 AND x1."logical_id" = e1."target_logical_id"`,
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
			` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND r0."resource_type" = $4`,
	} {
		if !strings.Contains(rendered.SQL, wantFragment) {
			t.Fatalf("assembled SQL missing canonical fragment %q:\n%s", wantFragment, rendered.SQL)
		}
	}
}

func pgsq02TraversalPlanForScope(project, generation string, paths []string, unrestricted bool,
	rootType, label, targetType string) ir.PhysicalPlan {
	plan := pgsq02ScopePlan(project, generation, paths, unrestricted)
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = label
	plan.BindVars["target_type"] = targetType
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "root_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "target_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
		}}},
	}
	return plan
}

const (
	pgsq02ProjectA     = "pgsq02-proj-a"
	pgsq02ProjectB     = "pgsq02-proj-b"
	pgsq02GenOne       = "gen-1"
	pgsq02GenTwo       = "gen-2"
	pgsq02PathPatient  = "FHIR/Patient"
	pgsq02PathSpecimen = "FHIR/Specimen"
	pgsq02PathConf     = "FHIR/Confidential"
	pgsq02Label        = "has-specimen"
)

var pgsq02ResourceNamePattern = regexp.MustCompile(`^pgsq02_it_[0-9]+_[0-9a-f]+$`)

type pgsq02Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	client *control.Client
	schema string
	fail   func(step string, err error)
	scrub  func(string) string
}

func newPGSQ02Service(t *testing.T) *pgsq02Service {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v string) string {
		s := v
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err.Error()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := "pgsq02_it_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !pgsq02ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err.Error()))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := "pgsq02_it_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !pgsq02ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err.Error()))
		}
	})

	client, err := control.OpenPool(ctx, control.ClientConfig{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	})
	if err != nil {
		fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := control.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	return &pgsq02Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, fail: fail, scrub: scrub}
}

func (s *pgsq02Service) insertResource(t *testing.T, project, generation, resourceType, logicalID, authPath string) int64 {
	t.Helper()
	repo, err := control.NewResourceRepository(s.client, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	seq, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		AuthResourcePath:  authPath,
	})
	if err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
	return seq
}

func (s *pgsq02Service) insertEdge(t *testing.T, project, generation, authPath, sourceType, sourceID, targetType, targetID, sourcePath string, ordinal int64) int64 {
	t.Helper()
	repo, err := control.NewEdgeRepository(s.client, s.schema)
	if err != nil {
		t.Fatalf("edge repository: %v", err)
	}
	seq, err := repo.InsertEdge(s.ctx, control.EdgeOccurrenceRow{
		Project: project, DatasetGeneration: generation, AuthResourcePath: authPath,
		Label: pgsq02Label, SourceResourceType: sourceType, SourceLogicalID: sourceID,
		TargetResourceType: targetType, TargetLogicalID: targetID,
		SourcePath: sourcePath, SourceOrdinal: ordinal,
	})
	if err != nil {
		s.fail("insert edge "+sourceID+"->"+targetID+" @"+project+"/"+generation, err)
	}
	return seq
}

func (s *pgsq02Service) run(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute statement "+sql, err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish statement", err)
	}
	return out
}

func (s *pgsq02Service) runRendered(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return s.run(t, rendered.SQL, args...)
}

func (s *pgsq02Service) runOnTwoDistinctConnections(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) ([][]string, [][]string, int32, int32) {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	read := func(conn *pgxpool.Conn) ([][]string, int32) {
		var pid int32
		if err := conn.QueryRow(s.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			s.fail("read pg_backend_pid", err)
		}
		rows, err := conn.Query(s.ctx, rendered.SQL, args...)
		if err != nil {
			s.fail("execute rendered statement on dedicated connection", err)
		}
		defer rows.Close()
		var out [][]string
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				s.fail("read connection row", err)
			}
			row := make([]string, len(values))
			for index, value := range values {
				row[index] = stringifyCell(value)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			s.fail("finish connection statement", err)
		}
		return out, pid
	}
	first, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire first dedicated connection", err)
	}
	defer first.Release()
	second, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire second dedicated connection", err)
	}
	defer second.Release()
	firstRows, firstPID := read(first)
	secondRows, secondPID := read(second)
	return firstRows, secondRows, firstPID, secondPID
}

func (s *pgsq02Service) expectRefused(t *testing.T, sql string, wantCode, wantConstraint string, args ...any) {
	t.Helper()
	_, err := s.iso.Exec(s.ctx, sql, args...)
	if err == nil {
		t.Fatalf("statement was accepted, want refusal by %s: %s", wantConstraint, sql)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		s.fail("expect pg error for "+sql, err)
	}
	if pgErr.Code != wantCode || pgErr.ConstraintName != wantConstraint {
		s.fail("statement refused by ("+pgErr.Code+","+pgErr.ConstraintName+
			") want ("+wantCode+","+wantConstraint+")", err)
	}
}

func pgsq02RootPlan(project, generation string, paths []string, unrestricted bool, resourceType string, columns ...string) ir.PhysicalPlan {
	projections := make([]ir.PhysicalProjection, 0, len(columns))
	for _, column := range columns {
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{column}},
		})
	}
	plan := pgsq02ScopePlan(project, generation, paths, unrestricted)
	plan.BindVars["root_collection"] = resourceType
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
	}
	return plan
}

func TestPGSQ02SameResourceIDTwoProjectsGenerationsReturnsAuthorizedTuple(t *testing.T) {
	svc := newPGSQ02Service(t)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Patient", "p-shared", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectB, pgsq02GenOne, "Patient", "p-shared", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenTwo, "Patient", "p-shared", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectB, pgsq02GenTwo, "Patient", "p-shared", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Observation", "p-shared", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Patient", "p-hidden", pgsq02PathConf)

	plan := pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, []string{pgsq02PathPatient}, false,
		"Patient", "project", "dataset_generation", "resource_type", "logical_id", "auth_resource_path")
	rendered, err := RenderRootScan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderRootScan: %v", err)
	}

	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq02ProjectA},
		{Type: command.PostgreSQLParameterText, Value: pgsq02GenOne},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", rendered.Args, wantArgs)
	}

	want := [][]string{{pgsq02ProjectA, pgsq02GenOne, "Patient", "p-shared", pgsq02PathPatient}}
	if got := svc.runRendered(t, rendered); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped scan rows = %v, want exactly the authorized tuple %v", got, want)
	}

	witness := svc.run(t,
		`SELECT project, dataset_generation, resource_type, logical_id, auth_resource_path`+
			` FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project = 'pgsq02-proj-a' AND dataset_generation = 'gen-1'`+
			` AND auth_resource_path = 'FHIR/Patient' AND resource_type = 'Patient'`+
			` ORDER BY resource_seq`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness rows = %v, want %v", witness, want)
	}

	for _, probe := range []struct {
		desc, sql string
		args      []any
	}{
		{"project decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-shared'`, []any{pgsq02ProjectB, pgsq02GenOne}},
		{"generation decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-shared'`, []any{pgsq02ProjectA, pgsq02GenTwo}},
		{"project+generation decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-shared'`, []any{pgsq02ProjectB, pgsq02GenTwo}},
		{"type decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Observation' AND logical_id='p-shared'`, []any{pgsq02ProjectA, pgsq02GenOne}},
		{"auth decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-hidden'`, []any{pgsq02ProjectA, pgsq02GenOne}},
	} {
		rows := svc.run(t, probe.sql, probe.args...)
		if len(rows) != 1 || rows[0][0] != "1" {
			t.Fatalf("%s must exist: %v", probe.desc, rows)
		}
	}

	type probe struct {
		desc     string
		sql      string
		args     []any
		wantRows [][]string
	}
	base := `SELECT logical_id FROM "` + svc.schema + `".fhir_resource WHERE `
	const orderTail = ` ORDER BY resource_seq`
	for _, p := range []probe{
		{"full chain (control)", base +
			`project=$1 AND dataset_generation=$2 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND resource_type=$4` + orderTail,
			[]any{pgsq02ProjectA, pgsq02GenOne, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-shared"}}},
		{"without project", base +
			`dataset_generation=$1 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($2::jsonb)) AND resource_type=$3` + orderTail,
			[]any{pgsq02GenOne, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-shared"}, {"p-shared"}}},
		{"without generation", base +
			`project=$1 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($2::jsonb)) AND resource_type=$3` + orderTail,
			[]any{pgsq02ProjectA, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-shared"}, {"p-shared"}}},
		{"without auth", base +
			`project=$1 AND dataset_generation=$2 AND resource_type=$3` + orderTail,
			[]any{pgsq02ProjectA, pgsq02GenOne, "Patient"},
			[][]string{{"p-shared"}, {"p-hidden"}}},
		{"without resource type", base +
			`project=$1 AND dataset_generation=$2 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` + orderTail,
			[]any{pgsq02ProjectA, pgsq02GenOne, json.RawMessage(`["FHIR/Patient"]`)},
			[][]string{{"p-shared"}, {"p-shared"}}},
	} {
		rows := svc.run(t, p.sql, p.args...)
		if !reflect.DeepEqual(rows, p.wantRows) {
			t.Fatalf("guard-drop %s rows = %v, want %v", p.desc, rows, p.wantRows)
		}
	}

	duplicateInsert := `INSERT INTO "` + svc.schema + `".fhir_resource (project, dataset_generation, resource_type, logical_id, auth_resource_path) VALUES ($1,$2,$3,$4,$5)`
	svc.expectRefused(t, duplicateInsert, "23505", "fhir_resource_natural_identity_ux",
		pgsq02ProjectA, pgsq02GenOne, "Patient", "p-shared", pgsq02PathPatient)
	svc.expectRefused(t, duplicateInsert, "23514", "fhir_resource_project_present",
		"", pgsq02GenOne, "Patient", "ck-project", pgsq02PathPatient)
}

func TestPGSQ02RestrictedPathsExactAndDenyAllZeroRowsRootAndEdge(t *testing.T) {
	svc := newPGSQ02Service(t)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Patient", "sp-1", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Specimen", "st-allow", pgsq02PathSpecimen)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Specimen", "st-hidden", pgsq02PathConf)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Specimen", "st-off", pgsq02PathSpecimen)
	svc.insertEdge(t, pgsq02ProjectA, pgsq02GenOne, pgsq02PathPatient, "Patient", "sp-1", "Specimen", "st-allow", "specimen[0]", 0)
	svc.insertEdge(t, pgsq02ProjectA, pgsq02GenOne, pgsq02PathPatient, "Patient", "sp-1", "Specimen", "st-hidden", "specimen[1]", 0)
	svc.insertEdge(t, pgsq02ProjectA, pgsq02GenOne, pgsq02PathConf, "Patient", "sp-1", "Specimen", "st-off", "specimen[2]", 0)

	binding := bindingFor(t, svc.schema)
	allowed := []string{pgsq02PathPatient, pgsq02PathSpecimen}

	patients, err := RenderRootScan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, allowed, false, "Patient", "logical_id"), binding)
	if err != nil {
		t.Fatalf("restricted patient scan: %v", err)
	}
	if got := svc.runRendered(t, patients); !reflect.DeepEqual(got, [][]string{{"sp-1"}}) {
		t.Fatalf("restricted patient rows = %v, want exactly [sp-1]", got)
	}
	specimens, err := RenderRootScan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, allowed, false, "Specimen", "logical_id"), binding)
	if err != nil {
		t.Fatalf("restricted specimen scan: %v", err)
	}
	wantVisible := [][]string{{"st-allow"}, {"st-off"}}
	if got := svc.runRendered(t, specimens); !reflect.DeepEqual(got, wantVisible) {
		t.Fatalf("restricted specimen rows = %v, want exactly %v", got, wantVisible)
	}

	unrestricted, err := RenderRootScan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, nil, true, "Specimen", "logical_id"), binding)
	if err != nil {
		t.Fatalf("unrestricted specimen scan: %v", err)
	}
	if got := svc.runRendered(t, unrestricted); !reflect.DeepEqual(got, [][]string{{"st-allow"}, {"st-hidden"}, {"st-off"}}) {
		t.Fatalf("unrestricted specimen rows = %v, want every stored path", got)
	}

	for _, resourceType := range []string{"Patient", "Specimen"} {
		denyAll, err := RenderRootScan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, []string{}, false, resourceType, "logical_id"), binding)
		if err != nil {
			t.Fatalf("deny-all %s scan: %v", resourceType, err)
		}
		if raw, ok := denyAll.Args[2].Value.(json.RawMessage); !ok || string(raw) != "[]" ||
			denyAll.Args[2].Type != command.PostgreSQLParameterJSONB {
			t.Fatalf("deny-all auth argument = %#v, want JSON empty array", denyAll.Args[2])
		}
		if got := svc.runRendered(t, denyAll); len(got) != 0 {
			t.Fatalf("deny-all %s scan read %v, want zero rows", resourceType, got)
		}
		stored := svc.run(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type=$3`,
			pgsq02ProjectA, pgsq02GenOne, resourceType)
		if len(stored) != 1 || stored[0][0] == "0" {
			t.Fatalf("deny-all %s witness must show stored rows: %v", resourceType, stored)
		}
	}

	plan := pgsq02TraversalPlanForScope(pgsq02ProjectA, pgsq02GenOne, allowed, false, "Patient", pgsq02Label, "Specimen")
	rendered, err := RenderPhysicalPlan(plan, binding)
	if err != nil {
		t.Fatalf("restricted traversal: %v", err)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq02ProjectA},
		{Type: command.PostgreSQLParameterText, Value: pgsq02GenOne},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Specimen"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: pgsq02Label},
		{Type: command.PostgreSQLParameterText, Value: "Specimen"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("traversal args = %#v, want %#v", rendered.Args, wantArgs)
	}
	wantRoute := [][]string{{"sp-1", "st-allow"}}
	if got := svc.runRendered(t, rendered); !reflect.DeepEqual(got, wantRoute) {
		t.Fatalf("restricted traversal rows = %v, want exactly %v", got, wantRoute)
	}

	denyEdge := pgsq02TraversalPlanForScope(pgsq02ProjectA, pgsq02GenOne, []string{}, false, "Patient", pgsq02Label, "Specimen")
	denyRendered, err := RenderPhysicalPlan(denyEdge, binding)
	if err != nil {
		t.Fatalf("deny-all traversal: %v", err)
	}
	if raw, ok := denyRendered.Args[2].Value.(json.RawMessage); !ok || string(raw) != "[]" {
		t.Fatalf("deny-all traversal auth = %#v, want JSON empty array", denyRendered.Args[2])
	}
	if got := svc.runRendered(t, denyRendered); len(got) != 0 {
		t.Fatalf("deny-all traversal read %v, want zero rows", got)
	}
	storedEdges := svc.run(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_edge_occurrence WHERE project=$1 AND dataset_generation=$2 AND label=$3`,
		pgsq02ProjectA, pgsq02GenOne, pgsq02Label)
	if len(storedEdges) != 1 || storedEdges[0][0] != "3" {
		t.Fatalf("edge witness must show all three occurrences stored: %v", storedEdges)
	}

	edgeJoin := `SELECT r0.logical_id, r1.logical_id FROM "` + svc.schema + `".fhir_resource r0` +
		` JOIN "` + svc.schema + `".fhir_edge_occurrence e ON `
	edgeScope := `e.project=$1 AND e.dataset_generation=$2 AND e.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e.label=$5 AND e.source_resource_type=$4 AND e.source_logical_id=r0.logical_id AND e.target_resource_type=$6`
	targetJoin := ` JOIN "` + svc.schema + `".fhir_resource r1 ON r1.project=$1 AND r1.dataset_generation=$2` +
		` AND r1.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r1.resource_type=$6 AND r1.logical_id=e.target_logical_id`
	where := ` WHERE r0.project=$1 AND r0.dataset_generation=$2` +
		` AND r0.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0.resource_type=$4`
	order := ` ORDER BY r0.resource_seq, e.edge_seq`
	scopeArgs := []any{pgsq02ProjectA, pgsq02GenOne, json.RawMessage(`["FHIR/Patient","FHIR/Specimen"]`), "Patient", pgsq02Label, "Specimen"}
	for _, p := range []struct {
		desc, sql string
		wantRows  [][]string
	}{
		{"full chain (control)", edgeJoin + edgeScope + targetJoin + where + order, wantRoute},
		{"without edge auth", edgeJoin +
			`e.project=$1 AND e.dataset_generation=$2 AND e.label=$5 AND e.source_resource_type=$4` +
			` AND e.source_logical_id=r0.logical_id AND e.target_resource_type=$6` + targetJoin + where + order,
			[][]string{{"sp-1", "st-allow"}, {"sp-1", "st-off"}}},
		{"without target auth", edgeJoin + edgeScope +
			` JOIN "` + svc.schema + `".fhir_resource r1 ON r1.project=$1 AND r1.dataset_generation=$2` +
			` AND r1.resource_type=$6 AND r1.logical_id=e.target_logical_id` + where + order,
			[][]string{{"sp-1", "st-allow"}, {"sp-1", "st-hidden"}}},
	} {
		rows := svc.run(t, p.sql, scopeArgs...)
		if !reflect.DeepEqual(rows, p.wantRows) {
			t.Fatalf("guard-drop %s rows = %v, want %v", p.desc, rows, p.wantRows)
		}
	}

	first, second, pid1, pid2 := svc.runOnTwoDistinctConnections(t, rendered)
	if pid1 == pid2 {
		t.Fatalf("the two connections were the same server process: pg_backend_pid %d", pid1)
	}
	if !reflect.DeepEqual(first, wantRoute) || !reflect.DeepEqual(second, wantRoute) {
		t.Fatalf("connection-scoped rows diverged: %v vs %v vs want %v", first, second, wantRoute)
	}
	denyFirst, denySecond, denyPID1, denyPID2 := svc.runOnTwoDistinctConnections(t, denyRendered)
	if denyPID1 == denyPID2 {
		t.Fatalf("the two deny-all connections were the same server process: pg_backend_pid %d", denyPID1)
	}
	if len(denyFirst) != 0 || len(denySecond) != 0 {
		t.Fatalf("deny-all read rows on a dedicated connection: %v / %v", denyFirst, denySecond)
	}

	duplicateEdge := `INSERT INTO "` + svc.schema + `".fhir_edge_occurrence (project, dataset_generation, auth_resource_path, label,` +
		` source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal)` +
		` VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	svc.expectRefused(t, duplicateEdge, "23505", "fhir_edge_occurrence_identity_ux",
		pgsq02ProjectA, pgsq02GenOne, pgsq02PathPatient, pgsq02Label, "Patient", "sp-1", "Specimen", "st-allow", "specimen[0]", int64(0))
	svc.expectRefused(t, duplicateEdge, "23514", "fhir_edge_occurrence_project_present",
		"", pgsq02GenOne, pgsq02PathPatient, pgsq02Label, "Patient", "sp-1", "Specimen", "st-allow", "specimen[1]", int64(0))
}

func TestPGSQ02EdgeIdentityAcrossScopesStaysIsolated(t *testing.T) {
	svc := newPGSQ02Service(t)
	type scope struct{ project, generation string }
	scopes := []scope{
		{pgsq02ProjectA, pgsq02GenOne},
		{pgsq02ProjectB, pgsq02GenOne},
		{pgsq02ProjectA, pgsq02GenTwo},
		{pgsq02ProjectB, pgsq02GenTwo},
	}
	for _, s := range scopes {
		svc.insertResource(t, s.project, s.generation, "Patient", "p-src", pgsq02PathPatient)
		svc.insertResource(t, s.project, s.generation, "Specimen", "p-tgt", pgsq02PathSpecimen)
	}
	for _, s := range scopes {
		svc.insertEdge(t, s.project, s.generation, pgsq02PathPatient, "Patient", "p-src", "Specimen", "p-tgt", "specimen[0]", 0)
	}

	for _, pair := range []struct {
		sql    string
		args   []any
		wanted string
	}{
		{`SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE resource_type='Patient' AND logical_id='p-src'`, nil, "4"},
		{`SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE resource_type='Specimen' AND logical_id='p-tgt'`, nil, "4"},
		{`SELECT count(*) FROM "` + svc.schema + `".fhir_edge_occurrence WHERE label=$1 AND source_logical_id='p-src' AND target_logical_id='p-tgt'`, []any{pgsq02Label}, "4"},
	} {
		rows := svc.run(t, pair.sql, pair.args...)
		if len(rows) != 1 || rows[0][0] != pair.wanted {
			t.Fatalf("witness %s = %v, want %s stored", pair.sql, rows, pair.wanted)
		}
	}

	plan := pgsq02TraversalPlanForScope(pgsq02ProjectA, pgsq02GenOne,
		[]string{pgsq02PathPatient, pgsq02PathSpecimen}, false, "Patient", pgsq02Label, "Specimen")
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq02ProjectA},
		{Type: command.PostgreSQLParameterText, Value: pgsq02GenOne},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Specimen"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: pgsq02Label},
		{Type: command.PostgreSQLParameterText, Value: "Specimen"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", rendered.Args, wantArgs)
	}
	want := [][]string{{"p-src", "p-tgt"}}
	if got := svc.runRendered(t, rendered); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped traversal over four live scopes = %v, want exactly the authorized occurrence %v", got, want)
	}

	edgeJoin := `SELECT r0.logical_id, r0.project, r1.project, r1.logical_id FROM "` + svc.schema + `".fhir_resource r0` +
		` JOIN "` + svc.schema + `".fhir_edge_occurrence e ON `
	targetPrefix := ` JOIN "` + svc.schema + `".fhir_resource r1 ON `
	rootWhere := ` WHERE r0.project=$1 AND r0.dataset_generation=$2` +
		` AND r0.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0.resource_type=$4`
	order := ` ORDER BY r0.resource_seq, e.edge_seq, r1.resource_seq`
	scopeArgs := []any{pgsq02ProjectA, pgsq02GenOne, json.RawMessage(`["FHIR/Patient","FHIR/Specimen"]`), "Patient", pgsq02Label, "Specimen"}
	edgeFull := `e.project=$1 AND e.dataset_generation=$2 AND e.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e.label=$5 AND e.source_resource_type=$4 AND e.source_logical_id=r0.logical_id AND e.target_resource_type=$6`
	targetFull := `r1.project=$1 AND r1.dataset_generation=$2 AND r1.auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r1.resource_type=$6 AND r1.logical_id=e.target_logical_id`
	authorizedRow := []string{"p-src", pgsq02ProjectA, pgsq02ProjectA, "p-tgt"}
	for _, p := range []struct {
		desc, sql string
		wantRows  [][]string
	}{
		{"full chain (control)", edgeJoin + edgeFull + targetPrefix + targetFull + rootWhere + order,
			[][]string{authorizedRow}},
		{"without edge project", edgeJoin +
			strings.Replace(edgeFull, `e.project=$1 AND `, "", 1) + targetPrefix + targetFull + rootWhere + order,
			[][]string{authorizedRow, authorizedRow}},
		{"without edge dataset_generation", edgeJoin +
			strings.Replace(edgeFull, `e.dataset_generation=$2 AND `, "", 1) + targetPrefix + targetFull + rootWhere + order,
			[][]string{authorizedRow, authorizedRow}},
		{"without root project", edgeJoin + edgeFull + targetPrefix + targetFull +
			strings.Replace(rootWhere, `r0.project=$1 AND `, "", 1) + order,
			[][]string{authorizedRow, {"p-src", pgsq02ProjectB, pgsq02ProjectA, "p-tgt"}}},
		{"without target project", edgeJoin + edgeFull + targetPrefix +
			strings.Replace(targetFull, `r1.project=$1 AND `, "", 1) + rootWhere + order,
			[][]string{authorizedRow, {"p-src", pgsq02ProjectA, pgsq02ProjectB, "p-tgt"}}},
	} {
		rows := svc.run(t, p.sql, scopeArgs...)
		if !reflect.DeepEqual(rows, p.wantRows) {
			t.Fatalf("guard-drop %s rows = %v, want %v", p.desc, rows, p.wantRows)
		}
	}

	duplicateEdge := `INSERT INTO "` + svc.schema + `".fhir_edge_occurrence (project, dataset_generation, auth_resource_path, label,` +
		` source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal)` +
		` VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	svc.expectRefused(t, duplicateEdge, "23505", "fhir_edge_occurrence_identity_ux",
		pgsq02ProjectA, pgsq02GenOne, pgsq02PathPatient, pgsq02Label, "Patient", "p-src", "Specimen", "p-tgt", "specimen[0]", int64(0))
	svc.expectRefused(t, duplicateEdge, "23514", "fhir_edge_occurrence_project_present",
		"", pgsq02GenOne, pgsq02PathPatient, pgsq02Label, "Patient", "p-src", "Specimen", "p-tgt", "specimen[9]", int64(0))
	duplicateResource := `INSERT INTO "` + svc.schema + `".fhir_resource (project, dataset_generation, resource_type, logical_id, auth_resource_path) VALUES ($1,$2,$3,$4,$5)`
	svc.expectRefused(t, duplicateResource, "23514", "fhir_resource_project_present",
		"", pgsq02GenOne, "Patient", "ck-project", pgsq02PathPatient)
}

func TestPGSQ02ScopeAllowedFalseReadsZeroRowsRootAndEdge(t *testing.T) {
	svc := newPGSQ02Service(t)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Patient", "sp-1", pgsq02PathPatient)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Specimen", "st-allow", pgsq02PathSpecimen)
	svc.insertResource(t, pgsq02ProjectA, pgsq02GenOne, "Specimen", "st-hidden", pgsq02PathConf)
	svc.insertEdge(t, pgsq02ProjectA, pgsq02GenOne, pgsq02PathPatient, "Patient", "sp-1", "Specimen", "st-allow", "specimen[0]", 0)

	binding := bindingFor(t, svc.schema)
	authorized := []string{pgsq02PathPatient, pgsq02PathSpecimen}

	rootAllowed, err := RenderRootScan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, authorized, false,
		"Patient", "logical_id"), binding)
	if err != nil {
		t.Fatalf("allowed root scan: %v", err)
	}
	if got := svc.runRendered(t, rootAllowed); !reflect.DeepEqual(got, [][]string{{"sp-1"}}) {
		t.Fatalf("allowed root scan rows = %v, want [sp-1]", got)
	}
	edgeAllowed, err := RenderPhysicalPlan(
		pgsq02TraversalPlanForScope(pgsq02ProjectA, pgsq02GenOne, authorized, false, "Patient", pgsq02Label, "Specimen"),
		binding)
	if err != nil {
		t.Fatalf("allowed traversal: %v", err)
	}
	wantRoute := [][]string{{"sp-1", "st-allow"}}
	if got := svc.runRendered(t, edgeAllowed); !reflect.DeepEqual(got, wantRoute) {
		t.Fatalf("allowed traversal rows = %v, want %v", got, wantRoute)
	}

	deniedRoot, err := RenderRootScan(pgsq02DeniedPlan(pgsq02RootPlan(pgsq02ProjectA, pgsq02GenOne, authorized, false,
		"Patient", "logical_id")), binding)
	if err != nil {
		t.Fatalf("denied root scan: %v", err)
	}
	if !reflect.DeepEqual(deniedRoot.Args, rootAllowed.Args) {
		t.Fatalf("denied root args drifted: %#v vs %#v", deniedRoot.Args, rootAllowed.Args)
	}
	if got := svc.runRendered(t, deniedRoot); len(got) != 0 {
		t.Fatalf("denied root scan read %v, want zero rows", got)
	}

	deniedEdge, err := RenderPhysicalPlan(pgsq02DeniedPlan(
		pgsq02TraversalPlanForScope(pgsq02ProjectA, pgsq02GenOne, authorized, false, "Patient", pgsq02Label, "Specimen")),
		binding)
	if err != nil {
		t.Fatalf("denied traversal: %v", err)
	}
	if !reflect.DeepEqual(deniedEdge.Args, edgeAllowed.Args) {
		t.Fatalf("denied traversal args drifted: %#v vs %#v", deniedEdge.Args, edgeAllowed.Args)
	}
	if got := svc.runRendered(t, deniedEdge); len(got) != 0 {
		t.Fatalf("denied traversal read %v, want zero rows", got)
	}

	for _, probe := range []struct {
		desc, sql string
		args      []any
		wanted    string
	}{
		{"root row", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='sp-1'`,
			[]any{pgsq02ProjectA, pgsq02GenOne}, "1"},
		{"target row", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Specimen' AND logical_id='st-allow'`,
			[]any{pgsq02ProjectA, pgsq02GenOne}, "1"},
		{"auth decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Specimen' AND logical_id='st-hidden'`,
			[]any{pgsq02ProjectA, pgsq02GenOne}, "1"},
		{"edge occurrence", `SELECT count(*) FROM "` + svc.schema + `".fhir_edge_occurrence WHERE project=$1 AND dataset_generation=$2 AND label=$3`,
			[]any{pgsq02ProjectA, pgsq02GenOne, pgsq02Label}, "1"},
	} {
		rows := svc.run(t, probe.sql, probe.args...)
		if len(rows) != 1 || rows[0][0] != probe.wanted {
			t.Fatalf("%s must be stored (%s): %v", probe.desc, probe.wanted, rows)
		}
	}

	first, second, pid1, pid2 := svc.runOnTwoDistinctConnections(t, deniedEdge)
	if pid1 == pid2 {
		t.Fatalf("the two deny connections were the same server process: pg_backend_pid %d", pid1)
	}
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("denied traversal read rows on dedicated connections: %v / %v", first, second)
	}
}

func TestZZNoLeftoverPgSQ02Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgsq02_it_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d pgsq02_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgsq02_it_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d pgsq02_it_* schemas still exist in the service database", schemas)
	}
}
