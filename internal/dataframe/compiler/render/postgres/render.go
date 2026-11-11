// Package postgres renders validated physical IR plans into parameterized
// PostgreSQL 18 statements for the source store (pg-ir-02).
//
// The renderer input is a physical plan plus an ir.PostgresStorageBinding.
// The binding is the only source of SQL identifiers: table and column names
// come from the compiler-owned allowlist in internal/dataframe/compiler/ir
// (postgres_storage.go), and an unregistered or hand-assembled identifier is
// rejected with ErrUnregisteredPostgresIdentifier. Everything else the plan
// carries — project, dataset generation, auth resource paths, resource
// types, edge labels, filter values, payload paths, limit values, and output
// column names — is runtime data and is passed to the driver as ordered,
// typed $n parameters. No plan string is ever interpolated into SQL text.
//
// The deterministic SORT/LIMIT execution window is registered in window.go
// (pg-sql-12) as the closed final slice: over the spine it keeps the merged
// typed-column contract; after UNNEST it orders and bounds the final
// expanded rows using the complete typed row identity — root and owner
// logical identities, storage occurrence witnesses, and the has-item and
// ordinality witnesses — never the root ID alone.
//
// Unsupported plans fail closed with ErrUnsupportedPostgresPlan; there is no
// fallback to the AQL renderer and no AQL-to-SQL conversion anywhere in this
// package. The rendered result is a command.RenderedPostgresPhysicalPlan,
// which additionally refuses AQL bind syntax and placeholder/argument
// mismatch at construction.
//
// The v1 supported surface is the scoped read spine: one direct root
// resource scan, typed scope predicates (explicit project, dataset
// generation, and an explicit auth mode), scalar filter comparisons —
// direct value comparisons over typed columns or JSONB payload paths with
// typed (spec value-kind-checked) bind values, and the closed
// selector-extraction comparisons whose ANY/NONE/ALL quantifier and
// null/missing reading preserve the physical-predicate contract (all
// predicate rendering and its capability registration live in
// predicates.go) — boolean ALL/ANY/NOT predicate trees, depth-one typed
// edge-occurrence traversals rendered by traversal.go — the OPTIONAL
// parent-preserving LEFT JOIN LATERAL child join, the REQUIRED match EXISTS
// semi-join (whose subplan payload filter re-reads the predicates.go
// comparison contract over the reached child rows), and the unnest
// owner-route step as an inner edge scan (renderTraversalScan parity: zero
// route occurrences eliminate the parent under either empty policy) — one
// column sort with deterministic identity tie-breaks, one bound limit, and
// one positional return projection. The JSON-array cardinality boundary
// (UNNEST, pg-sql-09) is registered in unnest.go as its own closed
// operation slice: one direct root scan, one or more JSON-array expansions
// with typed-identity owner routes, and a positional return over the
// stored rows plus the renderer-derived item, ancestor, ordinality, and
// has-item values. The membership population/cohort roots (pg-sql-18) are
// registered in population.go as their own closed entry slice: the
// constructor-only PostgresPopulationRootBinding (registered table allowlist
// pinned to the store migrations) makes RenderPopulationRootScan the sole
// binder for selection-bounded population roots and cohort-scoped roots,
// with the cohort guards rendered as an uncorrelated guard InitPlan that
// also carries the canonical deny; without a binding — every plain
// RenderPhysicalPlan path — membership roots remain capability gaps that
// return typed errors, never an AQL fallback. The correlated related-record
// sets (pg-sql-08) are registered in sets.go as their own closed operation
// slice: the child_sets.go child set with its FIRST/ALL/DISTINCT selector
// reductions renders as parent-preserving per-field scalar sublinks over
// the deduplicated child items, and the hop-only canonical RELATED
// existence shape (with its registered top-level NOT form) shares the
// validated correlated-hop renderer; production related-eligibility plans
// keep failing closed until the anchor prefix's subplan-argument call is
// registered (sets.go header). The REQUIRED match EXISTS stays owned by
// traversal.go unchanged. The terminal melt (UNPIVOT, pg-sql-15) is
// registered in unpivot.go as its own closed operation slice: one direct
// root scan, one CROSS JOIN LATERAL VALUES melt carrying one slot per
// authored input with the declared key literal, the value-type witness
// raising the canonical TABLE_UNPIVOT_VALUE_TYPE_MISMATCH tag through the
// guard barrier, the DROP-drops-null-rows / PRESERVE-keeps-them policy over
// the missing-versus-JSON-null tri-state, and the compiler-owned melt
// identity as the W3 stable order. The authored finite graph path union
// (pg-sql-20) is registered in graph.go as its own closed plan slice: one
// direct root scan with the canonical root scope block, typed root filters,
// and REQUIRED-match semi-joins, one PATH_SEED, one PATH_EXTEND per authored
// hop correlated to its parent path set (each carrying its canonical
// (edge, target) scope block and typed reached-row filters), and a terminal
// GRAPH_RETURN that renders the path-set union, deterministic identity
// deduplication, route order, and the bound limit+1 lookahead as one WITH
// statement — dispatched whole at the RenderPhysicalPlan entry, the
// structural twin of the AQL entry's pre-loop GRAPH_RETURN dispatch
// (render/aql/render.go:473-476). The nested object and bounded slice
// projections (pg-sql-11) are registered in objects.go as the closed
// composite RETURN surface: jsonb_build_object with bound name
// parameters, the per-field omission CASE at the AQL null reading, the
// bounded slice sublink with the logical_id tie-break over the singleton
// root row or a sets.go-validated child set (production compact-
// projection Extract.Prepared annotations resolve against that set's
// registered projection slots; everything else keeps the cited verbatim
// refusal), and recursion capped at depth 4. The coded Group tuple
// (CODED_GROUP, pg-sql-19) is registered in coded_group.go as its own
// closed plan slice: the inline single-stage shape lowers — one direct
// root scan with the canonical scope block and the spine's renderable
// source operations, and one authored CODED_GROUP stage — renders as one
// WITH statement: the guarded jsonb_array_elements expansion (AQL
// null/IS_ARRAY guard parity, the zero-item [null] candidate kept by LEFT
// JOIN), the total validity predicate with the space-only trim set of the
// ONE codedGroupTrimCharacters constant, the GROUP/EXCLUDE/ERROR
// missing-key policies (ERROR raising CONSTRUCTION_CODED_GROUP_MISSING_KEY
// per candidate through population.go's guardCast barrier at the AQL
// ASSERT position), per-root DISTINCT tuple dedup with COUNT(*) over
// contributing roots, the renderer-composed JSON-compact stable identity
// ordered COLLATE "C" under the bound preview limit (the AQL terminal
// identity-sort semantics, stage_sequence.go:399-448). Endpoint-lookup
// traversals,
// repeated/indexed selector predicates, correlated predicates,
// CONTAINS_TEXT, shared subsets, prepared sets, node-path sets, subplans
// outside the two registered correlated shapes, group rows,
// pivots, unpivots coexisting with filters, traversals, or unnests, every
// other stage sequence (composed stage sequences are pg-sql-16; coded-group
// row values are pg-sql-13), and ClickHouse payloads are capability gaps
// that return typed errors.
package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// ErrUnsupportedPostgresPlan reports a physical plan outside the closed v1
// PostgreSQL render surface. Callers must surface it as a capability gap;
// the renderer never falls back to AQL and never converts AQL to SQL.
var ErrUnsupportedPostgresPlan = errors.New("postgres renderer: unsupported physical plan")

// ErrInvalidPostgresPlanScope reports a plan whose explicit scope binds are
// missing or contradictory (blank project, absent auth mode flags, or an
// unrestricted mode carrying paths). Scope is never inferred and never
// widened; the canonical mapping itself lives in scope.go.
var ErrInvalidPostgresPlanScope = errors.New("postgres renderer: invalid plan scope")

// renderer-owned filterable column policy: single-segment document paths
// that map onto typed columns of the resource table. Any other path is a
// JSONB payload path and is bound as one whole text-path parameter. Storage
// identity sequences (resource_seq, edge_seq) are reserved for
// renderer-generated ordering and are never user-addressable columns.
// canonicalKeyColumn maps the Arango document-key references the shared
// lowering path uses (root "_key" and root document "id") onto the storage
// column that carries the same validated identity in the PostgreSQL store.
// Within one scoped root scan the generation-qualified key derives from the
// natural identity, so logical_id ordering and equality match the Arango
// document-key semantics exactly.
func canonicalKeyColumn(path string) string {
	switch path {
	case "_key", "id":
		return "logical_id"
	default:
		return path
	}
}

var resourceFilterColumns = map[string]bool{
	"project":            true,
	"dataset_generation": true,
	"resource_type":      true,
	"logical_id":         true,
	"auth_resource_path": true,
	"recorded_at":        true,
}

// RenderPhysicalPlan renders a validated physical plan onto the trusted
// PostgreSQL storage binding. The result is deterministic: the same plan and
// binding always yield byte-identical SQL and value-identical ordered
// arguments, and it never aliases plan data (every bound value is copied or
// re-encoded).
func RenderPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	// Whole-plan graph delegation (graph.go contract, pg-sql-20): a plan
	// mentioning PATH_SEED/PATH_EXTEND/GRAPH_RETURN is the authored graph
	// shape and is rendered WHOLE by the registered graph slice — the
	// structural twin of the AQL entry's pre-loop GRAPH_RETURN dispatch
	// (render/aql/render.go:473-476). No spine arm ever sees a path
	// operation; graph.go's validateGraphPlanSurface is the single contract
	// behind this door and behind RenderGraphPhysicalPlan (entry==dispatch
	// message equality), and every non-graph plan reaches the untouched
	// spine below exactly as merged.
	if graphPlanMentionsPathOperations(plan) {
		return renderGraphPhysicalPlan(plan, storage)
	}
	// Whole-plan coded-group delegation (coded_group.go contract,
	// pg-sql-19): a plan whose stage sequence mentions a CODED_GROUP stage
	// is rendered WHOLE by the registered slice — the structural twin of
	// the graph door above, with validateCodedGroupPlanSurface as the
	// single contract behind the door. No spine arm ever sees a
	// construction stage, and every other stage sequence reaches the
	// untouched StageSequence refusal below.
	if codedGroupPlanMentionsStage(plan) {
		return renderCodedGroupPhysicalPlan(plan, storage)
	}
	// Full-backstop defense in depth (unnest.go contract): a plan that
	// mentions UNNEST is pinned to the registered slice as the first
	// plan-facing act here, before plan validation, scope loading, or any
	// rendering can reject with an off-type error, so the dispatch entry
	// enforces one contract with RenderUnnest, not a partial backstop.
	// The re-check at the first UNNEST in renderUnnest stays as the local
	// invariant; the check is total over any plan, so a malformed non-unnest
	// plan reaches plan.Validate untouched.
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if err := validateUnnestPlanSurface(plan); err != nil {
				return command.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	// Full-backstop defense in depth (unpivot.go contract): a plan that
	// mentions UNPIVOT is pinned to the registered melt slice as the first
	// plan-facing act here, before plan validation, scope loading, or any
	// rendering can reject with an off-type error, so the dispatch entry
	// enforces one contract with RenderUnpivot, not a partial backstop. The
	// re-check at the melt in renderUnpivot stays the local invariant; the
	// check is total over any plan, so a malformed non-melt plan reaches
	// plan.Validate untouched.
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnpivotOp {
			if err := validateUnpivotPlanSurface(plan); err != nil {
				return command.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
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
	if plan.StageSequence != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: staged row-operator sequences are not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
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
	if err := renderer.renderOperations(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql := renderer.assemble()
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// rowScope records the renderer-owned SQL placement of one plan variable.
// The plan-authored variable name is used only as an internal map key; it
// never reaches SQL text. optional marks a row materialized by an OPTIONAL
// traversal join (traversal.go): it is NULL-extensible, so main-sequence
// FILTER and SORT may not reference it, while RETURN projections read its
// columns with SQL NULL for parent-preserving rows.
//
// A derived scope (unnest.go) carries a renderer-computed SQL expression
// instead of a storage alias: derivedValue is the only reading, there is no
// table alias or resource type, and only resolveValue may read it, so a
// derived variable can never be used as a join, filter column, or sort
// column. derivedJSON marks a jsonb-valued derived row whose non-empty
// paths are document paths over the derived value.
type rowScope struct {
	alias        string
	resourceType string
	derivedValue string
	derivedJSON  bool
	optional     bool
}

type planRenderer struct {
	storage ir.PostgresStorageBinding
	args    []command.PostgreSQLParameter
	// parameters deduplicates identical scalar scope binds (project,
	// generation, resource types, label, auth array) so repeated predicates
	// reference one placeholder.
	parameters map[string]int
	rows       map[string]rowScope
	// canonicalAuthLets maps each dropped canonical AUTH_RESOURCE_PATH_ALLOWED
	// LET variable to the registered scope row it was proven over
	// (scope_ops.go). The LET and its closing equality carry no PostgreSQL
	// meaning; loadScope owns the scope.
	canonicalAuthLets map[string]string

	// scope is the re-read canonical scope established by loadScope (its
	// definition and predicate mapping live in scope.go).
	scope     planScope
	rootAlias string
	// edgeAliases records the lateral aliases of OPTIONAL traversal joins in
	// plan order; the assembler orders every one by its exposed edge_seq so
	// repeated occurrences are deterministic on every execution.
	edgeAliases []string
	// traversalSeq is the single alias counter shared by traversal.go: every
	// registered traversal join (lateral child subquery or EXISTS step)
	// consumes one sequence number, so edge/target/lateral aliases are
	// unique and deterministic for a plan.
	traversalSeq  int
	where         []string
	joins         []string
	selects       []string
	outputColumns []string

	// window is the SORT/LIMIT state and total-order contract; its fields,
	// entry points, and ORDER BY assembly live in window.go (pg-sql-12).
	// Nothing outside window.go writes it.
	window executionWindow

	// Unnest state (unnest.go): lateral clauses in plan order, the
	// renderer-minted lateral aliases whose rn columns the window's total
	// order uses (window.go consumes them), and the expansion counter that
	// mints the aliases. All are renderer-owned; no plan string reaches SQL
	// text through them.
	laterals       []string
	lateralAliases []string
	unnestSeq      int

	// Melt state (unpivot.go, pg-sql-15): the expansion counter that mints
	// the VALUES-alias names, and the registered melt output contract —
	// melt output variable → output column name → the SQL expression that
	// emits it. resolveValue routes melt reads to resolveUnpivotOutputValue
	// and renderReturn routes a melted plan's projection list to
	// renderUnpivotReturn; with no melt rendered both fields stay nil and
	// the spine paths are untouched. All renderer-owned; no plan string
	// reaches SQL text through them.
	unpivotSeq     int
	unpivotOutputs map[string]map[string]string

	// membership is the trusted membership binding, set only by
	// RenderPopulationRootScan (population.go); nil keeps membership roots a
	// capability gap on the plain spine.
	membership *PostgresPopulationRootBinding

	// Composite state (objects.go, pg-sql-11): rootVariable names the
	// SPINE root-scan variable — the only registered singleton slice
	// source — set exactly where renderRootScan binds the direct-scan row
	// (membership roots delegate before it, so they never register one);
	// sliceSeq mints the renderer-owned aggregate aliases; setBodies
	// records each successfully validated child set's correlated body
	// (sets.go D2 hunk) so a registered SLICE over the set renders over
	// the same deduplicated item relation the reductions read;
	// setProjections records the same sets' registered traversal-time
	// projection slots (the compact-projection contract
	// lower/child_sets.go projectPhysicalChildSet stamps), which is what
	// a slice extract's Extract.Prepared reference resolves against.
	// All are renderer-owned; no plan string reaches SQL text through them.
	rootVariable   string
	sliceSeq       int
	setBodies      map[string]correlatedSetBody
	setProjections map[string][]ir.PhysicalSetProjectionField
}

// addParameter appends one ordered argument and returns its $n index. A
// non-empty cacheKey deduplicates equal scalar bindings.
func (r *planRenderer) addParameter(parameter command.PostgreSQLParameter, cacheKey string) int {
	if cacheKey != "" {
		if index, ok := r.parameters[cacheKey]; ok {
			return index
		}
	}
	r.args = append(r.args, parameter)
	index := len(r.args)
	if cacheKey != "" {
		r.parameters[cacheKey] = index
	}
	return index
}

func (r *planRenderer) textParameter(value, cacheKey string) int {
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: value}, cacheKey)
}

func (r *planRenderer) jsonbParameter(raw json.RawMessage) int {
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: append(json.RawMessage(nil), raw...)}, "")
}

func (r *planRenderer) renderOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	phase := 0 // 0 = source ops, 1 = window ops, 2 = return terminal
	sawSort, sawLimit := false, false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if phase != 0 || index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalFilterOp:
			if phase != 0 {
				return fmt.Errorf("%w: filters must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return err
			}
			if canonical {
				// loadScope already enforces this predicate from the
				// re-read canonical binds; the plan operation adds nothing.
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			if phase != 0 {
				return fmt.Errorf("%w: derived lets must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return err
			}
			if canonical {
				continue
			}
			return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalTraversalOp:
			if phase != 0 {
				return fmt.Errorf("%w: traversals must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderTraversal(plan, *operation.Traversal, false); err != nil {
				return err
			}
		case ir.PhysicalUnnestOp:
			if phase != 0 {
				return fmt.Errorf("%w: unnest must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: unnest payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderUnnest(plan, *operation.Unnest); err != nil {
				return err
			}
		case ir.PhysicalSetOp:
			if phase != 0 {
				return fmt.Errorf("%w: sets must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Set == nil {
				return fmt.Errorf("%w: operation %d: set payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderSet(plan, *operation.Set); err != nil {
				return err
			}
		case ir.PhysicalUnpivotOp:
			// The melt slice (unpivot.go, pg-sql-15): the closed sequence
			// [ROOT_SCAN, UNPIVOT, SORT?, LIMIT?, RETURN] is enforced by
			// validateUnpivotPlanSurface, applied at the entry above and
			// re-applied at the melt; this arm keeps positional dispatch.
			if phase != 0 {
				return fmt.Errorf("%w: unpivot must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Unpivot == nil {
				return fmt.Errorf("%w: operation %d: unpivot payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderUnpivot(plan, *operation.Unpivot); err != nil {
				return err
			}
		case ir.PhysicalSortOp:
			if sawSort || phase > 1 || (phase == 1 && sawLimit) {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			// The key contract (W1 typed columns vs. the W2 stable row
			// identity tuple) is enforced in window.go, the single window
			// contract owner; these arms keep the positional dispatch rules.
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return err
			}
			sawSort, phase = true, 1
		case ir.PhysicalLimitOp:
			if sawLimit || phase > 1 {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return err
			}
			sawLimit, phase = true, 1
		case ir.PhysicalReturnOp:
			if !last || phase > 1 {
				return fmt.Errorf("%w: RETURN must be the final operation", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderReturn(plan, *operation.Return); err != nil {
				return err
			}
			phase = 2
		default:
			return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if phase != 2 {
		return fmt.Errorf("%w: plan requires a RETURN terminal", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func (r *planRenderer) renderRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil {
		// Membership roots are registered by population.go (pg-sql-18); the
		// binding gate lives there, and the candidate seed below stays refused.
		return r.renderRootSources(plan, scan)
	}
	if scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	r.rootAlias = "r0"
	r.rootVariable = scan.Variable
	r.rows[scan.Variable] = rowScope{alias: r.rootAlias, resourceType: resourceType}
	r.where = append(r.where, r.scopedResourcePredicate(r.rootAlias,
		r.textParameter(resourceType, "text:"+resourceType)))
	return nil
}

// resolveValue maps one plan value onto either a trusted column expression
// or a parameterized JSONB payload path over its row alias. Payload paths
// are bound through documentPathExpression (null_semantics.go), the
// package's single path-binding authority, so extraction and comparison
// paths can never drift apart. Derived scopes (unnest.go) resolve through
// resolveDerivedValue against their renderer-computed expression instead;
// everything else resolves through the shared rows-parameterized resolver
// in traversal.go, which main sequence and EXISTS subplans share.
func (r *planRenderer) resolveValue(value ir.PhysicalValue) (string, error) {
	// Melt output reads (unpivot.go) resolve through the registered output
	// contract, never the stored-row path; with no melt rendered the map is
	// nil and this branch is inert.
	if columns, ok := r.unpivotOutputs[value.Variable]; ok {
		return r.resolveUnpivotOutputValue(columns, value)
	}
	if scope, ok := r.rows[value.Variable]; ok && scope.derivedValue != "" {
		return r.resolveDerivedValue(value, scope)
	}
	return r.resolveValueInRows(r.rows, value)
}

// rejectOptionalRowReferences enforces the OPTIONAL-traversal boundary for
// main-sequence FILTER operations: no comparison leaf, at any depth of an
// ALL/NOT composition, may read a row materialized by an OPTIONAL
// traversal join. A WHERE conjunct over null-extended child rows re-adds
// the inner-join exclusion the AQL LET-set never applies (render/aql
// navigation_render.go:11-37), and NOT/IS-DISTINCT-FROM forms would keep
// parents an AQL required match excludes (lower/required_match.go).
// Required matching must use the typed EXISTS subplan form, which renders
// over INNER joins where WHERE placement is exact. EXISTS predicate
// subplans are self-scoped and walk-free: their own contract is enforced by
// renderRequiredMatchExists.
func (r *planRenderer) rejectOptionalRowReferences(filter ir.PhysicalFilter) error {
	if filter.Expression != nil {
		return rejectOptionalInPredicate(*filter.Expression, r.rows)
	}
	return rejectOptionalInPredicate(ir.PhysicalPredicateExpression{
		Kind:       ir.PhysicalComparisonPredicate,
		Comparison: &filter.Predicate,
	}, r.rows)
}

func rejectOptionalInPredicate(predicate ir.PhysicalPredicateExpression, rows map[string]rowScope) error {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return nil // the comparison renderer reports the missing payload.
		}
		if predicate.Comparison.LeftExpression != nil &&
			predicate.Comparison.LeftExpression.Extract != nil {
			if scope, ok := rows[predicate.Comparison.LeftExpression.Extract.Source.Variable]; ok && scope.optional {
				return optionalRowRefError(predicate.Comparison.LeftExpression.Extract.Source.Variable)
			}
		}
		if predicate.Comparison.Left.BindKey == "" && predicate.Comparison.Left.Variable != "" {
			if scope, ok := rows[predicate.Comparison.Left.Variable]; ok && scope.optional {
				return optionalRowRefError(predicate.Comparison.Left.Variable)
			}
		}
		return nil
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate:
		for _, child := range predicate.Children {
			if err := rejectOptionalInPredicate(child, rows); err != nil {
				return err
			}
		}
		return nil
	case ir.PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if err := rejectOptionalInPredicate(child, rows); err != nil {
				return err
			}
		}
		return nil
	default: // EXISTS and unknown kinds: no main-sequence row references.
		return nil
	}
}

func optionalRowRefError(variable string) error {
	return fmt.Errorf("%w: FILTER references %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL; a WHERE conjunct over null-extended child rows would re-add the inner-join exclusion the AQL LET-set never applies (render/aql/navigation_render.go renderTraversalSet). Express required matching as the typed EXISTS subplan FILTER form",
		ErrUnsupportedPostgresPlan, variable)
}

// payloadPathSegmentPattern re-checks the frozen ir path contract at the
// renderer boundary.
var payloadPathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SORT and LIMIT are rendered through window.go's renderWindowSort and
// renderWindowLimit (pg-sql-12): the single execution-window contract owner
// for both the spine (W1) and the final window after UNNEST (W2).

// renderReturn assembles the positional projection list. Every projection
// first passes classifyExtractionProjections (selectors.go) — the single
// projection-contract definition, applied with the renderer's own row
// bindings — and is then emitted from its classification: direct values and
// VALUE expressions through resolveValue, selector extractions and presence
// sidecars through the canonical mappings in null_semantics.go, CALL
// expressions through the fragment renderer in calls.go, and the pg-sql-11
// OBJECT/SLICE composites through their recursive renderers in objects.go
// (plan flows through for the slice predicate's bind-backed right side).
func (r *planRenderer) renderReturn(plan ir.PhysicalPlan, ret ir.PhysicalReturn) error {
	// A melted plan's terminal projection list reads only registered melt
	// outputs (unpivot.go owns that contract, validateUnpivotReturn pins it
	// at the surface); the spine classification below stays byte-identical
	// when no melt rendered.
	if r.unpivotOutputs != nil {
		return r.renderUnpivotReturn(ret)
	}
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: RETURN requires at least one projection", ErrUnsupportedPostgresPlan)
	}
	classified, err := classifyExtractionProjections(ret.Projections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return err
	}
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
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionPresenceSidecar:
			scope := r.rows[projection.presence.Source.Variable]
			presence, err := r.propertyPresenceExpression(scope.alias, projection.presencePath)
			if err != nil {
				return fmt.Errorf("presence projection %q: %w", projection.projection.Name, err)
			}
			expression = presence
		case extractionCallExpression:
			// The deep walk (bind values, row bindings, and the closed
			// jsonb fragment grammar) is owned end to end by calls.go
			// (pg-sql-10); this dispatch only places the fragment.
			value, err := r.renderCallProjection(plan, projection.projection)
			if err != nil {
				return err
			}
			expression = value
		case extractionObject:
			value, err := r.renderObjectValue(plan, r.rows, *projection.projection.Expression, 1)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionSlice:
			value, err := r.renderSliceValue(plan, r.rows, *projection.projection.Expression, 1)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionUnitNormalized:
			// The closed contract and the whole jsonb fragment walk are
			// owned end to end by unit_normalization.go (pg-sql-21); this
			// dispatch only places the fragment, exactly like the CALL
			// arm above.
			value, err := r.renderUnitNormalizedProjection(projection)
			if err != nil {
				return err
			}
			expression = value
		}
		r.selects = append(r.selects, expression+` AS "c`+strconv.Itoa(len(r.selects))+`"`)
		r.outputColumns = append(r.outputColumns, projection.projection.Name)
	}
	return nil
}

func (r *planRenderer) assemble() string {
	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(strings.Join(r.selects, ", "))
	builder.WriteString(" FROM " + r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias)
	for _, join := range r.joins {
		builder.WriteString(" " + join)
	}
	// Lateral expansions trail the storage joins so each references only
	// rows that already bound (unnest.go mints them in plan order).
	for _, lateral := range r.laterals {
		builder.WriteString(" " + lateral)
	}
	if len(r.where) > 0 {
		builder.WriteString(" WHERE " + strings.Join(r.where, " AND "))
	}
	// Deterministic total order: the window contract in window.go maps the
	// plan sort keys, then appends the storage identities so repeated rows
	// from parallel edge occurrences order the same way on every execution,
	// then each unnest expansion's witnesses so duplicate items keep their
	// source-array position (the ordering counterpart of
	// ir.PhysicalUnnestSortKeys' has-item/ordinality tie-break).
	builder.WriteString(" ORDER BY " + strings.Join(r.windowOrder(), ", "))
	builder.WriteString(r.window.limitClause)
	return builder.String()
}
