package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// ---------------------------------------------------------------------------
// pg-sql-03: the canonical missing/null/empty mapping in null_semantics.go,
// pinned offline as golden SQL and ordered-argument literals. The live
// behavior matrix that executes every cell of the contract against real
// PostgreSQL 18 lives in selectors_test.go.
//
// These tests drive RenderPhysicalPlan directly (the shared read spine) so
// the mappings are pinned independently of the closed-slice gate; the same
// builders back RenderSelectorExtraction, which delegates here.
// ---------------------------------------------------------------------------

const (
	pgsq03Project = "pgsq03-proj"
	pgsq03Other   = "pgsq03-other"
	pgsq03Gen     = "gen-1"
	pgsq03Path    = "FHIR/Patient"
)

// pgsq03DocumentSource is the exact selector source shape lowering emits for
// a root-document selector (internal/dataframe/compiler/lower/
// root_projection.go: lowerSemanticFieldProjection anchors the extract at
// PhysicalValue{Variable: "root", Path: ["payload"]}).
func pgsq03DocumentSource() ir.PhysicalValue {
	return ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}
}

// pgsq03SelectorPlan builds the closed extraction plan shape: one direct
// ROOT_SCAN over resourceType plus a RETURN of the given projections, with
// the canonical scope binds (restricted auth mode, scope_allowed true).
func pgsq03SelectorPlan(resourceType string, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  resourceType,
			"project":                          pgsq03Project,
			"dataset_generation":               pgsq03Gen,
			"auth_resource_paths":              []string{pgsq03Path},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

// pgsq03ExtractProjection mirrors the EXTRACT projection construction in
// lower/root_projection.go: scalar-cardinality extract expression over the
// root document with its schema resource type and presence metadata.
func pgsq03ExtractProjection(t *testing.T, name, resourceType, selectorPath string,
	behavior ir.PhysicalNullBehavior, mode ir.PhysicalSelectorExecutionMode) ir.PhysicalProjection {
	t.Helper()
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", selectorPath, err)
	}
	source := pgsq03DocumentSource()
	return ir.PhysicalProjection{
		Name: name,
		Expression: &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: behavior,
			Extract: &ir.PhysicalExtract{
				Source:        source,
				ResourceType:  resourceType,
				Selector:      selector,
				ExecutionMode: mode,
			},
		},
		// The presence metadata form lowering attaches to every semantic
		// field projection (lower/root_projection.go
		// physicalProjectionPresence).
		Presence: &ir.PhysicalProjectionPresence{
			Source: source,
			Paths:  [][]string{pgsq03SelectorPath(t, selectorPath)},
		},
	}
}

func pgsq03SelectorPath(t *testing.T, selectorPath string) []string {
	t.Helper()
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", selectorPath, err)
	}
	path := make([]string, 0, len(selector.Steps))
	for _, step := range selector.Steps {
		path = append(path, step.Field)
	}
	return path
}

// pgsq03PresenceSidecar is the PresenceOutput form: a boolean proof of one
// selector path, emitted as its own positional projection.
func pgsq03PresenceSidecar(name string, path ...string) ir.PhysicalProjection {
	return ir.PhysicalProjection{
		Name: name,
		Presence: &ir.PhysicalProjectionPresence{
			Source: pgsq03DocumentSource(),
			Paths:  [][]string{path},
		},
		PresenceOutput: true,
	}
}

func pgsq03RenderOK(t *testing.T, plan ir.PhysicalPlan) compiler.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderSelectorExtraction(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderSelectorExtraction: %v", err)
	}
	return rendered
}

// ---------------------------------------------------------------------------
// Golden value expressions: one exact statement per registered null
// behavior, with the full ordered-argument list.
// ---------------------------------------------------------------------------

// pgsq03GoldenTail is the canonical scope-wrapped statement frame the
// shared read spine assembles (scope.go predicates + deterministic order),
// so each golden below pins only its projection list.
const pgsq03GoldenTail = ` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
	` WHERE r0."project" = $1` +
	` AND r0."dataset_generation" = $2` +
	` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
	` AND r0."resource_type" = $4` +
	` ORDER BY r0."resource_seq" ASC`

func pgsq03GoldenSQL(selects string) string {
	return `SELECT ` + selects + pgsq03GoldenTail
}

func pgsq03ScopeArgs() []compiler.PostgreSQLParameter {
	return []compiler.PostgreSQLParameter{
		{Type: compiler.PostgreSQLParameterText, Value: pgsq03Project},
		{Type: compiler.PostgreSQLParameterText, Value: pgsq03Gen},
		{Type: compiler.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: compiler.PostgreSQLParameterText, Value: "Patient"},
	}
}

func TestNullSemanticsGoldenPreserveNullValueAndPresence(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalPreserveNull, ir.PhysicalSelectorDirectScalar),
		pgsq03PresenceSidecar("gender_present", "gender"),
	)
	rendered := pgsq03RenderOK(t, plan)

	wantSQL := pgsq03GoldenSQL(`r0."logical_id" AS "c0", (r0."payload" #> $5::text[]) AS "c1", ` +
		`COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $6), false) AS "c2"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("preserve-null SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(pgsq03ScopeArgs(),
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "{gender}"},
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "gender"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("preserve-null args = %#v, want %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"logical_id", "gender", "gender_present"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
	// No path or scope data in the statement text; no AQL bind syntax.
	for _, forbidden := range []string{"@", "gender", "Patient", pgsq03Project, pgsq03Gen, pgsq03Path} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("SQL leaked %q: %s", forbidden, rendered.SQL)
		}
	}
}

func TestNullSemanticsGoldenOmitNullsCollapsesOnlyJSONNull(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalOmitNulls, ir.PhysicalSelectorGeneric))
	rendered := pgsq03RenderOK(t, plan)

	wantSQL := pgsq03GoldenSQL(`NULLIF((r0."payload" #> $5::text[]), 'null'::jsonb) AS "c0"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("omit-nulls SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(pgsq03ScopeArgs(),
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "{gender}"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("omit-nulls args = %#v", rendered.Args)
	}
}

func TestNullSemanticsGoldenEmptyOnNullCoercesOnlyNullStates(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalEmptyOnNull, ""))
	rendered := pgsq03RenderOK(t, plan)

	wantSQL := pgsq03GoldenSQL(`COALESCE(NULLIF((r0."payload" #> $5::text[]), 'null'::jsonb), '""'::jsonb) AS "c0"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("empty-on-null SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	if _, err := json.Marshal(rendered.Args); err != nil {
		t.Fatalf("args not encodable: %v", err)
	}
}

func TestNullSemanticsGoldenNestedPathBindsPrefixAndTerminalKey(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "meta_source", "Patient", "meta.source", ir.PhysicalPreserveNull, ir.PhysicalSelectorDirectScalar),
		pgsq03PresenceSidecar("meta_source_present", "meta", "source"),
	)
	rendered := pgsq03RenderOK(t, plan)

	// The value binds the whole path; the presence proof binds the parent
	// prefix and the terminal key separately — no segment is ever SQL text,
	// and the prefix parameter is shared by the two presence operands.
	wantSQL := pgsq03GoldenSQL(`(r0."payload" #> $5::text[]) AS "c0", ` +
		`COALESCE(jsonb_typeof(r0."payload" #> $6::text[]) = 'object' AND jsonb_exists(r0."payload" #> $6::text[], $7), false) AS "c1"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("nested SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(pgsq03ScopeArgs(),
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "{meta,source}"},
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "{meta}"},
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "source"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("nested args = %#v, want %#v", rendered.Args, wantArgs)
	}
	// Segments never reach SQL text. The projection list (everything before
	// FROM) must not contain the bound path words as text — "resource"
	// legitimately shares letters with "source", so the check is scoped to
	// the emitted projections.
	projections := rendered.SQL[:strings.Index(rendered.SQL, " FROM ")]
	for _, forbidden := range []string{"meta", "source"} {
		if strings.Contains(projections, forbidden) {
			t.Fatalf("path segment %q leaked into SQL projections: %s", forbidden, projections)
		}
	}
}

// ---------------------------------------------------------------------------
// Contract invariants that the goldens alone would not catch.
// ---------------------------------------------------------------------------

func TestNullSemanticsPresenceIsTotalNeverNull(t *testing.T) {
	// The presence bit is COALESCE(..., false): three-valued logic is
	// admitted in the value column only, never in the presence metadata
	// itself.
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("gender_present", "gender"),
	)
	sql := pgsq03RenderOK(t, plan).SQL
	if !strings.Contains(sql, `COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $6), false)`) {
		t.Fatalf("presence proof must be a COALESCE-total two-valued predicate:\n%s", sql)
	}
	// Drop-probe (offline counterpart of the live guard-drop): without the
	// COALESCE the same predicate is NULL for an absent document, which is
	// exactly the collapse the contract forbids.
	unguarded := strings.Replace(sql, `COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $6), false)`,
		`jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $6)`, 1)
	if unguarded == sql {
		t.Fatalf("unguarded drop-probe did not change the statement")
	}
	if strings.Contains(unguarded, ", false)") {
		t.Fatalf("drop probe still guarded")
	}
}

func TestNullSemanticsBehaviorFormsAreDistinctNotDecorated(t *testing.T) {
	forms := map[ir.PhysicalNullBehavior]string{}
	for _, behavior := range []ir.PhysicalNullBehavior{
		ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull,
	} {
		plan := pgsq03SelectorPlan("Patient",
			pgsq03ExtractProjection(t, "gender", "Patient", "gender", behavior, ir.PhysicalSelectorGeneric))
		forms[behavior] = pgsq03RenderOK(t, plan).SQL
	}
	if forms[ir.PhysicalPreserveNull] == forms[ir.PhysicalOmitNulls] ||
		forms[ir.PhysicalOmitNulls] == forms[ir.PhysicalEmptyOnNull] ||
		forms[ir.PhysicalPreserveNull] == forms[ir.PhysicalEmptyOnNull] {
		t.Fatalf("null behaviors rendered identically: %v", forms)
	}
	if strings.Contains(forms[ir.PhysicalPreserveNull], "NULLIF") {
		t.Fatalf("preserve-null must never coerce the stored JSON null:\n%s", forms[ir.PhysicalPreserveNull])
	}
	if strings.Contains(forms[ir.PhysicalOmitNulls], "COALESCE") {
		t.Fatalf("omit-nulls must not fabricate an empty value:\n%s", forms[ir.PhysicalOmitNulls])
	}
	if !strings.Contains(forms[ir.PhysicalEmptyOnNull], "NULLIF") || !strings.Contains(forms[ir.PhysicalEmptyOnNull], "COALESCE") {
		t.Fatalf("empty-on-null must coerce both JSON null and SQL NULL:\n%s", forms[ir.PhysicalEmptyOnNull])
	}
}

func TestNullSemanticsSamePathSharesOneParameter(t *testing.T) {
	// Identical document paths across value and presence operands dedupe
	// into one bound parameter — determinism and the single path-binding
	// authority in documentPathExpression.
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "a", "Patient", "meta.source", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric),
		pgsq03ExtractProjection(t, "b", "Patient", "meta.source", ir.PhysicalOmitNulls, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("c", "meta", "source"),
	)
	rendered := pgsq03RenderOK(t, plan)
	if len(rendered.Args) != 7 { // 4 scope + {meta,source} + {meta} + source
		t.Fatalf("deduplicated args = %d (%#v), want 7", len(rendered.Args), rendered.Args)
	}
	if !strings.Contains(rendered.SQL, `$5`) || !strings.Contains(rendered.SQL, `$6`) || !strings.Contains(rendered.SQL, `$7`) {
		t.Fatalf("expected exactly $1..$7 in:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "$8") {
		t.Fatalf("a path parameter was duplicated:\n%s", rendered.SQL)
	}
}

func TestNullSemanticsHostileSegmentsFailClosedBeforeSQL(t *testing.T) {
	// Segments are re-checked against the frozen ir path contract at the
	// renderer boundary: anything outside [A-Za-z_][A-Za-z0-9_]* is a
	// typed refusal with no SQL and no parameters, never a quote escape.
	for _, segment := range []string{"", "bad-name", "a.b", `"x"`, "drop;--", "1x", "ſexp"} {
		projection := ir.PhysicalProjection{
			Name: "evil",
			Expression: &ir.PhysicalExpression{
				Kind:         ir.PhysicalExtractExpression,
				Cardinality:  ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Extract: &ir.PhysicalExtract{
					Source:       pgsq03DocumentSource(),
					ResourceType: "Patient",
					Selector:     spec.Selector{Steps: []spec.SelectorStep{{Field: segment}}},
				},
			},
		}
		plan := pgsq03SelectorPlan("Patient", projection)
		rendered, err := RenderSelectorExtraction(plan, mustBinding(t))
		if !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("segment %q error = %v, want ErrUnsupportedPostgresPlan", segment, err)
		}
		if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
			t.Fatalf("rejected segment %q produced output: %#v", segment, rendered)
		}
		// The refusal must name the offending segment verbatim (quoted) —
		// an exact-named refusal, not a generic capability gap.
		wantIn := strconv.Quote(segment)
		if segment == "" {
			wantIn = "has no property"
		}
		if !strings.Contains(err.Error(), wantIn) {
			t.Fatalf("refusal for %q must contain %q: %v", segment, wantIn, err)
		}
	}
}

func TestNullSemanticsDeterministicAndNonAliasing(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalEmptyOnNull, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("gender_present", "gender"),
	)
	first := pgsq03RenderOK(t, plan)
	second := pgsq03RenderOK(t, plan)
	if first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("repeated render drifted")
	}
	capturedSQL, capturedArgs := first.SQL, append([]compiler.PostgreSQLParameter(nil), first.Args...)

	// Mutate every piece of plan data the renderer read. The scanned
	// resource type is deliberately not among them: drifting it away from
	// the selector's schema type is itself a refusal (see the slice's
	// resource-type trust gate), not a silent rebind.
	plan.BindVars["project"] = "mutated"
	plan.BindVars["auth_resource_paths"].([]string)[0] = "MUTATED"
	third, err := RenderSelectorExtraction(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("render after mutation: %v", err)
	}
	if first.SQL != capturedSQL || !reflect.DeepEqual(first.Args, capturedArgs) {
		t.Fatalf("rendered result aliased plan data")
	}
	// The mutated plan must bind the mutated values (proof the earlier
	// render froze copies, not references).
	if reflect.DeepEqual(third.Args, capturedArgs) {
		t.Fatalf("mutated plan re-bound the frozen arguments")
	}
}

func TestNullSemanticsUntrustedBindingAndScopeFailuresProduceNothing(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("gender_present", "gender"),
	)
	if _, err := RenderSelectorExtraction(plan, ir.PostgresStorageBinding{}); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	for _, mutate := range []func(map[string]any){
		func(b map[string]any) { delete(b, "project") },
		func(b map[string]any) { delete(b, "scope_allowed") },
		func(b map[string]any) { b["scope_allowed"] = "yes" },
		func(b map[string]any) { b["auth_resource_paths_unrestricted"] = true },
	} {
		mutated := pgsq03SelectorPlan("Patient",
			pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric))
		mutate(mutated.BindVars)
		rendered, err := RenderSelectorExtraction(mutated, mustBinding(t))
		if !errors.Is(err, ErrInvalidPostgresPlanScope) {
			t.Fatalf("scope defect error = %v, want ErrInvalidPostgresPlanScope", err)
		}
		if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
			t.Fatalf("rejected scope produced output: %#v", rendered)
		}
	}
}
