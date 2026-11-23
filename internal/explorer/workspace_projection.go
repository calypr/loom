package explorer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	workspacepkg "github.com/calypr/loom/internal/explorer/workspace"
)

type WorkspaceArtifact = workspacepkg.Artifact
type WorkspaceProjection = workspacepkg.Projection
type WorkspaceProjectionReader = workspacepkg.ProjectionReader

func workspaceBytesDigest(raw []byte) string { return workspacepkg.BytesDigest(raw) }

func RevisionWorkspaceArtifact(revision Revision) WorkspaceArtifact {
	return WorkspaceArtifact{Kind: "revision", Project: revision.Project, ExplorerID: revision.ExplorerID,
		ID: revision.ID, RawDigest: workspaceBytesDigest(revision.AuthoringWorkspaceBytes()), IntentDigest: revision.IntentDigest,
		Generation: revision.SourceGeneration, ReceiptID: revision.CompilationReceiptID}
}

func (revision Revision) AuthoringWorkspaceBytes() json.RawMessage {
	if len(revision.AuthoringBundle) != 0 {
		return revision.AuthoringBundle
	}
	return revision.Config
}

func DraftWorkspaceArtifact(revision DraftRevision) WorkspaceArtifact {
	return WorkspaceArtifact{Kind: "draft-revision", Project: revision.Project, ExplorerID: revision.ExplorerID,
		ID: revision.ID, RawDigest: workspaceBytesDigest(revision.DraftConfig), IntentDigest: revision.DraftDigest,
		SnapshotToken: revision.SnapshotToken, Generation: revision.SourceGeneration,
		ScopeDigest: revision.AuthorizationScopeDigest, DraftVersion: revision.DraftVersion}
}

func (s *Service) RevisionWorkspace(ctx context.Context, revision Revision) (authoringv2.Workspace, error) {
	workspace, _, err := s.artifactWorkspace(ctx, RevisionWorkspaceArtifact(revision), revision.AuthoringWorkspaceBytes())
	return workspace, err
}

func (s *Service) DraftRevisionWorkspace(ctx context.Context, revision DraftRevision) (authoringv2.Workspace, string, error) {
	return s.artifactWorkspace(ctx, DraftWorkspaceArtifact(revision), revision.DraftConfig)
}

func (s *Service) artifactWorkspace(ctx context.Context, source WorkspaceArtifact, raw []byte) (authoringv2.Workspace, string, error) {
	workspace, err := authoringv2.DecodeCurrentWorkspace(raw)
	if err == nil && workspace.SemanticsVersion == authoringv2.CurrentSemanticsVersion {
		digest, digestErr := workspace.Digest()
		if digestErr == nil && (source.IntentDigest == "" || source.IntentDigest == digest) {
			return workspace, digest, nil
		}
	}
	if converted, changed, err := authoringv2.ConvertStoredSourcePlan(raw, source.IntentDigest); err == nil && changed {
		digest, err := converted.Digest()
		return converted, digest, err
	}
	reader, ok := s.store.(WorkspaceProjectionReader)
	if !ok {
		return authoringv2.Workspace{}, "", fmt.Errorf("immutable workspace requires its stored migration projection")
	}
	projection, err := reader.WorkspaceProjection(ctx, source)
	if err != nil {
		return authoringv2.Workspace{}, "", err
	}
	workspace, err = projection.Validate(source)
	return workspace, projection.Digest, err
}
