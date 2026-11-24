package lifecycle

import (
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowsource"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionFilterSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionDeriveSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionUnpivotSourceFacts(snapshot, rootResourceType, fieldPath)
}
