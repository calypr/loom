package dataset

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/calypr/loom/internal/graphschema"
)

// SchemaIdentitySnapshot is the serializable, immutable schema metadata
// attached to a dataset generation. It is copied from the active Loom binary's
// graphschema.Identity when a generation is created.
//
// FHIRVersion is deliberately empty when the configured graph schema does not
// explicitly declare one. This package never infers a FHIR version from a URL,
// resource type, or generated code.
type SchemaIdentitySnapshot struct {
	schemaID               string
	fhirVersion            string
	schemaSHA256           string
	generatedResourceTypes []string
}

// SnapshotSchemaIdentity copies an immutable graphschema.Identity into the
// public dataset lifecycle value. The resulting snapshot has no reference to
// the source identity's backing data.
func SnapshotSchemaIdentity(identity graphschema.Identity) (SchemaIdentitySnapshot, error) {
	return NewSchemaIdentitySnapshot(
		identity.SchemaID(),
		identity.FHIRVersion(),
		identity.SchemaSHA256(),
		identity.GeneratedResourceTypes(),
	)
}

// NewSchemaIdentitySnapshot creates a validated schema snapshot. Resource
// types are canonicalized into lexicographic order; duplicate or malformed
// entries are rejected rather than silently coalesced.
func NewSchemaIdentitySnapshot(schemaID, fhirVersion, schemaSHA256 string, generatedResourceTypes []string) (SchemaIdentitySnapshot, error) {
	resourceTypes := append([]string(nil), generatedResourceTypes...)
	sort.Strings(resourceTypes)
	snapshot := SchemaIdentitySnapshot{
		schemaID:               schemaID,
		fhirVersion:            fhirVersion,
		schemaSHA256:           schemaSHA256,
		generatedResourceTypes: resourceTypes,
	}
	if err := snapshot.Validate(); err != nil {
		return SchemaIdentitySnapshot{}, err
	}
	return snapshot, nil
}

// SchemaID returns the exact graph-schema $id copied from graphschema, or
// an empty string when the source schema does not declare one.
func (s SchemaIdentitySnapshot) SchemaID() string { return s.schemaID }

// FHIRVersion returns only an explicitly declared source FHIR version. An
// empty result means that the source schema did not declare fhirVersion.
func (s SchemaIdentitySnapshot) FHIRVersion() string { return s.fhirVersion }

// SchemaSHA256 returns the SHA-256 digest of the exact graph-schema bytes.
func (s SchemaIdentitySnapshot) SchemaSHA256() string { return s.schemaSHA256 }

// GeneratedResourceTypes returns a sorted defensive copy of the compiled
// generated FHIR roots captured in this snapshot.
func (s SchemaIdentitySnapshot) GeneratedResourceTypes() []string {
	return append([]string(nil), s.generatedResourceTypes...)
}

// Clone returns an independent value with its own resource-type slice.
func (s SchemaIdentitySnapshot) Clone() SchemaIdentitySnapshot {
	return SchemaIdentitySnapshot{
		schemaID:               s.schemaID,
		fhirVersion:            s.fhirVersion,
		schemaSHA256:           s.schemaSHA256,
		generatedResourceTypes: s.GeneratedResourceTypes(),
	}
}

// Equal reports exact identity equality. Generated resource types are already
// canonicalized, so ordering cannot make equivalent snapshots compare unequal.
func (s SchemaIdentitySnapshot) Equal(other SchemaIdentitySnapshot) bool {
	return s.schemaID == other.schemaID &&
		s.fhirVersion == other.fhirVersion &&
		s.schemaSHA256 == other.schemaSHA256 &&
		slices.Equal(s.generatedResourceTypes, other.generatedResourceTypes)
}

// Validate checks a persisted or manually constructed snapshot. It does not
// re-read a schema file; compatibility decisions belong to an ingest adapter.
func (s SchemaIdentitySnapshot) Validate() error {
	if err := validateOptionalSchemaMetadata("schemaId", s.schemaID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchemaIdentity, err)
	}
	if err := validateOptionalSchemaMetadata("fhirVersion", s.fhirVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchemaIdentity, err)
	}
	if len(s.schemaSHA256) != 64 || strings.ToLower(s.schemaSHA256) != s.schemaSHA256 {
		return fmt.Errorf("%w: schemaSha256 must be a lower-case 64-character SHA-256 digest", ErrInvalidSchemaIdentity)
	}
	if _, err := hex.DecodeString(s.schemaSHA256); err != nil {
		return fmt.Errorf("%w: schemaSha256 is not hexadecimal: %v", ErrInvalidSchemaIdentity, err)
	}
	if len(s.generatedResourceTypes) == 0 {
		return fmt.Errorf("%w: generatedResourceTypes is required", ErrInvalidSchemaIdentity)
	}
	if len(s.generatedResourceTypes) > maxResourceTypes {
		return fmt.Errorf("%w: generatedResourceTypes exceeds %d entries", ErrInvalidSchemaIdentity, maxResourceTypes)
	}
	if !sort.StringsAreSorted(s.generatedResourceTypes) {
		return fmt.Errorf("%w: generatedResourceTypes must be sorted", ErrInvalidSchemaIdentity)
	}
	for index, resourceType := range s.generatedResourceTypes {
		if err := validateOpaqueIdentifier(fmt.Sprintf("generatedResourceTypes[%d]", index), resourceType, false); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSchemaIdentity, err)
		}
		if index > 0 && resourceType == s.generatedResourceTypes[index-1] {
			return fmt.Errorf("%w: generatedResourceTypes[%d] duplicates %q", ErrInvalidSchemaIdentity, index, resourceType)
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
	if !utf8ValidNonControl(value) {
		return fmt.Errorf("%s must be valid UTF-8 without control characters", field)
	}
	return nil
}

func utf8ValidNonControl(value string) bool {
	// Schema metadata must retain the exact text that graphschema observed;
	// unlike a project key it is not whitespace-normalized here.
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return utf8.ValidString(value)
}

func (s SchemaIdentitySnapshot) MarshalJSON() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(schemaIdentitySnapshotWire{
		SchemaID:               s.schemaID,
		FHIRVersion:            s.fhirVersion,
		SchemaSHA256:           s.schemaSHA256,
		GeneratedResourceTypes: s.GeneratedResourceTypes(),
	})
}

func (s *SchemaIdentitySnapshot) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil SchemaIdentitySnapshot", ErrInvalidSchemaIdentity)
	}
	var decoded schemaIdentitySnapshotWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidSchemaIdentity, err)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err != nil {
		return fmt.Errorf("%w: decode JSON fields: %v", ErrInvalidSchemaIdentity, err)
	}
	for _, field := range []string{"schemaId", "fhirVersion"} {
		if raw, present := rawFields[field]; present && isJSONNull(raw) {
			return fmt.Errorf("%w: %s must be a string when present", ErrInvalidSchemaIdentity, field)
		}
	}
	snapshot, err := NewSchemaIdentitySnapshot(
		decoded.SchemaID,
		decoded.FHIRVersion,
		decoded.SchemaSHA256,
		decoded.GeneratedResourceTypes,
	)
	if err != nil {
		return err
	}
	*s = snapshot
	return nil
}

type schemaIdentitySnapshotWire struct {
	SchemaID               string   `json:"schemaId,omitempty"`
	FHIRVersion            string   `json:"fhirVersion,omitempty"`
	SchemaSHA256           string   `json:"schemaSha256"`
	GeneratedResourceTypes []string `json:"generatedResourceTypes"`
}
