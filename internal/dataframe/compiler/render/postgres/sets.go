package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type correlatedSetBody struct {
	from        string
	where       string
	targetAlias string
	targetType  string
	targetVar   string
}

func (r *planRenderer) renderCorrelatedChildSteps(
	plan ir.PhysicalPlan,
	subplan ir.PhysicalSubplan,
	allowOptionalCapture bool,
	multipleTraversals bool,
	allowEndpointStrategy bool,
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
			if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
				if !allowEndpointStrategy {
					return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: endpoint-lookup strategy has no proven PostgreSQL contract for this consumer; the registered child-set surface accepts it only for sets reduced by a registered SUM/COUNT/MIN/MAX aggregate expression",
						ErrUnsupportedPostgresPlan, index)
				}
				if strings.TrimSpace(traversal.EndpointField) == "" || strings.TrimSpace(traversal.EndpointJoinField) == "" {
					return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: endpoint-lookup traversal payload is incomplete (the planner always names EndpointField and EndpointJoinField, lower/traversal.go:143-147)",
						ErrUnsupportedPostgresPlan, index)
				}
				expectedField, expectedJoinField := "_from", "_to"
				switch traversal.Direction {
				case ir.PhysicalInbound:
					expectedField, expectedJoinField = "_to", "_from"
				case ir.PhysicalOutbound:
				default:
					return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: endpoint-lookup direction %q carries no direction-derived join contract to check the named fields against",
						ErrUnsupportedPostgresPlan, index, string(traversal.Direction))
				}
				if traversal.EndpointField != expectedField || traversal.EndpointJoinField != expectedJoinField {
					return correlatedSetBody{}, fmt.Errorf("%w: correlated subplan operation %d: endpoint-lookup names (%s, %s) but direction %q derives (%s, %s) (lower/storage_route.go:30-39 always agrees; a divergence must fail closed)",
						ErrUnsupportedPostgresPlan, index, traversal.EndpointField, traversal.EndpointJoinField,
						string(traversal.Direction), expectedField, expectedJoinField)
				}
				traversal.Strategy = ir.PhysicalTraversalNative
			}
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
			edgeWhere, targetOn := r.edgeOccurrencePredicates(&verified, edgeAlias, targetAlias, nextSource.alias)
			if !sawTraversal {
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
	if set.Reduction == nil && !sliceConsumesSetVariable(plan, set.Variable) && !aggregateConsumesSetVariable(plan, set.Variable) {
		return fmt.Errorf("%w: set %q has no reduction contract; the registered surface renders sets only through their FIRST/ALL/DISTINCT selector reductions, SLICE consumers, and SUM/COUNT/MIN/MAX aggregate reducers over the registered projection slots, because no other consumer of the item array is registered",
			ErrUnsupportedPostgresPlan, set.Variable)
	}
	if set.Reduction != nil && set.Projection == nil {
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

	corr, err := r.renderCorrelatedChildSteps(plan, set.Subplan, true, false, aggregateConsumesSetVariable(plan, set.Variable))
	if err != nil {
		return fmt.Errorf("set %q: %w", set.Variable, err)
	}

	ret := set.Subplan.Return
	if ret.Kind != ir.PhysicalValueExpression || ret.Cardinality != ir.PhysicalObjectCardinality ||
		ret.NullBehavior != ir.PhysicalPreserveNull || ret.Value == nil ||
		ret.Value.BindKey != "" || ret.Value.Variable != corr.targetVar || len(ret.Value.Path) != 0 {
		return fmt.Errorf("%w: set %q subplan return must be the canonical whole-document VALUE expression over the reached target row (lower/child_sets.go buildOptionalChildPhysicalSet)",
			ErrUnsupportedPostgresPlan, set.Variable)
	}

	if r.setBodies == nil {
		r.setBodies = make(map[string]correlatedSetBody)
	}
	r.setBodies[set.Variable] = corr
	if set.Projection != nil && len(set.Projection.Fields) > 0 {
		if r.setProjections == nil {
			r.setProjections = make(map[string][]ir.PhysicalSetProjectionField)
		}
		r.setProjections[set.Variable] = append([]ir.PhysicalSetProjectionField(nil), set.Projection.Fields...)
	}
	if set.Reduction == nil {
		return nil
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
		objectArgs = append(objectArgs, "$"+strconv.Itoa(keyIndex)+"::text, "+value)
	}
	object := "jsonb_build_object(" + strings.Join(objectArgs, ", ") + ")"
	return r.registerDerivedValue(set.Reduction.Variable, object, true)
}

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

func (r *planRenderer) renderRelatedExists(plan ir.PhysicalPlan, subplan ir.PhysicalSubplan) (string, error) {
	if subplan.Sort != nil || subplan.Unique || subplan.DistinctBy != nil {
		return "", fmt.Errorf("%w: related-existence subplan cannot carry projection-only sort or unique modifiers (render/aql/predicates.go:315-317 refuses them for bounded EXISTS too; count-style eligibility is a separate capability)",
			ErrUnsupportedPostgresPlan)
	}
	corr, err := r.renderCorrelatedChildSteps(plan, subplan, false, true, false)
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
