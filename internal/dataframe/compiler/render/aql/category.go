package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const categoryColumnBind = "__loom_category_column"

const categoryLimitBind = "__loom_category_limit"

type RenderedCategoryScan struct {
	RenderedPhysicalPlan
	OverflowWitness *RenderedPhysicalPlan
}

type categoryScanRenderer struct {
	terminal ir.PhysicalCategoryScanReturn
}

func RenderCategoryScan(plan ir.PhysicalPlan, terminal ir.PhysicalCategoryScanReturn) (RenderedCategoryScan, error) {
	return categoryScanRenderer{terminal: terminal}.render(plan)
}

func (renderer categoryScanRenderer) render(plan ir.PhysicalPlan) (RenderedCategoryScan, error) {
	terminal := renderer.terminal
	if err := plan.Validate(); err != nil {
		return RenderedCategoryScan{}, err
	}
	if err := terminal.Validate(); err != nil {
		return RenderedCategoryScan{}, err
	}
	if plan.StageSequence != nil && terminal.PresenceCompanionColumn == "" {
		found := false
		for _, column := range plan.StageSequence.FinalColumns {
			if column.Name == terminal.CategoryColumn && !column.Internal {
				found = true
				break
			}
		}
		if !found {
			return RenderedCategoryScan{}, fmt.Errorf("terminal projection column %q is not public in the final stage", terminal.CategoryColumn)
		}
	}
	witnessPlan := plan
	plan = ir.ClonePhysicalPlan(plan)
	if terminal.RootIndexHint != "" {
		found := false
		for i := range plan.Operations {
			if root := plan.Operations[i].RootScan; root != nil {
				if root.Population != nil || root.CohortSource != nil || root.PageCandidateSeed != nil {
					return RenderedCategoryScan{}, fmt.Errorf("category index hint requires a direct root scan")
				}
				root.IndexHint = terminal.RootIndexHint
				found = true
			}
		}
		if !found {
			return RenderedCategoryScan{}, fmt.Errorf("category index requires a root scan")
		}
	}
	if terminal.IndexedRoot != nil {
		return renderer.renderIndexed(plan)
	}
	if terminal.ValueColumn != "" && plan.StageSequence != nil && terminal.PresenceCompanionColumn == "" {
		return renderer.renderValues(plan)
	}
	variables := categoryScanVariables(plan)
	options := renderer.sourceOptions(plan, "")
	if terminal.StreamRoot {
		if plan.StageSequence != nil {
			return RenderedCategoryScan{}, fmt.Errorf("streamed category scan cannot contain construction stages")
		}
		options.terminalReturnVariable = variables["row"]
	}
	source, err := renderPhysicalPlanWithOptions(plan, options)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	binds := source.BindVars
	for _, key := range []string{categoryColumnBind, categoryLimitBind} {
		if _, exists := binds[key]; exists {
			return RenderedCategoryScan{}, fmt.Errorf("category scan bind %q is already defined", key)
		}
	}
	valueBind, presenceBind := "", ""
	if terminal.ValueColumn != "" {
		valueBind = "__loom_category_value_column"
		if _, exists := binds[valueBind]; exists {
			return RenderedCategoryScan{}, fmt.Errorf("category value bind %q is already defined", valueBind)
		}
		binds[valueBind] = terminal.ValueColumn
	}
	if terminal.PresenceCompanionColumn != "" && !terminal.StreamRoot {
		presenceBind = "__loom_category_presence_column"
		if _, exists := binds[presenceBind]; exists {
			return RenderedCategoryScan{}, fmt.Errorf("category presence bind %q is already defined", presenceBind)
		}
		binds[presenceBind] = terminal.PresenceCompanionColumn
	}
	binds[categoryColumnBind] = terminal.CategoryColumn
	binds[categoryLimitBind] = terminal.MaxValues + 1
	query := source.Query
	if !terminal.StreamRoot {
		query = "LET " + variables["rows"] + " = (\n" + query + "\n)\nFOR " + variables["row"] + " IN " + variables["rows"] + "\n"
	}
	query += categoryDistinctSuffix(variables, terminal.PresenceMarkerColumn, presenceBind, valueBind)
	result := RenderedCategoryScan{RenderedPhysicalPlan: RenderedPhysicalPlan{Query: query, BindVars: binds}}
	if plan.StageSequence != nil && terminal.ValueColumn == "" {
		witness, eligible, err := renderRelatedEligibilityCategoryOverflowWitness(witnessPlan, terminal.CategoryColumn, terminal.PresenceMarkerColumn, terminal.MaxValues*4+1)
		if err != nil {
			return RenderedCategoryScan{}, err
		}
		if eligible && terminal.PresenceCompanionColumn == "" {
			witness.Query = "LET __loom_witness_rows = (\n" +
				witness.Query +
				"\n)\nFOR __loom_witness_row IN __loom_witness_rows\n" +
				fmt.Sprintf("  LET __loom_witness_present = __loom_witness_row[%q]\n", terminal.PresenceMarkerColumn) +
				"  LET __loom_witness_value = __loom_witness_present ? __loom_witness_row[@" +
				categoryColumnBind +
				"] : null\n  COLLECT present = __loom_witness_present, value = __loom_witness_value\n  LIMIT @" +
				categoryLimitBind +
				"\n  RETURN { present, value }"
			witness.BindVars[categoryColumnBind] = terminal.CategoryColumn
			witness.BindVars[categoryLimitBind] = terminal.MaxValues + 1
			result.OverflowWitness = &witness
		}
	}
	return result, nil
}

func (renderer categoryScanRenderer) sourceOptions(plan ir.PhysicalPlan, namespace string) physicalRenderOptions {
	t := renderer.terminal
	options := physicalRenderOptions{internalPrefix: namespace}
	if t.PresenceCompanionColumn != "" && !t.StreamRoot {
		return options
	}
	options.projectionPresenceMarkerColumn = t.PresenceMarkerColumn
	if plan.StageSequence != nil {
		options.terminalProjectionColumn = t.CategoryColumn
		options.omitTerminalRowSort = true
		for _, column := range plan.StageSequence.FinalColumns {
			if column.Name == t.CategoryColumn && !column.Internal {
				options.preserveProjectionPresenceNames = terminalProjectionPresenceNames(plan.StageSequence, column.ID)
				break
			}
		}
	} else {
		options.preserveProjectionPresenceNames = map[string]struct{}{t.CategoryColumn: {}}
	}
	return options
}

func categoryScanVariables(plan ir.PhysicalPlan) map[string]string {
	used := physicalPlanVariableNames(plan)
	if plan.StageSequence != nil {
		for name := range stageSequenceVariableNames(plan) {
			used[name] = struct{}{}
		}
	}
	result := map[string]string{}
	for _, role := range []string{"row", "rows", "present", "value", "groupPresent", "groupValue", "nonnull", "null", "parent", "value_rows", "value_row", "value_key", "value_keys", "presence_row", "presence_rows"} {
		base := "__loom_category_" + role
		if role == "groupPresent" {
			base = "__loom_category_group_present"
		}
		if role == "groupValue" {
			base = "__loom_category_group_value"
		}
		if role == "presence_rows" {
			base = "__loom_category_value_rows_presence"
		}
		name := base
		for suffix := 1; ; suffix++ {
			if _, exists := used[name]; !exists {
				break
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		used[name] = struct{}{}
		result[role] = name
	}
	return result
}

func categoryDistinctSuffix(v map[string]string, marker, presenceBind, valueBind string) string {
	query := ""
	if valueBind != "" {
		query = "FILTER " + v["row"] + "[@" + valueBind + "] != null\n"
	}
	present := fmt.Sprintf("%s[%q]", v["row"], marker)
	if presenceBind != "" {
		present = v["row"] + "[@" + presenceBind + "] == true"
	}
	return query +
		"LET " +
		v["present"] +
		" = " +
		present +
		"\nLET " +
		v["value"] +
		" = " +
		v["present"] +
		" ? " +
		v["row"] +
		"[@" +
		categoryColumnBind +
		"] : null\nCOLLECT " +
		v["groupPresent"] +
		" = " +
		v["present"] +
		", " +
		v["groupValue"] +
		" = " +
		v["value"] +
		"\nSORT " +
		v["groupPresent"] +
		" ASC, TYPENAME(" +
		v["groupValue"] +
		") ASC, " +
		v["groupValue"] +
		" ASC\nLIMIT @" +
		categoryLimitBind +
		"\nRETURN { present: " +
		v["groupPresent"] +
		", value: " +
		v["groupValue"] +
		" }"
}

func (renderer categoryScanRenderer) renderValues(plan ir.PhysicalPlan) (RenderedCategoryScan, error) {
	t := renderer.terminal
	values, err := renderPhysicalPlanWithOptions(plan, physicalRenderOptions{})
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	presence, err := renderPhysicalPlanWithOptions(plan, renderer.sourceOptions(plan, "category_presence_"))
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	binds := values.BindVars
	if err := mergeRenderedBindVars(binds, presence.BindVars); err != nil {
		return RenderedCategoryScan{}, err
	}
	for _, key := range []string{categoryColumnBind, categoryLimitBind, "__loom_category_value_column"} {
		if _, exists := binds[key]; exists {
			return RenderedCategoryScan{}, fmt.Errorf("category scan bind %q is already defined", key)
		}
	}
	binds[categoryColumnBind] = t.CategoryColumn
	binds[categoryLimitBind] = t.MaxValues + 1
	binds["__loom_category_value_column"] = t.ValueColumn
	v := categoryScanVariables(plan)
	query := "LET " +
		v["value_rows"] +
		" = (\n" +
		values.Query +
		"\n)\nLET " +
		v["value_keys"] +
		" = (\n  FOR " +
		v["value_row"] +
		" IN " +
		v["value_rows"] +
		"\n  FILTER " +
		v["value_row"] +
		"[@__loom_category_value_column] != null\n  COLLECT " +
		v["value_key"] +
		" = " +
		v["value_row"] +
		"[@" +
		categoryColumnBind +
		"]\n  RETURN " +
		v["value_key"] +
		"\n)\nLET " +
		v["presence_rows"] +
		" = (\n" +
		presence.Query +
		"\n)\nFOR " +
		v["row"] +
		" IN " +
		v["presence_rows"] +
		"\nFILTER " +
		v["row"] +
		"[@" +
		categoryColumnBind +
		"] IN " +
		v["value_keys"] +
		"\n" +
		categoryDistinctSuffix(v, t.PresenceMarkerColumn, "", "")
	return RenderedCategoryScan{RenderedPhysicalPlan: RenderedPhysicalPlan{Query: query, BindVars: binds}}, nil
}

func (renderer categoryScanRenderer) renderIndexed(plan ir.PhysicalPlan) (RenderedCategoryScan, error) {
	t := renderer.terminal
	scan := t.IndexedRoot
	maxValues := t.MaxValues
	var root *ir.PhysicalRootScan
	for _, operation := range plan.Operations {
		if operation.RootScan != nil {
			root = operation.RootScan
		}
	}
	if root == nil || plan.StageSequence != nil || root.Variable != scan.RootVariable {
		return RenderedCategoryScan{}, fmt.Errorf("indexed category source is not a direct root")
	}
	var presence *ir.PhysicalProjectionPresence
	for _, operation := range plan.Operations {
		if operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != t.CategoryColumn {
				continue
			}
			value, direct := ir.DirectScalarRootProjectionValue(projection, root.Variable)
			if !direct || presence != nil || projection.Hidden || value.Variable != root.Variable || value.BindKey != "" || !sameCategoryPath(value.Path, scan.ValuePath) || projection.Presence == nil {
				return RenderedCategoryScan{}, fmt.Errorf("indexed category does not match one direct source projection")
			}
			presence = projection.Presence
		}
	}
	if presence == nil || presence.Source.Variable != root.Variable || presence.Source.BindKey != "" || len(presence.Paths) != 1 || !sameCategoryPath(append(append([]string{}, presence.Source.Path...), presence.Paths[0]...), scan.ValuePath) {
		return RenderedCategoryScan{}, fmt.Errorf("indexed category source presence does not match its exact value path")
	}
	if scan.ResourceType != "" && (plan.Source.ResourceType != scan.ResourceType || plan.BindVars[root.CollectionBindKey] != scan.ResourceType) {
		return RenderedCategoryScan{}, fmt.Errorf("category type guard does not match the exact root collection")
	}
	rendered, err := renderPhysicalPlanWithOptions(plan, physicalRenderOptions{omitTerminalReturn: true, rootCategoryResourceType: scan.ResourceType})
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	prefix := rendered.Query
	bindVars := rendered.BindVars
	variables := categoryScanVariables(plan)
	valueExpression := root.Variable + "." + strings.Join(scan.ValuePath, ".")
	if len(scan.StoredParentPath) != 0 {
		return renderStoredParentCategoryQuery(prefix, root, valueExpression, scan.ValuePath, bindVars, maxValues, scan.StoredParentPath, variables)
	}
	presenceExpression, err := categoryScanRootPresenceExpression(*presence, root.Variable, bindVars)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	variables = categoryScanVariables(plan)
	branch := func(filter string, distinct string, result string) string {
		return prefix + "\nFILTER " + filter + "\n" + distinct + "\n" +
			"LIMIT @" + categoryLimitBind + "\n" + "RETURN " + result
	}
	nonNullQuery := branch(valueExpression+" != null", "COLLECT value = "+valueExpression,
		"{ present: true, value }")
	nullQuery := branch(valueExpression+" == null", "COLLECT present = "+presenceExpression+", value = "+valueExpression,
		"{ present, value }")
	query := "LET " + variables["nonnull"] + " = (\n" + nonNullQuery + "\n)\n" +
		"LET " + variables["null"] + " = (\n" + nullQuery + "\n)\n" +
		"FOR " + variables["row"] + " IN UNION_DISTINCT(" + variables["nonnull"] + ", " + variables["null"] + ")\n" +
		"SORT " + variables["row"] + ".present ASC, TYPENAME(" + variables["row"] + ".value) ASC, " + variables["row"] + ".value ASC\n" +
		"LIMIT @" + categoryLimitBind + "\n" +
		"RETURN { present: " + variables["row"] + ".present, value: " + variables["row"] + ".value }"
	if _, exists := bindVars[categoryLimitBind]; exists {
		return RenderedCategoryScan{}, fmt.Errorf("category scan bind %q is already defined", categoryLimitBind)
	}
	bindVars[categoryLimitBind] = maxValues + 1

	witnessQuery := prefix + "\nFILTER " + valueExpression + " == null\nFILTER NOT (" + presenceExpression + ")\nLIMIT 1\nRETURN { present: false, value: null }"
	witnessBinds := cloneCategoryScanBinds(bindVars)
	delete(witnessBinds, categoryLimitBind)
	return RenderedCategoryScan{RenderedPhysicalPlan: RenderedPhysicalPlan{Query: query, BindVars: bindVars}, OverflowWitness: &RenderedPhysicalPlan{Query: witnessQuery, BindVars: witnessBinds}}, nil
}

func renderStoredParentCategoryQuery(prefix string, root *ir.PhysicalRootScan, valueExpression string, valuePath []string, bindVars map[string]any, maxValues int, parentSegments []string, variables map[string]string) (RenderedCategoryScan, error) {
	parentPath := strings.Join(parentSegments, ".")
	if len(valuePath) != len(parentSegments)+1 || !samePathPrefix(valuePath, parentSegments) || valuePath[len(valuePath)-1] != "code" {
		return RenderedCategoryScan{}, fmt.Errorf("stored quantity category index does not match the direct code leaf")
	}
	parentExpression := root.Variable + "." + parentPath
	presenceBind := categoryScanUniqueBindKey(bindVars, "__loom_category_presence_1")
	bindVars[presenceBind] = "code"
	parentVariable := variables["parent"]
	variables["parent"] = parentVariable
	presentVariable := variables["present"]
	variables["present"] = presentVariable
	valueVariable := variables["value"]
	variables["value"] = valueVariable
	groupPresentVariable := variables["groupPresent"]
	variables["groupPresent"] = groupPresentVariable
	groupValueVariable := variables["groupValue"]
	variables["groupValue"] = groupValueVariable
	presenceExpression := "(IS_OBJECT(" + parentVariable + ") ? HAS(" + parentVariable + ", @" + presenceBind + ") : false)"
	query := prefix + "\nLET " + parentVariable + " = " + parentExpression + "\n" +
		"LET " + presentVariable + " = " + presenceExpression + "\n" +
		"LET " + valueVariable + " = " + valueExpression + "\n" +
		"COLLECT " + groupPresentVariable + " = " + presentVariable + ", " + groupValueVariable + " = " + valueVariable + "\n" +
		"SORT " + groupPresentVariable + " ASC, TYPENAME(" + groupValueVariable + ") ASC, " + groupValueVariable + " ASC\n" +
		"LIMIT @" + categoryLimitBind + "\n" +
		"RETURN { present: " + groupPresentVariable + ", value: " + groupValueVariable + " }"
	if _, exists := bindVars[categoryLimitBind]; exists {
		return RenderedCategoryScan{}, fmt.Errorf("category scan bind %q is already defined", categoryLimitBind)
	}
	bindVars[categoryLimitBind] = maxValues + 1
	witnessQuery := prefix + "\nLET " + parentVariable + " = " + parentExpression + "\n" +
		"FILTER " + valueExpression + " == null\nFILTER NOT " + presenceExpression + "\nLIMIT 1\nRETURN { present: false, value: null }"
	witnessBinds := cloneCategoryScanBinds(bindVars)
	delete(witnessBinds, categoryLimitBind)
	return RenderedCategoryScan{RenderedPhysicalPlan: RenderedPhysicalPlan{Query: query, BindVars: bindVars}, OverflowWitness: &RenderedPhysicalPlan{Query: witnessQuery, BindVars: witnessBinds}}, nil
}

func categoryScanRootPresenceExpression(presence ir.PhysicalProjectionPresence, rootVariable string, bindVars map[string]any) (string, error) {
	if presence.Source.Variable != rootVariable || presence.Source.BindKey != "" || len(presence.Paths) != 1 || len(presence.Paths[0]) == 0 {
		return "", fmt.Errorf("category projection presence is not one direct root path")
	}
	source := rootVariable
	if len(presence.Source.Path) > 0 {
		source += "." + strings.Join(presence.Source.Path, ".")
	}
	path := presence.Paths[0]
	for _, segment := range path {
		if segment == "" {
			return "", fmt.Errorf("category projection presence contains an empty path segment")
		}
	}
	var render func(string, int) (string, error)
	render = func(object string, index int) (string, error) {
		key := categoryScanUniqueBindKey(bindVars, fmt.Sprintf("__loom_category_presence_%d", index))
		bindVars[key] = path[index]
		has := fmt.Sprintf("HAS(%s, @%s)", object, key)
		if index == len(path)-1 {
			return fmt.Sprintf("(IS_OBJECT(%s) ? %s : false)", object, has), nil
		}
		child := object + "[@" + key + "]"
		rest, err := render(child, index+1)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(IS_OBJECT(%s) ? (%s ? %s : false) : false)", object, has, rest), nil
	}
	return render(source, 0)
}

func categoryScanUniqueBindKey(bindVars map[string]any, base string) string {
	key := base
	for suffix := 1; ; suffix++ {
		if _, exists := bindVars[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func samePathPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}

func cloneCategoryScanBinds(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = ir.ClonePhysicalBindValue(value)
	}
	return out
}

func sameCategoryPath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
