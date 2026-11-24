package aql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func (r *physicalPlanRenderer) renderConstructionExpandStage(stage ir.PhysicalConstructionStage) ([]string, error) {
	expand := stage.Expand
	if expand == nil {
		return nil, fmt.Errorf("expand stage is missing payload")
	}
	if _, ok := r.bindVars[expand.ConstructionIDBindKey]; !ok {
		return nil, fmt.Errorf("expand construction ID bind %q is missing", expand.ConstructionIDBindKey)
	}
	columnBind := r.newInternalBindKey("construction_expand_column")
	r.bindVars[columnBind] = expand.InputColumn
	identityColumn := ""
	for _, column := range stage.InputColumns {
		if column.Identity {
			identityColumn = column.Name
			break
		}
	}
	if identityColumn == "" {
		return nil, fmt.Errorf("expand input has no row identity column")
	}
	identityColumnBind := r.newInternalBindKey("construction_expand_identity_column")
	r.bindVars[identityColumnBind] = identityColumn
	rawVariable := r.newInternalVariable("construction_expand_raw")
	errorMessage := fmt.Sprintf("CONCAT(\"%s: construction \", @%s, \" has no items for row \", %s[@%s])", dataframeerrors.CodeConstructionExpansionEmpty, expand.ConstructionIDBindKey, stage.InputRowVariable, identityColumnBind)
	lines := []string{
		fmt.Sprintf("  LET %s = %s[@%s]", rawVariable, stage.InputRowVariable, columnBind),
		fmt.Sprintf("  FILTER ASSERT(%s == null OR IS_ARRAY(%s), \"CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH\")", rawVariable, rawVariable),
		fmt.Sprintf("  LET %s = (%s == null ? [] : %s)", expand.ItemsVariable, rawVariable, rawVariable),
	}
	if expand.EmptyPolicy == ir.PhysicalUnnestError {
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(LENGTH(%s) > 0, %s)", expand.ItemsVariable, errorMessage))
	}
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? [] : RANGE(0, LENGTH(%s) - 1)", expand.ItemsVariable, expand.ItemsVariable)
	if expand.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", expand.ItemsVariable, expand.ItemsVariable)
	}
	lines = append(lines,
		fmt.Sprintf("  FOR %s IN (%s)", expand.IndexVariable, indices),
		fmt.Sprintf("  LET %s = (%s == null ? null : %s[%s])", expand.ItemVariable, expand.IndexVariable, expand.ItemsVariable, expand.IndexVariable),
	)
	itemType, err := aqlTableScalarType(expand.InputKind)
	if err != nil {
		return nil, fmt.Errorf("expand item: %w", err)
	}
	itemTypeBind := r.newInternalBindKey("construction_expand_item_type")
	r.bindVars[itemTypeBind] = itemType
	lines = append(lines,
		fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH\")", expand.ItemVariable, expand.ItemVariable, itemTypeBind),
		fmt.Sprintf("  LET %s = TO_STRING([[\"input\", %s[@%s]], [\"construction\", @%s], [\"ordinal\", %s]])",
			expand.IdentityVariable, stage.InputRowVariable, identityColumnBind, expand.ConstructionIDBindKey, expand.IndexVariable),
	)
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

func constructionRelatedExpandIdentityExpression(inputRow, parentIdentityColumnBind, constructionIDBind, itemVariable string) string {
	return fmt.Sprintf(
		"TO_STRING([[\"input\", %s[@%s]], [\"construction\", @%s], (%s == null ? [\"empty\"] : [\"related\", %s.terminal_id])])",
		inputRow, parentIdentityColumnBind, constructionIDBind, itemVariable, itemVariable,
	)
}

func terminalRelatedExpandPreviewStage(sequence *ir.PhysicalStageSequence) (int, bool) {
	if sequence == nil || len(sequence.Stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil {
		return -1, false
	}
	index := len(sequence.Stages) - 1
	stage := sequence.Stages[index]
	if stage.ID != sequence.FinalStageID || stage.Kind != ir.PhysicalStageRelatedExpandOp ||
		stage.RelatedExpand == nil || stage.RowIdentityColumn == "" || stage.RowIdentityColumn != sequence.FinalRowIdentity ||
		stage.Filter != nil || len(stage.DerivedLets) != 0 || stage.RelatedExpand.IdentityVariable == "" {
		return -1, false
	}
	for _, projection := range stage.OutputProjections {
		if projection.Name != stage.RowIdentityColumn {
			continue
		}
		return index, projection.Expression == nil && projection.Value.Variable == stage.RelatedExpand.IdentityVariable &&
			projection.Value.BindKey == "" && len(projection.Value.Path) == 0
	}
	return -1, false
}

const relatedCategoryTargetCollectionBind = "__loom_related_category_target_collection"

const relatedCategoryLimitBind = "__loom_category_limit"

const relatedCategoryMaxValues = 256

func renderRelatedCategoryScan(scan ir.PhysicalRelatedCategoryScan, targetCollectionBind string) (string, error) {
	hops := flattenRelatedCategoryHops(scan.Hops)
	if len(hops) == 0 {
		return "", fmt.Errorf("related category scan requires traversal hops")
	}
	path := strings.Join(scan.Category.Path, ".")
	targetType := hops[len(hops)-1].Traversal.TargetTypeBindKey
	hint := fmt.Sprintf(" OPTIONS { indexHint: %q, forceIndexHint: false }", scan.IndexName)
	categoryPathPresence := relatedCategoryPathPresenceExpression("candidate", scan.Category.Path)
	terminalPathPresence := relatedCategoryPathPresenceExpression("terminal", scan.Category.Path)
	scope := func(v string) string {
		return fmt.Sprintf("%s.project == @project AND %s.dataset_generation == @dataset_generation AND (@auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths)", v, v, v)
	}
	lines := []string{
		"LET __loom_related_category_candidates = (",
		fmt.Sprintf(" FOR candidate IN @@%s%s", targetCollectionBind, hint),
		" FILTER " + scope("candidate"),
		fmt.Sprintf(" FILTER candidate.resourceType == @%s", targetType),
		" LET candidate_present = " + categoryPathPresence,
		" COLLECT present = candidate_present, value = candidate." + path,
		" RETURN { present, value }", ")",
		"LET __loom_related_category_values = (",
		" FOR category IN __loom_related_category_candidates",
		" LET membership = (",
		fmt.Sprintf("  FOR terminal IN @@%s%s", targetCollectionBind, hint),
		"  FILTER " + scope("terminal"),
		fmt.Sprintf("  FILTER terminal.resourceType == @%s", targetType),
		"  FILTER (" + terminalPathPresence + ") == category.present",
		"  FILTER terminal." + path + " == category.value",
	}
	current := "terminal"
	for i := len(hops) - 1; i >= 0; i-- {
		hop := hops[i]
		edge := fmt.Sprintf("reverse_edge_%d", i)
		previous := fmt.Sprintf("reverse_document_%d", i)
		targetEndpoint, sourceEndpoint := "_to", "_from"
		if hop.Traversal.Direction == ir.PhysicalInbound {
			targetEndpoint, sourceEndpoint = "_from", "_to"
		}
		lines = append(lines,
			fmt.Sprintf("  FOR %s IN @@%s", edge, hop.Traversal.EdgeCollectionBindKey),
			fmt.Sprintf("  FILTER %s.%s == %s._id", edge, targetEndpoint, current),
			fmt.Sprintf("  FILTER %s.label == @%s AND %s.%s == @%s", edge, hop.Traversal.EdgeLabelBindKey, edge, hop.Traversal.EdgeTargetTypeField, hop.Traversal.TargetTypeBindKey),
			"  FILTER "+scope(edge),
			fmt.Sprintf("  LET %s = DOCUMENT(%s.%s)", previous, edge, sourceEndpoint),
			"  FILTER "+previous+" != null AND "+scope(previous),
		)
		if i == 0 {
			lines = append(lines, fmt.Sprintf("  FILTER IS_SAME_COLLECTION(@%s, %s)", hop.Traversal.SourceTypeBindKey, previous))
		} else {
			lines = append(lines, fmt.Sprintf("  FILTER %s.resourceType == @%s", previous, hop.Traversal.SourceTypeBindKey))
		}
		current = previous
	}
	lines = append(lines, "  LIMIT 1", "  RETURN true", " )", " FILTER LENGTH(membership) > 0", " RETURN category", ")")
	emptyNames := []string{}
	prefix := []ir.PhysicalRelatedCategoryHop{}
	for i, stageHops := range scan.Hops {
		name := fmt.Sprintf("__loom_related_category_empty_stage_%d", i+1)
		emptyNames = append(emptyNames, name)
		rootVar := fmt.Sprintf("empty_root_%d", i)
		lines = append(lines,
			"LET "+name+" = (LENGTH((FOR existing_category IN __loom_related_category_values FILTER existing_category.present == true AND existing_category.value == null LIMIT 1 RETURN true)) > 0) ? [] : (",
			fmt.Sprintf(" FOR %s IN @@%s", rootVar, scan.Root.CollectionBindKey), " FILTER "+scope(rootVar))
		parent := rootVar
		for j, hop := range prefix {
			edge := fmt.Sprintf("parent_edge_%d_%d", i, j)
			target := fmt.Sprintf("parent_document_%d_%d", i, j)
			lines = append(lines, relatedCategoryForwardInline(hop, parent, edge, target, " ", scope)...)
			parent = target
		}
		lines = append(lines, " LET children = (")
		child := parent
		for j, hop := range stageHops {
			edge := fmt.Sprintf("child_edge_%d_%d", i, j)
			target := fmt.Sprintf("child_document_%d_%d", i, j)
			lines = append(lines, relatedCategoryForwardInline(hop, child, edge, target, "  ", scope)...)
			child = target
		}
		lines = append(lines, "  LIMIT 1", "  RETURN true", " )", " FILTER LENGTH(children) == 0", " LIMIT 1", " RETURN { present: true, value: null }", ")")
		prefix = append(prefix, stageHops...)
	}
	union := append([]string{"__loom_related_category_values"}, emptyNames...)
	lines = append(lines,
		"FOR category IN UNION_DISTINCT("+strings.Join(union, ", ")+")",
		" SORT category.present ASC, TYPENAME(category.value), category.value",
		" LIMIT @"+relatedCategoryLimitBind,
		" RETURN { present: category.present, value: category.value }")
	return strings.Join(lines, "\n") + "\n", nil
}

func renderRelatedCategoryStreamingScan(scan ir.PhysicalRelatedCategoryScan, targetCollectionBind string) (string, error) {
	if len(scan.Hops) != 2 || len(scan.Hops[0]) != 1 || len(scan.Hops[1]) != 1 {
		return "", fmt.Errorf("streaming related category scan requires two single-hop stages")
	}
	firstHop, secondHop := scan.Hops[0][0], scan.Hops[1][0]
	projectValueQuantityCode := relatedCategoryValueQuantityCodePath(scan.Category.Path)
	if projectValueQuantityCode && targetCollectionBind == "" {
		return "", fmt.Errorf("projected related category scan requires its target collection bind")
	}
	root := "__loom_related_category_stream_root"
	firstRecords := "__loom_related_category_stream_records_1"
	firstParent := "__loom_related_category_stream_first_parent"
	parentKey := "__loom_related_category_stream_parent_key"
	parent := "__loom_related_category_stream_parent"
	secondRecords := "__loom_related_category_stream_records_2"
	secondDocument := "__loom_related_category_stream_document_2"
	terminal := "__loom_related_category_stream_terminal"
	present := "__loom_related_category_stream_present"
	value := "__loom_related_category_stream_value"
	path := strings.Join(scan.Category.Path, ".")
	scope := func(variable string) string {
		return fmt.Sprintf("%s.project == @project AND %s.dataset_generation == @dataset_generation AND (@auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths)", variable, variable, variable)
	}
	lines := []string{
		fmt.Sprintf("FOR %s IN @@%s", root, scan.Root.CollectionBindKey),
		"FILTER " + scope(root),
		"LET " + firstRecords + " = (",
	}
	lines = append(lines, relatedCategoryForwardInline(firstHop,
		root, "__loom_related_category_stream_edge_1", "__loom_related_category_stream_document_1", " ", scope)...)
	lines = append(lines,
		" RETURN __loom_related_category_stream_document_1._id",
		")",
		"FOR "+firstParent+" IN APPEND("+firstRecords+", LENGTH("+firstRecords+")==0 ? [null] : [])",
		"COLLECT "+parentKey+" = "+firstParent,
		"LET "+parent+" = "+parentKey+" == null ? null : DOCUMENT("+parentKey+")",
		"FILTER "+parentKey+" == null OR ("+parent+" != null AND "+parent+".resourceType == @"+firstHop.Traversal.TargetTypeBindKey+" AND "+scope(parent)+")",
		"LET "+secondRecords+" = "+parentKey+" == null ? [] : (",
	)
	if projectValueQuantityCode {
		lines = append(lines, relatedCategoryProjectedTerminalInline(
			secondHop, parent, "__loom_related_category_stream_edge_2", secondDocument,
			"__loom_related_category_stream_terminal_records", targetCollectionBind, scan.Category.Path, " ", scope)...)
		lines = append(lines, ")",
			"FOR "+terminal+" IN APPEND("+secondRecords+", LENGTH("+secondRecords+")==0 ? [null] : [])",
			"LET "+present+" = "+terminal+" == null ? true : "+terminal+".present",
			"LET "+value+" = "+terminal+" == null ? null : "+terminal+".value",
		)
	} else {
		lines = append(lines, relatedCategoryForwardInline(secondHop, parent,
			"__loom_related_category_stream_edge_2", secondDocument, " ", scope)...)
		lines = append(lines,
			" RETURN "+secondDocument,
			")",
			"FOR "+terminal+" IN APPEND("+secondRecords+", LENGTH("+secondRecords+")==0 ? [null] : [])",
			"LET "+present+" = "+terminal+" == null ? true : ("+relatedCategoryPathPresenceExpression(terminal, scan.Category.Path)+")",
			"LET "+value+" = "+present+" ? "+terminal+"."+path+" : null",
		)
	}
	lines = append(lines,
		"COLLECT present = "+present+", value = "+value,
		"SORT present ASC, TYPENAME(value), value",
		"LIMIT @"+relatedCategoryLimitBind,
		"RETURN { present, value }",
	)
	return strings.Join(lines, "\n") + "\n", nil
}

func flattenRelatedCategoryHops(hops [][]ir.PhysicalRelatedCategoryHop) []ir.PhysicalRelatedCategoryHop {
	out := make([]ir.PhysicalRelatedCategoryHop, 0)
	for _, stage := range hops {
		out = append(out, stage...)
	}
	return out
}

func relatedCategoryPathPresenceExpression(variable string, path []string) string {
	current := variable
	checks := make([]string, 0, len(path))
	for _, segment := range path {
		checks = append(checks, "(IS_OBJECT("+current+") AND HAS("+current+", "+strconv.Quote(segment)+"))")
		current += "." + segment
	}
	return strings.Join(checks, " AND ")
}

func relatedCategoryValueQuantityCodePath(path []string) bool {
	return len(path) == 3 && path[0] == "payload" && path[1] == "valueQuantity" && path[2] == "code"
}

func relatedCategoryValueQuantityCodePresentExpression(variable string) string {
	parent := variable + ".payload.valueQuantity"
	return "(IS_OBJECT(" + parent + ") AND HAS(" + parent + ", \"code\"))"
}

func relatedCategoryProjectedTerminalInline(
	hop ir.PhysicalRelatedCategoryHop,
	source, edge, target, records, collectionBind string,
	path []string,
	indent string,
	scope func(string) string,
) []string {
	sourceEndpoint, targetEndpoint := "_from", "_to"
	if hop.Traversal.Direction == ir.PhysicalInbound {
		sourceEndpoint, targetEndpoint = "_to", "_from"
	}
	projectedPresent := "__loom_related_category_stream_projected_present"
	projectedValue := "__loom_related_category_stream_projected_value"
	fallbackTarget := "__loom_related_category_stream_fallback_document_2"
	fallbackPresent := "__loom_related_category_stream_fallback_present"
	fallbackValue := "__loom_related_category_stream_fallback_value"
	pathExpression := strings.Join(path, ".")
	return []string{
		fmt.Sprintf("%sFOR %s IN @@%s", indent, edge, hop.Traversal.EdgeCollectionBindKey),
		fmt.Sprintf("%sFILTER %s.%s == %s._id", indent, edge, sourceEndpoint, source),
		fmt.Sprintf("%sFILTER %s.label == @%s AND %s.%s == @%s", indent, edge, hop.Traversal.EdgeLabelBindKey, edge, hop.Traversal.EdgeTargetTypeField, hop.Traversal.TargetTypeBindKey),
		indent + "FILTER " + scope(edge),
		fmt.Sprintf("%sLET %s = IS_SAME_COLLECTION(@%s, %s.%s) ? (", indent, records, hop.Traversal.TargetTypeBindKey, edge, targetEndpoint),
		fmt.Sprintf("%s FOR %s IN @@%s", indent, target, collectionBind),
		fmt.Sprintf("%s FILTER %s._id == %s.%s", indent, target, edge, targetEndpoint),
		fmt.Sprintf("%s FILTER %s.resourceType == @%s", indent, target, hop.Traversal.TargetTypeBindKey),
		indent + " FILTER " + scope(target),
		fmt.Sprintf("%s LET %s = %s", indent, projectedPresent, relatedCategoryValueQuantityCodePresentExpression(target)),
		fmt.Sprintf("%s LET %s = %s ? %s.%s : null", indent, projectedValue, projectedPresent, target, pathExpression),
		fmt.Sprintf("%s RETURN { present: %s, value: %s }", indent, projectedPresent, projectedValue),
		indent + ") : (",
		fmt.Sprintf("%s LET %s = DOCUMENT(%s.%s)", indent, fallbackTarget, edge, targetEndpoint),
		fmt.Sprintf("%s FILTER %s != null AND %s.resourceType == @%s", indent, fallbackTarget, fallbackTarget, hop.Traversal.TargetTypeBindKey),
		indent + " FILTER " + scope(fallbackTarget),
		fmt.Sprintf("%s LET %s = %s", indent, fallbackPresent, relatedCategoryPathPresenceExpression(fallbackTarget, path)),
		fmt.Sprintf("%s LET %s = %s ? %s.%s : null", indent, fallbackValue, fallbackPresent, fallbackTarget, pathExpression),
		fmt.Sprintf("%s RETURN { present: %s, value: %s }", indent, fallbackPresent, fallbackValue),
		indent + ")",
		fmt.Sprintf("%sFOR projected_terminal IN %s", indent, records),
		fmt.Sprintf("%sRETURN projected_terminal", indent),
	}
}

func relatedCategoryForwardInline(hop ir.PhysicalRelatedCategoryHop, source, edge, target, indent string, scope func(string) string) []string {
	sourceEndpoint, targetEndpoint := "_from", "_to"
	if hop.Traversal.Direction == ir.PhysicalInbound {
		sourceEndpoint, targetEndpoint = "_to", "_from"
	}
	return []string{
		fmt.Sprintf("%sFOR %s IN @@%s", indent, edge, hop.Traversal.EdgeCollectionBindKey),
		fmt.Sprintf("%sFILTER %s.%s == %s._id", indent, edge, sourceEndpoint, source),
		fmt.Sprintf("%sFILTER %s.label == @%s AND %s.%s == @%s", indent, edge, hop.Traversal.EdgeLabelBindKey, edge, hop.Traversal.EdgeTargetTypeField, hop.Traversal.TargetTypeBindKey),
		indent + "FILTER " + scope(edge),
		fmt.Sprintf("%sLET %s = DOCUMENT(%s.%s)", indent, target, edge, targetEndpoint),
		fmt.Sprintf("%sFILTER %s != null AND %s.resourceType == @%s", indent, target, target, hop.Traversal.TargetTypeBindKey),
		indent + "FILTER " + scope(target),
	}
}

func relatedCategoryPhysicalBind(plan ir.PhysicalPlan, binds map[string]any, key string, collection bool) error {
	value, ok := plan.BindVars[key]
	if !ok || key == "" {
		return fmt.Errorf("related category scan bind %q is missing", key)
	}
	if collection {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("related category collection bind %q is not a string", key)
		}
		key = "@" + key
	}
	return mergeRenderedBindVars(binds, map[string]any{key: ir.ClonePhysicalBindValue(value)})
}

func relatedCategoryTargetCollectionBindKey(source, used map[string]any) string {
	key := relatedCategoryTargetCollectionBind
	for suffix := 1; ; suffix++ {
		_, sourceCollision := source[key]
		_, usedCollision := used[key]
		if !sourceCollision && !usedCollision {
			return key
		}
		key = fmt.Sprintf("%s_%d", relatedCategoryTargetCollectionBind, suffix)
	}
}

func validateRelatedCategoryDescriptor(plan ir.PhysicalPlan, scan ir.PhysicalRelatedCategoryScan) error {
	sequence := plan.StageSequence
	if sequence == nil {
		return fmt.Errorf("related category descriptor requires a staged plan")
	}
	stageIndex := 0
	var previous *ir.PhysicalStageRelatedExpand
	for _, stage := range sequence.Stages {
		if stage.RelatedExpand == nil {
			continue
		}
		if stageIndex >= len(scan.Hops) || len(scan.Hops[stageIndex]) != len(stage.RelatedExpand.Route) || len(scan.Hops[stageIndex]) == 0 {
			return fmt.Errorf("related category route does not match the stage prefix")
		}
		expand := stage.RelatedExpand
		actualTraversals := make([]ir.PhysicalTraversal, 0, len(expand.Route))
		for _, operation := range expand.RelatedRecords.Operations {
			if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
				actualTraversals = append(actualTraversals, *operation.Traversal)
			}
		}
		if len(actualTraversals) != len(expand.Route) {
			return fmt.Errorf("related category traversal descriptor does not match its physical subplan")
		}
		for hopIndex, hop := range scan.Hops[stageIndex] {
			route, traversal := hop.Route, hop.Traversal
			if !relatedCategorySameRoute(route, expand.Route[hopIndex]) || !relatedCategorySameTraversal(traversal, actualTraversals[hopIndex]) {
				return fmt.Errorf("related category hop descriptor does not match the physical route")
			}
			if route.EdgeID == "" || route.FromNodeID == "" || route.ToNodeID == "" || route.FromResourceType == "" || route.ToResourceType == "" || route.Relationship == "" ||
				plan.BindVars[traversal.SourceTypeBindKey] != route.FromResourceType || plan.BindVars[traversal.TargetTypeBindKey] != route.ToResourceType ||
				plan.BindVars[traversal.EdgeLabelBindKey] != route.Relationship || plan.BindVars[traversal.EdgeCollectionBindKey] != fhirschema.EdgeCollection ||
				traversal.Direction != relatedCategoryTraversalDirection(route.StorageDirection) || traversal.EdgeTargetTypeField != traversal.Direction.TargetTypeField() {
				return fmt.Errorf("related category traversal does not match its authored route")
			}
			if hopIndex > 0 && (scan.Hops[stageIndex][hopIndex-1].Route.ToNodeID != route.FromNodeID || scan.Hops[stageIndex][hopIndex-1].Route.ToResourceType != route.FromResourceType) {
				return fmt.Errorf("related category route is disconnected")
			}
			if hopIndex == 0 && previous == nil && (expand.AnchorKind != "root" || route.FromNodeID != expand.AnchorNodeID || route.FromResourceType != expand.AnchorResourceType) {
				return fmt.Errorf("related category root anchor is inconsistent")
			}
			if hopIndex == 0 && previous != nil && (expand.AnchorKind != "activeRelatedRecord" || route.FromNodeID != expand.AnchorNodeID || route.FromResourceType != expand.AnchorResourceType) {
				return fmt.Errorf("related category active anchor is inconsistent")
			}
		}
		last := scan.Hops[stageIndex][len(scan.Hops[stageIndex])-1].Route
		if expand.TargetNodeID != last.ToNodeID || expand.TargetResourceType != last.ToResourceType ||
			previous != nil && (expand.AnchorColumnID != previous.TerminalIdentityColumn || expand.AnchorNodeID != previous.TargetNodeID || expand.AnchorResourceType != previous.TargetResourceType) {
			return fmt.Errorf("related category stage endpoint is inconsistent")
		}
		previous = expand
		stageIndex++
	}
	if stageIndex != len(scan.Hops) || previous == nil || scan.Category.ActiveRecordColumn != previous.TerminalIdentityColumn ||
		scan.Category.TargetNodeID != previous.TargetNodeID || scan.Category.TargetResourceType != previous.TargetResourceType {
		return fmt.Errorf("related category field is not attached to the final related record")
	}
	categoryMatches := false
	for _, stage := range sequence.Stages {
		if stage.RelatedField != nil && relatedCategorySameField(scan.Category, *stage.RelatedField) {
			categoryMatches = true
		}
	}
	if !categoryMatches {
		return fmt.Errorf("related category descriptor does not match an authored related field")
	}
	if scan.Streaming {
		if len(scan.Hops) != 2 || len(scan.Hops[0]) != 1 || len(scan.Hops[1]) != 1 {
			return fmt.Errorf("streaming related category scan requires two single-hop stages")
		}
		for _, stage := range sequence.Stages {
			if stage.RelatedExpand != nil && stage.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
				return fmt.Errorf("streaming related category scan requires PRESERVE_PARENT")
			}
		}
	}
	return nil
}

func relatedCategorySameRoute(left, right ir.PhysicalStageRelatedRouteStep) bool {
	return left.StepIndex == right.StepIndex && left.FromOccurrenceID == right.FromOccurrenceID && left.EdgeID == right.EdgeID && left.FromNodeID == right.FromNodeID && left.ToNodeID == right.ToNodeID && left.ToOccurrenceID == right.ToOccurrenceID && left.FromResourceType == right.FromResourceType && left.ToResourceType == right.ToResourceType && left.Relationship == right.Relationship && left.StorageDirection == right.StorageDirection && left.MatchMode == right.MatchMode
}

func relatedCategorySameTraversal(left, right ir.PhysicalTraversal) bool {
	return left.SourceVariable == right.SourceVariable && left.TargetVariable == right.TargetVariable && left.EdgeVariable == right.EdgeVariable && left.Direction == right.Direction && left.EdgeCollectionBindKey == right.EdgeCollectionBindKey && left.EdgeLabelBindKey == right.EdgeLabelBindKey && left.SourceTypeBindKey == right.SourceTypeBindKey && left.TargetTypeBindKey == right.TargetTypeBindKey && left.EdgeTargetTypeField == right.EdgeTargetTypeField && left.Strategy == right.Strategy && left.EndpointField == right.EndpointField && left.EndpointJoinField == right.EndpointJoinField && relatedCategorySameStrings(left.EndpointIndexFields, right.EndpointIndexFields)
}

func relatedCategorySameField(left, right ir.PhysicalStageRelatedField) bool {
	return left.ActiveRecordColumn == right.ActiveRecordColumn && left.CandidateID == right.CandidateID && left.TargetNodeID == right.TargetNodeID && left.TargetResourceType == right.TargetResourceType && left.OutputColumnID == right.OutputColumnID && left.LogicalType == right.LogicalType && left.Nullable == right.Nullable && relatedCategorySameStrings(left.Path, right.Path)
}

func relatedCategorySameStrings(left, right []string) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func relatedCategoryTraversalDirection(storage string) ir.PhysicalTraversalDirection {
	if storage == "INBOUND" {
		return ir.PhysicalInbound
	}
	if storage == "OUTBOUND" {
		return ir.PhysicalOutbound
	}
	return ""
}

func relatedCategoryBindVars(plan ir.PhysicalPlan, scan ir.PhysicalRelatedCategoryScan, maxValues int) (map[string]any, string, error) {
	binds := map[string]any{}
	for _, key := range []string{"project", "dataset_generation", "auth_resource_paths_unrestricted", "auth_resource_paths"} {
		if err := relatedCategoryPhysicalBind(plan, binds, key, false); err != nil {
			return nil, "", err
		}
	}
	if err := relatedCategoryPhysicalBind(plan, binds, scan.Root.CollectionBindKey, true); err != nil {
		return nil, "", err
	}
	hops := flattenRelatedCategoryHops(scan.Hops)
	for _, hop := range hops {
		for _, key := range []string{hop.Traversal.EdgeLabelBindKey, hop.Traversal.TargetTypeBindKey} {
			if err := relatedCategoryPhysicalBind(plan, binds, key, false); err != nil {
				return nil, "", err
			}
		}
		if !scan.Streaming {
			if err := relatedCategoryPhysicalBind(plan, binds, hop.Traversal.SourceTypeBindKey, false); err != nil {
				return nil, "", err
			}
		}
		if err := relatedCategoryPhysicalBind(plan, binds, hop.Traversal.EdgeCollectionBindKey, true); err != nil {
			return nil, "", err
		}
	}
	targetCollectionBind := ""
	if !scan.Streaming {
		targetCollectionBind = relatedCategoryTargetCollectionBindKey(plan.BindVars, binds)
		binds["@"+targetCollectionBind] = scan.Category.TargetResourceType
	}
	if scan.Streaming && relatedCategoryValueQuantityCodePath(scan.Category.Path) {
		if len(scan.Hops) != 2 || len(scan.Hops[1]) != 1 {
			return nil, "", fmt.Errorf("projected related category scan requires the two-hop streaming shape")
		}
		targetCollection, ok := plan.BindVars[scan.Hops[1][0].Traversal.TargetTypeBindKey].(string)
		if !ok || targetCollection != scan.Category.TargetResourceType {
			return nil, "", fmt.Errorf("related category target collection does not match its typed target resource")
		}
		targetCollectionBind = relatedCategoryTargetCollectionBindKey(plan.BindVars, binds)
		binds["@"+targetCollectionBind] = targetCollection
	}
	_, sourceLimitCollision := plan.BindVars[relatedCategoryLimitBind]
	if _, exists := binds[relatedCategoryLimitBind]; scan.Streaming && (exists || sourceLimitCollision) {
		return nil, "", fmt.Errorf("category scan bind %q is already defined", relatedCategoryLimitBind)
	}
	binds[relatedCategoryLimitBind] = maxValues + 1
	return binds, targetCollectionBind, nil
}

func RenderRelatedCategoryScan(plan ir.PhysicalPlan, scan ir.PhysicalRelatedCategoryScan, maxValues int) (RenderedPhysicalPlan, error) {
	if maxValues < 1 || maxValues > relatedCategoryMaxValues {
		return RenderedPhysicalPlan{}, fmt.Errorf("invalid category scan limit")
	}
	if scan.Root.Variable == "" || scan.Root.CollectionBindKey == "" || len(scan.Hops) == 0 || scan.Category.TargetResourceType == "" || len(scan.Category.Path) == 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("incomplete related category scan descriptor")
	}
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate related category source plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate related category source scope: %w", err)
	}
	if _, ok := plan.BindVars[scan.Root.CollectionBindKey].(string); !ok || plan.BindVars[scan.Root.CollectionBindKey] != plan.Source.ResourceType ||
		plan.Source.ResourceType == "" || scan.Root.Population != nil || scan.Root.CohortSource != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("related category root does not match its exact source collection")
	}
	rootMatches := 0
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil &&
			operation.RootScan.Variable == scan.Root.Variable && operation.RootScan.CollectionBindKey == scan.Root.CollectionBindKey {
			rootMatches++
		}
	}
	if rootMatches != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related category root descriptor does not match the source plan")
	}
	for _, segment := range scan.Category.Path {
		if !previewSourcePathSegmentPattern.MatchString(segment) {
			return RenderedPhysicalPlan{}, fmt.Errorf("related category path contains an unsafe segment")
		}
	}
	if !scan.Streaming && !rootIndexHintPattern.MatchString(scan.IndexName) {
		return RenderedPhysicalPlan{}, fmt.Errorf("related category index hint is invalid")
	}
	if err := validateRelatedCategoryDescriptor(plan, scan); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if scan.Streaming && (len(scan.Hops) != 2 || len(scan.Hops[0]) != 1 || len(scan.Hops[1]) != 1) {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming related category scan requires two single-hop stages")
	}
	binds, targetCollectionBind, err := relatedCategoryBindVars(plan, scan, maxValues)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	var query string
	if scan.Streaming {
		query, err = renderRelatedCategoryStreamingScan(scan, targetCollectionBind)
	} else {
		if scan.IndexName == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("related category scan index hint is required")
		}
		query, err = renderRelatedCategoryScan(scan, targetCollectionBind)
	}
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	return RenderedPhysicalPlan{Query: query, BindVars: binds}, nil
}

func (r *physicalPlanRenderer) renderConstructionRelatedExpandStage(stage ir.PhysicalConstructionStage, previewLimitBindKey string) ([]string, error) {
	related := stage.RelatedExpand
	if related == nil {
		return nil, fmt.Errorf("related expansion stage is missing its typed payload")
	}
	records, err := r.renderSubplan(related.RelatedRecords, "  ", false)
	if err != nil {
		return nil, fmt.Errorf("render exact related-resource route: %w", err)
	}
	identityColumnBind := r.newInternalBindKey("related_expand_parent_identity_column")
	r.bindVars[identityColumnBind] = related.ParentIdentityColumn
	lines := make([]string, 0, 12)
	if previewLimitBindKey != "" {
		rawRecordsVariable := r.newInternalVariable("related_expand_raw_records")
		candidateVariable := r.newInternalVariable("related_expand_preview_candidate")

		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", rawRecordsVariable, records),
			fmt.Sprintf("  LET %s = (", related.RelatedRecordsVariable),
			fmt.Sprintf("    FOR %s IN %s", candidateVariable, rawRecordsVariable),
			fmt.Sprintf("      SORT %s ASC", constructionRelatedExpandIdentityExpression(stage.InputRowVariable, identityColumnBind, related.ConstructionIDBindKey, candidateVariable)),
			fmt.Sprintf("      LIMIT @%s", previewLimitBindKey),
			fmt.Sprintf("      RETURN %s", candidateVariable),
			"  )",
		)
	} else {
		lines = append(lines, fmt.Sprintf("  LET %s = %s", related.RelatedRecordsVariable, records))
	}
	errorMessage := fmt.Sprintf(
		"CONCAT(\"%s: related expansion construction \", @%s, \" has no related records for row \", %s[@%s])",
		dataframeerrors.CodeConstructionExpansionEmpty, related.ConstructionIDBindKey, stage.InputRowVariable, identityColumnBind,
	)
	if related.EmptyPolicy == ir.PhysicalUnnestError {
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(LENGTH(%s) > 0, %s)", related.RelatedRecordsVariable, errorMessage))
	}
	indices := fmt.Sprintf("LENGTH(%s) == 0 ? [] : RANGE(0, LENGTH(%s) - 1)", related.RelatedRecordsVariable, related.RelatedRecordsVariable)
	if related.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		indices = fmt.Sprintf("LENGTH(%s) == 0 ? [null] : RANGE(0, LENGTH(%s) - 1)", related.RelatedRecordsVariable, related.RelatedRecordsVariable)
	}
	lines = append(lines,
		fmt.Sprintf("  FOR %s IN (%s)", related.IndexVariable, indices),
		fmt.Sprintf("  LET %s = (%s == null ? null : %s[%s])", related.ItemVariable, related.IndexVariable, related.RelatedRecordsVariable, related.IndexVariable),
		fmt.Sprintf("  LET %s = %s", related.IdentityVariable, constructionRelatedExpandIdentityExpression(stage.InputRowVariable, identityColumnBind, related.ConstructionIDBindKey, related.ItemVariable)),
	)
	object, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("render related expansion output row: %w", err)
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, object),
		"  RETURN "+stage.OutputRowVariable,
	)
	return lines, nil
}
