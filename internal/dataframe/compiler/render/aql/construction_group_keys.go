package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type groupKeyRendering struct {
	lines                            []string
	collectKeys                      []string
	sortKeys                         []string
	keyTypeAssertions                []string
	identityParts                    []string
	semanticDisplayVariable          string
	semanticDisplayAggregateVariable string
	codingDisplayVariable            string
	codingDisplayAggregateVariable   string
}

func (r *physicalPlanRenderer) renderConstructionGroupKeys(stage ir.PhysicalConstructionStage, inputRowVariable, semanticMembershipVariable string, lines []string) (groupKeyRendering, error) {
	group := stage.Group
	collectKeys := make([]string, 0, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	keyTypeAssertions := make([]string, 0, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("construction_group_key_column_%d", index))
		r.bindVars[columnBind] = key.InputColumn
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return groupKeyRendering{}, fmt.Errorf("group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKey(fmt.Sprintf("construction_group_key_type_%d", index))
		r.bindVars[typeBind] = typeName
		value := fmt.Sprintf("%s[@%s]", inputRowVariable, columnBind)
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", value))
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		default:
			return groupKeyRendering{}, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
		}
		keyTypeAssertions = append(keyTypeAssertions, fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", key.Variable, key.Variable, typeBind))
		collectKeys = append(collectKeys, fmt.Sprintf("%s = %s", key.Variable, value))
		sortKeys = append(sortKeys, key.Variable+" ASC")
		nameBind := r.newInternalBindKey(fmt.Sprintf("construction_group_identity_key_%d", index))
		for _, column := range stage.OutputColumns {
			if column.Name == key.OutputColumn {
				r.bindVars[nameBind] = column.ID
				break
			}
		}
		identityParts = append(identityParts, "[@"+nameBind+", "+key.Variable+"]")
	}
	semanticDisplayVariable := ""
	semanticDisplayAggregateVariable := ""
	if group.SemanticKey != nil {
		semantic := group.SemanticKey
		collectKeys = append(collectKeys, fmt.Sprintf("%s = [%s.system, %s.version, %s.code]", semantic.TupleVariable, semanticMembershipVariable, semanticMembershipVariable, semanticMembershipVariable))
		sortKeys = append(sortKeys, semantic.TupleVariable+" ASC")
		semanticDisplayVariable = semanticMembershipVariable + ".display"
		semanticDisplayAggregateVariable = r.navigation().newInternalVariable("construction_group_semantic_min_display")
		familyBinding := r.newInternalBindKeyWithValue("construction_group_semantic_binding", semantic.BindingID)
		familyRule := r.newInternalBindKeyWithValue("construction_group_semantic_rule", semantic.RuleVersion)
		familyID := r.newInternalBindKeyWithValue("construction_group_semantic_family", semantic.FamilyID)
		familySystem := r.newInternalBindKeyWithValue("construction_group_semantic_system", semantic.System)
		familyVersion := r.newInternalBindKeyWithValue("construction_group_semantic_identity_version", semantic.Version)
		familyPath := r.newInternalBindKeyWithValue("construction_group_semantic_path", semantic.CodingPath)
		semanticOutputID := ""
		for _, column := range stage.OutputColumns {
			if column.Name == semantic.OutputColumn {
				semanticOutputID = column.ID
				break
			}
		}
		if semanticOutputID == "" {
			return groupKeyRendering{}, fmt.Errorf("semantic Group output %q has no stable column ID", semantic.OutputColumn)
		}
		semanticOutputBind := r.newInternalBindKeyWithValue("construction_group_semantic_output_column", semanticOutputID)
		identityParts = append(identityParts,
			"[@"+semanticOutputBind+", \"semantic_family\", @"+familyBinding+", @"+familyRule+", @"+familyID+", @"+familySystem+", @"+familyVersion+", @"+familyPath+", "+semantic.TupleVariable+"]")
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, fmt.Sprintf("  FILTER %s.code != null", semanticMembershipVariable))
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s.code != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", semanticMembershipVariable))
		default:
			return groupKeyRendering{}, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
		}
	}
	codingDisplayVariable := ""
	codingDisplayAggregateVariable := ""
	if group.CodingKey != nil {
		coding := group.CodingKey
		if group.SemanticKey != nil || coding.MembershipColumn == "" || coding.InputTupleVariable == "" || coding.TupleVariable == "" || coding.LabelVariable == "" {
			return groupKeyRendering{}, fmt.Errorf("CODING_IDENTITY Group key is incomplete or conflicts with a semantic family key")
		}
		membershipBind := r.newInternalBindKey("construction_group_coding_membership_column")
		r.bindVars[membershipBind] = coding.MembershipColumn
		membership := r.navigation().newInternalVariable("construction_group_coding_membership")
		lines = append(lines, fmt.Sprintf("  LET %s = %s[@%s]", membership, inputRowVariable, membershipBind))
		lines = append(lines, codingIdentityMembershipTypeAssertions(membership)...)
		display := fmt.Sprintf("(TRIM(%s.display) != \"\" ? %s.display : null)", membership, membership)
		codingDisplayVariable = r.navigation().newInternalVariable("construction_group_coding_display")
		codingDisplayAggregateVariable = r.navigation().newInternalVariable("construction_group_coding_min_display")
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", coding.InputTupleVariable, codingIdentityTupleExpression(membership)),
			fmt.Sprintf("  LET %s = %s", codingDisplayVariable, display),
		)
		missingCodeFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, coding.InputTupleVariable)
		if err != nil {
			return groupKeyRendering{}, err
		}
		if missingCodeFilter != "" {
			lines = append(lines, "  "+missingCodeFilter)
		}
		collectKeys = append(collectKeys, fmt.Sprintf("%s = %s", coding.TupleVariable, coding.InputTupleVariable))
		sortKeys = append(sortKeys, coding.TupleVariable+" ASC")
		outputID := ""
		for _, column := range stage.OutputColumns {
			if column.Name == coding.OutputColumn {
				outputID = column.ID
				break
			}
		}
		if outputID == "" {
			return groupKeyRendering{}, fmt.Errorf("CODING_IDENTITY Group output %q has no stable column ID", coding.OutputColumn)
		}
		outputBind := r.newInternalBindKeyWithValue("construction_group_coding_output_column", outputID)
		identityParts = append(identityParts, "[@"+outputBind+", \"coding_identity\", "+coding.TupleVariable+"]")
	}
	return groupKeyRendering{
		lines: lines, collectKeys: collectKeys, sortKeys: sortKeys,
		keyTypeAssertions: keyTypeAssertions, identityParts: identityParts,
		semanticDisplayVariable: semanticDisplayVariable, semanticDisplayAggregateVariable: semanticDisplayAggregateVariable,
		codingDisplayVariable: codingDisplayVariable, codingDisplayAggregateVariable: codingDisplayAggregateVariable,
	}, nil
}
