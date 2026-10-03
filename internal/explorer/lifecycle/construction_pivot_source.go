package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validateConstructionPivotSourceSelections(selections []ConstructionPivotSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := requireExactIdentity(selection.ChoiceID, fmt.Sprintf("pivotSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, fmt.Sprintf("pivotSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("pivotSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func (s *Service) constructionPivotSourceCapability(
	ctx context.Context,
	base constructionBase,
	outputID, stageID string,
) (ConstructionPivotSourceCapability, error) {
	result := ConstructionPivotSourceCapability{StageID: stageID, Choices: []ConstructionPivotSourceChoice{}}
	stage, found := constructionReceiptStage(base.stages, stageID)
	if !found {
		result.ReasonCode = "STALE_STAGE_REFERENCE"
		result.Reason = "the selected stage is not present in the current compiled output"
		return result, nil
	}
	if constructionRootProjectionCanReachStage(base.construction, stageID) && s.config.RowChoicePlanner != nil {
		choices, err := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
		if err != nil {
			return result, fmt.Errorf("list authorized root Pivot source choices for output %q: %w", outputID, err)
		}
		for _, choice := range choices {
			if choice.Kind != capability.RowChoiceFieldGroupKey || choice.OccurrenceID != authoringv2.RootOccurrenceID || choice.Reference ||
				choice.Cardinality != capability.RowChoiceOne || canonicalRowPath(choice.Path) == "resourceType" {
				continue
			}
			candidate, ok := constructionGroupSourceCandidate(base.snapshot, choice)
			if !ok {
				continue
			}
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRootProjectionColumnID(base.construction, stageID, choice.Path),
				OccurrenceID: choice.OccurrenceID, FieldPath: choice.Path, Label: constructionFHIRPathLabel(base.document.RootResourceType, choice.Path),
				FHIRType: choice.FHIRType, LogicalType: candidate.LogicalType, ValueType: choice.ValueType,
				IsIdentifier: fhirIdentifierPath(choice.Path), IsPopulated: candidate.Populated,
			})
		}
	}
	if stage.ActiveRelatedRecord != nil && constructionStageSupportsRelatedField(stage) {
		active := stage.ActiveRelatedRecord
		candidates := make([]capability.Candidate, 0)
		for _, candidate := range base.snapshot.Candidates {
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
			proved, err := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
			if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
				(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 ||
				!relatedFieldPathExecutable(proved.FieldPath) || !relatedFieldLogicalTypeExecutable(proved.LogicalType) {
				continue
			}
			choice, err := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, stageID, proved)
			if err != nil {
				continue
			}
			fhirType := constructionFHIRScalarType(active.TargetResourceType, proved.FieldPath)
			if fhirType == "" {
				continue
			}
			result.Choices = append(result.Choices, ConstructionPivotSourceChoice{
				ChoiceID: choice.ChoiceID, ColumnID: constructionOwnedRelatedFieldColumnID(base.construction, stageID, proved),
				OccurrenceID: "activeRelatedRecord", FieldPath: proved.FieldPath,
				Label:    constructionFHIRPathLabel(active.TargetResourceType, proved.FieldPath),
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
		result.ReasonCode = "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS"
		result.Reason = "the selected stage has no authorized populated scalar FHIR fields that can be carried into Pivot"
		return result, nil
	}
	result.Supported = true
	return result, nil
}

func constructionRootProjectionCanReachStage(construction authoringv2.Construction, stageID string) bool {
	if stageID == recipe.ConstructionSourceProjectionID {
		return true
	}
	for _, step := range construction.Steps {
		if !constructionOperationCarriesPivotSource(step.Operation.Kind) {
			return false
		}
		if step.ID == stageID {
			return true
		}
	}
	return false
}

func constructionOperationCarriesPivotSource(kind authoringv2.ConstructionOperationKind) bool {
	switch kind {
	case authoringv2.ConstructionOperationDerive, authoringv2.ConstructionOperationFilter, authoringv2.ConstructionOperationExpand,
		authoringv2.ConstructionOperationRelatedSource, authoringv2.ConstructionOperationRelatedExpand,
		authoringv2.ConstructionOperationRelatedEligibility, authoringv2.ConstructionOperationRelatedField:
		return true
	default:
		return false
	}
}

func constructionReceiptStage(stages []explorer.ReceiptConstructionStage, stageID string) (explorer.ReceiptConstructionStage, bool) {
	for _, stage := range stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return explorer.ReceiptConstructionStage{}, false
}

func constructionStageSupportsRelatedField(stage explorer.ReceiptConstructionStage) bool {
	for _, item := range stage.Capabilities {
		if item.Kind == "RELATED_FIELD" {
			return item.Supported
		}
	}
	return false
}

func constructionFHIRScalarType(resourceType, path string) string {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ""
	}
	path = strings.TrimPrefix(strings.TrimSpace(path), resourceType+".")
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(resourceType), path)
	if err != nil || facts.Shape != fhirschema.RowPathScalar || facts.Reference || facts.Cardinality != fhirschema.RowCardinalityOne {
		return ""
	}
	return facts.FHIRType
}

func constructionOwnedRootProjectionColumnID(construction authoringv2.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) ||
			constructionPivotInputStageID(construction, projection.OwnerStepID) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func constructionOwnedRelatedFieldColumnID(construction authoringv2.Construction, stageID string, candidate capability.Candidate) string {
	ownerID := ""
	for _, step := range construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot && constructionStepInputStageID(step) == stageID {
			ownerID = step.ID
			break
		}
	}
	if ownerID == "" {
		return ""
	}
	for _, step := range construction.Steps {
		if step.OwnerStepID != ownerID || step.Operation.Kind != authoringv2.ConstructionOperationRelatedField || step.Operation.RelatedField == nil {
			continue
		}
		source := step.Operation.RelatedField.Source
		if source.CandidateID == candidate.ID && source.NodeID == candidate.NodeID && source.ResourceType == candidate.ResourceType &&
			source.Path == candidate.FieldPath && source.LogicalType == candidate.LogicalType {
			return step.Operation.RelatedField.OutputColumnID
		}
	}
	return ""
}

func constructionPivotInputStageID(construction authoringv2.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.ID == pivotID {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionStepInputStageID(step authoringv2.ConstructionStep) string {
	if len(step.Inputs) != 1 {
		return ""
	}
	switch step.Inputs[0].Kind {
	case authoringv2.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID
	case authoringv2.ConstructionInputStepOutput:
		return step.Inputs[0].StepID
	default:
		return ""
	}
}

func constructionStepIndex(steps []authoringv2.ConstructionStep, id string) int {
	for index, step := range steps {
		if step.ID == id {
			return index
		}
	}
	return -1
}

func constructionPivotSourceHelperID(pivotID, columnID string) string {
	digest := sha256.Sum256([]byte(pivotID + "\x00" + columnID))
	return "pivot_source_" + hex.EncodeToString(digest[:10])
}

func preserveExistingPivotSourceTable(
	construction authoringv2.Construction,
	pivotID, helperID string,
	source authoringv2.ConstructionRelatedFieldSource,
	output authoringv2.StageColumn,
) authoringv2.StageColumn {
	index := constructionStepIndex(construction.Steps, helperID)
	if index < 0 {
		return output
	}
	previous := construction.Steps[index]
	if previous.OwnerStepID != pivotID || previous.Operation.Kind != authoringv2.ConstructionOperationRelatedField ||
		previous.Operation.RelatedField == nil || previous.Operation.RelatedField.OutputColumnID != output.ID ||
		!reflect.DeepEqual(previous.Operation.RelatedField.Source, source) {
		return output
	}
	for _, column := range previous.Outputs {
		if column.ID != output.ID {
			continue
		}
		if column.Table == nil {
			output.Table = nil
		} else {
			table := *column.Table
			if column.Table.Visible != nil {
				visible := *column.Table.Visible
				table.Visible = &visible
			}
			if column.Table.Order != nil {
				order := *column.Table.Order
				table.Order = &order
			}
			output.Table = &table
		}
		break
	}
	return output
}

func constructionFHIRPathLabel(resourceType, path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), resourceType+".")
	return resourceType + "." + path
}

type resolvedConstructionPivotSource struct {
	selection ConstructionPivotSourceSelection
	root      *authoringv2.ConstructionSourceProjection
	related   *authoringv2.ConstructionRelatedFieldSource
	choiceID  string
	label     string
}

// constructionCandidateWithPivotSources resolves the signed source choices,
// moves them into the accepted Pivot's private input chain, and returns the
// generated RELATED_FIELD steps that have already passed authorization.
func (s *Service) constructionCandidateWithPivotSources(
	ctx context.Context,
	base constructionBase,
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
	candidate = preserveConstructionOwnedInputs(base.construction, candidate, clearOwner)
	if !applyPivotSources {
		return candidate, authorizedHelpers, nil
	}

	pivotIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	if pivotIndex < 0 || candidate.Steps[pivotIndex].Operation.Kind != authoringv2.ConstructionOperationPivot || candidate.Steps[pivotIndex].Operation.Pivot == nil {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_REQUIRES_PIVOT", "pivotSources can only be applied to a Pivot step", nil)
	}
	pivotID := request.ChangedStepID
	if len(request.PivotSources) == 0 {
		cleared, err := clearConstructionPivotInputs(base.construction, candidate, pivotID)
		if err != nil {
			return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, authorizedHelpers, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[pivotIndex])
	anchorStageID := constructionOwnerAnchorStageID(base.construction, pivotID)
	if anchorStageID == "" {
		anchorStageID = acceptedStageID
	}
	if acceptedStageID == "" || anchorStageID == "" {
		return authoringv2.Construction{}, nil, unprocessable("construction-proposal", "PIVOT_SOURCE_STAGE_UNAVAILABLE", "the Pivot input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.stages, acceptedStageID); !found {
		return authoringv2.Construction{}, nil, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the Pivot source choices are not bound to an accepted stage", nil, nil)
	}
	if _, found := constructionReceiptStage(base.stages, anchorStageID); !found {
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

	// Editing a saved Pivot starts from the first accepted input before its old
	// owned RELATED_FIELD chain. The chain is rebuilt from signed choices.
	oldOwned := make(map[string]bool)
	for _, step := range base.construction.Steps {
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
	for _, projection := range base.construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != pivotID && !hasPivotSourceProjection(projections, projection.ColumnID) {
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

	// Source projections enter at the source stage. Preserve them through each
	// row-preserving prefix stage that leads to this Pivot.
	for index := 0; index < pivotIndex; index++ {
		step := &steps[index]
		if !constructionOperationCarriesPivotSource(step.Operation.Kind) {
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
		choice, err := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, constructionRefStageID(pivotAnchor), sourceCandidate(base.snapshot, *item.related))
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
		pivotSourceOutput = preserveExistingPivotSourceTable(base.construction, pivotID, helperID, *item.related, pivotSourceOutput)
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

func (s *Service) resolveConstructionPivotSource(
	ctx context.Context,
	base constructionBase,
	request ConstructionProposalRequest,
	acceptedStageID, anchorStageID string,
	selection ConstructionPivotSourceSelection,
) (resolvedConstructionPivotSource, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err == nil {
		if s.config.RowChoiceResolver == nil {
			return resolvedConstructionPivotSource{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized Pivot source choice resolution is not configured", nil)
		}
		resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: base.snapshot.Clone(), Route: base.document.Route,
			RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
		})
		if resolveErr != nil || identity.SnapshotToken != base.snapshot.Token || identity.SchemaDigest != base.snapshot.Identity.SchemaDigest ||
			identity.Kind != capability.RowChoiceFieldGroupKey || identity.Occurrence.OccurrenceID != authoringv2.RootOccurrenceID ||
			identity.Path != resolved.FieldPath || identity.Reference || identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
			resolved.OccurrenceID != authoringv2.RootOccurrenceID {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
		}
		rootNode, ok := base.snapshot.Node(identity.Occurrence.NodeID)
		if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.document.RootResourceType {
			return resolvedConstructionPivotSource{}, conflict("construction-proposal", "INVALID_PIVOT_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
		}
		candidate, found := uniqueRowSourceCandidate(base.snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
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
		label := constructionFHIRPathLabel(base.document.RootResourceType, facts.CanonicalPath)
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
	if !ok || choice.SnapshotToken != base.snapshot.Token || source.StageID != acceptedStageID {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "reload the active related field choice for the current Pivot input stage", nil, nil)
	}
	acceptedStage, found := constructionReceiptStage(base.stages, acceptedStageID)
	anchorStage, anchorFound := constructionReceiptStage(base.stages, anchorStageID)
	if !found || !anchorFound || acceptedStage.ActiveRelatedRecord == nil || anchorStage.ActiveRelatedRecord == nil ||
		*acceptedStage.ActiveRelatedRecord != *anchorStage.ActiveRelatedRecord {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the active related record is not preserved at the rebuilt Pivot input", nil, nil)
	}
	candidate, found := uniqueCapabilityCandidate(base.snapshot, source.CandidateID)
	active := anchorStage.ActiveRelatedRecord
	if !found || candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
		candidate.FieldPath != source.Path || candidate.LogicalType != source.LogicalType || !candidate.Populated ||
		(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return resolvedConstructionPivotSource{}, conflict("construction-proposal", "STALE_PIVOT_SOURCE_CHOICE", "the selected scalar is no longer available on the active related resource", nil, nil)
	}
	proved, err := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
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
	label := constructionFHIRPathLabel(proved.ResourceType, proved.FieldPath)
	return resolvedConstructionPivotSource{selection: selection, related: related, choiceID: selection.ChoiceID, label: label}, nil
}

func preserveConstructionOwnedInputs(base authoringv2.Construction, candidate authoringv2.Construction, skipOwnerID string) authoringv2.Construction {
	present := make(map[string]bool, len(candidate.Steps))
	for _, step := range candidate.Steps {
		present[step.ID] = true
	}
	for baseIndex, owner := range base.Steps {
		if owner.ID == skipOwnerID || owner.Operation.Kind != authoringv2.ConstructionOperationPivot || constructionStepIndex(candidate.Steps, owner.ID) < 0 {
			continue
		}
		missing := make([]authoringv2.ConstructionStep, 0)
		for _, step := range base.Steps[:baseIndex] {
			if step.OwnerStepID == owner.ID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField && !present[step.ID] {
				missing = append(missing, step)
				present[step.ID] = true
			}
		}
		index := constructionStepIndex(candidate.Steps, owner.ID)
		if len(missing) != 0 && index >= 0 {
			candidate.Steps = append(candidate.Steps[:index], append(missing, candidate.Steps[index:]...)...)
		}
		for _, projection := range base.SourceProjections {
			if projection.OwnerStepID == owner.ID && !hasPivotSourceProjection(candidate.SourceProjections, projection.ColumnID) {
				candidate.SourceProjections = append(candidate.SourceProjections, projection)
			}
		}
	}
	return candidate
}

func clearConstructionPivotInputs(base, candidate authoringv2.Construction, pivotID string) (authoringv2.Construction, error) {
	pivotIndex := constructionStepIndex(candidate.Steps, pivotID)
	if pivotIndex < 0 {
		return authoringv2.Construction{}, fmt.Errorf("the edited Pivot step is missing")
	}
	anchorStageID := constructionOwnerAnchorStageID(base, pivotID)
	if anchorStageID == "" {
		anchorStageID = constructionStepInputStageID(candidate.Steps[pivotIndex])
	}
	if anchorStageID == "" {
		return authoringv2.Construction{}, fmt.Errorf("the owned Pivot input anchor is unavailable")
	}
	oldProjectionIDs := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == pivotID {
			oldProjectionIDs[projection.ColumnID] = true
		}
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
	for _, step := range candidate.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			continue
		}
		if constructionStepIndex(candidate.Steps, step.ID) < pivotIndex {
			filtered := step.Outputs[:0]
			for _, output := range step.Outputs {
				if !oldProjectionIDs[output.ID] {
					filtered = append(filtered, output)
				}
			}
			step.Outputs = filtered
		}
		steps = append(steps, step)
	}
	pivotIndex = constructionStepIndex(steps, pivotID)
	pivot := &steps[pivotIndex]
	pivot.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}
	if anchorStageID != recipe.ConstructionSourceProjectionID {
		pivot.Inputs[0] = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: anchorStageID}
	}
	projections := candidate.SourceProjections[:0]
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != pivotID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnerAnchorStageID(construction authoringv2.Construction, pivotID string) string {
	for _, step := range construction.Steps {
		if step.OwnerStepID == pivotID && step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			return constructionStepInputStageID(step)
		}
	}
	return ""
}

func constructionRefStageID(input authoringv2.ConstructionInputRef) string {
	if input.Kind == authoringv2.ConstructionInputSourceProjection {
		return recipe.ConstructionSourceProjectionID
	}
	return input.StepID
}

func constructionAuthoringStageColumns(base constructionBase, steps []authoringv2.ConstructionStep, stageID string, projections []authoringv2.ConstructionSourceProjection) ([]authoringv2.StageColumn, error) {
	if stageID != recipe.ConstructionSourceProjectionID {
		if index := constructionStepIndex(steps, stageID); index >= 0 {
			return append([]authoringv2.StageColumn(nil), steps[index].Outputs...), nil
		}
	}
	if stage, found := constructionReceiptStage(base.stages, stageID); found {
		result := make([]authoringv2.StageColumn, 0, len(stage.Columns)+len(projections))
		for _, column := range stage.Columns {
			result = append(result, authoringv2.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type})
		}
		for _, projection := range projections {
			result = appendStageColumnIfAbsent(result, authoringv2.StageColumn{
				ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		return result, nil
	}
	return nil, fmt.Errorf("stage %q is not available", stageID)
}

func appendStageColumnIfAbsent(columns []authoringv2.StageColumn, column authoringv2.StageColumn) []authoringv2.StageColumn {
	for _, current := range columns {
		if current.ID == column.ID {
			return columns
		}
	}
	return append(columns, column)
}

func constructionPivotSourceOutputName(columnID string) string {
	digest := sha256.Sum256([]byte(columnID))
	return "__pivot_source_" + hex.EncodeToString(digest[:8])
}

func hasPivotSourceProjection(projections []authoringv2.ConstructionSourceProjection, columnID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID {
			return true
		}
	}
	return false
}

func hasNewRootPivotSource(sources []resolvedConstructionPivotSource) bool {
	for _, source := range sources {
		if source.root != nil {
			return true
		}
	}
	return false
}

func uniqueRowSourceCandidate(snapshot capability.Snapshot, nodeID, resourceType, path string) (capability.Candidate, bool) {
	var result capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID != nodeID || candidate.ResourceType != resourceType || canonicalRowPath(candidate.FieldPath) != canonicalRowPath(path) {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		result, found = candidate, true
	}
	return result, found
}

func sourceCandidate(snapshot capability.Snapshot, source authoringv2.ConstructionRelatedFieldSource) capability.Candidate {
	candidate, _ := uniqueCapabilityCandidate(snapshot, source.CandidateID)
	return candidate
}

func validatePivotSourceColumns(steps []authoringv2.ConstructionStep, pivotID string, sources []resolvedConstructionPivotSource) error {
	index := constructionStepIndex(steps, pivotID)
	if index < 0 || steps[index].Operation.Pivot == nil {
		return fmt.Errorf("the selected Pivot step is unavailable")
	}
	used := make(map[string]bool)
	pivot := steps[index].Operation.Pivot
	for _, id := range pivot.GroupKeyIDs {
		used[id] = true
	}
	used[pivot.CategoryColumnID], used[pivot.ValueColumnID] = true, true
	for _, source := range sources {
		if !used[source.selection.ColumnID] {
			return fmt.Errorf("pivotSources columnId %q is not used by the Pivot row key, category, or value", source.selection.ColumnID)
		}
	}
	return nil
}
