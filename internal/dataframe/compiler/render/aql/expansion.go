package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func constructionRelatedExpandIdentityExpression(inputRow, parentIdentityColumnBind, constructionIDBind, itemVariable string) string {
	return fmt.Sprintf(
		"TO_STRING([[\"input\", %s[@%s]], [\"construction\", @%s], (%s == null ? [\"empty\"] : [\"related\", %s.terminal_id])])",
		inputRow, parentIdentityColumnBind, constructionIDBind, itemVariable, itemVariable,
	)
}

func terminalRelatedExpandPreviewStage(sequence *ir.PhysicalStageSequence) (int, bool) {
	if sequence == nil || len(sequence.Stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil {
		return -1, false
	}
	index := len(sequence.Stages) - 1
	stage := sequence.Stages[index]
	if stage.ID != sequence.FinalStageID || stage.Kind != ir.PhysicalStageRelatedExpandOp ||
		stage.RelatedExpand == nil || stage.RowIdentityColumn == "" || stage.RowIdentityColumn != sequence.FinalRowIdentity ||
		stage.Filter != nil || len(stage.DerivedLets) != 0 || stage.RelatedExpand.IdentityVariable == "" {
		return -1, false
	}
	for _, projection := range stage.OutputProjections {
		if projection.Name != stage.RowIdentityColumn {
			continue
		}
		return index, projection.Expression == nil && projection.Value.Variable == stage.RelatedExpand.IdentityVariable &&
			projection.Value.BindKey == "" && len(projection.Value.Path) == 0
	}
	return -1, false
}
