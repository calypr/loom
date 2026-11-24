package support

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func ProposalDocument(workspace ws.Workspace, outputID string) *ws.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func DocumentDigest(document ws.Document) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode authoring document digest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
