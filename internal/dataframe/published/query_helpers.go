package published

import (
	"crypto/sha256"
	"encoding/hex"
)

func shortQueryID(query string) string {
	digest := sha256.Sum256([]byte(query))
	return hex.EncodeToString(digest[:8])
}
