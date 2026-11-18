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
	ErrGraphSchemaPathRequired = errors.New("graph schema path is required")

	ErrMalformedGraphSchema = errors.New("malformed graph schema")
)

func loadSchemaSnapshot(path string) (publication.SchemaSnapshot, error) {
	contents, metadata, err := readSchemaMetadata(path)
	if err != nil {
		return publication.SchemaSnapshot{}, err
	}
	return buildSchemaSnapshot(contents, metadata)
}

func readSchemaMetadata(path string) ([]byte, metadata, error) {
	if strings.TrimSpace(path) == "" {
		return nil, metadata{}, ErrGraphSchemaPathRequired
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, metadata{}, fmt.Errorf("read graph schema %q: %w", path, err)
	}
	parsed, err := parseMetadata(contents)
	if err != nil {
		return nil, metadata{}, err
	}
	return contents, parsed, nil
}

func buildSchemaSnapshot(contents []byte, metadata metadata) (publication.SchemaSnapshot, error) {
	digest := sha256.Sum256(contents)
	resourceTypes := fhirschema.ResourceTypes()
	sort.Strings(resourceTypes)
	return publication.NewSchemaSnapshot(metadata.schemaID, metadata.fhirVersion, hex.EncodeToString(digest[:]), resourceTypes)
}

type metadata struct {
	schemaID    string
	fhirVersion string
	definitions json.RawMessage
	rootTypes   json.RawMessage
}

type runtimeSchemaDefinition struct {
	ID    string                     `json:"$id"`
	Ref   string                     `json:"$ref"`
	Title string                     `json:"title"`
	Type  json.RawMessage            `json:"type"`
	AnyOf []*runtimeSchemaDefinition `json:"anyOf"`
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
	seen := make(map[string]struct{}, 4)
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
		case "$defs":
			if _, duplicate := seen[key]; duplicate {
				return metadata{}, malformedGraphSchemaError("duplicate top-level \"$defs\"", nil)
			}
			seen[key] = struct{}{}
			result.definitions = value
		case "anyOf":
			if _, duplicate := seen[key]; duplicate {
				return metadata{}, malformedGraphSchemaError("duplicate top-level \"anyOf\"", nil)
			}
			seen[key] = struct{}{}
			result.rootTypes = value
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

func parseRuntimeClasses(metadata metadata) (map[string]struct{}, error) {
	var definitions map[string]*runtimeSchemaDefinition
	if err := json.Unmarshal(metadata.definitions, &definitions); err != nil {
		return nil, malformedGraphSchemaError("decode top-level \"$defs\"", err)
	}
	if definitions == nil {
		return nil, malformedGraphSchemaError("top-level \"$defs\" must be an object", nil)
	}
	byReference := make(map[string]*runtimeSchemaDefinition, len(definitions)*3)
	escapePointer := strings.NewReplacer("~", "~0", "/", "~1")
	for name, definition := range definitions {
		if definition == nil {
			return nil, malformedGraphSchemaError(fmt.Sprintf("definition %q must be an object", name), nil)
		}
		pointer := "#/$defs/" + escapePointer.Replace(name)
		byReference[pointer] = definition
		byReference[metadata.schemaID+pointer] = definition
		if definition.ID != "" {
			if _, exists := byReference[definition.ID]; exists {
				return nil, malformedGraphSchemaError(fmt.Sprintf("duplicate schema reference %q", definition.ID), nil)
			}
			byReference[definition.ID] = definition
		}
	}
	var roots []*runtimeSchemaDefinition
	if err := json.Unmarshal(metadata.rootTypes, &roots); err != nil {
		return nil, malformedGraphSchemaError("decode top-level \"anyOf\"", err)
	}
	if roots == nil {
		return nil, malformedGraphSchemaError("top-level \"anyOf\" must be an array", nil)
	}
	classes := make(map[string]struct{})
	visited := make(map[*runtimeSchemaDefinition]bool)
	var scan func(*runtimeSchemaDefinition) bool
	scan = func(definition *runtimeSchemaDefinition) bool {
		if definition == nil {
			return false
		}
		if isObject, seen := visited[definition]; seen {
			return isObject
		}
		visited[definition] = false
		isObject := hasObjectType(definition.Type)
		if definition.Ref != "" {
			isObject = scan(byReference[definition.Ref]) || isObject
		}
		if isObject && definition.Title != "" {
			classes[definition.Title] = struct{}{}
		}
		for _, child := range definition.AnyOf {
			scan(child)
		}
		visited[definition] = isObject
		return isObject
	}
	for _, root := range roots {
		scan(root)
	}
	return classes, nil
}

func hasObjectType(raw json.RawMessage) bool {
	var typeName string
	if err := json.Unmarshal(raw, &typeName); err == nil {
		return typeName == "object"
	}
	var typeNames []string
	if err := json.Unmarshal(raw, &typeNames); err != nil {
		return false
	}
	for _, typeName := range typeNames {
		if typeName == "object" {
			return true
		}
	}
	return false
}

type runtimeGraphSchema struct {
	classes map[string]struct{}
}

func (s *runtimeGraphSchema) HasClass(name string) bool {
	_, ok := s.classes[name]
	return ok
}

func loadRuntimeGraphSchema(path string) (*runtimeGraphSchema, error) {
	_, schema, err := loadSchema(path)
	return schema, err
}

func loadSchema(path string) (publication.SchemaSnapshot, *runtimeGraphSchema, error) {
	contents, metadata, err := readSchemaMetadata(path)
	if err != nil {
		return publication.SchemaSnapshot{}, nil, err
	}
	if metadata.schemaID == "" {
		return publication.SchemaSnapshot{}, nil, malformedGraphSchemaError("graph schema requires a string \"$id\" and object \"$defs\"", nil)
	}
	classes, err := parseRuntimeClasses(metadata)
	if err != nil {
		return publication.SchemaSnapshot{}, nil, err
	}
	snapshot, err := buildSchemaSnapshot(contents, metadata)
	if err != nil {
		return publication.SchemaSnapshot{}, nil, err
	}
	return snapshot, &runtimeGraphSchema{classes: classes}, nil
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
