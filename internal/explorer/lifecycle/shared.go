package lifecycle

import (
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/construction"
)

type constructionBase = construction.Base

func constructionStepIndex(steps []ws.ConstructionStep, id string) int {
	for index, step := range steps {
		if step.ID == id {
			return index
		}
	}
	return -1
}
func constructionOwnerInputsClearedFromCandidate(base ws.Construction, candidate ws.Construction, ownerID string) bool {
	baseInputs := 0
	for _, step := range base.Steps {
		if step.OwnerStepID == ownerID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
			baseInputs++
		}
	}
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == ownerID {
			baseInputs++
		}
	}
	if baseInputs == 0 {
		return false
	}
	for _, step := range candidate.Steps {
		if step.OwnerStepID == ownerID && step.Operation.Kind == ws.ConstructionOperationRelatedField {
			return false
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == ownerID {
			return false
		}
	}
	return true
}
func hasPivotSourceProjection(projections []ws.ConstructionSourceProjection, columnID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID {
			return true
		}
	}
	return false
}
