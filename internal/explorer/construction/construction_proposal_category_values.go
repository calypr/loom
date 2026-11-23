package construction

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func (s *Service) verifyConstructionProposalPivotCategoriesExcept(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
	base *authoringv2.Construction,
	skip map[string]bool,
) ([]ConstructionProposalDiagnostic, error) {
	if s.config.ScanCategories == nil {
		return nil, nil
	}
	var diagnostics []ConstructionProposalDiagnostic
	baseByID := make(map[string]authoringv2.ConstructionStep, len(candidate.Steps))
	if base != nil {
		for _, step := range base.Steps {
			baseByID[step.ID] = step
		}
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
		if changed && base != nil && constructionPivotLabelOnlyEdit(*base, candidate, step.ID) {
			changed = false
		}
		if !changed && base != nil {
			prior, exists := baseByID[step.ID]
			changed = !exists || !reflect.DeepEqual(prior.Inputs, step.Inputs) || !reflect.DeepEqual(prior.Operation, step.Operation)
		}
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

			discoveryRequest.compileCandidate = true
		}
		discovery, err := s.DiscoverConstructionCategories(ctx, discoveryRequest)
		if err != nil {
			if changed {
				return nil, err
			}
			diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "PROBE_ERROR",
				fmt.Sprintf("Pivot %s categories were not verified because the category probe failed: %v", step.ID, err)))
			continue
		}

		switch discovery.Outcome {
		case constructionCategoryDiscoveryComplete:
			if len(discovery.Categories) == 0 {
				if !changed {
					diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "NO_OBSERVED_CATEGORIES",
						fmt.Sprintf("Pivot %s categories were not verified because the current rows have no observed values that can produce cells for the selected category and value columns", step.ID)))
					continue
				}
				return nil, unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
					fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
			}
		case constructionCategoryDiscoveryNoValues:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "NO_OBSERVED_CATEGORIES",
					fmt.Sprintf("Pivot %s categories were not verified because the current rows have no observed values that can produce cells for the selected category and value columns", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_NO_OBSERVED_CATEGORIES",
				fmt.Sprintf("Pivot %s has no observed values that can produce cells for the selected category and value columns", step.ID), nil)
		case constructionCategoryDiscoveryLimitExceeded:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "LIMIT_EXCEEDED",
					fmt.Sprintf("Pivot %s categories were not verified because this category field has more than 256 values in the current rows.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because this category field has more than 256 values in the current rows. Choose another category field, filter rows before pivoting, or reduce the number of categories, then retry.", nil)
		case constructionCategoryDiscoveryMissingUnsupported:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "MISSING_UNSUPPORTED",
					fmt.Sprintf("Pivot %s categories were not verified because some rows have no category field and this scan cannot distinguish an absent field from a null value.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
				"The Pivot categories cannot be verified because some rows have no category field and this scan cannot distinguish an absent field from a null value. Filter rows where the category field is missing, or choose a field populated in every row, then retry.", nil)
		default:
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "UNVERIFIABLE",
					fmt.Sprintf("Pivot %s categories were not verified against a complete value-bearing scan.", step.ID)))
				continue
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORIES_UNVERIFIABLE",
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
			if !changed {
				diagnostics = append(diagnostics, constructionProposalPivotProbeDiagnostic(step.ID, "CATEGORY_HAS_NO_VALUES",
					fmt.Sprintf("Pivot %s categories were not verified because category %s has no observed values for the selected category and value columns", step.ID, constructionCategoryLabel(category.Key))))
				break
			}
			return nil, unprocessable("construction-proposal", "PIVOT_CATEGORY_HAS_NO_VALUES",
				fmt.Sprintf("Pivot %s category %s has no observed values for the selected category and value columns", step.ID, constructionCategoryLabel(category.Key)), nil)
		}
	}
	return diagnostics, nil
}

func constructionProposalPivotProbeDiagnostic(stepID, outcome, message string) ConstructionProposalDiagnostic {
	return ConstructionProposalDiagnostic{
		Code:    "PIVOT_CATEGORIES_UNVERIFIABLE",
		Message: message,
		StepID:  stepID,
		Outcome: outcome,
	}
}

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

func constructionPivotLabelOnlyEdit(base, candidate authoringv2.Construction, stepID string) bool {
	if base.Version != candidate.Version || !reflect.DeepEqual(base.SourceProjections, candidate.SourceProjections) {
		return false
	}
	before, after := map[string]authoringv2.ConstructionStep{}, map[string]authoringv2.ConstructionStep{}
	for _, step := range base.Steps {
		before[step.ID] = step
	}
	for _, step := range candidate.Steps {
		after[step.ID] = step
	}
	if len(before) != len(base.Steps) || len(after) != len(candidate.Steps) {
		return false
	}
	stripLabels := func(step authoringv2.ConstructionStep) authoringv2.ConstructionStep {
		step.Outputs = append([]authoringv2.StageColumn(nil), step.Outputs...)
		for i := range step.Outputs {
			step.Outputs[i].Label = ""
		}
		return step
	}
	prior, exists := before[stepID]
	current, found := after[stepID]
	if !exists || !found || reflect.DeepEqual(prior.Outputs, current.Outputs) {
		return false
	}
	active := map[string]bool{}
	done := map[string]bool{}
	var unchanged func(string) bool
	unchanged = func(id string) bool {
		if active[id] {
			return false
		}
		if done[id] {
			return true
		}
		old, ok := before[id]
		next, present := after[id]
		if !ok || !present || !reflect.DeepEqual(stripLabels(old), stripLabels(next)) {
			return false
		}
		active[id] = true
		for _, input := range next.Inputs {
			if input.Kind == authoringv2.ConstructionInputStepOutput && !unchanged(input.StepID) {
				return false
			}
		}
		delete(active, id)
		done[id] = true
		return true
	}
	return unchanged(stepID)
}
