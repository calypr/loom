package postgres

// The coded Group tuple (CODED_GROUP, pg-sql-19) is registered here as its
// own closed plan slice: one direct root scan with the canonical scope
// block and the spine's own renderable source operations, one authored
// CODED_GROUP construction stage consuming that source inline, and the
// tuple-multiset grouping rendered as one WITH statement. Dispatch is the
// one additive hook in RenderPhysicalPlan (the structural twin of the
// graph.go door, render.go:152-163): any plan whose stage sequence
// mentions a CODED_GROUP stage is rendered WHOLE here, so no spine arm
// ever sees a construction stage and the StageSequence refusal at
// render.go:208-211 stays byte-identical for every other sequence shape.
// validateCodedGroupPlanSurface is the single contract definition behind
// both doors (unnest.go / unpivot.go / graph.go precedent).
//
// AQL ground truth is render/aql/construction_coded_group.go
// renderConstructionCodedGroupStage as dispatched by stage_sequence.go:324
// through the inline door codedGroupSourceRootVariable (stage_sequence.go
// :606-626). The registered mapping:
//
//   - The source region (root scan, canonical scope, typed FILTERs,
//     canonical auth LET, SORT/LIMIT window) renders through the
//     UNTOUCHED spine arms — loadScope, renderRootScan,
//     acceptCanonicalScopeFilter/renderFilter (which owns
//     rejectOptionalRowReferences), acceptCanonicalAuthLet,
//     renderWindowSort/renderWindowLimit (W1) — assembled as
//     "WITH cg_src AS (…)". The AQL inline door also renders the full
//     source and discards only the terminal projection
//     (stage_sequence.go:603-607, "only its terminal projection is
//     omitted"); this slice does the same: the source RETURN must exist
//     (lowering pins it, lower/recipe_construction_coded_group.go:180-184)
//     and its projections carry no PostgreSQL meaning, because CODED_GROUP
//     re-reads the ROOT rows, not the projected values.
//   - Root identity: AQL keys the grouping on the root document key
//     (SourceIdentityColumn == "_key", ir/physical_stage_validation.go
//     :2274) and the inline door only opens when lowering proved one
//     source row per root key (codedGroupSourceRootVariable
//     stage_sequence.go:613 + SourceRowsUnique, ir:2270-2271). The
//     PostgreSQL store carries the same logical identity in logical_id
//     (canonicalKeyColumn, render.go:119-131; the ingest contract quoted
//     at graph.go:513-521), so cg_root_key = logical_id is the exact
//     counterpart and "distinct source records" counts roots, never rows.
//   - Coding enumeration: the AQL path walk (construction_coded_group.go
//     :54-71) reads each non-repeated attribute and nests one FOR loop per
//     repeated segment inside ONE subquery, each guarded with
//     (x == null ? [] : (IS_ARRAY(x) ? x : [])); an empty, null, or
//     non-array value prunes that branch entirely — it contributes NO
//     candidate. Only when the WHOLE nested expansion produced zero items
//     does the root substitute exactly one null candidate
//     (LENGTH(rawCodings) == 0 ? [null] : rawCodings, :75). The PostgreSQL
//     counterpart mints the chain as ONE LEFT JOIN LATERAL around
//     (SELECT lastElem FROM jsonb_array_elements(G1) AS cgx1(elem) CROSS
//     JOIN LATERAL ... AS cgxN(elem)): CROSS JOIN LATERAL reproduces the
//     FOR-loop branch pruning (an emptied inner expansion removes its
//     outer row), and the single wrapping LEFT JOIN reproduces the
//     per-root zero-item rule — exactly one SQL-NULL-item candidate
//     appears only when the entire chain yields zero rows, which the
//     total validity predicate reads invalid. Per-level LEFT JOINs would
//     instead materialize a NULL candidate for EVERY emptied branch (the
//     component[].code.coding[] mixed-branch cell: one component with
//     codings, another with an empty coding array must contribute no
//     null tuple under GROUP and no raise under ERROR — pinned live in
//     coded_group_test.go). Consecutive scalar segments collapse into one
//     #> text[] extraction through documentPathFromExpression
//     (null_semantics.go, the package's single path-binding authority —
//     path segments reach SQL only as $n parameters), and every repeated
//     segment expands jsonb_array_elements behind a
//     CASE WHEN jsonb_typeof(cur) = 'array' THEN cur ELSE '[]'::jsonb END
//     guard, so SQL NULL, the jsonb null scalar, a non-array value, and a
//     missing parent all read as the empty array exactly as the AQL
//     null/IS_ARRAY guard does. A single-repeated-segment path keeps the
//     flat LEFT JOIN LATERAL jsonb_array_elements(guard) AS cgx1(elem)
//     ON true form, whose zero-row behavior already equals the chain.
//   - Tuple validity: the AQL valid LET (:77) is object AND
//     TYPENAME(system) == "string" AND TRIM(system) != "" AND
//     TYPENAME(code) == "string" AND TRIM(code) != ""; the key LETs (:100
//     -102) emit system/code verbatim (untrimmed) and keep version only
//     when it is a non-blank string, else null. The PostgreSQL form is
//     COALESCE(jsonb_typeof(…) = 'object' AND …, false) over
//     item ->> $key with btrim(item ->> $key, $trim) <> ''. $trim is the
//     ONE trim-set constant of this package, codedGroupTrimCharacters
//     (" "): ArangoDB's AQL TRIM trims spaces and PostgreSQL's btrim
//     defaults to spaces too, but the set is stated explicitly through
//     the single constant rather than inherited from either default
//     (structural law: a duplicated trim-set literal anywhere else in
//     this package is a test failure, and the live pins cover tab /
//     newline / Unicode-space-padded key values so the space-only reading
//     is pinned, not assumed). Values are emitted untrimmed exactly as
//     AQL (:100-102): "\tx" is a different tuple from "x".
//   - MissingKeyPolicy (construction_coded_group.go:80-88): GROUP keeps
//     every candidate and collapses invalid ones into the
//     (null,null,null) tuple (AQL COLLECT groups null keys together;
//     PostgreSQL DISTINCT / GROUP BY treat NULLs as equal); EXCLUDE is
//     the WHERE cg_valid conjunct; ERROR is the per-candidate ASSERT
//     counterpart FILTER ASSERT(valid,
//     "CONSTRUCTION_CODED_GROUP_MISSING_KEY") (:85) rendered as the
//     CASE WHEN cg_valid THEN 1 ELSE guardCast(tag) END > 0 conjunct over
//     population.go's constant-folding barrier (guardCast /
//     guardBarrierGUC, population.go:1059-1113). Placement is the AQL
//     position exactly: the conjunct lives in the WHERE of cg_tuples,
//     evaluated on EVERY candidate row — including the zero-item NULL
//     candidate a coding-less root contributes (the ASSERT fires on the
//     [null] candidate in AQL too) — and before the DISTINCT collapse, so
//     a query whose grouped result would be empty still raises whenever
//     any in-scope root carries an invalid candidate, while a zero-source
//     query never raises in either backend (no candidate rows).
//     Live-proven, with its sabotage controls, in coded_group_test.go.
//   - Repeated-Coding identity: AQL deduplicates per root with the inner
//     COLLECT (one tuple row per (root, tuple), :103-106) and counts
//     contributing ROOTS with the outer COLLECT … AGGREGATE SUM(1)
//     (:111-112) — a record containing the identical Coding twice
//     contributes one. The PostgreSQL counterparts are cg_tuples =
//     SELECT DISTINCT cg_root_key, system, version, code (per-root local
//     dedup; PostgreSQL's DISTINCT treats two NULL key columns as equal
//     exactly like AQL COLLECT) and cg_grouped = GROUP BY 1,2,3 with
//     COUNT(*) (contributing roots, never raw codings). The accepted
//     fixture pins all of it: two systems sharing one code, versioned
//     plus versionless codings, the duplicate identical coding inside one
//     record, missing tuples, and two source records produce exact
//     grouped tuples and counts.
//   - Stable row identity: the AQL identity is TO_STRING(["construction",
//     @constructionID, "operation", "CODED_GROUP", "occurrence",
//     @occurrenceID, "path", @codingPath, "system", s, "version", v,
//     "code", c]) (:127-129). The PostgreSQL form is the renderer-composed
//     JSON-compact concatenation (unpivot.go:64-81 precedent): '[' ||
//     members joined by ',' || ']', constant labels as renderer-owned
//     quoteSQLLiteral constants, the construction / occurrence / path bind
//     values through to_jsonb($n::text) of TEXT parameters, and the three
//     key values through COALESCE(to_jsonb(expr)::text, 'null') so both
//     key-null states spell null exactly. Every member is a string or
//     null, so to_jsonb's quoting is injective over the tuple and the
//     identity uniquely determines (constructionID, occurrenceID,
//     codingPath, system, version, code). Arango's TO_STRING rendering of
//     exotic control characters remains a QUARANTINED unverified runtime
//     fact (same bound as unpivot.go:73-78); byte-exactness is claimed
//     against independent literal expected rows only, and fixtures use
//     deterministic identities.
//   - Final order and bound: AQL emits the stage-internal
//     SORT system ASC, version ASC, code ASC (:126) and then the sequence
//     terminal re-sorts the stage rows by the stable identity
//     (stage_sequence.go:399-403 — the omitTerminalRowSort exemption at
//     :400 covers only Group/CohortGroup kinds, NOT CODED_GROUP — and
//     :444-448 renders LIMIT @previewLimit over those identity-ordered
//     rows). The internal sort is therefore unobservable output
//     semantics: the final rows are ordered by the identity string alone.
//     The PostgreSQL final order is ORDER BY <identity> COLLATE "C" ASC
//     LIMIT $n (graph.go:658 COLLATE "C" precedent): byte-deterministic
//     across executions and locales. Registered divergence D-19a: with
//     non-ASCII key values, PostgreSQL COLLATE "C" orders by raw UTF-8
//     bytes over the identity text while Arango orders by its own string
//     total order; the live non-ASCII cells in coded_group_test.go pin
//     what PostgreSQL actually does rather than claiming equality with an
//     Arango page. Agreement is claimed, per the window.go:103-110 bound,
//     for the deterministic ASCII identities used across the parity
//     fixtures.
//   - Preview limit: sequence.PreviewLimitBindKey (the AQL terminal LIMIT,
//     stage_sequence.go:446-448) is bound as one bigint $n parameter after
//     the identity ORDER BY; a non-int or non-positive bind value is a
//     typed refusal (graph.go:645-648 spelling).
//
// Refusal register (every line: probe-attested production-unreachable, or
// a registered accepted gap naming its enabling ticket; probe receipts at
// .ao/data/artifacts/loom-experiment-144/logs/pg19-reachability.log):
//
// L-entries are numbered in the pg-sql-10 L-table idiom (cf. L19,
// pg-sql-10).
//
//   - L-19.1 Second construction stage after CODED_GROUP, or any stage
//     kind other than CODED_GROUP in the sequence: registered accepted
//     gap naming pg-sql-16 ("Render composed construction stage
//     sequences"), coordinator-approved. Probe-attested reachable
//     ([CODED_GROUP, DERIVE] and [CODED_GROUP, FILTER] lower to two
//     stages and the AQL renderer renders both); AQL stays the only
//     backend that does. Evidence includes the staged-sequences
//     RED-at-branch-point capture
//     (logs/pg19-red-probe.log: "staged row-operator sequences are not
//     yet implemented for PostgreSQL") and the two-stage AQL render
//     (logs/pg19-reachability.log).
//   - L-19.2 REGISTERED (pg-sql-13 Q4) — was the accepted-at-recipe-level/
//     PG-typed-refused divergence of coordinator ruling 2026-11-10
//     (attribution, quoted verbatim from the ruling: "operator-config
//     entry counted as non-user surface per coordinator ruling
//     2026-11-10"). History kept for the verifier: the shape is accepted
//     by recipe validation (recipe/construction.go:1002-1007) and EMITTED
//     by lowering (lower/recipe_construction_coded_group.go:119-127)
//     through the narrow raw recipe-document channel only (recipe.Parse
//     via the server operator-config entry, internal/server/server.go:442;
//     both authoring paths are law-blocked — the codedGroup four-output
//     law, authoringv2/construction_validation.go:1694 +
//     validateDeclaredOutputIDs:1755-1769, and recipe-step output
//     membership, recipe/construction.go:1023-1028). pg-sql-13 closed the
//     gap: the inline ALL/ONE row values now render through the SHARED
//     group emitter (groupRowValueExpressionFor — mechanical dispatch per
//     the Q4 ruling; the contributing-root pool is cg_tuples, which is
//     exactly the AQL per-tuple record set, construction_coded_group.go:
//     46-125). The whole arm is additive: zero-row-value statements stay
//     byte-identical to the attested pg-sql-19 base. Load-bearing proofs:
//     TestCodedGroupIRRowValuesAliveControl pins the carry chain and the
//     shared ONE fragment byte-identical (parameter-index-normalized);
//     TestCodedGroupRowValuesSharedEmitterLaw forbids any inlined emitter
//     copy (RED banked at logs/pg13-shared-emitter-red-inlined-literal
//     .log); TestCodedGroupRowValuesLiveRender runs the pool live.
//     The ONE remaining refusal in this family is the many-cardinality
//     (local-array-expansion) input — REGISTERED NAMED GAP: the shared
//     pool emitter groupManyRowValuePoolFor renders from g_rows, so the
//     cg_tuples pool variant is wave 2 of this ticket; TestCodedGroupIR
//     RowValuesLiveZeroSideEffects keeps the fail-closed zero-SQL proof
//     on that shape.
//   - L-19.3 Non-first-stage (DOCUMENT re-query) shape: probe-attested
//     production-unreachable — recipe validation forces CODED_GROUP to be
//     the first step on the direct source projection
//     (recipe/construction.go:665-668, "prior construction stages are not
//     supported") and lowering sets InputStageID = SourceStageID
//     accordingly; every shape the probe lowered has
//     InputStageID == SourceStageID. Hand-assembled IR reaching this door
//     with another InputStageID is a typed refusal. Dependency note per
//     the ruling: if a future probe finds this shape reachable, it is
//     pg-sql-16 territory (L-19.1), not a new refusal surface here.
//   - L-19.4 REGISTERED NAMED DIVERGENCE — renders-at-AQL/
//     PG-typed-refused (coordinator F-3 ruling 2026-11-11,
//     "PROBE FIRST, no silent divergence either direction").
//     SET in the coded-group source region is whitelisted by lowering
//     (lower/recipe_construction_coded_group.go:176-179) and rendered
//     inline by AQL, so the probe decision was forced. Probe
//     (logs/pg19-f3-set-reachability.log, disposable legs archived at
//     artifacts/loom-experiment-144/probes/): a real
//     recipe.Parse→semantic→lower document with a traversal child emits
//     SET in the source region (setOps=1, ops
//     [ROOT_SCAN FILTER FILTER DERIVED_LET FILTER SET RETURN]) and the
//     AQL control renders it (err=nil). Gate chain on this head
//     (verifier-145 corrected, sabotage runs at 85e6b47b): this door
//     arm is the SOLE load-bearing gate. The statement renderer
//     (renderCodedGroupStatement) has no PhysicalSetOp dispatch arm —
//     the spine's renderSet is not wired into the coded-group path —
//     so sabotaging this arm with SET left whitelist-acceptable makes
//     the OPTIONAL plan RENDER with the SET silently dropped, the SQL
//     byte-for-byte the SET-less base (145's captured statement);
//     REQUIRED without the arm refuses at its typed required-match
//     FILTER rendering, traversal.go:415 "typed match filters must use
//     the Expression form" — not sets.go:236. The traversal.go:141
//     ENDPOINT_LOOKUP gate and the argument-integrity guard do NOT
//     fire on this committed path; they fired in the pre-fold probe
//     legs while SET was still handed to renderSet
//     (logs/pg19-f3-set-reachability.log) and stand as probe-era
//     evidence for why the shape stays unrenderable even if pg-sql-16
//     wires the dispatch: the default policy stamps ENDPOINT_LOOKUP on
//     every registered route (generated/fhirschema Traversals;
//     lower/storage_route.go:50-60), and the endpoint-rule dev-disabled
//     variant (LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL=off, a developer
//     switch, not a user surface — same non-user attribution as L-19.2)
//     emits an unreferenced placeholder. So no configuration renders
//     the whitelist arm and it is refused fail-closed at the door
//     naming pg-sql-16 as the enabling ticket (registering the
//     PostgreSQL contracts the probe located missing); corroborated by
//     the merged sets.go:82-95 finding "No current lowering output
//     reaches the hop-only renderer". The AQL inline render stays —
//     this is a named cross-target divergence, not an AQL regression.
//     Load-bearing proof: TestCodedGroupIRSetAliveControl builds the
//     lowering-emitted shape through the real producer channel
//     (plan.Validate passes) and pins the refusal cell-verbatim at both
//     the entry and the door; arm-removal sabotage RED banked at
//     logs/pg19-set-arm-removal-red.log, and the arm-removed
//     counterfactual is a SILENTLY-DROPPED statement, not a refusal or
//     a partial render (no dispatch arm exists; 145's sabotage table
//     at 85e6b47b). Fail-closed is zero SQL, zero side effects:
//     TestCodedGroupIRSetLiveZeroSideEffects.
//   - Population / cohort / candidate-seed roots: registered gap naming
//     population.go (pg-sql-18): the constructor-only
//     PostgresPopulationRootBinding makes RenderPopulationRootScan the
//     sole binder of membership roots, and the plain RenderPhysicalPlan
//     path (this door included) never carries one.
//   - TRAVERSAL, UNNEST, UNPIVOT, PATH_*, COLLECTION/KEY-SET/DOCUMENT
//     scans, EXPRESSION_LET, or any operation kind outside [ROOT_SCAN,
//     FILTER, canonical-DERIVED_LET, SORT, LIMIT, RETURN] (SET has its
//     own entry, L-19.4):
//     probe-attested production-unreachable for a CODED_GROUP plan —
//     lowering's own uniqueness gate refuses cardinality-changing
//     operations before a stage is ever built
//     (lower/recipe_construction_coded_group.go:165-193), and
//     ir/physical_stage_validation.go:2270 then demands SourceRowsUnique.
//     Hand-assembled IR is refused fail-closed.
//   - Non-canonical DERIVED_LET in the source region: the spine's own
//     refusal (render.go:379-391) is inherited verbatim; the canonical
//     auth LET alone is consumed.
//   - RowLineageReturn / CellTraceReturn / PopulationMappingReturn:
//     probe-attested production-unreachable through the compile target —
//     the PG compile path refuses each diagnostic surface at its own door
//     (compiler/row_lineage.go:655, cell_trace.go:54,
//     population_mapping.go:38 target.rejectPostgreSQL) — and ir
//     validation (physical_stage_validation.go:417-483) never pairs them
//     with an inline CODED_GROUP terminal; hand-assembled IR is refused.
//   - PreviewSourceWindowByRootID / PreviewTerminalPivotWindow:
//     probe-attested production-unreachable — the source-identity window
//     requires a terminal Pivot group key (ir/physical_stage_sequence.go
//     :16-20) and the Pivot-window proof (ir/physical_stage_validation.go
//     :278-284) restricts to Pivot terminals; production lowering sets
//     only the sequence-level field. BOTH levels are refused here — the
//     plan-level PreviewSourceWindowByRootID (ir/physical_types.go:27)
//     too, because this door dispatches before the spine's plan-level
//     gate (render.go:237) and is the only place the check can fire for
//     a coded-group plan, mirroring the graph door (graph.go:228-231).
//     Hand-assembled IR is refused fail-closed.
//   - OutputAuthResourcePathBindKey: the typed composite Combine prefix
//     surface (ir/physical_stage_sequence.go:25-28, physical_combine.go);
//     ClickHouse prefix/combine plans are already refused by the spine
//     gates this slice inherits. Refused.
//   - Every bind/payload defect the ir validator catches (non-empty
//     string binds, collection == resource type, occurrence == "base",
//     coding path == segments, SourceIdentityColumn == "_key",
//     SourceRowsUnique, safe distinct variables, typed key/count/identity
//     columns, hidden stable identity): enforced by plan.Validate()
//     (ir/physical_stage_validation.go:2251-2360) and re-read here at the
//     door BEFORE any rendering, so both doors refuse the same shapes
//     with the same typed error.
//   - Path-segment names outside the renderer-safe pattern: unreachable —
//     lowering's character gate (lower/recipe_construction_coded_group.go
//     :195-215) and the ir segment check (ir/physical_stage_validation.go
//     :2283-2287) admit only [A-Za-z_][A-Za-z0-9_]* names — re-checked per
//     segment before documentPathFromExpression accepts them, closing the
//     same door null_semantics.go:118-133 enforces.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// codedGroupRowIDColumn is the compiler-owned stable row identity column
// the sequence terminal sorts on (stage_sequence.go:399-403).
const codedGroupRowIDColumn = "__loom_row_id"

// codedGroupMissingKeyTag is the canonical AQL ASSERT tag
// (construction_coded_group.go:85) raised verbatim inside a 22P02 driver
// message through population.go's guardCast barrier.
const codedGroupMissingKeyTag = "CONSTRUCTION_CODED_GROUP_MISSING_KEY"

// codedGroupTrimCharacters is the ONE trim set of this package's
// blank-string validity gates, matching ArangoDB's space-only AQL TRIM at
// construction_coded_group.go:77-78/:101. It crosses the SQL boundary as a
// single bound text parameter and is referenced by every gate in this file
// through this constant; a duplicated trim-set literal anywhere else in
// the package is a structural test failure (coded_group_test.go).
const codedGroupTrimCharacters = " "

// codedGroupPlanMentionsStage reports whether a plan's stage sequence
// mentions the CODED_GROUP surface at all. RenderPhysicalPlan consults it
// once, before plan validation, so a coded-group plan never enters a spine
// arm (the whole-plan delegation door; graph.go:185-193 parity).
func codedGroupPlanMentionsStage(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStageCodedGroupOp {
			return true
		}
	}
	return false
}

// renderCodedGroupPhysicalPlan is the single coded-group door behind both
// entry points (the RenderPhysicalPlan hook and the direct door tests
// call). Gate order mirrors RenderPhysicalPlan for the gates that apply
// plan-wide, with validateCodedGroupPlanSurface as the first plan-facing
// act so both doors refuse the same shapes with the same typed error.
func renderCodedGroupPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateCodedGroupPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderCodedGroupStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// validateCodedGroupPlanSurface is the single closed-slice contract behind
// both doors. It is total over any plan mentioning a CODED_GROUP stage and
// refuses every shape outside the registered slice with
// ErrUnsupportedPostgresPlan before plan validation, scope loading, or any
// rendering can reject with an off-type error.
func validateCodedGroupPlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: coded-group dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap",
			ErrUnsupportedPostgresPlan)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageCodedGroupOp || stage.CodedGroup == nil {
		return fmt.Errorf("%w: operation %q is not part of the registered coded-group slice",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if stage.Group != nil || stage.CohortGroup != nil || stage.Filter != nil || stage.Expand != nil ||
		stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: CODED_GROUP requires only a coded-group payload (ir/physical_stage_validation.go:187 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage CODED_GROUP (InputStageID must equal %q); the DOCUMENT re-query shape is production-unreachable (recipe/construction.go:665-668)",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		// The plan-level PreviewSourceWindowByRootID (ir/physical_types.go
		// :27) must be refused HERE: the RenderPhysicalPlan hook dispatches
		// a coded-group plan before the spine's plan-level window gate
		// (render.go:237), so this door is the only place that check can
		// fire — the twin graph door keeps the same check
		// (graph.go:228-231). plan.Validate refuses the plan-level flag
		// only under whole-relation evidence
		// (ir/whole_relation_scope_evidence.go:159), so a hand-assembled
		// plan carrying it would otherwise render with the root-id window
		// state silently ignored.
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window require Pivot terminals and are not registered for CODED_GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for CODED_GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: coded-group preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	if err := validateCodedGroupPayload(stage, plan.BindVars); err != nil {
		return err
	}
	return validateCodedGroupOperations(plan)
}

// validateCodedGroupPayload re-reads the ir contract at the door so both
// doors refuse the same bind/payload defects with the same typed error
// before plan.Validate can speak.
func validateCodedGroupPayload(stage ir.PhysicalConstructionStage, bindVars map[string]any) error {
	coded := stage.CodedGroup
	for _, key := range []string{coded.RootCollectionBindKey, coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey} {
		if value, ok := bindVars[key].(string); !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: coded-group bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, key)
		}
	}
	if collection, _ := bindVars[coded.RootCollectionBindKey].(string); collection != coded.ResourceType || strings.TrimSpace(coded.ResourceType) == "" {
		return fmt.Errorf("%w: root collection bind does not match the exact root resource type", ErrUnsupportedPostgresPlan)
	}
	if occurrence, _ := bindVars[coded.OccurrenceIDBindKey].(string); occurrence != "base" {
		return fmt.Errorf("%w: the registered inline slice admits only the base root occurrence (ir/physical_stage_validation.go:2274)",
			ErrUnsupportedPostgresPlan)
	}
	if codingPath, _ := bindVars[coded.CodingPathBindKey].(string); codingPath != coded.CodingPath {
		return fmt.Errorf("%w: the Coding path bind differs from the compiled path", ErrUnsupportedPostgresPlan)
	}
	if !coded.SourceRowsUnique || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("%w: CODED_GROUP requires the compiler proof of one source row per root _key (ir/physical_stage_validation.go:2270-2274)",
			ErrUnsupportedPostgresPlan)
	}
	if err := validateCodedGroupRowValues(coded); err != nil {
		return err
	}
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyExclude, ir.PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("%w: missing-key policy %q is unsupported", ErrUnsupportedPostgresPlan, string(coded.MissingKeyPolicy))
	}
	if len(coded.PathSegments) == 0 || !coded.PathSegments[len(coded.PathSegments)-1].Repeated {
		return fmt.Errorf("%w: the Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	var canonical strings.Builder
	for index, segment := range coded.PathSegments {
		if !payloadPathSegmentPattern.MatchString(segment.Name) {
			return fmt.Errorf("%w: coded-group path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment.Name)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	if canonical.String() != coded.CodingPath {
		return fmt.Errorf("%w: the Coding path must be the canonical join of its segments", ErrUnsupportedPostgresPlan)
	}
	for _, variable := range []string{coded.SystemVariable, coded.VersionVariable, coded.CodeVariable, coded.CountVariable, coded.IdentityVariable, coded.GroupRowsVariable} {
		if variable == "" {
			return fmt.Errorf("%w: coded-group plan variables must be compiled and non-empty", ErrUnsupportedPostgresPlan)
		}
	}
	return nil
}

// codedGroupSanitizeRowValueProjections clears Hidden on the row-value
// projection clones for CLASSIFICATION only: the lowering stamps them
// hidden (physical_stage_validation.go:2346-2396) because they must never
// surface as RETURN projections — the arm honors that by rendering them
// exclusively as private pool columns. The clone's expression fields are
// untouched; the copy never flows back into the plan.
func codedGroupSanitizeRowValueProjections(projections []ir.PhysicalProjection) []ir.PhysicalProjection {
	out := make([]ir.PhysicalProjection, len(projections))
	for i, projection := range projections {
		projection.Hidden = false
		out[i] = projection
	}
	return out
}

// validateCodedGroupRowValues is the pg-sql-13 (Q4) door for the
// coded-group row-values arm. The inline ALL/ONE shape renders through
// the shared group emitter (groupRowValueExpressionFor); the door keeps
// both doors refusing the same defects before plan.Validate can speak:
// unique inputs (AQL construction_coded_group.go:92-94 duplicate
// refusal), one source projection per input and none extra, extraction
// scalar shapes only, the registered policy/kind sets (row_values.go:53
// parity), and the many-cardinality (local-array-expansion) input —
// whose pool CTE renders from g_rows — stays a named wave-2 gap.
func validateCodedGroupRowValues(coded *ir.PhysicalStageCodedGroup) error {
	if len(coded.RowValues) == 0 && len(coded.RowValueProjections) == 0 {
		return nil
	}
	inputs := make(map[string]bool, len(coded.RowValues))
	for _, rowValue := range coded.RowValues {
		if rowValue.InputColumn == "" || rowValue.Output == "" || rowValue.Variable == "" {
			return fmt.Errorf("%w: coded-group row value columns and variables must be compiled and non-empty",
				ErrUnsupportedPostgresPlan)
		}
		if inputs[rowValue.InputColumn] {
			return fmt.Errorf("%w: coded-group row value input column %q is declared twice (construction_coded_group.go:92-94 parity)",
				ErrUnsupportedPostgresPlan, rowValue.InputColumn)
		}
		inputs[rowValue.InputColumn] = true
		switch rowValue.Policy {
		case "ALL", "ONE":
		default:
			return fmt.Errorf("%w: row value %q has unsupported policy %q (row_values.go:53 parity)",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.Policy)
		}
		switch rowValue.InputKind {
		case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		default:
			return fmt.Errorf("%w: row value %q has unsupported input kind %q",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.InputKind)
		}
		if rowValue.InputMany {
			return fmt.Errorf("%w: coded-group many-cardinality (local-array-expansion) row values are a wave-2 pg-sql-13 gap (the shared pool emitter groupManyRowValuePoolFor renders from g_rows; a CODED_GROUP pool over cg_tuples is wave-2) and are refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		}
	}
	provided := make(map[string]int, len(coded.RowValueProjections))
	classified, err := classifyExtractionProjections(codedGroupSanitizeRowValueProjections(coded.RowValueProjections), func(string) (string, bool) {
		// The door has no renderer scope; the row IS the exact root
		// resource type (validated above against the root_collection
		// bind), so a selector provenance naming anything else fails.
		return coded.ResourceType, true
	})
	if err != nil {
		return err
	}
	for _, projection := range classified {
		provided[projection.projection.Name]++
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression, extractionSelector:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			if projection.kind != extractionSelector && len(value.Path) == 0 {
				return fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar projection (whole-document read)",
					ErrUnsupportedPostgresPlan, projection.projection.Name)
			}
		default:
			return fmt.Errorf("%w: coded-group row value projection %q is not a direct payload extraction (only the source-clone scalar shapes — direct value, value expression, selector — are registered, group-arm parity)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
	}
	for name := range inputs {
		if provided[name] != 1 {
			return fmt.Errorf("%w: coded-group row value input %q needs exactly one source projection (found %d)",
				ErrUnsupportedPostgresPlan, name, provided[name])
		}
	}
	for name := range provided {
		if !inputs[name] {
			return fmt.Errorf("%w: coded-group row value projection %q addresses no row value input column",
				ErrUnsupportedPostgresPlan, name)
		}
	}
	return nil
}

// validateCodedGroupOperations pins the source region to the shapes the
// spine arms render: one leading direct ROOT_SCAN, FILTER / canonical
// DERIVED_LET operations before the window (SET is refused outright,
// L-19.4), at most one SORT and one
// LIMIT (the merged spine pairing, W1), and one terminal RETURN.
// Everything outside is a registered refusal (see the file-header register
// for the reachability citations).
func validateCodedGroupOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: the coded-group source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the plain spine refuses them",
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
		case ir.PhysicalSetOp:
			// Registered named divergence L-19.4: production lowering
			// DOES mint SET in the coded-group source region (probe:
			// real recipe→semantic→lower, setOps=1) and AQL renders it
			// inline (probe: AQL control err=nil). This arm is the SOLE
			// load-bearing gate: the statement renderer
			// (renderCodedGroupStatement) has no PhysicalSetOp dispatch
			// arm, so without this refusal the OPTIONAL plan renders
			// with the SET SILENTLY DROPPED — byte-for-byte the
			// SET-less base SQL (verifier-145 sabotage at 85e6b47b) —
			// and REQUIRED refuses at its required-match FILTER at
			// traversal.go:415, never at sets.go:236. The
			// traversal.go:141 and argument-integrity-guard citations
			// are probe-era legs (SET still dispatched to renderSet
			// then), kept as evidence that wiring the dispatch would
			// not render the shape in any configuration either. No
			// configuration renders it, so the whitelist arm is
			// refused fail-closed here; see the file-header register
			// for the full chain. Evidence:
			// logs/pg19-f3-set-reachability.log.
			return fmt.Errorf("%w: coded-group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; probe-attested L-19.4) and is refused, never partially rendered",
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
				return fmt.Errorf("%w: the coded-group source requires a terminal RETURN with its projection list (lower/recipe_construction_coded_group.go:180-184)",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
		default:
			return fmt.Errorf("%w: operation %q is not part of the registered coded-group source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return fmt.Errorf("%w: the coded-group source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return nil
}

// renderCodedGroupStatement renders the whole plan. The source region
// reuses the spine arms verbatim, then the grouping CTEs and the final
// identity-ordered projection are assembled here. Aliases and CTE names
// are renderer-minted; every plan value crosses as a $n parameter.
func (r *planRenderer) renderCodedGroupStatement(plan ir.PhysicalPlan) (string, error) {
	coded := plan.StageSequence.Stages[0].CodedGroup

	// --- source region: the spine's own arms, untouched. ---
	if err := r.renderRootScan(plan, *plan.Operations[0].RootScan); err != nil {
		return "", err
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return "", err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered coded-group source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSortOp:
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", err
			}
		case ir.PhysicalLimitOp:
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", err
			}
		}
	}

	// --- pg-sql-13 (Q4) row-value source columns. ---
	// The row-value inputs ride the record scope as payload extractions
	// rendered through the SAME arms materializeGroupSource uses
	// (classification + resolveValue), then travel the candidate/tuple
	// chain so the shared group emitter aggregates them per tuple. The
	// whole arm is additive: with zero row values every string below is
	// empty and the statement is byte-identical to the attested base.
	type codedGroupRowValue struct {
		rowValue ir.PhysicalStageRowValue
		column   *groupColumn
	}
	var codedRowValues []codedGroupRowValue
	var rvSuffix, rvCarryS, rvCarryC string
	if len(coded.RowValues) > 0 {
		classified, err := classifyExtractionProjections(codedGroupSanitizeRowValueProjections(coded.RowValueProjections), func(variable string) (string, bool) {
			scope, ok := r.rows[variable]
			if !ok {
				return "", false
			}
			if scope.resourceType != "" {
				return scope.resourceType, true
			}
			// The coded gate scans the row as the exact root resource
			// type (door-validated against the root_collection bind); a
			// selector provenance naming anything else is a mismatch.
			return coded.ResourceType, true
		})
		if err != nil {
			return "", err
		}
		byName := map[string]classifiedExtractionProjection{}
		for _, projection := range classified {
			byName[projection.projection.Name] = projection
		}
		for i := range coded.RowValues {
			rowValue := coded.RowValues[i]
			projection, ok := byName[rowValue.InputColumn]
			if !ok {
				return "", fmt.Errorf("%w: coded-group row value input %q has no source projection",
					ErrUnsupportedPostgresPlan, rowValue.InputColumn)
			}
			var resolved string
			switch projection.kind {
			case extractionSelector:
				// The lowered source clone of a SELECT field arrives as
				// the selector arm (group source parity): the private
				// pool column renders through the same jsonb-preserving
				// scalar extract over the record payload.
				extracted, err := r.scalarExtractValueExpression(r.rootAlias, projection.path, projection.expression.NullBehavior)
				if err != nil {
					return "", fmt.Errorf("coded-group row value %q: %w", rowValue.Output, err)
				}
				resolved = extracted
			case extractionDirectValue, extractionValueExpression:
				value := projection.projection.Value
				if projection.kind == extractionValueExpression {
					value = *projection.projection.Expression.Value
				}
				if len(value.Path) == 0 {
					return "", fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar projection (whole-document read)",
						ErrUnsupportedPostgresPlan, rowValue.InputColumn)
				}
				renderedValue, err := r.resolveValue(value)
				if err != nil {
					return "", fmt.Errorf("coded-group row value %q: %w", rowValue.Output, err)
				}
				resolved = renderedValue
			default:
				return "", fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar arm",
					ErrUnsupportedPostgresPlan, rowValue.InputColumn)
			}
			if isPayloadExpression(resolved) {
				resolved = "(" + resolved + ")"
			}
			alias := "cg_rv" + strconv.Itoa(i)
			codedRowValues = append(codedRowValues, codedGroupRowValue{
				rowValue: rowValue,
				column:   &groupColumn{name: rowValue.InputColumn, alias: alias, expr: resolved, ref: `"` + alias + `"`, jsonb: true},
			})
			rvSuffix += ", " + resolved + ` AS ` + alias
			rvCarryS += ", s." + alias
			rvCarryC += ", c." + alias
		}
	}

	resource := r.storage.Resource
	source := "SELECT " + r.rootAlias + "." + resource.MustColumn("logical_id").SQLText() +
		` AS cg_root_key, ` + r.rootAlias + "." + resource.MustColumn("payload").SQLText() + ` AS cg_payload` + rvSuffix +
		" FROM " + resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		source += " " + join
	}
	for _, lateral := range r.laterals {
		source += " " + lateral
	}
	if len(r.where) > 0 {
		source += " WHERE " + strings.Join(r.where, " AND ")
	}
	source += " ORDER BY " + strings.Join(r.windowOrder(), ", ") + r.window.limitClause

	// --- coding expansion: guarded LEFT JOIN LATERAL chain. ---
	lateralSQL, itemExpression, err := r.codedGroupExpansion(coded.PathSegments)
	if err != nil {
		return "", err
	}

	systemKey := r.textParameter("system", "text:system")
	versionKey := r.textParameter("version", "text:version")
	codeKey := r.textParameter("code", "text:code")
	trimChars := r.textParameter(codedGroupTrimCharacters, "text:"+codedGroupTrimCharacters)
	dollar := func(index int) string { return "$" + strconv.Itoa(index) }

	// Key reads over the candidate item (cg_candidates scope) and over the
	// deduplicated candidate column (cg_tuples scope); both bind the same
	// text parameters.
	itemSystemText := itemExpression + " ->> " + dollar(systemKey) + "::text"
	itemCodeText := itemExpression + " ->> " + dollar(codeKey) + "::text"

	// The validity predicate mirrors the AQL valid LET exactly
	// (construction_coded_group.go:77): object, string-and-non-blank
	// system, string-and-non-blank code. Version is NOT part of validity
	// — an invalid/blank version only nulls the version key, never the
	// tuple.
	valid := "COALESCE(jsonb_typeof(" + itemExpression + ") = 'object'" +
		" AND jsonb_typeof(" + itemExpression + " -> " + dollar(systemKey) + "::text) = 'string'" +
		" AND btrim(" + itemSystemText + ", " + dollar(trimChars) + "::text) <> ''" +
		" AND jsonb_typeof(" + itemExpression + " -> " + dollar(codeKey) + "::text) = 'string'" +
		" AND btrim(" + itemCodeText + ", " + dollar(trimChars) + "::text) <> '', false)"

	candidates := "SELECT s.cg_root_key" + rvCarryS + ", " + itemExpression + " AS cg_item, " + valid + " AS cg_valid" +
		" FROM cg_src AS s" + lateralSQL

	// --- per-root tuple dedup + policy conjunct. ---
	tupleSystemText := "c.cg_item ->> " + dollar(systemKey) + "::text"
	tupleCodeText := "c.cg_item ->> " + dollar(codeKey) + "::text"
	tupleVersionText := "c.cg_item ->> " + dollar(versionKey) + "::text"

	var tuplesConjuncts []string
	// Row-value member type witnesses at the tuple position — the pool
	// forms here (one DISTINCT root per tuple), matching the AQL
	// renderConstructionRowValueLets ASSERT position; null-exempt,
	// raising the canonical VALUE tag through the barrier cast. The
	// witness/dedup law matches the group stage: equal texts bind equal
	// parameters, so a column addressed twice conjuncts once.
	seenTuplesConjunct := map[string]bool{}
	for i := range codedRowValues {
		_, witness, err := r.groupColumnRead(codedRowValues[i].column, codedRowValues[i].rowValue.InputKind,
			groupValueTypeTag, dollar)
		if err != nil {
			return "", fmt.Errorf("coded-group row value %q: %w", codedRowValues[i].rowValue.Output, err)
		}
		if witness != "" && !seenTuplesConjunct[witness] {
			seenTuplesConjunct[witness] = true
			tuplesConjuncts = append(tuplesConjuncts, witness)
		}
	}
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyExclude:
		tuplesConjuncts = append(tuplesConjuncts, "c.cg_valid")
	case ir.PhysicalStageGroupMissingKeyError:
		tuplesConjuncts = append(tuplesConjuncts,
			"(CASE WHEN c.cg_valid THEN 1 ELSE "+guardCast(codedGroupMissingKeyTag)+" END) > 0")
	}
	tuplesWhere := ""
	if len(tuplesConjuncts) > 0 {
		tuplesWhere = " WHERE " + strings.Join(tuplesConjuncts, " AND ")
	}
	tuples := "SELECT DISTINCT c.cg_root_key" + rvCarryC + "," +
		" CASE WHEN c.cg_valid THEN " + tupleSystemText + " END AS cg_system," +
		" CASE WHEN c.cg_valid AND jsonb_typeof(c.cg_item -> " + dollar(versionKey) + "::text) = 'string'" +
		" AND btrim(" + tupleVersionText + ", " + dollar(trimChars) + "::text) <> '' THEN " + tupleVersionText + " END AS cg_version," +
		" CASE WHEN c.cg_valid THEN " + tupleCodeText + " END AS cg_code" +
		" FROM cg_candidates AS c" + tuplesWhere

	// The tuple pool is one DISTINCT root per tuple (cg_tuples), which is
	// exactly the AQL contributing-record set; the shared group emitter
	// (groupRowValueExpressionFor, coordinator ruling 2026-11-11 Q4)
	// aggregates ALL/ONE over it in the same statement position as
	// COUNT(*).
	groupedSelects := "SELECT cg_system, cg_version, cg_code, COUNT(*) AS cg_count"
	for i := range codedRowValues {
		expression, err := groupRowValueExpressionFor(codedRowValues[i].rowValue.Policy,
			codedRowValues[i].rowValue.InputKind, false, codedRowValues[i].column, dollar)
		if err != nil {
			return "", fmt.Errorf("coded-group row value %q: %w", codedRowValues[i].rowValue.Output, err)
		}
		groupedSelects += ", " + expression + " AS cg_g" + strconv.Itoa(i)
	}
	grouped := groupedSelects + " FROM cg_tuples GROUP BY 1, 2, 3"

	// --- stable identity + final positional projection. ---
	constructionID := r.textParameter(bindStringVar(plan.BindVars, coded.ConstructionIDBindKey), "")
	occurrenceID := r.textParameter(bindStringVar(plan.BindVars, coded.OccurrenceIDBindKey), "")
	codingPath := r.textParameter(bindStringVar(plan.BindVars, coded.CodingPathBindKey), "")
	// The identity text must be byte-identical to the AQL ground truth
	// TO_STRING(["construction", @constructionID, "operation",
	// "CODED_GROUP", "occurrence", @occurrenceID, "path", @codingPath,
	// "system", system, "version", version, "code", code])
	// (construction_coded_group.go:127-129): every JSON string member is
	// double-quoted, exactly as the unpivot.go identity precedent emits
	// quoteSQLLiteral(`"TABLE_UNPIVOT"`).
	identity := "'[' || " +
		quoteSQLLiteral(`"construction"`) + " || ',' || to_jsonb(" + dollar(constructionID) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"operation"`) + " || ',' || " + quoteSQLLiteral(`"CODED_GROUP"`) +
		" || ',' || " + quoteSQLLiteral(`"occurrence"`) + " || ',' || to_jsonb(" + dollar(occurrenceID) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"path"`) + " || ',' || to_jsonb(" + dollar(codingPath) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"system"`) + " || ',' || COALESCE(to_jsonb(g.cg_system)::text, 'null')" +
		" || ',' || " + quoteSQLLiteral(`"version"`) + " || ',' || COALESCE(to_jsonb(g.cg_version)::text, 'null')" +
		" || ',' || " + quoteSQLLiteral(`"code"`) + " || ',' || COALESCE(to_jsonb(g.cg_code)::text, 'null')" +
		" || ']'"

	expressions := map[string]string{
		coded.SystemOutputColumn:  "g.cg_system",
		coded.VersionOutputColumn: "g.cg_version",
		coded.CodeOutputColumn:    "g.cg_code",
		coded.CountOutputColumn:   "g.cg_count",
		codedGroupRowIDColumn:     identity,
	}
	for i := range codedRowValues {
		expressions[codedRowValues[i].rowValue.Output] = "g.cg_g" + strconv.Itoa(i)
	}
	selects := make([]string, 0, len(plan.StageSequence.FinalColumns))
	names := make([]string, 0, len(plan.StageSequence.FinalColumns))
	for _, column := range plan.StageSequence.FinalColumns {
		expression, ok := expressions[column.Name]
		if !ok {
			return "", fmt.Errorf("%w: coded-group final column %q is not a registered tuple member",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		// Stage columns are addressed by Name (matching the ir projection
		// validator and the AQL renderer); the recipe-side ID is metadata
		// the renderer never dereferences. A duplicate Name would address
		// one tuple member twice and is refused, not guessed.
		for _, seen := range names {
			if seen == column.Name {
				return "", fmt.Errorf("%w: coded-group final column %q is declared twice",
					ErrUnsupportedPostgresPlan, column.Name)
			}
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, column.Name)
	}
	if len(names) != 5+len(codedRowValues) {
		return "", fmt.Errorf("%w: the registered slice emits the three tuple keys, the root count, the hidden stable identity, and one column per registered row value",
			ErrUnsupportedPostgresPlan)
	}
	r.outputColumns = names

	final := "SELECT " + strings.Join(selects, ", ") + " FROM cg_grouped AS g" +
		" ORDER BY " + identity + ` COLLATE "C" ASC`
	if plan.StageSequence.PreviewLimitBindKey != "" {
		limit := plan.BindVars[plan.StageSequence.PreviewLimitBindKey].(int)
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}

	sql := "WITH cg_src AS (" + source +
		"), cg_candidates AS (" + candidates +
		"), cg_tuples AS (" + tuples +
		"), cg_grouped AS (" + grouped + ") " + final
	return sql, nil
}

// codedGroupExpansion renders the guarded jsonb expansion over the source
// payload: consecutive scalar segments collapse into one #> text[]
// extraction through the package's single path-binding authority. The
// AQL ground truth nests one FOR loop per repeated segment inside ONE
// subquery that prunes emptied branches, and substitutes the null
// candidate exactly once per root, only when the whole expansion is empty
// (LENGTH(raw) == 0 ? [null] : raw, construction_coded_group.go:54-75).
// The SQL reproduces that exactly: two or more repeated segments mint ONE
// LEFT JOIN LATERAL around
// (SELECT cgxN.elem FROM jsonb_array_elements(guard1) AS cgx1(elem)
// CROSS JOIN LATERAL ... AS cgxN(elem)) AS cgexp(elem) ON true — CROSS
// JOIN LATERAL prunes an emptied inner branch with its outer row, and the
// single wrapping LEFT JOIN contributes exactly one SQL-NULL-item
// candidate only when the entire chain yields zero rows. A per-level
// LEFT JOIN would instead materialize a NULL candidate for every emptied
// branch (a root whose first component carries codings while a second
// has an empty coding array would gain a spurious invalid candidate). A
// single repeated segment keeps the flat LEFT JOIN LATERAL
// jsonb_array_elements(guard) AS cgx1(elem) ON true form, whose zero-row
// behavior already equals the chain. It returns the lateral clauses (in
// chain order, appended after the cg_src row) and the jsonb expression of
// the final item.
func (r *planRenderer) codedGroupExpansion(segments []ir.PhysicalCodedGroupPathSegment) (string, string, error) {
	current := "s.cg_payload"
	var expansions []string
	var lastAlias string
	var pending []string
	sequence := 0
	for _, segment := range segments {
		// Every segment — repeated or not — is a member access first,
		// mirroring the AQL path walk's attribute step
		// (construction_coded_group.go:57: `%s[%q]` per segment). A
		// repeated segment then contributes the array expansion.
		pending = append(pending, segment.Name)
		if !segment.Repeated {
			continue
		}
		extraction, err := r.documentPathFromExpression(current, pending)
		if err != nil {
			return "", "", err
		}
		current = extraction
		pending = nil
		sequence++
		alias := "cgx" + strconv.Itoa(sequence)
		guard := "CASE WHEN jsonb_typeof(" + current + ") = 'array' THEN " + current + " ELSE '[]'::jsonb END"
		expansions = append(expansions, "jsonb_array_elements("+guard+") AS "+alias+"(elem)")
		lastAlias = alias
		current = alias + ".elem"
	}
	if len(pending) != 0 || sequence == 0 {
		// Unreachable past validateCodedGroupPayload (last segment is
		// repeated); the fail-closed refusal stays.
		return "", "", fmt.Errorf("%w: the Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	if sequence == 1 {
		// Flat form: a lone guarded expansion already contributes
		// exactly one NULL row when empty (the AQL zero-item rule).
		return " LEFT JOIN LATERAL " + expansions[0] + " ON true", "cgx1.elem", nil
	}
	// One LEFT JOIN LATERAL around the CROSS JOIN LATERAL chain so the
	// AQL per-root zero-item rule fires once, not once per branch.
	chain := " LEFT JOIN LATERAL (SELECT " + lastAlias + ".elem FROM " +
		strings.Join(expansions, " CROSS JOIN LATERAL ") + ") AS cgexp(elem) ON true"
	return chain, "cgexp.elem", nil
}

// bindStringVar re-reads a door-validated non-empty string bind.
func bindStringVar(bindVars map[string]any, key string) string {
	value, _ := bindVars[key].(string)
	return value
}
