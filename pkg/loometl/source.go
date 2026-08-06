package loometl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func BytesResource(resourceType string, data []byte) (ResourceSource, error) {
	if err := validateResourceType(resourceType); err != nil {
		return ResourceSource{}, err
	}
	copyData := append([]byte(nil), data...)
	digest := sha256.Sum256(copyData)
	return ResourceSource{
		ResourceType: resourceType, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(copyData)),
		Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(copyData)), nil },
	}, nil
}

func FileResource(resourceType, filePath string) (ResourceSource, error) {
	if err := validateResourceType(resourceType); err != nil {
		return ResourceSource{}, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return ResourceSource{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return ResourceSource{}, copyErr
	}
	if closeErr != nil {
		return ResourceSource{}, closeErr
	}
	return ResourceSource{
		ResourceType: resourceType, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size,
		Open: func(context.Context) (io.ReadCloser, error) { return os.Open(filePath) },
	}, nil
}

func validateResourceType(value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("invalid FHIR resource type %q", value)
	}
	return nil
}

func validChecksum(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
