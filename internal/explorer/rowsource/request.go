package rowsource

type ProposalRequest struct {
	Project        string
	ExplorerID     string
	OutputID       string
	ChangedStepID  string
	FilterSources  []ConstructionFilterSourceSelection
	DeriveSources  []ConstructionDeriveSourceSelection
	UnpivotSources []ConstructionUnpivotSourceSelection
	ExpandSources  []ConstructionExpandSourceSelection
}
