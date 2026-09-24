package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	OutputID      string
	Section       FeatureCatalogSection
	ResourceType  string
	NodeID        string
	Query         string
	Cursor        string
	Limit         int
	rowRoot       string
	rowSetDigest  string
	defaultCohort bool
	available     availableColumnRoutes
	authorized    *AuthorizedCapability
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
	SourceRecords      *int64                                 `json:"sourceRecords,omitempty"`
	Coverage           FeatureCatalogCoverage                 `json:"coverage"`
	Readiness          authoringv2.SemanticSelectionReadiness `json:"readiness"`
	Source             FeatureCatalogSource                   `json:"source"`
	SourceDetails      []capability.ConstructionChoiceFact    `json:"sourceDetails"`
	ConstructionChoice *capability.ConstructionChoice         `json:"constructionChoice,omitempty"`
	PivotFamily        *FeatureCatalogPivotFamily             `json:"pivotFamily,omitempty"`
}

// FeatureCatalogPivotFamily is the stable identity of a correlated code/value
// source at one observed route. It describes a possible group of columns, not
// a claim that multiple codes occur on the same output row.
type FeatureCatalogPivotFamily struct {
	ID                    string                            `json:"id"`
	Title                 string                            `json:"title"`
	Relationship          string                            `json:"relationship"`
	Code                  string                            `json:"code"`
	Form                  capability.ConstructionChoiceForm `json:"form"`
	MultiCodeRowsPossible bool                              `json:"multiCodeRowsPossible"`
}

type FeatureCatalogCoverage struct {
	State         string `json:"state"`
	RowsWithValue *int64 `json:"rowsWithValue,omitempty"`
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
	workspace, err := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	rowContext, err := savedCatalogRowContext(workspace, req.OutputID)
	if err != nil {
		return BrowseFeatureCatalogResponse{}, malformed("feature-catalog", err.Error(), err)
	}
	req.rowRoot, req.rowSetDigest = rowContext.RootResource, rowContext.Digest
	if document := findSemanticOutput(workspace, req.OutputID); document != nil {
		req.defaultCohort = defaultCatalogRecordCohort(workspace, *document)
	}
	if s.config.AvailableColumns != nil {
		authorized, err := s.authorizedFeatureCatalog(ctx, req)
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
		routes, state, err := s.availableColumnRoutes(ctx, authorized, workspace, req.OutputID)
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
		if state != catalog.SemanticInventoryComplete {
			return BrowseFeatureCatalogResponse{State: state, Section: req.Section, Entries: []FeatureCatalogItem{}, SourceAvailability: catalog.SemanticInventorySourceAvailabilityUnknown}, nil
		}
		req.available, req.authorized = routes, &authorized
	}
	if req.Section == FeatureCatalogFields {
		return s.browseFeatureFields(ctx, req)
	}
	return s.browseSemanticFeatures(ctx, req)
}

func validateFeatureCatalogRequest(req BrowseFeatureCatalogRequest) error {
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || strings.TrimSpace(req.SnapshotToken) == "" || strings.TrimSpace(req.OutputID) == "" || req.Limit < 0 || req.Limit > catalog.SemanticInventoryPageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return malformed("feature-catalog", fmt.Sprintf("project, explorer, snapshotToken, outputId, section, and a page limit of at most %d are required", catalog.SemanticInventoryPageLimit), nil)
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
	contextToken, err := featureCatalogContextToken(snapshot, req.ExplorerID, req.OutputID, req.rowSetDigest)
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
	reachable := observedCatalogNodes(snapshot, req.rowRoot)
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
		if !featureFieldMatches(snapshot, candidate, req, query, reachable) {
			continue
		}
		classification, classifyErr := semanticfhir.ClassifyDirectField(index, fhirschema.DefinitionName(candidate.ResourceType), candidate.FieldPath)
		if classifyErr != nil || !classification.Eligible {
			continue
		}
		var verifiedChoice *capability.ConstructionChoice
		if req.available != nil {
			route, exists := req.available[catalog.AvailabilityFeature{Kind: "FIELD", ResourceType: candidate.ResourceType, FieldPath: candidate.FieldPath}]
			if !exists {
				continue
			}
			choice, err := availableFieldChoice(ctx, authorized, req.rowRoot, candidate, route)
			if err != nil {
				return BrowseFeatureCatalogResponse{}, unavailable("feature-catalog", "AVAILABILITY_ROUTE_COMPILE_FAILED", "an observed column route could not be compiled", err)
			}
			verifiedChoice = &choice
		}
		if len(result.Entries) == limit {
			hasMore = true
			break
		}
		item := directFeatureCatalogItem(snapshot, req, candidate)
		if verifiedChoice != nil {
			item.ConstructionChoice = verifiedChoice
			item.Coverage = FeatureCatalogCoverage{State: "VERIFIED"}
			item.Readiness = authoringv2.SemanticSelectionReadiness{Status: authoringv2.SemanticReadinessReady, Code: "READY", Message: "A populated source is reachable from this table."}
		}
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
	outerContext := strings.Join([]string{req.SnapshotToken, req.ExplorerID, req.OutputID, req.rowSetDigest, string(req.Section), req.ResourceType, req.NodeID, strings.TrimSpace(req.Query)}, "\x00")
	innerCursor, err := decodeFeatureCatalogCursor(req.Cursor, digestString(outerContext))
	if err != nil {
		return BrowseFeatureCatalogResponse{}, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = catalog.SemanticInventoryPageLimit
	}
	result := BrowseFeatureCatalogResponse{Section: req.Section, Entries: make([]FeatureCatalogItem, 0, limit)}
	page, browseErr := s.BrowseSemanticInventory(ctx, BrowseSemanticInventoryRequest{
		Project: req.Project, ExplorerID: req.ExplorerID, SnapshotToken: req.SnapshotToken,
		RowRoot: req.rowRoot, RowSetDigest: req.rowSetDigest, ResourceType: req.ResourceType,
		CatalogSection: catalog.SemanticInventoryCatalogSection(req.Section),
		Query:          req.Query, Cursor: innerCursor, Limit: limit,
		available: req.available,
	})
	if browseErr != nil {
		return BrowseFeatureCatalogResponse{}, browseErr
	}
	result.ContextToken, result.BuildID, result.State, result.SourceAvailability = page.ContextToken, page.BuildID, page.State, page.SourceAvailability
	for _, entry := range page.Entries {
		result.Entries = append(result.Entries, semanticFeatureCatalogItem(entry, req))
	}
	if page.NextCursor != "" {
		result.NextCursor, err = encodeFeatureCatalogCursor(featureCatalogCursor{Context: digestString(outerContext), Page: page.NextCursor})
		if err != nil {
			return BrowseFeatureCatalogResponse{}, err
		}
	}
	return result, nil
}

func (s *Service) authorizedFeatureCatalog(ctx context.Context, req BrowseFeatureCatalogRequest) (AuthorizedCapability, error) {
	if req.authorized != nil {
		return *req.authorized, nil
	}
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
		rootAllowed = rootAllowed || node.ResourceType == req.rowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
		nodeAllowed = nodeAllowed || node.ID == req.NodeID && (req.ResourceType == "" || node.ResourceType == req.ResourceType)
	}
	if !rootAllowed || !resourceAllowed || !nodeAllowed {
		return AuthorizedCapability{}, malformed("feature-catalog", "the row root, resource filter, or graph node is not available in this snapshot", nil)
	}
	return authorized, nil
}

func featureFieldMatches(snapshot capability.Snapshot, candidate capability.Candidate, req BrowseFeatureCatalogRequest, query string, reachable map[string]bool) bool {
	if candidate.BlockedReason != "" || candidate.ID == "" || candidate.ResourceType == "" || candidate.FieldPath == "" {
		return false
	}
	if req.NodeID != "" && candidate.NodeID != req.NodeID {
		return false
	}
	if req.ResourceType != "" && candidate.ResourceType != req.ResourceType {
		return false
	}
	if req.ResourceType == "" && req.NodeID == "" && !reachable[candidate.NodeID] {
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

func observedCatalogNodes(snapshot capability.Snapshot, rootResourceType string) map[string]bool {
	reachable := make(map[string]bool, len(snapshot.Nodes))
	frontier := make([]string, 0, 1)
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			reachable[node.ID] = true
			frontier = append(frontier, node.ID)
		}
	}
	for len(frontier) > 0 {
		next := make([]string, 0)
		for _, from := range frontier {
			for _, edge := range snapshot.Edges {
				if edge.FromNodeID != from || edge.ObservedEdgeCount <= 0 || edge.BlockedReason != "" || reachable[edge.ToNodeID] {
					continue
				}
				reachable[edge.ToNodeID] = true
				next = append(next, edge.ToNodeID)
			}
		}
		frontier = next
	}
	return reachable
}

func directFeatureCatalogItem(snapshot capability.Snapshot, req BrowseFeatureCatalogRequest, candidate capability.Candidate) FeatureCatalogItem {
	readiness := authoringv2.SemanticSelectionReadiness{Status: authoringv2.SemanticReadinessReady, Code: "READY", Message: "This primitive field can be added directly."}
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	var choicePointer *capability.ConstructionChoice
	if !canonicalConstructionRouteAvailable(snapshot, req.rowRoot, candidate.NodeID) {
		readiness = sourceNotConnectedReadiness()
	} else if err == nil {
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
		Coverage:           featureCatalogCoverage(req, candidate.ResourceType, &candidate.ObservedDocumentCount),
		Source:             FeatureCatalogSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
		SourceDetails:      []capability.ConstructionChoiceFact{{Label: "FHIR resource", Value: candidate.ResourceType}, {Label: "FHIR path", Value: candidate.FieldPath}},
		ConstructionChoice: choicePointer,
	}
}

func canonicalConstructionRouteAvailable(snapshot capability.Snapshot, rowRoot, targetNodeID string) bool {
	_, err := capability.PlanCanonicalConstructionRoute(snapshot, rowRoot, targetNodeID)
	return err == nil
}

func sourceNotConnectedReadiness() authoringv2.SemanticSelectionReadiness {
	return authoringv2.SemanticSelectionReadiness{
		Status:  authoringv2.SemanticReadinessUnsupported,
		Code:    "SOURCE_NOT_CONNECTED",
		Message: "This source is not connected to the current table rows through an observed FHIR relationship.",
	}
}

func semanticFeatureCatalogItem(entry SemanticInventoryItem, req BrowseFeatureCatalogRequest) FeatureCatalogItem {
	title := ""
	if entry.Role == catalog.SemanticRoleCategoricalSlot || entry.Role == catalog.SemanticRoleStructuredSlot {
		title = strings.TrimSpace(entry.SlotLabel)
	} else {
		title = strings.TrimSpace(entry.Display)
	}
	if title == "" {
		if entry.Role != catalog.SemanticRoleCategoricalSlot {
			title = strings.TrimSpace(entry.Code)
		}
	}
	if title == "" && strings.TrimSpace(entry.System) != "" {
		title = humanizeFeatureName(lastFeaturePathSegment(entry.System))
	}
	if title == "" {
		title = humanizeFeatureName(lastFeaturePathSegment(entry.SourcePath))
	}
	cardinality := semanticFeatureCardinality(entry)
	description := strings.TrimSpace(entry.SlotDescription)
	if description == "" {
		if entry.Role == catalog.SemanticRoleCategoricalSlot {
			description = "A categorical FHIR field. Observed codes are values in this column, not separate columns."
		} else if entry.Role == catalog.SemanticRoleStructuredSlot {
			description = "A structured value. All populated members are retained together."
		} else {
			description = "A FHIR concept paired with its schema-defined value."
		}
	}
	item := FeatureCatalogItem{
		Kind: "SEMANTIC_FEATURE", FeatureID: "semantic:" + entry.ConceptID + ":" + entry.BindingID,
		Title: title, Description: description,
		ResourceType: entry.ResourceType, ValueType: entry.ValueType, Cardinality: cardinality,
		Occurrences: entry.Occurrences, SourceRecords: entry.SourceRecords, Readiness: entry.Readiness,
		Coverage:      featureCatalogCoverage(req, entry.ResourceType, entry.SourceRecords),
		Source:        FeatureCatalogSource{Kind: capability.ConstructionChoiceSourceSemantic, ConceptID: entry.ConceptID, BindingID: entry.BindingID},
		SourceDetails: semanticFeatureSourceDetails(entry), ConstructionChoice: entry.ConstructionChoice,
	}
	var snapshot *capability.Snapshot
	if req.authorized != nil {
		snapshot = &req.authorized.Snapshot
	}
	item.PivotFamily = featureCatalogPivotFamily(entry, snapshot)
	return item
}

func featureCatalogPivotFamily(entry SemanticInventoryItem, snapshot *capability.Snapshot) *FeatureCatalogPivotFamily {
	if entry.Role != catalog.SemanticRoleCodedValue || entry.ConstructionChoice == nil || entry.Code == "" || entry.System == "" {
		return nil
	}
	source, ok := entry.ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.KeySelector == "" || source.ValueSelector == "" {
		return nil
	}
	form := capability.ConstructionChoiceForm("")
	for _, option := range entry.ConstructionChoice.Options {
		if option.Support != capability.ConstructionChoiceSupported || option.Preservation != capability.ConstructionChoicePreserving || option.RowEffect != capability.ConstructionChoicePreservesRows {
			continue
		}
		if option.Form == capability.ConstructionChoiceAll || option.Form == capability.ConstructionChoiceValue && form == "" {
			form = option.Form
		}
	}
	if form == "" {
		return nil
	}
	id, err := pivotFamilyID(source, entry.ConstructionChoice.Route, form)
	if err != nil {
		return nil
	}
	relationship := "On each " + source.ResourceType + " row"
	if hops := len(entry.ConstructionChoice.Route); hops > 0 {
		word := "connections"
		if hops == 1 {
			word = "connection"
		}
		relationship = fmt.Sprintf("From related %s records (%d %s)", source.ResourceType, hops, word)
	}
	return &FeatureCatalogPivotFamily{
		ID:                    id,
		Title:                 source.ResourceType + " " + humanizeFeatureName(lastFeaturePathSegment(source.SourcePath)) + " values",
		Relationship:          relationship,
		Code:                  entry.Code,
		Form:                  form,
		MultiCodeRowsPossible: pivotFamilyCanReachMultipleOwners(source.OwningScope, entry.ConstructionChoice.Route, snapshot),
	}
}

func pivotFamilyID(source capability.SemanticBindingChoiceSource, route []capability.ConstructionRouteStep, form capability.ConstructionChoiceForm) (string, error) {
	identity := struct {
		ResourceType  string                             `json:"resourceType"`
		SourcePath    string                             `json:"sourcePath"`
		OwningScope   string                             `json:"owningScope"`
		KeySelector   string                             `json:"keySelector"`
		System        string                             `json:"system"`
		ValueSelector string                             `json:"valueSelector"`
		LogicalType   string                             `json:"logicalType"`
		Form          capability.ConstructionChoiceForm  `json:"form"`
		Route         []capability.ConstructionRouteStep `json:"route"`
	}{source.ResourceType, source.SourcePath, source.OwningScope, source.KeySelector, source.System, source.ValueSelector, source.LogicalType, form, route}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "pivot-family-" + hex.EncodeToString(digest[:16]), nil
}

func pivotFamilyCanReachMultipleOwners(ownerPath string, route []capability.ConstructionRouteStep, snapshot *capability.Snapshot) bool {
	if strings.Contains(ownerPath, "[]") {
		return true
	}
	if snapshot == nil {
		return false
	}
	for _, step := range route {
		for _, edge := range snapshot.Edges {
			if edge.ID == step.EdgeID && edge.AllowsRepeatedTarget {
				return true
			}
		}
	}
	return false
}

func featureCatalogCoverage(req BrowseFeatureCatalogRequest, resourceType string, sourceRecords *int64) FeatureCatalogCoverage {
	if req.available != nil {
		return FeatureCatalogCoverage{State: "VERIFIED"}
	}
	if req.defaultCohort && resourceType == req.rowRoot && sourceRecords != nil {
		count := *sourceRecords
		return FeatureCatalogCoverage{State: "INDEXED", RowsWithValue: &count}
	}
	return FeatureCatalogCoverage{State: "PENDING"}
}

func semanticFeatureCardinality(entry SemanticInventoryItem) string {
	observation := catalog.SemanticObservation{
		Source:      catalog.SemanticObservationSource{Type: entry.ResourceType, Path: entry.SourcePath},
		OwningScope: entry.OwningScope,
		Value:       catalog.SemanticObservationValue{Selector: entry.ValueSelector},
	}
	path := semanticObservationValuePath(observation)
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(entry.ResourceType, path)
	if !ok {
		return "unknown"
	}
	if metadata.Repeated {
		return "many"
	}
	return "optional_one"
}

func semanticFeatureSourceDetails(entry SemanticInventoryItem) []capability.ConstructionChoiceFact {
	details := []capability.ConstructionChoiceFact{{Label: "FHIR resource", Value: entry.ResourceType}}
	if entry.Role != "" {
		details = append(details, capability.ConstructionChoiceFact{Label: "FHIR semantic role", Value: string(entry.Role)})
	}
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

func featureCatalogContextToken(snapshot capability.Snapshot, explorerID, outputID, rowSetDigest string) (string, error) {
	raw, err := json.Marshal([]string{"feature-catalog/v2", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, outputID, rowSetDigest})
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
