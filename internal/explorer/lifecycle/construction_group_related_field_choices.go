package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type observedRelatedGroupCandidate struct {
	candidate       capability.Candidate
	scalarFacts     fhirschema.RowPathFacts
	hasScalar       bool
	codingSelectors []relatedCodingSelector
}

type relatedCodingSelector struct {
	path  string
	facts fhirschema.RowPathFacts
}

func (s *Service) appendRelatedGroupFieldChoices(
	ctx context.Context,
	base constructionBase,
	stage explorer.ReceiptConstructionStage,
	fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext,
	index *fhirschema.Index,
	choicesByID map[string]ConstructionGroupFieldChoice,
	targetGroupStepID string,
	selectedGroupStepID string,
) (bool, error) {
	if fieldContext == nil || fieldContext.SourceExpansion != nil {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateAuthorizedReadScope(base.authorized.Scope, base.snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return false, conflict("construction-capabilities", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	anchor, err := constructionGroupRelatedAnchorForStage(base, fieldContext)
	if err != nil {
		return false, conflict("construction-capabilities", "STALE_ROUTE", "the exact Group input occurrence no longer resolves in the authorized snapshot", nil, err)
	}
	compilerContext, err := groupFieldCompilerContext(base, stage, fieldContext)
	if err != nil {
		return false, conflict("construction-capabilities", "INVALID_COMPILATION_RECEIPT", "the Group source context is not usable for related choices", nil, err)
	}
	candidatesByNode := make(map[string][]observedRelatedGroupCandidate)
	targetSet := make(map[string]struct{})
	for _, candidate := range base.snapshot.Candidates {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if candidate.NodeID == "" || candidate.ResourceType == "" || candidate.NodeID == anchor.nodeID ||
			!candidate.Observed || !candidate.Populated || fhirNonGroupableGroupFieldPath(candidate.FieldPath) {
			continue
		}
		node, ok := base.snapshot.Node(candidate.NodeID)
		if !ok || node.ResourceType != candidate.ResourceType {
			continue
		}
		entry := observedRelatedGroupCandidate{candidate: candidate}
		entry.scalarFacts, entry.hasScalar = populatedGroupScalarCandidate(candidate, index)
		entry.codingSelectors = observedRelatedCodingSelectors(candidate, index)
		if !entry.hasScalar && len(entry.codingSelectors) == 0 {
			continue
		}
		candidatesByNode[candidate.NodeID] = append(candidatesByNode[candidate.NodeID], entry)
		targetSet[candidate.NodeID] = struct{}{}
	}
	if len(targetSet) == 0 {
		return false, nil
	}
	targetNodeIDs := make([]string, 0, len(targetSet))
	for nodeID := range targetSet {
		targetNodeIDs = append(targetNodeIDs, nodeID)
	}
	sort.Strings(targetNodeIDs)
	sourceKey := fmt.Sprintf("group-related-field:%s:%s:%s:%s", fieldContext.SourceStageID, fieldContext.SourceShapeID, fieldContext.SourceIdentityColumn, anchor.occurrenceID)
	incomplete := false
	processedRoutes := make(map[string]struct{})
	processRoute := func(targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		resolvedRoute, routeErr := reauthorizeConstructionRouteFromAnchor(base.snapshot, anchor.nodeID, targetNodeID, route)
		if routeErr != nil {
			return nil, false, nil
		}
		routeKey := constructionGroupRelatedRouteKey(resolvedRoute)
		if _, exists := processedRoutes[routeKey]; exists {
			return resolvedRoute, true, nil
		}
		if !constructionRouteHasCompilerProof(ctx, base.authorized, resolvedRoute) {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
		processedRoutes[routeKey] = struct{}{}
		routeLabel := constructionGroupRelatedRouteLabel(anchor.resourceType, resolvedRoute)
		for _, observed := range candidatesByNode[targetNodeID] {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			candidate, proofErr := proveConstructionCandidate(ctx, base.authorized, anchor.resourceType, observed.candidate, resolvedRoute)
			if proofErr != nil || candidate.ID != observed.candidate.ID || candidate.NodeID != targetNodeID ||
				candidate.ResourceType != observed.candidate.ResourceType || canonicalRowPath(candidate.FieldPath) != canonicalRowPath(observed.candidate.FieldPath) ||
				!candidate.Observed || !candidate.Populated {
				if err := ctx.Err(); err != nil {
					return nil, false, err
				}
				continue
			}
			if observed.hasScalar {
				valueType := string(observed.scalarFacts.FHIRType)
				related, sourceShapeID, resolveErr := resolveRelatedGroupSource(compilerContext, "SCALAR", anchor, resolvedRoute, valueType, candidate.ID, candidate.FieldPath, "", "")
				if resolveErr == nil {
					source := groupFieldSourceChoiceFromRelated(fieldContext, related, sourceShapeID, candidate.NodeID, valueType, "ONE", "SCALAR", "", "")
					source.TargetGroupStepID = targetGroupStepID
					choice, choiceErr := capability.NewGroupFieldConstructionChoice(base.snapshot.Token, resolvedRoute, source, candidate)
					if choiceErr == nil {
						label := firstNonEmpty(observed.scalarFacts.Title, candidate.Label, constructionGroupFieldLabel(candidate.FieldPath))
						choicesByID[choice.ChoiceID] = ConstructionGroupFieldChoice{
							Kind: "FIELD_SOURCE", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: choice.ChoiceID},
							ValueKind: "SCALAR", Label: label, ScopeLabel: routeLabel, ValueType: candidate.LogicalType,
							SourceStageID: fieldContext.SourceStageID, SourceShapeID: sourceShapeID,
							CandidateID: candidate.ID, SourceRole: "RELATED_OCCURRENCE", OccurrenceID: related.TargetOccurrenceID,
							ResourceType: valueType, OwnerOccurrenceID: related.TargetOccurrenceID,
							OwnerResourceType: related.TargetResourceType, CanonicalPath: candidate.FieldPath,
							RelatedOccurrence: &ConstructionGroupRelatedOccurrenceChoice{RouteID: related.RouteID, RouteLabel: routeLabel},
						}
					}
				}
			}
			if targetGroupStepID != "" {
				continue
			}
			for _, selector := range observed.codingSelectors {
				related, sourceShapeID, resolveErr := resolveRelatedGroupSource(
					compilerContext, "CODING_IDENTITY", anchor, resolvedRoute, "Coding", "", "", selector.path,
					string(recipe.ConstructionGroupCodingEmptyPolicyGroup),
				)
				if resolveErr != nil {
					continue
				}
				rowChoice, rowChoiceErr := relatedCodingRowChoice(base.snapshot, anchor.rootRoute, resolvedRoute, related, selector.facts)
				if rowChoiceErr != nil {
					continue
				}
				source := groupFieldSourceChoiceFromRelated(fieldContext, related, sourceShapeID, candidate.NodeID, "Coding", "MANY", "CODING_IDENTITY", rowChoice.ChoiceID, selector.path)
				choice, choiceErr := capability.NewGroupCodingIdentityChoice(base.snapshot.Token, resolvedRoute, source)
				if choiceErr != nil {
					continue
				}
				label := relatedCodingFieldLabel(index, candidate.ResourceType, selector.path)
				choicesByID[choice.ChoiceID] = ConstructionGroupFieldChoice{
					Kind: "FIELD_SOURCE", FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: choice.ChoiceID},
					ValueKind: "CODING_IDENTITY", Label: label, ScopeLabel: routeLabel, ValueType: "string",
					SourceStageID: fieldContext.SourceStageID, SourceShapeID: sourceShapeID,
					SourceRole: "RELATED_OCCURRENCE", OccurrenceID: related.TargetOccurrenceID, ResourceType: "Coding",
					OwnerOccurrenceID: related.TargetOccurrenceID, OwnerResourceType: related.TargetResourceType, CanonicalPath: selector.path,
					RelatedOccurrence: &ConstructionGroupRelatedOccurrenceChoice{RouteID: related.RouteID, RouteLabel: routeLabel},
				}
			}
		}
		return resolvedRoute, true, nil
	}

	savedRouteGroupStepID := selectedGroupStepID
	if targetGroupStepID != "" {
		savedRouteGroupStepID = targetGroupStepID
	}
	for _, savedRoute := range savedRelatedGroupRoutes(base, savedRouteGroupStepID, fieldContext, anchor) {
		if _, _, err := processRoute(savedRoute.targetNodeID, savedRoute.steps); err != nil {
			return incomplete, err
		}
	}
	if targetGroupStepID != "" {
		return incomplete, nil
	}

	for _, targetNodeID := range targetNodeIDs {
		if err := ctx.Err(); err != nil {
			return incomplete, err
		}
		cursor := ""
		for pageIndex := 0; pageIndex < 82; pageIndex++ {
			if err := ctx.Err(); err != nil {
				return incomplete, err
			}
			page, planErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: base.snapshot, RootNodeID: anchor.nodeID, TargetNodeID: targetNodeID,
				SourceKey: sourceKey, Cursor: cursor, Limit: capability.ConstructionRouteMaxLimit,
			})
			if planErr != nil {
				return false, conflict("construction-capabilities", "STALE_OR_INVALID_ROUTE_CURSOR", "the exact Group related route set could not be resolved", nil, planErr)
			}
			route, proved, routeErr := firstProvenRelatedGroupRoute(ctx, targetNodeID, page.Routes, func(plannedRoute []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, bool, error) {
				return processRoute(targetNodeID, plannedRoute)
			})
			if routeErr != nil {
				return incomplete, routeErr
			}
			if proved && len(route) > 0 {
				break
			}
			if page.Complete {
				break
			}
			if page.NextCursor == "" {
				incomplete = true
				break
			}
			cursor = page.NextCursor
			if pageIndex == 81 {
				incomplete = true
			}
		}
	}
	return incomplete, nil
}

type savedRelatedGroupRoute struct {
	targetNodeID string
	steps        []capability.ConstructionRouteStep
}

func savedRelatedGroupRoutes(
	base constructionBase,
	groupStepID string,
	fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext,
	anchor constructionGroupRelatedAnchor,
) []savedRelatedGroupRoute {
	if groupStepID == "" || fieldContext == nil {
		return nil
	}
	var group *authoringv2.ConstructionStep
	for index := range base.construction.Steps {
		if base.construction.Steps[index].ID == groupStepID {
			group = &base.construction.Steps[index]
			break
		}
	}
	if group == nil || group.Operation.Kind != authoringv2.ConstructionOperationGroup || group.Operation.Group == nil {
		return nil
	}
	inputStageID, ok := constructionGroupInputStageID(*group)
	if !ok || inputStageID != fieldContext.SourceStageID {
		return nil
	}

	seen := make(map[string]struct{})
	routes := make([]savedRelatedGroupRoute, 0)
	for _, key := range group.Operation.Group.Keys {
		source := key.FieldSource
		if source == nil || source.Kind != "BINDING" || source.SourceStageID != fieldContext.SourceStageID || source.SourceShapeID == "" || source.Input == nil ||
			source.Input.Kind != "RELATED_OCCURRENCE" || source.Input.RelatedOccurrence == nil {
			continue
		}
		related := source.Input.RelatedOccurrence
		if related.AnchorOccurrenceID != anchor.occurrenceID || related.AnchorNodeID != anchor.nodeID ||
			related.AnchorResourceType != anchor.resourceType || related.TargetNodeID == "" || related.TargetOccurrenceID == "" ||
			len(related.Route) == 0 {
			continue
		}
		expectedOccurrenceID, expectedNodeID, expectedResourceType := anchor.occurrenceID, anchor.nodeID, anchor.resourceType
		steps := make([]capability.ConstructionRouteStep, len(related.Route))
		valid := true
		for index, step := range related.Route {
			if step.StepIndex != index || step.FromOccurrenceID != expectedOccurrenceID || step.FromNodeID != expectedNodeID ||
				step.FromResourceType != expectedResourceType || step.EdgeID == "" || step.ToOccurrenceID == "" ||
				step.ToNodeID == "" || step.ToResourceType == "" {
				valid = false
				break
			}
			steps[index] = capability.ConstructionRouteStep{
				EdgeID: step.EdgeID, FromNodeID: step.FromNodeID, ToNodeID: step.ToNodeID,
				FromResourceType: step.FromResourceType, ToResourceType: step.ToResourceType,
				Relationship: step.Relationship, StorageDirection: step.StorageDirection, MatchMode: step.MatchMode,
			}
			expectedOccurrenceID, expectedNodeID, expectedResourceType = step.ToOccurrenceID, step.ToNodeID, step.ToResourceType
		}
		if !valid || expectedOccurrenceID != related.TargetOccurrenceID || expectedNodeID != related.TargetNodeID ||
			expectedResourceType != related.TargetResourceType || constructionGroupRelatedRouteKey(steps) == "" {
			continue
		}
		targetNode, ok := base.snapshot.Node(related.TargetNodeID)
		if !ok || targetNode.ResourceType != related.TargetResourceType {
			continue
		}
		routeKey := constructionGroupRelatedRouteKey(steps)
		if _, exists := seen[routeKey]; exists {
			continue
		}
		seen[routeKey] = struct{}{}
		routes = append(routes, savedRelatedGroupRoute{targetNodeID: related.TargetNodeID, steps: steps})
	}
	return routes
}

func constructionGroupRelatedRouteKey(route []capability.ConstructionRouteStep) string {
	if len(route) == 0 {
		return ""
	}
	parts := make([]string, 0, len(route)*8)
	for _, step := range route {
		parts = append(parts, step.EdgeID, step.FromNodeID, step.ToNodeID, step.FromResourceType,
			step.ToResourceType, step.Relationship, step.StorageDirection, step.MatchMode)
	}
	return strings.Join(parts, "\x00")
}

func firstProvenRelatedGroupRoute(
	ctx context.Context,
	targetNodeID string,
	routes [][]capability.ConstructionRouteStep,
	prove func([]capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, bool, error),
) ([]capability.ConstructionRouteStep, bool, error) {
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if len(route) == 0 || route[len(route)-1].ToNodeID != targetNodeID {
			continue
		}
		resolved, ok, err := prove(route)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return resolved, true, nil
		}
	}
	return nil, false, nil
}

func groupFieldCompilerContext(
	base constructionBase,
	stage explorer.ReceiptConstructionStage,
	fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext,
) (*lower.CompiledGroupFieldSourceContext, error) {
	if fieldContext == nil || stage.ID != fieldContext.SourceStageID || fieldContext.SourceShapeID == "" ||
		base.document.Route.OccurrenceID == "" || base.document.RootResourceType == "" {
		return nil, fmt.Errorf("compiler context has incomplete stage or root occurrence facts")
	}
	if fieldContext.SourceExpansion != nil {
		return nil, fmt.Errorf("related Group field choices are not yet supported for an expanded input stage")
	}
	if base.document.Route.ResourceType != base.document.RootResourceType {
		return nil, fmt.Errorf("source document root occurrence resource differs from its root type")
	}
	context := &lower.CompiledGroupFieldSourceContext{
		SourceStageID: fieldContext.SourceStageID, SourceShapeID: fieldContext.SourceShapeID,
		SourceIdentityColumn: fieldContext.SourceIdentityColumn,
		RootOccurrenceID:     base.document.Route.OccurrenceID, RootResourceType: base.document.RootResourceType,
		FieldOwners: make([]lower.CompiledGroupFieldOwner, 0, len(fieldContext.FieldOwners)),
	}
	for _, owner := range fieldContext.FieldOwners {
		context.FieldOwners = append(context.FieldOwners, lower.CompiledGroupFieldOwner{
			Role: owner.Role, OccurrenceID: owner.OccurrenceID, OwnerResourceType: owner.OwnerResourceType,
			ResourceType: owner.ResourceType, BoundarySelector: owner.BoundarySelector, StepIndex: owner.StepIndex,
		})
	}
	return context, nil
}

func resolveRelatedGroupSource(
	context *lower.CompiledGroupFieldSourceContext,
	valueKind string,
	anchor constructionGroupRelatedAnchor,
	route []capability.ConstructionRouteStep,
	resourceType, candidateID, canonicalPath, codingSelector, codingEmptyPolicy string,
) (recipe.ConstructionGroupRelatedOccurrenceFieldSource, string, error) {
	steps := make([]recipe.ConstructionGroupRelatedRouteStep, len(route))
	for index, hop := range route {
		steps[index] = recipe.ConstructionGroupRelatedRouteStep{
			StepIndex: index, FromNodeID: hop.FromNodeID, FromResourceType: hop.FromResourceType,
			EdgeID: hop.EdgeID, Relationship: hop.Relationship, StorageDirection: hop.StorageDirection,
			MatchMode: hop.MatchMode, ToNodeID: hop.ToNodeID, ToResourceType: hop.ToResourceType,
		}
	}
	emptyPolicy := recipe.ExpansionPreserveParent
	for _, hop := range route {
		if hop.MatchMode == "REQUIRED" {
			emptyPolicy = recipe.ExpansionExclude
			break
		}
	}
	related := recipe.ConstructionGroupRelatedOccurrenceFieldSource{
		AnchorOccurrenceID: anchor.occurrenceID, AnchorNodeID: anchor.nodeID, AnchorResourceType: anchor.resourceType,
		Route: steps, TargetNodeID: route[len(route)-1].ToNodeID, TargetResourceType: route[len(route)-1].ToResourceType,
		ResourceType: resourceType, CandidateID: candidateID, CanonicalPath: canonicalPath,
		EmptyPolicy: emptyPolicy, Multiplicity: recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
		CodingSelector: codingSelector, CodingEmptyPolicy: recipe.ConstructionGroupCodingEmptyPolicy(codingEmptyPolicy),
	}
	if valueKind == "CODING_IDENTITY" {
		related.OrdinalSemantics = recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition
	}
	return lower.ResolveGroupRelatedOccurrenceSource(context, valueKind, related)
}

func groupFieldSourceChoiceFromRelated(
	fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext,
	related recipe.ConstructionGroupRelatedOccurrenceFieldSource,
	sourceShapeID, nodeID, fhirType, cardinality, shape, rowChoiceID, codingSelector string,
) capability.GroupFieldChoiceSource {
	steps := make([]capability.GroupFieldRelatedRouteStep, len(related.Route))
	for index, hop := range related.Route {
		steps[index] = capability.GroupFieldRelatedRouteStep{
			StepIndex: hop.StepIndex, FromOccurrenceID: hop.FromOccurrenceID, FromNodeID: hop.FromNodeID,
			FromResourceType: hop.FromResourceType, EdgeID: hop.EdgeID, Relationship: hop.Relationship,
			StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			ToOccurrenceID: hop.ToOccurrenceID, ToNodeID: hop.ToNodeID, ToResourceType: hop.ToResourceType,
		}
	}
	return capability.GroupFieldChoiceSource{
		Kind:          capability.ConstructionChoiceSourceGroupField,
		SourceStageID: fieldContext.SourceStageID, SourceShapeID: sourceShapeID,
		SourceIdentityColumn: fieldContext.SourceIdentityColumn, ValueKind: shape,
		NodeID: nodeID, FHIRType: fhirType, Cardinality: cardinality, Shape: shape, RowChoiceID: rowChoiceID,
		Input: &capability.GroupFieldChoiceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &capability.GroupFieldRelatedOccurrenceInput{
			RouteID: related.RouteID, AnchorOccurrenceID: related.AnchorOccurrenceID, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType: related.AnchorResourceType, Route: steps, TargetOccurrenceID: related.TargetOccurrenceID,
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType, ResourceType: related.ResourceType,
			CandidateID: related.CandidateID, CanonicalPath: related.CanonicalPath,
			EmptyPolicy: string(related.EmptyPolicy), Multiplicity: related.Multiplicity,
			CodingSelector: related.CodingSelector, CodingEmptyPolicy: string(related.CodingEmptyPolicy),
			OrdinalSemantics: related.OrdinalSemantics,
		}},
	}
}

func relatedCodingRowChoice(
	snapshot capability.Snapshot,
	anchorRootRoute, relatedRoute []capability.ConstructionRouteStep,
	related recipe.ConstructionGroupRelatedOccurrenceFieldSource,
	facts fhirschema.RowPathFacts,
) (capability.RowChoice, error) {
	fullRoute := append(append([]capability.ConstructionRouteStep(nil), anchorRootRoute...), relatedRoute...)
	return capability.NewRowChoice(snapshot, []capability.RowChoiceOccurrence{{
		OccurrenceID: related.TargetOccurrenceID, NodeID: related.TargetNodeID,
		ResourceType: related.TargetResourceType, Route: fullRoute,
	}}, related.TargetOccurrenceID, capability.RowChoiceExpandedScope, capability.RowChoiceFacts{
		ResourceType: related.TargetResourceType, CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: capability.RowChoiceCardinality(facts.Cardinality), Shape: capability.RowChoiceShape(facts.Shape),
		Reference: facts.Reference, Title: facts.Title, Description: facts.Description,
	})
}

func observedRelatedCodingSelectors(candidate capability.Candidate, index *fhirschema.Index) []relatedCodingSelector {
	if index == nil || !candidate.Observed || !candidate.Populated || candidate.ResourceType == "" {
		return nil
	}
	selectors := make(map[string]fhirschema.RowPathFacts)
	for _, boundary := range candidate.RepeatedBoundaries {
		selector := canonicalRowPath(boundary.Path)
		if !observedGroupCodingSelector(candidate, candidate.NodeID, candidate.ResourceType, selector) {
			continue
		}
		facts, err := index.ResolveRowPath(fhirschema.DefinitionName(candidate.ResourceType), selector)
		if err != nil || facts.CanonicalPath != selector || facts.FHIRType != "Coding" ||
			facts.Cardinality != fhirschema.RowCardinalityMany || facts.Shape != fhirschema.RowPathArray || facts.Reference {
			continue
		}
		selectors[selector] = facts
	}
	result := make([]relatedCodingSelector, 0, len(selectors))
	for selector, facts := range selectors {
		result = append(result, relatedCodingSelector{path: selector, facts: facts})
	}
	return result
}

func constructionGroupRelatedRouteLabel(anchorResource string, route []capability.ConstructionRouteStep) string {
	parts := []string{anchorResource}
	for _, step := range route {
		matchSemantics := "Optional: parent row is kept when unmatched"
		if step.MatchMode == "REQUIRED" {
			matchSemantics = "Required: unmatched parents are excluded"
		}
		parts = append(parts, step.Relationship+" → "+step.ToResourceType+" ("+matchSemantics+")")
	}
	parts = append(parts, "One distinct target per input row; duplicate paths to the same target do not multiply it")
	return strings.Join(parts, " · ")
}

func relatedCodingFieldLabel(index *fhirschema.Index, resourceType, selector string) string {
	parentPath := strings.TrimSuffix(selector, ".coding[]")
	if parentPath != selector && parentPath != "" {
		if facts, err := index.ResolveRowPath(fhirschema.DefinitionName(resourceType), parentPath); err == nil && strings.TrimSpace(facts.Title) != "" {
			return strings.TrimSpace(facts.Title)
		}
	}
	return constructionGroupFieldLabel(parentPath)
}
