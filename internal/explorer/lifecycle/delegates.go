package lifecycle

import (
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

func rowSourceProposalRequest(request ConstructionProposalRequest) rowsource.ProposalRequest {
	return rowsource.ProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID,
		FilterSources: request.FilterSources, DeriveSources: request.DeriveSources,
		UnpivotSources: request.UnpivotSources, ExpandSources: request.ExpandSources,
	}
}

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionFilterSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionDeriveSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionExpandSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionExpandSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionUnpivotSourceFacts(snapshot, rootResourceType, fieldPath)
}

func deriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	return rowsource.DeriveSourceLogicalTypeIsNumeric(logicalType)
}

func unpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	return rowsource.UnpivotSourceLogicalTypeIsTableScalar(logicalType)
}

func unpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	return rowsource.UnpivotSourceLogicalTypeIsMeltEligible(logicalType)
}

func unpivotMeltKindsSatisfiable(kinds []string) bool {
	return rowsource.UnpivotMeltKindsSatisfiable(kinds)
}
