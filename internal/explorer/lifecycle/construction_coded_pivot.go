package lifecycle

import (
	"context"
	"fmt"
	"reflect"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) resolveConstructionCodedPivotChoices(
	ctx context.Context,
	base constructionBase,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	verified := make(map[string]bool)
	priorByStep := make(map[string]authoringv2.ConstructionCodedPivot)
	for _, step := range base.construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationCodedPivot && step.Operation.CodedPivot != nil {
			priorByStep[step.ID] = *step.Operation.CodedPivot
		}
	}
	for stepIndex := range candidate.Steps {
		step := &candidate.Steps[stepIndex]
		if step.Operation.Kind != authoringv2.ConstructionOperationCodedPivot || step.Operation.CodedPivot == nil {
			continue
		}
		coded := step.Operation.CodedPivot
		if coded.SourceChoiceID == "" {
			continue
		}
		if len(step.Inputs) != 1 || step.Inputs[0].Kind != authoringv2.ConstructionInputSourceProjection || step.Inputs[0].StepID != "" {
			return candidate, nil, unprocessable("construction-proposal", "DIRECT_SOURCE_STAGE_REQUIRED", "coded Pivot currently requires the direct source projection stage", nil)
		}
		inputStage := constructionSourceStage(base.stages)
		if inputStage == nil || inputStage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedPivot(*inputStage) {
			return candidate, nil, unprocessable("construction-proposal", "CODED_PIVOT_UNAVAILABLE", "the direct source stage does not support coded Pivot", nil)
		}
		identity, err := capability.DecodeConstructionChoiceID(coded.SourceChoiceID)
		if err != nil || identity.SnapshotToken != base.snapshot.Token {
			return candidate, nil, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the coded source for the current authorized snapshot", nil, err)
		}
		family, route, err := s.reauthorizeFrameSourceChoice(ctx, base.snapshot.Identity.Project, base.explorerID, base.document.RootResourceType, outputID, coded.SourceChoiceID, base.authorized, identity)
		if err != nil {
			return candidate, nil, err
		}
		if len(route) != 0 || family.Family.ResourceType != base.document.RootResourceType {
			return candidate, nil, unprocessable("construction-proposal", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
		}
		selectedSource, ok := identity.Source.(capability.SemanticFrameChoiceSource)
		if !ok {
			return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "sourceChoiceId does not identify a semantic coded family", nil)
		}
		durableSource := &authoringv2.ConstructionCodedPivotSource{
			Family: family.Family, CandidateID: family.Candidate.ID,
			NodeID: family.Candidate.NodeID, FieldPath: family.Candidate.FieldPath,
			Route: append([]capability.ConstructionRouteStep(nil), route...),
		}
		if existing, exists := priorByStep[step.ID]; exists && existing.Source != nil && !reflect.DeepEqual(existing.Source, durableSource) {
			return candidate, nil, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected family differs from the saved coded Pivot source", nil, nil)
		}
		prior, priorExists := priorByStep[step.ID]
		priorKeys := make(map[string]bool, len(prior.Categories))
		for _, category := range prior.Categories {
			priorKeys[category.System+"\x00"+category.Code] = true
		}
		resolvedCategories := make([]authoringv2.ConstructionCodedPivotCategory, 0, len(coded.Categories))
		seenKeys, seenOutputs := map[string]bool{}, map[string]bool{}
		for categoryIndex, category := range coded.Categories {
			if category.ChoiceID != "" {
				entry, categoryCandidate, choiceErr := s.resolveCodedPivotCategoryChoice(ctx, base, selectedSource, identity, route, category.ChoiceID)
				if choiceErr != nil {
					return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", fmt.Sprintf("categories[%d]: %v", categoryIndex, choiceErr), choiceErr)
				}
				_ = categoryCandidate
				category.System, category.Code = entry.Observation.Key.System, entry.Observation.Key.Code
				category.ChoiceID = ""
			} else {
				key := category.System + "\x00" + category.Code
				if !priorExists || !priorKeys[key] || prior.Source == nil || !reflect.DeepEqual(prior.Source, durableSource) {
					return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", fmt.Sprintf("categories[%d] durable coded key is not retained from the accepted step", categoryIndex), nil)
				}
			}
			key := category.System + "\x00" + category.Code
			if seenKeys[key] || seenOutputs[category.OutputColumnID] {
				return candidate, nil, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", "coded Pivot categories contain duplicate keys or outputs", nil)
			}
			seenKeys[key], seenOutputs[category.OutputColumnID] = true, true
			resolvedCategories = append(resolvedCategories, category)
		}
		coded.SourceChoiceID = ""
		coded.Source = durableSource
		coded.Categories = resolvedCategories
		verified[step.ID] = true
	}
	return candidate, verified, nil
}

func (s *Service) resolveCodedPivotCategoryChoice(
	ctx context.Context,
	base constructionBase,
	frame capability.SemanticFrameChoiceSource,
	frameIdentity capability.ConstructionChoiceIdentity,
	route []capability.ConstructionRouteStep,
	choiceID string,
) (catalog.SemanticInventoryEntry, capability.Candidate, error) {
	identity, err := capability.DecodeConstructionChoiceID(choiceID)
	if err != nil || identity.SnapshotToken != base.snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemantic {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload this coded category for the current snapshot", nil, err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.Version != "" || source.BindingID != frame.Family.BindingID || source.ResourceType != frame.Family.ResourceType ||
		source.SourcePath != frame.Family.SourcePath || source.SourceCanonical != frame.Family.SourceCanonical || source.SourceProfile != frame.Family.SourceProfile ||
		source.OwningScope != frame.Family.OwningScope || source.ValueSelector != frame.Family.ValuePath || source.LogicalType != frame.Family.LogicalType ||
		!reflect.DeepEqual(identity.Route, route) {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "category choice does not belong to the selected coded family and route", nil)
	}
	if s.config.ResolveSemanticInventorySelections == nil {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "semantic inventory reauthorization is not configured", nil)
	}
	unrestricted := base.authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: projectid.Legacy(base.snapshot.Identity.Project), DatasetGeneration: base.snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), base.authorized.Scope.AuthResourcePaths...),
		References: []catalog.SemanticInventoryReference{{ConceptID: source.ConceptID, BindingID: source.BindingID}},
	})
	if err != nil {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "the selected coded category could not be reauthorized", err)
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(base.snapshot.Identity.Project), base.snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete ||
		resolved.Build.BuildID != expectedBuildID || frameIdentity.BuildID != expectedBuildID || identity.BuildID != expectedBuildID || len(resolved.Entries) != 1 {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the semantic inventory for this coded family changed", nil, nil)
	}
	entry := resolved.Entries[0]
	observation := entry.Observation
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if entry.ConceptID != source.ConceptID || !plan.Readiness.Addable() || observation.Key.Version != "" ||
		!semanticEntryMatchesFrameFamily(entry, observation, plan, frame.Family) {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the selected category is no longer a versionless member of this coded family", nil)
	}
	candidate, ok := semanticConstructionCandidate(base.snapshot, observation)
	if !ok || candidate.ID != source.CandidateID || candidate.NodeID != source.NodeID || candidate.FieldPath != source.FieldPath ||
		candidate.ID != frameIdentityCandidateID(frameIdentity) {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected category no longer matches its compiler-proved value field", nil, nil)
	}
	contextToken, err := semanticInventoryContextToken(base.snapshot, base.explorerID, base.document.RootResourceType, identity.BuildID)
	if err != nil || identity.SemanticContextToken != contextToken {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "reload coded categories for the current table root", nil, err)
	}
	expected, err := semanticInventoryConstructionChoiceForRoute(base.snapshot, contextToken, identity.BuildID, route, entry, candidate, false)
	if err != nil || !constructionChoiceIdentityMatches(expected, choiceID, identity) || !reflect.DeepEqual(expected.Source, source) {
		return catalog.SemanticInventoryEntry{}, capability.Candidate{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the coded category token does not match current semantic inventory evidence", err)
	}
	return entry, candidate, nil
}

func frameIdentityCandidateID(identity capability.ConstructionChoiceIdentity) string {
	source, ok := identity.Source.(capability.SemanticFrameChoiceSource)
	if !ok {
		return ""
	}
	return source.CandidateID
}

func constructionStageSupportsCodedPivot(stage explorer.ReceiptConstructionStage) bool {
	if stage.ID != recipe.ConstructionSourceProjectionID {
		return false
	}
	for _, choice := range stage.Capabilities {
		if choice.Kind == string(recipe.ConstructionCodedPivotOp) {
			return choice.Supported
		}
	}
	return false
}

func constructionSourceStage(stages []explorer.ReceiptConstructionStage) *explorer.ReceiptConstructionStage {
	for index := range stages {
		if stages[index].ID == recipe.ConstructionSourceProjectionID {
			return &stages[index]
		}
	}
	return nil
}
