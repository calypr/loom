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
func (s *Service) verifyConstructionProposalPivotCategories(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) error {
	if s.config.ScanCategories == nil || request.ChangedStepID == "" {
		return nil
	}
	index := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	if index < 0 {
		return nil
	}
	step := candidate.Steps[index]
	pivot := step.Operation.Pivot
	if step.Operation.Kind != authoringv2.ConstructionOperationPivot || pivot == nil || len(pivot.Categories) == 0 {
		return nil
	}
	discovery, err := s.DiscoverConstructionCategories(ctx, ConstructionCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, StageID: constructionStepInputStageID(step),
		CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
		GroupKeyIDs:           append([]string(nil), pivot.GroupKeyIDs...),
		PivotStepID:           request.ChangedStepID,
		PivotSources:          append([]ConstructionPivotSourceSelection(nil), request.PivotSources...),
		CandidateConstruction: &candidate,
	})
	if err != nil {
		return err
	}
	switch discovery.Outcome {
	case constructionCategoryDiscoveryComplete:
		if len(discovery.Categories) == 0 {
			return unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
				"the selected category and value columns have no observed values that can produce Pivot cells", nil)
		}
	case constructionCategoryDiscoveryNoValues:
		return unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
			"the selected category and value columns have no observed values that can produce Pivot cells", nil)
	default:
		// LIMIT_EXCEEDED and MISSING_UNSUPPORTED do not prove the authored
		// categories absent. Preserve the prior proposal behavior and let the
		// downstream pivot validation report the discovery outcome.
		return nil
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
			fmt.Sprintf("Pivot category %s has no observed values for the selected category and value columns", constructionCategoryLabel(category.Key)), nil)
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
