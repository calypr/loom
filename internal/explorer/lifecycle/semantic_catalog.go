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
	ConceptID     string `json:"conceptId"`
	BindingID     string `json:"bindingId"`
	ResourceType  string `json:"resourceType"`
	SourcePath    string `json:"sourcePath"`
	System        string `json:"system"`
	Code          string `json:"code"`
	CodingVersion string `json:"codingVersion"`
	Display       string `json:"display"`
	ValueSelector string `json:"valueSelector"`
	ValueType     string `json:"valueType"`
	OwningScope   string `json:"owningScope"`
	Occurrences   int64  `json:"occurrences"`
}

type BrowseSemanticInventoryResponse struct {
	ContextToken string                         `json:"contextToken"`
	BuildID      string                         `json:"buildId"`
	State        catalog.SemanticInventoryState `json:"state"`
	Entries      []SemanticInventoryItem        `json:"entries"`
	NextCursor   string                         `json:"nextCursor,omitempty"`
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
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, req.ExplorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, req.RowRoot, "all-authorized", page.Build.BuildID})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(identity)
	result.ContextToken = hex.EncodeToString(digest[:])
	if req.Cursor != "" && cursor.Context != result.ContextToken {
		return BrowseSemanticInventoryResponse{}, conflict("catalog", "STALE_CATALOG_CURSOR", "catalog context changed; restart search", nil, nil)
	}
	result.BuildID, result.State = page.Build.BuildID, page.State
	result.Entries = make([]SemanticInventoryItem, 0, len(page.Entries))
	for _, entry := range page.Entries {
		observation := entry.Observation
		result.Entries = append(result.Entries, SemanticInventoryItem{
			ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
			CodingVersion: observation.Key.Version, Display: observation.Key.Display,
			ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
			OwningScope: observation.OwningScope, Occurrences: observation.Population,
		})
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
