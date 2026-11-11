package postgres

// pivot.go registers the grouped-Pivot surface on the PostgreSQL render
// contract (pg-sql-14). Two closed shapes share one cell/identity core:
//
//   - The spine GROUPED_PIVOT operation (table-reshape pivot): one scoped
//     direct root scan (canonical scope filters and the canonical
//     AUTH_RESOURCE_PATH_ALLOWED let consumed through the untouched spine
//     arms), the registered pivot, and the reshaped execution window
//     (SORT over {OutputRowVariable, ["__loom_row_id"]} then LIMIT, the ir
//     reshaped-window guard re-read) rendered WHOLE — the flat assemble()
//     cannot express GROUP BY, so the slice owns its WITH/SELECT assembly
//     exactly as coded_group.go does. No spine arm, no phase machine, and
//     no window.go state changes; the default-arm refusal stays
//     byte-identical for every other plan.
//   - The inline single-stage CODED_PIVOT construction stage (coded
//     categories): the whole-plan door after the coded-group door, keyed
//     by stage payload presence (CodedCorrelation != nil), rendering the
//     AQL coded-value chain (owner expansion, FLATTENed codings, the
//     (system, code) match on one Coding item, the INVALID_CHOICE_ARM
//     assert on matched owners, and the FIRST-over-candidates value
//     fallback chain) as guarded jsonb LATERAL relations.
//
// AQL ground truth (ported branch-for-branch, diff receipts in the tests):
// render/aql/table_reshape.go renderGroupedTablePivot (:28-336) and
// renderCodedPivotCategoryValues (:338-389), with the selector loops of
// render/aql/selectors.go renderSelectorArrayFromSource (:317-349),
// extractFinalExpr (selector.go:42-51), and the correlation renderers of
// render/aql/predicates.go:120-207. Frozen category facts are VALUES:
// every category value, kind label, system/code pair, construction ID,
// column-name read, document path, needle, and trim set crosses SQL only
// as an ordered $n parameter (ir/physical_operations.go:548-550,
// "categories remain frozen values and never become query fragments").
// Tri-state cell semantics (ABSENT / PRESENT-JSON-NULL / PRESENT) follow
// this package's null_semantics.go contract table; the ordinary-vs-coded
// missing-condition asymmetry (table_reshape.go:230 raw-count versus
// :267 typed-count) is preserved verbatim. Coding.version is NOT consulted
// for matching — the pivot ground truth never reads it
// (renderCorrelationVersionClause is predicate-only) — so version-varied
// codings are duplicate observations (coordinator ruling R2, AQL parity
// governs; hand-carried VersionSelector/VersionBindKey and
// ValueScope/SelectionScope render byte-identically, goldens C11/C12).
//
// Registered mappings (unit- and live-pinned):
//   - Guards: one CASE chain in the AQL evaluation order (unlisted, then
//     per category: choice-arm (owner-loop ASSERT), missing, then
//     cardinality-or-type-mismatch; categories in declared order) so
//     competing guards pick the same winner the AQL lazy-LET order picks
//     (table_reshape.go:384 forces each category's owner-loop ASSERT
//     with its values LET, ahead of that category's cell guards);
//     SQLSTATE 22P02 tags through population.go's guardCast barrier.
//   - Identity: the renderer-composed JSON-compact concatenation is the
//     byte counterpart of TO_STRING(["GROUPED_PIVOT", @constructionID,
//     ["<Kind>", kv], …]) over ALL group keys including hidden ones
//     (render/aql/table_shape_exclusion.go:137-150). Non-ASCII and exotic
//     TO_STRING renderings stay QUARANTINED under the same bound as
//     unpivot.go's identity; fixtures use deterministic ASCII identities.
//   - Final order: with a plan SORT the rows order by the identity under
//     COLLATE "C" (the AQL reshaped-window row-identity sort, the D-19a
//     ASCII-agreement bound); without one, groups order by per-key Arango
//     type rank (null < bool < number < string) then within-type value,
//     with the identity last as a determinism tie-break the AQL SORT of
//     group-key variables leaves unspecified.
//   - OneInputRowPerGroup renders a uniform GROUP BY either way (identical
//     multiset; the flag is an AQL COLLECT-skip optimization).
//   - Presence reading without a contract follows the null_semantics
//     behavior table: PRESERVE_NULL/EMPTY_ON_NULL render the projected
//     property always-present (their value form cannot separate absent
//     from stored null — exactly where the contract's presence bit is
//     load-bearing); OMIT_NULLS reports present exactly when the read is
//     not SQL NULL (its null-omission is its absence). The producer keeps
//     the presence contract for authored NULL/MISSING keys
//     (lower/recipe_table_reshape.go:136-163), and the live legs pin the
//     contract and companion forms.
//   - A repeated TERMINAL selector step expands into item rows directly,
//     equal to the FLATTEN one level the AQL applies over the
//     array-per-root selector result (identical multiset).
//   - Reducers evaluate in IEEE double, matching Arango's JSON-number
//     semantics (coordinator ruling R4, same ruling as pg-sql-13 Q1).
//
// Refusal register (each line probe-attested production-unreachable with a
// receipt under artifacts/loom-experiment-151/logs/, an ir re-read
// citation, or a registered named gap naming its enabling ticket):
//   - S1 payload-nil / second reshape / pivot-after-reshape / SORT/LIMIT
//     shape: ir/physical_validation.go:91-133 re-reads (the unpivot.go
//     :195-286 spelling).
//   - S2 coded fields on the spine op: the Shape-A producer census
//     (lower/recipe_table_reshape.go) plus ir/physical_table_shape.go:36-38.
//   - S3 RowValues / RootContributor* on the spine op: construction-only
//     producers (lower/recipe_construction.go:1290-1308); row values are
//     the pg-sql-13 capability.
//   - S4 pivot coexisting with a non-canonical FILTER, TRAVERSAL, UNNEST,
//     SET, or EXPRESSION_LET, or with a stage sequence: renders-at-AQL,
//     typed-refused here, pg-sql-16 (probe P-14.2).
//   - S5 population/cohort/candidate-seed roots: the population.go
//     citation (validatePivotRootScan).
//   - S6 RETURN projections must be direct single-name reads of the pivot
//     output over the registered name set.
//   - C1-C2 stage payload exclusivity and single-first-stage identity:
//     ir/physical_stage_validation.go:99-108 re-reads (pg-sql-16 for
//     every other sequence shape).
//   - C3 single-stage non-coded construction PIVOT: never enters this
//     door (CodedCorrelation == nil keeps the merged StageSequence refusal
//     byte-identical); pg-sql-16.
//   - C4 coded RowValues: accepted at recipe level, refused fail-closed,
//     pg-sql-13 (producer lower/recipe_construction_coded_pivot.go:80-91).
//   - C5 coded RootContributor*: the sole producer never writes them.
//   - C6 SET / EXPRESSION_LET in the coded source region: probe-attested
//     production-unreachable (construction outputs reject legacy shape
//     operations at recipe validation, probe P-14.3: "construction cannot
//     be combined with legacy tableReshape or derivedColumns"); the
//     whitelist arms stay fail-closed, pg-sql-16 — without them the
//     statement builder has no dispatch arm and the shape would be
//     silently dropped (the L-19.4 sabotage lesson).
//   - C7 diagnostic returns: refused at their own compile doors
//     (compiler/row_lineage.go:655, cell_trace.go:54,
//     population_mapping.go:38); pivot terminals belong to
//     pg-sql-23/24/25/26.
//   - C8 preview windows: PreviewTerminalPivotWindow and the plan-level
//     PreviewSourceWindowByRootID refuse (this door dispatches before the
//     spine's plan-level gate at render.go, the graph/coded_group
//     argument); the sequence-level root-id fast-path flag is
//     probe-attested stamped for production direct-root coded pivots
//     (lower/recipe_construction.go:476-478; probe P-14.4) and is inert
//     without an execution preview limit (render/aql/stage_sequence.go:28
//     derives the windowing state from flag AND bind) — accepted inert,
//     and the active combination (flag AND preview limit) is the
//     registered named divergence naming pg-sql-16 (coordinator ruling
//     R3: never a silent render).
//   - C9 OutputAuthResourcePathBindKey: ir/physical_stage_sequence.go:25-28.
//   - C10 ExtensionURLSelectors/ExtensionURLBindKeys: LowerCorrelatedBinding
//     never fills them (lower/correlated_binding.go:690-706; probe P-14.5).
//   - C11/C12 hand-carried version fields or value/selection scopes:
//     AQL-parity rendering (see the header); production-unreachable by
//     census and probe P-14.5 (empty scopes, nil version).
//   - C13 bind/payload defects: ir re-reads at the door
//     (ir/physical_table_shape.go:13-94, 237-301) so both doors refuse alike.
//   - C14 selector steps are re-checked against the frozen path contract
//     before any jsonb read, and every segment/needle crosses SQL only as
//     a parameter through null_semantics.go's path-binding authority.
//
// Planner-facing command registration stays fail-closed; this entry point —
// not command dispatch — is where the operation is implemented and
// registered (predicates.go:97, unpivot.go:113-116 precedent).

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	spec "github.com/calypr/loom/internal/fhir/schema"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// Pivot guard tags — the canonical AQL ASSERT tags raised verbatim inside
// 22P02 driver messages through population.go's guardCast barrier
// (render/aql/table_reshape.go :210/:215/:224/:228/:252/:255/:307/:311 and
// the INVALID_CHOICE_ARM assert inside renderCodedPivotCategoryValues).
const (
	pivotValueTypeTag   = "TABLE_PIVOT_VALUE_TYPE_MISMATCH"
	pivotCardinalityTag = "TABLE_PIVOT_CELL_CARDINALITY"
	pivotCellMissingTag = "TABLE_PIVOT_CELL_MISSING"
	pivotUnlistedTag    = "TABLE_PIVOT_UNLISTED_CATEGORY"
	pivotChoiceArmTag   = "INVALID_CHOICE_ARM"
	pivotRowIDColumn    = "__loom_row_id"
	pivotIdentityKind   = "GROUPED_PIVOT"
)

// pivotValueTypeOf maps the frozen table-scalar kinds to jsonb_typeof
// names, mirroring aqlTableScalarType (render/aql/table_reshape.go:693-704)
// read through this package's jsonb representation.
var pivotValueTypeOf = map[string]string{
	"STRING":  "string",
	"INTEGER": "number",
	"DECIMAL": "number",
	"BOOLEAN": "boolean",
}

var pivotDuplicatePolicies = map[string]bool{"ERROR": true, "SUM": true, "MIN": true, "MAX": true}
var pivotMissingPolicies = map[string]bool{"NULL": true, "ERROR": true}

// RenderGroupedPivot renders exactly one scoped direct root scan reshaped
// by one registered GROUPED_PIVOT onto the trusted PostgreSQL storage
// binding (the RenderUnpivot front-door shape, unpivot.go:182-187).
func RenderGroupedPivot(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateGroupedPivotPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return renderGroupedPivotPhysicalPlan(plan, storage)
}

// RenderGroupedCodedPivot renders the inline single-stage CODED_PIVOT
// construction sequence (the coded_group.go:367-405 door shape behind both
// entry points).
func RenderGroupedCodedPivot(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	return renderGroupedCodedPivotPhysicalPlan(plan, storage)
}

// groupedPivotStagePlanMentionsSurface reports whether a plan's stage
// sequence mentions the CODED_PIVOT surface: a PIVOT stage carrying the
// coded payload. A single-stage NON-coded construction PIVOT (no
// CodedCorrelation) never enters this door — the merged StageSequence
// refusal at render.go stays byte-identical for that shape (C3).
func groupedPivotStagePlanMentionsSurface(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStagePivotOp && stage.GroupedPivot != nil && stage.GroupedPivot.CodedCorrelation != nil {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Shared closed-registry helpers
// ---------------------------------------------------------------------------

// pivotDollar renders one $n placeholder.
func pivotDollar(index int) string { return "$" + strconv.Itoa(index) }

// pivotAQLNotNull renders the jsonb reading of the AQL predicate `x != null`
// (the null_semantics tri-state contract: a missing property reads SQL NULL
// and a stored JSON null reads the 'null' scalar; both are AQL null).
func pivotAQLNotNull(expr string) string {
	return "(" + expr + " IS NOT NULL AND " + expr + " <> 'null'::jsonb)"
}

// pivotScalarText renders the text of a JSON scalar member (the AQL scalar
// reading used for exact string equality and IEEE-double conversion).
func pivotScalarText(expr string) string {
	return "(" + expr + " #>> '{}')"
}

// pivotSelectorOK re-checks one closed selector grammar before rendering:
// every step names a renderer-safe property, Iterate and Index are
// exclusive, indexes are non-negative, and a CONTAINS filter names a safe
// property (its needle is always a parameter).
func pivotSelectorOK(selector spec.Selector) error {
	if len(selector.Steps) == 0 {
		return fmt.Errorf("%w: selector is empty", ErrUnsupportedPostgresPlan)
	}
	for index, step := range selector.Steps {
		if step.Field == "" || !payloadPathSegmentPattern.MatchString(step.Field) {
			return fmt.Errorf("%w: selector step %d %q is not renderer-safe", ErrUnsupportedPostgresPlan, index, step.Field)
		}
		if step.Iterate && step.Index != nil {
			return fmt.Errorf("%w: selector step %d cannot both iterate and index", ErrUnsupportedPostgresPlan, index)
		}
		if step.Index != nil && *step.Index < 0 {
			return fmt.Errorf("%w: selector step %d indexes below zero", ErrUnsupportedPostgresPlan, index)
		}
	}
	if selector.Filter != nil {
		if selector.Filter.Field == "" || !payloadPathSegmentPattern.MatchString(selector.Filter.Field) {
			return fmt.Errorf("%w: selector CONTAINS filter property is not renderer-safe", ErrUnsupportedPostgresPlan)
		}
	}
	return nil
}

// pivotSelectorChain renders one selector as a FROM fragment (a chain of
// CROSS/JOIN LATERAL items beginning with the base expression bound into
// its own relation, appended after any host FROM item), the item
// expression of its result rows, the ordinality columns that chain the
// document order the AQL loops iterate, and the row conjuncts (the
// CONTAINS filter) every caller applies after the joins. It mirrors
// renderSelectorArrayFromSource (:317-349) and extractFinalExpr
// (selector.go:42-51) step for step:
//
//   - prefix plain member: guarded `-> ` read with the AQL
//     `LET s = cur.f FILTER s != null` row filter;
//   - prefix indexed step: array-guarded `-> $i::int` subscript (an
//     out-of-range or non-array read yields SQL NULL and the row is
//     dropped, matching the AQL `(cur.f ? cur.f : [])[i]` null filter);
//   - prefix repeated step: guarded jsonb_array_elements WITH ORDINALITY;
//     a non-array member expands to zero rows (the AQL
//     `FOR s IN (cur.f ? cur.f : [])`);
//   - the CONTAINS filter runs over the PREFIX result (the AQL position,
//     after the prefix loop and before the final extract):
//     CONTAINS(cur.f ? cur.f : "", @needle) — an empty needle matches every
//     candidate, a non-empty needle must occur in the string reading;
//   - final plain/index member: the same guarded read with the AQL
//     `FILTER __value != null` row filter; final repeated: the guarded
//     array is expanded into item rows, equal to the FLATTEN the AQL
//     callers apply over the array-per-root selector result (an
//     identical multiset, no null filter because arrays are never null).
//
// Every property name, index, and needle crosses SQL only as an ordered
// $n parameter.
func (r *planRenderer) pivotSelectorChain(base string, selector spec.Selector, name string) (from string, item string, ords []string, where []string, err error) {
	if err = pivotSelectorOK(selector); err != nil {
		return "", "", nil, nil, err
	}
	steps := selector.Steps
	root := name + "0"
	from = " CROSS JOIN LATERAL (VALUES (" + base + ")) AS " + root + "(v)"
	current := root + ".v"
	where = make([]string, 0, 1)
	memberParam := func(field string) string {
		return pivotDollar(r.textParameter(field, "pivot:prop:"+field))
	}
	for _, step := range steps[:len(steps)-1] {
		property := memberParam(step.Field)
		isObject := "jsonb_typeof(" + current + ") = 'object'"
		has := "jsonb_exists(" + current + ", " + property + "::text)"
		switch {
		case step.Index != nil:
			indexParameter := pivotDollar(r.addParameter(
				command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(*step.Index)}, ""))
			expr := "CASE WHEN " + isObject + " AND " + has +
				" AND jsonb_typeof(" + current + " -> " + property + "::text) = 'array'" +
				" THEN (" + current + " -> " + property + "::text) -> " + indexParameter + "::int END"
			alias := name + "_s"
			from += " JOIN LATERAL (SELECT " + expr + " AS v) AS " + alias + "(v) ON " + pivotAQLNotNull(alias+".v")
			current = alias + ".v"
		case step.Iterate:
			guard := "CASE WHEN " + isObject + " AND " + has +
				" AND jsonb_typeof(" + current + " -> " + property + "::text) = 'array'" +
				" THEN " + current + " -> " + property + "::text ELSE '[]'::jsonb END"
			alias := name + "_s"
			from += " CROSS JOIN LATERAL jsonb_array_elements(" + guard + ") WITH ORDINALITY AS " + alias + "(v, ord)"
			current = alias + ".v"
			ords = append(ords, alias+".ord")
		default:
			expr := "CASE WHEN " + isObject + " AND " + has +
				" THEN " + current + " -> " + property + "::text END"
			alias := name + "_s"
			from += " JOIN LATERAL (SELECT " + expr + " AS v) AS " + alias + "(v) ON " + pivotAQLNotNull(alias+".v")
			current = alias + ".v"
		}
		name += "x"
	}
	if selector.Filter != nil {
		property := memberParam(selector.Filter.Field)
		needle := pivotDollar(r.textParameter(selector.Filter.Needle, "pivot:needle:"+selector.Filter.Needle))
		hay := "CASE WHEN jsonb_typeof(" + current + ") = 'object'" +
			" AND jsonb_typeof(" + current + " -> " + property + "::text) = 'string'" +
			" THEN " + current + " ->> " + property + "::text ELSE '' END"
		where = append(where, "(length("+needle+") = 0 OR strpos("+hay+", "+needle+") > 0)")
	}
	last := steps[len(steps)-1]
	lastProperty := memberParam(last.Field)
	lastIsObject := "jsonb_typeof(" + current + ") = 'object'"
	lastHas := "jsonb_exists(" + current + ", " + lastProperty + "::text)"
	alias := name + "_f"
	switch {
	case last.Iterate:
		guard := "CASE WHEN " + lastIsObject + " AND " + lastHas +
			" AND jsonb_typeof(" + current + " -> " + lastProperty + "::text) = 'array'" +
			" THEN " + current + " -> " + lastProperty + "::text ELSE '[]'::jsonb END"
		from += " CROSS JOIN LATERAL jsonb_array_elements(" + guard + ") WITH ORDINALITY AS " + alias + "(v, ord)"
		item = alias + ".v"
		ords = append(ords, alias+".ord")
	case last.Index != nil:
		indexParameter := pivotDollar(r.addParameter(
			command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(*last.Index)}, ""))
		expr := "CASE WHEN " + lastIsObject + " AND " + lastHas +
			" AND jsonb_typeof(" + current + " -> " + lastProperty + "::text) = 'array'" +
			" THEN (" + current + " -> " + lastProperty + "::text) -> " + indexParameter + "::int END"
		from += " JOIN LATERAL (SELECT " + expr + " AS v) AS " + alias + "(v) ON " + pivotAQLNotNull(alias+".v")
		item = alias + ".v"
	default:
		expr := "CASE WHEN " + lastIsObject + " AND " + lastHas +
			" THEN " + current + " -> " + lastProperty + "::text END"
		from += " JOIN LATERAL (SELECT " + expr + " AS v) AS " + alias + "(v) ON " + pivotAQLNotNull(alias+".v")
		item = alias + ".v"
	}
	return from, item, ords, where, nil
}

// pivotWhereJoin renders the chain conjuncts as a WHERE clause.
func pivotWhereJoin(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}

// pivotSelectorArray renders one selector's result as a single jsonb ARRAY
// expression (the FLATTENed selector arrays of the AQL coded-value chain),
// aggregated in document order for determinism.
func (r *planRenderer) pivotSelectorArray(base string, selector spec.Selector, name string) (string, error) {
	from, item, ords, where, err := r.pivotSelectorChain(base, selector, name)
	if err != nil {
		return "", err
	}
	orderBy := ""
	if len(ords) != 0 {
		orderBy = " ORDER BY " + strings.Join(ords, ", ")
	}
	return "(SELECT COALESCE(jsonb_agg(" + item + orderBy + "), '[]'::jsonb)" +
		" FROM (SELECT 1) AS " + name + "_hub" + from + pivotWhereJoin(where) + ")", nil
}

// pivotIdentitySQL composes the row identity byte-counterpart of
// TO_STRING(["GROUPED_PIVOT", @constructionID, ["<Kind0>", kv0], …])
// (render/aql/table_shape_exclusion.go:137-150): JSON-compact members,
// strings double-quoted through to_jsonb of the bound parameter text.
// keyExprs carry the jsonb group-key values; every kind label and the
// construction ID cross SQL only as parameters.
func (r *planRenderer) pivotIdentitySQL(constructionID string, kinds []string, keyExprs []string) string {
	identity := "'[' || " + quoteSQLLiteral(`"`+pivotIdentityKind+`"`) +
		" || ',' || to_jsonb(" + pivotDollar(r.textParameter(constructionID, "")) + "::text)"
	for index, key := range keyExprs {
		kind := kinds[index]
		identity += " || ',' || '[' || to_jsonb(" + pivotDollar(r.textParameter(kind, "pivot:kind:"+kind)) + "::text)" +
			" || ',' || COALESCE(" + key + "::text, 'null') || ']'"
	}
	return identity + " || ']'"
}

// ---------------------------------------------------------------------------
// Scalar spine slice: contract
// ---------------------------------------------------------------------------

// validateGroupedPivotPlanSurface is the single closed-slice contract
// behind both scalar doors. It is total over any plan mentioning a
// GROUPED_PIVOT operation and refuses every shape outside the registered
// slice with ErrUnsupportedPostgresPlan before plan validation, scope
// loading, or any rendering can reject with an off-type error.
func validateGroupedPivotPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	// The leading ROOT_SCAN is required, not optional: the statement
	// builder and validateGroupedPivotPayload read Operations[0].RootScan
	// below, so without this front check a hand-carried plan such as
	// Operations = [GROUPED_PIVOT, RETURN] (or any non-root first
	// operation) would reach a nil payload dereference instead of a
	// typed refusal (the coded twin's leading-scan check at
	// validateCodedPivotOperations and the unpivot index-0 refusal,
	// unpivot.go:219-221; payload-nil discipline, unpivot.go:191-194).
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: the pivot slice leads with exactly one ROOT_SCAN at position 0", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return fmt.Errorf("%w: a pivot combined with a stage sequence has no PostgreSQL rendering (staged compositions are pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		// The plan-level preview root-id window (ir/physical_types.go:27)
		// must be refused HERE: this door dispatches before the spine's
		// plan-level window gate (render.go), so it is the only place the
		// check can fire for a pivot plan — the twin coded-pivot, coded-group,
		// and graph doors keep the same check (C8, ruling R3; the shape is
		// a pg-sql-16 terminal-windowing capability, never silently rendered).
		return fmt.Errorf("%w: the plan-level preview source window requires Pivot-terminal windowing contracts that are not registered for the scalar pivot (pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	var pivot *ir.PhysicalGroupedPivot
	pivotSeen, sortSeen, limitSeen, returnSeen := false, false, false, false
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the pivot slice leads with exactly one ROOT_SCAN at position 0, got one at %d",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: ROOT_SCAN is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := validatePivotRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalFilterOp:
			if pivotSeen {
				return fmt.Errorf("%w: FILTER must precede the pivot; filtering reshaped cells is a registered pg-sql-16 composition", ErrUnsupportedPostgresPlan)
			}
			if operation.Filter == nil {
				return fmt.Errorf("%w: FILTER is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			if pivotSeen {
				return fmt.Errorf("%w: DERIVED_LET must precede the pivot", ErrUnsupportedPostgresPlan)
			}
			if operation.DerivedLet == nil {
				return fmt.Errorf("%w: DERIVED_LET is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalGroupedPivotOp:
			if pivotSeen {
				return fmt.Errorf("%w: the pivot slice registers exactly one reshape; a second pivot has no PostgreSQL rendering (multiple reshapes are construction stage sequences, which stay refused)", ErrUnsupportedPostgresPlan)
			}
			if operation.GroupedPivot == nil {
				return fmt.Errorf("%w: GROUPED_PIVOT is missing its payload", ErrUnsupportedPostgresPlan)
			}
			pivot = operation.GroupedPivot
			pivotSeen = true
		case ir.PhysicalSortOp:
			if !pivotSeen || sortSeen || limitSeen || returnSeen {
				return fmt.Errorf("%w: the reshaped execution SORT must be at most one SORT directly after the pivot and before any LIMIT or RETURN", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return fmt.Errorf("%w: SORT is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := validatePivotSortKeys(*operation.Sort, *pivot); err != nil {
				return err
			}
			sortSeen = true
		case ir.PhysicalLimitOp:
			if !pivotSeen || limitSeen || returnSeen {
				return fmt.Errorf("%w: the reshaped execution LIMIT must be at most one LIMIT after the pivot's SORT and before RETURN", ErrUnsupportedPostgresPlan)
			}
			if !sortSeen {
				return fmt.Errorf("%w: a LIMIT after GROUPED_PIVOT requires the stable row identity SORT first (the ir reshaped-window guard's contract, re-read at the renderer)", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return fmt.Errorf("%w: LIMIT is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if value, ok := plan.BindVars[operation.Limit.BindKey].(int); !ok || value <= 0 {
				return fmt.Errorf("%w: reshaped LIMIT bind %q must be a positive int", ErrUnsupportedPostgresPlan, operation.Limit.BindKey)
			}
			limitSeen = true
		case ir.PhysicalReturnOp:
			if !pivotSeen {
				return fmt.Errorf("%w: RETURN cannot run before the pivot it projects", ErrUnsupportedPostgresPlan)
			}
			if returnSeen {
				return fmt.Errorf("%w: the pivot slice registers exactly one terminal RETURN", ErrUnsupportedPostgresPlan)
			}
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: the pivot slice requires a terminal RETURN after its pivot", ErrUnsupportedPostgresPlan)
			}
			if operation.Return == nil {
				return fmt.Errorf("%w: RETURN is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := validatePivotReturn(*operation.Return, *pivot); err != nil {
				return err
			}
			returnSeen = true
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with GROUPED_PIVOT in the registered slice; filters beyond the canonical scope, traversals, unnests, sets, subplans, and every other shape belong to the read spine or the pg-sql-16 composition capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !pivotSeen {
		return fmt.Errorf("%w: the pivot slice requires exactly one GROUPED_PIVOT between ROOT_SCAN and RETURN", ErrUnsupportedPostgresPlan)
	}
	if !returnSeen {
		return fmt.Errorf("%w: the pivot slice requires a terminal RETURN after its pivot", ErrUnsupportedPostgresPlan)
	}
	return validateGroupedPivotPayload(plan, *pivot, plan.Operations[0].RootScan.Variable)
}

// validatePivotRootScan re-reads the direct root scan exactly as the spine
// and the other registered slices bind it (the validateUnpivotRootScan
// spelling, unpivot.go:291-307).
func validatePivotRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.Variable) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.CollectionBindKey) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	if resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	return nil
}

// validatePivotSortKeys pins the reshaped window SORT to the single stable
// row-identity key (the ir reshaped-window guard,
// ir/physical_validation.go:101-132, re-read with the unpivot.go:451-461
// spelling).
func validatePivotSortKeys(sort ir.PhysicalSort, pivot ir.PhysicalGroupedPivot) error {
	if len(sort.Keys) != 1 || sort.Keys[0].BindKey != "" ||
		sort.Keys[0].Variable != pivot.OutputRowVariable ||
		len(sort.Keys[0].Path) != 1 || sort.Keys[0].Path[0] != pivotRowIDColumn {
		return fmt.Errorf("%w: the reshaped pivot SORT must sort on %s.%s exactly",
			ErrUnsupportedPostgresPlan, pivot.OutputRowVariable, pivotRowIDColumn)
	}
	return nil
}

// pivotLegalOutputNames lists the pivot's renderable output names: every
// group-key output (hidden ones included — the compiler-appended hidden
// auth key proves it observable), every category output, the evidence
// column exactly under EXCLUDE_WITH_EVIDENCE, and the stable row identity.
func pivotLegalOutputNames(pivot ir.PhysicalGroupedPivot) map[string]bool {
	legal := map[string]bool{pivotRowIDColumn: true}
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		legal[name] = true
	}
	for _, category := range pivot.Categories {
		legal[category.Output] = true
	}
	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		legal[pivot.UnlistedEvidenceColumn] = true
	}
	return legal
}

// validatePivotReturn pins every terminal projection to a direct
// single-name read of the pivot output row over the registered name set
// (the validateUnpivotReturn precedent, unpivot.go:466-495).
func validatePivotReturn(ret ir.PhysicalReturn, pivot ir.PhysicalGroupedPivot) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the pivot slice requires a terminal RETURN with at least one projection", ErrUnsupportedPostgresPlan)
	}
	legal := pivotLegalOutputNames(pivot)
	seen := map[string]bool{}
	for _, projection := range ret.Projections {
		if !legal[projection.Name] {
			return fmt.Errorf("%w: pivot RETURN projection %q is not a registered pivot output",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if seen[projection.Name] {
			return fmt.Errorf("%w: pivot RETURN projects %q twice", ErrUnsupportedPostgresPlan, projection.Name)
		}
		seen[projection.Name] = true
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: pivot RETURN projection %q must be a direct value read",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Value.Variable != pivot.OutputRowVariable {
			return fmt.Errorf("%w: pivot RETURN projection %q must read the pivot output row %q, got %q",
				ErrUnsupportedPostgresPlan, projection.Name, pivot.OutputRowVariable, projection.Value.Variable)
		}
		if len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return fmt.Errorf("%w: pivot RETURN projection %q must be the single-name read %s.%s",
				ErrUnsupportedPostgresPlan, projection.Name, pivot.OutputRowVariable, projection.Name)
		}
		if projection.Value.BindKey != "" {
			return fmt.Errorf("%w: pivot RETURN projection %q must be a row read, not a bind",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return nil
}

// validateGroupedPivotPayload enforces the closed scalar payload with
// re-read, never trusted, identity facts. The authoritative semantic gate
// is ir.Validate (ir/physical_table_shape.go validatePhysicalGroupedPivot
// :52-206); this is the renderer-side boundary keeping the slice closed.
func validateGroupedPivotPayload(plan ir.PhysicalPlan, pivot ir.PhysicalGroupedPivot, rootVariable string) error {
	if pivot.CodedCorrelation != nil || len(pivot.CodedCategories) != 0 || strings.TrimSpace(pivot.CodedSourceVariable) != "" {
		return fmt.Errorf("%w: coded Pivot fields on a spine operation have no rendering (the AQL producer census keeps them on the construction stage; ir/physical_table_shape.go:36-38)",
			ErrUnsupportedPostgresPlan)
	}
	if len(pivot.RowValues) != 0 {
		return fmt.Errorf("%w: pivot row values have no PostgreSQL rendering (row values are the pg-sql-13 capability; the spine producer lower/recipe_table_reshape.go never emits them)",
			ErrUnsupportedPostgresPlan)
	}
	if pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" || pivot.RootContributorVariable != "" {
		return fmt.Errorf("%w: pivot root-contributor columns have no PostgreSQL rendering (the construction-only producer lower/recipe_construction.go:1291-1308)",
			ErrUnsupportedPostgresPlan)
	}
	if len(pivot.Categories) == 0 {
		return fmt.Errorf("%w: the registered slice requires at least one frozen pivot category", ErrUnsupportedPostgresPlan)
	}
	if !pivotDuplicatePolicies[pivot.DuplicatePolicy] {
		return fmt.Errorf("%w: pivot duplicate policy %q is not registered", ErrUnsupportedPostgresPlan, pivot.DuplicatePolicy)
	}
	if !pivotMissingPolicies[pivot.MissingCellPolicy] {
		return fmt.Errorf("%w: pivot missing-cell policy %q is not registered", ErrUnsupportedPostgresPlan, pivot.MissingCellPolicy)
	}
	if pivot.UnlistedCategoryPolicy != "ERROR" && pivot.UnlistedCategoryPolicy != "EXCLUDE_WITH_EVIDENCE" {
		return fmt.Errorf("%w: pivot unlisted-category policy %q is not registered for the ordinary pivot (IGNORE is coded-only, ir/physical_table_shape.go:65-73)",
			ErrUnsupportedPostgresPlan, pivot.UnlistedCategoryPolicy)
	}
	if _, ok := pivotValueTypeOf[pivot.CategoryType]; !ok {
		return fmt.Errorf("%w: pivot category type %q is not a registered table scalar kind", ErrUnsupportedPostgresPlan, pivot.CategoryType)
	}
	if _, ok := pivotValueTypeOf[pivot.ValueType]; !ok {
		return fmt.Errorf("%w: pivot value type %q is not a registered table scalar kind", ErrUnsupportedPostgresPlan, pivot.ValueType)
	}
	if pivot.DuplicatePolicy != "ERROR" && pivot.ValueType != "INTEGER" && pivot.ValueType != "DECIMAL" {
		return fmt.Errorf("%w: pivot reducer %q requires a numeric value column (the producer gate, lower/recipe_table_reshape.go:126-130)",
			ErrUnsupportedPostgresPlan, pivot.DuplicatePolicy)
	}
	if (pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE") != (pivot.UnlistedEvidenceColumn != "") {
		return fmt.Errorf("%w: the unlisted evidence column exists exactly under EXCLUDE_WITH_EVIDENCE (ir/physical_table_shape.go:74-80)",
			ErrUnsupportedPostgresPlan)
	}
	if value, ok := plan.BindVars[pivot.ConstructionIDBindKey].(string); !ok || value == "" {
		return fmt.Errorf("%w: pivot construction ID bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, pivot.ConstructionIDBindKey)
	}
	missingCategory := false
	outputs := map[string]bool{}
	for index, category := range pivot.Categories {
		if outputs[category.Output] {
			return fmt.Errorf("%w: pivot category %d repeats output %q", ErrUnsupportedPostgresPlan, index, category.Output)
		}
		outputs[category.Output] = true
		switch category.MatchKind {
		case ir.PhysicalPivotCategoryValueMatch:
			if category.ValueBindKey == "" {
				return fmt.Errorf("%w: VALUE category %d requires a frozen value bind", ErrUnsupportedPostgresPlan, index)
			}
			typeOf, ok := pivotValueTypeOf[category.ValueKind]
			if !ok {
				return fmt.Errorf("%w: VALUE category %d kind %q is not a registered table scalar kind",
					ErrUnsupportedPostgresPlan, index, category.ValueKind)
			}
			if want := pivotValueTypeOf[pivot.CategoryType]; typeOf != want {
				return fmt.Errorf("%w: VALUE category %d kind %q disagrees with the frozen category type %q",
					ErrUnsupportedPostgresPlan, index, category.ValueKind, pivot.CategoryType)
			}
			if err := validatePivotCategoryBind(plan.BindVars, category); err != nil {
				return err
			}
		case ir.PhysicalPivotCategoryNullMatch:
			if category.ValueBindKey != "" {
				return fmt.Errorf("%w: NULL category %d must not carry a value bind", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalPivotCategoryMissingMatch:
			missingCategory = true
			if category.ValueBindKey != "" {
				return fmt.Errorf("%w: MISSING category %d must not carry a value bind", ErrUnsupportedPostgresPlan, index)
			}
		default:
			return fmt.Errorf("%w: pivot category %d match kind %q is not registered",
				ErrUnsupportedPostgresPlan, index, string(category.MatchKind))
		}
	}
	if missingCategory && pivot.CategoryPresence == nil && !pivot.CategoryPresenceFromInput {
		return fmt.Errorf("%w: a MISSING pivot category requires a preserved category presence contract (ir/physical_table_shape.go, re-read)", ErrUnsupportedPostgresPlan)
	}
	if pivot.CategoryPresence != nil && pivot.CategoryPresenceFromInput {
		return fmt.Errorf("%w: category presence cannot be both contract-computed and input-companion", ErrUnsupportedPostgresPlan)
	}
	if pivot.CategoryPresence != nil {
		if len(pivot.CategoryPresence.Paths) == 0 {
			return fmt.Errorf("%w: the category presence contract must prove at least one path", ErrUnsupportedPostgresPlan)
		}
		source := pivot.CategoryPresence.Source
		if source.Variable != "" && source.Variable != rootVariable {
			return fmt.Errorf("%w: the category presence contract must prove over the pivot's scanned root row %q, got %q",
				ErrUnsupportedPostgresPlan, rootVariable, source.Variable)
		}
		if len(source.Path) != 0 && !(len(source.Path) == 1 && source.Path[0] == "payload") {
			return fmt.Errorf("%w: the category presence contract source must be the root document, got path %v",
				ErrUnsupportedPostgresPlan, source.Path)
		}
	}
	if pivot.CategoryPresenceFromInput {
		companion := false
		for _, projection := range pivot.InputProjections {
			if projection.Name == pivot.CategoryPresenceColumn {
				companion = true
			}
		}
		if pivot.CategoryPresenceColumn == "" || !companion {
			return fmt.Errorf("%w: CategoryPresenceFromInput requires the companion column %q among the input projections",
				ErrUnsupportedPostgresPlan, pivot.CategoryPresenceColumn)
		}
	}
	for _, key := range pivot.GroupKeys {
		if _, ok := pivotValueTypeOf[key.Kind]; !ok {
			return fmt.Errorf("%w: pivot group key %q kind %q is not a registered table scalar kind",
				ErrUnsupportedPostgresPlan, key.Column, key.Kind)
		}
	}
	return validatePivotInputProjections(pivot, rootVariable)
}

// validatePivotCategoryBind re-reads the frozen category value: the bind
// must exist with a JSON-encodable scalar matching the declared kind.
func validatePivotCategoryBind(bindVars map[string]any, category ir.PhysicalGroupedPivotCategory) error {
	raw, ok := bindVars[category.ValueBindKey]
	if !ok {
		return fmt.Errorf("%w: pivot category value bind %q is missing", ErrUnsupportedPostgresPlan, category.ValueBindKey)
	}
	switch category.ValueKind {
	case "STRING":
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("%w: pivot category value bind %q must be a string for kind STRING",
				ErrUnsupportedPostgresPlan, category.ValueBindKey)
		}
	case "INTEGER":
		switch raw.(type) {
		case int64, int:
		default:
			return fmt.Errorf("%w: pivot category value bind %q must be an integer for kind INTEGER",
				ErrUnsupportedPostgresPlan, category.ValueBindKey)
		}
	case "DECIMAL":
		switch raw.(type) {
		case float64, int64, int:
		default:
			return fmt.Errorf("%w: pivot category value bind %q must be numeric for kind DECIMAL",
				ErrUnsupportedPostgresPlan, category.ValueBindKey)
		}
	case "BOOLEAN":
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("%w: pivot category value bind %q must be a boolean for kind BOOLEAN",
				ErrUnsupportedPostgresPlan, category.ValueBindKey)
		}
	}
	return nil
}

// pivotConsumedInputNames lists the input projections the pivot reads: the
// category column, the value column, every group-key column, and the
// presence companion when input-carried. The presence contract is computed
// over the root document, not a projection. Unconsumed projections (the
// OBJECT/SLICE composites ir can carry) are inert and reach no SQL: the
// group rows are read only through these names (and the refused RowValues
// and RootContributors), so materializing only these reads is observable
// equivalence to the AQL ground truth input document.
func pivotConsumedInputNames(pivot ir.PhysicalGroupedPivot) map[string]bool {
	consumed := map[string]bool{pivot.CategoryColumn: true, pivot.ValueColumn: true}
	for _, key := range pivot.GroupKeys {
		consumed[key.Column] = true
	}
	if pivot.CategoryPresenceFromInput {
		consumed[pivot.CategoryPresenceColumn] = true
	}
	return consumed
}

// pivotInputReadShape reports the rendered read form of one CONSUMED input
// projection: either a direct document path read of the scanned root row
// (the hidden auth projection the compiler mutator appends,
// compiler/recipe_execution.go:534-585) or an EXTRACT selector passing the
// package's single selector contract with a registered null behavior.
func pivotInputReadShape(projection ir.PhysicalProjection, rootVariable string) (path []string, behavior ir.PhysicalNullBehavior, err error) {
	if projection.Expression != nil {
		path, err := validateSelectorExtraction(projection)
		if err != nil {
			return nil, "", err
		}
		switch projection.Expression.NullBehavior {
		case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
		default:
			return nil, "", fmt.Errorf("%w: pivot input projection %q null behavior %q has no registered extraction form",
				ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.NullBehavior))
		}
		if projection.Expression.Extract != nil && projection.Expression.Extract.Source.Variable != rootVariable {
			return nil, "", fmt.Errorf("%w: pivot input projection %q must extract from the scanned root row %q, got %q",
				ErrUnsupportedPostgresPlan, projection.Name, rootVariable, projection.Expression.Extract.Source.Variable)
		}
		return path, projection.Expression.NullBehavior, nil
	}
	if projection.Value.Variable != rootVariable || len(projection.Value.Path) == 0 || projection.Value.BindKey != "" {
		return nil, "", fmt.Errorf("%w: pivot input projection %q must be a direct document read of %q or a registered selector extraction",
			ErrUnsupportedPostgresPlan, projection.Name, rootVariable)
	}
	return projection.Value.Path, ir.PhysicalPreserveNull, nil
}

// validatePivotInputProjections checks every CONSUMED input projection has
// a rendered read form and that every consumed name exists exactly once.
func validatePivotInputProjections(pivot ir.PhysicalGroupedPivot, rootVariable string) error {
	consumed := pivotConsumedInputNames(pivot)
	found := map[string]bool{}
	for _, projection := range pivot.InputProjections {
		if !consumed[projection.Name] {
			continue
		}
		if found[projection.Name] {
			return fmt.Errorf("%w: pivot input column %q is projected twice", ErrUnsupportedPostgresPlan, projection.Name)
		}
		found[projection.Name] = true
		if _, _, err := pivotInputReadShape(projection, rootVariable); err != nil {
			return err
		}
	}
	for name := range consumed {
		if !found[name] {
			return fmt.Errorf("%w: the pivot reads input column %q but no input projection carries it",
				ErrUnsupportedPostgresPlan, name)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scalar spine slice: door + statement
// ---------------------------------------------------------------------------

func renderGroupedPivotPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGroupedPivotPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderGroupedPivotStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// renderGroupedPivotStatement builds the whole scalar pivot statement:
//
//	WITH pv_src AS (SELECT <consumed input reads> FROM <source region>)
//	SELECT <group keys>, <category cells>, <evidence>, <identity>
//	FROM pv_src AS p GROUP BY <key slots> HAVING <ordered guard chain>
//	ORDER BY <window identity or per-key rank terms> [LIMIT $n]
func (r *planRenderer) renderGroupedPivotStatement(plan ir.PhysicalPlan) (string, error) {
	var pivot *ir.PhysicalGroupedPivot
	sortSeen := false
	limitBind := ""
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalGroupedPivotOp:
			pivot = operation.GroupedPivot
		case ir.PhysicalSortOp:
			sortSeen = true
		case ir.PhysicalLimitOp:
			limitBind = operation.Limit.BindKey
		}
	}
	rootVariable := plan.Operations[0].RootScan.Variable

	// --- source region through the untouched spine arms. ---
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
			if !canonical {
				return "", fmt.Errorf("%w: a non-canonical FILTER cannot coexist with the registered pivot (probe-attested renders-at-AQL divergence, pg-sql-16)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered pivot source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}

	// --- consumed input reads into source columns. ---
	consumed := pivotConsumedInputNames(*pivot)
	reads := map[string]string{}
	slotExpressions := make([]string, 0, len(pivot.InputProjections))
	addSlot := func(expression string) string {
		slot := "pv_" + strconv.Itoa(len(slotExpressions))
		slotExpressions = append(slotExpressions, expression+" AS "+slot)
		return "p." + slot
	}
	categoryBehavior := ir.PhysicalPreserveNull
	for _, projection := range pivot.InputProjections {
		if !consumed[projection.Name] {
			continue
		}
		path, behavior, err := pivotInputReadShape(projection, rootVariable)
		if err != nil {
			return "", err
		}
		var expression string
		if projection.Expression != nil {
			extraction, err := r.documentPathExpression(r.rootAlias, path)
			if err != nil {
				return "", err
			}
			switch behavior {
			case ir.PhysicalOmitNulls:
				expression = "NULLIF((" + extraction + "), 'null'::jsonb)"
			case ir.PhysicalEmptyOnNull:
				expression = "COALESCE(NULLIF((" + extraction + "), 'null'::jsonb), '\"\"'::jsonb)"
			default:
				expression = "(" + extraction + ")"
			}
		} else {
			extraction, err := r.documentPathFromExpression(r.documentColumn(r.rootAlias), path)
			if err != nil {
				return "", err
			}
			expression = "(" + extraction + ")"
		}
		reads[projection.Name] = addSlot(expression)
		if projection.Name == pivot.CategoryColumn {
			categoryBehavior = behavior
		}
	}
	pvCat, err := pivotSlotRead(reads, pivot.CategoryColumn)
	if err != nil {
		return "", err
	}
	pvVal, err := pivotSlotRead(reads, pivot.ValueColumn)
	if err != nil {
		return "", err
	}

	// --- category presence (null_semantics contract). ---
	present := ""
	switch {
	case pivot.CategoryPresence != nil:
		proofs := make([]string, 0, len(pivot.CategoryPresence.Paths))
		for _, path := range pivot.CategoryPresence.Paths {
			proof, err := r.propertyPresenceExpression(r.rootAlias, path)
			if err != nil {
				return "", err
			}
			proofs = append(proofs, proof)
		}
		presence := "CASE WHEN " + strings.Join(proofs, " OR ") + " THEN 'true'::jsonb ELSE 'false'::jsonb END"
		present = addSlot(presence) + " = 'true'::jsonb"
	case pivot.CategoryPresenceFromInput:
		companion, err := pivotSlotRead(reads, pivot.CategoryPresenceColumn)
		if err != nil {
			return "", err
		}
		// COALESCE keeps presence TWO-VALUED: the companion read is
		// SQL NULL when the presence property is absent from the
		// document (no NOT-NULL guarantee on the slot read), and AQL
		// renders presence as row[@presence] == true, which is
		// two-valued (render/aql/table_reshape.go:405-407, :431 —
		// null == true is false), so an absent-companion row matches
		// MISSING and is listed, not unlisted. A bare comparison would
		// leave present NULL there: the MISSING arm NOT (present) and
		// every VALUE/NULL arm go NULL, the row silently drops from
		// rawCount/typedCount (spurious CELL_MISSING under the ERROR
		// policy) and listed NULL flips into the unlisted count
		// through the load-bearing NOT COALESCE(listed, false) below.
		present = "COALESCE(" + companion + " = 'true'::jsonb, false)"
	default:
		switch categoryBehavior {
		case ir.PhysicalOmitNulls:
			present = pvCat + " IS NOT NULL"
		default:
			// PRESERVE_NULL and EMPTY_ON_NULL render the projected property
			// always-present; their value form cannot separate absent from
			// stored null (the registered mapping; the presence contract is
			// the load-bearing bit and is exercised live for NULL/MISSING
			// keys, which the producer always carries for those kinds).
			present = "true"
		}
	}

	valueTypeOf := quoteSQLLiteral(pivotValueTypeOf[pivot.ValueType])
	typedConjunct := pvVal + " IS NOT NULL AND " + pvVal + " <> 'null'::jsonb"

	// --- category matches (frozen values are parameters, never SQL text). ---
	matchPredicates := make([]string, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		switch category.MatchKind {
		case ir.PhysicalPivotCategoryValueMatch:
			raw, err := pivotCategoryRaw(plan.BindVars, category)
			if err != nil {
				return "", err
			}
			valueParameter := pivotDollar(r.jsonbParameter(raw))
			matchPredicates = append(matchPredicates,
				"("+present+" AND jsonb_typeof("+pvCat+") = "+quoteSQLLiteral(pivotValueTypeOf[category.ValueKind])+
					" AND "+pvCat+" = "+valueParameter+"::jsonb)")
		case ir.PhysicalPivotCategoryNullMatch:
			nullRead := "(" + pvCat + " IS NULL OR " + pvCat + " = 'null'::jsonb)"
			if categoryBehavior == ir.PhysicalOmitNulls {
				// The null-omission behavior reports stored null as absent;
				// nothing reads AQL-null while present.
				nullRead = "(" + pvCat + " IS NOT NULL AND " + pvCat + " = 'null'::jsonb)"
			}
			matchPredicates = append(matchPredicates, "("+present+" AND "+nullRead+")")
		case ir.PhysicalPivotCategoryMissingMatch:
			matchPredicates = append(matchPredicates, "NOT ("+present+")")
		}
	}
	listed := "(" + strings.Join(matchPredicates, " OR ") + ")"

	// --- group keys. ---
	keySlots := make([]string, 0, len(pivot.GroupKeys))
	kinds := make([]string, 0, len(pivot.GroupKeys))
	keyNames := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		expr, err := pivotSlotRead(reads, key.Column)
		if err != nil {
			return "", err
		}
		keySlots = append(keySlots, expr)
		kinds = append(kinds, key.Kind)
		name := key.Output
		if name == "" {
			name = key.Column
		}
		keyNames = append(keyNames, name)
	}

	// --- cells and the ordered guard chain. ---
	cellSelects := make([]string, 0, len(pivot.Categories))
	guardTerms := make([]string, 0, 2*len(pivot.Categories))
	if pivot.UnlistedCategoryPolicy == "ERROR" {
		// NOT COALESCE: a row whose category read is SQL NULL (absent
		// property) is unlisted under AQL's two-valued FILTER NOT
		// listed; SQL's three-valued logic would drop it silently.
		guardTerms = append(guardTerms,
			"WHEN COUNT(*) FILTER (WHERE NOT COALESCE("+listed+", false)) > 0 THEN "+guardCast(pivotUnlistedTag))
	}
	for _, match := range matchPredicates {
		rawCount := "COUNT(*) FILTER (WHERE " + match + ")"
		typedCount := "COUNT(*) FILTER (WHERE " + match + " AND " + typedConjunct + ")"
		mismatchCount := "COUNT(*) FILTER (WHERE " + match + " AND " + typedConjunct + " AND jsonb_typeof(" + pvVal + ") <> " + valueTypeOf + ")"
		cell := ""
		switch pivot.DuplicatePolicy {
		case "ERROR":
			// AQL: ASSERT(LENGTH(values)>0 MISSING)?(ASSERT(LENGTH<=1
			// CARDINALITY)?FIRST(values):null):null — null-preserving FIRST
			// over the raw matched list; the guards ride the HAVING chain.
			firstRaw := "(ARRAY_AGG(" + pvVal + ") FILTER (WHERE " + match + "))[1]"
			cell = "COALESCE(" + firstRaw + ", 'null'::jsonb)"
			if pivot.MissingCellPolicy == "ERROR" {
				guardTerms = append(guardTerms, "WHEN "+rawCount+" = 0 THEN "+guardCast(pivotCellMissingTag))
			}
			guardTerms = append(guardTerms, "WHEN "+rawCount+" > 1 THEN "+guardCast(pivotCardinalityTag))
		default:
			// SUM/MIN/MAX over the typed list: (ASSERT(missing-condition)?
			// (LENGTH(typed)==0 ? null : REDUCER(typed)):null). The ordinary
			// branch conditions MISSING on the RAW count
			// (table_reshape.go:230); the reducer enumerates the typed
			// list, where the value-type ASSERT lives (:215/:252).
			// The cast is pre-guarded on the declared type: an uncastable
			// string (typeof <> number, e.g. "abc") would otherwise abort
			// the aggregate with an UNTAGGED 22P02 before the HAVING chain
			// can name the winner (the AQL ASSERT(TYPENAME(v) == @type,
			// "TABLE_PIVOT_VALUE_TYPE_MISMATCH") at table_reshape.go:215
			// runs per item, before the reducer). Casting only numbers
			// keeps the aggregate total; the mismatchCount guard below
			// then raises the canonical tag for every non-number,
			// coercible or not (F-2).
			safeCast := "CASE WHEN jsonb_typeof(" + pvVal + ") = " + valueTypeOf + " THEN " +
				pivotScalarText(pvVal) + "::double precision END"
			reducer := pivot.DuplicatePolicy + "(" + safeCast + ") FILTER (WHERE " + match + " AND " + typedConjunct + ")"
			cell = "(CASE WHEN " + typedCount + " = 0 THEN 'null'::jsonb ELSE to_jsonb(" + reducer + ") END)"
			if pivot.MissingCellPolicy == "ERROR" {
				guardTerms = append(guardTerms, "WHEN "+rawCount+" = 0 THEN "+guardCast(pivotCellMissingTag))
			}
			guardTerms = append(guardTerms, "WHEN "+mismatchCount+" > 0 THEN "+guardCast(pivotValueTypeTag))
		}
		cellSelects = append(cellSelects, cell)
	}

	// --- identity (one composition shared with the window ORDER BY). ---
	identity := r.pivotIdentitySQL(bindStringVar(plan.BindVars, pivot.ConstructionIDBindKey), kinds, keySlots)

	// --- final positional projection over the registered RETURN list. ---
	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			returnOp = operation.Return
		}
	}
	expressions := map[string]string{}
	for index, name := range keyNames {
		expressions[name] = keySlots[index]
	}
	for index, category := range pivot.Categories {
		expressions[category.Output] = cellSelects[index]
	}
	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		// The per-group unlisted count is the kept evidence (the exclusion
		// RETURN itself is the AQL-only pg-sql-26 operation).
		expressions[pivot.UnlistedEvidenceColumn] =
			"to_jsonb(COUNT(*) FILTER (WHERE NOT COALESCE(" + listed + ", false)))"
	}
	expressions[pivotRowIDColumn] = identity
	selects := make([]string, 0, len(returnOp.Projections))
	names := make([]string, 0, len(returnOp.Projections))
	for _, projection := range returnOp.Projections {
		expression, ok := expressions[projection.Name]
		if !ok {
			return "", fmt.Errorf("%w: pivot RETURN projection %q has no rendered expression",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, projection.Name)
	}
	r.outputColumns = names

	// --- source SQL (spine arms) and the grouped statement. ---
	source := "SELECT " + strings.Join(slotExpressions, ", ") + " FROM " +
		r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		source += " " + join
	}
	for _, lateral := range r.laterals {
		source += " " + lateral
	}
	if len(r.where) > 0 {
		source += " WHERE " + strings.Join(r.where, " AND ")
	}
	statement := "WITH pv_src AS (" + source + ") SELECT " + strings.Join(selects, ", ") +
		" FROM pv_src AS p"
	if len(keySlots) > 0 {
		statement += " GROUP BY " + strings.Join(keySlots, ", ")
	} else {
		// A keyless pivot still groups all rows into one group.
		statement += " GROUP BY ()"
	}
	if len(guardTerms) > 0 {
		statement += " HAVING (CASE " + strings.Join(guardTerms, " ") + " ELSE 1 END) = 1"
	}
	statement += " ORDER BY " + pivotWindowOrder(keySlots, identity, sortSeen)
	if limitBind != "" {
		limit := plan.BindVars[limitBind].(int)
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		statement += " LIMIT " + pivotDollar(index)
	}
	return statement, nil
}

func pivotSlotRead(reads map[string]string, name string) (string, error) {
	expr, ok := reads[name]
	if !ok {
		return "", fmt.Errorf("%w: pivot input column %q has no rendered read", ErrUnsupportedPostgresPlan, name)
	}
	return expr, nil
}

// pivotCategoryRaw JSON-encodes one frozen category value bind for a
// jsonb comparison parameter.
func pivotCategoryRaw(bindVars map[string]any, category ir.PhysicalGroupedPivotCategory) (json.RawMessage, error) {
	raw, ok := bindVars[category.ValueBindKey]
	if !ok {
		return nil, fmt.Errorf("%w: pivot category value bind %q is missing", ErrUnsupportedPostgresPlan, category.ValueBindKey)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: pivot category value bind %q is not JSON-encodable: %v",
			ErrUnsupportedPostgresPlan, category.ValueBindKey, err)
	}
	return json.RawMessage(encoded), nil
}

// pivotWindowOrder renders the final ORDER BY. With the reshaped-window
// SORT the rows order by the stable identity under COLLATE "C" (the AQL
// window sorts {OutputRowVariable, __loom_row_id}, the D-19a bound).
// Without it the AQL emits SORT per group-key variable ASC
// (table_reshape.go:138-140): each key orders by its Arango type rank
// (null < bool < number < string) then within-type value, and the identity
// last breaks the ties AQL leaves unspecified (registered determinism
// refinement, ASCII-bounded).
func pivotWindowOrder(keySlots []string, identity string, sortSeen bool) string {
	if sortSeen {
		return identity + ` COLLATE "C" ASC`
	}
	terms := make([]string, 0, 4*len(keySlots)+1)
	for _, key := range keySlots {
		terms = append(terms,
			"(CASE WHEN "+key+" IS NULL THEN 0 WHEN jsonb_typeof("+key+") = 'boolean' THEN 1"+
				" WHEN jsonb_typeof("+key+") = 'number' THEN 2 WHEN jsonb_typeof("+key+") = 'string' THEN 3 ELSE 4 END) ASC",
			"(CASE WHEN jsonb_typeof("+key+") = 'boolean' THEN "+key+"::text END) ASC",
			"((CASE WHEN jsonb_typeof("+key+") = 'number' THEN "+key+" #>> '{}' END)::double precision) ASC",
			"((CASE WHEN jsonb_typeof("+key+") = 'string' THEN "+key+" #>> '{}' END) COLLATE \"C\") ASC")
	}
	terms = append(terms, identity+` COLLATE "C" ASC`)
	return strings.Join(terms, ", ")
}

// ---------------------------------------------------------------------------
// Coded slice: contract
// ---------------------------------------------------------------------------

// validateGroupedCodedPivotPlanSurface is the single closed-slice contract
// behind both coded doors. It refuses every shape outside the registered
// inline single-stage CODED_PIVOT with ErrUnsupportedPostgresPlan before
// plan validation, scope loading, or any rendering can reject with an
// off-type error.
func validateGroupedCodedPivotPlanSurface(plan ir.PhysicalPlan) error {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 {
		return fmt.Errorf("%w: the coded-pivot slice requires exactly one PIVOT stage", ErrUnsupportedPostgresPlan)
	}
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: multi-stage construction sequences are the pg-sql-16 capability; the registered slice admits only the inline single-stage CODED_PIVOT",
			ErrUnsupportedPostgresPlan)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStagePivotOp {
		return fmt.Errorf("%w: the registered slice admits only a PIVOT stage", ErrUnsupportedPostgresPlan)
	}
	pivot := stage.GroupedPivot
	if pivot == nil {
		return fmt.Errorf("%w: PIVOT is missing its grouped-pivot payload (ir/physical_stage_validation.go re-read at the door)", ErrUnsupportedPostgresPlan)
	}
	if stage.Filter != nil || stage.Group != nil || stage.CodedGroup != nil || stage.CohortGroup != nil ||
		stage.Expand != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: the coded Pivot stage carries a second payload (ir/physical_stage_validation.go:99 re-read at the door)", ErrUnsupportedPostgresPlan)
	}
	if pivot.CodedCorrelation == nil || len(pivot.CodedCategories) == 0 {
		return fmt.Errorf("%w: the coded-pivot door admits only coded categories", ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage CODED_PIVOT (InputStageID must equal %q and the stage must be final); the DOCUMENT re-query shape is production-unreachable",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38); pivot terminals belong to pg-sql-23 through pg-sql-26",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		// The plan-level PreviewSourceWindowByRootID (ir/physical_types.go
		// :27) must be refused HERE: this door dispatches before the
		// spine's plan-level window gate, so it is the only place the
		// check can fire for a coded-pivot plan — the twin coded-group and
		// graph doors keep the same check (coded_group.go:440, graph.go:228).
		return fmt.Errorf("%w: the plan-level preview source window and the terminal Pivot window require Pivot-terminal windowing contracts that are not registered for the coded pivot", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewSourceWindowByRootID && sequence.PreviewLimitBindKey != "" {
		// The root-id preview fast path is probe-attested production-stamped
		// for direct-root coded pivots (lower/recipe_construction.go:476-478,
		// probe P-14.4) but only ACTIVE with an execution preview limit
		// (render/aql/stage_sequence.go:28 derives the AQL windowing state
		// from flag AND bind). The active combination rewrites the source
		// into a windowed-by-root-id scan
		// (compiler/output.go:72-88 insertConstructionPreviewRootIDWindow)
		// — a registered named divergence for pg-sql-16, never silently
		// rendered (coordinator ruling R3).
		return fmt.Errorf("%w: the active preview root-id source window combined with a preview limit is a pg-sql-16 capability and is refused, never silently rendered (probe P-14.4)",
			ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for the coded pivot (ir/physical_stage_sequence.go:25-28)", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: coded-pivot preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	if err := validateCodedPivotOperations(plan); err != nil {
		return err
	}
	return validateCodedPivotPayload(plan, stage, pivot)
}

// validateCodedPivotOperations enforces the closed source-region operation
// list (the validateCodedGroupOperations shape, coded_group.go:535-610):
// ROOT_SCAN@0, FILTER/DERIVED_LET before the window, at most one SORT then
// LIMIT, and a terminal RETURN. SET and EXPRESSION_LET are the
// probe-attested unreachable whitelist arms kept fail-closed (C6): the
// statement builder has no dispatch for them, so without these refusals a
// leaked shape would be silently dropped (the L-19.4 sabotage lesson).
func validateCodedPivotOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: the coded-pivot source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
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
				return fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN", ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalSetOp:
			return fmt.Errorf("%w: coded-pivot source-region SET is a pg-sql-16 capability gap (probe-attested production-unreachable: construction outputs reject the legacy shape operations that mint it, probe P-14.3) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalExpressionLetOp:
			return fmt.Errorf("%w: coded-pivot source-region EXPRESSION_LET is a pg-sql-16 capability gap (probe-attested production-unreachable, probe P-14.3) and is refused, never partially rendered",
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
				return fmt.Errorf("%w: the coded-pivot source requires a terminal RETURN with its projection list", ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
		default:
			return fmt.Errorf("%w: operation %q is not part of the registered coded-pivot source surface (coexisting shapes are pg-sql-16)",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	return nil
}

// validateCodedPivotPayload re-reads the coded pivot contract at the door
// so both doors refuse the same bind/payload defects with the same typed
// error before plan.Validate can speak (the validateCodedGroupPayload /
// coded_group.go:456-461 spellings; ir/physical_table_shape.go:237-301 is
// authoritative).
func validateCodedPivotPayload(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, pivot *ir.PhysicalGroupedPivot) error {
	if !pivot.OneInputRowPerGroup {
		return fmt.Errorf("%w: the registered CODED_PIVOT requires the producer-proven OneInputRowPerGroup (lower/recipe_construction_coded_pivot.go:73-74, recipe_construction.go:476-478)", ErrUnsupportedPostgresPlan)
	}
	if pivot.UnlistedCategoryPolicy != "IGNORE" {
		return fmt.Errorf("%w: the producer-stamped coded Pivot unlisted-category policy is IGNORE (ir/physical_table_shape.go); %q has no rendering",
			ErrUnsupportedPostgresPlan, pivot.UnlistedCategoryPolicy)
	}
	if !pivotDuplicatePolicies[pivot.DuplicatePolicy] {
		return fmt.Errorf("%w: coded pivot duplicate policy %q is not registered", ErrUnsupportedPostgresPlan, pivot.DuplicatePolicy)
	}
	if !pivotMissingPolicies[pivot.MissingCellPolicy] {
		return fmt.Errorf("%w: coded pivot missing-cell policy %q is not registered", ErrUnsupportedPostgresPlan, pivot.MissingCellPolicy)
	}
	if _, ok := pivotValueTypeOf[pivot.ValueType]; !ok {
		return fmt.Errorf("%w: coded pivot value type %q is not a registered table scalar kind", ErrUnsupportedPostgresPlan, pivot.ValueType)
	}
	if len(pivot.RowValues) != 0 {
		return fmt.Errorf("%w: coded pivot row values have no PostgreSQL rendering (registered named divergence: accepted at recipe level, emitted by the producer at lower/recipe_construction_coded_pivot.go:80-91, refused fail-closed here; row values are the pg-sql-13 capability)",
			ErrUnsupportedPostgresPlan)
	}
	if pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" || pivot.RootContributorVariable != "" {
		return fmt.Errorf("%w: coded pivot root-contributor columns have no rendering and the sole producer never writes them (C5)", ErrUnsupportedPostgresPlan)
	}
	if pivot.DuplicatePolicy != "ERROR" && pivot.ValueType != "INTEGER" && pivot.ValueType != "DECIMAL" {
		return fmt.Errorf("%w: coded pivot reducer %q requires a numeric value type", ErrUnsupportedPostgresPlan, pivot.DuplicatePolicy)
	}
	if pivot.UnlistedEvidenceColumn != "" {
		return fmt.Errorf("%w: the coded pivot (IGNORE policy) carries an evidence column", ErrUnsupportedPostgresPlan)
	}
	if len(pivot.GroupKeys) != 1 || pivot.GroupKeys[0].Column != "_key" || !pivot.GroupKeys[0].Hidden ||
		pivot.GroupKeys[0].Kind != "STRING" {
		return fmt.Errorf("%w: the coded Pivot must group by the retained hidden root _key (ir/physical_table_shape.go:238-241)", ErrUnsupportedPostgresPlan)
	}
	if value, ok := plan.BindVars[pivot.ConstructionIDBindKey].(string); !ok || value == "" {
		return fmt.Errorf("%w: coded pivot construction ID bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, pivot.ConstructionIDBindKey)
	}
	rootVariable := plan.Operations[0].RootScan.Variable
	if pivot.CodedSourceVariable != rootVariable {
		return fmt.Errorf("%w: the coded pivot must stream the scanned root variable %q, got %q",
			ErrUnsupportedPostgresPlan, rootVariable, pivot.CodedSourceVariable)
	}
	if len(pivot.InputProjections) != 2 {
		return fmt.Errorf("%w: the coded pivot source projection carries only the direct root key and payload (ir/physical_table_shape.go:246-258)", ErrUnsupportedPostgresPlan)
	}
	rootKey, payload := false, false
	for _, projection := range pivot.InputProjections {
		switch projection.Name {
		case "_key":
			rootKey = projection.Hidden && projection.Value.Variable == pivot.CodedSourceVariable &&
				len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key"
		case ir.PhysicalCodedPivotSourcePayloadColumn:
			payload = projection.Hidden && projection.Value.Variable == pivot.CodedSourceVariable &&
				len(projection.Value.Path) == 1 && projection.Value.Path[0] == "payload"
		default:
			return fmt.Errorf("%w: coded pivot input projection %q is outside the registered pair",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	if !rootKey || !payload {
		return fmt.Errorf("%w: the coded pivot _key and payload projections must read the scanned root variable (ir/physical_table_shape.go:247-258)", ErrUnsupportedPostgresPlan)
	}
	correlation := pivot.CodedCorrelation
	if correlation.Source.Variable != pivot.InputRowVariable ||
		len(correlation.Source.Path) != 1 || correlation.Source.Path[0] != ir.PhysicalCodedPivotSourcePayloadColumn {
		return fmt.Errorf("%w: the coded pivot correlation must read the staged payload from its input row (ir/physical_table_shape.go:259-262)", ErrUnsupportedPostgresPlan)
	}
	if len(correlation.ExtensionURLSelectors) != 0 || len(correlation.ExtensionURLBindKeys) != 0 {
		return fmt.Errorf("%w: extension-URL correlations are never lowered onto the coded pivot (lower/correlated_binding.go:690-706; probe P-14.5) and have no rendering (C10)",
			ErrUnsupportedPostgresPlan)
	}
	for name, selector := range map[string]*spec.Selector{
		"owner":  &correlation.OwnerSelector,
		"key":    &correlation.KeySelector,
		"system": &correlation.SystemSelector,
		"code":   &correlation.CodeSelector,
		"value":  &correlation.ValueSelector,
	} {
		if err := pivotSelectorOK(*selector); err != nil {
			return fmt.Errorf("%w: coded pivot correlation %s selector: %w", ErrUnsupportedPostgresPlan, name, err)
		}
	}
	// The (system, code) match renders as ONE property test on the coding
	// item (the AQL LET __coded_system/__coded_code over one FLATTENed
	// KeySelector member, table_reshape.go:369-373), so only a single
	// plain property step is renderable. ir admits the full selector
	// grammar here (physical_validation_refs.go:420-425 gates multi-step
	// only for SYSTEM_VERSION) and the AQL oracle would evaluate the
	// whole chain (renderCorrelationScalar, predicates.go:151-157);
	// rendering Steps[0] alone and dropping the rest would silently match
	// nothing, which the closed-slice contract forbids. Refuse at the
	// door (the producer lowers both selectors as single steps;
	// lower/correlated_binding.go).
	for _, entry := range []struct {
		name     string
		selector *spec.Selector
	}{
		{"system", &correlation.SystemSelector},
		{"code", &correlation.CodeSelector},
	} {
		selector := entry.selector
		if len(selector.Steps) != 1 || selector.Filter != nil ||
			selector.Steps[0].Iterate || selector.Steps[0].Index != nil {
			return fmt.Errorf("%w: coded pivot correlation %s selector must be one plain property step (the rendered match is a single property test on the coding item; multi-step, indexed, iterated, and filtered forms have no registered rendering, pg-sql-16)",
				ErrUnsupportedPostgresPlan, entry.name)
		}
	}
	for index, fallback := range correlation.ValueFallbacks {
		if err := pivotSelectorOK(fallback); err != nil {
			return fmt.Errorf("%w: coded pivot value fallback %d: %w", ErrUnsupportedPostgresPlan, index, err)
		}
	}
	for index, choice := range correlation.ChoiceSelectors {
		if err := pivotSelectorOK(choice); err != nil {
			return fmt.Errorf("%w: coded pivot choice selector %d: %w", ErrUnsupportedPostgresPlan, index, err)
		}
	}
	if correlation.VersionSelector != nil && strings.TrimSpace(correlation.VersionBindKey) == "" {
		return fmt.Errorf("%w: a hand-carried correlation version selector requires its bind (ir re-read; production never carries version on the pivot — probe P-14.5)", ErrUnsupportedPostgresPlan)
	}
	// VersionSelector/VersionBindKey and ValueScope/SelectionScope are
	// deliberately NOT consulted by the rendering (C11/C12): the coded
	// pivot ground truth renderCodedPivotCategoryValues reads values from
	// the owner regardless (table_reshape.go:360/:364; the version clause
	// is predicate-only, correlation_value_scope.go:292-301). Goldens pin
	// byte-identical rendering with and without them.
	if len(pivot.CodedCategories) == 0 || len(pivot.CodedCategories) > 50 {
		return fmt.Errorf("%w: the coded pivot registers 1..50 frozen categories", ErrUnsupportedPostgresPlan)
	}
	outputs, identities := map[string]bool{}, map[string]bool{}
	for index, category := range pivot.CodedCategories {
		if category.Output == "" || outputs[category.Output] {
			return fmt.Errorf("%w: coded pivot category %d output is empty or duplicated", ErrUnsupportedPostgresPlan, index)
		}
		outputs[category.Output] = true
		if category.Output == "_key" || category.Output == pivotRowIDColumn {
			return fmt.Errorf("%w: coded pivot category %d collides with the root identity", ErrUnsupportedPostgresPlan, index)
		}
		for _, bindKey := range []string{category.SystemBindKey, category.CodeBindKey} {
			value, ok := plan.BindVars[bindKey].(string)
			if !ok || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
				return fmt.Errorf("%w: coded pivot category %d bind %q must be an exact non-empty string",
					ErrUnsupportedPostgresPlan, index, bindKey)
			}
		}
		system, _ := plan.BindVars[category.SystemBindKey].(string)
		code, _ := plan.BindVars[category.CodeBindKey].(string)
		pair := system + "\x00" + code
		if identities[pair] {
			return fmt.Errorf("%w: coded pivot category %d duplicates a bound system/code pair", ErrUnsupportedPostgresPlan, index)
		}
		identities[pair] = true
	}
	if correlation.SystemBindKey != pivot.CodedCategories[0].SystemBindKey ||
		correlation.CodeBindKey != pivot.CodedCategories[0].CodeBindKey {
		return fmt.Errorf("%w: the shared correlation must bind the first category (ir/physical_table_shape.go:260-262)", ErrUnsupportedPostgresPlan)
	}
	// Final columns: hidden _key, the category outputs, hidden identity.
	names := map[string]bool{"_key": true, pivotRowIDColumn: true}
	seen := map[string]bool{}
	for _, column := range plan.StageSequence.FinalColumns {
		if seen[column.Name] {
			return fmt.Errorf("%w: coded pivot final column %q is declared twice", ErrUnsupportedPostgresPlan, column.Name)
		}
		seen[column.Name] = true
		if !(names[column.Name] || outputs[column.Name]) {
			return fmt.Errorf("%w: coded pivot final column %q is not a registered tuple member",
				ErrUnsupportedPostgresPlan, column.Name)
		}
	}
	if !seen["_key"] || !seen[pivotRowIDColumn] || len(seen) != len(pivot.CodedCategories)+2 {
		return fmt.Errorf("%w: the registered slice emits the hidden root identity, every category column, and the hidden stable identity", ErrUnsupportedPostgresPlan)
	}
	if stage.OutputRowVariable == "" || stage.RowIdentityColumn == "" {
		return fmt.Errorf("%w: the coded pivot stage must name its output row and identity columns", ErrUnsupportedPostgresPlan)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Coded slice: door + statement
// ---------------------------------------------------------------------------

func renderGroupedCodedPivotPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGroupedCodedPivotPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderGroupedCodedPivotStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// renderGroupedCodedPivotStatement builds the coded pivot:
//
//	WITH cp_src     AS (<root scan streamed as (cp_key, cp_payload) + W1 window>),
//	     cp_owner    AS (owner expansion over each root payload),
//	     cp_cat_<i>  AS (matched owners with the fallback-chain value array,
//	                     the INVALID_CHOICE_ARM assert on matched owners),
//	     cp_elem_<i> AS (per-category value elements),
//	     cp_agg_<i>  AS (per-root raw/typed counts, mismatch, FIRST, reducers)
//	SELECT <_key>, <cells>, <identity> FROM cp_src LEFT JOIN cp_agg_<i> …
//	WHERE <ordered guard chain> ORDER BY identity COLLATE "C" [LIMIT $n]
//
// The (system, code) match runs on ONE Coding item (the AQL FILTER chain of
// renderCodedPivotCategoryValues:338-389): system and code must be strings,
// non-empty (exact, untrimmed — the coded pivot does not trim for matching;
// only the blank-string value fallback mode trims, through the ONE
// codedGroupTrimCharacters), and equal to the bound category pair.
func (r *planRenderer) renderGroupedCodedPivotStatement(plan ir.PhysicalPlan) (string, error) {
	sequence := plan.StageSequence
	stage := sequence.Stages[0]
	pivot := stage.GroupedPivot
	correlation := pivot.CodedCorrelation

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
			if !canonical {
				return "", fmt.Errorf("%w: a non-canonical FILTER cannot coexist with the registered coded pivot (renders-at-AQL divergence, pg-sql-16)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered coded-pivot source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
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

	resource := r.storage.Resource
	source := "SELECT " + r.rootAlias + "." + resource.MustColumn("logical_id").SQLText() +
		` AS cp_key, ` + r.rootAlias + "." + resource.MustColumn("payload").SQLText() + ` AS cp_payload` +
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

	valueTypeOf := quoteSQLLiteral(pivotValueTypeOf[pivot.ValueType])

	// --- owner expansion (renderCorrelationOwners, predicates.go:120-145):
	// an empty owner selector makes the payload itself the single owner; a
	// selector expands over the root payload, and roots with no owners
	// contribute no values (their cells fall through the LEFT JOINs). ---
	ownerFrom := ""
	ownerItem := "s.cp_payload"
	ownerWhere := ""
	if correlation.OwnerSelector.CanonicalPath() != "" {
		from, item, _, where, err := r.pivotSelectorChain("s.cp_payload", correlation.OwnerSelector, "ow")
		if err != nil {
			return "", err
		}
		ownerFrom, ownerItem, ownerWhere = from, item, pivotWhereJoin(where)
	}
	owners := "SELECT s.cp_key, " + ownerItem + " AS cp_owner FROM cp_src AS s" + ownerFrom + ownerWhere

	ctes := []string{"cp_src AS (" + source + ")", "cp_owner AS (" + owners + ")"}
	aggJoins := make([]string, 0, len(pivot.CodedCategories))
	cellExpressions := make([]string, 0, len(pivot.CodedCategories))
	guardTerms := make([]string, 0, 2*len(pivot.CodedCategories))

	for index, category := range pivot.CodedCategories {
		tag := strconv.Itoa(index)
		systemValue := bindStringVar(plan.BindVars, category.SystemBindKey)
		codeValue := bindStringVar(plan.BindVars, category.CodeBindKey)
		systemParameter := pivotDollar(r.textParameter(systemValue, "pivot:coded-system:"+systemValue))
		codeParameter := pivotDollar(r.textParameter(codeValue, "pivot:coded-code:"+codeValue))
		systemPath := correlation.SystemSelector.Steps[0].Field
		codePath := correlation.CodeSelector.Steps[0].Field
		systemKey := pivotDollar(r.textParameter(systemPath, "pivot:prop:"+systemPath))
		codeKey := pivotDollar(r.textParameter(codePath, "pivot:prop:"+codePath))

		// Coding existence on ONE item (the AQL FILTER __coded_system ==
		// @system FILTER __coded_code == @code over FLATTEN(KeySelector)).
		codingFrom, codingItem, _, codingWhere, err := r.pivotSelectorChain("o.cp_owner", correlation.KeySelector, "cd"+tag)
		if err != nil {
			return "", err
		}
		exists := "EXISTS (SELECT 1 FROM (SELECT 1) AS " + "cd" + tag + "_hub" + codingFrom +
			" WHERE jsonb_typeof(" + codingItem + " -> " + systemKey + "::text) = 'string'" +
			" AND " + codingItem + " ->> " + systemKey + "::text <> ''" +
			" AND " + codingItem + " ->> " + systemKey + "::text = " + systemParameter +
			" AND jsonb_typeof(" + codingItem + " -> " + codeKey + "::text) = 'string'" +
			" AND " + codingItem + " ->> " + codeKey + "::text <> ''" +
			" AND " + codingItem + " ->> " + codeKey + "::text = " + codeParameter +
			pivotCodingExtraWhere(codingWhere) + ")"

		// Unsupported choice arms on MATCHED owners only (the AQL
		// renderCorrelationUnsupportedChoiceValues + ASSERT position:
		// after the match filter, before value emptiness). The badness is
		// carried as DATA (cp_bad) and raised only in the single ordered
		// outer guard chain below — raising inside this CTE would let the
		// per-category CTE materialization outrank guards that AQL's lazy
		// category LETs evaluate first (table_reshape.go:384 forces the
		// category's owner-loop ASSERT ahead of that category's cell
		// guards and strictly before the next category), and PostgreSQL
		// fixes no order among the CTEs.
		unsupported, err := r.pivotUnsupportedChoiceValues(correlation, "o.cp_owner", "cs"+tag)
		if err != nil {
			return "", err
		}

		// Value fallback chain (renderCorrelationValues, predicates.go
		// :159-175): FIRST over [ValueSelector, ValueFallbacks...] with the
		// non-empty (or non-blank-string) eligibility gate, then FLATTEN.
		values, err := r.pivotCodedValues(correlation, "o.cp_owner", "cv"+tag)
		if err != nil {
			return "", err
		}

		matchCTE := "cp_cat_" + tag + " AS (" +
			"SELECT * FROM (SELECT o.cp_key, " + exists + " AS cp_m, " +
			"(jsonb_array_length(" + unsupported + ")) > 0 AS cp_bad, " + values + " AS cp_vals" +
			" FROM cp_owner AS o) t" +
			" WHERE t.cp_m)"
		elemCTE := "cp_elem_" + tag + " AS (" +
			"SELECT m.cp_key, e.cp_e, e.ord FROM cp_cat_" + tag + " AS m" +
			" CROSS JOIN LATERAL jsonb_array_elements(m.cp_vals) WITH ORDINALITY AS e(cp_e, ord))"
		aggCTE := "cp_agg_" + tag + " AS (" +
			"SELECT k.cp_key, k.cp_cnt," +
			" COALESCE(x.cp_tcnt, 0) AS cp_tcnt, COALESCE(x.cp_mm, false) AS cp_mm," +
			" x.cp_first, x.cp_sum, x.cp_min, x.cp_max, k.cp_bad" +
			" FROM (SELECT cp_key, SUM(jsonb_array_length(cp_vals)) AS cp_cnt," +
			" BOOL_OR(cp_bad) AS cp_bad FROM cp_cat_" + tag + " GROUP BY cp_key) AS k" +
			" LEFT JOIN (SELECT cp_key," +
			" COUNT(*) FILTER (WHERE e.cp_e <> 'null'::jsonb) AS cp_tcnt," +
			" BOOL_OR(e.cp_e <> 'null'::jsonb AND jsonb_typeof(e.cp_e) <> " + valueTypeOf + ") AS cp_mm," +
			" (ARRAY_AGG(e.cp_e ORDER BY e.ord))[1] AS cp_first," +
			// Pre-cast type guard (F-2): cp_mm raises the canonical
			// TABLE_PIVOT_VALUE_TYPE_MISMATCH tag in the outer chain; the
			// aggregate must not die untagged on an uncastable string
			// first (AQL asserts the type per item before reducing,
			// table_reshape.go:376/:252).
			" SUM(CASE WHEN jsonb_typeof(e.cp_e) = " + valueTypeOf + " THEN " + pivotScalarText("e.cp_e") + "::double precision END) FILTER (WHERE e.cp_e <> 'null'::jsonb) AS cp_sum," +
			" MIN(CASE WHEN jsonb_typeof(e.cp_e) = " + valueTypeOf + " THEN " + pivotScalarText("e.cp_e") + "::double precision END) FILTER (WHERE e.cp_e <> 'null'::jsonb) AS cp_min," +
			" MAX(CASE WHEN jsonb_typeof(e.cp_e) = " + valueTypeOf + " THEN " + pivotScalarText("e.cp_e") + "::double precision END) FILTER (WHERE e.cp_e <> 'null'::jsonb) AS cp_max" +
			" FROM cp_elem_" + tag + " AS e GROUP BY cp_key) AS x ON x.cp_key = k.cp_key)"
		ctes = append(ctes, matchCTE, elemCTE, aggCTE)

		alias := "a" + tag
		aggJoins = append(aggJoins, " LEFT JOIN cp_agg_"+tag+" AS "+alias+" ON "+alias+".cp_key = s.cp_key")

		// Category-local guard order mirrors the AQL projection: the
		// owner-loop choice-arm ASSERT (table_reshape.go:384) is forced
		// with the category's values LET, before that category's cell
		// guards, and the categories themselves are evaluated in
		// declared order — one CASE chain preserves all competing
		// winners (registered mapping, header :45-48).
		guardTerms = append(guardTerms, "WHEN COALESCE("+alias+".cp_bad, false) THEN "+guardCast(pivotChoiceArmTag))
		cnt := "COALESCE(" + alias + ".cp_cnt, 0)"
		typedCnt := "COALESCE(" + alias + ".cp_tcnt, 0)"
		cell := ""
		switch pivot.DuplicatePolicy {
		case "ERROR":
			// The coded ERROR branch guards on the RAW count as well
			// (table_reshape.go:252-258); FIRST is null-preserving.
			cell = "COALESCE(" + alias + ".cp_first, 'null'::jsonb)"
			if pivot.MissingCellPolicy == "ERROR" {
				guardTerms = append(guardTerms, "WHEN "+cnt+" = 0 THEN "+guardCast(pivotCellMissingTag))
			}
			guardTerms = append(guardTerms, "WHEN "+cnt+" > 1 THEN "+guardCast(pivotCardinalityTag))
		default:
			// The coded reducer branch conditions MISSING on the TYPED
			// count — the verbatim asymmetry against the ordinary branch
			// (table_reshape.go:267 vs :230).
			reducerColumn := map[string]string{"SUM": "cp_sum", "MIN": "cp_min", "MAX": "cp_max"}[pivot.DuplicatePolicy]
			cell = "(CASE WHEN " + typedCnt + " = 0 THEN 'null'::jsonb ELSE to_jsonb(" + alias + "." + reducerColumn + ") END)"
			if pivot.MissingCellPolicy == "ERROR" {
				guardTerms = append(guardTerms, "WHEN "+typedCnt+" = 0 THEN "+guardCast(pivotCellMissingTag))
			}
			guardTerms = append(guardTerms, "WHEN COALESCE("+alias+".cp_mm, false) THEN "+guardCast(pivotValueTypeTag))
		}
		cellExpressions = append(cellExpressions, cell)
	}

	keyExpr := "to_jsonb(s.cp_key::text)"
	identity := r.pivotIdentitySQL(bindStringVar(plan.BindVars, pivot.ConstructionIDBindKey),
		[]string{pivot.GroupKeys[0].Kind}, []string{keyExpr})

	expressions := map[string]string{"_key": keyExpr, pivotRowIDColumn: identity}
	for index, category := range pivot.CodedCategories {
		expressions[category.Output] = cellExpressions[index]
	}
	selects := make([]string, 0, len(sequence.FinalColumns))
	names := make([]string, 0, len(sequence.FinalColumns))
	for _, column := range sequence.FinalColumns {
		expression, ok := expressions[column.Name]
		if !ok {
			return "", fmt.Errorf("%w: coded pivot final column %q has no rendered expression",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, column.Name)
	}
	r.outputColumns = names

	statement := "WITH " + strings.Join(ctes, ", ") +
		" SELECT " + strings.Join(selects, ", ") + " FROM cp_src AS s" +
		strings.Join(aggJoins, "")
	if len(guardTerms) > 0 {
		statement += " WHERE (CASE " + strings.Join(guardTerms, " ") + " ELSE 1 END) = 1"
	}
	statement += ` ORDER BY ` + identity + ` COLLATE "C" ASC`
	if sequence.PreviewLimitBindKey != "" {
		limit := plan.BindVars[sequence.PreviewLimitBindKey].(int)
		limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		statement += " LIMIT " + pivotDollar(limitIndex)
	}
	return statement, nil
}

// pivotCodingExtraWhere appends the coding chain's own conjuncts (a
// CONTAINS filter on the KeySelector) to the EXISTS WHERE.
func pivotCodingExtraWhere(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " AND " + strings.Join(where, " AND ")
}

// pivotUnsupportedChoiceValues renders the FLATTEN([parts]) of the values
// stored on choice arms OUTSIDE the checked binding
// (renderCorrelationUnsupportedChoiceValues, predicates.go:185-207): each
// ChoiceSelector whose first step is not a declared arm contributes its
// expanded items. The result is always a jsonb array; only its LENGTH is
// consumed (by the INVALID_CHOICE_ARM guard).
func (r *planRenderer) pivotUnsupportedChoiceValues(correlation *ir.PhysicalCorrelation, base string, name string) (string, error) {
	if len(correlation.ChoiceSelectors) == 0 || len(correlation.ChoiceArms) == 0 {
		return "'[]'::jsonb", nil
	}
	parts := make([]string, 0, len(correlation.ChoiceSelectors))
	for index, selector := range correlation.ChoiceSelectors {
		if len(selector.Steps) == 0 || pivotChoiceArmAllowed(selector.Steps[0].Field, correlation.ChoiceArms) {
			continue
		}
		array, err := r.pivotSelectorArray(base, selector, name+strconv.Itoa(index))
		if err != nil {
			return "", err
		}
		parts = append(parts, array)
	}
	if len(parts) == 0 {
		return "'[]'::jsonb", nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	elements := make([]string, 0, len(parts))
	for _, part := range parts {
		elements = append(elements,
			"SELECT u.x FROM jsonb_array_elements(CASE WHEN jsonb_typeof("+part+") = 'array' THEN "+part+" ELSE '[]'::jsonb END) AS u(x)")
	}
	return "(SELECT COALESCE(jsonb_agg(w.x), '[]'::jsonb) FROM (" + strings.Join(elements, " UNION ALL ") + ") AS w)", nil
}

func pivotChoiceArmAllowed(arm string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == strings.TrimSpace(arm) {
			return true
		}
	}
	return false
}

// pivotCodedValues renders the value candidate chain of
// renderCorrelationValues (:159-175) plus the category-layer FLATTEN:
// FIRST over [ValueSelector, ValueFallbacks...] of the candidate arrays
// whose LENGTH is non-empty (or, in FallbackOnBlankString mode, whose
// items include a string that survives the ONE trim set), with the pick
// pinned to declared order by an explicit ordinal, then the chosen
// candidate FLATTENed ONE level exactly where the oracle does
// (LET __coded_values = FLATTEN(chosen), table_reshape.go:385): a
// top-level item that is itself an array splices into its items, while
// arrays nested below stay items — AQL FLATTEN is one level, the bound
// the package documents elsewhere (calls.go:1592-1594,
// unnest.go:61-62). The per-owner FLATTEN (:369) is mirrored by the
// cp_elem expansion in the statement builder. A matched owner whose
// candidates are all ineligible — or whose single candidate is [[]],
// flattening to empty — contributes nothing (the AQL FILTER
// LENGTH(__coded_values) > 0).
func (r *planRenderer) pivotCodedValues(correlation *ir.PhysicalCorrelation, base string, name string) (string, error) {
	candidates := make([]spec.Selector, 0, 1+len(correlation.ValueFallbacks))
	candidates = append(candidates, correlation.ValueSelector)
	candidates = append(candidates, correlation.ValueFallbacks...)
	arrays := make([]string, 0, len(candidates))
	for index, selector := range candidates {
		array, err := r.pivotSelectorArray(base, selector, name+strconv.Itoa(index))
		if err != nil {
			return "", err
		}
		arrays = append(arrays, array)
	}
	chosen := arrays[0]
	if len(arrays) > 1 {
		eligible := "(jsonb_typeof(c.cand) = 'array' AND jsonb_array_length(c.cand) > 0)"
		if correlation.FallbackOnBlankString {
			eligible = "(jsonb_typeof(c.cand) = 'array' AND EXISTS (" +
				"SELECT 1 FROM jsonb_array_elements(c.cand) AS b(x)" +
				" WHERE jsonb_typeof(b.x) = 'string'" +
				" AND " + r.codedGroupTrimmedNonBlank("b.x #>> '{}'") + "))"
		}
		// The ordinal column pins FIRST-in-declared-order (the AQL
		// FIRST(FOR __correlation_candidate IN [a1, a2, ...] FILTER
		// eligible RETURN candidate), predicates.go:177): a VALUES list
		// carries no row-order guarantee, so LIMIT 1 without ORDER BY
		// could let a later eligible fallback win under a planner
		// change and silently change cell values.
		rows := make([]string, 0, len(arrays))
		for index, array := range arrays {
			rows = append(rows, "("+strconv.Itoa(index+1)+",("+array+"))")
		}
		chosen = "COALESCE((SELECT c.cand FROM (VALUES " + strings.Join(rows, ",") +
			") AS c(ord, cand) WHERE " + eligible + " ORDER BY c.ord LIMIT 1), '[]'::jsonb)"
	}
	// FLATTEN at the chosen-candidate layer (table_reshape.go:385):
	// splice each top-level item one level (non-items pass through via
	// jsonb_build_array), aggregating in document order — outer ordinal
	// first, spliced-item ordinal second.
	return "(SELECT COALESCE(jsonb_agg(s.cp_e ORDER BY f.ord, s.ord1), '[]'::jsonb)" +
		" FROM jsonb_array_elements(CASE WHEN jsonb_typeof(" + chosen + ") = 'array' THEN " + chosen + " ELSE '[]'::jsonb END)" +
		" WITH ORDINALITY AS f(x, ord)" +
		" CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(f.x) = 'array' THEN f.x" +
		" ELSE jsonb_build_array(f.x) END) WITH ORDINALITY AS s(cp_e, ord1))", nil
}
