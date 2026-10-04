package compiler

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
)

const relatedCategoryTargetCollectionBind = "__loom_related_category_target_collection"

type relatedCategoryHop struct {
	traversal ir.PhysicalTraversal
	route     ir.PhysicalStageRelatedRouteStep
}

type relatedCategoryPlan struct {
	root        *ir.PhysicalRootScan
	rootVar     string
	rootType    string
	expands     []ir.PhysicalConstructionStage
	hopsByStage [][]relatedCategoryHop
	fieldByID   map[string]ir.PhysicalConstructionStage
	category    ir.PhysicalStageRelatedField
	value       ir.PhysicalStageRelatedField
	index       *PreviewCoveringIndexSpec
}

// compileRelatedCategoryScan accepts only a direct root source with its
// canonical project, generation, and authorization scope, followed by a
// connected chain of PRESERVE_PARENT related expansions and direct scalar
// related-field helpers. All other plans use the existing category renderer.
func compileRelatedCategoryScan(
	plan ir.PhysicalPlan,
	stageID, categoryColumnID, valueColumnID string,
	category, value lower.CompiledOutputColumn,
	maxValues int,
) (string, map[string]any, *PreviewCoveringIndexSpec, bool, error) {
	shape, eligible := relatedCategoryScanShape(plan, stageID, categoryColumnID, valueColumnID, category, value)
	if !eligible {
		return "", nil, nil, false, nil
	}
	query, bindVars, err := renderRelatedCategoryScan(plan, shape, maxValues)
	if err != nil {
		return "", nil, nil, true, err
	}
	return query, bindVars, shape.index, true, nil
}

func relatedCategoryScanShape(
	plan ir.PhysicalPlan,
	stageID, categoryColumnID, valueColumnID string,
	category, value lower.CompiledOutputColumn,
) (relatedCategoryPlan, bool) {
	sequence := plan.StageSequence
	if sequence == nil || stageID == "" || sequence.FinalStageID != stageID ||
		len(sequence.Stages) < 2 || sequence.SourceRowIdentity != "_key" ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.OutputAuthResourcePathBindKey != "" || categoryColumnID == "" || valueColumnID == "" ||
		category.ID != categoryColumnID || value.ID != valueColumnID || category.ID == value.ID ||
		!categoryScanDirectScalarColumn(category) || !categoryScanDirectScalarColumn(value) {
		return relatedCategoryPlan{}, false
	}
	if scopeAllowed, ok := plan.BindVars["scope_allowed"].(bool); !ok || !scopeAllowed {
		return relatedCategoryPlan{}, false
	}
	if _, ok := plan.BindVars["auth_resource_paths_unrestricted"].(bool); !ok {
		return relatedCategoryPlan{}, false
	}
	if _, ok := plan.BindVars["auth_resource_paths"].([]string); !ok {
		return relatedCategoryPlan{}, false
	}

	root, rootVar, rootType, ok := relatedCategorySource(plan)
	if !ok || root.Population != nil || root.CohortSource != nil {
		return relatedCategoryPlan{}, false
	}
	shape := relatedCategoryPlan{
		root: root, rootVar: rootVar, rootType: rootType,
		fieldByID: make(map[string]ir.PhysicalConstructionStage),
	}
	priorStageID := sequence.SourceStageID
	var previousExpand *ir.PhysicalStageRelatedExpand
	inFieldHelpers := false
	for _, stage := range sequence.Stages {
		if stage.ID == "" || stage.InputStageID != priorStageID || stage.Filter != nil ||
			len(stage.DerivedLets) != 0 {
			return relatedCategoryPlan{}, false
		}
		switch stage.Kind {
		case ir.PhysicalStageRelatedExpandOp:
			if inFieldHelpers || stage.RelatedExpand == nil ||
				stage.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
				return relatedCategoryPlan{}, false
			}
			expand := stage.RelatedExpand
			if len(expand.Route) == 0 || expand.AnchorKind == "" ||
				expand.RelatedRecordsVariable == "" || expand.TerminalIdentityColumn == "" ||
				expand.TargetNodeID == "" || expand.TargetResourceType == "" {
				return relatedCategoryPlan{}, false
			}
			if previousExpand == nil {
				if expand.AnchorKind != "root" || expand.AnchorColumnID != "_key" ||
					expand.AnchorResourceType != rootType {
					return relatedCategoryPlan{}, false
				}
			} else if expand.AnchorKind != "activeRelatedRecord" ||
				expand.AnchorColumnID != previousExpand.TerminalIdentityColumn ||
				expand.AnchorNodeID != previousExpand.TargetNodeID ||
				expand.AnchorResourceType != previousExpand.TargetResourceType {
				return relatedCategoryPlan{}, false
			}
			hops, ok := categoryRelatedStageHops(plan, stage)
			if !ok {
				return relatedCategoryPlan{}, false
			}
			shape.expands = append(shape.expands, stage)
			shape.hopsByStage = append(shape.hopsByStage, hops)
			previousExpand = expand
		case ir.PhysicalStageRelatedFieldOp:
			inFieldHelpers = true
			field := stage.RelatedField
			if field == nil || previousExpand == nil ||
				field.ActiveRecordColumn != previousExpand.TerminalIdentityColumn ||
				field.TargetNodeID != previousExpand.TargetNodeID ||
				field.TargetResourceType != previousExpand.TargetResourceType ||
				field.OutputColumnID == "" || field.LogicalType == "" ||
				!field.Nullable || len(field.Path) < 2 || field.Path[0] != "payload" ||
				!validPreviewIndexPath(field.Path) {
				return relatedCategoryPlan{}, false
			}
			if _, duplicate := shape.fieldByID[field.OutputColumnID]; duplicate {
				return relatedCategoryPlan{}, false
			}
			if !categoryRelatedFieldProjection(stage, *field) {
				return relatedCategoryPlan{}, false
			}
			shape.fieldByID[field.OutputColumnID] = stage
		default:
			return relatedCategoryPlan{}, false
		}
		priorStageID = stage.ID
	}
	if len(shape.expands) == 0 || len(shape.fieldByID) == 0 {
		return relatedCategoryPlan{}, false
	}
	categoryStage, categoryFound := shape.fieldByID[categoryColumnID]
	valueStage, valueFound := shape.fieldByID[valueColumnID]
	if !categoryFound || !valueFound ||
		categoryStage.RelatedField == nil || valueStage.RelatedField == nil ||
		categoryStage.RelatedField.ActiveRecordColumn != valueStage.RelatedField.ActiveRecordColumn ||
		categoryStage.RelatedField.TargetNodeID != valueStage.RelatedField.TargetNodeID ||
		categoryStage.RelatedField.TargetResourceType != valueStage.RelatedField.TargetResourceType ||
		categoryStage.RelatedField.OutputColumnID != category.ID ||
		valueStage.RelatedField.OutputColumnID != value.ID ||
		category.Name == "" || value.Name == "" ||
		category.Kind != categoryStage.RelatedField.LogicalType ||
		value.Kind != valueStage.RelatedField.LogicalType ||
		category.Cardinality != string(expression.OptionalOne) ||
		value.Cardinality != string(expression.OptionalOne) ||
		!category.Nullable || !value.Nullable {
		return relatedCategoryPlan{}, false
	}
	finalExpand := shape.expands[len(shape.expands)-1].RelatedExpand
	if categoryStage.RelatedField.ActiveRecordColumn != finalExpand.TerminalIdentityColumn {
		return relatedCategoryPlan{}, false
	}
	lastHop := shape.hopsByStage[len(shape.hopsByStage)-1]
	if len(lastHop) == 0 || lastHop[len(lastHop)-1].route.ToResourceType != categoryStage.RelatedField.TargetResourceType ||
		lastHop[len(lastHop)-1].route.ToNodeID != categoryStage.RelatedField.TargetNodeID {
		return relatedCategoryPlan{}, false
	}
	shape.category = *categoryStage.RelatedField
	shape.value = *valueStage.RelatedField
	collection := categoryStage.RelatedField.TargetResourceType
	path := strings.Join(categoryStage.RelatedField.Path, ".")
	fields := []string{"project", "dataset_generation", "resourceType", path, "auth_resource_path"}
	legacyFields := []string{"project", "dataset_generation", path, "auth_resource_path"}
	shape.index = &PreviewCoveringIndexSpec{
		Collection: collection,
		Name:       previewCoveringIndexName(collection, fields),
		Fields:     fields,
		Supersedes: &PreviewCoveringIndexReplacement{
			Name:   previewCoveringIndexName(collection, legacyFields),
			Fields: legacyFields,
		},
	}
	return shape, true
}

func relatedCategorySource(plan ir.PhysicalPlan) (*ir.PhysicalRootScan, string, string, bool) {
	operations := plan.Operations
	if len(operations) != 6 || operations[0].Kind != ir.PhysicalRootScanOp ||
		operations[0].RootScan == nil || operations[0].RootScan.Variable == "" ||
		operations[0].RootScan.CollectionBindKey == "" || operations[0].Source.ResourceType == "" ||
		operations[0].Source.ResourceType != plan.Source.ResourceType ||
		plan.BindVars[operations[0].RootScan.CollectionBindKey] != plan.Source.ResourceType ||
		!categoryRelatedSingleAuthScope(operations[1:5], operations[0].RootScan.Variable) ||
		operations[5].Kind != ir.PhysicalReturnOp || operations[5].Return == nil {
		return nil, "", "", false
	}
	if _, ok := plan.BindVars["project"].(string); !ok {
		return nil, "", "", false
	}
	return operations[0].RootScan, operations[0].RootScan.Variable, plan.Source.ResourceType, true
}

func categoryRelatedStageHops(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage) ([]relatedCategoryHop, bool) {
	expand := stage.RelatedExpand
	subplan := expand.RelatedRecords
	if len(subplan.Captures) != 1 || subplan.Captures[0] != stage.InputRowVariable ||
		!subplan.Unique || subplan.Sort == nil || subplan.Sort.Variable == "" ||
		len(subplan.Operations) == 0 || subplan.Return.Kind != ir.PhysicalObjectExpression ||
		subplan.Return.Object == nil || len(subplan.Return.Object.Fields) != 2 {
		return nil, false
	}
	first := subplan.Operations[0]
	position := 0
	anchorVariable := ""
	switch expand.AnchorKind {
	case "root":
		if first.Kind != ir.PhysicalCollectionScanOp || first.CollectionScan == nil ||
			first.CollectionScan.CollectionBindKey != "root_collection" ||
			plan.BindVars[first.CollectionScan.CollectionBindKey] != expand.AnchorResourceType ||
			first.Source.ResourceType != expand.AnchorResourceType ||
			len(subplan.Operations) < 6 ||
			!categoryRelatedPhysicalFilterEquals(subplan.Operations[1],
				ir.PhysicalValue{Variable: first.CollectionScan.Variable, Path: []string{"_key"}},
				ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{expand.AnchorColumnID}}) {
			return nil, false
		}
		anchorVariable = first.CollectionScan.Variable
		position = 2
	case "activeRelatedRecord":
		if first.Kind != ir.PhysicalDocumentLookupOp || first.DocumentLookup == nil ||
			first.DocumentLookup.Variable == "" ||
			first.DocumentLookup.CollectionBindKey == "" ||
			plan.BindVars[first.DocumentLookup.CollectionBindKey] != expand.AnchorResourceType ||
			first.DocumentLookup.ExactID.Variable != stage.InputRowVariable ||
			!reflect.DeepEqual(first.DocumentLookup.ExactID.Path, []string{expand.AnchorColumnID}) ||
			first.Source.ResourceType != expand.AnchorResourceType || len(subplan.Operations) < 5 {
			return nil, false
		}
		anchorVariable = first.DocumentLookup.Variable
		position = 1
	default:
		return nil, false
	}
	if anchorVariable == "" || !categoryRelatedSingleAuthScope(subplan.Operations[position:position+4], anchorVariable) {
		return nil, false
	}
	position += 4
	currentVariable := anchorVariable
	hops := make([]relatedCategoryHop, 0, len(expand.Route))
	for index, route := range expand.Route {
		if position+7 > len(subplan.Operations) ||
			route.EdgeID == "" || route.FromNodeID == "" || route.ToNodeID == "" ||
			route.FromResourceType == "" || route.ToResourceType == "" ||
			route.Relationship == "" ||
			(route.StorageDirection != "OUTBOUND" && route.StorageDirection != "INBOUND") ||
			(route.MatchMode != "OPTIONAL" && route.MatchMode != "REQUIRED") ||
			index > 0 && (expand.Route[index-1].ToNodeID != route.FromNodeID ||
				expand.Route[index-1].ToResourceType != route.FromResourceType) {
			return nil, false
		}
		operation := subplan.Operations[position]
		if operation.Kind != ir.PhysicalTraversalOp || operation.Traversal == nil {
			return nil, false
		}
		traversal := *operation.Traversal
		direction := ir.PhysicalInbound
		if route.StorageDirection == "OUTBOUND" {
			direction = ir.PhysicalOutbound
		}
		sourceType, sourceTypeOK := plan.BindVars[traversal.SourceTypeBindKey].(string)
		targetType, targetTypeOK := plan.BindVars[traversal.TargetTypeBindKey].(string)
		label, labelOK := plan.BindVars[traversal.EdgeLabelBindKey].(string)
		edgeCollection, collectionOK := plan.BindVars[traversal.EdgeCollectionBindKey].(string)
		if !sourceTypeOK || !targetTypeOK || !labelOK || !collectionOK ||
			traversal.SourceVariable != currentVariable || traversal.Direction != direction ||
			sourceType != route.FromResourceType || targetType != route.ToResourceType ||
			label != route.Relationship || edgeCollection != "fhir_edge" ||
			operation.Source.SemanticNode != route.ToNodeID ||
			operation.Source.ResourceType != route.ToResourceType ||
			operation.Source.Relationship != route.Relationship ||
			traversal.EdgeVariable == "" || traversal.TargetVariable == "" ||
			traversal.EdgeTargetTypeField != categoryRelatedTargetTypeField(direction) ||
			!categoryRelatedTraversalScope(subplan.Operations[position+1:position+7], traversal) {
			return nil, false
		}
		hops = append(hops, relatedCategoryHop{traversal: traversal, route: route})
		currentVariable = traversal.TargetVariable
		position += 7
	}
	if len(hops) != len(expand.Route) || position != len(subplan.Operations) ||
		subplan.Return.Object.Fields[0].Name != "terminal_id" ||
		!categoryRelatedExpressionValue(subplan.Return.Object.Fields[0].Expression, currentVariable, "_id") ||
		subplan.Return.Object.Fields[1].Name != "resource_id" ||
		!categoryRelatedExpressionValue(subplan.Return.Object.Fields[1].Expression, currentVariable, "id") ||
		expand.Route[0].FromNodeID != expand.AnchorNodeID ||
		expand.Route[0].FromResourceType != expand.AnchorResourceType ||
		expand.Route[len(expand.Route)-1].ToNodeID != expand.TargetNodeID ||
		expand.Route[len(expand.Route)-1].ToResourceType != expand.TargetResourceType ||
		subplan.Sort.Variable != currentVariable || !reflect.DeepEqual(subplan.Sort.Path, []string{"_id"}) {
		return nil, false
	}
	return hops, true
}

func categoryRelatedFieldProjection(stage ir.PhysicalConstructionStage, field ir.PhysicalStageRelatedField) bool {
	outputName := ""
	for _, column := range stage.OutputColumns {
		if column.ID != field.OutputColumnID {
			continue
		}
		if outputName != "" || column.Name == "" || column.Internal {
			return false
		}
		outputName = column.Name
	}
	if outputName == "" {
		return false
	}
	found := false
	for _, projection := range stage.OutputProjections {
		if projection.Name != outputName {
			continue
		}
		if found || projection.Expression == nil ||
			projection.Expression.Kind != ir.PhysicalRelatedFieldExpression ||
			projection.Expression.RelatedField == nil {
			return false
		}
		related := projection.Expression.RelatedField
		if !reflect.DeepEqual(related.DocumentID,
			ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{field.ActiveRecordColumn}}) ||
			related.ResourceType != field.TargetResourceType ||
			!reflect.DeepEqual(related.Path, field.Path) ||
			projection.Expression.Cardinality != ir.PhysicalScalarCardinality ||
			projection.Expression.NullBehavior != ir.PhysicalPreserveNull {
			return false
		}
		found = true
	}
	return found
}

func categoryRelatedSingleAuthScope(operations []ir.PhysicalOperation, variable string) bool {
	if len(operations) != 4 ||
		!categoryRelatedPhysicalFilterEquals(operations[0],
			ir.PhysicalValue{Variable: variable, Path: []string{"project"}},
			ir.PhysicalValue{BindKey: "project"}) ||
		!categoryRelatedPhysicalFilterEquals(operations[1],
			ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}},
			ir.PhysicalValue{BindKey: "dataset_generation"}) {
		return false
	}
	let := operations[2].DerivedLet
	if operations[2].Kind != ir.PhysicalDerivedLetOp || let == nil ||
		!strings.EqualFold(strings.TrimSpace(let.Operator), "AUTH_RESOURCE_PATH_ALLOWED") ||
		len(let.Inputs) != 3 ||
		!reflect.DeepEqual(let.Inputs[0], ir.PhysicalValue{Variable: variable, Path: []string{"auth_resource_path"}}) ||
		!reflect.DeepEqual(let.Inputs[1], ir.PhysicalValue{BindKey: "auth_resource_paths"}) ||
		!reflect.DeepEqual(let.Inputs[2], ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"}) {
		return false
	}
	return categoryRelatedPhysicalFilterEquals(operations[3],
		ir.PhysicalValue{Variable: let.Variable},
		ir.PhysicalValue{BindKey: "scope_allowed"})
}

func categoryRelatedTraversalScope(operations []ir.PhysicalOperation, traversal ir.PhysicalTraversal) bool {
	if len(operations) != 6 ||
		!categoryRelatedPhysicalFilterEquals(operations[0],
			ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"project"}},
			ir.PhysicalValue{BindKey: "project"}) ||
		!categoryRelatedPhysicalFilterEquals(operations[1],
			ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"project"}},
			ir.PhysicalValue{BindKey: "project"}) ||
		!categoryRelatedPhysicalFilterEquals(operations[2],
			ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"dataset_generation"}},
			ir.PhysicalValue{BindKey: "dataset_generation"}) ||
		!categoryRelatedPhysicalFilterEquals(operations[3],
			ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"dataset_generation"}},
			ir.PhysicalValue{BindKey: "dataset_generation"}) {
		return false
	}
	let := operations[4].DerivedLet
	if operations[4].Kind != ir.PhysicalDerivedLetOp || let == nil ||
		!strings.EqualFold(strings.TrimSpace(let.Operator), "AUTH_RESOURCE_PATH_ALLOWED") ||
		len(let.Inputs) != 4 ||
		!reflect.DeepEqual(let.Inputs[0], ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"auth_resource_path"}}) ||
		!reflect.DeepEqual(let.Inputs[1], ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"auth_resource_path"}}) ||
		!reflect.DeepEqual(let.Inputs[2], ir.PhysicalValue{BindKey: "auth_resource_paths"}) ||
		!reflect.DeepEqual(let.Inputs[3], ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"}) {
		return false
	}
	return categoryRelatedPhysicalFilterEquals(operations[5],
		ir.PhysicalValue{Variable: let.Variable},
		ir.PhysicalValue{BindKey: "scope_allowed"})
}

func categoryRelatedPhysicalFilterEquals(operation ir.PhysicalOperation, left, right ir.PhysicalValue) bool {
	if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil ||
		operation.Filter.Expression != nil {
		return false
	}
	predicate := operation.Filter.Predicate
	return predicate.LeftExpression == nil && predicate.Correlation == nil && predicate.Quantifier == "" &&
		strings.EqualFold(strings.TrimSpace(predicate.Operator), "EQUALS") &&
		predicate.Right != nil && reflect.DeepEqual(predicate.Left, left) &&
		reflect.DeepEqual(*predicate.Right, right)
}

func categoryRelatedTargetTypeField(direction ir.PhysicalTraversalDirection) string {
	if direction == ir.PhysicalInbound {
		return "from_type"
	}
	return "to_type"
}

func categoryRelatedExpressionValue(expression ir.PhysicalExpression, variable, path string) bool {
	return expression.Kind == ir.PhysicalValueExpression && expression.Value != nil &&
		expression.Value.Variable == variable && expression.Value.BindKey == "" &&
		reflect.DeepEqual(expression.Value.Path, []string{path})
}

func renderRelatedCategoryScan(plan ir.PhysicalPlan, shape relatedCategoryPlan, maxValues int) (string, map[string]any, error) {
	if maxValues < 1 || maxValues > MaxCategoryScanValues {
		return "", nil, fmt.Errorf("invalid category scan limit")
	}
	binds := cloneCategoryScanBinds(plan.BindVars)
	targetBind := categoryScanUniqueBindKey(binds, relatedCategoryTargetCollectionBind)
	binds["@"+targetBind] = shape.category.TargetResourceType
	binds[categoryLimitBind] = maxValues + 1
	path := strings.Join(shape.category.Path, ".")
	hops := []relatedCategoryHop{}
	for _, stageHops := range shape.hopsByStage {
		hops = append(hops, stageHops...)
	}
	targetType := hops[len(hops)-1].traversal.TargetTypeBindKey
	hint := fmt.Sprintf(" OPTIONS { indexHint: %q, forceIndexHint: false }", shape.index.Name)
	categoryPathPresence := categoryRelatedPathPresenceExpression("candidate", shape.category.Path)
	terminalPathPresence := categoryRelatedPathPresenceExpression("terminal", shape.category.Path)
	scope := func(v string) string {
		return fmt.Sprintf("%s.project == @project AND %s.dataset_generation == @dataset_generation AND (@auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths)", v, v, v)
	}
	lines := []string{
		"LET __loom_related_category_candidates = (",
		fmt.Sprintf(" FOR candidate IN @@%s%s", targetBind, hint),
		" FILTER " + scope("candidate"),
		fmt.Sprintf(" FILTER candidate.resourceType == @%s", targetType),
		" LET candidate_present = " + categoryPathPresence,
		" COLLECT present = candidate_present, value = candidate." + path,
		" RETURN { present, value }", ")",
		"LET __loom_related_category_values = (",
		" FOR category IN __loom_related_category_candidates",
		" LET membership = (",
		fmt.Sprintf("  FOR terminal IN @@%s%s", targetBind, hint),
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
		if hop.traversal.Direction == ir.PhysicalInbound {
			targetEndpoint, sourceEndpoint = "_from", "_to"
		}
		lines = append(lines,
			fmt.Sprintf("  FOR %s IN @@%s", edge, hop.traversal.EdgeCollectionBindKey),
			fmt.Sprintf("  FILTER %s.%s == %s._id", edge, targetEndpoint, current),
			fmt.Sprintf("  FILTER %s.label == @%s AND %s.%s == @%s", edge, hop.traversal.EdgeLabelBindKey, edge, hop.traversal.EdgeTargetTypeField, hop.traversal.TargetTypeBindKey),
			"  FILTER "+scope(edge),
			fmt.Sprintf("  LET %s = DOCUMENT(%s.%s)", previous, edge, sourceEndpoint),
			"  FILTER "+previous+" != null AND "+scope(previous),
		)
		if i == 0 {
			lines = append(lines, fmt.Sprintf("  FILTER IS_SAME_COLLECTION(@%s, %s)", hop.traversal.SourceTypeBindKey, previous))
		} else {
			lines = append(lines, fmt.Sprintf("  FILTER %s.resourceType == @%s", previous, hop.traversal.SourceTypeBindKey))
		}
		current = previous
	}
	lines = append(lines, "  LIMIT 1", "  RETURN true", " )", " FILTER LENGTH(membership) > 0", " RETURN category", ")")
	emptyNames := []string{}
	prefix := []relatedCategoryHop{}
	for i, stageHops := range shape.hopsByStage {
		name := fmt.Sprintf("__loom_related_category_empty_stage_%d", i+1)
		emptyNames = append(emptyNames, name)
		rootVar := fmt.Sprintf("empty_root_%d", i)
		lines = append(lines,
			"LET "+name+" = (LENGTH((FOR existing_category IN __loom_related_category_values FILTER existing_category.present == true AND existing_category.value == null LIMIT 1 RETURN true)) > 0) ? [] : (",
			fmt.Sprintf(" FOR %s IN @@%s", rootVar, shape.root.CollectionBindKey), " FILTER "+scope(rootVar))
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
		" LIMIT @"+categoryLimitBind,
		" RETURN { present: category.present, value: category.value }")
	query := strings.Join(lines, "\n") + "\n"
	binds["@"+shape.root.CollectionBindKey] = plan.BindVars[shape.root.CollectionBindKey]
	delete(binds, shape.root.CollectionBindKey)
	for _, hop := range hops {
		binds["@"+hop.traversal.EdgeCollectionBindKey] = plan.BindVars[hop.traversal.EdgeCollectionBindKey]
		delete(binds, hop.traversal.EdgeCollectionBindKey)
	}
	return query, categoryScanPruneBindVars(binds, query), nil
}

func categoryRelatedPathPresenceExpression(variable string, path []string) string {
	current := variable
	checks := make([]string, 0, len(path))
	for _, segment := range path {
		checks = append(checks, "(IS_OBJECT("+current+") AND HAS("+current+", "+strconv.Quote(segment)+"))")
		current += "." + segment
	}
	return strings.Join(checks, " AND ")
}

func relatedCategoryForwardInline(hop relatedCategoryHop, source, edge, target, indent string, scope func(string) string) []string {
	sourceEndpoint, targetEndpoint := "_from", "_to"
	if hop.traversal.Direction == ir.PhysicalInbound {
		sourceEndpoint, targetEndpoint = "_to", "_from"
	}
	return []string{
		fmt.Sprintf("%sFOR %s IN @@%s", indent, edge, hop.traversal.EdgeCollectionBindKey),
		fmt.Sprintf("%sFILTER %s.%s == %s._id", indent, edge, sourceEndpoint, source),
		fmt.Sprintf("%sFILTER %s.label == @%s AND %s.%s == @%s", indent, edge, hop.traversal.EdgeLabelBindKey, edge, hop.traversal.EdgeTargetTypeField, hop.traversal.TargetTypeBindKey),
		indent + "FILTER " + scope(edge),
		fmt.Sprintf("%sLET %s = DOCUMENT(%s.%s)", indent, target, edge, targetEndpoint),
		fmt.Sprintf("%sFILTER %s != null AND %s.resourceType == @%s", indent, target, target, hop.traversal.TargetTypeBindKey),
		indent + "FILTER " + scope(target),
	}
}
