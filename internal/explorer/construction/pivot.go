package construction

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func constructionPivotDiscoveryOutcomeError(stepID string, discovery ConstructionCategoryDiscoveryResponse) error {
	switch discovery.Outcome {
	case constructionCategoryDiscoveryNoValues:
		return unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
			fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", stepID), nil)
	case constructionCategoryDiscoveryLimitExceeded:
		return unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
			"The Pivot categories cannot be discovered because this category field has more than 256 values in the current rows. Choose another category field, filter rows before pivoting, or reduce the number of categories, then retry.", nil)
	case constructionCategoryDiscoveryMissingUnsupported:
		return unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
			"The Pivot categories cannot be discovered because some rows have no category field and this scan cannot distinguish an absent field from a null value. Filter rows where the category field is missing, or choose a field populated in every row, then retry.", nil)
	default:
		return unprocessable("construction-proposal", "PIVOT_DISCOVERY_INCOMPLETE",
			"The Pivot categories could not be discovered against a complete value-bearing scan. Refresh the preview and retry; if the problem persists, choose another category field or filter rows before pivoting.", nil)
	}
}

func constructionPivotDiscoveryEvidence(
	discovered []ConstructionDiscoveredCategory,
	groupKeyIDs []string,
	input []authoringv2.StageColumn,
) ([]authoringv2.ConstructionPivotDiscoveryCategory, error) {
	inputByID := make(map[string]authoringv2.StageColumn, len(input))
	for _, column := range input {
		if column.ID != "" {
			inputByID[column.ID] = column
		}
	}
	usedNames := make(map[string]bool, len(groupKeyIDs)+len(discovered))
	for _, id := range groupKeyIDs {
		column, exists := inputByID[id]
		if !exists {
			return nil, fmt.Errorf("group key %q is not present in the Pivot input", id)
		}
		usedNames[strings.ToLower(column.Name)] = true
	}
	evidence := make([]authoringv2.ConstructionPivotDiscoveryCategory, 0, len(discovered))
	for _, category := range discovered {
		label := strings.TrimSpace(category.Label)
		name := uniqueConstructionPivotOutputName(label, usedNames)
		usedNames[strings.ToLower(name)] = true
		evidence = append(evidence, authoringv2.ConstructionPivotDiscoveryCategory{
			Key: category.Key, Name: name, Label: label,
		})
	}
	return evidence, nil
}

func constructionUnresolvedPivotDiscovery(construction *authoringv2.Construction) string {
	if construction == nil {
		return ""
	}
	for _, step := range construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot &&
			step.Operation.Pivot != nil && step.Operation.Pivot.Discovery != nil {
			return step.ID
		}
	}
	return ""
}

func (s constructionRowSources) constructionPivotSourceCapability(
	ctx context.Context,
	base Base,
	outputID, stageID string,
) (ConstructionPivotSourceCapability, error) {
	result := ConstructionPivotSourceCapability{StageID: stageID, Choices: []ConstructionPivotSourceChoice{}}
	if importedSourceChoiceUnavailable(base.Document) {
		result.Supported = true
		return result, nil
	}
	stage, found := constructionReceiptStage(base.Stages, stageID)
	if !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	enumeration := rowSourceEnumeration{
		rootReachable:     constructionRootProjectionCanReachStage(base.Construction, stageID),
		plannerConfigured: s.planner != nil,
	}
	if enumeration.rootReachable && enumeration.plannerConfigured {
		choices, err := s.planner.ListRowChoices(ctx, base.Snapshot.Clone(), base.Document)
		if err != nil {
			result.ReasonCode, result.Reason = rowSourceInventoryFailure(pivotSourceReasons)
			return result, unavailable("construction-capabilities", result.ReasonCode, result.Reason,
				fmt.Errorf("list authorized root Pivot source choices for output %q: %w", outputID, err))
		}
		for _, choice := range choices {
			if rowSourceChoiceIsStaleVersion(choice, base.Snapshot) {
				enumeration.skip(rowSourceSkipVersion)
				continue
			}
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				enumeration.skip(rowSourceSkipRoute)
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.Snapshot, choice)
			if !ok {
				enumeration.skip(rowSourceCandidateSkipClass(base.Snapshot, choice))
				continue
			}
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRootProjectionColumnID(base.Construction, stageID, choice.Path),
				OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path, Label: constructionFieldDisplayLabel(base.Document.RootResourceType, choice.Path, candidate.Label, choice.Label),
				FHIRType: choice.FHIRType, LogicalType: candidate.LogicalType, ValueType: choice.ValueType,
				IsIdentifier: fhirIdentifierPath(choice.Path), IsPopulated: candidate.Populated,
			})
		}
	}
	if stage.ActiveRelatedRecord != nil && constructionStageSupportsRelatedField(stage) {
		active := stage.ActiveRelatedRecord
		candidates := make([]capability.Candidate, 0)
		for _, candidate := range base.Snapshot.Candidates {
			if candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType || !candidate.Populated ||
				(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
				len(candidate.RepeatedBoundaries) != 0 || !containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
				!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
				continue
			}
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		for _, candidate := range candidates {
			proved, err := proveConstructionCandidate(ctx, base.Authorized, active.TargetResourceType, candidate, nil)
			if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
				(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 ||
				!relatedFieldPathExecutable(proved.FieldPath) || !relatedFieldLogicalTypeExecutable(proved.LogicalType) {
				continue
			}
			choice, err := capability.NewConstructionRelatedFieldChoice(base.Snapshot.Token, stageID, proved)
			if err != nil {
				continue
			}
			fhirType := constructionFHIRScalarType(active.TargetResourceType, proved.FieldPath)
			if fhirType == "" {
				continue
			}
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRelatedFieldColumnID(base.Construction, stageID, proved),
				OccurrenceID: "activeRelatedRecord", FieldPath: proved.FieldPath,
				Label:    constructionFieldDisplayLabel(active.TargetResourceType, proved.FieldPath, proved.Label),
				FHIRType: fhirType, LogicalType: proved.LogicalType, ValueType: proved.LogicalType,
				IsIdentifier: fhirIdentifierPath(proved.FieldPath), IsPopulated: proved.Populated,
			})
		}
	}
	sort.SliceStable(result.Choices, func(i, j int) bool {
		if result.Choices[i].OccurrenceID != result.Choices[j].OccurrenceID {
			return result.Choices[i].OccurrenceID < result.Choices[j].OccurrenceID
		}
		if result.Choices[i].FieldPath != result.Choices[j].FieldPath {
			return result.Choices[i].FieldPath < result.Choices[j].FieldPath
		}
		return result.Choices[i].ChoiceID < result.Choices[j].ChoiceID
	})
	if len(result.Choices) == 0 {
		result.ReasonCode, result.Reason = enumeration.decision(pivotSourceReasons)
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func (s constructionRowSources) constructionCandidateWithPivotSources(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	authorizedHelpers := make(map[string]bool)
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyPivotSources := request.PivotSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == authoringv2.ConstructionOperationPivot
	clearOwner := ""
	if applyPivotSources {
		clearOwner = request.ChangedStepID
	}
	candidate = preserveConstructionOwnedInputs(base.Construction, candidate, clearOwner)
	if !applyPivotSources {
		return candidate, authorizedHelpers, nil
	}

	pivotIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	if pivotIndex < 0 || candidate.Steps[pivotIndex].Operation.Kind != authoringv2.ConstructionOperationPivot || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_REQUIRES_PIVOT", "pivotSources can only be applied to a Pivot step", nil)
	}
	pivotID := request.ChangedStepID
	if len(request.PivotSources) == 0 {
		cleared, err := clearConstructionPivotInputs(base.Construction, candidate, pivotID)
		if err != nil {
			return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, authorizedHelpers, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[pivotIndex])
	anchorStageID := constructionOwnerAnchorStageID(base.Construction, pivotID)
	if anchorStageID == "" {
		anchorStageID = acceptedStageID
	}
	if acceptedStageID == "" || anchorStageID == "" {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", "the Pivot input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
		return authoringv2.Construction{}, nil, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the Pivot source choices are not bound to an accepted stage", nil, nil)
	}
	if _, found := constructionReceiptStage(base.Stages, anchorStageID); !found {
		return authoringv2.Construction{}, nil, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the Pivot source input anchor is not present in the accepted compilation", nil, nil)
	}

	resolved := make([]resolvedConstructionPivotSource, 0, len(request.PivotSources))
	for _, selection := range request.PivotSources {
		item, err := s.resolveConstructionPivotSource(ctx, base, request, acceptedStageID, anchorStageID, selection)
		if err != nil {
			return authoringv2.Construction{}, nil, err
		}
		resolved = append(resolved, item)
	}
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].selection.ColumnID < resolved[j].selection.ColumnID
	})

	oldOwned := make(map[string]bool)
	for _, step := range base.Construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			oldOwned[step.ID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps)+len(resolved))
	for _, step := range candidate.Steps {
		if oldOwned[step.ID] {
			continue
		}
		steps = append(steps, step)
	}
	pivotIndex = constructionStepIndex(steps, pivotID)
	if pivotIndex < 0 {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_REQUIRES_PIVOT", "the candidate omits the Pivot being edited", nil)
	}

	projections := make([]authoringv2.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != pivotID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.Construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != pivotID && !hasPivotSourceProjection(projections, projection.ColumnID) &&
			!constructionOwnerInputsClearedFromCandidate(base.Construction, candidate, projection.OwnerStepID) {
			projections = append(projections, projection)
		}
	}
	for _, item := range resolved {
		if item.root == nil {
			continue
		}
		projections = append(projections, *item.root)
	}
	candidate.SourceProjections = projections

	for index := 0; index < pivotIndex; index++ {
		step := &steps[index]
		if !authoringv2.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			if hasNewRootPivotSource(resolved) {
				return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
			}
			continue
		}
		for _, item := range resolved {
			if item.root == nil {
				continue
			}
			step.Outputs = appendStageColumnIfAbsent(step.Outputs, authoringv2.StageColumn{
				ID: item.root.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(item.root.ColumnID),
				Label: item.root.Label, Type: item.root.LogicalType,
			})
		}
	}

	pivot := &steps[pivotIndex]
	pivotAnchor := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
	if anchorStageID != recipe.ConstructionSourceProjectionID {
		pivotAnchor = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: anchorStageID}
	}
	currentInputColumns, err := constructionAuthoringStageColumns(base, steps, anchorStageID, projections)
	if err != nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
	}
	for _, item := range resolved {
		if item.root != nil {
			currentInputColumns = appendStageColumnIfAbsent(currentInputColumns, authoringv2.StageColumn{
				ID: item.root.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(item.root.ColumnID),
				Label: item.root.Label, Type: item.root.LogicalType,
			})
		}
	}
	inserted := make([]authoringv2.ConstructionStep, 0, len(resolved))
	for _, item := range resolved {
		if item.related == nil {
			continue
		}
		helperID := constructionPivotSourceHelperID(pivotID, item.selection.ColumnID)
		choice, err := capability.NewConstructionRelatedFieldChoice(base.Snapshot.Token, constructionRefStageID(pivotAnchor), sourceCandidate(base.Snapshot, *item.related))
		if err != nil {
			return authoringv2.Construction{}, nil, unavailable("construction-proposal", "PIVOT_SOURCE_CHOICE_UNAVAILABLE", "the selected related field could not be reissued for the rebuilt input chain", err)
		}
		step := authoringv2.ConstructionStep{
			ID: helperID, OwnerStepID: pivotID,
			Inputs: []authoringv2.ConstructionInputRef{pivotAnchor},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
				ChoiceID: choice.ChoiceID, Source: *item.related, OutputColumnID: item.selection.ColumnID,
			}},
			Outputs: append([]authoringv2.StageColumn(nil), currentInputColumns...),
		}
		pivotSourceOutput := authoringv2.StageColumn{
			ID: item.selection.ColumnID, Name: constructionPivotSourceOutputName(item.selection.ColumnID),
			Label: item.label, Type: item.related.LogicalType, Nullable: true,
		}
		pivotSourceOutput = preserveExistingPivotSourceTable(base.Construction, pivotID, helperID, *item.related, pivotSourceOutput)
		step.Outputs = appendStageColumnIfAbsent(step.Outputs, pivotSourceOutput)
		inserted = append(inserted, step)
		currentInputColumns = append([]authoringv2.StageColumn(nil), step.Outputs...)
		pivotAnchor = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: helperID}
		authorizedHelpers[helperID] = true
	}

	pivot = &steps[pivotIndex]
	pivot.Inputs = []authoringv2.ConstructionInputRef{pivotAnchor}
	steps = append(steps[:pivotIndex], append(inserted, steps[pivotIndex:]...)...)
	candidate.Steps = steps
	if err := validatePivotSourceColumns(candidate.Steps, pivotID, resolved); err != nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, authorizedHelpers, nil
}

func (s constructionRowSources) resolveConstructionPivotSource(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	acceptedStageID, anchorStageID string,
	selection ConstructionPivotSourceSelection,
) (resolvedConstructionPivotSource, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err == nil {
		if s.resolver == nil {
			return resolvedConstructionPivotSource{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized Pivot source choice resolution is not configured", nil)
		}
		resolved, resolveErr := s.resolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: base.Snapshot.Clone(), Route: base.Document.Route,
			RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
		})
		if resolveErr != nil || identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest ||
			identity.Kind != capability.RowChoiceFieldGroupKey || identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
			identity.Path != resolved.FieldPath || identity.Reference || identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
			resolved.OccurrenceID != authoringv2.RootOccurrenceID {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
		}
		rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
		if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
		}
		candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
		if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
			!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
			return resolvedConstructionPivotSource{}, unprocessable("construction-proposal", "UNSUPPORTED_PIVOT_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
		}
		index, err := fhirschema.GeneratedIndex()
		if err != nil {
			return resolvedConstructionPivotSource{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
		}
		facts, err := index.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
		if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
			facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
		}
		label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
		projection := authoringv2.ConstructionSourceProjection{
			ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
			FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
		}
		return resolvedConstructionPivotSource{selection: selection, root: &projection, choiceID: selection.ChoiceID, label: label}, nil
	}

	choice, choiceErr := capability.DecodeConstructionChoiceID(selection.ChoiceID)
	if choiceErr != nil {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected field choice is invalid", nil, choiceErr)
	}
	source, ok := choice.Source.(capability.RelatedFieldChoiceSource)
	if !ok || choice.SnapshotToken != base.Snapshot.Token || source.StageID != acceptedStageID {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "reload the active related field choice for the current Pivot input stage", nil, nil)
	}
	acceptedStage, found := constructionReceiptStage(base.Stages, acceptedStageID)
	anchorStage, anchorFound := constructionReceiptStage(base.Stages, anchorStageID)
	if !found || !anchorFound || acceptedStage.ActiveRelatedRecord == nil || anchorStage.ActiveRelatedRecord == nil ||
		*acceptedStage.ActiveRelatedRecord != *anchorStage.ActiveRelatedRecord {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the active related record is not preserved at the rebuilt Pivot input", nil, nil)
	}
	candidate, found := uniqueCapabilityCandidate(base.Snapshot, source.CandidateID)
	active := anchorStage.ActiveRelatedRecord
	if !found || candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
		candidate.FieldPath != source.Path || candidate.LogicalType != source.LogicalType || !candidate.Populated ||
		(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the selected scalar is no longer available on the active related resource", nil, nil)
	}
	proved, err := proveConstructionCandidate(ctx, base.Authorized, active.TargetResourceType, candidate, nil)
	if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
		proved.FieldPath != source.Path || proved.LogicalType != source.LogicalType || !proved.Populated ||
		(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the selected scalar is no longer supported by the compiler", nil, err)
	}
	if source.NodeID != proved.NodeID || source.ResourceType != proved.ResourceType || source.Cardinality != proved.Cardinality {
		return resolvedConstructionPivotSource{}, unprocessable("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected related field does not match its signed source identity", nil)
	}
	related := &authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: proved.ID, NodeID: proved.NodeID,
		ResourceType: proved.ResourceType, Path: proved.FieldPath, Cardinality: proved.Cardinality,
		LogicalType: proved.LogicalType, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), proved.RepeatedBoundaries...),
	}
	label := constructionFieldDisplayLabel(proved.ResourceType, proved.FieldPath, proved.Label)
	return resolvedConstructionPivotSource{selection: selection, related: related, choiceID: selection.ChoiceID, label: label}, nil
}

func (s *Service) resolveConstructionPivotDiscoveries(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	resolvedSteps := make(map[string]bool)
	removed := make(map[string]bool, len(request.RemoveStepIDs))
	for _, stepID := range request.RemoveStepIDs {
		removed[stepID] = true
	}
	for index := range candidate.Steps {
		step := candidate.Steps[index]
		if removed[step.ID] {
			continue
		}
		pivot := step.Operation.Pivot
		if step.Operation.Kind != authoringv2.ConstructionOperationPivot || pivot == nil || pivot.Discovery == nil {
			continue
		}
		if len(pivot.Categories) != 0 {
			return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE",
				fmt.Sprintf("Pivot %s cannot combine explicit categories with discovery", step.ID), nil)
		}
		inputStageID := constructionStepInputStageID(step)
		if inputStageID == "" {
			return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE",
				fmt.Sprintf("Pivot %s discovery requires exactly one input stage", step.ID), nil)
		}
		discoveryRequest := ConstructionCategoryDiscoveryRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
			ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
			OutputID: request.OutputID, StageID: inputStageID,
			CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
			GroupKeyIDs:           append([]string(nil), pivot.GroupKeyIDs...),
			PivotStepID:           step.ID,
			CandidateConstruction: &candidate,
			compileCandidate:      true,
		}
		if step.ID == request.ChangedStepID {
			discoveryRequest.PivotSources = append([]ConstructionPivotSourceSelection(nil), request.PivotSources...)
		}
		discovery, err := s.DiscoverConstructionCategories(ctx, discoveryRequest)
		if err != nil {
			return candidate, nil, err
		}
		if discovery.Outcome != constructionCategoryDiscoveryComplete {
			return candidate, nil, constructionPivotDiscoveryOutcomeError(step.ID, discovery)
		}
		if len(discovery.Categories) == 0 {
			return candidate, nil, unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
				fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
		}
		input, err := constructionAuthoringStageColumns(base, candidate.Steps, inputStageID, candidate.SourceProjections)
		if err != nil {
			return candidate, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE",
				fmt.Sprintf("Pivot %s discovery input stage is unavailable: %v", step.ID, err), err)
		}
		evidence, err := constructionPivotDiscoveryEvidence(discovery.Categories, pivot.GroupKeyIDs, input)
		if err != nil {
			return candidate, nil, unprocessable("construction-proposal", "INVALID_PIVOT_DISCOVERY",
				fmt.Sprintf("Pivot %s discovery evidence is invalid: %v", step.ID, err), err)
		}
		resolved, err := authoringv2.ResolveConstructionPivotDiscovery(step, input, evidence)
		if err != nil {
			return candidate, nil, unprocessable("construction-proposal", "INVALID_PIVOT_DISCOVERY",
				fmt.Sprintf("Pivot %s discovery could not be resolved: %v", step.ID, err), err)
		}
		candidate.Steps[index] = resolved
		resolvedSteps[step.ID] = true
	}
	return candidate, resolvedSteps, nil
}
