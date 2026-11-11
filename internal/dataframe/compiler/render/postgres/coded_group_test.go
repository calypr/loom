package postgres

// pg-sql-19: the coded Group tuple over repeated FHIR Coding values.
// Offline: the registered slice renders the lowered inline CODED_GROUP plan
// (real recipe → semantic → lower, so the tested input is the production
// lowered shape), and the refusal register pins every typed refusal of the
// closed surface with its message verbatim, each paired with the healthy
// base render as the alive control. Live: exact tuple-multiset acceptance
// against literal expected rows on PostgreSQL 18 under the owned
// coded19_it_* namespace (registered with the TestMain delta audit), the
// MissingKeyPolicy GROUP/EXCLUDE/ERROR matrix including the zero-group
// guard cell and its sabotage controls, whitespace/Unicode/byte-order pins,
// and the alive controls (direct-SQL witnesses + insert-then-recount
// mutation probes) so a vacuously-empty grouping can never pass.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
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
	coded19CodingPath = "component[].code.coding[]"
	coded19StepID     = "group_codes"
)

func coded19Output(policy recipe.ConstructionGroupMissingKeyPolicy, project, generation string) (recipe.Output, recipe.RuntimeBindings) {
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	output := recipe.Output{
		Name: "coded_group_rows", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "resource_id", Name: "resource_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: coded19StepID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID: coded19StepID,
					Source: recipe.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: "Observation", CodingPath: coded19CodingPath,
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
					},
					MissingKeyPolicy:     policy,
					SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
					CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}},
		},
	}
	return output, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthResourcePaths: []string{"FHIR/Observation"}}
}

func coded19LoweredPlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy, project, generation string) ir.PhysicalPlan {
	t.Helper()
	output, bindings := coded19Output(policy, project, generation)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
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
	if compiled.Outputs[0].Plan.StageSequence == nil || len(compiled.Outputs[0].Plan.StageSequence.Stages) != 1 ||
		compiled.Outputs[0].Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageCodedGroupOp {
		t.Fatalf("lowered plan is not the inline single-stage CODED_GROUP shape: %#v", compiled.Outputs[0].Plan.StageSequence)
	}
	return ir.ClonePhysicalPlan(compiled.Outputs[0].Plan)
}

// coded19Identity builds the expected stable identity string independently
// of the renderer: the JSON-compact array spelling of the AQL TO_STRING
// member list (construction_coded_group.go:127-129). Each string member is
// JSON-quoted; a nil key member spells null unquoted.
func coded19Identity(t *testing.T, system, version, code *string) string {
	t.Helper()
	member := func(value *string) string {
		if value == nil {
			return "null"
		}
		raw, err := json.Marshal(*value)
		if err != nil {
			t.Fatalf("marshal identity member: %v", err)
		}
		return string(raw)
	}
	parts := []string{
		`"construction"`, `"group_codes"`, `"operation"`, `"CODED_GROUP"`,
		`"occurrence"`, `"base"`, `"path"`, `"component[].code.coding[]"`,
		`"system"`, member(system), `"version"`, member(version), `"code"`, member(code),
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type coded19Row struct {
	system, version, code *string
	count                 string
	identity              string
}

func coded19Rows(t *testing.T, rows [][]string) []coded19Row {
	t.Helper()
	optional := func(cell string) *string {
		if cell == "<null>" {
			return nil
		}
		value := cell
		return &value
	}
	out := make([]coded19Row, 0, len(rows))
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatalf("coded-group row = %#v, want 5 cells", row)
		}
		out = append(out, coded19Row{
			system: optional(row[0]), version: optional(row[1]), code: optional(row[2]),
			count: row[3], identity: row[4],
		})
	}
	return out
}

func coded19Text(value string) *string { return &value }

// ---------------------------------------------------------------------------
// Offline: the registered slice over the lowered plan.
// ---------------------------------------------------------------------------

func TestCodedGroupRenderedSliceContract(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	for _, want := range []string{
		`WITH cg_src AS (`,
		// Expansion shape (AQL parity, construction_coded_group.go:54-75):
		// ONE LEFT JOIN LATERAL wraps the CROSS JOIN LATERAL chain, so the
		// zero-item rule fires once per root, not once per emptied branch.
		`LEFT JOIN LATERAL (SELECT cgx2.elem FROM jsonb_array_elements(CASE WHEN jsonb_typeof(`,
		// Expansion regression pin: the first expansion must reach the
		// first repeated segment by NAME through the payload (AQL
		// attribute step per segment), never unnest the payload root.
		`jsonb_typeof(s.cg_payload #> $`,
		`AS cgx1(elem) CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(`,
		`) AS cgx2(elem)) AS cgexp(elem) ON true`,
		`cgexp.elem ->> $`,
		`SELECT DISTINCT c.cg_root_key,`,
		`COUNT(*) AS cg_count FROM cg_tuples GROUP BY 1, 2, 3`,
		`COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("statement is missing %q:\n%s", want, sql)
		}
	}
	// One wrapping LEFT JOIN only: per-level LEFT JOINs would materialize
	// a NULL candidate per emptied branch (the mixed-branch parity bug).
	if got := strings.Count(sql, "LEFT JOIN LATERAL"); got != 1 {
		t.Fatalf("wrapping LEFT JOIN LATERAL count = %d, want exactly 1:\n%s", got, sql)
	}
	// Group policy keeps every candidate: no WHERE on the dedup step.
	if strings.Contains(sql, " WHERE c.cg_valid") {
		t.Fatalf("GROUP policy must not filter candidates:\n%s", sql)
	}
	// AQL bind syntax never crosses into PostgreSQL text.
	if strings.Contains(sql, "@") {
		t.Fatalf("statement carries AQL bind syntax:\n%s", sql)
	}
	// No plan data in statement text: only registry columns, renderer-owned
	// CTE aliases, and $n parameters.
	for _, leaked := range []string{"proj-a", "gen-1", "FHIR/Observation", "Observation", "component", "coding", "group_codes", coded19CodingPath, "CONSTRUCTION_CODED_GROUP_MISSING_KEY"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text:\n%s", leaked, sql)
		}
	}
	// Path segments and key names bind as text parameters, in extraction
	// order: the single #> per run of scalar segments carries {code}, and
	// the two guarded array expansions read component first.
	if !strings.Contains(sql, `#> $`) {
		t.Fatalf("payload paths must bind through #> text[] parameters:\n%s", sql)
	}
	if got := strings.Count(sql, "btrim("); got != 3 {
		t.Fatalf("btrim gates = %d, want 3 (system+code validity, version key):\n%s", got, sql)
	}
	if got := strings.Count(sql, ` COLLATE "C"`); got != 1 {
		t.Fatalf(`identity COLLATE "C" terms = %d, want 1:\n%s`, got, sql)
	}

	// The declared column contract matches the lowered FinalColumns order
	// (three keys, the root count, and the hidden stable identity).
	want := []string{"code_system", "code_version", "code", "source_records", "__loom_row_id"}
	if !reflect.DeepEqual(rendered.OutputColumns, want) {
		t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
	}

	// Determinism: same plan, byte-identical statement and value-identical
	// arguments.
	second, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("coded-group render is not deterministic: %v", err)
	}
}

func TestCodedGroupPoliciesRender(t *testing.T) {
	group := renderOK(t, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1"))
	if strings.Contains(group.SQL, " WHERE c.cg_valid") || strings.Contains(group.SQL, coded19MissingKeyTagSQLProbe) {
		t.Fatalf("GROUP policy must neither filter nor guard candidates:\n%s", group.SQL)
	}

	exclude := renderOK(t, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyExclude, "proj-a", "gen-1"))
	if !strings.Contains(exclude.SQL, " WHERE c.cg_valid") {
		t.Fatalf("EXCLUDE policy must filter candidates:\n%s", exclude.SQL)
	}
	if strings.Contains(exclude.SQL, guardCast(codedGroupMissingKeyTag)) {
		t.Fatalf("EXCLUDE policy must not carry the ERROR guard:\n%s", exclude.SQL)
	}

	// ERROR renders the ASSERT counterpart at the candidate position over
	// the constant-folding barrier, with the tag carried verbatim.
	errorPlan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, "proj-a", "gen-1")
	errorRendered := renderOK(t, errorPlan)
	for _, want := range []string{
		" WHERE (CASE WHEN c.cg_valid THEN 1 ELSE CAST('" + codedGroupMissingKeyTag + "'",
		guardBarrierGUC,
		" END) > 0",
	} {
		if !strings.Contains(errorRendered.SQL, want) {
			t.Fatalf("ERROR guard is missing %q:\n%s", want, errorRendered.SQL)
		}
	}
}

const coded19MissingKeyTagSQLProbe = "CASE WHEN c.cg_valid THEN 1"

func TestCodedGroupIdentityOrderAndBound(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	rendered := renderOK(t, plan)
	// The AQL terminal re-sort orders the final rows by the stable identity
	// alone (stage_sequence.go:399-403/:446-448): exactly one ORDER BY in
	// the final statement, on the identity concatenation, with the preview
	// LIMIT bound after it.
	orderIndex := strings.LastIndex(rendered.SQL, " ORDER BY ")
	if orderIndex < 0 {
		t.Fatalf("final statement has no ORDER BY:\n%s", rendered.SQL)
	}
	final := rendered.SQL[orderIndex:]
	if !strings.Contains(final, `'[' || '"construction"'`) || !strings.Contains(final, `COALESCE(to_jsonb(g.cg_system)::text, 'null')`) {
		t.Fatalf("final ORDER BY must be the stable identity concatenation:\n%s", final)
	}
	if strings.Contains(final, "cg_system ASC") || strings.Contains(final, "cg_version ASC") {
		t.Fatalf("the unobservable AQL stage-internal sort must not be re-rendered:\n%s", final)
	}

	// With a preview limit bound, the bound LIMIT follows the identity
	// COLLATE "C" order (D6: the AQL terminal LIMIT position).
	limited := ir.ClonePhysicalPlan(plan)
	limited.StageSequence.PreviewLimitBindKey = "limit"
	limited.BindVars["limit"] = 25
	limitedRendered := renderOK(t, limited)
	if m, _ := regexp.MatchString(` COLLATE "C" ASC LIMIT \$[0-9]+$`, limitedRendered.SQL); !m {
		t.Fatalf("the bound preview LIMIT must follow the identity COLLATE \"C\" order:\n%s", limitedRendered.SQL)
	}
	if !strings.HasSuffix(rendered.SQL, ` COLLATE "C" ASC`) {
		t.Fatalf("the unwindowed statement ends in the identity order:\n%s", rendered.SQL)
	}
}

func TestCodedGroupWindowedSourceRegion(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	// The W1 source window renders through the untouched spine window arms:
	// a SORT before the RETURN becomes part of cg_src, and its ORDER BY
	// witnesses stay inside cg_src — never the final identity order.
	sorted := ir.ClonePhysicalPlan(plan)
	last := len(sorted.Operations) - 1
	sorted.Operations = append(sorted.Operations[:last],
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "srclimit"}},
		sorted.Operations[last])
	sorted.BindVars["srclimit"] = 3
	rendered := renderOK(t, sorted)
	srcEnd := strings.Index(rendered.SQL, "), cg_candidates AS (")
	if srcEnd < 0 {
		t.Fatalf("cg_src boundary missing:\n%s", rendered.SQL)
	}
	source := rendered.SQL[:srcEnd]
	if !strings.Contains(source, `r0."logical_id" ASC`) || !strings.Contains(source, `"resource_seq" ASC`) || !strings.Contains(source, "LIMIT $") {
		t.Fatalf("the W1 window must be rendered inside cg_src:\n%s", source)
	}
}

func TestCodedGroupSingleSegmentKeepsFlatLateral(t *testing.T) {
	// A single repeated segment keeps the flat LEFT JOIN LATERAL form
	// (its zero-row behavior already equals the wrapped chain, and the
	// merged golden stays byte-identical); only two or more repeated
	// segments mint the CROSS JOIN LATERAL chain.
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	coded := plan.StageSequence.Stages[0].CodedGroup
	coded.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "coding", Repeated: true}}
	coded.CodingPath = "coding[]"
	plan.BindVars[coded.CodingPathBindKey] = coded.CodingPath
	sql := renderOK(t, plan).SQL
	for _, want := range []string{
		`LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(`,
		`AS cgx1(elem) ON true`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("single-segment statement is missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "CROSS JOIN LATERAL") || strings.Contains(sql, "cgexp") {
		t.Fatalf("single-segment statement must keep the flat form:\n%s", sql)
	}
}

// ---------------------------------------------------------------------------
// Offline: the refusal register — every message pinned verbatim, each with
// the healthy base render as the alive control.
// ---------------------------------------------------------------------------

func TestCodedGroupRefusalRegister(t *testing.T) {
	base := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")

	// Alive control: the base plan renders.
	if _, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(base), mustBinding(t)); err != nil {
		t.Fatalf("alive control (base plan) must render: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(t *testing.T, plan *ir.PhysicalPlan)
		pin     string
		door    bool
		message string
	}{
		{
			name: "second_stage_pg_sql_16_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				second := plan.StageSequence.Stages[0]
				second.ID = "second"
				second.InputStageID = coded19StepID
				plan.StageSequence.Stages = append(plan.StageSequence.Stages, second)
			},
			pin:     "exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap",
			message: "",
		},
		{
			name: "stage_kind_not_coded_group",
			door: true,
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Kind = ir.PhysicalStageGroupOp
			},
			pin: `operation "GROUP" is not part of the registered coded-group slice`,
		},
		{
			name: "coded_group_payload_nil",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].CodedGroup = nil
			},
			pin: "is not part of the registered coded-group slice",
		},
		{
			name: "foreign_stage_payload_present",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group = &ir.PhysicalStageGroup{}
			},
			pin: "CODED_GROUP requires only a coded-group payload",
		},
		{
			name: "document_requery_shape_production_unreachable",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].InputStageID = "not_the_source_projection"
			},
			pin: "the DOCUMENT re-query shape is production-unreachable (recipe/construction.go:665-668)",
		},
		{
			name: "stage_id_differs_from_final",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].ID = "other"
			},
			pin: "the DOCUMENT re-query shape is production-unreachable (recipe/construction.go:665-668)",
		},
		{
			name: "row_values_pg_sql_13_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded19AttachIRRowValues(t, plan)
			},
			pin: "coded-group row values are a pg-sql-13 capability gap and are refused, never partially rendered",
		},
		{
			name: "row_value_projections_pg_sql_13_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				// Projections present without the RowValues list: the
				// other half of the same registered divergence (L-19.2).
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.RowValueProjections = []ir.PhysicalProjection{{Name: "resource_id", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}}
			},
			pin: "coded-group row values are a pg-sql-13 capability gap and are refused, never partially rendered",
		},
		{
			// Registered named divergence L-19.4 (coordinator F-3
			// ruling): lowering EMITs SET in the source region (probe:
			// logs/pg19-f3-set-reachability.log) and AQL renders it
			// inline; no PostgreSQL configuration can render the
			// lowering-emitted child-set subplan, so the door refuses
			// fail-closed. The producer-channel pin is
			// TestCodedGroupIRSetAliveControl.
			name: "source_region_set_pg_sql_16_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				plan.Operations = append(plan.Operations[:last],
					ir.PhysicalOperation{Kind: ir.PhysicalSetOp},
					plan.Operations[last])
			},
			pin: "coded-group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; probe-attested L-19.4) and is refused, never partially rendered",
		},
		{
			name: "occurrence_must_be_base",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.BindVars[plan.StageSequence.Stages[0].CodedGroup.OccurrenceIDBindKey] = "v2"
			},
			pin: "the registered inline slice admits only the base root occurrence (ir/physical_stage_validation.go:2274)",
		},
		{
			name: "coding_path_bind_mismatch",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.BindVars[plan.StageSequence.Stages[0].CodedGroup.CodingPathBindKey] = "component.code.coding"
			},
			pin: "the Coding path bind differs from the compiled path",
		},
		{
			name: "collection_bind_mismatch",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.BindVars[plan.StageSequence.Stages[0].CodedGroup.RootCollectionBindKey] = "Patient"
			},
			pin: "root collection bind does not match the exact root resource type",
		},
		{
			name: "uniqueness_proof_required",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].CodedGroup.SourceRowsUnique = false
			},
			pin: "CODED_GROUP requires the compiler proof of one source row per root _key",
		},
		{
			name: "identity_column_must_be_root_key",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].CodedGroup.SourceIdentityColumn = "id"
			},
			pin: "CODED_GROUP requires the compiler proof of one source row per root _key",
		},
		{
			name: "unknown_missing_key_policy",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].CodedGroup.MissingKeyPolicy = "SOMETHING"
			},
			pin: `missing-key policy "SOMETHING" is unsupported`,
		},
		{
			name: "path_must_end_repeated",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "component", Repeated: true}, {Name: "code"}}
				coded.CodingPath = "component[].code"
				plan.BindVars[coded.CodingPathBindKey] = coded.CodingPath
			},
			pin: "the Coding path must end in a repeated Coding array",
		},
		{
			name: "canonical_segment_join_required",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "component"}, {Name: "code"}, {Name: "coding", Repeated: true}}
			},
			pin: "the Coding path must be the canonical join of its segments",
		},
		{
			name: "unsafe_segment_name_refused",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.PathSegments = []ir.PhysicalCodedGroupPathSegment{{Name: "component", Repeated: true}, {Name: "code"}, {Name: "bad-name", Repeated: true}}
				coded.CodingPath = "component[].code.bad-name[]"
				plan.BindVars[coded.CodingPathBindKey] = coded.CodingPath
			},
			pin: `coded-group path segment "bad-name" is not renderer-safe`,
		},
		{
			name: "preview_limit_must_be_positive_int",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.PreviewLimitBindKey = "limit"
				plan.BindVars["limit"] = 0
			},
			pin: "coded-group preview limit bind \"limit\" must be a positive int",
		},
		{
			name: "preview_limit_non_int",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.PreviewLimitBindKey = "limit"
				plan.BindVars["limit"] = "ten"
			},
			pin: "coded-group preview limit bind \"limit\" must be a positive int",
		},
		{
			name: "traversal_outside_the_source_surface",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				plan.Operations = append(plan.Operations[:last],
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp},
					plan.Operations[last])
			},
			pin: `operation "TRAVERSAL" is not part of the registered coded-group source surface`,
		},
		{
			name: "unnest_outside_the_source_surface",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				plan.Operations = append(plan.Operations[:last],
					ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{}},
					plan.Operations[last])
			},
			pin: `operation "UNNEST" is not part of the registered coded-group source surface`,
		},
		{
			name: "expression_let_outside_the_source_surface",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				plan.Operations = append(plan.Operations[:last],
					ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp},
					plan.Operations[last])
			},
			pin: `operation "EXPRESSION_LET" is not part of the registered coded-group source surface`,
		},
		{
			name: "population_root_population_binding_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			},
			pin: "bound only by RenderPopulationRootScan (population.go)",
		},
		{
			name: "diagnostic_returns_have_no_pg_route",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{}
			},
			pin: "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
		},
		{
			name: "cell_trace_return_refused",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
			},
			pin: "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
		},
		{
			name: "preview_source_window_requires_pivot",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.PreviewSourceWindowByRootID = true
			},
			pin: "preview source windowing and the terminal Pivot window require Pivot terminals",
		},
		{
			// Review finding r4532120565: the PLAN-level flag
			// (ir/physical_types.go:27) must be refused HERE, not by the
			// spine gate at render.go:237 — the hook dispatches coded-group
			// plans before it, so this door is the only place the check
			// can fire (the twin graph door keeps the same check at
			// graph.go:228-231). plan.Validate covers it only under
			// whole-relation evidence (ir/whole_relation_scope_evidence.go:159).
			name: "plan_level_preview_source_window_refused",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.PreviewSourceWindowByRootID = true
			},
			pin: "preview source windowing and the terminal Pivot window require Pivot terminals",
		},
		{
			name: "output_auth_path_composite_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.StageSequence.OutputAuthResourcePathBindKey = "output_auth"
			},
			pin: "the composite Combine prefix authorization path is not registered for CODED_GROUP",
		},
		{
			name: "terminal_return_required",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				plan.Operations = plan.Operations[:last]
			},
			pin: "the coded-group source requires a terminal RETURN",
		},
		{
			name: "second_root_scan_refused",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				plan.Operations = append([]ir.PhysicalOperation{{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root2", CollectionBindKey: "root_collection"}}}, plan.Operations...)
			},
			pin: "exactly one leading ROOT_SCAN is required",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := ir.ClonePhysicalPlan(base)
			testCase.mutate(t, &plan)
			var err error
			if testCase.door {
				// Defense-in-depth shape: the entry dispatch never routes
				// this plan to the door (it mentions no CODED_GROUP kind),
				// so the door's own gate is exercised directly.
				_, err = renderCodedGroupPhysicalPlan(plan, mustBinding(t))
			} else {
				_, err = RenderPhysicalPlan(plan, mustBinding(t))
			}
			if !errors.Is(err, ErrUnsupportedPostgresPlan) {
				t.Fatalf("error = %v, want ErrUnsupportedPostgresPlan", err)
			}
			if !strings.Contains(err.Error(), testCase.pin) {
				t.Fatalf("refusal message must pin %q verbatim, got: %v", testCase.pin, err)
			}
		})
	}
}

// TestCodedGroupEntryDispatchEquality pins the graph.go door property: the
// RenderPhysicalPlan hook and the direct door enforce one contract with
// one message.
func TestCodedGroupEntryDispatchEquality(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, "proj-a", "gen-1")
	fromEntry, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("entry render: %v", err)
	}
	fromDoor, err := renderCodedGroupPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil || fromDoor.SQL != fromEntry.SQL || !reflect.DeepEqual(fromDoor.Args, fromEntry.Args) {
		t.Fatalf("entry and direct door diverge: %v", err)
	}

	// A refused shape refuses identically through both doors.
	broken := ir.ClonePhysicalPlan(plan)
	broken.StageSequence.Stages[0].CodedGroup.SourceRowsUnique = false
	entryErr := errorOf(RenderPhysicalPlan(broken, mustBinding(t)))
	doorErr := errorOf(renderCodedGroupPhysicalPlan(broken, mustBinding(t)))
	if entryErr == nil || doorErr == nil || entryErr.Error() != doorErr.Error() {
		t.Fatalf("doors must refuse with one message: entry=%v door=%v", entryErr, doorErr)
	}
}

func errorOf(_ any, err error) error { return err }

// ---------------------------------------------------------------------------
// D2 registered divergence L-19.2 — RowValues in CODED_GROUP.
//
// Registered named divergence (coordinator ruling 2026-11-10):
// accepted-at-recipe-level/PG-typed-refused, enabling ticket pg-sql-13;
// operator-config entry counted as non-user surface per coordinator ruling
// 2026-11-10. Evidence: artifacts/loom-experiment-144/logs/
// pg19-reachability.log (recipe.Parse accepts rowValues, lowering ACCEPTs
// and EMITs, AQL renders) and the two authoring-path blocks
// (authoringv2/construction_validation.go:1694 four-output law +
// :1755-1769 declared-output IDs; recipe/construction.go:1023-1028 step
// output membership). The renderer's fail-closed arm is load-bearing:
// TestCodedGroupIRRowValuesAliveControl assembles the ir-accepted shape
// directly (bypassing recipe validation, the raw-document/privilege
// channel), and the arm-removal sabotage (logs/pg19-arm-removal-red.log)
// shows that without the arm the plan renders a PARTIAL statement.
// ---------------------------------------------------------------------------

// coded19IRRowValuesRefusalMessage is the cell-verbatim refusal of the
// registered divergence (D2 condition 4).
const coded19IRRowValuesRefusalMessage = "postgres renderer: unsupported physical plan: coded-group row values are a pg-sql-13 capability gap and are refused, never partially rendered"

// coded19AttachIRRowValues attaches a rowValues list the ir validator
// ACCEPTS (the ACCEPT+EMIT channel the probe banked), built to the exact
// contract physical_stage_validation.go:2346-2396 demands: the hidden
// clone of the resolved source projection, the exact-variable output
// projection, and the ONE-policy output column.
func coded19AttachIRRowValues(t *testing.T, plan *ir.PhysicalPlan) {
	t.Helper()
	sourceReturn := plan.Operations[len(plan.Operations)-1].Return
	var resourceID ir.PhysicalProjection
	found := false
	for _, projection := range sourceReturn.Projections {
		if projection.Name == "resource_id" {
			resourceID, found = projection, true
		}
	}
	if !found {
		t.Fatal("lowered source Return lost the resource_id projection")
	}
	resourceID.Hidden = true
	coded := plan.StageSequence.Stages[0].CodedGroup
	coded.RowValues = []ir.PhysicalStageRowValue{{
		InputColumn: "resource_id", InputKind: "STRING", InputMany: false,
		Output: "row_value_1", Policy: "ONE", Variable: "cg19_rv_var",
	}}
	coded.RowValueProjections = []ir.PhysicalProjection{resourceID}
	stage := &plan.StageSequence.Stages[0]
	stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{Name: "row_value_1", Value: ir.PhysicalValue{Variable: "cg19_rv_var"}})
	column := ir.PhysicalStageColumn{ID: "rv_id", Name: "row_value_1", Label: "row_value_1", Kind: "string", Cardinality: "optional_one", Nullable: true}
	stage.OutputColumns = append(stage.OutputColumns, column)
	plan.StageSequence.FinalColumns = append(plan.StageSequence.FinalColumns, column)
}

// TestCodedGroupIRRowValuesAliveControl is the D2 condition-2 ALIVE
// assertion: the assembled plan passes ir.Validate (the producer chain
// accepts and emits this shape), so the ONLY thing standing between it and
// a rendered statement is this renderer's fail-closed arm. Removing the
// arm must turn this test RED with a rendered partial statement —
// evidence banked at logs/pg19-arm-removal-red.log.
func TestCodedGroupIRRowValuesAliveControl(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	coded19AttachIRRowValues(t, &plan)
	if err := plan.Validate(); err != nil {
		t.Fatalf("the producer channel must accept this shape (probe-banked ACCEPT+EMIT): %v", err)
	}
	for label, render := range map[string]func(ir.PhysicalPlan, ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error){
		"entry": RenderPhysicalPlan,
		"door":  renderCodedGroupPhysicalPlan,
	} {
		rendered, err := render(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if err == nil {
			t.Fatalf("%s rendered the refused RowValues shape — the fail-closed arm is not load-bearing:\n%s", label, rendered.SQL)
		}
		if !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("%s error = %v (%T), want ErrUnsupportedPostgresPlan", label, err, err)
		}
		if err.Error() != coded19IRRowValuesRefusalMessage {
			t.Fatalf("%s refusal message must be cell-verbatim:\n got %q\nwant %q", label, err.Error(), coded19IRRowValuesRefusalMessage)
		}
	}
}

// TestCodedGroupIRRowValuesLiveZeroSideEffects is the D2 condition-3 live
// proof on the owned coded19_it_* namespace: the refusal happens BEFORE
// any SQL is produced, so a refused RowValues plan touches the PostgreSQL
// service not at all — no tables, no rows, no statement text.
func TestCodedGroupIRRowValuesLiveZeroSideEffects(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	coded19Insert(t, svc, "rvw-1", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-rv", "", "kept")))

	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, coded19Project, coded19Generation)
	coded19AttachIRRowValues(t, &plan)

	before := coded19Inventory(t, svc)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err == nil || !strings.Contains(err.Error(), "pg-sql-13") {
		t.Fatalf("refusal required before any SQL; got rendered=%v err=%v", rendered.SQL, err)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 || len(rendered.OutputColumns) != 0 {
		t.Fatalf("a refused plan must carry zero SQL, zero parameters, zero columns: %#v", rendered)
	}
	if after := coded19Inventory(t, svc); after != before {
		t.Fatalf("the refused RowValues plan changed service state:\n before %q\n after  %q", before, after)
	}
	// Alive control: the schema is live and readable — the inventory
	// equality above is a real zero-delta, not a query silently returning
	// nothing.
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, coded19Project); rows[0][0] != "1" {
		t.Fatalf("alive witness: %v, want the single inserted row", rows)
	}
}

// coded19Inventory snapshots the owned database's structural and row
// state as one comparable string: catalog tables, their row counts, and
// the resource-table digest.
func coded19Inventory(t *testing.T, svc *pgir02Service) string {
	t.Helper()
	tables := svc.witness(t,
		`SELECT c.relname, c.reltuples::bigint FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace`+
			` WHERE n.nspname = $1 AND c.relkind = 'r' ORDER BY 1`, svc.schema)
	resourceRows := svc.witness(t,
		`SELECT count(*), coalesce(md5(string_agg(payload::text, ',' ORDER BY resource_seq)), '') FROM "`+svc.schema+`"."fhir_resource"`)
	return strings.Join([]string{fmt.Sprint(tables), fmt.Sprint(resourceRows)}, "|")
}

// ---------------------------------------------------------------------------
// Registered named divergence L-19.4 (coordinator F-3 ruling 2026-11-11,
// "PROBE FIRST, no silent divergence either direction"): SET in the
// coded-group source region. The forced probe (real
// recipe.Parse→semantic→lower, logs/pg19-f3-set-reachability.log,
// disposable legs archived at artifacts/loom-experiment-144/probes/)
// shows lowering EMITs the shape (setOps=1) and AQL renders it inline
// (AQL control err=nil), but NO PostgreSQL configuration renders the
// lowering-emitted child-set subplan: the production default policy
// gives it ENDPOINT_LOOKUP (refused at traversal.go:141; the REQUIRED
// variant additionally fails the non-Expression required-match filter
// at sets.go:236), and with the endpoint rule dev-disabled the child-set
// argument wiring emits an unreferenced placeholder, refused by the
// argument-integrity guard. So the baseline SET whitelist
// (lower/recipe_construction_coded_group.go:176-179) is refused
// fail-closed at this door naming pg-sql-16 as the enabling ticket —
// never partially rendered, zero SQL, zero side effects. Corroboration:
// the merged sets.go:82-95 finding "No current lowering output reaches
// the hop-only renderer". The arm-removal sabotage RED is banked at
// logs/pg19-set-arm-removal-red.log.
// ---------------------------------------------------------------------------

// coded19SetRefusalMessage is the cell-verbatim L-19.4 refusal.
const coded19SetRefusalMessage = "postgres renderer: unsupported physical plan: coded-group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; probe-attested L-19.4) and is refused, never partially rendered"

// coded19SetInSourceRegionPlan replays the banked F-3 probe through the
// real producer channel: a recipe document whose only child traversal
// feeds the CODED_GROUP source, lowering a SET into the source region.
func coded19SetInSourceRegionPlan(t *testing.T, required bool) ir.PhysicalPlan {
	t.Helper()
	match := ""
	if required {
		match = `"matchMode":"REQUIRED",`
	}
	doc := `{"recipeSchemaVersion":1,"name":"coded_group_rows","translationVersion":"test","outputs":[{
	 "name":"coded_group_rows","rootResourceType":"Observation","rowGrain":"resource",
	 "fields":[{"name":"resource_id","columnId":"resource_id","expr":{"select":"root.id"}}],
	 "traversals":[{"name":"subject_Patient","alias":"occ_parent","toResourceType":"Patient",` + match + `"fields":[{"name":"subject_gender","columnId":"subject_gender","expr":{"select":"occ_parent.gender"}}]}],
	 "construction":{"version":1,
	 "sourceColumns":[{"id":"resource_id","name":"resource_id"},{"id":"occ_parent__subject_gender","name":"occ_parent__subject_gender"}],
	 "steps":[{"id":"group_codes","inputs":[{"kind":"SOURCE_PROJECTION"}],
	  "operation":{"kind":"CODED_GROUP","codedGroup":{
	   "constructionId":"group_codes",
	   "source":{"occurrenceId":"base","resourceType":"Observation","codingPath":"component[].code.coding[]","fhirType":"Coding","cardinality":"MANY","shape":"ARRAY","route":[]},
	   "missingKeyPolicy":"GROUP",
	   "systemOutputColumnId":"system_id","versionOutputColumnId":"version_id","codeOutputColumnId":"code_id","distinctSourceCountOutputColumnId":"count_id"}},
	  "outputs":[
	   {"id":"system_id","name":"code_system","type":"string","nullable":true},
	   {"id":"version_id","name":"code_version","type":"string","nullable":true},
	   {"id":"code_id","name":"code","type":"string","nullable":true},
	   {"id":"count_id","name":"source_records","type":"integer"}]}]}}]}`
	parsed, err := recipe.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("recipe.Parse (probe-banked document): %v", err)
	}
	bindings := recipe.RuntimeBindings{Project: coded19Project, DatasetGeneration: coded19Generation, AuthResourcePaths: []string{"FHIR/Observation"}}
	plan, err := semantic.BuildRecipePlan(parsed, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan (the whitelist emits SET): %v", err)
	}
	lowered := compiled.Outputs[0].Plan
	setOps := 0
	for _, operation := range lowered.Operations {
		if operation.Kind == ir.PhysicalSetOp {
			setOps++
		}
	}
	if setOps != 1 {
		t.Fatalf("the probe-banked document must lower exactly one source-region SET, got %d", setOps)
	}
	return ir.ClonePhysicalPlan(lowered)
}

// TestCodedGroupIRSetAliveControl is the L-19.4 load-bearing assertion:
// the producer channel ACCEPTs and EMITs the shape (plan.Validate
// passes, the default policy stamps the child-set traversal
// ENDPOINT_LOOKUP — the exact probe finding), so the ONLY thing standing
// between it and a rendered statement is this renderer's fail-closed
// arm. Removing the arm must turn the register cell and this test RED —
// evidence banked at logs/pg19-set-arm-removal-red.log.
func TestCodedGroupIRSetAliveControl(t *testing.T) {
	for _, required := range []bool{false, true} {
		plan := coded19SetInSourceRegionPlan(t, required)
		if err := plan.Validate(); err != nil {
			t.Fatalf("required=%v: the producer channel must accept this shape (probe-banked EMIT): %v", required, err)
		}
		strategies := map[ir.PhysicalTraversalStrategy]bool{}
		for _, operation := range plan.Operations {
			if operation.Kind != ir.PhysicalSetOp || operation.Set == nil {
				continue
			}
			for _, sub := range operation.Set.Subplan.Operations {
				if sub.Kind == ir.PhysicalTraversalOp && sub.Traversal != nil {
					strategies[sub.Traversal.Strategy] = true
				}
			}
		}
		if !strategies[ir.PhysicalTraversalEndpointLookup] {
			t.Fatalf("required=%v: the probe pinned the default policy to stamp ENDPOINT_LOOKUP on the child-set subplan, got %v", required, strategies)
		}
		for label, render := range map[string]func(ir.PhysicalPlan, ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error){
			"entry": RenderPhysicalPlan,
			"door":  renderCodedGroupPhysicalPlan,
		} {
			rendered, err := render(ir.ClonePhysicalPlan(plan), mustBinding(t))
			if err == nil {
				t.Fatalf("required=%v %s rendered the refused SET shape — the fail-closed arm is not load-bearing:\n%s", required, label, rendered.SQL)
			}
			if !errors.Is(err, ErrUnsupportedPostgresPlan) {
				t.Fatalf("required=%v %s error = %v (%T), want ErrUnsupportedPostgresPlan", required, label, err, err)
			}
			if err.Error() != coded19SetRefusalMessage {
				t.Fatalf("required=%v %s refusal message must be cell-verbatim:\n got %q\nwant %q", required, label, err.Error(), coded19SetRefusalMessage)
			}
		}
	}
}

// TestCodedGroupIRSetLiveZeroSideEffects is the live proof on the owned
// coded19_it_* namespace: the refusal happens BEFORE any SQL is
// produced, so a refused SET-in-source-region plan touches the
// PostgreSQL service not at all.
func TestCodedGroupIRSetLiveZeroSideEffects(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	coded19Insert(t, svc, "stw-1", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-stw", "", "kept")))

	plan := coded19SetInSourceRegionPlan(t, false)

	before := coded19Inventory(t, svc)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err == nil || err.Error() != coded19SetRefusalMessage {
		t.Fatalf("refusal required before any SQL; got rendered=%v err=%v", rendered.SQL, err)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 || len(rendered.OutputColumns) != 0 {
		t.Fatalf("a refused plan must carry zero SQL, zero parameters, zero columns: %#v", rendered)
	}
	if after := coded19Inventory(t, svc); after != before {
		t.Fatalf("the refused SET plan changed service state:\n before %q\n after  %q", before, after)
	}
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, coded19Project); rows[0][0] != "1" {
		t.Fatalf("alive witness: %v, want the single inserted row", rows)
	}
}

// TestCodedGroupTrimSetStructuralLaw enforces the single-trim-set law: the
// trim characters exist as ONE Go constant in the package, every btrim call
// binds it as a parameter, and no other package file hard-codes a trim
// literal. The named victim: a second, drifted trim set.
func TestCodedGroupTrimSetStructuralLaw(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	constDeclaration := `const codedGroupTrimCharacters = " "`
	const bindingMarker = `, " + dollar(trimChars) + "::text)`
	declarations := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.Contains(text, constDeclaration) {
			declarations++
			if name != "coded_group.go" {
				t.Fatalf("trim-set constant declared outside coded_group.go: %s", name)
			}
		}
		if name != "coded_group.go" {
			for _, line := range strings.Split(text, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
					continue // documentation may name the constant
				}
				if strings.Contains(line, "btrim(") {
					t.Fatalf("%s introduces a btrim call outside the registered coded-group gate file: %s", name, trimmed)
				}
				if strings.Contains(line, "codedGroupTrimCharacters") {
					t.Fatalf("%s uses the trim-set constant outside the registered coded-group gate file: %s", name, trimmed)
				}
			}
		}
		// Every btrim call in the gate file must close its trim argument
		// through the single constant: the rendered argument is built as
		// ", " + dollar(trimChars) + "::text)". Scan only real code lines.
		lines := strings.Split(text, "\n")
		for lineOffset, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if !strings.Contains(line, "btrim(") {
				continue
			}
			if !strings.Contains(line, bindingMarker) {
				t.Fatalf("coded_group.go line %d has a btrim call that does not bind the trim set through the single constant:\n%s", lineOffset+1, line)
			}
		}
	}
	if declarations != 1 {
		t.Fatalf("trim-set constant declarations = %d, want exactly 1", declarations)
	}
}

// ---------------------------------------------------------------------------
// Live: owned namespace, delta audit, and the acceptance matrices.
// ---------------------------------------------------------------------------

func newCoded19Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "coded19_it")
}

// coded19RenderSvc renders through the entry door against the test's owned
// schema: the live acceptance must execute the very statement the entry
// dispatch produces, only bound to the isolated database.
func coded19RenderSvc(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan(%s): %v", svc.schema, err)
	}
	return rendered
}

var (
	coded19FixturesMu      sync.Mutex
	coded19TrackedFixtures = map[string]bool{}
)

func coded19TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	coded19FixturesMu.Lock()
	defer coded19FixturesMu.Unlock()
	coded19TrackedFixtures[database] = true
	coded19TrackedFixtures[svc.schema] = true
}

func coded19SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'coded19_it_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'coded19_it_%'`)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// coded19RunDeltaAudit wraps the composed audit chain (pg-sql-19 is the
// 6th, outermost hook) with a delta-scoped audit over the disjoint
// coded19_it_* namespace: every resource born during this run and still
// surviving at the end must have been tracked by a live test here, and
// none may survive at all. The pre-snapshot also proves the namespace
// started empty.
func coded19RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := coded19SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: pre-snapshot found %d surviving coded19_it_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := coded19SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	coded19FixturesMu.Lock()
	defer coded19FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if coded19TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	survivors, err := coded19SnapshotOwnedResources(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-19 delta audit: %d coded19_it_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

const (
	coded19Project    = "p19-proj"
	coded19Generation = "p19-gen"
)

func coding(system, version, code string) map[string]any {
	value := map[string]any{}
	if system != "" {
		value["system"] = system
	}
	if version != "" {
		value["version"] = version
	}
	if code != "" {
		value["code"] = code
	}
	return value
}

func codingPayload(codings ...map[string]any) map[string]any {
	return map[string]any{"component": []any{map[string]any{"code": map[string]any{"coding": codings}}}}
}

func coded19Insert(t *testing.T, svc *pgir02Service, logicalID, project, generation, authPath string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, project, generation, "Observation", logicalID, authPath, payload)
}

func TestCodedGroupLiveExactTupleMultiset(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)

	coded19Insert(t, svc, "obs-a", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("system-a", "", "shared"),
		coding("system-a", "", "shared"),
		coding("system-b", "", "shared"),
		coding("system-a", "", "only-a"),
	))
	coded19Insert(t, svc, "obs-b", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("system-a", "", "shared"),
		coding("system-a", "v1", "shared"),
		coding("system-a", " v1 ", "shared"),
	))
	coded19Insert(t, svc, "obs-missing", coded19Project, coded19Generation, "FHIR/Observation", nil)
	coded19Insert(t, svc, "obs-invalid", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(
		coding(" ", "", "shared"), coding("", "", "shared"), coding("system-a", "", ""),
	))
	// Non-array coding (object where the array belongs) and the [null]
	// item: both must read as zero items plus the AQL [null] candidate.
	svc.insertResource(t, svc.schema, coded19Project, coded19Generation, "Observation", "obs-nonarray", "FHIR/Observation",
		map[string]any{"component": []any{map[string]any{"code": map[string]any{"coding": coding("system-x", "", "y")}}}})
	svc.insertResource(t, svc.schema, coded19Project, coded19Generation, "Observation", "obs-nullitem", "FHIR/Observation",
		map[string]any{"component": []any{map[string]any{"code": map[string]any{"coding": []any{nil}}}}})

	// Scope witnesses (alive: they exist in the store, must never group).
	coded19Insert(t, svc, "obs-denied", coded19Project, coded19Generation, "FHIR/Hidden", codingPayload(coding("system-a", "", "denied")))
	coded19Insert(t, svc, "obs-oldgen", coded19Project, "p19-old-gen", "FHIR/Observation", codingPayload(coding("system-a", "", "oldgen")))
	coded19Insert(t, svc, "obs-foreign", "p19-foreign", coded19Generation, "FHIR/Observation", codingPayload(coding("system-a", "", "foreign")))

	// Pre-snapshot witness: the acceptance rows were really inserted.
	// Eight rows carry project p19-proj (six acceptance roots plus the
	// denied-path and old-generation witnesses; only the foreign-project
	// row leaves the project).
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, coded19Project); rows[0][0] != "8" {
		t.Fatalf("scope witness: p19-proj row count = %v, want 8", rows)
	}

	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, coded19Project, coded19Generation)
	rendered := coded19RenderSvc(t, svc, plan)
	for _, leaked := range []string{coded19Project, coded19Generation, "system-a", "shared", "FHIR/Observation"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, rendered.SQL)
		}
	}
	rows := coded19Rows(t, svc.runRendered(t, rendered))

	// The literal expected multiset: exact tuples and contributing-root
	// counts. The duplicate identical coding inside obs-a contributes ONE
	// to (system-a,null,shared), which obs-b also feeds: count 2.
	type want struct {
		system, version, code *string
		count                 string
	}
	expected := []want{
		{coded19Text("system-a"), nil, coded19Text("shared"), "2"},
		{coded19Text("system-b"), nil, coded19Text("shared"), "1"},
		{coded19Text("system-a"), nil, coded19Text("only-a"), "1"},
		{coded19Text("system-a"), coded19Text("v1"), coded19Text("shared"), "1"},
		{coded19Text("system-a"), coded19Text(" v1 "), coded19Text("shared"), "1"},
		// Four roots contribute the missing tuple: coding-less, non-array
		// coding, the [null] item, and the blank-system coding. The
		// blank-code and empty-string codings on obs-invalid do NOT: that
		// root's valid "shared" coding already owns its tuple, and the
		// stage counts roots that contribute NO tuple at all.
		{nil, nil, nil, "4"},
	}
	if len(rows) != len(expected) {
		t.Fatalf("grouped tuples = %v, want %d rows", rows, len(expected))
	}
	byTuple := map[string]coded19Row{}
	for _, row := range rows {
		key := fmt.Sprintf("%v|%v|%v", deref(row.system), deref(row.version), deref(row.code))
		if _, clash := byTuple[key]; clash {
			t.Fatalf("duplicate tuple row %q: %v", key, rows)
		}
		byTuple[key] = row
	}
	for _, want := range expected {
		key := fmt.Sprintf("%v|%v|%v", deref(want.system), deref(want.version), deref(want.code))
		row, ok := byTuple[key]
		if !ok {
			t.Fatalf("missing tuple row %q in %v", key, rows)
		}
		if row.count != want.count {
			t.Fatalf("tuple %q count = %q, want %q", key, row.count, want.count)
		}
	}
	for _, forbidden := range []string{"denied", "oldgen", "foreign", "system-x", "y"} {
		for _, row := range rows {
			if deref(row.code) == forbidden {
				t.Fatalf("out-of-scope coding %q grouped: %v", forbidden, rows)
			}
		}
	}

	// The final order is the identity string, COLLATE "C" bytes: compare
	// against the Go-side byte order of independently-built identities.
	identities := make([]string, len(rows))
	for index, row := range rows {
		identities[index] = row.identity
		if want := coded19Identity(t, row.system, row.version, row.code); row.identity != want {
			t.Fatalf("row identity = %q, want %q", row.identity, want)
		}
	}
	sorted := append([]string(nil), identities...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(identities, sorted) {
		t.Fatalf("rows are not ordered by the identity COLLATE \"C\" bytes:\n got %v\nwant %v", identities, sorted)
	}
	seen := map[string]bool{}
	for _, identity := range identities {
		if seen[identity] {
			t.Fatalf("duplicate row identity %q", identity)
		}
		seen[identity] = true
	}

	// Determinism across executions: identical identities.
	repeated := coded19Rows(t, svc.runRendered(t, rendered))
	if !reflect.DeepEqual(rows, repeated) {
		t.Fatalf("grouping changed between executions:\n%v\n%v", rows, repeated)
	}

	// EXCLUDE: the missing tuple disappears; every other tuple survives.
	excludeRendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyExclude, coded19Project, coded19Generation))
	excluded := coded19Rows(t, svc.runRendered(t, excludeRendered))
	if len(excluded) != len(expected)-1 {
		t.Fatalf("EXCLUDE rows = %v, want %d (missing tuple excluded)", excluded, len(expected)-1)
	}
	for _, row := range excluded {
		if row.system == nil && row.version == nil && row.code == nil {
			t.Fatalf("EXCLUDE kept the missing tuple: %v", excluded)
		}
	}

	// ERROR: the invalid candidates raise the canonical tag through the
	// guard barrier — even though the grouped result is non-empty, so the
	// raise is candidate-positioned, not result-positioned.
	errorRendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, coded19Project, coded19Generation))
	if err := coded19ExecErr(svc, errorRendered); err == nil {
		t.Fatalf("ERROR policy must raise on the invalid candidates")
	} else if !strings.Contains(svc.scrub(err), codedGroupMissingKeyTag) {
		t.Fatalf("ERROR policy error must carry the canonical tag: %v", svc.scrub(err))
	} else if !strings.Contains(svc.scrub(err), "22P02") {
		t.Fatalf("ERROR guard must raise through the barrier cast (22P02): %v", svc.scrub(err))
	}

	// Alive control / mutation probe: a newly inserted root carrying the
	// same coding lifts the (system-a,null,shared) count 2 -> 3 and adds
	// one missing-tuple contributor, proving the grouping reads live rows.
	coded19Insert(t, svc, "obs-c", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(coding("system-a", "", "shared")))
	mutated := coded19Rows(t, svc.runRendered(t, rendered))
	for _, row := range mutated {
		if deref(row.system) == "system-a" && row.version == nil && deref(row.code) == "shared" && row.count == "3" {
			return
		}
	}
	t.Fatalf("mutation probe: (system-a,null,shared) count never became 3: %v", mutated)
}

func deref(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func coded19QueryArgs(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return args
}

// coded19ExecErr executes a rendered statement for its error only, always
// closing the rows (an unclosed portal would pin a pooled connection and
// hang the owned-database DROP in cleanup).
func coded19ExecErr(svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan) error {
	rows, err := svc.iso.Query(svc.ctx, rendered.SQL, coded19QueryArgs(rendered)...)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	closeErr := rows.Err()
	rows.Close()
	return closeErr
}

func TestCodedGroupLiveErrorPolicyBarrierMatrix(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)

	// Partition 1 (zero-group live cell + both sabotage controls): every
	// candidate invalid — coding-less roots and blank-key codings only.
	const allInvalidProject = "p19-all-invalid"
	coded19Insert(t, svc, "inv-1", allInvalidProject, coded19Generation, "FHIR/Observation", codingPayload(coding("", "", "shared")))
	coded19Insert(t, svc, "inv-2", allInvalidProject, coded19Generation, "FHIR/Observation", nil)

	// EXCLUDE over the partition: zero rows, no raise.
	exclude := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyExclude, allInvalidProject, coded19Generation))
	if rows := svc.runRendered(t, exclude); len(rows) != 0 {
		t.Fatalf("EXCLUDE over all-invalid data must group nothing: %v", rows)
	}
	// Sabotage control A (the fixture is not vacuous): GROUP over the same
	// data yields the (null,null,null) tuple with both roots counted.
	grouped := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, allInvalidProject, coded19Generation))))
	if len(grouped) != 1 || grouped[0].count != "2" || grouped[0].system != nil {
		t.Fatalf("GROUP sabotage control: %v, want one missing tuple with count 2", grouped)
	}
	// Sabotage control B (the barrier fires): ERROR over the same
	// zero-group result raises.
	errorRendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, allInvalidProject, coded19Generation))
	if err := coded19ExecErr(svc, errorRendered); err == nil ||
		!strings.Contains(svc.scrub(err), codedGroupMissingKeyTag) {
		t.Fatalf("ERROR must raise at zero grouped rows: %v", svc.scrub(fmt.Sprint(err)))
	}
	// Zero-source control: ERROR with no in-scope roots never raises in
	// either backend (no candidate rows exist).
	empty := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, "p19-no-rows", coded19Generation))
	if rows := svc.runRendered(t, empty); len(rows) != 0 {
		t.Fatalf("zero-source ERROR must return zero rows: %v", rows)
	}
	// Healthy control: ERROR over all-valid data renders its tuples.
	const healthyProject = "p19-healthy"
	coded19Insert(t, svc, "ok-1", healthyProject, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-h", "", "c1")))
	healthy := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, healthyProject, coded19Generation))))
	if len(healthy) != 1 || deref(healthy[0].system) != "sys-h" || healthy[0].count != "1" {
		t.Fatalf("healthy ERROR control: %v", healthy)
	}

	// The guard qual survives planning (no constant-fold pruning): the
	// EXPLAIN plan text carries the tag and the barrier GUC read.
	planRows, err := svc.iso.Query(svc.ctx, "EXPLAIN (FORMAT JSON) "+errorRendered.SQL, coded19QueryArgs(errorRendered)...)
	if err != nil {
		svc.fail("explain ERROR statement", err)
	}
	var planText strings.Builder
	for planRows.Next() {
		var value []byte
		if err := planRows.Scan(&value); err != nil {
			svc.fail("scan explain row", err)
		}
		planText.Write(value)
	}
	planRows.Close()
	text := planText.String()
	for _, want := range []string{codedGroupMissingKeyTag, guardBarrierGUC} {
		if !strings.Contains(text, want) {
			t.Fatalf("EXPLAIN plan lost the guard (%q); the barrier was folded:\n%s", want, text)
		}
	}
}

func TestCodedGroupLiveWhitespaceUnicodeAndByteOrder(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	const project = "p19-ws"

	// Space-only trim boundary pins (the space-only law of
	// codedGroupTrimCharacters): tab/newline/Unicode-space padded values
	// stay VALID and are emitted untrimmed; a space-only padded system is
	// invalid; the string "null" is a code, distinct from the missing
	// tuple's unquoted null spelling.
	coded19Insert(t, svc, "ws-1", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-w", "", "\tT"), coding("sys-w", "", "\nN"), coding("sys-w", "", " U"),
	))
	coded19Insert(t, svc, "ws-2", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("  ", "", "ws-invalid"), coding("\t", "", "tab-system"),
	))
	coded19Insert(t, svc, "ws-3", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-w", "", "null"),
	))
	// Registered divergence D-19a cells: PostgreSQL COLLATE "C" orders the
	// identity text by raw UTF-8 bytes.
	coded19Insert(t, svc, "ws-4", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-u", "", "é"), coding("sys-u", "", "Ω"), coding("sys-u", "", "日本"), coding("sys-u", "", "a"),
	))

	rendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation))
	rows := coded19Rows(t, svc.runRendered(t, rendered))

	type tuple struct{ system, version, code string }
	counts := map[tuple]string{}
	for _, row := range rows {
		counts[tuple{deref(row.system), deref(row.version), deref(row.code)}] = row.count
	}
	for _, want := range []tuple{
		{"sys-w", "<nil>", "\tT"}, {"sys-w", "<nil>", "\nN"}, {"sys-w", "<nil>", " U"},
		{"\t", "<nil>", "tab-system"}, {"sys-w", "<nil>", "null"},
		{"sys-u", "<nil>", "é"}, {"sys-u", "<nil>", "Ω"}, {"sys-u", "<nil>", "日本"}, {"sys-u", "<nil>", "a"},
	} {
		if counts[want] != "1" {
			t.Fatalf("tuple %#v count = %q, want 1; all rows: %v", want, counts[want], rows)
		}
	}
	// The space-blank system is invalid and shares the missing tuple with
	// nothing else here: ws-2's tab-system coding is valid, so ws-2
	// contributes NO missing tuple… except the blank one: exactly one
	// missing-tuple contributor.
	if counts[tuple{"<nil>", "<nil>", "<nil>"}] != "1" {
		t.Fatalf("missing tuple must carry exactly the blank-system coding's root: %v", rows)
	}
	if _, leaked := counts[tuple{"  ", "<nil>", "ws-invalid"}]; leaked {
		t.Fatalf("space-blank system must not survive validity: %v", rows)
	}

	// D-19a: exact byte order over the non-ASCII codes. The expected order
	// is Go string byte order over the independently-built identities —
	// which is what COLLATE "C" must reproduce.
	identities := make([]string, 0, len(rows))
	for _, row := range rows {
		identities = append(identities, row.identity)
	}
	sorted := append([]string(nil), identities...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(identities, sorted) {
		t.Fatalf("D-19a byte order violated; got %v want %v", identities, sorted)
	}
	var asciiBeforeNonASCII []string
	for _, row := range rows {
		if deref(row.system) == "sys-u" {
			asciiBeforeNonASCII = append(asciiBeforeNonASCII, deref(row.code))
		}
	}
	if want := []string{"a", "é", "Ω", "日本"}; !reflect.DeepEqual(asciiBeforeNonASCII, want) {
		t.Fatalf("D-19a: sys-u codes ordered %v, want UTF-8 byte order %v", asciiBeforeNonASCII, want)
	}
}

// ---------------------------------------------------------------------------
// Production-compiled acceptance (batch law, verifier duty 5 / the 143
// Option-A saga): the golden, dedup, and tie-break acceptance legs must
// run on plans produced by the real production entries — the recipe
// document is parsed by recipe.Parse (the server registration entry,
// server/server.go:442) and lowered by the real semantic/lower chain —
// never on hand-assembled IR.
// ---------------------------------------------------------------------------

// coded19ProductionDocument is a literal recipe document (not a struct
// literal re-encoded): the exact bytes a caller would register.
const coded19ProductionDocument = `{
 "recipeSchemaVersion": 1,
 "name": "coded_group_rows",
 "translationVersion": "test",
 "outputs": [
  {
   "name": "coded_group_rows",
   "rootResourceType": "Observation",
   "rowGrain": "resource",
   "fields": [
    {"name": "resource_id", "columnId": "resource_id", "expr": {"select": "root.id"}}
   ],
   "construction": {
    "version": 1,
    "sourceColumns": [{"id": "resource_id", "name": "resource_id"}],
    "steps": [
     {
      "id": "group_codes",
      "inputs": [{"kind": "SOURCE_PROJECTION"}],
      "operation": {
       "kind": "CODED_GROUP",
       "codedGroup": {
        "constructionId": "group_codes",
        "source": {
         "occurrenceId": "base",
         "resourceType": "Observation",
         "codingPath": "component[].code.coding[]",
         "fhirType": "Coding",
         "cardinality": "MANY",
         "shape": "ARRAY",
         "route": []
        },
        "missingKeyPolicy": "%s",
        "systemOutputColumnId": "system_id",
        "versionOutputColumnId": "version_id",
        "codeOutputColumnId": "code_id",
        "distinctSourceCountOutputColumnId": "count_id"
       }
      },
      "outputs": [
       {"id": "system_id", "name": "code_system", "type": "string", "nullable": true},
       {"id": "version_id", "name": "code_version", "type": "string", "nullable": true},
       {"id": "code_id", "name": "code", "type": "string", "nullable": true},
       {"id": "count_id", "name": "source_records", "type": "integer"}
      ]
     }
    ]
   }
  }
 ]
}`

func coded19ProductionCompiledPlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy, project, generation string) ir.PhysicalPlan {
	t.Helper()
	document := fmt.Sprintf(coded19ProductionDocument, string(policy))
	parsed, err := recipe.Parse([]byte(document))
	if err != nil {
		t.Fatalf("recipe.Parse (production document entry): %v", err)
	}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, AuthResourcePaths: []string{"FHIR/Observation"}}
	plan, err := semantic.BuildRecipePlan(parsed, bindings)
	if err != nil {
		t.Fatalf("semantic.BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("semantic.ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	sequence := compiled.Outputs[0].Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStageCodedGroupOp {
		t.Fatalf("production lowering did not emit the inline single-stage CODED_GROUP: %#v", sequence)
	}
	return ir.ClonePhysicalPlan(compiled.Outputs[0].Plan)
}

// TestCodedGroupProductionCompiledEqualsStructFixture pins that every
// leg driven by the struct-built lowered plan is byte-identical to the
// plan the production document entry produces: the register-wide legs
// are production-compiled, not a hand-assembled approximation.
func TestCodedGroupProductionCompiledEqualsStructFixture(t *testing.T) {
	for _, policy := range []recipe.ConstructionGroupMissingKeyPolicy{
		recipe.ConstructionGroupMissingKeyGroup,
		recipe.ConstructionGroupMissingKeyExclude,
		recipe.ConstructionGroupMissingKeyError,
	} {
		fromDocument := coded19ProductionCompiledPlan(t, policy, "proj-a", "gen-1")
		fromStruct := coded19LoweredPlan(t, policy, "proj-a", "gen-1")
		if !reflect.DeepEqual(fromDocument, fromStruct) {
			t.Fatalf("policy %s: production-document plan differs from the struct fixture", policy)
		}
	}
}

// TestCodedGroupLiveProductionCompiledGoldenDedupTieBreak runs the three
// named legs live on the production-compiled plan only: the golden exact
// tuple multiset, the per-root dedup (duplicate identical coding counts
// once per contributing root), and the tie-break (identity-text COLLATE
// "C" final order) — the ticket's own acceptance fixture.
func TestCodedGroupLiveProductionCompiledGoldenDedupTieBreak(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	const project = "p19-prod"

	// The ticket acceptance fixture: two systems sharing a code, one
	// versioned and one versionless coding, a duplicate identical coding
	// inside one record, a missing tuple, and two source records.
	coded19Insert(t, svc, "prod-a", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-one", "", "shared"),
		coding("sys-one", "", "shared"), // duplicate identical coding
		coding("sys-two", "", "shared"), // second system, same code
	))
	coded19Insert(t, svc, "prod-b", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-one", "v1", "shared"), // versioned sibling
	))
	coded19Insert(t, svc, "prod-missing", project, coded19Generation, "FHIR/Observation", nil)

	plan := coded19ProductionCompiledPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation)
	rendered := coded19RenderSvc(t, svc, plan)
	rows := coded19Rows(t, svc.runRendered(t, rendered))

	// Golden leg: the literal expected multiset.
	type want struct {
		system, version, code string
		count                 string
	}
	expected := []want{
		{"sys-one", "<nil>", "shared", "1"},
		{"sys-two", "<nil>", "shared", "1"},
		{"sys-one", "v1", "shared", "1"},
		{"<nil>", "<nil>", "<nil>", "1"},
	}
	if len(rows) != len(expected) {
		t.Fatalf("production-compiled golden multiset = %v, want %d rows", rows, len(expected))
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[fmt.Sprintf("%v|%v|%v", deref(row.system), deref(row.version), deref(row.code))] = row.count
	}
	for _, want := range expected {
		key := want.system + "|" + want.version + "|" + want.code
		if got, ok := seen[key]; !ok {
			t.Fatalf("production-compiled golden row %q missing: %v", key, rows)
		} else if got != want.count {
			t.Fatalf("production-compiled golden row %q count = %q, want %q", key, got, want.count)
		}
	}
	// Dedup leg: the duplicate identical coding on prod-a contributed
	// exactly one contributing root to (sys-one,null,shared): count 1,
	// not 2 — and the two-system share kept sys-two as its own tuple.
	if seen["sys-one|<nil>|shared"] != "1" {
		t.Fatalf("dedup leg: duplicate identical coding counted %q roots, want 1", seen["sys-one|<nil>|shared"])
	}
	// Tie-break leg: final rows are the identity string in COLLATE "C"
	// byte order, built independently of the renderer.
	identities := make([]string, len(rows))
	for index, row := range rows {
		identities[index] = row.identity
		if want := coded19Identity(t, row.system, row.version, row.code); row.identity != want {
			t.Fatalf("production-compiled row identity = %q, want %q", row.identity, want)
		}
	}
	sorted := append([]string(nil), identities...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(identities, sorted) {
		t.Fatalf("production-compiled tie-break order violated:\n got %v\nwant %v", identities, sorted)
	}
	// Determinism across executions on the production-compiled plan.
	if repeat := coded19Rows(t, svc.runRendered(t, rendered)); !reflect.DeepEqual(rows, repeat) {
		t.Fatalf("production-compiled grouping changed between executions:\n%v\n%v", rows, repeat)
	}
}

// TestCodedGroupLiveMultiComponentBranchPruning pins the AQL nested-FOR
// branch-pruning parity for the production two-repeat path
// (component[].code.coding[]): a root whose first component carries
// codings while a second component's code.coding is absent, an empty
// array, or non-array must contribute NO invalid NULL candidate — under
// GROUP no extra (null,null,null) tuple, under ERROR no raise (AQL
// raises nothing on such a query). Only a root whose WHOLE nested
// expansion is empty substitutes the single [null] candidate
// (construction_coded_group.go:54-75). A per-level LEFT JOIN chain
// fails both cells; this is the review-finding regression cell.
func TestCodedGroupLiveMultiComponentBranchPruning(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	const project = "p19-branch"
	const allEmptyProject = "p19-branch-zero"

	component := func(codings any) map[string]any {
		if codings == nil {
			return map[string]any{"code": map[string]any{}}
		}
		return map[string]any{"code": map[string]any{"coding": codings}}
	}
	twoComponents := func(first, second map[string]any) map[string]any {
		return map[string]any{"component": []any{first, second}}
	}
	kept := []any{coding("sys-br", "", "kept")}
	coded19Insert(t, svc, "br-empty-second", project, coded19Generation, "FHIR/Observation",
		twoComponents(component(kept), component([]any{})))
	coded19Insert(t, svc, "br-absent-second", project, coded19Generation, "FHIR/Observation",
		twoComponents(component(kept), component(nil)))
	coded19Insert(t, svc, "br-nonarray-second", project, coded19Generation, "FHIR/Observation",
		twoComponents(component(kept), component(coding("sys-br", "", "ghost"))))
	// Order-mirrored branch: the EMPTY component comes first.
	coded19Insert(t, svc, "br-empty-first", project, coded19Generation, "FHIR/Observation",
		twoComponents(component([]any{}), component(kept)))
	// Whole-expansion-empty root: exactly one [null] candidate.
	coded19Insert(t, svc, "br-zero", allEmptyProject, coded19Generation, "FHIR/Observation",
		twoComponents(component([]any{}), component(nil)))

	// GROUP over the mixed-branch project: exactly the one tuple, count 4
	// contributing roots, and NO (null,null,null) row — the emptied
	// branches pruned with their outer rows, contributing nothing.
	grouped := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation))))
	if len(grouped) != 1 || deref(grouped[0].system) != "sys-br" || grouped[0].version != nil ||
		deref(grouped[0].code) != "kept" || grouped[0].count != "4" {
		t.Fatalf("mixed-branch GROUP multiset = %v, want ONLY the (sys-br,null,kept) tuple with count 4 and no null tuple", grouped)
	}

	// ERROR over the same data: AQL raises nothing here, so neither may
	// PostgreSQL (the per-level bug raised CONSTRUCTION_CODED_GROUP_
	// MISSING_KEY from the spurious NULL candidates).
	if err := coded19ExecErr(svc, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, project, coded19Generation))); err != nil {
		t.Fatalf("ERROR must stay silent when every root has at least one item: %v", svc.scrub(err))
	}

	// The zero-item rule still fires exactly once per root: the wholly
	// empty expansion contributes the missing tuple (count 1) and the
	// ERROR ASSERT raises on it.
	zero := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, allEmptyProject, coded19Generation))))
	if len(zero) != 1 || zero[0].system != nil || zero[0].version != nil || zero[0].code != nil || zero[0].count != "1" {
		t.Fatalf("whole-expansion-empty root must contribute exactly the missing tuple with count 1: %v", zero)
	}
	if err := coded19ExecErr(svc, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, allEmptyProject, coded19Generation))); err == nil ||
		!strings.Contains(svc.scrub(err), codedGroupMissingKeyTag) {
		t.Fatalf("ERROR must raise on the wholly-empty expansion candidate: %v", svc.scrub(fmt.Sprint(err)))
	}

	// Alive controls: the branch roots exist, and a newly inserted
	// coding-less root lifts the missing tuple — the GROUP cell's null-
	// tuple ABSENCE above is pruning, not a query that sees no rows.
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, project); rows[0][0] != "4" {
		t.Fatalf("branch witness: %v, want 4 roots", rows)
	}
	coded19Insert(t, svc, "br-none", project, coded19Generation, "FHIR/Observation", nil)
	mutated := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation))))
	if len(mutated) != 2 || mutated[1].system != nil || mutated[1].count != "1" {
		t.Fatalf("alive control: coding-less insertion must add the missing tuple: %v", mutated)
	}
}

func TestCodedGroupLiveWindowedSource(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	const project = "p19-window"

	for index, id := range []string{"w-1", "w-2", "w-3", "w-4"} {
		_ = index
		coded19Insert(t, svc, id, project, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-window", "", "shared")))
	}
	// Alive control: the store really holds four roots; the grouping under
	// a two-row source window must see two, deterministically.
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, project); rows[0][0] != "4" {
		t.Fatalf("window witness: %v, want 4 rows", rows)
	}

	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation)
	last := len(plan.Operations) - 1
	plan.Operations = append(plan.Operations[:last],
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "srclimit"}},
		plan.Operations[last])
	plan.BindVars["srclimit"] = 2
	rendered := coded19RenderSvc(t, svc, plan)
	rows := coded19Rows(t, svc.runRendered(t, rendered))
	if len(rows) != 1 || rows[0].count != "2" || deref(rows[0].system) != "sys-window" || rows[0].version != nil || deref(rows[0].code) != "shared" {
		t.Fatalf("windowed grouping = %v, want the (sys-window,null,shared) tuple with count 2 (not the missing tuple)", rows)
	}
	repeat := coded19Rows(t, svc.runRendered(t, rendered))
	if !reflect.DeepEqual(rows, repeat) {
		t.Fatalf("the source window is not deterministic across executions: %v vs %v", rows, repeat)
	}
}
