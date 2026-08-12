package queryapi

import (
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

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

// ProjectMapRequest discovers every populated, caller-visible resource and
// relationship in one active project generation. Unlike IntrospectionRequest,
// it deliberately has no root resource: the builder uses this response to let
// the user choose a root from facts that are actually present.
type ProjectMapRequest struct {
	Project                string
	AuthResourcePaths      []string
	IncludePivotOnlyFields bool
}

type ProjectMapResponse struct {
	Project          string
	SourceGeneration string
	Resources        []ResourceHints
	Relationships    []catalog.PopulatedReference
}

type ResourceHints struct {
	ResourceType string
	// DocumentCount is the largest authorized field document count observed for
	// this resource type. Catalog rows are field-scoped (there is no separate
	// resource-count row), so this is the strongest non-overcounting resource
	// count derivable from the populated facts. Counts are aggregated across
	// authorized auth-resource partitions before this value is computed.
	DocumentCount int64
	Fields        []FieldHint
	PivotFields   []FieldHint
	Traversals    []catalog.PopulatedReference
}

type SemanticCatalogRequest struct {
	Project                 string
	RootResourceType        string
	AuthResourcePaths       []string
	ResourceLimit           int
	ConceptLimitPerResource int
}

type RecipeColumnCandidatesRequest struct {
	Project, Output, DatasetGeneration, After string
	Recipe                                    recipe.Bundle
	NodePath, AuthResourcePaths               []string
	First                                     int
}

type RecipeColumnCandidatesResponse struct {
	Project, SourceGeneration, EndCursor string
	Candidates                           []schema.ColumnCandidate
	TotalCount                           int
	HasNext, Complete                    bool
	Diagnostics                          []string
}

type RelatedResourceHints struct {
	ViaLabel  string
	EdgeCount int64
	Target    ResourceHints
}
