package authoringcore

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
)

const DefaultDataframeID = "dataframe-default"
const defaultDataframeTitle = "Default dataframe"

func (s *Core) DefaultDataframeWorkspace(ctx context.Context, project string, snapshot capability.Snapshot, Catalog ws.CatalogSnapshot) (ws.Workspace, error) {
	if s.config.DefaultDataframeWorkspace == nil {
		return ws.Workspace{}, support.Unavailable("default-dataframe", "DEFAULT_DATAFRAME_UNAVAILABLE", "default dataframe workspace is not configured", nil)
	}
	workspace, err := s.config.DefaultDataframeWorkspace(ctx, project, snapshot.Clone(), Catalog)
	if err != nil {
		return ws.Workspace{}, support.Unavailable("default-dataframe", "DEFAULT_DATAFRAME_UNAVAILABLE", "default dataframe workspace could not be imported", err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return ws.Workspace{}, support.Conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	workspace, err = ws.DecodeWorkspace(canonical)
	if err != nil {
		return ws.Workspace{}, support.Conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	return workspace, nil
}

func (s *Core) VirtualDefaultDataframeOwner(ctx context.Context, project string, snapshot capability.Snapshot, Catalog ws.CatalogSnapshot) (*explorer.Explorer, error) {
	workspace, err := s.DefaultDataframeWorkspace(ctx, project, snapshot, Catalog)
	if err != nil {
		return nil, err
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, support.Conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return nil, support.Conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	title := strings.TrimSpace(workspace.Explorer.Title)
	if title == "" {
		title = defaultDataframeTitle
	}
	return &explorer.Explorer{
		Project: projectid.Legacy(project), ExplorerID: DefaultDataframeID,
		Title: title, ManagementMode: viewer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest, DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation, DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}, nil
}

func (s *Core) GetOwner(ctx context.Context, project, explorerID string) (*explorer.Explorer, error) {
	owner, err := s.store.Get(ctx, project, explorerID)
	if err != nil && !errors.Is(err, explorer.ErrNotFound) {
		return nil, support.PersistenceFailure("explorer", "the Explorer state could not be loaded", err)
	}
	if err == nil || strings.TrimSpace(explorerID) != DefaultDataframeID {
		return owner, err
	}
	if s.config.Capability.Current == nil || s.config.Capability.Catalog == nil {
		return nil, err
	}
	snapshot, snapshotErr := s.config.Capability.Current(ctx, project, explorerID, "")
	if snapshotErr != nil || !snapshot.Usable() {
		if snapshotErr == nil {
			snapshotErr = capability.ErrSnapshotUnavailable
		}
		return nil, support.Unavailable("default-dataframe", "CAPABILITY_UNAVAILABLE", "default dataframe capability snapshot is unavailable", snapshotErr)
	}
	return s.VirtualDefaultDataframeOwner(ctx, project, snapshot, s.config.Capability.Catalog(snapshot, explorerID))
}

func (s *Core) GetOwnerWithSnapshot(ctx context.Context, project, explorerID string, snapshot capability.Snapshot, Catalog ws.CatalogSnapshot) (*explorer.Explorer, error) {
	owner, err := s.store.Get(ctx, project, explorerID)
	if err != nil && !errors.Is(err, explorer.ErrNotFound) {
		return nil, support.PersistenceFailure("explorer", "the Explorer state could not be loaded", err)
	}
	if err == nil || strings.TrimSpace(explorerID) != DefaultDataframeID {
		return owner, err
	}
	return s.VirtualDefaultDataframeOwner(ctx, project, snapshot, Catalog)
}

func (s *Core) CurrentWorkspace(ctx context.Context, stage, project, explorerID string) (ws.Workspace, error) {
	project = projectid.Canonical(project)
	owner, err := s.GetOwner(ctx, project, explorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return ws.Workspace{}, support.NotFound(stage, "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return ws.Workspace{}, support.PersistenceFailure(stage, "the current Explorer workspace could not be loaded", err)
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := ws.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return ws.Workspace{}, support.Conflict(stage, "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return ws.Workspace{}, support.PersistenceFailure(stage, "the active Explorer workspace could not be loaded", activeErr)
		}
		workspace, decodeErr := s.store.RevisionWorkspace(ctx, *active)
		if decodeErr != nil {
			return ws.Workspace{}, support.Conflict(stage, "AUTHORING_STATE_MISSING", "the active Explorer workspace is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	return ws.Workspace{}, support.Conflict(stage, "AUTHORING_STATE_MISSING", "the Explorer has no V2 authoring workspace", nil, nil)
}
