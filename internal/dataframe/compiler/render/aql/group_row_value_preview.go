package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	groupRowValuePreviewKeyColumn = "__loom_preview_group_key"
	groupRowValuePreviewRootKey   = "__loom_preview_root_key"
)

type groupRowValuePreviewFrontier struct {
	query            string
	bindVars         map[string]any
	rootKeysVariable string
}

type groupRowValuePreviewShape struct {
	keyType string
}

// terminalGroupRowValuePreviewWindowFor proves narrow direct-root Group shapes
// whose row values or semantic memberships can be deferred until the first
// ordinary group keys are selected. The frontier scans the same scoped roots,
// groups and orders by the exact direct-root key, and retains every root key
// for those groups. The canonical source and Group then run over those roots.
func terminalGroupRowValuePreviewWindowFor(
	plan, sourcePlan ir.PhysicalPlan,
	sequence *ir.PhysicalStageSequence,
	options physicalRenderOptions,
	hasRelatedGroupPreview bool,
) (ir.PhysicalPlan, groupRowValuePreviewShape, bool) {
	if hasRelatedGroupPreview || sequence == nil || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil ||
		sequence.OutputAuthResourcePathBindKey != "" || options.omitTerminalRowSort || options.omitTerminalReturn ||
		options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" ||
		options.disableRootIndex ||
		options.projectionPresenceMarkerColumn != "" || len(options.preserveProjectionPresenceNames) != 0 ||
		options.twoScanPivotPreview || options.dynamicPivotPreview || options.streamingMissingCategoryPivotPreview ||
		options.storedPresenceObject || options.previewRootKeyWindowVariable != "" || options.previewGroupIdentityFilter != nil ||
		options.pivotGroupTupleFilter != nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	limit, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int)
	if !ok || limit <= 0 {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if len(sequence.Stages) != 1 {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	stage := sequence.Stages[0]
	group := stage.Group
	semanticGroup := group != nil && group.SemanticKey != nil
	// This frontier intentionally keeps the existing no-hint gate for ordinary
	// row-value Groups; only a proven semantic Group may carry a non-forcing
	// root hint into its direct-key scan.
	if options.rootIndexHint != "" && !semanticGroup {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || len(group.Keys) != 1 ||
		group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup ||
		group.RootContributorInputColumn != "" || group.RootContributorOutputColumn != "" || group.RootContributorVariable != "" ||
		group.RootContributorInputMany || len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" ||
		group.Aggregates[0].InputColumn != "" || group.Aggregates[0].InputKind != "" ||
		(semanticGroup && len(group.RowValues) != 0) || (!semanticGroup && len(group.RowValues) == 0) {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if group.Keys[0].Kind != "STRING" {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if !semanticGroup {
		for _, rowValue := range group.RowValues {
			if rowValue.Policy != "ALL" || !rowValue.InputMany || rowValue.InputKind != "STRING" {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		}
	}
	keyColumn := ir.PhysicalStageColumn{}
	keyColumnFound := false
	for _, column := range stage.InputColumns {
		if column.Name == group.Keys[0].InputColumn {
			keyColumn = column
			keyColumnFound = true
			break
		}
	}
	if !keyColumnFound || keyColumn.Internal || keyColumn.Kind != "string" ||
		(keyColumn.Cardinality != "required_one" && keyColumn.Cardinality != "optional_one") {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if len(sourcePlan.Operations) < 2 || sourcePlan.PreviewSourceWindowByRootID || sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp ||
		sourcePlan.Operations[0].RootScan == nil || sourcePlan.Operations[0].RootScan.Variable == "" ||
		sourcePlan.Operations[0].RootScan.Population != nil || sourcePlan.Operations[0].RootScan.CohortSource != nil ||
		sourcePlan.Operations[0].RootScan.PageCandidateSeed != nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	rootVariable := sourcePlan.Operations[0].RootScan.Variable
	last := len(sourcePlan.Operations) - 1
	if sourcePlan.Operations[last].Kind != ir.PhysicalReturnOp || sourcePlan.Operations[last].Return == nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	sourceLayout, err := buildNavigationRenderLayout(sourcePlan)
	if err != nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	rootScopeEnd := 1 + len(sourceLayout.rootScope)
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		case ir.PhysicalFilterOp:
			// Only the exact project/generation/auth filters in the generic
			// root-scope block are safe to reuse in the key frontier.
			if index >= rootScopeEnd {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		case ir.PhysicalDerivedLetOp:
			if index >= rootScopeEnd {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		case ir.PhysicalExpressionLetOp, ir.PhysicalSetOp:
			if index < rootScopeEnd {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		case ir.PhysicalReturnOp:
			if index != last {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
		default:
			// An UNNEST, traversal, or root window can change which source rows
			// contribute to a Group. Those shapes stay on the generic renderer.
			return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
		}
	}
	var keyProjection ir.PhysicalProjection
	keyProjectionIndex := -1
	rowValueWorkCanBeDeferred := false
	for index, projection := range sourcePlan.Operations[last].Return.Projections {
		for _, rowValue := range group.RowValues {
			if projection.Name == rowValue.InputColumn &&
				(projection.Expression != nil || projection.Value.Variable != rootVariable) {
				rowValueWorkCanBeDeferred = true
			}
		}
		if projection.Name == group.Keys[0].InputColumn {
			keyProjection = projection
			keyProjectionIndex = index
		}
	}
	keyPath, keyProjectionIsDirectRootScalar := directRootProjectionPath(keyProjection, rootVariable)
	semanticGroupWorkCanBeDeferred := false
	if semantic := group.SemanticKey; semantic != nil {
		rootScan := sourcePlan.Operations[0].RootScan
		semanticRootIdentityPresent := false
		for _, projection := range sourcePlan.Operations[last].Return.Projections {
			if projection.Name == semantic.SourceIdentityColumn && !projection.PresenceOutput &&
				projection.Value.Variable == rootVariable && len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key" &&
				projection.Presence == nil && projection.Expression == nil {
				semanticRootIdentityPresent = true
				break
			}
		}
		semanticGroupWorkCanBeDeferred = semantic.SourceRootVariable == rootVariable &&
			semantic.SourceIdentityColumn == "_key" && semantic.RootCollectionBindKey == rootScan.CollectionBindKey &&
			semantic.ResourceType != "" && semantic.ResourceType == sourcePlan.Source.ResourceType &&
			sourcePlan.BindVars[semantic.RootCollectionBindKey] == semantic.ResourceType &&
			semantic.CodingPath != "" && len(semantic.PathSegments) != 0 && semanticRootIdentityPresent
	}
	if keyProjectionIndex < 0 || keyProjection.Name == "" || keyProjection.PresenceOutput ||
		(!rowValueWorkCanBeDeferred && !semanticGroupWorkCanBeDeferred) || !keyProjectionIsDirectRootScalar {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	if keyProjection.Expression != nil {
		expression := keyProjection.Expression
		extract := expression.Extract
		if expression.NullBehavior != ir.PhysicalPreserveNull || sourcePlan.Source.ResourceType == "" || extract.ResourceType != sourcePlan.Source.ResourceType {
			return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
		}
		if keyProjection.Presence != nil {
			presence := keyProjection.Presence
			if !sameRenderPhysicalValue(presence.Source, extract.Source) || len(presence.Paths) != 1 || len(presence.Paths[0]) != len(extract.Selector.Steps) {
				return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
			}
			for index, step := range extract.Selector.Steps {
				if presence.Paths[0][index] != step.Field {
					return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
				}
			}
		}
	} else if keyProjection.Presence != nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	for _, segment := range keyPath {
		if !previewSourcePathSegmentPattern.MatchString(segment) {
			return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
		}
	}
	keyType, err := aqlTableScalarType(group.Keys[0].Kind)
	if err != nil {
		return ir.PhysicalPlan{}, groupRowValuePreviewShape{}, false
	}
	frontierPlan := ir.ClonePhysicalPlan(sourcePlan)
	frontierKeyProjection := frontierPlan.Operations[last].Return.Projections[keyProjectionIndex]
	frontierKeyProjection.Name = groupRowValuePreviewKeyColumn
	frontierPlan.Operations[last].Return.Projections = []ir.PhysicalProjection{
		frontierKeyProjection,
		{Name: groupRowValuePreviewRootKey, Hidden: true, Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"_key"}}},
	}
	pruneUnusedSourceGroupOperations(&frontierPlan)
	return frontierPlan, groupRowValuePreviewShape{keyType: keyType}, true
}

func renderGroupRowValuePreviewFrontier(
	frontierPlan ir.PhysicalPlan,
	shape groupRowValuePreviewShape,
	sequence *ir.PhysicalStageSequence,
	options physicalRenderOptions,
) (groupRowValuePreviewFrontier, error) {
	// Root hints are non-forcing. Apply the same hint to the key scan when one
	// was selected; the canonical source later resolves the selected keys with
	// DOCUMENT, so its root-window scan does not use or require that hint.
	source, err := renderPhysicalPlanWithOptions(frontierPlan, physicalRenderOptions{
		rootIndexHint:    options.rootIndexHint,
		disableRootIndex: options.disableRootIndex,
		internalPrefix:   "group_preview_source_",
	})
	if err != nil {
		return groupRowValuePreviewFrontier{}, fmt.Errorf("render selected GROUP-key source: %w", err)
	}
	bindVars := make(map[string]any, len(source.BindVars)+2)
	for key, value := range source.BindVars {
		bindVars[key] = clonePhysicalBindValue(value)
	}
	reserved := stageSequenceVariableNames(ir.PhysicalPlan{StageSequence: sequence})
	for variable := range physicalPlanVariableNames(frontierPlan) {
		reserved[variable] = struct{}{}
	}
	renderer := physicalPlanRenderer{bindVars: bindVars, reservedVars: reserved, internalPrefix: "construction_"}
	sourceRows := renderer.newInternalVariable("group_preview_source_rows")
	groupRootKeys := renderer.newInternalVariable("group_preview_root_keys_by_group")
	selectedRootKeys := renderer.newInternalVariable("group_preview_selected_root_keys")
	sourceRow := renderer.newInternalVariable("group_preview_source_row")
	groupKey := renderer.newInternalVariable("group_preview_group_key")
	groupRootKey := renderer.newInternalVariable("group_preview_root_key")
	groupKeyTypeBind := renderer.newInternalBindKey("group_preview_key_type")
	renderer.bindVars[groupKeyTypeBind] = shape.keyType
	limitBindKey := sequence.PreviewLimitBindKey
	if _, found := renderer.bindVars[limitBindKey]; !found {
		value, ok := frontierPlan.BindVars[limitBindKey]
		if !ok {
			return groupRowValuePreviewFrontier{}, fmt.Errorf("preview Group limit bind %q is missing", limitBindKey)
		}
		renderer.bindVars[limitBindKey] = clonePhysicalBindValue(value)
	}
	lines := []string{
		fmt.Sprintf("LET %s = (", sourceRows),
	}
	lines = appendIndented(lines, strings.Split(strings.TrimSuffix(source.Query, "\n"), "\n"))
	lines = append(lines,
		")",
		fmt.Sprintf("LET %s = (", groupRootKeys),
		fmt.Sprintf("  FOR %s IN %s", sourceRow, sourceRows),
		fmt.Sprintf("  COLLECT %s = %s.%s INTO %s = %s.%s", groupKey, sourceRow, groupRowValuePreviewKeyColumn, groupRootKey, sourceRow, groupRowValuePreviewRootKey),
		fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", groupKey, groupKey, groupKeyTypeBind),
		fmt.Sprintf("  SORT %s ASC", groupKey),
		fmt.Sprintf("  LIMIT @%s", limitBindKey),
		fmt.Sprintf("  RETURN %s", groupRootKey),
		")",
		fmt.Sprintf("LET %s = FLATTEN(%s, 1)", selectedRootKeys, groupRootKeys),
	)
	query := strings.Join(lines, "\n") + "\n"
	return groupRowValuePreviewFrontier{
		query: query, bindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query), rootKeysVariable: selectedRootKeys,
	}, nil
}
