package catalog

import "encoding/json"

type RetainedSemanticInventoryRow struct {
	Key              string          `json:"key"`
	AuthResourcePath *string         `json:"auth_resource_path"`
	Payload          json.RawMessage `json:"payload"`
}

type RetainedSemanticInventoryPage struct {
	Rows         []RetainedSemanticInventoryRow
	HasMore      bool
	SourceExists bool
}
