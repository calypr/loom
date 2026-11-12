package lifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

// DefaultDataframeID is the reserved identity for the repository's imported
// default dataframe workspace in the graphical Builder.
const DefaultDataframeID = "dataframe-default"

const defaultDataframeTitle = "Default dataframe"

func (s *Service) defaultDataframeWorkspace(ctx context.Context, project string, snapshot capability.Snapshot, catalog authoringv2.CatalogSnapshot) (authoringv2.Workspace, error) {
	if s.config.DefaultDataframeWorkspace == nil {
		return authoringv2.Workspace{}, unavailable("default-dataframe", "DEFAULT_DATAFRAME_UNAVAILABLE", "default dataframe workspace is not configured", nil)
	}
	workspace, err := s.config.DefaultDataframeWorkspace(ctx, project, snapshot.Clone(), catalog)
	if err != nil {
		return authoringv2.Workspace{}, unavailable("default-dataframe", "DEFAULT_DATAFRAME_UNAVAILABLE", "default dataframe workspace could not be imported", err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		return authoringv2.Workspace{}, conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	workspace, err = authoringv2.DecodeWorkspace(canonical)
	if err != nil {
		return authoringv2.Workspace{}, conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	return workspace, nil
}

func (s *Service) virtualDefaultDataframeOwner(ctx context.Context, project string, snapshot capability.Snapshot, catalog authoringv2.CatalogSnapshot) (*explorer.Explorer, error) {
	workspace, err := s.defaultDataframeWorkspace(ctx, project, snapshot, catalog)
	if err != nil {
		return nil, err
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return nil, conflict("default-dataframe", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	title := strings.TrimSpace(workspace.Explorer.Title)
	if title == "" {
		title = defaultDataframeTitle
	}
	return &explorer.Explorer{
		Project: projectid.Legacy(project), ExplorerID: DefaultDataframeID,
		Title: title, ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest, DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation, DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}, nil
}

// getOwner makes only the reserved native Builder identity virtual. Existing
// owners are returned as stored, including malformed drafts, so a corrupt
// persisted draft cannot be silently replaced by imported state.
func (s *Service) getOwner(ctx context.Context, project, explorerID string) (*explorer.Explorer, error) {
	owner, err := s.store.Get(ctx, project, explorerID)
	if err == nil || !errors.Is(err, explorer.ErrNotFound) || strings.TrimSpace(explorerID) != DefaultDataframeID {
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
		return nil, unavailable("default-dataframe", "CAPABILITY_UNAVAILABLE", "default dataframe capability snapshot is unavailable", snapshotErr)
	}
	return s.virtualDefaultDataframeOwner(ctx, project, snapshot, s.config.Capability.Catalog(snapshot, explorerID))
}

func (s *Service) getOwnerWithSnapshot(ctx context.Context, project, explorerID string, snapshot capability.Snapshot, catalog authoringv2.CatalogSnapshot) (*explorer.Explorer, error) {
	owner, err := s.store.Get(ctx, project, explorerID)
	if err == nil || !errors.Is(err, explorer.ErrNotFound) || strings.TrimSpace(explorerID) != DefaultDataframeID {
		return owner, err
	}
	return s.virtualDefaultDataframeOwner(ctx, project, snapshot, catalog)
}
