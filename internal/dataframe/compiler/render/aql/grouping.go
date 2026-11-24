package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (r *physicalPlanRenderer) renderConstructionGroupStage(stage ir.PhysicalConstructionStage, input constructionGroupInput) ([]string, error) {
	group := stage.Group
	if group == nil {
		return nil, fmt.Errorf("group stage is missing payload")
	}
	if group.RelatedOccurrence != nil {
		if err := validateConstructionGroupRelatedOccurrenceRendererShape(group.RelatedOccurrence); err != nil {
			return nil, err
		}
		if input.SourceRowInScope {
			if err := validateConstructionGroupRelatedRootKeyInput(stage, group.RelatedOccurrence); err != nil {
				return nil, err
			}
		}
	}
	countRowsOnly := len(group.Aggregates) != 0
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" {
			countRowsOnly = false
			break
		}
	}
	if group.RootContributorInputColumn != "" || group.DynamicRuntimeKeysVariable != "" {

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
	if !countRowsOnly && !aggregateRowValues && group.RelatedOccurrence == nil {
		projected, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.InputProjections})
		if err != nil {
			return nil, fmt.Errorf("input projection: %w", err)
		}
		if input.SourceRowInScope {
			inputRowVariable = r.navigation().newInternalVariable("construction_group_projected_input")
			lines = append(lines, fmt.Sprintf("  LET %s = %s", inputRowVariable, projected))
		} else {
			projectedRowsVariable = r.navigation().newInternalVariable("construction_group_input_rows")
			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s RETURN %s)", projectedRowsVariable, stage.InputRowVariable, input.RowsVariable, projected))
			rowSource = projectedRowsVariable
		}
		if len(group.Keys) == 0 {
			if !input.SourceRowInScope {
				rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", projectedRowsVariable, projectedRowsVariable)
			}
		}
	}
	if group.RelatedOccurrence != nil && !countRowsOnly && !aggregateRowValues && input.RowsVariable != "" &&
		!input.SourceRowInScope && len(group.Keys) == 0 && group.SemanticKey == nil && group.CodingKey == nil {
		rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", input.RowsVariable, input.RowsVariable)
	}
	if aggregateRowValues && len(group.Keys) == 0 && input.RowsVariable != "" {
		rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", input.RowsVariable, input.RowsVariable)
	}
	if input.RowsVariable != "" {
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, rowSource))
	} else if !input.SourceRowInScope && (!countRowsOnly || len(group.Keys) != 0 || group.SemanticKey != nil || group.CodingKey != nil) {
		return nil, fmt.Errorf("only keyless COUNT_ROWS groups can consume the current source scope")
	}
	if group.RelatedOccurrence != nil {
		mergedLines, mergedInput, err := r.renderConstructionGroupRelatedOccurrenceInput(
			stage, inputRowVariable, group.RelatedOccurrence,
			!countRowsOnly && !aggregateRowValues,
		)
		if err != nil {
			return nil, err
		}
		lines = append(lines, mergedLines...)
		inputRowVariable = mergedInput
	}
	semanticMembershipVariable := ""
	if group.SemanticKey != nil {
		semanticLines, membershipsVariable, err := r.renderConstructionGroupSemanticMembership(group.SemanticKey, input, inputRowVariable)
		if err != nil {
			return nil, err
		}
		lines = append(lines, semanticLines...)
		semanticMembershipVariable = r.navigation().newInternalVariable("construction_group_semantic_membership")
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", semanticMembershipVariable, membershipsVariable))
	}
	keys, err := r.renderConstructionGroupKeys(stage, inputRowVariable, semanticMembershipVariable, lines)
	if err != nil {
		return nil, err
	}
	lines, err = r.renderConstructionGroupCollection(group, inputRowVariable, keys, countRowsOnly, aggregateRowValues)
	if err != nil {
		return nil, err
	}
	identity := "TO_STRING([" + strings.Join(keys.identityParts, ", ") + "])"
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

func (r *physicalPlanRenderer) renderConstructionGroupRelatedOccurrenceInput(
	stage ir.PhysicalConstructionStage,
	parentRow string,
	adapter *ir.PhysicalStageGroupRelatedOccurrenceAdapter,
	projectBaseRow bool,
) ([]string, string, error) {
	if parentRow == "" {
		return nil, "", fmt.Errorf("related Group adapter has no in-scope parent row")
	}
	lines := make([]string, 0, len(adapter.ScalarProjections)+16)
	baseRow := parentRow
	if projectBaseRow {
		relatedColumns := make(map[string]struct{}, len(adapter.ScalarProjections)+2)
		for _, projection := range adapter.ScalarProjections {
			relatedColumns[projection.InputColumn] = struct{}{}
		}
		if adapter.CodingMembershipColumn != "" {
			relatedColumns[adapter.CodingMembershipColumn] = struct{}{}
		}
		relatedColumns[adapter.OccurrenceKeyColumn] = struct{}{}
		baseProjections := make([]ir.PhysicalProjection, 0, len(stage.InputProjections))
		for _, projection := range stage.InputProjections {
			if _, related := relatedColumns[projection.Name]; related {
				continue
			}
			baseProjections = append(baseProjections, projection)
		}
		projected, err := r.renderReturn(ir.PhysicalReturn{Projections: baseProjections})
		if err != nil {
			return nil, "", fmt.Errorf("related Group base input projection: %w", err)
		}
		baseRow = r.navigation().newInternalVariable("construction_group_related_base_row")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", baseRow, projected))
	}

	related := adapter.RelatedRecords
	targetField := "__loom_related_target"
	indexField := "__loom_related_index"
	routeResult := r.navigation().newInternalVariable("construction_group_related_route_result")
	routeRow := r.navigation().newInternalVariable("construction_group_related_route_row")
	relatedStage := ir.PhysicalConstructionStage{
		ID:                stage.ID + "_related_input",
		InputRowVariable:  parentRow,
		OutputRowVariable: routeRow,
		OutputProjections: []ir.PhysicalProjection{
			{Name: targetField, Value: ir.PhysicalValue{Variable: related.ItemVariable}},
			{Name: indexField, Value: ir.PhysicalValue{Variable: related.IndexVariable}},
		},
		RelatedExpand: &related,
	}
	routeLines, err := r.navigation().renderConstructionRelatedExpandStage(relatedStage, "")
	if err != nil {
		return nil, "", fmt.Errorf("render related Group route: %w", err)
	}
	lines = append(lines, fmt.Sprintf("  FOR %s IN (", routeResult))
	lines = append(lines, appendIndented(nil, routeLines)...)
	lines = append(lines, "  )")
	targetVariable := r.navigation().newInternalVariable("construction_group_related_target")
	indexVariable := r.navigation().newInternalVariable("construction_group_related_index")
	lines = append(lines,
		fmt.Sprintf("  LET %s = %s[@%s]", targetVariable, routeResult, r.newInternalBindKeyWithValue("construction_group_related_target_field", targetField)),
		fmt.Sprintf("  LET %s = %s[@%s]", indexVariable, routeResult, r.newInternalBindKeyWithValue("construction_group_related_index_field", indexField)),
		fmt.Sprintf("  LET %s = %s != null", adapter.RouteHasItemVariable, indexVariable),
	)

	itemPresent := ""
	ordinalVariable := ""
	terminalVariable := r.navigation().newInternalVariable("construction_group_related_terminal_id")
	lines = append(lines, fmt.Sprintf("  LET %s = (%s ? %s.terminal_id : null)", terminalVariable, adapter.RouteHasItemVariable, targetVariable))
	if adapter.CodingUnnest != nil {
		coding := *adapter.CodingUnnest
		occurrenceID := adapter.RelatedRecords.TargetResourceType
		if len(adapter.RelatedRecords.Route) != 0 {
			occurrenceID = adapter.RelatedRecords.Route[len(adapter.RelatedRecords.Route)-1].ToOccurrenceID
		}
		coding.Owner = ir.PhysicalUnnestOwner{
			OccurrenceID:  occurrenceID,
			ResourceType:  adapter.RelatedRecords.TargetResourceType,
			RootVariable:  targetVariable,
			OwnerVariable: targetVariable,
		}
		coding.Expression = ir.PhysicalExpression{
			Kind:         ir.PhysicalValueExpression,
			Cardinality:  ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Value:        &ir.PhysicalValue{Variable: targetVariable, Path: []string{adapter.CodingArrayColumn}},
		}

		coding.EmptyPolicy = ir.PhysicalUnnestPreserveParent
		codingLines, unnestErr := r.navigation().renderUnnest(coding, "  ", 0, 0)
		if unnestErr != nil {
			return nil, "", fmt.Errorf("render related Group Coding expansion: %w", unnestErr)
		}
		lines = append(lines, codingLines...)
		itemPresent = r.navigation().newInternalVariable("construction_group_related_coding_present")
		lines = append(lines, fmt.Sprintf("  LET %s = %s AND %s", itemPresent, adapter.RouteHasItemVariable, coding.HasItemVariable))
		ordinalVariable = r.navigation().newInternalVariable("construction_group_related_coding_ordinal")
		lines = append(lines, fmt.Sprintf("  LET %s = (%s ? %s : null)", ordinalVariable, itemPresent, coding.Ordinality))
		switch adapter.CodingEmptyPolicy {
		case ir.PhysicalUnnestPreserveParent:
		case ir.PhysicalUnnestExclude:
			lines = append(lines, fmt.Sprintf("  FILTER !%s OR %s", adapter.RouteHasItemVariable, coding.HasItemVariable))
		case ir.PhysicalUnnestError:
			errorBind := r.newInternalBindKeyWithValue("construction_group_related_coding_occurrence", coding.Owner.OccurrenceID)
			message := fmt.Sprintf("CONCAT(\"%s: row expansion occurrence \", @%s, \" has no items for owner \", %s)", dataframeerrors.CodeConstructionExpansionEmpty, errorBind, terminalVariable)
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(!%s OR %s, %s)", adapter.RouteHasItemVariable, coding.HasItemVariable, message))
		default:
			return nil, "", fmt.Errorf("unsupported related Group Coding empty policy %q", adapter.CodingEmptyPolicy)
		}
	}

	valueExpression := func(variable string, path ...string) ir.PhysicalExpression {
		value := ir.PhysicalValue{Variable: variable, Path: path}
		return ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &value,
		}
	}
	objectExpression := func(fields ...ir.PhysicalExpressionProjection) *ir.PhysicalExpression {
		return &ir.PhysicalExpression{
			Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: fields},
		}
	}
	projections := make([]ir.PhysicalProjection, 0, len(adapter.ScalarProjections)+3)
	for _, projection := range adapter.ScalarProjections {
		projections = append(projections, ir.PhysicalProjection{
			Name:  projection.InputColumn,
			Value: ir.PhysicalValue{Variable: targetVariable, Path: []string{projection.ResultField}},
		})
	}
	if adapter.CodingUnnest != nil {
		coding := adapter.CodingUnnest
		projections = append(projections, ir.PhysicalProjection{
			Name: adapter.CodingMembershipColumn,
			Expression: objectExpression(
				ir.PhysicalExpressionProjection{Name: "system", Expression: valueExpression(coding.OutputVariable, "system")},
				ir.PhysicalExpressionProjection{Name: "version", Expression: valueExpression(coding.OutputVariable, "version")},
				ir.PhysicalExpressionProjection{Name: "code", Expression: valueExpression(coding.OutputVariable, "code")},
				ir.PhysicalExpressionProjection{Name: "display", Expression: valueExpression(coding.OutputVariable, "display")},
			),
		})
	}
	rootID := valueExpression(parentRow, "_key")
	terminalID := valueExpression(terminalVariable)
	var ifItemPresent, ordinalValue ir.PhysicalExpression
	if adapter.CodingUnnest == nil {
		ifItemPresent = r.groupRelatedOccurrenceLiteral(false, "construction_group_related_occurrence_item_present")
		ordinalValue = r.groupRelatedOccurrenceLiteral(nil, "construction_group_related_occurrence_ordinal")
	} else {
		ifItemPresent = r.groupRelatedOccurrenceValue(itemPresent)
		ordinalValue = r.groupRelatedOccurrenceValue(ordinalVariable)
	}
	projections = append(projections, ir.PhysicalProjection{
		Name: adapter.OccurrenceKeyColumn,
		Expression: objectExpression(
			ir.PhysicalExpressionProjection{Name: "root_id", Expression: rootID},
			ir.PhysicalExpressionProjection{Name: "terminal_id", Expression: terminalID},
			ir.PhysicalExpressionProjection{Name: "item_present", Expression: ifItemPresent},
			ir.PhysicalExpressionProjection{Name: "ordinal", Expression: ordinalValue},
		),
	})
	relatedProjection, err := r.renderReturn(ir.PhysicalReturn{Projections: projections})
	if err != nil {
		return nil, "", fmt.Errorf("render related Group occurrence projection: %w", err)
	}
	merged := r.navigation().newInternalVariable("construction_group_related_input")
	lines = append(lines, fmt.Sprintf("  LET %s = (%s == null ? null : MERGE(%s, %s))", merged, parentRow, baseRow, relatedProjection))
	return lines, merged, nil
}

func (r *physicalPlanRenderer) groupRelatedOccurrenceLiteral(value any, base string) ir.PhysicalExpression {
	bind := r.newInternalBindKey(base)
	r.bindVars[bind] = value
	return ir.PhysicalExpression{
		Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: bind},
	}
}

func (r *physicalPlanRenderer) groupRelatedOccurrenceValue(value string) ir.PhysicalExpression {
	physicalValue := ir.PhysicalValue{Variable: value}
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &physicalValue,
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
		root = r.navigation().newInternalVariable("construction_group_semantic_root")
		lines = append(lines, fmt.Sprintf("  LET %s = DOCUMENT(@@%s, %s[@%s])", root, semantic.RootCollectionBindKey, inputRowVariable, identityColumn))
	}
	payload := r.navigation().newInternalVariable("construction_group_semantic_payload")
	lines = append(lines, fmt.Sprintf("  LET %s = (%s == null ? {} : %s.payload)", payload, root, root))
	rawCodings := r.navigation().newInternalVariable("construction_group_semantic_raw_codings")
	rawLines := []string{fmt.Sprintf("  LET %s = (", rawCodings)}
	pathExpression := payload
	for index, segment := range semantic.PathSegments {
		attribute := fmt.Sprintf("%s[%q]", pathExpression, segment.Name)
		if !segment.Repeated {
			pathExpression = attribute
			continue
		}
		item := r.navigation().newInternalVariable(fmt.Sprintf("construction_group_semantic_path_%d", index))
		rawLines = append(rawLines, fmt.Sprintf("    FOR %s IN (%s == null ? [] : (IS_ARRAY(%s) ? %s : []))", item, attribute, attribute, attribute))
		pathExpression = item
	}
	rawLines = append(rawLines, "      RETURN "+pathExpression, "  )")
	lines = append(lines, rawLines...)
	membershipLines, memberships, err := r.renderConstructionGroupSemanticMembershipFromRaw(semantic, rawCodings)
	if err != nil {
		return nil, "", err
	}
	lines = append(lines, membershipLines...)
	return lines, memberships, nil
}

func (r *physicalPlanRenderer) renderConstructionGroupSemanticMembershipFromRaw(
	semantic *ir.PhysicalStageGroupSemanticKey,
	rawCodings string,
) ([]string, string, error) {
	if semantic == nil || rawCodings == "" {
		return nil, "", fmt.Errorf("semantic group raw Coding projection is incomplete")
	}
	lines := make([]string, 0, 8)
	matched := r.navigation().newInternalVariable("construction_group_semantic_matches")
	codeSystemBind := r.newInternalBindKeyWithValue("construction_group_semantic_system", semantic.System)
	codeVersionBind := r.newInternalBindKeyWithValue("construction_group_semantic_version", semantic.Version)
	coding := r.navigation().newInternalVariable("construction_group_semantic_coding")
	codingVersion := fmt.Sprintf("(TYPENAME(%s.version) == \"string\" AND TRIM(%s.version) != \"\" ? %s.version : null)", coding, coding, coding)
	versionPredicate := fmt.Sprintf("%s == @%s", codingVersion, codeVersionBind)
	if semantic.Version == "" {
		versionPredicate = codingVersion + " == null"
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER TYPENAME(%s) == \"object\" FILTER %s.system == @%s FILTER %s RETURN %s)",
			matched, coding, rawCodings, coding, coding, codeSystemBind, versionPredicate, coding),
	)
	local := r.navigation().newInternalVariable("construction_group_semantic_row_memberships")
	matchedCoding := r.navigation().newInternalVariable("construction_group_semantic_matched_coding")
	code := r.navigation().newInternalVariable("construction_group_semantic_code")
	display := r.navigation().newInternalVariable("construction_group_semantic_display")
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
	rowVariable := r.navigation().newInternalVariable("root_contributor_row")
	value := fmt.Sprintf("%s.%s[@%s]", rowVariable, rowField, columnBind)
	if !inputMany {
		value = "[" + value + "]"
	}
	return []string{fmt.Sprintf(
		"  LET %s = SORTED_UNIQUE(FLATTEN((FOR %s IN %s FILTER %s.present RETURN %s), 1))",
		outputVariable, rowVariable, rowsVariable, rowVariable, value,
	)}, nil
}

func (r *physicalPlanRenderer) renderConstructionGroupAggregate(group *ir.PhysicalStageGroup, aggregate ir.PhysicalStageGroupAggregate, index int) (string, error) {
	if aggregate.Operation == "COUNT_ROWS" {
		member := r.navigation().newInternalVariable(fmt.Sprintf("construction_group_count_rows_%d", index))
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
	member := r.navigation().newInternalVariable(fmt.Sprintf("construction_group_member_%d", index))
	value := fmt.Sprintf("%s.row[@%s]", member, columnBind)
	valuesVariable := r.navigation().newInternalVariable(fmt.Sprintf("construction_group_values_%d", index))
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

func (r *physicalPlanRenderer) renderSemanticGroupProjectionRawCodings(root string, semantic *ir.PhysicalStageGroupSemanticKey) (string, error) {
	if root == "" || semantic == nil || len(semantic.PathSegments) == 0 {
		return "", fmt.Errorf("semantic Group Coding projection is incomplete")
	}
	pathExpression := root + ".payload"
	lines := make([]string, 0, len(semantic.PathSegments)+2)
	seenRepeated := false
	for index, segment := range semantic.PathSegments {
		attribute := fmt.Sprintf("%s[%q]", pathExpression, segment.Name)
		if !segment.Repeated {
			pathExpression = attribute
			continue
		}
		if index == len(semantic.PathSegments)-1 && !seenRepeated {
			return fmt.Sprintf("(IS_ARRAY(%s) ? %s : [])", attribute, attribute), nil
		}
		seenRepeated = true
		item := r.navigation().newInternalVariable(fmt.Sprintf("semantic_group_projection_path_%d", index+1))
		lines = append(lines, fmt.Sprintf("FOR %s IN (%s == null ? [] : (IS_ARRAY(%s) ? %s : []))", item, attribute, attribute, attribute))
		pathExpression = item
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("semantic Group Coding path has no repeated segment")
	}
	lines = append(lines, "RETURN "+pathExpression)
	for index := range lines {
		lines[index] = "  " + lines[index]
	}
	return "(\n" + strings.Join(lines, "\n") + "\n)", nil
}

func (r *physicalPlanRenderer) renderSemanticGroupProjectionAssessment(
	sourceQuery string,
	root *ir.PhysicalRootScan,
	semantic *ir.PhysicalStageGroupSemanticKey,
) ([]string, error) {
	rawProjection, err := r.renderSemanticGroupProjectionRawCodings(root.Variable, semantic)
	if err != nil {
		return nil, err
	}
	sample := r.navigation().newInternalVariable("semantic_group_projection_sample")
	projection := r.navigation().newInternalVariable("semantic_group_projection_sample_value")
	distinct := r.navigation().newInternalVariable("semantic_group_projection_distinct")
	distinctProjection := r.navigation().newInternalVariable("semantic_group_projection_distinct_value")
	serialized := r.navigation().newInternalVariable("semantic_group_projection_serialized_lengths")
	serializedCharacters := r.navigation().newInternalVariable("semantic_group_projection_serialized_characters")
	limitBind := r.newInternalBindKey("semantic_group_projection_sample_limit")
	r.bindVars[limitBind] = semanticGroupProjectionSampleLimit
	lines := []string{"LET " + sample + " = ("}
	for _, line := range strings.Split(strings.TrimSuffix(sourceQuery, "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	lines = append(lines,
		"  LET "+projection+" = "+rawProjection,
		"  FILTER LENGTH("+projection+") > 0",
		"  LIMIT @"+limitBind,
		"  RETURN "+projection,
		")",
		"LET "+distinct+" = UNIQUE("+sample+")",
		"LET "+serialized+" = (FOR "+distinctProjection+" IN "+distinct+" RETURN LENGTH(JSON_STRINGIFY("+distinctProjection+")))",
		"LET "+serializedCharacters+" = SUM("+serialized+")",
		"RETURN {sampleRows: LENGTH("+sample+"), distinctProjections: LENGTH("+distinct+"), serializedCharacters: "+serializedCharacters+"}",
	)
	return lines, nil
}

func (r *physicalPlanRenderer) renderSemanticGroupProjectionFactorization(
	sourceQuery string,
	root *ir.PhysicalRootScan,
	stage *ir.PhysicalConstructionStage,
	semantic *ir.PhysicalStageGroupSemanticKey,
	previewLimitBindKey string,
) ([]string, error) {
	group := stage.Group
	rawProjection, err := r.renderSemanticGroupProjectionRawCodings(root.Variable, semantic)
	if err != nil {
		return nil, err
	}
	raw := r.navigation().newInternalVariable("semantic_group_projection_raw")
	projection := r.navigation().newInternalVariable("semantic_group_projection_distinct_raw")
	weight := r.navigation().newInternalVariable("semantic_group_projection_weight")
	membership := r.navigation().newInternalVariable("semantic_group_projection_membership")
	count := r.navigation().newInternalVariable("semantic_group_projection_count_rows")
	displayMin := r.navigation().newInternalVariable("semantic_group_projection_min_display")
	lines := strings.Split(strings.TrimSuffix(sourceQuery, "\n"), "\n")
	lines = append(lines, "LET "+raw+" = "+rawProjection)
	lines = append(lines, "COLLECT "+projection+" = "+raw+" WITH COUNT INTO "+weight)
	semanticLines, localMemberships, err := r.renderConstructionGroupSemanticMembershipFromRaw(semantic, projection)
	if err != nil {
		return nil, fmt.Errorf("render semantic Group extractor: %w", err)
	}
	for _, line := range semanticLines {
		lines = append(lines, strings.TrimPrefix(line, "  "))
	}
	lines = append(lines, "FOR "+membership+" IN "+localMemberships)
	switch group.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup:
	case ir.PhysicalStageGroupMissingKeyExclude:
		lines = append(lines, "FILTER "+membership+".code != null")
	case ir.PhysicalStageGroupMissingKeyError:
		lines = append(lines, "FILTER ASSERT("+membership+".code != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")")
	default:
		return nil, fmt.Errorf("unsupported semantic Group missing-key policy %q", group.MissingKeyPolicy)
	}
	lines = append(lines, fmt.Sprintf("COLLECT %s = [%s.system, %s.version, %s.code] AGGREGATE %s = SUM(%s), %s = MIN(%s.display)",
		semantic.TupleVariable, membership, membership, membership, count, weight, displayMin, membership))
	lines = append(lines, "SORT "+semantic.TupleVariable+" ASC")
	if previewLimitBindKey != "" {
		lines = append(lines, "LIMIT @"+previewLimitBindKey)
	}
	lines = append(lines,
		fmt.Sprintf("LET %s = (%s != null ? %s : %s[2])", semantic.LabelVariable, displayMin, displayMin, semantic.TupleVariable),
		fmt.Sprintf("LET %s = %s", group.Aggregates[0].Variable, count),
	)
	constructionBind := group.ConstructionIDBindKey
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
	semanticOutputBind := r.newInternalBindKeyWithValue("semantic_group_projection_output_column", semanticOutputID)
	familyBinding := r.newInternalBindKeyWithValue("semantic_group_projection_binding", semantic.BindingID)
	familyRule := r.newInternalBindKeyWithValue("semantic_group_projection_rule", semantic.RuleVersion)
	familyID := r.newInternalBindKeyWithValue("semantic_group_projection_family", semantic.FamilyID)
	familySystem := r.newInternalBindKeyWithValue("semantic_group_projection_system", semantic.System)
	familyVersion := r.newInternalBindKeyWithValue("semantic_group_projection_version", semantic.Version)
	familyPath := r.newInternalBindKeyWithValue("semantic_group_projection_path", semantic.CodingPath)
	identityParts := []string{
		"[\"construction\", @" + constructionBind + "]",
		"[\"operation\", \"GROUP\"]",
		"[@" + semanticOutputBind + ", \"semantic_family\", @" + familyBinding + ", @" + familyRule + ", @" + familyID +
			", @" + familySystem + ", @" + familyVersion + ", @" + familyPath + ", " + semantic.TupleVariable + "]",
	}
	identity := "TO_STRING([" + strings.Join(identityParts, ", ") + "])"
	lines = append(lines, fmt.Sprintf("LET %s = %s", group.IdentityVariable, identity))
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("render semantic Group output row: %w", err)
	}
	lines = append(lines, fmt.Sprintf("LET %s = %s", stage.OutputRowVariable, output), "RETURN "+stage.OutputRowVariable)
	return lines, nil
}
