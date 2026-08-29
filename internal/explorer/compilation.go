package explorer

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// Compilation is the transport-neutral result shared by the V2 compiler and
// publication lifecycle. It contains executable recipe data and derived
// physical columns; the lossless ExplorerConfigV2 packet is stored separately.
type Compilation struct {
	Bundle         recipe.Bundle
	RecipeDigest   string
	EmittedColumns []EmittedColumn
}

type EmittedColumn struct {
	EmissionID     string `json:"emissionId"`
	OutputID       string `json:"outputId"`
	NodeID         string `json:"nodeId,omitempty"`
	SelectionID    string `json:"selectionId,omitempty"`
	CandidateID    string `json:"candidateId,omitempty"`
	OccurrenceID   string `json:"occurrenceId,omitempty"`
	ProjectionMode string `json:"projectionMode,omitempty"`
	PublicColumn   string `json:"publicColumn"`
	Label          string `json:"label,omitempty"`
	LogicalType    string `json:"logicalType"`
	Filterable     bool   `json:"filterable"`
	Chartable      bool   `json:"chartable"`
}
