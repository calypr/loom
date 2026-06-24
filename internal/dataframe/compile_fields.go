package dataframe

import (
	"fmt"
	"strings"
)

func (c *compiler) compileTraversal(parentVar string, parentIsArray bool, step TraversalStep, lets *[]string, objectLines *[]string) error {
	labelBind := c.newBind(step.Alias+"_label", step.Label)
	toBind := c.newBind(step.Alias+"_to", step.ToResourceType)
	nodeVar := sanitizeColumnName(step.Alias) + "_nodes"
	var let string
	if parentIsArray {
		let = fmt.Sprintf("  LET %s = UNIQUE(FLATTEN(FOR __parent IN %s FOR __node, __edge IN 1..1 INBOUND __parent fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s FILTER __node.resourceType == @%s RETURN [__node]))", nodeVar, parentVar, labelBind, toBind)
	} else {
		let = fmt.Sprintf("  LET %s = UNIQUE(FOR __node, __edge IN 1..1 INBOUND %s fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s FILTER __node.resourceType == @%s RETURN __node)", nodeVar, parentVar, labelBind, toBind)
	}
	*lets = append(*lets, let)
	for _, field := range step.Fields {
		sel, _ := ParseSelector(field.Select)
		expr, err := c.compileTraversalFieldSelect(nodeVar, field, sel)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + field.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, pivot := range step.Pivots {
		keySel, _ := ParseSelector(pivot.ColumnSelect)
		valueSel, _ := ParseSelector(pivot.ValueSelect)
		colName := sanitizeColumnName(step.Alias + "__" + pivot.Name)
		expr, err := c.compileTraversalPivot(nodeVar, keySel, valueSel, pivot.Columns)
		if err != nil {
			return err
		}
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
		c.pivotFields = append(c.pivotFields, colName)
	}
	for _, agg := range step.Aggregates {
		expr, err := c.compileSetAggregateExpr(nodeVar, agg)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + agg.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, slice := range step.Slices {
		expr, err := c.compileSetSlice(nodeVar, setModeNode, slice)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + slice.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, child := range step.Traversals {
		if err := c.compileTraversal(nodeVar, true, child, lets, objectLines); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) compileRootFieldSelect(payloadVar string, field FieldSelect, sel Selector) (string, error) {
	if len(field.FallbackSelects) > 0 {
		return c.compileFirstNonNullExpr(payloadVar, append([]string{field.Select}, field.FallbackSelects...)), nil
	}
	return c.compileRootField(payloadVar, sel)
}

func (c *compiler) compileRootField(payloadVar string, sel Selector) (string, error) {
	if sel.Filter == nil && selectorHasNoArrays(sel) {
		return compileDirectExpr(payloadVar, sel.Steps), nil
	}
	return "FIRST" + compileSelectorArrayExpr(payloadVar, sel, c), nil
}

func (c *compiler) compileTraversalFieldSelect(nodeVar string, field FieldSelect, sel Selector) (string, error) {
	if len(field.FallbackSelects) > 0 {
		tmp := DerivedField{
			Source:          nodeVar,
			Select:          field.Select,
			FallbackSelects: field.FallbackSelects,
		}
		return c.compileUniqueField("", tmp, map[string]setMode{nodeVar: setModeNode})
	}
	return c.compileTraversalField(nodeVar, sel)
}

func (c *compiler) compileTraversalField(nodeVar string, sel Selector) (string, error) {
	return fmt.Sprintf("UNIQUE(FLATTEN(FOR __n IN %s RETURN %s))", nodeVar, compileSelectorArrayExpr("__n.payload", sel, c)), nil
}

func (c *compiler) compileRootPivot(payloadVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	return c.compilePivotMapExpr("FOR __item IN ["+payloadVar+"]", "__item", keySel, valueSel, columns)
}

func (c *compiler) compileTraversalPivot(nodeVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	return c.compilePivotMapExpr("FOR __item IN "+nodeVar, "__item.payload", keySel, valueSel, columns)
}

func selectorHasNoArrays(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func compileDirectExpr(rootVar string, steps []SelectorStep) string {
	cur := rootVar
	for _, step := range steps {
		if step.Index != nil {
			cur = fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
			continue
		}
		cur = fmt.Sprintf("%s.%s", cur, step.Field)
	}
	return cur
}

func compileSelectorArrayExpr(rootVar string, sel Selector, c *compiler) string {
	prefix := sel.Steps
	if len(prefix) == 0 {
		return "[]"
	}
	last := prefix[len(prefix)-1]
	prefix = prefix[:len(prefix)-1]
	lines := []string{fmt.Sprintf("FOR __root IN [%s]", rootVar)}
	cur := "__root"
	tmpCount := 0
	for _, step := range prefix {
		next := fmt.Sprintf("__s%d", tmpCount)
		tmpCount++
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, cur, step.Field, cur, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, cur, step.Field, cur, step.Field, *step.Index))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, cur, step.Field))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		}
		cur = next
	}
	if sel.Filter != nil {
		filterBind := c.newBind("contains", sel.Filter.Needle)
		lines = append(lines, fmt.Sprintf("  FILTER CONTAINS(%s.%s ? %s.%s : \"\", @%s)", cur, sel.Filter.Field, cur, sel.Filter.Field, filterBind))
	}
	finalExpr := extractFinalExpr(cur, last)
	lines = append(lines, fmt.Sprintf("  LET __value = %s", finalExpr))
	lines = append(lines, "  FILTER __value != null")
	lines = append(lines, "  RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )"
}

func (c *compiler) compilePivotMapExpr(itemLoop string, payloadVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	keyExpr := compileSelectorArrayExpr(payloadVar, keySel, c)
	valueExpr := compileSelectorArrayExpr(payloadVar, valueSel, c)
	filterLine := ""
	if len(columns) > 0 {
		colBind := c.newBind("pivot_cols", append([]string(nil), columns...))
		filterLine = fmt.Sprintf("\n          FILTER POSITION(@%s, __key, true)", colBind)
	}
	return fmt.Sprintf(`MERGE(
    FOR __pair IN (
      %s
        LET __keys = UNIQUE(%s)
        LET __values = %s
        FILTER LENGTH(__values) > 0
        FOR __key IN __keys%s
          RETURN { key: __key, values: __values }
    )
      COLLECT __key = __pair.key INTO __group
      LET __flat_values = UNIQUE(FLATTEN(__group[*].__pair.values))
      FILTER LENGTH(__flat_values) > 0
      RETURN { [__key]: FIRST(__flat_values) }
  )`, itemLoop, keyExpr, valueExpr, filterLine), nil
}

func extractFinalExpr(cur string, step SelectorStep) string {
	switch {
	case step.Iterate:
		return fmt.Sprintf("(%s.%s ? %s.%s : [])", cur, step.Field, cur, step.Field)
	case step.Index != nil:
		return fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
	default:
		return fmt.Sprintf("%s.%s", cur, step.Field)
	}
}

func (c *compiler) newBind(prefix string, value any) string {
	name := fmt.Sprintf("__%s_%d", sanitizeColumnName(prefix), c.bindCount)
	c.bindCount++
	c.bindVars[name] = value
	return name
}
