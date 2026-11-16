package lifecycle

import (
	"context"
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// verifyConstructionProposalPivotCategories rejects authored Pivot categories
// that cannot produce a cell in the current category/value scan. Discovery is
// independent of proposal, so this check reuses the same compiler-owned scan
// instead of trusting client-supplied category keys.
//
// Every Pivot in the candidate is checked, not only the changed step. An
// unchanged Pivot keeps its existing owned input chain and is probed against the
// candidate prefix. When that probe cannot be compiled safely, the unchanged
// Pivot is left to the later proposal validation so a change elsewhere is not
// rejected for an unrelated saved step.
func (s *Service) verifyConstructionProposalPivotCategories(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) error {
	return s.verifyConstructionProposalPivotCategoriesExcept(ctx, request, candidate, nil)
}

// verifyConstructionProposalPivotCategoriesExcept skips steps whose categories
// were just frozen from a complete discovery scan. Re-scanning them would repeat
// the same value-bearing proof and reject only on a racing row change.
func (s *Service) verifyConstructionProposalPivotCategoriesExcept(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
	skip map[string]bool,
) error {
	if s.config.ScanCategories == nil {
		return nil
	}
	removed := make(map[string]struct{}, len(request.RemoveStepIDs))
	for _, stepID := range request.RemoveStepIDs {
		removed[stepID] = struct{}{}
	}
	for _, step := range candidate.Steps {
		if _, gone := removed[step.ID]; gone {
			continue
		}
		if skip[step.ID] {
			continue
		}
		pivot := step.Operation.Pivot
		if step.Operation.Kind != authoringv2.ConstructionOperationPivot || pivot == nil || len(pivot.Categories) == 0 {
			continue
		}
		changed := step.ID == request.ChangedStepID
		discoveryRequest := ConstructionCategoryDiscoveryRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
			ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
			OutputID: request.OutputID, StageID: constructionStepInputStageID(step),
			CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
			GroupKeyIDs:           append([]string(nil), pivot.GroupKeyIDs...),
			PivotStepID:           step.ID,
			CandidateConstruction: &candidate,
		}
		if changed {
			discoveryRequest.PivotSources = append([]ConstructionPivotSourceSelection(nil), request.PivotSources...)
		} else {
			// An unchanged Pivot must not consume the edited Pivot's signed source
			// choices. Compile its candidate prefix with its own owned inputs.
			discoveryRequest.compileCandidate = true
		}
		discovery, err := s.DiscoverConstructionCategories(ctx, discoveryRequest)
		if err != nil {
			if changed {
				return err
			}
			continue
		}
		// LIMIT_EXCEEDED and MISSING_UNSUPPORTED cannot prove an authored
		// category absent. Refuse rather than admit an unverified category; this
		// intentionally rejects otherwise-valid saved Pivots whose scan cannot
		// produce a complete value-bearing proof until the operator narrows the
		// rows, category field, or authored category set.
		switch discovery.Outcome {
		case constructionCategoryDiscoveryComplete:
			if len(discovery.Categories) == 0 {
				return unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
					fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
			}
		case constructionCategoryDiscoveryNoValues:
			return unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
				fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
		case constructionCategoryDiscoveryLimitExceeded:
			return unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because this category field has more than 256 values in the current rows. Choose another category field, filter rows before pivoting, or reduce the number of categories, then retry.", nil)
		case constructionCategoryDiscoveryMissingUnsupported:
			return unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because some rows have no category field and this scan cannot distinguish an absent field from a null value. Filter rows where the category field is missing, or choose a field populated in every row, then retry.", nil)
		default:
			return unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories could not be verified against a complete value-bearing scan. Refresh the preview and retry; if the problem persists, choose another category field or filter rows before pivoting.", nil)
		}
		observed := make(map[string]struct{}, len(discovery.Categories))
		for _, category := range discovery.Categories {
			observed[constructionProposalCategoryKey(category.Key)] = struct{}{}
		}
		for _, category := range pivot.Categories {
			if _, exists := observed[constructionProposalCategoryKey(category.Key)]; exists {
				continue
			}
			return unprocessable("construction-proposal", "PIVOT_CATEGORY_HAS_NO_VALUES",
				fmt.Sprintf("Pivot %s category %s has no observed values for the selected category and value columns", step.ID, constructionCategoryLabel(category.Key)), nil)
		}
	}
	return nil
}

// constructionProposalCategoryKey is the local canonical identity for a Pivot
// category key. authoringv2.TableScalar.identity is unexported, so encode every
// kind explicitly and preserve typed zero payloads (*int64(0), false, 0.0).
func constructionProposalCategoryKey(value authoringv2.TableScalar) string {
	switch value.Kind {
	case authoringv2.TableScalarString:
		if value.String == nil {
			return "STRING:<nil>"
		}
		return "STRING:" + strconv.Quote(*value.String)
	case authoringv2.TableScalarInteger:
		if value.Integer == nil {
			return "INTEGER:<nil>"
		}
		return "INTEGER:" + strconv.FormatInt(*value.Integer, 10)
	case authoringv2.TableScalarDecimal:
		if value.Decimal == nil {
			return "DECIMAL:<nil>"
		}
		return "DECIMAL:" + strconv.FormatFloat(*value.Decimal, 'g', -1, 64)
	case authoringv2.TableScalarBoolean:
		if value.Boolean == nil {
			return "BOOLEAN:<nil>"
		}
		return "BOOLEAN:" + strconv.FormatBool(*value.Boolean)
	case authoringv2.TableScalarNull:
		return "NULL"
	case authoringv2.TableScalarMissing:
		return "MISSING"
	default:
		return "UNKNOWN:" + string(value.Kind)
	}
}
