package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

func (s *Service) composeTableShapeProposal(ctx context.Context, request TableShapeProposalRequest, catalog tableshapecap.CatalogReceipt) (*authoringv2.TableShape, error) {
	if request.Mode == TableShapeProposalRemove {
		return nil, nil
	}
	if request.ReshapeResolutionID == "" && len(request.DerivedResolutionIDs) == 0 {
		return nil, fmt.Errorf("ADD and REPLACE require a reshape or derived resolution")
	}

	shape := &authoringv2.TableShape{}
	var reshapeReceipt *tableshapecap.ResolutionReceipt
	priorReceipts := make([]tableshapecap.ResolutionReceipt, 0, len(request.DerivedResolutionIDs)+1)
	if request.ReshapeResolutionID != "" {
		receipt, err := s.loadProposalResolution(ctx, catalog, request.ReshapeResolutionID)
		if err != nil {
			return nil, err
		}
		switch receipt.Kind {
		case tableshapecap.ResolutionPivot:
			if err := s.validateProposalPivot(ctx, catalog, receipt); err != nil {
				return nil, err
			}
			shape.Reshape, err = tableShapeReshapeFromPivot(catalog, receipt)
			if err != nil {
				return nil, err
			}
		case tableshapecap.ResolutionUnpivot:
			if len(request.DerivedResolutionIDs) != 0 {
				return nil, fmt.Errorf("UNPIVOT cannot be combined with derived resolutions")
			}
			if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt); err != nil {
				return nil, fmt.Errorf("unpivot resolution does not match the selected catalog: %w", err)
			}
			shape.Reshape, err = tableShapeReshapeFromUnpivot(catalog, receipt)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("reshape resolution has kind %q", receipt.Kind)
		}
		copy := receipt
		reshapeReceipt = &copy
		priorReceipts = append(priorReceipts, receipt)
	}

	priorOutputs := make(map[string]string, len(request.DerivedResolutionIDs))
	for _, id := range request.DerivedResolutionIDs {
		receipt, err := s.loadProposalResolution(ctx, catalog, id)
		if err != nil {
			return nil, err
		}
		if receipt.Kind != tableshapecap.ResolutionDerived || receipt.Derived == nil {
			return nil, fmt.Errorf("resolution %q is not a derived resolution", id)
		}
		if receipt.Derived.PivotResolutionID != "" &&
			(reshapeReceipt == nil || reshapeReceipt.ID != receipt.Derived.PivotResolutionID || reshapeReceipt.Kind != tableshapecap.ResolutionPivot) {
			return nil, fmt.Errorf("derived resolution %q references an unselected pivot", id)
		}
		if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt, priorReceipts...); err != nil {
			return nil, fmt.Errorf("derived resolution %q is invalid for its earlier dependencies: %w", id, err)
		}
		derived, err := tableShapeDerivedFromResolution(catalog, receipt, reshapeReceipt, priorOutputs)
		if err != nil {
			return nil, err
		}
		shape.Derived = append(shape.Derived, derived)
		priorOutputs[receipt.ID] = derived.Output.Column
		priorReceipts = append(priorReceipts, receipt)
	}
	if shape.Reshape == nil && len(shape.Derived) == 0 {
		return nil, fmt.Errorf("a table shape must contain a reshape or derived resolution")
	}
	return shape, nil
}

func (s *Service) loadProposalResolution(ctx context.Context, catalog tableshapecap.CatalogReceipt, id string) (tableshapecap.ResolutionReceipt, error) {
	receipt, err := s.config.TableShapeCapabilities.GetResolution(ctx, catalog.Binding, catalog.ID, id)
	if err != nil {
		if errors.Is(err, tableshapecap.ErrNotFound) {
			return tableshapecap.ResolutionReceipt{}, conflict("table-shape-proposal", "STALE_TABLE_SHAPE_RESOLUTION", "a selected table-shape resolution is missing or belongs to another catalog binding", nil, err)
		}
		return tableshapecap.ResolutionReceipt{}, unavailable("table-shape-proposal", "CAPABILITY_STORE_FAILED", "a selected table-shape resolution could not be loaded", err)
	}
	if err := receipt.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, conflict("table-shape-proposal", "STALE_TABLE_SHAPE_RESOLUTION", "a selected table-shape resolution failed receipt validation", nil, err)
	}
	if receipt.ID != id || receipt.Binding != catalog.Binding || receipt.ParentCatalogID != catalog.ID {
		return tableshapecap.ResolutionReceipt{}, conflict("table-shape-proposal", "STALE_TABLE_SHAPE_RESOLUTION", "a selected table-shape resolution belongs to another catalog binding", nil, nil)
	}
	return receipt, nil
}

func (s *Service) validateProposalPivot(ctx context.Context, catalog tableshapecap.CatalogReceipt, receipt tableshapecap.ResolutionReceipt) error {
	if receipt.Pivot == nil {
		return fmt.Errorf("pivot resolution %q has no pivot payload", receipt.ID)
	}
	scan, err := s.config.TableShapeCapabilities.GetCategoryScan(ctx, catalog.Binding, catalog.ID, receipt.Pivot.CategoryDiscoveryID)
	if err != nil {
		if errors.Is(err, tableshapecap.ErrNotFound) {
			return conflict("table-shape-proposal", "STALE_TABLE_SHAPE_CATEGORY_SCAN", "the pivot category scan is missing or belongs to another catalog binding", nil, err)
		}
		return unavailable("table-shape-proposal", "CAPABILITY_STORE_FAILED", "the pivot category scan could not be loaded", err)
	}
	if scan.ID != receipt.Pivot.CategoryDiscoveryID || scan.Binding != catalog.Binding || scan.ParentCatalogID != catalog.ID ||
		!scan.Proof.Complete || scan.Proof.Overflow {
		return conflict("table-shape-proposal", "STALE_TABLE_SHAPE_CATEGORY_SCAN", "the pivot resolution has no complete current category scan", nil, nil)
	}
	if err := scan.ValidateAgainstCatalog(catalog); err != nil {
		return conflict("table-shape-proposal", "STALE_TABLE_SHAPE_CATEGORY_SCAN", "the pivot category scan does not match the selected catalog", nil, err)
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(catalog, receipt); err != nil {
		return conflict("table-shape-proposal", "STALE_TABLE_SHAPE_RESOLUTION", "the pivot resolution does not match the selected catalog", nil, err)
	}
	if err := tableshapecap.ValidateResolutionAgainstCategoryScan(catalog, scan, receipt); err != nil {
		return conflict("table-shape-proposal", "STALE_TABLE_SHAPE_RESOLUTION", "the pivot resolution does not match its complete category scan", nil, err)
	}
	return nil
}

func tableShapeReshapeFromPivot(catalog tableshapecap.CatalogReceipt, receipt tableshapecap.ResolutionReceipt) (*authoringv2.TableReshape, error) {
	pivot := receipt.Pivot
	groupKeys := make([]string, 0, len(pivot.GroupColumnChoiceIDs))
	for _, id := range pivot.GroupColumnChoiceIDs {
		key, err := tableShapeCatalogColumn(catalog, tableshapecap.RolePivotGroup, id)
		if err != nil {
			return nil, err
		}
		groupKeys = append(groupKeys, key)
	}
	categoryColumn, err := tableShapeCatalogColumn(catalog, tableshapecap.RolePivotCategory, pivot.CategoryColumnChoiceID)
	if err != nil {
		return nil, err
	}
	valueColumn, err := tableShapeCatalogColumn(catalog, tableshapecap.RolePivotValue, pivot.ValueColumnChoiceID)
	if err != nil {
		return nil, err
	}
	duplicatePolicy, err := tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyDuplicate, pivot.DuplicatePolicyChoiceID)
	if err != nil {
		return nil, err
	}
	missingPolicy, err := tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyMissing, pivot.MissingPolicyChoiceID)
	if err != nil {
		return nil, err
	}
	unlistedPolicy, err := tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyUnlisted, pivot.UnlistedPolicyChoiceID)
	if err != nil {
		return nil, err
	}
	categories := make([]authoringv2.PivotCategory, 0, len(pivot.Categories))
	outputIdentities := make([]string, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		key, err := tableScalarFromCapability(category.Value, false)
		if err != nil {
			return nil, fmt.Errorf("pivot category %q has an invalid typed key: %w", category.ChoiceID, err)
		}
		categories = append(categories, authoringv2.PivotCategory{
			Key: key, Output: authoringv2.ColumnOutput{Column: category.OutputColumn, Label: category.OutputLabel},
		})
		outputIdentities = append(outputIdentities, category.OutputColumn)
	}
	return &authoringv2.TableReshape{
		Kind: "PIVOT",
		Pivot: &authoringv2.PivotConstruction{
			ConstructionID: tableShapeConstructionID(receipt.ID, outputIdentities...),
			GroupKeys:      groupKeys, CategoryColumn: categoryColumn, ValueColumn: valueColumn,
			Categories: categories, DuplicatePolicy: duplicatePolicy, MissingCellPolicy: missingPolicy,
			UnlistedCategoryPolicy: unlistedPolicy,
		},
	}, nil
}

func tableShapeReshapeFromUnpivot(catalog tableshapecap.CatalogReceipt, receipt tableshapecap.ResolutionReceipt) (*authoringv2.TableReshape, error) {
	unpivot := receipt.Unpivot
	if unpivot == nil {
		return nil, fmt.Errorf("unpivot resolution %q has no unpivot payload", receipt.ID)
	}
	inputs := make([]authoringv2.UnpivotInput, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		column, err := tableShapeCatalogColumn(catalog, tableshapecap.RoleUnpivotInput, input.ChoiceID)
		if err != nil {
			return nil, err
		}
		key, err := tableScalarFromCapability(input.Key, true)
		if err != nil {
			return nil, fmt.Errorf("unpivot key for %q is invalid: %w", column, err)
		}
		inputs = append(inputs, authoringv2.UnpivotInput{Column: column, Key: key})
	}
	nullPolicy, err := tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyUnpivotNull, unpivot.NullPolicyChoiceID)
	if err != nil {
		return nil, err
	}
	return &authoringv2.TableReshape{
		Kind: "UNPIVOT",
		Unpivot: &authoringv2.UnpivotConstruction{
			ConstructionID: tableShapeConstructionID(receipt.ID, unpivot.KeyOutput.Name, unpivot.ValueOutput.Name),
			Inputs:         inputs,
			KeyOutput:      authoringv2.ColumnOutput{Column: unpivot.KeyOutput.Name, Label: unpivot.KeyOutput.Label},
			ValueOutput:    authoringv2.ColumnOutput{Column: unpivot.ValueOutput.Name, Label: unpivot.ValueOutput.Label},
			NullRowPolicy:  nullPolicy,
		},
	}, nil
}

func tableShapeDerivedFromResolution(catalog tableshapecap.CatalogReceipt, receipt tableshapecap.ResolutionReceipt, pivot *tableshapecap.ResolutionReceipt, priorOutputs map[string]string) (authoringv2.DerivedConstruction, error) {
	resolved := receipt.Derived
	if resolved == nil {
		return authoringv2.DerivedConstruction{}, fmt.Errorf("derived resolution %q has no derived payload", receipt.ID)
	}
	operator, err := catalog.FindOperator(resolved.OperatorChoiceID)
	if err != nil {
		return authoringv2.DerivedConstruction{}, fmt.Errorf("derived resolution %q has an invalid operator choice: %w", receipt.ID, err)
	}
	missingPolicy, err := tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyDerivedMissing, resolved.MissingPolicyChoiceID)
	if err != nil {
		return authoringv2.DerivedConstruction{}, err
	}
	divisionPolicy := ""
	if resolved.DivisionByZeroPolicyChoiceID != "" {
		divisionPolicy, err = tableShapeCatalogPolicy(catalog, tableshapecap.RolePolicyDivisionByZero, resolved.DivisionByZeroPolicyChoiceID)
		if err != nil {
			return authoringv2.DerivedConstruction{}, err
		}
	}
	left, err := tableShapeOperandFromResolution(catalog, resolved.Left, pivot, priorOutputs)
	if err != nil {
		return authoringv2.DerivedConstruction{}, fmt.Errorf("derived resolution %q left operand: %w", receipt.ID, err)
	}
	right, err := tableShapeOperandFromResolution(catalog, resolved.Right, pivot, priorOutputs)
	if err != nil {
		return authoringv2.DerivedConstruction{}, fmt.Errorf("derived resolution %q right operand: %w", receipt.ID, err)
	}
	return authoringv2.DerivedConstruction{
		ConstructionID: tableShapeConstructionID(receipt.ID, resolved.Output.Name),
		Output:         authoringv2.ColumnOutput{Column: resolved.Output.Name, Label: resolved.Output.Label},
		Operation:      operator.Operator, Left: left, Right: right,
		MissingInputPolicy: missingPolicy, DivisionByZeroPolicy: divisionPolicy,
	}, nil
}

func tableShapeOperandFromResolution(catalog tableshapecap.CatalogReceipt, operand tableshapecap.ResolvedOperand, pivot *tableshapecap.ResolutionReceipt, priorOutputs map[string]string) (authoringv2.ArithmeticOperand, error) {
	switch operand.Kind {
	case tableshapecap.ResolvedOperandCatalogChoice:
		if pivot != nil && pivot.Pivot != nil {
			for _, output := range pivot.Pivot.DerivedOperands {
				if output.ChoiceID == operand.ChoiceID {
					return authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: output.OutputColumn}, nil
				}
			}
		}
		choice, err := catalog.FindOperand(operand.ChoiceID)
		if err != nil {
			return authoringv2.ArithmeticOperand{}, fmt.Errorf("base operand choice is unavailable: %w", err)
		}
		if err := choice.Operand.Validate(); err != nil {
			return authoringv2.ArithmeticOperand{}, fmt.Errorf("base operand choice is invalid: %w", err)
		}
		return authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: choice.Operand.ColumnKey}, nil
	case tableshapecap.ResolvedOperandResolution:
		if operand.OutputIndex == nil || *operand.OutputIndex != 0 {
			return authoringv2.ArithmeticOperand{}, fmt.Errorf("derived output reference must select its single bound output")
		}
		column, ok := priorOutputs[operand.ResolutionID]
		if !ok {
			return authoringv2.ArithmeticOperand{}, fmt.Errorf("derived reference %q must appear earlier in the proposal", operand.ResolutionID)
		}
		return authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: column}, nil
	case tableshapecap.ResolvedOperandLiteral:
		if operand.Literal == nil {
			return authoringv2.ArithmeticOperand{}, fmt.Errorf("literal operand is missing its value")
		}
		literal, err := tableScalarFromCapability(*operand.Literal, true)
		if err != nil {
			return authoringv2.ArithmeticOperand{}, err
		}
		return authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &literal}, nil
	default:
		return authoringv2.ArithmeticOperand{}, fmt.Errorf("unsupported resolved operand kind %q", operand.Kind)
	}
}

func tableShapeCatalogColumn(catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, id string) (string, error) {
	choice, err := catalog.FindColumn(role, id)
	if err != nil {
		return "", fmt.Errorf("column choice %q for role %q is unavailable: %w", id, role, err)
	}
	if findPublicColumn(catalog, choice.ColumnKey) == nil {
		return "", fmt.Errorf("column choice %q is not bound to a public catalog column", id)
	}
	return choice.ColumnKey, nil
}

func tableShapeCatalogPolicy(catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, id string) (string, error) {
	choice, err := catalog.FindPolicy(role, id)
	if err != nil {
		return "", fmt.Errorf("policy choice %q for role %q is unavailable: %w", id, role, err)
	}
	return choice.PolicyID, nil
}

func tableScalarFromCapability(value tableshapecap.Scalar, requireConcrete bool) (authoringv2.TableScalar, error) {
	if err := value.Validate(); err != nil {
		return authoringv2.TableScalar{}, err
	}
	if requireConcrete && (value.Kind == tableshapecap.ScalarNull || value.Kind == tableshapecap.ScalarMissing) {
		return authoringv2.TableScalar{}, fmt.Errorf("%s is not a concrete table scalar", value.Kind)
	}
	result := authoringv2.TableScalar{Kind: authoringv2.TableScalarKind(value.Kind)}
	if value.String != nil {
		copy := *value.String
		result.String = &copy
	}
	if value.Integer != nil {
		copy := *value.Integer
		result.Integer = &copy
	}
	if value.Decimal != nil {
		copy := *value.Decimal
		result.Decimal = &copy
	}
	if value.Boolean != nil {
		copy := *value.Boolean
		result.Boolean = &copy
	}
	if err := result.ValidateStructure(); err != nil {
		return authoringv2.TableScalar{}, err
	}
	if requireConcrete {
		if err := result.ValidateConcreteValue(); err != nil {
			return authoringv2.TableScalar{}, err
		}
	}
	return result, nil
}

func tableShapeConstructionID(resolutionID string, outputIdentities ...string) authoringv2.ConstructionID {
	identity := binary.AppendUvarint(nil, uint64(len(resolutionID)))
	identity = append(identity, resolutionID...)
	identity = binary.AppendUvarint(identity, uint64(len(outputIdentities)))
	for _, output := range outputIdentities {
		identity = binary.AppendUvarint(identity, uint64(len(output)))
		identity = append(identity, output...)
	}
	digest := sha256.Sum256(identity)
	return authoringv2.ConstructionID("shape-" + hex.EncodeToString(digest[:16]))
}
