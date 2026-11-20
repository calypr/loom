package explorer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type WorkspaceArtifact struct {
	Kind, Project, ExplorerID, ID string
	RawDigest, IntentDigest       string
	SnapshotToken, Generation     string
	ScopeDigest, ReceiptID        string
	DraftVersion                  int64
}

type WorkspaceProjection struct {
	ID        string
	Source    WorkspaceArtifact
	Workspace json.RawMessage
	Digest    string
}

type WorkspaceProjectionReader interface {
	WorkspaceProjection(context.Context, WorkspaceArtifact) (WorkspaceProjection, error)
}

func workspaceBytesDigest(raw []byte) string {

	if json.Valid(raw) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			if canonical, err := json.Marshal(value); err == nil {
				raw = canonical
			}
		}
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

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

func (source WorkspaceArtifact) Key() string {
	raw, _ := json.Marshal(source)
	return "workspace_projection_" + workspaceBytesDigest(raw)[len("sha256:"):]
}

func (projection WorkspaceProjection) ContentID() string {
	projection.ID = ""
	raw, _ := json.Marshal(projection)
	return workspaceBytesDigest(raw)
}

func (projection WorkspaceProjection) Validate(source WorkspaceArtifact) (authoringv2.Workspace, error) {
	if projection.Source != source || projection.ID == "" || projection.ID != projection.ContentID() {
		return authoringv2.Workspace{}, fmt.Errorf("workspace projection does not match its immutable source")
	}
	workspace, err := authoringv2.DecodeCurrentWorkspace(projection.Workspace)
	if err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("decode workspace projection: %w", err)
	}
	if workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
		return authoringv2.Workspace{}, fmt.Errorf("workspace projection is not current")
	}
	digest, err := workspace.Digest()
	if err != nil || digest != projection.Digest {
		return authoringv2.Workspace{}, fmt.Errorf("workspace projection digest mismatch")
	}
	return workspace, nil
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
