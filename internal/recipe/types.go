// Package recipe defines product intent independently of FHIR and dataframe
// compiler implementation details.
package recipe

const VersionV1 = "v1"

type TemplateID string

const (
	TemplatePatientCohort     TemplateID = "patient_cohort"
	TemplateSpecimenInventory TemplateID = "specimen_inventory"
	TemplateFileManifest      TemplateID = "file_manifest"
	TemplateDiagnoses         TemplateID = "diagnoses"
	TemplateLabsObservations  TemplateID = "labs_observations"
	TemplateStudyEnrollment   TemplateID = "study_enrollment"
)

type Grain string

const (
	GrainPatient         Grain = "patient"
	GrainSpecimen        Grain = "specimen"
	GrainFile            Grain = "file"
	GrainDiagnosis       Grain = "diagnosis"
	GrainObservation     Grain = "observation"
	GrainStudyEnrollment Grain = "study_enrollment"
)

type GenerationPolicy string

const (
	GenerationLatest GenerationPolicy = "latest"
	GenerationPinned GenerationPolicy = "pinned"
)

type ColumnSelection struct {
	ID         string `json:"id"`
	OutputName string `json:"outputName,omitempty"`
}

type FilterOperator string

const (
	FilterEquals      FilterOperator = "equals"
	FilterNotEquals   FilterOperator = "not_equals"
	FilterIn          FilterOperator = "in"
	FilterNotIn       FilterOperator = "not_in"
	FilterExists      FilterOperator = "exists"
	FilterMissing     FilterOperator = "missing"
	FilterContains    FilterOperator = "contains"
	FilterGreaterThan FilterOperator = "greater_than"
	FilterLessThan    FilterOperator = "less_than"
	FilterBetween     FilterOperator = "between"
)

type Filter struct {
	ColumnID string         `json:"columnId"`
	Operator FilterOperator `json:"operator"`
	Values   []string       `json:"values,omitempty"`
}

type DestinationType string

const (
	DestinationPreview       DestinationType = "preview"
	DestinationNDJSON        DestinationType = "ndjson"
	DestinationCSV           DestinationType = "csv"
	DestinationElasticsearch DestinationType = "elasticsearch"
)

// Destination intentionally contains no credentials or backend-specific
// configuration. Those belong to a future authorized delivery adapter.
type Destination struct {
	Type DestinationType `json:"type"`
}

// Recipe is the V1 product-level intent contract. Column and filter IDs are
// opaque semantic IDs issued by Loom's future capability API, never FHIR paths
// or AQL expressions.
type Recipe struct {
	Version          string            `json:"version"`
	Template         TemplateID        `json:"template"`
	TemplateVersion  int               `json:"templateVersion"`
	Project          string            `json:"project"`
	GenerationPolicy GenerationPolicy  `json:"generationPolicy"`
	Generation       string            `json:"generation,omitempty"`
	Grain            Grain             `json:"grain"`
	Columns          []ColumnSelection `json:"columns"`
	Filters          []Filter          `json:"filters,omitempty"`
	Destination      Destination       `json:"destination"`
}
