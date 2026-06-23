package dataframe

import "strings"

type PlanHint struct {
	Mode                    string
	Profile                 string
	NamedSetCount           int
	ClassifiedFileSummaries bool
	StudyLookup             bool
}

type logicalRequest struct {
	Project           string
	AuthResourcePaths []string
	Root              logicalNode
}

type logicalNode struct {
	ResourceType string
	Alias        string
	Label        string
	Fields       []FieldSelect
	Pivots       []PivotSelect
	Aggregates   []AggregateSelect
	Slices       []RepresentativeSlice
	Children     []logicalNode
}

type loweringContext struct {
	request                     logicalRequest
	builder                     Builder
	setsByName                  map[string]struct{}
	modes                       map[string]string
	rootNeighborSet             string
	patientTypeSets             map[string]string
	specimenGroupSet            string
	patientDocumentReferenceSet string
	specimenDocumentRefSet      string
	groupDocumentRefSet         string
	documentReferenceUnionSet   string
	documentReferenceSummarySet string
	classifyDocumentReferences  bool
	studyLookupSet              string
}

func buildLogicalRequest(builder Builder) logicalRequest {
	return logicalRequest{
		Project:           builder.Project,
		AuthResourcePaths: cloneStrings(builder.AuthResourcePaths),
		Root: logicalNode{
			ResourceType: builder.RootResourceType,
			Alias:        "root",
			Fields:       append([]FieldSelect(nil), builder.Fields...),
			Pivots:       append([]PivotSelect(nil), builder.Pivots...),
			Aggregates:   append([]AggregateSelect(nil), builder.Aggregates...),
			Slices:       append([]RepresentativeSlice(nil), builder.Slices...),
			Children:     logicalNodesFromTraversal(builder.Traversals),
		},
	}
}

func logicalNodesFromTraversal(in []TraversalStep) []logicalNode {
	if len(in) == 0 {
		return []logicalNode{}
	}
	out := make([]logicalNode, 0, len(in))
	for _, step := range in {
		out = append(out, logicalNode{
			ResourceType: step.ToResourceType,
			Alias:        step.Alias,
			Label:        step.Label,
			Fields:       append([]FieldSelect(nil), step.Fields...),
			Pivots:       append([]PivotSelect(nil), step.Pivots...),
			Aggregates:   append([]AggregateSelect(nil), step.Aggregates...),
			Slices:       append([]RepresentativeSlice(nil), step.Slices...),
			Children:     logicalNodesFromTraversal(step.Traversals),
		})
	}
	return out
}

func lowerGraphQLBuilder(builder Builder) (Builder, bool) {
	request := buildLogicalRequest(builder)
	if request.Root.ResourceType != "Patient" {
		builder.PlanHint = &PlanHint{Mode: "generic_traversal"}
		return builder, false
	}
	if !supportsPatientCaseAssayFamily(request.Root, request.Root.ResourceType) {
		builder.PlanHint = &PlanHint{Mode: "generic_traversal"}
		return builder, false
	}
	if !shouldUseStructuralLowering(request.Root) {
		builder.PlanHint = &PlanHint{Mode: "generic_traversal"}
		return builder, false
	}

	ctx := &loweringContext{
		request: request,
		builder: Builder{
			Project:           request.Project,
			AuthResourcePaths: request.AuthResourcePaths,
			RootResourceType:  request.Root.ResourceType,
			Fields:            append([]FieldSelect(nil), request.Root.Fields...),
			Pivots:            append([]PivotSelect(nil), request.Root.Pivots...),
			Aggregates:        append([]AggregateSelect(nil), request.Root.Aggregates...),
			Slices:            append([]RepresentativeSlice(nil), request.Root.Slices...),
			Sets:              []NamedSet{},
			DerivedFields:     []DerivedField{},
		},
		setsByName:      map[string]struct{}{},
		modes:           map[string]string{},
		patientTypeSets: map[string]string{},
	}

	ctx.classifyDocumentReferences = shouldClassifyDocumentReferenceFields(request.Root)
	if !ctx.lowerPatientRoot(request.Root) {
		builder.PlanHint = &PlanHint{Mode: "generic_traversal"}
		return builder, false
	}

	ctx.builder.PlanHint = &PlanHint{
		Mode:                    "advanced_lowered",
		Profile:                 "patient_case_assay_family",
		NamedSetCount:           len(ctx.builder.Sets),
		ClassifiedFileSummaries: ctx.documentReferenceSummarySet != "",
		StudyLookup:             ctx.studyLookupSet != "",
	}
	return ctx.builder, true
}

func supportsPatientCaseAssayFamily(node logicalNode, sourceType string) bool {
	for _, child := range node.Children {
		if !supportsPatientCaseAssayTraversal(sourceType, child) {
			return false
		}
		if !supportsPatientCaseAssayFamily(child, child.ResourceType) {
			return false
		}
	}
	return true
}

func supportsPatientCaseAssayTraversal(sourceType string, node logicalNode) bool {
	switch sourceType {
	case "Patient":
		return node.Label == "subject_Patient" && containsString([]string{"Condition", "ResearchSubject", "Specimen", "MedicationAdministration", "DocumentReference"}, node.ResourceType)
	case "Specimen":
		return (node.Label == "member_entity_Specimen" && node.ResourceType == "Group") ||
			(node.Label == "subject_Specimen" && node.ResourceType == "DocumentReference")
	case "Group":
		return node.Label == "subject_Group" && node.ResourceType == "DocumentReference"
	default:
		return false
	}
}

func (ctx *loweringContext) lowerPatientRoot(root logicalNode) bool {
	useRootNeighbors := shouldUseRootNeighborSet(root.Children)
	if useRootNeighbors {
		ctx.rootNeighborSet = ctx.ensureSet(NamedSet{
			Name:   "root_patient_neighbor_set",
			Kind:   SetKindTraverse,
			Label:  "subject_Patient",
			Unique: true,
		}, "node")
	}

	for _, child := range root.Children {
		switch child.ResourceType {
		case "Condition", "ResearchSubject", "Specimen", "MedicationAdministration":
			sourceSet := ctx.ensurePatientChildSet(child.ResourceType, useRootNeighbors)
			if child.ResourceType == "ResearchSubject" && requestNeedsStudyHydration(child) {
				ctx.ensureStudyLookupSet(sourceSet)
			}
			if child.ResourceType == "Specimen" {
				if !ctx.lowerSpecimenNode(child, sourceSet) {
					return false
				}
			} else {
				ctx.lowerNodeSelections(child, sourceSet, false)
			}
		case "DocumentReference":
			sourceSet := ctx.ensurePatientDocumentReferenceSet(useRootNeighbors)
			ctx.lowerDocumentReferenceNode(child, sourceSet)
		default:
			return false
		}
	}

	if ctx.classifyDocumentReferences {
		ctx.ensureDocumentReferenceSummarySet()
	}
	return true
}

func (ctx *loweringContext) lowerSpecimenNode(node logicalNode, specimenSet string) bool {
	ctx.lowerNodeSelections(node, specimenSet, false)
	for _, child := range node.Children {
		switch child.ResourceType {
		case "Group":
			groupSet := ctx.ensureSpecimenGroupSet(specimenSet)
			ctx.lowerNodeSelections(child, groupSet, false)
			for _, grandchild := range child.Children {
				if grandchild.ResourceType != "DocumentReference" {
					return false
				}
				docSet := ctx.ensureGroupDocumentReferenceSet(groupSet)
				ctx.lowerDocumentReferenceNode(grandchild, docSet)
			}
		case "DocumentReference":
			docSet := ctx.ensureSpecimenDocumentReferenceSet(specimenSet)
			ctx.lowerDocumentReferenceNode(child, docSet)
		default:
			return false
		}
	}
	return true
}

func (ctx *loweringContext) lowerDocumentReferenceNode(node logicalNode, routeSet string) {
	useSummary := ctx.classifyDocumentReferences && hasSingleDocumentReferenceAlias(ctx.request.Root)
	sourceSet := routeSet
	if useSummary {
		sourceSet = ctx.ensureDocumentReferenceSummarySet()
	}
	ctx.lowerNodeSelections(node, sourceSet, useSummary)
}

func (ctx *loweringContext) lowerNodeSelections(node logicalNode, sourceSet string, useDocumentSummary bool) {
	for _, field := range node.Fields {
		selectExpr := field.Select
		fallbacks := append([]string(nil), field.FallbackSelects...)
		if useDocumentSummary {
			if mapped, ok := mapDocumentReferenceSelectorToSummaryField(field.Select); ok {
				selectExpr = mapped
			}
			for i := range fallbacks {
				if mapped, ok := mapDocumentReferenceSelectorToSummaryField(fallbacks[i]); ok {
					fallbacks[i] = mapped
				}
			}
		}
		ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, DerivedField{
			Name:            sanitizeColumnName(node.Alias + "__" + field.Name),
			Source:          sourceSet,
			Operation:       DerivedOpUnique,
			Select:          selectExpr,
			FallbackSelects: fallbacks,
		})
	}
	for _, pivot := range node.Pivots {
		cols := pivot.Columns
		if len(cols) == 0 {
			cols = []string{"value"}
		}
		for _, col := range cols {
			ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, DerivedField{
				Name:        sanitizeColumnName(node.Alias + "__" + pivot.Name + "__" + col),
				Source:      sourceSet,
				Operation:   DerivedOpPivot,
				Select:      pivot.Select,
				PivotColumn: col,
				ValuePath:   pivot.ValuePath,
			})
		}
	}
	for _, agg := range node.Aggregates {
		field := DerivedField{
			Name:            sanitizeColumnName(node.Alias + "__" + agg.Name),
			Source:          sourceSet,
			Select:          agg.Select,
			PredicatePath:   agg.PredicatePath,
			PredicateEquals: agg.PredicateEquals,
		}
		if useDocumentSummary {
			if mapped, ok := mapDocumentReferenceSelectorToSummaryField(field.Select); ok {
				field.Select = mapped
			}
			if mapped, ok := mapDocumentReferenceSelectorToSummaryField(field.PredicatePath); ok {
				field.PredicatePath = mapped
			}
		}
		switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
		case "COUNT":
			if strings.TrimSpace(field.PredicatePath) != "" || strings.TrimSpace(field.Predicate) != "" {
				field.Operation = DerivedOpCountWhere
			} else {
				field.Operation = DerivedOpCount
			}
		case "COUNT_DISTINCT":
			field.Operation = DerivedOpCountDistinct
		case "EXISTS":
			field.Operation = DerivedOpAny
		case "DISTINCT_VALUES":
			field.Operation = DerivedOpUnique
		default:
			continue
		}
		ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, field)
	}
	for _, slice := range node.Slices {
		projected := RepresentativeSlice{
			Name:            sanitizeColumnName(node.Alias + "__" + slice.Name),
			SourceSet:       sourceSet,
			PredicatePath:   slice.PredicatePath,
			PredicateEquals: slice.PredicateEquals,
			Limit:           slice.Limit,
			Fields:          append([]FieldSelect(nil), slice.Fields...),
		}
		if useDocumentSummary {
			if mapped, ok := mapDocumentReferenceSelectorToSummaryField(projected.PredicatePath); ok {
				projected.PredicatePath = mapped
			}
			for i := range projected.Fields {
				if mapped, ok := mapDocumentReferenceSelectorToSummaryField(projected.Fields[i].Select); ok {
					projected.Fields[i].Select = mapped
				}
				for j := range projected.Fields[i].FallbackSelects {
					if mapped, ok := mapDocumentReferenceSelectorToSummaryField(projected.Fields[i].FallbackSelects[j]); ok {
						projected.Fields[i].FallbackSelects[j] = mapped
					}
				}
			}
		}
		ctx.builder.RepresentativeSlices = append(ctx.builder.RepresentativeSlices, projected)
	}
}

func (ctx *loweringContext) ensureSet(set NamedSet, mode string) string {
	if _, ok := ctx.setsByName[set.Name]; ok {
		return set.Name
	}
	ctx.builder.Sets = append(ctx.builder.Sets, set)
	ctx.setsByName[set.Name] = struct{}{}
	ctx.modes[set.Name] = mode
	return set.Name
}

func (ctx *loweringContext) ensurePatientChildSet(resourceType string, useRootNeighbors bool) string {
	if name, ok := ctx.patientTypeSets[resourceType]; ok {
		return name
	}
	setName := "patient_" + sanitizeColumnName(strings.ToLower(resourceType)) + "_set"
	set := NamedSet{
		Name:              setName,
		Kind:              SetKindFilter,
		Source:            ctx.rootNeighborSet,
		MatchResourceType: resourceType,
		SortField:         "_key",
	}
	if !useRootNeighbors {
		set = NamedSet{
			Name:           setName,
			Kind:           SetKindTraverse,
			Label:          "subject_Patient",
			ToResourceType: resourceType,
			Unique:         true,
		}
	}
	ctx.patientTypeSets[resourceType] = ctx.ensureSet(set, "node")
	return ctx.patientTypeSets[resourceType]
}

func (ctx *loweringContext) ensureSpecimenGroupSet(specimenSet string) string {
	if ctx.specimenGroupSet != "" {
		return ctx.specimenGroupSet
	}
	ctx.specimenGroupSet = ctx.ensureSet(NamedSet{
		Name:           "specimen_group_set",
		Kind:           SetKindTraverse,
		Source:         specimenSet,
		Label:          "member_entity_Specimen",
		ToResourceType: "Group",
		Unique:         true,
	}, "node")
	return ctx.specimenGroupSet
}

func (ctx *loweringContext) ensurePatientDocumentReferenceSet(useRootNeighbors bool) string {
	if ctx.patientDocumentReferenceSet != "" {
		return ctx.patientDocumentReferenceSet
	}
	set := NamedSet{
		Name:              "patient_document_reference_set",
		Kind:              SetKindFilter,
		Source:            ctx.rootNeighborSet,
		MatchResourceType: "DocumentReference",
	}
	if !useRootNeighbors {
		set = NamedSet{
			Name:           "patient_document_reference_set",
			Kind:           SetKindTraverse,
			Label:          "subject_Patient",
			ToResourceType: "DocumentReference",
			Unique:         true,
		}
	}
	ctx.patientDocumentReferenceSet = ctx.ensureSet(set, "node")
	return ctx.patientDocumentReferenceSet
}

func (ctx *loweringContext) ensureSpecimenDocumentReferenceSet(specimenSet string) string {
	if ctx.specimenDocumentRefSet != "" {
		return ctx.specimenDocumentRefSet
	}
	ctx.specimenDocumentRefSet = ctx.ensureSet(NamedSet{
		Name:           "specimen_document_reference_set",
		Kind:           SetKindTraverse,
		Source:         specimenSet,
		Label:          "subject_Specimen",
		ToResourceType: "DocumentReference",
		Unique:         true,
	}, "node")
	return ctx.specimenDocumentRefSet
}

func (ctx *loweringContext) ensureGroupDocumentReferenceSet(groupSet string) string {
	if ctx.groupDocumentRefSet != "" {
		return ctx.groupDocumentRefSet
	}
	ctx.groupDocumentRefSet = ctx.ensureSet(NamedSet{
		Name:           "group_document_reference_set",
		Kind:           SetKindTraverse,
		Source:         groupSet,
		Label:          "subject_Group",
		ToResourceType: "DocumentReference",
		Unique:         true,
	}, "node")
	return ctx.groupDocumentRefSet
}

func (ctx *loweringContext) ensureDocumentReferenceUnionSet() string {
	if ctx.documentReferenceUnionSet != "" {
		return ctx.documentReferenceUnionSet
	}
	sources := []string{}
	for _, name := range []string{ctx.patientDocumentReferenceSet, ctx.specimenDocumentRefSet, ctx.groupDocumentRefSet} {
		if name != "" {
			sources = append(sources, name)
		}
	}
	if len(sources) == 0 {
		return ""
	}
	if len(sources) == 1 {
		ctx.documentReferenceUnionSet = sources[0]
		return ctx.documentReferenceUnionSet
	}
	ctx.documentReferenceUnionSet = ctx.ensureSet(NamedSet{
		Name:    "document_reference_union_set",
		Kind:    SetKindUnion,
		Sources: sources,
	}, "node")
	return ctx.documentReferenceUnionSet
}

func (ctx *loweringContext) ensureDocumentReferenceSummarySet() string {
	if ctx.documentReferenceSummarySet != "" {
		return ctx.documentReferenceSummarySet
	}
	source := ctx.ensureDocumentReferenceUnionSet()
	if source == "" {
		return ""
	}
	ctx.documentReferenceSummarySet = ctx.ensureSet(NamedSet{
		Name:   "document_reference_summary_set",
		Kind:   SetKindClassifyDocumentReference,
		Source: source,
	}, "object")
	return ctx.documentReferenceSummarySet
}

func (ctx *loweringContext) ensureStudyLookupSet(researchSubjectSet string) string {
	if ctx.studyLookupSet != "" {
		return ctx.studyLookupSet
	}
	ctx.studyLookupSet = ctx.ensureSet(NamedSet{
		Name:   "research_subject_study_lookup_set",
		Kind:   SetKindLookupStudy,
		Source: researchSubjectSet,
	}, "object")
	return ctx.studyLookupSet
}

func shouldUseRootNeighborSet(children []logicalNode) bool {
	count := 0
	for _, child := range children {
		if child.Label == "subject_Patient" {
			count++
		}
	}
	return count > 1
}

func shouldUseStructuralLowering(root logicalNode) bool {
	if len(root.Children) > 1 {
		return true
	}
	if shouldClassifyDocumentReferenceFields(root) {
		return true
	}
	var hasNested bool
	var walk func(node logicalNode)
	walk = func(node logicalNode) {
		if len(node.Children) > 0 {
			hasNested = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, child := range root.Children {
		walk(child)
	}
	return hasNested
}

func shouldClassifyDocumentReferenceFields(root logicalNode) bool {
	nodes := collectDocumentReferenceNodes(root)
	for _, node := range nodes {
		for _, field := range node.Fields {
			if _, ok := mapDocumentReferenceSelectorToSummaryField(field.Select); ok {
				return true
			}
			for _, fallback := range field.FallbackSelects {
				if _, ok := mapDocumentReferenceSelectorToSummaryField(fallback); ok {
					return true
				}
			}
		}
		for _, agg := range node.Aggregates {
			if _, ok := mapDocumentReferenceSelectorToSummaryField(agg.Select); ok {
				return true
			}
			if _, ok := mapDocumentReferenceSelectorToSummaryField(agg.PredicatePath); ok {
				return true
			}
		}
		for _, slice := range node.Slices {
			if _, ok := mapDocumentReferenceSelectorToSummaryField(slice.PredicatePath); ok {
				return true
			}
			for _, field := range slice.Fields {
				if _, ok := mapDocumentReferenceSelectorToSummaryField(field.Select); ok {
					return true
				}
				for _, fallback := range field.FallbackSelects {
					if _, ok := mapDocumentReferenceSelectorToSummaryField(fallback); ok {
						return true
					}
				}
			}
		}
	}
	return false
}

func collectDocumentReferenceNodes(root logicalNode) []logicalNode {
	var out []logicalNode
	var walk func(node logicalNode)
	walk = func(node logicalNode) {
		if node.ResourceType == "DocumentReference" {
			out = append(out, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, child := range root.Children {
		walk(child)
	}
	return out
}

func hasSingleDocumentReferenceAlias(root logicalNode) bool {
	return len(collectDocumentReferenceNodes(root)) == 1
}

func requestNeedsStudyHydration(node logicalNode) bool {
	for _, field := range node.Fields {
		if strings.Contains(field.Name, "study") || strings.Contains(field.Select, "study") {
			return true
		}
	}
	for _, slice := range node.Slices {
		for _, field := range slice.Fields {
			if strings.Contains(field.Name, "study") || strings.Contains(field.Select, "study") {
				return true
			}
		}
	}
	return false
}

func mapDocumentReferenceSelectorToSummaryField(selectText string) (string, bool) {
	sel, err := ParseSelector(selectText)
	if err != nil {
		return "", false
	}
	path := sel.CanonicalPath()
	filterNeedle := ""
	if sel.Filter != nil {
		filterNeedle = sel.Filter.Needle
	}
	switch {
	case path == "identifier[].value" && filterNeedle == "file_id":
		return "file_id", true
	case path == "content[].attachment.title":
		return "file_name", true
	case path == "content[].attachment.url":
		return "file_url", true
	case path == "content[].attachment.size":
		return "file_size", true
	case path == "type.coding[].display" || path == "type.coding[].code":
		return "data_format", true
	case path == "category[].coding[].display" || path == "category[].coding[].code":
		switch filterNeedle {
		case "data_category":
			return "data_category", true
		case "data_type":
			return "data_type", true
		case "experimental_strategy":
			return "experimental_strategy", true
		case "workflow_type":
			return "workflow_type", true
		case "platform":
			return "platform", true
		case "access":
			return "access", true
		}
	}
	return "", false
}

func containsString(in []string, want string) bool {
	for _, item := range in {
		if item == want {
			return true
		}
	}
	return false
}
