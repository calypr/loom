package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	pgsq03Project = "pgsq03-proj"
	pgsq03Other   = "pgsq03-other"
	pgsq03Gen     = "gen-1"
	pgsq03Path    = "FHIR/Patient"
)

func pgsq03DocumentSource() ir.PhysicalValue {
	return ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}
}

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

func pgsq03RenderOK(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderSelectorExtraction(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderSelectorExtraction: %v", err)
	}
	return rendered
}

const pgsq03GoldenTail = ` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
	` WHERE r0."project" = $1` +
	` AND r0."dataset_generation" = $2` +
	` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
	` AND r0."resource_type" = $4` +
	` ORDER BY r0."resource_seq" ASC`

func pgsq03GoldenSQL(selects string) string {
	return `SELECT ` + selects + pgsq03GoldenTail
}

func pgsq03ScopeArgs() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq03Project},
		{Type: command.PostgreSQLParameterText, Value: pgsq03Gen},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
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
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{gender}"},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "gender"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("preserve-null args = %#v, want %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"logical_id", "gender", "gender_present"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
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
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{gender}"},
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

	wantSQL := pgsq03GoldenSQL(`(r0."payload" #> $5::text[]) AS "c0", ` +
		`COALESCE(jsonb_typeof(r0."payload" #> $6::text[]) = 'object' AND jsonb_exists(r0."payload" #> $6::text[], $7), false) AS "c1"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("nested SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(pgsq03ScopeArgs(),
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{meta,source}"},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{meta}"},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "source"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("nested args = %#v, want %#v", rendered.Args, wantArgs)
	}
	projections := rendered.SQL[:strings.Index(rendered.SQL, " FROM ")]
	for _, forbidden := range []string{"meta", "source"} {
		if strings.Contains(projections, forbidden) {
			t.Fatalf("path segment %q leaked into SQL projections: %s", forbidden, projections)
		}
	}
}

func TestNullSemanticsNullNamedSegmentsBindQuotedArrayElements(t *testing.T) {
	r := &planRenderer{storage: mustBinding(t), parameters: map[string]int{}, rows: map[string]rowScope{}}

	pathArgument := func(t *testing.T, expression string) string {
		t.Helper()
		var index int
		if _, err := fmt.Sscanf(expression, `r0."payload" #> $%d::text[]`, &index); err != nil || index < 1 || index > len(r.args) {
			t.Fatalf("unexpected path expression %q (err %v, %d args)", expression, err, len(r.args))
		}
		text, ok := r.args[index-1].Value.(string)
		if !ok {
			t.Fatalf("path argument %d is %#v, want string", index, r.args[index-1])
		}
		return text
	}

	if got, err := r.documentPathExpression("r0", []string{"gender"}); err != nil || pathArgument(t, got) != "{gender}" {
		t.Fatalf("plain segment must stay unquoted: %q %v", got, err)
	}
	for _, keyword := range []string{"null", "NULL", "nUlL"} {
		got, err := r.documentPathExpression("r0", []string{keyword})
		if err != nil {
			t.Fatalf("keyword %q: %v", keyword, err)
		}
		if want := `{"` + keyword + `"}`; pathArgument(t, got) != want {
			t.Fatalf("keyword %q bound as %q, want %q", keyword, pathArgument(t, got), want)
		}
	}
	if got, err := r.documentPathExpression("r0", []string{"nullable"}); err != nil || pathArgument(t, got) != "{nullable}" {
		t.Fatalf("near-miss segment must stay unquoted: %q %v", got, err)
	}
	if got, err := r.documentPathExpression("r0", []string{"null", "x"}); err != nil || pathArgument(t, got) != `{"null",x}` {
		t.Fatalf("nested keyword path = %q %v", got, err)
	}

	presence, err := r.propertyPresenceExpression("r0", []string{"null", "x"})
	if err != nil {
		t.Fatalf("nested presence: %v", err)
	}
	var prefixIndex, prefixAgain, keyIndex int
	if _, err := fmt.Sscanf(presence,
		`COALESCE(jsonb_typeof(r0."payload" #> $%d::text[]) = 'object' AND jsonb_exists(r0."payload" #> $%d::text[], $%d), false)`,
		&prefixIndex, &prefixAgain, &keyIndex); err != nil || prefixIndex != prefixAgain {
		t.Fatalf("nested presence shape unparseable: %q (err %v)", presence, err)
	}
	if prefixIndex < 1 || prefixIndex > len(r.args) {
		t.Fatalf("nested presence prefix index %d out of range", prefixIndex)
	}
	if text, ok := r.args[prefixIndex-1].Value.(string); !ok || text != `{"null"}` {
		t.Fatalf("presence prefix = %#v, want {\"null\"}", r.args[prefixIndex-1])
	}
	if keyIndex < 1 || keyIndex > len(r.args) {
		t.Fatalf("nested presence key index %d out of range", keyIndex)
	}
	if text, ok := r.args[keyIndex-1].Value.(string); !ok || text != "x" {
		t.Fatalf("presence terminal key = %#v, want plain x", r.args[keyIndex-1])
	}

	single, err := r.propertyPresenceExpression("r0", []string{"null"})
	if err != nil {
		t.Fatalf("single presence: %v", err)
	}
	if _, err := fmt.Sscanf(single,
		`COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $%d), false)`,
		&keyIndex); err != nil || keyIndex < 1 || keyIndex > len(r.args) {
		t.Fatalf("single presence shape unparseable: %q (err %v)", single, err)
	}
	if text, ok := r.args[keyIndex-1].Value.(string); !ok || text != "null" {
		t.Fatalf("single presence key = %#v, want plain null (scalar keys are not array elements)", r.args[keyIndex-1])
	}

	for _, argument := range r.args {
		if text, ok := argument.Value.(string); ok && text == "{null}" {
			t.Fatalf("unquoted null array element reached the driver: %s", text)
		}
	}
}

func TestNullSemanticsFilterPayloadPathsShareQuotedEncoding(t *testing.T) {
	plan := basePlan()
	plan.BindVars["null_value"] = "present"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"null"}},
			Right: &ir.PhysicalValue{BindKey: "null_value"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"null", "x"}},
			Right: &ir.PhysicalValue{BindKey: "null_value"},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	if text, ok := rendered.Args[4].Value.(string); !ok || text != `{"null"}` {
		t.Fatalf("filter payload path arg = %#v, want {\"null\"}", rendered.Args[4])
	}
	if text, ok := rendered.Args[6].Value.(string); !ok || text != `{"null",x}` {
		t.Fatalf("nested filter payload path arg = %#v, want {\"null\",x}", rendered.Args[6])
	}
	for _, argument := range rendered.Args {
		if text, ok := argument.Value.(string); ok && text == "{null}" {
			t.Fatalf("unquoted null array element reached the driver: %s", text)
		}
	}
}

func TestNullSemanticsPresenceIsTotalNeverNull(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("gender_present", "gender"),
	)
	sql := pgsq03RenderOK(t, plan).SQL
	if !strings.Contains(sql, `COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $6), false)`) {
		t.Fatalf("presence proof must be a COALESCE-total two-valued predicate:\n%s", sql)
	}
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
	plan := pgsq03SelectorPlan("Patient",
		pgsq03ExtractProjection(t, "a", "Patient", "meta.source", ir.PhysicalPreserveNull, ir.PhysicalSelectorGeneric),
		pgsq03ExtractProjection(t, "b", "Patient", "meta.source", ir.PhysicalOmitNulls, ir.PhysicalSelectorGeneric),
		pgsq03PresenceSidecar("c", "meta", "source"),
	)
	rendered := pgsq03RenderOK(t, plan)
	if len(rendered.Args) != 7 {
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
	capturedSQL, capturedArgs := first.SQL, append([]command.PostgreSQLParameter(nil), first.Args...)

	plan.BindVars["project"] = "mutated"
	plan.BindVars["auth_resource_paths"].([]string)[0] = "MUTATED"
	third, err := RenderSelectorExtraction(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("render after mutation: %v", err)
	}
	if first.SQL != capturedSQL || !reflect.DeepEqual(first.Args, capturedArgs) {
		t.Fatalf("rendered result aliased plan data")
	}
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
