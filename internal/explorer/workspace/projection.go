package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type Artifact struct {
	Kind, Project, ExplorerID, ID string
	RawDigest, IntentDigest       string
	SnapshotToken, Generation     string
	ScopeDigest, ReceiptID        string
	DraftVersion                  int64
}

type Projection struct {
	ID        string
	Source    Artifact
	Workspace json.RawMessage
	Digest    string
}

type ProjectionReader interface {
	WorkspaceProjection(context.Context, Artifact) (Projection, error)
}

func BytesDigest(raw []byte) string {

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

func (source Artifact) Key() string {
	raw, _ := json.Marshal(source)
	return "workspace_projection_" + BytesDigest(raw)[len("sha256:"):]
}

func (projection Projection) ContentID() string {
	projection.ID = ""
	raw, _ := json.Marshal(projection)
	return BytesDigest(raw)
}

func (projection Projection) Validate(source Artifact) (Workspace, error) {
	if projection.Source != source || projection.ID == "" || projection.ID != projection.ContentID() {
		return Workspace{}, fmt.Errorf("workspace projection does not match its immutable source")
	}
	workspace, err := DecodeCurrentWorkspace(projection.Workspace)
	if err != nil {
		return Workspace{}, fmt.Errorf("decode workspace projection: %w", err)
	}
	if workspace.SemanticsVersion != CurrentSemanticsVersion {
		return Workspace{}, fmt.Errorf("workspace projection is not current")
	}
	digest, err := workspace.Digest()
	if err != nil || digest != projection.Digest {
		return Workspace{}, fmt.Errorf("workspace projection digest mismatch")
	}
	return workspace, nil
}
