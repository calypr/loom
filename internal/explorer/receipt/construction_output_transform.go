package receipt

import (
	"fmt"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"strings"
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
	for _, operand := range []authoringv2.ConstructionOperand{derive.Left, derive.Right} {
		switch operand.Kind {
		case authoringv2.ConstructionColumnOperand:
			inputs = append(inputs, operand.ColumnID)
		case authoringv2.ConstructionLiteralOperand:
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
		for _, operand := range []authoringv2.ConstructionOperand{derive.Left, derive.Right} {
			if operand.Kind == authoringv2.ConstructionColumnOperand && operand.ColumnID == projection.ColumnID {
				consumed = true
			}
		}
		if !consumed {
			return fmt.Errorf("derive step %q carries source projection column %q it does not consume", step.ID, projection.ColumnID)
		}
		var declared *authoringv2.StageColumn
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
		expected := authoringv2.ConstructionSourceProjection{
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
		var declared *authoringv2.StageColumn
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
		expected := authoringv2.ConstructionSourceProjection{
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
