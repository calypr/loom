package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

type TableShapeSavedSupport struct {
	State      tableshapecap.AvailabilityState `json:"state"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Message    string                          `json:"message,omitempty"`
}

type TableShapeSavedProposalIntent struct {
	Kind                string                   `json:"kind"`
	ReshapeModeChoiceID string                   `json:"reshapeModeChoiceId"`
	Support             TableShapeSavedSupport   `json:"support"`
	Pivot               *TableShapeSavedPivot    `json:"pivot,omitempty"`
	Unpivot             *TableShapeSavedUnpivot  `json:"unpivot,omitempty"`
	DerivedColumns      []TableShapeSavedDerived `json:"derivedColumns"`
}

type TableShapeSavedPivot struct {
	PivotResolutionID       string                         `json:"pivotResolutionId"`
	GroupColumnChoiceIDs    []string                       `json:"groupColumnChoiceIds"`
	CategoryColumnChoiceID  string                         `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID     string                         `json:"valueColumnChoiceId"`
	CategoryDiscoveryID     string                         `json:"categoryDiscoveryId"`
	Categories              []TableShapeSavedPivotCategory `json:"includedCategories"`
	DerivedOperands         []TableShapeCatalogChoice      `json:"derivedOperands"`
	DuplicatePolicyChoiceID string                         `json:"duplicatePolicyChoiceId"`
	MissingPolicyChoiceID   string                         `json:"missingCellPolicyChoiceId"`
	UnlistedPolicyChoiceID  string                         `json:"unlistedCategoryPolicyChoiceId"`
}

type TableShapeSavedPivotCategory struct {
	ChoiceID     string                 `json:"choiceId,omitempty"`
	Value        tableshapecap.Scalar   `json:"value"`
	OutputColumn string                 `json:"outputColumn"`
	OutputLabel  string                 `json:"outputLabel"`
	Support      TableShapeSavedSupport `json:"support"`
}

type TableShapeSavedUnpivot struct {
	InputColumnChoiceIDs []string                      `json:"inputColumnChoiceIds"`
	Inputs               []TableShapeSavedUnpivotInput `json:"inputs"`
	KeyOutputColumn      string                        `json:"keyOutputColumn"`
	KeyOutputLabel       string                        `json:"keyOutputLabel"`
	ValueOutputColumn    string                        `json:"valueOutputColumn"`
	ValueOutputLabel     string                        `json:"valueOutputLabel"`
	NullPolicyChoiceID   string                        `json:"nullPolicyChoiceId"`
}

type TableShapeSavedUnpivotInput struct {
	ColumnChoiceID string               `json:"columnChoiceId"`
	Key            tableshapecap.Scalar `json:"key"`
}

type TableShapeSavedOperand struct {
	Kind         tableshapecap.ResolvedOperandKind `json:"kind"`
	ChoiceID     string                            `json:"choiceId,omitempty"`
	ResolutionID string                            `json:"resolutionId,omitempty"`
	Literal      *tableshapecap.Scalar             `json:"literal,omitempty"`
}

type TableShapeSavedDerived struct {
	LocalID                      string                 `json:"localId"`
	ResolutionID                 string                 `json:"resolutionId"`
	OutputColumn                 string                 `json:"outputColumn"`
	OutputLabel                  string                 `json:"outputLabel"`
	OperatorChoiceID             string                 `json:"operatorChoiceId"`
	Left                         TableShapeSavedOperand `json:"left"`
	Right                        TableShapeSavedOperand `json:"right"`
	Result                       tableshapecap.TypeFact `json:"result"`
	MissingPolicyChoiceID        string                 `json:"missingInputPolicyChoiceId"`
	DivisionByZeroPolicyChoiceID string                 `json:"divisionByZeroPolicyChoiceId,omitempty"`
}

type TableShapeDerivedReference struct {
	ResolutionID string                 `json:"resolutionId"`
	Label        string                 `json:"label"`
	Type         tableshapecap.TypeFact `json:"type"`
}

func (s *Service) savedTableShapeProposal(ctx context.Context, base tableShapeBase, catalog tableshapecap.CatalogReceipt, _ map[string]tableShapeColumnFacts) TableShapeSavedProposalIntent {
	intent := TableShapeSavedProposalIntent{
		Kind: "NONE", ReshapeModeChoiceID: tableShapeModeChoiceID(catalog.ID, "NONE"),
		Support:        TableShapeSavedSupport{State: tableshapecap.AvailabilitySupported},
		DerivedColumns: make([]TableShapeSavedDerived, 0),
	}
	shape := base.document.TableShape
	if shape == nil {
		return intent
	}
	if shape.Reshape != nil {
		switch shape.Reshape.Kind {
		case "PIVOT":
			intent.Kind = "GROUPED_PIVOT"
			intent.ReshapeModeChoiceID = tableShapeModeChoiceID(catalog.ID, "GROUPED_PIVOT")
			intent.Pivot = &TableShapeSavedPivot{}
			if err := s.restoreSavedPivot(ctx, base, catalog, shape.Reshape.Pivot, intent.Pivot); err != nil {
				refuseSavedProposal(&intent, err)
			}
		case "UNPIVOT":
			intent.Kind = "UNPIVOT"
			intent.ReshapeModeChoiceID = tableShapeModeChoiceID(catalog.ID, "UNPIVOT")
			intent.Unpivot = &TableShapeSavedUnpivot{}
			if err := restoreSavedUnpivot(catalog, shape.Reshape.Unpivot, intent.Unpivot); err != nil {
				refuseSavedProposal(&intent, err)
			}
		default:
			refuseSavedProposal(&intent, &Error{Code: "SAVED_RESHAPE_UNSUPPORTED", Message: "The saved reshape kind is not supported by this catalog."})
		}
	}
	priorByOutput := make(map[string]tableshapecap.ResolutionReceipt, len(shape.Derived))
	prior := make([]tableshapecap.ResolutionReceipt, 0, len(shape.Derived))
	pivotResolutionID := ""
	if intent.Pivot != nil && intent.Support.State == tableshapecap.AvailabilitySupported {
		pivotResolutionID = intent.Pivot.PivotResolutionID
	}
	for _, derived := range shape.Derived {
		resolved, err := s.restoreSavedDerived(ctx, base, catalog, derived, pivotResolutionID, priorByOutput, prior)
		if err != nil {
			refuseSavedProposal(&intent, err)
			return intent
		}
		priorByOutput[derived.Output.Column] = resolved
		prior = append(prior, resolved)
		intent.DerivedColumns = append(intent.DerivedColumns, TableShapeSavedDerived{
			LocalID: resolved.ID, ResolutionID: resolved.ID, OutputColumn: derived.Output.Column, OutputLabel: derived.Output.Label,
			OperatorChoiceID: resolved.Derived.OperatorChoiceID,
			Left:             savedOperandFromResolved(resolved.Derived.Left), Right: savedOperandFromResolved(resolved.Derived.Right),
			Result: resolved.Derived.Result, MissingPolicyChoiceID: resolved.Derived.MissingPolicyChoiceID,
			DivisionByZeroPolicyChoiceID: resolved.Derived.DivisionByZeroPolicyChoiceID,
		})
	}
	return intent
}

func refuseSavedProposal(intent *TableShapeSavedProposalIntent, err error) {
	if intent == nil || err == nil {
		return
	}
	code, message := "SAVED_SHAPE_UNAVAILABLE", err.Error()
	if typed, ok := err.(*Error); ok {
		if typed.Code != "" {
			code = typed.Code
		}
		if typed.Message != "" {
			message = typed.Message
		}
	}
	intent.Support = TableShapeSavedSupport{State: tableshapecap.AvailabilityRefused, ReasonCode: code, Message: message}
}

func (s *Service) restoreSavedPivot(ctx context.Context, base tableShapeBase, catalog tableshapecap.CatalogReceipt, saved *authoringv2.PivotConstruction, result *TableShapeSavedPivot) error {
	if saved == nil {
		return &Error{Code: "SAVED_PIVOT_MISSING", Message: "The saved pivot payload is missing."}
	}
	for _, key := range saved.GroupKeys {
		choice, err := findColumnChoiceID(catalog, tableshapecap.RolePivotGroup, key)
		if err != nil {
			return &Error{Code: "SAVED_GROUP_CHOICE_UNAVAILABLE", Message: "A saved pivot group column is unavailable in the current catalog.", Cause: err}
		}
		result.GroupColumnChoiceIDs = append(result.GroupColumnChoiceIDs, choice)
	}
	var err error
	result.CategoryColumnChoiceID, err = findColumnChoiceID(catalog, tableshapecap.RolePivotCategory, saved.CategoryColumn)
	if err != nil {
		return &Error{Code: "SAVED_CATEGORY_COLUMN_UNAVAILABLE", Message: "The saved pivot category column is unavailable in the current catalog.", Cause: err}
	}
	result.ValueColumnChoiceID, err = findColumnChoiceID(catalog, tableshapecap.RolePivotValue, saved.ValueColumn)
	if err != nil {
		return &Error{Code: "SAVED_VALUE_COLUMN_UNAVAILABLE", Message: "The saved pivot value column is unavailable in the current catalog.", Cause: err}
	}
	result.DuplicatePolicyChoiceID, err = findPolicyChoiceID(catalog, tableshapecap.RolePolicyDuplicate, saved.DuplicatePolicy)
	if err != nil {
		return &Error{Code: "SAVED_DUPLICATE_POLICY_UNAVAILABLE", Message: "The saved duplicate-cell policy is unavailable in the current catalog.", Cause: err}
	}
	result.MissingPolicyChoiceID, err = findPolicyChoiceID(catalog, tableshapecap.RolePolicyMissing, saved.MissingCellPolicy)
	if err != nil {
		return &Error{Code: "SAVED_MISSING_POLICY_UNAVAILABLE", Message: "The saved missing-cell policy is unavailable in the current catalog.", Cause: err}
	}
	result.UnlistedPolicyChoiceID, err = findPolicyChoiceID(catalog, tableshapecap.RolePolicyUnlisted, saved.UnlistedCategoryPolicy)
	if err != nil {
		return &Error{Code: "SAVED_UNLISTED_POLICY_UNAVAILABLE", Message: "The saved unlisted-category policy is unavailable in the current catalog.", Cause: err}
	}
	scan, err := s.scanTableShapeCategoryPair(ctx, base, catalog, result.CategoryColumnChoiceID, result.ValueColumnChoiceID)
	if err != nil {
		return err
	}
	stored, err := s.config.TableShapeCapabilities.PutCategoryScan(ctx, scan)
	if err != nil {
		return &Error{Code: "CAPABILITY_STORE_FAILED", Message: "The saved pivot category discovery could not be stored.", Cause: err}
	}
	result.CategoryDiscoveryID = stored.ID
	result.Categories = make([]TableShapeSavedPivotCategory, 0, len(saved.Categories))
	for _, savedCategory := range saved.Categories {
		value, err := tableScalarToCapabilityScalar(savedCategory.Key)
		if err != nil {
			return &Error{Code: "SAVED_CATEGORY_INVALID", Message: "The saved pivot contains an unsupported category value.", Cause: err}
		}
		category := TableShapeSavedPivotCategory{
			Value: value, OutputColumn: savedCategory.Output.Column, OutputLabel: savedCategory.Output.Label,
			Support: TableShapeSavedSupport{State: tableshapecap.AvailabilitySupported},
		}
		matched := false
		for _, discovered := range stored.Categories {
			if sameCapabilityScalar(discovered.Value, value) {
				category.ChoiceID = discovered.ChoiceID
				matched = true
				break
			}
		}
		if !matched {
			category.Support = TableShapeSavedSupport{State: tableshapecap.AvailabilityRefused, ReasonCode: "SAVED_CATEGORY_NOT_IN_CURRENT_SCAN", Message: "The saved category is absent from the current complete category scan."}
			result.Categories = append(result.Categories, category)
			return &Error{Code: "SAVED_CATEGORY_NOT_IN_CURRENT_SCAN", Message: "A saved pivot category is absent from the current complete category scan."}
		}
		result.Categories = append(result.Categories, category)
	}
	selection := TableShapePivotSelection{
		CategoryDiscoveryID: result.CategoryDiscoveryID, GroupColumnChoiceIDs: result.GroupColumnChoiceIDs,
		CategoryColumnChoiceID: result.CategoryColumnChoiceID, ValueColumnChoiceID: result.ValueColumnChoiceID,
		DuplicatePolicyChoiceID: result.DuplicatePolicyChoiceID, MissingPolicyChoiceID: result.MissingPolicyChoiceID,
		UnlistedPolicyChoiceID: result.UnlistedPolicyChoiceID,
	}
	for _, category := range result.Categories {
		selection.Categories = append(selection.Categories, TableShapePivotCategorySelection{ChoiceID: category.ChoiceID, OutputColumn: category.OutputColumn, OutputLabel: category.OutputLabel})
	}
	pivot, scan, err := s.resolveTableShapePivot(ctx, catalog, selection)
	if err != nil {
		return err
	}
	receipt, err := tableshapecap.NewResolutionReceipt(catalog.Binding, catalog.ID, tableshapecap.ResolutionPivot, &pivot, nil, nil, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return &Error{Code: "SAVED_PIVOT_INVALID", Message: "The saved pivot could not be reconstructed as an immutable resolution.", Cause: err}
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt); err != nil {
		return &Error{Code: "SAVED_PIVOT_INVALID", Message: "The saved pivot is not valid for the current catalog.", Cause: err}
	}
	if err := tableshapecap.ValidateResolutionAgainstCategoryScan(catalog, scan, receipt); err != nil {
		return &Error{Code: "SAVED_PIVOT_INVALID", Message: "The saved pivot category choices are not valid for the complete scan.", Cause: err}
	}
	storedResolution, err := s.config.TableShapeCapabilities.PutResolution(ctx, receipt)
	if err != nil {
		return &Error{Code: "CAPABILITY_STORE_FAILED", Message: "The saved pivot resolution could not be stored.", Cause: err}
	}
	result.PivotResolutionID = storedResolution.ID
	for _, operand := range storedResolution.Pivot.DerivedOperands {
		fact := operand.Type
		result.DerivedOperands = append(result.DerivedOperands, TableShapeCatalogChoice{Role: tableshapecap.RoleDerivedOperand, ID: operand.ChoiceID, Label: operand.OutputLabel, Type: &fact})
	}
	return nil
}

func restoreSavedUnpivot(catalog tableshapecap.CatalogReceipt, saved *authoringv2.UnpivotConstruction, result *TableShapeSavedUnpivot) error {
	if saved == nil {
		return &Error{Code: "SAVED_UNPIVOT_MISSING", Message: "The saved unpivot payload is missing."}
	}
	for _, input := range saved.Inputs {
		choice, err := findColumnChoiceID(catalog, tableshapecap.RoleUnpivotInput, input.Column)
		if err != nil {
			return &Error{Code: "SAVED_UNPIVOT_INPUT_UNAVAILABLE", Message: "A saved unpivot input is unavailable in the current catalog.", Cause: err}
		}
		key, err := tableScalarToCapabilityScalar(input.Key)
		if err != nil {
			return &Error{Code: "SAVED_UNPIVOT_KEY_INVALID", Message: "A saved unpivot key is not a supported typed scalar.", Cause: err}
		}
		result.InputColumnChoiceIDs = append(result.InputColumnChoiceIDs, choice)
		result.Inputs = append(result.Inputs, TableShapeSavedUnpivotInput{ColumnChoiceID: choice, Key: key})
	}
	choice, err := findPolicyChoiceID(catalog, tableshapecap.RolePolicyUnpivotNull, saved.NullRowPolicy)
	if err != nil {
		return &Error{Code: "SAVED_UNPIVOT_POLICY_UNAVAILABLE", Message: "The saved unpivot null-row policy is unavailable in the current catalog.", Cause: err}
	}
	result.NullPolicyChoiceID = choice
	result.KeyOutputColumn, result.KeyOutputLabel = saved.KeyOutput.Column, saved.KeyOutput.Label
	result.ValueOutputColumn, result.ValueOutputLabel = saved.ValueOutput.Column, saved.ValueOutput.Label
	return nil
}

func (s *Service) restoreSavedDerived(ctx context.Context, base tableShapeBase, catalog tableshapecap.CatalogReceipt, saved authoringv2.DerivedConstruction, pivotResolutionID string, priorByOutput map[string]tableshapecap.ResolutionReceipt, prior []tableshapecap.ResolutionReceipt) (tableshapecap.ResolutionReceipt, error) {
	operator, err := findOperatorChoiceID(catalog, saved.Operation)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DERIVED_OPERATOR_UNAVAILABLE", Message: "A saved derived operator is unavailable in the current catalog.", Cause: err}
	}
	missing, err := findPolicyChoiceID(catalog, tableshapecap.RolePolicyDerivedMissing, saved.MissingInputPolicy)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DERIVED_POLICY_UNAVAILABLE", Message: "A saved derived missing-input policy is unavailable in the current catalog.", Cause: err}
	}
	selection := TableShapeDerivedSelection{
		OutputColumn: saved.Output.Column, OutputLabel: saved.Output.Label,
		PivotResolutionID: pivotResolutionID,
		OperatorChoiceID:  operator, MissingPolicyChoiceID: missing,
	}
	var pivotReceipt *tableshapecap.ResolutionReceipt
	if pivotResolutionID != "" {
		loaded, loadErr := s.config.TableShapeCapabilities.GetResolution(ctx, catalog.Binding, catalog.ID, pivotResolutionID)
		if loadErr != nil || loaded.Kind != tableshapecap.ResolutionPivot || loaded.Pivot == nil || loaded.Validate() != nil {
			return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_PIVOT_REFERENCE_UNAVAILABLE", Message: "The saved derived chain cannot restore its pivot output context.", Cause: loadErr}
		}
		pivotReceipt = &loaded
	}
	if saved.DivisionByZeroPolicy != "" {
		selection.DivisionByZeroPolicyChoiceID, err = findPolicyChoiceID(catalog, tableshapecap.RolePolicyDivisionByZero, saved.DivisionByZeroPolicy)
		if err != nil {
			return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DIVISION_POLICY_UNAVAILABLE", Message: "A saved division-by-zero policy is unavailable in the current catalog.", Cause: err}
		}
	}
	selection.Left, err = savedOperandSelection(catalog, priorByOutput, pivotReceipt, saved.Left)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	selection.Right, err = savedOperandSelection(catalog, priorByOutput, pivotReceipt, saved.Right)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	resolved, resultType, dependencies, err := s.resolveTableShapeDerived(ctx, catalog, selection)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	compilerType, ok := savedDerivedCompilerType(base, saved)
	if !ok || compilerType != resultType {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DERIVED_SCHEMA_MISMATCH", Message: "The saved derived result does not match the compiler-owned final output schema."}
	}
	resolved.Result = compilerType
	for _, receipt := range prior {
		dependencies = append(dependencies, receipt)
	}
	receipt, err := tableshapecap.NewResolutionReceipt(catalog.Binding, catalog.ID, tableshapecap.ResolutionDerived, nil, nil, &resolved, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DERIVED_INVALID", Message: "The saved derived calculation could not be reconstructed as a typed resolution.", Cause: err}
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt, uniqueResolutionReceipts(dependencies)...); err != nil {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "SAVED_DERIVED_INVALID", Message: "The saved derived calculation is not valid for the current catalog.", Cause: err}
	}
	stored, err := s.config.TableShapeCapabilities.PutResolution(ctx, receipt)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "CAPABILITY_STORE_FAILED", Message: "The saved derived resolution could not be stored.", Cause: err}
	}
	if err := stored.Validate(); err != nil || stored.ID != receipt.ID {
		return tableshapecap.ResolutionReceipt{}, &Error{Code: "CAPABILITY_STORE_FAILED", Message: "The saved derived resolution failed identity validation.", Cause: err}
	}
	return stored, nil
}

func savedDerivedCompilerType(base tableShapeBase, saved authoringv2.DerivedConstruction) (tableshapecap.TypeFact, bool) {
	for _, column := range base.finalContract.Columns {
		if column.Column != saved.Output.Column || column.ConstructionID != string(saved.ConstructionID) {
			continue
		}
		unitIdentity, err := unitIdentityString(column.ResultUnit)
		if err != nil {
			return tableshapecap.TypeFact{}, false
		}
		fact := tableshapecap.TypeFact{LogicalType: tableShapeLogicalType(column.LogicalType), Nullable: column.Nullable, UnitIdentity: unitIdentity}
		return fact, fact.Validate() == nil
	}
	return tableshapecap.TypeFact{}, false
}

func savedOperandSelection(catalog tableshapecap.CatalogReceipt, priorByOutput map[string]tableshapecap.ResolutionReceipt, pivot *tableshapecap.ResolutionReceipt, saved authoringv2.ArithmeticOperand) (TableShapeOperandSelection, error) {
	switch saved.Kind {
	case "COLUMN":
		if prior, ok := priorByOutput[saved.Column]; ok {
			return TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: prior.ID}, nil
		}
		if pivot != nil {
			for _, choice := range pivot.Pivot.DerivedOperands {
				if choice.OutputColumn == saved.Column {
					return TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: choice.ChoiceID}, nil
				}
			}
			return TableShapeOperandSelection{}, &Error{Code: "SAVED_DERIVED_OPERAND_UNAVAILABLE", Message: "A saved derived column is not present in the selected pivot output."}
		}
		choice, err := findOperandChoiceID(catalog, saved.Column)
		if err != nil {
			return TableShapeOperandSelection{}, &Error{Code: "SAVED_DERIVED_OPERAND_UNAVAILABLE", Message: "A saved derived base column is unavailable in the current catalog.", Cause: err}
		}
		return TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: choice}, nil
	case "LITERAL":
		if saved.Literal == nil {
			return TableShapeOperandSelection{}, &Error{Code: "SAVED_DERIVED_LITERAL_MISSING", Message: "A saved derived literal payload is missing."}
		}
		value, err := tableScalarToCapabilityScalar(*saved.Literal)
		if err != nil {
			return TableShapeOperandSelection{}, err
		}
		return TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: &value}, nil
	default:
		return TableShapeOperandSelection{}, &Error{Code: "SAVED_DERIVED_OPERAND_INVALID", Message: "A saved derived operand has an unsupported kind."}
	}
}

func savedOperandFromResolved(value tableshapecap.ResolvedOperand) TableShapeSavedOperand {
	var literal *tableshapecap.Scalar
	if value.Literal != nil {
		copy := *value.Literal
		literal = &copy
	}
	return TableShapeSavedOperand{Kind: value.Kind, ChoiceID: value.ChoiceID, ResolutionID: value.ResolutionID, Literal: literal}
}

func findColumnChoiceID(catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, key string) (string, error) {
	for _, choice := range catalog.Choices.Columns {
		if choice.Role == role && choice.ColumnKey == key {
			return choice.ID, nil
		}
	}
	return "", tableshapecap.ErrNotFound
}

func findPolicyChoiceID(catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, policy string) (string, error) {
	for _, choice := range catalog.Choices.Policies {
		if choice.Role == role && choice.PolicyID == policy {
			return choice.ID, nil
		}
	}
	return "", tableshapecap.ErrNotFound
}

func findOperatorChoiceID(catalog tableshapecap.CatalogReceipt, operator string) (string, error) {
	for _, choice := range catalog.Choices.Operators {
		if choice.Operator == operator {
			return choice.ID, nil
		}
	}
	return "", tableshapecap.ErrNotFound
}

func findOperandChoiceID(catalog tableshapecap.CatalogReceipt, key string) (string, error) {
	for _, choice := range catalog.Choices.Operands {
		if choice.Operand.Kind == tableshapecap.OperandColumn && choice.Operand.ColumnKey == key {
			return choice.ID, nil
		}
	}
	return "", tableshapecap.ErrNotFound
}

func tableScalarToCapabilityScalar(value authoringv2.TableScalar) (tableshapecap.Scalar, error) {
	if err := value.ValidateStructure(); err != nil {
		return tableshapecap.Scalar{}, err
	}
	switch value.Kind {
	case authoringv2.TableScalarString:
		return tableshapecap.StringScalar(*value.String), nil
	case authoringv2.TableScalarInteger:
		return tableshapecap.IntegerScalar(*value.Integer), nil
	case authoringv2.TableScalarDecimal:
		return tableshapecap.DecimalScalar(*value.Decimal), nil
	case authoringv2.TableScalarBoolean:
		return tableshapecap.BooleanScalar(*value.Boolean), nil
	case authoringv2.TableScalarNull:
		return tableshapecap.NullScalar(), nil
	case authoringv2.TableScalarMissing:
		return tableshapecap.MissingScalar(), nil
	default:
		return tableshapecap.Scalar{}, fmt.Errorf("unsupported saved scalar kind %q", value.Kind)
	}
}

func sameCapabilityScalar(left, right tableshapecap.Scalar) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func uniqueResolutionReceipts(receipts []tableshapecap.ResolutionReceipt) []tableshapecap.ResolutionReceipt {
	seen := make(map[string]struct{}, len(receipts))
	out := make([]tableshapecap.ResolutionReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if _, exists := seen[receipt.ID]; exists {
			continue
		}
		seen[receipt.ID] = struct{}{}
		out = append(out, receipt)
	}
	return out
}
