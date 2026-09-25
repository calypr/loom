package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionExpandStage(stage ir.PhysicalConstructionStage) ([]string, error) {
	expand := stage.Expand
	if expand == nil {
		return nil, fmt.Errorf("expand stage is missing payload")
	}
	if _, ok := r.bindVars[expand.ConstructionIDBindKey]; !ok {
		return nil, fmt.Errorf("expand construction ID bind %q is missing", expand.ConstructionIDBindKey)
	}
	columnBind := r.newInternalBindKey("construction_expand_column")
	r.bindVars[columnBind] = expand.InputColumn
	identityColumn := ""
	for _, column := range stage.InputColumns {
		if column.Identity {
			identityColumn = column.Name
			break
		}
	}
	if identityColumn == "" {
		return nil, fmt.Errorf("expand input has no row identity column")
	}
	identityColumnBind := r.newInternalBindKey("construction_expand_identity_column")
	r.bindVars[identityColumnBind] = identityColumn
	rawVariable := r.newInternalVariable("construction_expand_raw")
	errorMessage := fmt.Sprintf("CONCAT(\"row expansion construction \", @%s, \" has no items for row \", %s[@%s])", expand.ConstructionIDBindKey, stage.InputRowVariable, identityColumnBind)
	lines := []string{
		fmt.Sprintf("  LET %s = %s[@%s]", rawVariable, stage.InputRowVariable, columnBind),
		fmt.Sprintf("  FILTER ASSERT(%s == null OR IS_ARRAY(%s), \"CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH\")", rawVariable, rawVariable),
		fmt.Sprintf("  LET %s = (%s == null ? [] : %s)", expand.ItemsVariable, rawVariable, rawVariable),
	}
	if expand.EmptyPolicy == ir.PhysicalUnnestError {
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(LENGTH(%s) > 0, %s)", expand.ItemsVariable, errorMessage))
	}
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? [] : RANGE(0, LENGTH(%s) - 1)", expand.ItemsVariable, expand.ItemsVariable)
	if expand.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", expand.ItemsVariable, expand.ItemsVariable)
	}
	lines = append(lines,
		fmt.Sprintf("  FOR %s IN (%s)", expand.IndexVariable, indices),
		fmt.Sprintf("  LET %s = (%s == null ? null : %s[%s])", expand.ItemVariable, expand.IndexVariable, expand.ItemsVariable, expand.IndexVariable),
	)
	itemType, err := aqlTableScalarType(expand.InputKind)
	if err != nil {
		return nil, fmt.Errorf("expand item: %w", err)
	}
	itemTypeBind := r.newInternalBindKey("construction_expand_item_type")
	r.bindVars[itemTypeBind] = itemType
	lines = append(lines,
		fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH\")", expand.ItemVariable, expand.ItemVariable, itemTypeBind),
		fmt.Sprintf("  LET %s = TO_STRING([[\"input\", %s[@%s]], [\"construction\", @%s], [\"ordinal\", %s]])",
			expand.IdentityVariable, stage.InputRowVariable, identityColumnBind, expand.ConstructionIDBindKey, expand.IndexVariable),
	)
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("output projection: %w", err)
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, output),
		"  RETURN "+stage.OutputRowVariable,
	)
	return lines, nil
}
