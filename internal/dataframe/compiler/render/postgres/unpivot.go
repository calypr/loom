package postgres

// This file registers the terminal melt as an implemented, closed PostgreSQL
// operation (pg-sql-15): PhysicalUnpivot expanded through one CROSS JOIN
// LATERAL VALUES list that carries one slot per authored input column in
// authored order — the renderer-facing capability registration point for the
// operation the backend-neutral plan names. The AQL behavior being matched is
// render/aql/table_reshape.go renderTableUnpivot (lines 502-620), the SOLE
// emitter of the operation shape; its consumers are render/aql/render.go:571
// (post-window dispatch via renderTableReshape, table_reshape.go:11-26),
// render/aql/stage_sequence.go:379-383 (the construction-stage arm calling
// the SAME emitter — PostgreSQL keeps its plan.StageSequence refusal, so
// that surface stays a typed gap citing this emitter), and the
// window/layout readers render/aql/navigation_layout.go:179,253-254,376-378.
// render/aql/cell_trace.go:383 renderUnpivotValueTraceQuery is a DIFFERENT
// shape (cell-trace evidence return, not the melt) and cell trace is
// unregistered for PostgreSQL (pg-sql-24 pending): no PG path renders it. The
// exclusion witness read (compiler/table_shape_exclusions.go:84 →
// traceReshapeApplies, table_reshape.go:622-627) is never populated here;
// PG carries no exclusion channel, so D6 stays a gap by absence of producer.
//
// The PostgreSQL spelling chosen so each AQL guarantee has an exact SQL
// counterpart rather than an approximation:
//
//   - One output row per (source row, authored input) pair: the VALUES list
//     is built over unpivot.Inputs in plan order, the counterpart of the AQL
//     slot array (table_reshape.go:511-531), whose order is the authored
//     input order. The slot ordinality integers are renderer-owned
//     constants; the ORDER BY witness unpivotOrdinals (window.go) makes the
//     authored order deterministic on every execution.
//   - The key is the declared key literal from the plan bind — ir type-
//     checks it against KeyKind (ir/physical_table_shape.go
//     validateTableScalarBind) and the renderer re-reads and re-marshals it
//     as one JSONB parameter. AQL renders the same bind into every slot
//     (slot {key: @KeyBindKey}).
//   - The value is the input column's canonical projection expression — the
//     single contract of classifyExtractionProjections (selectors.go)
//     rendered exactly as renderReturn renders it — wrapped in to_jsonb so
//     every VALUES row is homogeneous jsonb. Under PRESERVE_NULL extraction
//     the tri-state stays observable apart (SQL NULL for absent, jsonb
//     'null' for a stored JSON null) exactly as null_semantics.go maps the
//     stored states; AQL's object-property read collapses the two
//     (table_reshape.go:516), so this is a declared superset refinement
//     (D3), never a regression.
//   - DROP drops BOTH states: the AQL FILTER slot.value != null
//     (table_reshape.go:539-541) drops absent and stored JSON null alike
//     because the object read already folded them; the PostgreSQL conjunct
//     slot_value IS NOT NULL AND slot_value <> 'null'::jsonb drops SQL NULL
//     and the jsonb 'null' scalar alike. PRESERVE emits both, distinguished
//     only by the tri-state.
//   - The value-type check is the per-row counterpart of the AQL FILTER
//     ASSERT (table_reshape.go:538) with the SAME tag
//     TABLE_UNPIVOT_VALUE_TYPE_MISMATCH: a CASE whose violating arm raises
//     through population.go's guardCast barrier (population.go:1059-1113),
//     which prevents plan-time constant folding of the cast (the sabotage
//     pattern population.go documents and proves live). Because the AQL
//     ASSERT lives inside the slot loop, the per-row CASE is its exact
//     position: zero source rows means the check never fires in either
//     backend. Conjunct order is immaterial: a raising row is non-null
//     wrong-typed, so the DROP conjunct (which removes only SQL NULL and
//     the jsonb null scalar) never hides it, and null rows pass the witness
//     — the order_qual_clauses lesson from population.go applies to a plain
//     per-row qual with no sublink to prune.
//   - Stable row identity is the counterpart of TO_STRING([parts]) at
//     table_reshape.go:543-559: renderer-composed JSON-compact concatenation
//     over ['[' , to_jsonb($name), value-text, ']'] pairs — the identity
//     part names and the construction ID as TEXT parameters JSON-encoded by
//     to_jsonb, input-row values through COALESCE(to_jsonb(expr), 'null')
//     so both null states render the four characters null exactly as the
//     AQL object read collapses them, the construction pair
//     [constructionID, "TABLE_UNPIVOT"], and the key pair
//     [Inputs[0].KeyKind, slot.key] replicating the deliberate
//     first-input-kind spelling of table_reshape.go:557. Byte-exactness is
//     claimed against independent literal expected rows only: Arango's
//     TO_STRING rendering of exotic numbers and control characters is a
//     QUARANTINED unverified runtime fact and is never load-bearing here
//     (fixtures use deterministic ASCII identities, the same bound
//     window.go declares for collation). INTEGER keys outside ±2^53 and
//     non-finite DECIMAL keys are refused: beyond the double round-trip the
//     two backends' number spellings are unproven, and divergence must
//     fail closed.
//
// The slice is deliberately closed. The plan is exactly one direct ROOT_SCAN,
// exactly one UNPIVOT (ir/physical_validation.go:319-323 already refuses a
// second table reshape), an optional stable-identity SORT then LIMIT window,
// and a terminal RETURN whose every projection reads only the melt output
// variable by one name. Coexisting FILTER, TRAVERSAL, UNNEST, SET,
// subplans, EXPRESSION_LET, cell trace, or any other operation — and the
// richer AQL spines they form (D5) — are capability gaps: typed
// ErrUnsupportedPostgresPlan refusals that yield no SQL. The window arm is
// W3: SORT must be exactly one key {OutputRowVariable, ["__loom_row_id"]} —
// the ir reshaped-window guard's contract (ir/physical_validation.go:117-123,
// pinned by internal/dataframe/compiler/table_reshape_execution_window_test
// .go:62-102) — mapped to the renderer's identity expression, and LIMIT
// requires it (ir/physical_validation.go:125-129). window.go stays the single
// SORT/LIMIT contract owner: the hasUnpivot flag and the slot-ordinality
// witness list are registered through the same seam precedent as
// unnest.go's hasUnnest (unnest.go:670), and window.go's W1/W2 arms are
// untouched. The dedicated unpivotOrdinals witness (rather than reusing the
// unnest lateralAliases witnesses) keeps every existing ORDER BY term
// byte-identical: the expansion exposes slot_ord, not rn, and the unnest
// has-item term would be a vacuous constant here.
//
// validateUnpivotPlanSurface is the single contract definition, applied as
// the first plan-facing act at both entry points: RenderUnpivot applies it
// before delegation, and RenderPhysicalPlan — the read-spine dispatch —
// applies it before plan validation, scope loading, or any rendering once a
// plan mentions UNPIVOT, re-applying at the melt as a local invariant. The
// dispatch entry is a full backstop refusing the same shapes with the same
// typed error and message (defense in depth, not two contracts — the
// unnest.go:93-102 precedent).
//
// Planner-facing command registration (the selected compiler target) stays
// fail-closed for PostgreSQL exactly as in pg-sql-01/02/03/09/18: this entry
// point — not command dispatch — is where the unpivot operation is
// implemented and registered.
//
// Parameter discipline is inherited unchanged: everything the plan carries
// (key literals, identity part names, the construction ID, selector paths,
// scope binds) reaches PostgreSQL as ordered $n parameters through the
// package's existing binders — document paths through
// documentPathFromExpression (null_semantics.go, the single path-binding
// authority), keys and values through addParameter/jsonbParameter. The
// 'null'::jsonb coercions, the typeof constants, the "TABLE_UNPIVOT" tag,
// the key-kind literals, and the slot ordinals are renderer-owned contract
// constants fixed by this file, never plan data; expansion aliases are
// renderer-owned counters. No plan string is ever interpolated into SQL text.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// unpivotValueTypeTag is the canonical AQL assertion tag (table_reshape.go
// :538) raised verbatim inside a 22P02 driver message through guardCast.
const unpivotValueTypeTag = "TABLE_UNPIVOT_VALUE_TYPE_MISMATCH"

// unpivotRowIDColumn is the compiler-owned stable row identity column the
// reshaped execution SORT and the melt output contract address.
const unpivotRowIDColumn = "__loom_row_id"

// unpivotValueTypeOf maps the frozen table scalar kind to the closed
// jsonb_typeof constant matching aqlTableScalarType's TYPENAME mapping
// (table_reshape.go:693-704): STRING→string, INTEGER/DECIMAL→number,
// BOOLEAN→bool/boolean. The map's existence is the refusal: an unknown kind
// is not rendered.
var unpivotValueTypeOf = map[string]string{
	"STRING":  "string",
	"INTEGER": "number",
	"DECIMAL": "number",
	"BOOLEAN": "boolean",
}

// RenderUnpivot renders exactly one scoped direct root scan melted by one
// registered UNPIVOT onto the trusted PostgreSQL storage binding.
//
// The accepted plan shape is one leading ROOT_SCAN with a direct scan
// payload (no population, cohort, or candidate-seed source), exactly one
// UNNEST-free melt as registered above, an optional SORT then LIMIT
// execution window in the reshaped-window form, and a terminal RETURN whose
// projections read only the melt output variable by single names. Every
// failure is typed: ErrUnsupportedPostgresPlan for shapes outside this
// slice, ErrInvalidPostgresPlanScope for missing or contradictory scope
// binds, and ir.ErrUnregisteredPostgresIdentifier for a binding that did
// not come from ir.NewPostgresStorageBinding. A rejected plan never produces
// SQL and never routes to AQL. The result is deterministic: the same plan
// and binding always yield byte-identical SQL and value-identical ordered
// arguments.
//
// The closed shape passing here means RenderPhysicalPlan's own gates
// (binding trust, plan validation including validatePhysicalUnpivot,
// engine/ClickHouse/stage refusals, scope re-reading, deterministic
// assembly) all operate over a plan this slice has already pinned, and the
// core dispatch re-applies the same surface contract at the melt. There is
// no second SQL builder by design: one statement-shape authority keeps this
// registration and the read spine byte-identical for the shapes they share.
func RenderUnpivot(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateUnpivotPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, storage)
}

// validateUnpivotPlanSurface enforces the closed plan slice before any
// rendering work begins. Every refusal is an ErrUnsupportedPostgresPlan so
// callers classify the whole family as a capability gap, and payload-nil
// checks run before any dereference so a malformed plan can never reach a
// payload dereference downstream (the validateUnnestPlanSurface ordering
// discipline).
func validateUnpivotPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	sawUnpivot, sawReturn := false, false
	sawSort, sawLimit := false, false
	var melt *ir.PhysicalUnpivot
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the unpivot slice leads with exactly one ROOT_SCAN at position 0, got one at %d", ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: operation %d: ROOT_SCAN payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalUnpivotOp:
			if sawUnpivot {
				return fmt.Errorf("%w: operation %d: the unpivot slice registers exactly one melt; a second table reshape has no PostgreSQL rendering (multiple melts are construction stage sequences, which stay refused)", ErrUnsupportedPostgresPlan, index)
			}
			sawUnpivot = true
			if index == 0 {
				return fmt.Errorf("%w: operation %d: UNPIVOT cannot run before the rows it melts", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: UNPIVOT must precede the execution window; melting after SORT/LIMIT would window the pre-melt rows the window contract orders", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Unpivot == nil {
				return fmt.Errorf("%w: operation %d: UNPIVOT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotOperationShape(plan, *operation.Unpivot); err != nil {
				return fmt.Errorf("operation %d: %w", index, err)
			}
			melt = operation.Unpivot
		case ir.PhysicalSortOp:
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: SORT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnpivot {
				return fmt.Errorf("%w: operation %d: SORT must follow the melt; the reshaped window orders melted rows, not source rows", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: the unpivot window admits at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotSortKeys(*operation.Sort, *melt); err != nil {
				return err
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: LIMIT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnpivot {
				return fmt.Errorf("%w: operation %d: LIMIT must follow the melt; the window bounds melted rows, not source rows", ErrUnsupportedPostgresPlan, index)
			}
			if sawLimit {
				return fmt.Errorf("%w: operation %d: the unpivot window admits at most one LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if !sawSort {
				return fmt.Errorf("%w: operation %d: a LIMIT after UNPIVOT requires the stable row identity SORT first (the ir reshaped-window guard's contract, re-read at the renderer)", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			sawReturn = true
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: RETURN must be the final operation of the unpivot slice", ErrUnsupportedPostgresPlan)
			}
			if operation.Return == nil {
				return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
			}
			if melt == nil {
				return fmt.Errorf("%w: RETURN at %d precedes the melt; the registered slice reads melt outputs after UNPIVOT", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotReturn(*operation.Return, *melt); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with UNPIVOT in the registered slice; filters, traversals, unnests, sets, subplans, expression lets, cell trace, and every other shape belong to the read spine or a later capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawUnpivot {
		return fmt.Errorf("%w: the unpivot slice requires exactly one UNPIVOT between ROOT_SCAN and RETURN", ErrUnsupportedPostgresPlan)
	}
	if !sawReturn {
		return fmt.Errorf("%w: the unpivot slice requires a terminal RETURN after its melt", ErrUnsupportedPostgresPlan)
	}
	return nil
}

// validateUnpivotRootScan re-reads the direct root scan exactly as the spine
// and the other registered slices bind it before any melt renders over its
// rows.
func validateUnpivotRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
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

// validateUnpivotOperationShape enforces the structural half of the closed
// melt payload with re-read, never trusted, identity facts: the registry
// membership of every kind, the non-empty variable and column names ir's
// frozen patterns will also check, the construction-ID equality, and the
// shapes the renderer dereferences. ir.Validate carries the authoritative
// semantic gate (ir/physical_table_shape.go validatePhysicalUnpivot); this
// is the renderer-side boundary that keeps the slice closed and refuses the
// same shapes with the capability-gap type at the dispatch entry.
func validateUnpivotOperationShape(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot) error {
	if unpivot.InputRowVariable == "" || unpivot.SlotVariable == "" || unpivot.OutputRowVariable == "" {
		return fmt.Errorf("%w: unpivot requires input-row, slot, and output-row variables", ErrUnsupportedPostgresPlan)
	}
	if unpivot.InputRowVariable == unpivot.OutputRowVariable || unpivot.SlotVariable == unpivot.OutputRowVariable || unpivot.SlotVariable == unpivot.InputRowVariable {
		return fmt.Errorf("%w: unpivot variables must be distinct; they name distinct SQL placements", ErrUnsupportedPostgresPlan)
	}
	if len(unpivot.InputProjections) == 0 || len(unpivot.Inputs) == 0 || len(unpivot.IdentityParts) == 0 {
		return fmt.Errorf("%w: unpivot requires input projections, at least one selected input, and identity parts", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(unpivot.ConstructionID) == "" {
		return fmt.Errorf("%w: unpivot construction ID is required", ErrUnsupportedPostgresPlan)
	}
	if _, ok := plan.BindVars[unpivot.ConstructionIDBindKey].(string); !ok {
		return fmt.Errorf("%w: unpivot construction ID bind %q must carry a string", ErrUnsupportedPostgresPlan, unpivot.ConstructionIDBindKey)
	}
	if plan.BindVars[unpivot.ConstructionIDBindKey].(string) != unpivot.ConstructionID {
		return fmt.Errorf("%w: unpivot construction ID bind does not match construction metadata", ErrUnsupportedPostgresPlan)
	}
	if unpivot.KeyOutput == "" || unpivot.ValueOutput == "" || unpivot.KeyOutput == unpivot.ValueOutput {
		return fmt.Errorf("%w: unpivot key and value outputs must be non-empty and distinct", ErrUnsupportedPostgresPlan)
	}
	if _, ok := unpivotValueTypeOf[unpivot.KeyType]; !ok {
		return fmt.Errorf("%w: unpivot key type %q is outside the closed scalar registry", ErrUnsupportedPostgresPlan, unpivot.KeyType)
	}
	if _, ok := unpivotValueTypeOf[unpivot.ValueType]; !ok {
		return fmt.Errorf("%w: unpivot value type %q is outside the closed scalar registry", ErrUnsupportedPostgresPlan, unpivot.ValueType)
	}
	if unpivot.NullRowPolicy != "DROP" && unpivot.NullRowPolicy != "PRESERVE" {
		return fmt.Errorf("%w: unsupported null-row policy %q", ErrUnsupportedPostgresPlan, unpivot.NullRowPolicy)
	}
	projectionNames := make(map[string]bool, len(unpivot.InputProjections))
	for _, projection := range unpivot.InputProjections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: unpivot input projection names are required", ErrUnsupportedPostgresPlan)
		}
		if projectionNames[projection.Name] {
			return fmt.Errorf("%w: unpivot input projection %q is duplicated", ErrUnsupportedPostgresPlan, projection.Name)
		}
		projectionNames[projection.Name] = true
	}
	if projectionNames[unpivot.KeyOutput] || projectionNames[unpivot.ValueOutput] {
		return fmt.Errorf("%w: unpivot key or value output collides with an input projection", ErrUnsupportedPostgresPlan)
	}
	selected := make(map[string]bool, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		if input.Column == "" || !projectionNames[input.Column] {
			return fmt.Errorf("%w: unpivot input column %q is not an input projection", ErrUnsupportedPostgresPlan, input.Column)
		}
		if selected[input.Column] {
			return fmt.Errorf("%w: unpivot input column %q is duplicated", ErrUnsupportedPostgresPlan, input.Column)
		}
		selected[input.Column] = true
		if input.KeyBindKey == "" {
			return fmt.Errorf("%w: unpivot input %q requires a key bind", ErrUnsupportedPostgresPlan, input.Column)
		}
		if input.KeyKind != unpivot.KeyType {
			return fmt.Errorf("%w: unpivot input %q key kind %q does not match the canonical key type %q",
				ErrUnsupportedPostgresPlan, input.Column, input.KeyKind, unpivot.KeyType)
		}
		if !unpivotValueKindCompatible(unpivot.ValueType, input.ValueKind) {
			return fmt.Errorf("%w: unpivot input %q value kind %q is incompatible with the output type %q",
				ErrUnsupportedPostgresPlan, input.Column, input.ValueKind, unpivot.ValueType)
		}
	}
	preservedInputs := make(map[string]bool, len(unpivot.PreservedOutputs))
	preservedOutputs := make(map[string]bool, len(unpivot.PreservedOutputs))
	for _, output := range unpivot.PreservedOutputs {
		if output.InputColumn == "" || output.OutputColumn == "" {
			return fmt.Errorf("%w: unpivot preserved output mapping is incomplete", ErrUnsupportedPostgresPlan)
		}
		if !selected[output.InputColumn] && !projectionNames[output.InputColumn] {
			return fmt.Errorf("%w: unpivot preserved input %q is not an input projection", ErrUnsupportedPostgresPlan, output.InputColumn)
		}
		if preservedInputs[output.InputColumn] || preservedOutputs[output.OutputColumn] {
			return fmt.Errorf("%w: unpivot preserved output mapping duplicates %q", ErrUnsupportedPostgresPlan, output.InputColumn)
		}
		if output.OutputColumn == unpivot.KeyOutput || output.OutputColumn == unpivot.ValueOutput {
			return fmt.Errorf("%w: unpivot preserved output %q collides with the key or value output", ErrUnsupportedPostgresPlan, output.OutputColumn)
		}
		preservedInputs[output.InputColumn], preservedOutputs[output.OutputColumn] = true, true
	}
	identityNames := make(map[string]bool, len(unpivot.IdentityParts))
	for _, part := range unpivot.IdentityParts {
		if part.Name == "" || identityNames[part.Name] {
			return fmt.Errorf("%w: unpivot identity part name is empty or duplicated", ErrUnsupportedPostgresPlan)
		}
		identityNames[part.Name] = true
		if part.Value.Variable == unpivot.InputRowVariable {
			if len(part.Value.Path) != 1 || !projectionNames[part.Value.Path[0]] {
				return fmt.Errorf("%w: unpivot identity part %q references an input column that is not an input projection",
					ErrUnsupportedPostgresPlan, part.Name)
			}
			continue
		}
		if part.Value.Variable != "" || len(part.Value.Path) != 0 || part.Value.BindKey == "" {
			return fmt.Errorf("%w: unpivot identity part %q must reference the input row by one column or a scalar bind",
				ErrUnsupportedPostgresPlan, part.Name)
		}
	}
	return nil
}

// unpivotValueKindCompatible mirrors the closed INTEGER/DECIMAL widening of
// ir tableScalarKindsCompatible: the renderer re-reads, never trusts.
func unpivotValueKindCompatible(output, input string) bool {
	if output == input {
		return true
	}
	return (output == "DECIMAL" && input == "INTEGER") || (output == "INTEGER" && input == "DECIMAL")
}

// validateUnpivotSortKeys re-reads the reshaped-window SORT contract at the
// surface: exactly one key, the melt output variable's stable identity
// column, no bind (ir/physical_validation.go:117-123; the AQL counterpart is
// the layout guard navigation_layout.go renders into SORT
// <output>.__loom_row_id ASC).
func validateUnpivotSortKeys(sort ir.PhysicalSort, unpivot ir.PhysicalUnpivot) error {
	if len(sort.Keys) != 1 {
		return fmt.Errorf("%w: the reshaped execution SORT must carry exactly the stable row identity key", ErrUnsupportedPostgresPlan)
	}
	key := sort.Keys[0]
	if key.Variable != unpivot.OutputRowVariable || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != unpivotRowIDColumn {
		return fmt.Errorf("%w: the reshaped execution SORT must sort on %s.%s exactly",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable, unpivotRowIDColumn)
	}
	return nil
}

// validateUnpivotReturn pins the terminal projection list to the melt output
// contract: every projection reads only the melt output variable, by one
// legal output name, in direct value form.
func validateUnpivotReturn(ret ir.PhysicalReturn, unpivot ir.PhysicalUnpivot) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unpivot slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	legal := unpivotLegalOutputNames(unpivot)
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: melt projection %q uses %s; the melt RETURN reads only registered output columns by direct value",
				ErrUnsupportedPostgresPlan, projection.Name, unpivotProjectionForm(projection))
		}
		if projection.Value.BindKey != "" {
			return fmt.Errorf("%w: melt projection %q must read an output column, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Value.Variable != unpivot.OutputRowVariable {
			return fmt.Errorf("%w: melt projection %q reads variable %q; the melt RETURN reads only the melt output variable %q",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Variable, unpivot.OutputRowVariable)
		}
		if len(projection.Value.Path) != 1 {
			return fmt.Errorf("%w: melt projection %q must read the output column by exactly one name", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if !legal[projection.Value.Path[0]] {
			return fmt.Errorf("%w: melt projection %q reads %q, which is not a registered melt output",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Path[0])
		}
	}
	return nil
}

func unpivotProjectionForm(projection ir.PhysicalProjection) string {
	switch {
	case projection.PresenceOutput:
		return "presence-sidecar metadata"
	case projection.Presence != nil:
		return "presence metadata"
	default:
		return fmt.Sprintf("expression kind %q", string(projection.Expression.Kind))
	}
}

// unpivotLegalOutputNames is the closed output-name set of one melt: the key
// and value outputs, the stable row identity column, and every preserved
// input under its mapped output name (the PreservedOutputs contract ir
// already proved covers each retained input exactly).
func unpivotLegalOutputNames(unpivot ir.PhysicalUnpivot) map[string]bool {
	legal := map[string]bool{unpivot.KeyOutput: true, unpivot.ValueOutput: true, unpivotRowIDColumn: true}
	for _, output := range unpivot.PreservedOutputs {
		legal[output.OutputColumn] = true
	}
	return legal
}

// renderUnpivot renders the registered melt as one CROSS JOIN LATERAL VALUES
// expansion over the authored inputs. It is invoked only through the core
// dispatch, whose RenderPhysicalPlan entry pins the whole plan to the
// registered slice before any plan validation or rendering once a plan
// mentions UNPIVOT; this re-check keeps that invariant local, so one
// contract stands at both entry points.
func (r *planRenderer) renderUnpivot(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot) error {
	if r.unpivotSeq == 0 {
		if err := validateUnpivotPlanSurface(plan); err != nil {
			return err
		}
	}
	if err := validateUnpivotOperationShape(plan, unpivot); err != nil {
		return err
	}
	if _, taken := r.unpivotOutputs[unpivot.OutputRowVariable]; taken {
		return fmt.Errorf("%w: melt output variable %q is already bound; melt outputs name distinct SQL placements",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable)
	}
	if _, taken := r.rows[unpivot.OutputRowVariable]; taken {
		return fmt.Errorf("%w: melt output variable %q collides with a stored row variable",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable)
	}

	// The input columns are the spine's own projection contract: classify
	// with the single definition and render each expression exactly as
	// renderReturn would, so the melt can never build a second extractor.
	classified, err := classifyExtractionProjections(unpivot.InputProjections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return fmt.Errorf("unpivot input projections: %w", err)
	}
	inputExprs := make(map[string]string, len(classified))
	for _, projection := range classified {
		var expression string
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			resolved, err := r.resolveValue(value)
			if err != nil {
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionPresenceSidecar:
			scope := r.rows[projection.presence.Source.Variable]
			presence, err := r.propertyPresenceExpression(scope.alias, projection.presencePath)
			if err != nil {
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = presence
		}
		inputExprs[projection.projection.Name] = expression
	}

	r.unpivotSeq++
	alias := "x" + strconv.Itoa(r.unpivotSeq)

	// The VALUES rows: one per authored input in authored order — the
	// counterpart of the AQL slot array (table_reshape.go:511-531). The key
	// is the declared literal, re-read and re-marshalled as one JSONB
	// parameter; the value is the canonical expression wrapped in to_jsonb
	// so the VALUES column is homogeneously jsonb (typed-column inputs such
	// as the canonical logical id read as JSON strings, the counterpart of
	// the AQL object property carrying the spine's own rendered value).
	rows := make([]string, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		raw, err := unpivotKeyJSON(plan.BindVars[input.KeyBindKey], input.KeyKind)
		if err != nil {
			return fmt.Errorf("unpivot input %q: %w", input.Column, err)
		}
		keyIndex := r.jsonbParameter(raw)
		expression, ok := inputExprs[input.Column]
		if !ok {
			return fmt.Errorf("%w: unpivot input %q has no rendered projection expression", ErrUnsupportedPostgresPlan, input.Column)
		}
		rows = append(rows, fmt.Sprintf("($%d::jsonb, to_jsonb(%s), %d)", keyIndex, expression, len(rows)+1))
	}
	lateral := "CROSS JOIN LATERAL (VALUES " + strings.Join(rows, ", ") + ") AS " +
		alias + "(slot_key, slot_value, slot_ord)"
	r.laterals = append(r.laterals, lateral)

	// The per-row type witness (the AQL FILTER ASSERT counterpart,
	// table_reshape.go:538) and the null-row policy. Conjunct order is
	// immaterial: a raising row is non-null wrong-typed and the DROP
	// conjunct removes only null states, so no planner qual reordering can
	// hide a violation, and null rows pass the witness.
	slotValue := alias + ".slot_value"
	valueTypeOf := unpivotValueTypeOf[unpivot.ValueType]
	bad := "(" + slotValue + " IS NOT NULL AND " + slotValue + " <> 'null'::jsonb AND jsonb_typeof(" + slotValue + ") <> " + quoteSQLLiteral(valueTypeOf) + ")"
	// The comparison tail and the all-integer branch set are the
	// population.go guard idiom (guard+" = 1", population.go:1023-1035):
	// PostgreSQL requires one resolvable type across CASE branches, so an
	// ELSE true against the guardCast integer aborts at parse time
	// (SQLSTATE 42804) and never plans.
	r.where = append(r.where, "(CASE WHEN "+bad+" THEN "+guardCast(unpivotValueTypeTag)+" ELSE 1 END) = 1")
	if unpivot.NullRowPolicy == "DROP" {
		r.where = append(r.where, "("+slotValue+" IS NOT NULL AND "+slotValue+" <> 'null'::jsonb)")
	}

	// The melt output contract: each legal output name maps to the SQL
	// expression that emits it. The RETURN dispatch (render.go's single
	// routed line) reads only this map, so a melt output can never be
	// resolved through the stored-row path by accident.
	columns := map[string]string{
		unpivot.KeyOutput:   alias + ".slot_key",
		unpivot.ValueOutput: slotValue,
	}
	preservedNames := make(map[string]string, len(unpivot.PreservedOutputs))
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	selectedColumns := make(map[string]bool, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		selectedColumns[input.Column] = true
	}
	for _, projection := range unpivot.InputProjections {
		if selectedColumns[projection.Name] {
			// A melted input leaves the source row: the AQL output object
			// skips selected columns entirely (table_reshape.go:561-573).
			continue
		}
		if projection.Name == unpivotRowIDColumn {
			// The source row identity never passes through: the melt emits
			// its own identity (table_reshape.go:571), and the AQL output
			// row's __loom_row_id is the computed melt identity.
			continue
		}
		outputName := projection.Name
		if renamed, ok := preservedNames[projection.Name]; ok {
			outputName = renamed
		}
		expression, ok := inputExprs[projection.Name]
		if !ok {
			return fmt.Errorf("%w: unpivot preserved column %q has no rendered projection expression", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if _, taken := columns[outputName]; taken {
			return fmt.Errorf("%w: melt output column %q is emitted twice", ErrUnsupportedPostgresPlan, outputName)
		}
		columns[outputName] = expression
	}
	columns[unpivotRowIDColumn] = r.unpivotIdentityExpression(plan, unpivot, inputExprs, alias)
	if columns[unpivotRowIDColumn] == "" {
		return fmt.Errorf("%w: unpivot identity expression failed", ErrUnsupportedPostgresPlan)
	}
	if r.unpivotOutputs == nil {
		r.unpivotOutputs = map[string]map[string]string{}
	}
	r.unpivotOutputs[unpivot.OutputRowVariable] = columns

	// Window registration through the sanctioned seam (unnest.go:670's
	// hasUnnest precedent): W3 orders by the renderer's identity expression
	// (mapped in renderFinalWindowSortUnpivot) and appends exactly the
	// slot-ordinality witness below. The dedicated list — not the unnest
	// lateralAliases witnesses — keeps every existing ORDER BY term
	// byte-identical: the expansion exposes slot_ord, not rn, and the
	// has-item term would be a vacuous constant on a VALUES expansion that
	// never pads.
	r.window.hasUnpivot = true
	r.window.unpivotOrdinals = append(r.window.unpivotOrdinals, alias+".slot_ord")
	return nil
}

// unpivotKeyJSON re-reads one key bind under the closed per-kind type table
// (matching ir validateTableScalarBind at physical_table_shape.go:437-464)
// and encodes it as the canonical JSON the slot carries. The renderer's own
// encoder is the authority; the DB never re-spells the key. Integers beyond
// the double round-trip and non-finite decimals refuse (D4): their two-
// backend spellings are unproven, and divergence fails closed.
func unpivotKeyJSON(value any, kind string) (json.RawMessage, error) {
	switch kind {
	case "STRING":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under STRING", ErrUnsupportedPostgresPlan, value)
		}
		return marshalUnpivotKey(text)
	case "INTEGER":
		var integer int64
		switch typed := value.(type) {
		case int:
			integer = int64(typed)
		case int8:
			integer = int64(typed)
		case int16:
			integer = int64(typed)
		case int32:
			integer = int64(typed)
		case int64:
			integer = typed
		case uint:
			integer = int64(typed)
		case uint8:
			integer = int64(typed)
		case uint16:
			integer = int64(typed)
		case uint32:
			integer = int64(typed)
		case uint64:
			if typed > 1<<53 {
				return nil, fmt.Errorf("%w: unpivot INTEGER key %d exceeds the 2^53 double round-trip bound", ErrUnsupportedPostgresPlan, typed)
			}
			integer = int64(typed)
		default:
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under INTEGER", ErrUnsupportedPostgresPlan, value)
		}
		if integer > 1<<53 || integer < -(1<<53) {
			return nil, fmt.Errorf("%w: unpivot INTEGER key %d exceeds the 2^53 double round-trip bound", ErrUnsupportedPostgresPlan, integer)
		}
		return marshalUnpivotKey(integer)
	case "DECIMAL":
		var number float64
		switch typed := value.(type) {
		case float32:
			number = float64(typed)
		case float64:
			number = typed
		default:
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under DECIMAL", ErrUnsupportedPostgresPlan, value)
		}
		if !(number == number) || number > 1.7976931348623157e308 || number < -1.7976931348623157e308 {
			return nil, fmt.Errorf("%w: unpivot DECIMAL key bind must be finite", ErrUnsupportedPostgresPlan)
		}
		return marshalUnpivotKey(number)
	case "BOOLEAN":
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under BOOLEAN", ErrUnsupportedPostgresPlan, value)
		}
		return marshalUnpivotKey(boolean)
	default:
		return nil, fmt.Errorf("%w: unpivot key kind %q is outside the closed registry", ErrUnsupportedPostgresPlan, kind)
	}
}

func marshalUnpivotKey(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return nil, fmt.Errorf("%w: unpivot key is not JSON-encodable", ErrUnsupportedPostgresPlan)
	}
	return raw, nil
}

// unpivotIdentityExpression composes the counterpart of the AQL stable row
// identity, TO_STRING([pairs]) (table_reshape.go:543-559): the identity
// parts in plan order, then [constructionID, "TABLE_UNPIVOT"], then
// [Inputs[0].KeyKind, slot.key] with the deliberate first-input-kind
// spelling. Names and the construction ID cross the parameter boundary as
// TEXT parameters and are JSON-encoded by to_jsonb; input-row values render
// through COALESCE(to_jsonb(expr), 'null') so absent and stored JSON null
// both read the four characters null, exactly as the AQL object read
// collapses them. Every part is total: three-valued logic never reaches the
// concatenation.
func (r *planRenderer) unpivotIdentityExpression(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot, inputExprs map[string]string, alias string) string {
	pairs := make([]string, 0, len(unpivot.IdentityParts)+2)
	for _, part := range unpivot.IdentityParts {
		nameIndex := r.textParameter(part.Name, "unpivot-identity-name:"+part.Name)
		var valueText string
		if part.Value.Variable == unpivot.InputRowVariable {
			expression, ok := inputExprs[part.Value.Path[0]]
			if !ok {
				return ""
			}
			valueText = "COALESCE(to_jsonb(" + expression + "), 'null'::jsonb)::text"
		} else {
			// Scalar identity binds are plan data: re-read, never trust. The
			// melt accepts string binds only (the canonical "project" leg
			// lowering emits); other Go types have unproven two-backend
			// spellings and fail closed.
			bound, ok := plan.BindVars[part.Value.BindKey]
			if !ok {
				return ""
			}
			text, ok := bound.(string)
			if !ok {
				return ""
			}
			bindIndex := r.textParameter(text, "unpivot-identity-bind:"+part.Value.BindKey+":"+text)
			valueText = "to_jsonb($" + strconv.Itoa(bindIndex) + "::text)::text"
		}
		pairs = append(pairs, "'[' || to_jsonb($"+strconv.Itoa(nameIndex)+"::text) || ',' || "+valueText+" || ']'")
	}
	constructionIndex := r.textParameter(unpivot.ConstructionID, "unpivot-construction-id:"+unpivot.ConstructionID)
	pairs = append(pairs, "'[' || to_jsonb($"+strconv.Itoa(constructionIndex)+"::text) || ',' || "+quoteSQLLiteral(`"TABLE_UNPIVOT"`)+" || ']'")
	keyKind := unpivot.Inputs[0].KeyKind
	pairs = append(pairs, "'[' || "+quoteSQLLiteral(`"`+keyKind+`"`)+" || ',' || "+alias+".slot_key::text || ']'")
	return "'[' || " + strings.Join(pairs, " || ',' || ") + " || ']'"
}

// resolveUnpivotOutputValue maps one melt output read onto its registered
// SQL expression. It is reached only through resolveValue's routed branch,
// and it refuses anything the melt output contract does not register: bind
// values, multi-segment paths, and unknown names never resolve.
func (r *planRenderer) resolveUnpivotOutputValue(columns map[string]string, value ir.PhysicalValue) (string, error) {
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: melt reads must name an output column, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(value.Path) != 1 {
		return "", fmt.Errorf("%w: melt reads must name exactly one output column, got %v", ErrUnsupportedPostgresPlan, value.Path)
	}
	expression, ok := columns[value.Path[0]]
	if !ok {
		return "", fmt.Errorf("%w: %q is not a registered melt output", ErrUnsupportedPostgresPlan, value.Path[0])
	}
	return "(" + expression + ")", nil
}

// renderUnpivotReturn assembles the positional projection list for a melted
// plan. It is the melt counterpart of renderReturn (render.go routes here
// only when a melt rendered), over the same positional c<n> contract so the
// assembler is untouched; every projection passed validateUnpivotReturn at
// the surface, and the checks stand again here as the local invariant.
func (r *planRenderer) renderUnpivotReturn(ret ir.PhysicalReturn) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unpivot slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: melt projection %q carries %s", ErrUnsupportedPostgresPlan, projection.Name, unpivotProjectionForm(projection))
		}
		columns, ok := r.unpivotOutputs[projection.Value.Variable]
		if !ok {
			return fmt.Errorf("%w: melt projection %q reads variable %q, which never bound a melt",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Variable)
		}
		expression, err := r.resolveUnpivotOutputValue(columns, projection.Value)
		if err != nil {
			return fmt.Errorf("projection %q: %w", projection.Name, err)
		}
		r.selects = append(r.selects, expression+` AS "c`+strconv.Itoa(len(r.selects))+`"`)
		r.outputColumns = append(r.outputColumns, projection.Name)
	}
	return nil
}

// renderFinalWindowSortUnpivot is the W3 SORT entry point, called from
// window.go's single SORT dispatch when the registered melt rendered
// (window.go owns the contract; this is the melt's contribution to it, the
// counterpart of renderFinalWindowSort for W2). The plan key must equal the
// stable row identity exactly — surface gate re-check — and the ORDER BY
// term is the renderer's own identity expression, never a plan string.
func (r *planRenderer) renderFinalWindowSortUnpivot(sort ir.PhysicalSort) error {
	if r.window.sortSeen {
		return fmt.Errorf("%w: at most one SORT", ErrUnsupportedPostgresPlan)
	}
	if len(r.window.unpivotOrdinals) == 0 {
		return fmt.Errorf("%w: the melt window orders rows before the melt bound", ErrUnsupportedPostgresPlan)
	}
	if len(sort.Keys) != 1 {
		return fmt.Errorf("%w: the reshaped execution SORT must carry exactly the stable row identity key", ErrUnsupportedPostgresPlan)
	}
	key := sort.Keys[0]
	columns, ok := r.unpivotOutputs[key.Variable]
	if !ok || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != unpivotRowIDColumn {
		return fmt.Errorf("%w: the reshaped execution SORT must sort on the melt stable row identity column", ErrUnsupportedPostgresPlan)
	}
	identity, ok := columns[unpivotRowIDColumn]
	if !ok {
		return fmt.Errorf("%w: the melt stable row identity column is not registered", ErrUnsupportedPostgresPlan)
	}
	r.window.keys = append(r.window.keys, "("+identity+") ASC")
	return nil
}
