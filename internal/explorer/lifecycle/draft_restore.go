package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) prepareDraftRevisionRestore(
	ctx context.Context,
	project string,
	explorerID string,
	request authoringv2.ApplyCommandsRequest,
	snapshot capability.Snapshot,
	commands []authoringv2.Command,
) ([]authoringv2.Command, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandRestoreDraftRevision {
		return nil, malformed("commands", "RESTORE_DRAFT_REVISION must be the only command", nil)
	}
	command := &commands[0]
	owner, err := s.store.Get(ctx, project, explorerID)
	if err != nil {
		return nil, conflict("commands", "STALE_DRAFT_REVISION", "the Explorer draft changed; reload before restoring", nil, err)
	}
	if owner == nil || owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return nil, conflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before restoring", nil, explorer.ErrDraftConflict)
	}
	if owner.PreviousDraftRevisionID == "" || command.DraftRevisionID != owner.PreviousDraftRevisionID {
		return nil, conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision is no longer the current Undo target", nil, nil)
	}
	revision, err := s.store.GetDraftRevision(ctx, project, explorerID, command.DraftRevisionID)
	if errors.Is(err, explorer.ErrNotFound) {
		return nil, conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision is unavailable", nil, err)
	}
	if err != nil {
		return nil, internal("commands", "DRAFT_REVISION_READ_FAILED", fmt.Sprintf("read prior draft revision: %v", err), err)
	}
	if revision == nil || revision.ID != command.DraftRevisionID || projectid.Canonical(revision.Project) != projectid.Canonical(project) ||
		revision.ExplorerID != explorerID || revision.DraftVersion != owner.DraftVersion-1 {
		return nil, conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision does not belong to this Explorer draft", nil, nil)
	}
	currentSource := request.ResolvedDraftSourceContext()
	if currentSource == nil || currentSource.SnapshotToken != snapshot.Token || currentSource.SourceGeneration != snapshot.Identity.Generation ||
		currentSource.AuthorizationScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		return nil, conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context changed; reload before restoring", nil, nil)
	}

	var restored authoringv2.Workspace
	if len(revision.DraftConfig) != 0 {
		if revision.SnapshotToken != currentSource.SnapshotToken || revision.SourceGeneration != currentSource.SourceGeneration ||
			revision.AuthorizationScopeDigest != currentSource.AuthorizationScopeDigest {
			return nil, conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the prior draft was created from another authorized source context", nil, nil)
		}
		restored, err = authoringv2.DecodeWorkspace(revision.DraftConfig)
		if err != nil {
			return nil, conflict("commands", "DRAFT_REVISION_INVALID", "the prior draft revision is invalid", nil, err)
		}
		digest, digestErr := restored.Digest()
		if digestErr != nil || revision.DraftDigest == "" || digest != revision.DraftDigest {
			return nil, conflict("commands", "DRAFT_REVISION_INVALID", "the prior draft revision failed its digest check", nil, digestErr)
		}
	} else {
		// A first draft mutation can snapshot an empty draft config. Restore the
		// effective prior workspace from the still-active immutable revision.
		if owner.ActiveRevisionID != "" {
			active, activeErr := s.store.ActiveRevision(ctx, project, explorerID)
			if activeErr != nil {
				return nil, conflict("commands", "DRAFT_REVISION_INVALID", "the prior workspace cannot be loaded", nil, activeErr)
			}
			if active.SourceGeneration != currentSource.SourceGeneration {
				return nil, conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the active source generation changed since the prior workspace", nil, nil)
			}
			restored, err = authoringv2.DecodeWorkspace(active.AuthoringBundle)
			if err != nil {
				return nil, conflict("commands", "DRAFT_REVISION_INVALID", "the prior workspace is invalid", nil, err)
			}
		} else {
			restored = authoringv2.Workspace{
				APIVersion: authoringv2.APIVersion,
				Kind:       authoringv2.WorkspaceKind,
				Explorer:   authoringv2.ExplorerMetadata{Title: revision.Title},
				Documents:  []authoringv2.Document{},
				Tabs:       []authoringv2.Tab{},
			}
		}
	}
	if err := command.ResolveDraftRevision(restored); err != nil {
		return nil, malformed("commands", err.Error(), err)
	}
	return commands, nil
}
