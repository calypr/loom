package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func resolveConstructionSourceSchema(plan *ir.PhysicalPlan, declarations []recipe.StageColumn, schema []CompiledOutputColumn, outputName string) ([]CompiledOutputColumn, error) {
	projections := map[string]ir.PhysicalProjection{}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Hidden {
				continue
			}
			projections[projection.Name] = projection
		}
	}
	schemaByName := make(map[string]CompiledOutputColumn, len(schema))
	publicCount := 0
	for _, column := range schema {
		if column.Internal {
			continue
		}
		publicCount++
		schemaByName[column.Name] = column
	}
	if len(declarations) != publicCount {
		return nil, fmt.Errorf("construction source schema declares %d columns but resolved source emits %d public columns", len(declarations), publicCount)
	}
	resolved := make([]CompiledOutputColumn, 0, len(declarations)+1)
	seenIDs, seenNames := map[string]bool{}, map[string]bool{}
	for index, declaration := range declarations {
		if strings.TrimSpace(declaration.ID) == "" || declaration.ID != strings.TrimSpace(declaration.ID) || seenIDs[declaration.ID] {
			return nil, fmt.Errorf("construction source column %d has an empty, untrimmed, or duplicate ID %q", index, declaration.ID)
		}
		if strings.TrimSpace(declaration.Name) == "" || seenNames[declaration.Name] {
			return nil, fmt.Errorf("construction source column %d has an empty or duplicate resolved public name %q", index, declaration.Name)
		}
		compiled, exists := schemaByName[declaration.Name]
		if !exists {
			return nil, fmt.Errorf("construction source column %q does not match an exact public source output", declaration.Name)
		}
		if _, exists := projections[declaration.Name]; !exists {
			return nil, fmt.Errorf("construction source column %q has no public physical projection", declaration.Name)
		}
		compiled.ID, compiled.Name = declaration.ID, declaration.Name
		compiled.Label = loomtext.FirstNonBlank(declaration.Label, compiled.Label, declaration.Name)
		compiled.SourceChild = lineage.CloneSourceChild(declaration.SourceChild)
		resolved = append(resolved, compiled)
		seenIDs[declaration.ID], seenNames[declaration.Name] = true, true
	}
	if len(seenNames) != len(schemaByName) {
		return nil, fmt.Errorf("construction source schema for output %q omits a resolved public source column", outputName)
	}
	for name, column := range schemaByName {
		if !seenNames[name] {
			return nil, fmt.Errorf("construction source schema does not map public source column %q (logical type %s)", name, column.Kind)
		}
	}
	identity := constructionSourceIdentity(schema)
	if identity == "" {
		return nil, fmt.Errorf("construction source has no supported row identity projection")
	}
	identityColumn, ok := schemaColumn(schema, identity)
	if !ok {
		return nil, fmt.Errorf("construction source row identity %q is missing from its compiled schema", identity)
	}
	identityColumn.ID = identityColumn.Name
	identityColumn.Internal, identityColumn.Identity = true, true
	resolved = append(resolved, identityColumn)
	if dynamicKeys, ok := schemaColumn(schema, "__loom_dynamic_runtime_keys"); ok && dynamicKeys.Internal {
		dynamicKeys.ID = dynamicKeys.Name
		dynamicKeys.Identity = false
		resolved = append(resolved, dynamicKeys)
	}
	if identity != "_key" {
		if rootKey, ok := schemaColumn(schema, "_key"); ok && rootKey.Internal && rootKey.Identity &&
			rootKey.Kind == string(expression.KindString) && rootKey.Cardinality == string(expression.RequiredOne) {
			rootKey.ID, rootKey.Identity = "_key", false
			resolved = append(resolved, rootKey)
		}
	}
	for _, column := range resolved {
		if column.Internal || column.PresenceCompanionName == "" {
			continue
		}
		if companion, found := sourcePresenceCompanionColumn(schema, column.PresenceCompanionName); found {
			resolved = append(resolved, companion)
		} else {
			return nil, fmt.Errorf("construction source column %q has no compiled presence companion", column.ID)
		}
	}
	return resolved, nil
}

func preserveDynamicRuntimeKeys(result constructionStageResult, inputSchema []CompiledOutputColumn) constructionStageResult {
	for _, column := range inputSchema {
		if !column.Internal || column.Name != "__loom_dynamic_runtime_keys" {
			continue
		}
		if _, exists := schemaColumn(result.schema, column.Name); !exists {
			result.schema = append(result.schema, column)
		}
		if !hasPhysicalStageColumn(result.physical.OutputColumns, column.Name) {
			result.physical.OutputColumns = append(result.physical.OutputColumns, toPhysicalStageColumns([]CompiledOutputColumn{column})...)
		}
		if !hasPhysicalProjectionName(result.physical.OutputProjections, column.Name) {
			result.physical.OutputProjections = append(result.physical.OutputProjections, ir.PhysicalProjection{
				Name: column.Name, Hidden: true,
				Value: ir.PhysicalValue{Variable: result.physical.InputRowVariable, Path: []string{column.Name}},
			})
		}
		result.descriptor.Columns = cloneCompiledSchema(result.schema)
		break
	}
	return result
}

func hasPhysicalStageColumn(columns []ir.PhysicalStageColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func constructionSourceIdentity(schema []CompiledOutputColumn) string {
	for _, preferred := range []string{constructionRowID, sourceExpansionIdentity, "_key"} {
		if column, ok := schemaColumn(schema, preferred); ok && column.Internal && column.Identity {
			return preferred
		}
	}
	return ""
}

func constructionPivotCategoryColumnIDs(construction recipe.Construction) map[string]bool {
	ids := make(map[string]bool)
	for _, step := range construction.Steps {
		if step.Operation.Kind == recipe.ConstructionPivotOp && step.Operation.Pivot != nil {

			ids[step.Operation.Pivot.CategoryColumnID] = true
		}
	}
	return ids
}

func constructionPresenceCompanionName(outputName, columnID string) string {
	digest := sha256.Sum256([]byte(outputName + ":" + columnID))
	return "__loom_presence_" + hex.EncodeToString(digest[:8])
}

func attachConstructionSourcePresenceCompanions(plan *ir.PhysicalPlan, schema []CompiledOutputColumn, categoryIDs map[string]bool) []CompiledOutputColumn {
	if plan == nil || len(categoryIDs) == 0 {
		return schema
	}
	projections := map[string]ir.PhysicalProjection{}
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnIndex = index
		for _, projection := range operation.Return.Projections {
			if !projection.Hidden {
				projections[projection.Name] = projection
			}
		}
	}
	if returnIndex < 0 {
		return schema
	}
	result := append([]CompiledOutputColumn(nil), schema...)
	for index := range result {
		column := &result[index]
		if column.Internal || !categoryIDs[column.ID] {
			continue
		}
		projection, found := projections[column.Name]
		if !found || projection.Presence == nil {
			continue
		}
		companion := constructionPresenceCompanionName("source", column.ID)
		column.PresenceCompanionName = companion
		companionID := "presence:" + column.ID
		result = append(result, CompiledOutputColumn{
			ID: companionID, Name: companion, Label: companion, SemanticPath: "construction_presence:" + column.ID,
			Kind: string(expression.KindBoolean), Cardinality: string(expression.RequiredOne), Internal: true,
		})
		presence := *projection.Presence
		presence.Paths = cloneConstructionPresencePaths(projection.Presence.Paths)
		plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections, ir.PhysicalProjection{
			Name: companion, Hidden: true, Presence: &presence, PresenceOutput: true,
		})
	}
	return result
}

func cloneConstructionPresencePaths(paths [][]string) [][]string {
	cloned := make([][]string, len(paths))
	for index := range paths {
		cloned[index] = append([]string(nil), paths[index]...)
	}
	return cloned
}

func carryConstructionPresenceCompanions(input, output []CompiledOutputColumn, projections []ir.PhysicalProjection, inputRow string) ([]CompiledOutputColumn, []ir.PhysicalProjection) {
	inputByID := compiledSchemaByID(input)
	output = append([]CompiledOutputColumn(nil), output...)
	projections = append([]ir.PhysicalProjection(nil), projections...)
	for _, column := range output {
		if column.Internal || column.PresenceCompanionName == "" {
			continue
		}
		companionName := column.PresenceCompanionName
		if _, exists := schemaColumn(output, companionName); !exists {
			if companion, found := schemaColumn(input, companionName); found {
				output = append(output, companion)
			} else {
				output = append(output, CompiledOutputColumn{
					ID: "presence:" + column.ID, Name: companionName, Label: companionName,
					SemanticPath: "construction_presence:" + column.ID,
					Kind:         string(expression.KindBoolean), Cardinality: string(expression.RequiredOne), Internal: true,
				})
			}
		}
		if hasPhysicalProjectionName(projections, companionName) {
			continue
		}
		prior, found := inputByID[column.ID]
		if !found || prior.PresenceCompanionName != companionName {
			continue
		}
		presenceProjection := ir.PhysicalProjection{
			Name: companionName, Hidden: true,
			Value: ir.PhysicalValue{Variable: inputRow, Path: []string{companionName}},
		}

		if hasPhysicalProjectionName(projections, constructionRowID) {
			projections = insertProjectionBeforeIdentity(projections, presenceProjection, constructionRowID)
		} else {
			projections = append(projections, presenceProjection)
		}
	}
	return output, projections
}

func constructionPreservesPresenceCompanions(kind ir.PhysicalStageOperationKind) bool {
	switch kind {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageExpandOp,
		ir.PhysicalStageRelatedExpandOp, ir.PhysicalStageRelatedEligibilityOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func withoutConstructionPresenceCompanions(schema []CompiledOutputColumn) []CompiledOutputColumn {
	result := append([]CompiledOutputColumn(nil), schema...)
	for index := range result {
		result[index].PresenceCompanionName = ""
	}
	return result
}

func hasPhysicalProjectionName(projections []ir.PhysicalProjection, name string) bool {
	for _, projection := range projections {
		if projection.Name == name {
			return true
		}
	}
	return false
}

func schemaColumn(schema []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Name == name {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func sourcePresenceCompanionColumn(schema []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range schema {
		if column.Name == name && column.Internal && column.Kind == string(expression.KindBoolean) &&
			column.Cardinality == string(expression.RequiredOne) {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}
