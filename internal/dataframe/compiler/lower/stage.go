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
)

type constructionStageLowering struct {
	input       constructionStageInput
	inputByID   map[string]CompiledOutputColumn
	outputByID  map[string]recipe.StageColumn
	publicInput []CompiledOutputColumn
	physical    ir.PhysicalConstructionStage
	schema      []CompiledOutputColumn
	identity    string
}

func (lowerer *constructionLowerer) lowerConstructionStep(step recipe.ConstructionStep, input constructionStageInput) (constructionStageResult, error) {
	inputStageID, inputSchema, inputIdentity := input.stageID, input.schema, input.identity
	rootResourceType, index := input.rootResourceType, input.index
	rootContributorProvenance := input.rootContributorProvenance
	retainRootContributors := input.retainRootContributors
	inputByID := compiledSchemaByID(inputSchema)
	outputByID := make(map[string]recipe.StageColumn, len(step.Outputs))
	for _, column := range step.Outputs {
		outputByID[column.ID] = column
	}
	inputRow := allocateConstructionVariable(lowerer.usedVariables, "input", index)
	outputRow := allocateConstructionVariable(lowerer.usedVariables, "output", index)
	inputColumns := toPhysicalStageColumns(inputSchema)
	inputProjections := stageInputProjections(inputSchema, inputRow)
	base := ir.PhysicalConstructionStage{
		ID: step.ID, InputStageID: inputStageID, InputRowVariable: inputRow,
		OutputRowVariable: outputRow, InputColumns: inputColumns,
		InputProjections: inputProjections,
	}
	work := constructionStageLowering{
		input: input, inputByID: inputByID, outputByID: outputByID,
		publicInput: publicCompiledSchema(inputSchema), physical: base,
	}
	var err error
	switch step.Operation.Kind {
	case recipe.ConstructionDeriveOp:
		err = lowerer.lowerDeriveStage(step, &work)
	case recipe.ConstructionFilterOp:
		err = lowerer.lowerFilterStage(step, &work)
	case recipe.ConstructionPivotOp:
		err = lowerer.lowerPivotStage(step, &work)
	case recipe.ConstructionCodedPivotOp:
		err = lowerer.lowerCodedPivotStage(step, &work)
	case recipe.ConstructionUnpivotOp:
		err = lowerer.lowerUnpivotStage(step, &work)
	case recipe.ConstructionGroupOp:
		err = lowerer.lowerGroupStage(step, &work)
	case recipe.ConstructionCodedGroupOp:
		err = lowerer.lowerCodedGroupStage(step, &work)
	case recipe.ConstructionExpandOp:
		err = lowerer.lowerExpandStage(step, &work)
	case recipe.ConstructionRelatedSourceOp:
		err = lowerer.lowerRelatedSourceStage(step, &work)
	case recipe.ConstructionRelatedExpandOp:
		err = lowerer.lowerRelatedExpandStage(step, &work)
	case recipe.ConstructionRelatedEligibilityOp:
		err = lowerer.lowerRelatedEligibilityStage(step, &work)
	case recipe.ConstructionRelatedFieldOp:
		err = lowerer.lowerRelatedFieldStage(step, &work)
	default:
		return constructionStageResult{}, fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
	if err != nil {
		return constructionStageResult{}, err
	}
	base, outputSchema, outputIdentity := work.physical, work.schema, work.identity
	if constructionPreservesPresenceCompanions(base.Kind) {
		outputSchema, base.OutputProjections = carryConstructionPresenceCompanions(inputSchema, outputSchema, base.OutputProjections, inputRow)
	} else {
		outputSchema = withoutConstructionPresenceCompanions(outputSchema)
	}

	if base.Kind == ir.PhysicalStageDeriveOp {
		for _, column := range inputSchema {
			if column.Internal && column.Name == "__loom_reshape_unlisted_count" && strings.HasPrefix(column.SemanticPath, "table_reshape:") {
				if _, exists := schemaColumn(outputSchema, column.Name); !exists {
					outputSchema = append(outputSchema, column)
					base.OutputProjections = insertProjectionBeforeIdentity(base.OutputProjections, ir.PhysicalProjection{Name: column.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{column.Name}}}, outputIdentity)
				}
			}
		}
	}
	if outputIdentity == "" {
		return constructionStageResult{}, fmt.Errorf("operation did not produce a row identity")
	}
	if retainRootContributors && rootContributorProvenance && constructionCarriesRootContributorValue(step.Operation.Kind) && base.Kind != ir.PhysicalStageGroupOp && base.Kind != ir.PhysicalStagePivotOp {
		if contributor, ok := rootContributorInput(inputSchema, rootResourceType); ok {
			if _, alreadyRetained := rootContributorInput(outputSchema, rootResourceType); !alreadyRetained && contributor.Name != outputIdentity {
				outputSchema = append(outputSchema, contributor)
				projection := ir.PhysicalProjection{
					Name: contributor.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{contributor.Name}},
				}
				base.OutputProjections = insertProjectionBeforeIdentity(base.OutputProjections, projection, outputIdentity)
			}
		}
	}
	if base.Kind == ir.PhysicalStageExpandOp {
		if rootKey, ok := rootContributorRootKey(inputSchema, rootResourceType); ok && rootKey.Name != outputIdentity {
			if _, alreadyRetained := rootContributorRootKey(outputSchema, rootResourceType); !alreadyRetained {
				rootKey.Identity = false
				outputSchema = append(outputSchema, rootKey)
				base.OutputProjections = insertProjectionBeforeIdentity(base.OutputProjections, ir.PhysicalProjection{
					Name: rootKey.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{rootKey.Name}},
				}, outputIdentity)
			}
		}
	}
	if base.Kind != ir.PhysicalStageRelatedExpandOp && preservesActiveRelatedRecord(base.Kind) {
		if rootAnchor, ok := retainedRootResourceAnchor(inputSchema, rootResourceType); ok && rootAnchor.Name != outputIdentity {
			rootAnchor.Identity = false
			outputSchema = append(outputSchema, rootAnchor)
			base.OutputProjections = append(base.OutputProjections, ir.PhysicalProjection{
				Name: rootAnchor.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{rootAnchor.Name}},
			})
		}
		if anchor, ok := activeRelatedRecordColumn(inputSchema); ok {
			outputSchema = append(outputSchema, anchor)
			base.OutputProjections = append(base.OutputProjections, ir.PhysicalProjection{
				Name: anchor.Name, Hidden: true, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{anchor.Name}},
			})
		}
	}
	if base.Kind == ir.PhysicalStageGroupOp || base.Kind == ir.PhysicalStagePivotOp || base.Kind == ir.PhysicalStageUnpivotOp {
		outputSchema = append(outputSchema, constructionGeneratedRowIdentitySchema(outputIdentity))
	} else {
		outputSchema = append(outputSchema, constructionIdentitySchema(outputIdentity, outputSchema, inputSchema))
	}
	if base.Kind == ir.PhysicalStageDeriveOp || base.Kind == ir.PhysicalStageFilterOp || base.Kind == ir.PhysicalStageRelatedEligibilityOp || base.Kind == ir.PhysicalStageRelatedSourceOp || base.Kind == ir.PhysicalStageRelatedFieldOp {
		base.OutputProjections = append(base.OutputProjections, ir.PhysicalProjection{
			Name: outputIdentity, Hidden: true,
			Value: ir.PhysicalValue{Variable: inputRow, Path: []string{inputIdentity}},
		})
	} else if base.Kind == ir.PhysicalStageRelatedExpandOp {
		base.OutputProjections = append(base.OutputProjections, ir.PhysicalProjection{
			Name: outputIdentity, Hidden: true, Value: ir.PhysicalValue{Variable: base.RelatedExpand.IdentityVariable},
		})
	}
	base.InputRowVariable = inputRow
	base.RowIdentityColumn = outputIdentity
	if len(base.InputColumns) == len(inputColumns) {
		base.InputColumns = inputColumns
	}
	base.OutputColumns = toPhysicalStageColumns(outputSchema)
	if base.Kind == ir.PhysicalStageDeriveOp || base.Kind == ir.PhysicalStageFilterOp || base.Kind == ir.PhysicalStageRelatedEligibilityOp || base.Kind == ir.PhysicalStageRelatedSourceOp || base.Kind == ir.PhysicalStageRelatedFieldOp {
		base.InputProjections = inputProjections
	}
	descriptor := CompiledStageDescriptor{
		ID: step.ID, InputStageID: inputStageID, Operation: string(step.Operation.Kind),
		Columns: cloneCompiledSchema(outputSchema), RowIdentityColumn: outputIdentity,
	}
	if base.RelatedExpand != nil {
		descriptor.RelatedExpand = &CompiledRelatedExpandStage{
			AnchorColumnID: base.RelatedExpand.AnchorColumnID, AnchorColumn: base.RelatedExpand.AnchorColumnID,
			AnchorKind: base.RelatedExpand.AnchorKind, AnchorNodeID: base.RelatedExpand.AnchorNodeID,
			AnchorResourceType:     base.RelatedExpand.AnchorResourceType,
			RelatedRecordColumnID:  base.RelatedExpand.RelatedRecordColumnID,
			ParentIdentityColumnID: base.RelatedExpand.ParentIdentityColumnID,
			ParentIdentityColumn:   base.RelatedExpand.ParentIdentityColumn,
			TerminalIdentityColumn: base.RelatedExpand.TerminalIdentityColumn,
			TargetNodeID:           base.RelatedExpand.TargetNodeID, TargetResourceType: base.RelatedExpand.TargetResourceType,
			Route: append([]recipe.ConstructionRelatedRouteStep(nil), step.Operation.RelatedExpand.Route...),
		}
	}
	if anchor, ok := activeRelatedRecordColumn(outputSchema); ok {
		descriptor.ActiveRelatedRecord = &CompiledActiveRelatedRecordStage{
			TerminalIdentityColumn: anchor.Name,
			TargetNodeID:           anchor.RelatedRecordAnchor.TargetNodeID,
			TargetResourceType:     anchor.RelatedRecordAnchor.TargetResourceType,
			Nullable:               anchor.Nullable,
		}
	}
	rootContributorOutputProvenance := rootContributorProvenance && constructionPreservesRootContributorProvenance(step.Operation.Kind)
	lazyRootContributorOutputAvailable := input.lazyRootContributorAvailable && constructionPreservesLazyRootContributorAvailability(step.Operation.Kind)
	if step.Operation.Kind == recipe.ConstructionGroupOp || step.Operation.Kind == recipe.ConstructionPivotOp {
		lazyRootContributorOutputAvailable = rootContributorOutputProvenance
	}
	descriptor.Capabilities = stageCapabilities(outputSchema, lazyRootContributorOutputAvailable)
	descriptor.RelatedExpandAnchors = relatedExpandAnchors(outputSchema, rootResourceType, lazyRootContributorOutputAvailable)
	return constructionStageResult{physical: base, schema: outputSchema, identity: outputIdentity, rootContributorProvenance: rootContributorOutputProvenance, lazyRootContributorAvailable: lazyRootContributorOutputAvailable, descriptor: descriptor}, nil
}

func (lowerer *constructionLowerer) lowerConstructionRelatedSource(
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedSource,
	inputByID map[string]CompiledOutputColumn,
	outputByID map[string]recipe.StageColumn,
	inputRow, inputIdentity, rootResourceType string,
	index int,
) ([]ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID != inputIdentity || rootResourceType == "" || rootResourceType != lowerer.plan.Source.ResourceType {
		reason := "row_identity_changed"
		if rootResourceType == "" || rootResourceType != lowerer.plan.Source.ResourceType {
			reason = "root_resource_changed"
		}
		return nil, nil, &RelatedSourceAnchorError{StepID: step.ID, Reason: reason}
	}
	anchor, ok := inputByID[related.AnchorColumnID]
	if !ok || !anchor.Internal || !anchor.Identity || anchor.Name != inputIdentity {
		return nil, nil, &RelatedSourceAnchorError{StepID: step.ID, Reason: "row_identity_unproven"}
	}
	contributor, hasContributor := rootContributorInputFromMap(inputByID, rootResourceType)
	directRootAnchor := anchor.Name == "_key" && anchor.RootContributorResourceType == rootResourceType &&
		anchor.Kind == string(expression.KindString) && anchor.Cardinality == string(expression.RequiredOne)
	if !directRootAnchor && !hasContributor {
		return nil, nil, fmt.Errorf("related source requires a compiler-proven root contributor identity after row shaping")
	}
	if related.Source.Cardinality != "optional_one" && related.Source.Cardinality != "required_one" {
		return nil, nil, fmt.Errorf("related source field must be scalar for ALL_MATCHES")
	}
	rowValuePolicy := related.RowValuePolicy
	if rowValuePolicy == "" {
		rowValuePolicy = recipe.ConstructionRowValueAll
	}
	if rowValuePolicy != recipe.ConstructionRowValueAll && rowValuePolicy != recipe.ConstructionRowValueOne {
		return nil, nil, fmt.Errorf("related source row value policy %q is unsupported", rowValuePolicy)
	}
	if rowValuePolicy == recipe.ConstructionRowValueOne && related.Form != "ALL" {
		return nil, nil, fmt.Errorf("related source ONE row value policy requires form ALL")
	}
	if related.SourceOccurrenceID != related.Source.NodeID || len(related.Route) == 0 {
		return nil, nil, fmt.Errorf("related source must persist its exact source occurrence and route")
	}
	if _, scalar := TableReshapeScalarKind(related.Source.LogicalType); !scalar {
		return nil, nil, fmt.Errorf("related source logical type %q is not scalar", related.Source.LogicalType)
	}
	selectorPath := strings.TrimPrefix(related.Source.Path, related.Source.ResourceType+".")
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		return nil, nil, fmt.Errorf("related source field path: %w", err)
	}

	rootVariable := allocateConstructionVariable(lowerer.usedVariables, fmt.Sprintf("related_%d_root", index), index)
	rootNode := recipeplan.SemanticNode{Alias: lowerer.plan.Source.SemanticNode, ResourceType: rootResourceType}
	keySetAnchor := !directRootAnchor && hasContributor && contributor.Cardinality == string(expression.Many)
	rootOperation := ir.PhysicalOperation{
		Kind:   ir.PhysicalCollectionScanOp,
		Source: ir.PhysicalSource{SemanticNode: rootNode.Alias, ResourceType: rootResourceType},
		CollectionScan: &ir.PhysicalCollectionScan{
			Variable: rootVariable, CollectionBindKey: "root_collection",
		},
	}
	if keySetAnchor {
		keyVariable := allocateConstructionVariable(lowerer.usedVariables, fmt.Sprintf("related_%d_root_key", index), index)
		rootOperation = ir.PhysicalOperation{
			Kind:   ir.PhysicalKeySetLookupOp,
			Source: ir.PhysicalSource{SemanticNode: rootNode.Alias, ResourceType: rootResourceType, SemanticField: "_key"},
			KeySetLookup: &ir.PhysicalKeySetLookup{
				Variable: rootVariable, KeyVariable: keyVariable, CollectionBindKey: "root_collection",
				Keys: ir.PhysicalValue{Variable: inputRow, Path: []string{contributor.Name}},
			},
		}
	}
	subplan := ir.PhysicalSubplan{Captures: []string{inputRow}, Operations: []ir.PhysicalOperation{rootOperation}}
	rootKeyFilter := ir.PhysicalPredicate{
		Operator: "EQUALS", Left: ir.PhysicalValue{Variable: rootVariable, Path: []string{"_key"}},
		Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{inputIdentity}},
	}
	if !directRootAnchor {
		rootKeyFilter.Right = &ir.PhysicalValue{Variable: inputRow, Path: []string{contributor.Name}}
	}
	if !keySetAnchor {
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind:   ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{SemanticNode: rootNode.Alias, ResourceType: rootResourceType, SemanticField: "_key"},
			Filter: &ir.PhysicalFilter{Predicate: rootKeyFilter},
		})
	}
	subplan.Operations = appendProjectScope(subplan.Operations, []string{rootVariable}, "", rootNode)
	subplan.Operations = appendDatasetGenerationScope(subplan.Operations, []string{rootVariable}, "", rootNode)
	subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{{Variable: rootVariable, Path: []string{"auth_resource_path"}}}, fmt.Sprintf("related_%d_root_scope_allowed", index), rootNode)

	currentVariable := rootVariable
	currentResource := rootResourceType
	for routeIndex, hop := range related.Route {
		if hop.FromResourceType != currentResource || strings.ToUpper(hop.StorageDirection) != hop.StorageDirection ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") {
			return nil, nil, fmt.Errorf("related source route hop %d does not extend the compiled source", routeIndex)
		}
		prefix := fmt.Sprintf("related_%d_hop_%d", index, routeIndex+1)
		targetVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_target", index)
		edgeVariable := allocateConstructionVariable(lowerer.usedVariables, prefix+"_edge", index)
		traversal, traversalErr := physicalpkg.BuildPhysicalTraversal(physicalpkg.TraversalLoweringRequest{
			FromType: currentResource, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: lowerer.policy,
		})
		if traversalErr != nil {
			return nil, nil, fmt.Errorf("related source route hop %d: %w", routeIndex, traversalErr)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return nil, nil, fmt.Errorf("related source route hop %d storage direction changed", routeIndex)
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
		subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}, prefix+"_scope_allowed", child)
		currentVariable, currentResource = targetVariable, hop.ToResourceType
	}
	if currentResource != related.Source.ResourceType || related.Route[len(related.Route)-1].ToNodeID != related.Source.NodeID {
		return nil, nil, fmt.Errorf("related source route does not end at the exact source occurrence")
	}
	fieldExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: currentVariable, Path: []string{"payload"}}, ResourceType: related.Source.ResourceType, Selector: selector, ExecutionMode: physicalpkg.SelectorExecutionMode(related.Source.ResourceType, selector)},
	}
	if predicate := related.Predicate; predicate != nil {
		if predicate.CandidateID != related.Source.CandidateID {
			return nil, nil, fmt.Errorf("related source contributor predicate candidate does not match the selected source field")
		}
		filterValues := fieldExpression
		filterValues.Cardinality = ir.PhysicalArrayCardinality
		filterValues.NullBehavior = ir.PhysicalEmptyOnNull
		physicalPredicate := ir.PhysicalPredicate{
			Operator: string(predicate.Operator), LeftExpression: &filterValues,
		}
		if predicate.Value != nil {
			literal, literalErr := constructionFilterLiteral(*predicate.Value)
			if literalErr != nil {
				return nil, nil, fmt.Errorf("related source contributor predicate: %w", literalErr)
			}
			bindKey := fmt.Sprintf("related_%d_contributor_value", index)
			lowerer.plan.BindVars[bindKey] = literal
			physicalPredicate.Right = &ir.PhysicalValue{BindKey: bindKey}
			physicalPredicate.ValueKind = spec.FilterValueKind(predicate.Value.Kind)
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{
				SemanticNode: related.Source.NodeID, ResourceType: related.Source.ResourceType,
				SemanticField: related.Source.CandidateID,
			},
			Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind: ir.PhysicalComparisonPredicate, Comparison: &physicalPredicate,
			}},
		})
	}
	outputKind := related.Source.LogicalType
	outputCardinality := expression.Many
	outputNullable := true
	if related.Form == "ALL" {
		subplan.Return = fieldExpression
		terminalIdentity := ir.PhysicalValue{Variable: currentVariable, Path: []string{"_id"}}
		subplan.Sort = &terminalIdentity
		subplan.DistinctBy = &terminalIdentity
	} else {
		identity := ir.PhysicalValue{Variable: currentVariable, Path: []string{"_id"}}
		subplan.Return = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &identity}
		subplan.Sort = &identity
		subplan.Unique = true
	}
	matchingValues := ir.PhysicalExpression{Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Subplan: &subplan}
	outputExpression := matchingValues
	if rowValuePolicy == recipe.ConstructionRowValueOne {
		kind, scalar := TableReshapeScalarKind(related.Source.LogicalType)
		if !scalar {
			return nil, nil, fmt.Errorf("related source logical type %q is not a supported table scalar", related.Source.LogicalType)
		}
		outputExpression = ir.PhysicalExpression{
			Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Call: &ir.PhysicalCall{Name: "distinct_present_one", TargetKind: kind, Args: []ir.PhysicalExpression{matchingValues}},
		}
		outputCardinality = expression.OptionalOne
	}
	if related.Form == "COUNT" || related.Form == "PRESENCE" {
		outputExpression = ir.PhysicalExpression{
			Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Call: &ir.PhysicalCall{Name: "length", Args: []ir.PhysicalExpression{matchingValues}},
		}
		outputKind, outputCardinality, outputNullable = "integer", expression.RequiredOne, false
		if related.Form == "PRESENCE" {
			zeroKey := fmt.Sprintf("related_%d_zero", index)
			lowerer.plan.BindVars[zeroKey] = 0
			zero := ir.PhysicalExpression{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: zeroKey}}
			outputExpression = ir.PhysicalExpression{
				Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
				Call: &ir.PhysicalCall{Name: "gt", Args: []ir.PhysicalExpression{outputExpression, zero}},
			}
			outputKind = "boolean"
		}
	}

	outputSchema := make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
	projections := make([]ir.PhysicalProjection, 0, len(step.Outputs)+1)
	for _, declaration := range step.Outputs {
		if prior, exists := inputByID[declaration.ID]; exists {
			prior.Name, prior.Label = declaration.Name, loomtext.FirstNonBlank(declaration.Label, prior.Label, declaration.Name)
			outputSchema = append(outputSchema, prior)
			projections = append(projections, ir.PhysicalProjection{Name: prior.Name, Value: ir.PhysicalValue{Variable: inputRow, Path: []string{inputByID[declaration.ID].Name}}})
			continue
		}
		if declaration.ID != related.OutputColumnID {
			return nil, nil, fmt.Errorf("related source output schema contains unexpected new column ID %q", declaration.ID)
		}
		column := recipe.StageColumn{ID: declaration.ID, Name: declaration.Name}
		if _, exists := outputByID[column.ID]; !exists {
			return nil, nil, fmt.Errorf("related source output column %q is missing from step schema", related.OutputColumnID)
		}
		outputSchema = append(outputSchema, CompiledOutputColumn{
			ID: declaration.ID, Name: declaration.Name, Label: loomtext.FirstNonBlank(declaration.Label, declaration.Name),
			SemanticPath: "related_source:" + related.Source.NodeID + "." + selectorPath,
			Kind:         outputKind, Cardinality: string(outputCardinality), Nullable: outputNullable,
		})
		projections = append(projections, ir.PhysicalProjection{Name: declaration.Name, Expression: &outputExpression})
	}
	return projections, outputSchema, nil
}

func constructionSemanticPivot(pivot recipe.ConstructionPivot, input map[string]CompiledOutputColumn, output map[string]recipe.StageColumn) (semanticPivot recipeplan.SemanticGroupedPivot, outputNames map[string]string, err error) {
	if pivot.Discovery != nil {
		return semanticPivot, nil, fmt.Errorf("pivot %q discovery must be resolved to concrete categories before compilation", pivot.ConstructionID)
	}
	name := func(id string) (string, error) {
		column, ok := input[id]
		if !ok || column.Internal {
			return "", fmt.Errorf("pivot input column ID %q is not public", id)
		}
		return column.Name, nil
	}
	semanticPivot = recipeplan.SemanticGroupedPivot{
		ConstructionID: pivot.ConstructionID, DuplicatePolicy: pivot.DuplicatePolicy,
		MissingCellPolicy: pivot.MissingCellPolicy, UnlistedCategoryPolicy: pivot.UnlistedCategoryPolicy,
	}
	outputNames = map[string]string{}
	for _, id := range pivot.GroupKeyIDs {
		inputColumn := input[id]
		outputColumn := output[id]
		semanticPivot.GroupKeys = append(semanticPivot.GroupKeys, inputColumn.Name)
		outputNames[inputColumn.Name] = outputColumn.Name
	}
	var nameErr error
	semanticPivot.CategoryColumn, nameErr = name(pivot.CategoryColumnID)
	if nameErr != nil {
		return semanticPivot, nil, fmt.Errorf("category column: %w", nameErr)
	}
	semanticPivot.ValueColumn, nameErr = name(pivot.ValueColumnID)
	if nameErr != nil {
		return semanticPivot, nil, fmt.Errorf("value column: %w", nameErr)
	}
	for _, category := range pivot.Categories {
		column, ok := output[category.OutputColumnID]
		if !ok {
			return semanticPivot, nil, fmt.Errorf("pivot output column ID %q is missing", category.OutputColumnID)
		}
		semanticPivot.Categories = append(semanticPivot.Categories, recipeplan.SemanticGroupedPivotCategory{Key: category.Key, Output: column.Name, Label: column.Label})
		outputNames[column.Name] = column.Name
	}
	return semanticPivot, outputNames, nil
}

func constructionSemanticUnpivot(unpivot recipe.ConstructionUnpivot, input map[string]CompiledOutputColumn, output map[string]recipe.StageColumn) (recipeplan.SemanticUnpivot, error) {
	semanticUnpivot := recipeplan.SemanticUnpivot{
		ConstructionID: unpivot.ConstructionID, NullRowPolicy: unpivot.NullRowPolicy,
	}
	for _, item := range unpivot.Inputs {
		column, ok := input[item.ColumnID]
		if !ok || column.Internal {
			return recipeplan.SemanticUnpivot{}, fmt.Errorf("unpivot input column ID %q is not public", item.ColumnID)
		}
		semanticUnpivot.Inputs = append(semanticUnpivot.Inputs, recipeplan.SemanticUnpivotInput{Column: column.Name, Key: item.Key})
	}
	keyOutput, ok := output[unpivot.KeyOutputColumnID]
	if !ok {
		return recipeplan.SemanticUnpivot{}, fmt.Errorf("unpivot key output ID %q is missing", unpivot.KeyOutputColumnID)
	}
	valueOutput, ok := output[unpivot.ValueOutputColumnID]
	if !ok {
		return recipeplan.SemanticUnpivot{}, fmt.Errorf("unpivot value output ID %q is missing", unpivot.ValueOutputColumnID)
	}
	semanticUnpivot.KeyOutput, semanticUnpivot.KeyLabel = keyOutput.Name, keyOutput.Label
	semanticUnpivot.ValueOutput, semanticUnpivot.ValueLabel = valueOutput.Name, valueOutput.Label
	return semanticUnpivot, nil
}

func reconcileConstructionShapeSchema(declarations []recipe.StageColumn, outputNames map[string]string, computed []CompiledOutputColumn, input map[string]CompiledOutputColumn) ([]CompiledOutputColumn, error) {
	computedByName := make(map[string]CompiledOutputColumn, len(computed))
	for _, column := range computed {
		computedByName[column.Name] = column
	}
	result := make([]CompiledOutputColumn, 0, len(declarations))
	for _, declaration := range declarations {
		computedName := ""
		for inputName, outputName := range outputNames {
			if outputName == declaration.Name {
				computedName = inputName
				break
			}
		}
		if computedName == "" {
			if prior, ok := input[declaration.ID]; ok {
				computedName = prior.Name
			}
		}
		column, ok := computedByName[computedName]
		if !ok {
			return nil, fmt.Errorf("stage output ID %q name %q has no physical shape output", declaration.ID, declaration.Name)
		}
		column.ID, column.Name = declaration.ID, declaration.Name
		column.Label = loomtext.FirstNonBlank(declaration.Label, column.Label, declaration.Name)
		column.Internal, column.Identity = false, false
		result = append(result, column)
	}
	return result, nil
}

func reconcileConstructionUnpivotSchema(declarations []recipe.StageColumn, computed []CompiledOutputColumn, input map[string]CompiledOutputColumn, operation recipe.ConstructionUnpivot) ([]CompiledOutputColumn, error) {
	computedByName := make(map[string]CompiledOutputColumn, len(computed))
	for _, column := range computed {
		computedByName[column.Name] = column
	}
	selected := map[string]bool{}
	for _, item := range operation.Inputs {
		selected[item.ColumnID] = true
	}
	result := make([]CompiledOutputColumn, 0, len(declarations))
	for _, declaration := range declarations {
		var column CompiledOutputColumn
		if declaration.ID == operation.KeyOutputColumnID || declaration.ID == operation.ValueOutputColumnID {
			column, _ = computedByName[declaration.Name]
		} else {
			prior, ok := input[declaration.ID]
			if !ok || selected[declaration.ID] || prior.Internal {
				return nil, fmt.Errorf("unpivot output ID %q is neither a retained input nor a key/value output", declaration.ID)
			}
			column, _ = computedByName[prior.Name]
		}
		if column.Name == "" {
			return nil, fmt.Errorf("unpivot output ID %q name %q has no physical output", declaration.ID, declaration.Name)
		}
		column.ID, column.Name = declaration.ID, declaration.Name
		column.Label = loomtext.FirstNonBlank(declaration.Label, column.Label, declaration.Name)
		column.Internal, column.Identity = false, false
		result = append(result, column)
	}
	for _, column := range computed {
		if column.Internal && column.Name != constructionRowID {
			result = append(result, column)
		}
	}
	return result, nil
}

func constructionUnpivotPreservedOutputs(operation recipe.ConstructionUnpivot, outputs []recipe.StageColumn, input map[string]CompiledOutputColumn) []ir.PhysicalUnpivotOutput {
	selected := map[string]bool{}
	for _, item := range operation.Inputs {
		selected[item.ColumnID] = true
	}
	var result []ir.PhysicalUnpivotOutput
	for _, output := range outputs {
		prior, exists := input[output.ID]
		if exists && !prior.Internal && !selected[output.ID] {
			result = append(result, ir.PhysicalUnpivotOutput{InputColumn: prior.Name, OutputColumn: output.Name})
		}
	}
	internalNames := make([]string, 0)
	for inputID, column := range input {
		if column.Internal && !selected[inputID] && column.Name != constructionRowID {
			internalNames = append(internalNames, column.Name)
		}
	}
	sort.Strings(internalNames)
	for _, name := range internalNames {
		result = append(result, ir.PhysicalUnpivotOutput{InputColumn: name, OutputColumn: name})
	}
	return result
}

func constructionDerivedColumn(derive recipe.ConstructionDerive, output recipe.StageColumn, input map[string]CompiledOutputColumn) (recipe.DerivedColumn, error) {
	convertOperand := func(operand recipe.ConstructionOperand) (recipe.DerivedOperand, error) {
		if operand.Kind == recipe.DerivedColumnOperand {
			column, ok := input[operand.ColumnID]
			if !ok || column.Internal {
				return recipe.DerivedOperand{}, fmt.Errorf("derived input column ID %q is not public", operand.ColumnID)
			}
			return recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: column.Name}, nil
		}
		return recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: operand.Literal}, nil
	}
	left, err := convertOperand(derive.Left)
	if err != nil {
		return recipe.DerivedColumn{}, fmt.Errorf("left operand: %w", err)
	}
	right, err := convertOperand(derive.Right)
	if err != nil {
		return recipe.DerivedColumn{}, fmt.Errorf("right operand: %w", err)
	}
	return recipe.DerivedColumn{
		ConstructionID: derive.ConstructionID, Name: output.Name, Label: output.Label,
		Operation: derive.Operation, Left: left, Right: right,
		MissingInputPolicy: derive.MissingInputPolicy, DivisionByZeroPolicy: derive.DivisionByZeroPolicy,
	}, nil
}

func constructionPhysicalFilter(plan *ir.PhysicalPlan, filter recipe.ConstructionFilter, row string, column CompiledOutputColumn) (ir.PhysicalFilter, error) {
	predicate := ir.PhysicalPredicate{
		Operator: string(filter.Operator), Left: ir.PhysicalValue{Variable: row, Path: []string{column.Name}},
	}
	if filter.Operator != recipe.FilterExists && filter.Operator != recipe.FilterMissing {
		values := make([]any, 0, len(filter.Values))
		for _, value := range filter.Values {
			literal, err := constructionFilterLiteral(value)
			if err != nil {
				return ir.PhysicalFilter{}, err
			}
			values = append(values, literal)
		}
		bindKey := nextTableReshapeBindKey(plan.BindVars, "construction_filter_value")
		if filter.Operator == recipe.FilterIn {
			plan.BindVars[bindKey] = values
		} else {
			plan.BindVars[bindKey] = values[0]
		}
		predicate.Right = &ir.PhysicalValue{BindKey: bindKey}
	}
	return ir.PhysicalFilter{Predicate: predicate}, nil
}

func constructionFilterLiteral(value recipe.FilterValue) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	switch value.Kind {
	case recipe.FilterString:
		return *value.String, nil
	case recipe.FilterCode:
		return value.Code.Code, nil
	case recipe.FilterBoolean:
		return *value.Boolean, nil
	case recipe.FilterInteger:
		return *value.Integer, nil
	case recipe.FilterDecimal:
		return *value.Decimal, nil
	case recipe.FilterDate:
		return *value.Date, nil
	case recipe.FilterDateTime:
		return *value.DateTime, nil
	default:
		return nil, fmt.Errorf("unsupported construction filter value kind %q", value.Kind)
	}
}
