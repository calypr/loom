package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	publication "github.com/calypr/loom/internal/dataset"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var (
	// ErrGraphSchemaPathRequired reports a missing configured graph-schema path.
	ErrGraphSchemaPathRequired = errors.New("graph schema path is required")
	// ErrMalformedGraphSchema reports JSON that cannot safely provide schema
	// identity metadata.
	ErrMalformedGraphSchema = errors.New("malformed graph schema")
)

// loadSchemaSnapshot records exact source bytes and generated FHIR roots.
func loadSchemaSnapshot(path string) (publication.SchemaSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return publication.SchemaSnapshot{}, ErrGraphSchemaPathRequired
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return publication.SchemaSnapshot{}, fmt.Errorf("read graph schema %q: %w", path, err)
	}

	metadata, err := parseMetadata(contents)
	if err != nil {
		return publication.SchemaSnapshot{}, err
	}

	digest := sha256.Sum256(contents)
	resourceTypes := fhirschema.ResourceTypes()
	sort.Strings(resourceTypes)

	return publication.NewSchemaSnapshot(metadata.schemaID, metadata.fhirVersion, hex.EncodeToString(digest[:]), resourceTypes)
}

type metadata struct {
	schemaID    string
	fhirVersion string
}

func parseMetadata(contents []byte) (metadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	start, err := decoder.Token()
	if err != nil {
		return metadata{}, malformedGraphSchemaError("decode JSON", err)
	}
	if start != json.Delim('{') {
		return metadata{}, malformedGraphSchemaError("top-level JSON value must be an object", nil)
	}

	var result metadata
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return metadata{}, malformedGraphSchemaError("read object key", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return metadata{}, malformedGraphSchemaError("top-level object key is not a string", nil)
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return metadata{}, malformedGraphSchemaError(fmt.Sprintf("read top-level field %q", key), err)
		}

		switch key {
		case "$id":
			if _, duplicate := seen[key]; duplicate {
				return metadata{}, malformedGraphSchemaError("duplicate top-level \"$id\"", nil)
			}
			seen[key] = struct{}{}
			value, err := requiredJSONString(value)
			if err != nil {
				return metadata{}, malformedGraphSchemaError("top-level \"$id\" must be a string", err)
			}
			result.schemaID = value
		case "fhirVersion":
			if _, duplicate := seen[key]; duplicate {
				return metadata{}, malformedGraphSchemaError("duplicate top-level \"fhirVersion\"", nil)
			}
			seen[key] = struct{}{}
			value, err := requiredJSONString(value)
			if err != nil {
				return metadata{}, malformedGraphSchemaError("top-level \"fhirVersion\" must be a string", err)
			}
			result.fhirVersion = value
		}
	}

	end, err := decoder.Token()
	if err != nil {
		return metadata{}, malformedGraphSchemaError("finish top-level object", err)
	}
	if end != json.Delim('}') {
		return metadata{}, malformedGraphSchemaError("top-level JSON value must be an object", nil)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return metadata{}, malformedGraphSchemaError("multiple top-level JSON values", nil)
		}
		return metadata{}, malformedGraphSchemaError("finish JSON document", err)
	}

	return result, nil
}

func requiredJSONString(raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("null is not a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func malformedGraphSchemaError(problem string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrMalformedGraphSchema, problem)
	}
	return fmt.Errorf("%w: %s: %w", ErrMalformedGraphSchema, problem, err)
}
