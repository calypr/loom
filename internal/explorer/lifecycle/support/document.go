package support

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func DocumentDigest(document ws.Document) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode authoring document digest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
