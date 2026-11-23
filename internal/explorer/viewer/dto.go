package viewer

import (
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/capability"
)

const (
	ManagementRepository  ManagementMode = "REPOSITORY"
	ManagementInteractive ManagementMode = "INTERACTIVE"
)

type ManagementMode string

type Diagnostic struct {
	Severity  string         `json:"severity"`
	Stage     string         `json:"stage,omitempty"`
	Code      string         `json:"code"`
	FieldPath string         `json:"fieldPath,omitempty"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

type DatasetMetadata struct {
	Generation   string          `json:"generation,omitempty"`
	SchemaDigest string          `json:"schemaDigest,omitempty"`
	Outputs      []DatasetOutput `json:"outputs"`
}

type DatasetOutput struct {
	Name        string                       `json:"name"`
	State       string                       `json:"state"`
	Queryable   bool                         `json:"queryable"`
	Fingerprint string                       `json:"fingerprint,omitempty"`
	Selector    *dataset.DataframeSelector   `json:"selector,omitempty"`
	Columns     []publication.PhysicalColumn `json:"columns,omitempty"`
}

type PublicationMetadata struct {
	State       string    `json:"state"`
	Generation  string    `json:"generation,omitempty"`
	ExecutionID string    `json:"executionId,omitempty"`
	RevisionID  string    `json:"revisionId,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

type Materialization struct {
	OutputID          string                       `json:"outputId"`
	Output            string                       `json:"output"`
	MaterializationID string                       `json:"materializationId"`
	Fingerprint       string                       `json:"fingerprint,omitempty"`
	Selector          *dataset.DataframeSelector   `json:"selector,omitempty"`
	Columns           []publication.PhysicalColumn `json:"columns"`
}

type EmittedColumn struct {
	EmissionID            string                          `json:"emissionId"`
	OutputID              string                          `json:"outputId"`
	NodeID                string                          `json:"nodeId,omitempty"`
	SelectionID           string                          `json:"selectionId,omitempty"`
	CandidateID           string                          `json:"candidateId,omitempty"`
	OccurrenceID          string                          `json:"occurrenceId,omitempty"`
	ProjectionMode        string                          `json:"projectionMode,omitempty"`
	AuthoredColumns       []string                        `json:"authoredColumns,omitempty"`
	InputColumns          []string                        `json:"inputColumns,omitempty"`
	ConstructionID        string                          `json:"constructionId,omitempty"`
	PublicColumn          string                          `json:"publicColumn"`
	Label                 string                          `json:"label,omitempty"`
	LogicalType           string                          `json:"logicalType"`
	Cardinality           string                          `json:"cardinality,omitempty"`
	Nullable              bool                            `json:"nullable,omitempty"`
	ResultUnit            *unit.UnitIdentity              `json:"resultUnit,omitempty"`
	Shape                 string                          `json:"shape"`
	SourceResourceType    string                          `json:"sourceResourceType,omitempty"`
	SourcePath            string                          `json:"sourcePath,omitempty"`
	ChoiceArm             string                          `json:"choiceArm,omitempty"`
	Coordinates           []capability.RepeatedCoordinate `json:"coordinates,omitempty"`
	Lossless              bool                            `json:"lossless,omitempty"`
	MLReady               bool                            `json:"mlReady,omitempty"`
	StructuralSuitability string                          `json:"structuralSuitability,omitempty"`
	LossReasons           []string                        `json:"lossReasons,omitempty"`
	Filterable            bool                            `json:"filterable"`
	Chartable             bool                            `json:"chartable"`
	UnitNormalization     *PublicUnitNormalization        `json:"unitNormalization,omitempty"`
}

type PublicUnitRuleIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type PublicUnitNormalization struct {
	Target unit.UnitIdentity        `json:"target"`
	Rules  []PublicUnitRuleIdentity `json:"rules"`
}
