package receipt

import (
	"fmt"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func (stage constructionOutputStep) reconcileRelatedSource() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	if step.Operation.RelatedSource == nil {
		return fmt.Errorf("related-source step %q has no operation payload", step.ID)
	}
	related := step.Operation.RelatedSource
	if (related.Form != capability.ConstructionChoiceAll && related.Form != capability.ConstructionChoiceCount && related.Form != capability.ConstructionChoicePresence) || related.ContributorRule.Policy != authoringv2.ConstructionRelatedAllMatches {
		return fmt.Errorf("related-source step %q has unsupported output form or contributor policy", step.ID)
	}
	quality := ConstructedOutputQuality{
		Lossless: false, Shape: "array", StructuralSuitability: "requires-review",
		LossReasons: []string{"RELATED_SOURCE_AUTHORIZED_MATCHES_ONLY"},
	}
	if related.Form != capability.ConstructionChoiceAll || related.RowValuePolicy == authoringv2.ConstructionRowValueOne {
		quality.Shape = ""
	}
	if related.Form != capability.ConstructionChoiceAll {
		quality.LossReasons = []string{"RELATED_SOURCE_DISTINCT_RECORD_REDUCTION"}
	}
	if err := stage.addOutput(related.OutputColumnID, step.ID, nil, quality); err != nil {
		return err
	}
	for _, output := range step.Outputs {
		if output.ID == related.OutputColumnID {
			metadata := nextAuthored[output.Name]
			metadata.NodeID = related.Source.NodeID
			metadata.CandidateID = related.Source.CandidateID
			metadata.OccurrenceID = related.SourceOccurrenceID
			metadata.SourceResourceType = related.Source.ResourceType
			metadata.SourcePath = related.Source.Path
			nextAuthored[output.Name] = metadata
			break
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileRelatedExpand() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	if step.Operation.RelatedExpand == nil {
		return fmt.Errorf("related-expansion step %q has no operation payload", step.ID)
	}
	related := step.Operation.RelatedExpand
	quality := ConstructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"RELATED_EXPANSION_CHANGES_ROW_GRAIN"},
	}
	if err := stage.addOutput(related.RelatedRecordColumnID, step.ID, nil, quality); err != nil {
		return err
	}
	for _, output := range step.Outputs {
		if output.ID == related.RelatedRecordColumnID {
			metadata := nextAuthored[output.Name]
			metadata.NodeID = related.TargetNodeID
			metadata.SourceResourceType = related.TargetResourceType
			metadata.SourcePath = "id"
			nextAuthored[output.Name] = metadata
			break
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileRelatedEligibility() error {
	step := stage.step
	if step.Operation.RelatedEligibility == nil {
		return fmt.Errorf("related-eligibility step %q has no operation payload", step.ID)
	}
	return nil
}

func (stage constructionOutputStep) reconcileRelatedField() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	if step.Operation.RelatedField == nil {
		return fmt.Errorf("related-field step %q has no operation payload", step.ID)
	}
	related := step.Operation.RelatedField
	quality := ConstructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"RELATED_FIELD_AUTHORIZED_EXACT_RECORD"},
	}
	if err := stage.addOutput(related.OutputColumnID, step.ID, nil, quality); err != nil {
		return err
	}
	for _, output := range step.Outputs {
		if output.ID == related.OutputColumnID {
			metadata := nextAuthored[output.Name]
			metadata.NodeID = related.Source.NodeID
			metadata.CandidateID = related.Source.CandidateID
			metadata.SourceResourceType = related.Source.ResourceType
			metadata.SourcePath = related.Source.Path
			nextAuthored[output.Name] = metadata
			break
		}
	}
	return nil
}
