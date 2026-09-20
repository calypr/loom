package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type BrowseSemanticInventoryRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	RowRoot       string
	ResourceType  string
	Query         string
	Cursor        string
	Limit         int
}

type SemanticInventoryItem struct {
	ConceptID          string                                 `json:"conceptId"`
	BindingID          string                                 `json:"bindingId"`
	ResourceType       string                                 `json:"resourceType"`
	SourcePath         string                                 `json:"sourcePath"`
	System             string                                 `json:"system"`
	Code               string                                 `json:"code"`
	CodingVersion      string                                 `json:"codingVersion"`
	Display            string                                 `json:"display"`
	ValueSelector      string                                 `json:"valueSelector"`
	ValueType          string                                 `json:"valueType"`
	OwningScope        string                                 `json:"owningScope"`
	Occurrences        int64                                  `json:"occurrences"`
	Readiness          authoringv2.SemanticSelectionReadiness `json:"readiness"`
	ConstructionChoice *capability.ConstructionChoice         `json:"constructionChoice,omitempty"`
}

type BrowseSemanticInventoryResponse struct {
	ContextToken       string                         `json:"contextToken"`
	BuildID            string                         `json:"buildId"`
	State              catalog.SemanticInventoryState `json:"state"`
	SourceAvailability string                         `json:"sourceAvailability"`
	Entries            []SemanticInventoryItem        `json:"entries"`
	NextCursor         string                         `json:"nextCursor,omitempty"`
}

type semanticBrowseCursor struct {
	Context string `json:"context"`
	Page    string `json:"page"`
}

func (s *Service) BrowseSemanticInventory(ctx context.Context, req BrowseSemanticInventoryRequest) (BrowseSemanticInventoryResponse, error) {
	var result BrowseSemanticInventoryResponse
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || req.SnapshotToken == "" || req.RowRoot == "" || req.Limit < 0 || req.Limit > catalog.SemanticInventoryPageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return result, malformed("catalog", "project, explorer, snapshotToken, rowRoot, and a page limit of at most 50 are required", nil)
	}
	if s.config.SemanticInventory == nil || s.config.Capability.ForCompilation == nil {
		return result, unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory browsing is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, req.Project, req.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, err)
	}
	if err != nil {
		return result, err
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(req.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(req.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("catalog", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	rootAllowed := false
	resourceAllowed := req.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == req.RowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, malformed("catalog", "the row root or resource filter is not available in this snapshot", nil)
	}
	var cursor semanticBrowseCursor
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Context == "" || cursor.Page == "" {
			return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, nil)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	page, err := s.config.SemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: authorized.Scope.AuthResourcePaths,
		ResourceType: req.ResourceType, Query: req.Query, Cursor: cursor.Page, Limit: req.Limit,
	})
	if errors.Is(err, catalog.ErrSemanticInventoryCursorMismatch) {
		return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	if err != nil {
		return result, err
	}
	result.ContextToken, err = semanticInventoryContextToken(snapshot, req.ExplorerID, req.RowRoot, page.Build.BuildID)
	if err != nil {
		return result, err
	}
	if req.Cursor != "" && cursor.Context != result.ContextToken {
		return BrowseSemanticInventoryResponse{}, conflict("catalog", "STALE_CATALOG_CURSOR", "catalog context changed; restart search", nil, nil)
	}
	result.BuildID, result.State = page.Build.BuildID, page.State
	result.SourceAvailability = page.Build.SourceAvailability
	if result.SourceAvailability == "" {
		result.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnknown
	}
	result.Entries = make([]SemanticInventoryItem, 0, len(page.Entries))
	for _, entry := range page.Entries {
		observation := entry.Observation
		plan := authoringv2.ResolveSemanticSelectionPlan(observation)
		item := SemanticInventoryItem{
			ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
			CodingVersion: observation.Key.Version, Display: observation.Key.Display,
			ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
			OwningScope: observation.OwningScope, Occurrences: observation.Population,
			Readiness: plan.Readiness,
		}
		if plan.Readiness.Addable() {
			if candidate, ok := semanticConstructionCandidate(snapshot, observation); ok {
				ownerRecordsProved := proveOwnerRecords(ctx, authorized, req.RowRoot, observation)
				if choice, err := semanticInventoryConstructionChoice(snapshot, result.ContextToken, result.BuildID, entry, candidate, ownerRecordsProved); err == nil {
					item.ConstructionChoice = &choice
				} else {
					item.Readiness = compilerProofUnavailable()
				}
			} else {
				item.Readiness = compilerProofUnavailable()
			}
		}
		result.Entries = append(result.Entries, item)
	}
	if page.NextCursor != "" {
		raw, err := json.Marshal(semanticBrowseCursor{Context: result.ContextToken, Page: page.NextCursor})
		if err != nil {
			return BrowseSemanticInventoryResponse{}, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func semanticInventoryConstructionChoice(snapshot capability.Snapshot, contextToken, buildID string, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	return semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, candidate, ownerRecordsProved)
}

func semanticInventoryConstructionChoiceForRoute(snapshot capability.Snapshot, contextToken, buildID string, route []capability.ConstructionRouteStep, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	logicalType := observation.LogicalType
	if logicalType == "" {
		logicalType = observation.Value.Type
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
		SourceProfile: observation.Source.Profile, FieldPath: candidate.FieldPath,
		OwningScope: observation.OwningScope, ExtensionURLPath: observation.ExtensionURLPath,
		KeySelector: observation.Key.Selector, System: observation.Key.System, Version: observation.Key.Version,
		Code: observation.Key.Code, ValueSelector: observation.Value.Selector, ChoiceArm: observation.ChoiceArm,
		LogicalType: logicalType, RuleHint: observation.RuleHint, RuleVersion: observation.RuleVersion,
		SchemaVersion: observation.SchemaVersion,
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, candidate, ownerRecordsProved)
}

func proveOwnerRecords(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation) bool {
	return proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, nil)
}

func proveOwnerRecordsForRoute(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation, route []capability.ConstructionRouteStep) bool {
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return false
	}
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		compilerRoute = append(compilerRoute, compilerprobe.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode})
	}
	_, err := compilerprobe.ProbeOwnerRecords(ctx, compilerprobe.OwnerRecordsRequest{
		Scope: compilerprobe.Scope{
			Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
			AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
		},
		RootResourceType: rootResourceType,
		ResourceType:     observation.Source.Type,
		Route:            compilerRoute,
		Binding:          *plan.Source.Lookup.Binding,
		Key:              *plan.Source.Lookup.Key,
	})
	return err == nil
}

func semanticConstructionCandidate(snapshot capability.Snapshot, observation catalog.SemanticObservation) (capability.Candidate, bool) {
	resourceType := strings.TrimSpace(observation.Source.Type)
	valuePath := semanticObservationValuePath(observation)
	if resourceType == "" || valuePath == "" {
		return capability.Candidate{}, false
	}
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ResourceType == resourceType && canonicalInventoryPath(candidate.FieldPath) == valuePath {
			if found {
				return capability.Candidate{}, false
			}
			match, found = candidate, true
		}
	}
	return match, found
}

// Coded-value observations keep key and value selectors relative to the object
// that owns their correlation. Capability candidates address the scalar from
// the resource root, so the owning scope is restored only at this boundary.
func semanticObservationValuePath(observation catalog.SemanticObservation) string {
	valuePath := canonicalInventoryPath(observation.Value.Selector)
	ownerPath := canonicalInventoryPath(observation.OwningScope)
	if valuePath == "" || ownerPath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func canonicalInventoryPath(raw string) string {
	path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "root."))
	if path == "" {
		return ""
	}
	segments := strings.Split(path, ".")
	for index := range segments {
		segments[index] = strings.TrimSpace(segments[index])
		if segments[index] == "" {
			return ""
		}
	}
	return strings.Join(segments, ".")
}

func compilerProofUnavailable() authoringv2.SemanticSelectionReadiness {
	return authoringv2.SemanticSelectionReadiness{
		Status: authoringv2.SemanticReadinessUnsupported, Code: "COMPILER_PROOF_UNAVAILABLE",
		Message: "The exact semantic value path has no matching compiler-proved capability field.",
	}
}

func semanticInventoryContextToken(snapshot capability.Snapshot, explorerID, rowRoot, buildID string) (string, error) {
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot, "all-authorized", buildID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}
