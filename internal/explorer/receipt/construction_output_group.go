package receipt

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"strings"
)

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
			var compiledKey *lower.CompiledOutputColumn
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
		if aggregate.Operation != authoringv2.ConstructionGroupCountRows {
			inputs = []string{aggregate.InputColumnID}
		}
		if err := stage.addOutput(aggregate.OutputColumnID, group.ConstructionID, inputs, quality); err != nil {
			return err
		}
	}
	for _, rowValue := range step.RowValues {
		rowQuality := quality
		if rowValue.Policy == authoringv2.ConstructionRowValueAll {
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
