package postgres

// pg-sql-13: the stage GROUP — typed scalar keys, the seven registered
// reducers, ALL/ONE row values, and the stable identity. Offline: the
// registered slice renders the lowered inline GROUP plan (real recipe →
// semantic → lower, so the tested input is the production lowered shape),
// the reducer/key-kind/policy tables are pinned as golden mirrors of the
// registered emitters, and the refusal register pins every typed refusal
// of the closed surface with its message verbatim, each paired with the
// healthy base render as the alive control. Live: exact group multisets
// against literal expected rows on PostgreSQL 18 under the owned
// verify_sql13v1_* namespace (registered with the TestMain delta audit),
// the per-reducer empty-group 0-vs-NULL split, the MissingKeyPolicy
// matrix with alive controls, the row-value semantics including InputMany,
// the name→column map pinned by the swapped-source-order cell, and the
// verbatim canonical tags through the guard barrier (22P02).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Lowered-plan fixtures (the production shape: recipe → semantic → lower).
// ---------------------------------------------------------------------------

const (
	group13StepID     = "pg13_group"
	group13Project    = "p13-proj"
	group13Generation = "p13-gen"
	group13AuthPath   = "FHIR/Observation"
)

type group13Fixture struct {
	policy      recipe.ConstructionGroupMissingKeyPolicy
	keys        []recipe.ConstructionGroupKey
	aggregates  []recipe.ConstructionGroupAggregate
	rowValues   []recipe.ConstructionRowValue
	outputs     []recipe.StageColumn
	sourceOrder []string // names from {resource_id,status,value}; default as declared
}

func group13DefaultOutput() recipe.Output {
	return recipe.Output{
		Name: "pg13_group_rows", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{
			{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "status", ColumnID: "status", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "value", ColumnID: "value", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "resource_id", Name: "resource_id"},
				{ID: "status", Name: "status"},
				{ID: "value", Name: "value"},
			},
		},
	}
}

func (f group13Fixture) output() recipe.Output {
	output := group13DefaultOutput()
	if len(f.sourceOrder) > 0 {
		byName := map[string]recipe.Field{}
		for _, field := range output.Fields {
			byName[field.Name] = field
		}
		columns := map[string]recipe.StageColumn{}
		for _, column := range output.Construction.SourceColumns {
			columns[column.Name] = column
		}
		output.Fields = nil
		output.Construction.SourceColumns = nil
		for _, name := range f.sourceOrder {
			output.Fields = append(output.Fields, byName[name])
			output.Construction.SourceColumns = append(output.Construction.SourceColumns, columns[name])
		}
	}
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: group13StepID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: group13StepID, MissingKeyPolicy: f.policy,
			Keys: f.keys, Aggregates: f.aggregates,
		}},
		Outputs:   f.outputs,
		RowValues: f.rowValues,
	}}
	return output
}

func group13LoweredPlan(t *testing.T, fixture group13Fixture) ir.PhysicalPlan {
	t.Helper()
	output := fixture.output()
	bindings := recipe.RuntimeBindings{Project: group13Project, DatasetGeneration: group13Generation, AuthResourcePaths: []string{group13AuthPath}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	lowered := compiled.Outputs[0].Plan
	if lowered.StageSequence == nil || len(lowered.StageSequence.Stages) != 1 ||
		lowered.StageSequence.Stages[0].Kind != ir.PhysicalStageGroupOp ||
		lowered.StageSequence.Stages[0].InputStageID != lowered.StageSequence.SourceStageID {
		t.Fatalf("lowered plan is not the inline single-stage GROUP shape: %#v", lowered.StageSequence)
	}
	return ir.ClonePhysicalPlan(lowered)
}

// group13KeyedAllReducers is the ticket's full shape: STRING + DECIMAL
// keys and the seven registered reducers.
func group13KeyedPlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy) ir.PhysicalPlan {
	t.Helper()
	return group13LoweredPlan(t, group13Fixture{
		policy: policy,
		keys: []recipe.ConstructionGroupKey{
			{InputColumnID: "status", OutputColumnID: "status_group"},
			{InputColumnID: "value", OutputColumnID: "value_group"},
		},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
			{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "value", OutputColumnID: "non_null_id"},
			{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status", OutputColumnID: "distinct_id"},
			{Operation: recipe.ConstructionGroupSum, InputColumnID: "value", OutputColumnID: "sum_id"},
			{Operation: recipe.ConstructionGroupMean, InputColumnID: "value", OutputColumnID: "mean_id"},
			{Operation: recipe.ConstructionGroupMin, InputColumnID: "value", OutputColumnID: "min_id"},
			{Operation: recipe.ConstructionGroupMax, InputColumnID: "value", OutputColumnID: "max_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "value_group", Name: "value_group", Type: "decimal", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "non_null_id", Name: "non_null", Type: "integer"},
			{ID: "distinct_id", Name: "distinct", Type: "integer"},
			{ID: "sum_id", Name: "total", Type: "decimal", Nullable: true},
			{ID: "mean_id", Name: "mean", Type: "decimal", Nullable: true},
			{ID: "min_id", Name: "minimum", Type: "decimal", Nullable: true},
			{ID: "max_id", Name: "maximum", Type: "decimal", Nullable: true},
		},
	})
}

func group13CountRowsPlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy) ir.PhysicalPlan {
	t.Helper()
	return group13LoweredPlan(t, group13Fixture{
		policy:     policy,
		keys:       []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
		aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
		},
	})
}

func group13KeylessPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	return group13LoweredPlan(t, group13Fixture{
		policy:     recipe.ConstructionGroupMissingKeyGroup,
		aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		rowValues: []recipe.ConstructionRowValue{
			{InputColumnID: "status", OutputColumnID: "status_values", Policy: recipe.ConstructionRowValueAll},
			{InputColumnID: "value", OutputColumnID: "value_one", Policy: recipe.ConstructionRowValueOne},
		},
		outputs: []recipe.StageColumn{
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "status_values", Name: "status_values", Type: "array"},
			{ID: "value_one", Name: "value_one", Type: "decimal", Nullable: true},
		},
	})
}

func group13RowValuePlan(t *testing.T, policy recipe.ConstructionRowValuePolicy, valueType string) ir.PhysicalPlan {
	t.Helper()
	return group13LoweredPlan(t, group13Fixture{
		policy:     recipe.ConstructionGroupMissingKeyGroup,
		keys:       []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
		aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		rowValues: []recipe.ConstructionRowValue{
			{InputColumnID: "value", OutputColumnID: "value_values", Policy: policy},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "value_values", Name: "value_values", Type: valueType, Nullable: true},
		},
	})
}

// group13Identity builds the expected stable identity string independently
// of the renderer: the JSON-compact array spelling of the AQL
// TO_STRING member list (construction_group.go:113,143,339-340) —
// [["construction",@cid],["operation","GROUP"],[<colID>,<key>]...].
// String members are JSON-quoted; a numeric member is the raw numeric
// text (unquoted); nil spells null.
func group13Identity(t *testing.T, plan ir.PhysicalPlan, keys []group13Member) string {
	t.Helper()
	stage := plan.StageSequence.Stages[0]
	outputIDs := map[string]string{}
	for _, column := range stage.OutputColumns {
		if _, seen := outputIDs[column.Name]; !seen {
			outputIDs[column.Name] = column.ID
		}
	}
	member := func(value group13Member) string {
		if value.nilValue {
			return "null"
		}
		if value.numeric {
			return value.text
		}
		raw, err := json.Marshal(value.text)
		if err != nil {
			t.Fatalf("marshal identity member: %v", err)
		}
		return string(raw)
	}
	parts := []string{
		`["construction","` + group13StepID + `"]`, `["operation","GROUP"]`,
	}
	for _, key := range keys {
		columnID, ok := outputIDs[key.outputColumn]
		if !ok {
			t.Fatalf("group key output %q has no stable column ID", key.outputColumn)
		}
		parts = append(parts, fmt.Sprintf("[%q,%s]", columnID, member(key)))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type group13Member struct {
	outputColumn string
	text         string
	numeric      bool
	nilValue     bool
}

func group13TextMember(outputColumn, text string) group13Member {
	return group13Member{outputColumn: outputColumn, text: text}
}

func group13NumericMember(outputColumn, text string) group13Member {
	return group13Member{outputColumn: outputColumn, text: text, numeric: true}
}

func group13NullMember(outputColumn string) group13Member {
	return group13Member{outputColumn: outputColumn, nilValue: true}
}

// ---------------------------------------------------------------------------
// Offline: the registered slice over the lowered plan.
// ---------------------------------------------------------------------------

func TestGroupRenderedSliceContract(t *testing.T) {
	plan := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	for _, want := range []string{
		`WITH g_src AS (`,
		`), g_rows AS (SELECT * FROM g_src`,
		`), g_grp AS (SELECT`,
		`FROM g_rows GROUP BY 1, 2`,
		`COUNT(*) AS g_a0`,
		`FROM g_grp AS g ORDER BY g.g_k0 COLLATE "C" ASC NULLS FIRST, g.g_k1 ASC NULLS FIRST, `,
		`COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("statement is missing %q:\n%s", want, sql)
		}
	}
	// Value-type witnesses dedupe by (read, tag): the five reducers over
	// "value" share one conjunct; status's pool conjunct carries the value
	// tag, the key conjuncts carry the key tag.
	if got := strings.Count(sql, groupMissingKeyTag); got != 0 {
		t.Fatalf("GROUP policy must not carry the missing-key guard (found %d):\n%s", got, sql)
	}
	if got := strings.Count(sql, groupKeyTypeTag); got != 2 {
		t.Fatalf("key-type witnesses = %d, want 2 (status+value):\n%s", got, sql)
	}
	if got := strings.Count(sql, groupValueTypeTag); got != 2 {
		t.Fatalf("value-pool witnesses = %d, want 2 after dedupe (status+value):\n%s", got, sql)
	}
	// AQL bind syntax never crosses into PostgreSQL text.
	if strings.Contains(sql, "@") {
		t.Fatalf("statement carries AQL bind syntax:\n%s", sql)
	}
	// No plan data in statement text: every bound value crosses as $n.
	for _, leaked := range []string{group13Project, group13Generation, group13AuthPath, "Observation", "status", "value", "valueQuantity", group13StepID} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text:\n%s", leaked, sql)
		}
	}
	// Payload paths bind through #> text[] parameters.
	if !strings.Contains(sql, `#> $`) {
		t.Fatalf("payload paths must bind through #> text[] parameters:\n%s", sql)
	}

	// Output columns: keys, reducers, and the hidden stable identity, in
	// FinalColumns order.
	want := []string{"status_group", "value_group", "rows", "non_null", "distinct", "total", "mean", "minimum", "maximum", "__loom_row_id"}
	if !reflect.DeepEqual(rendered.OutputColumns, want) {
		t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
	}

	// Determinism: same plan, byte-identical statement and value-identical
	// arguments.
	second, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("group render is not deterministic: %v", err)
	}
}

func TestGroupPoliciesRender(t *testing.T) {
	group := renderOK(t, group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup))
	if strings.Contains(group.SQL, " IS NOT NULL") || strings.Contains(group.SQL, guardCast(groupMissingKeyTag)) {
		t.Fatalf("GROUP policy must neither filter nor guard key rows:\n%s", group.SQL)
	}

	exclude := renderOK(t, group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyExclude))
	if !strings.Contains(exclude.SQL, " IS NOT NULL") {
		t.Fatalf("EXCLUDE policy must conjunct the null-key drop:\n%s", exclude.SQL)
	}
	if strings.Contains(exclude.SQL, guardCast(groupMissingKeyTag)) {
		t.Fatalf("EXCLUDE policy must not carry the ERROR guard:\n%s", exclude.SQL)
	}

	// ERROR renders the ASSERT counterpart at the g_rows row position
	// (the AQL pre-COLLECT FILTER position) over the constant-folding
	// barrier, with the tag carried verbatim.
	errorRendered := renderOK(t, group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyError))
	for _, want := range []string{
		"(CASE WHEN ", " IS NOT NULL THEN 1 ELSE CAST('" + groupMissingKeyTag + "'",
		guardBarrierGUC,
		" END) > 0",
	} {
		if !strings.Contains(errorRendered.SQL, want) {
			t.Fatalf("ERROR guard is missing %q:\n%s", want, errorRendered.SQL)
		}
	}
}

func TestGroupReducerTableRender(t *testing.T) {
	sql := renderOK(t, group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)).SQL
	for _, want := range []string{
		`COUNT(*) AS g_a0`,
		`COUNT(`,
		`COUNT(DISTINCT `,
		`MIN(`,
		`MAX(`,
		// SUM/MEAN run EXACT numeric accumulation through the group band
		// cast on the calls.go band constants (coordinator ruling
		// 2026-11-11 Q1, F-2 repair, F-6 final form: the overflow arm
		// reads the registered 0 — oracle-attested parity, D-13h — so
		// accumulation stays numeric-exact, and the old post-hoc
		// non-finite gate is GONE; no 'Infinity' comparison may return).
		`SUM((CASE WHEN `, // the F-8 member clamp inside SUM
		` > '1e400'::numeric THEN '1e400'::numeric`,
		` IS NULL THEN NULL WHEN abs(SUM((CASE WHEN `,                                   // member-less group keeps NULL (arm-specific flip, not a coalesce)
		`::double precision ELSE 0::double precision END ELSE 0::double precision END)`, // subnormal 0 + overflow-band 0 arms
		// MEAN: the bounded double sum divided by the pool count over the
		// numeric read (beyond-bound members counted like LENGTH; the
		// zero-value group reads NULL pre-division; overflow 0/n = 0).
		` = 0 THEN NULL ELSE (CASE WHEN SUM((CASE WHEN `,
		`) / (COUNT(`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("reducer table is missing %q:\n%s", want, sql)
		}
	}
	// The unreachable post-hoc finiteness gate (verifier F-2) must stay
	// deleted: a returned 'Infinity' equality would mean float8
	// accumulation crept back.
	for _, forbidden := range []string{`'Infinity'::double precision`, `groupDoubleNotFinite`} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("reducer table reintroduced the deleted gate fragment %q:\n%s", forbidden, sql)
		}
	}
	// Text comparisons pin Arango's byte-order total compare.
	if !strings.Contains(sql, ` COLLATE "C")`) && !strings.Contains(sql, ` COLLATE "C"))`) && !strings.Contains(sql, ` COLLATE "C") AS`) {
		t.Fatalf("string reducer must COLLATE \"C\":\n%s", sql)
	}
}

func TestGroupKeyKindsRender(t *testing.T) {
	// The four registered key kinds read typed in the group domain
	// (construction_group.go:110-144 parity): STRING text, INTEGER/DECIMAL
	// numeric (2 and 2.0 share a group, D-13e), BOOLEAN boolean.
	for _, cell := range []struct {
		kind    string
		read    string
		collate bool
	}{
		{kind: "STRING", read: ` #>> '{}')`, collate: true},
		{kind: "INTEGER", read: ` #>> '{}')::numeric`, collate: false},
		{kind: "DECIMAL", read: ` #>> '{}')::numeric`, collate: false},
		{kind: "BOOLEAN", read: ` #>> '{}')::boolean`, collate: false},
	} {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		stage := &plan.StageSequence.Stages[0]
		stage.Group.Keys[0].Kind = cell.kind
		// The ir contract pins key kind to the INPUT column kind and the
		// OUTPUT column kind, so the hand-assembled kind table retypes
		// all three schema sites together (golden mirror only — the
		// runtime kind truth is the live DECIMAL/STRING cells).
		kindLower := strings.ToLower(cell.kind)
		for _, columns := range [][]ir.PhysicalStageColumn{plan.StageSequence.SourceColumns, stage.InputColumns, stage.OutputColumns, plan.StageSequence.FinalColumns} {
			for i := range columns {
				if columns[i].Name == stage.Group.Keys[0].InputColumn || columns[i].Name == stage.Group.Keys[0].OutputColumn {
					columns[i].Kind = kindLower
				}
			}
		}
		sql := renderOK(t, plan).SQL
		if !strings.Contains(sql, cell.read) {
			t.Fatalf("kind %s key must read %q:\n%s", cell.kind, cell.read, sql)
		}
		orderIndex := strings.LastIndex(sql, " ORDER BY ")
		order := sql[orderIndex:]
		if got := strings.Contains(order, `g.g_k0 COLLATE "C"`); got != cell.collate {
			t.Fatalf("kind %s order collate = %v, want %v:\n%s", cell.kind, got, cell.collate, order)
		}
	}
}

func TestGroupIdentityOrderAndBound(t *testing.T) {
	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	rendered := renderOK(t, plan)
	orderIndex := strings.LastIndex(rendered.SQL, " ORDER BY ")
	if orderIndex < 0 {
		t.Fatalf("final statement has no ORDER BY:\n%s", rendered.SQL)
	}
	final := rendered.SQL[orderIndex:]
	// The AQL stage sort (keys ASC, :135) plus the terminal identity
	// order with the registered total refinement (D-13c): keys first,
	// identity COLLATE "C" last.
	//
	// D-13c attribution (verifier F-4, 2026-11-11): PG total-order
	// refinement on AQL-unspecified sort ties per coordinator ruling
	// 2026-11-11 — AQL's stage sort (construction_group.go:135) leaves
	// ties among equal keys unspecified, so PG pins keys ASC NULLS
	// FIRST and the identity tie-break COLLATE "C" ASC; both live in the
	// plan and header registers, this is the order-test register.
	if !strings.HasPrefix(final, ` ORDER BY g.g_k0 COLLATE "C" ASC NULLS FIRST, `) {
		t.Fatalf("final order must start with the key sort:\n%s", final)
	}
	if !strings.Contains(final, `'[' || '["construction",' || to_jsonb($`) ||
		!strings.Contains(final, `'["operation","GROUP"]'`) ||
		!strings.Contains(final, `COALESCE(to_jsonb(g.g_k0)::text, 'null')`) {
		t.Fatalf("final ORDER BY must end in the stable identity concatenation:\n%s", final)
	}
	if !strings.HasSuffix(final, ` COLLATE "C" ASC`) {
		t.Fatalf("the unwindowed statement ends in the identity order:\n%s", final)
	}

	// The identity is the registered composition: construction member,
	// operation member, and one member per key [columnID, key].
	want := group13Identity(t, plan, []group13Member{group13TextMember("status_group", "final")})
	// (Structural shape only — the literal cell runs live.)
	if !strings.Contains(want, `["construction","`+group13StepID+`"]`) || !strings.Contains(want, `["operation","GROUP"]`) {
		t.Fatalf("independent identity builder is off contract: %s", want)
	}

	// With a preview limit bound, the bound LIMIT follows the identity
	// order (stage_sequence.go:453 over the GROUP-skipped identity sort).
	limited := ir.ClonePhysicalPlan(plan)
	limited.StageSequence.PreviewLimitBindKey = "limit"
	limited.BindVars["limit"] = 25
	limitedRendered := renderOK(t, limited)
	if m, _ := regexp.MatchString(` COLLATE "C" ASC LIMIT \$[0-9]+$`, limitedRendered.SQL); !m {
		t.Fatalf("the bound preview LIMIT must follow the identity order:\n%s", limitedRendered.SQL)
	}
	if len(limitedRendered.Args) != len(rendered.Args)+1 {
		t.Fatalf("preview limit must bind as one extra parameter")
	}
	last := limitedRendered.Args[len(limitedRendered.Args)-1]
	if last.Type != command.PostgreSQLParameterBigint || last.Value != int64(25) {
		t.Fatalf("preview limit bind = %#v, want bigint 25", last)
	}
}

func TestGroupWindowedSourceRegion(t *testing.T) {
	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	sorted := ir.ClonePhysicalPlan(plan)
	last := len(sorted.Operations) - 1
	sorted.Operations = append(sorted.Operations[:last],
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "srclimit"}},
		sorted.Operations[last])
	sorted.BindVars["srclimit"] = 3
	rendered := renderOK(t, sorted)
	srcEnd := strings.Index(rendered.SQL, "), g_rows AS (")
	if srcEnd < 0 {
		t.Fatalf("g_src boundary missing:\n%s", rendered.SQL)
	}
	source := rendered.SQL[:srcEnd]
	if !strings.Contains(source, `r0."logical_id" ASC`) || !strings.Contains(source, `"resource_seq" ASC`) || !strings.Contains(source, "LIMIT $") {
		t.Fatalf("the W1 window must be rendered inside g_src:\n%s", source)
	}
}

func TestGroupRowValuesRender(t *testing.T) {
	all := renderOK(t, group13RowValuePlan(t, recipe.ConstructionRowValueAll, "array")).SQL
	for _, want := range []string{
		`to_jsonb(COALESCE(ARRAY_AGG(DISTINCT `,
		`) FILTER (WHERE `,
		` IS NOT NULL), '{}'::numeric[]`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("ALL emitter is missing %q:\n%s", want, all)
		}
	}
	one := renderOK(t, group13RowValuePlan(t, recipe.ConstructionRowValueOne, "decimal")).SQL
	for _, want := range []string{
		`CASE WHEN COUNT(DISTINCT `,
		` <= 1 THEN MIN(`,
		` ELSE `,
		` END) AS g_rv0`,
	} {
		if !strings.Contains(one, want) {
			t.Fatalf("ONE emitter is missing %q:\n%s", want, one)
		}
	}
	if !strings.Contains(one, guardCast(groupRowValueMultipleTag)) {
		t.Fatalf("ONE must raise the canonical multi-value tag through the guard:\n%s", one)
	}
}

func TestGroupInputManyRender(t *testing.T) {
	// Hand-assembled per the AQL group_row_value_preview_test.go template:
	// one jsonb array layer flattened per row through a LATERAL
	// jsonb_array_elements in its OWN pool CTE (the expansion multiplies
	// rows, so it must not share g_grp's aggregate context), the AQL
	// null-filter (row_values.go:33,45) applied before unique, and the
	// row-level IS_ARRAY witness at the g_rows position
	// (row_values.go:79-83 parity, CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH).
	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	appendGroup13ManyRowValue(t, &plan, "ALL", "STRING")
	sql := renderOK(t, plan).SQL
	for _, want := range []string{
		` AS (SELECT `,
		` FROM g_rows, LATERAL jsonb_array_elements(`,
		`) _rv(item) WHERE `,
		// Verifier F-3 (2026-11-11): the null filter rides at the JSONB
		// level — `WHERE NULLIF((_rv.item), 'null'::jsonb) IS NOT NULL`
		// — never the typed member read. Conjuncts carry no cast, so the
		// typeof guard is the only raise source in the WHERE regardless
		// of conjunct evaluation order (a mistyped DECIMAL member must
		// read the tagged 22P02, not a raw cast error).
		` WHERE NULLIF((_rv.item), 'null'::jsonb) IS NOT NULL AND (CASE WHEN COALESCE(NULLIF((_rv.item), 'null'::jsonb) IS NULL OR jsonb_typeof(NULLIF((_rv.item), 'null'::jsonb)) =`,
		` THEN 1 ELSE CAST('` + groupValueTypeTag + `'`,
		` GROUP BY 1`,
		`AS m_pool`,
		`ARRAY_AGG(DISTINCT `,
		`'{}'::text[]`,
		`LEFT JOIN g_m0 ON `,
		` IS NOT DISTINCT FROM g_m0.m_k0`,
		`MAX(g_m0.m_pool::text)::jsonb AS g_rv0`,
		`= 'array', false) THEN 1 ELSE CAST('` + groupValueTypeTag + `'`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("InputMany ALL emitter is missing %q:\n%s", want, sql)
		}
	}
	onePlan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	appendGroup13ManyRowValue(t, &onePlan, "ONE", "STRING")
	oneSQL := renderOK(t, onePlan).SQL
	for _, want := range []string{
		`COUNT(DISTINCT `,
		`<= 1 THEN MIN(`,
		` ELSE ` + groupRowValueRaiseArm("STRING"),
		`AS m_pool`,
		`MAX(g_m0.m_pool) AS g_rv0`,
	} {
		if !strings.Contains(oneSQL, want) {
			t.Fatalf("InputMany ONE emitter is missing %q:\n%s", want, oneSQL)
		}
	}
}

// appendGroup13ManyRowValue attaches the hand-assembled many-cardinality
// row value (the group_row_value_preview_test.go template): the source
// RETURN gains one jsonb array projection named "values" and the stage
// gains its ALL/ONE row value over it.
// inputKind is the row-value InputKind (STRING/INTEGER/DECIMAL/BOOLEAN);
// the source/output column kinds follow it (verifier F-3 cells need the
// DECIMAL pool, whose mistyped-member raise the guard-before-cast law
// tags).
func appendGroup13ManyRowValue(t *testing.T, plan *ir.PhysicalPlan, policy, inputKind string) {
	t.Helper()
	returnIndex := -1
	for i, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			returnIndex = i
		}
	}
	if returnIndex < 0 {
		t.Fatal("plan has no terminal RETURN")
	}
	// The source-projection path is payload-RELATIVE (the renderer binds
	// {status}, {valueQuantity,value} alike); a {payload,values} path
	// would read payload->payload->values — always missing (measured).
	arrayValue := ir.PhysicalValue{Variable: "root", Path: []string{"values"}}
	plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections,
		ir.PhysicalProjection{Name: "values", Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &arrayValue,
		}})
	columnKind := map[string]string{
		"STRING": "string", "INTEGER": "integer", "DECIMAL": "decimal", "BOOLEAN": "boolean",
	}[inputKind]
	if columnKind == "" {
		t.Fatalf("unknown row-value input kind %q", inputKind)
	}
	stage := &plan.StageSequence.Stages[0]
	stage.Group.RowValues = []ir.PhysicalStageRowValue{{
		InputColumn: "values", InputKind: inputKind, InputMany: true,
		Output: "values_set", Policy: policy, Variable: "values_set",
	}}
	// The ir cross-checks the row-value input against the stage's input
	// schema and InputProjections (the group_row_value_preview_test.go
	// template registers all sites): the source column enters the
	// sequence schema, the stage input schema, and the stage's input
	// projection list together.
	sourceColumn := ir.PhysicalStageColumn{ID: "values-source", Name: "values", Kind: columnKind, Cardinality: "many"}
	plan.StageSequence.SourceColumns = append(plan.StageSequence.SourceColumns, sourceColumn)
	stage.InputColumns = append(stage.InputColumns, sourceColumn)
	stage.InputProjections = append(stage.InputProjections,
		ir.PhysicalProjection{Name: "values", Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{"values"}}})
	// The ir output contract: ALL is a many-cardinality array, ONE a
	// nullable scalar (row_values lowering law).
	cardinality := "many"
	if policy == "ONE" {
		cardinality = "optional_one"
	}
	column := ir.PhysicalStageColumn{ID: "values-set-output", Name: "values_set", Kind: columnKind, Cardinality: cardinality, Nullable: policy == "ONE"}
	stage.OutputColumns = append(stage.OutputColumns, column)
	plan.StageSequence.FinalColumns = append(plan.StageSequence.FinalColumns, column)
	stage.OutputProjections = append(stage.OutputProjections,
		ir.PhysicalProjection{Name: "values_set", Value: ir.PhysicalValue{Variable: "values_set"}})
}

func TestGroupKeylessGrandTotal(t *testing.T) {
	sql := renderOK(t, group13KeylessPlan(t)).SQL
	if strings.Contains(sql, "GROUP BY") {
		t.Fatalf("the keyless grand total renders without GROUP BY:\n%s", sql)
	}
	for _, want := range []string{`COUNT(*) AS g_a0`, `to_jsonb(COALESCE(ARRAY_AGG(DISTINCT `, `CASE WHEN COUNT(DISTINCT `} {
		if !strings.Contains(sql, want) {
			t.Fatalf("keyless grand total is missing %q:\n%s", want, sql)
		}
	}
	// The keyless gate admits the COUNT_ROWS-only combination (+ row
	// values, the AQL :44-52 legal pair) and nothing else. The AQL gate
	// refuses keyless non-COUNT_ROWS at lowering; the door re-read stays
	// load-bearing for hand-assembled IR.
	keylessSum := group13KeylessPlan(t)
	stage := &keylessSum.StageSequence.Stages[0]
	stage.Group.Aggregates = []ir.PhysicalStageGroupAggregate{
		{Operation: "COUNT_ROWS", Output: "rows", Variable: stage.Group.Aggregates[0].Variable, OutputKind: "integer"},
		{Operation: "SUM", InputColumn: "value", Output: "total", Variable: "g_sum", InputKind: "DECIMAL", OutputKind: "decimal"},
	}
	keylessSum.StageSequence.FinalColumns = append(keylessSum.StageSequence.FinalColumns,
		ir.PhysicalStageColumn{ID: "sum_id", Name: "total", Kind: "decimal", Cardinality: "optional_one", Nullable: true})
	stage.OutputColumns = append(stage.OutputColumns,
		ir.PhysicalStageColumn{ID: "sum_id", Name: "total", Kind: "decimal", Cardinality: "optional_one", Nullable: true})
	stage.OutputProjections = append(stage.OutputProjections,
		ir.PhysicalProjection{Name: "total", Value: ir.PhysicalValue{Variable: "g_sum"}})
	requireGroup13Refusal(t, keylessSum, "only keyless COUNT_ROWS groups can consume the source scope")
}

// ---------------------------------------------------------------------------
// Offline: the name→column map — swapped source order re-reads by NAME.
// ---------------------------------------------------------------------------

func TestGroupNameMapFollowsSourceOrder(t *testing.T) {
	// The positional c0..cN materialization is keyed by NAME, so a
	// swapped source RETURN must map each key/aggregate to its column's
	// EXPRESSION, never to a position accident (standing condition of the
	// coordinator ruling 2026-11-11: a swapped fixture order must not
	// silently misread columns).
	swappedFixture := group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys: []recipe.ConstructionGroupKey{
			{InputColumnID: "status", OutputColumnID: "status_group"},
			{InputColumnID: "value", OutputColumnID: "value_group"},
		},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
			{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "value", OutputColumnID: "non_null_id"},
			{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status", OutputColumnID: "distinct_id"},
			{Operation: recipe.ConstructionGroupSum, InputColumnID: "value", OutputColumnID: "sum_id"},
			{Operation: recipe.ConstructionGroupMean, InputColumnID: "value", OutputColumnID: "mean_id"},
			{Operation: recipe.ConstructionGroupMin, InputColumnID: "value", OutputColumnID: "min_id"},
			{Operation: recipe.ConstructionGroupMax, InputColumnID: "value", OutputColumnID: "max_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "value_group", Name: "value_group", Type: "decimal", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "non_null_id", Name: "non_null", Type: "integer"},
			{ID: "distinct_id", Name: "distinct", Type: "integer"},
			{ID: "sum_id", Name: "total", Type: "decimal", Nullable: true},
			{ID: "mean_id", Name: "mean", Type: "decimal", Nullable: true},
			{ID: "min_id", Name: "minimum", Type: "decimal", Nullable: true},
			{ID: "max_id", Name: "maximum", Type: "decimal", Nullable: true},
		},
		sourceOrder: []string{"resource_id", "value", "status"},
	}
	baseSameShape := group13LoweredPlan(t, func(f group13Fixture) group13Fixture { f.sourceOrder = nil; return f }(swappedFixture))
	swapped := group13LoweredPlan(t, swappedFixture)
	baseSQL := renderOK(t, baseSameShape).SQL
	swappedSQL := renderOK(t, swapped).SQL
	// The two source RETURN projections extract the same two payload
	// paths; the DECIMAL key must read through the SAME extraction
	// parameter in both renderings (the paths bind identically by
	// content), and the g_grp section — whose materialized "c<i>"
	// references are name-resolved — must be identical up to the
	// positional renumbering the swap legitimately causes.
	grp := func(sql string) string {
		start := strings.Index(sql, "), g_grp AS (SELECT ")
		if start < 0 {
			t.Fatalf("g_grp boundary missing:\n%s", sql)
		}
		end := strings.Index(sql[start:], " FROM g_rows")
		if end < 0 {
			t.Fatalf("g_rows boundary missing:\n%s", sql)
		}
		// Parameter numbering follows g_src projection order; the map
		// law is about WHICH expression each name resolves to, so the
		// comparison normalizes $n indices. The materialized "c<i>"
		// references are canonicalized by FIRST-USE order within the
		// region: a name→column map that misreads (resolving a name to
		// the wrong source column) produces a use pattern that is not
		// first-use-consistent with the other rendering, so the
		// comparison still catches position accidents.
		region := regexp.MustCompile(`\$[0-9]+`).ReplaceAllString(sql[start:start+end], "$N")
		seen := map[string]string{}
		return regexp.MustCompile(`"c[0-9]+"`).ReplaceAllStringFunc(region, func(match string) string {
			if canonical, ok := seen[match]; ok {
				return canonical
			}
			canonical := `"k` + strconv.Itoa(len(seen)) + `"`
			seen[match] = canonical
			return canonical
		})
	}
	if grp(baseSQL) != grp(swappedSQL) {
		t.Fatalf("g_grp must not depend on source projection order:\nbase:    %s\nswapped: %s", grp(baseSQL), grp(swappedSQL))
	}
}

// ---------------------------------------------------------------------------
// Offline: the refusal register — every message pinned verbatim, each with
// the healthy base render as the alive control.
// ---------------------------------------------------------------------------

func requireGroup13Refusal(t *testing.T, plan ir.PhysicalPlan, want string) {
	t.Helper()
	_, err := renderGroupPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err == nil {
		t.Fatalf("plan must be refused (want %q)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %q, want substring %q", err, want)
	}
	// Refusals come from either door (typed ErrUnsupportedPostgresPlan)
	// or the ir schema gate (the plan.Validate leg both doors run
	// unchanged); belt-and-braces door arms behind an ir gate are pinned
	// with the ir message and flagged in the cell comment.
	if !strings.Contains(err.Error(), ErrUnsupportedPostgresPlan.Error()) &&
		!strings.HasPrefix(err.Error(), "validate physical plan:") {
		t.Fatalf("refusal is neither a door refusal nor an ir plan refusal: %q", err)
	}
	// Entry-door equality: whenever the plan still MENTIONS the GROUP
	// surface (so the door dispatches), RenderPhysicalPlan must refuse
	// with the identical message. Mutations that rename the stage kind
	// away are direct-door cells only (the sequence then never reaches
	// the group door). Multi-stage sequences are the exception since
	// pg-sql-16: the composition door (stage_sequence.go) claims them
	// ahead of this slice, so their entry refusal is the composition
	// register's own message (pinned in stage_sequence_test.go) while the
	// group door keeps the single-stage L-13.1 message for direct calls.
	if groupPlanMentionsStage(plan) && !stageSequenceCompositionMentions(plan) {
		_, entryErr := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if entryErr == nil || entryErr.Error() != err.Error() {
			t.Fatalf("entry door refused differently: entry=%v dispatch=%v", entryErr, err)
		}
	}
}

func group13AliveControl(t *testing.T) {
	t.Helper()
	if _, err := renderGroupPhysicalPlan(group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup), mustBinding(t)); err != nil {
		t.Fatalf("alive control: the healthy base plan must render: %v", err)
	}
}

func TestGroupRefusalRegister(t *testing.T) {
	base := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)

	cases := []struct {
		name   string
		mutate func(t *testing.T, plan *ir.PhysicalPlan)
		want   string
	}{
		{"L-13.1-multi-stage", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages = append(plan.StageSequence.Stages, ir.ClonePhysicalPlan(*plan).StageSequence.Stages[0])
		}, "exactly one GROUP construction stage"},
		{"L-13.1-foreign-stage-kind", func(t *testing.T, plan *ir.PhysicalPlan) {
			// Direct-door cell (the sequence no longer MENTIONS a GROUP
			// stage, so the entry door never dispatches it — the spine
			// arms refuse it as merged): the door's stage-kind arm.
			plan.StageSequence.Stages[0].Kind = ir.PhysicalStageUnpivotOp
		}, "operation \"UNPIVOT\" is not part of the registered group slice"},
		{"L-13.1-foreign-payload", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Filter = &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS",
				Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}
		}, "GROUP requires only a group payload"},
		{"L-13.1-not-inline-first-stage", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].InputStageID = "somewhere-else"
		}, "admits only the inline first-stage GROUP"},
		{"L-13.10-cell-trace-return", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
		}, "row-lineage, cell-trace, and population-mapping returns"},
		{"L-13.10-preview-window", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.PreviewSourceWindowByRootID = true
		}, "preview source windowing"},
		{"L-13.10-auth-resource-path-bind", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.OutputAuthResourcePathBindKey = "output_auth_resource_path"
		}, "composite Combine prefix authorization path"},
		{"L-13.13-preview-limit-zero", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.PreviewLimitBindKey = "limit"
			plan.BindVars["limit"] = 0
		}, "must be a positive int"},
		{"L-13.13-preview-limit-type", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.PreviewLimitBindKey = "limit"
			plan.BindVars["limit"] = "25"
		}, "must be a positive int"},
		{"L-13.13-construction-bind-blank", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars[plan.StageSequence.Stages[0].Group.ConstructionIDBindKey] = "   "
		}, "must be a non-empty string"},
		{"L-13.12-missing-key-policy", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.MissingKeyPolicy = "DROP"
		}, "unsupported missing-key policy"},
		{"L-13.12-reducer-operation", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Aggregates[0].Operation = "MEDIAN"
		}, "unsupported aggregate operation"},
		{"L-13.12-row-value-policy", func(t *testing.T, plan *ir.PhysicalPlan) {
			appendGroup13ManyRowValue(t, plan, "MANY", "STRING")
		}, "has unsupported policy"},
		{"L-13.3-semantic-key", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey = &ir.PhysicalStageGroupSemanticKey{
				OutputColumn: "category", TupleVariable: "sem_tuple", LabelVariable: "sem_label",
				SourceRootVariable: "root", SourceIdentityColumn: "_key", RootCollectionBindKey: "root_collection",
				ResourceType: "Observation", CodingPath: "code.coding[]",
			}
		}, "semantic family Group key is a registered named gap"},
		{"L-13.4-coding-key", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.CodingKey = &ir.PhysicalStageGroupCodingKey{
				OutputColumn: "code", InputTupleVariable: "code_tuple", TupleVariable: "code_item",
				LabelVariable: "code_label", MembershipColumn: "codes",
			}
		}, "CODING_IDENTITY Group key is refused"},
		{"L-13.6-root-contributor", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.RootContributorInputColumn = "contributors"
		}, "root-contributor set is production-unreachable"},
		{"L-13.8-population-root", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
		}, "population, cohort, and candidate-seed roots"},
		{"L-13.9-unnest-operation", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.Operations = append(plan.Operations[:1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalUnnestOp,
				Unnest: &ir.PhysicalUnnest{}}}, plan.Operations[1:]...)...)
		}, "not part of the registered group source surface (L-13.9)"},
		{"L-13.2-set-operation", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.Operations = append(plan.Operations[:1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalSetOp}}, plan.Operations[1:]...)...)
		}, "group source-region SET is a pg-sql-16 capability gap"},
		{"L-13.9-missing-return", func(t *testing.T, plan *ir.PhysicalPlan) {
			last := len(plan.Operations) - 1
			plan.Operations = append(plan.Operations[:last], plan.Operations[last+1:]...)
		}, "requires a terminal RETURN"},
		{"L-13.11-key-kind", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].Kind = "ARRAY"
		}, "unsupported scalar kind"},
		{"L-13.11-key-hidden", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].InputInternal = true
		}, "hidden or field-sourced input surface"},
		{"L-13.11-key-field-source", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].FieldSource = &ir.PhysicalStageGroupFieldSource{
				Kind: ir.PhysicalStageGroupFieldSourceCurrentStage,
			}
		}, "hidden or field-sourced input surface"},
		{"L-13.11-row-value-field-source", func(t *testing.T, plan *ir.PhysicalPlan) {
			appendGroup13ManyRowValue(t, plan, "ALL", "STRING")
			plan.StageSequence.Stages[0].Group.RowValues[0].FieldSource = &ir.PhysicalStageGroupFieldSource{
				Kind: ir.PhysicalStageGroupFieldSourceCurrentStage,
			}
		}, "field-sourced input surface"},
		// Unknown input columns: the door's name-map re-read refuses
		// first (L-13.11), before plan.Validate's public-scalar gate.
		{"L-13.11-key-unknown-input", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].InputColumn = "no_such_column"
		}, "not among the source projection names (L-13.11)"},
		{"L-13.11-aggregate-unknown-input", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Aggregates[1].InputColumn = "no_such_column"
		}, "not among the source projection names (L-13.11)"},
		{"L-13.13-count-rows-with-input", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Aggregates[0].InputColumn = "status"
		}, "COUNT_ROWS must not have an input column"},
		{"L-13.13-sum-without-input", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Aggregates[3].InputColumn = ""
		}, "requires an input column"},
	}
	for _, cell := range cases {
		t.Run(cell.name, func(t *testing.T) {
			plan := ir.ClonePhysicalPlan(base)
			cell.mutate(t, &plan)
			requireGroup13Refusal(t, plan, cell.want)
		})
	}
	// L-13.5 was the refusal pin for the related-occurrence Group adapter.
	// pg-sql-16 G3(c) CLOSED the gap: the RenderPhysicalPlan entry now
	// ROUTES every adapter-mentioning sequence to the composition slice
	// (stageSequenceRelatedOccurrenceMentions, ahead of this door), whose
	// renderStageGroup dispatch arm owns the adapter, so the table's
	// entry==dispatch equality arm cannot apply to this shape — it reaches
	// a different door by design. This pin keeps both halves honest.
	t.Run("L-13.5-related-occurrence-routes-to-composition", func(t *testing.T) {
		plan := ir.ClonePhysicalPlan(base)
		plan.StageSequence.Stages[0].Group.RelatedOccurrence = &ir.PhysicalStageGroupRelatedOccurrenceAdapter{RouteID: "r"}
		if !stageSequenceRelatedOccurrenceMentions(plan) {
			t.Fatal("the entry claim must route the adapter shape to the composition slice")
		}
		_, entryErr := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if entryErr == nil || !strings.Contains(entryErr.Error(), "unreferenced related route adapter") {
			t.Fatalf("the entry must reach the composition door and the ir bound-field gate: %v", entryErr)
		}
		if strings.Contains(entryErr.Error(), "registered group slice") ||
			strings.Contains(entryErr.Error(), "L-13.5; correlated-hop") {
			t.Fatalf("the group door must not answer an adapter-bearing sequence: %v", entryErr)
		}
		_, directErr := renderGroupPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if directErr == nil || !strings.Contains(directErr.Error(),
			"related-occurrence Group adapter is a pg-sql-16 capability gap") {
			t.Fatalf("the direct door must keep the registered L-13.5 defense-in-depth refusal: %v", directErr)
		}
	})

	group13AliveControl(t)
}

// TestGroupDuplicateNameRefusals pins the L-13.11/L-13.13 name laws that
// need the whole projection/column set mutated together.
func TestGroupDuplicateNameRefusals(t *testing.T) {
	// Duplicate/unregistered names: the ir schema-equality gate refuses
	// first (name map and column-count arms stay the door's
	// belt-and-braces re-reads, L-13.11/L-13.13).
	t.Run("duplicate-projection-names", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		last := len(plan.Operations) - 1
		dup := plan.Operations[last].Return.Projections[0]
		plan.Operations[last].Return.Projections = append(plan.Operations[last].Return.Projections, dup)
		requireGroup13Refusal(t, plan, "is empty or duplicated")
	})
	t.Run("duplicate-final-column", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		plan.StageSequence.FinalColumns = append(plan.StageSequence.FinalColumns,
			plan.StageSequence.FinalColumns[0])
		requireGroup13Refusal(t, plan, "final output schema differs from the final stage")
	})
	t.Run("unregistered-final-column", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		plan.StageSequence.FinalColumns = append(plan.StageSequence.FinalColumns,
			ir.PhysicalStageColumn{ID: "ghost", Name: "ghost", Kind: "string", Cardinality: "optional_one", Nullable: true})
		requireGroup13Refusal(t, plan, "final output schema differs from the final stage")
	})
	t.Run("key-output-without-stable-id", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		for i := range plan.StageSequence.Stages[0].OutputColumns {
			if plan.StageSequence.Stages[0].OutputColumns[i].Name == "status_group" {
				plan.StageSequence.Stages[0].OutputColumns[i].ID = ""
			}
		}
		// The ir validator demands stable IDs; the door re-read must fire
		// first with the registered identity message.
		_, err := renderGroupPhysicalPlan(plan, mustBinding(t))
		if err == nil {
			t.Fatal("must refuse a key output without a stable column ID")
		}
	})
	t.Run("whole-document-direct-value", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		last := len(plan.Operations) - 1
		plan.Operations[last].Return.Projections = append(plan.Operations[last].Return.Projections,
			ir.PhysicalProjection{Name: "doc", Value: ir.PhysicalValue{Variable: "root"}})
		requireGroup13Refusal(t, plan, "scalar projection")
	})
	t.Run("composite-input-refused", func(t *testing.T) {
		// A VALID single-payload OBJECT expression projection (the
		// objects.go objObjectExpr shape) passes ir, classifies as
		// extractionObject (selectors.go:330), and the slice refuses
		// composites as group inputs (L-13.11, the lowering's
		// public-scalar gate).
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		last := len(plan.Operations) - 1
		objExpr := ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{{
				Name: "gender",
				Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Value:        &ir.PhysicalValue{Variable: "root", Path: []string{"payload", "gender"}},
				},
			}}},
		}
		plan.Operations[last].Return.Projections = append(plan.Operations[last].Return.Projections,
			ir.PhysicalProjection{Name: "obj", Expression: &objExpr})
		stage := &plan.StageSequence.Stages[0]
		stage.Group.Aggregates[0] = ir.PhysicalStageGroupAggregate{
			Operation: "COUNT_DISTINCT", InputColumn: "obj", Output: "rows", Variable: stage.Group.Aggregates[0].Variable,
			InputKind: "STRING", OutputKind: "integer",
		}
		// The ir public-scalar gate refuses first; the door's composite
		// classification arm (materializeGroupSource) is the belt.
		requireGroup13Refusal(t, plan, "is not a public scalar input")
	})
	t.Run("unit-normalized-refused", func(t *testing.T) {
		// A CONTRACT-VALID pg-sql-21 unit-normalized RETURN projection
		// (the sql21UnitProjection shape lower emits — passes ir and
		// classifyUnitNormalizedProjection, classifying as
		// extractionUnitNormalized at selectors.go:334-339). The read
		// spine registers the kind (unit_normalization.go via
		// renderReturn); the closed GROUP slice must refuse it at the
		// materialize arm — before the pg-sql-21 merge it fell through
		// with an EMPTY expression and degraded g_src into an untyped
		// syntax error (review finding PRRT_kwDOS-yV5s62Cc1D).
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		last := len(plan.Operations) - 1
		plan.Operations[last].Return.Projections = append(plan.Operations[last].Return.Projections,
			sql21UnitProjection(t, nil))
		requireGroup13Refusal(t, plan, "unit normalization belongs to the read spine registration (unit_normalization.go), not the closed GROUP slice")
	})
}

// TestGroupEntryDispatchEquality pins the door contract: for the plan
// classes the group door owns, both entry points refuse with the identical
// message (validateGroupPlanSurface is the single contract behind both).
func TestGroupEntryDispatchEquality(t *testing.T) {
	plan := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	plan.StageSequence.Stages[0].Group.MissingKeyPolicy = "NOPE"
	_, entryErr := RenderPhysicalPlan(plan, mustBinding(t))
	_, dispatchErr := renderGroupPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if entryErr == nil || dispatchErr == nil || entryErr.Error() != dispatchErr.Error() {
		t.Fatalf("entry/dispatch refusal mismatch: entry=%v dispatch=%v", entryErr, dispatchErr)
	}
	// Door scoping: a sequence whose stage carries a FOREIGN kind no
	// longer mentions GROUP, so the group door must not dispatch — the
	// plan reaches the merged spine/ir refusals with no group-slice
	// wording anywhere.
	spine := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	spine.StageSequence.Stages[0].Kind = ir.PhysicalStagePivotOp
	spine.StageSequence.Stages[0].Group = nil
	if groupPlanMentionsStage(spine) {
		t.Fatal("a PIVOT-kind sequence must not trigger the group door")
	}
	_, err := RenderPhysicalPlan(spine, mustBinding(t))
	if err == nil {
		t.Fatal("the non-group sequence must be refused")
	}
	if strings.Contains(err.Error(), "registered group slice") {
		t.Fatalf("the group door must not answer a non-GROUP sequence: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Structural laws over group.go itself.
// ---------------------------------------------------------------------------

func TestGroupStructuralLaws(t *testing.T) {
	source := readGroup13Source(t, "group.go")

	// Wave 1 registers NO trim site: the btrim-text leg of the package
	// trim-set law keeps applying to group.go verbatim (coordinator
	// ruling 2026-11-11, Q3: the exemption is for wave 2's ONE blank-gate
	// constant), and the codedGroupTrimCharacters constant-use check keeps
	// applying to every CODE line of this file (documentation may name
	// the constant, exactly as the package law at
	// TestCodedGroupTrimSetStructuralLaw scans).
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if strings.Contains(line, "btrim(") {
			t.Fatalf(`group.go must not bind btrim in wave 1 (wave 2 pre-approval covers the ONE groupTrimCharacters gate only): %s`, trimmed)
		}
		if strings.Contains(line, "codedGroupTrimCharacters") {
			t.Fatalf("group.go must never bind the coded-group trim constant: %s", trimmed)
		}
	}
	if got := strings.Count(source, "const groupTrimCharacters"); got != 1 {
		t.Fatalf("groupTrimCharacters declarations = %d, want 1", got)
	}

	// Wave 1 registers NO UNNEST render arm (verifier 156 advisory N-2,
	// 2026-11-11, final form): forbidden-fragment law over the group
	// source — group.go carries ZERO references to the spine's
	// lateral-expansion emitter (its symbol is a "render"+"Unnest"
	// built here so the ban works with the symbol alive in
	// unnest.go/render.go, where the pg-sql-09 SPINE dispatch owns it —
	// deletion was retracted by the coordinator's stop-prevention
	// relay). The validateGroupOperations L-13.9 refusal is the single
	// UNNEST gate for the group slice; wave 2 registers its own arm
	// when the CodingKey slice opens.
	if got := strings.Count(source, "render"+"Unnest"); got != 0 {
		t.Fatalf("group.go lateral-expansion emitter references = %d, want 0 (L-13.9 refusal is the single UNNEST gate)", got)
	}

	// The materializeGroupSource projection switch is exhaustive: the
	// pg-sql-21 unit-normalized kind carries an explicit refusal and a
	// catch-all default returns a typed refusal, so a future classifier
	// kind can never materialize with an empty expression (the
	// fallthrough the unit arm was missing — review finding
	// PRRT_kwDOS-yV5s62Cc1D; mirrored law in selectors.go, unnest.go,
	// objects.go consumers).
	start := strings.Index(source, "func (r *planRenderer) materializeGroupSource")
	end := strings.Index(source[start:], "\nfunc groupResolveColumn")
	if start < 0 || end < 0 {
		t.Fatal("materializeGroupSource boundary not found for the switch-exhaustiveness law")
	}
	switchBody := source[start : start+end]
	for _, required := range []string{
		"case extractionUnitNormalized:",
		"return nil, fmt.Errorf(\"%w: group source projection %q is a unit-normalized extraction",
		"default:",
		"return nil, fmt.Errorf(\"%w: group source projection %q is not a registered projection arm",
	} {
		if !strings.Contains(switchBody, required) {
			t.Fatalf("materializeGroupSource switch is missing the required arm %q", required)
		}
	}

	// The one emitter per registered shape: the ALL/ONE emitter is
	// declared exactly once (it is the Q4 shared half).
	if got := strings.Count(source, "func groupRowValueExpressionFor"); got != 1 {
		t.Fatalf("ALL/ONE emitter declarations = %d, want 1 (the shared Q4 emitter)", got)
	}

	// The dispatch door is the one additive hook; no other package file
	// may dispatch GROUP rendering (the spine default arms stay the only
	// other path, and they refuse).
	for _, file := range []string{"render.go", "coded_group.go", "graph.go", "unnest.go", "unpivot.go"} {
		other := readGroup13Source(t, file)
		if strings.Contains(other, "renderGroupPhysicalPlan") && file != "render.go" {
			t.Fatalf("%s must not dispatch the group slice", file)
		}
	}
	if got := strings.Count(readGroup13Source(t, "render.go"), "renderGroupPhysicalPlan("); got != 1 {
		t.Fatalf("render.go group door calls = %d, want 1", got)
	}
}

func readGroup13Source(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// Live: the owned verify_sql13v1_* namespace (TestMain delta audit).
// ---------------------------------------------------------------------------

func newGroup13Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "verify_sql13v1")
}

func group13RenderSvc(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan(%s): %v", svc.schema, err)
	}
	return rendered
}

var (
	group13FixturesMu      sync.Mutex
	group13TrackedFixtures = map[string]bool{}
)

func group13TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	group13FixturesMu.Lock()
	defer group13FixturesMu.Unlock()
	group13TrackedFixtures[database] = true
	group13TrackedFixtures[svc.schema] = true
}

func group13SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify_sql13v1_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_sql13v1_%'`)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %v", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// group13RunDeltaAudit wraps the composed audit chain with a delta-scoped
// audit over the disjoint verify_sql13v1_* namespace: every resource born
// during this run and still surviving at the end must have been tracked by
// a live test here, and none may survive at all (the D5 starts-empty
// assertion covers the namespace the ticket owns alone).
func group13RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := group13SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: pre-snapshot found %d surviving verify_sql13v1_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := group13SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	group13FixturesMu.Lock()
	defer group13FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if group13TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	survivors, err := group13SnapshotOwnedResources(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-13 delta audit: %d verify_sql13v1_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

func group13Insert(t *testing.T, svc *pgir02Service, logicalID, project, generation, authPath string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, project, generation, "Observation", logicalID, authPath, payload)
}

// group13StatusRow plants a payload with the status key only — the
// valueQuantity path is MISSING (the AQL missing-attribute reading).
func group13StatusRow(status string) map[string]any {
	return map[string]any{"status": status}
}

// group13ValueOnlyRow plants a payload with only the nested value — the
// status path is MISSING (the null-STRING-key rows of the GROUP policy).
func group13ValueOnlyRow(v any) map[string]any {
	return map[string]any{"valueQuantity": map[string]any{"value": v}}
}

// group13ValueRow nests the value under valueQuantity.value to match the
// fixture's field expression root.valueQuantity.value; the value column
// extracts that path (a payload storing a bare "value" key reads NULL).
// A nil value stores an explicit JSON null — the D-13f partner of the
// missing-attribute row: both must read SQL NULL alike.
func group13ValueRow(status string, v any) map[string]any {
	return map[string]any{"status": status, "valueQuantity": map[string]any{"value": v}}
}

// group13MainFixture plants the ticket's acceptance table: independent
// groups with STRING + DECIMAL keys, a missing-attribute row, a stored
// JSON-null row (the D-13f both-read-null equivalence), duplicate rows,
// and three out-of-scope witnesses (denied path, old generation, foreign
// project) that must never group.
func group13MainFixture(t *testing.T, svc *pgir02Service) {
	t.Helper()
	group13Insert(t, svc, "o-1", group13Project, group13Generation, group13AuthPath, group13ValueRow("final", 2))
	group13Insert(t, svc, "o-2", group13Project, group13Generation, group13AuthPath, group13ValueRow("final", 2))
	group13Insert(t, svc, "o-3", group13Project, group13Generation, group13AuthPath, group13ValueRow("final", 2.5))
	group13Insert(t, svc, "o-4", group13Project, group13Generation, group13AuthPath, group13ValueRow("amended", 3))
	group13Insert(t, svc, "o-5", group13Project, group13Generation, group13AuthPath, group13StatusRow("final"))
	group13Insert(t, svc, "o-6", group13Project, group13Generation, group13AuthPath, group13ValueOnlyRow(5))
	group13Insert(t, svc, "o-7", group13Project, group13Generation, group13AuthPath, group13ValueRow("final", nil))
	group13Insert(t, svc, "w-denied", group13Project, group13Generation, "FHIR/Hidden", group13ValueRow("final", 2))
	group13Insert(t, svc, "w-oldgen", group13Project, "p13-old-gen", group13AuthPath, group13ValueRow("final", 2))
	group13Insert(t, svc, "w-foreign", "p13-foreign", group13Generation, group13AuthPath, group13ValueRow("final", 2))
	// Scope witness: the seven acceptance rows are really planted.
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3`,
		group13Project, group13Generation, group13AuthPath); rows[0][0] != "7" {
		t.Fatalf("scope witness: acceptance row count = %v, want 7", rows)
	}
}

// group13KeyedRows are the acceptance rows of the full keyed plan: the
// two keys, the seven reducers, and the stable identity, in FinalColumns
// order. Order: Arango's ASC lattice with null keys FIRST (registered),
// then value keys null-first, then the identity COLLATE "C" refinement.
func group13KeyedRows(t *testing.T, plan ir.PhysicalPlan) [][]string {
	t.Helper()
	null := "<null>"
	return [][]string{
		{null, "5", "1", "1", "0", "5", "5", "5", "5",
			group13Identity(t, plan, []group13Member{group13NullMember("status_group"), group13NumericMember("value_group", "5")})},
		{"amended", "3", "1", "1", "1", "3", "3", "3", "3",
			group13Identity(t, plan, []group13Member{group13TextMember("status_group", "amended"), group13NumericMember("value_group", "3")})},
		{"final", null, "2", "0", "1", null, null, null, null,
			group13Identity(t, plan, []group13Member{group13TextMember("status_group", "final"), group13NullMember("value_group")})},
		{"final", "2", "2", "2", "1", "4", "2", "2", "2",
			group13Identity(t, plan, []group13Member{group13TextMember("status_group", "final"), group13NumericMember("value_group", "2")})},
		{"final", "2.5", "1", "1", "1", "2.5", "2.5", "2.5", "2.5",
			group13Identity(t, plan, []group13Member{group13TextMember("status_group", "final"), group13NumericMember("value_group", "2.5")})},
	}
}

func TestGroupLiveExactGroupsMultiset(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13MainFixture(t, svc)

	plan := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	rendered := group13RenderSvc(t, svc, plan)
	for _, leaked := range []string{group13Project, group13Generation, group13AuthPath, "final", "amended", "Observation"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, rendered.SQL)
		}
	}

	// F-11 (the pg-sql-10 fold-barrier law, applied to the IEEE band
	// arms of SUM/MEAN per coordinator ruling 2026-11-11 Q1): EXPLAIN —
	// the plan-time leg — completes under BOTH plan_cache_modes before
	// any execution.
	group13ExplainBothModes(t, svc, rendered)

	rows := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(rows, group13KeyedRows(t, plan)) {
		t.Fatalf("grouped rows mismatch:\n got %v\nwant %v", rows, group13KeyedRows(t, plan))
	}

	// Determinism across executions: identical rows.
	if repeated := svc.runRendered(t, rendered); !reflect.DeepEqual(repeated, rows) {
		t.Fatalf("grouping changed between executions:\n%v\n%v", rows, repeated)
	}

	// Out-of-scope witnesses never grouped: every row key is one of the
	// acceptance keys (the (final,2) count above already proves the
	// witnesses did not contribute; assert the identity uniqueness too).
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row[9]] {
			t.Fatalf("duplicate row identity %q", row[9])
		}
		seen[row[9]] = true
	}

	// EXCLUDE (alive control — the flip is load-bearing: with the
	// conjunct removed the null-key groups reappear): the two null-key
	// groups vanish, every other group survives untouched.
	excludePlan := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyExclude)
	excluded := svc.runRendered(t, group13RenderSvc(t, svc, excludePlan))
	if len(excluded) != 3 {
		t.Fatalf("EXCLUDE rows = %v, want the three full-key groups", excluded)
	}
	for _, row := range excluded {
		if row[0] == "<null>" || row[1] == "<null>" {
			t.Fatalf("EXCLUDE kept a null-key group: %v", row)
		}
	}

	// ERROR (alive control — the guard is candidate-positioned): the
	// null-key rows raise the canonical tag through the fold barrier,
	// even though the GROUP-policy result over the same data is
	// non-empty.
	errorPlan := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyError)
	errorRendered := group13RenderSvc(t, svc, errorPlan)
	group13ExpectRaise(t, svc, errorRendered, groupMissingKeyTag)

	// Preview limit: the registered order's first two groups only.
	limited := group13KeyedPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	limited.StageSequence.PreviewLimitBindKey = "limit"
	limited.BindVars["limit"] = 2
	limitedRows := svc.runRendered(t, group13RenderSvc(t, svc, limited))
	if len(limitedRows) != 2 || limitedRows[0][0] != "<null>" || limitedRows[1][0] != "amended" {
		t.Fatalf("preview limit must keep the registered order's first two groups: %v", limitedRows)
	}

	// Mutation probe: a newly planted duplicate lifts (final,2) rows 2→3.
	group13Insert(t, svc, "o-8", group13Project, group13Generation, group13AuthPath, group13ValueRow("final", 2))
	mutated := svc.runRendered(t, rendered)
	if mutated[3][2] != "3" {
		t.Fatalf("mutation probe: (final,2) rows never became 3: %v", mutated)
	}
}

func group13Args(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, len(rendered.Args))
	for i, parameter := range rendered.Args {
		args[i] = parameter.Value
	}
	return args
}

// group13ExpectRaise executes a statement to completion and asserts the
// canonical tag came back through the fold barrier's 22P02 cast. pgx
// defers execution to the first Next, so a barrier raise surfaces on the
// iterator, never on Query — the rows are drained and closed here (an
// undrained rows leaks the pinned connection and wedges DROP DATABASE
// cleanup; measured).
// group13ExplainBothModes is the uniform F-11 application (the pg-sql-10
// fold-barrier law, verifier 156 advisory N-4, 2026-11-11: coverage must
// span every numeric-reading class, not just the overflow case): EXPLAIN —
// the plan-time leg — completes under BOTH plan_cache_modes before any
// execution. An arm that folds into a raising constant fails HERE, never
// at execution; guard casts stay unfolded because current_setting() keeps
// them stable.
func group13ExplainBothModes(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan) {
	t.Helper()
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		conn, err := svc.iso.Acquire(svc.ctx)
		if err != nil {
			t.Fatalf("acquire pinned connection: %v", err)
		}
		if _, err := conn.Exec(svc.ctx, "SET plan_cache_mode = "+mode); err != nil {
			conn.Release()
			t.Fatalf("pin plan_cache_mode %s: %v", mode, err)
		}
		args := make([]any, len(rendered.Args))
		for i, parameter := range rendered.Args {
			args[i] = parameter.Value
		}
		if _, err := conn.Exec(svc.ctx, "EXPLAIN "+rendered.SQL, args...); err != nil {
			conn.Release()
			t.Fatalf("plan-time EXPLAIN raised under %s: %v", mode, err)
		}
		conn.Release()
	}
}

func group13ExpectRaise(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan, tag string) {
	t.Helper()
	rows, err := svc.iso.Query(svc.ctx, rendered.SQL, group13Args(rendered)...)
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	if err == nil {
		t.Fatalf("statement must raise %q through the barrier", tag)
	}
	scrubbed := svc.scrub(err)
	if !strings.Contains(scrubbed, tag) || !strings.Contains(scrubbed, "22P02") {
		t.Fatalf("must raise %q through the barrier cast: %v", tag, scrubbed)
	}
}

// TestGroupLiveValueTypeWitnesses pins the two type witnesses live with
// alive controls: the healthy fixture renders, then one poisoned row
// raises the canonical tag through the barrier — key witness and value
// pool witness separately, and the InputMany non-array IS_ARRAY parity.
func TestGroupLiveValueTypeWitnesses(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13MainFixture(t, svc)

	// Key witness: the DECIMAL key over a string-stored value raises the
	// KEY tag. The plan carries only COUNT_ROWS (no input column), so on
	// the value column the KEY typeof witness is the ONLY conjunct — the
	// raise is unambiguous regardless of AND evaluation order. Alive
	// control: the same statement over the healthy rows first renders.
	keyPlan := group13LoweredPlan(t, group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys: []recipe.ConstructionGroupKey{
			{InputColumnID: "status", OutputColumnID: "status_group"},
			{InputColumnID: "value", OutputColumnID: "value_group"},
		},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "value_group", Name: "value_group", Type: "decimal", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
		},
	})
	rendered := group13RenderSvc(t, svc, keyPlan)
	group13ExplainBothModes(t, svc, rendered)
	if healthy := svc.runRendered(t, rendered); len(healthy) != 5 {
		t.Fatalf("key-witness alive control: healthy rows = %v, want the five groups", healthy)
	}
	group13Insert(t, svc, "p-key", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "poisoned", "valueQuantity": map[string]any{"value": "abc"}})
	group13ExpectRaise(t, svc, rendered, groupKeyTypeTag)

	// Value-pool witness: the SUM over the same poison raises the VALUE
	// tag on a plan whose key is STRING-only (the key witness would not
	// catch the value pool here).
	valuePlan := group13LoweredPlan(t, group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys:   []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupSum, InputColumnID: "value", OutputColumnID: "sum_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "sum_id", Name: "total", Type: "decimal", Nullable: true},
		},
	})
	valueRendered := group13RenderSvc(t, svc, valuePlan)
	// F-11 uniformity (advisory N-4): the raise-bearing plans must ALSO
	// survive plan time in both modes — the guard casts stay unfolded
	// (current_setting keeps them stable), so EXPLAIN completes even
	// where execution raises.
	group13ExplainBothModes(t, svc, valueRendered)
	group13ExpectRaise(t, svc, valueRendered, groupValueTypeTag)
}

func TestGroupLiveRowValues(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	// ALL over one group: {3,1,3,missing,null} → [1,3] (distinct,
	// sorted, nulls and missing excluded, row_values.go:38,45 parity);
	// an all-missing group reads []; the ONE policy reads the single
	// distinct value and raises the multi-value tag otherwise.
	group13Insert(t, svc, "rv-a", group13Project, group13Generation, group13AuthPath, group13ValueRow("rv", 3))
	group13Insert(t, svc, "rv-b", group13Project, group13Generation, group13AuthPath, group13ValueRow("rv", 1))
	group13Insert(t, svc, "rv-c", group13Project, group13Generation, group13AuthPath, group13ValueRow("rv", 3))
	group13Insert(t, svc, "rv-d", group13Project, group13Generation, group13AuthPath, group13StatusRow("rv"))
	group13Insert(t, svc, "rv-e", group13Project, group13Generation, group13AuthPath, group13ValueRow("rv", nil))
	group13Insert(t, svc, "rv-f", group13Project, group13Generation, group13AuthPath, group13StatusRow("rvnull"))

	allPlan := group13RowValuePlan(t, recipe.ConstructionRowValueAll, "array")
	allRendered := group13RenderSvc(t, svc, allPlan)
	group13ExplainBothModes(t, svc, allRendered)
	rows := svc.runRendered(t, allRendered)
	if len(rows) != 2 {
		t.Fatalf("ALL rows = %v, want two groups", rows)
	}
	// Columns: status_group, rows, value_values, identity.
	if rows[0][0] != "rv" || rows[0][2] != `[1 3]` {
		t.Fatalf("ALL distinct-sorted null-free array mismatch: %v", rows[0])
	}
	if rows[1][0] != "rvnull" || rows[1][2] != `[]` {
		t.Fatalf("all-missing group must read the empty array: %v", rows[1])
	}
}

func TestGroupLiveRowValueOne(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13Insert(t, svc, "ro-a", group13Project, group13Generation, group13AuthPath, group13ValueRow("same", 7))
	group13Insert(t, svc, "ro-b", group13Project, group13Generation, group13AuthPath, group13ValueRow("same", 7))
	group13Insert(t, svc, "ro-c", group13Project, group13Generation, group13AuthPath, group13StatusRow("onlynull"))

	plan := group13RowValuePlan(t, recipe.ConstructionRowValueOne, "decimal")
	rendered := group13RenderSvc(t, svc, plan)
	group13ExplainBothModes(t, svc, rendered)
	rows := svc.runRendered(t, rendered)
	// Columns: status_group, rows, value_one, identity.
	if len(rows) != 2 || rows[0][0] != "onlynull" || rows[0][2] != "<null>" || rows[1][0] != "same" || rows[1][2] != "7" {
		t.Fatalf("ONE rows = %v, want (onlynull,<null>) then (same,7)", rows)
	}

	// Alive control / mutation: a second DISTINCT member raises the
	// canonical multi-value tag through the barrier.
	group13Insert(t, svc, "ro-d", group13Project, group13Generation, group13AuthPath, group13ValueRow("same", 8))
	group13ExpectRaise(t, svc, rendered, groupRowValueMultipleTag)
}

// TestGroupLiveRowValueOneString pins the ONE policy over a STRING-kind
// member live: the CASE success branch is text, and the barrier raise
// arm must type-match it (an uncast integer ELSE arm raised 42804 on
// PG18 before groupRowValueRaiseArm).
func TestGroupLiveRowValueOneString(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13Insert(t, svc, "rs-a", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "same", "id": "s-1"})
	plan := group13LoweredPlan(t, group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys:   []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
		},
		rowValues: []recipe.ConstructionRowValue{
			{InputColumnID: "resource_id", OutputColumnID: "id_one", Policy: recipe.ConstructionRowValueOne},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "id_one", Name: "id_one", Type: "string", Nullable: true},
		},
	})
	rendered := group13RenderSvc(t, svc, plan)
	group13ExplainBothModes(t, svc, rendered)
	if rows := svc.runRendered(t, rendered); len(rows) != 1 || rows[0][2] != "s-1" {
		t.Fatalf("ONE over one STRING member: %v, want the single id", rows)
	}
	group13Insert(t, svc, "rs-b", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "same", "id": "s-2"})
	group13ExpectRaise(t, svc, rendered, groupRowValueMultipleTag)
}

func TestGroupLiveRowValueInputMany(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13Insert(t, svc, "mn-a", group13Project, group13Generation, group13AuthPath, map[string]any{"status": "many", "values": []any{"b", "a", "b"}})
	group13Insert(t, svc, "mn-b", group13Project, group13Generation, group13AuthPath, map[string]any{"status": "many", "values": []any{"c"}})

	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	appendGroup13ManyRowValue(t, &plan, "ALL", "STRING")
	rendered := group13RenderSvc(t, svc, plan)
	group13ExplainBothModes(t, svc, rendered)
	rows := svc.runRendered(t, rendered)
	// Columns follow FinalColumns order: status_group, rows, identity,
	// then the appended pool column (the lowering stamps identity before
	// hand-attached row values).
	if len(rows) != 1 || rows[0][0] != "many" || rows[0][3] != `[a b c]` {
		t.Fatalf("InputMany ALL must flatten one layer and dedupe across rows: %v", rows)
	}

	// Alive control: a stored NON-ARRAY raises the value-type tag at the
	// g_rows row position (row_values.go:79-83 IS_ARRAY parity).
	group13Insert(t, svc, "mn-c", group13Project, group13Generation, group13AuthPath, map[string]any{"status": "many", "values": "oops"})
	group13ExpectRaise(t, svc, rendered, groupValueTypeTag)
}

// TestGroupLiveRowValueInputManyDecimal pins the verifier F-3 repair
// (2026-11-11 relay): the DECIMAL pool's mistyped-member raise must
// carry CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH. The old pool WHERE put
// the member's ::numeric cast in the null-filter conjunct; PG gives no
// left-to-right guarantee between conjuncts, so a raw 22P02 could win
// the race against the typeof guard. The WHERE now carries no cast at
// all (the null filter rides at the jsonb level, matching the STRING
// pool's already-safe shape), so the guard is the only raise source.
func TestGroupLiveRowValueInputManyDecimal(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13Insert(t, svc, "dm-a", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "pool", "values": []any{1, 2, 2}})
	group13Insert(t, svc, "dm-b", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "pool", "values": []any{3}})

	allPlan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	appendGroup13ManyRowValue(t, &allPlan, "ALL", "DECIMAL")
	allRendered := group13RenderSvc(t, svc, allPlan)
	group13ExplainBothModes(t, svc, allRendered)
	rows := svc.runRendered(t, allRendered)
	if len(rows) != 1 || rows[0][3] != `[1 2 3]` {
		t.Fatalf("DECIMAL ALL pool must dedupe and sort numerically: %v", rows)
	}

	// F-3 cell: a string member inside a DECIMAL pool raises the TAGGED
	// value-type 22P02 (guard decides; the cast never runs for it),
	// never a raw cast error — group13ExpectRaise asserts both the tag
	// and the SQLSTATE.
	group13Insert(t, svc, "dm-c", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "pool", "values": []any{1, "oops"}})
	group13ExpectRaise(t, svc, group13RenderSvc(t, svc, allPlan), groupValueTypeTag)

	// The ONE pool rides the same cast-free WHERE; a clean pool renders
	// its single value.
	oneSvc := newGroup13Service(t)
	group13TrackFixtures(t, oneSvc)
	group13Insert(t, oneSvc, "dn-a", group13Project, group13Generation, group13AuthPath,
		map[string]any{"status": "pool", "values": []any{7}})
	onePlan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	appendGroup13ManyRowValue(t, &onePlan, "ONE", "DECIMAL")
	oneRendered := group13RenderSvc(t, oneSvc, onePlan)
	group13ExplainBothModes(t, oneSvc, oneRendered)
	oneRows := oneSvc.runRendered(t, oneRendered)
	if len(oneRows) != 1 || oneRows[0][3] != "7" {
		t.Fatalf("DECIMAL ONE pool must read the single numeric value: %v", oneRows)
	}
}

// TestGroupLiveDoubleSumOverflow pins the verifier F-2 repair (2026-11-11
// relay). The old design accumulated float8 and gated non-finite sums
// post-hoc — unreachable: PG18.6 raises 22003 the moment the RUNNING
// float8 SUM overflows (the isolated repro SUM of two 1.7e308 members,
// fault-controlled below), while every member is finite and passes the
// member-level band, so PG aborted where AQL answered. Accumulation is
// now EXACT in numeric (F-8-clamped members — the clamp is inert for
// double-representable values), bounded to double by the group band
// cast, so every arm is raise-free, and the readings match the live
// ArangoDB 3.12.4-3 oracle (verifier 156, coordinator ruling
// 2026-11-11, D-13h): native SUM over storable members reads 0 when
// the running sum leaves the double domain, and representable sums
// read the correctly-rounded double (the seventeen class carries the
// D-13i registered refinement) — the expected values below are that
// oracle, pinned by this in-repo test.
func TestGroupLiveDoubleSumOverflow(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)

	// Fault control (156's class is real on this PG18.6): the raw float8
	// overflow raises 22003 — this is what the old reducer form inherited.
	conn, err := svc.iso.Acquire(svc.ctx)
	if err != nil {
		t.Fatalf("acquire pinned connection: %v", err)
	}
	_, err = conn.Exec(svc.ctx,
		`SELECT SUM(x) FROM (VALUES ('1.7e308'::double precision), ('1.7e308'::double precision)) v(x)`)
	if err == nil || !strings.Contains(err.Error(), "22003") {
		conn.Release()
		t.Fatalf("fault control: float8 SUM overflow must raise 22003 on PG18.6, got %v", err)
	}
	conn.Release()

	// Fixtures (oracle classes per the archived 156 probe): "pair" is the
	// isolated repro (two 1.7e308, exact sum leaves the double band →
	// 0/0); "crowd" is member-vs-cumulative overflow (twenty 1e307 —
	// every member finite, exact sum 2e308 leaves the band → 0/0);
	// "kept" is exactly representable (two 1e307 → 2e307, mean 1e307);
	// "seventeen" is the representable-class probe (17x1e307): PG reads
	// the correctly-rounded double of the exact sum (1.7e+308); the
	// archived AQL stepped-IEEE SUM probe read the two-bit-increment lower
	// 1.6999999999999995e308 — the D-13i registered refinement in the
	// assertions covers the split (register, don't mimic).
	fixture := group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys: []recipe.ConstructionGroupKey{
			{InputColumnID: "status", OutputColumnID: "status_group"},
		},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
			{Operation: recipe.ConstructionGroupSum, InputColumnID: "value", OutputColumnID: "sum_id"},
			{Operation: recipe.ConstructionGroupMean, InputColumnID: "value", OutputColumnID: "mean_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "sum_id", Name: "total", Type: "decimal", Nullable: true},
			{ID: "mean_id", Name: "mean", Type: "decimal", Nullable: true},
		},
	}
	plan := group13LoweredPlan(t, fixture)
	for i := 0; i < 2; i++ {
		group13Insert(t, svc, fmt.Sprintf("ovf-p%d", i), group13Project, group13Generation, group13AuthPath, group13ValueRow("pair", 1.7e308))
	}
	for i := 0; i < 20; i++ {
		group13Insert(t, svc, fmt.Sprintf("ovf-c%d", i), group13Project, group13Generation, group13AuthPath, group13ValueRow("crowd", 1e307))
	}
	for i := 0; i < 2; i++ {
		group13Insert(t, svc, fmt.Sprintf("ovf-k%d", i), group13Project, group13Generation, group13AuthPath, group13ValueRow("kept", 1e307))
	}
	for i := 0; i < 17; i++ {
		group13Insert(t, svc, fmt.Sprintf("ovf-s%d", i), group13Project, group13Generation, group13AuthPath, group13ValueRow("seventeen", 1e307))
	}

	rendered := group13RenderSvc(t, svc, plan)

	// F-11 law (pg-sql-10 fold-barrier): EXPLAIN — the plan-time leg —
	// completes under BOTH plan_cache_modes before any execution: no arm
	// of the bounded accumulator folds into a raising constant.
	group13ExplainBothModes(t, svc, rendered)

	// Oracle-attested parity (live ArangoDB 3.12.4-3 native SUM/LENGTH,
	// verifier 156's archived probe; coordinator ruling 2026-11-11):
	// any member set whose EXACT sum leaves the double band reads 0/0
	// (pair 2x1.7e308 → 0/0, crowd 20x1e307 → 0/0 — SUM and MEAN 0/n);
	// anything representable reads the correctly-rounded double (kept
	// 2x1e307 → 2e307/1e307; seventeen → 1.7e+308, whose two-bit-increment split
	// from the stepped AQL read is the D-13i registered refinement).
	rows := svc.runRendered(t, rendered)
	if len(rows) != 4 {
		t.Fatalf("four groups expected: %v", rows)
	}
	// D-13i REGISTERED REFINEMENT (verifier 156 reproduced both reads at
	// this lane's head — PG 1.7e+308 hex b76 correctly rounded; live AQL
	// 1.6999999999999995e308 hex b74 stepped, Go replica bit-exact —
	// two bit increments apart; coordinator ruling 2026-11-11):
	// Correct-rounding of exact numeric accumulation may read a small number of bit increments above AQL stepped-IEEE native SUM for non-binary-representable members (measured: 2 increments on the 17x1e307 fixture, PG b76 vs AQL b74); PG reading is the correctly-rounded result; registered refinement D-13i per coordinator ruling 2026-11-11.
	// The cell asserts the higher-precision PG reading; the stepped AQL
	// value is an accumulation artifact, not an exactness target —
	// register, don't mimic.
	sevSum, sevErr := strconv.ParseFloat("1.7e308", 64)
	if sevErr != nil {
		t.Fatalf("parse 1.7e308: %v", sevErr)
	}
	want := map[string]struct {
		count           string
		sum, mean       string
		sumVal, meanVal float64
		exact           bool
	}{
		"crowd":     {count: "20", sum: "0", mean: "0"},
		"pair":      {count: "2", sum: "0", mean: "0"},
		"kept":      {count: "2", sumVal: 2e307, meanVal: 1e307, exact: true},
		"seventeen": {count: "17", sumVal: sevSum, meanVal: sevSum / 17, exact: true},
	}
	for _, row := range rows {
		w, ok := want[row[0]]
		if !ok {
			t.Fatalf("unexpected group %q", row[0])
		}
		if row[1] != w.count {
			t.Fatalf("group %s count = %q, want %q: %v", row[0], row[1], w.count, row)
		}
		if !w.exact {
			if row[2] != w.sum || row[3] != w.mean {
				t.Fatalf("overflow-band group %s = %v, want [%s 0 0]", row[0], row, w.count)
			}
			continue
		}
		// Representable sums read the correctly-rounded double; the
		// canonical numeric text is compared through the asserted double
		// value (kept: binary-exact members; seventeen: D-13i).
		sumVal, sumErr := strconv.ParseFloat(row[2], 64)
		meanVal, meanErr := strconv.ParseFloat(row[3], 64)
		if sumErr != nil || meanErr != nil || sumVal != w.sumVal || meanVal != w.meanVal {
			t.Fatalf("representable group %s = %v, want exact %v/%v", row[0], row, w.sumVal, w.meanVal)
		}
	}
}

func TestGroupLiveKeylessGrandTotal(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13Insert(t, svc, "w-only", "p13-foreign", group13Generation, group13AuthPath, group13ValueRow("final", 1))

	// Zero rows in scope: the AQL [null]-padding reading, value for
	// value — COUNT_ROWS 0, ALL [], ONE null — and the ERROR policy over
	// a zero-source query never raises in either backend.
	for _, policy := range []ir.PhysicalStageGroupMissingKeyPolicy{
		ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyError,
	} {
		plan := group13KeylessPlan(t)
		plan.StageSequence.Stages[0].Group.MissingKeyPolicy = policy
		keylessRendered := group13RenderSvc(t, svc, plan)
		group13ExplainBothModes(t, svc, keylessRendered)
		rows := svc.runRendered(t, keylessRendered)
		if len(rows) != 1 || rows[0][0] != "0" || rows[0][1] != "[]" || rows[0][2] != "<null>" {
			t.Fatalf("keyless grand total over zero rows under %s = %v, want one row [0,[],null]", policy, rows)
		}
	}
}

func TestGroupLiveSourceOrderSwap(t *testing.T) {
	svc := newGroup13Service(t)
	group13TrackFixtures(t, svc)
	group13MainFixture(t, svc)

	fixture := group13Fixture{
		policy: recipe.ConstructionGroupMissingKeyGroup,
		keys: []recipe.ConstructionGroupKey{
			{InputColumnID: "status", OutputColumnID: "status_group"},
			{InputColumnID: "value", OutputColumnID: "value_group"},
		},
		aggregates: []recipe.ConstructionGroupAggregate{
			{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
			{Operation: recipe.ConstructionGroupSum, InputColumnID: "value", OutputColumnID: "sum_id"},
		},
		outputs: []recipe.StageColumn{
			{ID: "status_group", Name: "status_group", Type: "string", Nullable: true},
			{ID: "value_group", Name: "value_group", Type: "decimal", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "sum_id", Name: "total", Type: "decimal", Nullable: true},
		},
	}
	planA := group13LoweredPlan(t, fixture)
	swapped := fixture
	swapped.sourceOrder = []string{"resource_id", "value", "status"}
	planB := group13LoweredPlan(t, swapped)
	renderedA := group13RenderSvc(t, svc, planA)
	renderedB := group13RenderSvc(t, svc, planB)
	group13ExplainBothModes(t, svc, renderedA)
	group13ExplainBothModes(t, svc, renderedB)
	rowsA := svc.runRendered(t, renderedA)
	rowsB := svc.runRendered(t, renderedB)
	if !reflect.DeepEqual(rowsA, rowsB) {
		t.Fatalf("swapping the source projection order must not change results (the name→column map must follow names):\nA %v\nB %v", rowsA, rowsB)
	}
	if len(rowsA) != 5 {
		t.Fatalf("expected five groups, got %v", rowsA)
	}
	// Spot pin: the DECIMAL key still reads the quantity value, not the
	// status text (a position accident would misread the value key).
	if rowsB[3][0] != "final" || rowsB[3][1] != "2" || rowsB[3][3] != "4" {
		t.Fatalf("value key misread after swap: %v", rowsB)
	}
}
