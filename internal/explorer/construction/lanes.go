package construction

import "github.com/calypr/loom/internal/explorer/rowsource"

func (request ConstructionProposalRequest) RowSourceRequest() rowsource.ProposalRequest {
	return rowsource.ProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID,
		FilterSources: request.FilterSources, DeriveSources: request.DeriveSources,
		UnpivotSources: request.UnpivotSources, ExpandSources: request.ExpandSources,
	}
}
