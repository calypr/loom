package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

const (
	constructionSemanticGroupChoiceLimit     = 50
	constructionSemanticGroupChoicePageLimit = 5
)

// ConstructionSemanticGroupChoice is an ephemeral, exact family choice for
// the selected Group stage. The durable operation stores a resolved binding.
type ConstructionSemanticGroupChoice struct {
	ChoiceID     string `json:"choiceId"`
	BindingID    string `json:"bindingId"`
	RuleVersion  string `json:"ruleVersion"`
	Label        string `json:"label"`
	ScopeLabel   string `json:"scopeLabel"`
	ResourceType string `json:"resourceType"`
	SourcePath   string `json:"sourcePath"`
}

func (s *Service) constructionSemanticGroupChoices(
	ctx context.Context,
	base constructionBase,
	outputID string,
	stage explorer.ReceiptConstructionStage,
) ([]ConstructionSemanticGroupChoice, bool, error) {
	choices := []ConstructionSemanticGroupChoice{}
	if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedPivot(stage) ||
		!constructionStageSupportsGroup(stage) || base.document.Route.OccurrenceID != authoringv2.RootOccurrenceID ||
		s.config.SemanticInventory == nil {
		return choices, false, nil
	}
	unrestricted := base.authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	pageOptions := catalog.SemanticInventoryPageOptions{
		Project: projectid.Legacy(base.snapshot.Identity.Project), DatasetGeneration: base.snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), base.authorized.Scope.AuthResourcePaths...),
		ResourceType: base.document.RootResourceType, SelectionMode: catalog.SemanticInventorySystemVersionCategory,
		Limit: constructionSemanticGroupChoiceLimit,
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(base.snapshot.Identity.Project), base.snapshot.Identity.Generation)
	contextToken := ""
	seenFamilies := map[string]struct{}{}
	cursor := ""
	for pageNumber := 0; pageNumber < constructionSemanticGroupChoicePageLimit && len(choices) < constructionSemanticGroupChoiceLimit; pageNumber++ {
		pageOptions.Cursor = cursor
		page, err := s.config.SemanticInventory(ctx, pageOptions)
		if err != nil {
			return nil, false, fmt.Errorf("read exact semantic grouping families: %w", err)
		}
		if page.State != catalog.SemanticInventoryComplete || page.Build.State != catalog.SemanticInventoryComplete || page.Build.BuildID != expectedBuildID {
			return []ConstructionSemanticGroupChoice{}, false, nil
		}
		if contextToken == "" {
			contextToken, err = semanticInventoryContextToken(base.snapshot, base.explorerID, base.document.RootResourceType, page.Build.BuildID)
			if err != nil {
				return nil, false, err
			}
		}
		for _, family := range page.Families {
			if _, seen := seenFamilies[family.FamilyID]; seen || family.FamilyID == "" {
				continue
			}
			seenFamilies[family.FamilyID] = struct{}{}
			codingPath, ok := constructionSemanticGroupCodingPath(base.document.RootResourceType, family)
			if !ok || !semanticGroupFamilyReady(family) {
				continue
			}
			entry := semanticInventoryEntryForFamily(family)
			candidate, ok := semanticConstructionCandidate(base.snapshot, entry.Observation)
			if !ok {
				continue
			}
			choice, err := semanticCategoryConstructionChoiceForRoute(base.snapshot, contextToken, page.Build.BuildID, nil, family, candidate)
			if err != nil {
				continue
			}
			source, ok := choice.Source.(capability.SemanticBindingChoiceSource)
			if !ok {
				continue
			}
			source.GroupOutputID, source.GroupStageID = outputID, stage.ID
			choice, err = capability.NewSemanticConstructionChoiceForRoute(
				base.snapshot.Token, contextToken, page.Build.BuildID, nil, source, candidate,
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
			if family.SourceKind == catalog.SemanticInventorySourceFile {
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

func semanticGroupFamilyReady(family catalog.SemanticInventoryFamily) bool {
	if family.BindingID == "" || family.FamilyID == "" || family.System == "" || family.RuleVersion == "" ||
		family.SchemaVersion <= 0 || family.Population <= 0 || family.Completeness != catalog.SemanticComplete || family.Status != "SUPPORTED" {
		return false
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(semanticInventoryEntryForFamily(family).Observation)
	return plan.Readiness.Addable() && plan.Source != nil && plan.Source.Lookup != nil && plan.Source.Lookup.Binding != nil && plan.Source.Lookup.Key != nil
}

func constructionSemanticGroupCodingPath(rootResourceType string, family catalog.SemanticInventoryFamily) (string, bool) {
	if family.ResourceType != rootResourceType || family.Source.Type != rootResourceType || family.Source.Path == "" {
		return "", false
	}
	path := strings.TrimPrefix(family.Source.Path, rootResourceType+".")
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return "", false
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(rootResourceType))
	if err != nil {
		return "", false
	}
	for _, candidate := range paths {
		if candidate == path {
			facts, resolveErr := index.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
			return path, resolveErr == nil && facts.CanonicalPath == path && facts.FHIRType == "Coding" &&
				facts.Cardinality == fhirschema.RowCardinalityMany && facts.Shape == fhirschema.RowPathArray && !facts.Reference
		}
	}
	return "", false
}

func (s *Service) resolveConstructionGroupSemanticCategories(
	ctx context.Context,
	base constructionBase,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	priorByStep := make(map[string][]authoringv2.ConstructionGroupKey)
	for _, step := range base.construction.Steps {
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
			stage := constructionSourceStage(base.stages)
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
	base constructionBase,
	outputID, stageID, choiceID string,
) (authoringv2.ConstructionGroupSemanticCategory, error) {
	identity, err := capability.DecodeConstructionChoiceID(choiceID)
	if err != nil || identity.SnapshotToken != base.snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemantic {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the semantic Group field for the current snapshot", nil, err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.GroupOutputID != outputID || source.GroupStageID != stageID || len(identity.Route) != 0 ||
		source.RuleHint != catalog.SemanticRuleHintCodedCategoryV1 {
		return authoringv2.ConstructionGroupSemanticCategory{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the semantic Group choice does not belong to this exact output stage", nil)
	}
	family, err := s.reauthorizeConstructionSemanticGroupFamily(ctx, base, source.BindingID, source.ConceptID, source.SourceKind, source.ResourceType, source.SourcePath, source.System, source.Version, source.RuleVersion)
	if err != nil {
		return authoringv2.ConstructionGroupSemanticCategory{}, err
	}
	codingPath, ok := constructionSemanticGroupCodingPath(base.document.RootResourceType, family)
	if !ok || codingPath != strings.TrimPrefix(family.Source.Path, base.document.RootResourceType+".") {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected semantic family no longer maps to a generated root Coding path", nil, nil)
	}
	candidate, ok := semanticConstructionCandidate(base.snapshot, semanticInventoryEntryForFamily(family).Observation)
	if !ok || candidate.ID != source.CandidateID || candidate.NodeID != source.NodeID || candidate.FieldPath != source.FieldPath {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the semantic family no longer has its exact compiler-proved field", nil, nil)
	}
	buildID := catalog.SemanticInventoryBuildID(projectid.Legacy(base.snapshot.Identity.Project), base.snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(base.snapshot, base.explorerID, base.document.RootResourceType, buildID)
	if err != nil || identity.SemanticContextToken != contextToken || identity.BuildID != buildID {
		return authoringv2.ConstructionGroupSemanticCategory{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "reload semantic Group choices for the current inventory", nil, err)
	}
	entry := semanticInventoryEntryForFamily(family)
	expected, err := semanticCategoryConstructionChoiceForRoute(base.snapshot, contextToken, buildID, nil, family, candidate)
	if err == nil {
		semanticSource, sourceOK := expected.Source.(capability.SemanticBindingChoiceSource)
		if sourceOK {
			semanticSource.GroupOutputID, semanticSource.GroupStageID = outputID, stageID
			expected, err = capability.NewSemanticConstructionChoiceForRoute(base.snapshot.Token, contextToken, buildID, nil, semanticSource, candidate)
		}
	}
	if err != nil || expected.ChoiceID != choiceID || identity.BuildID != buildID || entry.BindingID != family.BindingID {
		return authoringv2.ConstructionGroupSemanticCategory{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the semantic Group choice cannot be reissued from current inventory evidence", err)
	}
	return constructionGroupSemanticBinding(family, codingPath), nil
}

func (s *Service) reauthorizeConstructionSemanticGroupBinding(
	ctx context.Context,
	base constructionBase,
	binding authoringv2.ConstructionGroupSemanticCategory,
) error {
	family, err := s.reauthorizeConstructionSemanticGroupFamily(ctx, base, binding.BindingID, binding.FamilyID,
		binding.SourceKind, binding.ResourceType, binding.CodingPath, binding.System, binding.Version, binding.RuleVersion)
	if err != nil {
		return err
	}
	path, ok := constructionSemanticGroupCodingPath(base.document.RootResourceType, family)
	if !ok || path != binding.CodingPath || family.FamilyID != binding.FamilyID || family.System != binding.System || family.Version != binding.Version {
		return conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the saved semantic Group family no longer matches current generated FHIR metadata", nil, nil)
	}
	return nil
}

func (s *Service) reauthorizeConstructionSemanticGroupFamily(
	ctx context.Context,
	base constructionBase,
	bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion string,
) (catalog.SemanticInventoryFamily, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return catalog.SemanticInventoryFamily{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "semantic inventory reauthorization is not configured", nil)
	}
	unrestricted := base.authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	path := strings.TrimPrefix(sourcePath, resourceType+".")
	reference := catalog.SemanticInventoryFamilyReference{
		FamilyID: familyID, BindingID: bindingID, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: path, System: system, Version: version,
	}
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: projectid.Legacy(base.snapshot.Identity.Project), DatasetGeneration: base.snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), base.authorized.Scope.AuthResourcePaths...),
		FamilyReferences: []catalog.SemanticInventoryFamilyReference{reference},
	})
	if err != nil {
		return catalog.SemanticInventoryFamily{}, unavailable("construction-proposal", "CATALOG_UNAVAILABLE", "the selected semantic Group family could not be reauthorized", err)
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(base.snapshot.Identity.Project), base.snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete ||
		resolved.Build.BuildID != expectedBuildID || len(resolved.Families) != 1 {
		return catalog.SemanticInventoryFamily{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the semantic inventory family changed; reload Group choices", nil, nil)
	}
	family := resolved.Families[0]
	if family.FamilyID != familyID || family.BindingID != bindingID || family.ResourceType != resourceType ||
		family.SourceKind != sourceKind || family.System != system || family.Version != version || family.RuleVersion != ruleVersion ||
		strings.TrimPrefix(family.Source.Path, resourceType+".") != path || !semanticGroupFamilyReady(family) {
		return catalog.SemanticInventoryFamily{}, conflict("construction-proposal", "STALE_SEMANTIC_CONTEXT", "the semantic inventory family no longer matches the saved Group binding", nil, nil)
	}
	return family, nil
}

func constructionGroupSemanticBinding(family catalog.SemanticInventoryFamily, codingPath string) authoringv2.ConstructionGroupSemanticCategory {
	return authoringv2.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: family.BindingID, RuleVersion: family.RuleVersion, FamilyID: family.FamilyID, SourceKind: family.SourceKind,
		System: family.System, Version: family.Version, ResourceType: family.ResourceType, CodingPath: codingPath,
	}
}
