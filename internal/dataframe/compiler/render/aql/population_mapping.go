package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderPopulationMappingReturn(terminal ir.PhysicalPopulationMappingReturn) ([]string, error) {
	members, err := r.renderExpression(terminal.Members)
	if err != nil {
		return nil, fmt.Errorf("mapping members: %w", err)
	}
	rowID, err := r.renderExpression(terminal.RowID)
	if err != nil {
		return nil, fmt.Errorf("mapping row identity: %w", err)
	}
	memberVariable := r.newInternalVariable("population_mapping_member")
	memberNameKey := r.newInternalBindKey("population_mapping_member_name")
	rowIDNameKey := r.newInternalBindKey("population_mapping_row_id_name")
	r.bindVars[memberNameKey] = ir.PhysicalPopulationMappingMemberField
	r.bindVars[rowIDNameKey] = ir.PhysicalPopulationMappingRowIDField
	return []string{
		fmt.Sprintf("FOR %s IN (%s == null ? [] : %s)", memberVariable, members, members),
		fmt.Sprintf("RETURN { [@%s]: %s, [@%s]: %s }", memberNameKey, memberVariable, rowIDNameKey, rowID),
	}, nil
}
