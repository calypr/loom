package catalog

// RelationshipRebuildOptions describes the explicit repair path. Normal
// discovery never uses this scan; operators call it after an old dataset was
// loaded before the relationship catalog existed or after a repair.
type RelationshipRebuildOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted *bool
	CursorBatch                   int
	BatchSize                     int
	WriteAPI                      string
}

type RelationshipRebuildSummary struct {
	Project           string  `json:"project"`
	DatasetGeneration string  `json:"dataset_generation,omitempty"`
	Rows              int     `json:"rows"`
	EdgeCount         int64   `json:"edge_count"`
	InvalidEdgeCount  int64   `json:"invalid_edge_count,omitempty"`
	Seconds           float64 `json:"seconds"`
}

// RelationshipAuditOptions identifies the immutable edge namespace to audit.
// The audit is read-only and is intentionally separate from catalog rebuild so
// operators can inspect malformed historical data before choosing a repair.
type RelationshipAuditOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted *bool
	CursorBatch                   int
}

type InvalidRelationship struct {
	FromType  string `json:"from_type"`
	Label     string `json:"label"`
	ToType    string `json:"to_type"`
	EdgeCount int64  `json:"edge_count"`
}

type RelationshipAuditSummary struct {
	Project           string                `json:"project"`
	DatasetGeneration string                `json:"dataset_generation,omitempty"`
	InvalidEdgeCount  int64                 `json:"invalid_edge_count"`
	InvalidRelations  []InvalidRelationship `json:"invalid_relationships,omitempty"`
}
