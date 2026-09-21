package explorer

import (
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/capability"
)

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
