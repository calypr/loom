package postgres

// The GROUP stage (pg-sql-13) is registered here as its own closed plan
// slice: one direct root scan with the canonical scope block and the
// spine's own renderable source operations, one authored GROUP construction
// stage consuming that source inline, and the typed-key grouping with the
// seven registered reducers rendered as one WITH statement. Dispatch is the
// one additive hook in RenderPhysicalPlan (the structural twin of the
// graph.go and coded_group.go doors): any plan whose stage sequence
// mentions a GROUP stage is rendered WHOLE here, so no spine arm ever sees
// a construction stage and the StageSequence refusal stays byte-identical
// for every other sequence shape. validateGroupPlanSurface is the single
// contract definition behind both doors (unnest.go / unpivot.go / graph.go
// / coded_group.go precedent).
//
// AQL ground truth is render/aql/construction_group.go
// renderConstructionGroupStage (keys :110-144, COLLECT :286-297,
// aggregates renderConstructionGroupAggregate :783-821, identity
// :113,143,339-340, key-sort + preview stage_sequence.go:449-453) with
// row values from render/aql/row_values.go. The registered mapping:
//
//   - Source region: the spine arms render g_src UNTOUCHED (loadScope,
//     renderRootScan, acceptCanonicalScopeFilter/renderFilter owning
//     rejectOptionalRowReferences, acceptCanonicalAuthLet,
//     renderWindowSort/renderWindowLimit W1). Unlike coded_group.go, the
//     source RETURN is LOAD-BEARING here: its projections are materialized
//     positionally as c0..cN (renderer-minted aliases; no plan column name
//     reaches SQL text), and the name->ordinal map is the counterpart of
//     AQL's inputRow[@construction_group_key_column_i] member reads
//     (construction_group.go:114-116 over the InputProjections the lowering
//     stamps, lower/recipe_construction.go:1186-1190,2386-2396). The map
//     is built at render time from the RETURN list by NAME, so a swapped
//     fixture order re-reads by name, never by position accident (pinned
//     live: the group_test.go order-swap cell).
//   - Keys (§3.2): the typed read of the mapped source column. A jsonb
//     column reads NULLIF(col,'null'::jsonb) first, so a stored JSON null
//     and a missing attribute both read SQL NULL at the key exactly as
//     AQL's inputRow[@col] reads both as null; the tri-state stays
//     preserved upstream in the extraction/presence contract (registered
//     D-13f). The per-key type witness
//     (CASE WHEN COALESCE(x IS NULL OR jsonb_typeof(x) = @type, false)
//     THEN 1 ELSE guardCast(CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH) END) > 0
//     sits at the g_rows row position (AQL asserts post-COLLECT, :133
//     emitted :236/:274/:298 — identical decision set, registered timing
//     D-13d). Typed storage columns need no witness (the storage type IS
//     the witness; the AQL assert is vacuous there — agreement by
//     construction). GROUP BY over the typed read treats NULL keys as one
//     group exactly like AQL COLLECT. INTEGER/DECIMAL group in the numeric
//     domain so 2 and 2.0 share a group, matching Arango's single number
//     space (registered D-13e).
//   - Missing-key policies (:124-132, all three arms + fail-closed
//     default): GROUP keeps null keys; EXCLUDE conjuncts value IS NOT NULL
//     per key; ERROR conjuncts the CASE non-null THEN 1 ELSE
//     guardCast("CONSTRUCTION_GROUP_MISSING_KEY") END > 0 raise. All three
//     sit in the g_rows WHERE — the AQL pre-COLLECT FILTER position — and
//     a zero-source query never raises in either backend.
//   - Reducers (§3.3, one emitter, the table below): the value pool is the
//     non-null, witness-passed typed read (AQL :796-800 filters
//     member.present — always true in this slice, no related merge — and
//     != null, then type-asserts CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH).
//     The empty-group split is live-pinned per reducer:
//     COUNT_ROWS/COUNT_NON_NULL/COUNT_DISTINCT read 0,
//     SUM/MIN/MAX/MEAN read NULL. SUM and MEAN accumulate EXACTLY in
//     numeric per the coordinator ruling 2026-11-11 (Q1, F-2 repair) in
//     its F-6 final form (2026-11-11: float8 SUM overflow raises 22003
//     on PG18.6 — isolated repro sum(1.7e308 + 1.7e308); a post-hoc
//     finiteness gate can never decide, because every MEMBER is finite
//     while the running SUM overflows): members are each magnitude-capped
//     by the calls.go F-8 clamp so the numeric addition itself can never
//     leave the storable domain (PG18 raises 22003 on a numeric RESULT
//     overflow even between storable operands), and the exact sum is
//     bounded to double by the group band cast (groupDoubleBandCast, the
//     calls.go band discipline on the same band constants): magnitudes at
//     or beyond the overflow band read 0, magnitudes below the subnormal
//     floor read the registered 0 — never a raising arm.
//     Overflow-band reads 0 matching live ArangoDB 3.12.4-3 native SUM over storable members; oracle probe archived by verifier; coordinator ruling 2026-11-11.
//     D-13h, ORACLE-ATTESTED PARITY.
//     bounded double sum by the pool count over all non-null members
//     (overflow-band groups read 0/n = 0, matching the native SUM/LENGTH
//     render path — not the AVG function's interpreted-mode reading; the
//     denominator counts beyond-bound members like LENGTH) — the quotient
//     cannot overflow (|sum| < DBL_MAX, count >= 1) and PG18 flushes an
//     underflowing double quotient to 0.
//     The beyond-cap class (|member| > 1e400) is un-ingestable in Arango
//     (VPack out of range — no divergence exists to register); PG reads
//     0 there too, matching the closest representable AQL reading — the
//     same D-13h parity registration.
//     MIN/MAX/COUNT_DISTINCT and the
//     row-value sets compute in the numeric/text/bool domain where jsonb
//     preserves decimal text; oracle agreement with Arango's double-domain
//     compare is claimed for double-exactly-representable fixture values
//     (registered D-13a bound). Text comparisons pin Arango's byte-order
//     total compare with COLLATE "C" (registered D-13b, ASCII-pinned live
//     like coded_group D-19a).
//   - Row values (§3.4, ALL/ONE, render/aql/row_values.go): ALL renders
//     ARRAY_AGG(DISTINCT member ORDER BY member <COLLATE "C">) over the
//     group's non-null witness-passed members, emitted as jsonb, empty ->
//     []; ONE renders the COUNT(DISTINCT) <= 1 assert through
//     guardCast("CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") with MIN as the
//     first-or-null value (SORTED_UNIQUE/FIRST picks the same value under
//     the same sort — registered D-13g). InputMany flattens one jsonb
//     array layer per row inside a correlated DISTINCT subselect; the
//     non-array member raises the canonical
//     CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH at the g_rows row position
//     (row_values.go:33,80 parity). These emitters are SHARED with the
//     coded-group slice's row-value arm (coordinator ruling 2026-11-11,
//     Q4): both slices bind the same functions and a drift between halves
//     is a sabotage-test failure (group_test.go emitter-identity cell).
//   - Keyless grand total (§3.5): admitted only for the gate-legal
//     COUNT_ROWS-only combination (AQL :44-52). The no-GROUP-BY single-row
//     aggregate over zero rows reproduces the AQL [null]-padding reading
//     value-for-value: COUNT_ROWS 0, reducers NULL, ALL [], ONE null.
//   - Stable identity (§3.6): the AQL identity
//     TO_STRING([["construction",@cid],["operation","GROUP"],
//     [@columnID_i,keyVar]...]) (construction_group.go:113,143,339-340)
//     renders as the renderer-composed JSON-compact concatenation
//     (unpivot.go/coded_group.go precedent): constant labels as
//     quoteSQLLiteral constants, the construction identity through
//     to_jsonb($n::text), per key '["'||to_jsonb(@colID)||'",'
//     ||COALESCE(to_jsonb(keyval)::text,'null')||']'. Every member is a
//     string or null, so to_jsonb's quoting is injective over the tuple.
//     Arango's TO_STRING rendering of exotic control characters stays
//     QUARANTINED (unpivot.go:73-78 bound); byte-exactness is claimed only
//     against independent literal expected rows with deterministic ASCII
//     identities.
//   - Final order and bound: the stage sorts the key variables ASC
//     (:135->:238/:274/:299) and the sequence terminal SKIPS the identity
//     sort for a GROUP last stage (stage_sequence.go:449-450), emitting
//     LIMIT @PreviewLimitBindKey over those rows (:453). Arango's ASC
//     lattice puts null before every value, so key terms carry NULLS
//     FIRST (null-vs-value key positions stay in the AQL order). Key
//     ties are AQL-unspecified; the registered final order
//     ORDER BY <key_i [COLLATE "C"] ASC NULLS FIRST>...,
//     <identity> COLLATE "C" ASC [LIMIT $n] is a total, locale-free
//     refinement — REGISTERED DIVERGENCE: PG total-order refinement on
//     AQL-unspecified sort ties per coordinator ruling 2026-11-11
//     (D-13c). The preview limit binds as one bigint $n; a non-int or
//     non-positive bind is a typed refusal.
//   - Coding key (coordinator ruling 2026-11-11, Q2: in-ticket wave 2 for
//     the LOCAL-array-expansion source): WAVE 1 refuses every CodingKey
//     (validateGroupPayload, never partially rendered). Wave 2 registers
//     the local-array-expansion form: the membership column is one g_src
//     jsonb column (the lowering projects the expansion item's Coding
//     object); the tuple keys read system/code verbatim and version only
//     when a non-blank string, with the ONE blank-gate constant
//     groupTrimCharacters (this file's own single-source trim set; the
//     package trim-set law name-exempts this file for the btrim-text leg
//     while the codedGroupTrimCharacters constant-use check keeps applying
//     to every line here). Validity, key, and policy semantics reuse the
//     AQL codingIdentityTupleExpression/missing-code gate
//     (construction_group.go:634-661) exactly as coded_group.go does.
//     Route-sourced CodingKeys stay the L-13.4 named gap.
//
// Refusal register (probe receipts: artifacts/loom-experiment-132/logs/
// pg13-reachability.log; L-numbers per the ratified plan §6):
//
//   - L-13.1 multi-stage / non-first-stage / foreign stage kind: probe-
//     attested reachable as TWO stages (EXPAND-then-GROUP,
//     lower/recipe_construction_reshape_test.go:40-88) — registered
//     accepted gap naming pg-sql-16.
//   - L-13.2 source-region SET: registered named divergence naming
//     pg-sql-16, sole-load-bearing-gate chain identical to coded_group.go
//     L-19.4 (no dispatch arm exists; removal silently drops the SET).
//   - L-13.3 SemanticKey: CLOSED for the inline door by pg-sql-16 G2
//     (stage_semantic.go: membership walk, matcher, per-row dedup,
//     zero-match fallback, tuple identity, code policy — AQL
//     :100-181,:663-746 twin). String-comparator honesty (D-13b law): the
//     display MIN and tuple COLLATE "C" terms claim parity only on the
//     ASCII-letters class, pinned by the banked ArangoDB 3.12.4-3 receipts
//     o2b-compare.wire ("a"<"b", "A"<"a", case-strict ==) and
//     o2b-sort.wire; the receipts also pin the divergence class (Arango
//     sorts "_" and "~" BEFORE letters): codes/displays with symbols or
//     mixed case are out-of-parity, no cross-engine order asserted, no
//     fixture depends on one. The door still REFUSES an UNRESOLVED family
//     (blank binding/rule/family/system) with the historical registered-gap
//     wording — that preserved refusal now guards the malformed-input path,
//     not an open gap: resolution happens upstream at
//     recipe/construction_reshape.go:68 (Bundle.Validate, "semanticCategory
//     is not a resolved BINDING"), lower/recipe_construction_reshape.go:
//     71-105 (BINDING kind + resource + FHIR-schema-proven Coding path),
//     and ir/physical_stage_validation.go:1478-1489 (exact resolved family
//     identity), the last enforced by plan.Validate at every render entry
//     (render.go, group.go) after this door's own re-read
//     (validateGroupSemanticKey precedes it). Nothing in the production
//     pipeline can mint an unresolved family: a plan that reaches the
//     renderer unresolved is reachable-by-construction only as
//     hand-carried IR, the pre-approved surface of plan §5 G2. Composition
//     stays refused at the stage_sequence.go validateStageKindPayload
//     GROUP-arm gate.
//   - L-13.4 route-sourced CodingKey (RelatedSource/SourceUnnest):
//     registered named divergence naming pg-sql-16.
//   - L-13.5 RelatedOccurrence: CLOSED for the composed door by pg-sql-16
//     G3(c) (stage_related_occurrence.go; entry routes adapter-mentioning
//     sequences to the composition slice, the dispatch arm consumes every
//     adapter column). The single-stage INLINE door keeps this
//     defense-in-depth re-read for direct callers — repo-test-attested
//     single-stage reachable shape (TestRelatedGroupFieldSourcesShareOneExactRouteAdapter);
//     sets.go:82-95 hop-only finding cited in the adapter header.
//   - L-13.6 RootContributorInputColumn: probe-attested unreachable
//     through the PG compile target (lineage surfaces refuse at their own
//     doors: compiler/row_lineage.go:655, cell_trace.go:54,
//     population_mapping.go:38; the inline-group probe never emits it —
//     rootContributor="" in every receipt cell); hand-assembled IR
//     refused.
//   - Population/cohort/candidate-seed roots: population.go gate (19 twin).
//   - Foreign operations / non-canonical DERIVED_LET / diagnostic returns
//     / preview windows / OutputAuthResourcePathBindKey / unknown policy,
//     reducer, or row-value policy strings: the coded_group.go twins with
//     the same citations; fail-closed, never partially rendered.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// groupRowIDColumn is the hidden stable identity column the stage emits
// (lower stamps constructionGeneratedRowIdentitySchema).
const groupRowIDColumn = "__loom_row_id"

// Canonical AQL assertion tags raised verbatim through guardCast.
const (
	groupMissingKeyTag       = "CONSTRUCTION_GROUP_MISSING_KEY"
	groupKeyTypeTag          = "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH"
	groupValueTypeTag        = "CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH"
	groupRowValueMultipleTag = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"
)

// groupTrimCharacters is the ONE trim set of THIS slice's blank-string
// validity gates (coding-key version/code normalization at the AQL parity
// of construction_coded_group.go:77-78/:101-102 and
// construction_group.go:643-648). Like codedGroupTrimCharacters it crosses
// the SQL boundary as one bound text parameter and is referenced only
// through this constant; the package trim-set law (coded_group_test.go)
// name-exempts this file for the btrim-text leg while the
// codedGroupTrimCharacters constant-use check keeps applying to every line
// here — binding the coded-group constant from this file stays fatal.
const groupTrimCharacters = " "

// groupPlanMentionsStage reports whether a plan's stage sequence mentions
// the GROUP surface at all. RenderPhysicalPlan consults it once, before
// plan validation, so a group plan never enters a spine arm.
func groupPlanMentionsStage(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStageGroupOp {
			return true
		}
	}
	return false
}

// renderGroupPhysicalPlan is the single group door behind both entry
// points. Gate order mirrors RenderPhysicalPlan; validateGroupPlanSurface
// is the first plan-facing act so both doors refuse identically.
func renderGroupPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGroupPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderGroupStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// validateGroupPlanSurface is the closed-slice contract: total over any
// plan mentioning a GROUP stage, refusing every shape outside the
// registered slice with ErrUnsupportedPostgresPlan before plan validation,
// scope loading, or any rendering can reject with an off-type error.
func validateGroupPlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: group dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: the registered group slice admits exactly one GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap (L-13.1)",
			ErrUnsupportedPostgresPlan)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil {
		return fmt.Errorf("%w: operation %q is not part of the registered group slice",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if stage.CodedGroup != nil || stage.CohortGroup != nil || stage.Filter != nil || stage.Expand != nil ||
		stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: GROUP requires only a group payload (ir/physical_stage_validation.go:187 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage GROUP (InputStageID must equal %q); composed sequences are a pg-sql-16 capability gap (L-13.1)",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window require Pivot terminals and are not registered for GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: group preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	// Operation-shape first: the name-set the payload inputs resolve
	// against comes from the terminal RETURN, so the L-13.11 door re-read
	// can speak before plan.Validate.
	returnNames, err := validateGroupOperations(plan)
	if err != nil {
		return err
	}
	if err := validateGroupPayload(stage, plan.BindVars, returnNames); err != nil {
		return err
	}
	return validateGroupSemanticKey(plan, stage)
}

// validateGroupPayload re-reads the door-bearing contract before
// plan.Validate can speak: named-gap fields per the refusal register,
// policy/reducer/kind enums, and variable presence.
func validateGroupPayload(stage ir.PhysicalConstructionStage, bindVars map[string]any, returnNames map[string]bool) error {
	group := stage.Group
	if value, ok := bindVars[group.ConstructionIDBindKey].(string); !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: group construction identity bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, group.ConstructionIDBindKey)
	}
	switch group.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyExclude, ir.PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("%w: unsupported missing-key policy %q (construction_group.go:131 parity)",
			ErrUnsupportedPostgresPlan, string(group.MissingKeyPolicy))
	}
	// L-13.3 is CLOSED for the inline door (pg-sql-16 G2): the semantic
	// family key renders through the registered membership walk, matcher,
	// per-row dedup, fallback row, tuple identity, and code policy — all in
	// stage_semantic.go, with the door's own contract re-reads in
	// validateGroupSemanticKey. The composed door keeps its separate
	// byte-identical refusal (stage_sequence.go validateStageKindPayload GROUP-arm gate) until the composition
	// carrier lands.
	if group.SemanticKey != nil && group.CodingKey != nil {
		return fmt.Errorf("%w: Group supports at most one coded identity key (ir/physical_stage_validation.go:1475 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	// L-13.5 enabling condition CLOSED by pg-sql-16 G3(c): the composed
	// door dispatches the adapter (stage_sequence.go renderStageGroup,
	// stage_related_occurrence.go), and the RenderPhysicalPlan entry routes
	// every adapter-mentioning sequence there ahead of this door. This
	// re-read stays as the direct-call defense-in-depth refusal — a shape
	// that reaches the inline slice speaks the registered gap voice.
	if group.RelatedOccurrence != nil {
		return fmt.Errorf("%w: the related-occurrence Group adapter is a pg-sql-16 capability gap (L-13.5; correlated-hop child-set contracts) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.RootContributorInputColumn != "" {
		return fmt.Errorf("%w: the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.GroupRowsVariable == "" || group.IdentityVariable == "" {
		return fmt.Errorf("%w: group plan variables must be compiled and non-empty", ErrUnsupportedPostgresPlan)
	}
	for index, key := range group.Keys {
		switch key.Kind {
		case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		default:
			return fmt.Errorf("%w: group key %d has unsupported scalar kind %q",
				ErrUnsupportedPostgresPlan, index, key.Kind)
		}
		if key.InputColumn == "" || key.OutputColumn == "" || key.Variable == "" {
			return fmt.Errorf("%w: group key %d must be fully compiled (input, output, variable)",
				ErrUnsupportedPostgresPlan, index)
		}
		if key.InputInternal || key.FieldSource != nil {
			// The registered slice addresses PUBLIC source projections by
			// name only; the lowering gate admits public scalars and
			// nothing else (recipe_construction_reshape.go:200-207,
			// 245-249), so a hidden/field-sourced input is hand-assembled
			// IR — fail closed (L-13.11).
			return fmt.Errorf("%w: group key %d uses a hidden or field-sourced input surface, which the public-scalar lowering gate never emits (L-13.11)",
				ErrUnsupportedPostgresPlan, index)
		}
		if !returnNames[key.InputColumn] {
			return fmt.Errorf("%w: group key %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, key.InputColumn)
		}
	}
	// A semantic family key is a COLLECT key of its own (the AQL collectKeys
	// tuple, construction_group.go:151), so a group carrying one is never
	// the keyless grand total: the COUNT_ROWS-only gate below is the AQL
	// :44-52 gate for a group with NO keys at all.
	keyless := len(group.Keys) == 0 && group.SemanticKey == nil
	for index, aggregate := range group.Aggregates {
		switch aggregate.Operation {
		case "COUNT_ROWS", "COUNT_NON_NULL", "COUNT_DISTINCT", "MIN", "MAX", "SUM", "MEAN":
		default:
			return fmt.Errorf("%w: aggregate %d: unsupported aggregate operation %q (construction_group.go:807-808 parity)",
				ErrUnsupportedPostgresPlan, index, string(aggregate.Operation))
		}
		if aggregate.Operation == "COUNT_ROWS" {
			if aggregate.InputColumn != "" {
				return fmt.Errorf("%w: aggregate %d COUNT_ROWS must not have an input column", ErrUnsupportedPostgresPlan, index)
			}
		} else if aggregate.InputColumn == "" {
			return fmt.Errorf("%w: aggregate %d %s requires an input column",
				ErrUnsupportedPostgresPlan, index, string(aggregate.Operation))
		}
		if aggregate.Output == "" || aggregate.Variable == "" {
			return fmt.Errorf("%w: aggregate %d must be fully compiled (output, variable)", ErrUnsupportedPostgresPlan, index)
		}
		if aggregate.InputColumn != "" && !returnNames[aggregate.InputColumn] {
			return fmt.Errorf("%w: aggregate %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, aggregate.InputColumn)
		}
	}
	if keyless {
		// The AQL gate (construction_group.go:44-52) admits a keyless
		// Group only as the COUNT_ROWS-only grand total (row values
		// allowed alongside).
		if len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" {
			return fmt.Errorf("%w: only keyless COUNT_ROWS groups can consume the source scope (construction_group.go:44-52 re-read at the door)",
				ErrUnsupportedPostgresPlan)
		}
	}
	for index, rowValue := range group.RowValues {
		switch rowValue.Policy {
		case "ALL", "ONE":
		default:
			return fmt.Errorf("%w: row value %q has unsupported policy %q",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.Policy)
		}
		if rowValue.InputColumn == "" || rowValue.Output == "" || rowValue.Variable == "" {
			return fmt.Errorf("%w: row value %d must be fully compiled (input, output, variable)",
				ErrUnsupportedPostgresPlan, index)
		}
		if rowValue.FieldSource != nil {
			return fmt.Errorf("%w: row value %d uses a field-sourced input surface, which the public-scalar lowering gate never emits (L-13.11)",
				ErrUnsupportedPostgresPlan, index)
		}
		if !returnNames[rowValue.InputColumn] {
			return fmt.Errorf("%w: row value %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, rowValue.InputColumn)
		}
	}
	// CodingKey: registered only with wave 2 of this ticket (coordinator
	// ruling 2026-11-11, Q2: the LOCAL-array-expansion source). Until wave
	// 2 lands, every CodingKey — local or route-sourced — fails closed
	// (route-sourced stays the permanent L-13.4 pg-sql-16 gap; wave 2
	// narrows this arm to route-sourced only).
	if group.CodingKey != nil {
		return fmt.Errorf("%w: the CODING_IDENTITY Group key is refused, never partially rendered (L-13.4: route-sourced keys name pg-sql-16; the local-array-expansion key is wave 2 of this ticket)",
			ErrUnsupportedPostgresPlan)
	}
	return nil
}

// validateGroupOperations pins the source region to the spine-renderable
// shapes (coded_group.go validateCodedGroupOperations twin): one leading
// direct ROOT_SCAN, FILTER / canonical DERIVED_LET before the window (SET
// refused outright, L-13.2), at most one SORT and one LIMIT (W1), and one
// terminal RETURN with its projection list. UNNEST is NOT admitted: the
// spine-registered expansion (pg-sql-09) belongs to the CodingKey source,
// wave 1 refuses every CodingKey at the payload gate, and the emitter has
// no UNNEST arm (verifier 156 advisory N-2, 2026-11-11 — the dead
// lateral-expansion emitter call this gate made unreachable was deleted;
// the spine keeps its own dispatch outside this file, and wave 2
// registers its own arm when the CodingKey slice opens). The L-13.9
// refusal below is the single UNNEST gate.
func validateGroupOperations(plan ir.PhysicalPlan) (map[string]bool, error) {
	if len(plan.Operations) == 0 {
		return nil, fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return nil, fmt.Errorf("%w: the group source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return nil, fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the plain spine refuses them",
			ErrUnsupportedPostgresPlan)
	}
	sawSort, sawLimit, sawReturn := false, false, false
	var returnNames map[string]bool
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return nil, fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			if sawSort || sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return nil, fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return nil, fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalUnnestOp:
			// UNNEST belongs to the CodingKey source (the local-array
			// expansion the lowering stamps upstream, spine contract
			// owned by unnest.go/pg-sql-09). Wave 1 refuses every
			// CodingKey at the payload gate, so no registered slice can
			// carry an expansion; plain groups never see one (probe ops
			// lists, receipt log). L-13.9.
			return nil, fmt.Errorf("%w: operation %q is not part of the registered group source surface (L-13.9)",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalSetOp:
			// Registered named divergence L-13.2 (the coded-group L-19.4
			// twin): the statement renderer has no PhysicalSetOp dispatch
			// arm, so WITHOUT this arm a whitelist-accepted SET renders
			// with the SET SILENTLY DROPPED — byte-for-byte the SET-less
			// SQL. The sets.go:82-95 "no current lowering output reaches
			// the hop-only renderer" finding stays cited; the arm is
			// refused fail-closed and the enabling contracts name
			// pg-sql-16.
			return nil, fmt.Errorf("%w: group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; L-13.2) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalSortOp:
			if sawSort || sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return nil, fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return nil, fmt.Errorf("%w: operation %d: limit payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			if !last || operation.Return == nil || len(operation.Return.Projections) == 0 {
				return nil, fmt.Errorf("%w: the group source requires a terminal RETURN with its projection list",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
			returnNames = map[string]bool{}
			for _, projection := range operation.Return.Projections {
				if projection.Name == groupSemanticCarrierName {
					// The semantic walk's carrier is renderer-minted and
					// never addressable by plan name (stage_semantic.go).
					return nil, fmt.Errorf("%w: source projection name %q is reserved for the renderer's semantic carrier column",
						ErrUnsupportedPostgresPlan, projection.Name)
				}
				returnNames[projection.Name] = true
			}
		default:
			return nil, fmt.Errorf("%w: operation %q is not part of the registered group source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return nil, fmt.Errorf("%w: the group source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return returnNames, nil
}

// groupColumn is one materialized source-projection column. Name is the
// authored projection name (map key only — never SQL text); alias is the
// renderer-minted "c<i>" column alias. Expr is the rendering used in the
// g_src SELECT list; Ref is the materialized-column reference every
// downstream read consumes. The r0 root alias is visible ONLY inside
// g_src, so downstream regions never re-render the source expression,
// they read the materialized column (the positional c0..cN
// materialization law). Ref is deliberately UNQUALIFIED: each consuming
// region has exactly one relation in its FROM — the g_rows policy WHERE
// over g_src, and the g_grp aggregates over g_rows — so one text is
// legal in both, which also keeps the witness-dedup map single-valued.
type groupColumn struct {
	name  string
	alias string
	expr  string
	ref   string
	jsonb bool
}

// renderGroupStatement renders the whole group plan: g_src (spine arms +
// positional projection materialization), g_rows (row-level witnesses and
// policy conjuncts), g_grp (the grouped aggregate row), and the final
// identity-ordered positional projection.
func (r *planRenderer) renderGroupStatement(plan ir.PhysicalPlan) (string, error) {
	stage := plan.StageSequence.Stages[0]

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
				return "", fmt.Errorf("%w: operation %q is not part of the registered group source surface",
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

	// --- positional projection materialization (name->column map). ---
	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			returnOp = operation.Return
		}
	}
	columns, err := r.materializeGroupSource(plan, returnOp)
	if err != nil {
		return "", err
	}
	if stage.Group.SemanticKey != nil {
		// The AQL walk reads the in-scope source row's payload
		// (aql/stage_sequence.go:314), so the raw Coding projection is
		// materialized here as one hidden g_src column (stage_semantic.go).
		carrier, err := r.groupSemanticCarrierColumn(stage.Group.SemanticKey)
		if err != nil {
			return "", err
		}
		columns = append(columns, *carrier)
	}
	resource := r.storage.Resource
	selectList := make([]string, 0, len(columns))
	for i := range columns {
		selectList = append(selectList, columns[i].expr+` AS "`+columns[i].alias+`"`)
	}
	source := "SELECT " + strings.Join(selectList, ", ") +
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

	region, err := r.renderGroupAggregateRegion(plan, stage, columns, source, "g_",
		plan.StageSequence.FinalColumns, groupRowIDColumn)
	if err != nil {
		return "", err
	}
	r.outputColumns = region.names
	final := region.final + " ORDER BY " + strings.Join(region.order, ", ")
	if plan.StageSequence.PreviewLimitBindKey != "" {
		limit := plan.BindVars[plan.StageSequence.PreviewLimitBindKey].(int)
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}
	return "WITH " + strings.Join(region.ctes, ", ") + " " + final, nil
}

// groupRegionResult is one rendered grouped-aggregate region: the WITH
// members (source, guarded rows, optional InputMany pools, grouped row) in
// dependency order, the positional final SELECT over them, the registered
// ORDER BY terms, and the positional output names. The inline single-stage
// door (renderGroupStatement) and the composed stage builder
// (stage_sequence.go) share this one emitter through
// renderGroupAggregateRegion: the inline door passes the group slice's own
// prefix "g_" plus the spine-built source text and FinalColumns and renders
// byte-identically to the pre-composition emitter (the byte-preservation
// law); composition passes a per-stage prefix and a prior-CTE source text.
type groupRegionResult struct {
	ctes        []string
	final       string
	order       []string
	names       []string
	keyCollates []string
}

// renderGroupAggregateRegion renders the registered grouped-aggregate
// region (the guarded rows, the aggregates and row values, the construction
// identity, and the positional final projection) over a caller-supplied
// source SELECT and relation prefix. Every relation and internal column
// name is prefix-minted by the renderer, never plan data. columns is the
// positional name->column map for the source relation; identityColumn names
// the hidden stable row-identity output.
func (r *planRenderer) renderGroupAggregateRegion(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	columns []groupColumn,
	sourceText string,
	prefix string,
	outputColumns []ir.PhysicalStageColumn,
	identityColumn string,
) (groupRegionResult, error) {
	group := stage.Group
	nameToColumn := map[string]*groupColumn{}
	var semanticCarrier *groupColumn
	for i := range columns {
		if columns[i].name == groupSemanticCarrierName {
			if semanticCarrier != nil {
				return groupRegionResult{}, fmt.Errorf("%w: the semantic carrier column is declared twice",
					ErrUnsupportedPostgresPlan)
			}
			semanticCarrier = &columns[i]
			continue
		}
		nameToColumn[columns[i].name] = &columns[i]
	}

	// --- resolve key/aggregate/row-value inputs through the name map. ---
	keyColumns := make([]*groupColumn, len(group.Keys))
	for i, key := range group.Keys {
		column, err := groupResolveColumn(nameToColumn, key.InputColumn)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		keyColumns[i] = column
	}
	type groupedAggregate struct {
		aggregate ir.PhysicalStageGroupAggregate
		column    *groupColumn
		alias     string
	}
	aggregates := make([]groupedAggregate, 0, len(group.Aggregates))
	for i, aggregate := range group.Aggregates {
		var column *groupColumn
		if aggregate.InputColumn != "" {
			resolved, err := groupResolveColumn(nameToColumn, aggregate.InputColumn)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", aggregate.Output, err)
			}
			column = resolved
		}
		aggregates = append(aggregates, groupedAggregate{
			aggregate: aggregate, column: column, alias: prefix + "a" + strconv.Itoa(i),
		})
	}
	type groupedRowValue struct {
		rowValue ir.PhysicalStageRowValue
		column   *groupColumn
		alias    string
	}
	rowValues := make([]groupedRowValue, 0, len(group.RowValues))
	for i, rowValue := range group.RowValues {
		column, err := groupResolveColumn(nameToColumn, rowValue.InputColumn)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group row value %q: %w", rowValue.Output, err)
		}
		rowValues = append(rowValues, groupedRowValue{
			rowValue: rowValue, column: column, alias: prefix + "rv" + strconv.Itoa(i),
		})
	}

	dollar := func(index int) string { return "$" + strconv.Itoa(index) }

	// --- semantic family key (pg-sql-16 G2, L-13.3 closed). ---
	var semantic *groupSemanticRegion
	if group.SemanticKey != nil {
		region, err := r.renderGroupSemanticRegion(group.SemanticKey, semanticCarrier, stage, prefix, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group semantic key %q: %w", group.SemanticKey.OutputColumn, err)
		}
		semantic = &region
	} else if semanticCarrier != nil {
		return groupRegionResult{}, fmt.Errorf("%w: the semantic carrier column is minted only beside a semantic family key",
			ErrUnsupportedPostgresPlan)
	}

	// --- g_rows: row-level witnesses + missing-key policy conjuncts. ---
	var conjuncts []string
	keyReads := make([]string, len(group.Keys))
	keyCollates := make([]string, len(group.Keys))
	for i, key := range group.Keys {
		typedRead, witness, err := r.groupColumnRead(keyColumns[i], key.Kind, groupKeyTypeTag, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		keyReads[i] = typedRead
		if key.Kind == "STRING" {
			keyCollates[i] = ` COLLATE "C"`
		}
		if witness != "" {
			conjuncts = append(conjuncts, witness)
		}
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyExclude:
			conjuncts = append(conjuncts, typedRead+" IS NOT NULL")
		case ir.PhysicalStageGroupMissingKeyError:
			conjuncts = append(conjuncts,
				"(CASE WHEN "+typedRead+" IS NOT NULL THEN 1 ELSE "+guardCast(groupMissingKeyTag)+" END) > 0")
		}
	}
	for _, item := range aggregates {
		if item.column == nil {
			continue
		}
		_, witness, err := r.groupColumnRead(item.column, item.aggregate.InputKind, groupValueTypeTag, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", item.aggregate.Output, err)
		}
		if witness != "" {
			conjuncts = append(conjuncts, witness)
		}
	}
	for _, item := range rowValues {
		if !item.rowValue.InputMany {
			_, witness, err := r.groupColumnRead(item.column, item.rowValue.InputKind, groupValueTypeTag, dollar)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
			}
			if witness != "" {
				conjuncts = append(conjuncts, witness)
			}
			continue
		}
		// The AQL InputMany ROW position asserts only IS_ARRAY
		// (row_values.go:79-83) — the stored value IS an array, so the
		// scalar typeof witness must not fire on the array column; the
		// per-member typeof assert rides inside the pool CTE instead
		// (row_values.go:33,80). The missing/JSON-null row is the AQL
		// null (excluded before the assert), so only a non-null stored
		// non-array raises here.
		nullRead := groupNullRead(item.column)
		conjuncts = append(conjuncts, "(CASE WHEN COALESCE("+nullRead+
			" IS NULL OR jsonb_typeof("+nullRead+") = 'array', false) THEN 1 ELSE "+
			guardCast(groupValueTypeTag)+" END) > 0")
	}

	// The semantic membership policy rides over membership.code at the same
	// pre-COLLECT row position as the ordinary key policies
	// (construction_group.go:172-180 — FILTER / ASSERT <membership>.code
	// != null).
	if semantic != nil {
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyExclude:
			conjuncts = append(conjuncts, semantic.codeRead+" IS NOT NULL")
		case ir.PhysicalStageGroupMissingKeyError:
			conjuncts = append(conjuncts,
				"(CASE WHEN "+semantic.codeRead+" IS NOT NULL THEN 1 ELSE "+guardCast(groupMissingKeyTag)+" END) > 0")
		}
	}

	// One witness per distinct (read, tag) text: a column addressed by
	// several consumers (key + reducer + row value) renders the identical
	// conjunct once; the text is deterministic because textParameter binds
	// equal values to one parameter index.
	deduped := make([]string, 0, len(conjuncts))
	seenConjunct := map[string]bool{}
	for _, conjunct := range conjuncts {
		if seenConjunct[conjunct] {
			continue
		}
		seenConjunct[conjunct] = true
		deduped = append(deduped, conjunct)
	}
	rows := "SELECT * FROM " + prefix + "src"
	if semantic != nil {
		// The AQL FOR-in-memberships sits before COLLECT, so the
		// membership join multiplies the rows every reducer counts.
		rows += semantic.lateral
	}
	if len(deduped) > 0 {
		rows += " WHERE " + strings.Join(deduped, " AND ")
	}

	// --- g_grp. ---
	grpSelects := make([]string, 0, len(keyReads)+1+len(aggregates)+len(rowValues))
	keyAliases := make([]string, 0, len(keyReads))
	for i := range keyReads {
		alias := prefix + "k" + strconv.Itoa(i)
		grpSelects = append(grpSelects, keyReads[i]+" AS "+alias)
		keyAliases = append(keyAliases, alias)
	}
	if semantic != nil {
		// The membership tuple is the AQL COLLECT key
		// [membership.system, membership.version, membership.code]
		// (construction_group.go:151) and the last sort key before the
		// identity tie-break (:152). Its members are string-or-null, so the
		// element-wise terms carry the registered "C" collation (ASCII-letter
		// parity class per D-16e; symbols and mixed case are the registered
		// out-of-parity class) and Arango's
		// null-before-value lattice (NULLS FIRST, the D-13c registration
		// this slice already applies to every key term).
		for i, read := range semantic.tupleReads {
			alias := semantic.tupleAliases[i]
			keyReads = append(keyReads, read)
			keyCollates = append(keyCollates, ` COLLATE "C"`)
			grpSelects = append(grpSelects, read+" AS "+alias)
			keyAliases = append(keyAliases, alias)
		}
		grpSelects = append(grpSelects, semantic.label+" AS "+prefix+"slb")
	}
	for _, item := range aggregates {
		expression, err := r.groupAggregateExpression(item.aggregate, item.column, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", item.aggregate.Output, err)
		}
		grpSelects = append(grpSelects, expression+" AS "+item.alias)
	}
	var manyCTEs, manyJoins []string
	for i, item := range rowValues {
		if item.rowValue.InputMany {
			name := prefix + "m" + strconv.Itoa(i)
			selectText, singleValueRef, err := r.groupManyRowValuePoolFor(item.rowValue.Policy, item.rowValue.InputKind,
				item.column, keyReads, name, prefix+"rows", dollar)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
			}
			manyCTEs = append(manyCTEs, name+" AS ("+selectText+")")
			grpSelects = append(grpSelects, singleValueRef+" AS "+item.alias)
			conditions := make([]string, 0, len(keyReads))
			for j := range keyReads {
				conditions = append(conditions, keyReads[j]+" IS NOT DISTINCT FROM "+name+".m_k"+strconv.Itoa(j))
			}
			on := "TRUE"
			if len(conditions) > 0 {
				on = strings.Join(conditions, " AND ")
			}
			manyJoins = append(manyJoins, "LEFT JOIN "+name+" ON "+on)
			continue
		}
		expression, err := groupRowValueExpressionFor(item.rowValue.Policy, item.rowValue.InputKind,
			item.rowValue.InputMany, item.column, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
		}
		grpSelects = append(grpSelects, expression+" AS "+item.alias)
	}
	grouped := "SELECT " + strings.Join(grpSelects, ", ") + " FROM " + prefix + "rows"
	if len(manyJoins) > 0 {
		grouped += " " + strings.Join(manyJoins, " ")
	}
	if len(keyAliases) > 0 {
		positions := make([]string, len(keyAliases))
		for i := range positions {
			positions[i] = strconv.Itoa(i + 1)
		}
		grouped += " GROUP BY " + strings.Join(positions, ", ")
	}

	// --- identity. ---
	constructionID := r.textParameter(bindStringVar(plan.BindVars, group.ConstructionIDBindKey), "")
	outputIDs := map[string]string{}
	for _, column := range stage.OutputColumns {
		if _, seen := outputIDs[column.Name]; !seen {
			outputIDs[column.Name] = column.ID
		}
	}
	// AQL ground truth (construction_group.go:113,143,339): the identity
	// is TO_STRING([["construction",@cid],["operation","GROUP"],
	// [@columnID_i,keyVar_i],...]) — an OUTER array of MEMBER ARRAYS
	// (unlike the coded-group flat form). JSON-compact: no spaces, every
	// string member double-quoted through to_jsonb (which also escapes),
	// missing keys the literal null.
	identityParts := []string{
		`'["construction",' || to_jsonb(` + dollar(constructionID) + "::text) || ']'",
		`'["operation","GROUP"]'`,
	}
	appendKeyPart := func(columnName string, alias string) error {
		columnID, ok := outputIDs[columnName]
		if !ok {
			return fmt.Errorf("%w: group key output %q has no stable column ID (construction_group.go:136-142 parity)",
				ErrUnsupportedPostgresPlan, columnName)
		}
		idBind := r.textParameter(columnID, "")
		identityParts = append(identityParts,
			"'[' || to_jsonb("+dollar(idBind)+"::text) || ',' || COALESCE(to_jsonb(g."+alias+")::text, 'null') || ']'")
		return nil
	}
	for i, key := range group.Keys {
		if err := appendKeyPart(key.OutputColumn, prefix+"k"+strconv.Itoa(i)); err != nil {
			return groupRegionResult{}, err
		}
	}
	if semantic != nil {
		// The semantic identity member rides after the ordinary key members,
		// the AQL identityParts order (construction_group.go:170-171).
		identityParts = append(identityParts, semantic.identityPart)
	}
	identity := "'[' || " + strings.Join(identityParts, " || ',' || ") + " || ']'"

	// --- final positional projection over FinalColumns. ---
	expressions := map[string]string{}
	for i, key := range group.Keys {
		expressions[key.OutputColumn] = "g." + prefix + "k" + strconv.Itoa(i)
	}
	for _, item := range aggregates {
		expressions[item.aggregate.Output] = "g." + item.alias
	}
	for _, item := range rowValues {
		expressions[item.rowValue.Output] = "g." + item.alias
	}
	if semantic != nil {
		// The semantic output is the group label: MIN over the group's
		// non-null displays, else the tuple's code.
		expressions[group.SemanticKey.OutputColumn] = "g." + prefix + "slb"
	}
	expressions[identityColumn] = identity
	selects := make([]string, 0, len(outputColumns))
	names := make([]string, 0, len(outputColumns))
	for _, column := range outputColumns {
		expression, ok := expressions[column.Name]
		if !ok {
			return groupRegionResult{}, fmt.Errorf("%w: group final column %q is not a registered key, reducer, row value, or the stable identity",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		for _, seen := range names {
			if seen == column.Name {
				return groupRegionResult{}, fmt.Errorf("%w: group final column %q is declared twice", ErrUnsupportedPostgresPlan, column.Name)
			}
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, column.Name)
	}
	emitted := len(group.Keys) + len(aggregates) + len(rowValues) + 1
	if semantic != nil {
		// The semantic label is one more emitted column, beside the ordinary
		// keys, reducers, row values, and the hidden stable identity.
		emitted++
	}
	if len(names) != emitted {
		return groupRegionResult{}, fmt.Errorf("%w: the registered slice emits every key, reducer, and row value plus the hidden stable identity",
			ErrUnsupportedPostgresPlan)
	}

	final := "SELECT " + strings.Join(selects, ", ") + " FROM " + prefix + "grp AS g"
	// Key terms sort ASC NULLS FIRST: Arango's ASC total order places
	// null before every value (its documented sort lattice), so NULLS
	// FIRST keeps the KEY positions in the AQL order; the identity
	// tie-break below is the registered total-order refinement (D-13c).
	order := make([]string, 0, len(keyAliases)+1)
	for i, alias := range keyAliases {
		order = append(order, "g."+alias+keyCollates[i]+" ASC NULLS FIRST")
	}
	order = append(order, identity+` COLLATE "C" ASC`)

	ctes := []string{
		prefix + "src AS (" + sourceText + ")",
		prefix + "rows AS (" + rows + ")",
	}
	ctes = append(ctes, manyCTEs...)
	ctes = append(ctes, prefix+"grp AS ("+grouped+")")
	return groupRegionResult{ctes: ctes, final: final, order: order, names: names, keyCollates: keyCollates}, nil
}

// materializeGroupSource renders the source RETURN projections through the
// SAME registered projection arms renderReturn dispatches (selectors.go
// classification + spine resolvers) and materializes them positionally.
func (r *planRenderer) materializeGroupSource(plan ir.PhysicalPlan, ret *ir.PhysicalReturn) ([]groupColumn, error) {
	classified, err := classifyExtractionProjections(ret.Projections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return nil, err
	}
	columns := make([]groupColumn, 0, len(classified))
	seen := map[string]bool{}
	for i, projection := range classified {
		if seen[projection.projection.Name] {
			return nil, fmt.Errorf("%w: group source projection name %q is declared twice (L-13.11)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
		seen[projection.projection.Name] = true
		var expression string
		isJSONB := false
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			if len(value.Path) == 0 {
				// The whole row document is a composite, not a registered
				// scalar input (the public-scalar lowering gate never
				// emits one; L-13.11).
				return nil, fmt.Errorf("%w: group source projection %q is not a registered scalar projection (whole-document read)",
					ErrUnsupportedPostgresPlan, projection.projection.Name)
			}
			resolved, err := r.resolveValue(value)
			if err != nil {
				return nil, fmt.Errorf("group source projection %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if len(value.Path) > 0 {
				isJSONB = true
			}
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return nil, fmt.Errorf("group source projection %q: %w", projection.projection.Name, err)
			}
			expression = value
			isJSONB = true
		case extractionPresenceSidecar:
			return nil, fmt.Errorf("%w: group source projection %q is a presence sidecar and cannot be addressed by key, reducer, or row value",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		case extractionCallExpression:
			value, err := r.renderCallProjection(plan, projection.projection)
			if err != nil {
				return nil, err
			}
			expression = value
			isJSONB = true
		case extractionObject, extractionSlice:
			return nil, fmt.Errorf("%w: group source projection %q is a composite and cannot be addressed by key, reducer, or row value (lowering admits public scalars only)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		case extractionUnitNormalized:
			// pg-sql-21 contract receipt: the unit-normalized projection
			// is registered on the full read spine (unit_normalization.go
			// via renderReturn), not in this closed GROUP slice — the
			// same spine-only ruling as CALL, OBJECT, and SLICE
			// (selectors.go:578-583 wording). Without this arm the kind
			// fell through with an empty expression and degraded g_src
			// into an untyped syntax error (review finding
			// PRRT_kwDOS-yV5s62Cc1D).
			return nil, fmt.Errorf("%w: group source projection %q is a unit-normalized extraction; unit normalization belongs to the read spine registration (unit_normalization.go), not the closed GROUP slice",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		default:
			// Catch-all: a future classifier kind must never materialize
			// with an empty expression (the fallthrough the unit arm
			// above was missing).
			return nil, fmt.Errorf("%w: group source projection %q is not a registered projection arm",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
		alias := "c" + strconv.Itoa(i)
		columns = append(columns, groupColumn{
			name: projection.projection.Name, alias: alias, expr: expression,
			ref: `"` + alias + `"`, jsonb: isJSONB,
		})
	}
	return columns, nil
}

func groupResolveColumn(nameToColumn map[string]*groupColumn, name string) (*groupColumn, error) {
	column, ok := nameToColumn[name]
	if !ok {
		return nil, fmt.Errorf("%w: group input column %q is not among the source projection names (L-13.11)",
			ErrUnsupportedPostgresPlan, name)
	}
	return column, nil
}

// groupNullRead reads the column excluding the stored-JSON-null scalar so
// it collapses to SQL NULL (the AQL inputRow[@col] null reading, D-13f).
func groupNullRead(column *groupColumn) string {
	if !column.jsonb {
		return column.ref
	}
	return "NULLIF(" + column.ref + ", 'null'::jsonb)"
}

// groupColumnRead renders (typedRead, rowWitness) for one column at one
// registered scalar kind. The witness is empty for typed storage columns
// (storage type is the witness); for jsonb columns it is the
// COALESCE(typeof, false) gate raising the canonical mismatch tag through
// guardCast — null-exempt exactly like the AQL assertions.
func (r *planRenderer) groupColumnRead(column *groupColumn, kind string, tag string, dollar func(int) string) (string, string, error) {
	nullRead := groupNullRead(column)
	var typeofTag string
	switch kind {
	case "STRING":
		typeofTag = "string"
	case "INTEGER", "DECIMAL":
		typeofTag = "number"
	case "BOOLEAN":
		typeofTag = "boolean"
	default:
		return "", "", fmt.Errorf("%w: group column kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	witness := ""
	if column.jsonb {
		typeBind := r.textParameter(typeofTag, "text:typeof:"+typeofTag)
		witness = "(CASE WHEN COALESCE(" + nullRead + " IS NULL OR jsonb_typeof(" + nullRead + ") = " +
			dollar(typeBind) + "::text, false) THEN 1 ELSE " + guardCast(tag) + " END) > 0"
	}
	switch kind {
	case "STRING":
		if !column.jsonb {
			return column.ref, "", nil
		}
		return "((" + nullRead + ") #>> '{}')", witness, nil
	case "INTEGER", "DECIMAL":
		if !column.jsonb {
			return "(" + column.ref + ")::numeric", "", nil
		}
		return "((" + nullRead + ") #>> '{}')::numeric", witness, nil
	case "BOOLEAN":
		if !column.jsonb {
			return "(" + column.ref + ")::boolean", "", nil
		}
		return "((" + nullRead + ") #>> '{}')::boolean", witness, nil
	}
	return "", "", fmt.Errorf("%w: group column kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
}

// groupAggregateExpression renders one reducer over the group (AQL
// renderConstructionGroupAggregate :783-821 parity; the empty-group split
// is pinned live per reducer).
func (r *planRenderer) groupAggregateExpression(aggregate ir.PhysicalStageGroupAggregate, column *groupColumn, dollar func(int) string) (string, error) {
	if aggregate.Operation == "COUNT_ROWS" {
		return "COUNT(*)", nil
	}
	typedRead, _, err := r.groupColumnRead(column, aggregate.InputKind, groupValueTypeTag, dollar)
	if err != nil {
		return "", err
	}
	collate := ""
	if aggregate.InputKind == "STRING" {
		collate = ` COLLATE "C"`
	}
	switch aggregate.Operation {
	case "COUNT_NON_NULL":
		return "COUNT(" + typedRead + ")", nil
	case "COUNT_DISTINCT":
		return "COUNT(DISTINCT " + typedRead + collate + ")", nil
	case "MIN":
		return "MIN(" + typedRead + collate + ")", nil
	case "MAX":
		return "MAX(" + typedRead + collate + ")", nil
	case "SUM":
		// Exact-numeric accumulation with the group band cast
		// (coordinator ruling 2026-11-11 Q1, F-2 repair, F-6 final form):
		// the previous design summed float8 and gated non-finite sums
		// post-hoc — unreachable, because PG18.6 raises 22003 the moment
		// the running float8 SUM overflows (sum(1.7e308+1.7e308)) while
		// every member is finite and passes the member-level gate. The
		// overflow band reads the registered 0 (oracle-attested parity,
		// D-13h); a member-less group keeps the registered NULL.
		return groupBoundedDoubleSum(column, aggregate.InputKind), nil
	case "MEAN":
		// SUM(pool)/LENGTH(pool) per the native render path: the division
		// runs on the bounded double — an overflowing exact sum already
		// read the registered 0 and 0/n reads 0 (matching the native
		// SUM/LENGTH reading; the AVG function's interpreted-mode reading
		// is not the emitted path). The denominator counts the whole pool
		// (every non-null member, beyond-bound members included, like
		// LENGTH; the consumer-side type witness already refused mistyped
		// members at the g_rows WHERE). Denominator >= 1 whenever a value
		// exists; the zero-value group reads NULL pre-division. The
		// quotient cannot overflow (|sum| < DBL_MAX, count >= 1) and
		// PG18 flushes an underflowing double quotient to 0 without
		// raising.
		sum := groupBoundedDoubleSum(column, aggregate.InputKind)
		poolCount := "COUNT(" + groupNumericRead(column, aggregate.InputKind) + ")"
		return "CASE WHEN " + poolCount + " = 0 THEN NULL ELSE " +
			sum + " / (" + poolCount + ")::double precision END", nil
	}
	return "", fmt.Errorf("%w: aggregate %q is not a registered reducer", ErrUnsupportedPostgresPlan, string(aggregate.Operation))
}

// groupDoubleBandCast bounds one never-raising numeric fragment to
// double on the calls.go band constants (callDoubleOverflowBound /
// callDoubleUnderflowFloor — the pg-sql-10 measurements that keep the
// CAST inside the abs < overflow-bound band, where it cannot raise),
// with the SUM/MEAN overflow-band arm reading the registered 0 rather
// than the calls.go NULL: overflow-band reads 0 matching live ArangoDB
// 3.12.4-3 native SUM over storable members; oracle probe archived by
// verifier; coordinator ruling 2026-11-11 (D-13h, ORACLE-ATTESTED
// PARITY — the F-6 flip over the pre-oracle NULL arm, which had been
// blessed only as a registered divergence). A NULL fragment (the
// member-less group, the registered empty-group SUM/MIN/MAX/MEAN
// reading) keeps the NULL: the flip is arm-specific, not a global
// coalesce.
func groupDoubleBandCast(numericFragment string) string {
	return "(CASE WHEN " + numericFragment + " IS NULL THEN NULL" +
		" WHEN abs(" + numericFragment + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericFragment + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + numericFragment + ")::double precision ELSE 0::double precision END" +
		" ELSE 0::double precision END)"
}

// groupBoundedDoubleSum renders the SUM/MEAN accumulator (verifier F-2
// fix, coordinator relay 2026-11-11; F-6 parity form): the exact numeric
// SUM of F-8-clamped members, bounded to double by the group band cast.
// Exactness matters twice: float8 SUM raises 22003 on cumulative
// overflow (the F-2 repro, live fault-controlled), and PG18 also raises
// when a NUMERIC RESULT leaves the storable domain between storable
// operands (the F-8 probes in calls.go) — so each member's magnitude is
// capped by callBandOperandClamp before the addition. The cap is inert
// for every double-representable member (it sits 10^92 above the band
// boundary), so representable verdicts are the correctly-rounded double
// of the exact sum (2x1e307 -> 2e307 exact; the 17x1e307 live cell
// carries the D-13i registered refinement for the two-bit-increment split between
// this exact-then-round reading and AQL's stepped IEEE SUM — PG's is
// the higher-precision result); any member set
// whose exact sum leaves the double band reads 0 through the overflow
// arm (2x1.7e308 -> 0, 20x1e307 -> 0, SUM and MEAN 0/n), and the beyond-cap
// class (un-ingestable in Arango, VPack out of range — no divergence
// exists to register) reads 0 as the closest representable parity — the
// same D-13h registration the header carries.
func groupBoundedDoubleSum(column *groupColumn, kind string) string {
	return groupDoubleBandCast("SUM(" + callBandOperandClamp(groupNumericRead(column, kind)) + ")")
}

// groupNumericRead renders the numeric-domain member read consumed by the
// float8 band helper (jsonb numbers through the canonical text→numeric
// cast, which cannot raise on jsonb-stored numbers; typed numeric/bigint
// columns through the numeric cast).
func groupNumericRead(column *groupColumn, kind string) string {
	if kind == "INTEGER" || kind == "DECIMAL" {
		if !column.jsonb {
			return "(" + column.ref + ")::numeric"
		}
		return "((" + groupNullRead(column) + ") #>> '{}')::numeric"
	}
	return ""
}

// groupRowValueMemberRead returns the typed member rendering over a
// source text (jsonb payload members through the NULLIF + #>> canonical
// read; storage-typed columns through the direct cast). Shared by the
// inline and many-pool halves of the emitter so both read identically.
func groupRowValueMemberRead(column *groupColumn, kind string) func(source string) string {
	return func(source string) string {
		nullRead := source
		if column.jsonb {
			nullRead = "NULLIF(" + source + ", 'null'::jsonb)"
		}
		switch kind {
		case "STRING":
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')"
			}
			return "(" + nullRead + ")"
		case "INTEGER", "DECIMAL":
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')::numeric"
			}
			return "(" + nullRead + ")::numeric"
		default:
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')::boolean"
			}
			return "(" + nullRead + ")::boolean"
		}
	}
}

// groupRowValueCollateAndArrayType pins the "C"-collated text sort (the
// Arango byte-order compare) and the kind-matched empty-array type.
func groupRowValueCollateAndArrayType(kind string) (collate, arrayType string, ok bool) {
	switch kind {
	case "STRING":
		return ` COLLATE "C"`, "text[]", true
	case "INTEGER", "DECIMAL":
		return "", "numeric[]", true
	case "BOOLEAN":
		return "", "boolean[]", true
	}
	return "", "", false
}

// groupManyRowValuePoolFor renders the InputMany (local-array-expansion)
// pool as its own CTE: one jsonb array layer flattened PER ROW through a
// LATERAL jsonb_array_elements, then grouped by the same key reads as
// g_grp. It must be a separate relation because the per-row expansion
// multiplies rows — folding it into g_grp's aggregate context would
// inflate COUNT_ROWS/COUNT_NON_NULL (and PG rejects the lateral
// reference there: 42803, measured). The AQL pool null-filters members
// BEFORE SORTED_UNIQUE (row_values.go:33,45 — FILTER row_value != null
// then FILTER item != null), so a stored JSON null (or a missing value,
// already SQL NULL via groupNullRead) never enters the set for either
// policy. A group with zero pool members is absent from the CTE; the
// LEFT JOIN then reads ALL the registered '[]' and ONE NULL, matching
// the AQL empty-pool readings. Same emitter family as
// groupRowValueExpressionFor (coordinator ruling 2026-11-11, Q4 —
// mechanical dispatch only, shared member read, a drift is a
// sabotage-test failure).
func (r *planRenderer) groupManyRowValuePoolFor(policy, kind string, column *groupColumn, keyReads []string, cteName string, rowsRelation string, dollar func(int) string) (selectText, singleValueRef string, err error) {
	switch policy {
	case "ALL", "ONE":
	default:
		return "", "", fmt.Errorf("%w: row value policy %q is unsupported", ErrUnsupportedPostgresPlan, policy)
	}
	collate, arrayType, ok := groupRowValueCollateAndArrayType(kind)
	if !ok {
		return "", "", fmt.Errorf("%w: row value input kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	if !column.jsonb {
		return "", "", fmt.Errorf("%w: a many-cardinality row value requires a jsonb source column", ErrUnsupportedPostgresPlan)
	}
	member := groupRowValueMemberRead(column, kind)
	memberRead := member("(_rv.item)")
	selects := make([]string, 0, len(keyReads)+1)
	for i, keyRead := range keyReads {
		selects = append(selects, keyRead+" AS m_k"+strconv.Itoa(i))
	}
	if policy == "ALL" {
		distinct := memberRead + collate
		selects = append(selects, "to_jsonb(COALESCE(ARRAY_AGG(DISTINCT "+distinct+" ORDER BY "+distinct+
			"), '{}'::"+arrayType+")) AS m_pool")
	} else {
		selects = append(selects, "(CASE WHEN COUNT(DISTINCT "+memberRead+collate+") <= 1 THEN MIN("+memberRead+collate+
			") ELSE "+groupRowValueRaiseArm(kind)+" END) AS m_pool")
	}
	// Per-member typeof assert at the AQL pool position (row_values.go:
	// 33,80 — after the null filter): null-exempt like the AQL ASSERT,
	// raising the canonical VALUE tag through the barrier cast.
	typeofTag := map[string]string{"STRING": "string", "INTEGER": "number", "DECIMAL": "number", "BOOLEAN": "boolean"}[kind]
	itemNull := "NULLIF((_rv.item), 'null'::jsonb)"
	typeBind := r.textParameter(typeofTag, "text:typeof:"+typeofTag)
	// The WHERE carries NO typed read: PG gives no left-to-right
	// guarantee between conjuncts, so the member's ::numeric/::boolean
	// cast inside the old null filter could evaluate before the typeof
	// guard and raise a raw 22P02 without the registered tag (verifier
	// F-2026-11-11 F-3). The null filter rides at the jsonb level
	// (NULLIF — identical pool for every kind: the #>>/cast of a
	// non-null jsonb member is never NULL, and a mistyped member raises
	// only through the guard), matching the STRING pool's already-safe
	// cast-free shape; the aggregates over memberRead then see only
	// guard-passed rows, where the cast cannot raise.
	selectText = "SELECT " + strings.Join(selects, ", ") +
		" FROM " + rowsRelation + ", LATERAL jsonb_array_elements(" + groupNullRead(column) + ") _rv(item)" +
		" WHERE " + itemNull + " IS NOT NULL AND (CASE WHEN COALESCE(" + itemNull +
		" IS NULL OR jsonb_typeof(" + itemNull + ") = " + dollar(typeBind) + "::text, false) THEN 1 ELSE " +
		guardCast(groupValueTypeTag) + " END) > 0"
	if len(keyReads) > 0 {
		positions := make([]string, len(keyReads))
		for i := range positions {
			positions[i] = strconv.Itoa(i + 1)
		}
		selectText += " GROUP BY " + strings.Join(positions, ", ")
	}
	// The pool CTE is GROUP BY'd on the same key reads, so it holds AT
	// MOST one row per group: the LEFT JOIN is a functional dependency PG
	// cannot infer through a CTE (42803, measured), and MAX over the
	// single joined value (or the all-NULL zero-pool group) reads exactly
	// that value — the registered empty-pool readings are preserved.
	// jsonb has no max aggregate (42883, measured): the ALL pool extracts
	// through jsonb's canonical text round-trip; ONE aggregates its
	// typed pool directly.
	if policy == "ALL" {
		return selectText, "MAX(" + cteName + ".m_pool::text)::jsonb", nil
	}
	return selectText, "MAX(" + cteName + ".m_pool)", nil
}

// groupRowValueExpressionFor is the single inline ALL/ONE emitter shared
// by the GROUP stage and the coded-group slice (AQL render/aql/row_values.go
// parity; coordinator ruling 2026-11-11, Q4 — mechanical dispatch only,
// zero semantic edits per slice; a drift between the halves is a
// sabotage-test failure). Many-cardinality inputs ride
// groupManyRowValuePoolFor instead.
func groupRowValueExpressionFor(policy, kind string, many bool, column *groupColumn, dollar func(int) string) (string, error) {
	switch policy {
	case "ALL", "ONE":
	default:
		return "", fmt.Errorf("%w: row value policy %q is unsupported", ErrUnsupportedPostgresPlan, policy)
	}
	if many {
		return "", fmt.Errorf("%w: many-cardinality row values render through groupManyRowValuePoolFor", ErrUnsupportedPostgresPlan)
	}
	collate, arrayType, ok := groupRowValueCollateAndArrayType(kind)
	if !ok {
		return "", fmt.Errorf("%w: row value input kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	member := groupRowValueMemberRead(column, kind)
	read := member(column.ref)
	if policy == "ALL" {
		// array_agg KEEPS NULL members (unlike the other aggregates), so
		// the AQL null-filter rides as an explicit FILTER (row_values.go:38,45).
		// The COLLATE rides INSIDE the DISTINCT argument: PG requires every
		// ORDER BY expression to appear in the argument list verbatim, and
		// `x COLLATE "C"` ≠ `x` as an expression (measured 42P10).
		distinct := read + collate
		return "to_jsonb(COALESCE(ARRAY_AGG(DISTINCT " + distinct + " ORDER BY " + distinct +
			") FILTER (WHERE " + read + " IS NOT NULL), '{}'" + "::" + arrayType + "))", nil
	}
	return "(CASE WHEN COUNT(DISTINCT " + read + collate + ") <= 1 THEN MIN(" + read + collate +
		") ELSE " + groupRowValueRaiseArm(kind) + " END)", nil
}

// groupRowValueRaiseArm is the ONE-policy ELSE arm: the barrier cast must
// RAISE (an uncast text arm would read as a value), so it stays the
// integer cast that triggers 22P02 — but a CASE branch of integer against
// a text/boolean success branch cannot be matched by PG (measured 42804),
// so the raise is re-cast into the success branch's type; the outer cast
// never executes because the inner integer cast raises first.
func groupRowValueRaiseArm(kind string) string {
	guard := guardCast(groupRowValueMultipleTag)
	switch kind {
	case "STRING":
		return "CAST(" + guard + " AS text)"
	case "BOOLEAN":
		return "CAST(" + guard + " AS boolean)"
	}
	return guard
}
