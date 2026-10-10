package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func appendRecipeTerminalCombine(plan *ir.PhysicalPlan, step recipe.ConstructionStep, workspaceOutputSchemas map[string][]CompiledOutputColumn) ([]CompiledOutputColumn, []CompiledStageDescriptor, string, error) {
	if plan == nil {
		return nil, nil, "", fmt.Errorf("physical plan is required")
	}
	combine := step.Operation.Combine
	if combine == nil {
		return nil, nil, "", fmt.Errorf("terminal combine payload is required")
	}
	physical := ir.PhysicalClickHouseCombine{
		Inputs:      make([]ir.PhysicalCombineInputRef, 0, len(step.Inputs)),
		Keys:        make([]ir.PhysicalCombineKey, 0, len(combine.Keys)),
		Projections: make([]ir.PhysicalCombineProjection, 0, len(combine.Projections)),
		JoinType:    string(combine.JoinType), MembershipMode: string(combine.MembershipMode),
		RightMatchPolicy: string(combine.RightMatchPolicy),
	}
	switch combine.Kind {
	case recipe.ConstructionCombineKeyJoin:
		physical.Kind = ir.PhysicalCombineKeyJoin
	case recipe.ConstructionCombineAppend:
		physical.Kind = ir.PhysicalCombineAppend
	case recipe.ConstructionCombineMembership:
		physical.Kind = ir.PhysicalCombineMembership
	default:
		return nil, nil, "", fmt.Errorf("unsupported combine kind %q", combine.Kind)
	}
	for inputIndex, input := range step.Inputs {
		if input.Kind == recipe.ConstructionWorkspaceOutputInput {
			inputSchema, exists := workspaceOutputSchemas[input.OutputID]
			if !exists || len(inputSchema) == 0 {
				return nil, nil, "", fmt.Errorf("workspace output input %q has no compiler-resolved sibling schema", input.OutputID)
			}
			physical.Inputs = append(physical.Inputs, ir.PhysicalCombineInputRef{WorkspaceOutputID: input.OutputID})
			if err := validateWorkspaceCombineInputSchema(inputIndex, input.OutputID, inputSchema, step.Outputs, combine); err != nil {
				return nil, nil, "", err
			}
			continue
		}
		physical.Inputs = append(physical.Inputs, ir.PhysicalCombineInputRef{TableID: input.TableID, RevisionID: input.RevisionID, OutputID: input.OutputID})
	}
	for _, key := range combine.Keys {
		physical.Keys = append(physical.Keys, ir.PhysicalCombineKey{LeftColumnID: key.LeftColumnID, RightColumnID: key.RightColumnID})
	}
	for _, projection := range combine.Projections {
		physical.Projections = append(physical.Projections, ir.PhysicalCombineProjection{
			OutputColumnID: projection.OutputColumnID, InputIndex: projection.InputIndex, InputColumnID: projection.InputColumnID,
		})
	}

	schema := make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
	schema = append(schema, CompiledOutputColumn{
		Name: constructionRowID, SemanticPath: "loom:row_id",
		Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne),
		Internal: true, Identity: true,
	})
	for _, output := range step.Outputs {
		logicalType, clickHouseType, err := constructionCombineColumnType(output)
		if err != nil {
			return nil, nil, "", fmt.Errorf("combine output %q: %w", output.Name, err)
		}
		if output.Nullable {
			clickHouseType = "Nullable(" + clickHouseType + ")"
		}
		physical.Outputs = append(physical.Outputs, ir.PhysicalCombineOutputColumn{
			ID: output.ID, Name: output.Name, SemanticPath: "combine:" + output.ID,
			LogicalType: logicalType, ClickHouseType: clickHouseType, Nullable: output.Nullable,
		})
		cardinality := expression.RequiredOne
		if output.Nullable {
			cardinality = expression.OptionalOne
		}
		schema = append(schema, CompiledOutputColumn{
			ID: output.ID, Name: output.Name, Label: output.Label, SemanticPath: "combine:" + output.ID,
			Kind: logicalType, Cardinality: string(cardinality), Nullable: output.Nullable,
		})
	}
	if hasWorkspaceCombineInput(step.Inputs) {
		if err := physical.ValidateForWorkspaceCompilation(); err != nil {
			return nil, nil, "", fmt.Errorf("validate terminal workspace-output combine: %w", err)
		}
	} else if err := physical.Validate(); err != nil {
		return nil, nil, "", fmt.Errorf("validate terminal ClickHouse combine: %w", err)
	}
	version := plan.Version
	if version <= 0 {
		version = 1
	}
	*plan = ir.PhysicalPlan{Version: version, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &physical}
	descriptor := CompiledStageDescriptor{
		ID: step.ID, Operation: string(combine.Kind), Columns: cloneCompiledSchema(schema),
		RowIdentityColumn: constructionRowID, Capabilities: stageCapabilities(schema, false),
	}
	return schema, []CompiledStageDescriptor{descriptor}, constructionRowID, nil
}

func hasWorkspaceCombineInput(inputs []recipe.ConstructionInputRef) bool {
	for _, input := range inputs {
		if input.Kind == recipe.ConstructionWorkspaceOutputInput {
			return true
		}
	}
	return false
}

func validateWorkspaceCombineInputSchema(inputIndex int, outputID string, schema []CompiledOutputColumn, outputs []recipe.StageColumn, combine *recipe.ConstructionCombine) error {
	columns := make(map[string]CompiledOutputColumn, len(schema))
	for _, column := range schema {
		if column.ID != "" {
			columns[column.ID] = column
		}
	}
	for keyIndex, key := range combine.Keys {
		columnID := key.LeftColumnID
		if inputIndex == 1 {
			columnID = key.RightColumnID
		}
		if inputIndex > 1 {
			continue
		}
		if _, ok := columns[columnID]; !ok {
			return fmt.Errorf("combine key %d references missing compiler-owned column %q on workspace output %q", keyIndex, columnID, outputID)
		}
	}
	for projectionIndex, projection := range combine.Projections {
		if projection.InputIndex != inputIndex {
			continue
		}
		inputColumn, ok := columns[projection.InputColumnID]
		if !ok {
			return fmt.Errorf("combine projection %d references missing compiler-owned column %q on workspace output input %d", projectionIndex, projection.InputColumnID, inputIndex)
		}
		outputColumn, ok := stageColumnByID(outputs, projection.OutputColumnID)
		if !ok {
			return fmt.Errorf("combine projection %d references missing output column %q", projectionIndex, projection.OutputColumnID)
		}
		logicalType, _, err := constructionCombineColumnType(outputColumn)
		if err != nil {
			return err
		}
		if !strings.EqualFold(inputColumn.Kind, logicalType) {
			return fmt.Errorf("combine output %q declares type %q but workspace output %q column %q has compiler-owned type %q", outputColumn.ID, logicalType, outputID, inputColumn.ID, inputColumn.Kind)
		}
		if inputColumn.Cardinality == string(expression.Many) {
			return fmt.Errorf("combine projection %d cannot consume repeated workspace output column %q", projectionIndex, inputColumn.ID)
		}
		if (inputColumn.Nullable || inputColumn.Cardinality == string(expression.OptionalOne)) && !outputColumn.Nullable {
			return fmt.Errorf("combine output %q must be nullable to preserve workspace input column %q", outputColumn.ID, inputColumn.ID)
		}
	}
	return nil
}

func stageColumnByID(columns []recipe.StageColumn, id string) (recipe.StageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return recipe.StageColumn{}, false
}

func constructionCombineColumnType(column recipe.StageColumn) (string, string, error) {
	var logical, physical string
	switch strings.ToLower(strings.TrimSpace(column.Type)) {
	case "string":
		logical, physical = "string", "String"
	case "code":
		logical, physical = "code", "String"
	case "uuid":
		logical, physical = "uuid", "UUID"
	case "date":
		logical, physical = "date", "Date"
	case "date_time", "date-time", "datetime":
		logical, physical = "date-time", "DateTime64(3)"
	case "boolean":
		logical, physical = "boolean", "Bool"
	case "integer":
		logical, physical = "integer", "Int64"
	case "decimal":
		logical, physical = "decimal", "Float64"
	default:
		return "", "", fmt.Errorf("unsupported concrete scalar type %q", column.Type)
	}
	return logical, physical, nil
}
