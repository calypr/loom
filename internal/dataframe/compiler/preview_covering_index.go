package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

const previewCoveringIndexNamePrefix = "loom_pivot_preview_"

var previewCoveringIndexPrefixFields = []string{"project", "dataset_generation", "auth_resource_path"}

func previewCoveringIndexSpec(plan ir.PhysicalPlan) *PreviewCoveringIndexSpec {
	sequence := plan.StageSequence
	if sequence == nil || sequence.PreviewLimitBindKey == "" || sequence.PreviewSourceWindowByRootID ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || len(sequence.Stages) != 1 {
		return nil
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp && stage.Kind != ir.PhysicalStageGroupOp {
		return nil
	}

	rootScan, sourceReturn, ok := previewCoveringIndexSource(plan)
	if !ok || rootScan.Population != nil {
		return nil
	}
	collection, ok := plan.BindVars[rootScan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return nil
	}
	switch stage.Kind {
	case ir.PhysicalStagePivotOp:
		if !sequence.PreviewTerminalPivotWindow || stage.GroupedPivot == nil ||
			stage.GroupedPivot.OneInputRowPerGroup || len(stage.GroupedPivot.GroupKeys) == 0 ||
			stage.GroupedPivot.CategoryPresence != nil || stage.GroupedPivot.CategoryPresenceColumn != "" {
			return nil
		}
		projectionPaths, ok := previewCoveringProjectionPaths(sourceReturn.Projections, rootScan.Variable)
		if !ok || !pivotInputsPassThroughSourceColumns(stage, sourceReturn.Projections) {
			return nil
		}
		groupKeyPaths, ok := previewCoveringPivotGroupKeyPaths(stage.GroupedPivot, sourceReturn.Projections, rootScan.Variable)
		if !ok {
			return nil
		}
		spec := previewCoveringIndexSpecForSourcePaths(collection, projectionPaths)
		if spec != nil {
			spec.pivotGroupKeyPaths = groupKeyPaths
		}
		return spec
	case ir.PhysicalStageGroupOp:
		if sequence.PreviewTerminalPivotWindow {
			return nil
		}
		projectionPaths, ok := previewCoveringGroupProjectionPaths(stage, sourceReturn.Projections, rootScan.Variable)
		if !ok {
			return nil
		}
		spec := previewCoveringGroupIndexSpec(collection, projectionPaths)
		if spec != nil {
			spec.PrepareAfterPreview = true
		}
		return spec
	}
	return nil
}

func previewGroupScanSpec(plan ir.PhysicalPlan, covering *PreviewCoveringIndexSpec) *PreviewGroupScanSpec {
	sequence := plan.StageSequence
	if covering == nil || !covering.PrepareAfterPreview || sequence == nil || len(sequence.Stages) != 1 ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil {
		return nil
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID || stage.Kind != ir.PhysicalStageGroupOp {
		return nil
	}
	root, _, ok := previewCoveringIndexSource(plan)
	if !ok || root.Population != nil {
		return nil
	}
	scopeCount, err := aql.RenderPhysicalRootScopeCount(plan)
	if err != nil {
		return nil
	}
	sequential, err := aql.RenderPhysicalPlanWithRootIndexDisabled(plan)
	if err != nil {
		return nil
	}
	return &PreviewGroupScanSpec{
		Collection:         covering.Collection,
		SequentialQuery:    sequential.Query,
		SequentialBindVars: sequential.BindVars,
		ScopeCountQuery:    scopeCount.Query,
		ScopeCountBindVars: scopeCount.BindVars,
	}
}

func previewCoveringGroupProjectionPaths(stage ir.PhysicalConstructionStage, projections []ir.PhysicalProjection, rootVariable string) ([]string, bool) {
	group := stage.Group
	if group == nil || len(group.Aggregates) == 0 {
		return nil, false
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" {
			return nil, false
		}
	}
	for _, rowValue := range group.RowValues {
		if rowValue.Policy != "ALL" && rowValue.Policy != "ONE" {
			return nil, false
		}
	}
	inputProjections, ok := groupInputProjectionsByName(stage)
	if !ok {
		return nil, false
	}
	sourceProjections := make(map[string]ir.PhysicalProjection, len(projections))
	for _, projection := range projections {
		if projection.Name == "" {
			return nil, false
		}
		if _, exists := sourceProjections[projection.Name]; exists {
			return nil, false
		}
		sourceProjections[projection.Name] = projection
	}
	required := make(map[string]bool, len(group.Keys)+len(group.RowValues))
	for _, key := range group.Keys {
		required[key.InputColumn] = false
	}
	for _, rowValue := range group.RowValues {
		required[rowValue.InputColumn] = true
	}
	paths := make([]string, 0, len(required))
	for name, isRowValue := range required {
		sourceProjection, sourceFound := sourceProjections[name]
		inputProjection, inputFound := inputProjections[name]
		if !sourceFound || !inputFound ||
			inputProjection.Value.Variable != stage.InputRowVariable || len(inputProjection.Value.Path) != 1 || inputProjection.Value.Path[0] != name {
			return nil, false
		}
		var path string
		var pathFound bool
		if isRowValue {
			path, pathFound = previewCoveringGroupProjectionPath(sourceProjection, rootVariable)
		} else {
			path, pathFound = previewCoveringProjectionPath(sourceProjection, rootVariable)
		}
		if !pathFound {
			return nil, false
		}
		paths = append(paths, path)
	}
	return paths, len(paths) != 0
}

func groupInputProjectionsByName(stage ir.PhysicalConstructionStage) (map[string]ir.PhysicalProjection, bool) {
	inputProjections := make(map[string]ir.PhysicalProjection, len(stage.InputProjections))
	for _, projection := range stage.InputProjections {
		if projection.Name == "" || projection.Expression != nil || projection.Presence != nil ||
			projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return nil, false
		}
		if _, duplicate := inputProjections[projection.Name]; duplicate {
			return nil, false
		}
		inputProjections[projection.Name] = projection
	}
	return inputProjections, true
}

func previewCoveringGroupProjectionPath(projection ir.PhysicalProjection, rootVariable string) (string, bool) {
	if projection.Expression == nil || projection.Expression.Cardinality == ir.PhysicalScalarCardinality {
		return previewCoveringProjectionPath(projection, rootVariable)
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalArrayCardinality || expression.Extract == nil {
		return "", false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorConditionalArray || extract.Source.Variable != rootVariable ||
		extract.Source.BindKey != "" || len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 ||
		extract.Distinct || extract.Prepared != nil || extract.UnitNormalization != nil || extract.Selector.Filter != nil {
		return "", false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, part := range path {
		if !validPreviewIndexPath([]string{part}) {
			return "", false
		}
	}
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Index != nil || !validPreviewIndexPath([]string{step.Field}) {
			return "", false
		}
		path = append(path, step.Field)
		if step.Iterate {
			return strings.Join(path, "."), true
		}
	}
	return "", false
}

func previewCoveringPivotGroupKeyPaths(pivot *ir.PhysicalGroupedPivot, projections []ir.PhysicalProjection, rootVariable string) ([][]string, bool) {
	if pivot == nil || len(pivot.GroupKeys) == 0 {
		return nil, false
	}
	byName := make(map[string]ir.PhysicalProjection, len(projections))
	for _, projection := range projections {
		byName[projection.Name] = projection
	}
	paths := make([][]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		projection, found := byName[key.Column]
		if !found {
			return nil, false
		}
		path, ok := previewCoveringProjectionPath(projection, rootVariable)
		if !ok {
			return nil, false
		}
		paths = append(paths, strings.Split(path, "."))
	}
	return paths, true
}

func previewCoveringIndexSpecForSourcePaths(collection string, projectionPaths []string) *PreviewCoveringIndexSpec {
	if strings.TrimSpace(collection) == "" {
		return nil
	}
	pathSet := make(map[string]struct{}, len(projectionPaths)+len(previewCoveringIndexPrefixFields))
	for _, field := range previewCoveringIndexPrefixFields {
		pathSet[field] = struct{}{}
	}
	for _, path := range projectionPaths {
		pathSet[path] = struct{}{}
	}
	pathSet["_key"] = struct{}{}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fields := append([]string(nil), previewCoveringIndexPrefixFields...)
	for _, path := range paths {
		if containsIndexField(fields, path) {
			continue
		}
		fields = append(fields, path)
	}
	if len(fields) < 4 || len(fields) > 32 {
		return nil
	}
	name := previewCoveringIndexName(collection, fields)
	return &PreviewCoveringIndexSpec{Collection: collection, Name: name, Fields: fields}
}

func previewCoveringGroupIndexSpec(collection string, projectionPaths []string) *PreviewCoveringIndexSpec {
	if strings.TrimSpace(collection) == "" || len(projectionPaths) == 0 {
		return nil
	}
	projectionPathSet := make(map[string]struct{}, len(projectionPaths))
	for _, path := range projectionPaths {
		if !validPreviewIndexPath(strings.Split(path, ".")) {
			return nil
		}
		projectionPathSet[path] = struct{}{}
	}
	if len(projectionPathSet) == 0 || len(projectionPathSet)+len(previewCoveringIndexPrefixFields) > 32 {
		return nil
	}
	projectionPaths = make([]string, 0, len(projectionPathSet))
	for path := range projectionPathSet {
		projectionPaths = append(projectionPaths, path)
	}
	sort.Strings(projectionPaths)
	fields := append([]string(nil), previewCoveringIndexPrefixFields...)
	fields = append(fields, projectionPaths...)
	return &PreviewCoveringIndexSpec{Collection: collection, Name: previewCoveringIndexName(collection, fields), Fields: fields}
}

func previewCoveringIndexSource(plan ir.PhysicalPlan) (*ir.PhysicalRootScan, *ir.PhysicalReturn, bool) {
	var rootScan *ir.PhysicalRootScan
	var sourceReturn *ir.PhysicalReturn
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || rootScan != nil || operation.RootScan == nil {
				return nil, nil, false
			}
			rootScan = operation.RootScan
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		case ir.PhysicalReturnOp:
			if sourceReturn != nil || operation.Return == nil || index != len(plan.Operations)-1 {
				return nil, nil, false
			}
			sourceReturn = operation.Return
		default:
			return nil, nil, false
		}
	}
	return rootScan, sourceReturn, rootScan != nil && sourceReturn != nil
}

func previewCoveringProjectionPaths(projections []ir.PhysicalProjection, rootVariable string) ([]string, bool) {
	if len(projections) == 0 {
		return nil, false
	}
	paths := make([]string, 0, len(projections))
	seenNames := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if projection.Name == "" {
			return nil, false
		}
		if _, exists := seenNames[projection.Name]; exists {
			return nil, false
		}
		seenNames[projection.Name] = struct{}{}
		path, ok := previewCoveringProjectionPath(projection, rootVariable)
		if !ok {
			return nil, false
		}
		paths = append(paths, path)
	}
	return paths, true
}

func previewCoveringProjectionPath(projection ir.PhysicalProjection, rootVariable string) (string, bool) {
	if projection.Expression == nil {
		if projection.Value.Variable != rootVariable || !validPreviewIndexPath(projection.Value.Path) {
			return "", false
		}
		return strings.Join(projection.Value.Path, "."), true
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return "", false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" || len(extract.Fallbacks) != 0 ||
		extract.Distinct || extract.Prepared != nil || extract.UnitNormalization != nil || extract.Selector.Filter != nil ||
		len(extract.Selector.Steps) == 0 {
		return "", false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return "", false
		}
		path = append(path, step.Field)
	}
	if !validPreviewIndexPath(path) {
		return "", false
	}
	return strings.Join(path, "."), true
}

func validPreviewIndexPath(path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, part := range path {
		if part == "" {
			return false
		}
		for index, char := range part {
			valid := char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9'
			if !valid {
				return false
			}
		}
	}
	return true
}

func pivotInputsPassThroughSourceColumns(stage ir.PhysicalConstructionStage, sourceProjections []ir.PhysicalProjection) bool {
	if stage.GroupedPivot == nil || len(stage.GroupedPivot.InputProjections) != len(sourceProjections) {
		return false
	}
	sourceNames := make(map[string]struct{}, len(sourceProjections))
	for _, projection := range sourceProjections {
		sourceNames[projection.Name] = struct{}{}
	}
	seenNames := make(map[string]struct{}, len(stage.GroupedPivot.InputProjections))
	for _, projection := range stage.GroupedPivot.InputProjections {
		if _, exists := sourceNames[projection.Name]; !exists || projection.Expression != nil || projection.Presence != nil ||
			projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return false
		}
		if _, exists := seenNames[projection.Name]; exists {
			return false
		}
		seenNames[projection.Name] = struct{}{}
	}
	return len(seenNames) == len(sourceNames)
}

func containsIndexField(fields []string, field string) bool {
	for _, existing := range fields {
		if existing == field {
			return true
		}
	}
	return false
}

func previewCoveringIndexName(collection string, fields []string) string {
	canonical := collection + "\x00" + strings.Join(fields, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s%s", previewCoveringIndexNamePrefix, hex.EncodeToString(digest[:8]))
}
