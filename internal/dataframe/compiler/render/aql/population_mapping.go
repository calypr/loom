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
	identityValue := ""
	if terminal.ExplicitIdentity != nil {
		identityField = ir.PhysicalPopulationMappingExplicitIdentityField
		explicit, err := r.renderExpression(*terminal.ExplicitIdentity)
		if err != nil {
			return nil, fmt.Errorf("mapping explicit identity: %w", err)
		}
		identityValue = explicit
	} else {
		parts := make([]string, 0, len(terminal.IdentityParts))
		for index, part := range terminal.IdentityParts {
			expression, err := r.renderExpression(part.Expression)
			if err != nil {
				return nil, fmt.Errorf("mapping identity part %d (%s): %w", index, part.Name, err)
			}
			parts = append(parts, expression)
		}
		identityValue = "[" + strings.Join(parts, ", ") + "]"
	}
	if identityValue == "" {
		return nil, fmt.Errorf("mapping identity has no renderable value")
	}
	r.bindVars[identityNameKey] = identityField
	iterable := fmt.Sprintf("(%s == null ? [] : %s)", members, members)
	return []string{
		fmt.Sprintf("FOR %s IN %s", memberVariable, iterable),
		fmt.Sprintf("RETURN { [@%s]: %s, [@%s]: %s }", memberNameKey, memberVariable, identityNameKey, identityValue),
	}, nil
}

func (r *physicalPlanRenderer) renderConstructionPopulationMappingReturn(terminal ir.PhysicalStagePopulationMappingReturn, sourceRows, finalRow string) []string {
	memberNameKey := r.newInternalBindKey("population_mapping_member_name")
	identityNameKey := r.newInternalBindKey("population_mapping_identity_name")
	sourceRootKeyName := r.newInternalBindKey("population_mapping_source_root_key")
	sourceMembersName := r.newInternalBindKey("population_mapping_source_members")
	contributorsName := r.newInternalBindKey("population_mapping_final_contributors")
	rowIdentityName := r.newInternalBindKey("population_mapping_final_row_identity")
	r.bindVars[memberNameKey] = ir.PhysicalPopulationMappingMemberField
	r.bindVars[identityNameKey] = ir.PhysicalPopulationMappingIdentityPartsField
	r.bindVars[sourceRootKeyName] = terminal.SourceRootKeyColumn
	r.bindVars[sourceMembersName] = terminal.SourceMemberIDsColumn
	r.bindVars[contributorsName] = terminal.FinalRootContributorColumn
	r.bindVars[rowIdentityName] = terminal.RowIdentityColumn

	contributorValues := fmt.Sprintf("%s[@%s]", finalRow, contributorsName)
	if terminal.FinalRootContributorsMany {
		contributorValues = fmt.Sprintf("(%s == null ? [] : %s)", contributorValues, contributorValues)
	} else {
		contributorValues = fmt.Sprintf("(%s == null ? [] : [%s])", contributorValues, contributorValues)
	}
	rootKeys := r.newInternalVariable("population_mapping_root_keys")
	sourceRow := r.newInternalVariable("population_mapping_source_row")
	memberValue := r.newInternalVariable("population_mapping_source_member")
	selectedMember := r.newInternalVariable("population_mapping_selected_member")
	selectedIdentity := r.newInternalVariable("population_mapping_selected_identity")
	return []string{
		fmt.Sprintf("LET %s = %s", rootKeys, contributorValues),
		fmt.Sprintf("FOR %s IN %s", sourceRow, sourceRows),
		fmt.Sprintf("FILTER %s[@%s] IN %s", sourceRow, sourceRootKeyName, rootKeys),
		fmt.Sprintf("FOR %s IN (%s[@%s] == null ? [] : %s[@%s])", memberValue, sourceRow, sourceMembersName, sourceRow, sourceMembersName),
		fmt.Sprintf("COLLECT %s = %s, %s = %s[@%s]", selectedMember, memberValue, selectedIdentity, finalRow, rowIdentityName),
		fmt.Sprintf("SORT %s ASC, %s ASC", selectedIdentity, selectedMember),
		fmt.Sprintf("RETURN { [@%s]: %s, [@%s]: [%s] }", memberNameKey, selectedMember, identityNameKey, selectedIdentity),
	}
}
