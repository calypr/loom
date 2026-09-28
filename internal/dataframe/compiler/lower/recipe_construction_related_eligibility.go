package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func lowerConstructionRelatedEligibility(
	plan *ir.PhysicalPlan,
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedEligibility,
	input map[string]CompiledOutputColumn,
	outputs map[string]recipe.StageColumn,
	inputRow, inputIdentity, rootResourceType string,
	policy ir.PhysicalOptimizationPolicy,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalFilter, []ir.PhysicalOperation, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID == "" || inputIdentity == "" || len(related.Route) == 0 {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility requires a retained exact row resource anchor")
	}
	anchor, ok := input[related.AnchorColumnID]
	if !ok || !anchor.Internal || anchor.Kind != string(expression.KindString) ||
		(anchor.Cardinality != string(expression.RequiredOne) && anchor.Cardinality != string(expression.OptionalOne)) {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility anchor is not a compiler-proven exact resource identity")
	}
	anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, rootResourceType
	if related.AnchorColumnID == "_key" {
		if anchor.Name != "_key" || anchor.Cardinality != string(expression.RequiredOne) || rootResourceType != plan.Source.ResourceType {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility root anchor is not the compiler-proven root document key")
		}
	} else {
		if anchor.RelatedRecordAnchor == nil || anchor.RelatedRecordAnchor.TargetNodeID == "" || anchor.RelatedRecordAnchor.TargetResourceType == "" {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility anchor is not an active exact related-record identity")
		}
		anchorKind = "activeRelatedRecord"
		anchorNodeID = anchor.RelatedRecordAnchor.TargetNodeID
		anchorResourceType = anchor.RelatedRecordAnchor.TargetResourceType
	}
	if anchorNodeID != related.Route[0].FromNodeID || anchorResourceType != related.Route[0].FromResourceType {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route does not start at its selected row resource anchor")
	}
	identity, ok := input[inputIdentity]
	if !ok || !identity.Internal || !identity.Identity || identity.Name != inputIdentity || identity.Kind != string(expression.KindString) ||
		identity.Cardinality != string(expression.RequiredOne) {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility input has no required scalar row identity")
	}
	if related.ContributorPolicy != "ALL_MATCHES" {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility requires ALL_MATCHES contributor policy")
	}
	if related.ContributorPredicate != nil && related.ContributorSource == nil {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility predicate requires an exact contributor field")
	}

	currentNodeID, currentResource := anchorNodeID, anchorResourceType
	for routeIndex, hop := range related.Route {
		if hop.FromNodeID != currentNodeID || hop.FromResourceType != currentResource || hop.EdgeID == "" || hop.ToNodeID == "" ||
			hop.ToResourceType == "" || hop.Relationship == "" || (hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route hop %d is incomplete or discontinuous", routeIndex)
		}
		currentNodeID, currentResource = hop.ToNodeID, hop.ToResourceType
	}
	if currentNodeID != related.TargetNodeID || currentResource != related.TargetResourceType {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route does not end at its exact target")
	}

	anchorVariable := allocateConstructionVariable(usedVariables, fmt.Sprintf("related_eligibility_%d_anchor", stepIndex), stepIndex)
	anchorNode := semantic.SemanticNode{Alias: anchorNodeID, ResourceType: anchorResourceType}
	subplan := ir.PhysicalSubplan{Captures: []string{inputRow}}
	if anchorKind == "root" {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalCollectionScanOp, Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType},
			CollectionScan: &ir.PhysicalCollectionScan{Variable: anchorVariable, CollectionBindKey: "root_collection"},
		})
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp, Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_key"},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
			}},
		})
	} else {
		collectionBind := nextTableReshapeBindKey(plan.BindVars, "related_eligibility_anchor_collection")
		plan.BindVars[collectionBind] = anchorResourceType
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalDocumentLookupOp, Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_id"},
			DocumentLookup: &ir.PhysicalDocumentLookup{
				Variable: anchorVariable, CollectionBindKey: collectionBind,
				ExactID: ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
			},
		})
	}
	subplan.Operations = appendProjectScope(subplan.Operations, []string{anchorVariable}, "", anchorNode)
	subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{anchorVariable}, "", anchorNode)
	subplan.Operations = appendAuthScope(subplan.Operations,
		[]ir.PhysicalValue{{Variable: anchorVariable, Path: []string{"auth_resource_path"}}},
		fmt.Sprintf("related_eligibility_%d_anchor_scope_allowed", stepIndex), anchorNode)

	currentVariable := anchorVariable
	for routeIndex, hop := range related.Route {
		prefix := fmt.Sprintf("related_eligibility_%d_hop_%d", stepIndex, routeIndex+1)
		targetVariable := allocateConstructionVariable(usedVariables, prefix+"_target", stepIndex)
		edgeVariable := allocateConstructionVariable(usedVariables, prefix+"_edge", stepIndex)
		traversal, err := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: hop.FromResourceType, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: policy,
		})
		if err != nil {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route hop %d: %w", routeIndex, err)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route hop %d storage direction changed", routeIndex)
		}
		for key, value := range traversal.BindVars {
			plan.BindVars[key] = value
		}
		child := semantic.SemanticNode{Alias: hop.ToNodeID, ResourceType: hop.ToResourceType, EdgeLabel: hop.Relationship}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:      ir.PhysicalTraversalOp,
			Source:    ir.PhysicalSource{SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: hop.Relationship},
			Traversal: &traversal.Traversal,
		})
		scoped := []string{edgeVariable, targetVariable}
		subplan.Operations = appendProjectScope(subplan.Operations, scoped, hop.Relationship, child)
		subplan.Operations = appendDatasetGenerationScope(subplan.Operations, scoped, hop.Relationship, child)
		subplan.Operations = appendAuthScope(subplan.Operations,
			[]ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}},
			prefix+"_scope_allowed", child)
		currentVariable = targetVariable
	}

	if source := related.ContributorSource; source != nil {
		if source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
			(source.Cardinality != "optional_one" && source.Cardinality != "required_one") {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility contributor field is not an exact target scalar")
		}
		selectorPath := strings.TrimPrefix(source.Path, source.ResourceType+".")
		selector, err := spec.ParseSelector(selectorPath)
		if err != nil {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility contributor path: %w", err)
		}
		field := ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: currentVariable, Path: []string{"payload"}}, ResourceType: source.ResourceType,
				Selector: selector, ExecutionMode: selectorExecutionMode(source.ResourceType, selector)},
		}
		if predicate := related.ContributorPredicate; predicate != nil {
			if predicate.CandidateID != source.CandidateID {
				return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility predicate differs from its selected contributor field")
			}
			comparison := ir.PhysicalPredicate{Operator: string(predicate.Operator), LeftExpression: &field}
			if predicate.Operator == recipe.FilterEquals {
				if predicate.Value == nil {
					return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility EQUALS predicate requires a value")
				}
				literal, err := constructionFilterLiteral(*predicate.Value)
				if err != nil {
					return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility predicate value: %w", err)
				}
				bindKey := nextTableReshapeBindKey(plan.BindVars, "related_eligibility_contributor_value")
				plan.BindVars[bindKey] = literal
				comparison.Right = &ir.PhysicalValue{BindKey: bindKey}
				comparison.ValueKind = spec.FilterValueKind(predicate.Value.Kind)
			}
			subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
				Kind:   ir.PhysicalFilterOp,
				Source: ir.PhysicalSource{SemanticNode: source.NodeID, ResourceType: source.ResourceType, SemanticField: source.CandidateID},
				Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &comparison}},
			})
		}
	}

	terminalID := ir.PhysicalValue{Variable: currentVariable, Path: []string{"_id"}}
	derivedLets := []ir.PhysicalOperation(nil)
	var filter ir.PhysicalFilter
	switch related.MatchKind {
	case recipe.RelatedEligibilityExists:
		subplan.Return = physicalValueExpression(terminalID)
		filter.Expression = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	case recipe.RelatedEligibilityAbsent:
		subplan.Return = physicalValueExpression(terminalID)
		filter.Expression = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}}}
	case recipe.RelatedEligibilityCountAtLeast:
		if related.Threshold == nil || *related.Threshold <= 0 {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility COUNT_AT_LEAST requires a positive threshold")
		}
		subplan.Sort, subplan.Unique = &terminalID, true
		subplan.Return = physicalValueExpression(terminalID)
		countVariable := allocateConstructionVariable(usedVariables, fmt.Sprintf("related_eligibility_%d_distinct_count", stepIndex), stepIndex)
		thresholdBind := nextTableReshapeBindKey(plan.BindVars, "related_eligibility_threshold")
		plan.BindVars[thresholdBind] = *related.Threshold
		array := ir.PhysicalExpression{Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Subplan: &subplan}
		count := ir.PhysicalExpression{
			Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Call: &ir.PhysicalCall{Name: "length", Args: []ir.PhysicalExpression{array}},
		}
		derivedLets = append(derivedLets, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp,
			ExpressionLet: &ir.PhysicalExpressionLet{Variable: countVariable, Expression: count}})
		filter.Expression = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
			Operator: "GTE", Left: ir.PhysicalValue{Variable: countVariable}, Right: &ir.PhysicalValue{BindKey: thresholdBind},
		}}
	default:
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility match kind %q is unsupported", related.MatchKind)
	}

	compiled := make([]CompiledOutputColumn, 0, len(step.Outputs))
	for _, declaration := range step.Outputs {
		prior, exists := input[declaration.ID]
		if !exists || prior.Internal {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility output ID %q is not a public input column", declaration.ID)
		}
		prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
		compiled = append(compiled, prior)
	}
	projections := stagePassThroughProjections(step.Outputs, input, inputRow)
	return filter, derivedLets, projections, compiled, nil
}
