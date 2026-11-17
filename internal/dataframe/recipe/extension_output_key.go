package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const extensionOutputKeyPrefix = "ext-v1:"

// ExtensionColumnOutputKey identifies one discovered Extension mapping by its
// exact structural location. Raw ancestor URLs distinguish equal leaf URLs
// under different nested extensions; value type is deliberately excluded so
// datatype evolution does not change output identity.
func ExtensionColumnOutputKey(mapping ExtensionColumnMapping) string {
	identity := struct {
		SourcePath string   `json:"sourcePath"`
		URLPath    []string `json:"urlPath"`
		URL        string   `json:"url"`
	}{SourcePath: mapping.SourcePath, URLPath: append([]string(nil), mapping.URLPath...), URL: mapping.URL}
	canonical, _ := json.Marshal(identity)
	digest := sha256.Sum256(canonical)
	return extensionOutputKeyPrefix + hex.EncodeToString(digest[:])
}

// ValidExtensionColumnOutputKey reports whether key has the bounded v1 wire
// form used by ExtensionColumnOutputKey.
func ValidExtensionColumnOutputKey(key string) bool {
	if len(key) != len(extensionOutputKeyPrefix)+sha256.Size*2 || key[:len(extensionOutputKeyPrefix)] != extensionOutputKeyPrefix {
		return false
	}
	_, err := hex.DecodeString(key[len(extensionOutputKeyPrefix):])
	return err == nil
}
