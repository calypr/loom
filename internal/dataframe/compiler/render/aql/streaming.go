package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql/internal/aqlsyntax"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type streamingPivotCategoryAccumulator struct {
	matchVariable string
	valueVariable string
	matchedCount  string
	nonNullCount  string
	reducedValue  string
	outputValue   string
	category      ir.PhysicalGroupedPivotCategory
}

func rebaseStreamingPivotProjectionToStoredParent(
	projection ir.PhysicalProjection,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) (ir.PhysicalProjection, bool) {
	rebased := false
	if projection.PresenceOutput && projection.Presence != nil {
		presence := *projection.Presence
		if rebaseStreamingPivotPresenceToStoredParent(&presence, rootVariable, parentPath, parentVariable) {
			projection.Presence = &presence
			rebased = true
		}
	} else if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression && projection.Expression.Extract != nil {
		expression := *projection.Expression
		extract := *expression.Extract
		if streamingPivotDirectScalarProjection(projection, rootVariable) {
			if rebaseStreamingPivotExtractToStoredParent(&extract, rootVariable, parentPath, parentVariable) {
				expression.Extract = &extract
				projection.Expression = &expression
				rebased = true
			}
		}
	} else if projection.Expression == nil {
		if value, ok := rebaseStreamingPivotValueToStoredParent(projection.Value, rootVariable, parentPath, parentVariable); ok {
			projection.Value = value
			rebased = true
		}
	}
	return projection, rebased
}

func rebaseStreamingPivotValueToStoredParent(
	value ir.PhysicalValue,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) (ir.PhysicalValue, bool) {
	if value.Variable != rootVariable || value.BindKey != "" || !aqlsyntax.PathHasPrefix(value.Path, parentPath) {
		return value, false
	}
	value.Variable = parentVariable
	value.Path = append([]string(nil), value.Path[len(parentPath):]...)
	return value, true
}

func rebaseStreamingPivotPresenceToStoredParent(
	presence *ir.PhysicalProjectionPresence,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) bool {
	if presence == nil {
		return false
	}
	if source, ok := rebaseStreamingPivotValueToStoredParent(presence.Source, rootVariable, parentPath, parentVariable); ok {
		presence.Source = source
		return true
	}
	source := presence.Source
	if source.Variable != rootVariable || source.BindKey != "" || len(source.Path) >= len(parentPath) ||
		len(presence.Paths) == 0 || !aqlsyntax.PathHasPrefix(parentPath, source.Path) {
		return false
	}
	parentRemainder := parentPath[len(source.Path):]
	for _, path := range presence.Paths {
		if len(path) <= len(parentRemainder) {
			return false
		}
		for index, segment := range parentRemainder {
			if path[index] != segment {
				return false
			}
		}
	}
	source.Variable = parentVariable
	source.Path = nil
	presence.Source = source
	paths := make([][]string, 0, len(presence.Paths))
	for _, path := range presence.Paths {
		paths = append(paths, append([]string(nil), path[len(parentRemainder):]...))
	}
	presence.Paths = paths
	return true
}

func rebaseStreamingPivotExtractToStoredParent(
	extract *ir.PhysicalExtract,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) bool {
	if !streamingPivotDirectScalarExtract(extract, rootVariable) {
		return false
	}
	stepOffset := 0
	if len(extract.Source.Path) >= len(parentPath) {
		if !aqlsyntax.PathHasPrefix(extract.Source.Path, parentPath) {
			return false
		}
		extract.Source.Path = append([]string(nil), extract.Source.Path[len(parentPath):]...)
	} else {
		if !aqlsyntax.PathHasPrefix(parentPath, extract.Source.Path) {
			return false
		}
		stepOffset = len(parentPath) - len(extract.Source.Path)
		if stepOffset > len(extract.Selector.Steps) {
			return false
		}
		for index := 0; index < stepOffset; index++ {
			if extract.Selector.Steps[index].Field != parentPath[len(extract.Source.Path)+index] {
				return false
			}
		}
		extract.Source.Path = nil
		extract.Selector.Steps = append([]spec.SelectorStep(nil), extract.Selector.Steps[stepOffset:]...)
	}
	extract.Source.Variable = parentVariable
	return true
}

func streamingPivotDirectScalarProjection(projection ir.PhysicalProjection, rootVariable string) bool {
	if projection.Presence != nil && (projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "") {
		return false
	}
	if projection.Expression == nil {
		return projection.Value.Variable == rootVariable && projection.Value.BindKey == "" && len(projection.Value.Path) > 0
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return false
	}
	return streamingPivotDirectScalarExtract(expression.Extract, rootVariable)
}

func streamingPivotDirectScalarExtract(extract *ir.PhysicalExtract, rootVariable string) bool {
	if extract == nil {
		return false
	}
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		extract.Source.BindKey != "" || len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 ||
		extract.Prepared != nil || extract.Distinct || extract.UnitNormalization != nil ||
		extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return false
	}
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func groupedPivotDirectCategoryMatchPredicate(
	category ir.PhysicalGroupedPivotCategory,
	categoryValue string,
	presenceValue string,
	categoryTypeBind string,
) (string, error) {
	if categoryValue == "" || presenceValue == "" {
		return "", fmt.Errorf("category scalar projection is required")
	}
	return groupedPivotCategoryMatchExpression(category, categoryValue, presenceValue+" == true", categoryTypeBind, true)
}
