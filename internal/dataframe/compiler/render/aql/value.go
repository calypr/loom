package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionRowValueLets(
	rowValues []ir.PhysicalStageRowValue,
	contributors, valueField string,
) ([]string, error) {
	lines := make([]string, 0, len(rowValues)*3)
	for index, rowValue := range rowValues {
		columnBind := r.newInternalBindKeyWithValue("construction_row_value_column", rowValue.InputColumn)
		typeName, err := aqlTableScalarType(rowValue.InputKind)
		if rowValue.InputKind == "OBJECT" {
			typeName, err = "object", nil
		}
		if err != nil {
			return nil, fmt.Errorf("row value %q input type: %w", rowValue.Output, err)
		}
		typeBind := r.newInternalBindKeyWithValue("construction_row_value_type", typeName)
		member := r.newInternalVariable(fmt.Sprintf("construction_row_value_member_%d", index))
		source := member
		if valueField != "" {
			source += "." + valueField
		}
		source += "[@" + columnBind + "]"
		values := r.newInternalVariable(fmt.Sprintf("construction_row_value_values_%d", index))
		if rowValue.InputMany {
			lines = append(lines, fmt.Sprintf(
				"  LET %s = FLATTEN((FOR %s IN %s LET row_value = %s FILTER row_value != null RETURN ASSERT(IS_ARRAY(row_value), \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") ? row_value : []), 1)",
				values, member, contributors, source,
			))
		} else {
			lines = append(lines, fmt.Sprintf(
				"  LET %s = (FOR %s IN %s LET row_value = %s FILTER row_value != null RETURN row_value)",
				values, member, contributors, source,
			))
		}
		item := r.newInternalVariable(fmt.Sprintf("construction_row_value_item_%d", index))
		unique := r.newInternalVariable(fmt.Sprintf("construction_row_value_unique_%d", index))
		lines = append(lines, fmt.Sprintf(
			"  LET %s = SORTED_UNIQUE((FOR %s IN %s FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") RETURN %s))",
			unique, item, values, item, item, typeBind, item,
		))
		if rowValue.Policy == "ALL" {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", rowValue.Variable, unique))
			continue
		}
		if rowValue.Policy != "ONE" {
			return nil, fmt.Errorf("row value %q has unsupported policy %q", rowValue.Output, rowValue.Policy)
		}
		lines = append(lines, fmt.Sprintf(
			"  LET %s = (ASSERT(LENGTH(%s) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\") ? (LENGTH(%s) == 0 ? null : FIRST(%s)) : null)",
			rowValue.Variable, unique, unique, unique,
		))
	}
	return lines, nil
}

func (r *physicalPlanRenderer) renderConstructionGroupRowValueAggregate(
	rowValue ir.PhysicalStageRowValue,
	inputRow string,
	index int,
) (string, string, string, error) {
	columnBind := r.newInternalBindKeyWithValue("construction_row_value_column", rowValue.InputColumn)
	typeName, err := aqlTableScalarType(rowValue.InputKind)
	if rowValue.InputKind == "OBJECT" {
		typeName, err = "object", nil
	}
	if err != nil {
		return "", "", "", fmt.Errorf("input type: %w", err)
	}
	typeBind := r.newInternalBindKeyWithValue("construction_row_value_type", typeName)
	value := fmt.Sprintf("%s[@%s]", inputRow, columnBind)
	values := "[]"
	if rowValue.InputMany {
		values = fmt.Sprintf("(%s == null ? [] : (ASSERT(IS_ARRAY(%s), \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") ? %s : []))", value, value, value)
	} else {
		values = fmt.Sprintf("(%s == null ? [] : [%s])", value, value)
	}
	aggregateVariable := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_arrays_%d", index))
	item := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_item_%d", index))
	unique := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_unique_%d", index))
	uniqueLine := fmt.Sprintf(
		"  LET %s = SORTED_UNIQUE((FOR %s IN FLATTEN(%s, 1) FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") RETURN %s))",
		unique, item, aggregateVariable, item, item, typeBind, item,
	)
	var outputLine string
	switch rowValue.Policy {
	case "ALL":
		outputLine = fmt.Sprintf("  LET %s = %s", rowValue.Variable, unique)
	case "ONE":
		outputLine = fmt.Sprintf(
			"  LET %s = (ASSERT(LENGTH(%s) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\") ? (LENGTH(%s) == 0 ? null : FIRST(%s)) : null)",
			rowValue.Variable, unique, unique, unique,
		)
	default:
		return "", "", "", fmt.Errorf("unsupported policy %q", rowValue.Policy)
	}
	return aggregateVariable, values, uniqueLine + "\n" + outputLine, nil
}

func (r *physicalPlanRenderer) renderExpression(expression ir.PhysicalExpression) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return r.renderValue(*expression.Value)
	case ir.PhysicalLiteralExpression:
		if expression.Literal == nil {
			return "", fmt.Errorf("LITERAL expression is missing payload")
		}
		return r.renderLiteral(*expression.Literal)
	case ir.PhysicalExtractExpression:
		return r.renderExtract(expression)
	case ir.PhysicalAggregateExpression:
		return r.renderAggregate(expression)
	case ir.PhysicalPivotExpression:
		return r.renderPivot(expression)
	case ir.PhysicalOwnerRecordsExpression:
		return r.renderOwnerRecords(expression)
	case ir.PhysicalSliceExpression:
		return r.renderSlice(expression)
	case ir.PhysicalObjectLookupExpression:
		if expression.ObjectLookup == nil {
			return "", fmt.Errorf("OBJECT_LOOKUP expression is missing payload")
		}
		if _, ok := r.bindVars[expression.ObjectLookup.KeyBindKey]; !ok {
			return "", fmt.Errorf("object lookup bind %q is not defined", expression.ObjectLookup.KeyBindKey)
		}
		lookup := fmt.Sprintf("%s[@%s]", expression.ObjectLookup.ObjectVariable, expression.ObjectLookup.KeyBindKey)
		if expression.NullBehavior == ir.PhysicalEmptyOnNull {
			return fmt.Sprintf("(HAS(%s, @%s) ? %s : [])", expression.ObjectLookup.ObjectVariable, expression.ObjectLookup.KeyBindKey, lookup), nil
		}
		return lookup, nil
	case ir.PhysicalKeyedMapExpression:
		return r.renderKeyedMap(expression)
	case ir.PhysicalObjectKeysExpression:
		if expression.ObjectKeys == nil {
			return "", fmt.Errorf("OBJECT_KEYS expression is missing payload")
		}
		return fmt.Sprintf("SORTED_UNIQUE(ATTRIBUTES(%s, true))", expression.ObjectKeys.ObjectVariable), nil
	case ir.PhysicalKeySetExpression:
		return r.renderKeySet(expression)
	case ir.PhysicalObjectExpression:
		return r.renderObject(expression)
	case ir.PhysicalSubplanExpression:
		if expression.Subplan == nil {
			return "", fmt.Errorf("SUBPLAN expression is missing payload")
		}
		return r.renderSubplan(*expression.Subplan, "  ", false)
	case ir.PhysicalCallExpression:
		return r.renderCall(expression)
	case ir.PhysicalRelatedFieldExpression:
		return r.renderRelatedField(expression)
	default:
		return "", fmt.Errorf("physical renderer does not yet support expression kind %q", expression.Kind)
	}
}

func (r *physicalPlanRenderer) renderRelatedField(expression ir.PhysicalExpression) (string, error) {
	if expression.RelatedField == nil || expression.RelatedField.ResourceType == "" || len(expression.RelatedField.Path) == 0 {
		return "", fmt.Errorf("RELATED_FIELD expression is missing its exact field path")
	}
	documentID, err := r.renderValue(expression.RelatedField.DocumentID)
	if err != nil {
		return "", fmt.Errorf("related field document ID: %w", err)
	}

	document := "related_field_document"
	collection := "[DOCUMENT(" + documentID + ")]"
	field := document + ".payload"
	for index, segment := range expression.RelatedField.Path {
		if index == 0 && segment == "payload" {
			continue
		}
		if !validRelatedFieldSegment(segment) {
			return "", fmt.Errorf("related field path segment %d is invalid", index)
		}
		pathBind := r.newInternalBindKey("related_field_path")
		r.bindVars[pathBind] = segment
		field += "[@" + pathBind + "]"
	}
	resourceTypeBind := r.newInternalBindKey("related_field_resource_type")
	r.bindVars[resourceTypeBind] = expression.RelatedField.ResourceType
	if expression.RelatedField.PresenceOnly {
		present, err := r.renderPropertyPathPresence(document+".payload", expression.RelatedField.Path[1:])
		if err != nil {
			return "", fmt.Errorf("related field presence: %w", err)
		}

		rows := fmt.Sprintf("(FOR %s IN %s FILTER %s != null AND %s.project == @project AND %s.dataset_generation == @dataset_generation AND %s.resourceType == @%s AND (@auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths) RETURN %s)",
			document, collection, document, document, document, document, resourceTypeBind, document, present)
		return "FIRST(APPEND(" + rows + ", [true]))", nil
	}
	return fmt.Sprintf("FIRST(FOR %s IN %s FILTER %s != null AND %s.project == @project AND %s.dataset_generation == @dataset_generation AND %s.resourceType == @%s AND (@auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths) RETURN %s)",
		document, collection, document, document, document, document, resourceTypeBind, document, field), nil
}

func validRelatedFieldSegment(segment string) bool {
	if segment == "" || (segment[0] < 'a' || segment[0] > 'z') && (segment[0] < 'A' || segment[0] > 'Z') {
		return false
	}
	for index := 1; index < len(segment); index++ {
		character := segment[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func (r *physicalPlanRenderer) renderKeyedMap(expression ir.PhysicalExpression) (string, error) {
	keyed := expression.KeyedMap
	if keyed == nil {
		return "", fmt.Errorf("KEYED_MAP expression is missing payload")
	}
	source, err := r.renderExpression(keyed.Source)
	if err != nil {
		return "", fmt.Errorf("keyed map source: %w", err)
	}
	item := keyed.ItemVariable
	previous := r.preparedItem
	r.preparedItem = item
	key, err := r.renderExpression(keyed.ItemKey)
	if err != nil {
		r.preparedItem = previous
		return "", fmt.Errorf("keyed map key: %w", err)
	}
	valueExpressions := make([]string, 0, 1+len(keyed.ValueFallbacks))
	value, err := r.renderExpression(keyed.ItemValue)
	if err != nil {
		r.preparedItem = previous
		return "", fmt.Errorf("keyed map value: %w", err)
	}
	valueExpressions = append(valueExpressions, value)
	for _, fallback := range keyed.ValueFallbacks {
		fallbackValue, fallbackErr := r.renderExpression(fallback)
		if fallbackErr != nil {
			r.preparedItem = previous
			return "", fmt.Errorf("keyed map fallback: %w", fallbackErr)
		}
		valueExpressions = append(valueExpressions, fallbackValue)
	}
	r.preparedItem = previous
	valueExpression := valueExpressions[0]
	if len(valueExpressions) > 1 {
		valueExpression = "FIRST(FOR __loom_keyed_candidate IN [" + strings.Join(valueExpressions, ", ") + "] FILTER __loom_keyed_candidate != null RETURN __loom_keyed_candidate)"
	}

	sourceLoop := source
	if keyed.FlattenSource {
		sourceLoop = "FLATTEN(" + source + ")"
	}
	values := "FIRST(__loom_keyed_group[*].__loom_keyed_value)"
	switch keyed.Reduction {
	case ir.PhysicalMapFirstSorted:
		values = "FIRST(SORTED_UNIQUE(__loom_keyed_group[*].__loom_keyed_value))"
	case ir.PhysicalMapAll:
		values = "(FOR __loom_keyed_item IN __loom_keyed_group[*].__loom_keyed_value SORT __loom_keyed_item RETURN __loom_keyed_item)"
	case ir.PhysicalMapDistinct:
		values = "SORTED_UNIQUE(__loom_keyed_group[*].__loom_keyed_value)"
	}
	return fmt.Sprintf(`MERGE(
	  FOR %s IN %s
    LET __loom_keyed_key = %s
    LET __loom_keyed_value = %s
    FILTER __loom_keyed_key != null
    FILTER __loom_keyed_value != null
    COLLECT __loom_keyed_group_key = __loom_keyed_key INTO __loom_keyed_group
    LET __loom_keyed_values = %s
    RETURN { [__loom_keyed_group_key]: __loom_keyed_values }
)`, item, sourceLoop, key, valueExpression, values), nil
}

func (r *physicalPlanRenderer) renderKeySet(expression ir.PhysicalExpression) (string, error) {
	keySet := expression.KeySet
	if keySet == nil {
		return "", fmt.Errorf("KEY_SET expression is missing payload")
	}
	source, err := r.renderExpression(keySet.Source)
	if err != nil {
		return "", fmt.Errorf("key set source: %w", err)
	}
	previousPreparedItem := r.preparedItem
	r.preparedItem = keySet.ItemVariable
	key, err := r.renderExpression(keySet.ItemKey)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", fmt.Errorf("key set item key: %w", err)
	}
	return fmt.Sprintf("SORTED_UNIQUE(FLATTEN(FOR %s IN %s RETURN %s))", keySet.ItemVariable, source, key), nil
}

func (r *physicalPlanRenderer) renderLiteral(literal ir.PhysicalLiteral) (string, error) {
	if literal.BindKey == "" {
		return "", fmt.Errorf("literal bind key is required")
	}
	if _, collection := r.collectionKeys[literal.BindKey]; collection {
		return "", fmt.Errorf("literal bind key %q cannot be a collection bind", literal.BindKey)
	}
	if _, ok := r.bindVars[literal.BindKey]; !ok {
		return "", fmt.Errorf("literal bind key %q is not defined", literal.BindKey)
	}
	return "@" + literal.BindKey, nil
}
