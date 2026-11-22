package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
	if dynamicKeys, ok := schemaColumn(schema, "__loom_dynamic_runtime_keys"); ok && dynamicKeys.Internal {
		dynamicKeys.ID = dynamicKeys.Name
		dynamicKeys.Identity = false
		resolved = append(resolved, dynamicKeys)
	}
	if identity != "_key" {
		if rootKey, ok := schemaColumn(schema, "_key"); ok && rootKey.Internal && rootKey.Identity &&
			rootKey.Kind == string(expression.KindString) && rootKey.Cardinality == string(expression.RequiredOne) {
			rootKey.ID, rootKey.Identity = "_key", false
			resolved = append(resolved, rootKey)
		}
	}
	for _, column := range resolved {
		if column.Internal || column.PresenceCompanionName == "" {
			continue
		}
		if companion, found := sourcePresenceCompanionColumn(schema, column.PresenceCompanionName); found {
			resolved = append(resolved, companion)
		} else {
			return nil, fmt.Errorf("construction source column %q has no compiled presence companion", column.ID)
		}
	}
	return resolved, nil
}

func preserveDynamicRuntimeKeys(result constructionStageResult, inputSchema []CompiledOutputColumn) constructionStageResult {
	for _, column := range inputSchema {
		if !column.Internal || column.Name != "__loom_dynamic_runtime_keys" {
			continue
		}
		if _, exists := schemaColumn(result.schema, column.Name); !exists {
			result.schema = append(result.schema, column)
		}
		if !hasPhysicalStageColumn(result.physical.OutputColumns, column.Name) {
			result.physical.OutputColumns = append(result.physical.OutputColumns, toPhysicalStageColumns([]CompiledOutputColumn{column})...)
		}
		if !hasPhysicalProjectionName(result.physical.OutputProjections, column.Name) {
			result.physical.OutputProjections = append(result.physical.OutputProjections, ir.PhysicalProjection{
				Name: column.Name, Hidden: true,
				Value: ir.PhysicalValue{Variable: result.physical.InputRowVariable, Path: []string{column.Name}},
			})
		}
		result.descriptor.Columns = cloneCompiledSchema(result.schema)
		break
	}
	return result
}

func hasPhysicalStageColumn(columns []ir.PhysicalStageColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func constructionSourceIdentity(schema []CompiledOutputColumn) string {
	for _, preferred := range []string{constructionRowID, sourceExpansionIdentity, "_key"} {
		if column, ok := schemaColumn(schema, preferred); ok && column.Internal && column.Identity {
			return preferred
		}
	}
	return ""
}

func constructionPivotCategoryColumnIDs(construction recipe.Construction) map[string]bool {
	ids := make(map[string]bool)
	for _, step := range construction.Steps {
		if step.Operation.Kind == recipe.ConstructionPivotOp && step.Operation.Pivot != nil {

			ids[step.Operation.Pivot.CategoryColumnID] = true
		}
	}
	return ids
}

func constructionPresenceCompanionName(outputName, columnID string) string {
	digest := sha256.Sum256([]byte(outputName + ":" + columnID))
	return "__loom_presence_" + hex.EncodeToString(digest[:8])
}

func attachConstructionSourcePresenceCompanions(plan *ir.PhysicalPlan, schema []CompiledOutputColumn, categoryIDs map[string]bool) ([]CompiledOutputColumn, error) {
	if plan == nil || len(categoryIDs) == 0 {
		return schema, nil
	}
	projections := map[string]ir.PhysicalProjection{}
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnIndex = index
		for _, projection := range operation.Return.Projections {
			if !projection.Hidden {
				projections[projection.Name] = projection
			}
		}
	}
	if returnIndex < 0 {
		return schema, nil
	}
	result := append([]CompiledOutputColumn(nil), schema...)
	for index := range result {
		column := &result[index]
		if column.Internal || !categoryIDs[column.ID] {
			continue
		}
		projection, found := projections[column.Name]
		if !found || projection.Presence == nil {
			continue
		}
		companion := constructionPresenceCompanionName("source", column.ID)
		column.PresenceCompanionName = companion
		companionID := "presence:" + column.ID
		result = append(result, CompiledOutputColumn{
			ID: companionID, Name: companion, Label: companion, SemanticPath: "construction_presence:" + column.ID,
			Kind: string(expression.KindBoolean), Cardinality: string(expression.RequiredOne), Internal: true,
		})
		presence := *projection.Presence
		presence.Paths = cloneConstructionPresencePaths(projection.Presence.Paths)
		plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections, ir.PhysicalProjection{
			Name: companion, Hidden: true, Presence: &presence, PresenceOutput: true,
		})
	}
	return result, nil
}

func cloneConstructionPresencePaths(paths [][]string) [][]string {
	cloned := make([][]string, len(paths))
	for index := range paths {
		cloned[index] = append([]string(nil), paths[index]...)
	}
	return cloned
}

func carryConstructionPresenceCompanions(input, output []CompiledOutputColumn, projections []ir.PhysicalProjection, inputRow string) ([]CompiledOutputColumn, []ir.PhysicalProjection) {
	inputByID := compiledSchemaByID(input)
	output = append([]CompiledOutputColumn(nil), output...)
	projections = append([]ir.PhysicalProjection(nil), projections...)
	for _, column := range output {
		if column.Internal || column.PresenceCompanionName == "" {
			continue
		}
		companionName := column.PresenceCompanionName
		if _, exists := schemaColumn(output, companionName); !exists {
			if companion, found := schemaColumn(input, companionName); found {
				output = append(output, companion)
			} else {
				output = append(output, CompiledOutputColumn{
					ID: "presence:" + column.ID, Name: companionName, Label: companionName,
					SemanticPath: "construction_presence:" + column.ID,
					Kind:         string(expression.KindBoolean), Cardinality: string(expression.RequiredOne), Internal: true,
				})
			}
		}
		if hasPhysicalProjectionName(projections, companionName) {
			continue
		}
		prior, found := inputByID[column.ID]
		if !found || prior.PresenceCompanionName != companionName {
			continue
		}
		presenceProjection := ir.PhysicalProjection{
			Name: companionName, Hidden: true,
			Value: ir.PhysicalValue{Variable: inputRow, Path: []string{companionName}},
		}

		if hasPhysicalProjectionName(projections, constructionRowID) {
			projections = insertProjectionBeforeIdentity(projections, presenceProjection, constructionRowID)
		} else {
			projections = append(projections, presenceProjection)
		}
	}
	return output, projections
}

func constructionPreservesPresenceCompanions(kind ir.PhysicalStageOperationKind) bool {
	switch kind {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageExpandOp,
		ir.PhysicalStageRelatedExpandOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func withoutConstructionPresenceCompanions(schema []CompiledOutputColumn) []CompiledOutputColumn {
	result := append([]CompiledOutputColumn(nil), schema...)
	for index := range result {
		result[index].PresenceCompanionName = ""
	}
	return result
}

func hasPhysicalProjectionName(projections []ir.PhysicalProjection, name string) bool {
	for _, projection := range projections {
		if projection.Name == name {
			return true
		}
	}
	return false
}

func schemaColumn(schema []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Name == name {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func sourcePresenceCompanionColumn(schema []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Name == name && column.Internal && column.Kind == string(expression.KindBoolean) &&
			column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

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
		traversal, traversalErr := BuildPhysicalTraversal(TraversalLoweringRequest{
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

func markRootContributorIdentity(schema []CompiledOutputColumn, rootResourceType string) []CompiledOutputColumn {
	if rootResourceType == "" {
		return schema
	}
	marked := cloneCompiledSchema(schema)
	for index := range marked {
		column := &marked[index]
		if column.Name == "_key" && column.Internal && column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			column.RootContributorResourceType = rootResourceType
		}
	}
	return marked
}

func hasRootContributorIdentity(schema []CompiledOutputColumn, rootResourceType string) bool {
	_, ok := rootContributorInput(schema, rootResourceType)
	return ok
}

func rootContributorInput(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	if rootResourceType == "" {
		return CompiledOutputColumn{}, false
	}
	for _, column := range schema {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorSetFromMap(columns map[string]CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range columns {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorInputFromMap(columns map[string]CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	if contributor, ok := rootContributorSetFromMap(columns, rootResourceType); ok {
		return contributor, true
	}
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) &&
			(column.Cardinality == string(expression.RequiredOne) || column.Cardinality == string(expression.OptionalOne)) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorSet(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.RootContributorResourceType == rootResourceType &&
			column.Name == rootContributorSetColumn && column.Kind == string(expression.KindString) &&
			column.Cardinality == string(expression.Many) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func rootContributorRootKey(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func hasCompilerOwnedRootContributor(schema []CompiledOutputColumn) bool {
	for _, column := range schema {
		if column.RootContributorResourceType == "" {
			continue
		}
		if _, found := rootContributorInput(schema, column.RootContributorResourceType); found {
			return true
		}
	}
	return false
}

func constructionNeedsRetainedRootContributors(steps []recipe.ConstructionStep) bool {
	seenShape := false
	for _, step := range steps {
		switch step.Operation.Kind {
		case recipe.ConstructionGroupOp, recipe.ConstructionPivotOp:
			seenShape = true
		case recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp, recipe.ConstructionRelatedEligibilityOp:
			if seenShape {
				return true
			}
		}
	}
	return false
}

func constructionPreservesRootContributorProvenance(operation recipe.ConstructionOperationKind) bool {
	switch operation {
	case recipe.ConstructionDeriveOp, recipe.ConstructionFilterOp, recipe.ConstructionPivotOp,
		recipe.ConstructionUnpivotOp, recipe.ConstructionGroupOp, recipe.ConstructionExpandOp,
		recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp,
		recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp:
		return true
	default:
		return false
	}
}

func constructionCarriesRootContributorValue(operation recipe.ConstructionOperationKind) bool {
	return constructionPreservesRootContributorProvenance(operation)
}

func constructionPreservesLazyRootContributorAvailability(operation recipe.ConstructionOperationKind) bool {
	switch operation {
	case recipe.ConstructionDeriveOp, recipe.ConstructionFilterOp, recipe.ConstructionUnpivotOp,
		recipe.ConstructionExpandOp, recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp,
		recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp:
		return true
	default:
		return false
	}
}

func stageCapabilities(columns []CompiledOutputColumn, lazyRootContributorAvailable bool) []StageOperationCapability {
	public := publicCompiledSchema(columns)
	numeric, scalar, arrays := 0, 0, 0
	rootKey, rootRowIdentity := false, false
	rootContributorAvailable := lazyRootContributorAvailable || hasCompilerOwnedRootContributor(columns)
	_, activeRelatedRecord := activeRelatedRecordColumn(columns)
	for _, column := range columns {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType != "" &&
			column.Kind == string(expression.KindString) && column.Cardinality == string(expression.RequiredOne) {
			rootKey = true
			rootRowIdentity = column.Identity
		}
	}
	for _, column := range public {
		if column.Cardinality == string(expression.Many) {
			arrays++
			continue
		}
		if _, ok := TableReshapeScalarKind(column.Kind); ok {
			scalar++
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
		capability(recipe.ConstructionUnpivotOp, scalar > 0, "NO_COMPATIBLE_UNPIVOT_COLUMNS", "unpivot requires at least one public scalar column; selected columns must have compatible types"),
		capability(recipe.ConstructionGroupOp, len(public) > 0, "NO_PUBLIC_COLUMNS", "group requires at least one public column or row-count input"),
		capability(recipe.ConstructionCodedGroupOp, false, "DIRECT_SOURCE_STAGE_REQUIRED", "coded grouping is available only on the direct source projection stage because transformed rows may omit Coding occurrences"),
		capability(recipe.ConstructionCodedPivotOp, false, "DIRECT_SOURCE_STAGE_REQUIRED", "coded Pivot is available only on the direct source projection stage"),
		capability(recipe.ConstructionExpandOp, arrays > 0, "NO_ARRAY_COLUMNS", "expand requires a public array-valued column"),
		capability(recipe.ConstructionRelatedSourceOp, rootRowIdentity || rootContributorAvailable, "NO_SOURCE_ROW_ANCHOR", "related source requires a root document identity or compiler-proven root contributors to survive this stage"),
		capability(recipe.ConstructionRelatedExpandOp, rootKey || rootContributorAvailable || activeRelatedRecord, "NO_SOURCE_ROW_ANCHOR", "related expansion requires a retained root key, compiler-proven source records, or exact related-record identity"),
		capability(recipe.ConstructionRelatedEligibilityOp, rootKey || rootContributorAvailable || activeRelatedRecord, "NO_SOURCE_ROW_ANCHOR", "related eligibility requires a retained root key, compiler-proven root contributors, or exact related-record identity on this stage"),
		capability(recipe.ConstructionRelatedFieldOp, activeRelatedRecord, "NO_ACTIVE_RELATED_RECORD", "related field requires the exact terminal resource identity to survive this stage"),
	}
}

func codedPivotSourceCapability(columns []CompiledOutputColumn, rootResourceType string, sourceRowsUnique bool) StageOperationCapability {
	capability := StageOperationCapability{
		Operation: recipe.ConstructionCodedPivotOp, ReasonCode: "SOURCE_ROW_NOT_ROOT",
		Reason: "coded Pivot requires direct root rows with a retained root document identity",
	}
	identity, found := schemaColumn(columns, "_key")
	if sourceRowsUnique && found && identity.Internal && identity.Identity && identity.Kind == string(expression.KindString) &&
		identity.Cardinality == string(expression.RequiredOne) && fhirschema.HasResource(rootResourceType) {
		capability.Supported, capability.ReasonCode, capability.Reason = true, "", ""
	}
	return capability
}

const codedPivotSourcePayloadColumn = ir.PhysicalCodedPivotSourcePayloadColumn

func codedGroupSourceCapability(columns []CompiledOutputColumn, rootResourceType string) StageOperationCapability {
	capability := StageOperationCapability{
		Operation:  recipe.ConstructionCodedGroupOp,
		ReasonCode: "SOURCE_ROW_NOT_ROOT",
		Reason:     "coded grouping requires direct root rows with a retained root record identity",
	}
	identity, found := schemaColumn(columns, "_key")
	if !found || !identity.Internal || !identity.Identity {
		return capability
	}
	publicColumns := 0
	for _, column := range columns {
		if !column.Internal {
			publicColumns++
		}
	}
	if publicColumns == 0 {
		capability.ReasonCode = "NO_PUBLIC_SOURCE_COLUMNS"
		capability.Reason = "coded grouping requires at least one public source column on the input stage"
		return capability
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		capability.ReasonCode = "FHIR_SCHEMA_UNAVAILABLE"
		capability.Reason = "generated FHIR Coding metadata is unavailable"
		return capability
	}
	hasCoding, err := index.HasRepeatedCodingPath(fhirschema.DefinitionName(rootResourceType))
	if err != nil {
		capability.ReasonCode = "FHIR_SCHEMA_UNAVAILABLE"
		capability.Reason = "generated FHIR Coding metadata is unavailable for the selected root"
		return capability
	}
	if !hasCoding {
		capability.ReasonCode = "NO_REPEATED_CODING_PATHS"
		capability.Reason = "the selected root has no generated repeated Coding fields"
		return capability
	}
	capability.Supported = true
	capability.ReasonCode = ""
	capability.Reason = ""
	return capability
}

func withConstructionCapability(capabilities []StageOperationCapability, replacement StageOperationCapability) []StageOperationCapability {
	for index := range capabilities {
		if capabilities[index].Operation == replacement.Operation {
			capabilities[index] = replacement
			return capabilities
		}
	}
	return append(capabilities, replacement)
}

func relatedExpandAnchors(schema []CompiledOutputColumn, rootResourceType string, rootContributorsAvailable bool) []CompiledRelatedExpandAnchor {
	anchors := make([]CompiledRelatedExpandAnchor, 0, 3)
	_, retainedRootKey := retainedRootResourceAnchor(schema, rootResourceType)
	if retainedRootKey && rootResourceType != "" {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: "_key", Kind: "root", ResourceType: rootResourceType,
			Label: "Original " + rootResourceType,
		})
	}
	_, retainedRootContributorSet := rootContributorSet(schema, rootResourceType)
	if !retainedRootKey && (retainedRootContributorSet || rootContributorsAvailable) && rootResourceType != "" {
		anchors = append(anchors, CompiledRelatedExpandAnchor{
			AnchorColumnID: rootContributorSetColumn, Kind: "rootContributors", ResourceType: rootResourceType,
			Label: "Records that make up this row",
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

func retainedRootResourceAnchor(schema []CompiledOutputColumn, rootResourceType string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Internal && column.Name == "_key" && column.RootContributorResourceType == rootResourceType && column.Kind == string(expression.KindString) &&
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
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
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
			RelatedRecordAnchor:         relatedAnchor,
			RootContributorResourceType: column.RootContributorResourceType,
			NormalizedUnit:              cloneUnitIdentity(column.NormalizedUnit),
			PresenceCompanionName:       column.PresenceCompanionName,
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

func constructionGeneratedRowIdentitySchema(identity string) CompiledOutputColumn {
	return CompiledOutputColumn{
		ID: identity, Name: identity, Label: identity, SemanticPath: "construction:row_identity",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true,
	}
}

func insertProjectionBeforeIdentity(projections []ir.PhysicalProjection, projection ir.PhysicalProjection, identity string) []ir.PhysicalProjection {
	for index, existing := range projections {
		if existing.Name == identity {
			projections = append(projections, ir.PhysicalProjection{})
			copy(projections[index+1:], projections[index:])
			projections[index] = projection
			return projections
		}
	}
	return append(projections, projection)
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
