package recipe

import (
	"fmt"
	"math"
	"strings"
)

// ConstructionSourceProjectionID names the implicit source projection stage
// that precedes every authored construction step.
const ConstructionSourceProjectionID = "source_projection"

const maxConstructionSteps = 128

type Construction struct {
	Version int                `json:"version"`
	Steps   []ConstructionStep `json:"steps"`
}

// ConstructionStep applies one typed operation to the preceding stage and
// declares that stage's complete output schema. Column IDs are stable across
// display-name edits; Names are physical output names.
type ConstructionStep struct {
	ID        string                 `json:"id"`
	Inputs    []ConstructionInputRef `json:"inputs"`
	Operation ConstructionOperation  `json:"operation"`
	Outputs   []StageColumn          `json:"outputs"`
}

type ConstructionInputKind string

const (
	ConstructionSourceProjectionInput ConstructionInputKind = "SOURCE_PROJECTION"
	ConstructionStepOutputInput       ConstructionInputKind = "STEP_OUTPUT"
	ConstructionTableRevisionInput    ConstructionInputKind = "TABLE_REVISION"
)

// ConstructionInputRef is a closed reference to the input row set. The
// compiler currently executes source and prior-step inputs; table revisions
// are represented here for the later Combine package and rejected until its
// immutable table-input execution contract is available.
type ConstructionInputRef struct {
	Kind       ConstructionInputKind `json:"kind"`
	StepID     string                `json:"stepId,omitempty"`
	TableID    string                `json:"tableId,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	OutputID   string                `json:"outputId,omitempty"`
}

// StageColumn is the logical schema contract for one stage output. Type is an
// optional authoring hint (or "INFER"); the compiler derives the authoritative
// type from source and operation semantics.
type StageColumn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	Type  string `json:"type,omitempty"`
}

type ConstructionOperationKind string

const (
	ConstructionPivotOp   ConstructionOperationKind = "PIVOT"
	ConstructionDeriveOp  ConstructionOperationKind = "DERIVE"
	ConstructionFilterOp  ConstructionOperationKind = "FILTER"
	ConstructionUnpivotOp ConstructionOperationKind = "UNPIVOT"
)

// ConstructionOperation is a closed tagged union. Its operands refer to
// stable stage column IDs, never mutable display or physical names.
type ConstructionOperation struct {
	Kind    ConstructionOperationKind `json:"kind"`
	Pivot   *ConstructionPivot        `json:"pivot,omitempty"`
	Derive  *ConstructionDerive       `json:"derive,omitempty"`
	Filter  *ConstructionFilter       `json:"filter,omitempty"`
	Unpivot *ConstructionUnpivot      `json:"unpivot,omitempty"`
}

type ConstructionPivot struct {
	ConstructionID         string                      `json:"constructionId"`
	GroupKeyIDs            []string                    `json:"groupKeyIds"`
	CategoryColumnID       string                      `json:"categoryColumnId"`
	ValueColumnID          string                      `json:"valueColumnId"`
	Categories             []ConstructionPivotCategory `json:"categories"`
	DuplicatePolicy        PivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      PivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy PivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type ConstructionPivotCategory struct {
	Key            TableScalar `json:"key"`
	OutputColumnID string      `json:"outputColumnId"`
}

type ConstructionDerive struct {
	ConstructionID       string               `json:"constructionId"`
	OutputColumnID       string               `json:"outputColumnId"`
	Operation            DerivedOperation     `json:"operation"`
	Left                 ConstructionOperand  `json:"left"`
	Right                ConstructionOperand  `json:"right"`
	MissingInputPolicy   MissingInputPolicy   `json:"missingInputPolicy"`
	DivisionByZeroPolicy DivisionByZeroPolicy `json:"divisionByZeroPolicy,omitempty"`
}

type ConstructionOperand struct {
	Kind     DerivedOperandKind `json:"kind"`
	ColumnID string             `json:"columnId,omitempty"`
	Literal  *DerivedLiteral    `json:"literal,omitempty"`
}

type ConstructionFilter struct {
	ColumnID string         `json:"columnId"`
	Operator FilterOperator `json:"operator"`
	Values   []FilterValue  `json:"values,omitempty"`
}

type ConstructionUnpivot struct {
	ConstructionID      string                     `json:"constructionId"`
	Inputs              []ConstructionUnpivotInput `json:"inputs"`
	KeyOutputColumnID   string                     `json:"keyOutputColumnId"`
	ValueOutputColumnID string                     `json:"valueOutputColumnId"`
	NullRowPolicy       UnpivotNullRowPolicy       `json:"nullRowPolicy"`
}

type ConstructionUnpivotInput struct {
	ColumnID string      `json:"columnId"`
	Key      TableScalar `json:"key"`
}

func (construction Construction) Validate(sourceFields []Field) error {
	if construction.Version != 1 {
		return fmt.Errorf("construction version must be 1")
	}
	if len(construction.Steps) == 0 || len(construction.Steps) > maxConstructionSteps {
		return fmt.Errorf("construction steps must contain between 1 and %d entries", maxConstructionSteps)
	}
	sourceColumns := make([]StageColumn, 0, len(sourceFields))
	for _, field := range sourceFields {
		if strings.TrimSpace(field.ColumnID) == "" {
			return fmt.Errorf("source field %q requires a stable columnId", field.Name)
		}
		sourceColumns = append(sourceColumns, StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
	}
	if err := validateStageColumns(sourceColumns, "source projection"); err != nil {
		return err
	}
	priorStepID := ""
	priorColumns := sourceColumns
	stepIDs := make(map[string]bool, len(construction.Steps))
	constructionIDs := make(map[string]bool, len(construction.Steps))
	for index, step := range construction.Steps {
		path := fmt.Sprintf("steps[%d]", index)
		if err := validateOpaqueIdentity(step.ID, path+".id"); err != nil {
			return err
		}
		if step.ID == ConstructionSourceProjectionID || stepIDs[step.ID] {
			return fmt.Errorf("%s.id is reserved or duplicated", path)
		}
		stepIDs[step.ID] = true
		if len(step.Inputs) != 1 {
			return fmt.Errorf("%s.inputs must contain exactly one stage reference", path)
		}
		input := step.Inputs[0]
		switch input.Kind {
		case ConstructionSourceProjectionInput:
			if index != 0 || input.StepID != "" || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
				return fmt.Errorf("%s.inputs[0] must reference the source projection only on the first step", path)
			}
		case ConstructionStepOutputInput:
			if index == 0 || input.StepID != priorStepID || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
				return fmt.Errorf("%s.inputs[0] must reference the immediately preceding step", path)
			}
		case ConstructionTableRevisionInput:
			if strings.TrimSpace(input.TableID) == "" || strings.TrimSpace(input.RevisionID) == "" || strings.TrimSpace(input.OutputID) == "" || input.StepID != "" {
				return fmt.Errorf("%s.inputs[0] requires tableId, immutable revisionId, and outputId", path)
			}
			return fmt.Errorf("%s.inputs[0] table revision inputs are not supported by this compiler", path)
		default:
			return fmt.Errorf("%s.inputs[0] has unsupported kind %q", path, input.Kind)
		}
		if input.Kind == ConstructionStepOutputInput && !stepIDs[input.StepID] {
			return fmt.Errorf("%s.inputs[0] references unknown prior step %q", path, input.StepID)
		}
		if err := validateConstructionOperation(step.Operation, priorColumns, step.Outputs, path+".operation", constructionIDs); err != nil {
			return err
		}
		if err := validateStageColumns(step.Outputs, path+".outputs"); err != nil {
			return err
		}
		priorStepID = step.ID
		priorColumns = step.Outputs
	}
	return nil
}

func validateConstructionOperation(operation ConstructionOperation, input, output []StageColumn, path string, constructionIDs map[string]bool) error {
	payloads := 0
	for _, present := range []bool{operation.Pivot != nil, operation.Derive != nil, operation.Filter != nil, operation.Unpivot != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("%s must contain exactly one operation payload", path)
	}
	inputByID := stageColumnMap(input)
	outputByID := stageColumnMap(output)
	switch operation.Kind {
	case ConstructionPivotOp:
		if operation.Pivot == nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil {
			return fmt.Errorf("%s pivot operation requires only pivot payload", path)
		}
		pivot := operation.Pivot
		if err := validateConstructionID(pivot.ConstructionID, path+".pivot.constructionId", constructionIDs); err != nil {
			return err
		}
		if len(pivot.GroupKeyIDs) == 0 || len(pivot.Categories) == 0 || len(pivot.Categories) > maxPivotColumns {
			return fmt.Errorf("%s pivot requires group keys and 1..%d categories", path, maxPivotColumns)
		}
		if pivot.CategoryColumnID == "" || pivot.ValueColumnID == "" || pivot.CategoryColumnID == pivot.ValueColumnID {
			return fmt.Errorf("%s pivot category and value column IDs must be distinct and non-empty", path)
		}
		used := map[string]bool{pivot.CategoryColumnID: true, pivot.ValueColumnID: true}
		for _, id := range pivot.GroupKeyIDs {
			if id == "" || used[id] || inputByID[id].ID == "" {
				return fmt.Errorf("%s pivot group key %q is missing or duplicated", path, id)
			}
			used[id] = true
		}
		if inputByID[pivot.CategoryColumnID].ID == "" || inputByID[pivot.ValueColumnID].ID == "" {
			return fmt.Errorf("%s pivot category or value column is not in the input schema", path)
		}
		if !validPivotDuplicatePolicy(pivot.DuplicatePolicy) || !validPivotMissingPolicy(pivot.MissingCellPolicy) || !validPivotUnlistedPolicy(pivot.UnlistedCategoryPolicy) {
			return fmt.Errorf("%s pivot policies are incomplete or unsupported", path)
		}
		expected := make(map[string]bool, len(pivot.GroupKeyIDs)+len(pivot.Categories))
		for _, id := range pivot.GroupKeyIDs {
			expected[id] = true
		}
		for index, category := range pivot.Categories {
			if err := category.Key.ValidatePivotCategoryKey(); err != nil {
				return fmt.Errorf("%s pivot categories[%d].key: %w", path, index, err)
			}
			if category.OutputColumnID == "" || expected[category.OutputColumnID] || inputByID[category.OutputColumnID].ID != "" {
				return fmt.Errorf("%s pivot category output ID %q is empty, duplicated, or collides with input", path, category.OutputColumnID)
			}
			expected[category.OutputColumnID] = true
		}
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionDeriveOp:
		if operation.Derive == nil || operation.Pivot != nil || operation.Filter != nil || operation.Unpivot != nil {
			return fmt.Errorf("%s derive operation requires only derive payload", path)
		}
		derive := operation.Derive
		if err := validateConstructionID(derive.ConstructionID, path+".derive.constructionId", constructionIDs); err != nil {
			return err
		}
		if derive.OutputColumnID == "" || inputByID[derive.OutputColumnID].ID != "" {
			return fmt.Errorf("%s derive outputColumnId is empty or already exists", path)
		}
		if err := validateConstructionDerivedPolicies(*derive, path+".derive"); err != nil {
			return err
		}
		if err := validateConstructionOperand(derive.Left, inputByID, path+".derive.left"); err != nil {
			return err
		}
		if err := validateConstructionOperand(derive.Right, inputByID, path+".derive.right"); err != nil {
			return err
		}
		expected := make(map[string]bool, len(inputByID)+1)
		for id := range inputByID {
			expected[id] = true
		}
		expected[derive.OutputColumnID] = true
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionFilterOp:
		if operation.Filter == nil || operation.Pivot != nil || operation.Derive != nil || operation.Unpivot != nil {
			return fmt.Errorf("%s filter operation requires only filter payload", path)
		}
		filter := operation.Filter
		if filter.ColumnID == "" || inputByID[filter.ColumnID].ID == "" {
			return fmt.Errorf("%s filter columnId is missing from input schema", path)
		}
		if err := validateConstructionFilter(*filter, path+".filter"); err != nil {
			return err
		}
		expected := make(map[string]bool, len(inputByID))
		for id := range inputByID {
			expected[id] = true
		}
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionUnpivotOp:
		if operation.Unpivot == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil {
			return fmt.Errorf("%s unpivot operation requires only unpivot payload", path)
		}
		unpivot := operation.Unpivot
		if err := validateConstructionID(unpivot.ConstructionID, path+".unpivot.constructionId", constructionIDs); err != nil {
			return err
		}
		if len(unpivot.Inputs) == 0 || unpivot.KeyOutputColumnID == "" || unpivot.ValueOutputColumnID == "" || unpivot.KeyOutputColumnID == unpivot.ValueOutputColumnID {
			return fmt.Errorf("%s unpivot requires inputs and distinct key/value output IDs", path)
		}
		selected := make(map[string]bool, len(unpivot.Inputs))
		keys := make(map[string]bool, len(unpivot.Inputs))
		expected := make(map[string]bool, len(inputByID)+2)
		for id := range inputByID {
			expected[id] = true
		}
		for index, item := range unpivot.Inputs {
			if item.ColumnID == "" || inputByID[item.ColumnID].ID == "" || selected[item.ColumnID] {
				return fmt.Errorf("%s unpivot inputs[%d].columnId is missing or duplicated", path, index)
			}
			if err := item.Key.ValidateConcreteValue(); err != nil {
				return fmt.Errorf("%s unpivot inputs[%d].key: %w", path, index, err)
			}
			identity := item.Key.identity()
			if keys[identity] {
				return fmt.Errorf("%s unpivot input keys must be unique", path)
			}
			keys[identity] = true
			selected[item.ColumnID] = true
			delete(expected, item.ColumnID)
		}
		if unpivot.NullRowPolicy != UnpivotNullDrop && unpivot.NullRowPolicy != UnpivotNullPreserve {
			return fmt.Errorf("%s unpivot nullRowPolicy is unsupported", path)
		}
		if inputByID[unpivot.KeyOutputColumnID].ID != "" || inputByID[unpivot.ValueOutputColumnID].ID != "" {
			return fmt.Errorf("%s unpivot output IDs collide with input schema", path)
		}
		expected[unpivot.KeyOutputColumnID] = true
		expected[unpivot.ValueOutputColumnID] = true
		return requireExactStageOutputIDs(expected, outputByID, path)
	default:
		return fmt.Errorf("%s has unsupported operation kind %q", path, operation.Kind)
	}
}

func validateConstructionFilter(filter ConstructionFilter, path string) error {
	if !filter.Operator.Valid() {
		return fmt.Errorf("%s operator %q is unsupported", path, filter.Operator)
	}
	if filter.Operator == FilterExists || filter.Operator == FilterMissing {
		if len(filter.Values) != 0 {
			return fmt.Errorf("%s operator %s does not accept values", path, filter.Operator)
		}
		return nil
	}
	if filter.Operator == FilterIn {
		if len(filter.Values) == 0 {
			return fmt.Errorf("%s IN requires values", path)
		}
	} else if len(filter.Values) != 1 {
		return fmt.Errorf("%s operator %s requires exactly one value", path, filter.Operator)
	}
	for index, value := range filter.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("%s values[%d]: %w", path, index, err)
		}
		if !filterOperatorSupportsKind(filter.Operator, value.Kind) {
			return fmt.Errorf("%s operator %s is incompatible with %s", path, filter.Operator, value.Kind)
		}
	}
	return nil
}

func validateConstructionOperand(operand ConstructionOperand, columns map[string]StageColumn, path string) error {
	switch operand.Kind {
	case DerivedColumnOperand:
		if operand.ColumnID == "" || operand.Literal != nil || columns[operand.ColumnID].ID == "" {
			return fmt.Errorf("%s COLUMN requires a known columnId only", path)
		}
	case DerivedLiteralOperand:
		if operand.ColumnID != "" || operand.Literal == nil {
			return fmt.Errorf("%s LITERAL requires a literal only", path)
		}
		if err := validateDerivedLiteral(*operand.Literal, path+".literal"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s kind must be COLUMN or LITERAL", path)
	}
	return nil
}

func validateDerivedLiteral(literal DerivedLiteral, path string) error {
	switch literal.Kind {
	case NumericInteger:
		if literal.Integer == nil || literal.Decimal != nil {
			return fmt.Errorf("%s INTEGER requires only an integer value", path)
		}
	case NumericDecimal:
		if literal.Decimal == nil || literal.Integer != nil || math.IsNaN(*literal.Decimal) || math.IsInf(*literal.Decimal, 0) {
			return fmt.Errorf("%s DECIMAL requires one finite decimal value", path)
		}
	default:
		return fmt.Errorf("%s kind must be INTEGER or DECIMAL", path)
	}
	return nil
}

func validateStageColumns(columns []StageColumn, path string) error {
	if len(columns) == 0 {
		return fmt.Errorf("%s must contain at least one column", path)
	}
	ids := make(map[string]bool, len(columns))
	names := make(map[string]bool, len(columns))
	for index, column := range columns {
		columnPath := fmt.Sprintf("%s[%d]", path, index)
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) || ids[column.ID] {
			return fmt.Errorf("%s.id is empty, untrimmed, or duplicated", columnPath)
		}
		if err := validateRecipeName(column.Name, columnPath+".name"); err != nil {
			return err
		}
		if names[column.Name] {
			return fmt.Errorf("%s.name %q is duplicated", columnPath, column.Name)
		}
		if column.Type != "" && !strings.EqualFold(column.Type, "INFER") && !validConstructionLogicalType(column.Type) {
			return fmt.Errorf("%s.type %q is unsupported", columnPath, column.Type)
		}
		ids[column.ID] = true
		names[column.Name] = true
	}
	return nil
}

func validConstructionLogicalType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "boolean", "code", "date", "date_time", "datetime", "decimal", "integer", "number", "object", "string", "uuid":
		return true
	default:
		return false
	}
}

func stageColumnMap(columns []StageColumn) map[string]StageColumn {
	result := make(map[string]StageColumn, len(columns))
	for _, column := range columns {
		result[column.ID] = column
	}
	return result
}

func requireExactStageOutputIDs(expected map[string]bool, actual map[string]StageColumn, path string) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("%s outputs do not match the operation's complete output schema", path)
	}
	for id := range expected {
		if actual[id].ID == "" {
			return fmt.Errorf("%s outputs are missing column ID %q", path, id)
		}
	}
	return nil
}

func validateConstructionDerivedPolicies(derive ConstructionDerive, path string) error {
	switch derive.Operation {
	case DerivedAdd, DerivedSubtract, DerivedMultiply:
		if derive.DivisionByZeroPolicy != "" {
			return fmt.Errorf("%s divisionByZeroPolicy is only valid for DIVIDE", path)
		}
	case DerivedDivide:
		if derive.DivisionByZeroPolicy != DivisionByZeroNull && derive.DivisionByZeroPolicy != DivisionByZeroError {
			return fmt.Errorf("%s DIVIDE requires NULL or ERROR divisionByZeroPolicy", path)
		}
	default:
		return fmt.Errorf("%s operation must be ADD, SUBTRACT, MULTIPLY, or DIVIDE", path)
	}
	if derive.MissingInputPolicy != MissingInputPropagateNull && derive.MissingInputPolicy != MissingInputError {
		return fmt.Errorf("%s missingInputPolicy must be PROPAGATE_NULL or ERROR", path)
	}
	return nil
}

func validateConstructionID(value, path string, seen map[string]bool) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s is required and must be trimmed", path)
	}
	if seen[value] {
		return fmt.Errorf("%s duplicates another construction ID", path)
	}
	seen[value] = true
	return nil
}

func validateOpaqueIdentity(value, path string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > 256 {
		return fmt.Errorf("%s is required, trimmed, and at most 256 characters", path)
	}
	return nil
}

func validPivotDuplicatePolicy(policy PivotDuplicatePolicy) bool {
	return policy == PivotDuplicateError || policy == PivotDuplicateSum || policy == PivotDuplicateMin || policy == PivotDuplicateMax
}

func validPivotMissingPolicy(policy PivotMissingCellPolicy) bool {
	return policy == PivotMissingCellNull || policy == PivotMissingCellError
}

func validPivotUnlistedPolicy(policy PivotUnlistedCategoryPolicy) bool {
	return policy == PivotUnlistedCategoryError || policy == PivotUnlistedCategoryExcludeWithEvidence
}
