package execution

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

func relatedExpandRootPageOrder(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) == 0 {
		return false
	}
	for _, stage := range sequence.Stages {
		if stage.Kind != ir.PhysicalStageRelatedExpandOp {
			return false
		}
	}
	return true
}
