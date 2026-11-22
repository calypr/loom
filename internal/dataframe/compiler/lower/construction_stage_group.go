package lower

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func (lowerer *constructionLowerer) lowerGroupStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	inputStageID := work.input.stageID
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	rootContributorProvenance := work.input.rootContributorProvenance
	retainRootContributors := work.input.retainRootContributors
	group := step.Operation.Group
	physicalGroup, projections, compiled, err := lowerer.lowerConstructionGroup(
		*group, step.RowValues, step.Outputs, inputByID, inputIdentity,
		inputStageID, rootResourceType, outputByID, index, &work.physical,
	)
	if err != nil {
		return err
	}
	if retainRootContributors && rootContributorProvenance {
		contributor, ok := rootContributorInput(inputSchema, rootResourceType)
		if !ok {
			return fmt.Errorf("Group cannot retain root contributors because the compiler-proven root identity was lost")
		}
		physicalGroup.RootContributorInputColumn = contributor.Name
		physicalGroup.RootContributorInputMany = contributor.Cardinality == string(expression.Many)
		physicalGroup.RootContributorOutputColumn = rootContributorSetColumn
		physicalGroup.RootContributorVariable = allocateConstructionVariable(lowerer.usedVariables, "group_root_contributors", index)
		compiled = append(compiled, CompiledOutputColumn{
			ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
			SemanticPath: "construction_root_contributors:" + group.ConstructionID,
			Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
			RootContributorResourceType: rootResourceType,
		})
		identityProjection := projections[len(projections)-1]
		projections[len(projections)-1] = ir.PhysicalProjection{Name: rootContributorSetColumn, Hidden: true, Value: ir.PhysicalValue{Variable: physicalGroup.RootContributorVariable}}
		projections = append(projections, identityProjection)
	}
	if inventory, ok := schemaColumn(inputSchema, "__loom_dynamic_runtime_keys"); ok && inventory.Internal {
		physicalGroup.DynamicRuntimeKeysVariable = allocateConstructionVariable(lowerer.usedVariables, "group_dynamic_keys", index)
		inventory.Cardinality = string(expression.Many)
		compiled = append(compiled, inventory)
		identityProjection := projections[len(projections)-1]
		projections[len(projections)-1] = ir.PhysicalProjection{
			Name: inventory.Name, Hidden: true, Value: ir.PhysicalValue{Variable: physicalGroup.DynamicRuntimeKeysVariable},
		}
		projections = append(projections, identityProjection)
	}
	work.physical.Kind, work.physical.Group = ir.PhysicalStageGroupOp, &physicalGroup
	work.physical.OutputProjections = projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerCodedGroupStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	codedGroup := step.Operation.CodedGroup
	if codedGroup == nil {
		return fmt.Errorf("coded group payload is required")
	}
	physicalCodedGroup, projections, compiled, err := lowerConstructionCodedGroup(
		lowerer.plan, *codedGroup, step.RowValues, step.Outputs, inputByID, inputIdentity, rootResourceType, lowerer.usedVariables, index,
	)
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.CodedGroup, work.physical.OutputProjections = ir.PhysicalStageCodedGroupOp, &physicalCodedGroup, projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}
