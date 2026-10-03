package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func constructionShapesRows(kind ConstructionOperationKind) bool {
	switch kind {
	case ConstructionOperationGroup, ConstructionOperationCodedGroup, ConstructionOperationPivot, ConstructionOperationCodedPivot:
		return true
	default:
		return false
	}
}

// CanPopulateConstructionRows proves that a source column can reach every
// shaping boundary and continue through the remaining construction.
func CanPopulateConstructionRows(construction *Construction) bool {
	if construction == nil {
		return false
	}
	shaped := false
	for _, step := range construction.Steps {
		if constructionShapesRows(step.Operation.Kind) {
			shaped = true
			continue
		}
		if !constructionCanCarrySourceProjection(step.Operation.Kind) {
			return false
		}
	}
	return shaped
}

func (s ConstructionStep) inputColumnIDs() []string {
	ids := s.Operation.inputColumnIDs()
	for _, value := range s.RowValues {
		if !containsConstructionColumnID(ids, value.InputColumnID) {
			ids = append(ids, value.InputColumnID)
		}
	}
	return ids
}

func constructionStepWithoutRowValueOutputs(step ConstructionStep) ConstructionStep {
	if len(step.RowValues) == 0 {
		return step
	}
	attached := make(map[string]bool, len(step.RowValues))
	for _, value := range step.RowValues {
		attached[value.OutputColumnID] = true
	}
	outputs := make([]StageColumn, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		if !attached[output.ID] {
			outputs = append(outputs, output)
		}
	}
	step.Outputs = outputs
	return step
}

func validateConstructionRowValues(step ConstructionStep, input map[string]StageColumn) error {
	if len(step.RowValues) == 0 {
		return nil
	}
	if !constructionShapesRows(step.Operation.Kind) {
		return fmt.Errorf("rowValues require a grouping or pivot step")
	}
	outputs := make(map[string]StageColumn, len(step.Outputs))
	for _, column := range step.Outputs {
		outputs[column.ID] = column
	}
	seen := make(map[string]bool, len(step.RowValues))
	for _, value := range step.RowValues {
		if !requiredID(value.InputColumnID) || !requiredID(value.OutputColumnID) || seen[value.OutputColumnID] {
			return fmt.Errorf("rowValues require distinct outputColumnId and exact inputColumnId")
		}
		seen[value.OutputColumnID] = true
		if _, found := input[value.InputColumnID]; !found {
			return missingConstructionColumn("rowValues.inputColumnId", value.InputColumnID)
		}
		if _, collision := input[value.OutputColumnID]; collision {
			return fmt.Errorf("rowValues output collides with an input column")
		}
		output, found := outputs[value.OutputColumnID]
		if !found {
			return fmt.Errorf("rowValues output %q is not declared", value.OutputColumnID)
		}
		switch value.Policy {
		case ConstructionRowValueAll:
			if output.Type != "array" {
				return fmt.Errorf("ALL row values require an array output")
			}
		case ConstructionRowValueOne:
			if output.Type == "array" {
				return fmt.Errorf("ONE row values require a scalar output")
			}
		default:
			return fmt.Errorf("rowValues policy must be ALL or ONE")
		}
	}
	return nil
}

func populateConstructionColumn(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	if document.Construction == nil || len(document.Construction.Steps) == 0 {
		return nil
	}
	if policy == "" {
		policy = ConstructionRowValueAll
	}
	if policy != ConstructionRowValueAll && policy != ConstructionRowValueOne {
		return fmt.Errorf("row value policy must be ALL or ONE")
	}
	current := StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Nullable: true}
	for index := range document.Construction.Steps {
		step := &document.Construction.Steps[index]
		if constructionShapesRows(step.Operation.Kind) {
			digest := sha256.Sum256([]byte(step.ID + "\x00" + column.ColumnID))
			output := current
			output.ID = "row_value_" + hex.EncodeToString(digest[:12])
			output.Type = column.LogicalType
			if policy == ConstructionRowValueAll {
				output.Type = "array"
				output.Nullable = false
			}
			step.RowValues = append(step.RowValues, ConstructionRowValue{InputColumnID: current.ID, OutputColumnID: output.ID, Policy: policy})
			step.Outputs = append(step.Outputs, output)
			current = output
		} else if constructionCanCarrySourceProjection(step.Operation.Kind) {
			step.Outputs = append(step.Outputs, current)
		} else {
			return fmt.Errorf("adding source values across %s is not supported", step.Operation.Kind)
		}
	}
	return nil
}

func populateExplicitGroupRowValue(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	if document == nil || document.Rows.Kind != RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
		return fmt.Errorf("row values require an explicit group source")
	}
	if column.ColumnID == "" || column.OccurrenceID != RootOccurrenceID || column.Source.Kind != SourceField || column.Source.Field == nil || column.ValueTransformation != nil {
		return fmt.Errorf("explicit group row values support only an untransformed root FHIR field column with a stable columnId")
	}
	if policy == "" {
		policy = ConstructionRowValueAll
	}
	if policy != ConstructionRowValueAll && policy != ConstructionRowValueOne {
		return fmt.Errorf("row value policy must be ALL or ONE")
	}
	values := document.Rows.Groups.RowValues
	for index := range values {
		if values[index].ColumnID == column.ColumnID {
			values[index].Policy = policy
			document.Rows.Groups.RowValues = values
			return nil
		}
	}
	document.Rows.Groups.RowValues = append(values, ExplicitGroupRowValue{ColumnID: column.ColumnID, Policy: policy})
	return nil
}

func removeExplicitGroupRowValue(document *Document, columnID string) {
	if document == nil || document.Rows.Groups == nil || len(document.Rows.Groups.RowValues) == 0 {
		return
	}
	values := document.Rows.Groups.RowValues[:0]
	for _, value := range document.Rows.Groups.RowValues {
		if value.ColumnID != columnID {
			values = append(values, value)
		}
	}
	document.Rows.Groups.RowValues = values
}

func removeConstructionColumnValues(document *Document, columnID string) {
	if document.Construction == nil {
		return
	}
	removed := map[string]bool{columnID: true}
	for index := range document.Construction.Steps {
		step := &document.Construction.Steps[index]
		values := make([]ConstructionRowValue, 0, len(step.RowValues))
		for _, value := range step.RowValues {
			if removed[value.InputColumnID] {
				removed[value.OutputColumnID] = true
			} else {
				values = append(values, value)
			}
		}
		step.RowValues = values
		outputs := make([]StageColumn, 0, len(step.Outputs))
		for _, output := range step.Outputs {
			if !removed[output.ID] {
				outputs = append(outputs, output)
			}
		}
		step.Outputs = outputs
	}
}
