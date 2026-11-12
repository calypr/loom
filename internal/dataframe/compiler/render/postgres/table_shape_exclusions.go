package postgres

// table_shape_exclusions.go registers the table-shape excluded-row evidence
// terminal on the PostgreSQL render contract (pg-sql-26). It is the producer
// half of the typed evidence request the compiler already builds at
// compiler/table_shape_exclusions.go: the pivot keeps the per-group unlisted
// COUNT (pivot.go:1273-1278, which named this ticket) and this file renders
// the per-row evidence RETURN itself.
//
// AQL ground truth, ported branch-for-branch with the diff receipts cited at
// each asserted branch:
//   - render/aql/table_shape_exclusion.go:10-135 renderTableShapeExclusionReturn
//     (input document, unlisted FILTER, identity LET, the seven-term SORT, the
//     OFFSET/FETCH window, and the nine-field RETURN).
//   - render/aql/table_shape_exclusion.go:137-150 renderGroupedPivotIdentity,
//     which this file reaches through the SAME emitter the pivot uses,
//     r.pivotIdentitySQL (pivot.go:406-415), so the evidence row identity is
//     structurally identical to the pivot's own __loom_row_id rather than a
//     second port of it.
//   - render/aql/table_reshape.go:391-435 groupedPivotListedPredicate /
//     groupedPivotCategoryMatchPredicate / groupedPivotCategoryMatchExpression
//     (the listed predicate), rendered here in this package's jsonb spelling,
//     which is itself the pivot's registered spelling (pivot.go:1164-1189).
//     Reusing the predicate rather than the pivot's private copy keeps this
//     terminal's own contract against AQL; the live cross-check leg pins the
//     two against each other (per group, evidence rows == the pivot's unlisted
//     count), so a divergence in either port fails rather than silently
//     agreeing on a wrong answer.
//
// The terminal reads ONLY what the evidence needs: the category column, the
// presence companion when input-carried, the group-key columns, and the stable
// row tie-breaker. The pivot's value column is never read — the AQL terminal
// never touches it (its SORT has no value term), so no cell reducer,
// cardinality, or value-type guard can perturb evidence rows.
//
// Registered mappings (each pinned by a unit golden and a live leg):
//   - Unlisted filter: WHERE NOT COALESCE(listed, false). PostgreSQL's
//     three-valued NOT would silently drop a row whose category read is SQL
//     NULL, exactly the case AQL's two-valued FILTER NOT listed keeps
//     (pivot.go:1213-1217 carries the same reasoning for the count).
//   - Category presence: the pivot region's derivation verbatim
//     (pivot.go:1116-1159) — presence contract CASE over the root document,
//     the COALESCE'd input companion, or the null-behavior table
//     (PRESERVE_NULL/EMPTY_ON_NULL always-present, OMIT_NULLS IS NOT NULL).
//     The evidence category_present field therefore equals AQL's
//     HAS(input, @category) for every registered producer form.
//   - identity_available: COALESCE over the IS_OBJECT/IS_STRING/non-empty
//     conjunction (table_shape_exclusion.go:75), because a SQL NULL document
//     reads NULL there where AQL's IS_OBJECT(null) is false.
//   - SORT: the AQL seven-term order verbatim — identity, then
//     identity_available DESC, resourceType, id, category_present,
//     category_value, stable row column. Text members order by the Arango
//     type rank plus within-type value under COLLATE "C", the same registered
//     spelling as pivotWindowOrder (pivot.go:1343-1358); the identity orders
//     under COLLATE "C" alone (the D-19a ASCII-agreement bound).
//   - Document-key reads: a consumed single-segment root "_key" or root "id"
//     reads the storage identity column through canonicalKeyColumn
//     (render.go:171-184) as to_jsonb(col::text), the package's registered
//     mapping for that reference (objects.go:739, window.go:221). This is
//     load-bearing here and nowhere else in the pivot family: the tie-breaker
//     the evidence SORT needs IS that column for the table-reshape producer,
//     and a payload-path read of it would be SQL NULL for every row, which
//     silently removes the deterministic page order the acceptance contract
//     names (the live paging legs pin it).
//   - The document: AQL reaches it through the injected
//     __loom_trace_source_document field (table_reshape.go:629-660 merges the
//     root payload into the input row). This terminal is a stored-row query,
//     so it reads the trusted payload column through null_semantics.go's
//     documentColumn — the same bytes the merged field carries. Where a
//     payload is not a JSON object AQL's attribute access raises while SQL
//     reads NULL: such rows are un-ingestable at the store boundary (the
//     banked Arango receipt class), and the row still lands as
//     identity_status UNAVAILABLE with the source-identity omission.
//
// Refusal register (each an ir re-read, a pivot-door refusal reused verbatim,
// or a named capability gap — never a silent drop):
//   - X1 operations outside [ROOT_SCAN@0, FILTER*/DERIVED_LET*, terminal@last]:
//     the compiler truncates the plan at the pivot when it mints this terminal
//     (compiler/table_shape_exclusions.go:129-133), so a SORT, LIMIT, RETURN,
//     second reshape, or any spine operation reaching here is a hand-carried
//     shape the statement builder has no dispatch for. Fail-closed, the
//     L-19.4 sabotage lesson (pivot.go:1440-1444).
//   - X2 stage sequence, plan-level preview root-id window, engine,
//     ClickHouse payloads, deferred lets: the pivot door's own refusals
//     (pivot.go:441-452, 894-906), re-read at this door because it dispatches
//     before the spine gates.
//   - X3 pivot payload: validateGroupedPivotPayload runs unchanged
//     (pivot.go:646-765), then this door additionally requires
//     EXCLUDE_WITH_EVIDENCE and a stable row tie-breaker.
//   - X4 bind contract: the three terminal binds exist as ints with
//     offset >= 0, limit > 0, and fetchLimit == limit+1, the ir validation
//     (ir/physical_validation.go validatePhysicalTableShapeExclusionReturn)
//     re-read at the renderer.
//   - X5 a consumed column with no renderable input projection:
//     validatePivotInputProjections plus this door's stable-row check.
//   - X6 population/cohort/candidate-seed roots: validatePivotRootScan
//     verbatim (pivot.go:550-566).
//   - X7 a non-canonical FILTER or DERIVED_LET: renders-at-AQL, refused here
//     exactly as the pivot statement builder does (pivot.go:951-969).
//
// Planner-facing command registration stays fail-closed; this entry point —
// not command dispatch — is where the terminal is implemented and registered
// (the pivot.go:142-144 precedent).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// exclusionSlotPrefix names this terminal's source slots. It is distinct from
// the pivot's "pv_" so the two regions never collide by construction.
const exclusionSlotPrefix = "px_"

// tableShapeExclusionPlanMentionsSurface is the door predicate: a plan
// mentioning the evidence terminal is the authored diagnostic shape and is
// rendered WHOLE by this registered slice.
func tableShapeExclusionPlanMentionsSurface(plan ir.PhysicalPlan) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalTableShapeExclusionReturnOp {
			return true
		}
	}
	return false
}

// RenderTableShapeExclusions renders exactly one scoped direct root scan
// followed by the excluded-row evidence terminal onto the trusted PostgreSQL
// storage binding (the RenderGroupedPivot front-door shape, pivot.go:188-193).
// Entry and dispatch share one validator, so the two voices are equal.
func RenderTableShapeExclusions(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	return renderTableShapeExclusionPhysicalPlan(plan, storage)
}

func renderTableShapeExclusionPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateTableShapeExclusionPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderTableShapeExclusionStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// ---------------------------------------------------------------------------
// Contract
// ---------------------------------------------------------------------------

// validateTableShapeExclusionPlanSurface is the single closed-slice contract
// behind both the door and the RenderPhysicalPlan dispatch arm. It is total
// over any plan mentioning the evidence terminal and refuses every shape
// outside the registered slice with ErrUnsupportedPostgresPlan before plan
// validation, scope loading, or any rendering can reject with an off-type
// error (the validateGroupedPivotPlanSurface spelling, pivot.go:426-545).
func validateTableShapeExclusionPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	// The leading ROOT_SCAN is required, not optional: the statement builder
	// dereferences Operations[0].RootScan, so without this front check a
	// hand-carried plan would reach a nil dereference instead of a typed
	// refusal (pivot.go:430-440, the same payload-nil discipline).
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: the exclusion slice leads with exactly one ROOT_SCAN at position 0", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return fmt.Errorf("%w: an exclusion terminal combined with a stage sequence has no PostgreSQL rendering (staged compositions are pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		// This door dispatches before the spine's plan-level window gate, so
		// it is the only place the check can fire for an exclusion plan
		// (pivot.go:444-452, ruling R3: never silently rendered).
		return fmt.Errorf("%w: the plan-level preview source window requires terminal windowing contracts that are not registered for the exclusion terminal (pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	var terminal *ir.PhysicalTableShapeExclusionReturn
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the exclusion slice leads with exactly one ROOT_SCAN at position 0, got one at %d",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: ROOT_SCAN is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := validatePivotRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return fmt.Errorf("%w: FILTER is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return fmt.Errorf("%w: DERIVED_LET is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalTableShapeExclusionReturnOp:
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: the evidence terminal must be the final operation, got position %d of %d",
					ErrUnsupportedPostgresPlan, index, len(plan.Operations))
			}
			if operation.TableShapeExclusionReturn == nil {
				return fmt.Errorf("%w: the evidence terminal is missing its payload", ErrUnsupportedPostgresPlan)
			}
			terminal = operation.TableShapeExclusionReturn
		default:
			// X1: the compiler truncates the plan at the pivot when it mints
			// this terminal, so every other operation is hand-carried and has
			// no dispatch arm here — a SORT or LIMIT would be silently dropped
			// rather than applied twice, because the terminal owns its window.
			return fmt.Errorf("%w: operation %q cannot coexist with the evidence terminal in the registered slice; the terminal owns its own SORT and OFFSET/LIMIT window, and every other shape belongs to the read spine or the pg-sql-16 composition capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if terminal == nil {
		return fmt.Errorf("%w: the exclusion slice requires a terminal evidence operation after ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	rootVariable := plan.Operations[0].RootScan.Variable
	if err := validateTableShapeExclusionBinds(plan, *terminal); err != nil {
		return err
	}
	pivot := terminal.Pivot
	if pivot.UnlistedCategoryPolicy != string(ir.PhysicalPivotUnlistedCategoryExcludeWithEvidence) {
		return fmt.Errorf("%w: the evidence terminal exists exactly under the EXCLUDE_WITH_EVIDENCE policy, got %q (ir/physical_validation.go re-read; the compiler entry refuses any other policy with TABLE_SHAPE_EXCLUSION_NO_POLICY)",
			ErrUnsupportedPostgresPlan, pivot.UnlistedCategoryPolicy)
	}
	// X3: the pivot payload contract is the pivot door's own, unchanged, so a
	// shape is never legal for the count and illegal for the evidence rows.
	if err := validateGroupedPivotPayload(plan, pivot, rootVariable); err != nil {
		return err
	}
	// X5: the tie-breaker the evidence SORT needs, and the one AQL refuses
	// without (table_shape_exclusion.go:84-95).
	if _, err := exclusionStableRowColumn(pivot); err != nil {
		return err
	}
	return nil
}

// validateTableShapeExclusionBinds re-reads the window contract the ir
// validator enforces (X4): three int binds, offset non-negative, limit
// positive, and the probe row bound exactly limit+1, so the executor's
// has-more probe and the SQL LIMIT agree by construction.
func validateTableShapeExclusionBinds(plan ir.PhysicalPlan, terminal ir.PhysicalTableShapeExclusionReturn) error {
	bind := func(key, field string) (int, error) {
		if strings.TrimSpace(key) == "" {
			return 0, fmt.Errorf("%w: the evidence terminal has no %s bind key", ErrUnsupportedPostgresPlan, field)
		}
		value, ok := plan.BindVars[key].(int)
		if !ok {
			return 0, fmt.Errorf("%w: evidence %s bind %q must be an int", ErrUnsupportedPostgresPlan, field, key)
		}
		return value, nil
	}
	offset, err := bind(terminal.OffsetBindKey, "offset")
	if err != nil {
		return err
	}
	limit, err := bind(terminal.LimitBindKey, "limit")
	if err != nil {
		return err
	}
	fetchLimit, err := bind(terminal.FetchLimitBindKey, "fetch limit")
	if err != nil {
		return err
	}
	if offset < 0 {
		return fmt.Errorf("%w: evidence offset must be non-negative, got %d", ErrUnsupportedPostgresPlan, offset)
	}
	if limit <= 0 {
		return fmt.Errorf("%w: evidence limit must be positive, got %d", ErrUnsupportedPostgresPlan, limit)
	}
	if fetchLimit != limit+1 {
		return fmt.Errorf("%w: evidence fetch limit must be limit+1 (%d), got %d",
			ErrUnsupportedPostgresPlan, limit+1, fetchLimit)
	}
	return nil
}

// exclusionStableRowColumn picks the deterministic tie-breaker the final SORT
// needs, the AQL rule verbatim (table_shape_exclusion.go:84-95): the stable
// generated row id wins, otherwise the stored document key; without either,
// page order across equal evidence rows is not deterministic and the shape is
// refused rather than rendered unstable.
func exclusionStableRowColumn(pivot ir.PhysicalGroupedPivot) (string, error) {
	stable := ""
	for _, projection := range pivot.InputProjections {
		if projection.Name == pivotRowIDColumn {
			return pivotRowIDColumn, nil
		}
		if projection.Name == "_key" {
			stable = projection.Name
		}
	}
	if stable == "" {
		return "", fmt.Errorf("%w: table-shape exclusion input has no stable row tie-breaker (%s or _key)",
			ErrUnsupportedPostgresPlan, pivotRowIDColumn)
	}
	return stable, nil
}

// exclusionConsumedInputNames lists exactly the input projections this
// terminal reads: the category column, the presence companion only when
// input-carried, every group-key column, and the stable row tie-breaker. The
// pivot's value column is deliberately absent — the AQL terminal never reads
// it — and so no reducer, cardinality, or value-type guard reaches evidence
// rows.
func exclusionConsumedInputNames(pivot ir.PhysicalGroupedPivot, stableRowColumn string) map[string]bool {
	consumed := map[string]bool{pivot.CategoryColumn: true, stableRowColumn: true}
	for _, key := range pivot.GroupKeys {
		consumed[key.Column] = true
	}
	if pivot.CategoryPresenceFromInput {
		consumed[pivot.CategoryPresenceColumn] = true
	}
	return consumed
}

// ---------------------------------------------------------------------------
// Statement
// ---------------------------------------------------------------------------

// renderTableShapeExclusionStatement builds the whole evidence statement:
//
//	WITH px_src AS (SELECT <consumed reads> FROM <source region>)
//	SELECT <nine evidence fields> AS "c0" … "c8"
//	FROM px_src AS p
//	WHERE NOT COALESCE(<listed>, false)
//	ORDER BY <the AQL seven-term order>
//	OFFSET $offset LIMIT $fetchLimit
func (r *planRenderer) renderTableShapeExclusionStatement(plan ir.PhysicalPlan) (string, error) {
	var terminal *ir.PhysicalTableShapeExclusionReturn
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalTableShapeExclusionReturnOp {
			terminal = plan.Operations[index].TableShapeExclusionReturn
		}
	}
	if terminal == nil {
		return "", fmt.Errorf("%w: the exclusion slice requires a terminal evidence operation", ErrUnsupportedPostgresPlan)
	}
	pivot := terminal.Pivot
	rootVariable := plan.Operations[0].RootScan.Variable

	// --- source region through the untouched spine arms (verbatim the pivot
	// statement builder, pivot.go:945-986). ---
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
				// X7: renders-at-AQL, typed-refused here (pivot.go:956-959).
				return "", fmt.Errorf("%w: a non-canonical FILTER cannot coexist with the registered evidence terminal (probe-attested renders-at-AQL divergence, pg-sql-16)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered evidence terminal source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}
	fromText := r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		fromText += " " + join
	}
	for _, lateral := range r.laterals {
		fromText += " " + lateral
	}
	if len(r.where) > 0 {
		fromText += " WHERE " + strings.Join(r.where, " AND ")
	}

	// --- consumed input reads into source slots. ---
	stableRowColumn, err := exclusionStableRowColumn(pivot)
	if err != nil {
		return "", err
	}
	consumed := exclusionConsumedInputNames(pivot, stableRowColumn)
	reads := map[string]string{}
	slotExpressions := make([]string, 0, len(consumed))
	addSlot := func(expression string) string {
		slot := exclusionSlotPrefix + strconv.Itoa(len(slotExpressions))
		slotExpressions = append(slotExpressions, expression+" AS "+slot)
		return "p." + slot
	}
	// bareReads records each input slot's UNqualified alias so the presence
	// derivation below can compose a new CTE slot from it: a CTE SELECT item
	// may name sibling output aliases but not the outer p. qualification
	// ("missing FROM-clause entry for table p", SQLSTATE 42P01).
	bareReads := map[string]string{}
	categoryBehavior := ir.PhysicalPreserveNull
	for _, projection := range pivot.InputProjections {
		if !consumed[projection.Name] {
			continue
		}
		if _, rendered := reads[projection.Name]; rendered {
			// A column that is both category and group key (the producer's
			// pivot-on-itself shape) reads one slot, not two: the reads are
			// the identical expression, so a second slot would only inflate
			// the relation.
			continue
		}
		expression, behavior, err := r.exclusionInputRead(projection, rootVariable)
		if err != nil {
			return "", err
		}
		slot := exclusionSlotPrefix + strconv.Itoa(len(slotExpressions))
		reads[projection.Name] = addSlot(expression)
		bareReads[projection.Name] = slot
		if projection.Name == pivot.CategoryColumn {
			categoryBehavior = behavior
		}
	}
	pvCat, err := pivotSlotRead(reads, pivot.CategoryColumn)
	if err != nil {
		return "", err
	}
	stableRead, err := pivotSlotRead(reads, stableRowColumn)
	if err != nil {
		return "", err
	}

	// --- category presence: the pivot region's derivation verbatim
	// (pivot.go:1116-1159), because the evidence category_present field must
	// equal AQL's HAS(input, @category) for every registered producer form. ---
	present := ""
	// One boolean CTE slot per derivation (AQL :81-82 LET presence, and the
	// :101 SORT term that reads it as a VALUE). Composition inside the CTE
	// uses bare sibling aliases; consumers outside use the qualified alias.
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
		present = addSlot("CASE WHEN " + strings.Join(proofs, " OR ") + " THEN true ELSE false END")
	case pivot.CategoryPresenceFromInput:
		if _, err := pivotSlotRead(reads, pivot.CategoryPresenceColumn); err != nil {
			return "", err
		}
		// COALESCE keeps presence TWO-VALUED, exactly as the pivot does
		// (pivot.go:1134-1146): an absent companion property reads SQL NULL,
		// and AQL's row[@presence] == true is false there, so the row is
		// listed — never silently unlisted evidence.
		present = addSlot("COALESCE(" + bareReads[pivot.CategoryPresenceColumn] + " = 'true'::jsonb, false)")
	default:
		switch categoryBehavior {
		case ir.PhysicalOmitNulls:
			present = addSlot(bareReads[pivot.CategoryColumn] + " IS NOT NULL")
		default:
			present = addSlot("true")
		}
	}

	// --- the listed predicate (AQL table_reshape.go:391-435 in this
	// package's jsonb spelling, pivot.go:1164-1189). ---
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
				// nothing reads AQL-null while present (pivot.go:1180-1183).
				nullRead = "(" + pvCat + " IS NOT NULL AND " + pvCat + " = 'null'::jsonb)"
			}
			matchPredicates = append(matchPredicates, "("+present+" AND "+nullRead+")")
		case ir.PhysicalPivotCategoryMissingMatch:
			matchPredicates = append(matchPredicates, "NOT ("+present+")")
		default:
			return "", fmt.Errorf("%w: pivot category match kind %q is not registered",
				ErrUnsupportedPostgresPlan, string(category.MatchKind))
		}
	}
	listed := "(" + strings.Join(matchPredicates, " OR ") + ")"

	// --- group keys and the identity: the pivot's OWN emitter, so the
	// evidence row identity is byte-identical to the pivot's __loom_row_id. ---
	keySlots := make([]string, 0, len(pivot.GroupKeys))
	kinds := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		expr, err := pivotSlotRead(reads, key.Column)
		if err != nil {
			return "", err
		}
		keySlots = append(keySlots, expr)
		kinds = append(kinds, key.Kind)
	}
	identity := r.pivotIdentitySQL(bindStringVar(plan.BindVars, pivot.ConstructionIDBindKey), kinds, keySlots)

	// --- the source document and its identity availability, materialized as
	// slots so the outer SELECT and the final SORT read them from the
	// relation (they cannot reach r0 directly). AQL reaches the document
	// through the injected __loom_trace_source_document field
	// (table_reshape.go:629-660 merges the root payload into the input row);
	// this terminal is a stored-row query, so the trusted payload column
	// through null_semantics.go's documentColumn carries the same bytes. ---
	document := addSlot(r.documentColumn(r.rootAlias))
	sourceDocument := r.documentColumn(r.rootAlias)
	identityAvailable := addSlot("COALESCE(jsonb_typeof(" + sourceDocument + ") = 'object' AND " +
		"jsonb_typeof(" + sourceDocument + " -> 'resourceType') = 'string' AND " +
		exclusionTextMember(sourceDocument, "resourceType") + " <> '' AND " +
		"jsonb_typeof(" + sourceDocument + " -> 'id') = 'string' AND " +
		exclusionTextMember(sourceDocument, "id") + " <> '', false)")

	// --- the nine evidence fields, in the AQL RETURN order
	// (table_shape_exclusion.go:105-133). Every field NAME and every frozen
	// label crosses SQL only as an ordered $n parameter. ---
	evidenceValue := "CASE WHEN " + present + " THEN " + pvCat + " END"
	textParameter := func(value, key string) string {
		return pivotDollar(r.textParameter(value, "exclusion:"+key))
	}
	selects := []string{
		"(CASE WHEN " + identityAvailable + " THEN " + document + " -> 'resourceType' END)" +
			` AS "c0"`,
		"(CASE WHEN " + identityAvailable + " THEN " + document + " -> 'id' END)" +
			` AS "c1"`,
		"(CASE WHEN " + identityAvailable + " THEN " + textParameter(ir.PhysicalTableShapeExclusionIdentityExact, "exact") +
			" ELSE " + textParameter(ir.PhysicalTableShapeExclusionIdentityUnavailable, "unavailable") + " END)" +
			` AS "c2"`,
		evidenceValue + ` AS "c3"`,
		"to_jsonb(" + present + ")" + ` AS "c4"`,
		textParameter(pivot.CategoryType, "category-type") + ` AS "c5"`,
		identity + ` AS "c6"`,
		textParameter(ir.PhysicalTableShapeExclusionReasonUnlistedCategory, "reason") + ` AS "c7"`,
		"(CASE WHEN " + identityAvailable + " THEN NULL ELSE " +
			textParameter(ir.PhysicalTableShapeExclusionSourceIdentityUnavailable, "omission") + " END)" + ` AS "c8"`,
	}
	r.outputColumns = []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}

	// --- the AQL seven-term SORT (table_shape_exclusion.go:101), term for
	// term, then the OFFSET/probe-bound window (AQL LIMIT @offset, @fetchLimit
	// at :102). ---
	order := []string{identity + ` COLLATE "C" ASC`, identityAvailable + " DESC"}
	order = append(order, exclusionTypeRankTerms(document+" -> 'resourceType'")...)
	order = append(order, exclusionTypeRankTerms(document+" -> 'id'")...)
	order = append(order, present+" ASC")
	order = append(order, exclusionTypeRankTerms("CASE WHEN "+present+" THEN "+pvCat+" END")...)
	order = append(order, exclusionTypeRankTerms(stableRead)...)

	statement := "WITH " + exclusionSlotPrefix + "src AS (SELECT " +
		strings.Join(slotExpressions, ", ") + " FROM " + fromText + ") " +
		"SELECT " + strings.Join(selects, ", ") + " FROM " + exclusionSlotPrefix + "src AS p " +
		"WHERE NOT COALESCE(" + listed + ", false) " +
		"ORDER BY " + strings.Join(order, ", ")
	offset := plan.BindVars[terminal.OffsetBindKey].(int)
	fetchLimit := plan.BindVars[terminal.FetchLimitBindKey].(int)
	offsetIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(offset)}, "")
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(fetchLimit)}, "")
	statement += " OFFSET " + pivotDollar(offsetIndex) + " LIMIT " + pivotDollar(limitIndex)
	return statement, nil
}

// exclusionInputRead renders one consumed input projection over the stored
// root row. It is the pivot door's read callback verbatim
// (pivot.go:999-1023) so a column the two regions both read reads byte-
// identically: a selector extraction takes the null-behavior form, a direct
// read takes the document-path form, and every path crosses SQL only as a
// text[] parameter through null_semantics.go, the package's path authority.
func (r *planRenderer) exclusionInputRead(projection ir.PhysicalProjection, rootVariable string) (string, ir.PhysicalNullBehavior, error) {
	path, behavior, err := pivotInputReadShape(projection, rootVariable)
	if err != nil {
		return "", "", err
	}
	if projection.Expression != nil {
		extraction, err := r.documentPathExpression(r.rootAlias, path)
		if err != nil {
			return "", "", err
		}
		switch behavior {
		case ir.PhysicalOmitNulls:
			return "NULLIF((" + extraction + "), 'null'::jsonb)", behavior, nil
		case ir.PhysicalEmptyOnNull:
			return "COALESCE(NULLIF((" + extraction + "), 'null'::jsonb), '\"\"'::jsonb)", behavior, nil
		default:
			return "(" + extraction + ")", behavior, nil
		}
	}
	if len(path) == 1 {
		// The Arango document-key reference the shared lowering path uses
		// (root "_key" and root document "id") reads the storage column that
		// carries the same validated identity — canonicalKeyColumn,
		// render.go:171-184, the mapping objects.go:739 and window.go:221
		// take for the identical reference. Reading it as a payload path
		// instead would return SQL NULL for every row and silently destroy
		// the deterministic page order the acceptance contract names.
		// to_jsonb keeps the slot jsonb-typed like every other read.
		if column := canonicalKeyColumn(path[0]); resourceFilterColumns[column] {
			return "to_jsonb(" + r.rootAlias + "." + r.storage.Resource.MustColumn(column).SQLText() + "::text)", behavior, nil
		}
	}
	extraction, err := r.documentPathFromExpression(r.documentColumn(r.rootAlias), path)
	if err != nil {
		return "", "", err
	}
	return "(" + extraction + ")", behavior, nil
} // exclusionTextMember renders the SQL text of one JSON string member, the
// reading AQL's IS_STRING guard then non-empty comparison performs.
func exclusionTextMember(document, member string) string {
	return "(" + document + " -> '" + member + "' #>> '{}')"
}

// exclusionTypeRankTerms renders one Arango-ordered SORT term set for a jsonb
// expression: null before boolean before number before string, then the
// within-type value, exactly the registered spelling of pivotWindowOrder's
// per-key terms (pivot.go:1347-1354). Reusing the shape (rather than a bare
// text comparison) keeps evidence pages ordered the way the AQL SORT orders
// them when a member is null or of an unexpected JSON type.
func exclusionTypeRankTerms(expr string) []string {
	return []string{
		"(CASE WHEN " + expr + " IS NULL THEN 0 WHEN jsonb_typeof(" + expr + ") = 'boolean' THEN 1" +
			" WHEN jsonb_typeof(" + expr + ") = 'number' THEN 2 WHEN jsonb_typeof(" + expr + ") = 'string' THEN 3 ELSE 4 END) ASC",
		"(CASE WHEN jsonb_typeof(" + expr + ") = 'boolean' THEN " + expr + "::text END) ASC",
		"((CASE WHEN jsonb_typeof(" + expr + ") = 'number' THEN " + expr + " #>> '{}' END)::double precision) ASC",
		"((CASE WHEN jsonb_typeof(" + expr + ") = 'string' THEN " + expr + " #>> '{}' END) COLLATE \"C\") ASC",
	}
}
