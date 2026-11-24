package construction

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func analyzeConstructionCandidateWithCascade(
	base ws.Document,
	candidate ws.Construction,
	changedStepID string,
	requestedRemovals []string,
	baseStages []receiptmodel.ReceiptConstructionStage,
) (ws.Document, ws.ConstructionImpact, error) {
	removeStepIDs := append([]string(nil), requestedRemovals...)
	removed := make(map[string]bool, len(removeStepIDs))
	for _, stepID := range removeStepIDs {
		removed[stepID] = true
	}
	invalidatedProviders := append([]string(nil), requestedRemovals...)
	if constructionRelatedExpandTargetChanged(base.Construction, candidate, changedStepID) {
		invalidatedProviders = append(invalidatedProviders, changedStepID)
	}
	seedImpact := ws.ConstructionImpact{RemovedStepIDs: invalidatedProviders}
	seedImpact = constructionImpactForRemovedRelatedExpandAnchors(baseStages, &candidate, seedImpact)
	for _, stepID := range constructionStepsToRemoveForMissingInputs(candidate.Steps, seedImpact.MissingInputs, changedStepID, removed) {
		removed[stepID] = true
		removeStepIDs = append(removeStepIDs, stepID)
	}

	for {
		candidateAttempt := candidate
		candidateAttempt.Steps = make([]ws.ConstructionStep, 0, len(candidate.Steps))
		for _, step := range candidate.Steps {
			if !removed[step.ID] {
				candidateAttempt.Steps = append(candidateAttempt.Steps, step)
			}
		}
		candidateDocument, impact, err := base.AnalyzeConstructionCandidate(candidateAttempt, changedStepID, removeStepIDs)
		if err != nil {
			return ws.Document{}, ws.ConstructionImpact{}, err
		}
		impact = constructionImpactForRemovedRelatedExpandAnchors(baseStages, candidateDocument.Construction, impact)
		if !impact.HasMissingInputs() {
			return candidateDocument, impact, nil
		}

		newRemovals := constructionStepsToRemoveForMissingInputs(candidateDocument.Construction.Steps, impact.MissingInputs, changedStepID, removed)
		if len(newRemovals) == 0 {
			return candidateDocument, impact, nil
		}
		for _, stepID := range newRemovals {
			removed[stepID] = true
		}
		removeStepIDs = append(removeStepIDs, newRemovals...)
	}
}

func constructionRelatedExpandTargetChanged(base *ws.Construction, candidate ws.Construction, changedStepID string) bool {
	if base == nil || changedStepID == "" {
		return false
	}
	var baseStep, candidateStep *ws.ConstructionStep
	for index := range base.Steps {
		if base.Steps[index].ID == changedStepID {
			baseStep = &base.Steps[index]
			break
		}
	}
	for index := range candidate.Steps {
		if candidate.Steps[index].ID == changedStepID {
			candidateStep = &candidate.Steps[index]
			break
		}
	}
	if baseStep == nil || baseStep.Operation.RelatedExpand == nil {
		return false
	}
	if candidateStep == nil || candidateStep.Operation.RelatedExpand == nil {
		return true
	}
	return baseStep.Operation.RelatedExpand.TargetNodeID != candidateStep.Operation.RelatedExpand.TargetNodeID ||
		baseStep.Operation.RelatedExpand.TargetResourceType != candidateStep.Operation.RelatedExpand.TargetResourceType
}

func constructionStepsToRemoveForMissingInputs(
	steps []ws.ConstructionStep,
	missingInputs []ws.ConstructionDependencyIssue,
	protectedStepID string,
	alreadyRemoved map[string]bool,
) []string {
	stepsByID := make(map[string]ws.ConstructionStep, len(steps))
	for _, step := range steps {
		stepsByID[step.ID] = step
	}
	remove := make([]string, 0, len(missingInputs))
	selected := make(map[string]bool, len(missingInputs))
	for _, missing := range missingInputs {
		if missing.StepID == protectedStepID {
			continue
		}
		step, exists := stepsByID[missing.StepID]
		if !exists {
			continue
		}
		stepID := step.ID
		if step.OwnerStepID != "" {
			stepID = step.OwnerStepID
		}
		if stepID == protectedStepID || alreadyRemoved[stepID] || selected[stepID] {
			continue
		}
		if _, exists := stepsByID[stepID]; !exists {
			continue
		}
		selected[stepID] = true
		remove = append(remove, stepID)
	}
	return remove
}

func constructionImpactForRemovedRelatedExpandAnchors(
	baseStages []receiptmodel.ReceiptConstructionStage,
	candidate *ws.Construction,
	impact ws.ConstructionImpact,
) ws.ConstructionImpact {
	if candidate == nil || len(impact.RemovedStepIDs) == 0 {
		return impact
	}
	removed := make(map[string]bool, len(impact.RemovedStepIDs))
	for _, stepID := range impact.RemovedStepIDs {
		removed[stepID] = true
	}
	removedTerminalIdentities := make(map[string]bool)
	for _, stage := range baseStages {
		if removed[stage.ID] && stage.Operation == string(recipe.ConstructionRelatedExpandOp) && stage.RelatedExpand != nil {
			removedTerminalIdentities[stage.RelatedExpand.TerminalIdentityColumn] = true
		}
	}
	if len(removedTerminalIdentities) == 0 {
		return impact
	}
	for _, step := range candidate.Steps {
		anchorColumnID := ""
		switch step.Operation.Kind {
		case ws.ConstructionOperationRelatedField:
			for _, stage := range baseStages {
				if stage.ID == step.ID && stage.ActiveRelatedRecord != nil &&
					removedTerminalIdentities[stage.ActiveRelatedRecord.TerminalIdentityColumn] {
					anchorColumnID = stage.ActiveRelatedRecord.TerminalIdentityColumn
					break
				}
			}
		case ws.ConstructionOperationRelatedExpand:
			if step.Operation.RelatedExpand != nil {
				anchorColumnID = step.Operation.RelatedExpand.AnchorColumnID
			}
		case ws.ConstructionOperationRelatedEligibility:
			if step.Operation.RelatedEligibility != nil {
				anchorColumnID = step.Operation.RelatedEligibility.AnchorColumnID
			}
		}
		if anchorColumnID == "" || !removedTerminalIdentities[anchorColumnID] {
			continue
		}
		issue := ws.ConstructionDependencyIssue{StepID: step.ID, ColumnID: anchorColumnID}
		alreadyReported := false
		for _, existing := range impact.MissingInputs {
			if existing == issue {
				alreadyReported = true
				break
			}
		}
		if !alreadyReported {
			impact.MissingInputs = append(impact.MissingInputs, issue)
		}
	}
	return impact
}

func reauthorizeConstructionRelatedField(
	ctx context.Context,
	base Base,
	step ws.ConstructionStep,
	related ws.ConstructionRelatedField,
) error {
	inputStageID, err := relatedFieldInputStageID(step)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	var inputStage *receiptmodel.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == inputStageID {
			inputStage = &base.Stages[index]
			break
		}
	}
	if inputStage == nil {
		return conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the related field input stage is not in the current compiled output", nil, nil)
	}
	active := inputStage.ActiveRelatedRecord
	if active == nil {
		return unprocessable("construction-proposal", "NO_ACTIVE_RELATED_RECORD", "the related field input stage does not retain an exact terminal resource identity", nil)
	}
	supported := false
	for _, operation := range inputStage.Capabilities {
		if operation.Kind == "RELATED_FIELD" {
			supported = operation.Supported
			break
		}
	}
	if !supported {
		return unprocessable("construction-proposal", "NO_ACTIVE_RELATED_RECORD", "the related field input stage cannot project an exact terminal field", nil)
	}
	if related.Source.Kind != capability.ConstructionChoiceSourceField || related.Source.NodeID != active.TargetNodeID ||
		related.Source.ResourceType != active.TargetResourceType || len(related.Source.RepeatedBoundaries) != 0 {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field source does not identify a scalar on the active terminal resource", nil)
	}
	choice, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field choice identity is invalid", err)
	}
	if choice.SnapshotToken != base.Snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the exact related field for the current authorized snapshot", nil, nil)
	}
	choiceSource, ok := choice.Source.(capability.RelatedFieldChoiceSource)
	if !ok || choiceSource.StageID != inputStageID || choiceSource.CandidateID != related.Source.CandidateID ||
		choiceSource.NodeID != active.TargetNodeID || choiceSource.ResourceType != active.TargetResourceType ||
		choiceSource.Path != related.Source.Path || choiceSource.Cardinality != related.Source.Cardinality ||
		choiceSource.LogicalType != related.Source.LogicalType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field source differs from its stage-bound server choice", nil)
	}
	candidate, found := uniqueCapabilityCandidate(base.Snapshot, related.Source.CandidateID)
	if !found || candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
		candidate.FieldPath != related.Source.Path || (candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
		len(candidate.RepeatedBoundaries) != 0 || !relatedFieldPathExecutable(candidate.FieldPath) ||
		!relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the exact scalar candidate is no longer available on the active terminal resource", nil, nil)
	}
	proved, err := proveConstructionCandidate(ctx, base.Authorized, active.TargetResourceType, candidate, nil)
	if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
		proved.FieldPath != related.Source.Path || proved.Cardinality != related.Source.Cardinality || proved.LogicalType != related.Source.LogicalType ||
		len(proved.RepeatedBoundaries) != 0 || !relatedFieldPathExecutable(proved.FieldPath) ||
		!relatedFieldLogicalTypeExecutable(proved.LogicalType) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected scalar field is no longer supported by the compiler", nil, err)
	}
	reissued, err := capability.NewConstructionRelatedFieldChoice(base.Snapshot.Token, inputStageID, proved)
	if err != nil || reissued.ChoiceID != related.ChoiceID || reissued.Source.CandidateID != related.Source.CandidateID ||
		reissued.Source.NodeID != related.Source.NodeID || reissued.Source.ResourceType != related.Source.ResourceType ||
		reissued.Source.Path != related.Source.Path || reissued.Source.Cardinality != related.Source.Cardinality {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the related field choice could not be reissued from the current authorized candidate", err)
	}
	return nil
}

func constructionCodedGroupChoices(
	base Base,
	outputID string,
	stage receiptmodel.ReceiptConstructionStage,
) ([]receiptmodel.ReceiptConstructionCodedGroupChoice, error) {
	if importedSourceRootChoiceInventoryMissing(base) {
		return []receiptmodel.ReceiptConstructionCodedGroupChoice{}, nil
	}
	if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn != "_key" ||
		!constructionStageSupportsCodedGroup(stage) {
		return nil, nil
	}
	occurrence, err := resolveRowChoiceOccurrence(
		base.Snapshot, base.Document.RootResourceType, base.Document.Route,
		ws.RootOccurrenceID,
	)
	if err != nil {
		return nil, fmt.Errorf("the direct root occurrence is not available for coded grouping: %w", err)
	}
	if len(occurrence.Route) != 0 {
		return nil, fmt.Errorf("the direct root occurrence unexpectedly includes a route")
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, err
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(base.Document.RootResourceType))
	if err != nil {
		return nil, err
	}
	populatedPaths := make(map[string]struct{})
	for _, candidate := range base.Snapshot.Candidates {
		if candidate.NodeID == occurrence.NodeID && candidate.ResourceType == occurrence.ResourceType &&
			candidate.Observed && candidate.Populated && candidate.ObservedDocumentCount > 0 {
			populatedPaths[candidate.FieldPath] = struct{}{}
		}
	}
	choices := make([]receiptmodel.ReceiptConstructionCodedGroupChoice, 0, len(paths))
	for _, path := range paths {
		facts, err := index.ResolveRowPath(fhirschema.DefinitionName(base.Document.RootResourceType), path)
		if err != nil || facts.FHIRType != "Coding" || facts.Cardinality != fhirschema.RowCardinalityMany ||
			facts.Shape != fhirschema.RowPathArray || facts.Reference {
			continue
		}
		if _, populated := populatedPaths[path+".code"]; !populated {
			continue
		}
		choice, err := capability.NewConstructionCodedGroupChoice(base.Snapshot, outputID, stage.ID, occurrence, capability.RowChoiceFacts{
			ResourceType: string(facts.ResourceType), CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
			Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray, Reference: facts.Reference,
			Title: facts.Title, Description: facts.Description,
		})
		if err != nil {
			return nil, err
		}
		choices = append(choices, receiptmodel.ReceiptConstructionCodedGroupChoice{
			ChoiceID: choice.ChoiceID, OccurrenceID: choice.OccurrenceID, ResourceType: choice.ResourceType,
			CodingPath: choice.CodingPath, Label: choice.Label,
		})
	}
	return choices, nil
}

func reauthorizeConstructionCodedGroup(
	ctx context.Context,
	base Base,
	outputID string,
	step ws.ConstructionStep,
	coded ws.ConstructionCodedGroup,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireExactIdentity(coded.ChoiceID, "codedGroup.choiceId"); err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "choose a current repeated Coding field before proposing this step", err)
	}
	if len(step.Inputs) != 1 || step.Inputs[0].Kind != ws.ConstructionInputSourceProjection ||
		step.Inputs[0].StepID != "" {
		return unprocessable("construction-proposal", "DIRECT_SOURCE_STAGE_REQUIRED", "coded grouping is currently available only on the direct source projection stage", nil)
	}
	var inputStage *receiptmodel.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == recipe.ConstructionSourceProjectionID {
			inputStage = &base.Stages[index]
			break
		}
	}
	if inputStage == nil || inputStage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedGroup(*inputStage) {
		return unprocessable("construction-proposal", "CODED_GROUP_UNAVAILABLE", "the direct source stage does not expose executable repeated Coding fields", nil)
	}
	if coded.Source.OccurrenceID != ws.RootOccurrenceID || coded.Source.ResourceType != base.Document.RootResourceType ||
		coded.Source.FHIRType != "Coding" || coded.Source.Cardinality != "MANY" || coded.Source.Shape != "ARRAY" || len(coded.Source.Route) != 0 {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "coded grouping source facts must identify a root repeated Coding path with an empty route", nil)
	}
	occurrence, err := resolveRowChoiceOccurrence(base.Snapshot, base.Document.RootResourceType, base.Document.Route, coded.Source.OccurrenceID)
	if err != nil || len(occurrence.Route) != 0 {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the root Coding occurrence is no longer available in the authorized snapshot", nil, err)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated FHIR schema for coded grouping: %w", err)
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(base.Document.RootResourceType))
	if err != nil {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "generated FHIR Coding metadata for the selected root is unavailable", nil, err)
	}
	pathAvailable := false
	for _, path := range paths {
		if path == coded.Source.CodingPath {
			pathAvailable = true
			break
		}
	}
	facts, factErr := index.ResolveRowPath(fhirschema.DefinitionName(base.Document.RootResourceType), coded.Source.CodingPath)
	if !pathAvailable || factErr != nil || facts.FHIRType != "Coding" || facts.Cardinality != fhirschema.RowCardinalityMany ||
		facts.Shape != fhirschema.RowPathArray || facts.Reference {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected Coding path is no longer an executable repeated Coding field", nil, factErr)
	}
	identity, err := capability.DecodeConstructionChoiceID(coded.ChoiceID)
	if err != nil || identity.SnapshotToken != base.Snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload this Coding field from the current authorized snapshot", nil, err)
	}
	choiceSource, ok := identity.Source.(capability.CodedGroupChoiceSource)
	if !ok || identity.Kind != capability.ConstructionChoiceSourceCodedGroup ||
		choiceSource.OutputID != outputID || choiceSource.StageID != recipe.ConstructionSourceProjectionID ||
		choiceSource.OccurrenceID != coded.Source.OccurrenceID || choiceSource.NodeID != occurrence.NodeID ||
		choiceSource.ResourceType != coded.Source.ResourceType || choiceSource.Path != coded.Source.CodingPath ||
		choiceSource.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "coded grouping source differs from its exact stage-bound server choice", nil)
	}
	proved, err := capability.NewConstructionCodedGroupChoice(base.Snapshot, outputID, recipe.ConstructionSourceProjectionID, occurrence, capability.RowChoiceFacts{
		ResourceType: string(facts.ResourceType), CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray, Reference: facts.Reference,
		Title: facts.Title, Description: facts.Description,
	})
	if err != nil || proved.ChoiceID != coded.ChoiceID || proved.CodingPath != coded.Source.CodingPath ||
		proved.OccurrenceID != coded.Source.OccurrenceID || proved.ResourceType != coded.Source.ResourceType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the coded grouping choice cannot be reissued from current generated metadata", err)
	}
	return nil
}

func constructionStageSupportsCodedGroup(stage receiptmodel.ReceiptConstructionStage) bool {
	for _, operation := range stage.Capabilities {
		if operation.Kind == string(recipe.ConstructionCodedGroupOp) {
			return operation.Supported
		}
	}
	return false
}

func relatedFieldInputStageID(step ws.ConstructionStep) (string, error) {
	if len(step.Inputs) != 1 {
		return "", fmt.Errorf("related field requires exactly one input stage")
	}
	input := step.Inputs[0]
	switch input.Kind {
	case ws.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID, nil
	case ws.ConstructionInputStepOutput:
		if strings.TrimSpace(input.StepID) == "" || input.StepID != strings.TrimSpace(input.StepID) {
			return "", fmt.Errorf("related field input step ID must be exact")
		}
		return input.StepID, nil
	default:
		return "", fmt.Errorf("related field input must be a source or prior construction stage")
	}
}

func reauthorizeConstructionRelatedExpand(
	ctx context.Context,
	base Base,
	rootResourceType string,
	step ws.ConstructionStep,
	related ws.ConstructionRelatedExpand,
) error {
	inputStageID, err := relatedExpandInputStageID(step)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	var inputStage *receiptmodel.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == inputStageID {
			inputStage = &base.Stages[index]
			break
		}
	}
	if inputStage == nil {
		return conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the related expansion input stage is not in the current compiled output", nil, nil)
	}
	if !constructionStageSupportsRelatedExpand(*inputStage) {
		return unprocessable("construction-proposal", "UNSUPPORTED_CONSTRUCTION_OPERATION", "the related expansion input stage does not support this operation", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.Snapshot, *inputStage, rootResourceType, related.AnchorColumnID)
	if err != nil {
		return unprocessable("construction-proposal", "NO_SOURCE_ROW_ANCHOR", "the related expansion input stage does not retain the selected exact resource anchor", err)
	}
	choice, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion route choice identity is invalid", err)
	}
	if choice.SnapshotToken != base.Snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion route for the current authorized snapshot", nil, nil)
	}
	source, ok := choice.Source.(capability.RelatedResourceChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion must use an exact related-resource route choice", nil)
	}
	if source.StageID != inputStageID || source.AnchorColumnID != related.AnchorColumnID || source.AnchorKind != anchor.Kind ||
		source.AnchorNodeID != anchor.NodeID || source.AnchorResourceType != anchor.ResourceType ||
		source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
		!reflect.DeepEqual(choice.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion anchor, target, input stage, or route differs from its server-issued choice", nil)
	}
	resolvedRoute, err := reauthorizeConstructionRouteFromAnchor(base.Snapshot, anchor.NodeID, related.TargetNodeID, related.Route)
	if err != nil || !reflect.DeepEqual(resolvedRoute, related.Route) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected related-resource route is no longer available", nil, err)
	}
	if !constructionRouteHasCompilerProof(ctx, base.Authorized, resolvedRoute) {
		return unprocessable("construction-proposal", "UNSUPPORTED_CONSTRUCTION_ROUTE", "the selected related-resource route is no longer supported by the compiler", nil)
	}
	if related.ContributorRule.Predicate == nil {
		return nil
	}
	return reauthorizeRelatedExpandContributor(ctx, base, anchor.ResourceType, related)
}

func reauthorizeConstructionRelatedEligibility(
	ctx context.Context,
	base Base,
	rootResourceType string,
	step ws.ConstructionStep,
	related ws.ConstructionRelatedEligibility,
) error {

	return reauthorizeConstructionRelatedExpand(ctx, base, rootResourceType, step, ws.ConstructionRelatedExpand{
		AnchorColumnID: related.AnchorColumnID, ChoiceID: related.ChoiceID,
		TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
		Route: related.Route, ContributorRule: related.ContributorRule,
		ContributorSource: related.ContributorSource, ContributorChoiceID: related.ContributorChoiceID,
		EmptyPolicy:           ws.ConstructionExpandEmptyExclude,
		RelatedRecordColumnID: "__related_eligibility_probe",
	})
}

func relatedExpandInputStageID(step ws.ConstructionStep) (string, error) {
	if len(step.Inputs) != 1 {
		return "", fmt.Errorf("related expansion requires exactly one input stage")
	}
	input := step.Inputs[0]
	switch input.Kind {
	case ws.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID, nil
	case ws.ConstructionInputStepOutput:
		if strings.TrimSpace(input.StepID) == "" || input.StepID != strings.TrimSpace(input.StepID) {
			return "", fmt.Errorf("related expansion input step ID must be exact")
		}
		return input.StepID, nil
	default:
		return "", fmt.Errorf("related expansion input must be a source or prior construction stage")
	}
}

func reauthorizeRelatedExpandContributor(ctx context.Context, base Base, rootResourceType string, related ws.ConstructionRelatedExpand) error {
	if related.ContributorSource == nil || related.ContributorChoiceID == "" {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor predicate has no exact field choice", nil)
	}
	identity, err := capability.DecodeConstructionChoiceID(related.ContributorChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor choice identity is invalid", err)
	}
	if identity.SnapshotToken != base.Snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion contributor for the current authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.FieldChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor must use an exact field choice", nil)
	}
	authored := related.ContributorSource
	expected := capability.FieldChoiceSource{
		Kind: authored.Kind, CandidateID: authored.CandidateID, NodeID: authored.NodeID, ResourceType: authored.ResourceType,
		Path: authored.Path, Cardinality: authored.Cardinality, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), authored.RepeatedBoundaries...),
	}
	if !reflect.DeepEqual(source, expected) || !reflect.DeepEqual(identity.Route, related.Route) ||
		source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor does not match the exact target route and field", nil)
	}
	form := capability.ConstructionChoiceValue
	if capability.IsRepeatedCardinality(source.Cardinality) {
		candidate, found := uniqueCapabilityCandidate(base.Snapshot, source.CandidateID)
		if !found {
			return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the exact related expansion contributor is no longer available", nil, nil)
		}
		route, routeErr := reauthorizeConstructionRoute(base.Snapshot, rootResourceType, candidate.NodeID, identity.Route)
		if routeErr != nil || !reflect.DeepEqual(route, identity.Route) {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor route is unavailable or changed", routeErr)
		}
		provedCandidate, proofErr := proveConstructionCandidate(ctx, base.Authorized, rootResourceType, candidate, route)
		if proofErr != nil {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor has no compiler-proved field form", proofErr)
		}
		choice, choiceErr := capability.NewFieldConstructionChoiceForRoute(base.Snapshot.Token, route, provedCandidate)
		if choiceErr != nil {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor has no compiler-proved field form", choiceErr)
		}
		hasSupportedForm := false
		for _, option := range choice.Options {
			if option.Support == capability.ConstructionChoiceSupported {
				form, hasSupportedForm = option.Form, true
				break
			}
		}
		if !hasSupportedForm {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor has no compiler-proved field form", nil)
		}
		resolved, _, resolveErr := resolveFieldConstructionChoiceFromProvenCandidate(
			base.Catalog, route,
			authoringv2.ConstructionChoiceSelection{ChoiceID: related.ContributorChoiceID, Form: form},
			identity, source, candidate, provedCandidate, choice,
		)
		if resolveErr != nil {
			return resolveErr
		}
		if resolved.LogicalType != authored.LogicalType || !reflect.DeepEqual(resolved.Route, related.Route) {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor type or route differs from its current compiler-proved choice", nil)
		}
		return nil
	}
	resolved, _, err := resolveFieldConstructionChoice(ctx, base.Authorized, base.Snapshot, base.Catalog, rootResourceType,
		authoringv2.ConstructionChoiceSelection{ChoiceID: related.ContributorChoiceID, Form: form}, identity, source)
	if err != nil {
		return err
	}
	if resolved.LogicalType != authored.LogicalType || !reflect.DeepEqual(resolved.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor type or route differs from its current compiler-proved choice", nil)
	}
	return nil
}

func reauthorizeConstructionRelatedSource(ctx context.Context, base Base, rootResourceType string, related ws.ConstructionRelatedSource) error {
	identity, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source choice identity is invalid", err)
	}
	if identity.SnapshotToken != base.Snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related source choice for the current authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.FieldChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source must use an exact field choice", nil)
	}
	authoredSource := capability.FieldChoiceSource{
		Kind: related.Source.Kind, CandidateID: related.Source.CandidateID, NodeID: related.Source.NodeID,
		ResourceType: related.Source.ResourceType, Path: related.Source.Path, Cardinality: related.Source.Cardinality,
		RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), related.Source.RepeatedBoundaries...),
	}
	if !reflect.DeepEqual(source, authoredSource) || !reflect.DeepEqual(identity.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source route or field does not match its server-issued choice", nil)
	}
	selection := authoringv2.ConstructionChoiceSelection{ChoiceID: related.ChoiceID, Form: related.Form}
	resolved, _, err := resolveFieldConstructionChoice(ctx, base.Authorized, base.Snapshot, base.Catalog, rootResourceType, selection, identity, source)
	if err != nil {
		return err
	}
	if resolved.LogicalType != related.Source.LogicalType || !reflect.DeepEqual(resolved.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source type or route differs from the current compiler-proved choice", nil)
	}
	return nil
}
