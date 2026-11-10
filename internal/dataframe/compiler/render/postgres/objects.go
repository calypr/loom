package postgres

// This file registers nested object and bounded slice projections as an
// implemented, closed PostgreSQL operation slice (pg-sql-11). It is the
// single authority in this package for reading ir.PhysicalObject and
// ir.PhysicalSlice payloads out of a RETURN projection (and, recursively,
// out of each other).
//
// The AQL counterpart is render/aql/collections.go: renderObject (lines
// 14-68) builds `{ [name]: value }` objects with a stable field sort and a
// MERGE-based per-field omission filter, and renderSlice (lines 70-153)
// emits a bounded correlated FOR loop whose SORT carries the item _key
// tie-break and whose LIMIT is bind-carried. The PostgreSQL spellings
// preserve the same readings:
//
//   - Object keys are bound text parameters of jsonb_build_object (the
//     sets.go:343-347 42P18 idiom: variadic "any" needs the explicit
//     ::text cast); no plan name, path, or value is interpolated into SQL
//     text. Field names reach PostgreSQL only as data.
//   - OMIT_NULLS fields drop their key when the rendered value is SQL NULL
//     or the jsonb 'null' scalar — the exact reading of the AQL omission
//     FILTER (collections.go:63-66, which drops both states because AQL
//     evaluates a missing document property and a stored JSON null to the
//     same null there). Non-omit fields always keep their key, so a
//     PRESERVE_NULL field renders the missing state as SQL NULL (jsonb
//     null via to_jsonb/`#>` per null_semantics.go) exactly where AQL
//     renders the AQL null.
//   - A slice over the single root row is a one-row sublink:
//     SELECT [WHERE predicate] ORDER BY <sort> ASC NULLS FIRST, identity
//     LIMIT $n, aggregated back with COALESCE(jsonb_agg(...), '[]'::jsonb)
//     — the array expression's EMPTY_ON_NULL reading (lower/root_projection.
//     go:465) and the AQL FOR loop's empty iteration.
//
// Registered divergences (L-table):
//
//	L1  jsonb does not preserve object key order: jsonb_build_object output
//	    is canonicalized by PostgreSQL, while the AQL object literal/MERGE
//	    keeps field insertion order (collections.go:19-22 even stable-sorts
//	    fields by name before emitting). Object consumers key on names, and
//	    the acceptance proof is decoded equality across declaration orders
//	    (objects_test.go TestObject11LiveKeyOrderIrrelevance), not text.
//	L2  cross-type jsonb sort order (objects inside arrays inside strings
//	    inside nulls inside numbers) differs from AQL's basic-type order
//	    (null < false < true < number < string < array < document). The
//	    registered sort forms keep homogeneous values: the lowering default
//	    sorts the logical_id text column (lower/root_projection.go:406),
//	    where both orders coincide; a scalar EXTRACT sort compares stored
//	    jsonb values and is registered for same-type-class operands.
//	    SQL NULL sorts first (NULLS FIRST) to match the AQL missing/null
//	    first position.
//	L3  SLICE is registered over exactly two item sources: the single root
//	    row (the singleton form lower/root_projection.go:161 emits) and a
//	    child set whose reduction contract was validated by sets.go — the
//	    D2 handoff (coordinator-ratified): the item relation is the
//	    byte-identical SELECT-DISTINCT (logical_id, resource_type, payload)
//	    dedup the reductions read. Production compact-projection plans
//	    annotate slice extracts with Extract.Prepared via
//	    lower/child_sets.go:573-579 (probe-attested, the D3 evidence), so
//	    those plans keep a cited typed refusal naming the Prepared gap —
//	    no Prepared-bearing plan is ever rendered, exactly the
//	    sets.go:81-95 RELATED handoff pattern.
//
// Prepared-bearing extractions are refused verbatim (D3 handoff, mirroring
// the selectors.go:343-345 registration refusal wording): production
// compact-projection plans carry Extract.Prepared references inside slice
// projections (probe-attested) and stay a cited capability gap.
//
// Recursion discipline (D1, coordinator ruling): OBJECT/SLICE operands may
// nest to depth 4; depth 5 is a typed ErrUnsupportedPostgresPlan refusal.
// The cap is a DEFENSIVE closed-surface bound, not a parity limit: no
// production lowering can derive a tree beyond depth 1, because the only
// SLICE constructor (lower/root_projection.go physicalSliceExpression)
// fills projections with EXTRACT expressions exclusively (lines 459-463),
// and the only RETURN-reachable OBJECT constructor (lower/recipe_expression.
// go lowerDocumentRef, lines 128-146) fills fields with VALUE expressions
// exclusively — so the depth-5 tree is lowering-impossible; AQL's
// renderExpression recursion is unbounded, and the contrast is cited here
// as required.
//
// Every refusal is ErrUnsupportedPostgresPlan; there is no fallback to the
// AQL renderer and no AQL-to-SQL conversion.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// maxCompositeDepth is the D1 defensive nesting cap: the outermost
// RETURN-level OBJECT or SLICE renders at depth 1, and each composite
// operand increments. A composite at depth 5 fails closed.
const maxCompositeDepth = 4

// sliceConsumesSetVariable reports whether any RETURN projection of the
// plan carries a SLICE whose item source is the named set variable,
// recursively through composite operands. It is the pre-scan the sets.go
// D2 gate uses to accept a nil-Reduction set ONLY when the item array has
// this registered consumer; without one, the pg-sql-08 no-consumer
// refusal stays byte-identical (objects_test.go pins it).
func sliceConsumesSetVariable(plan ir.PhysicalPlan, setVariable string) bool {
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for index := range operation.Return.Projections {
			if operation.Return.Projections[index].Expression != nil &&
				expressionConsumesSetVariable(*operation.Return.Projections[index].Expression, setVariable) {
				return true
			}
		}
	}
	return false
}

func expressionConsumesSetVariable(expression ir.PhysicalExpression, setVariable string) bool {
	if slice := expression.Slice; slice != nil {
		if slice.Source.Variable == setVariable && slice.Source.BindKey == "" {
			return true
		}
		for index := range slice.Projections {
			if expressionConsumesSetVariable(slice.Projections[index].Expression, setVariable) {
				return true
			}
		}
	}
	if object := expression.Object; object != nil {
		for index := range object.Fields {
			if expressionConsumesSetVariable(object.Fields[index].Expression, setVariable) {
				return true
			}
		}
	}
	return false
}

// sliceItemVariable is the renderer-owned key the item scope is bound
// under; it never reaches SQL text (the SQL aliases are minted from
// planRenderer.sliceSeq).
const sliceItemVariable = "__loom_slice_item"

// rebindSliceOperand rewrites one item projection's references from the
// slice source variable onto the item scope — the SQL counterpart of the
// AQL loop-variable rebind (render/aql/collections.go:98-106): extraction
// sources become the item document (path kept, so a malformed path still
// meets the closed contract's refusal) and VALUE reads resolve onto the
// item row; OBJECT fields recurse. A nested SLICE operand is deliberately
// not rebound: its own source names a row variable, and the registered
// item sources (L3) are the spine root row and a validated child set, so
// a slice inside a slice item fails closed on its unbound source.
// The plan's own payload is never mutated (the predicate and sort rebinds
// already follow this copy discipline).
func rebindSliceOperand(expression ir.PhysicalExpression, sourceVariable string) ir.PhysicalExpression {
	rewritten := expression
	if extract := rewritten.Extract; extract != nil && extract.Source.Variable == sourceVariable {
		extractCopy := *extract
		extractCopy.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: extractCopy.Source.Path}
		rewritten.Extract = &extractCopy
	}
	if value := rewritten.Value; value != nil && value.Variable == sourceVariable {
		valueCopy := *value
		valueCopy.Variable = sliceItemVariable
		rewritten.Value = &valueCopy
	}
	if object := rewritten.Object; object != nil {
		fields := make([]ir.PhysicalExpressionProjection, len(object.Fields))
		for index, field := range object.Fields {
			fields[index] = field
			fields[index].Expression = rebindSliceOperand(field.Expression, sourceVariable)
		}
		objectCopy := *object
		objectCopy.Fields = fields
		rewritten.Object = &objectCopy
	}
	return rewritten
}

// objectFieldJSON renders one OBJECT/SLICE operand as a jsonb-typed SQL
// expression: document-path extractions and composites are jsonb by
// construction; typed storage columns are lifted with to_jsonb so a SQL
// NULL stays SQL NULL (the null_semantics.go missing reading) instead of
// becoming the jsonb 'null' scalar.
func (r *planRenderer) objectFieldJSON(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return r.compositeValueJSON(rows, expression)
	case ir.PhysicalExtractExpression:
		return r.compositeExtractJSON(rows, expression)
	case ir.PhysicalObjectExpression:
		return r.renderObjectValue(plan, rows, expression, depth+1)
	case ir.PhysicalSliceExpression:
		return r.renderSliceValue(plan, rows, expression, depth+1)
	default:
		return "", fmt.Errorf("%w: composite operand kind %q has no registered PostgreSQL rendering (literal, aggregate, pivot, owner-records, object-lookup, keyed-map, object-keys, key-set, subplan, call, and related-field forms are separate capabilities)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

// compositeValueJSON resolves one VALUE operand over rows through the
// closed storage-identity mapping: the whole document at ["payload"], the
// canonical key references onto logical_id (render.go:119-131,
// window.go:42), the document property resourceType onto the resource_type
// column (the storage identity the store writes, cf. population.go:310),
// the typed scope columns of resourceFilterColumns, and every other
// property path as a bound jsonb document path. The mapping mirrors
// lower/recipe_expression.go lowerDocumentRef's exact field spellings
// (payload/id/resourceType/key).
func (r *planRenderer) compositeValueJSON(rows map[string]rowScope, expression ir.PhysicalExpression) (string, error) {
	value := expression.Value
	if value == nil {
		return "", fmt.Errorf("%w: composite VALUE operand has no value payload", ErrUnsupportedPostgresPlan)
	}
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: composite VALUE operand %v must read a row value, not a bind value", ErrUnsupportedPostgresPlan, value.Path)
	}
	scope, ok := rows[value.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: composite VALUE operand reads variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 0 {
		return "", fmt.Errorf("%w: composite VALUE operand must name a document path or a typed column; the whole-document reference is spelled [\"payload\"] (lower/recipe_expression.go:128-146)",
			ErrUnsupportedPostgresPlan)
	}
	column := func(name string) string {
		return "to_jsonb(" + scope.alias + "." + r.storage.Resource.MustColumn(name).SQLText() + ")"
	}
	if len(value.Path) == 1 {
		segment := value.Path[0]
		switch {
		case segment == "payload":
			if expression.Cardinality != ir.PhysicalObjectCardinality {
				return "", fmt.Errorf(`%w: composite VALUE operand ["payload"] must carry OBJECT cardinality (lower/recipe_expression.go:133-137)`, ErrUnsupportedPostgresPlan)
			}
			return r.documentColumn(scope.alias), nil
		case segment == "resourceType":
			return column("resource_type"), nil
		case segment == "_key" || segment == "id":
			return column(canonicalKeyColumn(segment)), nil
		case resourceFilterColumns[segment]:
			return column(segment), nil
		}
	}
	return r.documentPathExpression(scope.alias, value.Path)
}

// compositeExtractJSON renders one closed selector extraction operand. The
// re-reads mirror validateSelectorExtraction (selectors.go:303-407) over
// the caller's rows: registered behaviors and modes only, no fallbacks,
// no DISTINCT, no unit normalization, no selector filter, direct property
// steps only, the row document at ["payload"] as the exact source, and the
// schema resource type agreeing with the row. Prepared references are the
// D3 handoff refusal, tested verbatim (objects_test.go).
func (r *planRenderer) compositeExtractJSON(rows map[string]rowScope, expression ir.PhysicalExpression) (string, error) {
	extract := expression.Extract
	if extract == nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand has no payload", ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality && expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: composite EXTRACT operand is registered for SCALAR and ARRAY cardinality only", ErrUnsupportedPostgresPlan)
	}
	switch expression.NullBehavior {
	case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
	default:
		return "", fmt.Errorf("%w: composite EXTRACT operand: null behavior %q has no registered PostgreSQL extraction form",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return "", fmt.Errorf("%w: composite EXTRACT operand: CONDITIONAL_ARRAY selector mode requires repeated-step rendering, which is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return "", fmt.Errorf("%w: composite EXTRACT operand: unknown selector execution mode %q", ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Prepared != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if extract.Distinct {
		return "", fmt.Errorf("%w: composite EXTRACT operand: DISTINCT projection is meaningful only for array-valued selectors registered with their own reduction",
			ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return "", fmt.Errorf("%w: composite EXTRACT operand: ordered fallback selectors are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: unit-normalized extraction is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return "", fmt.Errorf("%w: composite EXTRACT operand: selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Selector.Steps) == 0 {
		return "", fmt.Errorf("%w: composite EXTRACT operand: the selector is empty", ErrUnsupportedPostgresPlan)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		switch {
		case step.Field == "":
			return "", fmt.Errorf("%w: composite EXTRACT operand: selector step %d has no property", ErrUnsupportedPostgresPlan, index)
		case step.Iterate:
			return "", fmt.Errorf("%w: composite EXTRACT operand: repeated ([]) selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, index)
		case step.Index != nil:
			return "", fmt.Errorf("%w: composite EXTRACT operand: indexed selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return "", fmt.Errorf("%w: composite EXTRACT operand: selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
		path = append(path, step.Field)
	}
	if extract.Source.BindKey != "" {
		return "", fmt.Errorf("%w: composite EXTRACT operand: the selector source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return "", fmt.Errorf(`%w: composite EXTRACT operand: the selector source must be the row document at ["payload"], the exact source shape lowering emits; got %v`,
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	scope, ok := rows[extract.Source.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: composite EXTRACT operand reads variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
		return "", fmt.Errorf("%w: composite EXTRACT operand validates selector against resource type %q, but the row is scanned as %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	return r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
}

// renderObjectValue renders one ir.PhysicalObject as one jsonb expression
// (L1: key order is consumer-irrelevant by registration). Field order is
// the declaration order; jsonb canonicalizes it. With no OMIT_NULLS field
// the object is one jsonb_build_object call; otherwise every field is one
// single-pair CASE merged with ||, the per-field reading of the AQL
// MERGE-over-a-literal-fields loop (collections.go:59-67): an omit field
// contributes no pair when its value is SQL NULL or the jsonb 'null'
// scalar, a non-omit field always contributes its pair.
func (r *planRenderer) renderObjectValue(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	object := expression.Object
	if object == nil {
		return "", fmt.Errorf("%w: OBJECT expression is missing payload", ErrUnsupportedPostgresPlan)
	}
	if r.membership != nil {
		return "", fmt.Errorf("%w: OBJECT projection is registered on the plain spine only; membership-root routes keep the population.go contract and refuse composite projections", ErrUnsupportedPostgresPlan)
	}
	if depth > maxCompositeDepth {
		return "", fmt.Errorf("%w: OBJECT nesting exceeds the registered maximum depth %d (defensive closed-surface bound; production lowering derives at most depth 1 — SLICE projections are EXTRACT-only and the document envelope's fields are VALUE-only — and AQL's renderExpression recursion is unbounded, cited per ruling)",
			ErrUnsupportedPostgresPlan, maxCompositeDepth)
	}
	switch expression.Cardinality {
	case ir.PhysicalObjectCardinality:
	default:
		return "", fmt.Errorf("%w: OBJECT expression must carry OBJECT cardinality, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	switch expression.NullBehavior {
	case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
		// AQL renderObject (collections.go:14-68) never consults the
		// expression's own null behavior; the PostgreSQL rendering reads
		// per-field behavior only. The three registered values therefore
		// carry no whole-object effect, exactly as in AQL.
	default:
		return "", fmt.Errorf("%w: OBJECT expression null behavior %q is not a registered form", ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	if len(object.Fields) == 0 {
		return "", fmt.Errorf("%w: OBJECT expression requires at least one field (ir/physical_validation_shapes.go:39-54)", ErrUnsupportedPostgresPlan)
	}
	seen := make(map[string]bool, len(object.Fields))
	parts := make([]string, 0, len(object.Fields))
	anyOmit := false
	for index, field := range object.Fields {
		if strings.TrimSpace(field.Name) == "" || seen[field.Name] {
			return "", fmt.Errorf("%w: OBJECT field %d name %q is empty or duplicated (ir/physical_validation_shapes.go:45-47)",
				ErrUnsupportedPostgresPlan, index, field.Name)
		}
		seen[field.Name] = true
		value, err := r.objectFieldJSON(plan, rows, field.Expression, depth)
		if err != nil {
			return "", fmt.Errorf("object field %q: %w", field.Name, err)
		}
		nameIndex := r.textParameter(field.Name, "text:"+field.Name)
		pair := "jsonb_build_object($" + strconv.Itoa(nameIndex) + "::text, " + value + ")"
		if field.Expression.NullBehavior == ir.PhysicalOmitNulls {
			anyOmit = true
			pair = "CASE WHEN (" + value + ") IS NULL OR (" + value + ") = 'null'::jsonb THEN '{}'::jsonb ELSE " + pair + " END"
		}
		parts = append(parts, pair)
	}
	if !anyOmit {
		return strings.Join(parts, " || "), nil
	}
	return "(" + strings.Join(parts, " || ") + ")", nil
}

// renderSliceValue renders one ir.PhysicalSlice over one of the two
// registered item sources (L3): the single root row, or a child set
// validated by sets.go. The inner select carries the AQL FOR-loop
// clauses — FILTER, SORT value ASC NULLS FIRST with the identity
// tie-break (the item _key leg, collections.go:130, mapped to logical_id
// per canonicalKeyColumn), bind-carried LIMIT — and the outer aggregate
// restores the array expression's EMPTY_ON_NULL reading with
// COALESCE(..., '[]'::jsonb).
func (r *planRenderer) renderSliceValue(plan ir.PhysicalPlan, rows map[string]rowScope, expression ir.PhysicalExpression, depth int) (string, error) {
	slice := expression.Slice
	if slice == nil {
		return "", fmt.Errorf("%w: SLICE expression is missing payload", ErrUnsupportedPostgresPlan)
	}
	if r.membership != nil {
		return "", fmt.Errorf("%w: SLICE projection is registered on the plain spine only; membership-root routes keep the population.go contract and refuse composite projections", ErrUnsupportedPostgresPlan)
	}
	if depth > maxCompositeDepth {
		return "", fmt.Errorf("%w: SLICE nesting exceeds the registered maximum depth %d (defensive closed-surface bound; production SLICE projections are EXTRACT-only, lower/root_projection.go:459-463, and AQL recursion is unbounded, cited per ruling)",
			ErrUnsupportedPostgresPlan, maxCompositeDepth)
	}
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return "", fmt.Errorf("%w: SLICE expression must carry ARRAY cardinality, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return "", fmt.Errorf("%w: SLICE expression null behavior %q is not the registered EMPTY_ON_NULL array reading (lower/root_projection.go:465)",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	if len(slice.Projections) == 0 {
		return "", fmt.Errorf("%w: SLICE requires at least one projection (ir/physical_validation_shapes.go:39-54)", ErrUnsupportedPostgresPlan)
	}

	source := slice.Source
	if source.BindKey != "" {
		return "", fmt.Errorf("%w: SLICE source must be a row variable, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(source.Path) != 0 {
		return "", fmt.Errorf("%w: SLICE source must be the row itself, the exact form lower/root_projection.go:402-408 passes (no document path); got %v",
			ErrUnsupportedPostgresPlan, source.Path)
	}
	if body, ok := r.setBodies[source.Variable]; ok {
		return r.renderSliceOverChildSet(plan, slice, body, depth)
	}
	scope, ok := rows[source.Variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: SLICE source variable %q does not bind a readable stored row", ErrUnsupportedPostgresPlan, source.Variable)
	}
	if source.Variable != r.rootVariable {
		return "", fmt.Errorf("%w: SLICE source variable %q is not a registered item source: the registered sources are the ROOT_SCAN row (singleton) and a child set whose reduction contract was validated by sets.go; traversal targets, derived values, prepared variables, and melt outputs have no registered slice contract",
			ErrUnsupportedPostgresPlan, source.Variable)
	}
	return r.assembleSliceSublink(plan, slice, map[string]rowScope{sliceItemVariable: scope}, scope, "", depth)
}

// renderSliceOverChildSet renders a SLICE over a child set validated by
// sets.go (the D2 handoff): the item relation is the byte-identical
// SELECT-DISTINCT (logical_id, resource_type, payload) dedup the
// FIRST/ALL/DISTINCT reductions read (sets.go setReductionSublink's items
// text), so one child occurrence join semantics serves both surfaces. The
// tie-break parity leg: AQL's SORT v ASC, item._key ASC
// (render/aql/collections.go:130) becomes ORDER BY <sort> ASC NULLS
// FIRST, logical_id ASC — total because the registered single-hop join
// fixes the child resource type and logical_id is unique across items of
// one set (sets.go:407-411). Prepared-bearing plans never reach here:
// their extracts carry Extract.Prepared and refuse verbatim in
// compositeExtractJSON (the D3 ruling).
func (r *planRenderer) renderSliceOverChildSet(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, body correlatedSetBody, depth int) (string, error) {
	r.sliceSeq++
	itemAlias := "slx" + strconv.Itoa(r.sliceSeq)
	logicalIDColumn := r.storage.Resource.MustColumn("logical_id").SQLText()
	resourceTypeColumn := r.storage.Resource.MustColumn("resource_type").SQLText()
	payloadColumn := r.storage.Resource.MustColumn("payload").SQLText()
	items := "(SELECT DISTINCT " + body.targetAlias + "." + logicalIDColumn + " AS " + logicalIDColumn +
		", " + body.targetAlias + "." + resourceTypeColumn + " AS " + resourceTypeColumn +
		", " + body.targetAlias + "." + payloadColumn + " AS " + payloadColumn +
		" " + body.from + " WHERE " + body.where + ")"
	itemScope := rowScope{alias: itemAlias, resourceType: body.targetType}
	return r.assembleSliceSublink(plan, slice,
		map[string]rowScope{sliceItemVariable: itemScope}, itemScope,
		" FROM "+items+" AS "+itemAlias, depth)
}

// assembleSliceSublink assembles the shared bounded sublink for both
// registered item sources: the inner select (item object + identity key,
// optional predicate, the total-order SORT with the logical_id tie-break,
// bind-carried LIMIT) and the outer COALESCE'd jsonb_agg restoring the
// EMPTY_ON_NULL array reading. fromClause is empty for the FROM-less
// singleton (the implicit one-row select) and the deduplicated items
// relation for the child set.
func (r *planRenderer) assembleSliceSublink(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope, itemScope rowScope, fromClause string, depth int) (string, error) {
	itemObject, err := r.sliceItemObject(plan, slice, itemRows, depth)
	if err != nil {
		return "", err
	}
	sortExpr, err := r.renderSliceSort(slice, itemRows, itemScope)
	if err != nil {
		return "", err
	}
	// The sort value is projected so the OUTER jsonb_agg can apply the
	// same total order the inner LIMIT cut used: aggregate ORDER BY cannot
	// reference inner-scope expressions, and jsonb_agg ignores input row
	// order without its own ORDER BY.
	innerSelect := "SELECT " + itemObject + " AS \"item\", " +
		sortExpr + " AS \"item_sort\", " +
		itemScope.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText() + " AS \"item_key\"" + fromClause
	if slice.Predicate != nil {
		predicate, err := r.renderSlicePredicate(plan, slice, itemRows)
		if err != nil {
			return "", err
		}
		innerSelect += " WHERE " + predicate
	}
	limitValue, ok := plan.BindVars[slice.LimitBindKey].(int)
	if !ok || limitValue <= 0 {
		return "", fmt.Errorf("%w: slice limit bind %q must be a positive int (ir/physical_validation_shapes.go:28-31)",
			ErrUnsupportedPostgresPlan, slice.LimitBindKey)
	}
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limitValue)}, "")
	innerSelect += " ORDER BY " + sortExpr + " ASC NULLS FIRST, item_key ASC LIMIT $" + strconv.Itoa(limitIndex)

	r.sliceSeq++
	sliceAlias := "sl" + strconv.Itoa(r.sliceSeq)
	return "(SELECT COALESCE(jsonb_agg(" + sliceAlias + ".\"item\" ORDER BY " + sliceAlias + ".\"item_sort\" ASC NULLS FIRST, " +
		sliceAlias + ".\"item_key\"), '[]'::jsonb) FROM (" + innerSelect + ") " + sliceAlias + ")", nil
}

// sliceItemObject renders the slice's per-item projection object: the
// RETURN of the AQL loop (collections.go:132-151), whose names are bound
// parameters and whose values render through the recursive operand
// surface over the item scope. Projection names must be non-empty and
// distinct (the ir contract, re-read here as everywhere else).
func (r *planRenderer) sliceItemObject(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope, depth int) (string, error) {
	seen := make(map[string]bool, len(slice.Projections))
	args := make([]string, 0, 2*len(slice.Projections))
	for index, projection := range slice.Projections {
		if strings.TrimSpace(projection.Name) == "" || seen[projection.Name] {
			return "", fmt.Errorf("%w: slice projection %d name %q is empty or duplicated (ir/physical_validation_shapes.go:45-47)",
				ErrUnsupportedPostgresPlan, index, projection.Name)
		}
		seen[projection.Name] = true
		value, err := r.objectFieldJSON(plan, itemRows,
			rebindSliceOperand(projection.Expression, slice.Source.Variable), depth)
		if err != nil {
			return "", fmt.Errorf("slice projection %d (%s): %w", index, projection.Name, err)
		}
		nameIndex := r.textParameter(projection.Name, "text:"+projection.Name)
		args = append(args, "$"+strconv.Itoa(nameIndex)+"::text", value)
	}
	return "jsonb_build_object(" + strings.Join(args, ", ") + ")", nil
}

// renderSlicePredicate renders the closed slice predicate: exactly one
// COMPARISON (the AQL contract, collections.go:93-95) in the
// LeftExpression selector form both slice producers emit
// (lower/typed_predicate.go:30-44 and lower/root_projection.go:417-430),
// after the AQL-parity rebind of the extraction source onto the item
// scope (collections.go:98-106).
func (r *planRenderer) renderSlicePredicate(plan ir.PhysicalPlan, slice *ir.PhysicalSlice, itemRows map[string]rowScope) (string, error) {
	predicate := *slice.Predicate
	if predicate.Kind != ir.PhysicalComparisonPredicate || predicate.Comparison == nil {
		return "", fmt.Errorf("%w: slice predicate must be a comparison (the AQL contract, render/aql/collections.go:93-95)", ErrUnsupportedPostgresPlan)
	}
	comparison := *predicate.Comparison
	if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
		return "", fmt.Errorf("%w: slice predicate must carry the selector-extraction left side both slice producers emit (lower/typed_predicate.go:30-44, lower/root_projection.go:417-430); a direct column comparison is not a slice lowering output",
			ErrUnsupportedPostgresPlan)
	}
	if comparison.Left.Variable != "" || comparison.Left.BindKey != "" {
		return "", fmt.Errorf("%w: slice predicate must not also carry a direct left value", ErrUnsupportedPostgresPlan)
	}
	left := *comparison.LeftExpression
	extract := *left.Extract
	if extract.Source.Variable != slice.Source.Variable {
		return "", fmt.Errorf("%w: slice predicate extraction must read the slice source row (rebind target), got variable %q",
			ErrUnsupportedPostgresPlan, extract.Source.Variable)
	}
	extract.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: []string{"payload"}}
	left.Extract = &extract
	comparison.LeftExpression = &left
	fragment, err := r.renderPredicateFragmentScoped(plan, ir.PhysicalPredicateExpression{
		Kind:       ir.PhysicalComparisonPredicate,
		Comparison: &comparison,
	}, itemRows, true)
	if err != nil {
		return "", fmt.Errorf("slice predicate: %w", err)
	}
	// A three-valued predicate row is dropped by WHERE exactly as the AQL
	// FILTER keeps only true, so fragment.mayYieldNull needs no NOT-style
	// guard here (the registered negation contract lives in
	// renderPredicateFragmentScoped itself).
	return fragment.sql, nil
}

// renderSliceSort renders the closed sort key: the lowering's stable
// identity form (lower/root_projection.go:406, VALUE over the source
// variable at ["_key"], mapped onto logical_id per canonicalKeyColumn) or
// one closed scalar EXTRACT operand (L2's homogeneous-value leg). Any
// other sort kind is a typed refusal.
func (r *planRenderer) renderSliceSort(slice *ir.PhysicalSlice, itemRows map[string]rowScope, scope rowScope) (string, error) {
	if slice.Sort == nil {
		return "", fmt.Errorf("%w: slice requires sort expression (ir/physical_validation_shapes.go:19-24)", ErrUnsupportedPostgresPlan)
	}
	sortExpression := *slice.Sort
	if sortExpression.Kind == ir.PhysicalValueExpression && sortExpression.Value != nil {
		value := *sortExpression.Value
		if value.Variable != slice.Source.Variable || value.BindKey != "" || len(value.Path) != 1 ||
			(value.Path[0] != "_key" && value.Path[0] != "id") {
			return "", fmt.Errorf("%w: slice VALUE sort must be the lowering identity key over the slice source variable (lower/root_projection.go:406: VALUE %v); got variable %q path %v",
				ErrUnsupportedPostgresPlan, []string{"_key"}, value.Variable, value.Path)
		}
		return scope.alias + "." + r.storage.Resource.MustColumn(canonicalKeyColumn(value.Path[0])).SQLText(), nil
	}
	if sortExpression.Kind == ir.PhysicalExtractExpression {
		if sortExpression.Extract == nil {
			return "", fmt.Errorf("%w: slice EXTRACT sort has no extraction payload", ErrUnsupportedPostgresPlan)
		}
		if sortExpression.Cardinality != ir.PhysicalScalarCardinality {
			return "", fmt.Errorf("%w: slice EXTRACT sort must be scalar-cardinality; array sort values are a separate capability", ErrUnsupportedPostgresPlan)
		}
		extract := *sortExpression.Extract
		if extract.Source.Variable != slice.Source.Variable {
			return "", fmt.Errorf("%w: slice sort extraction must read the slice source row, got variable %q",
				ErrUnsupportedPostgresPlan, extract.Source.Variable)
		}
		// Item-scope rebind on a copy (the AQL sort rebind,
		// collections.go:119-125, adapted to the payload-anchored
		// extractor): the plan's own payload is never mutated.
		extract.Source = ir.PhysicalValue{Variable: sliceItemVariable, Path: []string{"payload"}}
		sortCopy := sortExpression
		sortCopy.Extract = &extract
		return r.compositeExtractJSON(itemRows, sortCopy)
	}
	return "", fmt.Errorf("%w: slice sort kind %q is outside the registered forms (VALUE identity key or scalar EXTRACT)",
		ErrUnsupportedPostgresPlan, string(sortExpression.Kind))
}
