// Package schemaidentity identifies the graph schema compiled into this Loom
// process. It intentionally does not decide whether a dataset is compatible
// with that schema; dataset-generation persistence and comparison belong to a
// later layer.
package schemaidentity

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

	"github.com/calypr/loom/internal/fhirschema"
)

var (
	// ErrGraphSchemaPathRequired reports a missing configured graph-schema path.
	ErrGraphSchemaPathRequired = errors.New("graph schema path is required")
	// ErrMalformedGraphSchema reports JSON that cannot safely provide schema
	// identity metadata.
	ErrMalformedGraphSchema = errors.New("malformed graph schema")
)

// Identity is a stable description of the configured graph-schema file and
// the generated FHIR roots available to this Loom binary. Its fields are kept
// private so callers cannot mutate the identity after it has been loaded.
//
// GeneratedResourceTypes is deliberately binary metadata, not a second
// classification of the configured file. Loading a different graph-schema
// file changes its source metadata and byte digest, but later ingestion
// compatibility validation must decide whether that file matches the compiled
// fhirschema artifact.
//
// SchemaID is copied only from the graph schema's top-level "$id" value.
// FHIRVersion is copied only from an explicit top-level "fhirVersion" value;
// Loom does not infer it from a URL, definition description, or resource
// content. It is therefore normally empty for the checked-in schema.
type Identity struct {
	schemaID               string
	fhirVersion            string
	schemaSHA256           string
	generatedResourceTypes []string
}

// Load reads the configured graph-schema JSON file and returns its immutable
// identity. The SHA-256 is computed over the exact bytes read from disk, before
// JSON decoding, so whitespace and any other file change changes the digest.
// The configured file supplies only source metadata; root-resource metadata is
// always read from the generated fhirschema artifact compiled into this binary.
func Load(path string) (Identity, error) {
	if strings.TrimSpace(path) == "" {
		return Identity{}, ErrGraphSchemaPathRequired
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, fmt.Errorf("read graph schema %q: %w", path, err)
	}

	metadata, err := parseMetadata(contents)
	if err != nil {
		return Identity{}, err
	}

	digest := sha256.Sum256(contents)
	resourceTypes := fhirschema.ResourceTypes()
	sort.Strings(resourceTypes)

	return Identity{
		schemaID:               metadata.schemaID,
		fhirVersion:            metadata.fhirVersion,
		schemaSHA256:           hex.EncodeToString(digest[:]),
		generatedResourceTypes: append([]string(nil), resourceTypes...),
	}, nil
}

// SchemaID returns the exact top-level "$id" value, or an empty string when
// the configured graph schema does not declare one.
func (i Identity) SchemaID() string { return i.schemaID }

// FHIRVersion returns the exact top-level "fhirVersion" value, or an empty
// string when the configured graph schema does not explicitly declare it.
func (i Identity) FHIRVersion() string { return i.fhirVersion }

// SchemaSHA256 returns the lower-case hexadecimal SHA-256 of the exact graph
// schema bytes read by Load.
func (i Identity) SchemaSHA256() string { return i.schemaSHA256 }

// GeneratedResourceTypes returns a sorted defensive copy of the concrete FHIR
// resource roots in generated fhirschema metadata. It deliberately does not
// classify JSON definitions in the configured file a second time.
func (i Identity) GeneratedResourceTypes() []string {
	return append([]string(nil), i.generatedResourceTypes...)
}

// MarshalJSON serializes the immutable identity without exposing its backing
// slice. There is intentionally no UnmarshalJSON method: an Identity must be
// created by Load so its digest and generated-resource metadata stay coupled.
func (i Identity) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		SchemaID               string   `json:"schemaId,omitempty"`
		FHIRVersion            string   `json:"fhirVersion,omitempty"`
		SchemaSHA256           string   `json:"schemaSha256"`
		GeneratedResourceTypes []string `json:"generatedResourceTypes"`
	}{
		SchemaID:               i.schemaID,
		FHIRVersion:            i.fhirVersion,
		SchemaSHA256:           i.schemaSHA256,
		GeneratedResourceTypes: i.GeneratedResourceTypes(),
	})
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
	return fmt.Errorf("%w: %s: %v", ErrMalformedGraphSchema, problem, err)
}
