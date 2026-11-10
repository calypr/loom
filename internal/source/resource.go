package source

import "encoding/json"

// Resource is one logical FHIR resource offered to or read back from a
// source store. It mirrors the source-graph vertex identity (project,
// dataset generation, resource type, logical FHIR id, and auth path as
// typed fields; the resource document as JSON bytes) so the same logical
// identity reads identically from either supported backend.
//
// A Resource never carries a storage-assigned identity: the storage
// sequence is assigned by the backend on commit and returned separately or
// on its physical read row. A write built from a Resource cannot supply or
// collide with one.
type Resource struct {
	// Project is the owning project. Required, non-blank.
	Project string
	// DatasetGeneration is the owning immutable dataset generation. The
	// empty string is the legacy dataset namespace, never "any generation".
	DatasetGeneration string
	// ResourceType is the canonical FHIR resource type. Required, non-blank.
	ResourceType string
	// LogicalID is the logical FHIR resource id (the payload "id").
	// Required, non-blank. It is preserved unchanged, independent of any
	// backend's physical key.
	LogicalID string
	// AuthResourcePath is the typed, non-null authorization scope value.
	// The empty string is the legacy (unscoped) namespace.
	AuthResourcePath string
	// Payload is the resource document as JSON bytes. A nil Payload means
	// no resource document is present and stays distinct, through every
	// backend round trip, from the bytes "null", which is an explicit JSON
	// null document. Within a JSON document a missing key stays absent, an
	// explicit JSON null stays a present key with value null, and empty
	// arrays and empty objects stay empty containers.
	Payload json.RawMessage
}

// ResourceScope binds one resource read to exactly one storage identity and
// its authorization scope, with no implicit modes: restricted and
// unrestricted access are chosen deliberately and a restricted-empty scope
// can never be mistaken for unrestricted access.
type ResourceScope struct {
	// Project is required and matched exactly.
	Project string
	// DatasetGeneration is matched exactly. The empty string is the legacy
	// dataset namespace, never "any generation". An omitted generation is
	// never an all-generation query.
	DatasetGeneration string
	// AuthResourcePathsUnrestricted must be non-nil: true selects the
	// explicit unrestricted mode (then AuthResourcePaths must be empty),
	// false selects restricted mode. A nil value is an error, not the
	// legacy "empty means unrestricted" convention, so a restricted-empty
	// authorization can never be mistaken for unrestricted access.
	AuthResourcePathsUnrestricted *bool
	// AuthResourcePaths are the authorized auth_resource_path values in
	// restricted mode. An empty list in restricted mode is a legitimate
	// authorization state that reads nothing.
	AuthResourcePaths []string
}
