package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const previewCoveringIndexNamePrefix = "loom_pivot_preview_"

var previewCoveringIndexPrefixFields = []string{"project", "dataset_generation", "auth_resource_path"}

func previewCoveringIndexSpec(plan ir.PhysicalPlan) *PreviewCoveringIndexSpec {
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || len(sequence.Stages) != 1 {
		return nil
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.GroupedPivot.OneInputRowPerGroup || len(stage.GroupedPivot.GroupKeys) == 0 ||
		stage.GroupedPivot.CategoryPresence != nil || stage.GroupedPivot.CategoryPresenceColumn != "" {
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
	projectionPaths, ok := previewCoveringProjectionPaths(sourceReturn.Projections, rootScan.Variable)
	if !ok {
		return nil
	}
	if !pivotInputsPassThroughSourceColumns(stage, sourceReturn.Projections) {
		return nil
	}

	return previewCoveringIndexSpecForSourcePaths(collection, projectionPaths)
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
