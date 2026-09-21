package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

func (h *explorerHTTPHandlers) getTableShapeCapabilitiesDirect(ctx context.Context, project, explorerID string, body *loomapi.TableShapeCapabilitiesRequest) (loomapi.TableShapeCapabilitiesResponse, error) {
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return loomapi.TableShapeCapabilitiesResponse{}, err
	}
	if body == nil {
		return loomapi.TableShapeCapabilitiesResponse{}, malformedRouteError("table-shape-capabilities", nil)
	}
	value, err := h.application.GetTableShapeCatalog(ctx, lifecycle.TableShapeCatalogRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId,
	})
	if err != nil {
		return loomapi.TableShapeCapabilitiesResponse{}, err
	}
	return tableShapeCapabilitiesResponse(value), nil
}

func (h *explorerHTTPHandlers) discoverTableShapeCategoriesDirect(ctx context.Context, project, explorerID string, body *loomapi.TableShapeCategoryDiscoveryRequest) (loomapi.TableShapeCategoryDiscoveryResponse, error) {
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return loomapi.TableShapeCategoryDiscoveryResponse{}, err
	}
	if body == nil {
		return loomapi.TableShapeCategoryDiscoveryResponse{}, malformedRouteError("table-shape-category-discovery", nil)
	}
	value, err := h.application.DiscoverTableShapeCategories(ctx, lifecycle.TableShapeCategoryDiscoveryRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, CatalogID: body.CatalogId,
		CategoryColumnChoiceID: body.CategoryColumnChoiceId, ValueColumnChoiceID: body.ValueColumnChoiceId,
	})
	if err != nil {
		return loomapi.TableShapeCategoryDiscoveryResponse{}, err
	}
	result := loomapi.TableShapeCategoryDiscoveryResponse{
		CatalogId: value.CatalogID, DiscoveryIdentity: value.DiscoveryID,
		Kind: loomapi.TableShapeCategoryDiscoveryResponseKind("complete"),
		Pair: loomapi.TableShapePivotCategoryPair{
			CategoryColumn: loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: value.CategoryColumnChoiceID},
			ValueColumn:    loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: value.ValueColumnChoiceID},
		},
		Categories: make([]loomapi.TableShapePivotCategoryChoice, 0, len(value.Categories)),
	}
	for _, category := range value.Categories {
		result.Categories = append(result.Categories, loomapi.TableShapePivotCategoryChoice{
			ChoiceId: category.ID, ChoiceKind: "pivotCategory", Label: category.Label,
			Availability:    choiceAvailability(true, ""),
			SuggestedOutput: loomapi.TableShapeOutputName{Column: category.SuggestedOutputColumn, Label: category.SuggestedOutputLabel},
			Value:           capabilityScalar(category.Value),
		})
	}
	return result, nil
}

func (h *explorerHTTPHandlers) resolveTableShapeDirect(ctx context.Context, project, explorerID string, body *loomapi.TableShapeResolutionRequest) (loomapi.TableShapeResolutionResponse, error) {
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return loomapi.TableShapeResolutionResponse{}, err
	}
	if body == nil {
		return loomapi.TableShapeResolutionResponse{}, malformedRouteError("table-shape-resolution", nil)
	}
	request := lifecycle.TableShapeResolutionRequest{
		Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
		ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
		OutputID: body.OutputId, CatalogID: body.CatalogId, Kind: tableshapecap.ResolutionKind(body.Kind),
	}
	if body.Pivot != nil {
		selection := body.Pivot
		request.Pivot = &lifecycle.TableShapePivotSelection{
			CategoryDiscoveryID:     selection.CategoryDiscoveryId,
			GroupColumnChoiceIDs:    append([]string(nil), selection.GroupColumnChoiceIds...),
			CategoryColumnChoiceID:  selection.CategoryColumnChoiceId,
			ValueColumnChoiceID:     selection.ValueColumnChoiceId,
			DuplicatePolicyChoiceID: selection.DuplicatePolicyChoiceId,
			MissingPolicyChoiceID:   selection.MissingPolicyChoiceId,
			UnlistedPolicyChoiceID:  selection.UnlistedPolicyChoiceId,
		}
		for _, category := range selection.Categories {
			request.Pivot.Categories = append(request.Pivot.Categories, lifecycle.TableShapePivotCategorySelection{
				ChoiceID: category.ChoiceId, OutputColumn: category.OutputColumn, OutputLabel: category.OutputLabel,
			})
		}
	}
	if body.Unpivot != nil {
		selection := body.Unpivot
		request.Unpivot = &lifecycle.TableShapeUnpivotSelection{
			InputColumnChoiceIDs: append([]string(nil), selection.InputColumnChoiceIds...),
			NullPolicyChoiceID:   selection.NullPolicyChoiceId,
			KeyOutputColumn:      selection.KeyOutputColumn, KeyOutputLabel: selection.KeyOutputLabel,
			ValueOutputColumn: selection.ValueOutputColumn, ValueOutputLabel: selection.ValueOutputLabel,
		}
	}
	if body.Derived != nil {
		selection := body.Derived
		derived := &lifecycle.TableShapeDerivedSelection{
			OutputColumn: selection.OutputColumn, OutputLabel: selection.OutputLabel,
			OperatorChoiceID: selection.OperatorChoiceId, MissingPolicyChoiceID: selection.MissingPolicyChoiceId,
			Left: lifecycleOperandSelection(selection.Left), Right: lifecycleOperandSelection(selection.Right),
		}
		if selection.PivotResolutionId != nil {
			derived.PivotResolutionID = *selection.PivotResolutionId
		}
		if selection.DivisionByZeroPolicyChoiceId != nil {
			derived.DivisionByZeroPolicyChoiceID = *selection.DivisionByZeroPolicyChoiceId
		}
		request.Derived = derived
	}
	value, err := h.application.ResolveTableShape(ctx, request)
	if err != nil {
		return loomapi.TableShapeResolutionResponse{}, err
	}
	return tableShapeResolutionResponse(value, body), nil
}

func lifecycleOperandSelection(value loomapi.TableShapeOperandSelection) lifecycle.TableShapeOperandSelection {
	result := lifecycle.TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandKind(value.Kind)}
	if value.ChoiceId != nil {
		result.ChoiceID = *value.ChoiceId
	}
	if value.ResolutionId != nil {
		result.ResolutionID = *value.ResolutionId
	}
	if value.Literal != nil {
		literal := capabilityScalarFromAPI(*value.Literal)
		result.Literal = &literal
	}
	return result
}

func (r *HTTPRoutes) GetExplorerTableShapeCapabilities(ctx context.Context, request loomapi.GetExplorerTableShapeCapabilitiesRequestObject) (loomapi.GetExplorerTableShapeCapabilitiesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getExplorerTableShapeCapabilities", explorerUnavailable("table-shape-capabilities", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.GetExplorerTableShapeCapabilities503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.getTableShapeCapabilitiesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerTableShapeCapabilities200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerTableShapeCapabilities", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.GetExplorerTableShapeCapabilities400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.GetExplorerTableShapeCapabilities401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.GetExplorerTableShapeCapabilities403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.GetExplorerTableShapeCapabilities404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.GetExplorerTableShapeCapabilities409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.GetExplorerTableShapeCapabilities422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetExplorerTableShapeCapabilities500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetExplorerTableShapeCapabilities503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerTableShapeCapabilities", status)
	}
}

func (r *HTTPRoutes) DiscoverExplorerTableShapeCategories(ctx context.Context, request loomapi.DiscoverExplorerTableShapeCategoriesRequestObject) (loomapi.DiscoverExplorerTableShapeCategoriesResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerTableShapeCategories", explorerUnavailable("table-shape-category-discovery", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.DiscoverExplorerTableShapeCategories503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.discoverTableShapeCategoriesDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DiscoverExplorerTableShapeCategories200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerTableShapeCategories", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.DiscoverExplorerTableShapeCategories400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.DiscoverExplorerTableShapeCategories401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.DiscoverExplorerTableShapeCategories403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.DiscoverExplorerTableShapeCategories404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.DiscoverExplorerTableShapeCategories409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.DiscoverExplorerTableShapeCategories422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.DiscoverExplorerTableShapeCategories500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.DiscoverExplorerTableShapeCategories503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("discoverExplorerTableShapeCategories", status)
	}
}

func (r *HTTPRoutes) ResolveExplorerTableShape(ctx context.Context, request loomapi.ResolveExplorerTableShapeRequestObject) (loomapi.ResolveExplorerTableShapeResponseObject, error) {
	if r == nil || r.explorer == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "resolveExplorerTableShape", explorerUnavailable("table-shape-resolution", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ResolveExplorerTableShape503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := r.explorer.resolveTableShapeDirect(ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ResolveExplorerTableShape200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "resolveExplorerTableShape", err)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ResolveExplorerTableShape400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case http.StatusUnauthorized:
		return loomapi.ResolveExplorerTableShape401JSONResponse{ServiceUnauthorizedJSONResponse: authoringUnauthorizedResponse(failure)}, nil
	case http.StatusForbidden:
		return loomapi.ResolveExplorerTableShape403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case http.StatusNotFound:
		return loomapi.ResolveExplorerTableShape404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case http.StatusConflict:
		return loomapi.ResolveExplorerTableShape409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ResolveExplorerTableShape422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case http.StatusInternalServerError:
		return loomapi.ResolveExplorerTableShape500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ResolveExplorerTableShape503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("resolveExplorerTableShape", status)
	}
}

func tableShapeCapabilitiesResponse(value lifecycle.TableShapeCatalogResult) loomapi.TableShapeCapabilitiesResponse {
	result := loomapi.TableShapeCapabilitiesResponse{
		CatalogId: value.CatalogID, OutputId: value.OutputID,
		GroupColumns:              make([]loomapi.TableShapeEditorChoice, 0),
		CategoryColumns:           make([]loomapi.TableShapeEditorChoice, 0),
		ValueColumns:              make([]loomapi.TableShapeEditorChoice, 0),
		UnpivotColumns:            make([]loomapi.TableShapeEditorChoice, 0),
		DuplicatePolicies:         make([]loomapi.TableShapeEditorChoice, 0),
		MissingCellPolicies:       make([]loomapi.TableShapeEditorChoice, 0),
		UnlistedCategoryPolicies:  make([]loomapi.TableShapeEditorChoice, 0),
		UnpivotNullRowPolicies:    make([]loomapi.TableShapeEditorChoice, 0),
		BinaryOperators:           make([]loomapi.TableShapeBinaryOperatorChoice, 0),
		Operands:                  make([]loomapi.TableShapeEditorChoice, 0),
		MissingInputPolicies:      make([]loomapi.TableShapeEditorChoice, 0),
		DivisionByZeroPolicies:    make([]loomapi.TableShapeEditorChoice, 0),
		ReshapeModes:              make([]loomapi.TableShapeReshapeModeChoice, 0),
		PivotCategoryDiscovery:    loomapi.TableShapePivotCategoryDiscovery{Kind: "not-requested"},
		DerivedOutputSuggestions:  make([]loomapi.TableShapeOutputSuggestion, 0),
		SavedProposalAvailability: savedAvailability(value.SavedProposal.Support),
	}
	for _, column := range value.Columns {
		for _, choice := range column.Choices {
			mapped := editorChoice(choice.ID, "column", column.Label, true, "")
			switch choice.Role {
			case tableshapecap.RolePivotGroup:
				result.GroupColumns = append(result.GroupColumns, mapped)
			case tableshapecap.RolePivotCategory:
				result.CategoryColumns = append(result.CategoryColumns, mapped)
			case tableshapecap.RolePivotValue:
				result.ValueColumns = append(result.ValueColumns, mapped)
			case tableshapecap.RoleUnpivotInput:
				result.UnpivotColumns = append(result.UnpivotColumns, mapped)
			}
		}
	}
	for _, choice := range value.Operands {
		result.Operands = append(result.Operands, editorChoice(choice.ID, "operand", choice.Label, true, ""))
	}
	for _, choice := range value.Operators {
		result.BinaryOperators = append(result.BinaryOperators, loomapi.TableShapeBinaryOperatorChoice{
			ChoiceId: choice.ID, ChoiceKind: "binaryOperator", Label: choice.Label,
			Availability: choiceAvailability(true, ""), RequiresDivisionByZeroPolicy: choice.RequiresDivisionByZeroPolicy,
		})
	}
	for _, choice := range value.Policies {
		mapped := editorChoice(choice.ID, policyEditorKind(choice.Role), choice.Label, true, "")
		switch choice.Role {
		case tableshapecap.RolePolicyDuplicate:
			result.DuplicatePolicies = append(result.DuplicatePolicies, mapped)
		case tableshapecap.RolePolicyMissing:
			result.MissingCellPolicies = append(result.MissingCellPolicies, mapped)
		case tableshapecap.RolePolicyUnlisted:
			result.UnlistedCategoryPolicies = append(result.UnlistedCategoryPolicies, mapped)
		case tableshapecap.RolePolicyUnpivotNull:
			result.UnpivotNullRowPolicies = append(result.UnpivotNullRowPolicies, mapped)
		case tableshapecap.RolePolicyDerivedMissing:
			result.MissingInputPolicies = append(result.MissingInputPolicies, mapped)
		case tableshapecap.RolePolicyDivisionByZero:
			result.DivisionByZeroPolicies = append(result.DivisionByZeroPolicies, mapped)
		}
	}
	for _, mode := range value.ReshapeModes {
		availability := choiceAvailability(mode.State == tableshapecap.AvailabilitySupported, mode.Reason)
		result.ReshapeModes = append(result.ReshapeModes, loomapi.TableShapeReshapeModeChoice{
			ChoiceId: mode.ID, ChoiceKind: "reshapeMode", Label: mode.Label, Mode: loomapi.TableShapeReshapeModeChoiceMode(mode.Mode), Availability: availability,
		})
	}
	result.DerivedAvailability = roleChoiceAvailability(value.Availability, tableshapecap.RoleDerivedOperand)
	result.UnpivotWithDerivedAvailability = loomapi.TableShapeChoiceAvailability{Kind: "unsupported", Reason: stringPointer("Derived columns are not supported after unpivot in this proposal.")}
	result.UnpivotKeyOutput, result.UnpivotValueOutput = unpivotOutputSupports(value)
	if result.DerivedAvailability.Kind == "supported" {
		result.DerivedOutputSuggestions = []loomapi.TableShapeOutputSuggestion{outputSuggestion(value.CatalogID, "derivedOutput", "Calculated value", "calculated_value", "Calculated value", "Determined from selected operands")}
	}
	result.SavedProposalIntent = savedProposalIntent(value.SavedProposal)
	return result
}

func unpivotOutputSupports(value lifecycle.TableShapeCatalogResult) (loomapi.TableShapeOutputDescriptorSupport, loomapi.TableShapeOutputDescriptorSupport) {
	availability := roleChoiceAvailability(value.Availability, tableshapecap.RoleUnpivotInput)
	if availability.Kind == "unsupported" {
		return loomapi.TableShapeOutputDescriptorSupport{Kind: "unsupported", Reason: availability.Reason}, loomapi.TableShapeOutputDescriptorSupport{Kind: "unsupported", Reason: availability.Reason}
	}
	keySuggestion := outputSuggestion(value.CatalogID, "unpivotKeyOutput", "Source column", "source_column", "Source column", "Text")
	valueSuggestion := outputSuggestion(value.CatalogID, "unpivotValueOutput", "Selected value", "value", "Value", "Compatible source value")
	keySuggestions := []loomapi.TableShapeOutputSuggestion{keySuggestion}
	valueSuggestions := []loomapi.TableShapeOutputSuggestion{valueSuggestion}
	keyType, valueType := "Text", "Compatible source value"
	return loomapi.TableShapeOutputDescriptorSupport{Kind: "supported", ResultTypeLabel: &keyType, Suggestions: &keySuggestions}, loomapi.TableShapeOutputDescriptorSupport{Kind: "supported", ResultTypeLabel: &valueType, Suggestions: &valueSuggestions}
}

func outputSuggestion(catalogID, choiceKind, label, column, outputLabel, resultType string) loomapi.TableShapeOutputSuggestion {
	sum := sha256.Sum256([]byte("loom-table-shape-suggestion\x00" + catalogID + "\x00" + choiceKind))
	return loomapi.TableShapeOutputSuggestion{
		ChoiceId: "tss_" + hex.EncodeToString(sum[:16]), ChoiceKind: loomapi.TableShapeOutputSuggestionChoiceKind(choiceKind), Label: label,
		Availability: choiceAvailability(true, ""), SuggestedOutput: loomapi.TableShapeOutputName{Column: column, Label: outputLabel}, ResultTypeLabel: resultType,
	}
}

func savedProposalIntent(value lifecycle.TableShapeSavedProposalIntent) loomapi.TableShapeProposalIntent {
	intent := loomapi.TableShapeProposalIntent{
		Kind:           loomapi.TableShapeProposalIntentKind(value.Kind),
		ReshapeMode:    loomapi.TableShapeReshapeModeReference{Kind: "reshapeMode", ChoiceId: value.ReshapeModeChoiceID},
		DerivedColumns: make([]loomapi.TableShapeDerivedColumnProposal, 0, len(value.DerivedColumns)),
	}
	if value.Pivot != nil {
		pivot := value.Pivot
		result := &loomapi.TableShapePivotProposal{
			CategoryColumn:            loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: pivot.CategoryColumnChoiceID},
			ValueColumn:               loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: pivot.ValueColumnChoiceID},
			CategoryDiscoveryIdentity: pivot.CategoryDiscoveryID,
			DuplicatePolicy:           loomapi.TableShapeDuplicatePolicyReference{Kind: "duplicatePolicy", ChoiceId: pivot.DuplicatePolicyChoiceID},
			MissingCellPolicy:         loomapi.TableShapeMissingCellPolicyReference{Kind: "missingCellPolicy", ChoiceId: pivot.MissingPolicyChoiceID},
			UnlistedCategoryPolicy:    loomapi.TableShapeUnlistedCategoryPolicyReference{Kind: "unlistedCategoryPolicy", ChoiceId: pivot.UnlistedPolicyChoiceID},
			GroupColumns:              make([]loomapi.TableShapeColumnReference, 0, len(pivot.GroupColumnChoiceIDs)),
			IncludedCategories:        make([]loomapi.TableShapeSavedPivotCategorySelection, 0, len(pivot.Categories)),
		}
		for _, id := range pivot.GroupColumnChoiceIDs {
			result.GroupColumns = append(result.GroupColumns, loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: id})
		}
		for _, category := range pivot.Categories {
			if category.ChoiceID == "" {
				continue
			}
			result.IncludedCategories = append(result.IncludedCategories, loomapi.TableShapeSavedPivotCategorySelection{
				Category: loomapi.TableShapePivotCategoryReference{Kind: "pivotCategory", ChoiceId: category.ChoiceID},
				Output:   loomapi.TableShapeOutputName{Column: category.OutputColumn, Label: category.OutputLabel},
			})
		}
		intent.Pivot = result
	}
	if value.Unpivot != nil {
		unpivot := value.Unpivot
		result := &loomapi.TableShapeUnpivotProposal{
			KeyOutput:     loomapi.TableShapeOutputName{Column: unpivot.KeyOutputColumn, Label: unpivot.KeyOutputLabel},
			ValueOutput:   loomapi.TableShapeOutputName{Column: unpivot.ValueOutputColumn, Label: unpivot.ValueOutputLabel},
			NullRowPolicy: loomapi.TableShapeUnpivotNullRowPolicyReference{Kind: "unpivotNullRowPolicy", ChoiceId: unpivot.NullPolicyChoiceID},
			InputColumns:  make([]loomapi.TableShapeColumnReference, 0, len(unpivot.InputColumnChoiceIDs)),
		}
		for _, id := range unpivot.InputColumnChoiceIDs {
			result.InputColumns = append(result.InputColumns, loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: id})
		}
		intent.Unpivot = result
	}
	resolutionToLocal := make(map[string]string, len(value.DerivedColumns))
	for _, derived := range value.DerivedColumns {
		resolutionToLocal[derived.ResolutionID] = derived.LocalID
	}
	for _, derived := range value.DerivedColumns {
		division := (*loomapi.TableShapeDivisionByZeroPolicyReference)(nil)
		if derived.DivisionByZeroPolicyChoiceID != "" {
			division = &loomapi.TableShapeDivisionByZeroPolicyReference{Kind: "divisionByZeroPolicy", ChoiceId: derived.DivisionByZeroPolicyChoiceID}
		}
		intent.DerivedColumns = append(intent.DerivedColumns, loomapi.TableShapeDerivedColumnProposal{
			LocalId:     derived.LocalID,
			Output:      loomapi.TableShapeOutputName{Column: derived.OutputColumn, Label: derived.OutputLabel},
			Operator:    loomapi.TableShapeBinaryOperatorReference{Kind: "binaryOperator", ChoiceId: derived.OperatorChoiceID},
			LeftOperand: savedOperandIntent(derived.Left, resolutionToLocal, value.Pivot), RightOperand: savedOperandIntent(derived.Right, resolutionToLocal, value.Pivot),
			MissingInputPolicy:   loomapi.TableShapeMissingInputPolicyReference{Kind: "missingInputPolicy", ChoiceId: derived.MissingPolicyChoiceID},
			DivisionByZeroPolicy: division,
		})
	}
	return intent
}

func savedOperandIntent(value lifecycle.TableShapeSavedOperand, resolutionToLocal map[string]string, pivot *lifecycle.TableShapeSavedPivot) loomapi.TableShapeDerivedOperandIntent {
	switch value.Kind {
	case tableshapecap.ResolvedOperandCatalogChoice:
		if pivot != nil {
			for _, output := range pivot.PostPivotOutputs {
				if output.OperandChoiceID != value.ChoiceID {
					continue
				}
				if output.Kind == "group" {
					return loomapi.TableShapeDerivedOperandIntent{Kind: "pivotOutput", Reference: &loomapi.TableShapeDerivedOperandReference{
						Kind: "group", Column: &loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: output.ColumnChoiceID},
					}}
				}
				if output.Kind == "category" {
					return loomapi.TableShapeDerivedOperandIntent{Kind: "pivotOutput", Reference: &loomapi.TableShapeDerivedOperandReference{
						Kind: "category", Category: &loomapi.TableShapePivotCategoryReference{Kind: "pivotCategory", ChoiceId: output.CategoryChoiceID},
					}}
				}
			}
		}
		choiceID := value.ChoiceID
		return loomapi.TableShapeDerivedOperandIntent{Kind: "base", Reference: &loomapi.TableShapeDerivedOperandReference{Kind: "operand", ChoiceId: &choiceID}}
	case tableshapecap.ResolvedOperandResolution:
		localID := resolutionToLocal[value.ResolutionID]
		return loomapi.TableShapeDerivedOperandIntent{Kind: "derived", LocalId: &localID}
	case tableshapecap.ResolvedOperandLiteral:
		if value.Literal == nil {
			representation := loomapi.TableShapeDerivedOperandIntentRepresentation("integer")
			return loomapi.TableShapeDerivedOperandIntent{Kind: "literal", Representation: &representation, Text: stringPointer("")}
		}
		text, representation := scalarNumberText(*value.Literal)
		return loomapi.TableShapeDerivedOperandIntent{Kind: "literal", Representation: &representation, Text: &text}
	default:
		return loomapi.TableShapeDerivedOperandIntent{Kind: "base"}
	}
}

func scalarNumberText(value tableshapecap.Scalar) (string, loomapi.TableShapeDerivedOperandIntentRepresentation) {
	switch value.Kind {
	case tableshapecap.ScalarInteger:
		if value.Integer != nil {
			return strconv.FormatInt(*value.Integer, 10), "integer"
		}
	case tableshapecap.ScalarDecimal:
		if value.Decimal != nil {
			return strconv.FormatFloat(*value.Decimal, 'f', -1, 64), "decimal"
		}
	}
	return "", "integer"
}

func tableShapeResolutionResponse(value lifecycle.TableShapeResolutionResult, request *loomapi.TableShapeResolutionRequest) loomapi.TableShapeResolutionResponse {
	result := loomapi.TableShapeResolutionResponse{
		CatalogId: value.CatalogID, ResolutionId: value.ResolutionID,
		Kind:              loomapi.TableShapeResolutionResponseKind(value.Kind),
		OutputDescriptors: make([]loomapi.TableShapeResolvedOutputDescriptor, 0),
		PostPivotOperands: make([]loomapi.TableShapeEditorChoice, 0, len(value.DerivedOperands)),
	}
	if value.CategoryDiscoveryID != "" {
		result.CategoryDiscoveryId = &value.CategoryDiscoveryID
	}
	if len(value.Categories) > 0 {
		categories := make([]loomapi.TableShapeResolvedCategory, 0, len(value.Categories))
		for _, category := range value.Categories {
			categories = append(categories, loomapi.TableShapeResolvedCategory{
				Id: category.ID, Value: capabilityScalar(category.Value), OutputColumn: category.OutputColumn,
				OutputLabel: category.OutputLabel, Type: typeFact(category.Type),
			})
			var operandChoiceID *string
			if category.OperandChoiceID != "" {
				operandChoiceID = &category.OperandChoiceID
			}
			result.OutputDescriptors = append(result.OutputDescriptors, loomapi.TableShapeResolvedOutputDescriptor{
				Kind: "category", Category: &loomapi.TableShapePivotCategoryReference{Kind: "pivotCategory", ChoiceId: category.ID},
				OperandChoiceId: operandChoiceID, OutputColumn: category.OutputColumn, OutputLabel: category.OutputLabel, Type: typeFact(category.Type),
			})
		}
		result.Categories = &categories
	}
	for _, output := range value.PostPivotOutputs {
		if output.Kind != "group" {
			continue
		}
		var operandChoiceID *string
		if output.OperandChoiceID != "" {
			operandChoiceID = &output.OperandChoiceID
		}
		result.OutputDescriptors = append(result.OutputDescriptors, loomapi.TableShapeResolvedOutputDescriptor{
			Kind: "group", GroupColumn: &loomapi.TableShapeColumnReference{Kind: "column", ChoiceId: output.ColumnChoiceID},
			OperandChoiceId: operandChoiceID, OutputColumn: output.OutputColumn, OutputLabel: output.OutputLabel, Type: typeFact(output.Type),
		})
	}
	for _, operand := range value.DerivedOperands {
		result.PostPivotOperands = append(result.PostPivotOperands, editorChoice(operand.ID, "operand", operand.Label, true, ""))
	}
	if value.Result != nil {
		mapped := typeFact(*value.Result)
		result.Result = &mapped
		if request.Derived != nil {
			result.OutputDescriptors = append(result.OutputDescriptors, loomapi.TableShapeResolvedOutputDescriptor{
				Kind: "derived", OutputColumn: request.Derived.OutputColumn, OutputLabel: request.Derived.OutputLabel, Type: mapped,
			})
		}
	}
	if value.KeyResult != nil {
		mapped := typeFact(*value.KeyResult)
		result.KeyResult = &mapped
		if request.Unpivot != nil {
			result.OutputDescriptors = append(result.OutputDescriptors, loomapi.TableShapeResolvedOutputDescriptor{
				Kind: "unpivotKey", OutputColumn: request.Unpivot.KeyOutputColumn, OutputLabel: request.Unpivot.KeyOutputLabel, Type: mapped,
			})
		}
	}
	if value.ValueResult != nil {
		mapped := typeFact(*value.ValueResult)
		result.ValueResult = &mapped
		if request.Unpivot != nil {
			result.OutputDescriptors = append(result.OutputDescriptors, loomapi.TableShapeResolvedOutputDescriptor{
				Kind: "unpivotValue", OutputColumn: request.Unpivot.ValueOutputColumn, OutputLabel: request.Unpivot.ValueOutputLabel, Type: mapped,
			})
		}
	}
	return result
}

func capabilityScalar(value tableshapecap.Scalar) loomapi.TableShapeScalar {
	result := loomapi.TableShapeScalar{Kind: loomapi.TableShapeScalarKind(value.Kind)}
	result.String = value.String
	result.Integer = value.Integer
	result.Decimal = value.Decimal
	result.Boolean = value.Boolean
	return result
}

func capabilityScalarFromAPI(value loomapi.TableShapeScalar) tableshapecap.Scalar {
	return tableshapecap.Scalar{Kind: tableshapecap.ScalarKind(value.Kind), String: value.String, Integer: value.Integer, Decimal: value.Decimal, Boolean: value.Boolean}
}

func typeFact(value tableshapecap.TypeFact) loomapi.TableShapeTypeFact {
	var unit *string
	if value.UnitIdentity != "" {
		unit = &value.UnitIdentity
	}
	return loomapi.TableShapeTypeFact{LogicalType: loomapi.TableShapeLogicalType(value.LogicalType), Nullable: value.Nullable, UnitIdentity: unit}
}

func editorChoice(id, kind, label string, supported bool, reason string) loomapi.TableShapeEditorChoice {
	return loomapi.TableShapeEditorChoice{ChoiceId: id, ChoiceKind: loomapi.TableShapeEditorChoiceChoiceKind(kind), Label: label, Availability: choiceAvailability(supported, reason)}
}

func choiceAvailability(supported bool, reason string) loomapi.TableShapeChoiceAvailability {
	if supported {
		return loomapi.TableShapeChoiceAvailability{Kind: "supported"}
	}
	return loomapi.TableShapeChoiceAvailability{Kind: "unsupported", Reason: stringPointer(reason)}
}

func roleChoiceAvailability(availability []tableshapecap.RoleAvailability, role tableshapecap.ChoiceRole) loomapi.TableShapeChoiceAvailability {
	for _, item := range availability {
		if item.Role == role {
			return choiceAvailability(item.State == tableshapecap.AvailabilitySupported, item.Message)
		}
	}
	return choiceAvailability(false, "This choice role is unavailable in the current compiler catalog.")
}

func savedAvailability(value lifecycle.TableShapeSavedSupport) loomapi.TableShapeChoiceAvailability {
	return choiceAvailability(value.State == tableshapecap.AvailabilitySupported, value.Message)
}

func policyEditorKind(role tableshapecap.ChoiceRole) string {
	switch role {
	case tableshapecap.RolePolicyDuplicate:
		return "duplicatePolicy"
	case tableshapecap.RolePolicyMissing:
		return "missingCellPolicy"
	case tableshapecap.RolePolicyUnlisted:
		return "unlistedCategoryPolicy"
	case tableshapecap.RolePolicyUnpivotNull:
		return "unpivotNullRowPolicy"
	case tableshapecap.RolePolicyDerivedMissing:
		return "missingInputPolicy"
	case tableshapecap.RolePolicyDivisionByZero:
		return "divisionByZeroPolicy"
	default:
		return ""
	}
}

func stringPointer(value string) *string { return &value }
