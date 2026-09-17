package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderPopulationMappingReturn(terminal ir.PhysicalPopulationMappingReturn) ([]string, error) {
	members, err := r.renderExpression(terminal.Members)
	if err != nil {
		return nil, fmt.Errorf("mapping members: %w", err)
	}
	memberVariable := r.newInternalVariable("population_mapping_member")
	memberNameKey := r.newInternalBindKey("population_mapping_member_name")
	identityNameKey := r.newInternalBindKey("population_mapping_identity_name")
	r.bindVars[memberNameKey] = ir.PhysicalPopulationMappingMemberField
	identityField := ir.PhysicalPopulationMappingIdentityPartsField
	identityValue := make([]string, 0, len(terminal.IdentityParts))
	if terminal.ExplicitIdentity != nil {
		identityField = ir.PhysicalPopulationMappingExplicitIdentityField
		explicit, err := r.renderExpression(*terminal.ExplicitIdentity)
		if err != nil {
			return nil, fmt.Errorf("mapping explicit identity: %w", err)
		}
		identityValue = append(identityValue, explicit)
	} else {
		for index, part := range terminal.IdentityParts {
			expression, err := r.renderExpression(part.Expression)
			if err != nil {
				return nil, fmt.Errorf("mapping identity part %d (%s): %w", index, part.Name, err)
			}
			identityValue = append(identityValue, expression)
		}
	}
	if len(identityValue) == 0 {
		return nil, fmt.Errorf("mapping identity has no renderable value")
	}
	r.bindVars[identityNameKey] = identityField
	return []string{
		fmt.Sprintf("FOR %s IN (%s == null ? [] : %s)", memberVariable, members, members),
		fmt.Sprintf("RETURN { [@%s]: %s, [@%s]: %s }", memberNameKey, memberVariable, identityNameKey, "["+strings.Join(identityValue, ", ")+"]"),
	}, nil
}
