package catalog

import "encoding/json"

// RetainedSemanticInventoryRow is one retained source document read through
// the keyset page contract. AuthResourcePath stays a *string so SQL NULL /
// missing and the empty-string legacy namespace remain distinguishable, and
// Payload keeps raw JSON bytes (a nil RawMessage is SQL NULL, distinct from
// the bytes "null").
type RetainedSemanticInventoryRow struct {
	Key              string          `json:"key"`
	AuthResourcePath *string         `json:"auth_resource_path"`
	Payload          json.RawMessage `json:"payload"`
}

// RetainedSemanticInventoryPage is one keyset page of retained source rows
// in stable byte-order key. The source-availability reading is preserved
// across backends: HasMore reports more rows beyond this page; SourceExists
// distinguishes a verified population from a missing or unproven one.
type RetainedSemanticInventoryPage struct {
	Rows         []RetainedSemanticInventoryRow
	HasMore      bool
	SourceExists bool
}
