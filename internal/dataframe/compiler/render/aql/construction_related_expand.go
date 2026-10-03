package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (r *physicalPlanRenderer) renderConstructionRelatedExpandStage(stage ir.PhysicalConstructionStage, previewLimitBindKey string) ([]string, error) {
	related := stage.RelatedExpand
	if related == nil {
		return nil, fmt.Errorf("related expansion stage is missing its typed payload")
	}
	records, err := r.renderSubplan(related.RelatedRecords, "  ", false)
	if err != nil {
		return nil, fmt.Errorf("render exact related-resource route: %w", err)
	}
	identityColumnBind := r.newInternalBindKey("related_expand_parent_identity_column")
	r.bindVars[identityColumnBind] = related.ParentIdentityColumn
	lines := make([]string, 0, 12)
	if previewLimitBindKey != "" {
		rawRecordsVariable := r.newInternalVariable("related_expand_raw_records")
		candidateVariable := r.newInternalVariable("related_expand_preview_candidate")
		// Keeping each parent's first N rows by the final row identity preserves
		// the global first N: any discarded row already has N predecessors in
		// its own parent partition.
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", rawRecordsVariable, records),
			fmt.Sprintf("  LET %s = (", related.RelatedRecordsVariable),
			fmt.Sprintf("    FOR %s IN %s", candidateVariable, rawRecordsVariable),
			fmt.Sprintf("      SORT %s ASC", constructionRelatedExpandIdentityExpression(stage.InputRowVariable, identityColumnBind, related.ConstructionIDBindKey, candidateVariable)),
			fmt.Sprintf("      LIMIT @%s", previewLimitBindKey),
			fmt.Sprintf("      RETURN %s", candidateVariable),
			"  )",
		)
	} else {
		lines = append(lines, fmt.Sprintf("  LET %s = %s", related.RelatedRecordsVariable, records))
	}
	errorMessage := fmt.Sprintf(
		"CONCAT(\"%s: related expansion construction \", @%s, \" has no related records for row \", %s[@%s])",
		dataframeerrors.CodeConstructionExpansionEmpty, related.ConstructionIDBindKey, stage.InputRowVariable, identityColumnBind,
	)
	if related.EmptyPolicy == ir.PhysicalUnnestError {
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(LENGTH(%s) > 0, %s)", related.RelatedRecordsVariable, errorMessage))
	}
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? [] : RANGE(0, LENGTH(%s) - 1)", related.RelatedRecordsVariable, related.RelatedRecordsVariable)
	if related.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", related.RelatedRecordsVariable, related.RelatedRecordsVariable)
	}
	lines = append(lines,
		fmt.Sprintf("  FOR %s IN (%s)", related.IndexVariable, indices),
		fmt.Sprintf("  LET %s = (%s == null ? null : %s[%s])", related.ItemVariable, related.IndexVariable, related.RelatedRecordsVariable, related.IndexVariable),
		fmt.Sprintf("  LET %s = %s", related.IdentityVariable, constructionRelatedExpandIdentityExpression(stage.InputRowVariable, identityColumnBind, related.ConstructionIDBindKey, related.ItemVariable)),
	)
	object, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("render related expansion output row: %w", err)
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, object),
		"  RETURN "+stage.OutputRowVariable,
	)
	return lines, nil
}

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
