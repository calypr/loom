package execution

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

// Related expansions embed the previous row identity as their first identity
// component. With root-key source identity, sorting each root page preserves
// the same preview prefix as a larger page. Start small to avoid expanding
// later roots after an early root has already filled the preview.
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
