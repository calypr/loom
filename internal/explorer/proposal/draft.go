package proposal

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
)

type draftTarget struct {
	project       string
	explorerID    string
	snapshotToken string
	draftVersion  int64
	draftDigest   string
	outputID      string
}

type draftDiagnostics struct {
	stage           string
	staleCatalog    string
	staleScope      string
	changedDraft    string
	unreadableDraft string
	workspaceDigest string
}

type proposalDraft struct {
	authorization ports.AuthorizedCapability
	owner         *explorer.Explorer
	workspace     ws.Workspace
	document      *ws.Document
}

func (s *Service) prepareDraft(ctx context.Context, target draftTarget, diagnostics draftDiagnostics) (proposalDraft, error) {
	authorized, err := s.config.Capability.ForCompilation(ctx, target.project, target.snapshotToken)
	snapshot := authorized.Snapshot
	if err != nil || snapshot.ValidateToken(target.snapshotToken) != nil ||
		projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(target.project) || snapshot.Identity.Generation == "" {
		return proposalDraft{}, support.Conflict(diagnostics.stage, "STALE_CATALOG_SNAPSHOT", diagnostics.staleCatalog, nil, err)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return proposalDraft{}, support.Conflict(diagnostics.stage, "STALE_AUTHORIZATION_SCOPE", diagnostics.staleScope, nil, err)
	}
	owner, err := s.config.Core.GetOwner(ctx, target.project, target.explorerID)
	if err != nil {
		return proposalDraft{}, err
	}
	if owner.DraftVersion != target.draftVersion || owner.DraftDigest != target.draftDigest {
		return proposalDraft{}, support.Conflict(diagnostics.stage, "DRAFT_CONFLICT", diagnostics.changedDraft, nil, explorer.ErrDraftConflict)
	}
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return proposalDraft{}, support.Conflict(diagnostics.stage, "AUTHORING_STATE_MISSING", diagnostics.unreadableDraft, nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil || digest != owner.DraftDigest {
		return proposalDraft{}, support.Conflict(diagnostics.stage, "DRAFT_CONFLICT", diagnostics.workspaceDigest, nil, err)
	}
	return proposalDraft{
		authorization: authorized, owner: owner, workspace: workspace,
		document: workspace.FindOutputDocument(target.outputID),
	}, nil
}
