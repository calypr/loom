package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func lowerConstructionRelatedExpand(
	plan *ir.PhysicalPlan,
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedExpand,
	input map[string]CompiledOutputColumn,
	outputs map[string]recipe.StageColumn,
	inputRow, inputIdentity, rootResourceType string,
	policy ir.PhysicalOptimizationPolicy,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalStageRelatedExpand, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID == "" || inputIdentity == "" {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion requires a retained row resource anchor")
	}
	if len(related.Route) == 0 {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route must contain at least one hop")
	}
	anchor, ok := input[related.AnchorColumnID]
	if !ok || !anchor.Internal || anchor.Kind != string(expression.KindString) ||
		(anchor.Cardinality != string(expression.RequiredOne) && anchor.Cardinality != string(expression.OptionalOne)) {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion anchor is not a compiler-proven exact resource identity")
	}
	anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, rootResourceType
	if related.AnchorColumnID == "_key" {
		if anchor.Name != "_key" || anchor.Cardinality != string(expression.RequiredOne) || rootResourceType != plan.Source.ResourceType {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion root anchor is not the compiler-proven root document key")
		}
	} else {
		if anchor.RelatedRecordAnchor == nil || anchor.RelatedRecordAnchor.TargetNodeID == "" || anchor.RelatedRecordAnchor.TargetResourceType == "" {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion anchor is not an active exact related-record identity")
		}
		anchorKind = "activeRelatedRecord"
		anchorNodeID = anchor.RelatedRecordAnchor.TargetNodeID
		anchorResourceType = anchor.RelatedRecordAnchor.TargetResourceType
	}
	if anchorNodeID != related.Route[0].FromNodeID || anchorResourceType != related.Route[0].FromResourceType {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route does not start at its selected row resource anchor")
	}
	identity, ok := input[inputIdentity]
	if !ok || !identity.Internal || !identity.Identity || identity.Name != inputIdentity || identity.Kind != string(expression.KindString) ||
		identity.Cardinality != string(expression.RequiredOne) {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion input has no required scalar row identity")
	}
	if related.ContributorPolicy != "ALL_MATCHES" {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion requires ALL_MATCHES contributor policy")
	}
	if related.ContributorPredicate != nil && related.ContributorSource == nil {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion predicate requires an exact contributor field")
	}

	currentNodeID, currentResource := anchorNodeID, anchorResourceType
	for index, hop := range related.Route {
		if hop.FromNodeID != currentNodeID || hop.FromResourceType != currentResource ||
			hop.EdgeID == "" || hop.ToNodeID == "" || hop.ToResourceType == "" || hop.Relationship == "" ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route hop %d is incomplete or discontinuous", index)
		}
		currentNodeID, currentResource = hop.ToNodeID, hop.ToResourceType
	}
	if currentNodeID != related.TargetNodeID || currentResource != related.TargetResourceType {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route does not end at its exact target")
	}

	anchorVariable := allocateConstructionVariable(usedVariables, fmt.Sprintf("related_expand_%d_anchor", stepIndex), stepIndex)
	anchorNode := semantic.SemanticNode{Alias: anchorNodeID, ResourceType: anchorResourceType}
	subplan := ir.PhysicalSubplan{Captures: []string{inputRow}}
	if anchorKind == "root" {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:           ir.PhysicalCollectionScanOp,
			Source:         ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType},
			CollectionScan: &ir.PhysicalCollectionScan{Variable: anchorVariable, CollectionBindKey: "root_collection"},
		})
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_key"},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
			}},
		})
	} else {
		collectionBind := nextTableReshapeBindKey(plan.BindVars, "related_expand_anchor_collection")
		plan.BindVars[collectionBind] = anchorResourceType
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalDocumentLookupOp,
			Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_id"},
			DocumentLookup: &ir.PhysicalDocumentLookup{
				Variable: anchorVariable, CollectionBindKey: collectionBind,
				ExactID: ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
			},
		})
	}
	subplan.Operations = appendProjectScope(subplan.Operations, []string{anchorVariable}, "", anchorNode)
	subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{anchorVariable}, "", anchorNode)
	subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{{Variable: anchorVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("related_expand_%d_anchor_scope_allowed", stepIndex), anchorNode)

	currentVariable := anchorVariable
	for routeIndex, hop := range related.Route {
		prefix := fmt.Sprintf("related_expand_%d_hop_%d", stepIndex, routeIndex+1)
		targetVariable := allocateConstructionVariable(usedVariables, prefix+"_target", stepIndex)
		edgeVariable := allocateConstructionVariable(usedVariables, prefix+"_edge", stepIndex)
		traversal, err := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: hop.FromResourceType, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: policy,
		})
		if err != nil {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route hop %d: %w", routeIndex, err)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route hop %d storage direction changed", routeIndex)
		}
		for key, value := range traversal.BindVars {
			plan.BindVars[key] = value
		}
		child := semantic.SemanticNode{Alias: hop.ToNodeID, ResourceType: hop.ToResourceType, EdgeLabel: hop.Relationship}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalTraversalOp, Source: ir.PhysicalSource{
				SemanticNode: child.Alias, ResourceType: child.ResourceType, Relationship: hop.Relationship,
			}, Traversal: &traversal.Traversal,
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
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion contributor field is not an exact target scalar")
		}
		selectorPath := strings.TrimPrefix(source.Path, source.ResourceType+".")
		selector, err := spec.ParseSelector(selectorPath)
		if err != nil {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion contributor path: %w", err)
		}
		field := ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: currentVariable, Path: []string{"payload"}},
				ResourceType: source.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(source.ResourceType, selector)},
		}
		if predicate := related.ContributorPredicate; predicate != nil {
			if predicate.CandidateID != source.CandidateID {
				return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion predicate differs from its selected contributor field")
			}
			comparison := ir.PhysicalPredicate{Operator: string(predicate.Operator), LeftExpression: &field}
			if predicate.Operator == recipe.FilterEquals {
				if predicate.Value == nil {
					return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion EQUALS predicate requires a value")
				}
				literal, err := constructionFilterLiteral(*predicate.Value)
				if err != nil {
					return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion predicate value: %w", err)
				}
				bindKey := nextTableReshapeBindKey(plan.BindVars, "related_expand_contributor_value")
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
	resourceID := ir.PhysicalValue{Variable: currentVariable, Path: []string{"id"}}
	subplan.Sort, subplan.Unique = &terminalID, true
	subplan.Return = ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "terminal_id", Expression: physicalValueExpression(terminalID)},
			{Name: "resource_id", Expression: physicalValueExpression(resourceID)},
		}},
	}

	constructionBind := nextTableReshapeBindKey(plan.BindVars, "construction_related_expand_id")
	plan.BindVars[constructionBind] = step.ID
	parentColumnID, terminalColumnID := relatedExpandIdentityColumnNames(step.ID)
	physical := ir.PhysicalStageRelatedExpand{
		AnchorColumnID: related.AnchorColumnID, AnchorKind: anchorKind, AnchorNodeID: anchorNodeID,
		AnchorResourceType: anchorResourceType, RelatedRecordColumnID: related.RelatedRecordColumnID,
		TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
		ParentIdentityColumn: inputIdentity, ParentIdentityColumnID: parentColumnID,
		TerminalIdentityColumn: terminalColumnID,
		RelatedRecordsVariable: allocateConstructionVariable(usedVariables, "related_expand_records", stepIndex),
		IndexVariable:          allocateConstructionVariable(usedVariables, "related_expand_index", stepIndex),
		ItemVariable:           allocateConstructionVariable(usedVariables, "related_expand_item", stepIndex),
		IdentityVariable:       allocateConstructionVariable(usedVariables, "related_expand_identity", stepIndex),
		ConstructionIDBindKey:  constructionBind, EmptyPolicy: ir.PhysicalUnnestEmptyPolicy(related.EmptyPolicy.Normalized()),
		RelatedRecords: subplan, Route: constructionPhysicalRelatedRoute(related.Route),
	}

	compiledByID := make(map[string]CompiledOutputColumn, len(step.Outputs)+5)
	projectionByID := make(map[string]ir.PhysicalProjection, len(step.Outputs)+5)
	for _, declaration := range step.Outputs {
		if prior, exists := input[declaration.ID]; exists {
			if prior.Internal {
				return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion cannot expose hidden input column %q", declaration.ID)
			}
			prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
			compiledByID[declaration.ID] = prior
			projectionByID[declaration.ID] = ir.PhysicalProjection{Name: declaration.Name, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{input[declaration.ID].Name}}}
			continue
		}
		if declaration.ID != related.RelatedRecordColumnID {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion output schema contains unexpected column ID %q", declaration.ID)
		}
		cardinality, nullable := string(expression.RequiredOne), false
		if related.EmptyPolicy.Normalized() == recipe.ExpansionPreserveParent {
			cardinality, nullable = string(expression.OptionalOne), true
		}
		compiledByID[declaration.ID] = CompiledOutputColumn{
			ID: declaration.ID, Name: declaration.Name, Label: constructionFirstNonEmpty(declaration.Label, declaration.Name),
			SemanticPath: "related_expand:" + related.TargetNodeID + ":id", Kind: string(expression.KindString),
			Cardinality: cardinality, Nullable: nullable,
		}
		projectionByID[declaration.ID] = ir.PhysicalProjection{Name: declaration.Name, Value: ir.PhysicalValue{Variable: physical.ItemVariable, Path: []string{"resource_id"}}}
	}

	compiled := make([]CompiledOutputColumn, 0, len(step.Outputs)+len(input)+3)
	projections := make([]ir.PhysicalProjection, 0, len(step.Outputs)+len(input)+3)
	for _, declaration := range step.Outputs {
		column, ok := compiledByID[declaration.ID]
		if !ok {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion output schema is missing column %q", declaration.ID)
		}
		compiled = append(compiled, column)
		projections = append(projections, projectionByID[declaration.ID])
	}
	inputIDs := make([]string, 0, len(input))
	for id := range input {
		inputIDs = append(inputIDs, id)
	}
	sort.Strings(inputIDs)
	for _, id := range inputIDs {
		column := input[id]
		if !column.Internal || column.Identity && column.Name != "_key" || column.RelatedRecordAnchor != nil {
			continue
		}
		kept := column
		if column.Name == "_key" {
			kept.Identity = false
		}
		compiled = append(compiled, kept)
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{column.Name}},
		})
	}
	parentColumn := CompiledOutputColumn{
		ID: parentColumnID, Name: parentColumnID, Label: parentColumnID, SemanticPath: "related_expand:parent_row_identity",
		Kind: identity.Kind, Cardinality: identity.Cardinality, Internal: true,
	}
	compiled = append(compiled, parentColumn)
	projections = append(projections, ir.PhysicalProjection{
		Name: parentColumnID, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{inputIdentity}},
	})
	terminalNullable := related.EmptyPolicy.Normalized() == recipe.ExpansionPreserveParent
	terminalCardinality := string(expression.RequiredOne)
	if terminalNullable {
		terminalCardinality = string(expression.OptionalOne)
	}
	terminalColumn := CompiledOutputColumn{
		ID: terminalColumnID, Name: terminalColumnID, Label: terminalColumnID, SemanticPath: "related_expand:terminal_document_identity",
		Kind: string(expression.KindString), Cardinality: terminalCardinality, Nullable: terminalNullable, Internal: true,
		RelatedRecordAnchor: &CompiledRelatedRecordAnchor{TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType},
	}
	compiled = append(compiled, terminalColumn)
	projections = append(projections, ir.PhysicalProjection{
		Name: terminalColumnID, Hidden: true, Value: ir.PhysicalValue{Variable: physical.ItemVariable, Path: []string{"terminal_id"}},
	})
	return physical, projections, compiled, nil
}

func relatedExpandIdentityColumnNames(stepID string) (string, string) {
	digest := sha256.Sum256([]byte(stepID))
	suffix := hex.EncodeToString(digest[:])
	return "__loom_related_parent_row_id_" + suffix, "__loom_related_terminal_id_" + suffix
}

func physicalValueExpression(value ir.PhysicalValue) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &value,
	}
}

func constructionPhysicalRelatedRoute(route []recipe.ConstructionRelatedRouteStep) []ir.PhysicalStageRelatedRouteStep {
	result := make([]ir.PhysicalStageRelatedRouteStep, 0, len(route))
	for _, hop := range route {
		result = append(result, ir.PhysicalStageRelatedRouteStep{
			EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
		})
	}
	return result
}
