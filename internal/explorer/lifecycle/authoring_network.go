package lifecycle

import (
	"context"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type AuthoringNetworkRequest struct {
	Project              string `json:"-"`
	ExplorerID           string `json:"-"`
	OutputID             string `json:"outputId"`
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
}
type AuthoringNetworkResponse struct {
	SnapshotToken string                       `json:"snapshotToken"`
	DraftVersion  int64                        `json:"draftVersion"`
	DraftDigest   string                       `json:"draftDigest"`
	RowIdentity   authoringv2.RowIdentityPlan  `json:"rowIdentity"`
	Network       authoringv2.AuthoringNetwork `json:"network"`
}

func (s *Service) DescribeAuthoringNetwork(ctx context.Context, request AuthoringNetworkRequest) (AuthoringNetworkResponse, error) {
	empty := AuthoringNetworkResponse{}
	for name, value := range map[string]string{"project": request.Project, "explorerId": request.ExplorerID, "outputId": request.OutputID, "snapshotToken": request.SnapshotToken, "expectedDraftDigest": request.ExpectedDraftDigest} {
		if err := requireExactIdentity(value, name); err != nil {
			return empty, malformed("authoring-network", err.Error(), err)
		}
	}
	if request.ExpectedDraftVersion < 1 {
		return empty, malformed("authoring-network", "expectedDraftVersion must be positive", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.AuthoringNetwork == nil {
		return empty, unavailable("authoring-network", "CAPABILITY_UNAVAILABLE", "authoring network hydration is unavailable", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation == "" {
		return empty, conflict("authoring-network", "STALE_CATALOG_SNAPSHOT", "reload the source catalog", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return empty, conflict("authoring-network", "STALE_AUTHORIZATION_SCOPE", "source authorization changed", nil, err)
	}
	owner, err := s.getOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return empty, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return empty, conflict("authoring-network", "DRAFT_CONFLICT", "saved draft changed; reload its network", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return empty, conflict("authoring-network", "AUTHORING_STATE_MISSING", "saved source cannot be read", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil || digest != owner.DraftDigest {
		return empty, conflict("authoring-network", "DRAFT_CONFLICT", "saved source digest does not match", nil, err)
	}
	document := proposalDocument(workspace, request.OutputID)
	if document == nil {
		return empty, unprocessable("authoring-network", "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	network, err := s.config.AuthoringNetwork(ctx, request.Project, authorized, *document)
	if err != nil {
		return empty, unprocessable("authoring-network", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	rowIdentity := rowIdentityForNetwork(*document, network)
	return AuthoringNetworkResponse{RowIdentity: rowIdentity, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Network: network}, nil
}

// rowIdentityForNetwork uses the effective imported overlay, not its historical
// projected route. Saved and proposed recipes must describe identical semantics.
func rowIdentityForNetwork(document authoringv2.Document, network authoringv2.AuthoringNetwork) authoringv2.RowIdentityPlan {
	effective := document
	effective.Rows = network.SourceRows
	var route func(string) authoringv2.RouteNode
	route = func(id string) authoringv2.RouteNode {
		var result authoringv2.RouteNode
		for _, occurrence := range network.Occurrences {
			if occurrence.OccurrenceID == id {
				result = authoringv2.RouteNode{OccurrenceID: id, ResourceType: occurrence.ResourceType, CatalogEdgeID: occurrence.CatalogEdgeID, Relationship: occurrence.Relationship, MatchMode: occurrence.MatchMode}
				break
			}
		}
		for _, occurrence := range network.Occurrences {
			if occurrence.ParentOccurrenceID == id && occurrence.Enabled {
				result.Children = append(result.Children, route(occurrence.OccurrenceID))
			}
		}
		return result
	}
	effective.Route = route(authoringv2.RootOccurrenceID)
	return authoringv2.DescribeRowIdentity(effective)
}
