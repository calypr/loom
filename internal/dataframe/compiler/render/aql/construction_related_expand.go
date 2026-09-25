package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionRelatedExpandStage(stage ir.PhysicalConstructionStage) ([]string, error) {
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
	errorMessage := fmt.Sprintf(
		"CONCAT(\"related expansion construction \", @%s, \" has no related records for row \", %s[@%s])",
		related.ConstructionIDBindKey, stage.InputRowVariable, identityColumnBind,
	)
	lines := []string{fmt.Sprintf("  LET %s = %s", related.RelatedRecordsVariable, records)}
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
		fmt.Sprintf(
			"  LET %s = TO_STRING([[\"input\", %s[@%s]], [\"construction\", @%s], (%s == null ? [\"empty\"] : [\"related\", %s.terminal_id])])",
			related.IdentityVariable, stage.InputRowVariable, identityColumnBind, related.ConstructionIDBindKey, related.ItemVariable, related.ItemVariable,
		),
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
