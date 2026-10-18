package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type constructionGroupInput struct {
	RowsVariable     string
	SourceRowInScope bool
}

func (r *physicalPlanRenderer) renderConstructionGroupStage(stage ir.PhysicalConstructionStage, input constructionGroupInput) ([]string, error) {
	group := stage.Group
	if group == nil {
		return nil, fmt.Errorf("group stage is missing payload")
	}
	countRowsOnly := len(group.Aggregates) != 0
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" {
			countRowsOnly = false
			break
		}
	}
	if group.RootContributorInputColumn != "" {
		// Root contributor collection needs the exact grouped rows. Keep the
		// streaming COUNT_ROWS shortcut for ordinary Groups, but materialize
		// this demanded-only lineage alongside the existing group collection.
		countRowsOnly = false
	}
	aggregateRowValues := countRowsOnly && (input.RowsVariable != "" || input.SourceRowInScope) && constructionGroupRowValuesCanAggregate(group.RowValues)
	if len(group.RowValues) != 0 && !aggregateRowValues {
		countRowsOnly = false
	}
	if input.SourceRowInScope && (input.RowsVariable != "" ||
		(len(group.Keys) == 0 && group.SemanticKey == nil && group.CodingKey == nil && !(countRowsOnly && aggregateRowValues))) {
		return nil, fmt.Errorf("in-scope source row requires a keyed Group or a keyless COUNT_ROWS Group with row values")
	}
	lines := make([]string, 0, 8+len(group.Keys)*2+len(group.Aggregates)+len(group.RowValues))
	rowSource := input.RowsVariable
	inputRowVariable := stage.InputRowVariable
	var projectedRowsVariable string
	if !countRowsOnly && !aggregateRowValues {
		projected, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.InputProjections})
		if err != nil {
			return nil, fmt.Errorf("input projection: %w", err)
		}
		if input.SourceRowInScope {
			inputRowVariable = r.newInternalVariable("construction_group_projected_input")
			lines = append(lines, fmt.Sprintf("  LET %s = %s", inputRowVariable, projected))
		} else {
			projectedRowsVariable = r.newInternalVariable("construction_group_input_rows")
			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s RETURN %s)", projectedRowsVariable, stage.InputRowVariable, input.RowsVariable, projected))
			rowSource = projectedRowsVariable
		}
		if len(group.Keys) == 0 {
			if !input.SourceRowInScope {
				rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", projectedRowsVariable, projectedRowsVariable)
			}
		}
	}
	if aggregateRowValues && len(group.Keys) == 0 && input.RowsVariable != "" {
		rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", input.RowsVariable, input.RowsVariable)
	}
	if input.RowsVariable != "" {
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, rowSource))
	} else if !input.SourceRowInScope && (!countRowsOnly || len(group.Keys) != 0 || group.SemanticKey != nil || group.CodingKey != nil) {
		return nil, fmt.Errorf("only keyless COUNT_ROWS groups can consume the current source scope")
	}
	semanticMembershipVariable := ""
	if group.SemanticKey != nil {
		semanticLines, membershipsVariable, err := r.renderConstructionGroupSemanticMembership(group.SemanticKey, input, inputRowVariable)
		if err != nil {
			return nil, err
		}
		lines = append(lines, semanticLines...)
		semanticMembershipVariable = r.newInternalVariable("construction_group_semantic_membership")
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", semanticMembershipVariable, membershipsVariable))
	}
	collectKeys := make([]string, 0, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	keyTypeAssertions := make([]string, 0, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("construction_group_key_column_%d", index))
		r.bindVars[columnBind] = key.InputColumn
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return nil, fmt.Errorf("group key %q: %w", key.InputColumn, err)
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
			return nil, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
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
		semanticDisplayAggregateVariable = r.newInternalVariable("construction_group_semantic_min_display")
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
			return nil, fmt.Errorf("semantic Group output %q has no stable column ID", semantic.OutputColumn)
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
			return nil, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
		}
	}
	codingDisplayVariable := ""
	codingDisplayAggregateVariable := ""
	if group.CodingKey != nil {
		coding := group.CodingKey
		if group.SemanticKey != nil || coding.MembershipColumn == "" || coding.InputTupleVariable == "" || coding.TupleVariable == "" || coding.LabelVariable == "" {
			return nil, fmt.Errorf("CODING_IDENTITY Group key is incomplete or conflicts with a semantic family key")
		}
		membershipBind := r.newInternalBindKey("construction_group_coding_membership_column")
		r.bindVars[membershipBind] = coding.MembershipColumn
		membership := r.newInternalVariable("construction_group_coding_membership")
		lines = append(lines, fmt.Sprintf("  LET %s = %s[@%s]", membership, inputRowVariable, membershipBind))
		lines = append(lines, codingIdentityMembershipTypeAssertions(membership)...)
		display := fmt.Sprintf("(TRIM(%s.display) != \"\" ? %s.display : null)", membership, membership)
		codingDisplayVariable = r.newInternalVariable("construction_group_coding_display")
		codingDisplayAggregateVariable = r.newInternalVariable("construction_group_coding_min_display")
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", coding.InputTupleVariable, codingIdentityTupleExpression(membership)),
			fmt.Sprintf("  LET %s = %s", codingDisplayVariable, display),
		)
		missingCodeFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, coding.InputTupleVariable)
		if err != nil {
			return nil, err
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
			return nil, fmt.Errorf("CODING_IDENTITY Group output %q has no stable column ID", coding.OutputColumn)
		}
		outputBind := r.newInternalBindKeyWithValue("construction_group_coding_output_column", outputID)
		identityParts = append(identityParts, "[@"+outputBind+", \"coding_identity\", "+coding.TupleVariable+"]")
	}
	if countRowsOnly && !aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		collect := "COLLECT"
		if len(collectKeys) != 0 {
			collect += " " + strings.Join(collectKeys, ", ")
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  %s AGGREGATE %s = SUM(1), %s = MIN(%s)", collect, countVariable, semanticDisplayAggregateVariable, semanticDisplayVariable))
		} else if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  %s AGGREGATE %s = SUM(1), %s = MIN(%s)", collect, countVariable, codingDisplayAggregateVariable, codingDisplayVariable))
		} else {
			lines = append(lines, fmt.Sprintf("  %s WITH COUNT INTO %s", collect, countVariable))
		}
		lines = append(lines, keyTypeAssertions...)
		if len(sortKeys) != 0 {
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		for _, aggregate := range group.Aggregates {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, countVariable))
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.SemanticKey.LabelVariable, semanticDisplayAggregateVariable, semanticDisplayAggregateVariable, group.SemanticKey.TupleVariable))
		}
		if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.CodingKey.LabelVariable, codingDisplayAggregateVariable, codingDisplayAggregateVariable, group.CodingKey.TupleVariable))
		}
	} else if aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		aggregateParts := []string{fmt.Sprintf("%s = SUM(%s != null ? 1 : 0)", countVariable, inputRowVariable)}
		if group.SemanticKey != nil {
			aggregateParts = append(aggregateParts, fmt.Sprintf("%s = MIN(%s)", semanticDisplayAggregateVariable, semanticDisplayVariable))
		}
		if group.CodingKey != nil {
			aggregateParts = append(aggregateParts, fmt.Sprintf("%s = MIN(%s)", codingDisplayAggregateVariable, codingDisplayVariable))
		}
		rowValueVariables := make([]string, 0, len(group.RowValues))
		for index, rowValue := range group.RowValues {
			aggregateVariable, aggregateExpression, outputLine, err := r.renderConstructionGroupRowValueAggregate(rowValue, inputRowVariable, index)
			if err != nil {
				return nil, fmt.Errorf("row value %q: %w", rowValue.Output, err)
			}
			aggregateParts = append(aggregateParts, aggregateVariable+" = UNIQUE("+aggregateExpression+")")
			rowValueVariables = append(rowValueVariables, outputLine)
		}
		collect := "COLLECT"
		if len(collectKeys) != 0 {
			collect += " " + strings.Join(collectKeys, ", ")
		}
		lines = append(lines, "  "+collect+" AGGREGATE "+strings.Join(aggregateParts, ", "))
		lines = append(lines, keyTypeAssertions...)
		if len(sortKeys) != 0 {
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		for _, aggregate := range group.Aggregates {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, countVariable))
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.SemanticKey.LabelVariable, semanticDisplayAggregateVariable, semanticDisplayAggregateVariable, group.SemanticKey.TupleVariable))
		}
		if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.CodingKey.LabelVariable, codingDisplayAggregateVariable, codingDisplayAggregateVariable, group.CodingKey.TupleVariable))
		}
		lines = append(lines, rowValueVariables...)
	} else {
		groupMembers := fmt.Sprintf("{row: %s, present: %s != null}", inputRowVariable, inputRowVariable)
		if group.SemanticKey != nil {
			groupMembers = fmt.Sprintf("{row: %s, present: %s != null, semanticDisplay: %s}", inputRowVariable, inputRowVariable, semanticDisplayVariable)
		} else if group.CodingKey != nil {
			groupMembers = fmt.Sprintf("{row: %s, present: %s != null, codingDisplay: %s}", inputRowVariable, inputRowVariable, codingDisplayVariable)
		}
		if len(collectKeys) == 0 {
			allRowsVariable := r.newInternalVariable("construction_group_all_rows")
			lines = append(lines, fmt.Sprintf("  COLLECT %s = null INTO %s = %s", allRowsVariable, group.GroupRowsVariable, groupMembers))
		} else {
			lines = append(lines, fmt.Sprintf("  COLLECT %s INTO %s = %s", strings.Join(collectKeys, ", "), group.GroupRowsVariable, groupMembers))
			lines = append(lines, keyTypeAssertions...)
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		if group.SemanticKey != nil {
			displays := r.newInternalVariable("construction_group_semantic_displays")
			member := r.newInternalVariable("construction_group_semantic_display")
			lines = append(lines,
				fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.semanticDisplay != null RETURN %s.semanticDisplay)", displays, member, group.GroupRowsVariable, member, member),
				fmt.Sprintf("  LET %s = (LENGTH(%s) != 0 ? MIN(%s) : %s[2])", group.SemanticKey.LabelVariable, displays, displays, group.SemanticKey.TupleVariable),
			)
		} else if group.CodingKey != nil {
			displays := r.newInternalVariable("construction_group_coding_displays")
			member := r.newInternalVariable("construction_group_coding_display")
			lines = append(lines,
				fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.codingDisplay != null RETURN %s.codingDisplay)", displays, member, group.GroupRowsVariable, member, member),
				fmt.Sprintf("  LET %s = (LENGTH(%s) != 0 ? MIN(%s) : %s[2])", group.CodingKey.LabelVariable, displays, displays, group.CodingKey.TupleVariable),
			)
		}
		for index, aggregate := range group.Aggregates {
			expression, err := r.renderConstructionGroupAggregate(group, aggregate, index)
			if err != nil {
				return nil, fmt.Errorf("aggregate %q: %w", aggregate.Output, err)
			}
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, expression))
		}
		rowValueLines, rowValueErr := r.renderConstructionRowValueLets(group.RowValues, group.GroupRowsVariable, "row")
		if rowValueErr != nil {
			return nil, rowValueErr
		}
		lines = append(lines, rowValueLines...)
		if group.RootContributorInputColumn != "" {
			contributorLines, contributorErr := r.renderRootContributorSet(
				group.RootContributorInputColumn, group.RootContributorInputMany,
				group.GroupRowsVariable, "row", group.RootContributorVariable,
			)
			if contributorErr != nil {
				return nil, contributorErr
			}
			lines = append(lines, contributorLines...)
		}
	}
	identity := "TO_STRING([" + strings.Join(identityParts, ", ") + "])"
	lines = append(lines, fmt.Sprintf("  LET %s = %s", group.IdentityVariable, identity))
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

func codingIdentityMembershipTypeAssertions(membership string) []string {
	lines := make([]string, 0, 3)
	for _, field := range []string{"system", "version", "display"} {
		value := fmt.Sprintf("%s.%s", membership, field)
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == \"string\", \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value))
	}
	return lines
}

func codingIdentityTupleExpression(membership string) string {
	system := fmt.Sprintf("%s.system", membership)
	version := fmt.Sprintf("(TRIM(%s.version) != \"\" ? %s.version : null)", membership, membership)
	code := fmt.Sprintf("(TYPENAME(%s.code) == \"string\" AND TRIM(%s.code) != \"\" ? %s.code : null)", membership, membership, membership)
	return fmt.Sprintf("[%s, %s, %s]", system, version, code)
}

func codingIdentityMissingCodeFilter(policy ir.PhysicalStageGroupMissingKeyPolicy, tupleVariable string) (string, error) {
	switch policy {
	case ir.PhysicalStageGroupMissingKeyGroup:
		return "", nil
	case ir.PhysicalStageGroupMissingKeyExclude:
		return fmt.Sprintf("FILTER %s[2] != null", tupleVariable), nil
	case ir.PhysicalStageGroupMissingKeyError:
		return fmt.Sprintf("FILTER ASSERT(%s[2] != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", tupleVariable), nil
	default:
		return "", fmt.Errorf("unsupported missing-key policy %q", policy)
	}
}

func (r *physicalPlanRenderer) renderConstructionGroupSemanticMembership(
	semantic *ir.PhysicalStageGroupSemanticKey,
	input constructionGroupInput,
	inputRowVariable string,
) ([]string, string, error) {
	if semantic == nil || semantic.RootCollectionBindKey == "" || semantic.SourceIdentityColumn == "" {
		return nil, "", fmt.Errorf("semantic group key has no source identity")
	}
	root := semantic.SourceRootVariable
	lines := make([]string, 0, len(semantic.PathSegments)+8)
	if !input.SourceRowInScope {
		identityColumn := r.newInternalBindKeyWithValue("construction_group_semantic_source_identity", semantic.SourceIdentityColumn)
		root = r.newInternalVariable("construction_group_semantic_root")
		lines = append(lines, fmt.Sprintf("  LET %s = DOCUMENT(@@%s, %s[@%s])", root, semantic.RootCollectionBindKey, inputRowVariable, identityColumn))
	}
	payload := r.newInternalVariable("construction_group_semantic_payload")
	lines = append(lines, fmt.Sprintf("  LET %s = (%s == null ? {} : %s.payload)", payload, root, root))
	rawCodings := r.newInternalVariable("construction_group_semantic_raw_codings")
	rawLines := []string{fmt.Sprintf("  LET %s = (", rawCodings)}
	pathExpression := payload
	for index, segment := range semantic.PathSegments {
		attribute := fmt.Sprintf("%s[%q]", pathExpression, segment.Name)
		if !segment.Repeated {
			pathExpression = attribute
			continue
		}
		item := r.newInternalVariable(fmt.Sprintf("construction_group_semantic_path_%d", index))
		rawLines = append(rawLines, fmt.Sprintf("    FOR %s IN (%s == null ? [] : (IS_ARRAY(%s) ? %s : []))", item, attribute, attribute, attribute))
		pathExpression = item
	}
	rawLines = append(rawLines, "      RETURN "+pathExpression, "  )")
	lines = append(lines, rawLines...)
	matched := r.newInternalVariable("construction_group_semantic_matches")
	codeSystemBind := r.newInternalBindKeyWithValue("construction_group_semantic_system", semantic.System)
	codeVersionBind := r.newInternalBindKeyWithValue("construction_group_semantic_version", semantic.Version)
	coding := r.newInternalVariable("construction_group_semantic_coding")
	codingVersion := fmt.Sprintf("(TYPENAME(%s.version) == \"string\" AND TRIM(%s.version) != \"\" ? %s.version : null)", coding, coding, coding)
	versionPredicate := fmt.Sprintf("%s == @%s", codingVersion, codeVersionBind)
	if semantic.Version == "" {
		versionPredicate = codingVersion + " == null"
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER TYPENAME(%s) == \"object\" FILTER %s.system == @%s FILTER %s RETURN %s)",
			matched, coding, rawCodings, coding, coding, codeSystemBind, versionPredicate, coding),
	)
	local := r.newInternalVariable("construction_group_semantic_row_memberships")
	matchedCoding := r.newInternalVariable("construction_group_semantic_matched_coding")
	code := r.newInternalVariable("construction_group_semantic_code")
	display := r.newInternalVariable("construction_group_semantic_display")
	localCode := fmt.Sprintf("(TYPENAME(%s.code) == \"string\" AND TRIM(%s.code) != \"\" ? %s.code : null)", matchedCoding, matchedCoding, matchedCoding)
	localDisplay := fmt.Sprintf("(TYPENAME(%s.display) == \"string\" AND TRIM(%s.display) != \"\" ? %s.display : null)", matchedCoding, matchedCoding, matchedCoding)
	matchedVersion := "null"
	if semantic.Version != "" {
		matchedVersion = "@" + codeVersionBind
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = (LENGTH(%s) == 0 ? [{system: null, version: null, code: null, display: null}] : (FOR %s IN %s COLLECT %s = %s AGGREGATE %s = MIN(%s) RETURN {system: (%s != null ? @%s : null), version: (%s != null ? %s : null), code: %s, display: %s}))",
			local, matched, matchedCoding, matched, code, localCode, display, localDisplay, code, codeSystemBind, code, matchedVersion, code, display),
	)
	// Local aggregation collapses duplicate Coding tuples within this one
	// physical input row. The outer Group still sees every physical row.
	return lines, local, nil
}

func (r *physicalPlanRenderer) renderRootContributorSet(
	inputColumn string,
	inputMany bool,
	rowsVariable string,
	rowField string,
	outputVariable string,
) ([]string, error) {
	if inputColumn == "" || rowsVariable == "" || outputVariable == "" {
		return nil, fmt.Errorf("root contributor set requires input, rows, and output")
	}
	columnBind := r.newInternalBindKey("root_contributor_column")
	r.bindVars[columnBind] = inputColumn
	rowVariable := r.newInternalVariable("root_contributor_row")
	value := fmt.Sprintf("%s.%s[@%s]", rowVariable, rowField, columnBind)
	if !inputMany {
		value = "[" + value + "]"
	}
	return []string{fmt.Sprintf(
		"  LET %s = SORTED_UNIQUE(FLATTEN((FOR %s IN %s FILTER %s.present RETURN %s), 1))",
		outputVariable, rowVariable, rowsVariable, rowVariable, value,
	)}, nil
}

func constructionGroupRowValuesCanAggregate(rowValues []ir.PhysicalStageRowValue) bool {
	if len(rowValues) == 0 {
		return false
	}
	for _, rowValue := range rowValues {
		if rowValue.Policy != "ALL" && rowValue.Policy != "ONE" {
			return false
		}
	}
	return true
}

func (r *physicalPlanRenderer) renderConstructionGroupAggregate(group *ir.PhysicalStageGroup, aggregate ir.PhysicalStageGroupAggregate, index int) (string, error) {
	if aggregate.Operation == "COUNT_ROWS" {
		member := r.newInternalVariable(fmt.Sprintf("construction_group_count_rows_%d", index))
		return fmt.Sprintf("LENGTH((FOR %s IN %s FILTER %s.present RETURN 1))", member, group.GroupRowsVariable, member), nil
	}
	columnBind := r.newInternalBindKey(fmt.Sprintf("construction_group_aggregate_column_%d", index))
	r.bindVars[columnBind] = aggregate.InputColumn
	typeName, err := aqlTableScalarType(aggregate.InputKind)
	if err != nil {
		return "", err
	}
	typeBind := r.newInternalBindKey(fmt.Sprintf("construction_group_aggregate_type_%d", index))
	r.bindVars[typeBind] = typeName
	member := r.newInternalVariable(fmt.Sprintf("construction_group_member_%d", index))
	value := fmt.Sprintf("%s.row[@%s]", member, columnBind)
	valuesVariable := r.newInternalVariable(fmt.Sprintf("construction_group_values_%d", index))
	values := fmt.Sprintf("(FOR %s IN %s FILTER %s.present FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH\") RETURN %s)",
		member, group.GroupRowsVariable, member, value, value, typeBind, value)
	if aggregate.Operation == "COUNT_NON_NULL" {
		return "LENGTH(" + values + ")", nil
	}
	if aggregate.Operation == "COUNT_DISTINCT" {
		return "LENGTH(SORTED_UNIQUE(" + values + "))", nil
	}
	if aggregate.Operation != "SUM" && aggregate.Operation != "MIN" && aggregate.Operation != "MAX" && aggregate.Operation != "MEAN" {
		return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
	}
	// Materialize each group's typed values once, then apply null behavior:
	// numeric reducers over no non-null values return null rather than zero.
	reducer := aggregate.Operation
	if reducer == "MEAN" {
		reducer = "SUM"
	}
	result := reducer + "(" + valuesVariable + ")"
	if aggregate.Operation == "MEAN" {
		result += " / LENGTH(" + valuesVariable + ")"
	}
	return fmt.Sprintf("FIRST(FOR %s IN [%s] RETURN LENGTH(%s) == 0 ? null : %s)",
		valuesVariable, values, valuesVariable, result), nil
}
