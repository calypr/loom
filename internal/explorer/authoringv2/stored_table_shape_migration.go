package authoringv2

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func MigrateStoredTableShapeWorkspace(raw []byte, catalog CatalogSnapshot) (StoredWorkspaceMigration, error) {
	result, err := MigrateStoredWorkspace(raw, catalog)
	if err != nil {
		return result, err
	}
	var wire struct {
		Documents []json.RawMessage `json:"documents"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return result, fmt.Errorf("read stored documents: %w", err)
	}
	if len(wire.Documents) != len(result.Workspace.Documents) {
		return result, fmt.Errorf("stored document count changed during decode")
	}
	workspace, err := cloneWorkspace(result.Workspace)
	if err != nil {
		return result, fmt.Errorf("detach stored workspace: %w", err)
	}
	changed := false
	for index, rawDocument := range wire.Documents {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawDocument, &fields); err != nil {
			return result, fmt.Errorf("documents[%d]: %w", index, err)
		}
		shapeRaw, present := fields["tableShape"]
		if !present || bytes.Equal(bytes.TrimSpace(shapeRaw), []byte("null")) {
			continue
		}
		document := workspace.Documents[index]
		if document.Construction != nil {
			return result, fmt.Errorf("documents[%d]: construction and tableShape cannot both define post-source operations", index)
		}
		var shape storedTableShape
		if err := strictDecode(shapeRaw, &shape); err != nil {
			return result, fmt.Errorf("documents[%d].tableShape: %w", index, err)
		}
		if err := shape.Validate(document.Columns); err != nil {
			return result, fmt.Errorf("documents[%d].tableShape: %w", index, err)
		}
		document.ArchiveRetiredShape = nil
		seed, err := StartDocumentConstruction(document)
		if err != nil {
			return result, fmt.Errorf("documents[%d]: prepare Construction source projection: %w", index, err)
		}
		source, err := sourceStageColumns(seed.Columns)
		if err != nil {
			return result, fmt.Errorf("documents[%d]: prepare Construction source stage: %w", index, err)
		}
		steps, err := migrateStoredTableShapeSteps(document.Output.ID, &shape, source)
		if err != nil {
			return result, fmt.Errorf("documents[%d].tableShape: %w", index, err)
		}
		seed.Construction.Steps = steps
		if err := seed.Validate(); err != nil {
			return result, fmt.Errorf("documents[%d]: migrated Construction is invalid: %w", index, err)
		}
		workspace.Documents[index] = seed
		changed = true
	}
	if !changed {
		return result, nil
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return result, fmt.Errorf("canonicalize migrated Construction workspace: %w", err)
	}
	var detached Workspace
	if err := json.Unmarshal(canonical, &detached); err != nil {
		return result, fmt.Errorf("decode migrated Construction workspace: %w", err)
	}
	result.Workspace = detached
	result.CanonicalJSON = append([]byte(nil), canonical...)
	result.MigratedCanonicalDigest = workspaceDigest(canonical)
	result.DigestChanged = false
	for _, oldDigest := range result.AcceptedOriginalDigests {
		if oldDigest != result.MigratedCanonicalDigest {
			result.DigestChanged = true
		}
	}
	return result, nil
}

func migrateStoredTableShapeSteps(outputID string, shape *storedTableShape, source []StageColumn) ([]ConstructionStep, error) {
	steps := make([]ConstructionStep, 0)
	current := append([]StageColumn(nil), source...)
	byName := storedStageColumnsByName(current)
	if shape == nil {
		return steps, nil
	}
	if shape.Reshape != nil {
		switch shape.Reshape.Kind {
		case "PIVOT":
			pivot := shape.Reshape.Pivot

			operation := ConstructionPivot{
				ConstructionID:         string(pivot.ConstructionID),
				GroupKeyIDs:            make([]string, 0, len(pivot.GroupKeys)),
				CategoryColumnID:       byName[pivot.CategoryColumn],
				ValueColumnID:          byName[pivot.ValueColumn],
				Categories:             make([]ConstructionPivotCategory, 0, len(pivot.Categories)),
				DuplicatePolicy:        ConstructionPivotDuplicatePolicy(pivot.DuplicatePolicy),
				MissingCellPolicy:      ConstructionPivotMissingCellPolicy(pivot.MissingCellPolicy),
				UnlistedCategoryPolicy: ConstructionPivotUnlistedCategoryPolicy(pivot.UnlistedCategoryPolicy),
			}
			for _, name := range pivot.GroupKeys {
				operation.GroupKeyIDs = append(operation.GroupKeyIDs, byName[name])
			}
			outputs := make([]StageColumn, 0, len(pivot.GroupKeys)+len(pivot.Categories))
			for _, name := range pivot.GroupKeys {
				outputs = append(outputs, findStoredStageColumn(current, byName[name]))
			}
			valueType := findStoredStageColumn(current, operation.ValueColumnID).Type
			for i, category := range pivot.Categories {
				outputID := stableConstructionID("column", outputID, string(pivot.ConstructionID), category.Output.Column)
				operation.Categories = append(operation.Categories, ConstructionPivotCategory{Key: cloneTableScalar(category.Key), OutputColumnID: outputID})
				outputs = append(outputs, StageColumn{ID: outputID, Name: category.Output.Column, Label: category.Output.Label, Type: valueType})
				if i > 0 && outputID == operation.Categories[i-1].OutputColumnID {
					return nil, fmt.Errorf("legacy pivot generated duplicate output identity for %q", category.Output.Column)
				}
			}
			step := ConstructionStep{ID: operation.ConstructionID, Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}, Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &operation}, Outputs: outputs}
			steps = append(steps, step)
			current = outputs
			byName = storedStageColumnsByName(current)
		case "UNPIVOT":
			unpivot := shape.Reshape.Unpivot
			operation := ConstructionUnpivot{
				ConstructionID:      string(unpivot.ConstructionID),
				Inputs:              make([]ConstructionUnpivotInput, 0, len(unpivot.Inputs)),
				KeyOutputColumnID:   stableConstructionID("column", outputID, string(unpivot.ConstructionID), "key"),
				ValueOutputColumnID: stableConstructionID("column", outputID, string(unpivot.ConstructionID), "value"),
				NullRowPolicy:       ConstructionUnpivotNullPolicy(unpivot.NullRowPolicy),
			}
			removed := make(map[string]bool, len(unpivot.Inputs))
			var valueType string
			for i, item := range unpivot.Inputs {
				columnID := byName[item.Column]
				operation.Inputs = append(operation.Inputs, ConstructionUnpivotInput{ColumnID: columnID, Key: cloneTableScalar(item.Key)})
				removed[columnID] = true
				inputType := findStoredStageColumn(current, columnID).Type
				if i == 0 {
					valueType = inputType
				} else if valueType != inputType {
					valueType = ""
				}
			}
			keyType := storedUnpivotKeyType(operation.Inputs)
			outputs := make([]StageColumn, 0, len(current)-len(removed)+2)
			for _, column := range current {
				if !removed[column.ID] {
					outputs = append(outputs, column)
				}
			}
			outputs = append(outputs,
				StageColumn{ID: operation.KeyOutputColumnID, Name: unpivot.KeyOutput.Column, Label: unpivot.KeyOutput.Label, Type: keyType},
				StageColumn{ID: operation.ValueOutputColumnID, Name: unpivot.ValueOutput.Column, Label: unpivot.ValueOutput.Label, Type: valueType},
			)
			step := ConstructionStep{ID: operation.ConstructionID, Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}, Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot, Unpivot: &operation}, Outputs: outputs}
			steps = append(steps, step)
			current = outputs
			byName = storedStageColumnsByName(current)
		default:
			return nil, fmt.Errorf("cannot migrate unsupported table reshape kind %q", shape.Reshape.Kind)
		}
	}
	ordered, err := topologicallyOrderStoredDerived(shape.Derived, current)
	if err != nil {
		return nil, fmt.Errorf("cannot migrate legacy derived operations: %w", err)
	}
	for _, derived := range ordered {
		operation := ConstructionDerive{
			ConstructionID:       string(derived.ConstructionID),
			OutputColumnID:       stableConstructionID("column", outputID, string(derived.ConstructionID), derived.Output.Column),
			Operation:            ConstructionDerivedOperation(derived.Operation),
			MissingInputPolicy:   ConstructionMissingInputPolicy(derived.MissingInputPolicy),
			DivisionByZeroPolicy: ConstructionDivisionByZeroPolicy(derived.DivisionByZeroPolicy),
		}
		operation.Left, err = migrateStoredArithmeticOperand(derived.Left, byName)
		if err != nil {
			return nil, fmt.Errorf("cannot migrate derived %q left operand: %w", derived.Output.Column, err)
		}
		operation.Right, err = migrateStoredArithmeticOperand(derived.Right, byName)
		if err != nil {
			return nil, fmt.Errorf("cannot migrate derived %q right operand: %w", derived.Output.Column, err)
		}
		outputs := append([]StageColumn(nil), current...)
		outputs = append(outputs, StageColumn{ID: operation.OutputColumnID, Name: derived.Output.Column, Label: derived.Output.Label})
		input := ConstructionInputRef{Kind: ConstructionInputSourceProjection}
		if len(steps) > 0 {
			input = ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: steps[len(steps)-1].ID}
		}
		step := ConstructionStep{ID: operation.ConstructionID, Inputs: []ConstructionInputRef{input}, Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &operation}, Outputs: outputs}
		steps = append(steps, step)
		current = outputs
		byName = storedStageColumnsByName(current)
	}
	if len(steps) > 0 {
		for i := 1; i < len(steps); i++ {
			steps[i].Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: steps[i-1].ID}}
		}
	}
	return steps, nil
}

func migrateStoredArithmeticOperand(operand storedArithmeticOperand, columns map[string]string) (ConstructionOperand, error) {
	switch operand.Kind {
	case "COLUMN":
		id := columns[operand.Column]
		if id == "" {
			return ConstructionOperand{}, fmt.Errorf("unknown column %q", operand.Column)
		}
		return ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: id}, nil
	case "LITERAL":
		if operand.Literal == nil {
			return ConstructionOperand{}, fmt.Errorf("literal payload is missing")
		}
		literal := ConstructionLiteral{}
		switch operand.Literal.Kind {
		case TableScalarInteger:
			literal.Kind, literal.Integer = ConstructionNumericInteger, cloneInt64(operand.Literal.Integer)
		case TableScalarDecimal:
			literal.Kind, literal.Decimal = ConstructionNumericDecimal, cloneFloat64(operand.Literal.Decimal)
		default:
			return ConstructionOperand{}, fmt.Errorf("legacy arithmetic literal kind %q is not numeric", operand.Literal.Kind)
		}
		return ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &literal}, nil
	default:
		return ConstructionOperand{}, fmt.Errorf("unsupported legacy operand kind %q", operand.Kind)
	}
}

func topologicallyOrderStoredDerived(derived []storedDerivedConstruction, input []StageColumn) ([]storedDerivedConstruction, error) {
	if len(derived) == 0 {
		return nil, nil
	}
	byName := make(map[string]storedDerivedConstruction, len(derived))
	for _, item := range derived {
		if _, duplicate := byName[item.Output.Column]; duplicate {
			return nil, fmt.Errorf("duplicate derived output %q", item.Output.Column)
		}
		byName[item.Output.Column] = item
	}
	known := make(map[string]bool, len(input)+len(derived))
	for _, column := range input {
		known[column.Name] = true
	}
	remaining := make(map[string]bool, len(derived))
	for _, item := range derived {
		remaining[item.Output.Column] = true
	}
	ordered := make([]storedDerivedConstruction, 0, len(derived))
	for len(remaining) > 0 {
		progress := false
		for _, item := range derived {
			name := item.Output.Column
			if !remaining[name] {
				continue
			}
			if !storedOperandNameKnown(item.Left, known) || !storedOperandNameKnown(item.Right, known) {
				continue
			}
			ordered = append(ordered, item)
			known[name] = true
			delete(remaining, name)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("derived column dependencies cannot be ordered")
		}
	}
	return ordered, nil
}

func storedOperandNameKnown(operand storedArithmeticOperand, known map[string]bool) bool {
	if operand.Kind != "COLUMN" {
		return operand.Kind == "LITERAL" && operand.Literal != nil
	}
	return known[operand.Column]
}

func storedStageColumnsByName(columns []StageColumn) map[string]string {
	byName := make(map[string]string, len(columns))
	for _, column := range columns {
		byName[column.Name] = column.ID
	}
	return byName
}

func findStoredStageColumn(columns []StageColumn, id string) StageColumn {
	for _, column := range columns {
		if column.ID == id {
			return column
		}
	}
	return StageColumn{}
}

func storedUnpivotKeyType(inputs []ConstructionUnpivotInput) string {
	if len(inputs) == 0 {
		return ""
	}
	kind := inputs[0].Key.Kind
	for _, input := range inputs[1:] {
		if input.Key.Kind != kind {
			return ""
		}
	}
	switch kind {
	case TableScalarString:
		return "string"
	case TableScalarInteger:
		return "integer"
	case TableScalarDecimal:
		return "decimal"
	case TableScalarBoolean:
		return "boolean"
	default:
		return ""
	}
}
