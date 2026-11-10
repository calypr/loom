package postgres

// This file registers the correlated related-record set and its reductions
// as an implemented, closed PostgreSQL operation slice (pg-sql-08), plus the
// RELATED existence filter the recipe builder lowers beside it. It is the
// single authority in this package for the ir.PhysicalSetOp surface.
//
// The AQL counterpart of the set is render/aql/navigation_render.go
// renderSet (lines 217-332): a LET over a FOR loop inside a LET, so the set
// never changes the parent row count, and a parent with an empty set is
// still emitted. The registered PostgreSQL spelling keeps that reading with
// per-field scalar sublinks in the SELECT list: the set materializes no
// join, so the outer row count is untouched by construction, and each
// reduction field reads exactly its own aggregate over the correlated child
// items (a NULL-correlated parent — an OPTIONAL anchor with no matching
// occurrence — aggregates over zero rows, which is AQL's empty-set reading
// with the parent preserved).
//
// The closed child-set shape is the one lower/child_sets.go
// buildOptionalChildPhysicalSet (lines 13-153) produces:
//
//   - Subplan: exactly one capture (the correlation anchor), exactly one
//     depth-one TRAVERSAL step anchored on it (the shared
//     validateTraversalContract re-reads every discriminator), the one
//     canonical project/dataset_generation/auth block
//     lower/plan_orchestration.go emits for the hop's edge and target rows
//     (consumed via population.go matchCanonicalScopeBlock and ignored:
//     scope.go's re-read structural predicates are the single scope
//     authority, D5), then zero or more typed FILTER operations rendered by
//     the shared renderPredicateFragmentScoped contract over subplan-local
//     rows, and the child_sets.go:88 canonical return: a VALUE expression,
//     OBJECT cardinality, PRESERVE_NULL null behavior, over the reached
//     target document.
//   - Set flags: Unique and SortByKey are pinned true (child_sets.go:88);
//     the renderer's deterministic (logical_id, element-ordinality) order
//     implements the SortByKey contract, and UNIQUE(items) is the
//     SELECT-DISTINCT item dedup below.
//   - Reduction: FIRST/ALL/DISTINCT over selector slots projected in the
//     child subquery (child_sets.go:507-531), read downstream as
//     {variable: <set>_reduced, path: [name]}. The reduced variable is
//     registered as a renderer-derived jsonb value (unnest.go
//     registerDerivedValue), so it reads only through
//     resolveValue/resolveDerivedValue — never as a join, filter, sort, or
//     selector source.
//
// Reduction parity (render/aql/navigation_render.go
// renderPhysicalSetReduction, lines 371-392):
//
//   - FIRST  ~ FIRST(FLATTEN(items[*].slot)):  first element in set order
//     (SORT BY target._key → logical_id, then slot-array ordinality), NULL
//     over the empty set exactly as AQL FIRST([]) reads NULL.
//   - ALL    ~ FLATTEN(items[*].slot):        every element in set order,
//     duplicates preserved, '[]' over the empty set.
//   - DISTINCT ~ SORTED_UNIQUE(FLATTEN(...)):  cited-and-diverged (plan
//     ruling D1, accepted on the ticket's identity-aware acceptance): the
//     PostgreSQL rendering dedups by (child identity, value) — duplicate
//     values inside one child collapse, equal values from two different
//     children both survive. See the DISTINCT sublink below.
//
// Items are the deduped child documents: SELECT DISTINCT
// (logical_id, resource_type, payload) over the edge INNER JOIN resource
// occurrence join. That mirrors AQL UNIQUE(set) over identical reached
// documents (navigation_render.go:311-314): two occurrences of the same
// edge to the same child contribute one item, while unresolved targets
// never join (the INNER edge-to-target join is the traversal.go exactness
// decision).
//
// The RELATED existence filter (lower/recipe_construction_related_eligibility.go
// :208-213) is the FILTER-side sibling of the REQUIRED match: a bounded
// correlated EXISTS over one or more chained hops, each hop followed by its
// canonical scope block, typed FILTER operations over reached rows, and the
// canonical scalar {_id} return physicalValueExpression emits. EXISTS is a
// semi-join: it never multiplies parent rows, is total (never SQL NULL), so
// the registered top-level NOT(EXISTS) form needs no COALESCE guard. The
// predicate dispatch in predicates.go classifies each whole-FILTER EXISTS:
// the REQUIRED constant-1 return stays with traversal.go
// renderRequiredMatchExists unchanged; the canonical RELATED {_id} return is
// rendered here; anything else keeps the legacy traversal.go refusal, so
// every pre-existing golden error stays byte-identical.
//
// Refusals (all ErrUnsupportedPostgresPlan, never an AQL fallback): shared
// subsets over a materialized set (renderSharedSubset has no registered
// PostgreSQL contract), prepared sets, node-path sets, a set without a
// reduction contract (nothing downstream can read the items), a set
// without its selector projection, subplan projection modifiers
// (Sort/Unique/DistinctBy), nested SET or UNNEST inside a subplan,
// CollectionScan/KeySetLookup/DocumentLookup steps (the subplan-argument
// call surface stays unregistered this wave), plan-authored scope blocks
// that are not exactly canonical, OPTIONAL anchors for EXISTS (the
// traversal.go:350-354 rule — a NULL-extended anchor column would let
// subplan FILTERs compare absent rows), and every return shape outside the
// two canonical pins.
//
// All plan data still crosses only the $n boundary: labels, types, paths,
// filter values, selector paths, and the reduction field names reaching
// jsonb_build_object are bound parameters; only registry-validated
// identifiers and renderer-owned aliases reach SQL text.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// correlatedSetBody is the one validated, one-time rendering of a child
// subplan's correlated occurrence join: the FROM list (edge relation plus
// the INNER edge-to-target join), the WHERE conjunction (correlated edge
// predicates, the canonical-ignored scope block's structural replacement,
// and the typed subplan FILTERs), and the reached target row's binding.
// Every SELECT-list sublink for one set reuses this text, so a multi-field
// reduction executes the same correlated scan per field without ever
// re-adding duplicate bind parameters.
type correlatedSetBody struct {
	from        string
	where       string
	targetAlias string
	targetType  string
	targetVar   string
}

// renderCorrelatedChildSteps validates and renders the operation list both
// registered correlated surfaces share. allowOptionalCapture distinguishes
// the SET anchor (an OPTIONAL row's NULL correlation aggregates over zero
// child rows, the AQL LET-set reading) from the EXISTS anchor
// (traversal.go:350-354: NULL-extended anchor columns may not correlate a
// semi-join). multipleTraversals distinguishes the single-hop child set
// (child_sets.go emits exactly one) from the chained related-eligibility
// route.
func (r *planRenderer) renderCorrelatedChildSteps(
	plan ir.PhysicalPlan,
	subplan ir.PhysicalSubplan,
	allowOptionalCapture bool,
	multipleTraversals bool,
) (correlatedSetBody, error) {
	if len(subplan.Captures) != 1 {
		return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan must capture exactly one correlation anchor, got %d captures",
			ErrUnsupportedPostgresPlan, len(subplan.Captures))
	}
	captureVariable := subplan.Captures[0]
	capture, ok := r.rows[captureVariable]
	if !ok || capture.derivedValue != "" {
		return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan capture %q does not bind a readable row (edge variables, set variables, computed values, and unreached variables cannot anchor a correlation)",
			ErrUnsupportedPostgresPlan, captureVariable)
	}
	if capture.optional && !allowOptionalCapture {
		return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan capture %q is a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL; a NULL-extended anchor column would let the semi-join compare absent rows (the traversal.go:350-354 required-match rule)",
			ErrUnsupportedPostgresPlan, captureVariable)
	}

	rows := map[string]rowScope{captureVariable: capture}
	fromParts := make([]string, 0, 4)
	whereParts := make([]string, 0, 4)
	sawTraversal := false
	nextSource := capture
	nextSourceVariable := captureVariable
	for index := 0; index < len(subplan.Operations); {
		operation := subplan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalTraversalOp:
			if operation.Traversal == nil {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: TRAVERSAL payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			if sawTraversal && !multipleTraversals {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: the registered child set carries exactly one depth-one traversal hop (lower/child_sets.go buildOptionalChildPhysicalSet); chained routes belong to the related-existence surface",
					ErrUnsupportedPostgresPlan, index)
			}
			traversal := *operation.Traversal
			if traversal.SourceVariable != nextSourceVariable {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: traversal must anchor on %q (the capture for the first hop, the previous hop's reached row thereafter), got source %q",
					ErrUnsupportedPostgresPlan, index, nextSourceVariable, traversal.SourceVariable)
			}
			verified, err := r.validateTraversalContract(plan, traversal, nextSource)
			if err != nil {
				return correlatedSetBody{}, fmt.Errorf("correlated subplan operation %d: %w", index, err)
			}
			seq := r.nextTraversalSequence()
			edgeAlias := "e" + strconv.Itoa(seq)
			targetAlias := "x" + strconv.Itoa(seq)
			edgeWhere, targetOn := r.edgeOccurrencePredicates(verified, edgeAlias, targetAlias, nextSource.alias)
			if !sawTraversal {
				// The first hop's edge relation is the subplan FROM; its
				// predicates carry the outer correlation and land in WHERE.
				fromParts = append(fromParts, "FROM "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias)
				whereParts = append(whereParts, edgeWhere)
			} else {
				fromParts = append(fromParts,
					"INNER JOIN "+r.storage.Edge.Identifier().SQLText()+" AS "+edgeAlias+" ON "+edgeWhere)
			}
			fromParts = append(fromParts,
				"INNER JOIN "+r.storage.Resource.Identifier().SQLText()+" AS "+targetAlias+" ON "+targetOn)
			rows[traversal.TargetVariable] = rowScope{alias: targetAlias, resourceType: verified.targetType}
			nextSource = rows[traversal.TargetVariable]
			nextSourceVariable = traversal.TargetVariable
			sawTraversal = true
			index++
			// The lowering always emits the canonical scope block for a
			// hop's edge and target rows (lower/child_sets.go:24-26 and
			// recipe_construction_related_eligibility.go:155-160). It is
			// consumed exactly and ignored: the structural predicates from
			// scope.go above are the single scope authority, and any block
			// that is not canonically exact is a typed refusal.
			if err := matchCanonicalScopeBlock(subplan.Operations, &index,
				[]string{traversal.EdgeVariable, traversal.TargetVariable}); err != nil {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: canonical subplan scope block: %s",
					ErrUnsupportedPostgresPlan, index, err.Error())
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: FILTER payload is missing",
					ErrUnsupportedPostgresPlan, index)
			}
			if !sawTraversal {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: a FILTER may only follow a traversal step (the reduction is over reached child rows, and subplan scope is structural)",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.Filter.Expression == nil {
				return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: subplan filters must use the typed Expression form the lowering emits",
					ErrUnsupportedPostgresPlan, index)
			}
			clause, err := r.renderPredicateFragmentScoped(plan, *operation.Filter.Expression, rows, true)
			if err != nil {
				return correlatedSetBody{}, fmt.Errorf("correlated subplan operation %d: %w", index, err)
			}
			whereParts = append(whereParts, clause.sql)
			index++
		default:
			return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d has kind %q; only TRAVERSAL hops (each closing with its canonical scope block) and typed FILTER operations are registered — shared subsets, nested sets and unnests, collection scans, keyed lookups, document lookups, and non-canonical scope operations are capability gaps",
				ErrUnsupportedPostgresPlan, index, string(operation.Kind))
		}
	}
	if !sawTraversal {
		return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan must contain at least one traversal hop", ErrUnsupportedPostgresPlan)
	}
	return correlatedSetBody{
		from:        strings.Join(fromParts, " "),
		where:       strings.Join(whereParts, " AND "),
		targetAlias: nextSource.alias,
		targetType:  nextSource.resourceType,
		targetVar:   nextSourceVariable,
	}, nil
}

// renderSet registers one ir.PhysicalSetOp: it validates the closed
// child-set contract, renders each reduction field as one correlated
// scalar sublink over the deduped child items, and binds the reduced
// variable as a renderer-derived jsonb value. The set variable itself is
// deliberately never bound: the registered surface has no item-array
// consumer, so any direct read of the set fails closed as an unbound
// variable instead of rendering an unregistered aggregate.
//
// Derived reads inline their expression, so each projection of the reduced
// variable repeats the sublink text once per reader. That duplication is
// textual only: PostgreSQL's planner merges structurally equal scalar
// sublinks into one SubPlan (backend/optimizer/README sameexpr rules), so
// each distinct correlated scan still executes once per outer row.
func (r *planRenderer) renderSet(plan ir.PhysicalPlan, set ir.PhysicalSet) error {
	if set.SourceSetVariable != "" {
		return fmt.Errorf("%w: shared subsets over a materialized set (source set %q) have no registered PostgreSQL contract (the AQL counterpart is render/aql/navigation_render.go renderSharedSubset)",
			ErrUnsupportedPostgresPlan, set.SourceSetVariable)
	}
	if set.Prepared != nil {
		return fmt.Errorf("%w: prepared sets are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	switch set.Kind {
	case "", ir.PhysicalNodeSetKind:
	case ir.PhysicalNodePathSetKind:
		return fmt.Errorf("%w: NODE_PATH sets have no registered PostgreSQL contract (the graph-return surface is a separate capability)", ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown set kind %q", ErrUnsupportedPostgresPlan, string(set.Kind))
	}
	if set.Reduction == nil {
		return fmt.Errorf("%w: set %q has no reduction contract; the registered surface renders sets only through their FIRST/ALL/DISTINCT selector reductions, because no other consumer of the item array is registered",
			ErrUnsupportedPostgresPlan, set.Variable)
	}
	if set.Projection == nil {
		return fmt.Errorf("%w: set %q reduction requires the traversal-time selector projection lower/child_sets.go computes in the child subquery",
			ErrUnsupportedPostgresPlan, set.Variable)
	}
	if !set.Unique || !set.SortByKey {
		return fmt.Errorf("%w: set %q must carry Unique and SortByKey (the child_sets.go:88 contract the (logical_id, element-ordinality) render order implements)",
			ErrUnsupportedPostgresPlan, set.Variable)
	}
	if set.Subplan.Sort != nil || set.Subplan.Unique || set.Subplan.DistinctBy != nil {
		return fmt.Errorf("%w: set %q subplan cannot carry projection-only modifiers (the set-level SortByKey/Unique contract owns the ordering)",
			ErrUnsupportedPostgresPlan, set.Variable)
	}

	corr, err := r.renderCorrelatedChildSteps(plan, set.Subplan, true, false)
	if err != nil {
		return fmt.Errorf("set %q: %w", set.Variable, err)
	}

	// The child_sets.go:88 canonical return: the whole reached target
	// document, OBJECT cardinality, PRESERVE_NULL. EXISTS-style value
	// returns and selector returns are other capabilities' shapes.
	ret := set.Subplan.Return
	if ret.Kind != ir.PhysicalValueExpression || ret.Cardinality != ir.PhysicalObjectCardinality ||
		ret.NullBehavior != ir.PhysicalPreserveNull || ret.Value == nil ||
		ret.Value.BindKey != "" || ret.Value.Variable != corr.targetVar || len(ret.Value.Path) != 0 {
		return fmt.Errorf("%w: set %q subplan return must be the canonical whole-document VALUE expression over the reached target row (lower/child_sets.go buildOptionalChildPhysicalSet)",
			ErrUnsupportedPostgresPlan, set.Variable)
	}

	projectionFields := make(map[string]ir.PhysicalSetProjectionField, len(set.Projection.Fields))
	for _, field := range set.Projection.Fields {
		if _, duplicate := projectionFields[field.Name]; duplicate {
			return fmt.Errorf("%w: set %q projection field %q is duplicated", ErrUnsupportedPostgresPlan, set.Variable, field.Name)
		}
		projectionFields[field.Name] = field
	}

	objectArgs := make([]string, 0, len(set.Reduction.Fields))
	for _, field := range set.Reduction.Fields {
		projectionField, ok := projectionFields[field.SourceField]
		if !ok {
			return fmt.Errorf("%w: set %q reduction field %q reads projection slot %q, which the set projection does not declare",
				ErrUnsupportedPostgresPlan, set.Variable, field.Name, field.SourceField)
		}
		keyIndex := r.textParameter(field.Name, "text:"+field.Name)
		value, err := r.setReductionSublink(set.Variable, field.Mode, projectionField, corr)
		if err != nil {
			return fmt.Errorf("set %q reduction field %q: %w", set.Variable, field.Name, err)
		}
		// jsonb_build_object is variadic "any": the bound key parameter
		// carries no inferable type, so it is cast to text explicitly
		// (SQLSTATE 42P18 otherwise; the sublink values are jsonb-typed by
		// construction).
		objectArgs = append(objectArgs, "$"+strconv.Itoa(keyIndex)+"::text, "+value)
	}
	object := "jsonb_build_object(" + strings.Join(objectArgs, ", ") + ")"
	return r.registerDerivedValue(set.Reduction.Variable, object, true)
}

// setProjectionSlotExpression renders one projection slot's null-free
// 0..1-item value list over the deduped item row: the shared
// selectorValuesExpression normalization (missing or JSON-null document
// path → '[]', present value → the value whole), evaluated against the
// item subquery's payload column. A stored array is never unpacked here —
// FLATTEN happens in the reduction sublink's WITH ORDINALITY expansion.
func (r *planRenderer) setProjectionSlotExpression(
	field ir.PhysicalSetProjectionField,
	corr correlatedSetBody,
	itemAlias string,
) (string, error) {
	rows := map[string]rowScope{corr.targetVar: {alias: itemAlias, resourceType: corr.targetType}}
	expression := ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: corr.targetVar, Path: []string{"payload"}},
			ResourceType:  field.ResourceType,
			Selector:      field.Selector,
			ExecutionMode: field.ExecutionMode,
		},
	}
	slot, err := r.selectorValuesExpression(expression, rows)
	if err != nil {
		return "", fmt.Errorf("projected slot %q: %w", field.Name, err)
	}
	return slot, nil
}

// setReductionSublink renders one reduction field as a correlated scalar
// sublink over the set's deduped child items.
//
// The item subquery is the SELECT-DISTINCT dedup of (logical_id,
// resource_type, payload) over the correlated occurrence join — the
// counterpart of AQL UNIQUE(set) (navigation_render.go:311-314), so
// repeated edge occurrences to one child contribute one item. The
// WITH ORDINALITY expansion of each item's slot list is the FLATTEN;
// the deterministic total order is (logical_id, element ordinality),
// the AQL SORT BY target._key (SortByKey) plus slot-array order:
//
//   - FIRST:  ORDER BY that total order LIMIT 1 — NULL over zero elements,
//     AQL FIRST([]) parity.
//   - ALL:    jsonb_agg over the total order, COALESCE'd to '[]'.
//   - DISTINCT: identity-aware (plan ruling D1 on the ticket's
//     acceptance): DISTINCT ON (item identity, element value) keeping the
//     lowest ordinality inside the group, then jsonb_agg over
//     (logical_id, element ordinality). Duplicate values inside one child
//     collapse as SORTED_UNIQUE does; equal values from two different
//     children both survive, which the AQL value-level SORTED_UNIQUE
//     (navigation_render.go:384-385) would erase. Elements of a jsonb
//     array are never SQL NULL (a stored null is the jsonb 'null' value),
//     so nulls flow through exactly as AQL FLATTEN keeps them.
func (r *planRenderer) setReductionSublink(
	setVariable string,
	mode ir.PhysicalSetReductionMode,
	field ir.PhysicalSetProjectionField,
	corr correlatedSetBody,
) (string, error) {
	seq := r.nextTraversalSequence()
	itemAlias := "i" + strconv.Itoa(seq)
	elementAlias := "s" + strconv.Itoa(seq)
	distinctAlias := "u" + strconv.Itoa(seq)
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()

	items := "(SELECT DISTINCT " + corr.targetAlias + "." + logicalIDColumn + " AS " + logicalIDColumn +
		", " + corr.targetAlias + "." + resourceTypeColumn + " AS " + resourceTypeColumn +
		", " + corr.targetAlias + "." + payloadColumn + " AS " + payloadColumn +
		" " + corr.from + " WHERE " + corr.where + ")"
	slot, err := r.setProjectionSlotExpression(field, corr, itemAlias)
	if err != nil {
		return "", err
	}
	lateral := " CROSS JOIN LATERAL jsonb_array_elements(" + slot + ") WITH ORDINALITY AS " +
		elementAlias + "(v, ord)"
	itemOrder := itemAlias + "." + logicalIDColumn
	elementOrder := elementAlias + ".ord"

	switch mode {
	case ir.PhysicalSetReductionFirst:
		return "(SELECT " + elementAlias + ".v FROM " + items + " AS " + itemAlias + lateral +
			" ORDER BY " + itemOrder + ", " + elementOrder + " LIMIT 1)", nil
	case ir.PhysicalSetReductionAll:
		return "(SELECT COALESCE(jsonb_agg(" + elementAlias + ".v ORDER BY " + itemOrder + ", " + elementOrder +
			"), '[]'::jsonb) FROM " + items + " AS " + itemAlias + lateral + ")", nil
	case ir.PhysicalSetReductionDistinct:
		return "(SELECT COALESCE(jsonb_agg(" + distinctAlias + ".v ORDER BY " +
			distinctAlias + "." + logicalIDColumn + ", " + distinctAlias + ".ord), '[]'::jsonb) FROM " +
			"(SELECT DISTINCT ON (" + itemOrder + ", " + elementAlias + ".v) " +
			itemOrder + " AS " + logicalIDColumn + ", " + elementAlias + ".v AS v, " + elementAlias + ".ord AS ord FROM " +
			items + " AS " + itemAlias + lateral +
			" ORDER BY " + itemOrder + ", " + elementAlias + ".v, " + elementOrder + ") " + distinctAlias + ")", nil
	default:
		return "", fmt.Errorf("%w: set %q reduction field %q has unsupported mode %q",
			ErrUnsupportedPostgresPlan, setVariable, field.Name, string(mode))
	}
}

// isRelatedExistsSubplan is the cheap shape gate the predicates.go dispatch
// uses to split whole-FILTER EXISTS subplans between the two registered
// correlated surfaces. It never errors: the REQUIRED constant-1 return
// (validateRequiredMatchReturn) stays with traversal.go untouched, the
// canonical related-eligibility {_id} return routes here, and every other
// shape falls through to the legacy traversal.go refusal so all
// pre-existing golden errors stay byte-identical. The two canonical return
// shapes are disjoint: REQUIRED binds an integer constant with no variable,
// RELATED reads the last hop's target row at ["_id"].
func isRelatedExistsSubplan(subplan *ir.PhysicalSubplan) bool {
	if subplan == nil || len(subplan.Captures) != 1 {
		return false
	}
	if subplan.Sort != nil || subplan.Unique || subplan.DistinctBy != nil {
		return false
	}
	lastTarget := ""
	for _, operation := range subplan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
			lastTarget = operation.Traversal.TargetVariable
		}
	}
	if lastTarget == "" {
		return false
	}
	ret := subplan.Return
	return ret.Kind == ir.PhysicalValueExpression &&
		ret.Cardinality == ir.PhysicalScalarCardinality &&
		ret.NullBehavior == ir.PhysicalPreserveNull &&
		ret.Value != nil && ret.Value.BindKey == "" &&
		ret.Value.Variable == lastTarget &&
		len(ret.Value.Path) == 1 && ret.Value.Path[0] == "_id"
}

// renderRelatedExists renders the RELATED eligibility semi-join:
// lower/recipe_construction_related_eligibility.go:208-213 emits it for
// RelatedEligibilityExists (and the NOT form) as a bounded correlated
// EXISTS over one or more chained hops, each closing with its canonical
// scope block, typed FILTERs over reached rows, and the canonical scalar
// {_id} return (physicalValueExpression, recipe_construction_related_expand.go:336).
// EXISTS discards the value; the pin keeps the renderer from silently
// accepting another surface's return. Like the REQUIRED match, projection
// modifiers are refused (the AQL bounded renderSubplan refuses them too,
// render/aql/predicates.go:315-317). The result is total — EXISTS is never
// SQL NULL — so the registered NOT form wraps it unguarded.
func (r *planRenderer) renderRelatedExists(plan ir.PhysicalPlan, subplan ir.PhysicalSubplan) (string, error) {
	if subplan.Sort != nil || subplan.Unique || subplan.DistinctBy != nil {
		return "", fmt.Errorf("%w: related-existence subplan cannot carry projection-only sort or unique modifiers (render/aql/predicates.go:315-317 refuses them for bounded EXISTS too; count-style eligibility is a separate capability)",
			ErrUnsupportedPostgresPlan)
	}
	corr, err := r.renderCorrelatedChildSteps(plan, subplan, false, true)
	if err != nil {
		return "", fmt.Errorf("related-existence subplan: %w", err)
	}
	ret := subplan.Return
	if ret.Kind != ir.PhysicalValueExpression || ret.Cardinality != ir.PhysicalScalarCardinality ||
		ret.NullBehavior != ir.PhysicalPreserveNull || ret.Value == nil ||
		ret.Value.BindKey != "" || ret.Value.Variable != corr.targetVar ||
		len(ret.Value.Path) != 1 || ret.Value.Path[0] != "_id" {
		return "", fmt.Errorf("%w: related-existence subplan return must be the canonical scalar {_id} VALUE expression over the last hop's target row (lower/recipe_construction_related_eligibility.go physicalValueExpression form)",
			ErrUnsupportedPostgresPlan)
	}
	return "EXISTS (SELECT 1 " + corr.from + " WHERE " + corr.where + ")", nil
}
