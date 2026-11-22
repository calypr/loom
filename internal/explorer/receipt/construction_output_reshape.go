package receipt

import (
	"fmt"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"strings"
)

func (stage constructionOutputStep) reconcileExpand() error {
	step := stage.step
	if step.Operation.Expand == nil {
		return fmt.Errorf("expand step %q has no operation payload", step.ID)
	}
	expand := step.Operation.Expand
	quality := ConstructedOutputQuality{
		StructuralSuitability: "requires-review",
		LossReasons:           []string{"TABLE_SHAPE_EXPAND_CHANGES_ROW_GRAIN"},
	}
	if err := stage.addOutput(expand.OutputColumnID, expand.ConstructionID, []string{expand.InputColumnID}, quality); err != nil {
		return err
	}
	if expand.OrdinalColumnID != "" {
		if err := stage.addOutput(expand.OrdinalColumnID, expand.ConstructionID, []string{expand.InputColumnID}, quality); err != nil {
			return err
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcilePivot() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	prior := stage.prior
	authored := stage.authored
	sourceOnlyIDs := stage.sourceOnlyIDs
	if step.Operation.Pivot == nil {
		return fmt.Errorf("pivot step %q has no operation payload", step.ID)
	}
	pivot := step.Operation.Pivot
	dependencies := append([]string(nil), pivot.GroupKeyIDs...)
	dependencies = append(dependencies, pivot.CategoryColumnID, pivot.ValueColumnID)
	quality, err := pivotOutputQuality(string(pivot.DuplicatePolicy), string(pivot.MissingCellPolicy), string(pivot.UnlistedCategoryPolicy))
	if err != nil {
		return fmt.Errorf("pivot step %q: %w", step.ID, err)
	}
	for _, keyID := range pivot.GroupKeyIDs {
		_, privateSource := sourceOnlyIDs[keyID]
		priorMetadata, constructedInput := authored[prior[keyID]]
		if !privateSource && !constructedInput {
			continue
		}
		for _, output := range step.Outputs {
			if output.ID == keyID {
				if privateSource {
					priorMetadata = AuthoredOutputColumn{
						ConstructionID: pivot.ConstructionID, TypedStageOutput: true, Quality: quality,
					}
				}
				priorMetadata.Label = output.Label
				nextAuthored[output.Name] = priorMetadata
				break
			}
		}
	}
	for _, category := range pivot.Categories {
		if err := stage.addOutput(category.OutputColumnID, pivot.ConstructionID, dependencies, quality); err != nil {
			return err
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileCodedPivot() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	if step.Operation.CodedPivot == nil || step.Operation.CodedPivot.Source == nil {
		return fmt.Errorf("coded pivot step %q has no source", step.ID)
	}
	pivot := step.Operation.CodedPivot
	quality, err := pivotOutputQuality(string(pivot.DuplicatePolicy), string(pivot.MissingCellPolicy), "EXCLUDE_WITH_EVIDENCE")
	if err != nil {
		return fmt.Errorf("coded pivot step %q: %w", step.ID, err)
	}
	quality.LossReasons = []string{"CODED_PIVOT_SELECTED_CATEGORIES_ONLY"}
	if pivot.DuplicatePolicy != authoringv2.ConstructionPivotDuplicateError {
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_PIVOT_DUPLICATES_AGGREGATED")
	}
	for _, category := range pivot.Categories {
		if err := stage.addOutput(category.OutputColumnID, pivot.ConstructionID, nil, quality); err != nil {
			return err
		}
		for _, output := range step.Outputs {
			if output.ID == category.OutputColumnID {
				metadata := nextAuthored[output.Name]
				metadata.NodeID = pivot.Source.NodeID
				metadata.CandidateID = pivot.Source.CandidateID
				metadata.SourceResourceType = pivot.Source.Family.ResourceType
				metadata.SourcePath = pivot.Source.Family.ValuePath
				nextAuthored[output.Name] = metadata
				break
			}
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileUnpivot() error {
	step := stage.step
	document := stage.document
	compiledOutput := stage.compiledOutput
	rowSourceSnapshot := stage.rowSourceSnapshot
	prior := stage.prior
	stepIndex := stage.stepIndex
	if step.Operation.Unpivot == nil {
		return fmt.Errorf("unpivot step %q has no operation payload", step.ID)
	}
	unpivot := step.Operation.Unpivot
	dependencies := make([]string, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		dependencies = append(dependencies, input.ColumnID)
	}
	quality, err := unpivotOutputQuality(string(unpivot.NullRowPolicy))
	if err != nil {
		return fmt.Errorf("unpivot step %q: %w", step.ID, err)
	}
	for _, projection := range document.Construction.SourceProjections {
		if projection.OwnerStepID != step.ID {
			continue
		}
		audited := make([]string, 0, len(quality.LossReasons)+1)
		audited = append(audited, quality.LossReasons...)
		audited = append(audited, "CONSTRUCTION_UNPIVOT_SOURCE_ROWS_UNPIVOTED_FROM_UNSELECTED_FIELD")
		quality.LossReasons = audited
		break
	}
	for _, columnID := range []string{unpivot.KeyOutputColumnID, unpivot.ValueOutputColumnID} {
		if err := stage.addOutput(columnID, unpivot.ConstructionID, dependencies, quality); err != nil {
			return err
		}
	}
	for _, projection := range document.Construction.SourceProjections {
		if projection.OwnerStepID != step.ID {
			continue
		}
		consumed := false
		for _, input := range unpivot.Inputs {
			if input.ColumnID == projection.ColumnID {
				consumed = true
			}
		}
		if !consumed {
			return fmt.Errorf("unpivot step %q carries source projection column %q it does not melt as an input", step.ID, projection.ColumnID)
		}
		var declared *authoringv2.StageColumn
		for carrierIndex := 0; carrierIndex < stepIndex; carrierIndex++ {
			for outputIndex := range document.Construction.Steps[carrierIndex].Outputs {
				if document.Construction.Steps[carrierIndex].Outputs[outputIndex].ID == projection.ColumnID {
					declared = &document.Construction.Steps[carrierIndex].Outputs[outputIndex]
					break
				}
			}
			if declared != nil {
				break
			}
		}
		if declared == nil && stepIndex == 0 && compiledUnpivotSourceSlotMatches(compiledOutput, projection) {

			for _, column := range compiledOutput.Stages[0].Columns {
				if column.ID == projection.ColumnID && !column.Internal && !column.Identity {
					declared = &authoringv2.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label}
					break
				}
			}
		}
		if declared == nil || !requiredConstructionID(declared.ID) || strings.TrimSpace(declared.Name) == "" || strings.TrimSpace(declared.Label) == "" {
			return fmt.Errorf("unpivot step %q source projection output %q is missing a declared stable ID, name, or label", step.ID, projection.ColumnID)
		}
		if prior[declared.ID] != declared.Name {
			return fmt.Errorf("unpivot step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if !compiledUnpivotSourceSlotMatches(compiledOutput, projection) {
			return fmt.Errorf("unpivot step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if strings.TrimSpace(rowSourceSnapshot.Token) == "" {
			return fmt.Errorf("unpivot step %q source projection output %q cannot be claimed without an authorized capability snapshot", step.ID, declared.ID)
		}
		derived, ok := lifecycle.ConstructionUnpivotSourceFacts(rowSourceSnapshot, document.RootResourceType, projection.FieldPath)
		expected := authoringv2.ConstructionSourceProjection{
			ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
			OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
			FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
		}
		if !ok || expected != projection {
			return fmt.Errorf("unpivot step %q source projection output %q no longer matches the authorized unpivot source derivation for field %q", step.ID, declared.ID, projection.FieldPath)
		}

	}
	return nil
}

func (stage constructionOutputStep) reconcileCombine() error {
	step := stage.step
	document := stage.document
	compiledOutput := stage.compiledOutput
	resolvedInputs := stage.resolvedInputs
	nextAuthored := stage.nextAuthored
	if step.Operation.Combine == nil {
		return fmt.Errorf("combine step %q has no operation payload", step.ID)
	}
	quality := ConstructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"CONSTRUCTION_COMBINE_CHANGES_ROW_SOURCE"},
	}
	for _, output := range step.Outputs {
		if !requiredConstructionID(output.ID) || strings.TrimSpace(output.Name) == "" || strings.TrimSpace(output.Label) == "" {
			return fmt.Errorf("combine step %q output is missing a stable ID, name, or label", step.ID)
		}
		if _, duplicate := nextAuthored[output.Name]; duplicate {
			return fmt.Errorf("construction output %q is duplicated for document %q", output.Name, document.Output.ID)
		}
		inputNames := make([]string, 0)
		for _, projection := range step.Operation.Combine.Projections {
			if projection.OutputColumnID != output.ID {
				continue
			}
			name, err := combineProjectionInputName(compiledOutput, resolvedInputs, projection.InputIndex, projection.InputColumnID)
			if err != nil {
				return fmt.Errorf("combine step %q output %q: %w", step.ID, output.Name, err)
			}
			inputNames = append(inputNames, name)
		}
		uniqueNames, err := uniqueColumnsInOrder(inputNames)
		if err != nil {
			return fmt.Errorf("combine step %q output %q: %w", step.ID, output.Name, err)
		}
		if len(uniqueNames) == 0 {
			return fmt.Errorf("combine step %q output %q has no compiler-resolved input columns", step.ID, output.Name)
		}
		nextAuthored[output.Name] = AuthoredOutputColumn{
			ConstructionID: step.ID, Label: output.Label, InputColumns: uniqueNames,
			ExternalInputLineage: true, Quality: quality,
		}
	}
	return nil
}
