package authoringv2

import (
	"fmt"
	"math"
	"strings"
)

func (c *Construction) Validate(sourceColumns []Column) error {
	if c == nil {
		return nil
	}
	if c.Version != ConstructionVersion {
		return fmt.Errorf("unsupported construction version %d", c.Version)
	}
	source, err := sourceStageColumns(sourceColumns)
	if err != nil {
		return err
	}
	if len(c.Steps) == 0 {
		return nil
	}
	stepIDs := make(map[string]bool, len(c.Steps))
	for i, step := range c.Steps {
		if !requiredID(step.ID) {
			return fmt.Errorf("steps[%d].id is required", i)
		}
		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate step id %q", step.ID)
		}
		stepIDs[step.ID] = true
		inputColumns, err := constructionStepInputSchema(c.Steps, source, i)
		if err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
		if err := validateConstructionStep(step, inputColumns); err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
	}
	return nil
}

func constructionStepInputSchema(steps []ConstructionStep, source []StageColumn, index int) ([]StageColumn, error) {
	step := steps[index]
	if len(step.Inputs) != 1 {
		return nil, fmt.Errorf("requires exactly one input")
	}
	input := step.Inputs[0]
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("inputs[0]: %w", err)
	}
	switch input.Kind {
	case ConstructionInputSourceProjection:
		if index != 0 {
			return nil, fmt.Errorf("must consume the preceding step output")
		}
		return source, nil
	case ConstructionInputStepOutput:
		if index == 0 || input.StepID != steps[index-1].ID {
			return nil, fmt.Errorf("must consume the preceding step output")
		}
		return steps[index-1].Outputs, nil
	case ConstructionInputTableRevision:
		return nil, fmt.Errorf("operation %q does not support table revision inputs", step.Operation.Kind)
	default:
		return nil, fmt.Errorf("inputs[0] has unsupported kind %q", input.Kind)
	}
}

func sourceStageColumns(columns []Column) ([]StageColumn, error) {
	stage := make([]StageColumn, len(columns))
	seenIDs := make(map[string]bool, len(columns))
	for i, column := range columns {
		if !requiredID(column.ColumnID) {
			return nil, fmt.Errorf("source column %q requires columnId in a staged construction", column.Column)
		}
		if seenIDs[column.ColumnID] {
			return nil, fmt.Errorf("source columns contain duplicate columnId %q", column.ColumnID)
		}
		seenIDs[column.ColumnID] = true
		stage[i] = StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}
	}
	if err := validateStageColumns(stage); err != nil {
		return nil, fmt.Errorf("source projection: %w", err)
	}
	return stage, nil
}

func validateConstructionStep(step ConstructionStep, inputColumns []StageColumn) error {
	if !requiredID(step.ID) {
		return fmt.Errorf("id is required")
	}
	input, err := stageColumnIndex(inputColumns)
	if err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if err := validateStageColumns(step.Outputs); err != nil {
		return fmt.Errorf("outputs: %w", err)
	}
	payloads := 0
	for _, present := range []bool{step.Operation.Pivot != nil, step.Operation.Derive != nil, step.Operation.Filter != nil, step.Operation.Unpivot != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("operation must contain exactly one payload matching kind")
	}
	switch step.Operation.Kind {
	case ConstructionOperationPivot:
		if step.Operation.Pivot == nil || step.Operation.Derive != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionPivot(step, input)
	case ConstructionOperationDerive:
		if step.Operation.Derive == nil || step.Operation.Pivot != nil || step.Operation.Filter != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionDerive(step, input, inputColumns)
	case ConstructionOperationFilter:
		if step.Operation.Filter == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Unpivot != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionFilter(step, input, inputColumns)
	case ConstructionOperationUnpivot:
		if step.Operation.Unpivot == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil {
			return fmt.Errorf("operation must contain exactly one payload matching kind")
		}
		return validateConstructionUnpivot(step, input, inputColumns)
	default:
		return fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
}

func validateStageColumns(columns []StageColumn) error {
	ids := make(map[string]bool, len(columns))
	names := make(map[string]bool, len(columns))
	for i, column := range columns {
		if !requiredID(column.ID) {
			return fmt.Errorf("outputs[%d].id is required", i)
		}
		if ids[column.ID] {
			return fmt.Errorf("outputs contain duplicate id %q", column.ID)
		}
		ids[column.ID] = true
		if !physicalColumnPattern.MatchString(column.Name) {
			return fmt.Errorf("output %q has invalid physical name %q", column.ID, column.Name)
		}
		if names[column.Name] {
			return fmt.Errorf("outputs contain duplicate name %q", column.Name)
		}
		names[column.Name] = true
		if strings.TrimSpace(column.Label) == "" {
			return fmt.Errorf("output %q label is required", column.ID)
		}
		if column.Type != "" && column.Type != "INFER" && column.Type != strings.TrimSpace(column.Type) {
			return fmt.Errorf("output %q type must not contain surrounding whitespace", column.ID)
		}
	}
	return nil
}

func stageColumnIndex(columns []StageColumn) (map[string]StageColumn, error) {
	index := make(map[string]StageColumn, len(columns))
	for _, column := range columns {
		if !requiredID(column.ID) {
			return nil, fmt.Errorf("column id is required")
		}
		if _, exists := index[column.ID]; exists {
			return nil, fmt.Errorf("duplicate column id %q", column.ID)
		}
		index[column.ID] = column
	}
	return index, nil
}

func validateConstructionPivot(step ConstructionStep, input map[string]StageColumn) error {
	pivot := step.Operation.Pivot
	if !sameOperationID(step.ID, pivot.ConstructionID) {
		return fmt.Errorf("pivot constructionId must equal step id")
	}
	if len(pivot.GroupKeyIDs) == 0 {
		return fmt.Errorf("pivot groupKeyIds must be non-empty")
	}
	groupIDs := make(map[string]bool, len(pivot.GroupKeyIDs))
	for _, id := range pivot.GroupKeyIDs {
		if !requiredID(id) {
			return fmt.Errorf("pivot groupKeyIds contain an empty id")
		}
		if groupIDs[id] {
			return fmt.Errorf("pivot groupKeyIds contain duplicate id %q", id)
		}
		if _, exists := input[id]; !exists {
			return missingConstructionColumn("groupKeyIds", id)
		}
		groupIDs[id] = true
	}
	for _, reference := range []struct{ field, id string }{
		{field: "categoryColumnId", id: pivot.CategoryColumnID},
		{field: "valueColumnId", id: pivot.ValueColumnID},
	} {
		field, id := reference.field, reference.id
		if !requiredID(id) {
			return fmt.Errorf("pivot %s is required", field)
		}
		if _, exists := input[id]; !exists {
			return missingConstructionColumn(field, id)
		}
		if groupIDs[id] {
			return fmt.Errorf("pivot %s cannot also be a group key", field)
		}
	}
	if pivot.CategoryColumnID == pivot.ValueColumnID {
		return fmt.Errorf("pivot category and value columns must differ")
	}
	if len(pivot.Categories) == 0 {
		return fmt.Errorf("pivot categories must be non-empty")
	}
	if !oneOf(string(pivot.DuplicatePolicy), "ERROR", "SUM", "MIN", "MAX") {
		return fmt.Errorf("pivot duplicatePolicy must be ERROR, SUM, MIN, or MAX")
	}
	if !oneOf(string(pivot.MissingCellPolicy), "NULL", "ERROR") {
		return fmt.Errorf("pivot missingCellPolicy must be NULL or ERROR")
	}
	if !oneOf(string(pivot.UnlistedCategoryPolicy), "ERROR", "EXCLUDE_WITH_EVIDENCE") {
		return fmt.Errorf("pivot unlistedCategoryPolicy must be ERROR or EXCLUDE_WITH_EVIDENCE")
	}
	seenKeys, outputIDs := map[string]bool{}, map[string]bool{}
	for i, category := range pivot.Categories {
		if err := category.Key.ValidatePivotCategoryKey(); err != nil {
			return fmt.Errorf("categories[%d].key: %w", i, err)
		}
		key := category.Key.identity()
		if seenKeys[key] {
			return fmt.Errorf("pivot categories contain duplicate key at index %d", i)
		}
		seenKeys[key] = true
		id := category.OutputColumnID
		if !requiredID(id) {
			return fmt.Errorf("categories[%d].outputColumnId is required", i)
		}
		if _, exists := input[id]; exists || groupIDs[id] || outputIDs[id] {
			return fmt.Errorf("pivot output column id %q is not unique", id)
		}
		outputIDs[id] = true
	}
	want := make([]string, 0, len(groupIDs)+len(outputIDs))
	want = append(want, pivot.GroupKeyIDs...)
	for _, category := range pivot.Categories {
		want = append(want, category.OutputColumnID)
	}
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionDerive(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	derive := step.Operation.Derive
	if !sameOperationID(step.ID, derive.ConstructionID) {
		return fmt.Errorf("derive constructionId must equal step id")
	}
	if !requiredID(derive.OutputColumnID) {
		return fmt.Errorf("derive outputColumnId is required")
	}
	if _, exists := input[derive.OutputColumnID]; exists {
		return fmt.Errorf("derive outputColumnId %q already exists", derive.OutputColumnID)
	}
	if !oneOf(string(derive.Operation), "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE") {
		return fmt.Errorf("unsupported derived operation %q", derive.Operation)
	}
	if !oneOf(string(derive.MissingInputPolicy), "PROPAGATE_NULL", "ERROR") {
		return fmt.Errorf("missingInputPolicy must be PROPAGATE_NULL or ERROR")
	}
	if derive.Operation == ConstructionDerivedDivide && !oneOf(string(derive.DivisionByZeroPolicy), "NULL", "ERROR") {
		return fmt.Errorf("DIVIDE requires divisionByZeroPolicy")
	}
	if derive.Operation != ConstructionDerivedDivide && derive.DivisionByZeroPolicy != "" {
		return fmt.Errorf("divisionByZeroPolicy is only valid for DIVIDE")
	}
	if err := validateConstructionOperand(derive.Left, input); err != nil {
		return fmt.Errorf("left: %w", err)
	}
	if err := validateConstructionOperand(derive.Right, input); err != nil {
		return fmt.Errorf("right: %w", err)
	}
	want := orderedStageIDs(inputColumns)
	want = append(want, derive.OutputColumnID)
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateConstructionOperand(operand ConstructionOperand, input map[string]StageColumn) error {
	switch operand.Kind {
	case ConstructionColumnOperand:
		if !requiredID(operand.ColumnID) || operand.Literal != nil {
			return fmt.Errorf("COLUMN operand must contain only columnId")
		}
		if _, exists := input[operand.ColumnID]; !exists {
			return missingConstructionColumn("columnId", operand.ColumnID)
		}
	case ConstructionLiteralOperand:
		if operand.ColumnID != "" || operand.Literal == nil {
			return fmt.Errorf("LITERAL operand must contain only literal")
		}
		if err := validateConstructionLiteral(*operand.Literal); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operand kind must be COLUMN or LITERAL")
	}
	return nil
}

func validateConstructionLiteral(literal ConstructionLiteral) error {
	payloads := 0
	if literal.Integer != nil {
		payloads++
	}
	if literal.Decimal != nil {
		payloads++
	}
	switch literal.Kind {
	case ConstructionNumericInteger:
		if payloads != 1 || literal.Integer == nil || literal.Decimal != nil {
			return fmt.Errorf("INTEGER literal must contain exactly one integer")
		}
	case ConstructionNumericDecimal:
		if payloads != 1 || literal.Decimal == nil || literal.Integer != nil {
			return fmt.Errorf("DECIMAL literal must contain exactly one decimal")
		}
		if math.IsNaN(*literal.Decimal) || math.IsInf(*literal.Decimal, 0) {
			return fmt.Errorf("DECIMAL literal must be finite")
		}
	default:
		return fmt.Errorf("unsupported literal kind %q", literal.Kind)
	}
	return nil
}

func validateConstructionFilter(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	filter := step.Operation.Filter
	if !requiredID(filter.ColumnID) {
		return fmt.Errorf("filter columnId is required")
	}
	if _, exists := input[filter.ColumnID]; !exists {
		return missingConstructionColumn("columnId", filter.ColumnID)
	}
	if !oneOf(string(filter.Operator), "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE") {
		return fmt.Errorf("unsupported filter operator %q", filter.Operator)
	}
	switch filter.Operator {
	case ConstructionFilterExists, ConstructionFilterMissing:
		if len(filter.Values) != 0 {
			return fmt.Errorf("%s does not accept values", filter.Operator)
		}
	case ConstructionFilterIn:
		if len(filter.Values) == 0 {
			return fmt.Errorf("IN requires at least one value")
		}
	default:
		if len(filter.Values) != 1 {
			return fmt.Errorf("%s requires exactly one value", filter.Operator)
		}
	}
	for i, value := range filter.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("values[%d]: %w", i, err)
		}
	}
	return validateDeclaredOutputIDs(step.Outputs, orderedStageIDs(inputColumns))
}

func (v FilterValue) Validate() error {
	payloads := 0
	for _, present := range []bool{v.String != nil, v.Code != nil, v.Boolean != nil, v.Integer != nil, v.Decimal != nil, v.Date != nil, v.DateTime != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("filter value must contain exactly one value payload")
	}
	switch v.Kind {
	case ConstructionFilterString:
		if v.String == nil {
			return fmt.Errorf("STRING value requires string")
		}
	case ConstructionFilterCode:
		if v.Code == nil || !requiredID(v.Code.Code) {
			return fmt.Errorf("CODE value requires a non-empty code")
		}
	case ConstructionFilterBoolean:
		if v.Boolean == nil {
			return fmt.Errorf("BOOLEAN value requires boolean")
		}
	case ConstructionFilterInteger:
		if v.Integer == nil {
			return fmt.Errorf("INTEGER value requires integer")
		}
	case ConstructionFilterDecimal:
		if v.Decimal == nil || math.IsNaN(*v.Decimal) || math.IsInf(*v.Decimal, 0) {
			return fmt.Errorf("DECIMAL value requires a finite decimal")
		}
	case ConstructionFilterDate:
		if v.Date == nil || strings.TrimSpace(*v.Date) == "" {
			return fmt.Errorf("DATE value requires a date")
		}
	case ConstructionFilterDateTime:
		if v.DateTime == nil || strings.TrimSpace(*v.DateTime) == "" {
			return fmt.Errorf("DATE_TIME value requires a dateTime")
		}
	default:
		return fmt.Errorf("unsupported filter value kind %q", v.Kind)
	}
	return nil
}

func validateConstructionUnpivot(step ConstructionStep, input map[string]StageColumn, inputColumns []StageColumn) error {
	unpivot := step.Operation.Unpivot
	if !sameOperationID(step.ID, unpivot.ConstructionID) {
		return fmt.Errorf("unpivot constructionId must equal step id")
	}
	if len(unpivot.Inputs) == 0 {
		return fmt.Errorf("unpivot inputs must be non-empty")
	}
	if !oneOf(string(unpivot.NullRowPolicy), "DROP", "PRESERVE") {
		return fmt.Errorf("nullRowPolicy must be DROP or PRESERVE")
	}
	inputIDs := make(map[string]bool, len(unpivot.Inputs))
	keys := make(map[string]bool, len(unpivot.Inputs))
	for i, item := range unpivot.Inputs {
		if !requiredID(item.ColumnID) {
			return fmt.Errorf("inputs[%d].columnId is required", i)
		}
		if _, exists := input[item.ColumnID]; !exists {
			return missingConstructionColumn(fmt.Sprintf("inputs[%d].columnId", i), item.ColumnID)
		}
		if inputIDs[item.ColumnID] {
			return fmt.Errorf("unpivot inputs contain duplicate column id %q", item.ColumnID)
		}
		inputIDs[item.ColumnID] = true
		if err := item.Key.ValidateConcreteValue(); err != nil {
			return fmt.Errorf("inputs[%d].key: %w", i, err)
		}
		key := item.Key.identity()
		if keys[key] {
			return fmt.Errorf("unpivot inputs contain duplicate key at index %d", i)
		}
		keys[key] = true
	}
	if !requiredID(unpivot.KeyOutputColumnID) || !requiredID(unpivot.ValueOutputColumnID) || unpivot.KeyOutputColumnID == unpivot.ValueOutputColumnID {
		return fmt.Errorf("unpivot requires distinct key and value output column ids")
	}
	if _, exists := input[unpivot.KeyOutputColumnID]; exists {
		return fmt.Errorf("unpivot key output column id already exists")
	}
	if _, exists := input[unpivot.ValueOutputColumnID]; exists {
		return fmt.Errorf("unpivot value output column id already exists")
	}
	want := make([]string, 0, len(input)-len(inputIDs)+2)
	for _, column := range inputColumns {
		if !inputIDs[column.ID] {
			want = append(want, column.ID)
		}
	}
	want = append(want, unpivot.KeyOutputColumnID, unpivot.ValueOutputColumnID)
	return validateDeclaredOutputIDs(step.Outputs, want)
}

func validateDeclaredOutputIDs(outputs []StageColumn, expected []string) error {
	if len(outputs) != len(expected) {
		return fmt.Errorf("outputs contain %d columns; operation requires %d", len(outputs), len(expected))
	}
	declared := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		declared[output.ID] = true
	}
	for _, id := range expected {
		if !declared[id] {
			return fmt.Errorf("outputs are missing required column id %q", id)
		}
	}
	return nil
}

func orderedStageIDs(columns []StageColumn) []string {
	ids := make([]string, 0, len(columns))
	for _, column := range columns {
		ids = append(ids, column.ID)
	}
	return ids
}

func missingConstructionColumn(field, id string) error {
	return fmt.Errorf("%s references missing column id %q", field, id)
}

func sameOperationID(stepID, operationID string) bool {
	return requiredID(operationID) && stepID == operationID
}
