package construction

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

type constructionRouteFlow struct {
	capability       ports.CapabilityResolver
	loadBase         func(context.Context, string, string, string, int64, string, string) (Base, error)
	currentWorkspace func(context.Context, string, string) (authoringv2.Workspace, error)
	resolveInventory func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
}

func (s *Service) constructionRoutes() constructionRouteFlow {
	return constructionRouteFlow{
		capability: s.config.Capability, loadBase: s.loadConstructionBase,
		currentWorkspace: s.currentWorkspace, resolveInventory: s.config.ResolveSemanticInventorySelections,
	}
}

func (s *Service) SearchRelatedFieldChoices(ctx context.Context, request RelatedFieldChoiceSearchRequest) (RelatedFieldChoiceSearchResponse, error) {
	return s.constructionRoutes().searchRelatedFieldChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandContributorChoices(ctx context.Context, request RelatedExpandContributorChoiceSearchRequest) (RelatedExpandContributorChoiceSearchResponse, error) {
	return s.constructionRoutes().searchRelatedExpandContributorChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandChoices(ctx context.Context, request RelatedExpandChoiceSearchRequest) (RelatedExpandChoiceSearchResponse, error) {
	return s.constructionRoutes().searchRelatedExpandChoices(ctx, request)
}

func (s *Service) SearchConstructionChoices(ctx context.Context, request ConstructionChoiceSearchRequest) (ConstructionChoiceSearchResponse, error) {
	return s.constructionRoutes().searchConstructionChoices(ctx, request)
}

func (s constructionRouteFlow) searchRelatedFieldChoices(ctx context.Context, request RelatedFieldChoiceSearchRequest) (RelatedFieldChoiceSearchResponse, error) {
	result := RelatedFieldChoiceSearchResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, StageID: request.StageID,
		Choices: []RelatedFieldChoice{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.StageID) == "" || strings.TrimSpace(request.ExpectedDraftDigest) == "" ||
		request.ExpectedDraftVersion < 1 || strings.TrimSpace(request.Query) != request.Query ||
		len(request.Query) > 256 || strings.TrimSpace(request.Cursor) != request.Cursor || len(request.Cursor) > 4096 {
		return result, malformed("related-field-choices", "project, explorer, snapshot, draft, output, and stage identities are required", nil)
	}
	if s.capability.ForCompilation == nil || s.capability.Catalog == nil {
		return result, unavailable("related-field-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.Owner.DraftVersion, base.Owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == request.StageID {
			stage = &base.Stages[index]
			break
		}
	}
	if stage == nil {
		return result, conflict("related-field-choices", "STALE_STAGE_REFERENCE", "the selected stage is not in the current compiled output", nil, nil)
	}
	if stage.ActiveRelatedRecord == nil {
		return result, unprocessable("related-field-choices", "NO_ACTIVE_RELATED_RECORD", "the selected stage does not retain an exact terminal resource identity", nil)
	}
	active := stage.ActiveRelatedRecord
	if active.TargetNodeID == "" || active.TargetResourceType == "" || active.TerminalIdentityColumn == "" {
		return result, conflict("related-field-choices", "INVALID_COMPILATION_RECEIPT", "the selected stage has incomplete terminal identity metadata", nil, nil)
	}
	if err := validateAuthorizedReadScope(base.Authorized.Scope, base.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-field-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	if cursor := request.Cursor; cursor != "" {
		decoded, decodeErr := decodeRelatedFieldChoiceCursor(cursor)
		if decodeErr != nil || decoded.Version != 1 || decoded.Snapshot != request.SnapshotToken ||
			decoded.DraftVersion != base.Owner.DraftVersion || decoded.DraftDigest != base.Owner.DraftDigest ||
			decoded.OutputID != request.OutputID || decoded.StageID != request.StageID || decoded.Query != request.Query || decoded.Offset < 0 {
			return result, conflict("related-field-choices", "STALE_OR_INVALID_CHOICE_CURSOR", "restart exact field search for this output stage", nil, decodeErr)
		}
	}
	candidates := make([]capability.Candidate, 0)
	query := strings.ToLower(request.Query)
	for _, candidate := range base.Snapshot.Candidates {
		if candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
			(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
			len(candidate.RepeatedBoundaries) != 0 || !slices.Contains(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!relatedFieldPathExecutable(candidate.FieldPath) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(candidate.Label), query) && !strings.Contains(strings.ToLower(candidate.FieldPath), query) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	offset := 0
	if request.Cursor != "" {
		decoded, _ := decodeRelatedFieldChoiceCursor(request.Cursor)
		offset = decoded.Offset
	}
	if offset > len(candidates) {
		return result, conflict("related-field-choices", "STALE_OR_INVALID_CHOICE_CURSOR", "restart exact field search for this output stage", nil, nil)
	}
	limit := request.Limit
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 50 {
		return result, malformed("related-field-choices", "limit must be between 1 and 50", nil)
	}
	end := offset + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	for _, candidate := range candidates[offset:end] {
		proved, proofErr := proveConstructionCandidate(ctx, base.Authorized, active.TargetResourceType, candidate, nil)
		if proofErr != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
			(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 ||
			!relatedFieldPathExecutable(proved.FieldPath) || !relatedFieldLogicalTypeExecutable(proved.LogicalType) {
			continue
		}
		choice, choiceErr := capability.NewConstructionRelatedFieldChoice(base.Snapshot.Token, request.StageID, proved)
		if choiceErr != nil {
			continue
		}
		result.Choices = append(result.Choices, RelatedFieldChoice{
			ChoiceID: choice.ChoiceID, Label: choice.Label,
			Source: authoringv2.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: choice.Source.CandidateID,
				NodeID: choice.Source.NodeID, ResourceType: choice.Source.ResourceType, Path: choice.Source.Path,
				Cardinality: proved.Cardinality, LogicalType: proved.LogicalType,
			},
		})
	}
	result.Complete = end >= len(candidates)
	result.Truncated = !result.Complete
	if result.Truncated {
		cursor, encodeErr := encodeRelatedFieldChoiceCursor(relatedFieldChoiceCursor{
			Version: 1, Snapshot: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
			OutputID: request.OutputID, StageID: request.StageID, Query: request.Query, Offset: end,
		})
		if encodeErr != nil {
			return RelatedFieldChoiceSearchResponse{}, fmt.Errorf("encode related field cursor: %w", encodeErr)
		}
		result.NextCursor = cursor
	}
	return result, nil
}

func (s constructionRouteFlow) searchRelatedExpandContributorChoices(ctx context.Context, request RelatedExpandContributorChoiceSearchRequest) (RelatedExpandContributorChoiceSearchResponse, error) {
	result := RelatedExpandContributorChoiceSearchResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, StageID: request.StageID,
		RouteChoiceID: request.RouteChoiceID, Choices: []RelatedExpandContributorChoice{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.StageID) == "" || strings.TrimSpace(request.RouteChoiceID) == "" ||
		strings.TrimSpace(request.ExpectedDraftDigest) == "" || request.ExpectedDraftVersion < 1 ||
		strings.TrimSpace(request.Query) != request.Query || len(request.Query) > 256 ||
		strings.TrimSpace(request.Cursor) != request.Cursor || len(request.Cursor) > 4096 {
		return result, malformed("related-expand-contributors", "project, explorer, snapshot, draft, output, stage, and exact route choice identities are required", nil)
	}
	if s.capability.ForCompilation == nil || s.capability.Catalog == nil {
		return result, unavailable("related-expand-contributors", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.Owner.DraftVersion, base.Owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == request.StageID {
			stage = &base.Stages[index]
			break
		}
	}
	if stage == nil {
		return result, conflict("related-expand-contributors", "STALE_STAGE_REFERENCE", "the selected stage is not in the current compiled output", nil, nil)
	}
	if !constructionStageSupportsRelatedExpand(*stage) {
		return result, unprocessable("related-expand-contributors", "NO_SOURCE_ROW_ANCHOR", "the selected stage does not expose executable related expansion", nil)
	}
	identity, err := capability.DecodeConstructionChoiceID(request.RouteChoiceID)
	if err != nil {
		return result, unprocessable("related-expand-contributors", "INVALID_CONSTRUCTION_CHOICE", "the selected route choice is invalid", err)
	}
	if identity.SnapshotToken != base.Snapshot.Token {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion route for the current authorized snapshot", nil, nil)
	}
	selected, ok := identity.Source.(capability.RelatedResourceChoiceSource)
	if !ok || selected.StageID != request.StageID {
		return result, unprocessable("related-expand-contributors", "INVALID_CONSTRUCTION_CHOICE", "the selected route choice does not belong to this stage", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.Snapshot, *stage, base.Document.RootResourceType, selected.AnchorColumnID)
	if err != nil || selected.AnchorKind != anchor.Kind || selected.AnchorNodeID != anchor.NodeID || selected.AnchorResourceType != anchor.ResourceType {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "the selected route no longer matches this stage's exact row anchor", nil, err)
	}
	resolvedRoute, err := reauthorizeConstructionRouteFromAnchor(base.Snapshot, anchor.NodeID, selected.NodeID, identity.Route)
	if err != nil || !reflect.DeepEqual(resolvedRoute, identity.Route) || len(resolvedRoute) == 0 ||
		resolvedRoute[len(resolvedRoute)-1].ToResourceType != selected.ResourceType {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "the selected route no longer resolves to its exact terminal resource", nil, err)
	}
	if err := validateAuthorizedReadScope(base.Authorized.Scope, base.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-expand-contributors", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	if !constructionRouteHasCompilerProof(ctx, base.Authorized, resolvedRoute) {
		return result, unprocessable("related-expand-contributors", "UNSUPPORTED_CONSTRUCTION_ROUTE", "the selected route is no longer supported by the compiler", nil)
	}
	if cursor := request.Cursor; cursor != "" {
		decoded, decodeErr := decodeRelatedExpandContributorChoiceCursor(cursor)
		if decodeErr != nil || decoded.Version != 1 || decoded.Snapshot != request.SnapshotToken ||
			decoded.DraftVersion != base.Owner.DraftVersion || decoded.DraftDigest != base.Owner.DraftDigest ||
			decoded.OutputID != request.OutputID || decoded.StageID != request.StageID ||
			decoded.RouteChoice != request.RouteChoiceID || decoded.Query != request.Query || decoded.Offset < 0 {
			return result, conflict("related-expand-contributors", "STALE_OR_INVALID_CHOICE_CURSOR", "restart contributor search for this route and stage", nil, decodeErr)
		}
	}
	candidates := make([]capability.Candidate, 0)
	query := strings.ToLower(request.Query)
	for _, candidate := range base.Snapshot.Candidates {
		if candidate.NodeID != selected.NodeID || candidate.ResourceType != selected.ResourceType ||
			!relatedContributorCandidateExecutable(candidate) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(candidate.Label), query) && !strings.Contains(strings.ToLower(candidate.FieldPath), query) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	offset := 0
	if request.Cursor != "" {
		decoded, _ := decodeRelatedExpandContributorChoiceCursor(request.Cursor)
		offset = decoded.Offset
	}
	if offset > len(candidates) {
		return result, conflict("related-expand-contributors", "STALE_OR_INVALID_CHOICE_CURSOR", "restart contributor search for this route and stage", nil, nil)
	}
	limit := request.Limit
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 50 {
		return result, malformed("related-expand-contributors", "limit must be between 1 and 50", nil)
	}
	end := offset + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	for _, candidate := range candidates[offset:end] {
		proved, proofErr := proveConstructionCandidate(ctx, base.Authorized, anchor.ResourceType, candidate, resolvedRoute)
		if proofErr != nil || proved.NodeID != selected.NodeID || proved.ResourceType != selected.ResourceType ||
			!relatedContributorCandidateExecutable(proved) {
			continue
		}
		choice, choiceErr := capability.NewFieldConstructionChoiceForRoute(base.Snapshot.Token, resolvedRoute, proved)
		if choiceErr != nil {
			continue
		}
		operators := []string{"EXISTS"}
		logicalType := strings.ToLower(strings.TrimSpace(proved.LogicalType))
		if logicalType == "string" || logicalType == "code" {
			operators = append(operators, "EQUALS")
		}
		result.Choices = append(result.Choices, RelatedExpandContributorChoice{
			ChoiceID: choice.ChoiceID, Label: firstNonEmpty(proved.Label, proved.ResourceType+"."+proved.FieldPath),
			Source: authoringv2.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: proved.ID, NodeID: proved.NodeID,
				ResourceType: proved.ResourceType, Path: proved.FieldPath, Cardinality: proved.Cardinality,
				LogicalType: proved.LogicalType, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), proved.RepeatedBoundaries...),
			},
			Operators: operators, SuggestedValues: append([]string{}, proved.SuggestedValues...),
			SuggestionsComplete: proved.SuggestionsComplete, SuggestionsTruncated: proved.SuggestionsTruncated,
			SuggestionsSource: "catalog",
		})
	}
	result.Complete = end >= len(candidates)
	result.Truncated = !result.Complete
	if result.Truncated {
		cursor, encodeErr := encodeRelatedExpandContributorChoiceCursor(relatedExpandContributorChoiceCursor{
			Version: 1, Snapshot: request.SnapshotToken, DraftVersion: base.Owner.DraftVersion, DraftDigest: base.Owner.DraftDigest,
			OutputID: request.OutputID, StageID: request.StageID, RouteChoice: request.RouteChoiceID, Query: request.Query, Offset: end,
		})
		if encodeErr != nil {
			return RelatedExpandContributorChoiceSearchResponse{}, fmt.Errorf("encode related expansion contributor cursor: %w", encodeErr)
		}
		result.NextCursor = cursor
	}
	return result, nil
}

func relatedContributorCandidateCardinality(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "optional_one", "required_one", "many":
		return true
	default:
		return false
	}
}

func relatedContributorCandidateExecutable(candidate capability.Candidate) bool {
	if !relatedContributorCandidateCardinality(candidate.Cardinality) || !relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return false
	}
	selector, err := spec.ParseSelector(candidate.FieldPath)
	if err != nil || selector.Filter != nil || selector.CanonicalPath() != candidate.FieldPath {
		return false
	}
	repeated := strings.EqualFold(strings.TrimSpace(candidate.Cardinality), "many")
	if repeated {
		if len(candidate.RepeatedBoundaries) == 0 || !relatedContributorRepeatedBoundariesMatch(selector, candidate.RepeatedBoundaries) {
			return false
		}
	} else if len(candidate.RepeatedBoundaries) != 0 || !slices.Contains(candidate.ProjectionModes, capability.ProjectionScalar) {
		return false
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(candidate.ResourceType, selector.CanonicalPath())
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown || metadata.Repeated != repeated {
		return false
	}
	return relatedContributorLogicalTypeMatches(candidate.LogicalType, metadata.Primitive)
}

func relatedContributorRepeatedBoundariesMatch(selector spec.Selector, boundaries []capability.RepeatedBoundary) bool {
	want := make([]string, 0, len(boundaries))
	path := make([]string, 0, len(selector.Steps))
	for _, step := range selector.Steps {
		if step.Index != nil {
			return false
		}
		segment := step.Field
		if step.Iterate {
			segment += "[]"
		}
		path = append(path, segment)
		if step.Iterate {
			want = append(want, strings.Join(path, "."))
		}
	}
	if len(want) != len(boundaries) {
		return false
	}
	for index, boundary := range boundaries {
		if boundary.Path != want[index] || boundary.MaxItems < 0 {
			return false
		}
	}
	return len(want) != 0
}

func relatedContributorLogicalTypeMatches(logicalType string, primitive fhirschema.PrimitiveKind) bool {
	switch primitive {
	case fhirschema.PrimitiveString:
		return strings.EqualFold(logicalType, "string") || strings.EqualFold(logicalType, "code") || strings.EqualFold(logicalType, "uuid")
	case fhirschema.PrimitiveDate:
		return strings.EqualFold(logicalType, "date")
	case fhirschema.PrimitiveDateTime:
		return strings.EqualFold(logicalType, "date_time")
	case fhirschema.PrimitiveBoolean:
		return strings.EqualFold(logicalType, "boolean")
	case fhirschema.PrimitiveInteger:
		return strings.EqualFold(logicalType, "integer")
	case fhirschema.PrimitiveDecimal:
		return strings.EqualFold(logicalType, "decimal")
	default:
		return false
	}
}

func relatedFieldPathExecutable(path string) bool {
	_, err := spec.ParseDirectScalarSelector(path)
	return err == nil
}

func relatedFieldLogicalTypeExecutable(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "string", "date", "date_time", "code", "uuid", "integer", "decimal", "boolean":
		return true
	default:
		return false
	}
}

func encodeRelatedFieldChoiceCursor(cursor relatedFieldChoiceCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeRelatedFieldChoiceCursor(encoded string) (relatedFieldChoiceCursor, error) {
	var cursor relatedFieldChoiceCursor
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return cursor, err
	}
	return cursor, nil
}

func encodeRelatedExpandContributorChoiceCursor(cursor relatedExpandContributorChoiceCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeRelatedExpandContributorChoiceCursor(encoded string) (relatedExpandContributorChoiceCursor, error) {
	var cursor relatedExpandContributorChoiceCursor
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return cursor, err
	}
	return cursor, nil
}

type ResolvedPopulationRouteChoice struct {
	Route       []capability.ConstructionRouteStep
	SelectionID string
}

func (s constructionRouteFlow) searchRelatedExpandChoices(ctx context.Context, request RelatedExpandChoiceSearchRequest) (RelatedExpandChoiceSearchResponse, error) {
	result := RelatedExpandChoiceSearchResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, StageID: request.StageID,
		Choices: []RelatedExpandRouteChoice{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		strings.TrimSpace(request.StageID) == "" || strings.TrimSpace(request.AnchorColumnID) == "" || strings.TrimSpace(request.TargetResourceType) == "" ||
		strings.TrimSpace(request.Cursor) != request.Cursor || len(request.Cursor) > 4096 ||
		strings.TrimSpace(request.ExpectedDraftDigest) == "" || request.ExpectedDraftVersion < 1 {
		return result, malformed("related-expand-choices", "project, explorer, snapshot, draft, output, stage, and target resource identities are required", nil)
	}
	if s.capability.ForCompilation == nil || s.capability.Catalog == nil {
		return result, unavailable("related-expand-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.Owner.DraftVersion, base.Owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.Stages {
		if base.Stages[index].ID == request.StageID {
			stage = &base.Stages[index]
			break
		}
	}
	if stage == nil {
		return result, conflict("related-expand-choices", "STALE_STAGE_REFERENCE", "the selected stage is not in the current compiled output", nil, nil)
	}
	if !constructionStageSupportsRelatedExpand(*stage) {
		return result, unprocessable("related-expand-choices", "NO_SOURCE_ROW_ANCHOR", "the selected stage does not expose executable related expansion", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.Snapshot, *stage, base.Document.RootResourceType, request.AnchorColumnID)
	if err != nil {
		return result, unprocessable("related-expand-choices", "NO_SOURCE_ROW_ANCHOR", "the selected stage does not retain that exact resource anchor", err)
	}
	if err := validateAuthorizedReadScope(base.Authorized.Scope, base.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-expand-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	targetNodeIDs := make([]string, 0)
	for _, node := range base.Snapshot.Nodes {
		if node.ID != "" && node.ResourceType == request.TargetResourceType {
			targetNodeIDs = append(targetNodeIDs, node.ID)
		}
	}
	if len(targetNodeIDs) == 0 && !request.SavedGraphOnly {
		result.Complete = true
		return result, nil
	}
	var routes [][]capability.ConstructionRouteStep
	if request.SavedGraphOnly {
		if anchor.Kind == "root" || anchor.Kind == "rootContributors" {
			routes = savedRelatedExpandRoutes(base.Snapshot, base.Document.Route, anchor, request.TargetResourceType)
		}
		cursor := savedRelatedExpandCursor{
			Snapshot: request.SnapshotToken, Draft: request.ExpectedDraftDigest, Output: request.OutputID,
			Stage: request.StageID, Anchor: request.AnchorColumnID, Target: request.TargetResourceType,
		}
		var pageErr error
		routes, result.NextCursor, pageErr = pageSavedRelatedExpandRoutes(routes, cursor, request.Cursor, request.Limit)
		if pageErr != nil {
			return result, conflict("related-expand-choices", "STALE_OR_INVALID_ROUTE_CURSOR", "restart saved graph path search", nil, pageErr)
		}
		result.Complete = result.NextCursor == ""
	} else {
		page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
			Snapshot: base.Snapshot, RootNodeID: anchor.NodeID, TargetNodeIDs: targetNodeIDs,
			SourceKey: "related-resource:" + request.StageID + ":" + anchor.ColumnID + ":" + anchor.Kind + ":" + anchor.NodeID + ":" + request.TargetResourceType,
			Cursor:    request.Cursor, Limit: request.Limit,
		})
		if err != nil {
			return result, conflict("related-expand-choices", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this stage and resource type", nil, err)
		}
		result.Complete, result.Truncated, result.NextCursor = page.Complete, page.Truncated, page.NextCursor
		routes = page.Routes
	}
	proofs := make(map[capability.ConstructionRouteStep]bool)
	for _, route := range routes {
		if len(route) == 0 {
			continue
		}
		targetNodeID := route[len(route)-1].ToNodeID
		resolved, routeErr := reauthorizeConstructionRouteFromAnchor(base.Snapshot, anchor.NodeID, targetNodeID, route)
		if routeErr != nil || !constructionRouteHasCompilerProofCached(ctx, base.Authorized, resolved, proofs) {
			continue
		}
		choice, choiceErr := capability.NewConstructionRelatedResourceRouteChoiceFromAnchor(
			base.Snapshot.Token, request.StageID, anchor.ColumnID, anchor.Kind, anchor.NodeID, anchor.ResourceType,
			targetNodeID, request.TargetResourceType, resolved,
		)
		if choiceErr != nil {
			continue
		}
		result.Choices = append(result.Choices, RelatedExpandRouteChoice{
			ChoiceID: choice.ChoiceID, TargetNodeID: choice.TargetNodeID,
			TargetResourceType: choice.TargetResource, Route: choice.Route,
			AnchorColumnID: choice.AnchorColumnID, AnchorKind: choice.AnchorKind, NodeID: choice.AnchorNodeID,
			ResourceType: choice.AnchorResourceType, AnchorLabel: anchor.Label,
		})
	}
	return result, nil
}

type savedRelatedExpandCursor struct {
	Snapshot, Draft, Output, Stage, Anchor, Target string
	Offset                                         int
}

func pageSavedRelatedExpandRoutes(routes [][]capability.ConstructionRouteStep, scope savedRelatedExpandCursor, encoded string, limit int) ([][]capability.ConstructionRouteStep, string, error) {
	offset := 0
	if encoded != "" {
		var cursor savedRelatedExpandCursor
		value, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || json.Unmarshal(value, &cursor) != nil {
			return nil, "", errors.New("invalid saved graph route cursor")
		}
		offset = cursor.Offset
		cursor.Offset = 0
		if cursor != scope || offset <= 0 || offset >= len(routes) {
			return nil, "", errors.New("saved graph route cursor has a stale scope or offset")
		}
	}
	if limit <= 0 {
		limit = 12
	}
	end := min(offset+min(limit, 50), len(routes))
	if end == len(routes) {
		return routes[offset:end], "", nil
	}
	scope.Offset = end
	next, err := json.Marshal(scope)
	if err != nil {
		return nil, "", err
	}
	return routes[offset:end], base64.RawURLEncoding.EncodeToString(next), nil
}

func savedRelatedExpandRoutes(snapshot capability.Snapshot, root authoringv2.RouteNode, anchor resolvedRelatedExpandAnchor, target string) [][]capability.ConstructionRouteStep {
	var routes [][]capability.ConstructionRouteStep
	var visit func(authoringv2.RouteNode, string, []capability.ConstructionRouteStep)
	visit = func(parent authoringv2.RouteNode, nodeID string, path []capability.ConstructionRouteStep) {
		if snapshot.Policy.Route.MaxHops > 0 && len(path) >= snapshot.Policy.Route.MaxHops {
			return
		}
		for _, child := range parent.Children {
			edge, err := resolveCapabilityRouteEdge(snapshot, nodeID, parent, child)
			if err != nil {
				continue
			}
			next := append(slices.Clone(path), capability.ConstructionRouteStep{
				EdgeID: edge.ID, FromNodeID: nodeID, ToNodeID: edge.ToNodeID,
				FromResourceType: parent.ResourceType, ToResourceType: child.ResourceType,
				Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)),
				MatchMode: string(child.MatchMode.Normalized()),
			})
			if child.ResourceType == target {
				routes = append(routes, next)
			}
			visit(child, edge.ToNodeID, next)
		}
	}
	visit(root, anchor.NodeID, nil)
	return routes
}

func constructionStageSupportsRelatedExpand(stage explorer.ReceiptConstructionStage) bool {
	for _, operation := range stage.Capabilities {
		if operation.Kind == "RELATED_EXPAND" {
			return operation.Supported
		}
	}
	return false
}

type resolvedRelatedExpandAnchor struct {
	ColumnID     string
	Kind         string
	NodeID       string
	ResourceType string
	Label        string
}

func resolveRelatedExpandAnchor(snapshot capability.Snapshot, stage explorer.ReceiptConstructionStage, rootResourceType, columnID string) (resolvedRelatedExpandAnchor, error) {
	for _, anchor := range stage.RelatedExpandAnchors {
		if anchor.AnchorColumnID != columnID {
			continue
		}
		resolved := resolvedRelatedExpandAnchor{
			ColumnID: anchor.AnchorColumnID, Kind: anchor.Kind,
			ResourceType: anchor.ResourceType, Label: anchor.Label,
		}
		switch anchor.Kind {
		case "root", "rootContributors":
			if anchor.ResourceType != rootResourceType || rootResourceType == "" ||
				anchor.Kind == "root" && (anchor.AnchorColumnID != "_key" || anchor.NodeID != "") ||
				anchor.Kind == "rootContributors" && (anchor.AnchorColumnID != "__loom_root_contributor_keys" || anchor.NodeID != "") {
				return resolvedRelatedExpandAnchor{}, fmt.Errorf("compiler root anchor differs from the output root resource")
			}
			for _, node := range snapshot.Nodes {
				if !node.RowRootEligible || node.ResourceType != rootResourceType {
					continue
				}
				if resolved.NodeID != "" {
					return resolvedRelatedExpandAnchor{}, fmt.Errorf("row root is ambiguous")
				}
				resolved.NodeID = node.ID
			}
		case "activeRelatedRecord":
			active := stage.ActiveRelatedRecord
			if active == nil || active.TerminalIdentityColumn != anchor.AnchorColumnID ||
				active.TargetNodeID != anchor.NodeID || active.TargetResourceType != anchor.ResourceType {
				return resolvedRelatedExpandAnchor{}, fmt.Errorf("compiler active anchor differs from the selected stage terminal identity")
			}
			resolved.NodeID = anchor.NodeID
		default:
			return resolvedRelatedExpandAnchor{}, fmt.Errorf("compiler returned unsupported row anchor kind %q", anchor.Kind)
		}
		if resolved.NodeID == "" {
			return resolvedRelatedExpandAnchor{}, fmt.Errorf("row anchor node is unavailable")
		}
		if node, ok := snapshot.Node(resolved.NodeID); !ok || node.ResourceType != resolved.ResourceType {
			return resolvedRelatedExpandAnchor{}, fmt.Errorf("row anchor no longer identifies the exact resource node")
		}
		return resolved, nil
	}
	return resolvedRelatedExpandAnchor{}, fmt.Errorf("anchor column %q is not compiler-proven for stage %q", columnID, stage.ID)
}
