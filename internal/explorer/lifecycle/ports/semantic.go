package ports

import (
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/semantic"
)

type FrameSourceFamilyCandidate struct {
	Family    capability.SemanticFrameFamily
	Entry     semantic.Entry
	Candidate capability.Candidate
}
