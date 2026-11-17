package postgres

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type renderClassifierCase struct {
	name                    string
	mutate                  func(*ir.PhysicalPlan)
	wantKind                postgresRenderKind
	wantUnnest              bool
	wantUnpivot             bool
	wantGroupedPivot        bool
	wantTableShapeExclusion bool
	wantSQL                 string
	wantArgs                []command.PostgreSQLParameter
	wantColumns             []string
	wantErr                 string
}

func classifierRowLineageReturn() *ir.PhysicalRowLineageReturn {
	return &ir.PhysicalRowLineageReturn{
		RowIDBindKey:        "row_id",
		OffsetBindKey:       "offset",
		LimitBindKey:        "limit",
		FetchLimitBindKey:   "fetch_limit",
		ResourceType:        "Patient",
		ResourceIDColumn:    rowLineageResourceIDColumn,
		OccurrenceKeyColumn: rowLineageOccurrenceKeyColumn,
	}
}

func classifierGroupStage() ir.PhysicalConstructionStage {
	return ir.PhysicalConstructionStage{
		ID:   "s1",
		Kind: ir.PhysicalStageGroupOp,
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable: "rows",
			IdentityVariable:  "group_id",
		},
	}
}

func classifierDeriveStage(id string) ir.PhysicalConstructionStage {
	return ir.PhysicalConstructionStage{ID: id, Kind: ir.PhysicalStageDeriveOp}
}

func classifierCodedGroupStage(id string) ir.PhysicalConstructionStage {
	return ir.PhysicalConstructionStage{ID: id, Kind: ir.PhysicalStageCodedGroupOp}
}

func classifierCodedPivotStage(id string) ir.PhysicalConstructionStage {
	return ir.PhysicalConstructionStage{
		ID:           id,
		Kind:         ir.PhysicalStagePivotOp,
		GroupedPivot: &ir.PhysicalGroupedPivot{CodedCorrelation: &ir.PhysicalCorrelation{}},
	}
}

func classifierRelatedOccurrenceStage(id string) ir.PhysicalConstructionStage {
	return ir.PhysicalConstructionStage{
		ID:   id,
		Kind: ir.PhysicalStageDeriveOp,
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable: "rows",
			RelatedOccurrence: &ir.PhysicalStageGroupRelatedOccurrenceAdapter{RouteID: "route-1"},
		},
	}
}

func classifierStageSequence(stages ...ir.PhysicalConstructionStage) func(*ir.PhysicalPlan) {
	return func(p *ir.PhysicalPlan) {
		p.StageSequence = &ir.PhysicalStageSequence{
			SourceStageID:     "src",
			SourceRowIdentity: "row",
			Stages:            stages,
		}
	}
}

func classifierRowLineageSequence(stages ...ir.PhysicalConstructionStage) func(*ir.PhysicalPlan) {
	return func(p *ir.PhysicalPlan) {
		p.StageSequence = &ir.PhysicalStageSequence{
			SourceStageID:     "src",
			SourceRowIdentity: "row",
			Stages:            stages,
			RowLineageReturn:  classifierRowLineageReturn(),
		}
	}
}

func renderClassifierCases() []renderClassifierCase {
	cellTrace := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalCellTraceReturnOp,
			CellTraceReturn: &ir.PhysicalCellTraceReturn{
				OffsetBindKey:     "offset",
				LimitBindKey:      "limit",
				FetchLimitBindKey: "fetch_limit",
			},
		})
	}
	nilCellTrace := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations, ir.PhysicalOperation{Kind: ir.PhysicalCellTraceReturnOp})
	}
	graph := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations, ir.PhysicalOperation{Kind: ir.PhysicalPathSeedOp, PathSeed: &ir.PhysicalPathSeed{}})
	}
	unnest := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations[:1], append([]ir.PhysicalOperation{
			{Kind: ir.PhysicalUnnestOp},
		}, p.Operations[1:]...)...)
	}
	unpivot := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations[:1], append([]ir.PhysicalOperation{
			{Kind: ir.PhysicalUnpivotOp},
		}, p.Operations[1:]...)...)
	}
	groupedPivotOp := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations[:1], append([]ir.PhysicalOperation{
			{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &ir.PhysicalGroupedPivot{}},
		}, p.Operations[1:]...)...)
	}
	tableShapeExclusion := func(p *ir.PhysicalPlan) {
		p.Operations = append(p.Operations[:1], append([]ir.PhysicalOperation{
			{Kind: ir.PhysicalTableShapeExclusionReturnOp, TableShapeExclusionReturn: &ir.PhysicalTableShapeExclusionReturn{}},
		}, p.Operations[1:]...)...)
	}
	combine := func(muts ...func(*ir.PhysicalPlan)) func(*ir.PhysicalPlan) {
		return func(p *ir.PhysicalPlan) {
			for _, mutate := range muts {
				mutate(p)
			}
		}
	}
	return []renderClassifierCase{
		{
			name:     "generic",
			wantKind: postgresRenderKindGeneric,
			wantSQL: `SELECT r0."logical_id" AS "c0" FROM "pgir02_it_unit"."fhir_resource" AS r0` +
				` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
				` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
				` AND r0."resource_type" = $4 ORDER BY r0."resource_seq" ASC`,
			wantArgs: []command.PostgreSQLParameter{
				{Type: command.PostgreSQLParameterText, Value: "proj-a"},
				{Type: command.PostgreSQLParameterText, Value: "gen-1"},
				{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Observation"]`)},
				{Type: command.PostgreSQLParameterText, Value: "Patient"},
			},
			wantColumns: []string{"logical_id"},
		},
		{
			name:     "cell-trace",
			mutate:   cellTrace,
			wantKind: postgresRenderKindCellTrace,
			wantErr:  `postgres renderer: unsupported physical plan: operation "RETURN" is not part of the cell-trace render surface`,
		},
		{
			name:     "graph",
			mutate:   graph,
			wantKind: postgresRenderKindGraph,
			wantErr:  `postgres renderer: unsupported physical plan: graph plans require GRAPH_RETURN as the final operation (lower/graph_plan.go:139)`,
		},
		{
			name:     "row-lineage",
			mutate:   classifierRowLineageSequence(classifierGroupStage()),
			wantKind: postgresRenderKindRowLineage,
			wantErr:  `postgres renderer: unsupported physical plan: contributor-page row id bind "row_id" must be a string`,
		},
		{
			name:     "stage-sequence-composition",
			mutate:   classifierStageSequence(classifierDeriveStage("s1"), classifierDeriveStage("s2")),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)`,
		},
		{
			name:     "coded-group",
			mutate:   classifierStageSequence(classifierCodedGroupStage("s1")),
			wantKind: postgresRenderKindCodedGroup,
			wantErr:  `postgres renderer: unsupported physical plan: operation "CODED_GROUP" is not part of the registered coded-group slice`,
		},
		{
			name:     "related-occurrence",
			mutate:   classifierStageSequence(classifierRelatedOccurrenceStage("s1")),
			wantKind: postgresRenderKindStageSequenceRelatedOccurrence,
			wantErr:  `postgres renderer: unsupported physical plan: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)`,
		},
		{
			name:     "group",
			mutate:   classifierStageSequence(classifierGroupStage()),
			wantKind: postgresRenderKindGroup,
			wantErr:  `postgres renderer: unsupported physical plan: the registered slice admits only the inline first-stage GROUP (InputStageID must equal "src"); composed sequences are a pg-sql-16 capability gap (L-13.1)`,
		},
		{
			name:     "grouped-coded-pivot",
			mutate:   classifierStageSequence(classifierCodedPivotStage("s1")),
			wantKind: postgresRenderKindGroupedCodedPivot,
			wantErr:  `postgres renderer: unsupported physical plan: the coded-pivot door admits only coded categories`,
		},
		{
			name:       "unnest",
			mutate:     unnest,
			wantKind:   postgresRenderKindGeneric,
			wantUnnest: true,
			wantErr:    `postgres renderer: unsupported physical plan: operation 1: UNNEST payload is missing`,
		},
		{
			name:        "unpivot",
			mutate:      unpivot,
			wantKind:    postgresRenderKindGeneric,
			wantUnpivot: true,
			wantErr:     `postgres renderer: unsupported physical plan: operation 1: UNPIVOT payload is missing`,
		},
		{
			name:             "grouped-pivot",
			mutate:           groupedPivotOp,
			wantKind:         postgresRenderKindGroupedPivot,
			wantGroupedPivot: true,
			wantErr:          `postgres renderer: unsupported physical plan: pivot RETURN projection "logical_id" is not a registered pivot output`,
		},
		{
			name:                    "table-shape-exclusion",
			mutate:                  tableShapeExclusion,
			wantKind:                postgresRenderKindTableShapeExclusion,
			wantTableShapeExclusion: true,
			wantErr:                 `postgres renderer: unsupported physical plan: the evidence terminal must be the final operation, got position 1 of 3`,
		},
		{
			name:     "stage-sequence-payload",
			mutate:   classifierStageSequence(classifierDeriveStage("s1")),
			wantKind: postgresRenderKindStageSequencePayload,
			wantErr:  `postgres renderer: unsupported physical plan: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)`,
		},
		{
			name:     "cell-trace-beats-unpivot",
			mutate:   combine(cellTrace, unpivot),
			wantKind: postgresRenderKindCellTrace,
			wantErr:  `postgres renderer: unsupported physical plan: operation "UNPIVOT" is not part of the cell-trace render surface`,
		},
		{
			name:        "nil-cell-trace-falls-to-unpivot",
			mutate:      combine(nilCellTrace, unpivot),
			wantKind:    postgresRenderKindGeneric,
			wantUnpivot: true,
			wantErr:     `postgres renderer: unsupported physical plan: operation 1: UNPIVOT payload is missing`,
		},
		{
			name:     "cell-trace-beats-graph",
			mutate:   combine(cellTrace, graph),
			wantKind: postgresRenderKindCellTrace,
			wantErr:  `postgres renderer: unsupported physical plan: operation "RETURN" is not part of the cell-trace render surface`,
		},
		{
			name:     "cell-trace-inert-under-stage-sequence",
			mutate:   combine(cellTrace, classifierStageSequence(classifierDeriveStage("s1"), classifierDeriveStage("s2"))),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: the composition source requires a terminal RETURN with its projection list`,
		},
		{
			name:     "graph-beats-stage-sequence-composition",
			mutate:   combine(graph, classifierStageSequence(classifierDeriveStage("s1"), classifierDeriveStage("s2"))),
			wantKind: postgresRenderKindGraph,
			wantErr:  `postgres renderer: unsupported physical plan: graph plans require GRAPH_RETURN as the final operation (lower/graph_plan.go:139)`,
		},
		{
			name:     "row-lineage-yields-to-composition",
			mutate:   classifierRowLineageSequence(classifierGroupStage(), classifierDeriveStage("s2")),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)`,
		},
		{
			name: "row-lineage-yields-to-cell-trace-terminal",
			mutate: func(p *ir.PhysicalPlan) {
				classifierRowLineageSequence(classifierGroupStage())(p)
				p.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
			},
			wantKind: postgresRenderKindGroup,
			wantErr:  `postgres renderer: unsupported physical plan: the registered slice admits only the inline first-stage GROUP (InputStageID must equal "src"); composed sequences are a pg-sql-16 capability gap (L-13.1)`,
		},
		{
			name: "row-lineage-yields-to-population-mapping-terminal",
			mutate: func(p *ir.PhysicalPlan) {
				classifierRowLineageSequence(classifierGroupStage())(p)
				p.StageSequence.PopulationMappingReturn = &ir.PhysicalStagePopulationMappingReturn{}
			},
			wantKind: postgresRenderKindGroup,
			wantErr:  `postgres renderer: unsupported physical plan: the registered slice admits only the inline first-stage GROUP (InputStageID must equal "src"); composed sequences are a pg-sql-16 capability gap (L-13.1)`,
		},
		{
			name:     "composition-beats-coded-group-and-group",
			mutate:   classifierStageSequence(classifierGroupStage(), classifierCodedGroupStage("s2")),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap`,
		},
		{
			name: "related-occurrence-beats-group",
			mutate: func(p *ir.PhysicalPlan) {
				stage := classifierGroupStage()
				stage.Group.RelatedOccurrence = &ir.PhysicalStageGroupRelatedOccurrenceAdapter{RouteID: "route-1"}
				classifierStageSequence(stage)(p)
			},
			wantKind: postgresRenderKindStageSequenceRelatedOccurrence,
			wantErr:  `postgres renderer: unsupported physical plan: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)`,
		},
		{
			name:     "composition-beats-group-and-coded-pivot",
			mutate:   classifierStageSequence(classifierGroupStage(), classifierCodedPivotStage("s2")),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: a coded Pivot at stage index 1 violates the ir rule that the coded Pivot must be the first stage over the direct source projection (ir/physical_stage_validation.go:106-108); coded-pivot composition beyond stage 1 stays the pg-sql-22/23 adjacency`,
		},
		{
			name:     "stage-sequence-composition-beats-grouped-coded-pivot",
			mutate:   classifierStageSequence(classifierCodedPivotStage("s1"), classifierDeriveStage("s2")),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: multi-stage construction sequences are the pg-sql-16 capability; the registered slice admits only the inline single-stage CODED_PIVOT`,
		},
		{
			name:             "unpivot-validates-before-grouped-pivot",
			mutate:           combine(groupedPivotOp, unpivot),
			wantKind:         postgresRenderKindGroupedPivot,
			wantUnpivot:      true,
			wantGroupedPivot: true,
			wantErr:          `postgres renderer: unsupported physical plan: operation 1: UNPIVOT payload is missing`,
		},
		{
			name:             "unnest-validates-before-unpivot",
			mutate:           combine(groupedPivotOp, unpivot, unnest),
			wantKind:         postgresRenderKindGroupedPivot,
			wantUnnest:       true,
			wantUnpivot:      true,
			wantGroupedPivot: true,
			wantErr:          `postgres renderer: unsupported physical plan: operation 1: UNNEST payload is missing`,
		},
		{
			name:                    "unnest-validates-before-table-shape-exclusion",
			mutate:                  combine(tableShapeExclusion, unnest),
			wantKind:                postgresRenderKindTableShapeExclusion,
			wantUnnest:              true,
			wantTableShapeExclusion: true,
			wantErr:                 `postgres renderer: unsupported physical plan: operation 1: UNNEST payload is missing`,
		},
		{
			name:                    "grouped-pivot-beats-table-shape-exclusion",
			mutate:                  combine(groupedPivotOp, tableShapeExclusion),
			wantKind:                postgresRenderKindGroupedPivot,
			wantGroupedPivot:        true,
			wantTableShapeExclusion: true,
			wantErr:                 `postgres renderer: unsupported physical plan: operation "TABLE_SHAPE_EXCLUSION_RETURN" cannot coexist with GROUPED_PIVOT in the registered slice; filters beyond the canonical scope, traversals, unnests, sets, subplans, and every other shape belong to the read spine or the pg-sql-16 composition capability`,
		},
		{
			name:        "unpivot-validates-before-stage-sequence-payload",
			mutate:      combine(classifierStageSequence(classifierDeriveStage("s1")), unpivot),
			wantKind:    postgresRenderKindStageSequencePayload,
			wantUnpivot: true,
			wantErr:     `postgres renderer: unsupported physical plan: operation 1: UNPIVOT payload is missing`,
		},
		{
			name:     "graph-beats-grouped-pivot",
			mutate:   combine(graph, groupedPivotOp),
			wantKind: postgresRenderKindGraph,
			wantErr:  `postgres renderer: unsupported physical plan: graph plans require GRAPH_RETURN as the final operation (lower/graph_plan.go:139)`,
		},
		{
			name:     "row-lineage-beats-unpivot-validation",
			mutate:   combine(classifierRowLineageSequence(classifierGroupStage()), unpivot),
			wantKind: postgresRenderKindRowLineage,
			wantErr:  `postgres renderer: unsupported physical plan: contributor-page row id bind "row_id" must be a string`,
		},
		{
			name:                    "table-shape-exclusion-beats-stage-sequence-payload",
			mutate:                  combine(classifierStageSequence(classifierDeriveStage("s1")), tableShapeExclusion),
			wantKind:                postgresRenderKindTableShapeExclusion,
			wantTableShapeExclusion: true,
			wantErr:                 `postgres renderer: unsupported physical plan: an exclusion terminal combined with a stage sequence has no PostgreSQL rendering (staged compositions are pg-sql-16)`,
		},
		{
			name:     "group-stage-shields-bad-unpivot",
			mutate:   combine(classifierStageSequence(classifierGroupStage()), unpivot),
			wantKind: postgresRenderKindGroup,
			wantErr:  `postgres renderer: unsupported physical plan: the registered slice admits only the inline first-stage GROUP (InputStageID must equal "src"); composed sequences are a pg-sql-16 capability gap (L-13.1)`,
		},
		{
			name:     "composition-shields-bad-unnest",
			mutate:   combine(classifierStageSequence(classifierDeriveStage("s1"), classifierDeriveStage("s2")), unnest),
			wantKind: postgresRenderKindStageSequenceComposition,
			wantErr:  `postgres renderer: unsupported physical plan: operation 1: unnest payload is missing`,
		},
		{
			name:     "generic-empty-operations",
			mutate:   func(p *ir.PhysicalPlan) { p.Operations = nil },
			wantKind: postgresRenderKindGeneric,
			wantErr:  `validate physical plan: physical plan requires exactly one root scan`,
		},
		{
			name:     "generic-engine-clickhouse",
			mutate:   func(p *ir.PhysicalPlan) { p.Engine = ir.PhysicalEngineClickHouse },
			wantKind: postgresRenderKindGeneric,
			wantErr:  `validate physical plan: ClickHouse physical plan requires a typed combine payload`,
		},
	}
}

func classifierPlan(t *testing.T, mutate func(*ir.PhysicalPlan)) ir.PhysicalPlan {
	t.Helper()
	plan := basePlan()
	if mutate != nil {
		mutate(&plan)
	}
	return plan
}

func TestRenderPlanClassifierPrecedenceMatrix(t *testing.T) {
	for _, tc := range renderClassifierCases() {
		plan := classifierPlan(t, tc.mutate)
		got := classifyPostgresRenderPlan(plan)
		if got.kind != tc.wantKind {
			t.Errorf("%s: kind = %v, want %v", tc.name, got.kind, tc.wantKind)
		}
		if got.unnestRequested != tc.wantUnnest {
			t.Errorf("%s: unnestRequested = %t, want %t", tc.name, got.unnestRequested, tc.wantUnnest)
		}
		if got.unpivotRequested != tc.wantUnpivot {
			t.Errorf("%s: unpivotRequested = %t, want %t", tc.name, got.unpivotRequested, tc.wantUnpivot)
		}
		if got.groupedPivotRequested != tc.wantGroupedPivot {
			t.Errorf("%s: groupedPivotRequested = %t, want %t", tc.name, got.groupedPivotRequested, tc.wantGroupedPivot)
		}
		if got.tableShapeExclusionRequested != tc.wantTableShapeExclusion {
			t.Errorf("%s: tableShapeExclusionRequested = %t, want %t", tc.name, got.tableShapeExclusionRequested, tc.wantTableShapeExclusion)
		}
	}
}

func TestRenderPlanDispatcherMatchesClassifierMatrix(t *testing.T) {
	for _, tc := range renderClassifierCases() {
		plan := classifierPlan(t, tc.mutate)
		rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
		if tc.wantErr != "" {
			if err == nil {
				t.Errorf("%s: render succeeded with SQL %q, want error %q", tc.name, rendered.SQL, tc.wantErr)
				continue
			}
			if err.Error() != tc.wantErr {
				t.Errorf("%s: error = %q, want %q", tc.name, err.Error(), tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: render error = %v, want exact SQL", tc.name, err)
			continue
		}
		if rendered.SQL != tc.wantSQL {
			t.Errorf("%s: SQL mismatch:\n got %s\nwant %s", tc.name, rendered.SQL, tc.wantSQL)
		}
		if !reflect.DeepEqual(rendered.Args, tc.wantArgs) {
			t.Errorf("%s: args = %#v, want %#v", tc.name, rendered.Args, tc.wantArgs)
		}
		if !reflect.DeepEqual(rendered.OutputColumns, tc.wantColumns) {
			t.Errorf("%s: output columns = %v, want %v", tc.name, rendered.OutputColumns, tc.wantColumns)
		}
	}
}

func TestRenderPlanClassifierKindNamesAreClosed(t *testing.T) {
	seen := map[postgresRenderKind]bool{}
	for _, tc := range renderClassifierCases() {
		kind := classifyPostgresRenderPlan(classifierPlan(t, tc.mutate)).kind
		if seen[kind] {
			continue
		}
		seen[kind] = true
		if kind.String() == "unknown" {
			t.Errorf("kind %d has no closed name", uint8(kind))
		}
	}
	for kind := postgresRenderKind(0); kind <= postgresRenderKindStageSequencePayload; kind++ {
		if !seen[kind] {
			t.Errorf("classifier matrix never selects kind %v", kind)
		}
	}
}
