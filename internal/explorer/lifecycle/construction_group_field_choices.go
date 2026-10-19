package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func (s *Service) constructionGroupFieldChoices(
	ctx context.Context,
	base constructionBase,
	outputID string,
	stage explorer.ReceiptConstructionStage,
	semanticChoices []ConstructionSemanticGroupChoice,
	semanticChoicesIncomplete bool,
	targetGroupStepID string,
	selectedGroupStepID string,
) ([]ConstructionGroupFieldChoice, bool, error) {
	choices := make([]ConstructionGroupFieldChoice, 0, len(semanticChoices)+16)
	for _, semantic := range semanticChoices {
		choices = append(choices, ConstructionGroupFieldChoice{
			Kind:             "SEMANTIC_CATEGORY",
			SemanticCategory: &authoringv2.ConstructionGroupSemanticCategory{Kind: "CHOICE", ChoiceID: semantic.ChoiceID},
			Label:            semantic.Label, ScopeLabel: semantic.ScopeLabel, ResourceType: semantic.ResourceType, CanonicalPath: semantic.SourcePath,
		})
	}
	context := stage.GroupFieldSourceContext
	if context == nil {
		return choices, semanticChoicesIncomplete, nil
	}
	if stage.ID != context.SourceStageID || stage.RowIdentityColumn != context.SourceIdentityColumn ||
		context.SourceStageID == "" || context.SourceShapeID == "" {
		return nil, false, conflict("construction-capabilities", "INVALID_COMPILATION_RECEIPT", "the selected source stage has inconsistent Group field context", nil, nil)
	}
	if (context.SourceExpansion == nil) != (context.SourceIdentityColumn == "_key") ||
		(context.SourceExpansion != nil && context.SourceIdentityColumn != "__loom_expansion_identity") {
		return nil, false, conflict("construction-capabilities", "INVALID_COMPILATION_RECEIPT", "the Group source shape does not match its row identity", nil, nil)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, false, unavailable("construction-capabilities", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	choicesByID := make(map[string]ConstructionGroupFieldChoice)
	for _, owner := range context.FieldOwners {
		occurrence, route, routeErr := constructionGroupFieldOwnerRoute(base, context, owner.OccurrenceID)
		if routeErr != nil || occurrence.OccurrenceID != owner.OccurrenceID || occurrence.ResourceType != owner.OwnerResourceType {
			return nil, false, conflict("construction-capabilities", "STALE_ROUTE", "the compiled Group field owner no longer matches the authorized route", nil, routeErr)
		}
		if owner.Role == "ROOT" && (owner.OccurrenceID != base.document.Route.OccurrenceID || owner.ResourceType != base.document.RootResourceType) {
			return nil, false, conflict("construction-capabilities", "STALE_ROUTE", "the compiled Group root field owner changed", nil, nil)
		}
		for _, candidate := range base.snapshot.Candidates {
			if candidate.NodeID != occurrence.NodeID || candidate.ResourceType != owner.OwnerResourceType || !candidate.Observed || !candidate.Populated ||
				fhirNonGroupableGroupFieldPath(candidate.FieldPath) {
				continue
			}
			fieldPath, ok := groupFieldPathForOwner(owner, context.SourceExpansion, candidate.FieldPath)
			if !ok || fieldPath == "" {
				continue
			}
			consumedBoundaries, consumedOK := groupConsumedFieldBoundaries(context.SourceExpansion, owner)
			if !consumedOK {
				continue
			}
			facts, factsErr := index.ResolveRowPath(fhirschema.DefinitionName(owner.ResourceType), fieldPath)
			if factsErr != nil || facts.CanonicalPath != fieldPath || facts.Shape != fhirschema.RowPathScalar ||
				facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || !capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
				continue
			}
			proved, proofErr := proveConstructionCandidate(ctx, base.authorized, base.document.RootResourceType, candidate, route)
			if proofErr != nil || proved.ID != candidate.ID || proved.NodeID != occurrence.NodeID ||
				proved.ResourceType != owner.OwnerResourceType || canonicalRowPath(proved.FieldPath) != canonicalRowPath(candidate.FieldPath) {
				continue
			}
			source := capability.GroupFieldChoiceSource{
				TargetGroupStepID: targetGroupStepID,
				SourceStageID:     context.SourceStageID, SourceShapeID: context.SourceShapeID,
				SourceIdentityColumn: context.SourceIdentityColumn,
				SourceRole:           owner.Role, OwnerOccurrenceID: owner.OccurrenceID, OwnerResourceType: owner.OwnerResourceType,
				OccurrenceID: owner.OccurrenceID, ResourceType: owner.ResourceType,
				OwnerSelector:      groupSourceSelector(context.SourceExpansion),
				BoundarySelector:   owner.BoundarySelector,
				ConsumedBoundaries: consumedBoundaries,
				ValueKind:          "SCALAR", CandidateID: proved.ID, NodeID: proved.NodeID,
				CanonicalPath: fieldPath, FHIRType: string(facts.FHIRType),
				Cardinality: "ONE", Shape: "SCALAR",
			}
			choice, choiceErr := capability.NewGroupFieldConstructionChoice(base.snapshot.Token, route, source, proved)
			if choiceErr != nil {
				continue
			}
			label := firstNonEmpty(facts.Title, proved.Label, constructionGroupFieldLabel(fieldPath))
			response := ConstructionGroupFieldChoice{
				Kind: "FIELD_SOURCE", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: choice.ChoiceID},
				ValueKind: "SCALAR", Label: label, ScopeLabel: groupFieldScopeLabel(owner, context.SourceExpansion),
				ValueType: proved.LogicalType, SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID,
				CandidateID: proved.ID, SourceRole: owner.Role, OccurrenceID: owner.OccurrenceID,
				ResourceType: owner.ResourceType, OwnerOccurrenceID: owner.OccurrenceID,
				OwnerResourceType: owner.OwnerResourceType, CanonicalPath: fieldPath,
				BoundarySelector: owner.BoundarySelector,
			}
			response.InputColumnID = matchedPublicGroupFieldColumn(base.document, stage, owner.OccurrenceID, proved.FieldPath)
			choicesByID[choice.ChoiceID] = response
		}
	}
	if targetGroupStepID == "" && context.SourceExpansion != nil && s.config.RowChoicePlanner != nil {
		rowChoices, listErr := s.config.RowChoicePlanner.ListRowChoices(ctx, base.snapshot.Clone(), base.document)
		if listErr != nil {
			return nil, false, conflict("construction-capabilities", "STALE_ROUTE", "the current Coding row choice could not be resolved", nil, listErr)
		}
		expansion := context.SourceExpansion
		owner, route, routeErr := constructionGroupFieldOwnerRoute(base, context, expansion.OwnerOccurrenceID)
		observedCoding := false
		if routeErr == nil {
			for _, candidate := range base.snapshot.Candidates {
				if observedGroupCodingSelector(candidate, owner.NodeID, expansion.OwnerResourceType, expansion.Selector) {
					observedCoding = true
					break
				}
			}
		}
		if routeErr == nil && observedCoding {
			for _, rowChoice := range rowChoices {
				if rowChoice.Kind != capability.RowChoiceExpandedScope || rowChoice.OccurrenceID != expansion.OwnerOccurrenceID ||
					rowChoice.NodeID != owner.NodeID || rowChoice.ResourceType != expansion.OwnerResourceType ||
					rowChoice.Path != expansion.Selector || rowChoice.FHIRType != "Coding" || rowChoice.Reference ||
					rowChoice.Cardinality != capability.RowChoiceMany || !equalGroupFieldRoutes(rowChoice.Route, route) {
					continue
				}
				source := capability.GroupFieldChoiceSource{
					SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID,
					SourceIdentityColumn: context.SourceIdentityColumn,
					SourceRole:           "CURRENT_ITEM", OwnerOccurrenceID: expansion.OwnerOccurrenceID,
					OwnerResourceType: expansion.OwnerResourceType, OwnerSelector: expansion.Selector,
					OccurrenceID: expansion.OwnerOccurrenceID, ResourceType: "Coding", RowChoiceID: rowChoice.ChoiceID,
					ValueKind: "CODING_IDENTITY", NodeID: owner.NodeID, CanonicalPath: expansion.Selector,
					FHIRType: "Coding", Cardinality: "ONE", Shape: "CODING_IDENTITY",
				}
				choice, choiceErr := capability.NewGroupCodingIdentityChoice(base.snapshot.Token, route, source)
				if choiceErr != nil {
					continue
				}
				choicesByID[choice.ChoiceID] = ConstructionGroupFieldChoice{
					Kind: "FIELD_SOURCE", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: choice.ChoiceID},
					ValueKind: "CODING_IDENTITY", Label: "Coding", ScopeLabel: expansion.OwnerResourceType + " · " + expansion.Selector,
					ValueType: "string", SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID,
					SourceRole: "CURRENT_ITEM", OccurrenceID: expansion.OwnerOccurrenceID, ResourceType: "Coding",
					OwnerOccurrenceID: expansion.OwnerOccurrenceID, OwnerResourceType: expansion.OwnerResourceType,
					CanonicalPath: expansion.Selector,
				}
			}
		}
	}
	relatedChoicesIncomplete := false
	if context.SourceExpansion == nil {
		relatedChoicesIncomplete, err = s.appendRelatedGroupFieldChoices(ctx, base, stage, context, index, choicesByID, targetGroupStepID, selectedGroupStepID)
		if err != nil {
			return nil, false, err
		}
	}
	for _, choice := range choicesByID {
		choices = append(choices, choice)
	}
	markSavedGroupFieldChoices(base, stage.ID, selectedGroupStepID, choices)
	sortConstructionGroupFieldChoices(choices)
	return choices, semanticChoicesIncomplete || relatedChoicesIncomplete, nil
}

func markSavedGroupFieldChoices(base constructionBase, stageID, groupStepID string, choices []ConstructionGroupFieldChoice) {
	if groupStepID == "" {
		return
	}
	var selected *authoringv2.ConstructionStep
	for index := range base.construction.Steps {
		if base.construction.Steps[index].ID == groupStepID {
			selected = &base.construction.Steps[index]
			break
		}
	}
	if selected == nil || selected.Operation.Kind != authoringv2.ConstructionOperationGroup || selected.Operation.Group == nil {
		return
	}
	inputStageID, ok := constructionGroupInputStageID(*selected)
	if !ok || inputStageID != stageID {
		return
	}
	for choiceIndex := range choices {
		for _, key := range selected.Operation.Group.Keys {
			if key.FieldSource != nil && key.FieldSource.Kind == "BINDING" && groupFieldChoiceMatchesBinding(choices[choiceIndex], *key.FieldSource) {
				choices[choiceIndex].MatchedOutputKeyID = key.OutputColumnID
				break
			}
		}
	}
}

func sortConstructionGroupFieldChoices(choices []ConstructionGroupFieldChoice) {
	choiceID := func(choice ConstructionGroupFieldChoice) string {
		if choice.FieldSource != nil {
			return choice.FieldSource.ChoiceID
		}
		if choice.SemanticCategory != nil {
			return choice.SemanticCategory.ChoiceID
		}
		return ""
	}
	sort.Slice(choices, func(i, j int) bool {
		left, right := choices[i], choices[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Label != right.Label {
			return left.Label < right.Label
		}
		if left.CanonicalPath != right.CanonicalPath {
			return left.CanonicalPath < right.CanonicalPath
		}
		if left.SourceRole != right.SourceRole {
			return left.SourceRole < right.SourceRole
		}
		if left.OwnerResourceType != right.OwnerResourceType {
			return left.OwnerResourceType < right.OwnerResourceType
		}
		if left.OwnerOccurrenceID != right.OwnerOccurrenceID {
			return left.OwnerOccurrenceID < right.OwnerOccurrenceID
		}
		return choiceID(left) < choiceID(right)
	})
}

func (s *Service) constructionGroupRowValueChoices(
	ctx context.Context,
	base constructionBase,
	outputID string,
	stage explorer.ReceiptConstructionStage,
	groupStepID string,
) ([]ConstructionGroupFieldChoice, error) {
	if groupStepID == "" {
		return []ConstructionGroupFieldChoice{}, nil
	}
	var target *authoringv2.ConstructionStep
	for index := range base.construction.Steps {
		if base.construction.Steps[index].ID == groupStepID {
			target = &base.construction.Steps[index]
			break
		}
	}
	if target == nil || target.Operation.Kind != authoringv2.ConstructionOperationGroup || target.Operation.Group == nil {
		return nil, conflict("construction-capabilities", "STALE_GROUP_STEP", "row-value choices require the exact saved Group step", nil, nil)
	}
	inputStageID, ok := constructionGroupInputStageID(*target)
	if !ok || inputStageID != stage.ID {
		return nil, conflict("construction-capabilities", "STALE_GROUP_STEP_INPUT", "row-value choices must use the exact Group input stage", nil, nil)
	}
	choices, _, err := s.constructionGroupFieldChoices(ctx, base, outputID, stage, nil, false, groupStepID, "")
	if err != nil {
		return nil, err
	}
	result := make([]ConstructionGroupFieldChoice, 0, len(choices))
	for _, choice := range choices {
		if choice.Kind == "FIELD_SOURCE" && choice.ValueKind == "SCALAR" {
			result = append(result, choice)
		}
	}
	return result, nil
}

func (s *Service) resolveConstructionGroupFieldSources(
	ctx context.Context,
	base constructionBase,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	priorByStep := make(map[string][]authoringv2.ConstructionGroupKey)
	priorRowValuesByStep := make(map[string][]authoringv2.ConstructionRowValue)
	for _, step := range base.construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationGroup && step.Operation.Group != nil {
			priorByStep[step.ID] = append([]authoringv2.ConstructionGroupKey(nil), step.Operation.Group.Keys...)
			priorRowValuesByStep[step.ID] = append([]authoringv2.ConstructionRowValue(nil), step.RowValues...)
		}
	}
	for stepIndex := range candidate.Steps {
		step := &candidate.Steps[stepIndex]
		if step.Operation.Kind != authoringv2.ConstructionOperationGroup || step.Operation.Group == nil {
			continue
		}
		inputStageID, ok := constructionGroupInputStageID(*step)
		if !ok {
			if constructionStepHasGroupFieldSource(*step) {
				return candidate, unprocessable("construction-proposal", "GROUP_FIELD_SOURCE_STAGE_UNSUPPORTED", "Group fieldSource requires its exact compiled source input stage", nil)
			}
			continue
		}
		stage := receiptConstructionStageByID(base.stages, inputStageID)
		if stage == nil || stage.GroupFieldSourceContext == nil || stage.GroupFieldSourceContext.SourceStageID != inputStageID {
			if constructionStepHasGroupFieldSource(*step) {
				return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CONTEXT", "the selected Group input stage has no current compiler-issued field context", nil, nil)
			}
			continue
		}
		groupRelatedRouteID := ""
		currentChoices, _, err := s.constructionGroupFieldChoices(ctx, base, outputID, *stage, nil, false, "", step.ID)
		if err != nil {
			return candidate, err
		}
		for keyIndex := range step.Operation.Group.Keys {
			key := &step.Operation.Group.Keys[keyIndex]
			if key.FieldSource == nil {
				continue
			}
			prior := groupFieldSourceForOutput(priorByStep[step.ID], key.OutputColumnID)
			var matching *ConstructionGroupFieldChoice
			resolveChoiceToBinding := key.FieldSource.Kind == "CHOICE"
			switch key.FieldSource.Kind {
			case "CHOICE":
				if key.FieldSource.ChoiceID == "" {
					return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "Group fieldSource CHOICE requires a signed choiceId", nil)
				}
				for index := range currentChoices {
					choice := &currentChoices[index]
					if choice.Kind == "FIELD_SOURCE" && choice.FieldSource != nil && choice.FieldSource.Kind == "CHOICE" &&
						choice.FieldSource.ChoiceID == key.FieldSource.ChoiceID {
						matching = choice
						break
					}
				}
			case "BINDING":
				if prior == nil || prior.FieldSource == nil || !reflect.DeepEqual(*prior.FieldSource, *key.FieldSource) {
					return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "a new Group field requires a current signed source choice", nil)
				}
				for index := range currentChoices {
					choice := &currentChoices[index]
					if choice.Kind == "FIELD_SOURCE" && groupFieldChoiceMatchesBinding(*choice, *key.FieldSource) {
						matching = choice
						break
					}
				}
			default:
				return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "Group fieldSource must be a current CHOICE or retained BINDING", nil)
			}
			if matching == nil || matching.FieldSource == nil || matching.FieldSource.Kind != "CHOICE" {
				return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "reload the Group field choice for this exact stage and source shape", nil, nil)
			}
			if err := bindOneGroupRelatedRoute(matching, &groupRelatedRouteID); err != nil {
				return candidate, err
			}
			identity, decodeErr := capability.DecodeConstructionChoiceID(matching.FieldSource.ChoiceID)
			if decodeErr != nil || identity.SnapshotToken != base.snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceGroupField || identity.SemanticContextToken != "" || identity.BuildID != "" {
				return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "reload the Group field choice for the current authorized snapshot", nil, decodeErr)
			}
			source, sourceOK := identity.Source.(capability.GroupFieldChoiceSource)
			if !sourceOK || source.TargetGroupStepID != "" || !groupChoiceMatchesInputContext(source, *stage.GroupFieldSourceContext) {
				return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "the Group field choice no longer matches its exact compiled stage context", nil, nil)
			}
			if key.FieldSource.Kind == "BINDING" && !groupFieldChoiceMatchesBinding(*matching, *key.FieldSource) {
				return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_BINDING", "the saved Group field binding no longer matches the live compiler-resolved occurrence", nil, nil)
			}
			if resolveChoiceToBinding {
				key.FieldSource = constructionGroupFieldBinding(source)
			}
		}
		if groupStepHasRowValueFieldSource(*step) {
			rowChoices, choiceErr := s.constructionGroupRowValueChoices(ctx, base, outputID, *stage, step.ID)
			if choiceErr != nil {
				return candidate, choiceErr
			}
			for rowValueIndex := range step.RowValues {
				rowValue := &step.RowValues[rowValueIndex]
				if rowValue.FieldSource == nil {
					continue
				}
				prior := groupRowValueSourceForOutput(priorRowValuesByStep[step.ID], rowValue.OutputColumnID)
				var matching *ConstructionGroupFieldChoice
				resolveChoiceToBinding := rowValue.FieldSource.Kind == "CHOICE"
				switch rowValue.FieldSource.Kind {
				case "CHOICE":
					if rowValue.FieldSource.ChoiceID == "" {
						return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "Group rowValue fieldSource CHOICE requires a signed choiceId", nil)
					}
					for index := range rowChoices {
						choice := &rowChoices[index]
						if choice.FieldSource != nil && choice.FieldSource.Kind == "CHOICE" && choice.FieldSource.ChoiceID == rowValue.FieldSource.ChoiceID {
							matching = choice
							break
						}
					}
				case "BINDING":
					if prior == nil || prior.FieldSource == nil || !reflect.DeepEqual(*prior.FieldSource, *rowValue.FieldSource) {
						return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "a new Group row value requires a current signed source choice", nil)
					}
					for index := range rowChoices {
						choice := &rowChoices[index]
						if groupFieldChoiceMatchesBinding(*choice, *rowValue.FieldSource) {
							matching = choice
							break
						}
					}
				default:
					return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "Group rowValue fieldSource must be a current SCALAR CHOICE or retained BINDING", nil)
				}
				if matching == nil || matching.FieldSource == nil || matching.FieldSource.Kind != "CHOICE" {
					return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "reload the scalar field choice for this exact Group input stage and source shape", nil, nil)
				}
				if err := bindOneGroupRelatedRoute(matching, &groupRelatedRouteID); err != nil {
					return candidate, err
				}
				identity, decodeErr := capability.DecodeConstructionChoiceID(matching.FieldSource.ChoiceID)
				if decodeErr != nil || identity.SnapshotToken != base.snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceGroupField || identity.SemanticContextToken != "" || identity.BuildID != "" {
					return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "reload the Group row-value choice for the current authorized snapshot", nil, decodeErr)
				}
				source, sourceOK := identity.Source.(capability.GroupFieldChoiceSource)
				if !sourceOK || source.TargetGroupStepID != step.ID || !groupChoiceMatchesInputContext(source, *stage.GroupFieldSourceContext) ||
					source.OwnerOccurrenceID != source.OccurrenceID || source.ValueKind != "SCALAR" {
					return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "the Group row-value choice no longer matches its exact Group step, input stage, and source shape", nil, nil)
				}
				if rowValue.FieldSource.Kind == "BINDING" && !groupFieldChoiceMatchesBinding(*matching, *rowValue.FieldSource) {
					return candidate, conflict("construction-proposal", "STALE_GROUP_FIELD_BINDING", "the saved Group row-value binding no longer matches the live compiler-resolved occurrence", nil, nil)
				}
				if resolveChoiceToBinding {
					rowValue.FieldSource = constructionGroupFieldBinding(source)
				}
			}
		}
	}
	return candidate, nil
}

func bindOneGroupRelatedRoute(choice *ConstructionGroupFieldChoice, routeID *string) error {
	if choice == nil || choice.RelatedOccurrence == nil {
		return nil
	}
	choiceRouteID := strings.TrimSpace(choice.RelatedOccurrence.RouteID)
	if choiceRouteID == "" {
		return conflict("construction-proposal", "STALE_GROUP_FIELD_CHOICE", "the related Group field choice has no exact route identity", nil, nil)
	}
	if *routeID != "" && *routeID != choiceRouteID {
		return unprocessable("construction-proposal", "GROUP_FIELD_ROUTE_MISMATCH", "all related fields in one Group must use the same exact route", nil)
	}
	*routeID = choiceRouteID
	return nil
}

func groupChoiceMatchesInputContext(source capability.GroupFieldChoiceSource, context explorer.ReceiptConstructionGroupFieldSourceContext) bool {
	if source.SourceStageID != context.SourceStageID || source.SourceIdentityColumn != context.SourceIdentityColumn ||
		source.SourceShapeID == "" || source.OwnerOccurrenceID != source.OccurrenceID {
		return false
	}
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
		return source.Input.RelatedOccurrence != nil
	}
	return source.SourceShapeID == context.SourceShapeID
}

func constructionStepHasGroupFieldSource(step authoringv2.ConstructionStep) bool {
	if groupStepHasRowValueFieldSource(step) {
		return true
	}
	if step.Operation.Group == nil {
		return false
	}
	for _, key := range step.Operation.Group.Keys {
		if key.FieldSource != nil {
			return true
		}
	}
	return false
}

func groupStepHasRowValueFieldSource(step authoringv2.ConstructionStep) bool {
	if step.Operation.Kind != authoringv2.ConstructionOperationGroup {
		return false
	}
	for _, value := range step.RowValues {
		if value.FieldSource != nil {
			return true
		}
	}
	return false
}

func groupRowValueSourceForOutput(values []authoringv2.ConstructionRowValue, outputID string) *authoringv2.ConstructionRowValue {
	for index := range values {
		if values[index].OutputColumnID == outputID && values[index].FieldSource != nil {
			return &values[index]
		}
	}
	return nil
}

func constructionGroupInputStageID(step authoringv2.ConstructionStep) (string, bool) {
	if len(step.Inputs) != 1 {
		return "", false
	}
	switch step.Inputs[0].Kind {
	case authoringv2.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID, true
	case authoringv2.ConstructionInputStepOutput:
		return step.Inputs[0].StepID, true
	default:
		return "", false
	}
}

func receiptConstructionStageByID(stages []explorer.ReceiptConstructionStage, stageID string) *explorer.ReceiptConstructionStage {
	for index := range stages {
		if stages[index].ID == stageID {
			return &stages[index]
		}
	}
	return nil
}

func groupFieldSourceForOutput(keys []authoringv2.ConstructionGroupKey, outputID string) *authoringv2.ConstructionGroupKey {
	for index := range keys {
		if keys[index].OutputColumnID == outputID && keys[index].FieldSource != nil {
			return &keys[index]
		}
	}
	return nil
}

func groupFieldChoiceMatchesBinding(choice ConstructionGroupFieldChoice, binding authoringv2.ConstructionGroupFieldSource) bool {
	if choice.Kind != "FIELD_SOURCE" || choice.FieldSource == nil || choice.FieldSource.Kind != "CHOICE" {
		return false
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.FieldSource.ChoiceID)
	if err != nil || identity.Kind != capability.ConstructionChoiceSourceGroupField {
		return false
	}
	source, ok := identity.Source.(capability.GroupFieldChoiceSource)
	if !ok || source.SourceStageID != binding.SourceStageID || source.SourceShapeID != binding.SourceShapeID || source.ValueKind != binding.ValueKind {
		return false
	}
	resolved := constructionGroupFieldBinding(source)
	return resolved != nil && sameConstructionGroupFieldBinding(*resolved, binding)
}

func constructionGroupFieldBinding(source capability.GroupFieldChoiceSource) *authoringv2.ConstructionGroupFieldSource {
	var input *authoringv2.ConstructionGroupFieldSourceInput
	if source.Input != nil {
		switch source.Input.Kind {
		case "CURRENT_STAGE":
			if current := source.Input.CurrentStage; current != nil {
				input = &authoringv2.ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: &authoringv2.ConstructionGroupFieldCurrentStage{
					SourceRole: current.SourceRole, OccurrenceID: current.OccurrenceID,
					OwnerResourceType: current.OwnerResourceType, ResourceType: current.ResourceType,
					CandidateID: current.CandidateID, CanonicalPath: current.CanonicalPath,
					BoundarySelector: current.BoundarySelector,
				}}
			}
		case "RELATED_OCCURRENCE":
			if related := source.Input.RelatedOccurrence; related != nil {
				steps := make([]authoringv2.ConstructionGroupFieldRelatedRouteStep, len(related.Route))
				for index, step := range related.Route {
					steps[index] = authoringv2.ConstructionGroupFieldRelatedRouteStep{
						StepIndex: step.StepIndex, FromOccurrenceID: step.FromOccurrenceID, FromNodeID: step.FromNodeID,
						FromResourceType: step.FromResourceType, EdgeID: step.EdgeID, Relationship: step.Relationship,
						StorageDirection: step.StorageDirection, MatchMode: step.MatchMode,
						ToOccurrenceID: step.ToOccurrenceID, ToNodeID: step.ToNodeID, ToResourceType: step.ToResourceType,
					}
				}
				input = &authoringv2.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &authoringv2.ConstructionGroupFieldRelatedOccurrence{
					AnchorOccurrenceID: related.AnchorOccurrenceID, AnchorNodeID: related.AnchorNodeID, AnchorResourceType: related.AnchorResourceType,
					Route: steps, TargetOccurrenceID: related.TargetOccurrenceID, TargetNodeID: related.TargetNodeID,
					TargetResourceType: related.TargetResourceType, ResourceType: related.ResourceType,
					CandidateID: related.CandidateID, CanonicalPath: related.CanonicalPath,
					EmptyPolicy: related.EmptyPolicy, Multiplicity: related.Multiplicity,
					CodingSelector: related.CodingSelector, CodingEmptyPolicy: related.CodingEmptyPolicy,
					OrdinalSemantics: related.OrdinalSemantics,
				}}
			}
		}
	}
	if input == nil {
		return nil
	}
	return &authoringv2.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: source.ValueKind, SourceStageID: source.SourceStageID,
		SourceShapeID: source.SourceShapeID, Input: input,
	}
}

func sameConstructionGroupFieldBinding(left, right authoringv2.ConstructionGroupFieldSource) bool {
	if left.Kind != right.Kind || left.ValueKind != right.ValueKind || left.SourceStageID != right.SourceStageID ||
		left.SourceShapeID != right.SourceShapeID || left.Input == nil || right.Input == nil || left.Input.Kind != right.Input.Kind {
		return false
	}
	switch left.Input.Kind {
	case "CURRENT_STAGE":
		return reflect.DeepEqual(left.Input.CurrentStage, right.Input.CurrentStage)
	case "RELATED_OCCURRENCE":
		return reflect.DeepEqual(left.Input.RelatedOccurrence, right.Input.RelatedOccurrence)
	default:
		return false
	}
}

func constructionGroupFieldOwnerRoute(
	base constructionBase,
	context *explorer.ReceiptConstructionGroupFieldSourceContext,
	occurrenceID string,
) (capability.RowChoiceOccurrence, []capability.ConstructionRouteStep, error) {
	occurrence, err := resolveRowChoiceOccurrence(base.snapshot, base.document.RootResourceType, base.document.Route, occurrenceID, "")
	if err != nil {
		return capability.RowChoiceOccurrence{}, nil, err
	}
	if context.SourceExpansion != nil && occurrenceID == context.SourceExpansion.OwnerOccurrenceID {
		if !compiledGroupRouteMatches(base.document.Route, occurrenceID, context.SourceExpansion.Route, occurrence.Route) {
			return capability.RowChoiceOccurrence{}, nil, fmt.Errorf("compiled owner route differs from the saved exact occurrence route")
		}
	}
	return occurrence, occurrence.Route, nil
}

func compiledGroupRouteMatches(
	root authoringv2.RouteNode,
	occurrenceID string,
	compiled []explorer.ReceiptConstructionGroupFieldSourceRouteStep,
	resolved []capability.ConstructionRouteStep,
) bool {
	path, found := routePathToOccurrence(root, occurrenceID)
	if !found || len(path) == 0 || len(path)-1 != len(compiled) || len(compiled) != len(resolved) {
		return false
	}
	for index, fact := range compiled {
		parent, child := path[index], path[index+1]
		step := resolved[index]
		if fact.OccurrenceID != child.OccurrenceID || fact.FromType != parent.ResourceType || fact.EdgeLabel != child.Relationship ||
			fact.ToType != child.ResourceType || !strings.EqualFold(fact.Direction, step.StorageDirection) ||
			step.FromResourceType != fact.FromType || step.Relationship != fact.EdgeLabel || step.ToResourceType != fact.ToType {
			return false
		}
	}
	return true
}

func groupFieldPathForOwner(
	owner explorer.ReceiptConstructionGroupFieldOwner,
	expansion *explorer.ReceiptConstructionGroupFieldSourceExpansion,
	candidatePath string,
) (string, bool) {
	path := canonicalRowPath(candidatePath)
	switch owner.Role {
	case "ROOT", "OWNER":
		return path, path != ""
	case "ANCESTOR":
		return stripGroupFieldPathPrefix(path, owner.BoundarySelector)
	case "CURRENT_ITEM":
		if expansion == nil {
			return "", false
		}
		return stripGroupFieldPathPrefix(path, expansion.Selector)
	default:
		return "", false
	}
}

func groupConsumedFieldBoundaries(
	expansion *explorer.ReceiptConstructionGroupFieldSourceExpansion,
	owner explorer.ReceiptConstructionGroupFieldOwner,
) ([]string, bool) {
	switch owner.Role {
	case "ROOT", "OWNER":
		return nil, true
	case "ANCESTOR":
		if expansion == nil || expansion.OwnerOccurrenceID != owner.OccurrenceID || expansion.OwnerResourceType != owner.OwnerResourceType {
			return nil, false
		}
		result := make([]string, 0, len(expansion.Ancestors))
		previousStepIndex := -1
		found := false
		for _, ancestor := range expansion.Ancestors {
			boundary := canonicalRowPath(ancestor.BoundarySelector)
			if ancestor.StepIndex <= previousStepIndex || boundary == "" || boundary != ancestor.BoundarySelector {
				return nil, false
			}
			previousStepIndex = ancestor.StepIndex
			if ancestor.StepIndex > owner.StepIndex {
				break
			}
			if ancestor.StepIndex == owner.StepIndex {
				if ancestor.BoundarySelector != owner.BoundarySelector || ancestor.ResourceType != owner.ResourceType {
					return nil, false
				}
				found = true
			}
			result = append(result, ancestor.BoundarySelector)
			if found {
				break
			}
		}
		return result, found
	case "CURRENT_ITEM":
		if expansion == nil || expansion.OwnerOccurrenceID != owner.OccurrenceID || expansion.OwnerResourceType != owner.OwnerResourceType {
			return nil, false
		}
		result := make([]string, 0, len(expansion.Ancestors)+1)
		previousStepIndex := -1
		for _, ancestor := range expansion.Ancestors {
			boundary := canonicalRowPath(ancestor.BoundarySelector)
			if ancestor.StepIndex <= previousStepIndex || boundary == "" || boundary != ancestor.BoundarySelector {
				return nil, false
			}
			previousStepIndex = ancestor.StepIndex
			result = append(result, ancestor.BoundarySelector)
		}
		selector := canonicalRowPath(expansion.Selector)
		if selector == "" || selector != expansion.Selector {
			return nil, false
		}
		result = append(result, expansion.Selector)
		return result, true
	default:
		return nil, false
	}
}

type constructionGroupRelatedAnchor struct {
	occurrenceID string
	nodeID       string
	resourceType string
	rootRoute    []capability.ConstructionRouteStep
}

func constructionGroupRelatedAnchorForStage(
	base constructionBase,
	context *explorer.ReceiptConstructionGroupFieldSourceContext,
) (constructionGroupRelatedAnchor, error) {
	if context == nil {
		return constructionGroupRelatedAnchor{}, fmt.Errorf("Group source context is unavailable")
	}
	if expansion := context.SourceExpansion; expansion != nil {
		occurrence, route, err := constructionGroupFieldOwnerRoute(base, context, expansion.OwnerOccurrenceID)
		if err != nil {
			return constructionGroupRelatedAnchor{}, fmt.Errorf("expanded Group anchor no longer matches its exact owner occurrence: %w", err)
		}
		if occurrence.ResourceType != expansion.OwnerResourceType {
			return constructionGroupRelatedAnchor{}, fmt.Errorf("expanded Group anchor resource type changed")
		}
		return constructionGroupRelatedAnchor{
			occurrenceID: expansion.OwnerOccurrenceID, nodeID: occurrence.NodeID,
			resourceType: expansion.OwnerResourceType, rootRoute: route,
		}, nil
	}
	rootOccurrence := base.document.Route.OccurrenceID
	if rootOccurrence == "" || base.document.Route.ResourceType != base.document.RootResourceType {
		return constructionGroupRelatedAnchor{}, fmt.Errorf("document has no exact compiler-rooted source occurrence")
	}
	occurrence, err := resolveRowChoiceOccurrence(base.snapshot, base.document.RootResourceType, base.document.Route, rootOccurrence, "")
	if err != nil {
		return constructionGroupRelatedAnchor{}, fmt.Errorf("source root occurrence no longer resolves to one authorized row root: %w", err)
	}
	if len(occurrence.Route) != 0 {
		return constructionGroupRelatedAnchor{}, fmt.Errorf("source root occurrence unexpectedly carries a route")
	}
	return constructionGroupRelatedAnchor{
		occurrenceID: rootOccurrence, nodeID: occurrence.NodeID,
		resourceType: base.document.RootResourceType, rootRoute: occurrence.Route,
	}, nil
}

func populatedGroupScalarCandidate(candidate capability.Candidate, index *fhirschema.Index) (fhirschema.RowPathFacts, bool) {
	if index == nil || !candidate.Observed || !candidate.Populated || candidate.NodeID == "" || candidate.ResourceType == "" ||
		candidate.FieldPath == "" || fhirNonGroupableGroupFieldPath(candidate.FieldPath) || !capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return fhirschema.RowPathFacts{}, false
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(candidate.ResourceType), candidate.FieldPath)
	if err != nil || facts.CanonicalPath != candidate.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
		return fhirschema.RowPathFacts{}, false
	}
	return facts, true
}

func observedGroupCodingSelector(candidate capability.Candidate, nodeID, resourceType, selector string) bool {
	if !candidate.Observed || !candidate.Populated || candidate.NodeID != nodeID || candidate.ResourceType != resourceType ||
		canonicalRowPath(selector) == "" || canonicalRowPath(selector) != selector {
		return false
	}
	fieldPath := canonicalRowPath(candidate.FieldPath)
	for _, boundary := range candidate.RepeatedBoundaries {
		if canonicalRowPath(boundary.Path) != selector {
			continue
		}
		if fieldPath == selector || strings.HasPrefix(fieldPath, selector+".") {
			return true
		}
	}
	return false
}

func stripGroupFieldPathPrefix(path, prefix string) (string, bool) {
	path, prefix = canonicalRowPath(path), canonicalRowPath(prefix)
	if path == "" || prefix == "" || !strings.HasPrefix(path, prefix+".") {
		return "", false
	}
	relative := strings.TrimPrefix(path, prefix+".")
	return relative, relative != ""
}

func groupSourceSelector(expansion *explorer.ReceiptConstructionGroupFieldSourceExpansion) string {
	if expansion == nil {
		return ""
	}
	return expansion.Selector
}

func equalGroupFieldRoutes(left, right []capability.ConstructionRouteStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func groupFieldScopeLabel(owner explorer.ReceiptConstructionGroupFieldOwner, expansion *explorer.ReceiptConstructionGroupFieldSourceExpansion) string {
	parts := []string{owner.OwnerResourceType}
	if owner.Role == "ANCESTOR" {
		parts = append(parts, owner.ResourceType, owner.BoundarySelector)
	} else if owner.Role == "CURRENT_ITEM" && expansion != nil {
		parts = append(parts, owner.ResourceType, expansion.Selector)
	} else if owner.Role == "OWNER" {
		parts = append(parts, "owner")
	} else {
		parts = append(parts, "root")
	}
	return strings.Join(parts, " · ")
}

func matchedPublicGroupFieldColumn(
	document authoringv2.Document,
	stage explorer.ReceiptConstructionStage,
	occurrenceID, candidatePath string,
) string {
	stageIDs := make(map[string]bool, len(stage.Columns))
	for _, column := range stage.Columns {
		stageIDs[column.ID] = true
	}
	var match string
	for _, column := range document.Columns {
		if column.ColumnID == "" || !stageIDs[column.ColumnID] || column.OccurrenceID != occurrenceID ||
			canonicalRowPath(column.Source.FieldPath()) != canonicalRowPath(candidatePath) {
			continue
		}
		if match != "" {
			return ""
		}
		match = column.ColumnID
	}
	return match
}
