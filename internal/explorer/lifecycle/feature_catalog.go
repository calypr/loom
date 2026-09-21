package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	semanticfhir "github.com/calypr/loom/internal/fhir/semantic"
	"github.com/calypr/loom/internal/projectid"
)

type FeatureCatalogSection string

const (
	FeatureCatalogConcepts    FeatureCatalogSection = "CONCEPTS"
	FeatureCatalogFields      FeatureCatalogSection = "FIELDS"
	FeatureCatalogNeedsReview FeatureCatalogSection = "NEEDS_REVIEW"
)

type BrowseFeatureCatalogRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	RowRoot       string
	Section       FeatureCatalogSection
	ResourceType  string
	NodeID        string
	Query         string
	Cursor        string
	Limit         int
}

type FeatureCatalogSource struct {
	Kind        capability.ConstructionChoiceSourceKind `json:"kind"`
	CandidateID string                                  `json:"candidateId,omitempty"`
	ConceptID   string                                  `json:"conceptId,omitempty"`
	BindingID   string                                  `json:"bindingId,omitempty"`
}

type FeatureCatalogItem struct {
	Kind               string                                 `json:"kind"`
	FeatureID          string                                 `json:"featureId"`
	Title              string                                 `json:"title"`
	Description        string                                 `json:"description"`
	ResourceType       string                                 `json:"resourceType"`
	ValueType          string                                 `json:"valueType"`
	Cardinality        string                                 `json:"cardinality"`
	Occurrences        int64                                  `json:"occurrences"`
	Readiness          authoringv2.SemanticSelectionReadiness `json:"readiness"`
	Source             FeatureCatalogSource                   `json:"source"`
	SourceDetails      []capability.ConstructionChoiceFact    `json:"sourceDetails"`
	ConstructionChoice *capability.ConstructionChoice         `json:"constructionChoice,omitempty"`
}

type BrowseFeatureCatalogResponse struct {
	ContextToken       string                         `json:"contextToken"`
	BuildID            string                         `json:"buildId"`
	State              catalog.SemanticInventoryState `json:"state"`
	SourceAvailability string                         `json:"sourceAvailability"`
	Section            FeatureCatalogSection          `json:"section"`
	Entries            []FeatureCatalogItem           `json:"entries"`
	NextCursor         string                         `json:"nextCursor,omitempty"`
}

type featureCatalogCursor struct {
	Context string `json:"context"`
	Page    string `json:"page"`
}

func (s *Service) BrowseFeatureCatalog(ctx context.Context, req BrowseFeatureCatalogRequest) (BrowseFeatureCatalogResponse, error) {
	if err := validateFeatureCatalogRequest(req); err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	if req.Section == FeatureCatalogFields {
		return s.browseFeatureFields(ctx, req)
	}
	return s.browseSemanticFeatures(ctx, req)
}

func validateFeatureCatalogRequest(req BrowseFeatureCatalogRequest) error {
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || strings.TrimSpace(req.SnapshotToken) == "" || strings.TrimSpace(req.RowRoot) == "" || req.Limit < 0 || req.Limit > catalog.SemanticInventoryPageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return malformed("feature-catalog", "project, explorer, snapshotToken, rowRoot, section, and a page limit of at most 50 are required", nil)
	}
	switch req.Section {
	case FeatureCatalogConcepts, FeatureCatalogFields, FeatureCatalogNeedsReview:
		return nil
	default:
		return malformed("feature-catalog", "section must be CONCEPTS, FIELDS, or NEEDS_REVIEW", nil)
	}
}

func (s *Service) browseFeatureFields(ctx context.Context, req BrowseFeatureCatalogRequest) (BrowseFeatureCatalogResponse, error) {
	authorized, err := s.authorizedFeatureCatalog(ctx, req)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	snapshot := authorized.Snapshot
	contextToken, err := featureCatalogContextToken(snapshot, req.ExplorerID, req.RowRoot)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	cursorContext, err := featureCatalogCursorContext(contextToken, req)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	pageAfter, err := decodeFeatureCatalogCursor(req.Cursor, cursorContext)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return BrowseFeatureCatalogResponse{}, unavailable("feature-catalog", "CATALOG_UNAVAILABLE", "FHIR schema metadata is unavailable", nil)
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	candidates := append([]capability.Candidate(nil), snapshot.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		return featureFieldSortKey(candidates[i]) < featureFieldSortKey(candidates[j])
	})
	limit := req.Limit
	if limit == 0 {
		limit = catalog.SemanticInventoryPageLimit
	}
	result := BrowseFeatureCatalogResponse{
		ContextToken:       contextToken,
		State:              catalog.SemanticInventoryComplete,
		SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
		Section:            FeatureCatalogFields,
		Entries:            make([]FeatureCatalogItem, 0, limit),
	}
	lastKey := ""
	hasMore := false
	for _, candidate := range candidates {
		key := featureFieldSortKey(candidate)
		if pageAfter != "" && key <= pageAfter {
			continue
		}
		if !featureFieldMatches(snapshot, candidate, req, query) {
			continue
		}
		classification, classifyErr := semanticfhir.ClassifyDirectField(index, fhirschema.DefinitionName(candidate.ResourceType), candidate.FieldPath)
		if classifyErr != nil || !classification.Eligible {
			continue
		}
		if len(result.Entries) == limit {
			hasMore = true
			break
		}
		item := directFeatureCatalogItem(snapshot.Token, candidate)
		result.Entries = append(result.Entries, item)
		lastKey = key
	}
	if hasMore {
		result.NextCursor, err = encodeFeatureCatalogCursor(featureCatalogCursor{Context: cursorContext, Page: lastKey})
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
	}
	return result, nil
}

func (s *Service) browseSemanticFeatures(ctx context.Context, req BrowseFeatureCatalogRequest) (BrowseFeatureCatalogResponse, error) {
	// Decode only the feature-catalog envelope here. The semantic inventory
	// method validates its own build-bound inner cursor.
	outerContext := strings.Join([]string{req.SnapshotToken, req.ExplorerID, req.RowRoot, string(req.Section), req.ResourceType, req.NodeID, strings.TrimSpace(req.Query)}, "\x00")
	innerCursor, err := decodeFeatureCatalogCursor(req.Cursor, digestString(outerContext))
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = catalog.SemanticInventoryPageLimit
	}
	result := BrowseFeatureCatalogResponse{Section: req.Section, Entries: make([]FeatureCatalogItem, 0, limit)}
	next := innerCursor
	for scans := 0; scans < 20 && len(result.Entries) < limit; scans++ {
		page, browseErr := s.BrowseSemanticInventory(ctx, BrowseSemanticInventoryRequest{
			Project: req.Project, ExplorerID: req.ExplorerID, SnapshotToken: req.SnapshotToken,
			RowRoot: req.RowRoot, ResourceType: req.ResourceType, Query: req.Query,
			Cursor: next, Limit: limit - len(result.Entries),
		})
		if browseErr != nil {
			return BrowseFeatureCatalogResponse{}, browseErr
		}
		result.ContextToken, result.BuildID, result.State, result.SourceAvailability = page.ContextToken, page.BuildID, page.State, page.SourceAvailability
		for _, entry := range page.Entries {
			if !semanticFeatureInSection(entry, req.Section) {
				continue
			}
			result.Entries = append(result.Entries, semanticFeatureCatalogItem(entry))
		}
		previous := next
		next = page.NextCursor
		if next == "" {
			break
		}
		if next == previous {
			return BrowseFeatureCatalogResponse{}, unavailable("feature-catalog", "CATALOG_PAGINATION_STALLED", "semantic catalog pagination did not advance", nil)
		}
	}
	if next != "" {
		next, err = s.nextSemanticSectionCursor(ctx, req, next)
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
	}
	if next != "" {
		result.NextCursor, err = encodeFeatureCatalogCursor(featureCatalogCursor{Context: digestString(outerContext), Page: next})
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
	}
	return result, nil
}

func semanticFeatureInSection(entry SemanticInventoryItem, section FeatureCatalogSection) bool {
	addable := entry.Readiness.Addable()
	return section == FeatureCatalogConcepts && addable || section == FeatureCatalogNeedsReview && !addable
}

// nextSemanticSectionCursor makes the feature-catalog cursor truthful when an
// inventory page ends exactly at the requested limit. Pages belonging to the
// other section can be consumed safely; the cursor immediately before the
// first matching entry must be retained so the next request does not skip it.
// The probe preserves the original page size because inventory cursors bind it.
func (s *Service) nextSemanticSectionCursor(ctx context.Context, req BrowseFeatureCatalogRequest, cursor string) (string, error) {
	limit := req.Limit
	if limit == 0 {
		limit = catalog.SemanticInventoryPageLimit
	}
	for scans := 0; scans < 20 && cursor != ""; scans++ {
		resume := cursor
		page, err := s.BrowseSemanticInventory(ctx, BrowseSemanticInventoryRequest{
			Project: req.Project, ExplorerID: req.ExplorerID, SnapshotToken: req.SnapshotToken,
			RowRoot: req.RowRoot, ResourceType: req.ResourceType, Query: req.Query,
			Cursor: cursor, Limit: limit,
		})
		if err != nil {
			return "", err
		}
		for _, entry := range page.Entries {
			if semanticFeatureInSection(entry, req.Section) {
				return resume, nil
			}
		}
		if page.NextCursor == cursor {
			return "", unavailable("feature-catalog", "CATALOG_PAGINATION_STALLED", "semantic catalog pagination did not advance", nil)
		}
		cursor = page.NextCursor
	}
	return cursor, nil
}

func (s *Service) authorizedFeatureCatalog(ctx context.Context, req BrowseFeatureCatalogRequest) (AuthorizedCapability, error) {
	if s.config.Capability.ForCompilation == nil {
		return AuthorizedCapability{}, unavailable("feature-catalog", "CATALOG_UNAVAILABLE", "feature catalog browsing is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, req.Project, req.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return AuthorizedCapability{}, conflict("feature-catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, err)
	}
	if err != nil {
		return AuthorizedCapability{}, err
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(req.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(req.Project) || snapshot.Identity.Generation == "" {
		return AuthorizedCapability{}, conflict("feature-catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return AuthorizedCapability{}, conflict("feature-catalog", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	rootAllowed := false
	resourceAllowed := req.ResourceType == ""
	nodeAllowed := req.NodeID == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == req.RowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
		nodeAllowed = nodeAllowed || node.ID == req.NodeID && (req.ResourceType == "" || node.ResourceType == req.ResourceType)
	}
	if !rootAllowed || !resourceAllowed || !nodeAllowed {
		return AuthorizedCapability{}, malformed("feature-catalog", "the row root, resource filter, or graph node is not available in this snapshot", nil)
	}
	return authorized, nil
}

func featureFieldMatches(snapshot capability.Snapshot, candidate capability.Candidate, req BrowseFeatureCatalogRequest, query string) bool {
	if candidate.BlockedReason != "" || candidate.ID == "" || candidate.ResourceType == "" || candidate.FieldPath == "" {
		return false
	}
	if req.NodeID != "" && candidate.NodeID != req.NodeID {
		return false
	}
	if req.ResourceType != "" && candidate.ResourceType != req.ResourceType {
		return false
	}
	if req.ResourceType == "" && req.NodeID == "" && query == "" && candidate.ResourceType != req.RowRoot {
		return false
	}
	if _, ok := snapshot.Node(candidate.NodeID); !ok {
		return false
	}
	if query == "" {
		return true
	}
	searchable := strings.ToLower(strings.Join([]string{candidate.Label, candidate.FieldPath, candidate.LogicalType, candidate.ResourceType}, " "))
	return strings.Contains(searchable, query)
}

func directFeatureCatalogItem(snapshotToken string, candidate capability.Candidate) FeatureCatalogItem {
	readiness := authoringv2.SemanticSelectionReadiness{Status: authoringv2.SemanticReadinessReady, Code: "READY", Message: "This primitive field can be added directly."}
	choice, err := capability.NewFieldConstructionChoice(snapshotToken, candidate)
	var choicePointer *capability.ConstructionChoice
	if err == nil {
		choicePointer = &choice
	} else {
		readiness = compilerProofUnavailable()
	}
	title := strings.TrimSpace(candidate.Label)
	if title == "" || strings.Contains(title, ".") || strings.Contains(title, "[]") {
		title = humanizeFeatureName(lastFeaturePathSegment(candidate.FieldPath))
	}
	return FeatureCatalogItem{
		Kind: "DIRECT_FIELD", FeatureID: "field:" + candidate.ID, Title: title,
		Description:  "A primitive value stored directly on " + candidate.ResourceType + ".",
		ResourceType: candidate.ResourceType, ValueType: candidate.LogicalType, Cardinality: candidate.Cardinality,
		Occurrences: candidate.ObservedDocumentCount, Readiness: readiness,
		Source:             FeatureCatalogSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
		SourceDetails:      []capability.ConstructionChoiceFact{{Label: "FHIR resource", Value: candidate.ResourceType}, {Label: "FHIR path", Value: candidate.FieldPath}},
		ConstructionChoice: choicePointer,
	}
}

func semanticFeatureCatalogItem(entry SemanticInventoryItem) FeatureCatalogItem {
	title := strings.TrimSpace(entry.Display)
	if title == "" {
		title = strings.TrimSpace(entry.Code)
	}
	if title == "" {
		title = humanizeFeatureName(lastFeaturePathSegment(entry.System))
	}
	if title == "" {
		title = humanizeFeatureName(lastFeaturePathSegment(entry.SourcePath))
	}
	cardinality := "semantic_value"
	return FeatureCatalogItem{
		Kind: "SEMANTIC_FEATURE", FeatureID: "semantic:" + entry.ConceptID + ":" + entry.BindingID,
		Title: title, Description: "A FHIR concept paired with its schema-defined value.",
		ResourceType: entry.ResourceType, ValueType: entry.ValueType, Cardinality: cardinality,
		Occurrences: entry.Occurrences, Readiness: entry.Readiness,
		Source:        FeatureCatalogSource{Kind: capability.ConstructionChoiceSourceSemantic, ConceptID: entry.ConceptID, BindingID: entry.BindingID},
		SourceDetails: semanticFeatureSourceDetails(entry), ConstructionChoice: entry.ConstructionChoice,
	}
}

func semanticFeatureSourceDetails(entry SemanticInventoryItem) []capability.ConstructionChoiceFact {
	details := []capability.ConstructionChoiceFact{{Label: "FHIR resource", Value: entry.ResourceType}}
	if entry.System != "" {
		details = append(details, capability.ConstructionChoiceFact{Label: "Code system", Value: entry.System})
	}
	if entry.Code != "" {
		details = append(details, capability.ConstructionChoiceFact{Label: "Code", Value: entry.Code})
	}
	if entry.SourcePath != "" {
		details = append(details, capability.ConstructionChoiceFact{Label: "FHIR source", Value: entry.SourcePath})
	}
	if entry.ValueSelector != "" {
		details = append(details, capability.ConstructionChoiceFact{Label: "Value member", Value: entry.ValueSelector})
	}
	return details
}

func featureCatalogContextToken(snapshot capability.Snapshot, explorerID, rowRoot string) (string, error) {
	raw, err := json.Marshal([]string{"feature-catalog/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot})
	if err != nil {
		return "", err
	}
	return digestString(string(raw)), nil
}

func featureCatalogCursorContext(contextToken string, req BrowseFeatureCatalogRequest) (string, error) {
	raw, err := json.Marshal([]string{contextToken, string(req.Section), req.ResourceType, req.NodeID, strings.TrimSpace(req.Query)})
	if err != nil {
		return "", err
	}
	return digestString(string(raw)), nil
}

func encodeFeatureCatalogCursor(cursor featureCatalogCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeFeatureCatalogCursor(raw, expectedContext string) (string, error) {
	if raw == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	var cursor featureCatalogCursor
	if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Context != expectedContext || cursor.Page == "" {
		return "", conflict("feature-catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	return cursor.Page, nil
}

func featureFieldSortKey(candidate capability.Candidate) string {
	return strings.Join([]string{candidate.ResourceType, candidate.NodeID, candidate.ID}, "\x00")
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func lastFeaturePathSegment(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	if dot := strings.LastIndex(value, "."); dot >= 0 {
		value = value[dot+1:]
	}
	return strings.TrimSuffix(value, "[]")
}

func humanizeFeatureName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Feature"
	}
	var words []rune
	for index, current := range []rune(value) {
		if current == '_' || current == '-' {
			words = append(words, ' ')
			continue
		}
		if index > 0 && unicode.IsUpper(current) && len(words) > 0 && words[len(words)-1] != ' ' {
			words = append(words, ' ')
		}
		words = append(words, unicode.ToLower(current))
	}
	words[0] = unicode.ToUpper(words[0])
	return string(words)
}
