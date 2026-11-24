package fieldprofile

import (
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"github.com/calypr/loom/internal/semantic"
)

const (
	FieldCatalogCollection       = fieldcatalog.FieldCatalogCollection
	FieldProfileStatusRecordType = fieldcatalog.FieldProfileStatusRecordType
	fieldKindScalar              = "scalar"
	fieldKindObject              = "object"
	fieldKindArray               = "array"
	fieldKindCodeableConcept     = "codeable_concept"
	fieldKindCoding              = "coding"
	pivotKindCodeableConcept     = "codeable_concept_display_value"
	pivotKindObservation         = "observation_code_value"
)

type Profiler struct {
	project                  string
	datasetGeneration        string
	semanticInventoryBuildID string
	authResourcePath         string
	resourceType             string
	semanticSourceKind       string
	semanticOnly             bool
	semanticExtractor        semantic.Extractor
	limits                   ProfileLimits
	shapeCache               *ShapePlanCache
	stats                    map[string]*fieldCatalogStats
	retainedBytes            int
	truncated                bool
	enrichmentTruncated      bool
	budget                   *retentionBudget
}

type fieldCatalogStats struct {
	path                  string
	kind                  string
	docCount              int64
	maxItems              int
	distinctValues        []string
	distinctSet           map[string]struct{}
	distinctTruncated     bool
	pivotCandidate        bool
	pivotKind             string
	pivotColumns          []string
	pivotColumnSet        map[string]struct{}
	pivotFamily           string
	pivotColumnSelect     string
	pivotValueSelect      string
	pivotItemSource       string
	pivotItemResourceType string
	pivotValueSelectors   []string
	extensionValues       []fieldcatalog.ExtensionValueObservation
	extensionValueSet     map[string]struct{}
	semanticObservations  map[string]*semanticObservationStats
}

type semanticObservationStats struct {
	observation semantic.Observation
	exampleSet  map[string]struct{}
	unitSet     map[string]struct{}
}
