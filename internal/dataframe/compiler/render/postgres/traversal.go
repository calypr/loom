package postgres

// This file registers the depth-one relationship traversal as an
// implemented, closed PostgreSQL operation (pg-sql-06). It is the single
// authority in this package for lowering one typed traversal onto the edge
// relation, in both of the two shapes the backend-neutral plan carries:
//
//   - OPTIONAL set traversal (the TRAVERSAL operation): the AQL counterpart
//     is the post-window LET-set materialization of render/aql
//     navigation_render.go renderTraversalSet (lines 11-37) — a FOR loop
//     inside a LET, so a parent whose set is empty is still emitted. The
//     PostgreSQL rendering is LEFT JOIN LATERAL (edge INNER JOIN target) ON
//     TRUE: the parent survives with SQL NULL child columns when no
//     occurrence survives, and every surviving edge occurrence contributes
//     exactly one row (no multiplicity collapse).
//   - Unnest owner-route step (the traversal carried by
//     ir.PhysicalUnnestOwner.Route): the AQL counterpart is the INNER
//     renderTraversalScan FOR loop that renderUnnest renders inside the
//     current loop (render/aql/navigation_render.go) — NOT a LET set, so a
//     parent with zero surviving route occurrences emits zero rows under
//     both empty policies. The PostgreSQL rendering is a top-level INNER
//     JOIN edge × target (renderTraversal routeInner=true).
//   - REQUIRED match filtering (the FILTER operation whose expression is an
//     EXISTS predicate over a bounded correlated subplan starting with
//     traversal step(s) — the shape lower/required_match.go lowers for
//     REQUIRED semantic routes, pinned on the AQL side by
//     compiler/physical_required_match_test.go): rendered as
//     WHERE EXISTS (SELECT 1 FROM edge JOIN resource … correlated …), the
//     pre-window root semi-join the AQL renderer spells as
//     FILTER LENGTH((FOR … LIMIT 1)). A required match excludes the
//     no-match parent; EXISTS makes the LIMIT 1 witness limit unnecessary.
//
// The AQL renderer distinguishes set traversal from required semi-join
// filters exactly this way (lower/required_match.go:12-15: the EXISTS
// subplan "is a root semi-join and must run before the root sort/window"),
// and match mode is therefore derived from plan structure, never from a
// plan-supplied label: a TRAVERSAL operation alone is OPTIONAL, a paired
// EXISTS FILTER carries the REQUIRED match filter.
//
// Exactness decisions:
//
//   - The edge and the reached target join INNERSIDE the lateral subquery.
//     That is the analogue of AQL graph traversal never yielding a vertex
//     for a dangling edge and of the endpoint-lookup form's
//     "FILTER target != null" (render/aql/navigation_render.go:51). A flat
//     LEFT JOIN edge + LEFT JOIN target pair would delete a parent whose
//     only edges dangle (both joined rows fail and no parent-only row is
//     produced); the LATERAL shape produces exactly one NULL-extended
//     parent row whenever zero occurrences survive, and zero NULL rows
//     otherwise. This is why LATERAL is used instead of chained LEFT JOINs.
//   - Every traversal is validated against the direction/discriminator
//     contract the AQL navigation renderer enforces
//     (render/aql/navigation_layout.go:393-438 validateGenericNavigation
//     traversal): OUTBOUND must carry EdgeTargetTypeField "to_type",
//     INBOUND "from_type", the edge collection bind must be "fhir_edge",
//     and the edge variable must be named. ANY direction has no proven
//     PostgreSQL contract and is refused, exactly as the AQL generic
//     navigation surface refuses it (navigation_layout.go:394-396).
//   - Scope (project, dataset generation, auth mode) for the edge row and
//     the reached target row comes exclusively from the re-read canonical
//     mapping in scope.go (loadScope plus scopedEdgePredicate and
//     scopedResourcePredicate). Plan-authored scope operations inside an
//     EXISTS subplan — including the canonical project/generation/auth
//     block the AQL pipeline emits — are refused, never trusted: scope is
//     established structurally here, per the pg-sql-02 contract.
//   - FILTER and SORT in the main sequence may not reference a row
//     materialized by an OPTIONAL traversal join: a WHERE conjunct over
//     null-extended child rows would re-add the inner-join exclusion the
//     AQL LET-set never applies, and NOT/IS-DISTINCT-FROM forms would keep
//     parents an AQL required match excludes. Required matching must use
//     the typed EXISTS subplan form.
//
// All plan data — label, endpoint resource types, filter values, document
// paths — is bound as ordered typed $n parameters; only registry-validated
// identifiers (storage binding plus the frozen column list below) reach SQL
// text. There is no fallback to the AQL renderer and no AQL-to-SQL
// conversion: every shape outside this slice is a typed
// ErrUnsupportedPostgresPlan refusal that yields no SQL.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// traversalResourceColumns is the frozen projection list a lateral child
// subquery exposes for the reached target row, in this fixed order. The set
// is the resource-table allowlist (ir/postgres_storage.go, pinned by
// TestColumnAllowlistsMatchMigrationsLiterally); every name is re-resolved
// through the storage binding registry, so a store-migration rename fails
// closed at MustColumn rather than silently changing statement shape.
var traversalResourceColumns = []string{
	"auth_resource_path", "dataset_generation", "logical_id", "payload",
	"project", "recorded_at", "resource_seq", "resource_type",
}

// requiredMatchResultValue is the canonical EXISTS subplan return value:
// lower/required_match.go binds one result bind to the integer 1 and
// returns it as a scalar PRESERVE_NULL value expression
// (lower/required_match.go:167-172). Anything else is a plan defect.
const requiredMatchResultValue = 1

// validatedTraversal is one fully checked depth-one traversal plus its
// bound discriminator parameters and direction-selected endpoint columns.
// Both join renderers consume the same validated fact set, so validation
// and rendering can never disagree about a traversal's identity.
type validatedTraversal struct {
	traversal     ir.PhysicalTraversal
	label         string
	labelIndex    int
	sourceType    string
	targetType    string
	anchoredType  string
	anchoredID    string
	reachedType   string
	reachedID     string
	anchoredIndex int
	reachedIndex  int
}

// validateTraversalContract re-reads every traversal discriminator from the
// plan and the anchored row's proven identity and enforces the closed
// depth-one contract. Every refusal names the boundary it enforces and
// classifies as ErrUnsupportedPostgresPlan.
func (r *planRenderer) validateTraversalContract(
	plan ir.PhysicalPlan,
	traversal ir.PhysicalTraversal,
	source rowScope,
) (validatedTraversal, error) {
	switch traversal.Direction {
	case ir.PhysicalOutbound, ir.PhysicalInbound:
	case ir.PhysicalAny:
		return validatedTraversal{}, fmt.Errorf("%w: ANY-direction traversal has no proven PostgreSQL contract (the AQL generic navigation surface refuses it too, render/aql/navigation_layout.go:394-396)", ErrUnsupportedPostgresPlan)
	default:
		return validatedTraversal{}, fmt.Errorf("%w: unknown traversal direction %q", ErrUnsupportedPostgresPlan, string(traversal.Direction))
	}
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		return validatedTraversal{}, fmt.Errorf("%w: endpoint-lookup strategy has no proven PostgreSQL contract yet; native typed-identity joins are the only registered shape", ErrUnsupportedPostgresPlan)
	}
	if traversal.Strategy != "" && traversal.Strategy != ir.PhysicalTraversalNative {
		return validatedTraversal{}, fmt.Errorf("%w: unknown traversal strategy %q", ErrUnsupportedPostgresPlan, string(traversal.Strategy))
	}
	// The same direction-aware discriminator proof lower/storage_route.go
	// requires of Arango routes and render/aql/navigation_layout.go:397-404
	// demands before rendering: without it the typed-identity join cannot be
	// attributed to a proven storage direction.
	expectedField := "to_type"
	if traversal.Direction == ir.PhysicalInbound {
		expectedField = "from_type"
	}
	if traversal.EdgeTargetTypeField != expectedField {
		return validatedTraversal{}, fmt.Errorf("%w: %s traversal must carry EdgeTargetTypeField %q, got %q",
			ErrUnsupportedPostgresPlan, string(traversal.Direction), expectedField, traversal.EdgeTargetTypeField)
	}
	// The AQL navigation layout requires a named edge variable and pins the
	// edge collection bind to fhir_edge (navigation_layout.go:404, 407-409).
	// The PostgreSQL renderer does not render the edge variable, but a
	// plan that omits it did not go through the shared traversal lowering
	// contract, and an edge bind other than fhir_edge has no storage
	// counterpart: both are capability gaps, never silent accepts.
	if traversal.EdgeVariable == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal must name its edge variable (the shared lowering contract always does; render/aql/navigation_layout.go:404)", ErrUnsupportedPostgresPlan)
	}
	if traversal.EdgeCollectionBindKey == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal must name the edge collection bind", ErrUnsupportedPostgresPlan)
	}
	if collection, ok := plan.BindVars[traversal.EdgeCollectionBindKey].(string); !ok || collection != "fhir_edge" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal edge collection bind %q must be bound to \"fhir_edge\" (the only registered edge relation; render/aql/navigation_layout.go:407-409)",
			ErrUnsupportedPostgresPlan, traversal.EdgeCollectionBindKey)
	}
	label, ok := plan.BindVars[traversal.EdgeLabelBindKey].(string)
	if !ok || strings.TrimSpace(label) == "" {
		return validatedTraversal{}, fmt.Errorf("%w: edge label bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, traversal.EdgeLabelBindKey)
	}
	targetType, ok := plan.BindVars[traversal.TargetTypeBindKey].(string)
	if !ok || strings.TrimSpace(targetType) == "" {
		return validatedTraversal{}, fmt.Errorf("%w: traversal target type bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, traversal.TargetTypeBindKey)
	}
	if traversal.SourceTypeBindKey != "" {
		if bound, ok := plan.BindVars[traversal.SourceTypeBindKey].(string); !ok || bound != source.resourceType {
			return validatedTraversal{}, fmt.Errorf("%w: traversal source type bind does not match the current row resource type %q",
				ErrUnsupportedPostgresPlan, source.resourceType)
		}
	}

	// Direction-selected endpoint columns of the edge relation: the anchored
	// endpoint carries the exact source row identity, the reached endpoint is
	// the typed identity the join carries toward the resource table.
	verified := validatedTraversal{traversal: traversal, label: label, sourceType: source.resourceType, targetType: targetType}
	if traversal.Direction == ir.PhysicalOutbound {
		verified.anchoredType, verified.anchoredID = "source_resource_type", "source_logical_id"
		verified.reachedType, verified.reachedID = "target_resource_type", "target_logical_id"
	} else {
		verified.anchoredType, verified.anchoredID = "target_resource_type", "target_logical_id"
		verified.reachedType, verified.reachedID = "source_resource_type", "source_logical_id"
	}
	verified.labelIndex = r.textParameter(label, "text:"+label)
	verified.anchoredIndex = r.textParameter(source.resourceType, "text:"+source.resourceType)
	verified.reachedIndex = r.textParameter(targetType, "text:"+targetType)
	return verified, nil
}

// edgeOccurrencePredicates renders the two predicate chains of one edge
// occurrence join over the given aliases: the edge-side chain (canonical
// edge scope from scope.go, then the traversal payload: label, anchored
// resource type, anchored row identity equality, reached resource type) and
// the target-side chain (canonical resource scope for the reached type plus
// the logical-id equality against the reached endpoint). The anchored-id
// equality correlates the occurrence to the source row alias; for an EXISTS
// subplan's first step that alias is the captured outer row.
func (r *planRenderer) edgeOccurrencePredicates(
	verified validatedTraversal,
	edgeAlias, targetAlias, sourceAlias string,
) (edgeWhere string, targetOn string) {
	edgeColumns := r.storage.Edge
	resourceColumns := r.storage.Resource

	// The edge row's canonical project/generation/auth scope comes from the
	// single mapping in scope.go; the label and endpoint identity clauses
	// appended here are traversal payload, not canonical scope.
	edgeWhere = r.scopedEdgePredicate(edgeAlias)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn("label").SQLText() +
		" = $" + strconv.Itoa(verified.labelIndex)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.anchoredType).SQLText() +
		" = $" + strconv.Itoa(verified.anchoredIndex)
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.anchoredID).SQLText() +
		" = " + sourceAlias + "." + resourceColumns.MustColumn("logical_id").SQLText()
	edgeWhere += " AND " + edgeAlias + "." + edgeColumns.MustColumn(verified.reachedType).SQLText() +
		" = $" + strconv.Itoa(verified.reachedIndex)

	targetOn = r.scopedResourcePredicate(targetAlias, verified.reachedIndex) +
		" AND " + targetAlias + "." + resourceColumns.MustColumn("logical_id").SQLText() +
		" = " + edgeAlias + "." + edgeColumns.MustColumn(verified.reachedID).SQLText()
	return edgeWhere, targetOn
}

// renderTraversal renders one registered depth-one typed traversal in one
// of two AQL-parity shapes selected by routeInner:
//
//   - Spine traversal (routeInner=false): the OPTIONAL parent-preserving
//     edge-occurrence join. The reached target row (and the renderer-owned
//     edge witness) live inside a LEFT JOIN LATERAL subquery whose
//     edge-to-target join is INNER, so each surviving occurrence yields one
//     row and zero surviving occurrences yield exactly one NULL-extended
//     parent row — the LET-set reading of render/aql navigation_render.go
//     renderTraversalSet, never an inner-join exclusion. The target variable
//     becomes a readable optional row (RETURN may project it with SQL NULL
//     columns); the edge variable binds nothing, because edge-scoped
//     predicates are canonical-structural and never plan-addressable.
//
//   - Unnest owner-route step (routeInner=true): the inner edge scan of
//     render/aql navigation_render.go renderTraversalScan, which
//     renderUnnest renders as plain FOR loops inside the current loop —
//     NOT a LET set. A parent with zero surviving route occurrences emits
//     zero rows under BOTH PhysicalUnnestExclude and
//     PhysicalUnnestPreserveParent (the PreserveParent pad row lives in
//     the expansion, which the route gates). Rendered as a top-level
//     INNER JOIN edge × target with the target row readable (non-optional)
//     and the edge witness extended into the total order.
func (r *planRenderer) renderTraversal(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal, routeInner bool) error {
	source, ok := r.rows[traversal.SourceVariable]
	if !ok || source.derivedValue != "" {
		return fmt.Errorf("%w: traversal source variable %q does not bind a readable row (edge variables, set variables, and computed unnest values are not readable)",
			ErrUnsupportedPostgresPlan, traversal.SourceVariable)
	}
	verified, err := r.validateTraversalContract(plan, traversal, source)
	if err != nil {
		return err
	}

	seq := r.nextTraversalSequence()
	edgeAlias := "e" + strconv.Itoa(seq)
	innerAlias := "x" + strconv.Itoa(seq)
	lateralAlias := "r" + strconv.Itoa(seq)
	edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, innerAlias, source.alias)

	if routeInner {
		// renderTraversalScan parity: the route is an inner edge scan at the
		// current loop level. Zero surviving occurrences eliminate the parent
		// row entirely, under either unnest empty policy.
		join := "INNER JOIN " + r.storage.Edge.Identifier().SQLText() + " AS " + edgeAlias +
			" ON " + edgeWhere +
			" INNER JOIN " + r.storage.Resource.Identifier().SQLText() + " AS " + innerAlias +
			" ON " + targetOn
		r.joins = append(r.joins, join)
		r.edgeAliases = append(r.edgeAliases, edgeAlias)
		r.rows[traversal.TargetVariable] = rowScope{alias: innerAlias, resourceType: verified.targetType}
		// The window's route occurrence witness resolves the plan-named edge
		// variable through this binding only (window.go); the top-level
		// e{n} alias is the ORDER BY-visible one in route mode.
		return r.window.rememberEdge(traversal.EdgeVariable, edgeAlias)
	}

	projection := make([]string, 0, len(traversalResourceColumns)+1)
	for _, column := range traversalResourceColumns {
		name := r.storage.Resource.MustColumn(column).SQLText()
		projection = append(projection, innerAlias+"."+name+" AS "+name)
	}
	// The storage edge identity is exposed as the deterministic ordering
	// witness. It is renderer-owned text over the registry column and is
	// never user-addressable: resourceFilterColumns excludes it, so a plan
	// path can only reach the lateral's payload column, never this alias.
	projection = append(projection, edgeAlias+"."+r.storage.Edge.MustColumn("edge_seq").SQLText()+` AS "edge_seq"`)

	join := "LEFT JOIN LATERAL (SELECT " + strings.Join(projection, ", ") +
		" FROM " + r.storage.Edge.Identifier().SQLText() + " AS " + edgeAlias +
		" INNER JOIN " + r.storage.Resource.Identifier().SQLText() + " AS " + innerAlias +
		" ON " + targetOn +
		" WHERE " + edgeWhere +
		") AS " + lateralAlias + " ON TRUE"
	r.joins = append(r.joins, join)
	r.edgeAliases = append(r.edgeAliases, lateralAlias)
	r.rows[traversal.TargetVariable] = rowScope{alias: lateralAlias, resourceType: verified.targetType, optional: true}
	// The window's route occurrence witness resolves the plan-named edge
	// variable through this binding only (window.go). In spine mode the
	// ORDER BY-visible alias is the lateral itself: it projects the
	// renderer-owned edge_seq, while e{n} lives inside the subquery.
	return r.window.rememberEdge(traversal.EdgeVariable, lateralAlias)
}

// renderRequiredMatchExists renders the REQUIRED match filter: the bounded
// correlated EXISTS subplan lower/required_match.go produces for REQUIRED
// semantic routes. The subplan contract is re-read, never trusted: exactly
// one capture (the correlation anchor), one or more chained depth-one
// TRAVERSAL steps (step k+1 anchors on step k's reached row), FILTER
// operations over subplan-local rows only, and the canonical scalar
// VALUE-expression return of the integer constant 1. Any other operation —
// plan-authored scope blocks (DERIVED_LET and friends), nested EXISTS,
// sets, unnests, sorts, limits, collection scans — is a typed refusal:
// canonical scope for the subplan's edge and target rows is established
// structurally here through the re-read scope.go mapping, exactly as the
// pg-sql-02 contract requires.
func (r *planRenderer) renderRequiredMatchExists(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicateExpression,
) (predicateFragment, error) {
	if predicate.Exists == nil {
		return predicateFragment{}, fmt.Errorf("%w: EXISTS predicate has no subplan payload", ErrUnsupportedPostgresPlan)
	}
	subplan := *predicate.Exists
	if len(subplan.Captures) != 1 {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan must capture exactly one correlation anchor, got %d captures",
			ErrUnsupportedPostgresPlan, len(subplan.Captures))
	}
	anchorVariable := subplan.Captures[0]
	anchor, ok := r.rows[anchorVariable]
	if !ok || anchor.derivedValue != "" || anchor.optional {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan capture %q does not bind a readable row (edge variables, computed unnest values, and OPTIONAL LEFT JOIN LATERAL rows cannot anchor a correlation; lower/required_match.go always captures the root row, and NULL-extended anchor columns would let subplan FILTERs compare absent rows)",
			ErrUnsupportedPostgresPlan, anchorVariable)
	}
	if err := validateRequiredMatchReturn(plan, subplan); err != nil {
		return predicateFragment{}, err
	}

	// Local scope: the captured anchor plus rows introduced by subplan
	// steps. Plan data can never reach a row outside this map, and the
	// subplan's edge variables are deliberately never bound.
	rows := map[string]rowScope{anchorVariable: anchor}
	fromParts := make([]string, 0, 4)
	whereParts := make([]string, 0, 4)
	sawTraversal := false
	nextSource := rowScope{alias: anchor.alias, resourceType: anchor.resourceType}
	nextSourceVariable := anchorVariable
	for index, operation := range subplan.Operations {
		switch operation.Kind {
		case ir.PhysicalTraversalOp:
			if operation.Traversal == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: TRAVERSAL payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			traversal := *operation.Traversal
			if traversal.SourceVariable != nextSourceVariable {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: traversal must anchor on %q (the capture for the first step, the previous step's reached row thereafter), got source %q",
					ErrUnsupportedPostgresPlan, index, nextSourceVariable, traversal.SourceVariable)
			}
			verified, err := r.validateTraversalContract(plan, traversal, nextSource)
			if err != nil {
				return predicateFragment{}, fmt.Errorf("required-match subplan operation %d: %w", index, err)
			}
			seq := r.nextTraversalSequence()
			edgeAlias := "e" + strconv.Itoa(seq)
			targetAlias := "x" + strconv.Itoa(seq)
			edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, nextSource.alias)
			if !sawTraversal {
				// The first step's edge relation is the subplan FROM; its
				// predicates carry the outer correlation and land in WHERE.
				fromParts = append(fromParts, "FROM "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias)
				whereParts = append(whereParts, edgeWhere)
			} else {
				// Chained steps anchor on the previous reached row and join
				// their edge with the full predicate chain in ON.
				fromParts = append(fromParts,
					"INNER JOIN "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias+" ON "+edgeWhere)
			}
			fromParts = append(fromParts,
				"INNER JOIN "+r.storage.Resource.Identifier().SQLText()+" AS "+targetAlias+" ON "+targetOn)
			rows[traversal.TargetVariable] = rowScope{alias: targetAlias, resourceType: verified.targetType}
			nextSource = rows[traversal.TargetVariable]
			nextSourceVariable = traversal.TargetVariable
			sawTraversal = true
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: FILTER payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			if !sawTraversal {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: a FILTER may only follow a traversal step (the match is over reached child rows, and subplan scope is structural)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.Filter.Expression == nil {
				return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d: typed match filters must use the Expression form",
					ErrUnsupportedPostgresPlan, index)
			}
			clause, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, rows, true)
			if err != nil {
				return predicateFragment{}, fmt.Errorf("required-match subplan operation %d: %w", index, err)
			}
			whereParts = append(whereParts, clause.sql)
		default:
			return predicateFragment{}, fmt.Errorf("%w: required-match subplan operation %d has kind %q; only chained TRAVERSAL steps and typed FILTER operations are registered, and plan-authored scope operations (including the canonical project/generation/auth block) are refused because scope is established structurally from the re-read binds",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
	}
	if !sawTraversal {
		return predicateFragment{}, fmt.Errorf("%w: required-match subplan must contain at least one traversal step", ErrUnsupportedPostgresPlan)
	}
	body := "EXISTS (SELECT 1 " + strings.Join(fromParts, " ") + " WHERE " + strings.Join(whereParts, " AND ") + ")"
	// EXISTS is total: it never yields SQL NULL, so the fragment is
	// two-valued and NOT over it needs no COALESCE guard.
	return predicateFragment{sql: body}, nil
}

// validateRequiredMatchReturn pins the subplan return to the exact canonical
// form lower/required_match.go emits (lines 167-172): a scalar-cardinality,
// PRESERVE_NULL VALUE expression over one bind whose plan value is the
// integer 1. EXISTS discards the value, so the constant pin keeps the
// renderer from silently accepting an array or expression return.
func validateRequiredMatchReturn(plan ir.PhysicalPlan, subplan ir.PhysicalSubplan) error {
	expression := subplan.Return
	if expression.Kind != ir.PhysicalValueExpression || expression.Value == nil ||
		expression.Value.BindKey == "" || expression.Value.Variable != "" || len(expression.Value.Path) != 0 {
		return fmt.Errorf("%w: required-match subplan return must be the canonical scalar VALUE expression over one bind (the lower/required_match.go constant-1 form)",
			ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: required-match subplan return must be SCALAR cardinality with PRESERVE_NULL null behavior (the lower/required_match.go constant-1 form)",
			ErrUnsupportedPostgresPlan)
	}
	if subplan.Sort != nil || subplan.Unique || subplan.DistinctBy != nil {
		return fmt.Errorf("%w: required-match subplan cannot carry projection modifiers", ErrUnsupportedPostgresPlan)
	}
	if value, ok := plan.BindVars[expression.Value.BindKey].(int); !ok || value != requiredMatchResultValue {
		return fmt.Errorf("%w: required-match subplan return bind %q must be bound to the integer constant %d",
			ErrUnsupportedPostgresPlan, expression.Value.BindKey, requiredMatchResultValue)
	}
	return nil
}

// nextTraversalSequence hands out the renderer-owned alias sequence shared
// by lateral child subqueries and EXISTS steps, so every edge and target
// alias in one statement is unique and deterministic for the plan.
func (r *planRenderer) nextTraversalSequence() int {
	r.traversalSeq++
	return r.traversalSeq
}

// ---------------------------------------------------------------------------
// Scoped predicate machinery.
//
// The main sequence and EXISTS subplans share one predicate renderer; the
// rows map parameterizes variable resolution and the allowSelectorExtract
// flag closes the subplan-only selector form. The main sequence renders
// with (r.rows, false), keeping every pre-existing refusal byte-identical.
// ---------------------------------------------------------------------------

// resolveValueInRows maps one plan value onto either a trusted column
// expression or a parameterized JSONB payload path over its row alias,
// resolving variables exclusively through rows. Derived (unnest.go) rows
// are refused here: they carry no table alias and resolve only through
// resolveValue's resolveDerivedValue branch in the main sequence.
func (r *planRenderer) resolveValueInRows(rows map[string]rowScope, value ir.PhysicalValue) (string, error) {
	scope, ok := rows[value.Variable]
	if !ok || scope.derivedValue != "" {
		return "", fmt.Errorf("%w: variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 1 && resourceFilterColumns[value.Path[0]] {
		return scope.alias + "." + r.storage.Resource.MustColumn(canonicalKeyColumn(value.Path[0])).SQLText(), nil
	}
	return r.documentPathExpression(scope.alias, value.Path)
}

// renderPredicateFragmentScoped renders COMPARISON, ALL, and NOT predicate
// trees against rows. EXISTS and ANY are refused: EXISTS at the top level
// is dispatched by renderPredicateFragment (nested composition beyond one
// required match is not registered), and ANY is unregistered package-wide.
func (r *planRenderer) renderPredicateFragmentScoped(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicateExpression,
	rows map[string]rowScope,
	allowSelectorExtract bool,
) (predicateFragment, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return predicateFragment{}, fmt.Errorf("%w: COMPARISON predicate has no payload", ErrUnsupportedPostgresPlan)
		}
		return r.renderComparisonFragmentScoped(plan, *predicate.Comparison, rows, allowSelectorExtract)
	case ir.PhysicalNotPredicate:
		if len(predicate.Children) != 1 {
			return predicateFragment{}, fmt.Errorf("%w: NOT requires exactly one child", ErrUnsupportedPostgresPlan)
		}
		child, err := r.renderPredicateFragmentScoped(plan, predicate.Children[0], rows, allowSelectorExtract)
		if err != nil {
			return predicateFragment{}, err
		}
		if child.mayYieldNull {
			return predicateFragment{sql: "COALESCE(NOT (" + child.sql + "), true)"}, nil
		}
		return predicateFragment{sql: "NOT (" + child.sql + ")"}, nil
	case ir.PhysicalAllPredicate:
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ALL requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragmentScoped(plan, child, rows, allowSelectorExtract)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " AND "), mayYieldNull: mayYieldNull}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: predicate kind %q is not part of the registered traversal predicate surface",
			ErrUnsupportedPostgresPlan, string(predicate.Kind))
	}
}

// renderComparisonFragmentScoped renders one comparison against rows. The
// LeftExpression form is registered only for REQUIRED-match subplan filters
// (allowSelectorExtract) and is owned end to end by
// renderSelectorMatchFragment, the mirror of the AQL selector predicate.
func (r *planRenderer) renderComparisonFragmentScoped(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicate,
	rows map[string]rowScope,
	allowSelectorExtract bool,
) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.LeftExpression != nil {
		if !allowSelectorExtract {
			return predicateFragment{}, fmt.Errorf("%w: selector-expression and correlated predicates are not yet implemented for PostgreSQL",
				ErrUnsupportedPostgresPlan)
		}
		return r.renderSelectorMatchFragment(plan, predicate, rows)
	}
	if predicate.Quantifier != "" {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	if predicate.Left.BindKey != "" {
		return predicateFragment{}, fmt.Errorf("%w: filter left side must be a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	var left string
	var err error
	switch {
	case predicate.Left.Variable != "":
		left, err = r.resolveValueInRows(rows, predicate.Left)
		if err != nil {
			return predicateFragment{}, err
		}
	default:
		return predicateFragment{}, fmt.Errorf("%w: filter must read a row path or a registered selector expression",
			ErrUnsupportedPostgresPlan)
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	switch operator {
	case "EXISTS", "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: %s must not carry a right value", ErrUnsupportedPostgresPlan, operator)
		}
		if isPayloadExpression(left) {
			presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
			if operator == "EXISTS" {
				return predicateFragment{sql: presence + " <> 'null'"}, nil
			}
			return predicateFragment{sql: presence + " = 'null'"}, nil
		}
		// Registered storage columns are stored NOT NULL and jsonb_typeof has
		// no text/timestamptz input, so column presence is nullability itself.
		if operator == "EXISTS" {
			return predicateFragment{sql: left + " IS NOT NULL"}, nil
		}
		return predicateFragment{sql: left + " IS NULL"}, nil
	case "EQUALS", "NOT_EQUALS", "GT", "GTE", "LT", "LTE", "IN":
	default:
		return predicateFragment{}, fmt.Errorf("%w: filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	if !isPayloadExpression(left) {
		return r.renderColumnComparison(left, operator, predicate.ValueKind, value)
	}
	return r.renderPayloadComparison(left, operator, predicate.ValueKind, value)
}

// renderSelectorMatchFragment renders the canonical REQUIRED-match selector
// filter lower/required_match.go:131-137 emits: a COMPARISON whose left side
// is an ARRAY-cardinality, EMPTY_ON_NULL EXTRACT over the reached row's
// ["payload"] document, with a validated ValueKind and an optional
// ANY/ALL/NONE quantifier. It mirrors render/aql/predicates.go
// renderSelectorPredicate (lines 212-266) term for term:
//
//   - The selector's value list is normalized exactly as AQL's selector FOR
//     loop returns it for the registered direct-step form: a missing or
//     JSON-null document path yields zero values; a present, non-null value
//     yields the single-element list — the value whole, a stored array
//     included. The non-iterated terminal is LET __value = cur.field
//     FILTER __value != null RETURN __value (render/aql/selectors.go:343,
//     extractFinalExpr default at render/aql/selector.go:48-50): arrays are
//     never unpacked here, matching the main sequence's 0..1-item reading
//     of the same shape (predicates.go). (ArangoDB propagates undefined
//     through missing intermediate properties and drops undefined from
//     RETURN, so every absent/null shape observes zero values; SQL NULL and
//     the jsonb 'null' scalar are the PostgreSQL counterparts.)
//   - EXISTS/MISSING test the value list for emptiness, EQUALS/NOT_EQUALS/
//     IN/GT..LTE test the quantified element match, and the empty quantifier
//     means ANY exactly as predicates.go:256-258 defaults it.
//   - Element comparisons are the package's registered selector match,
//     predicates.go selectorMatchExpression: typed bind validation per
//     value kind, the temporal timestamptz guards for DATE/DATE_TIME
//     ordering, the numeric-ordering guard against jsonb cross-type
//     misordering, the IN list reading, and CONTAINS_TEXT as the shared
//     capability gap. The subplan match and the main-sequence selector
//     predicate therefore agree term for term on the same item.
//
// The selector shape itself is re-validated against the closed direct-
// property subset (the same refusals selectors.go registers for projection
// selectors, plus the exact ARRAY/EMPTY_ON_NULL/"payload"-source form the
// lowerer emits); nothing outside the subset produces SQL. ir-plan
// validation runs ahead of the renderer in RenderPhysicalPlan, so these
// checks are defense-in-depth for the unexported entry points.
func (r *planRenderer) renderSelectorMatchFragment(
	plan ir.PhysicalPlan,
	predicate ir.PhysicalPredicate,
	rows map[string]rowScope,
) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Quantifier != "" && !predicate.Quantifier.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not part of the registered traversal predicate surface",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	quantifier := predicate.Quantifier
	if quantifier == "" {
		quantifier = spec.QuantifierAny
	}
	values, err := r.selectorValuesExpression(*predicate.LeftExpression, rows)
	if err != nil {
		return predicateFragment{}, err
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	length := "jsonb_array_length(" + values + ")"
	switch operator {
	case "EXISTS":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: EXISTS must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "(" + length + " > 0)"}, nil
	case "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: MISSING must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "(" + length + " = 0)"}, nil
	case "EQUALS", "NOT_EQUALS", "IN", "GT", "GTE", "LT", "LTE":
	case "CONTAINS_TEXT":
		return predicateFragment{}, fmt.Errorf("%w: selector CONTAINS_TEXT match needs the AQL TO_STRING/CONTAINS reading (render/aql/predicates.go:243) and is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if !predicate.ValueKind.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: selector filter value kind %q is invalid", ErrUnsupportedPostgresPlan, string(predicate.ValueKind))
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	// The per-item comparison is the package's registered selector match
	// (predicates.go selectorMatchExpression): typed bind validation per
	// value kind, the temporal timestamptz guards for DATE/DATE_TIME, the
	// numeric-ordering guard, the IN list reading, and the CONTAINS_TEXT
	// gap. Reusing it keeps the subplan match from drifting from the main
	// sequence it must agree with; only the multiset normalization and the
	// quantifier skeleton above are traversal-owned AQL parity.
	match, err := r.selectorMatchExpression("__sv.v", operator, predicate.ValueKind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	anyMatch := "EXISTS (SELECT 1 FROM jsonb_array_elements(" + values + ") AS __sv(v) WHERE " + match + ")"
	switch quantifier {
	case spec.QuantifierAny:
		return predicateFragment{sql: "(" + anyMatch + ")"}, nil
	case spec.QuantifierNone:
		return predicateFragment{sql: "(NOT " + anyMatch + ")"}, nil
	default: // ALL: non-empty and no failing element, per predicates.go:265.
		// The values list is null-free by construction, so NOT(exists
		// mismatch) is the "every element matches" witness.
		mismatch := "EXISTS (SELECT 1 FROM jsonb_array_elements(" + values + ") AS __sv(v) WHERE NOT (" + match + "))"
		return predicateFragment{sql: "(" + length + " > 0 AND NOT " + mismatch + ")"}, nil
	}
}

// selectorValuesExpression validates the canonical REQUIRED-match selector
// extraction and returns its null-free jsonb value list. The registered
// direct-step form's terminal does NOT unpack stored arrays:
// render/aql/selectors.go:343 closes the non-iterated chain with
// LET __value = cur.field FILTER __value != null RETURN __value (the
// extractFinalExpr default branch, render/aql/selector.go:48-50), so a
// present, non-null document value yields exactly one value — the value
// whole, arrays included — exactly as the main sequence registers the same
// ARRAY/EMPTY_ON_NULL shape (predicates.go 0..1-item reading):
//
//	missing | jsonb-null -> '[]'
//	otherwise            -> [value]
func (r *planRenderer) selectorValuesExpression(
	expression ir.PhysicalExpression,
	rows map[string]rowScope,
) (string, error) {
	if expression.Kind != ir.PhysicalExtractExpression {
		return "", fmt.Errorf("%w: required-match filter left side must be an EXTRACT selector expression, got kind %q",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: required-match filter selector is missing its EXTRACT payload", ErrUnsupportedPostgresPlan)
	}
	// The ir plan gate rejects non-array selector predicate left sides
	// (physical_validation.go:1359-1360) and lower emits exactly the
	// ARRAY/EMPTY_ON_NULL form (lower/required_match.go:135-137); anything
	// else reaching here is off the registered surface.
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: required-match filter selector must be the ARRAY-cardinality form lower/required_match.go emits; got %q",
			ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: required-match filter selector must carry the EMPTY_ON_NULL null behavior lower/required_match.go emits; got %q",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return "", fmt.Errorf("%w: required-match filter selector mode CONDITIONAL_ARRAY requires repeated-step rendering, which is not registered for PostgreSQL", ErrUnsupportedPostgresPlan)
	default:
		return "", fmt.Errorf("%w: required-match filter selector execution mode %q is unknown",
			ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Distinct {
		return "", fmt.Errorf("%w: required-match filter selector DISTINCT changes element multiplicity the quantified match does not model (render/aql/predicates.go reads the raw value list)", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return "", fmt.Errorf("%w: required-match filter selector fallbacks are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.Prepared != nil {
		return "", fmt.Errorf("%w: required-match filter selector prepared-set references are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: required-match filter selector unit normalization is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return "", fmt.Errorf("%w: required-match filter selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Selector.Steps) == 0 {
		return "", fmt.Errorf("%w: required-match filter selector is empty", ErrUnsupportedPostgresPlan)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		switch {
		case step.Field == "":
			return "", fmt.Errorf("%w: required-match filter selector step %d has no property", ErrUnsupportedPostgresPlan, index)
		case step.Iterate:
			return "", fmt.Errorf("%w: required-match filter selector repeated ([]) step %d requires array rendering, which is not registered", ErrUnsupportedPostgresPlan, index)
		case step.Index != nil:
			return "", fmt.Errorf("%w: required-match filter selector indexed step %d requires array rendering, which is not registered", ErrUnsupportedPostgresPlan, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: required-match filter selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
		path = append(path, step.Field)
	}
	if extract.Source.BindKey != "" {
		return "", fmt.Errorf("%w: required-match filter selector source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf(`%w: required-match filter selector source must be the row document at ["payload"], the exact form lower/required_match.go emits; got %v`,
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	scope, ok := rows[extract.Source.Variable]
	if !ok {
		return "", fmt.Errorf("%w: required-match filter selector variable %q does not bind a readable subplan row",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" {
		return "", fmt.Errorf("%w: required-match filter selector must name the schema resource type it was validated against", ErrUnsupportedPostgresPlan)
	}
	if extract.ResourceType != scope.resourceType {
		return "", fmt.Errorf("%w: required-match filter selector validates against resource type %q, but the row is joined as %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	extract_, err := r.documentPathExpression(scope.alias, path)
	if err != nil {
		return "", err
	}
	document := "(" + extract_ + ")"
	return "(CASE WHEN " + document + " IS NULL OR jsonb_typeof(" + document + ") = 'null' THEN '[]'::jsonb" +
		" ELSE jsonb_build_array(" + document + ") END)", nil
}
