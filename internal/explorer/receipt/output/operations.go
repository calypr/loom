package output

import (
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (stage constructionOutputStep) reconcileDerive() error {
	step := stage.step
	document := stage.document
	compiledOutput := stage.compiledOutput
	rowSourceSnapshot := stage.rowSourceSnapshot
	nextAuthored := stage.nextAuthored
	prior := stage.prior
	if step.Operation.Derive == nil {
		return fmt.Errorf("derive step %q has no operation payload", step.ID)
	}
	derive := step.Operation.Derive
	inputs := make([]string, 0, 2)
	for _, operand := range []ws.ConstructionOperand{derive.Left, derive.Right} {
		switch operand.Kind {
		case ws.ConstructionColumnOperand:
			inputs = append(inputs, operand.ColumnID)
		case ws.ConstructionLiteralOperand:
		default:
			return fmt.Errorf("derive step %q has unsupported operand kind %q", step.ID, operand.Kind)
		}
	}
	quality, err := derivedOutputQuality(string(derive.Operation))
	if err != nil {
		return fmt.Errorf("derive step %q: %w", step.ID, err)
	}
	if err := stage.addOutput(derive.OutputColumnID, derive.ConstructionID, inputs, quality); err != nil {
		return err
	}
	for _, projection := range document.Construction.SourceProjections {
		if projection.OwnerStepID != step.ID {
			continue
		}
		consumed := false
		for _, operand := range []ws.ConstructionOperand{derive.Left, derive.Right} {
			if operand.Kind == ws.ConstructionColumnOperand && operand.ColumnID == projection.ColumnID {
				consumed = true
			}
		}
		if !consumed {
			return fmt.Errorf("derive step %q carries source projection column %q it does not consume", step.ID, projection.ColumnID)
		}
		var declared *ws.StageColumn
		for outputIndex := range step.Outputs {
			if step.Outputs[outputIndex].ID == projection.ColumnID {
				declared = &step.Outputs[outputIndex]
				break
			}
		}
		if declared == nil || !requiredConstructionID(declared.ID) || strings.TrimSpace(declared.Name) == "" || strings.TrimSpace(declared.Label) == "" {
			return fmt.Errorf("derive step %q source projection output %q is missing a declared stable ID, name, or label", step.ID, projection.ColumnID)
		}
		if prior[declared.ID] != declared.Name {
			return fmt.Errorf("derive step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if !compiledDeriveSourceSlotMatches(compiledOutput, projection) {
			return fmt.Errorf("derive step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if strings.TrimSpace(rowSourceSnapshot.Token) == "" {
			return fmt.Errorf("derive step %q source projection output %q cannot be claimed without an authorized capability snapshot", step.ID, declared.ID)
		}
		derived, ok := lifecycle.ConstructionDeriveSourceFacts(rowSourceSnapshot, document.RootResourceType, projection.FieldPath)
		expected := ws.ConstructionSourceProjection{
			ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
			OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
			FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
		}
		if !ok || expected != projection {
			return fmt.Errorf("derive step %q source projection output %q no longer matches the authorized derive source derivation for field %q", step.ID, declared.ID, projection.FieldPath)
		}
		if _, duplicate := nextAuthored[declared.Name]; duplicate {
			return fmt.Errorf("construction output %q is duplicated for document %q", declared.Name, document.Output.ID)
		}
		nextAuthored[declared.Name] = AuthoredOutputColumn{
			ConstructionID: declared.ID, Label: declared.Label, TypedStageOutput: true,
			Quality: ConstructedOutputQuality{
				Lossless: false, StructuralSuitability: "requires-review",
				LossReasons: []string{"CONSTRUCTION_DERIVE_SOURCE_ARITHMETIC_FROM_UNSELECTED_FIELD"},
			},
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileFilter() error {
	step := stage.step
	document := stage.document
	compiledOutput := stage.compiledOutput
	rowSourceSnapshot := stage.rowSourceSnapshot
	nextAuthored := stage.nextAuthored
	prior := stage.prior
	if step.Operation.Filter == nil {
		return fmt.Errorf("filter step %q has no operation payload", step.ID)
	}
	for _, projection := range document.Construction.SourceProjections {
		if projection.OwnerStepID != step.ID {
			continue
		}
		if projection.ColumnID != step.Operation.Filter.ColumnID {
			return fmt.Errorf("filter step %q carries source projection column %q it does not consume", step.ID, projection.ColumnID)
		}
		var declared *ws.StageColumn
		for outputIndex := range step.Outputs {
			if step.Outputs[outputIndex].ID == projection.ColumnID {
				declared = &step.Outputs[outputIndex]
				break
			}
		}
		if declared == nil || !requiredConstructionID(declared.ID) || strings.TrimSpace(declared.Name) == "" || strings.TrimSpace(declared.Label) == "" {
			return fmt.Errorf("filter step %q source projection output %q is missing a declared stable ID, name, or label", step.ID, projection.ColumnID)
		}
		if prior[declared.ID] != declared.Name {
			return fmt.Errorf("filter step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if !compiledFilterSourceSlotMatches(compiledOutput, projection) {
			return fmt.Errorf("filter step %q source projection output %q does not carry the compiled source column identity", step.ID, declared.ID)
		}
		if strings.TrimSpace(rowSourceSnapshot.Token) == "" {
			return fmt.Errorf("filter step %q source projection output %q cannot be claimed without an authorized capability snapshot", step.ID, declared.ID)
		}
		derived, ok := lifecycle.ConstructionFilterSourceFacts(rowSourceSnapshot, document.RootResourceType, projection.FieldPath)
		expected := ws.ConstructionSourceProjection{
			ColumnID: projection.ColumnID, OwnerStepID: projection.OwnerStepID,
			OccurrenceID: derived.OccurrenceID, FieldPath: derived.FieldPath,
			FHIRType: derived.FHIRType, LogicalType: derived.LogicalType, Label: derived.Label,
		}
		if !ok || expected != projection {
			return fmt.Errorf("filter step %q source projection output %q no longer matches the authorized filter source derivation for field %q", step.ID, declared.ID, projection.FieldPath)
		}
		if _, duplicate := nextAuthored[declared.Name]; duplicate {
			return fmt.Errorf("construction output %q is duplicated for document %q", declared.Name, document.Output.ID)
		}
		nextAuthored[declared.Name] = AuthoredOutputColumn{
			ConstructionID: declared.ID, Label: declared.Label, TypedStageOutput: true,
			Quality: ConstructedOutputQuality{
				Lossless: false, StructuralSuitability: "requires-review",
				LossReasons: []string{"CONSTRUCTION_FILTER_SOURCE_ROWS_FILTERED"},
			},
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileGroup() error {
	step := stage.step
	compiledOutput := stage.compiledOutput
	nextAuthored := stage.nextAuthored
	prior := stage.prior
	if step.Operation.Group == nil {
		return fmt.Errorf("group step %q has no operation payload", step.ID)
	}
	group := step.Operation.Group
	quality := ConstructedOutputQuality{
		StructuralSuitability: "requires-review",
		LossReasons:           []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN"},
	}
	for _, key := range group.Keys {
		if key.SemanticCategory != nil {
			category := key.SemanticCategory
			if category.Kind != "BINDING" || strings.TrimSpace(category.BindingID) == "" ||
				strings.TrimSpace(category.FamilyID) == "" || strings.TrimSpace(category.ResourceType) == "" ||
				strings.TrimSpace(category.CodingPath) == "" {
				return fmt.Errorf("step %q semantic Group output %q has no resolved binding identity", step.ID, key.OutputColumnID)
			}
			var compiledKey *facade.CompiledOutputColumn
			for stageIndex := range compiledOutput.Stages {
				stage := &compiledOutput.Stages[stageIndex]
				if stage.ID != step.ID || stage.Operation != string(step.Operation.Kind) {
					continue
				}
				for columnIndex := range stage.Columns {
					column := &stage.Columns[columnIndex]
					if column.ID == key.OutputColumnID {
						if compiledKey != nil {
							return fmt.Errorf("step %q semantic Group output %q has duplicate compiled schema identities", step.ID, key.OutputColumnID)
						}
						compiledKey = column
					}
				}
			}
			wantSemanticPath := "construction_semantic_group:" + category.BindingID + ":" + category.FamilyID
			if compiledKey == nil || compiledKey.SemanticPath != wantSemanticPath {
				return fmt.Errorf("step %q semantic Group output %q does not match its compiled binding metadata", step.ID, key.OutputColumnID)
			}
			if err := stage.addOutput(key.OutputColumnID, group.ConstructionID, nil, quality); err != nil {
				return err
			}
			metadata := nextAuthored[compiledKey.Name]
			metadata.SourceResourceType = category.ResourceType
			metadata.SourcePath = category.CodingPath
			nextAuthored[compiledKey.Name] = metadata
			continue
		}
		if key.FieldSource != nil {
			binding := key.FieldSource
			identity, ok := groupFieldSourceOutputIdentityForReconcile(binding)
			if !ok {
				return fmt.Errorf("step %q Group output %q has no resolved field-source binding identity", step.ID, key.OutputColumnID)
			}
			if err := stage.addOutput(key.OutputColumnID, group.ConstructionID, nil, quality); err != nil {
				return err
			}
			for _, output := range step.Outputs {
				if output.ID != key.OutputColumnID {
					continue
				}
				metadata := nextAuthored[output.Name]
				metadata.CandidateID = identity.CandidateID
				metadata.OccurrenceID = identity.OccurrenceID
				metadata.SourceResourceType = identity.SourceResourceType
				metadata.SourcePath = identity.SourcePath
				nextAuthored[output.Name] = metadata
				break
			}
			continue
		}
		if err := stage.addOutput(key.OutputColumnID, group.ConstructionID, []string{key.InputColumnID}, quality); err != nil {
			return err
		}
		inputName := prior[key.InputColumnID]
		for _, output := range step.Outputs {
			if output.ID == key.OutputColumnID && output.Name == inputName {
				metadata := nextAuthored[output.Name]
				metadata.PreservesSourceIdentity = true
				nextAuthored[output.Name] = metadata
				break
			}
		}
	}
	for _, aggregate := range group.Aggregates {
		inputs := []string(nil)
		if aggregate.Operation != ws.ConstructionGroupCountRows {
			inputs = []string{aggregate.InputColumnID}
		}
		if err := stage.addOutput(aggregate.OutputColumnID, group.ConstructionID, inputs, quality); err != nil {
			return err
		}
	}
	for _, rowValue := range step.RowValues {
		rowQuality := quality
		if rowValue.Policy == ws.ConstructionRowValueAll {
			rowQuality.Shape = "array"
		}
		inputs := []string(nil)
		var fieldSourceIdentity groupFieldSourceOutputIdentity
		if rowValue.FieldSource != nil {
			binding := rowValue.FieldSource
			var ok bool
			fieldSourceIdentity, ok = groupFieldSourceOutputIdentityForReconcile(binding)
			if !ok || binding.ValueKind != "SCALAR" {
				return fmt.Errorf("step %q Group row-value output %q has no resolved scalar field-source binding identity", step.ID, rowValue.OutputColumnID)
			}
		} else {
			inputs = []string{rowValue.InputColumnID}
		}
		if err := stage.addOutput(rowValue.OutputColumnID, group.ConstructionID, inputs, rowQuality); err != nil {
			return err
		}
		if rowValue.FieldSource == nil {
			continue
		}
		for _, output := range step.Outputs {
			if output.ID != rowValue.OutputColumnID {
				continue
			}
			metadata := nextAuthored[output.Name]
			metadata.CandidateID = fieldSourceIdentity.CandidateID
			metadata.OccurrenceID = fieldSourceIdentity.OccurrenceID
			metadata.SourceResourceType = fieldSourceIdentity.SourceResourceType
			metadata.SourcePath = fieldSourceIdentity.SourcePath
			nextAuthored[output.Name] = metadata
			break
		}
	}
	return nil
}

func (stage constructionOutputStep) reconcileCodedGroup() error {
	step := stage.step
	document := stage.document
	nextAuthored := stage.nextAuthored
	if step.Operation.CodedGroup == nil {
		return fmt.Errorf("coded group step %q has no operation payload", step.ID)
	}
	group := step.Operation.CodedGroup
	quality := ConstructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"CODED_GROUP_REDUCES_TO_DISTINCT_SOURCE_RECORDS"},
	}
	inputIDs := make([]string, 0, len(document.Columns))
	for _, column := range document.Columns {
		if column.OccurrenceID == group.Source.OccurrenceID {
			inputIDs = append(inputIDs, column.ColumnID)
		}
	}
	if len(inputIDs) == 0 {
		return fmt.Errorf("coded group step %q has no source columns on occurrence %q for lineage", step.ID, group.Source.OccurrenceID)
	}
	for _, output := range []struct {
		id   string
		path string
	}{
		{id: group.SystemOutputColumnID, path: group.Source.CodingPath + ".system"},
		{id: group.VersionOutputColumnID, path: group.Source.CodingPath + ".version"},
		{id: group.CodeOutputColumnID, path: group.Source.CodingPath + ".code"},
		{id: group.DistinctSourceCountOutputColumnID, path: group.Source.CodingPath},
	} {
		if err := stage.addOutput(output.id, group.ConstructionID, inputIDs, quality); err != nil {
			return err
		}
		for _, declared := range step.Outputs {
			if declared.ID == output.id {
				metadata := nextAuthored[declared.Name]
				metadata.OccurrenceID = group.Source.OccurrenceID
				metadata.SourceResourceType = group.Source.ResourceType
				metadata.SourcePath = output.path
				nextAuthored[declared.Name] = metadata
				break
			}
		}
	}
	return nil
}

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
	if pivot.DuplicatePolicy != ws.ConstructionPivotDuplicateError {
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
		var declared *ws.StageColumn
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
					declared = &ws.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label}
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
		expected := ws.ConstructionSourceProjection{
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

func (stage constructionOutputStep) reconcileRelatedSource() error {
	step := stage.step
	nextAuthored := stage.nextAuthored
	if step.Operation.RelatedSource == nil {
		return fmt.Errorf("related-source step %q has no operation payload", step.ID)
	}
	related := step.Operation.RelatedSource
	if (related.Form != capability.ConstructionChoiceAll && related.Form != capability.ConstructionChoiceCount && related.Form != capability.ConstructionChoicePresence) || related.ContributorRule.Policy != ws.ConstructionRelatedAllMatches {
		return fmt.Errorf("related-source step %q has unsupported output form or contributor policy", step.ID)
	}
	quality := ConstructedOutputQuality{
		Lossless: false, Shape: "array", StructuralSuitability: "requires-review",
		LossReasons: []string{"RELATED_SOURCE_AUTHORIZED_MATCHES_ONLY"},
	}
	if related.Form != capability.ConstructionChoiceAll || related.RowValuePolicy == ws.ConstructionRowValueOne {
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
