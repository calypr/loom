package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func appendRecipeTerminalCombine(plan *ir.PhysicalPlan, step recipe.ConstructionStep) ([]CompiledOutputColumn, []CompiledStageDescriptor, string, error) {
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
	for _, input := range step.Inputs {
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
	if err := physical.Validate(); err != nil {
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
