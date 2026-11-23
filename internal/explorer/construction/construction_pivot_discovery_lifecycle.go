package construction

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

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
