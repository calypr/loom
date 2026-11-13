package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
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

func TestCodedGroupRenderedSliceContract(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	rendered := renderOK(t, plan)
	sql := rendered.SQL

	for _, want := range []string{
		`WITH cg_src AS (`,
		`LEFT JOIN LATERAL (SELECT cgx2.elem FROM jsonb_array_elements(CASE WHEN jsonb_typeof(`,
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
	if got := strings.Count(sql, "LEFT JOIN LATERAL"); got != 1 {
		t.Fatalf("wrapping LEFT JOIN LATERAL count = %d, want exactly 1:\n%s", got, sql)
	}
	if strings.Contains(sql, " WHERE c.cg_valid") {
		t.Fatalf("GROUP policy must not filter candidates:\n%s", sql)
	}
	if strings.Contains(sql, "@") {
		t.Fatalf("statement carries AQL bind syntax:\n%s", sql)
	}
	for _, leaked := range []string{"proj-a", "gen-1", "FHIR/Observation", "Observation", "component", "coding", "group_codes", coded19CodingPath, "CONSTRUCTION_CODED_GROUP_MISSING_KEY"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text:\n%s", leaked, sql)
		}
	}
	if !strings.Contains(sql, `#> $`) {
		t.Fatalf("payload paths must bind through #> text[] parameters:\n%s", sql)
	}
	if got := strings.Count(sql, "btrim("); got != 3 {
		t.Fatalf("btrim gates = %d, want 3 (system+code validity, version key):\n%s", got, sql)
	}
	if got := strings.Count(sql, ` COLLATE "C"`); got != 1 {
		t.Fatalf(`identity COLLATE "C" terms = %d, want 1:\n%s`, got, sql)
	}

	want := []string{"code_system", "code_version", "code", "source_records", "__loom_row_id"}
	if !reflect.DeepEqual(rendered.OutputColumns, want) {
		t.Fatalf("output columns = %#v, want %#v", rendered.OutputColumns, want)
	}

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

func TestCodedGroupRefusalRegister(t *testing.T) {
	base := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")

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
			name: "row_value_input_many_wave2_gap",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded19AttachIRRowValuesPolicy(t, plan, "ONE", true)
			},
			pin: coded19ManyRowValuesRefusalPin,
		},
		{
			name: "row_value_input_without_projection",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.RowValues = []ir.PhysicalStageRowValue{{
					InputColumn: "resource_id", InputKind: "STRING", Output: "row_value_1", Policy: "ONE", Variable: "cg19_rv_var",
				}}
			},
			pin: "needs exactly one source projection",
		},
		{
			name: "row_value_projections_without_row_values",
			mutate: func(t *testing.T, plan *ir.PhysicalPlan) {
				coded := plan.StageSequence.Stages[0].CodedGroup
				coded.RowValueProjections = []ir.PhysicalProjection{{Name: "resource_id", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}}
			},
			pin: "addresses no row value input column",
		},
		{
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

	broken := ir.ClonePhysicalPlan(plan)
	broken.StageSequence.Stages[0].CodedGroup.SourceRowsUnique = false
	entryErr := errorOf(RenderPhysicalPlan(broken, mustBinding(t)))
	doorErr := errorOf(renderCodedGroupPhysicalPlan(broken, mustBinding(t)))
	if entryErr == nil || doorErr == nil || entryErr.Error() != doorErr.Error() {
		t.Fatalf("doors must refuse with one message: entry=%v door=%v", entryErr, doorErr)
	}
}

func errorOf(_ any, err error) error { return err }

const coded19ManyRowValuesRefusalPin = "coded-group many-cardinality (local-array-expansion) row values are a wave-2 pg-sql-13 gap"

func coded19AttachIRRowValues(t *testing.T, plan *ir.PhysicalPlan) {
	t.Helper()
	coded19AttachIRRowValuesPolicy(t, plan, "ONE", false)
}

func coded19AttachIRRowValuesPolicy(t *testing.T, plan *ir.PhysicalPlan, policy string, many bool) {
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
		InputColumn: "resource_id", InputKind: "STRING", InputMany: many,
		Output: "row_value_1", Policy: policy, Variable: "cg19_rv_var",
	}}
	coded.RowValueProjections = []ir.PhysicalProjection{resourceID}
	stage := &plan.StageSequence.Stages[0]
	stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{Name: "row_value_1", Value: ir.PhysicalValue{Variable: "cg19_rv_var"}})
	cardinality := "many"
	nullable := false
	if policy == "ONE" {
		cardinality = "optional_one"
		nullable = true
	}
	column := ir.PhysicalStageColumn{ID: "rv_id", Name: "row_value_1", Label: "row_value_1", Kind: "string", Cardinality: cardinality, Nullable: nullable}
	stage.OutputColumns = append(stage.OutputColumns, column)
	plan.StageSequence.FinalColumns = append(plan.StageSequence.FinalColumns, column)
}

func TestCodedGroupIRRowValuesAliveControl(t *testing.T) {
	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, "proj-a", "gen-1")
	coded19AttachIRRowValues(t, &plan)
	if err := plan.Validate(); err != nil {
		t.Fatalf("the producer channel must accept this shape (probe-banked ACCEPT+EMIT): %v", err)
	}
	shared, err := groupRowValueExpressionFor("ONE", "STRING", false,
		&groupColumn{name: "resource_id", alias: "cg_rv0", ref: `"cg_rv0"`, jsonb: true},
		func(index int) string { return "$" + strconv.Itoa(index) })
	if err != nil {
		t.Fatalf("shared emitter: %v", err)
	}
	normalize := regexp.MustCompile(`\$[0-9]+`).ReplaceAllString(shared, "$N")
	for label, render := range map[string]func(ir.PhysicalPlan, ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error){
		"entry": RenderPhysicalPlan,
		"door":  renderCodedGroupPhysicalPlan,
	} {
		rendered, err := render(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if err != nil {
			t.Fatalf("%s must render the registered row-values arm (pg-sql-13 Q4): %v", label, err)
		}
		for _, want := range []string{
			` AS cg_payload, `, ` AS cg_rv0`, `, s.cg_rv0`, `, c.cg_rv0`,
			`AS cg_g0`, `g.cg_g0 AS "c5"`,
		} {
			if !strings.Contains(rendered.SQL, want) {
				t.Fatalf("%s arm is missing %q:\n%s", label, want, rendered.SQL)
			}
		}
		if normalized := regexp.MustCompile(`\$[0-9]+`).ReplaceAllString(rendered.SQL, "$N"); !strings.Contains(normalized, normalize+` AS cg_g0`) {
			t.Fatalf("%s must emit the SHARED ONE fragment byte-identical (same-emitter law, Q4):\n%s", label, rendered.SQL)
		}
		if len(rendered.OutputColumns) != 6 {
			t.Fatalf("%s outputs = %d, want five tuple members plus the row value", label, len(rendered.OutputColumns))
		}
	}
}

func TestCodedGroupRowValuesSharedEmitterLaw(t *testing.T) {
	source, err := os.ReadFile("coded_group.go")
	if err != nil {
		t.Fatalf("read coded_group.go: %v", err)
	}
	var code []string
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		code = append(code, line)
	}
	text := strings.Join(code, "\n")
	for _, want := range []string{`groupRowValueExpressionFor(`, `groupColumnRead(`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the coded-group row-values arm must dispatch through the shared emitter %q", want)
		}
	}
	for _, forbidden := range []string{`ARRAY_AGG(DISTINCT `, `CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES`, `CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("coded_group.go carries an inlined emitter literal %q — one emitter only (Q4)", forbidden)
		}
	}
}

func TestCodedGroupIRRowValuesLiveZeroSideEffects(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	coded19Insert(t, svc, "rvw-1", coded19Project, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-rv", "", "kept")))

	plan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, coded19Project, coded19Generation)
	coded19AttachIRRowValuesPolicy(t, &plan, "ONE", true)

	before := coded19Inventory(t, svc)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err == nil || !strings.Contains(err.Error(), coded19ManyRowValuesRefusalPin) {
		t.Fatalf("refusal required before any SQL; got rendered=%v err=%v", rendered.SQL, err)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 || len(rendered.OutputColumns) != 0 {
		t.Fatalf("a refused plan must carry zero SQL, zero parameters, zero columns: %#v", rendered)
	}
	if after := coded19Inventory(t, svc); after != before {
		t.Fatalf("the refused many row-values plan changed service state:\n before %q\n after  %q", before, after)
	}
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1`, coded19Project); rows[0][0] != "1" {
		t.Fatalf("alive witness: %v, want the single inserted row", rows)
	}
}

func TestCodedGroupRowValuesLiveRender(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	onePayload := codingPayload(coding("sys-rv", "", "kept"))
	onePayload["id"] = "rv-a"
	coded19Insert(t, svc, "rvw-a", coded19Project, coded19Generation, "FHIR/Observation", onePayload)

	onePlan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, coded19Project, coded19Generation)
	coded19AttachIRRowValuesPolicy(t, &onePlan, "ONE", false)
	oneRendered, err := RenderPhysicalPlan(onePlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("registered row-values arm must render: %v", err)
	}
	rows := svc.runRendered(t, oneRendered)
	if len(rows) != 1 || rows[0][0] != "sys-rv" || rows[0][2] != "kept" || rows[0][3] != "1" || rows[0][5] != "rv-a" {
		t.Fatalf("ONE over one contributing root: %v", rows)
	}

	allPlan := coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, coded19Project, coded19Generation)
	coded19AttachIRRowValuesPolicy(t, &allPlan, "ALL", false)
	allRendered, err := RenderPhysicalPlan(allPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("registered row-values arm must render (ALL): %v", err)
	}

	secondPayload := codingPayload(coding("sys-rv", "", "kept"))
	secondPayload["id"] = "rv-b"
	coded19Insert(t, svc, "rvw-b", coded19Project, coded19Generation, "FHIR/Observation", secondPayload)
	allRows := svc.runRendered(t, allRendered)
	if len(allRows) != 1 || allRows[0][3] != "2" || allRows[0][5] != `[rv-a rv-b]` {
		t.Fatalf("ALL over two contributing roots: %v", allRows)
	}
	group13ExpectRaise(t, svc, oneRendered, groupRowValueMultipleTag)

	svc.witness(t, `DELETE FROM "`+svc.schema+`"."fhir_resource" WHERE logical_id = 'rvw-b'`)
	if again := svc.runRendered(t, oneRendered); len(again) != 1 || again[0][5] != "rv-a" {
		t.Fatalf("ONE after removing the second root: %v", again)
	}
}

func coded19Inventory(t *testing.T, svc *pgir02Service) string {
	t.Helper()
	tables := svc.witness(t,
		`SELECT c.relname, c.reltuples::bigint FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace`+
			` WHERE n.nspname = $1 AND c.relkind = 'r' ORDER BY 1`, svc.schema)
	resourceRows := svc.witness(t,
		`SELECT count(*), coalesce(md5(string_agg(payload::text, ',' ORDER BY resource_seq)), '') FROM "`+svc.schema+`"."fhir_resource"`)
	return strings.Join([]string{fmt.Sprint(tables), fmt.Sprint(resourceRows)}, "|")
}

const coded19SetRefusalMessage = "postgres renderer: unsupported physical plan: coded-group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; probe-attested L-19.4) and is refused, never partially rendered"

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
					continue
				}
				if strings.Contains(line, "btrim(") {
					if name != "calls.go" {
						t.Fatalf("%s introduces a btrim call outside the registered coded-group gate file: %s", name, trimmed)
					}
				}
				if strings.Contains(line, "codedGroupTrimCharacters") {
					t.Fatalf("%s uses the trim-set constant outside the registered coded-group gate file: %s", name, trimmed)
				}
			}
		}
		if name != "coded_group.go" {
			continue
		}
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

func newCoded19Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "coded19_it")
}

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
	svc.insertResource(t, svc.schema, coded19Project, coded19Generation, "Observation", "obs-nonarray", "FHIR/Observation",
		map[string]any{"component": []any{map[string]any{"code": map[string]any{"coding": coding("system-x", "", "y")}}}})
	svc.insertResource(t, svc.schema, coded19Project, coded19Generation, "Observation", "obs-nullitem", "FHIR/Observation",
		map[string]any{"component": []any{map[string]any{"code": map[string]any{"coding": []any{nil}}}}})

	coded19Insert(t, svc, "obs-denied", coded19Project, coded19Generation, "FHIR/Hidden", codingPayload(coding("system-a", "", "denied")))
	coded19Insert(t, svc, "obs-oldgen", coded19Project, "p19-old-gen", "FHIR/Observation", codingPayload(coding("system-a", "", "oldgen")))
	coded19Insert(t, svc, "obs-foreign", "p19-foreign", coded19Generation, "FHIR/Observation", codingPayload(coding("system-a", "", "foreign")))

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

	repeated := coded19Rows(t, svc.runRendered(t, rendered))
	if !reflect.DeepEqual(rows, repeated) {
		t.Fatalf("grouping changed between executions:\n%v\n%v", rows, repeated)
	}

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

	errorRendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, coded19Project, coded19Generation))
	if err := coded19ExecErr(svc, errorRendered); err == nil {
		t.Fatalf("ERROR policy must raise on the invalid candidates")
	} else if !strings.Contains(svc.scrub(err), codedGroupMissingKeyTag) {
		t.Fatalf("ERROR policy error must carry the canonical tag: %v", svc.scrub(err))
	} else if !strings.Contains(svc.scrub(err), "22P02") {
		t.Fatalf("ERROR guard must raise through the barrier cast (22P02): %v", svc.scrub(err))
	}

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

	const allInvalidProject = "p19-all-invalid"
	coded19Insert(t, svc, "inv-1", allInvalidProject, coded19Generation, "FHIR/Observation", codingPayload(coding("", "", "shared")))
	coded19Insert(t, svc, "inv-2", allInvalidProject, coded19Generation, "FHIR/Observation", nil)

	exclude := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyExclude, allInvalidProject, coded19Generation))
	if rows := svc.runRendered(t, exclude); len(rows) != 0 {
		t.Fatalf("EXCLUDE over all-invalid data must group nothing: %v", rows)
	}
	grouped := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, allInvalidProject, coded19Generation))))
	if len(grouped) != 1 || grouped[0].count != "2" || grouped[0].system != nil {
		t.Fatalf("GROUP sabotage control: %v, want one missing tuple with count 2", grouped)
	}
	errorRendered := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, allInvalidProject, coded19Generation))
	if err := coded19ExecErr(svc, errorRendered); err == nil ||
		!strings.Contains(svc.scrub(err), codedGroupMissingKeyTag) {
		t.Fatalf("ERROR must raise at zero grouped rows: %v", svc.scrub(fmt.Sprint(err)))
	}
	empty := coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, "p19-no-rows", coded19Generation))
	if rows := svc.runRendered(t, empty); len(rows) != 0 {
		t.Fatalf("zero-source ERROR must return zero rows: %v", rows)
	}
	const healthyProject = "p19-healthy"
	coded19Insert(t, svc, "ok-1", healthyProject, coded19Generation, "FHIR/Observation", codingPayload(coding("sys-h", "", "c1")))
	healthy := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc, coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, healthyProject, coded19Generation))))
	if len(healthy) != 1 || deref(healthy[0].system) != "sys-h" || healthy[0].count != "1" {
		t.Fatalf("healthy ERROR control: %v", healthy)
	}

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

	coded19Insert(t, svc, "ws-1", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-w", "", "\tT"), coding("sys-w", "", "\nN"), coding("sys-w", "", " U"),
	))
	coded19Insert(t, svc, "ws-2", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("  ", "", "ws-invalid"), coding("\t", "", "tab-system"),
	))
	coded19Insert(t, svc, "ws-3", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-w", "", "null"),
	))
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
	if counts[tuple{"<nil>", "<nil>", "<nil>"}] != "1" {
		t.Fatalf("missing tuple must carry exactly the blank-system coding's root: %v", rows)
	}
	if _, leaked := counts[tuple{"  ", "<nil>", "ws-invalid"}]; leaked {
		t.Fatalf("space-blank system must not survive validity: %v", rows)
	}

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

func TestCodedGroupLiveProductionCompiledGoldenDedupTieBreak(t *testing.T) {
	svc := newCoded19Service(t)
	coded19TrackFixtures(t, svc)
	const project = "p19-prod"

	coded19Insert(t, svc, "prod-a", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-one", "", "shared"),
		coding("sys-one", "", "shared"),
		coding("sys-two", "", "shared"),
	))
	coded19Insert(t, svc, "prod-b", project, coded19Generation, "FHIR/Observation", codingPayload(
		coding("sys-one", "v1", "shared"),
	))
	coded19Insert(t, svc, "prod-missing", project, coded19Generation, "FHIR/Observation", nil)

	plan := coded19ProductionCompiledPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation)
	rendered := coded19RenderSvc(t, svc, plan)
	rows := coded19Rows(t, svc.runRendered(t, rendered))

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
	if seen["sys-one|<nil>|shared"] != "1" {
		t.Fatalf("dedup leg: duplicate identical coding counted %q roots, want 1", seen["sys-one|<nil>|shared"])
	}
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
	if repeat := coded19Rows(t, svc.runRendered(t, rendered)); !reflect.DeepEqual(rows, repeat) {
		t.Fatalf("production-compiled grouping changed between executions:\n%v\n%v", rows, repeat)
	}
}

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
	coded19Insert(t, svc, "br-empty-first", project, coded19Generation, "FHIR/Observation",
		twoComponents(component([]any{}), component(kept)))
	coded19Insert(t, svc, "br-zero", allEmptyProject, coded19Generation, "FHIR/Observation",
		twoComponents(component([]any{}), component(nil)))

	grouped := coded19Rows(t, svc.runRendered(t, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyGroup, project, coded19Generation))))
	if len(grouped) != 1 || deref(grouped[0].system) != "sys-br" || grouped[0].version != nil ||
		deref(grouped[0].code) != "kept" || grouped[0].count != "4" {
		t.Fatalf("mixed-branch GROUP multiset = %v, want ONLY the (sys-br,null,kept) tuple with count 4 and no null tuple", grouped)
	}

	if err := coded19ExecErr(svc, coded19RenderSvc(t, svc,
		coded19LoweredPlan(t, recipe.ConstructionGroupMissingKeyError, project, coded19Generation))); err != nil {
		t.Fatalf("ERROR must stay silent when every root has at least one item: %v", svc.scrub(err))
	}

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
