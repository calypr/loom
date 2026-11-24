package sourceload

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func sourceLoadFileForType(files []SourceManifestFile, resourceType string) (SourceManifestFile, bool) {
	for _, file := range files {
		typeName, err := canonicalSourceResourceType(file.Path)
		if err == nil && typeName == resourceType {
			return file, true
		}
	}
	return SourceManifestFile{}, false
}

func hashSourceLoadFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash source file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
