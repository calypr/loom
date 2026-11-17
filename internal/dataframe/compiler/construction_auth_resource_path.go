package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func constructionReshapeAuthPathSupported(sequence *ir.PhysicalStageSequence) bool {
	if sequence == nil || len(sequence.Stages) == 0 || sequence.OutputAuthResourcePathBindKey != "" {
		return false
	}
	reshape := false
	for _, stage := range sequence.Stages {
		switch stage.Kind {
		case ir.PhysicalStagePivotOp, ir.PhysicalStageUnpivotOp:
			reshape = true
		case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp:
		default:
			return false
		}
	}
	return reshape
}

func appendConstructionReshapeAuthPath(physical *ir.PhysicalPlan, rootVariable string) error {
	sequence := physical.StageSequence
	var source *ir.PhysicalReturn
	reserved := []string{"auth_resource_path"}
	for index := range physical.Operations {
		if physical.Operations[index].Return != nil {
			source = physical.Operations[index].Return
		}
	}
	if source == nil {
		return fmt.Errorf("construction authorization path requires source projections")
	}
	for _, column := range sequence.SourceColumns {
		reserved = append(reserved, column.Name, column.ID)
	}
	for _, stage := range sequence.Stages {
		for _, column := range stage.InputColumns {
			reserved = append(reserved, column.Name, column.ID)
		}
		for _, column := range stage.OutputColumns {
			reserved = append(reserved, column.Name, column.ID)
		}
	}
	for _, column := range sequence.FinalColumns {
		if column.Name == "auth_resource_path" {
			return fmt.Errorf("construction output declares reserved authorization path metadata")
		}
	}
	name := uniqueAuthResourcePathColumn(source.Projections, reserved...)
	column := ir.PhysicalStageColumn{ID: name, Name: name, Kind: "string", Cardinality: "optional_one", Nullable: true, Internal: true}
	source.Projections = append(source.Projections, ir.PhysicalProjection{Name: name, Hidden: true, Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}}})
	sequence.SourceColumns = append(sequence.SourceColumns, column)
	for index := range sequence.Stages {
		stage := &sequence.Stages[index]
		input := ir.PhysicalProjection{Name: name, Hidden: true, Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{name}}}
		stage.InputColumns = append(stage.InputColumns, column)
		stage.OutputColumns = append(stage.OutputColumns, column)
		stage.InputProjections = append(stage.InputProjections, input)
		output := input
		switch stage.Kind {
		case ir.PhysicalStagePivotOp:
			pivot := stage.GroupedPivot
			if pivot == nil {
				return fmt.Errorf("construction pivot lacks authorization path payload")
			}
			pivot.InputProjections = append(pivot.InputProjections, input)
			variable := pivot.GroupRowsVariable + "_auth_resource_path"
			for _, key := range pivot.GroupKeys {
				if key.Variable == variable {
					variable += "_group"
				}
			}
			pivot.GroupKeys = append(pivot.GroupKeys, ir.PhysicalGroupedPivotKey{Column: name, Variable: variable, Kind: "STRING", Hidden: true})
			output.Value = ir.PhysicalValue{Variable: pivot.OutputRowVariable, Path: []string{name}}
		case ir.PhysicalStageUnpivotOp:
			unpivot := stage.Unpivot
			if unpivot == nil {
				return fmt.Errorf("construction unpivot lacks authorization path payload")
			}
			unpivot.InputProjections = append(unpivot.InputProjections, input)
			if len(unpivot.PreservedOutputs) > 0 {
				unpivot.PreservedOutputs = appendUniqueUnpivotOutput(unpivot.PreservedOutputs, ir.PhysicalUnpivotOutput{InputColumn: name, OutputColumn: name})
			}
			output.Value = ir.PhysicalValue{Variable: unpivot.OutputRowVariable, Path: []string{name}}
		}
		stage.OutputProjections = append(stage.OutputProjections, output)
	}
	sequence.FinalColumns = append(sequence.FinalColumns, column)
	sequence.OutputAuthResourcePathColumn = name
	return nil
}
