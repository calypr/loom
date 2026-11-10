package postgres

// This file registers the JSON-array cardinality boundary as an implemented,
// closed PostgreSQL operation (pg-sql-09). It is the renderer-facing
// capability registration point for the operation named by the physical IR's
// PhysicalUnnest: one direct root scan whose rows are expanded through
// jsonb_array_elements ... WITH ORDINALITY lateral joins, carrying typed
// duplicate-preserving item identity, zero-based stable ordinality, retained
// parent and repeated-ancestor identity, and the missing / JSON-null /
// SQL-NULL distinctions of null_semantics.go. The AQL behavior being matched
// is render/aql/navigation_render.go renderUnnest: a source is evaluated
// once per parent row and normalized to [] on null; INNER (EXCLUDE) emits no
// rows for an empty source; OUTER (PRESERVE_PARENT) emits exactly one parent
// row with a null item; indexed loops preserve duplicate values and a stable
// zero-based position; ancestors ride a carrier object captured at each
// repeated prefix step.
//
// The PostgreSQL spelling is chosen so each AQL guarantee has an exact SQL
// counterpart rather than an approximation:
//
//   - INNER is CROSS JOIN LATERAL over one expansion chain; OUTER is the
//     same subquery as LEFT JOIN LATERAL ... ON true, which yields exactly
//     one row with every derived column SQL NULL when the expansion is
//     empty — the counterpart of AQL's [null] index sentinel.
//   - The ordinality column is ROW_NUMBER() OVER (ORDER BY every step's
//     WITH-ORDINALITY position) - 1 over the rows that survive the
//     terminal null-item filter. AQL numbers items after every loop and
//     filter (its index variable is assigned post-FILTER), so the index is
//     the position among surviving items, not the stored array position:
//     [1, null, 2] yields ordinality 0 and 1 in both backends. The row
//     number is dense, zero-based, stable across executions, and
//     duplicate-distinguishing (WITH ORDINALITY assigns a position per
//     array element, so equal jsonb items remain distinct rows).
//   - has-item is (rn IS NOT NULL): true for every real item and false
//     exactly on the OUTER pad row. The AQL reference drops stored JSON
//     null items at the terminal position (terminal FILTER ... != null),
//     so [null] is an empty source — INNER emits no rows and OUTER pads
//     one — and no emitted item is ever the jsonb 'null' scalar.
//   - Each repeated prefix selector step expands through its own chained
//     CROSS JOIN LATERAL jsonb_array_elements ... WITH ORDINALITY, the exact
//     counterpart of AQL's nested FOR loops; an ancestor column is the
//     enclosing expansion's captured value, the counterpart of the carrier
//     object, so a correlated projection reads the exact enclosing item.
//   - Source normalization reproduces the AQL iterate ternary
//     (cur.field ? cur.field : []) exactly, for every step including the
//     terminal one: AQL's falsy set is null, false, 0, and "", so SQL NULL
//     (absent document or missing key), the JSON null scalar, and stored
//     false/0/"" all read as the empty array with no error, at prefix and
//     terminal alike. A truthy non-array at a prefix expansion, or at a
//     terminal expansion that has ancestors, reaches jsonb_array_elements
//     and raises at execution, matching AQL's FOR-over-non-array collection
//     error and its lineage FOR loop; only an ancestor-free terminal
//     expansion turns a truthy non-array into one item, matching AQL's
//     RETURN [value] that FLATTEN unwraps back to that single item —
//     renderUnnest dispatches the terminal source form on ancestor presence
//     for exactly this reason. Missing, JSON-null,
//     and SQL-NULL payload all read as the empty source, the same
//     null-or-missing source collapse the AQL renderer applies and
//     null_semantics.go documents. Parity is bounded by ir.Validate's
//     schema-validated selector: FHIR element arrays are never arrays of
//     arrays, so the AQL reference's one-level FLATTEN over such stored
//     data is outside the contract and arrays pass through unflattened.
//
// The ORDER BY contract extends the spine's identity total order:
// resource_seq, then every edge_seq, then every expansion rn in plan order,
// so duplicate items keep their source-array position on every execution —
// the rendering counterpart of ir.PhysicalUnnestSortKeys' has-item and
// ordinality tie-breaks. While an execution window is open, window.go also
// places each expansion's (rn IS NOT NULL) has-item witness immediately
// before its rn, so a page cut by the bound LIMIT positions the OUTER pad
// row ahead of its group's items, exactly where the AQL tuple's
// has-item-before-ordinality order puts it.
//
// The slice is deliberately closed. The plan is exactly one direct
// ROOT_SCAN, one or more UNNEST operations, an optional SORT then LIMIT
// execution window, and a terminal RETURN; the ERROR empty policy, selector
// filters, indexed or non-property steps, prepared references, ordered
// fallbacks, unit normalization, DISTINCT, route-step scope operations,
// conditionally-array selector modes, presence or hidden projections,
// selector projections, and coexisting filters, traversals, or any other
// operation are capability gaps: typed ErrUnsupportedPostgresPlan refusals
// that yield no SQL. The SORT/LIMIT window arms admit only the closed
// sequence [ROOT_SCAN, UNNEST+, SORT?, LIMIT?, RETURN]; their key contract —
// the complete stable row identity tuple over final expanded rows — is the
// single contract of window.go (pg-sql-12), which this gate defers to. As
// part of that window contract, edge joins are registered once per edge
// variable (window.go's rememberEdge): re-binding one variable to a second
// join would silently point the edge_seq occurrence witness at the later
// duplicate join, so the registration refuses it — a defense-in-depth pin of
// the same invariant ir.Validate already enforces at the plan surface
// ("variable is already defined"), witnessed by
// TestWindowEdgeVariableRebindIsRefusedAtRegistration. There is no fallback
// to the AQL renderer and no AQL-to-SQL conversion here. validateUnnestPlanSurface
// is the single contract definition, applied as the first plan-facing act at
// both entry points: RenderUnnest applies it before delegation, and
// RenderPhysicalPlan — the read-spine dispatch — applies it before plan
// validation, scope loading, or any rendering once a plan mentions UNNEST,
// re-applying at the first UNNEST as a local invariant. The dispatch entry
// is therefore a full backstop that refuses the same shapes with the same
// typed error and message, not a partial one (witnessed by
// TestUnnestDispatchGateIsFullBackstop; defense in depth, not two
// contracts).
//
// Planner-facing command registration (the selected compiler target)
// stays fail-closed for PostgreSQL exactly as in pg-sql-01/02/03: this entry
// point — not command dispatch — is where the unnest operation is
// implemented and registered.
//
// Parameter discipline is inherited unchanged: everything the plan carries
// (selector path segments, resource types, edge label and type binds, scope
// binds) reaches PostgreSQL as ordered $n parameters through the package's
// existing binders — selector paths through documentPathFromExpression
// (null_semantics.go, the single path-binding authority), scope and route
// binds through renderTraversal's re-read paths. The '[]', 'null', and
// 'array' literals in the expansion CASE forms are renderer-owned contract
// constants fixed by this file, never plan data; lateral and expansion
// aliases are renderer-owned counters. No plan string is ever interpolated
// into SQL text.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderUnnest renders exactly one scoped direct root scan expanded by one
// or more JSON-array unnest operations onto the trusted PostgreSQL storage
// binding.
//
// The accepted plan shape is one leading ROOT_SCAN with a direct scan
// payload (no population, cohort, or candidate-seed source), one or more
// UNNEST operations, and a terminal RETURN. Every UNNEST must carry the
// registered shape: EXCLUDE or PRESERVE_PARENT empty policy (ERROR fails
// closed as an unregistered capability), an ARRAY-cardinality EXTRACT source
// over the owner row document at ["payload"] with the owner's exact resource
// type, property-only selector steps with a repeated ([]) terminal step,
// ancestors restricted to distinct repeated prefix steps, and an owner route
// of native typed-identity traversals with empty scope. RETURN projections
// may read stored-row values and the renderer-derived item, ancestor,
// ordinality, and has-item values through direct or VALUE-expression forms
// only; presence sidecars, hidden projections, and selector projections are
// outside this slice. Plan scope binds are re-read through the canonical
// mapping in scope.go, never trusted downstream.
//
// The result is deterministic: the same plan and binding always yield
// byte-identical SQL and value-identical ordered arguments. Failures are
// typed: ErrUnsupportedPostgresPlan for shapes outside this slice,
// ErrInvalidPostgresPlanScope for missing or contradictory scope binds, and
// ir.ErrUnregisteredPostgresIdentifier for a binding that did not come from
// ir.NewPostgresStorageBinding. A rejected plan never produces SQL and never
// routes to AQL.
func RenderUnnest(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateUnnestPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	// The closed shape passed, so RenderPhysicalPlan's own gates (binding
	// trust, plan validation including schema validation of the selector,
	// engine/ClickHouse/stage refusals, scope re-reading, deterministic
	// assembly) all operate over a plan this slice has already pinned, and
	// the core dispatch re-applies the same surface contract at the first
	// UNNEST. There is no second SQL builder here by design: one
	// statement-shape authority keeps this registration and the read spine
	// byte-identical for the shapes they share.
	return RenderPhysicalPlan(plan, storage)
}

// validateUnnestPlanSurface enforces the closed plan slice before any
// rendering work begins. Every refusal is an ErrUnsupportedPostgresPlan so
// callers classify the whole family as a capability gap, and payload-nil
// checks run before any dereference so a malformed plan can never reach a
// payload dereference downstream — the same ordering discipline
// validateRootScanOperation applies.
func validateUnnestPlanSurface(plan ir.PhysicalPlan) error {
	sawUnnest, sawReturn := false, false
	sawSort, sawLimit := false, false
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the unnest slice leads with exactly one ROOT_SCAN at position 0, got one at %d", ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: operation %d: ROOT_SCAN payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnnestRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalUnnestOp:
			sawUnnest = true
			if index == 0 {
				return fmt.Errorf("%w: operation %d: UNNEST cannot run before the rows it expands", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: UNNEST must precede the execution window; expansion after SORT/LIMIT would window the pre-expansion rows the window contract orders", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: UNNEST payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnnestOperationShape(plan, *operation.Unnest); err != nil {
				return fmt.Errorf("operation %d: %w", index, err)
			}
		case ir.PhysicalSortOp:
			// The window arms of the closed slice keep the sequence
			// [ROOT_SCAN, UNNEST+, SORT?, LIMIT?, RETURN]; the key contract
			// (the exact stable row identity tuple) is enforced at render
			// time in window.go, the single window contract owner.
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: SORT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnnest {
				return fmt.Errorf("%w: operation %d: SORT must follow every UNNEST; the window orders final expanded rows, not pre-expansion sources", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: the unnest window admits at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: LIMIT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnnest {
				return fmt.Errorf("%w: operation %d: LIMIT must follow every UNNEST; the window bounds final expanded rows, not pre-expansion sources", ErrUnsupportedPostgresPlan, index)
			}
			if sawLimit {
				return fmt.Errorf("%w: operation %d: the unnest window admits at most one LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if !sawSort {
				return fmt.Errorf("%w: operation %d: a LIMIT after UNNEST requires the complete stable row identity SORT first", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			sawReturn = true
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: RETURN must be the final operation of the unnest slice", ErrUnsupportedPostgresPlan)
			}
			if operation.Return == nil {
				return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
			}
			if err := validateUnnestReturn(*operation.Return); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with UNNEST in the registered slice; filters, traversal operations, and every other shape beyond the SORT/LIMIT window belong to the read spine or a later capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawUnnest {
		return fmt.Errorf("%w: the unnest slice requires at least one UNNEST operation between ROOT_SCAN and RETURN", ErrUnsupportedPostgresPlan)
	}
	if !sawReturn {
		return fmt.Errorf("%w: the unnest slice requires a terminal RETURN after its expansions", ErrUnsupportedPostgresPlan)
	}
	// Derived scopes name distinct SQL outputs, so expansion output
	// variables (item, ordinality, has-item, ancestors) must be pairwise
	// distinct and distinct from every stored row variable. Checked
	// statically here so both entry points refuse with the capability-gap
	// type; registerDerivedValue keeps its runtime check as the
	// second line.
	bound := map[string]string{plan.Operations[0].RootScan.Variable: "root scan row"}
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalUnnestOp {
			continue
		}
		unnest := operation.Unnest
		outputs := []struct {
			role, variable string
		}{
			{"item", unnest.OutputVariable},
			{"item-discriminator", unnest.HasItemVariable},
			{"ordinality", unnest.Ordinality},
		}
		for _, ancestor := range unnest.Ancestors {
			outputs = append(outputs, struct {
				role, variable string
			}{"ancestor", ancestor.Variable})
		}
		for _, output := range outputs {
			if output.variable == "" {
				continue
			}
			if ownerRole, taken := bound[output.variable]; taken {
				return fmt.Errorf("%w: operation %d binds %s variable %q, already bound as the %s; expansion outputs must be unique",
					ErrUnsupportedPostgresPlan, index, output.role, output.variable, ownerRole)
			}
			bound[output.variable] = "operation " + strconv.Itoa(index) + " " + output.role
		}
	}
	return nil
}

// validateUnnestRootScan re-reads the direct root scan exactly as the spine
// binds it before any expansion renders over its rows.
func validateUnnestRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if scan.Variable == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if scan.CollectionBindKey == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	if resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	return nil
}

// validateUnnestReturn pins RETURN to the forms this slice can emit from
// registered row and derived scopes. classifyExtractionProjections remains
// the single projection contract during assembly; these are the additional
// slice rules, applied before delegation exactly the way the
// selector-extraction slice adds its own rules on top of the shared
// contract.
func validateUnnestReturn(ret ir.PhysicalReturn) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unnest slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Hidden {
			return fmt.Errorf("%w: projection %q is hidden executor-side metadata with no registered PostgreSQL rendering",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: projection %q carries presence metadata; presence proofs pair with selector extractions, which are outside the unnest slice",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Expression != nil {
			if projection.Expression.Kind != ir.PhysicalValueExpression || projection.Expression.Value == nil {
				return fmt.Errorf("%w: projection %q uses expression kind %q; the unnest slice emits direct values and VALUE expressions only, and selector projections over expanded items belong to a later capability",
					ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.Kind))
			}
			if projection.Expression.Value.BindKey != "" {
				return fmt.Errorf("%w: projection %q must read a row or derived value, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
			}
			continue
		}
		if projection.Value.BindKey != "" {
			return fmt.Errorf("%w: projection %q must read a row or derived value, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Value.Variable == "" {
			return fmt.Errorf("%w: projection %q must read exactly one typed row or derived value", ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return nil
}

// validateUnnestOperationShape enforces the structural half of the closed
// UNNEST payload: the registered expansion form this slice renders, with
// every richer shape refused by name. ir.Validate has already schema-checked
// the selector and the owner route chain; this is the renderer-side
// boundary that keeps the slice closed and re-reads, never trusts, the
// identity facts the SQL is built from.
func validateUnnestOperationShape(plan ir.PhysicalPlan, unnest ir.PhysicalUnnest) error {
	owner := unnest.Owner
	if owner.RootVariable == "" || owner.OwnerVariable == "" || unnest.OutputVariable == "" || unnest.HasItemVariable == "" {
		return fmt.Errorf("%w: unnest requires root, owner, output, and item-discriminator variables", ErrUnsupportedPostgresPlan)
	}
	switch unnest.EmptyPolicy {
	case ir.PhysicalUnnestExclude, ir.PhysicalUnnestPreserveParent:
	case ir.PhysicalUnnestError:
		return fmt.Errorf("%w: ERROR unnest policy has no proven PostgreSQL contract yet (AQL's ASSERT-style typed raise has no pure-SQL primitive); it is registered as a capability gap rather than approximated",
			ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown unnest empty policy %q", ErrUnsupportedPostgresPlan, string(unnest.EmptyPolicy))
	}

	// Owner route: native typed-identity traversals only, each with an
	// empty scope. renderTraversal remains the sole join emitter; ANY
	// direction, endpoint lookup, and unknown strategies refuse there as
	// they do on the spine, and route-step scope operations never render.
	previous := owner.RootVariable
	for stepIndex, step := range owner.Route {
		if strings.TrimSpace(step.OccurrenceID) == "" {
			return fmt.Errorf("%w: unnest owner route step %d is missing an occurrence ID", ErrUnsupportedPostgresPlan, stepIndex)
		}
		if len(step.Scope) != 0 {
			return fmt.Errorf("%w: unnest owner route step %d carries %d scope operations, which have no registered PostgreSQL rendering",
				ErrUnsupportedPostgresPlan, stepIndex, len(step.Scope))
		}
		switch step.Traversal.Direction {
		case ir.PhysicalOutbound, ir.PhysicalInbound:
		default:
			return fmt.Errorf("%w: unnest owner route step %d has direction %q, which has no proven PostgreSQL contract",
				ErrUnsupportedPostgresPlan, stepIndex, string(step.Traversal.Direction))
		}
		if step.Traversal.Strategy != "" && step.Traversal.Strategy != ir.PhysicalTraversalNative {
			return fmt.Errorf("%w: unnest owner route step %d has strategy %q; native typed-identity joins are the only registered shape",
				ErrUnsupportedPostgresPlan, stepIndex, string(step.Traversal.Strategy))
		}
		if step.Traversal.SourceVariable != previous {
			return fmt.Errorf("%w: unnest owner route step %d starts at %q, want %q",
				ErrUnsupportedPostgresPlan, stepIndex, step.Traversal.SourceVariable, previous)
		}
		previous = step.Traversal.TargetVariable
	}
	if previous != owner.OwnerVariable {
		return fmt.Errorf("%w: unnest route terminates at %q, not owner %q", ErrUnsupportedPostgresPlan, previous, owner.OwnerVariable)
	}

	expression := unnest.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
		return fmt.Errorf("%w: unnest source must be an EXTRACT selector over the owner row document; value and other expression sources belong to a later capability",
			ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return fmt.Errorf("%w: unnest source expression must be array-valued, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: unnest source null behavior %q has no registered PostgreSQL form; the expansion normalizes null-or-missing sources per the AQL contract and scalar null behaviors belong to value projections",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	extract := expression.Extract
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric:
	case ir.PhysicalSelectorDirectScalar:
		return fmt.Errorf("%w: DIRECT_SCALAR selector mode contradicts a repeated terminal step", ErrUnsupportedPostgresPlan)
	case ir.PhysicalSelectorConditionalArray:
		return fmt.Errorf("%w: CONDITIONAL_ARRAY selector mode requires conditional-array rendering, which is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown selector execution mode %q", ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Prepared != nil {
		return fmt.Errorf("%w: prepared-set selector references are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return fmt.Errorf("%w: ordered fallback selectors are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return fmt.Errorf("%w: unit-normalized extraction is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Distinct {
		return fmt.Errorf("%w: DISTINCT expansion is a separate capability: deduplication would erase the duplicate-item preservation this operation exists to provide",
			ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return fmt.Errorf("%w: selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Source.BindKey != "" {
		return fmt.Errorf("%w: the unnest source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return fmt.Errorf("%w: the unnest source must be the owner row document at [\"payload\"], the exact source shape lowering emits (lower/row_expansion.go); got %v",
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	if extract.Source.Variable != owner.OwnerVariable {
		return fmt.Errorf("%w: unnest source reads variable %q, want the exact owner occurrence %q",
			ErrUnsupportedPostgresPlan, extract.Source.Variable, owner.OwnerVariable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != owner.ResourceType {
		return fmt.Errorf("%w: unnest selector schema type %q must name the owner resource type %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, owner.ResourceType)
	}

	steps := extract.Selector.Steps
	if len(steps) == 0 {
		return fmt.Errorf("%w: the unnest selector is empty", ErrUnsupportedPostgresPlan)
	}
	for stepIndex, step := range steps {
		if step.Field == "" {
			return fmt.Errorf("%w: unnest selector step %d has no property", ErrUnsupportedPostgresPlan, stepIndex)
		}
		if step.Index != nil {
			return fmt.Errorf("%w: unnest selector step %d is indexed; positional selection belongs to a later capability",
				ErrUnsupportedPostgresPlan, stepIndex)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return fmt.Errorf("%w: unnest selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
	}
	if !steps[len(steps)-1].Iterate {
		return fmt.Errorf("%w: the unnest selector must end with a repeated ([]) step, so expansion has a defined terminal cardinality",
			ErrUnsupportedPostgresPlan)
	}
	seenAncestorSteps := make(map[int]bool, len(unnest.Ancestors))
	for ancestorIndex, ancestor := range unnest.Ancestors {
		if ancestor.StepIndex < 0 || ancestor.StepIndex >= len(steps)-1 || !steps[ancestor.StepIndex].Iterate {
			return fmt.Errorf("%w: unnest ancestor %d does not identify a repeated prefix selector step",
				ErrUnsupportedPostgresPlan, ancestorIndex)
		}
		if seenAncestorSteps[ancestor.StepIndex] {
			return fmt.Errorf("%w: unnest repeats ancestor selector step %d", ErrUnsupportedPostgresPlan, ancestor.StepIndex)
		}
		seenAncestorSteps[ancestor.StepIndex] = true
	}
	return nil
}

// renderUnnest renders one registered UNNEST as a lateral expansion chain.
// It is invoked only through the core dispatch, whose RenderPhysicalPlan
// entry pins the whole plan to the registered slice before any plan
// validation or rendering once a plan mentions UNNEST; this re-check keeps
// that invariant local, so one contract stands at both entry points.
func (r *planRenderer) renderUnnest(plan ir.PhysicalPlan, unnest ir.PhysicalUnnest) error {
	if r.unnestSeq == 0 {
		if err := validateUnnestPlanSurface(plan); err != nil {
			return err
		}
	}
	if err := validateUnnestOperationShape(plan, unnest); err != nil {
		return err
	}

	// Owner route through the traversal slice's sole join emitter, in its
	// route-inner mode: the AQL reference renders the route as the inner
	// renderTraversalScan FOR loops inside the current loop (render/aql
	// navigation_render.go renderUnnest), so a parent with zero surviving
	// route occurrences emits zero rows under BOTH empty policies — the
	// PreserveParent pad row belongs to the expansion, which the route
	// gates. Each step appends a top-level INNER JOIN edge × target before
	// the expansion laterals, so the parent row identity (and every
	// edge_seq tie-break) is the storage identity the ORDER BY contract
	// uses. The gate above already proved the static root-to-owner chain;
	// renderTraversal still refuses any step whose source never bound a
	// row.
	for stepIndex, step := range unnest.Owner.Route {
		if err := r.renderTraversal(plan, step.Traversal, true); err != nil {
			return fmt.Errorf("%w: unnest owner route step %d: %v", ErrUnsupportedPostgresPlan, stepIndex, err)
		}
	}
	ownerScope, ok := r.rows[unnest.Owner.OwnerVariable]
	if !ok || ownerScope.derivedValue != "" {
		return fmt.Errorf("%w: unnest owner %q does not bind a readable stored row", ErrUnsupportedPostgresPlan, unnest.Owner.OwnerVariable)
	}
	if ownerScope.resourceType != unnest.Owner.ResourceType {
		return fmt.Errorf("%w: unnest owner %q is scanned as %q, not the declared owner type %q",
			ErrUnsupportedPostgresPlan, unnest.Owner.OwnerVariable, ownerScope.resourceType, unnest.Owner.ResourceType)
	}

	// Split the property-only selector into one path per repeated step:
	// expansion k extracts along the segments since the previous repeated
	// step, inclusive of that step's field. The gate proved every step is
	// a property and the terminal step is the final repeated one.
	type expansion struct {
		path           []string
		iterateStep    int
		expansionAlias string
		valueColumn    string
	}
	var expansions []expansion
	current := make([]string, 0, 4)
	for stepIndex, step := range unnest.Expression.Extract.Selector.Steps {
		current = append(current, step.Field)
		if step.Iterate {
			expansions = append(expansions, expansion{
				path:        append([]string(nil), current...),
				iterateStep: stepIndex,
			})
			current = nil
		}
	}

	r.unnestSeq++
	lateralAlias := "u" + strconv.Itoa(r.unnestSeq)

	// Build the chained expansion. The first expansion extracts from the
	// owner row document; each later expansion extracts from the previous
	// expansion's element. Every expansion applies the AQL iterate ternary:
	// the falsy set (null, false, 0, "") reads as the empty array; the
	// terminal additionally wraps a truthy scalar as its single item and
	// drops stored JSON null items (the AQL terminal FILTER), while a
	// truthy scalar at a prefix expansion raises in PostgreSQL the way
	// AQL's FOR-over-scalar raises.
	var from strings.Builder
	orderByOrds := make([]string, 0, len(expansions))
	ancestorColumns := make([]string, len(unnest.Ancestors))
	base := r.documentColumn(ownerScope.alias)
	terminal := len(expansions) - 1
	for index, stepExpansion := range expansions {
		extraction, err := r.documentPathFromExpression(base, stepExpansion.path)
		if err != nil {
			return fmt.Errorf("unnest expansion %d: %w", index, err)
		}
		expansionAlias := lateralAlias + "_e" + strconv.Itoa(index)
		valueColumn := "value"
		var sourceForm string
		if index == terminal && len(unnest.Ancestors) == 0 {
			// The AQL reference dispatches on ancestor presence
			// (render/aql/navigation_render.go renderUnnestSource): with no
			// ancestors the terminal goes through the selector extraction
			// pipeline, whose RETURN + one-level FLATTEN turn a truthy
			// scalar into that single item; with ancestors every step,
			// terminal included, is a plain ternary FOR, so a truthy
			// scalar must reach jsonb_array_elements and raise, matching
			// AQL's FOR-over-non-array error.
			valueColumn = "item"
			sourceForm = unnestSingleLayerForm(extraction)
		} else {
			if index == terminal {
				valueColumn = "item"
			}
			sourceForm = unnestNullToEmptyForm(extraction)
		}
		fragment := "jsonb_array_elements(" + sourceForm + ") WITH ORDINALITY AS " +
			expansionAlias + "(" + valueColumn + ", ord)"
		if index == 0 {
			from.WriteString("FROM " + fragment)
		} else {
			from.WriteString(" CROSS JOIN LATERAL " + fragment)
		}
		expansions[index].expansionAlias = expansionAlias
		expansions[index].valueColumn = valueColumn
		orderByOrds = append(orderByOrds, expansionAlias+".ord")
		base = expansionAlias + ".value"
	}
	// Map each ancestor onto the expansion captured at its repeated step.
	for ancestorIndex, ancestor := range unnest.Ancestors {
		var captured string
		for _, stepExpansion := range expansions {
			if stepExpansion.iterateStep == ancestor.StepIndex {
				captured = stepExpansion.expansionAlias + "." + stepExpansion.valueColumn
				break
			}
		}
		if captured == "" {
			return fmt.Errorf("%w: unnest ancestor %d was not captured by an expansion", ErrUnsupportedPostgresPlan, ancestorIndex)
		}
		ancestorColumns[ancestorIndex] = captured + " AS ancestor_" + strconv.Itoa(ancestorIndex)
	}

	// The ordinality row number: the terminal item filter removes stored
	// JSON null items, exactly as the AQL reference does with its terminal
	// FILTER ... != null, so the surviving rows are numbered densely with
	// ROW_NUMBER over the WITH-ORDINALITY position tuples in step order —
	// AQL assigns its index variable after every loop and filter, so its
	// item index is the position among surviving items, not the stored
	// array position (1-based here; the derived ordinality column
	// subtracts one). WITH ORDINALITY still distinguishes equal items.
	rowNumber := "ROW_NUMBER() OVER (ORDER BY " + strings.Join(orderByOrds, ", ") + ") AS rn"
	selectList := expansions[terminal].expansionAlias + "." + expansions[terminal].valueColumn + " AS item"
	for _, column := range ancestorColumns {
		if column != "" {
			selectList += ", " + column
		}
	}
	selectList += ", " + rowNumber
	// Terminal null-item removal: the AQL reference drops stored JSON null
	// items at the terminal position (render/aql/selectors.go
	// flattenTerminalRepeatedValues FILTER v != null and
	// render/aql/navigation_render.go's terminal FILTER current != null),
	// so [null] contributes no items, [1, null, 2] yields ordinality
	// 0,1,... and an OUTER source that empties under the filter pads like
	// any other empty source. jsonb_array_elements never yields a SQL NULL
	// element, so the equality test cannot misfire on real items.
	terminalItem := expansions[terminal].expansionAlias + "." + expansions[terminal].valueColumn
	subquery := "SELECT " + selectList + " " + from.String() +
		" WHERE jsonb_typeof(" + terminalItem + ") <> 'null'"

	join := "CROSS JOIN LATERAL (" + subquery + ") AS " + lateralAlias
	if unnest.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		join = "LEFT JOIN LATERAL (" + subquery + ") AS " + lateralAlias + " ON true"
	}
	r.laterals = append(r.laterals, join)
	// Source-array stability: each expansion's rn is unique within its
	// parent group, so appending it after the storage identities makes the
	// total order deterministic and keeps duplicate items in array order.
	// The window's total order (window.go) consumes the minted alias and,
	// while a window is open, adds this expansion's has-item witness before
	// the rn.
	r.lateralAliases = append(r.lateralAliases, lateralAlias)
	r.window.hasUnnest = true

	// Derived value scopes: the item rides the jsonb column and is never
	// the jsonb 'null' scalar, because the terminal filter removed stored
	// null items exactly as the AQL reference's terminal FILTER does — the
	// only SQL NULL item is the OUTER pad row's, the counterpart of AQL's
	// [null] index sentinel; ordinality is zero-based with NULL only on
	// the pad row; has-item is two-valued and total, false exactly on the
	// pad row.
	if err := r.registerDerivedValue(unnest.OutputVariable, lateralAlias+".item", true); err != nil {
		return err
	}
	if unnest.Ordinality != "" {
		if err := r.registerDerivedValue(unnest.Ordinality, lateralAlias+".rn - 1", false); err != nil {
			return err
		}
	}
	if err := r.registerDerivedValue(unnest.HasItemVariable, lateralAlias+".rn IS NOT NULL", false); err != nil {
		return err
	}
	for ancestorIndex, ancestor := range unnest.Ancestors {
		if err := r.registerDerivedValue(ancestor.Variable, lateralAlias+".ancestor_"+strconv.Itoa(ancestorIndex), true); err != nil {
			return err
		}
	}
	return nil
}

// registerDerivedValue binds one renderer-computed value under its plan
// variable. Derived scopes carry an expression only: no alias, no resource
// type, and resolution solely through resolveValue, so they can never
// become join, filter, sort, or selector sources.
func (r *planRenderer) registerDerivedValue(variable, expression string, jsonValue bool) error {
	if _, taken := r.rows[variable]; taken {
		return fmt.Errorf("%w: unnest output variable %q is already bound; expansion output variables must be unique because they name distinct SQL outputs",
			ErrUnsupportedPostgresPlan, variable)
	}
	r.rows[variable] = rowScope{derivedValue: expression, derivedJSON: jsonValue}
	return nil
}

// resolveDerivedValue reads one plan value off a derived scope. The bare
// value is the computed expression; a non-empty path over a jsonb-derived
// value descends through documentPathFromExpression, the same path binder
// stored documents use; a path over a non-jsonb derived value (ordinality,
// has-item) has no meaningful reading and fails closed.
func (r *planRenderer) resolveDerivedValue(value ir.PhysicalValue, scope rowScope) (string, error) {
	if len(value.Path) == 0 {
		return "(" + scope.derivedValue + ")", nil
	}
	if !scope.derivedJSON {
		return "", fmt.Errorf("%w: value %q reads path %v of computed variable %q, which is not a JSON document",
			ErrUnsupportedPostgresPlan, value.Variable, value.Path, value.Variable)
	}
	expression, err := r.documentPathFromExpression(scope.derivedValue, value.Path)
	if err != nil {
		return "", err
	}
	return "(" + expression + ")", nil
}

// unnestFalsyArms is the AQL ternary's falsy set. AQL renders every iterate
// step as FOR v IN (cur.field ? cur.field : []), and AQL treats exactly
// null, false, 0, and "" as falsy (ArangoDB AQL typing), so a stored
// false/0/"" at an iterate field contributes zero rows with no error. JSON
// null and SQL NULL are folded by the first CASE arm; these arms carry the
// remaining two falsy values so the PostgreSQL expansion matches the AQL
// row count instead of raising or emitting rows. Numeric equality on jsonb
// covers every stored spelling of zero (0, 0.0, -0.0, 0e0).
const unnestFalsyArms = " WHEN (%s) = 'false'::jsonb OR (%s) = '0'::jsonb OR (%s) = '\"\"'::jsonb THEN '[]'::jsonb"

// unnestNullToEmptyForm is the prefix-expansion source form, the exact
// counterpart of AQL's iterate ternary (render/aql/navigation_render.go and
// render/aql/selectors.go renderSelectorArrayFromSource): SQL NULL (absent
// document, missing key, or a null/non-object parent along the path), the
// JSON null scalar, and the remaining falsy scalars read as the empty array;
// an array passes through; a truthy non-array passes through so
// jsonb_array_elements raises at execution, matching AQL's
// FOR-over-non-array collection error rather than inventing rows.
func unnestNullToEmptyForm(extraction string) string {
	return "CASE" +
		" WHEN (" + extraction + ") IS NULL OR jsonb_typeof((" + extraction + ")) = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof((" + extraction + ")) = 'array' THEN (" + extraction + ")" +
		unnestFalsyArmsWith(extraction) +
		" ELSE (" + extraction + ") END"
}

// unnestSingleLayerForm is the terminal-expansion source form. The AQL
// reference for the registered shape evaluates the terminal iterate step's
// ternary and then FLATTENs the returned values one level, dropping null
// items (render/aql/selectors.go flattenTerminalRepeatedValues and
// render/aql/navigation_render.go's terminal FILTER ... != null): the
// falsy set and null/missing read as the empty array, an array contributes
// its elements in stored order (null items removed by the expansion's
// WHERE, duplicates preserved by WITH ORDINALITY), and a truthy non-array
// becomes one single-element array — the RETURN [value] that FLATTEN
// unwraps back to that single item.
func unnestSingleLayerForm(extraction string) string {
	return "CASE" +
		" WHEN (" + extraction + ") IS NULL OR jsonb_typeof((" + extraction + ")) = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof((" + extraction + ")) = 'array' THEN (" + extraction + ")" +
		unnestFalsyArmsWith(extraction) +
		" ELSE jsonb_build_array((" + extraction + ")) END"
}

func unnestFalsyArmsWith(extraction string) string {
	return fmt.Sprintf(unnestFalsyArms, extraction, extraction, extraction)
}
