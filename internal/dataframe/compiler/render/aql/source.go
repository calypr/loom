package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type stageSourceRenderer struct {
	plan     ir.PhysicalPlan
	sequence *ir.PhysicalStageSequence
	stages   []ir.PhysicalConstructionStage
	options  physicalRenderOptions
}

func (source stageSourceRenderer) groupSourceRowVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || len(stages) != 1 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.OutputAuthResourcePathBindKey != "" ||
		options.omitTerminalReturn || options.terminalReturnVariable != "" || options.terminalProjectionColumn != "" ||
		options.omitTerminalRowSort || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 {
		return ""
	}
	stage := stages[0]
	group := stage.Group
	keylessRowValueGroup := group != nil && len(group.Keys) == 0 && len(group.RowValues) > 0
	hasGroupKey := group != nil && (len(group.Keys) != 0 || group.SemanticKey != nil || group.CodingKey != nil)
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStageGroupOp || group == nil || (!hasGroupKey && !keylessRowValueGroup) ||
		group.RootContributorInputColumn != "" || group.DynamicRuntimeKeysVariable != "" ||
		!constructionGroupCountsOnlyRows(group) ||
		(len(group.RowValues) > 0 && !constructionGroupRowValuesCanAggregate(group.RowValues)) {
		return ""
	}
	if len(sourcePlan.Operations) < 2 || sourcePlan.PreviewSourceWindowByRootID ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	last := len(sourcePlan.Operations) - 1
	if sourcePlan.Operations[last].Kind != ir.PhysicalReturnOp || sourcePlan.Operations[last].Return == nil {
		return ""
	}
	for index, operation := range sourcePlan.Operations[:last] {
		if index == 0 {
			if operation.Kind != ir.PhysicalRootScanOp || operation.RootScan == nil {
				return ""
			}
			continue
		}
		switch operation.Kind {
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp, ir.PhysicalSetOp, ir.PhysicalUnnestOp:
		default:
			return ""
		}
	}
	layout, err := buildNavigationRenderLayout(sourcePlan)
	if err != nil || len(layout.rootWindow) != 0 || len(layout.unnests) > 1 {
		return ""
	}
	if len(layout.unnests) == 1 {
		if group.SemanticKey != nil || group.CodingKey == nil && len(group.Keys) == 0 && len(group.RowValues) == 0 {
			return ""
		}
	}
	for _, item := range layout.postWindow {
		if item.operation.Kind != ir.PhysicalExpressionLetOp {
			return ""
		}
	}
	if stage.InputRowVariable == "" {
		return ""
	}
	if _, collision := physicalPlanVariableNames(sourcePlan)[stage.InputRowVariable]; collision {
		return ""
	}
	return stage.InputRowVariable
}

func (source stageSourceRenderer) codedGroupSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || len(stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || stages[0].Kind != ir.PhysicalStageCodedGroupOp || stages[0].CodedGroup == nil ||
		stages[0].InputStageID != sequence.SourceStageID || !stages[0].CodedGroup.SourceRowsUnique ||
		stages[0].CodedGroup.SourceIdentityColumn != "_key" || len(sourcePlan.Operations) < 2 ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	root := sourcePlan.Operations[0].RootScan
	if root.Variable == "" || root.CollectionBindKey != stages[0].CodedGroup.RootCollectionBindKey {
		return ""
	}
	returns := 0
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:

		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourcePlan.Operations)-1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returns != 1 {
		return ""
	}
	return root.Variable
}

func (source stageSourceRenderer) codedPivotSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || len(stages) == 0 || sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || stages[0].Kind != ir.PhysicalStagePivotOp || stages[0].GroupedPivot == nil ||
		stages[0].GroupedPivot.CodedCorrelation == nil || stages[0].InputStageID != sequence.SourceStageID ||
		stages[0].GroupedPivot.CodedSourceVariable == "" || !stages[0].GroupedPivot.OneInputRowPerGroup ||
		sequence.SourceRowIdentity != "_key" || len(sourcePlan.Operations) < 2 ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil {
		return ""
	}
	root := sourcePlan.Operations[0].RootScan
	if root.Variable == "" || root.Variable != stages[0].GroupedPivot.CodedSourceVariable ||
		root.CollectionBindKey == "" || sourcePlan.BindVars[root.CollectionBindKey] != stages[0].GroupedPivot.CodedCorrelation.ResourceType {
		return ""
	}
	returns := 0
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:

		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourcePlan.Operations)-1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returns != 1 {
		return ""
	}
	return root.Variable
}

func (source stageSourceRenderer) streamingMissingCategoryPivotSourceRootVariable() string {
	sourcePlan, sequence, stages, options := source.plan, source.sequence, source.stages, source.options
	if sequence == nil || !options.streamingMissingCategoryPivot() || len(stages) != 1 ||
		sequence.RowLineageReturn != nil || sequence.CellTraceReturn != nil || sequence.PopulationMappingReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.OutputAuthResourcePathBindKey != "" ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 {
		return ""
	}
	stage := stages[0]
	pivot := stage.GroupedPivot
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivot.OneInputRowPerGroup ||
		pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresence != nil || pivot.CategoryPresenceColumn == "" {
		return ""
	}
	hasMissing := false
	for _, category := range pivot.Categories {
		if category.MatchKind == ir.PhysicalPivotCategoryMissingMatch {
			hasMissing = true
		}
	}
	if !hasMissing || len(sourcePlan.Operations) < 2 || sourcePlan.PreviewSourceWindowByRootID ||
		sourcePlan.Operations[0].Kind != ir.PhysicalRootScanOp || sourcePlan.Operations[0].RootScan == nil ||
		sourcePlan.Operations[0].RootScan.Population != nil {
		return ""
	}
	rootVariable := sourcePlan.Operations[0].RootScan.Variable
	if rootVariable == "" {
		return ""
	}
	returnCount := 0
	sourceProjectionNames := make(map[string]struct{})
	for index, operation := range sourcePlan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ""
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return ""
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return ""
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil {
				return ""
			}
		case ir.PhysicalReturnOp:
			returnCount++
			if index != len(sourcePlan.Operations)-1 || operation.Return == nil {
				return ""
			}
			presenceOutputs := 0
			for _, projection := range operation.Return.Projections {
				if projection.Name == "" {
					return ""
				}
				if _, duplicate := sourceProjectionNames[projection.Name]; duplicate {
					return ""
				}
				sourceProjectionNames[projection.Name] = struct{}{}
				if projection.PresenceOutput {
					if projection.Name != pivot.CategoryPresenceColumn || projection.Presence == nil ||
						projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "" {
						return ""
					}
					presenceOutputs++
					continue
				}
				if !directRootCategoryProjection(projection, rootVariable) {
					return ""
				}
			}
			if presenceOutputs != 1 {
				return ""
			}
		default:
			return ""
		}
	}
	if returnCount != 1 {
		return ""
	}
	if !streamingPivotInputsAreSourceRows(*pivot, stage.InputRowVariable, sourceProjectionNames) {
		return ""
	}
	if _, collision := physicalPlanVariableNames(sourcePlan)[pivot.InputRowVariable]; collision {
		return ""
	}
	return rootVariable
}

func streamingPivotStoredPresenceOutputName(sourcePlan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, rootVariable string) (string, []string, bool) {
	pivot := stage.GroupedPivot
	if pivot == nil || pivot.CategoryColumn == "" || pivot.CategoryPresenceColumn == "" {
		return "", nil, false
	}
	var sourceReturn *ir.PhysicalReturn
	for index := range sourcePlan.Operations {
		operation := &sourcePlan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp {
			continue
		}
		if sourceReturn != nil || index != len(sourcePlan.Operations)-1 || operation.Return == nil {
			return "", nil, false
		}
		sourceReturn = operation.Return
	}
	if sourceReturn == nil {
		return "", nil, false
	}
	var categoryPath []string
	var presence *ir.PhysicalProjectionPresence
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name == pivot.CategoryColumn {
			var ok bool
			categoryPath, ok = directRootProjectionPath(*projection, rootVariable)
			if !ok {
				return "", nil, false
			}
		}
		if projection.Name == pivot.CategoryPresenceColumn && projection.PresenceOutput {
			if projection.Presence == nil || projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "" {
				return "", nil, false
			}
			copy := *projection.Presence
			presence = &copy
		}
	}
	if len(categoryPath) < 3 || presence == nil || len(presence.Paths) != 1 || len(presence.Paths[0]) < 1 {
		return "", nil, false
	}
	fullPath := append(append([]string(nil), presence.Source.Path...), presence.Paths[0]...)
	if !reflect.DeepEqual(fullPath, categoryPath) || len(fullPath) < 3 || strings.Join(fullPath[:len(fullPath)-1], ".") == "payload" {
		return "", nil, false
	}
	for _, segment := range fullPath {
		if !previewSourcePathSegmentPattern.MatchString(segment) {
			return "", nil, false
		}
	}
	return pivot.CategoryPresenceColumn, append([]string(nil), fullPath[:len(fullPath)-1]...), true
}

func directRootProjectionPath(projection ir.PhysicalProjection, rootVariable string) ([]string, bool) {
	if projection.Expression == nil {
		if projection.Value.Variable != rootVariable || projection.Value.BindKey != "" || len(projection.Value.Path) == 0 {
			return nil, false
		}
		return append([]string(nil), projection.Value.Path...), true
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return nil, false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable || extract.Source.BindKey != "" ||
		len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 || extract.Prepared != nil || extract.Distinct ||
		extract.UnitNormalization != nil || extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return nil, false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return nil, false
		}
		path = append(path, step.Field)
	}
	return path, true
}

func streamingPivotInputsAreSourceRows(pivot ir.PhysicalGroupedPivot, inputRowVariable string, sourceProjectionNames map[string]struct{}) bool {
	if inputRowVariable == "" || len(pivot.InputProjections) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(pivot.InputProjections))
	for _, projection := range pivot.InputProjections {
		if projection.Name == "" || projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput ||
			projection.Value.Variable != inputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return false
		}
		if _, ok := sourceProjectionNames[projection.Name]; !ok {
			return false
		}
		if _, duplicate := seen[projection.Name]; duplicate {
			return false
		}
		seen[projection.Name] = struct{}{}
	}
	required := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn}
	for _, key := range pivot.GroupKeys {
		required = append(required, key.Column)
	}
	for _, name := range required {
		if name == "" {
			return false
		}
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

func rebindStreamingPivotSourceProjections(stages []ir.PhysicalConstructionStage, sourcePlan ir.PhysicalPlan) error {
	if len(stages) != 1 || stages[0].GroupedPivot == nil {
		return fmt.Errorf("streaming missing-category Pivot source rebinding requires one Pivot stage")
	}
	var sourceReturn *ir.PhysicalReturn
	for index := range sourcePlan.Operations {
		operation := &sourcePlan.Operations[index]
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			sourceReturn = operation.Return
			break
		}
	}
	if sourceReturn == nil {
		return fmt.Errorf("streaming missing-category Pivot source has no RETURN")
	}
	sourceProjections := make(map[string]ir.PhysicalProjection, len(sourceReturn.Projections))
	for _, projection := range sourceReturn.Projections {
		if projection.Name == "" {
			return fmt.Errorf("streaming missing-category Pivot source has an unnamed projection")
		}
		if _, duplicate := sourceProjections[projection.Name]; duplicate {
			return fmt.Errorf("streaming missing-category Pivot source repeats projection %q", projection.Name)
		}
		sourceProjections[projection.Name] = projection
	}
	pivot := *stages[0].GroupedPivot
	projections := make([]ir.PhysicalProjection, 0, len(pivot.InputProjections))
	for _, input := range pivot.InputProjections {
		projection, ok := sourceProjections[input.Name]
		if !ok {
			return fmt.Errorf("streaming missing-category Pivot source is missing input projection %q", input.Name)
		}
		projection.Hidden = input.Hidden
		projections = append(projections, projection)
	}
	pivot.InputProjections = projections
	stages[0].GroupedPivot = &pivot
	return nil
}
