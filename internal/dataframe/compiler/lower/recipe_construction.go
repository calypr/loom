package lower

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const constructionRowID = "__loom_row_id"

// CompiledStageDescriptor is the compiler-owned schema and capability view
// for one exact stage reference. Columns describe the stage output.
type CompiledStageDescriptor struct {
	ID                   string
	InputStageID         string
	Operation            string
	Columns              []CompiledOutputColumn
	RowIdentityColumn    string
	Capabilities         []StageOperationCapability
	RelatedExpandAnchors []CompiledRelatedExpandAnchor
	RelatedExpand        *CompiledRelatedExpandStage
	ActiveRelatedRecord  *CompiledActiveRelatedRecordStage
}

type CompiledRelatedExpandAnchor struct {
	AnchorColumnID string
	Kind           string
	NodeID         string
	ResourceType   string
	Label          string
}

type CompiledRelatedExpandStage struct {
	AnchorColumnID         string
	AnchorColumn           string
	AnchorKind             string
	AnchorNodeID           string
	AnchorResourceType     string
	RelatedRecordColumnID  string
	ParentIdentityColumnID string
	ParentIdentityColumn   string
	TerminalIdentityColumn string
	TargetNodeID           string
	TargetResourceType     string
	Route                  []recipe.ConstructionRelatedRouteStep
}

type CompiledActiveRelatedRecordStage struct {
	TerminalIdentityColumn string
	TargetNodeID           string
	TargetResourceType     string
	Nullable               bool
}

type StageOperationCapability struct {
	Operation  recipe.ConstructionOperationKind
	Supported  bool
	ReasonCode string
	Reason     string
}

// DescribeConstructionSourceStage returns the compiler-owned capability view
// for the exact resolved source schema. It is also used by capability-only
// discovery for a new, zero-column table: the physical compiler has still
// resolved row identity and source columns, but the result is never treated
// as an executable public output.
func DescribeConstructionSourceStage(schema []CompiledOutputColumn, rootResourceType string) (CompiledStageDescriptor, error) {
	identity := constructionSourceIdentity(schema)
	if identity == "" {
		return CompiledStageDescriptor{}, fmt.Errorf("construction source has no supported row identity projection")
	}
	columns := cloneCompiledSchema(schema)
	identityFound := false
	for index := range columns {
		if columns[index].Name == identity {
			columns[index].ID = columns[index].Name
			columns[index].Internal = true
			columns[index].Identity = true
			identityFound = true
			continue
		}
		if !columns[index].Internal && strings.TrimSpace(columns[index].ID) == "" {
			return CompiledStageDescriptor{}, fmt.Errorf("construction source column %q has no stable compiled ID", columns[index].Name)
		}
	}
	if !identityFound {
		return CompiledStageDescriptor{}, fmt.Errorf("construction source row identity %q is missing from its compiled schema", identity)
	}
	descriptor := CompiledStageDescriptor{
		ID: recipe.ConstructionSourceProjectionID, Operation: "SOURCE_PROJECTION",
		Columns: columns, RowIdentityColumn: identity,
	}
	descriptor.Capabilities = stageCapabilities(columns)
	descriptor.RelatedExpandAnchors = relatedExpandAnchors(columns, rootResourceType)
	return descriptor, nil
}

type constructionStageResult struct {
	physical   ir.PhysicalConstructionStage
	schema     []CompiledOutputColumn
	identity   string
	descriptor CompiledStageDescriptor
}

func appendRecipeConstructionStages(plan *ir.PhysicalPlan, outputName, rootResourceType string, construction recipe.Construction, sourceSchema []CompiledOutputColumn, policy ir.PhysicalOptimizationPolicy) ([]CompiledOutputColumn, []CompiledStageDescriptor, string, error) {
	if plan == nil {
		return nil, nil, "", fmt.Errorf("physical plan is required")
	}
	if step, ok := construction.TerminalCombineStep(); ok {
		return appendRecipeTerminalCombine(plan, step)
	}
	if len(construction.SourceColumns) == 0 {
		return nil, nil, "", fmt.Errorf("construction source schema must be supplied by the resolved source compiler")
	}
	resolvedSource, err := resolveConstructionSourceSchema(plan, construction.SourceColumns, sourceSchema, outputName)
	if err != nil {
		return nil, nil, "", err
	}
	sourceIdentity := constructionSourceIdentity(resolvedSource)
	if sourceIdentity == "" {
		return nil, nil, "", fmt.Errorf("construction source has no supported row identity projection")
	}
	sourcePhysicalColumns := toPhysicalStageColumns(resolvedSource)
	sequence := &ir.PhysicalStageSequence{
		SourceStageID:     recipe.ConstructionSourceProjectionID,
		SourceRowIdentity: sourceIdentity,
		SourceColumns:     sourcePhysicalColumns,
	}
	descriptors := []CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID, Operation: "SOURCE_PROJECTION",
		Columns: cloneCompiledSchema(resolvedSource), RowIdentityColumn: sourceIdentity,
	}}
	descriptors[0].Capabilities = stageCapabilities(resolvedSource)
	descriptors[0].RelatedExpandAnchors = relatedExpandAnchors(resolvedSource, rootResourceType)
	if len(construction.Steps) == 0 {
		return resolvedSource, descriptors, sourceIdentity, nil
	}
	usedVariables := physicalPlanVariables(plan.Operations)
	priorSchema, priorIdentity := resolvedSource, sourceIdentity
	priorStageID := recipe.ConstructionSourceProjectionID
	for index, step := range construction.Steps {
		result, stageErr := lowerConstructionStep(plan, step, priorStageID, priorSchema, priorIdentity, rootResourceType, policy, usedVariables, index)
		if stageErr != nil {
			return nil, nil, "", fmt.Errorf("construction step %q: %w", step.ID, stageErr)
		}
		sequence.Stages = append(sequence.Stages, result.physical)
		descriptors = append(descriptors, result.descriptor)
		priorStageID, priorSchema, priorIdentity = step.ID, result.schema, result.identity
	}
	sequence.FinalStageID = priorStageID
	sequence.FinalRowIdentity = priorIdentity
	sequence.FinalColumns = toPhysicalStageColumns(priorSchema)
	sequence.PreviewSourceWindowByRootID = constructionRootIDPivotFastPathEligible(plan, sequence, descriptors, rootResourceType)
	if sequence.PreviewSourceWindowByRootID {
		sequence.Stages[0].GroupedPivot.OneInputRowPerGroup = true
	}
	plan.StageSequence = sequence
	if err := plan.Validate(); err != nil {
		return nil, nil, "", fmt.Errorf("validate typed construction sequence: %w", err)
	}
	return priorSchema, descriptors, priorIdentity, nil
}

func constructionRootIDPivotFastPathEligible(plan *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, descriptors []CompiledStageDescriptor, rootResourceType string) bool {
	if plan == nil || sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 1 || len(descriptors) != 2 {
		return false
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil || len(stage.GroupedPivot.GroupKeys) == 0 {
		return false
	}
	if !constructionRootIDPivotSourceEligible(plan, rootResourceType) {
		return false
	}
	return groupedPivotHasDirectRootIDKey(plan, stage.GroupedPivot.GroupKeys, descriptors[0].Columns, rootResourceType)
}

func constructionRootIDPivotSourceEligible(plan *ir.PhysicalPlan, rootResourceType string) bool {
	if len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return false
	}
	if plan.Source.ResourceType != rootResourceType || plan.Operations[0].Source.ResourceType != rootResourceType {
		return false
	}
	returns := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return false
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		case ir.PhysicalReturnOp:
			returns++
			if index != len(plan.Operations)-1 {
				return false
			}
		default:
			// Traversals, expansions, and other row-shaping operations must
			// remain ahead of any source limit.
			return false
		}
	}
	return returns == 1
}

func groupedPivotHasDirectRootIDKey(plan *ir.PhysicalPlan, keys []ir.PhysicalGroupedPivotKey, schema []CompiledOutputColumn, rootResourceType string) bool {
	for _, key := range keys {
		column, ok := schemaColumn(schema, key.Column)
		if !ok || column.Internal || column.Kind != string(expression.KindString) || column.Cardinality == string(expression.Many) {
			continue
		}
		// The lowered source projection is authoritative. Catalog fieldRef values
		// can give a direct FHIR id an opaque semantic identity.
		if constructionPreviewProjectionIsRootID(plan, column.Name, rootResourceType) {
			return true
		}
	}
	return false
}

func constructionPreviewProjectionIsRootID(plan *ir.PhysicalPlan, columnName, rootResourceType string) bool {
	found := false
	root := plan.Operations[0].RootScan.Variable
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != columnName || found {
				continue
			}
			if projection.Expression == nil {
				if projection.Value.Variable != root || len(projection.Value.Path) != 2 || projection.Value.Path[0] != "payload" || projection.Value.Path[1] != "id" {
					return false
				}
			} else {
				physical := projection.Expression
				if physical.Kind != ir.PhysicalExtractExpression || physical.Cardinality != ir.PhysicalScalarCardinality || physical.Extract == nil {
					return false
				}
				extract := physical.Extract
				if extract.ResourceType != rootResourceType || extract.Source.Variable != root ||
					len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" ||
					len(extract.Selector.Steps) != 1 || extract.Selector.Filter != nil || len(extract.Fallbacks) != 0 || extract.Distinct {
					return false
				}
				step := extract.Selector.Steps[0]
				if step.Field != "id" || step.Iterate || step.Index != nil {
					return false
				}
			}
			found = true
		}
	}
	return found
}

func resolveConstructionSourceSchema(plan *ir.PhysicalPlan, declarations []recipe.StageColumn, schema []CompiledOutputColumn, outputName string) ([]CompiledOutputColumn, error) {
	projections := map[string]ir.PhysicalProjection{}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Hidden {
				continue
			}
			projections[projection.Name] = projection
		}
	}
	schemaByName := make(map[string]CompiledOutputColumn, len(schema))
	publicCount := 0
	for _, column := range schema {
		if column.Internal {
			continue
		}
		publicCount++
		schemaByName[column.Name] = column
	}
	if len(declarations) != publicCount {
		return nil, fmt.Errorf("construction source schema declares %d columns but resolved source emits %d public columns", len(declarations), publicCount)
	}
	resolved := make([]CompiledOutputColumn, 0, len(declarations)+1)
	seenIDs, seenNames := map[string]bool{}, map[string]bool{}
	for index, declaration := range declarations {
		if strings.TrimSpace(declaration.ID) == "" || declaration.ID != strings.TrimSpace(declaration.ID) || seenIDs[declaration.ID] {
			return nil, fmt.Errorf("construction source column %d has an empty, untrimmed, or duplicate ID %q", index, declaration.ID)
		}
		if strings.TrimSpace(declaration.Name) == "" || seenNames[declaration.Name] {
			return nil, fmt.Errorf("construction source column %d has an empty or duplicate resolved public name %q", index, declaration.Name)
		}
		compiled, exists := schemaByName[declaration.Name]
		if !exists {
			return nil, fmt.Errorf("construction source column %q does not match an exact public source output", declaration.Name)
		}
		if _, exists := projections[declaration.Name]; !exists {
			return nil, fmt.Errorf("construction source column %q has no public physical projection", declaration.Name)
		}
		compiled.ID, compiled.Name = declaration.ID, declaration.Name
		compiled.Label = constructionFirstNonEmpty(declaration.Label, compiled.Label, declaration.Name)
		compiled.SourceChild = lineage.CloneSourceChild(declaration.SourceChild)
		resolved = append(resolved, compiled)
		seenIDs[declaration.ID], seenNames[declaration.Name] = true, true
	}
	if len(seenNames) != len(schemaByName) {
		return nil, fmt.Errorf("construction source schema for output %q omits a resolved public source column", outputName)
	}
	for name, column := range schemaByName {
		if !seenNames[name] {
			return nil, fmt.Errorf("construction source schema does not map public source column %q (logical type %s)", name, column.Kind)
		}
	}
	identity := constructionSourceIdentity(schema)
	if identity == "" {
		return nil, fmt.Errorf("construction source has no supported row identity projection")
	}
	identityColumn, ok := schemaColumn(schema, identity)
	if !ok {
		return nil, fmt.Errorf("construction source row identity %q is missing from its compiled schema", identity)
	}
	identityColumn.ID = identityColumn.Name
	identityColumn.Internal, identityColumn.Identity = true, true
	resolved = append(resolved, identityColumn)
	return resolved, nil
}

func constructionSourceIdentity(schema []CompiledOutputColumn) string {
	for _, preferred := range []string{constructionRowID, "__loom_expansion_identity", "_key"} {
		if column, ok := schemaColumn(schema, preferred); ok && column.Internal && column.Identity {
			return preferred
		}
	}
	return ""
}

func schemaColumn(schema []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Name == name {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func lowerConstructionStep(plan *ir.PhysicalPlan, step recipe.ConstructionStep, inputStageID string, inputSchema []CompiledOutputColumn, inputIdentity, rootResourceType string, policy ir.PhysicalOptimizationPolicy, usedVariables map[string]bool, index int) (constructionStageResult, error) {
	inputByID := compiledSchemaByID(inputSchema)
	outputByID := make(map[string]recipe.StageColumn, len(step.Outputs))
	for _, column := range step.Outputs {
		outputByID[column.ID] = column
	}
	inputRow := allocateConstructionVariable(usedVariables, "input", index)
	outputRow := allocateConstructionVariable(usedVariables, "output", index)
	inputColumns := toPhysicalStageColumns(inputSchema)
	inputProjections := stageInputProjections(inputSchema, inputRow)
	base := ir.PhysicalConstructionStage{
		ID: step.ID, InputStageID: inputStageID, InputRowVariable: inputRow,
		OutputRowVariable: outputRow, InputColumns: inputColumns,
		InputProjections: inputProjections,
	}
	publicInput := publicCompiledSchema(inputSchema)
	var outputSchema []CompiledOutputColumn
	var outputIdentity string
	switch step.Operation.Kind {
	case recipe.ConstructionDeriveOp:
		derive := step.Operation.Derive
		column, ok := outputByID[derive.OutputColumnID]
		if !ok {
			return constructionStageResult{}, fmt.Errorf("derived output ID %q is missing from step schema", derive.OutputColumnID)
		}
		recipeColumn, err := constructionDerivedColumn(*derive, column, inputByID)
		if err != nil {
			return constructionStageResult{}, err
		}
		temporary := ir.PhysicalPlan{BindVars: plan.BindVars, Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: inputProjections}}}}
		derivedTypes, err := appendRecipeDerivedColumnsWithVariables(&temporary, []recipe.DerivedColumn{recipeColumn}, inputSchema, usedVariables)
		if err != nil {
			return constructionStageResult{}, err
		}
		derivedProjection, found := findProjection(temporary.Operations, column.Name)
		if !found {
			return constructionStageResult{}, fmt.Errorf("derived output %q has no physical projection", column.Name)
		}
		base.Kind = ir.PhysicalStageDeriveOp
		for _, operation := range temporary.Operations {
			if operation.Kind != ir.PhysicalReturnOp {
				base.DerivedLets = append(base.DerivedLets, operation)
			}
		}
		outputSchema = make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
		for _, declaration := range step.Outputs {
			if prior, exists := inputByID[declaration.ID]; exists {
				prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
				outputSchema = append(outputSchema, prior)
				continue
			}
			if declaration.ID != derive.OutputColumnID {
				return constructionStageResult{}, fmt.Errorf("derive output schema contains unexpected new column ID %q", declaration.ID)
			}
			metadata := derivedTypes[recipeColumn.Name]
			outputSchema = append(outputSchema, CompiledOutputColumn{
				ID: declaration.ID, Name: declaration.Name, Label: constructionFirstNonEmpty(declaration.Label, declaration.Name),
				SemanticPath: "derived:" + derive.ConstructionID, Kind: string(metadata.Type.Kind),
				Cardinality: string(metadata.Type.Cardinality), Nullable: metadata.Type.Cardinality.Optional(),
				NormalizedUnit: cloneUnitIdentity(metadata.NormalizedUnit),
			})
		}
		base.OutputProjections = stagePassThroughAndDerivedProjections(step.Outputs, inputByID, inputRow, column.ID, column.Name, derivedProjection)
		outputIdentity = inputIdentity
	case recipe.ConstructionFilterOp:
		filter := step.Operation.Filter
		inputColumn, ok := inputByID[filter.ColumnID]
		if !ok || inputColumn.Internal {
			return constructionStageResult{}, fmt.Errorf("filter column ID %q is not a public input", filter.ColumnID)
		}
		physicalFilter, err := constructionPhysicalFilter(plan, *filter, inputRow, inputColumn)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.Filter = ir.PhysicalStageFilterOp, &physicalFilter
		outputSchema = make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
		for _, declaration := range step.Outputs {
			prior, exists := inputByID[declaration.ID]
			if !exists || prior.Internal {
				return constructionStageResult{}, fmt.Errorf("filter output ID %q is not a public input column", declaration.ID)
			}
			prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
			outputSchema = append(outputSchema, prior)
		}
		base.OutputProjections = stagePassThroughProjections(step.Outputs, inputByID, inputRow)
		outputIdentity = inputIdentity
	case recipe.ConstructionPivotOp:
		pivot := step.Operation.Pivot
		if pivot.UnlistedCategoryPolicy == recipe.PivotUnlistedCategoryExcludeWithEvidence {
			return constructionStageResult{}, fmt.Errorf("EXCLUDE_WITH_EVIDENCE pivot policy is not available inside a construction sequence")
		}
		semanticPivot, outputNames, err := constructionSemanticPivot(*pivot, inputByID, outputByID)
		if err != nil {
			return constructionStageResult{}, err
		}
		nameSchema, nameProjections := compiledSchemaByName(publicInput), stageProjectionMap(inputProjections)
		physicalPivot, projections, compiled, err := lowerRecipeGroupedPivot(plan, semanticPivot, inputProjections, nameSchema, nameProjections, usedVariables)
		if err != nil {
			return constructionStageResult{}, err
		}
		for index := range physicalPivot.GroupKeys {
			outputName := outputNames[physicalPivot.GroupKeys[index].Column]
			physicalPivot.GroupKeys[index].Output = outputName
		}
		for index := range physicalPivot.Categories {
			physicalPivot.Categories[index].Output = outputNames[physicalPivot.Categories[index].Output]
		}
		base.Kind, base.GroupedPivot = ir.PhysicalStagePivotOp, &physicalPivot
		base.OutputRowVariable = physicalPivot.OutputRowVariable
		base.OutputProjections = projections
		outputSchema, err = reconcileConstructionShapeSchema(step.Outputs, outputNames, compiled, inputByID)
		if err != nil {
			return constructionStageResult{}, err
		}
		outputIdentity = constructionRowID
	case recipe.ConstructionUnpivotOp:
		unpivot := step.Operation.Unpivot
		semanticUnpivot, err := constructionSemanticUnpivot(*unpivot, inputByID, outputByID)
		if err != nil {
			return constructionStageResult{}, err
		}
		nameProjections := stageProjectionMap(inputProjections)
		identity := spec.RowIdentity{Grain: spec.RowGrainPatient, Fields: []string{inputIdentity}}
		physicalUnpivot, projections, compiled, err := lowerRecipeUnpivot(plan, semanticUnpivot, &identity, inputProjections, compiledSchemaByName(inputSchema), nameProjections, usedVariables)
		if err != nil {
			return constructionStageResult{}, err
		}
		physicalUnpivot.PreservedOutputs = constructionUnpivotPreservedOutputs(*unpivot, step.Outputs, inputByID)
		base.Kind, base.Unpivot = ir.PhysicalStageUnpivotOp, &physicalUnpivot
		base.OutputRowVariable = physicalUnpivot.OutputRowVariable
		base.OutputProjections = projections
		outputSchema, err = reconcileConstructionUnpivotSchema(step.Outputs, compiled, inputByID, *unpivot)
		if err != nil {
			return constructionStageResult{}, err
		}
		outputIdentity = constructionRowID
	case recipe.ConstructionGroupOp:
		group := step.Operation.Group
		physicalGroup, projections, compiled, err := lowerConstructionGroup(plan, *group, step.Outputs, inputByID, outputByID, usedVariables, index)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.Group = ir.PhysicalStageGroupOp, &physicalGroup
		base.OutputProjections = projections
		outputSchema, outputIdentity = compiled, constructionRowID
	case recipe.ConstructionExpandOp:
		expand := step.Operation.Expand
		physicalExpand, projections, compiled, err := lowerConstructionExpand(plan, *expand, step.Outputs, inputByID, outputByID, inputRow, usedVariables, index)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.Expand = ir.PhysicalStageExpandOp, &physicalExpand
		base.OutputProjections = projections
		outputSchema, outputIdentity = compiled, constructionRowID
	case recipe.ConstructionRelatedSourceOp:
		related := step.Operation.RelatedSource
		if related == nil {
			return constructionStageResult{}, fmt.Errorf("related source payload is required")
		}
		projections, compiled, err := lowerConstructionRelatedSource(plan, step, *related, inputByID, outputByID, inputRow, inputIdentity, rootResourceType, policy, usedVariables, index)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.OutputProjections = ir.PhysicalStageRelatedSourceOp, projections
		base.RelatedSource = &ir.PhysicalStageRelatedSource{
			AnchorColumnID: related.AnchorColumnID, OutputColumnID: related.OutputColumnID,
			CandidateID: related.Source.CandidateID, SourceOccurrenceID: related.SourceOccurrenceID,
			ResourceType: related.Source.ResourceType, Path: related.Source.Path, LogicalType: related.Source.LogicalType,
			Form: related.Form, ContributorPolicy: related.ContributorPolicy,
		}
		outputSchema, outputIdentity = compiled, inputIdentity
	case recipe.ConstructionRelatedExpandOp:
		related := step.Operation.RelatedExpand
		if related == nil {
			return constructionStageResult{}, fmt.Errorf("related expansion payload is required")
		}
		physicalExpand, projections, compiled, err := lowerConstructionRelatedExpand(
			plan, step, *related, inputByID, outputByID, inputRow, inputIdentity,
			rootResourceType, policy, usedVariables, index,
		)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.RelatedExpand = ir.PhysicalStageRelatedExpandOp, &physicalExpand
		base.OutputProjections = projections
		outputSchema, outputIdentity = compiled, constructionRowID
	case recipe.ConstructionRelatedFieldOp:
		related := step.Operation.RelatedField
		if related == nil {
			return constructionStageResult{}, fmt.Errorf("related field payload is required")
		}
		physicalRelatedField, projections, compiled, err := lowerConstructionRelatedField(step, *related, inputByID, outputByID, inputSchema, inputRow)
		if err != nil {
			return constructionStageResult{}, err
		}
		base.Kind, base.RelatedField, base.OutputProjections = ir.PhysicalStageRelatedFieldOp, &physicalRelatedField, projections
		outputSchema, outputIdentity = compiled, inputIdentity
	default:
		return constructionStageResult{}, fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
	if outputIdentity == "" {
		return constructionStageResult{}, fmt.Errorf("operation did not produce a row identity")
	}
	if base.Kind != ir.PhysicalStageRelatedExpandOp && preservesActiveRelatedRecord(base.Kind) {
		if rootAnchor, ok := retainedRootResourceAnchor(inputSchema); ok && rootAnchor.Name != outputIdentity {
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
	outputSchema = append(outputSchema, constructionIdentitySchema(outputIdentity, outputSchema, inputSchema))
	if base.Kind == ir.PhysicalStageDeriveOp || base.Kind == ir.PhysicalStageFilterOp || base.Kind == ir.PhysicalStageRelatedSourceOp || base.Kind == ir.PhysicalStageRelatedFieldOp {
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
	base.InputColumns = inputColumns
	base.OutputColumns = toPhysicalStageColumns(outputSchema)
	if base.Kind == ir.PhysicalStageDeriveOp || base.Kind == ir.PhysicalStageFilterOp || base.Kind == ir.PhysicalStageRelatedSourceOp || base.Kind == ir.PhysicalStageRelatedFieldOp {
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
	descriptor.Capabilities = stageCapabilities(outputSchema)
	descriptor.RelatedExpandAnchors = relatedExpandAnchors(outputSchema, rootResourceType)
	return constructionStageResult{physical: base, schema: outputSchema, identity: outputIdentity, descriptor: descriptor}, nil
}

func lowerConstructionRelatedSource(
	plan *ir.PhysicalPlan,
	step recipe.ConstructionStep,
	related recipe.ConstructionRelatedSource,
	inputByID map[string]CompiledOutputColumn,
	outputByID map[string]recipe.StageColumn,
	inputRow, inputIdentity, rootResourceType string,
	policy ir.PhysicalOptimizationPolicy,
	usedVariables map[string]bool,
	index int,
) ([]ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if related.AnchorColumnID != inputIdentity || inputIdentity != "_key" {
		return nil, nil, fmt.Errorf("related source anchor must be the retained root document row identity")
	}
	anchor, ok := inputByID[related.AnchorColumnID]
	if !ok || !anchor.Internal || !anchor.Identity || anchor.Name != "_key" || rootResourceType != plan.Source.ResourceType {
		return nil, nil, fmt.Errorf("related source anchor is not the compiler-proven root document identity")
	}
	if related.Source.Cardinality != "optional_one" && related.Source.Cardinality != "required_one" {
		return nil, nil, fmt.Errorf("related source field must be scalar for ALL_MATCHES")
	}
	if related.SourceOccurrenceID != related.Source.NodeID || len(related.Route) == 0 {
		return nil, nil, fmt.Errorf("related source must persist its exact source occurrence and route")
	}
	if _, scalar := tableReshapeScalarKind(related.Source.LogicalType); !scalar {
		return nil, nil, fmt.Errorf("related source logical type %q is not scalar", related.Source.LogicalType)
	}
	selectorPath := strings.TrimPrefix(related.Source.Path, related.Source.ResourceType+".")
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		return nil, nil, fmt.Errorf("related source field path: %w", err)
	}

	rootVariable := allocateConstructionVariable(usedVariables, fmt.Sprintf("related_%d_root", index), index)
	rootNode := semantic.SemanticNode{Alias: plan.Source.SemanticNode, ResourceType: rootResourceType}
	subplan := ir.PhysicalSubplan{
		Captures: []string{inputRow},
		Operations: []ir.PhysicalOperation{{
			Kind:           ir.PhysicalCollectionScanOp,
			Source:         ir.PhysicalSource{SemanticNode: rootNode.Alias, ResourceType: rootResourceType},
			CollectionScan: &ir.PhysicalCollectionScan{Variable: rootVariable, CollectionBindKey: "root_collection"},
		}},
	}
	subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{SemanticNode: rootNode.Alias, ResourceType: rootResourceType, SemanticField: "_key"},
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: rootVariable, Path: []string{"_key"}},
			Right: &ir.PhysicalValue{Variable: inputRow, Path: []string{inputIdentity}},
		}},
	})
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
		targetVariable := allocateConstructionVariable(usedVariables, prefix+"_target", index)
		edgeVariable := allocateConstructionVariable(usedVariables, prefix+"_edge", index)
		traversal, traversalErr := BuildPhysicalTraversal(TraversalLoweringRequest{
			FromType: currentResource, EdgeLabel: hop.Relationship, ToType: hop.ToResourceType,
			SourceVariable: currentVariable, TargetVariable: targetVariable, EdgeVariable: edgeVariable,
			BindPrefix: prefix, Policy: policy,
		})
		if traversalErr != nil {
			return nil, nil, fmt.Errorf("related source route hop %d: %w", routeIndex, traversalErr)
		}
		if strings.ToUpper(string(traversal.Traversal.Direction)) != hop.StorageDirection {
			return nil, nil, fmt.Errorf("related source route hop %d storage direction changed", routeIndex)
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
		subplan.Operations = appendAuthScope(subplan.Operations, []ir.PhysicalValue{{Variable: edgeVariable, Path: []string{"auth_resource_path"}}, {Variable: targetVariable, Path: []string{"auth_resource_path"}}}, prefix+"_scope_allowed", child)
		currentVariable, currentResource = targetVariable, hop.ToResourceType
	}
	if currentResource != related.Source.ResourceType || related.Route[len(related.Route)-1].ToNodeID != related.Source.NodeID {
		return nil, nil, fmt.Errorf("related source route does not end at the exact source occurrence")
	}
	fieldExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: currentVariable, Path: []string{"payload"}}, ResourceType: related.Source.ResourceType, Selector: selector, ExecutionMode: selectorExecutionMode(related.Source.ResourceType, selector)},
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
			plan.BindVars[bindKey] = literal
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
	} else {
		identity := ir.PhysicalValue{Variable: currentVariable, Path: []string{"_id"}}
		subplan.Return = ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &identity}
		subplan.Sort = &identity
		subplan.Unique = true
	}
	matchingValues := ir.PhysicalExpression{Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Subplan: &subplan}
	outputExpression := matchingValues
	if related.Form == "COUNT" || related.Form == "PRESENCE" {
		outputExpression = ir.PhysicalExpression{
			Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Call: &ir.PhysicalCall{Name: "length", Args: []ir.PhysicalExpression{matchingValues}},
		}
		outputKind, outputCardinality, outputNullable = "integer", expression.RequiredOne, false
		if related.Form == "PRESENCE" {
			zeroKey := fmt.Sprintf("related_%d_zero", index)
			plan.BindVars[zeroKey] = 0
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
			prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
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
			ID: declaration.ID, Name: declaration.Name, Label: constructionFirstNonEmpty(declaration.Label, declaration.Name),
			SemanticPath: "related_source:" + related.Source.NodeID + "." + selectorPath,
			Kind:         outputKind, Cardinality: string(outputCardinality), Nullable: outputNullable,
		})
		projections = append(projections, ir.PhysicalProjection{Name: declaration.Name, Expression: &outputExpression})
	}
	return projections, outputSchema, nil
}

func constructionSemanticPivot(pivot recipe.ConstructionPivot, input map[string]CompiledOutputColumn, output map[string]recipe.StageColumn) (semanticPivot semantic.SemanticGroupedPivot, outputNames map[string]string, err error) {
	name := func(id string) (string, error) {
		column, ok := input[id]
		if !ok || column.Internal {
			return "", fmt.Errorf("pivot input column ID %q is not public", id)
		}
		return column.Name, nil
	}
	semanticPivot = semantic.SemanticGroupedPivot{
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
		semanticPivot.Categories = append(semanticPivot.Categories, semantic.SemanticGroupedPivotCategory{Key: category.Key, Output: column.Name, Label: column.Label})
		outputNames[column.Name] = column.Name
	}
	return semanticPivot, outputNames, nil
}

func constructionSemanticUnpivot(unpivot recipe.ConstructionUnpivot, input map[string]CompiledOutputColumn, output map[string]recipe.StageColumn) (semantic.SemanticUnpivot, error) {
	semanticUnpivot := semantic.SemanticUnpivot{
		ConstructionID: unpivot.ConstructionID, NullRowPolicy: unpivot.NullRowPolicy,
	}
	for _, item := range unpivot.Inputs {
		column, ok := input[item.ColumnID]
		if !ok || column.Internal {
			return semantic.SemanticUnpivot{}, fmt.Errorf("unpivot input column ID %q is not public", item.ColumnID)
		}
		semanticUnpivot.Inputs = append(semanticUnpivot.Inputs, semantic.SemanticUnpivotInput{Column: column.Name, Key: item.Key})
	}
	keyOutput, ok := output[unpivot.KeyOutputColumnID]
	if !ok {
		return semantic.SemanticUnpivot{}, fmt.Errorf("unpivot key output ID %q is missing", unpivot.KeyOutputColumnID)
	}
	valueOutput, ok := output[unpivot.ValueOutputColumnID]
	if !ok {
		return semantic.SemanticUnpivot{}, fmt.Errorf("unpivot value output ID %q is missing", unpivot.ValueOutputColumnID)
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
		column.Label = constructionFirstNonEmpty(declaration.Label, column.Label, declaration.Name)
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
		column.Label = constructionFirstNonEmpty(declaration.Label, column.Label, declaration.Name)
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

func stageCapabilities(columns []CompiledOutputColumn) []StageOperationCapability {
	public := publicCompiledSchema(columns)
	numeric, scalar, unpivotPairs, arrays := 0, 0, false, 0
	rootKey, rootRowIdentity := false, false
	_, activeRelatedRecord := activeRelatedRecordColumn(columns)
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			rootKey = true
			rootRowIdentity = column.Identity
		}
	}
	for index, column := range public {
		if column.Cardinality == string(expression.Many) {
			arrays++
			continue
		}
		if _, ok := tableReshapeScalarKind(column.Kind); ok {
			scalar++
			for _, other := range public[index+1:] {
				if other.Cardinality != string(expression.Many) {
					left, lok := tableReshapeScalarKind(column.Kind)
					right, rok := tableReshapeScalarKind(other.Kind)
					if lok && rok && constructionScalarKindsCompatible(left, right) {
						unpivotPairs = true
					}
				}
			}
		}
		if column.Kind == string(expression.KindInteger) || column.Kind == string(expression.KindDecimal) {
			numeric++
		}
	}
	capability := func(operation recipe.ConstructionOperationKind, supported bool, code, reason string) StageOperationCapability {
		return StageOperationCapability{Operation: operation, Supported: supported, ReasonCode: code, Reason: reason}
	}
	return []StageOperationCapability{
		capability(recipe.ConstructionDeriveOp, numeric > 0, "NO_NUMERIC_COLUMN", "derive requires at least one public scalar numeric column or a numeric literal operand"),
		capability(recipe.ConstructionFilterOp, len(public) > 0, "NO_PUBLIC_COLUMNS", "filter requires a public output column"),
		capability(recipe.ConstructionPivotOp, scalar >= 3, "INSUFFICIENT_SCALAR_COLUMNS", "pivot requires public scalar group, category, and value columns"),
		capability(recipe.ConstructionUnpivotOp, unpivotPairs, "NO_COMPATIBLE_UNPIVOT_COLUMNS", "unpivot requires at least two public scalar columns with compatible types"),
		capability(recipe.ConstructionGroupOp, len(public) > 0, "NO_PUBLIC_COLUMNS", "group requires at least one public column or row-count input"),
		capability(recipe.ConstructionExpandOp, arrays > 0, "NO_ARRAY_COLUMNS", "expand requires a public array-valued column"),
		capability(recipe.ConstructionRelatedSourceOp, rootRowIdentity, "NO_SOURCE_ROW_ANCHOR", "related source requires the root document identity to survive this stage"),
		capability(recipe.ConstructionRelatedExpandOp, rootKey || activeRelatedRecord, "NO_SOURCE_ROW_ANCHOR", "related expansion requires a retained root key or exact related-record identity"),
		capability(recipe.ConstructionRelatedFieldOp, activeRelatedRecord, "NO_ACTIVE_RELATED_RECORD", "related field requires the exact terminal resource identity to survive this stage"),
	}
}

func relatedExpandAnchors(schema []CompiledOutputColumn, rootResourceType string) []CompiledRelatedExpandAnchor {
	anchors := make([]CompiledRelatedExpandAnchor, 0, 2)
	if _, retained := retainedRootResourceAnchor(schema); retained && rootResourceType != "" {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: "_key", Kind: "root", ResourceType: rootResourceType,
			Label: "Original " + rootResourceType,
		})
	}
	if active, ok := activeRelatedRecordColumn(schema); ok {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: active.Name, Kind: "activeRelatedRecord",
			NodeID: active.RelatedRecordAnchor.TargetNodeID, ResourceType: active.RelatedRecordAnchor.TargetResourceType,
			Label: "Current related " + active.RelatedRecordAnchor.TargetResourceType,
		})
	}
	return anchors
}

func retainedRootResourceAnchor(schema []CompiledOutputColumn) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func activeRelatedRecordColumn(schema []CompiledOutputColumn) (CompiledOutputColumn, bool) {
	var active CompiledOutputColumn
	for _, column := range schema {
		if column.RelatedRecordAnchor == nil {
			continue
		}
		if active.Name != "" || !column.Internal || column.Identity || column.Kind != string(expression.KindString) ||
			(column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne)) {
			return CompiledOutputColumn{}, false
		}
		active = column
	}
	return active, active.Name != ""
}

func preservesActiveRelatedRecord(operation ir.PhysicalStageOperationKind) bool {
	switch operation {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func stageInputProjections(schema []CompiledOutputColumn, row string) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(schema))
	for _, column := range schema {
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: row, Path: []string{column.Name}},
		})
	}
	return projections
}

func stagePassThroughProjections(outputs []recipe.StageColumn, input map[string]CompiledOutputColumn, row string) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(outputs))
	for _, output := range outputs {
		prior := input[output.ID]
		projections = append(projections, ir.PhysicalProjection{
			Name: output.Name, Value: ir.PhysicalValue{Variable: row, Path: []string{prior.Name}},
			Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: row}, Paths: [][]string{{prior.Name}},
			},
		})
	}
	return projections
}

func stagePassThroughAndDerivedProjections(outputs []recipe.StageColumn, input map[string]CompiledOutputColumn, row, derivedID, derivedName string, derived ir.PhysicalProjection) []ir.PhysicalProjection {
	projections := make([]ir.PhysicalProjection, 0, len(outputs))
	for _, output := range outputs {
		if output.ID == derivedID {
			derived.Name = derivedName
			projections = append(projections, derived)
			continue
		}
		prior := input[output.ID]
		projections = append(projections, ir.PhysicalProjection{
			Name: output.Name, Value: ir.PhysicalValue{Variable: row, Path: []string{prior.Name}},
			Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: row}, Paths: [][]string{{prior.Name}},
			},
		})
	}
	return projections
}

func stageProjectionMap(projections []ir.PhysicalProjection) map[string]ir.PhysicalProjection {
	result := make(map[string]ir.PhysicalProjection, len(projections))
	for _, projection := range projections {
		result[projection.Name] = projection
	}
	return result
}

func compiledSchemaByID(schema []CompiledOutputColumn) map[string]CompiledOutputColumn {
	result := make(map[string]CompiledOutputColumn, len(schema))
	for _, column := range schema {
		result[column.ID] = column
	}
	return result
}

func compiledSchemaByName(schema []CompiledOutputColumn) map[string]CompiledOutputColumn {
	result := make(map[string]CompiledOutputColumn, len(schema))
	for _, column := range schema {
		result[column.Name] = column
	}
	return result
}

func publicCompiledSchema(schema []CompiledOutputColumn) []CompiledOutputColumn {
	result := make([]CompiledOutputColumn, 0, len(schema))
	for _, column := range schema {
		if !column.Internal {
			result = append(result, column)
		}
	}
	return result
}

func toPhysicalStageColumns(schema []CompiledOutputColumn) []ir.PhysicalStageColumn {
	result := make([]ir.PhysicalStageColumn, 0, len(schema))
	for _, column := range schema {
		var relatedAnchor *ir.PhysicalStageRelatedRecordAnchor
		if column.RelatedRecordAnchor != nil {
			relatedAnchor = &ir.PhysicalStageRelatedRecordAnchor{
				NodeID: column.RelatedRecordAnchor.TargetNodeID, ResourceType: column.RelatedRecordAnchor.TargetResourceType,
			}
		}
		result = append(result, ir.PhysicalStageColumn{
			ID: column.ID, Name: column.Name, Label: column.Label,
			Kind: column.Kind, Cardinality: column.Cardinality, Nullable: column.Nullable,
			Internal: column.Internal, Identity: column.Identity,
			RelatedRecordAnchor: relatedAnchor,
			NormalizedUnit:      cloneUnitIdentity(column.NormalizedUnit),
		})
	}
	return result
}

func constructionIdentitySchema(identity string, output, input []CompiledOutputColumn) CompiledOutputColumn {
	if column, ok := schemaColumn(input, identity); ok {
		column.ID, column.Name = identity, identity
		column.Internal, column.Identity = true, true
		return column
	}
	for _, column := range output {
		if column.Name == identity {
			column.ID, column.Internal, column.Identity = identity, true, true
			return column
		}
	}
	return CompiledOutputColumn{
		ID: identity, Name: identity, Label: identity, SemanticPath: "construction:row_identity",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
	}
}

func findProjection(operations []ir.PhysicalOperation, name string) (ir.PhysicalProjection, bool) {
	for _, operation := range operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == name {
				return projection, true
			}
		}
	}
	return ir.PhysicalProjection{}, false
}

func allocateConstructionVariable(used map[string]bool, kind string, index int) string {
	base := fmt.Sprintf("__loom_construction_%s_%d", kind, index+1)
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s_%d", base, suffix)
		}
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func cloneCompiledSchema(schema []CompiledOutputColumn) []CompiledOutputColumn {
	return CloneCompiledOutputSchema(schema)
}

func constructionFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func constructionScalarKindsCompatible(output, input string) bool {
	return output == input || output == "DECIMAL" && input == "INTEGER"
}
