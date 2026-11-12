package postgres

// The typed cell-value evidence terminal (pg-sql-24) is registered here as
// its own closed plan slice: an inline plan (StageSequence == nil) whose final
// RETURN the compiler replaced with exactly one PhysicalCellTraceReturnOp over
// one direct root scan, compiled from the canonical output plan by
// CompileCellTraceOutput's main path (compiler/cell_trace.go). Dispatch is
// the single additive cell-trace door in RenderPhysicalPlan (at the HEAD of
// the whole-plan region, before the graph door, coordinator ruling
// 2026-11-12: a path-op plan carrying the terminal must never route into the
// graph validator, which has never seen this companion; claiming every
// terminal-carrying plan at the head keeps unregistered shapes fail-closed
// with a named refusal). The claim test is narrow by construction:
// compileGroupRowsCellTrace mints GroupRows.CellTrace and
// compileConstructionCellTrace mints StageSequence.CellTraceReturn, so
// neither shape carries the op and both keep their compile-entry named
// refusals; every plan without the op routes byte-identically to the doors
// that claimed it before this one existed.
// validateCellTracePlanSurface is the single total contract behind the door
// (render.go / graph.go / row_lineage.go precedent); the entry==dispatch law
// is between the door entry (RenderPhysicalPlan) and a direct
// renderCellTracePhysicalPlan call — the test's dispatch side — which must
// refuse identical shapes with byte-identical messages.
//
// AQL ground truth is render/aql/cell_trace.go :11-98 (renderCellTraceReturn)
// and :525-554 (renderReducedSetTraceContributors, renderTraceContributor-
// Queries), ported branch-for-branch:
//
//   - the six LET/@bind select members map to six per-row correlated select
//     expressions over the spine's flat ordered SELECT: __loom_trace_value,
//     __loom_trace_contributions (SLICE(page, 0, @limit) as a capped ordered
//     jsonb_agg), __loom_trace_identity_parts or __loom_trace_explicit_-
//     identity, __loom_trace_status (the LENGTH==0 / ==null / lossy>1 / VALUE
//     CASE with the LIMIT-2 status candidate count), __loom_trace_has_more
//     (LENGTH(page) > @limit as a bounded COUNT), and __loom_trace_omission
//     (the empty string: the main path mints no omission — only the refused
//     Reshape/Construction lineages do);
//   - the contributor record {resourceType, resourceId, value} maps to
//     jsonb_build_object over REGISTERED COLUMN READS, never literals: the
//     child/root row's resource_type column and logical_id column (the spine
//     canonicalization that maps the FHIR id/_key document reference onto
//     logical_id, traversal.go:490-501);
//   - the two registered contributor arms mirror the AQL dispatch order
//     (renderTraceContributors :74-138): Contribution first, then the bare
//     root EXTRACT; every other value kind (AGGREGATE, OWNER_RECORDS, CALL
//     recode, Prepared extract, set-sourced extract, Construction, Reshape)
//     refuses by name and registers with its own terminal surface in a later
//     wave, exactly as pg-sql-23 registered only the bounded page.
//
// Registered arms:
//
//   - A (reduced-set contributors, the AQL renderReducedSetTraceContributors
//     twin :525-533): the terminal carries Contribution{SetVariable,
//     ValueField, Lossy} over one registered SET reduction. The contributor
//     page is the byte-twin of the spine's OWN reduction page
//     (setReductionSublink sets.go:440-495): the same DISTINCT item relation
//     over the correlated occurrence join, the same setProjectionSlot-
//     Expression slot with the same WITH ORDINALITY FLATTEN, the same
//     total order (logical_id, element ordinality). This is the property the
//     page rests on: contributor membership must never drift from the
//     reduced column the PostgreSQL preview published, so the page reuses
//     the spine's registered text rather than an AQL-shaped re-derivation.
//     The page relation is bounded OFFSET @offset / LIMIT @fetch_limit (the
//     lookahead), the published array is the first @limit of it, and
//     hasMore compares the bounded relation's length against @limit — the
//     AQL double-cap law (:530-531, execution/row_lineage.go:73-75 parity).
//   - B (direct typed cell over the root row, the AQL
//     renderTraceContributorQueries twin :535-554 with items [root]): the
//     terminal's Value is an unPrepared EXTRACT whose source is the root row
//     variable. The contributor is the traced row itself — AQL's
//     FOR item IN [root] iterates exactly the current row — so the page is
//     rendered directly over the statement's own root alias: every
//     out-of-scope filter the statement applies to the row bounds the
//     contributor stream identically, and no witness row can ever join a
//     self-row page. The register is scalar-cardinality only (the spine's
//     own selector contract, validateSelectorExtraction :432): the AQL
//     FILTER value != null arm applies (absent OR JSON-null ⇒ zero
//     contributors ⇒ NO_MATCH), and array/object selector extracts refuse
//     with the spine's registered voice.
//
// Status law (the AQL :58-67 CASE, branch-for-branch, evaluated per row):
// RowExists first (the terminal attests an existing typed row: value-null ⇒
// RECORDED_NULL else VALUE, availability never speaks); otherwise zero
// status candidates ⇒ NO_MATCH, value SQL-NULL or JSON-null ⇒
// RECORDED_NULL, Contribution.Lossy with more than one candidate ⇒
// AMBIGUOUS (FIRST-mode ties the compiler cannot order), else VALUE.
//
// Registered divergences (documented, like D-23a/b):
//
//   - D-24a: arm A expands the spine's REGISTERED slot expression
//     (setProjectionSlotExpression: missing OR JSON-null document path ⇒ the
//     empty list), while the AQL contributor twin reads the raw item field
//     with (item.f == null ? [null] : TO_ARRAY(item.f)). The PostgreSQL
//     preview's own reduced column already uses the spine normalization, so
//     preview/trace agreement (the trace must agree with what this backend
//     published) outranks the AQL null-as-single-item reading: a cell whose
//     registered slot normalizes to empty contributes zero items and reads
//     NO_MATCH here, matching the published column. Arm B keeps the AQL
//     reading exactly: the null-vs-missing distinction is rendered from the
//     value expression itself, never from a slot.
//   - D-24b: arm B's single contributor is provably the current row, so the
//     page/cap machinery collapses to its provable bounds (zero or one item
//     ⇒ hasMore is the constant false, the published array is at most one
//     record). The text is the registered jsonb_build_object shape; no plan
//     data crosses anywhere but a parameter.
//
// Refusal register (typed ErrUnsupportedPostgresPlan, never partially
// rendered, never an AQL fallback). Post-merge enforcement: the PostgreSQL
// target dispatches to compileCellTracePostgreSQL (compiler/cell_trace.go)
// before target.rejectPostgreSQL can speak, and the compile entry refuses
// the stage-sequence (composed construction) and group-rows shapes by name
// before preparation; registering this slice removed no refusal, it moved
// these refusals to a named authority. The seal list retired this surface's
// AQL-only arm once the terminal became PG-implemented; the named
// zero-output refusal stays pinned by the compiler battery's
// zero-output-names-its-surface cell:
//
//   - StageSequence != nil: the composed-construction companion keeps the
//     stage_sequence.go / group.go / coded_group.go / pivot.go voices
//     (their :262/:328/:454/:1398 companion refusals are untouched because
//     those plans never carry the op).
//   - Construction != nil (and its RelatedSource replay): the construction
//     lineage registers with the composition slice's terminal wave; the
//     related-source replay registers with the related-occurrence surface.
//   - Reshape != nil: pivot/unpivot table-shape lineage registers with the
//     pivot and unpivot terminal surfaces (their omission codes — the
//     TABLE_SHAPE_* family — never reach a rendered statement here).
//   - Value kinds outside the two arms: AGGREGATE values register with the
//     aggregate/unnest terminal surfaces; OWNER_RECORDS with the owner-
//     records surface; the CALL category-recode walk with the recode
//     surface; Prepared extracts and set-sourced extract contributors carry
//     no registered PostgreSQL item relation yet.
//   - non-registered operations: top-level TRAVERSAL, UNNEST, UNPIVOT,
//     GROUP_ROWS, RETURN (the compiler replaced it — a surviving RETURN is a
//     hand-assembled plan), PATH_*/GRAPH_* ops, and any second
//     CellTraceReturnOp or companion POPULATION_MAPPING_RETURN /
//     TABLE_SHAPE_EXCLUSION_RETURN operation.
//   - non-direct roots: population/cohort membership roots and the
//     candidate seed keep the population.go/candidate surfaces; both arms
//     correlate stored rows by (logical_id, resource_type) over the direct
//     resource table only.
//   - bind seal: the three page binds must be plan ints with 0 <= offset,
//     1 <= limit, and fetch_limit == limit+1 exactly (the registered
//     lookahead); any other spelling is a plan defect, refused before
//     rendering.
//   - terminal seal: no omission code (only the refused lineages mint one);
//     identity is either one explicit expression or at least one part,
//     never both and never neither; every identity part is a string bind,
//     a resolvable row value, or an unPrepared selector EXTRACT.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	// cellTraceStatusCandidateLimit is the AQL status-candidate LIMIT 2
	// (render/aql/cell_trace.go:531,553): two candidates are enough to
	// distinguish zero, one, and many for NO_MATCH and the lossy AMBIGUOUS.
	cellTraceStatusCandidateLimit = 2
	// cellTracePageAlias names the arm A capped page relation; renderer-owned,
	// never plan data.
	cellTracePageAlias = "capped"
)

// cellTraceTerminalMentions is the door's claim test: an inline plan
// (StageSequence == nil) mentioning at least one PhysicalCellTraceReturnOp.
// It is deliberately generous — fail-closed lives in
// validateCellTracePlanSurface behind the door — so that every shape the
// slice does not register is refused with a NAMED typed error here rather
// than falling through to a door that would silently drop the trace columns
// (coordinator ruling 2026-11-12, head placement).
func cellTraceTerminalMentions(plan ir.PhysicalPlan) bool {
	if plan.StageSequence != nil {
		return false
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalCellTraceReturnOp && operation.CellTraceReturn != nil {
			return true
		}
	}
	return false
}

// renderCellTracePhysicalPlan is the single cell-trace door. Its only
// production caller is the cell-trace arm of RenderPhysicalPlan (render.go,
// at the head of the whole-plan region, before the graph door); the direct
// calls in cell_trace_test.go are the test's dispatch side of the
// entry==dispatch equality law, not a second door. Gate order mirrors the
// other slice doors: storage validation, then validateCellTracePlanSurface
// as the first plan-facing act so the door and a direct dispatch call refuse
// the same shapes with the same typed errors BEFORE plan.Validate can speak,
// then the plan-wide gates, then the builder.
func renderCellTracePhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateCellTracePlanSurface(plan); err != nil {
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
	sql, err := renderer.renderCellTraceStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// validateCellTracePlanSurface is the total closed-slice contract behind the
// single door. It speaks typed refusals over ANY plan the door claims before
// plan validation, scope loading, or any rendering can reject with an
// off-type error.
func validateCellTracePlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence != nil {
		return fmt.Errorf("%w: cell-trace dispatch requires an inline plan; composed-construction traces keep the composition slice's registered voice",
			ErrUnsupportedPostgresPlan)
	}
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: cell-trace dispatch requires at least one operation", ErrUnsupportedPostgresPlan)
	}
	terminal := (*ir.PhysicalCellTraceReturn)(nil)
	terminalIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalCellTraceReturnOp {
			if operation.CellTraceReturn == nil {
				return fmt.Errorf("%w: operation %d: cell-trace terminal payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if terminal != nil {
				return fmt.Errorf("%w: operation %d: a plan carries at most one cell-trace terminal", ErrUnsupportedPostgresPlan, index)
			}
			terminal = operation.CellTraceReturn
			terminalIndex = index
			continue
		}
		switch operation.Kind {
		case ir.PhysicalPopulationMappingReturnOp, ir.PhysicalTableShapeExclusionReturnOp:
			return fmt.Errorf("%w: operation %d: the %q companion terminal keeps its registered surface voice",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
		// Registered source arms only: any other top-level operation
		// (TRAVERSAL, UNNEST, UNPIVOT, GROUP_ROWS, a surviving RETURN, the
		// PATH_*/GRAPH_* family, collection/key/document lookups) refuses
		// here, typed, before plan validation or the spine walk can speak
		// with an off-type error. The render-side walk keeps the same
		// closed set as defense in depth.
		switch operation.Kind {
		case ir.PhysicalRootScanOp, ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:
		default:
			return fmt.Errorf("%w: operation %q is not part of the cell-trace render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if terminal == nil {
		return fmt.Errorf("%w: cell-trace dispatch requires the evidence terminal", ErrUnsupportedPostgresPlan)
	}
	if terminalIndex != len(plan.Operations)-1 {
		return fmt.Errorf("%w: the cell-trace terminal must be the final operation", ErrUnsupportedPostgresPlan)
	}
	if terminal.OmissionCode != "" {
		return fmt.Errorf("%w: the inline main-path terminal mints no omission code; omission-bearing lineages refuse by name",
			ErrUnsupportedPostgresPlan)
	}
	if terminal.Construction != nil {
		return fmt.Errorf("%w: construction cell lineage registers with the composition slice's terminal wave", ErrUnsupportedPostgresPlan)
	}
	if terminal.Reshape != nil {
		return fmt.Errorf("%w: table-shape (pivot/unpivot) cell lineage registers with the pivot and unpivot terminal surfaces", ErrUnsupportedPostgresPlan)
	}
	if err := validateCellTraceIdentity(terminal); err != nil {
		return err
	}
	if err := validateCellTraceBinds(plan, terminal); err != nil {
		return err
	}
	if terminal.Contribution != nil {
		return validateCellTraceContribution(plan, terminal)
	}
	// Arm B: the bare unPrepared EXTRACT over the root row.
	if terminal.Value.Kind != ir.PhysicalExtractExpression || terminal.Value.Extract == nil ||
		terminal.Value.Extract.Prepared != nil {
		return fmt.Errorf("%w: contributor evidence registers only for the reduced-set contribution and the direct root selector extract; aggregate, owner-records, call-recode, and prepared-extract values register with their terminal surfaces",
			ErrUnsupportedPostgresPlan)
	}
	if len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: cell-trace dispatch requires one leading direct ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	if terminal.Value.Extract.Source.Variable != plan.Operations[0].RootScan.Variable {
		return fmt.Errorf("%w: extract contributor %q must read the root row %q; set-sourced extract contributors have no registered item relation yet",
			ErrUnsupportedPostgresPlan, terminal.Value.Extract.Source.Variable, plan.Operations[0].RootScan.Variable)
	}
	if terminal.Value.Cardinality != ir.PhysicalScalarCardinality {
		return fmt.Errorf("%w: selector extraction is registered for SCALAR cardinality only; array and object selector projections belong to a later capability",
			ErrUnsupportedPostgresPlan)
	}
	return nil
}

// validateCellTraceIdentity mirrors the compiler's mint law
// (compiler/population_mapping.go finalPopulationMappingIdentity): the
// ordered identity parts are minted first, from RowIdentity.Fields, and the
// explicit row identity is minted only beside them (a __loom_row_id
// projection). The IR validator refuses a parts-less terminal
// (ir/physical_validation.go:1531-1532), so the parts spelling is the
// mandatory half; the explicit spelling, when present, is the published
// contract column by the shared pick law in renderCellTraceColumns
// (compiler/cell_trace.go cellTraceQuery names the same column).
func validateCellTraceIdentity(terminal *ir.PhysicalCellTraceReturn) error {
	if len(terminal.IdentityParts) == 0 {
		return fmt.Errorf("%w: the terminal carries no ordered identity parts; the mint mints them first and the IR validator never admits a parts-less terminal", ErrUnsupportedPostgresPlan)
	}
	for _, part := range terminal.IdentityParts {
		if part.Name == "" {
			return fmt.Errorf("%w: identity part without a name", ErrUnsupportedPostgresPlan)
		}
		if err := validateCellTraceIdentityExpression(part.Expression, "identity part "+strconv.Quote(part.Name)); err != nil {
			return err
		}
	}
	if terminal.ExplicitIdentity != nil {
		return validateCellTraceIdentityExpression(*terminal.ExplicitIdentity, "explicit identity")
	}
	return nil
}

// validateCellTraceIdentityExpression is the structural half of the identity
// dispatcher (renderCellTraceExpressionSQL owns the rendering half with the
// same closed kind list).
func validateCellTraceIdentityExpression(expression ir.PhysicalExpression, label string) error {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		if expression.Value == nil || (expression.Value.BindKey == "" && expression.Value.Variable == "") {
			return fmt.Errorf("%w: %s is a VALUE without a bind key or row variable", ErrUnsupportedPostgresPlan, label)
		}
		return nil
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil || expression.Extract.Prepared != nil {
			return fmt.Errorf("%w: %s is a prepared extract, which has no registered PostgreSQL rendering", ErrUnsupportedPostgresPlan, label)
		}
		if expression.Extract.Source.Variable == "" {
			return fmt.Errorf("%w: %s reads from no row variable", ErrUnsupportedPostgresPlan, label)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s kind %q has no registered cell-trace rendering", ErrUnsupportedPostgresPlan, label, string(expression.Kind))
	}
}

// validateCellTraceBinds is the page-bind seal: the compiler mints three int
// binds and the renderer reads them as typed bigints.
func validateCellTraceBinds(plan ir.PhysicalPlan, terminal *ir.PhysicalCellTraceReturn) error {
	if terminal.OffsetBindKey == "" || terminal.LimitBindKey == "" || terminal.FetchLimitBindKey == "" {
		return fmt.Errorf("%w: the terminal must carry the offset, limit, and fetch-limit bind keys", ErrUnsupportedPostgresPlan)
	}
	offset, ok := plan.BindVars[terminal.OffsetBindKey].(int)
	if !ok || offset < 0 {
		return fmt.Errorf("%w: cell-trace offset bind %q must be a non-negative int", ErrUnsupportedPostgresPlan, terminal.OffsetBindKey)
	}
	limit, ok := plan.BindVars[terminal.LimitBindKey].(int)
	if !ok || limit < 1 {
		return fmt.Errorf("%w: cell-trace limit bind %q must be an int of at least 1", ErrUnsupportedPostgresPlan, terminal.LimitBindKey)
	}
	fetch, ok := plan.BindVars[terminal.FetchLimitBindKey].(int)
	if !ok || fetch != limit+1 {
		return fmt.Errorf("%w: cell-trace fetch-limit bind %q must be the registered limit+1 lookahead", ErrUnsupportedPostgresPlan, terminal.FetchLimitBindKey)
	}
	return nil
}

// validateCellTraceContribution pins the mint-consistency facts of arm A:
// the contribution names one registered SET whose reduction produced the
// terminal's value, and the value field is one of that SET's projection
// slots. Lossy must be exactly the FIRST-mode reading.
func validateCellTraceContribution(plan ir.PhysicalPlan, terminal *ir.PhysicalCellTraceReturn) error {
	contribution := terminal.Contribution
	if contribution.SetVariable == "" || contribution.ValueField == "" {
		return fmt.Errorf("%w: the contribution must name its source set and value field", ErrUnsupportedPostgresPlan)
	}
	if terminal.Value.Kind != ir.PhysicalValueExpression || terminal.Value.Value == nil ||
		terminal.Value.Value.Variable == "" || len(terminal.Value.Value.Path) != 1 ||
		terminal.Value.Value.BindKey != "" {
		return fmt.Errorf("%w: the reduced-set terminal value must read one field of the reduction variable", ErrUnsupportedPostgresPlan)
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalSetOp || operation.Set == nil || operation.Set.Reduction == nil {
			continue
		}
		set := operation.Set
		if set.Reduction.Variable != terminal.Value.Value.Variable {
			continue
		}
		if set.Variable != contribution.SetVariable || set.Reduction.SourceSetVariable != contribution.SetVariable {
			return fmt.Errorf("%w: the reduced-set terminal reads reduction %q of set %q, whose contribution names set %q; only the self-sourced child-set reduction shape is registered",
				ErrUnsupportedPostgresPlan, set.Reduction.Variable, set.Variable, contribution.SetVariable)
		}
		matched := false
		for _, field := range set.Reduction.Fields {
			if field.Name != terminal.Value.Value.Path[0] {
				continue
			}
			if field.SourceField != contribution.ValueField {
				return fmt.Errorf("%w: contribution value field %q disagrees with reduction field %q source slot %q",
					ErrUnsupportedPostgresPlan, contribution.ValueField, field.Name, field.SourceField)
			}
			if lossy := field.Mode == ir.PhysicalSetReductionFirst; lossy != contribution.Lossy {
				return fmt.Errorf("%w: contribution lossy=%v disagrees with reduction field %q mode %q",
					ErrUnsupportedPostgresPlan, contribution.Lossy, field.Name, string(field.Mode))
			}
			matched = true
		}
		if !matched {
			return fmt.Errorf("%w: reduction field %q of set %q is unknown", ErrUnsupportedPostgresPlan, terminal.Value.Value.Path[0], set.Variable)
		}
		return nil
	}
	return fmt.Errorf("%w: the contribution names set %q, which carries no reduction over the terminal value", ErrUnsupportedPostgresPlan, contribution.SetVariable)
}

// renderCellTraceStatement walks the spine's registered source arms with the
// spine's own positional rules (renderOperations, render.go:537-652) and
// then places the six trace columns on the flat ordered SELECT. A RETURN
// operation anywhere is a hand-assembled plan: the compiler replaced the
// terminal it read.
func (r *planRenderer) renderCellTraceStatement(plan ir.PhysicalPlan) (string, error) {
	dollar := func(index int) string { return "$" + strconv.Itoa(index) }
	terminal := plan.Operations[len(plan.Operations)-1].CellTraceReturn
	phase := 0 // 0 = source ops, 1 = window ops; the trace terminal is the final op.
	sawSort, sawLimit := false, false
	rootVariable := ""
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if phase != 0 || index != 0 {
				return "", fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
			scan := operation.RootScan
			if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
				return "", fmt.Errorf("%w: membership and candidate-seed roots keep the population surfaces' registered contracts; the cell-trace arms correlate direct stored rows",
					ErrUnsupportedPostgresPlan)
			}
			if err := r.renderRootScan(plan, *scan); err != nil {
				return "", err
			}
			rootVariable = scan.Variable
		case ir.PhysicalFilterOp:
			if phase != 0 {
				return "", fmt.Errorf("%w: filters must precede the execution window", ErrUnsupportedPostgresPlan)
			}
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
			if phase != 0 {
				return "", fmt.Errorf("%w: derived lets must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if canonical {
				continue
			}
			return "", fmt.Errorf("%w: operation %q is not part of the cell-trace render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalSetOp:
			// The shared mint runs the plan through withGenericPhysical-
			// ExecutionWindow first, which inserts the root SORT before the
			// child SET operations (the AQL LET ordering: child sets stay
			// correlated per surviving root row). A PostgreSQL SET renders
			// only per-row correlated sublinks and renderer-registered
			// relations — never an ORDER-BY-relevant FROM item — so a SET
			// after the root window SORT is layout-neutral; a SET after the
			// page LIMIT would bind against rows the page already dropped
			// and stays refused.
			if phase > 1 || (phase == 1 && sawLimit) {
				return "", fmt.Errorf("%w: sets must precede the execution window's LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Set == nil {
				return "", fmt.Errorf("%w: operation %d: set payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderSet(plan, *operation.Set); err != nil {
				return "", err
			}
		case ir.PhysicalSortOp:
			if sawSort || phase > 1 || (phase == 1 && sawLimit) {
				return "", fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", err
			}
			sawSort, phase = true, 1
		case ir.PhysicalLimitOp:
			if sawLimit || phase > 1 {
				return "", fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", err
			}
			sawLimit, phase = true, 1
		case ir.PhysicalCellTraceReturnOp:
			if !last || phase > 1 {
				return "", fmt.Errorf("%w: the cell-trace terminal must be the final operation", ErrUnsupportedPostgresPlan)
			}
		default:
			return "", fmt.Errorf("%w: operation %q is not part of the cell-trace render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if rootVariable == "" || r.rootVariable != rootVariable {
		return "", fmt.Errorf("%w: the cell-trace terminal requires the direct root row", ErrUnsupportedPostgresPlan)
	}
	return r.renderCellTraceColumns(plan, terminal, rootVariable, dollar)
}

// renderCellTraceColumns renders the six select-list columns and seals the
// output contract, then assembles the statement with the spine's own ORDER BY
// and window LIMIT.
func (r *planRenderer) renderCellTraceColumns(plan ir.PhysicalPlan, terminal *ir.PhysicalCellTraceReturn, rootVariable string, dollar func(int) string) (string, error) {
	valueText, err := r.renderCellTraceExpression(plan, "trace value", terminal.Value)
	if err != nil {
		return "", err
	}

	var contributors, status, hasMore string
	if terminal.Contribution != nil {
		contributors, status, hasMore, err = r.renderCellTraceSetArms(plan, terminal, valueText, dollar)
	} else {
		contributors, status, hasMore, err = r.renderCellTraceRootArms(plan, terminal, rootVariable, valueText, dollar)
	}
	if err != nil {
		return "", err
	}
	if terminal.RowExists {
		status = "CASE WHEN " + cellTraceIsNullForm(valueText) + " THEN 'RECORDED_NULL' ELSE 'VALUE' END"
	}

	// Identity pick law, shared with the compiled contract (compiler/
	// cell_trace.go cellTraceQuery): a minted explicit row identity is the
	// published contract column — the mint always keeps it beside the
	// ordered parts (validateCellTraceIdentity pins that) — and otherwise
	// the ordered identity-parts array is. Both halves render through the
	// same closed expression dispatcher as the traced value.
	identityText := ""
	identityColumn := ir.PhysicalCellTraceIdentityPartsField
	if terminal.ExplicitIdentity != nil {
		rendered, renderErr := r.renderCellTraceExpression(plan, "explicit identity", *terminal.ExplicitIdentity)
		if renderErr != nil {
			return "", renderErr
		}
		identityText = rendered
		identityColumn = ir.PhysicalCellTraceExplicitIdentityField
	} else {
		parts := make([]string, 0, len(terminal.IdentityParts))
		for _, part := range terminal.IdentityParts {
			rendered, renderErr := r.renderCellTraceExpression(plan, "identity part "+strconv.Quote(part.Name), part.Expression)
			if renderErr != nil {
				return "", renderErr
			}
			parts = append(parts, rendered)
		}
		identityText = "jsonb_build_array(" + strings.Join(parts, ", ") + ")"
	}

	omission := dollar(r.textParameter("", "cell-trace-omission")) + "::text"

	r.selects = []string{
		valueText + ` AS "` + ir.PhysicalCellTraceValueField + `"`,
		contributors + ` AS "` + ir.PhysicalCellTraceContributionsField + `"`,
		identityText + ` AS "` + identityColumn + `"`,
		status + ` AS "` + ir.PhysicalCellTraceStatusField + `"`,
		hasMore + ` AS "` + ir.PhysicalCellTraceHasMoreField + `"`,
		omission + ` AS "` + ir.PhysicalCellTraceOmissionField + `"`,
	}
	r.outputColumns = []string{
		ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, identityColumn,
		ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField, ir.PhysicalCellTraceOmissionField,
	}
	return r.assemble(), nil
}

// renderCellTraceSetArms is the arm A triple: the capped ordered contributor
// array, the status CASE over the LIMIT-2 candidate count, and hasMore —
// each a per-row correlated sublink over the spine's own registered item
// relation for the contributing set (the setReductionSublink byte-twin).
func (r *planRenderer) renderCellTraceSetArms(plan ir.PhysicalPlan, terminal *ir.PhysicalCellTraceReturn, valueText string, dollar func(int) string) (contributors, status, hasMore string, err error) {
	contribution := terminal.Contribution
	corr, ok := r.setBodies[contribution.SetVariable]
	if !ok {
		return "", "", "", fmt.Errorf("%w: contribution set %q did not register a correlated item relation", ErrUnsupportedPostgresPlan, contribution.SetVariable)
	}
	fields := r.setProjections[contribution.SetVariable]
	field := ir.PhysicalSetProjectionField{}
	found := false
	for _, candidate := range fields {
		if candidate.Name == contribution.ValueField {
			field, found = candidate, true
		}
	}
	if !found {
		return "", "", "", fmt.Errorf("%w: contribution value field %q is not a projection slot of set %q",
			ErrUnsupportedPostgresPlan, contribution.ValueField, contribution.SetVariable)
	}

	seq := r.nextTraversalSequence()
	itemAlias := "i" + strconv.Itoa(seq)
	elementAlias := "s" + strconv.Itoa(seq)
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()
	items := "(SELECT DISTINCT " + corr.targetAlias + "." + logicalIDColumn + " AS " + logicalIDColumn +
		", " + corr.targetAlias + "." + resourceTypeColumn + " AS " + resourceTypeColumn +
		", " + corr.targetAlias + "." + payloadColumn + " AS " + payloadColumn +
		" " + corr.from + " WHERE " + corr.where + ")"
	slot, err := r.setProjectionSlotExpression(field, corr, itemAlias)
	if err != nil {
		return "", "", "", fmt.Errorf("contributor slot %q: %w", contribution.ValueField, err)
	}
	lateral := " CROSS JOIN LATERAL jsonb_array_elements(" + slot + ") WITH ORDINALITY AS " +
		elementAlias + "(v, ord)"
	fromClause := items + " AS " + itemAlias + lateral
	itemOrder := itemAlias + "." + logicalIDColumn
	elementOrder := elementAlias + ".ord"
	totalOrder := itemOrder + ", " + elementOrder

	offsetIndex := r.integerParameter(plan.BindVars[terminal.OffsetBindKey].(int))
	fetchIndex := r.integerParameter(plan.BindVars[terminal.FetchLimitBindKey].(int))
	limitIndex := r.integerParameter(plan.BindVars[terminal.LimitBindKey].(int))

	page := "(SELECT " + itemAlias + "." + logicalIDColumn + " AS c_lid, " +
		itemAlias + "." + resourceTypeColumn + " AS c_rt, " +
		elementAlias + ".v AS c_v, " + elementAlias + ".ord AS c_ord FROM " + fromClause +
		" ORDER BY " + totalOrder +
		" OFFSET " + dollar(offsetIndex) + " LIMIT " + dollar(fetchIndex) + ")"
	contributors = "COALESCE((SELECT jsonb_agg(jsonb_build_object(" +
		"'resourceType', " + cellTracePageAlias + ".c_rt, " +
		"'resourceId', " + cellTracePageAlias + ".c_lid, " +
		"'value', " + cellTracePageAlias + ".c_v" +
		") ORDER BY " + cellTracePageAlias + ".c_lid, " + cellTracePageAlias + ".c_ord" +
		") FROM (SELECT * FROM " + page + " AS " + cellTracePageAlias +
		" ORDER BY " + cellTracePageAlias + ".c_lid, " + cellTracePageAlias + ".c_ord" +
		" LIMIT " + dollar(limitIndex) + ") AS " + cellTracePageAlias + "), '[]'::jsonb)"
	candidateCount := "(SELECT COUNT(*) FROM (SELECT 1 FROM " + fromClause +
		" ORDER BY " + totalOrder + " LIMIT " + strconv.Itoa(cellTraceStatusCandidateLimit) + ") AS __loom_t_status)"
	hasMore = "(SELECT COUNT(*) > " + dollar(limitIndex) + " FROM (SELECT 1 FROM " + fromClause +
		" ORDER BY " + totalOrder + " OFFSET " + dollar(offsetIndex) + " LIMIT " + dollar(fetchIndex) + ") AS __loom_t_more)"
	status = cellTraceStatusCase(candidateCount, valueText, contribution.Lossy && !terminal.RowExists)
	return contributors, status, hasMore, nil
}

// renderCellTraceRootArms is the arm B triple: the traced row is its own
// single contributor, so the page collapses to its provable bounds (D-24b):
// the record renders over the statement's own root alias — the same row the
// statement's registered scope conjuncts already bound — the published array
// is zero or one record, and hasMore is the constant false. The collapse
// does not erase the offset: the AQL twin renders the page as
// `LIMIT @cell_trace_offset, @cell_trace_fetch_limit` over the one-item
// relation then `SLICE(page, 0, @limit)` (render/aql/cell_trace.go:552), so
// an offset >= 1 skips the sole candidate and publishes an empty array while
// the offset-free LIMIT-2 candidate query still reads VALUE. The sealed
// offset bind gates the record here with the same law.
func (r *planRenderer) renderCellTraceRootArms(plan ir.PhysicalPlan, terminal *ir.PhysicalCellTraceReturn, rootVariable string, valueText string, dollar func(int) string) (contributors, status, hasMore string, err error) {
	scope, ok := r.rows[rootVariable]
	if !ok || scope.derivedValue != "" {
		return "", "", "", fmt.Errorf("%w: cell-trace root %q does not bind a stored row", ErrUnsupportedPostgresPlan, rootVariable)
	}
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()

	// AQL renderTraceContributorQueries :547-549, the scalar arm: FILTER
	// value != null is null-equal, so both absent and JSON-null drop the
	// item (the validator has already pinned scalar cardinality).
	itemExists := "NOT " + cellTraceIsNullForm(valueText)
	record := "jsonb_build_object('resourceType', " + r.rootAlias + "." + resourceTypeColumn +
		", 'resourceId', " + r.rootAlias + "." + logicalIDColumn +
		", 'value', " + valueText + ")"
	offsetIndex := r.integerParameter(plan.BindVars[terminal.OffsetBindKey].(int))
	contributors = "CASE WHEN " + itemExists + " AND " + dollar(offsetIndex) + " = 0 THEN jsonb_build_array(" + record + ") ELSE '[]'::jsonb END"
	candidateCount := "(CASE WHEN " + itemExists + " THEN 1 ELSE 0 END)"
	status = cellTraceStatusCase(candidateCount, valueText, false)
	hasMore = "false"
	return contributors, status, hasMore, nil
}

// cellTraceStatusCase reproduces the AQL status CASE (render/aql/cell_trace.
// go:61-67) branch-for-branch on one row: zero candidates ⇒ NO_MATCH,
// SQL-NULL or JSON-null value ⇒ RECORDED_NULL, lossy with more than one
// candidate ⇒ AMBIGUOUS, else VALUE. The RowExists override (which speaks
// first and never consults availability) is applied by the caller.
func cellTraceStatusCase(candidateCount, valueText string, lossy bool) string {
	var builder strings.Builder
	builder.WriteString("CASE WHEN " + candidateCount + " = 0 THEN 'NO_MATCH'")
	builder.WriteString(" WHEN " + cellTraceIsNullForm(valueText) + " THEN 'RECORDED_NULL'")
	if lossy {
		builder.WriteString(" WHEN " + candidateCount + " > 1 THEN 'AMBIGUOUS'")
	}
	builder.WriteString(" ELSE 'VALUE' END")
	return builder.String()
}

// cellTraceIsNullForm is the SQL twin of the AQL `value == null` test
// (:60,:64,:498): SQL NULL (absent path) and the JSON-null scalar both read
// as null evidence; SQL NULL is never the same fact as a recorded JSON null
// to the storage layer, but AQL folds both here, and the status column is
// where the fold lives.
func cellTraceIsNullForm(valueText string) string {
	return "(((" + valueText + ") IS NULL) OR jsonb_typeof((" + valueText + ")) = 'null')"
}

// renderCellTraceExpression is the closed expression dispatcher shared by the
// value column and every identity part. Its three arms are exactly the
// expressions physicalProjectionExpression (compiler/population_mapping.
// go:387-395) can mint for the registered shapes, each rendered through the
// SAME registered reading the preview's own RETURN used (render.go
// renderReturn :805-830): resolvable row values through resolveValue,
// unPrepared selector extractions through scalarExtractValueExpression, and
// compiler bind values as to_jsonb text. Every other kind refuses by name.
func (r *planRenderer) renderCellTraceExpression(plan ir.PhysicalPlan, label string, expression ir.PhysicalExpression) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		if expression.Value == nil {
			return "", fmt.Errorf("%w: %s VALUE has no payload", ErrUnsupportedPostgresPlan, label)
		}
		if expression.Value.BindKey != "" {
			raw, ok := plan.BindVars[expression.Value.BindKey].(string)
			if !ok {
				return "", fmt.Errorf("%w: %s bind %q must be a non-empty string identity bind", ErrUnsupportedPostgresPlan, label, expression.Value.BindKey)
			}
			index := r.textParameter(raw, "text:"+raw)
			return "to_jsonb($" + strconv.Itoa(index) + "::text)", nil
		}
		resolved, err := r.resolveValue(*expression.Value)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrUnsupportedPostgresPlan, label, err)
		}
		if isPayloadExpression(resolved) {
			resolved = "(" + resolved + ")"
		}
		return resolved, nil
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil || expression.Extract.Prepared != nil {
			return "", fmt.Errorf("%w: %s is a prepared extract, which has no registered PostgreSQL rendering", ErrUnsupportedPostgresPlan, label)
		}
		classified, err := classifyExtractionProjections([]ir.PhysicalProjection{{Name: label, Expression: &expression}}, func(variable string) (string, bool) {
			rowScope, ok := r.rows[variable]
			return rowScope.resourceType, ok
		})
		if err != nil {
			return "", err
		}
		if len(classified) != 1 {
			return "", fmt.Errorf("%w: %s did not classify as one projection", ErrUnsupportedPostgresPlan, label)
		}
		switch classified[0].kind {
		case extractionDirectValue, extractionValueExpression:
			value := classified[0].projection.Value
			if classified[0].kind == extractionValueExpression {
				value = *classified[0].projection.Expression.Value
			}
			resolved, err := r.resolveValue(value)
			if err != nil {
				return "", fmt.Errorf("%w: %s: %w", ErrUnsupportedPostgresPlan, label, err)
			}
			if isPayloadExpression(resolved) {
				resolved = "(" + resolved + ")"
			}
			return resolved, nil
		case extractionSelector:
			scope := r.rows[classified[0].expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, classified[0].path, classified[0].expression.NullBehavior)
			if err != nil {
				return "", fmt.Errorf("%w: %s: %w", ErrUnsupportedPostgresPlan, label, err)
			}
			return value, nil
		default:
			return "", fmt.Errorf("%w: %s kind %q has no registered cell-trace rendering",
				ErrUnsupportedPostgresPlan, label, string(expression.Kind))
		}
	default:
		return "", fmt.Errorf("%w: %s kind %q has no registered cell-trace rendering", ErrUnsupportedPostgresPlan, label, string(expression.Kind))
	}
}
