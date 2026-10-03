package lifecycle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

type ConstructionChoiceSearchSource struct {
	Kind         capability.ConstructionChoiceSourceKind `json:"kind"`
	CandidateID  string                                  `json:"candidateId,omitempty"`
	ContextToken string                                  `json:"contextToken,omitempty"`
	BuildID      string                                  `json:"buildId,omitempty"`
	ConceptID    string                                  `json:"conceptId,omitempty"`
	BindingID    string                                  `json:"bindingId,omitempty"`
}

type ConstructionChoiceSearchRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
	OccurrenceID  string
	Source        ConstructionChoiceSearchSource
	Limit         int
	Cursor        string
}

type ConstructionChoiceSearchResponse struct {
	SnapshotToken string                          `json:"snapshotToken"`
	OutputID      string                          `json:"outputId"`
	Complete      bool                            `json:"complete"`
	Truncated     bool                            `json:"truncated"`
	NextCursor    string                          `json:"nextCursor,omitempty"`
	Choices       []capability.ConstructionChoice `json:"choices"`
}

type RelatedExpandChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	AnchorColumnID       string
	TargetResourceType   string
	Limit                int
	Cursor               string
}

type RelatedExpandRouteChoice struct {
	ChoiceID           string                             `json:"choiceId"`
	AnchorColumnID     string                             `json:"anchorColumnId"`
	AnchorKind         string                             `json:"kind"`
	NodeID             string                             `json:"nodeId"`
	ResourceType       string                             `json:"resourceType"`
	AnchorLabel        string                             `json:"label"`
	TargetNodeID       string                             `json:"targetNodeId"`
	TargetResourceType string                             `json:"targetResourceType"`
	Route              []capability.ConstructionRouteStep `json:"route"`
}

type RelatedExpandChoiceSearchResponse struct {
	SnapshotToken string                     `json:"snapshotToken"`
	DraftVersion  int64                      `json:"draftVersion"`
	DraftDigest   string                     `json:"draftDigest"`
	OutputID      string                     `json:"outputId"`
	StageID       string                     `json:"stageId"`
	Complete      bool                       `json:"complete"`
	Truncated     bool                       `json:"truncated"`
	NextCursor    string                     `json:"nextCursor,omitempty"`
	Choices       []RelatedExpandRouteChoice `json:"choices"`
}

type RelatedFieldChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	Query                string
	Limit                int
	Cursor               string
}

type RelatedFieldChoice struct {
	ChoiceID string                                     `json:"choiceId"`
	Label    string                                     `json:"label"`
	Source   authoringv2.ConstructionRelatedFieldSource `json:"source"`
}

type RelatedFieldChoiceSearchResponse struct {
	SnapshotToken string               `json:"snapshotToken"`
	DraftVersion  int64                `json:"draftVersion"`
	DraftDigest   string               `json:"draftDigest"`
	OutputID      string               `json:"outputId"`
	StageID       string               `json:"stageId"`
	Complete      bool                 `json:"complete"`
	Truncated     bool                 `json:"truncated"`
	NextCursor    string               `json:"nextCursor,omitempty"`
	Choices       []RelatedFieldChoice `json:"choices"`
}

type RelatedExpandContributorChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	RouteChoiceID        string
	Query                string
	Limit                int
	Cursor               string
}

type RelatedExpandContributorChoice struct {
	ChoiceID             string                                     `json:"choiceId"`
	Source               authoringv2.ConstructionRelatedFieldSource `json:"source"`
	Label                string                                     `json:"label"`
	Operators            []string                                   `json:"operators"`
	SuggestedValues      []string                                   `json:"suggestedValues"`
	SuggestionsComplete  bool                                       `json:"suggestionsComplete"`
	SuggestionsTruncated bool                                       `json:"suggestionsTruncated"`
	SuggestionsSource    string                                     `json:"suggestionsSource"`
}

type RelatedExpandContributorChoiceSearchResponse struct {
	SnapshotToken string                           `json:"snapshotToken"`
	DraftVersion  int64                            `json:"draftVersion"`
	DraftDigest   string                           `json:"draftDigest"`
	OutputID      string                           `json:"outputId"`
	StageID       string                           `json:"stageId"`
	RouteChoiceID string                           `json:"routeChoiceId"`
	Complete      bool                             `json:"complete"`
	Truncated     bool                             `json:"truncated"`
	NextCursor    string                           `json:"nextCursor,omitempty"`
	Choices       []RelatedExpandContributorChoice `json:"choices"`
}

type relatedExpandContributorChoiceCursor struct {
	Version      int    `json:"version"`
	Snapshot     string `json:"snapshot"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	OutputID     string `json:"outputId"`
	StageID      string `json:"stageId"`
	RouteChoice  string `json:"routeChoice"`
	Query        string `json:"query"`
	Offset       int    `json:"offset"`
}

type relatedFieldChoiceCursor struct {
	Version      int    `json:"version"`
	Snapshot     string `json:"snapshot"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	OutputID     string `json:"outputId"`
	StageID      string `json:"stageId"`
	Query        string `json:"query"`
	Offset       int    `json:"offset"`
}

// SearchRelatedFieldChoices returns compiler-proved scalar fields on the
// active exact terminal resource at one current output stage.
func (s *Service) SearchRelatedFieldChoices(ctx context.Context, request RelatedFieldChoiceSearchRequest) (RelatedFieldChoiceSearchResponse, error) {
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
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("related-field-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.owner.DraftVersion, base.owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == request.StageID {
			stage = &base.stages[index]
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
	if err := validateAuthorizedReadScope(base.authorized.Scope, base.snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-field-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	if cursor := request.Cursor; cursor != "" {
		decoded, decodeErr := decodeRelatedFieldChoiceCursor(cursor)
		if decodeErr != nil || decoded.Version != 1 || decoded.Snapshot != request.SnapshotToken ||
			decoded.DraftVersion != base.owner.DraftVersion || decoded.DraftDigest != base.owner.DraftDigest ||
			decoded.OutputID != request.OutputID || decoded.StageID != request.StageID || decoded.Query != request.Query || decoded.Offset < 0 {
			return result, conflict("related-field-choices", "STALE_OR_INVALID_CHOICE_CURSOR", "restart exact field search for this output stage", nil, decodeErr)
		}
	}
	candidates := make([]capability.Candidate, 0)
	query := strings.ToLower(request.Query)
	for _, candidate := range base.snapshot.Candidates {
		if candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
			(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
			len(candidate.RepeatedBoundaries) != 0 || !containsProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
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
		proved, proofErr := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
		if proofErr != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
			(proved.Cardinality != "optional_one" && proved.Cardinality != "required_one") || len(proved.RepeatedBoundaries) != 0 ||
			!relatedFieldPathExecutable(proved.FieldPath) || !relatedFieldLogicalTypeExecutable(proved.LogicalType) {
			continue
		}
		choice, choiceErr := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, request.StageID, proved)
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
			Version: 1, Snapshot: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
			OutputID: request.OutputID, StageID: request.StageID, Query: request.Query, Offset: end,
		})
		if encodeErr != nil {
			return RelatedFieldChoiceSearchResponse{}, fmt.Errorf("encode related field cursor: %w", encodeErr)
		}
		result.NextCursor = cursor
	}
	return result, nil
}

// SearchRelatedExpandContributorChoices lists compiler-proved scalar leaves,
// including explicitly bounded repeated leaves, on the route's terminal resource.
// The route token and catalog suggestions are bound to the current draft, snapshot, and stage.
func (s *Service) SearchRelatedExpandContributorChoices(ctx context.Context, request RelatedExpandContributorChoiceSearchRequest) (RelatedExpandContributorChoiceSearchResponse, error) {
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
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("related-expand-contributors", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.owner.DraftVersion, base.owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == request.StageID {
			stage = &base.stages[index]
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
	if identity.SnapshotToken != base.snapshot.Token {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion route for the current authorized snapshot", nil, nil)
	}
	selected, ok := identity.Source.(capability.RelatedResourceChoiceSource)
	if !ok || selected.StageID != request.StageID {
		return result, unprocessable("related-expand-contributors", "INVALID_CONSTRUCTION_CHOICE", "the selected route choice does not belong to this stage", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.snapshot, *stage, base.document.RootResourceType, selected.AnchorColumnID)
	if err != nil || selected.AnchorKind != anchor.Kind || selected.AnchorNodeID != anchor.NodeID || selected.AnchorResourceType != anchor.ResourceType {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "the selected route no longer matches this stage's exact row anchor", nil, err)
	}
	resolvedRoute, err := reauthorizeConstructionRouteFromAnchor(base.snapshot, anchor.NodeID, selected.NodeID, identity.Route)
	if err != nil || !reflect.DeepEqual(resolvedRoute, identity.Route) || len(resolvedRoute) == 0 ||
		resolvedRoute[len(resolvedRoute)-1].ToResourceType != selected.ResourceType {
		return result, conflict("related-expand-contributors", "STALE_CONSTRUCTION_CHOICE", "the selected route no longer resolves to its exact terminal resource", nil, err)
	}
	if err := validateAuthorizedReadScope(base.authorized.Scope, base.snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-expand-contributors", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	if !constructionRouteHasCompilerProof(ctx, base.authorized, resolvedRoute) {
		return result, unprocessable("related-expand-contributors", "UNSUPPORTED_CONSTRUCTION_ROUTE", "the selected route is no longer supported by the compiler", nil)
	}
	if cursor := request.Cursor; cursor != "" {
		decoded, decodeErr := decodeRelatedExpandContributorChoiceCursor(cursor)
		if decodeErr != nil || decoded.Version != 1 || decoded.Snapshot != request.SnapshotToken ||
			decoded.DraftVersion != base.owner.DraftVersion || decoded.DraftDigest != base.owner.DraftDigest ||
			decoded.OutputID != request.OutputID || decoded.StageID != request.StageID ||
			decoded.RouteChoice != request.RouteChoiceID || decoded.Query != request.Query || decoded.Offset < 0 {
			return result, conflict("related-expand-contributors", "STALE_OR_INVALID_CHOICE_CURSOR", "restart contributor search for this route and stage", nil, decodeErr)
		}
	}
	candidates := make([]capability.Candidate, 0)
	query := strings.ToLower(request.Query)
	for _, candidate := range base.snapshot.Candidates {
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
		proved, proofErr := proveConstructionCandidate(ctx, base.authorized, anchor.ResourceType, candidate, resolvedRoute)
		if proofErr != nil || proved.NodeID != selected.NodeID || proved.ResourceType != selected.ResourceType ||
			!relatedContributorCandidateExecutable(proved) {
			continue
		}
		choice, choiceErr := capability.NewFieldConstructionChoiceForRoute(base.snapshot.Token, resolvedRoute, proved)
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
			Version: 1, Snapshot: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
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
	} else if len(candidate.RepeatedBoundaries) != 0 || !containsProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) {
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

func containsProjectionMode(modes []capability.ProjectionMode, want capability.ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
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

// SearchRelatedExpandChoices returns server-issued exact routes from the
// selected compiler stage's retained root resource anchor.
func (s *Service) SearchRelatedExpandChoices(ctx context.Context, request RelatedExpandChoiceSearchRequest) (RelatedExpandChoiceSearchResponse, error) {
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
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("related-expand-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken,
		request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return result, err
	}
	result.DraftVersion, result.DraftDigest = base.owner.DraftVersion, base.owner.DraftDigest
	var stage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == request.StageID {
			stage = &base.stages[index]
			break
		}
	}
	if stage == nil {
		return result, conflict("related-expand-choices", "STALE_STAGE_REFERENCE", "the selected stage is not in the current compiled output", nil, nil)
	}
	if !constructionStageSupportsRelatedExpand(*stage) {
		return result, unprocessable("related-expand-choices", "NO_SOURCE_ROW_ANCHOR", "the selected stage does not expose executable related expansion", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.snapshot, *stage, base.document.RootResourceType, request.AnchorColumnID)
	if err != nil {
		return result, unprocessable("related-expand-choices", "NO_SOURCE_ROW_ANCHOR", "the selected stage does not retain that exact resource anchor", err)
	}
	if err := validateAuthorizedReadScope(base.authorized.Scope, base.snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("related-expand-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	targetNodeIDs := make([]string, 0)
	for _, node := range base.snapshot.Nodes {
		if node.ID != "" && node.ResourceType == request.TargetResourceType {
			targetNodeIDs = append(targetNodeIDs, node.ID)
		}
	}
	if len(targetNodeIDs) == 0 {
		result.Complete = true
		return result, nil
	}
	page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
		Snapshot: base.snapshot, RootNodeID: anchor.NodeID, TargetNodeIDs: targetNodeIDs,
		SourceKey: "related-resource:" + request.StageID + ":" + anchor.ColumnID + ":" + anchor.Kind + ":" + anchor.NodeID + ":" + request.TargetResourceType,
		Cursor:    request.Cursor, Limit: request.Limit,
	})
	if err != nil {
		return result, conflict("related-expand-choices", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this stage and resource type", nil, err)
	}
	result.Complete, result.Truncated, result.NextCursor = page.Complete, page.Truncated, page.NextCursor
	for _, route := range page.Routes {
		if len(route) == 0 {
			continue
		}
		targetNodeID := route[len(route)-1].ToNodeID
		resolved, routeErr := reauthorizeConstructionRouteFromAnchor(base.snapshot, anchor.NodeID, targetNodeID, route)
		if routeErr != nil || !constructionRouteHasCompilerProof(ctx, base.authorized, resolved) {
			continue
		}
		choice, choiceErr := capability.NewConstructionRelatedResourceRouteChoiceFromAnchor(
			base.snapshot.Token, request.StageID, anchor.ColumnID, anchor.Kind, anchor.NodeID, anchor.ResourceType,
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

func constructionRouteHasCompilerProof(ctx context.Context, authorized AuthorizedCapability, route []capability.ConstructionRouteStep) bool {
	if len(route) == 0 {
		return false
	}
	for _, hop := range route {
		matchMode := spec.TraversalMatchOptional
		if hop.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		proof, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: hop.FromResourceType,
			Traversal: compilerprobe.Traversal{
				FromResourceType: hop.FromResourceType, EdgeLabel: hop.Relationship,
				ToResourceType: hop.ToResourceType, MatchMode: matchMode,
			},
		})
		if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != hop.StorageDirection {
			return false
		}
	}
	return true
}

func resolveCapabilityRouteEdge(snapshot capability.Snapshot, parentNodeID string, parent, child authoringv2.RouteNode) (capability.Edge, error) {
	valid := func(edge capability.Edge) bool {
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		return edge.ID != "" && edge.BlockedReason == "" && edge.FromNodeID == parentNodeID && edge.Label == child.Relationship &&
			fromOK && toOK && from.ResourceType == parent.ResourceType && to.ResourceType == child.ResourceType &&
			(edge.SourceResourceType == "" || edge.SourceResourceType == parent.ResourceType) && (edge.TargetResourceType == "" || edge.TargetResourceType == child.ResourceType) &&
			(direction == "" || direction == "INBOUND" || direction == "OUTBOUND")
	}
	if child.CatalogEdgeID != "" {
		edge, found := snapshot.Edge(child.CatalogEdgeID)
		if !found || !valid(edge) {
			return capability.Edge{}, fmt.Errorf("catalog edge %q no longer identifies the saved route step", child.CatalogEdgeID)
		}
		return edge, nil
	}
	var match capability.Edge
	for _, edge := range snapshot.Edges {
		if !valid(edge) {
			continue
		}
		if match.ID != "" {
			return capability.Edge{}, fmt.Errorf("saved route step resolves to multiple capability edges")
		}
		match = edge
	}
	if match.ID == "" {
		return capability.Edge{}, fmt.Errorf("saved route step no longer resolves to a capability edge")
	}
	return match, nil
}

func reauthorizeConstructionRoute(snapshot capability.Snapshot, rootResourceType, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			if root.ID != "" {
				return nil, fmt.Errorf("row root is ambiguous")
			}
			root = node
		}
	}
	if root.ID == "" {
		return nil, fmt.Errorf("row root is unavailable")
	}
	return reauthorizeConstructionRouteFromAnchor(snapshot, root.ID, targetNodeID, route)
}

func reauthorizeConstructionRouteFromAnchor(snapshot capability.Snapshot, anchorNodeID, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	current, ok := snapshot.Node(anchorNodeID)
	if !ok || current.ID == "" {
		return nil, fmt.Errorf("row resource anchor is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(route) > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("route exceeds the current policy")
	}
	seenEdges := make(map[string]bool, len(route))
	resolved := make([]capability.ConstructionRouteStep, 0, len(route))
	for index, step := range route {
		if strings.TrimSpace(step.EdgeID) == "" || step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return nil, fmt.Errorf("route step %d is incomplete", index)
		}
		edge, found := snapshot.Edge(step.EdgeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		if !found || edge.BlockedReason != "" || edge.FromNodeID != current.ID ||
			edge.Label != step.Relationship || direction != strings.ToUpper(strings.TrimSpace(step.StorageDirection)) ||
			(edge.SourceResourceType != "" && edge.SourceResourceType != step.FromResourceType) || (edge.TargetResourceType != "" && edge.TargetResourceType != step.ToResourceType) ||
			edge.FromNodeID != step.FromNodeID || edge.ToNodeID != step.ToNodeID {
			return nil, fmt.Errorf("route step %d no longer identifies the authorized capability edge", index)
		}
		if current.ResourceType != step.FromResourceType {
			return nil, fmt.Errorf("route step %d does not extend its parent", index)
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && seenEdges[edge.ID] {
			return nil, fmt.Errorf("route repeats an edge forbidden by current policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return nil, fmt.Errorf("route contains a self-loop forbidden by current policy")
		}
		if direction != "" && direction != "INBOUND" && direction != "OUTBOUND" {
			return nil, fmt.Errorf("route has an unsupported storage direction")
		}
		to, found := snapshot.Node(edge.ToNodeID)
		if !found || to.ResourceType != step.ToResourceType {
			return nil, fmt.Errorf("route target is unavailable")
		}
		seenEdges[edge.ID] = true
		resolved = append(resolved, step)
		current = to
	}
	if current.ID != targetNodeID {
		return nil, fmt.Errorf("route does not end at the exact source node")
	}
	return resolved, nil
}

func proveConstructionCandidate(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for index, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := compilerprobe.Traversal{
			FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship,
			ToResourceType: step.ToResourceType, MatchMode: matchMode,
		}
		provenEdge, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || provenEdge.Traversal == nil || string(provenEdge.Traversal.StorageDirection) != step.StorageDirection {
			return capability.Candidate{}, fmt.Errorf("route step %d compiler proof changed: %w", index, err)
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	proof, err := compilerprobe.ProbeCandidate(ctx, compilerprobe.CandidateRequest{
		Scope: constructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: candidate.ResourceType, FieldRef: candidate.ResourceType + "." + candidate.FieldPath,
		Selector: candidate.FieldPath, Route: compilerRoute,
	})
	if err != nil || proof.Candidate == nil {
		if err == nil {
			err = fmt.Errorf("compiler returned no candidate proof")
		}
		return capability.Candidate{}, err
	}
	updated := candidate
	updated.LogicalType = string(proof.Candidate.Primitive)
	updated.Cardinality = string(proof.Candidate.Cardinality)
	updated.ProjectionModes = make([]capability.ProjectionMode, 0, len(proof.Candidate.ProjectionModes))
	for _, mode := range proof.Candidate.ProjectionModes {
		updated.ProjectionModes = append(updated.ProjectionModes, capability.ProjectionMode(strings.ToUpper(string(mode))))
	}
	return updated, nil
}

func constructionCompilerScope(authorized AuthorizedCapability) compilerprobe.Scope {
	return compilerprobe.Scope{
		Project: authorized.Snapshot.Identity.Project, DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
	}
}

func constructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	if choice.ChoiceID == submittedID {
		return true
	}
	return identity.Version == "construction-choice/v1" && len(identity.Route) == 0 && reflect.DeepEqual(choice.Source, identity.Source)
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	path, found := routePathToOccurrence(authoredRoot, occurrenceID)
	if !found || len(path) == 0 || path[0].OccurrenceID != authoringv2.RootOccurrenceID || path[0].ResourceType != rootResourceType {
		return nil, fmt.Errorf("occurrence is not in this output's saved route")
	}
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		if root.ID != "" {
			return nil, fmt.Errorf("authorized row root is ambiguous")
		}
		root = node
	}
	if root.ID == "" {
		return nil, fmt.Errorf("authorized row root is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(path)-1 > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("saved route exceeds the current route policy")
	}
	if len(path) == 1 {
		if root.ID != targetNodeID {
			return nil, fmt.Errorf("root occurrence does not match the candidate terminal")
		}
		return []capability.ConstructionRouteStep{}, nil
	}

	var matches [][]capability.ConstructionRouteStep
	var visit func(capability.Node, int, []capability.ConstructionRouteStep, map[string]bool) error
	visit = func(current capability.Node, pathIndex int, steps []capability.ConstructionRouteStep, usedEdges map[string]bool) error {
		if len(matches) > 1 {
			return nil
		}
		if pathIndex == len(path) {
			if current.ID == targetNodeID {
				matches = append(matches, cloneConstructionRoute(steps))
			}
			return nil
		}
		parent := path[pathIndex-1]
		authored := path[pathIndex]
		matchMode := string(authored.MatchMode.Normalized())
		edge, err := resolveCapabilityRouteEdge(snapshot, current.ID, parent, authored)
		if err != nil {
			return err
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && usedEdges[edge.ID] {
			return fmt.Errorf("saved route repeats an edge forbidden by policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return fmt.Errorf("saved route contains a self-loop forbidden by policy")
		}
		to, _ := snapshot.Node(edge.ToNodeID)
		next := append(cloneConstructionRoute(steps), capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: matchMode,
		})
		nextUsed := make(map[string]bool, len(usedEdges)+1)
		for id, used := range usedEdges {
			nextUsed[id] = used
		}
		nextUsed[edge.ID] = true
		if err := visit(to, pathIndex+1, next, nextUsed); err != nil {
			return err
		}
		return nil
	}
	if err := visit(root, 1, nil, map[string]bool{}); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("saved route does not resolve to the exact candidate terminal")
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("saved route has multiple authorized capability edge resolutions")
	}
	return reauthorizeConstructionRoute(snapshot, rootResourceType, targetNodeID, matches[0])
}

func routePathToOccurrence(root authoringv2.RouteNode, occurrenceID string) ([]authoringv2.RouteNode, bool) {
	if root.OccurrenceID == occurrenceID {
		return []authoringv2.RouteNode{root}, true
	}
	for _, child := range root.Children {
		path, found := routePathToOccurrence(child, occurrenceID)
		if !found {
			continue
		}
		return append([]authoringv2.RouteNode{root}, path...), true
	}
	return nil, false
}

// SearchConstructionChoices resolves one exact field or semantic identity for
// the current table output, enumerates its distinct directed routes, and only
// returns choices that compile with that full route.
func (s *Service) SearchConstructionChoices(ctx context.Context, request ConstructionChoiceSearchRequest) (ConstructionChoiceSearchResponse, error) {
	result := ConstructionChoiceSearchResponse{SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Choices: []capability.ConstructionChoice{}}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" || len(request.Cursor) > 4096 {
		return result, malformed("construction-choices", "project, explorerId, snapshotToken, and outputId are required", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("construction-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	identityKey, err := constructionSearchSourceKey(request.Source)
	if err != nil {
		return result, err
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
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
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, malformed("construction-choices", "outputId does not identify a valid row-rooted table", nil)
	}
	var candidate capability.Candidate
	var semanticEntry catalog.SemanticInventoryEntry
	semanticContext := ""
	buildID := ""
	if request.Source.Kind == capability.ConstructionChoiceSourceSemantic {
		semanticEntry, buildID, err = s.resolveConstructionSearchSemanticEntry(ctx, authorized, request)
		if err != nil {
			return result, err
		}
		semanticContext, err = semanticInventoryContextToken(snapshot, request.ExplorerID, document.RootResourceType, buildID)
		if err != nil || request.Source.ContextToken != semanticContext {
			return result, conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "reload the semantic choice for the current table root and inventory build", nil, err)
		}
		var found bool
		candidate, found = semanticConstructionCandidate(snapshot, semanticEntry.Observation)
		if !found || semanticEntry.Observation.Source.Type != candidate.ResourceType {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic binding and candidate resource do not match", nil)
		}
		plan := authoringv2.ResolveSemanticSelectionPlan(semanticEntry.Observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic source has no supported typed compiler binding", nil)
		}
	} else {
		var found bool
		candidate, found = uniqueCapabilityCandidate(snapshot, request.Source.CandidateID)
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
		if len(resolvedRoute) > 0 {
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
			ownerRecords := proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, semanticEntry.Observation, resolvedRoute)
			choice, err = semanticInventoryConstructionChoiceForRoute(snapshot, semanticContext, buildID, resolvedRoute, semanticEntry, provenCandidate, ownerRecords)
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

func constructionSearchSourceKey(source ConstructionChoiceSearchSource) (string, error) {
	switch source.Kind {
	case capability.ConstructionChoiceSourceField:
		if strings.TrimSpace(source.CandidateID) == "" || source.ContextToken != "" || source.BuildID != "" || source.ConceptID != "" || source.BindingID != "" {
			return "", malformed("construction-choices", "FIELD source requires only candidateId", nil)
		}
		return "field:" + source.CandidateID, nil
	case capability.ConstructionChoiceSourceSemantic:
		if strings.TrimSpace(source.ContextToken) == "" || strings.TrimSpace(source.BuildID) == "" || strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" || source.CandidateID != "" {
			return "", malformed("construction-choices", "SEMANTIC source requires contextToken, buildId, conceptId, and bindingId", nil)
		}
		return "semantic:" + source.ContextToken + "\x00" + source.BuildID + "\x00" + source.BindingID + "\x00" + source.ConceptID, nil
	default:
		return "", malformed("construction-choices", "source kind must be FIELD or SEMANTIC", nil)
	}
}

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	project = projectid.Canonical(project)
	owner, err := s.store.Get(ctx, project, explorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return authoringv2.Workspace{}, notFound("construction-choices", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return authoringv2.Workspace{}, internal("construction-choices", "EXPLORER_READ_FAILED", "the current Explorer workspace could not be loaded", err)
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace cannot be loaded", nil, activeErr)
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
		if decodeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the Explorer has no V2 authoring workspace", nil, nil)
}

func (s *Service) resolveConstructionSearchSemanticEntry(ctx context.Context, authorized AuthorizedCapability, request ConstructionChoiceSearchRequest) (catalog.SemanticInventoryEntry, string, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References: []catalog.SemanticInventoryReference{{ConceptID: request.Source.ConceptID, BindingID: request.Source.BindingID}},
	})
	if err != nil {
		return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "the selected semantic source could not be resolved", err)
	}
	expectedBuild := catalog.SemanticInventoryBuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete || resolved.Build.BuildID != expectedBuild || request.Source.BuildID != expectedBuild {
		return catalog.SemanticInventoryEntry{}, "", conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "the semantic source belongs to a different or incomplete inventory build", nil, nil)
	}
	var match catalog.SemanticInventoryEntry
	for _, entry := range resolved.Entries {
		if entry.ConceptID != request.Source.ConceptID || entry.BindingID != request.Source.BindingID || match.ConceptID != "" {
			return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated or duplicate source", nil)
		}
		match = entry
	}
	if match.ConceptID == "" {
		return catalog.SemanticInventoryEntry{}, "", unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact semantic source is unavailable in this authorized inventory", nil)
	}
	return match, expectedBuild, nil
}
