package physical

import "github.com/calypr/loom/internal/dataframe/compiler/ir"

type PhysicalPlan = ir.PhysicalPlan
type PhysicalProjection = ir.PhysicalProjection
type PhysicalCombineInputRef = ir.PhysicalCombineInputRef
type PhysicalStageSequence = ir.PhysicalStageSequence
type PhysicalGroupRows = ir.PhysicalGroupRows
type PhysicalConstructionStage = ir.PhysicalConstructionStage
type PhysicalClickHouseCombine = ir.PhysicalClickHouseCombine

const (
	PhysicalReturnOp = ir.PhysicalReturnOp
	PhysicalUnnestOp = ir.PhysicalUnnestOp
)
