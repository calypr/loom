package catalog

// RelationshipRebuildOptions describes the explicit repair path. Normal
// discovery never uses this scan; operators call it after an old dataset was
// loaded before the relationship catalog existed or after a repair.
type RelationshipRebuildOptions struct {
	URL                           string
	Database                      string
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
	Seconds           float64 `json:"seconds"`
}
