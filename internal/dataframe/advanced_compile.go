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

func compileAdvanced(builder Builder, limit int) (CompiledQuery, error) {
	c := &compiler{
		builder: builder,
		bindVars: map[string]any{
			"project":                          builder.Project,
			"auth_resource_paths":              builder.AuthResourcePaths,
			"auth_resource_paths_unrestricted": builder.AuthResourcePaths == nil,
		},
		columns: []string{"_key"},
	}
	if limit > 0 {
		c.bindVars["preview_limit"] = limit
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

	for _, field := range builder.Fields {
		sel, _ := ParseSelector(field.Select)
		expr, err := c.compileRootField(rootVar+".payload", sel)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(field.Name), expr))
		c.columns = append(c.columns, field.Name)
	}
	for _, pivot := range builder.Pivots {
		sel, _ := ParseSelector(pivot.Select)
		cols := pivot.Columns
		if len(cols) == 0 {
			cols = []string{"value"}
		}
		for _, col := range cols {
			colName := sanitizeColumnName(pivot.Name + "__" + col)
			expr, err := c.compileRootPivot(rootVar+".payload", sel, col)
			if err != nil {
				return CompiledQuery{}, err
			}
			objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
			c.columns = append(c.columns, colName)
		}
	}
	for _, step := range builder.Traversals {
		if err := c.compileTraversal(rootVar, false, step, &lets, &objectLines); err != nil {
			return CompiledQuery{}, err
		}
	}
	for _, field := range builder.DerivedFields {
		expr, err := c.compileDerivedField(rootVar, field, setModes)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(field.Name), expr))
		c.columns = append(c.columns, field.Name)
	}
	for _, slice := range builder.RepresentativeSlices {
		expr, err := c.compileRepresentativeSlice(slice, setModes)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(slice.Name), expr))
		c.columns = append(c.columns, slice.Name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("FOR %s IN %s\n", rootVar, builder.RootResourceType))
	sb.WriteString(fmt.Sprintf("  FILTER %s.project == @project\n", rootVar))
	sb.WriteString(fmt.Sprintf("  FILTER @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths\n", rootVar))
	sb.WriteString(fmt.Sprintf("  SORT %s._key\n", rootVar))
	if limit > 0 {
		sb.WriteString("  LIMIT @preview_limit\n")
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
		RootResourceType:  builder.RootResourceType,
		AuthResourcePaths: append([]string(nil), builder.AuthResourcePaths...),
		Query:             sb.String(),
		BindVars:          c.bindVars,
		Columns:           append([]string(nil), c.columns...),
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
		if set.ToResourceType != "" {
			toBind := c.newBind(name+"_to", set.ToResourceType)
			toFilter = fmt.Sprintf(" FILTER __node.resourceType == @%s", toBind)
		}
		query := fmt.Sprintf("  LET %s = UNIQUE(%s)", name, c.compileTraverseSetSource(source, set.Source != "" && set.Source != "root", labelBind, toFilter))
		return query, setModeNode, nil
	case SetKindFilter:
		source := sanitizeColumnName(set.Source)
		lines := []string{fmt.Sprintf("FOR __item IN %s", source)}
		if set.MatchResourceType != "" {
			typeBind := c.newBind(name+"_match", set.MatchResourceType)
			lines = append(lines, fmt.Sprintf("  FILTER __item.resourceType == @%s", typeBind))
		}
		if set.SortField != "" {
			lines = append(lines, fmt.Sprintf("  SORT __item.%s", set.SortField))
		}
		lines = append(lines, "  RETURN __item")
		query := "  LET " + name + " = (\n    " + strings.Join(lines, "\n    ") + "\n  )"
		if set.Unique {
			query = fmt.Sprintf("  LET %s = UNIQUE(%s)", name, strings.TrimPrefix(query, "  LET "+name+" = "))
		}
		return query, modes[set.Source], nil
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

func (c *compiler) compileTraverseSetSource(source string, sourceIsSet bool, labelBind string, toFilter string) string {
	if sourceIsSet {
		return fmt.Sprintf("(FLATTEN(FOR __parent IN %s FOR __node, __edge IN 1..1 INBOUND __parent fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s%s RETURN [__node]))", source, labelBind, toFilter)
	}
	return fmt.Sprintf("(FOR __node, __edge IN 1..1 INBOUND %s fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s%s RETURN __node)", source, labelBind, toFilter)
}

func (c *compiler) compileDerivedField(rootVar string, field DerivedField, modes map[string]setMode) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(field.Operation)) {
	case DerivedOpRawExpr:
		return field.RawExpr, nil
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
	case DerivedOpCountWhere:
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s RETURN 1)", sanitizeColumnName(field.Source), c.compilePredicateExpr("__item", modes[field.Source], field.Predicate)), nil
	case DerivedOpAny:
		return fmt.Sprintf("LENGTH(FOR __item IN %s FILTER %s LIMIT 1 RETURN 1) > 0", sanitizeColumnName(field.Source), c.compilePredicateExpr("__item", modes[field.Source], field.Predicate)), nil
	case DerivedOpFirstNonNull:
		return c.compileFirstNonNullField(rootVar, field, modes)
	case DerivedOpUnique:
		return c.compileUniqueField(rootVar, field, modes)
	default:
		return "", fmt.Errorf("unsupported derived field operation %q", field.Operation)
	}
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
		return fmt.Sprintf("UNIQUE(%s)", c.compileSelectorArrayForSelects(rootVar+".payload", selects)), nil
	}
	setVar := sanitizeColumnName(field.Source)
	mode := modes[field.Source]
	return fmt.Sprintf(`UNIQUE(FLATTEN(
    FOR __item IN %s
      RETURN %s
  ))`, setVar, c.compileSelectorArrayForSelects(setPayloadVar("__item", mode), selects)), nil
}

func (c *compiler) compileRepresentativeSlice(slice RepresentativeSlice, modes map[string]setMode) (string, error) {
	setVar := sanitizeColumnName(slice.SourceSet)
	mode := modes[slice.SourceSet]
	filter := "true"
	if strings.TrimSpace(slice.Predicate) != "" {
		filter = c.compilePredicateExpr("__item", mode, slice.Predicate)
	}
	return fmt.Sprintf("SLICE(FOR __item IN %s FILTER %s RETURN __item, 0, %d)", setVar, filter, slice.Limit), nil
}

func (c *compiler) compilePredicateExpr(itemVar string, mode setMode, predicate string) string {
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
    LET __study = __study_id ? DOCUMENT(CONCAT("ResearchStudy/", __study_id)) : null
    FILTER __study != null
    RETURN __study.payload
  )`, researchSubjects, rootVar, rootVar)
}

func literalExpr(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
