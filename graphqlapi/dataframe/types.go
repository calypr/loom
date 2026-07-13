package dataframeapi

import "github.com/calypr/loom/internal/catalog"

type IntrospectionRequest struct {
	Project                string
	RootResourceType       string
	AuthResourcePaths      []string
	IncludePivotOnlyFields bool
}

type IntrospectionResponse struct {
	Project           string
	RootResourceType  string
	AuthResourcePaths []string
	Root              ResourceHints
	RelatedResources  []RelatedResourceHints
	Traversals        []catalog.PopulatedReference
	Fields            []FieldHint
	PivotFields       []FieldHint
}

type ResourceHints struct {
	ResourceType string
	Fields       []FieldHint
	PivotFields  []FieldHint
	Traversals   []catalog.PopulatedReference
}

type RelatedResourceHints struct {
	ViaLabel  string
	EdgeCount int64
	Target    ResourceHints
}

// DatasetSummary is the frontend-facing discovery DTO. It deliberately
// contains catalog facts rather than persistence documents or collection
// names.
type DatasetSummary struct {
	Project           string
	DatasetGeneration string
	State             string
	ResourceTypes     []ResourceTypeSummary
}

type ResourceTypeSummary struct {
	ResourceType        string
	DocumentCount       int64
	PopulatedFieldCount int
	PivotCandidateCount int
}
