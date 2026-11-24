package lower

import (
	"fmt"
	"sort"
	"strings"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	physicalpkg "github.com/calypr/loom/internal/dataframe/compiler/lower/physical"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func (lowerer *constructionLowerer) lowerConstructionGroupRelatedCodingKey(
	source recipe.ConstructionGroupFieldSource,
	context *CompiledGroupFieldSourceContext,
	output recipe.StageColumn,
	missingPolicy recipe.ConstructionGroupMissingKeyPolicy,
	stepIndex, sourceIndex int,
	stage *ir.PhysicalConstructionStage,
	adapterSlot **ir.PhysicalStageGroupRelatedOccurrenceAdapter,
) (ir.PhysicalStageGroupCodingKey, CompiledOutputColumn, ir.PhysicalProjection, CompiledOutputColumn, ir.PhysicalProjection, error) {
	fail := func(err error) (ir.PhysicalStageGroupCodingKey, CompiledOutputColumn, ir.PhysicalProjection, CompiledOutputColumn, ir.PhysicalProjection, error) {
		return ir.PhysicalStageGroupCodingKey{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, CompiledOutputColumn{}, ir.PhysicalProjection{}, err
	}
	if source.ValueKind != "CODING_IDENTITY" || output.ID == "" || output.Name == "" ||
		(output.Type != "" && output.Type != "INFER" && !strings.EqualFold(output.Type, "string")) {
		return fail(fmt.Errorf("related CODING_IDENTITY Group output must be a string column"))
	}
	bound, err := BindGroupRelatedOccurrenceSource(context, source)
	if err != nil {
		return fail(err)
	}
	if bound.Input == nil || bound.Input.RelatedOccurrence == nil {
		return fail(fmt.Errorf("related Coding source has no related occurrence binding"))
	}
	related := bound.Input.RelatedOccurrence
	if related.ResourceType != "Coding" || related.CodingSelector == "" ||
		related.OrdinalSemantics != recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition {
		return fail(fmt.Errorf("related CODING_IDENTITY source has incomplete selected Coding facts"))
	}
	selector, err := spec.ParseSelector(related.CodingSelector)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return fail(fmt.Errorf("related Coding selector %q is not canonical", related.CodingSelector))
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fail(fmt.Errorf("load generated FHIR schema: %w", err))
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(related.TargetResourceType), related.CodingSelector)
	if err != nil || facts.CanonicalPath != related.CodingSelector || facts.FHIRType != "Coding" ||
		facts.Shape != fhirschema.RowPathArray || facts.Cardinality != fhirschema.RowCardinalityMany || facts.Reference {
		return fail(fmt.Errorf("related selector %q is not a generated repeated Coding path on %s", related.CodingSelector, related.TargetResourceType))
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(related.TargetResourceType))
	if err != nil {
		return fail(fmt.Errorf("enumerate generated Coding paths: %w", err))
	}
	found := false
	for _, path := range paths {
		if path == related.CodingSelector {
			found = true
			break
		}
	}
	if !found {
		return fail(fmt.Errorf("related selector %q is not advertised as a repeated Coding path", related.CodingSelector))
	}
	adapter, err := ensureGroupRelatedOccurrenceAdapter(lowerer.plan, bound, context, stage, stepIndex, lowerer.usedVariables, adapterSlot, lowerer.policy)
	if err != nil {
		return fail(err)
	}
	if adapter.CodingSelector != "" {
		return fail(fmt.Errorf("Group supports one related Coding identity source"))
	}
	codingArrayColumn := fmt.Sprintf("__loom_group_related_codings_%d_%d", stepIndex, sourceIndex)
	membershipColumn := fmt.Sprintf("__loom_group_related_coding_membership_%d", stepIndex)
	arrayProjection := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source:       ir.PhysicalValue{Variable: adapter.RelatedRecords.RelatedRecords.Sort.Variable, Path: []string{"payload"}},
			ResourceType: related.TargetResourceType, Selector: selector,
			ExecutionMode: physicalpkg.SelectorExecutionMode(related.TargetResourceType, selector),
		},
	}
	if adapter.RelatedRecords.RelatedRecords.Return.Kind != ir.PhysicalObjectExpression || adapter.RelatedRecords.RelatedRecords.Return.Object == nil {
		return fail(fmt.Errorf("related Group route has no typed target projection"))
	}
	adapter.RelatedRecords.RelatedRecords.Return.Object.Fields = append(
		adapter.RelatedRecords.RelatedRecords.Return.Object.Fields,
		ir.PhysicalExpressionProjection{Name: codingArrayColumn, Expression: arrayProjection},
	)
	codingEmptyPolicy, ok := lowerGroupCodingEmptyPolicy(related.CodingEmptyPolicy)
	if !ok {
		return fail(fmt.Errorf("related Coding empty policy %q is unsupported", related.CodingEmptyPolicy))
	}
	ordinality := allocateConstructionVariable(lowerer.usedVariables, "group_related_coding_ordinal", stepIndex)
	codingItem := allocateConstructionVariable(lowerer.usedVariables, "group_related_coding_item", stepIndex)
	codingHasItem := allocateConstructionVariable(lowerer.usedVariables, "group_related_coding_has_item", stepIndex)
	codingUnnest := &ir.PhysicalUnnest{
		OutputVariable: codingItem, HasItemVariable: codingHasItem, Ordinality: ordinality,
		EmptyPolicy: codingEmptyPolicy,
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Value:        &ir.PhysicalValue{Variable: adapter.RelatedRecords.ItemVariable, Path: []string{codingArrayColumn}},
		},
	}
	adapter.CodingSelector, adapter.CodingArrayColumn = related.CodingSelector, codingArrayColumn
	adapter.CodingEmptyPolicy, adapter.OrdinalSemantics = codingEmptyPolicy, related.OrdinalSemantics
	adapter.CodingUnnest = codingUnnest
	adapter.CodingMembershipColumn = membershipColumn
	adapter.OccurrenceKeyColumn = groupRelatedOccurrenceKeyColumn
	valueProjection := func(variable string, path ...string) ir.PhysicalExpression {
		value := ir.PhysicalValue{Variable: variable, Path: path}
		return ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &value,
		}
	}
	membershipProjection := ir.PhysicalProjection{
		Name: membershipColumn, Hidden: true,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "system", Expression: valueProjection(codingUnnest.OutputVariable, "system")},
				{Name: "version", Expression: valueProjection(codingUnnest.OutputVariable, "version")},
				{Name: "code", Expression: valueProjection(codingUnnest.OutputVariable, "code")},
				{Name: "display", Expression: valueProjection(codingUnnest.OutputVariable, "display")},
			}},
		},
	}
	occurrenceProjection := ir.PhysicalProjection{
		Name: adapter.OccurrenceKeyColumn, Hidden: true,
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
				{Name: "root_id", Expression: valueProjection(stage.InputRowVariable, "_key")},
				{Name: "terminal_id", Expression: valueProjection(adapter.RelatedRecords.ItemVariable, "terminal_id")},
				{Name: "item_present", Expression: valueProjection(codingUnnest.HasItemVariable)},
				{Name: "ordinal", Expression: valueProjection(codingUnnest.Ordinality)},
			}},
		},
	}
	membershipInputColumn := CompiledOutputColumn{
		ID: membershipColumn, Name: membershipColumn, Label: membershipColumn,
		Kind: "object", Cardinality: string(expression.OptionalOne), Nullable: true, Internal: true,
		SemanticPath: "construction_group_related_coding_membership:" + bound.SourceShapeID,
	}
	if stage != nil {
		stage.InputColumns = append(stage.InputColumns, toPhysicalStageColumns([]CompiledOutputColumn{membershipInputColumn})...)
		stage.InputProjections = append(stage.InputProjections, membershipProjection)
		foundOccurrenceProjection := false
		for index := range stage.InputProjections {
			if stage.InputProjections[index].Name == adapter.OccurrenceKeyColumn {
				stage.InputProjections[index] = occurrenceProjection
				foundOccurrenceProjection = true
				break
			}
		}
		if !foundOccurrenceProjection {
			return fail(fmt.Errorf("related Group occurrence projection is missing from its owning stage"))
		}
	}
	key := ir.PhysicalStageGroupCodingKey{
		OutputColumn:       output.Name,
		InputTupleVariable: allocateConstructionVariable(lowerer.usedVariables, "group_coding_input_tuple", stepIndex),
		TupleVariable:      allocateConstructionVariable(lowerer.usedVariables, "group_coding_tuple", stepIndex),
		LabelVariable:      allocateConstructionVariable(lowerer.usedVariables, "group_coding_label", stepIndex),
		MembershipColumn:   membershipColumn, SourceStageID: bound.SourceStageID,
		SourceShapeID: bound.SourceShapeID, SourceOccurrenceKeyColumn: adapter.OccurrenceKeyColumn,
		OwnerOccurrenceID: related.TargetOccurrenceID, OwnerResourceType: related.TargetResourceType,
		CodingPath: related.CodingSelector,
		RelatedSource: &ir.PhysicalStageGroupRelatedOccurrenceFieldSource{
			RouteID: related.RouteID, SourceStageID: bound.SourceStageID, SourceShapeID: bound.SourceShapeID,
			AnchorOccurrenceID: related.AnchorOccurrenceID, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType: related.AnchorResourceType, Route: physicalGroupRelatedRoute(related.Route),
			TargetOccurrenceID: related.TargetOccurrenceID, TargetNodeID: related.TargetNodeID,
			TargetResourceType: related.TargetResourceType, ResourceType: related.ResourceType,
			EmptyPolicy: string(related.EmptyPolicy), Multiplicity: related.Multiplicity,
			CodingSelector: related.CodingSelector, CodingEmptyPolicy: string(related.CodingEmptyPolicy),
			OrdinalSemantics: related.OrdinalSemantics, MembershipColumn: membershipColumn,
		},
	}
	column := CompiledOutputColumn{
		ID: output.ID, Name: output.Name, Label: loomtext.FirstNonBlank(output.Label, output.Name),
		SemanticValueKind: CompiledSemanticValueCodingIdentityDisplay,
		Kind:              "string", Cardinality: string(expression.RequiredOne),
		Nullable:     missingPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup,
		SemanticPath: "construction_group_related_coding_identity:" + bound.SourceShapeID + ":" + related.CodingSelector,
	}
	projection := ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: key.LabelVariable}}
	return key, column, projection, membershipInputColumn, membershipProjection, nil
}

func (lowerer *constructionLowerer) lowerConstructionRelatedEligibility(
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedEligibility,
	input map[string]CompiledOutputColumn,
	inputRow, inputIdentity, rootResourceType string,
	stepIndex int,
) (ir.PhysicalFilter, []ir.PhysicalOperation, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID == "" || inputIdentity == "" || len(related.Route) == 0 {
		return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility requires a retained exact row resource anchor")
	}
	anchor, ok := input[related.AnchorColumnID]
	if !ok || !anchor.Internal || anchor.Kind != string(expression.KindString) {
		return ir.PhysicalFilter{}, nil, nil, nil, &RelatedEligibilityAnchorError{StepID: step.ID}
	}
	anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, rootResourceType
	if related.AnchorColumnID == "_key" {
		if anchor.Name != "_key" || anchor.Cardinality != string(expression.RequiredOne) ||
			anchor.RootContributorResourceType != rootResourceType || rootResourceType != lowerer.plan.Source.ResourceType {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility root anchor is not the compiler-proven root document key")
		}
	} else if related.AnchorColumnID == rootContributorSetColumn {
		_, found := rootContributorSetFromMap(input, rootResourceType)
		if !found || anchor.Name != rootContributorSetColumn || anchor.Cardinality != string(expression.Many) ||
			anchor.RootContributorResourceType != rootResourceType || rootResourceType == "" || rootResourceType != lowerer.plan.Source.ResourceType {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility root-contributor anchor is not the compiler-owned root identity set")
		}
		anchorKind = "rootContributors"
	} else {
		if (anchor.Cardinality != string(expression.RequiredOne) && anchor.Cardinality != string(expression.OptionalOne)) ||
			anchor.RelatedRecordAnchor == nil || anchor.RelatedRecordAnchor.TargetNodeID == "" || anchor.RelatedRecordAnchor.TargetResourceType == "" {
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
	if !ok || !identity.Internal || !identity.Identity || identity.Name != inputIdentity ||
		(identity.Kind != string(expression.KindString) && !(inputIdentity == "__loom_expansion_identity" && identity.Kind == string(expression.KindObject))) ||
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

	anchorVariable := allocateConstructionVariable(lowerer.usedVariables, fmt.Sprintf("related_eligibility_%d_anchor", stepIndex), stepIndex)
	anchorNode := recipeplan.SemanticNode{Alias: anchorNodeID, ResourceType: anchorResourceType}
	subplan := ir.PhysicalSubplan{Captures: []string{inputRow}}
	if anchorKind == "root" || anchorKind == "rootContributors" {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalCollectionScanOp, Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType},
			CollectionScan: &ir.PhysicalCollectionScan{Variable: anchorVariable, CollectionBindKey: "root_collection"},
		})
		operator := "EQUALS"
		anchorPath := "_key"
		if anchorKind == "rootContributors" {
			operator = "IN"
			anchorPath = anchor.Name
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp, Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_key"},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: operator, Left: ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{anchorPath}},
			}},
		})
	} else {
		collectionBind := nextTableReshapeBindKey(lowerer.plan.BindVars, "related_eligibility_anchor_collection")
		lowerer.plan.BindVars[collectionBind] = anchorResourceType
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
		targetVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_target", stepIndex)
		edgeVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_edge", stepIndex)
		traversal, err := physicalpkg.BuildPhysicalTraversal(physicalpkg.TraversalLoweringRequest{
			FromType: hop.FromResourceType, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: lowerer.policy,
		})
		if err != nil {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route hop %d: %w", routeIndex, err)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return ir.PhysicalFilter{}, nil, nil, nil, fmt.Errorf("related eligibility route hop %d storage direction changed", routeIndex)
		}
		for key, value := range traversal.BindVars {
			lowerer.plan.BindVars[key] = value
		}
		child := recipeplan.SemanticNode{Alias: hop.ToNodeID, ResourceType: hop.ToResourceType, EdgeLabel: hop.Relationship}
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
				Selector: selector, ExecutionMode: physicalpkg.SelectorExecutionMode(source.ResourceType, selector)},
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
				bindKey := nextTableReshapeBindKey(lowerer.plan.BindVars, "related_eligibility_contributor_value")
				lowerer.plan.BindVars[bindKey] = literal
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
		countVariable := allocateConstructionVariable(lowerer.usedVariables, fmt.Sprintf("related_eligibility_%d_distinct_count", stepIndex), stepIndex)
		thresholdBind := nextTableReshapeBindKey(lowerer.plan.BindVars, "related_eligibility_threshold")
		lowerer.plan.BindVars[thresholdBind] = *related.Threshold
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
		prior.Name, prior.Label = declaration.Name, loomtext.FirstNonBlank(declaration.Label, prior.Label, declaration.Name)
		compiled = append(compiled, prior)
	}
	projections := stagePassThroughProjections(step.Outputs, input, inputRow)
	return filter, derivedLets, projections, compiled, nil
}

func (lowerer *constructionLowerer) lowerConstructionRelatedExpand(
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedExpand,
	input map[string]CompiledOutputColumn,
	inputRow, inputIdentity, rootResourceType string,
	stepIndex int,
) (ir.PhysicalStageRelatedExpand, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID == "" || inputIdentity == "" {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion requires a retained row resource anchor")
	}
	if len(related.Route) == 0 {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route must contain at least one hop")
	}
	anchor, ok := input[related.AnchorColumnID]
	if !ok || !anchor.Internal || anchor.Kind != string(expression.KindString) {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion anchor is not a compiler-proven exact resource identity")
	}
	anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, rootResourceType
	rootContributorColumn := ""
	if related.AnchorColumnID == rootContributorSetColumn {
		contributor, found := rootContributorSetFromMap(input, rootResourceType)
		if !found || contributor.Name != anchor.Name || anchor.RootContributorResourceType != rootResourceType ||
			anchor.Cardinality != string(expression.Many) || rootResourceType == "" || rootResourceType != lowerer.plan.Source.ResourceType {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion contributor anchor is not the compiler-proven root identity set")
		}
		anchorKind = "rootContributors"
		rootContributorColumn = contributor.Name
	} else if related.AnchorColumnID == "_key" {
		if anchor.Cardinality != string(expression.RequiredOne) {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion root anchor is not scalar")
		}
		if anchor.Name != "_key" || anchor.Cardinality != string(expression.RequiredOne) || rootResourceType != lowerer.plan.Source.ResourceType {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion root anchor is not the compiler-proven root document key")
		}
	} else {
		if anchor.Cardinality != string(expression.RequiredOne) && anchor.Cardinality != string(expression.OptionalOne) {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion active anchor is not scalar")
		}
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

	anchorVariable := allocateConstructionVariable(lowerer.usedVariables, fmt.Sprintf("related_expand_%d_anchor", stepIndex), stepIndex)
	anchorNode := recipeplan.SemanticNode{Alias: anchorNodeID, ResourceType: anchorResourceType}
	subplan := ir.PhysicalSubplan{Captures: []string{inputRow}}
	if anchorKind == "root" || anchorKind == "rootContributors" {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:           ir.PhysicalCollectionScanOp,
			Source:         ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType},
			CollectionScan: &ir.PhysicalCollectionScan{Variable: anchorVariable, CollectionBindKey: "root_collection"},
		})
		operator, rightColumn := "EQUALS", anchor.Name
		if anchorKind == "rootContributors" {
			operator, rightColumn = "IN", rootContributorColumn
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: anchorNode.Alias, ResourceType: anchorResourceType, SemanticField: "_key"},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: operator, Left: ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{rightColumn}},
			}},
		})
	} else {
		collectionBind := nextTableReshapeBindKey(lowerer.plan.BindVars, "related_expand_anchor_collection")
		lowerer.plan.BindVars[collectionBind] = anchorResourceType
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
		targetVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_target", stepIndex)
		edgeVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_edge", stepIndex)
		traversal, err := physicalpkg.BuildPhysicalTraversal(physicalpkg.TraversalLoweringRequest{
			FromType: hop.FromResourceType, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: lowerer.policy,
		})
		if err != nil {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route hop %d: %w", routeIndex, err)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return ir.PhysicalStageRelatedExpand{}, nil, nil, fmt.Errorf("related expansion route hop %d storage direction changed", routeIndex)
		}
		for key, value := range traversal.BindVars {
			lowerer.plan.BindVars[key] = value
		}
		child := recipeplan.SemanticNode{Alias: hop.ToNodeID, ResourceType: hop.ToResourceType, EdgeLabel: hop.Relationship}
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

	if err := lowerer.lowerConstructionRelatedExpandContributor(&subplan, related, currentVariable); err != nil {
		return ir.PhysicalStageRelatedExpand{}, nil, nil, err
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

	constructionBind := nextTableReshapeBindKey(lowerer.plan.BindVars, "construction_related_expand_id")
	lowerer.plan.BindVars[constructionBind] = step.ID
	parentColumnID, terminalColumnID := relatedExpandIdentityColumnNames(step.ID)
	contributorRootType := ""
	if rootContributorColumn != "" {
		contributorRootType = rootResourceType
	}
	physical := ir.PhysicalStageRelatedExpand{
		AnchorColumnID: related.AnchorColumnID, AnchorKind: anchorKind, AnchorNodeID: anchorNodeID,
		AnchorResourceType: anchorResourceType, RelatedRecordColumnID: related.RelatedRecordColumnID,
		RootContributorColumn: rootContributorColumn, RootResourceType: contributorRootType,
		TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
		ParentIdentityColumn: inputIdentity, ParentIdentityColumnID: parentColumnID,
		TerminalIdentityColumn: terminalColumnID,
		RelatedRecordsVariable: allocateConstructionVariable(lowerer.usedVariables, "related_expand_records", stepIndex),
		IndexVariable:          allocateConstructionVariable(lowerer.usedVariables, "related_expand_index", stepIndex),
		ItemVariable:           allocateConstructionVariable(lowerer.usedVariables, "related_expand_item", stepIndex),
		IdentityVariable:       allocateConstructionVariable(lowerer.usedVariables, "related_expand_identity", stepIndex),
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
			prior.Name, prior.Label = declaration.Name, loomtext.FirstNonBlank(declaration.Label, prior.Label, declaration.Name)
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
			ID: declaration.ID, Name: declaration.Name, Label: loomtext.FirstNonBlank(declaration.Label, declaration.Name),
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

func (lowerer *constructionLowerer) lowerConstructionRelatedExpandContributor(subplan *ir.PhysicalSubplan, related recipe.ConstructionRelatedExpand, currentVariable string) error {
	source := related.ContributorSource
	if source == nil {
		return nil
	}
	if source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
		(source.Cardinality != "optional_one" && source.Cardinality != "required_one" && source.Cardinality != "many") {
		return fmt.Errorf("related expansion contributor field is not an exact target scalar or repeated scalar")
	}
	if source.Cardinality == "many" && len(source.RepeatedBoundaries) == 0 {
		return fmt.Errorf("related expansion repeated contributor field is missing its exact repeated boundaries")
	}
	if source.Cardinality != "many" && len(source.RepeatedBoundaries) != 0 {
		return fmt.Errorf("related expansion scalar contributor field cannot carry repeated boundaries")
	}
	selectorPath := strings.TrimPrefix(source.Path, source.ResourceType+".")
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		return fmt.Errorf("related expansion contributor path: %w", err)
	}
	field := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: currentVariable, Path: []string{"payload"}},
			ResourceType: source.ResourceType, Selector: selector, ExecutionMode: physicalpkg.SelectorExecutionMode(source.ResourceType, selector)},
	}
	if predicate := related.ContributorPredicate; predicate != nil {
		if predicate.CandidateID != source.CandidateID {
			return fmt.Errorf("related expansion predicate differs from its selected contributor field")
		}
		comparison := ir.PhysicalPredicate{
			Operator: string(predicate.Operator), Quantifier: spec.ArrayQuantifier(predicate.Quantifier), LeftExpression: &field,
		}
		if predicate.Operator == recipe.FilterEquals {
			if predicate.Value == nil {
				return fmt.Errorf("related expansion EQUALS predicate requires a value")
			}
			literal, err := constructionFilterLiteral(*predicate.Value)
			if err != nil {
				return fmt.Errorf("related expansion predicate value: %w", err)
			}
			bindKey := nextTableReshapeBindKey(lowerer.plan.BindVars, "related_expand_contributor_value")
			lowerer.plan.BindVars[bindKey] = literal
			comparison.Right = &ir.PhysicalValue{BindKey: bindKey}
			comparison.ValueKind = spec.FilterValueKind(predicate.Value.Kind)
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: source.NodeID, ResourceType: source.ResourceType, SemanticField: source.CandidateID},
			Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &comparison}},
		})
	}
	return nil
}

func (lowerer *constructionLowerer) lowerConstructionGroup(
	group recipe.ConstructionGroup,
	rowValues []recipe.ConstructionRowValue,
	declarations []recipe.StageColumn,
	input map[string]CompiledOutputColumn,
	inputIdentity string,
	inputStageID string,
	rootResourceType string,
	outputs map[string]recipe.StageColumn,
	stepIndex int,
	stage *ir.PhysicalConstructionStage,
) (ir.PhysicalStageGroup, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	constructionBind := nextTableReshapeBindKey(lowerer.plan.BindVars, "construction_group_id")
	lowerer.plan.BindVars[constructionBind] = group.ConstructionID
	physical := ir.PhysicalStageGroup{
		GroupRowsVariable:     allocateConstructionVariable(lowerer.usedVariables, "group_rows", stepIndex),
		IdentityVariable:      allocateConstructionVariable(lowerer.usedVariables, "group_identity", stepIndex),
		ConstructionIDBindKey: constructionBind,
		MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyPolicy(group.MissingKeyPolicy.Normalized()),
	}
	var groupFieldContext *CompiledGroupFieldSourceContext
	needsGroupFieldContext := false
	for _, key := range group.Keys {
		if key.FieldSource != nil {
			needsGroupFieldContext = true
		}
	}
	for _, rowValue := range rowValues {
		if rowValue.FieldSource != nil {
			needsGroupFieldContext = true
		}
	}
	if needsGroupFieldContext {
		if inputStageID != recipe.ConstructionSourceProjectionID {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource must bind the exact input stage")
		}
		var err error
		groupFieldContext, err = describeGroupFieldSourceContext(inputStageID, rootResourceType, inputIdentity, lowerer.plan)
		if err != nil {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("recompute Group field source context: %w", err)
		}
		if groupFieldContext == nil {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource has no exact compiler-proven source shape")
		}
	}
	metadataByOutputID := make(map[string]CompiledOutputColumn, len(declarations))
	projectionByOutputID := make(map[string]ir.PhysicalProjection, len(declarations))
	for _, key := range group.Keys {
		if key.SemanticCategory != nil {
			if err := constructionDirectRootRowsUnique(lowerer.plan, "semantic GROUP"); err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic Group requires row-preserving direct root input: %w", err)
			}
			if inputStageID != recipe.ConstructionSourceProjectionID || inputIdentity != "_key" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group keys require direct root rows identified by _key")
			}
			category := key.SemanticCategory
			if category.Kind != "BINDING" || category.ResourceType != rootResourceType {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group binding does not match direct root resource %q", rootResourceType)
			}
			if len(lowerer.plan.Operations) == 0 || lowerer.plan.Operations[0].Kind != ir.PhysicalRootScanOp || lowerer.plan.Operations[0].RootScan == nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group requires a direct root resource scan")
			}
			rootScan := lowerer.plan.Operations[0].RootScan
			if rootScan.CollectionBindKey == "" || lowerer.plan.BindVars[rootScan.CollectionBindKey] != rootResourceType || rootScan.Variable == "" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group root collection does not match selected root resource %q", rootResourceType)
			}
			index, err := fhirschema.GeneratedIndex()
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("load generated FHIR schema: %w", err)
			}
			facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), category.CodingPath)
			if err != nil || facts.ResourceType != fhirschema.DefinitionName(rootResourceType) ||
				facts.CanonicalPath != category.CodingPath || facts.FHIRType != "Coding" ||
				facts.Cardinality != fhirschema.RowCardinalityMany || facts.Shape != fhirschema.RowPathArray || facts.Reference {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group path %q is not an executable generated repeated Coding path on %s", category.CodingPath, rootResourceType)
			}
			paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(rootResourceType))
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("enumerate generated Coding paths: %w", err)
			}
			pathFound := false
			for _, path := range paths {
				if path == category.CodingPath {
					pathFound = true
					break
				}
			}
			if !pathFound {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group path %q is not advertised by the generated FHIR schema", category.CodingPath)
			}
			segments, err := parseCodedGroupPath(category.CodingPath)
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, err
			}
			output, ok := outputs[key.OutputColumnID]
			if !ok {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group output column ID %q is missing from step schema", key.OutputColumnID)
			}
			if output.Type != "" && output.Type != "INFER" && output.Type != "string" {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("semantic group output %q must be a string column", output.ID)
			}
			physical.SemanticKey = &ir.PhysicalStageGroupSemanticKey{
				OutputColumn: output.Name, TupleVariable: allocateConstructionVariable(lowerer.usedVariables, "group_semantic_tuple", stepIndex),
				LabelVariable:      allocateConstructionVariable(lowerer.usedVariables, "group_semantic_label", stepIndex),
				SourceRootVariable: rootScan.Variable, SourceIdentityColumn: inputIdentity,
				RootCollectionBindKey: rootScan.CollectionBindKey, ResourceType: rootResourceType,
				CodingPath: category.CodingPath, PathSegments: segments, BindingID: category.BindingID,
				RuleVersion: category.RuleVersion, FamilyID: category.FamilyID, System: category.System, Version: category.Version,
			}
			metadataByOutputID[output.ID] = CompiledOutputColumn{
				ID: output.ID, Name: output.Name, Label: loomtext.FirstNonBlank(output.Label, output.Name),
				Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne),
				Nullable:     group.MissingKeyPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup,
				SemanticPath: "construction_semantic_group:" + category.BindingID + ":" + category.FamilyID,
			}
			projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physical.SemanticKey.LabelVariable}}
			continue
		}
		if key.FieldSource != nil {
			source := *key.FieldSource
			output, ok := outputs[key.OutputColumnID]
			if !ok {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource output column ID %q is missing from step schema", key.OutputColumnID)
			}
			if source.ValueKind == "CODING_IDENTITY" {
				if physical.SemanticKey != nil || physical.CodingKey != nil {
					return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group supports at most one coded identity key")
				}
				if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
					codingKey, column, projection, membershipColumn, membershipProjection, err := lowerer.lowerConstructionGroupRelatedCodingKey(
						source, groupFieldContext, output, group.MissingKeyPolicy, stepIndex, len(physical.Keys), stage,
						&physical.RelatedOccurrence,
					)
					if err != nil {
						return ir.PhysicalStageGroup{}, nil, nil, err
					}
					physical.CodingKey = &codingKey
					metadataByOutputID[output.ID] = column
					projectionByOutputID[output.ID] = projection
					_ = membershipColumn
					_ = membershipProjection
					continue
				}
				codingKey, column, projection, _, _, err := lowerConstructionGroupCodingKey(
					lowerer.plan, source, groupFieldContext, output, group.MissingKeyPolicy, lowerer.usedVariables, stepIndex, stage,
				)
				if err != nil {
					return ir.PhysicalStageGroup{}, nil, nil, err
				}
				physical.CodingKey = &codingKey
				metadataByOutputID[output.ID] = column
				projectionByOutputID[output.ID] = projection
				continue
			}
			inputColumn, inputProjection, fieldSource, err := lowerConstructionGroupScalarSource(
				lowerer.plan, source, groupFieldContext, stepIndex, len(physical.Keys), lowerer.usedVariables, stage,
				constructionGroupFieldSourceLoweringOptions{RelatedAdapterSlot: &physical.RelatedOccurrence, Policy: lowerer.policy},
			)
			if err != nil {
				return ir.PhysicalStageGroup{}, nil, nil, err
			}
			kind, scalar := TableReshapeScalarKind(inputColumn.Kind)
			if !scalar {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource %q is not a supported scalar", source.CanonicalPath)
			}
			if output.Type != "" && output.Type != "INFER" && !strings.EqualFold(output.Type, inputColumn.Kind) {
				return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("Group fieldSource output %q must preserve scalar type %q", output.ID, inputColumn.Kind)
			}
			physicalKey := ir.PhysicalStageGroupKey{
				InputColumn: inputColumn.Name, OutputColumn: output.Name,
				Variable: allocateConstructionVariable(lowerer.usedVariables, "group_key", stepIndex), Kind: kind,
				InputInternal: true, FieldSource: fieldSource,
			}
			physical.Keys = append(physical.Keys, physicalKey)
			inputColumn.ID, inputColumn.Name = output.ID, output.Name
			inputColumn.Label = loomtext.FirstNonBlank(output.Label, output.Name)
			inputColumn.Internal = false
			metadataByOutputID[output.ID] = inputColumn
			projectionByOutputID[output.ID] = inputProjection
			projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalKey.Variable}}
			continue
		}
		inputColumn, ok := input[key.InputColumnID]
		if !ok || inputColumn.Internal {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key input column ID %q is not public", key.InputColumnID)
		}
		if inputColumn.SemanticValueKind == CompiledSemanticValueCodingIdentityDisplay {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key input column ID %q is a CODING_IDENTITY display label and cannot be reused as scalar identity", key.InputColumnID)
		}
		kind, scalar := TableReshapeScalarKind(inputColumn.Kind)
		if !scalar || inputColumn.Cardinality == string(expression.Many) {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key column %q must be a public scalar column", inputColumn.Name)
		}
		output, ok := outputs[key.OutputColumnID]
		if !ok {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group key output column ID %q is missing from step schema", key.OutputColumnID)
		}
		physicalKey := ir.PhysicalStageGroupKey{
			InputColumn: inputColumn.Name, OutputColumn: output.Name,
			Variable: allocateConstructionVariable(lowerer.usedVariables, "group_key", stepIndex), Kind: kind,
		}
		physical.Keys = append(physical.Keys, physicalKey)
		column := inputColumn
		column.ID, column.Name = output.ID, output.Name
		column.Label = loomtext.FirstNonBlank(output.Label, inputColumn.Label, output.Name)
		metadataByOutputID[output.ID] = column
		projectionByOutputID[output.ID] = ir.PhysicalProjection{Name: output.Name, Value: ir.PhysicalValue{Variable: physicalKey.Variable}}
	}
	aggregates, aggregateColumns, aggregateProjections, err := lowerConstructionGroupAggregates(group, input, outputs, lowerer.usedVariables, stepIndex)
	if err != nil {
		return ir.PhysicalStageGroup{}, nil, nil, err
	}
	physical.Aggregates = aggregates
	for id, column := range aggregateColumns {
		metadataByOutputID[id] = column
	}
	for id, projection := range aggregateProjections {
		projectionByOutputID[id] = projection
	}
	rowValueSource := &constructionGroupRowValueSourceContext{
		Plan: lowerer.plan, SourceContext: groupFieldContext, InputStageID: inputStageID,
		Stage: stage, FirstSourceIndex: len(group.Keys), RelatedAdapterSlot: &physical.RelatedOccurrence, Policy: lowerer.policy,
	}
	physicalRowValues, rowValueColumns, err := lowerConstructionRowValues(
		rowValues, input, outputs, group.ConstructionID, lowerer.usedVariables, stepIndex, rowValueSource,
	)
	if err != nil {
		return ir.PhysicalStageGroup{}, nil, nil, err
	}
	physical.RowValues = physicalRowValues
	for index, rowValue := range physicalRowValues {
		column := rowValueColumns[index]
		metadataByOutputID[column.ID] = column
		projectionByOutputID[column.ID] = ir.PhysicalProjection{Name: column.Name, Value: ir.PhysicalValue{Variable: rowValue.Variable}}
	}
	compiled := make([]CompiledOutputColumn, 0, len(declarations)+1)
	projections := make([]ir.PhysicalProjection, 0, len(declarations)+1)
	for _, declaration := range declarations {
		column, ok := metadataByOutputID[declaration.ID]
		if !ok {
			return ir.PhysicalStageGroup{}, nil, nil, fmt.Errorf("group output schema contains unexpected column ID %q", declaration.ID)
		}
		column.Label = loomtext.FirstNonBlank(declaration.Label, column.Label, declaration.Name)
		column.Name = declaration.Name
		projection := projectionByOutputID[declaration.ID]
		projection.Name = declaration.Name
		compiled = append(compiled, column)
		projections = append(projections, projection)
	}
	projections = append(projections, ir.PhysicalProjection{
		Name: constructionRowID, Hidden: true, Value: ir.PhysicalValue{Variable: physical.IdentityVariable},
	})
	return physical, projections, compiled, nil
}
