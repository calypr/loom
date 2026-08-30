package explorer

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
