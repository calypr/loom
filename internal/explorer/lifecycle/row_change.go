package lifecycle

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// AssessRowChange resolves capability evidence against one exact persisted
// draft. It is read-only; mutation remains on the existing CAS command path.
func (s *Service) AssessRowChange(ctx context.Context, request AssessRowChangeRequest) (AssessRowChangeResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 1 || strings.TrimSpace(request.DraftDigest) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RootNodeID) == "" {
		return AssessRowChangeResult{}, malformed("row-change", "snapshotToken, draftVersion, draftDigest, outputId, and rootNodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return AssessRowChangeResult{}, unavailable("row-change", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return AssessRowChangeResult{}, conflict("row-change", "DRAFT_CONFLICT", "the Explorer draft changed; reassess the row definition", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return AssessRowChangeResult{}, conflict("row-change", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be assessed", nil, err)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return AssessRowChangeResult{}, conflict("row-change", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	assessment, err := authoringv2.AssessRowChange(workspace, s.config.Capability.Catalog(snapshot, request.ExplorerID), authoringv2.RowChangeRequest{
		OutputID: request.OutputID, RootNodeID: request.RootNodeID, RootOccurrenceID: request.RootOccurrenceID, RouteRebase: request.RouteRebase,
	})
	if err != nil {
		return AssessRowChangeResult{}, unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
	}
	return AssessRowChangeResult{
		SnapshotToken: request.SnapshotToken,
		DraftVersion:  owner.DraftVersion,
		DraftDigest:   owner.DraftDigest,
		Assessment:    assessment,
	}, nil
}
