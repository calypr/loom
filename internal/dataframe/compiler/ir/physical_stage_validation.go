package ir

import (
	"fmt"
	"strings"
)

func validatePhysicalStageSequence(sequence PhysicalStageSequence, sourceOperations []PhysicalOperation, bindVars map[string]any) error {
	if sequence.SourceStageID == "" || sequence.SourceRowIdentity == "" || sequence.FinalStageID == "" || sequence.FinalRowIdentity == "" {
		return fmt.Errorf("source/final stage IDs and row identity columns are required")
	}
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("at least one construction stage is required")
	}
	if _, ok := bindVars[sequence.PreviewLimitBindKey]; sequence.PreviewLimitBindKey != "" && !ok {
		return fmt.Errorf("preview limit bind %q is missing", sequence.PreviewLimitBindKey)
	}
	projectionNames := map[string]bool{}
	for _, operation := range sourceOperations {
		if operation.Kind == PhysicalGroupedPivotOp || operation.Kind == PhysicalUnpivotOp {
			return fmt.Errorf("source projection cannot contain a terminal table reshape")
		}
		if operation.Kind == PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				projectionNames[projection.Name] = true
			}
		}
	}
	if err := validatePhysicalStageColumns(sequence.SourceColumns, sequence.SourceRowIdentity); err != nil {
		return fmt.Errorf("source projection schema: %w", err)
	}
	for _, column := range sequence.SourceColumns {
		if !projectionNames[column.Name] {
			return fmt.Errorf("source projection column %q is missing from the typed source RETURN", column.Name)
		}
	}
	priorStageID := sequence.SourceStageID
	priorColumns := sequence.SourceColumns
	priorRowIdentity := sequence.SourceRowIdentity
	seenIDs := map[string]bool{sequence.SourceStageID: true}
	for index, stage := range sequence.Stages {
		path := fmt.Sprintf("stage[%d]", index)
		if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) || seenIDs[stage.ID] {
			return fmt.Errorf("%s ID is empty, untrimmed, or duplicated", path)
		}
		seenIDs[stage.ID] = true
		if stage.InputStageID != priorStageID {
			return fmt.Errorf("%s input stage %q does not reference prior stage %q", path, stage.InputStageID, priorStageID)
		}
		if err := validatePhysicalStageColumns(stage.InputColumns, priorRowIdentity); err != nil {
			return fmt.Errorf("%s input schema: %w", path, err)
		}
		if !samePhysicalStageColumns(stage.InputColumns, priorColumns) {
			return fmt.Errorf("%s input schema differs from preceding stage output", path)
		}
		if err := validatePhysicalStageColumns(stage.OutputColumns, stage.RowIdentityColumn); err != nil {
			return fmt.Errorf("%s output schema: %w", path, err)
		}
		if err := validateStageVariables(stage); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		switch stage.Kind {
		case PhysicalStageDeriveOp:
			if stage.Filter != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
				return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
			}
			if len(stage.DerivedLets) == 0 {
				return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
			}
			if err := validateStageRowOperations(stage, bindVars, false); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageFilterOp:
			if stage.Filter == nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s FILTER requires only a filter payload", path)
			}
			if err := validateStageRowOperations(stage, bindVars, true); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStagePivotOp:
			if stage.GroupedPivot == nil || stage.Filter != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s PIVOT requires only a grouped-pivot payload", path)
			}
			if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match grouped pivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if err := validatePhysicalGroupedPivot(*stage.GroupedPivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s grouped pivot: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, groupedPivotOutputNames(*stage.GroupedPivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageUnpivotOp:
			if stage.Unpivot == nil || stage.Filter != nil || stage.GroupedPivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s UNPIVOT requires only an unpivot payload", path)
			}
			if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match unpivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if err := validatePhysicalUnpivot(*stage.Unpivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s unpivot: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, unpivotOutputNames(*stage.Unpivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		default:
			return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
		}
		priorStageID, priorColumns, priorRowIdentity = stage.ID, stage.OutputColumns, stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID || sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("final stage or row identity does not match the final operation")
	}
	if !samePhysicalStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("final output schema differs from the final stage")
	}
	return nil
}

func validatePhysicalStageColumns(columns []PhysicalStageColumn, identityName string) error {
	if len(columns) == 0 || strings.TrimSpace(identityName) == "" {
		return fmt.Errorf("columns and row identity are required")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	identityFound := false
	for _, column := range columns {
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) || ids[column.ID] {
			return fmt.Errorf("column ID %q is empty, untrimmed, or duplicated", column.ID)
		}
		if !physicalPathPartPattern.MatchString(column.Name) || names[column.Name] {
			return fmt.Errorf("column name %q is unsafe or duplicated", column.Name)
		}
		if strings.TrimSpace(column.Kind) == "" || strings.TrimSpace(column.Cardinality) == "" {
			return fmt.Errorf("column %q requires logical kind and cardinality", column.Name)
		}
		ids[column.ID], names[column.Name] = true, true
		if column.Name == identityName {
			if !column.Internal || !column.Identity {
				return fmt.Errorf("row identity column %q must be internal and identity-marked", identityName)
			}
			identityFound = true
		}
	}
	if !identityFound {
		return fmt.Errorf("row identity column %q is absent from schema", identityName)
	}
	return nil
}

func samePhysicalStageColumns(left, right []PhysicalStageColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind || a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal || a.Identity != b.Identity {
			return false
		}
	}
	return true
}

func validateStageVariables(stage PhysicalConstructionStage) error {
	if !physicalVariablePattern.MatchString(stage.InputRowVariable) || !physicalVariablePattern.MatchString(stage.OutputRowVariable) || stage.InputRowVariable == stage.OutputRowVariable {
		return fmt.Errorf("input and output row variables must be safe and distinct")
	}
	return nil
}

func validateStageRowOperations(stage PhysicalConstructionStage, bindVars map[string]any, expectFilter bool) error {
	defined := map[string]bool{stage.InputRowVariable: true}
	for index, operation := range stage.DerivedLets {
		if operation.Kind != PhysicalExpressionLetOp || operation.ExpressionLet == nil {
			return fmt.Errorf("derived operation %d must be a typed expression LET", index)
		}
		if err := validatePhysicalExpression(operation.ExpressionLet.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("derived operation %d: %w", index, err)
		}
		if err := definePhysicalVariable(defined, operation.ExpressionLet.Variable); err != nil {
			return err
		}
	}
	if expectFilter {
		if err := validatePhysicalFilter(*stage.Filter, defined, bindVars); err != nil {
			return err
		}
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	return nil
}

func validateStageProjectionNames(projections []PhysicalProjection, columns []PhysicalStageColumn) error {
	if len(projections) != len(columns) {
		return fmt.Errorf("output projection count %d does not match schema count %d", len(projections), len(columns))
	}
	for index := range projections {
		if projections[index].Name != columns[index].Name || projections[index].Hidden != columns[index].Internal {
			return fmt.Errorf("output projection %d does not match schema column %q", index, columns[index].Name)
		}
	}
	return nil
}

func groupedPivotOutputNames(pivot PhysicalGroupedPivot) []string {
	columns := make([]string, 0, len(pivot.GroupKeys)+len(pivot.Categories)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		columns = append(columns, name)
	}
	for _, category := range pivot.Categories {
		columns = append(columns, category.Output)
	}
	if pivot.UnlistedEvidenceColumn != "" {
		columns = append(columns, pivot.UnlistedEvidenceColumn)
	}
	columns = append(columns, "__loom_row_id")
	return columns
}

func unpivotOutputNames(unpivot PhysicalUnpivot) []string {
	selected := map[string]bool{}
	for _, input := range unpivot.Inputs {
		selected[input.Column] = true
	}
	preservedNames := map[string]string{}
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	columns := make([]string, 0, len(unpivot.InputProjections)+2)
	for _, projection := range unpivot.InputProjections {
		if !selected[projection.Name] && projection.Name != "__loom_row_id" {
			name := projection.Name
			if renamed, ok := preservedNames[projection.Name]; ok {
				name = renamed
			}
			columns = append(columns, name)
		}
	}
	columns = append(columns, unpivot.KeyOutput, unpivot.ValueOutput, "__loom_row_id")
	return columns
}

func validateShapeStageOutput(stage PhysicalConstructionStage, expected []string) error {
	actual := make(map[string]bool, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		actual[column.Name] = true
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("shape output schema has %d columns, operation emits %d", len(actual), len(expected))
	}
	for _, name := range expected {
		if !actual[name] {
			return fmt.Errorf("shape output schema is missing emitted column %q", name)
		}
	}
	return nil
}
