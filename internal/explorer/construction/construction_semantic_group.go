package construction

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

const (
	constructionSemanticGroupChoiceLimit     = 50
	constructionSemanticGroupChoicePageLimit = 5
)

func (s *Service) constructionSemanticGroupChoices(
	ctx context.Context,
	base Base,
	outputID string,
	stage explorer.ReceiptConstructionStage,
) ([]ConstructionSemanticGroupChoice, bool, error) {
	choices := []ConstructionSemanticGroupChoice{}
	if importedSourceRootChoiceInventoryMissing(base) {
		return choices, false, nil
	}
	if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedPivot(stage) ||
		!constructionStageSupportsGroup(stage) || base.Document.Route.OccurrenceID != authoringv2.RootOccurrenceID ||
		s.config.SemanticInventory == nil {
		return choices, false, nil
	}
	unrestricted := base.Authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	pageOptions := semantic.PageOptions{
		Project: projectid.Legacy(base.Snapshot.Identity.Project), DatasetGeneration: base.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), base.Authorized.Scope.AuthResourcePaths...),
		ResourceType: base.Document.RootResourceType, SelectionMode: semantic.SystemVersionCategory,
		Limit: constructionSemanticGroupChoiceLimit,
	}
	expectedBuildID := semantic.BuildID(projectid.Legacy(base.Snapshot.Identity.Project), base.Snapshot.Identity.Generation)
	contextToken := ""
	seenFamilies := map[string]struct{}{}
	cursor := ""
	for pageNumber := 0; pageNumber < constructionSemanticGroupChoicePageLimit && len(choices) < constructionSemanticGroupChoiceLimit; pageNumber++ {
		pageOptions.Cursor = cursor
		page, err := s.config.SemanticInventory(ctx, pageOptions)
		if err != nil {
			return nil, false, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "semantic inventory could not be read for semantic Group choices", err)
		}
		if page.State != semantic.Complete || page.Build.State != semantic.Complete || page.Build.BuildID != expectedBuildID {
			return []ConstructionSemanticGroupChoice{}, false, nil
		}
		if contextToken == "" {
			contextToken, err = semanticInventoryContextToken(base.Snapshot, base.ExplorerID, base.Document.RootResourceType, page.Build.BuildID)
			if err != nil {
				return nil, false, internal("construction-proposal", "SEMANTIC_CONTEXT_FAILED", "the semantic inventory context token could not be built", err)
			}
		}
		for _, family := range page.Families {
			if _, seen := seenFamilies[family.FamilyID]; seen || family.FamilyID == "" {
				continue
			}
			seenFamilies[family.FamilyID] = struct{}{}
			codingPath, ok := constructionSemanticGroupCodingPath(base.Document.RootResourceType, family)
			if !ok || !semanticGroupFamilyReady(family) {
				continue
			}
			entry := semanticInventoryEntryForFamily(family)
			candidate, ok := semanticConstructionCandidate(base.Snapshot, entry.Observation)
			if !ok {
				continue
			}
			choice, err := semanticCategoryConstructionChoiceForRoute(base.Snapshot, contextToken, page.Build.BuildID, nil, family, candidate)
			if err != nil {
				continue
			}
			source, ok := choice.Source.(capability.SemanticBindingChoiceSource)
			if !ok {
				continue
			}
			source.GroupOutputID, source.GroupStageID = outputID, stage.ID
			choice, err = capability.NewSemanticConstructionChoiceForRoute(
				base.Snapshot.Token, contextToken, page.Build.BuildID, nil, source, candidate,
			)
			if err != nil {
				continue
			}
			fieldPath := strings.TrimSuffix(codingPath, ".coding[]")
			fieldLabel := constructionGroupFieldLabel(fieldPath)
			if fieldLabel != "" {
				fieldLabel = strings.ToLower(fieldLabel[:1]) + fieldLabel[1:]
			}
			label := strings.TrimSpace(family.ResourceType + " " + fieldLabel)
			sourceLabel := "retained records"
			if family.SourceKind == semantic.SourceFile {
				sourceLabel = "source file"
			}
			scopeLabel := fmt.Sprintf("%s · %s.%s · %s", sourceLabel, family.ResourceType, codingPath, family.System)
			if family.Version == "" {
				scopeLabel += " · versionless"
			} else {
				scopeLabel += " · version " + family.Version
			}
			choices = append(choices, ConstructionSemanticGroupChoice{
				ChoiceID: choice.ChoiceID, BindingID: family.BindingID, RuleVersion: family.RuleVersion,
				Label: label, ScopeLabel: scopeLabel, ResourceType: family.ResourceType, SourcePath: codingPath,
			})
			if len(choices) == constructionSemanticGroupChoiceLimit {
				break
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	incomplete := cursor != ""
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Label != choices[j].Label {
			return choices[i].Label < choices[j].Label
		}
		if choices[i].ScopeLabel != choices[j].ScopeLabel {
			return choices[i].ScopeLabel < choices[j].ScopeLabel
		}
		return choices[i].ChoiceID < choices[j].ChoiceID
	})
	return choices, incomplete, nil
}

func constructionStageSupportsGroup(stage explorer.ReceiptConstructionStage) bool {
	for _, operation := range stage.Capabilities {
		if operation.Kind == string(recipe.ConstructionGroupOp) {
			return operation.Supported
		}
	}
	return false
}

func semanticGroupFamilyReady(family semantic.Family) bool {
	if family.BindingID == "" || family.FamilyID == "" || family.System == "" || family.RuleVersion == "" ||
		family.SchemaVersion <= 0 || family.Population <= 0 || family.Completeness != semantic.CompletenessComplete || family.Status != "SUPPORTED" {
		return false
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(semanticInventoryEntryForFamily(family).Observation)
	return plan.Readiness.Addable() && plan.Source != nil && plan.Source.Lookup != nil && plan.Source.Lookup.Binding != nil && plan.Source.Lookup.Key != nil
}

var semanticGroupCodingPathSets sync.Map

func constructionSemanticGroupCodingPath(rootResourceType string, family semantic.Family) (string, bool) {
	if family.ResourceType != rootResourceType || family.Source.Type != rootResourceType || family.Source.Path == "" {
		return "", false
	}
	path := strings.TrimPrefix(family.Source.Path, rootResourceType+".")
	pathSet, err := constructionSemanticGroupCodingPathSet(rootResourceType)
	if err != nil {
		return "", false
	}
	return path, pathSet[path]
}

func constructionSemanticGroupCodingPathSet(rootResourceType string) (map[string]bool, error) {
	if cached, ok := semanticGroupCodingPathSets.Load(rootResourceType); ok {
		return cached.(map[string]bool), nil
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, err
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(rootResourceType))
	if err != nil {
		return nil, err
	}
	pathSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		facts, resolveErr := index.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
		pathSet[path] = resolveErr == nil && facts.CanonicalPath == path && facts.FHIRType == "Coding" &&
			facts.Cardinality == fhirschema.RowCardinalityMany && facts.Shape == fhirschema.RowPathArray && !facts.Reference
	}
	actual, _ := semanticGroupCodingPathSets.LoadOrStore(rootResourceType, pathSet)
	return actual.(map[string]bool), nil
}

func (s *Service) resolveConstructionGroupSemanticCategories(
	ctx context.Context,
	base Base,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	priorByStep := make(map[string][]authoringv2.ConstructionGroupKey)
	for _, step := range base.Construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationGroup && step.Operation.Group != nil {
			priorByStep[step.ID] = append([]authoringv2.ConstructionGroupKey(nil), step.Operation.Group.Keys...)
		}
	}
	for stepIndex := range candidate.Steps {
		step := &candidate.Steps[stepIndex]
		if step.Operation.Kind != authoringv2.ConstructionOperationGroup || step.Operation.Group == nil {
			continue
		}
		group := step.Operation.Group
		semanticCount := 0
		for keyIndex := range group.Keys {
			key := &group.Keys[keyIndex]
			if key.SemanticCategory == nil {
				continue
			}
			semanticCount++
			if len(step.Inputs) != 1 || step.Inputs[0].Kind != authoringv2.ConstructionInputSourceProjection || step.Inputs[0].StepID != "" {
				return candidate, unprocessable("construction-proposal", "DIRECT_SOURCE_STAGE_REQUIRED", "semantic Group keys currently require the direct source projection stage", nil)
			}
			stage := constructionSourceStage(base.Stages)
			if stage == nil || stage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedPivot(*stage) || !constructionStageSupportsGroup(*stage) {
				return candidate, unprocessable("construction-proposal", "SEMANTIC_GROUP_UNAVAILABLE", "the direct source stage does not support semantic Group keys", nil)
			}
			prior := semanticBindingForGroupKey(priorByStep[step.ID], key.OutputColumnID)
			resolved := key.SemanticCategory
			switch resolved.Kind {
			case "CHOICE":
				binding, err := s.resolveConstructionSemanticGroupChoice(ctx, base, outputID, stage.ID, resolved.ChoiceID)
				if err != nil {
					return candidate, err
				}
				*resolved = binding
			case "BINDING":
				if prior == nil || prior.SemanticCategory == nil || !reflect.DeepEqual(*prior.SemanticCategory, *resolved) {
					return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "a new semantic Group key requires a current stage choice", nil)
				}
				if err := s.reauthorizeConstructionSemanticGroupBinding(ctx, base, *resolved); err != nil {
					return candidate, err
				}
			default:
				return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "semantic Group key must be a current choice or a retained binding", nil)
			}
		}
		if semanticCount > 1 {
			return candidate, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", "Group supports at most one semantic category key", nil)
		}
	}
	return candidate, nil
}

func semanticBindingForGroupKey(keys []authoringv2.ConstructionGroupKey, outputColumnID string) *authoringv2.ConstructionGroupKey {
	for index := range keys {
		if keys[index].OutputColumnID == outputColumnID && keys[index].SemanticCategory != nil {
			return &keys[index]
		}
	}
	return nil
}

func (s *Service) resolveConstructionSemanticGroupChoice(
	ctx context.Context,
	base Base,
	outputID, stageID, choiceID string,
) (authoringv2.ConstructionGroupSemanticCategory, error) {
	identity, err := capability.DecodeConstructionChoiceID(choiceID)
	if err != nil || identity.SnapshotToken != base.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemantic {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the semantic Group field for the current snapshot", nil, err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.GroupOutputID != outputID || source.GroupStageID != stageID || len(identity.Route) != 0 ||
		source.RuleHint != semantic.RuleHintCodedCategoryV1 {
		return authoringv2.ConstructionGroupSemanticCategory{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the semantic Group choice does not belong to this exact output stage", nil)
	}
	family, err := s.reauthorizeConstructionSemanticGroupFamily(ctx, base, source.BindingID, source.ConceptID, source.SourceKind, source.ResourceType, source.SourcePath, source.System, source.Version, source.RuleVersion)
	if err != nil {
		return authoringv2.ConstructionGroupSemanticCategory{}, err
	}
	codingPath, ok := constructionSemanticGroupCodingPath(base.Document.RootResourceType, family)
	if !ok || codingPath != strings.TrimPrefix(family.Source.Path, base.Document.RootResourceType+".") {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected semantic family no longer maps to a generated root Coding path", nil, nil)
	}
	candidate, ok := semanticConstructionCandidate(base.Snapshot, semanticInventoryEntryForFamily(family).Observation)
	if !ok || candidate.ID != source.CandidateID || candidate.NodeID != source.NodeID || candidate.FieldPath != source.FieldPath {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the semantic family no longer has its exact compiler-proved field", nil, nil)
	}
	buildID := semantic.BuildID(projectid.Legacy(base.Snapshot.Identity.Project), base.Snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(base.Snapshot, base.ExplorerID, base.Document.RootResourceType, buildID)
	if err != nil {
		return authoringv2.ConstructionGroupSemanticCategory{}, internal("construction-proposal", "SEMANTIC_CONTEXT_FAILED", "the semantic inventory context token could not be built", err)
	}
	if identity.SemanticContextToken != contextToken || identity.BuildID != buildID {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "reload semantic Group choices for the current inventory", nil, nil)
	}
	entry := semanticInventoryEntryForFamily(family)
	expected, err := semanticCategoryConstructionChoiceForRoute(base.Snapshot, contextToken, buildID, nil, family, candidate)
	if err == nil {
		semanticSource, sourceOK := expected.Source.(capability.SemanticBindingChoiceSource)
		if sourceOK {
			semanticSource.GroupOutputID, semanticSource.GroupStageID = outputID, stageID
			expected, err = capability.NewSemanticConstructionChoiceForRoute(base.Snapshot.Token, contextToken, buildID, nil, semanticSource, candidate)
		}
	}
	if err != nil || expected.ChoiceID != choiceID || identity.BuildID != buildID || entry.BindingID != family.BindingID {
		return authoringv2.ConstructionGroupSemanticCategory{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the semantic Group choice cannot be reissued from current inventory evidence", err)
	}
	return constructionGroupSemanticBinding(family, codingPath), nil
}

func (s *Service) reauthorizeConstructionSemanticGroupBinding(
	ctx context.Context,
	base Base,
	binding authoringv2.ConstructionGroupSemanticCategory,
) error {
	family, err := s.reauthorizeConstructionSemanticGroupFamily(ctx, base, binding.BindingID, binding.FamilyID,
		binding.SourceKind, binding.ResourceType, binding.CodingPath, binding.System, binding.Version, binding.RuleVersion)
	if err != nil {
		return err
	}
	path, ok := constructionSemanticGroupCodingPath(base.Document.RootResourceType, family)
	if !ok || path != binding.CodingPath || family.FamilyID != binding.FamilyID || family.System != binding.System || family.Version != binding.Version {
		return conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the saved semantic Group family no longer matches current generated FHIR metadata", nil, nil)
	}
	return nil
}

func (s *Service) reauthorizeConstructionSemanticGroupFamily(
	ctx context.Context,
	base Base,
	bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion string,
) (semantic.Family, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return semantic.Family{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "semantic inventory reauthorization is not configured", nil)
	}
	unrestricted := base.Authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	path := strings.TrimPrefix(sourcePath, resourceType+".")
	reference := semantic.FamilyReference{
		FamilyID: familyID, BindingID: bindingID, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: path, System: system, Version: version,
	}
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(base.Snapshot.Identity.Project), DatasetGeneration: base.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), base.Authorized.Scope.AuthResourcePaths...),
		FamilyReferences: []semantic.FamilyReference{reference},
	})
	if err != nil {
		return semantic.Family{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "the selected semantic Group family could not be reauthorized", err)
	}
	expectedBuildID := semantic.BuildID(projectid.Legacy(base.Snapshot.Identity.Project), base.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete ||
		resolved.Build.BuildID != expectedBuildID || len(resolved.Families) != 1 {
		return semantic.Family{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the semantic inventory family changed; reload Group choices", nil, nil)
	}
	family := resolved.Families[0]
	if family.FamilyID != familyID || family.BindingID != bindingID || family.ResourceType != resourceType ||
		family.SourceKind != sourceKind || family.System != system || family.Version != version || family.RuleVersion != ruleVersion ||
		strings.TrimPrefix(family.Source.Path, resourceType+".") != path || !semanticGroupFamilyReady(family) {
		return semantic.Family{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the semantic inventory family no longer matches the saved Group binding", nil, nil)
	}
	return family, nil
}

func constructionGroupSemanticBinding(family semantic.Family, codingPath string) authoringv2.ConstructionGroupSemanticCategory {
	return authoringv2.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: family.BindingID, RuleVersion: family.RuleVersion, FamilyID: family.FamilyID, SourceKind: family.SourceKind,
		System: family.System, Version: family.Version, ResourceType: family.ResourceType, CodingPath: codingPath,
	}
}
