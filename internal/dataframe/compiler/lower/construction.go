package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

const constructionRowID = "__loom_row_id"
const sourceExpansionIdentity = "__loom_expansion_identity"
const rootContributorSetColumn = "__loom_root_contributor_keys"

type CompiledStageDescriptor struct {
	ID                      string
	InputStageID            string
	Operation               string
	Columns                 []CompiledOutputColumn
	RowIdentityColumn       string
	Capabilities            []StageOperationCapability
	RelatedExpandAnchors    []CompiledRelatedExpandAnchor
	RelatedExpand           *CompiledRelatedExpandStage
	ActiveRelatedRecord     *CompiledActiveRelatedRecordStage
	GroupFieldSourceContext *CompiledGroupFieldSourceContext
}

type CompiledGroupFieldSourceContext struct {
	SourceStageID        string
	SourceShapeID        string
	SourceIdentityColumn string
	RootOccurrenceID     string
	RootResourceType     string
	SourceExpansion      *CompiledGroupFieldSourceExpansion
	FieldOwners          []CompiledGroupFieldOwner
}

type CompiledGroupFieldSourceExpansion struct {
	OwnerOccurrenceID string
	OwnerResourceType string
	Selector          string
	Route             []CompiledGroupFieldSourceRouteStep
	Ancestors         []CompiledGroupFieldSourceAncestor
	OrdinalSemantics  string
	IdentitySemantics string
	EmptyPolicy       string
}

type CompiledGroupFieldSourceRouteStep struct {
	OccurrenceID string
	FromType     string
	EdgeLabel    string
	ToType       string
	Direction    string
}

type CompiledGroupFieldSourceAncestor struct {
	StepIndex        int
	BoundarySelector string
	ResourceType     string
}

type CompiledGroupFieldOwner struct {
	Role              string
	OccurrenceID      string
	OwnerResourceType string
	ResourceType      string
	BoundarySelector  string
	StepIndex         int
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

func DescribeConstructionSourceStage(schema []CompiledOutputColumn, rootResourceType string, sourcePlan *ir.PhysicalPlan) (CompiledStageDescriptor, error) {
	identity := constructionSourceIdentity(schema)
	if identity == "" {
		return CompiledStageDescriptor{}, fmt.Errorf("construction source has no supported row identity projection")
	}
	columns := cloneCompiledSchema(schema)
	columns = markRootContributorIdentity(columns, rootResourceType)
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
	descriptor.Capabilities = stageCapabilities(columns, false)
	descriptor.Capabilities = withConstructionCapability(descriptor.Capabilities, codedGroupSourceCapability(columns, rootResourceType))
	descriptor.Capabilities = withConstructionCapability(descriptor.Capabilities,
		codedPivotSourceCapability(columns, rootResourceType, constructionDirectRootRowsUnique(sourcePlan, "CODED_PIVOT") == nil))
	descriptor.RelatedExpandAnchors = relatedExpandAnchors(columns, rootResourceType, false)
	groupFieldContext, err := describeGroupFieldSourceContext(recipe.ConstructionSourceProjectionID, rootResourceType, identity, sourcePlan)
	if err != nil {
		return CompiledStageDescriptor{}, fmt.Errorf("describe Group field source context: %w", err)
	}
	descriptor.GroupFieldSourceContext = groupFieldContext
	return descriptor, nil
}

func appendGroupSourceProjectionColumns(schema []CompiledOutputColumn, additional []ir.PhysicalStageColumn) []CompiledOutputColumn {
	result := append([]CompiledOutputColumn(nil), schema...)
	seen := make(map[string]bool, len(result)+len(additional))
	for _, column := range result {
		seen[column.Name] = true
	}
	for _, column := range additional {
		if seen[column.Name] {
			continue
		}
		compiled := CompiledOutputColumn{
			ID: column.ID, Name: column.Name, Label: column.Label, Kind: column.Kind,
			Cardinality: column.Cardinality, Nullable: column.Nullable, Internal: column.Internal,
			Identity: column.Identity, RootContributorResourceType: column.RootContributorResourceType,
			SemanticPath: "construction_group_source_projection:" + column.Name,
		}
		if column.RelatedRecordAnchor != nil {
			compiled.RelatedRecordAnchor = &CompiledRelatedRecordAnchor{TargetNodeID: column.RelatedRecordAnchor.NodeID, TargetResourceType: column.RelatedRecordAnchor.ResourceType}
		}
		compiled.NormalizedUnit = cloneUnitIdentity(column.NormalizedUnit)
		result = append(result, compiled)
		seen[column.Name] = true
	}
	return result
}

func physicalGroupRelatedAdapterInputColumn(group *ir.PhysicalStageGroup, name string) bool {
	if group == nil || group.RelatedOccurrence == nil || name == "" {
		return false
	}
	adapter := group.RelatedOccurrence
	if name == adapter.CodingMembershipColumn || name == adapter.OccurrenceKeyColumn {
		return true
	}
	for _, projection := range adapter.ScalarProjections {
		if projection.InputColumn == name {
			return true
		}
	}
	return false
}

func constructionFirstGroupHasBoundFieldSource(construction recipe.Construction) bool {
	if len(construction.Steps) == 0 || construction.Steps[0].Operation.Kind != recipe.ConstructionGroupOp || construction.Steps[0].Operation.Group == nil {
		return false
	}
	valid := func(source *recipe.ConstructionGroupFieldSource) bool {
		if source == nil || source.Kind != "BINDING" || source.ChoiceID != "" ||
			source.SourceStageID != recipe.ConstructionSourceProjectionID || source.SourceShapeID == "" ||
			(source.ValueKind != "SCALAR" && source.ValueKind != "CODING_IDENTITY") {
			return false
		}
		input := source.Input
		if input == nil {
			if source.SourceRole == "" && source.OccurrenceID == "" && source.OwnerResourceType == "" && source.ResourceType == "" && source.CandidateID == "" && source.CanonicalPath == "" {
				return false
			}
			input = &recipe.ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: &recipe.ConstructionGroupCurrentStageFieldSource{
				SourceRole: source.SourceRole, OccurrenceID: source.OccurrenceID, OwnerResourceType: source.OwnerResourceType,
				ResourceType: source.ResourceType, CandidateID: source.CandidateID, CanonicalPath: source.CanonicalPath,
				BoundarySelector: source.BoundarySelector,
			}}
		}
		if input.Kind == "CURRENT_STAGE" && (input.CurrentStage == nil || input.RelatedOccurrence != nil) ||
			input.Kind == "RELATED_OCCURRENCE" && (input.RelatedOccurrence == nil || input.CurrentStage != nil) {
			return false
		}
		switch input.Kind {
		case "CURRENT_STAGE":
			current := input.CurrentStage
			return current != nil && current.SourceRole != "" && current.OccurrenceID != "" &&
				current.OwnerResourceType != "" && current.ResourceType != "" && current.CanonicalPath != ""
		case "RELATED_OCCURRENCE":
			related := input.RelatedOccurrence
			if related == nil || related.AnchorOccurrenceID == "" || related.AnchorResourceType == "" ||
				related.TargetOccurrenceID == "" || related.TargetResourceType == "" || len(related.Route) == 0 ||
				related.Multiplicity != recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput || related.EmptyPolicy == "" {
				return false
			}
			if source.ValueKind == "CODING_IDENTITY" {
				return related.ResourceType == "Coding" && related.CodingSelector != "" && related.CodingEmptyPolicy != "" && related.OrdinalSemantics != ""
			}
			return related.ResourceType != "" && related.CandidateID != "" && related.CanonicalPath != ""
		default:
			return false
		}
	}
	for _, key := range construction.Steps[0].Operation.Group.Keys {
		if valid(key.FieldSource) {
			return true
		}
	}
	for _, rowValue := range construction.Steps[0].RowValues {
		if valid(rowValue.FieldSource) {
			return true
		}
	}
	return false
}

type constructionStageInput struct {
	stageID                      string
	schema                       []CompiledOutputColumn
	identity                     string
	rootResourceType             string
	index                        int
	rootContributorProvenance    bool
	lazyRootContributorAvailable bool
	retainRootContributors       bool
	presenceCategoryIDs          map[string]bool
}

type constructionStageResult struct {
	physical                     ir.PhysicalConstructionStage
	schema                       []CompiledOutputColumn
	identity                     string
	rootContributorProvenance    bool
	lazyRootContributorAvailable bool
	descriptor                   CompiledStageDescriptor
}

type constructionLowerer struct {
	plan          *ir.PhysicalPlan
	policy        ir.PhysicalOptimizationPolicy
	usedVariables map[string]bool
}

type cohortGroupCompileInput struct {
	Output      recipeplan.OutputPlan
	Rows        ir.PhysicalGroupRows
	BindVars    map[string]any
	AfterStepID string
}

func appendRecipeConstructionStages(plan *ir.PhysicalPlan, outputName, rootResourceType string, sourceRowExpansion bool, construction recipe.Construction, sourceSchema []CompiledOutputColumn, policy ir.PhysicalOptimizationPolicy, cohort *cohortGroupCompileInput, workspaceOutputSchemas map[string][]CompiledOutputColumn) ([]CompiledOutputColumn, []CompiledStageDescriptor, string, error) {
	if plan == nil {
		return nil, nil, "", fmt.Errorf("physical plan is required")
	}
	if step, ok := construction.TerminalCombineStep(); ok {
		if cohort != nil {
			return nil, nil, "", fmt.Errorf("terminal Combine cannot be combined with an explicit cohort")
		}
		return appendRecipeTerminalCombine(plan, step, workspaceOutputSchemas)
	}
	allowEmptyRootRelatedExpandSource := len(construction.Steps) > 0 &&
		construction.Steps[0].Operation.Kind == recipe.ConstructionRelatedExpandOp &&
		construction.Steps[0].Operation.RelatedExpand != nil && construction.Steps[0].Operation.RelatedExpand.AnchorColumnID == "_key" &&
		len(publicCompiledSchema(sourceSchema)) == 0 && constructionSourceIdentity(sourceSchema) != "" &&
		rootResourceType != "" && rootResourceType == plan.Source.ResourceType
	allowEmptyBoundGroupSource := len(construction.SourceColumns) == 0 && constructionFirstGroupHasBoundFieldSource(construction) &&
		constructionSourceIdentity(sourceSchema) != "" && rootResourceType != "" && rootResourceType == plan.Source.ResourceType
	allowEmptyIdentitySource := len(construction.SourceColumns) == 0 && constructionSourceIdentity(sourceSchema) != "" &&
		(len(construction.Steps) == 0 || allowEmptyRootRelatedExpandSource || allowEmptyBoundGroupSource)
	if len(construction.SourceColumns) == 0 && !allowEmptyIdentitySource &&
		(len(construction.Steps) == 0 || construction.Steps[0].Operation.Kind != recipe.ConstructionCodedPivotOp) {
		return nil, nil, "", fmt.Errorf("construction source schema must be supplied by the resolved source compiler")
	}
	presenceCategoryIDs := constructionPivotCategoryColumnIDs(construction)
	var err error
	sourceSchema, err = attachConstructionSourcePresenceCompanions(plan, sourceSchema, presenceCategoryIDs)
	if err != nil {
		return nil, nil, "", err
	}
	resolvedSource, err := resolveConstructionSourceSchema(plan, construction.SourceColumns, sourceSchema, outputName)
	if err != nil {
		return nil, nil, "", err
	}
	resolvedSource = markRootContributorIdentity(resolvedSource, rootResourceType)
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
	rootContributorProvenance := hasRootContributorIdentity(resolvedSource, rootResourceType)
	populationRootSourceAvailable := populationRootContributorAvailable(plan, resolvedSource, rootResourceType)
	lazyRootContributorAvailable := false
	descriptors[0].Capabilities = stageCapabilities(resolvedSource, false)
	descriptors[0].Capabilities = withConstructionCapability(descriptors[0].Capabilities, codedGroupSourceCapability(resolvedSource, rootResourceType))
	descriptors[0].Capabilities = withConstructionCapability(descriptors[0].Capabilities,
		codedPivotSourceCapability(resolvedSource, rootResourceType, constructionDirectRootRowsUnique(plan, "CODED_PIVOT") == nil))
	descriptors[0].RelatedExpandAnchors = relatedExpandAnchors(resolvedSource, rootResourceType, false)
	descriptors[0].GroupFieldSourceContext, err = describeGroupFieldSourceContext(
		recipe.ConstructionSourceProjectionID, rootResourceType, sourceIdentity, plan,
	)
	if err != nil {
		return nil, nil, "", fmt.Errorf("describe Group field source context: %w", err)
	}
	if len(construction.Steps) == 0 && cohort == nil {
		return resolvedSource, descriptors, sourceIdentity, nil
	}
	usedVariables := physicalPlanVariables(plan.Operations)
	lowerer := &constructionLowerer{plan: plan, policy: policy, usedVariables: usedVariables}
	retainForLaterOperations := constructionNeedsRetainedRootContributors(construction.Steps) || cohortNeedsRetainedRootContributors(construction, cohort)
	retainForPopulation := populationConstructionNeedsRetainedRootContributors(construction.Steps, cohort, populationRootSourceAvailable)
	retainRootContributors := retainForLaterOperations || retainForPopulation
	if retainForPopulation && !retainForLaterOperations && (constructionTerminalCodingGroupRequeriesExpandedPreimage(construction.Steps, plan, sourceIdentity, rootResourceType) ||
		constructionTerminalRelatedCodingGroupRequeriesPreimage(construction.Steps, plan, sourceIdentity, rootResourceType)) {

		retainRootContributors = false
	}
	priorSchema, priorIdentity := resolvedSource, sourceIdentity
	priorStageID := recipe.ConstructionSourceProjectionID
	cohortAnchorIndex, err := constructionCohortAnchorIndex(construction, cohort)
	if err != nil {
		return nil, nil, "", err
	}
	appendCohort := func() error {
		if cohort == nil {
			return nil
		}
		if !rootContributorProvenance {
			return fmt.Errorf("cohort stage requires compiler-proven root contributors from its preceding stage")
		}
		contributor, ok := rootContributorInput(priorSchema, rootResourceType)
		if !ok {
			return fmt.Errorf("cohort stage lost its compiler-proven root contributor identity")
		}
		outputSchema, schemaErr := cohortGroupStageOutputSchema(cohort.Output, priorSchema, rootResourceType)
		if schemaErr != nil {
			return schemaErr
		}
		inputRow := allocateConstructionVariable(usedVariables, "cohort_input", len(sequence.Stages))
		outputRow := allocateConstructionVariable(usedVariables, "cohort_output", len(sequence.Stages))
		rootContributorVariable := allocateConstructionVariable(usedVariables, "cohort_root_contributors", len(sequence.Stages))
		preserveMissingMembers := len(cohort.Output.Root.Filters) == 0
		for index := 0; preserveMissingMembers && index <= cohortAnchorIndex; index++ {
			preserveMissingMembers = !constructionOperationCanDropRows(construction.Steps[index].Operation)
		}
		physical := ir.PhysicalConstructionStage{
			ID: recipe.ConstructionCohortGroupStageID, InputStageID: priorStageID,
			Kind: ir.PhysicalStageCohortGroupOp, InputRowVariable: inputRow, OutputRowVariable: outputRow,
			InputColumns: toPhysicalStageColumns(priorSchema), OutputColumns: toPhysicalStageColumns(outputSchema),
			CohortGroup: &ir.PhysicalStageCohortGroup{
				Rows: cohort.Rows, ContributorInputColumn: contributor.Name,
				ContributorInputMany:        contributor.Cardinality == string(expression.Many),
				RootContributorOutputColumn: rootContributorSetColumn, RootContributorVariable: rootContributorVariable,
				PreserveMissingMembers: preserveMissingMembers,
			},
			RowIdentityColumn: constructionRowID,
		}
		if rootScan := constructionRootScan(plan); rootScan != nil && rootScan.Population == nil &&
			hasExactRootIdentity(priorSchema, rootResourceType) && cohortRootScanPrefixSafe(sequence.Stages, rootResourceType) {
			rootScan.CohortSource = &ir.PhysicalCohortRootSource{
				CohortStageID: recipe.ConstructionCohortGroupStageID, CohortInputStageID: priorStageID,
				RootIdentityColumn: "_key", RootResourceType: rootResourceType,
				RevisionCollectionBindKey:         cohort.Rows.RevisionCollectionBindKey,
				SelectionCollectionBindKey:        cohort.Rows.SelectionCollectionBindKey,
				SelectionMembersCollectionBindKey: cohort.Rows.SelectionMembersCollectionBindKey,
				MembershipsCollectionBindKey:      cohort.Rows.MembershipsCollectionBindKey,
				RevisionIDBindKey:                 cohort.Rows.RevisionIDBindKey, ProjectBindKey: cohort.Rows.ProjectBindKey,
				ResourceProjectBindKey:   cohort.Rows.ResourceProjectBindKey,
				DatasetGenerationBindKey: cohort.Rows.DatasetGenerationBindKey, ResourceTypeBindKey: cohort.Rows.ResourceTypeBindKey,
				PolicyBindKey: cohort.Rows.PolicyBindKey,
			}
		}
		descriptor := CompiledStageDescriptor{
			ID: recipe.ConstructionCohortGroupStageID, InputStageID: priorStageID, Operation: "COHORT_GROUP",
			Columns: cloneCompiledSchema(outputSchema), RowIdentityColumn: constructionRowID,
			Capabilities:         stageCapabilities(outputSchema, false),
			RelatedExpandAnchors: relatedExpandAnchors(outputSchema, rootResourceType, true),
		}
		descriptor.Capabilities = append(descriptor.Capabilities, StageOperationCapability{Operation: recipe.ConstructionOperationKind("ROW_VALUES"), Supported: true})
		sequence.Stages = append(sequence.Stages, physical)
		descriptors = append(descriptors, descriptor)
		priorStageID, priorSchema, priorIdentity = recipe.ConstructionCohortGroupStageID, outputSchema, constructionRowID
		rootContributorProvenance, lazyRootContributorAvailable = true, true
		return nil
	}
	for index, step := range construction.Steps {
		if cohort != nil && index == cohortAnchorIndex+1 {
			if err := appendCohort(); err != nil {
				return nil, nil, "", err
			}
		}
		if sourceRowExpansion && step.Operation.Kind == recipe.ConstructionRelatedSourceOp {
			var err error
			step, err = rebindExpandedRelatedSourceAnchor(step, priorSchema, priorIdentity, rootResourceType, plan.Source.ResourceType, outputName)
			if err != nil {
				return nil, nil, "", fmt.Errorf("construction step %q: %w", step.ID, err)
			}
		}
		result, stageErr := lowerer.lowerConstructionStep(step, constructionStageInput{
			stageID: priorStageID, schema: priorSchema, identity: priorIdentity, rootResourceType: rootResourceType, index: index,
			rootContributorProvenance: rootContributorProvenance, lazyRootContributorAvailable: lazyRootContributorAvailable,
			retainRootContributors: retainRootContributors, presenceCategoryIDs: presenceCategoryIDs,
		})
		if stageErr != nil {
			if anchorErr, ok := stageErr.(*RelatedSourceAnchorError); ok && anchorErr.OutputID == "" {
				anchorErr.OutputID = outputName
			}
			return nil, nil, "", fmt.Errorf("construction step %q: %w", step.ID, stageErr)
		}
		switch step.Operation.Kind {
		case recipe.ConstructionFilterOp, recipe.ConstructionDeriveOp, recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedFieldOp:
			result = preserveDynamicRuntimeKeys(result, priorSchema)
		}
		if result.physical.InputStageID == sequence.SourceStageID && len(result.physical.InputColumns) > len(sequence.SourceColumns) {
			extra := result.physical.InputColumns[len(sequence.SourceColumns):]
			sourceProjectionColumns := make([]ir.PhysicalStageColumn, 0, len(extra))
			for _, column := range extra {
				if !physicalGroupRelatedAdapterInputColumn(result.physical.Group, column.Name) {
					sourceProjectionColumns = append(sourceProjectionColumns, column)
				}
			}
			if len(sourceProjectionColumns) != 0 {
				resolvedSource = appendGroupSourceProjectionColumns(resolvedSource, sourceProjectionColumns)
				sequence.SourceColumns = toPhysicalStageColumns(resolvedSource)
				descriptors[0].Columns = cloneCompiledSchema(resolvedSource)
			}
		}
		sequence.Stages = append(sequence.Stages, result.physical)
		descriptors = append(descriptors, result.descriptor)
		priorStageID, priorSchema, priorIdentity = step.ID, result.schema, result.identity
		rootContributorProvenance = result.rootContributorProvenance
		lazyRootContributorAvailable = result.lazyRootContributorAvailable
	}
	if cohort != nil && cohortAnchorIndex == len(construction.Steps)-1 {
		if err := appendCohort(); err != nil {
			return nil, nil, "", err
		}
	}
	sequence.FinalStageID = priorStageID
	sequence.FinalRowIdentity = priorIdentity
	sequence.FinalColumns = toPhysicalStageColumns(priorSchema)
	sequence.PreviewSourceWindowByRootID = constructionRootIDPivotFastPathEligible(plan, sequence, descriptors, rootResourceType)
	if sequence.PreviewSourceWindowByRootID && len(sequence.Stages) == 1 {
		sequence.Stages[0].GroupedPivot.OneInputRowPerGroup = true
	}
	plan.StageSequence = sequence
	if err := plan.Validate(); err != nil {
		return nil, nil, "", fmt.Errorf("validate typed construction sequence: %w", err)
	}
	return priorSchema, descriptors, priorIdentity, nil
}

type RelatedSourceAnchorError struct {
	OutputID string
	StepID   string
	Reason   string
}

func (e *RelatedSourceAnchorError) Error() string {
	return fmt.Sprintf("related source step %q has an invalid row anchor (%s)", e.StepID, e.Reason)
}

func rebindExpandedRelatedSourceAnchor(
	step recipe.ConstructionStep,
	inputSchema []CompiledOutputColumn,
	inputIdentity, rootResourceType, sourceResourceType, outputID string,
) (recipe.ConstructionStep, error) {
	related := step.Operation.RelatedSource
	if related == nil || related.AnchorColumnID == inputIdentity {
		return step, nil
	}
	if rootResourceType != sourceResourceType {
		return step, &RelatedSourceAnchorError{OutputID: outputID, StepID: step.ID, Reason: "root_resource_changed"}
	}
	if related.AnchorColumnID != "_key" || inputIdentity != sourceExpansionIdentity {
		return step, &RelatedSourceAnchorError{OutputID: outputID, StepID: step.ID, Reason: "row_identity_changed"}
	}
	rootKey, ok := rootContributorInput(inputSchema, rootResourceType)
	if !ok || rootKey.Name != "_key" || rootKey.RootContributorResourceType != rootResourceType ||
		rootKey.Kind != string(expression.KindString) ||
		(rootKey.Cardinality != string(expression.RequiredOne) && rootKey.Cardinality != string(expression.OptionalOne)) {
		return step, &RelatedSourceAnchorError{OutputID: outputID, StepID: step.ID, Reason: "root_identity_unproven"}
	}
	updated := *related
	updated.AnchorColumnID = inputIdentity
	operation := step.Operation
	operation.RelatedSource = &updated
	step.Operation = operation
	return step, nil
}

func constructionRootScan(plan *ir.PhysicalPlan) *ir.PhysicalRootScan {
	if plan == nil || len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp {
		return nil
	}
	return plan.Operations[0].RootScan
}

func hasExactRootIdentity(schema []CompiledOutputColumn, rootResourceType string) bool {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			return true
		}
	}
	return false
}

func cohortRootScanPrefixSafe(stages []ir.PhysicalConstructionStage, rootResourceType string) bool {
	for _, stage := range stages {
		if stage.Kind != ir.PhysicalStageFilterOp || !physicalStageHasExactRootIdentity(stage.InputColumns, rootResourceType) ||
			!physicalStageHasExactRootIdentity(stage.OutputColumns, rootResourceType) {
			return false
		}
	}
	return true
}

func physicalStageHasExactRootIdentity(columns []ir.PhysicalStageColumn, rootResourceType string) bool {
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			return true
		}
	}
	return false
}

func constructionCohortAnchorIndex(construction recipe.Construction, cohort *cohortGroupCompileInput) (int, error) {
	if cohort == nil {
		return -1, nil
	}
	if cohort.AfterStepID == "" {
		return -1, nil
	}
	for index, step := range construction.Steps {
		if step.ID == cohort.AfterStepID {
			return index, nil
		}
	}
	return -1, fmt.Errorf("cohort afterStepId %q does not name a construction step", cohort.AfterStepID)
}

func constructionOperationCanDropRows(operation recipe.ConstructionOperation) bool {
	switch operation.Kind {
	case recipe.ConstructionFilterOp, recipe.ConstructionRelatedEligibilityOp:
		return true
	case recipe.ConstructionRelatedExpandOp:
		return operation.RelatedExpand != nil && operation.RelatedExpand.EmptyPolicy == recipe.ExpansionExclude
	case recipe.ConstructionUnpivotOp:
		return operation.Unpivot != nil && operation.Unpivot.NullRowPolicy == recipe.UnpivotNullDrop
	case recipe.ConstructionGroupOp:
		return operation.Group != nil && operation.Group.MissingKeyPolicy.Normalized() == recipe.ConstructionGroupMissingKeyExclude
	default:

		return false
	}
}

func cohortNeedsRetainedRootContributors(construction recipe.Construction, cohort *cohortGroupCompileInput) bool {
	if cohort == nil {
		return false
	}
	anchor, err := constructionCohortAnchorIndex(construction, cohort)
	if err != nil {
		return false
	}
	for index, step := range construction.Steps {
		if index <= anchor && (step.Operation.Kind == recipe.ConstructionGroupOp || step.Operation.Kind == recipe.ConstructionPivotOp) {
			return true
		}
		if index > anchor && step.Operation.Kind == recipe.ConstructionRelatedSourceOp {
			return true
		}
	}
	return false
}

func populationConstructionNeedsRetainedRootContributors(steps []recipe.ConstructionStep, cohort *cohortGroupCompileInput, sourceAvailable bool) bool {
	if !sourceAvailable {
		return false
	}
	if cohort != nil {
		return true
	}
	for _, step := range steps {
		switch step.Operation.Kind {
		case recipe.ConstructionGroupOp, recipe.ConstructionPivotOp:
			return true
		}
	}
	return false
}

func constructionTerminalCodingGroupRequeriesExpandedPreimage(
	steps []recipe.ConstructionStep,
	plan *ir.PhysicalPlan,
	sourceIdentity, rootResourceType string,
) bool {
	if len(steps) != 1 || steps[0].Operation.Kind != recipe.ConstructionGroupOp || steps[0].Operation.Group == nil ||
		sourceIdentity != sourceExpansionIdentity || rootResourceType == "" {
		return false
	}
	group := steps[0].Operation.Group
	if len(group.Aggregates) != 1 || group.Aggregates[0].Operation != recipe.ConstructionGroupCountRows ||
		group.Aggregates[0].InputColumnID != "" {
		return false
	}
	var codingSource *recipe.ConstructionGroupFieldSource
	for _, key := range group.Keys {
		if key.SemanticCategory != nil {
			return false
		}
		if key.FieldSource == nil || key.FieldSource.ValueKind != "CODING_IDENTITY" {
			continue
		}
		if codingSource != nil {
			return false
		}
		codingSource = key.FieldSource
	}
	if codingSource == nil {
		return false
	}
	context, err := describeGroupFieldSourceContext(recipe.ConstructionSourceProjectionID, rootResourceType, sourceIdentity, plan)
	if err != nil || context == nil || context.SourceExpansion == nil {
		return false
	}
	normalizedSource, err := normalizeConstructionCurrentGroupFieldSource(*codingSource)
	if err != nil {
		return false
	}
	_, err = validateGroupCodingIdentitySource(plan, normalizedSource, context, recipe.ConstructionSourceProjectionID)
	return err == nil
}

func constructionTerminalRelatedCodingGroupRequeriesPreimage(
	steps []recipe.ConstructionStep,
	plan *ir.PhysicalPlan,
	sourceIdentity, rootResourceType string,
) bool {
	if len(steps) != 1 || steps[0].Operation.Kind != recipe.ConstructionGroupOp || steps[0].Operation.Group == nil ||
		sourceIdentity != "_key" || rootResourceType == "" {
		return false
	}
	group := steps[0].Operation.Group
	if len(group.Aggregates) != 1 || group.Aggregates[0].Operation != recipe.ConstructionGroupCountRows || group.Aggregates[0].InputColumnID != "" {
		return false
	}
	context, err := describeGroupFieldSourceContext(recipe.ConstructionSourceProjectionID, rootResourceType, sourceIdentity, plan)
	if err != nil || context == nil || context.SourceExpansion != nil {
		return false
	}
	var selectedRouteID string
	bind := func(source *recipe.ConstructionGroupFieldSource) bool {
		if source == nil || source.Input == nil || source.Input.Kind != "RELATED_OCCURRENCE" {
			return true
		}
		resolved, err := BindGroupRelatedOccurrenceSource(context, *source)
		if err != nil || resolved.Input == nil || resolved.Input.RelatedOccurrence == nil {
			return false
		}
		routeID := resolved.Input.RelatedOccurrence.RouteID
		if selectedRouteID == "" {
			selectedRouteID = routeID
			return true
		}
		return selectedRouteID == routeID
	}
	var codedRelated *recipe.ConstructionGroupFieldSource
	for _, key := range group.Keys {
		if key.SemanticCategory != nil {
			return false
		}
		if key.FieldSource != nil && key.FieldSource.ValueKind == "CODING_IDENTITY" {
			if key.FieldSource.Input == nil || key.FieldSource.Input.Kind != "RELATED_OCCURRENCE" || codedRelated != nil {
				return false
			}
			codedRelated = key.FieldSource
		}
		if !bind(key.FieldSource) {
			return false
		}
	}
	if codedRelated == nil {
		return false
	}
	for _, rowValue := range steps[0].RowValues {
		if !bind(rowValue.FieldSource) {
			return false
		}
	}
	return selectedRouteID != ""
}

func populationRootContributorAvailable(plan *ir.PhysicalPlan, schema []CompiledOutputColumn, rootResourceType string) bool {
	root := constructionRootScan(plan)
	if root == nil || root.Population == nil || rootResourceType == "" {
		return false
	}
	for _, column := range schema {
		if column.Internal && column.Name == "_key" &&
			column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.RequiredOne) {
			return true
		}
	}
	return false
}

func cohortGroupStageOutputSchema(output recipeplan.OutputPlan, inputSchema []CompiledOutputColumn, rootResourceType string) ([]CompiledOutputColumn, error) {
	if output.GroupRows == nil {
		return nil, fmt.Errorf("explicit group rows are required")
	}
	columns, err := recipeOutputSchema(ir.PhysicalPlan{}, output, nil, nil)
	if err != nil {
		return nil, err
	}
	if _, exists := schemaColumn(columns, rootContributorSetColumn); exists {
		return nil, fmt.Errorf("cohort output column %q is reserved for compiler-owned contributor identity", rootContributorSetColumn)
	}
	columns = append(columns, CompiledOutputColumn{
		ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
		SemanticPath: "construction_root_contributors:cohort_group",
		Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
		RootContributorResourceType: rootResourceType,
	})
	if _, found := rootContributorInput(inputSchema, rootResourceType); !found {
		return nil, fmt.Errorf("cohort input schema has no compiler-proven root contributor identity")
	}
	return columns, nil
}

func constructionRootIDPivotFastPathEligible(plan *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, descriptors []CompiledStageDescriptor, rootResourceType string) bool {
	if plan == nil || sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) == 0 || len(descriptors) != len(sequence.Stages)+1 {
		return false
	}
	stage := sequence.Stages[len(sequence.Stages)-1]
	if stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil || len(stage.GroupedPivot.GroupKeys) == 0 {
		return false
	}
	if !constructionRootIDPivotSourceEligible(plan, rootResourceType) {
		return false
	}
	if stage.GroupedPivot.CodedCorrelation != nil {
		if len(sequence.Stages) != 1 {
			return false
		}
		return constructionCodedPivotHasDirectRootKey(plan, stage.GroupedPivot, descriptors[0].Columns, rootResourceType)
	}
	if len(sequence.Stages) > 1 {
		if len(stage.GroupedPivot.GroupKeys) != 1 {
			return false
		}
		key := stage.GroupedPivot.GroupKeys[0].Column
		for _, prefix := range sequence.Stages[:len(sequence.Stages)-1] {
			switch prefix.Kind {
			case ir.PhysicalStageRelatedExpandOp:
				if prefix.RelatedExpand == nil || prefix.RelatedExpand.EmptyPolicy != "PRESERVE_PARENT" {
					return false
				}
			case ir.PhysicalStageRelatedFieldOp:
			default:
				return false
			}
			carried := false
			for _, projection := range prefix.OutputProjections {
				if projection.Name == key && projection.Expression == nil && projection.Value.Variable == prefix.InputRowVariable && len(projection.Value.Path) == 1 && projection.Value.Path[0] == key {
					carried = true
				}
			}
			if !carried {
				return false
			}
		}
	}
	return groupedPivotHasDirectRootIDKey(plan, stage.GroupedPivot.GroupKeys, descriptors[0].Columns, rootResourceType)
}

func constructionCodedPivotHasDirectRootKey(plan *ir.PhysicalPlan, pivot *ir.PhysicalGroupedPivot, schema []CompiledOutputColumn, rootResourceType string) bool {
	if plan == nil || pivot == nil || len(pivot.GroupKeys) != 1 || pivot.GroupKeys[0].Column != "_key" ||
		!pivot.GroupKeys[0].Hidden || pivot.GroupKeys[0].Kind != "STRING" ||
		pivot.CodedSourceVariable == "" || len(plan.Operations) == 0 || plan.Operations[0].RootScan == nil {
		return false
	}
	root := plan.Operations[0].RootScan
	identity, found := schemaColumn(schema, "_key")
	if !found || !identity.Internal || !identity.Identity || identity.Kind != string(expression.KindString) ||
		identity.Cardinality != string(expression.RequiredOne) || plan.Source.ResourceType != rootResourceType ||
		root.Variable != pivot.CodedSourceVariable {
		return false
	}
	for _, projection := range pivot.InputProjections {
		if projection.Name == "_key" {
			return projection.Hidden && projection.Value.Variable == root.Variable &&
				len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key"
		}
	}
	return false
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
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp, ir.PhysicalSetOp:
		case ir.PhysicalReturnOp:
			returns++
			if index != len(plan.Operations)-1 {
				return false
			}
		default:

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
