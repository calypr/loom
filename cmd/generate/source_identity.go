package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func sourceSchemaDigest(schemaPath, resourceTypesPath, definitionsPath string) (string, error) {
	digest := sha256.New()
	writeInput := func(name, path string) error {
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		info, err := input.Stat()
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%s\n%d\n", name, info.Size())
		if _, err := io.Copy(digest, input); err != nil {
			return fmt.Errorf("hash %s: %w", path, err)
		}
		return nil
	}
	if err := writeInput("schema", schemaPath); err != nil {
		return "", err
	}
	if err := writeInput("resource types", resourceTypesPath); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(definitionsPath)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (name != "package.json" && !strings.HasPrefix(name, "StructureDefinition-")) {
			continue
		}
		if err := writeInput(name, filepath.Join(definitionsPath, name)); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
