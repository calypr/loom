package construction

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

func constructionSearchSourceKey(source ConstructionChoiceSearchSource) (string, error) {
	switch source.Kind {
	case capability.ConstructionChoiceSourceField:
		if strings.TrimSpace(source.CandidateID) == "" || source.ContextToken != "" || source.BuildID != "" || source.ConceptID != "" || source.BindingID != "" || source.ChoiceID != "" || source.SelectionMode != "" {
			return "", malformed("construction-choices", "FIELD source requires only candidateId", nil)
		}
		return "field:" + source.CandidateID, nil
	case capability.ConstructionChoiceSourceSemantic:
		if strings.TrimSpace(source.ContextToken) == "" || strings.TrimSpace(source.BuildID) == "" || strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" || source.CandidateID != "" {
			return "", malformed("construction-choices", "SEMANTIC source requires contextToken, buildId, conceptId, and bindingId", nil)
		}
		if source.SelectionMode == "" || source.SelectionMode == semantic.ExactConcept {
			if source.ChoiceID != "" {
				return "", malformed("construction-choices", "exact SEMANTIC source cannot include a category choiceId", nil)
			}
			return "semantic:" + source.ContextToken + "\x00" + source.BuildID + "\x00" + source.BindingID + "\x00" + source.ConceptID, nil
		}
		if source.SelectionMode != semantic.SystemVersionCategory || strings.TrimSpace(source.ChoiceID) == "" {
			return "", malformed("construction-choices", "category SEMANTIC source requires a signed choiceId", nil)
		}
		return "semantic-category:" + source.ContextToken + "\x00" + source.BuildID + "\x00" + source.BindingID + "\x00" + source.ConceptID + "\x00" + source.ChoiceID, nil
	default:
		return "", malformed("construction-choices", "source kind must be FIELD or SEMANTIC", nil)
	}
}

func (s constructionRouteFlow) searchConstructionChoices(ctx context.Context, request ConstructionChoiceSearchRequest) (ConstructionChoiceSearchResponse, error) {
	result := ConstructionChoiceSearchResponse{SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Choices: []capability.ConstructionChoice{}}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" || len(request.Cursor) > 4096 {
		return result, malformed("construction-choices", "project, explorerId, snapshotToken, and outputId are required", nil)
	}
	if s.capability.ForCompilation == nil || s.capability.Catalog == nil {
		return result, unavailable("construction-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	identityKey, err := constructionSearchSourceKey(request.Source)
	if err != nil {
		return result, err
	}
	authorized, err := s.capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, conflict("construction-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching routes", nil, err)
	}
	snapshot := authorized.Snapshot
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("construction-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching routes", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("construction-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := workspace.FindOutputDocument(request.OutputID)
	if document == nil || document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, malformed("construction-choices", "outputId does not identify a valid row-rooted table", nil)
	}
	var candidate capability.Candidate
	var semanticEntry semantic.Entry
	var semanticFamily *semantic.Family
	semanticContext := ""
	buildID := ""
	if request.Source.Kind == capability.ConstructionChoiceSourceSemantic {
		if request.Source.SelectionMode == semantic.SystemVersionCategory {
			var family semantic.Family
			family, buildID, err = s.resolveConstructionSearchSemanticFamily(ctx, authorized, request)
			if err == nil {
				semanticFamily = &family
				semanticEntry = semantic.EntryForFamily(family)
			}
		} else {
			semanticEntry, buildID, err = s.resolveConstructionSearchSemanticEntry(ctx, authorized, request)
		}
		if err != nil {
			return result, err
		}
		semanticContext, err = semanticInventoryContextToken(snapshot, request.ExplorerID, document.RootResourceType, buildID)
		if err != nil || request.Source.ContextToken != semanticContext {
			return result, conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "reload the semantic choice for the current table root and inventory build", nil, err)
		}
		var found bool
		candidate, found = capability.SemanticConstructionCandidate(snapshot, semanticEntry.Observation)
		if !found || semanticEntry.Observation.Source.Type != candidate.ResourceType {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic binding and candidate resource do not match", nil)
		}
		plan := semanticauthoring.ResolveSemanticSelectionPlan(semanticEntry.Observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic source has no supported typed compiler binding", nil)
		}
	} else {
		var found bool
		candidate, found = capability.UniqueCapabilityCandidate(snapshot, request.Source.CandidateID)
		if !found {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact field candidate is unavailable in this authorized snapshot", nil)
		}
	}
	var routes [][]capability.ConstructionRouteStep
	if occurrenceID := strings.TrimSpace(request.OccurrenceID); occurrenceID != "" {
		if strings.TrimSpace(request.Cursor) != "" {
			return result, malformed("construction-choices", "cursor cannot be combined with occurrenceId", nil)
		}
		route, routeErr := constructionRouteForOccurrence(snapshot, document.RootResourceType, candidate.NodeID, document.Route, occurrenceID)
		if routeErr != nil {
			return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected occurrence does not resolve to this source through one authorized route", routeErr)
		}
		routes = [][]capability.ConstructionRouteStep{route}
		result.Complete = true
	} else {
		page, planErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
			Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: candidate.NodeID,
			SourceKey: identityKey, Cursor: request.Cursor, Limit: request.Limit,
		})
		if planErr != nil {
			return result, conflict("construction-choices", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this exact source", nil, planErr)
		}
		result.Complete, result.Truncated, result.NextCursor = page.Complete, page.Truncated, page.NextCursor
		routes = page.Routes
	}
	for _, route := range routes {
		resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, document.RootResourceType, candidate.NodeID, route)
		if routeErr != nil {
			if strings.TrimSpace(request.OccurrenceID) != "" {
				return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected occurrence route is no longer authorized", routeErr)
			}
			continue
		}
		provenCandidate := candidate
		if len(resolvedRoute) > 0 || strings.HasPrefix(candidate.ID, "schema_field.") {
			provenCandidate, err = proveConstructionCandidate(ctx, authorized, document.RootResourceType, candidate, resolvedRoute)
			if err != nil {
				if strings.TrimSpace(request.OccurrenceID) != "" {
					return result, unprocessable("construction-choices", "UNPROVEN_OCCURRENCE_ROUTE", "the complete selected route and source do not compile", err)
				}
				continue
			}
		}
		var choice capability.ConstructionChoice
		if request.Source.Kind == capability.ConstructionChoiceSourceField {
			if err = capability.ValidateConstructionSourceType(provenCandidate, snapshot.Candidates); err != nil {
				return result, unprocessable("construction-choices", "UNSUPPORTED_CONSTRUCTION_SOURCE_TYPE", err.Error(), err)
			}
			choice, err = capability.NewFieldConstructionChoiceForRoute(snapshot.Token, resolvedRoute, provenCandidate)
		} else {
			if semanticFamily != nil {
				choice, err = semanticCategoryConstructionChoiceForRoute(snapshot, semanticContext, buildID, resolvedRoute, *semanticFamily, provenCandidate)
			} else {
				ownerRecords := proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, semanticEntry.Observation, resolvedRoute)
				choice, err = capability.SemanticConstructionChoiceForRoute(snapshot, semanticContext, buildID, resolvedRoute, semanticEntry, provenCandidate, ownerRecords)
			}
		}
		if err != nil {
			if strings.TrimSpace(request.OccurrenceID) != "" {
				return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected source could not be bound to its exact route", err)
			}
			continue
		}
		result.Choices = append(result.Choices, choice)
	}
	return result, nil
}

func (s constructionRouteFlow) resolveConstructionSearchSemanticEntry(ctx context.Context, authorized ports.AuthorizedCapability, request ConstructionChoiceSearchRequest) (semantic.Entry, string, error) {
	if s.resolveInventory == nil {
		return semantic.Entry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.resolveInventory(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References: []semantic.Reference{{ConceptID: request.Source.ConceptID, BindingID: request.Source.BindingID}},
	})
	if err != nil {
		return semantic.Entry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "the selected semantic source could not be resolved", err)
	}
	expectedBuild := semantic.BuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete || resolved.Build.BuildID != expectedBuild || request.Source.BuildID != expectedBuild {
		return semantic.Entry{}, "", conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "the semantic source belongs to a different or incomplete inventory build", nil, nil)
	}
	var match semantic.Entry
	for _, entry := range resolved.Entries {
		if entry.ConceptID != request.Source.ConceptID || entry.BindingID != request.Source.BindingID || match.ConceptID != "" {
			return semantic.Entry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated or duplicate source", nil)
		}
		match = entry
	}
	if match.ConceptID == "" {
		return semantic.Entry{}, "", unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact semantic source is unavailable in this authorized inventory", nil)
	}
	return match, expectedBuild, nil
}

func (s constructionRouteFlow) resolveConstructionSearchSemanticFamily(ctx context.Context, authorized ports.AuthorizedCapability, request ConstructionChoiceSearchRequest) (semantic.Family, string, error) {
	var zero semantic.Family
	identity, err := capability.DecodeConstructionChoiceID(request.Source.ChoiceID)
	if err != nil || identity.SnapshotToken != authorized.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemantic ||
		identity.SemanticContextToken != request.Source.ContextToken || identity.BuildID != request.Source.BuildID {
		return zero, "", conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "reload the signed category choice for the current table root and inventory build", nil, err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.RuleHint != semantic.RuleHintCodedCategoryV1 || source.ConceptID != request.Source.ConceptID ||
		source.BindingID != request.Source.BindingID || source.System == "" || source.Code != "" ||
		(source.SourceKind != semantic.SourceFile && source.SourceKind != semantic.SourceRetained) {
		return zero, "", unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the signed source is not a supported coded category family", nil)
	}
	if s.resolveInventory == nil {
		return zero, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	reference := semantic.FamilyReference{
		FamilyID: source.ConceptID, BindingID: source.BindingID, SourceKind: source.SourceKind,
		ResourceType: source.ResourceType, SourcePath: source.SourcePath, System: source.System, Version: source.Version,
	}
	resolved, err := s.resolveInventory(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		FamilyReferences: []semantic.FamilyReference{reference},
	})
	if err != nil {
		return zero, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "the selected semantic category could not be resolved", err)
	}
	expectedBuild := semantic.BuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete ||
		resolved.Build.BuildID != expectedBuild || request.Source.BuildID != expectedBuild {
		return zero, "", conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "the semantic category belongs to a different or incomplete inventory build", nil, nil)
	}
	if len(resolved.Families) != 1 || resolved.Families[0].FamilyReference != reference {
		return zero, "", unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact semantic category is unavailable in this authorized inventory", nil)
	}
	return resolved.Families[0], expectedBuild, nil
}
