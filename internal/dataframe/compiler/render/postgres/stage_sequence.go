package postgres

// The composed construction stage sequence (pg-sql-16) is registered here as
// its own closed plan slice: the source region (the group slice's spine arms
// PLUS the UNNEST/SET arms that close L-13.2), one CTE per construction
// stage over the immediately prior relation, and the terminal projection
// with the registered order and preview limit. Dispatch is the additive
// composition door in RenderPhysicalPlan (after the graph door, before the
// four inline slice doors) plus the conversion of the former blanket
// StageSequence refusal: every plan with two or more stages, and every
// single-stage shape no inline door claims, lands behind
// validateStageSequencePlanSurface — the single total contract for the
// slice (render.go / graph.go / coded_group.go / group.go / pivot.go
// precedent; entry==dispatch message equality holds for every shape).
//
// AQL ground truth is render/aql/stage_sequence.go :264-533 — the LET-chain
// ported branch-for-branch: the source LET maps to __loom_src, each stage
// LET maps to __loom_stage_N (stage N+1 reads ONLY stage N's registered
// output columns — typed intermediate rows: SQL scalar types or jsonb,
// never reparsed SQL, never AQL), and the terminal FOR/SORT/LIMIT/RETURN
// maps to the final SELECT. Stage payloads are rendered by the registered
// slice emitters repointed at the prior relation (group.go core, pivot.go
// cell/identity core, unpivot.go melt core, the EXPAND port here) — reuse
// is by repointing, not string surgery, and the inline single-stage doors
// render byte-identically (golden-pinned: the S0 byte-preservation mutant
// suite).
//
// Registered composition surface (rendered):
//
//   - Source region: the group-slice whitelist (one direct ROOT_SCAN +
//     canonical scope filters + the canonical AUTH_RESOURCE_PATH_ALLOWED
//     let + renderable spine filters, at most one SORT then one LIMIT, a
//     terminal RETURN) PLUS UNNEST (unnest.go arms) and SET (sets.go child
//     sets) — the L-13.2 closure: the SET arm exists and the silent-drop
//     counterfactual is RED (L-19.4 lesson honored). Population/cohort/
//     candidate-seed roots stay refused (population.go gate).
//   - Stage kinds: FILTER, DERIVE, EXPAND, UNPIVOT, GROUP, and the staged
//     NON-coded PIVOT (the C3/S4 closure — categories, system, version, and
//     code stay $n parameters, never SQL identifiers). Each stage CTE
//     selects exactly its declared OutputColumns positionally as c0..cN
//     (renderer-minted aliases; no plan column name reaches SQL text) and
//     carries its RowIdentityColumn as the identity column.
//   - Terminal: SELECT <FinalColumns reads> FROM __loom_stage_last ORDER BY
//     <registered order> [LIMIT $n]. LIMIT binds PreviewLimitBindKey as
//     one bigint $n; a non-int or non-positive bind is a typed refusal
//     (render/aql/stage_sequence.go:452-453 twin).
//
// Terminal order (D-16a, mandatory attribution):
//
//	CTE terminal re-sort over GROUP-last per coordinator ruling 2026-11-11;
//	SQL CTE order unspecified; refines registered group total order.
//
// A GROUP-last stage re-applies exactly the group slice's registered total
// order (keys ASC NULLS FIRST, identity COLLATE "C" ASC, D-13c citations
// kept verbatim, not re-litigated); every other last stage sorts
// <identity> COLLATE "C" ASC (render/aql/stage_sequence.go:450 twin). The
// ORDER BY non-vacuity ALIVE control is live-pinned (tie rows distinguished
// by the COLLATE "C" tiebreak, the #130-era tie-ambiguity technique).
//
// Negative-index selector steps over typed intermediate rows are registered
// here (unit_normalization.go arm, oracle-pinned): ArangoDB 3.12.4-3
// negative subscripts are TAIL INDEXES (n=-1 reads the last element,
// n<=-len-1 reads null; objects subscript by the decimal key string for
// both signs; a non-array subscript reads null) — receipts o1-idx-*.wire.
// The PostgreSQL twin: array n<0 reads col -> (jsonb_array_length(col) + n)
// with the n <= -len-1 arm reading NULL; objects read the key text(n); any
// other stored kind reads NULL. Comparison-class cells pin COLLATE "C" with
// ASCII-lowercase fixtures (o2b receipts; the D-13b collation class).
//
// Refusal register (probe receipts under this session's logs/; L-numbers
// per the ratified plan §6, all closed-or-refused classes C-01..C-18 of the
// coordinator's A2 receipt battery are typed — no silent path):
//
//   - L-16.1 non-adjacent/foreign stage reference: the ir chain rules
//     (physical_stage_validation.go:53-73,275-290) re-read at the door;
//     chain skip, wrong first-stage input, duplicate/empty/untrimmed IDs,
//     foreign FinalStageID, FinalRowIdentity/FinalColumns schema drift, and
//     a stage whose InputStageID references a NON-ADJACENT earlier stage
//     (the CTE chain reads exactly the prior CTE). Typed refusal, never
//     partially rendered (acceptance clause 2).
//   - L-16.2 CODED_GROUP / COHORT_GROUP inside a composition: registered
//     refusal in the coded-group voice (the coded_group.go:436 message text
//     verbatim); nothing in pg-sql-22/23/25/26 needs a composed CODED_GROUP
//     today (coordinator A3 ruling 2026-11-11). Enabling ticket registers
//     when real demand lands.
//   - L-16.3 PIVOT stage with CodedCorrelation: stage-0 coded PIVOT with
//     trailing stages refuses in the coded-pivot voice (the pivot.go:1304
//     message verbatim); index>0 is unreachable from lowering
//     (ir/physical_stage_validation.go:106-108) and hand-assembled shapes
//     refuse in the same voice. Coded-pivot composition stays the
//     pg-sql-22/23 adjacency.
//   - L-16.4 diagnostic returns / preview windows /
//     OutputAuthResourcePathBindKey: the coded_group.go/pivot.go twins with
//     the same citations (compiler/row_lineage.go:655, cell_trace.go:54,
//     population_mapping.go:38); windowing contracts register with the
//     pg-sql-22/23 terminals, the auth path column with pg-ir-03.
//   - L-16.5 source-region EXPRESSION_LET: the probe-attested
//     production-unreachable twin of pivot.go C6/P-14.3; the arm stays
//     fail-closed (no dispatch ⇒ silent drop is the L-19.4 class).
//   - L-16.6 foreign operations / non-canonical DERIVED_LET / unknown
//     policy, kind, reducer, or operator strings / unregistered stage
//     expression kinds: the slice twins, fail-closed, never partially
//     rendered.
//
// Registered divergences (verbatim test-comment form per slice; see
// stage_sequence_test.go U7 for the law cells):
//
//   - D-16a terminal re-sort over a GROUP-last stage: REGISTERED
//     REFINEMENT, attribution line quoted at the top of this header
//     (coordinator ruling 2026-11-11).
//   - D-16b EXPAND ordinal over duplicate equal items: jsonb_array_elements
//     WITH ORDINALITY is positional exactly like AQL RANGE indices —
//     declared EQUIVALENT (witness column pinned), no divergence.
//   - D-16c melt+re-melt (UNPIVOT then GROUP): the W3 stable melt order is
//     already registered (pg-sql-15); composition inherits it — cited, not
//     new.
//   - D-16d EXPAND empty-policy raise: the AQL CONCAT diagnostic suffix
//     ("construction <cid> has no items for row <identity>") embeds row
//     data in the raise message; the guardCast barrier raises SQLSTATE
//     22P02 with the canonical CONSTRUCTION_EXPANSION_EMPTY tag only — the
//     same static-tag posture as the pivot guard chain.
//   - D-16e+ oracle-attested cells (tail semantics, comparison class) carry
//     their receipts in the test comments with the o1/o2 wire names.
//   - D-16f composition correlation version clause (plan §5 G5(b) closure
//     adjudication): the AQL version clause
//     (render/aql/correlation_value_scope.go:28-38,
//     NOT_NULL(x,"")==@bind) has no composition surface to arm — the only
//     correlation-carrying composition read is a comparison predicate, and
//     PostgreSQL has no registered correlated-comparison rendering at all
//     (predicates.go:276 typed refusal, spine-wide, cited; the graph slice
//     refuses correlated extend filters the same way, graph_test.go:511).
//     Census re-attested at this head: LowerCorrelatedBinding never fills
//     version for construction (P-14.5), so the vacuous surface stays
//     fail-closed with battery pins (correlated composition filter refuses
//     typed; VersionSelector-without-VersionBindKey refuses at the ir
//     gate). The version-equality that AQL does consult lands through the
//     G2 semantic matcher, not here. No silent path.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// Registered canonical assertion tags raised verbatim through guardCast by
// the composition stage emitters (render/aql/construction_expand.go:36,40,57
// parity; the empty-policy message is the static-tag twin, D-16d).
const (
	stageExpandArrayTypeTag = "CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH"
	stageExpandItemTypeTag  = "CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH"
	stageExpandEmptyTag     = "CONSTRUCTION_EXPANSION_EMPTY"
)

// stageSequenceCTEPrefix mints every relation name of the composed
// statement: __loom_src, __loom_stage_N, and the suffixed internals of the
// repointed slice cores. AQL spells the same chain
// __loom_construction_source_projection / __loom_construction_stage_N
// (render/aql/stage_sequence.go:284-302); the SQL names are renderer-owned
// constants, never plan data.
const stageSequenceCTEPrefix = "__loom_"

// stageSequenceCompositionMentions reports whether RenderPhysicalPlan's
// composition door claims a plan: a stage sequence with two or more stages
// is the composed shape and is rendered WHOLE here, never by the four
// inline slice doors (coordinator A2 ruling 2026-11-11). Single-stage plans
// still route to their inline doors untouched; the ones no door claims
// reach this door through the converted blanket refusal.
func stageSequenceCompositionMentions(plan ir.PhysicalPlan) bool {
	return plan.StageSequence != nil && len(plan.StageSequence.Stages) >= 2
}

// renderStageSequencePhysicalPlan is the single composition door behind
// both entry points (the RenderPhysicalPlan hook and the direct door
// tests). Gate order mirrors the other slice doors: storage validation,
// then validateStageSequencePlanSurface as the first plan-facing act so
// both doors refuse the same shapes with the same typed errors BEFORE
// plan.Validate can speak, then the plan-wide gates, then the builder.
func renderStageSequencePhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateStageSequencePlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}
	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql, err := renderer.renderStageSequenceStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// validateStageSequencePlanSurface is the total closed-slice contract
// behind both doors. It speaks typed ErrUnsupportedPostgresPlan refusals
// over ANY plan the door claims before plan validation, scope loading, or
// any rendering can reject with an off-type error, and it reproduces the
// registered voices of shapes whose ownership stays in another slice
// (#132 register-split voice law).
func validateStageSequencePlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: composition dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	// C-01 (zero stages), L-16.1 first cell: ir/physical_stage_validation.go:15-16
	// requires at least one stage; the door speaks first.
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("%w: stage %d ID is empty, untrimmed, or duplicated — the composition door requires at least one construction stage (ir/physical_stage_validation.go:15 re-read at the door)",
			ErrUnsupportedPostgresPlan, 0)
	}
	// L-16.4 first: diagnostic returns and windowing refuse with the
	// registered citations regardless of stage kinds (acceptance battery
	// classes C-16/C-17; plan-level and sequence-level flags).
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		// The plan-level flag must be refused HERE: the composition door
		// dispatches before the spine's plan-level window gate, so this is
		// the only place the check can fire for the shapes it claims — the
		// twin doors keep the same check (coded_group.go:458, pivot.go:1331).
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window are pg-sql-22/23 terminal windowing contracts and are refused, never silently rendered",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for composed sequences (ir/physical_stage_sequence.go:25-28; the auth path column registers with pg-ir-03)",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: composition preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	// L-16.2 / L-16.3 voices: coded-slice shapes the composition door
	// claims refuse in their slice's registered voice (coordinator A3
	// ruling; #132 voice law). The message texts are the coded_group.go:436
	// and pivot.go:1304 texts verbatim so dispatch-level and door-level
	// refusals stay byte-equal.
	for index, stage := range sequence.Stages {
		switch {
		case stage.Kind == ir.PhysicalStageCodedGroupOp || stage.CodedGroup != nil:
			return fmt.Errorf("%w: the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap",
				ErrUnsupportedPostgresPlan)
		case stage.Kind == ir.PhysicalStageCohortGroupOp:
			return fmt.Errorf("%w: the COHORT_GROUP stage (index %d) is not part of the registered composition surface: the explicit-cohort boundary has no PostgreSQL rendering and its lineage surfaces refuse at their own doors (compiler/row_lineage.go:655)",
				ErrUnsupportedPostgresPlan, index)
		case stage.GroupedPivot != nil && stage.GroupedPivot.CodedCorrelation != nil:
			if index == 0 {
				return fmt.Errorf("%w: multi-stage construction sequences are the pg-sql-16 capability; the registered slice admits only the inline single-stage CODED_PIVOT",
					ErrUnsupportedPostgresPlan)
			}
			return fmt.Errorf("%w: a coded Pivot at stage index %d violates the ir rule that the coded Pivot must be the first stage over the direct source projection (ir/physical_stage_validation.go:106-108); coded-pivot composition beyond stage 1 stays the pg-sql-22/23 adjacency",
				ErrUnsupportedPostgresPlan, index)
		}
	}
	if err := validateStageSequenceOperations(plan); err != nil {
		return err
	}
	return validateStageSequenceChain(plan)
}

// validateStageSequenceOperations enforces the closed source-region
// operation list: the group-slice whitelist (validateGroupOperations twin)
// PLUS UNNEST (unnest.go arms) and SET (sets.go child sets) — the L-13.2
// closure. SET now HAS a dispatch arm (the source CTE renders it through
// sets.go), and removing that arm is the silent-drop class the L-19.4
// sabotage lesson forbids (S5). EXPRESSION_LET stays the probe-attested
// unreachable fail-closed arm (L-16.5, the pivot.go C6 twin): the source
// builder has no dispatch for it.
func validateStageSequenceOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: the composition source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the composition door refuses them",
			ErrUnsupportedPostgresPlan)
	}
	sawSort, sawLimit, sawReturn := false, false, false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalUnnestOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source expansions must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: unnest payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			// The registered unnest contract is the single definition
			// (unnest.go); the full-backstop re-check runs inside
			// renderUnnest at the arm.
		case ir.PhysicalSetOp:
			// L-13.2 CLOSED (pg-sql-16): the source CTE renders lowering-
			// emitted child sets through the validated sets.go correlated-
			// set machinery. The arm is load-bearing: with no dispatch the
			// SET would be silently dropped (the L-19.4 law, sets.go:82-95
			// finding cited).
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source child sets must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Set == nil {
				return fmt.Errorf("%w: operation %d: set payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalExpressionLetOp:
			// L-16.5: probe-attested production-unreachable (pivot.go C6 /
			// probe P-14.3 twin); fail-closed, never partially rendered.
			return fmt.Errorf("%w: source-region EXPRESSION_LET has no dispatch arm in the composition builder and is refused fail-closed (L-16.5, probe P-14.3 twin)",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalSortOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: limit payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			if !last || operation.Return == nil || len(operation.Return.Projections) == 0 {
				return fmt.Errorf("%w: the composition source requires a terminal RETURN with its projection list",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
		default:
			return fmt.Errorf("%w: operation %q is not part of the registered composition source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return fmt.Errorf("%w: the composition source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return nil
}

// validateStageSequenceChain re-reads the ir stage-chain contract at the
// door (physical_stage_validation.go:11-73,275-290) so every invalid
// reference is refused with a typed error before plan.Validate can speak
// (acceptance clause 2, L-16.1): source/final IDs and identity columns
// required; SourceColumns ⊆ the typed source RETURN; per-stage ID
// non-empty/trimmed/unique; stage[i].InputStageID == prior stage ID (the
// FIRST stage == SourceStageID — a forward or skip reference reads no CTE
// and has no rendering); InputColumns == prior OutputColumns (except the
// group-extend rule); per-stage OutputColumns schema vs RowIdentityColumn;
// FinalStageID/FinalRowIdentity match the last stage; FinalColumns == the
// last OutputColumns; per-kind payload exclusivity.
func validateStageSequenceChain(plan ir.PhysicalPlan) error {
	sequence := plan.StageSequence
	if sequence.SourceStageID == "" || sequence.SourceRowIdentity == "" || sequence.FinalStageID == "" || sequence.FinalRowIdentity == "" {
		return fmt.Errorf("%w: source/final stage IDs and row identity columns are required (ir/physical_stage_validation.go:12 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	projectionNames := map[string]bool{}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp || operation.Kind == ir.PhysicalUnpivotOp {
			return fmt.Errorf("%w: source projection cannot contain a terminal table reshape (ir/physical_stage_validation.go:33 re-read at the door)",
				ErrUnsupportedPostgresPlan)
		}
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				projectionNames[projection.Name] = true
			}
		}
	}
	if err := validateStageColumns(sequence.SourceColumns, sequence.SourceRowIdentity); err != nil {
		return fmt.Errorf("%w: composition source projection schema: %v", ErrUnsupportedPostgresPlan, err)
	}
	for _, column := range sequence.SourceColumns {
		if !projectionNames[column.Name] {
			return fmt.Errorf("%w: source projection column %q is missing from the typed source RETURN (ir/physical_stage_validation.go:46 re-read at the door)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
	}
	priorStageID := sequence.SourceStageID
	priorColumns := sequence.SourceColumns
	priorRowIdentity := sequence.SourceRowIdentity
	seenIDs := map[string]bool{sequence.SourceStageID: true}
	for index, stage := range sequence.Stages {
		path := "stage[" + strconv.Itoa(index) + "]"
		if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) || seenIDs[stage.ID] {
			return fmt.Errorf("%w: %s ID is empty, untrimmed, or duplicated (L-16.1)", ErrUnsupportedPostgresPlan, path)
		}
		seenIDs[stage.ID] = true
		if stage.InputStageID != priorStageID {
			return fmt.Errorf("%w: %s input stage %q does not reference prior stage %q — the CTE chain reads exactly the prior relation (L-16.1)",
				ErrUnsupportedPostgresPlan, path, stage.InputStageID, priorStageID)
		}
		if err := validateStageColumns(stage.InputColumns, priorRowIdentity); err != nil {
			return fmt.Errorf("%w: %s input schema: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		if !sameStageColumns(stage.InputColumns, priorColumns) && !groupInputColumnsExtendSource(stage, priorColumns) {
			return fmt.Errorf("%w: %s input schema differs from preceding stage output (ir/physical_stage_validation.go:66 re-read at the door)",
				ErrUnsupportedPostgresPlan, path)
		}
		if err := validateStageColumns(stage.OutputColumns, stage.RowIdentityColumn); err != nil {
			return fmt.Errorf("%w: %s output schema: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		if err := validateStageKindPayload(stage); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrUnsupportedPostgresPlan, path, err)
		}
		priorStageID = stage.ID
		priorColumns = stage.OutputColumns
		priorRowIdentity = stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID {
		return fmt.Errorf("%w: final stage ID %q does not reference the last stage %q (L-16.1)",
			ErrUnsupportedPostgresPlan, sequence.FinalStageID, last.ID)
	}
	if sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("%w: final row identity %q drifts from the last stage identity %q (L-16.1)",
			ErrUnsupportedPostgresPlan, sequence.FinalRowIdentity, last.RowIdentityColumn)
	}
	if !sameStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("%w: final columns differ from the last stage output schema (ir/physical_stage_validation.go:286 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	return nil
}

// validateStageColumns mirrors ir.validatePhysicalStageColumns at the door:
// names non-empty and unique, exactly one identity column when one is
// required.
func validateStageColumns(columns []ir.PhysicalStageColumn, identityColumn string) error {
	if len(columns) == 0 {
		return fmt.Errorf("a stage schema must declare at least one column")
	}
	seen := map[string]bool{}
	identities := 0
	for _, column := range columns {
		if strings.TrimSpace(column.Name) == "" {
			return fmt.Errorf("column ID %q has an empty name", column.ID)
		}
		if seen[column.Name] {
			return fmt.Errorf("column name %q is declared twice", column.Name)
		}
		seen[column.Name] = true
		if column.Identity {
			identities++
		}
	}
	if identityColumn != "" {
		if !seen[identityColumn] {
			return fmt.Errorf("row identity column %q is not declared in the stage schema", identityColumn)
		}
	}
	if identities > 1 {
		return fmt.Errorf("the stage schema declares %d identity columns", identities)
	}
	return nil
}

// sameStageColumns compares two declared schemas by the ir rule
// (reflect.DeepEqual parity at physical_stage_validation.go:65): same
// columns in same order, same declared types and roles.
func sameStageColumns(left, right []ir.PhysicalStageColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		a, b := left[i], right[i]
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind ||
			a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal ||
			a.Identity != b.Identity || a.RootContributorResourceType != b.RootContributorResourceType ||
			a.PresenceCompanionName != b.PresenceCompanionName {
			return false
		}
	}
	return true
}

// groupInputColumnsExtendSource is the ir :65 group-extend rule re-read:
// a GROUP stage may carry exactly the prior schema plus the compiler-
// stamped group inputs (the lowering keeps InputColumns a superset form
// for the group's hidden reads); membership is by name against the prior
// schema, matching ir.physicalGroupInputColumnsExtendSource.
func groupInputColumnsExtendSource(stage ir.PhysicalConstructionStage, prior []ir.PhysicalStageColumn) bool {
	if stage.Kind != ir.PhysicalStageGroupOp {
		return false
	}
	priorNames := map[string]bool{}
	for _, column := range prior {
		priorNames[column.Name] = true
	}
	for _, column := range stage.InputColumns {
		if !priorNames[column.Name] {
			return false
		}
	}
	return len(stage.InputColumns) >= len(prior)
}

// validateStageKindPayload re-reads the per-kind payload exclusivity table
// (ir/physical_stage_validation.go:80-190 parity for the registered kinds)
// so no kind/payload mismatch reaches the builder (battery class C-18).
func validateStageKindPayload(stage ir.PhysicalConstructionStage) error {
	switch stage.Kind {
	case ir.PhysicalStageDeriveOp:
		if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil {
			return fmt.Errorf("DERIVE has mismatched operation payloads (ir/physical_stage_validation.go:82 re-read at the door)")
		}
		if len(stage.DerivedLets) == 0 {
			return fmt.Errorf("DERIVE requires at least one typed expression LET (ir/physical_stage_validation.go:86 re-read at the door)")
		}
		for index, operation := range stage.DerivedLets {
			if operation.Kind != ir.PhysicalExpressionLetOp || operation.ExpressionLet == nil {
				return fmt.Errorf("DERIVE derived operation %d has kind %q (only typed expression LETs are registered)",
					index, string(operation.Kind))
			}
		}
	case ir.PhysicalStageFilterOp:
		if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil ||
			len(stage.DerivedLets) != 0 {
			return fmt.Errorf("FILTER requires only a filter payload (ir/physical_stage_validation.go:92 re-read at the door)")
		}
	case ir.PhysicalStageExpandOp:
		if stage.Expand == nil || stage.Filter != nil || stage.Group != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil ||
			len(stage.DerivedLets) != 0 {
			return fmt.Errorf("EXPAND requires only an expand payload (ir/physical_stage_validation.go:82 shape re-read at the door)")
		}
	case ir.PhysicalStageUnpivotOp:
		if stage.Unpivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil ||
			stage.GroupedPivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("UNPIVOT requires only an unpivot payload (ir/physical_stage_validation.go:141 re-read at the door)")
		}
		if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
			return fmt.Errorf("output row variable does not match unpivot (ir/physical_stage_validation.go:145 re-read at the door)")
		}
	case ir.PhysicalStageGroupOp:
		if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("GROUP requires only a group payload (ir/physical_stage_validation.go:155 re-read at the door)")
		}
		// Fail-closed arm gate: a composed GROUP must carry every
		// registered-arm refusal the inline door speaks (group.go
		// validateGroupPayload twins). Without this re-read a composed
		// stage would silently drop the arm's rows-to-be (the L-13.2
		// "no dispatch arm ⇒ silent drop" law).
		if stage.Group.SemanticKey != nil {
			return fmt.Errorf("the semantic family Group key is a registered named gap (L-13.3; enabling contract: the semantic membership walk and matcher) and is refused, never partially rendered")
		}
		if stage.Group.RelatedOccurrence != nil {
			return fmt.Errorf("the related-occurrence Group adapter is a pg-sql-16 capability gap (L-13.5; correlated-hop child-set contracts) and is refused, never partially rendered")
		}
		if stage.Group.CodingKey != nil {
			return fmt.Errorf("CodingKey group keys are refused at the composition payload gate until their registered arm lands (group.go:444 twin, wave law)")
		}
		if stage.Group.RootContributorInputColumn != "" {
			return fmt.Errorf("the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered")
		}
	case ir.PhysicalStagePivotOp:
		if stage.GroupedPivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil ||
			stage.Unpivot != nil || stage.RelatedField != nil || stage.RelatedSource != nil ||
			stage.RelatedExpand != nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("PIVOT requires only a grouped-pivot payload (ir/physical_stage_validation.go:99 re-read at the door)")
		}
		if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
			return fmt.Errorf("output row variable does not match grouped pivot (ir/physical_stage_validation.go:102 re-read at the door)")
		}
	case ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedExpandOp,
		ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		// C-11: the RELATED_* stages belong to the related-route slices
		// (their own typed refusals upstream); the composition surface
		// refuses them fail-closed with the enabling capability.
		return fmt.Errorf("the %s stage is a related-route capability and is refused in composition, never partially rendered (the correlated-hop child-set contracts; sets.go:82-95 hop finding cited)",
			string(stage.Kind))
	default:
		return fmt.Errorf("stage kind %q has no registered composition rendering", string(stage.Kind))
	}
	return nil
}

// ---------------------------------------------------------------------------
// The composed statement
// ---------------------------------------------------------------------------

// Registered relation and internal-relation names. Every one is a
// renderer-owned constant; no plan string ever reaches SQL text as an
// identifier (the closed-surface law every slice shares).
const (
	stageSourceRelation = stageSequenceCTEPrefix + "src"
	stageRelationPrefix = stageSequenceCTEPrefix + "stage_"
	// stageTerminalAlias is the single relation alias of the terminal SELECT.
	stageTerminalAlias = "s"
)

// stageColumnFact is one materialized column of an intermediate relation:
// its positional alias inside its own CTE and the storage class the emitter
// minted it with. The typed-intermediate-row law: a stage column is either
// a SQL scalar (text/numeric/boolean) or jsonb — never re-spelled SQL,
// never an AQL document — and the class is decided by the EMITTER that
// produced the column, because only that emitter knows whether the
// expression it rendered is a scalar cast or a jsonb read. Downstream reads
// consult the fact, so a stage never guesses another stage's types.
type stageColumnFact struct {
	alias string
	jsonb bool
}

// stageColumnExpr is one pre-rendered output expression of a stage, keyed by
// output column name: reshape stages (EXPAND/UNPIVOT/GROUP/PIVOT) mint the
// expressions their own payload owns (item, ordinal, melt slots, keys,
// cells, identities) and hand them to the shared positional emitter.
type stageColumnExpr struct {
	expr  string
	jsonb bool
}

// stageRowScope binds one composed stage row variable to the prior
// relation's materialized columns. qualification prefixes every positional
// read (the stages read FROM one aliased relation, so "s." keeps the melt
// and expand laterals unambiguous); the GROUP region passes "" because its
// consuming CTEs hold exactly one relation each (the unqualified-ref law
// group.go:556-563 states).
type stageRowScope struct {
	qualification string
	columns       map[string]stageColumnFact
}

// resolveStageRowValue maps one stage row read onto its materialized
// column. A stage row is not a stored document, so the closed read form is
// a column name plus an optional jsonb tail path: bind values, whole-row
// reads, and tail paths over SQL scalars are typed refusals (the
// typed-column law; the whole-row form would be a composite).
func (r *planRenderer) resolveStageRowValue(scope stageRowScope, value ir.PhysicalValue) (string, error) {
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: a stage row read must name an intermediate column, not a bind value",
			ErrUnsupportedPostgresPlan)
	}
	if len(value.Path) == 0 {
		return "", fmt.Errorf("%w: a stage row read must name at least one intermediate column; the whole intermediate row is a composite, not a registered scalar",
			ErrUnsupportedPostgresPlan)
	}
	fact, ok := scope.columns[value.Path[0]]
	if !ok {
		return "", fmt.Errorf("%w: stage column %q is not among the registered intermediate columns of the prior stage (L-16.1)",
			ErrUnsupportedPostgresPlan, value.Path[0])
	}
	base := scope.qualification + `"` + fact.alias + `"`
	if len(value.Path) == 1 {
		if !fact.jsonb {
			return base, nil
		}
		return r.stageJSONBRead(base), nil
	}
	if !fact.jsonb {
		return "", fmt.Errorf("%w: stage column %q is a SQL scalar and carries no document path %v",
			ErrUnsupportedPostgresPlan, value.Path[0], value.Path[1:])
	}
	return r.documentPathFromExpression(base, value.Path[1:])
}

// stageJSONBRead renders one jsonb intermediate column as a payload read.
// The "#> $" marker is the package's single payload-detection contract
// (predicates.go:354), so a jsonb stage column reaches the registered
// three-valued comparison arms instead of the typed-column arms; the empty
// text[] path reads the value itself, which is exactly AQL's row[@col]
// reading of a stored JSON value, and an SQL NULL column stays SQL NULL
// (the ABSENT reading).
func (r *planRenderer) stageJSONBRead(base string) string {
	return base + " #> $" + strconv.Itoa(r.textParameter("{}", "path:{}")) + "::text[]"
}

// guardCastJSONB is the guardCast barrier for a jsonb-valued CASE branch:
// the inner integer cast raises SQLSTATE 22P02 naming the tag the moment
// the branch is selected, while the outer text→jsonb casts keep the CASE
// type-resolvable at parse time (an integer→jsonb cast has no registration
// and would abort the whole statement at analysis instead of at the guard).
func guardCastJSONB(tag string) string {
	return "CAST(" + guardCast(tag) + "::text AS jsonb)"
}

// renderStageSequenceStatement renders the whole composed plan: the source
// CTE, one CTE per construction stage over the immediately prior relation,
// and the terminal positional projection with the registered order and
// preview limit. AQL ground truth is render/aql/stage_sequence.go:264-533
// ported branch-for-branch (the LET chain becomes the WITH chain, stage N+1
// reads only stage N).
func (r *planRenderer) renderStageSequenceStatement(plan ir.PhysicalPlan) (string, error) {
	sequence := plan.StageSequence
	source, sourceFacts, err := r.renderStageSequenceSource(plan)
	if err != nil {
		return "", err
	}
	ctes := []string{stageSourceRelation + " AS (" + source + ")"}
	relation := stageSourceRelation
	facts := sourceFacts
	var last *ir.PhysicalConstructionStage
	for index := range sequence.Stages {
		stage := sequence.Stages[index]
		body, stageCTEs, stageFacts, err := r.renderStageCTE(plan, stage, index, relation, facts)
		if err != nil {
			return "", err
		}
		ctes = append(ctes, stageCTEs...)
		relation = stageRelationPrefix + strconv.Itoa(index)
		ctes = append(ctes, relation+" AS ("+body+")")
		facts = stageFacts
		last = &sequence.Stages[index]
	}
	if last == nil {
		return "", fmt.Errorf("%w: the composition builder requires at least one stage", ErrUnsupportedPostgresPlan)
	}

	// --- terminal: the AQL stage_sequence.go:435-528 twin. ---
	selects := make([]string, 0, len(sequence.FinalColumns))
	names := make([]string, 0, len(sequence.FinalColumns))
	for index, column := range sequence.FinalColumns {
		fact, ok := facts[column.Name]
		if !ok {
			return "", fmt.Errorf("%w: final column %q is not a column of the last stage (ir/physical_stage_validation.go:286 re-read at the builder)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		selects = append(selects, stageTerminalAlias+`.`+`"`+fact.alias+`"`+` AS "c`+strconv.Itoa(index)+`"`)
		names = append(names, column.Name)
	}
	final := "SELECT " + strings.Join(selects, ", ") + " FROM " + relation + " AS " + stageTerminalAlias
	// D-16a attribution (coordinator ruling 2026-11-11): CTE terminal re-sort over GROUP-last per coordinator ruling 2026-11-11; SQL CTE order unspecified; refines registered group total order.
	// PostgreSQL makes no promise that a CTE's rows arrive in its own
	// ORDER BY order, so the terminal re-applies exactly the group slice's
	// registered total order (keys ASC NULLS FIRST with the STRING keys
	// under COLLATE "C", the D-13c citations kept verbatim) over the
	// materialized key columns, and every other last stage sorts the
	// identity under COLLATE "C" — the render/aql/stage_sequence.go:450
	// twin.
	final += " ORDER BY " + strings.Join(stageTerminalOrder(*last, facts), ", ")
	if sequence.PreviewLimitBindKey != "" {
		limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int)
		if !ok || limit <= 0 {
			return "", fmt.Errorf("%w: composition preview limit bind %q must be a positive int (render/aql/stage_sequence.go:452-453 twin)",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}
	r.outputColumns = names
	return "WITH " + strings.Join(ctes, ", ") + " " + final, nil
}

// stageTerminalOrder renders the registered terminal ORDER BY terms over the
// last stage's materialized columns.
func stageTerminalOrder(stage ir.PhysicalConstructionStage, facts map[string]stageColumnFact) []string {
	read := func(name string) string {
		return stageTerminalAlias + "." + `"` + facts[name].alias + `"`
	}
	if stage.Kind == ir.PhysicalStageGroupOp && stage.Group != nil {
		terms := make([]string, 0, len(stage.Group.Keys)+1)
		for _, key := range stage.Group.Keys {
			collate := ""
			if key.Kind == "STRING" {
				collate = ` COLLATE "C"`
			}
			terms = append(terms, read(key.OutputColumn)+collate+" ASC NULLS FIRST")
		}
		terms = append(terms, read(stage.RowIdentityColumn)+` COLLATE "C" ASC`)
		return terms
	}
	return []string{read(stage.RowIdentityColumn) + ` COLLATE "C" ASC`}
}

// renderStageSequenceSource renders the source CTE: the group slice's spine
// arms (one ROOT_SCAN, canonical scope filters, the canonical auth LET, the
// renderable spine filters, at most one SORT then one LIMIT, the terminal
// RETURN) plus the UNNEST and SET arms that close L-13.2, then the
// positional materialization of sequence.SourceColumns.
func (r *planRenderer) renderStageSequenceSource(plan ir.PhysicalPlan) (string, map[string]stageColumnFact, error) {
	if err := r.renderRootScan(plan, *plan.Operations[0].RootScan); err != nil {
		return "", nil, err
	}
	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return "", nil, err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", nil, err
			}
			if !canonical {
				return "", nil, fmt.Errorf("%w: a non-canonical DERIVED_LET has no dispatch arm in the composition source region (L-16.6)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalUnnestOp:
			// The registered expansion (unnest.go owns the whole contract:
			// iterate forms, falsy arms, zero-item-kept policy, witnesses).
			if err := r.renderUnnest(plan, *operation.Unnest); err != nil {
				return "", nil, err
			}
		case ir.PhysicalSetOp:
			// L-13.2 CLOSED (pg-sql-16): the source region renders
			// lowering-emitted child sets through the validated sets.go
			// correlated-set machinery. Removing this arm would silently
			// drop the SET (the L-19.4 law; sets.go:82-95 hop finding).
			if err := r.renderSet(plan, *operation.Set); err != nil {
				return "", nil, err
			}
		case ir.PhysicalSortOp:
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", nil, err
			}
		case ir.PhysicalLimitOp:
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", nil, err
			}
		case ir.PhysicalReturnOp:
			returnOp = operation.Return
		}
	}
	// Only the DECLARED source columns are materialized: the typed source
	// RETURN may carry hidden executor-side metadata (the spine's own
	// reserved columns) that no stage addresses, and materializing a column
	// nothing reads would both bloat the CTE and make the registered
	// materializer refuse a shape the composition never addresses.
	addressed := map[string]bool{}
	for _, column := range plan.StageSequence.SourceColumns {
		addressed[column.Name] = true
	}
	// Lowering-emitted presence companion columns (the hidden boolean
	// sidecars of lower/recipe_construction.go:1075, declared in
	// SourceColumns as the PresenceCompanionName of their value column) are
	// stage presence evidence, deliberately refused by the registered group
	// materializer's sidecar arm. The composition renders them directly
	// over the source row document with the null_semantics.go presence
	// renderer, as a SQL boolean scalar.
	companionExprs := map[string]string{}
	// The registered MANY-cardinality array-selector arm: a declared many
	// source column reads its ValueModeAll EXTRACT through the PG twin of
	// the AQL array selector (render/aql/selector.go extractFinalExpr
	// ((x.f ? x.f : [])) + render/aql/selectors.go FLATTEN for ALL): the
	// items of the iterated array in order, each read through the trailing
	// property path, MISSING values dropped by the []-operator reading (a
	// jsonb path read yields SQL NULL for a missing property or a non-object
	// item; a stored JSON null is the jsonb 'null' scalar and stays), and a
	// missing/non-array parent collapses to the empty array exactly as the
	// AQL falsy guard + FLATTEN does.
	declaredCardinality := map[string]string{}
	for _, column := range plan.StageSequence.SourceColumns {
		declaredCardinality[column.Name] = strings.ToUpper(strings.TrimSpace(column.Cardinality))
	}
	arrayExprs := map[string]string{}
	// The canonical-key selector arm: a stored FHIR document never carries
	// an "id"/"_key" property (traversal.go:490-501, graph.go:94-104), so
	// a root single-step id/_key selector reads the logical_id sidecar,
	// the exact registered twin of resolveValueInRows' canonicalization
	// and of coded_group.go:873's cg_root_key. A payload-path read of the
	// absent property would silently render SQL NULL.
	keyExprs := map[string]string{}
	setExprs := map[string]string{}
	filtered := ir.PhysicalReturn{}
	for _, projection := range returnOp.Projections {
		if !addressed[projection.Name] {
			continue
		}
		if projection.Hidden && projection.PresenceOutput && projection.Presence != nil &&
			len(projection.Presence.Paths) == 1 && len(projection.Presence.Paths[0]) > 0 {
			scope, ok := r.rows[projection.Presence.Source.Variable]
			if !ok || scope.alias != r.rootAlias {
				return "", nil, fmt.Errorf("%w: presence companion %q reads a variable the composition source region has not registered (L-16.6)",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			presence, err := r.propertyPresenceExpression(r.rootAlias, projection.Presence.Paths[0])
			if err != nil {
				return "", nil, err
			}
			companionExprs[projection.Name] = "(" + presence + ")"
			continue
		}
		// The SET slice-column arm: a representative-slice SLICE over a
		// correlated child set renders through the registered child-set
		// slice contract (objects.go renderSliceValue → the setBodies arm,
		// renderSliceOverChildSet) exactly as the spine's RETURN does. The
		// group materializer's composite refusal is never reached; a slice
		// over anything else keeps its registered refusals (root-row slice
		// is the spine's, child sets the composition never rendered stay
		// the materializer's typed composite refusal).
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalSliceExpression &&
			projection.Expression.Slice != nil {
			if _, registered := r.setBodies[projection.Expression.Slice.Source.Variable]; registered {
				expression, err := r.renderSliceValue(plan, r.rows, *projection.Expression, 0)
				if err != nil {
					return "", nil, fmt.Errorf("slice column %q: %w", projection.Name, err)
				}
				setExprs[projection.Name] = expression
				continue
			}
		}
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression &&
			projection.Expression.Cardinality != ir.PhysicalArrayCardinality {
			if extract := projection.Expression.Extract; extract != nil &&
				len(extract.Selector.Steps) == 1 && !extract.Selector.Steps[0].Iterate &&
				extract.Selector.Steps[0].Index == nil && extract.Selector.Filter == nil &&
				len(extract.Fallbacks) == 0 && !extract.Distinct &&
				extract.Prepared == nil && extract.UnitNormalization == nil &&
				len(extract.Source.Path) == 1 && extract.Source.Path[0] == "payload" {
				// Only the three registered scalar postures (scalarExtract
				// ValueExpression, null_semantics.go:279-296) intercept; an
				// unregistered posture stays the classifier's typed refusal,
				// the key arm never swallows it.
				posture := projection.Expression.NullBehavior
				if column := canonicalKeyColumn(extract.Selector.Steps[0].Field); resourceFilterColumns[column] &&
					(posture == ir.PhysicalPreserveNull || posture == ir.PhysicalOmitNulls ||
						posture == ir.PhysicalEmptyOnNull) {
					scope, ok := r.rows[extract.Source.Variable]
					if !ok || scope.alias != r.rootAlias {
						return "", nil, fmt.Errorf("%w: key selector %q reads a variable the composition source region has not registered (L-16.6)",
							ErrUnsupportedPostgresPlan, projection.Name)
					}
					if extract.ResourceType != scope.resourceType {
						return "", nil, fmt.Errorf("%w: key selector %q validates against resource type %q, but the row is scanned as %q",
							ErrUnsupportedPostgresPlan, projection.Name, extract.ResourceType, scope.resourceType)
					}
					// All three registered scalar postures collapse onto the
					// sidecar for an always-present identity: the column is
					// never missing, never JSON null.
					keyExprs[projection.Name] = r.rootAlias + "." +
						r.storage.Resource.MustColumn(column).SQLText()
					continue
				}
			}
		}
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression &&
			projection.Expression.Cardinality == ir.PhysicalArrayCardinality {
			if declaredCardinality[projection.Name] != "MANY" {
				return "", nil, fmt.Errorf("%w: source projection %q is an array EXTRACT but its declared column cardinality is %q (the many storage class is the only registered array reading)",
					ErrUnsupportedPostgresPlan, projection.Name, declaredCardinality[projection.Name])
			}
			expression, err := r.stageArraySelectorExpression(projection)
			if err != nil {
				return "", nil, err
			}
			arrayExprs[projection.Name] = expression
			continue
		}
		filtered.Projections = append(filtered.Projections, projection)
	}
	columns, err := r.materializeGroupSource(plan, &filtered)
	if err != nil {
		return "", nil, err
	}
	byName := map[string]groupColumn{}
	for i := range columns {
		byName[columns[i].name] = columns[i]
	}
	selects := make([]string, 0, len(plan.StageSequence.SourceColumns))
	facts := map[string]stageColumnFact{}
	for index, column := range plan.StageSequence.SourceColumns {
		alias := "c" + strconv.Itoa(index)
		if presence, ok := companionExprs[column.Name]; ok {
			// A declared presence companion column must carry the lowered
			// boolean sidecar shape: boolean kind, never an identity or
			// root-contributor column, and the emitter here is the
			// registered scalar (SQL boolean) presence renderer.
			if !strings.EqualFold(strings.TrimSpace(column.Kind), "boolean") || column.Identity {
				return "", nil, fmt.Errorf("%w: source column %q is rendered as a presence companion but is declared kind %q identity=%v",
					ErrUnsupportedPostgresPlan, column.Name, column.Kind, column.Identity)
			}
			selects = append(selects, presence+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: false}
			continue
		}
		if composite, ok := setExprs[column.Name]; ok {
			if err := validateStageColumnClass(column, true); err != nil {
				return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
			}
			selects = append(selects, composite+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: true}
			continue
		}
		if key, ok := keyExprs[column.Name]; ok {
			if err := validateStageColumnClass(column, false); err != nil {
				return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
			}
			selects = append(selects, key+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: false}
			continue
		}
		if array, ok := arrayExprs[column.Name]; ok {
			if !strings.EqualFold(strings.TrimSpace(column.Cardinality), "many") {
				return "", nil, fmt.Errorf("%w: source column %q rendered an array reading under cardinality %q",
					ErrUnsupportedPostgresPlan, column.Name, column.Cardinality)
			}
			selects = append(selects, array+` AS "`+alias+`"`)
			facts[column.Name] = stageColumnFact{alias: alias, jsonb: true}
			continue
		}
		source, ok := byName[column.Name]
		if !ok {
			return "", nil, fmt.Errorf("%w: source projection column %q is not among the typed source RETURN projections (ir/physical_stage_validation.go:46 re-read at the builder)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		selects = append(selects, source.expr+` AS "`+alias+`"`)
		// The storage class of an intermediate column is decided from the
		// rendered expression through the single registered payload-
		// detection contract (predicates.go isPayloadExpression): the
		// materializer's path-based jsonb flag marks the root-identity
		// _key scalar too, and a fact must describe what the SQL actually
		// stores, or later reads would wrap a bare scalar in jsonb casts.
		class := isPayloadExpression(source.expr)
		if err := validateStageColumnClass(column, class); err != nil {
			return "", nil, fmt.Errorf("%w: source column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
		}
		facts[column.Name] = stageColumnFact{alias: alias, jsonb: class}
	}
	resource := r.storage.Resource
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		sql += " " + join
	}
	for _, lateral := range r.laterals {
		sql += " " + lateral
	}
	if len(r.where) > 0 {
		sql += " WHERE " + strings.Join(r.where, " AND ")
	}
	sql += " ORDER BY " + strings.Join(r.windowOrder(), ", ") + r.window.limitClause
	return sql, facts, nil
}

// stageArraySelectorExpression renders the registered MANY-cardinality
// ValueModeAll EXTRACT of the composition source region: the PG twin of
// the AQL array selector — the falsy-guarded iterated array
// ((x.f ? x.f : []), render/aql/selector.go extractFinalExpr), each item
// read through the trailing property path, MISSING items dropped exactly
// as AQL's []-operator drops them (a jsonb path read yields SQL NULL for
// a missing property or a non-object item, while a stored JSON null stays
// the observable jsonb 'null' scalar), gathered back into one jsonb array
// in document order, with the missing/non-array parent collapsing to the
// empty array (the FLATTEN-over-guarded-[] twin, EMPTY_ON_NULL posture).
// The registered form is one iterated property step plus trailing plain
// property steps; indexes, multiple iterate steps, filters, fallbacks,
// DISTINCT, prepared references, and unit normalization stay typed
// refusals (their capabilities belong to the later array-rendering and
// unit slices).
func (r *planRenderer) stageArraySelectorExpression(projection ir.PhysicalProjection) (string, error) {
	extract := projection.Expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: array source projection %q: EXTRACT expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: array source projection %q: null behavior %q has no registered array reading (the empty-array posture is EMPTY_ON_NULL)",
			ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.NullBehavior))
	}
	if extract.Selector.Filter != nil || len(extract.Fallbacks) != 0 || extract.Distinct ||
		extract.Prepared != nil || extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: array source projection %q uses a richer selector form (filter, fallbacks, DISTINCT, prepared, or unit normalization) that is not registered",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf("%w: array source projection %q must read the row document at [\"payload\"], got %v",
			ErrUnsupportedPostgresPlan, projection.Name, extract.Source.Path)
	}
	scope, ok := r.rows[extract.Source.Variable]
	if !ok || scope.alias != r.rootAlias {
		return "", fmt.Errorf("%w: array source projection %q reads a variable the composition source region has not registered (L-16.6)",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	iterate := -1
	for index, step := range extract.Selector.Steps {
		if step.Index != nil {
			return "", fmt.Errorf("%w: array source projection %q: indexed steps are the unit slice's registered reading, not the composition array gathering",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: array source projection %q: selector step %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, projection.Name, step.Field)
		}
		if step.Iterate {
			if iterate >= 0 {
				return "", fmt.Errorf("%w: array source projection %q: repeated steps beyond one iterate step are the later array-rendering capability",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			iterate = index
		}
	}
	if iterate < 0 {
		return "", fmt.Errorf("%w: array source projection %q has no iterated selector step; a many column needs exactly one [] step",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	// The iterated step's own field names the array: root.note[].text
	// gathers over the note array itself, so the guarded array path
	// includes the iterate step (the AQL FLATTEN twin iterates
	// doc.note, not doc).
	prefixSteps := make([]string, 0, iterate+1)
	for _, step := range extract.Selector.Steps[:iterate+1] {
		prefixSteps = append(prefixSteps, step.Field)
	}
	tailSteps := make([]string, 0, len(extract.Selector.Steps)-iterate-1)
	for _, step := range extract.Selector.Steps[iterate+1:] {
		tailSteps = append(tailSteps, step.Field)
	}
	arr := r.documentColumn(r.rootAlias)
	if len(prefixSteps) > 0 {
		// Non-empty prefix: the guarded array is read through the property
		// path above the iterated step.
		var err error
		arr, err = r.documentPathFromExpression(arr, prefixSteps)
		if err != nil {
			return "", fmt.Errorf("array source projection %q: %w", projection.Name, err)
		}
	}
	var leafTail string
	if len(tailSteps) == 0 {
		// The item itself: the registered whole-document reading.
		leafTail = "{}"
	} else {
		leafTail = "{" + strings.Join(mapSlice(tailSteps, arrayLiteralElement), ",") + "}"
	}
	leafTailParameter := r.textParameter(leafTail, "path:"+leafTail)
	leaf := "e #> $" + strconv.Itoa(leafTailParameter) + "::text[]"
	return "(CASE WHEN jsonb_typeof(" + arr + ") = 'array' THEN COALESCE((SELECT jsonb_agg(" + leaf +
		") FROM jsonb_array_elements(" + arr + ") AS e WHERE " + leaf + " IS NOT NULL), '[]'::jsonb) ELSE '[]'::jsonb END)", nil
}

func mapSlice(input []string, mapFn func(string) string) []string {
	out := make([]string, 0, len(input))
	for _, value := range input {
		out = append(out, mapFn(value))
	}
	return out
}

// validateStageColumnClass is the builder-side schema contract: the declared
// cardinality must agree with the storage class the emitter minted, so a
// many-cardinality column can never be read as a scalar and a required
// scalar can never arrive as an unguarded jsonb blob.
func validateStageColumnClass(column ir.PhysicalStageColumn, jsonb bool) error {
	cardinality := strings.ToUpper(strings.TrimSpace(column.Cardinality))
	if cardinality == "MANY" && !jsonb {
		return fmt.Errorf("cardinality %q needs the jsonb storage class but the emitter rendered a SQL scalar", column.Cardinality)
	}
	if column.RootContributorResourceType != "" {
		// The compiler stamps RootContributorResourceType on two distinct
		// shapes (lower/recipe_construction.go:2075-2130): the ROOT IDENTITY
		// scalar — internal string _key, required_one/optional_one, a
		// registered scalar the spine already renders — and the population
		// root-contributor SET column (__loom_root_contributor_keys, MANY),
		// whose values are bound only by RenderPopulationRootScan
		// (population.go). The composition renders the scalar identity and
		// refuses the set: a population-bound contributor column read over
		// the spine would silently substitute spine rows for population
		// rows, the exact identity-injection class the population contract
		// isolates.
		if !(column.Internal && column.Name == "_key" &&
			strings.EqualFold(strings.TrimSpace(column.Kind), "string") &&
			(strings.EqualFold(strings.TrimSpace(column.Cardinality), "required_one") ||
				strings.EqualFold(strings.TrimSpace(column.Cardinality), "optional_one"))) {
			return fmt.Errorf("root-contributor columns are bound only by RenderPopulationRootScan (population.go) and are outside the registered composition surface")
		}
	}
	return nil
}

// renderStageCTE renders one stage relation. Each stage gets a FRESH read
// contract for its own input variable, so a stage can never read a
// non-adjacent stage's columns (L-16.1: the chain re-read plus this
// builder-side scoping).
func (r *planRenderer) renderStageCTE(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (body string, ctes []string, out map[string]stageColumnFact, err error) {
	r.stageRows = map[string]stageRowScope{}
	r.unpivotOutputs = nil
	scope := stageRowScope{qualification: stageTerminalAlias + ".", columns: facts}
	switch stage.Kind {
	case ir.PhysicalStageFilterOp, ir.PhysicalStageDeriveOp:
		body, out, err = r.renderPassThroughStage(plan, stage, relation, scope)
	case ir.PhysicalStageExpandOp:
		body, out, err = r.renderExpandStage(plan, stage, index, relation, scope)
	case ir.PhysicalStageUnpivotOp:
		body, out, err = r.renderStageMelt(plan, stage, relation, scope)
	case ir.PhysicalStageGroupOp:
		body, ctes, out, err = r.renderStageGroup(plan, stage, index, relation, facts)
	case ir.PhysicalStagePivotOp:
		body, ctes, out, err = r.renderStagePivot(plan, stage, index, relation, facts)
	default:
		return "", nil, nil, fmt.Errorf("%w: stage kind %q has no registered composition rendering",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if err != nil {
		return "", nil, nil, err
	}
	if _, ok := out[stage.RowIdentityColumn]; !ok {
		return "", nil, nil, fmt.Errorf("%w: stage %q does not emit its registered row identity column %q (stage identity contract)",
			ErrUnsupportedPostgresPlan, stage.ID, stage.RowIdentityColumn)
	}
	return body, ctes, out, nil
}

// renderPassThroughStage renders a FILTER or DERIVE stage: the stage filter
// becomes the WHERE conjunct, the DERIVE LETs become materialized columns,
// and every output column is the positional read of the prior relation
// (render/aql/stage_sequence.go:405-428 twin).
func (r *planRenderer) renderPassThroughStage(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	r.stageRows[stage.InputRowVariable] = scope
	r.stageRows[stage.OutputRowVariable] = scope
	direct := map[string]stageColumnExpr{}
	for _, operation := range stage.DerivedLets {
		derived := operation.ExpressionLet
		if derived.Expression.Kind != ir.PhysicalValueExpression || derived.Expression.Value == nil {
			return "", nil, fmt.Errorf("%w: a composition DERIVE LET of expression kind %q has no registered rendering; CALL, selector, and unit-normalization expressions belong to their own slice registrations (calls.go, selectors.go, unit_normalization.go) and are refused here (L-16.6)",
				ErrUnsupportedPostgresPlan, string(derived.Expression.Kind))
		}
		expression, err := r.resolveValue(*derived.Expression.Value)
		if err != nil {
			return "", nil, fmt.Errorf("derive LET %q: %w", derived.Variable, err)
		}
		jsonb := isPayloadExpression(expression)
		// The spine's own LET contract (unnest.go:716-729) keeps derived
		// reads path-aware, so a later stage or the output projection list
		// can address a property of a derived jsonb value.
		r.rows[derived.Variable] = rowScope{derivedValue: expression, derivedJSON: jsonb}
		direct[derived.Variable] = stageColumnExpr{expr: expression, jsonb: jsonb}
	}
	var where []string
	if stage.Filter != nil {
		if stage.Filter.Expression != nil && stage.Filter.Expression.Kind == ir.PhysicalExistsPredicate {
			return "", nil, fmt.Errorf("%w: an EXISTS filter inside a composition stage is a related-route contract (sets.go renderRelatedExists) and is refused over intermediate rows (L-16.6)",
				ErrUnsupportedPostgresPlan)
		}
		clause, err := r.renderFilter(plan, *stage.Filter)
		if err != nil {
			return "", nil, err
		}
		where = append(where, clause)
	}
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation)
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql, facts, nil
}

// stageRelationSource spells one stage's FROM clause over its immediately
// prior relation, always under the same alias so one read spelling serves
// every stage.
func stageRelationSource(relation string) string {
	return relation + " AS " + stageTerminalAlias
}

// renderExpandStage ports render/aql/construction_expand.go:10-69: the raw
// read, the array-type ASSERT, the null→[] items form, the ERROR empty
// policy raise, the positional expansion with its ordinal, the item-type
// ASSERT, and the compact composition identity.
func (r *planRenderer) renderExpandStage(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, index int, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	expand := stage.Expand
	if _, ok := plan.BindVars[expand.ConstructionIDBindKey]; !ok {
		return "", nil, fmt.Errorf("%w: expand construction ID bind %q is missing", ErrUnsupportedPostgresPlan, expand.ConstructionIDBindKey)
	}
	input, ok := scope.columns[expand.InputColumn]
	if !ok {
		return "", nil, fmt.Errorf("%w: expand input column %q is not among the prior stage's registered columns (L-16.1)",
			ErrUnsupportedPostgresPlan, expand.InputColumn)
	}
	if !input.jsonb {
		return "", nil, fmt.Errorf("%w: expand input column %q is a SQL scalar and cannot hold an array", ErrUnsupportedPostgresPlan, expand.InputColumn)
	}
	identityColumn := ""
	for _, column := range stage.InputColumns {
		if column.Identity {
			identityColumn = column.Name
			break
		}
	}
	if identityColumn == "" {
		return "", nil, fmt.Errorf("%w: expand input has no row identity column (render/aql/construction_expand.go:26 twin)", ErrUnsupportedPostgresPlan)
	}
	identity, ok := scope.columns[identityColumn]
	if !ok {
		return "", nil, fmt.Errorf("%w: expand input identity column %q is missing from the prior relation (L-16.1)",
			ErrUnsupportedPostgresPlan, identityColumn)
	}
	typeofTag := r.textParameter("array", "text:typeof:array")
	itemTypeOf, err := stageScalarTypeOf(expand.InputKind)
	if err != nil {
		return "", nil, fmt.Errorf("expand item: %w", err)
	}
	itemTypeBind := r.textParameter(itemTypeOf, "text:typeof:"+itemTypeOf)

	raw := stageTerminalAlias + "." + `"` + input.alias + `"`
	items := "COALESCE(NULLIF(" + raw + ", 'null'::jsonb), '[]'::jsonb)"
	// The AQL ASSERT order is preserved inside one CASE, whose branches
	// PostgreSQL evaluates left to right: the array-type assert, then the
	// ERROR empty assert, then the items. Riding the LATERAL argument is
	// what keeps the AQL raise position — the argument is evaluated for
	// every input row, including a zero-item row a CROSS JOIN would drop.
	guarded := "CASE WHEN NOT (" + raw + " IS NULL OR " + raw + " = 'null'::jsonb OR jsonb_typeof(" + raw + ") = $" +
		strconv.Itoa(typeofTag) + "::text) THEN " + guardCastJSONB(stageExpandArrayTypeTag)
	if expand.EmptyPolicy == ir.PhysicalUnnestError {
		guarded += " WHEN jsonb_array_length(" + items + ") = 0 THEN " + guardCastJSONB(stageExpandEmptyTag)
	}
	guarded += " ELSE " + items + " END"

	expansion := "jsonb_array_elements(" + guarded + ") WITH ORDINALITY"
	join := "CROSS JOIN LATERAL " + expansion + " AS " + expandRelation(index) + "(item, ord)"
	if expand.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		// The AQL `LENGTH(items) == 0 ? [null] : RANGE(...)` twin: a LEFT
		// JOIN LATERAL keeps exactly one null-item row for a zero-item
		// input (the unnest.go zero-item-kept precedent).
		join = "LEFT JOIN LATERAL " + expansion + " AS " + expandRelation(index) + "(item, ord) ON TRUE"
	}
	item := expandRelation(index) + ".item"
	ordinal := expandRelation(index) + ".ord - 1"

	// The per-item ASSERT twin (construction_expand.go:57): null-exempt
	// exactly like AQL's `item == null OR TYPENAME(item) == @type`.
	itemGuard := "(CASE WHEN " + item + " IS NULL OR " + item + " = 'null'::jsonb OR jsonb_typeof(" + item +
		") = $" + strconv.Itoa(itemTypeBind) + "::text THEN 1 ELSE " + guardCast(stageExpandItemTypeTag) + " END) > 0"

	// The identity twin of construction_expand.go:58-60:
	// TO_STRING([["input", row[id]], ["construction", @cid], ["ordinal", i]])
	// — an OUTER array of MEMBER ARRAYS, JSON-compact, the ordinal a bare
	// JSON number (the PRESERVE_PARENT null ordinal reads "null").
	identityRead := stageTerminalAlias + "." + `"` + identity.alias + `"`
	constructionBind := r.textParameter(bindStringVar(plan.BindVars, expand.ConstructionIDBindKey), "")
	identityParts := []string{
		`'["input",' || COALESCE(to_jsonb(` + identityRead + `)::text, 'null') || ']'`,
		`'["construction",' || to_jsonb($` + strconv.Itoa(constructionBind) + `::text) || ']'`,
		`'["ordinal",' || COALESCE((` + expandRelation(index) + `.ord - 1)::text, 'null') || ']'`,
	}
	identityText := "'[' || " + strings.Join(identityParts, " || ',' || ") + " || ']'"

	direct := map[string]stageColumnExpr{
		expand.OutputColumn:     {expr: item, jsonb: true},
		expand.OrdinalColumn:    {expr: "(" + ordinal + ")", jsonb: false},
		stage.RowIdentityColumn: {expr: identityText, jsonb: false},
	}
	r.stageRows[stage.InputRowVariable] = scope
	r.stageRows[stage.OutputRowVariable] = scope
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation) + " " + join +
		" WHERE " + itemGuard
	return sql, facts, nil
}

// expandRelation mints the per-stage ordinality expansion alias.
func expandRelation(index int) string { return stageSequenceCTEPrefix + "ex" + strconv.Itoa(index) }

// stageScalarTypeOf maps one registered scalar kind onto the jsonb_typeof
// name the AQL TYPENAME assert compares against (aqlTableScalarType twin).
func stageScalarTypeOf(kind string) (string, error) {
	switch kind {
	case "STRING", "CODE":
		return "string", nil
	case "INTEGER", "DECIMAL":
		return "number", nil
	case "BOOLEAN":
		return "boolean", nil
	default:
		return "", fmt.Errorf("%w: expand item kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
}

// renderStageMelt renders an UNPIVOT stage over the prior relation by the
// SAME melt region the inline door uses (unpivot.go), so a staged melt can
// never drift from the registered melt (the byte-preservation law).
func (r *planRenderer) renderStageMelt(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, relation string, scope stageRowScope) (string, map[string]stageColumnFact, error) {
	unpivot := stage.Unpivot
	inputExprs := make(map[string]string, len(unpivot.InputProjections))
	for _, projection := range unpivot.InputProjections {
		expression, err := r.resolveStageColumnRead(plan, scope, projection)
		if err != nil {
			return "", nil, fmt.Errorf("unpivot input %q: %w", projection.Name, err)
		}
		inputExprs[projection.Name] = expression
	}
	r.unpivotSeq++
	alias := "x" + strconv.Itoa(r.unpivotSeq)
	region, err := r.renderMeltRegion(plan, *unpivot, inputExprs, alias)
	if err != nil {
		return "", nil, err
	}
	// The melt output contract routes through the registered read map, so
	// melt outputs can never resolve through the stored-row path.
	r.unpivotOutputs = map[string]map[string]string{unpivot.OutputRowVariable: region.columns}
	direct := map[string]stageColumnExpr{}
	for name, expression := range region.columns {
		direct[name] = stageColumnExpr{expr: expression, jsonb: name == unpivot.KeyOutput || name == unpivot.ValueOutput}
	}
	r.stageRows[stage.InputRowVariable] = scope
	selects, facts, err := r.renderStageOutputSelects(plan, stage, direct, scope)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + stageRelationSource(relation) + " " + region.lateral
	if len(region.conjuncts) > 0 {
		sql += " WHERE " + strings.Join(region.conjuncts, " AND ")
	}
	return sql, facts, nil
}

// resolveStageColumnRead renders one authored projection as a read over the
// prior relation, accepting only the registered column-name form.
func (r *planRenderer) resolveStageColumnRead(plan ir.PhysicalPlan, scope stageRowScope, projection ir.PhysicalProjection) (string, error) {
	if projection.Expression != nil {
		// The lowering-minted carry-over form (the AQL MERGE(inputRow,
		// {…}) twin, render/aql/construction_expand.go:10-69): an
		// OBJECT_LOOKUP over the stage's registered input row variable
		// whose key bind names one prior-stage column. It reads exactly
		// through the named-column contract, never through the stored-row
		// path.
		if projection.Expression.Kind == ir.PhysicalObjectLookupExpression && projection.Expression.ObjectLookup != nil &&
			projection.Expression.Cardinality == ir.PhysicalScalarCardinality &&
			projection.Expression.NullBehavior == ir.PhysicalPreserveNull {
			lookup := projection.Expression.ObjectLookup
			if lookup.ObjectVariable == "" || lookup.KeyBindKey == "" {
				return "", fmt.Errorf("%w: stage input projection %q: the carry-over object lookup needs its object variable and key bind",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			if _, registered := r.stageRows[lookup.ObjectVariable]; !registered {
				return "", fmt.Errorf("%w: stage input projection %q reads object variable %q, which no stage row registers (L-16.1)",
					ErrUnsupportedPostgresPlan, projection.Name, lookup.ObjectVariable)
			}
			column, ok := plan.BindVars[lookup.KeyBindKey].(string)
			if !ok || strings.TrimSpace(column) == "" {
				return "", fmt.Errorf("%w: stage input projection %q: carry-over key bind %q is not a non-blank string",
					ErrUnsupportedPostgresPlan, projection.Name, lookup.KeyBindKey)
			}
			return r.resolveStageRowValue(scope, ir.PhysicalValue{Variable: lookup.ObjectVariable, Path: []string{column}})
		}
		return "", fmt.Errorf("%w: stage input projection %q uses expression kind %q; staged inputs are named intermediate columns and selector/call forms register with their own slices (L-16.6)",
			ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.Kind))
	}
	if projection.Value.Variable == "" || len(projection.Value.Path) == 0 {
		return "", fmt.Errorf("%w: stage input projection %q is not a named intermediate column read", ErrUnsupportedPostgresPlan, projection.Name)
	}
	return r.resolveStageRowValue(scope, projection.Value)
}

// renderStageGroup renders a GROUP stage through the group slice's ONE
// aggregate region emitter (group.go), repointed at the prior relation: the
// g_rows witnesses, the missing-key policies, the reducers, the row-value
// pools, and the construction identity all stay the registered texts.
func (r *planRenderer) renderStageGroup(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (string, []string, map[string]stageColumnFact, error) {
	columns := make([]groupColumn, 0, len(stage.InputColumns))
	for _, column := range stage.InputColumns {
		fact, ok := facts[column.Name]
		if !ok {
			return "", nil, nil, fmt.Errorf("%w: group input column %q is not among the prior stage's registered columns (L-16.1)",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		// The group region's consuming CTEs each hold exactly one relation,
		// so its reads are unqualified (group.go:560-563).
		columns = append(columns, groupColumn{
			name: column.Name, alias: fact.alias, ref: `"` + fact.alias + `"`, jsonb: fact.jsonb,
		})
	}
	jsonbOutputs := map[string]bool{}
	for _, rowValue := range stage.Group.RowValues {
		// The storage class follows the emitter exactly: an ALL policy
		// row value materializes as to_jsonb(ARRAY_AGG(...)) — jsonb —
		// while the ONE policy collapses through MIN(...) #>> '{}' and
		// stays a SQL scalar. Marking a ONE row value jsonb would make a
		// later stage read it with a document operator and abort at
		// analysis (42883), so the fact must describe what group.go
		// actually emits per policy (groupRowValueExpressionFor).
		jsonbOutputs[rowValue.Output] = rowValue.Policy == "ALL"
	}
	region, err := r.renderGroupAggregateRegion(plan, stage, columns,
		"SELECT * FROM "+relation, "__loom_g"+strconv.Itoa(index)+"_",
		stage.OutputColumns, stage.RowIdentityColumn)
	if err != nil {
		return "", nil, nil, err
	}
	out := factsFromNames(region.names, func(name string) bool { return jsonbOutputs[name] })
	return region.final, region.ctes, out, nil
}

// renderStagePivot renders a staged NON-coded PIVOT through the pivot
// slice's ONE region emitter (pivot.go), repointed at the prior relation.
// The consumed reads wrap the materialized column in to_jsonb so the slot
// column is uniformly jsonb — the AQL slot array's value form — and
// document-path category presence is refused because an intermediate row is
// no longer a document (the presence must ride a materialized companion).
func (r *planRenderer) renderStagePivot(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	index int,
	relation string,
	facts map[string]stageColumnFact,
) (string, []string, map[string]stageColumnFact, error) {
	pivot := stage.GroupedPivot
	scope := stageRowScope{qualification: stageTerminalAlias + ".", columns: facts}
	projectionNames := make([]string, 0, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		projectionNames = append(projectionNames, column.Name)
	}
	region, err := r.renderGroupedPivotRegion(plan, pivot, "__loom_pv"+strconv.Itoa(index)+"_",
		projectionNames, stage.RowIdentityColumn, relation+" AS "+stageTerminalAlias,
		func(projection ir.PhysicalProjection) (string, ir.PhysicalNullBehavior, error) {
			expression, err := r.resolveStageColumnRead(plan, scope, projection)
			if err != nil {
				return "", "", err
			}
			return "to_jsonb(" + expression + ")", ir.PhysicalPreserveNull, nil
		}, nil)
	if err != nil {
		return "", nil, nil, err
	}
	// Every pivot output rides a jsonb slot except the registered row
	// identity, which the emitter renders as compact composition TEXT.
	out := factsFromNames(region.names, func(name string) bool { return name != stage.RowIdentityColumn })
	return region.final, region.ctes, out, nil
}

// factsFromNames mints positional facts for one emitter's output names.
func factsFromNames(names []string, jsonb func(string) bool) map[string]stageColumnFact {
	facts := make(map[string]stageColumnFact, len(names))
	for index, name := range names {
		facts[name] = stageColumnFact{alias: "c" + strconv.Itoa(index), jsonb: jsonb(name)}
	}
	return facts
}

// renderStageOutputSelects is the ONE positional emitter for every stage
// relation: the declared OutputColumns are selected positionally as
// renderer-minted c0..cN aliases in schema order (no plan column name ever
// reaches SQL text), each satisfied by a reshape expression the payload
// owns, or by the registered output projection read over the prior
// relation. The stage row identity is emitted by its own emitter arm, so
// the schema contract (name, Kind, Cardinality, identity) is enforced here
// for every kind alike.
func (r *planRenderer) renderStageOutputSelects(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	direct map[string]stageColumnExpr,
	scope stageRowScope,
) ([]string, map[string]stageColumnFact, error) {
	byName := map[string]ir.PhysicalProjection{}
	for _, projection := range stage.OutputProjections {
		if _, seen := byName[projection.Name]; seen {
			return nil, nil, fmt.Errorf("%w: output projection %q is declared twice (stage schema parity)",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		byName[projection.Name] = projection
	}
	selects := make([]string, 0, len(stage.OutputColumns))
	facts := make(map[string]stageColumnFact, len(stage.OutputColumns))
	for index, column := range stage.OutputColumns {
		var expression string
		isJSONB := false
		if rendered, ok := direct[column.Name]; ok {
			expression, isJSONB = rendered.expr, rendered.jsonb
		} else {
			projection, ok := byName[column.Name]
			if !ok {
				return nil, nil, fmt.Errorf("%w: output column %q has no output projection and no registered reshape expression (stage output/input parity)",
					ErrUnsupportedPostgresPlan, column.Name)
			}
			if projection.Presence != nil && projection.Value.Variable == "" {
				proof, err := r.stagePresenceProof(scope, projection)
				if err != nil {
					return nil, nil, err
				}
				expression, isJSONB = proof, true
			} else {
				read, err := r.resolveStageColumnRead(plan, scope, projection)
				if err != nil {
					return nil, nil, fmt.Errorf("output column %q: %w", column.Name, err)
				}
				expression, isJSONB = read, isPayloadExpression(read)
			}
		}
		if err := validateStageColumnClass(column, isJSONB); err != nil {
			return nil, nil, fmt.Errorf("%w: stage output column %q: %v", ErrUnsupportedPostgresPlan, column.Name, err)
		}
		alias := "c" + strconv.Itoa(index)
		selects = append(selects, expression+` AS "`+alias+`"`)
		facts[column.Name] = stageColumnFact{alias: alias, jsonb: isJSONB}
	}
	return selects, facts, nil
}

// stagePresenceProof renders the presence companion of one intermediate
// column: the registered two-state jsonb boolean of null_semantics.go, over
// the intermediate reading in which an SQL NULL or a stored JSON null is
// the AQL absent/null state.
func (r *planRenderer) stagePresenceProof(scope stageRowScope, projection ir.PhysicalProjection) (string, error) {
	if len(projection.Presence.Paths) != 1 || len(projection.Presence.Paths[0]) == 0 {
		return "", fmt.Errorf("%w: a stage presence companion needs exactly one column path (null_semantics.go contract)",
			ErrUnsupportedPostgresPlan)
	}
	name := projection.Presence.Paths[0][0]
	fact, ok := scope.columns[name]
	if !ok {
		return "", fmt.Errorf("%w: presence companion %q names no registered intermediate column (L-16.1)",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(projection.Presence.Paths[0]) > 1 {
		return "", fmt.Errorf("%w: a stage presence companion cannot address a document tail path over an intermediate column",
			ErrUnsupportedPostgresPlan)
	}
	base := scope.qualification + `"` + fact.alias + `"`
	if !fact.jsonb {
		return "(CASE WHEN " + base + " IS NULL THEN 'false'::jsonb ELSE 'true'::jsonb END)", nil
	}
	return "(CASE WHEN COALESCE(" + base + " IS NULL OR " + base + " = 'null'::jsonb, false) THEN 'false'::jsonb ELSE 'true'::jsonb END)", nil
}
