package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	sourceGroupPreviewKeyColumn = "__loom_preview_source_group_key"
	sourceGroupPreviewRootKey   = "__loom_preview_source_group_root_key"
)

type sourceGroupPreviewShape struct {
	keyType string
	unnest  ir.PhysicalUnnest
}

type sourceGroupPreviewFrontier struct {
	query            string
	bindVars         map[string]any
	rootKeysVariable string
}

// terminalSourceGroupPreviewWindowFor proves the source-Group shape whose
// first preview groups can be selected before expanding related rows. The
// frontier retains the exact root filters and key values, then checks each
// group for at least one scoped route witness. The canonical source and Group
// still compute all selected contributors, including duplicate edges/items.
func terminalSourceGroupPreviewWindowFor(
	plan, sourcePlan ir.PhysicalPlan,
	sequence *ir.PhysicalStageSequence,
	stages []ir.PhysicalConstructionStage,
	options physicalRenderOptions,
	hasRelatedGroupPreview bool,
) (ir.PhysicalPlan, sourceGroupPreviewShape, bool) {
	if plan.Engine == ir.PhysicalEngineClickHouse || hasRelatedGroupPreview || sequence == nil ||
		sequence.PreviewLimitBindKey == "" || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil ||
		sequence.OutputAuthResourcePathBindKey != "" || options.omitTerminalRowSort || options.omitTerminalReturn ||
		options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" || options.rootIndexHint != "" ||
		options.disableRootIndex || options.projectionPresenceMarkerColumn != "" || len(options.preserveProjectionPresenceNames) != 0 ||
		options.twoScanPivotPreview || options.dynamicPivotPreview || options.streamingMissingCategoryPivotPreview ||
		options.storedPresenceObject || options.previewRootKeyWindowVariable != "" || options.previewGroupIdentityFilter != nil ||
		options.pivotGroupTupleFilter != nil {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	if limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || limit <= 0 ||
		len(sequence.Stages) != 1 || len(stages) != 1 {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	stage := stages[0]
	group := stage.Group
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || len(group.Keys) != 1 ||
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup || group.RootContributorInputColumn != "" ||
		group.RootContributorOutputColumn != "" || group.RootContributorVariable != "" || group.RootContributorInputMany ||
		len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" ||
		group.Aggregates[0].InputColumn != "" || group.Aggregates[0].InputKind != "" || len(group.RowValues) != 0 ||
		group.Keys[0].Kind != "STRING" || stage.InputRowVariable == "" {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	if _, collision := physicalPlanVariableNames(sourcePlan)[stage.InputRowVariable]; collision {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	proofPlan := ir.ClonePhysicalPlan(sourcePlan)
	pruneUnusedSourceGroupProjections(&proofPlan, sequence)
	if len(proofPlan.Operations) < 7 || proofPlan.PreviewSourceWindowByRootID || proofPlan.Source.ResourceType == "" ||
		sourcePlan.BindVars["root_collection"] != sourcePlan.Source.ResourceType ||
		proofPlan.Operations[0].Kind != ir.PhysicalRootScanOp || proofPlan.Operations[0].RootScan == nil ||
		proofPlan.Operations[0].RootScan.Variable == "" || proofPlan.Operations[0].RootScan.Population != nil ||
		proofPlan.Operations[0].RootScan.CohortSource != nil || proofPlan.Operations[0].RootScan.PageCandidateSeed != nil ||
		proofPlan.Operations[0].RootScan.CollectionBindKey != "root_collection" ||
		proofPlan.Operations[5].Kind != ir.PhysicalUnnestOp || proofPlan.Operations[5].Unnest == nil ||
		proofPlan.Operations[len(proofPlan.Operations)-1].Kind != ir.PhysicalReturnOp || proofPlan.Operations[len(proofPlan.Operations)-1].Return == nil {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	root := proofPlan.Operations[0].RootScan.Variable
	layout, err := buildNavigationRenderLayout(proofPlan)
	if err != nil || len(layout.unnests) != 1 || len(layout.rootWindow) != 0 ||
		len(layout.unnests[0].Owner.Route) != 1 {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	for _, item := range layout.postWindow {
		if item.operation.Kind != ir.PhysicalExpressionLetOp {
			return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
		}
	}
	unnest := layout.unnests[0]
	if unnest.EmptyPolicy != ir.PhysicalUnnestPreserveParent || unnest.Owner.RootVariable != root ||
		unnest.Owner.OwnerVariable != unnest.Owner.Route[0].Traversal.TargetVariable ||
		unnest.Owner.Route[0].Traversal.SourceVariable != root ||
		validateGenericNavigationTraversal(sourcePlan, unnest.Owner.Route[0].Traversal) != nil {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	if _, err := validateGenericNavigationScopeBlock(unnest.Owner.Route[0].Scope,
		unnest.Owner.Route[0].Traversal.TargetVariable, unnest.Owner.Route[0].Traversal.EdgeVariable,
		unnest.Owner.Route[0].Traversal.TargetVariable); err != nil {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	keyColumn := ir.PhysicalStageColumn{}
	keyColumnFound := false
	for _, column := range stage.InputColumns {
		if column.Name == group.Keys[0].InputColumn {
			keyColumn, keyColumnFound = column, true
			break
		}
	}
	if !keyColumnFound || keyColumn.Internal || keyColumn.Kind != "string" ||
		(keyColumn.Cardinality != "required_one" && keyColumn.Cardinality != "optional_one") {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	var keyProjection ir.PhysicalProjection
	returnIndex := len(proofPlan.Operations) - 1
	for _, projection := range proofPlan.Operations[returnIndex].Return.Projections {
		if projection.Name == group.Keys[0].InputColumn {
			keyProjection = projection
			break
		}
	}
	keyPath, directRootScalar := directRootProjectionPath(keyProjection, root)
	if keyProjection.Name == "" || keyProjection.PresenceOutput || !directRootScalar {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	if keyProjection.Expression != nil && (keyProjection.Expression.NullBehavior != ir.PhysicalPreserveNull ||
		keyProjection.Expression.Extract == nil || keyProjection.Expression.Extract.ResourceType != sourcePlan.Source.ResourceType) {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	for _, segment := range keyPath {
		if !previewSourcePathSegmentPattern.MatchString(segment) {
			return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
		}
	}
	keyType, err := aqlTableScalarType(group.Keys[0].Kind)
	if err != nil {
		return ir.PhysicalPlan{}, sourceGroupPreviewShape{}, false
	}
	frontierPlan := ir.ClonePhysicalPlan(proofPlan)
	frontierKeyProjection := keyProjection
	frontierKeyProjection.Name = sourceGroupPreviewKeyColumn
	frontierPlan.Operations = append(
		append([]ir.PhysicalOperation(nil), frontierPlan.Operations[:5]...),
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			frontierKeyProjection,
			{Name: sourceGroupPreviewRootKey, Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}},
		}}},
	)
	return frontierPlan, sourceGroupPreviewShape{keyType: keyType, unnest: unnest}, true
}

func renderSourceGroupPreviewFrontier(
	frontierPlan ir.PhysicalPlan,
	shape sourceGroupPreviewShape,
	sequence *ir.PhysicalStageSequence,
	collectionKeys map[string]struct{},
) (sourceGroupPreviewFrontier, error) {
	source, err := renderPhysicalPlanWithOptions(frontierPlan, physicalRenderOptions{internalPrefix: "source_group_frontier_source_"})
	if err != nil {
		return sourceGroupPreviewFrontier{}, fmt.Errorf("render source Group-key frontier: %w", err)
	}
	bindVars := make(map[string]any, len(source.BindVars)+len(collectionKeys)+8)
	for key, value := range source.BindVars {
		bindVars[key] = clonePhysicalBindValue(value)
	}
	for key, value := range runtimePhysicalBindVars(frontierPlan.BindVars, collectionKeys) {
		if existing, found := bindVars[key]; found && !sameRenderPhysicalBindValue(existing, value) {
			return sourceGroupPreviewFrontier{}, fmt.Errorf("source Group frontier bind variable %q conflicts across scans", key)
		}
		bindVars[key] = clonePhysicalBindValue(value)
	}
	reserved := stageSequenceVariableNames(ir.PhysicalPlan{Operations: frontierPlan.Operations, StageSequence: sequence})
	for variable := range physicalPlanVariableNames(frontierPlan) {
		reserved[variable] = struct{}{}
	}
	for _, variable := range []string{
		shape.unnest.OutputVariable, shape.unnest.HasItemVariable, shape.unnest.Ordinality,
		shape.unnest.Owner.OwnerVariable,
		shape.unnest.Owner.Route[0].Traversal.SourceVariable,
		shape.unnest.Owner.Route[0].Traversal.TargetVariable,
		shape.unnest.Owner.Route[0].Traversal.EdgeVariable,
	} {
		if variable != "" {
			reserved[variable] = struct{}{}
		}
	}
	for _, operation := range shape.unnest.Owner.Route[0].Scope {
		if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet != nil {
			reserved[operation.DerivedLet.Variable] = struct{}{}
		}
	}
	renderer := physicalPlanRenderer{bindVars: bindVars, collectionKeys: collectionKeys, reservedVars: reserved, internalPrefix: "construction_source_group_preview_"}
	frontierRows := renderer.newInternalVariable("rows")
	groupRoots := renderer.newInternalVariable("roots_by_key")
	selectedGroups := renderer.newInternalVariable("selected_groups")
	selectedRootKeys := renderer.newInternalVariable("selected_root_keys")
	row := renderer.newInternalVariable("row")
	group := renderer.newInternalVariable("group")
	rootKey := renderer.newInternalVariable("root_key")
	badGroups := renderer.newInternalVariable("bad_key_groups")
	badGroup := renderer.newInternalVariable("bad_key_group")
	badRootKey := renderer.newInternalVariable("bad_key_root_key")
	badRoot := renderer.newInternalVariable("bad_key_root")
	eligibleRootKey := renderer.newInternalVariable("eligible_root_key")
	eligibleRoot := renderer.newInternalVariable("eligible_root")
	keyTypeBind := renderer.newInternalBindKey("group_key_type")
	renderer.bindVars[keyTypeBind] = shape.keyType
	if _, found := renderer.bindVars[sequence.PreviewLimitBindKey]; !found {
		value, ok := frontierPlan.BindVars[sequence.PreviewLimitBindKey]
		if !ok {
			return sourceGroupPreviewFrontier{}, fmt.Errorf("preview Group limit bind %q is missing", sequence.PreviewLimitBindKey)
		}
		renderer.bindVars[sequence.PreviewLimitBindKey] = clonePhysicalBindValue(value)
	}
	traversal := shape.unnest.Owner.Route[0].Traversal
	routeForRoot := func(rootVariable string, indent string) ([]string, error) {
		lines := renderer.renderTraversalScan(traversal, rootVariable, indent)
		for index, operation := range shape.unnest.Owner.Route[0].Scope {
			rendered, renderErr := renderer.renderScopeOperation(operation, indent+"  ")
			if renderErr != nil {
				return nil, fmt.Errorf("render scoped source Group route operation %d: %w", index, renderErr)
			}
			lines = append(lines, rendered...)
		}
		return lines, nil
	}
	renderWitness := func(rootKeyVariable, rootVariable, prefix string) ([]string, error) {
		lines := []string{fmt.Sprintf("FOR %s IN %s.root_keys", rootKeyVariable, prefix),
			fmt.Sprintf("LET %s = DOCUMENT(@@root_collection, %s)", rootVariable, rootKeyVariable),
			fmt.Sprintf("FILTER %s != null", rootVariable)}
		routeLines, routeErr := routeForRoot(rootVariable, "  ")
		if routeErr != nil {
			return nil, routeErr
		}
		lines = append(lines, routeLines...)
		lines = append(lines, "  LIMIT 1", "  RETURN 1")
		return lines, nil
	}
	badWitness, err := renderWitness(badRootKey, badRoot, badGroup)
	if err != nil {
		return sourceGroupPreviewFrontier{}, err
	}
	selectedWitness, err := renderWitness(eligibleRootKey, eligibleRoot, group)
	if err != nil {
		return sourceGroupPreviewFrontier{}, err
	}
	lines := []string{
		fmt.Sprintf("LET %s = (", frontierRows),
	}
	lines = appendIndented(lines, strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n"))
	lines = append(lines,
		")",
		fmt.Sprintf("LET %s = (", groupRoots),
		fmt.Sprintf("  FOR %s IN %s", row, frontierRows),
		fmt.Sprintf("  COLLECT %s = %s.%s INTO %s = %s.%s", group, row, sourceGroupPreviewKeyColumn, rootKey, row, sourceGroupPreviewRootKey),
		fmt.Sprintf("  RETURN {%s: %s, root_keys: %s}", "group_key", group, rootKey),
		")",
		fmt.Sprintf("LET %s = (", badGroups),
		fmt.Sprintf("  FOR %s IN %s", badGroup, groupRoots),
		fmt.Sprintf("  FILTER %s.group_key != null", badGroup),
		fmt.Sprintf("  FILTER TYPENAME(%s.group_key) != @%s", badGroup, keyTypeBind),
		fmt.Sprintf("  FILTER LENGTH(("),
	)
	lines = appendIndented(lines, badWitness)
	lines = append(lines,
		"  )) > 0",
		"  LIMIT 1",
		"  RETURN 1",
		")",
		fmt.Sprintf("LET %s = (", selectedGroups),
		fmt.Sprintf("  FOR %s IN %s", group, groupRoots),
		fmt.Sprintf("  FILTER ASSERT(LENGTH(%s) == 0, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", badGroups),
		fmt.Sprintf("  SORT %s.group_key ASC", group),
		fmt.Sprintf("  FILTER LENGTH(("),
	)
	lines = appendIndented(lines, selectedWitness)
	lines = append(lines,
		"  )) > 0",
		fmt.Sprintf("  LIMIT @%s", sequence.PreviewLimitBindKey),
		"  RETURN "+group+".root_keys",
		")",
		fmt.Sprintf("LET %s = FLATTEN(%s, 1)", selectedRootKeys, selectedGroups),
	)
	query := strings.Join(lines, "\n") + "\n"
	return sourceGroupPreviewFrontier{
		query: query, bindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query), rootKeysVariable: selectedRootKeys,
	}, nil
}

func sameRenderPhysicalBindValue(left, right any) bool {
	return reflect.DeepEqual(left, right)
}
