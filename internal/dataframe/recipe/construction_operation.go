package recipe

import (
	"fmt"
	"math"

	"github.com/calypr/loom/internal/dataframe/spec"
)

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

type ConstructionExpand struct {
	ConstructionID  string               `json:"constructionId"`
	InputColumnID   string               `json:"inputColumnId"`
	OutputColumnID  string               `json:"outputColumnId"`
	OrdinalColumnID string               `json:"ordinalColumnId,omitempty"`
	EmptyPolicy     ExpansionEmptyPolicy `json:"emptyPolicy,omitempty"`
}

func validateConstructionOperation(operation ConstructionOperation, input, output []StageColumn, rowValues []ConstructionRowValue, path string, constructionIDs map[string]bool) error {
	rowValueIDs := make(map[string]bool, len(rowValues))
	if len(rowValues) != 0 {
		switch operation.Kind {
		case ConstructionGroupOp, ConstructionCodedGroupOp, ConstructionPivotOp, ConstructionCodedPivotOp:
		default:
			return fmt.Errorf("%s rowValues are supported only on GROUP, CODED_GROUP, PIVOT, and CODED_PIVOT", path)
		}
	}
	inputColumns, outputColumns := stageColumnMap(input), stageColumnMap(output)
	for index, rowValue := range rowValues {
		if rowValue.FieldSource == nil {
			if !validConstructionColumnID(rowValue.InputColumnID) || inputColumns[rowValue.InputColumnID].ID == "" {
				return fmt.Errorf("%s.rowValues[%d].inputColumnId is missing from the input schema", path, index)
			}
		} else {
			if operation.Kind != ConstructionGroupOp || rowValue.InputColumnID != "" {
				return fmt.Errorf("%s.rowValues[%d] must choose exactly one source representation and fieldSource is only supported on GROUP", path, index)
			}
			if err := validateConstructionGroupScalarFieldSource(*rowValue.FieldSource, fmt.Sprintf("%s.rowValues[%d].fieldSource", path, index)); err != nil {
				return err
			}
		}
		if !validConstructionColumnID(rowValue.OutputColumnID) || inputColumns[rowValue.OutputColumnID].ID != "" || rowValueIDs[rowValue.OutputColumnID] {
			return fmt.Errorf("%s.rowValues[%d].outputColumnId is empty, duplicated, or collides with the input schema", path, index)
		}
		if _, ok := outputColumns[rowValue.OutputColumnID]; !ok {
			return fmt.Errorf("%s.rowValues[%d].outputColumnId %q is missing from step outputs", path, index, rowValue.OutputColumnID)
		}
		switch rowValue.Policy {
		case ConstructionRowValueAll, ConstructionRowValueOne:
		default:
			return fmt.Errorf("%s.rowValues[%d].policy %q is unsupported", path, index, rowValue.Policy)
		}
		rowValueIDs[rowValue.OutputColumnID] = true
	}
	baseOutput := make([]StageColumn, 0, len(output)-len(rowValues))
	for _, column := range output {
		if !rowValueIDs[column.ID] {
			baseOutput = append(baseOutput, column)
		}
	}
	output = baseOutput
	payloads := 0
	for _, present := range []bool{operation.Pivot != nil, operation.Derive != nil, operation.Filter != nil, operation.Unpivot != nil, operation.Group != nil, operation.CodedGroup != nil, operation.CodedPivot != nil, operation.Expand != nil, operation.RelatedSource != nil, operation.RelatedExpand != nil, operation.RelatedEligibility != nil, operation.RelatedField != nil} {
		if present {
			payloads++
		}
	}
	if operation.Combine != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("%s must contain exactly one operation payload", path)
	}
	inputByID := stageColumnMap(input)
	outputByID := stageColumnMap(output)
	switch operation.Kind {
	case ConstructionPivotOp:
		if operation.Pivot == nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s pivot operation requires only pivot payload", path)
		}
		pivot := operation.Pivot
		if err := validateConstructionID(pivot.ConstructionID, path+".pivot.constructionId", constructionIDs); err != nil {
			return err
		}
		if pivot.Discovery != nil {
			if len(pivot.Categories) != 0 {
				return fmt.Errorf("%s pivot cannot combine explicit categories with discovery", path)
			}
			if pivot.Discovery.MaxCategories <= 0 || pivot.Discovery.MaxCategories > maxPivotColumns {
				return fmt.Errorf("%s pivot discovery maxCategories must be 1..%d", path, maxPivotColumns)
			}
			return fmt.Errorf("%s pivot discovery must be resolved to concrete categories before compilation", path)
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
		for _, id := range pivot.GroupKeyIDs {
			if scalar, known := constructionPivotTypeIsScalar(inputByID[id].Type); known && !scalar {
				return fmt.Errorf("%s pivot group key %q type %q is not a scalar", path, id, inputByID[id].Type)
			}
		}
		for _, reference := range []struct{ field, id string }{
			{field: "categoryColumnId", id: pivot.CategoryColumnID},
			{field: "valueColumnId", id: pivot.ValueColumnID},
		} {
			if scalar, known := constructionPivotTypeIsScalar(inputByID[reference.id].Type); known && !scalar {
				return fmt.Errorf("%s pivot %s column %q type %q is not a scalar", path, reference.field, reference.id, inputByID[reference.id].Type)
			}
		}
		if !validPivotDuplicatePolicy(pivot.DuplicatePolicy) || !validPivotMissingPolicy(pivot.MissingCellPolicy) || !validPivotUnlistedPolicy(pivot.UnlistedCategoryPolicy) {
			return fmt.Errorf("%s pivot policies are incomplete or unsupported", path)
		}
		categoryKind, categoryKindKnown := constructionPivotScalarKind(inputByID[pivot.CategoryColumnID].Type)
		if pivot.DuplicatePolicy == PivotDuplicateSum {
			valueKind, valueKindKnown := constructionPivotScalarKind(inputByID[pivot.ValueColumnID].Type)
			if valueKindKnown && valueKind != "INTEGER" && valueKind != "DECIMAL" {
				return fmt.Errorf("%s pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got %q", path, inputByID[pivot.ValueColumnID].Type)
			}
		}
		expected := make(map[string]bool, len(pivot.GroupKeyIDs)+len(pivot.Categories))
		for _, id := range pivot.GroupKeyIDs {
			expected[id] = true
		}
		categoryKeys := make(map[string]bool, len(pivot.Categories))
		for index, category := range pivot.Categories {
			if err := category.Key.ValidatePivotCategoryKey(); err != nil {
				return fmt.Errorf("%s pivot categories[%d].key: %w", path, index, err)
			}
			if categoryKindKnown && category.Key.Kind != TableScalarNull && category.Key.Kind != TableScalarMissing &&
				string(category.Key.Kind) != categoryKind {
				return fmt.Errorf("%s pivot categories[%d].key type %q does not match category column type %q", path, index, category.Key.Kind, inputByID[pivot.CategoryColumnID].Type)
			}
			identity := category.Key.identity()
			if categoryKeys[identity] {
				return fmt.Errorf("%s pivot category keys must be unique", path)
			}
			categoryKeys[identity] = true
			if category.OutputColumnID == "" || expected[category.OutputColumnID] || inputByID[category.OutputColumnID].ID != "" {
				return fmt.Errorf("%s pivot category output ID %q is empty, duplicated, or collides with input", path, category.OutputColumnID)
			}
			expected[category.OutputColumnID] = true
		}
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionDeriveOp:
		if operation.Derive == nil || operation.Pivot != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
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
		if operation.Filter == nil || operation.Pivot != nil || operation.Derive != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
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
		if operation.Unpivot == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
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
	case ConstructionGroupOp:
		if operation.Group == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s group operation requires only group payload", path)
		}
		return validateConstructionGroup(*operation.Group, inputByID, outputByID, path, constructionIDs, len(rowValues) != 0)
	case ConstructionCodedGroupOp:
		if operation.CodedGroup == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil || operation.RelatedSource != nil || operation.RelatedExpand != nil || operation.RelatedEligibility != nil || operation.RelatedField != nil {
			return fmt.Errorf("%s coded group operation requires only codedGroup payload", path)
		}
		return validateConstructionCodedGroup(*operation.CodedGroup, inputByID, outputByID, path, constructionIDs)
	case ConstructionCodedPivotOp:
		if operation.CodedPivot == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.CodedGroup != nil || operation.Expand != nil || operation.Combine != nil || operation.RelatedSource != nil || operation.RelatedExpand != nil || operation.RelatedEligibility != nil || operation.RelatedField != nil {
			return fmt.Errorf("%s coded pivot operation requires only codedPivot payload", path)
		}
		return validateConstructionCodedPivot(*operation.CodedPivot, inputByID, outputByID, path, constructionIDs)
	case ConstructionExpandOp:
		if operation.Expand == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Combine != nil {
			return fmt.Errorf("%s expand operation requires only expand payload", path)
		}
		return validateConstructionExpand(*operation.Expand, inputByID, outputByID, path, constructionIDs)
	case ConstructionRelatedSourceOp:
		if operation.RelatedSource == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s related source operation requires only relatedSource payload", path)
		}
		return validateConstructionRelatedSource(*operation.RelatedSource, inputByID, outputByID, path)
	case ConstructionRelatedExpandOp:
		if operation.RelatedExpand == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil || operation.RelatedSource != nil || operation.RelatedField != nil {
			return fmt.Errorf("%s related expansion operation requires only relatedExpand payload", path)
		}
		return validateConstructionRelatedExpand(*operation.RelatedExpand, inputByID, outputByID, path)
	case ConstructionRelatedEligibilityOp:
		if operation.RelatedEligibility == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil || operation.RelatedSource != nil || operation.RelatedExpand != nil || operation.RelatedField != nil {
			return fmt.Errorf("%s related eligibility operation requires only relatedEligibility payload", path)
		}
		return validateConstructionRelatedEligibility(*operation.RelatedEligibility, inputByID, outputByID, path)
	case ConstructionRelatedFieldOp:
		if operation.RelatedField == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil || operation.RelatedSource != nil || operation.RelatedExpand != nil {
			return fmt.Errorf("%s related field operation requires only relatedField payload", path)
		}
		return validateConstructionRelatedField(*operation.RelatedField, inputByID, outputByID, path)
	case ConstructionCombineOp:
		return fmt.Errorf("%s combine must be the sole terminal operation over exact table revision inputs", path)
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
		if !spec.OperatorSupportsKind(filter.Operator, value.Kind) {
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
