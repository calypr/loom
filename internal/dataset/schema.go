package dataset

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type SchemaSnapshot struct {
	SchemaID               string   `json:"schemaId,omitempty"`
	FHIRVersion            string   `json:"fhirVersion,omitempty"`
	SchemaSHA256           string   `json:"schemaSha256"`
	GeneratedResourceTypes []string `json:"generatedResourceTypes"`
}

func NewSchemaSnapshot(schemaID, fhirVersion, sha256 string, resourceTypes []string) (SchemaSnapshot, error) {
	resourceTypes = append([]string(nil), resourceTypes...)
	sort.Strings(resourceTypes)
	snapshot := SchemaSnapshot{schemaID, fhirVersion, sha256, resourceTypes}
	if err := snapshot.Validate(); err != nil {
		return SchemaSnapshot{}, err
	}
	return snapshot, nil
}

func (s SchemaSnapshot) Validate() error {
	if err := validateOptionalSchemaMetadata("schemaId", s.SchemaID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSchemaIdentity, err)
	}
	if err := validateOptionalSchemaMetadata("fhirVersion", s.FHIRVersion); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSchemaIdentity, err)
	}
	if len(s.SchemaSHA256) != 64 || strings.ToLower(s.SchemaSHA256) != s.SchemaSHA256 {
		return fmt.Errorf("%w: schemaSha256 must be a lower-case 64-character SHA-256 digest", ErrInvalidSchemaIdentity)
	}
	if _, err := hex.DecodeString(s.SchemaSHA256); err != nil {
		return fmt.Errorf("%w: schemaSha256 is not hexadecimal: %w", ErrInvalidSchemaIdentity, err)
	}
	if len(s.GeneratedResourceTypes) == 0 {
		return fmt.Errorf("%w: generatedResourceTypes is required", ErrInvalidSchemaIdentity)
	}
	if len(s.GeneratedResourceTypes) > maxResourceTypes {
		return fmt.Errorf("%w: generatedResourceTypes exceeds %d entries", ErrInvalidSchemaIdentity, maxResourceTypes)
	}
	if !sort.StringsAreSorted(s.GeneratedResourceTypes) {
		return fmt.Errorf("%w: generatedResourceTypes must be sorted", ErrInvalidSchemaIdentity)
	}
	for i, resourceType := range s.GeneratedResourceTypes {
		if err := validateOpaqueIdentifier(fmt.Sprintf("generatedResourceTypes[%d]", i), resourceType); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidSchemaIdentity, err)
		}
		if i > 0 && resourceType == s.GeneratedResourceTypes[i-1] {
			return fmt.Errorf("%w: generatedResourceTypes[%d] duplicates %q", ErrInvalidSchemaIdentity, i, resourceType)
		}
	}
	return nil
}

func validateOptionalSchemaMetadata(field, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxOpaqueIdentifierBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxOpaqueIdentifierBytes)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must be valid UTF-8 without control characters", field)
		}
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8 without control characters", field)
	}
	return nil
}
