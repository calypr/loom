package dataframe

import (
	"encoding/json"
	"fmt"
	"strings"
)

type setMode string

const (
	setModeNode   setMode = "node"
	setModeObject setMode = "object"
)

func compileLowered(builder Builder, limit int) (CompiledQuery, error) {
	genericSetRoutes, err := resolveGenericLoweredStorageRoutes(builder)
	if err != nil {
		return CompiledQuery{}, err
	}
	c := &compiler{
		builder: builder,
		bindVars: map[string]any{
			"project":                          builder.Project,
			datasetGenerationBindKey:           datasetGenerationBindValue(builder.DatasetGeneration),
			"auth_resource_paths":              builder.AuthResourcePaths,
			"auth_resource_paths_unrestricted": builderAuthScopeUnrestricted(builder),
		},
		columns:          []string{"_key"},
		pivotFields:      []string{},
		pivotExprs:       map[string]string{},
		genericSetRoutes: genericSetRoutes,
	}
	if limit > 0 {
		c.bindVars["limit"] = limit
	}
	setModes := map[string]setMode{}
	rootVar := "root"
	lets := []string{}
	objectLines := []string{}

	for _, set := range builder.Sets {
		let, mode, err := c.compileNamedSet(rootVar, set, setModes)
		if err != nil {
			return CompiledQuery{}, err
		}
		lets = append(lets, let)
		setModes[set.Name] = mode
	}
	rootFilter, err := c.compileTypedFilters(rootVar+".payload", builder.Filters)
	if err != nil {
		return CompiledQuery{}, err
	}
	requiredMatchFilters, err := c.compileRequiredTraversalMatches(rootVar, builder.RequiredTraversalMatches)
	if err != nil {
		return CompiledQuery{}, err
	}

	for _, field := range builder.Fields {
		sel, err := ParseSelector(field.Select)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("root field %q selector: %w", field.Name, err)
		}
		expr, err := c.compileRootFieldSelect(rootVar+".payload", field, sel)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(field.Name), expr))
		c.columns = append(c.columns, field.Name)
	}
	for _, pivot := range builder.Pivots {
		keySel, err := ParseSelector(pivot.ColumnSelect)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("root pivot %q key selector: %w", pivot.Name, err)
		}
		valueSel, err := ParseSelector(pivot.ValueSelect)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("root pivot %q value selector: %w", pivot.Name, err)
		}
		colName := sanitizeColumnName(pivot.Name)
		expr, err := c.compileRootPivot(rootVar+".payload", keySel, valueSel, pivot.Columns)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
		c.pivotFields = append(c.pivotFields, colName)
	}
	for _, agg := range builder.Aggregates {
		expr, err := c.compileRootAggregateExpr(rootVar+".payload", agg)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(agg.Name), expr))
		c.columns = append(c.columns, agg.Name)
	}
	for _, step := range builder.Traversals {
		if err := c.compileTraversal(rootVar, false, step, &lets, &objectLines); err != nil {
			return CompiledQuery{}, err
		}
	}
	pivotLets, pivotExprs, err := c.compileDerivedPivotMapLets(rootVar, builder.DerivedFields, setModes)
	if err != nil {
		return CompiledQuery{}, err
	}
	lets = append(lets, pivotLets...)
	for key, value := range pivotExprs {
		c.pivotExprs[key] = value
	}
	for _, field := range builder.DerivedFields {
		expr, err := c.compileDerivedField(rootVar, field, setModes)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(field.Name), expr))
		c.columns = append(c.columns, field.Name)
		if strings.ToUpper(strings.TrimSpace(field.Operation)) == DerivedOpPivot {
			c.pivotFields = append(c.pivotFields, field.Name)
		}
	}
	for _, slice := range builder.RepresentativeSlices {
		expr, err := c.compileRepresentativeSlice(slice, setModes)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(slice.Name), expr))
		c.columns = append(c.columns, slice.Name)
	}
	for _, slice := range builder.Slices {
		expr, err := c.compileRootSlice(rootVar, slice)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(slice.Name), expr))
		c.columns = append(c.columns, slice.Name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("FOR %s IN %s\n", rootVar, builder.RootResourceType))
	sb.WriteString(fmt.Sprintf("  FILTER %s.project == @project\n", rootVar))
	sb.WriteString(fmt.Sprintf("  FILTER %s.%s == @%s\n", rootVar, datasetGenerationField, datasetGenerationBindKey))
	sb.WriteString(fmt.Sprintf("  FILTER @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths\n", rootVar))
	if rootFilter != "true" {
		sb.WriteString(fmt.Sprintf("  FILTER %s\n", rootFilter))
	}
	for _, matchFilter := range requiredMatchFilters {
		sb.WriteString(fmt.Sprintf("  FILTER %s\n", matchFilter))
	}
	sb.WriteString(fmt.Sprintf("  SORT %s._key\n", rootVar))
	if limit > 0 {
		sb.WriteString("  LIMIT @limit\n")
	}
	for _, let := range lets {
		sb.WriteString(let)
		sb.WriteByte('\n')
	}
	sb.WriteString("  RETURN {\n")
	sb.WriteString(fmt.Sprintf("    %s: %s._key", quoteKey("_key"), rootVar))
	for _, line := range objectLines {
		sb.WriteString(",\n")
		sb.WriteString(line)
	}
	sb.WriteString("\n  }\n")
	return CompiledQuery{
		Project:           builder.Project,
		DatasetGeneration: normalizeDatasetGeneration(builder.DatasetGeneration),
		RootResourceType:  builder.RootResourceType,
		AuthResourcePaths: append([]string(nil), builder.AuthResourcePaths...),
		PlanMode:          planMode(builder.PlanHint),
		PlanProfile:       planProfile(builder.PlanHint),
		NamedSetCount:     planNamedSetCount(builder.PlanHint),
		FileSummaries:     planFileSummaries(builder.PlanHint),
		StudyLookup:       planStudyLookup(builder.PlanHint),
		OptimizationRules: planAppliedRules(builder.PlanHint),
		RowIdentity:       planRowIdentity(builder.PlanHint),
		Query:             sb.String(),
		BindVars:          c.bindVars,
		Columns:           append([]string(nil), c.columns...),
		PivotFields:       append([]string(nil), c.pivotFields...),
		Limit:             limit,
	}, nil
}

func (c *compiler) compileNamedSet(rootVar string, set NamedSet, modes map[string]setMode) (string, setMode, error) {
	name := sanitizeColumnName(set.Name)
	switch strings.ToUpper(strings.TrimSpace(set.Kind)) {
	case SetKindTraverse:
		source := rootVar
		sourceMode := setModeNode
		if set.Source != "" && set.Source != "root" {
			source = sanitizeColumnName(set.Source)
			sourceMode = modes[set.Source]
			_ = sourceMode
		}
		labelBind := c.newBind(name+"_label", set.Label)
		var toFilter string
		if set.ToResourceType != "" && !set.AllTargetTypes {
			toBind := c.newBind(name+"_to", set.ToResourceType)
			toFilter = fmt.Sprintf(" FILTER __node.resourceType == @%s", toBind)
		}
		filters, err := c.compileTypedFilters("__node.payload", set.Filters)
		if err != nil {
			return "", "", fmt.Errorf("compile filters for set %q: %w", set.Name, err)
		}
		if filters != "true" {
			toFilter += " FILTER " + filters
		}
		edgeTypeFilter := ""
		// A shared sibling prefix intentionally spans several target resource
		// types. Its ToResourceType is only a route-validation anchor, so an
		// edge type predicate here would discard every sibling except that
		// anchor. Typed subsets apply their resourceType filters afterwards.
		if route, generic := c.genericSetRoutes[set.Name]; generic && !set.AllTargetTypes {
			edgeTypeField := route.targetEdgeTypeField()
			if edgeTypeField == "" {
				return "", "", fmt.Errorf("compile generic traversal set %q: %w: route direction %q has no fhir_edge target type field", set.Name, ErrUnsupportedStorageRoute, route.Direction)
			}
			edgeTypeBind := c.newBind(name+"_edge_target_type", set.ToResourceType)
			edgeTypeFilter = fmt.Sprintf(" FILTER __edge.%s == @%s", edgeTypeField, edgeTypeBind)
		}
		expr := c.compileTraverseSetSource(source, set.Source != "" && set.Source != "root", set.Direction, labelBind, edgeTypeFilter, toFilter)
		setExpr := expr
		if set.Unique {
			setExpr = fmt.Sprintf("UNIQUE(%s)", setExpr)
		}
		if set.SortField != "" {
			setExpr = fmt.Sprintf("(FOR __item IN %s SORT __item.%s RETURN __item)", setExpr, set.SortField)
		}
		return fmt.Sprintf("  LET %s = %s", name, setExpr), setModeNode, nil
	case SetKindFilter:
		source := sanitizeColumnName(set.Source)
		lines := []string{fmt.Sprintf("FOR __item IN %s", source)}
		if set.MatchResourceType != "" {
			typeBind := c.newBind(name+"_match", set.MatchResourceType)
			lines = append(lines, fmt.Sprintf("  FILTER __item.resourceType == @%s", typeBind))
		}
		filters, err := c.compileTypedFilters("__item.payload", set.Filters)
		if err != nil {
			return "", "", fmt.Errorf("compile filters for set %q: %w", set.Name, err)
		}
		if filters != "true" {
			lines = append(lines, "  FILTER "+filters)
		}
		lines = append(lines, "  RETURN __item")
		setExpr := "(\n    " + strings.Join(lines, "\n    ") + "\n  )"
		if set.Unique {
			setExpr = fmt.Sprintf("UNIQUE(%s)", setExpr)
		}
		// UNIQUE is allowed to choose its own internal implementation order.
		// Reapply the requested stable order after deduplication rather than
		// assuming the order of the input subquery survives it.
		if set.SortField != "" {
			setExpr = fmt.Sprintf("(FOR __item IN %s SORT __item.%s RETURN __item)", setExpr, set.SortField)
		}
		return fmt.Sprintf("  LET %s = %s", name, setExpr), modes[set.Source], nil
	case SetKindUnion:
		parts := make([]string, 0, len(set.Sources))
		mode := setModeNode
		for i, src := range set.Sources {
			if i == 0 {
				mode = modes[src]
			}
			parts = append(parts, sanitizeColumnName(src))
		}
		return fmt.Sprintf("  LET %s = UNIQUE(FLATTEN([%s]))", name, strings.Join(parts, ", ")), mode, nil
	case SetKindClassifyDocumentReference:
		source := sanitizeColumnName(set.Source)
		return "  LET " + name + " = " + compileDocumentReferenceSummarySet(source), setModeObject, nil
	case SetKindLookupStudy:
		source := sanitizeColumnName(set.Source)
		return "  LET " + name + " = " + compileStudyLookupSet(rootVar, source), setModeObject, nil
	default:
		return "", "", fmt.Errorf("unsupported set kind %q", set.Kind)
	}
}

type pivotMapGroup struct {
	letName      string
	sourceExpr   string
	mode         setMode
	keySelect    string
	valueSelect  string
	columns      []string
	columnSet    map[string]struct{}
	unrestricted bool
}

func (c *compiler) compileDerivedPivotMapLets(rootVar string, fields []DerivedField, modes map[string]setMode) ([]string, map[string]string, error) {
	groups := map[string]*pivotMapGroup{}
	order := make([]string, 0, 8)
	fieldExprs := make(map[string]string)
	groupIndex := 0

	for _, field := range fields {
		if strings.ToUpper(strings.TrimSpace(field.Operation)) != DerivedOpPivot {
			continue
		}
		sourceExpr := ""
		mode := setModeNode
		switch strings.TrimSpace(field.Source) {
		case "", "root":
			sourceExpr = "[" + rootVar + "]"
			mode = setModeNode
		default:
			sourceExpr = sanitizeColumnName(field.Source)
			mode = modes[field.Source]
		}
		groupKey := strings.Join([]string{sourceExpr, string(mode), field.PivotKeySelect, field.PivotValueSelect}, "|")
		group, ok := groups[groupKey]
		if !ok {
			group = &pivotMapGroup{
				letName:     fmt.Sprintf("__pivot_map_%d", groupIndex),
				sourceExpr:  sourceExpr,
				mode:        mode,
				keySelect:   field.PivotKeySelect,
				valueSelect: field.PivotValueSelect,
				columns:     []string{},
				columnSet:   map[string]struct{}{},
			}
			groupIndex++
			groups[groupKey] = group
			order = append(order, groupKey)
		}
		if len(field.PivotColumns) == 0 {
			group.unrestricted = true
		}
		for _, col := range field.PivotColumns {
			if _, ok := group.columnSet[col]; ok {
				continue
			}
			group.columnSet[col] = struct{}{}
			group.columns = append(group.columns, col)
		}
		fieldExprs[field.Name] = c.compilePivotMapProjection(group.letName, field.PivotColumns)
	}

	lets := make([]string, 0, len(order))
	for _, key := range order {
		group := groups[key]
		keySel, err := ParseSelector(group.keySelect)
		if err != nil {
			return nil, nil, err
		}
		valueSel, err := ParseSelector(group.valueSelect)
		if err != nil {
			return nil, nil, err
		}
		payloadVar := setPayloadVar("__item", group.mode)
		keyExpr := compileSelectorArrayExpr(payloadVar, keySel, c)
		valueExpr := compileSelectorArrayExpr(payloadVar, valueSel, c)
		filterLine := ""
		if !group.unrestricted && len(group.columns) > 0 {
			colsBind := c.newBind(group.letName+"_cols", append([]string(nil), group.columns...))
			filterLine = fmt.Sprintf("\n          FILTER POSITION(@%s, __key, true)", colsBind)
		}
		let := fmt.Sprintf(`  LET %s = MERGE(
    FOR __pair IN (
      FOR __item IN %s
        LET __keys = UNIQUE(%s)
        LET __values = %s
        FILTER LENGTH(__values) > 0
        FOR __key IN __keys%s
          RETURN { key: __key, values: __values }
    )
      COLLECT __key = __pair.key INTO __group
		LET __flat_values = SORTED_UNIQUE(FLATTEN(__group[*].__pair.values))
      FILTER LENGTH(__flat_values) > 0
      RETURN { [__key]: FIRST(__flat_values) }
  )`, group.letName, group.sourceExpr, keyExpr, valueExpr, filterLine)
		lets = append(lets, let)
	}
	return lets, fieldExprs, nil
}

func (c *compiler) compilePivotMapProjection(mapVar string, columns []string) string {
	if len(columns) == 0 {
		return mapVar
	}
	colsBind := c.newBind(mapVar+"_projection_cols", append([]string(nil), columns...))
	return fmt.Sprintf(`MERGE(
    FOR __key IN @%s
      FILTER HAS(%s, __key)
      RETURN { [__key]: %s[__key] }
  )`, colsBind, mapVar, mapVar)
}

func (c *compiler) compileTraverseSetSource(source string, sourceIsSet bool, direction string, labelBind string, edgeTypeFilter string, toFilter string) string {
	direction = strings.ToUpper(strings.TrimSpace(direction))
	if direction == "" {
		direction = "INBOUND"
	}
	if sourceIsSet {
		return fmt.Sprintf("(FLATTEN(FOR __parent IN %s FOR __node, __edge IN 1..1 %s __parent fhir_edge FILTER __edge.project == @project FILTER __node.project == @project FILTER __edge.%s == @%s FILTER __node.%s == @%s FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s%s%s RETURN [__node]))", source, direction, datasetGenerationField, datasetGenerationBindKey, datasetGenerationField, datasetGenerationBindKey, labelBind, edgeTypeFilter, toFilter)
	}
	return fmt.Sprintf("(FOR __node, __edge IN 1..1 %s %s fhir_edge FILTER __edge.project == @project FILTER __node.project == @project FILTER __edge.%s == @%s FILTER __node.%s == @%s FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s%s%s RETURN __node)", direction, source, datasetGenerationField, datasetGenerationBindKey, datasetGenerationField, datasetGenerationBindKey, labelBind, edgeTypeFilter, toFilter)
}

func (c *compiler) compileDerivedField(rootVar string, field DerivedField, modes map[string]setMode) (string, error) {
	if strings.ToUpper(strings.TrimSpace(field.Operation)) == DerivedOpPivot {
		if expr, ok := c.pivotExprs[field.Name]; ok {
			return expr, nil
		}
	}
	switch strings.ToUpper(strings.TrimSpace(field.Operation)) {
	case DerivedOpConst:
		return literalExpr(field.ConstValue), nil
	case DerivedOpRootField:
		sel, err := ParseSelector(field.Select)
		if err != nil {
			return "", err
		}
		if sel.Filter == nil && selectorHasNoArrays(sel) {
			return compileDirectExpr(rootVar, sel.Steps), nil
		}
		return "FIRST" + compileSelectorArrayExpr(rootVar, sel, c), nil
	case DerivedOpCount:
		return fmt.Sprintf("LENGTH(%s)", sanitizeColumnName(field.Source)), nil
	case DerivedOpCountDistinct:
		uniqueExpr, err := c.compileUniqueField(rootVar, field, modes)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("LENGTH(%s)", uniqueExpr), nil
	case DerivedOpMin, DerivedOpMax:
		values, err := c.compileAllField(rootVar, field, modes)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", strings.ToUpper(strings.TrimSpace(field.Operation)), values), nil
	case DerivedOpCountWhere:
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s RETURN 1)", sanitizeColumnName(field.Source), c.compilePredicateExpr("__item", modes[field.Source], field.Predicate, field.PredicatePath, field.PredicateEquals)), nil
	case DerivedOpAny:
		if strings.TrimSpace(field.Predicate) == "" && strings.TrimSpace(field.PredicatePath) == "" {
			return fmt.Sprintf("LENGTH(%s) > 0", sanitizeColumnName(field.Source)), nil
		}
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s LIMIT 1 RETURN 1) > 0", sanitizeColumnName(field.Source), c.compilePredicateExpr("__item", modes[field.Source], field.Predicate, field.PredicatePath, field.PredicateEquals)), nil
	case DerivedOpFirstNonNull:
		return c.compileFirstNonNullField(rootVar, field, modes)
	case DerivedOpAll:
		return c.compileAllField(rootVar, field, modes)
	case DerivedOpUnique:
		return c.compileUniqueField(rootVar, field, modes)
	case DerivedOpPivot:
		return c.compilePivotField(field, modes)
	default:
		return "", fmt.Errorf("unsupported derived field operation %q", field.Operation)
	}
}

func (c *compiler) compileAllField(rootVar string, field DerivedField, modes map[string]setMode) (string, error) {
	selects := append([]string{field.Select}, field.FallbackSelects...)
	if field.Source == "root" || field.Source == "" {
		return c.compileSelectorArrayForSelects(rootVar+".payload", selects), nil
	}
	setVar := sanitizeColumnName(field.Source)
	mode := modes[field.Source]
	return fmt.Sprintf(`FLATTEN(
    FOR __item IN %s
      RETURN %s
  )`, setVar, c.compileSelectorArrayForSelects(setPayloadVar("__item", mode), selects)), nil
}

func (c *compiler) compileFirstNonNullField(rootVar string, field DerivedField, modes map[string]setMode) (string, error) {
	selects := append([]string{field.Select}, field.FallbackSelects...)
	if field.Source == "root" || field.Source == "" {
		return c.compileFirstNonNullExpr(rootVar+".payload", selects), nil
	}
	setVar := sanitizeColumnName(field.Source)
	mode := modes[field.Source]
	return fmt.Sprintf(`FIRST(
    FOR __item IN %s
      LET __value = %s
      FILTER __value != null
      RETURN __value
  )`, setVar, c.compileFirstNonNullExpr(setPayloadVar("__item", mode), selects)), nil
}

func (c *compiler) compileUniqueField(rootVar string, field DerivedField, modes map[string]setMode) (string, error) {
	selects := append([]string{field.Select}, field.FallbackSelects...)
	if field.Source == "root" || field.Source == "" {
		return fmt.Sprintf("SORTED_UNIQUE(%s)", c.compileSelectorArrayForSelects(rootVar+".payload", selects)), nil
	}
	setVar := sanitizeColumnName(field.Source)
	mode := modes[field.Source]
	return fmt.Sprintf(`SORTED_UNIQUE(FLATTEN(
    FOR __item IN %s
      RETURN %s
  ))`, setVar, c.compileSelectorArrayForSelects(setPayloadVar("__item", mode), selects)), nil
}

func (c *compiler) compilePivotField(field DerivedField, modes map[string]setMode) (string, error) {
	setVar := sanitizeColumnName(field.Source)
	mode := modes[field.Source]
	keySel, err := ParseSelector(field.PivotKeySelect)
	if err != nil {
		return "", err
	}
	valueSel, err := ParseSelector(field.PivotValueSelect)
	if err != nil {
		return "", err
	}
	return c.compilePivotMapExpr("FOR __item IN "+setVar, setPayloadVar("__item", mode), keySel, valueSel, field.PivotColumns)
}

func (c *compiler) compileRepresentativeSlice(slice RepresentativeSlice, modes map[string]setMode) (string, error) {
	setVar := sanitizeColumnName(slice.SourceSet)
	mode := modes[slice.SourceSet]
	filter := "true"
	if strings.TrimSpace(slice.Predicate) != "" || strings.TrimSpace(slice.PredicatePath) != "" {
		filter = c.compilePredicateExpr("__item", mode, slice.Predicate, slice.PredicatePath, slice.PredicateEquals)
	}
	return fmt.Sprintf("SLICE(FOR __item IN %s FILTER %s RETURN %s, 0, %d)", setVar, filter, c.compileSliceProjection("__item", mode, slice.Fields), slice.Limit), nil
}

func (c *compiler) compilePredicateExpr(itemVar string, mode setMode, predicate string, predicatePath string, predicateEquals string) string {
	if strings.TrimSpace(predicatePath) != "" {
		sel, err := ParseSelector(predicatePath)
		if err == nil {
			values := compileSelectorArrayExpr(setPayloadVar(itemVar, mode), sel, c)
			if predicateEquals != "" {
				bind := c.newBind("predicate_equals", predicateEquals)
				return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER __value == @%s LIMIT 1 RETURN 1) > 0", values, bind)
			}
			return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER __value != null LIMIT 1 RETURN 1) > 0", values)
		}
	}
	predicate = strings.TrimSpace(predicate)
	if predicate == "" {
		return "true"
	}
	if strings.Contains(predicate, ".") || strings.Contains(predicate, "[") || strings.Contains(predicate, " where ") {
		sel, err := ParseSelector(predicate)
		if err == nil {
			return fmt.Sprintf("FIRST(%s) != null", compileSelectorArrayExpr(setPayloadVar(itemVar, mode), sel, c))
		}
	}
	return itemVar + "." + predicate
}

func (c *compiler) compileRootAggregateExpr(payloadVar string, agg AggregateSelect) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
	case "COUNT":
		if strings.TrimSpace(agg.PredicatePath) == "" {
			return "1", nil
		}
		filter := c.compileRootPredicateExpr(payloadVar, agg.PredicatePath, agg.PredicateEquals)
		return fmt.Sprintf("(%s ? 1 : 0)", filter), nil
	case "COUNT_DISTINCT":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for COUNT_DISTINCT", agg.Name)
		}
		values, err := c.compileRootSelectorArray(payloadVar, agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("LENGTH(SORTED_UNIQUE(%s))", values), nil
	case "EXISTS":
		if strings.TrimSpace(agg.PredicatePath) != "" {
			return c.compileRootPredicateExpr(payloadVar, agg.PredicatePath, agg.PredicateEquals), nil
		}
		if strings.TrimSpace(agg.Select) == "" {
			return "true", nil
		}
		values, err := c.compileRootSelectorArray(payloadVar, agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER __value != null LIMIT 1 RETURN 1) > 0", values), nil
	case "DISTINCT_VALUES":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for DISTINCT_VALUES", agg.Name)
		}
		values, err := c.compileRootSelectorArray(payloadVar, agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SORTED_UNIQUE(%s)", values), nil
	case "MIN", "MAX":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for %s", agg.Name, strings.ToUpper(strings.TrimSpace(agg.Operation)))
		}
		values, err := c.compileRootSelectorArray(payloadVar, agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", strings.ToUpper(strings.TrimSpace(agg.Operation)), values), nil
	default:
		return "", fmt.Errorf("unsupported aggregate operation %q", agg.Operation)
	}
}

func (c *compiler) compileRootSlice(rootVar string, slice RepresentativeSlice) (string, error) {
	filter := "true"
	if strings.TrimSpace(slice.PredicatePath) != "" || strings.TrimSpace(slice.Predicate) != "" {
		filter = c.compilePredicateExpr("__item", setModeNode, slice.Predicate, slice.PredicatePath, slice.PredicateEquals)
	}
	return fmt.Sprintf("SLICE(FOR __item IN [%s] FILTER %s RETURN %s, 0, %d)", rootVar, filter, c.compileSliceProjection("__item", setModeNode, slice.Fields), slice.Limit), nil
}

func (c *compiler) compileSliceProjection(itemVar string, mode setMode, fields []FieldSelect) string {
	if len(fields) == 0 {
		return itemVar
	}
	lines := make([]string, 0, len(fields))
	payloadVar := setPayloadVar(itemVar, mode)
	for _, field := range fields {
		sel, err := ParseSelector(field.Select)
		if err != nil {
			continue
		}
		var expr string
		if len(field.FallbackSelects) > 0 {
			expr = c.compileFirstNonNullExpr(payloadVar, append([]string{field.Select}, field.FallbackSelects...))
		} else if sel.Filter == nil && selectorHasNoArrays(sel) {
			expr = compileDirectExpr(payloadVar, sel.Steps)
		} else {
			expr = "FIRST" + compileSelectorArrayExpr(payloadVar, sel, c)
		}
		lines = append(lines, fmt.Sprintf("%s: %s", quoteKey(field.Name), expr))
	}
	if len(lines) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(lines, ", ") + " }"
}

func (c *compiler) compileRootPredicateExpr(payloadVar string, predicatePath string, predicateEquals string) string {
	sel, err := ParseSelector(predicatePath)
	if err != nil {
		return "false"
	}
	values := compileSelectorArrayExpr(payloadVar, sel, c)
	if predicateEquals != "" {
		bind := c.newBind("predicate_equals", predicateEquals)
		return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER __value == @%s LIMIT 1 RETURN 1) > 0", values, bind)
	}
	return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER __value != null LIMIT 1 RETURN 1) > 0", values)
}

func (c *compiler) compileRootSelectorArray(payloadVar string, selectText string) (string, error) {
	sel, err := ParseSelector(selectText)
	if err != nil {
		return "", err
	}
	return compileSelectorArrayExpr(payloadVar, sel, c), nil
}

func (c *compiler) compileSetAggregateExpr(setVar string, agg AggregateSelect) (string, error) {
	mode := setModeNode
	switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
	case "COUNT":
		if strings.TrimSpace(agg.PredicatePath) == "" {
			return fmt.Sprintf("LENGTH(%s)", setVar), nil
		}
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s RETURN 1)", setVar, c.compilePredicateExpr("__item", mode, "", agg.PredicatePath, agg.PredicateEquals)), nil
	case "COUNT_DISTINCT":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for COUNT_DISTINCT", agg.Name)
		}
		sel, err := ParseSelector(agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("LENGTH(SORTED_UNIQUE(FLATTEN(FOR __item IN %s RETURN %s)))", setVar, compileSelectorArrayExpr("__item.payload", sel, c)), nil
	case "EXISTS":
		if strings.TrimSpace(agg.PredicatePath) != "" {
			return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s LIMIT 1 RETURN 1) > 0", setVar, c.compilePredicateExpr("__item", mode, "", agg.PredicatePath, agg.PredicateEquals)), nil
		}
		if strings.TrimSpace(agg.Select) == "" {
			return fmt.Sprintf("LENGTH(%s) > 0", setVar), nil
		}
		sel, err := ParseSelector(agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER LENGTH(%s) > 0 LIMIT 1 RETURN 1) > 0", setVar, compileSelectorArrayExpr("__item.payload", sel, c)), nil
	case "DISTINCT_VALUES":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for DISTINCT_VALUES", agg.Name)
		}
		sel, err := ParseSelector(agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SORTED_UNIQUE(FLATTEN(FOR __item IN %s RETURN %s))", setVar, compileSelectorArrayExpr("__item.payload", sel, c)), nil
	case "MIN", "MAX":
		if strings.TrimSpace(agg.Select) == "" {
			return "", fmt.Errorf("aggregate %q requires fhirPath for %s", agg.Name, strings.ToUpper(strings.TrimSpace(agg.Operation)))
		}
		sel, err := ParseSelector(agg.Select)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(FLATTEN(FOR __item IN %s RETURN %s))", strings.ToUpper(strings.TrimSpace(agg.Operation)), setVar, compileSelectorArrayExpr("__item.payload", sel, c)), nil
	default:
		return "", fmt.Errorf("unsupported aggregate operation %q", agg.Operation)
	}
}

func (c *compiler) compileSetSlice(setVar string, mode setMode, slice RepresentativeSlice) (string, error) {
	filter := "true"
	if strings.TrimSpace(slice.PredicatePath) != "" || strings.TrimSpace(slice.Predicate) != "" {
		filter = c.compilePredicateExpr("__item", mode, slice.Predicate, slice.PredicatePath, slice.PredicateEquals)
	}
	return fmt.Sprintf("SLICE(FOR __item IN %s FILTER %s RETURN %s, 0, %d)", setVar, filter, c.compileSliceProjection("__item", mode, slice.Fields), slice.Limit), nil
}

func (c *compiler) compileFirstNonNullExpr(rootVar string, selects []string) string {
	parts := make([]string, 0, len(selects))
	for _, selText := range selects {
		sel, err := ParseSelector(selText)
		if err != nil {
			continue
		}
		if sel.Filter == nil && selectorHasNoArrays(sel) {
			parts = append(parts, compileDirectExpr(rootVar, sel.Steps))
		} else {
			parts = append(parts, "FIRST"+compileSelectorArrayExpr(rootVar, sel, c))
		}
	}
	if len(parts) == 0 {
		return "null"
	}
	return fmt.Sprintf("FIRST(FOR __candidate IN [%s] FILTER __candidate != null RETURN __candidate)", strings.Join(parts, ", "))
}

func (c *compiler) compileSelectorArrayForSelects(rootVar string, selects []string) string {
	if len(selects) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(selects))
	for _, selText := range selects {
		sel, err := ParseSelector(selText)
		if err != nil {
			continue
		}
		if sel.Filter == nil && selectorHasNoArrays(sel) {
			parts = append(parts, fmt.Sprintf("(FOR __value IN [%s] FILTER __value != null RETURN __value)", compileDirectExpr(rootVar, sel.Steps)))
		} else {
			parts = append(parts, compileSelectorArrayExpr(rootVar, sel, c))
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return fmt.Sprintf("FLATTEN([%s])", strings.Join(parts, ", "))
}

func setPayloadVar(itemVar string, mode setMode) string {
	if mode == setModeNode {
		return itemVar + ".payload"
	}
	return itemVar
}

func compileDocumentReferenceSummarySet(source string) string {
	return fmt.Sprintf(`(
    FOR __doc_ref IN %s
      LET doc = __doc_ref.payload
      LET doc_ids = doc.identifier ? doc.identifier : []
      LET content = LENGTH(doc.content ? doc.content : []) > 0 ? doc.content[0] : {}
      LET attachment = content.attachment ? content.attachment : {}
      LET type_codings = doc.type && doc.type.coding ? doc.type.coding : []
      LET category_codings = FLATTEN(
        FOR category IN doc.category ? doc.category : []
          RETURN category.coding ? category.coding : []
      )
      LET file_id = FIRST(FOR id IN doc_ids FILTER CONTAINS(id.system ? id.system : "", "file_id") RETURN id.value)
      LET data_format = FIRST(FOR coding IN type_codings RETURN coding.display ? coding.display : coding.code)
      LET data_category = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "data_category") RETURN coding.display ? coding.display : coding.code)
      LET data_type = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "data_type") RETURN coding.display ? coding.display : coding.code)
      LET experimental_strategy = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "experimental_strategy") RETURN coding.display ? coding.display : coding.code)
      LET workflow_type = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "workflow_type") RETURN coding.display ? coding.display : coding.code)
      LET platform = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "platform") RETURN coding.display ? coding.display : coding.code)
      LET access = FIRST(FOR coding IN category_codings FILTER CONTAINS(coding.system ? coding.system : "", "access") RETURN coding.display ? coding.display : coding.code)
      RETURN {
        file_did: doc.id,
        file_id: file_id,
        file_name: attachment.title,
        file_url: attachment.url,
        file_size: attachment.size,
        data_category: data_category,
        data_type: data_type,
        data_format: data_format,
        experimental_strategy: experimental_strategy,
        workflow_type: workflow_type,
        platform: platform,
        access: access,
        is_snv: data_category == "Simple Nucleotide Variation",
        is_annotated_somatic: data_category == "Simple Nucleotide Variation" && data_type == "Annotated Somatic Mutation",
        is_raw_somatic: data_category == "Simple Nucleotide Variation" && data_type == "Raw Simple Somatic Mutation",
        is_expression: data_category == "Transcriptome Profiling" && data_type == "Gene Expression Quantification",
        is_fusion: data_type == "Transcript Fusion",
        is_cnv: data_category == "Copy Number Variation",
        is_methylation: data_category == "DNA Methylation",
        is_slide: data_type == "Slide Image",
        is_aligned_reads: data_type == "Aligned Reads",
        is_clinical: data_category == "Clinical",
        is_wxs: experimental_strategy == "WXS",
        is_wgs: experimental_strategy == "WGS",
        is_rnaseq: experimental_strategy == "RNA-Seq"
      }
  )`, source)
}

func compileStudyLookupSet(rootVar, researchSubjects string) string {
	return fmt.Sprintf(`(
    LET __study_ref = FIRST(
      FOR __item IN %s
        LET __value = __item.payload.study ? __item.payload.study.reference : null
        FILTER __value != null
        RETURN __value
    )
    LET __resolved_ref = __study_ref ? __study_ref : FIRST(
      FOR __ext IN ( %s.payload.extension ? %s.payload.extension : [] )
        FILTER CONTAINS(__ext.url ? __ext.url : "", "part-of-study") && __ext.valueReference
        RETURN __ext.valueReference.reference
    )
    LET __study_parts = __resolved_ref ? SPLIT(__resolved_ref, "/") : []
    LET __study_id = LENGTH(__study_parts) == 2 ? __study_parts[1] : null
    LET __study = FIRST(
      FOR __candidate IN ResearchStudy
        FILTER __study_id != null
        FILTER __candidate.project == @project
        FILTER __candidate.dataset_generation == @dataset_generation
        FILTER @auth_resource_paths_unrestricted == true OR __candidate.auth_resource_path IN @auth_resource_paths
        FILTER __candidate.id == __study_id
        LIMIT 1
        RETURN __candidate
    )
    FILTER __study != null
    RETURN __study.payload
  )`, researchSubjects, rootVar, rootVar)
}

func literalExpr(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
