package postgres

// This file registers the authored finite graph path union as an
// implemented, closed PostgreSQL operation slice (pg-sql-20): one direct
// ROOT_SCAN, the canonical root scope block plus typed root FILTERs and the
// REQUIRED-match EXISTS semi-joins, one PATH_SEED, zero or more PATH_EXTEND
// operations (each one authored depth-one hop over its parent path set with
// its own canonical (edge, target) scope block and typed reached-row
// FILTERs), and one terminal GRAPH_RETURN carrying the union of named path
// sets, deterministic identity deduplication, route order, and the limit+1
// lookahead bound. It is the single authority in this package for the
// ir.PhysicalPathSeedOp / PhysicalPathExtendOp / PhysicalGraphReturnOp
// surface.
//
// The AQL counterpart of the whole shape is render/aql/graph_render.go
// renderGraphPhysicalPlan (lines 14-78), the SOLE renderer of the path
// union; its emitter is lower/graph_plan.go BuildGraphPhysicalPlan
// (PATH_SEED :67-74, PATH_EXTEND :120-129 via walk :86-135, GRAPH_RETURN
// :139), the SOLE production producer of all three operation kinds — every
// other repository reader of these kinds is a validator, counter, or
// reserved-name collector (render/aql/render.go:473-476 whole-plan dispatch,
// render/aql/validation.go:76-86 and :309, render/aql/navigation_render.go
// :594-599, ir/physical_validation.go:249-266 and :746-835,
// ir/physical_validation_expr.go:456, compiler/output.go:60,
// lower/recipe_derived_columns.go:396-401), and the optimizer carries no
// PATH_*/GRAPH_RETURN pass at all, so a graph plan reaches whichever
// renderer exactly as BuildGraphPhysicalPlan emitted it (planner-hazard
// note: the named unsupported-backend error on the compiler target in
// compiler/graph_execution.go:22-24 stays byte-stable this wave; the graph
// command dispatch flip is banked as its own follow-up per the ratified
// plan ruling, and the arch-backend-11 receipt in
// compiler/selected_source_contract_test.go stays untouched).
//
// The PostgreSQL spelling chosen so each AQL guarantee has an exact SQL
// counterpart rather than an approximation:
//
//   - One CTE per authored path set (path_0 for the seed, then one per
//     PATH_EXTEND in plan order): the counterpart of the AQL LET per set
//     (graph_render.go:87, :147). Each extend joins the PARENT set to the
//     edge occurrence and the reached resource with the exact INNER
//     edgeOccurrencePredicates/traversal contract from traversal.go — the
//     same anchor-equality spelling the spine's routeInner traversal uses,
//     correlated through the parent CTE's terminal resource_type/logical_id
//     columns. AQL needs no LATERAL either: its LET-set FOR loop over the
//     parent set is set-scoped (graph_render.go:98), and a path set is a
//     materialized array, so a plain INNER JOIN is the exact reading.
//     MatchMode is never read here because the AQL path renderer never
//     reads it either (graph_render.go:90-150): REQUIRED-ness is
//     planner-enforced by the root semi-join and the root-set exclusion
//     (graph_plan.go:79-84, :159-183), and an OPTIONAL miss simply
//     contributes zero rows to its set.
//   - Path union: UNION ALL of exactly GRAPH_RETURN.PathSets in plan order
//     (the counterpart of the FLATTEN over rows[*].sN in graph_render.go
//     :159-164). The surface re-reads that PathSets is route-order
//     ascending, which walk/graph_plan.go always emits and which is the
//     flatten order the AQL FIRST tie-break depends on.
//   - Identity deduplication: AQL COLLECT __loom_path_identity INTO group;
//     LET row = FIRST(group[*].path) (graph_render.go:166-167) becomes
//     ROW_NUMBER() OVER (PARTITION BY path_identity ORDER BY route_order
//     ASC, occurrence_trail ASC) keeping rn = 1. The identity is the
//     internal dedupe key only: AQL UNSETs it from the public row
//     (graph_render.go:170) and PostgreSQL never selects it, so its
//     spelling — a jsonb array chain [["type","id"],["type","id","label"],…]
//     built by jsonb_build_array/jsonb || at :85/:144's AQL counterpart —
//     is renderer-owned and injective (JSON encoding, no delimiter
//     ambiguity), with equivalence classes in exact bijection to Arango
//     document identity via migration 000002's UNIQUE
//     (project, dataset_generation, resource_type, logical_id), the same
//     citation window.go:42-45 uses for logical_id uniqueness. Arango's
//     TO_STRING/_id byte behavior is a QUARANTINED unverified runtime fact
//     and is never load-bearing. The FIRST-vs-ROW_NUMBER winner choice is
//     a declared deterministic refinement (plan DIV-9): AQL's group order
//     depends on unproven scan/join order (QUARANTINED), while on a true
//     tie both candidate rows carry identical public fields (same reached
//     documents, same labels), so the choice is unobservable.
//   - Route order + tie-break order: SORT row.__loom_route_order,
//     __loom_path_identity (graph_render.go:168) maps to ORDER BY
//     route_order ASC, path_identity::text COLLATE "C" ASC. The identity is
//     a renderer-owned internal column, so pinning its comparison to the
//     "C" collation (byte order, matching Arango's byte-wise comparison on
//     ASCII) is contract-declared exactly like window.go's collation note;
//     exotic-codepoint agreement with Arango is QUARANTINED, never claimed.
//   - Lookahead: LIMIT @graph_limit (graph_render.go:169) maps to one
//     bound $n LIMIT over the graph_limit bind (limit+1,
//     graph_plan.go:14,:49) — the public limit stays outside this statement
//     and the +1 witness keeps the hasMore contract exact. The LIMIT sits
//     ABOVE the dedupe (plan-proven: EXPLAIN shows the Limit node over the
//     dedupe WindowAgg), the counterpart of AQL's LIMIT after COLLECT.
//   - Public path object: RETURN UNSET(row, "terminal", "__loom_path_
//     identity", "__loom_route_order") (graph_render.go:170) yields exactly
//     {terminalAlias, nodes[], relationships[]}; nodes entries are
//     {alias, resourceType, id, key, payload} and relationship entries
//     {alias, label, fromResourceType, toResourceType} — the exact field
//     sets of graph_render.go:85, :143, and :145. id is
//     resource_type || '/' || logical_id (the Arango <collection>/<key>
//     spelling carried by storage identity), key is logical_id, payload is
//     the stored document. Every public value crosses as a $n parameter or
//     a registry column; nothing plan-authored is interpolated into SQL
//     text, and the terminal/identity/order columns stay renderer-internal.
//
// Scope (project, dataset generation, auth mode) for the root, the edge,
// and the reached target rows comes exclusively from the re-read canonical
// mapping in scope.go — every node AND edge of every route carries it
// (scopedResourcePredicate per resource row, scopedEdgePredicate per edge
// row inside edgeOccurrencePredicates), matching the canonical blocks the
// planner emits for each (graph_plan.go:57-59 root, :114-116 extend, whose
// blocks are consumed exactly and ignored via population.go
// matchCanonicalScopeBlock, D5 precedent). Plan-authored scope blocks that
// are not byte-canonical, and every typed FILTER outside the registered
// forms, are typed refusals; scope is established structurally, never
// trusted.
//
// Refusals (all ErrUnsupportedPostgresPlan, never an AQL fallback):
// ANY direction,
// correlated CODE path filters (graph_plan.go:259-278 — correlated
// predicates are a registered capability gap), non-empty SourcePath
// (graph_plan.go:124 always emits nil; AQL :101-107's nested-terminal read
// has no registered spelling), extend-scope FILTERs addressing the edge
// variable or using the bare-Predicate / non-extract forms
// (appendGraphPathFilters graph_plan.go:254-317 always emits the
// expression extract over the target), extra DERIVED_LET/EXPRESSION_LET
// operations in an extend scope, any operation kind outside the closed
// slice (TRAVERSAL, UNNEST, SET, UNPIVOT, GROUP_ROWS, pivots, stage,
// collection scans — capability gaps, matching the spine's own refusals),
// a second PATH_SEED, a PATH_EXTEND before the seed or on an undefined
// source set, root-region operations that are not FILTER/DERIVED_LET, a
// stray FILTER/DERIVED_LET after the seed (the graph plan is flat), a
// non-ascending PathSets order, multiple or non-terminal GRAPH_RETURNs,
// an endpoint-lookup payload truncation (see graph20NativeStrategyTraversal
// for why the strategy itself renders), and every scope defect (typed
// ErrInvalidPostgresPlanScope via loadScope).
// Arbitrary unbounded recursive graph search remains unsupported: each
// PATH_EXTEND is one authored depth-one hop (≤32 declarations,
// graph_plan.go:13/:31-33 planner-pinned), no WITH RECURSIVE is ever
// rendered (EXPLAIN-proven in graph_test.go), and there is no fallback to
// AQL or AQL-to-SQL conversion anywhere on this path.
//
// validateGraphPlanSurface is the single contract definition, applied as
// the first plan-facing act at both doors: RenderGraphPhysicalPlan applies
// it before plan validation and scope loading, and RenderPhysicalPlan's
// whole-plan delegation (the one additive dispatch hunk, the structural
// twin of render/aql/render.go:473-476's pre-loop GRAPH_RETURN dispatch)
// routes every plan mentioning PATH_SEED/PATH_EXTEND/GRAPH_RETURN through
// the same function — one contract, not two (the unnest.go:93-102 /
// unpivot.go:104-111 precedent). Plan-authored variable names never reach
// SQL text: CTE, edge, and target aliases come from the renderer's own
// counter (traversal.go's nextTraversalSequence), storage identifiers from
// the registry allowlist, and every plan value through addParameter.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// graphOutputColumn is the single positional output column of a graph
// statement, matching the CompiledQuery column name the graph frontend
// publishes (compiler/graph_execution.go:53 Columns/PublicColumns
// {"paths"}); the public rows are the UNSET-ed path objects.
const graphOutputColumn = "paths"

// RenderGraphPhysicalPlan renders the authored finite graph path union onto
// the trusted PostgreSQL storage binding. Determinism, sealing, and the
// parameter discipline are the package contract (RenderPhysicalPlan's doc):
// the same plan and binding yield byte-identical SQL and value-identical
// ordered arguments, and nothing plan-authored reaches SQL text.
func RenderGraphPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	return renderGraphPhysicalPlan(plan, storage)
}

// graphPlanMentionsPathOperations reports whether a plan mentions the graph
// path surface at all. RenderPhysicalPlan consults it once, before plan
// validation, so a graph plan never enters a spine arm (the whole-plan
// delegation door; render/aql/render.go:473-476 parity).
func graphPlanMentionsPathOperations(plan ir.PhysicalPlan) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPathSeedOp || operation.Kind == ir.PhysicalPathExtendOp || operation.Kind == ir.PhysicalGraphReturnOp {
			return true
		}
	}
	return false
}

// renderGraphPhysicalPlan is the single graph door behind both entry
// points. Gate order mirrors RenderPhysicalPlan for the gates that apply
// plan-wide (engine, ClickHouse payloads, stage sequences, preview
// windowing, deferred lets), with validateGraphPlanSurface as the first
// plan-facing act so both doors refuse the same shapes with the same
// typed error and message.
func renderGraphPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGraphPlanSurface(plan); err != nil {
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
	sql, outputColumns, err := renderer.renderGraphStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, outputColumns)
}

// validateGraphPlanSurface pins the closed graph slice:
// [ROOT_SCAN, (FILTER | DERIVED_LET)*, PATH_SEED, PATH_EXTEND*, GRAPH_RETURN]
// plus the cross-field pins the shared IR validator does not carry. Every
// refusal is ErrUnsupportedPostgresPlan; there is no partial accept.
func validateGraphPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) < 3 {
		return fmt.Errorf("%w: graph plans require at least ROOT_SCAN, PATH_SEED, and a final GRAPH_RETURN", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: graph plans require one leading ROOT_SCAN payload (lower/graph_plan.go:51-55)", ErrUnsupportedPostgresPlan)
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != ir.PhysicalGraphReturnOp || last.GraphReturn == nil {
		return fmt.Errorf("%w: graph plans require GRAPH_RETURN as the final operation (lower/graph_plan.go:139)", ErrUnsupportedPostgresPlan)
	}
	seedIndex := -1
	definedSets := map[string]bool{}
	var lastRouteOrder int
	var haveRouteOrder bool
	for index := 1; index < len(plan.Operations)-1; index++ {
		operation := plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a FILTER after PATH_SEED must live inside a PATH_EXTEND Scope (the graph plan is flat; lower/graph_plan.go:113-119)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.Filter == nil {
				return fmt.Errorf("%w: graph plan operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalDerivedLetOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a DERIVED_LET after PATH_SEED must live inside a PATH_EXTEND Scope (the graph plan is flat)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.DerivedLet == nil {
				return fmt.Errorf("%w: graph plan operation %d: derived let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalPathSeedOp:
			if seedIndex != -1 {
				return fmt.Errorf("%w: graph plan operation %d: a second PATH_SEED is outside the closed graph slice", ErrUnsupportedPostgresPlan, index)
			}
			seed := operation.PathSeed
			if seed == nil {
				return fmt.Errorf("%w: graph plan operation %d: path seed payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if seed.Variable == "" {
				return fmt.Errorf("%w: graph plan operation %d: path seed must name its set variable", ErrUnsupportedPostgresPlan, index)
			}
			if value := seed.Node.Value; value.Variable != first.RootScan.Variable || len(value.Path) != 0 || value.BindKey != "" {
				return fmt.Errorf("%w: graph plan operation %d: the seed node must read the root row itself (lower/graph_plan.go:70-72; the AQL seed renders the one root value, render/aql/graph_render.go:80-88)",
					ErrUnsupportedPostgresPlan, index)
			}
			seedIndex = index
			definedSets[seed.Variable] = true
		case ir.PhysicalPathExtendOp:
			if seedIndex == -1 {
				return fmt.Errorf("%w: graph plan operation %d: PATH_EXTEND before PATH_SEED is outside the closed graph slice", ErrUnsupportedPostgresPlan, index)
			}
			extend := operation.PathExtend
			if extend == nil {
				return fmt.Errorf("%w: graph plan operation %d: path extend payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if extend.Variable == "" {
				return fmt.Errorf("%w: graph plan operation %d: path extend must name its set variable", ErrUnsupportedPostgresPlan, index)
			}
			if !definedSets[extend.SourceVariable] {
				return fmt.Errorf("%w: graph plan operation %d: source path set %q is not a set defined by this plan",
					ErrUnsupportedPostgresPlan, index, extend.SourceVariable)
			}
			if extend.Traversal.Direction != ir.PhysicalInbound &&
				extend.Traversal.Direction != ir.PhysicalOutbound {
				return fmt.Errorf("%w: graph plan operation %d: %s-direction traversal has no proven PostgreSQL contract (the spine registers INBOUND/OUTBOUND only, lower/traversal.go; ANY is a registered capability gap)",
					ErrUnsupportedPostgresPlan, index, string(extend.Traversal.Direction))
			}
			if len(extend.SourcePath) != 0 {
				return fmt.Errorf("%w: graph plan operation %d: a non-empty SourcePath has no registered PostgreSQL spelling (the planner always emits nil, lower/graph_plan.go:124; the AQL nested-terminal read render/aql/graph_render.go:101-107 stays a typed gap)",
					ErrUnsupportedPostgresPlan, index)
			}
			// The endpoint-lookup strategy is normalized to the registered
			// native join (see graph20NativeStrategyTraversal); the payload
			// the AQL endpoint arm would read must be complete first, so a
			// hand-assembled truncation fails closed instead of silently
			// rendering the native join.
			if extend.Traversal.Strategy == ir.PhysicalTraversalEndpointLookup &&
				(strings.TrimSpace(extend.Traversal.EndpointField) == "" || strings.TrimSpace(extend.Traversal.EndpointJoinField) == "") {
				return fmt.Errorf("%w: graph plan operation %d: endpoint-lookup traversal payload is incomplete (the planner always names EndpointField and EndpointJoinField, lower/traversal.go:69-70)",
					ErrUnsupportedPostgresPlan, index)
			}
			if value := extend.Node.Value; value.Variable != extend.Traversal.TargetVariable || len(value.Path) != 0 || value.BindKey != "" {
				return fmt.Errorf("%w: graph plan operation %d: the extend node must read the reached target row itself (lower/graph_plan.go:125)",
					ErrUnsupportedPostgresPlan, index)
			}
			// The canonical (edge, target) scope block is consumed exactly
			// and ignored (its structural predicates come from scope.go);
			// everything after it must be the registered FILTER form. The
			// block is re-consumed at render as the local invariant.
			blockEnd := 0
			if err := matchCanonicalScopeBlock(extend.Scope, &blockEnd, []string{extend.Traversal.EdgeVariable, extend.Traversal.TargetVariable}); err != nil {
				return fmt.Errorf("%w: graph plan operation %d extend scope %d: canonical hop scope block: %s",
					ErrUnsupportedPostgresPlan, index, blockEnd, err.Error())
			}
			for scopeIndex := blockEnd; scopeIndex < len(extend.Scope); scopeIndex++ {
				scopeOperation := extend.Scope[scopeIndex]
				switch scopeOperation.Kind {
				case ir.PhysicalFilterOp:
					if scopeOperation.Filter == nil {
						return fmt.Errorf("%w: graph plan operation %d extend scope %d: filter payload is missing", ErrUnsupportedPostgresPlan, index, scopeIndex)
					}
					if err := validateGraphExtendFilter(index, scopeIndex, extend, scopeOperation.Filter); err != nil {
						return err
					}
				case ir.PhysicalDerivedLetOp:
					// Any LET outside the canonical block (and EXPRESSION_LET,
					// refused by default) is a gap.
					if scopeOperation.DerivedLet == nil || scopeOperation.DerivedLet.Operator != canonicalAuthLetOperator {
						return fmt.Errorf("%w: graph plan operation %d extend scope %d: only the canonical AUTH_RESOURCE_PATH_ALLOWED LET is registered",
							ErrUnsupportedPostgresPlan, index, scopeIndex)
					}
				default:
					return fmt.Errorf("%w: graph plan operation %d extend scope %d: operation %q is outside the closed graph slice",
						ErrUnsupportedPostgresPlan, index, scopeIndex, string(scopeOperation.Kind))
				}
			}
			definedSets[extend.Variable] = true
			if haveRouteOrder && extend.RouteOrder <= lastRouteOrder {
				return fmt.Errorf("%w: graph plan operation %d: route order %d does not ascend after %d (walk/graph_plan.go:85-90 always emits DFS-ascending orders; the PathSets flatten order depends on it)",
					ErrUnsupportedPostgresPlan, index, extend.RouteOrder, lastRouteOrder)
			}
			lastRouteOrder, haveRouteOrder = extend.RouteOrder, true
		default:
			return fmt.Errorf("%w: graph plan operation %d (%q) is not part of the closed graph slice",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
	}
	if seedIndex == -1 {
		return fmt.Errorf("%w: graph plans require exactly one PATH_SEED (lower/graph_plan.go:67-74)", ErrUnsupportedPostgresPlan)
	}
	// PathSets must list defined sets in route-order-ascending order: the
	// planner appends sets in walk order (graph_plan.go:81-112), and the
	// AQL FIRST tie-break reads them in that flatten order
	// (render/aql/graph_render.go:161-167).
	seenSets := map[string]bool{}
	previousRouteOrder := 0
	for setIndex, setName := range last.GraphReturn.PathSets {
		if !definedSets[setName] {
			return fmt.Errorf("%w: graph return path set %q is not a set defined by this plan", ErrUnsupportedPostgresPlan, setName)
		}
		if seenSets[setName] {
			return fmt.Errorf("%w: graph return path set %q is listed twice", ErrUnsupportedPostgresPlan, setName)
		}
		seenSets[setName] = true
		routeOrder := 0
		if setName != plan.Operations[seedIndex].PathSeed.Variable {
			routeOrder = -1
			for index := seedIndex + 1; index < len(plan.Operations)-1; index++ {
				if extend := plan.Operations[index].PathExtend; extend != nil && extend.Variable == setName {
					routeOrder = extend.RouteOrder
					break
				}
			}
			if routeOrder < 0 {
				return fmt.Errorf("%w: graph return path set %q is not route-ordered", ErrUnsupportedPostgresPlan, setName)
			}
		}
		if setIndex > 0 && routeOrder <= previousRouteOrder {
			return fmt.Errorf("%w: graph return path sets are not route-order ascending at %q (%d after %d); the planner's flatten order is part of the dedupe contract",
				ErrUnsupportedPostgresPlan, setName, routeOrder, previousRouteOrder)
		}
		previousRouteOrder = routeOrder
	}
	return nil
}

// validateGraphExtendFilter pins the one registered extend-scope FILTER
// form: the typed comparison expression the planner emits over the reached
// target's payload (lower/graph_plan.go:280-315), never a correlated CODE
// filter (:259-278, a registered capability gap), never a bare-Predicate
// form or an edge-variable address (the edge row is never readable, the
// traversal.go precedent).
func validateGraphExtendFilter(index, scopeIndex int, extend *ir.PhysicalPathExtend, filter *ir.PhysicalFilter) error {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: graph plan operation %d extend scope %d: %s", ErrUnsupportedPostgresPlan, index, scopeIndex, reason)
	}
	if filter.Expression == nil || filter.Expression.Kind != ir.PhysicalComparisonPredicate || filter.Expression.Comparison == nil {
		return refuse("extend-scope FILTERs must be the planner's comparison-expression form (lower/graph_plan.go:277/:313)")
	}
	comparison := filter.Expression.Comparison
	if comparison.Correlation != nil {
		return refuse("correlated CODE path filters are a registered capability gap (lower/graph_plan.go:259-278; correlated predicates have no registered PostgreSQL contract)")
	}
	if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
		return refuse("the compared value must be the planner's selector extraction (lower/graph_plan.go:284-286)")
	}
	extract := comparison.LeftExpression.Extract
	if extract.Source.Variable != extend.Traversal.TargetVariable || len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return refuse("extend-scope FILTERs may only read the reached target's payload (lower/graph_plan.go:285-286; the edge row is never readable)")
	}
	return nil
}

// graphPathSet records the renderer-owned CTE alias and terminal identity
// columns of one materialized path set. Plan-authored set names are map
// keys only and never reach SQL text.
type graphPathSet struct {
	cteAlias     string
	terminalType string
}

// renderGraphStatement assembles the whole WITH statement. The renderer's
// rows map carries the root binding only (seed-side rendering); each
// extend renders its edge/target rows in a scope-local rows map so plan
// FILTERs can never address a row outside their own hop.
func (r *planRenderer) renderGraphStatement(plan ir.PhysicalPlan) (string, []string, error) {
	rootScan := plan.Operations[0].RootScan
	if err := r.renderRootScan(plan, *rootScan); err != nil {
		return "", nil, err
	}
	seedConjuncts := r.where
	r.where = nil

	// Root region: canonical scope operations are accepted and dropped
	// (scope.go's re-read predicates are the single scope authority), typed
	// root FILTERs — including the REQUIRED-match EXISTS semi-joins — render
	// through the shared spine contract (renderFilter routes the canonical
	// constant-1 EXISTS to traversal.go renderRequiredMatchExists).
	seedIndex := 1
	for index := 1; index < len(plan.Operations)-1; index++ {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalPathSeedOp {
			seedIndex = index
			break
		}
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			renderPlan := graph20NormalizeRequiredMatchSubplan(plan, index)
			clause, err := r.renderFilter(renderPlan, *renderPlan.Operations[index].Filter)
			if err != nil {
				return "", nil, fmt.Errorf("graph root filter %d: %w", index, err)
			}
			seedConjuncts = append(seedConjuncts, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", nil, err
			}
			if canonical {
				continue
			}
			return "", nil, fmt.Errorf("%w: graph root operation %d is not a canonical scope operation", ErrUnsupportedPostgresPlan, index)
		}
	}

	resourceTable := r.storage.Resource.Identifier().SQLText()
	edgeTable := r.storage.Edge.Identifier().SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()
	edgeSeqColumn := r.storage.Edge.MustColumn("edge_seq").SQLText()

	rootRow := r.rows[rootScan.Variable]
	seed := plan.Operations[seedIndex].PathSeed

	// path_0 — PATH_SEED: the AQL LET [path] over the root row
	// (render/aql/graph_render.go:80-88). The terminal columns are the
	// correlation face for child extends.
	seedNode := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(seed.Node.Alias, "text:"+seed.Node.Alias)) + "::text" +
		", 'resourceType', $" + strconv.Itoa(r.textParameter(seed.Node.ResourceType, "text:"+seed.Node.ResourceType)) + "::text" +
		", 'id', " + rootRow.alias + "." + resourceTypeColumn + " || '/' || " + rootRow.alias + "." + logicalIDColumn +
		", 'key', " + rootRow.alias + "." + logicalIDColumn +
		", 'payload', " + rootRow.alias + "." + payloadColumn + ")"
	seedSelect := "SELECT jsonb_build_array(" + rootRow.alias + "." + resourceTypeColumn + ", " + rootRow.alias + "." + logicalIDColumn + ") AS \"path_identity\"" +
		", $" + strconv.Itoa(r.graphBigintParameter(seed.RouteOrder)) + "::bigint AS \"route_order\"" +
		", $" + strconv.Itoa(r.textParameter(seed.Node.Alias, "text:"+seed.Node.Alias)) + "::text AS \"terminal_alias\"" +
		", jsonb_build_array(" + seedNode + ") AS \"nodes\"" +
		", '[]'::jsonb AS \"relationships\", '[]'::jsonb AS \"occurrence_trail\"" +
		", " + rootRow.alias + "." + resourceTypeColumn + " AS \"resource_type\"" +
		", " + rootRow.alias + "." + logicalIDColumn + " AS \"logical_id\"" +
		" FROM " + resourceTable + " AS " + rootRow.alias
	if len(seedConjuncts) > 0 {
		seedSelect += " WHERE " + strings.Join(seedConjuncts, " AND ")
	}

	sets := map[string]graphPathSet{seed.Variable: {cteAlias: "path_0", terminalType: seed.Node.ResourceType}}
	ctes := []string{"path_0 AS (" + seedSelect + ")"}
	terminalTypeMismatch := func(index int, setName, want, got string) error {
		return fmt.Errorf("%w: graph plan operation %d: set %q terminal type %q does not continue the authored chain from %q",
			ErrUnsupportedPostgresPlan, index, setName, got, want)
	}

	for index := seedIndex + 1; index < len(plan.Operations)-1; index++ {
		extend := plan.Operations[index].PathExtend
		parent, ok := sets[extend.SourceVariable]
		if !ok {
			return "", nil, fmt.Errorf("%w: graph plan operation %d: source path set %q is not a set defined by this plan",
				ErrUnsupportedPostgresPlan, index, extend.SourceVariable)
		}
		if parent.terminalType != extend.Relationship.FromResourceType {
			return "", nil, terminalTypeMismatch(index, extend.SourceVariable, parent.terminalType, extend.Relationship.FromResourceType)
		}
		// The parent set CTE is aliased "p" in this CTE's FROM; every
		// correlation to it (the anchored-id equality inside the edge
		// predicate chain and the contract check that feeds it) must use
		// the declared alias, never the bare CTE name.
		const parentAlias = "p"
		verified, err := r.validateTraversalContract(plan, graph20NativeStrategyTraversal(extend.Traversal), rowScope{alias: parentAlias, resourceType: extend.Relationship.FromResourceType})
		if err != nil {
			return "", nil, fmt.Errorf("graph plan operation %d traversal: %w", index, err)
		}
		if extend.Node.ResourceType != verified.targetType {
			return "", nil, fmt.Errorf("%w: graph plan operation %d: node resource type %q does not match the traversal target type bind %q (lower/graph_plan.go:125-126 always agrees; a divergence must fail closed)",
				ErrUnsupportedPostgresPlan, index, extend.Node.ResourceType, verified.targetType)
		}
		seq := r.nextTraversalSequence()
		edgeAlias := "e" + strconv.Itoa(seq)
		targetAlias := "x" + strconv.Itoa(seq)
		edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, parentAlias)

		localRows := map[string]rowScope{extend.Traversal.TargetVariable: {alias: targetAlias, resourceType: verified.targetType}}
		var extendConjuncts []string
		scopeIndex := 0
		if err := matchCanonicalScopeBlock(extend.Scope, &scopeIndex, []string{extend.Traversal.EdgeVariable, extend.Traversal.TargetVariable}); err != nil {
			return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: canonical hop scope block: %s",
				ErrUnsupportedPostgresPlan, index, scopeIndex, err.Error())
		}
		for ; scopeIndex < len(extend.Scope); scopeIndex++ {
			operation := extend.Scope[scopeIndex]
			if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil {
				return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: only FILTER operations follow the canonical scope block",
					ErrUnsupportedPostgresPlan, index, scopeIndex)
			}
			fragment, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, localRows, true)
			if err != nil {
				return "", nil, fmt.Errorf("%w: graph plan operation %d extend scope %d: %w", ErrUnsupportedPostgresPlan, index, scopeIndex, err)
			}
			extendConjuncts = append(extendConjuncts, fragment.sql)
		}

		labelRef := "$" + strconv.Itoa(verified.labelIndex)
		nodeObject := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(extend.Node.Alias, "text:"+extend.Node.Alias)) + "::text" +
			", 'resourceType', $" + strconv.Itoa(r.textParameter(extend.Node.ResourceType, "text:"+extend.Node.ResourceType)) + "::text" +
			", 'id', " + targetAlias + "." + resourceTypeColumn + " || '/' || " + targetAlias + "." + logicalIDColumn +
			", 'key', " + targetAlias + "." + logicalIDColumn +
			", 'payload', " + targetAlias + "." + payloadColumn + ")"
		relationshipObject := "jsonb_build_object('alias', $" + strconv.Itoa(r.textParameter(extend.Relationship.Alias, "text:"+extend.Relationship.Alias)) + "::text" +
			", 'label', " + labelRef + "::text" +
			", 'fromResourceType', $" + strconv.Itoa(r.textParameter(extend.Relationship.FromResourceType, "text:"+extend.Relationship.FromResourceType)) + "::text" +
			", 'toResourceType', $" + strconv.Itoa(r.textParameter(extend.Relationship.ToResourceType, "text:"+extend.Relationship.ToResourceType)) + "::text" + ")"

		cteAlias := "path_" + strconv.Itoa(len(ctes))
		selectText := "SELECT p.\"path_identity\" || jsonb_build_array(" + targetAlias + "." + resourceTypeColumn + ", " + targetAlias + "." + logicalIDColumn + ", " + labelRef + "::text) AS \"path_identity\"" +
			", $" + strconv.Itoa(r.graphBigintParameter(extend.RouteOrder)) + "::bigint AS \"route_order\"" +
			", $" + strconv.Itoa(r.textParameter(extend.Node.Alias, "text:"+extend.Node.Alias)) + "::text AS \"terminal_alias\"" +
			", p.\"nodes\" || jsonb_build_array(" + nodeObject + ") AS \"nodes\"" +
			", p.\"relationships\" || jsonb_build_array(" + relationshipObject + ") AS \"relationships\"" +
			", p.\"occurrence_trail\" || jsonb_build_array(" + edgeAlias + "." + edgeSeqColumn + ") AS \"occurrence_trail\"" +
			", " + targetAlias + "." + resourceTypeColumn + " AS \"resource_type\"" +
			", " + targetAlias + "." + logicalIDColumn + " AS \"logical_id\"" +
			" FROM " + parent.cteAlias + " AS p" +
			" INNER JOIN " + edgeTable + " AS " + edgeAlias + " ON " + edgeWhere +
			" INNER JOIN " + resourceTable + " AS " + targetAlias + " ON " + targetOn
		if len(extendConjuncts) > 0 {
			selectText += " WHERE " + strings.Join(extendConjuncts, " AND ")
		}
		ctes = append(ctes, cteAlias+" AS ("+selectText+")")
		sets[extend.Variable] = graphPathSet{cteAlias: cteAlias, terminalType: extend.Node.ResourceType}
	}

	graphReturn := plan.Operations[len(plan.Operations)-1].GraphReturn
	arms := make([]string, 0, len(graphReturn.PathSets))
	for _, setName := range graphReturn.PathSets {
		pathSet, ok := sets[setName]
		if !ok {
			return "", nil, fmt.Errorf("%w: graph return path set %q is not a set defined by this plan", ErrUnsupportedPostgresPlan, setName)
		}
		arms = append(arms, "SELECT \"path_identity\", \"route_order\", \"terminal_alias\", \"nodes\", \"relationships\", \"occurrence_trail\" FROM "+pathSet.cteAlias)
	}

	limit, ok := plan.BindVars[graphReturn.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return "", nil, fmt.Errorf("%w: graph limit bind %q must be a positive int (lower/graph_plan.go:21-23/:49)",
			ErrUnsupportedPostgresPlan, graphReturn.LimitBindKey)
	}
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")

	sql := "WITH " + strings.Join(ctes, ", ") +
		", graph_paths AS (SELECT *, ROW_NUMBER() OVER (PARTITION BY path_identity ORDER BY route_order ASC, occurrence_trail ASC) AS rn" +
		" FROM (" + strings.Join(arms, " UNION ALL ") + ") AS g_union)" +
		" SELECT jsonb_build_object('terminalAlias', terminal_alias, 'nodes', nodes, 'relationships', relationships) AS \"c0\"" +
		" FROM graph_paths AS g_row WHERE g_row.rn = 1" +
		" ORDER BY g_row.route_order ASC, g_row.path_identity::text COLLATE \"C\" ASC" +
		" LIMIT $" + strconv.Itoa(limitIndex)
	return sql, []string{graphOutputColumn}, nil
}

// graphBigintParameter binds one renderer-visible integer plan value as an
// ordered bigint argument — route orders and the lookahead bound cross the
// $n boundary exactly like the spine's LIMIT (window.go's
// renderWindowLimit spelling); no plan integer is ever interpolated.
// graph20NormalizeRequiredMatchSubplan returns plan with one consumption
// applied to the REQUIRED-match EXISTS subplan at the given root operation
// index. lower/required_match.go:121-126 embeds the canonical
// project/generation/auth block (appendProjectScope/
// appendDatasetGenerationScope/appendAuthScope, lower/plan_orchestration.go
// :239-277) after each EXISTS step, and traversal.go
// renderRequiredMatchExists refuses plan-authored scope operations because
// scope is established structurally from the re-read storage binds:
// edgeOccurrencePredicates re-emits the identical canonical chain
// (scopedEdgePredicate + scopedResourcePredicate, traversal.go:206-241) for
// every edge and reached row. So a block accepted by the shared
// matchCanonicalScopeBlock authority (population.go:691 — exact operator,
// both sides, the plan-canonical @project/@dataset_generation/
// @auth_resource_paths/@auth_resource_paths_unrestricted/@scope_allowed
// binds, scope.go:79-83) carries no constraint the structural chain does
// not already apply, and consuming it loses nothing — the same D5
// precedent under which the root region drops its own block. Anything the
// authority does not accept is passed through untouched, and traversal.go's
// typed refusals speak unchanged; subplans without blocks (pinned offline
// shapes) and non-canonical EXISTS are returned as-is.
func graph20NormalizeRequiredMatchSubplan(plan ir.PhysicalPlan, index int) ir.PhysicalPlan {
	operation := plan.Operations[index]
	if operation.Filter == nil || operation.Filter.Expression == nil ||
		operation.Filter.Expression.Kind != ir.PhysicalExistsPredicate || operation.Filter.Expression.Exists == nil ||
		validateRequiredMatchReturn(plan, *operation.Filter.Expression.Exists) != nil {
		return plan
	}
	operations := operation.Filter.Expression.Exists.Operations
	kept := make([]ir.PhysicalOperation, 0, len(operations))
	consumed := false
	var blockVariables []string
	for step := 0; step < len(operations); step++ {
		candidate := operations[step]
		if candidate.Kind == ir.PhysicalTraversalOp && candidate.Traversal != nil {
			blockVariables = []string{candidate.Traversal.EdgeVariable, candidate.Traversal.TargetVariable}
			kept = append(kept, candidate)
			continue
		}
		if blockVariables != nil {
			cursor := step
			if err := matchCanonicalScopeBlock(operations, &cursor, blockVariables); err == nil && cursor > step {
				step = cursor - 1
				blockVariables = nil
				consumed = true
				continue
			}
		}
		kept = append(kept, candidate)
	}
	if !consumed {
		return plan
	}
	normalized := plan
	normalizedOperations := append([]ir.PhysicalOperation(nil), plan.Operations...)
	operationCopy := normalizedOperations[index]
	filterCopy := *operationCopy.Filter
	expressionCopy := *filterCopy.Expression
	existsCopy := *expressionCopy.Exists
	existsCopy.Operations = kept
	expressionCopy.Exists = &existsCopy
	filterCopy.Expression = &expressionCopy
	operationCopy.Filter = &filterCopy
	normalizedOperations[index] = operationCopy
	normalized.Operations = normalizedOperations
	return normalized
}

func (r *planRenderer) graphBigintParameter(value int) int {
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(value)}, "")
}

// graph20NativeStrategyTraversal clears the endpoint-lookup label for the
// graph join renderer after the surface has pinned the endpoint payload.
// Both AQL graph arms select the same edge set on the flat
// fhir_edge_occurrence relation — anchored identity, label, reached type,
// and dangling-edge exclusion (render/aql/graph_render.go:115-131) — so the
// strategy is a physical-planner hint with no PostgreSQL storage
// counterpart; the single typed-identity join authority
// (traversal.go validateTraversalContract + edgeOccurrencePredicates)
// renders either spelling. Unknown strategies pass through untouched and
// meet traversal.go's refusal unchanged; the spine's endpoint-lookup
// refusal (traversal.go:132-134) is not widened or weakened here.
func graph20NativeStrategyTraversal(traversal ir.PhysicalTraversal) ir.PhysicalTraversal {
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		traversal.Strategy = ir.PhysicalTraversalNative
	}
	return traversal
}
