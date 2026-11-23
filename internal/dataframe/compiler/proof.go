package compiler

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
)

func categoryScanPreservesSourceCategories(plan ir.PhysicalPlan, stageID, categoryColumnID string, category lower.CompiledOutputColumn) bool {
	sequence := plan.StageSequence
	if sequence == nil || stageID == "" || stageID == sequence.SourceStageID || sequence.FinalStageID != stageID ||
		len(sequence.Stages) == 0 || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.OutputAuthResourcePathBindKey != "" {
		return false
	}
	if category.ID != categoryColumnID || category.Name == "" {
		return false
	}

	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok {
		return false
	}
	var sourceCategory ir.PhysicalStageColumn
	if !uniquePhysicalStageColumn(sequence.SourceColumns, categoryColumnID, category.Name, &sourceCategory) ||
		!categoryScanDirectScalarStageColumn(sourceCategory) || sourceCategory.NormalizedUnit != nil || sourceCategory.RelatedRecordAnchor != nil {
		return false
	}
	var sourceProjection *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if sourceProjection != nil || projection.Hidden {
			return false
		}
		sourceProjection = projection
	}
	if sourceProjection == nil {
		return false
	}
	value, direct := ir.DirectScalarRootProjectionValue(*sourceProjection, root.Variable)
	if !direct || !categoryScanProjectionPresenceMatches(*sourceProjection, root.Variable, value) {
		return false
	}

	priorStageID := sequence.SourceStageID
	seenRelatedExpand := false
	for _, stage := range sequence.Stages {
		if stage.ID == "" || stage.InputStageID != priorStageID || stage.Filter != nil || len(stage.DerivedLets) != 0 {
			return false
		}
		var inputCategory, outputCategory ir.PhysicalStageColumn
		if !uniquePhysicalStageColumn(stage.InputColumns, categoryColumnID, category.Name, &inputCategory) ||
			!uniquePhysicalStageColumn(stage.OutputColumns, categoryColumnID, category.Name, &outputCategory) ||
			!sameCategoryScanColumn(inputCategory, sourceCategory) || !sameCategoryScanColumn(outputCategory, sourceCategory) ||
			!categoryScanPassesThroughProjection(stage, inputCategory) {
			return false
		}
		switch stage.Kind {
		case ir.PhysicalStageRelatedExpandOp:
			if stage.RelatedExpand == nil || stage.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || stage.RelatedField != nil {
				return false
			}
			seenRelatedExpand = true
		case ir.PhysicalStageRelatedFieldOp:
			if !seenRelatedExpand || stage.RelatedField == nil || stage.RelatedField.OutputColumnID == categoryColumnID || stage.RelatedExpand != nil {
				return false
			}
		default:
			return false
		}
		priorStageID = stage.ID
	}
	return seenRelatedExpand
}

func uniquePhysicalStageColumn(columns []ir.PhysicalStageColumn, id, name string, result *ir.PhysicalStageColumn) bool {
	found := false
	for _, column := range columns {
		if column.ID != id && column.Name != name {
			continue
		}
		if column.ID != id || column.Name != name || found {
			return false
		}
		*result = column
		found = true
	}
	return found
}

func sameCategoryScanColumn(column, source ir.PhysicalStageColumn) bool {
	return column.ID == source.ID && column.Name == source.Name && column.Kind == source.Kind &&
		column.Cardinality == source.Cardinality && column.Nullable == source.Nullable &&
		column.Internal == source.Internal && column.Identity == source.Identity &&
		column.NormalizedUnit == nil && column.RelatedRecordAnchor == nil
}

func categoryScanPassesThroughProjection(stage ir.PhysicalConstructionStage, input ir.PhysicalStageColumn) bool {
	if stage.InputRowVariable == "" {
		return false
	}
	found := false
	for _, projection := range stage.OutputProjections {
		if projection.Name != input.Name {
			continue
		}
		if found || projection.Hidden || projection.Expression != nil || projection.Presence != nil ||
			projection.Value.Variable != stage.InputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != input.Name {
			return false
		}
		found = true
	}
	return found
}

func categoryScanDirectRootSource(plan ir.PhysicalPlan, category lower.CompiledOutputColumn) bool {
	if plan.StageSequence != nil || !categoryScanDirectScalarColumn(category) {
		return false
	}
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Variable == "" {
		return false
	}
	var selected *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if selected != nil || projection.Hidden {
			return false
		}
		selected = projection
	}
	if selected == nil {
		return false
	}
	value, direct := ir.DirectScalarRootProjectionValue(*selected, root.Variable)
	return direct && categoryScanProjectionPresenceMatches(*selected, root.Variable, value)
}

func categoryScanRootProjectionSource(plan ir.PhysicalPlan) (*ir.PhysicalRootScan, *ir.PhysicalReturn, bool) {
	if len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return nil, nil, false
	}
	last := len(plan.Operations) - 1
	if plan.Operations[last].Kind != ir.PhysicalReturnOp || plan.Operations[last].Return == nil {
		return nil, nil, false
	}
	root := plan.Operations[0].RootScan
	correlatedVariables := map[string]struct{}{root.Variable: {}}
	for _, operation := range plan.Operations[1:last] {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return nil, nil, false
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return nil, nil, false
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil {
				return nil, nil, false
			}
		case ir.PhysicalSetOp:
			if operation.Set == nil || !categoryScanSetIsScoped(*operation.Set, correlatedVariables) {
				return nil, nil, false
			}
			set := operation.Set
			correlatedVariables[set.Variable] = struct{}{}
			if set.Prepared != nil {
				correlatedVariables[set.Prepared.Variable] = struct{}{}
			}
		default:

			return nil, nil, false
		}
	}
	return root, plan.Operations[last].Return, true
}

func categoryScanSetIsScoped(set ir.PhysicalSet, priorVariables map[string]struct{}) bool {
	if set.Variable == "" {
		return false
	}
	if set.SourceSetVariable != "" {
		_, sourceFound := priorVariables[set.SourceSetVariable]
		return sourceFound && set.ItemVariable != "" && len(set.Subplan.Captures) == 1 && set.Subplan.Captures[0] == set.SourceSetVariable
	}
	if len(set.Subplan.Operations) == 0 || set.Subplan.Operations[0].Kind != ir.PhysicalTraversalOp || set.Subplan.Operations[0].Traversal == nil || len(set.Subplan.Captures) == 0 {
		return false
	}
	traversalSource := set.Subplan.Operations[0].Traversal.SourceVariable
	if _, found := priorVariables[traversalSource]; !found {
		return false
	}
	for _, capture := range set.Subplan.Captures {
		if _, found := priorVariables[capture]; !found {
			return false
		}
	}
	return true
}

func categoryScanProjectionPresenceMatches(projection ir.PhysicalProjection, rootVariable string, value ir.PhysicalValue) bool {
	presence := projection.Presence
	if presence == nil || presence.Source.Variable != rootVariable || presence.Source.BindKey != "" || len(presence.Source.Path) == 0 ||
		len(presence.Paths) != 1 || len(presence.Paths[0]) == 0 {
		return false
	}
	path := append(append([]string(nil), presence.Source.Path...), presence.Paths[0]...)
	if presence.Source.Path[0] != "payload" || value.Variable != rootVariable || value.BindKey != "" || len(path) != len(value.Path) {
		return false
	}
	for index := range path {
		if path[index] != value.Path[index] {
			return false
		}
	}
	return true
}

func categoryScanDirectScalarColumn(column lower.CompiledOutputColumn) bool {
	if column.Internal || column.Identity || column.Name == "" ||
		(column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne)) {
		return false
	}
	switch expression.ValueKind(column.Kind) {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return true
	default:
		return false
	}
}

func categoryScanCategoryIndexSpec(plan ir.PhysicalPlan, category lower.CompiledOutputColumn) *PreviewCoveringIndexSpec {
	if !categoryScanDirectRootSource(plan, category) {
		return nil
	}
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Population != nil {
		return nil
	}
	var selected *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if selected != nil || projection.Hidden {
			return nil
		}
		selected = projection
	}
	if selected == nil {
		return nil
	}
	path, ok := previewCoveringProjectionPath(*selected, root.Variable)
	if !ok || !validPreviewIndexPath(strings.Split(path, ".")) {
		return nil
	}
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return nil
	}
	rootType := strings.TrimSpace(plan.Source.ResourceType)
	if collection == "Observation" && rootType == collection &&
		path == "payload.valueQuantity.code" && categoryScanHasExactRootPresencePath(*selected, root.Variable, strings.Split(path, ".")) {
		legacyStoredValues := previewQuantityCategoryStoredValues()
		fields := previewQuantityCategoryFields()
		return &PreviewCoveringIndexSpec{
			Collection:   collection,
			Name:         previewCoveringIndexNameWithStoredValues(collection, fields, legacyStoredValues),
			Fields:       fields,
			StoredValues: legacyStoredValues,
		}
	}
	if rootType == "" || collection != rootType {
		fields := []string{"project", "dataset_generation", path, "auth_resource_path"}
		return &PreviewCoveringIndexSpec{Collection: collection, Name: previewCoveringIndexName(collection, fields), Fields: fields}
	}
	fields := []string{"project", "dataset_generation", path}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, duplicate := seen[field]; duplicate {
			return nil
		}
		seen[field] = struct{}{}
	}
	storedValues := []string{"auth_resource_path"}
	if rootType != "" && collection == rootType {
		storedValues = append(storedValues, "resourceType")
	}
	return &PreviewCoveringIndexSpec{
		Collection: collection, Name: previewCoveringIndexNameWithStoredValues(collection, fields, storedValues),
		Fields: fields, StoredValues: storedValues,
	}
}
